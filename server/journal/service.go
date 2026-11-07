package journal

import (
	"context"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	journalv1 "github.com/mrsimpson/agentic-office/server/journal/api"
	journalv1connect "github.com/mrsimpson/agentic-office/server/journal/api/journalv1connect"
)

const (
	// DefaultLimit is the number of entries ListEntries returns when the request sets no limit.
	DefaultLimit = 100
	// MaxLimit is the largest number of entries ListEntries returns. A larger limit is reduced.
	MaxLimit = 1000
	// ReservedPrefix starts the command IDs of entries that the server writes itself, for example
	// the minutes at the end of a session. Clients cannot use command IDs with this prefix.
	ReservedPrefix = "office/"
)

// Caller is who makes a call, as the token check (if-token-check) learned it from the token.
type Caller struct {
	// ParticipantID is empty for the token of a stage link.
	ParticipantID string
	Kind          journalv1.AuthorKind
}

// Authorizer returns the caller of a call. It fails with Unauthenticated if the call carries no
// valid token. In office-server it reads the result of the identity token check.
type Authorizer interface {
	Caller(ctx context.Context) (Caller, error)
}

// AuthorizerFunc is a function that is an Authorizer.
type AuthorizerFunc func(ctx context.Context) (Caller, error)

func (f AuthorizerFunc) Caller(ctx context.Context) (Caller, error) { return f(ctx) }

// Service implements the journal API (if-journal-api): JournalService for clients and Record for
// other building blocks inside the server.
type Service struct {
	store Store
	auth  Authorizer
	mu    sync.Mutex // serialises appends
}

var _ journalv1connect.JournalServiceHandler = (*Service)(nil)

// New returns a Service that keeps its entries in store. If auth is nil, every call of a client
// fails with Unauthenticated.
func New(store Store, auth Authorizer) *Service {
	if auth == nil {
		auth = AuthorizerFunc(func(context.Context) (Caller, error) {
			return Caller{}, connect.NewError(connect.CodeUnauthenticated, "no token check configured")
		})
	}
	return &Service{store: store, auth: auth}
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, msg) }

func knownKind(k journalv1.EntryKind) bool {
	_, ok := journalv1.EntryKind_name[int32(k)]
	return ok
}

// AppendEntry appends an entry for the participant of the access token. The server sets the
// author kind, the time and the ID.
func (s *Service) AppendEntry(ctx context.Context, req *journalv1.AppendEntryRequest) (*journalv1.AppendEntryResponse, error) {
	if req.GetAuthorParticipantId() == "" {
		return nil, invalid("author_participant_id is required")
	}
	if strings.HasPrefix(req.GetCommandId(), ReservedPrefix) {
		return nil, invalid("command_id must not start with " + ReservedPrefix)
	}
	c, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetAuthorParticipantId() != c.ParticipantID {
		return nil, connect.NewError(connect.CodePermissionDenied,
			"author_participant_id must be the participant of the access token")
	}
	e, dup, err := s.Record(ctx, &journalv1.JournalEntry{
		CommandId: req.GetCommandId(), Kind: req.GetKind(), Text: req.GetText(),
		AuthorParticipantId: c.ParticipantID, AuthorKind: c.Kind, SessionId: req.GetSessionId(),
	})
	if err != nil {
		return nil, err
	}
	return &journalv1.AppendEntryResponse{Entry: e, Duplicate: dup}, nil
}

// Record appends an entry on behalf of another building block of the server, for example the
// minutes at the end of a session. There is no token check: the caller sets the author and the
// author kind. The service sets the time and the ID.
//
// Record is idempotent by command_id, like AppendEntry: if an entry with the same command_id,
// kind, text, author and session exists, it returns that entry and true. The same command_id
// with other content fails with AlreadyExists.
func (s *Service) Record(_ context.Context, e *journalv1.JournalEntry) (*journalv1.JournalEntry, bool, error) {
	switch {
	case e.GetCommandId() == "":
		return nil, false, invalid("command_id is required")
	case e.GetKind() == journalv1.EntryKind_ENTRY_KIND_UNSPECIFIED:
		return nil, false, invalid("kind is required")
	case !knownKind(e.GetKind()):
		return nil, false, invalid("kind is unknown")
	case strings.TrimSpace(e.GetText()) == "":
		return nil, false, invalid("text is required")
	case e.GetAuthorParticipantId() == "":
		return nil, false, invalid("author_participant_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, err := s.store.ByCommand(e.GetCommandId())
	if err != nil {
		return nil, false, connect.NewError(connect.CodeInternal, "read journal: "+err.Error())
	}
	if prev != nil {
		if prev.GetKind() != e.GetKind() || prev.GetText() != e.GetText() ||
			prev.GetAuthorParticipantId() != e.GetAuthorParticipantId() || prev.GetSessionId() != e.GetSessionId() {
			return nil, false, connect.NewError(connect.CodeAlreadyExists,
				"command_id was already used for another entry")
		}
		return prev, true, nil
	}
	ne := &journalv1.JournalEntry{
		CommandId: e.GetCommandId(), Kind: e.GetKind(), Text: e.GetText(),
		AuthorParticipantId: e.GetAuthorParticipantId(), AuthorKind: e.GetAuthorKind(),
		SessionId: e.GetSessionId(), Time: timestamppb.Now(),
	}
	ne, err = s.store.Append(ne)
	if err != nil {
		return nil, false, connect.NewError(connect.CodeInternal, "append to journal: "+err.Error())
	}
	return ne, false, nil
}

// ListEntries returns the entries after the cursor that match the filter, oldest first.
func (s *Service) ListEntries(ctx context.Context, req *journalv1.ListEntriesRequest) (*journalv1.ListEntriesResponse, error) {
	if !knownKind(req.GetKind()) {
		return nil, invalid("kind is unknown")
	}
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	if limit == 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	entries, err := s.store.List(Filter{After: req.GetAfterEntryId(), Kind: req.GetKind(),
		SessionID: req.GetSessionId(), Limit: limit})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "read journal: "+err.Error())
	}
	return &journalv1.ListEntriesResponse{Entries: entries}, nil
}

// caller returns the participant of the access token. A stage link has no participant.
func (s *Service) caller(ctx context.Context) (Caller, error) {
	c, err := s.auth.Caller(ctx)
	if err != nil {
		return Caller{}, err
	}
	if c.ParticipantID == "" {
		return Caller{}, connect.NewError(connect.CodePermissionDenied, "a stage link cannot use the journal")
	}
	return c, nil
}

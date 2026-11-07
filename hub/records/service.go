package records

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
)

const (
	// DefaultLimit is the number of records a list call returns when the request sets no limit.
	DefaultLimit = 100
	// MaxLimit is the largest number of records a list call returns. A larger limit is reduced.
	MaxLimit = 1000
)

// Directories of the record types in the archive.
const (
	DecisionsDir = "decisions"
	MinutesDir   = "minutes"
	NotesDir     = "notes"
)

// Service implements the decisions, the minutes and the notes (if-decision-api, if-minutes-api,
// if-note-api) for clients, and StoreMinutes for the meeting session.
type Service struct {
	decisions *collection[*decisionv1.Decision]
	minutes   *collection[*minutesv1.Minutes]
	notes     *collection[*notev1.Note]
}

var (
	_ decisionv1connect.DecisionServiceHandler = (*Service)(nil)
	_ minutesv1connect.MinutesServiceHandler   = (*Service)(nil)
	_ notev1connect.NoteServiceHandler         = (*Service)(nil)
)

// Open reads all records from the archive and returns the service that stores new records there.
func Open(ctx context.Context, arch storage.Archive) (*Service, error) {
	d, err := openCollection(ctx, arch, "decision", DecisionsDir, func() *decisionv1.Decision { return &decisionv1.Decision{} },
		func(r *decisionv1.Decision, id uint32) { r.Id = id })
	if err != nil {
		return nil, err
	}
	m, err := openCollection(ctx, arch, "minutes", MinutesDir, func() *minutesv1.Minutes { return &minutesv1.Minutes{} },
		func(r *minutesv1.Minutes, id uint32) { r.Id = id })
	if err != nil {
		return nil, err
	}
	n, err := openCollection(ctx, arch, "note", NotesDir, func() *notev1.Note { return &notev1.Note{} },
		func(r *notev1.Note, id uint32) { r.Id = id })
	if err != nil {
		return nil, err
	}
	return &Service{decisions: d, minutes: m, notes: n}, nil
}

// Counts returns the number of decisions, minutes and notes.
func (s *Service) Counts() (decisions, minutes, notes int) {
	return s.decisions.count(), s.minutes.count(), s.notes.count()
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, msg) }

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// kind is the kind of the caller, from the token.
type kind int

const (
	kindUnknown kind = iota
	kindHuman
	kindAgent
)

// caller returns the participant of the access token and its kind. A stage link has no
// participant.
func caller(ctx context.Context) (string, kind, error) {
	c, ok := tokencheck.CallerFrom(ctx)
	if !ok {
		return "", kindUnknown, connect.NewError(connect.CodeUnauthenticated, "no access token")
	}
	if c.ParticipantID == "" {
		return "", kindUnknown, connect.NewError(connect.CodePermissionDenied, "a stage link cannot use the records")
	}
	switch c.Kind {
	case tokencheck.KindHuman:
		return c.ParticipantID, kindHuman, nil
	case tokencheck.KindAgent:
		return c.ParticipantID, kindAgent, nil
	}
	return c.ParticipantID, kindUnknown, nil
}

// author checks a write of a client: the fields and that the author is the caller.
func author(ctx context.Context, commandID, text, authorID string) (kind, error) {
	switch {
	case commandID == "":
		return 0, invalid("command_id is required")
	case blank(text):
		return 0, invalid("text is required")
	case authorID == "":
		return 0, invalid("author_participant_id is required")
	}
	pid, k, err := caller(ctx)
	if err != nil {
		return 0, err
	}
	if authorID != pid {
		return 0, connect.NewError(connect.CodePermissionDenied,
			"author_participant_id must be the participant of the access token")
	}
	return k, nil
}

// RecordDecision records a decision of the participant of the access token.
func (s *Service) RecordDecision(ctx context.Context, req *decisionv1.RecordDecisionRequest) (*decisionv1.RecordDecisionResponse, error) {
	k, err := author(ctx, req.GetCommandId(), req.GetText(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	ak := map[kind]decisionv1.AuthorKind{kindHuman: decisionv1.AuthorKind_AUTHOR_KIND_HUMAN, kindAgent: decisionv1.AuthorKind_AUTHOR_KIND_AGENT}[k]
	d, dup, err := s.decisions.add(ctx, &decisionv1.Decision{CommandId: req.GetCommandId(), Text: req.GetText(),
		AuthorParticipantId: req.GetAuthorParticipantId(), AuthorKind: ak, SessionId: req.GetSessionId(), Time: timestamppb.Now()},
		func(a, b *decisionv1.Decision) bool {
			return a.GetText() == b.GetText() && a.GetAuthorParticipantId() == b.GetAuthorParticipantId() && a.GetSessionId() == b.GetSessionId()
		}, "Decision", req.GetText())
	if err != nil {
		return nil, err
	}
	return &decisionv1.RecordDecisionResponse{Decision: d, Duplicate: dup}, nil
}

// ListDecisions returns the decisions after the cursor, oldest first.
func (s *Service) ListDecisions(ctx context.Context, req *decisionv1.ListDecisionsRequest) (*decisionv1.ListDecisionsResponse, error) {
	if _, _, err := caller(ctx); err != nil {
		return nil, err
	}
	return &decisionv1.ListDecisionsResponse{Decisions: s.decisions.list(req.GetAfterId(), req.GetSessionId(), req.GetLimit())}, nil
}

// AddNote adds a note of the participant of the access token.
func (s *Service) AddNote(ctx context.Context, req *notev1.AddNoteRequest) (*notev1.AddNoteResponse, error) {
	k, err := author(ctx, req.GetCommandId(), req.GetText(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	ak := map[kind]notev1.AuthorKind{kindHuman: notev1.AuthorKind_AUTHOR_KIND_HUMAN, kindAgent: notev1.AuthorKind_AUTHOR_KIND_AGENT}[k]
	n, dup, err := s.notes.add(ctx, &notev1.Note{CommandId: req.GetCommandId(), Text: req.GetText(),
		AuthorParticipantId: req.GetAuthorParticipantId(), AuthorKind: ak, SessionId: req.GetSessionId(), Time: timestamppb.Now()},
		func(a, b *notev1.Note) bool {
			return a.GetText() == b.GetText() && a.GetAuthorParticipantId() == b.GetAuthorParticipantId() && a.GetSessionId() == b.GetSessionId()
		}, "Note", req.GetText())
	if err != nil {
		return nil, err
	}
	return &notev1.AddNoteResponse{Note: n, Duplicate: dup}, nil
}

// ListNotes returns the notes after the cursor, oldest first.
func (s *Service) ListNotes(ctx context.Context, req *notev1.ListNotesRequest) (*notev1.ListNotesResponse, error) {
	if _, _, err := caller(ctx); err != nil {
		return nil, err
	}
	return &notev1.ListNotesResponse{Notes: s.notes.list(req.GetAfterId(), req.GetSessionId(), req.GetLimit())}, nil
}

// GetMinutes returns the newest stored minutes of a session.
func (s *Service) GetMinutes(ctx context.Context, req *minutesv1.GetMinutesRequest) (*minutesv1.GetMinutesResponse, error) {
	if req.GetSessionId() == "" {
		return nil, invalid("session_id is required")
	}
	if _, _, err := caller(ctx); err != nil {
		return nil, err
	}
	m, ok := s.minutes.latest(req.GetSessionId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, "the session has no stored minutes")
	}
	return &minutesv1.GetMinutesResponse{Minutes: m}, nil
}

// ListMinutes returns the stored minutes after the cursor, oldest first.
func (s *Service) ListMinutes(ctx context.Context, req *minutesv1.ListMinutesRequest) (*minutesv1.ListMinutesResponse, error) {
	if _, _, err := caller(ctx); err != nil {
		return nil, err
	}
	return &minutesv1.ListMinutesResponse{Minutes: s.minutes.list(req.GetAfterId(), req.GetSessionId(), req.GetLimit())}, nil
}

// StoreMinutes stores the minutes of a session on behalf of the meeting session, when the
// session ends (rs-session-ends). There is no token check: the caller sets the author and the
// author kind. The service sets the time and the ID. It returns when the minutes are committed
// to the archive.
//
// StoreMinutes is idempotent by command_id: if minutes with the same command_id, session, text
// and author exist, it returns them and true. The same command_id with other content fails with
// AlreadyExists.
func (s *Service) StoreMinutes(ctx context.Context, m *minutesv1.Minutes) (*minutesv1.Minutes, bool, error) {
	switch {
	case m.GetCommandId() == "":
		return nil, false, invalid("command_id is required")
	case m.GetSessionId() == "":
		return nil, false, invalid("session_id is required")
	case blank(m.GetText()):
		return nil, false, invalid("text is required")
	case m.GetAuthorParticipantId() == "":
		return nil, false, invalid("author_participant_id is required")
	}
	return s.minutes.add(ctx, &minutesv1.Minutes{CommandId: m.GetCommandId(), SessionId: m.GetSessionId(), Text: m.GetText(),
		AuthorParticipantId: m.GetAuthorParticipantId(), AuthorKind: m.GetAuthorKind(), Time: timestamppb.Now()},
		func(a, b *minutesv1.Minutes) bool {
			return a.GetText() == b.GetText() && a.GetAuthorParticipantId() == b.GetAuthorParticipantId() && a.GetSessionId() == b.GetSessionId()
		}, "Minutes", "session "+m.GetSessionId())
}

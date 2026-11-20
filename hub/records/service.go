package records

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/archive/watch"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	"github.com/mrsimpson/agentic-office/hub/records/check"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	"github.com/mrsimpson/agentic-office/hub/session/release"
)

const (
	// DefaultLimit is the number of records a list call returns when the request sets no limit.
	DefaultLimit = 100
	// MaxLimit is the largest number of records a list call returns. A larger limit is reduced.
	MaxLimit = 1000
)

// Directories of the record types in the archive.
const (
	DecisionsDir = check.DecisionsDir
	MinutesDir   = check.MinutesDir
	NotesDir     = check.NotesDir
)

// Options are what the records part of a tenant works with.
type Options struct {
	// Archive is the artifact repository of the tenant (if-archive-storage). Required.
	Archive storage.Archive
	// Watcher reports the changes of the artifact repository (if-archive-watch). Required.
	Watcher watch.Watcher
	// Staging is the staging repository of the tenant (if-archive-storage). Required.
	Staging storage.Staging
	// StagingWatcher reports the changes of the staging repository (if-archive-watch). Required.
	StagingWatcher watch.Watcher
	// Logf logs the files that the part ignores. Nil means log.Printf.
	Logf func(string, ...any)
}

// Service implements the decisions, the minutes and the notes (if-decision-api, if-minutes-api,
// if-note-api) for clients, and StoreMinutes for the meeting session.
type Service struct {
	decisions *collection[*decisionv1.Decision]
	minutes   *collection[*minutesv1.Minutes]
	notes     *collection[*notev1.Note]

	opts     Options
	sessions release.Checker // set by UseSessions
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

var (
	_ decisionv1connect.DecisionServiceHandler = (*Service)(nil)
	_ minutesv1connect.MinutesServiceHandler   = (*Service)(nil)
	_ notev1connect.NoteServiceHandler         = (*Service)(nil)
)

// loader is what Service needs of every collection.
type loader interface {
	loadAll(context.Context, bool) error
	loadAllStaged(context.Context) error
	reload(context.Context, []watch.Change) error
	reloadStage(context.Context, []watch.Change) error
}

func (s *Service) all() []loader { return []loader{s.decisions, s.minutes, s.notes} }

// Open reads all records from the artifact repository and the staging repository and returns the
// service that stores new records there. The service follows both repositories (if-archive-watch)
// until Close: it reloads the record files that a commit or a push changed or added. Call
// UseSessions before the service takes calls.
func Open(ctx context.Context, opts Options) (*Service, error) {
	if opts.Archive == nil || opts.Watcher == nil || opts.Staging == nil || opts.StagingWatcher == nil {
		return nil, errors.New("records: the archive, the staging repository and their watchers are required")
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	// Watch first, then read: a change in between is reported and read again.
	sub, err := opts.Watcher.Watch(DecisionsDir, MinutesDir, NotesDir)
	if err != nil {
		return nil, fmt.Errorf("records: watch the archive: %w", err)
	}
	stageSub, err := opts.StagingWatcher.Watch(DecisionsDir, MinutesDir, NotesDir)
	if err != nil {
		sub.Close()
		return nil, fmt.Errorf("records: watch the staging repository: %w", err)
	}
	logf := opts.Logf
	s := &Service{opts: opts, done: make(chan struct{}),
		decisions: newCollection(opts.Archive, opts.Staging, "decision", "Decision", DecisionsDir, check.SchemaDecision, marshalYAML,
			func() *decisionv1.Decision { return &decisionv1.Decision{} }, logf),
		minutes: newCollection(opts.Archive, opts.Staging, "minutes", "Minutes", MinutesDir, check.SchemaMinutes, marshalYAML,
			func() *minutesv1.Minutes { return &minutesv1.Minutes{} }, logf),
		notes: newCollection(opts.Archive, opts.Staging, "note", "Note", NotesDir, check.SchemaNote, marshalMarkdown,
			func() *notev1.Note { return &notev1.Note{} }, logf)}
	for _, c := range s.all() {
		if err := c.loadAll(ctx, true); err != nil {
			sub.Close()
			stageSub.Close()
			return nil, err
		}
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	go func() {
		s.follow(sub, opts.Watcher, "the archive", func(l loader) func(context.Context, []watch.Change) error { return l.reload },
			func(l loader) error { return l.loadAll(s.ctx, false) })
		done <- struct{}{}
	}()
	go func() {
		s.follow(stageSub, opts.StagingWatcher, "the staging repository",
			func(l loader) func(context.Context, []watch.Change) error { return l.reloadStage },
			func(l loader) error { return l.loadAllStaged(s.ctx) })
		done <- struct{}{}
	}()
	go func() { <-done; <-done; close(s.done) }()
	return s, nil
}

// UseSessions gives the part the release status of the sessions of its tenant
// (if-session-release). Call it before the service takes calls.
func (s *Service) UseSessions(c release.Checker) { s.sessions = c }

// Close stops following the archive. The service still answers calls.
func (s *Service) Close() {
	s.cancel()
	<-s.done
}

// follow reloads the files that a repository reports until Close. A subscription that was
// dropped (the part was too slow) is replaced, and then every file is read again, because
// changes may have been missed.
func (s *Service) follow(sub watch.Subscription, w watch.Watcher, what string,
	reload func(loader) func(context.Context, []watch.Change) error, readAll func(loader) error) {
	for {
		s.pump(sub, reload)
		sub.Close()
		if s.ctx.Err() != nil {
			return
		}
		s.opts.Logf("records: %s dropped the watcher (%v); watching again and reading all records", what, sub.Err())
		var err error
		if sub, err = w.Watch(DecisionsDir, MinutesDir, NotesDir); err != nil {
			s.opts.Logf("records: cannot watch %s again, its changes are not seen until the hub restarts: %v", what, err)
			return
		}
		for _, c := range s.all() {
			if err := readAll(c); err != nil {
				s.opts.Logf("%v", err)
			}
		}
	}
}

func (s *Service) pump(sub watch.Subscription, reload func(loader) func(context.Context, []watch.Change) error) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case changes, ok := <-sub.Changes():
			if !ok {
				return
			}
			for _, c := range s.all() {
				if err := reload(c)(s.ctx, changes); err != nil {
					s.opts.Logf("%v", err)
				}
			}
		}
	}
}

// Counts returns the number of decisions, minutes and notes, released and staged.
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

// caller returns the caller of the access token and its kind. A stage link has no participant.
func caller(ctx context.Context) (tokencheck.Caller, kind, error) {
	c, ok := tokencheck.CallerFrom(ctx)
	if !ok {
		return c, kindUnknown, connect.NewError(connect.CodeUnauthenticated, "no access token")
	}
	if c.ParticipantID == "" {
		return c, kindUnknown, connect.NewError(connect.CodePermissionDenied, "a stage link cannot use the records")
	}
	switch c.Kind {
	case tokencheck.KindHuman:
		return c, kindHuman, nil
	case tokencheck.KindAgent:
		return c, kindAgent, nil
	}
	return c, kindUnknown, nil
}

// author checks a write of a client: the fields and that the author is the caller.
func author(ctx context.Context, commandID, text, authorID string) (tokencheck.Caller, kind, error) {
	switch {
	case commandID == "":
		return tokencheck.Caller{}, 0, invalid("command_id is required")
	case blank(text):
		return tokencheck.Caller{}, 0, invalid("text is required")
	case authorID == "":
		return tokencheck.Caller{}, 0, invalid("author_participant_id is required")
	}
	c, k, err := caller(ctx)
	if err != nil {
		return c, 0, err
	}
	if authorID != c.ParticipantID {
		return c, 0, connect.NewError(connect.CodePermissionDenied,
			"author_participant_id must be the participant of the access token")
	}
	return c, k, nil
}

// storageAuthor is the author of a change: the participant, with its display name when it has
// one.
func storageAuthor(pid, name string) storage.Author {
	if name == "" {
		name = pid
	}
	return storage.ParticipantAuthor(name, pid)
}

// errNotParticipant is the answer for a session that the caller may not name. A session that does
// not exist gets the same answer.
func errNotParticipant() error {
	return connect.NewError(connect.CodePermissionDenied, "only a participant of the session may name it")
}

// tookPart asks the meeting session whether the caller took part in a session.
func (s *Service) tookPart(ctx context.Context, sessionID, pid string) error {
	if s.sessions == nil {
		return connect.NewError(connect.CodeInternal, "records: no release status of the sessions")
	}
	ok, err := s.sessions.TookPart(ctx, sessionID, pid)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "ask the meeting session: "+err.Error())
	}
	if !ok {
		return errNotParticipant()
	}
	return nil
}

// releasing reports an error when the session is released but its staging area is still moving:
// the release status and the area disagree then, so a write waits. It returns errReleased when the
// area is gone or its release is done (the area keeps only what the release kept back).
func (s *Service) releasing(ctx context.Context, sessionID string) error {
	st, err := s.sessions.State(ctx, sessionID)
	if errors.Is(err, release.ErrNoSession) {
		return errNotParticipant()
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, "ask the meeting session: "+err.Error())
	}
	if st.Status != release.Released {
		return nil
	}
	if a, err := s.opts.Staging.Area(ctx, sessionID); err == nil {
		if a.Release != nil && a.Release.Done {
			return errReleased // the area keeps only what the release kept back
		}
		return connect.NewError(connect.CodeUnavailable, "the session is being released; repeat the command")
	} else if !errors.Is(err, storage.ErrNoArea) {
		return connect.NewError(connect.CodeInternal, "read the staging area: "+err.Error())
	}
	return errReleased
}

// errReleased tells route that the session is released.
var errReleased = errors.New("released")

// route returns where a new record that names a session goes (concept-session-release): the
// staging area of the session while it is not released, otherwise the artifact repository.
// Only a participant may name a session. It never runs while the part holds a lock, because the
// meeting session may wait for the part at the same time (StoreMinutes).
func (s *Service) route(ctx context.Context, sessionID string, c tokencheck.Caller) (target, error) {
	author := storageAuthor(c.ParticipantID, c.DisplayName)
	if sessionID == "" {
		return target{author: author}, nil
	}
	if err := s.tookPart(ctx, sessionID, c.ParticipantID); err != nil {
		return target{}, err
	}
	switch err := s.releasing(ctx, sessionID); {
	case errors.Is(err, errReleased):
		return target{author: author}, nil
	case err != nil:
		return target{}, err
	}
	return target{session: sessionID, author: author}, nil
}

// readerOf returns whether the caller may read the staged records of a session: a tenant admin,
// by role, or a reader of the staging area of the session (concept-session-release). It asks each
// area once per call.
func (s *Service) readerOf(ctx context.Context, c tokencheck.Caller) reader {
	if c.TenantRole == tokencheck.RoleTenantAdmin {
		return func(string) bool { return true }
	}
	known := map[string]bool{}
	return func(session string) bool {
		if v, ok := known[session]; ok {
			return v
		}
		a, err := s.opts.Staging.Area(ctx, session)
		if err != nil && !errors.Is(err, storage.ErrNoArea) {
			s.opts.Logf("records: read the readers of session %q: %v", session, err)
		}
		v := err == nil && slices.Contains(a.Readers, c.ParticipantID)
		known[session] = v
		return v
	}
}

// create stores a new record of a client: the stored record of a repeated command at once,
// otherwise at the place that route gives.
func create[T record](ctx context.Context, s *Service, c *collection[T], r T, same func(stored, r T) bool, text string,
	who tokencheck.Caller) (T, bool, error) {
	var zero T
	if prev, isStaged, ok := c.found(r.GetCommandId()); ok {
		if !same(prev, r) {
			return zero, false, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another "+c.kind)
		}
		return shown(prev, isStaged), true, nil
	}
	to, err := s.route(ctx, r.GetSessionId(), who)
	if err != nil {
		return zero, false, err
	}
	return c.add(ctx, r, same, text, to)
}

// edit changes a staged record, or removes it when mutate is nil, for a participant of its
// session. It returns the stored result of a repeated command and true.
func edit[T record](ctx context.Context, s *Service, c *collection[T], who tokencheck.Caller, id uint32, sessionID, cid, digest string,
	mutate func(T) T) (T, bool, error) {
	var zero T
	if prev, ok, err := c.changed(cid, digest); err != nil || ok {
		return prev, ok, err
	}
	if id == 0 {
		return zero, false, invalid("id is required")
	}
	if mutate == nil {
		// A staged record that the release kept back because its path is taken goes on removal.
		if sid := c.namedBy(id); sid != "" {
			kept, err := keptBack(ctx, s.opts.Staging, sid, c.path(id))
			if err != nil {
				return zero, false, err
			}
			if kept {
				if err := s.tookPart(ctx, sid, who.ParticipantID); err != nil {
					return zero, false, connect.NewError(connect.CodePermissionDenied,
						fmt.Sprintf("only a participant of the session of %s %d may remove it", c.kind, id))
				}
				if sessionID != sid {
					return zero, false, connect.NewError(connect.CodeFailedPrecondition,
						fmt.Sprintf("%s %d was not released with session %s; a removal must name that session", c.kind, id, sid))
				}
				return zero, false, c.discard(ctx, id, sid, cid, digest, storageAuthor(who.ParticipantID, who.DisplayName))
			}
		}
	}
	w := c.locate(id)
	switch w.state {
	case 0:
		return zero, false, connect.NewError(connect.CodeNotFound, fmt.Sprintf("there is no %s %d", c.kind, id))
	case entryReleased:
		return zero, false, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("%s %d is released and never changes; record a new one that refers to it", c.kind, id))
	case entryStub:
		return zero, false, connect.NewError(connect.CodeFailedPrecondition, fmt.Sprintf("%s %d is a stub, and no interface changes a stub", c.kind, id))
	}
	if err := s.tookPart(ctx, w.session, who.ParticipantID); err != nil {
		return zero, false, connect.NewError(connect.CodePermissionDenied,
			fmt.Sprintf("only a participant of the session of %s %d may change or remove it", c.kind, id))
	}
	if sessionID != w.session {
		return zero, false, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("%s %d waits for the release of session %s; a change or a removal must name that session", c.kind, id, w.session))
	}
	if w.state == entryRemoved {
		if mutate == nil {
			return zero, true, nil
		}
		return zero, false, connect.NewError(connect.CodeNotFound, fmt.Sprintf("%s %d was removed", c.kind, id))
	}
	switch err := s.releasing(ctx, w.session); {
	case errors.Is(err, errReleased):
		return zero, false, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("%s %d is released and never changes; record a new one that refers to it", c.kind, id))
	case err != nil:
		return zero, false, err
	}
	r, err := c.change(ctx, id, w.session, cid, digest, storageAuthor(who.ParticipantID, who.DisplayName), mutate)
	return r, false, err
}

// keptBack reports whether the area of a session keeps the file at path p because its release
// is done and the path was taken (concept-session-release).
func keptBack(ctx context.Context, st storage.Staging, session, p string) (bool, error) {
	a, err := st.Area(ctx, session)
	if errors.Is(err, storage.ErrNoArea) {
		return false, nil
	}
	if err != nil {
		return false, connect.NewError(connect.CodeInternal, "read the staging area: "+err.Error())
	}
	return a.Release != nil && a.Release.Done && slices.Contains(a.Names, p), nil
}

// digest is the content of a change or a removal, for its idempotency.
func digest(parts ...any) string { return fmt.Sprintf("%q", parts) }

// RecordDecision records a decision of the participant of the access token.
func (s *Service) RecordDecision(ctx context.Context, req *decisionv1.RecordDecisionRequest) (*decisionv1.RecordDecisionResponse, error) {
	who, k, err := author(ctx, req.GetCommandId(), req.GetText(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	ak := map[kind]decisionv1.AuthorKind{kindHuman: decisionv1.AuthorKind_AUTHOR_KIND_HUMAN, kindAgent: decisionv1.AuthorKind_AUTHOR_KIND_AGENT}[k]
	d, dup, err := create(ctx, s, s.decisions, &decisionv1.Decision{CommandId: req.GetCommandId(), Text: req.GetText(),
		AuthorParticipantId: req.GetAuthorParticipantId(), AuthorKind: ak, SessionId: req.GetSessionId(), Time: timestamppb.Now()},
		func(a, b *decisionv1.Decision) bool {
			return a.GetText() == b.GetText() && a.GetAuthorParticipantId() == b.GetAuthorParticipantId() && a.GetSessionId() == b.GetSessionId()
		}, req.GetText(), who)
	if err != nil {
		return nil, err
	}
	return &decisionv1.RecordDecisionResponse{Decision: d, Duplicate: dup}, nil
}

// ChangeDecision changes a staged decision for a participant of its session.
func (s *Service) ChangeDecision(ctx context.Context, req *decisionv1.ChangeDecisionRequest) (*decisionv1.ChangeDecisionResponse, error) {
	who, k, err := author(ctx, req.GetCommandId(), req.GetText(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	ak := map[kind]decisionv1.AuthorKind{kindHuman: decisionv1.AuthorKind_AUTHOR_KIND_HUMAN, kindAgent: decisionv1.AuthorKind_AUTHOR_KIND_AGENT}[k]
	d, dup, err := edit(ctx, s, s.decisions, who, req.GetId(), req.GetSessionId(), req.GetCommandId(),
		digest("change", req.GetId(), req.GetSessionId(), req.GetAuthorParticipantId(), req.GetText()),
		func(d *decisionv1.Decision) *decisionv1.Decision {
			d.Text, d.AuthorParticipantId, d.AuthorKind, d.Time = req.GetText(), req.GetAuthorParticipantId(), ak, timestamppb.Now()
			return d
		})
	if err != nil {
		return nil, err
	}
	return &decisionv1.ChangeDecisionResponse{Decision: d, Duplicate: dup}, nil
}

// RemoveDecision removes a staged decision for a participant of its session.
func (s *Service) RemoveDecision(ctx context.Context, req *decisionv1.RemoveDecisionRequest) (*decisionv1.RemoveDecisionResponse, error) {
	who, _, err := author(ctx, req.GetCommandId(), "-", req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	_, dup, err := edit(ctx, s, s.decisions, who, req.GetId(), req.GetSessionId(), req.GetCommandId(),
		digest("remove", req.GetId(), req.GetSessionId(), req.GetAuthorParticipantId()), nil)
	if err != nil {
		return nil, err
	}
	return &decisionv1.RemoveDecisionResponse{Duplicate: dup}, nil
}

// ListDecisions returns the decisions after the cursor, oldest first.
func (s *Service) ListDecisions(ctx context.Context, req *decisionv1.ListDecisionsRequest) (*decisionv1.ListDecisionsResponse, error) {
	who, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	return &decisionv1.ListDecisionsResponse{Decisions: s.decisions.list(req.GetAfterId(), req.GetSessionId(), req.GetLimit(), s.readerOf(ctx, who))}, nil
}

// AddNote adds a note of the participant of the access token.
func (s *Service) AddNote(ctx context.Context, req *notev1.AddNoteRequest) (*notev1.AddNoteResponse, error) {
	who, k, err := author(ctx, req.GetCommandId(), req.GetText(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	tags, err := noteTags(req.GetTags())
	if err != nil {
		return nil, err
	}
	ak := map[kind]notev1.AuthorKind{kindHuman: notev1.AuthorKind_AUTHOR_KIND_HUMAN, kindAgent: notev1.AuthorKind_AUTHOR_KIND_AGENT}[k]
	n, dup, err := create(ctx, s, s.notes, &notev1.Note{CommandId: req.GetCommandId(), Text: req.GetText(),
		AuthorParticipantId: req.GetAuthorParticipantId(), AuthorKind: ak, SessionId: req.GetSessionId(), Time: timestamppb.Now(), Tags: tags},
		func(a, b *notev1.Note) bool {
			return a.GetText() == b.GetText() && a.GetAuthorParticipantId() == b.GetAuthorParticipantId() && a.GetSessionId() == b.GetSessionId() &&
				slices.Equal(a.GetTags(), b.GetTags())
		}, req.GetText(), who)
	if err != nil {
		return nil, err
	}
	return &notev1.AddNoteResponse{Note: n, Duplicate: dup}, nil
}

// ChangeNote changes a staged note for a participant of its session.
func (s *Service) ChangeNote(ctx context.Context, req *notev1.ChangeNoteRequest) (*notev1.ChangeNoteResponse, error) {
	who, k, err := author(ctx, req.GetCommandId(), req.GetText(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	tags, err := noteTags(req.GetTags())
	if err != nil {
		return nil, err
	}
	ak := map[kind]notev1.AuthorKind{kindHuman: notev1.AuthorKind_AUTHOR_KIND_HUMAN, kindAgent: notev1.AuthorKind_AUTHOR_KIND_AGENT}[k]
	n, dup, err := edit(ctx, s, s.notes, who, req.GetId(), req.GetSessionId(), req.GetCommandId(),
		digest("change", req.GetId(), req.GetSessionId(), req.GetAuthorParticipantId(), req.GetText(), tags),
		func(n *notev1.Note) *notev1.Note {
			n.Text, n.Tags, n.AuthorParticipantId, n.AuthorKind, n.Time = req.GetText(), tags, req.GetAuthorParticipantId(), ak, timestamppb.Now()
			return n
		})
	if err != nil {
		return nil, err
	}
	return &notev1.ChangeNoteResponse{Note: n, Duplicate: dup}, nil
}

// RemoveNote removes a staged note for a participant of its session.
func (s *Service) RemoveNote(ctx context.Context, req *notev1.RemoveNoteRequest) (*notev1.RemoveNoteResponse, error) {
	who, _, err := author(ctx, req.GetCommandId(), "-", req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	_, dup, err := edit(ctx, s, s.notes, who, req.GetId(), req.GetSessionId(), req.GetCommandId(),
		digest("remove", req.GetId(), req.GetSessionId(), req.GetAuthorParticipantId()), nil)
	if err != nil {
		return nil, err
	}
	return &notev1.RemoveNoteResponse{Duplicate: dup}, nil
}

// noteTags returns the tags of a new note: without the spaces around each tag and without
// repeats. A blank tag or a tag with a line break is InvalidArgument.
func noteTags(in []string) ([]string, error) {
	var out []string
	for _, t := range in {
		t = strings.TrimSpace(t)
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if reasons := check.TagProblems(out); len(reasons) > 0 {
		return nil, invalid(strings.Join(reasons, "; "))
	}
	return out, nil
}

// ListNotes returns the notes after the cursor, oldest first.
func (s *Service) ListNotes(ctx context.Context, req *notev1.ListNotesRequest) (*notev1.ListNotesResponse, error) {
	who, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	return &notev1.ListNotesResponse{Notes: s.notes.list(req.GetAfterId(), req.GetSessionId(), req.GetLimit(), s.readerOf(ctx, who))}, nil
}

// GetMinutes returns the newest stored minutes of a session that the caller may read.
func (s *Service) GetMinutes(ctx context.Context, req *minutesv1.GetMinutesRequest) (*minutesv1.GetMinutesResponse, error) {
	if req.GetSessionId() == "" {
		return nil, invalid("session_id is required")
	}
	who, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	m, ok := s.minutes.latest(req.GetSessionId(), s.readerOf(ctx, who))
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, "the session has no stored minutes")
	}
	return &minutesv1.GetMinutesResponse{Minutes: m}, nil
}

// ListMinutes returns the stored minutes after the cursor, oldest first.
func (s *Service) ListMinutes(ctx context.Context, req *minutesv1.ListMinutesRequest) (*minutesv1.ListMinutesResponse, error) {
	who, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	return &minutesv1.ListMinutesResponse{Minutes: s.minutes.list(req.GetAfterId(), req.GetSessionId(), req.GetLimit(), s.readerOf(ctx, who))}, nil
}

// ChangeMinutes changes staged minutes for a participant of their session.
func (s *Service) ChangeMinutes(ctx context.Context, req *minutesv1.ChangeMinutesRequest) (*minutesv1.ChangeMinutesResponse, error) {
	who, k, err := author(ctx, req.GetCommandId(), req.GetText(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	ak := map[kind]minutesv1.AuthorKind{kindHuman: minutesv1.AuthorKind_AUTHOR_KIND_HUMAN, kindAgent: minutesv1.AuthorKind_AUTHOR_KIND_AGENT}[k]
	m, dup, err := edit(ctx, s, s.minutes, who, req.GetId(), req.GetSessionId(), req.GetCommandId(),
		digest("change", req.GetId(), req.GetSessionId(), req.GetAuthorParticipantId(), req.GetText()),
		func(m *minutesv1.Minutes) *minutesv1.Minutes {
			m.Text, m.AuthorParticipantId, m.AuthorKind, m.Time = req.GetText(), req.GetAuthorParticipantId(), ak, timestamppb.Now()
			return m
		})
	if err != nil {
		return nil, err
	}
	return &minutesv1.ChangeMinutesResponse{Minutes: m, Duplicate: dup}, nil
}

// RemoveMinutes removes staged minutes for a participant of their session.
func (s *Service) RemoveMinutes(ctx context.Context, req *minutesv1.RemoveMinutesRequest) (*minutesv1.RemoveMinutesResponse, error) {
	who, _, err := author(ctx, req.GetCommandId(), "-", req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	_, dup, err := edit(ctx, s, s.minutes, who, req.GetId(), req.GetSessionId(), req.GetCommandId(),
		digest("remove", req.GetId(), req.GetSessionId(), req.GetAuthorParticipantId()), nil)
	if err != nil {
		return nil, err
	}
	return &minutesv1.RemoveMinutesResponse{Duplicate: dup}, nil
}

// StoreMinutes stores the minutes of a session on behalf of the meeting session, when the
// session ends (rs-session-ends). There is no token check: the caller sets the author and the
// author kind, and authorName is the display name of the author. The service sets the time and
// the ID. The minutes go into the staging area of the session, and their stub into the artifact
// repository (concept-session-release). It returns when both are committed.
//
// StoreMinutes does not ask the meeting session (if-session-release): the meeting session calls
// it while it ends the session, so the session is not released and the author took part. So the
// two parts never wait for each other.
//
// StoreMinutes is idempotent by command_id: if minutes with the same command_id, session, text
// and author exist, it returns them and true. The same command_id with other content fails with
// AlreadyExists.
func (s *Service) StoreMinutes(ctx context.Context, m *minutesv1.Minutes, authorName string) (*minutesv1.Minutes, bool, error) {
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
		}, "session "+m.GetSessionId(), target{session: m.GetSessionId(), author: storageAuthor(m.GetAuthorParticipantId(), authorName)})
}

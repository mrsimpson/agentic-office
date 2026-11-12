package session

import (
	"context"
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/merkle"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
)

// Caller is who makes a call, as the token check (if-token-check) learned it from the token.
type Caller struct {
	// ParticipantID is the identity of the caller. It is also the participant ID in the session.
	ParticipantID string
	DisplayName   string
	Kind          sessionstreamv1.ParticipantKind
	// StageSessionID is set for the read-only token of a stage link. Such a caller may only
	// subscribe to this session.
	StageSessionID string
	// Done is closed when the token expires or is revoked. An open stream ends then. Nil means
	// never.
	Done <-chan struct{}
}

// Authorizer returns the caller of a call. It fails with Unauthenticated if the call carries no
// valid token. In office-hub it reads the result of the identity token check.
type Authorizer interface {
	Caller(ctx context.Context) (Caller, error)
}

// AuthorizerFunc is a function that is an Authorizer.
type AuthorizerFunc func(ctx context.Context) (Caller, error)

func (f AuthorizerFunc) Caller(ctx context.Context) (Caller, error) { return f(ctx) }

type denyAll struct{}

func (denyAll) Caller(context.Context) (Caller, error) {
	return Caller{}, connect.NewError(connect.CodeUnauthenticated, "no token check configured")
}

// Minutes is the part of the minutes (if-minutes-api) that the session needs: it stores the
// minutes when a session ends (rs-session-ends). records.Service implements it.
type Minutes interface {
	// StoreMinutes stores the minutes of a session. It is idempotent by the command ID of the
	// minutes: a repeated call returns the stored minutes and true.
	StoreMinutes(ctx context.Context, m *minutesv1.Minutes) (*minutesv1.Minutes, bool, error)
}

// MinutesCommandPrefix starts the command ID of the minutes that the session stores. The rest is
// the command ID of the EndSession command.
const MinutesCommandPrefix = "office/session-end/"

// Service implements the session commands and the session stream.
type Service struct {
	store   Store
	auth    Authorizer
	minutes Minutes
	key     ed25519.PrivateKey // signs tree heads (dec-merkle-log)

	mu       sync.Mutex
	sessions map[string]*state

	// The archive (dec-session-persistence), set by UseArchive. See RunArchive.
	arch   *archiver
	archMu sync.Mutex
	dirty  map[string]bool // sessions with events that are not in the archive yet
	wake   chan struct{}   // wakes the writer when a session ends
}

var (
	_ sessioncommandsv1connect.SessionCommandsServiceHandler = (*Service)(nil)
	_ sessionstreamv1connect.SessionStreamServiceHandler     = (*Service)(nil)
)

// New returns a Service that keeps its logs in store and stores the minutes of ended sessions
// with minutes. It signs tree heads with key. If auth is nil, every call fails with
// Unauthenticated. If minutes is nil, the minutes are not stored. key is required: New panics without it
// (dec-hub-signing-key).
func New(store Store, auth Authorizer, minutes Minutes, key ed25519.PrivateKey) *Service {
	if auth == nil {
		auth = denyAll{}
	}
	if len(key) != ed25519.PrivateKeySize {
		panic("session: no signing key")
	}
	return &Service{store: store, auth: auth, minutes: minutes, key: key, sessions: map[string]*state{}}
}

// UseArchive makes the service write its sessions to an archive, and restores the sessions that
// are stored there (dec-restore-from-peers, see restore). Call it before the service takes
// commands, then run RunArchive.
func (s *Service) UseArchive(ctx context.Context, opts ArchiveOptions) error {
	if opts.Archive == nil {
		return errors.New("session: no archive")
	}
	if opts.Interval <= 0 {
		return errors.New("session: the archive interval must be positive")
	}
	if opts.RestoreWait < 0 {
		return errors.New("session: the restore wait must not be negative")
	}
	s.arch = &archiver{opts: opts, lines: map[string]*archivedLog{}, committed: map[string]int{}}
	s.dirty = map[string]bool{}
	s.wake = make(chan struct{}, 1)
	return s.restore(ctx)
}

// state is what the service knows about one session. It is derived from the log only, so it can
// be rebuilt from the store at any time.
type state struct {
	mu     sync.Mutex // serialises the commands of the session
	loaded bool
	// schema is the schema of the session (dec-session-schema): sessionstreamv1.Schema for a session
	// this hub started, the schema of the stored tree head for a restored one. The hub continues
	// only sessions of its own schema; see checkSchema.
	schema       string
	lastID       uint32
	ended        bool
	participants map[string]*participant
	utterances   map[uint32]*utterance  // utterance.final event ID -> utterance
	minutes      *sessionstreamv1.Event // the last minutes.updated event
	// An imported session (dec-session-import, import.go): importer is the participant who sent
	// the first page; importing is true until import.completed.
	importer  string
	importing bool
	imported  uint32 // number of imported utterances
	// leaves are the leaf hashes of the Merkle tree: leaves[i] belongs to event i+1. The slice
	// only grows; its elements never change.
	leaves [][]byte
	// frontier holds the roots of the perfect subtrees of the tree over leaves, largest first, so
	// the root hash of the current log takes O(log n) hashes per event.
	frontier [][]byte
	// head is the signed tree head of the current log, once it was needed; nil after a new event.
	head *sessionstreamv1.SignedTreeHead

	// After a restart (dec-restore-from-peers): restoring is set while the hub waits for a peer
	// to fill in the events after the last write to the archive. Commands wait until
	// restoreUntil, or until a peer's copy was accepted. broken is set if the stored log did not
	// verify; then commands fail until a peer offers a copy.
	restoring    bool
	restoreUntil time.Time
	broken       string

	// refs counts the calls that read the log of the session outside st.mu, such as an open
	// stream. A session that is read leaves memory never (dec-session-eviction). Changed under
	// st.mu only.
	refs int
	// evicted is set when the session left memory. The state is not used any more: a call that
	// finds it looks the session up again (see lock).
	evicted bool
}

type participant struct {
	joined *sessionstreamv1.Event // the participant.joined event of the current membership
	left   bool
	paused bool
}

type utterance struct {
	speaker      string
	offTheRecord bool
}

// apply updates the state with one event of the log. It fails only if the event cannot be a leaf
// of the Merkle tree, for example a placeholder in the log.
func (st *state) apply(ev *sessionstreamv1.Event) error {
	if ev.GetId() != uint32(len(st.leaves)+1) {
		return connect.NewError(connect.CodeInternal, "session log has a gap")
	}
	h, err := merkle.EventLeafHash(ev)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "leaf hash of an event: "+err.Error())
	}
	st.pushLeaf(h)
	st.lastID = ev.GetId()
	switch p := ev.GetPayload().(type) {
	case *sessionstreamv1.Event_ParticipantJoined:
		st.participants[p.ParticipantJoined.GetParticipantId()] = &participant{joined: ev}
	case *sessionstreamv1.Event_ParticipantLeft:
		if pt := st.participants[p.ParticipantLeft.GetParticipantId()]; pt != nil {
			pt.left = true
		}
	case *sessionstreamv1.Event_ParticipantPaused:
		if pt := st.participants[p.ParticipantPaused.GetParticipantId()]; pt != nil {
			pt.paused = true
		}
	case *sessionstreamv1.Event_ParticipantResumed:
		if pt := st.participants[p.ParticipantResumed.GetParticipantId()]; pt != nil {
			pt.paused = false
		}
	case *sessionstreamv1.Event_UtteranceFinal:
		st.utterances[ev.GetId()] = &utterance{speaker: p.UtteranceFinal.GetParticipantId()}
	case *sessionstreamv1.Event_UtteranceOffTheRecord:
		if u := st.utterances[p.UtteranceOffTheRecord.GetUtteranceEventId()]; u != nil {
			u.offTheRecord = true
		}
	case *sessionstreamv1.Event_MinutesUpdated:
		st.minutes = ev
	case *sessionstreamv1.Event_SessionEnded:
		st.ended = true
	case *sessionstreamv1.Event_SessionImported:
		st.importer, st.importing = ev.GetActorParticipantId(), true
	case *sessionstreamv1.Event_ImportCompleted:
		st.importing = false
	}
	if ev.GetUtteranceFinal() != nil && st.importing {
		st.imported++
	}
	return nil
}

// finished reports whether the session is over: it has ended, or it is an imported conversation
// that is complete. Such a session may leave memory (dec-session-eviction). The caller holds st.mu.
func (st *state) finished() bool {
	return st.ended || (st.importer != "" && !st.importing)
}

// newState returns the empty state of a session of a schema.
func newState(schema string) *state {
	return &state{schema: schema, participants: map[string]*participant{}, utterances: map[uint32]*utterance{}}
}

// checkSchema fails for a session of another schema than the one of this hub
// (dec-session-schema): the hub serves its log but does not continue it. The caller holds st.mu.
func (st *state) checkSchema() error {
	if st.schema == sessionstreamv1.Schema {
		return nil
	}
	return precondition(schemaMismatch(st.schema))
}

func schemaMismatch(schema string) string {
	return "the session has schema \"" + schema + "\"; this hub has schema \"" + sessionstreamv1.Schema +
		"\" and does not continue a session of another schema"
}

// lock returns the locked state of a session. It loads the state from the store on first use.
// The caller must unlock st.mu.
//
// A session that is not in memory is read from the store; if the store has no log of it, from the
// archive (dec-session-eviction): an ended session left memory when it was archived and nobody
// followed it. Only then a session is new.
func (s *Service) lock(sessionID string) (*state, error) {
	for {
		s.mu.Lock()
		st, ok := s.sessions[sessionID]
		if !ok {
			st = newState(sessionstreamv1.Schema)
			s.sessions[sessionID] = st
		}
		s.mu.Unlock()
		st.mu.Lock()
		if st.evicted {
			// The session left memory between the lookup and the lock: look it up again.
			st.mu.Unlock()
			continue
		}
		if !st.loaded {
			if err := s.load(sessionID, st); err != nil {
				st.mu.Unlock()
				return nil, err
			}
			st.loaded = true
		}
		return st, nil
	}
}

// load fills the state of a session that is not in memory: from the log in the store, else from
// the archive. The caller holds st.mu.
func (s *Service) load(sessionID string, st *state) error {
	evs, _, err := s.store.Read(sessionID, 0)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "read session log: "+err.Error())
	}
	if len(evs) == 0 && s.arch != nil {
		return s.loadStored(context.Background(), sessionID, st)
	}
	for _, ev := range evs {
		if err := st.apply(ev); err != nil {
			return err
		}
	}
	return nil
}

func invalid(msg string) error  { return connect.NewError(connect.CodeInvalidArgument, msg) }
func notFound(msg string) error { return connect.NewError(connect.CodeNotFound, msg) }
func precondition(msg string) error {
	return connect.NewError(connect.CodeFailedPrecondition, msg)
}

// command is one session command. build checks the command against the state and returns the
// event with its payload. The service fills in the other fields.
type command struct {
	sessionID     string
	commandID     string
	participantID string // empty for join: the participant of the token joins
	// types are the event types this command can create. A repeated command_id must match one.
	types []string
	// check, if set, checks the command against the caller before anything else.
	check func(Caller) error
	build func(st *state, c Caller) (*sessionstreamv1.Event, error)
}

// run carries out a command once (dec-idempotent-commands).
//
// Order of checks: required fields, caller (token, participant_id), repeated command_id, end of
// session, participant, then the checks of the command itself.
func (s *Service) run(ctx context.Context, c command, isJoin bool) (*sessioncommandsv1.CommandResult, error) {
	if c.sessionID == "" {
		return nil, invalid("session_id is required")
	}
	if c.commandID == "" {
		return nil, invalid("command_id is required")
	}
	if !isJoin && c.participantID == "" {
		return nil, invalid("participant_id is required")
	}
	caller, err := s.auth.Caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.ParticipantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, "a stage link cannot send commands")
	}
	if isJoin {
		c.participantID = caller.ParticipantID
	} else if c.participantID != caller.ParticipantID {
		return nil, connect.NewError(connect.CodePermissionDenied,
			"participant_id must be the participant of the access token")
	}
	if c.check != nil {
		if err := c.check(caller); err != nil {
			return nil, err
		}
	}
	st, err := s.lock(c.sessionID)
	if err != nil {
		return nil, err
	}
	defer st.mu.Unlock()

	prev, err := s.store.ByCommand(c.sessionID, c.commandID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "read session log: "+err.Error())
	}
	if prev != nil {
		if !slices.Contains(c.types, prev.GetType()) || prev.GetActorParticipantId() != c.participantID {
			return nil, connect.NewError(connect.CodeAlreadyExists,
				"command_id was already used for another command")
		}
		return &sessioncommandsv1.CommandResult{Event: prev, Duplicate: true}, nil
	}
	if err := st.checkSchema(); err != nil {
		return nil, err
	}
	if err := st.checkRestored(); err != nil {
		return nil, err
	}
	if st.importing {
		return nil, precondition("the session is being imported; it takes session commands after the import is complete")
	}
	if st.ended {
		return nil, precondition("the session has ended")
	}
	pt := st.participants[c.participantID]
	if isJoin {
		// A participant joins once. Another join while it is in the session (for example from a
		// second client of the same participant) returns the first join.
		if pt != nil && !pt.left {
			return &sessioncommandsv1.CommandResult{Event: pt.joined, Duplicate: true}, nil
		}
	} else {
		if pt == nil {
			return nil, notFound("participant_id is not a participant of this session")
		}
		if pt.left {
			return nil, precondition("the participant has left the session")
		}
	}
	ev, err := c.build(st, caller)
	if err != nil {
		return nil, err
	}
	ev.SessionId = c.sessionID
	ev.CommandId = c.commandID
	ev.ActorParticipantId = c.participantID
	ev.Type = events.TypeOf(ev)
	ev.Time = timestamppb.Now()
	ev, err = s.store.Append(c.sessionID, ev)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "append to session log: "+err.Error())
	}
	if err := st.apply(ev); err != nil {
		return nil, err
	}
	s.markDirty(c.sessionID, ev.GetType())
	return &sessioncommandsv1.CommandResult{Event: ev}, nil
}

// knownParticipant checks a participant ID that a command names in its payload.
func knownParticipant(st *state, id, field string) error {
	if id == "" {
		return invalid(field + " is required")
	}
	if st.participants[id] == nil {
		return notFound(field + " is not a participant of this session")
	}
	return nil
}

// knownUtterance checks an utterance.final event ID that a command names.
func knownUtterance(st *state, id uint32) (*utterance, error) {
	if id == 0 {
		return nil, invalid("utterance_event_id is required")
	}
	u := st.utterances[id]
	if u == nil {
		return nil, notFound("utterance_event_id is not an utterance.final event of this session")
	}
	return u, nil
}

// Join adds the participant of the access token to the session. display_name defaults to the
// registered name. kind is optional; if set, it must be the registered kind.
func (s *Service) Join(ctx context.Context, req *sessioncommandsv1.JoinRequest) (*sessioncommandsv1.JoinResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(),
		types: []string{events.ParticipantJoined},
		check: func(c Caller) error {
			if k := req.GetKind(); k != sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_UNSPECIFIED && k != c.Kind {
				return connect.NewError(connect.CodePermissionDenied, "kind is not the registered kind of the participant")
			}
			return nil
		},
		build: func(_ *state, c Caller) (*sessionstreamv1.Event, error) {
			name := req.GetDisplayName()
			if name == "" {
				name = c.DisplayName
			}
			return &sessionstreamv1.Event{
				Payload: &sessionstreamv1.Event_ParticipantJoined{ParticipantJoined: &sessionstreamv1.ParticipantJoined{
					ParticipantId: c.ParticipantID, DisplayName: name, Kind: c.Kind,
					Captured: req.GetCaptured(),
				}},
			}, nil
		},
	}, true)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.JoinResponse{
		ParticipantId: res.GetEvent().GetParticipantJoined().GetParticipantId(), Result: res,
	}, nil
}

func (s *Service) Leave(ctx context.Context, req *sessioncommandsv1.LeaveRequest) (*sessioncommandsv1.LeaveResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.ParticipantLeft},
		build: func(*state, Caller) (*sessionstreamv1.Event, error) {
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_ParticipantLeft{
				ParticipantLeft: &sessionstreamv1.ParticipantLeft{ParticipantId: req.GetParticipantId()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.LeaveResponse{Result: res}, nil
}

func (s *Service) SubmitUtterance(ctx context.Context, req *sessioncommandsv1.SubmitUtteranceRequest) (*sessioncommandsv1.SubmitUtteranceResponse, error) {
	if req.GetText() == "" {
		return nil, invalid("text is required")
	}
	if req.GetModality() == sessionstreamv1.Modality_MODALITY_UNSPECIFIED {
		return nil, invalid("modality is required")
	}
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.UtteranceFinal},
		build: func(*state, Caller) (*sessionstreamv1.Event, error) {
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_UtteranceFinal{UtteranceFinal: &sessionstreamv1.UtteranceFinal{
				ParticipantId: req.GetParticipantId(), Text: req.GetText(), Modality: req.GetModality(),
				TranscribedAt: req.GetTranscribedAt(),
			}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.SubmitUtteranceResponse{Result: res}, nil
}

// MarkOffTheRecord appends utterance.off_the_record (dec-off-the-record). Only the speaker can mark
// an utterance off the record, and each one only once. A moderator role is out of scope (#31).
func (s *Service) MarkOffTheRecord(ctx context.Context, req *sessioncommandsv1.MarkOffTheRecordRequest) (*sessioncommandsv1.MarkOffTheRecordResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.UtteranceOffTheRecord},
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			u, err := knownUtterance(st, req.GetUtteranceEventId())
			if err != nil {
				return nil, err
			}
			if u.speaker != req.GetParticipantId() {
				return nil, connect.NewError(connect.CodePermissionDenied, "only the speaker can mark an utterance off the record")
			}
			if u.offTheRecord {
				return nil, precondition("the utterance is already off the record")
			}
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_UtteranceOffTheRecord{UtteranceOffTheRecord: &sessionstreamv1.UtteranceOffTheRecord{
				UtteranceEventId: req.GetUtteranceEventId(),
			}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.MarkOffTheRecordResponse{Result: res}, nil
}

// SubmitSignal appends turn.end, silence.long or addressed.
//
// The session does not create signals on its own; it only checks and stores the signals that
// clients submit. In particular it does not search utterances for names to create addressed.
// We chose this because it is the easiest to change: if the session should later detect
// addressed itself (#14), that adds events with an empty command_id and keeps this command
// valid. The reverse change would remove events that clients already rely on.
func (s *Service) SubmitSignal(ctx context.Context, req *sessioncommandsv1.SubmitSignalRequest) (*sessioncommandsv1.SubmitSignalResponse, error) {
	if req.GetSignal() == nil {
		return nil, invalid("one of turn_end, silence_long or addressed is required")
	}
	if sl := req.GetSilenceLong(); sl != nil && (sl.GetDuration() == nil || sl.GetDuration().AsDuration() <= 0) {
		return nil, invalid("silence_long.duration must be positive")
	}
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.TurnEnd, events.SilenceLong, events.Addressed},
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			switch sig := req.GetSignal().(type) {
			case *sessioncommandsv1.SubmitSignalRequest_TurnEnd:
				if err := knownParticipant(st, sig.TurnEnd.GetParticipantId(), "turn_end.participant_id"); err != nil {
					return nil, err
				}
				return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_TurnEnd{TurnEnd: sig.TurnEnd}}, nil
			case *sessioncommandsv1.SubmitSignalRequest_SilenceLong:
				return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_SilenceLong{SilenceLong: sig.SilenceLong}}, nil
			case *sessioncommandsv1.SubmitSignalRequest_Addressed:
				if err := knownParticipant(st, sig.Addressed.GetParticipantId(), "addressed.participant_id"); err != nil {
					return nil, err
				}
				if _, err := knownUtterance(st, sig.Addressed.GetUtteranceEventId()); err != nil {
					return nil, err
				}
				return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_Addressed{Addressed: sig.Addressed}}, nil
			}
			return nil, invalid("unknown signal")
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.SubmitSignalResponse{Result: res}, nil
}

func (s *Service) Pause(ctx context.Context, req *sessioncommandsv1.PauseRequest) (*sessioncommandsv1.PauseResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.ParticipantPaused},
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			if st.participants[req.GetParticipantId()].paused {
				return nil, precondition("the participant is already paused")
			}
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_ParticipantPaused{
				ParticipantPaused: &sessionstreamv1.ParticipantPaused{ParticipantId: req.GetParticipantId()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.PauseResponse{Result: res}, nil
}

func (s *Service) Resume(ctx context.Context, req *sessioncommandsv1.ResumeRequest) (*sessioncommandsv1.ResumeResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.ParticipantResumed},
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			if !st.participants[req.GetParticipantId()].paused {
				return nil, precondition("the participant is not paused")
			}
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_ParticipantResumed{
				ParticipantResumed: &sessionstreamv1.ParticipantResumed{ParticipantId: req.GetParticipantId()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.ResumeResponse{Result: res}, nil
}

func (s *Service) RaiseHand(ctx context.Context, req *sessioncommandsv1.RaiseHandRequest) (*sessioncommandsv1.RaiseHandResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.HandRaised},
		build: func(*state, Caller) (*sessionstreamv1.Event, error) {
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_HandRaised{HandRaised: &sessionstreamv1.HandRaised{
				ParticipantId: req.GetParticipantId(), Topic: req.GetTopic()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.RaiseHandResponse{Result: res}, nil
}

func (s *Service) UpdateMinutes(ctx context.Context, req *sessioncommandsv1.UpdateMinutesRequest) (*sessioncommandsv1.UpdateMinutesResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.MinutesUpdated},
		build: func(*state, Caller) (*sessionstreamv1.Event, error) {
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_MinutesUpdated{
				MinutesUpdated: &sessionstreamv1.MinutesUpdated{Text: req.GetText()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.UpdateMinutesResponse{Result: res}, nil
}

// FocusArtifact appends artifact.focused. It does not check that the artifact exists: the session is
// only the log of what participants did, and the stage handles an unknown ID.
func (s *Service) FocusArtifact(ctx context.Context, req *sessioncommandsv1.FocusArtifactRequest) (*sessioncommandsv1.FocusArtifactResponse, error) {
	if req.GetArtifactId() == "" {
		return nil, invalid("artifact_id is required")
	}
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.ArtifactFocused},
		build: func(*state, Caller) (*sessionstreamv1.Event, error) {
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_ArtifactFocused{
				ArtifactFocused: &sessionstreamv1.ArtifactFocused{ArtifactId: req.GetArtifactId()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.FocusArtifactResponse{Result: res}, nil
}

// NoteArtifact appends artifact.created or artifact.changed for the participant of the access token
// in ctx (dec-artifacts). The parts of the hub that write artifacts call it in-process after the
// write, with the context of the caller. The participant must have joined the session. It is
// idempotent by command_id like every command.
func (s *Service) NoteArtifact(ctx context.Context, sessionID, participantID, commandID string, created bool, path, version string) error {
	typ := events.ArtifactChanged
	if created {
		typ = events.ArtifactCreated
	}
	_, err := s.run(ctx, command{
		sessionID: sessionID, commandID: commandID, participantID: participantID, types: []string{typ},
		build: func(*state, Caller) (*sessionstreamv1.Event, error) {
			if created {
				return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_ArtifactCreated{
					ArtifactCreated: &sessionstreamv1.ArtifactCreated{Path: path, Version: version}}}, nil
			}
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_ArtifactChanged{
				ArtifactChanged: &sessionstreamv1.ArtifactChanged{Path: path, Version: version}}}, nil
		},
	}, false)
	return err
}

// EndSession stores the confirmed minutes (if-minutes-api) and then appends session.ended
// (rs-session-ends). session.ended carries the signed tree head of the log before it. After it, every new command fails with FailedPrecondition; a repeated
// command_id still returns its stored event. Any participant can end the session for now: roles
// are open (#14).
//
// The confirmed minutes are the text of the last minutes.updated event: ending the session
// confirms it. There is no separate confirm step yet (#22). The stored minutes have the session
// ID, and as author the participant who wrote that last version, with the kind it joined with.
// If the session has no minutes, or the last text is blank, nothing is stored. The command ID of
// the minutes is MinutesCommandPrefix plus the command ID of EndSession, so a repeated EndSession
// does not store the minutes twice. If storing fails, the session does not end and EndSession
// fails with Internal; the client can repeat it.
func (s *Service) EndSession(ctx context.Context, req *sessioncommandsv1.EndSessionRequest) (*sessioncommandsv1.EndSessionResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.SessionEnded},
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			if err := s.storeMinutes(ctx, st, req); err != nil {
				return nil, err
			}
			// The head of the log before session.ended (dec-merkle-log).
			head := s.signedHead(req.GetSessionId(), st)
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_SessionEnded{SessionEnded: &sessionstreamv1.SessionEnded{
				TreeHead: head}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessioncommandsv1.EndSessionResponse{Result: res}, nil
}

// storeMinutes stores the last minutes of the session. The caller holds st.mu.
func (s *Service) storeMinutes(ctx context.Context, st *state, req *sessioncommandsv1.EndSessionRequest) error {
	if s.minutes == nil || st.minutes == nil {
		return nil
	}
	text := st.minutes.GetMinutesUpdated().GetText()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	author := st.minutes.GetActorParticipantId()
	var kind sessionstreamv1.ParticipantKind
	if pt := st.participants[author]; pt != nil {
		kind = pt.joined.GetParticipantJoined().GetKind()
	}
	_, _, err := s.minutes.StoreMinutes(ctx, &minutesv1.Minutes{
		CommandId: MinutesCommandPrefix + req.GetCommandId(), SessionId: req.GetSessionId(),
		Text: text, AuthorParticipantId: author, AuthorKind: authorKind(kind),
	})
	if err != nil {
		return connect.NewError(connect.CodeInternal, "store the minutes: "+err.Error())
	}
	return nil
}

// Subscribe sends every event after the cursor, then live events. It returns after it has sent
// session.ended. A cursor beyond the last event fails with OutOfRange: the client has events the
// server does not know, for example after the server lost its memory store in a restart.
func (s *Service) Subscribe(ctx context.Context, req *sessionstreamv1.SubscribeRequest, stream sessionstreamv1connect.SessionStreamServiceSubscribeServerStream) error {
	sid := req.GetSessionId()
	if sid == "" {
		return invalid("session_id is required")
	}
	caller, err := s.auth.Caller(ctx)
	if err != nil {
		return err
	}
	if caller.StageSessionID != "" && caller.StageSessionID != sid {
		return connect.NewError(connect.CodePermissionDenied, "the stage link is for another session")
	}
	cursor := req.GetAfterEventId()
	st, err := s.lock(sid)
	if err != nil {
		return err
	}
	last, ended := st.lastID, st.ended
	// An open stream reads the log: the session stays in memory until the stream ends.
	st.refs++
	st.mu.Unlock()
	defer s.release(sid, st)
	if cursor > last {
		return connect.NewError(connect.CodeOutOfRange, "after_event_id is beyond the last event of the session")
	}
	if ended && cursor >= last {
		return nil
	}
	if err := stream.SendHeaders(); err != nil {
		return err
	}
	for {
		select {
		case <-caller.Done:
			return connect.NewError(connect.CodeUnauthenticated, "access token expired or revoked")
		default:
		}
		evs, wait, err := s.store.Read(sid, cursor)
		if err != nil {
			return connect.NewError(connect.CodeInternal, "read session log: "+err.Error())
		}
		evs, head, err := s.prepare(sid, evs)
		if err != nil {
			return err
		}
		for i, ev := range evs {
			res := &sessionstreamv1.SubscribeResponse{Event: ev}
			if i == len(evs)-1 {
				// A signed head over the whole copy of the subscriber (dec-restore-from-peers).
				res.TreeHead = head
			}
			if err := stream.Send(res); err != nil {
				return err
			}
			cursor = ev.GetId()
			if ev.GetType() == events.SessionEnded {
				return nil
			}
		}
		if len(evs) > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			// The client went away or the server stops. Either way the client may reconnect.
			return connect.NewError(connect.CodeUnavailable, "stream closed before session.ended")
		case <-caller.Done:
			// The client authenticates again and resumes from its cursor (rs-authenticate).
			return connect.NewError(connect.CodeUnauthenticated, "access token expired or revoked")
		case <-wait:
		}
	}
}

// authorKind maps the kind of a participant to the kind of the author of minutes. The minutes
// own their type (if-minutes-api), so the session maps it here.
func authorKind(k sessionstreamv1.ParticipantKind) minutesv1.AuthorKind {
	switch k {
	case sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN:
		return minutesv1.AuthorKind_AUTHOR_KIND_HUMAN
	case sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT:
		return minutesv1.AuthorKind_AUTHOR_KIND_AGENT
	}
	return minutesv1.AuthorKind_AUTHOR_KIND_UNSPECIFIED
}

// GetEvents returns the events after a cursor, at most limit of them (dec-events-from-cursor).
func (s *Service) GetEvents(ctx context.Context, req *sessionstreamv1.GetEventsRequest) (*sessionstreamv1.GetEventsResponse, error) {
	sid := req.GetSessionId()
	if sid == "" {
		return nil, invalid("session_id is required")
	}
	caller, err := s.auth.Caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.StageSessionID != "" && caller.StageSessionID != sid {
		return nil, connect.NewError(connect.CodePermissionDenied, "the stage link is for another session")
	}
	limit := int(req.GetLimit())
	switch {
	case limit == 0:
		limit = 100
	case limit > 1000:
		return nil, invalid("limit is at most 1000")
	}
	st, err := s.lock(sid)
	if err != nil {
		return nil, err
	}
	last := st.lastID
	st.refs++
	st.mu.Unlock()
	defer s.release(sid, st)
	cursor := req.GetAfterEventId()
	if cursor > last {
		return nil, connect.NewError(connect.CodeOutOfRange, "after_event_id is beyond the last event of the session")
	}
	evs, _, err := s.store.Read(sid, cursor)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "read session log: "+err.Error())
	}
	more := len(evs) > limit
	if more {
		evs = evs[:limit]
	}
	if evs, _, err = s.prepare(sid, evs); err != nil {
		return nil, err
	}
	return &sessionstreamv1.GetEventsResponse{Events: evs, More: more}, nil
}

// prepare replaces every utterance that is off the record by a placeholder with the same ID
// and without its text (dec-off-the-record). The placeholder carries the leaf hash of the
// original. The log itself keeps the original. prepare also returns the signed tree head over
// the events up to the last one in evs if that is the last event of the session, else nil.
func (s *Service) prepare(sid string, evs []*sessionstreamv1.Event) ([]*sessionstreamv1.Event, *sessionstreamv1.SignedTreeHead, error) {
	st, err := s.lock(sid)
	if err != nil {
		return nil, nil, err
	}
	defer st.mu.Unlock()
	var head *sessionstreamv1.SignedTreeHead
	// Commands append to the store and apply to the state under st.mu, so the state is at least
	// as far as the events that were read before.
	if n := len(evs); n > 0 && evs[n-1].GetId() == st.lastID {
		head = s.signedHead(sid, st)
	}
	out := make([]*sessionstreamv1.Event, 0, len(evs))
	for _, ev := range evs {
		if ev.GetUtteranceFinal() != nil {
			if u := st.utterances[ev.GetId()]; u != nil && u.offTheRecord {
				c := proto.Clone(ev).(*sessionstreamv1.Event)
				f := c.GetUtteranceFinal()
				f.Text, f.TranscribedAt, f.OffTheRecord = "", nil, true
				// The placeholder keeps the leaf hash of the original (dec-merkle-log).
				c.LeafHash = st.leaves[ev.GetId()-1]
				ev = c
			}
		}
		out = append(out, ev)
	}
	return out, head, nil
}

// tree returns the leaf hashes of the current log of a session and its schema, after the checks
// that every read of a session needs. The caller must not change the slice.
func (s *Service) tree(ctx context.Context, sid string) ([][]byte, string, error) {
	if sid == "" {
		return nil, "", invalid("session_id is required")
	}
	caller, err := s.auth.Caller(ctx)
	if err != nil {
		return nil, "", err
	}
	if caller.StageSessionID != "" && caller.StageSessionID != sid {
		return nil, "", connect.NewError(connect.CodePermissionDenied, "the stage link is for another session")
	}
	st, err := s.lock(sid)
	if err != nil {
		return nil, "", err
	}
	defer st.mu.Unlock()
	n := len(st.leaves)
	return st.leaves[:n:n], st.schema, nil
}

// treeSize resolves a requested tree size: 0 is the current size; a size beyond it is
// OutOfRange.
func treeSize(req uint32, current int, field string) (int, error) {
	if req == 0 {
		return current, nil
	}
	if int(req) > current {
		return 0, connect.NewError(connect.CodeOutOfRange, field+" is beyond the last event of the session")
	}
	return int(req), nil
}

// GetTreeHead returns the signed tree head of the session log, for the current log or for its
// first tree_size events (dec-merkle-log). The head of an empty session has size 0.
func (s *Service) GetTreeHead(ctx context.Context, req *sessioncommandsv1.GetTreeHeadRequest) (*sessioncommandsv1.GetTreeHeadResponse, error) {
	leaves, schema, err := s.tree(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	n, err := treeSize(req.GetTreeSize(), len(leaves), "tree_size")
	if err != nil {
		return nil, err
	}
	if n == len(leaves) {
		if head := s.currentHead(req.GetSessionId(), n); head != nil {
			return &sessioncommandsv1.GetTreeHeadResponse{TreeHead: head}, nil
		}
	}
	return &sessioncommandsv1.GetTreeHeadResponse{TreeHead: merkle.SignTreeHead(s.key, schema, req.GetSessionId(), leaves[:n])}, nil
}

// GetConsistencyProof returns the proof that the log of second_size events extends the log of
// first_size events. first_size must be at least 1 and at most second_size.
func (s *Service) GetConsistencyProof(ctx context.Context, req *sessioncommandsv1.GetConsistencyProofRequest) (*sessioncommandsv1.GetConsistencyProofResponse, error) {
	leaves, _, err := s.tree(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	second, err := treeSize(req.GetSecondSize(), len(leaves), "second_size")
	if err != nil {
		return nil, err
	}
	first := int(req.GetFirstSize())
	if first == 0 {
		return nil, invalid("first_size is required")
	}
	if first > second {
		return nil, invalid("first_size is greater than second_size")
	}
	return &sessioncommandsv1.GetConsistencyProofResponse{FirstSize: uint32(first), SecondSize: uint32(second),
		Hashes: merkle.ConsistencyProof(leaves[:second], first)}, nil
}

// GetInclusionProof returns the audit path of one event in the tree of tree_size events.
func (s *Service) GetInclusionProof(ctx context.Context, req *sessioncommandsv1.GetInclusionProofRequest) (*sessioncommandsv1.GetInclusionProofResponse, error) {
	leaves, _, err := s.tree(ctx, req.GetSessionId())
	if err != nil {
		return nil, err
	}
	n, err := treeSize(req.GetTreeSize(), len(leaves), "tree_size")
	if err != nil {
		return nil, err
	}
	id := req.GetEventId()
	if id == 0 {
		return nil, invalid("event_id is required")
	}
	if int(id) > n {
		return nil, connect.NewError(connect.CodeOutOfRange, "event_id is beyond the tree size")
	}
	return &sessioncommandsv1.GetInclusionProofResponse{EventId: id, TreeSize: uint32(n),
		Hashes: merkle.InclusionProof(leaves[:n], int(id-1))}, nil
}

// GetHubKey returns the public key that signs tree heads. Every caller with a token may read it.
func (s *Service) GetHubKey(ctx context.Context, _ *sessioncommandsv1.GetHubKeyRequest) (*sessioncommandsv1.GetHubKeyResponse, error) {
	if _, err := s.auth.Caller(ctx); err != nil {
		return nil, err
	}
	return &sessioncommandsv1.GetHubKeyResponse{PublicKey: s.key.Public().(ed25519.PublicKey)}, nil
}

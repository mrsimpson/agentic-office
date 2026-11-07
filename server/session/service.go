package session

import (
	"context"
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	journalv1 "github.com/mrsimpson/agentic-office/server/journal/api"
	sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"
	"github.com/mrsimpson/agentic-office/server/session/api/events"
	sessionv1connect "github.com/mrsimpson/agentic-office/server/session/api/sessionv1connect"
)

// Caller is who makes a call, as the token check (if-token-check) learned it from the token.
type Caller struct {
	// ParticipantID is the identity of the caller. It is also the participant ID in the session.
	ParticipantID string
	DisplayName   string
	Kind          sessionv1.ParticipantKind
	// StageSessionID is set for the read-only token of a stage link. Such a caller may only
	// subscribe to this session.
	StageSessionID string
	// Done is closed when the token expires or is revoked. An open stream ends then. Nil means
	// never.
	Done <-chan struct{}
}

// Authorizer returns the caller of a call. It fails with Unauthenticated if the call carries no
// valid token. In office-server it reads the result of the identity token check.
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

// Journal is the part of the journal API (if-journal-api) that the session needs: it stores the
// minutes when a session ends (rs-session-ends). journal.Service implements it.
type Journal interface {
	// Record appends an entry. It is idempotent by the command ID of the entry: a repeated call
	// returns the stored entry and true.
	Record(ctx context.Context, e *journalv1.JournalEntry) (*journalv1.JournalEntry, bool, error)
}

// MinutesCommandPrefix starts the command ID of the minutes entry that the session writes into
// the journal. The rest is the command ID of the EndSession command.
const MinutesCommandPrefix = "office/session-end/"

// Service implements the session commands and the session stream.
type Service struct {
	store   Store
	auth    Authorizer
	journal Journal

	mu       sync.Mutex
	sessions map[string]*state
}

var _ sessionv1connect.SessionServiceHandler = (*Service)(nil)

// New returns a Service that keeps its logs in store and stores the minutes of ended sessions in
// journal. If auth is nil, every call fails with Unauthenticated. If journal is nil, the minutes
// are not stored.
func New(store Store, auth Authorizer, journal Journal) *Service {
	if auth == nil {
		auth = denyAll{}
	}
	return &Service{store: store, auth: auth, journal: journal, sessions: map[string]*state{}}
}

// state is what the service knows about one session. It is derived from the log only, so it can
// be rebuilt from the store at any time.
type state struct {
	mu           sync.Mutex // serialises the commands of the session
	loaded       bool
	lastID       uint32
	ended        bool
	participants map[string]*participant
	utterances   map[uint32]*utterance // utterance.final event ID -> utterance
	minutes      *sessionv1.Event      // the last minutes.updated event
}

type participant struct {
	joined *sessionv1.Event // the participant.joined event of the current membership
	left   bool
	paused bool
}

type utterance struct {
	speaker   string
	retracted bool
}

// apply updates the state with one event of the log.
func (st *state) apply(ev *sessionv1.Event) {
	st.lastID = ev.GetId()
	switch p := ev.GetPayload().(type) {
	case *sessionv1.Event_ParticipantJoined:
		st.participants[p.ParticipantJoined.GetParticipantId()] = &participant{joined: ev}
	case *sessionv1.Event_ParticipantLeft:
		if pt := st.participants[p.ParticipantLeft.GetParticipantId()]; pt != nil {
			pt.left = true
		}
	case *sessionv1.Event_ParticipantPaused:
		if pt := st.participants[p.ParticipantPaused.GetParticipantId()]; pt != nil {
			pt.paused = true
		}
	case *sessionv1.Event_ParticipantResumed:
		if pt := st.participants[p.ParticipantResumed.GetParticipantId()]; pt != nil {
			pt.paused = false
		}
	case *sessionv1.Event_UtteranceFinal:
		st.utterances[ev.GetId()] = &utterance{speaker: p.UtteranceFinal.GetParticipantId()}
	case *sessionv1.Event_UtteranceRetracted:
		if u := st.utterances[p.UtteranceRetracted.GetUtteranceEventId()]; u != nil {
			u.retracted = true
		}
	case *sessionv1.Event_MinutesUpdated:
		st.minutes = ev
	case *sessionv1.Event_SessionEnded:
		st.ended = true
	}
}

// lock returns the locked state of a session. It loads the state from the store on first use.
// The caller must unlock st.mu.
func (s *Service) lock(sessionID string) (*state, error) {
	s.mu.Lock()
	st, ok := s.sessions[sessionID]
	if !ok {
		st = &state{participants: map[string]*participant{}, utterances: map[uint32]*utterance{}}
		s.sessions[sessionID] = st
	}
	s.mu.Unlock()
	st.mu.Lock()
	if !st.loaded {
		evs, _, err := s.store.Read(sessionID, 0)
		if err != nil {
			st.mu.Unlock()
			return nil, connect.NewError(connect.CodeInternal, "read session log: "+err.Error())
		}
		for _, ev := range evs {
			st.apply(ev)
		}
		st.loaded = true
	}
	return st, nil
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
	build func(st *state, c Caller) (*sessionv1.Event, error)
}

// run carries out a command once (dec-idempotent-commands).
//
// Order of checks: required fields, caller (token, participant_id), repeated command_id, end of
// session, participant, then the checks of the command itself.
func (s *Service) run(ctx context.Context, c command, isJoin bool) (*sessionv1.CommandResult, error) {
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
		return &sessionv1.CommandResult{Event: prev, Duplicate: true}, nil
	}
	if st.ended {
		return nil, precondition("the session has ended")
	}
	pt := st.participants[c.participantID]
	if isJoin {
		// A participant joins once. Another join while it is in the session (for example from a
		// second client of the same participant) returns the first join.
		if pt != nil && !pt.left {
			return &sessionv1.CommandResult{Event: pt.joined, Duplicate: true}, nil
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
	st.apply(ev)
	return &sessionv1.CommandResult{Event: ev}, nil
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
func (s *Service) Join(ctx context.Context, req *sessionv1.JoinRequest) (*sessionv1.JoinResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(),
		types: []string{events.ParticipantJoined},
		check: func(c Caller) error {
			if k := req.GetKind(); k != sessionv1.ParticipantKind_PARTICIPANT_KIND_UNSPECIFIED && k != c.Kind {
				return connect.NewError(connect.CodePermissionDenied, "kind is not the registered kind of the participant")
			}
			return nil
		},
		build: func(_ *state, c Caller) (*sessionv1.Event, error) {
			name := req.GetDisplayName()
			if name == "" {
				name = c.DisplayName
			}
			return &sessionv1.Event{
				Payload: &sessionv1.Event_ParticipantJoined{ParticipantJoined: &sessionv1.ParticipantJoined{
					ParticipantId: c.ParticipantID, DisplayName: name, Kind: c.Kind,
					Captured: req.GetCaptured(),
				}},
			}, nil
		},
	}, true)
	if err != nil {
		return nil, err
	}
	return &sessionv1.JoinResponse{
		ParticipantId: res.GetEvent().GetParticipantJoined().GetParticipantId(), Result: res,
	}, nil
}

func (s *Service) Leave(ctx context.Context, req *sessionv1.LeaveRequest) (*sessionv1.LeaveResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.ParticipantLeft},
		build: func(*state, Caller) (*sessionv1.Event, error) {
			return &sessionv1.Event{Payload: &sessionv1.Event_ParticipantLeft{
				ParticipantLeft: &sessionv1.ParticipantLeft{ParticipantId: req.GetParticipantId()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.LeaveResponse{Result: res}, nil
}

func (s *Service) SubmitUtterance(ctx context.Context, req *sessionv1.SubmitUtteranceRequest) (*sessionv1.SubmitUtteranceResponse, error) {
	if req.GetText() == "" {
		return nil, invalid("text is required")
	}
	if req.GetModality() == sessionv1.Modality_MODALITY_UNSPECIFIED {
		return nil, invalid("modality is required")
	}
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.UtteranceFinal},
		build: func(*state, Caller) (*sessionv1.Event, error) {
			return &sessionv1.Event{Payload: &sessionv1.Event_UtteranceFinal{UtteranceFinal: &sessionv1.UtteranceFinal{
				ParticipantId: req.GetParticipantId(), Text: req.GetText(), Modality: req.GetModality(),
				TranscribedAt: req.GetTranscribedAt(),
			}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.SubmitUtteranceResponse{Result: res}, nil
}

// RetractUtterance appends utterance.retracted. A participant can retract only their own
// utterances, and each one only once. OPEN: may a moderator retract the utterances of others?
// Roles are not part of the contract yet (#14).
func (s *Service) RetractUtterance(ctx context.Context, req *sessionv1.RetractUtteranceRequest) (*sessionv1.RetractUtteranceResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.UtteranceRetracted},
		build: func(st *state, _ Caller) (*sessionv1.Event, error) {
			u, err := knownUtterance(st, req.GetUtteranceEventId())
			if err != nil {
				return nil, err
			}
			if u.speaker != req.GetParticipantId() {
				return nil, connect.NewError(connect.CodePermissionDenied, "only the speaker can retract an utterance")
			}
			if u.retracted {
				return nil, precondition("the utterance is already retracted")
			}
			return &sessionv1.Event{Payload: &sessionv1.Event_UtteranceRetracted{UtteranceRetracted: &sessionv1.UtteranceRetracted{
				UtteranceEventId: req.GetUtteranceEventId(),
			}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.RetractUtteranceResponse{Result: res}, nil
}

// SubmitSignal appends turn.end, silence.long or addressed.
//
// The session does not create signals on its own; it only checks and stores the signals that
// clients submit. In particular it does not search utterances for names to create addressed.
// We chose this because it is the easiest to change: if the session should later detect
// addressed itself (#14), that adds events with an empty command_id and keeps this command
// valid. The reverse change would remove events that clients already rely on.
func (s *Service) SubmitSignal(ctx context.Context, req *sessionv1.SubmitSignalRequest) (*sessionv1.SubmitSignalResponse, error) {
	if req.GetSignal() == nil {
		return nil, invalid("one of turn_end, silence_long or addressed is required")
	}
	if sl := req.GetSilenceLong(); sl != nil && (sl.GetDuration() == nil || sl.GetDuration().AsDuration() <= 0) {
		return nil, invalid("silence_long.duration must be positive")
	}
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.TurnEnd, events.SilenceLong, events.Addressed},
		build: func(st *state, _ Caller) (*sessionv1.Event, error) {
			switch sig := req.GetSignal().(type) {
			case *sessionv1.SubmitSignalRequest_TurnEnd:
				if err := knownParticipant(st, sig.TurnEnd.GetParticipantId(), "turn_end.participant_id"); err != nil {
					return nil, err
				}
				return &sessionv1.Event{Payload: &sessionv1.Event_TurnEnd{TurnEnd: sig.TurnEnd}}, nil
			case *sessionv1.SubmitSignalRequest_SilenceLong:
				return &sessionv1.Event{Payload: &sessionv1.Event_SilenceLong{SilenceLong: sig.SilenceLong}}, nil
			case *sessionv1.SubmitSignalRequest_Addressed:
				if err := knownParticipant(st, sig.Addressed.GetParticipantId(), "addressed.participant_id"); err != nil {
					return nil, err
				}
				if _, err := knownUtterance(st, sig.Addressed.GetUtteranceEventId()); err != nil {
					return nil, err
				}
				return &sessionv1.Event{Payload: &sessionv1.Event_Addressed{Addressed: sig.Addressed}}, nil
			}
			return nil, invalid("unknown signal")
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.SubmitSignalResponse{Result: res}, nil
}

func (s *Service) Pause(ctx context.Context, req *sessionv1.PauseRequest) (*sessionv1.PauseResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.ParticipantPaused},
		build: func(st *state, _ Caller) (*sessionv1.Event, error) {
			if st.participants[req.GetParticipantId()].paused {
				return nil, precondition("the participant is already paused")
			}
			return &sessionv1.Event{Payload: &sessionv1.Event_ParticipantPaused{
				ParticipantPaused: &sessionv1.ParticipantPaused{ParticipantId: req.GetParticipantId()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.PauseResponse{Result: res}, nil
}

func (s *Service) Resume(ctx context.Context, req *sessionv1.ResumeRequest) (*sessionv1.ResumeResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.ParticipantResumed},
		build: func(st *state, _ Caller) (*sessionv1.Event, error) {
			if !st.participants[req.GetParticipantId()].paused {
				return nil, precondition("the participant is not paused")
			}
			return &sessionv1.Event{Payload: &sessionv1.Event_ParticipantResumed{
				ParticipantResumed: &sessionv1.ParticipantResumed{ParticipantId: req.GetParticipantId()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.ResumeResponse{Result: res}, nil
}

func (s *Service) RaiseHand(ctx context.Context, req *sessionv1.RaiseHandRequest) (*sessionv1.RaiseHandResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.HandRaised},
		build: func(*state, Caller) (*sessionv1.Event, error) {
			return &sessionv1.Event{Payload: &sessionv1.Event_HandRaised{HandRaised: &sessionv1.HandRaised{
				ParticipantId: req.GetParticipantId(), Topic: req.GetTopic()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.RaiseHandResponse{Result: res}, nil
}

func (s *Service) UpdateMinutes(ctx context.Context, req *sessionv1.UpdateMinutesRequest) (*sessionv1.UpdateMinutesResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.MinutesUpdated},
		build: func(*state, Caller) (*sessionv1.Event, error) {
			return &sessionv1.Event{Payload: &sessionv1.Event_MinutesUpdated{
				MinutesUpdated: &sessionv1.MinutesUpdated{Text: req.GetText()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.UpdateMinutesResponse{Result: res}, nil
}

// ShowArtifact appends artifact.shown. It does not check that the artifact exists: the session is
// only the log of what participants did, and the stage handles an unknown ID.
func (s *Service) ShowArtifact(ctx context.Context, req *sessionv1.ShowArtifactRequest) (*sessionv1.ShowArtifactResponse, error) {
	if req.GetArtifactId() == "" {
		return nil, invalid("artifact_id is required")
	}
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.ArtifactShown},
		build: func(*state, Caller) (*sessionv1.Event, error) {
			return &sessionv1.Event{Payload: &sessionv1.Event_ArtifactShown{
				ArtifactShown: &sessionv1.ArtifactShown{ArtifactId: req.GetArtifactId()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.ShowArtifactResponse{Result: res}, nil
}

// EndSession stores the confirmed minutes in the journal and then appends session.ended
// (rs-session-ends). After it, every new command fails with FailedPrecondition; a repeated
// command_id still returns its stored event. Any participant can end the session for now: roles
// are open (#14).
//
// The confirmed minutes are the text of the last minutes.updated event: ending the session
// confirms it. There is no separate confirm step yet (#22). The journal entry has
// kind minutes, the session ID, and as author the participant who wrote that last version, with
// the kind it joined with. If the session has no minutes, or the last text is blank, nothing is
// stored. The command ID of the entry is MinutesCommandPrefix plus the command ID of EndSession,
// so a repeated EndSession does not store the minutes twice. If the journal fails, the session
// does not end and EndSession fails with Internal; the client can repeat it.
func (s *Service) EndSession(ctx context.Context, req *sessionv1.EndSessionRequest) (*sessionv1.EndSessionResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.SessionEnded},
		build: func(st *state, _ Caller) (*sessionv1.Event, error) {
			if err := s.storeMinutes(ctx, st, req); err != nil {
				return nil, err
			}
			return &sessionv1.Event{Payload: &sessionv1.Event_SessionEnded{SessionEnded: &sessionv1.SessionEnded{}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	return &sessionv1.EndSessionResponse{Result: res}, nil
}

// storeMinutes writes the last minutes of the session into the journal. The caller holds st.mu.
func (s *Service) storeMinutes(ctx context.Context, st *state, req *sessionv1.EndSessionRequest) error {
	if s.journal == nil || st.minutes == nil {
		return nil
	}
	text := st.minutes.GetMinutesUpdated().GetText()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	author := st.minutes.GetActorParticipantId()
	var kind sessionv1.ParticipantKind
	if pt := st.participants[author]; pt != nil {
		kind = pt.joined.GetParticipantJoined().GetKind()
	}
	_, _, err := s.journal.Record(ctx, &journalv1.JournalEntry{
		CommandId: MinutesCommandPrefix + req.GetCommandId(), Kind: journalv1.EntryKind_ENTRY_KIND_MINUTES,
		Text: text, AuthorParticipantId: author, AuthorKind: authorKind(kind), SessionId: req.GetSessionId(),
	})
	if err != nil {
		return connect.NewError(connect.CodeInternal, "store the minutes in the journal: "+err.Error())
	}
	return nil
}

// Subscribe sends every event after the cursor, then live events. It returns after it has sent
// session.ended. A cursor beyond the last event fails with OutOfRange: the client has events the
// server does not know, for example after the server lost its memory store in a restart.
func (s *Service) Subscribe(ctx context.Context, req *sessionv1.SubscribeRequest, stream sessionv1connect.SessionServiceSubscribeServerStream) error {
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
	st.mu.Unlock()
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
		for _, ev := range evs {
			if err := stream.Send(&sessionv1.SubscribeResponse{Event: ev}); err != nil {
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

// authorKind maps the kind of a participant to the kind of a journal author. The journal owns its
// own type (if-journal-api), so the session maps it here.
func authorKind(k sessionv1.ParticipantKind) journalv1.AuthorKind {
	switch k {
	case sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN:
		return journalv1.AuthorKind_AUTHOR_KIND_HUMAN
	case sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT:
		return journalv1.AuthorKind_AUTHOR_KIND_AGENT
	}
	return journalv1.AuthorKind_AUTHOR_KIND_UNSPECIFIED
}

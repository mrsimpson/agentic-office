package session

import (
	"context"

	"connectrpc.com/connect/v2"

	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
)

// mayTrim refuses an agent: only a human or a tenant admin trims or deletes a session
// (dec-session-trim-delete).
func mayTrim(c Caller) error {
	if c.Kind != sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN {
		return connect.NewError(connect.CodePermissionDenied, "only a human who took part in the session or a tenant admin may trim, delete or restore it; an agent never does")
	}
	return nil
}

// endedOnly lets a command run on an ended session that is not released; every other command is
// refused after the end.
func endedOnly(st *state) error { return nil }

// TrimSession cuts an ended session off after an event (see commands.proto).
func (s *Service) TrimSession(ctx context.Context, req *sessioncommandsv1.TrimSessionRequest) (*sessioncommandsv1.TrimSessionResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.SessionTrimmed}, tookPart: true, orAdmin: true, check: mayTrim, afterEnd: endedOnly,
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			switch {
			case !st.ended:
				return nil, precondition("the session has not ended; end it first")
			case st.released:
				return nil, precondition("the session is released; what it released stays")
			case req.GetLastEventId() == 0 || req.GetLastEventId() > st.lastID:
				return nil, invalid("last_event_id must be an event of the session")
			}
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_SessionTrimmed{
				SessionTrimmed: &sessionstreamv1.SessionTrimmed{LastEventId: req.GetLastEventId()}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	s.summaries.drop(req.GetSessionId())
	return &sessioncommandsv1.TrimSessionResponse{Result: res}, nil
}

// DeleteSession marks an ended session as deleted (see commands.proto).
func (s *Service) DeleteSession(ctx context.Context, req *sessioncommandsv1.DeleteSessionRequest) (*sessioncommandsv1.DeleteSessionResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.SessionDeleted}, tookPart: true, orAdmin: true, check: mayTrim, afterEnd: endedOnly,
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			switch {
			case !st.ended:
				return nil, precondition("the session has not ended; end it first")
			case st.released:
				return nil, precondition("the session is released; what it released stays")
			case st.deleted:
				return nil, precondition("the session is deleted already")
			}
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_SessionDeleted{SessionDeleted: &sessionstreamv1.SessionDeleted{}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	s.summaries.drop(req.GetSessionId())
	return &sessioncommandsv1.DeleteSessionResponse{Result: res}, nil
}

// RestoreSession removes the mark of a deleted session (see commands.proto).
func (s *Service) RestoreSession(ctx context.Context, req *sessioncommandsv1.RestoreSessionRequest) (*sessioncommandsv1.RestoreSessionResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.SessionRestored}, tookPart: true, orAdmin: true, check: mayTrim, afterEnd: endedOnly,
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			if !st.deleted {
				return nil, precondition("the session is not deleted")
			}
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_SessionRestored{SessionRestored: &sessionstreamv1.SessionRestored{}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	s.summaries.drop(req.GetSessionId())
	return &sessioncommandsv1.RestoreSessionResponse{Result: res}, nil
}

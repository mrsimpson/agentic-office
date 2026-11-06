// Package server is the spike of the meeting session (bb-session-service).
// It keeps one append-only event log per session in memory. There is no auth and no persistence.
package server

import (
	"context"
	"sync"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	officev1 "github.com/mrsimpson/agentic-office/gen/office/v1"
	"github.com/mrsimpson/agentic-office/gen/office/v1/officev1connect"
)

type session struct {
	mu        sync.Mutex
	events    []*officev1.Event          // events[i].Id == i+1
	byCmd     map[string]*officev1.Event // command ID -> stored event (dec-idempotent-commands)
	notify    chan struct{}              // closed and replaced on every append
	partByCmd map[string]string          // join command ID -> participant ID
}

// Server implements officev1connect.SessionServiceHandler.
type Server struct {
	mu       sync.Mutex
	sessions map[string]*session
	// OnAppend is called after an event is appended. Used for measurements. May be nil.
	OnAppend func(*officev1.Event)
}

var _ officev1connect.SessionServiceHandler = (*Server)(nil)

func New() *Server { return &Server{sessions: map[string]*session{}} }

func (s *Server) session(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.sessions[id]
	if !ok {
		ss = &session{byCmd: map[string]*officev1.Event{}, partByCmd: map[string]string{}, notify: make(chan struct{})}
		s.sessions[id] = ss
	}
	return ss
}

// appendLocked adds an event. The caller holds ss.mu.
func (s *Server) appendLocked(ss *session, ev *officev1.Event) *officev1.Event {
	ev.Id = uint64(len(ss.events) + 1)
	ev.Time = timestamppb.Now()
	ss.events = append(ss.events, ev)
	if ev.CommandId != "" {
		ss.byCmd[ev.CommandId] = ev
	}
	close(ss.notify)
	ss.notify = make(chan struct{})
	if s.OnAppend != nil {
		s.OnAppend(ev)
	}
	return ev
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, msg) }

func (s *Server) Join(_ context.Context, req *officev1.JoinRequest) (*officev1.JoinResponse, error) {
	if req.GetSessionId() == "" || req.GetCommandId() == "" {
		return nil, invalid("session_id and command_id are required")
	}
	ss := s.session(req.GetSessionId())
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ev, ok := ss.byCmd[req.GetCommandId()]; ok {
		return &officev1.JoinResponse{ParticipantId: ss.partByCmd[req.GetCommandId()], Event: ev}, nil
	}
	pid := uuid.NewString()
	ss.partByCmd[req.GetCommandId()] = pid
	ev := s.appendLocked(ss, &officev1.Event{
		SessionId: req.GetSessionId(),
		Type:      "participant.joined",
		CommandId: req.GetCommandId(),
		Payload: &officev1.Event_ParticipantJoined{ParticipantJoined: &officev1.ParticipantJoined{
			ParticipantId: pid, DisplayName: req.GetDisplayName(), Kind: req.GetKind(),
		}},
	})
	return &officev1.JoinResponse{ParticipantId: pid, Event: ev}, nil
}

func (s *Server) SubmitUtterance(_ context.Context, req *officev1.SubmitUtteranceRequest) (*officev1.SubmitUtteranceResponse, error) {
	if req.GetSessionId() == "" || req.GetCommandId() == "" || req.GetParticipantId() == "" {
		return nil, invalid("session_id, command_id and participant_id are required")
	}
	ss := s.session(req.GetSessionId())
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ev, ok := ss.byCmd[req.GetCommandId()]; ok {
		return &officev1.SubmitUtteranceResponse{Event: ev, Duplicate: true}, nil
	}
	ev := s.appendLocked(ss, &officev1.Event{
		SessionId: req.GetSessionId(),
		Type:      "utterance.final",
		CommandId: req.GetCommandId(),
		Payload: &officev1.Event_UtteranceFinal{UtteranceFinal: &officev1.UtteranceFinal{
			ParticipantId: req.GetParticipantId(), Text: req.GetText(),
			Modality: req.GetModality(), TranscribedAt: req.GetTranscribedAt(),
		}},
	})
	return &officev1.SubmitUtteranceResponse{Event: ev}, nil
}

func (s *Server) Subscribe(ctx context.Context, req *officev1.SubscribeRequest, stream officev1connect.SessionServiceSubscribeServerStream) error {
	if req.GetSessionId() == "" {
		return invalid("session_id is required")
	}
	ss := s.session(req.GetSessionId())
	cursor := req.GetAfterEventId()
	if err := stream.SendHeaders(); err != nil {
		return err
	}
	for {
		ss.mu.Lock()
		pending := ss.events[min(cursor, uint64(len(ss.events))):]
		wait := ss.notify
		ss.mu.Unlock()
		for _, ev := range pending {
			if err := stream.Send(&officev1.SubscribeResponse{Event: ev}); err != nil {
				return err
			}
			cursor = ev.Id
		}
		if len(pending) > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wait:
		}
	}
}

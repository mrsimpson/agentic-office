package contract

import (
	"context"
	"sync"
	"testing"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"

	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// stubSession is a minimal fake of the meeting session. It follows one rule of the contract:
// a repeated join command ID returns the same participant (dec-idempotent-commands).
// Tests of blocks that need a session (for example the CLI) can use fakes like this one.
type stubSession struct {
	sessioncommandsv1connect.UnimplementedSessionCommandsServiceHandler
	mu     sync.Mutex
	byCmd  map[string]*sessioncommandsv1.JoinResponse
	nextID uint32
}

func (s *stubSession) Join(_ context.Context, req *sessioncommandsv1.JoinRequest) (*sessioncommandsv1.JoinResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if res, ok := s.byCmd[req.GetCommandId()]; ok {
		return &sessioncommandsv1.JoinResponse{ParticipantId: res.GetParticipantId(),
			Result: &sessioncommandsv1.CommandResult{Event: res.GetResult().GetEvent(), Duplicate: true}}, nil
	}
	s.nextID++
	pid := coreapi.NewCommandID()
	res := &sessioncommandsv1.JoinResponse{ParticipantId: pid, Result: &sessioncommandsv1.CommandResult{Event: &sessionstreamv1.Event{
		Id: s.nextID, SessionId: req.GetSessionId(), Type: "participant.joined", CommandId: req.GetCommandId(),
		Payload: &sessionstreamv1.Event_ParticipantJoined{ParticipantJoined: &sessionstreamv1.ParticipantJoined{ParticipantId: pid}},
	}}}
	s.byCmd[req.GetCommandId()] = res
	return res, nil
}

// Example of a contract test against a stub service.
func TestExampleAgainstStubSession(t *testing.T) {
	url := contracttest.StartFake(t, func(cs *connect.Server) {
		sessioncommandsv1connect.RegisterSessionCommandsServiceHandler(cs, &stubSession{byCmd: map[string]*sessioncommandsv1.JoinResponse{}})
	})
	c := contracttest.NewClients(url, nil)
	ctx := context.Background()
	req := &sessioncommandsv1.JoinRequest{SessionId: "demo", CommandId: coreapi.NewCommandID(), DisplayName: "agent",
		Kind: sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT}

	first, err := c.Commands.Join(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Commands.Join(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !again.GetResult().GetDuplicate() || again.GetParticipantId() != first.GetParticipantId() ||
		again.GetResult().GetEvent().GetId() != first.GetResult().GetEvent().GetId() {
		t.Fatalf("repeated join: got %v, want same participant and event as %v", again, first)
	}
	if code := connect.CodeOf(func() error {
		_, err := c.Commands.Leave(ctx, &sessioncommandsv1.LeaveRequest{})
		return err
	}()); code != connect.CodeUnimplemented {
		t.Fatalf("Leave on the stub: want unimplemented, got %v", code)
	}
}

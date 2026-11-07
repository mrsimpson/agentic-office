package contract

import (
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"
	sessionv1connect "github.com/mrsimpson/agentic-office/server/session/api/sessionv1connect"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// stubSession is a minimal fake of the meeting session. It follows one rule of the contract:
// a repeated join command ID returns the same participant (dec-idempotent-commands).
// Tests of blocks that need a session (for example the CLI) can use fakes like this one.
type stubSession struct {
	sessionv1connect.UnimplementedSessionServiceHandler
	mu     sync.Mutex
	byCmd  map[string]*sessionv1.JoinResponse
	nextID uint32
}

func (s *stubSession) Join(_ context.Context, req *sessionv1.JoinRequest) (*sessionv1.JoinResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if res, ok := s.byCmd[req.GetCommandId()]; ok {
		return &sessionv1.JoinResponse{ParticipantId: res.GetParticipantId(),
			Result: &sessionv1.CommandResult{Event: res.GetResult().GetEvent(), Duplicate: true}}, nil
	}
	s.nextID++
	pid := ids.NewCommandID()
	res := &sessionv1.JoinResponse{ParticipantId: pid, Result: &sessionv1.CommandResult{Event: &sessionv1.Event{
		Id: s.nextID, SessionId: req.GetSessionId(), Type: "participant.joined", CommandId: req.GetCommandId(),
		Payload: &sessionv1.Event_ParticipantJoined{ParticipantJoined: &sessionv1.ParticipantJoined{ParticipantId: pid}},
	}}}
	s.byCmd[req.GetCommandId()] = res
	return res, nil
}

// Example of a contract test against a stub service.
func TestExampleAgainstStubSession(t *testing.T) {
	url := contracttest.StartFake(t, func(cs *connect.Server) {
		sessionv1connect.RegisterSessionServiceHandler(cs, &stubSession{byCmd: map[string]*sessionv1.JoinResponse{}})
	})
	c := contracttest.NewClients(url, nil)
	ctx := context.Background()
	req := &sessionv1.JoinRequest{SessionId: "demo", CommandId: ids.NewCommandID(), DisplayName: "agent",
		Kind: sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT}

	first, err := c.Session.Join(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Session.Join(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !again.GetResult().GetDuplicate() || again.GetParticipantId() != first.GetParticipantId() ||
		again.GetResult().GetEvent().GetId() != first.GetResult().GetEvent().GetId() {
		t.Fatalf("repeated join: got %v, want same participant and event as %v", again, first)
	}
	if code := connect.CodeOf(func() error {
		_, err := c.Session.Leave(ctx, &sessionv1.LeaveRequest{})
		return err
	}()); code != connect.CodeUnimplemented {
		t.Fatalf("Leave on the stub: want unimplemented, got %v", code)
	}
}

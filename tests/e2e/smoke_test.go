//go:build e2e

// Package e2e holds end-to-end tests with real processes across several building blocks.
// Run the quick ones with "make e2e" and all of them with "make e2e-all".
package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// Smoke test: server and client binaries start and stop, and a participant authenticates over
// TLS and joins a session. Later work packages add the real end-to-end scenarios (voice to agent
// log, resume, latency).
func TestSmoke(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", identityv1.ActorKind_ACTOR_KIND_HUMAN)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if res, err := ada.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: "smoke", CommandId: ids.NewCommandID()}); err != nil ||
		res.GetParticipantId() != "ada" {
		t.Fatalf("join: %v, %v", res, err)
	}
	out, _, code := contracttest.Run(t, "office", nil, "version")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("office version: exit %d", code)
	}
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("server stop: exit %d, err %v", code, err)
	}
}

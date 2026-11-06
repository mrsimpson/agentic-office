//go:build e2e

// Package e2e holds end-to-end tests with real processes across several building blocks.
// Run them with "make e2e".
package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/core/contracttest"
)

// Smoke test: server and client binaries start and stop. Later work packages add the real
// end-to-end scenarios (voice to agent log, resume, latency).
func TestSmoke(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	out, _, code := contracttest.Run(t, "office", nil, "version")
	if code != 0 || strings.TrimSpace(out) == "" {
		t.Fatalf("office version: exit %d", code)
	}
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("server stop: exit %d, err %v", code, err)
	}
}

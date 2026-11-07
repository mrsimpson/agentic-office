package contracttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// GitGate is a git wrapper first in PATH that can make every "git commit" fail. With it a test
// stops the writes of the hub to the archive while the session goes on, for example to lose the
// events after the last write in a crash (dec-restore-from-peers).
type GitGate struct {
	// Env puts the wrapper first in PATH. Pass it to StartHub and StartHubIn.
	Env  []string
	flag string
}

// NewGitGate writes the wrapper. Commits succeed until Close.
func NewGitGate(t testing.TB) *GitGate {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	dir := filepath.Join(t.TempDir(), "git-gate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	flag := filepath.Join(dir, "closed")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = commit ] && [ -e %[1]q ]; then
  echo "git-gate: commits are closed" >&2
  exit 1
fi
exec %[2]q "$@"
`, flag, real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &GitGate{Env: []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}, flag: flag}
}

// Close makes every following commit fail.
func (g *GitGate) Close(t testing.TB) {
	t.Helper()
	if err := os.WriteFile(g.flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Open lets commits succeed again.
func (g *GitGate) Open(t testing.TB) {
	t.Helper()
	if err := os.Remove(g.flag); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// Addr returns the listen address of the hub (host:port), for example to restart it on the same
// port with "-addr".
func (s *Hub) Addr() string { return strings.TrimPrefix(s.URL, "https://") }

package contracttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// AreaStateFile returns the path of the state of the staging area of a session in the staging
// repository (dec-staging-repository): the readers, the deadline, whether an objection holds it,
// the names of its files and the state of a release. The session ID must be safe as a file name.
func AreaStateFile(sessionID string) string { return ".areas/" + sessionID + "/area.yaml" }

// AreaState reads the state of the staging area of a session from the staging repository of a
// tenant, or returns nil if the session has no area.
func AreaState(t testing.TB, staging, sessionID string) map[string]any {
	t.Helper()
	out, err := exec.Command("git", "-C", staging, "show", "main:"+AreaStateFile(sessionID)).Output()
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := yaml.Unmarshal(out, &m); err != nil {
		t.Fatalf("state of the staging area of %s: %v", sessionID, err)
	}
	return m
}

// NewArtifactGate is a GitGate that closes only the commits in the artifact repository of a
// tenant. The staging and the session-log repositories go on. With it a test cuts a release off
// after the area was closed for its release.
func NewArtifactGate(t testing.TB, tenant string) *GitGate {
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
	// The hub sets GIT_DIR to the repository it works on.
	script := fmt.Sprintf(`#!/bin/sh
case "$GIT_DIR" in */repos/tenants/%[3]s) mine=yes;; *) mine=no;; esac
if [ "$1" = commit-tree ] && [ "$mine" = yes ] && [ -e %[1]q ]; then
  echo "git-gate: commits are closed" >&2
  exit 1
fi
exec %[2]q "$@"
`, flag, real, tenant)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &GitGate{Env: []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}, flag: flag}
}

// ReleaseCommits returns the commits of the artifact repository that release a session, newest
// first, as "<author name> <author email>".
func ReleaseCommits(t testing.TB, repo, sessionID string) []string {
	t.Helper()
	out := Git(t, repo, "log", "--format=%an <%ae>", "--grep=^Office-Release: "+sessionID+"$", "main")
	return strings.FieldsFunc(out, func(r rune) bool { return r == '\n' })
}

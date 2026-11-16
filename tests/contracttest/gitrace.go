package contracttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// GitRace is a git wrapper first in PATH that moves main of one repository with the real git tool
// at a chosen moment: when the hub is about to move main (git update-ref), the wrapper first
// commits one file from a clone and pushes it. So the hub finds main moved at the exact step that
// matters, with no timing involved. The wrapper does it once for each call of Arm.
type GitRace struct {
	// Env puts the wrapper first in PATH. Pass it to StartHub and StartHubIn.
	Env []string

	dir string
}

// NewGitRace writes the wrapper for the repository of a tenant.
func NewGitRace(t testing.TB, tenant string) *GitRace {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	dir := filepath.Join(t.TempDir(), "git-race")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The directory "armed" exists while a foreign change waits. The hub sets GIT_DIR to the
	// repository it works on.
	script := fmt.Sprintf(`#!/bin/sh
case "$GIT_DIR" in */repos/tenants/%[1]s) mine=yes;; *) mine=no;; esac
if [ "$1" = update-ref ] && [ "$mine" = yes ] && [ -d %[2]q ]; then
  file=$(cat %[2]q/file)
  work=$(mktemp -d)
  g() { env -u GIT_DIR -u GIT_INDEX_FILE %[3]q -c user.name=foreign -c user.email=f@example.org "$@"; }
  g clone -q "$GIT_DIR" "$work/w" || exit 1
  mkdir -p "$work/w/$(dirname "$file")"
  cp %[2]q/content "$work/w/$file"
  rm -rf %[2]q
  g -C "$work/w" add -A || exit 1
  g -C "$work/w" commit -q -m "foreign change" || exit 1
  g -C "$work/w" push -q origin HEAD:main || exit 1
  rm -rf "$work"
fi
exec %[3]q "$@"
`, tenant, filepath.Join(dir, "armed"), real)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &GitRace{Env: []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}, dir: dir}
}

// Arm makes the next move of main by the hub follow a foreign commit of the file with the content,
// made with the real git tool.
func (g *GitRace) Arm(t testing.TB, file, content string) {
	t.Helper()
	armed := filepath.Join(g.dir, "armed")
	if err := os.MkdirAll(armed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(armed, "file"), []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(armed, "content"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Fired reports whether the foreign commit was made.
func (g *GitRace) Fired() bool {
	_, err := os.Stat(filepath.Join(g.dir, "armed"))
	return err != nil
}

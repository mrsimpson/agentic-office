package contract

// Contract test of the memory skill of the office CLI (if-cli, dec-wiki): the CLI shows the skill
// and installs it into the skills directory of an agent harness. The test needs no hub.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// TestMemorySkill: "skill show" prints the skill, "skill install" writes the same text into the
// default directory or into a given one, and replaces an older copy.
func TestMemorySkill(t *testing.T) {
	env := helpEnv(t)
	text, errOut, code := contracttest.Run(t, "office", env, "skill", "show")
	if code != 0 {
		t.Fatalf("office skill show: exit %d: %s", code, errOut)
	}
	for _, want := range []string{"name: office-memory", "\ndescription: ", "never change", "status:", "owner:", "reviewed:", "utterance_say"} {
		if !strings.Contains(text, want) {
			t.Errorf("office skill show: no %q", want)
		}
	}

	// Default: .claude/skills in the working directory.
	work := t.TempDir()
	out, errOut, code := contracttest.RunIn(t, work, "office", env, nil, "skill", "install", "--json")
	if code != 0 {
		t.Fatalf("office skill install: exit %d: %s", code, errOut)
	}
	var res struct{ Skill, File string }
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Skill != "office-memory" {
		t.Fatalf("office skill install --json: %q (%v)", out, err)
	}
	want := filepath.Join(".claude", "skills", "office-memory", "SKILL.md")
	if res.File != want {
		t.Errorf("office skill install: file %q, want %q", res.File, want)
	}
	if got, err := os.ReadFile(filepath.Join(work, want)); err != nil || string(got) != text {
		t.Errorf("office skill install: the file differs from office skill show (%v)", err)
	}

	// A given directory; an older copy is replaced.
	dir := filepath.Join(t.TempDir(), "skills")
	file := filepath.Join(dir, "office-memory", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := contracttest.Run(t, "office", env, "skill", "install", dir); code != 0 {
		t.Fatalf("office skill install %s: exit %d: %s", dir, code, errOut)
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != text {
		t.Errorf("office skill install %s: the old copy was not replaced (%v)", dir, err)
	}

	// Too many arguments are a usage error.
	if _, _, code := contracttest.Run(t, "office", env, "skill", "install", "a", "b"); code != 2 {
		t.Errorf("office skill install a b: exit %d, want 2", code)
	}
}

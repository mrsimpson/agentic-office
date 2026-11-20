package contract

// Contract tests of the skills injector of the agent runner (bb-agent-runner, dec-skills): the
// runner hands the office memory skill (if-office-memory-skill) to the harness in the working
// directory of the agent.

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	officememory "github.com/mrsimpson/agentic-office/skills/office-memory"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// runAgentIn runs "office agent run" for u in the working directory dir until the first prompt
// has ended, and stops it. It returns the text of the first prompt and the log of the runner.
func runAgentIn(t *testing.T, u *officeUser, dir, sid string) (string, string) {
	t.Helper()
	p, rec := u.agentRunIn(t, dir, nil, "--session", sid, "--role", "You keep the minutes.", "--", contracttest.ACPDouble(t))
	r, _ := contracttest.WaitACPRecord(t, rec, 20*time.Second, func(r contracttest.ACPRecord) bool { return r.Kind() == "prompt" })
	waitPromptEnd(t, rec, 1)
	if code, err := p.Stop(15 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: exit %d, %v\n%s", code, err, p.Stderr())
	}
	return r.Text(), p.Stderr()
}

// gitIn runs git in dir and returns its output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// checkShippedSkill fails unless dir holds every file of the shipped skill with its content.
func checkShippedSkill(t *testing.T, dir string) {
	t.Helper()
	n := 0
	err := fs.WalkDir(officememory.Files(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		want, err := fs.ReadFile(officememory.Files(), p)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			t.Errorf("the skill lacks %s: %v", p, err)
		} else if string(got) != string(want) {
			t.Errorf("%s differs from the shipped skill", p)
		}
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("read the shipped skill: %d files, %v", n, err)
	}
}

// TestAgentRunnerSkills: the runner writes the skill office-memory into .agent/skills of the
// working directory and links .claude/skills/office-memory to it. In a Git work tree, neither
// shows up as a change. A later start replaces the office's own copy. The first prompt does not
// mention the skill, but names the session.
func TestAgentRunnerSkills(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")

	sid := newSession()
	prompt, _ := runAgentIn(t, bot, dir, sid)
	skill := filepath.Join(dir, ".agent", "skills", officememory.Name)
	checkShippedSkill(t, skill)
	link := filepath.Join(dir, ".claude", "skills", officememory.Name)
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("%s is no symbolic link: %v", link, err)
	}
	if a, err := filepath.EvalSymlinks(link); err != nil {
		t.Fatal(err)
	} else if b, _ := filepath.EvalSymlinks(skill); a != b {
		t.Errorf("the link leads to %s, want %s", a, b)
	}
	if st := gitIn(t, dir, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("the skills show up in Git:\n%s", st)
	}

	for _, want := range []string{sid, "must name the session", "Tasks are not held"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("first prompt lacks %q:\n%s", want, prompt)
		}
	}
	for _, not := range []string{"office-memory", "skill"} {
		if strings.Contains(prompt, not) {
			t.Errorf("first prompt mentions %q:\n%s", not, prompt)
		}
	}

	// The next start replaces the office's own copy, also when someone changed it.
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "extra.md"), []byte("extra"), 0o644); err != nil {
		t.Fatal(err)
	}
	runAgentIn(t, bot, dir, newSession())
	checkShippedSkill(t, skill)
	if _, err := os.Stat(filepath.Join(skill, "extra.md")); err == nil {
		t.Error("the replaced skill still holds a file that the office did not ship")
	}
	if st := gitIn(t, dir, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Errorf("the skills show up in Git after the second start:\n%s", st)
	}
	exclude, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(exclude), "/.agent/skills/"+officememory.Name+"/"); n != 1 {
		t.Errorf("the exclude file names the skill %d times:\n%s", n, exclude)
	}
}

// TestAgentRunnerForeignSkill: a skill folder of the same name that the office did not write
// stays as it is. The runner warns and does not link it for Claude Code.
func TestAgentRunnerForeignSkill(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	dir := t.TempDir()
	skill := filepath.Join(dir, ".agent", "skills", officememory.Name)
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	own := "---\nname: office-memory\ndescription: our own rules\n---\n"
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr := runAgentIn(t, bot, dir, newSession())
	if b, err := os.ReadFile(filepath.Join(skill, "SKILL.md")); err != nil || string(b) != own {
		t.Errorf("the runner changed a skill that the office did not write: %q, %v", b, err)
	}
	if !strings.Contains(stderr, "did not write") || !strings.Contains(stderr, skill) {
		t.Errorf("the runner does not warn about the foreign skill:\n%s", stderr)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "skills", officememory.Name)); err == nil {
		t.Error("the runner linked a skill that the office did not write")
	}
}

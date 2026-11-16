package contract

// Contract tests of "joining a meeting follows the clone" (bb-sync, bb-cli and bb-mcp through
// if-cli and if-mcp, dec-artifact-sync): the real office binary joins a meeting in a set-up
// directory, in a worktree of it, and in another directory, against a real office-hub and the real
// git. The tests wait for conditions with generous deadlines and never check how long something
// takes.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// cliJoin runs "office meeting join" in dir. The replicator is its own process, in the same
// directory.
func (s *syncSet) cliJoin(t *testing.T, dir, sid string) (joined, string, string) {
	t.Helper()
	out, errOut, code := s.u.office(dir, "meeting", "join", sid, "--json")
	var j joined
	if code != 0 || json.Unmarshal([]byte(out), &j) != nil || j.ReplicatorPID == 0 {
		t.Fatalf("join in %s: exit %d\nstdout: %s\nstderr: %s", dir, code, out, errOut)
	}
	killAtEnd(t, j.ReplicatorPID)
	return j, out, errOut
}

// replicatorLog is the output of the detached replicator of a join.
func replicatorLog(j joined) string {
	return fileContent(strings.TrimSuffix(j.LogPath, ".jsonl") + ".replicator.log")
}

// pushFromSecondClone pushes a file to main of the hub from the clone of another participant.
func (s *syncSet) pushFromSecondClone(t *testing.T, name, file, content string) {
	t.Helper()
	other := syncMember(t, s.srv, name, name)
	dir := filepath.Join(other.work, "artifacts")
	other.mustInit(dir)
	wt := filepath.Join(other.work, "change")
	other.mustGit(dir, "worktree", "add", "-q", wt, "-b", "change")
	contracttest.WriteFile(t, wt, file, content)
	other.mustGit(wt, "add", "-A")
	other.mustGit(wt, "commit", "-q", "-m", "push "+file)
	other.mustGit(wt, "push", "-q", "origin", "HEAD:main")
}

// A meeting joined in the set-up directory and in a worktree of it follows the clone: a change by
// the documents interface and a change by push arrive while the replicator runs. The follow ends
// with the replicator.
func TestSyncJoinFollowsTheClone(t *testing.T) {
	srv := startSyncHub(t)
	s := newSyncSet(t, srv)
	wt := s.worktree(t, "mine")
	for i, where := range []struct{ name, dir string }{{"set-up directory", s.dir}, {"worktree", wt}} {
		sid := newSession()
		j, out, errOut := s.cliJoin(t, where.dir, sid)
		if strings.TrimSpace(errOut) != "" || out == "" {
			t.Errorf("join in the %s: stdout %q, stderr %q", where.name, out, errOut)
		}
		name := []string{"interface", "worktree"}[i]
		writeDoc(t, s.ada, "documents/plan/"+name+".md", "# "+name+"\n")
		waitFile(t, filepath.Join(s.dir, "documents/plan/"+name+".md"), "# "+name+"\n")
		s.pushFromSecondClone(t, "heidi"+name, "documents/plan/push-"+name+".md", "# Push "+name+"\n")
		waitFile(t, filepath.Join(s.dir, "documents/plan/push-"+name+".md"), "# Push "+name+"\n")
		waitFor(t, "the replicator log to tell about the update", func() bool {
			return strings.Contains(replicatorLog(j), "artifact sync: updated")
		})
		if got := replicatorLog(j); strings.Contains(got, "failed") || strings.Contains(got, "stopped") {
			t.Errorf("the log of the replicator in the %s:\n%s", where.name, got)
		}

		// The follow ends with the replicator: no change arrives after it.
		if _, errOut, code := s.u.office(where.dir, "meeting", "leave", "--session", sid); code != 0 {
			t.Fatalf("leave: %d %s", code, errOut)
		}
		waitUntil(t, 30*time.Second, "the replicator to stop", func() bool { return !alive(j.ReplicatorPID) })
		writeDoc(t, s.ada, "documents/plan/after-"+name+".md", "# After\n")
		time.Sleep(time.Second)
		if _, err := os.Stat(filepath.Join(s.dir, "documents/plan/after-"+name+".md")); err == nil {
			t.Errorf("a change arrived after the replicator ended (%s)", where.name)
		}
		s.sync(t) // bring the directory up to date for the next round
	}
}

// A meeting joined in another directory starts nothing extra: the output is the path of the log
// only, and the log of the replicator says nothing about the sync. This holds for a plain directory, a plain Git
// repository and a Git repository that was not set up.
func TestSyncJoinInOtherDirectoryDoesNothing(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	plain := t.TempDir()
	repo := filepath.Join(s.u.work, "plain")
	s.u.mustGit(s.u.work, "init", "-q", repo)
	for _, dir := range []string{plain, repo, s.u.work} {
		j, out, errOut := s.cliJoin(t, dir, newSession())
		if strings.TrimSpace(errOut) != "" || strings.TrimSpace(out) == "" {
			t.Errorf("join in %s: stdout %q, stderr %q", dir, out, errOut)
		}
		if got := replicatorLog(j); strings.Contains(got, "artifact sync") {
			t.Errorf("join in %s: the log of the replicator tells about the sync:\n%s", dir, got)
		}
	}
	// The set-up directory was not touched by the others.
	if res := s.sync(t); res.Updated {
		t.Errorf("update: %+v", res)
	}
}

// A problem with the clone never stops the join. It is reported once, in plain words, in the log of
// the replicator. The follow goes on: after the problem is fixed, changes arrive.
func TestSyncJoinReportsOwnCommitsOnce(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	contracttest.WriteFile(t, s.dir, "documents/own.md", "# Own\n")
	s.u.mustGit(s.dir, "add", "-A")
	s.u.mustGit(s.dir, "commit", "-q", "--no-verify", "-m", "own commit")

	j, _, errOut := s.cliJoin(t, s.dir, newSession())
	if strings.TrimSpace(errOut) != "" {
		t.Errorf("join: stderr %q", errOut)
	}
	waitFor(t, "the report of the problem", func() bool { return strings.Contains(replicatorLog(j), "commits of its own") })
	if n := strings.Count(replicatorLog(j), "commits of its own"); n != 1 {
		t.Errorf("the problem is reported %d times:\n%s", n, replicatorLog(j))
	}
	// Fix it: drop the own commit. The next change arrives.
	s.u.mustGit(s.dir, "reset", "-q", "--hard", "origin/main")
	writeDoc(t, s.ada, "documents/plan/fixed.md", "# Fixed\n")
	waitFile(t, filepath.Join(s.dir, "documents/plan/fixed.md"), "# Fixed\n")
	if n := strings.Count(replicatorLog(j), "commits of its own"); n != 1 {
		t.Errorf("after the fix, the problem is reported %d times:\n%s", n, replicatorLog(j))
	}
}

// The hub of the clone is not reachable: the join works, the problem is reported once.
func TestSyncJoinReportsUnreachableHubOnce(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	s.u.mustGit(s.dir, "config", "remote.origin.url", "https://127.0.0.1:1/git/"+contracttest.DefaultTenant)

	j, _, _ := s.cliJoin(t, s.dir, newSession())
	waitFor(t, "the report of the problem", func() bool { return strings.Contains(replicatorLog(j), "failed") })
	// The update retries with growing pauses; give it room to repeat itself.
	time.Sleep(4 * time.Second)
	if n := strings.Count(replicatorLog(j), "update of"); n != 1 {
		t.Errorf("the problem is reported %d times:\n%s", n, replicatorLog(j))
	}
	// The meeting itself works: the log gets the events.
	if !alive(j.ReplicatorPID) {
		t.Errorf("the replicator stopped:\n%s", replicatorLog(j))
	}
}

// startMCPIn starts "office mcp" in dir with the environment of the sync user.
func (s *syncSet) startMCPIn(t *testing.T, dir string) *mcpServer {
	t.Helper()
	cmd := exec.Command(contracttest.Binary(t, "office"), "mcp")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), s.u.env...)
	m := &mcpServer{t: t, cmd: cmd, stderr: &syncBuffer{}}
	cmd.Stderr = m.stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: 10 * time.Second}, nil)
	if err != nil {
		t.Fatalf("connect to office mcp: %v\n%s", err, m.stderr)
	}
	m.cs = cs
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cs.Close()
		}
		if t.Failed() {
			t.Logf("stderr of office mcp:\n%s", m.stderr)
		}
	})
	return m
}

// meeting_join in the set-up directory and in a worktree of it follows the clone while the MCP
// server runs, and the follow ends with the server.
func TestSyncMCPJoinFollowsTheClone(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	wt := s.worktree(t, "mine")
	for i, where := range []struct{ name, dir string }{{"set-up directory", s.dir}, {"worktree", wt}} {
		m := s.startMCPIn(t, where.dir)
		m.ok("meeting_join", map[string]any{"session": newSession()})
		name := []string{"interface", "worktree"}[i]
		writeDoc(t, s.ada, "documents/plan/mcp-"+name+".md", "# "+name+"\n")
		waitFile(t, filepath.Join(s.dir, "documents/plan/mcp-"+name+".md"), "# "+name+"\n")
		s.pushFromSecondClone(t, "heidi"+name, "documents/plan/mcp-push-"+name+".md", "# Push "+name+"\n")
		waitFile(t, filepath.Join(s.dir, "documents/plan/mcp-push-"+name+".md"), "# Push "+name+"\n")
		if got := m.stderr.String(); strings.Contains(got, "failed") || strings.Contains(got, "stopped") {
			t.Errorf("the log of the MCP server in the %s:\n%s", where.name, got)
		}

		m.close()
		if alive(m.cmd.Process.Pid) {
			t.Fatalf("office mcp still runs (%s)", where.name)
		}
		writeDoc(t, s.ada, "documents/plan/mcp-after-"+name+".md", "# After\n")
		time.Sleep(time.Second)
		if _, err := os.Stat(filepath.Join(s.dir, "documents/plan/mcp-after-"+name+".md")); err == nil {
			t.Errorf("a change arrived after the MCP server ended (%s)", where.name)
		}
		s.sync(t)
	}
}

// meeting_join in another directory starts nothing and says nothing.
func TestSyncMCPJoinInOtherDirectoryDoesNothing(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	m := s.startMCPIn(t, t.TempDir())
	m.ok("meeting_join", map[string]any{"session": newSession()})
	m.ok("meeting_join", map[string]any{"session": newSession()})
	if strings.Contains(m.stderr.String(), "artifact sync") {
		t.Errorf("the log of the MCP server:\n%s", m.stderr)
	}
}

// A problem with the clone never fails meeting_join. It is reported once.
func TestSyncMCPJoinReportsProblemsOnce(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	contracttest.WriteFile(t, s.dir, "documents/own.md", "# Own\n")
	s.u.mustGit(s.dir, "add", "-A")
	s.u.mustGit(s.dir, "commit", "-q", "--no-verify", "-m", "own commit")

	m := s.startMCPIn(t, s.dir)
	m.ok("meeting_join", map[string]any{"session": newSession()})
	waitFor(t, "the report of the problem", func() bool { return strings.Contains(m.stderr.String(), "commits of its own") })
	m.ok("meeting_join", map[string]any{"session": newSession()})
	s.u.mustGit(s.dir, "reset", "-q", "--hard", "origin/main")
	writeDoc(t, s.ada, "documents/plan/fixed.md", "# Fixed\n")
	waitFile(t, filepath.Join(s.dir, "documents/plan/fixed.md"), "# Fixed\n")
	if n := strings.Count(m.stderr.String(), "commits of its own"); n != 1 {
		t.Errorf("the problem is reported %d times:\n%s", n, m.stderr)
	}

	// The hub of the clone is not reachable.
	s2 := newSyncSet(t, startSyncHub(t))
	s2.u.mustGit(s2.dir, "config", "remote.origin.url", "https://127.0.0.1:1/git/"+contracttest.DefaultTenant)
	m2 := s2.startMCPIn(t, s2.dir)
	m2.ok("meeting_join", map[string]any{"session": newSession()})
	waitFor(t, "the report of the problem", func() bool { return strings.Contains(m2.stderr.String(), "failed") })
}

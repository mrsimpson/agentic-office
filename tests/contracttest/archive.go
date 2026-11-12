package contracttest

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/logfile"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/merkle"
)

// ArchiveDir returns the default Git repository of the archive of the hub.
func (s *Hub) ArchiveDir() string { return filepath.Join(s.DataDir, "archive") }

// HubRepoDir is the Git repository of the hub: users, tenants and memberships (system/).
func (s *Hub) HubRepoDir() string { return filepath.Join(s.DataDir, "repos", "hub") }

// TenantRepoDir is the Git repository of a tenant. It exists once the tenant is created.
func (s *Hub) TenantRepoDir(tenantID string) string {
	return filepath.Join(s.DataDir, "repos", "tenants", tenantID)
}

// Git runs git in dir and returns its output. It fails the test if git fails.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$`)

// ArchivedSessionDir returns the directory of a session in the archive, relative to the
// repository: sessions/<name>, where <name> is the session ID if it is safe as a file name, else
// "=" plus its base64url form (README, Hub setup).
func ArchivedSessionDir(sessionID string) string {
	if safeName.MatchString(sessionID) {
		return "sessions/" + sessionID
	}
	return "sessions/=" + base64.RawURLEncoding.EncodeToString([]byte(sessionID))
}

// ArchivedSession is the stored form of a session in one commit of the archive.
type ArchivedSession struct {
	Commit string
	Files  []string // all files the commit changed
	Log    []byte
	Events []*sessionstreamv1.Event
	Head   *sessionstreamv1.SignedTreeHead
}

// SessionCommits returns the commits that changed the log of a session, oldest first. It returns
// nil if the repository has no commit yet.
func SessionCommits(t testing.TB, repo, sessionID string) []string {
	t.Helper()
	if err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", "HEAD").Run(); err != nil {
		return nil
	}
	out := Git(t, repo, "log", "--reverse", "--format=%H", "--", ArchivedSessionDir(sessionID)+"/log.jsonl")
	return strings.Fields(out)
}

// ReadArchivedSession reads the log and the tree head of a session in one commit.
func ReadArchivedSession(t testing.TB, repo, sessionID, commit string) *ArchivedSession {
	t.Helper()
	dir := ArchivedSessionDir(sessionID)
	a := &ArchivedSession{Commit: commit,
		Files: strings.Fields(Git(t, repo, "show", "--format=", "--name-only", commit))}
	a.Log = []byte(Git(t, repo, "show", commit+":"+dir+"/log.jsonl"))
	evs, err := logfile.UnmarshalLines(a.Log)
	if err != nil {
		t.Fatalf("commit %s: parse the log: %v", commit, err)
	}
	a.Events = evs
	head, err := logfile.UnmarshalTreeHead([]byte(Git(t, repo, "show", commit+":"+dir+"/tree-head.yaml")))
	if err != nil {
		t.Fatalf("commit %s: parse the tree head: %v", commit, err)
	}
	a.Head = head
	return a
}

// Verify checks the stored session: the tree head is signed by hubKey, covers exactly the stored
// events, and the commit wrote the log and the tree head together.
func (a *ArchivedSession) Verify(t testing.TB, sessionID string, hubKey ed25519.PublicKey) {
	t.Helper()
	dir := ArchivedSessionDir(sessionID)
	has := func(f string) bool {
		for _, x := range a.Files {
			if x == f {
				return true
			}
		}
		return false
	}
	if !has(dir+"/log.jsonl") || !has(dir+"/tree-head.yaml") {
		t.Errorf("commit %s: want the log and the tree head in one commit, got %v", a.Commit, a.Files)
	}
	if a.Head.GetSessionId() != sessionID || int(a.Head.GetTreeSize()) != len(a.Events) {
		t.Fatalf("commit %s: tree head %v for %d stored events", a.Commit, a.Head, len(a.Events))
	}
	if err := merkle.VerifyEvents(a.Events, a.Head, hubKey); err != nil {
		t.Fatalf("commit %s: stored tree head does not verify: %v", a.Commit, err)
	}
}

// WaitArchived waits until the archive has a commit of a session whose log matches done, and
// returns it.
func WaitArchived(t testing.TB, repo, sessionID string, timeout time.Duration, done func(*ArchivedSession) bool) *ArchivedSession {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if c := SessionCommits(t, repo, sessionID); len(c) > 0 {
			a := ReadArchivedSession(t, repo, sessionID, c[len(c)-1])
			if done(a) {
				return a
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for session %q in the archive %s", sessionID, repo)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Ended reports whether the last stored event is session.ended.
func (a *ArchivedSession) Ended() bool {
	return len(a.Events) > 0 && a.Events[len(a.Events)-1].GetSessionEnded() != nil
}

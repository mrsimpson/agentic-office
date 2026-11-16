package contract

// Contract tests of the archive (bb-archive, dec-yaml-git-storage) and of how the meeting session
// writes its logs there (dec-session-persistence). They start the real office-hub and read its
// Git repository (by default <data-dir>/archive) with git. The stored logs are read with the
// stored form of the session interface (package logfile) and checked with its Merkle code.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// After session.ended the whole session is in the archive in one commit, without waiting for the
// interval: the raw log with the original of an off-the-record sentence and its mark, and the
// tree head signed by the hub, which verifies.
func TestArchiveSessionAfterEnd(t *testing.T) {
	m := newMeetingArgs(t, "-archive-interval", "1h")
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "Public.")
	secret := m.say(ada, "Secret.")
	if _, err := m.s(ada).MarkOffTheRecord(m.ctx(), &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: ada, UtteranceEventId: secret.GetId()}); err != nil {
		t.Fatal(err)
	}
	m.end(ada)
	repo := m.srv.ArchiveDir()
	a := contracttest.WaitArchived(t, repo, m.sid, 10*time.Second, (*contracttest.ArchivedSession).Ended)
	a.Verify(t, m.sid, hubKey(t, m.reader.Commands))

	// One write: the interval is long, so only session.ended caused a commit.
	if c := contracttest.SessionCommits(t, repo, m.sid); len(c) != 1 {
		t.Fatalf("want one commit of the session, got %d", len(c))
	}
	// The stored log has all events of the session, one line each.
	evs := m.events()
	if len(a.Events) != len(evs) || bytes.Count(a.Log, []byte("\n")) != len(evs) {
		t.Fatalf("stored %d events (%d lines), the session has %d", len(a.Events), bytes.Count(a.Log, []byte("\n")), len(evs))
	}
	// The raw form: the original text and the mark. The interface gives a placeholder.
	stored := a.Events[secret.GetId()-1]
	if stored.GetUtteranceFinal().GetText() != "Secret." || stored.GetLeafHash() != nil {
		t.Errorf("stored utterance: %v", stored)
	}
	if !bytes.Contains(a.Log, []byte(`"type":"utterance.off_the_record"`)) {
		t.Errorf("no off-the-record mark in the stored log:\n%s", a.Log)
	}
	if got := evs[secret.GetId()-1].GetUtteranceFinal(); got.GetText() != "" || !got.GetOffTheRecord() {
		t.Errorf("GetEvents gives %v, want a placeholder", got)
	}
	// The lines are the lines of the local log: the dotted type and the ID as a number.
	first := strings.SplitN(string(a.Log), "\n", 2)[0]
	if !strings.Contains(first, `"id":1,`) || !strings.Contains(first, `"type":"participant.joined"`) {
		t.Errorf("first line: %s", first)
	}
}

// While a session runs, the hub writes it at the interval. Every write is one commit with the log
// as it was then and a tree head over exactly that log; each log extends the one before.
func TestArchiveWritesAtInterval(t *testing.T) {
	m := newMeetingArgs(t, "-archive-interval", "200ms")
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "One.")
	repo := m.srv.ArchiveDir()
	contracttest.WaitArchived(t, repo, m.sid, 10*time.Second, func(a *contracttest.ArchivedSession) bool {
		return len(a.Events) >= 2
	})
	m.say(ada, "Two.")
	m.say(ada, "Three.")
	contracttest.WaitArchived(t, repo, m.sid, 10*time.Second, func(a *contracttest.ArchivedSession) bool {
		return len(a.Events) >= 4 && !a.Ended()
	})
	m.end(ada)
	contracttest.WaitArchived(t, repo, m.sid, 10*time.Second, (*contracttest.ArchivedSession).Ended)

	key := hubKey(t, m.reader.Commands)
	commits := contracttest.SessionCommits(t, repo, m.sid)
	if len(commits) < 3 {
		t.Fatalf("want at least 3 writes of the running session, got %d", len(commits))
	}
	var prev []byte
	for _, c := range commits {
		a := contracttest.ReadArchivedSession(t, repo, m.sid, c)
		a.Verify(t, m.sid, key)
		if !bytes.HasPrefix(a.Log, prev) || len(a.Log) == len(prev) {
			t.Fatalf("commit %s does not extend the log before it", c)
		}
		prev = a.Log
	}
	// An unchanged session is not written again.
	time.Sleep(time.Second)
	if n := len(contracttest.SessionCommits(t, repo, m.sid)); n != len(commits) {
		t.Errorf("%d commits after the end, want %d", n, len(commits))
	}
}

// When the hub stops, it writes the changed sessions a last time. A session ID that is not safe
// as a file name is stored under "=" plus its base64url form.
func TestArchiveOnStopAndUnsafeName(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-archive-interval", "1h")
	repo := srv.ArchiveDir()
	ada := srv.Participant(t, "ada", "Ada", identityv1.ActorKind_ACTOR_KIND_HUMAN)
	sid := "team/weekly 1"
	m := &meeting{t: t, srv: srv, sid: sid, actors: map[string]*contracttest.Actor{"ada": ada}, reader: ada}
	if _, err := ada.Commands.Join(m.ctx(), &sessioncommandsv1.JoinRequest{SessionId: sid, CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	m.say("ada", "Before the stop.")
	key := hubKey(t, ada.Commands)
	if c := contracttest.SessionCommits(t, repo, sid); len(c) != 0 {
		t.Fatalf("written before the interval: %v", c)
	}
	if code, err := srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: exit %d, %v", code, err)
	}
	if got := contracttest.ArchivedSessionDir(sid); got != "sessions/=dGVhbS93ZWVrbHkgMQ" {
		t.Fatalf("archive dir %s", got)
	}
	c := contracttest.SessionCommits(t, repo, sid)
	if len(c) != 1 {
		t.Fatalf("want one commit after the stop, got %d", len(c))
	}
	a := contracttest.ReadArchivedSession(t, repo, sid, c[0])
	a.Verify(t, sid, key)
	if len(a.Events) != 2 || a.Events[1].GetUtteranceFinal().GetText() != "Before the stop." {
		t.Fatalf("stored events: %v", a.Events)
	}
}

// commitCount returns the number of commits on main, or 0 for a repository without commits.
func commitCount(t *testing.T, repo string) int {
	t.Helper()
	if strings.TrimSpace(contracttest.Git(t, repo, "branch", "--list", "main")) == "" {
		return 0
	}
	return len(strings.Fields(contracttest.Git(t, repo, "rev-list", "main")))
}

func createDoc(t *testing.T, a *contracttest.Actor, path, content string) (*documentv1.CreateDocumentResponse, error) {
	t.Helper()
	return a.Document.CreateDocument(ctx10(t), &documentv1.CreateDocumentRequest{Path: path, Content: content, CommandId: coreapi.NewCommandID()})
}

// The repositories of the archive are bare: there is no work tree. A change is exactly one
// commit with the files of the change, and nothing is left in the directory of the repository
// but the Git data.
func TestArchiveCommitIsOneCommitInABareRepository(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	repo := srv.ArchiveDir()
	before := commitCount(t, repo)
	if _, err := createDoc(t, ada, "documents/plan/one.md", "# One\n"); err != nil {
		t.Fatal(err)
	}
	if got := commitCount(t, repo); got != before+1 {
		t.Fatalf("%d commits after one change, want %d", got, before+1)
	}
	if files := strings.Fields(contracttest.Git(t, repo, "show", "--format=", "--name-only", "main")); len(files) != 1 || files[0] != "documents/plan/one.md" {
		t.Errorf("the commit changed %v", files)
	}
	if got := strings.TrimSpace(contracttest.Git(t, repo, "rev-parse", "--is-bare-repository")); got != "true" {
		t.Errorf("is-bare-repository: %s", got)
	}
	entries, err := os.ReadDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "documents" || strings.HasPrefix(e.Name(), "office-") || e.Name() == ".git" {
			t.Errorf("%s in the directory of the repository", e.Name())
		}
	}
	// A clone of the repository shows the file.
	work := filepath.Join(t.TempDir(), "clone")
	contracttest.Git(t, filepath.Dir(work), "clone", "-q", repo, work)
	if b, err := os.ReadFile(filepath.Join(work, "documents", "plan", "one.md")); err != nil || string(b) != "# One\n" {
		t.Errorf("file in a clone: %q, %v", b, err)
	}
}

// A commit that fails leaves nothing visible: main does not move, and the file is not readable.
// The next change of the same file works.
func TestArchiveFailedCommitLeavesNothingVisible(t *testing.T) {
	gate := contracttest.NewGitGate(t)
	srv := contracttest.StartHub(t, gate.Env)
	ada := srv.Participant(t, "ada", "Ada", human)
	repo := srv.ArchiveDir()
	if _, err := createDoc(t, ada, "documents/base.md", "base"); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(contracttest.Git(t, repo, "rev-parse", "main"))
	files := contracttest.Files(t, repo)

	gate.Close(t)
	if _, err := createDoc(t, ada, "documents/lost.md", "lost"); err == nil {
		t.Fatal("a commit that cannot be made succeeded")
	}
	if got := strings.TrimSpace(contracttest.Git(t, repo, "rev-parse", "main")); got != head {
		t.Fatalf("main moved from %s to %s", head, got)
	}
	if got := contracttest.Files(t, repo); strings.Join(got, ",") != strings.Join(files, ",") {
		t.Fatalf("files %v after a failed commit, want %v", got, files)
	}
	_, err := ada.Archive.ReadFile(ctx10(t), &archivev1.ReadFileRequest{Path: "documents/lost.md"})
	wantCode(t, "read a file of a failed commit", err, connect.CodeNotFound)

	// The failed change is not committed with a later one.
	gate.Open(t)
	if _, err := createDoc(t, ada, "documents/next.md", "next"); err != nil {
		t.Fatal(err)
	}
	if got := contracttest.Files(t, repo); strings.Contains(strings.Join(got, ","), "lost.md") {
		t.Errorf("the file of the failed change is in %v", got)
	}
	if n := strings.Fields(contracttest.Git(t, repo, "show", "--format=", "--name-only", "main")); len(n) != 1 || n[0] != "documents/next.md" {
		t.Errorf("the next commit changed %v", n)
	}
}

// Someone moves main with the real git tool at the moment the archive moves it. The archive
// builds its change on the new state and tries again: both changes are in main, in one line of
// commits.
func TestArchiveRetriesWhenMainMoved(t *testing.T) {
	race := contracttest.NewGitRace(t, contracttest.DefaultTenant)
	srv := contracttest.StartHub(t, race.Env)
	ada := srv.Participant(t, "ada", "Ada", human)
	repo := srv.ArchiveDir()
	if _, err := createDoc(t, ada, "documents/base.md", "base"); err != nil {
		t.Fatal(err)
	}

	race.Arm(t, "other/foreign.md", "foreign\n")
	if _, err := createDoc(t, ada, "documents/mine.md", "mine"); err != nil {
		t.Fatalf("a change while main moved: %v", err)
	}
	if !race.Fired() {
		t.Fatal("the foreign change was not made")
	}
	files := strings.Join(contracttest.Files(t, repo), ",")
	for _, want := range []string{"documents/base.md", "documents/mine.md", "other/foreign.md"} {
		if !strings.Contains(files, want) {
			t.Errorf("%s is lost: %s", want, files)
		}
	}
	// A line of commits: the foreign commit first, then ours on top of it, no merge.
	if merges := contracttest.Git(t, repo, "rev-list", "--merges", "main"); merges != "" {
		t.Errorf("merge commits: %s", merges)
	}
	if got := strings.TrimSpace(contracttest.Git(t, repo, "show", "--format=", "--name-only", "main")); got != "documents/mine.md" {
		t.Errorf("the last commit changed %q", got)
	}
	if got := strings.TrimSpace(contracttest.Git(t, repo, "show", "--format=", "--name-only", "main~1")); got != "other/foreign.md" {
		t.Errorf("the commit before changed %q", got)
	}
	// Both are readable through the archive, too.
	if got := readDoc(t, ada, "documents/mine.md"); string(got.GetContent()) != "mine" {
		t.Errorf("read: %q", got.GetContent())
	}
}

// When the foreign commit changed the same file, the change fails with a clear error and writes
// nothing: the foreign change stays as it is.
func TestArchiveConflictWhenAFileChangedMeanwhile(t *testing.T) {
	race := contracttest.NewGitRace(t, contracttest.DefaultTenant)
	srv := contracttest.StartHub(t, race.Env)
	ada := srv.Participant(t, "ada", "Ada", human)
	repo := srv.ArchiveDir()
	created, err := createDoc(t, ada, "documents/a.md", "one")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(contracttest.Git(t, repo, "rev-parse", "main"))

	race.Arm(t, "documents/a.md", "foreign\n")
	_, err = ada.Document.ChangeDocument(ctx10(t), &documentv1.ChangeDocumentRequest{Path: "documents/a.md", Content: "mine",
		CommandId: coreapi.NewCommandID(), BaseVersion: created.GetDocument().GetVersion()})
	wantCode(t, "change of a file that changed meanwhile", err, connect.CodeAborted)
	if !race.Fired() {
		t.Fatal("the foreign change was not made")
	}
	if got := contracttest.Git(t, repo, "show", "main:documents/a.md"); got != "foreign\n" {
		t.Errorf("file after the conflict: %q", got)
	}
	if got := strings.TrimSpace(contracttest.Git(t, repo, "rev-list", "--count", head+"..main")); got != "1" {
		t.Errorf("%s commits after the foreign one, want only the foreign commit", got)
	}
}

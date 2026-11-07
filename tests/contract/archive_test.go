package contract

// Contract tests of the archive (bb-archive, dec-yaml-git-storage) and of how the meeting session
// writes its logs there (dec-session-persistence). They start the real office-hub and read its
// Git repository (by default <data-dir>/archive) with git. The stored logs are read with the
// stored form of the session interface (package logfile) and checked with its Merkle code.

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// After session.ended the whole session is in the archive in one commit, without waiting for the
// interval: the raw log with the original of an off-the-record sentence and its mark, and the
// tree head signed by the hub, which verifies.
func TestArchiveSessionAfterEnd(t *testing.T) {
	m := newMeetingArgs(t, "-archive-interval", "1h")
	ada := m.join("Ada", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "Public.")
	secret := m.say(ada, "Secret.")
	if _, err := m.s(ada).MarkOffTheRecord(m.ctx(), &sessionv1.MarkOffTheRecordRequest{SessionId: m.sid,
		CommandId: ids.NewCommandID(), ParticipantId: ada, UtteranceEventId: secret.GetId()}); err != nil {
		t.Fatal(err)
	}
	m.end(ada)
	repo := m.srv.ArchiveDir()
	a := contracttest.WaitArchived(t, repo, m.sid, 10*time.Second, (*contracttest.ArchivedSession).Ended)
	a.Verify(t, m.sid, hubKey(t, m.reader.Session))

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
	// The work tree has the same files as the commit.
	contracttest.Git(t, repo, "diff", "--quiet", "HEAD")
}

// While a session runs, the hub writes it at the interval. Every write is one commit with the log
// as it was then and a tree head over exactly that log; each log extends the one before.
func TestArchiveWritesAtInterval(t *testing.T) {
	m := newMeetingArgs(t, "-archive-interval", "200ms")
	ada := m.join("Ada", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
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

	key := hubKey(t, m.reader.Session)
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
// as a file name is stored under "=" plus its base64url form. -archive-dir moves the repository.
func TestArchiveOnStopAndUnsafeName(t *testing.T) {
	repo := t.TempDir() + "/repo"
	srv := contracttest.StartHub(t, nil, "-archive-interval", "1h", "-archive-dir", repo)
	ada := srv.Participant(t, "ada", "Ada", identityv1.ActorKind_ACTOR_KIND_HUMAN)
	sid := "team/weekly 1"
	m := &meeting{t: t, srv: srv, sid: sid, actors: map[string]*contracttest.Actor{"ada": ada}, reader: ada}
	if _, err := ada.Session.Join(m.ctx(), &sessionv1.JoinRequest{SessionId: sid, CommandId: ids.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	m.say("ada", "Before the stop.")
	key := hubKey(t, ada.Session)
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

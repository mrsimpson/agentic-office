package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// speak sends an utterance of a participant in a session and returns its event ID.
func speak(t *testing.T, a *contracttest.Actor, sid, text string) uint32 {
	t.Helper()
	res, err := a.Commands.SubmitUtterance(ctx10(t), &sessioncommandsv1.SubmitUtteranceRequest{SessionId: sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: a.ParticipantID, Text: text, Modality: sessionstreamv1.Modality_MODALITY_TEXT})
	if err != nil {
		t.Fatalf("say %q: %v", text, err)
	}
	return res.GetResult().GetEvent().GetId()
}

// The session logs lie in a repository of their own. A clone of the artifact repository holds no
// session log and no original of an off-the-record sentence, the session-log repository holds the
// logs and verifies as before, records go to the artifact repository, and every tenant has its
// own two repositories (dec-off-the-record).
func TestSessionLogsLieInTheirOwnRepository(t *testing.T) {
	m := newTwoTenants(t, "-archive-interval", "100ms")
	const acmeSecret, globexSecret = "Acme secret sentence.", "Globex secret sentence."
	for _, c := range []struct {
		a      *contracttest.Actor
		public string
		secret string
	}{{m.ada, "Acme public sentence.", acmeSecret}, {m.bob, "Globex public sentence.", globexSecret}} {
		join2(t, c.a, "weekly")
		speak(t, c.a, "weekly", c.public)
		id := speak(t, c.a, "weekly", c.secret)
		if _, err := c.a.Commands.MarkOffTheRecord(ctx10(t), &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: "weekly",
			CommandId: coreapi.NewCommandID(), ParticipantId: c.a.ParticipantID, UtteranceEventId: id}); err != nil {
			t.Fatal(err)
		}
		// A record written while the session writes lands in the artifact repository.
		recordDecision(t, c.a, "Decision of "+c.a.TenantID+".", "")
	}
	// Acme ends its session; globex keeps it open over the restart.
	if _, err := m.ada.Commands.EndSession(ctx10(t), &sessioncommandsv1.EndSessionRequest{SessionId: "weekly",
		CommandId: coreapi.NewCommandID(), ParticipantId: m.ada.ParticipantID}); err != nil {
		t.Fatal(err)
	}
	waitLogs := func(tenant string, done func(*contracttest.ArchivedSession) bool) {
		contracttest.WaitArchived(t, m.srv.TenantSessionLogsDir(tenant), "weekly", 20*time.Second, done)
	}
	waitLogs("acme", (*contracttest.ArchivedSession).Ended)
	waitLogs("globex", func(a *contracttest.ArchivedSession) bool { return len(a.Events) >= 4 })
	if code, err := m.srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	m2 := setUpAgain(t, m)

	for tenant, secret := range map[string]string{"acme": acmeSecret, "globex": globexSecret} {
		other := map[string]string{"acme": globexSecret, "globex": acmeSecret}[tenant]
		artifacts, logs := m2.srv.TenantRepoDir(tenant), m2.srv.TenantSessionLogsDir(tenant)
		// A clone of the artifact repository has no sessions/ and no original of a sentence.
		clone := filepath.Join(t.TempDir(), "clone")
		contracttest.Git(t, filepath.Dir(clone), "clone", "-q", artifacts, clone)
		if _, err := os.Stat(filepath.Join(clone, "sessions")); err == nil {
			t.Errorf("%s: the clone of the artifact repository has a directory sessions", tenant)
		}
		for _, f := range contracttest.Files(t, artifacts) {
			if strings.HasPrefix(f, "sessions/") || strings.Contains(f, "log.jsonl") || strings.Contains(f, "tree-head") {
				t.Errorf("%s: session file %s in the artifact repository", tenant, f)
			}
		}
		all := contracttest.AllText(t, artifacts)
		if strings.Contains(all, secret) || strings.Contains(all, "public sentence.") {
			t.Errorf("%s: sentences of a session in the artifact repository:\n%.600s", tenant, all)
		}
		if history := contracttest.Git(t, artifacts, "log", "--all", "-p"); strings.Contains(history, secret) {
			t.Errorf("%s: the original of an off-the-record sentence is in the history of the artifact repository", tenant)
		}
		// The record lies in the artifact repository, and only there.
		if !strings.Contains(all, "Decision of "+tenant+".") {
			t.Errorf("%s: no decision in the artifact repository", tenant)
		}
		for _, f := range contracttest.Files(t, logs) {
			if !strings.HasPrefix(f, "sessions/") {
				t.Errorf("%s: %s in the session-log repository", tenant, f)
			}
		}
		text := contracttest.AllText(t, logs)
		if !strings.Contains(text, secret) || strings.Contains(text, other) || strings.Contains(text, "Decision of") {
			t.Errorf("%s: session-log repository:\n%.800s", tenant, text)
		}
	}

	// The logs verify as before; the log of the ended session of acme is complete.
	for tenant, a := range map[string]*contracttest.Actor{"acme": m2.ada, "globex": m2.bob} {
		key := hubKey(t, a.Commands)
		repo := m2.srv.TenantSessionLogsDir(tenant)
		commits := contracttest.SessionCommits(t, repo, "weekly")
		if len(commits) == 0 {
			t.Fatalf("%s: no commit of the session", tenant)
		}
		for _, c := range commits {
			contracttest.ReadArchivedSession(t, repo, "weekly", c).Verify(t, "weekly", key)
		}
		if tenant == "acme" && !contracttest.ReadArchivedSession(t, repo, "weekly", commits[len(commits)-1]).Ended() {
			t.Errorf("acme: the last stored log has not ended")
		}
	}
	// The open session of globex is restored from the session-log repository after the restart.
	if evs := eventsOf(t, m2.bob, "weekly"); len(evs) < 4 {
		t.Errorf("session of globex after the restart: %d events", len(evs))
	}
}

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/logfile"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/merkle"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

var regexpFilledIn = regexp.MustCompile(`restore: session "restart": \w+ filled in the events 5 to 7`)

// TestHubRestartRestoresFromArchiveAndPeers (dec-restore-from-peers, rs-hub-restart): two
// participants follow a session with the office CLI and its replicator. The hub writes the
// session to the archive, then its writes fail, more events come, and the hub is killed. After
// the restart on the same data directory the hub restores the session from the archive, a
// replicator fills in the rest from its local copy, and everyone continues: both local logs, the
// hub and the archive have every event exactly once, with IDs without gaps.
func TestHubRestartRestoresFromArchiveAndPeers(t *testing.T) {
	gate := contracttest.NewGitGate(t)
	hubArgs := []string{"-archive-interval", "300ms", "-restore-wait", "1m"}
	srv := contracttest.StartHub(t, gate.Env, hubArgs...)
	runtime := filepath.Join(t.TempDir(), "run")
	envFor := func(key string) []string {
		return []string{"OFFICE_HUB=" + srv.URL, "OFFICE_CA_CERT=" + srv.CertFile, "OFFICE_TENANT=" + contracttest.DefaultTenant, "OFFICE_KEY=" + key,
			"OFFICE_RUNTIME_DIR=" + runtime, "OFFICE_SESSION=", "OFFICE_JSON="}
	}
	office := func(env []string, args ...string) map[string]any {
		t.Helper()
		out, errOut, code := contracttest.Run(t, "office", env, append(args, "--json")...)
		var v map[string]any
		if code != 0 || json.Unmarshal([]byte(out), &v) != nil {
			t.Fatalf("office %v: exit %d, %q, %q", args, code, out, errOut)
		}
		return v
	}
	adminKey := filepath.Join(t.TempDir(), "admin.pem")
	if err := auth.WriteKey(adminKey, srv.AdminKey); err != nil {
		t.Fatal(err)
	}
	admin := envFor(adminKey)
	user := func(pid, name, kind string) []string {
		env := envFor(filepath.Join(t.TempDir(), pid+".pem"))
		k := office(env, "key", "create")
		office(admin, "admin", "user", "create", pid, k["publicKey"].(string), "--name", name, "--kind", kind)
		srv.EnsureMember(t, pid)
		return env
	}
	ada, bot := user("ada", "Ada", "human"), user("bot", "Bot", "agent")
	const sid = "restart"
	join := func(env []string) (string, int) {
		v := office(env, "meeting", "join", sid)
		pid := int(v["replicatorPid"].(float64))
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		return v["logPath"].(string), pid
	}
	adaLog, adaRep := join(ada)
	botLog, botRep := join(bot)
	read := func(path string) []*sessionstreamv1.Event {
		b, _ := os.ReadFile(path)
		evs, err := logfile.UnmarshalLines(b[:bytes.LastIndexByte(b, '\n')+1])
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return evs
	}
	waitLines := func(n int) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for len(read(adaLog)) < n || len(read(botLog)) < n {
			if time.Now().After(deadline) {
				t.Fatalf("timeout: want %d lines, ada has %d, bot %d", n, len(read(adaLog)), len(read(botLog)))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	office(ada, "say", "One.")
	office(bot, "say", "Two.")
	waitLines(4)
	contracttest.WaitArchived(t, srv.ArchiveDir(), sid, 10*time.Second, func(a *contracttest.ArchivedSession) bool {
		return len(a.Events) == 4
	})
	// The archive stops here. These events exist only on the hub and in the local logs.
	gate.Close(t)
	office(ada, "say", "Three.")
	office(bot, "say", "Four.")
	office(ada, "say", "Five.")
	waitLines(7)
	for _, l := range []string{adaLog, botLog} {
		head := strings.TrimSuffix(l, ".jsonl") + ".tree-head.yaml"
		deadline := time.Now().Add(10 * time.Second)
		for {
			b, _ := os.ReadFile(head)
			if h, err := logfile.UnmarshalTreeHead(b); err == nil && h.GetTreeSize() == 7 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no tree head of size 7 next to %s", l)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// The hub crashes: no last write.
	srv.Kill()
	gate.Open(t)
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, gate.Env, append([]string{"-addr", srv.Addr()}, hubArgs...)...)
	reader := srv.Participant(t, "reader", "Reader", identityv1.ActorKind_ACTOR_KIND_AGENT)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	key, err := reader.Commands.GetHubKey(ctx, &sessioncommandsv1.GetHubKeyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// The hub restored 4 events from the archive; a replicator fills in 5 to 7.
	srv.WaitStderr(t, regexpFilledIn, 20*time.Second)
	head, err := reader.Commands.GetTreeHead(ctx, &sessioncommandsv1.GetTreeHeadRequest{SessionId: sid})
	if err != nil || head.GetTreeHead().GetTreeSize() != 7 {
		t.Fatalf("hub after the fill-in: %v, %v", head, err)
	}

	// Everyone continues.
	office(ada, "say", "Six.")
	office(bot, "say", "Seven.")
	office(ada, "meeting", "end")
	for _, pid := range []int{adaRep, botRep} {
		deadline := time.Now().Add(20 * time.Second)
		for syscall.Kill(pid, 0) == nil && !zombie(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("replicator %d did not stop after session.ended", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	ab, _ := os.ReadFile(adaLog)
	bb, _ := os.ReadFile(botLog)
	if !bytes.Equal(ab, bb) {
		t.Fatalf("the two local logs differ:\n%s\n---\n%s", ab, bb)
	}
	local := read(adaLog)
	res, err := reader.Stream.GetEvents(ctx, &sessionstreamv1.GetEventsRequest{SessionId: sid, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetEvents()) != len(local) {
		t.Fatalf("hub has %d events, the local log %d", len(res.GetEvents()), len(local))
	}
	said := map[string]int{}
	for i, ev := range local {
		if ev.GetId() != uint32(i+1) {
			t.Fatalf("line %d has ID %d", i+1, ev.GetId())
		}
		hl, _ := logfile.MarshalLine(res.GetEvents()[i])
		ll, _ := logfile.MarshalLine(ev)
		if !bytes.Equal(hl, ll) {
			t.Fatalf("event %d differs:\nhub   %s\nlocal %s", i+1, hl, ll)
		}
		if u := ev.GetUtteranceFinal(); u != nil {
			said[u.GetText()]++
		}
	}
	for _, s := range []string{"One.", "Two.", "Three.", "Four.", "Five.", "Six.", "Seven."} {
		if said[s] != 1 {
			t.Errorf("%q is %d times in the log, want once", s, said[s])
		}
	}
	if last := local[len(local)-1]; last.GetType() != events.SessionEnded {
		t.Fatalf("last line: %v", last)
	}
	if err := merkle.VerifyEvents(local[:len(local)-1], local[len(local)-1].GetSessionEnded().GetTreeHead(), key.GetPublicKey()); err != nil {
		t.Fatalf("the local log does not verify against session.ended: %v", err)
	}
	a := contracttest.WaitArchived(t, srv.ArchiveDir(), sid, 10*time.Second, (*contracttest.ArchivedSession).Ended)
	a.Verify(t, sid, key.GetPublicKey())
	if len(a.Events) != len(local) {
		t.Fatalf("archive has %d events, the local log %d", len(a.Events), len(local))
	}
}

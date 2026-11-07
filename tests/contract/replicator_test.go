package contract

// Contract tests of the log replicator (bb-replicator) through if-local-log. The replicator runs
// as a real process, started by "office meeting join"; the tests only look at the file it writes
// and at the process. Events are written through the session commands of the real server.
// Helpers are in cli_test.go.

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/events"
	"github.com/mrsimpson/agentic-office/hub/session/api/logfile"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// serverLog reads the whole session log from the server, up to session.ended.
func serverLog(t *testing.T, a *contracttest.Actor, sid string) []string {
	t.Helper()
	st, err := a.Session.Subscribe(ctx10(t), &sessionv1.SubscribeRequest{SessionId: sid})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var out []string
	for {
		res, err := st.Receive()
		if err != nil {
			t.Fatalf("server log: %v", err)
		}
		line, err := logfile.MarshalLine(res.GetEvent())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.TrimSuffix(string(line), "\n"))
		if res.GetEvent().GetType() == events.SessionEnded {
			return out
		}
	}
}

// sameAsServer checks that the local log has every event of the server exactly once, in order,
// byte for byte, and nothing else.
func sameAsServer(t *testing.T, path string, server []string) {
	t.Helper()
	lines := readLog(t, path)
	b, _ := os.ReadFile(path)
	if len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("%s does not end with a complete line", path)
	}
	if len(lines) != len(server) {
		t.Fatalf("%s has %d lines, the server has %d events", path, len(lines), len(server))
	}
	for i, l := range lines {
		if l.raw != server[i] {
			t.Fatalf("%s line %d:\n got %s\nwant %s", path, i+1, l.raw, server[i])
		}
	}
}

func TestReplicatorResumesAfterKill(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	writer := m.srv.Participant(t, "writer", "Writer", agent)
	sid := newSession()
	if _, err := writer.Session.Join(ctx10(t), &sessionv1.JoinRequest{SessionId: sid, CommandId: ids.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	j := bot.join(sid)
	firstJoin := j.Event

	// The writer appends events the whole time.
	const total = 300
	done := make(chan error, 1)
	go func() {
		for i := range total {
			_, err := writer.Session.SubmitUtterance(ctx10(t), &sessionv1.SubmitUtteranceRequest{SessionId: sid,
				CommandId: ids.NewCommandID(), ParticipantId: "writer", Text: "sentence " + string(rune('a'+i%26)),
				Modality: sessionv1.Modality_MODALITY_TEXT})
			if err != nil {
				done <- err
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		done <- nil
	}()

	// Kill the replicator three times while events arrive. Each time, leave a torn line, as if
	// it died in the middle of a write. A new join of the same client resumes.
	for round := 1; round <= 3; round++ {
		waitUntil(t, 10*time.Second, "more lines", func() bool { return len(readLog(t, j.LogPath)) >= round*40 })
		if err := syscall.Kill(j.ReplicatorPID, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, 5*time.Second, "replicator dies", func() bool { return !alive(j.ReplicatorPID) })
		f, err := os.OpenFile(j.LogPath, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(`{"id":99999,"sessionId":"` + sid + `","type":"utter`)
		f.Close()
		time.Sleep(30 * time.Millisecond) // let events pile up on the server

		again := bot.join(sid)
		if again.ReplicatorPID == j.ReplicatorPID || again.LogPath != j.LogPath {
			t.Fatalf("round %d: rejoin %+v after %+v", round, again, j)
		}
		// The restarted client is the same participant: the server returns the first join.
		if !again.Duplicate || string(again.Event) != string(firstJoin) {
			t.Fatalf("round %d: rejoin is not the stored join: %s", round, again.Event)
		}
		j = again
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Session.EndSession(ctx10(t), &sessionv1.EndSessionRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: "writer"}); err != nil {
		t.Fatal(err)
	}
	// The replicator writes session.ended and stops.
	waitUntil(t, 10*time.Second, "replicator stops after session.ended", func() bool { return !alive(j.ReplicatorPID) })
	server := serverLog(t, writer, sid)
	if len(server) < total+3 {
		t.Fatalf("server has only %d events", len(server))
	}
	sameAsServer(t, j.LogPath, server)

	// A join after the end does not start a replicator that writes anything new.
	out, _, code := bot.run("meeting", "join", sid, "--json")
	if code == 0 {
		var again joined
		_ = json.Unmarshal([]byte(out), &again)
		killAtEnd(t, again.ReplicatorPID)
		waitUntil(t, 5*time.Second, "replicator of an ended session stops", func() bool { return !alive(again.ReplicatorPID) })
	}
	sameAsServer(t, j.LogPath, server)
}

func TestReplicatorTwoParticipantsOnOneMachine(t *testing.T) {
	m := newMachine(t)
	ada := m.user("ada", "Ada", "human")
	bot := m.user("bot", "Bot", "agent")
	sid := newSession()
	ja, jb := ada.join(sid), bot.join(sid)
	if ja.LogPath == jb.LogPath || ja.ReplicatorPID == jb.ReplicatorPID {
		t.Fatalf("two participants share a file or a replicator: %+v %+v", ja, jb)
	}
	for i := range 10 {
		u := ada
		if i%2 == 1 {
			u = bot
		}
		u.ok("say", "turn", string(rune('0'+i)))
	}
	ada.ok("meeting", "end")
	for _, j := range []joined{ja, jb} {
		waitUntil(t, 10*time.Second, "replicator stops", func() bool { return !alive(j.ReplicatorPID) })
	}
	server := serverLog(t, m.srv.Participant(t, "reader", "Reader", agent), sid)
	sameAsServer(t, ja.LogPath, server)
	sameAsServer(t, jb.LogPath, server)
}

func TestReplicatorStopsOnSessionEnded(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	sid := newSession()
	j := bot.join(sid)
	_, end := eventOf(t, bot.ok("meeting", "end"))
	waitUntil(t, 5*time.Second, "replicator stops", func() bool { return !alive(j.ReplicatorPID) })
	lines := readLog(t, j.LogPath)
	if last := lines[len(lines)-1]; last.ev.GetType() != events.SessionEnded || last.ev.GetId() != end.GetId() {
		t.Fatalf("last line: %s", last.raw)
	}
}

func TestReplicatorStopsWhenParentDies(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	sid := newSession()
	// In the foreground, join is the parent of the replicator and prints the path at once.
	fg := contracttest.Start(t, "office", bot.env, "meeting", "join", sid, "--foreground", "--json")
	type res struct {
		line string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		l, err := fg.Stdout.ReadString('\n')
		ch <- res{l, err}
	}()
	var j joined
	select {
	case r := <-ch:
		if r.err != nil || json.Unmarshal([]byte(r.line), &j) != nil || j.ReplicatorPID == 0 {
			t.Fatalf("foreground join: %q, %v\n%s", r.line, r.err, fg.Stderr())
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("foreground join printed nothing\n%s", fg.Stderr())
	}
	killAtEnd(t, j.ReplicatorPID)
	_, ev := eventOf(t, bot.ok("say", "hello"))
	waitLog(t, j.LogPath, ev.GetId())
	if !alive(j.ReplicatorPID) {
		t.Fatal("replicator is not running")
	}
	fg.Kill() // SIGKILL: the parent gets no chance to stop its child
	waitUntil(t, 5*time.Second, "replicator stops after its parent died", func() bool { return !alive(j.ReplicatorPID) })
	// Nothing is lost: a new join resumes after the last line.
	j2 := bot.join(sid)
	_, ev = eventOf(t, bot.ok("say", "after the parent died"))
	lines := waitLog(t, j2.LogPath, ev.GetId())
	for i, l := range lines {
		if l.ev.GetId() != uint32(i+1) {
			t.Fatalf("line %d has ID %d", i+1, l.ev.GetId())
		}
	}

	// A foreground join ends by itself when the session ends.
	bot.ok("meeting", "leave")
	fg2 := contracttest.Start(t, "office", bot.env, "meeting", "join", sid, "--foreground")
	if _, err := fg2.Stdout.ReadString('\n'); err != nil {
		t.Fatalf("foreground join: %v\n%s", err, fg2.Stderr())
	}
	bot.ok("meeting", "end", "--session", sid)
	select {
	case <-fg2.Done():
		if c := fg2.Cmd.ProcessState.ExitCode(); c != 0 {
			t.Fatalf("foreground join exit %d", c)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("foreground join did not end with the session")
	}
}

// TestReplicatorCatchesUpAfterNetworkDrop (qs-resume-after-drop): the replicator of the bot
// reaches the server through a proxy. The proxy is cut for 7 s while a writer appends events
// directly. Within 2 s after the network is back, the local log has all events, in order, without
// gaps or duplicates.
func TestReplicatorCatchesUpAfterNetworkDrop(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	proxy := contracttest.StartProxy(t, m.srv.URL)
	viaProxy := *bot
	viaProxy.env = append(append([]string{}, bot.env...), "OFFICE_HUB="+proxy.URL)
	writer := m.srv.Participant(t, "writer", "Writer", agent)
	sid := newSession()
	if _, err := writer.Session.Join(ctx10(t), &sessionv1.JoinRequest{SessionId: sid, CommandId: ids.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	j := viaProxy.join(sid)
	say := func(text string) uint32 {
		t.Helper()
		res, err := writer.Session.SubmitUtterance(ctx10(t), &sessionv1.SubmitUtteranceRequest{SessionId: sid,
			CommandId: ids.NewCommandID(), ParticipantId: "writer", Text: text, Modality: sessionv1.Modality_MODALITY_TEXT})
		if err != nil {
			t.Fatal(err)
		}
		return res.GetResult().GetEvent().GetId()
	}
	waitLog(t, j.LogPath, say("before the drop"))

	const drop = 7 * time.Second
	proxy.Cut()
	cutAt := time.Now()
	var last uint32
	for i := 0; time.Since(cutAt) < drop; i++ {
		last = say("during the drop " + string(rune('a'+i%26)))
		time.Sleep(100 * time.Millisecond)
	}
	if n := len(readLog(t, j.LogPath)); n > 0 && readLog(t, j.LogPath)[n-1].ev.GetId() >= last {
		t.Fatal("the local log got events while the network was cut")
	}
	proxy.Restore()
	restoredAt := time.Now()
	waitUntil(t, 10*time.Second, "local log catches up", func() bool {
		lines := readLog(t, j.LogPath)
		return len(lines) > 0 && lines[len(lines)-1].ev.GetId() >= last
	})
	catchUp := time.Since(restoredAt)
	t.Logf("network cut for %v (%d connections refused); local log complete %v after restore",
		drop, proxy.Refused(), catchUp.Round(time.Millisecond))
	if catchUp > 2*time.Second {
		t.Fatalf("local log complete only %v after the network is back, want at most 2 s", catchUp)
	}

	if _, err := writer.Session.EndSession(ctx10(t), &sessionv1.EndSessionRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: "writer"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "replicator stops after session.ended", func() bool { return !alive(j.ReplicatorPID) })
	sameAsServer(t, j.LogPath, serverLog(t, writer, sid))
}

package contract

// Contract tests of the MCP server (bb-mcp) through if-mcp: they start the real "office mcp"
// process, talk to it over stdio with the MCP Go SDK client, and check the tool results and the
// local log it writes (if-local-log). Other participants use the office CLI.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrsimpson/agentic-office/server/session/api/events"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// mcpServer is a running "office mcp" with a connected MCP client.
type mcpServer struct {
	t      *testing.T
	cmd    *exec.Cmd
	cs     *mcp.ClientSession
	stderr *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startMCP starts "office mcp" with the environment of u and connects an MCP client.
func startMCP(t *testing.T, u *officeUser, args ...string) *mcpServer {
	t.Helper()
	cmd := exec.Command(contracttest.Binary(t, "office"), append([]string{"mcp"}, args...)...)
	cmd.Env = append(os.Environ(), u.env...)
	s := &mcpServer{t: t, cmd: cmd, stderr: &syncBuffer{}}
	cmd.Stderr = s.stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "contract-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd, TerminateDuration: 10 * time.Second}, nil)
	if err != nil {
		t.Fatalf("connect to office mcp: %v\n%s", err, s.stderr)
	}
	s.cs = cs
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cs.Close()
		}
		if t.Failed() {
			t.Logf("stderr of office mcp:\n%s", s.stderr)
		}
	})
	return s
}

// call calls a tool. It returns the JSON object of the result and whether it is an error. The
// text content and the structured content must be the same object.
func (s *mcpServer) call(name string, args map[string]any) (map[string]any, bool) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if args == nil {
		args = map[string]any{}
	}
	res, err := s.cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		s.t.Fatalf("%s: %v", name, err)
	}
	if len(res.Content) != 1 {
		s.t.Fatalf("%s: want one content block, got %d", name, len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		s.t.Fatalf("%s: content is %T, want text", name, res.Content[0])
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(text.Text), &v); err != nil || strings.Contains(strings.TrimSpace(text.Text), "\n") {
		s.t.Fatalf("%s: want one JSON object as text, got %q", name, text.Text)
	}
	sc, _ := json.Marshal(res.StructuredContent)
	var w map[string]any
	if err := json.Unmarshal(sc, &w); err != nil {
		s.t.Fatalf("%s: structured content %s: %v", name, sc, err)
	}
	a, _ := json.Marshal(v)
	b, _ := json.Marshal(w)
	if !bytes.Equal(a, b) {
		s.t.Fatalf("%s: text and structured content differ:\n%s\n%s", name, a, b)
	}
	return v, res.IsError
}

// ok calls a tool that must succeed.
func (s *mcpServer) ok(name string, args map[string]any) map[string]any {
	s.t.Helper()
	v, isErr := s.call(name, args)
	if isErr || v["error"] != nil {
		s.t.Fatalf("%s %v: %v", name, args, v)
	}
	return v
}

// fails calls a tool that must fail with the given error code and a message.
func (s *mcpServer) fails(code, name string, args map[string]any) string {
	s.t.Helper()
	v, isErr := s.call(name, args)
	e, _ := v["error"].(map[string]any)
	if !isErr || e == nil || e["code"] != code || e["message"] == "" {
		s.t.Fatalf("%s %v: want error code %q, got isError=%v %v", name, args, code, isErr, v)
	}
	return e["message"].(string)
}

// close closes stdin, as an MCP client does at the end. The server must exit by itself.
func (s *mcpServer) close() time.Duration {
	s.t.Helper()
	start := time.Now()
	if err := s.cs.Close(); err != nil {
		s.t.Fatalf("close: %v\n%s", err, s.stderr)
	}
	return time.Since(start)
}

// mcpJoined is the result of meeting_join.
func mcpJoined(t *testing.T, v map[string]any) (logPath, pid string, replicatorPID int) {
	t.Helper()
	logPath, _ = v["logPath"].(string)
	pid, _ = v["participantId"].(string)
	f, _ := v["replicatorPid"].(float64)
	if logPath == "" || pid == "" || f == 0 {
		t.Fatalf("meeting_join: %v", v)
	}
	return logPath, pid, int(f)
}

// The tools of if-mcp. Names use "_" instead of "." (see README.md).
var mcpTools = []string{"meeting_join", "meeting_log", "meeting_leave", "meeting_pause", "meeting_resume", "meeting_end",
	"utterance_say", "utterance_off_the_record", "signal_turn_end", "signal_silence_long", "signal_addressed", "hand_raise",
	"minutes_update", "stage_show", "stage_link", "slides_render", "artifact_get", "journal_append", "journal_list", "whoami"}

// TestMCPToolsDescribeTheLog: the server offers every tool, and tells an agent how to follow
// the log file and which event types to filter for.
func TestMCPToolsDescribeTheLog(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	s := startMCP(t, bot)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var names []string
	var join *mcp.Tool
	for tool, err := range s.cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("tool %s has no description", tool.Name)
		}
		if tool.Name == "meeting_join" {
			join = tool
		}
	}
	for _, n := range mcpTools {
		if !slices.Contains(names, n) {
			t.Errorf("tool %s is missing (have %v)", n, names)
		}
	}
	if join == nil {
		t.Fatal("no meeting_join")
	}
	for _, want := range []string{"logPath", "tail -n +1 -f", `"type":"turn.end"`, `"type":"addressed"`, "jq", events.UtteranceFinal,
		events.UtteranceOffTheRecord, events.SessionEnded} {
		if !strings.Contains(join.Description, want) {
			t.Errorf("meeting_join description does not mention %q", want)
		}
	}
	if ins := s.cs.InitializeResult().Instructions; !strings.Contains(ins, "meeting_join") || !strings.Contains(ins, "tail -n +1 -f") {
		t.Errorf("instructions do not explain how to follow the log: %q", ins)
	}
}

// TestMCPJoinAndCommands: meeting_join returns a path that receives every event, also those of
// a participant who uses the CLI. Every session tool returns its event in the form of its line
// in the log, and the event appears there exactly once.
func TestMCPJoinAndCommands(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")
	sid := newSession()
	s := startMCP(t, bot)

	start := time.Now()
	logPath, pid, repPID := mcpJoined(t, s.ok("meeting_join", map[string]any{"session": sid}))
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("meeting_join took %v", d)
	}
	if pid != "bot" || !filepath.IsAbs(logPath) || !strings.HasPrefix(logPath, m.runtime+string(filepath.Separator)) {
		t.Fatalf("meeting_join: participant %q, path %q", pid, logPath)
	}
	if repPID != s.cmd.Process.Pid {
		t.Fatalf("the replicator must run in the MCP process %d, got %d", s.cmd.Process.Pid, repPID)
	}
	if fi, err := os.Stat(logPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("log file: %v, %v", fi, err)
	}
	// Joining again is the same participant and the same file.
	again := s.ok("meeting_join", map[string]any{"session": sid})
	if again["logPath"] != logPath || again["duplicate"] != true {
		t.Fatalf("second meeting_join: %v", again)
	}
	if v := s.ok("meeting_log", nil); v["logPath"] != logPath || v["replicatorRunning"] != true || v["sessionId"] != sid {
		t.Fatalf("meeting_log: %v", v)
	}
	if v := s.ok("whoami", nil); v["participantId"] != "bot" || v["keyId"] == "" {
		t.Fatalf("whoami: %v", v)
	}

	// Ada uses the CLI.
	ada.join(sid)
	_, adaUtt := eventOf(t, ada.ok("say", "Hello bot, can you summarize?"))
	waitLog(t, logPath, adaUtt.GetId())

	results := map[uint32]string{}
	do := func(wantType, name string, args map[string]any) map[string]any {
		t.Helper()
		v := s.ok(name, args)
		line, ev := eventOf(t, v)
		if ev.GetType() != wantType {
			t.Fatalf("%s: event type %s, want %s", name, ev.GetType(), wantType)
		}
		results[ev.GetId()] = line
		return v
	}
	_, utt := eventOf(t, do(events.UtteranceFinal, "utterance_say", map[string]any{"text": "I can summarize that."}))
	do(events.UtteranceFinal, "utterance_say", map[string]any{"text": "Second sentence.", "session": sid})
	do(events.HandRaised, "hand_raise", map[string]any{"topic": "budget"})
	do(events.MinutesUpdated, "minutes_update", map[string]any{"text": "Budget: agreed"})
	minutes := filepath.Join(t.TempDir(), "minutes.md")
	if err := os.WriteFile(minutes, []byte("line 1\nline 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ev := eventOf(t, do(events.MinutesUpdated, "minutes_update", map[string]any{"file": minutes}))
	if ev.GetMinutesUpdated().GetText() != "line 1\nline 2\n" {
		t.Fatalf("minutes from a file: %q", ev.GetMinutesUpdated().GetText())
	}
	do(events.ArtifactShown, "stage_show", map[string]any{"artifactId": "slides/42"})
	do(events.ParticipantPaused, "meeting_pause", nil)
	do(events.ParticipantResumed, "meeting_resume", nil)
	do(events.TurnEnd, "signal_turn_end", map[string]any{"participant": "ada"})
	do(events.SilenceLong, "signal_silence_long", map[string]any{"duration": "12s"})
	do(events.Addressed, "signal_addressed", map[string]any{"participant": "bot", "utteranceEventId": adaUtt.GetId()})
	_, ret := eventOf(t, do(events.UtteranceOffTheRecord, "utterance_off_the_record", map[string]any{"eventId": utt.GetId()}))
	if ret.GetUtteranceOffTheRecord().GetUtteranceEventId() != utt.GetId() {
		t.Fatalf("mark off the record points to %d, want %d", ret.GetUtteranceOffTheRecord().GetUtteranceEventId(), utt.GetId())
	}
	// last=true marks off the record the newest own sentence that is not off the record, read from the local log.
	_, lastUtt := eventOf(t, do(events.UtteranceFinal, "utterance_say", map[string]any{"text": "Oops."}))
	waitLog(t, logPath, lastUtt.GetId())
	_, last := eventOf(t, do(events.UtteranceOffTheRecord, "utterance_off_the_record", map[string]any{"last": true}))
	if last.GetUtteranceOffTheRecord().GetUtteranceEventId() != lastUtt.GetId() {
		t.Fatalf("mark off the record last points to %d, want %d", last.GetUtteranceOffTheRecord().GetUtteranceEventId(), lastUtt.GetId())
	}
	// meeting_events reads the events after a cursor from the server: no gaps, and the sentences
	// off the record are placeholders without text.
	got := s.ok("meeting_events", map[string]any{"after": 0})
	evs, _ := got["events"].([]any)
	if len(evs) < int(last.GetId()) {
		t.Fatalf("meeting_events: %d events, want at least %d", len(evs), last.GetId())
	}
	for i, raw := range evs {
		ev, _ := raw.(map[string]any)
		if id, _ := ev["id"].(float64); int(id) != i+1 {
			t.Fatalf("meeting_events: event %d has id %v", i+1, ev["id"])
		}
		if int(ev["id"].(float64)) == int(utt.GetId()) {
			u, _ := ev["utteranceFinal"].(map[string]any)
			if u["offTheRecord"] != true || u["text"] != nil {
				t.Fatalf("meeting_events: sentence off the record is not a placeholder: %v", ev)
			}
		}
	}
	page := s.ok("meeting_events", map[string]any{"after": utt.GetId() - 1, "limit": 1})
	if p, _ := page["events"].([]any); len(p) != 1 || page["more"] != true {
		t.Fatalf("meeting_events with limit 1: %v", page)
	}

	link := s.ok("stage_link", nil)
	if u, _ := link["url"].(string); !strings.HasPrefix(u, "http") {
		t.Fatalf("stage_link: %v", link)
	}
	if _, err := time.Parse(time.RFC3339, link["expiresAt"].(string)); err != nil {
		t.Fatalf("stage_link expiresAt: %v", link)
	}

	// Ada speaks again; the bot's file gets it, too.
	_, adaLast := eventOf(t, ada.ok("say", "Thanks."))
	var maxID uint32
	for id := range results {
		maxID = max(maxID, id)
	}
	lines := waitLog(t, logPath, max(maxID, adaLast.GetId()))
	seen := map[uint32]int{}
	for i, l := range lines {
		seen[l.ev.GetId()]++
		if l.ev.GetId() != uint32(i+1) {
			t.Fatalf("line %d has ID %d", i+1, l.ev.GetId())
		}
		if want, ok := results[l.ev.GetId()]; ok && want != l.raw {
			t.Fatalf("line %d differs from the tool result:\n got %s\nwant %s", i+1, l.raw, want)
		}
	}
	for id := range results {
		if seen[id] != 1 {
			t.Fatalf("event %d appears %d times", id, seen[id])
		}
	}
	if lines[adaLast.GetId()-1].ev.GetUtteranceFinal().GetText() != "Thanks." {
		t.Fatalf("CLI utterance in the MCP log: %s", lines[adaLast.GetId()-1].raw)
	}

	// The CLI sees the same replicator: the MCP process holds the lock on the file.
	if v := bot.ok("meeting", "log", "--session", sid); v["replicatorRunning"] != true {
		t.Fatalf("office meeting log while MCP runs: %v", v)
	}

	// Leave: the file ends with participant.left and the replicator stops. A new join restarts it.
	left := s.ok("meeting_leave", nil)
	_, leftEv := eventOf(t, left)
	if leftEv.GetType() != events.ParticipantLeft {
		t.Fatalf("meeting_leave: %v", left)
	}
	if v := s.ok("meeting_log", map[string]any{"session": sid}); v["replicatorRunning"] != false {
		t.Fatalf("meeting_log after leave: %v", v)
	}
	lines = readLog(t, logPath)
	if l := lines[len(lines)-1]; l.ev.GetId() != leftEv.GetId() {
		t.Fatalf("last line after leave: %s", l.raw)
	}
	_, _, repPID2 := mcpJoined(t, s.ok("meeting_join", map[string]any{"session": sid}))
	if repPID2 != s.cmd.Process.Pid {
		t.Fatalf("rejoin: replicator PID %d", repPID2)
	}
	_, ev = eventOf(t, s.ok("utterance_say", map[string]any{"text": "I am back."}))
	waitLog(t, logPath, ev.GetId())

	// meeting_end: session.ended is the last line, and the replicator stops.
	_, end := eventOf(t, s.ok("meeting_end", nil))
	lines = waitLog(t, logPath, end.GetId())
	if lines[len(lines)-1].ev.GetType() != events.SessionEnded {
		t.Fatalf("last line: %s", lines[len(lines)-1].raw)
	}
	waitUntil(t, 5*time.Second, "replicator stops after session.ended", func() bool {
		v := s.ok("meeting_log", map[string]any{"session": sid})
		return v["replicatorRunning"] == false
	})

	// Closing stdin ends the server by itself (no SIGTERM needed).
	if d := s.close(); d > 5*time.Second {
		t.Fatalf("office mcp took %v to exit after stdin closed", d)
	}
	if c := s.cmd.ProcessState.ExitCode(); c != 0 {
		t.Fatalf("office mcp exit code %d", c)
	}
}

// TestMCPReplicatorStopsWithTheProcess: the replication lives as long as the MCP process. When
// the client closes the connection or the process is killed, no writer is left and the file
// stops growing. A new join resumes the file without gaps.
func TestMCPReplicatorStopsWithTheProcess(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")
	sid := newSession()
	ada.join(sid)

	stopped := func(how string, stop func(s *mcpServer)) string {
		t.Helper()
		s := startMCP(t, bot)
		logPath, _, _ := mcpJoined(t, s.ok("meeting_join", map[string]any{"session": sid}))
		_, ev := eventOf(t, ada.ok("say", "before "+how))
		waitLog(t, logPath, ev.GetId())
		stop(s)
		if alive(s.cmd.Process.Pid) {
			t.Fatalf("%s: office mcp still runs", how)
		}
		// No writer remains: the lock on the file is free.
		if v := bot.ok("meeting", "log", "--session", sid); v["replicatorRunning"] != false {
			t.Fatalf("%s: a replicator still holds the file: %v", how, v)
		}
		size := func() int64 {
			fi, err := os.Stat(logPath)
			if err != nil {
				t.Fatal(err)
			}
			return fi.Size()
		}
		before := size()
		_, ev = eventOf(t, ada.ok("say", "after "+how))
		waitLog(t, filepath.Join(m.runtime, sid, "ada.jsonl"), ev.GetId())
		time.Sleep(500 * time.Millisecond)
		if after := size(); after != before {
			t.Fatalf("%s: the file grew from %d to %d bytes after the MCP process ended", how, before, after)
		}
		return logPath
	}
	stopped("close", func(s *mcpServer) { s.close() })
	logPath := stopped("SIGKILL", func(s *mcpServer) {
		_ = s.cmd.Process.Kill()
		_ = s.cs.Close() // reaps the process
	})

	// A new MCP process resumes the file after the last complete line.
	s := startMCP(t, bot)
	s.ok("meeting_join", map[string]any{"session": sid})
	_, ev := eventOf(t, ada.ok("say", "resumed"))
	lines := waitLog(t, logPath, ev.GetId())
	for i, l := range lines {
		if l.ev.GetId() != uint32(i+1) {
			t.Fatalf("line %d has ID %d", i+1, l.ev.GetId())
		}
	}
}

// TestMCPErrors: every tool returns a clear error for bad input, for errors of the server and
// for services that the server does not implement yet. Errors have the form of the CLI's
// --json errors and set isError.
func TestMCPErrors(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")
	s := startMCP(t, bot)

	// Before a join there is no default session.
	s.fails("no_session", "utterance_say", map[string]any{"text": "hello"})
	s.fails("no_session", "meeting_log", nil)

	sid := newSession()
	s.ok("meeting_join", map[string]any{"session": sid})
	ada.join(sid)
	_, adaUtt := eventOf(t, ada.ok("say", "Mine."))

	// Bad input: missing, empty or unknown arguments and wrong values.
	usage := []struct {
		tool string
		args map[string]any
	}{
		{"meeting_join", nil},
		{"meeting_join", map[string]any{"session": " "}},
		{"utterance_say", nil},
		{"utterance_say", map[string]any{"text": ""}},
		{"utterance_say", map[string]any{"txt": "typo"}},
		{"utterance_say", map[string]any{"text": 42}},
		{"utterance_off_the_record", nil},
		{"utterance_off_the_record", map[string]any{"eventId": 1, "last": true}},
		{"utterance_off_the_record", map[string]any{"eventId": -1}},
		{"signal_silence_long", map[string]any{"duration": "long"}},
		{"signal_silence_long", map[string]any{"duration": "-3s"}},
		{"signal_addressed", map[string]any{"participant": "ada"}},
		{"minutes_update", nil},
		{"minutes_update", map[string]any{"text": "a", "file": "b"}},
		{"stage_show", map[string]any{"artifactId": ""}},
		{"slides_render", nil},
		{"slides_render", map[string]any{"markdown": "# t\n## s", "slides": []any{map[string]any{"title": "x"}}}},
		{"slides_render", map[string]any{"markdown": "text before any slide"}},
		{"artifact_get", nil},
		{"journal_append", map[string]any{"kind": "gossip", "text": "x"}},
		{"journal_append", map[string]any{"kind": "decision", "text": ""}},
		{"journal_list", map[string]any{"kind": "gossip"}},
		{"whoami", map[string]any{"extra": 1}},
	}
	for _, u := range usage {
		s.fails("usage", u.tool, u.args)
	}

	// Errors of the server keep their Connect code.
	s.fails("permission_denied", "utterance_off_the_record", map[string]any{"eventId": adaUtt.GetId()})

	// Errors of the artifact tools keep their Connect code too.
	s.fails("not_found", "artifact_get", map[string]any{"id": "slides/99"})
	s.fails("invalid_argument", "slides_render", map[string]any{"slides": []any{map[string]any{"title": " "}}})

	// After the end of the session, commands fail with the server's code.
	s.ok("meeting_end", nil)
	s.fails("failed_precondition", "utterance_say", map[string]any{"text": "too late", "session": sid})

	// An unknown tool is a protocol error.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.cs.CallTool(ctx, &mcp.CallToolParams{Name: "meeting.join", Arguments: map[string]any{}}); err == nil {
		t.Fatal("an unknown tool must fail")
	}

	// Without a key file, every tool says so; the server still starts.
	nokey := m.userWithKey(filepath.Join(t.TempDir(), "missing.pem"))
	s2 := startMCP(t, nokey)
	msg := s2.fails("no_key", "meeting_join", map[string]any{"session": sid})
	if !strings.Contains(msg, "office key create") {
		t.Fatalf("no_key message: %q", msg)
	}
	s2.fails("no_key", "whoami", nil)
}

// TestMCPJournal: journal_append and journal_list against the real journal, and the minutes that
// meeting_end stores in it.
func TestMCPJournal(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")
	s := startMCP(t, bot)

	v := s.ok("journal_append", map[string]any{"kind": "decision", "text": "We ship."})
	e, _ := v["entry"].(map[string]any)
	if v["duplicate"] != false || e["id"] != float64(1) || e["kind"] != "ENTRY_KIND_DECISION" ||
		e["authorParticipantId"] != "bot" || e["authorKind"] != "AUTHOR_KIND_AGENT" || e["sessionId"] != nil {
		t.Fatalf("journal_append: %v", v)
	}
	ada.ok("journal", "append", "--kind", "task", "--session", "s1", "Ada writes the plan.")
	s.ok("journal_append", map[string]any{"kind": "note", "text": "A note.", "session": "s1"})

	ids := func(args map[string]any) string {
		t.Helper()
		var got []string
		for _, e := range entriesOf(t, s.ok("journal_list", args)) {
			got = append(got, fmt.Sprint(e["id"]))
		}
		return strings.Join(got, ",")
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{nil, "1,2,3"},
		{map[string]any{"kind": "task"}, "2"},
		{map[string]any{"session": "s1"}, "2,3"},
		{map[string]any{"afterEntryId": 1, "limit": 1}, "2"},
		{map[string]any{"afterEntryId": 3}, ""},
	} {
		if got := ids(c.args); got != c.want {
			t.Fatalf("journal_list %v: %s, want %s", c.args, got, c.want)
		}
	}

	// The minutes of the session go into the journal when it ends.
	sid := newSession()
	s.ok("meeting_join", map[string]any{"session": sid})
	s.ok("minutes_update", map[string]any{"text": "Final: we ship"})
	s.ok("meeting_end", nil)
	got := entriesOf(t, s.ok("journal_list", map[string]any{"session": sid, "kind": "minutes"}))
	if len(got) != 1 || got[0]["text"] != "Final: we ship" || got[0]["authorParticipantId"] != "bot" {
		t.Fatalf("minutes of the session: %v", got)
	}
}

// TestMCPWithCLIReplicator: if "office meeting join" already runs a replicator for the same
// participant, meeting_join reuses it and reports its PID; the file keeps one writer.
// TestMCPArtifacts: slides_render and artifact_get against the real artifact tools. The three
// forms of a deck give the same content.
func TestMCPArtifacts(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")
	s := startMCP(t, bot)
	dir := t.TempDir()
	deck := filepath.Join(dir, "deck.md")
	if err := os.WriteFile(deck, []byte("# Budget\n\n## Plan\n- one\n- <two>\nsay this\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forms := []map[string]any{
		{"title": "Budget", "slides": []any{map[string]any{"title": "Plan", "bullets": []any{"one", "<two>"}, "notes": "say this"}}},
		{"markdown": "# Budget\n## Plan\n- one\n- <two>\nsay this\n"},
		{"file": deck},
	}
	var contents []string
	for i, f := range forms {
		v := s.ok("slides_render", f)
		id := fmt.Sprintf("slides/%d", i+1)
		if v["artifactId"] != id || v["mediaType"] != "text/html; charset=utf-8" || v["duplicate"] != false {
			t.Fatalf("slides_render %v: %v", f, v)
		}
		a, _ := s.ok("artifact_get", map[string]any{"id": id})["artifact"].(map[string]any)
		if a["id"] != id || a["authorParticipantId"] != "bot" {
			t.Fatalf("artifact_get %s: %v", id, a)
		}
		b, err := base64.StdEncoding.DecodeString(a["content"].(string))
		if err != nil {
			t.Fatal(err)
		}
		contents = append(contents, string(b))
	}
	if contents[0] != contents[1] || contents[1] != contents[2] || !strings.Contains(contents[0], "<li>&lt;two&gt;</li>") {
		t.Fatalf("three forms of one deck, other content:\n%s", strings.Join(contents, "\n---\n"))
	}
	// The CLI of another participant sees the same artifact.
	if html, _, code := ada.run("artifact", "get", "slides/3"); code != 0 || html != contents[0] {
		t.Fatalf("CLI get of an MCP artifact: exit %d", code)
	}
	out := filepath.Join(dir, "out.html")
	if v := s.ok("artifact_get", map[string]any{"id": "slides/1", "out": out}); v["file"] != out {
		t.Fatalf("artifact_get with out: %v", v)
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != contents[0] {
		t.Fatalf("out file: %v", err)
	}
}

func TestMCPWithCLIReplicator(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	sid := newSession()
	j := bot.join(sid)
	s := startMCP(t, bot, "--session", sid)
	logPath, _, repPID := mcpJoined(t, s.ok("meeting_join", map[string]any{"session": sid}))
	if logPath != j.LogPath || repPID != j.ReplicatorPID {
		t.Fatalf("meeting_join with a CLI replicator: path %s pid %d, want %s pid %d", logPath, repPID, j.LogPath, j.ReplicatorPID)
	}
	// --session sets the default session of the tools.
	_, ev := eventOf(t, s.ok("utterance_say", map[string]any{"text": "via MCP"}))
	if ev.GetSessionId() != sid {
		t.Fatalf("session %s, want %s", ev.GetSessionId(), sid)
	}
	waitLog(t, logPath, ev.GetId())
	s.close()
	// The CLI replicator is not this process: it keeps running.
	if !alive(j.ReplicatorPID) {
		t.Fatal("the CLI replicator stopped with the MCP process")
	}
	_, ev = eventOf(t, bot.ok("say", "via CLI"))
	waitLog(t, logPath, ev.GetId())
}

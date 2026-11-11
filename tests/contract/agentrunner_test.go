package contract

// Contract tests of the agent runner (bb-agent-runner) through its interfaces: the command
// "office agent run" (if-cli) and the client side of the Agent Client Protocol (if-acp-client).
// The harness is the ACP test double from tests/acpdouble; it calls no model. The hub and the
// MCP server that the double starts are real.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// agentRun starts "office agent run" for u with the ACP test double as the harness. It returns
// the process and the record file of the double. A prompt with "please hold" waits until the
// file <record file>.release exists.
func (u *officeUser) agentRun(t *testing.T, sid string, flags ...string) (*contracttest.Process, string) {
	t.Helper()
	rec := filepath.Join(t.TempDir(), "acp-record.jsonl")
	args := append([]string{"agent", "run", "--session", sid}, flags...)
	args = append(args, "--", contracttest.ACPDouble(t))
	p := contracttest.Start(t, "office", append(u.env, "ACP_DOUBLE_RECORD="+rec, "ACP_DOUBLE_RELEASE="+rec+".release"), args...)
	return p, rec
}

// utterancesOf returns the texts of the utterance.final events of participant pid in a log.
func utterancesOf(lines []logLine, pid string) []string {
	var out []string
	for _, l := range lines {
		if u := l.ev.GetUtteranceFinal(); u != nil && u.GetParticipantId() == pid {
			out = append(out, u.GetText())
		}
	}
	return out
}

// waitUtterance waits until the log at path has an utterance of pid with this text.
func waitUtterance(t *testing.T, path, pid, text string) []logLine {
	t.Helper()
	var lines []logLine
	waitUntil(t, 20*time.Second, pid+" says "+text, func() bool {
		lines = readLog(t, path)
		for _, s := range utterancesOf(lines, pid) {
			if s == text {
				return true
			}
		}
		return false
	})
	return lines
}

// TestAgentRunnerHarness: the runner joins with the agent's key, starts the harness, opens one
// ACP session with the office MCP server, sends the role in a first prompt, answers permission
// requests by the default rule, and stops the harness when it ends.
func TestAgentRunnerHarness(t *testing.T) {
	m := newMachine(t)
	ada, bot := m.user("ada", "Ada", "human"), m.user("bot", "Bot", "agent")
	sid := newSession()
	adaLog := ada.join(sid).LogPath

	role := `You are Bot, the minute taker. At the start, please say "Hello, I am Bot." and then please run "rm -rf /tmp/x".`
	p, rec := bot.agentRun(t, sid, "--role", role)

	// The agent speaks through the MCP server that the runner handed to the harness.
	lines := waitUtterance(t, adaLog, "bot", "Hello, I am Bot.")
	joinedBot := false
	for _, l := range lines {
		if j := l.ev.GetParticipantJoined(); j != nil && j.GetParticipantId() == "bot" {
			joinedBot = true
		}
	}
	if !joinedBot {
		t.Fatal("the runner did not join the session as the agent")
	}

	_, recs := contracttest.WaitACPRecord(t, rec, 10*time.Second, func(r contracttest.ACPRecord) bool {
		return r.Kind() == "permission" && strings.HasPrefix(r["title"].(string), "Run ")
	})
	var kinds []string
	for _, r := range recs {
		kinds = append(kinds, r.Kind())
		switch r.Kind() {
		case "initialize":
			params := r["params"].(map[string]any)
			if params["protocolVersion"] != float64(1) {
				t.Errorf("initialize: protocol version %v", params["protocolVersion"])
			}
			caps, _ := json.Marshal(params["clientCapabilities"])
			if strings.Contains(string(caps), "true") {
				t.Errorf("the runner must offer no file system and no terminal: %s", caps)
			}
		case "session/new":
			params := r["params"].(map[string]any)
			if cwd, _ := params["cwd"].(string); !filepath.IsAbs(cwd) {
				t.Errorf("session/new: cwd %q is not absolute", cwd)
			}
			servers := params["mcpServers"].([]any)
			if len(servers) != 1 {
				t.Fatalf("session/new: want one MCP server, got %v", servers)
			}
			s := servers[0].(map[string]any)
			if s["name"] != "office" || filepath.Base(s["command"].(string)) != "office" {
				t.Errorf("MCP server: %v", s)
			}
			if args, _ := json.Marshal(s["args"]); string(args) != `["mcp","--session","`+sid+`"]` {
				t.Errorf("MCP server args: %s", args)
			}
			env := map[string]string{}
			for _, e := range s["env"].([]any) {
				kv := e.(map[string]any)
				env[kv["name"].(string)] = kv["value"].(string)
			}
			if env["OFFICE_KEY"] != bot.key || env["OFFICE_HUB"] != m.srv.URL || env["OFFICE_RUNTIME_DIR"] == "" {
				t.Errorf("MCP server env: %v", env)
			}
		case "prompt":
			if r["n"] == float64(1) {
				for _, want := range []string{sid, role, "utterance_say", "off the record"} {
					if !strings.Contains(r.Text(), want) {
						t.Errorf("first prompt lacks %q:\n%s", want, r.Text())
					}
				}
			}
		case "permission":
			switch title := r["title"].(string); {
			case title == "mcp__office__utterance_say":
				if r["outcome"] != "selected:allow" || r["toolError"] != nil {
					t.Errorf("office tool: %v", r)
				}
			case strings.HasPrefix(title, "Run "):
				if r["outcome"] != "selected:reject" {
					t.Errorf("a shell command must be rejected by default: %v", r)
				}
			}
		}
	}
	if strings.Join(kinds[:3], " ") != "start initialize session/new" {
		t.Errorf("order of the ACP steps: %v", kinds)
	}

	// SIGTERM stops the runner, and the runner stops the harness.
	if code, err := p.Stop(15 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: exit %d, %v\n%s", code, err, p.Stderr())
	}
	recs = contracttest.ReadACPRecords(t, rec)
	if recs[len(recs)-1].Kind() != "exit" {
		t.Errorf("the harness did not end when the runner stopped: %v", recs[len(recs)-1])
	}
	if pid, ok := recs[0]["pid"].(float64); ok && alive(int(pid)) {
		t.Errorf("the harness process %d still runs", int(pid))
	}
}

// TestAgentRunnerAllow: --allow replaces the default rule; "*" allows every tool.
func TestAgentRunnerAllow(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	sid := newSession()
	_, rec := bot.agentRun(t, sid, "--allow", "run *", "--role", `please run "make test" and please say "not allowed"`)
	_, recs := contracttest.WaitACPRecord(t, rec, 20*time.Second, func(r contracttest.ACPRecord) bool {
		return r.Kind() == "prompt_end"
	})
	got := map[string]string{}
	for _, r := range recs {
		if r.Kind() == "permission" {
			got[r["title"].(string)] = r["outcome"].(string)
		}
	}
	if got["Run make test"] != "selected:allow" || got["mcp__office__utterance_say"] != "selected:reject" {
		t.Errorf("permissions with --allow: %v", got)
	}
}

// TestAgentRunnerRoleFile: the role can come from a file.
func TestAgentRunnerRoleFile(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	f := filepath.Join(t.TempDir(), "role.md")
	if err := os.WriteFile(f, []byte("You keep the minutes of the weekly meeting."), 0o600); err != nil {
		t.Fatal(err)
	}
	_, rec := bot.agentRun(t, newSession(), "--role-file", f)
	r, _ := contracttest.WaitACPRecord(t, rec, 20*time.Second, func(r contracttest.ACPRecord) bool { return r.Kind() == "prompt" })
	if !strings.Contains(r.Text(), "You keep the minutes of the weekly meeting.") {
		t.Errorf("the role file is not in the first prompt:\n%s", r.Text())
	}
}

// TestAgentRunnerErrors: usage errors, and a harness that stops.
func TestAgentRunnerErrors(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	for _, args := range [][]string{
		{"agent", "run", "--session", "s1"}, // no harness
		{"agent", "run", "--", "true"},      // no session
		{"agent", "run", "--session", "s1", "--role", "a", "--role-file", "b", "--", "true"}, // both roles
	} {
		if _, errOut, code := bot.run(args...); code != 2 {
			t.Errorf("office %v: want exit 2, got %d: %s", args, code, errOut)
		}
	}

	// A harness that is not an ACP agent ends at once: the runner fails and says so.
	_, errOut, code := bot.run("agent", "run", "--session", newSession(), "--", "true")
	if code != 1 || !strings.Contains(errOut, "harness") {
		t.Fatalf("harness that stops: exit %d, %q", code, errOut)
	}
	_, errOut, code = bot.run("agent", "run", "--session", newSession(), "--", filepath.Join(t.TempDir(), "missing"))
	if code != 1 || !strings.Contains(errOut, "start the harness") {
		t.Fatalf("missing harness: exit %d, %q", code, errOut)
	}
}

// TestAgentRunnerSessionEnds: the runner stops by itself after session.ended.
func TestAgentRunnerSessionEnds(t *testing.T) {
	m := newMachine(t)
	ada, bot := m.user("ada", "Ada", "human"), m.user("bot", "Bot", "agent")
	sid := newSession()
	adaLog := ada.join(sid).LogPath
	p, rec := bot.agentRun(t, sid, "--role", `please say "ready"`)
	waitUtterance(t, adaLog, "bot", "ready")
	ada.ok("say", "Final words.", "--session", sid) // no turn.end: only the last prompt carries it
	ada.ok("meeting", "end", "--session", sid)
	select {
	case <-p.Done():
	case <-time.After(20 * time.Second):
		t.Fatalf("the runner did not stop after session.ended:\n%s", p.Stderr())
	}
	if code := p.Cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, p.Stderr())
	}
	recs := contracttest.ReadACPRecords(t, rec)
	if recs[len(recs)-1].Kind() != "exit" {
		t.Errorf("the harness did not end: %v", recs[len(recs)-1])
	}
	ps := promptTexts(recs)
	if len(ps) != 2 {
		t.Fatalf("want the first and the last prompt, got %d:\n%s", len(ps), strings.Join(ps, "\n---\n"))
	}
	if last := ps[1]; !strings.Contains(last, "has ended") || !strings.Contains(last, "Ada (human): Final words.") {
		t.Errorf("last prompt:\n%s", last)
	}
}

// promptTexts returns the texts of all prompts, in order.
func promptTexts(recs []contracttest.ACPRecord) []string {
	var out []string
	for _, r := range recs {
		if r.Kind() == "prompt" {
			out = append(out, r.Text())
		}
	}
	return out
}

// waitPrompt waits until the double has received prompt n and returns all prompt texts.
func waitPrompt(t *testing.T, rec string, n int) []string {
	t.Helper()
	_, recs := contracttest.WaitACPRecord(t, rec, 20*time.Second, func(r contracttest.ACPRecord) bool {
		return r.Kind() == "prompt" && r["n"] == float64(n)
	})
	return promptTexts(recs)
}

// waitPromptEnd waits until prompt n has ended.
func waitPromptEnd(t *testing.T, rec string, n int) {
	t.Helper()
	contracttest.WaitACPRecord(t, rec, 20*time.Second, func(r contracttest.ACPRecord) bool {
		return r.Kind() == "prompt_end" && r["n"] == float64(n)
	})
}

// meetingOf is a session with Ada (human), Carl (agent, CLI) and Bot (agent, runner).
type meetingOf struct {
	t              *testing.T
	sid            string
	ada, carl, bot *officeUser
	adaLog, botLog string
	runner         *contracttest.Process
	rec            string
}

func newMeetingWithRunner(t *testing.T, flags ...string) *meetingOf {
	t.Helper()
	return newMeetingWithRunnerEnv(t, nil, flags...)
}

// newMeetingWithRunnerEnv is newMeetingWithRunner with extra environment variables for the
// runner.
func newMeetingWithRunnerEnv(t *testing.T, env []string, flags ...string) *meetingOf {
	t.Helper()
	m := newMachine(t)
	mt := &meetingOf{t: t, sid: newSession(), ada: m.user("ada", "Ada", "human"), carl: m.user("carl", "Carl", "agent"),
		bot: m.user("bot", "Bot", "agent")}
	mt.bot.env = append(mt.bot.env, env...)
	mt.adaLog = mt.ada.join(mt.sid).LogPath
	mt.carl.join(mt.sid)
	mt.runner, mt.rec = mt.bot.agentRun(t, mt.sid, append([]string{"--role", `You are Bot. please say "Bot is here."`}, flags...)...)
	waitUtterance(t, mt.adaLog, "bot", "Bot is here.")
	waitPromptEnd(t, mt.rec, 1)
	mt.botLog = filepath.Join(m.runtime, mt.sid, "bot.jsonl")
	return mt
}

// say lets u say text and returns the event ID.
func (mt *meetingOf) say(u *officeUser, text string) uint32 {
	mt.t.Helper()
	_, ev := eventOf(mt.t, u.ok("say", text, "--session", mt.sid))
	return ev.GetId()
}

// turnEnd ends the turn of u and returns the event ID.
func (mt *meetingOf) turnEnd(u *officeUser) uint32 {
	mt.t.Helper()
	_, ev := eventOf(mt.t, u.ok("signal", "turn-end", "--session", mt.sid))
	return ev.GetId()
}

// count returns how often s occurs in all prompts.
func count(prompts []string, s string) int {
	n := 0
	for _, p := range prompts {
		n += strings.Count(p, s)
	}
	return n
}

// TestAgentRunnerTurns: at each change of speaker the agent gets what others said since the last
// delivery, in order, with who spoke and their kind; never its own utterances; nothing twice. A
// turn without new utterances wakes nobody.
func TestAgentRunnerTurns(t *testing.T) {
	mt := newMeetingWithRunner(t)

	mt.say(mt.ada, "Good morning.")
	mt.say(mt.ada, `Bot, please say "Good morning, Ada."`)
	mt.turnEnd(mt.ada)
	waitUtterance(t, mt.adaLog, "bot", "Good morning, Ada.")
	ps := waitPrompt(t, mt.rec, 2)
	p2 := ps[1]
	i, j := strings.Index(p2, "Ada (human): Good morning."), strings.Index(p2, `Ada (human): Bot, please say "Good morning, Ada."`)
	if i < 0 || j < 0 || i > j {
		t.Fatalf("prompt 2 must list both sentences of Ada in order:\n%s", p2)
	}
	if !strings.Contains(p2, "The turn of Ada (human) has ended.") || strings.Contains(p2, "Bot is here.") {
		t.Fatalf("prompt 2:\n%s", p2)
	}
	waitPromptEnd(t, mt.rec, 2)

	mt.say(mt.carl, "I have the numbers.")
	mt.turnEnd(mt.carl)
	ps = waitPrompt(t, mt.rec, 3)
	p3 := ps[2]
	if !strings.Contains(p3, "Carl (agent): I have the numbers.") || !strings.Contains(p3, "The turn of Carl (agent) has ended.") {
		t.Fatalf("prompt 3:\n%s", p3)
	}
	if strings.Contains(p3, "Good morning") {
		t.Fatalf("prompt 3 repeats earlier or own utterances:\n%s", p3)
	}
	waitPromptEnd(t, mt.rec, 3)

	// A change of speaker without new utterances wakes nobody; the next one with an utterance does.
	mt.turnEnd(mt.ada)
	last := mt.turnEnd(mt.carl)
	waitLog(t, mt.botLog, last)
	mt.say(mt.ada, "Next topic.")
	mt.turnEnd(mt.ada)
	ps = waitPrompt(t, mt.rec, 4)
	if !strings.Contains(ps[3], "Ada (human): Next topic.") {
		t.Fatalf("prompt 4:\n%s", ps[3])
	}
	for _, s := range []string{"Good morning.", "I have the numbers.", "Next topic."} {
		if n := count(ps, s); n != 1 {
			t.Errorf("%q is in %d prompts, want 1", s, n)
		}
	}
}

// TestAgentRunnerBusy: while the agent works on a prompt, further turns are queued and sent
// together in one prompt afterwards; an utterance after the last turn.end waits for the next one.
func TestAgentRunnerBusy(t *testing.T) {
	mt := newMeetingWithRunner(t)

	mt.say(mt.ada, "Bot, please hold.")
	mt.turnEnd(mt.ada)
	contracttest.WaitACPRecord(t, mt.rec, 20*time.Second, func(r contracttest.ACPRecord) bool { return r.Kind() == "hold" })
	mt.say(mt.ada, "One.")
	mt.turnEnd(mt.ada)
	mt.say(mt.carl, "Two.")
	mt.turnEnd(mt.carl)
	three := mt.say(mt.ada, "Three.")
	waitLog(t, mt.botLog, three)
	time.Sleep(500 * time.Millisecond) // let the runner read the log before the agent is free again
	if ps := promptTexts(contracttest.ReadACPRecords(t, mt.rec)); len(ps) != 2 {
		t.Fatalf("a prompt was sent while the agent was busy: %d prompts", len(ps))
	}
	if err := os.WriteFile(mt.rec+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ps := waitPrompt(t, mt.rec, 3)
	p3 := ps[2]
	if !strings.Contains(p3, "Ada (human): One.") || !strings.Contains(p3, "Carl (agent): Two.") ||
		strings.Index(p3, "One.") > strings.Index(p3, "Two.") || strings.Contains(p3, "Three.") {
		t.Fatalf("prompt 3 must carry the queued turns in order, without the open turn:\n%s", p3)
	}
	waitPromptEnd(t, mt.rec, 3)
	mt.turnEnd(mt.ada)
	ps = waitPrompt(t, mt.rec, 4)
	if !strings.Contains(ps[3], "Ada (human): Three.") {
		t.Fatalf("prompt 4:\n%s", ps[3])
	}
	for _, s := range []string{"please hold", "One.", "Two.", "Three."} {
		if n := count(ps, s); n != 1 {
			t.Errorf("%q is in %d prompts, want 1", s, n)
		}
	}
}

// TestAgentRunnerOffTheRecord: a sentence that goes off the record before delivery never reaches
// the agent; for a delivered one, the next prompt says that it is off the record.
func TestAgentRunnerOffTheRecord(t *testing.T) {
	mt := newMeetingWithRunner(t)

	mt.say(mt.ada, "The salary of Bob is 5000.")
	mt.ada.ok("utterance", "off-the-record", "--last", "--session", mt.sid)
	mt.say(mt.ada, "Public sentence.")
	mt.turnEnd(mt.ada)
	ps := waitPrompt(t, mt.rec, 2)
	if !strings.Contains(ps[1], "Public sentence.") || strings.Contains(ps[1], "salary") {
		t.Fatalf("prompt 2:\n%s", ps[1])
	}
	waitPromptEnd(t, mt.rec, 2)

	secret := mt.say(mt.ada, "Second secret.")
	mt.turnEnd(mt.ada)
	waitPrompt(t, mt.rec, 3)
	waitPromptEnd(t, mt.rec, 3)
	mt.ada.ok("utterance", "off-the-record", strconv.Itoa(int(secret)), "--session", mt.sid)
	mt.say(mt.ada, "After.")
	mt.turnEnd(mt.ada)
	ps = waitPrompt(t, mt.rec, 4)
	if want := fmt.Sprintf("[%d]. Do not use or repeat them", secret); !strings.Contains(ps[3], want) ||
		!strings.Contains(ps[3], "Ada (human): After.") || strings.Contains(ps[3], "Second secret.") {
		t.Fatalf("prompt 4 must name [%d] as off the record:\n%s", secret, ps[3])
	}
}

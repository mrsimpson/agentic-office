package contract

// Contract tests of what the agent runner delivers to the agent, and when
// (dec-wake-check-per-utterance, dec-agent-runner-delivery). This file covers the runner without
// a decision model: it wakes the agent at each change of speaker. The harness is the ACP test
// double from tests/acpdouble. The tests wait for records and log lines, never for time.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

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

// newMeetingWithRunner starts the meeting and the runner for Bot with flags. env are extra
// environment variables for the runner. It returns when the first prompt has ended.
func newMeetingWithRunner(t *testing.T, env []string, flags ...string) *meetingOf {
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

// waitStderr waits until the runner has logged s.
func (mt *meetingOf) waitStderr(s string) {
	mt.t.Helper()
	waitUntil(mt.t, 20*time.Second, "the runner logs "+s, func() bool { return strings.Contains(mt.runner.Stderr(), s) })
}

// release ends a "please hold" of the ACP test double.
func (mt *meetingOf) release() {
	mt.t.Helper()
	if err := os.WriteFile(mt.rec+".release", nil, 0o600); err != nil {
		mt.t.Fatal(err)
	}
}

// count returns how often s occurs in all prompts.
func count(prompts []string, s string) int {
	n := 0
	for _, p := range prompts {
		n += strings.Count(p, s)
	}
	return n
}

// TestAgentRunnerTurns: without a decision model, the runner says so at start and wakes the
// agent at each change of speaker, not at each utterance. The agent gets what others said since
// the last delivery, in order, with who spoke and their kind; never its own utterances; nothing
// twice. A turn without new utterances wakes nobody.
func TestAgentRunnerTurns(t *testing.T) {
	mt := newMeetingWithRunner(t, nil)
	mt.waitStderr("a new topic within a monologue is not detected")

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
// together in one prompt afterwards. Nothing is lost and nothing is sent twice.
func TestAgentRunnerBusy(t *testing.T) {
	mt := newMeetingWithRunner(t, nil)

	mt.say(mt.ada, "Bot, please hold.")
	mt.turnEnd(mt.ada)
	contracttest.WaitACPRecord(t, mt.rec, 20*time.Second, func(r contracttest.ACPRecord) bool { return r.Kind() == "hold" })
	mt.say(mt.ada, "One.")
	mt.turnEnd(mt.ada)
	mt.say(mt.carl, "Two.")
	mt.turnEnd(mt.carl)
	mt.waitStderr("the agent is busy; 2 utterances wait for it")
	if ps := promptTexts(contracttest.ReadACPRecords(t, mt.rec)); len(ps) != 2 {
		t.Fatalf("a prompt was sent while the agent was busy: %d prompts", len(ps))
	}
	mt.release()
	ps := waitPrompt(t, mt.rec, 3)
	p3 := ps[2]
	if !strings.Contains(p3, "Ada (human): One.") || !strings.Contains(p3, "Carl (agent): Two.") ||
		strings.Index(p3, "One.") > strings.Index(p3, "Two.") {
		t.Fatalf("prompt 3 must carry the queued turns in order:\n%s", p3)
	}
	waitPromptEnd(t, mt.rec, 3)
	mt.say(mt.ada, "Three.")
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
// the agent, and neither does its placeholder; for a delivered one, the runner tells the agent
// at once that it is off the record.
func TestAgentRunnerOffTheRecord(t *testing.T) {
	mt := newMeetingWithRunner(t, nil)

	mt.say(mt.ada, "The salary of Bob is 5000.")
	mt.ada.ok("utterance", "off-the-record", "--last", "--session", mt.sid)
	mt.say(mt.ada, "Public sentence.")
	mt.turnEnd(mt.ada)
	ps := waitPrompt(t, mt.rec, 2)
	if !strings.Contains(ps[1], "Public sentence.") || strings.Contains(ps[1], "salary") || strings.Contains(ps[1], "Ada (human): \n") {
		t.Fatalf("prompt 2:\n%s", ps[1])
	}
	waitPromptEnd(t, mt.rec, 2)

	secret := mt.say(mt.ada, "Second secret.")
	mt.turnEnd(mt.ada)
	waitPrompt(t, mt.rec, 3)
	waitPromptEnd(t, mt.rec, 3)
	mt.ada.ok("utterance", "off-the-record", strconv.Itoa(int(secret)), "--session", mt.sid)
	ps = waitPrompt(t, mt.rec, 4)
	if want := fmt.Sprintf("[%d]. Do not use or repeat them", secret); !strings.Contains(ps[3], want) ||
		strings.Contains(ps[3], "Second secret.") {
		t.Fatalf("prompt 4 must name [%d] as off the record:\n%s", secret, ps[3])
	}
}

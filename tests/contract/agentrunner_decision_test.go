package contract

// Contract tests of the agent runner with a decision model (if-decision-model,
// dec-byo-decision-model, dec-wake-check-per-utterance): the command kind with the test double
// tests/decisiondouble, and the OpenAI-compatible endpoint with a fake HTTP server. No real model
// runs. The tests wait for records and log lines, never for time.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// decider is the decision model test double with its record and release files.
type decider struct {
	cmd, rec, release string
}

func newDecider(t *testing.T) decider {
	dir := t.TempDir()
	d := decider{rec: filepath.Join(dir, "decide.jsonl"), release: filepath.Join(dir, "decide.release")}
	d.cmd = "DECIDE_RECORD='" + d.rec + "' DECIDE_RELEASE='" + d.release + "' '" + contracttest.DecisionDouble(t) + "'"
	return d
}

// wait waits until the double has got n requests and returns them.
func (d decider) wait(t *testing.T, n int) []map[string]any {
	t.Helper()
	var reqs []map[string]any
	waitUntil(t, 20*time.Second, fmt.Sprintf("%d requests to the decision model", n), func() bool {
		reqs = contracttest.DecisionRequests(t, d.rec)
		return len(reqs) >= n
	})
	return reqs
}

// texts returns the texts of a list of utterances in a request.
func texts(us any) []string {
	out := []string{}
	l, _ := us.([]any)
	for _, u := range l {
		out = append(out, u.(map[string]any)["text"].(string))
	}
	return out
}

// TestAgentRunnerDecisionPerUtterance: with a decision model, the runner asks it after each
// utterance of another participant, also in the middle of a monologue, and once more when the
// speaker ends the turn. It wakes the agent only on act or topic_closed, with all utterances
// since the last delivery. The requests follow the contract and never carry the agent's own
// utterances as new.
func TestAgentRunnerDecisionPerUtterance(t *testing.T) {
	d := newDecider(t)
	mt := newMeetingWithRunner(t, nil, "--decide-cmd", d.cmd,
		"--topic-task", "Write the decisions of the block into the minutes.")
	mt.waitStderr("decision model: command")

	// A monologue: the first sentence does not concern the agent; the second names it.
	u1 := mt.say(mt.ada, "We start with the budget.")
	reqs := d.wait(t, 1)
	mt.waitStderr(fmt.Sprintf("decision up to event %d: do not wake", u1))
	u2 := mt.say(mt.ada, `Bot, please say "Here."`)
	reqs = d.wait(t, 2)
	waitUtterance(t, mt.adaLog, "bot", "Here.")
	ps := waitPrompt(t, mt.rec, 2)
	p2 := ps[1]
	i, j := strings.Index(p2, "Ada (human): We start with the budget."), strings.Index(p2, `Ada (human): Bot, please say "Here."`)
	if i < 0 || j < 0 || i > j || !strings.Contains(p2, "may still be speaking") ||
		!strings.Contains(p2, "the speaker addressed: bot.") || strings.Contains(p2, "has ended") {
		t.Fatalf("prompt 2 must carry the monologue so far, in order, without an end of turn:\n%s", p2)
	}
	waitPromptEnd(t, mt.rec, 2)

	r1, r2 := reqs[0], reqs[1]
	agent := r1["agent"].(map[string]any)
	if r1["session"] != mt.sid || agent["participant_id"] != "bot" || agent["name"] != "Bot" || agent["kind"] != "agent" ||
		!strings.Contains(r1["role"].(string), "You are Bot.") {
		t.Errorf("request 1: %v", r1)
	}
	kinds := map[string]string{}
	for _, p := range r1["participants"].([]any) {
		pm := p.(map[string]any)
		kinds[pm["participant_id"].(string)] = pm["kind"].(string)
	}
	if kinds["ada"] != "human" || kinds["carl"] != "agent" || kinds["bot"] != "agent" {
		t.Errorf("participants: %v", kinds)
	}
	if got := texts(r1["new"]); len(got) != 1 || got[0] != "We start with the budget." || r1["turn_ended"] != false {
		t.Errorf("request 1: new %v, turn_ended %v", got, r1["turn_ended"])
	}
	if got := texts(r1["context"]); len(got) == 0 || got[len(got)-1] != "Bot is here." {
		t.Errorf("request 1: the context must end with the agent's own last utterance: %v", got)
	}
	if got := texts(r2["new"]); len(got) != 2 || got[1] != `Bot, please say "Here."` || r2["turn_ended"] != false {
		t.Errorf("request 2: new %v, turn_ended %v", got, r2["turn_ended"])
	}
	if ev := r2["new"].([]any)[1].(map[string]any)["event_id"]; ev != float64(u2) {
		t.Errorf("request 2: event ID %v, want %d", ev, u2)
	}

	// The end of the turn: nothing new waits, so no check. Then an utterance and an end of turn
	// that do not concern the agent: two checks, the second with turn_ended, and no prompt.
	mt.turnEnd(mt.ada)
	u3 := mt.say(mt.carl, "Thanks.")
	d.wait(t, 3)
	mt.turnEnd(mt.carl)
	reqs = d.wait(t, 4)
	if got := texts(reqs[3]["new"]); len(got) != 1 || got[0] != "Thanks." || reqs[3]["turn_ended"] != true {
		t.Errorf("request 4: new %v, turn_ended %v", got, reqs[3]["turn_ended"])
	}
	waitUntil(t, 20*time.Second, "two decisions not to wake", func() bool {
		return strings.Count(mt.runner.Stderr(), fmt.Sprintf("decision up to event %d: do not wake", u3)) == 2
	})

	// A block of topics ends: the agent gets the task of the role and what did not wake it.
	mt.say(mt.ada, "Agreed, next topic.")
	ps = waitPrompt(t, mt.rec, 3)
	if p3 := ps[2]; !strings.Contains(p3, "A block of topics has ended. Write the decisions of the block into the minutes.") ||
		!strings.Contains(p3, "Carl (agent): Thanks.") || !strings.Contains(p3, "Ada (human): Agreed, next topic.") {
		t.Fatalf("prompt 3:\n%s", p3)
	}
	for _, s := range []string{"We start with the budget.", "Thanks.", "Agreed, next topic."} {
		if n := count(ps, s); n != 1 {
			t.Errorf("%q is in %d prompts, want 1", s, n)
		}
	}
	for i, r := range contracttest.DecisionRequests(t, d.rec) {
		for _, u := range r["new"].([]any) {
			if u.(map[string]any)["participant_id"] == "bot" {
				t.Errorf("request %d carries an utterance of the agent as new: %v", i+1, u)
			}
		}
	}
}

// TestAgentRunnerDecisionFallback: if the decision model fails, is too slow or answers garbage,
// the runner wakes the agent and logs why.
func TestAgentRunnerDecisionFallback(t *testing.T) {
	d := newDecider(t)
	mt := newMeetingWithRunner(t, nil, "--decide-cmd", d.cmd, "--decide-timeout", "2s")
	for i, c := range []struct{ say, log string }{
		{"decider fail", "decision model failed (command: exit status 3"},
		{"decider slow", "decision model failed (no answer within 2s)"},
		{"decider garbage", "decision model failed (bad answer of the decision model"},
	} {
		mt.say(mt.ada, c.say)
		ps := waitPrompt(t, mt.rec, 2+i)
		if !strings.Contains(ps[1+i], "Ada (human): "+c.say) {
			t.Fatalf("prompt %d:\n%s", 2+i, ps[1+i])
		}
		waitPromptEnd(t, mt.rec, 2+i)
		if !strings.Contains(mt.runner.Stderr(), c.log) {
			t.Fatalf("the runner did not log %q:\n%s", c.log, mt.runner.Stderr())
		}
	}
}

// TestAgentRunnerDecisionBusy: while the agent works, the runner goes on checking. Utterances
// that come meanwhile wait, and all of them go out in one prompt when the agent is free.
func TestAgentRunnerDecisionBusy(t *testing.T) {
	d := newDecider(t)
	mt := newMeetingWithRunner(t, nil, "--decide-cmd", d.cmd)

	mt.say(mt.ada, "Bot, please hold.")
	contracttest.WaitACPRecord(t, mt.rec, 20*time.Second, func(r contracttest.ACPRecord) bool { return r.Kind() == "hold" })
	one := mt.say(mt.ada, "One.")
	mt.waitStderr(fmt.Sprintf("decision up to event %d: do not wake", one))
	two := mt.say(mt.carl, "Bot, two.")
	mt.waitStderr(fmt.Sprintf("decision up to event %d: wake", two))
	mt.waitStderr("the agent is busy; 2 utterances wait for it")
	mt.say(mt.ada, "Three.")
	mt.waitStderr("the agent is busy; 3 utterances wait for it")
	if ps := promptTexts(contracttest.ReadACPRecords(t, mt.rec)); len(ps) != 2 {
		t.Fatalf("a prompt was sent while the agent was busy: %d prompts", len(ps))
	}
	mt.release()
	ps := waitPrompt(t, mt.rec, 3)
	p3 := ps[2]
	a, b, c := strings.Index(p3, "Ada (human): One."), strings.Index(p3, "Carl (agent): Bot, two."), strings.Index(p3, "Ada (human): Three.")
	if a < 0 || b < 0 || c < 0 || a > b || b > c {
		t.Fatalf("prompt 3 must carry the waiting utterances in order:\n%s", p3)
	}
	for _, s := range []string{"please hold", "One.", "Bot, two.", "Three."} {
		if n := count(ps, s); n != 1 {
			t.Errorf("%q is in %d prompts, want 1", s, n)
		}
	}
}

// TestAgentRunnerDecisionWhileAsking: utterances that come while the decision model is asked are
// not lost. At most one check runs; the next check judges all utterances that wait.
func TestAgentRunnerDecisionWhileAsking(t *testing.T) {
	d := newDecider(t)
	mt := newMeetingWithRunner(t, nil, "--decide-cmd", d.cmd, "--decide-timeout", "60s")

	mt.say(mt.ada, "decider hold")
	d.wait(t, 1)
	mt.say(mt.ada, "Two.")
	three := mt.say(mt.ada, `Bot, please say "Three."`)
	waitLog(t, mt.botLog, three)
	if n := len(contracttest.DecisionRequests(t, d.rec)); n != 1 {
		t.Fatalf("a second check started while the first one ran: %d requests", n)
	}
	if err := os.WriteFile(d.release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitUtterance(t, mt.adaLog, "bot", "Three.")
	ps := waitPrompt(t, mt.rec, 2)
	for _, s := range []string{"decider hold", "Two.", `please say "Three."`} {
		if n := count(ps, s); n != 1 {
			t.Errorf("%q is in %d prompts, want 1:\n%s", s, n, ps[1])
		}
	}
	reqs := contracttest.DecisionRequests(t, d.rec)
	if len(reqs) > 3 {
		t.Errorf("want at most one check per utterance, got %d", len(reqs))
	}
	if got := texts(reqs[len(reqs)-1]["new"]); len(got) != 3 || got[0] != "decider hold" {
		t.Errorf("the last check must judge all waiting utterances: %v", got)
	}
}

// TestAgentRunnerDecisionEndpoint: the OpenAI-compatible kind posts a chat completion with a JSON
// schema for structured output and the key from OFFICE_DECIDE_KEY; an error status wakes the
// agent.
func TestAgentRunnerDecisionEndpoint(t *testing.T) {
	var mu sync.Mutex
	type call struct {
		path, auth string
		body       map[string]any
	}
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		calls = append(calls, call{r.URL.Path, r.Header.Get("Authorization"), body})
		n := len(calls)
		mu.Unlock()
		if n == 2 {
			http.Error(w, "overloaded", http.StatusServiceUnavailable)
			return
		}
		content := `{"act":true,"addressed":"bot","topic_closed":false,"reason":"asked by name"}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}}})
	}))
	defer srv.Close()

	mt := newMeetingWithRunner(t, []string{"OFFICE_DECIDE_KEY=test-key"},
		"--decide-url", srv.URL+"/v1", "--decide-model", "tiny-model")
	mt.say(mt.ada, "Bot, are you there?")
	ps := waitPrompt(t, mt.rec, 2)
	if !strings.Contains(ps[1], "Bot, are you there?") || !strings.Contains(ps[1], "the speaker addressed: bot.") {
		t.Fatalf("prompt 2:\n%s", ps[1])
	}
	waitPromptEnd(t, mt.rec, 2)
	mt.say(mt.ada, "Still there?")
	ps = waitPrompt(t, mt.rec, 3)
	if !strings.Contains(ps[2], "Still there?") || !strings.Contains(mt.runner.Stderr(), "503") {
		t.Fatalf("an error status must wake the agent and be logged:\n%s\n%s", ps[2], mt.runner.Stderr())
	}

	mu.Lock()
	c := calls[0]
	mu.Unlock()
	if c.path != "/v1/chat/completions" || c.auth != "Bearer test-key" || c.body["model"] != "tiny-model" {
		t.Errorf("call: path %q, auth %q, model %v", c.path, c.auth, c.body["model"])
	}
	rf := c.body["response_format"].(map[string]any)
	js := rf["json_schema"].(map[string]any)
	schema, _ := json.Marshal(js["schema"])
	if rf["type"] != "json_schema" || js["strict"] != true || !strings.Contains(string(schema), `"topic_closed"`) {
		t.Errorf("response_format: %v", rf)
	}
	msgs := c.body["messages"].([]any)
	var req map[string]any
	if err := json.Unmarshal([]byte(msgs[1].(map[string]any)["content"].(string)), &req); err != nil {
		t.Fatalf("the user message is not the JSON request: %v", err)
	}
	if got := texts(req["new"]); len(got) != 1 || got[0] != "Bot, are you there?" {
		t.Errorf("request new: %v", got)
	}
	if strings.Contains(mt.runner.Stderr(), "test-key") {
		t.Error("the runner logged the key")
	}
}

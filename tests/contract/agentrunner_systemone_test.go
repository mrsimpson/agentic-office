package contract

// Contract tests of the agent runner with the wake strategy decision-model (if-decision-model,
// dec-byo-decision-model, dec-agent-runner-delivery). The decision model is a fake HTTP server
// of the System One API in this file; no real model runs. The tests wait for records and log
// lines, never for time.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// systemOne is a fake endpoint of the System One API. It judges the last new utterance of the
// state by fixed rules:
//
//   - act: 0.93 if the utterance names the agent, 0.6 if it says "maybe", else 0.05;
//   - topic_closed: 0.9 if it says "next topic", else 0.1;
//   - addressed: the agent if the utterance names it, else "nobody";
//   - "s1 garbage": a noul outside 0 to 1.
//
// While down is true, it answers 500. With wrap, it wraps the answer in a Cloudflare envelope.
type systemOne struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []systemOneCall
	down  bool
	wrap  bool
}

type systemOneCall struct {
	path, auth string
	body       map[string]any
}

func newSystemOne(t *testing.T, wrap bool) *systemOne {
	s := &systemOne{wrap: wrap}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *systemOne) setDown(down bool) {
	s.mu.Lock()
	s.down = down
	s.mu.Unlock()
}

func (s *systemOne) call(i int) systemOneCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[i]
}

func (s *systemOne) serve(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	s.mu.Lock()
	s.calls = append(s.calls, systemOneCall{r.URL.Path, r.Header.Get("Authorization"), body})
	down, wrap := s.down, s.wrap
	s.mu.Unlock()
	if down {
		http.Error(w, "overloaded", http.StatusInternalServerError)
		return
	}
	state, _ := body["state"].(map[string]any)
	agent, _ := state["agent"].(map[string]any)
	news, _ := state["new"].([]any)
	text := ""
	if len(news) > 0 {
		text, _ = news[len(news)-1].(map[string]any)["text"].(string)
	}
	name, _ := agent["name"].(string)
	act, closed, addressed := 0.05, 0.1, "nobody"
	switch {
	case name != "" && strings.Contains(text, name):
		act, addressed = 0.93, agent["participant_id"].(string)
	case strings.Contains(strings.ToLower(text), "maybe"):
		act = 0.6
	case strings.Contains(text, "s1 garbage"):
		act = 1.7
	}
	if strings.Contains(strings.ToLower(text), "next topic") {
		closed = 0.9
	}
	probs := map[string]any{}
	q, _ := body["questions"].(map[string]any)
	crit, _ := q["addressed"].(map[string]any)["criteria"].(map[string]any)
	for k := range crit {
		probs[k] = 0.1
	}
	probs[addressed] = 0.8
	ans := map[string]any{
		"model": body["model"],
		"answers": map[string]any{
			"act":          map[string]any{"type": "noul", "noul": act},
			"topic_closed": map[string]any{"type": "noul", "noul": closed},
			"addressed":    map[string]any{"type": "choice", "choice": addressed, "confidence": 0.8, "probabilities": probs},
		},
		"usage": map[string]any{"input_tokens": 120},
	}
	if wrap {
		ans = map[string]any{"result": ans, "success": true, "errors": []any{}, "messages": []any{}}
	}
	_ = json.NewEncoder(w).Encode(ans)
}

// TestAgentRunnerDecisionModel: the runner posts the state and three questions to the full URL
// with the key as a bearer token. A noul at or above the threshold (default 0.5) wakes the agent;
// the choice "addressed" goes into the prompt; topic_closed adds the task of the role. The runner
// logs the probabilities, never the key.
func TestAgentRunnerDecisionModel(t *testing.T) {
	s1 := newSystemOne(t, false)
	mt := newMeetingWithRunner(t, []string{"OFFICE_DECIDE_KEY=s1-secret-key"},
		"--wake", "decision-model", "--decide-url", s1.srv.URL+"/v1/systemone", "--decide-model", "jev-1.14.0",
		"--topic-task", "Write the decisions of the block into the minutes.")
	mt.waitStderr("wake strategy decision-model: endpoint " + s1.srv.URL + "/v1/systemone, model jev-1.14.0, threshold 0.5")

	u1 := mt.say(mt.ada, "We start with the budget.")
	mt.waitStderr(fmt.Sprintf("decision up to event %d: do not wake (addressed \"\"): act 0.05, topic_closed 0.10, addressed nobody (0.80)", u1))
	mt.say(mt.ada, `Bot, please say "Here."`)
	waitUtterance(t, mt.adaLog, "bot", "Here.")
	ps := waitPrompt(t, mt.rec, 2)
	if p := ps[1]; !strings.Contains(p, "Ada (human): We start with the budget.") || !strings.Contains(p, `Ada (human): Bot, please say "Here."`) ||
		!strings.Contains(p, "the speaker addressed: bot.") || strings.Contains(p, "A block of topics") {
		t.Fatalf("prompt 2:\n%s", p)
	}
	if !strings.Contains(mt.runner.Stderr(), "act 0.93, topic_closed 0.10, addressed bot (0.80); ada 0.10, bot 0.80, carl 0.10, nobody 0.10") {
		t.Errorf("the runner must log the probabilities:\n%s", mt.runner.Stderr())
	}
	waitPromptEnd(t, mt.rec, 2)

	mt.say(mt.ada, "Agreed, next topic.")
	ps = waitPrompt(t, mt.rec, 3)
	if p := ps[2]; !strings.Contains(p, "A block of topics has ended. Write the decisions of the block into the minutes.") ||
		!strings.Contains(p, "Ada (human): Agreed, next topic.") || strings.Contains(p, "the speaker addressed") {
		t.Fatalf("prompt 3:\n%s", p)
	}

	c := s1.call(0)
	if c.path != "/v1/systemone" || c.auth != "Bearer s1-secret-key" || c.body["model"] != "jev-1.14.0" {
		t.Errorf("call: path %q, auth %q, model %v", c.path, c.auth, c.body["model"])
	}
	st := c.body["state"].(map[string]any)
	if a := st["agent"].(map[string]any); a["participant_id"] != "bot" || a["name"] != "Bot" || st["session"] != mt.sid ||
		!strings.Contains(st["role"].(string), "You are Bot.") || st["turn_ended"] != false {
		t.Errorf("state: %v", st)
	}
	if got := texts(st["new"]); len(got) != 1 || got[0] != "We start with the budget." {
		t.Errorf("state new: %v", got)
	}
	if got := texts(st["earlier"]); len(got) == 0 || got[len(got)-1] != "Bot is here." {
		t.Errorf("state earlier: %v", got)
	}
	q := c.body["questions"].(map[string]any)
	for name, typ := range map[string]string{"act": "noul", "topic_closed": "noul", "addressed": "choice"} {
		qq, _ := q[name].(map[string]any)
		if qq["type"] != typ || qq["instructions"] == "" {
			t.Errorf("question %s: %v", name, qq)
		}
	}
	var keys []string
	for k := range q["addressed"].(map[string]any)["criteria"].(map[string]any) {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "ada,bot,carl,nobody" {
		t.Errorf("criteria of addressed: %v", keys)
	}
	if strings.Contains(mt.runner.Stderr(), "s1-secret-key") {
		t.Error("the runner logged the key")
	}
}

// TestAgentRunnerDecisionModelThreshold: --decide-threshold sets the probability from which an
// answer counts as yes. The answer may come in the envelope of the Cloudflare REST API.
func TestAgentRunnerDecisionModelThreshold(t *testing.T) {
	s1 := newSystemOne(t, true)
	mt := newMeetingWithRunner(t, nil, "--wake", "decision-model", "--decide-url", s1.srv.URL+"/client/v4/accounts/x/ai/run/@cf/cloudflare/clef",
		"--decide-model", "clef-flash", "--decide-threshold", "0.7")
	u1 := mt.say(mt.ada, "Maybe we need a minute taker.")
	mt.waitStderr(fmt.Sprintf("decision up to event %d: do not wake (addressed \"\"): act 0.60", u1))
	mt.say(mt.ada, `Bot, please say "Yes."`)
	ps := waitPrompt(t, mt.rec, 2)
	if p := ps[1]; !strings.Contains(p, "Ada (human): Maybe we need a minute taker.") || !strings.Contains(p, `Ada (human): Bot, please say "Yes."`) {
		t.Fatalf("prompt 2:\n%s", p)
	}
	if c := s1.call(0); c.path != "/client/v4/accounts/x/ai/run/@cf/cloudflare/clef" || c.body["model"] != "clef-flash" {
		t.Errorf("call: path %q, model %v", c.path, c.body["model"])
	}
}

// TestAgentRunnerDecisionModelFallback: one failure does not wake the agent; the utterances wait
// for the next change of speaker. After three failures within five minutes, the runner switches
// to turn-end and logs it once. It asks the model again at most once per change of speaker, at
// the first utterance after it, and switches back when the model answers. Nothing is lost and
// nothing is sent twice. The test needs no clock: the three failures come at once.
func TestAgentRunnerDecisionModelFallback(t *testing.T) {
	s1 := newSystemOne(t, false)
	mt := newMeetingWithRunner(t, nil, "--wake", "decision-model", "--decide-url", s1.srv.URL, "--decide-model", "jev-latest")
	failures := func(n int) {
		t.Helper()
		waitUntil(t, 20*time.Second, fmt.Sprintf("%d failures logged", n), func() bool {
			return strings.Count(mt.runner.Stderr(), "the decision model failed (") == n
		})
	}
	const again = "asking the decision model again\n"

	// One failure: no wake-up until the change of speaker.
	s1.setDown(true)
	mt.say(mt.ada, "First.")
	failures(1)
	mt.waitStderr("the utterances wait for the next change of speaker")
	if strings.Contains(mt.runner.Stderr(), "waking the agent") {
		t.Fatalf("a failure woke the agent:\n%s", mt.runner.Stderr())
	}
	mt.turnEnd(mt.ada)
	ps := waitPrompt(t, mt.rec, 2)
	if !strings.Contains(ps[1], "Ada (human): First.") || !strings.Contains(ps[1], "The turn of Ada (human) has ended.") {
		t.Fatalf("prompt 2:\n%s", ps[1])
	}
	waitPromptEnd(t, mt.rec, 2)

	// Two more failures: the runner switches to turn-end. Until the next change of speaker, it
	// does not ask the model.
	mt.say(mt.ada, "Second.")
	failures(2)
	mt.say(mt.ada, "Third.")
	failures(3)
	mt.waitStderr("3 failures within 5m0s: switching to turn-end, asking the decision model again after the next change of speaker")
	mt.say(mt.ada, "Fourth.")
	mt.turnEnd(mt.ada)
	ps = waitPrompt(t, mt.rec, 3)
	for _, s := range []string{"Ada (human): Second.", "Ada (human): Third.", "Ada (human): Fourth."} {
		if !strings.Contains(ps[2], s) {
			t.Fatalf("prompt 3 lacks %q:\n%s", s, ps[2])
		}
	}
	if strings.Contains(mt.runner.Stderr(), again) {
		t.Fatalf("the runner asked the model again before a change of speaker:\n%s", mt.runner.Stderr())
	}
	waitPromptEnd(t, mt.rec, 3)

	// After the change of speaker, the first utterance asks the model again; it still fails. The
	// next utterance does not ask it.
	mt.say(mt.ada, "Fifth.")
	mt.waitStderr("the decision model still fails (endpoint: 500")
	mt.say(mt.ada, "Sixth.")
	mt.turnEnd(mt.ada)
	ps = waitPrompt(t, mt.rec, 4)
	if !strings.Contains(ps[3], "Ada (human): Fifth.") || !strings.Contains(ps[3], "Ada (human): Sixth.") {
		t.Fatalf("prompt 4:\n%s", ps[3])
	}
	if n := strings.Count(mt.runner.Stderr(), again); n != 1 {
		t.Fatalf("the runner asked the model %d times within one turn, want once:\n%s", n, mt.runner.Stderr())
	}
	waitPromptEnd(t, mt.rec, 4)

	// The model answers again: the runner switches back and wakes the agent without a turn.end.
	s1.setDown(false)
	mt.say(mt.ada, `Bot, please say "Back."`)
	waitUtterance(t, mt.adaLog, "bot", "Back.")
	mt.waitStderr("the decision model answers again: switching back from turn-end to decision-model")
	waitPromptEnd(t, mt.rec, 5)

	// An answer outside the contract is one failure, not a switch.
	mt.say(mt.ada, "s1 garbage")
	mt.waitStderr("the decision model failed (bad answer of the decision model")
	if n := strings.Count(mt.runner.Stderr(), "switching to turn-end"); n != 1 {
		t.Errorf("the switch to turn-end is logged %d times, want once", n)
	}
	ps = promptTexts(contracttest.ReadACPRecords(t, mt.rec))
	for _, s := range []string{"First.", "Second.", "Third.", "Fourth.", "Fifth.", "Sixth.", `please say "Back."`} {
		if n := count(ps, s); n != 1 {
			t.Errorf("%q is in %d prompts, want 1", s, n)
		}
	}
}

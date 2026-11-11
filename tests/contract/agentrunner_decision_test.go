package contract

// Contract tests of the agent runner with a decision model (if-decision-model,
// dec-byo-decision-model): the command kind with the test double tests/decisiondouble, and the
// OpenAI-compatible endpoint with a fake HTTP server. No real model runs.

import (
	"bufio"
	"encoding/json"
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

// decisionRequests reads the requests that the decision model test double recorded.
func decisionRequests(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var v map[string]any
		if json.Unmarshal(sc.Bytes(), &v) == nil {
			out = append(out, v)
		}
	}
	return out
}

func texts(us any) []string {
	var out []string
	for _, u := range us.([]any) {
		out = append(out, u.(map[string]any)["text"].(string))
	}
	return out
}

// TestAgentRunnerDecisionCommand: with a decision model, the runner wakes the agent only when the
// model says act or topic_closed. Utterances that did not wake it reach it with the next prompt.
// If the model fails, is too slow or answers garbage, the runner wakes the agent and logs why.
func TestAgentRunnerDecisionCommand(t *testing.T) {
	drec := filepath.Join(t.TempDir(), "decide.jsonl")
	cmd := "DECIDE_RECORD='" + drec + "' '" + contracttest.DecisionDouble(t) + "'"
	mt := newMeetingWithRunner(t, "--decide-cmd", cmd, "--decide-timeout", "2s",
		"--topic-task", "Write the decisions of the block into the minutes.")

	// Not addressed: the agent is not woken.
	mt.say(mt.ada, "The weather is nice.")
	mt.turnEnd(mt.ada)
	waitUntil(t, 20*time.Second, "the first decision", func() bool { return len(decisionRequests(t, drec)) == 1 })

	// Addressed: the agent gets both turns in one prompt.
	mt.say(mt.ada, `Bot, please say "Yes, Ada."`)
	mt.turnEnd(mt.ada)
	waitUtterance(t, mt.adaLog, "bot", "Yes, Ada.")
	ps := waitPrompt(t, mt.rec, 2)
	if !strings.Contains(ps[1], "Ada (human): The weather is nice.") || !strings.Contains(ps[1], `Ada (human): Bot, please say`) ||
		!strings.Contains(ps[1], "the speaker addressed: bot.") {
		t.Fatalf("prompt 2 must carry the turn that did not wake the agent, and the addressee:\n%s", ps[1])
	}
	waitPromptEnd(t, mt.rec, 2)

	// A block of topics ends: the agent gets the task of the role, though nobody asked it.
	mt.say(mt.carl, "Agreed, next topic.")
	mt.turnEnd(mt.carl)
	ps = waitPrompt(t, mt.rec, 3)
	if !strings.Contains(ps[2], "A block of topics has ended. Write the decisions of the block into the minutes.") ||
		!strings.Contains(ps[2], "Nobody asked you anything") {
		t.Fatalf("prompt 3:\n%s", ps[2])
	}
	waitPromptEnd(t, mt.rec, 3)

	// Fallback: the model fails, is slow, or answers garbage. The agent is woken each time.
	for i, c := range []struct{ say, log string }{
		{"decider fail", "decision model failed (command: exit status 3"},
		{"decider slow", "decision model failed (no answer within 2s)"},
		{"decider garbage", "decision model failed (bad answer of the decision model"},
	} {
		mt.say(mt.ada, c.say)
		mt.turnEnd(mt.ada)
		ps = waitPrompt(t, mt.rec, 4+i)
		if !strings.Contains(ps[3+i], c.say) {
			t.Fatalf("prompt %d:\n%s", 4+i, ps[3+i])
		}
		waitPromptEnd(t, mt.rec, 4+i)
		if !strings.Contains(mt.runner.Stderr(), c.log) {
			t.Fatalf("the runner did not log %q:\n%s", c.log, mt.runner.Stderr())
		}
	}

	// The requests follow the contract.
	reqs := decisionRequests(t, drec)
	r0, r1 := reqs[0], reqs[1]
	agent := r0["agent"].(map[string]any)
	if r0["session"] != mt.sid || agent["participant_id"] != "bot" || agent["name"] != "Bot" || agent["kind"] != "agent" ||
		!strings.Contains(r0["role"].(string), "You are Bot.") {
		t.Errorf("request 1: %v", r0)
	}
	kinds := map[string]string{}
	for _, p := range r0["participants"].([]any) {
		pm := p.(map[string]any)
		kinds[pm["participant_id"].(string)] = pm["kind"].(string)
	}
	if kinds["ada"] != "human" || kinds["carl"] != "agent" || kinds["bot"] != "agent" {
		t.Errorf("participants: %v", kinds)
	}
	if got := texts(r0["new"]); len(got) != 1 || got[0] != "The weather is nice." {
		t.Errorf("request 1 new: %v", got)
	}
	if by := r0["turn_ended_by"].(map[string]any); by["participant_id"] != "ada" || by["kind"] != "human" {
		t.Errorf("request 1 turn_ended_by: %v", by)
	}
	if got := texts(r1["new"]); len(got) != 2 || got[0] != "The weather is nice." {
		t.Errorf("request 2 new: %v", got)
	}
	if got := texts(r1["context"]); len(got) == 0 || got[len(got)-1] != "Bot is here." {
		t.Errorf("request 2 context must end with the agent's own last utterance: %v", got)
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

	mt := newMeetingWithRunnerEnv(t, []string{"OFFICE_DECIDE_KEY=test-key"},
		"--decide-url", srv.URL+"/v1", "--decide-model", "tiny-model")
	mt.say(mt.ada, "Bot, are you there?")
	mt.turnEnd(mt.ada)
	ps := waitPrompt(t, mt.rec, 2)
	if !strings.Contains(ps[1], "Bot, are you there?") || !strings.Contains(ps[1], "the speaker addressed: bot.") {
		t.Fatalf("prompt 2:\n%s", ps[1])
	}
	waitPromptEnd(t, mt.rec, 2)
	mt.say(mt.ada, "Still there?")
	mt.turnEnd(mt.ada)
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

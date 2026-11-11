package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/client/agentrunner/decision"
)

// DecisionModel configures a decision model that the user brings (dec-byo-decision-model). Set
// either Command or URL. The zero value means: no decision model; the runner wakes the agent at
// every change of speaker.
type DecisionModel struct {
	// Command is a shell command (run with sh -c). It reads one decision.Request as JSON on stdin
	// and writes one decision.Answer as JSON on stdout.
	Command string
	// URL is the base URL of an OpenAI-compatible API, for example http://127.0.0.1:11434/v1. The
	// runner posts to URL + "/chat/completions" with structured output (JSON schema).
	URL string
	// Model is the model name for URL.
	Model string
	// APIKey is sent as a bearer token to URL. It may be empty.
	APIKey string
	// Timeout bounds one decision. Zero means 10 s.
	Timeout time.Duration
}

func (d DecisionModel) configured() bool { return d.Command != "" || d.URL != "" }

// String describes the model for the log, without the key.
func (d DecisionModel) String() string {
	if d.Command != "" {
		return "command " + d.Command
	}
	return fmt.Sprintf("endpoint %s, model %s", d.URL, d.Model)
}

// ask asks the decision model. An error means that the runner falls back to waking the agent.
func (d DecisionModel) ask(ctx context.Context, req decision.Request) (decision.Answer, error) {
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	in, err := json.Marshal(req)
	if err != nil {
		return decision.Answer{}, err
	}
	var out []byte
	if d.Command != "" {
		out, err = d.runCommand(ctx, in)
	} else {
		out, err = d.callEndpoint(ctx, in)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return decision.Answer{}, fmt.Errorf("no answer within %s", timeout)
	}
	if err != nil {
		return decision.Answer{}, err
	}
	return decision.ParseAnswer(out)
}

func (d DecisionModel) runCommand(ctx context.Context, in []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", d.Command)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("command: %v: %s", err, oneLine(stderr.String(), 200))
	}
	return out, nil
}

// systemPrompt tells a chat model what to decide. A command gets only the request.
const systemPrompt = `You decide for an agent in a meeting whether it should act now. The user message is a JSON request: the session, the agent (participant_id, name, kind) and its role, the participants, earlier utterances as context, the new utterances that the agent has not seen, and who just ended their turn.

Answer with JSON only:
- act: true if a speaker addressed the agent, by name, by its role or by the context (for example a question that follows the agent's last contribution), or if the role of the agent asks it to contribute now. False if the talk does not concern the agent. When you are unsure, answer true.
- addressed: the participant_id that the last speaker addressed, or an empty string.
- topic_closed: true if the participants closed a topic or a block of topics, for example "agreed, next point".
- reason: a short reason.`

func (d DecisionModel) callEndpoint(ctx context.Context, in []byte) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"model": d.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": string(in)},
		},
		"temperature": 0,
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "decision",
				"strict": true,
				"schema": json.RawMessage(decision.AnswerSchema),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(d.URL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if d.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+d.APIKey)
	}
	resp, err := http.DefaultClient.Do(hr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("endpoint: %s: %s", resp.Status, oneLine(string(b), 200))
	}
	var cr struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &cr); err != nil {
		return nil, fmt.Errorf("endpoint: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, errors.New("endpoint: no choices")
	}
	if r := cr.Choices[0].Message.Refusal; r != "" {
		return nil, fmt.Errorf("endpoint refused: %s", oneLine(r, 200))
	}
	return []byte(cr.Choices[0].Message.Content), nil
}

// contextSize is how many earlier utterances a request carries at most.
const contextSize = 20

// decisionRequest builds the request for a batch.
func (r *runner) decisionRequest(b batch) decision.Request {
	me := r.who(r.self)
	req := decision.Request{Session: r.o.Session, Role: r.o.Role,
		Agent:   decision.Participant{ParticipantID: r.self, Name: me.name, Kind: me.kind},
		Context: []decision.Utterance{}, New: []decision.Utterance{}, Participants: []decision.Participant{}}
	ids := make([]string, 0, len(r.names))
	for pid := range r.names {
		ids = append(ids, pid)
	}
	sort.Strings(ids)
	for _, pid := range ids {
		p := r.names[pid]
		req.Participants = append(req.Participants, decision.Participant{ParticipantID: pid, Name: p.name, Kind: p.kind})
	}
	utt := func(u utterance) decision.Utterance {
		p := r.who(u.pid)
		return decision.Utterance{EventID: u.id, ParticipantID: u.pid, Name: p.name, Kind: p.kind, Text: u.text}
	}
	inBatch := map[uint32]bool{}
	for _, u := range b.utterances {
		inBatch[u.id] = true
		req.New = append(req.New, utt(u))
	}
	for _, u := range r.d.recent {
		if !inBatch[u.id] && (len(b.utterances) == 0 || u.id < b.utterances[len(b.utterances)-1].id) {
			req.Context = append(req.Context, utt(u))
		}
	}
	if n := len(req.Context); n > contextSize {
		req.Context = req.Context[n-contextSize:]
	}
	p := r.who(b.turnBy)
	req.TurnEndedBy = decision.Participant{ParticipantID: b.turnBy, Name: p.name, Kind: p.kind}
	return req
}

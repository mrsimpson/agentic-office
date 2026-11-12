package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/client/agentrunner/decision"
)

// llmWake asks an LLM judge after each utterance (--wake llm, if-llm-judge): a command, or an
// OpenAI-compatible chat endpoint with structured output by JSON schema. It falls back to
// turn-end when the judge fails.
type llmWake struct {
	fallback
	command string
	url     string
	model   string
	key     string
}

func (w *llmWake) String() string {
	how := "command " + w.command
	if w.command == "" {
		how = fmt.Sprintf("endpoint %s, model %s", w.url, w.model)
	}
	return "wake strategy " + WakeLLM + ": " + how + "; it is asked after each utterance of another participant"
}

// ask asks the LLM judge.
func (w *llmWake) ask(ctx context.Context, req decision.Request) (verdict, error) {
	in, err := json.Marshal(req)
	if err != nil {
		return verdict{}, err
	}
	out, err := w.bounded(ctx, func(ctx context.Context) ([]byte, error) {
		if w.command != "" {
			return w.runCommand(ctx, in)
		}
		return w.callEndpoint(ctx, in)
	})
	if err != nil {
		return verdict{}, err
	}
	a, err := decision.ParseAnswer(out)
	if err != nil {
		return verdict{}, err
	}
	return verdict{act: a.Act, topicClosed: a.TopicClosed, addressed: a.Addressed, note: oneLine(a.Reason, 200)}, nil
}

func (w *llmWake) runCommand(ctx context.Context, in []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", w.command)
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

// systemPrompt tells an LLM judge behind a chat endpoint what to decide. A command gets only the request.
const systemPrompt = `You decide for an agent in a meeting whether it should act now. The user message is a JSON request: the session, the agent (participant_id, name, kind) and its role, the participants, earlier utterances as context, the new utterances that the agent has not got yet, and whether the speaker of the last new utterance has ended their turn (turn_ended). You are asked after each utterance, so the speaker may still be speaking.

Answer with JSON only:
- act: true if a speaker addressed the agent, by name, by its role or by the context (for example a question that follows the agent's last contribution), or if the last new utterance concerns the role of the agent so that it should contribute now. False if the talk does not concern the agent. When you are unsure, answer true.
- addressed: the participant_id that the last speaker addressed, or an empty string.
- topic_closed: true if the participants closed a topic or a block of topics, or the speaker moved on to a new topic, for example "agreed, next point".
- reason: a short reason.`

func (w *llmWake) callEndpoint(ctx context.Context, in []byte) ([]byte, error) {
	body, err := json.Marshal(map[string]any{
		"model": w.model,
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
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(w.url, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	b, err := post(hr, w.key)
	if err != nil {
		return nil, err
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

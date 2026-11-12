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

	"github.com/mrsimpson/agentic-office/client/agentrunner/forwardingjudge"
)

// forwardingJudgeWake asks a forwarding judge after each utterance (--wake llm,
// if-forwarding-judge): a command, or an OpenAI-compatible chat endpoint with structured output by
// JSON schema. It falls back to turn-end when the judge fails.
type forwardingJudgeWake struct {
	fallback
	command string
	url     string
	model   string
	key     string
}

func (w *forwardingJudgeWake) String() string {
	how := "command " + w.command
	if w.command == "" {
		how = fmt.Sprintf("endpoint %s, model %s", w.url, w.model)
	}
	return "wake strategy " + WakeLLM + ": " + how + "; it is asked after each utterance of another participant"
}

// ask asks the forwarding judge.
func (w *forwardingJudgeWake) ask(ctx context.Context, req checkRequest) (verdict, error) {
	in, err := json.Marshal(judgeRequest(req))
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
	a, err := forwardingjudge.ParseAnswer(out)
	if err != nil {
		return verdict{}, err
	}
	return verdict{act: a.Act, topicClosed: a.TopicClosed, addressed: a.Addressed, note: oneLine(a.Reason, 200)}, nil
}

func (w *forwardingJudgeWake) runCommand(ctx context.Context, in []byte) ([]byte, error) {
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

// systemPrompt tells a forwarding judge behind a chat endpoint what to decide. A command gets only
// the request.
const systemPrompt = `You decide for an agent in a meeting whether it should act now. The user message is a JSON request: the session, the agent (participant_id, name, kind) and its role, the participants, earlier utterances as context, the new utterances that the agent has not got yet, and whether the speaker of the last new utterance has ended their turn (turn_ended). You are asked after each utterance, so the speaker may still be speaking.

Answer with JSON only:
- act: true if a speaker addressed the agent, by name, by its role or by the context (for example a question that follows the agent's last contribution), or if the last new utterance concerns the role of the agent so that it should contribute now. False if the talk does not concern the agent. When you are unsure, answer true.
- addressed: the participant_id that the last speaker addressed, or an empty string.
- topic_closed: true if the participants closed a topic or a block of topics, or the speaker moved on to a new topic, for example "agreed, next point".
- reason: a short reason.`

func (w *forwardingJudgeWake) callEndpoint(ctx context.Context, in []byte) ([]byte, error) {
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
				"schema": json.RawMessage(forwardingjudge.AnswerSchema),
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

// judgeRequest maps the request of a check to the contract of the forwarding judge.
func judgeRequest(req checkRequest) forwardingjudge.Request {
	part := func(p person) forwardingjudge.Participant {
		return forwardingjudge.Participant{ParticipantID: p.id, Name: p.name, Kind: p.kind}
	}
	utt := func(us []spoken) []forwardingjudge.Utterance {
		out := make([]forwardingjudge.Utterance, len(us))
		for i, u := range us {
			out[i] = forwardingjudge.Utterance{EventID: u.eventID, ParticipantID: u.who.id, Name: u.who.name, Kind: u.who.kind, Text: u.text}
		}
		return out
	}
	fr := forwardingjudge.Request{Session: req.session, Agent: part(req.agent), Role: req.role,
		Participants: make([]forwardingjudge.Participant, len(req.participants)),
		Context:      utt(req.earlier), New: utt(req.new), TurnEnded: req.turnEnded}
	for i, p := range req.participants {
		fr.Participants[i] = part(p)
	}
	return fr
}

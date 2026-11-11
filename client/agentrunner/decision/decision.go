// Package decision is the contract of a decision model (if-decision-model in
// docs/architecture/05-building-blocks.arc42.md, dec-byo-decision-model).
//
// The user brings the model. After each utterance of another participant, the agent runner sends
// a Request and expects an Answer. The model is reached in one of two ways:
//
//   - a command: it reads one Request as JSON on stdin and writes one Answer as JSON on stdout,
//     then exits;
//   - an OpenAI-compatible chat endpoint: the runner sends the Request as the user message and
//     asks for structured output with AnswerSchema (response_format of type json_schema).
//
// All names in the JSON use snake_case.
package decision

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Participant is a participant of the session.
type Participant struct {
	ParticipantID string `json:"participant_id"`
	Name          string `json:"name"`
	// Kind is "human" or "agent".
	Kind string `json:"kind"`
}

// Utterance is one utterance of the session log.
type Utterance struct {
	// EventID is the ID of the utterance.final event in the session log.
	EventID       uint32 `json:"event_id"`
	ParticipantID string `json:"participant_id"`
	Name          string `json:"name"`
	// Kind is "human" or "agent".
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Request is what the runner asks the decision model after an utterance.
type Request struct {
	// Session is the ID of the session.
	Session string `json:"session"`
	// Agent is the participant that the runner keeps in the meeting.
	Agent Participant `json:"agent"`
	// Role is the role of the agent, as the user gave it. It may be empty.
	Role string `json:"role"`
	// Participants are all participants that joined the session so far.
	Participants []Participant `json:"participants"`
	// Context are earlier utterances, oldest first, for the meaning of New. The agent may have
	// seen them already.
	Context []Utterance `json:"context"`
	// New are the utterances that the agent has not got yet, oldest first. The agent's own
	// utterances and sentences off the record are left out.
	New []Utterance `json:"new"`
	// TurnEnded is true when the speaker of the last utterance in New ended their turn.
	TurnEnded bool `json:"turn_ended"`
}

// Answer is the structured output of the decision model.
type Answer struct {
	// Act is true if the agent should act now.
	Act bool `json:"act"`
	// Addressed is the participant ID or name that the speaker addressed, or empty.
	Addressed string `json:"addressed"`
	// TopicClosed is true if a block of topics has ended.
	TopicClosed bool `json:"topic_closed"`
	// Reason is a short reason, for the log of the runner.
	Reason string `json:"reason"`
}

// AnswerSchema is the JSON schema of Answer, for structured output.
const AnswerSchema = `{
  "type": "object",
  "properties": {
    "act": {"type": "boolean", "description": "true if the agent should act now"},
    "addressed": {"type": "string", "description": "participant ID or name that the speaker addressed, or empty"},
    "topic_closed": {"type": "boolean", "description": "true if a block of topics has ended"},
    "reason": {"type": "string", "description": "a short reason"}
  },
  "required": ["act", "addressed", "topic_closed", "reason"],
  "additionalProperties": false
}`

// ErrBadAnswer means that an answer does not follow the contract.
var ErrBadAnswer = errors.New("bad answer of the decision model")

// ParseAnswer reads an Answer. The fields act and topic_closed must be there and be booleans;
// addressed and reason may be missing.
func ParseAnswer(b []byte) (Answer, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return Answer{}, fmt.Errorf("%w: %v", ErrBadAnswer, err)
	}
	for _, k := range []string{"act", "topic_closed"} {
		if _, ok := raw[k]; !ok {
			return Answer{}, fmt.Errorf("%w: %q is missing", ErrBadAnswer, k)
		}
	}
	var a Answer
	if err := json.Unmarshal(b, &a); err != nil {
		return Answer{}, fmt.Errorf("%w: %v", ErrBadAnswer, err)
	}
	return a, nil
}

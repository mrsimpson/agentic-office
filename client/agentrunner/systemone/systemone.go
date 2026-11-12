// Package systemone is the contract of a decision model (if-decision-model in
// docs/architecture/05-building-blocks.arc42.md, dec-byo-decision-model): the System One API.
//
// A decision model is a small model that answers questions about a state with probabilities.
// TypeSafe Jev offers this API, and Cloudflare Clef says that it is compatible with it. After each
// utterance of another participant, the agent runner posts one Request as JSON to the endpoint
// that the user gives and reads one Response.
//
// The runner asks three questions:
//
//   - "act" (type noul): should the agent act now?
//   - "topic_closed" (type noul): has a topic or a block of topics ended?
//   - "addressed" (type choice): whom did the speaker address? The criteria are the participants
//     and "nobody".
//
// A noul answer is a probability between 0 and 1. A choice answer names one criterion. The
// response may come bare or wrapped in an envelope {"result": …, "success": true}, as the
// Cloudflare REST API sends it. All names in the JSON use snake_case.
package systemone

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// The types of questions.
const (
	// TypeNoul asks for the probability that the answer is yes.
	TypeNoul = "noul"
	// TypeChoice asks which criterion fits best.
	TypeChoice = "choice"
)

// The questions that the runner asks.
const (
	QuestionAct         = "act"
	QuestionTopicClosed = "topic_closed"
	QuestionAddressed   = "addressed"
)

// Nobody is the criterion of the question "addressed" for no one in particular.
const Nobody = "nobody"

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

// State is what the decision model judges.
type State struct {
	// Session is the ID of the session.
	Session string `json:"session"`
	// Agent is the participant that the runner keeps in the meeting.
	Agent Participant `json:"agent"`
	// Role is the role of the agent, as the user gave it. It may be empty.
	Role string `json:"role"`
	// Participants are all participants that joined the session so far.
	Participants []Participant `json:"participants"`
	// Earlier are earlier utterances, oldest first, for the meaning of New.
	Earlier []Utterance `json:"earlier"`
	// New are the utterances that the agent has not got yet, oldest first.
	New []Utterance `json:"new"`
	// TurnEnded is true when the speaker of the last utterance in New ended their turn.
	TurnEnded bool `json:"turn_ended"`
}

// Question is one question about the state.
type Question struct {
	// Type is TypeNoul or TypeChoice.
	Type string `json:"type"`
	// Instructions say what the question means.
	Instructions string `json:"instructions"`
	// Criteria are the options of a choice: a key and its description. A choice needs at least
	// two.
	Criteria map[string]string `json:"criteria,omitempty"`
}

// Request is what the runner posts to the endpoint.
type Request struct {
	Model     string              `json:"model"`
	State     State               `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// Answer is the answer to one question.
type Answer struct {
	Type string `json:"type"`
	// Noul is the probability of yes, for TypeNoul.
	Noul *float64 `json:"noul,omitempty"`
	// Choice is the chosen criterion, for TypeChoice.
	Choice string `json:"choice,omitempty"`
	// Confidence is the probability of Choice.
	Confidence float64 `json:"confidence,omitempty"`
	// Probabilities are the probabilities of all criteria.
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// Response is what the endpoint answers.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	// Usage is what the endpoint reports about tokens. The runner does not read it.
	Usage json.RawMessage `json:"usage,omitempty"`
}

// ErrBadResponse means that a response does not follow the contract.
var ErrBadResponse = errors.New("bad answer of the decision model")

// Validate checks that a request asks valid questions.
func (r Request) Validate() error {
	if len(r.Questions) == 0 {
		return errors.New("no question")
	}
	for name, q := range r.Questions {
		switch q.Type {
		case TypeNoul:
		case TypeChoice:
			if len(q.Criteria) < 2 {
				return fmt.Errorf("question %q: a choice needs at least two criteria", name)
			}
		default:
			return fmt.Errorf("question %q: unknown type %q", name, q.Type)
		}
	}
	return nil
}

// ParseResponse reads the response to the questions of a request. It accepts the bare response
// and a response wrapped in {"result": …}. Every question must have an answer of its type: a
// noul between 0 and 1, or a choice that is one of the criteria.
func ParseResponse(b []byte, questions map[string]Question) (Response, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	if _, bare := raw["answers"]; !bare {
		res, ok := raw["result"]
		if !ok {
			return Response{}, fmt.Errorf("%w: no answers", ErrBadResponse)
		}
		var success *bool
		if s, ok := raw["success"]; ok {
			_ = json.Unmarshal(s, &success)
		}
		if success != nil && !*success {
			return Response{}, fmt.Errorf("%w: success is false: %s", ErrBadResponse, raw["errors"])
		}
		b = res
	}
	var r Response
	if err := json.Unmarshal(b, &r); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrBadResponse, err)
	}
	names := make([]string, 0, len(questions))
	for name := range questions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		q := questions[name]
		a, ok := r.Answers[name]
		if !ok {
			return Response{}, fmt.Errorf("%w: no answer to %q", ErrBadResponse, name)
		}
		if a.Type != "" && a.Type != q.Type {
			return Response{}, fmt.Errorf("%w: %q: type %q, want %q", ErrBadResponse, name, a.Type, q.Type)
		}
		switch q.Type {
		case TypeNoul:
			if a.Noul == nil || *a.Noul < 0 || *a.Noul > 1 {
				return Response{}, fmt.Errorf("%w: %q: noul must be a number between 0 and 1", ErrBadResponse, name)
			}
		case TypeChoice:
			if _, ok := q.Criteria[a.Choice]; !ok {
				return Response{}, fmt.Errorf("%w: %q: choice %q is not a criterion", ErrBadResponse, name, a.Choice)
			}
		}
	}
	return r, nil
}

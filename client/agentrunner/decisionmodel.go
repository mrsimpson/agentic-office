package agentrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/mrsimpson/agentic-office/client/agentrunner/systemone"
)

// decisionModelWake asks a decision model after each utterance (--wake decision-model,
// if-decision-model): an endpoint of the System One API, for example TypeSafe Jev or Cloudflare
// Clef. A noul answer at or above the threshold counts as yes. It falls back to turn-end when
// the model fails.
type decisionModelWake struct {
	fallback
	url       string
	model     string
	key       string
	threshold float64
}

func (w *decisionModelWake) String() string {
	return fmt.Sprintf("wake strategy %s: endpoint %s, model %s, threshold %g; it is asked after each utterance of another participant",
		WakeDecisionModel, w.url, w.model, w.threshold)
}

// The instructions of the questions to a decision model.
const (
	actInstructions = "The state is a meeting: the agent, its role, the participants, earlier utterances, the new utterances that the agent has not got yet, and whether the speaker of the last new utterance has ended the turn. The runner asks after each utterance, so the speaker may still be speaking. " +
		"Yes if a speaker addressed the agent, by name, by its role or by the context (for example a question that follows the agent's last contribution), or if the last new utterance concerns the role of the agent so that it should contribute now. No if the talk does not concern the agent."
	topicClosedInstructions = "Yes if the participants closed a topic or a block of topics, or the speaker of the new utterances moved on to a new topic, for example \"agreed, next point\"."
	addressedInstructions   = "Whom did the speaker of the last new utterance address?"
)

// questions returns the questions about one state: act, topic_closed, and addressed over the
// participants and nobody.
func questions(st systemone.State) map[string]systemone.Question {
	criteria := map[string]string{systemone.Nobody: "no one in particular"}
	for _, p := range st.Participants {
		if p.ParticipantID != systemone.Nobody {
			criteria[p.ParticipantID] = p.Name
		}
	}
	return map[string]systemone.Question{
		systemone.QuestionAct:         {Type: systemone.TypeNoul, Instructions: actInstructions},
		systemone.QuestionTopicClosed: {Type: systemone.TypeNoul, Instructions: topicClosedInstructions},
		systemone.QuestionAddressed:   {Type: systemone.TypeChoice, Instructions: addressedInstructions, Criteria: criteria},
	}
}

// ask asks the decision model.
func (w *decisionModelWake) ask(ctx context.Context, req checkRequest) (verdict, error) {
	st := stateOf(req)
	sr := systemone.Request{Model: w.model, State: st, Questions: questions(st)}
	if err := sr.Validate(); err != nil {
		return verdict{}, err
	}
	body, err := json.Marshal(sr)
	if err != nil {
		return verdict{}, err
	}
	out, err := w.bounded(ctx, func(ctx context.Context) ([]byte, error) {
		hr, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		return post(hr, w.key)
	})
	if err != nil {
		return verdict{}, err
	}
	resp, err := systemone.ParseResponse(out, sr.Questions)
	if err != nil {
		return verdict{}, err
	}
	act := *resp.Answers[systemone.QuestionAct].Noul
	closed := *resp.Answers[systemone.QuestionTopicClosed].Noul
	addr := resp.Answers[systemone.QuestionAddressed]
	v := verdict{act: act >= w.threshold, topicClosed: closed >= w.threshold,
		note: fmt.Sprintf("act %.2f, topic_closed %.2f, addressed %s (%.2f)%s", act, closed, addr.Choice, addr.Confidence, probabilities(addr.Probabilities))}
	if addr.Choice != systemone.Nobody {
		v.addressed = addr.Choice
	}
	return v, nil
}

// probabilities lists the probabilities of a choice for the log, sorted by key.
func probabilities(p map[string]float64) string {
	if len(p) == 0 {
		return ""
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %.2f", k, p[k])
	}
	return "; " + strings.Join(parts, ", ")
}

// stateOf maps the request of a check to the state of the System One API.
func stateOf(req checkRequest) systemone.State {
	part := func(p person) systemone.Participant {
		return systemone.Participant{ParticipantID: p.id, Name: p.name, Kind: p.kind}
	}
	utt := func(us []spoken) []systemone.Utterance {
		out := make([]systemone.Utterance, len(us))
		for i, u := range us {
			out[i] = systemone.Utterance{EventID: u.eventID, ParticipantID: u.who.id, Name: u.who.name, Kind: u.who.kind, Text: u.text}
		}
		return out
	}
	st := systemone.State{Session: req.session, Agent: part(req.agent), Role: req.role,
		Participants: make([]systemone.Participant, len(req.participants)),
		Earlier:      utt(req.earlier), New: utt(req.new), TurnEnded: req.turnEnded}
	for i, p := range req.participants {
		st.Participants[i] = part(p)
	}
	return st
}

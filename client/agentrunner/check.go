package agentrunner

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/mrsimpson/agentic-office/client/agentrunner/decision"
)

// post sends hr with the key as a bearer token and returns the body of a 200 answer.
func post(hr *http.Request, key string) ([]byte, error) {
	hr.Header.Set("Content-Type", "application/json")
	if key != "" {
		hr.Header.Set("Authorization", "Bearer "+key)
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
	return b, nil
}

// decisionRequest builds the request for a check: all pending utterances are new.
func (r *runner) decisionRequest() decision.Request {
	me := r.who(r.self)
	req := decision.Request{Session: r.o.Session, Role: r.o.Role,
		Agent:   decision.Participant{ParticipantID: r.self, Name: me.name, Kind: me.kind},
		Context: []decision.Utterance{}, New: []decision.Utterance{}, Participants: []decision.Participant{},
		TurnEnded: r.d.turnBy != ""}
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
	pending := map[uint32]bool{}
	for _, u := range r.d.pending {
		pending[u.id] = true
		req.New = append(req.New, utt(u))
	}
	last := uint32(0)
	if n := len(r.d.pending); n > 0 {
		last = r.d.pending[n-1].id
	}
	for _, u := range r.d.recent {
		if !pending[u.id] && u.id < last {
			req.Context = append(req.Context, utt(u))
		}
	}
	if n := len(req.Context); n > contextSize {
		req.Context = req.Context[n-contextSize:]
	}
	return req
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

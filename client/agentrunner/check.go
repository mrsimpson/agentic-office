package agentrunner

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
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

// person is a participant, as the wake strategies see it.
type person struct {
	id   string
	name string
	kind string // "human" or "agent"
}

// spoken is one utterance, as the wake strategies see it.
type spoken struct {
	eventID uint32
	who     person
	text    string
}

// checkRequest is what the runner asks a wake strategy to judge (dec-wake-check-per-utterance).
// It belongs to the runner; each strategy maps it to the contract of its model.
type checkRequest struct {
	session      string
	agent        person
	role         string
	participants []person // all participants that joined so far, sorted by ID
	earlier      []spoken // earlier utterances, oldest first, the agent's own included
	new          []spoken // the utterances that the agent has not got yet, oldest first
	turnEnded    bool     // the speaker of the last new utterance has ended the turn
}

// checkRequest builds the request for a check: all pending utterances are new.
func (r *runner) checkRequest() checkRequest {
	who := func(pid string) person {
		p := r.who(pid)
		return person{id: pid, name: p.name, kind: p.kind}
	}
	req := checkRequest{session: r.o.Session, agent: who(r.self), role: r.o.Role,
		participants: []person{}, earlier: []spoken{}, new: []spoken{}, turnEnded: r.d.turnBy != ""}
	ids := make([]string, 0, len(r.names))
	for pid := range r.names {
		ids = append(ids, pid)
	}
	sort.Strings(ids)
	for _, pid := range ids {
		req.participants = append(req.participants, who(pid))
	}
	utt := func(u utterance) spoken { return spoken{eventID: u.id, who: who(u.pid), text: u.text} }
	pending := map[uint32]bool{}
	for _, u := range r.d.pending {
		pending[u.id] = true
		req.new = append(req.new, utt(u))
	}
	last := uint32(0)
	if n := len(r.d.pending); n > 0 {
		last = r.d.pending[n-1].id
	}
	for _, u := range r.d.recent {
		if !pending[u.id] && u.id < last {
			req.earlier = append(req.earlier, utt(u))
		}
	}
	if n := len(req.earlier); n > contextSize {
		req.earlier = req.earlier[n-contextSize:]
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

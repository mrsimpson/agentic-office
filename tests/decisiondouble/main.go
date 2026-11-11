// Command decisiondouble is a test double of a decision model of the command kind
// (if-decision-model). It reads one request as JSON on stdin and writes one answer as JSON on
// stdout. It calls no model; it follows fixed rules on the new utterances:
//
//   - "decider fail": exits with an error;
//   - "decider slow": sleeps for a minute (the runner's time-out ends it);
//   - "decider garbage": writes text that is not JSON;
//   - otherwise act is true if a new utterance names the agent (its name), and topic_closed is
//     true if one says "next topic".
//
// It appends every request as one JSON line to the file DECIDE_RECORD.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	if p := os.Getenv("DECIDE_RECORD"); p != "" {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			fail(err)
		}
		_, _ = f.Write(append(compact(in), '\n'))
		_ = f.Close()
	}
	var req struct {
		Agent struct {
			ParticipantID string `json:"participant_id"`
			Name          string `json:"name"`
		} `json:"agent"`
		New []struct {
			Text string `json:"text"`
		} `json:"new"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		fail(err)
	}
	var all strings.Builder
	for _, u := range req.New {
		all.WriteString(u.Text + "\n")
	}
	text := all.String()
	switch {
	case strings.Contains(text, "decider fail"):
		fail(fmt.Errorf("asked to fail"))
	case strings.Contains(text, "decider slow"):
		time.Sleep(time.Minute)
	case strings.Contains(text, "decider garbage"):
		fmt.Println("this is not JSON")
		return
	}
	a := map[string]any{"act": false, "addressed": "", "topic_closed": false, "reason": "nobody named the agent"}
	if req.Agent.Name != "" && strings.Contains(text, req.Agent.Name) {
		a["act"], a["addressed"], a["reason"] = true, req.Agent.ParticipantID, "the agent was named"
	}
	if strings.Contains(strings.ToLower(text), "next topic") {
		a["topic_closed"] = true
	}
	_ = json.NewEncoder(os.Stdout).Encode(a)
}

func compact(b []byte) []byte {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return b
	}
	out, _ := json.Marshal(v)
	return out
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "decisiondouble:", err)
	os.Exit(3)
}

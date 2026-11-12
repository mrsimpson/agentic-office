// Command forwardingjudgedouble is a test double of a forwarding judge of the command kind
// (if-forwarding-judge, --wake llm). It reads one request as JSON on stdin and writes one answer as
// JSON on stdout. It calls no model. It follows fixed rules on the last new utterance, the one that
// the runner checks:
//
//   - "decider fail": exits with an error;
//   - "decider slow": sleeps for a minute (the runner's time-out ends it);
//   - "decider garbage": writes text that is not JSON;
//   - "decider hold": waits until the file DECIDE_RELEASE exists, then answers as below;
//   - otherwise act is true if the utterance names the agent (its name), and topic_closed is
//     true if it says "next topic".
//
// It appends every request as one JSON line to the file DECIDE_RECORD, before it answers.
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
	if len(req.New) == 0 {
		fail(fmt.Errorf("no new utterance"))
	}
	text := req.New[len(req.New)-1].Text
	switch {
	case strings.Contains(text, "decider fail"):
		fail(fmt.Errorf("asked to fail"))
	case strings.Contains(text, "decider slow"):
		time.Sleep(time.Minute)
	case strings.Contains(text, "decider garbage"):
		fmt.Println("this is not JSON")
		return
	case strings.Contains(text, "decider hold"):
		for release := os.Getenv("DECIDE_RELEASE"); release != ""; time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(release); err == nil {
				break
			}
		}
	}
	a := map[string]any{"act": false, "addressed": "", "topic_closed": false, "reason": "nobody named the agent"}
	if req.Agent.Name != "" && strings.Contains(text, req.Agent.Name) {
		a["act"], a["addressed"], a["reason"] = true, req.Agent.ParticipantID, "the agent was named"
	}
	if strings.Contains(strings.ToLower(text), "next topic") {
		a["topic_closed"], a["reason"] = true, "the speaker moved on"
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
	fmt.Fprintln(os.Stderr, "forwardingjudgedouble:", err)
	os.Exit(3)
}

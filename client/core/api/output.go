package coreapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/logfile"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
)

// This file holds what the CLI (--json) and the MCP server share in their output and input, so
// that both have the same shapes (dec-cli-and-mcp).

// EventJSON returns the event as in the local log: its canonical, compact JSON.
func EventJSON(ev *sessionstreamv1.Event) json.RawMessage {
	line, err := logfile.MarshalLine(ev)
	if err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(bytes.TrimRight(line, "\n"))
}

// MessageJSON returns the canonical, compact JSON form of a message (camelCase field names).
func MessageJSON(m proto.Message) json.RawMessage {
	raw, err := protojson.Marshal(m)
	if err != nil {
		return json.RawMessage("null")
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(b.Bytes())
}

// ResultJSON is the output of a session command: {"event": …, "duplicate": …}.
func ResultJSON(res *sessioncommandsv1.CommandResult) map[string]any {
	return map[string]any{"event": EventJSON(res.GetEvent()), "duplicate": res.GetDuplicate()}
}

// ReleaseStatusJSON is the release status of a session: {"state": "open"|"waiting"|"held"|"released",
// "deadline": RFC 3339 time, only while waiting}.
func ReleaseStatusJSON(rs *sessioncommandsv1.ReleaseStatus) map[string]any {
	out := map[string]any{"state": releaseStateName(rs.GetState())}
	if d := rs.GetDeadline(); d != nil {
		out["deadline"] = d.AsTime().UTC().Format(time.RFC3339)
	}
	return out
}

func releaseStateName(s sessioncommandsv1.ReleaseState) string {
	switch s {
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_OPEN:
		return "open"
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_WAITING:
		return "waiting"
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_HELD:
		return "held"
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_RELEASED:
		return "released"
	}
	return "unknown"
}

// EndJSON is the output of the end of a session: {"event", "duplicate", "release": the release
// status (ReleaseStatusJSON)}.
func EndJSON(res *sessioncommandsv1.EndSessionResponse) map[string]any {
	out := ResultJSON(res.GetResult())
	out["release"] = ReleaseStatusJSON(res.GetRelease())
	return out
}

// ReleaseJSON is the output of a release: {"event", "duplicate", "quotes": [{"path", "line",
// "utteranceEventId"}]}. A quote is a place where a staged file quotes a sentence off the record
// word for word.
func ReleaseJSON(res *sessioncommandsv1.ReleaseSessionResponse) map[string]any {
	out := ResultJSON(res.GetResult())
	quotes := []map[string]any{}
	for _, q := range res.GetQuotes() {
		quotes = append(quotes, map[string]any{"path": q.GetPath(), "line": q.GetLine(), "utteranceEventId": q.GetUtteranceEventId()})
	}
	out["quotes"] = quotes
	return out
}

// ReleaseHint tells the humans of an ended session how its release goes on, in plain words.
func ReleaseHint(sessionID string, rs *sessioncommandsv1.ReleaseStatus) string {
	switch rs.GetState() {
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_RELEASED:
		return "The session is released: what it staged is in the artifact repository of the tenant."
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_HELD:
		return fmt.Sprintf("The session is held. A human who took part releases it with \"office session release %s\".", sessionID)
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_WAITING:
		when := "the end of the grace period"
		if d := rs.GetDeadline(); d != nil {
			when = d.AsTime().UTC().Format("2006-01-02 15:04 UTC")
		}
		return fmt.Sprintf("What the session staged waits for its release until %s. Every human who took part can "+
			"release it now with \"office session release %s\", or hold it with \"office session hold %s\".", when, sessionID, sessionID)
	}
	return ""
}

// QuoteLine describes a quote of a sentence off the record for a human.
func QuoteLine(q *sessioncommandsv1.Quote) string {
	return fmt.Sprintf("%s:%d quotes the sentence off the record of event %d word for word", q.GetPath(), q.GetLine(), q.GetUtteranceEventId())
}

// ErrBadInput marks input that a caller must fix (a usage error).
var ErrBadInput = errors.New("bad input")

// ParseDeck reads a deck from a file named name: Markdown if the name ends with ".md" (see
// ParseDeckMarkdown), else JSON in the form of RenderDeckRequest ({"title": …, "slides":
// [{"title": …, "bullets": […], "notes": …}]}).
func ParseDeck(name string, b []byte) (*slidesv1.RenderDeckRequest, error) {
	if strings.EqualFold(filepath.Ext(name), ".md") {
		req, err := ParseDeckMarkdown(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return req, nil
	}
	req := &slidesv1.RenderDeckRequest{}
	if err := protojson.Unmarshal(b, req); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBadInput, name, err)
	}
	return req, nil
}

// ParseDeckMarkdown reads a deck in Markdown: "# " is the deck title, "## " starts a slide,
// "- " or "* " is a bullet, other text becomes the notes of the slide.
func ParseDeckMarkdown(b []byte) (*slidesv1.RenderDeckRequest, error) {
	req := &slidesv1.RenderDeckRequest{}
	var cur *slidesv1.Slide
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "## "):
			cur = &slidesv1.Slide{Title: strings.TrimSpace(line[3:])}
			req.Slides = append(req.Slides, cur)
		case strings.HasPrefix(line, "# "):
			req.Title = strings.TrimSpace(line[2:])
		case line == "":
		case cur == nil:
			return nil, fmt.Errorf("%w: text before the first slide (\"## title\")", ErrBadInput)
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			cur.Bullets = append(cur.Bullets, strings.TrimSpace(line[2:]))
		default:
			cur.Notes = strings.TrimSpace(cur.Notes + "\n" + line)
		}
	}
	return req, sc.Err()
}

// ErrNothingOffTheRecord means that the local log has no own sentence that can be off the record.
var ErrNothingOffTheRecord = errors.New("the local log has no own sentence to mark off the record")

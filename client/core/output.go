package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/logfile"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
)

// This file holds what the CLI (--json) and the MCP server share in their output and input, so
// that both have the same shapes (dec-cli-and-mcp).

// EventJSON returns the event as in the local log: its canonical, compact JSON.
func EventJSON(ev *sessionv1.Event) json.RawMessage {
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
func ResultJSON(res *sessionv1.CommandResult) map[string]any {
	return map[string]any{"event": EventJSON(res.GetEvent()), "duplicate": res.GetDuplicate()}
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

// MarkOffTheRecordLast marks off the record the newest own sentence that is not off the record yet. It reads the local log.
func (c *Client) MarkOffTheRecordLast(ctx context.Context, sessionID string) (*sessionv1.CommandResult, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	p, err := c.Paths(sessionID, pid)
	if err != nil {
		return nil, err
	}
	evs, err := ReadLog(p.Log)
	if err != nil {
		return nil, err
	}
	g := NewGateway(pid)
	for _, ev := range evs {
		g.Apply(ev)
	}
	u := g.LastSent()
	if u == nil {
		return nil, ErrNothingOffTheRecord
	}
	return c.MarkOffTheRecord(ctx, sessionID, u.EventID)
}

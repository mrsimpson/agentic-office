package coreapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	sessionimportv1 "github.com/mrsimpson/agentic-office/hub/session/importapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
)

// This file is the import of conversations (dec-import-format, if-session-import). The format is
// documented in README.md, section "Import a conversation".

// Conversation is a conversation in the import format: one JSON document.
type Conversation struct {
	// Source says where the conversation comes from. Required.
	Source string `json:"source" jsonschema:"Required. Where the conversation comes from, for example \"Zoom meeting 2026-11-06\" or a URL."`
	// Title is optional.
	Title        string                    `json:"title,omitempty" jsonschema:"Title of the conversation. Optional."`
	Participants []ConversationParticipant `json:"participants" jsonschema:"Everyone who speaks in the conversation."`
	Utterances   []ConversationUtterance   `json:"utterances" jsonschema:"The messages, in order."`
}

// ConversationParticipant is a speaker of a conversation.
type ConversationParticipant struct {
	// ID names the participant inside the file; utterances refer to it as speaker.
	ID   string `json:"id" jsonschema:"Short ID inside the conversation, for example \"ada\": 1 to 64 letters, digits, '.', '_' or '-'. Utterances name it as speaker."`
	Name string `json:"name" jsonschema:"Display name."`
	// Kind is "human" or "agent".
	Kind string `json:"kind" jsonschema:"\"human\" or \"agent\"."`
}

// ConversationUtterance is one message of a conversation.
type ConversationUtterance struct {
	Speaker string `json:"speaker" jsonschema:"The id of the participant who said or wrote it."`
	Text    string `json:"text" jsonschema:"What was said or written."`
	// Time is optional, in RFC 3339, for example "2026-11-06T10:00:00Z".
	Time string `json:"time,omitempty" jsonschema:"When, in RFC 3339, for example \"2026-11-06T10:00:00Z\". Optional."`
	// Modality is "text" (default) or "voice".
	Modality string `json:"modality,omitempty" jsonschema:"\"text\" (default) for written messages, \"voice\" for speech."`
}

// ImportPageSize is the number of utterances that Import sends per page.
const ImportPageSize = 500

// ParseConversation reads a conversation in the import format. Unknown fields are an error, so a
// typo does not get lost. All errors wrap ErrBadInput.
func ParseConversation(r io.Reader) (*Conversation, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var c Conversation
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%w: the conversation is not valid JSON in the import format: %v", ErrBadInput, err)
	}
	if dec.More() {
		return nil, fmt.Errorf("%w: more than one JSON document; a file holds one conversation", ErrBadInput)
	}
	if _, err := c.Pages(); err != nil {
		return nil, err
	}
	return &c, nil
}

// ParseConversationBytes is ParseConversation for a byte slice.
func ParseConversationBytes(b []byte) (*Conversation, error) {
	return ParseConversation(bytes.NewReader(b))
}

func badInput(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadInput, fmt.Sprintf(format, a...))
}

// Pages checks the conversation and splits it into the pages of the import interface. The
// session ID and the command IDs are not set.
func (c *Conversation) Pages() ([]*sessionimportv1.ImportConversationRequest, error) {
	if strings.TrimSpace(c.Source) == "" {
		return nil, badInput("source is required: where the conversation comes from")
	}
	known := map[string]bool{}
	var ps []*sessionimportv1.ImportedParticipant
	for i, p := range c.Participants {
		var kind sessionstreamv1.ParticipantKind
		switch p.Kind {
		case "human":
			kind = sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN
		case "agent":
			kind = sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT
		default:
			return nil, badInput("participants[%d].kind must be \"human\" or \"agent\"", i)
		}
		switch {
		case p.ID == "":
			return nil, badInput("participants[%d].id is required", i)
		case known[p.ID]:
			return nil, badInput("participants[%d].id %q is repeated", i, p.ID)
		case strings.TrimSpace(p.Name) == "":
			return nil, badInput("participants[%d].name is required", i)
		}
		known[p.ID] = true
		ps = append(ps, &sessionimportv1.ImportedParticipant{Key: p.ID, DisplayName: p.Name, Kind: kind})
	}
	var us []*sessionimportv1.ImportedUtterance
	for i, u := range c.Utterances {
		if !known[u.Speaker] {
			return nil, badInput("utterances[%d].speaker %q is not the id of a participant", i, u.Speaker)
		}
		if strings.TrimSpace(u.Text) == "" {
			return nil, badInput("utterances[%d].text is required", i)
		}
		m := sessionstreamv1.Modality_MODALITY_TEXT
		switch u.Modality {
		case "", "text":
		case "voice":
			m = sessionstreamv1.Modality_MODALITY_VOICE
		default:
			return nil, badInput("utterances[%d].modality must be \"text\" or \"voice\"", i)
		}
		iu := &sessionimportv1.ImportedUtterance{Speaker: u.Speaker, Text: u.Text, Modality: m}
		if u.Time != "" {
			t, err := time.Parse(time.RFC3339Nano, u.Time)
			if err != nil {
				return nil, badInput("utterances[%d].time must be RFC 3339, for example 2026-11-06T10:00:00Z", i)
			}
			iu.Time = timestamppb.New(t)
		}
		us = append(us, iu)
	}
	// Participants go first, so every utterance names a participant of the same or an earlier page.
	var pages []*sessionimportv1.ImportConversationRequest
	for len(pages) == 0 || len(ps) > 0 || len(us) > 0 {
		p := &sessionimportv1.ImportConversationRequest{}
		n := min(len(ps), 1000)
		p.Participants, ps = ps[:n], ps[n:]
		if len(ps) == 0 {
			n = min(len(us), ImportPageSize)
			p.Utterances, us = us[:n], us[n:]
		}
		pages = append(pages, p)
	}
	pages[0].Conversation = &sessionimportv1.Conversation{Source: c.Source, Title: c.Title}
	pages[len(pages)-1].Complete = true
	return pages, nil
}

// Imported is the result of Import.
type Imported struct {
	SessionID    string
	Pages        int
	FirstEventID uint32
	LastEventID  uint32
	Participants int
	Utterances   int
	// TreeHead is the tree head signed by the hub over the whole imported log.
	TreeHead *sessionstreamv1.SignedTreeHead
}

// ImportedJSON is the output of an import in the CLI (--json) and the MCP tool.
func ImportedJSON(im *Imported) map[string]any {
	return map[string]any{"sessionId": im.SessionID, "pages": im.Pages, "firstEventId": im.FirstEventID,
		"lastEventId": im.LastEventID, "participants": im.Participants, "utterances": im.Utterances,
		"treeHead": MessageJSON(im.TreeHead)}
}

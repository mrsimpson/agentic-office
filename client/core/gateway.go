package core

import (
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/events"
)

// Status of an own utterance in the gateway view (dec-gateway-cli).
type Status string

const (
	// StatusPartial: the ears are still transcribing. Never in the log (dec-no-partials-in-log).
	StatusPartial Status = "partial"
	// StatusFinal: the ears have the final text; it is not confirmed by the server yet.
	StatusFinal Status = "final"
	// StatusSent: the utterance is in the session log.
	StatusSent Status = "sent"
	// StatusOffTheRecord: the speaker marked the utterance off the record.
	StatusOffTheRecord Status = "off_the_record"
)

// Utterance is an own utterance in the gateway view.
type Utterance struct {
	// CommandID is the client ID of the utterance. The ears create it when the text is final.
	CommandID string `json:"commandId,omitempty"`
	// EventID is the ID of the utterance.final event, once it is sent.
	EventID uint32 `json:"eventId,omitempty"`
	Text    string `json:"text"`
	Status  Status `json:"status"`
}

// Change is one thing the gateway view shows.
type Change struct {
	// Utterance is set if an own utterance changed its status.
	Utterance *Utterance `json:"utterance,omitempty"`
	// Paused is set if the participant was paused (true) or resumed (false).
	Paused *bool `json:"paused,omitempty"`
	// Left is true if the participant left the session.
	Left bool `json:"left,omitempty"`
	// Ended is true if the session ended.
	Ended bool `json:"ended,omitempty"`
}

// Gateway is the state behind the gateway view of one participant: its own utterances with
// their status and whether it is paused. Feed it the local log with Apply and, from the ears,
// the local status with Local. It is not safe for concurrent use.
type Gateway struct {
	ParticipantID string
	Paused        bool
	Ended         bool
	Left          bool

	utts    []*Utterance
	byCmd   map[string]*Utterance
	byEvent map[uint32]*Utterance
}

// NewGateway returns an empty gateway state for participantID.
func NewGateway(participantID string) *Gateway {
	return &Gateway{ParticipantID: participantID, byCmd: map[string]*Utterance{}, byEvent: map[uint32]*Utterance{}}
}

// Local records the local status of an utterance that is not in the log yet (partial or
// final). The ears (#8) call it; cmdID is the command ID the utterance will be sent with.
func (g *Gateway) Local(cmdID, text string, status Status) Change {
	u := g.byCmd[cmdID]
	if u == nil {
		u = &Utterance{CommandID: cmdID}
		g.byCmd[cmdID] = u
		g.utts = append(g.utts, u)
	}
	if u.Status != StatusSent && u.Status != StatusOffTheRecord {
		u.Text, u.Status = text, status
	}
	return Change{Utterance: copyOf(u)}
}

// Apply takes one event of the local log and returns what changed for the view.
func (g *Gateway) Apply(ev *sessionv1.Event) []Change {
	me := g.ParticipantID
	switch ev.GetType() {
	case events.UtteranceFinal:
		p := ev.GetUtteranceFinal()
		if p.GetParticipantId() != me {
			return nil
		}
		u := g.byCmd[ev.GetCommandId()]
		if u == nil || ev.GetCommandId() == "" {
			u = &Utterance{CommandID: ev.GetCommandId()}
			g.utts = append(g.utts, u)
			if u.CommandID != "" {
				g.byCmd[u.CommandID] = u
			}
		}
		if p.GetOffTheRecord() {
			// A placeholder: the sentence was marked off the record before this copy was made.
			// Keep the text the gateway view already had, if any.
			u.EventID, u.Status = ev.GetId(), StatusOffTheRecord
		} else {
			u.EventID, u.Text, u.Status = ev.GetId(), p.GetText(), StatusSent
		}
		g.byEvent[u.EventID] = u
		return []Change{{Utterance: copyOf(u)}}
	case events.UtteranceOffTheRecord:
		u := g.byEvent[ev.GetUtteranceOffTheRecord().GetUtteranceEventId()]
		if u == nil {
			return nil
		}
		u.Status = StatusOffTheRecord
		return []Change{{Utterance: copyOf(u)}}
	case events.UtteranceCorrected:
		u := g.byEvent[ev.GetUtteranceCorrected().GetUtteranceEventId()]
		if u == nil {
			return nil
		}
		u.Text = ev.GetUtteranceCorrected().GetText()
		return []Change{{Utterance: copyOf(u)}}
	case events.ParticipantPaused:
		if ev.GetParticipantPaused().GetParticipantId() == me {
			g.Paused = true
			return []Change{{Paused: ptr(true)}}
		}
	case events.ParticipantResumed:
		if ev.GetParticipantResumed().GetParticipantId() == me {
			g.Paused = false
			return []Change{{Paused: ptr(false)}}
		}
	case events.ParticipantLeft:
		if ev.GetParticipantLeft().GetParticipantId() == me {
			g.Left = true
			return []Change{{Left: true}}
		}
	case events.SessionEnded:
		g.Ended = true
		return []Change{{Ended: true}}
	}
	return nil
}

// LastSent returns the newest own utterance that is sent and not off the record, or nil. It is the
// sentence that the off-the-record key marks.
func (g *Gateway) LastSent() *Utterance {
	for i := len(g.utts) - 1; i >= 0; i-- {
		if g.utts[i].Status == StatusSent {
			return copyOf(g.utts[i])
		}
	}
	return nil
}

// Utterances returns all own utterances, oldest first.
func (g *Gateway) Utterances() []Utterance {
	out := make([]Utterance, len(g.utts))
	for i, u := range g.utts {
		out[i] = *u
	}
	return out
}

func copyOf(u *Utterance) *Utterance { c := *u; return &c }

func ptr[T any](v T) *T { return &v }

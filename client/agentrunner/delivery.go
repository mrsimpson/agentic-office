package agentrunner

import (
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
)

// utterance is an utterance of the session log.
type utterance struct {
	id   uint32
	pid  string
	text string
}

// trigger is what an event asks of the runner.
type trigger int

const (
	triggerNone      trigger = iota
	triggerUtterance         // an utterance of another participant: check it
	triggerTurnEnd           // another participant ended their turn after pending utterances
	triggerNotice            // a delivered utterance went off the record: tell the agent soon
)

// delivery keeps what the agent gets and when (dec-agent-runner-delivery). It sees every event
// after the agent's join, in the order of the log, and keeps:
//
//   - pending: utterances of others that the agent has not got, in order. The agent's own
//     utterances and placeholders of sentences off the record never get in. A sentence that
//     goes off the record while it is pending is dropped.
//   - offNotices: delivered utterances that went off the record later; the next prompt names them.
//   - turnBy: the participant whose turn.end came after the last pending utterance, or empty.
//   - the state of the wake-up: whether a wake-up is due, and what the decision model said
//     (topic closed, addressed) since the last delivery.
//   - recent: the last utterances of everybody, the agent included, as context for a decision
//     model. Sentences off the record are removed.
type delivery struct {
	recent      []utterance
	pending     []utterance
	delivered   map[uint32]bool
	offNotices  []uint32
	turnBy      string
	wake        bool   // wake the agent as soon as it is free
	topicClosed bool   // the decision model said that a block of topics has ended
	addressed   string // whom the speaker addressed, by the decision model
	ended       bool   // session.ended
	left        bool   // the agent's own participant.left
}

// batch is what one prompt delivers.
type batch struct {
	utterances  []utterance
	offNotices  []uint32
	turnBy      string
	topicClosed bool
	addressed   string
}

func (b batch) empty() bool { return len(b.utterances) == 0 && len(b.offNotices) == 0 }

// span returns the event IDs of the first and the last utterance, or 0, 0.
func (b batch) span() (uint32, uint32) {
	if len(b.utterances) == 0 {
		return 0, 0
	}
	return b.utterances[0].id, b.utterances[len(b.utterances)-1].id
}

// apply takes one event of the log after the agent's join and says what it asks of the runner.
func (d *delivery) apply(ev *sessionstreamv1.Event, self string) trigger {
	if d.delivered == nil {
		d.delivered = map[uint32]bool{}
	}
	switch ev.GetType() {
	case events.UtteranceFinal:
		u := ev.GetUtteranceFinal()
		if u.GetOffTheRecord() {
			return triggerNone // a placeholder: no text
		}
		ut := utterance{id: ev.GetId(), pid: u.GetParticipantId(), text: u.GetText()}
		d.recent = append(d.recent, ut)
		if len(d.recent) > 2*contextSize {
			d.recent = append([]utterance(nil), d.recent[len(d.recent)-contextSize:]...)
		}
		if u.GetParticipantId() == self {
			return triggerNone
		}
		d.pending = append(d.pending, ut)
		d.turnBy = ""
		return triggerUtterance
	case events.UtteranceOffTheRecord:
		id := ev.GetUtteranceOffTheRecord().GetUtteranceEventId()
		for i, u := range d.recent {
			if u.id == id {
				d.recent = append(d.recent[:i:i], d.recent[i+1:]...)
				break
			}
		}
		for i, u := range d.pending {
			if u.id == id {
				d.pending = append(d.pending[:i:i], d.pending[i+1:]...)
				return triggerNone
			}
		}
		if d.delivered[id] {
			d.offNotices = append(d.offNotices, id)
			return triggerNotice
		}
	case events.TurnEnd:
		if pid := ev.GetTurnEnd().GetParticipantId(); pid != self && len(d.pending) > 0 {
			d.turnBy = pid
			return triggerTurnEnd
		}
	case events.SessionEnded:
		d.ended = true
	case events.ParticipantLeft:
		if ev.GetParticipantLeft().GetParticipantId() == self {
			d.left = true
		}
	}
	return triggerNone
}

// pendingUpTo reports whether an utterance up to the event ID last is still pending, so that
// a check of it still counts.
func (d *delivery) pendingUpTo(last uint32) bool {
	return len(d.pending) > 0 && d.pending[0].id <= last
}

// take returns everything that waits for the agent and resets the wake-up.
func (d *delivery) take() batch {
	b := batch{utterances: d.pending, offNotices: d.offNotices, turnBy: d.turnBy,
		topicClosed: d.topicClosed, addressed: d.addressed}
	if d.delivered == nil {
		d.delivered = map[uint32]bool{}
	}
	for _, u := range b.utterances {
		d.delivered[u.id] = true
	}
	d.pending, d.offNotices, d.turnBy = nil, nil, ""
	d.wake, d.topicClosed, d.addressed = false, false, ""
	return b
}

package agentrunner

import (
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/events"
)

// utterance is an utterance of another participant that the agent has not got yet.
type utterance struct {
	id   uint32
	pid  string
	text string
}

// delivery decides what the agent gets and when (dec-wake-on-turn-end,
// dec-agent-runner-delivery). It sees every event after the agent's join, in the order of the
// log, and keeps:
//
//   - pending: utterances of others that the agent has not got, in order. The agent's own
//     utterances and placeholders of sentences off the record never get in. A sentence that
//     goes off the record while it is pending is dropped.
//   - offNotices: delivered utterances that went off the record later; the next prompt names them.
//   - the last turn.end of another participant that is not delivered yet. While the agent works,
//     later turns only move it on, so they go out together.
//   - recent: the last utterances of everybody, the agent included, as context for a decision
//     model. Sentences off the record are removed.
type delivery struct {
	recent     []utterance
	pending    []utterance
	delivered  map[uint32]bool
	offNotices []uint32
	turnEnd    uint32 // the last turn.end not delivered yet, or 0
	turnBy     string // whose turn ended
	ended      bool   // session.ended
	left       bool   // the agent's own participant.left
}

// batch is what one prompt delivers.
type batch struct {
	utterances []utterance
	offNotices []uint32
	turnEnd    uint32
	turnBy     string
}

func (b batch) empty() bool { return len(b.utterances) == 0 && len(b.offNotices) == 0 }

// first returns the event ID of the first utterance, or 0.
func (b batch) first() uint32 {
	if len(b.utterances) == 0 {
		return 0
	}
	return b.utterances[0].id
}

// apply takes one event of the log after the agent's join.
func (d *delivery) apply(ev *sessionv1.Event, self string) {
	if d.delivered == nil {
		d.delivered = map[uint32]bool{}
	}
	switch ev.GetType() {
	case events.UtteranceFinal:
		u := ev.GetUtteranceFinal()
		if u.GetOffTheRecord() {
			return
		}
		ut := utterance{id: ev.GetId(), pid: u.GetParticipantId(), text: u.GetText()}
		d.recent = append(d.recent, ut)
		if len(d.recent) > 2*contextSize {
			d.recent = append([]utterance(nil), d.recent[len(d.recent)-contextSize:]...)
		}
		if u.GetParticipantId() != self {
			d.pending = append(d.pending, ut)
		}
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
				return
			}
		}
		if d.delivered[id] {
			d.offNotices = append(d.offNotices, id)
		}
	case events.TurnEnd:
		if pid := ev.GetTurnEnd().GetParticipantId(); pid != self {
			d.turnEnd, d.turnBy = ev.GetId(), pid
		}
	case events.SessionEnded:
		d.ended = true
	case events.ParticipantLeft:
		if ev.GetParticipantLeft().GetParticipantId() == self {
			d.left = true
		}
	}
}

// due reports whether a change of speaker waits to be delivered.
func (d *delivery) due() bool { return d.turnEnd != 0 }

// take returns the batch for the waiting change of speaker: the pending utterances before it and
// the notices. Utterances after it stay pending for the next change of speaker.
func (d *delivery) take() batch {
	b := batch{turnEnd: d.turnEnd, turnBy: d.turnBy, offNotices: d.offNotices}
	n := 0
	for n < len(d.pending) && d.pending[n].id < d.turnEnd {
		n++
	}
	b.utterances = append([]utterance(nil), d.pending[:n]...)
	d.pending = d.pending[n:]
	d.offNotices, d.turnEnd, d.turnBy = nil, 0, ""
	d.markDelivered(b)
	return b
}

// takeAll returns everything that is left, for the last prompt.
func (d *delivery) takeAll() batch {
	b := batch{utterances: d.pending, offNotices: d.offNotices, turnEnd: d.turnEnd, turnBy: d.turnBy}
	d.pending, d.offNotices, d.turnEnd, d.turnBy = nil, nil, 0, ""
	d.markDelivered(b)
	return b
}

func (d *delivery) markDelivered(b batch) {
	if d.delivered == nil {
		d.delivered = map[uint32]bool{}
	}
	for _, u := range b.utterances {
		d.delivered[u.id] = true
	}
}

// requeue puts a batch back that did not wake the agent: its utterances go before the pending
// ones and its notices stay, so the next prompt carries them (nothing is lost). A sentence of
// the batch that went off the record meanwhile came in as a notice; it is dropped with its
// notice, because the agent never got it.
func (d *delivery) requeue(b batch) {
	off := map[uint32]bool{}
	for _, id := range d.offNotices {
		off[id] = true
	}
	var back []utterance
	dropped := map[uint32]bool{}
	for _, u := range b.utterances {
		delete(d.delivered, u.id)
		if off[u.id] {
			dropped[u.id] = true
			continue
		}
		back = append(back, u)
	}
	notices := append([]uint32(nil), b.offNotices...)
	for _, id := range d.offNotices {
		if !dropped[id] {
			notices = append(notices, id)
		}
	}
	d.pending = append(back, d.pending...)
	d.offNotices = notices
}

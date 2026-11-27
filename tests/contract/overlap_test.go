package contract

// Contract tests of the overlap hint (dec-overlap-hint): the hub stores it on a voice utterance
// and passes it on, the placeholder of an utterance off the record does not carry it, and the
// agent runner gives it to the agent. They start the real office-hub and use only its interfaces.

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/merkle"
)

// voice returns a voice utterance of pid with the overlap hint set as given.
func (m *meeting) voice(pid, text string, overlap bool) *sessioncommandsv1.SubmitUtteranceRequest {
	req := m.utterance(pid, text)
	req.Modality, req.Overlap = sessionstreamv1.Modality_MODALITY_VOICE, overlap
	return req
}

func (m *meeting) submit(pid string, req *sessioncommandsv1.SubmitUtteranceRequest) *sessionstreamv1.Event {
	m.t.Helper()
	res, err := m.s(pid).SubmitUtterance(m.ctx(), req)
	if err != nil {
		m.t.Fatalf("submit %q: %v", req.GetText(), err)
	}
	return res.GetResult().GetEvent()
}

// A voice utterance with the hint carries it in the command result, in the stream and in the
// events that a reader fetches; an utterance without the hint does not. A typed utterance cannot
// carry the hint.
func TestOverlapHintIsStoredAndPassedOn(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	with := m.submit(ada, m.voice(ada, "We should maybe ship it.", true))
	without := m.submit(ada, m.voice(ada, "Clear sentence.", false))
	typed := m.say(ada, "Typed sentence.")
	if !with.GetUtteranceFinal().GetOverlap() || without.GetUtteranceFinal().GetOverlap() || typed.GetUtteranceFinal().GetOverlap() {
		t.Fatalf("command results: %v, %v, %v", with, without, typed)
	}

	for what, log := range map[string][]*sessionstreamv1.Event{"stream": m.read(0, 4), "events from a cursor": m.events()} {
		if len(log) < 4 {
			t.Fatalf("%s: %d events", what, len(log))
		}
		got := func(ev *sessionstreamv1.Event) bool { return log[ev.GetId()-1].GetUtteranceFinal().GetOverlap() }
		if !got(with) || got(without) || got(typed) {
			t.Errorf("%s: overlap of the three utterances is %v, %v, %v; want true, false, false", what, got(with), got(without), got(typed))
		}
	}

	req := m.utterance(ada, "Typed with a hint.")
	req.Overlap = true
	_, err := m.s(ada).SubmitUtterance(m.ctx(), req)
	wantCode(t, "typed utterance with the overlap hint", err, connect.CodeInvalidArgument)
	if n := len(m.read(0, 4)); n != 4 {
		t.Fatalf("log has %d events", n)
	}
}

// The placeholder of an utterance off the record does not carry the hint, and the log still
// verifies: the leaf hash of the placeholder is the one of the original, which had the hint.
func TestOverlapHintGoneFromPlaceholder(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "Public sentence.")
	secret := m.submit(ada, m.voice(ada, "The budget is ten million.", true))
	original := m.read(0, 3)
	if !original[2].GetUtteranceFinal().GetOverlap() {
		t.Fatalf("original: %v", original[2])
	}
	if _, err := m.s(ada).MarkOffTheRecord(m.ctx(), &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: ada, UtteranceEventId: secret.GetId()}); err != nil {
		t.Fatal(err)
	}
	log := m.events()
	ph := log[secret.GetId()-1]
	if f := ph.GetUtteranceFinal(); !f.GetOffTheRecord() || f.GetText() != "" || f.GetOverlap() || len(ph.GetLeafHash()) != merkle.HashSize {
		t.Fatalf("placeholder: %v", ph)
	}
	if err := merkle.VerifyEvents(log, m.head(0), hubKey(t, m.reader.Commands)); err != nil {
		t.Fatalf("log with the placeholder: %v", err)
	}
}

// The runner gives the hint to the agent with the utterance, in the wake-up, and explains it in
// its first prompt. A wake-up without a hinted utterance has no note. The hint is also in the
// local log of a participant.
func TestAgentRunnerOverlapHint(t *testing.T) {
	mt := newMeetingWithRunner(t, nil)
	ada := mt.ada.actorOf(t)
	voice := func(text string, overlap bool) uint32 {
		t.Helper()
		res, err := ada.Commands.SubmitUtterance(ctx10(t), &sessioncommandsv1.SubmitUtteranceRequest{SessionId: mt.sid,
			CommandId: coreapi.NewCommandID(), ParticipantId: "ada", Text: text,
			Modality: sessionstreamv1.Modality_MODALITY_VOICE, Overlap: overlap})
		if err != nil {
			t.Fatal(err)
		}
		return res.GetResult().GetEvent().GetId()
	}
	const note = "[another voice may be in this sentence]"

	id := voice("Please book the room for", true)
	mt.turnEnd(mt.ada)
	ps := waitPrompt(t, mt.rec, 2)
	if p1 := ps[0]; !strings.Contains(p1, note) || !strings.Contains(p1, "may hold words of someone else") ||
		!strings.Contains(p1, "asked the speaker one short question") {
		t.Fatalf("prompt 1 must explain the note:\n%s", p1)
	}
	if !strings.Contains(ps[1], "Ada (human): Please book the room for "+note) {
		t.Fatalf("prompt 2 must carry the note on the line of the utterance:\n%s", ps[1])
	}
	waitPromptEnd(t, mt.rec, 2)

	for _, l := range waitLog(t, mt.adaLog, id) {
		if l.ev.GetId() == id && (!l.ev.GetUtteranceFinal().GetOverlap() || !strings.Contains(l.raw, `"overlap":true`)) {
			t.Fatalf("local log line without the hint: %s", l.raw)
		}
	}

	voice("Thursday at ten.", false)
	mt.turnEnd(mt.ada)
	ps = waitPrompt(t, mt.rec, 3)
	if !strings.Contains(ps[2], "Ada (human): Thursday at ten.") || strings.Contains(ps[2], "another voice") {
		t.Fatalf("prompt 3 must have no note:\n%s", ps[2])
	}
}

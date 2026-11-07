package contract

// Contract tests of the meeting session (bb-session-service) through if-session-commands and
// if-session-stream. They start the real office-server and use only the Connect clients.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	identityv1 "github.com/mrsimpson/agentic-office/server/identity/api"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"
	"github.com/mrsimpson/agentic-office/server/session/api/events"
	sessionv1connect "github.com/mrsimpson/agentic-office/server/session/api/sessionv1connect"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// meeting is one session on a fresh office-server. Every participant has its own key and
// clients; reader is a participant that only subscribes.
type meeting struct {
	t      *testing.T
	srv    *contracttest.Server
	sid    string
	actors map[string]*contracttest.Actor
	reader *contracttest.Actor
}

func newMeeting(t *testing.T) *meeting {
	t.Helper()
	srv := contracttest.StartServer(t, nil)
	return &meeting{t: t, srv: srv, sid: "s-" + ids.NewCommandID(), actors: map[string]*contracttest.Actor{},
		reader: srv.Participant(t, "reader", "Reader", identityv1.ActorKind_ACTOR_KIND_AGENT)}
}

// participantID turns a display name into a participant ID, for example "writer 1" -> "writer-1".
func participantID(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "-"))
}

// register registers a participant without joining and returns its actor.
func (m *meeting) register(name string, kind sessionv1.ParticipantKind) *contracttest.Actor {
	m.t.Helper()
	a := m.srv.Participant(m.t, participantID(name), name, actorKind(kind))
	m.actors[a.ParticipantID] = a
	return a
}

// s returns the session client of a participant.
func (m *meeting) s(pid string) sessionv1connect.SessionServiceClient {
	m.t.Helper()
	a := m.actors[pid]
	if a == nil {
		m.t.Fatalf("no actor for participant %q", pid)
	}
	return a.Session
}

func (m *meeting) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.t.Cleanup(cancel)
	return ctx
}

func (m *meeting) join(name string, kind sessionv1.ParticipantKind) string {
	m.t.Helper()
	res, err := m.register(name, kind).Session.Join(m.ctx(), &sessionv1.JoinRequest{SessionId: m.sid,
		CommandId: ids.NewCommandID(), DisplayName: name, Kind: kind})
	if err != nil {
		m.t.Fatalf("join %s: %v", name, err)
	}
	return res.GetParticipantId()
}

func (m *meeting) say(pid, text string) *sessionv1.Event {
	m.t.Helper()
	res, err := m.s(pid).SubmitUtterance(m.ctx(), m.utterance(pid, text))
	if err != nil {
		m.t.Fatalf("say %q: %v", text, err)
	}
	return res.GetResult().GetEvent()
}

func (m *meeting) utterance(pid, text string) *sessionv1.SubmitUtteranceRequest {
	return &sessionv1.SubmitUtteranceRequest{SessionId: m.sid, CommandId: ids.NewCommandID(), ParticipantId: pid,
		Text: text, Modality: sessionv1.Modality_MODALITY_TEXT}
}

// readAll subscribes after cursor and reads until the stream ends (after session.ended).
func (m *meeting) readAll(cursor uint32) []*sessionv1.Event {
	m.t.Helper()
	st, err := m.reader.Session.Subscribe(m.ctx(), &sessionv1.SubscribeRequest{SessionId: m.sid, AfterEventId: cursor})
	if err != nil {
		m.t.Fatal(err)
	}
	defer st.Close()
	var out []*sessionv1.Event
	for {
		res, err := st.Receive()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			m.t.Fatalf("receive after %d events: %v", len(out), err)
		}
		out = append(out, res.GetEvent())
	}
}

// read subscribes after cursor and reads exactly n events, then closes the stream.
func (m *meeting) read(cursor uint32, n int) []*sessionv1.Event {
	m.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := m.reader.Session.Subscribe(ctx, &sessionv1.SubscribeRequest{SessionId: m.sid, AfterEventId: cursor})
	if err != nil {
		m.t.Fatal(err)
	}
	defer st.Close()
	out := make([]*sessionv1.Event, 0, n)
	for len(out) < n {
		res, err := st.Receive()
		if err != nil {
			m.t.Fatalf("receive event %d of %d: %v", len(out)+1, n, err)
		}
		out = append(out, res.GetEvent())
	}
	return out
}

func (m *meeting) end(pid string) *sessionv1.Event {
	m.t.Helper()
	res, err := m.s(pid).EndSession(m.ctx(), &sessionv1.EndSessionRequest{SessionId: m.sid,
		CommandId: ids.NewCommandID(), ParticipantId: pid})
	if err != nil {
		m.t.Fatalf("end session: %v", err)
	}
	return res.GetResult().GetEvent()
}

// checkLog checks the rules every log follows: IDs start at 1 and grow by 1, the type matches
// the payload, and the session ID is set.
func checkLog(t *testing.T, sid string, log []*sessionv1.Event) {
	t.Helper()
	for i, ev := range log {
		if ev.GetId() != uint32(i+1) {
			t.Fatalf("event %d has id %d, want %d", i, ev.GetId(), i+1)
		}
		if ev.GetType() == "" || ev.GetType() != events.TypeOf(ev) {
			t.Fatalf("event %d: type %q, payload type %q", ev.GetId(), ev.GetType(), events.TypeOf(ev))
		}
		if ev.GetSessionId() != sid || ev.GetTime() == nil {
			t.Fatalf("event %d: session %q, time %v", ev.GetId(), ev.GetSessionId(), ev.GetTime())
		}
	}
}

func wantCode(t *testing.T, what string, err error, code connect.Code) {
	t.Helper()
	if got := connect.CodeOf(err); err == nil || got != code {
		t.Errorf("%s: want code %v, got %v (%v)", what, code, got, err)
	}
}

// The participant ID in a session is the participant of the access token. A participant joins
// once: a repeated join, with the same or a new command_id, returns the first join.
func TestSessionJoinIsIdempotent(t *testing.T) {
	m := newMeeting(t)
	ada := m.register("Ada", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	req := &sessionv1.JoinRequest{SessionId: m.sid, CommandId: ids.NewCommandID(), DisplayName: "Ada L.",
		Kind: sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN, Captured: true}
	first, err := ada.Session.Join(m.ctx(), req)
	if err != nil {
		t.Fatal(err)
	}
	ev := first.GetResult().GetEvent()
	pj := ev.GetParticipantJoined()
	if first.GetResult().GetDuplicate() || first.GetParticipantId() != ada.ParticipantID || ev.GetId() != 1 ||
		ev.GetType() != events.ParticipantJoined || ev.GetCommandId() != req.GetCommandId() ||
		ev.GetActorParticipantId() != first.GetParticipantId() || pj.GetParticipantId() != first.GetParticipantId() ||
		pj.GetDisplayName() != "Ada L." || pj.GetKind() != req.GetKind() || !pj.GetCaptured() {
		t.Fatalf("first join: %v", first)
	}

	// A client that restarts and sends the same join again stays the same participant.
	again, err := ada.Session.Join(m.ctx(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !again.GetResult().GetDuplicate() || again.GetParticipantId() != first.GetParticipantId() ||
		!proto.Equal(again.GetResult().GetEvent(), ev) {
		t.Fatalf("repeated join: got %v, want the same participant and event as %v", again, first)
	}
	// A second client of the same participant (another key, a new command_id) gets the same join.
	second := m.srv.Participant(t, ada.ParticipantID, "", identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED)
	other, err := second.Session.Join(m.ctx(), &sessionv1.JoinRequest{SessionId: m.sid, CommandId: ids.NewCommandID()})
	if err != nil || !other.GetResult().GetDuplicate() || !proto.Equal(other.GetResult().GetEvent(), ev) {
		t.Fatalf("join of a second key: got %v, %v, want the first join with duplicate set", other, err)
	}

	// Without display_name and kind, the registered ones are used. A wrong kind is rejected.
	bot := m.register("Bot", sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT)
	_, err = bot.Session.Join(m.ctx(), &sessionv1.JoinRequest{SessionId: m.sid, CommandId: ids.NewCommandID(),
		Kind: sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN})
	wantCode(t, "join with a kind that is not the registered one", err, connect.CodePermissionDenied)
	res, err := bot.Session.Join(m.ctx(), &sessionv1.JoinRequest{SessionId: m.sid, CommandId: ids.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	if bj := res.GetResult().GetEvent().GetParticipantJoined(); res.GetParticipantId() != bot.ParticipantID ||
		bj.GetDisplayName() != "Bot" || bj.GetKind() != sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT {
		t.Fatalf("join without name and kind: %v", res)
	}
	m.end(bot.ParticipantID)
	log := m.readAll(0)
	checkLog(t, m.sid, log)
	if len(log) != 3 {
		t.Fatalf("log has %d events, want 3 (two joins, end)", len(log))
	}
}

// Every command creates exactly one event, and a repeated command_id creates none.
func TestSessionCommandsAreIdempotent(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	bot := m.join("Bot", sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT)
	said := m.say(ada, "Bot, what is the budget?")
	cmd := func() string { return ids.NewCommandID() }
	sid := m.sid

	type call struct {
		name string
		typ  string
		do   func(ctx context.Context) (*sessionv1.CommandResult, error)
	}
	// Each request is built once, so the second call repeats the same command_id.
	utt := &sessionv1.SubmitUtteranceRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada, Text: "Hello",
		Modality: sessionv1.Modality_MODALITY_VOICE}
	retract := &sessionv1.RetractUtteranceRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada,
		UtteranceEventId: said.GetId()}
	turn := &sessionv1.SubmitSignalRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada,
		Signal: &sessionv1.SubmitSignalRequest_TurnEnd{TurnEnd: &sessionv1.TurnEnd{ParticipantId: ada}}}
	silence := &sessionv1.SubmitSignalRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada,
		Signal: &sessionv1.SubmitSignalRequest_SilenceLong{SilenceLong: &sessionv1.SilenceLong{Duration: durationpb.New(6 * time.Second)}}}
	addressed := &sessionv1.SubmitSignalRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada,
		Signal: &sessionv1.SubmitSignalRequest_Addressed{Addressed: &sessionv1.Addressed{ParticipantId: bot, UtteranceEventId: said.GetId()}}}
	pause := &sessionv1.PauseRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada}
	resume := &sessionv1.ResumeRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada}
	hand := &sessionv1.RaiseHandRequest{SessionId: sid, CommandId: cmd(), ParticipantId: bot, Topic: "budget"}
	minutes := &sessionv1.UpdateMinutesRequest{SessionId: sid, CommandId: cmd(), ParticipantId: bot, Text: "- budget"}
	show := &sessionv1.ShowArtifactRequest{SessionId: sid, CommandId: cmd(), ParticipantId: bot, ArtifactId: "slides/42"}
	leave := &sessionv1.LeaveRequest{SessionId: sid, CommandId: cmd(), ParticipantId: bot}
	end := &sessionv1.EndSessionRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada}

	calls := []call{
		{"submit utterance", events.UtteranceFinal, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(utt.GetParticipantId()).SubmitUtterance(ctx, utt)
			return r.GetResult(), err
		}},
		{"retract utterance", events.UtteranceRetracted, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(retract.GetParticipantId()).RetractUtterance(ctx, retract)
			return r.GetResult(), err
		}},
		{"turn.end", events.TurnEnd, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(turn.GetParticipantId()).SubmitSignal(ctx, turn)
			return r.GetResult(), err
		}},
		{"silence.long", events.SilenceLong, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(silence.GetParticipantId()).SubmitSignal(ctx, silence)
			return r.GetResult(), err
		}},
		{"addressed", events.Addressed, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(addressed.GetParticipantId()).SubmitSignal(ctx, addressed)
			return r.GetResult(), err
		}},
		{"pause", events.ParticipantPaused, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(pause.GetParticipantId()).Pause(ctx, pause)
			return r.GetResult(), err
		}},
		{"resume", events.ParticipantResumed, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(resume.GetParticipantId()).Resume(ctx, resume)
			return r.GetResult(), err
		}},
		{"raise hand", events.HandRaised, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(hand.GetParticipantId()).RaiseHand(ctx, hand)
			return r.GetResult(), err
		}},
		{"update minutes", events.MinutesUpdated, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(minutes.GetParticipantId()).UpdateMinutes(ctx, minutes)
			return r.GetResult(), err
		}},
		{"show artifact", events.ArtifactShown, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(show.GetParticipantId()).ShowArtifact(ctx, show)
			return r.GetResult(), err
		}},
		{"leave", events.ParticipantLeft, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(leave.GetParticipantId()).Leave(ctx, leave)
			return r.GetResult(), err
		}},
		{"end session", events.SessionEnded, func(ctx context.Context) (*sessionv1.CommandResult, error) {
			r, err := m.s(end.GetParticipantId()).EndSession(ctx, end)
			return r.GetResult(), err
		}},
	}
	byID := map[uint32]*sessionv1.Event{}
	for _, c := range calls {
		first, err := c.do(m.ctx())
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		again, err := c.do(m.ctx())
		if err != nil {
			t.Fatalf("%s repeated: %v", c.name, err)
		}
		ev := first.GetEvent()
		if first.GetDuplicate() || ev.GetType() != c.typ || ev.GetCommandId() == "" {
			t.Fatalf("%s: got %v, want a new %s event", c.name, first, c.typ)
		}
		if !again.GetDuplicate() || !proto.Equal(again.GetEvent(), ev) {
			t.Fatalf("%s repeated: got %v, want the stored event %v with duplicate set", c.name, again, ev)
		}
		byID[ev.GetId()] = ev
	}

	log := m.readAll(0)
	checkLog(t, m.sid, log)
	// Two joins and one utterance before, then exactly one event per command.
	if len(log) != 3+len(calls) {
		t.Fatalf("log has %d events, want %d", len(log), 3+len(calls))
	}
	for id, ev := range byID {
		if !proto.Equal(log[id-1], ev) {
			t.Errorf("event %d in the stream differs from the command result:\n%v\n%v", id, log[id-1], ev)
		}
	}
	if log[len(log)-1].GetType() != events.SessionEnded {
		t.Fatalf("last event is %q, want session.ended", log[len(log)-1].GetType())
	}
}

func TestSessionRejectsInvalidCommands(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	bot := m.join("Bot", sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT)
	s, ctx, sid, cmd := m.s(ada), m.ctx(), m.sid, ids.NewCommandID

	_, err := s.Join(ctx, &sessionv1.JoinRequest{CommandId: cmd(), DisplayName: "X", Kind: sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN})
	wantCode(t, "join without session_id", err, connect.CodeInvalidArgument)
	_, err = s.Join(ctx, &sessionv1.JoinRequest{SessionId: sid, DisplayName: "X", Kind: sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN})
	wantCode(t, "join without command_id", err, connect.CodeInvalidArgument)

	_, err = s.SubmitUtterance(ctx, &sessionv1.SubmitUtteranceRequest{CommandId: cmd(), ParticipantId: ada, Text: "x", Modality: sessionv1.Modality_MODALITY_TEXT})
	wantCode(t, "utterance without session_id", err, connect.CodeInvalidArgument)
	_, err = s.SubmitUtterance(ctx, &sessionv1.SubmitUtteranceRequest{SessionId: sid, ParticipantId: ada, Text: "x", Modality: sessionv1.Modality_MODALITY_TEXT})
	wantCode(t, "utterance without command_id", err, connect.CodeInvalidArgument)
	_, err = s.SubmitUtterance(ctx, &sessionv1.SubmitUtteranceRequest{SessionId: sid, CommandId: cmd(), Text: "x", Modality: sessionv1.Modality_MODALITY_TEXT})
	wantCode(t, "utterance without participant_id", err, connect.CodeInvalidArgument)
	_, err = s.SubmitUtterance(ctx, &sessionv1.SubmitUtteranceRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada, Modality: sessionv1.Modality_MODALITY_TEXT})
	wantCode(t, "utterance without text", err, connect.CodeInvalidArgument)
	_, err = s.RaiseHand(ctx, &sessionv1.RaiseHandRequest{SessionId: sid, CommandId: cmd()})
	wantCode(t, "raise hand without participant_id", err, connect.CodeInvalidArgument)
	_, err = s.ShowArtifact(ctx, &sessionv1.ShowArtifactRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada})
	wantCode(t, "show artifact without artifact_id", err, connect.CodeInvalidArgument)
	_, err = s.SubmitSignal(ctx, &sessionv1.SubmitSignalRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada})
	wantCode(t, "signal without signal", err, connect.CodeInvalidArgument)
	if st, err := s.Subscribe(ctx, &sessionv1.SubscribeRequest{}); err != nil {
		wantCode(t, "subscribe without session_id", err, connect.CodeInvalidArgument)
	} else {
		_, err = st.Receive()
		st.Close()
		wantCode(t, "subscribe without session_id", err, connect.CodeInvalidArgument)
	}

	// participant_id must be the participant of the token.
	_, err = s.SubmitUtterance(ctx, m.utterance("nobody", "x"))
	wantCode(t, "utterance with the participant_id of nobody", err, connect.CodePermissionDenied)
	_, err = s.SubmitUtterance(ctx, m.utterance(bot, "x"))
	wantCode(t, "utterance with the participant_id of someone else", err, connect.CodePermissionDenied)
	_, err = s.EndSession(ctx, &sessionv1.EndSessionRequest{SessionId: sid, CommandId: cmd(), ParticipantId: bot})
	wantCode(t, "end with the participant_id of someone else", err, connect.CodePermissionDenied)
	// A registered participant who did not join is not a participant of the session.
	outsider := m.register("Outsider", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	_, err = outsider.Session.SubmitUtterance(ctx, m.utterance(outsider.ParticipantID, "x"))
	wantCode(t, "utterance of a participant who did not join", err, connect.CodeNotFound)
	_, err = s.SubmitUtterance(ctx, &sessionv1.SubmitUtteranceRequest{SessionId: "other-" + sid, CommandId: cmd(),
		ParticipantId: ada, Text: "x", Modality: sessionv1.Modality_MODALITY_TEXT})
	wantCode(t, "utterance in a session the participant did not join", err, connect.CodeNotFound)
	_, err = s.SubmitSignal(ctx, &sessionv1.SubmitSignalRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada,
		Signal: &sessionv1.SubmitSignalRequest_Addressed{Addressed: &sessionv1.Addressed{ParticipantId: bot, UtteranceEventId: 99}}})
	wantCode(t, "addressed with an unknown utterance", err, connect.CodeNotFound)
	_, err = s.SubmitSignal(ctx, &sessionv1.SubmitSignalRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada,
		Signal: &sessionv1.SubmitSignalRequest_TurnEnd{TurnEnd: &sessionv1.TurnEnd{ParticipantId: "nobody"}}})
	wantCode(t, "turn.end of an unknown participant", err, connect.CodeNotFound)

	_, err = s.Resume(ctx, &sessionv1.ResumeRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada})
	wantCode(t, "resume without pause", err, connect.CodeFailedPrecondition)

	// A command_id belongs to one command of one participant.
	used := m.utterance(ada, "first")
	if _, err := s.SubmitUtterance(ctx, used); err != nil {
		t.Fatal(err)
	}
	_, err = s.RaiseHand(ctx, &sessionv1.RaiseHandRequest{SessionId: sid, CommandId: used.GetCommandId(), ParticipantId: ada})
	wantCode(t, "command_id reused for another command", err, connect.CodeAlreadyExists)
	reused := proto.Clone(used).(*sessionv1.SubmitUtteranceRequest)
	reused.ParticipantId = bot
	_, err = m.s(bot).SubmitUtterance(ctx, reused)
	wantCode(t, "command_id reused by another participant", err, connect.CodeAlreadyExists)

	if _, err := m.s(bot).Leave(ctx, &sessionv1.LeaveRequest{SessionId: sid, CommandId: cmd(), ParticipantId: bot}); err != nil {
		t.Fatal(err)
	}
	_, err = m.s(bot).RaiseHand(ctx, &sessionv1.RaiseHandRequest{SessionId: sid, CommandId: cmd(), ParticipantId: bot})
	wantCode(t, "command of a participant who left", err, connect.CodeFailedPrecondition)

	// Rejected commands leave no trace in the log.
	m.end(ada)
	log := m.readAll(0)
	checkLog(t, m.sid, log)
	if len(log) != 5 {
		t.Fatalf("log has %d events, want 5 (two joins, utterance, leave, end): %v", len(log), log)
	}
}

// A retraction is a new event that points to the utterance. The utterance itself stays as it was.
func TestSessionRetractionIsANewEvent(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	bot := m.join("Bot", sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT)
	said := m.say(ada, "The budget is ten million.")
	s, ctx, sid := m.s(ada), m.ctx(), m.sid

	before := m.read(0, 3)
	_, err := m.s(bot).RetractUtterance(ctx, &sessionv1.RetractUtteranceRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: bot, UtteranceEventId: said.GetId()})
	wantCode(t, "retract someone else's utterance", err, connect.CodePermissionDenied)
	_, err = s.RetractUtterance(ctx, &sessionv1.RetractUtteranceRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: ada, UtteranceEventId: 1})
	wantCode(t, "retract an event that is not an utterance", err, connect.CodeNotFound)

	res, err := s.RetractUtterance(ctx, &sessionv1.RetractUtteranceRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: ada, UtteranceEventId: said.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	ev := res.GetResult().GetEvent()
	if ev.GetId() != said.GetId()+1 || ev.GetType() != events.UtteranceRetracted ||
		ev.GetUtteranceRetracted().GetUtteranceEventId() != said.GetId() || ev.GetActorParticipantId() != ada {
		t.Fatalf("retraction: %v", ev)
	}
	_, err = s.RetractUtterance(ctx, &sessionv1.RetractUtteranceRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: ada, UtteranceEventId: said.GetId()})
	wantCode(t, "retract twice", err, connect.CodeFailedPrecondition)

	m.end(ada)
	log := m.readAll(0)
	checkLog(t, m.sid, log)
	for i, ev := range before {
		if !proto.Equal(log[i], ev) {
			t.Fatalf("event %d changed after the retraction:\n%v\n%v", ev.GetId(), ev, log[i])
		}
	}
	if !proto.Equal(log[ev.GetId()-1], ev) {
		t.Fatalf("retraction in the log: %v", log[ev.GetId()-1])
	}
}

// A subscriber that reconnects with its last event ID gets every later event exactly once,
// also while events are written.
func TestSessionResumeFromCursor(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	const total = 400 // utterances

	written := make(chan error, 1)
	go func() {
		for i := range total {
			if _, err := m.s(ada).SubmitUtterance(context.Background(), m.utterance(ada, fmt.Sprint(i))); err != nil {
				written <- err
				return
			}
		}
		_, err := m.s(ada).EndSession(context.Background(), &sessionv1.EndSessionRequest{SessionId: m.sid,
			CommandId: ids.NewCommandID(), ParticipantId: ada})
		written <- err
	}()

	// Read in chunks of different sizes and reconnect after each chunk.
	var got []*sessionv1.Event
	var cursor uint32
	chunks := []int{1, 7, 50, 3, 120, 2}
	for i := 0; ; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		st, err := m.reader.Session.Subscribe(ctx, &sessionv1.SubscribeRequest{SessionId: m.sid, AfterEventId: cursor})
		if err != nil {
			t.Fatal(err)
		}
		n, ended := chunks[i%len(chunks)], false
		for range n {
			res, err := st.Receive()
			if errors.Is(err, io.EOF) {
				ended = true
				break
			}
			if err != nil {
				t.Fatalf("reconnect %d: %v", i, err)
			}
			got = append(got, res.GetEvent())
			cursor = res.GetEvent().GetId()
		}
		st.Close()
		cancel()
		if ended {
			break
		}
		if i > 10*total {
			t.Fatal("too many reconnects")
		}
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	checkLog(t, m.sid, got)
	if want := 1 + total + 1; len(got) != want {
		t.Fatalf("got %d events, want %d", len(got), want)
	}
	for i, ev := range got[1 : 1+total] {
		if ev.GetUtteranceFinal().GetText() != fmt.Sprint(i) {
			t.Fatalf("event %d: text %q, want %q", ev.GetId(), ev.GetUtteranceFinal().GetText(), fmt.Sprint(i))
		}
	}

	// A cursor beyond the last event is an error, not an endless wait.
	st, err := m.reader.Session.Subscribe(m.ctx(), &sessionv1.SubscribeRequest{SessionId: m.sid, AfterEventId: cursor + 5})
	if err == nil {
		defer st.Close()
		_, err = st.Receive()
	}
	wantCode(t, "cursor beyond the end", err, connect.CodeOutOfRange)
}

// After session.ended the stream closes and every new command fails.
func TestSessionEnded(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	said := m.utterance(ada, "Goodbye")
	if _, err := m.s(ada).SubmitUtterance(m.ctx(), said); err != nil {
		t.Fatal(err)
	}

	// A live subscriber waits at the end of the log.
	live, err := m.reader.Session.Subscribe(m.ctx(), &sessionv1.SubscribeRequest{SessionId: m.sid, AfterEventId: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()

	ended := m.end(ada)
	res, err := live.Receive()
	if err != nil || res.GetEvent().GetType() != events.SessionEnded || res.GetEvent().GetId() != ended.GetId() {
		t.Fatalf("live subscriber: got %v, %v, want session.ended", res, err)
	}
	if _, err := live.Receive(); !errors.Is(err, io.EOF) {
		t.Fatalf("live subscriber after session.ended: want end of stream, got %v", err)
	}

	s, ctx, sid, cmd := m.s(ada), m.ctx(), m.sid, ids.NewCommandID
	_, err = s.SubmitUtterance(ctx, m.utterance(ada, "too late"))
	wantCode(t, "utterance after the end", err, connect.CodeFailedPrecondition)
	late := m.register("Late", sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	_, err = late.Session.Join(ctx, &sessionv1.JoinRequest{SessionId: sid, CommandId: cmd()})
	wantCode(t, "join after the end", err, connect.CodeFailedPrecondition)
	_, err = s.EndSession(ctx, &sessionv1.EndSessionRequest{SessionId: sid, CommandId: cmd(), ParticipantId: ada})
	wantCode(t, "end twice", err, connect.CodeFailedPrecondition)

	// A command that was sent before the end can still be repeated, for example after a drop.
	again, err := s.SubmitUtterance(ctx, said)
	if err != nil || !again.GetResult().GetDuplicate() || again.GetResult().GetEvent().GetId() != 2 {
		t.Fatalf("repeated utterance after the end: %v, %v", again, err)
	}

	// New subscribers get the whole log, ending with session.ended, and then the stream closes.
	log := m.readAll(0)
	checkLog(t, m.sid, log)
	if len(log) != 3 || log[2].GetType() != events.SessionEnded {
		t.Fatalf("log: %v", log)
	}
	if rest := m.readAll(ended.GetId()); len(rest) != 0 {
		t.Fatalf("subscribe after session.ended: got %v, want an empty stream", rest)
	}
}

// Many subscribers and many writers at the same time all see the same log.
func TestSessionConcurrentSubscribers(t *testing.T) {
	m := newMeeting(t)
	const writers, perWriter, readers = 4, 50, 5
	pids := make([]string, writers)
	for i := range pids {
		pids[i] = m.join(fmt.Sprint("writer ", i), sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT)
	}

	logs := make([][]*sessionv1.Event, readers)
	var rwg sync.WaitGroup
	for r := range readers {
		rwg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			st, err := m.reader.Session.Subscribe(ctx, &sessionv1.SubscribeRequest{SessionId: m.sid})
			if err != nil {
				t.Error(err)
				return
			}
			defer st.Close()
			for {
				res, err := st.Receive()
				if errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					t.Errorf("reader %d: %v", r, err)
					return
				}
				logs[r] = append(logs[r], res.GetEvent())
			}
		})
	}

	var wwg sync.WaitGroup
	for w := range writers {
		wwg.Go(func() {
			for i := range perWriter {
				if _, err := m.s(pids[w]).SubmitUtterance(context.Background(), m.utterance(pids[w], fmt.Sprint(w, "/", i))); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wwg.Wait()
	m.end(pids[0])
	rwg.Wait()

	want := writers + writers*perWriter + 1
	for r, log := range logs {
		checkLog(t, m.sid, log)
		if len(log) != want {
			t.Fatalf("reader %d got %d events, want %d", r, len(log), want)
		}
		for i := range log {
			if !proto.Equal(log[i], logs[0][i]) {
				t.Fatalf("reader %d and reader 0 differ at event %d", r, i+1)
			}
		}
	}
}

// actorKind maps the kind of a session participant to the kind of the actor that identity
// registers. Each interface owns its own kind type.
func actorKind(k sessionv1.ParticipantKind) identityv1.ActorKind {
	switch k {
	case sessionv1.ParticipantKind_PARTICIPANT_KIND_HUMAN:
		return identityv1.ActorKind_ACTOR_KIND_HUMAN
	case sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT:
		return identityv1.ActorKind_ACTOR_KIND_AGENT
	}
	return identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED
}

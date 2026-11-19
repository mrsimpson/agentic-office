package contract

// Contract tests of the session activity (if-session-activity, dec-agent-activity) through its
// Connect clients against the real office-hub.

import (
	"errors"
	"io"
	"net/url"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"

	sessionactivityv1 "github.com/mrsimpson/agentic-office/hub/session/activityapi"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
)

const (
	working = sessionactivityv1.ActivityState_ACTIVITY_STATE_WORKING
	idle    = sessionactivityv1.ActivityState_ACTIVITY_STATE_IDLE
)

// report reports an activity of pid and returns the error.
func (m *meeting) report(pid string, state sessionactivityv1.ActivityState, tool string, unspoken bool) error {
	m.t.Helper()
	_, err := m.actors[pid].Activity.ReportActivity(m.ctx(), &sessionactivityv1.ReportActivityRequest{SessionId: m.sid,
		ParticipantId: pid, State: state, Tool: tool, UnspokenReply: unspoken})
	return err
}

// activityWatch is an open WatchActivity stream.
type activityWatch struct {
	t    *testing.T
	next chan []*sessionactivityv1.Activity
	errc chan error
}

// wait waits until the stream sends a list for which ok is true and returns it.
func (w *activityWatch) wait(what string, ok func([]*sessionactivityv1.Activity) bool) []*sessionactivityv1.Activity {
	w.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case as := <-w.next:
			if ok(as) {
				return as
			}
		case err := <-w.errc:
			// Lists that came before the end still count.
			for {
				select {
				case as := <-w.next:
					if ok(as) {
						w.errc <- err
						return as
					}
					continue
				default:
				}
				break
			}
			w.t.Fatalf("the activity stream ended before %s: %v", what, err)
		case <-deadline:
			w.t.Fatalf("no activities with %s within 10 s", what)
		}
	}
}

// TestSessionActivity: a participant reports its own activity; the watchers of the session get
// the activities at once and after every change. The activity of a participant ends when it
// leaves, all activities end when the session ends, and the watch then closes. The activity
// never reaches the log.
func TestSessionActivity(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	bot := m.join("Bot", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT)
	carl := m.join("Carl", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT)

	st, err := m.actors[ada].Activity.WatchActivity(m.ctx(), &sessionactivityv1.WatchActivityRequest{SessionId: m.sid})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := &activityWatch{t: t, next: make(chan []*sessionactivityv1.Activity, 16), errc: make(chan error, 1)}
	go func() {
		for {
			res, err := st.Receive()
			if err != nil {
				w.errc <- err
				return
			}
			w.next <- res.GetActivities()
		}
	}()
	w.wait("an empty list at once", func(as []*sessionactivityv1.Activity) bool { return len(as) == 0 })

	res, err := m.actors[bot].Activity.ReportActivity(m.ctx(), &sessionactivityv1.ReportActivityRequest{SessionId: m.sid,
		ParticipantId: bot, State: working, Tool: "fetch"})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetTtl().AsDuration() <= 0 || res.GetActivity().GetTool() != "fetch" || res.GetActivity().GetSince() == nil {
		t.Fatalf("report: %v", res)
	}
	w.wait("bot working with fetch", func(as []*sessionactivityv1.Activity) bool {
		return len(as) == 1 && as[0].GetParticipantId() == bot && as[0].GetState() == working && as[0].GetTool() == "fetch"
	})

	// A new report replaces the old one; the list is ordered by participant.
	if err := m.report(carl, working, "minutes_update", false); err != nil {
		t.Fatal(err)
	}
	if err := m.report(bot, idle, "", true); err != nil {
		t.Fatal(err)
	}
	w.wait("bot idle with an unspoken reply, then carl", func(as []*sessionactivityv1.Activity) bool {
		return len(as) == 2 && as[0].GetParticipantId() == bot && as[0].GetState() == idle && as[0].GetUnspokenReply() &&
			as[1].GetParticipantId() == carl && as[1].GetTool() == "minutes_update"
	})

	// Leaving ends the activity of that participant.
	if _, err := m.s(carl).Leave(m.ctx(), &sessioncommandsv1.LeaveRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: carl}); err != nil {
		t.Fatal(err)
	}
	w.wait("only bot after carl left", func(as []*sessionactivityv1.Activity) bool {
		return len(as) == 1 && as[0].GetParticipantId() == bot
	})
	if code := connect.CodeOf(m.report(carl, working, "", false)); code != connect.CodeFailedPrecondition {
		t.Errorf("a participant that left reports: %v, want failed_precondition", code)
	}

	// The activity is not in the log: three joins and one leave, nothing else.
	for _, ev := range m.read(0, 4) {
		if ev.GetType() != "participant.joined" && ev.GetType() != "participant.left" {
			t.Errorf("unexpected event in the log: %v", ev)
		}
	}

	// The end of the session ends all activities and the watch.
	m.end(ada)
	w.wait("no activities after the end", func(as []*sessionactivityv1.Activity) bool { return len(as) == 0 })
	select {
	case err := <-w.errc:
		if !errors.Is(err, io.EOF) {
			t.Errorf("the activity stream ended with %v, want the end of the stream", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the activity stream did not close after session.ended")
	}
	if code := connect.CodeOf(m.report(bot, working, "", false)); code != connect.CodeFailedPrecondition {
		t.Errorf("a report after the end: %v, want failed_precondition", code)
	}
}

// TestSessionActivityRules: only the participant itself reports, only after it joined, never with
// a stage link, and a tool is a short name, not text. A stage link may watch its own session only.
func TestSessionActivityRules(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	bot := m.join("Bot", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT)
	stranger := m.register("Stranger", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT).ParticipantID

	cases := []struct {
		name string
		as   string
		req  *sessionactivityv1.ReportActivityRequest
		want connect.Code
	}{
		{"for another participant", ada, &sessionactivityv1.ReportActivityRequest{SessionId: m.sid, ParticipantId: bot, State: working}, connect.CodePermissionDenied},
		{"not joined", stranger, &sessionactivityv1.ReportActivityRequest{SessionId: m.sid, ParticipantId: stranger, State: working}, connect.CodeFailedPrecondition},
		{"no state", bot, &sessionactivityv1.ReportActivityRequest{SessionId: m.sid, ParticipantId: bot}, connect.CodeInvalidArgument},
		{"a title as tool", bot, &sessionactivityv1.ReportActivityRequest{SessionId: m.sid, ParticipantId: bot, State: working, Tool: "Fetch https://example.org/?q=secret"}, connect.CodeInvalidArgument},
		{"a tool while idle", bot, &sessionactivityv1.ReportActivityRequest{SessionId: m.sid, ParticipantId: bot, State: idle, Tool: "fetch"}, connect.CodeInvalidArgument},
		{"unspoken while working", bot, &sessionactivityv1.ReportActivityRequest{SessionId: m.sid, ParticipantId: bot, State: working, UnspokenReply: true}, connect.CodeInvalidArgument},
		{"no session", bot, &sessionactivityv1.ReportActivityRequest{ParticipantId: bot, State: working}, connect.CodeInvalidArgument},
	}
	for _, c := range cases {
		_, err := m.actors[c.as].Activity.ReportActivity(m.ctx(), c.req)
		if code := connect.CodeOf(err); code != c.want {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}

	// A stage link watches its own session, but cannot report or watch another one.
	link, err := url.Parse(stageLink(t, m.actors[ada], m.sid))
	if err != nil {
		t.Fatal(err)
	}
	stage := m.srv.WithToken(t, link.Query().Get("token"))
	if _, err := stage.Activity.ReportActivity(m.ctx(), &sessionactivityv1.ReportActivityRequest{SessionId: m.sid,
		ParticipantId: ada, State: working}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a stage link reports: %v, want permission_denied", err)
	}
	if err := m.report(bot, working, "search", false); err != nil {
		t.Fatal(err)
	}
	st, err := stage.Activity.WatchActivity(m.ctx(), &sessionactivityv1.WatchActivityRequest{SessionId: m.sid})
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.Receive()
	st.Close()
	if err != nil {
		t.Fatalf("a stage link watches its session: %v", err)
	}
	if as := res.GetActivities(); len(as) != 1 || as[0].GetParticipantId() != bot || as[0].GetTool() != "search" {
		t.Errorf("the stage link sees %v", as)
	}
	other, err := stage.Activity.WatchActivity(m.ctx(), &sessionactivityv1.WatchActivityRequest{SessionId: m.sid + "-other"})
	if err == nil {
		_, err = other.Receive()
		other.Close()
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a stage link watches another session: %v, want permission_denied", err)
	}
}

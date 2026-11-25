package contract

// Contract tests of the trim and the delete of an ended session (dec-session-trim-delete) through
// if-session-commands, if-session-list and if-session-stream, against the real office-hub.

import (
	"testing"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionlistv1 "github.com/mrsimpson/agentic-office/hub/session/listapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

func (m *meeting) trimBy(pid string, last uint32) (*sessioncommandsv1.TrimSessionResponse, error) {
	return m.s(pid).TrimSession(m.ctx(), &sessioncommandsv1.TrimSessionRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: pid, LastEventId: last})
}

func (m *meeting) deleteBy(pid string) (*sessioncommandsv1.DeleteSessionResponse, error) {
	return m.s(pid).DeleteSession(m.ctx(), &sessioncommandsv1.DeleteSessionRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: pid})
}

func (m *meeting) restoreBy(pid string) (*sessioncommandsv1.RestoreSessionResponse, error) {
	return m.s(pid).RestoreSession(m.ctx(), &sessioncommandsv1.RestoreSessionRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: pid})
}

// listed returns the summary of the session in the list of a participant, or nil.
func (m *meeting) listed(a *contracttest.Actor, includeDeleted bool) *sessionlistv1.Session {
	m.t.Helper()
	res, err := a.Sessions.ListSessions(ctx10(m.t), &sessionlistv1.ListSessionsRequest{IncludeDeleted: includeDeleted})
	if err != nil {
		m.t.Fatalf("ListSessions: %v", err)
	}
	for _, s := range res.GetSessions() {
		if s.GetSessionId() == m.sid {
			return s
		}
	}
	return nil
}

func TestTrimAndDeleteTakeOnlyAnEndedSession(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", kHuman)
	first := m.say(ada, "We start.")
	_, err := m.trimBy(ada, first.GetId())
	wantCode(t, "trim of a running session", err, connect.CodeFailedPrecondition)
	_, err = m.deleteBy(ada)
	wantCode(t, "delete of a running session", err, connect.CodeFailedPrecondition)
	m.end(ada)
	if _, err := m.trimBy(ada, first.GetId()); err != nil {
		t.Fatalf("trim of an ended session: %v", err)
	}
}

func TestTrimIsAnEventAndCanBeUndone(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", kHuman)
	first := m.say(ada, "The meeting is cancelled.")
	m.say(ada, "More that does not count.")
	m.end(ada)
	res, err := m.trimBy(ada, first.GetId())
	if err != nil {
		t.Fatal(err)
	}
	ev := res.GetResult().GetEvent()
	if ev.GetType() != events.SessionTrimmed || ev.GetSessionTrimmed().GetLastEventId() != first.GetId() || ev.GetActorParticipantId() != ada {
		t.Fatalf("trim event %v", ev)
	}
	if s := m.listed(m.actors[ada], false); s == nil || s.GetTrimmedAfterEventId() != first.GetId() {
		t.Fatalf("the list shows %v, want a session trimmed after event %d", s, first.GetId())
	}
	// The log keeps every event.
	if n := len(m.logOf(ada)); n < 5 {
		t.Errorf("the log has %d events, want every event and the trim", n)
	}
	_, err = m.trimBy(ada, 0)
	wantCode(t, "trim at event 0", err, connect.CodeInvalidArgument)
	_, err = m.trimBy(ada, 9999)
	wantCode(t, "trim beyond the log", err, connect.CodeInvalidArgument)
	// A trim at the end of the session removes the cut.
	var endID uint32
	for _, e := range m.logOf(ada) {
		if e.GetType() == events.SessionEnded {
			endID = e.GetId()
		}
	}
	if _, err := m.trimBy(ada, endID); err != nil {
		t.Fatal(err)
	}
	if s := m.listed(m.actors[ada], false); s == nil || s.GetTrimmedAfterEventId() != 0 {
		t.Fatalf("the list shows %v, want no cut", s)
	}
}

func TestDeleteHidesAndRestoreShowsTheSession(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", kHuman)
	m.say(ada, "Short meeting.")
	m.end(ada)
	if _, err := m.deleteBy(ada); err != nil {
		t.Fatal(err)
	}
	_, err := m.deleteBy(ada)
	wantCode(t, "second delete", err, connect.CodeFailedPrecondition)
	if m.listed(m.actors[ada], false) != nil {
		t.Fatal("a deleted session is in the list")
	}
	if s := m.listed(m.actors[ada], true); s == nil || !s.GetDeleted() {
		t.Fatalf("the list with deleted sessions shows %v, want a deleted session", s)
	}
	if _, err := m.restoreBy(ada); err != nil {
		t.Fatal(err)
	}
	_, err = m.restoreBy(ada)
	wantCode(t, "restore of a session that is not deleted", err, connect.CodeFailedPrecondition)
	if s := m.listed(m.actors[ada], false); s == nil || s.GetDeleted() {
		t.Fatalf("the list shows %v, want the restored session", s)
	}
}

func TestTrimAndDeleteAreForHumansWhoTookPartAndTenantAdmins(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	first := m.say(ada, "Hello.")
	m.end(ada)
	_, err := m.trimBy(bot, first.GetId())
	wantCode(t, "trim by an agent", err, connect.CodePermissionDenied)
	_, err = m.deleteBy(bot)
	wantCode(t, "delete by an agent", err, connect.CodePermissionDenied)
	// A human who took no part does not know the session.
	eve := m.register("Eve", kHuman)
	_, err = m.trimBy(eve.ParticipantID, first.GetId())
	wantCode(t, "trim by a stranger", err, connect.CodeNotFound)
	// A tenant admin may.
	boss := m.register("Boss", kHuman)
	ctx := m.ctx()
	if _, err := m.srv.Admin(t).User.SetMembership(ctx, &userv1.SetMembershipRequest{CommandId: coreapi.NewCommandID(),
		TenantId: contracttest.DefaultTenant, UserId: boss.ParticipantID, Role: userv1.Role_ROLE_TENANT_ADMIN}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.deleteBy(boss.ParticipantID); err != nil {
		t.Fatalf("delete by a tenant admin: %v", err)
	}
}

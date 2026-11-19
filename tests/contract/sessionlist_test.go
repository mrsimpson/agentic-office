package contract

// Contract tests of the session list (if-session-list, dec-web-portal) through its Connect client
// against the real office-hub.

import (
	"net/url"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessionactivityv1 "github.com/mrsimpson/agentic-office/hub/session/activityapi"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionimportv1 "github.com/mrsimpson/agentic-office/hub/session/importapi"
	sessionlistv1 "github.com/mrsimpson/agentic-office/hub/session/listapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// listSessions returns the IDs of the sessions that a sees, in the order of the list, and the list.
func listSessions(t *testing.T, a *contracttest.Actor) ([]string, map[string]*sessionlistv1.Session) {
	t.Helper()
	res, err := a.Sessions.ListSessions(ctx10(t), &sessionlistv1.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions as %s: %v", a.ParticipantID, err)
	}
	var ids []string
	byID := map[string]*sessionlistv1.Session{}
	for _, s := range res.GetSessions() {
		ids = append(ids, s.GetSessionId())
		byID[s.GetSessionId()] = s
	}
	return ids, byID
}

func joinAs(t *testing.T, a *contracttest.Actor, sid string) {
	t.Helper()
	if _, err := a.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: sid, CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatalf("join %s as %s: %v", sid, a.ParticipantID, err)
	}
}

// TestSessionList: a participant sees the sessions it joined or imported, the newest first, with
// start, end, participants and the mark of an import; a tenant admin sees every session of the
// tenant; a stage link sees none. Ended sessions that only the archive holds after a restart stay
// in the list.
func TestSessionList(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human) // tenant admin
	bob := srv.Participant(t, "bob", "Bob", human)
	carl := srv.Participant(t, "carl", "Carl", agent)
	weekly, review, imported := "weekly-"+coreapi.NewCommandID(), "review-"+coreapi.NewCommandID(), "imp-"+coreapi.NewCommandID()

	joinAs(t, bob, weekly)
	joinAs(t, carl, weekly)
	if _, err := bob.Commands.EndSession(ctx10(t), &sessioncommandsv1.EndSessionRequest{SessionId: weekly, CommandId: coreapi.NewCommandID(),
		ParticipantId: "bob"}); err != nil {
		t.Fatal(err)
	}
	joinAs(t, carl, review)
	if _, err := bob.Import.ImportConversation(ctx10(t), &sessionimportv1.ImportConversationRequest{SessionId: imported,
		CommandId: coreapi.NewCommandID(), ParticipantId: "bob", Conversation: conversation(), Participants: people(),
		Utterances: []*sessionimportv1.ImportedUtterance{utt("ada", "Hello.")}, Complete: true}); err != nil {
		t.Fatal(err)
	}

	check := func(phase string) {
		t.Helper()
		ids, byID := listSessions(t, bob)
		if strings.Join(ids, " ") != imported+" "+weekly {
			t.Errorf("%s: bob sees %v, want the import, then weekly", phase, ids)
		}
		w := byID[weekly]
		if w == nil || w.GetEndedAt() == nil || w.GetStartedAt() == nil || w.GetImported() || len(w.GetParticipants()) != 2 ||
			w.GetParticipants()[0].GetParticipantId() != "bob" || w.GetParticipants()[1].GetDisplayName() != "Carl" ||
			w.GetParticipants()[1].GetKind() != pAgent {
			t.Errorf("%s: weekly: %v", phase, w)
		}
		if im := byID[imported]; im == nil || !im.GetImported() || len(im.GetParticipants()) == 0 || !im.GetParticipants()[0].GetImported() {
			t.Errorf("%s: the import: %v", phase, im)
		}
		if ids, byID := listSessions(t, carl); strings.Join(ids, " ") != review+" "+weekly || byID[review].GetEndedAt() != nil {
			t.Errorf("%s: carl sees %v, want review (running), then weekly", phase, ids)
		}
		if ids, _ := listSessions(t, ada); strings.Join(ids, " ") != imported+" "+review+" "+weekly {
			t.Errorf("%s: the tenant admin sees %v, want all three", phase, ids)
		}
	}
	check("live")

	// A stage link sees no list.
	link, err := ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: weekly, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link.GetUrl())
	_, err = srv.WithToken(t, u.Query().Get("token")).Sessions.ListSessions(ctx10(t), &sessionlistv1.ListSessionsRequest{})
	wantCode(t, "ListSessions with a stage link", err, connect.CodePermissionDenied)

	// After a restart the ended session lies in the archive only; it stays in the list.
	if code, err := srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop the hub: exit %d, %v", code, err)
	}
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	ada, bob, carl = srv.WithKey(t, ada.Key), srv.WithKey(t, bob.Key), srv.WithKey(t, carl.Key)
	check("after a restart")
}

// TestSessionStreamRights: everyone who ever joined a session, the participant who imported it,
// the tenant admins and a stage link of the session read it through the session stream; for
// every other member of the tenant the session does not exist. The same holds for the tree
// heads and the activities, for a running session and for an ended one that only the archive
// holds.
func TestSessionStreamRights(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human) // tenant admin
	bob := srv.Participant(t, "bob", "Bob", human)
	carl := srv.Participant(t, "carl", "Carl", agent)
	dora := srv.Participant(t, "dora", "Dora", human) // a member who takes part in nothing
	sid, imported := "weekly-"+coreapi.NewCommandID(), "imp-"+coreapi.NewCommandID()
	joinAs(t, bob, sid)
	joinAs(t, carl, sid)
	if _, err := carl.Commands.Leave(ctx10(t), &sessioncommandsv1.LeaveRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: "carl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := dora.Import.ImportConversation(ctx10(t), &sessionimportv1.ImportConversationRequest{SessionId: imported,
		CommandId: coreapi.NewCommandID(), ParticipantId: "dora", Conversation: conversation(), Participants: people(),
		Utterances: []*sessionimportv1.ImportedUtterance{utt("ada", "Hello.")}, Complete: true}); err != nil {
		t.Fatal(err)
	}
	link, err := ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(link.GetUrl())
	stageToken := u.Query().Get("token")

	reads := func(a *contracttest.Actor, s string) error {
		t.Helper()
		_, err := a.Stream.GetEvents(ctx10(t), &sessionstreamv1.GetEventsRequest{SessionId: s})
		if _, err2 := a.Commands.GetTreeHead(ctx10(t), &sessioncommandsv1.GetTreeHeadRequest{SessionId: s}); connect.CodeOf(err2) != connect.CodeOf(err) {
			t.Errorf("%s on %s: GetEvents %v, GetTreeHead %v", a.ParticipantID, s, err, err2)
		}
		st, err3 := a.Stream.Subscribe(ctx10(t), &sessionstreamv1.SubscribeRequest{SessionId: s})
		if err3 == nil {
			_, err3 = st.Receive()
			st.Close()
		}
		if connect.CodeOf(err3) != connect.CodeOf(err) {
			t.Errorf("%s on %s: GetEvents %v, Subscribe %v", a.ParticipantID, s, err, err3)
		}
		return err
	}
	check := func(phase string) {
		t.Helper()
		for _, c := range []struct {
			who  *contracttest.Actor
			s    string
			want connect.Code
		}{
			{bob, sid, 0}, {carl, sid, 0}, {ada, sid, 0}, {dora, sid, connect.CodeNotFound},
			{dora, imported, 0}, {bob, imported, connect.CodeNotFound}, {ada, imported, 0},
		} {
			if got := codeOrOK(reads(c.who, c.s)); got != c.want {
				t.Errorf("%s: %s reads %s: %v, want %v", phase, c.who.ParticipantID, c.s, got, c.want)
			}
		}
		st0, err := srv.WithToken(t, stageToken).Stream.Subscribe(ctx10(t), &sessionstreamv1.SubscribeRequest{SessionId: sid})
		if err == nil {
			_, err = st0.Receive()
			st0.Close()
		}
		if err != nil {
			t.Errorf("%s: the stage link reads its session: %v", phase, err)
		}
		st, err := dora.Activity.WatchActivity(ctx10(t), &sessionactivityv1.WatchActivityRequest{SessionId: sid})
		if err == nil {
			_, err = st.Receive()
			st.Close()
		}
		wantCode(t, phase+": dora watches the activities", err, connect.CodeNotFound)
	}
	check("running")

	// Ended, and after a restart only in the archive.
	if _, err := bob.Commands.EndSession(ctx10(t), &sessioncommandsv1.EndSessionRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: "bob"}); err != nil {
		t.Fatal(err)
	}
	if code, err := srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop the hub: exit %d, %v", code, err)
	}
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	ada, bob, carl, dora = srv.WithKey(t, ada.Key), srv.WithKey(t, bob.Key), srv.WithKey(t, carl.Key), srv.WithKey(t, dora.Key)
	link, err = ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(link.GetUrl())
	stageToken = u.Query().Get("token")
	check("ended, after a restart")
}

// codeOrOK is the code of an error, or 0 for no error (connect.CodeOf(nil) is unknown).
func codeOrOK(err error) connect.Code {
	if err == nil {
		return 0
	}
	return connect.CodeOf(err)
}

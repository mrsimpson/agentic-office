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
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionimportv1 "github.com/mrsimpson/agentic-office/hub/session/importapi"
	sessionlistv1 "github.com/mrsimpson/agentic-office/hub/session/listapi"
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

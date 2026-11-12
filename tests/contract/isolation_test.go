package contract

// Contract tests of the separation of tenants (concept-multi-tenancy): the meeting session, the
// records, the tasks, the slides and the stage link belong to one tenant. Every call names its
// tenant in the header Office-Tenant (auth.TenantHeader); the membership is checked before.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// twoTenants is a hub with the tenants acme (ada, cy) and globex (bob, cy). Cy is a member of both.
type twoTenants struct {
	srv                 *contracttest.Hub
	ada, bob            *contracttest.Actor // in their tenant
	cyAcme, cyGlobex    *contracttest.Actor // cy, naming one tenant
	adaKey, bobKey, cyK *contracttest.Actor // users with no tenant named
}

func newTwoTenants(t *testing.T, args ...string) *twoTenants {
	t.Helper()
	srv := contracttest.StartHub(t, nil, args...)
	return setUpTwoTenants(t, srv)
}

func setUpTwoTenants(t *testing.T, srv *contracttest.Hub) *twoTenants {
	t.Helper()
	admin := srv.Admin(t)
	adaU := srv.User(t, "ada", "Ada", human)
	bobU := srv.User(t, "bob", "Bob", human)
	cyU := srv.User(t, "cy", "Cy", human)
	if err := createTenant(t, admin, "acme", "ada"); err != nil {
		t.Fatal(err)
	}
	if err := createTenant(t, admin, "globex", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := setMember(t, admin, "acme", "cy", roleMember); err != nil {
		t.Fatal(err)
	}
	if err := setMember(t, admin, "globex", "cy", roleMember); err != nil {
		t.Fatal(err)
	}
	return &twoTenants{srv: srv, adaKey: adaU, bobKey: bobU, cyK: cyU,
		ada: srv.WithTenant(t, adaU.Key, "acme"), bob: srv.WithTenant(t, bobU.Key, "globex"),
		cyAcme: srv.WithTenant(t, cyU.Key, "acme"), cyGlobex: srv.WithTenant(t, cyU.Key, "globex")}
}

func join2(t *testing.T, a *contracttest.Actor, sid string) {
	t.Helper()
	if _, err := a.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: sid, CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatalf("join %s in %s as %s: %v", sid, a.TenantID, a.ParticipantID, err)
	}
}

func eventsOf(t *testing.T, a *contracttest.Actor, sid string) []*sessionstreamv1.Event {
	t.Helper()
	res, err := a.Stream.GetEvents(ctx10(t), &sessionstreamv1.GetEventsRequest{SessionId: sid, Limit: 1000})
	if err != nil {
		t.Fatalf("events of %s in %s: %v", sid, a.TenantID, err)
	}
	return res.GetEvents()
}

// The same session ID in two tenants is two sessions. Decisions, notes, tasks and decks of one
// tenant are not visible in the other, also for a user who is a member of both.
func TestTenantsAreSeparated(t *testing.T) {
	m := newTwoTenants(t)

	join2(t, m.ada, "weekly")
	join2(t, m.bob, "weekly")
	if _, err := m.cyGlobex.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: "weekly", CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatal(err)
	}
	// Acme has Ada's join only; Globex has Bob's and Cy's. Cy is not in the session of Acme.
	if evs := eventsOf(t, m.ada, "weekly"); len(evs) != 1 || evs[0].GetParticipantJoined().GetDisplayName() != "Ada" {
		t.Fatalf("session of acme: %v", evs)
	}
	if evs := eventsOf(t, m.bob, "weekly"); len(evs) != 2 {
		t.Fatalf("session of globex: %v", evs)
	}
	if evs := eventsOf(t, m.cyAcme, "weekly"); len(evs) != 1 {
		t.Fatalf("session of acme, read by Cy: %v", evs)
	}

	recordDecision(t, m.ada, "Acme ships.", "weekly")
	addNote(t, m.ada, "Acme note.", "weekly")
	createTask(t, m.ada, &taskv1.CreateTaskRequest{Title: "Acme task", SessionId: "weekly"})
	acmeDeck := render(t, m.ada, &slidesv1.RenderDeckRequest{Title: "Acme deck", Slides: []*slidesv1.Slide{{Title: "Acme"}}})
	if got := listDecisions(t, m.bob, &decisionv1.ListDecisionsRequest{}); len(got) != 0 {
		t.Fatalf("decisions of globex: %v", got)
	}
	if res, err := m.cyGlobex.Note.ListNotes(ctx10(t), &notev1.ListNotesRequest{}); err != nil || len(res.GetNotes()) != 0 {
		t.Fatalf("notes of globex read by Cy: %v, %v", res, err)
	}
	if res, err := m.bob.Task.ListTasks(ctx10(t), &taskv1.ListTasksRequest{}); err != nil || len(res.GetTasks()) != 0 {
		t.Fatalf("tasks of globex: %v, %v", res, err)
	}
	// The deck IDs count in each tenant: slides/1 is another deck in Globex.
	globexDeck := render(t, m.bob, &slidesv1.RenderDeckRequest{Title: "Globex deck", Slides: []*slidesv1.Slide{{Title: "Globex"}}})
	if acmeDeck.GetId() != "slides/1" || globexDeck.GetId() != "slides/1" {
		t.Fatalf("deck IDs: %s and %s, want slides/1 in both", acmeDeck.GetId(), globexDeck.GetId())
	}
	if res, err := m.cyAcme.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "slides/1"}); err != nil || res.GetDeck().GetTitle() != "Acme deck" {
		t.Fatalf("slides/1 of acme: %v, %v", res, err)
	}
	if res, err := m.cyGlobex.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "slides/1"}); err != nil || res.GetDeck().GetTitle() != "Globex deck" {
		t.Fatalf("slides/1 of globex: %v, %v", res, err)
	}
	// Cy sees each tenant's own records.
	if got := listDecisions(t, m.cyAcme, &decisionv1.ListDecisionsRequest{}); len(got) != 1 {
		t.Fatalf("decisions of acme read by Cy: %v", got)
	}
}

// A call names a tenant, and the caller must be a member. Anyone else gets the answer for a tenant
// that does not exist: a user of another tenant, a system admin, and a name nobody has.
func TestTenantAccess(t *testing.T) {
	m := newTwoTenants(t)
	join := func(a *contracttest.Actor) error {
		_, err := a.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: "s", CommandId: coreapi.NewCommandID()})
		return err
	}
	as := func(u *contracttest.Actor, tenant string) *contracttest.Actor {
		return m.srv.WithTenant(t, u.Key, tenant)
	}

	wantCode(t, "bob names acme", join(as(m.bobKey, "acme")), connect.CodeNotFound)
	wantCode(t, "the system admin names acme", join(as(m.srv.Admin(t), "acme")), connect.CodeNotFound)
	wantCode(t, "a tenant nobody has", join(as(m.bobKey, "nowhere")), connect.CodeNotFound)
	wantCode(t, "no tenant named", join(m.bobKey), connect.CodeInvalidArgument)
	wantCode(t, "bob reads decisions of acme", func() error {
		_, err := as(m.bobKey, "acme").Decision.ListDecisions(ctx10(t), &decisionv1.ListDecisionsRequest{})
		return err
	}(), connect.CodeNotFound)
	wantCode(t, "bob creates a stage link for acme", func() error {
		_, err := as(m.bobKey, "acme").Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: coreapi.NewCommandID()})
		return err
	}(), connect.CodeNotFound)
	// The answers for a tenant that exists and one that does not are the same.
	_, errExists := as(m.bobKey, "acme").Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: "s", CommandId: "x"})
	_, errMissing := as(m.bobKey, "nowhere").Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: "s", CommandId: "x"})
	if connect.CodeOf(errExists) != connect.CodeOf(errMissing) || errExists.Error() != errMissing.Error() {
		t.Fatalf("answers differ: %v and %v", errExists, errMissing)
	}
	// The user API and the tenant API need no tenant.
	if _, err := m.bobKey.User.ListMemberships(ctx10(t), &userv1.ListMembershipsRequest{TenantId: "globex"}); err != nil {
		t.Fatalf("user API without a tenant: %v", err)
	}

	// A member who is removed can no longer name the tenant, and tokens stay valid for the other tenant.
	if err := removeMember(t, m.srv.Admin(t), "globex", "cy"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "cy names globex after the removal", join(m.cyGlobex), connect.CodeNotFound)
	if err := join(m.cyAcme); err != nil {
		t.Fatalf("cy in acme after the removal from globex: %v", err)
	}
}

// A stage link belongs to the tenant of the user who creates it. It shows the session of this
// tenant, and it ends when the tenant is removed.
func TestStageLinkBelongsToTheTenant(t *testing.T) {
	m := newTwoTenants(t)
	join2(t, m.ada, "weekly")
	join2(t, m.bob, "weekly")
	m.srv.Participant(t, "viewer", "Viewer", human)
	link, err := m.ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: "weekly", CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	stage := m.srv.WithToken(t, stageToken(t, link.GetUrl()))
	st, err := stage.Stream.Subscribe(ctx10(t), &sessionstreamv1.SubscribeRequest{SessionId: "weekly"})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	first, err := st.Receive()
	if err != nil || first.GetEvent().GetParticipantJoined().GetDisplayName() != "Ada" {
		t.Fatalf("first event of the stage of acme: %v, %v", first, err)
	}

	// The page of the stage shows the tenant's session. The link is no key to the other tenant.
	res, err := m.srv.HTTPClient(t).Get(link.GetUrl())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("stage page: status %d", res.StatusCode)
	}

	if err := removeTenant(t, m.srv.Admin(t), "acme"); err != nil {
		t.Fatal(err)
	}
	_, err = stage.Stream.GetEvents(ctx10(t), &sessionstreamv1.GetEventsRequest{SessionId: "weekly"})
	wantCode(t, "stage link of a removed tenant", err, connect.CodeUnauthenticated)
}

// Each tenant keeps its content in its own repository, and a restart restores it.
func TestTenantContentLivesInTheTenantRepository(t *testing.T) {
	m := newTwoTenants(t, "-archive-interval", "100ms")
	join2(t, m.ada, "weekly")
	join2(t, m.bob, "weekly")
	recordDecision(t, m.ada, "Acme ships.", "weekly")
	recordDecision(t, m.bob, "Globex waits.", "weekly")
	createTask(t, m.bob, &taskv1.CreateTaskRequest{Title: "Globex task"})
	render(t, m.ada, &slidesv1.RenderDeckRequest{Title: "Acme deck", Slides: []*slidesv1.Slide{{Title: "Acme"}}})
	contracttest.WaitArchived(t, m.srv.TenantRepoDir("acme"), "weekly", 20*time.Second, func(a *contracttest.ArchivedSession) bool { return len(a.Events) == 1 })
	contracttest.WaitArchived(t, m.srv.TenantRepoDir("globex"), "weekly", 20*time.Second, func(a *contracttest.ArchivedSession) bool { return len(a.Events) == 1 })

	read := func(tenant string) string {
		dir := m.srv.TenantRepoDir(tenant)
		var all strings.Builder
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && !strings.Contains(p, "/.git/") {
				b, _ := os.ReadFile(p)
				all.Write(b)
			}
			return nil
		})
		return all.String()
	}
	acme, globex := read("acme"), read("globex")
	if !strings.Contains(acme, "Acme ships.") || strings.Contains(acme, "Globex") || strings.Contains(acme, "bob") {
		t.Errorf("repository of acme:\n%.600s", acme)
	}
	if !strings.Contains(globex, "Globex waits.") || strings.Contains(globex, "Acme") || strings.Contains(globex, "ada") {
		t.Errorf("repository of globex:\n%.600s", globex)
	}

	if code, err := m.srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	m2 := setUpAgain(t, m)
	if evs := eventsOf(t, m2.ada, "weekly"); len(evs) != 1 || evs[0].GetParticipantJoined().GetDisplayName() != "Ada" {
		t.Fatalf("session of acme after the restart: %v", evs)
	}
	if evs := eventsOf(t, m2.bob, "weekly"); len(evs) != 1 || evs[0].GetParticipantJoined().GetDisplayName() != "Bob" {
		t.Fatalf("session of globex after the restart: %v", evs)
	}
	if got := listDecisions(t, m2.bob, &decisionv1.ListDecisionsRequest{}); len(got) != 1 || got[0].GetText() != "Globex waits." {
		t.Fatalf("decisions of globex after the restart: %v", got)
	}
}

// setUpAgain starts the hub again on the data of m and returns actors with the same keys.
func setUpAgain(t *testing.T, m *twoTenants) *twoTenants {
	t.Helper()
	srv := contracttest.StartHubIn(t, m.srv.DataDir, m.srv.AdminKey, nil, "-archive-interval", "100ms")
	return &twoTenants{srv: srv, ada: srv.WithTenant(t, m.ada.Key, "acme"), bob: srv.WithTenant(t, m.bob.Key, "globex"),
		cyAcme: srv.WithTenant(t, m.cyAcme.Key, "acme"), cyGlobex: srv.WithTenant(t, m.cyGlobex.Key, "globex")}
}

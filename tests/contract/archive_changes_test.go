package contract

// Contract tests of the changes interface of the archive (if-artifact-changes,
// dec-artifact-sync). They start the real office-hub, follow a tenant through Connect and change
// the archive through the documents interface. The path of a change by push and a notice is
// tested in hub/archive (changes_contract_test.go), because the notice is an in-process call.
// The tests wait on channels with generous deadlines and never check how long something takes.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	changesv1 "github.com/mrsimpson/agentic-office/hub/archive/changes/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// follower reads the events of one Follow call in the background.
type changeFollower struct {
	events chan *changesv1.FollowResponse
	errs   chan error
}

// follow opens a call, waits for the first message (the call is registered) and returns a
// follower of the following events. A failed call fails the test.
func followChanges(t *testing.T, c contracttest.Clients, prefixes ...string) *changeFollower {
	t.Helper()
	f, err := tryFollow(t, c, prefixes...)
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	return f
}

// tryFollow is follow that returns the error of the call.
func tryFollow(t *testing.T, c contracttest.Clients, prefixes ...string) (*changeFollower, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := c.Changes.Follow(ctx, &changesv1.FollowRequest{PathPrefixes: prefixes})
	if err != nil {
		return nil, err
	}
	f := &changeFollower{events: make(chan *changesv1.FollowResponse, 1000), errs: make(chan error, 1)}
	first := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			res, err := st.Receive()
			if err != nil {
				f.errs <- err
				if i == 0 {
					close(first)
				}
				return
			}
			if i == 0 {
				close(first)
				continue
			}
			f.events <- res
		}
	}()
	select {
	case <-first:
	case <-time.After(20 * time.Second):
		t.Fatal("the follower got no first message")
	}
	select {
	case err := <-f.errs:
		return nil, err
	default:
	}
	return f, nil
}

// next returns the next event.
func (f *changeFollower) next(t *testing.T) *changesv1.FollowResponse {
	t.Helper()
	select {
	case ev := <-f.events:
		return ev
	case err := <-f.errs:
		t.Fatalf("the stream ended: %v", err)
	case <-time.After(20 * time.Second):
		t.Fatal("no event")
	}
	return nil
}

// described returns the changes of an event as "kind path version" strings.
func described(ev *changesv1.FollowResponse) []string {
	var out []string
	for _, c := range ev.GetChanges() {
		out = append(out, fmt.Sprintf("%s %s %s", c.GetKind(), c.GetPath(), c.GetVersion()))
	}
	return out
}

func newDoc(t *testing.T, a *contracttest.Actor, path, content string) string {
	t.Helper()
	res, err := a.Document.CreateDocument(ctx10(t), &documentv1.CreateDocumentRequest{Path: path, Content: content, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	return res.GetDocument().GetVersion()
}

// A commit of the archive gives one event with the new version, the same version as the read
// interface. A change gives kind changed. Every follower gets every event.
func TestArtifactChangesOfArchiveCommits(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	cy := srv.Participant(t, "cy", "Cy", human)
	f1 := followChanges(t, ada.Clients)
	f2 := followChanges(t, cy.Clients)

	v1 := newDoc(t, ada, "documents/plan.md", "# Plan\n")
	want := fmt.Sprintf("%s documents/plan.md %s", changesv1.ChangeKind_CHANGE_KIND_CREATED, v1)
	for i, f := range []*changeFollower{f1, f2} {
		if got := described(f.next(t)); len(got) != 1 || got[0] != want {
			t.Errorf("follower %d: %v, want %s", i+1, got, want)
		}
	}
	if read := readDoc(t, ada, "documents/plan.md").GetVersion(); read != v1 {
		t.Errorf("version of the event %q, of the read interface %q", v1, read)
	}

	res, err := ada.Document.ChangeDocument(ctx10(t), &documentv1.ChangeDocumentRequest{Path: "documents/plan.md", Content: "# Plan 2\n",
		BaseVersion: v1, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	want = fmt.Sprintf("%s documents/plan.md %s", changesv1.ChangeKind_CHANGE_KIND_CHANGED, res.GetDocument().GetVersion())
	for i, f := range []*changeFollower{f1, f2} {
		if got := described(f.next(t)); len(got) != 1 || got[0] != want {
			t.Errorf("follower %d: %v, want %s", i+1, got, want)
		}
	}
}

// A path prefix limits the events to the artifacts below it. The prefix "documents/a" does not
// match "documents/ab".
func TestArtifactChangesPathPrefix(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	filtered := followChanges(t, ada.Clients, "documents/a")
	all := followChanges(t, ada.Clients)

	newDoc(t, ada, "documents/ab/x.md", "x")
	newDoc(t, ada, "documents/b/y.md", "y")
	v := newDoc(t, ada, "documents/a/z.md", "z")
	for _, p := range []string{"documents/ab/x.md", "documents/b/y.md", "documents/a/z.md"} {
		if got := all.next(t).GetChanges(); len(got) != 1 || got[0].GetPath() != p {
			t.Fatalf("follower of all: %v, want %s", got, p)
		}
	}
	want := fmt.Sprintf("%s documents/a/z.md %s", changesv1.ChangeKind_CHANGE_KIND_CREATED, v)
	if got := described(filtered.next(t)); len(got) != 1 || got[0] != want {
		t.Errorf("filtered follower: %v, want only %s", got, want)
	}

	_, err := tryFollow(t, ada.Clients, "../etc")
	wantCode(t, "invalid prefix", err, connect.CodeInvalidArgument)
}

// Only Markdown and YAML files are named, and never a session log. A change that touches only
// other files gives no event.
func TestArtifactChangesNameOnlyArtifacts(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-archive-interval", "1h")
	ada := srv.Participant(t, "ada", "Ada", human)
	f := followChanges(t, ada.Clients)

	// The session log is committed when the session ends; it is under sessions/.
	sid := "team/1"
	join(t, ada.Clients, sid)
	if _, err := ada.Commands.EndSession(ctx10(t), &sessioncommandsv1.EndSessionRequest{SessionId: sid, CommandId: coreapi.NewCommandID(), ParticipantId: ada.ParticipantID}); err != nil {
		t.Fatal(err)
	}
	contracttest.WaitArchived(t, srv.ArchiveDir(), sid, 20*time.Second, (*contracttest.ArchivedSession).Ended)
	// A document that is not Markdown or YAML is refused by the documents interface, so the
	// last event proves what came before: only the document, never the session log.
	v := newDoc(t, ada, "documents/last.md", "last")
	want := fmt.Sprintf("%s documents/last.md %s", changesv1.ChangeKind_CHANGE_KIND_CREATED, v)
	ev := f.next(t)
	if got := described(ev); len(got) != 1 || got[0] != want {
		t.Errorf("event after the session log: %v, want only %s", got, want)
	}
}

// An outsider, a caller of another tenant and a stage link get nothing; a caller without a token
// is not known. The events of another tenant do not reach a follower.
func TestArtifactChangesAreForMembers(t *testing.T) {
	tt := newTwoTenants(t)
	acme := followChanges(t, tt.ada.Clients)
	globex := followChanges(t, tt.bob.Clients)

	// Bob is no member of acme: the same answer as for a tenant that does not exist.
	bobInAcme := tt.srv.WithTenant(t, tt.bobKey.Key, "acme")
	_, err := tryFollow(t, bobInAcme.Clients)
	wantCode(t, "outsider", err, connect.CodeNotFound)
	_, err = tryFollow(t, tt.ada.Clients)
	if err != nil {
		t.Fatal(err)
	}
	ghost := tt.srv.WithTenant(t, tt.adaKey.Key, "nowhere")
	_, err = tryFollow(t, ghost.Clients)
	wantCode(t, "tenant that does not exist", err, connect.CodeNotFound)
	_, err = tryFollow(t, tt.srv.Anonymous(t))
	wantCode(t, "no token", err, connect.CodeUnauthenticated)

	sid := "team/1"
	join2(t, tt.ada, sid)
	link, err := tt.ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	stage := tt.srv.WithToken(t, stageToken(t, link.GetUrl()))
	_, err = tryFollow(t, stage)
	wantCode(t, "stage link", err, connect.CodePermissionDenied)

	// Each tenant hears only its own changes.
	va := newDoc(t, tt.ada, "documents/a.md", "a")
	vb := newDoc(t, tt.bob, "documents/b.md", "b")
	if got := described(acme.next(t)); len(got) != 1 || got[0] != fmt.Sprintf("%s documents/a.md %s", changesv1.ChangeKind_CHANGE_KIND_CREATED, va) {
		t.Errorf("acme: %v", got)
	}
	if got := described(globex.next(t)); len(got) != 1 || got[0] != fmt.Sprintf("%s documents/b.md %s", changesv1.ChangeKind_CHANGE_KIND_CREATED, vb) {
		t.Errorf("globex: %v", got)
	}
}

// A follower that never reads must not hold up the commits, and it does not hold up another
// follower.
func TestArtifactChangesSlowFollowerDoesNotBlock(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stalled, err := ada.Changes.Follow(ctx, &changesv1.FollowRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	if _, err := stalled.Receive(); err != nil { // registered; now it never reads again
		t.Fatal(err)
	}
	reader := followChanges(t, ada.Clients)

	const n = 60
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			newDoc(t, ada, fmt.Sprintf("documents/d%02d.md", i), "x")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Minute):
		t.Fatal("the commits are held up by a follower that does not read")
	}
	for i := 0; i < n; i++ {
		reader.next(t)
	}
	// The events of the stalled follower wait in the buffer of the hub or of the connection. The
	// archive drops a follower that cannot keep up; that case is tested in hub/archive, where
	// the stream blocks at once.
}

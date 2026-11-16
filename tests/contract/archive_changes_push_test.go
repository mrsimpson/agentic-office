package contract

// Contract tests of the changes interface of the archive (if-artifact-changes) for changes that
// come by a real "git push" to the real office-hub (if-git-access). Git access tells the archive
// that main moved, and the archive reports the change to its followers. The tests use the real
// git tool and the Connect client of the changes service. They wait on channels with generous
// deadlines and never check how long something takes. The cases that need the in-process notice
// with a timing that a push cannot make are in hub/archive (changes_notice_test.go).

import (
	"context"
	"fmt"
	"strings"
	"testing"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	changesv1 "github.com/mrsimpson/agentic-office/hub/archive/changes/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// versionIn returns the version (the Git object ID) of a file in the last commit of the clone.
func versionIn(t *testing.T, p *pusher, file string) string {
	t.Helper()
	return strings.TrimSpace(contracttest.Git(t, p.dir, "rev-parse", "HEAD:"+file))
}

func change(kind changesv1.ChangeKind, path, version string) string {
	return fmt.Sprintf("%s %s %s", kind, path, version)
}

func wantEvent(t *testing.T, what string, ev *changesv1.FollowResponse, want ...string) {
	t.Helper()
	if got := described(ev); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// A push gives the same kind of event as a commit of the archive: created, changed and removed
// artifacts with the new version, the same for every follower. Several files in one push are one
// event, ordered by path.
func TestArtifactChangesOfPushes(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	cy := srv.Participant(t, "cy", "Cy", human)
	f1, f2 := followChanges(t, ada.Clients), followChanges(t, cy.Clients)
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
	both := func(what string, want ...string) {
		t.Helper()
		wantEvent(t, what+", follower 1", f1.next(t), want...)
		wantEvent(t, what+", follower 2", f2.next(t), want...)
	}

	v := newDoc(t, ada, "documents/one.md", "one")
	both("commit", change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "documents/one.md", v))

	p.mustGit("pull", "-q", "origin", "main")
	p.commitFile("documents/two.md", "two")
	p.mustPush()
	v2 := versionIn(t, p, "documents/two.md")
	both("push, created", change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "documents/two.md", v2))
	if read := readDoc(t, ada, "documents/two.md").GetVersion(); read != v2 {
		t.Errorf("version of the event %q, of the read interface %q", v2, read)
	}

	p.commitFile("documents/two.md", "two, changed")
	p.mustPush()
	both("push, changed", change(changesv1.ChangeKind_CHANGE_KIND_CHANGED, "documents/two.md", versionIn(t, p, "documents/two.md")))

	p.mustGit("rm", "-q", "documents/one.md")
	p.mustGit("commit", "-q", "-m", "remove one")
	p.mustPush()
	both("push, removed", change(changesv1.ChangeKind_CHANGE_KIND_REMOVED, "documents/one.md", ""))

	p.write("z.md", "z")
	p.write("notes/000003.yaml", noteFile)
	p.write("m/n.md", "n")
	p.write("a.md", "a")
	p.commit("several files")
	p.mustPush()
	both("push, several files",
		change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "a.md", versionIn(t, p, "a.md")),
		change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "m/n.md", versionIn(t, p, "m/n.md")),
		change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "notes/000003.yaml", versionIn(t, p, "notes/000003.yaml")),
		change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "z.md", versionIn(t, p, "z.md")))
}

// A push that changes no artifact gives no event: the next event is the next real change. The
// same holds for a push of two commits whose final state equals the state before.
func TestArtifactChangesNoEventWithoutChange(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	f := followChanges(t, ada.Clients)
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)

	p.commitFile("docs/a.md", "a")
	p.mustPush()
	wantEvent(t, "first push", f.next(t), change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "docs/a.md", versionIn(t, p, "docs/a.md")))

	before := mainOf(t, srv, contracttest.DefaultTenant)
	p.mustGit("commit", "-q", "--allow-empty", "-m", "nothing changes")
	p.mustPush()
	p.commitFile("docs/b.md", "b")
	p.mustGit("rm", "-q", "docs/b.md")
	p.mustGit("commit", "-q", "-m", "undo")
	p.mustPush()
	if mainOf(t, srv, contracttest.DefaultTenant) == before {
		t.Fatal("main did not move")
	}

	v := newDoc(t, ada, "documents/next.md", "next")
	wantEvent(t, "after the pushes without a change", f.next(t), change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "documents/next.md", v))
}

// A change by push and a commit of the archive that follow each other are both reported, in the
// order in which main moved.
func TestArtifactChangesPushThenCommit(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	f := followChanges(t, ada.Clients)
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)

	p.commitFile("docs/a.md", "a")
	p.mustPush()
	v := newDoc(t, ada, "documents/b.md", "b")
	wantEvent(t, "push", f.next(t), change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "docs/a.md", versionIn(t, p, "docs/a.md")))
	wantEvent(t, "commit", f.next(t), change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "documents/b.md", v))
}

// A follower with path prefixes gets only the artifacts below them from a push; an event with
// none of them is not sent to it. The prefix "notes" does not match "notes-old".
func TestArtifactChangesPushPathPrefix(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	all, some := followChanges(t, ada.Clients), followChanges(t, ada.Clients, "notes", "plan.md")
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)

	p.write("notes-old/x.md", "x")
	p.write("notes/y.md", "y")
	p.write("plan.md", "p")
	p.write("other/z.md", "z")
	p.commit("four files")
	p.mustPush()
	c := func(path string) string {
		return change(changesv1.ChangeKind_CHANGE_KIND_CREATED, path, versionIn(t, p, path))
	}
	wantEvent(t, "all", all.next(t), c("notes-old/x.md"), c("notes/y.md"), c("other/z.md"), c("plan.md"))
	wantEvent(t, "prefixes", some.next(t), c("notes/y.md"), c("plan.md"))

	p.commitFile("other/w.md", "w")
	p.mustPush()
	wantEvent(t, "all, second push", all.next(t), c("other/w.md"))
	p.commitFile("notes/v.md", "v")
	p.mustPush()
	wantEvent(t, "all, third push", all.next(t), c("notes/v.md"))
	wantEvent(t, "prefixes, next event", some.next(t), c("notes/v.md"))
}

// A follower that has gone is removed and does not disturb a push or a commit.
func TestArtifactChangesGoneFollowerDoesNotDisturb(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ctx, cancel := context.WithCancel(context.Background())
	st, err := ada.Changes.Follow(ctx, &changesv1.FollowRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Receive(); err != nil {
		t.Fatal(err)
	}
	cancel()
	other := followChanges(t, ada.Clients)
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)

	p.commitFile("docs/a.md", "a")
	p.mustPush()
	wantEvent(t, "push", other.next(t), change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "docs/a.md", versionIn(t, p, "docs/a.md")))
	res, err := ada.Document.CreateDocument(ctx10(t), &documentv1.CreateDocumentRequest{Path: "documents/b.md", Content: "b", CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	wantEvent(t, "commit", other.next(t), change(changesv1.ChangeKind_CHANGE_KIND_CREATED, "documents/b.md", res.GetDocument().GetVersion()))
}

package archive_test

// Contract tests of the two in-process and stream interfaces of the archive that report changes of
// main: if-archive-repository (the notice "main moved") and if-artifact-changes. They need the
// notice, which is an in-process call that a test in another process cannot make, so they live
// here and use only the interfaces: repository.Repositories, storage.Archive, and the Connect
// client of the changes service. A change that is not the archive's own is made with the real git
// command line tool. The tests of the same service through a real hub are in
// tests/contract/archive_changes_test.go. The tests wait on channels with generous deadlines and
// never check how long something takes.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"

	"github.com/mrsimpson/agentic-office/hub/archive"
	changesv1 "github.com/mrsimpson/agentic-office/hub/archive/changes/api"
	"github.com/mrsimpson/agentic-office/hub/archive/changes/api/changesv1connect"
	"github.com/mrsimpson/agentic-office/hub/archive/repository"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
)

type fixture struct {
	repos  repository.Repositories
	arch   storage.Archive
	client changesv1connect.ArchiveChangesServiceClient
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	r, err := archive.OpenRepos(ctx, t.TempDir(), archive.Options{Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CreateTenant(ctx, "acme"); err != nil {
		t.Fatal(err)
	}
	arch, err := r.Tenant("acme")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := r.Changes("acme")
	if err != nil {
		t.Fatal(err)
	}
	// The caller is a participant, as the identity interceptor of the hub would put it.
	asParticipant := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return next(tokencheck.WithCaller(ctx, tokencheck.Caller{ParticipantID: "ada", TenantID: "acme"}), spec, stream)
		}
	}
	cs := connect.NewServer(asParticipant)
	changesv1connect.RegisterArchiveChangesServiceHandler(cs, svc)
	return &fixture{repos: r, arch: arch, client: changesv1connect.NewArchiveChangesServiceClient(connect.NewClient(connectinprocess.New(cs)))}
}

type follower struct {
	events chan *changesv1.FollowResponse
	errs   chan error
}

// follow waits for the first message and returns a follower of the events after it.
func (f *fixture) follow(t *testing.T, prefixes ...string) *follower {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, err := f.client.Follow(ctx, &changesv1.FollowRequest{PathPrefixes: prefixes})
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.Receive()
	if err != nil {
		t.Fatalf("first message: %v", err)
	}
	if first.GetRegistered() == nil || first.GetChanges() != nil {
		t.Fatalf("the first message is %v, want the registered signal", first)
	}
	fl := &follower{events: make(chan *changesv1.FollowResponse, 1000), errs: make(chan error, 1)}
	go func() {
		for {
			res, err := st.Receive()
			if err != nil {
				fl.errs <- err
				return
			}
			fl.events <- res
		}
	}()
	return fl
}

func (f *follower) next(t *testing.T) []string {
	t.Helper()
	select {
	case ev := <-f.events:
		var out []string
		for _, c := range ev.GetChanges().GetChanges() {
			out = append(out, fmt.Sprintf("%s %s %s", strings.TrimPrefix(c.GetKind().String(), "CHANGE_KIND_"), c.GetPath(), c.GetVersion()))
		}
		return out
	case err := <-f.errs:
		t.Fatalf("the stream ended: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("no event")
	}
	return nil
}

// git runs the real git command line tool.
func git(t *testing.T, dir string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=editor", "-c", "user.email=e@example.org"}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// push makes a change with git in a clone of the repository of the tenant and pushes it to main.
// files maps a path to its content; "" removes the file. It returns the object ID of each file.
func (f *fixture) push(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	dir, err := f.repos.Dir("acme")
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "work")
	git(t, filepath.Dir(work), "", "clone", "-q", dir, work)
	ids := map[string]string{}
	for p, content := range files {
		full := filepath.Join(work, filepath.FromSlash(p))
		if content == "" {
			if err := os.Remove(full); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		ids[p] = strings.TrimSpace(git(t, work, "", "hash-object", p))
	}
	git(t, work, "", "add", "-A")
	git(t, work, "", "commit", "-q", "-m", "change by git")
	git(t, work, "", "push", "-q", "origin", "HEAD:main")
	return ids
}

func (f *fixture) commit(t *testing.T, files ...storage.File) {
	t.Helper()
	if _, err := f.arch.Commit(context.Background(), storage.Change{Message: "change by the archive", Files: files}); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) moved(t *testing.T) {
	t.Helper()
	if err := f.repos.MainMoved(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
}

func equal(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// A commit of the archive and a change by git with the notice give the same kind of event:
// created, changed and removed artifacts with the new version, the same for every follower.
func TestSameEventForCommitAndPush(t *testing.T) {
	f := newFixture(t)
	a, b := f.follow(t), f.follow(t)
	next := func(want ...string) {
		t.Helper()
		equal(t, "follower 1", a.next(t), want...)
		equal(t, "follower 2", b.next(t), want...)
	}

	f.commit(t, storage.File{Path: "notes/one.md", Data: []byte("one")})
	one := strings.TrimSpace(git(t, mustDir(t, f), "", "rev-parse", "main:notes/one.md"))
	next("CREATED notes/one.md " + one)

	ids := f.push(t, map[string]string{"notes/two.yaml": "two: 2\n"})
	f.moved(t)
	next("CREATED notes/two.yaml " + ids["notes/two.yaml"])

	// Changed by the archive and by git.
	f.commit(t, storage.File{Path: "notes/one.md", Data: []byte("one, changed")})
	one = strings.TrimSpace(git(t, mustDir(t, f), "", "rev-parse", "main:notes/one.md"))
	next("CHANGED notes/one.md " + one)
	ids = f.push(t, map[string]string{"notes/two.yaml": "two: 22\n"})
	f.moved(t)
	next("CHANGED notes/two.yaml " + ids["notes/two.yaml"])

	// Removed by git, and several files in one change, ordered by path.
	f.push(t, map[string]string{"notes/one.md": ""})
	f.moved(t)
	next("REMOVED notes/one.md ")
	ids = f.push(t, map[string]string{"z.md": "z", "a.yml": "a: 1\n", "m/n.md": "n"})
	f.moved(t)
	next("CREATED a.yml "+ids["a.yml"], "CREATED m/n.md "+ids["m/n.md"], "CREATED z.md "+ids["z.md"])
}

// Only Markdown and YAML files are named, and never a hidden path. A change without such a file gives no event.
func TestChangesNameOnlyArtifacts(t *testing.T) {
	f := newFixture(t)
	fl := f.follow(t)
	ids := f.push(t, map[string]string{
		"notes/a.txt":  "text",
		"notes/b.html": "<p>",
		".github/c.md": "hidden",
		"notes/d.md":   "d",
	})
	f.moved(t)
	equal(t, "event", fl.next(t), "CREATED notes/d.md "+ids["notes/d.md"])

	// Changes that touch no artifact give no event: the next event is the next real change.
	f.push(t, map[string]string{"notes/a.txt": "text 2", ".github/f.md": "more"})
	f.moved(t)
	f.commit(t, storage.File{Path: "notes/g.txt", Data: []byte("g")})
	f.commit(t, storage.File{Path: "notes/e.md", Data: []byte("e")})
	e := strings.TrimSpace(git(t, mustDir(t, f), "", "rev-parse", "main:notes/e.md"))
	equal(t, "event after changes without an artifact", fl.next(t), "CREATED notes/e.md "+e)
}

// A follower with prefixes gets only the artifacts below them; an event with none of them is not
// sent to it.
func TestChangesPathPrefix(t *testing.T) {
	f := newFixture(t)
	all, notes := f.follow(t), f.follow(t, "notes", "plan.md")
	ids := f.push(t, map[string]string{"notes-old/x.md": "x", "notes/y.md": "y", "plan.md": "p", "other/z.md": "z"})
	f.moved(t)
	equal(t, "all", all.next(t), "CREATED notes-old/x.md "+ids["notes-old/x.md"], "CREATED notes/y.md "+ids["notes/y.md"],
		"CREATED other/z.md "+ids["other/z.md"], "CREATED plan.md "+ids["plan.md"])
	equal(t, "prefixes", notes.next(t), "CREATED notes/y.md "+ids["notes/y.md"], "CREATED plan.md "+ids["plan.md"])
	f.commit(t, storage.File{Path: "other/w.md", Data: []byte("w")})
	f.commit(t, storage.File{Path: "notes/v.md", Data: []byte("v")})
	if got := notes.next(t); len(got) != 1 || !strings.Contains(got[0], "notes/v.md") {
		t.Errorf("prefixes: %q, want only notes/v.md", got)
	}
}

// A change by git whose notice has not come yet is not lost: it is reported before the next
// change, and a notice for a state that is known gives no second event.
func TestChangesNoticeIsNotNeededForOrderAndNotRepeated(t *testing.T) {
	f := newFixture(t)
	fl := f.follow(t)
	ids := f.push(t, map[string]string{"a.md": "a"})
	f.commit(t, storage.File{Path: "b.md", Data: []byte("b")})
	b := strings.TrimSpace(git(t, mustDir(t, f), "", "rev-parse", "main:b.md"))
	equal(t, "first", fl.next(t), "CREATED a.md "+ids["a.md"])
	equal(t, "second", fl.next(t), "CREATED b.md "+b)
	f.moved(t) // main did not move since
	f.moved(t)
	f.commit(t, storage.File{Path: "c.md", Data: []byte("c")})
	c := strings.TrimSpace(git(t, mustDir(t, f), "", "rev-parse", "main:c.md"))
	equal(t, "third", fl.next(t), "CREATED c.md "+c)
}

// A follower that does not read is dropped, never waited for: the commits go on, another follower
// gets every event, and the stalled stream ends with resource_exhausted after what it had.
func TestSlowFollowerIsDroppedAndNeverBlocks(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stalled, err := f.client.Follow(ctx, &changesv1.FollowRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if first, err := stalled.Receive(); err != nil || first.GetRegistered() == nil {
		t.Fatalf("first message: %v, %v", first, err)
	}
	reader := f.follow(t)

	const n = 100
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			f.commit(t, storage.File{Path: fmt.Sprintf("notes/%03d.md", i), Data: []byte("x")})
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
	got := 0
	for {
		_, err := stalled.Receive()
		if err != nil {
			if connect.CodeOf(err) != connect.CodeResourceExhausted {
				t.Fatalf("the dropped follower ended with %v, want resource_exhausted", err)
			}
			break
		}
		got++
	}
	if got >= n {
		t.Errorf("the stalled follower got all %d events; it should have been dropped", got)
	}
}

// A follower that has gone is removed and does not disturb a commit.
func TestGoneFollowerDoesNotDisturb(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	st, err := f.client.Follow(ctx, &changesv1.FollowRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Receive(); err != nil {
		t.Fatal(err)
	}
	cancel()
	other := f.follow(t)
	f.commit(t, storage.File{Path: "a.md", Data: []byte("a")})
	other.next(t)
}

// Git access asks for the directory of a tenant, and a tenant without a repository is refused.
func TestRepositoryDirAndUnknownTenant(t *testing.T) {
	f := newFixture(t)
	dir := mustDir(t, f)
	if out := strings.TrimSpace(git(t, dir, "", "rev-parse", "--is-bare-repository")); out != "true" {
		t.Errorf("the repository is not bare: %q", out)
	}
	if _, err := f.repos.Dir("globex"); err != repository.ErrNoTenant {
		t.Errorf("Dir of an unknown tenant: %v", err)
	}
	if err := f.repos.MainMoved(context.Background(), "globex"); err != repository.ErrNoTenant {
		t.Errorf("MainMoved of an unknown tenant: %v", err)
	}
}

func mustDir(t *testing.T, f *fixture) string {
	t.Helper()
	d, err := f.repos.Dir("acme")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

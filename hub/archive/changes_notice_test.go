package archive_test

// Contract tests of the in-process interface if-archive-repository (the notice "main moved") and
// of the changes stream (if-artifact-changes) for the cases that a real hub cannot show from
// outside. A real "git push" sends the notice right after Git accepted the push, so a test in
// tests/contract cannot make the late notice, the repeated notice, a notice for an unknown
// tenant or a stream that blocks at once. Everything else about the changes of the archive is
// tested through the real hub in tests/contract (archive_changes_test.go,
// archive_changes_push_test.go). These tests use only the interfaces: repository.Repositories,
// storage.Archive, the Connect client of the changes service and the real git tool. They wait
// on channels with generous deadlines and never check how long something takes.

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

// A tenant without a repository is refused. A real hub never asks for one, because Git access
// checks the tenant first, so only a call in the same process can reach this case.
func TestRepositoriesUnknownTenant(t *testing.T) {
	f := newFixture(t)
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

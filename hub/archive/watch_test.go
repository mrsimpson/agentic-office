package archive_test

// Contract tests of the in-process interface if-archive-watch. They lie in the provider tree
// because the interface is an in-process Go interface: a real hub shows it only through the parts
// that use it (tests/contract/artifact_reload_test.go). Here a test can be the watcher itself and
// stop reading, which no part does. The tests use only the interfaces watch.Watcher,
// storage.Archive and repository.Repositories, and they never check how long something takes: a
// slow watcher is shown by commits that all succeed and by a watcher that is dropped.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/hub/archive"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/archive/watch"
)

func newWatchFixture(t *testing.T) (watch.Watcher, storage.Archive) {
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
	w, err := r.Watcher("acme")
	if err != nil {
		t.Fatal(err)
	}
	return w, arch
}

func commitFile(t *testing.T, arch storage.Archive, path, content string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := arch.Commit(ctx, storage.Change{Message: "change " + path, Files: []storage.File{{Path: path, Data: []byte(content)}}}); err != nil {
		t.Fatalf("commit %s: %v", path, err)
	}
}

func nextChanges(t *testing.T, s watch.Subscription) []watch.Change {
	t.Helper()
	select {
	case c, ok := <-s.Changes():
		if !ok {
			t.Fatalf("the subscription closed: %v", s.Err())
		}
		return c
	case <-time.After(2 * time.Minute):
		t.Fatal("no change within two minutes")
	}
	return nil
}

// A watcher gets the created, changed and removed artifacts of its directories after a commit, and
// nothing else.
func TestWatchReportsChangesInItsDirectories(t *testing.T) {
	w, arch := newWatchFixture(t)
	s, err := w.Watch("notes")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	all, err := w.Watch()
	if err != nil {
		t.Fatal(err)
	}
	defer all.Close()

	commitFile(t, arch, "docs/a.md", "a\n")
	commitFile(t, arch, "notes/000001.yaml", "one\n")
	commitFile(t, arch, "notes/000001.yaml", "two\n")
	if _, err := arch.Commit(context.Background(), storage.Change{Message: "x", Files: []storage.File{{Path: "notes/000002.txt", Data: []byte("x")}}}); err != nil {
		t.Fatal(err)
	}

	want := []watch.Kind{watch.Created, watch.Changed}
	for _, k := range want {
		got := nextChanges(t, s)
		if len(got) != 1 || got[0].Path != "notes/000001.yaml" || got[0].Kind != k || got[0].Version == "" {
			t.Fatalf("change %v, want %s of notes/000001.yaml", got, k)
		}
	}
	// The watcher without directory also saw the Markdown file, first.
	if got := nextChanges(t, all); len(got) != 1 || got[0].Path != "docs/a.md" || got[0].Kind != watch.Created {
		t.Fatalf("change %v", got)
	}
	if _, err := w.Watch("../x"); err == nil {
		t.Error("a directory that is not a valid path was accepted")
	}
}

// A watcher that never reads does not hold up a commit: every commit succeeds. The watcher is
// dropped and says so, and another watcher that reads gets every change.
func TestSlowWatcherDoesNotHoldUpCommits(t *testing.T) {
	w, arch := newWatchFixture(t)
	slow, err := w.Watch("notes")
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	fast, err := w.Watch("notes")
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()

	const commits = 100
	got := make(chan int, 1)
	go func() {
		n := 0
		for c := range fast.Changes() {
			n += len(c)
			if n == commits {
				break
			}
		}
		got <- n
	}()
	for i := 1; i <= commits; i++ {
		commitFile(t, arch, fmt.Sprintf("notes/%06d.yaml", i), "x\n")
	}
	select {
	case n := <-got:
		if n != commits {
			t.Errorf("the reading watcher got %d changes, want %d", n, commits)
		}
	case <-time.After(2 * time.Minute):
		t.Fatal("the reading watcher did not get all changes")
	}
	// The slow watcher can read what was buffered; then its channel closes with ErrDropped.
	for range slow.Changes() {
	}
	if err := slow.Err(); !errors.Is(err, watch.ErrDropped) {
		t.Errorf("error of the slow watcher: %v, want ErrDropped", err)
	}
	if err := fast.Err(); err != nil {
		t.Errorf("error of the fast watcher: %v", err)
	}
	// A new watcher after the drop works.
	again, err := w.Watch("notes")
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	commitFile(t, arch, "notes/000999.yaml", "x\n")
	if got := nextChanges(t, again); len(got) != 1 || got[0].Path != "notes/000999.yaml" {
		t.Errorf("change %v", got)
	}
}

// Close ends a subscription without an error, also when its watcher is blocked.
func TestWatchCloseFreesTheWatcher(t *testing.T) {
	w, arch := newWatchFixture(t)
	s, err := w.Watch()
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, arch, "docs/a.md", "a\n")
	s.Close()
	s.Close()
	for range s.Changes() {
	}
	if err := s.Err(); err != nil {
		t.Errorf("error after Close: %v", err)
	}
}

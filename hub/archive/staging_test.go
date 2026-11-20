package archive_test

// Contract tests of the staging repository of a tenant (dec-staging-repository,
// concept-session-release) through the in-process interfaces if-archive-storage (storage.Staging,
// storage.Archive), if-archive-watch and if-archive-repository. They lie in the provider tree
// because the interfaces are in-process Go interfaces and no part of the hub uses the staging
// repository yet; a test in tests/contract may not open the archive. A restart is a second OpenRepos on the same directory. A release that a restart
// cut off is made with what a dead hub leaves behind: a lock file of Git in the artifact
// repository, which the next start removes, and a main that moved to the recorded release commit
// just before the hub died.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/hub/archive"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/archive/watch"
)

const stagingTenant = "acme"

var (
	authorAda = storage.Author{Name: "Ada", Email: "ada@example.org"}
	authorBen = storage.Author{Name: "Ben", Email: "ben@example.org"}
)

type stagingFixture struct {
	root    string
	repos   *archive.Repos
	staging storage.Staging
	arch    storage.Archive
	dir     string // the artifact repository, as Git access finds it
}

func openStaging(t *testing.T, root string, create bool) *stagingFixture {
	t.Helper()
	ctx := context.Background()
	r, err := archive.OpenRepos(ctx, root, archive.Options{Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if create {
		if err := r.CreateTenant(ctx, stagingTenant); err != nil {
			t.Fatal(err)
		}
	}
	f := &stagingFixture{root: root, repos: r}
	if f.staging, err = r.Staging(stagingTenant); err != nil {
		t.Fatal(err)
	}
	if f.arch, err = r.Tenant(stagingTenant); err != nil {
		t.Fatal(err)
	}
	if f.dir, err = r.Dir(stagingTenant); err != nil {
		t.Fatal(err)
	}
	return f
}

func newStaging(t *testing.T) *stagingFixture { return openStaging(t, t.TempDir(), true) }

func stagingCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// stage writes one file into the area of a session.
func (f *stagingFixture) stage(t *testing.T, session, path, content string, author storage.Author) {
	t.Helper()
	if _, err := f.staging.Commit(stagingCtx(t), session, storage.Change{Message: "stage " + path, Author: author,
		Files: []storage.File{{Path: path, Data: []byte(content)}}}); err != nil {
		t.Fatalf("stage %s: %v", path, err)
	}
}

// stub writes the stub of a staged file into the artifact repository, as the part of a type does.
func (f *stagingFixture) stub(t *testing.T, path string) {
	t.Helper()
	if _, err := f.arch.Commit(stagingCtx(t), storage.Change{Message: "Reserve a name",
		Files: []storage.File{{Path: path, Data: []byte("waits for a release\n"), Base: storage.NoFile}}}); err != nil {
		t.Fatalf("stub %s: %v", path, err)
	}
}

// artifacts returns the files of a directory of the artifact repository as path -> content.
func (f *stagingFixture) artifacts(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := f.arch.Read(stagingCtx(t), dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, x := range files {
		out[x.Path] = string(x.Data)
	}
	return out
}

func stagingGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// releaseCommits returns the IDs of the commits on main of the artifact repository that name the
// session as released.
func (f *stagingFixture) releaseCommits(t *testing.T, session string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(stagingGit(t, f.dir, "log", "--format=%H %(trailers:key=Office-Release,valueonly,separator=%x20)", "main"), "\n") {
		id, value, _ := strings.Cut(line, " ")
		if strings.TrimSpace(value) == session {
			out = append(out, id)
		}
	}
	return out
}

// An area keeps the staged files of its session, its readers and its release state. The files lie
// at the paths they get in the artifact repository, a path belongs to one area, and nothing of an
// area reaches the artifact repository before the release.
func TestStagingAreas(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	if _, err := f.staging.Area(ctx, "weekly"); !errors.Is(err, storage.ErrNoArea) {
		t.Fatalf("area before open: %v, want ErrNoArea", err)
	}
	if _, err := f.staging.Commit(ctx, "weekly", storage.Change{Message: "x", Files: []storage.File{{Path: "notes/1.md", Data: []byte("x")}}}); !errors.Is(err, storage.ErrNoArea) {
		t.Fatalf("write before open: %v, want ErrNoArea", err)
	}
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada"}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC)
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada", "p-ben"}, Deadline: deadline, Held: true}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "weekly", "minutes/000001.md", "# Minutes\n", authorAda)
	f.stage(t, "weekly", "notes/000001.md", "A note.\n", authorBen)
	f.stage(t, "weekly", "minutes/000001.md", "# Minutes\n\nMore.\n", authorAda)
	f.stage(t, "weekly", "notes/000002.md", "Another note.\n", storage.Author{})
	if _, err := f.staging.Commit(ctx, "weekly", storage.Change{Message: "remove a note", Author: authorBen,
		Files: []storage.File{{Path: "notes/000002.md", Remove: true}}}); err != nil {
		t.Fatal(err)
	}

	a, err := f.staging.Area(ctx, "weekly")
	if err != nil {
		t.Fatal(err)
	}
	if a.Session != "weekly" || !slices.Equal(a.Readers, []string{"p-ada", "p-ben"}) || !a.Deadline.Equal(deadline) || !a.Held || a.Release != nil {
		t.Errorf("area: %+v", a)
	}
	if want := []string{"minutes/000001.md", "notes/000001.md", "notes/000002.md"}; !slices.Equal(a.Names, want) {
		t.Errorf("names %v, want %v", a.Names, want)
	}
	if want := []storage.Author{authorAda, authorBen}; !slices.Equal(a.Authors, want) {
		t.Errorf("authors %v, want %v", a.Authors, want)
	}
	list, err := f.staging.List(ctx, "weekly")
	if err != nil || !slices.Equal(list, []string{"minutes/000001.md", "notes/000001.md"}) {
		t.Errorf("list %v, %v", list, err)
	}
	files, err := f.staging.Read(ctx, "weekly", "minutes")
	if err != nil || len(files) != 1 || string(files[0].Data) != "# Minutes\n\nMore.\n" || files[0].Version == "" {
		t.Errorf("read %+v, %v", files, err)
	}

	// A second session has its own area; a path of the first area is not its path.
	if err := f.staging.OpenArea(ctx, "daily", storage.AreaState{Readers: []string{"p-cy"}}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "daily", "notes/000003.md", "Daily note.\n", authorAda)
	for _, p := range []string{"notes/000001.md", "notes/000002.md"} {
		if _, err := f.staging.Commit(ctx, "daily", storage.Change{Message: "take", Files: []storage.File{{Path: p, Data: []byte("x")}}}); !storage.IsConflict(err) {
			t.Errorf("write of %s of another area: %v, want a conflict", p, err)
		}
	}
	if _, err := f.staging.Commit(ctx, "daily", storage.Change{Message: "remove", Files: []storage.File{{Path: "notes/000001.md", Remove: true}}}); err == nil {
		t.Error("an area removed a file of another area")
	}
	if files, err := f.staging.Read(ctx, "daily", "notes"); err != nil || len(files) != 1 || files[0].Path != "notes/000003.md" {
		t.Errorf("read of the second area: %+v, %v", files, err)
	}
	// A stale base is refused, as in the artifact repository.
	if _, err := f.staging.Commit(ctx, "weekly", storage.Change{Message: "stale", Files: []storage.File{{Path: "notes/000001.md", Data: []byte("y"), Base: storage.NoFile}}}); !storage.IsConflict(err) {
		t.Errorf("stale base: %v, want a conflict", err)
	}
	// A session ID that is no file name works too.
	if err := f.staging.OpenArea(ctx, "a/b c", storage.AreaState{}); err != nil {
		t.Fatal(err)
	}
	areas, err := f.staging.Areas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, x := range areas {
		ids = append(ids, x.Session)
	}
	if want := []string{"a/b c", "daily", "weekly"}; !slices.Equal(ids, want) {
		t.Errorf("areas %v, want %v", ids, want)
	}
	// Nothing staged is in the artifact repository.
	if got := f.artifacts(t, "notes"); len(got) != 0 {
		t.Errorf("artifact repository holds %v", got)
	}
	// The state survives a restart.
	g := openStaging(t, f.root, false)
	if b, err := g.staging.Area(ctx, "weekly"); err != nil || !slices.Equal(b.Names, a.Names) || !b.Deadline.Equal(deadline) {
		t.Errorf("area after a restart: %+v, %v", b, err)
	}
}

// The release moves every file of the area into the artifact repository in one commit. Each file
// replaces its stub, the stub of a removed file goes away, the author is the participant who
// released and the authors of the area are co-authors. The commit names the session, and the area
// is gone afterwards.
func TestStagingReleaseCommit(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada"}}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct {
		path, content string
		by            storage.Author
	}{{"minutes/000001.md", "# Minutes\n", authorAda}, {"notes/000001.md", "A note.\n", authorBen}, {"notes/000002.md", "Gone.\n", authorBen}} {
		f.stage(t, "weekly", s.path, s.content, s.by)
		f.stub(t, s.path)
	}
	if _, err := f.staging.Commit(ctx, "weekly", storage.Change{Message: "remove", Author: authorBen,
		Files: []storage.File{{Path: "notes/000002.md", Remove: true}}}); err != nil {
		t.Fatal(err)
	}
	f.stub(t, "notes/000009.md") // a stub of another session stays
	before := stagingGit(t, f.dir, "rev-parse", "main")

	id, err := f.staging.Release(ctx, "weekly", authorAda)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || strings.TrimSpace(stagingGit(t, f.dir, "rev-parse", "main")) != id {
		t.Fatalf("release commit %q is not main", id)
	}
	if parent := stagingGit(t, f.dir, "rev-parse", id+"^"); parent != before {
		t.Errorf("the release is more than one commit")
	}
	want := map[string]string{"minutes/000001.md": "# Minutes\n"}
	if got := f.artifacts(t, "minutes"); !mapsEqual(got, want) {
		t.Errorf("minutes %v, want %v", got, want)
	}
	want = map[string]string{"notes/000001.md": "A note.\n", "notes/000009.md": "waits for a release\n"}
	if got := f.artifacts(t, "notes"); !mapsEqual(got, want) {
		t.Errorf("notes %v, want %v", got, want)
	}
	meta := stagingGit(t, f.dir, "log", "-1", "--format=%an <%ae>%n%cn%n%B", id)
	for _, s := range []string{"Ada <ada@example.org>\noffice-hub\n", "Co-authored-by: Ben <ben@example.org>", "Office-Release: weekly"} {
		if !strings.Contains(meta, s) {
			t.Errorf("release commit lacks %q:\n%s", s, meta)
		}
	}
	if strings.Contains(meta, "Co-authored-by: Ada") {
		t.Errorf("the author is also a co-author:\n%s", meta)
	}
	if _, err := f.staging.Area(ctx, "weekly"); !errors.Is(err, storage.ErrNoArea) {
		t.Errorf("area after the release: %v, want ErrNoArea", err)
	}
	if _, err := f.staging.Release(ctx, "weekly", authorAda); !errors.Is(err, storage.ErrNoArea) {
		t.Errorf("second release: %v, want ErrNoArea", err)
	}
	if got := f.releaseCommits(t, "weekly"); len(got) != 1 {
		t.Errorf("release commits %v, want one", got)
	}

	// The hub releases when no author is given. An area with nothing staged releases without a
	// commit.
	if err := f.staging.OpenArea(ctx, "daily", storage.AreaState{}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "daily", "notes/000003.md", "Daily.\n", authorBen)
	f.stub(t, "notes/000003.md")
	id, err = f.staging.Release(ctx, "daily", storage.Author{})
	if err != nil {
		t.Fatal(err)
	}
	if meta := stagingGit(t, f.dir, "log", "-1", "--format=%an%n%B", id); !strings.HasPrefix(meta, "office-hub\n") || !strings.Contains(meta, "Co-authored-by: Ben <ben@example.org>") {
		t.Errorf("release by the hub:\n%s", meta)
	}
	if err := f.staging.OpenArea(ctx, "empty", storage.AreaState{}); err != nil {
		t.Fatal(err)
	}
	main := stagingGit(t, f.dir, "rev-parse", "main")
	if id, err := f.staging.Release(ctx, "empty", authorAda); err != nil || id != "" {
		t.Errorf("release of an empty area: %q, %v", id, err)
	}
	if stagingGit(t, f.dir, "rev-parse", "main") != main {
		t.Error("the release of an empty area made a commit")
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// A release that a restart cut off before its commit landed keeps the area closed for writes.
// The next start completes it with the author of the first call, in exactly one commit.
func TestStagingCompletesReleaseCutOffBeforeTheCommit(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada"}}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "weekly", "notes/000001.md", "A note.\n", authorBen)
	f.stub(t, "notes/000001.md")
	// The hub dies while Git moves main of the artifact repository: its lock file stays.
	lock := filepath.Join(f.dir, "refs", "heads", "main.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.staging.Release(ctx, "weekly", authorAda); err == nil {
		t.Fatal("the release worked although main was locked")
	}
	a, err := f.staging.Area(ctx, "weekly")
	if err != nil || a.Release == nil || a.Release.Author != authorAda {
		t.Fatalf("area after the cut: %+v, %v", a, err)
	}
	if _, err := f.staging.Commit(ctx, "weekly", storage.Change{Message: "late", Files: []storage.File{{Path: "notes/000001.md", Data: []byte("late")}}}); !errors.Is(err, storage.ErrAreaClosed) {
		t.Errorf("write to a closed area: %v, want ErrAreaClosed", err)
	}
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada", "p-ben"}}); !errors.Is(err, storage.ErrAreaClosed) {
		t.Errorf("state of a closed area: %v, want ErrAreaClosed", err)
	}

	g := openStaging(t, f.root, false) // the restart removes the lock file
	areas, err := g.staging.Areas(ctx)
	if err != nil || len(areas) != 1 || areas[0].Release == nil {
		t.Fatalf("areas after the restart: %+v, %v", areas, err)
	}
	id, err := g.staging.Release(ctx, "weekly", storage.Author{})
	if err != nil {
		t.Fatal(err)
	}
	if got := g.releaseCommits(t, "weekly"); !slices.Equal(got, []string{id}) {
		t.Errorf("release commits %v, want [%s]", got, id)
	}
	if by := stagingGit(t, g.dir, "log", "-1", "--format=%an", id); by != "Ada\n" {
		t.Errorf("author %q, want Ada", by)
	}
	if got := g.artifacts(t, "notes"); got["notes/000001.md"] != "A note.\n" {
		t.Errorf("notes %v", got)
	}
	if _, err := g.staging.Area(ctx, "weekly"); !errors.Is(err, storage.ErrNoArea) {
		t.Errorf("area after the release: %v", err)
	}
}

// A release that a restart cut off after its commit landed is not made again: the next start only
// cleans up the area.
func TestStagingCompletesReleaseCutOffAfterTheCommit(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "weekly", "notes/000001.md", "A note.\n", authorBen)
	f.stub(t, "notes/000001.md")
	lock := filepath.Join(f.dir, "refs", "heads", "main.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.staging.Release(ctx, "weekly", authorAda); err == nil {
		t.Fatal("the release worked although main was locked")
	}
	a, err := f.staging.Area(ctx, "weekly")
	if err != nil || a.Release == nil || a.Release.Commit == "" {
		t.Fatalf("area after the cut: %+v, %v", a, err)
	}
	// Git moved main to the recorded commit, and the hub died right after.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	stagingGit(t, f.dir, "update-ref", "refs/heads/main", a.Release.Commit)

	g := openStaging(t, f.root, false)
	id, err := g.staging.Release(ctx, "weekly", authorBen)
	if err != nil {
		t.Fatal(err)
	}
	if id != a.Release.Commit {
		t.Errorf("release returned %s, want the recorded commit %s", id, a.Release.Commit)
	}
	if got := g.releaseCommits(t, "weekly"); len(got) != 1 {
		t.Errorf("release commits %v, want one", got)
	}
	if _, err := g.staging.Area(ctx, "weekly"); !errors.Is(err, storage.ErrNoArea) {
		t.Errorf("area after the release: %v", err)
	}
	if list, err := g.staging.Areas(ctx); err != nil || len(list) != 0 {
		t.Errorf("areas %v, %v", list, err)
	}
}

// The parts follow the staging repository like the artifact repository: they get the staged
// artifacts that were created, changed and removed, and nothing of the state of the areas. The
// release removes the files from the staging repository and changes the stubs in the artifact
// repository.
func TestStagingWatch(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	sw, err := f.repos.StagingWatcher(stagingTenant)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := sw.Watch()
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	aw, err := f.repos.Watcher(stagingTenant)
	if err != nil {
		t.Fatal(err)
	}
	released, err := aw.Watch("notes")
	if err != nil {
		t.Fatal(err)
	}
	defer released.Close()

	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada"}}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "weekly", "notes/000001.md", "one\n", authorAda)
	f.stage(t, "weekly", "notes/000001.md", "two\n", authorAda)
	if got := nextChanges(t, staged); len(got) != 1 || got[0].Path != "notes/000001.md" || got[0].Kind != watch.Created || got[0].Version == "" {
		t.Fatalf("staged change %v, want notes/000001.md created", got)
	}
	if got := nextChanges(t, staged); len(got) != 1 || got[0].Kind != watch.Changed {
		t.Fatalf("staged change %v, want changed", got)
	}
	f.stub(t, "notes/000001.md")
	if got := nextChanges(t, released); len(got) != 1 || got[0].Kind != watch.Created {
		t.Fatalf("stub %v", got)
	}
	if _, err := f.staging.Release(ctx, "weekly", authorAda); err != nil {
		t.Fatal(err)
	}
	if got := nextChanges(t, released); len(got) != 1 || got[0].Path != "notes/000001.md" || got[0].Kind != watch.Changed {
		t.Errorf("released change %v, want notes/000001.md changed", got)
	}
	if got := nextChanges(t, staged); len(got) != 1 || got[0].Path != "notes/000001.md" || got[0].Kind != watch.Removed {
		t.Errorf("staged change %v, want notes/000001.md removed", got)
	}
	if _, err := f.repos.StagingWatcher("nowhere"); err == nil {
		t.Error("a watcher of a tenant without repositories")
	}
}

// Git access finds the repository of a tenant through if-archive-repository. That repository
// is the artifact repository: a clone of it holds no staged file and no state of an area.
func TestStagingIsNotTheRepositoryOfGitAccess(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-secret-reader"}}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "weekly", "notes/000001.md", "Staged secret.\n", authorAda)
	f.stub(t, "notes/000001.md")
	clone := filepath.Join(t.TempDir(), "c")
	stagingGit(t, filepath.Dir(clone), "clone", "-q", f.dir, clone)
	objects := stagingGit(t, clone, "cat-file", "--batch-all-objects", "--batch")
	for _, s := range []string{"Staged secret.", "p-secret-reader"} {
		if strings.Contains(objects, s) {
			t.Errorf("the repository of Git access holds %q", s)
		}
	}
	if !strings.Contains(objects, "waits for a release") {
		t.Error("the clone lacks the stub")
	}
}

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

// stub writes the stub of a staged file into the artifact repository through the area of a
// session, as the part of a type does.
func (f *stagingFixture) stub(t *testing.T, session, path string) {
	t.Helper()
	if _, err := f.staging.Stub(stagingCtx(t), session, storage.Change{Message: "Reserve a name",
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
		f.stub(t, "weekly", s.path)
	}
	if _, err := f.staging.Commit(ctx, "weekly", storage.Change{Message: "remove", Author: authorBen,
		Files: []storage.File{{Path: "notes/000002.md", Remove: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.staging.OpenArea(ctx, "other", storage.AreaState{}); err != nil {
		t.Fatal(err)
	}
	f.stub(t, "other", "notes/000009.md") // a stub of another session stays
	before := stagingGit(t, f.dir, "rev-parse", "main")

	res, err := f.staging.Release(ctx, "weekly", authorAda)
	if err != nil {
		t.Fatal(err)
	}
	id := res.Commit
	if len(res.Conflicts) != 0 {
		t.Errorf("conflicts %v, want none", res.Conflicts)
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
	f.stub(t, "daily", "notes/000003.md")
	res, err = f.staging.Release(ctx, "daily", storage.Author{})
	id = res.Commit
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
	if res, err := f.staging.Release(ctx, "empty", authorAda); err != nil || res.Commit != "" || len(res.Conflicts) != 0 {
		t.Errorf("release of an empty area: %+v, %v", res, err)
	}
	if stagingGit(t, f.dir, "rev-parse", "main") != main {
		t.Error("the release of an empty area made a commit")
	}
}

// Issue #217: a part stages a note, but a push puts a file at the same path before the stub is
// written. The stub commit fails, and the part takes the staged file back and stages the next ID.
// The area forgets the path it gave up, so the release keeps the pushed file. A file that the
// session itself removed still loses its stub at the release.
func TestStagingTakeBackKeepsAPushedFile(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada"}}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "weekly", "notes/000031.md", "The agent's note.\n", authorAda)
	// The push of a participant comes first.
	if _, err := f.arch.Commit(ctx, storage.Change{Message: "pushed", Author: authorBen,
		Files: []storage.File{{Path: "notes/000031.md", Data: []byte("Ben's own note.\n"), Base: storage.NoFile}}}); err != nil {
		t.Fatal(err)
	}
	// The stub cannot be written now.
	_, err := f.staging.Stub(ctx, "weekly", storage.Change{Message: "Reserve a name",
		Files: []storage.File{{Path: "notes/000031.md", Data: []byte("waits for a release\n"), Base: storage.NoFile}}})
	if !storage.IsConflict(err) {
		t.Fatalf("stub over a pushed file: %v, want a conflict", err)
	}
	if err := f.staging.TakeBack(ctx, "weekly", "notes/000031.md"); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "weekly", "notes/000032.md", "The agent's note.\n", authorAda)
	f.stub(t, "weekly", "notes/000032.md")
	f.stage(t, "weekly", "notes/000033.md", "Removed later.\n", authorAda)
	f.stub(t, "weekly", "notes/000033.md")
	if _, err := f.staging.Commit(ctx, "weekly", storage.Change{Message: "remove", Author: authorAda,
		Files: []storage.File{{Path: "notes/000033.md", Remove: true}}}); err != nil {
		t.Fatal(err)
	}

	a, err := f.staging.Area(ctx, "weekly")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"notes/000032.md", "notes/000033.md"}; !slices.Equal(a.Names, want) {
		t.Errorf("names %v, want %v", a.Names, want)
	}
	if list, err := f.staging.List(ctx, "weekly"); err != nil || !slices.Equal(list, []string{"notes/000032.md"}) {
		t.Errorf("list %v, %v", list, err)
	}
	// A path that the area does not name changes nothing.
	if err := f.staging.TakeBack(ctx, "weekly", "notes/000099.md"); err != nil {
		t.Errorf("take back an unknown path: %v", err)
	}
	if err := f.staging.TakeBack(ctx, "nobody", "notes/000032.md"); !errors.Is(err, storage.ErrNoArea) {
		t.Errorf("take back without an area: %v, want ErrNoArea", err)
	}

	if _, err := f.staging.Release(ctx, "weekly", authorAda); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"notes/000031.md": "Ben's own note.\n", "notes/000032.md": "The agent's note.\n"}
	if got := f.artifacts(t, "notes"); !mapsEqual(got, want) {
		t.Errorf("notes after the release %v, want %v", got, want)
	}
	if err := f.staging.TakeBack(ctx, "weekly", "notes/000032.md"); !errors.Is(err, storage.ErrNoArea) {
		t.Errorf("take back after the release: %v, want ErrNoArea", err)
	}
}

// Issue #220: the hub stages a note and dies before it writes the stub. A participant pushes
// their own file at that path, so the stub cannot be written at the next start. The release keeps
// the pushed file, releases the rest of the session and reports the conflict. The area stays,
// closed, with its readers and only the note that was not released, so its readers still see it.
// A file that only looks like a stub, but was not written through the area, is not replaced
// either. The order of staged file and stub does not matter.
func TestStagingReleaseKeepsAPushedFile(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada", "p-ben"}}); err != nil {
		t.Fatal(err)
	}
	// A normal note: staged file, then stub.
	f.stage(t, "weekly", "notes/000030.md", "Released note.\n", authorAda)
	f.stub(t, "weekly", "notes/000030.md")
	// The other order: stub, then staged file.
	f.stub(t, "weekly", "minutes/000001.md")
	f.stage(t, "weekly", "minutes/000001.md", "# Minutes\n", authorAda)
	// The crash: a staged file without a stub.
	f.stage(t, "weekly", "notes/000031.md", "The agent's note.\n", authorAda)
	// A stub that someone else wrote, not through the area.
	f.stage(t, "weekly", "notes/000033.md", "Another agent note.\n", authorAda)
	if _, err := f.arch.Commit(ctx, storage.Change{Message: "Reserve a name",
		Files: []storage.File{{Path: "notes/000033.md", Data: []byte("waits for a release\n"), Base: storage.NoFile}}}); err != nil {
		t.Fatal(err)
	}
	// A staged file without a stub at a free path: the release writes it, nothing is replaced.
	f.stage(t, "weekly", "notes/000034.md", "No stub.\n", authorAda)

	// While the hub is down, Ben pushes his own file at the path of the crashed note.
	if _, err := f.arch.Commit(ctx, storage.Change{Message: "pushed", Author: authorBen,
		Files: []storage.File{{Path: "notes/000031.md", Data: []byte("Ben's own note.\n"), Base: storage.NoFile}}}); err != nil {
		t.Fatal(err)
	}
	g := openStaging(t, f.root, false)
	// At the start, the part tries to write the missing stub and fails.
	if _, err := g.staging.Stub(ctx, "weekly", storage.Change{Message: "Reserve a name",
		Files: []storage.File{{Path: "notes/000031.md", Data: []byte("waits for a release\n"), Base: storage.NoFile}}}); !storage.IsConflict(err) {
		t.Fatalf("stub over the pushed file: %v, want a conflict", err)
	}

	res, err := g.staging.Release(ctx, "weekly", authorAda)
	if err != nil {
		t.Fatal(err)
	}
	wantConflicts := []storage.Conflict{{Session: "weekly", Path: "notes/000031.md"}, {Session: "weekly", Path: "notes/000033.md"}}
	if res.Commit == "" || !slices.Equal(res.Conflicts, wantConflicts) {
		t.Fatalf("release %+v, want a commit and conflicts %v", res, wantConflicts)
	}
	want := map[string]string{
		"notes/000030.md": "Released note.\n",
		"notes/000031.md": "Ben's own note.\n",
		"notes/000033.md": "waits for a release\n",
		"notes/000034.md": "No stub.\n",
	}
	if got := g.artifacts(t, "notes"); !mapsEqual(got, want) {
		t.Errorf("notes after the release %v, want %v", got, want)
	}
	if got := g.artifacts(t, "minutes"); got["minutes/000001.md"] != "# Minutes\n" {
		t.Errorf("minutes after the release %v", got)
	}
	rc, err := g.staging.Released(ctx, "weekly")
	if err != nil || rc.ID != res.Commit {
		t.Fatalf("released %+v, %v", rc, err)
	}
	if wantPaths := []string{"minutes/000001.md", "notes/000030.md", "notes/000034.md"}; !slices.Equal(rc.Paths, wantPaths) {
		t.Errorf("released paths %v, want %v", rc.Paths, wantPaths)
	}

	// The area keeps the notes that were not released, for its readers.
	a, err := g.staging.Area(ctx, "weekly")
	if err != nil {
		t.Fatal(err)
	}
	if a.Release == nil || !a.Release.Done || a.Release.Commit != res.Commit || !slices.Equal(a.Readers, []string{"p-ada", "p-ben"}) {
		t.Errorf("area after the release: %+v", a)
	}
	if wantNames := []string{"notes/000031.md", "notes/000033.md"}; !slices.Equal(a.Names, wantNames) {
		t.Errorf("names %v, want %v", a.Names, wantNames)
	}
	if list, err := g.staging.List(ctx, "weekly"); err != nil || !slices.Equal(list, []string{"notes/000031.md", "notes/000033.md"}) {
		t.Errorf("list %v, %v", list, err)
	}
	if files, err := g.staging.Read(ctx, "weekly", "notes/000031.md"); err != nil || len(files) != 1 || string(files[0].Data) != "The agent's note.\n" {
		t.Errorf("read the kept note: %+v, %v", files, err)
	}
	// Nothing writes into the area any more.
	if _, err := g.staging.Commit(ctx, "weekly", storage.Change{Message: "late", Files: []storage.File{{Path: "notes/000031.md", Data: []byte("x")}}}); !errors.Is(err, storage.ErrAreaClosed) {
		t.Errorf("write after the release: %v, want ErrAreaClosed", err)
	}
	if _, err := g.staging.Stub(ctx, "weekly", storage.Change{Message: "late", Files: []storage.File{{Path: "notes/000040.md", Data: []byte("x")}}}); !errors.Is(err, storage.ErrAreaClosed) {
		t.Errorf("stub after the release: %v, want ErrAreaClosed", err)
	}
	if err := g.staging.OpenArea(ctx, "weekly", storage.AreaState{}); !errors.Is(err, storage.ErrAreaClosed) {
		t.Errorf("open after the release: %v, want ErrAreaClosed", err)
	}
	// Another area cannot take the kept path.
	if err := g.staging.OpenArea(ctx, "daily", storage.AreaState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.staging.Commit(ctx, "daily", storage.Change{Message: "take", Files: []storage.File{{Path: "notes/000031.md", Data: []byte("x")}}}); !storage.IsConflict(err) {
		t.Errorf("another area wrote a kept path: %v, want a conflict", err)
	}
	// The release again, also after a restart, gives the same answer and makes no commit.
	h := openStaging(t, f.root, false)
	again, err := h.staging.Release(ctx, "weekly", authorBen)
	if err != nil || again.Commit != res.Commit || !slices.Equal(again.Conflicts, wantConflicts) {
		t.Errorf("release again %+v, %v", again, err)
	}
	if got := h.releaseCommits(t, "weekly"); !slices.Equal(got, []string{res.Commit}) {
		t.Errorf("release commits %v, want [%s]", got, res.Commit)
	}
}

// A stub that the area records is still a stub of a name whose staged file is gone: the release
// removes it. A failed stub leaves no record, and a stub of a path of another area is refused.
func TestStagingStubs(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	for _, s := range []string{"weekly", "daily"} {
		if err := f.staging.OpenArea(ctx, s, storage.AreaState{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.staging.Stub(ctx, "nobody", storage.Change{Message: "x", Files: []storage.File{{Path: "notes/000001.md", Data: []byte("x")}}}); !errors.Is(err, storage.ErrNoArea) {
		t.Errorf("stub without an area: %v, want ErrNoArea", err)
	}
	if _, err := f.staging.Stub(ctx, "weekly", storage.Change{Message: "x", Files: []storage.File{{Path: "notes/000001.md", Remove: true}}}); err == nil {
		t.Error("a stub removed a file")
	}
	f.stub(t, "weekly", "notes/000001.md") // a stub without a staged file
	if _, err := f.staging.Stub(ctx, "daily", storage.Change{Message: "x", Files: []storage.File{{Path: "notes/000001.md", Data: []byte("x")}}}); !storage.IsConflict(err) {
		t.Errorf("stub of a path of another area: %v, want a conflict", err)
	}
	// A pushed file makes the stub fail; the area forgets the name it had only for that stub.
	if _, err := f.arch.Commit(ctx, storage.Change{Message: "pushed", Files: []storage.File{{Path: "notes/000002.md", Data: []byte("pushed\n")}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.staging.Stub(ctx, "daily", storage.Change{Message: "x", Files: []storage.File{{Path: "notes/000002.md", Data: []byte("x"), Base: storage.NoFile}}}); !storage.IsConflict(err) {
		t.Errorf("stub over a pushed file: %v, want a conflict", err)
	}
	if a, err := f.staging.Area(ctx, "daily"); err != nil || len(a.Names) != 0 {
		t.Errorf("area after a failed stub: %+v, %v", a, err)
	}
	if a, err := f.staging.Area(ctx, "weekly"); err != nil || !slices.Equal(a.Names, []string{"notes/000001.md"}) {
		t.Errorf("area with a stub: %+v, %v", a, err)
	}
	res, err := f.staging.Release(ctx, "weekly", authorAda)
	if err != nil || res.Commit == "" || len(res.Conflicts) != 0 {
		t.Fatalf("release %+v, %v", res, err)
	}
	if got := f.artifacts(t, "notes"); !mapsEqual(got, map[string]string{"notes/000002.md": "pushed\n"}) {
		t.Errorf("notes %v: the recorded stub must go, the pushed file stay", got)
	}
}

// A restart cuts off a release with a conflict. The next start completes it, reports the same
// conflict, and leaves the pushed file alone.
func TestStagingCompletesReleaseWithAConflict(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada"}}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "weekly", "notes/000001.md", "A note.\n", authorBen)
	f.stub(t, "weekly", "notes/000001.md")
	f.stage(t, "weekly", "notes/000002.md", "Taken.\n", authorBen)
	if _, err := f.arch.Commit(ctx, storage.Change{Message: "pushed", Files: []storage.File{{Path: "notes/000002.md", Data: []byte("pushed\n")}}}); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(f.dir, "refs", "heads", "main.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.staging.Release(ctx, "weekly", authorAda); err == nil {
		t.Fatal("the release worked although main was locked")
	}
	g := openStaging(t, f.root, false)
	res, err := g.staging.Release(ctx, "weekly", storage.Author{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Conflicts, []storage.Conflict{{Session: "weekly", Path: "notes/000002.md"}}) {
		t.Errorf("conflicts %v", res.Conflicts)
	}
	if got := g.releaseCommits(t, "weekly"); !slices.Equal(got, []string{res.Commit}) {
		t.Errorf("release commits %v, want [%s]", got, res.Commit)
	}
	want := map[string]string{"notes/000001.md": "A note.\n", "notes/000002.md": "pushed\n"}
	if got := g.artifacts(t, "notes"); !mapsEqual(got, want) {
		t.Errorf("notes %v, want %v", got, want)
	}
	if a, err := g.staging.Area(ctx, "weekly"); err != nil || a.Release == nil || !a.Release.Done || !slices.Equal(a.Names, []string{"notes/000002.md"}) {
		t.Errorf("area %+v, %v", a, err)
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
	f.stub(t, "weekly", "notes/000001.md")
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
	res, err := g.staging.Release(ctx, "weekly", storage.Author{})
	if err != nil {
		t.Fatal(err)
	}
	id := res.Commit
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
	f.stub(t, "weekly", "notes/000001.md")
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
	res, err := g.staging.Release(ctx, "weekly", authorBen)
	if err != nil {
		t.Fatal(err)
	}
	id := res.Commit
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
	f.stub(t, "weekly", "notes/000001.md")
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
	f.stub(t, "weekly", "notes/000001.md")
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

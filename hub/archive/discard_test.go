package archive_test

// Contract tests of the discard of a staged file that the release kept back (issue #231,
// concept-session-release) through if-archive-storage (storage.Staging) and the in-process read of
// if-archive-read (ReadService.Load).

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/hub/archive"
	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
)

// A participant discards a file that the release kept back. The area forgets the path and its
// conflict, and goes with its last kept file, together with its readers. From then on, the read
// interface serves the file of the artifact repository at the path, also to the readers of the
// session. A discard is refused while the release of the area is not done, for a path that the
// area does not keep, and for a session without an area.
func TestStagingDiscardKeptFile(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	read := archive.NewReadService(f.arch, f.staging)
	if err := f.staging.OpenArea(ctx, "weekly", storage.AreaState{Readers: []string{"p-ada"}}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "weekly", "notes/000001.md", "Released note.\n", authorAda)
	f.stub(t, "weekly", "notes/000001.md")
	f.stage(t, "weekly", "notes/000002.md", "The agent's note.\n", authorAda)
	f.stage(t, "weekly", "notes/000003.md", "Another note.\n", authorAda)

	// Before the release, nothing is kept.
	if err := f.staging.Discard(ctx, "weekly", "notes/000002.md", authorAda); !errors.Is(err, storage.ErrNotKept) {
		t.Fatalf("discard before the release: %v, want ErrNotKept", err)
	}
	if err := f.staging.Discard(ctx, "nobody", "notes/000002.md", authorAda); !errors.Is(err, storage.ErrNoArea) {
		t.Fatalf("discard without an area: %v, want ErrNoArea", err)
	}
	// Ben takes two paths in the artifact repository.
	if _, err := f.arch.Commit(ctx, storage.Change{Message: "pushed", Author: authorBen, Files: []storage.File{
		{Path: "notes/000002.md", Data: []byte("Ben's note.\n"), Base: storage.NoFile},
		{Path: "notes/000003.md", Data: []byte("Ben's other note.\n"), Base: storage.NoFile}}}); err != nil {
		t.Fatal(err)
	}
	res, err := f.staging.Release(ctx, "weekly", authorAda)
	if err != nil || len(res.Conflicts) != 2 {
		t.Fatalf("release %+v, %v, want two conflicts", res, err)
	}
	loaded := func(p string) (string, bool) {
		t.Helper()
		got, err := read.Load(ctx, "weekly", &archivev1.ReadFileRequest{Path: p})
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(got.GetFile().GetContent()), got.GetFile().GetStaged()
	}
	if got, staged := loaded("notes/000002.md"); got != "The agent's note.\n" || !staged {
		t.Errorf("the kept note before the discard: %q, staged %v", got, staged)
	}

	// A released path and a path that the area never named are no kept files.
	if err := f.staging.Discard(ctx, "weekly", "notes/000001.md", authorAda); !errors.Is(err, storage.ErrNotKept) {
		t.Errorf("discard of a released path: %v, want ErrNotKept", err)
	}
	if err := f.staging.Discard(ctx, "weekly", "notes/000009.md", authorAda); !errors.Is(err, storage.ErrNotKept) {
		t.Errorf("discard of an unknown path: %v, want ErrNotKept", err)
	}

	// The first discard: the area stays for the other kept note.
	if err := f.staging.Discard(ctx, "weekly", "notes/000002.md", authorAda); err != nil {
		t.Fatal(err)
	}
	a, err := f.staging.Area(ctx, "weekly")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Names, []string{"notes/000003.md"}) || a.Release == nil || !a.Release.Done || !slices.Equal(a.Readers, []string{"p-ada"}) {
		t.Errorf("area after the first discard: %+v", a)
	}
	again, err := f.staging.Release(ctx, "weekly", authorAda)
	if err != nil || !slices.Equal(again.Conflicts, []storage.Conflict{{Session: "weekly", Path: "notes/000003.md"}}) {
		t.Errorf("release after the first discard: %+v, %v", again, err)
	}
	if got, staged := loaded("notes/000002.md"); got != "Ben's note.\n" || staged {
		t.Errorf("the discarded path for a reader of the session: %q, staged %v", got, staged)
	}
	if got, staged := loaded("notes/000003.md"); got != "Another note.\n" || !staged {
		t.Errorf("the other kept note: %q, staged %v", got, staged)
	}
	if err := f.staging.Discard(ctx, "weekly", "notes/000002.md", authorAda); !errors.Is(err, storage.ErrNotKept) {
		t.Errorf("discard again: %v, want ErrNotKept", err)
	}

	// The last discard, by the hub: the area goes with its readers.
	if err := f.staging.Discard(ctx, "weekly", "notes/000003.md", storage.Author{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.staging.Area(ctx, "weekly"); !errors.Is(err, storage.ErrNoArea) {
		t.Errorf("area after the last discard: %v, want ErrNoArea", err)
	}
	if areas, err := f.staging.Areas(ctx); err != nil || len(areas) != 0 {
		t.Errorf("areas after the last discard: %+v, %v", areas, err)
	}
	if _, err := f.staging.Release(ctx, "weekly", authorAda); !errors.Is(err, storage.ErrNoArea) {
		t.Errorf("release after the last discard: %v, want ErrNoArea", err)
	}
	if got, staged := loaded("notes/000003.md"); got != "Ben's other note.\n" || staged {
		t.Errorf("the last discarded path: %q, staged %v", got, staged)
	}
	if out := stagingGit(t, filepath.Join(f.root, "staging", stagingTenant), "ls-tree", "-r", "--name-only", "main"); strings.TrimSpace(out) != "" {
		t.Errorf("the staging repository still holds files:\n%s", out)
	}
	// The artifact repository is as Ben left it, and the release commit is the only one.
	want := map[string]string{"notes/000001.md": "Released note.\n", "notes/000002.md": "Ben's note.\n", "notes/000003.md": "Ben's other note.\n"}
	if got := f.artifacts(t, "notes"); !mapsEqual(got, want) {
		t.Errorf("notes %v, want %v", got, want)
	}
	if got := f.releaseCommits(t, "weekly"); !slices.Equal(got, []string{res.Commit}) {
		t.Errorf("release commits %v, want [%s]", got, res.Commit)
	}
}

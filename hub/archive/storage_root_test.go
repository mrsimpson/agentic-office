package archive_test

// Contract test of if-archive-storage: Read and List with storage.Root give every file of the
// repository, so a part can keep a view of all artifacts of a tenant (concept-wiki). The test uses
// only the interface storage.Archive.

import (
	"context"
	"slices"
	"testing"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
)

func TestReadAndListTheWholeRepository(t *testing.T) {
	_, arch := newWatchFixture(t)
	ctx := context.Background()

	// A new tenant has a first commit without files.
	if files, err := arch.Read(ctx, storage.Root); err != nil || len(files) != 0 {
		t.Fatalf("read the root of a new tenant: %v, %v", files, err)
	}
	commitFile(t, arch, "notes/000001.md", "one")
	commitFile(t, arch, "documents/a/b.md", "two")
	commitFile(t, arch, "top.yaml", "three: 3")

	want := []string{"documents/a/b.md", "notes/000001.md", "top.yaml"}
	files, err := arch.Read(ctx, storage.Root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Path)
		if f.Version == "" || len(f.Data) == 0 {
			t.Errorf("%s: no content or no version", f.Path)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("read the root: %v, want %v", got, want)
	}
	if paths, err := arch.List(ctx, storage.Root); err != nil || !slices.Equal(paths, want) {
		t.Errorf("list the root: %v, %v, want %v", paths, err, want)
	}
	// A path that is not valid still fails.
	if _, err := arch.Read(ctx, "../x"); err == nil {
		t.Error("read ../x: no error")
	}
}

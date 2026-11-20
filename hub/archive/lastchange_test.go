package archive_test

// Contract tests of the newest change of a path (storage.Archive.LastChange, if-archive-storage):
// the author and the time of a plain change, a change by the hub, a removal that does not count,
// and a release, whose author is who last wrote the file in the area of the session. They use the
// fixture of the staging tests, because a release needs the staging repository.

import (
	"errors"
	"testing"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
)

func (f *stagingFixture) lastChange(t *testing.T, p string) storage.PathChange {
	t.Helper()
	ch, err := f.arch.LastChange(stagingCtx(t), p)
	if err != nil {
		t.Fatalf("the last change of %s: %v", p, err)
	}
	if ch.Time.IsZero() {
		t.Errorf("the last change of %s has no time", p)
	}
	return ch
}

func (f *stagingFixture) write(t *testing.T, author storage.Author, files ...storage.File) {
	t.Helper()
	if _, err := f.arch.Commit(stagingCtx(t), storage.Change{Message: "write", Author: author, Files: files}); err != nil {
		t.Fatal(err)
	}
}

func TestLastChangeOfAPlainChange(t *testing.T) {
	f := newStaging(t)
	ada := storage.ParticipantAuthor("Ada", "ada")
	f.write(t, ada, storage.File{Path: "notes/000001.md", Data: []byte("One.\n")})
	f.write(t, authorBen, storage.File{Path: "notes/000002.md", Data: []byte("Two.\n")})

	ch := f.lastChange(t, "notes/000001.md")
	if ch.Author != ada || ch.Release != nil {
		t.Errorf("the note of Ada: %+v", ch)
	}
	if id := ch.Author.ParticipantID(); id != "ada" {
		t.Errorf("the participant ID of Ada: %q", id)
	}
	if id := authorBen.ParticipantID(); id != "" {
		t.Errorf("an author who is no participant has the ID %q", id)
	}

	// A change by the hub has no author. A removal of the file is no change that wrote it.
	f.write(t, storage.Author{}, storage.File{Path: "notes/000002.md", Data: []byte("Two, by the hub.\n")})
	if ch := f.lastChange(t, "notes/000002.md"); ch.Author != (storage.Author{}) {
		t.Errorf("the change by the hub has the author %+v", ch.Author)
	}
	f.write(t, authorBen, storage.File{Path: "notes/000001.md", Remove: true})
	if ch := f.lastChange(t, "notes/000001.md"); ch.Author != ada {
		t.Errorf("after the removal: %+v, want the write of Ada", ch)
	}

	for _, p := range []string{"notes/000009.md", "../x", ""} {
		if _, err := f.arch.LastChange(stagingCtx(t), p); !errors.Is(err, storage.ErrNoChange) {
			t.Errorf("the last change of %q: %v, want ErrNoChange", p, err)
		}
	}
}

func TestLastChangeOfARelease(t *testing.T) {
	f := newStaging(t)
	ctx := stagingCtx(t)
	session := "weekly meeting/1" // not safe as a file name
	if err := f.staging.OpenArea(ctx, session, storage.AreaState{}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, session, "notes/000001.md", "First.\n", authorBen)
	f.stub(t, session, "notes/000001.md")
	f.stage(t, session, "notes/000001.md", "Second.\n", authorAda)
	f.stage(t, session, "notes/000002.md", "Ben's.\n", authorBen)
	f.stub(t, session, "notes/000002.md")
	if _, err := f.staging.Release(ctx, session, authorBen); err != nil {
		t.Fatal(err)
	}

	ch := f.lastChange(t, "notes/000001.md")
	if ch.Author != authorAda {
		t.Errorf("the author of the released note: %+v, want Ada, who wrote it last", ch.Author)
	}
	if r := ch.Release; r == nil || r.Session != session || r.By != authorBen || r.Time.Before(ch.Time) {
		t.Errorf("the release of the note: %+v, want session %q released by Ben after the write", r, session)
	}
	if ch := f.lastChange(t, "notes/000002.md"); ch.Author != authorBen || ch.Release == nil {
		t.Errorf("the other note: %+v", ch)
	}

	// A later session stages the same path again. Until it is released, the released version
	// stays the newest change.
	if err := f.staging.OpenArea(ctx, "later", storage.AreaState{}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "later", "notes/000001.md", "Third.\n", authorBen)
	if ch := f.lastChange(t, "notes/000001.md"); ch.Author != authorAda || ch.Release == nil || ch.Release.Session != session {
		t.Errorf("with a later staged version: %+v", ch)
	}

	// The hub releases at the deadline: the release names no one.
	if err := f.staging.OpenArea(ctx, "daily", storage.AreaState{}); err != nil {
		t.Fatal(err)
	}
	f.stage(t, "daily", "notes/000003.md", "Daily.\n", authorAda)
	f.stub(t, "daily", "notes/000003.md")
	if _, err := f.staging.Release(ctx, "daily", storage.Author{}); err != nil {
		t.Fatal(err)
	}
	if ch := f.lastChange(t, "notes/000003.md"); ch.Author != authorAda || ch.Release == nil || ch.Release.By != (storage.Author{}) {
		t.Errorf("the release by the hub: %+v", ch)
	}

	// A plain change after the release is the newest change, without a release.
	f.write(t, authorBen, storage.File{Path: "notes/000002.md", Data: []byte("Changed.\n")})
	if ch := f.lastChange(t, "notes/000002.md"); ch.Author != authorBen || ch.Release != nil {
		t.Errorf("the change after the release: %+v", ch)
	}
}

package contract

// Contract tests of the read interface of the archive (if-archive-read, dec-artifacts). They
// start the real office-hub. A deck is a YAML file in the archive, so the tests read it back
// through the interface.

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// A participant reads a file with its content and its version, and lists a directory. The
// version stays the same while the file does not change and differs between files.
func TestArchiveReadFileAndList(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ctx := ctx10(t)
	d1 := render(t, ada, budgetDeck())
	second := budgetDeck()
	second.Title = "Other deck"
	d2 := render(t, ada, second)
	if d1.GetId() != "slides/1" || d2.GetId() != "slides/2" {
		t.Fatalf("decks %q and %q", d1.GetId(), d2.GetId())
	}

	got, err := ada.Archive.ReadFile(ctx, &archivev1.ReadFileRequest{Path: "slides/000001.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	f := got.GetFile()
	if f.GetPath() != "slides/000001.yaml" || !strings.Contains(string(f.GetContent()), "Budget 2027") || f.GetVersion() == "" {
		t.Fatalf("file: %q, %q, version %q", f.GetPath(), f.GetContent(), f.GetVersion())
	}
	again, _ := ada.Archive.ReadFile(ctx, &archivev1.ReadFileRequest{Path: "slides/000001.yaml"})
	if again.GetFile().GetVersion() != f.GetVersion() {
		t.Errorf("version of an unchanged file: %q, then %q", f.GetVersion(), again.GetFile().GetVersion())
	}

	list, err := ada.Archive.ListFiles(ctx, &archivev1.ListFilesRequest{Dir: "slides"})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	versions := map[string]string{}
	for _, fi := range list.GetFiles() {
		paths = append(paths, fi.GetPath())
		versions[fi.GetPath()] = fi.GetVersion()
	}
	// The rendered decks are HTML files and are not artifacts.
	if strings.Join(paths, " ") != "slides/000001.yaml slides/000002.yaml" {
		t.Fatalf("list: %v", paths)
	}
	if versions["slides/000001.yaml"] != f.GetVersion() || versions["slides/000001.yaml"] == versions["slides/000002.yaml"] {
		t.Errorf("versions in the list: %v, file version %q", versions, f.GetVersion())
	}
	empty, err := ada.Archive.ListFiles(ctx, &archivev1.ListFilesRequest{Dir: "nothing"})
	if err != nil || len(empty.GetFiles()) != 0 {
		t.Errorf("list of a directory without files: %v, %v", empty, err)
	}
}

// Paths that cannot be read, and callers that may not read.
func TestArchiveReadRefuses(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ctx := ctx10(t)
	render(t, ada, budgetDeck())
	read := func(c contracttest.Clients, p string) error {
		_, err := c.Archive.ReadFile(ctx, &archivev1.ReadFileRequest{Path: p})
		return err
	}

	wantCode(t, "unknown file", read(ada.Clients, "slides/000009.yaml"), connect.CodeNotFound)
	wantCode(t, "HTML file", read(ada.Clients, "slides/000001.html"), connect.CodeInvalidArgument)
	wantCode(t, "no directory", read(ada.Clients, "plan.md"), connect.CodeInvalidArgument)
	wantCode(t, "path outside the repository", read(ada.Clients, "../etc/x.md"), connect.CodeInvalidArgument)
	wantCode(t, "hidden path", read(ada.Clients, ".git/x.md"), connect.CodeInvalidArgument)
	// The session logs hold the original of off-the-record sentences.
	wantCode(t, "session log", read(ada.Clients, "sessions/s1/log.yaml"), connect.CodeInvalidArgument)
	_, err := ada.Archive.ListFiles(ctx, &archivev1.ListFilesRequest{Dir: "sessions"})
	wantCode(t, "list of the session logs", err, connect.CodeInvalidArgument)

	// No token, and the token of a stage link.
	anon := srv.Anonymous(t)
	wantCode(t, "no token", read(anon, "slides/000001.yaml"), connect.CodeUnauthenticated)
	sid := "team/1"
	join(t, ada.Clients, sid)
	link, err := ada.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	stage := srv.WithToken(t, stageToken(t, link.GetUrl()))
	wantCode(t, "stage link", read(stage, "slides/000001.yaml"), connect.CodePermissionDenied)
}

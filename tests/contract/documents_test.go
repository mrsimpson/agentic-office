package contract

// Contract tests of the documents (if-document-api, dec-artifacts). They start the real
// office-hub, write documents through the interface and read them back through the archive read
// interface and with git.

import (
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

func readDoc(t *testing.T, a *contracttest.Actor, path string) *archivev1.File {
	t.Helper()
	res, err := a.Archive.ReadFile(ctx10(t), &archivev1.ReadFileRequest{Path: path})
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return res.GetFile()
}

// A document is a Markdown file with the participant as author of its commit. The version that
// the call returns is the version that the archive gives.
func TestDocumentCreateAndRead(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human)
	ctx := ctx10(t)
	cmd := coreapi.NewCommandID()
	req := &documentv1.CreateDocumentRequest{Path: "documents/plan/budget.md", Content: "# Budget\n", CommandId: cmd}
	res, err := ada.Document.CreateDocument(ctx, req)
	if err != nil || res.GetDuplicate() {
		t.Fatalf("create: %v, %v", res, err)
	}
	f := readDoc(t, ada, "documents/plan/budget.md")
	if string(f.GetContent()) != "# Budget\n" || f.GetVersion() != res.GetDocument().GetVersion() {
		t.Fatalf("read %q version %q, create gave %q", f.GetContent(), f.GetVersion(), res.GetDocument().GetVersion())
	}
	// The identity of the caller is the author of the commit.
	if got := strings.TrimSpace(contracttest.Git(t, srv.ArchiveDir(), "log", "-1", "--format=%an <%ae>|%cn", "--", "documents/plan/budget.md")); got != "Ada Lovelace <ada@office.invalid>|office-hub" {
		t.Errorf("author of the commit: %q", got)
	}
	// A repeated command has no extra effect.
	again, err := ada.Document.CreateDocument(ctx, req)
	if err != nil || !again.GetDuplicate() || again.GetDocument().GetVersion() != f.GetVersion() {
		t.Errorf("repeated create: %v, %v", again, err)
	}
	if n := strings.Count(contracttest.Git(t, srv.ArchiveDir(), "log", "--format=%H", "--", "documents"), "\n"); n != 1 {
		t.Errorf("%d commits for one create", n)
	}
	// The same command ID with other content is an error.
	other := &documentv1.CreateDocumentRequest{Path: req.Path, Content: "other", CommandId: cmd}
	_, err = ada.Document.CreateDocument(ctx, other)
	wantCode(t, "command ID of another command", err, connect.CodeAlreadyExists)
	// The path is taken.
	_, err = ada.Document.CreateDocument(ctx, &documentv1.CreateDocumentRequest{Path: req.Path, Content: "x", CommandId: coreapi.NewCommandID()})
	wantCode(t, "create over an existing document", err, connect.CodeAlreadyExists)
}

// Of two changes with the same base the first one wins. The second is refused with aborted and
// the current version, and it writes nothing. Everyone may write.
func TestDocumentChangeNeedsCurrentBase(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", identityv1.ActorKind_ACTOR_KIND_AGENT)
	ctx := ctx10(t)
	created, err := ada.Document.CreateDocument(ctx, &documentv1.CreateDocumentRequest{Path: "documents/a.md", Content: "one", CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	base := created.GetDocument().GetVersion()
	change := func(a *contracttest.Actor, content, base string) (*documentv1.ChangeDocumentResponse, error) {
		return a.Document.ChangeDocument(ctx, &documentv1.ChangeDocumentRequest{Path: "documents/a.md", Content: content,
			CommandId: coreapi.NewCommandID(), BaseVersion: base})
	}
	first, err := change(ada, "two", base)
	if err != nil {
		t.Fatal(err)
	}
	_, err = change(bot, "three", base)
	wantCode(t, "change with a stale base", err, connect.CodeAborted)
	if !strings.Contains(err.Error(), first.GetDocument().GetVersion()) {
		t.Errorf("the error does not give the current version %s: %v", first.GetDocument().GetVersion(), err)
	}
	if got := readDoc(t, ada, "documents/a.md"); string(got.GetContent()) != "two" || got.GetVersion() != first.GetDocument().GetVersion() {
		t.Fatalf("document after a refused change: %q", got.GetContent())
	}
	// The agent reads again and succeeds; the commit names the agent.
	if _, err := change(bot, "three", first.GetDocument().GetVersion()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(contracttest.Git(t, srv.ArchiveDir(), "log", "-1", "--format=%an", "--", "documents/a.md")); got != "Bot" {
		t.Errorf("author of the last change: %q", got)
	}
	_, err = ada.Document.ChangeDocument(ctx, &documentv1.ChangeDocumentRequest{Path: "documents/none.md", Content: "x",
		CommandId: coreapi.NewCommandID(), BaseVersion: base})
	wantCode(t, "change of an unknown document", err, connect.CodeNotFound)
	_, err = ada.Document.ChangeDocument(ctx, &documentv1.ChangeDocumentRequest{Path: "documents/a.md", Content: "x", CommandId: coreapi.NewCommandID()})
	wantCode(t, "change without a base", err, connect.CodeInvalidArgument)
}

// Paths, content and callers that are refused.
func TestDocumentRefuses(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ctx := ctx10(t)
	create := func(c contracttest.Clients, path, content string) error {
		_, err := c.Document.CreateDocument(ctx, &documentv1.CreateDocumentRequest{Path: path, Content: content, CommandId: coreapi.NewCommandID()})
		return err
	}
	for _, p := range []string{"", "plan.md", "documents/plan.txt", "slides/1.md", "documents/../x.md", "documents/.hidden.md", "/documents/a.md"} {
		wantCode(t, "path "+p, create(ada.Clients, p, "x"), connect.CodeInvalidArgument)
	}
	wantCode(t, "empty content", create(ada.Clients, "documents/a.md", ""), connect.CodeInvalidArgument)
	wantCode(t, "content that is too large", create(ada.Clients, "documents/a.md", strings.Repeat("x", 1<<20+1)), connect.CodeInvalidArgument)
	_, err := ada.Document.CreateDocument(ctx, &documentv1.CreateDocumentRequest{Path: "documents/a.md", Content: "x"})
	wantCode(t, "no command_id", err, connect.CodeInvalidArgument)
	wantCode(t, "no token", create(srv.Anonymous(t), "documents/a.md", "x"), connect.CodeUnauthenticated)
	join(t, ada.Clients, "s")
	link, err := ada.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, "stage link", create(srv.WithToken(t, stageToken(t, link.GetUrl())), "documents/a.md", "x"), connect.CodePermissionDenied)
}

// A call that names a session puts artifact.created or artifact.changed into its log, for the
// participant. A participant who has not joined gets an error. A repeated command does not add
// another event.
func TestDocumentEventsInSession(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	bob := m.register("Bob", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	ctx := m.ctx()
	a := m.actors[ada]
	create := &documentv1.CreateDocumentRequest{Path: "documents/notes.md", Content: "one", CommandId: coreapi.NewCommandID(), SessionId: m.sid}
	res, err := a.Document.CreateDocument(ctx, create)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Document.CreateDocument(ctx, create); err != nil {
		t.Fatal(err)
	}
	chg, err := a.Document.ChangeDocument(ctx, &documentv1.ChangeDocumentRequest{Path: "documents/notes.md", Content: "two",
		CommandId: coreapi.NewCommandID(), SessionId: m.sid, BaseVersion: res.GetDocument().GetVersion()})
	if err != nil {
		t.Fatal(err)
	}
	var got []*sessionstreamv1.Event
	for _, ev := range m.events() {
		if strings.HasPrefix(ev.GetType(), "artifact.") {
			got = append(got, ev)
		}
	}
	if len(got) != 2 {
		t.Fatalf("%d artifact events, want 2: %v", len(got), got)
	}
	c, ch := got[0].GetArtifactCreated(), got[1].GetArtifactChanged()
	if got[0].GetActorParticipantId() != ada || c.GetPath() != "documents/notes.md" || c.GetVersion() != res.GetDocument().GetVersion() ||
		ch.GetPath() != "documents/notes.md" || ch.GetVersion() != chg.GetDocument().GetVersion() {
		t.Errorf("events: %v", got)
	}
	// Bob has not joined.
	_, err = bob.Document.CreateDocument(ctx, &documentv1.CreateDocumentRequest{Path: "documents/bob.md", Content: "x",
		CommandId: coreapi.NewCommandID(), SessionId: m.sid})
	if err == nil {
		t.Fatalf("a participant who has not joined wrote into the session")
	}
}

package contract

// Contract tests of the staged documents and slide decks (concept-session-release) through the
// documents (if-document-api), the slides (if-slides-api), the archive read interface
// (if-archive-read), the slides check (if-slides-check) and the stage (if-stage-web), against the
// real office-hub. A document or a deck that names a session that is not released waits in the
// staging area of the session; the artifact repository holds its stub.

import (
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"golang.org/x/net/html"
	"google.golang.org/protobuf/proto"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	slidescheck "github.com/mrsimpson/agentic-office/hub/slides/check"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// createDocIn creates a document as a, naming session (empty: none).
func createDocIn(t *testing.T, a *contracttest.Actor, session, path, content string) (*documentv1.DocumentVersion, error) {
	t.Helper()
	res, err := a.Document.CreateDocument(ctx10(t), &documentv1.CreateDocumentRequest{Path: path, Content: content,
		CommandId: coreapi.NewCommandID(), SessionId: session})
	return res.GetDocument(), err
}

// changeDocIn changes a document as a, naming session (empty: none).
func changeDocIn(t *testing.T, a *contracttest.Actor, session, path, content, base string) (*documentv1.DocumentVersion, error) {
	t.Helper()
	res, err := a.Document.ChangeDocument(ctx10(t), &documentv1.ChangeDocumentRequest{Path: path, Content: content,
		CommandId: coreapi.NewCommandID(), SessionId: session, BaseVersion: base})
	return res.GetDocument(), err
}

// readFile reads a file of the archive as a.
func readFile(t *testing.T, a *contracttest.Actor, path string) (*archivev1.File, error) {
	t.Helper()
	res, err := a.Archive.ReadFile(ctx10(t), &archivev1.ReadFileRequest{Path: path})
	return res.GetFile(), err
}

// listed returns the files of a directory as a sees them, by path.
func listed(t *testing.T, a *contracttest.Actor, dir string) map[string]*archivev1.FileInfo {
	t.Helper()
	res, err := a.Archive.ListFiles(ctx10(t), &archivev1.ListFilesRequest{Dir: dir})
	if err != nil {
		t.Fatalf("list %s as %s: %v", dir, a.ParticipantID, err)
	}
	out := map[string]*archivev1.FileInfo{}
	for _, f := range res.GetFiles() {
		out[f.GetPath()] = f
	}
	return out
}

// renderIn renders a deck as a that names session.
func renderIn(t *testing.T, a *contracttest.Actor, session string, deck *slidesv1.RenderDeckRequest) (*slidesv1.Deck, error) {
	t.Helper()
	req := proto.Clone(deck).(*slidesv1.RenderDeckRequest)
	req.CommandId, req.AuthorParticipantId, req.SessionId = coreapi.NewCommandID(), a.ParticipantID, session
	res, err := a.Slides.RenderDeck(ctx10(t), req)
	return res.GetDeck(), err
}

// deckOf returns a deck as a sees it.
func deckOf(t *testing.T, a *contracttest.Actor, id string) (*slidesv1.Deck, error) {
	t.Helper()
	res, err := a.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: id})
	return res.GetDeck(), err
}

// isDeckStub reports whether a deck is the stub of deck id: only the ID and stub are set.
func isDeckStub(id string, d *slidesv1.Deck) bool {
	return d.GetId() == id && d.GetStub() && !d.GetStaged() && d.GetTitle() == "" && len(d.GetSlides()) == 0 &&
		d.GetSessionId() == "" && d.GetAuthorParticipantId() == "" && len(d.GetContent()) == 0
}

// TestStagedDocuments: only a participant names a session. A new document of a session that is
// not released goes into the staging area, and its stub into the artifact repository: the same
// content for every document, without session or time, by the hub, with a commit message that
// says nothing about the document. The readers of the session and the tenant admins read the
// staged document; every other member reads the stub. Every participant changes or removes it
// before the release, naming the session; other changes are refused. A released document changes
// in the artifact repository and is not held. The release moves the documents.
func TestStagedDocuments(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	admin := m.reader // the first participant is the tenant admin
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	carl := m.register("Carl", kHuman).ParticipantID // a member who never joined
	other := "s-" + coreapi.NewCommandID()
	joinAll(t, other, m.a(ada))

	// Only a participant names a session; a session that does not exist gets the same answer.
	_, err := createDocIn(t, m.a(carl), m.sid, "documents/carl.md", "# Carl\n")
	wantCode(t, "a document of a session carl did not join", err, connect.CodePermissionDenied)
	_, err = createDocIn(t, m.a(ada), "s-nowhere", "documents/nowhere.md", "# Nowhere\n")
	wantCode(t, "a document of a session that does not exist", err, connect.CodePermissionDenied)

	plan, err := createDocIn(t, m.a(ada), m.sid, "documents/plan.md", "# Plan\n\nWe meet on Monday.\n")
	if err != nil || !plan.GetStaged() {
		t.Fatalf("staged document: %v, %v", plan, err)
	}
	draft, err := createDocIn(t, m.a(bot), m.sid, "documents/draft.md", "# Draft\n")
	if err != nil || !draft.GetStaged() {
		t.Fatalf("staged document of the bot: %v, %v", draft, err)
	}
	if got := stagedFile(t, m.srv, "documents/plan.md"); got != "# Plan\n\nWe meet on Monday.\n" {
		t.Errorf("the staged file: %q", got)
	}
	stub := m.artifact("documents/plan.md")
	if !isDocumentStub(stub) || m.artifact("documents/draft.md") != stub {
		t.Errorf("the artifact repository does not hold the same stub for both documents:\n%s\n%s", stub, m.artifact("documents/draft.md"))
	}
	if y, body := frontMatter(t, "documents/plan.md", stub); y["schema"] != "office.document.v1" || y["stub"] != true || len(y) != 2 ||
		strings.Contains(body, "Monday") {
		t.Errorf("the stub of a document: %v %q", y, body)
	}
	c1, c2 := commitsAt(t, m.srv, "documents/plan.md"), commitsAt(t, m.srv, "documents/draft.md")
	if len(c1) != 1 || len(c2) != 1 || c1[0] != c2[0] || !strings.HasPrefix(c1[0], "office-hub|") {
		t.Errorf("stub commits %v, %v: want one each, the same, by the hub", c1, c2)
	}
	for _, s := range []string{"plan", "Plan", m.sid, ada, "Ada"} {
		if strings.Contains(c1[0], s) {
			t.Errorf("the commit of a stub %q names %q", c1[0], s)
		}
	}

	// The content of a stub is never written, and a taken path is refused.
	_, err = createDocIn(t, m.a(ada), "", "documents/fake.md", stub)
	wantCode(t, "a document with the content of a stub", err, connect.CodeInvalidArgument)
	_, err = createDocIn(t, m.a(carl), "", "documents/plan.md", "# Mine\n")
	wantCode(t, "a document over a stub", err, connect.CodeAlreadyExists)
	_, err = createDocIn(t, m.a(ada), other, "documents/plan.md", "# Mine\n")
	wantCode(t, "a document over a document of another session", err, connect.CodeAlreadyExists)

	// Readers of the session and the tenant admin read the staged document; others the stub.
	for name, a := range map[string]*contracttest.Actor{"ada": m.a(ada), "bot": m.a(bot), "the tenant admin": admin} {
		f, err := readFile(t, a, "documents/plan.md")
		if err != nil || !f.GetStaged() || string(f.GetContent()) != "# Plan\n\nWe meet on Monday.\n" || f.GetVersion() != plan.GetVersion() {
			t.Errorf("the staged document as %s: %v, %v", name, f, err)
		}
		if l := listed(t, a, "documents")["documents/plan.md"]; !l.GetStaged() || l.GetVersion() != plan.GetVersion() {
			t.Errorf("the staged document in the list as %s: %v", name, l)
		}
	}
	f, err := readFile(t, m.a(carl), "documents/plan.md")
	if err != nil || f.GetStaged() || string(f.GetContent()) != stub {
		t.Errorf("the staged document as a member who did not take part: %v, %v", f, err)
	}
	if l := listed(t, m.a(carl), "documents")["documents/plan.md"]; l == nil || l.GetStaged() || l.GetVersion() != f.GetVersion() {
		t.Errorf("the staged document in the list of a member who did not take part: %v", l)
	}

	// Bot changes the document of Ada; the stub stays as it is.
	changed, err := changeDocIn(t, m.a(bot), m.sid, "documents/plan.md", "# Plan\n\nWe meet on Tuesday.\n", plan.GetVersion())
	if err != nil || !changed.GetStaged() {
		t.Fatalf("change of a staged document: %v, %v", changed, err)
	}
	if got := stagedFile(t, m.srv, "documents/plan.md"); got != "# Plan\n\nWe meet on Tuesday.\n" {
		t.Errorf("the staged file after the change: %q", got)
	}
	if c := commitsAt(t, m.srv, "documents/plan.md"); len(c) != 1 {
		t.Errorf("a change of a staged document touched its stub: %v", c)
	}
	refused := func(what, pid, session, base string, code connect.Code) {
		t.Helper()
		_, err := changeDocIn(t, m.a(pid), session, "documents/plan.md", "x", base)
		wantCode(t, "change "+what, err, code)
		_, err = m.a(pid).Document.RemoveDocument(ctx10(t), &documentv1.RemoveDocumentRequest{Path: "documents/plan.md",
			CommandId: coreapi.NewCommandID(), SessionId: session, BaseVersion: base})
		wantCode(t, "remove "+what, err, code)
	}
	refused("naming no session", ada, "", changed.GetVersion(), connect.CodeFailedPrecondition)
	refused("naming another session", ada, other, changed.GetVersion(), connect.CodeFailedPrecondition)
	refused("by a member who did not take part", carl, m.sid, changed.GetVersion(), connect.CodePermissionDenied)
	refused("on a stale base", ada, m.sid, plan.GetVersion(), connect.CodeAborted)
	refused("of the stub version", ada, m.sid, f.GetVersion(), connect.CodeAborted)

	// Ada removes the draft of Bot. Readers no longer find it; others still read the stub.
	rm := &documentv1.RemoveDocumentRequest{Path: "documents/draft.md", CommandId: coreapi.NewCommandID(), SessionId: m.sid,
		BaseVersion: draft.GetVersion()}
	if r, err := m.a(ada).Document.RemoveDocument(ctx10(t), rm); err != nil || r.GetDuplicate() {
		t.Fatalf("remove the staged draft: %v, %v", r, err)
	}
	if r, err := m.a(ada).Document.RemoveDocument(ctx10(t), rm); err != nil || !r.GetDuplicate() {
		t.Errorf("repeated removal: %v, %v", r, err)
	}
	_, err = readFile(t, m.a(bot), "documents/draft.md")
	wantCode(t, "a removed staged document for a reader", err, connect.CodeNotFound)
	if _, ok := listed(t, m.a(bot), "documents")["documents/draft.md"]; ok {
		t.Error("a removed staged document is listed for a reader")
	}
	if f, err := readFile(t, m.a(carl), "documents/draft.md"); err != nil || string(f.GetContent()) != stub {
		t.Errorf("a removed staged document for a member who did not take part: %v, %v", f, err)
	}
	_, err = changeDocIn(t, m.a(ada), m.sid, "documents/draft.md", "x", draft.GetVersion())
	wantCode(t, "change a removed document", err, connect.CodeNotFound)

	// A document without a session is released at once. A change of it that names the session goes
	// to the artifact repository and is not held; it cannot be removed here.
	free, err := createDocIn(t, m.a(ada), "", "documents/free.md", "# Free\n")
	if err != nil || free.GetStaged() {
		t.Fatalf("a document without a session: %v, %v", free, err)
	}
	freeChanged, err := changeDocIn(t, m.a(bot), m.sid, "documents/free.md", "# Free\n\nChanged in the meeting.\n", free.GetVersion())
	if err != nil || freeChanged.GetStaged() {
		t.Fatalf("change of a released document in the meeting: %v, %v", freeChanged, err)
	}
	if got := m.artifact("documents/free.md"); got != "# Free\n\nChanged in the meeting.\n" {
		t.Errorf("the released document after a change in the meeting: %q", got)
	}
	if f, err := readFile(t, m.a(carl), "documents/free.md"); err != nil || f.GetStaged() || f.GetVersion() != freeChanged.GetVersion() {
		t.Errorf("the changed released document for every member: %v, %v", f, err)
	}
	_, err = changeDocIn(t, m.a(carl), m.sid, "documents/free.md", "x", freeChanged.GetVersion())
	wantCode(t, "a change that names a session the member did not join", err, connect.CodePermissionDenied)
	_, err = m.a(ada).Document.RemoveDocument(ctx10(t), &documentv1.RemoveDocumentRequest{Path: "documents/free.md",
		CommandId: coreapi.NewCommandID(), SessionId: m.sid, BaseVersion: freeChanged.GetVersion()})
	wantCode(t, "remove a released document", err, connect.CodeFailedPrecondition)

	// The release moves the staged document over its stub and removes the stub of the removed one.
	m.end(ada)
	if _, err := m.releaseBy(ada); err != nil {
		t.Fatal(err)
	}
	if got := m.artifact("documents/plan.md"); got != "# Plan\n\nWe meet on Tuesday.\n" {
		t.Errorf("the released document: %q", got)
	}
	if got := m.artifact("documents/draft.md"); got != "" {
		t.Errorf("the stub of the removed document is still there:\n%s", got)
	}
	if f, err := readFile(t, m.a(carl), "documents/plan.md"); err != nil || f.GetStaged() || f.GetVersion() != changed.GetVersion() {
		t.Errorf("the released document for every member: %v, %v", f, err)
	}
	// After the release, a change that names the session goes to the artifact repository, and a
	// new document of the session is released at once.
	after, err := changeDocIn(t, m.a(bot), m.sid, "documents/plan.md", "# Plan\n\nDone.\n", changed.GetVersion())
	if err != nil || after.GetStaged() || m.artifact("documents/plan.md") != "# Plan\n\nDone.\n" {
		t.Errorf("a change after the release: %v, %v", after, err)
	}
	late, err := createDocIn(t, m.a(bot), m.sid, "documents/late.md", "# Late\n")
	if err != nil || late.GetStaged() || m.artifact("documents/late.md") != "# Late\n" {
		t.Errorf("a document of a released session: %v, %v", late, err)
	}
}

// TestStagedDocumentAgainAfterRemoval: a participant removes a staged document and creates it
// again in the same session; its stub stays as it is.
func TestStagedDocumentAgainAfterRemoval(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	first, err := createDocIn(t, m.a(ada), m.sid, "documents/again.md", "# First\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.a(ada).Document.RemoveDocument(ctx10(t), &documentv1.RemoveDocumentRequest{Path: "documents/again.md",
		CommandId: coreapi.NewCommandID(), SessionId: m.sid, BaseVersion: first.GetVersion()}); err != nil {
		t.Fatal(err)
	}
	second, err := createDocIn(t, m.a(ada), m.sid, "documents/again.md", "# Second\n")
	if err != nil || !second.GetStaged() {
		t.Fatalf("create the removed document again: %v, %v", second, err)
	}
	if f, err := readFile(t, m.a(ada), "documents/again.md"); err != nil || string(f.GetContent()) != "# Second\n" {
		t.Errorf("the document created again: %v, %v", f, err)
	}
	if c := commitsAt(t, m.srv, "documents/again.md"); len(c) != 1 {
		t.Errorf("commits of the stub: %v, want one", c)
	}
}

// TestStagedDecks: a deck that names a session that is not released goes into the staging area,
// and its stub into the artifact repository. The slides check accepts the stub as a second form of
// the schema. The readers of the session and the tenant admins get the staged deck; every other
// member gets the stub. Every participant changes or removes it before the release, naming the
// session; other changes are refused. The release moves the decks, and a restart keeps them.
func TestStagedDecks(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	admin := m.reader
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	carl := m.register("Carl", kHuman).ParticipantID
	other := "s-" + coreapi.NewCommandID()
	joinAll(t, other, m.a(ada))

	_, err := renderIn(t, m.a(carl), m.sid, budgetDeck())
	wantCode(t, "a deck of a session carl did not join", err, connect.CodePermissionDenied)
	_, err = renderIn(t, m.a(ada), "s-nowhere", budgetDeck())
	wantCode(t, "a deck of a session that does not exist", err, connect.CodePermissionDenied)

	d1, err := renderIn(t, m.a(ada), m.sid, budgetDeck())
	if err != nil || d1.GetId() != "slides/1" || !d1.GetStaged() || d1.GetSessionId() != m.sid || len(d1.GetContent()) == 0 {
		t.Fatalf("staged deck: %v, %v", d1, err)
	}
	d2, err := renderIn(t, m.a(bot), m.sid, &slidesv1.RenderDeckRequest{Slides: []*slidesv1.Slide{{Title: "Draft"}}})
	if err != nil || !d2.GetStaged() {
		t.Fatalf("staged deck of the bot: %v, %v", d2, err)
	}
	free, err := renderIn(t, m.a(carl), "", &slidesv1.RenderDeckRequest{Slides: []*slidesv1.Slide{{Title: "Free"}}})
	if err != nil || free.GetStaged() || free.GetId() != "slides/3" {
		t.Fatalf("a deck without a session: %v, %v", free, err)
	}

	// The staging area holds the deck with its session; the artifact repository holds the stub.
	if got := stagedFile(t, m.srv, "slides/000001.yaml"); !strings.Contains(got, m.sid) || !strings.Contains(got, "Budget 2027") {
		t.Errorf("the staged deck file:\n%s", got)
	}
	stub := string(slidescheck.Stub())
	for _, p := range []string{"slides/000001.yaml", "slides/000002.yaml"} {
		if got := m.artifact(p); got != stub {
			t.Errorf("%s in the artifact repository is not the stub of a deck:\n%s", p, got)
		}
	}
	if strings.Contains(stub, m.sid) || strings.Contains(stub, "time") || strings.Contains(stub, ada) {
		t.Errorf("the stub names the session, a time or an author:\n%s", stub)
	}
	c1, c2 := commitsAt(t, m.srv, "slides/000001.yaml"), commitsAt(t, m.srv, "slides/000002.yaml")
	if len(c1) != 1 || len(c2) != 1 || c1[0] != c2[0] || !strings.HasPrefix(c1[0], "office-hub|") || strings.Contains(c1[0], "Budget") {
		t.Errorf("stub commits %v, %v: want one each, the same, by the hub, saying nothing about the deck", c1, c2)
	}
	// The slides check accepts the stub, byte for byte, and refuses a deck file that stores the
	// fields staged or stub.
	if !slidescheck.IsStub([]byte(stub)) || len(slidescheck.Validate(slidescheck.SchemaDeck, "slides/000007.yaml", []byte(stub))) != 0 {
		t.Error("the slides check refuses the stub of a deck")
	}
	if len(slidescheck.Validate(slidescheck.SchemaDeck, "slides/000007.yaml", []byte(stub+"\n"))) == 0 {
		t.Error("the slides check accepts a stub with another content")
	}
	withStaged := strings.Replace(stagedFile(t, m.srv, "slides/000001.yaml"), "schema: office.slides.v1\n", "schema: office.slides.v1\nstaged: true\n", 1)
	if len(slidescheck.Validate(slidescheck.SchemaDeck, "slides/000001.yaml", []byte(withStaged))) == 0 {
		t.Error("the slides check accepts a deck that stores the field staged")
	}

	// Readers and the tenant admin get the staged deck; others the stub.
	for name, a := range map[string]*contracttest.Actor{"ada": m.a(ada), "bot": m.a(bot), "the tenant admin": admin} {
		if d, err := deckOf(t, a, "slides/1"); err != nil || !d.GetStaged() || d.GetTitle() != "Budget 2027" || string(d.GetContent()) != string(d1.GetContent()) {
			t.Errorf("the staged deck as %s: %v", name, err)
		}
	}
	if d, err := deckOf(t, m.a(carl), "slides/1"); err != nil || !isDeckStub("slides/1", d) {
		t.Errorf("the staged deck as a member who did not take part: %v, %v", d, err)
	}

	// Bot changes the deck of Ada: Bot becomes the author; the stub stays.
	change := &slidesv1.ChangeDeckRequest{CommandId: coreapi.NewCommandID(), Id: "slides/1", SessionId: m.sid, Title: "Budget 2028",
		Slides: []*slidesv1.Slide{{Title: "Plan", Bullets: []string{"Hire three people"}}}, AuthorParticipantId: bot}
	res, err := m.a(bot).Slides.ChangeDeck(ctx10(t), change)
	if err != nil || res.GetDuplicate() || res.GetDeck().GetTitle() != "Budget 2028" || res.GetDeck().GetAuthorParticipantId() != bot ||
		res.GetDeck().GetAuthorKind() != slidesv1.AuthorKind_AUTHOR_KIND_AGENT || !res.GetDeck().GetStaged() ||
		res.GetDeck().GetCommandId() != d1.GetCommandId() || !strings.Contains(string(res.GetDeck().GetContent()), "Hire three people") {
		t.Fatalf("changed deck: %v, %v", res, err)
	}
	if !strings.Contains(stagedFile(t, m.srv, "slides/000001.yaml"), "Budget 2028") {
		t.Error("the staged file was not changed")
	}
	if c := commitsAt(t, m.srv, "slides/000001.yaml"); len(c) != 1 {
		t.Errorf("a change of a staged deck touched its stub: %v", c)
	}
	if again, err := m.a(bot).Slides.ChangeDeck(ctx10(t), change); err != nil || !again.GetDuplicate() {
		t.Errorf("repeated change: %v, %v", again, err)
	}
	refused := func(what, pid, id, session string, code connect.Code) {
		t.Helper()
		_, err := m.a(pid).Slides.ChangeDeck(ctx10(t), &slidesv1.ChangeDeckRequest{CommandId: coreapi.NewCommandID(), Id: id, SessionId: session,
			Slides: []*slidesv1.Slide{{Title: "x"}}, AuthorParticipantId: pid})
		wantCode(t, "change "+what, err, code)
		_, err = m.a(pid).Slides.RemoveDeck(ctx10(t), &slidesv1.RemoveDeckRequest{CommandId: coreapi.NewCommandID(), Id: id, SessionId: session,
			AuthorParticipantId: pid})
		wantCode(t, "remove "+what, err, code)
	}
	refused("naming no session", ada, "slides/1", "", connect.CodeFailedPrecondition)
	refused("naming another session", ada, "slides/1", other, connect.CodeFailedPrecondition)
	refused("by a member who did not take part", carl, "slides/1", m.sid, connect.CodePermissionDenied)
	refused("of a released deck", carl, free.GetId(), "", connect.CodeFailedPrecondition)
	refused("of a deck that does not exist", ada, "slides/99", m.sid, connect.CodeNotFound)

	// Ada removes the deck of Bot. Readers no longer find it; others still get the stub.
	rm := &slidesv1.RemoveDeckRequest{CommandId: coreapi.NewCommandID(), Id: "slides/2", SessionId: m.sid, AuthorParticipantId: ada}
	if r, err := m.a(ada).Slides.RemoveDeck(ctx10(t), rm); err != nil || r.GetDuplicate() {
		t.Fatalf("remove the deck: %v, %v", r, err)
	}
	if r, err := m.a(ada).Slides.RemoveDeck(ctx10(t), rm); err != nil || !r.GetDuplicate() {
		t.Errorf("repeated removal: %v, %v", r, err)
	}
	_, err = deckOf(t, m.a(bot), "slides/2")
	wantCode(t, "a removed deck for a reader", err, connect.CodeNotFound)
	if d, err := deckOf(t, m.a(carl), "slides/2"); err != nil || !isDeckStub("slides/2", d) {
		t.Errorf("a removed deck for a member who did not take part: %v, %v", d, err)
	}

	// The staged deck survives a restart.
	m.restart(false, nil, "-release-grace-period", "1h")
	if d, err := deckOf(t, m.a(ada), "slides/1"); err != nil || !d.GetStaged() || d.GetTitle() != "Budget 2028" {
		t.Errorf("the staged deck after a restart: %v, %v", d, err)
	}
	if d, err := deckOf(t, m.a(carl), "slides/1"); err != nil || !isDeckStub("slides/1", d) {
		t.Errorf("the stub after a restart: %v, %v", d, err)
	}

	// The release moves the deck over its stub and removes the stub of the removed deck.
	m.end(ada)
	if _, err := m.releaseBy(ada); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 15*time.Second, "the released deck for every member", func() bool {
		d, err := deckOf(t, m.a(carl), "slides/1")
		return err == nil && !d.GetStaged() && !d.GetStub() && d.GetTitle() == "Budget 2028" && d.GetSessionId() == m.sid
	})
	if got := m.artifact("slides/000002.yaml"); got != "" {
		t.Errorf("the stub of the removed deck is still there:\n%s", got)
	}
	waitUntil(t, 15*time.Second, "the removed deck gone", func() bool {
		_, err := deckOf(t, m.a(carl), "slides/2")
		return connect.CodeOf(err) == connect.CodeNotFound
	})
	refused("after the release", ada, "slides/1", m.sid, connect.CodeFailedPrecondition)
	// A deck that names the released session is released at once.
	late, err := renderIn(t, m.a(bot), m.sid, &slidesv1.RenderDeckRequest{Slides: []*slidesv1.Slide{{Title: "Late"}}})
	if err != nil || late.GetStaged() || late.GetId() != "slides/4" {
		t.Errorf("a deck of a released session: %v, %v", late, err)
	}
}

// TestStageShowsStagedArtifactsOfItsSession: the stage of a session shows a staged document, note
// and deck of its own session with their content; a staged document and deck of another session
// show as their stubs.
func TestStageShowsStagedArtifactsOfItsSession(t *testing.T) {
	m := newStageMeeting(t, "-release-grace-period", "1h")
	st := openStage(t, m.srv, m.link)
	bot := m.joinAs("Bot", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT, false)
	a := m.actors[bot]
	other := "s-" + coreapi.NewCommandID()
	joinAll(t, other, a)

	focus := func(id string) {
		t.Helper()
		if _, err := m.s(bot).FocusArtifact(m.ctx(), &sessioncommandsv1.FocusArtifactRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
			ParticipantId: bot, ArtifactId: id}); err != nil {
			t.Fatal(err)
		}
	}
	srcdoc := func(n *html.Node) string {
		for _, f := range findAll(n, byTag("iframe")) {
			s, _ := attr(f, "srcdoc")
			return s
		}
		return ""
	}

	// A document of its own session.
	if d, err := createDocIn(t, a, m.sid, "documents/own.md", "# Own *plan*\n"); err != nil || !d.GetStaged() {
		t.Fatalf("staged document: %v, %v", d, err)
	}
	focus("documents/own.md")
	st.wait("artifact", "the staged document of the session", func(n *html.Node) bool { return strings.Contains(srcdoc(n), "<em>plan</em>") })

	// A deck of its own session.
	deck, err := renderIn(t, a, m.sid, budgetDeck())
	if err != nil || !deck.GetStaged() {
		t.Fatalf("staged deck: %v, %v", deck, err)
	}
	focus(deck.GetId())
	st.wait("artifact", "the staged deck of the session", func(n *html.Node) bool { return srcdoc(n) == string(deck.GetContent()) })

	// A document of another session shows its stub.
	if d, err := createDocIn(t, a, other, "documents/other.md", "# Secret *plan*\n"); err != nil || !d.GetStaged() {
		t.Fatalf("staged document of another session: %v, %v", d, err)
	}
	focus("documents/other.md")
	n := st.wait("artifact", "the stub of a document of another session", func(n *html.Node) bool {
		return strings.Contains(srcdoc(n), "waits for the release")
	})
	if strings.Contains(srcdoc(n), "Secret") {
		t.Errorf("the stage shows a staged document of another session:\n%s", srcdoc(n))
	}

	// A deck of another session shows a note.
	otherDeck, err := renderIn(t, a, other, &slidesv1.RenderDeckRequest{Slides: []*slidesv1.Slide{{Title: "Secret plan"}}})
	if err != nil {
		t.Fatal(err)
	}
	focus(otherDeck.GetId())
	n = st.wait("artifact", "a note for a deck of another session", func(n *html.Node) bool {
		return len(findAll(n, byTag("iframe"))) == 0 && strings.Contains(text(n), "waits for the release")
	})
	if strings.Contains(text(n), "Secret") {
		t.Errorf("the stage shows a staged deck of another session: %q", text(n))
	}
}

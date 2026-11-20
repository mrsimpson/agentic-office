package contract

// Contract tests of the discard of staged artifacts that the release kept back (issue #231,
// concept-session-release) through the removal of the type interfaces (if-note-api,
// if-slides-api, if-document-api) and the portal (if-portal-web), against the real office-hub.

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// unreleased returns the staged files of the session that its release kept back, as pid reads them.
func (m *meeting) unreleased(pid string) []string {
	m.t.Helper()
	rel, err := m.s(pid).GetRelease(m.ctx(), &sessioncommandsv1.GetReleaseRequest{SessionId: m.sid})
	if err != nil {
		m.t.Fatal(err)
	}
	return rel.GetUnreleased()
}

// A note, a deck and a document of a session are staged; while the hub is down, Ben pushes his own
// files at their paths. The release keeps the three back. Every participant of the session, an
// agent too, discards them through the removal of their type, naming the session; a member who did
// not take part may not. A move by hand is a create that names the session, which now goes to the
// artifact repository, then a discard. The portal discards the last one with its form, and the
// area goes with it. From then on, the readers of the session read Ben's files.
func TestDiscardKeptArtifacts(t *testing.T) {
	args := []string{"-release-grace-period", "1h"}
	m := newMeetingArgs(t, args...)
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	dora := m.register("Dora", kHuman).ParticipantID // a member who never joined
	m.say(ada, "We meet on Monday.")
	note := m.note(ada, m.sid, "The agent's note.")
	deck, err := renderIn(t, m.a(ada), m.sid, &slidesv1.RenderDeckRequest{Title: "Agent plan", Slides: []*slidesv1.Slide{{Title: "One"}}})
	if err != nil || !deck.GetStaged() {
		t.Fatalf("stage a deck: %v, %v", deck, err)
	}
	m.stageDocument(ada, "documents/taken.md", "# Taken\n\nThe agent's version.\n")
	m.end(ada)

	notePath := fmt.Sprintf("notes/%06d.md", note.GetId())
	deckNumber, err := strconv.Atoi(strings.TrimPrefix(deck.GetId(), "slides/"))
	if err != nil {
		t.Fatal(err)
	}
	deckPath := fmt.Sprintf("slides/%06d.yaml", deckNumber)
	stagedNote := contracttest.Git(t, m.staging(), "show", "main:"+notePath)
	stagedDeck := contracttest.Git(t, m.staging(), "show", "main:"+deckPath)
	m.srv.Kill()
	contracttest.Edit(t, m.srv.ArchiveDir(), "Ben's own files", func(work string) {
		benNote := strings.ReplaceAll(strings.ReplaceAll(stagedNote, note.GetCommandId(), "ben-note"), "The agent's note.", "Ben's note.")
		contracttest.WriteFile(t, work, notePath, benNote)
		benDeck := strings.ReplaceAll(strings.ReplaceAll(stagedDeck, deck.GetCommandId(), "ben-deck"), "Agent plan", "Ben plan")
		contracttest.WriteFile(t, work, deckPath, benDeck)
		contracttest.WriteFile(t, work, "documents/taken.md", "# Ben\n\nBen's own file.\n")
	})
	m.start(nil, args...)
	if _, err := m.releaseBy(ada); err != nil {
		t.Fatalf("release: %v", err)
	}
	want := []string{"documents/taken.md", notePath, deckPath}
	if got := m.unreleased(ada); !slices.Equal(got, want) {
		t.Fatalf("not released %v, want %v", got, want)
	}

	// A member who did not take part may not discard; a removal must name the session.
	removeNote := func(pid, session string) error {
		_, err := m.a(pid).Note.RemoveNote(m.ctx(), &notev1.RemoveNoteRequest{CommandId: coreapi.NewCommandID(), Id: note.GetId(),
			SessionId: session, AuthorParticipantId: pid})
		return err
	}
	if err := removeNote(dora, m.sid); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("discard by a member who did not take part: %v, want permission_denied", err)
	}
	if err := removeNote(ada, ""); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("discard without the session: %v, want failed_precondition", err)
	}
	if f, err := readFile(t, m.a(ada), notePath); err != nil || !f.GetStaged() || !strings.Contains(string(f.GetContent()), "The agent's note.") {
		t.Fatalf("the kept note before the discard: %v, %v", f, err)
	}
	// Ada discards the note: the area forgets it, and she reads Ben's note.
	if err := removeNote(ada, m.sid); err != nil {
		t.Fatalf("discard the kept note: %v", err)
	}
	if got := m.unreleased(ada); !slices.Equal(got, []string{"documents/taken.md", deckPath}) {
		t.Errorf("not released after the discard of the note: %v", got)
	}
	if f, err := readFile(t, m.a(ada), notePath); err != nil || f.GetStaged() || !strings.Contains(string(f.GetContent()), "Ben's note.") {
		t.Errorf("the note after the discard: %v, %v", f, err)
	}
	// The note at the path is Ben's, released, and never changes.
	if err := removeNote(ada, m.sid); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("remove of the released note: %v, want failed_precondition", err)
	}

	// The agent moves the deck by hand: a new deck that names the session is released at once, then
	// the kept deck goes.
	moved, err := renderIn(t, m.a(bot), m.sid, &slidesv1.RenderDeckRequest{Title: "Agent plan", Slides: []*slidesv1.Slide{{Title: "One"}}})
	if err != nil || moved.GetStaged() || moved.GetId() == deck.GetId() {
		t.Fatalf("the deck again at a new path: %v, %v", moved, err)
	}
	if _, err := m.a(bot).Slides.RemoveDeck(m.ctx(), &slidesv1.RemoveDeckRequest{CommandId: coreapi.NewCommandID(), Id: deck.GetId(),
		SessionId: m.sid, AuthorParticipantId: bot}); err != nil {
		t.Fatalf("discard the kept deck: %v", err)
	}
	if d, err := deckOf(t, m.a(ada), deck.GetId()); err != nil || d.GetStaged() || d.GetTitle() != "Ben plan" {
		t.Errorf("the deck after the discard: %v, %v", d, err)
	}
	if contracttest.AreaState(t, m.staging(), m.sid) == nil {
		t.Fatal("the area went before its last kept artifact")
	}

	// The portal discards the document with its form; the area goes with it.
	page := m.srv.URL + "/portal/t/" + contracttest.DefaultTenant + "/s/" + url.PathEscape(m.sid)
	b, _, _ := openPortal(t, m.srv, m.a(ada), contracttest.DefaultTenant)
	_, doc, _ := portalPage(t, b, page)
	form := one(t, doc, "data-discard", "documents/taken.md")
	fields := url.Values{}
	for _, in := range findAll(form, byTag("input")) {
		n, _ := attr(in, "name")
		v, _ := attr(in, "value")
		fields.Set(n, v)
	}
	action, _ := attr(form, "action")
	if fields.Get("path") != "documents/taken.md" || fields.Get("version") == "" || fields.Get("command_id") == "" {
		t.Fatalf("the discard form: %v", fields)
	}
	res, body := post(t, b, m.srv.URL+action, fields)
	doc = parseDoc(t, body)
	if res.StatusCode != http.StatusOK || len(findAll(doc, withAttr("data-notice", ""))) != 1 || len(findAll(doc, withAttr("data-not-released", ""))) != 0 {
		t.Fatalf("after the discard in the portal: status %d\n%s", res.StatusCode, text(doc))
	}
	if contracttest.AreaState(t, m.staging(), m.sid) != nil {
		t.Error("the area stays after its last kept artifact went")
	}
	if got := m.unreleased(ada); len(got) != 0 {
		t.Errorf("not released after the last discard: %v", got)
	}
	if f, err := readFile(t, m.a(ada), "documents/taken.md"); err != nil || f.GetStaged() || string(f.GetContent()) != "# Ben\n\nBen's own file.\n" {
		t.Errorf("the document after the discard: %v, %v", f, err)
	}
	// The same form again has no second effect; a new command for the path is refused, because
	// Ben's document is released.
	if res, _ := post(t, b, m.srv.URL+action, fields); res.StatusCode != http.StatusOK {
		t.Errorf("the discard form sent twice: status %d", res.StatusCode)
	}
	fields.Set("command_id", coreapi.NewCommandID())
	if res, _ := post(t, b, m.srv.URL+action, fields); res.StatusCode == http.StatusOK {
		t.Errorf("a discard of the released document: status %d", res.StatusCode)
	}
	if f, err := readFile(t, m.a(dora), "documents/taken.md"); err != nil || f.GetStaged() || string(f.GetContent()) != "# Ben\n\nBen's own file.\n" {
		t.Errorf("the document for a member who did not take part: %v, %v", f, err)
	}
}

package contract

// Contract tests of the slides part (bb-slides) through if-slides-api. They start the real
// office-hub, use only the Connect clients, and read the archive repository with git.

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"
	"go.yaml.in/yaml/v3"
	"golang.org/x/net/html"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const htmlType = "text/html; charset=utf-8"

func budgetDeck() *slidesv1.RenderDeckRequest {
	return &slidesv1.RenderDeckRequest{Title: "Budget 2027", Slides: []*slidesv1.Slide{
		{Title: "Plan", Bullets: []string{"Hire two people", "Open an office in Lyon"}, Notes: "Say the numbers slowly."},
		{Title: "Costs", Bullets: []string{"Rent: 4,000 EUR a month"}},
		{Title: "Questions"},
	}}
}

// render renders deck as a with a new command ID and fails the test on an error.
func render(t *testing.T, a *contracttest.Actor, deck *slidesv1.RenderDeckRequest) *slidesv1.Deck {
	t.Helper()
	req := proto.Clone(deck).(*slidesv1.RenderDeckRequest)
	req.CommandId, req.AuthorParticipantId = coreapi.NewCommandID(), a.ParticipantID
	res, err := a.Slides.RenderDeck(ctx10(t), req)
	if err != nil {
		t.Fatalf("render %q as %s: %v", deck.GetTitle(), a.ParticipantID, err)
	}
	if res.GetDuplicate() {
		t.Fatalf("render %q: duplicate on the first call", deck.GetTitle())
	}
	return res.GetDeck()
}

func getDeck(t *testing.T, a *contracttest.Actor, id string) *slidesv1.Deck {
	t.Helper()
	res, err := a.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: id})
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return res.GetDeck()
}

// page is what a browser sees in an HTML document.
type page struct {
	tags  map[string]int    // element name: count
	attrs map[string]bool   // attribute names
	text  map[string]string // element id: its text, white space joined
	all   string            // all text of the body
}

func parsePage(t *testing.T, b []byte) page {
	t.Helper()
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := page{tags: map[string]int{}, attrs: map[string]bool{}, text: map[string]string{}}
	var textOf func(n *html.Node) string
	textOf = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var s []string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if t := strings.TrimSpace(textOf(c)); t != "" {
				s = append(s, t)
			}
		}
		return strings.Join(s, " ")
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			p.tags[n.Data]++
			for _, a := range n.Attr {
				p.attrs[a.Key] = true
				if a.Key == "id" {
					p.text[a.Val] = textOf(n)
				}
			}
			if n.Data == "body" {
				p.all = textOf(n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return p
}

// TestSlidesRenderAndGet: a deck gets the ID "slides/<n>", keeps its input and its rendered HTML,
// and anyone with a participant token can fetch it by its ID.
func TestSlidesRenderAndGet(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	bot := srv.Participant(t, "bot", "Bot", agent)
	ada := srv.Participant(t, "ada", "Ada", human)

	before := time.Now().Add(-time.Second)
	a := render(t, bot, budgetDeck())
	if a.GetId() != "slides/1" || a.GetMediaType() != htmlType || a.GetAuthorParticipantId() != "bot" ||
		a.GetAuthorKind() != slidesv1.AuthorKind_AUTHOR_KIND_AGENT || a.GetCommandId() == "" ||
		a.GetCreateTime().AsTime().Before(before) || a.GetCreateTime().AsTime().After(time.Now().Add(time.Second)) {
		t.Fatalf("deck: id %q, type %q, author %q (%v), command %q, created %v", a.GetId(), a.GetMediaType(),
			a.GetAuthorParticipantId(), a.GetAuthorKind(), a.GetCommandId(), a.GetCreateTime().AsTime())
	}
	// The deck keeps its input as the client sent it.
	if want := budgetDeck(); a.GetTitle() != want.GetTitle() || !sameSlides(a.GetSlides(), want.GetSlides()) {
		t.Fatalf("input of the deck: %q %v, want %q %v", a.GetTitle(), a.GetSlides(), want.GetTitle(), want.GetSlides())
	}
	if !bytes.HasPrefix(a.GetContent(), []byte("<!DOCTYPE html>")) {
		t.Fatalf("content does not start with a doctype: %.80q", a.GetContent())
	}
	p := parsePage(t, a.GetContent())
	for id, want := range map[string]string{
		"title":   "Budget 2027",
		"slide-1": "Plan Hire two people Open an office in Lyon Say the numbers slowly. Budget 2027 · 1 / 3",
		"slide-2": "Costs Rent: 4,000 EUR a month Budget 2027 · 2 / 3",
		"slide-3": "Questions Budget 2027 · 3 / 3",
	} {
		if p.text[id] != want {
			t.Errorf("#%s: %q, want %q", id, p.text[id], want)
		}
	}
	if p.tags["section"] != 4 || p.tags["li"] != 3 || p.tags["script"] != 0 || p.tags["img"] != 0 || p.tags["link"] != 0 {
		t.Errorf("elements: %v", p.tags)
	}

	// Fetch by ID: the same deck, also for another participant.
	for _, who := range []*contracttest.Actor{bot, ada} {
		if got := getDeck(t, who, "slides/1"); !proto.Equal(got, a) {
			t.Fatalf("get as %s: %v, want %v", who.ParticipantID, got, a)
		}
	}

	// A deck without a title has no title slide; the document title is the first slide.
	b := render(t, ada, &slidesv1.RenderDeckRequest{Slides: []*slidesv1.Slide{{Title: "Only", Bullets: []string{"x"}}}})
	if b.GetId() != "slides/2" || b.GetAuthorParticipantId() != "ada" {
		t.Fatalf("second deck: %q by %q", b.GetId(), b.GetAuthorParticipantId())
	}
	if b.GetAuthorKind() != slidesv1.AuthorKind_AUTHOR_KIND_HUMAN {
		t.Fatalf("author kind of a human: %v", b.GetAuthorKind())
	}
	p = parsePage(t, b.GetContent())
	if _, ok := p.text["title"]; ok || p.text["slide-1"] != "Only x 1 / 1" || p.tags["section"] != 1 {
		t.Fatalf("deck without title: %v %v", p.text, p.tags)
	}
	if !proto.Equal(getDeck(t, bot, "slides/1"), a) {
		t.Fatal("slides/1 changed after another render")
	}
}

// TestSlidesDeterministic: the same deck gives byte-equal content, whoever renders it, with
// another command ID, on another hub (con-no-intelligence). Another deck gives other content.
func TestSlidesDeterministic(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	bot := srv.Participant(t, "bot", "Bot", agent)
	ada := srv.Participant(t, "ada", "Ada", human)

	a1 := render(t, bot, budgetDeck())
	time.Sleep(10 * time.Millisecond)
	a2 := render(t, ada, budgetDeck())
	other := srv2Render(t)
	if a1.GetId() == a2.GetId() {
		t.Fatalf("two renders share the ID %s", a1.GetId())
	}
	if !bytes.Equal(a1.GetContent(), a2.GetContent()) {
		t.Fatalf("same deck, other content:\n%s\n---\n%s", a1.GetContent(), a2.GetContent())
	}
	if !bytes.Equal(a1.GetContent(), other) {
		t.Fatal("same deck on another hub: other content")
	}

	changes := map[string]func(d *slidesv1.RenderDeckRequest){
		"title":          func(d *slidesv1.RenderDeckRequest) { d.Title = "Budget 2028" },
		"slide title":    func(d *slidesv1.RenderDeckRequest) { d.Slides[1].Title = "Cost" },
		"bullet":         func(d *slidesv1.RenderDeckRequest) { d.Slides[0].Bullets[1] = "Open an office in Lille" },
		"bullet order":   func(d *slidesv1.RenderDeckRequest) { b := d.Slides[0].Bullets; b[0], b[1] = b[1], b[0] },
		"notes":          func(d *slidesv1.RenderDeckRequest) { d.Slides[0].Notes = "Say it fast." },
		"one slide less": func(d *slidesv1.RenderDeckRequest) { d.Slides = d.Slides[:2] },
	}
	seen := map[string]string{string(a1.GetContent()): "original"}
	for name, change := range changes {
		d := budgetDeck()
		change(d)
		c := render(t, bot, d).GetContent()
		if prev, ok := seen[string(c)]; ok {
			t.Errorf("%s: same content as %s", name, prev)
		}
		seen[string(c)] = name
	}
}

// srv2Render renders budgetDeck on a fresh hub and returns the content.
func srv2Render(t *testing.T) []byte {
	t.Helper()
	srv := contracttest.StartHub(t, nil)
	return render(t, srv.Participant(t, "carl", "Carl", human), budgetDeck()).GetContent()
}

// TestSlidesIdempotent: a repeated render returns the stored deck and makes no commit; a reused
// command ID with another deck or author fails.
func TestSlidesIdempotent(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	bot := srv.Participant(t, "bot", "Bot", agent)
	ada := srv.Participant(t, "ada", "Ada", human)

	req := budgetDeck()
	req.CommandId, req.AuthorParticipantId = coreapi.NewCommandID(), "bot"
	first, err := bot.Slides.RenderDeck(ctx10(t), req)
	if err != nil || first.GetDuplicate() {
		t.Fatalf("first: %v, %v", first, err)
	}
	commits := commitsOf(t, srv, "slides")
	for range 2 {
		again, err := bot.Slides.RenderDeck(ctx10(t), req)
		if err != nil || !again.GetDuplicate() || !proto.Equal(again.GetDeck(), first.GetDeck()) {
			t.Fatalf("repeat: %v, %v; want %v with duplicate", again, err, first.GetDeck())
		}
	}
	other := proto.Clone(req).(*slidesv1.RenderDeckRequest)
	other.Slides[2].Bullets = []string{"new"}
	_, err = bot.Slides.RenderDeck(ctx10(t), other)
	wantCode(t, "same command ID, other deck", err, connect.CodeAlreadyExists)
	byAda := proto.Clone(req).(*slidesv1.RenderDeckRequest)
	byAda.AuthorParticipantId = "ada"
	_, err = ada.Slides.RenderDeck(ctx10(t), byAda)
	wantCode(t, "same command ID, other author", err, connect.CodeAlreadyExists)
	if n := commitsOf(t, srv, "slides"); n != commits {
		t.Fatalf("repeats made commits: %d, want %d", n, commits)
	}

	if next := render(t, bot, budgetDeck()); next.GetId() != "slides/2" {
		t.Fatalf("next deck: %s, want slides/2", next.GetId())
	}
}

// TestSlidesErrors: bad requests, a wrong author, no token, stage links and unknown IDs.
func TestSlidesErrors(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ok := func() *slidesv1.RenderDeckRequest {
		r := budgetDeck()
		r.CommandId, r.AuthorParticipantId = coreapi.NewCommandID(), "ada"
		return r
	}
	huge := make([]string, 20000)
	for i := range huge {
		huge[i] = strings.Repeat("x", 60)
	}
	bad := map[string]func(r *slidesv1.RenderDeckRequest){
		"no command_id":     func(r *slidesv1.RenderDeckRequest) { r.CommandId = "" },
		"no author":         func(r *slidesv1.RenderDeckRequest) { r.AuthorParticipantId = "" },
		"no slides":         func(r *slidesv1.RenderDeckRequest) { r.Slides = nil },
		"slide no title":    func(r *slidesv1.RenderDeckRequest) { r.Slides[1].Title = "" },
		"slide blank title": func(r *slidesv1.RenderDeckRequest) { r.Slides[1].Title = " \t\n" },
		"too large":         func(r *slidesv1.RenderDeckRequest) { r.Slides[0].Bullets = huge },
	}
	for name, change := range bad {
		r := ok()
		change(r)
		_, err := ada.Slides.RenderDeck(ctx10(t), r)
		wantCode(t, name, err, connect.CodeInvalidArgument)
	}
	r := ok()
	r.AuthorParticipantId = "bob"
	_, err := ada.Slides.RenderDeck(ctx10(t), r)
	wantCode(t, "author is not the caller", err, connect.CodePermissionDenied)

	_, err = ada.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "slides/1"})
	wantCode(t, "unknown ID", err, connect.CodeNotFound)
	_, err = ada.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "nonsense"})
	wantCode(t, "ID of no known form", err, connect.CodeNotFound)
	_, err = ada.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{})
	wantCode(t, "no ID", err, connect.CodeInvalidArgument)

	a := render(t, ada, budgetDeck())
	anon := srv.Anonymous(t)
	_, err = anon.Slides.RenderDeck(ctx10(t), ok())
	wantCode(t, "render without token", err, connect.CodeUnauthenticated)
	_, err = anon.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: a.GetId()})
	wantCode(t, "get without token", err, connect.CodeUnauthenticated)
	_, err = srv.WithToken(t, "not-a-token").Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: a.GetId()})
	wantCode(t, "get with a bad token", err, connect.CodeUnauthenticated)

	// The stage renders on the hub; a stage link cannot use the slides interface.
	link, err := ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: "s1", CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	stage := srv.WithToken(t, stageToken(t, link.GetUrl()))
	_, err = stage.Slides.RenderDeck(ctx10(t), ok())
	wantCode(t, "render with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: a.GetId()})
	wantCode(t, "get with a stage link", err, connect.CodePermissionDenied)

	// Failed calls stored nothing: the one good render is slides/1, the next is slides/2.
	if a.GetId() != "slides/1" {
		t.Fatalf("first good render: %s, want slides/1", a.GetId())
	}
	_, err = ada.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "slides/2"})
	wantCode(t, "slides/2 after failed calls", err, connect.CodeNotFound)
	if n := commitsOf(t, srv, "slides"); n != 1 {
		t.Fatalf("failed calls made commits: %d commits of slides, want 1", n)
	}
}

// TestSlidesEscapeText: all text of a deck is shown as text. Nothing in it becomes markup.
func TestSlidesEscapeText(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	bot := srv.Participant(t, "bot", "Bot", agent)
	hostile := []string{
		`<script>alert(1)</script>`,
		`"><img src=x onerror=alert(2)>`,
		`</style><style>body{display:none}</style>`,
		`</title><iframe src="https://evil.example"></iframe>`,
		`Tom & Jerry &amp; 'quotes' <b>bold</b> <!-- comment -->`,
		`<a href="javascript:alert(3)">link</a>`,
	}
	deck := &slidesv1.RenderDeckRequest{Title: hostile[3]}
	for i, h := range hostile {
		deck.Slides = append(deck.Slides, &slidesv1.Slide{Title: h, Bullets: []string{h, "plain " + h}, Notes: hostile[(i+1)%len(hostile)]})
	}
	a := render(t, bot, deck)
	p := parsePage(t, a.GetContent())

	allowedTags := map[string]bool{"html": true, "head": true, "meta": true, "title": true, "style": true,
		"body": true, "section": true, "h1": true, "h2": true, "ul": true, "li": true, "aside": true, "footer": true}
	for tag := range p.tags {
		if !allowedTags[tag] {
			t.Errorf("hostile text became the element <%s>", tag)
		}
	}
	allowedAttrs := map[string]bool{"charset": true, "name": true, "content": true, "http-equiv": true,
		"class": true, "id": true, "aria-label": true, "data-slides": true}
	for a := range p.attrs {
		if !allowedAttrs[a] {
			t.Errorf("hostile text became the attribute %s", a)
		}
	}
	if p.tags["style"] != 1 || p.tags["title"] != 1 {
		t.Errorf("elements: %v", p.tags)
	}
	if p.text["title"] != hostile[3] {
		t.Errorf("title slide: %q, want %q", p.text["title"], hostile[3])
	}
	for i, h := range hostile {
		id := "slide-" + string(rune('1'+i))
		if !strings.HasPrefix(p.text[id], h+" "+h+" plain "+h+" ") {
			t.Errorf("#%s: %q, want the hostile text as it is", id, p.text[id])
		}
	}
	if !strings.Contains(string(a.GetContent()), `default-src 'none'`) {
		t.Error("no Content-Security-Policy that blocks outside resources")
	}
}

// sameTime reports whether v is a time in RFC 3339 that equals want.
func sameTime(v any, want time.Time) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	got, err := time.Parse(time.RFC3339Nano, s)
	return err == nil && got.Equal(want)
}

// sameSlides reports whether two lists of slides are equal.
func sameSlides(a, b []*slidesv1.Slide) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// TestSlidesInArchive: a render returns only after its commit. The commit holds the input as
// YAML (slides/NNNNNN.yaml) and the rendered deck as HTML (slides/NNNNNN.html), and nothing else.
func TestSlidesInArchive(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	bot := srv.Participant(t, "bot", "Bot", agent)
	deck := budgetDeck()
	deck.Title = "2027" // a number stays a string
	deck.Slides[0].Notes = "Line one\n\n  indented: yes # not a comment\n"
	a := render(t, bot, deck)

	repo := srv.ArchiveDir()
	files := strings.Fields(contracttest.Git(t, repo, "show", "--format=", "--name-only", "HEAD"))
	if strings.Join(files, " ") != "slides/000001.html slides/000001.yaml" {
		t.Fatalf("files of the last commit: %v", files)
	}
	if msg := strings.TrimSpace(contracttest.Git(t, repo, "log", "-1", "--format=%s")); !strings.Contains(msg, "slides/1") {
		t.Errorf("commit message %q does not name the deck", msg)
	}
	if html := contracttest.Git(t, repo, "show", "HEAD:slides/000001.html"); html != string(a.GetContent()) {
		t.Fatal("the HTML file in the archive is not the rendered deck")
	}
	y := archivedYAML(t, srv, "slides/000001.yaml")
	sl, _ := y["slides"].([]any)
	first, _ := func() (map[string]any, bool) {
		if len(sl) == 0 {
			return nil, false
		}
		m, ok := sl[0].(map[string]any)
		return m, ok
	}()
	if y["id"] != "slides/1" || y["title"] != "2027" || y["author_participant_id"] != "bot" ||
		y["author_kind"] != "AUTHOR_KIND_AGENT" || y["command_id"] != a.GetCommandId() ||
		!sameTime(y["create_time"], a.GetCreateTime().AsTime()) || len(sl) != 3 ||
		first["title"] != "Plan" || first["notes"] != deck.Slides[0].Notes {
		t.Fatalf("YAML of the deck: %v", y)
	}
	if _, ok := y["content"]; ok {
		t.Error("the YAML file holds the rendered deck; it belongs in the HTML file")
	}
	if raw := contracttest.Git(t, repo, "show", "HEAD:slides/000001.yaml"); !strings.Contains(raw, "notes: |\n") {
		t.Errorf("notes with line breaks are not a literal block:\n%s", raw)
	}

	render(t, bot, &slidesv1.RenderDeckRequest{Slides: []*slidesv1.Slide{{Title: "Second"}}})
	if n := commitsOf(t, srv, "slides"); n != 2 {
		t.Fatalf("two decks: %d commits, want 2", n)
	}
}

// TestSlidesRenderStoredInput: the input in the archive is enough to get the deck again. Rendering
// it again, on the same hub or on another one, gives the stored output byte for byte.
func TestSlidesRenderStoredInput(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	deck := budgetDeck()
	deck.Slides[1].Bullets = append(deck.Slides[1].Bullets, "  spaces around  ", "<b>not bold</b>")
	a := render(t, ada, deck)

	// Read the input back from the archive in the form of a render request.
	raw := contracttest.Git(t, srv.ArchiveDir(), "show", "HEAD:slides/000001.yaml")
	var stored map[string]any
	if err := yaml.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"title": stored["title"], "slides": stored["slides"]}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	again := &slidesv1.RenderDeckRequest{}
	if err := protojson.Unmarshal(b, again); err != nil {
		t.Fatalf("stored input is not a deck: %v\n%s", err, raw)
	}
	stored1 := []byte(contracttest.Git(t, srv.ArchiveDir(), "show", "HEAD:slides/000001.html"))
	for name, who := range map[string]*contracttest.Actor{
		"same hub": ada,
		"another hub": func() *contracttest.Actor {
			s := contracttest.StartHub(t, nil)
			return s.Participant(t, "carl", "Carl", human)
		}(),
	} {
		got := render(t, who, again)
		if !bytes.Equal(got.GetContent(), stored1) || !bytes.Equal(got.GetContent(), a.GetContent()) {
			t.Errorf("%s: rendering the stored input gives other content", name)
		}
	}
}

// TestSlidesSurviveRestart: the hub reads the decks back from the archive when it starts. A deck
// keeps its input and output, IDs do not start again, and a repeat is still a duplicate. The old
// file of the artifact tools is not written.
func TestSlidesSurviveRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	req := budgetDeck()
	req.CommandId, req.AuthorParticipantId = coreapi.NewCommandID(), "ada"
	res, err := ada.Slides.RenderDeck(ctx10(t), req)
	if err != nil {
		t.Fatal(err)
	}
	a1 := res.GetDeck()
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	if entries, err := os.ReadDir(srv.DataDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".jsonl") {
				t.Errorf("the hub wrote %s; decks belong in the archive", e.Name())
			}
		}
	}

	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	ada = srv.WithKey(t, ada.Key)
	bot := srv.Participant(t, "bot", "Bot", agent)
	for _, who := range []*contracttest.Actor{ada, bot} {
		if got := getDeck(t, who, a1.GetId()); !proto.Equal(got, a1) {
			t.Fatalf("after restart as %s: %v, want %v", who.ParticipantID, got, a1)
		}
	}
	again, err := ada.Slides.RenderDeck(ctx10(t), req)
	if err != nil || !again.GetDuplicate() || !proto.Equal(again.GetDeck(), a1) {
		t.Fatalf("repeat after restart: %v, %v", again, err)
	}
	other := proto.Clone(req).(*slidesv1.RenderDeckRequest)
	other.Title = "Budget 2028"
	_, err = ada.Slides.RenderDeck(ctx10(t), other)
	wantCode(t, "reused command ID after restart", err, connect.CodeAlreadyExists)
	if a2 := render(t, ada, budgetDeck()); a2.GetId() != "slides/2" || !bytes.Equal(a2.GetContent(), a1.GetContent()) {
		t.Fatalf("next after restart: %s", a2.GetId())
	}
}

// TestSlidesBadArchive: a deck in the archive without its rendered output stops the start of the
// hub with an error that names the file.
func TestSlidesBadArchive(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	render(t, ada, budgetDeck())
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	repo := srv.ArchiveDir()
	contracttest.Git(t, repo, "rm", "-q", "slides/000001.html")
	contracttest.Git(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "remove the output")

	p := contracttest.Start(t, "office-hub", nil, "-addr", "127.0.0.1:0", "-data-dir", srv.DataDir,
		"-signing-key", srv.SigningKeyFile)
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal("the hub started with a deck without its output")
	}
	if !strings.Contains(p.Stderr(), "slides/000001.html") {
		t.Fatalf("the error does not name the missing file:\n%s", p.Stderr())
	}
}

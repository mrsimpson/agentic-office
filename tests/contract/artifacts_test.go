package contract

// Contract tests of the artifact tools (bb-artifact-service) through if-artifact-api. They start
// the real office-server and use only the Connect clients.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"golang.org/x/net/html"
	"google.golang.org/protobuf/proto"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	artifactv1 "github.com/mrsimpson/agentic-office/server/artifacts/api"
	identityv1 "github.com/mrsimpson/agentic-office/server/identity/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const htmlType = "text/html; charset=utf-8"

func budgetDeck() *artifactv1.RenderSlidesRequest {
	return &artifactv1.RenderSlidesRequest{Title: "Budget 2027", Slides: []*artifactv1.Slide{
		{Title: "Plan", Bullets: []string{"Hire two people", "Open an office in Lyon"}, Notes: "Say the numbers slowly."},
		{Title: "Costs", Bullets: []string{"Rent: 4,000 EUR a month"}},
		{Title: "Questions"},
	}}
}

// render renders deck as a with a new command ID and fails the test on an error.
func render(t *testing.T, a *contracttest.Actor, deck *artifactv1.RenderSlidesRequest) *artifactv1.Artifact {
	t.Helper()
	req := proto.Clone(deck).(*artifactv1.RenderSlidesRequest)
	req.CommandId, req.AuthorParticipantId = ids.NewCommandID(), a.ParticipantID
	res, err := a.Artifact.RenderSlides(ctx10(t), req)
	if err != nil {
		t.Fatalf("render %q as %s: %v", deck.GetTitle(), a.ParticipantID, err)
	}
	if res.GetDuplicate() {
		t.Fatalf("render %q: duplicate on the first call", deck.GetTitle())
	}
	return res.GetArtifact()
}

func getArtifact(t *testing.T, a *contracttest.Actor, id string) *artifactv1.Artifact {
	t.Helper()
	res, err := a.Artifact.GetArtifact(ctx10(t), &artifactv1.GetArtifactRequest{Id: id})
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return res.GetArtifact()
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

// TestArtifactsRenderAndGet: a deck becomes an HTML artifact "slides/<n>" that anyone with a
// participant token can fetch by its ID.
func TestArtifactsRenderAndGet(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	bot := srv.Participant(t, "bot", "Bot", agent)
	ada := srv.Participant(t, "ada", "Ada", human)

	before := time.Now().Add(-time.Second)
	a := render(t, bot, budgetDeck())
	if a.GetId() != "slides/1" || a.GetMediaType() != htmlType || a.GetAuthorParticipantId() != "bot" ||
		a.GetCreatedAt().AsTime().Before(before) || a.GetCreatedAt().AsTime().After(time.Now().Add(time.Second)) {
		t.Fatalf("artifact: id %q, type %q, author %q, created %v", a.GetId(), a.GetMediaType(),
			a.GetAuthorParticipantId(), a.GetCreatedAt().AsTime())
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

	// Fetch by ID: the same artifact, also for another participant.
	for _, who := range []*contracttest.Actor{bot, ada} {
		if got := getArtifact(t, who, "slides/1"); !proto.Equal(got, a) {
			t.Fatalf("get as %s: %v, want %v", who.ParticipantID, got, a)
		}
	}

	// A deck without a title has no title slide; the document title is the first slide.
	b := render(t, ada, &artifactv1.RenderSlidesRequest{Slides: []*artifactv1.Slide{{Title: "Only", Bullets: []string{"x"}}}})
	if b.GetId() != "slides/2" || b.GetAuthorParticipantId() != "ada" {
		t.Fatalf("second artifact: %q by %q", b.GetId(), b.GetAuthorParticipantId())
	}
	p = parsePage(t, b.GetContent())
	if _, ok := p.text["title"]; ok || p.text["slide-1"] != "Only x 1 / 1" || p.tags["section"] != 1 {
		t.Fatalf("deck without title: %v %v", p.text, p.tags)
	}
	if !proto.Equal(getArtifact(t, bot, "slides/1"), a) {
		t.Fatal("slides/1 changed after another render")
	}
}

// TestArtifactsDeterministic: the same deck gives byte-equal content, whoever renders it, with
// another command ID, on another server (con-no-intelligence). Another deck gives other content.
func TestArtifactsDeterministic(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
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
		t.Fatal("same deck on another server: other content")
	}

	changes := map[string]func(d *artifactv1.RenderSlidesRequest){
		"title":          func(d *artifactv1.RenderSlidesRequest) { d.Title = "Budget 2028" },
		"slide title":    func(d *artifactv1.RenderSlidesRequest) { d.Slides[1].Title = "Cost" },
		"bullet":         func(d *artifactv1.RenderSlidesRequest) { d.Slides[0].Bullets[1] = "Open an office in Lille" },
		"bullet order":   func(d *artifactv1.RenderSlidesRequest) { b := d.Slides[0].Bullets; b[0], b[1] = b[1], b[0] },
		"notes":          func(d *artifactv1.RenderSlidesRequest) { d.Slides[0].Notes = "Say it fast." },
		"one slide less": func(d *artifactv1.RenderSlidesRequest) { d.Slides = d.Slides[:2] },
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

// srv2Render renders budgetDeck on a fresh server and returns the content.
func srv2Render(t *testing.T) []byte {
	t.Helper()
	srv := contracttest.StartServer(t, nil)
	return render(t, srv.Participant(t, "carl", "Carl", human), budgetDeck()).GetContent()
}

// TestArtifactsIdempotent: a repeated render returns the stored artifact; a reused command ID
// with another deck or author fails.
func TestArtifactsIdempotent(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	bot := srv.Participant(t, "bot", "Bot", agent)
	ada := srv.Participant(t, "ada", "Ada", human)

	req := budgetDeck()
	req.CommandId, req.AuthorParticipantId = ids.NewCommandID(), "bot"
	first, err := bot.Artifact.RenderSlides(ctx10(t), req)
	if err != nil || first.GetDuplicate() {
		t.Fatalf("first: %v, %v", first, err)
	}
	for range 2 {
		again, err := bot.Artifact.RenderSlides(ctx10(t), req)
		if err != nil || !again.GetDuplicate() || !proto.Equal(again.GetArtifact(), first.GetArtifact()) {
			t.Fatalf("repeat: %v, %v; want %v with duplicate", again, err, first.GetArtifact())
		}
	}
	other := proto.Clone(req).(*artifactv1.RenderSlidesRequest)
	other.Slides[2].Bullets = []string{"new"}
	_, err = bot.Artifact.RenderSlides(ctx10(t), other)
	wantCode(t, "same command ID, other deck", err, connect.CodeAlreadyExists)
	byAda := proto.Clone(req).(*artifactv1.RenderSlidesRequest)
	byAda.AuthorParticipantId = "ada"
	_, err = ada.Artifact.RenderSlides(ctx10(t), byAda)
	wantCode(t, "same command ID, other author", err, connect.CodeAlreadyExists)

	if next := render(t, bot, budgetDeck()); next.GetId() != "slides/2" {
		t.Fatalf("next artifact: %s, want slides/2", next.GetId())
	}
}

// TestArtifactsErrors: bad requests, a wrong author, no token, stage links and unknown IDs.
func TestArtifactsErrors(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ok := func() *artifactv1.RenderSlidesRequest {
		r := budgetDeck()
		r.CommandId, r.AuthorParticipantId = ids.NewCommandID(), "ada"
		return r
	}
	huge := make([]string, 20000)
	for i := range huge {
		huge[i] = strings.Repeat("x", 60)
	}
	bad := map[string]func(r *artifactv1.RenderSlidesRequest){
		"no command_id":     func(r *artifactv1.RenderSlidesRequest) { r.CommandId = "" },
		"no author":         func(r *artifactv1.RenderSlidesRequest) { r.AuthorParticipantId = "" },
		"no slides":         func(r *artifactv1.RenderSlidesRequest) { r.Slides = nil },
		"slide no title":    func(r *artifactv1.RenderSlidesRequest) { r.Slides[1].Title = "" },
		"slide blank title": func(r *artifactv1.RenderSlidesRequest) { r.Slides[1].Title = " \t\n" },
		"too large":         func(r *artifactv1.RenderSlidesRequest) { r.Slides[0].Bullets = huge },
	}
	for name, change := range bad {
		r := ok()
		change(r)
		_, err := ada.Artifact.RenderSlides(ctx10(t), r)
		wantCode(t, name, err, connect.CodeInvalidArgument)
	}
	r := ok()
	r.AuthorParticipantId = "bob"
	_, err := ada.Artifact.RenderSlides(ctx10(t), r)
	wantCode(t, "author is not the caller", err, connect.CodePermissionDenied)

	_, err = ada.Artifact.GetArtifact(ctx10(t), &artifactv1.GetArtifactRequest{Id: "slides/1"})
	wantCode(t, "unknown ID", err, connect.CodeNotFound)
	_, err = ada.Artifact.GetArtifact(ctx10(t), &artifactv1.GetArtifactRequest{Id: "nonsense"})
	wantCode(t, "ID of no known form", err, connect.CodeNotFound)
	_, err = ada.Artifact.GetArtifact(ctx10(t), &artifactv1.GetArtifactRequest{})
	wantCode(t, "no ID", err, connect.CodeInvalidArgument)

	a := render(t, ada, budgetDeck())
	anon := srv.Anonymous(t)
	_, err = anon.Artifact.RenderSlides(ctx10(t), ok())
	wantCode(t, "render without token", err, connect.CodeUnauthenticated)
	_, err = anon.Artifact.GetArtifact(ctx10(t), &artifactv1.GetArtifactRequest{Id: a.GetId()})
	wantCode(t, "get without token", err, connect.CodeUnauthenticated)
	_, err = srv.WithToken(t, "not-a-token").Artifact.GetArtifact(ctx10(t), &artifactv1.GetArtifactRequest{Id: a.GetId()})
	wantCode(t, "get with a bad token", err, connect.CodeUnauthenticated)

	// The stage renders on the server; a stage link cannot use the artifact API.
	link, err := ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: "s1", CommandId: ids.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	stage := srv.WithToken(t, stageToken(t, link.GetUrl()))
	_, err = stage.Artifact.RenderSlides(ctx10(t), ok())
	wantCode(t, "render with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Artifact.GetArtifact(ctx10(t), &artifactv1.GetArtifactRequest{Id: a.GetId()})
	wantCode(t, "get with a stage link", err, connect.CodePermissionDenied)

	// Failed calls stored nothing: the one good render is slides/1, the next is slides/2.
	if a.GetId() != "slides/1" {
		t.Fatalf("first good render: %s, want slides/1", a.GetId())
	}
	_, err = ada.Artifact.GetArtifact(ctx10(t), &artifactv1.GetArtifactRequest{Id: "slides/2"})
	wantCode(t, "slides/2 after failed calls", err, connect.CodeNotFound)
}

// TestArtifactsEscapeText: all text of a deck is shown as text. Nothing in it becomes markup.
func TestArtifactsEscapeText(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	bot := srv.Participant(t, "bot", "Bot", agent)
	hostile := []string{
		`<script>alert(1)</script>`,
		`"><img src=x onerror=alert(2)>`,
		`</style><style>body{display:none}</style>`,
		`</title><iframe src="https://evil.example"></iframe>`,
		`Tom & Jerry &amp; 'quotes' <b>bold</b> <!-- comment -->`,
		`<a href="javascript:alert(3)">link</a>`,
	}
	deck := &artifactv1.RenderSlidesRequest{Title: hostile[3]}
	for i, h := range hostile {
		deck.Slides = append(deck.Slides, &artifactv1.Slide{Title: h, Bullets: []string{h, "plain " + h}, Notes: hostile[(i+1)%len(hostile)]})
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

// TestArtifactsSurviveRestart: the office server keeps artifacts in its data directory, so IDs
// do not start again and a repeat after a restart is still a duplicate.
func TestArtifactsSurviveRestart(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	req := budgetDeck()
	req.CommandId, req.AuthorParticipantId = ids.NewCommandID(), "ada"
	res, err := ada.Artifact.RenderSlides(ctx10(t), req)
	if err != nil {
		t.Fatal(err)
	}
	a1 := res.GetArtifact()
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	path := filepath.Join(srv.DataDir, "artifacts.jsonl")
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("artifact file: %v, %v", fi, err)
	}
	// The server stopped while it wrote a line: the incomplete line is dropped at the start.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"commandId":"x","input":{"tit`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	srv = contracttest.StartServerIn(t, srv.DataDir, srv.AdminKey, nil)
	ada = srv.WithKey(t, ada.Key)
	if got := getArtifact(t, ada, a1.GetId()); !proto.Equal(got, a1) {
		t.Fatalf("after restart: %v, want %v", got, a1)
	}
	again, err := ada.Artifact.RenderSlides(ctx10(t), req)
	if err != nil || !again.GetDuplicate() || !proto.Equal(again.GetArtifact(), a1) {
		t.Fatalf("repeat after restart: %v, %v", again, err)
	}
	if a2 := render(t, ada, budgetDeck()); a2.GetId() != "slides/2" || !bytes.Equal(a2.GetContent(), a1.GetContent()) {
		t.Fatalf("next after restart: %s", a2.GetId())
	}
}

package contract

// Contract tests of the wiki in the portal (if-portal-web, concept-wiki): the index with its tags,
// one artifact with its rendered text, links, backlinks, sources and the documents that cite it,
// the search, the broken links, the ingest queue and the separation of tenants. The tests write
// through the interfaces of the parts and open the pages with a browser-like HTTP client. The view
// of the wiki follows a write a moment later, so the tests wait for a condition.

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const portalPricingPage = `---
schema: office.document.v1
title: Pricing
summary: How we price the product for customers.
tags: [pricing, sales]
sources: [notes/000001.md]
status: reviewed
owner: ada
reviewed:
  - id: bob
    date: 2026-11-19
---
# Prices

The price for [ACME](customers/acme.md) follows the [note](../notes/000001.md).
See also [the plan](missing.md) and [the web](https://example.org/).

<script>alert("no")</script>
`

// wikiPath is the address of a page of the wiki of a tenant in the portal.
func wikiPath(tenant, rest string) string {
	return "/portal/t/" + url.PathEscape(tenant) + "/wiki" + rest
}

// openPage fetches a page of the portal and checks its status.
func openPage(t *testing.T, b *http.Client, srv *contracttest.Hub, page string, status int) *html.Node {
	t.Helper()
	res, body := fetch(t, b, srv.URL+page)
	if res.StatusCode != status {
		t.Fatalf("%s: status %d, want %d\n%s", page, res.StatusCode, status, body)
	}
	return parseDoc(t, body)
}

// entryPaths returns the artifacts that a list of the wiki shows, in order.
func entryPaths(doc *html.Node) []string {
	var out []string
	for _, el := range findAll(doc, func(n *html.Node) bool { _, ok := attr(n, "data-entry"); return ok }) {
		v, _ := attr(el, "data-entry")
		out = append(out, v)
	}
	return out
}

// hrefs returns the targets of the links under n with this text.
func hrefs(n *html.Node, label string) []string {
	var out []string
	for _, a := range findAll(n, byTag("a")) {
		if strings.TrimSpace(text(a)) == label {
			h, _ := attr(a, "href")
			out = append(out, h)
		}
	}
	return out
}

// A member reads the wiki of the tenant in the portal: the index with its tags and the filter, an
// artifact with its rendered text, its trust data, its front matter as written, links, backlinks,
// sources and citations, a link that leads to the page of the other artifact, the search, the
// broken links and the ingest queue.
func TestPortalWiki(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bob := srv.Participant(t, "bob", "Bob", human)
	tenant := contracttest.DefaultTenant
	addTaggedNote(t, ada, "The new price for ACME.\n\nIt starts in January.", "", "pricing")
	addTaggedNote(t, ada, "Ask the bank about the loan.", "", "bank")
	if _, err := createDocIn(t, ada, "", "documents/pricing.md", portalPricingPage); err != nil {
		t.Fatal(err)
	}
	if _, err := createDocIn(t, ada, "", "documents/customers/acme.md", "# ACME GmbH\n\nA customer since 2025. See [pricing](../pricing.md).\n"); err != nil {
		t.Fatal(err)
	}
	all := []string{"documents/customers/acme.md", "documents/pricing.md", "notes/000001.md", "notes/000002.md"}
	waitFor(t, "the artifacts in the wiki", func() bool { return slices.Equal(wikiIndex(t, ada, ""), all) })

	b, _, doc := openPortal(t, srv, bob, tenant)
	if len(findAll(doc, withAttr("data-nav", wikiPath(tenant, "")))) != 1 {
		t.Fatalf("no wiki in the navigation: %q", text(doc))
	}

	// The index, its tags and the filter.
	doc = openPage(t, b, srv, wikiPath(tenant, ""), http.StatusOK)
	wantList(t, "the index", entryPaths(doc), all)
	if len(findAll(doc, byTag("script"))) != 0 {
		t.Errorf("the index has scripts")
	}
	var tags []string
	for _, el := range findAll(doc, func(n *html.Node) bool { _, ok := attr(n, "data-tag-filter"); return ok }) {
		tags = append(tags, strings.Join(strings.Fields(text(el)), " "))
	}
	wantList(t, "the tags", tags, []string{"all", "bank 1", "pricing 2", "sales 1"})
	salesHref := hrefs(findAll(doc, withAttr("data-tags", ""))[0], "sales 1")
	if len(salesHref) != 1 || salesHref[0] != wikiPath(tenant, "?tag=sales") {
		t.Errorf("the link of the tag sales: %q", salesHref)
	}
	wantList(t, "the index tagged pricing", entryPaths(openPage(t, b, srv, wikiPath(tenant, "?tag=pricing"), http.StatusOK)),
		[]string{"documents/pricing.md", "notes/000001.md"})
	if d := openPage(t, b, srv, wikiPath(tenant, "?tag=nothing"), http.StatusOK); len(entryPaths(d)) != 0 || len(findAll(d, withAttr("data-empty", "wiki"))) != 1 {
		t.Errorf("the index tagged nothing: %q", text(d))
	}

	// One page: the text rendered, raw HTML left out, links to other artifacts lead to their pages.
	pricingPage := wikiPath(tenant, "/a/documents/pricing.md")
	acmePage := wikiPath(tenant, "/a/documents/customers/acme.md")
	notePage := wikiPath(tenant, "/a/notes/000001.md")
	doc = openPage(t, b, srv, pricingPage, http.StatusOK)
	if len(findAll(doc, byTag("script"))) != 0 || strings.Contains(text(doc), "alert") {
		t.Errorf("the page renders raw HTML: %q", text(doc))
	}
	content := findAll(doc, withAttr("data-content", ""))
	if len(content) != 1 || len(findAll(content[0], byTag("h1"))) != 1 || !strings.Contains(text(content[0]), "The price for ACME follows the note.") {
		t.Fatalf("the rendered text: %q", text(doc))
	}
	if got := hrefs(content[0], "ACME"); !slices.Equal(got, []string{acmePage}) {
		t.Errorf("the link to ACME leads to %q, want %q", got, acmePage)
	}
	if got := hrefs(content[0], "note"); !slices.Equal(got, []string{notePage}) {
		t.Errorf("the link to the note leads to %q, want %q", got, notePage)
	}
	if got := hrefs(content[0], "the plan"); len(got) != 0 || !strings.Contains(text(content[0]), "the plan") {
		t.Errorf("the broken link in the text: %q", got)
	}
	if got := hrefs(content[0], "the web"); !slices.Equal(got, []string{"https://example.org/"}) {
		t.Errorf("the link to another site: %q", got)
	}
	facts := findAll(doc, withAttr("data-artifact", "documents/pricing.md"))
	if len(facts) != 1 || !strings.Contains(text(facts[0]), "page") || !strings.Contains(text(facts[0]), "How we price the product for customers.") {
		t.Fatalf("the facts of the page: %q", text(doc))
	}
	if fm := findAll(facts[0], withAttr("data-front-matter", "")); len(fm) != 1 || !strings.Contains(text(fm[0]), "status: reviewed") ||
		!strings.Contains(text(fm[0]), "owner: ada") || !strings.Contains(text(fm[0]), "id: bob") {
		t.Errorf("the front matter: %q", text(facts[0]))
	}
	// The trust data that the wiki gives: the state of review, and who changed the page last.
	for _, want := range []struct{ attr, value, text string }{
		{"data-status", "", "reviewed"}, {"data-owner", "", "ada"}, {"data-review", "bob 2026-11-19", "bob on 2026-11-19"},
		{"data-author", "ada", "Ada"}, {"data-author-kind", "human", "human"}, {"data-newest-source", "", "written"},
	} {
		if el := findAll(facts[0], withAttr(want.attr, want.value)); len(el) != 1 || !strings.Contains(text(el[0]), want.text) {
			t.Errorf("the trust data %s=%q: %q", want.attr, want.value, text(facts[0]))
		}
	}
	if len(findAll(facts[0], withAttr("data-last-change", ""))) != 1 || len(findAll(facts[0], withAttr("data-released", ""))) != 0 {
		t.Errorf("the last change of the page: %q", text(facts[0]))
	}
	var links []string
	for _, el := range findAll(doc, func(n *html.Node) bool { _, ok := attr(n, "data-link"); return ok }) {
		v, _ := attr(el, "data-link")
		if _, broken := attr(el, "data-broken-link"); broken {
			v += " (broken)"
		}
		links = append(links, v)
	}
	// The links of the text, then the cited sources.
	wantList(t, "the links and sources of the page", links, []string{"documents/customers/acme.md", "notes/000001.md", "documents/missing.md (broken)", "notes/000001.md"})
	wantList(t, "the backlinks of the page", entryPaths(doc), []string{"documents/customers/acme.md"})

	// The link leads to the page of the other artifact, which links back.
	doc = openPage(t, b, srv, acmePage, http.StatusOK)
	if h1 := findAll(doc, byTag("h1")); len(h1) == 0 || text(h1[0]) != "ACME GmbH" {
		t.Errorf("the page of ACME: %q", text(doc))
	}
	if got := hrefs(findAll(doc, withAttr("data-content", ""))[0], "pricing"); !slices.Equal(got, []string{pricingPage}) {
		t.Errorf("the link back to pricing: %q", got)
	}

	// A source: its backlinks and the documents that cite it.
	doc = openPage(t, b, srv, notePage, http.StatusOK)
	if !strings.Contains(text(findAll(doc, withAttr("data-kind", ""))[0]), "source") {
		t.Errorf("the kind of the note: %q", text(doc))
	}
	wantList(t, "the backlinks and citations of the note", entryPaths(doc), []string{"documents/pricing.md", "documents/pricing.md"})
	openPage(t, b, srv, wikiPath(tenant, "/a/documents/missing.md"), http.StatusNotFound)
	openPage(t, b, srv, wikiPath(tenant, "/nothing"), http.StatusNotFound)

	// The search: a form without scripts that sends a GET.
	doc = openPage(t, b, srv, wikiPath(tenant, ""), http.StatusOK)
	form := findAll(doc, withAttr("data-form", "search"))
	if len(form) != 1 {
		t.Fatalf("no search form: %q", text(doc))
	}
	if m, _ := attr(form[0], "method"); m != "get" {
		t.Errorf("the search form uses %q", m)
	}
	if a, _ := attr(form[0], "action"); a != wikiPath(tenant, "/search") {
		t.Errorf("the search form goes to %q", a)
	}
	hits := entryPaths(openPage(t, b, srv, wikiPath(tenant, "/search?q=January+price"), http.StatusOK))
	if len(hits) == 0 || hits[0] != "notes/000001.md" {
		t.Errorf("search January price: %q, want the note first", hits)
	}
	wantList(t, "search loan", entryPaths(openPage(t, b, srv, wikiPath(tenant, "/search?q=loan"), http.StatusOK)), []string{"notes/000002.md"})
	if d := openPage(t, b, srv, wikiPath(tenant, "/search?q=zebra"), http.StatusOK); len(findAll(d, withAttr("data-empty", "hits"))) != 1 {
		t.Errorf("search without hits: %q", text(d))
	}
	if d := openPage(t, b, srv, wikiPath(tenant, "/search"), http.StatusOK); len(findAll(d, withAttr("data-empty", "query"))) != 1 {
		t.Errorf("search without a query: %q", text(d))
	}
	if d := openPage(t, b, srv, wikiPath(tenant, "/search?q=%2C%3B"), http.StatusBadRequest); len(findAll(d, withAttr("data-error", ""))) != 1 {
		t.Errorf("search without a word: %q", text(d))
	}

	// The broken links and the ingest queue.
	doc = openPage(t, b, srv, wikiPath(tenant, "/broken"), http.StatusOK)
	broken := findAll(doc, withAttr("data-broken", "documents/pricing.md -> documents/missing.md"))
	if len(broken) != 1 || !slices.Equal(hrefs(broken[0], "documents/pricing.md"), []string{pricingPage}) {
		t.Errorf("the broken links: %q", text(doc))
	}
	wantList(t, "the ingest queue", entryPaths(openPage(t, b, srv, wikiPath(tenant, "/queue"), http.StatusOK)), []string{"notes/000002.md"})
}

// Each tenant shows its own wiki. A user who is no member of a tenant gets no wiki of it, and a
// stage link gets no wiki at all.
func TestPortalWikiTenants(t *testing.T) {
	m := newTwoTenants(t)
	addTaggedNote(t, m.ada, "An acme note.", "", "shared")
	addTaggedNote(t, m.bob, "A globex note.", "", "shared")
	waitFor(t, "the note of acme", func() bool { return len(wikiIndex(t, m.ada, "")) == 1 })
	waitFor(t, "the note of globex", func() bool { return len(wikiIndex(t, m.bob, "")) == 1 })

	b, _, _ := openPortal(t, m.srv, m.cyK, "acme")
	for tenant, title := range map[string]string{"acme": "An acme note.", "globex": "A globex note."} {
		doc := openPage(t, b, m.srv, wikiPath(tenant, "/a/notes/000001.md"), http.StatusOK)
		if h1 := findAll(doc, byTag("h1")); len(h1) == 0 || text(h1[0]) != title {
			t.Errorf("the note of %s: %q", tenant, text(doc))
		}
		wantList(t, "the index of "+tenant, entryPaths(openPage(t, b, m.srv, wikiPath(tenant, ""), http.StatusOK)), []string{"notes/000001.md"})
	}
	if d := openPage(t, b, m.srv, wikiPath("globex", "/search?q=acme"), http.StatusOK); len(entryPaths(d)) != 0 {
		t.Errorf("search acme in globex: %q", entryPaths(d))
	}

	// Bob is no member of acme: its wiki does not exist for him.
	b, _, _ = openPortal(t, m.srv, m.bobKey, "globex")
	for _, page := range []string{"", "/a/notes/000001.md", "/search?q=note", "/broken", "/queue"} {
		res, body := fetch(t, b, m.srv.URL+wikiPath("acme", page))
		if res.StatusCode != http.StatusNotFound || strings.Contains(body, "acme note") {
			t.Errorf("bob opens %s of acme: status %d", page, res.StatusCode)
		}
	}

	// A stage link is no portal session: with its token, the portal shows no wiki.
	sid := "s-" + coreapi.NewCommandID()
	join2(t, m.ada, sid)
	link, err := m.ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	stage := browser(t, m.srv, true)
	u, _ := url.Parse(m.srv.URL + "/portal/")
	stage.Jar.SetCookies(u, []*http.Cookie{{Name: "office_portal", Value: stageToken(t, link.GetUrl()), Path: "/portal/"}})
	res, body := fetch(t, stage, m.srv.URL+wikiPath("acme", ""))
	if res.StatusCode == http.StatusOK || strings.Contains(body, "acme note") {
		t.Errorf("a stage link opens the wiki: status %d", res.StatusCode)
	}
}

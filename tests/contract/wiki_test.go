package contract

// Contract tests of the wiki part (bb-wiki) through its interface (if-wiki-api), against the real
// office-hub: the index with its tags, links and backlinks, broken links, the ingest queue, the
// search, the stubs of staged artifacts, the reload after a write and after a push, the rebuild
// after a restart, and the separation of tenants. The tests write through the interfaces of the
// parts and push with real Git. The view follows a change after the write returns, so the tests
// wait for a condition with a generous deadline and never check how long it takes.

import (
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	wikiv1 "github.com/mrsimpson/agentic-office/hub/wiki/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// wikiIndex returns the paths of the index as a sees it, with tag as the filter.
func wikiIndex(t *testing.T, a *contracttest.Actor, tag string) []string {
	t.Helper()
	res, err := a.Wiki.ListArtifacts(ctx10(t), &wikiv1.ListArtifactsRequest{Tag: tag})
	if err != nil {
		t.Fatalf("list the wiki as %s: %v", a.ParticipantID, err)
	}
	var out []string
	for _, x := range res.GetArtifacts() {
		out = append(out, x.GetPath())
	}
	return out
}

func wikiArtifact(t *testing.T, a *contracttest.Actor, p string) *wikiv1.GetArtifactResponse {
	t.Helper()
	res, err := a.Wiki.GetArtifact(ctx10(t), &wikiv1.GetArtifactRequest{Path: p})
	if err != nil {
		t.Fatalf("get %s from the wiki: %v", p, err)
	}
	return res
}

func wikiQueue(t *testing.T, a *contracttest.Actor) []string {
	t.Helper()
	res, err := a.Wiki.ListIngestQueue(ctx10(t), &wikiv1.ListIngestQueueRequest{})
	if err != nil {
		t.Fatalf("the ingest queue: %v", err)
	}
	var out []string
	for _, x := range res.GetSources() {
		out = append(out, x.GetPath())
	}
	return out
}

func wikiSearch(t *testing.T, a *contracttest.Actor, q string) []string {
	t.Helper()
	res, err := a.Wiki.Search(ctx10(t), &wikiv1.SearchRequest{Query: q})
	if err != nil {
		t.Fatalf("search %q: %v", q, err)
	}
	var out []string
	for i, h := range res.GetHits() {
		if h.GetScore() <= 0 || (i > 0 && h.GetScore() > res.GetHits()[i-1].GetScore()) {
			t.Errorf("search %q: hit %d has score %v after %v", q, i, h.GetScore(), res.GetHits())
		}
		out = append(out, h.GetArtifact().GetPath())
	}
	return out
}

// wantHits checks that a search finds exactly the paths, with first as the best hit.
func wantHits(t *testing.T, a *contracttest.Actor, q, first string, others ...string) {
	t.Helper()
	got := wikiSearch(t, a, q)
	want := append([]string{first}, others...)
	sorted := slices.Clone(got)
	slices.Sort(sorted)
	slices.Sort(want)
	if len(got) == 0 || got[0] != first || !slices.Equal(sorted, want) {
		t.Errorf("search %q: got %q, want %q first among %q", q, got, first, want)
	}
}

func brokenLinks(t *testing.T, a *contracttest.Actor) []string {
	t.Helper()
	res, err := a.Wiki.ListBrokenLinks(ctx10(t), &wikiv1.ListBrokenLinksRequest{})
	if err != nil {
		t.Fatalf("broken links: %v", err)
	}
	var out []string
	for _, l := range res.GetLinks() {
		s := l.GetFromPath() + " -> " + l.GetToPath()
		if l.GetSource() {
			s += " (source)"
		}
		out = append(out, s)
	}
	return out
}

func linkPaths(ls []*wikiv1.Link) []string {
	var out []string
	for _, l := range ls {
		s := l.GetPath()
		if l.GetBroken() {
			s += " (broken)"
		}
		if l.GetArtifact() != nil {
			s += " [" + l.GetArtifact().GetTitle() + "]"
		}
		out = append(out, s)
	}
	return out
}

func artifactPaths(as []*wikiv1.Artifact) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.GetPath())
	}
	return out
}

func wantList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

func addTaggedNote(t *testing.T, a *contracttest.Actor, text, session string, tags ...string) *notev1.Note {
	t.Helper()
	res, err := a.Note.AddNote(ctx10(t), &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: text,
		AuthorParticipantId: a.ParticipantID, SessionId: session, Tags: tags})
	if err != nil {
		t.Fatalf("add the note %q: %v", text, err)
	}
	return res.GetNote()
}

const pricingPage = `---
schema: office.document.v1
title: Pricing
summary: How we price the product for customers.
tags: [pricing, sales]
sources: [notes/000001.md, minutes/000009.yaml]
---
# Prices

The price for [ACME](customers/acme.md) follows the [note](../notes/000001.md).
See also [the plan](missing.md).
`

const acmePage = "# ACME GmbH\n\nA customer since 2025. See [pricing](../pricing.md).\n"

// The index lists every artifact with its title, summary and tags, filtered by tag. Each artifact
// has its links, backlinks, cited sources and the documents that cite it. Links and sources that
// lead nowhere are listed. The released sources that no document cites are the ingest queue. The
// search ranks by words.
func TestWikiView(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
	p.write("notes/000001.md", `---
schema: office.note.v1
id: 1
author_participant_id: ada
author_kind: AUTHOR_KIND_HUMAN
time: "2026-11-16T10:00:00Z"
command_id: pushed-note-1
tags: [pricing]
---
The new price for ACME.

It starts in January.
`)
	p.write("minutes/000001.yaml", minutesYAML(1, "Weekly sync about the roadmap."))
	p.write("decisions/000001.yaml", decisionYAML(1, "We hire two people."))
	p.write("tasks/000001.yaml", taskYAML(1, "Call ACME"))
	p.write("documents/pricing.md", pricingPage)
	p.write("documents/customers/acme.md", acmePage)
	p.commit("add a small wiki")
	p.mustPush()

	all := []string{"decisions/000001.yaml", "documents/customers/acme.md", "documents/pricing.md",
		"minutes/000001.yaml", "notes/000001.md", "tasks/000001.yaml"}
	waitFor(t, "the pushed artifacts in the wiki", func() bool { return slices.Equal(wikiIndex(t, ada, ""), all) })

	// The index, its tags and the filter.
	res, err := ada.Wiki.ListArtifacts(ctx10(t), &wikiv1.ListArtifactsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res.GetArtifacts() {
		switch a.GetPath() {
		case "documents/pricing.md":
			if a.GetTitle() != "Pricing" || a.GetSummary() != "How we price the product for customers." ||
				!slices.Equal(a.GetTags(), []string{"pricing", "sales"}) || a.GetSchema() != "office.document.v1" || a.GetSource() {
				t.Errorf("the pricing page in the index: %v", a)
			}
		case "notes/000001.md":
			if a.GetTitle() != "The new price for ACME." || a.GetSummary() != "It starts in January." || !a.GetSource() ||
				a.GetSchema() != "office.note.v1" {
				t.Errorf("the note in the index: %v", a)
			}
		case "documents/customers/acme.md":
			if a.GetTitle() != "ACME GmbH" || a.GetSchema() != "" || a.GetSource() {
				t.Errorf("the plain page in the index: %v", a)
			}
		case "tasks/000001.yaml":
			if a.GetTitle() != "Call ACME" || a.GetSource() {
				t.Errorf("the task in the index: %v", a)
			}
		case "minutes/000001.yaml", "decisions/000001.yaml":
			if !a.GetSource() {
				t.Errorf("%s is not a source", a.GetPath())
			}
		}
	}
	var tags []string
	for _, tc := range res.GetTags() {
		tags = append(tags, tc.GetTag()+"="+string(rune('0'+tc.GetCount())))
	}
	wantList(t, "the tags", tags, []string{"pricing=2", "sales=1"})
	wantList(t, "the artifacts tagged pricing", wikiIndex(t, ada, "pricing"), []string{"documents/pricing.md", "notes/000001.md"})
	wantList(t, "the artifacts tagged Pricing", wikiIndex(t, ada, "Pricing"), nil)

	// Links, backlinks, sources and citations.
	pricing := wikiArtifact(t, ada, "documents/pricing.md")
	wantList(t, "the links of the pricing page", linkPaths(pricing.GetLinks()), []string{
		"documents/customers/acme.md [ACME GmbH]", "notes/000001.md [The new price for ACME.]", "documents/missing.md (broken)"})
	wantList(t, "the sources of the pricing page", linkPaths(pricing.GetSources()), []string{
		"notes/000001.md [The new price for ACME.]", "minutes/000009.yaml (broken)"})
	wantList(t, "the backlinks of the pricing page", artifactPaths(pricing.GetBacklinks()), []string{"documents/customers/acme.md"})
	note := wikiArtifact(t, ada, "notes/000001.md")
	wantList(t, "the backlinks of the note", artifactPaths(note.GetBacklinks()), []string{"documents/pricing.md"})
	wantList(t, "the documents that cite the note", artifactPaths(note.GetCitedBy()), []string{"documents/pricing.md"})
	_, err = ada.Wiki.GetArtifact(ctx10(t), &wikiv1.GetArtifactRequest{Path: "documents/missing.md"})
	wantCode(t, "an artifact that does not exist", err, connect.CodeNotFound)
	_, err = ada.Wiki.GetArtifact(ctx10(t), &wikiv1.GetArtifactRequest{})
	wantCode(t, "an artifact without a path", err, connect.CodeInvalidArgument)

	wantList(t, "the broken links", brokenLinks(t, ada), []string{
		"documents/pricing.md -> documents/missing.md", "documents/pricing.md -> minutes/000009.yaml (source)"})
	wantList(t, "the ingest queue", wikiQueue(t, ada), []string{"decisions/000001.yaml", "minutes/000001.yaml"})

	// The search: a word in the title and the tags ranks above a word in the text only; more
	// words of the query rank higher; case does not matter; words match whole.
	wantHits(t, ada, "Pricing", "documents/pricing.md", "documents/customers/acme.md", "notes/000001.md")
	wantHits(t, ada, "acme", "documents/customers/acme.md", "documents/pricing.md", "notes/000001.md", "tasks/000001.yaml")
	if got := wikiSearch(t, ada, "price january"); len(got) == 0 || got[0] != "notes/000001.md" {
		t.Errorf("search price january: %v, want the note first", got)
	}
	wantList(t, "search for a part of a word", wikiSearch(t, ada, "pric"), nil)
	wantList(t, "search roadmap", wikiSearch(t, ada, "roadmap"), []string{"minutes/000001.yaml"})
	_, err = ada.Wiki.Search(ctx10(t), &wikiv1.SearchRequest{Query: " ,; "})
	wantCode(t, "a query without a word", err, connect.CodeInvalidArgument)
	if res, err := ada.Wiki.Search(ctx10(t), &wikiv1.SearchRequest{Query: "acme", Limit: 2}); err != nil || len(res.GetHits()) != 2 {
		t.Errorf("search with limit 2: %v, %v", res, err)
	}

	// A note written through its interface joins the index and the queue. A document that cites
	// it, changed through its interface, takes it out of the queue.
	added := addTaggedNote(t, ada, "Ask the bank about the loan.", "", "bank")
	notePath := "notes/000002.md"
	if added.GetId() != 2 {
		t.Fatalf("the new note has ID %d, want 2", added.GetId())
	}
	waitFor(t, "the new note in the queue", func() bool {
		return slices.Equal(wikiQueue(t, ada), []string{"decisions/000001.yaml", "minutes/000001.yaml", notePath})
	})
	wantList(t, "the artifacts tagged bank", wikiIndex(t, ada, "bank"), []string{notePath})
	wantList(t, "search loan", wikiSearch(t, ada, "loan"), []string{notePath})
	f, err := readFile(t, ada, "documents/pricing.md")
	if err != nil {
		t.Fatal(err)
	}
	cites := replace(t, pricingPage, "sources: [notes/000001.md, minutes/000009.yaml]", "sources: [notes/000001.md, minutes/000001.yaml, "+notePath+"]")
	if _, err := changeDocIn(t, ada, "", "documents/pricing.md", cites, f.GetVersion()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the cited sources out of the queue", func() bool {
		return slices.Equal(wikiQueue(t, ada), []string{"decisions/000001.yaml"})
	})
	wantList(t, "the broken links after the change", brokenLinks(t, ada), []string{"documents/pricing.md -> documents/missing.md"})

	// A push that removes a page: its links are gone, and a link to it is broken.
	p.mustGit("pull", "-q", "--no-rebase", "--no-edit")
	p.mustGit("rm", "-q", "documents/customers/acme.md")
	p.mustGit("commit", "-q", "-m", "remove the customer page")
	p.mustPush()
	waitFor(t, "the removed page out of the wiki", func() bool {
		return !slices.Contains(wikiIndex(t, ada, ""), "documents/customers/acme.md")
	})
	pricing = wikiArtifact(t, ada, "documents/pricing.md")
	wantList(t, "the backlinks after the removal", artifactPaths(pricing.GetBacklinks()), nil)
	wantList(t, "the broken links after the removal", brokenLinks(t, ada), []string{
		"documents/pricing.md -> documents/customers/acme.md", "documents/pricing.md -> documents/missing.md"})
	if got := wikiSearch(t, ada, "ACME GmbH"); slices.Contains(got, "documents/customers/acme.md") || len(got) != 3 {
		t.Errorf("search acme after the removal: %v", got)
	}
}

// A staged document and a staged note have stubs in the artifact repository. The stubs are not
// in the index, the search or the queue, and a link to a stub is not broken.
func TestWikiLeavesStubsOut(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	sid := "s-" + coreapi.NewCommandID()
	join(t, ada.Clients, sid)
	if d, err := createDocIn(t, ada, sid, "documents/secret.md", "# Secret plan\n\nThe zebra project.\n"); err != nil || !d.GetStaged() {
		t.Fatalf("the staged document: %v, %v", d, err)
	}
	if n := addTaggedNote(t, ada, "A zebra note.", sid, "zebra"); !n.GetStaged() {
		t.Fatalf("the note is not staged: %v", n)
	}
	if _, err := createDocIn(t, ada, "", "documents/open.md", "# Open\n\nSee [the plan](secret.md) and [the note](../notes/000001.md).\n"); err != nil {
		t.Fatal(err)
	}
	// The changes come in order: once the last page is in the view, the stubs were seen too.
	waitFor(t, "the open page in the wiki", func() bool { return slices.Contains(wikiIndex(t, ada, ""), "documents/open.md") })

	wantList(t, "the index", wikiIndex(t, ada, ""), []string{"documents/open.md"})
	wantList(t, "the artifacts tagged zebra", wikiIndex(t, ada, "zebra"), nil)
	wantList(t, "search zebra", wikiSearch(t, ada, "zebra"), nil)
	wantList(t, "the ingest queue", wikiQueue(t, ada), nil)
	wantList(t, "the broken links", brokenLinks(t, ada), nil)
	open := wikiArtifact(t, ada, "documents/open.md")
	wantList(t, "the links to stubs", linkPaths(open.GetLinks()), []string{"documents/secret.md", "notes/000001.md"})
	_, err := ada.Wiki.GetArtifact(ctx10(t), &wikiv1.GetArtifactRequest{Path: "documents/secret.md"})
	wantCode(t, "the stub of a document", err, connect.CodeNotFound)
}

// The view is not stored: after a restart, the hub builds it again from the repository.
func TestWikiRebuildsAfterRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	addTaggedNote(t, ada, "Rebuild me.", "", "restart")
	if _, err := createDocIn(t, ada, "", "documents/page.md", "# Page\n\nSee [the note](../notes/000001.md) and [nothing](none.md).\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the page in the wiki", func() bool { return len(wikiIndex(t, ada, "")) == 2 })
	if _, err := srv.Stop(10 * time.Second); err != nil {
		t.Fatalf("stop the hub: %v", err)
	}

	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	ada = srv.WithKey(t, ada.Key)
	wantList(t, "the index after the restart", wikiIndex(t, ada, ""), []string{"documents/page.md", "notes/000001.md"})
	wantList(t, "the tagged note after the restart", wikiIndex(t, ada, "restart"), []string{"notes/000001.md"})
	wantList(t, "the backlinks after the restart", artifactPaths(wikiArtifact(t, ada, "notes/000001.md").GetBacklinks()), []string{"documents/page.md"})
	wantList(t, "the broken links after the restart", brokenLinks(t, ada), []string{"documents/page.md -> documents/none.md"})
	wantList(t, "the queue after the restart", wikiQueue(t, ada), []string{"notes/000001.md"})
	wantList(t, "search after the restart", wikiSearch(t, ada, "rebuild"), []string{"notes/000001.md"})
}

// Each tenant has its own view, and only its members read it. A call needs a token, and a stage
// link cannot read the wiki.
func TestWikiTenantsAndTokens(t *testing.T) {
	m := newTwoTenants(t)
	addTaggedNote(t, m.ada, "An acme note.", "", "shared")
	addTaggedNote(t, m.bob, "A globex note.", "", "shared")
	waitFor(t, "the note of acme", func() bool { return len(wikiIndex(t, m.ada, "")) == 1 })
	waitFor(t, "the note of globex", func() bool { return len(wikiIndex(t, m.bob, "")) == 1 })

	wantList(t, "search in acme", wikiSearch(t, m.cyAcme, "note"), []string{"notes/000001.md"})
	if got := wikiArtifact(t, m.cyAcme, "notes/000001.md").GetArtifact().GetTitle(); got != "An acme note." {
		t.Errorf("the note of acme read by cy: %q", got)
	}
	if got := wikiArtifact(t, m.cyGlobex, "notes/000001.md").GetArtifact().GetTitle(); got != "A globex note." {
		t.Errorf("the note of globex read by cy: %q", got)
	}
	wantList(t, "search acme in globex", wikiSearch(t, m.bob, "acme"), nil)

	// Bob is no member of acme.
	_, err := m.srv.WithTenant(t, m.bobKey.Key, "acme").Wiki.ListArtifacts(ctx10(t), &wikiv1.ListArtifactsRequest{})
	wantCode(t, "bob reads the wiki of acme", err, connect.CodeNotFound)
	_, err = m.srv.Anonymous(t).Wiki.ListArtifacts(ctx10(t), &wikiv1.ListArtifactsRequest{})
	wantCode(t, "a call without a token", err, connect.CodeUnauthenticated)

	sid := "s-" + coreapi.NewCommandID()
	join2(t, m.ada, sid)
	link, err := m.ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	stage := m.srv.WithToken(t, stageToken(t, link.GetUrl()))
	_, err = stage.Wiki.Search(ctx10(t), &wikiv1.SearchRequest{Query: "note"})
	wantCode(t, "a stage link searches the wiki", err, connect.CodePermissionDenied)
}

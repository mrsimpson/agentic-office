package contract

// Contract tests of the front matter of documents and the hints for links that lead nowhere
// (dec-wiki, concept-wiki) through if-documents-check, if-artifact-check, if-document-api and
// if-git-access. A document may name the schema office.document.v1 in its front matter; then its
// fields are checked. A document without front matter stays valid. A write or a push with a link
// that leads nowhere gets a hint and is never refused for it.

import (
	"os"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	artifactcheck "github.com/mrsimpson/agentic-office/hub/artifacts/check"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	documentscheck "github.com/mrsimpson/agentic-office/hub/documents/check"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// fullPage is a document whose front matter has every field of the schema.
const fullPage = `---
schema: office.document.v1
title: ACME GmbH
summary: Customer since 2025; contract, contacts and open topics.
tags: [customer, b2b]
sources:
  - minutes/000012.yaml
  - notes/000031.md
status: draft
owner: ada
reviewed:
  - id: bob
    date: 2026-11-19
  - id: cy.agent
    date: "2026-11-20"
---

# ACME GmbH

The price is 40 EUR per seat (see [the plain page](plain.md)).
`

// TestDocumentFrontMatterCheck: the documents check accepts every field of the schema and refuses
// each wrong field with a reason in plain words. The artifact check gives the same answer for a
// pushed file.
func TestDocumentFrontMatterCheck(t *testing.T) {
	const s, p = "office.document.v1", "documents/acme.md"
	with := func(fields string) string { return "---\nschema: office.document.v1\n" + fields + "---\n# Page\n" }
	cases := []checkCase{
		{"every field", s, p, fullPage, ""},
		{"the schema only", s, p, with(""), ""},
		{"each status draft", s, p, with("status: draft\n"), ""},
		{"each status reviewed", s, p, with("status: reviewed\n"), ""},
		{"each status outdated", s, p, with("status: outdated\n"), ""},
		{"an empty list of tags", s, p, with("tags: []\n"), ""},
		{"a title that is a number", s, p, with("title: 2026\n"), ""},
		{"front matter with CRLF", s, p, strings.ReplaceAll(with("title: A\n"), "\n", "\r\n"), ""},
		{"an unknown field", s, p, with("author: ada\n"), `the field "author" is not a field of a document`},
		{"the field stub", s, p, with("stub: true\n"), "only the stub of a document"},
		{"a field twice", s, p, with("title: A\ntitle: B\n"), "there twice"},
		{"an empty title", s, p, with("title: \"\"\n"), "title must be one line of text"},
		{"a title without a value", s, p, with("title:\n"), "title must be one line of text"},
		{"a title with two lines", s, p, with("title: \"A\\nB\"\n"), "without a line break"},
		{"a title that is a list", s, p, with("title: [a]\n"), "title must be one line of text"},
		{"a summary with two lines", s, p, with("summary: |\n  one\n  two\n"), "summary must be one line"},
		{"tags that are no list", s, p, with("tags: customer\n"), "tags must be a list"},
		{"a blank tag", s, p, with("tags: [\" \"]\n"), "tag 1 of the field tags is blank"},
		{"a tag with spaces", s, p, with("tags: [\" a\"]\n"), "spaces around it"},
		{"a repeated tag", s, p, with("tags: [a, a]\n"), "is repeated"},
		{"a tag that is a list", s, p, with("tags: [[a]]\n"), "must be text"},
		{"sources that are no list", s, p, with("sources: minutes/000012.yaml\n"), "sources must be a list"},
		{"a source outside the repository", s, p, with("sources: [../x.yaml]\n"), "from the top of the repository"},
		{"an absolute source", s, p, with("sources: [/minutes/000012.yaml]\n"), "from the top of the repository"},
		{"a source that is not clean", s, p, with("sources: [minutes/./000012.yaml]\n"), "from the top of the repository"},
		{"a hidden source", s, p, with("sources: [.git/config.yaml]\n"), "hidden element"},
		{"a source of another type", s, p, with("sources: [minutes/000012.json]\n"), "must end in .md, .yaml or .yml"},
		{"a repeated source", s, p, with("sources: [notes/1.md, notes/1.md]\n"), "is repeated"},
		{"an unknown status", s, p, with("status: done\n"), "status must be one of draft, reviewed, outdated"},
		{"an owner that is no participant ID", s, p, with("owner: Ada Lovelace\n"), "owner must be the participant ID"},
		{"reviews that are no list", s, p, with("reviewed: bob\n"), "list of reviews"},
		{"a review that is text", s, p, with("reviewed: [bob]\n"), "review 1 must have the fields id and date"},
		{"a review without a date", s, p, with("reviewed:\n  - id: bob\n"), "review 1 has no date"},
		{"a review without an id", s, p, with("reviewed:\n  - date: 2026-11-19\n"), "review 1 has no id"},
		{"a review with a wrong date", s, p, with("reviewed:\n  - id: bob\n    date: 19.11.2026\n"), "YYYY-MM-DD"},
		{"a review with a day that does not exist", s, p, with("reviewed:\n  - id: bob\n    date: 2026-02-30\n"), "YYYY-MM-DD"},
		{"a review with another field", s, p, with("reviewed:\n  - id: bob\n    date: 2026-11-19\n    note: ok\n"), `review 1 has the field "note"`},
		{"a review by no participant ID", s, p, with("reviewed:\n  - id: \"bob smith\"\n    date: 2026-11-19\n"), "participant ID of the reviewer"},
	}
	runChecks(t, documentscheck.Validate, cases)
	// The artifact check passes a document that names the schema to the documents check. A field
	// that is there twice is broken YAML for the artifact check already.
	for _, c := range cases {
		got := artifactcheck.Validate(c.path, []byte(c.content))
		if c.name == "a field twice" {
			if len(got) == 0 {
				t.Errorf("%s: the artifact check accepts it", c.name)
			}
			continue
		}
		if (c.reason == "") != (len(got) == 0) || !slices.Equal(got, documentscheck.Validate(c.schema, c.path, []byte(c.content))) {
			t.Errorf("%s: the artifact check says %q, the documents check %q", c.name, got, documentscheck.Validate(c.schema, c.path, []byte(c.content)))
		}
	}
	// The check of the documents interface: the same rules as a push.
	for _, c := range []struct {
		name, content, reason string
	}{
		{"no front matter", "# Plan\n\nText.\n", ""},
		{"only a rule at the start", "---\n\nText without a closing rule\n", ""},
		{"front matter without a schema", "---\ntitle: A\nanything: [1, 2]\n---\nText\n", ""},
		{"every field", fullPage, ""},
		{"a wrong field", strings.Replace(fullPage, "status: draft", "status: done", 1), "status must be one of"},
		{"another schema", "---\nschema: office.note.v1\n---\nText\n", "names the schema office.document.v1 or no schema"},
		{"broken front matter", "---\ntitle: [unclosed\n---\nText\n", "not valid YAML"},
	} {
		got := documentscheck.Check("documents/plan.md", []byte(c.content))
		if c.reason == "" && len(got) != 0 || c.reason != "" && !slices.ContainsFunc(got, func(r string) bool { return strings.Contains(r, c.reason) }) {
			t.Errorf("%s: Check gives %q, want %q", c.name, got, c.reason)
		}
	}
}

// TestDocumentLinkHints: the documents check and the artifact check give a hint for every relative
// link of a document that leads to no file, and nothing for links with a scheme, to a place in
// the page, or in code. Only documents get hints.
func TestDocumentLinkHints(t *testing.T) {
	files := map[string]bool{"documents/a.md": true, "documents": true, "documents/area/b.md": true, "documents/area": true,
		"minutes/000012.yaml": true, "minutes": true}
	exists := func(p string) bool { return files[p] }
	page := "---\nschema: office.document.v1\ntitle: \"[x](nowhere-in-front-matter.md)\"\n---\n" +
		"# Page\n\n" +
		"[a](a.md) [a again](./a.md#top) [b](area/b.md) [area](area/) [minutes](../minutes/000012.yaml)\n" +
		"[web](https://example.org/x.md) [mail](mailto:a@example.org) [place](#top) [root](/x.md)\n" +
		"[space](<a.md>) [encoded](area/%62.md)\n\n" +
		"[missing](missing.md) [missing again](missing.md) ![image](img/logo.png) [out](../../etc/passwd)\n\n" +
		"[ref][r]\n\n[r]: area/none.md\n\n" +
		"`[code](code.md)`\n\n```\n[block](block.md)\n```\n"
	got := documentscheck.Hints("documents/page.md", []byte(page), exists)
	want := []string{
		`the link "missing.md" leads nowhere: there is no documents/missing.md`,
		`the link "img/logo.png" leads nowhere: there is no documents/img/logo.png`,
		`the link "../../etc/passwd" leads out of the repository`,
		`the link "area/none.md" leads nowhere: there is no documents/area/none.md`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("hints\n%q\nwant\n%q", got, want)
	}
	if got := artifactcheck.Hints("documents/page.md", []byte(page), exists); !slices.Equal(got, want) {
		t.Errorf("the artifact check gives\n%q\nwant\n%q", got, want)
	}
	for name, c := range map[string]struct{ path, content string }{
		"a note":                 {"notes/000003.md", "[x](missing.md)\n"},
		"a YAML file":            {"documents/a.yaml", "text: \"[x](missing.md)\"\n"},
		"the stub of a document": {"documents/plan.md", string(documentscheck.Stub())},
		"a page without links":   {"documents/plan.md", "# Plan\n"},
	} {
		if got := artifactcheck.Hints(c.path, []byte(c.content), exists); len(got) != 0 {
			t.Errorf("%s: hints %q", name, got)
		}
	}
}

// TestDocumentFrontMatterOnInterface: the documents interface checks the front matter like a push.
// An invalid document is refused with the reasons and writes nothing; a document without front
// matter, or with every field, is written.
func TestDocumentFrontMatterOnInterface(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ctx := ctx10(t)
	create := func(path, content string) (*documentv1.CreateDocumentResponse, error) {
		return ada.Document.CreateDocument(ctx, &documentv1.CreateDocumentRequest{Path: path, Content: content, CommandId: coreapi.NewCommandID()})
	}
	if _, err := create("documents/plain.md", "# Plain\n\nNo front matter.\n"); err != nil {
		t.Fatalf("a document without front matter: %v", err)
	}
	full, err := create("documents/acme.md", fullPage)
	if err != nil {
		t.Fatalf("a document with every field: %v", err)
	}
	for name, c := range map[string]struct{ content, reason string }{
		"a wrong status":      {strings.Replace(fullPage, "status: draft", "status: done", 1), "status must be one of draft, reviewed, outdated"},
		"an unknown field":    {"---\nschema: office.document.v1\nauthor: ada\n---\n# A\n", `the field "author" is not a field of a document`},
		"another schema":      {"---\nschema: office.note.v1\n---\n# A\n", "names the schema office.document.v1 or no schema"},
		"broken front matter": {"---\ntitle: [unclosed\n---\n# A\n", "not valid YAML"},
	} {
		_, err := create("documents/bad.md", c.content)
		wantCode(t, name, err, connect.CodeInvalidArgument)
		if err != nil && !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: the error does not say %q: %v", name, c.reason, err)
		}
	}
	if got := contracttest.Files(t, srv.ArchiveDir()); slices.Contains(got, "documents/bad.md") {
		t.Error("a refused document was written")
	}
	// A change is checked the same way.
	_, err = ada.Document.ChangeDocument(ctx, &documentv1.ChangeDocumentRequest{Path: "documents/acme.md", CommandId: coreapi.NewCommandID(),
		BaseVersion: full.GetDocument().GetVersion(), Content: strings.Replace(fullPage, "owner: ada", "owner: Ada Lovelace", 1)})
	wantCode(t, "a change with a wrong owner", err, connect.CodeInvalidArgument)
	if _, err := ada.Document.ChangeDocument(ctx, &documentv1.ChangeDocumentRequest{Path: "documents/acme.md", CommandId: coreapi.NewCommandID(),
		BaseVersion: full.GetDocument().GetVersion(), Content: strings.Replace(fullPage, "status: draft", "status: reviewed", 1)}); err != nil {
		t.Fatalf("a valid change: %v", err)
	}
}

// TestDocumentHintsOnInterface: a create or a change answers with a hint for every relative link
// that leads nowhere and writes the document all the same. A link to a staged document, whose
// stub lies in the repository, works; so does a link to a released file.
func TestDocumentHintsOnInterface(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	a := m.a(ada)
	ctx := ctx10(t)
	m.stageDocument(ada, "documents/staged.md", "# Staged\n")
	writeDoc(t, a, "documents/area/released.md", "# Released\n")

	content := "# Page\n\n[staged](staged.md) [released](area/released.md) [area](area/) [self](page.md) [web](https://example.org)\n\n" +
		"[missing](missing.md) [up](../nowhere/x.md)\n"
	res, err := a.Document.CreateDocument(ctx, &documentv1.CreateDocumentRequest{Path: "documents/page.md", Content: content, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatalf("a document with broken links is refused: %v", err)
	}
	want := []string{
		`the link "missing.md" leads nowhere: there is no documents/missing.md`,
		`the link "../nowhere/x.md" leads nowhere: there is no nowhere/x.md`,
	}
	if !slices.Equal(res.GetHints(), want) {
		t.Fatalf("hints of the create\n%q\nwant\n%q", res.GetHints(), want)
	}
	if !m.releasedDoc("documents/page.md") {
		t.Fatal("the document with broken links was not written")
	}
	// A staged document gets hints too, and a link to a document of the same session works.
	staged, err := a.Document.CreateDocument(ctx, &documentv1.CreateDocumentRequest{Path: "documents/next.md",
		Content: "[staged](staged.md) [gone](gone.md)\n", CommandId: coreapi.NewCommandID(), SessionId: m.sid})
	if err != nil || !staged.GetDocument().GetStaged() {
		t.Fatalf("stage: %v, %v", staged, err)
	}
	if want := []string{`the link "gone.md" leads nowhere: there is no documents/gone.md`}; !slices.Equal(staged.GetHints(), want) {
		t.Errorf("hints of a staged create %q, want %q", staged.GetHints(), want)
	}
	// A change gets the hints of its new content.
	changed, err := a.Document.ChangeDocument(ctx, &documentv1.ChangeDocumentRequest{Path: "documents/page.md", Content: "[fixed](staged.md) [new](new.md)\n",
		CommandId: coreapi.NewCommandID(), BaseVersion: res.GetDocument().GetVersion()})
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if want := []string{`the link "new.md" leads nowhere: there is no documents/new.md`}; !slices.Equal(changed.GetHints(), want) {
		t.Errorf("hints of the change %q, want %q", changed.GetHints(), want)
	}
	fixed, err := a.Document.ChangeDocument(ctx, &documentv1.ChangeDocumentRequest{Path: "documents/page.md", Content: "[fixed](staged.md)\n",
		CommandId: coreapi.NewCommandID(), BaseVersion: changed.GetDocument().GetVersion()})
	if err != nil || len(fixed.GetHints()) != 0 {
		t.Errorf("a change without broken links: %v, %v", fixed.GetHints(), err)
	}
}

// TestGitPushDocumentFrontMatterAndHints: a push checks the front matter of a document that names
// the schema and refuses invalid fields. A link that leads nowhere is shown as a "remote:" hint and
// never refuses the push. The push sees its own new state, so links between the files of one push
// work, and so does a link to a stub.
func TestGitPushDocumentFrontMatterAndHints(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	const tenant = contracttest.DefaultTenant
	m.stageDocument(ada, "documents/staged.md", "# Staged\n")
	p := newPusher(t, m.srv, m.a(ada), "Ada", tenant)

	// Invalid front matter is refused with the reason.
	before := mainOf(t, m.srv, tenant)
	p.commitFile("documents/acme.md", strings.Replace(fullPage, "status: draft", "status: done", 1))
	out, code := p.push()
	refused(t, m.srv, tenant, before, out, code, "remote:", "documents/acme.md: the field status must be one of draft, reviewed, outdated")
	p.mustGit("reset", "-q", "--hard", "origin/main")

	// Valid front matter, a document without front matter, and links between the files of the
	// push and to the stub: no hints.
	p.write("documents/acme.md", fullPage)
	p.write("documents/plain.md", "# Plain\n\n[acme](acme.md) [staged](staged.md) [data](../data/ref.yaml)\n")
	p.write("data/ref.yaml", "a: 1\n") // any YAML without a schema
	p.commit("pages")
	out, code = p.push()
	if code != 0 {
		t.Fatalf("push: %d\n%s", code, out)
	}
	if strings.Contains(out, "hint") {
		t.Errorf("hints for links that work:\n%s", out)
	}

	// Broken links: the push is accepted and shows a hint for each.
	p.commitFile("documents/broken.md", "# Broken\n\n[missing](missing.md) [ok](plain.md)\n")
	out, code = p.push()
	if code != 0 {
		t.Fatalf("a push with a broken link is refused: %d\n%s", code, out)
	}
	if !strings.Contains(out, `remote:   - documents/broken.md: the link "missing.md" leads nowhere: there is no documents/missing.md`) {
		t.Errorf("the push does not show the hint:\n%s", out)
	}
	if strings.Contains(out, "plain.md\" leads nowhere") {
		t.Errorf("a hint for a link that works:\n%s", out)
	}
	if !m.releasedDoc("documents/broken.md") {
		t.Error("the document with a broken link is not on main")
	}
}

// TestMemorySkillPageIsValid: the example page of the skill office-memory follows the schema of
// documents, so an agent that copies it writes a valid page.
func TestMemorySkillPageIsValid(t *testing.T) {
	skill, err := os.ReadFile("../../skills/office-memory/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(skill), "```markdown\n")
	page, _, closed := strings.Cut(rest, "\n```\n")
	if !ok || !closed || !strings.Contains(page, "schema: office.document.v1") {
		t.Fatalf("the skill has no example page that names the schema:\n%s", page)
	}
	if got := artifactcheck.Validate("documents/customers/acme.md", []byte(page+"\n")); len(got) != 0 {
		t.Fatalf("the example page of the skill is invalid: %q", got)
	}
}

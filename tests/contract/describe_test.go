package contract

// Contract tests of the descriptions of the artifacts for the wiki (dec-wiki, concept-wiki)
// through the four description interfaces if-records-describe, if-tasks-describe,
// if-slides-describe and if-documents-describe, and the description call of if-artifact-check. Each part describes the files of its own types:
// title, summary, tags, outgoing links, sources and whether a file is a stub. The artifact
// description passes a file to the part that owns it. The descriptions are in-process Go calls,
// so the tests call them directly and use nothing but their signatures.

import (
	"reflect"
	"strings"
	"testing"

	artifactcheck "github.com/mrsimpson/agentic-office/hub/artifacts/check"
	documentscheck "github.com/mrsimpson/agentic-office/hub/documents/check"
	documentsdescribe "github.com/mrsimpson/agentic-office/hub/documents/describe"
	recordscheck "github.com/mrsimpson/agentic-office/hub/records/check"
	recordsdescribe "github.com/mrsimpson/agentic-office/hub/records/describe"
	slidescheck "github.com/mrsimpson/agentic-office/hub/slides/check"
	slidesdescribe "github.com/mrsimpson/agentic-office/hub/slides/describe"
	tasksdescribe "github.com/mrsimpson/agentic-office/hub/tasks/describe"
)

// linkedMinutes are minutes whose text is Markdown with a heading and links of every kind.
const linkedMinutes = `schema: office.minutes.v1
id: 7
session_id: s1
text: |-
  # Weekly *sync*

  We agreed on [the plan](../documents/plan.md) and checked [a site](https://example.org).

  - Ask [the bank](../notes/000003.md#call) again.
  - See [the plan](../documents/plan.md) once more, [the top](#top),
    [a page](/documents/x.md) and [outside](../../x.md).
  - ![a chart](../documents/chart.png)
author_participant_id: ada
author_kind: AUTHOR_KIND_AGENT
time: "2026-11-16T10:00:00Z"
command_id: c-7
`

// TestRecordsDescription: a record is described by its text and, for a note, its tags. The first
// block of the text is the title, the rest is the summary, and the relative links are paths from
// the top of the repository.
func TestRecordsDescription(t *testing.T) {
	const d, m, n = "office.decision.v1", "office.minutes.v1", "office.note.v1"
	cases := []struct {
		name    string
		schema  string
		path    string
		content string
		want    recordsdescribe.Description
	}{
		{"a decision", d, "decisions/000001.yaml", decisionFile,
			recordsdescribe.Description{Title: "Use Go for every component."}},
		{"a note with tags", n, "notes/000003.md", noteFile,
			recordsdescribe.Description{Title: "Call the bank.", Tags: []string{"bank"}}},
		{"minutes with a heading and links", m, "minutes/000007.yaml", linkedMinutes,
			recordsdescribe.Description{
				Title: "Weekly sync",
				Summary: "We agreed on the plan and checked a site. Ask the bank again. " +
					"See the plan once more, the top, a page and outside.",
				Links: []string{"documents/plan.md", "notes/000003.md"},
			}},
		{"a note whose text links to a page", n, "notes/000003.md",
			replace(t, noteFile, "Call the bank.\n", "Call the bank.\n\nThe terms are on [the page](../documents/bank.md).\n"),
			recordsdescribe.Description{Title: "Call the bank.", Summary: "The terms are on the page.",
				Tags: []string{"bank"}, Links: []string{"documents/bank.md"}}},
		{"the stub of a decision", d, "decisions/000002.yaml", string(recordscheck.Stub(d)),
			recordsdescribe.Description{Stub: true}},
		{"the stub of minutes", m, "minutes/000002.yaml", string(recordscheck.Stub(m)),
			recordsdescribe.Description{Stub: true}},
		{"the stub of a note", n, "notes/000002.md", string(recordscheck.Stub(n)),
			recordsdescribe.Description{Stub: true}},
	}
	for _, c := range cases {
		got, ok := recordsdescribe.Describe(c.schema, c.path, []byte(c.content))
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Describe = %+v, %v; want %+v, true", c.name, got, ok, c.want)
		}
	}

	long := replace(t, decisionFile, "Use Go for every component.", strings.Repeat("word ", 40)+"\n\n  "+strings.Repeat("more ", 60))
	long = replace(t, long, "text: ", "text: |-\n  ")
	got, ok := recordsdescribe.Describe(d, "decisions/000001.yaml", []byte(long))
	if !ok {
		t.Fatalf("a decision with a long text is not described")
	}
	if r := []rune(got.Title); len(r) > recordsdescribe.MaxTitle || !strings.HasSuffix(got.Title, "…") {
		t.Errorf("a long title must be cut to %d characters and end in …: %q", recordsdescribe.MaxTitle, got.Title)
	}
	if r := []rune(got.Summary); len(r) > recordsdescribe.MaxSummary || !strings.HasSuffix(got.Summary, "…") {
		t.Errorf("a long summary must be cut to %d characters and end in …: %q", recordsdescribe.MaxSummary, got.Summary)
	}

	refused := []struct{ name, schema, path, content string }{
		{"a schema of another part", "office.task.v1", "tasks/000002.yaml", taskFile},
		{"a file that the check refuses", d, "decisions/000001.yaml", replace(t, decisionFile, "command_id: c-1\n", "")},
		{"a record at the wrong path", d, "decisions/000009.yaml", decisionFile},
		{"the stub of another type", d, "decisions/000002.yaml", string(recordscheck.Stub(m))},
	}
	for _, c := range refused {
		if got, ok := recordsdescribe.Describe(c.schema, c.path, []byte(c.content)); ok {
			t.Errorf("%s: Describe = %+v, true; want not described", c.name, got)
		}
	}
}

// TestTasksDescription: a task is described by its title and its description.
func TestTasksDescription(t *testing.T) {
	const s = "office.task.v1"
	got, ok := tasksdescribe.Describe(s, "tasks/000002.yaml", []byte(taskFile))
	if want := (tasksdescribe.Description{Title: "Buy milk"}); !ok || got != want {
		t.Errorf("Describe = %+v, %v; want %+v, true", got, ok, want)
	}
	withText := replace(t, taskFile, "status:", "description: |-\n  Two litres,\n  low fat.\nstatus:")
	got, ok = tasksdescribe.Describe(s, "tasks/000002.yaml", []byte(withText))
	if want := (tasksdescribe.Description{Title: "Buy milk", Summary: "Two litres, low fat."}); !ok || got != want {
		t.Errorf("with a description: Describe = %+v, %v; want %+v, true", got, ok, want)
	}
	for name, c := range map[string]struct{ schema, path, content string }{
		"a schema of another part":      {"office.slides.v1", "slides/000004.yaml", deckFile},
		"a file that the check refuses": {s, "tasks/000002.yaml", replace(t, taskFile, "title: Buy milk\n", "")},
	} {
		if got, ok := tasksdescribe.Describe(c.schema, c.path, []byte(c.content)); ok {
			t.Errorf("%s: Describe = %+v, true; want not described", name, got)
		}
	}
}

// TestSlidesDescription: a deck is described by its title and the titles of its slides; its stub
// as a stub only.
func TestSlidesDescription(t *testing.T) {
	const s = "office.slides.v1"
	twoSlides := replace(t, deckFile, "author_participant_id", "  - title: Costs\nauthor_participant_id")
	cases := []struct {
		name, path, content string
		want                slidesdescribe.Description
	}{
		{"a deck", "slides/000004.yaml", twoSlides, slidesdescribe.Description{Title: "Budget", Summary: "Plan; Costs"}},
		{"a deck without a title", "slides/000004.yaml", replace(t, twoSlides, "title: Budget\n", ""),
			slidesdescribe.Description{Title: "Plan", Summary: "Plan; Costs"}},
		{"the stub of a deck", "slides/000005.yaml", string(slidescheck.Stub()), slidesdescribe.Description{Stub: true}},
	}
	for _, c := range cases {
		got, ok := slidesdescribe.Describe(s, c.path, []byte(c.content))
		if !ok || got != c.want {
			t.Errorf("%s: Describe = %+v, %v; want %+v, true", c.name, got, ok, c.want)
		}
	}
	for name, c := range map[string]struct{ schema, path, content string }{
		"a schema of another part":      {"office.task.v1", "tasks/000002.yaml", taskFile},
		"a file that the check refuses": {s, "slides/000004.yaml", replace(t, deckFile, "command_id: c-1\n", "")},
	} {
		if got, ok := slidesdescribe.Describe(c.schema, c.path, []byte(c.content)); ok {
			t.Errorf("%s: Describe = %+v, true; want not described", name, got)
		}
	}
}

// TestDocumentsDescription: a document is described by its front matter and its body: the title
// from the front matter, else the first heading, else the name of the file; summary, tags,
// sources, status, owner and reviews from the front matter of the schema; links from the body.
func TestDocumentsDescription(t *testing.T) {
	const s = "office.document.v1"
	cases := []struct {
		name, schema, path, content string
		want                        documentsdescribe.Description
	}{
		{"a page with every field", s, "documents/customers/acme.md", fullPage, documentsdescribe.Description{
			Title:   "ACME GmbH",
			Summary: "Customer since 2025; contract, contacts and open topics.",
			Tags:    []string{"customer", "b2b"},
			Sources: []string{"minutes/000012.yaml", "notes/000031.md"},
			Links:   []string{"documents/customers/plain.md"},
			Status:  "draft",
			Owner:   "ada",
			Reviews: []documentsdescribe.Review{{ParticipantID: "bob", Date: "2026-11-19"}, {ParticipantID: "cy.agent", Date: "2026-11-20"}},
		}},
		{"a page without front matter", "", "documents/customers/README.md",
			"Intro text.\n\n## The *customers* of `2026`\n\n- [ACME](acme.md#contacts)\n- [Beta](./beta.md) and [ACME](acme.md)\n" +
				"- [Minutes](../../minutes/000012.yaml), [web](https://example.org), [mail](mailto:a@b.c)\n" +
				"- [here](#top), [root](/x.md), [out](../../../x.md), [self](README.md)\n- ![logo](logo.png)\n",
			documentsdescribe.Description{
				Title: "The customers of 2026",
				Links: []string{"documents/customers/acme.md", "documents/customers/beta.md", "minutes/000012.yaml"},
			}},
		{"a page that names the schema without a title", s, "documents/pricing.md",
			"---\nschema: office.document.v1\nsummary: What we charge.\nsources: [decisions/000004.yaml]\n---\n# Pricing\n",
			documentsdescribe.Description{Title: "Pricing", Summary: "What we charge.", Sources: []string{"decisions/000004.yaml"}}},
		{"front matter without the schema is not read", "", "documents/x.md",
			"---\ntitle: Other tool\nsummary: no\ntags: [a]\nstatus: done\nowner: x\n---\n# Heading\n",
			documentsdescribe.Description{Title: "Heading"}},
		{"a page without title and heading", "", "documents/area/notes-on-x.md", "Just text.\n",
			documentsdescribe.Description{Title: "notes-on-x"}},
		{"the stub of a document", s, "documents/secret.md", string(documentscheck.Stub()),
			documentsdescribe.Description{Stub: true}},
	}
	for _, c := range cases {
		got, ok := documentsdescribe.Describe(c.schema, c.path, []byte(c.content))
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Describe = %+v, %v; want %+v, true", c.name, got, ok, c.want)
		}
	}
	refused := []struct{ name, schema, path, content string }{
		{"a file outside the documents", "", "notes/x.md", "# X\n"},
		{"a YAML file under the documents", "", "documents/x.yaml", "a: 1\n"},
		{"a schema of another part", "office.note.v1", "documents/x.md", noteFile},
		{"a page that the check refuses", s, "documents/x.md", "---\nschema: office.document.v1\nstatus: done\n---\n# X\n"},
		{"front matter that is not YAML", "", "documents/x.md", "---\n: : [\n---\n# X\n"},
	}
	for _, c := range refused {
		if got, ok := documentsdescribe.Describe(c.schema, c.path, []byte(c.content)); ok {
			t.Errorf("%s: Describe = %+v, true; want not described", c.name, got)
		}
	}
}

// TestArtifactDescription: the description of the artifact check passes every file to the part that owns its
// type and gives the same answer as that part, with the schema that the file names.
func TestArtifactDescription(t *testing.T) {
	cases := []struct {
		name, path, content string
		want                artifactcheck.Description
	}{
		{"a decision", "decisions/000001.yaml", decisionFile, artifactcheck.Description{
			Schema: "office.decision.v1", Title: "Use Go for every component.", Source: true}},
		{"minutes", "minutes/000007.yaml", linkedMinutes, artifactcheck.Description{
			Schema: "office.minutes.v1", Title: "Weekly sync",
			Summary: "We agreed on the plan and checked a site. Ask the bank again. " +
				"See the plan once more, the top, a page and outside.",
			Links: []string{"documents/plan.md", "notes/000003.md"}, Source: true}},
		{"a note", "notes/000003.md", noteFile, artifactcheck.Description{
			Schema: "office.note.v1", Title: "Call the bank.", Tags: []string{"bank"}, Source: true}},
		{"a task", "tasks/000002.yaml", taskFile, artifactcheck.Description{
			Schema: "office.task.v1", Title: "Buy milk"}},
		{"a deck", "slides/000004.yaml", deckFile, artifactcheck.Description{
			Schema: "office.slides.v1", Title: "Budget", Summary: "Plan"}},
		{"a page of the schema", "documents/customers/acme.md", fullPage, artifactcheck.Description{
			Schema: "office.document.v1", Title: "ACME GmbH",
			Summary: "Customer since 2025; contract, contacts and open topics.",
			Tags:    []string{"customer", "b2b"},
			Sources: []string{"minutes/000012.yaml", "notes/000031.md"},
			Links:   []string{"documents/customers/plain.md"},
			Status:  "draft", Owner: "ada",
			Reviews: []artifactcheck.Review{{ParticipantID: "bob", Date: "2026-11-19"}, {ParticipantID: "cy.agent", Date: "2026-11-20"}}}},
		{"a page without a schema", "documents/plain.md", "# Plain\n\nSee [ACME](customers/acme.md).\n",
			artifactcheck.Description{Title: "Plain", Links: []string{"documents/customers/acme.md"}}},
		{"the stub of a note", "notes/000002.md", string(recordscheck.Stub("office.note.v1")),
			artifactcheck.Description{Schema: "office.note.v1", Stub: true}},
		{"the stub of a deck", "slides/000005.yaml", string(slidescheck.Stub()),
			artifactcheck.Description{Schema: "office.slides.v1", Stub: true}},
		{"the stub of a document", "documents/secret.md", string(documentscheck.Stub()),
			artifactcheck.Description{Schema: "office.document.v1", Stub: true}},
	}
	for _, c := range cases {
		got, ok := artifactcheck.Describe(c.path, []byte(c.content))
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Describe = %+v, %v; want %+v, true", c.name, got, ok, c.want)
		}
	}
	refused := []struct{ name, path, content string }{
		{"a schema that no part owns", "x/1.yaml", "schema: office.unknown.v1\n"},
		{"a file that the check of its owner refuses", "decisions/000001.yaml", replace(t, decisionFile, "command_id: c-1\n", "")},
		{"a YAML file without a schema", "config/x.yaml", "a: 1\n"},
		{"Markdown without a schema outside the documents", "README.md", "# Read me\n"},
		{"a file with another ending", "documents/x.txt", "text"},
		{"a path outside the repository", "../x.md", "# X\n"},
		{"YAML that is not valid", "x.yaml", "a: [\n"},
	}
	for _, c := range refused {
		if got, ok := artifactcheck.Describe(c.path, []byte(c.content)); ok {
			t.Errorf("%s: Describe = %+v, true; want not described", c.name, got)
		}
	}
}

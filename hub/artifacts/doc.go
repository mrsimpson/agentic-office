// Package artifacts is the building block Artifacts (bb-artifacts in
// docs/architecture/05-building-blocks.arc42.md). It knows which part of the hub owns which schema
// and passes the check of a file to that part (dec-artifact-sync).
//
// How it works:
//
//   - The block owns no schema. Each part that owns artifact types names its schemas, checks its
//     files and recognises the stubs of its types through its own check interface
//     (if-records-check, if-tasks-check, if-slides-check, if-documents-check).
//   - The list parts holds one line for each of these parts. A new type needs a check in its own
//     part and one line in this list. Two parts that name the same schema stop the start of the
//     program, because the owner would be unclear.
//   - Owner finds the part of a schema by its name. The interface of the block (package check,
//     if-artifact-check) reads the schema name that a file names, asks Owner and passes the file
//     on.
//   - A part may also give hints for the files of its types, such as links that lead nowhere
//     (concept-wiki). A hint never makes a file invalid. Hinters returns the parts that give
//     hints; each part decides by itself which files are of its types.
//   - A part may mark its files as files that no agent may change (Part.NoAgents). Git access
//     asks this through if-artifact-check (NoAgents) for the old and the new state of every path
//     that an agent pushes. Tenants does so for the definition of a team agent (dec-team-agents).
//   - Each part describes the files of its types for the wiki (dec-wiki): title, summary, tags,
//     outgoing links, sources, the state of review of a document and whether a file is a stub, through its own description
//     interface (if-records-describe, if-tasks-describe, if-slides-describe,
//     if-documents-describe). The interface if-artifact-check (Describe of package check) asks the
//     owner of the schema that a file names. A file that names no schema goes to the parts in
//     the order of the list, and the first part that describes it wins; only the documents part
//     describes files without a schema.
package artifacts

import (
	"fmt"
	"slices"

	documentscheck "github.com/mrsimpson/agentic-office/hub/documents/check"
	documentsdescribe "github.com/mrsimpson/agentic-office/hub/documents/describe"
	recordscheck "github.com/mrsimpson/agentic-office/hub/records/check"
	recordsdescribe "github.com/mrsimpson/agentic-office/hub/records/describe"
	slidescheck "github.com/mrsimpson/agentic-office/hub/slides/check"
	slidesdescribe "github.com/mrsimpson/agentic-office/hub/slides/describe"
	taskscheck "github.com/mrsimpson/agentic-office/hub/tasks/check"
	tasksdescribe "github.com/mrsimpson/agentic-office/hub/tasks/describe"
	tenantscheck "github.com/mrsimpson/agentic-office/hub/tenant/check"
)

// Check checks the content of the file at path against the schema. It returns the reasons in
// plain words, or nothing when the file is valid.
type Check func(schema, path string, content []byte) []string

// StubCheck reports whether content is the stub of a staged artifact of the schema
// (concept-session-release).
type StubCheck func(schema string, content []byte) bool

// HintCheck returns hints in plain words for the file at path, or nothing when the file is not of
// a type of the part. exists reports whether a clean path inside the repository is a file or a
// directory with files.
type HintCheck func(path string, content []byte, exists func(string) bool) []string

// Description is what a part tells about one file of its types. The fields are those of the
// description of the interface if-artifact-check.
type Description struct {
	Title   string
	Summary string
	Tags    []string
	Links   []string
	Sources []string
	// Source is true for a source of the wiki: a record (decision, minutes or note) that a
	// document may cite (dec-wiki).
	Source bool
	// Status, Owner and Reviews are the state of review of a document; only documents have them.
	Status  string
	Owner   string
	Reviews []Review
	Stub    bool
}

// Review is one review of a document: the participant ID of the reviewer and the day.
type Review struct {
	ParticipantID string
	Date          string
}

// Describer describes the file at path with the given content as a file of the schema; schema is
// "" when the file names none. ok is false when the file is not of a type of the part, or the
// part refuses it.
type Describer func(schema, path string, content []byte) (d Description, ok bool)

// Part is what the artifacts part knows of a part of the hub that owns schemas.
type Part struct {
	Name     string
	Validate Check
	IsStub   StubCheck
	Hints    HintCheck // nil: the part gives no hints
	Describe Describer
	// NoAgents is set for a part whose files no agent may change (dec-team-agents).
	NoAgents bool
}

type entry struct {
	Part
	schemas func() []string
}

// noStubs is the stub check of a part whose types are never staged.
func noStubs(string, []byte) bool { return false }

// parts is the fixed list of the owners of schemas. A new type adds one line.
var parts = []entry{
	{Part{"records", recordscheck.Validate, recordscheck.IsStub, nil, describeRecord, false}, recordscheck.Schemas},
	{Part{"tasks", taskscheck.Validate, noStubs, nil, describeTask, false}, taskscheck.Schemas},
	{Part{"slides", slidescheck.Validate, func(_ string, c []byte) bool { return slidescheck.IsStub(c) }, nil, describeDeck, false}, slidescheck.Schemas},
	{Part{"documents", documentscheck.Validate, func(_ string, c []byte) bool { return documentscheck.IsStub(c) }, documentscheck.Hints, describeDocument, false}, documentscheck.Schemas},
	// The definition of a team agent is configuration of the tenant: never staged, never described
	// for the wiki, and never changed by an agent (dec-team-agents).
	{Part{"tenants", tenantscheck.Validate, noStubs, nil, noDescription, true}, tenantscheck.Schemas},
}

// noDescription is the describer of a part whose files the wiki does not show.
func noDescription(string, string, []byte) (Description, bool) { return Description{}, false }

// The describers take the description of each part into the form of this part.

func describeRecord(schema, p string, c []byte) (Description, bool) {
	d, ok := recordsdescribe.Describe(schema, p, c)
	// Every record that is not a stub is a source of the wiki (dec-wiki).
	return Description{Title: d.Title, Summary: d.Summary, Tags: d.Tags, Links: d.Links, Source: ok && !d.Stub, Stub: d.Stub}, ok
}

func describeTask(schema, p string, c []byte) (Description, bool) {
	d, ok := tasksdescribe.Describe(schema, p, c)
	return Description{Title: d.Title, Summary: d.Summary}, ok
}

func describeDeck(schema, p string, c []byte) (Description, bool) {
	d, ok := slidesdescribe.Describe(schema, p, c)
	return Description{Title: d.Title, Summary: d.Summary, Stub: d.Stub}, ok
}

func describeDocument(schema, p string, c []byte) (Description, bool) {
	d, ok := documentsdescribe.Describe(schema, p, c)
	var reviews []Review
	for _, r := range d.Reviews {
		reviews = append(reviews, Review{ParticipantID: r.ParticipantID, Date: r.Date})
	}
	return Description{Title: d.Title, Summary: d.Summary, Tags: d.Tags, Links: d.Links, Sources: d.Sources,
		Status: d.Status, Owner: d.Owner, Reviews: reviews, Stub: d.Stub}, ok
}

// Describers returns the parts in the order of the list.
func Describers() []Part {
	out := make([]Part, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.Part)
	}
	return out
}

// owners maps the name of a schema to the part that owns it.
var owners = func() map[string]Part {
	m := map[string]Part{}
	for _, p := range parts {
		for _, s := range p.schemas() {
			if o, dup := m[s]; dup {
				panic(fmt.Sprintf("artifacts: the schema %q has more than one owner (%s and %s)", s, o.Name, p.Name))
			}
			m[s] = p.Part
		}
	}
	return m
}()

// Owner returns the part that owns the schema, and false if no part owns it.
func Owner(schema string) (Part, bool) {
	p, ok := owners[schema]
	return p, ok
}

// Hinters returns the parts that give hints for their files, in the order of the list.
func Hinters() []Part {
	var out []Part
	for _, p := range parts {
		if p.Hints != nil {
			out = append(out, p.Part)
		}
	}
	return out
}

// Schemas returns the names of all schemas that a part owns, sorted.
func Schemas() []string {
	var out []string
	for s := range owners {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

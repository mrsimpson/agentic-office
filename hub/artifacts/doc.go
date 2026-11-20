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
package artifacts

import (
	"fmt"
	"slices"

	documentscheck "github.com/mrsimpson/agentic-office/hub/documents/check"
	recordscheck "github.com/mrsimpson/agentic-office/hub/records/check"
	slidescheck "github.com/mrsimpson/agentic-office/hub/slides/check"
	taskscheck "github.com/mrsimpson/agentic-office/hub/tasks/check"
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

// Part is what the artifacts part knows of a part of the hub that owns schemas.
type Part struct {
	Name     string
	Validate Check
	IsStub   StubCheck
	Hints    HintCheck // nil: the part gives no hints
}

type entry struct {
	Part
	schemas func() []string
}

// noStubs is the stub check of a part whose types are never staged.
func noStubs(string, []byte) bool { return false }

// parts is the fixed list of the owners of schemas. A new type adds one line.
var parts = []entry{
	{Part{"records", recordscheck.Validate, recordscheck.IsStub, nil}, recordscheck.Schemas},
	{Part{"tasks", taskscheck.Validate, noStubs, nil}, taskscheck.Schemas},
	{Part{"slides", slidescheck.Validate, func(_ string, c []byte) bool { return slidescheck.IsStub(c) }, nil}, slidescheck.Schemas},
	{Part{"documents", documentscheck.Validate, func(_ string, c []byte) bool { return documentscheck.IsStub(c) }, documentscheck.Hints}, documentscheck.Schemas},
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

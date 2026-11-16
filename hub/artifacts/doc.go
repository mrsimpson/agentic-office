// Package artifacts is the building block Artifacts (bb-artifacts in
// docs/architecture/05-building-blocks.arc42.md). It knows which part of the hub owns which schema
// and passes the check of a file to that part (dec-artifact-sync).
//
// How it works:
//
//   - The block owns no schema. Each part that owns artifact types names its schemas and checks
//     its files through its own check interface (if-records-check, if-tasks-check,
//     if-slides-check).
//   - The list parts holds one line for each of these parts. A new type needs a check in its own
//     part and one line in this list. Two parts that name the same schema stop the start of the
//     program, because the owner would be unclear.
//   - Owner finds the check of a schema by its name. The interface of the block (package check,
//     if-artifact-check) reads the schema name that a file names, asks Owner and passes the file
//     on.
package artifacts

import (
	"fmt"
	"slices"

	recordscheck "github.com/mrsimpson/agentic-office/hub/records/check"
	slidescheck "github.com/mrsimpson/agentic-office/hub/slides/check"
	taskscheck "github.com/mrsimpson/agentic-office/hub/tasks/check"
)

// Check checks the content of the file at path against the schema. It returns the reasons in
// plain words, or nothing when the file is valid.
type Check func(schema, path string, content []byte) []string

// part is a part of the hub that owns schemas.
type part struct {
	name     string
	schemas  func() []string
	validate Check
}

// parts is the fixed list of the owners of schemas. A new type adds one line.
var parts = []part{
	{"records", recordscheck.Schemas, recordscheck.Validate},
	{"tasks", taskscheck.Schemas, taskscheck.Validate},
	{"slides", slidescheck.Schemas, slidescheck.Validate},
}

// owners maps the name of a schema to the check of the part that owns it.
var owners = func() map[string]Check {
	m := map[string]Check{}
	for _, p := range parts {
		for _, s := range p.schemas() {
			if _, dup := m[s]; dup {
				panic(fmt.Sprintf("artifacts: the schema %q has more than one owner (%s is one of them)", s, p.name))
			}
			m[s] = p.validate
		}
	}
	return m
}()

// Owner returns the check of the part that owns the schema, and false if no part owns it.
func Owner(schema string) (Check, bool) {
	c, ok := owners[schema]
	return c, ok
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

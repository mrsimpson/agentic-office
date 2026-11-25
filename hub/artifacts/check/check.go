// Package check is the interface if-artifact-check of the building block Artifacts: it checks one
// file against the schema that the file names, whichever part owns the schema
// (dec-artifact-sync).
//
// The signatures use only types that Go has built in and the type Description of this package,
// so a caller needs no other type of the hub.
//
// The rules:
//
//   - An artifact is a Markdown file (.md) or a YAML file (.yaml or .yml). A file with another
//     ending is invalid.
//   - A YAML file names its schema in the top-level field "schema". A Markdown file names it in
//     the field "schema" of its YAML front matter, which is the YAML between a first line "---"
//     and the next line "---".
//   - A file that names a schema is checked by the part that owns the schema. A schema that no
//     part owns is invalid.
//   - A file that names no schema is valid when it is valid Markdown (any text in UTF-8) or valid
//     YAML.
//   - A file is the stub of a staged artifact (concept-session-release) when it names a schema and
//     the part that owns the schema recognises it as the stub of its type (IsStub). Git access
//     asks this for the old state of every path that a push changes or removes.
//   - No agent may change a file of a schema that the owner keeps from agents (NoAgents).
//   - The parts may give hints for a file, such as a link of a document that leads nowhere
//     (Hints). A hint never makes a file invalid. Git access shows them with a push.
//   - The part that owns the type of a file describes it for the wiki (Describe): title,
//     summary, tags, outgoing links, sources and whether it is a stub.
package check

import (
	"fmt"
	"strings"

	"github.com/mrsimpson/agentic-office/hub/artifacts"
)

// SchemaKey is the field that names the schema of a file.
const SchemaKey = artifacts.SchemaKey

// Validate checks the file at filePath, a path relative to the repository of the tenant, with the
// given content. It returns the reasons in plain words, or nothing when the file is valid.
func Validate(filePath string, content []byte) []string {
	clean, schema, named, err := schemaOf(filePath, content)
	if err != nil {
		return []string{err.Error()}
	}
	if !named {
		return nil
	}
	owner, ok := artifacts.Owner(schema)
	if !ok {
		return []string{fmt.Sprintf("the schema %q is not known; the schemas are %s", schema, strings.Join(artifacts.Schemas(), ", "))}
	}
	return owner.Validate(schema, clean, content)
}

// IsStub reports whether the file at filePath with the given content is the stub of a staged
// artifact (concept-session-release): the file names a schema, and the part that owns the schema
// recognises the content as the stub of its type. A file that is not valid is never a stub.
func IsStub(filePath string, content []byte) bool {
	clean, schema, named, err := schemaOf(filePath, content)
	if err != nil || !named {
		return false
	}
	owner, ok := artifacts.Owner(schema)
	return ok && owner.IsStub(schema, content) && len(owner.Validate(schema, clean, content)) == 0
}

// NoAgents reports whether no agent may change the file at filePath with the given content: the
// file names a schema, and the part that owns the schema keeps its files from agents, such as the
// definition of a team agent (dec-team-agents). Git access asks this for the old and the new
// state of every path that an agent pushes. A file that is not valid, or that names no schema,
// is not one of them.
func NoAgents(filePath string, content []byte) bool {
	_, schema, named, err := schemaOf(filePath, content)
	if err != nil || !named {
		return false
	}
	owner, ok := artifacts.Owner(schema)
	return ok && owner.NoAgents
}

// Hints returns hints in plain words for the file at filePath, a path relative to the repository
// of the tenant, with the given content: what the parts of the hub find worth a look, such as a
// link of a document that leads nowhere. exists reports whether a clean path inside the
// repository is a file or a directory with files, in the state that the file belongs to. A hint
// never makes a file invalid.
func Hints(filePath string, content []byte, exists func(path string) bool) []string {
	clean, _, _, err := schemaOf(filePath, content)
	if err != nil {
		return nil
	}
	var hints []string
	for _, p := range artifacts.Hinters() {
		hints = append(hints, p.Hints(clean, content, exists)...)
	}
	return hints
}

// schemaOf returns the clean path of a file and the schema that the file names, if any.
func schemaOf(filePath string, content []byte) (clean, schema string, named bool, err error) {
	return artifacts.SchemaOf(filePath, content)
}

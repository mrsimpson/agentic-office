// Package describe is the interface if-tasks-describe of the building block Tasks
// (docs/architecture/05-building-blocks.arc42.md). It describes one file of a task for the wiki
// (concept-wiki, dec-wiki): its title and summary. The tasks part owns the fields of its type, so
// no other part reads them (dec-artifact-sync).
//
// The rules:
//
//   - Only a file that the check of the tasks accepts for the schema is described.
//   - The title is the title of the task.
//   - The summary is the description of the task, with all white space as single spaces, cut to
//     MaxSummary characters. A text that is cut ends in "…".
//   - A task has no tags and no links, and it is never staged, so it has no stub.
//
// The signatures use only types that Go has built in and the type Description of this package.
package describe

import (
	"path"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mrsimpson/agentic-office/hub/tasks/check"
)

// MaxSummary is the longest summary, in characters.
const MaxSummary = 200

// Description is what the tasks part tells about one task.
type Description struct {
	Title   string
	Summary string
}

// task holds the fields of a task file that a description uses.
type task struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

// Describe describes the file at filePath, a path relative to the repository of the tenant, with
// the given content, as a task of the schema. ok is false when the schema is not the schema of
// tasks or the check of the tasks refuses the file.
func Describe(schema, filePath string, content []byte) (d Description, ok bool) {
	if len(check.Validate(schema, path.Clean(filePath), content)) > 0 {
		return Description{}, false
	}
	var v task
	if err := yaml.Unmarshal(content, &v); err != nil {
		return Description{}, false
	}
	d.Title = strings.Join(strings.Fields(v.Title), " ")
	d.Summary = cut(strings.Join(strings.Fields(v.Description), " "), MaxSummary)
	return d, true
}

// cut shortens s to max characters; a text that is cut ends in "…".
func cut(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

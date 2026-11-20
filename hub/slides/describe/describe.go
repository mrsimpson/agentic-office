// Package describe is the interface if-slides-describe of the building block Slides
// (docs/architecture/05-building-blocks.arc42.md). It describes one file of a slide deck for the
// wiki (concept-wiki, dec-wiki): its title and summary, and whether it is the stub of a staged
// deck. The slides part owns the fields of its type, so no other part reads them
// (dec-artifact-sync).
//
// The rules:
//
//   - Only a file that the check of the slides accepts for the schema is described.
//   - The stub of a deck is described as a stub only: no title and no summary.
//   - The title is the title of the deck. A deck without a title takes the title of its first
//     slide.
//   - The summary is the titles of the slides, separated by "; ", cut to MaxSummary characters.
//     A text that is cut ends in "…".
//   - A deck has no tags and no links.
//
// The signatures use only types that Go has built in and the type Description of this package.
package describe

import (
	"path"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mrsimpson/agentic-office/hub/slides/check"
)

// MaxSummary is the longest summary, in characters.
const MaxSummary = 200

// Description is what the slides part tells about one deck.
type Description struct {
	Title   string
	Summary string
	// Stub is true for the stub of a deck. A stub has no other field.
	Stub bool
}

// deck holds the fields of a deck file that a description uses.
type deck struct {
	Title  string `yaml:"title"`
	Slides []struct {
		Title string `yaml:"title"`
	} `yaml:"slides"`
}

// Describe describes the file at filePath, a path relative to the repository of the tenant, with
// the given content, as a deck of the schema. ok is false when the schema is not the schema of
// decks or the check of the slides refuses the file.
func Describe(schema, filePath string, content []byte) (d Description, ok bool) {
	if len(check.Validate(schema, path.Clean(filePath), content)) > 0 {
		return Description{}, false
	}
	if check.IsStub(content) {
		return Description{Stub: true}, true
	}
	var v deck
	if err := yaml.Unmarshal(content, &v); err != nil {
		return Description{}, false
	}
	var titles []string
	for _, s := range v.Slides {
		titles = append(titles, strings.Join(strings.Fields(s.Title), " "))
	}
	d.Title = strings.Join(strings.Fields(v.Title), " ")
	if d.Title == "" && len(titles) > 0 {
		d.Title = titles[0]
	}
	d.Summary = cut(strings.Join(titles, "; "), MaxSummary)
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

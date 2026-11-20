// Package describe is the interface if-records-describe of the building block Records
// (docs/architecture/05-building-blocks.arc42.md). It describes one file of a record type
// (decision, minutes or note) for the wiki (concept-wiki, dec-wiki): its title, summary, tags and
// outgoing links, and whether it is the stub of a staged record. The records part owns the fields
// of its types, so no other part reads them (dec-artifact-sync).
//
// The rules:
//
//   - Only a file that the check of the records accepts for the schema is described.
//   - The stub of a record type is described as a stub only: no title, no summary, no links.
//   - The text of a record is read as Markdown. A record has no field for a title: its title is
//     the plain text of the first block of its text (a heading or a paragraph), without the marks
//     of Markdown, cut to MaxTitle characters.
//   - The summary is the plain text of the other blocks, with all white space as single spaces,
//     cut to MaxSummary characters. It is empty when the text has one block only.
//   - The tags are the tags of a note. Decisions and minutes have no tags.
//   - The links are the relative Markdown links of the text, resolved from the directory of the
//     record to paths from the top of the repository, each once, in the order of the text. Links
//     with a scheme (https:, mailto:), links from the top of a site ("/..."), links to a place in
//     the same file ("#..."), links that lead out of the repository, and images are left out.
//
// A text that is cut ends in "…".
//
// The signatures use only types that Go has built in and the type Description of this package.
package describe

import (
	"net/url"
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/mrsimpson/agentic-office/hub/records/check"
)

// The longest title and summary, in characters.
const (
	MaxTitle   = 80
	MaxSummary = 200
)

// Description is what the records part tells about one record.
type Description struct {
	Title   string
	Summary string
	Tags    []string
	// Links are the paths, from the top of the repository, that the text links to.
	Links []string
	// Stub is true for the stub of a record. A stub has no other field.
	Stub bool
}

// Describe describes the file at filePath, a path relative to the repository of the tenant, with
// the given content, as a record of the schema. ok is false when the schema is not a schema of
// the records or the check of the records refuses the file.
func Describe(schema, filePath string, content []byte) (d Description, ok bool) {
	p := path.Clean(filePath)
	if len(check.Validate(schema, p, content)) > 0 {
		return Description{}, false
	}
	if check.IsStub(schema, content) {
		return Description{Stub: true}, true
	}
	fields, reason := check.Fields(schema, content)
	if reason != "" {
		return Description{}, false
	}
	txt, _ := fields[check.TextKey].(string)
	doc := goldmark.DefaultParser().Parse(text.NewReader([]byte(txt)))
	d.Title, d.Summary = titleAndSummary(doc, []byte(txt))
	if schema == check.SchemaNote {
		if tags, ok := fields["tags"].([]any); ok {
			for _, t := range tags {
				if s, ok := t.(string); ok {
					d.Tags = append(d.Tags, s)
				}
			}
		}
	}
	d.Links = links(p, doc)
	return d, true
}

// titleAndSummary returns the plain text of the first block of the text and of the other blocks.
func titleAndSummary(doc ast.Node, src []byte) (title, summary string) {
	var blocks []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindHeading, ast.KindParagraph, ast.KindTextBlock:
			if t := strings.Join(strings.Fields(plainText(n, src)), " "); t != "" {
				blocks = append(blocks, t)
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	if len(blocks) == 0 {
		return "", ""
	}
	return cut(blocks[0], MaxTitle), cut(strings.Join(blocks[1:], " "), MaxSummary)
}

// plainText returns the text of the inline nodes under n, without the marks of Markdown.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Image:
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		case *ast.AutoLink:
			b.Write(t.Label(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// cut shortens s to max characters; a text that is cut ends in "…".
func cut(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

// links returns the paths that the relative Markdown links of the text of the record at p lead
// to.
func links(p string, doc ast.Node) []string {
	var out []string
	seen := map[string]bool{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		l, isLink := n.(*ast.Link)
		if !entering || !isLink {
			return ast.WalkContinue, nil
		}
		dest := strings.TrimSpace(string(l.Destination))
		u, err := url.Parse(dest)
		if err != nil || u.Scheme != "" || u.Host != "" || strings.HasPrefix(dest, "/") || u.Path == "" {
			return ast.WalkContinue, nil
		}
		target := path.Join(path.Dir(p), u.Path)
		if target == "." || target == ".." || strings.HasPrefix(target, "../") || target == p || seen[target] {
			return ast.WalkContinue, nil
		}
		seen[target] = true
		out = append(out, target)
		return ast.WalkContinue, nil
	})
	return out
}

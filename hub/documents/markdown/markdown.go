// Package markdown reads the parts of a Markdown document that the check and the description of
// a document both need: the front matter, the links and the first heading. It is inside the
// building block Documents and no interface.
package markdown

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// SplitFrontMatter splits a Markdown file into its YAML front matter and its body. The front
// matter is the text between a first line "---" and the next line "---". ok is false if the file
// has no front matter.
func SplitFrontMatter(content []byte) (front, body []byte, ok bool) {
	rest, ok := bytes.CutPrefix(content, []byte("---\n"))
	if !ok {
		rest, ok = bytes.CutPrefix(content, []byte("---\r\n"))
	}
	if !ok {
		return nil, nil, false
	}
	lines := bytes.SplitAfter(rest, []byte("\n"))
	for i, line := range lines {
		if string(bytes.TrimRight(line, "\r\n")) == "---" {
			return bytes.Join(lines[:i], nil), bytes.Join(lines[i+1:], nil), true
		}
	}
	return nil, nil, false
}

// Destination is the target of a link or an image, as it is written.
type Destination struct {
	Target string
	Image  bool
}

// Links returns the destinations of the links and images of a Markdown text, in order.
func Links(body []byte) []Destination {
	doc := goldmark.DefaultParser().Parse(text.NewReader(body))
	var out []Destination
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch l := n.(type) {
		case *ast.Link:
			out = append(out, Destination{Target: strings.TrimSpace(string(l.Destination))})
		case *ast.Image:
			out = append(out, Destination{Target: strings.TrimSpace(string(l.Destination)), Image: true})
		}
		return ast.WalkContinue, nil
	})
	return out
}

// FirstHeading returns the plain text of the first heading of a Markdown text, of any level, or
// "" if it has none.
func FirstHeading(body []byte) string {
	doc := goldmark.DefaultParser().Parse(text.NewReader(body))
	var title string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if h, ok := n.(*ast.Heading); ok && entering {
			title = strings.Join(strings.Fields(plainText(h, body)), " ")
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return title
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

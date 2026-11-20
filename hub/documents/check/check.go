// Package check is the interface if-documents-check of the building block Documents
// (docs/architecture/05-building-blocks.arc42.md). It holds the stub of a document
// (concept-session-release), tells a stub from a document, checks the front matter of a document
// that names the schema of documents (dec-artifact-sync, dec-wiki), and finds the links of a
// document that lead nowhere (concept-wiki).
//
// A document is Markdown under the directory Dir. Most documents name no schema: they are plain
// Markdown, and the artifacts part accepts them without asking this part. A document may name the
// schema of documents (Schema) in the field "schema" of its front matter; the artifacts part then
// passes it to Validate, and the documents part checks it the same way (Check).
//
// The front matter of the schema has these fields, all optional:
//
//	schema:   office.document.v1
//	title:    one line of text
//	summary:  one line of text
//	tags:     a list of tags; each one line, not blank, no spaces around it, not repeated
//	sources:  a list of paths of the records that the document draws on, from the top of the
//	          repository, for example minutes/000012.yaml; each ends in .md, .yaml or .yml
//	status:   draft, reviewed or outdated
//	owner:    the participant ID of the owner
//	reviewed: a list of reviews, each with the fields id (the participant ID of the reviewer)
//	          and date (the day of the review, YYYY-MM-DD)
//
// Any other field is invalid. A document without front matter is valid, and its first heading is
// its title.
//
// A document that waits for the release of its session lies in the staging area of the session,
// and its stub lies at the same path in the artifact repository. Every stub of a document has the
// same content (Stub): Markdown whose front matter names the schema of documents and holds
// "stub: true", and a body that says that the document waits for a release. It names no session,
// no time and no author. A stub is recognised byte for byte (IsStub), so the documents part and
// the check of pushed documents decide the same way.
//
// Hints finds the relative Markdown links of a document that lead to no file. A hint never makes a
// document invalid.
//
// The signatures use only types that Go has built in.
package check

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"go.yaml.in/yaml/v3"
)

// Schema is the name of the schema of documents. A stub names it in its front matter.
const Schema = "office.document.v1"

// Dir is the directory of the documents in the archive.
const Dir = "documents"

// The fields of the front matter of a document.
const (
	FieldSchema   = "schema"
	FieldTitle    = "title"
	FieldSummary  = "summary"
	FieldTags     = "tags"
	FieldSources  = "sources"
	FieldStatus   = "status"
	FieldOwner    = "owner"
	FieldReviewed = "reviewed"
)

// Fields returns the names of the fields of the front matter of a document, in this order.
func Fields() []string {
	return []string{FieldSchema, FieldTitle, FieldSummary, FieldTags, FieldSources, FieldStatus, FieldOwner, FieldReviewed}
}

// Statuses returns the values of the field status.
func Statuses() []string { return []string{"draft", "reviewed", "outdated"} }

// stub is the content of every stub of a document.
const stub = "---\nschema: " + Schema + "\nstub: true\n---\nThis document waits for the release of the session that wrote it.\n"

// Stub returns the content of the stub of a document: the same for every staged document.
func Stub() []byte { return []byte(stub) }

// IsStub reports whether content is the stub of a document.
func IsStub(content []byte) bool { return string(content) == stub }

// Schemas returns the names of the schemas that this part owns.
func Schemas() []string { return []string{Schema} }

// PathProblem returns why p is not the path of a document, or "". The path of a document is
// Dir/<name>.md. The caller checks that p is a clean path inside the repository.
func PathProblem(p string) string {
	if !strings.HasPrefix(p, Dir+"/") || path.Ext(p) != ".md" {
		return "the path of a document is " + Dir + "/<name>.md"
	}
	return ""
}

// Validate checks the content of the file at filePath against the schema. It returns the reasons
// in plain words, or nothing when the file is valid. A document of the schema lies at the path of
// a document, and its front matter has only the fields of the schema; the stub of a document is
// one of them.
func Validate(schema, filePath string, content []byte) []string {
	if schema != Schema {
		return []string{fmt.Sprintf("the schema %q is not a schema of the documents", schema)}
	}
	if PathProblem(path.Clean(filePath)) != "" {
		return []string{fmt.Sprintf("a file of the schema %s lies in %s/ and ends in .md", schema, Dir)}
	}
	if IsStub(content) {
		return nil
	}
	front, _, ok := splitFrontMatter(content)
	if !ok {
		return []string{fmt.Sprintf("a document of the schema %s names it in its front matter", Schema)}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return []string{"the front matter is not valid YAML: " + firstLine(err)}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return []string{"the front matter must be a list of fields, one per line"}
	}
	return fieldProblems(doc.Content[0])
}

// Check checks a document that is written through the documents interface, with the same rules
// as a push: front matter that is valid YAML, and that names no schema or the schema of documents.
// A document that names the schema is checked by Validate. The caller checks the path.
func Check(filePath string, content []byte) []string {
	if !bytes.HasPrefix(content, []byte("---")) {
		return nil
	}
	front, _, ok := splitFrontMatter(content)
	if !ok {
		return nil // no closing line: the document only starts with a rule
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(front, &doc); err != nil {
		return []string{"the front matter is not valid YAML: " + firstLine(err)}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != FieldSchema {
			continue
		}
		v := m.Content[i+1]
		if v.Kind != yaml.ScalarNode || v.Value != Schema {
			return []string{fmt.Sprintf("a document names the schema %s or no schema in the field %s", Schema, FieldSchema)}
		}
		return Validate(Schema, filePath, content)
	}
	return nil
}

// participantID is the form of a participant ID.
var participantID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// fieldProblems checks the fields of the front matter of a document of the schema.
func fieldProblems(m *yaml.Node) []string {
	var reasons []string
	seen := map[string]bool{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, v := m.Content[i].Value, m.Content[i+1]
		if seen[key] {
			reasons = append(reasons, fmt.Sprintf("the field %s is there twice", key))
			continue
		}
		seen[key] = true
		switch key {
		case FieldSchema:
			if v.Kind != yaml.ScalarNode || v.Value != Schema {
				reasons = append(reasons, fmt.Sprintf("the field %s must be %q", FieldSchema, Schema))
			}
		case FieldTitle, FieldSummary:
			if r := lineProblem(key, v); r != "" {
				reasons = append(reasons, r)
			}
		case FieldTags:
			reasons = append(reasons, listProblems(key, "tag", v, tagProblem)...)
		case FieldSources:
			reasons = append(reasons, listProblems(key, "source", v, sourceProblem)...)
		case FieldStatus:
			if !isText(v) || !slices.Contains(Statuses(), v.Value) {
				reasons = append(reasons, fmt.Sprintf("the field %s must be one of %s", FieldStatus, strings.Join(Statuses(), ", ")))
			}
		case FieldOwner:
			if !isText(v) || !participantID.MatchString(v.Value) {
				reasons = append(reasons, fmt.Sprintf("the field %s must be the participant ID of the owner", FieldOwner))
			}
		case FieldReviewed:
			reasons = append(reasons, reviewProblems(v)...)
		case "stub":
			reasons = append(reasons, "only the stub of a document has the field stub; the hub writes stubs")
		default:
			reasons = append(reasons, fmt.Sprintf("the field %q is not a field of a document; the fields are %s", key, strings.Join(Fields(), ", ")))
		}
	}
	return reasons
}

// isText reports whether a value is a single value that is not empty.
func isText(v *yaml.Node) bool { return v.Kind == yaml.ScalarNode && v.Tag != "!!null" }

// lineProblem checks a field that holds one line of text.
func lineProblem(field string, v *yaml.Node) string {
	switch {
	case !isText(v) || strings.TrimSpace(v.Value) == "":
		return fmt.Sprintf("the field %s must be one line of text", field)
	case strings.ContainsAny(v.Value, "\r\n"):
		return fmt.Sprintf("the field %s must be one line, without a line break", field)
	}
	return ""
}

// listProblems checks a field that holds a list of values; one names each value of the list.
func listProblems(field, one string, v *yaml.Node, problem func(string) string) []string {
	if v.Kind != yaml.SequenceNode {
		return []string{fmt.Sprintf("the field %s must be a list", field)}
	}
	var reasons []string
	seen := map[string]bool{}
	for i, item := range v.Content {
		switch {
		case !isText(item):
			reasons = append(reasons, fmt.Sprintf("%s %d of the field %s must be text", one, i+1, field))
		case seen[item.Value]:
			reasons = append(reasons, fmt.Sprintf("%s %d of the field %s (%q) is repeated", one, i+1, field, item.Value))
		default:
			if r := problem(item.Value); r != "" {
				reasons = append(reasons, fmt.Sprintf("%s %d of the field %s %s", one, i+1, field, r))
			}
		}
		seen[item.Value] = true
	}
	return reasons
}

// tagProblem checks one tag: one line, not blank, without spaces around it.
func tagProblem(tag string) string {
	switch {
	case strings.TrimSpace(tag) == "":
		return "is blank"
	case strings.ContainsAny(tag, "\r\n"):
		return "has a line break"
	case strings.TrimSpace(tag) != tag:
		return "has spaces around it"
	}
	return ""
}

// sourceProblem checks one source: the path of a file of the repository, from its top.
func sourceProblem(p string) string {
	if p == "" || path.IsAbs(p) || strings.Contains(p, `\`) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return fmt.Sprintf("(%q) must be the path of a record from the top of the repository, for example minutes/000012.yaml", p)
	}
	for _, el := range strings.Split(p, "/") {
		if strings.HasPrefix(el, ".") {
			return fmt.Sprintf("(%q) has a hidden element %q", p, el)
		}
	}
	switch path.Ext(p) {
	case ".md", ".yaml", ".yml":
		return ""
	}
	return fmt.Sprintf("(%q) must end in .md, .yaml or .yml", p)
}

// reviewProblems checks the field reviewed: a list of reviews with the fields id and date.
func reviewProblems(v *yaml.Node) []string {
	if v.Kind != yaml.SequenceNode {
		return []string{fmt.Sprintf("the field %s must be a list of reviews, each with the fields id and date", FieldReviewed)}
	}
	var reasons []string
	for i, r := range v.Content {
		n := i + 1
		if r.Kind != yaml.MappingNode {
			reasons = append(reasons, fmt.Sprintf("review %d must have the fields id and date", n))
			continue
		}
		has := map[string]bool{}
		for j := 0; j+1 < len(r.Content); j += 2 {
			key, val := r.Content[j].Value, r.Content[j+1]
			if has[key] {
				reasons = append(reasons, fmt.Sprintf("review %d has the field %s twice", n, key))
				continue
			}
			has[key] = true
			switch key {
			case "id":
				if !isText(val) || !participantID.MatchString(val.Value) {
					reasons = append(reasons, fmt.Sprintf("the id of review %d must be the participant ID of the reviewer", n))
				}
			case "date":
				if _, err := time.Parse(time.DateOnly, val.Value); !isText(val) || err != nil {
					reasons = append(reasons, fmt.Sprintf("the date of review %d must be a day in the form YYYY-MM-DD", n))
				}
			default:
				reasons = append(reasons, fmt.Sprintf("review %d has the field %q; a review has only the fields id and date", n, key))
			}
		}
		for _, want := range []string{"id", "date"} {
			if !has[want] {
				reasons = append(reasons, fmt.Sprintf("review %d has no %s", n, want))
			}
		}
	}
	return reasons
}

// Hints returns a hint in plain words for every relative Markdown link of the document at filePath
// that leads to no file. exists reports whether a clean path inside the repository is a file or a
// directory with files. Links with a scheme (https:, mailto:), links from the top of a site
// ("/..."), and links to a place in the same document ("#...") are not checked. A file that is
// not at the path of a document, and the stub of a document, get no hints.
func Hints(filePath string, content []byte, exists func(p string) bool) []string {
	p := path.Clean(filePath)
	if PathProblem(p) != "" || IsStub(content) {
		return nil
	}
	body := content
	if _, b, ok := splitFrontMatter(content); ok {
		body = b
	}
	var hints []string
	seen := map[string]bool{}
	for _, dest := range links(body) {
		if seen[dest] {
			continue
		}
		seen[dest] = true
		u, err := url.Parse(dest)
		if err != nil {
			hints = append(hints, fmt.Sprintf("the link %q is not a valid address", dest))
			continue
		}
		if u.Scheme != "" || u.Host != "" || strings.HasPrefix(dest, "/") || u.Path == "" {
			continue
		}
		target := path.Join(path.Dir(p), u.Path)
		switch {
		case target == "..", strings.HasPrefix(target, "../"):
			hints = append(hints, fmt.Sprintf("the link %q leads out of the repository", dest))
		case target == ".":
		case !exists(target):
			hints = append(hints, fmt.Sprintf("the link %q leads nowhere: there is no %s", dest, target))
		}
	}
	return hints
}

// links returns the destinations of the links and images of a Markdown text, in order.
func links(body []byte) []string {
	doc := goldmark.DefaultParser().Parse(text.NewReader(body))
	var out []string
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch l := n.(type) {
		case *ast.Link:
			out = append(out, strings.TrimSpace(string(l.Destination)))
		case *ast.Image:
			out = append(out, strings.TrimSpace(string(l.Destination)))
		}
		return ast.WalkContinue, nil
	})
	return out
}

// splitFrontMatter splits a Markdown file into its YAML front matter and its body. The front
// matter is the text between a first line "---" and the next line "---". ok is false if the file
// has no front matter.
func splitFrontMatter(content []byte) (front, body []byte, ok bool) {
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

func firstLine(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	return s
}

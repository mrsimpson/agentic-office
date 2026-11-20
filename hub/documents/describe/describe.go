// Package describe is the interface if-documents-describe of the building block Documents
// (docs/architecture/05-building-blocks.arc42.md). It describes one document for the wiki
// (concept-wiki, dec-wiki): its title, summary, tags, outgoing links and sources, and whether it
// is the stub of a document. The documents part owns the fields of its type, so no other part
// reads them (dec-artifact-sync).
//
// The rules:
//
//   - A document is a Markdown file under the directory of the documents (check.Dir). It names no
//     schema, or the schema of documents (check.Schema). Any other file is not described.
//   - A document that the check of documents refuses is not described.
//   - The stub of a document is described as a stub only: no title, no summary, no links.
//   - The title is the field title of the front matter. Without it, the title is the first
//     heading of the body. Without a heading, the title is the name of the file without ".md".
//   - The summary, the tags and the sources come from the front matter. Only a document that
//     names the schema of documents has them; the front matter of a document without the schema
//     is not read.
//   - The links are the relative Markdown links of the body, resolved to paths from the top of
//     the repository, each once, in the order of the text. Links with a scheme (https:, mailto:),
//     links from the top of a site ("/..."), links to a place in the same document ("#..."),
//     links that lead out of the repository, and images are not links of the description.
//   - The sources are the paths of the field sources, as written: the records that the document
//     cites (from the top of the repository, for example minutes/000012.yaml).
//
// The signatures use only types that Go has built in and the type Description of this package.
package describe

import (
	"net/url"
	"path"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mrsimpson/agentic-office/hub/documents/check"
	"github.com/mrsimpson/agentic-office/hub/documents/markdown"
)

// Description is what the documents part tells about one document.
type Description struct {
	Title   string
	Summary string
	Tags    []string
	// Links are the paths, from the top of the repository, that the body links to.
	Links []string
	// Sources are the paths, from the top of the repository, of the records that the document
	// cites in its front matter.
	Sources []string
	// Stub is true for the stub of a document. A stub has no other field.
	Stub bool
}

// frontMatter holds the fields of the schema of documents that a description uses.
type frontMatter struct {
	Schema  string   `yaml:"schema"`
	Title   string   `yaml:"title"`
	Summary string   `yaml:"summary"`
	Tags    []string `yaml:"tags"`
	Sources []string `yaml:"sources"`
}

// Describe describes the document at filePath, a path relative to the repository of the tenant,
// with the given content. schema is the schema that the file names, or "" when it names none. ok
// is false when the file is not a document that the check of documents accepts.
func Describe(schema, filePath string, content []byte) (d Description, ok bool) {
	p := path.Clean(filePath)
	if check.PathProblem(p) != "" || (schema != "" && schema != check.Schema) {
		return Description{}, false
	}
	if check.IsStub(content) {
		return Description{Stub: true}, true
	}
	if len(check.Check(p, content)) > 0 {
		return Description{}, false
	}
	body := content
	if front, b, has := markdown.SplitFrontMatter(content); has {
		body = b
		var fm frontMatter
		if err := yaml.Unmarshal(front, &fm); err == nil && fm.Schema == check.Schema {
			d.Title, d.Summary, d.Tags, d.Sources = fm.Title, fm.Summary, fm.Tags, fm.Sources
		}
	}
	if d.Title == "" {
		d.Title = markdown.FirstHeading(body)
	}
	if d.Title == "" {
		d.Title = strings.TrimSuffix(path.Base(p), ".md")
	}
	d.Links = links(p, body)
	return d, true
}

// links returns the paths that the relative links of the body of the document at p lead to.
func links(p string, body []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, dest := range markdown.Links(body) {
		if dest.Image {
			continue
		}
		u, err := url.Parse(dest.Target)
		if err != nil || u.Scheme != "" || u.Host != "" || strings.HasPrefix(dest.Target, "/") || u.Path == "" {
			continue
		}
		target := path.Join(path.Dir(p), u.Path)
		if target == "." || target == ".." || strings.HasPrefix(target, "../") || target == p || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}

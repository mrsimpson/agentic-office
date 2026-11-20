package portal

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"google.golang.org/protobuf/types/known/timestamppb"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	"github.com/mrsimpson/agentic-office/hub/archive/api/archivev1connect"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/portal/web"
	wikiv1 "github.com/mrsimpson/agentic-office/hub/wiki/api"
	"github.com/mrsimpson/agentic-office/hub/wiki/api/wikiv1connect"
)

// This file is the wiki of a tenant in the portal (concept-wiki): the index with its tags, one
// artifact with its trust data, links, backlinks, sources and the documents that cite it, the search, the
// broken links and the ingest queue. The view of the wiki (if-wiki-api) decides what is in it and
// how links resolve; the content of an artifact comes from the archive read API.

// searchLimit is the most hits that the search page shows.
const searchLimit = 50

// wikiView is the wiki part of a page.
type wikiView struct {
	Page                                         string // index, artifact, search, broken or queue
	IndexHref, SearchHref, BrokenHref, QueueHref string
	Query                                        string
	Tag                                          string // the filter of the index
	Tags                                         []tagView
	Artifacts                                    []entryView // the index, the hits or the queue
	Broken                                       []brokenView
	Artifact                                     *artifactPage
}

type tagView struct {
	Tag, Href string
	Count     uint32
	On        bool
}

// entryView is an artifact in a list.
type entryView struct {
	Path, Href, Title, Summary, Schema string
	Tags                               []tagView
	Source                             bool
}

// linkView is a link or a cited source. Href is set when the target is an artifact of the wiki.
type linkView struct {
	Path, Href, Title string
	Broken            bool
}

type brokenView struct {
	FromPath, FromHref, ToPath string
	Source                     bool
}

// artifactPage is one artifact with its content and its references.
type artifactPage struct {
	Entry              entryView
	Kind               string
	FrontMatter        string        // the front matter of a Markdown file, as written
	Body               template.HTML // the rendered text of a Markdown file
	Text               string        // the text of any other file
	Staged, Cut        bool
	Missing            bool // the archive has no readable file at the path
	Links, Sources     []linkView
	Backlinks, CitedBy []entryView
	Trust              trustView
}

// trustView is the trust data of an artifact as the wiki gives it (dec-wiki). The times are
// formatted; an empty field is not shown.
type trustView struct {
	Status, Owner string
	Reviews       []reviewView
	// LastAuthor is who wrote the artifact last; nil when the hub wrote it.
	LastAuthor *authorView
	LastChange string
	// NewestSource is when the newest cited source was written.
	NewestSource string
	// ReleasedAt is set when the last change was a release; ReleasedBy is nil when the hub
	// released the session.
	ReleasedAt string
	ReleasedBy *authorView
}

// formatTime formats a time of the trust data, or gives "" when it is not set.
func formatTime(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(timeLayout)
}

type reviewView struct{ ParticipantID, Date string }

// authorView is an author. Kind is "human", "agent" or "".
type authorView struct{ Name, ParticipantID, Kind string }

func authorOf(a *wikiv1.Author) *authorView {
	if a == nil {
		return nil
	}
	v := &authorView{Name: a.GetName(), ParticipantID: a.GetParticipantId()}
	switch a.GetKind() {
	case wikiv1.AuthorKind_AUTHOR_KIND_HUMAN:
		v.Kind = "human"
	case wikiv1.AuthorKind_AUTHOR_KIND_AGENT:
		v.Kind = "agent"
	}
	return v
}

func trustOf(t *wikiv1.Trust) trustView {
	v := trustView{Status: t.GetStatus(), Owner: t.GetOwner(), LastAuthor: authorOf(t.GetLastAuthor()),
		ReleasedBy: authorOf(t.GetReleasedBy())}
	for _, r := range t.GetReviews() {
		v.Reviews = append(v.Reviews, reviewView{ParticipantID: r.GetParticipantId(), Date: r.GetDate()})
	}
	v.LastChange = formatTime(t.GetLastChange())
	v.NewestSource = formatTime(t.GetNewestSourceTime())
	v.ReleasedAt = formatTime(t.GetReleasedAt())
	return v
}

func (h *Handler) wikiClient(r *http.Request, tenant string) wikiv1connect.WikiServiceClient {
	ck, _ := r.Cookie(web.CookieName)
	return wikiv1connect.NewWikiServiceClient(connect.NewClient(h.opts.Hub, auth.StaticToken(ck.Value), auth.Tenant(tenant)))
}

// wiki serves the pages of the wiki of a tenant. page is the rest of the escaped path after
// /portal/t/<tenant>/wiki.
func (h *Handler) wiki(w http.ResponseWriter, r *http.Request, c *backend, tenant, page string) {
	v := &wikiView{IndexHref: web.WikiPath(tenant), SearchHref: web.WikiSearchPath(tenant), BrokenHref: web.WikiBrokenPath(tenant),
		QueueHref: web.WikiQueuePath(tenant)}
	wiki := h.wikiClient(r, tenant)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	title := "Wiki"
	switch {
	case page == "":
		v.Page, v.Tag = "index", r.URL.Query().Get("tag")
		res, err := wiki.ListArtifacts(ctx, &wikiv1.ListArtifactsRequest{Tag: v.Tag})
		if err != nil {
			h.denied(w, err)
			return
		}
		for _, t := range res.GetTags() {
			v.Tags = append(v.Tags, tagView{Tag: t.GetTag(), Count: t.GetCount(), Href: web.WikiTagPath(tenant, t.GetTag()), On: t.GetTag() == v.Tag})
		}
		v.Artifacts = entries(tenant, res.GetArtifacts())
	case page == "/search":
		v.Page, v.Query, title = "search", strings.TrimSpace(r.URL.Query().Get("q")), "Search the wiki"
		if v.Query != "" {
			res, err := wiki.Search(ctx, &wikiv1.SearchRequest{Query: v.Query, Limit: searchLimit})
			if connect.CodeOf(err) == connect.CodeInvalidArgument {
				h.wikiPage(w, http.StatusBadRequest, c, tenant, title, v, "The search needs at least one word of letters or digits.")
				return
			}
			if err != nil {
				h.denied(w, err)
				return
			}
			for _, hit := range res.GetHits() {
				v.Artifacts = append(v.Artifacts, entryOf(tenant, hit.GetArtifact()))
			}
		}
	case page == "/broken":
		v.Page, title = "broken", "Broken links"
		res, err := wiki.ListBrokenLinks(ctx, &wikiv1.ListBrokenLinksRequest{})
		if err != nil {
			h.denied(w, err)
			return
		}
		for _, l := range res.GetLinks() {
			v.Broken = append(v.Broken, brokenView{FromPath: l.GetFromPath(), FromHref: web.WikiArtifactPath(tenant, l.GetFromPath()),
				ToPath: l.GetToPath(), Source: l.GetSource()})
		}
	case page == "/queue":
		v.Page, title = "queue", "Ingest queue"
		res, err := wiki.ListIngestQueue(ctx, &wikiv1.ListIngestQueueRequest{})
		if err != nil {
			h.denied(w, err)
			return
		}
		v.Artifacts = entries(tenant, res.GetSources())
	case strings.HasPrefix(page, "/a/"):
		p, err := url.PathUnescape(page[len("/a/"):])
		if err != nil || p == "" {
			h.fail(w, http.StatusNotFound, "Not found", "There is no such artifact in the wiki.")
			return
		}
		res, err := wiki.GetArtifact(ctx, &wikiv1.GetArtifactRequest{Path: p})
		if code := connect.CodeOf(err); code == connect.CodeNotFound || code == connect.CodeInvalidArgument {
			h.fail(w, http.StatusNotFound, "Not found", "There is no such artifact in the wiki. It may wait for the release of its session.")
			return
		}
		if err != nil {
			h.denied(w, err)
			return
		}
		a, err := h.artifactPage(ctx, r, tenant, res)
		if err != nil {
			h.denied(w, err)
			return
		}
		v.Page, v.Artifact, title = "artifact", a, a.Entry.Title
	default:
		h.fail(w, http.StatusNotFound, "Not found", "There is no such page.")
		return
	}
	h.wikiPage(w, http.StatusOK, c, tenant, title, v, "")
}

func (h *Handler) wikiPage(w http.ResponseWriter, status int, c *backend, tenant, title string, v *wikiView, errMsg string) {
	render(w, status, c.me, web.WikiPath(tenant), tenant, map[string]any{"Title": title, "Section": tenant + " · Wiki",
		"Mode": "wiki", "Wiki": v, "Error": errMsg})
}

func entries(tenant string, as []*wikiv1.Artifact) []entryView {
	var out []entryView
	for _, a := range as {
		out = append(out, entryOf(tenant, a))
	}
	return out
}

func entryOf(tenant string, a *wikiv1.Artifact) entryView {
	e := entryView{Path: a.GetPath(), Href: web.WikiArtifactPath(tenant, a.GetPath()), Title: a.GetTitle(), Summary: a.GetSummary(),
		Schema: a.GetSchema(), Source: a.GetSource()}
	if e.Title == "" {
		e.Title = e.Path
	}
	for _, t := range a.GetTags() {
		e.Tags = append(e.Tags, tagView{Tag: t, Href: web.WikiTagPath(tenant, t)})
	}
	return e
}

func links(tenant string, ls []*wikiv1.Link) []linkView {
	var out []linkView
	for _, l := range ls {
		lv := linkView{Path: l.GetPath(), Broken: l.GetBroken()}
		if a := l.GetArtifact(); a != nil {
			lv.Href, lv.Title = web.WikiArtifactPath(tenant, a.GetPath()), entryOf(tenant, a).Title
		}
		out = append(out, lv)
	}
	return out
}

// kindOf names the kind of an artifact for the reader.
func kindOf(a *wikiv1.Artifact) string {
	switch {
	case a.GetSource():
		return "source: a record that is written once"
	case a.GetSchema() == "" || strings.HasPrefix(a.GetSchema(), "office.document."):
		return "page"
	}
	return "artifact"
}

// artifactPage builds the page of one artifact: what the wiki says about it, and its content from
// the archive read API.
func (h *Handler) artifactPage(ctx context.Context, r *http.Request, tenant string, res *wikiv1.GetArtifactResponse) (*artifactPage, error) {
	a := &artifactPage{Entry: entryOf(tenant, res.GetArtifact()), Kind: kindOf(res.GetArtifact()), Links: links(tenant, res.GetLinks()),
		Sources: links(tenant, res.GetSources()), Backlinks: entries(tenant, res.GetBacklinks()), CitedBy: entries(tenant, res.GetCitedBy()),
		Trust: trustOf(res.GetTrust())}
	p := a.Entry.Path
	ck, _ := r.Cookie(web.CookieName)
	read := archivev1connect.NewArchiveReadServiceClient(connect.NewClient(h.opts.Hub, auth.StaticToken(ck.Value), auth.Tenant(tenant)))
	f, err := read.ReadFile(ctx, &archivev1.ReadFileRequest{Path: p})
	if code := connect.CodeOf(err); code == connect.CodeNotFound || code == connect.CodeInvalidArgument {
		// The view follows the repository a moment later: the file may be gone already.
		a.Missing = true
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	content := f.GetFile().GetContent()
	a.Staged = f.GetFile().GetStaged()
	if len(content) > maxArtifactShown {
		content, a.Cut = content[:maxArtifactShown], true
	}
	content = []byte(strings.ToValidUTF8(string(content), string(utf8.RuneError)))
	if path.Ext(p) != ".md" {
		a.Text = string(content)
		return a, nil
	}
	body := content
	if front, rest, ok := splitFrontMatter(content); ok {
		a.FrontMatter, body = string(front), rest
	}
	targets := map[string]string{} // the target of a link -> the page of its artifact
	for _, l := range res.GetLinks() {
		if l.GetArtifact() != nil {
			targets[l.GetPath()] = web.WikiArtifactPath(tenant, l.GetArtifact().GetPath())
		}
	}
	a.Body = wikiMarkdown(p, body, targets)
	return a, nil
}

// splitFrontMatter splits a Markdown file into the text between a first line "---" and the next
// line "---", and the rest. The page shows the front matter as written and renders the rest.
func splitFrontMatter(content []byte) (front, body []byte, ok bool) {
	rest, ok := bytes.CutPrefix(content, []byte("---\n"))
	if !ok {
		rest, ok = bytes.CutPrefix(content, []byte("---\r\n"))
	}
	if !ok {
		return nil, content, false
	}
	lines := bytes.SplitAfter(rest, []byte("\n"))
	for i, line := range lines {
		if string(bytes.TrimRight(line, "\r\n")) == "---" {
			return bytes.Join(lines[:i], nil), bytes.Join(lines[i+1:], nil), true
		}
	}
	return nil, content, false
}

// wikiMarkdown renders the text of the Markdown artifact at p. A relative link whose target the
// wiki knows as an artifact leads to the page of that artifact in the portal. Any other relative
// link would lead to a page of the portal that does not exist, so the page shows only its text;
// the list of links below the text says where it leads. Raw HTML and unsafe links are left out.
func wikiMarkdown(p string, body []byte, targets map[string]string) template.HTML {
	doc := markdown.Parser().Parse(text.NewReader(body))
	var found []*ast.Link
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if l, ok := n.(*ast.Link); ok && entering {
			found = append(found, l)
		}
		return ast.WalkContinue, nil
	})
	for _, l := range found {
		dest := strings.TrimSpace(string(l.Destination))
		u, err := url.Parse(dest)
		if err != nil {
			unlink(l)
			continue
		}
		if u.Scheme != "" || u.Host != "" || strings.HasPrefix(dest, "#") {
			continue // a link to another site, or to a place on this page
		}
		if href, ok := targets[path.Join(path.Dir(p), u.Path)]; ok && !strings.HasPrefix(dest, "/") {
			l.Destination = []byte(href)
			continue
		}
		unlink(l)
	}
	var b bytes.Buffer
	if err := markdown.Renderer().Render(&b, body, doc); err != nil {
		return template.HTML(template.HTMLEscapeString(string(body)))
	}
	return template.HTML(b.String()) // goldmark leaves out raw HTML and unsafe links
}

// unlink replaces a link with its text.
func unlink(l *ast.Link) {
	parent := l.Parent()
	for c := l.FirstChild(); c != nil; {
		next := c.NextSibling()
		parent.InsertBefore(parent, l, c)
		c = next
	}
	parent.RemoveChild(parent, l)
}

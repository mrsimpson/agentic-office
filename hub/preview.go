package main

// The subcommand "office-hub preview": the pages of the stage and the portal with fixture data,
// for the people who work on the design. It starts no hub: there is no session, no token and no
// archive. The pages come from the real templates and the real view code of the stage and the
// portal; the data comes from their fixtures (hub/stage and hub/portal, preview*.go).

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrsimpson/agentic-office/hub/portal"
	portalweb "github.com/mrsimpson/agentic-office/hub/portal/web"
	"github.com/mrsimpson/agentic-office/hub/stage"
	stageweb "github.com/mrsimpson/agentic-office/hub/stage/web"
)

// previewHelp is the help of "office-hub preview".
const previewHelp = `office-hub preview: the pages of the stage and the portal with fixture data

usage:
  office-hub preview [-addr 127.0.0.1:8099] [-src DIR]   serve the pages on this computer
  office-hub preview -out DIR [-src DIR]                 write the pages as HTML files and exit

For work on the design of the stage and the portal. It starts no hub: there is no session, no
login and no archive, and nothing leaves this computer. The pages are made by the real
templates and the real view code; the data is fictional (fixtures). The fixtures cover a live
session with many tabs, long names and raised hands, an agent that answered only in its own chat,
an empty, an imported and an ended session, every release state of a session page, the wiki, the
administration and the error pages.

Serving: the preview listens on a loopback address only and refuses any other. It reads the
templates and the style sheets from the source tree (-src, the root of a checkout) at every
change, and a small script in each page reloads the page when a file changed. Start it from a
checkout with "make ui-dev". Open the address in a browser: the start page lists every page.

Writing: -out writes every page to a file, plus index.html that links them, and the style
sheets that the pages link (below stage/assets and portal/assets), and exits. It exits with an
error when a page does not render, so a broken template cannot go unnoticed. Links between the
pages point to the files, links to the style sheets to their relative path.

Without the source tree (no hub/stage/web/embed below -src), the preview uses the templates that
are built into the program, and the pages do not reload.

Flags:
`

// previewPage is a rendered fixture page.
type previewPage struct {
	Name, Title, Path string
	HTML              []byte
	Portal            bool
}

// previewAsset is a static file of a page, as the real stage or portal serves it.
type previewAsset struct {
	Path string // the address, for example /stage/assets/base-0123456789ab.css
	Type string // the media type
	Body []byte
}

// previewSource says where the templates come from.
type previewSource struct {
	stage, portal string // directories of the template files; empty: built into the program
}

// onDisk reports whether the templates are read from a source tree.
func (s previewSource) onDisk() bool { return s.stage != "" }

// openPreviewSource uses the template files below root if they exist, else the built-in ones.
func openPreviewSource(root string) previewSource {
	s := filepath.Join(root, filepath.FromSlash(stageweb.SourceDir))
	p := filepath.Join(root, filepath.FromSlash(portalweb.SourceDir))
	if isDir(s) && isDir(p) {
		return previewSource{stage: s, portal: p}
	}
	return previewSource{}
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// version changes when a template file changes: it is the newest modification time.
func (s previewSource) version() string {
	if !s.onDisk() {
		return "built-in"
	}
	var newest int64
	for _, dir := range []string{s.stage, s.portal} {
		es, _ := os.ReadDir(dir)
		for _, e := range es {
			if fi, err := e.Info(); err == nil && fi.ModTime().UnixNano() > newest {
				newest = fi.ModTime().UnixNano()
			}
		}
	}
	return strconv.FormatInt(newest, 10)
}

// render renders every fixture page. It returns the pages that rendered, the static files that
// the pages link and one error that lists every page that did not render.
func (s previewSource) render() ([]previewPage, []previewAsset, error) {
	var errs []error
	st, pt := stageweb.Embedded(), portalweb.Embedded()
	if s.onDisk() {
		var err error
		if st, err = stageweb.Load(os.DirFS(s.stage)); err != nil {
			return nil, nil, fmt.Errorf("stage templates in %s: %w", s.stage, err)
		}
		if pt, err = portalweb.Load(os.DirFS(s.portal)); err != nil {
			return nil, nil, fmt.Errorf("portal templates in %s: %w", s.portal, err)
		}
	}
	var pages []previewPage
	sp, err := stage.Preview(st)
	errs = append(errs, err)
	for _, p := range sp {
		pages = append(pages, previewPage{Name: p.Name, Title: p.Title, Path: p.Path, HTML: p.HTML})
	}
	pp, err := portal.Preview(pt)
	errs = append(errs, err)
	for _, p := range pp {
		pages = append(pages, previewPage{Name: p.Name, Title: p.Title, Path: p.Path, HTML: p.HTML, Portal: true})
	}
	if len(pages) == 0 {
		errs = append(errs, errors.New("no page rendered"))
	}
	var assets []previewAsset
	for _, a := range st.Assets {
		assets = append(assets, previewAsset{Path: a.Path(), Type: a.Type, Body: a.Body})
	}
	for _, a := range pt.Assets {
		assets = append(assets, previewAsset{Path: a.Path(), Type: a.Type, Body: a.Body})
	}
	return pages, assets, errors.Join(errs...)
}

// indexTemplate is the start page of the preview. It is not a page of the product.
var indexTemplate = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Preview of the stage and the portal</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:60rem;margin:2rem auto;padding:0 1rem}
code{font-family:ui-monospace,monospace}small{color:#666}pre{background:#fee;padding:.8rem;white-space:pre-wrap}</style></head>
<body><h1>Preview of the stage and the portal</h1>
<p>Fixture data, rendered by the real templates. {{if .Live}}The pages reload when a template or a style sheet changes.{{else}}The pages are static.{{end}}</p>
{{if .Errors}}<h2>Pages that did not render</h2><pre data-preview-errors>{{.Errors}}</pre>{{end}}
<h2>Stage</h2><ul>{{range .Stage}}<li><a href="{{.Href}}" data-preview-page="{{.Name}}">{{.Title}}</a> <small><code>{{.Name}}</code></small></li>{{end}}</ul>
<h2>Portal</h2><ul>{{range .Portal}}<li><a href="{{.Href}}" data-preview-page="{{.Name}}">{{.Title}}</a> <small><code>{{.Name}}</code></small></li>{{end}}</ul>
</body></html>
`))

type indexEntry struct{ Name, Title, Href string }

// indexPage renders the start page. href gives the link of a page.
func indexPage(pages []previewPage, err error, live bool, href func(previewPage) string) []byte {
	data := struct {
		Live          bool
		Errors        string
		Stage, Portal []indexEntry
	}{Live: live}
	if err != nil {
		data.Errors = err.Error()
	}
	for _, p := range pages {
		e := indexEntry{Name: p.Name, Title: p.Title, Href: href(p)}
		if p.Portal {
			data.Portal = append(data.Portal, e)
		} else {
			data.Stage = append(data.Stage, e)
		}
	}
	var b bytes.Buffer
	if err := indexTemplate.Execute(&b, data); err != nil {
		panic(err) // the template is part of the program
	}
	return b.Bytes()
}

// reloadScript polls the version of the templates and reloads the page when it changes. Only the
// preview adds it; the real pages do not have it.
const reloadScript = `<script>(function(){var v=null;function poll(){fetch("/__version",{cache:"no-store"}).then(function(r){return r.text()}).then(function(t){if(v===null){v=t}else if(t!==v){location.reload();return}setTimeout(poll,1000)}).catch(function(){setTimeout(poll,2000)})}poll()})();</script>`

// withReload adds the reload script to a page.
func withReload(html []byte) []byte {
	i := bytes.LastIndex(html, []byte("</body>"))
	if i < 0 {
		return append(html, reloadScript...)
	}
	return append(append(append([]byte{}, html[:i]...), reloadScript...), html[i:]...)
}

// routeKey normalises an address for the lookup of a fixture page: no trailing slash (except the
// portal start page), no fragment, the query kept as it is.
func routePath(p string) string {
	if p != "/" && p != portalweb.HomePath {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

// previewRoutes maps the real address of a page to the page. A page with a query string has its
// own key; the address without query is the fallback of every query.
func previewRoutes(pages []previewPage) map[string]previewPage {
	routes := map[string]previewPage{}
	for _, p := range pages {
		if p.Path == "" {
			continue
		}
		u, err := url.Parse(p.Path)
		if err != nil {
			continue
		}
		key := routePath(u.Path)
		if u.RawQuery != "" {
			key += "?" + u.RawQuery
		}
		if _, taken := routes[key]; !taken {
			routes[key] = p
		}
	}
	return routes
}

// previewServer serves the pages and renders them again when a template file changed.
type previewServer struct {
	src previewSource

	mu      sync.Mutex
	version string
	pages   []previewPage
	assets  map[string]previewAsset // by address
	byName  map[string]previewPage
	routes  map[string]previewPage
	err     error
}

// current returns the rendered pages, rendered again if a template file changed.
func (s *previewServer) current() *previewServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v := s.src.version(); s.byName == nil || v != s.version {
		s.version = v
		var assets []previewAsset
		s.pages, assets, s.err = s.src.render()
		s.assets = map[string]previewAsset{}
		for _, a := range assets {
			s.assets[a.Path] = a
		}
		s.byName = map[string]previewPage{}
		for _, p := range s.pages {
			s.byName[p.Name] = p
		}
		s.routes = previewRoutes(s.pages)
	}
	return &previewServer{src: s.src, version: s.version, pages: s.pages, assets: s.assets, byName: s.byName, routes: s.routes, err: s.err}
}

func (s *previewServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "The preview only answers GET.", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/__version" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, s.src.version())
		return
	}
	cur := s.current()
	live := s.src.onDisk()
	send := func(html []byte) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if live {
			html = withReload(html)
		}
		_, _ = w.Write(html)
	}
	// The static files of the pages, at the addresses of the real stage and portal.
	if a, ok := cur.assets[r.URL.Path]; ok {
		w.Header().Set("Content-Type", a.Type)
		_, _ = w.Write(a.Body)
		return
	}
	path := routePath(r.URL.Path)
	if path == "/" || path == "/index.html" {
		send(indexPage(cur.pages, cur.err, live, func(p previewPage) string { return p.Name }))
		return
	}
	if p, ok := cur.byName[strings.TrimPrefix(path, "/")]; ok {
		send(p.HTML)
		return
	}
	if r.URL.RawQuery != "" {
		if p, ok := cur.routes[path+"?"+r.URL.RawQuery]; ok {
			send(p.HTML)
			return
		}
	}
	if p, ok := cur.routes[path]; ok {
		send(p.HTML)
		return
	}
	msg := "This address is no fixture page. The start page lists them."
	if cur.err != nil {
		msg += "\n\nPages that did not render:\n" + cur.err.Error()
	}
	http.Error(w, msg, http.StatusNotFound)
}

// previewAssetFile returns the place of a static file in the folder that writePreview fills: the
// address of the real page below the folder, so /stage/assets/x.css becomes stage/assets/x.css.
// The pages link it by this relative path.
func previewAssetFile(address string) string { return strings.TrimPrefix(address, "/") }

// writePreview writes every page, the static files that they link and index.html to dir. Links
// between pages point to the files, links to static files to their relative path.
func writePreview(dir string, pages []previewPage, assets []previewAsset) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Replace the address of a page in a link by its file.
	var pairs []string
	for _, a := range assets {
		file := previewAssetFile(a.Path)
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(filepath.FromSlash(file))), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(file)), a.Body, 0o644); err != nil {
			return err
		}
		pairs = append(pairs, `href="`+a.Path+`"`, `href="`+file+`"`)
	}
	for p, page := range previewRoutes(pages) {
		pairs = append(pairs, `href="`+template.HTMLEscapeString(p)+`"`, `href="`+page.Name+`"`)
		if !strings.Contains(p, "?") && p != portalweb.HomePath {
			// The tenant pages link with a trailing slash.
			pairs = append(pairs, `href="`+template.HTMLEscapeString(p)+`/"`, `href="`+page.Name+`"`)
		}
	}
	links := strings.NewReplacer(pairs...)
	for _, p := range pages {
		if err := os.WriteFile(filepath.Join(dir, p.Name), []byte(links.Replace(string(p.HTML))), 0o644); err != nil {
			return err
		}
	}
	index := indexPage(pages, nil, false, func(p previewPage) string { return p.Name })
	return os.WriteFile(filepath.Join(dir, "index.html"), index, 0o644)
}

// loopbackOnly returns an error unless addr is a loopback address.
func loopbackOnly(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("the preview listens on a loopback address only (127.0.0.1, ::1 or localhost), not on %q: it has no login", addr)
	}
	return nil
}

// preview runs "office-hub preview".
func preview(args []string) {
	fset := flag.NewFlagSet("preview", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	addr := fset.String("addr", "127.0.0.1:8099", "listen address; a loopback address only (port 0 picks a free port)")
	src := fset.String("src", ".", "root of a checkout of the repository; the templates and style sheets are read from there")
	out := fset.String("out", "", "write every page as an HTML file into this directory, with index.html, and exit")
	usage := func(w io.Writer) {
		fmt.Fprint(w, previewHelp)
		fset.SetOutput(w)
		fset.PrintDefaults()
	}
	if len(args) == 1 && isHelpFlag(args[0]) {
		usage(os.Stdout)
		return
	}
	if err := fset.Parse(args); err != nil || fset.NArg() != 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	source := openPreviewSource(*src)
	if !source.onDisk() {
		log.Printf("no templates below %s (%s): using the templates that are built into this program; the pages do not reload",
			*src, stageweb.SourceDir)
	}

	if *out != "" {
		pages, assets, renderErr := source.render()
		if err := writePreview(*out, pages, assets); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %d pages and index.html to %s\n", len(pages), *out)
		if renderErr != nil {
			log.Fatalf("pages that did not render:\n%v", renderErr)
		}
		return
	}

	if err := loopbackOnly(*addr); err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	// Tests and scripts read this line to learn the address.
	log.Printf("office-hub preview listening on http://%s", ln.Addr())
	srv := &http.Server{Handler: &previewServer{src: source}, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

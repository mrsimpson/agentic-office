// Package web is the stage view (if-stage-web): the web page of one session that a browser opens
// with a stage link, and its event stream.
//
// Addresses:
//
//	GET /stage/<escaped session>?token=<token>         the page
//	GET /stage/<escaped session>/events?token=<token>  server-sent events for the page
//	GET /stage/<escaped session>/artifact?token=<token>&path=<path>
//	                                                   the artifact region for one artifact of the
//	                                                   session, for the own focus of a browser
//	GET /stage/assets/<name>                           a style sheet of the page (Assets); no token
//
// The page has regions (Regions). Every event of the stream is named after a region and carries
// the inner HTML of that region; the one inline script (Script) only replaces the region. An
// event "end" ends the stream. The page links two style sheets of its own origin (Assets): the
// base of the design system and the page. Its Content-Security-Policy (PageCSP) allows style sheets
// of the same origin and inline styles (artifacts in a frame bring their own), the inline script by
// its hash and the event stream of the same origin. An error page (ErrorPage, ErrorCSP) shows no
// session data.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"strings"
)

// PathPrefix is where the stage is mounted. Identity creates links <public-url>/stage/<session>.
const PathPrefix = "/stage/"

// AssetPrefix is where the static files of the page are served. They need no token: they hold no
// session data.
const AssetPrefix = PathPrefix + "assets/"

// Asset is a static file that the page links: a style sheet, or the notice of the third-party
// licences that the base style sheet needs.
type Asset struct {
	// Name is the last part of the address. A style sheet has a hash of its content in its name, so
	// a changed file has a new address and a browser never uses a stale copy.
	Name string
	// Type is the media type of the answer.
	Type string
	// Body is the content.
	Body []byte
	// Immutable says that the name carries a hash of the content, so the file may be cached for ever.
	Immutable bool
}

// Path returns the address of the asset.
func (a Asset) Path() string { return AssetPrefix + a.Name }

// Regions are the regions of the page. Each region is an element with this ID. Every event of the
// event stream is named after one and carries its inner HTML; the stream sends a region again only
// when its HTML changed.
var Regions = []string{"status", "origin", "participants", "artifacts", "artifact", "artifactlog", "utterances"}

//go:embed embed/*
var files embed.FS

// SourceDir is the directory of the embedded files in a checkout of the repository. The preview
// reads the files from there, so a change shows without a new build.
const SourceDir = "hub/stage/web/embed"

// Templates are the templates and the script of the page, parsed from a set of files.
type Templates struct {
	// Page is the page of a session with every region. Fields: Session, Events (the address of
	// the event stream), Artifact, Portal, Regions (the inner HTML of every region, by name) and
	// Script.
	Page *template.Template
	// ErrorPage is the page for a missing, unknown, expired or wrong token. Fields: Title,
	// Message, Status.
	ErrorPage *template.Template
	// Regions renders the inner HTML of each region (one template per name in Regions).
	Regions *template.Template
	// Script is the one inline script of the page.
	Script string
	// Assets are the static files of the page, served at AssetPrefix.
	Assets []Asset
}

// Asset returns the asset with this name (the last part of its address).
func (t *Templates) Asset(name string) (Asset, bool) {
	for _, a := range t.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// styleSheets are the style sheets of the page, in the order in which the page links them: the
// base of the design system first, then the page, which may override the base.
var styleSheets = []string{"base.css", "stage.css"}

// Load parses the files page.html, error.html, regions.html and script.js in fsys and reads the
// style sheets (base.css, stage.css) and third-party-notices.txt as assets. The files of the
// package are the files in embed/; the preview loads them from disk.
func Load(fsys fs.FS) (*Templates, error) {
	read := func(name string) (string, error) {
		b, err := fs.ReadFile(fsys, name)
		return string(b), err
	}
	t := &Templates{}
	paths := map[string]string{} // source file name -> address
	for _, file := range styleSheets {
		body, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		a := Asset{Name: strings.TrimSuffix(file, ".css") + "-" + hex.EncodeToString(sum[:6]) + ".css",
			Type: "text/css; charset=utf-8", Body: body, Immutable: true}
		t.Assets = append(t.Assets, a)
		paths[file] = a.Path()
	}
	notice, err := fs.ReadFile(fsys, "third-party-notices.txt")
	if err != nil {
		return nil, err
	}
	t.Assets = append(t.Assets, Asset{Name: "third-party-notices.txt", Type: "text/plain; charset=utf-8", Body: notice})
	funcs := template.FuncMap{"asset": func(file string) (string, error) {
		if p, ok := paths[file]; ok {
			return p, nil
		}
		return "", fmt.Errorf("no style sheet %q", file)
	}}
	parse := func(name string, funcs template.FuncMap) (*template.Template, error) {
		text, err := read(name)
		if err != nil {
			return nil, err
		}
		return template.New(strings.TrimSuffix(name, ".html")).Funcs(funcs).Parse(text)
	}
	if t.Page, err = parse("page.html", funcs); err != nil {
		return nil, err
	}
	if t.ErrorPage, err = parse("error.html", funcs); err != nil {
		return nil, err
	}
	if t.Regions, err = parse("regions.html", nil); err != nil {
		return nil, err
	}
	if t.Script, err = read("script.js"); err != nil {
		return nil, err
	}
	return t, nil
}

// Embedded returns the templates and the script that are compiled into the program.
func Embedded() *Templates { return embedded }

// embedded are the templates compiled into the program. The files are part of the package, so a
// failure to load them is a programming error.
var embedded = func() *Templates {
	sub, err := fs.Sub(files, "embed")
	if err != nil {
		panic(err)
	}
	t, err := Load(sub)
	if err != nil {
		panic(err)
	}
	return t
}()

// ErrorPage is the page for a missing, unknown, expired or wrong token. It shows no session
// data. Fields: Title, Message, Status.
var ErrorPage = embedded.ErrorPage

// Script opens the event stream and replaces each region with the HTML the server rendered. The
// transcript is a scroll box: it follows new utterances while the reader is at its end; a reader
// who scrolled up keeps the place and gets a button that jumps to the end.
//
// A click on a tab sets the own focus of this browser: the page fetches that artifact from the
// artifact address and shows it instead of the shared focus, which it keeps for later. Nothing
// changes for anyone else. While the own focus is set, the tab of the shared focus pulses after
// each change of the shared focus. A click on the tab of the shared focus follows the session again.
var Script = embedded.Script

var scriptHash = func() string {
	h := sha256.Sum256([]byte(Script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(h[:]) + "'"
}()

// Page is the page of a session with every region. Fields: Session, Events (the address of the
// event stream), Regions (the inner HTML of every region, by name) and Script.
var Page = embedded.Page

// PageCSP is the Content-Security-Policy of the page. Style sheets come from the same origin; the
// inline styles stay because the artifacts that the page shows in a frame bring their own style
// blocks (a frame with srcdoc takes the policy of the page).
var PageCSP = "default-src 'none'; style-src 'self' 'unsafe-inline'; script-src " + scriptHash +
	"; connect-src 'self'; base-uri 'none'; form-action 'none'"

// ErrorCSP is the Content-Security-Policy of the error page.
const ErrorCSP = "default-src 'none'; style-src 'self' 'unsafe-inline'; base-uri 'none'; form-action 'none'"

// Assets are the static files of the page that are compiled into the program.
var Assets = embedded.Assets

// RegionTemplates render the inner HTML of each region (one template per name in Regions) from
// the view of a session.
var RegionTemplates = embedded.Regions

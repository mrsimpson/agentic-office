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
//
// The page has regions (Regions). Every event of the stream is named after a region and carries
// the inner HTML of that region; the one inline script (Script) only replaces the region. An
// event "end" ends the stream. The page has no external assets; its Content-Security-Policy
// (PageCSP) allows only the inline styles, the inline script by its hash and the event stream of
// the same origin. An error page (ErrorPage, ErrorCSP) shows no session data.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"html/template"
)

// PathPrefix is where the stage is mounted. Identity creates links <public-url>/stage/<session>.
const PathPrefix = "/stage/"

// Regions are the regions of the page. Each region is an element with this ID. Every event of the
// event stream is named after one and carries its inner HTML; the stream sends a region again only
// when its HTML changed.
var Regions = []string{"status", "origin", "participants", "artifacts", "artifact", "artifactlog", "utterances"}

//go:embed embed/*
var files embed.FS

// read returns an embedded file. The files are part of the package, so a missing file is a
// programming error.
func read(name string) string {
	b, err := files.ReadFile("embed/" + name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

var css = read("stage.css")

// funcs gives the page templates the style sheet. The template package would escape it as text.
var funcs = template.FuncMap{"css": func() template.CSS { return template.CSS(css) }}

// ErrorPage is the page for a missing, unknown, expired or wrong token. It shows no session
// data. Fields: Title, Message, Status.
var ErrorPage = template.Must(template.New("error").Funcs(funcs).Parse(read("error.html")))

// Script opens the event stream and replaces each region with the HTML the server rendered. The
// transcript is a scroll box: it follows new utterances while the reader is at its end; a reader
// who scrolled up keeps the place and gets a button that jumps to the end.
//
// A click on a tab sets the own focus of this browser: the page fetches that artifact from the
// artifact address and shows it instead of the shared focus, which it keeps for later. Nothing
// changes for anyone else. While the own focus is set, the tab of the shared focus pulses after
// each change of the shared focus. A click on the tab of the shared focus follows the session again.
var Script = read("script.js")

var scriptHash = func() string {
	h := sha256.Sum256([]byte(Script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(h[:]) + "'"
}()

// Page is the page of a session with every region. Fields: Session, Events (the address of the
// event stream), Regions (the inner HTML of every region, by name) and Script.
var Page = template.Must(template.New("page").Funcs(funcs).Parse(read("page.html")))

// PageCSP is the Content-Security-Policy of the page.
var PageCSP = "default-src 'none'; style-src 'unsafe-inline'; script-src " + scriptHash +
	"; connect-src 'self'; base-uri 'none'; form-action 'none'"

// ErrorCSP is the Content-Security-Policy of the error page.
const ErrorCSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"

// RegionTemplates render the inner HTML of each region (one template per name in Regions) from
// the view of a session.
var RegionTemplates = template.Must(template.New("stage").Parse(read("regions.html")))

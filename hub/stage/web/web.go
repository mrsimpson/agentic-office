// Package web is the stage view (if-stage-web): the web page of one session that a browser opens
// with a stage link, and its event stream.
//
// Addresses:
//
//	GET /stage/<escaped session>?token=<token>         the page
//	GET /stage/<escaped session>/events?token=<token>  server-sent events for the page
//
// The page has regions (Regions). Every event of the stream is named after a region and carries
// the inner HTML of that region; the one inline script (Script) only replaces the region. An
// event "end" ends the stream. The page has no external assets; its Content-Security-Policy
// (PageCSP) allows only the inline styles, the inline script by its hash and the event stream of
// the same origin. An error page (ErrorPage, ErrorCSP) shows no session data.
package web

import (
	"crypto/sha256"
	"encoding/base64"
	"html/template"
)

// PathPrefix is where the stage is mounted. Identity creates links <public-url>/stage/<session>.
const PathPrefix = "/stage/"

// Regions are the regions of the page. Each region is an element with this ID. Every event of the
// event stream is named after one and carries its inner HTML; the stream sends a region again only
// when its HTML changed.
var Regions = []string{"status", "origin", "participants", "hands", "artifact", "minutes", "utterances"}

// ErrorPage is the page for a missing, unknown, expired or wrong token. It shows no session
// data. Fields: Title, Message, Status.
var ErrorPage = template.Must(template.New("error").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · Agentic office stage</title><style>` + css + `</style></head>
<body class="error"><main><h1>{{.Title}}</h1><p id="error" data-status="{{.Status}}">{{.Message}}</p></main></body></html>
`))

// Script opens the event stream and replaces each region with the HTML the server rendered.
const Script = `(function () {
  var main = document.getElementById("stage");
  var status = document.getElementById("status");
  var es = new EventSource(main.getAttribute("data-events"));
  ["status", "origin", "participants", "hands", "artifact", "minutes", "utterances"].forEach(function (name) {
    es.addEventListener(name, function (e) { document.getElementById(name).innerHTML = e.data; });
  });
  es.addEventListener("end", function () { es.close(); });
  es.onerror = function () {
    status.textContent = es.readyState === EventSource.CLOSED ? "Not connected. Reload the page." : "Reconnecting …";
  };
})();`

var scriptHash = func() string {
	h := sha256.Sum256([]byte(Script))
	return "'sha256-" + base64.StdEncoding.EncodeToString(h[:]) + "'"
}()

const css = `
:root { color-scheme: dark; }
* { box-sizing: border-box; }
html { font-size: clamp(18px, 1.55vw, 34px); }
body { margin: 0; background: #000; color: #fff; font-family: system-ui, "Segoe UI", Roboto, Arial, sans-serif; line-height: 1.3; }
header { display: flex; justify-content: space-between; align-items: baseline; gap: 1rem; padding: 0.5rem 1rem; border-bottom: 3px solid #fff; }
header h1 { margin: 0; font-size: 1.4rem; }
#status span { font-weight: 700; }
#status [data-status="live"] { color: #6f6; }
#status [data-status="imported"] { color: #6cf; }
#status [data-status="ended"], #status [data-status="stopped"] { color: #ffd400; }
main.stage { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 1fr); gap: 1rem; padding: 1rem; }
section { margin-bottom: 1rem; }
h2 { margin: 0 0 0.4rem; font-size: 1.1rem; text-transform: uppercase; letter-spacing: 0.05em; color: #ffd400; }
ul, ol { margin: 0; padding: 0; list-style: none; }
li { padding: 0.25rem 0; border-bottom: 1px solid #444; }
.empty, .note { color: #ccc; font-style: italic; margin: 0; }
.note { color: #ffd400; font-size: 1.2rem; }
.badge, .capture { display: inline-block; padding: 0 0.4em; border-radius: 0.3em; font-size: 0.8rem; font-weight: 700; vertical-align: middle; }
.badge.agent { background: #fff; color: #000; }
.capture.captured { background: #d40000; color: #fff; }
.capture.paused { background: #ffd400; color: #000; }
.capture.not-captured { border: 2px solid #fff; color: #fff; }
.capture.imported { border: 2px solid #6cf; color: #6cf; }
.capture.left { border: 2px dashed #999; color: #ccc; }
.name { font-weight: 700; }
.hands li { font-size: 1.15rem; color: #ffd400; }
#artifact iframe { width: 100%; height: 62vh; border: 0; background: #fff; }
#artifact { margin-bottom: 1rem; }
.minutes-text { white-space: pre-wrap; max-height: 40vh; overflow: auto; }
.utterances li { font-size: 1.05rem; }
#origin .imported { margin: 0; padding: 0.5rem 1rem; border-bottom: 3px solid #6cf; color: #6cf; white-space: pre-line; }
.source, .title { font-weight: 700; }
.utterances time { color: #ccc; font-size: 0.8rem; }
.corrected { color: #ccc; font-size: 0.8rem; }
body.error main { padding: 2rem; }
body.error h1 { color: #ffd400; }
body.error p { font-size: 1.3rem; }
`

// Page is the page of a session with every region. Fields: Session, Events (the address of the
// event stream), Regions (the inner HTML of every region, by name) and Script.
var Page = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Session}} · Agentic office stage</title><style>` + css + `</style></head>
<body>
<header><h1>Session <span id="session">{{.Session}}</span></h1><div id="status">{{.Regions.status}}</div></header>
<div id="origin">{{.Regions.origin}}</div>
<noscript><p class="note">This page needs JavaScript for live updates.</p></noscript>
<main id="stage" class="stage" data-events="{{.Events}}">
<div class="left">
<section id="artifact" aria-label="Shown artifact">{{.Regions.artifact}}</section>
<section id="utterances" aria-live="polite">{{.Regions.utterances}}</section>
</div>
<div class="right">
<section id="participants">{{.Regions.participants}}</section>
<section id="hands" aria-live="polite">{{.Regions.hands}}</section>
<section id="minutes">{{.Regions.minutes}}</section>
</div>
</main>
<script>{{.Script}}</script>
</body></html>
`))

// PageCSP is the Content-Security-Policy of the page.
var PageCSP = "default-src 'none'; style-src 'unsafe-inline'; script-src " + scriptHash +
	"; connect-src 'self'; base-uri 'none'; form-action 'none'"

// ErrorCSP is the Content-Security-Policy of the error page.
const ErrorCSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"

// RegionTemplates render the inner HTML of each region (one template per name in Regions) from
// the view of a session.
var RegionTemplates = template.Must(template.New("stage").Parse(`
{{define "status"}}<span data-status="{{.Status}}">{{.StatusText}}</span>{{end}}

{{define "origin"}}{{with .Imported}}<p class="imported" data-imported="{{if .Complete}}complete{{else}}importing{{end}}">Imported from <span class="source">{{.Source}}</span>{{if .Title}}: <span class="title">{{.Title}}</span>{{end}}, by {{.By}}.{{if not .Complete}} The import is not complete yet.{{end}}
The office did not see this conversation happen. The hub's signature proves who imported it, not who said what.</p>{{end}}{{end}}

{{define "name"}}<span class="name">{{.Name}}</span>{{if .Agent}} <span class="badge agent" data-kind="agent">agent</span>{{end}}{{end}}

{{define "participants"}}<h2>Participants</h2>
{{if .Participants}}<ul class="participants">
{{range .Participants}}<li data-participant="{{.ID}}" data-kind="{{if .Agent}}agent{{else}}human{{end}}" data-capture="{{.Status}}">{{template "name" .}} <span class="capture {{.Status}}">{{.StatusText}}</span></li>
{{end}}</ul>{{else}}<p class="empty">Nobody has joined yet.</p>{{end}}{{end}}

{{define "hands"}}<h2>Raised hands</h2>
{{if .Hands}}<ol class="hands">
{{range .Hands}}<li data-participant="{{.Participant.ID}}">✋ {{template "name" .Participant}}{{if .Topic}}: <span class="topic">{{.Topic}}</span>{{end}}</li>
{{end}}</ol>{{else}}<p class="empty">No raised hands.</p>{{end}}{{end}}

{{define "artifact"}}{{with .Artifact}}{{if .Content}}<iframe title="{{.ID}}" data-artifact="{{.ID}}" sandbox="" referrerpolicy="no-referrer" srcdoc="{{.Content}}"></iframe>
{{else if .ID}}<p class="note" data-artifact="{{.ID}}">{{.Note}}</p>
{{else}}<p class="empty">Nothing is shown.</p>{{end}}{{end}}{{end}}

{{define "minutes"}}<h2>Minutes</h2>
{{if .Minutes}}<div class="minutes-text">{{.Minutes}}</div>{{else}}<p class="empty">No minutes yet.</p>{{end}}{{end}}

{{define "utterances"}}<h2>Recent contributions</h2>
{{if .Utterances}}<ol class="utterances">
{{range .Utterances}}<li data-utterance="{{.EventID}}" data-participant="{{.Speaker.ID}}">{{template "name" .Speaker}}: {{if not .OriginalTime.IsZero}}<time datetime="{{.OriginalTime.Format "2006-01-02T15:04:05Z07:00"}}">{{.OriginalTime.Format "2006-01-02 15:04"}} UTC</time> {{end}}<span class="text">{{.Text}}</span>{{if .Corrected}} <span class="corrected">(corrected)</span>{{end}}</li>
{{end}}</ol>{{else}}<p class="empty">Nothing said yet.</p>{{end}}{{end}}
`))

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
var Regions = []string{"status", "origin", "participants", "hands", "artifact", "artifacts", "minutes", "utterances"}

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
  ["status", "origin", "participants", "hands", "artifact", "artifacts", "minutes", "utterances"].forEach(function (name) {
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
:root { color-scheme: dark; --bg: #0e1116; --surface: #161b22; --raised: #1d242d; --line: #2a323d; --text: #e8edf3;
  --muted: #8d99a8; --accent: #7c8cff; --accent-soft: #7c8cff22; --live: #3ddc97; --warn: #f5b84b; --rec: #ff6b6b; --imp: #5cc8ff; --r: 14px; }
* { box-sizing: border-box; }
html { font-size: clamp(16px, 1.3vw, 30px); }
body { margin: 0; background: var(--bg); color: var(--text); font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; line-height: 1.45; -webkit-font-smoothing: antialiased; }
header { display: flex; justify-content: space-between; align-items: center; gap: 1rem; padding: 0.8rem 1.5rem; }
.brand { display: flex; align-items: center; gap: 0.8rem; }
.logo { width: 1.7rem; height: 1.7rem; border-radius: 0.5rem; background: linear-gradient(135deg, var(--accent), #b07cff); }
.eyebrow, h2 { margin: 0; font-size: 0.72rem; font-weight: 600; letter-spacing: 0.09em; text-transform: uppercase; color: var(--muted); }
header h1 { margin: 0; font-size: 1.5rem; font-weight: 650; letter-spacing: -0.01em; }
#status span { display: inline-flex; align-items: center; gap: 0.45em; padding: 0.2em 0.8em; border-radius: 99px; font-size: 0.85rem; font-weight: 600; background: var(--raised); border: 1px solid var(--line); }
#status span::before { content: ""; width: 0.55em; height: 0.55em; border-radius: 50%; background: currentColor; }
#status [data-status="live"] { color: var(--live); }
#status [data-status="imported"] { color: var(--imp); }
#status [data-status="ended"], #status [data-status="stopped"] { color: var(--warn); }
#status [data-status="connecting"] { color: var(--muted); }
main.stage { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 1fr); gap: 1.2rem; padding: 0 1.5rem 1.5rem; }
.left, .right { display: flex; flex-direction: column; gap: 1.2rem; min-width: 0; }
section { background: var(--surface); border: 1px solid var(--line); border-radius: var(--r); padding: 1rem 1.2rem; }
h2 { margin-bottom: 0.7rem; }
ul, ol { margin: 0; padding: 0; list-style: none; }
li { padding: 0.35rem 0; }
.empty, .note { color: var(--muted); margin: 0; }
.note { color: var(--warn); }
.badge, .capture { display: inline-flex; align-items: center; gap: 0.4em; font-size: 0.78rem; font-weight: 600; vertical-align: middle; }
.badge.agent { padding: 0 0.5em; border-radius: 0.4em; background: #f5b84b22; color: var(--warn); }
.capture::before { content: ""; width: 0.55em; height: 0.55em; border-radius: 50%; background: currentColor; }
.capture.captured { color: var(--rec); }
.capture.paused { color: var(--warn); }
.capture.not-captured, .capture.left { color: var(--muted); }
.capture.imported { color: var(--imp); }
.participants li { display: flex; align-items: center; gap: 0.7rem; }
.participants .capture { margin-left: auto; }
.avatar { flex: none; width: 2rem; height: 2rem; border-radius: 50%; display: grid; place-items: center; font-size: 0.72rem; font-weight: 700; background: var(--accent-soft); color: var(--accent); }
.avatar::before { content: attr(data-initials); }
.avatar.agent { border-radius: 0.55rem; background: #f5b84b22; color: var(--warn); }
.name { font-weight: 650; }
.hands li { margin-bottom: 0.5rem; padding: 0.7rem 0.9rem; border-radius: 12px; background: var(--accent-soft); border: 1px solid var(--accent); }
section:empty { display: none; }
#artifact { padding: 0; overflow: hidden; }
.art-head { display: flex; justify-content: space-between; gap: 1rem; padding: 0.6rem 1.2rem; border-bottom: 1px solid var(--line); }
.art-id { color: var(--muted); font-size: 0.8rem; font-weight: 600; letter-spacing: 0.05em; }
.chips { display: flex; flex-wrap: wrap; gap: 0.5rem; }
.chip { padding: 0.25rem 0.8rem; border-radius: 99px; background: var(--raised); border: 1px solid var(--line); color: var(--muted); font-size: 0.85rem; }
.chip.focused { color: var(--accent); border-color: var(--accent); background: var(--accent-soft); }
#artifact iframe { display: block; width: 100%; height: 50vh; border: 0; background: #fff; }
#artifact .note, #artifact .empty { padding: 1rem 1.2rem; }
.minutes-text { white-space: pre-wrap; max-height: 40vh; overflow: auto; }
.utterances li { display: flex; gap: 0.7rem; align-items: flex-start; }
.utterances .line { min-width: 0; }
.utterances time, .corrected { color: var(--muted); font-size: 0.8rem; }
#origin .imported { margin: 0 1.5rem 1.2rem; padding: 0.8rem 1.2rem; border-radius: var(--r); border: 1px solid var(--imp); background: #5cc8ff14; color: var(--imp); white-space: pre-line; }
.source, .title { font-weight: 700; }
body.error main { padding: 2rem; }
body.error h1 { color: var(--warn); }
body.error p { font-size: 1.2rem; }
`

// Page is the page of a session with every region. Fields: Session, Events (the address of the
// event stream), Regions (the inner HTML of every region, by name) and Script.
var Page = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Session}} · Agentic office stage</title><style>` + css + `</style></head>
<body>
<header><div class="brand"><div class="logo"></div><div><div class="eyebrow">Agentic office</div><h1>Session <span id="session">{{.Session}}</span></h1></div></div><div id="status">{{.Regions.status}}</div></header>
<div id="origin">{{.Regions.origin}}</div>
<noscript><p class="note">This page needs JavaScript for live updates.</p></noscript>
<main id="stage" class="stage" data-events="{{.Events}}">
<div class="left">
<section id="artifact" aria-label="Shown artifact">{{.Regions.artifact}}</section>
<section id="artifacts" aria-label="Artifacts of the session">{{.Regions.artifacts}}</section>
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

{{define "avatar"}}<span class="avatar{{if .Agent}} agent{{end}}" data-initials="{{.Initials}}" aria-hidden="true"></span>{{end}}

{{define "name"}}<span class="name">{{.Name}}</span>{{if .Agent}} <span class="badge agent" data-kind="agent">agent</span>{{end}}{{end}}

{{define "participants"}}<h2>Participants</h2>
{{if .Participants}}<ul class="participants">
{{range .Participants}}<li data-participant="{{.ID}}" data-kind="{{if .Agent}}agent{{else}}human{{end}}" data-capture="{{.Status}}">{{template "avatar" .}}{{template "name" .}} <span class="capture {{.Status}}">{{.StatusText}}</span></li>
{{end}}</ul>{{else}}<p class="empty">Nobody has joined yet.</p>{{end}}{{end}}

{{define "hands"}}<h2>Raised hands</h2>
{{if .Hands}}<ol class="hands">
{{range .Hands}}<li data-participant="{{.Participant.ID}}">✋ {{template "name" .Participant}}{{if .Topic}}: <span class="topic">{{.Topic}}</span>{{end}}</li>
{{end}}</ol>{{else}}<p class="empty">No raised hands.</p>{{end}}{{end}}

{{define "artifact"}}{{with .Artifact}}{{if .ID}}<div class="art-head"><span class="eyebrow">Now showing</span><span class="art-id">{{.ID}}</span></div>
{{end}}{{if .Content}}<iframe title="{{.ID}}" data-artifact="{{.ID}}" sandbox="" referrerpolicy="no-referrer" srcdoc="{{.Content}}"></iframe>
{{else if .ID}}<p class="note" data-artifact="{{.ID}}">{{.Note}}</p>
{{else}}<p class="empty">Nothing is shown.</p>{{end}}{{end}}{{end}}

{{define "artifacts"}}{{if gt (len .Artifacts) 1}}<h2>Artifacts</h2>
<ul class="chips">
{{range .Artifacts}}<li class="chip{{if .Focused}} focused{{end}}" data-artifact="{{.Path}}">{{.Path}}</li>
{{end}}</ul>{{end}}{{end}}

{{define "minutes"}}<h2>Minutes</h2>
{{if .Minutes}}<div class="minutes-text">{{.Minutes}}</div>{{else}}<p class="empty">No minutes yet.</p>{{end}}{{end}}

{{define "utterances"}}<h2>Recent contributions</h2>
{{if .Utterances}}<ol class="utterances">
{{range .Utterances}}<li data-utterance="{{.EventID}}" data-participant="{{.Speaker.ID}}">{{template "avatar" .Speaker}}<span class="line">{{template "name" .Speaker}}: {{if not .OriginalTime.IsZero}}<time datetime="{{.OriginalTime.Format "2006-01-02T15:04:05Z07:00"}}">{{.OriginalTime.Format "2006-01-02 15:04"}} UTC</time> {{end}}<span class="text">{{.Text}}</span>{{if .Corrected}} <span class="corrected">(corrected)</span>{{end}}</span></li>
{{end}}</ol>{{else}}<p class="empty">Nothing said yet.</p>{{end}}{{end}}
`))

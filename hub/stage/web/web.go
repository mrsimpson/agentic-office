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
	"encoding/base64"
	"html/template"
)

// PathPrefix is where the stage is mounted. Identity creates links <public-url>/stage/<session>.
const PathPrefix = "/stage/"

// Regions are the regions of the page. Each region is an element with this ID. Every event of the
// event stream is named after one and carries its inner HTML; the stream sends a region again only
// when its HTML changed.
var Regions = []string{"status", "origin", "participants", "artifacts", "artifact", "artifactlog", "utterances"}

// ErrorPage is the page for a missing, unknown, expired or wrong token. It shows no session
// data. Fields: Title, Message, Status.
var ErrorPage = template.Must(template.New("error").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · Agentic office stage</title><style>` + css + `</style></head>
<body class="error"><main><h1>{{.Title}}</h1><p id="error" data-status="{{.Status}}">{{.Message}}</p></main></body></html>
`))

// Script opens the event stream and replaces each region with the HTML the server rendered. The
// transcript is a scroll box: it follows new utterances while the reader is at its end; a reader
// who scrolled up keeps the place and gets a button that jumps to the end.
//
// A click on a tab sets the own focus of this browser: the page fetches that artifact from the
// artifact address and shows it instead of the shared focus, which it keeps for later. Nothing
// changes for anyone else. While the own focus is set, the tab of the shared focus pulses after
// each change of the shared focus. A click on the tab of the shared focus follows the session again.
const Script = `(function () {
  var main = document.getElementById("stage");
  var status = document.getElementById("status");
  var log = document.getElementById("utterances");
  var tabs = document.getElementById("artifacts");
  var shown = document.getElementById("artifact");
  var own = null, shared = null, sharedHTML = shown.innerHTML, pulse = null;
  function sharedFocus() { var f = tabs.querySelector(".tab.focused"); return f ? f.getAttribute("data-artifact") : null; }
  function mark() {
    tabs.querySelectorAll(".tab").forEach(function (el) {
      var p = el.getAttribute("data-artifact");
      el.classList.toggle("own", own !== null && p === own);
      el.classList.toggle("pulse", own !== null && p === pulse);
      el.setAttribute("aria-selected", String(own === null ? el.classList.contains("focused") : p === own));
    });
  }
  function follow() { own = null; pulse = null; shown.innerHTML = sharedHTML; mark(); }
  function open(p) {
    if (p === sharedFocus()) { follow(); return; }
    own = p; pulse = null; mark();
    load(p);
  }
  function load(p) {
    fetch(main.getAttribute("data-artifact") + "&path=" + encodeURIComponent(p), { credentials: "same-origin" })
      .then(function (r) { return r.ok ? r.text() : Promise.reject(r.status); })
      .then(function (html) { if (own === p) { shown.innerHTML = html; } })
      .catch(function () { if (own === p) { shown.innerHTML = '<p class="note">This artifact cannot be shown here.</p>'; } });
  }
  function pick(e) {
    var el = e.target.closest(".tab");
    if (!el || (e.type === "keydown" && e.key !== "Enter" && e.key !== " ")) { return; }
    e.preventDefault();
    open(el.getAttribute("data-artifact"));
  }
  tabs.addEventListener("click", pick);
  tabs.addEventListener("keydown", pick);
  var jump = document.getElementById("jump");
  function atEnd() { return log.scrollHeight - log.scrollTop - log.clientHeight < 32; }
  function toEnd() { log.scrollTop = log.scrollHeight; jump.hidden = true; }
  log.addEventListener("scroll", function () { if (atEnd()) { jump.hidden = true; } });
  jump.addEventListener("click", toEnd);
  toEnd();
  var es = new EventSource(main.getAttribute("data-events"));
  ["status", "origin", "participants", "artifactlog"].forEach(function (name) {
    es.addEventListener(name, function (e) { document.getElementById(name).innerHTML = e.data; });
  });
  es.addEventListener("artifacts", function (e) {
    tabs.innerHTML = e.data;
    var now = sharedFocus();
    if (own !== null && now !== shared) { pulse = now; }
    if (own !== null && own === now) { follow(); }
    shared = now;
    mark();
  });
  es.addEventListener("artifactlog", function () { if (own !== null) { load(own); } });
  es.addEventListener("artifact", function (e) {
    sharedHTML = e.data;
    if (own === null) { shown.innerHTML = e.data; }
  });
  es.addEventListener("utterances", function (e) {
    var stay = atEnd(), top = log.scrollTop;
    log.innerHTML = e.data;
    if (stay) { toEnd(); } else { log.scrollTop = top; jump.hidden = false; }
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
:root { color-scheme: light dark; --bg: #fafaf8; --text: #111; --muted: #777; --line: #e4e4df; --soft: #f0f0ec; --ink: #111; --on-ink: #fff;
  --live: #1f8a52; --rec: #c0392b; --warn: #b7791f; --imp: #1f6fb0; }
@media (prefers-color-scheme: dark) { :root { --bg: #0c0c0d; --text: #ececec; --muted: #8a8a90; --line: #26262a; --soft: #17171a; --ink: #ececec; --on-ink: #0c0c0d;
  --live: #3ddc97; --rec: #ff6b5e; --warn: #f5b84b; --imp: #5cc8ff; } }
* { box-sizing: border-box; }
html { font-size: clamp(16px, 1.25vw, 30px); }
body { margin: 0; background: var(--bg); color: var(--text); font-family: "Helvetica Neue", Helvetica, Arial, system-ui, sans-serif; line-height: 1.5; -webkit-font-smoothing: antialiased; }
body:not(.error) { height: 100vh; height: 100dvh; display: flex; flex-direction: column; }
header { flex: none; display: flex; justify-content: space-between; align-items: center; gap: 1rem 2rem; padding: 0.9rem 2.4rem; border-bottom: 1px solid var(--line); }
.sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
.brand { flex: none; display: flex; align-items: center; gap: 0.8rem; }
.logo { width: 1.1rem; height: 1.1rem; border-radius: 50%; background: var(--ink); }
.eyebrow, h2 { margin: 0; font-size: 0.7rem; font-weight: 500; letter-spacing: 0.13em; text-transform: uppercase; color: var(--muted); }
header .side { flex: none; display: flex; align-items: center; gap: 1.2rem; }
#portal { color: var(--muted); font-size: 0.85rem; text-decoration: underline; text-underline-offset: 0.2em; }
header h1 { margin: 0; font-size: 1.1rem; font-weight: 600; }
#status span { display: inline-flex; align-items: center; gap: 0.45em; padding: 0.15em 0.8em; border-radius: 99px; font-size: 0.8rem; font-weight: 500; border: 1px solid currentColor; }
#status span::before { content: ""; width: 0.5em; height: 0.5em; border-radius: 50%; background: currentColor; }
#status [data-status="live"] { color: var(--live); }
#status [data-status="imported"] { color: var(--imp); }
#status [data-status="ended"], #status [data-status="stopped"] { color: var(--warn); }
#status [data-status="connecting"] { color: var(--muted); }
#origin { flex: none; }
main.stage { flex: 1; min-height: 0; display: grid; grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); gap: 3.2rem; padding: 1.6rem 2.4rem 1.6rem; }
.left, .right { display: flex; flex-direction: column; gap: 2.2rem; min-width: 0; min-height: 0; }
.left { overflow-y: auto; }
section { padding: 0; }
#artifactlog { border-top: 1px solid var(--line); padding-top: 1rem; }
.transcript { position: relative; flex: 1; min-height: 0; display: flex; flex-direction: column; }
.transcript h2 { flex: none; padding-bottom: 0.6rem; margin-bottom: 0; border-bottom: 1px solid var(--line); }
#utterances { flex: 1; min-height: 0; overflow-y: auto; padding: 0.8rem 0.4rem 1rem 0; }
#jump { position: absolute; left: 50%; bottom: 1rem; transform: translateX(-50%); padding: 0.45rem 1.1rem; border: 0; border-radius: 99px; background: var(--ink); color: var(--on-ink); font: inherit; font-size: 0.85rem; font-weight: 600; cursor: pointer; box-shadow: 0 4px 14px rgba(0, 0, 0, 0.2); }
#jump[hidden] { display: none; }
h2 { margin-bottom: 0.8rem; display: flex; align-items: center; gap: 0.6rem; }
ul, ol { margin: 0; padding: 0; list-style: none; }
li { padding: 0.3rem 0; }
section:empty { display: none; }
.empty, .note { color: var(--muted); margin: 0; }
.note { color: var(--warn); }
.badge, .capture { display: inline-flex; align-items: center; gap: 0.4em; font-size: 0.75rem; font-weight: 500; vertical-align: middle; }
.badge.agent { padding: 0 0.5em; border: 1px solid var(--line); border-radius: 0.3em; color: var(--muted); }
.capture::before { content: ""; width: 0.5em; height: 0.5em; border-radius: 50%; background: currentColor; }
.capture.captured { color: var(--rec); }
.capture.paused { color: var(--warn); }
.capture.not-captured, .capture.left { color: var(--muted); }
.capture.imported { color: var(--imp); }
.avatar { flex: none; width: 2rem; height: 2rem; border-radius: 50%; border: 1px solid var(--text); display: grid; place-items: center; font-size: 0.68rem; font-weight: 600; }
.avatar::before { content: attr(data-initials); }
.avatar.agent { border-radius: 0; }
.name { font-weight: 600; }
.nameline { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
#participants { flex: 1; min-width: 0; display: flex; align-items: center; justify-content: center; gap: 0.9rem; }
#participants .empty { font-size: 0.85rem; }
.people { display: flex; flex-wrap: wrap; justify-content: center; gap: 0.5rem; }
.person { position: relative; padding: 0.2rem; border-radius: 50%; outline-offset: 2px; }
.person .avatar { width: 2.2rem; height: 2.2rem; background: var(--bg); }
.person.captured .avatar { box-shadow: 0 0 0 2px var(--bg), 0 0 0 4px var(--rec); }
.person.paused .avatar { box-shadow: 0 0 0 2px var(--bg), 0 0 0 4px var(--warn); }
.person.imported .avatar { box-shadow: 0 0 0 2px var(--bg), 0 0 0 4px var(--imp); }
.person.left .avatar { opacity: 0.4; }
.person .hand { position: absolute; top: -0.2rem; right: -0.3rem; min-width: 1.1rem; height: 1.1rem; padding: 0 0.3rem; justify-content: center; font-size: 0.65rem; line-height: 1.1rem; background: var(--warn); color: #fff; }
.act-dot { position: absolute; right: 0; bottom: 0; width: 0.7rem; height: 0.7rem; border-radius: 50%; border: 2px solid var(--bg); background: var(--muted); }
.act-dot.working { background: var(--imp); animation: pulse 1.4s ease-in-out infinite; }
.act-dot.unspoken { background: var(--warn); }
@keyframes pulse { 50% { box-shadow: 0 0 0 0.35rem transparent, 0 0 0 0.2rem var(--imp); } }
@media (prefers-reduced-motion: reduce) { .act-dot.working { animation: none; } }
.person .card { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
.person:hover .card, .person:focus .card { clip: auto; width: max-content; max-width: 20rem; height: auto; overflow: visible; top: calc(100% + 0.5rem); left: 50%; transform: translateX(-50%); z-index: 5; display: flex; flex-direction: column; gap: 0.15rem; padding: 0.6rem 0.8rem; border: 1px solid var(--line); border-radius: 6px; background: var(--bg); box-shadow: 0 6px 18px rgba(0, 0, 0, 0.15); font-size: 0.85rem; }
.card .topic { color: var(--muted); white-space: normal; }
.card .activity { color: var(--muted); }
.card .activity.working { color: var(--imp); }
.card .activity.unspoken { color: var(--warn); }
.hand, .hands-count { white-space: nowrap; display: inline-flex; align-items: center; gap: 0.25em; font-size: 0.8rem; font-weight: 600; padding: 0.05em 0.6em; border-radius: 99px; background: var(--ink); color: var(--on-ink); letter-spacing: 0; text-transform: none; }
#artifact { padding: 0; border: 1px solid var(--line); border-radius: 6px; overflow: hidden; background: var(--bg); }
#artifact iframe { display: block; width: 100%; height: 55vh; border: 0; background: #fff; }
#artifact .note, #artifact .empty { padding: 1rem 1.2rem; }
.art-head { display: flex; justify-content: space-between; gap: 1rem; padding: 0.55rem 1.2rem; border-bottom: 1px solid var(--line); }
.art-id { color: var(--muted); font-size: 0.8rem; }
.minutes-main { max-width: 40rem; margin: 0 auto; padding: 1.4rem 1.6rem 2rem; min-height: 40vh; }
.minutes-main .eyebrow { margin-bottom: 1.2rem; text-align: center; }
.minutes-body { font-family: Georgia, "Times New Roman", serif; font-size: 1.2rem; line-height: 1.7; }
.minutes-body h1, .minutes-body h2, .minutes-body h3 { font-family: "Helvetica Neue", Helvetica, Arial, sans-serif; line-height: 1.25; margin: 1.4em 0 0.4em; }
.minutes-body h1 { font-size: 2rem; font-weight: 400; font-family: Georgia, "Times New Roman", serif; margin-top: 0.2em; }
.minutes-body h2, .minutes-body h3 { font-size: 0.72rem; font-weight: 600; letter-spacing: 0.14em; text-transform: uppercase; color: var(--muted); }
.minutes-body ul, .minutes-body ol { padding-left: 1.3em; list-style: revert; }
.minutes-body li { padding: 0.15rem 0; }
.minutes-body p { margin: 0.6em 0; }
.placeholder { min-height: 36vh; display: flex; flex-direction: column; align-items: center; justify-content: center; text-align: center; gap: 0.5rem; }
.placeholder h2 { margin: 0; font-size: 1.3rem; text-transform: none; letter-spacing: 0; color: var(--text); font-weight: 500; font-family: Georgia, "Times New Roman", serif; }
.placeholder p { margin: 0; color: var(--muted); max-width: 24rem; }
.ph-icon { width: 3rem; height: 3rem; border-radius: 50%; border: 1px solid var(--line); display: grid; place-items: center; margin-bottom: 0.6rem; color: var(--muted); }
.ph-icon::before { content: "\270E"; font-size: 1.3rem; }
.viewer { display: flex; flex-direction: column; }
.tabs { display: flex; flex-wrap: wrap; gap: 0.2rem; border-bottom: 1px solid var(--line); }
.tab { padding: 0.5rem 1.1rem; color: var(--muted); font-size: 0.85rem; border: 1px solid transparent; border-bottom: 0; border-radius: 6px 6px 0 0; margin-bottom: -1px; }
.tab.focused { color: var(--text); border-color: var(--line); background: var(--bg); font-weight: 600; }
.tab { cursor: pointer; }
.tab:hover { color: var(--text); }
.tabs:has(.own) .tab.focused { border-color: transparent; font-weight: 400; color: var(--muted); }
.tab.focused::after { content: ""; display: inline-block; width: 0.4rem; height: 0.4rem; margin-left: 0.45rem; border-radius: 50%; background: var(--live); vertical-align: middle; }
.tabs .tab.own { color: var(--text); border-color: var(--line); background: var(--bg); font-weight: 600; }
.tab.pulse { animation: tabpulse 1.2s ease-in-out infinite; color: var(--text); }
@keyframes tabpulse { 50% { background: var(--soft); box-shadow: 0 0 0 0.2rem var(--live) inset; } }
@media (prefers-reduced-motion: reduce) { .tab.pulse { animation: none; box-shadow: 0 0 0 0.15rem var(--live) inset; } }
#artifacts:not(:empty) + #artifact { border-top-left-radius: 0; border-top-color: var(--line); }
.artifact-log li { display: flex; gap: 0.8rem; align-items: baseline; font-size: 0.9rem; border-bottom: 1px solid var(--line); }
.artifact-log li:last-child { border-bottom: 0; }
.artifact-log time { color: var(--muted); font-variant-numeric: tabular-nums; flex: none; }
.artifact-log .verb { color: var(--muted); }
.artifact-log .path { margin-left: auto; color: var(--muted); font-size: 0.8rem; overflow-wrap: anywhere; text-align: right; }
.right .minutes-body { font-size: 1rem; line-height: 1.6; max-height: 40vh; overflow: auto; }
.right .minutes-body h1 { font-size: 1.3rem; }
.utterances li { display: flex; gap: 0.8rem; align-items: flex-start; }
@media (max-width: 52rem) {
  body:not(.error) { height: auto; display: block; }
  header { flex-wrap: wrap; padding: 0.8rem 1rem; }
  #participants { order: 3; flex-basis: 100%; justify-content: flex-start; }
  .people { justify-content: flex-start; }
  main.stage { display: flex; flex-direction: column; gap: 2rem; padding: 1.2rem 1rem; }
  .left { overflow: visible; }
  #utterances { max-height: 70vh; }
}
.utterances .line { min-width: 0; }
.utterances time, .corrected { color: var(--muted); font-size: 0.8rem; }
#origin .imported { margin: 1.2rem 2.4rem 0; padding: 0.8rem 1.2rem; border: 1px solid var(--imp); border-radius: 6px; color: var(--imp); white-space: pre-line; }
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
<header><div class="brand"><div class="logo"></div><div><div class="eyebrow">Agentic office</div><h1>Session <span id="session">{{.Session}}</span></h1></div></div><section id="participants" aria-label="Participants">{{.Regions.participants}}</section><div class="side">{{with .Portal}}<a id="portal" href="{{.}}" rel="noreferrer" target="_blank">Open in portal</a>{{end}}<div id="status">{{.Regions.status}}</div></div></header>
<div id="origin">{{.Regions.origin}}</div>
<noscript><p class="note">This page needs JavaScript for live updates.</p></noscript>
<main id="stage" class="stage" data-events="{{.Events}}" data-artifact="{{.Artifact}}">
<div class="left">
<div class="viewer">
<section id="artifacts" aria-label="Artifacts of the session">{{.Regions.artifacts}}</section>
<section id="artifact" aria-label="Artifact in focus">{{.Regions.artifact}}</section>
</div>
<section id="artifactlog" class="artifact-log" aria-label="Artifact log">{{.Regions.artifactlog}}</section>
</div>
<div class="right">
<section class="transcript" aria-label="Transcript"><h2>Transcript</h2>
<div id="utterances" class="scroll" aria-live="polite" tabindex="0">{{.Regions.utterances}}</div>
<button id="jump" type="button" hidden>New messages &#8595;</button>
</section>
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

{{define "participants"}}<h2 class="sr-only">Participants</h2>
{{if .Participants}}<ul class="people">
{{range .Participants}}<li class="person {{.Status}}" tabindex="0" data-participant="{{.ID}}" data-kind="{{if .Agent}}agent{{else}}human{{end}}" data-capture="{{.Status}}"{{if .HandNumber}} data-hand="{{.HandNumber}}"{{end}}>{{template "avatar" .}}{{if .HandNumber}}<span class="hand" aria-hidden="true">{{.HandNumber}}</span>{{end}}{{with .Activity}}<span class="act-dot {{.State}}" aria-hidden="true"></span>{{end}}<span class="card"><span class="nameline">{{template "name" .}}</span><span class="capture {{.Status}}">{{.StatusText}}</span>{{if .HandNumber}}<span class="topic">Raised hand {{.HandNumber}}{{if .HandTopic}}: {{.HandTopic}}{{end}}</span>{{end}}{{with .Activity}}<span class="activity {{.State}}" data-activity="{{.State}}"{{if .Tool}} data-tool="{{.Tool}}"{{end}}>{{.Text}}</span>{{end}}</span></li>
{{end}}</ul>{{if .HandCount}}<span class="hands-count" data-hands="{{.HandCount}}" title="Raised hands">&#9995; {{.HandCount}}</span>{{end}}{{else}}<p class="empty">Nobody has joined yet.</p>{{end}}{{end}}

{{define "artifact"}}{{if .ShowsMinutes}}{{template "minutes-main" .}}{{else}}{{with .Artifact}}{{if .ID}}<div class="art-head"><span class="eyebrow">Now showing</span><span class="art-id">{{.ID}}</span></div>
{{end}}{{if .Content}}<iframe title="{{.ID}}" data-artifact="{{.ID}}" sandbox="" referrerpolicy="no-referrer" srcdoc="{{.Content}}"></iframe>
{{else if .ID}}<p class="note" data-artifact="{{.ID}}">{{.Note}}</p>
{{else}}<p class="empty">Nothing is shown.</p>{{end}}{{end}}{{end}}{{end}}

{{define "minutes-main"}}<div class="minutes-main">
{{if .Minutes}}<div class="eyebrow">Minutes</div>
<div class="minutes-body" data-minutes>{{.MinutesHTML}}</div>
{{else}}<div class="placeholder" data-placeholder="minutes"><div class="ph-icon" aria-hidden="true"></div>
<h2>Waiting for the minutes</h2>
<p>Nobody is taking minutes yet. Ask an agent to join this session and keep them.</p></div>
{{end}}</div>{{end}}

{{define "artifactlog"}}{{if .ArtifactLog}}<h2>Artifact log</h2>
<ol>
{{range .ArtifactLog}}<li data-log="{{.Verb}}"><time datetime="{{.Time.Format "2006-01-02T15:04:05Z07:00"}}">{{.Time.Format "15:04"}}</time><span>{{template "name" .Actor}} <span class="verb">{{.Verb}}</span></span><span class="path">{{.Path}}</span></li>
{{end}}</ol>{{end}}{{end}}

{{define "artifacts"}}{{if gt (len .Artifacts) 1}}<ul class="tabs" role="tablist">
{{range .Artifacts}}<li class="tab{{if .Focused}} focused{{end}}" role="tab" tabindex="0" aria-selected="{{.Focused}}" data-artifact="{{.Path}}" title="{{.Path}}">{{.Label}}</li>
{{end}}</ul>{{end}}{{end}}

{{define "utterances"}}{{if .Utterances}}<ol class="utterances">
{{range .Utterances}}<li data-utterance="{{.EventID}}" data-participant="{{.Speaker.ID}}">{{template "avatar" .Speaker}}<span class="line">{{template "name" .Speaker}}: {{if not .OriginalTime.IsZero}}<time datetime="{{.OriginalTime.Format "2006-01-02T15:04:05Z07:00"}}">{{.OriginalTime.Format "2006-01-02 15:04"}} UTC</time> {{end}}<span class="text">{{.Text}}</span>{{if .Corrected}} <span class="corrected">(corrected)</span>{{end}}</span></li>
{{end}}</ol>{{else}}<p class="empty">Nothing said yet.</p>{{end}}{{end}}
`))

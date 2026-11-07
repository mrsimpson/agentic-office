package stage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect/v2"

	artifactv1 "github.com/mrsimpson/agentic-office/server/artifacts/api"
	"github.com/mrsimpson/agentic-office/server/identity/api/auth"
	sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"
	sessionv1connect "github.com/mrsimpson/agentic-office/server/session/api/sessionv1connect"
)

// PathPrefix is where the stage is mounted. Identity creates links <public-url>/stage/<session>.
const PathPrefix = "/stage/"

// Artifacts loads artifacts in-process (if-artifact-api). artifacts.Service implements it.
type Artifacts interface {
	Load(ctx context.Context, id string) (*artifactv1.Artifact, error)
}

// Options configure a Handler.
type Options struct {
	// Session is a transport to the session service behind the token check, for example
	// connectinprocess.New on the server of office-server. The stage subscribes through it with
	// the token of the link, so it has exactly the rights of the link.
	Session connect.Transport
	// StageSession returns the session of a stage link token. It returns "" for a valid token
	// that is not a stage link, and an error for an unknown, expired or revoked token.
	StageSession func(token string) (string, error)
	// Artifacts loads shown artifacts. Nil means the stage shows a note instead.
	Artifacts Artifacts
	// KeepAlive is the interval of comments on an idle event stream. Default 15 seconds.
	KeepAlive time.Duration
}

// Handler serves the stage (if-stage-web).
type Handler struct {
	opts Options
}

// New returns a Handler.
func New(opts Options) *Handler {
	if opts.KeepAlive <= 0 {
		opts.KeepAlive = 15 * time.Second
	}
	return &Handler{opts: opts}
}

// ServeHTTP serves GET <PathPrefix><session>?token=… (the page) and
// GET <PathPrefix><session>/events?token=… (the event stream).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	// The token is in the URL: never send it on, never cache the page.
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")

	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), PathPrefix)
	if !ok {
		h.fail(w, http.StatusNotFound, "Not found", "There is no stage at this address.")
		return
	}
	escaped, sub, _ := strings.Cut(rest, "/")
	sid, err := url.PathUnescape(escaped)
	if err != nil || sid == "" || (sub != "" && sub != "events") {
		h.fail(w, http.StatusNotFound, "Not found", "There is no stage at this address.")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hdr.Set("Allow", "GET, HEAD")
		h.fail(w, http.StatusMethodNotAllowed, "Method not allowed", "The stage only answers GET.")
		return
	}
	token := r.URL.Query().Get("token")
	if status, title, msg := h.check(token, sid); status != http.StatusOK {
		h.fail(w, status, title, msg)
		return
	}
	if sub == "events" {
		h.events(w, r, sid, token)
		return
	}
	h.page(w, r, sid, token)
}

// check returns http.StatusOK if token is a valid stage link for the session.
func (h *Handler) check(token, sid string) (int, string, string) {
	if token == "" {
		return http.StatusUnauthorized, "No stage link",
			"This address has no token. Ask a participant for the stage link: office stage link."
	}
	if h.opts.StageSession == nil {
		return http.StatusUnauthorized, "Stage link not valid", "The server cannot check stage links."
	}
	linked, err := h.opts.StageSession(token)
	switch {
	case err != nil:
		return http.StatusUnauthorized, "Stage link not valid",
			"This stage link is unknown or has expired. Ask a participant for a new link: office stage link."
	case linked == "":
		return http.StatusForbidden, "Not a stage link",
			"This token is not a stage link. Ask a participant for the stage link: office stage link."
	case linked != sid:
		return http.StatusForbidden, "Stage link for another session",
			"This stage link is for another session."
	}
	return http.StatusOK, "", ""
}

var errorPage = template.Must(template.New("error").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · Agentic office stage</title><style>` + css + `</style></head>
<body class="error"><main><h1>{{.Title}}</h1><p id="error" data-status="{{.Status}}">{{.Message}}</p></main></body></html>
`))

func (h *Handler) fail(w http.ResponseWriter, status int, title, msg string) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = errorPage.Execute(w, map[string]any{"Title": title, "Message": msg, "Status": status})
}

// script opens the event stream and replaces each region with the HTML the server rendered.
const script = `(function () {
  var main = document.getElementById("stage");
  var status = document.getElementById("status");
  var es = new EventSource(main.getAttribute("data-events"));
  ["status", "participants", "hands", "artifact", "minutes", "utterances"].forEach(function (name) {
    es.addEventListener(name, function (e) { document.getElementById(name).innerHTML = e.data; });
  });
  es.addEventListener("end", function () { es.close(); });
  es.onerror = function () {
    status.textContent = es.readyState === EventSource.CLOSED ? "Not connected. Reload the page." : "Reconnecting …";
  };
})();`

var scriptHash = func() string {
	h := sha256.Sum256([]byte(script))
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
.capture.left { border: 2px dashed #999; color: #ccc; }
.name { font-weight: 700; }
.hands li { font-size: 1.15rem; color: #ffd400; }
#artifact iframe { width: 100%; height: 62vh; border: 0; background: #fff; }
#artifact { margin-bottom: 1rem; }
.minutes-text { white-space: pre-wrap; max-height: 40vh; overflow: auto; }
.utterances li { font-size: 1.05rem; }
.corrected { color: #ccc; font-size: 0.8rem; }
body.error main { padding: 2rem; }
body.error h1 { color: #ffd400; }
body.error p { font-size: 1.3rem; }
`

var pageTemplate = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Session}} · Agentic office stage</title><style>` + css + `</style></head>
<body>
<header><h1>Session <span id="session">{{.Session}}</span></h1><div id="status">{{.Regions.status}}</div></header>
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

// page renders the page with an empty view. The event stream fills it.
func (h *Handler) page(w http.ResponseWriter, r *http.Request, sid, token string) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src "+scriptHash+
		"; connect-src 'self'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	events := PathPrefix + url.PathEscape(sid) + "/events?" + url.Values{"token": {token}}.Encode()
	_ = pageTemplate.Execute(w, map[string]any{"Session": sid, "Events": events,
		"Regions": newView().renderAll(), "Script": template.JS(script)})
}

// coalesce is how long the stream waits for more events before it sends the changed regions.
// The first events of a stream, the history, arrive in one burst.
const coalesce = 30 * time.Millisecond

// events streams the regions of the view as server-sent events. Each event is named after a
// region and carries its inner HTML. The first message sends all regions; later messages send
// only regions that changed. After session.ended, or when the link expires, it sends "end".
func (h *Handler) events(w http.ResponseWriter, r *http.Request, sid, token string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.fail(w, http.StatusInternalServerError, "No streaming", "The server cannot stream events.")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	client := sessionv1connect.NewSessionServiceClient(connect.NewClient(h.opts.Session, auth.StaticToken(token)))
	st, err := client.Subscribe(ctx, &sessionv1.SubscribeRequest{SessionId: sid})
	if err != nil {
		h.streamError(w, err)
		return
	}
	defer st.Close()

	evs := make(chan *sessionv1.Event)
	errc := make(chan error, 1)
	go func() {
		for {
			res, err := st.Receive()
			if err != nil {
				errc <- err
				return
			}
			select {
			case evs <- res.GetEvent():
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}
		}
	}()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()

	var load func(context.Context, string) (*artifactv1.Artifact, error)
	if h.opts.Artifacts != nil {
		load = h.opts.Artifacts.Load
	}
	v := newView()
	sent := map[string]string{}
	send := func() {
		for _, region := range regions {
			html := v.render(region)
			if old, ok := sent[region]; ok && old == html {
				continue
			}
			sent[region] = html
			writeEvent(w, region, html)
		}
		flusher.Flush()
	}
	end := func() {
		writeEvent(w, "end", v.Status())
		flusher.Flush()
	}

	timer := time.NewTimer(coalesce)
	pending := true
	keepAlive := time.NewTicker(h.opts.KeepAlive)
	defer keepAlive.Stop()
	for {
		select {
		case ev := <-evs:
			v.apply(ctx, ev, load)
			if !pending {
				pending = true
				timer.Reset(coalesce)
			}
		case <-timer.C:
			pending = false
			v.live = true
			send()
		case err := <-errc:
			v.live = true
			switch {
			case errors.Is(err, io.EOF):
				// The stream ends after session.ended.
			case connect.CodeOf(err) == connect.CodeUnauthenticated:
				v.stopped = "The stage link has expired. Ask a participant for a new link."
			case connect.CodeOf(err) == connect.CodePermissionDenied:
				v.stopped = "This stage link is not allowed to show the session."
			default:
				// The server stops or the client went away. The browser reconnects.
				return
			}
			send()
			end()
			return
		case <-keepAlive.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// streamError answers an event stream request that could not subscribe.
func (h *Handler) streamError(w http.ResponseWriter, err error) {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated:
		h.fail(w, http.StatusUnauthorized, "Stage link not valid", "This stage link is unknown or has expired.")
	case connect.CodePermissionDenied:
		h.fail(w, http.StatusForbidden, "Not allowed", "This stage link is not allowed to show the session.")
	default:
		h.fail(w, http.StatusServiceUnavailable, "Session not available", "The session stream is not available.")
	}
}

// writeEvent writes one server-sent event. Every line of data gets its own "data:" field; the
// browser joins them with newlines.
func writeEvent(w io.Writer, name, data string) {
	var b strings.Builder
	b.WriteString("event: " + name + "\n")
	for _, line := range strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(data), "\n") {
		b.WriteString("data: " + line + "\n")
	}
	b.WriteString("\n")
	_, _ = io.WriteString(w, b.String())
}

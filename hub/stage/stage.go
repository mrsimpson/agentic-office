package stage

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/hub/stage/web"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
)

// Slides loads decks in-process (if-slides-api). slides.Service implements it.
type Slides interface {
	Load(ctx context.Context, id string) (*slidesv1.Deck, error)
}

// Options configure a Handler.
type Options struct {
	// Session is a transport to the session service behind the token check, for example
	// connectinprocess.New on the server of office-hub. The stage subscribes through it with
	// the token of the link, so it has exactly the rights of the link.
	Session connect.Transport
	// StageSession returns the session of a stage link token. It returns "" for a valid token
	// that is not a stage link, and an error for an unknown, expired or revoked token.
	StageSession func(token string) (string, error)
	// Slides loads shown decks (artifact IDs "slides/<n>"). Nil means the stage shows a note
	// instead.
	Slides Slides
	// Artifacts reads artifacts by their path (if-archive-read). Nil means the stage shows a note
	// instead.
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

// ServeHTTP serves GET <web.PathPrefix><session>?token=… (the page) and
// GET <web.PathPrefix><session>/events?token=… (the event stream).
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	// The token is in the URL: never send it on, never cache the page.
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Content-Type-Options", "nosniff")

	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), web.PathPrefix)
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

func (h *Handler) fail(w http.ResponseWriter, status int, title, msg string) {
	w.Header().Set("Content-Security-Policy", web.ErrorCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = web.ErrorPage.Execute(w, map[string]any{"Title": title, "Message": msg, "Status": status})
}

// page renders the page with an empty view. The event stream fills it.
func (h *Handler) page(w http.ResponseWriter, r *http.Request, sid, token string) {
	w.Header().Set("Content-Security-Policy", web.PageCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	events := web.PathPrefix + url.PathEscape(sid) + "/events?" + url.Values{"token": {token}}.Encode()
	_ = web.Page.Execute(w, map[string]any{"Session": sid, "Events": events,
		"Regions": newView().renderAll(), "Script": template.JS(web.Script)})
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
	client := sessionstreamv1connect.NewSessionStreamServiceClient(connect.NewClient(h.opts.Session, auth.StaticToken(token)))
	st, err := client.Subscribe(ctx, &sessionstreamv1.SubscribeRequest{SessionId: sid})
	if err != nil {
		h.streamError(w, err)
		return
	}
	defer st.Close()

	evs := make(chan *sessionstreamv1.Event)
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

	v := newView()
	sent := map[string]string{}
	send := func() {
		for _, region := range web.Regions {
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
			v.apply(ev)
			if !pending {
				pending = true
				timer.Reset(coalesce)
			}
		case <-timer.C:
			pending = false
			v.live = true
			v.refresh(ctx, h.opts.Slides, h.opts.Artifacts)
			send()
		case err := <-errc:
			v.live = true
			v.refresh(ctx, h.opts.Slides, h.opts.Artifacts)
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

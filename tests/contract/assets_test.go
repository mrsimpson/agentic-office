package contract

// Contract tests of the static files of the stage and the portal (dec-ui-base): the base style
// sheet and the page style sheet are served at an address of their own, without a token and
// without a login, and the Content-Security-Policy of each page allows them. The tests start the
// real office-hub and read the pages as a browser does. No test depends on timing.

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// linkedSheets returns the addresses that the page links as style sheets, in the order of the page.
func linkedSheets(doc *html.Node) []string {
	var hrefs []string
	for _, n := range findAll(doc, byTag("link")) {
		if attrOf(n, "rel") == "stylesheet" {
			hrefs = append(hrefs, attrOf(n, "href"))
		}
	}
	return hrefs
}

// hashedSheet matches the address of a style sheet: its name holds a hash of the content.
var hashedSheet = regexp.MustCompile(`^/(stage|portal)/assets/(base|stage|portal)-[0-9a-f]{12}\.css$`)

// checkStyleSheets checks that a page links the base and its own style sheet, in this order, from
// the address prefix, and that each address answers as a static file: without a token, without a
// cookie, as text/css, cached for a long time, and without any data of a session.
func checkStyleSheets(t *testing.T, srv *contracttest.Hub, doc *html.Node, prefix, own string, secrets ...string) {
	t.Helper()
	hrefs := linkedSheets(doc)
	if len(hrefs) != 2 {
		t.Fatalf("the page links %d style sheets %v, want 2 (the base, then the page)", len(hrefs), hrefs)
	}
	for i, want := range []string{"base", own} {
		if !hashedSheet.MatchString(hrefs[i]) || !strings.HasPrefix(hrefs[i], prefix+want+"-") {
			t.Errorf("style sheet %d is %q, want %s%s-<hash>.css", i+1, hrefs[i], prefix, want)
		}
	}
	// A client without cookie and without token: a new client of the hub has none.
	hc := srv.HTTPClient(t)
	for _, href := range hrefs {
		res, body := fetch(t, hc, srv.URL+href)
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", href, res.StatusCode)
			continue
		}
		if ct := res.Header.Get("Content-Type"); ct != "text/css; charset=utf-8" {
			t.Errorf("GET %s: content type %q", href, ct)
		}
		cc := res.Header.Get("Cache-Control")
		if !strings.Contains(cc, "max-age=31536000") || !strings.Contains(cc, "immutable") || strings.Contains(cc, "no-store") {
			t.Errorf("GET %s: Cache-Control %q, want a long cache for an address with a hash", href, cc)
		}
		if res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s: no X-Content-Type-Options: nosniff", href)
		}
		if len(res.Cookies()) != 0 {
			t.Errorf("GET %s sets a cookie", href)
		}
		if body == "" || strings.Contains(body, "@font-face") || strings.Contains(body, "url(") {
			t.Errorf("GET %s: empty, or it loads a font or a file (%d bytes)", href, len(body))
		}
		// A style sheet holds no session data: no data anchor of a page and none of the secrets.
		if strings.Contains(body, `data-participant="`) || strings.Contains(body, `data-session="`) {
			t.Errorf("GET %s has data anchors of a page", href)
		}
		for _, s := range secrets {
			if s != "" && strings.Contains(body, s) {
				t.Errorf("GET %s leaks %q", href, s)
			}
		}
		if res.Header.Get("Content-Security-Policy") != "default-src 'none'" {
			t.Errorf("GET %s: CSP %q", href, res.Header.Get("Content-Security-Policy"))
		}
	}
	// The base holds our theme and the components.
	if _, body := fetch(t, hc, srv.URL+hrefs[0]); !strings.Contains(body, "--db-font-family-sans:system-ui") || !strings.Contains(body, ".db-button") {
		t.Errorf("the base does not hold the theme and the button component")
	}
	// The notice of the third-party licences is served next to the style sheets.
	if res, body := fetch(t, hc, srv.URL+prefix+"third-party-notices.txt"); res.StatusCode != http.StatusOK ||
		!strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") || !strings.Contains(body, "Apache License") {
		t.Errorf("GET %sthird-party-notices.txt: status %d, content type %q", prefix, res.StatusCode, res.Header.Get("Content-Type"))
	}
	// An unknown name answers 404 and shows no data; so does a style sheet with a wrong hash.
	wrongHash := strings.Replace(hrefs[0], "base-", "base-0", 1)[len(prefix):]
	for _, name := range []string{"nothing.css", "base.css", wrongHash, "base-"} {
		res, body := fetch(t, hc, srv.URL+prefix+name)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s%s: status %d, want 404", prefix, name, res.StatusCode)
		}
		for _, s := range secrets {
			if s != "" && strings.Contains(body, s) {
				t.Errorf("GET %s%s leaks %q", prefix, name, s)
			}
		}
	}
}

// cspDirectives returns the directives of a Content-Security-Policy by name.
func cspDirectives(header string) map[string]string {
	out := map[string]string{}
	for _, d := range strings.Split(header, ";") {
		name, val, _ := strings.Cut(strings.TrimSpace(d), " ")
		out[name] = val
	}
	return out
}

// TestStageServesItsStyleSheets: the stage page links the base and the page style sheet from
// /stage/assets/, the files answer without a token, and the Content-Security-Policy of the page
// and of the error page allows them and keeps default-src 'none'.
func TestStageServesItsStyleSheets(t *testing.T) {
	m := newStageMeeting(t)
	res, body := get(t, m.srv, m.link)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("page: status %d", res.StatusCode)
	}
	doc := parseDoc(t, body)
	checkStyleSheets(t, m.srv, doc, "/stage/assets/", "stage", stageToken(t, m.link), m.sid)
	// The page has no inline style sheet of its own; only artifacts in a frame bring theirs.
	if len(findAll(doc, byTag("style"))) != 0 {
		t.Errorf("the page has an inline style element")
	}

	pageCSP := cspDirectives(res.Header.Get("Content-Security-Policy"))
	if pageCSP["default-src"] != "'none'" || pageCSP["style-src"] != "'self' 'unsafe-inline'" ||
		!strings.HasPrefix(pageCSP["script-src"], "'sha256-") || pageCSP["connect-src"] != "'self'" {
		t.Errorf("page CSP %v", pageCSP)
	}

	// The error page links the same files and has the same style rule.
	res, body = get(t, m.srv, withToken(t, m.link, ""))
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error page: status %d", res.StatusCode)
	}
	checkStyleSheets(t, m.srv, parseDoc(t, body), "/stage/assets/", "stage", m.sid)
	errCSP := cspDirectives(res.Header.Get("Content-Security-Policy"))
	if errCSP["default-src"] != "'none'" || errCSP["style-src"] != "'self' 'unsafe-inline'" {
		t.Errorf("error page CSP %v", errCSP)
	}
}

// TestStageKeepsInlineStylesForArtifacts: an artifact in a frame brings its own style block, and a
// frame with srcdoc takes the Content-Security-Policy of the page that holds it. So the page must
// allow inline styles in style-src, next to 'self'; without them every deck would show unstyled.
//
// The test checks the header of the page response, not the rendering in a browser: the policy is
// the whole mechanism, and a header check needs no browser. It confirms the evidence row of
// dec-ui-base (the stage keeps inline styles because of artifacts in a frame).
func TestStageKeepsInlineStylesForArtifacts(t *testing.T) {
	m := newStageMeeting(t)
	deck := render(t, m.reader, budgetDeck())
	if !strings.Contains(string(deck.GetContent()), "<style") {
		t.Fatalf("a rendered deck has no style block of its own: this test would prove nothing")
	}
	bot := m.joinAs("Minute Bot", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT, false)
	if _, err := m.s(bot).FocusArtifact(m.ctx(), &sessioncommandsv1.FocusArtifactRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: bot, ArtifactId: deck.GetId()}); err != nil {
		t.Fatal(err)
	}
	st := openStage(t, m.srv, m.link)
	n := st.wait("artifact", "iframe of "+deck.GetId(), func(n *html.Node) bool { return len(findAll(n, byTag("iframe"))) == 1 })
	if srcdoc, _ := attr(findAll(n, byTag("iframe"))[0], "srcdoc"); !strings.Contains(srcdoc, "<style") {
		t.Fatalf("the frame holds no style block")
	}
	res, _ := get(t, m.srv, m.link)
	style := cspDirectives(res.Header.Get("Content-Security-Policy"))["style-src"]
	if !strings.Contains(style, "'unsafe-inline'") || !strings.Contains(style, "'self'") {
		t.Errorf("style-src %q of the page must allow 'self' and 'unsafe-inline': the artifact in the frame brings its own style block", style)
	}
}

// TestPortalServesItsStyleSheets: every portal page, with a login or without, links the base and the
// page style sheet from /portal/assets/, the files answer without a login, and the
// Content-Security-Policy allows style sheets of the same origin only (no inline style), keeps
// default-src 'none' and forbids framing.
func TestPortalServesItsStyleSheets(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	srv.Participant(t, "ada", "Ada Lovelace", human)

	check := func(name string, res *http.Response, body string) {
		t.Helper()
		doc := parseDoc(t, body)
		checkStyleSheets(t, srv, doc, "/portal/assets/", "portal", "Ada Lovelace")
		if len(findAll(doc, byTag("style"))) != 0 {
			t.Errorf("%s: the page has an inline style element", name)
		}
		if n := findAll(doc, func(n *html.Node) bool { _, ok := attr(n, "style"); return ok }); len(n) != 0 {
			t.Errorf("%s: the page has %d style attributes, which the policy refuses", name, len(n))
		}
		raw := res.Header.Get("Content-Security-Policy")
		c := cspDirectives(raw)
		if c["default-src"] != "'none'" || c["style-src"] != "'self'" || c["frame-ancestors"] != "'none'" ||
			strings.Contains(raw, "unsafe-inline") || c["script-src"] != "" {
			t.Errorf("%s: CSP %q", name, raw)
		}
	}

	// An error page: no login.
	anon := browser(t, srv, true)
	res, body := fetch(t, anon, srv.URL+"/portal/admin/keys")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a login: status %d", res.StatusCode)
	}
	check("error page", res, body)

	// A page with a login.
	link, err := admin.Identity.CreatePortalLink(ctx10(t), &identityv1.CreatePortalLinkRequest{CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	b := browser(t, srv, true)
	if res, _ := fetch(t, b, link.GetUrl()); res.StatusCode != http.StatusOK {
		t.Fatalf("portal link: status %d", res.StatusCode)
	}
	res, body = fetch(t, b, srv.URL+"/portal/admin/keys")
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "Ada Lovelace") {
		t.Fatalf("keys page: status %d", res.StatusCode)
	}
	check("keys page", res, body)

	// The assets answer a browser that has the cookie as well, and they do not need it. A page of
	// the portal is never cached; an asset is.
	href := linkedSheets(parseDoc(t, body))[0]
	if res, _ := fetch(t, b, srv.URL+href); res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") == "no-store" {
		t.Errorf("GET %s with a login: status %d, Cache-Control %q", href, res.StatusCode, res.Header.Get("Cache-Control"))
	}
	if res, _ := fetch(t, b, srv.URL+"/portal/admin/keys"); res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("a page of the portal: Cache-Control %q, want no-store", res.Header.Get("Cache-Control"))
	}
	// Assets answer GET and HEAD only.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+href, strings.NewReader("x"))
	if res, err := srv.HTTPClient(t).Do(req); err != nil || res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST %s: %v, %v, want 405", href, res, err)
	}
}

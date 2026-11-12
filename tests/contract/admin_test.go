package contract

// Contract tests of the admin view (if-admin-web, dec-admin-web). They start the real office-hub
// and open the pages with a browser-like HTTP client.

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"golang.org/x/net/html"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// browser is an HTTP client with cookies. It does not follow redirects when follow is false.
func browser(t *testing.T, srv *contracttest.Hub, follow bool) *http.Client {
	t.Helper()
	hc := srv.HTTPClient(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	hc.Jar = jar
	if !follow {
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return hc
}

func fetch(t *testing.T, hc *http.Client, url string) (*http.Response, string) {
	t.Helper()
	res, err := hc.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// An admin link opens the view once. The session is a cookie that scripts cannot read; the pages
// show the users and their keys, a revoked key, and the placeholders.
func TestAdminViewOpensWithALink(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	srv.Participant(t, "ada", "Ada Lovelace", human)
	srv.Participant(t, "bot", "Bot", identityv1.ActorKind_ACTOR_KIND_AGENT)
	ctx := ctx10(t)
	newLink := func() string {
		res, err := admin.Identity.CreateAdminLink(ctx, &identityv1.CreateAdminLinkRequest{CommandId: coreapi.NewCommandID()})
		if err != nil {
			t.Fatal(err)
		}
		return res.GetUrl()
	}

	// Without a session there is nothing to see.
	anon := browser(t, srv, true)
	if res, body := fetch(t, anon, srv.URL+"/admin/keys"); res.StatusCode != http.StatusUnauthorized || strings.Contains(body, "Ada") {
		t.Fatalf("without a session: status %d, %q", res.StatusCode, body)
	}
	if res, _ := fetch(t, anon, srv.URL+"/admin/login?code=oal_unknown"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown code: status %d", res.StatusCode)
	}

	link := newLink()
	first := browser(t, srv, false)
	res, _ := fetch(t, first, link)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/admin/keys" {
		t.Fatalf("link: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
	var session *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "office_admin" {
			session = c
		}
	}
	if session == nil || !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteStrictMode || session.Path != "/admin/" || session.Value == "" {
		t.Fatalf("session cookie: %+v", session)
	}
	// The code works once.
	if res, _ := fetch(t, browser(t, srv, true), link); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("second use of the link: status %d", res.StatusCode)
	}

	// The pages, with a browser that follows the redirect.
	b := browser(t, srv, true)
	res, body := fetch(t, b, newLink())
	if res.StatusCode != http.StatusOK {
		t.Fatalf("keys page: status %d\n%s", res.StatusCode, body)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") ||
		strings.Contains(csp, "script-src") || res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("headers: CSP %q, cache %q", csp, res.Header.Get("Cache-Control"))
	}
	doc := parseDoc(t, body)
	if len(findAll(doc, byTag("script"))) != 0 {
		t.Errorf("the page has scripts")
	}
	row := func(pid string) *html.Node {
		els := findAll(doc, withAttr("data-participant", pid))
		if len(els) != 1 {
			t.Fatalf("%d rows for %s in:\n%s", len(els), pid, text(doc))
		}
		return els[0]
	}
	if got := text(row("ada")); !strings.Contains(got, "Ada Lovelace") || !strings.Contains(got, "human") || !strings.Contains(got, "active") || strings.Contains(got, "admin") {
		t.Errorf("row of ada: %q", got)
	}
	if got := text(row("bot")); !strings.Contains(got, "agent") {
		t.Errorf("row of bot: %q", got)
	}
	if got := text(row(contracttest.AdminID)); !strings.Contains(got, "admin") {
		t.Errorf("row of the admin: %q", got)
	}

	// A revoked key shows as revoked.
	keys, err := admin.Identity.ListKeys(ctx, &identityv1.ListKeysRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys.GetKeys() {
		if k.GetParticipantId() == "bot" {
			if _, err := admin.Identity.RevokeKey(ctx, &identityv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(), KeyId: k.GetKeyId()}); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, body = fetch(t, b, srv.URL+"/admin/keys")
	doc = parseDoc(t, body)
	if got := text(row("bot")); !strings.Contains(got, "revoked") {
		t.Errorf("row of the revoked bot: %q", got)
	}

	// The placeholders.
	for _, p := range []string{"/admin/tenants", "/admin/admins"} {
		res, body := fetch(t, b, srv.URL+p)
		if res.StatusCode != http.StatusOK || len(findAll(parseDoc(t, body), withAttr("data-placeholder", p))) != 1 {
			t.Errorf("%s: status %d", p, res.StatusCode)
		}
	}
	if res, _ := fetch(t, b, srv.URL+"/admin/nothing"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown page: status %d", res.StatusCode)
	}
}

// A session cookie that the identity part does not know ends the session. A participant who is
// not an admin gets no link.
func TestAdminViewRefusesOthers(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	_, err := ada.Identity.CreateAdminLink(ctx10(t), &identityv1.CreateAdminLinkRequest{CommandId: coreapi.NewCommandID()})
	wantCode(t, "admin link for a participant", err, connect.CodePermissionDenied)

	req, _ := http.NewRequestWithContext(ctx10(t), http.MethodGet, srv.URL+"/admin/keys", nil)
	req.AddCookie(&http.Cookie{Name: "office_admin", Value: "oat_forged"})
	res, err := srv.HTTPClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	cleared := false
	for _, c := range res.Cookies() {
		cleared = cleared || (c.Name == "office_admin" && c.MaxAge < 0)
	}
	if res.StatusCode != http.StatusUnauthorized || !cleared {
		t.Errorf("forged cookie: status %d, cookie cleared %v", res.StatusCode, cleared)
	}
	post, _ := http.NewRequestWithContext(ctx10(t), http.MethodPost, srv.URL+"/admin/keys", nil)
	res, err = srv.HTTPClient(t).Do(post)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d", res.StatusCode)
	}
}

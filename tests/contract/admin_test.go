package contract

// Contract tests of the admin view (if-admin-web, dec-admin-web). They start the real office-hub
// and open the pages with a browser-like HTTP client.

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"golang.org/x/net/html"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
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

// signIn returns a browser that has opened a fresh admin link.
func signIn(t *testing.T, srv *contracttest.Hub) *http.Client {
	t.Helper()
	res, err := srv.Admin(t).Identity.CreateAdminLink(ctx10(t), &identityv1.CreateAdminLinkRequest{CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	b := browser(t, srv, true)
	if r, body := fetch(t, b, res.GetUrl()); r.StatusCode != http.StatusOK {
		t.Fatalf("sign in: status %d\n%s", r.StatusCode, body)
	}
	return b
}

// formField returns the value of an input of the page.
func formField(t *testing.T, doc *html.Node, name string) string {
	t.Helper()
	for _, el := range findAll(doc, withAttr("name", name)) {
		if v, ok := attr(el, "value"); ok {
			return v
		}
	}
	t.Fatalf("no field %q in the page", name)
	return ""
}

func post(t *testing.T, hc *http.Client, url string, v url.Values) (*http.Response, string) {
	t.Helper()
	res, err := hc.PostForm(url, v)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// An admin registers a key with a form and revokes it after a confirmation. The key works until it
// is revoked.
func TestAdminViewRegistersAndRevokesKeys(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	b := signIn(t, srv)
	ctx := ctx10(t)

	_, body := fetch(t, b, srv.URL+"/admin/keys/new")
	doc := parseDoc(t, body)
	csrf := formField(t, doc, "csrf")
	key, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := auth.FormatPublicKey(auth.PublicKey(key))
	res, body := post(t, b, srv.URL+"/admin/keys/register", url.Values{"csrf": {csrf}, "participant_id": {"newbot"},
		"display_name": {"New Bot"}, "kind": {"agent"}, "public_key": {pub}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register: status %d\n%s", res.StatusCode, body)
	}
	doc = parseDoc(t, body)
	if len(findAll(doc, withAttr("data-notice", ""))) != 1 || !strings.Contains(text(doc), "The key is registered.") {
		t.Errorf("no notice after the registration: %q", text(doc))
	}
	row := findAll(doc, withAttr("data-participant", "newbot"))
	if len(row) != 1 || !strings.Contains(text(row[0]), "New Bot") || !strings.Contains(text(row[0]), "agent") || !strings.Contains(text(row[0]), "active") {
		t.Fatalf("row of the new key: %q", text(doc))
	}
	// The new key works.
	bot := srv.WithKey(t, key)
	if _, err := bot.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatalf("the registered key does not work: %v", err)
	}
	// Notice and page are gone after a reload.
	if _, body := fetch(t, b, srv.URL+"/admin/keys"); strings.Contains(body, "The key is registered.") {
		t.Errorf("the notice stays")
	}

	// Revoke: the link opens a page that asks, a post revokes.
	keyID := auth.KeyID(auth.PublicKey(key))
	_, body = fetch(t, b, srv.URL+"/admin/keys")
	var revokeHref string
	for _, a := range findAll(parseDoc(t, body), byTag("a")) {
		if h, _ := attr(a, "href"); strings.HasPrefix(h, "/admin/keys/revoke?") && strings.Contains(h, "key_id=") {
			if r := findAll(parseDoc(t, body), withAttr("data-key", keyID)); len(r) == 1 && strings.Contains(text(r[0]), "Revoke") {
				revokeHref = h
			}
		}
	}
	if revokeHref == "" {
		t.Fatalf("no link to revoke the new key")
	}
	res, body = fetch(t, b, srv.URL+revokeHref)
	doc = parseDoc(t, body)
	if res.StatusCode != http.StatusOK || len(findAll(doc, withAttr("data-confirm", "revoke"))) != 1 || !strings.Contains(text(doc), "New Bot") {
		t.Fatalf("confirmation: status %d\n%s", res.StatusCode, text(doc))
	}
	// Nothing happened yet.
	if _, err := bot.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatalf("the key stopped before the confirmation: %v", err)
	}
	res, body = post(t, b, srv.URL+"/admin/keys/revoke", url.Values{"csrf": {formField(t, doc, "csrf")}, "key_id": {keyID}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "The key is revoked.") {
		t.Fatalf("revoke: status %d\n%s", res.StatusCode, body)
	}
	if r := findAll(parseDoc(t, body), withAttr("data-key", keyID)); len(r) != 1 || !strings.Contains(text(r[0]), "revoked") || strings.Contains(text(r[0]), "Revoke ") {
		t.Errorf("row after the revocation: %q", text(parseDoc(t, body)))
	}
	if _, err := bot.Identity.CreateStageLink(ctx, &identityv1.CreateStageLinkRequest{SessionId: "s", CommandId: coreapi.NewCommandID()}); err == nil {
		t.Errorf("the revoked key still works")
	}
}

// The forms refuse posts that do not come from a page of the view, bad input and keys that cannot
// be changed. The form keeps what the admin typed.
func TestAdminViewFormsRefuse(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	srv.Participant(t, "ada", "Ada", human)
	b := signIn(t, srv)
	_, body := fetch(t, b, srv.URL+"/admin/keys/new")
	csrf := formField(t, parseDoc(t, body), "csrf")
	key, _ := auth.NewKey()
	pub := auth.FormatPublicKey(auth.PublicKey(key))
	form := func(over map[string]string) url.Values {
		v := url.Values{"csrf": {csrf}, "participant_id": {"carl"}, "display_name": {"Carl"}, "kind": {"human"}, "public_key": {pub}}
		for k, s := range over {
			v.Set(k, s)
		}
		return v
	}
	reg := srv.URL + "/admin/keys/register"

	// Not from a page of the view.
	if res, _ := post(t, b, reg, form(map[string]string{"csrf": "wrong"})); res.StatusCode != http.StatusForbidden {
		t.Errorf("wrong token: status %d", res.StatusCode)
	}
	req, _ := http.NewRequestWithContext(ctx10(t), http.MethodPost, reg, strings.NewReader(form(nil).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	if res, err := b.Do(req); err != nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("foreign origin: %v, %v", res, err)
	}
	if res, _ := post(t, browser(t, srv, true), reg, form(nil)); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no session: status %d", res.StatusCode)
	}
	// Another session cannot use the token of this page.
	other := signIn(t, srv)
	if res, _ := post(t, other, reg, form(nil)); res.StatusCode != http.StatusForbidden {
		t.Errorf("token of another session: status %d", res.StatusCode)
	}
	if res, _ := fetch(t, b, srv.URL+"/admin/keys/register"); res.Request.URL.Path != "/admin/keys" {
		t.Errorf("GET of the register address ends at %s", res.Request.URL.Path)
	}

	// Bad input: the form comes back with the message and the values.
	for name, v := range map[string]url.Values{
		"public key": form(map[string]string{"public_key": "not a key"}),
		"user ID":    form(map[string]string{"participant_id": "bad id!"}),
		"no name":    form(map[string]string{"display_name": ""}),
	} {
		res, body := post(t, b, reg, v)
		doc := parseDoc(t, body)
		if res.StatusCode != http.StatusBadRequest || len(findAll(doc, withAttr("data-error", ""))) != 1 {
			t.Errorf("%s: status %d\n%s", name, res.StatusCode, text(doc))
			continue
		}
		if got := text(findAll(doc, byTag("textarea"))[0]); got != v.Get("public_key") {
			t.Errorf("%s: the form lost the public key: %q", name, got)
		}
	}
	// A key of another participant, and a participant with another kind.
	var adaKey string
	keys, _ := srv.Admin(t).Identity.ListKeys(ctx10(t), &identityv1.ListKeysRequest{})
	for _, k := range keys.GetKeys() {
		if k.GetParticipantId() == "ada" {
			adaKey = k.GetKeyId()
		}
	}
	if res, _ := post(t, b, reg, form(map[string]string{"participant_id": "ada", "kind": "agent"})); res.StatusCode != http.StatusConflict {
		t.Errorf("ada as an agent: status %d", res.StatusCode)
	}

	// Revoke: a key of the admin file, an unknown key and a key that is revoked.
	var adminKey string
	for _, k := range keys.GetKeys() {
		if k.GetAdmin() {
			adminKey = k.GetKeyId()
		}
	}
	if res, _ := fetch(t, b, srv.URL+"/admin/keys/revoke?key_id="+url.QueryEscape(adminKey)); res.StatusCode != http.StatusConflict {
		t.Errorf("confirm for the key of the admin file: status %d", res.StatusCode)
	}
	if res, _ := fetch(t, b, srv.URL+"/admin/keys/revoke?key_id=nothing"); res.StatusCode != http.StatusConflict {
		t.Errorf("confirm for an unknown key: status %d", res.StatusCode)
	}
	rev := srv.URL + "/admin/keys/revoke"
	if res, _ := post(t, b, rev, url.Values{"csrf": {csrf}, "key_id": {adminKey}}); res.StatusCode != http.StatusConflict {
		t.Errorf("revoke of the key of the admin file: status %d", res.StatusCode)
	}
	if res, _ := post(t, b, rev, url.Values{"csrf": {"wrong"}, "key_id": {adaKey}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("revoke with a wrong token: status %d", res.StatusCode)
	}
	if res, _ := post(t, b, rev, url.Values{"csrf": {csrf}, "key_id": {"nothing"}}); res.StatusCode != http.StatusConflict {
		t.Errorf("revoke of an unknown key: status %d", res.StatusCode)
	}
}

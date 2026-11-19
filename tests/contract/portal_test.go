package contract

// Contract tests of the portal (if-portal-web, dec-web-portal). They start the real office-hub and
// open the pages with a browser-like HTTP client.

import (
	"crypto/ed25519"
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
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
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

// A portal link opens the portal once. The session is a cookie that scripts cannot read. For a
// system admin the pages show the users and their keys, a revoked key, the tenants and the system
// admins.
func TestPortalAdministration(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	srv.Participant(t, "ada", "Ada Lovelace", human)
	srv.Participant(t, "bot", "Bot", identityv1.ActorKind_ACTOR_KIND_AGENT)
	ctx := ctx10(t)
	newLink := func() string {
		res, err := admin.Identity.CreatePortalLink(ctx, &identityv1.CreatePortalLinkRequest{CommandId: coreapi.NewCommandID()})
		if err != nil {
			t.Fatal(err)
		}
		return res.GetUrl()
	}

	// Without a session there is nothing to see.
	anon := browser(t, srv, true)
	if res, body := fetch(t, anon, srv.URL+"/portal/admin/keys"); res.StatusCode != http.StatusUnauthorized || strings.Contains(body, "Ada") {
		t.Fatalf("without a session: status %d, %q", res.StatusCode, body)
	}
	if res, _ := fetch(t, anon, srv.URL+"/portal/login?code=opl_unknown"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown code: status %d", res.StatusCode)
	}

	link := newLink()
	first := browser(t, srv, false)
	res, _ := fetch(t, first, link)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/portal/" {
		t.Fatalf("link: status %d, location %q", res.StatusCode, res.Header.Get("Location"))
	}
	var session *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "office_portal" {
			session = c
		}
	}
	if session == nil || !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteStrictMode || session.Path != "/portal/" || session.Value == "" {
		t.Fatalf("session cookie: %+v", session)
	}
	// The code works once.
	if res, _ := fetch(t, browser(t, srv, true), link); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("second use of the link: status %d", res.StatusCode)
	}

	// The pages, with a browser that follows the redirect to the start page.
	b := browser(t, srv, true)
	res, body := fetch(t, b, newLink())
	if res.StatusCode != http.StatusOK || len(findAll(parseDoc(t, body), withAttr("data-nav", "/portal/admin/keys"))) != 1 {
		t.Fatalf("start page of a system admin: status %d\n%s", res.StatusCode, body)
	}
	res, body = fetch(t, b, srv.URL+"/portal/admin/keys")
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
	if got := text(row("ada")); !strings.Contains(got, "Ada Lovelace") || !strings.Contains(got, "human") || !strings.Contains(got, "active") || strings.Contains(got, "system admin") {
		t.Errorf("row of ada: %q", got)
	}
	if got := text(row("bot")); !strings.Contains(got, "agent") {
		t.Errorf("row of bot: %q", got)
	}
	if got := text(row(contracttest.AdminID)); !strings.Contains(got, "system admin") {
		t.Errorf("row of the system admin: %q", got)
	}

	// A user needs a key that works, so a key is revoked when its user has another one.
	extra, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	extraID := srv.RegisterKey(t, "bot", "", identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED, extra)
	var botKey string
	users, err := admin.User.ListUsers(ctx, &userv1.ListUsersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users.GetUsers() {
		if u.GetUserId() == "bot" {
			botKey = u.GetKeys()[0].GetKeyId()
		}
	}
	if _, err := admin.User.RevokeKey(ctx, &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(), KeyId: botKey}); err != nil {
		t.Fatal(err)
	}
	_, body = fetch(t, b, srv.URL+"/portal/admin/keys")
	doc = parseDoc(t, body)
	if r := findAll(doc, withAttr("data-key", botKey)); len(r) != 1 || !strings.Contains(text(r[0]), "revoked") {
		t.Errorf("row of the revoked key: %q", text(doc))
	}
	if r := findAll(doc, withAttr("data-key", extraID)); len(r) != 1 || !strings.Contains(text(r[0]), "active") {
		t.Errorf("row of the other key: %q", text(doc))
	}

	// The tenants and the system admins.
	_, body = fetch(t, b, srv.URL+"/portal/admin/tenants")
	if rows := findAll(parseDoc(t, body), withAttr("data-tenant", contracttest.DefaultTenant)); len(rows) != 1 || !strings.Contains(text(rows[0]), "active") {
		t.Errorf("tenants page: %q", text(parseDoc(t, body)))
	}
	_, body = fetch(t, b, srv.URL+"/portal/admin/admins")
	doc = parseDoc(t, body)
	if len(findAll(doc, withAttr("data-participant", contracttest.AdminID))) != 1 || len(findAll(doc, withAttr("data-participant", "ada"))) != 0 {
		t.Errorf("system admins page: %q", text(doc))
	}
	if res, _ := fetch(t, b, srv.URL+"/portal/admin/nothing"); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown page: status %d", res.StatusCode)
	}
}

// A session cookie that the identity part does not know ends the session. A user who is not a
// system admin gets a link, but no administration: no links in the navigation, and the pages
// refuse.
func TestPortalRefusesOthers(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	srv.Participant(t, "ada", "Ada", human)
	bob := srv.Participant(t, "bob", "Bob", human)
	res, err := bob.Identity.CreatePortalLink(ctx10(t), &identityv1.CreatePortalLinkRequest{CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatalf("portal link for a member: %v", err)
	}
	b := browser(t, srv, true)
	if r, body := fetch(t, b, res.GetUrl()); r.StatusCode != http.StatusOK || len(findAll(parseDoc(t, body), withAttr("data-nav", "/portal/admin/keys"))) != 0 {
		t.Fatalf("start of a member: status %d\n%s", r.StatusCode, body)
	}
	for _, page := range []string{"/portal/admin/keys", "/portal/admin/keys/new", "/portal/admin/tenants", "/portal/admin/admins"} {
		if r, body := fetch(t, b, srv.URL+page); r.StatusCode != http.StatusForbidden || strings.Contains(body, "Ada") {
			t.Errorf("%s for a member: status %d", page, r.StatusCode)
		}
	}

	req, _ := http.NewRequestWithContext(ctx10(t), http.MethodGet, srv.URL+"/portal/admin/keys", nil)
	req.AddCookie(&http.Cookie{Name: "office_portal", Value: "oat_forged"})
	r, err := srv.HTTPClient(t).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	cleared := false
	for _, c := range r.Cookies() {
		cleared = cleared || (c.Name == "office_portal" && c.MaxAge < 0)
	}
	if r.StatusCode != http.StatusUnauthorized || !cleared {
		t.Errorf("forged cookie: status %d, cookie cleared %v", r.StatusCode, cleared)
	}
	post, _ := http.NewRequestWithContext(ctx10(t), http.MethodPost, srv.URL+"/portal/admin/keys", nil)
	r, err = srv.HTTPClient(t).Do(post)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d", r.StatusCode)
	}
	// The admin view is gone.
	if r, _ := fetch(t, browser(t, srv, true), srv.URL+"/admin/keys"); r.StatusCode != http.StatusNotFound {
		t.Errorf("/admin/keys: status %d, want 404", r.StatusCode)
	}
}

// signIn returns a browser that has opened a fresh portal link of the system admin.
func signIn(t *testing.T, srv *contracttest.Hub) *http.Client {
	t.Helper()
	res, err := srv.Admin(t).Identity.CreatePortalLink(ctx10(t), &identityv1.CreatePortalLinkRequest{CommandId: coreapi.NewCommandID()})
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

// keyWorks reports whether a key can still log in.
func keyWorks(t *testing.T, srv *contracttest.Hub, key ed25519.PrivateKey) bool {
	t.Helper()
	ts := auth.NewTokenSource(contracttest.NewClients(srv.URL, srv.HTTPClient(t)).Identity, key)
	_, err := ts.ParticipantID(ctx10(t))
	return err == nil
}

// An admin creates a user with a form, gives the user a second key with the same form and revokes
// a key after a confirmation. A key works until it is revoked.
func TestPortalRegistersAndRevokesKeys(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	b := signIn(t, srv)

	_, body := fetch(t, b, srv.URL+"/portal/admin/keys/new")
	csrf := formField(t, parseDoc(t, body), "csrf")
	register := func(key ed25519.PrivateKey, name string) (*http.Response, string) {
		return post(t, b, srv.URL+"/portal/admin/keys/register", url.Values{"csrf": {csrf}, "participant_id": {"newbot"},
			"display_name": {name}, "kind": {"agent"}, "public_key": {auth.FormatPublicKey(auth.PublicKey(key))}})
	}
	key1, _ := auth.NewKey()
	res, body := register(key1, "New Bot")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register: status %d\n%s", res.StatusCode, body)
	}
	doc := parseDoc(t, body)
	if len(findAll(doc, withAttr("data-notice", ""))) != 1 || !strings.Contains(text(doc), "The key is registered.") {
		t.Errorf("no notice after the registration: %q", text(doc))
	}
	id1 := auth.KeyID(auth.PublicKey(key1))
	row := findAll(doc, withAttr("data-key", id1))
	if len(row) != 1 || !strings.Contains(text(row[0]), "New Bot") || !strings.Contains(text(row[0]), "agent") || !strings.Contains(text(row[0]), "active") {
		t.Fatalf("row of the new key: %q", text(doc))
	}
	if !keyWorks(t, srv, key1) {
		t.Fatalf("the registered key does not work")
	}
	if _, body := fetch(t, b, srv.URL+"/portal/admin/keys"); strings.Contains(body, "The key is registered.") {
		t.Errorf("the notice stays")
	}
	// The same form gives the known user another key.
	key2, _ := auth.NewKey()
	if res, body := register(key2, "New Bot"); res.StatusCode != http.StatusOK || len(findAll(parseDoc(t, body), withAttr("data-participant", "newbot"))) != 2 {
		t.Fatalf("second key: status %d\n%s", res.StatusCode, text(parseDoc(t, body)))
	}
	id2 := auth.KeyID(auth.PublicKey(key2))

	// Revoke: the link opens a page that asks, a post revokes.
	revokeHref := "/portal/admin/keys/revoke?key_id=" + url.QueryEscape(id1)
	_, body = fetch(t, b, srv.URL+"/portal/admin/keys")
	found := false
	for _, a := range findAll(parseDoc(t, body), byTag("a")) {
		h, _ := attr(a, "href")
		if u, err := url.Parse(h); err == nil && u.Path == "/portal/admin/keys/revoke" && u.Query().Get("key_id") == id1 {
			found = true
			revokeHref = h
		}
	}
	if !found {
		t.Fatalf("no link to revoke the first key")
	}
	res, body = fetch(t, b, srv.URL+revokeHref)
	doc = parseDoc(t, body)
	if res.StatusCode != http.StatusOK || len(findAll(doc, withAttr("data-confirm", "revoke"))) != 1 || !strings.Contains(text(doc), "New Bot") {
		t.Fatalf("confirmation: status %d\n%s", res.StatusCode, text(doc))
	}
	if !keyWorks(t, srv, key1) {
		t.Fatalf("the key stopped before the confirmation")
	}
	res, body = post(t, b, srv.URL+"/portal/admin/keys/revoke", url.Values{"csrf": {formField(t, doc, "csrf")}, "key_id": {id1}})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "The key is revoked.") {
		t.Fatalf("revoke: status %d\n%s", res.StatusCode, body)
	}
	if r := findAll(parseDoc(t, body), withAttr("data-key", id1)); len(r) != 1 || !strings.Contains(text(r[0]), "revoked") || strings.Contains(text(r[0]), "Revoke ") {
		t.Errorf("row after the revocation: %q", text(parseDoc(t, body)))
	}
	if keyWorks(t, srv, key1) || !keyWorks(t, srv, key2) {
		t.Errorf("after the revocation: first key works %v, second key works %v", keyWorks(t, srv, key1), keyWorks(t, srv, key2))
	}
	// The last key of a user cannot be revoked.
	res, body = post(t, b, srv.URL+"/portal/admin/keys/revoke", url.Values{"csrf": {csrf}, "key_id": {id2}})
	if res.StatusCode != http.StatusConflict || !keyWorks(t, srv, key2) {
		t.Errorf("revoke of the last key: status %d, key works %v\n%s", res.StatusCode, keyWorks(t, srv, key2), text(parseDoc(t, body)))
	}
}

// The forms refuse posts that do not come from a page of the view, bad input and keys that cannot
// be changed. The form keeps what the admin typed.
func TestPortalFormsRefuse(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	b := signIn(t, srv)
	_, body := fetch(t, b, srv.URL+"/portal/admin/keys/new")
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
	reg := srv.URL + "/portal/admin/keys/register"

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
	other := signIn(t, srv)
	if res, _ := post(t, other, reg, form(nil)); res.StatusCode != http.StatusForbidden {
		t.Errorf("token of another session: status %d", res.StatusCode)
	}
	if res, _ := fetch(t, b, srv.URL+"/portal/admin/keys/register"); res.Request.URL.Path != "/portal/admin/keys" {
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
	// A key of another user cannot be registered again.
	adaPub := auth.FormatPublicKey(auth.PublicKey(ada.Key))
	if res, _ := post(t, b, reg, form(map[string]string{"public_key": adaPub})); res.StatusCode != http.StatusConflict {
		t.Errorf("a key of another user: status %d", res.StatusCode)
	}

	// Revoke: an unknown key, a wrong token, and the only key of a user.
	adaKey := ada.KeyID
	rev := srv.URL + "/portal/admin/keys/revoke"
	if res, _ := fetch(t, b, srv.URL+"/portal/admin/keys/revoke?key_id=nothing"); res.StatusCode != http.StatusConflict {
		t.Errorf("confirm for an unknown key: status %d", res.StatusCode)
	}
	if res, _ := post(t, b, rev, url.Values{"csrf": {"wrong"}, "key_id": {adaKey}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("revoke with a wrong token: status %d", res.StatusCode)
	}
	if res, _ := post(t, b, rev, url.Values{"csrf": {csrf}, "key_id": {"nothing"}}); res.StatusCode != http.StatusConflict {
		t.Errorf("revoke of an unknown key: status %d", res.StatusCode)
	}
	if res, _ := post(t, b, rev, url.Values{"csrf": {csrf}, "key_id": {adaKey}}); res.StatusCode != http.StatusConflict {
		t.Errorf("revoke of the only key of a user: status %d", res.StatusCode)
	}
}

// openPortal opens a portal link of actor a for tenant (empty: the start page) and returns the
// browser and the page it ends on.
func openPortal(t *testing.T, srv *contracttest.Hub, a *contracttest.Actor, tenant string) (*http.Client, *http.Response, *html.Node) {
	t.Helper()
	link, err := a.Identity.CreatePortalLink(ctx10(t), &identityv1.CreatePortalLinkRequest{CommandId: coreapi.NewCommandID(), TenantId: tenant})
	if err != nil {
		t.Fatalf("portal link of %s for %q: %v", a.ParticipantID, tenant, err)
	}
	b := browser(t, srv, true)
	res, body := fetch(t, b, link.GetUrl())
	return b, res, parseDoc(t, body)
}

// The frame of the portal shows what the key may see: a member opens its tenant and sees its role,
// a tenant admin also sees the members, and a tenant of which the user is no member does not
// exist. A link may only name a tenant of the user.
func TestPortalTenants(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human) // the tenant admin of the default tenant
	bob := srv.Participant(t, "bob", "Bob", human)
	tenantPage := "/portal/t/" + contracttest.DefaultTenant + "/"
	membersPage := "/portal/t/" + contracttest.DefaultTenant + "/members"

	// Bob took part in one session; Ada, the tenant admin, in none.
	weekly := "weekly-" + coreapi.NewCommandID()
	joinAs(t, bob, weekly)

	// A member: the link of the tenant opens its page with its sessions; no members, no
	// administration.
	b, res, doc := openPortal(t, srv, bob, contracttest.DefaultTenant)
	rows := findAll(doc, withAttr("data-session", weekly))
	if len(rows) != 1 || len(findAll(rows[0], withAttr("data-participant", "bob"))) != 1 || !strings.Contains(text(rows[0]), "running") {
		t.Errorf("sessions of bob: %q", text(doc))
	}
	if res.StatusCode != http.StatusOK || res.Request.URL.Path != tenantPage {
		t.Fatalf("member: ends on %s with status %d", res.Request.URL.Path, res.StatusCode)
	}
	if roles := findAll(doc, withAttr("data-role", "")); len(roles) != 1 || text(roles[0]) != "member" {
		t.Errorf("role of bob: %q", text(doc))
	}
	if len(findAll(doc, withAttr("data-me", "bob"))) != 1 || len(findAll(doc, withAttr("data-nav", tenantPage))) == 0 ||
		len(findAll(doc, withAttr("data-nav", membersPage))) != 0 || len(findAll(doc, withAttr("data-nav", "/portal/admin/keys"))) != 0 {
		t.Errorf("navigation of a member: %q", text(doc))
	}
	if r, _ := fetch(t, b, srv.URL+membersPage); r.StatusCode != http.StatusForbidden {
		t.Errorf("members page for a member: status %d", r.StatusCode)
	}
	if r, _ := fetch(t, b, srv.URL+"/portal/t/no-such-tenant/"); r.StatusCode != http.StatusNotFound {
		t.Errorf("a foreign tenant: status %d", r.StatusCode)
	}
	// One tenant and no administration: the start page leads to the tenant.
	if r, _ := fetch(t, b, srv.URL+"/portal/"); r.Request.URL.Path != tenantPage {
		t.Errorf("start page of a member with one tenant ends on %s", r.Request.URL.Path)
	}

	// The tenant admin sees the members, and every session of the tenant.
	b, _, doc = openPortal(t, srv, ada, contracttest.DefaultTenant)
	if len(findAll(doc, withAttr("data-session", weekly))) != 1 {
		t.Errorf("sessions of the tenant admin: %q", text(doc))
	}
	if len(findAll(doc, withAttr("data-nav", membersPage))) != 1 {
		t.Fatalf("navigation of the tenant admin: %q", text(doc))
	}
	r, body := fetch(t, b, srv.URL+membersPage)
	doc = parseDoc(t, body)
	ma, mb := findAll(doc, withAttr("data-member", "ada")), findAll(doc, withAttr("data-member", "bob"))
	if r.StatusCode != http.StatusOK || len(ma) != 1 || !strings.Contains(text(ma[0]), "tenant admin") || len(mb) != 1 || !strings.Contains(text(mb[0]), "member") {
		t.Errorf("members page: status %d\n%s", r.StatusCode, text(doc))
	}

	// A link names only a tenant of the user.
	_, err := bob.Identity.CreatePortalLink(ctx10(t), &identityv1.CreatePortalLinkRequest{CommandId: coreapi.NewCommandID(), TenantId: "no-such-tenant"})
	wantCode(t, "portal link for a foreign tenant", err, connect.CodeNotFound)

	// The system admin is no member: the start page lists no tenant, and the tenant does not exist.
	b, res, doc = openPortal(t, srv, srv.Admin(t), "")
	if res.Request.URL.Path != "/portal/" || len(findAll(doc, withAttr("data-empty", "tenants"))) != 1 {
		t.Errorf("start page of the system admin: %s\n%s", res.Request.URL.Path, text(doc))
	}
	if r, _ := fetch(t, b, srv.URL+tenantPage); r.StatusCode != http.StatusNotFound {
		t.Errorf("tenant page for the system admin: status %d", r.StatusCode)
	}
}

// TestPortalSessionView: a participant opens a session from the list of its tenant and reads it:
// the header, the stored minutes, the decisions, notes and tasks of the session, and the
// transcript without the sentence that went off the record. The tenant admin reads it too; a
// member who never joined it gets 404. A running session shows its running minutes. A session ID
// with a slash works.
func TestPortalSessionView(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human) // tenant admin
	bob := srv.Participant(t, "bob", "Bob", human)
	dora := srv.Participant(t, "dora", "Dora", human)
	ctx := ctx10(t)
	sid := "budget/" + coreapi.NewCommandID()
	joinAs(t, bob, sid)
	say := func(text string) uint32 {
		res, err := bob.Commands.SubmitUtterance(ctx, &sessioncommandsv1.SubmitUtteranceRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
			ParticipantId: "bob", Text: text, Modality: sessionstreamv1.Modality_MODALITY_TEXT})
		if err != nil {
			t.Fatal(err)
		}
		return res.GetResult().GetEvent().GetId()
	}
	say("We start with the budget.")
	secret := say("The secret number is 42.")
	if _, err := bob.Commands.MarkOffTheRecord(ctx, &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: "bob", UtteranceEventId: secret}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Commands.UpdateMinutes(ctx, &sessioncommandsv1.UpdateMinutesRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: "bob", Text: "# Budget\n\n- running minutes"}); err != nil {
		t.Fatal(err)
	}

	// Running: the running minutes, and the note that the session runs.
	b, _, doc := openPortal(t, srv, bob, contracttest.DefaultTenant)
	href := ""
	for _, a := range findAll(doc, byTag("a")) {
		if h, _ := attr(a, "href"); strings.Contains(h, "/s/") {
			href = h
		}
	}
	if href != "/portal/t/"+contracttest.DefaultTenant+"/s/"+url.PathEscape(sid) {
		t.Fatalf("link of the session in the list: %q", href)
	}
	res, body := fetch(t, b, srv.URL+href)
	doc = parseDoc(t, body)
	if res.StatusCode != http.StatusOK || len(findAll(doc, withAttr("data-running", ""))) != 1 || len(findAll(doc, withAttr("data-running-minutes", ""))) != 1 {
		t.Fatalf("running session: status %d\n%s", res.StatusCode, text(doc))
	}

	// Ended, with records.
	if _, err := bob.Decision.RecordDecision(ctx, &decisionv1.RecordDecisionRequest{CommandId: coreapi.NewCommandID(), Text: "We cut **travel**.",
		AuthorParticipantId: "bob", SessionId: sid}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Decision.RecordDecision(ctx, &decisionv1.RecordDecisionRequest{CommandId: coreapi.NewCommandID(), Text: "Another session.",
		AuthorParticipantId: "bob", SessionId: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Note.AddNote(ctx, &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: "Ask the bank.", AuthorParticipantId: "bob",
		SessionId: sid}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Task.CreateTask(ctx, &taskv1.CreateTaskRequest{CommandId: coreapi.NewCommandID(), Title: "Send the numbers", AuthorParticipantId: "bob",
		AssigneeParticipantId: "ada", SessionId: sid}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Commands.UpdateMinutes(ctx, &sessioncommandsv1.UpdateMinutesRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: "bob", Text: "# Budget\n\nWe cut travel."}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Commands.EndSession(ctx, &sessioncommandsv1.EndSessionRequest{SessionId: sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: "bob"}); err != nil {
		t.Fatal(err)
	}
	res, body = fetch(t, b, srv.URL+href)
	doc = parseDoc(t, body)
	all := text(doc)
	if res.StatusCode != http.StatusOK || len(findAll(doc, withAttr("data-running", ""))) != 0 {
		t.Fatalf("ended session: status %d\n%s", res.StatusCode, all)
	}
	if m := findAll(doc, withAttr("data-minutes", "")); len(m) != 1 || !strings.Contains(text(m[0]), "We cut travel.") ||
		len(findAll(doc, withAttr("data-running-minutes", ""))) != 0 {
		t.Errorf("stored minutes: %q", all)
	}
	if d := findAll(doc, withAttr("data-decision", "")); len(d) != 1 || !strings.Contains(text(d[0]), "We cut travel.") ||
		len(findAll(d[0], byTag("strong"))) != 1 {
		t.Errorf("decisions of the session: %d\n%s", len(d), all)
	}
	if len(findAll(doc, withAttr("data-note", ""))) != 1 || !strings.Contains(all, "Ask the bank.") || !strings.Contains(all, "Send the numbers") {
		t.Errorf("notes and tasks: %q", all)
	}
	lines := findAll(doc, withAttr("data-utterance", ""))
	if len(lines) != 1 || !strings.Contains(text(lines[0]), "We start with the budget.") || strings.Contains(body, "secret number") {
		t.Errorf("transcript: %d lines\n%s", len(lines), all)
	}

	// The tenant admin reads it; a member who never joined it does not.
	b, _, _ = openPortal(t, srv, ada, contracttest.DefaultTenant)
	if r, _ := fetch(t, b, srv.URL+href); r.StatusCode != http.StatusOK {
		t.Errorf("the tenant admin: status %d", r.StatusCode)
	}
	b, _, _ = openPortal(t, srv, dora, contracttest.DefaultTenant)
	if r, body := fetch(t, b, srv.URL+href); r.StatusCode != http.StatusNotFound || strings.Contains(body, "budget") {
		t.Errorf("a member who never joined: status %d", r.StatusCode)
	}
}

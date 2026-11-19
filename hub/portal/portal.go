package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect/v2"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	"github.com/mrsimpson/agentic-office/hub/identity/userapi/userv1connect"
	"github.com/mrsimpson/agentic-office/hub/portal/web"
	tenantv1 "github.com/mrsimpson/agentic-office/hub/tenant/api"
	"github.com/mrsimpson/agentic-office/hub/tenant/api/tenantv1connect"
)

// Options configure a Handler.
type Options struct {
	// Hub is a transport to the server of the hub behind the token check, for example
	// connectinprocess.New on the server of office-hub. The portal calls it with the token of the
	// user, so it has exactly the rights of the key of the user.
	Hub connect.Transport
}

// Handler serves the portal (if-portal-web).
type Handler struct {
	opts Options
}

// New returns a Handler.
func New(opts Options) *Handler { return &Handler{opts: opts} }

// keyView is a key as the page shows it, with its user.
type keyView struct {
	ParticipantId string
	Name          string
	KindName      string
	KeyId         string
	Revoked       bool
	Removed       bool // the user is removed
	Admin         bool // the user is a system admin
}

// keyRows returns one row for each key of each user, ordered by user and key.
func keyRows(users []*userv1.User) []keyView {
	var rows []keyView
	for _, u := range users {
		name := u.GetDisplayName()
		if name == "" {
			name = u.GetUserId()
		}
		kind := "human"
		if u.GetKind() == identityv1.ActorKind_ACTOR_KIND_AGENT {
			kind = "agent"
		}
		for _, k := range u.GetKeys() {
			rows = append(rows, keyView{ParticipantId: u.GetUserId(), Name: name, KindName: kind, KeyId: k.GetKeyId(),
				Revoked: k.GetRevoked(), Removed: u.GetValidUntil() != nil, Admin: u.GetSystemAdmin()})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].ParticipantId != rows[j].ParticipantId {
			return rows[i].ParticipantId < rows[j].ParticipantId
		}
		return rows[i].KeyId < rows[j].KeyId
	})
	return rows
}

// ServeHTTP serves the pages of the portal.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", web.CSP)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	path := r.URL.Path
	if path != web.HomePath {
		path = strings.TrimSuffix(path, "/")
	}
	post := path == web.RegisterPath || path == web.RevokePath
	if r.Method != http.MethodGet && !(post && r.Method == http.MethodPost) {
		if path == web.RegisterPath {
			w.Header().Set("Allow", http.MethodPost)
		} else if path == web.RevokePath {
			w.Header().Set("Allow", "GET, POST")
		} else {
			w.Header().Set("Allow", http.MethodGet)
		}
		h.fail(w, http.StatusMethodNotAllowed, "Not allowed", "This address does not answer this method.")
		return
	}
	if r.Method == http.MethodGet && path == web.RegisterPath {
		http.Redirect(w, r, web.KeysPath, http.StatusSeeOther)
		return
	}
	if rest, ok := strings.CutPrefix(path, web.TenantPrefix); ok {
		tenant, page, _ := strings.Cut(rest, "/")
		h.tenant(w, r, tenant, page)
		return
	}
	switch path {
	case web.HomePath:
		h.home(w, r)
	case web.LoginPath:
		h.login(w, r)
	case "/portal/admin":
		http.Redirect(w, r, web.KeysPath, http.StatusSeeOther)
	case web.KeysPath:
		h.keys(w, r)
	case web.NewKeyPath:
		h.newKey(w, r)
	case web.RegisterPath:
		h.register(w, r)
	case web.RevokePath:
		if r.Method == http.MethodPost {
			h.revoke(w, r)
		} else {
			h.confirmRevoke(w, r)
		}
	case web.TenantsPath:
		h.tenants(w, r)
	case web.AdminsPath:
		h.admins(w, r)
	default:
		h.fail(w, http.StatusNotFound, "Not found", "There is no such page.")
	}
}

func (h *Handler) fail(w http.ResponseWriter, status int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = web.ErrorPage.Execute(w, map[string]any{"Title": title, "Message": msg})
}

const askForLink = "Ask for a new link with: office portal link"

// login swaps the code of a portal link for a session. A code works once. It opens the tenant that
// the link names, else the start page.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		h.fail(w, http.StatusUnauthorized, "Portal link not valid", "The link has no code. "+askForLink)
		return
	}
	c := identityv1connect.NewIdentityServiceClient(connect.NewClient(h.opts.Hub))
	res, err := c.ExchangePortalLink(r.Context(), &identityv1.ExchangePortalLinkRequest{Code: code})
	if err != nil {
		h.fail(w, http.StatusUnauthorized, "Portal link not valid", "The link is unknown, was used before or has expired. "+askForLink)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: web.CookieName, Value: res.GetAccessToken(), Path: web.PathPrefix,
		Expires: res.GetExpiresAt().AsTime(), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	next := web.HomePath
	if t := res.GetTenantId(); t != "" {
		next = web.TenantPath(t)
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// backend is what the pages ask, with the token of the session: the auth API, the user API and
// the tenant API.
type backend struct {
	me       *user // the user of the session
	identity identityv1connect.IdentityServiceClient
	users    userv1connect.UserServiceClient
	tenants  tenantv1connect.TenantServiceClient
}

func (h *Handler) client(w http.ResponseWriter, r *http.Request) (*backend, bool) {
	ck, err := r.Cookie(web.CookieName)
	if err != nil || ck.Value == "" {
		h.fail(w, http.StatusUnauthorized, "Not signed in", "Open a portal link to sign in. "+askForLink)
		return nil, false
	}
	cc := connect.NewClient(h.opts.Hub, auth.StaticToken(ck.Value))
	b := &backend{identity: identityv1connect.NewIdentityServiceClient(cc), users: userv1connect.NewUserServiceClient(cc),
		tenants: tenantv1connect.NewTenantServiceClient(cc)}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	who, err := b.identity.WhoAmI(ctx, &identityv1.WhoAmIRequest{})
	if err != nil {
		h.denied(w, err)
		return nil, false
	}
	b.me = userOf(who)
	return b, true
}

// denied answers a call that the identity part refused, and clears the cookie of an old session.
func (h *Handler) denied(w http.ResponseWriter, err error) {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated:
		http.SetCookie(w, &http.Cookie{Name: web.CookieName, Path: web.PathPrefix, MaxAge: -1, HttpOnly: true})
		h.fail(w, http.StatusUnauthorized, "Session ended", "Your session in the portal has ended. "+askForLink)
	case connect.CodePermissionDenied:
		h.fail(w, http.StatusForbidden, "Not allowed", "Your key may not see this page.")
	case connect.CodeNotFound:
		h.fail(w, http.StatusNotFound, "Not found", "There is no such page for you.")
	default:
		h.fail(w, http.StatusServiceUnavailable, "Not available", "The hub could not answer. Try again.")
	}
}

func (h *Handler) keys(w http.ResponseWriter, r *http.Request) {
	c, ok := h.client(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	res, err := c.users.ListUsers(ctx, &userv1.ListUsersRequest{})
	if err != nil {
		h.denied(w, err)
		return
	}
	keys := keyRows(res.GetUsers())
	notice := map[string]string{"registered": "The key is registered.", "revoked": "The key is revoked."}[r.URL.Query().Get("done")]
	h.page(w, c.me, web.KeysPath, "", map[string]any{"Title": "Users and keys", "Section": "Administration", "Keys": keys, "Notice": notice})
}

// tenantView is a tenant as the page shows it.
type tenantView struct {
	TenantId, Name string
	Removed        bool
}

func (h *Handler) tenants(w http.ResponseWriter, r *http.Request) {
	c, ok := h.client(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	res, err := c.tenants.ListTenants(ctx, &tenantv1.ListTenantsRequest{})
	if err != nil {
		h.denied(w, err)
		return
	}
	var rows []tenantView
	for _, t := range res.GetTenants() {
		rows = append(rows, tenantView{TenantId: t.GetTenantId(), Name: t.GetDisplayName(), Removed: t.GetValidUntil() != nil})
	}
	h.page(w, c.me, web.TenantsPath, "", map[string]any{"Title": "Tenants", "Section": "Administration", "Mode": "tenants", "Tenants": rows})
}

func (h *Handler) admins(w http.ResponseWriter, r *http.Request) {
	c, ok := h.client(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	res, err := c.users.ListUsers(ctx, &userv1.ListUsersRequest{})
	if err != nil {
		h.denied(w, err)
		return
	}
	var admins []*userv1.User
	for _, u := range res.GetUsers() {
		if u.GetSystemAdmin() {
			admins = append(admins, u)
		}
	}
	h.page(w, c.me, web.AdminsPath, "", map[string]any{"Title": "System admins", "Section": "Administration", "Mode": "admins", "Keys": keyRows(admins)})
}

// page renders a page with the frame: the user and the navigation by what the key may see.
func (h *Handler) page(w http.ResponseWriter, u *user, active, tenant string, data map[string]any) {
	render(w, http.StatusOK, u, active, tenant, data)
}

func render(w http.ResponseWriter, status int, u *user, active, tenant string, data map[string]any) {
	data["Me"] = u
	data["Nav"] = u.nav(active, tenant)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = web.Page.Execute(w, data)
}

// csrf is the token that a form carries. It is derived from the access token of the session,
// which scripts and other sites cannot read.
func csrf(sessionToken string) string {
	h := sha256.Sum256([]byte("office-portal-csrf\x00" + sessionToken))
	return hex.EncodeToString(h[:])
}

func commandID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// formValues are the fields of the form that registers a key.
type formValues struct {
	ParticipantID, DisplayName, Kind, PublicKey string
}

func (h *Handler) registerPage(w http.ResponseWriter, me *user, status int, token string, form formValues, errMsg string) {
	render(w, status, me, web.KeysPath, "", map[string]any{"Title": "Register a key", "Section": "Administration", "Mode": "register",
		"CSRF": csrf(token), "Form": form, "Error": errMsg})
}

func (h *Handler) newKey(w http.ResponseWriter, r *http.Request) {
	c, ok := h.client(w, r)
	if !ok {
		return
	}
	if !c.me.SystemAdmin {
		h.fail(w, http.StatusForbidden, "Not allowed", "Your key may not see this page.")
		return
	}
	ck, _ := r.Cookie(web.CookieName)
	h.registerPage(w, c.me, http.StatusOK, ck.Value, formValues{Kind: "human"}, "")
}

// checkForm checks that a form post comes from a page of this view: the origin, if the browser
// sends one, is this server, and the form carries the token of the session.
func (h *Handler) checkForm(w http.ResponseWriter, r *http.Request) (*backend, string, bool) {
	c, ok := h.client(w, r)
	if !ok {
		return nil, "", false
	}
	ck, _ := r.Cookie(web.CookieName)
	if o := r.Header.Get("Origin"); o != "" && o != "https://"+r.Host && o != "http://"+r.Host {
		h.fail(w, http.StatusForbidden, "Form not valid", "The form does not come from this page.")
		return nil, "", false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil || r.PostForm.Get("csrf") != csrf(ck.Value) {
		h.fail(w, http.StatusForbidden, "Form not valid", "The form is old or does not come from this page. Go back, reload it and try again.")
		return nil, "", false
	}
	return c, ck.Value, true
}

// failed answers a call of a form that the identity part refused. A refusal that the admin can fix
// shows the message on the form page; the other refusals end the session or show an error page.
func (h *Handler) failed(w http.ResponseWriter, err error, show func(status int, msg string)) {
	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument:
		show(http.StatusBadRequest, connect.CodeOf(err).String()+": "+messageOf(err))
	case connect.CodeFailedPrecondition, connect.CodeAlreadyExists, connect.CodeNotFound:
		show(http.StatusConflict, messageOf(err))
	default:
		h.denied(w, err)
	}
}

func messageOf(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Message()
	}
	return err.Error()
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	c, token, ok := h.checkForm(w, r)
	if !ok {
		return
	}
	form := formValues{ParticipantID: strings.TrimSpace(r.PostForm.Get("participant_id")), DisplayName: strings.TrimSpace(r.PostForm.Get("display_name")),
		Kind: r.PostForm.Get("kind"), PublicKey: strings.TrimSpace(r.PostForm.Get("public_key"))}
	show := func(status int, msg string) { h.registerPage(w, c.me, status, token, form, msg) }
	pub, err := auth.ParsePublicKey(form.PublicKey)
	if err != nil {
		show(http.StatusBadRequest, "The public key is not a base64 Ed25519 public key. Use the one that office key show prints.")
		return
	}
	kind := identityv1.ActorKind_ACTOR_KIND_HUMAN
	if form.Kind == "agent" {
		kind = identityv1.ActorKind_ACTOR_KIND_AGENT
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// A user that exists gets another key; a new user is created with this key.
	list, err := c.users.ListUsers(ctx, &userv1.ListUsersRequest{})
	if err != nil {
		h.failed(w, err, show)
		return
	}
	exists := false
	for _, u := range list.GetUsers() {
		exists = exists || (u.GetUserId() == form.ParticipantID && u.GetValidUntil() == nil)
	}
	if exists {
		_, err = c.users.AddKey(ctx, &userv1.AddKeyRequest{CommandId: commandID(), UserId: form.ParticipantID, PublicKey: pub})
	} else {
		_, err = c.users.CreateUser(ctx, &userv1.CreateUserRequest{CommandId: commandID(), UserId: form.ParticipantID,
			DisplayName: form.DisplayName, Kind: kind, PublicKey: pub})
	}
	if err != nil {
		h.failed(w, err, show)
		return
	}
	http.Redirect(w, r, web.KeysPath+"?done=registered", http.StatusSeeOther)
}

// findKey returns the key with this ID, as the list of users shows it.
func findKey(ctx context.Context, c *backend, id string) (keyView, error) {
	res, err := c.users.ListUsers(ctx, &userv1.ListUsersRequest{})
	if err != nil {
		return keyView{}, err
	}
	for _, k := range keyRows(res.GetUsers()) {
		if k.KeyId == id {
			return k, nil
		}
	}
	return keyView{}, connect.NewError(connect.CodeNotFound, "there is no such key")
}

func (h *Handler) confirmRevoke(w http.ResponseWriter, r *http.Request) {
	c, ok := h.client(w, r)
	if !ok {
		return
	}
	ck, _ := r.Cookie(web.CookieName)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	k, err := findKey(ctx, c, r.URL.Query().Get("key_id"))
	if err != nil {
		h.failed(w, err, func(status int, msg string) { h.fail(w, status, "Key not found", msg) })
		return
	}
	if k.Revoked || k.Removed {
		h.fail(w, http.StatusConflict, "Cannot revoke", "This key is already revoked, or its user is removed.")
		return
	}
	h.page(w, c.me, web.KeysPath, "", map[string]any{"Title": "Revoke a key", "Section": "Administration", "Mode": "revoke", "CSRF": csrf(ck.Value), "Key": k})
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	c, _, ok := h.checkForm(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := c.users.RevokeKey(ctx, &userv1.RevokeKeyRequest{CommandId: commandID(), KeyId: r.PostForm.Get("key_id")}); err != nil {
		h.failed(w, err, func(status int, msg string) { h.fail(w, status, "Cannot revoke", msg) })
		return
	}
	http.Redirect(w, r, web.KeysPath+"?done=revoked", http.StatusSeeOther)
}

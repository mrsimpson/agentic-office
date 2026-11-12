package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/hub/admin/web"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
)

// Options configure a Handler.
type Options struct {
	// Identity is a transport to the identity part behind the token check, for example
	// connectinprocess.New on the server of office-hub. The admin view calls it with the token of
	// the admin, so it has exactly the rights of the admin key.
	Identity connect.Transport
}

// Handler serves the admin view (if-admin-web).
type Handler struct {
	opts Options
}

// New returns a Handler.
func New(opts Options) *Handler { return &Handler{opts: opts} }

// keyView is a key as the page shows it.
type keyView struct {
	*identityv1.KeyInfo
}

// Name is the display name, or the participant ID if there is none.
func (k keyView) Name() string {
	if k.GetDisplayName() != "" {
		return k.GetDisplayName()
	}
	return k.GetParticipantId()
}

// KindName is "human" or "agent".
func (k keyView) KindName() string {
	if k.GetKind() == identityv1.ActorKind_ACTOR_KIND_AGENT {
		return "agent"
	}
	return "human"
}

// ServeHTTP serves the pages of the admin view.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", web.CSP)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	path := strings.TrimSuffix(r.URL.Path, "/")
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
	switch path {
	case "/admin":
		http.Redirect(w, r, web.KeysPath, http.StatusSeeOther)
	case web.LoginPath:
		h.login(w, r)
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
	case "/admin/tenants":
		h.placeholder(w, r, "Tenants", "Tenants arrive with multi-tenancy. Each tenant will have its own repository and its own admins.")
	case "/admin/admins":
		h.placeholder(w, r, "System admins", "System admins move from the admin file into the hub repository with multi-tenancy.")
	default:
		h.fail(w, http.StatusNotFound, "Not found", "There is no such page.")
	}
}

func (h *Handler) fail(w http.ResponseWriter, status int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = web.ErrorPage.Execute(w, map[string]any{"Title": title, "Message": msg})
}

const askForLink = "Ask for a new link with: office admin link"

// login swaps the code of an admin link for a session. A code works once.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		h.fail(w, http.StatusUnauthorized, "Admin link not valid", "The link has no code. "+askForLink)
		return
	}
	c := identityv1connect.NewIdentityServiceClient(connect.NewClient(h.opts.Identity))
	res, err := c.ExchangeAdminLink(r.Context(), &identityv1.ExchangeAdminLinkRequest{Code: code})
	if err != nil {
		h.fail(w, http.StatusUnauthorized, "Admin link not valid", "The link is unknown, was used before or has expired. "+askForLink)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: web.CookieName, Value: res.GetAccessToken(), Path: web.PathPrefix,
		Expires: res.GetExpiresAt().AsTime(), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, web.KeysPath, http.StatusSeeOther)
}

// client returns an identity client with the token of the session, or answers with an error page.
func (h *Handler) client(w http.ResponseWriter, r *http.Request) (identityv1connect.IdentityServiceClient, bool) {
	ck, err := r.Cookie(web.CookieName)
	if err != nil || ck.Value == "" {
		h.fail(w, http.StatusUnauthorized, "Not signed in", "Open an admin link to sign in. "+askForLink)
		return nil, false
	}
	return identityv1connect.NewIdentityServiceClient(connect.NewClient(h.opts.Identity, auth.StaticToken(ck.Value))), true
}

// denied answers a call that the identity part refused, and clears the cookie of an old session.
func (h *Handler) denied(w http.ResponseWriter, err error) {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated:
		http.SetCookie(w, &http.Cookie{Name: web.CookieName, Path: web.PathPrefix, MaxAge: -1, HttpOnly: true})
		h.fail(w, http.StatusUnauthorized, "Session ended", "Your admin session has ended. "+askForLink)
	case connect.CodePermissionDenied:
		h.fail(w, http.StatusForbidden, "Not allowed", "Only admins may use this view.")
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
	res, err := c.ListKeys(ctx, &identityv1.ListKeysRequest{})
	if err != nil {
		h.denied(w, err)
		return
	}
	keys := make([]keyView, 0, len(res.GetKeys()))
	for _, k := range res.GetKeys() {
		keys = append(keys, keyView{k})
	}
	notice := map[string]string{"registered": "The key is registered.", "revoked": "The key is revoked."}[r.URL.Query().Get("done")]
	h.page(w, map[string]any{"Title": "Users and keys", "Section": "Hub", "Active": web.KeysPath, "Keys": keys, "Notice": notice})
}

func (h *Handler) placeholder(w http.ResponseWriter, r *http.Request, title, text string) {
	if _, ok := h.client(w, r); !ok {
		return
	}
	h.page(w, map[string]any{"Title": title, "Mode": "placeholder", "Section": "Tenants", "Active": "/admin/" + strings.ToLower(strings.Fields(title)[len(strings.Fields(title))-1]), "Placeholder": text})
}

func (h *Handler) page(w http.ResponseWriter, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.Page.Execute(w, data)
}

// csrf is the token that a form carries. It is derived from the access token of the session,
// which scripts and other sites cannot read.
func csrf(sessionToken string) string {
	h := sha256.Sum256([]byte("office-admin-csrf\x00" + sessionToken))
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

func (h *Handler) registerPage(w http.ResponseWriter, status int, token string, form formValues, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = web.Page.Execute(w, map[string]any{"Title": "Register a key", "Section": "Hub", "Active": web.KeysPath, "Mode": "register",
		"CSRF": csrf(token), "Form": form, "Error": errMsg})
}

func (h *Handler) newKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.client(w, r); !ok {
		return
	}
	ck, _ := r.Cookie(web.CookieName)
	h.registerPage(w, http.StatusOK, ck.Value, formValues{Kind: "human"}, "")
}

// checkForm checks that a form post comes from a page of this view: the origin, if the browser
// sends one, is this server, and the form carries the token of the session.
func (h *Handler) checkForm(w http.ResponseWriter, r *http.Request) (identityv1connect.IdentityServiceClient, string, bool) {
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
	show := func(status int, msg string) { h.registerPage(w, status, token, form, msg) }
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
	_, err = c.RegisterKey(ctx, &identityv1.RegisterKeyRequest{CommandId: commandID(), ParticipantId: form.ParticipantID,
		DisplayName: form.DisplayName, Kind: kind, PublicKey: pub})
	if err != nil {
		h.failed(w, err, show)
		return
	}
	http.Redirect(w, r, web.KeysPath+"?done=registered", http.StatusSeeOther)
}

// findKey returns the key with this ID, as ListKeys shows it.
func findKey(ctx context.Context, c identityv1connect.IdentityServiceClient, id string) (keyView, error) {
	res, err := c.ListKeys(ctx, &identityv1.ListKeysRequest{})
	if err != nil {
		return keyView{}, err
	}
	for _, k := range res.GetKeys() {
		if k.GetKeyId() == id {
			return keyView{k}, nil
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
	if k.GetRevoked() || k.GetAdmin() {
		h.fail(w, http.StatusConflict, "Cannot revoke", "This key is already revoked, or it is a key of the admin file: remove it there and restart the server.")
		return
	}
	h.page(w, map[string]any{"Title": "Revoke a key", "Section": "Hub", "Active": web.KeysPath, "Mode": "revoke", "CSRF": csrf(ck.Value), "Key": k})
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	c, _, ok := h.checkForm(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, err := c.RevokeKey(ctx, &identityv1.RevokeKeyRequest{CommandId: commandID(), KeyId: r.PostForm.Get("key_id")}); err != nil {
		h.failed(w, err, func(status int, msg string) { h.fail(w, status, "Cannot revoke", msg) })
		return
	}
	http.Redirect(w, r, web.KeysPath+"?done=revoked", http.StatusSeeOther)
}

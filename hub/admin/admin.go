package admin

import (
	"context"
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
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		h.fail(w, http.StatusMethodNotAllowed, "Not allowed", "This address only answers GET.")
		return
	}
	switch strings.TrimSuffix(r.URL.Path, "/") {
	case "/admin":
		http.Redirect(w, r, web.KeysPath, http.StatusSeeOther)
	case web.LoginPath:
		h.login(w, r)
	case web.KeysPath:
		h.keys(w, r)
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
	h.page(w, map[string]any{"Title": "Users and keys", "Section": "Hub", "Active": web.KeysPath, "Keys": keys})
}

func (h *Handler) placeholder(w http.ResponseWriter, r *http.Request, title, text string) {
	if _, ok := h.client(w, r); !ok {
		return
	}
	h.page(w, map[string]any{"Title": title, "Section": "Tenants", "Active": "/admin/" + strings.ToLower(strings.Fields(title)[len(strings.Fields(title))-1]), "Placeholder": text})
}

func (h *Handler) page(w http.ResponseWriter, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.Page.Execute(w, data)
}

// Package web is the portal (if-portal-web, dec-web-portal): the web pages of the hub for every
// user. The pages show what the key of the user may see.
//
// Addresses:
//
//	GET  /portal/login?code=<code>        swaps the code of a portal link for a session
//	GET  /portal/                         the start page: the tenants of the user
//	GET  /portal/t/<tenant>/              the start page of a tenant: the role and the sessions
//	GET  /portal/t/<tenant>/s/<session>   one session: transcript, minutes, records, release
//	POST /portal/t/<tenant>/s/<session>/release  releases the session (a human who took part)
//	POST /portal/t/<tenant>/s/<session>/trim     cuts an ended session off after an event
//	POST /portal/t/<tenant>/s/<session>/delete   deletes an ended session softly
//	POST /portal/t/<tenant>/s/<session>/restore  restores a deleted session
//	POST /portal/t/<tenant>/s/<session>/object   objects to the release (a human who took part)
//	GET  /portal/t/<tenant>/members       the members of a tenant (tenant admins)
//	GET  /portal/t/<tenant>/wiki          the index of the wiki of a tenant; ?tag=<tag> filters it
//	GET  /portal/t/<tenant>/wiki/a/<path> one artifact of the wiki, with its links and backlinks
//	GET  /portal/t/<tenant>/wiki/search   searches the wiki; ?q=<words>
//	GET  /portal/t/<tenant>/wiki/broken   the links of the wiki that lead nowhere
//	GET  /portal/t/<tenant>/wiki/queue    the sources that no document cites yet
//	GET  /portal/admin/keys               the users and their keys (system admins)
//	GET  /portal/admin/keys/new           the form that registers a key
//	POST /portal/admin/keys/register      registers a key
//	GET  /portal/admin/keys/revoke        asks to confirm that a key is revoked
//	POST /portal/admin/keys/revoke        revokes a key
//	GET  /portal/admin/tenants            the tenants of the hub
//	GET  /portal/admin/admins             the system admins
//
// The navigation shows only what the key may see. The pages have no scripts and no external
// assets. The session is a cookie (CookieName) that scripts cannot read. The
// Content-Security-Policy (CSP) allows only inline styles.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/url"
	"strings"
)

const (
	// PathPrefix is where the portal is mounted.
	PathPrefix = "/portal/"
	// HomePath is the start page.
	HomePath = "/portal/"
	// LoginPath is the address that takes the code of a portal link.
	LoginPath = "/portal/login"
	// TenantPrefix starts the pages of a tenant: /portal/t/<tenant>/…
	TenantPrefix = "/portal/t/"
	// KeysPath is the list of users and keys.
	KeysPath = "/portal/admin/keys"
	// NewKeyPath is the form that registers a key; RegisterPath takes the form.
	NewKeyPath   = "/portal/admin/keys/new"
	RegisterPath = "/portal/admin/keys/register"
	// RevokePath asks for the confirmation (GET) and revokes the key (POST).
	RevokePath = "/portal/admin/keys/revoke"
	// TenantsPath and AdminsPath list the tenants and the system admins.
	TenantsPath = "/portal/admin/tenants"
	AdminsPath  = "/portal/admin/admins"
	// CookieName is the cookie with the access token of the user.
	CookieName = "office_portal"
)

// TenantPath is the start page of a tenant.
func TenantPath(tenant string) string { return TenantPrefix + url.PathEscape(tenant) + "/" }

// SessionPath is the page of one session of a tenant.
func SessionPath(tenant, session string) string {
	return TenantPrefix + url.PathEscape(tenant) + "/s/" + url.PathEscape(session)
}

// ReleasePath takes the form that releases a session; ObjectPath the form that objects.
func ReleasePath(tenant, session string) string { return SessionPath(tenant, session) + "/release" }

// ObjectPath takes the form that objects to the release of a session.
func ObjectPath(tenant, session string) string { return SessionPath(tenant, session) + "/object" }

// DiscardPath takes the form that discards a staged artifact that the release of a session kept
// back.
func DiscardPath(tenant, session string) string { return SessionPath(tenant, session) + "/discard" }

// TrimPath takes the form that trims an ended session after an event; DeletePath the form that
// deletes it softly; RestorePath the form that restores a deleted session (dec-session-trim-delete).
func TrimPath(tenant, session string) string { return SessionPath(tenant, session) + "/trim" }

// DeletePath takes the form that deletes an ended session softly.
func DeletePath(tenant, session string) string { return SessionPath(tenant, session) + "/delete" }

// RestorePath takes the form that restores a deleted session.
func RestorePath(tenant, session string) string { return SessionPath(tenant, session) + "/restore" }

// MembersPath is the page with the members of a tenant.
func MembersPath(tenant string) string { return TenantPrefix + url.PathEscape(tenant) + "/members" }

// WikiPath is the index of the wiki of a tenant.
func WikiPath(tenant string) string { return TenantPrefix + url.PathEscape(tenant) + "/wiki" }

// WikiTagPath is the index of the wiki of a tenant, filtered by a tag.
func WikiTagPath(tenant, tag string) string { return WikiPath(tenant) + "?tag=" + url.QueryEscape(tag) }

// WikiArtifactPath is the page of one artifact of the wiki. The path of the artifact keeps its
// slashes; each part is escaped.
func WikiArtifactPath(tenant, artifact string) string {
	parts := strings.Split(artifact, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return WikiPath(tenant) + "/a/" + strings.Join(parts, "/")
}

// WikiSearchPath is the search of the wiki of a tenant; the query is the parameter q.
func WikiSearchPath(tenant string) string { return WikiPath(tenant) + "/search" }

// WikiBrokenPath lists the links of the wiki that lead nowhere.
func WikiBrokenPath(tenant string) string { return WikiPath(tenant) + "/broken" }

// WikiQueuePath lists the sources that no document cites yet (the ingest queue).
func WikiQueuePath(tenant string) string { return WikiPath(tenant) + "/queue" }

// CSP is the Content-Security-Policy of every page. The pages cannot be framed.
const CSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

//go:embed embed/*
var files embed.FS

// SourceDir is the directory of the embedded files in a checkout of the repository. The preview
// reads the files from there, so a change shows without a new build.
const SourceDir = "hub/portal/web/embed"

// Templates are the page templates of the portal, parsed from a set of files.
type Templates struct {
	// Page is a page of the portal. Fields: Title, Section, Me, Nav, Mode and the fields of the
	// mode. The parts of the page (release, wiki and the lists of the wiki) are templates of it.
	Page *template.Template
	// ErrorPage is the page for a missing, used or expired session or link. It shows no data.
	// Fields: Title, Message.
	ErrorPage *template.Template
}

// Load parses the files page.html, error.html, the parts of the page and portal.css in fsys. The
// files of the package are the files in embed/; the preview loads them from disk.
func Load(fsys fs.FS) (*Templates, error) {
	read := func(name string) (string, error) {
		b, err := fs.ReadFile(fsys, name)
		return string(b), err
	}
	css, err := read("portal.css")
	if err != nil {
		return nil, err
	}
	// The template package would escape the style sheet as text.
	cssFunc := func() template.CSS { return template.CSS(css) }
	text, err := read("page.html")
	if err != nil {
		return nil, err
	}
	page, err := template.New("page").Funcs(template.FuncMap{
		"css": cssFunc,
		"initials": func(name string) string {
			var out []rune
			for _, w := range splitWords(name) {
				out = append(out, upperFirst(w))
				if len(out) == 2 {
					break
				}
			}
			return string(out)
		},
		"short": func(id string) string {
			if len(id) > 22 {
				return id[:14] + "…" + id[len(id)-6:]
			}
			return id
		},
	}).Parse(text)
	if err != nil {
		return nil, err
	}
	for _, part := range []string{"release", "wiki", "wiki-author", "wiki-entries", "wiki-links"} {
		if text, err = read(part + ".html"); err != nil {
			return nil, err
		}
		if _, err = page.New(part).Parse(text); err != nil {
			return nil, err
		}
	}
	if text, err = read("error.html"); err != nil {
		return nil, err
	}
	errPage, err := template.New("error").Funcs(template.FuncMap{"css": cssFunc}).Parse(text)
	if err != nil {
		return nil, err
	}
	return &Templates{Page: page, ErrorPage: errPage}, nil
}

// Embedded returns the templates that are compiled into the program.
func Embedded() *Templates { return embedded }

// embedded are the templates compiled into the program. The files are part of the package, so a
// failure to load them is a programming error.
var embedded = func() *Templates {
	sub, err := fs.Sub(files, "embed")
	if err != nil {
		panic(err)
	}
	t, err := Load(sub)
	if err != nil {
		panic(err)
	}
	return t
}()

// Page is a page of the portal. Fields: Title, Section, Me, Nav, Mode and the fields of the
// mode. The parts of the page (release, wiki and the lists of the wiki) are templates of it.
var Page = embedded.Page

// ErrorPage is the page for a missing, used or expired session or link. It shows no data. Fields:
// Title, Message.
var ErrorPage = embedded.ErrorPage

func splitWords(s string) []string {
	var out []string
	cur := []rune{}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = cur[:0]
			}
			continue
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func upperFirst(w string) rune {
	r := []rune(w)[0]
	if r >= 'a' && r <= 'z' {
		return r - 'a' + 'A'
	}
	return r
}

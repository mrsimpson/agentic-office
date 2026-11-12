// Package web is the admin view (if-admin-web): the web pages for the admins of the hub.
//
// Addresses:
//
//	GET  /admin/login?code=<code>   swaps the code of an admin link for a session and opens the view
//	GET  /admin/keys                the users and their keys
//	GET  /admin/keys/new            the form that registers a key
//	POST /admin/keys/register       registers a key
//	GET  /admin/keys/revoke         asks to confirm that a key is revoked
//	POST /admin/keys/revoke         revokes a key
//	GET  /admin/tenants             placeholder until multi-tenancy (dec-multi-tenancy)
//	GET  /admin/admins              placeholder until multi-tenancy
//
// The pages have no scripts and no external assets. The session is a cookie (CookieName) that
// scripts cannot read. The Content-Security-Policy (CSP) allows only inline styles.
package web

import "html/template"

const (
	// PathPrefix is where the admin view is mounted.
	PathPrefix = "/admin/"
	// LoginPath is the address that takes the code of an admin link.
	LoginPath = "/admin/login"
	// KeysPath is the first page.
	KeysPath = "/admin/keys"
	// NewKeyPath is the form that registers a key; RegisterPath takes the form.
	NewKeyPath   = "/admin/keys/new"
	RegisterPath = "/admin/keys/register"
	// RevokePath asks for the confirmation (GET) and revokes the key (POST).
	RevokePath = "/admin/keys/revoke"
	// CookieName is the cookie with the access token of the admin.
	CookieName = "office_admin"
)

// CSP is the Content-Security-Policy of every page. The pages cannot be framed.
const CSP = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// The design is the design of the stage (hub/stage/web). There is no shared package, so the tokens
// are copied.
const css = `
:root { color-scheme: light dark; --bg: #fafaf8; --text: #111; --muted: #777; --line: #e4e4df; --soft: #f0f0ec; --ink: #111; --on-ink: #fff;
  --live: #1f8a52; --rec: #c0392b; --warn: #b7791f; }
@media (prefers-color-scheme: dark) { :root { --bg: #0c0c0d; --text: #ececec; --muted: #8a8a90; --line: #26262a; --soft: #17171a; --ink: #ececec; --on-ink: #0c0c0d;
  --live: #3ddc97; --rec: #ff6b5e; --warn: #f5b84b; } }
* { box-sizing: border-box; }
html { font-size: clamp(15px, 1.1vw, 24px); }
body { margin: 0; background: var(--bg); color: var(--text); font-family: "Helvetica Neue", Helvetica, Arial, system-ui, sans-serif; line-height: 1.5; -webkit-font-smoothing: antialiased; display: grid; grid-template-columns: 15rem minmax(0, 1fr); min-height: 100vh; }
nav { border-right: 1px solid var(--line); padding: 1.6rem 1.2rem; }
.brand { display: flex; align-items: center; gap: 0.7rem; font-weight: 600; margin-bottom: 2rem; }
.logo { width: 1.1rem; height: 1.1rem; border-radius: 50%; background: var(--ink); }
.eyebrow, h2 { margin: 0; font-size: 0.7rem; font-weight: 500; letter-spacing: 0.13em; text-transform: uppercase; color: var(--muted); }
nav .eyebrow { margin: 1.4rem 0.6rem 0.4rem; }
nav a { display: block; padding: 0.4rem 0.6rem; color: var(--muted); text-decoration: none; border-left: 2px solid transparent; }
nav a.on { color: var(--text); border-left-color: var(--ink); font-weight: 600; }
main { padding: 2rem 3rem 3rem; max-width: 70rem; }
h1 { margin: 0.2rem 0 1.6rem; font-size: 1.8rem; font-weight: 400; font-family: Georgia, "Times New Roman", serif; }
table { width: 100%; border-collapse: collapse; }
th { text-align: left; padding: 0.6rem 0.8rem; font-size: 0.7rem; font-weight: 500; letter-spacing: 0.13em; text-transform: uppercase; color: var(--muted); border-bottom: 1px solid var(--text); }
td { padding: 0.7rem 0.8rem; border-bottom: 1px solid var(--line); vertical-align: middle; }
.who { display: flex; align-items: center; gap: 0.8rem; }
.avatar { flex: none; width: 2rem; height: 2rem; border-radius: 50%; border: 1px solid var(--text); display: grid; place-items: center; font-size: 0.68rem; font-weight: 600; }
.avatar::before { content: attr(data-initials); }
.avatar.agent { border-radius: 0; }
.name { font-weight: 600; }
small, .muted { color: var(--muted); }
code { font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 0.85rem; color: var(--muted); }
.badge { display: inline-flex; align-items: center; gap: 0.4em; font-size: 0.75rem; font-weight: 500; padding: 0 0.6em; border: 1px solid var(--line); border-radius: 99px; color: var(--muted); }
.badge::before { content: ""; width: 0.5em; height: 0.5em; border-radius: 50%; background: currentColor; }
.badge.active { color: var(--live); border-color: currentColor; }
.badge.revoked { color: var(--rec); border-color: currentColor; }
.badge.admin { color: var(--text); border-color: var(--text); }
.empty { color: var(--muted); }
.toolbar { display: flex; justify-content: flex-end; margin: -3.2rem 0 1.4rem; }
.button, button { font: inherit; font-size: 0.9rem; font-weight: 500; padding: 0.4rem 1.1rem; border: 1px solid var(--ink); border-radius: 0; background: var(--ink); color: var(--on-ink); text-decoration: none; cursor: pointer; }
.button.quiet, button.quiet { background: none; color: var(--text); border-color: var(--line); }
button.danger { background: var(--rec); border-color: var(--rec); color: #fff; }
.link { color: var(--muted); font-size: 0.85rem; text-decoration: underline; text-underline-offset: 0.2em; }
form.stack { display: flex; flex-direction: column; gap: 1.2rem; max-width: 34rem; }
label { display: flex; flex-direction: column; gap: 0.3rem; font-weight: 600; font-size: 0.9rem; }
label small { font-weight: 400; }
input, select, textarea { font: inherit; padding: 0.5rem 0.7rem; border: 1px solid var(--line); border-radius: 0; background: var(--bg); color: var(--text); }
input:focus, select:focus, textarea:focus { outline: 1px solid var(--ink); border-color: var(--ink); }
textarea { font-family: ui-monospace, Menlo, Consolas, monospace; font-size: 0.85rem; min-height: 4.5rem; }
.buttons { display: flex; gap: 0.8rem; align-items: center; }
.notice, .error { margin: 0 0 1.4rem; padding: 0.6rem 1rem; border-left: 3px solid var(--live); background: var(--soft); max-width: 40rem; }
.error { border-left-color: var(--rec); }
.facts { max-width: 34rem; margin: 0 0 1.4rem; }
.facts dt { font-size: 0.7rem; letter-spacing: 0.13em; text-transform: uppercase; color: var(--muted); margin-top: 0.8rem; }
.facts dd { margin: 0.1rem 0 0; }
.placeholder { max-width: 30rem; padding: 2rem 0; }
.placeholder p { color: var(--muted); }
body.error { display: block; }
body.error main { padding: 4rem 3rem; }
body.error h1 { color: var(--warn); }
`

// Page is a page of the admin view. Fields: Title, Active (the address of the open page), Keys,
// Message (for placeholders).
var Page = template.Must(template.New("page").Funcs(template.FuncMap{
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
}).Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Title}} · Agentic office admin</title><style>` + css + `</style></head>
<body>
<nav><div class="brand"><div class="logo"></div>Office admin</div>
<div class="eyebrow">Hub</div>
<a href="/admin/keys"{{if eq .Active "/admin/keys"}} class="on"{{end}}>Users and keys</a>
<div class="eyebrow">Tenants</div>
<a href="/admin/tenants"{{if eq .Active "/admin/tenants"}} class="on"{{end}}>Tenants</a>
<a href="/admin/admins"{{if eq .Active "/admin/admins"}} class="on"{{end}}>System admins</a>
</nav>
<main>
<div class="eyebrow">{{.Section}}</div>
<h1>{{.Title}}</h1>
{{if .Notice}}<p class="notice" role="status" data-notice>{{.Notice}}</p>{{end}}{{if .Error}}<p class="error" role="alert" data-error>{{.Error}}</p>{{end}}
{{if eq .Mode "placeholder"}}<div class="placeholder" data-placeholder="{{.Active}}"><p>{{.Placeholder}}</p></div>
{{else if eq .Mode "register"}}<form class="stack" method="post" action="/admin/keys/register" data-form="register">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<label>User ID<input name="participant_id" value="{{.Form.ParticipantID}}" required maxlength="64" pattern="[A-Za-z0-9][A-Za-z0-9._-]*" autocomplete="off"><small>1 to 64 letters, digits, ".", "_" or "-", for example ada.</small></label>
<label>Display name<input name="display_name" value="{{.Form.DisplayName}}" maxlength="100" autocomplete="off"><small>Needed for a new user.</small></label>
<label>Kind<select name="kind"><option value="human"{{if ne .Form.Kind "agent"}} selected{{end}}>human</option><option value="agent"{{if eq .Form.Kind "agent"}} selected{{end}}>agent</option></select></label>
<label>Public key<textarea name="public_key" required spellcheck="false" autocomplete="off">{{.Form.PublicKey}}</textarea><small>The base64 public key that <code>office key show</code> prints.</small></label>
<div class="buttons"><button type="submit">Register key</button><a class="link" href="/admin/keys">Cancel</a></div>
</form>
{{else if eq .Mode "revoke"}}<dl class="facts" data-confirm="revoke">
<dt>User</dt><dd>{{.Key.Name}} <small>{{.Key.ParticipantId}}</small></dd>
<dt>Key</dt><dd><code>{{.Key.KeyId}}</code></dd></dl>
<p class="muted">The key stops working at once, and all its access tokens end. A revoked key cannot be registered again.</p>
<form class="buttons" method="post" action="/admin/keys/revoke" data-form="revoke">
<input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="key_id" value="{{.Key.KeyId}}">
<button class="danger" type="submit">Revoke key</button><a class="link" href="/admin/keys">Cancel</a></form>
{{else}}<div class="toolbar"><a class="button" href="/admin/keys/new">Register key</a></div>
{{if .Keys}}<table class="keys">
<tr><th>User</th><th>Kind</th><th>Key</th><th>Status</th><th></th></tr>
{{range .Keys}}<tr data-participant="{{.ParticipantId}}" data-key="{{.KeyId}}">
<td><div class="who"><span class="avatar{{if eq .KindName "agent"}} agent{{end}}" data-initials="{{initials .Name}}" aria-hidden="true"></span><div><div class="name">{{.Name}}</div><small>{{.ParticipantId}}</small></div></div></td>
<td>{{.KindName}}</td>
<td><code title="{{.KeyId}}">{{short .KeyId}}</code></td>
<td>{{if .Revoked}}<span class="badge revoked">revoked</span>{{else}}<span class="badge active">active</span>{{end}}{{if .Admin}} <span class="badge admin">admin</span>{{end}}</td>
<td>{{if and (not .Revoked) (not .Admin)}}<a class="link" href="/admin/keys/revoke?key_id={{.KeyId}}">Revoke</a>{{end}}</td>
</tr>
{{end}}</table>{{else}}<p class="empty">There are no keys yet.</p>{{end}}{{end}}
</main></body></html>
`))

// ErrorPage is the page for a missing, used or expired session or link. It shows no data. Fields:
// Title, Message.
var ErrorPage = template.Must(template.New("error").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · Agentic office admin</title><style>` + css + `</style></head>
<body class="error"><main><h1>{{.Title}}</h1><p id="error">{{.Message}}</p></main></body></html>
`))

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

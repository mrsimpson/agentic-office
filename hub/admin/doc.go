// Package admin is the building block Admin view (bb-admin in
// docs/architecture/05-building-blocks.arc42.md). It realizes if-admin-web, whose contract is
// package web: the addresses, the pages and the design of the admin view.
//
// The admin view (/admin/) is a web page for the admins of the hub. It has the design of the
// stage, in dark and light, and no scripts (dec-admin-web). Today it shows the users with their
// keys, the tenants and the system admins (read only), and it creates users and adds and revokes
// keys. Only a system admin gets a link.
//
// How it works:
//
//   - "office admin link" prints a link. The link works once and expires after a few minutes.
//     The identity part has the calls behind it: CreateAdminLink (only for a system admin, no
//     tenant) and ExchangeAdminLink (needs no token).
//   - Opening the link swaps its code for an access token of the admin key, keeps the token in a
//     cookie that scripts cannot read (office_admin, one hour), and redirects to the list of
//     keys. A used or expired link gives an error page. The pages are /admin/keys,
//     /admin/tenants and /admin/admins.
//   - Every page asks the identity part in-process with this token, so the view has exactly the
//     rights of the admin key and no rights of its own.
//   - Register a key: "Register key" opens a form with the user ID, the display name (needed for a
//     new user), the kind and the base64 public key that "office key show" prints. It calls
//     CreateUser for a new user and AddKey for a known one. A refusal shows its message on the
//     form, which keeps what you typed.
//   - Revoke a key: "Revoke" next to a key opens a page that asks you to confirm. It calls
//     RevokeKey. A user keeps at least one key that works: the last key cannot be revoked.
//   - Every form carries a token that is derived from the session of the admin, and the view
//     refuses a post whose Origin is another server.
package admin

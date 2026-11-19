// Package portal is the building block Portal (bb-portal in
// docs/architecture/05-building-blocks.arc42.md). It realizes if-portal-web, whose contract is
// package web: the addresses, the pages and the design of the portal (dec-web-portal).
//
// The portal (/portal/) is the web view of the hub for every user. It has the design of the
// stage, in dark and light, and no scripts. It shows what the key of the user may see: the
// tenants of the user, the sessions of a tenant that the user may see (if-session-list), the
// members of a tenant to its tenant admins, and the administration (users and keys, tenants,
// system admins) to system admins. The records and artifacts of a tenant follow (#179).
//
// How it works:
//
//   - "office portal link" prints a link. The link works once and expires after a few minutes.
//     The identity part has the calls behind it: CreatePortalLink (every user; it may name a
//     tenant of the user) and ExchangePortalLink (needs no token).
//   - Opening the link swaps its code for an access token of the key, keeps the token in a cookie
//     that scripts cannot read (office_portal, one hour), and redirects to the tenant that the
//     link names, else to the start page. A used or expired link gives an error page.
//   - Every page asks the auth API who the user is (WhoAmI) and builds the navigation from it:
//     the tenants of the user, the pages of the open tenant, and the administration for a system
//     admin. Every page then asks the interfaces in-process with the token, so the portal has
//     exactly the rights of the key and no rights of its own. A tenant of which the user is no
//     member does not exist for it (404).
//   - The page of a tenant lists the sessions that the user may see, the newest first: those it
//     joined or imported, and every session for a tenant admin. It asks the session list with the
//     tenant of the page.
//   - The start page lists the tenants of the user. A user with one tenant and no administration
//     goes straight to that tenant.
//   - Register a key: "Register key" opens a form with the user ID, the display name (needed for a
//     new user), the kind and the base64 public key that "office key show" prints. It calls
//     CreateUser for a new user and AddKey for a known one. A refusal shows its message on the
//     form, which keeps what you typed.
//   - Revoke a key: "Revoke" next to a key opens a page that asks you to confirm. It calls
//     RevokeKey. A user keeps at least one key that works: the last key cannot be revoked.
//   - Every form carries a token that is derived from the session of the user, and the portal
//     refuses a post whose Origin is another server.
package portal

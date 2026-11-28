// Package portal is the building block Portal (bb-portal in
// docs/architecture/05-building-blocks.arc42.md). It realizes if-portal-web, whose contract is
// package web: the addresses, the pages and the design of the portal (dec-web-portal).
//
// The portal (/portal/) is the web view of the hub for every user. It has the design of the
// stage, in dark and light, and no scripts. It shows what the key of the user may see: the
// tenants of the user, the sessions of a tenant that the user may see (if-session-list), the
// members of a tenant to its tenant admins, and the administration (users and keys, tenants,
// system admins) to system admins, and the wiki of a tenant to its members.
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
//   - The page of a session (session.go) reads the log from the session stream, which decides who
//     may read it (a refusal gives 404), and the minutes, decisions, notes and tasks of the session
//     from their interfaces. It shows the header, the stored minutes (the running minutes while
//     the session runs), the records and the transcript: a sentence off the record is a
//     placeholder and does not appear, a correction replaces the text. A session ID may hold a
//     slash; the pages of a tenant work on the escaped path.
//   - The page of a session also shows its release (release.go, concept-session-release): the
//     status and the deadline from GetRelease of the session commands, and the staged artifacts,
//     read through the archive read API, which serves the staged version to the readers of the
//     session. When GetRelease says that the user may release (a human who took part), the page
//     has a form that releases and, while the session waits, one that objects. They post to
//     .../s/<session>/release and .../object and call ReleaseSession and ObjectToRelease; each
//     form carries a command ID, so a form sent twice has one effect. When the hub refuses a
//     release because staged files quote sentences off the record, the page reads the places from
//     the error detail and shows a form "Release anyway" that sends the refused command again
//     with the quote token. Next to each artifact that the release kept back (unreleased of
//     GetRelease), the page has a form "Discard" that posts to .../discard with the path and the
//     version that the page read. It calls the removal of the interface of the type that the
//     directory of the path names (decisions, minutes, notes, slides or documents) with the
//     session; the part of the type decides. A member who may not read the session
//     gets 404 from the session stream; for a released session the page then shows only the
//     release: who released, the co-authors of the release commit and the released artifacts.
//   - The page of an ended session that is not released also has the trim and delete forms
//     (dec-session-trim-delete), for a human who took part and for a human tenant admin. "Trim"
//     keeps the sentence that the user chooses with everything up to the next sentence, and calls
//     TrimSession; the transcript and the running minutes of the page then stop there, and the
//     portal writes the stored minutes again from the log up to the cut (or removes them when no
//     minutes count). "Delete" and "Restore" call DeleteSession and RestoreSession. The list of a
//     tenant leaves out deleted sessions; "?deleted=1" shows them.
//   - The wiki of a tenant (wiki.go, concept-wiki) asks the view of the wiki (if-wiki-api) with
//     the tenant of the page; every member reads it. /wiki is the index with every tag and its
//     count, and ?tag=<tag> filters it. /wiki/a/<path> is one artifact: what the index says about
//     it, its content from the archive read API, its links, its backlinks, its cited sources and
//     the documents that cite it. A Markdown file shows its front matter as written (the status,
//     owner and reviews of a document) and its text rendered without raw HTML; a link in the text
//     whose target the view knows as an artifact leads to the page of that artifact, and any other
//     relative link shows only its text. Any other file shows as text. /wiki/search?q=<words> is
//     a form without scripts and its hits, best first. /wiki/broken lists the links that lead
//     nowhere, /wiki/queue the sources that no document cites yet. The view leaves out staged
//     artifacts, so their pages give 404.
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
//   - Static files (dec-ui-base): every page links two style sheets of the same origin, the base
//     of the design system and its own, at /portal/assets/<name>. They need no login and no
//     cookie, because they hold no data of a user. The name of a style sheet holds a hash of its
//     content, so the answer is cached for a year and a changed file has a new address. An unknown
//     name answers 404. The notice of the third-party licences is served there too. The pages
//     have no inline style: the Content-Security-Policy allows style sheets of the same origin
//     only.
package portal

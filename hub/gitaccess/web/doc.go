// Package web is the interface if-git-access of the building block Git access: the repository of
// a tenant over HTTPS with the Git smart protocol (dec-artifact-sync).
//
// Address: the hub address, then PathPrefix, then the tenant. The repository of the tenant
// "acme" on the hub "https://hub.example.org:8740" is
//
//	https://hub.example.org:8740/git/acme
//
// Any Git client can use it: "git clone https://hub.example.org:8740/git/acme". The repository
// holds the artifacts of the tenant on the branch main and nothing else. The session logs lie in
// another repository that this interface never serves.
//
// Authentication: HTTP Basic. The user name is ignored. The password is the access token of the
// participant, as a Git credential helper hands it over. A request without a token, or with an
// unknown or expired token, gets 401 and the header "WWW-Authenticate: Basic", so that Git asks
// for credentials. A participant who is not a member of the tenant, the token of a stage link and
// a tenant that does not exist all get the same answer: 404 "not found".
//
// Operations: fetch and clone (the Git service git-upload-pack) and push (git-receive-pack).
// Other addresses below the repository, such as the files of the dumb protocol, answer 404.
//
// Rules of a push. A refused push changes nothing, and Git shows the reasons as "remote:" lines.
//
//   - Only the branch main may change, and only forward. No other branch, no tag, no deletion, no
//     forced push. If main has moved since your last fetch, the push is refused: fetch, merge or
//     rebase, and push again.
//   - The author of every new commit is you: your display name and the address
//     "<participant ID>@office.invalid". Set user.name and user.email accordingly.
//   - Only Markdown (.md) and YAML (.yaml, .yml) files change. You may remove them. Hidden files
//     and folders, links and every other file type are refused.
//   - Every added or changed file must pass the check of its schema (the final state of the
//     push, not each commit).
//   - A push may have a pack of at most 32 MiB, files of at most 1 MiB and at most 500 changed
//     files, unless the operator of the hub set other limits.
package web

// PathPrefix is where the repositories are mounted. The tenant follows it as the next path
// segment.
const PathPrefix = "/git/"

// Package gitaccess is the building block Git access (bb-git-access in
// docs/architecture/05-building-blocks.arc42.md). It serves the artifact repository of every
// tenant over the Git smart protocol behind a gate for the token and the tenant
// (dec-artifact-sync). The address and the rules for the client are in the package web
// (if-git-access).
//
// This part is the gate and the wiring only. Git does the work: the part hands every request that
// passes the gate to "git http-backend" (CGI) and translates nothing.
//
// The gate runs before Git reads anything:
//
//  1. The token is the password of HTTP Basic. The token check (if-token-check) decides. No token
//     or a bad one gives 401 with a Basic challenge.
//  2. The caller must be an active member of the tenant in the address. A non-member, the token of
//     a stage link and an unknown tenant all get 404.
//  3. The repository comes from the archive (if-archive-repository, Dir). The part never touches
//     the session-log repository.
//  4. Only the fetch of the smart protocol passes. A push is refused with 403 in this step; all
//     other addresses answer 404.
//
// How Git is started: for every request, one process "git http-backend" with GIT_PROJECT_ROOT set
// to the repository of this one tenant, so no other repository is reachable. The environment is
// built from scratch: no system or global Git configuration, no hooks, no REMOTE_USER (Git
// itself then refuses a push), the config http.receivepack=false, and all references hidden
// except refs/heads/main. The header Authorization is not passed on to Git. The "git" command
// line tool must be in PATH.
package gitaccess

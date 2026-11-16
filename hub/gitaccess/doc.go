// Package gitaccess is the building block Git access (bb-git-access in
// docs/architecture/05-building-blocks.arc42.md). It serves the artifact repository of every
// tenant over the Git smart protocol behind a gate for the token and the tenant
// (dec-artifact-sync). The address and the rules for the client are in the package web
// (if-git-access).
//
// This part is the gate, the environment and the rules of a push. Git does the work: the part
// hands every request that passes the gate to "git http-backend" (CGI) and translates nothing.
//
// The gate runs before Git reads anything:
//
//  1. The token is the password of HTTP Basic. The token check (if-token-check) decides. No token
//     or a bad one gives 401 with a Basic challenge.
//  2. The caller must be an active member of the tenant in the address. A non-member, the token of
//     a stage link and an unknown tenant all get 404.
//  3. The repository comes from the archive (if-archive-repository, Dir). The part never touches
//     the session-log repository.
//  4. Only the smart protocol of one repository passes, for fetch (git-upload-pack) and for push
//     (git-receive-pack). All other addresses answer 404.
//
// How Git is started: for every request, one process "git http-backend" with GIT_PROJECT_ROOT set
// to the repository of this one tenant, so no other repository is reachable. The environment is
// built from scratch: no system or global Git configuration, and the Git configuration of the
// request comes from GIT_CONFIG_COUNT and its keys, so the configuration file of the repository
// stays as the archive wrote it. All references are hidden except refs/heads/main, for fetch and
// for push. The header Authorization is not passed on to Git. The "git" command line tool must be
// in PATH.
//
// A push (rs-git-push) is checked inside Git, by the hook pre-receive, before Git accepts it.
// The hook is the hub program itself: when the part starts, it writes a small script into
// Options.HooksDir that starts the hub program with the argument SubcommandPreReceive, and Git
// finds the script through core.hooksPath. The hook gets the participant (ID and display name) and
// the tenant in its environment from the gate (the Env... constants), never from the client. The
// rules, in RunPreReceive:
//
//   - Only the reference refs/heads/main may change. No other branch, no tag, no deletion, and no
//     rewrite of the history (the new commit must descend from the old main; Git also runs with
//     receive.denyNonFastForwards and receive.denyDeletes). A push on an old base is refused with
//     the advice to fetch and merge first.
//   - The author of every new commit is the participant of the token: the display name and the
//     address "<participant ID>@office.invalid", as for the commits of the hub. One commit with
//     another author refuses the whole push.
//   - Only artifact files change: Markdown (.md) and YAML (.yaml, .yml). A file may be removed. A
//     hidden path element, an absolute path, ".." and every other file type is refused, and so is
//     a link or a submodule.
//   - Every file that was added or changed between the old main and the new main is checked with
//     the artifact check (if-artifact-check). Only this final state counts, not each commit in
//     between. The hook knows no schema.
//   - The size is limited (risk-push-size) by three options: MaxPushBytes for the pack
//     (receive.maxInputSize), MaxFileBytes for one file and MaxFiles for the number of changed
//     files. The defaults are 32 MiB, 1 MiB and 500 files; the hub program sets them with
//     -git-max-push-mib, -git-max-file-kib and -git-max-files.
//
// A refused push changes nothing and shows its reasons to the participant as "remote:" lines.
// After a push, whatever the result, the part tells the archive (if-archive-repository,
// MainMoved), which sends a real change of main to the followers (if-artifact-changes). A push and
// a commit of the archive at the same moment are safe, because both move main with the atomic
// reference update of Git: the later one finds main moved and starts again (the archive) or is
// refused (the push).
package gitaccess

// Package archive is the building block Archive (bb-archive in
// docs/architecture/05-building-blocks.arc42.md).
//
// It provides if-archive-storage (package storage): it stores the records of the parts of the
// hub as files in a local bare Git repository on the hub host (dec-yaml-git-storage). The
// repository has no work tree: the files exist only as Git objects. Every Change is one commit.
// After a commit, the archive pushes it to a remote through a Pusher. The only Pusher for now is NoPush, which does nothing and reports
// success (issue #43 covers the real remote).
//
// The hub has several repositories (concept-multi-tenancy), kept in Repos (storage.Archives): the
// hub repository, where Identity keeps users, tenants and memberships, and three repositories for
// each tenant: one for its artifacts, one for its session logs and one for what sessions have not
// released (the staging repository). They lie below one root in a fixed layout, hub/,
// tenants/<id>/ (artifacts), session-logs/<id>/ (session logs) and staging/<id>/ (staging). The
// four directories keep the repositories apart, whatever the tenant ID is. Each is a Repo with its
// own commit queue, so a busy tenant does not hold up another tenant or the hub repository, and a
// session write does not hold up a record. The repositories of a tenant are created with the
// tenant and stay when the tenant is removed. At start, Repos opens them and creates the
// session-log and the staging repository if a tenant has none yet.
//
// The artifact repository of a tenant starts with one first commit that holds no artifact: an
// empty commit by office-hub, so the branch main exists from the start and a clone of a new tenant
// works. The session-log repository and the hub repository have no such commit. CreateTenant
// makes the first commit; Open does not, and a tenant repository without main is not supported.
//
// The session logs hold the original of sentences that are off the record (dec-off-the-record),
// so they have a repository of their own: only the meeting session uses it (Archives.SessionLogs),
// Git access never serves it, and no interface for participants reads it. Git access never
// serves the staging repository either: if-archive-repository gives it only the artifact
// repository. if-archive-repository and if-artifact-changes concern the artifact repository only.
// if-archive-read reads the artifact repository, and a staged file from the staging area for a
// reader of its session or a tenant admin (ReadService); the stage reads the staged files of its
// own session.
//
// The staging repository (dec-staging-repository, type Staging, storage.Staging) has the layout
// of the artifact repository: a staged file lies at the path that it gets on its release. The
// state of each area lies in .areas/<name>/area.yaml, where <name> is the session ID, or "=" plus
// its base64url form when the ID is not safe as a file name. The state holds the session ID, the
// readers, the deadline, whether an objection holds the release, every name that ever lay in the
// area, the authors of its changes and the steps of a running release. The directory starts with a
// dot, so no artifact path can lie in it, and no read of artifacts and no watcher sees it. A write
// to an area reads the state of every area first, so a path belongs to one area only. Every
// method takes the commit queue of the staging repository for its whole read and write, and only
// the archive writes this repository, so the state it reads is the state it builds on.
//
// A part that staged a file and then could not write its stub takes the file back (TakeBack): the
// file goes away and the area forgets its name, because the path may hold a file that a push put
// there in between. So the release never removes a file at a path whose stub was never written.
//
// The release runs one at a time per tenant and has three steps, each recorded:
//
//  1. Close: the state gets the release author; from now on writes and OpenArea fail with
//     storage.ErrAreaClosed.
//  2. Move: one commit in the artifact repository with every file of the area and the removal of
//     the stub of every name whose file is gone. The author is the release author (or office-hub),
//     the message names the session in the trailer "Office-Release" and lists the authors of the
//     area as "Co-authored-by", except the author of the commit. The archive copies the file
//     objects as they are and reads no content. The ID of the commit is written into the state
//     before main moves to it; when main moved in the meantime, the commit is built again and
//     recorded again.
//  3. Clean up: one commit in the staging repository removes the files and the state of the area.
//
// Released finds the release commit of a session in the history of main by its trailer, with its
// author (empty for the hub), its co-authors and the paths it wrote.
//
// After a restart, a closed area shows its running release (Area.Release) and Release completes
// it: the recorded commit is not made again when it lies in the history of main, which is never
// rewritten. An area with nothing to move releases without a commit in the artifact repository.
//
// It provides if-archive-watch (package watch): the in-process form of the changes. The parts that
// keep files in memory (records, tasks, slides) ask Repo.Watch (Repos.Watcher for a tenant) for the
// created, changed and removed artifacts below their directories and reload them. A watcher is a
// follower of the same delivery as a call of the changes stream (follow.go): one path serves both,
// with the same buffer of 16 events, and a watcher that does not read fast enough is dropped and
// its subscription ends with watch.ErrDropped. The watcher is registered when Watch returns. No
// token is needed. The artifact repository (Repos.Watcher) and the staging repository
// (Repos.StagingWatcher) of a tenant have watchers.
//
// It provides if-archive-read (ReadService, the Protobuf contract in package api), if-archive-repository
// (Repos implements package repository: where Git finds the bare repository of a tenant, and the
// notice that main moved) and if-artifact-changes (ChangesService, the contract in package
// changes/api: the artifacts that changed on main).
//
// Reports of changes: every change of main of a repository gives one event to the followers of
// the tenant, whether Commit made it or a push did and Git access reported it with MainMoved.
// The repository remembers the last state of main that it told the followers. After a change, it
// puts the move into a queue without waiting; one goroutine, which runs only while the queue is
// not empty, compares the old and the new tree (git diff-tree) and sends the artifacts (Markdown
// and YAML files) to each follower. Delivery to a follower never blocks:
// a follower with a full buffer is dropped (see package changes). The state of main at start is
// not reported. A follower first gets the signal "registered", a message of its own type without
// changes, and then one message with changes for each reported change of main.
//
// How it works:
//
//   - Open creates the directory and runs "git init --bare" if it is not a repository yet. The
//     branch is main. It uses the git command line tool that it finds in PATH when it opens.
//   - Commits are predictable: git runs without the system and global configuration of the user
//     of the hub (no hooks, no signing, no editor) and with the identity "office-hub".
//   - Only the hub writes the repository by its own commits, but other tools may move main, for
//     example a push from a clone. Open removes the lock files and the temporary index that a hub
//     left when it died during a commit. On Linux, git stops when the hub dies.
//   - Commit carries out one change at a time in each repository. Session writes lie in the
//     session-log repository, so a record is written synchronously and never waits behind them.
//   - Commit writes every file as a Git object, builds the new tree on the current commit with a
//     temporary index, creates the commit object and then moves main to it in one atomic step
//     that names the commit main must still have (git update-ref with the old value). A change
//     that alters nothing creates no commit.
//   - When main moved in the meantime, Commit builds the same change on the new commit and tries
//     again. If someone else changed a file of the change in between, Commit fails with a
//     *storage.ConflictError. It does the same when the Base of a file is stale. A failed commit
//     leaves nothing visible: main does not move. Objects that nobody refers to may stay in the
//     repository.
//   - Read returns the files of a directory as they are in the current commit (git ls-tree and
//     git cat-file). It does not wait for a running change. The records of the hub use it to
//     rebuild their index at start.
//   - Names returns every path that ever lay in a directory in the history of main, also of files
//     that were removed later (git log with the filter "added", without renames, no content read).
//     The parts that number their files take the highest number among the names as the floor of
//     their next number, so a number whose file a push removed is never given again. History is
//     never rewritten, so the answer only grows. The cost is one git run for each directory.
//   - A failed push does not fail the commit: the commit is in the local repository, and the
//     next push takes it along. The error is logged.
//
// The archive does not know the formats of the records. The meeting session decides the layout
// of a session (see package session), the records part the layout of decisions, minutes and
// notes (see package records).
package archive

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
// hub repository, where Identity keeps users, tenants and memberships, and one repository for each
// tenant: one for its artifacts and one for its session logs. They lie below one root in a fixed
// layout, hub/, tenants/<id>/ (artifacts) and session-logs/<id>/ (session logs). The three
// directories keep the repositories apart, whatever the tenant ID is. Each is a Repo with its own
// commit queue, so a busy tenant does not hold up another tenant or the hub repository, and a
// session write does not hold up a record. Both repositories of a tenant are created with the
// tenant and stay when the tenant is removed. At start, Repos opens both and creates the
// session-log repository if a tenant has none yet.
//
// The session logs hold the original of sentences that are off the record (dec-off-the-record),
// so they have a repository of their own: only the meeting session uses it (Archives.SessionLogs),
// Git access never serves it, and no interface for participants reads it. if-archive-read,
// if-archive-repository and if-artifact-changes concern the artifact repository only.
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
//     session-log repository, so a record never waits for them (dec-records-synchronous).
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
//   - A failed push does not fail the commit: the commit is in the local repository, and the
//     next push takes it along. The error is logged.
//
// The archive does not know the formats of the records. The meeting session decides the layout
// of a session (see package session), the records part the layout of decisions, minutes and
// notes (see package records).
package archive

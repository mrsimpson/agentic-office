// Package archive is the building block Archive (bb-archive in
// docs/architecture/05-building-blocks.arc42.md).
//
// It provides if-archive-storage (package storage): it stores the records of the parts of the
// hub as files in a local Git repository on the hub host and commits locally
// (dec-yaml-git-storage). Every Change is one commit. After a commit, the archive pushes it to a
// remote through a Pusher. The only Pusher for now is NoPush, which does nothing and reports
// success (issue #43 covers the real remote).
//
// How it works:
//
//   - Open creates the directory and runs "git init" if it is not a repository yet. The branch
//     is main. It uses the git command line tool that it finds in PATH when it opens.
//   - Commits are predictable: git runs without the system and global configuration of the user
//     of the hub (no hooks, no signing, no editor) and with the identity "office-hub".
//   - Only the hub writes the repository. Open removes a lock file that a hub left when it died
//     during a commit. On Linux, git stops when the hub dies.
//   - Commit carries out one change at a time. It writes every file to a temporary file and
//     renames it, then adds the files and commits them. A change that alters nothing creates no
//     commit. If the commit fails, the files stay in the work tree; the next change commits
//     them too.
//   - Read returns the files of a directory as they are in the current commit (git ls-tree and
//     git cat-file), not the work tree, so the files of a failed commit are not read. It does
//     not wait for a running change. The records of the hub use it to rebuild their index at
//     start.
//   - A failed push does not fail the commit: the commit is in the local repository, and the
//     next push takes it along. The error is logged.
//
// The archive does not know the formats of the records. The meeting session decides the layout
// of a session (see package session), the records part the layout of decisions, minutes and
// notes (see package records).
package archive

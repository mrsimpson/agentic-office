// Package storage is the archive storage (if-archive-storage): the in-process interface through
// which the parts of the hub store their records in the archive (dec-records-per-type,
// dec-yaml-git-storage).
//
// A record is a file in a repository (the hub repository or the repository of a tenant, see Archives). A Change writes one or more files and is recorded as one
// commit, so a reader of the repository sees all files of a change or none. The caller decides
// the paths and the content: the archive does not know what a session or a decision is.
package storage

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Archive stores files in a repository. An Archive is safe for use by many goroutines; it
// carries out one change at a time.
//
// A record is written synchronously, and it does not wait behind session writes: the session
// logs have their own repository and so their own queue (see Archives).
//
// A part of the hub reads its records back with Read, for example to rebuild its index when the
// hub starts.
type Archive interface {
	// Commit writes the files of c and records them as one commit with the message of c. It
	// returns the ID of the commit. A change with a stale Base of a file fails with a *ConflictError and
	// writes nothing. The same error follows when someone else moved the repository during the
	// commit and changed a file of this change; if they changed other files, Commit builds the
	// change on the new state and succeeds. A change that does not alter any file creates no commit and
	// returns the ID of the current commit. Commit fails if a path is not valid (see CheckPath).
	// Commit may take a long time; callers that must not wait call it in the background.
	Commit(ctx context.Context, c Change) (string, error)
	// Read returns the files under the directory dir as they are in the current commit, ordered
	// by path. dir is a valid path (see CheckPath); if it names a file, Read returns this file. A directory that is not in the current
	// commit, or a repository without commits, has no files. A change whose commit failed is
	// not visible.
	Read(ctx context.Context, dir string) ([]File, error)
	// List returns the paths of the files under the directory dir as they are in the current
	// commit, ordered, without reading their content. A directory that is not in the current
	// commit has no files. A caller that wants the content of a few files from a large
	// directory lists it first and reads them one by one.
	List(ctx context.Context, dir string) ([]string, error)
	// Names returns every path under the directory dir that ever lay in it in the history of
	// main, also the paths of files that were removed later. The paths are ordered and have no
	// duplicates; the content is not read. History is never rewritten, so a name that is in the
	// result stays in it. A part that numbers its files uses the highest number among the names
	// as the lowest value that its next number may be built on, so that it never gives a number
	// again that a removed file had. A directory that never held a file, or a repository without
	// commits, has no names. Names fails if dir is not a valid path (see CheckPath). The cost is
	// one walk through the history of the directory.
	Names(ctx context.Context, dir string) ([]string, error)
}

// Archives gives the archive of each repository (concept-multi-tenancy). The hub has one
// repository of its own, for the users, the tenants and the memberships. Each tenant has three
// repositories: one for its artifacts and records (Tenant), one for its session logs
// (SessionLogs) and one for what sessions have not released yet (Staging,
// dec-staging-repository). The session logs hold the original of sentences that are off the
// record (dec-off-the-record), so they never share a repository with the artifacts: only the
// meeting session uses the session-log repository, and no interface for participants reads it.
// Every member clones the artifact repository, so nothing that waits for a release lies there.
// Git access serves neither the session-log nor the staging repository. Every repository has its
// own commit queue, so a busy tenant does not hold up another tenant or the hub repository, and a
// session write does not hold up a record.
type Archives interface {
	// Hub returns the archive of the hub repository.
	Hub() Archive
	// CreateTenant creates the artifact, the session-log and the staging repository of a tenant.
	// It does nothing for a repository that exists. It fails if id is not a valid tenant ID
	// (see CheckTenantID).
	CreateTenant(ctx context.Context, id string) error
	// Tenant returns the archive of the artifact repository of a tenant: records, tasks,
	// slides and documents. It fails with ErrNoTenant if the repository was not created.
	Tenant(id string) (Archive, error)
	// SessionLogs returns the archive of the session-log repository of a tenant. Only the
	// meeting session uses it. It fails with ErrNoTenant if the repository was not created.
	SessionLogs(id string) (Archive, error)
	// Staging returns the staging repository of a tenant. It fails with ErrNoTenant if the
	// repository was not created.
	Staging(id string) (Staging, error)
}

// ErrNoTenant is the error of Archives.Tenant for a tenant without a repository.
var ErrNoTenant = errors.New("archive: the tenant has no repository")

// CheckTenantID reports whether id is a valid tenant ID: 1 to 64 letters, digits, ".", "_" or
// "-", starting with a letter or digit.
func CheckTenantID(id string) error {
	if id == "" || len(id) > 64 {
		return fmt.Errorf("tenant ID %q must have 1 to 64 characters", id)
	}
	for i, r := range id {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !letter && (i == 0 || (r != '.' && r != '_' && r != '-')) {
			return fmt.Errorf("tenant ID %q must be letters, digits, '.', '_' or '-', starting with a letter or digit", id)
		}
	}
	return nil
}

// Change is a set of files that is written as one commit.
type Change struct {
	// Message describes the change. It must not be empty.
	Message string
	// Author is who made the change (dec-artifacts: the identity of the participant is passed
	// through). Empty means the hub itself.
	Author Author
	Files  []File
}

// Author names the person or agent behind a change. Both fields are required when an Author is
// given.
type Author struct {
	Name  string
	Email string
}

// File is the whole new content of one file.
type File struct {
	// Path is relative to the root of the repository, with slashes (see CheckPath).
	Path string
	Data []byte
	// Version identifies the content of the file (a Git object ID). Read sets it; Commit
	// ignores it.
	Version string
	// Base is the version of the file that the author of a change read. Commit refuses the change
	// with a *ConflictError when the file in the current commit has another version. NoFile
	// means that the file must not exist yet. Empty means no check.
	Base string
	// Remove removes the file instead of writing it; Data is ignored. Removing a file that does
	// not exist changes nothing. Read never sets it.
	Remove bool
}

// NoFile is a Base that says the file must not exist yet.
const NoFile = "none"

// ConflictError is the error of a Commit whose Base of a file is not the version in the current
// commit, or whose file was changed by someone else during the commit: another change came first. The author reads the file again and tries again.
type ConflictError struct {
	Path string
	// Current is the version of the file in the current commit, or "" if the file does not exist.
	Current string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("archive: %s has changed since it was read (current version %q)", e.Path, e.Current)
}

// IsConflict reports whether err is a *ConflictError.
func IsConflict(err error) bool {
	var c *ConflictError
	return errors.As(err, &c)
}

// CheckPath reports whether p is a valid path of a file in the archive: relative, clean, with
// slashes, no element that starts with a dot (this excludes ".", ".." and ".git"), and no empty
// element.
func CheckPath(p string) error {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || strings.Contains(p, `\`) {
		return fmt.Errorf("archive path %q must be relative and clean, with slashes", p)
	}
	for _, el := range strings.Split(p, "/") {
		if el == "" || strings.HasPrefix(el, ".") {
			return fmt.Errorf("archive path %q has an element that is empty or starts with a dot", p)
		}
	}
	return nil
}

// Staging is the staging repository of a tenant (dec-staging-repository,
// concept-session-release). It has the same layout as the artifact repository: a staged file
// lies at the path that it gets in the artifact repository. It holds one area for each session
// that is not released. An area keeps the staged files of the session, the list of its readers
// and the state of its release. The archive reads no content of a staged file: it stores and moves
// the files as they are. Git access never serves this repository.
//
// A path belongs to at most one area. The stub of a staged file in the artifact repository is
// the business of the part that owns the type: it writes the file into the area first, so that the
// area knows the name, and then the stub. The release replaces the stubs.
//
// Staging is safe for use by many goroutines. Every method that changes the repository makes
// one commit, so a reader sees a change completely or not at all. Session IDs can be any
// non-empty text.
type Staging interface {
	// OpenArea creates the area of a session if it does not exist, and writes its readers and
	// its release state. It replaces the state that was there. The meeting session is the only
	// writer of the state. It fails with ErrAreaClosed when a release of the area runs.
	OpenArea(ctx context.Context, session string, s AreaState) error
	// Area returns the area of a session. It fails with ErrNoArea if the session has no area,
	// also after its release.
	Area(ctx context.Context, session string) (Area, error)
	// Areas returns every area, ordered by session ID. The meeting session uses it at start to
	// rebuild the deadlines and to complete a release that a restart cut off (Area.Release).
	Areas(ctx context.Context) ([]Area, error)
	// Commit writes and removes files in the area of a session (File.Remove) as one commit, as
	// Archive.Commit does, including the checks of Base and of the paths. The area remembers
	// every path of the change (Area.Names) and the author (Area.Authors). It fails with
	// ErrNoArea if the session has no area, with ErrAreaClosed when a release of the area runs,
	// and with a *ConflictError when a path holds a file of another area.
	Commit(ctx context.Context, session string, c Change) (string, error)
	// Read returns the files of the area of a session under the directory dir, as
	// Archive.Read does. A file of another area is not returned. It fails with ErrNoArea if the
	// session has no area.
	Read(ctx context.Context, session, dir string) ([]File, error)
	// List returns the paths of the files of the area of a session, ordered, without their
	// content. Removed files are not in the list. It fails with ErrNoArea if the session has no
	// area.
	List(ctx context.Context, session string) ([]string, error)
	// Release moves the area of a session into the artifact repository and returns the ID of
	// the release commit, or "" when the area has nothing to move. The steps:
	//
	//  1. It closes the area for writes and records who releases.
	//  2. It writes every file of the area into the artifact repository in one commit. Each
	//     file replaces the stub at its path; the stub of a name whose file was removed from
	//     the area goes away. The author of the commit is author, or the hub when author is
	//     empty. The authors of the area (Area.Authors) are named as co-authors, except the
	//     author of the commit. The message names the session (trailer "Office-Release").
	//  3. It removes the area from the staging repository.
	//
	// Every step is recorded in the staging repository. When a restart cut a release off,
	// Release completes it: a step that was done is never done again, and the author recorded
	// in step 1 stays, whatever author is given now. It fails with ErrNoArea if the session has
	// no area, also after a complete release.
	Release(ctx context.Context, session string, author Author) (string, error)
	// Released returns the release commit of a session from the history of main in the artifact
	// repository: the commit that names the session in its trailer "Office-Release". It fails
	// with ErrNotReleased if there is no such commit: the session is not released, its release
	// has not moved the area yet, or the area had nothing to move.
	Released(ctx context.Context, session string) (ReleaseCommit, error)
}

// ReleaseCommit is the commit that moved the area of a session into the artifact repository.
type ReleaseCommit struct {
	ID   string
	Time time.Time
	// Author is the author of the commit; empty means the hub.
	Author Author
	// CoAuthors are the authors that the commit names as co-authors, in their order.
	CoAuthors []Author
	// Paths are the files that the commit wrote, ordered. Removed stubs are not in the list.
	Paths []string
}

// ErrNotReleased is the error of Staging.Released for a session without a release commit.
var ErrNotReleased = errors.New("archive: the session has no release commit")

// AreaState is what the meeting session writes into the area of a session.
type AreaState struct {
	// Readers are the IDs of the participants who may read the area, ordered as given.
	Readers []string
	// Deadline is when the release of the session is due. Zero means that no deadline is set.
	Deadline time.Time
	// Held is true while an objection holds the release.
	Held bool
}

// Area is the area of one session in the staging repository.
type Area struct {
	// Session is the ID of the session.
	Session string
	AreaState
	// Names are the paths that ever lay in the area, also those of removed files, ordered. The
	// release removes the stub of a name whose file is gone.
	Names []string
	// Authors are the authors of the changes in the area, without duplicates, in the order of
	// their first change. Changes by the hub add no author.
	Authors []Author
	// Release is the state of a running release, or nil while no release runs.
	Release *ReleaseState
}

// ReleaseState is the state of a running release.
type ReleaseState struct {
	// Author is the author of the release commit; empty means the hub.
	Author Author
	// Commit is the ID of the release commit once it was built, before it lands in the
	// artifact repository; "" before.
	Commit string
}

// ErrNoArea is the error for a session without an area in the staging repository.
var ErrNoArea = errors.New("archive: the session has no staging area")

// ErrAreaClosed is the error for a write to an area whose release runs.
var ErrAreaClosed = errors.New("archive: the staging area is closed for its release")

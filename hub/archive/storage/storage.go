// Package storage is the archive storage (if-archive-storage): the in-process interface through
// which the parts of the hub store their records in the archive (dec-records-per-type,
// dec-yaml-git-storage).
//
// A record is a file in a repository. A Change writes one or more files and is recorded as one
// commit, so a reader of the repository sees all files of a change or none. The caller decides
// the paths and the content: the archive does not know what a session or a decision is.
package storage

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// Archive stores files in a repository. An Archive is safe for use by many goroutines; it
// carries out one change at a time.
//
// Records go first (dec-records-synchronous): when changes wait, the archive carries out every
// waiting change that is not Background before a waiting Background change. A change that is in
// progress is never interrupted. So a record waits at most for the one background change that is
// in progress, never for a queue of them.
//
// A part of the hub reads its records back with Read, for example to rebuild its index when the
// hub starts.
type Archive interface {
	// Commit writes the files of c and records them as one commit with the message of c. It
	// returns the ID of the commit. A change that does not alter any file creates no commit and
	// returns the ID of the current commit. Commit fails if a path is not valid (see CheckPath).
	// Commit may take a long time; callers that must not wait call it in the background and mark
	// the change as Background.
	Commit(ctx context.Context, c Change) (string, error)
	// Read returns the files under the directory dir as they are in the current commit, ordered
	// by path. dir is a valid path (see CheckPath). A directory that is not in the current
	// commit, or a repository without commits, has no files. Files of a change whose commit
	// failed are not returned.
	Read(ctx context.Context, dir string) ([]File, error)
}

// Change is a set of files that is written as one commit.
type Change struct {
	// Message describes the change. It must not be empty.
	Message string
	Files   []File
	// Background marks a change that no caller waits for, such as a write of session logs. A
	// waiting Background change goes after every waiting change that is not Background.
	Background bool
}

// File is the whole new content of one file.
type File struct {
	// Path is relative to the root of the repository, with slashes (see CheckPath).
	Path string
	Data []byte
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

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
	// by path. dir is a valid path (see CheckPath); if it names a file, Read returns this file. A directory that is not in the current
	// commit, or a repository without commits, has no files. Files of a change whose commit
	// failed are not returned.
	Read(ctx context.Context, dir string) ([]File, error)
	// List returns the paths of the files under the directory dir as they are in the current
	// commit, ordered, without reading their content. A directory that is not in the current
	// commit has no files. A caller that wants the content of a few files from a large
	// directory lists it first and reads them one by one.
	List(ctx context.Context, dir string) ([]string, error)
}

// Archives gives the archive of each repository (concept-multi-tenancy). The hub has one
// repository of its own, for the users, the tenants and the memberships, and each tenant has one
// repository for its content. Every repository has its own commit queue, so a busy tenant does
// not hold up another tenant or the hub repository.
type Archives interface {
	// Hub returns the archive of the hub repository.
	Hub() Archive
	// CreateTenant creates the repository of a tenant. It does nothing if the repository exists.
	// It fails if id is not a valid tenant ID (see CheckTenantID).
	CreateTenant(ctx context.Context, id string) error
	// Tenant returns the archive of the repository of a tenant. It fails with ErrNoTenant if
	// the repository was not created.
	Tenant(id string) (Archive, error)
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

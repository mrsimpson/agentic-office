// Package watch is the interface if-archive-watch of the building block Archive
// (docs/architecture/05-building-blocks.arc42.md). It tells the parts of the hub that keep files of
// the archive in memory (records, tasks, slides) which artifacts changed on the branch main of the
// artifact repository of their tenant, after a commit of the archive and after a push
// (dec-artifact-sync, risk-stale-memory).
//
// The interface is in-process and needs no token. A part calls Watcher.Watch with the directories
// it cares about and gets a Subscription. The archive implements Watcher for each tenant (the
// type archive.Repo).
//
// The changes are the same that if-artifact-changes reports to a participant: the artifacts
// (Markdown and YAML files) that were created, changed or removed, with the version of the new
// content (the object ID of the content, the same version as in if-archive-read). The archive
// uses one path for both interfaces: a watcher is a follower of the changes of the tenant, with
// its own buffer.
//
// How it works:
//
//   - Watch registers the watcher before it returns. A part that watches first and reads its
//     files afterwards loses no change: a change that comes in between is reported, and
//     reading a file again gives the same result.
//   - A change names a path and what happened to it. It holds no content and no position. There
//     is no cursor and no replay. A part reads the files that changed from the archive
//     (if-archive-storage) when it gets the report.
//   - Without directories, a watcher gets every artifact of the tenant. With directories, it
//     gets the artifacts in them (a path equal to a directory or below it).
//   - The commit path never waits for a watcher. A watcher that does not read fast enough is
//     dropped, as a follower of the stream is: its channel of changes closes and Err returns
//     ErrDropped. The part then watches again and reads all its files, because it may have missed
//     changes. A slow watcher never holds up a commit or another watcher.
//   - Close ends a subscription that is no longer needed. It is safe to call more than once.
package watch

import "errors"

// Kind tells what happened to an artifact.
type Kind int

const (
	// Created means the artifact is new.
	Created Kind = iota + 1
	// Changed means the artifact has new content.
	Changed
	// Removed means the artifact is gone.
	Removed
)

// String returns the name of the kind.
func (k Kind) String() string {
	switch k {
	case Created:
		return "created"
	case Changed:
		return "changed"
	case Removed:
		return "removed"
	}
	return "unknown"
}

// Change is one artifact that changed.
type Change struct {
	// Path is the path of the file in the artifact repository, for example "notes/000003.yaml".
	Path string
	Kind Kind
	// Version is the object ID of the new content. It is empty for a removed artifact.
	Version string
}

// ErrDropped is the error of a subscription whose watcher did not read fast enough. The part
// watches again and reads its files again.
var ErrDropped = errors.New("watch: the watcher did not read fast enough and was dropped")

// Watcher gives the changes of the artifact repository of one tenant.
type Watcher interface {
	// Watch registers a watcher for the artifacts below the directories, or for all artifacts
	// when there is no directory. It fails if a directory is not a valid archive path. The
	// watcher is registered when Watch returns.
	Watch(dirs ...string) (Subscription, error)
}

// Subscription is the stream of changes of one watcher.
type Subscription interface {
	// Changes returns the channel of changes. Each message lists the changes of one commit or
	// push, ordered by path, at least one entry. The channel closes when the subscription ends:
	// ask Err why.
	Changes() <-chan []Change
	// Err returns why the channel of changes closed: ErrDropped for a watcher that was too
	// slow, nil after Close. It returns nil while the channel is open.
	Err() error
	// Close ends the subscription. A watcher that is blocked on a full buffer is freed.
	Close()
}

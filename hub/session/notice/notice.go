// Package notice is the interface through which the parts of the hub that write artifacts tell a
// meeting session about it (if-session-notice, dec-artifacts). It is an in-process interface.
package notice

import "context"

// Notifier appends artifact events to the log of a session. The meeting session implements it.
type Notifier interface {
	// NoteArtifact appends artifact.created (created is true) or artifact.changed to the log of
	// the session. ctx carries the access token of the participant who wrote the artifact; the
	// participant must have joined the session. It is idempotent by commandID.
	NoteArtifact(ctx context.Context, sessionID, participantID, commandID string, created bool, path, version string) error
}

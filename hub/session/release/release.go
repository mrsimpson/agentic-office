// Package release is the release status of sessions (if-session-release,
// concept-session-release). It is an in-process interface of the meeting session.
//
// The parts of the hub that write artifacts for a session ask it two things: whether the session
// is released, which decides where a write goes (the staging area of the session, or the
// artifact repository), and whether the caller took part in the session, because only a
// participant may name a session in a write.
package release

import (
	"context"
	"errors"
	"time"
)

// Status is where a session is on its way to the release.
type Status int

const (
	// Open: the session runs. What it creates waits in its staging area.
	Open Status = iota + 1
	// Waiting: the session has ended and waits for its release until State.Deadline.
	Waiting
	// Held: a human objected. Only a human releases the session now.
	Held
	// Released: the session is released. What it staged moves, or has moved, into the artifact
	// repository, and new writes go there too.
	Released
)

func (s Status) String() string {
	switch s {
	case Open:
		return "open"
	case Waiting:
		return "waiting"
	case Held:
		return "held"
	case Released:
		return "released"
	}
	return "unknown"
}

// State is the release status of one session.
type State struct {
	Status Status
	// Deadline is when the hub releases the session, while the status is Waiting. It can be zero
	// while the hub has no deadline for the session, for example during a retry.
	Deadline time.Time
}

// ErrNoSession is the error of Checker.State for a session that has no event.
var ErrNoSession = errors.New("release: no such session")

// Checker answers the release questions about a session. The meeting session implements it. It
// is safe for use by many goroutines. A session that has left memory is loaded on demand.
type Checker interface {
	// State returns the release status of a session. Every session that exists has one, also a
	// session that staged nothing. It fails with ErrNoSession if the session does not exist.
	State(ctx context.Context, sessionID string) (State, error)
	// TookPart reports whether a participant took part in a session: it joined the session at
	// any time, or it imported the session. A session that does not exist has no participant.
	TookPart(ctx context.Context, sessionID, participantID string) (bool, error)
}

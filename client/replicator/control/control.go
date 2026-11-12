// Package control is the replication control (if-replication): the in-process interface to start
// the replication of a session for a participant, to find a running replicator and to stop it.
//
// The log replicator (client/replicator) implements Replicator. The wiring of the participant
// client hands a New function to the parts that start a replication: the CLI runs the replicator
// as its own process, the MCP server and the agent runner run it inside their own process.
package control

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
)

// Options say which session a replicator copies into which file.
type Options struct {
	// Stream is a session stream client with a valid token source (auth.Bearer). The replicator
	// subscribes through it.
	Stream sessionstreamv1connect.SessionStreamServiceClient
	// Commands is a session commands client with a valid token source. The replicator offers
	// its copy through it after the hub lost events, and checks the tree head of the hub.
	Commands sessioncommandsv1connect.SessionCommandsServiceClient
	// SessionID is the session to copy.
	SessionID string
	// ParticipantID is the participant the file belongs to. The replicator stops after it
	// has written that participant's participant.left. Empty: stop only on session.ended.
	ParticipantID string
	// Path is the local session log.
	Path string
	// TreeHeadPath is the file with the latest signed tree head over the local log
	// (dec-restore-from-peers). Empty means locallog.TreeHeadPath(Path).
	TreeHeadPath string
	// Logf receives status messages. Nil means no messages.
	Logf func(format string, args ...any)
	// OnWrite is called after every written line. Optional.
	OnWrite func(*sessionstreamv1.Event)
	// MaxBackoff overrides the longest wait between two reconnects when it is greater than
	// zero.
	MaxBackoff time.Duration
}

// Replicator copies the session stream of one session into one local file.
type Replicator interface {
	// Open opens or creates the file and takes an exclusive lock on it. It fails with
	// ErrRunning if another replicator has the lock.
	Open() error
	// Run copies the stream into the file until the session ends, the participant leaves or
	// ctx ends. It fails with ErrFork if the local log and the log of the hub differ.
	Run(ctx context.Context) (Reason, error)
	// Cursor returns the ID of the last event in the file.
	Cursor() uint32
	// Close releases the file and its lock.
	Close() error
}

// New returns a replicator for the options. It does not open the file yet.
type New func(Options) Replicator

// Reason tells why Run stopped.
type Reason string

const (
	// Ended: the replicator wrote session.ended.
	Ended Reason = "session.ended"
	// Left: the replicator wrote participant.left of its own participant.
	Left Reason = "participant.left"
	// Canceled: the context ended.
	Canceled Reason = "canceled"
)

// ErrRunning means that another replicator holds the lock of the file.
var ErrRunning = errors.New("a replicator is already running for this file")

// ErrFork means that the log of the hub and the local log differ: the local copy can no longer
// be continued.
var ErrFork = errors.New("the local log and the log of the hub differ")

// Running reports whether a replicator holds the lock of the file at path. A replicator holds an
// exclusive lock (flock) on its file while it runs.
func Running(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

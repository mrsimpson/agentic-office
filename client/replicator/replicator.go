package replicator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"

	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/events"
	"github.com/mrsimpson/agentic-office/hub/session/api/logfile"
	"github.com/mrsimpson/agentic-office/hub/session/api/merkle"
	sessionv1connect "github.com/mrsimpson/agentic-office/hub/session/api/sessionv1connect"
)

// ErrRunning means that another replicator holds the lock of the file.
var ErrRunning = errors.New("a replicator is already running for this file")

// Reconnect backoff after a drop. The wait starts at MinBackoff and doubles up to MaxBackoff.
// MaxBackoff bounds how long the local log lags behind after the network is back
// (qs-resume-after-drop asks for 2 s), so keep it well below that.
const (
	MinBackoff = 100 * time.Millisecond
	MaxBackoff = time.Second
)

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

// Replicator copies the session stream of one session into one local file.
type Replicator struct {
	// Session is a session client with a valid token source (auth.Bearer).
	Session sessionv1connect.SessionServiceClient
	// SessionID is the session to copy.
	SessionID string
	// ParticipantID is the participant the file belongs to. The replicator stops after it
	// has written that participant's participant.left. Empty: stop only on session.ended.
	ParticipantID string
	// Path is the local session log.
	Path string
	// TreeHeadPath is the file with the latest signed tree head over the local log
	// (dec-restore-from-peers). Empty means Path with the extension replaced by
	// ".tree-head.yaml".
	TreeHeadPath string
	// Logf receives status messages. Nil means no messages.
	Logf func(format string, args ...any)
	// OnWrite is called after every written line. Optional.
	OnWrite func(*sessionv1.Event)
	// MaxBackoff overrides the package MaxBackoff when it is greater than zero.
	MaxBackoff time.Duration

	f      *os.File
	cursor uint32
	ended  bool
	// head is the latest signed tree head of the hub over the local log, or nil.
	head *sessionv1.SignedTreeHead
	// behind is set when the hub did not know the cursor: the hub lost events. Before the next
	// subscribe the replicator offers its copy and checks that the hub's log still matches.
	behind bool
}

// ErrFork means that the log of the hub and the local log differ: the local copy can no longer
// be continued.
var ErrFork = errors.New("the local log and the log of the hub differ")

// treeHeadPath returns the file of the tree head.
func (r *Replicator) treeHeadPath() string {
	if r.TreeHeadPath != "" {
		return r.TreeHeadPath
	}
	return strings.TrimSuffix(r.Path, filepath.Ext(r.Path)) + ".tree-head.yaml"
}

// Open opens or creates the file (mode 0600, directory 0700) and takes an exclusive lock on it.
// It cuts an incomplete last line (a crash during a write) and reads the cursor from the last
// complete line. It fails with ErrRunning if another replicator has the lock.
func (r *Replicator) Open() error {
	if r.Logf == nil {
		r.Logf = func(string, ...any) {}
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(r.Path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrRunning
		}
		return fmt.Errorf("lock %s: %w", r.Path, err)
	}
	if err := r.load(f); err != nil {
		f.Close()
		return err
	}
	r.f = f
	r.loadHead()
	return nil
}

// loadHead reads the stored tree head. A head that does not fit the local log is ignored.
func (r *Replicator) loadHead() {
	data, err := os.ReadFile(r.treeHeadPath())
	if err != nil {
		return
	}
	h, err := logfile.UnmarshalTreeHead(data)
	if err != nil || h.GetSessionId() != r.SessionID || h.GetTreeSize() > r.cursor {
		r.Logf("replicator: ignoring the stored tree head %s", r.treeHeadPath())
		return
	}
	r.head = h
}

// writeHead stores the tree head next to the log: a new file that replaces the old one, so the
// file always holds a whole head.
func (r *Replicator) writeHead(h *sessionv1.SignedTreeHead) error {
	data, err := logfile.MarshalTreeHead(h)
	if err != nil {
		return err
	}
	p := r.treeHeadPath()
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	r.head = h
	return nil
}

func (r *Replicator) load(f *os.File) error {
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if n := len(data); n > 0 && data[n-1] != '\n' {
		keep := bytes.LastIndexByte(data, '\n') + 1
		r.Logf("replicator: cutting incomplete last line (%d bytes)", n-keep)
		if err := f.Truncate(int64(keep)); err != nil {
			return err
		}
		data = data[:keep]
	}
	if len(data) > 0 {
		body := data[:len(data)-1]
		last := body[bytes.LastIndexByte(body, '\n')+1:]
		ev, err := logfile.UnmarshalLine(last)
		if err != nil {
			return fmt.Errorf("%s: read cursor from last line: %w", r.Path, err)
		}
		r.cursor = ev.GetId()
		r.ended = ev.GetType() == events.SessionEnded
	}
	_, err = f.Seek(0, io.SeekEnd)
	return err
}

// Cursor returns the ID of the last event in the file.
func (r *Replicator) Cursor() uint32 { return r.cursor }

// Close releases the file and its lock.
func (r *Replicator) Close() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// fatal tells errors after which a reconnect cannot help.
func fatal(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument, connect.CodePermissionDenied, connect.CodeFailedPrecondition:
		return true
	}
	return errors.Is(err, ErrFork)
}

// Run follows the stream until it has written session.ended or the participant's own
// participant.left, or ctx ends. After a drop it reconnects from the cursor with a backoff.
//
// If the hub does not know the cursor (out_of_range), the hub lost events, for example in a
// restart (dec-restore-from-peers). The replicator then offers the events the hub misses, with
// the signed tree head it holds over them, and retries until the hub has the cursor again (it
// or another peer filled in). Before it follows the stream again, it checks that the log of the
// hub up to the cursor has the root hash of the local log.
//
// Run returns an error only for a problem a reconnect cannot solve, for example a local log
// that differs from the log of the hub (ErrFork), or a copy the hub rejects. Run closes the file
// when it returns.
func (r *Replicator) Run(ctx context.Context) (Reason, error) {
	defer r.Close()
	if r.ended {
		return Ended, nil
	}
	maxBackoff := MaxBackoff
	if r.MaxBackoff > 0 {
		maxBackoff = r.MaxBackoff
	}
	backoff := min(MinBackoff, maxBackoff)
	for {
		var got bool
		var reason Reason
		var err error
		if r.behind {
			err = r.restore(ctx)
		}
		if err == nil {
			got, reason, err = r.follow(ctx)
		}
		if connect.CodeOf(err) == connect.CodeOutOfRange && !r.behind {
			r.behind = true
			r.Logf("replicator: the hub does not know cursor %d; offering the local copy", r.cursor)
			continue
		}
		if reason != "" {
			return reason, nil
		}
		if ctx.Err() != nil {
			return Canceled, nil
		}
		if fatal(err) {
			return "", fmt.Errorf("session stream: %w", err)
		}
		if got {
			backoff = min(MinBackoff, maxBackoff)
		}
		// Up to 20 % less, so that many clients after the same drop do not retry in step.
		wait := backoff - rand.N(backoff/5+1)
		if connect.CodeOf(err) == connect.CodeUnauthenticated {
			wait = 0 // the token was dropped; authenticate again at once
		}
		r.Logf("replicator: stream lost (%v), reconnect from cursor %d in %v", err, r.cursor, wait)
		select {
		case <-ctx.Done():
			return Canceled, nil
		case <-time.After(wait):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (r *Replicator) follow(ctx context.Context) (got bool, reason Reason, err error) {
	stream, err := r.Session.Subscribe(ctx, &sessionv1.SubscribeRequest{SessionId: r.SessionID, AfterEventId: r.cursor})
	if err != nil {
		return false, "", err
	}
	defer stream.Close()
	for {
		res, err := stream.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = errors.New("stream closed before session.ended")
			}
			return got, "", err
		}
		got = true
		ev := res.GetEvent()
		if ev.GetId() <= r.cursor {
			continue // already in the file
		}
		if ev.GetId() != r.cursor+1 {
			return got, "", fmt.Errorf("gap in stream: have %d, got %d", r.cursor, ev.GetId())
		}
		if err := r.write(ev); err != nil {
			return got, "", err
		}
		if h := res.GetTreeHead(); h != nil && h.GetTreeSize() == ev.GetId() && h.GetSessionId() == r.SessionID {
			if err := r.writeHead(h); err != nil {
				return got, "", err
			}
		}
		switch {
		case ev.GetType() == events.SessionEnded:
			return got, Ended, nil
		case r.ParticipantID != "" && ev.GetType() == events.ParticipantLeft &&
			ev.GetParticipantLeft().GetParticipantId() == r.ParticipantID:
			return got, Left, nil
		}
	}
}

// write appends one line with a single write call, so a crash leaves at most one torn line.
func (r *Replicator) write(ev *sessionv1.Event) error {
	line, err := logfile.MarshalLine(ev)
	if err != nil {
		return err
	}
	if _, err := r.f.Write(line); err != nil {
		return err
	}
	r.cursor = ev.GetId()
	if r.OnWrite != nil {
		r.OnWrite(ev)
	}
	return nil
}

// Running reports whether a replicator holds the lock of the file at path.
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

// errBehind means that the hub has fewer events than the local log and the local copy cannot fill
// them in (yet). The replicator retries: another peer may fill them in.
var errBehind = errors.New("the hub has fewer events than the local log")

// restore runs while the hub is behind the local log. It offers the events the hub misses, if
// the stored tree head covers them, and then checks that the log of the hub up to the cursor is
// the local log. It returns nil when the replicator can follow the stream again.
func (r *Replicator) restore(ctx context.Context) error {
	res, err := r.Session.GetTreeHead(ctx, &sessionv1.GetTreeHeadRequest{SessionId: r.SessionID})
	if err != nil {
		return err
	}
	n := res.GetTreeHead().GetTreeSize()
	if n < r.cursor {
		if r.head == nil || r.head.GetTreeSize() <= n {
			r.Logf("replicator: the hub has %d events, the local log %d, and no signed tree head covers the missing ones; waiting for another peer",
				n, r.cursor)
			return errBehind
		}
		evs, err := r.localEvents()
		if err != nil {
			return err
		}
		size := r.head.GetTreeSize()
		offer, err := r.Session.OfferLog(ctx, &sessionv1.OfferLogRequest{SessionId: r.SessionID, AfterEventId: n,
			Events: evs[n:size], TreeHead: r.head})
		if err != nil {
			return fmt.Errorf("offer the local copy: %w", err)
		}
		r.Logf("replicator: offered the events %d to %d: accepted %v, the hub has %d events",
			n+1, size, offer.GetAccepted(), offer.GetTreeHead().GetTreeSize())
		if offer.GetTreeHead().GetTreeSize() < r.cursor {
			return errBehind
		}
	}
	// The hub has the cursor again. Its log up to the cursor must be the local log.
	res, err = r.Session.GetTreeHead(ctx, &sessionv1.GetTreeHeadRequest{SessionId: r.SessionID, TreeSize: r.cursor})
	if err != nil {
		return err
	}
	evs, err := r.localEvents()
	if err != nil {
		return err
	}
	leaves, err := merkle.LeafHashes(r.SessionID, evs)
	if err != nil {
		return err
	}
	if !bytes.Equal(merkle.RootHash(leaves), res.GetTreeHead().GetRootHash()) {
		return fmt.Errorf("%w: the root hashes over the events 1 to %d differ", ErrFork, r.cursor)
	}
	r.behind = false
	r.Logf("replicator: the hub has the local log again; following from cursor %d", r.cursor)
	return nil
}

// localEvents reads the whole local log.
func (r *Replicator) localEvents() ([]*sessionv1.Event, error) {
	data, err := os.ReadFile(r.Path)
	if err != nil {
		return nil, err
	}
	evs, err := logfile.UnmarshalLines(data)
	if err != nil {
		return nil, err
	}
	if len(evs) != int(r.cursor) {
		return nil, fmt.Errorf("%s has %d events, the cursor is %d", r.Path, len(evs), r.cursor)
	}
	return evs, nil
}

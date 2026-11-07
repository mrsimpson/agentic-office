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
	"syscall"
	"time"

	"connectrpc.com/connect/v2"

	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/events"
	"github.com/mrsimpson/agentic-office/hub/session/api/logfile"
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
	// Logf receives status messages. Nil means no messages.
	Logf func(format string, args ...any)
	// OnWrite is called after every written line. Optional.
	OnWrite func(*sessionv1.Event)
	// MaxBackoff overrides the package MaxBackoff when it is greater than zero.
	MaxBackoff time.Duration

	f      *os.File
	cursor uint32
	ended  bool
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
	case connect.CodeInvalidArgument, connect.CodePermissionDenied, connect.CodeOutOfRange:
		return true
	}
	return false
}

// Run follows the stream until it has written session.ended or the participant's own
// participant.left, or ctx ends. After a drop it reconnects from the cursor with a backoff.
// It returns an error only for a problem a reconnect cannot solve, for example a cursor the
// server does not know (out_of_range, after a restart of a server without persistence).
// Run closes the file when it returns.
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
		got, reason, err := r.follow(ctx)
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

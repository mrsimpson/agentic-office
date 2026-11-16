package sync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/client/sync/control"
	changesv1 "github.com/mrsimpson/agentic-office/hub/archive/changes/api"
	"github.com/mrsimpson/agentic-office/hub/archive/changes/api/changesv1connect"
)

// Follow implements control.Sync.
func (s *syncer) Follow(ctx context.Context, dir string, o control.FollowOptions) error {
	git, err := s.gitPath()
	if err != nil {
		return err
	}
	abs, err := s.mainDirectory(git, dir)
	if err != nil {
		return err
	}
	if o.FirstPause <= 0 {
		o.FirstPause = time.Second
	}
	if o.MaxPause <= 0 {
		o.MaxPause = 30 * time.Second
	}
	if o.MaxPause < o.FirstPause {
		o.MaxPause = o.FirstPause
	}
	f := &follower{s: s, git: git, dir: abs, o: o, pause: o.FirstPause}
	for {
		err := f.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		switch connect.CodeOf(err) {
		case connect.CodePermissionDenied, connect.CodeNotFound, connect.CodeInvalidArgument:
			return fmt.Errorf("the hub refuses to tell you about changes (are you a member of the tenant?): %w", err)
		}
		s.logf("the connection to the hub ended (%v); connecting again in %s", err, f.pause)
		if !sleep(ctx, f.pause) {
			return nil
		}
		f.pause = min(f.pause*2, o.MaxPause)
	}
}

// follower holds the state of one Follow call across connections.
type follower struct {
	s     *syncer
	git   string
	dir   string
	o     control.FollowOptions
	pause time.Duration
}

// session is one connection: connect, wait for the registered signal, update, then update after
// each event. It returns the reason that the connection ended.
func (f *follower) session(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := changesv1connect.NewArchiveChangesServiceClient(f.s.opts.Client.Connection())
	st, err := client.Follow(ctx, &changesv1.FollowRequest{PathPrefixes: f.o.PathPrefixes})
	if err != nil {
		return err
	}
	first, err := st.Receive()
	if err != nil {
		return err
	}
	if first.GetRegistered() == nil {
		return errors.New("the hub did not start with the registered signal")
	}
	f.s.logf("following the changes of the hub")
	f.pause = f.o.FirstPause

	// A goroutine reads the events all the time, also while an update runs, so the hub never sees
	// a slow follower. Events that arrive during an update are one wish for one more update.
	wake := make(chan struct{}, 1)
	ended := make(chan error, 1)
	go func() {
		for {
			msg, err := st.Receive()
			if err != nil {
				ended <- err
				return
			}
			if msg.GetChanges() != nil {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
		}
	}()

	// The update comes after the registration: a change before it is in the fetch, a change after it
	// is an event.
	f.updateUntilDone(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-ended:
			if err == nil {
				err = errors.New("the hub ended the stream")
			}
			return err
		case <-wake:
			f.updateUntilDone(ctx)
		}
	}
}

// updateUntilDone updates once and reports the result. If the hub cannot be reached, it tries
// again after a pause that grows, until it works, the participant has to act, or ctx ends.
func (f *follower) updateUntilDone(ctx context.Context) {
	pause := f.o.FirstPause
	for ctx.Err() == nil {
		res, err := f.s.update(ctx, f.git, f.dir)
		if ctx.Err() != nil {
			return
		}
		if f.o.Updated != nil {
			f.o.Updated(res, err)
		}
		if !errors.Is(err, control.ErrFetch) {
			return
		}
		if !sleep(ctx, pause) {
			return
		}
		pause = min(pause*2, f.o.MaxPause)
	}
}

// sleep waits for d. It returns false if ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

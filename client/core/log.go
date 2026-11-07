package core

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/mrsimpson/agentic-office/client/replicator/locallog"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
)

// ReadLog reads all complete lines of a local session log. A missing file is an empty log.
func ReadLog(path string) ([]*sessionv1.Event, error) {
	var out []*sessionv1.Event
	err := FollowLog(context.Background(), path, false, func(ev *sessionv1.Event) error {
		out = append(out, ev)
		return nil
	})
	return out, err
}

// ErrStop can be returned by the function of FollowLog to stop without an error.
var ErrStop = errors.New("stop")

// FollowLog calls fn for every complete line of the local session log, in order. With follow,
// it waits for new lines until ctx ends or fn returns an error; it also waits for the file to
// appear. Without follow, it stops at the end of the file. ErrStop from fn ends it with nil.
func FollowLog(ctx context.Context, path string, follow bool, fn func(*sessionv1.Event) error) error {
	var f *os.File
	for {
		var err error
		f, err = os.Open(path)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if !follow {
			return nil
		}
		if err := sleep(ctx, 50*time.Millisecond); err != nil {
			return nil
		}
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var partial []byte
	for {
		chunk, err := r.ReadBytes('\n')
		partial = append(partial, chunk...)
		if err == nil {
			ev, perr := locallog.UnmarshalLine(partial)
			partial = partial[:0]
			if perr != nil {
				return perr
			}
			if ferr := fn(ev); ferr != nil {
				if errors.Is(ferr, ErrStop) {
					return nil
				}
				return ferr
			}
			continue
		}
		if !errors.Is(err, io.EOF) {
			return err
		}
		if !follow {
			return nil
		}
		// A torn last line can be cut by the replicator. If the file got shorter than what we
		// have read, drop the partial line and read on from the new end.
		if len(partial) > 0 {
			if pos, err := f.Seek(0, io.SeekCurrent); err == nil {
				if fi, err := f.Stat(); err == nil && fi.Size() < pos {
					if _, err := f.Seek(fi.Size(), io.SeekStart); err == nil {
						r.Reset(f)
						partial = bytes.Clone(partial[:0])
					}
				}
			}
		}
		if err := sleep(ctx, 50*time.Millisecond); err != nil {
			return nil
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Package locallog is the local session log (if-local-log): how a participant reads the file that
// the log replicator writes.
//
// The file is append-only, with one line per event in the line form of the session stream
// (package logfile of the session stream): compact canonical JSON with the dotted type and the
// event ID as a number. Only one replicator writes a file at a time; it holds an exclusive lock
// (flock) on it while it runs. A crash can leave an incomplete last line; the replicator cuts it
// when it opens the file again, and a reader skips it.
//
// Next to the log, the replicator keeps the latest tree head that the hub signed over it
// (TreeHeadPath).
package locallog

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/logfile"
)

// Read reads all complete lines of a local session log. A missing file is an empty log.
func Read(path string) ([]*sessionstreamv1.Event, error) {
	var out []*sessionstreamv1.Event
	err := Follow(context.Background(), path, false, func(ev *sessionstreamv1.Event) error {
		out = append(out, ev)
		return nil
	})
	return out, err
}

// ErrStop can be returned by the function of Follow to stop without an error.
var ErrStop = errors.New("stop")

// Follow calls fn for every complete line of the local session log, in order. With follow,
// it waits for new lines until ctx ends or fn returns an error; it also waits for the file to
// appear. Without follow, it stops at the end of the file. ErrStop from fn ends it with nil.
func Follow(ctx context.Context, path string, follow bool, fn func(*sessionstreamv1.Event) error) error {
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
			ev, perr := logfile.UnmarshalLine(partial)
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

// TreeHeadPath returns the file next to the log at logPath that holds the latest signed tree head
// over the log: the log path with its extension replaced by ".tree-head.yaml". The file is YAML in
// the form of package logfile of the session stream.
func TreeHeadPath(logPath string) string {
	return strings.TrimSuffix(logPath, filepath.Ext(logPath)) + ".tree-head.yaml"
}

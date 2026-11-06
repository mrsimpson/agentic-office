// Package replicator is the spike of the log replicator (bb-replicator).
// It follows the session stream and appends every event as one JSON line to a local file.
// The last line of the file is the cursor (concept-event-log).
package replicator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	officev1 "github.com/mrsimpson/agentic-office/gen/office/v1"
	"github.com/mrsimpson/agentic-office/gen/office/v1/officev1connect"
	"github.com/mrsimpson/agentic-office/internal/metrics"
)

// Path returns the local log path for a session.
func Path(dir, session string) string { return filepath.Join(dir, session+".jsonl") }

// Replicator copies one session log into one local file.
type Replicator struct {
	Client  officev1connect.SessionServiceClient
	Session string
	Path    string
	Logf    func(format string, args ...any)

	f      *os.File
	cursor uint64
}

// Open creates the file (mode 0600) if needed and reads the cursor from its last line.
// If the last line is incomplete (a crash during a write), it is cut off.
func (r *Replicator) Open() error {
	if r.Logf == nil {
		r.Logf = log.Printf
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(r.Path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
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
	if lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")); len(data) > 0 {
		var ev officev1.Event
		if err := protojson.Unmarshal(lines[len(lines)-1], &ev); err != nil {
			return fmt.Errorf("read cursor from last line: %w", err)
		}
		r.cursor = ev.GetId()
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	r.f = f
	return nil
}

// Cursor returns the ID of the last event in the file.
func (r *Replicator) Cursor() uint64 { return r.cursor }

// Run follows the stream until ctx ends. After a drop it reconnects from the cursor.
func (r *Replicator) Run(ctx context.Context) error {
	defer r.f.Close()
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		got, err := r.follow(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if got {
			backoff = 100 * time.Millisecond
		}
		r.Logf("replicator: stream lost (%v), reconnect from cursor %d in %v", err, r.cursor, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 2*time.Second)
	}
	return nil
}

func (r *Replicator) follow(ctx context.Context) (bool, error) {
	stream, err := r.Client.Subscribe(ctx, &officev1.SubscribeRequest{SessionId: r.Session, AfterEventId: r.cursor})
	if err != nil {
		return false, err
	}
	defer stream.Close()
	got := false
	for {
		res, err := stream.Receive()
		if err != nil {
			return got, err
		}
		got = true
		ev := res.GetEvent()
		if ev.GetId() <= r.cursor {
			continue // already in the file; should not happen
		}
		if ev.GetId() != r.cursor+1 {
			return got, fmt.Errorf("gap in stream: have %d, got %d", r.cursor, ev.GetId())
		}
		if err := r.write(ev); err != nil {
			return got, err
		}
	}
}

func (r *Replicator) write(ev *officev1.Event) error {
	raw, err := protojson.Marshal(ev)
	if err != nil {
		return err
	}
	// protojson adds random spaces on purpose. Compact the line so that lines are stable.
	var line bytes.Buffer
	if err := json.Compact(&line, raw); err != nil {
		return err
	}
	line.WriteByte('\n')
	if _, err := r.f.Write(line.Bytes()); err != nil { // one write per line, O_APPEND not needed: single writer
		return err
	}
	r.cursor = ev.GetId()
	metrics.Emit("replica_write", map[string]any{"event_id": ev.GetId(), "type": ev.GetType()})
	return nil
}

package cli

// Joining a meeting in a set-up directory (dec-artifact-sync): the replicator process also follows
// the changes of the clone, for as long as it runs. The replicator process starts in the directory
// of "office meeting join", so it finds the clone from its working directory, also from a
// worktree of the clone. A directory that is no set-up clone starts nothing and prints nothing.
// A failure never stops the replication: it is written to the log of the replicator, once.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/sync/control"
)

// startCloneFollow starts the follow in the background. The returned function waits until it has
// ended; call it after ctx ended.
func startCloneFollow(ctx context.Context, c coreapi.Client, lg *log.Logger) func() {
	if NewSync == nil {
		return func() {}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		followClone(ctx, c, lg)
	}()
	return wg.Wait
}

func followClone(ctx context.Context, c coreapi.Client, lg *log.Logger) {
	r := &onceReporter{lg: lg}
	s := NewSync(control.Options{Client: c, Logf: r.logf})
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	dir, err := s.MainDirectory(ctx, wd)
	if err != nil {
		// No set-up clone here (or no Git to tell): nothing to do and nothing to say.
		if !errors.Is(err, control.ErrNotSetUp) && !errors.Is(err, control.ErrNoGit) {
			r.report("artifact sync: " + err.Error())
		}
		return
	}
	lg.Printf("artifact sync: following the changes of %s", dir)
	err = s.Follow(ctx, dir, control.FollowOptions{Updated: func(res control.UpdateResult, err error) {
		if err != nil {
			r.report("artifact sync: update of " + dir + " failed: " + err.Error())
			return
		}
		r.ok()
		if res.Updated {
			lg.Printf("artifact sync: updated %s: main moved from %s to %s", dir, short(res.From), short(res.To))
		}
	}})
	if err != nil {
		r.report("artifact sync stopped: " + err.Error())
	}
}

// onceReporter writes a problem once: the same text or the same kind of status message is not
// repeated until something else was written or an update worked.
type onceReporter struct {
	lg   *log.Logger
	mu   sync.Mutex
	last string
}

func (r *onceReporter) write(key, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == r.last {
		return
	}
	r.last = key
	r.lg.Print(text)
}

func (r *onceReporter) report(text string) { r.write(text, text) }

func (r *onceReporter) ok() {
	r.mu.Lock()
	r.last = ""
	r.mu.Unlock()
}

// logf receives the status messages of the sync. Repeated messages of one kind (the reconnects of
// a hub that is away) are written once.
func (r *onceReporter) logf(format string, args ...any) {
	r.write(format, "artifact sync: "+fmt.Sprintf(format, args...))
}

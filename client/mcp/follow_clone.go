package mcp

// Joining a meeting in a set-up directory (dec-artifact-sync): the first meeting_join of this
// process also follows the changes of the clone that the working directory of the process belongs
// to (the main directory, or a worktree of it), until the process ends. A directory that is no
// set-up clone starts nothing and says nothing. A failure never fails the join: it goes to the log
// of the server, once.

import (
	"errors"
	"fmt"
	"os"
	"sync"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/sync/control"
)

// followClone starts the follow once per process. It never blocks and never fails.
func (s *Server) followClone(c coreapi.Client) {
	s.mu.Lock()
	if s.cloneFollowing || s.NewSync == nil {
		s.mu.Unlock()
		return
	}
	s.cloneFollowing = true
	ctx := s.ctx
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		r := &onceReporter{log: s.log.Printf}
		sy := s.NewSync(control.Options{Client: c, Logf: r.logf})
		wd, err := os.Getwd()
		if err != nil {
			return
		}
		dir, err := sy.MainDirectory(ctx, wd)
		if err != nil {
			// No set-up clone here (or no Git to tell): nothing to do and nothing to say.
			if !errors.Is(err, control.ErrNotSetUp) && !errors.Is(err, control.ErrNoGit) {
				r.report("artifact sync: " + err.Error())
			}
			return
		}
		s.log.Printf("artifact sync: following the changes of %s", dir)
		err = sy.Follow(ctx, dir, control.FollowOptions{Updated: func(res control.UpdateResult, err error) {
			if err != nil {
				r.report("artifact sync: update of " + dir + " failed: " + err.Error())
				return
			}
			r.ok()
			if res.Updated {
				s.log.Printf("artifact sync: updated %s: main moved from %s to %s", dir, short(res.From), short(res.To))
			}
		}})
		if err != nil {
			r.report("artifact sync stopped: " + err.Error())
		}
	}()
}

func short(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// onceReporter writes a problem once: the same text or the same kind of status message is not
// repeated until something else was written or an update worked.
type onceReporter struct {
	log  func(format string, args ...any)
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
	r.log("%s", text)
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

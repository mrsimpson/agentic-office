package agentrunner

import (
	"context"
	"errors"
	"fmt"

	"github.com/mrsimpson/agentic-office/client/core"
	"github.com/mrsimpson/agentic-office/client/replicator"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
)

// meeting is the agent's place in one session: its participant, its local log and the
// replicator that writes the log.
type meeting struct {
	client  *core.Client
	joined  *core.Joined
	logf    func(format string, args ...any)
	repDone chan struct{} // closed when the replicator of this process stops; nil if another process writes the log
}

// joinMeeting joins sessionID with the key of cfg and makes sure that a replicator writes the
// local log: one in this process, or the one of another process. The replicator of this process
// stops when ctx ends, after session.ended, or after the agent's own participant.left.
func joinMeeting(ctx context.Context, cfg core.Config, sessionID string, logf func(string, ...any)) (*meeting, error) {
	c, err := core.Dial(cfg)
	if err != nil {
		return nil, err
	}
	j, err := c.Join(ctx, sessionID, core.JoinOptions{})
	if err != nil {
		return nil, fmt.Errorf("join %s: %w", sessionID, err)
	}
	m := &meeting{client: c, joined: j, logf: logf}
	r := &replicator.Replicator{Session: c.Session, SessionID: sessionID, ParticipantID: j.ParticipantID,
		Path: j.Paths.Log, Logf: logf}
	switch err := r.Open(); {
	case errors.Is(err, replicator.ErrRunning):
		logf("another process writes %s; following it", j.Paths.Log)
		return m, nil
	case err != nil:
		return nil, err
	}
	m.repDone = make(chan struct{})
	go func() {
		defer close(m.repDone)
		reason, err := r.Run(ctx)
		if err != nil {
			logf("replicator stopped: %v", err)
			return
		}
		logf("replicator stopped: %s", reason)
	}()
	return m, nil
}

// follow calls fn for every event of the local log, from the first line, until ctx ends or fn
// returns an error. core.ErrStop from fn ends it without an error.
func (m *meeting) follow(ctx context.Context, fn func(*sessionv1.Event) error) error {
	return core.FollowLog(ctx, m.joined.Paths.Log, true, fn)
}

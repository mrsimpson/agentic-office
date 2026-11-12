package core

import (
	"context"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"github.com/mrsimpson/agentic-office/client/replicator/locallog"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
)

// MarkOffTheRecordLast marks off the record the newest own sentence that is not off the record yet. It reads the local log.
func (c *hubClient) MarkOffTheRecordLast(ctx context.Context, sessionID string) (*sessioncommandsv1.CommandResult, error) {
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	p, err := c.cfg.Paths(sessionID, pid)
	if err != nil {
		return nil, err
	}
	evs, err := locallog.Read(p.Log)
	if err != nil {
		return nil, err
	}
	g := coreapi.NewGateway(pid)
	for _, ev := range evs {
		g.Apply(ev)
	}
	u := g.LastSent()
	if u == nil {
		return nil, coreapi.ErrNothingOffTheRecord
	}
	return c.MarkOffTheRecord(ctx, sessionID, u.EventID)
}

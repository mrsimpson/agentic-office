package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/replicator/control"

	acp "github.com/coder/acp-go-sdk"

	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
)

// Options configure one runner: one agent key in one session.
type Options struct {
	// Config is the configuration of the agent (its key, the hub, the runtime directory).
	Config coreapi.Config
	// Session is the session to take part in.
	Session string
	// Role is the role of the agent in the meeting, in plain words. It may be empty.
	Role string
	// Harness is the command of the agent harness and its arguments, for example
	// ["opencode", "acp"].
	Harness []string
	// Allow are patterns for tool calls that the runner allows when the harness asks; "*" stands
	// for any text. Empty: the office tools only (dec-agent-runner-delivery).
	Allow []string
	// Cwd is the working directory of the ACP session. Empty: the current directory.
	Cwd string
	// Office is the office binary that the harness starts as the MCP server. Empty: this
	// program.
	Office string
	// Version is reported to the harness.
	Version string
	// Stderr receives the log of the runner and the stderr of the harness. Nil: os.Stderr.
	Stderr io.Writer
	// Dial dials the office hub (if-client-core).
	Dial coreapi.Dial
	// NewReplicator creates the replicator of this process (if-replication).
	NewReplicator control.New
}

// Run keeps the agent in the session until the session ends, the agent leaves, the harness
// stops or ctx ends. It stops the harness before it returns. The end of ctx is no error.
func Run(ctx context.Context, o Options) error {
	if o.Session == "" {
		return errors.New("no session")
	}
	if len(o.Harness) == 0 {
		return errors.New("no harness command")
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	logger := log.New(o.Stderr, "office agent: ", log.LstdFlags)
	logf := logger.Printf
	cfg, err := absConfig(o.Config)
	if err != nil {
		return err
	}
	if o.Cwd == "" {
		if o.Cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	if o.Cwd, err = filepath.Abs(o.Cwd); err != nil {
		return err
	}
	if o.Office == "" {
		if o.Office, err = lookupOffice(); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m, err := joinMeeting(ctx, o, cfg, logf)
	if err != nil {
		return err
	}
	logf("joined %s as %s; local log %s", o.Session, m.joined.ParticipantID, m.joined.Paths.Log)

	h, err := startHarness(o.Harness, o.Stderr, logf, permissionRule{globs: o.Allow})
	if err != nil {
		return err
	}
	defer func() {
		h.stop()
		logf("harness stopped")
	}()
	office := acp.McpServerStdio{Name: "office", Command: o.Office, Args: []string{"mcp", "--session", o.Session},
		Env: []acp.EnvVariable{absEnv(coreapi.EnvHub, cfg.Hub), absEnv(coreapi.EnvCACert, cfg.CACert),
			absEnv(coreapi.EnvKey, cfg.KeyFile), absEnv(coreapi.EnvRuntimeDir, cfg.RuntimeDir)}}
	if err := h.open(ctx, o.Cwd, office, o.Version); err != nil {
		return err
	}

	r := &runner{o: o, m: m, h: h, logf: logf, self: m.joined.ParticipantID,
		start: m.joined.Result.GetEvent().GetId(), names: map[string]participant{}}
	err = r.loop(ctx)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

// absConfig makes the file paths of cfg absolute, because the MCP server runs in another
// working directory.
func absConfig(cfg coreapi.Config) (coreapi.Config, error) {
	for _, p := range []*string{&cfg.CACert, &cfg.KeyFile, &cfg.RuntimeDir} {
		if *p == "" {
			continue
		}
		a, err := filepath.Abs(*p)
		if err != nil {
			return cfg, err
		}
		*p = a
	}
	return cfg, nil
}

// participant is what the runner knows of a participant from participant.joined.
type participant struct {
	name string
	kind string // "human" or "agent"
}

// runner is the state of one run.
type runner struct {
	o     Options
	m     *meeting
	h     *harness
	logf  func(string, ...any)
	self  string // participant ID of the agent
	start uint32 // the join event of the agent; what came before is history
	names map[string]participant
}

// loop sends the first prompt and follows the local log until the run ends.
func (r *runner) loop(ctx context.Context) error {
	evs := make(chan *sessionstreamv1.Event)
	ferr := make(chan error, 1)
	go func() {
		ferr <- r.m.follow(ctx, func(ev *sessionstreamv1.Event) error {
			select {
			case evs <- ev:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	type turn struct {
		stop acp.StopReason
		err  error
	}
	done := make(chan turn, 1)
	busy := true
	go func() {
		stop, err := r.h.prompt(ctx, r.firstPrompt())
		done <- turn{stop, err}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.h.done:
			return r.h.exitError()
		case err := <-ferr:
			if err == nil {
				err = ctx.Err()
			}
			return fmt.Errorf("follow the local log: %w", err)
		case t := <-done:
			busy = false
			if t.err != nil {
				return fmt.Errorf("prompt: %w", t.err)
			}
			r.logf("the agent ended its turn (%s)", t.stop)
		case ev := <-evs:
			r.learn(ev)
			switch {
			case ev.GetType() == events.SessionEnded:
				r.logf("the session ended")
				return waitTurn(ctx, busy, done, r.h.done)
			case ev.GetType() == events.ParticipantLeft && ev.GetParticipantLeft().GetParticipantId() == r.self && ev.GetId() > r.start:
				r.logf("the agent left the session")
				return waitTurn(ctx, busy, done, r.h.done)
			}
		}
	}
}

// waitTurn waits for a running turn to end before the runner stops.
func waitTurn[T any](ctx context.Context, busy bool, done <-chan T, exited <-chan struct{}) error {
	if !busy {
		return nil
	}
	select {
	case <-done:
	case <-exited:
	case <-ctx.Done():
	}
	return nil
}

// learn keeps the names and kinds of the participants.
func (r *runner) learn(ev *sessionstreamv1.Event) {
	if j := ev.GetParticipantJoined(); j != nil {
		kind := "human"
		if j.GetKind() == sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT {
			kind = "agent"
		}
		name := j.GetDisplayName()
		if name == "" {
			name = j.GetParticipantId()
		}
		r.names[j.GetParticipantId()] = participant{name: name, kind: kind}
	}
}

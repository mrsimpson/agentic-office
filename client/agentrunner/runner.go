package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	acp "github.com/coder/acp-go-sdk"

	"github.com/mrsimpson/agentic-office/client/core"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
)

// Options configure one runner: one agent key in one session.
type Options struct {
	// Config is the configuration of the agent (its key, the hub, the runtime directory).
	Config core.Config
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
	m, err := joinMeeting(ctx, cfg, o.Session, logf)
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
		Env: []acp.EnvVariable{absEnv(core.EnvHub, cfg.Hub), absEnv(core.EnvCACert, cfg.CACert),
			absEnv(core.EnvKey, cfg.KeyFile), absEnv(core.EnvRuntimeDir, cfg.RuntimeDir)}}
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
func absConfig(cfg core.Config) (core.Config, error) {
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
	d     delivery
}

// result is the end of one piece of work: a prompt.
type result struct {
	stop  acp.StopReason
	err   error
	final bool // the last prompt after session.ended
}

// loop sends the first prompt and follows the local log until the run ends. At most one prompt
// runs at a time (dec-agent-runner-delivery).
func (r *runner) loop(ctx context.Context) error {
	evs := make(chan *sessionv1.Event)
	ferr := make(chan error, 1)
	go func() {
		ferr <- r.m.follow(ctx, func(ev *sessionv1.Event) error {
			select {
			case evs <- ev:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	done := make(chan result, 1)
	busy := true
	r.send(ctx, done, r.firstPrompt(), false)
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
		case res := <-done:
			busy = false
			if res.err != nil {
				return fmt.Errorf("prompt: %w", res.err)
			}
			r.logf("the agent ended its turn (%s)", res.stop)
			if res.final {
				return nil
			}
		case ev := <-evs:
			r.learn(ev)
			if ev.GetId() > r.start {
				r.d.apply(ev, r.self)
			}
		}
		if busy {
			continue
		}
		switch {
		case r.d.left:
			r.logf("the agent left the session")
			return nil
		case r.d.ended:
			r.logf("the session ended; sending the last prompt")
			busy = true
			r.send(ctx, done, r.finalPrompt(r.d.takeAll()), true)
		case r.d.due():
			b := r.d.take()
			if b.empty() {
				continue
			}
			busy = true
			r.logf("sending %d utterances (events %d to %d)", len(b.utterances), b.first(), b.turnEnd)
			r.send(ctx, done, r.turnPrompt(b), false)
		}
	}
}

// send runs one prompt in the background and reports its end on done.
func (r *runner) send(ctx context.Context, done chan<- result, text string, final bool) {
	go func() {
		stop, err := r.h.prompt(ctx, text)
		done <- result{stop: stop, err: err, final: final}
	}()
}

// learn keeps the names and kinds of the participants.
func (r *runner) learn(ev *sessionv1.Event) {
	if j := ev.GetParticipantJoined(); j != nil {
		kind := "human"
		if j.GetKind() == sessionv1.ParticipantKind_PARTICIPANT_KIND_AGENT {
			kind = "agent"
		}
		name := j.GetDisplayName()
		if name == "" {
			name = j.GetParticipantId()
		}
		r.names[j.GetParticipantId()] = participant{name: name, kind: kind}
	}
}

// who returns the name and kind of a participant.
func (r *runner) who(pid string) participant {
	if p, ok := r.names[pid]; ok {
		return p
	}
	return participant{name: pid, kind: "participant"}
}

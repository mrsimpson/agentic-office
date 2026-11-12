package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/mrsimpson/agentic-office/client/agentrunner/acpclient"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/replicator/control"

	acp "github.com/coder/acp-go-sdk"

	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
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
	// Wake chooses the wake strategy and its model (dec-agent-runner-delivery,
	// dec-byo-decision-model). The zero value: wake the agent at each change of speaker.
	Wake Wake
	// TopicTask is the task of the role when a block of topics has ended. Empty: a default task.
	TopicTask string
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
	wake, err := newWakeStrategy(o.Wake, logf)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m, err := joinMeeting(ctx, o, cfg, logf)
	if err != nil {
		return err
	}
	logf("joined %s as %s; local log %s", o.Session, m.joined.ParticipantID, m.joined.Paths.Log)

	h, err := acpclient.Start(o.Harness, o.Stderr, logf, acpclient.Rule{Globs: o.Allow})
	if err != nil {
		return err
	}
	defer func() {
		h.Stop()
		logf("harness stopped")
	}()
	office := acp.McpServerStdio{Name: "office", Command: o.Office, Args: []string{"mcp", "--session", o.Session},
		Env: []acp.EnvVariable{absEnv(coreapi.EnvHub, cfg.Hub), absEnv(coreapi.EnvCACert, cfg.CACert),
			absEnv(coreapi.EnvKey, cfg.KeyFile), absEnv(coreapi.EnvRuntimeDir, cfg.RuntimeDir)}}
	if err := h.Open(ctx, o.Cwd, office, o.Version); err != nil {
		return err
	}

	logf("%s", wake)
	r := &runner{o: o, wake: wake, m: m, h: h, logf: logf, self: m.joined.ParticipantID,
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
	wake  wakeStrategy
	m     *meeting
	h     *acpclient.Harness
	logf  func(string, ...any)
	self  string // participant ID of the agent
	start uint32 // the join event of the agent; what came before is history
	names map[string]participant
	d     delivery
	// The check of the model of the wake strategy (dec-wake-check-per-utterance). At most one
	// runs at a time.
	checkDue bool // an utterance or a turn.end waits for a check
	checking bool // a check runs
	waiting  int  // how many waiting utterances the log named last while the agent was busy
}

// turn is the end of one prompt.
type turn struct {
	stop  acp.StopReason
	err   error
	final bool // the last prompt after session.ended
}

// check is the end of one check of the model.
type check struct {
	verdict verdict
	err     error
	last    uint32 // the event ID of the last utterance that the check judged
}

// loop sends the first prompt and follows the local log until the run ends. At most one prompt
// and at most one check of the model run at a time (dec-agent-runner-delivery).
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

	done := make(chan turn, 1)
	checked := make(chan check, 1)
	busy := true
	r.send(ctx, done, r.firstPrompt(), false)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.h.Done():
			return r.h.ExitError()
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
			if t.final {
				return nil
			}
		case c := <-checked:
			r.checking = false
			r.judge(c)
		case ev := <-evs:
			r.learn(ev)
			if ev.GetId() > r.start {
				r.take(ev)
			}
		}
		if r.d.left {
			r.logf("the agent left the session")
			return waitTurn(ctx, busy, done, r.h.Done())
		}
		if busy {
			if r.d.wake && len(r.d.pending) != r.waiting {
				r.waiting = len(r.d.pending)
				r.logf("the agent is busy; %d utterances wait for it", r.waiting)
			}
			r.startCheck(ctx, checked)
			continue
		}
		r.waiting = 0
		switch {
		case r.d.ended:
			r.logf("the session ended; sending the last prompt")
			busy = true
			r.send(ctx, done, r.finalPrompt(r.d.take()), true)
		case r.d.wake:
			b := r.d.take()
			r.checkDue = false // everything pending goes to the agent now
			if b.empty() {
				break
			}
			first, last := b.span()
			r.logf("waking the agent with %d utterances (events %d to %d)", len(b.utterances), first, last)
			busy = true
			r.send(ctx, done, r.wakePrompt(b), false)
		default:
			r.startCheck(ctx, checked)
		}
	}
}

// take applies one event of the log after the agent's join. The wake strategy says what an
// utterance or a turn.end of another participant asks of the runner.
func (r *runner) take(ev *sessionstreamv1.Event) {
	switch r.d.apply(ev, r.self) {
	case triggerUtterance:
		r.follow(r.wake.utterance(time.Now()))
	case triggerTurnEnd:
		r.follow(r.wake.turnEnd(time.Now()))
	case triggerNotice:
		// The agent got a sentence that is now off the record. It must know that soon.
		r.d.wake = true
	}
}

// follow does what the wake strategy asks.
func (r *runner) follow(s step) {
	switch s {
	case stepWake:
		r.d.wake = true
	case stepCheck:
		r.checkDue = true
	}
}

// startCheck starts a check of the model if one is due and none runs. The check judges
// all pending utterances; utterances that come while it runs wait for the next check, which
// then judges them all together (dec-agent-runner-delivery).
func (r *runner) startCheck(ctx context.Context, checked chan<- check) {
	if !r.checkDue || r.checking || r.d.ended || len(r.d.pending) == 0 {
		return
	}
	req := r.checkRequest()
	last := req.new[len(req.new)-1].eventID
	r.checkDue, r.checking = false, true
	go func() {
		v, err := r.wake.ask(ctx, req)
		checked <- check{verdict: v, err: err, last: last}
	}()
}

// judge takes the end of a check. The wake strategy learns of it in any case, so that it
// counts failures. An answer about utterances that the agent got meanwhile counts no more.
func (r *runner) judge(c check) {
	s := r.wake.answered(time.Now(), c.verdict, c.err, r.d.turnBy != "")
	if !r.d.pendingUpTo(c.last) {
		return
	}
	v := c.verdict
	switch {
	case c.err != nil:
		if s == stepWake {
			r.logf("the turn has ended meanwhile; waking the agent")
			r.d.wake = true
		}
	case s == stepWake:
		r.logf("decision up to event %d: wake (act %t, addressed %q, topic closed %t): %s",
			c.last, v.act, v.addressed, v.topicClosed, v.note)
		r.d.wake = true
		r.d.topicClosed = r.d.topicClosed || v.topicClosed
		if v.addressed != "" {
			r.d.addressed = v.addressed
		}
	default:
		r.logf("decision up to event %d: do not wake (addressed %q): %s", c.last, v.addressed, v.note)
	}
}

// send runs one prompt in the background and reports its end on done.
func (r *runner) send(ctx context.Context, done chan<- turn, text string, final bool) {
	go func() {
		stop, err := r.h.Prompt(ctx, text)
		done <- turn{stop: stop, err: err, final: final}
	}()
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

// who returns the name and kind of a participant.
func (r *runner) who(pid string) participant {
	if p, ok := r.names[pid]; ok {
		return p
	}
	return participant{name: pid, kind: "participant"}
}

// absEnv returns the environment entry name=value.
func absEnv(name, value string) acp.EnvVariable { return acp.EnvVariable{Name: name, Value: value} }

// lookupOffice returns the path of the running office binary, for the MCP server.
func lookupOffice() (string, error) { return os.Executable() }

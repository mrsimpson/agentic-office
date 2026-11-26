package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/client/agentrunner/acpclient"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/replicator/control"

	"connectrpc.com/connect/v2"
	acp "github.com/coder/acp-go-sdk"

	"github.com/mrsimpson/agentic-office/hub/session/activityapi/sessionactivityv1connect"
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
	// AlsoAllow are patterns that allow tool calls in addition to the rule that Allow gives, for
	// example the tools that the definition of a team agent names.
	AlsoAllow []string
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

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	m, err := joinMeeting(ctx, o, cfg, logf)
	if err != nil {
		return err
	}
	logf("joined %s as %s; local log %s", o.Session, m.joined.ParticipantID, m.joined.Paths.Log)

	// The skills injector (dec-skills): the harness finds the skills when it starts.
	injectSkills(o.Cwd, logf)
	// The harness of a team agent gets the token of the agent in a file and never the key of the
	// agent launcher (dec-team-agents).
	officeEnv := []acp.EnvVariable{absEnv(coreapi.EnvHub, cfg.Hub), absEnv(coreapi.EnvCACert, cfg.CACert),
		absEnv(coreapi.EnvRuntimeDir, cfg.RuntimeDir), absEnv(coreapi.EnvTenant, cfg.Tenant)}
	if cfg.TeamAgent != "" {
		tokenFile, err := keepTokenFile(ctx, cancel, m, cfg, logf)
		if err != nil {
			return err
		}
		officeEnv = append(officeEnv, absEnv(coreapi.EnvTokenFile, tokenFile))
	} else {
		officeEnv = append(officeEnv, absEnv(coreapi.EnvKey, cfg.KeyFile))
	}
	r := &runner{o: o, wake: wake, m: m, logf: logf, self: m.joined.ParticipantID,
		start: m.joined.Result.GetEvent().GetId(), names: map[string]participant{},
		office: acp.McpServerStdio{Name: "office", Command: o.Office, Args: []string{"mcp", "--session", o.Session},
			Env: officeEnv}}
	if err := r.startHarness(ctx); err != nil {
		return err
	}
	defer func() {
		r.h.Stop()
		logf("harness stopped")
	}()

	logf("%s", wake)
	// The activity of the agent for the stage (dec-agent-activity).
	r.rep = newReporter(sessionactivityv1connect.NewSessionActivityServiceClient(m.client.Connection()),
		o.Session, r.self, logf)
	go r.rep.run(ctx)
	err = r.loop(ctx)
	// A team agent that ended (removed, or its agent launcher left) stops the runner with the reason.
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

// startHarness starts the harness and opens its ACP session with the office MCP server. The
// harness becomes the harness of the runner.
func (r *runner) startHarness(ctx context.Context) error {
	h, err := acpclient.Start(r.o.Harness, r.o.Stderr, r.logf, acpclient.Rule{Globs: r.o.Allow, Also: r.o.AlsoAllow})
	if err != nil {
		return err
	}
	h.OnToolCall(func(title string, kind acp.ToolKind) {
		r.rep.set(activity{working: true, tool: shortToolName(title, kind)})
	})
	r.h = h
	if err := h.Open(ctx, r.o.Cwd, r.office, r.o.Version); err != nil {
		h.Stop()
		r.logf("harness stopped")
		return err
	}
	return nil
}

const (
	// maxRestarts is how often the runner starts a failing harness again within restartWindow.
	// After that, the harness is broken for good, and the run ends with its error.
	maxRestarts   = 3
	restartWindow = 10 * time.Minute
	// restartPause is the wait before the first restart; each further restart within the window
	// waits one pause longer.
	restartPause = 2 * time.Second
)

// restart replaces a harness that stopped or failed a prompt (for example "Internal error" of
// its model service) by a new one, and gives the new harness what the old one did not get: the
// utterances of the prompt that failed. It reports whether the run goes on. It does not restart
// when ctx has ended, when the agent has left the session, when no turn of the agent has ended
// well yet (that is a setup error), or after too many restarts in a short time.
func (r *runner) restart(ctx context.Context, cause error) bool {
	if ctx.Err() != nil || r.d.left || !r.healthy {
		return false
	}
	now := time.Now()
	recent := r.restarts[:0]
	for _, t := range r.restarts {
		if now.Sub(t) < restartWindow {
			recent = append(recent, t)
		}
	}
	r.restarts = recent
	if len(r.restarts) >= maxRestarts {
		r.logf("the harness failed %d times within %s; giving up", maxRestarts+1, restartWindow)
		return false
	}
	r.restarts = append(r.restarts, now)
	pause := time.Duration(len(r.restarts)) * restartPause
	r.logf("WARNING: the harness failed: %v; starting it again in %s (restart %d of %d within %s)",
		cause, pause, len(r.restarts), maxRestarts, restartWindow)
	r.rep.set(activity{})
	r.h.Stop()
	select {
	case <-time.After(pause):
	case <-ctx.Done():
		return false
	}
	if err := r.startHarness(ctx); err != nil {
		r.logf("could not start the harness again: %v", err)
		return false
	}
	r.d.requeue(r.inflight)
	r.inflight = batch{}
	return true
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

// keepTokenFile writes the token of the team agent into a file in the runtime directory that only
// the user reads, and keeps it fresh until ctx ends. The MCP server of the harness reads it, so
// the harness has the token of the agent and not the key of the agent launcher (dec-team-agents).
// When the hub refuses a new token for good (the team agent was removed, the agent launcher left
// the tenant or lost the key), keepTokenFile cancels ctx with this reason and the run ends.
func keepTokenFile(ctx context.Context, cancel context.CancelCauseFunc, m *meeting, cfg coreapi.Config, logf func(string, ...any)) (string, error) {
	dir := filepath.Join(cfg.RuntimeDir, "tokens")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file := filepath.Join(dir, cfg.TeamAgent+"-"+tokenFileName(m.joined.SessionID)+".json")
	if err := m.client.WriteTokenFile(ctx, file); err != nil {
		return "", err
	}
	go func() {
		defer func() { _ = os.Remove(file) }()
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				err := m.client.WriteTokenFile(ctx, file)
				if err == nil || ctx.Err() != nil {
					continue
				}
				switch connect.CodeOf(err) {
				case connect.CodeNotFound, connect.CodeUnauthenticated, connect.CodePermissionDenied:
					logf("the team agent %s cannot go on: %v; stopping", cfg.TeamAgent, err)
					cancel(fmt.Errorf("the team agent %s ended: %w", cfg.TeamAgent, err))
					return
				}
				logf("the token of the team agent could not be renewed: %v", err)
			}
		}
	}()
	return file, nil
}

// tokenFileName makes a session ID fit a file name.
func tokenFileName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, s)
}

// participant is what the runner knows of a participant from participant.joined.
type participant struct {
	name string
	kind string // "human" or "agent"
}

// runner is the state of one run.
type runner struct {
	o    Options
	wake wakeStrategy
	m    *meeting
	h    *acpclient.Harness
	logf func(string, ...any)
	// office is the MCP server that every harness of this run gets.
	office acp.McpServerStdio
	self   string // participant ID of the agent
	start  uint32 // the join event of the agent; what came before is history
	names  map[string]participant
	d      delivery
	// The check of the model of the wake strategy (dec-wake-check-per-utterance). At most one
	// runs at a time.
	checkDue bool // an utterance or a turn.end waits for a check
	checking bool // a check runs
	waiting  int  // how many waiting utterances the log named last while the agent was busy
	// spoke: the agent spoke or raised its hand since the last prompt began, by the log.
	spoke bool
	// rep reports the activity of the agent to the hub.
	rep *reporter
	// inflight is what the running prompt delivers. If the harness fails during that prompt, the
	// agent did not get it, and the runner delivers it again to the new harness.
	inflight batch
	// healthy: a turn of the agent has ended without an error. Before that, a failing harness is a
	// setup error (no login, no model) and ends the run at once.
	healthy bool
	// restarts are the times of the recent restarts of the harness.
	restarts []time.Time
}

// turn is the end of one prompt.
type turn struct {
	acpclient.Turn
	h     *acpclient.Harness // the harness that ran the prompt
	err   error
	final bool // the last prompt after session.ended
	asked bool // the prompt that asked the agent to speak an unspoken reply
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
	r.send(ctx, done, r.firstPrompt(false), turn{})
	// restartAgent starts the harness again after a failure and sends the agent a prompt that tells it
	// what happened. It reports whether the run goes on.
	restartAgent := func(cause error) bool {
		if !r.restart(ctx, cause) {
			return false
		}
		busy = true
		r.send(ctx, done, r.firstPrompt(true), turn{})
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.h.Done():
			if cause := r.h.ExitError(); !restartAgent(cause) {
				return cause
			}
		case err := <-ferr:
			if err == nil {
				err = ctx.Err()
			}
			return fmt.Errorf("follow the local log: %w", err)
		case t := <-done:
			if t.h != r.h {
				break // the end of a prompt of a harness that the runner has replaced
			}
			busy = false
			if t.err != nil {
				cause := fmt.Errorf("prompt: %w", t.err)
				if !restartAgent(cause) {
					return cause
				}
				break
			}
			r.healthy, r.inflight = true, batch{}
			r.logf("the agent ended its turn (%s)", t.Stop)
			if t.final {
				return nil
			}
			switch r.unspoken(t) {
			case replyAsk:
				busy = true
				r.send(ctx, done, r.unspokenPrompt(t.Reply), turn{asked: true})
			case replyLost:
				r.rep.set(activity{unspoken: true})
			default:
				r.rep.set(activity{})
			}
		case c := <-checked:
			r.checking = false
			r.judge(c)
		case ev := <-evs:
			r.learn(ev)
			r.noteSpoken(ev)
			if ev.GetId() > r.start {
				r.take(ev)
			}
		}
		if r.d.left || r.d.ended {
			r.rep.stop()
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
			r.inflight = r.d.take()
			r.send(ctx, done, r.finalPrompt(r.inflight), turn{final: true})
		case r.d.wake:
			b := r.d.take()
			r.checkDue = false // everything pending goes to the agent now
			if b.empty() {
				break
			}
			first, last := b.span()
			r.logf("waking the agent with %d utterances (events %d to %d)", len(b.utterances), first, last)
			busy = true
			r.inflight = b
			r.send(ctx, done, r.wakePrompt(b), turn{})
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

// send runs one prompt in the background and reports its end on done, with the flags of t.
func (r *runner) send(ctx context.Context, done chan<- turn, text string, t turn) {
	r.spoke = false
	r.rep.set(activity{working: true})
	t.h = r.h
	go func() {
		t.Turn, t.err = t.h.Prompt(ctx, text)
		done <- t
	}()
}

// speaks matches the title of a tool call with which the agent reaches the meeting: it speaks or
// raises its hand.
var speaks = regexp.MustCompile(`(?i)utterance_say|hand_raise`)

// noteSpoken notes an utterance or a raised hand of the agent itself.
func (r *runner) noteSpoken(ev *sessionstreamv1.Event) {
	if u := ev.GetUtteranceFinal(); u != nil && u.GetParticipantId() == r.self {
		r.spoke = true
	}
	if h := ev.GetHandRaised(); h != nil && h.GetParticipantId() == r.self {
		r.spoke = true
	}
}

// replyFate is what became of the reply that the agent wrote in its chat in a turn.
type replyFate int

const (
	replyNone replyFate = iota // no reply, or a reply next to a spoken answer
	replyAsk                   // nobody heard it: ask the agent once whether it was meant for the meeting
	replyLost                  // nobody heard it, and the agent was asked already
)

// unspoken says whether the agent ended a turn with a reply in its chat and without speaking:
// the reply reached nobody in the meeting. It logs the reply. The runner asks once per prompt;
// after the prompt that asked the agent to speak, the reply is lost, and the runner says so.
func (r *runner) unspoken(t turn) replyFate {
	if t.Reply == "" {
		return replyNone
	}
	spoke := r.spoke
	for _, title := range t.Tools {
		spoke = spoke || speaks.MatchString(title)
	}
	switch {
	case spoke:
		r.logf("the agent's reply in its chat (not spoken): %s", oneLine(t.Reply, 300))
		return replyNone
	case t.asked:
		r.logf("WARNING: the agent again did not speak; this reply reached nobody in the meeting: %s", oneLine(t.Reply, 300))
		return replyLost
	default:
		r.logf("WARNING: the agent wrote a reply in its chat but did not speak it; asking it once whether it was meant for the meeting: %s",
			oneLine(t.Reply, 300))
		return replyAsk
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

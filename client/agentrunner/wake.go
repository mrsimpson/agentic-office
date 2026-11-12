package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// The wake strategies (dec-agent-runner-delivery). The user chooses one with --wake.
const (
	// WakeTurnEnd wakes the agent at each change of speaker (turn.end of another participant).
	WakeTurnEnd = "turn-end"
	// WakeLLM asks a forwarding judge after each utterance (if-forwarding-judge).
	WakeLLM = "llm"
	// WakeDecisionModel asks a decision model after each utterance (if-decision-model).
	WakeDecisionModel = "decision-model"
)

// Wake configures the wake strategy (dec-agent-runner-delivery, dec-byo-decision-model).
type Wake struct {
	// Strategy is WakeTurnEnd, WakeLLM or WakeDecisionModel. Empty: WakeTurnEnd.
	Strategy string
	// Command is the forwarding judge as a shell command (run with sh -c), for WakeLLM. It reads
	// one forwardingjudge.Request as JSON on stdin and writes one forwardingjudge.Answer as JSON
	// on stdout.
	Command string
	// URL is, for WakeLLM, the base URL of an OpenAI-compatible API, for example
	// http://127.0.0.1:11434/v1; the runner posts to URL + "/chat/completions". For
	// WakeDecisionModel, it is the full URL of the System One endpoint; nothing is appended.
	URL string
	// Model is the model name for URL.
	Model string
	// APIKey is sent as a bearer token to URL. It may be empty.
	APIKey string
	// Threshold is the probability from which a noul answer of a decision model counts as yes.
	// Zero means 0.5.
	Threshold float64
	// Timeout bounds one check. Zero means 10 s.
	Timeout time.Duration
}

// step is what a wake strategy asks of the runner.
type step int

const (
	stepWait  step = iota // nothing now; the pending utterances wait
	stepWake              // wake the agent as soon as it is free
	stepCheck             // check the pending utterances with the model
)

// verdict is the answer of a model, in the terms of the runner.
type verdict struct {
	act         bool
	topicClosed bool
	addressed   string // a participant ID, or empty
	note        string // the reason or the probabilities, for the log
}

// wakeStrategy is the one seam between the runner and the ways to decide when to wake the agent
// (dec-agent-runner-delivery). The runner chooses one strategy at start and knows only this
// interface and its own types (checkRequest, verdict, step); each strategy maps them to the
// contract of its model. The runner keeps the rules that hold for all strategies: one prompt and
// one check at a time, the queue, the order, off the record and the end of the session.
type wakeStrategy interface {
	// String describes the strategy for the log at start, without a key.
	String() string
	// utterance is called when an utterance of another participant is pending.
	utterance(now time.Time) step
	// turnEnd is called when another participant ended the turn after pending utterances.
	turnEnd(now time.Time) step
	// ask asks the model about the pending utterances. Only a strategy that returns stepCheck
	// is asked.
	ask(ctx context.Context, req checkRequest) (verdict, error)
	// answered takes the end of a check: the verdict or the error. turnEnded says whether the
	// speaker of the last pending utterance has ended the turn by now. It returns stepWake or
	// stepWait.
	answered(now time.Time, v verdict, err error, turnEnded bool) step
}

// newWakeStrategy chooses the wake strategy of w.
func newWakeStrategy(w Wake, logf func(string, ...any)) (wakeStrategy, error) {
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	switch w.Strategy {
	case "", WakeTurnEnd:
		if w.Command != "" || w.URL != "" {
			return nil, fmt.Errorf("the wake strategy %s asks no model", WakeTurnEnd)
		}
		return turnEndWake{}, nil
	case WakeLLM:
		switch {
		case w.Command != "" && w.URL != "":
			return nil, errors.New("give a command or a URL for the forwarding judge, not both")
		case w.Command == "" && w.URL == "":
			return nil, errors.New("the forwarding judge needs a command or a URL")
		case w.URL != "" && w.Model == "":
			return nil, errors.New("the forwarding judge needs a model name")
		}
		return &forwardingJudgeWake{fallback: newFallback(WakeLLM, "forwarding judge", timeout, logf),
			command: w.Command, url: w.URL, model: w.Model, key: w.APIKey}, nil
	case WakeDecisionModel:
		switch {
		case w.Command != "":
			return nil, errors.New("a decision model is an endpoint, not a command")
		case w.URL == "" || w.Model == "":
			return nil, errors.New("the decision model needs a URL and a model name")
		}
		th := w.Threshold
		if th == 0 {
			th = 0.5
		}
		if th < 0 || th > 1 {
			return nil, fmt.Errorf("threshold %v is not between 0 and 1", th)
		}
		return &decisionModelWake{fallback: newFallback(WakeDecisionModel, "decision model", timeout, logf),
			url: w.URL, model: w.Model, key: w.APIKey, threshold: th}, nil
	}
	return nil, fmt.Errorf("unknown wake strategy %q (want %s, %s or %s)", w.Strategy, WakeTurnEnd, WakeLLM, WakeDecisionModel)
}

// turnEndWake wakes the agent at each change of speaker. It asks no model.
type turnEndWake struct{}

func (turnEndWake) String() string {
	return "wake strategy " + WakeTurnEnd + ": the agent is woken at each change of speaker (turn.end); a new topic within a monologue is not detected (--wake " + WakeLLM + " or --wake " + WakeDecisionModel + " detect it)"
}
func (turnEndWake) utterance(time.Time) step { return stepWait }
func (turnEndWake) turnEnd(time.Time) step   { return stepWake }
func (turnEndWake) ask(context.Context, checkRequest) (verdict, error) {
	return verdict{}, errors.New("the wake strategy " + WakeTurnEnd + " asks no model")
}
func (turnEndWake) answered(time.Time, verdict, error, bool) step { return stepWait }

// failuresToSwitch failures within failureWindow make a model strategy switch to turn-end.
const (
	failuresToSwitch = 3
	failureWindow    = 5 * time.Minute
)

// fallback is the part that the model strategies share (dec-agent-runner-delivery): they check
// each utterance with a model, and fall back to turn-end when the model fails.
//
//   - One failure (an error, a time-out or an answer outside the contract) does not wake the
//     agent. The pending utterances wait for the next turn.end of another participant; if that
//     turn.end has come already, the agent is woken now.
//   - After failuresToSwitch failures within failureWindow, with no answer between them, the
//     strategy works as turn-end and logs it once. It asks the model again at most once per
//     change of speaker: at the first utterance after a turn.end. When the model answers, it
//     switches back with a log line.
//
// There is no timer: only the window for counting failures reads the clock.
type fallback struct {
	name     string // the name of the strategy
	label    string // the model, in words
	timeout  time.Duration
	logf     func(string, ...any)
	failures []time.Time // the failed checks since the last answer, within failureWindow
	switched bool        // switched to turn-end
	probe    bool        // with switched: the next utterance may ask the model again
	unjudged bool        // the last check failed; the pending utterances wait for a turn.end
}

func newFallback(name, label string, timeout time.Duration, logf func(string, ...any)) fallback {
	return fallback{name: name, label: label, timeout: timeout, logf: logf}
}

func (f *fallback) utterance(time.Time) step {
	if f.switched {
		if !f.probe {
			return stepWait
		}
		f.probe = false
		f.logf("asking the %s again", f.label)
	}
	// The check judges all pending utterances, also those of a failed check.
	f.unjudged = false
	return stepCheck
}

func (f *fallback) turnEnd(time.Time) step {
	if f.switched {
		f.probe = true
		return stepWake
	}
	if f.unjudged {
		return stepWake
	}
	return stepCheck
}

func (f *fallback) answered(now time.Time, v verdict, err error, turnEnded bool) step {
	if err != nil {
		kept := f.failures[:0]
		for _, t := range f.failures {
			if now.Sub(t) < failureWindow {
				kept = append(kept, t)
			}
		}
		f.failures = append(kept, now)
		switch {
		case f.switched:
			f.logf("the %s still fails (%v); asking it again after the next change of speaker", f.label, err)
		case len(f.failures) >= failuresToSwitch:
			f.switched, f.probe = true, false
			f.logf("the %s failed (%v); %d failures within %s: switching to %s, asking the %s again after the next change of speaker",
				f.label, err, len(f.failures), failureWindow, WakeTurnEnd, f.label)
		default:
			f.logf("the %s failed (%v); the utterances wait for the next change of speaker", f.label, err)
		}
		f.unjudged = true
		if turnEnded {
			return stepWake
		}
		return stepWait
	}
	if f.switched {
		f.logf("the %s answers again: switching back from %s to %s", f.label, WakeTurnEnd, f.name)
	}
	f.failures, f.switched, f.probe, f.unjudged = nil, false, false, false
	if v.act || v.topicClosed {
		return stepWake
	}
	return stepWait
}

// bounded runs call with the time-out of the strategy and reports a time-out as an error.
func (f *fallback) bounded(ctx context.Context, call func(context.Context) ([]byte, error)) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	out, err := call(ctx)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("no answer within %s", f.timeout)
	}
	return out, err
}

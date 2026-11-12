package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// AgentOptions are what the agent runner (bb-agent-runner) needs from the CLI.
type AgentOptions struct {
	Config  coreapi.Config
	Session string
	Role    string
	Harness []string // the harness command and its arguments
	Allow   []string // patterns of tool calls to allow; empty: the office tools only
	Cwd     string
	Office  string // this binary, for the MCP server of the harness
	Version string
	Stderr  io.Writer
	// Wake is the wake strategy (dec-agent-runner-delivery): WakeTurnEnd, WakeLLM or
	// WakeDecisionModel.
	Wake string
	// The model of the strategies llm and decision-model (dec-byo-decision-model): for llm a
	// command, or an OpenAI-compatible base URL with a model; for decision-model the full URL of
	// a System One endpoint with a model. The key is optional.
	DecideCommand   string
	DecideURL       string
	DecideModel     string
	DecideKey       string
	DecideThreshold float64
	DecideTimeout   time.Duration
	DecideRetry     time.Duration
	// TopicTask is the task of the role when a block of topics has ended.
	TopicTask string
}

// The wake strategies of --wake.
const (
	WakeTurnEnd       = "turn-end"
	WakeLLM           = "llm"
	WakeDecisionModel = "decision-model"
)

// EnvDecideKey holds the key of the model's endpoint, so that it is not on the command
// line.
const EnvDecideKey = "OFFICE_DECIDE_KEY"

// RunAgent is the hook for the agent runner (client/agentrunner). The wiring of the participant
// client (client/) sets it, so that the CLI does not depend on the runner.
var RunAgent func(ctx context.Context, opts AgentOptions) error

// stringList is a flag that may be given several times.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// cmdAgentRun runs the agent runner: office agent run --session S [--role T | --role-file F]
// [--allow PATTERN]... [--cwd DIR] [--wake turn-end|llm|decision-model]
// [--decide-cmd CMD | --decide-url URL --decide-model M] [--decide-threshold P]
// [--decide-timeout D] [--decide-retry D] [--topic-task T] -- <harness command>.
func cmdAgentRun(e *env, fs *flag.FlagSet) func([]string) error {
	role := fs.String("role", "", "the role of the agent, in plain words")
	roleFile := fs.String("role-file", "", "read the role from this file")
	var allow stringList
	fs.Var(&allow, "allow", "allow tool calls whose title matches this pattern (* is any text); repeatable; default: the office tools only")
	cwd := fs.String("cwd", "", "working directory of the agent's session (default: the current directory)")
	wake := fs.String("wake", WakeTurnEnd, "when to wake the agent: "+WakeTurnEnd+" (at each change of speaker), "+WakeLLM+" (an LLM judge decides after each utterance) or "+WakeDecisionModel+" (a decision model decides after each utterance)")
	decideCmd := fs.String("decide-cmd", "", "with --wake "+WakeLLM+": a shell command that reads the request as JSON on stdin and writes the answer as JSON on stdout")
	decideURL := fs.String("decide-url", "", "with --wake "+WakeLLM+": base URL of an OpenAI-compatible API, for example http://127.0.0.1:11434/v1; with --wake "+WakeDecisionModel+": the full URL of a System One endpoint (key in "+EnvDecideKey+")")
	decideModel := fs.String("decide-model", "", "the model name for --decide-url")
	decideThreshold := fs.Float64("decide-threshold", 0.5, "with --wake "+WakeDecisionModel+": the probability from which an answer counts as yes")
	decideTimeout := fs.Duration("decide-timeout", 10*time.Second, "after this time without an answer, a check of the model has failed")
	decideRetry := fs.Duration("decide-retry", 30*time.Second, "after the switch to "+WakeTurnEnd+", wait this long before asking the model again; the wait doubles up to 5m")
	topicTask := fs.String("topic-task", "", "the task of the role when a block of topics has ended (with --wake "+WakeLLM+" or "+WakeDecisionModel+")")
	return func(args []string) error {
		if len(args) == 0 {
			return usagef("no harness command: office agent run --session S -- <harness command>")
		}
		if e.session == "" {
			return usagef("no session: pass --session or set OFFICE_SESSION")
		}
		if *role != "" && *roleFile != "" {
			return usagef("give --role or --role-file, not both")
		}
		r := *role
		if *roleFile != "" {
			b, err := os.ReadFile(*roleFile)
			if err != nil {
				return usagef("role file: %v", err)
			}
			r = string(b)
		}
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if err := checkWake(*wake, set, *decideCmd, *decideURL, *decideModel); err != nil {
			return err
		}
		if *decideThreshold <= 0 || *decideThreshold > 1 {
			return usagef("--decide-threshold must be above 0 and at most 1")
		}
		if *decideTimeout <= 0 {
			return usagef("--decide-timeout must be positive")
		}
		if *decideRetry < 0 {
			return usagef("--decide-retry must not be negative")
		}
		if RunAgent == nil {
			return errors.New("this office binary has no agent runner")
		}
		office, err := os.Executable()
		if err != nil {
			return err
		}
		return RunAgent(e.ctx, AgentOptions{Config: e.cfg, Session: e.session, Role: r, Harness: args,
			Allow: allow, Cwd: *cwd, Office: office, Version: Version, Stderr: e.stderr,
			Wake: *wake, DecideCommand: *decideCmd, DecideURL: *decideURL, DecideModel: *decideModel,
			DecideKey: os.Getenv(EnvDecideKey), DecideThreshold: *decideThreshold, DecideTimeout: *decideTimeout,
			DecideRetry: *decideRetry, TopicTask: *topicTask})
	}
}

// checkWake checks that the flags of the model fit the wake strategy. set are the flags given.
func checkWake(wake string, set map[string]bool, cmd, url, model string) error {
	given := func(names ...string) string {
		for _, n := range names {
			if set[n] {
				return "--" + n
			}
		}
		return ""
	}
	switch wake {
	case WakeTurnEnd:
		if f := given("decide-cmd", "decide-url", "decide-model", "decide-threshold", "decide-timeout", "decide-retry"); f != "" {
			return usagef("%s needs --wake %s or --wake %s", f, WakeLLM, WakeDecisionModel)
		}
	case WakeLLM:
		switch {
		case cmd != "" && url != "":
			return usagef("give --decide-cmd or --decide-url, not both")
		case cmd == "" && url == "":
			return usagef("--wake %s needs --decide-cmd or --decide-url", WakeLLM)
		case url != "" && model == "":
			return usagef("--decide-url needs --decide-model")
		case set["decide-threshold"]:
			return usagef("--decide-threshold needs --wake %s", WakeDecisionModel)
		}
	case WakeDecisionModel:
		switch {
		case cmd != "":
			return usagef("--wake %s takes --decide-url, not --decide-cmd", WakeDecisionModel)
		case url == "" || model == "":
			return usagef("--wake %s needs --decide-url and --decide-model", WakeDecisionModel)
		}
	default:
		return usagef("--wake must be %s, %s or %s", WakeTurnEnd, WakeLLM, WakeDecisionModel)
	}
	return nil
}

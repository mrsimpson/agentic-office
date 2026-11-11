package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/client/core"
)

// AgentOptions are what the agent runner (bb-agent-runner) needs from the CLI.
type AgentOptions struct {
	Config  core.Config
	Session string
	Role    string
	Harness []string // the harness command and its arguments
	Allow   []string // patterns of tool calls to allow; empty: the office tools only
	Cwd     string
	Office  string // this binary, for the MCP server of the harness
	Version string
	Stderr  io.Writer
	// The decision model (dec-byo-decision-model): a command, or an OpenAI-compatible endpoint
	// with a model and an optional key. All empty: wake the agent at every change of speaker.
	DecideCommand string
	DecideURL     string
	DecideModel   string
	DecideKey     string
	DecideTimeout time.Duration
	// TopicTask is the task of the role when a block of topics has ended.
	TopicTask string
}

// EnvDecideKey holds the key of the decision model's endpoint, so that it is not on the command
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
// [--allow PATTERN]... [--cwd DIR] -- <harness command>.
func cmdAgentRun(e *env, fs *flag.FlagSet) func([]string) error {
	role := fs.String("role", "", "the role of the agent, in plain words")
	roleFile := fs.String("role-file", "", "read the role from this file")
	var allow stringList
	fs.Var(&allow, "allow", "allow tool calls whose title matches this pattern (* is any text); repeatable; default: the office tools only")
	cwd := fs.String("cwd", "", "working directory of the agent's session (default: the current directory)")
	decideCmd := fs.String("decide-cmd", "", "decision model: a shell command that reads the request as JSON on stdin and writes the answer as JSON on stdout")
	decideURL := fs.String("decide-url", "", "decision model: base URL of an OpenAI-compatible API, for example http://127.0.0.1:11434/v1 (key in "+EnvDecideKey+")")
	decideModel := fs.String("decide-model", "", "the model name for --decide-url")
	decideTimeout := fs.Duration("decide-timeout", 10*time.Second, "after this time without an answer, wake the agent")
	topicTask := fs.String("topic-task", "", "the task of the role when a block of topics has ended (with a decision model)")
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
		if *decideCmd != "" && *decideURL != "" {
			return usagef("give --decide-cmd or --decide-url, not both")
		}
		if *decideURL != "" && *decideModel == "" {
			return usagef("--decide-url needs --decide-model")
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
			DecideCommand: *decideCmd, DecideURL: *decideURL, DecideModel: *decideModel,
			DecideKey: os.Getenv(EnvDecideKey), DecideTimeout: *decideTimeout, TopicTask: *topicTask})
	}
}

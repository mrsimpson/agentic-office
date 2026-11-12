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
	// TopicTask is the task of the role when a block of topics has ended.
	TopicTask string
}

// The wake strategies of --wake.
const (
	WakeTurnEnd       = "turn-end"
	WakeLLM           = "llm"
	WakeDecisionModel = "decision-model"
)

// EnvDecideKey is the default environment variable that holds the key of the model's endpoint,
// so that the key is neither on the command line nor in a file. A configuration file can name
// another variable (agent.wake.key-env).
const EnvDecideKey = "OFFICE_DECIDE_KEY"

// EnvSession is the environment variable of the session.
const EnvSession = "OFFICE_SESSION"

// RunAgent is the hook for the agent runner (client/agentrunner). The wiring of the participant
// client (client/) sets it, so that the CLI does not depend on the runner.
var RunAgent func(ctx context.Context, opts AgentOptions) error

// stringList is a flag that may be given several times.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// agentFlags maps the flags of "office agent run" to the keys of the configuration files.
var agentFlags = []struct{ flag, key string }{
	{"session", keySession}, {"role", keyRole}, {"role-file", keyRoleFile}, {"allow", keyAllow},
	{"cwd", keyCwd}, {"topic-task", keyTopicTask}, {"wake", keyStrategy}, {"decide-cmd", keyCommand},
	{"decide-url", keyURL}, {"decide-model", keyModel}, {"decide-threshold", keyThreshold},
	{"decide-timeout", keyTimeout},
}

// flagOf returns the flag of a key, with its dashes, or the key if it has no flag.
func flagOf(key string) string {
	for _, f := range agentFlags {
		if f.key == key {
			return "--" + f.flag
		}
	}
	return key
}

// name names a key for an error: the flag if the value came from a flag, else the key and its
// source.
func (s settings) name(key string) string {
	st, ok := s[key]
	switch {
	case !ok:
		return flagOf(key) + " (" + key + ")"
	case st.Source.Kind == sourceFlag:
		return flagOf(key)
	}
	return key + " (" + st.Source.String() + ")"
}

// cmdAgentRun runs the agent runner: office agent run --session S [--role T | --role-file F]
// [--allow PATTERN]... [--cwd DIR] [--wake turn-end|llm|decision-model]
// [--decide-cmd CMD | --decide-url URL --decide-model M] [--decide-threshold P]
// [--decide-timeout D] [--topic-task T] -- <harness command>. Every option can also come from a
// configuration file (concept-client-configuration).
func cmdAgentRun(e *env, fs *flag.FlagSet) func([]string) error {
	role := fs.String("role", "", "the role of the agent, in plain words (agent.role)")
	roleFile := fs.String("role-file", "", "read the role from this file (agent.role-file)")
	var allow stringList
	fs.Var(&allow, "allow", "allow tool calls whose title matches this pattern (* is any text); repeatable; default: the office tools only (agent.allow)")
	cwd := fs.String("cwd", "", "working directory of the agent's session (default: the current directory) (agent.cwd)")
	wake := fs.String("wake", WakeTurnEnd, "when to wake the agent: "+WakeTurnEnd+" (at each change of speaker), "+WakeLLM+" (a forwarding judge decides after each utterance) or "+WakeDecisionModel+" (a decision model decides after each utterance) (agent.wake.strategy)")
	decideCmd := fs.String("decide-cmd", "", "with --wake "+WakeLLM+": a shell command that reads the request as JSON on stdin and writes the answer as JSON on stdout (agent.wake.command)")
	decideURL := fs.String("decide-url", "", "with --wake "+WakeLLM+": base URL of an OpenAI-compatible API, for example http://127.0.0.1:11434/v1; with --wake "+WakeDecisionModel+": the full URL of a System One endpoint (key in "+EnvDecideKey+" or the variable that agent.wake.key-env names) (agent.wake.url)")
	decideModel := fs.String("decide-model", "", "the model name for --decide-url (agent.wake.model)")
	decideThreshold := fs.Float64("decide-threshold", 0.5, "with --wake "+WakeDecisionModel+": the probability from which an answer counts as yes (agent.wake.threshold)")
	decideTimeout := fs.Duration("decide-timeout", 10*time.Second, "after this time without an answer, a check of the model has failed (agent.wake.timeout)")
	topicTask := fs.String("topic-task", "", "the task of the role when a block of topics has ended (with --wake "+WakeLLM+" or "+WakeDecisionModel+") (agent.topic-task)")
	return func(args []string) error {
		set := map[string]bool{}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		switch {
		case set["role"] && set["role-file"]:
			return usagef("give --role or --role-file, not both")
		case set["decide-cmd"] && set["decide-url"]:
			return usagef("give --decide-cmd or --decide-url, not both")
		case set["decide-threshold"] && (*decideThreshold <= 0 || *decideThreshold > 1):
			return usagef("--decide-threshold must be above 0 and at most 1")
		case set["decide-timeout"] && *decideTimeout <= 0:
			return usagef("--decide-timeout must be positive")
		}
		if _, ok := wakeKeys[*wake]; set["wake"] && !ok {
			return usagef("--wake must be %s, %s or %s", WakeTurnEnd, WakeLLM, WakeDecisionModel)
		}
		start, err := os.Getwd()
		if err != nil {
			return err
		}
		s, _, err := loadConfig(start)
		if err != nil {
			return err
		}
		if v := os.Getenv(EnvSession); v != "" {
			s.apply(layer{src: source{Kind: sourceEnv, From: EnvSession}, values: map[string]any{keySession: v}})
		}
		flagValues := map[string]any{"session": e.session, "role": *role, "role-file": *roleFile,
			"allow": []string(allow), "cwd": *cwd, "topic-task": *topicTask, "wake": *wake,
			"decide-cmd": *decideCmd, "decide-url": *decideURL, "decide-model": *decideModel,
			"decide-threshold": *decideThreshold, "decide-timeout": *decideTimeout}
		for _, f := range agentFlags {
			if set[f.flag] {
				s.apply(layer{src: source{Kind: sourceFlag, From: "--" + f.flag}, values: map[string]any{f.key: flagValues[f.flag]}})
			}
		}
		if len(args) > 0 {
			s.apply(layer{src: source{Kind: sourceFlag, From: "the command after --"}, values: map[string]any{keyHarness: args}})
		}
		if err := checkWake(s); err != nil {
			return err
		}
		harness := s.list(keyHarness)
		if len(harness) == 0 {
			return usagef("no harness command: office agent run --session S -- <harness command>, or set agent.harness in a configuration file")
		}
		session := s.str(keySession)
		if session == "" {
			return usagef("no session: pass --session, set %s or set agent.session in a configuration file", EnvSession)
		}
		r := s.str(keyRole)
		if f := s.str(keyRoleFile); f != "" {
			b, err := os.ReadFile(f)
			if err != nil {
				return usagef("role file of %s: %v", s.name(keyRoleFile), err)
			}
			r = string(b)
		}
		keyEnv := s.str(keyKeyEnv)
		key := os.Getenv(keyEnv)
		if key == "" && s[keyKeyEnv].Source.Kind != sourceDefault && s.has(keyURL) {
			return usagef("%s names the environment variable %s, which is not set", s.name(keyKeyEnv), keyEnv)
		}
		if RunAgent == nil {
			return errors.New("this office binary has no agent runner")
		}
		office, err := os.Executable()
		if err != nil {
			return err
		}
		threshold, _ := s[keyThreshold].Value.(float64)
		timeout, _ := s[keyTimeout].Value.(time.Duration)
		return RunAgent(e.ctx, AgentOptions{Config: e.cfg, Session: session, Role: r, Harness: harness,
			Allow: s.list(keyAllow), Cwd: s.str(keyCwd), Office: office, Version: Version, Stderr: e.stderr,
			Wake: s.str(keyStrategy), DecideCommand: s.str(keyCommand), DecideURL: s.str(keyURL),
			DecideModel: s.str(keyModel), DecideKey: key, DecideThreshold: threshold, DecideTimeout: timeout,
			TopicTask: s.str(keyTopicTask)})
	}
}

// checkWake checks that the settings of the model fit the wake strategy. A setting that does
// not fit is an error when it comes from a flag. When it comes from a file, it is left out, so
// that a flag such as --wake turn-end can override the strategy of a file.
func checkWake(s settings) error {
	strategy := s.str(keyStrategy)
	for _, k := range s.unused() {
		if s[k].Source.Kind == sourceFlag {
			return usagef("%s does not go with the wake strategy %s of %s", flagOf(k), strategy, s.name(keyStrategy))
		}
		delete(s, k)
	}
	switch strategy {
	case WakeLLM:
		switch {
		case !s.has(keyCommand) && !s.has(keyURL):
			return usagef("the wake strategy %s of %s needs %s or %s", WakeLLM, s.name(keyStrategy), s.name(keyCommand), s.name(keyURL))
		case s.has(keyURL) && !s.has(keyModel):
			return usagef("%s needs %s", s.name(keyURL), s.name(keyModel))
		}
	case WakeDecisionModel:
		if !s.has(keyURL) || !s.has(keyModel) {
			return usagef("the wake strategy %s of %s needs %s and %s", WakeDecisionModel, s.name(keyStrategy), s.name(keyURL), s.name(keyModel))
		}
	}
	return nil
}

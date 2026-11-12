package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mrsimpson/agentic-office/client/cli/commands"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/replicator/control"

	"connectrpc.com/connect/v2"

	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
)

// Dial dials the office hub (if-client-core). The wiring of the participant client (client/) sets
// it.
var Dial coreapi.Dial

// NewReplicator creates a log replicator (if-replication) for "office replicator run". The wiring
// of the participant client (client/) sets it.
var NewReplicator control.New

// Version is set at build time with -ldflags "-X .../client/cli.Version=...".
var Version = "dev"

// env is what every command gets.
type env struct {
	ctx            context.Context
	stdout, stderr io.Writer
	stdin          io.Reader
	json           bool
	session        string
	cfg            coreapi.Config
}

// commandTable maps a command path (one, two or three words) to its implementation. The function
// adds the command's own flags and returns the function that runs it with the positional
// arguments.
var commandTable map[string]func(e *env, fs *flag.FlagSet) func(args []string) error

func init() {
	commandTable = map[string]func(e *env, fs *flag.FlagSet) func(args []string) error{
		"key create":               cmdKeyCreate,
		"key show":                 cmdKeyShow,
		"whoami":                   cmdWhoami,
		"admin user create":        cmdAdminUserCreate,
		"admin key add":            cmdAdminKeyAdd,
		"admin key revoke":         cmdAdminRevoke,
		"meeting join":             cmdJoin,
		"models download":          cmdModelsDownload,
		"models list":              cmdModelsList,
		"vocabulary extract":       cmdVocabularyExtract,
		"meeting log":              cmdLogPath,
		"meeting events":           cmdEvents,
		"meeting view":             cmdView,
		"meeting leave":            cmdLeave,
		"meeting pause":            cmdSimple("pause", coreapi.Client.Pause),
		"meeting resume":           cmdSimple("resume", coreapi.Client.Resume),
		"meeting end":              cmdSimple("end", coreapi.Client.EndSession),
		"session import":           cmdImport,
		"say":                      cmdSay,
		"utterance off-the-record": cmdOffTheRecord,
		"signal turn-end":          cmdTurnEnd,
		"signal silence-long":      cmdSilenceLong,
		"signal addressed":         cmdAddressed,
		"hand raise":               cmdRaiseHand,
		"minutes update":           cmdMinutes,
		"stage show":               cmdStageShow,
		"stage link":               cmdStageLink,
		"slides render":            cmdSlidesRender,
		"slides get":               cmdSlidesGet,
		"decision record":          cmdDecisionRecord,
		"decision list":            cmdDecisionList,
		"note add":                 cmdNoteAdd,
		"note list":                cmdNoteList,
		"minutes show":             cmdMinutesShow,
		"minutes list":             cmdMinutesList,
		"task create":              cmdTaskCreate,
		"task get":                 cmdTaskGet,
		"task list":                cmdTaskList,
		"task update":              cmdTaskUpdate,
		"replicator run":           cmdReplicatorRun,
		"mcp":                      cmdMCP,
		"agent run":                cmdAgentRun,
		"config show":              cmdConfigShow,
	}
}

// usageError is a wrong call. It exits with commands.ExitUsage.
type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func usagef(format string, args ...any) error { return usageError{fmt.Sprintf(format, args...)} }

var errHelp = errors.New("help")

// Run runs the CLI and returns the exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return RunContext(ctx, args, os.Stdin, stdout, stderr)
}

// RunContext runs the CLI with a context and stdin.
func RunContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, commands.Usage)
		return commands.ExitUsage
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, Version)
		return commands.ExitOK
	case "help", "-h", "--help":
		fmt.Fprint(stdout, commands.Usage)
		return commands.ExitOK
	}
	name, rest := "", args
	for n := 3; n >= 1; n-- {
		if len(args) >= n {
			if _, ok := commandTable[strings.Join(args[:n], " ")]; ok {
				name, rest = strings.Join(args[:n], " "), args[n:]
				break
			}
		}
	}
	e := &env{ctx: ctx, stdout: stdout, stderr: stderr, stdin: stdin, cfg: coreapi.ConfigFromEnv(),
		session: os.Getenv("OFFICE_SESSION"), json: os.Getenv("OFFICE_JSON") == "1"}
	if name == "" {
		for _, a := range args {
			if a == "--json" || a == "-json" {
				e.json = true
			}
		}
		if !e.json {
			fmt.Fprintf(stderr, "office: unknown command %q\n\n%s", strings.Join(args, " "), commands.Usage)
			return commands.ExitUsage
		}
		return e.fail(usagef("unknown command %q (see \"office help\")", strings.Join(args, " ")))
	}
	fs := flag.NewFlagSet("office "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&e.json, "json", e.json, "print one JSON object on stdout")
	fs.StringVar(&e.session, "session", e.session, "session ID")
	fs.StringVar(&e.cfg.Hub, "hub", e.cfg.Hub, "office hub URL")
	fs.StringVar(&e.cfg.CACert, "ca-cert", e.cfg.CACert, "server certificate to trust (PEM)")
	fs.StringVar(&e.cfg.Tenant, "tenant", e.cfg.Tenant, "tenant to work in")
	fs.StringVar(&e.cfg.KeyFile, "key", e.cfg.KeyFile, "private key file")
	fs.StringVar(&e.cfg.RuntimeDir, "runtime-dir", e.cfg.RuntimeDir, "directory for local logs and state")
	runCmd := commandTable[name](e, fs)
	pos, err := parseInterspersed(fs, rest)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(stdout, "usage: office %s [flags]\n\nFlags:\n", name)
		fs.SetOutput(stdout)
		fs.PrintDefaults()
		return commands.ExitOK
	}
	if err != nil {
		return e.fail(usagef("%v", err))
	}
	return e.fail(runCmd(pos))
}

// parseInterspersed parses flags that may come before, between or after positional arguments.
// Everything after "--" is positional.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		consumed := len(args) - fs.NArg()
		if consumed > 0 && args[consumed-1] == "--" {
			return append(pos, fs.Args()...), nil
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// fail prints err and returns the exit code. A nil error is success.
func (e *env) fail(err error) int {
	if err == nil {
		return commands.ExitOK
	}
	code, exit := errorCode(err), commands.ExitError
	var u usageError
	if errors.As(err, &u) {
		exit = commands.ExitUsage
	}
	msg := err.Error()
	if e.json {
		e.printJSON(commands.ErrorJSON(code, msg))
	} else {
		fmt.Fprintf(e.stderr, "office: %s\n", msg)
		if exit == commands.ExitUsage {
			fmt.Fprintln(e.stderr, "run \"office help\" for the list of commands")
		}
	}
	return exit
}

// errorCode returns the code of an error in --json output: the Connect code for errors of the
// server, else a code of the CLI.
func errorCode(err error) string {
	var u usageError
	var ce *connect.Error
	switch {
	case errors.As(err, &u):
		return commands.CodeUsage
	case errors.Is(err, coreapi.ErrNoKey):
		return commands.CodeNoKey
	case errors.Is(err, coreapi.ErrNoSession):
		return commands.CodeNoSession
	case errors.Is(err, errEarsNotImplemented):
		return commands.CodeUnimplemented
	case errors.As(err, &ce):
		return ce.Code().String()
	}
	return commands.CodeError
}

func (e *env) printJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(e.stderr, "office: %v\n", err)
		return
	}
	fmt.Fprintf(e.stdout, "%s\n", b)
}

// eventJSON returns the event as in the local log.
func eventJSON(ev *sessionstreamv1.Event) json.RawMessage { return coreapi.EventJSON(ev) }

// printResult prints the result of a session command.
func (e *env) printResult(res *sessioncommandsv1.CommandResult) error {
	if e.json {
		e.printJSON(coreapi.ResultJSON(res))
		return nil
	}
	dup := ""
	if res.GetDuplicate() {
		dup = " (duplicate)"
	}
	fmt.Fprintf(e.stdout, "event %d %s%s\n", res.GetEvent().GetId(), res.GetEvent().GetType(), dup)
	return nil
}

// client dials the server with the configuration of e.
func (e *env) client() (coreapi.Client, error) {
	if Dial == nil {
		return nil, errors.New("no client core: the wiring of the participant client sets cli.Dial")
	}
	return Dial(e.cfg)
}

// progress prints a line of progress to stderr; with --json, as {"progress": text}.
func (e *env) progress(text string) {
	if e.json {
		b, _ := json.Marshal(map[string]string{"progress": text})
		fmt.Fprintln(e.stderr, string(b))
		return
	}
	fmt.Fprintln(e.stderr, text)
}

// sessionID returns --session or the only session this participant joined on this machine.
func (e *env) sessionID(c coreapi.Client) (string, error) {
	if e.session != "" {
		return e.session, nil
	}
	return c.DefaultSession(e.ctx)
}

// sessionCommand dials, finds the session and runs f.
func (e *env) sessionCommand(f func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error)) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	sid, err := e.sessionID(c)
	if err != nil {
		return err
	}
	res, err := f(c, sid)
	if err != nil {
		return err
	}
	return e.printResult(res)
}

func noArgs(args []string) error {
	if len(args) > 0 {
		return usagef("unexpected arguments: %s", strings.Join(args, " "))
	}
	return nil
}

// cmdSimple is a session command without arguments.
func cmdSimple(_ string, f func(coreapi.Client, context.Context, string) (*sessioncommandsv1.CommandResult, error)) func(e *env, fs *flag.FlagSet) func([]string) error {
	return func(e *env, fs *flag.FlagSet) func([]string) error {
		return func(args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			return e.sessionCommand(func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error) {
				return f(c, e.ctx, sid)
			})
		}
	}
}

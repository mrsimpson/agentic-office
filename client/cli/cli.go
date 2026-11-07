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

	"connectrpc.com/connect/v2"

	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"

	"github.com/mrsimpson/agentic-office/client/core"
)

// Version is set at build time with -ldflags "-X .../client/cli.Version=...".
var Version = "dev"

const usage = `usage: office <command> [flags]

Keys and identity:
  key create                         create a key pair (if there is none) and print the public key
  key show                           print the public key and key ID
  whoami                             authenticate and print the participant ID
  admin key register <participant> <public-key> [--name N] [--kind human|agent]
  admin key revoke <key-id>

Meeting:
  meeting join <session> [--name N] [--foreground] [--mic | --wav FILE]
                                     join, start the log replicator, print the log path
  meeting log                        print the path of the local log
  meeting view                       gateway view: own utterances; keys p pause, o off the record, q quit
  meeting events [--after N] [--limit N]  events after a cursor, read from the server (for gaps or without a file system)
  meeting leave | pause | resume | end
  say <text>                         submit a typed utterance
  utterance off-the-record <event-id> | --last
  signal turn-end [--participant P]
  signal silence-long --duration D
  signal addressed --participant P --utterance ID
  hand raise [--topic T]
  minutes update <text> | --file F   (--file - reads stdin)
  stage show <artifact-id>
  stage link                         create a read-only link to the stage

Slides (the ID, such as slides/42, is what stage show takes):
  slides render <deck.json | deck.md>  render a deck and print its ID
  slides get <id> [--out FILE]       the rendered deck (--json: input and output)

Records (never put a sentence that is off the record into them):
  decision record <text> | --file F  record a decision (--session links it to a session)
  decision list [--after N] [--limit N]
  note add <text> | --file F         add a note (--session links it to a session)
  note list [--after N] [--limit N]
  minutes show [<session>]           the minutes stored when the session ended
  minutes list [--after N] [--limit N]
  (the list commands take --session to show only the records of one session)

Tasks (local drafts in the hub for now; a ref such as local:42 is opaque):
  task create <title> [--description D | --description-file F] [--assignee P] [--status S]
                                     create a task (--session links it to a session)
  task get <ref>
  task list [--status S[,S]] [--assignee P] [--page-token T] [--limit N]  (--session filters)
  task update <ref> [--title T] [--description D | --description-file F] [--assignee P] [--status S]
                                     change a task (--session S links it, --session "" removes the link)
  (statuses: open, in_progress, done, cancelled)

MCP:
  mcp                                run the MCP server on stdin/stdout (for an MCP client; see README.md)

Other:
  version                            print the version
  help                               print this help

Flags of every command:
  --json            print one JSON object on stdout (env OFFICE_JSON=1)
  --session S       the session; default: the only session joined from this machine (env OFFICE_SESSION)
  --hub URL         office hub (env OFFICE_HUB, default https://127.0.0.1:8740)
  --ca-cert FILE    trust this server certificate (env OFFICE_CA_CERT)
  --key FILE        private key of this actor (env OFFICE_KEY)
  --runtime-dir D   directory for local logs and state (env OFFICE_RUNTIME_DIR)

Exit codes: 0 success, 1 error, 2 usage error. See README.md for the --json format.
`

// env is what every command gets.
type env struct {
	ctx            context.Context
	stdout, stderr io.Writer
	stdin          io.Reader
	json           bool
	session        string
	cfg            core.Config
}

// commands maps a command path (one, two or three words) to its implementation. The function
// adds the command's own flags and returns the function that runs it with the positional
// arguments.
var commands map[string]func(e *env, fs *flag.FlagSet) func(args []string) error

func init() {
	commands = map[string]func(e *env, fs *flag.FlagSet) func(args []string) error{
		"key create":               cmdKeyCreate,
		"key show":                 cmdKeyShow,
		"whoami":                   cmdWhoami,
		"admin key register":       cmdAdminRegister,
		"admin key revoke":         cmdAdminRevoke,
		"meeting join":             cmdJoin,
		"meeting log":              cmdLogPath,
		"meeting events":           cmdEvents,
		"meeting view":             cmdView,
		"meeting leave":            cmdLeave,
		"meeting pause":            cmdSimple("pause", (*core.Client).Pause),
		"meeting resume":           cmdSimple("resume", (*core.Client).Resume),
		"meeting end":              cmdSimple("end", (*core.Client).EndSession),
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
	}
}

// usageError is a wrong call. It exits with code 2.
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
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, Version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	name, rest := "", args
	for n := 3; n >= 1; n-- {
		if len(args) >= n {
			if _, ok := commands[strings.Join(args[:n], " ")]; ok {
				name, rest = strings.Join(args[:n], " "), args[n:]
				break
			}
		}
	}
	e := &env{ctx: ctx, stdout: stdout, stderr: stderr, stdin: stdin, cfg: core.ConfigFromEnv(),
		session: os.Getenv("OFFICE_SESSION"), json: os.Getenv("OFFICE_JSON") == "1"}
	if name == "" {
		for _, a := range args {
			if a == "--json" || a == "-json" {
				e.json = true
			}
		}
		if !e.json {
			fmt.Fprintf(stderr, "office: unknown command %q\n\n%s", strings.Join(args, " "), usage)
			return 2
		}
		return e.fail(usagef("unknown command %q (see \"office help\")", strings.Join(args, " ")))
	}
	fs := flag.NewFlagSet("office "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&e.json, "json", e.json, "print one JSON object on stdout")
	fs.StringVar(&e.session, "session", e.session, "session ID")
	fs.StringVar(&e.cfg.Hub, "hub", e.cfg.Hub, "office hub URL")
	fs.StringVar(&e.cfg.CACert, "ca-cert", e.cfg.CACert, "server certificate to trust (PEM)")
	fs.StringVar(&e.cfg.KeyFile, "key", e.cfg.KeyFile, "private key file")
	fs.StringVar(&e.cfg.RuntimeDir, "runtime-dir", e.cfg.RuntimeDir, "directory for local logs and state")
	runCmd := commands[name](e, fs)
	pos, err := parseInterspersed(fs, rest)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(stdout, "usage: office %s [flags]\n\nFlags:\n", name)
		fs.SetOutput(stdout)
		fs.PrintDefaults()
		return 0
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
		return 0
	}
	code, exit := errorCode(err), 1
	var u usageError
	if errors.As(err, &u) {
		exit = 2
	}
	msg := err.Error()
	if e.json {
		e.printJSON(map[string]any{"error": map[string]string{"code": code, "message": msg}})
	} else {
		fmt.Fprintf(e.stderr, "office: %s\n", msg)
		if exit == 2 {
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
		return "usage"
	case errors.Is(err, core.ErrNoKey):
		return "no_key"
	case errors.Is(err, core.ErrNoSession):
		return "no_session"
	case errors.Is(err, errEarsNotImplemented):
		return "unimplemented"
	case errors.As(err, &ce):
		return ce.Code().String()
	}
	return "error"
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
func eventJSON(ev *sessionv1.Event) json.RawMessage { return core.EventJSON(ev) }

// printResult prints the result of a session command.
func (e *env) printResult(res *sessionv1.CommandResult) error {
	if e.json {
		e.printJSON(core.ResultJSON(res))
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
func (e *env) client() (*core.Client, error) { return core.Dial(e.cfg) }

// sessionID returns --session or the only session this participant joined on this machine.
func (e *env) sessionID(c *core.Client) (string, error) {
	if e.session != "" {
		return e.session, nil
	}
	return c.DefaultSession(e.ctx)
}

// sessionCommand dials, finds the session and runs f.
func (e *env) sessionCommand(f func(c *core.Client, sid string) (*sessionv1.CommandResult, error)) error {
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
func cmdSimple(_ string, f func(*core.Client, context.Context, string) (*sessionv1.CommandResult, error)) func(e *env, fs *flag.FlagSet) func([]string) error {
	return func(e *env, fs *flag.FlagSet) func([]string) error {
		return func(args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			return e.sessionCommand(func(c *core.Client, sid string) (*sessionv1.CommandResult, error) {
				return f(c, e.ctx, sid)
			})
		}
	}
}

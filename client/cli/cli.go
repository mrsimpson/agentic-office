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

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"

	"github.com/mrsimpson/agentic-office/client/participant"
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
  meeting view                       gateway view: own utterances; keys p pause, r retract, q quit
  meeting leave | pause | resume | end
  say <text>                         submit a typed utterance
  utterance retract <event-id> | --last
  signal turn-end [--participant P]
  signal silence-long --duration D
  signal addressed --participant P --utterance ID
  hand raise [--topic T]
  minutes update <text> | --file F   (--file - reads stdin)
  stage show <artifact-id>
  stage link                         create a read-only link to the stage

Artifacts and journal:
  slides render <deck.json | deck.md>
  artifact get <id> [--out FILE]
  journal append --kind decision|task|minutes|note <text>
  journal list [--kind K] [--after N] [--limit N]

MCP:
  mcp                                run the MCP server on stdin/stdout (for an MCP client; see README.md)

Other:
  version                            print the version
  help                               print this help

Flags of every command:
  --json            print one JSON object on stdout (env OFFICE_JSON=1)
  --session S       the session; default: the only session joined from this machine (env OFFICE_SESSION)
  --server URL      office server (env OFFICE_SERVER, default https://127.0.0.1:8740)
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
	cfg            participant.Config
}

// commands maps a command path (one, two or three words) to its implementation. The function
// adds the command's own flags and returns the function that runs it with the positional
// arguments.
var commands map[string]func(e *env, fs *flag.FlagSet) func(args []string) error

func init() {
	commands = map[string]func(e *env, fs *flag.FlagSet) func(args []string) error{
		"key create":          cmdKeyCreate,
		"key show":            cmdKeyShow,
		"whoami":              cmdWhoami,
		"admin key register":  cmdAdminRegister,
		"admin key revoke":    cmdAdminRevoke,
		"meeting join":        cmdJoin,
		"meeting log":         cmdLogPath,
		"meeting view":        cmdView,
		"meeting leave":       cmdLeave,
		"meeting pause":       cmdSimple("pause", (*participant.Client).Pause),
		"meeting resume":      cmdSimple("resume", (*participant.Client).Resume),
		"meeting end":         cmdSimple("end", (*participant.Client).EndSession),
		"say":                 cmdSay,
		"utterance retract":   cmdRetract,
		"signal turn-end":     cmdTurnEnd,
		"signal silence-long": cmdSilenceLong,
		"signal addressed":    cmdAddressed,
		"hand raise":          cmdRaiseHand,
		"minutes update":      cmdMinutes,
		"stage show":          cmdStageShow,
		"stage link":          cmdStageLink,
		"slides render":       cmdSlidesRender,
		"artifact get":        cmdArtifactGet,
		"journal append":      cmdJournalAppend,
		"journal list":        cmdJournalList,
		"replicator run":      cmdReplicatorRun,
		"mcp":                 cmdMCP,
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
	e := &env{ctx: ctx, stdout: stdout, stderr: stderr, stdin: stdin, cfg: participant.ConfigFromEnv(),
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
	fs.StringVar(&e.cfg.Server, "server", e.cfg.Server, "office server URL")
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
	case errors.Is(err, participant.ErrNoKey):
		return "no_key"
	case errors.Is(err, participant.ErrNoSession):
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
func eventJSON(ev *officev1.Event) json.RawMessage { return participant.EventJSON(ev) }

// printResult prints the result of a session command.
func (e *env) printResult(res *officev1.CommandResult) error {
	if e.json {
		e.printJSON(participant.ResultJSON(res))
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
func (e *env) client() (*participant.Client, error) { return participant.Dial(e.cfg) }

// sessionID returns --session or the only session this participant joined on this machine.
func (e *env) sessionID(c *participant.Client) (string, error) {
	if e.session != "" {
		return e.session, nil
	}
	return c.DefaultSession(e.ctx)
}

// sessionCommand dials, finds the session and runs f.
func (e *env) sessionCommand(f func(c *participant.Client, sid string) (*officev1.CommandResult, error)) error {
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
func cmdSimple(_ string, f func(*participant.Client, context.Context, string) (*officev1.CommandResult, error)) func(e *env, fs *flag.FlagSet) func([]string) error {
	return func(e *env, fs *flag.FlagSet) func([]string) error {
		return func(args []string) error {
			if err := noArgs(args); err != nil {
				return err
			}
			return e.sessionCommand(func(c *participant.Client, sid string) (*officev1.CommandResult, error) {
				return f(c, e.ctx, sid)
			})
		}
	}
}

func entryJSON(en *officev1.JournalEntry) json.RawMessage { return participant.MessageJSON(en) }

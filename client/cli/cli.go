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
		"key create":                cmdKeyCreate,
		"key show":                  cmdKeyShow,
		"whoami":                    cmdWhoami,
		"admin user create":         cmdAdminUserCreate,
		"admin user list":           cmdAdminUserList,
		"admin user remove":         cmdAdminUserRemove,
		"admin system-admin add":    cmdAdminSystemAdmin(true),
		"admin system-admin remove": cmdAdminSystemAdmin(false),
		"admin tenant create":       cmdAdminTenantCreate,
		"admin tenant list":         cmdAdminTenantList,
		"admin tenant remove":       cmdAdminTenantRemove,
		"admin member set":          cmdAdminMemberSet,
		"admin member remove":       cmdAdminMemberRemove,
		"admin member list":         cmdAdminMemberList,
		"admin key add":             cmdAdminKeyAdd,
		"admin key revoke":          cmdAdminRevoke,
		"meeting join":              cmdJoin,
		"models download":           cmdModelsDownload,
		"models list":               cmdModelsList,
		"vocabulary extract":        cmdVocabularyExtract,
		"voice enrol":               cmdVoiceEnrol,
		"voice show":                cmdVoiceShow,
		"voice delete":              cmdVoiceDelete,
		"meeting log":               cmdLogPath,
		"meeting events":            cmdEvents,
		"meeting view":              cmdView,
		"meeting leave":             cmdLeave,
		"meeting pause":             cmdSimple("pause", coreapi.Client.Pause),
		"meeting resume":            cmdSimple("resume", coreapi.Client.Resume),
		"meeting end":               cmdMeetingEnd,
		"session import":            cmdImport,
		"session list":              cmdSessionList,
		"session release":           cmdSessionRelease,
		"session hold":              cmdSessionHold,
		"say":                       cmdSay,
		"utterance off-the-record":  cmdOffTheRecord,
		"signal turn-end":           cmdTurnEnd,
		"signal silence-long":       cmdSilenceLong,
		"signal addressed":          cmdAddressed,
		"hand raise":                cmdRaiseHand,
		"minutes update":            cmdMinutes,
		"stage focus":               cmdStageFocus,
		"portal link":               cmdPortalLink,
		"stage link":                cmdStageLink,
		"slides render":             cmdSlidesRender,
		"slides get":                cmdSlidesGet,
		"slides change":             cmdSlidesChange,
		"slides remove":             cmdSlidesRemove,
		"decision record":           cmdDecisionRecord,
		"decision list":             cmdDecisionList,
		"note add":                  cmdNoteAdd,
		"note list":                 cmdNoteList,
		"minutes show":              cmdMinutesShow,
		"minutes list":              cmdMinutesList,
		"decision change":           cmdDecisionChange,
		"decision remove":           cmdDecisionRemove,
		"note change":               cmdNoteChange,
		"note remove":               cmdNoteRemove,
		"minutes change":            cmdMinutesChange,
		"minutes remove":            cmdMinutesRemove,
		"task create":               cmdTaskCreate,
		"task get":                  cmdTaskGet,
		"task list":                 cmdTaskList,
		"task update":               cmdTaskUpdate,
		"replicator run":            cmdReplicatorRun,
		"mcp":                       cmdMCP,
		"agent run":                 cmdAgentRun,
		"team-agent create":         cmdTeamAgentCreate,
		"team-agent list":           cmdTeamAgentList,
		"team-agent remove":         cmdTeamAgentRemove,
		"config show":               cmdConfigShow,
		"hub add":                   cmdHubAdd,
		"hub list":                  cmdHubList,
		"hub remove":                cmdHubRemove,
		"document create":           cmdDocumentCreate,
		"document change":           cmdDocumentChange,
		"document remove":           cmdDocumentRemove,
		"document get":              cmdDocumentGet,
		"document list":             cmdDocumentList,
		"sync":                      cmdSyncUpdate,
		"sync follow":               cmdSyncFollow,
		"sync init":                 cmdSyncInit,
		"sync credential":           cmdSyncCredential,
	}
	checkHelp()
}

// checkHelp stops the start when a command has no help (client/cli/commands/help.go), or help
// exists for a command that does not exist. Every command must explain itself.
func checkHelp() {
	for name := range commandTable {
		if _, ok := commands.Lookup(name); !ok {
			panic("office: the command " + name + " has no help text in client/cli/commands/help.go")
		}
	}
	for _, name := range commands.Names() {
		if _, ok := commandTable[name]; !ok && name != "help" && name != "version" {
			panic("office: help text for the unknown command " + name + " in client/cli/commands/help.go")
		}
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
		fmt.Fprint(stderr, commands.Overview())
		return commands.ExitUsage
	}
	if (args[0] == "version" || args[0] == "help") && len(args) == 2 && isHelpFlag(args[1]) {
		printHelp(stdout, args[0], nil)
		return commands.ExitOK
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, Version)
		return commands.ExitOK
	case "-h", "--help":
		fmt.Fprint(stdout, commands.Overview())
		return commands.ExitOK
	case "help":
		if len(args) == 1 {
			fmt.Fprint(stdout, commands.Overview())
			return commands.ExitOK
		}
		if t, ok := commands.Topic(strings.Join(args[1:], " ")); ok {
			fmt.Fprint(stdout, t)
			return commands.ExitOK
		}
		// "office help meeting join" is "office meeting join --help".
		return RunContext(ctx, append(append([]string{}, args[1:]...), "--help"), stdin, stdout, stderr)
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
		return e.unknownCommand(args)
	}
	fs := flag.NewFlagSet("office "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&e.json, "json", e.json, "print one JSON object on stdout")
	fs.StringVar(&e.session, "session", e.session, "session ID")
	// The flags of the connection are read after parsing, with the files and the environment
	// (resolveConnection). An empty value is a value: --tenant "" means no tenant.
	for _, f := range []struct{ name, usage string }{
		{"hub", "office hub URL (env OFFICE_HUB)"},
		{"ca-cert", "server certificate to trust, PEM (env OFFICE_CA_CERT)"},
		{"tenant", "tenant to work in (env OFFICE_TENANT)"},
		{"key", "private key file (env OFFICE_KEY)"},
		{"hub-profile", "hub profile to use (env OFFICE_HUB_PROFILE)"},
	} {
		fs.String(f.name, "", f.usage)
	}
	fs.StringVar(&e.cfg.RuntimeDir, "runtime-dir", e.cfg.RuntimeDir, "directory for local logs and state")
	runCmd := commandTable[name](e, fs)
	pos, err := parseInterspersed(fs, rest)
	if errors.Is(err, flag.ErrHelp) {
		printHelp(stdout, name, fs)
		return commands.ExitOK
	}
	if err != nil {
		return e.fail(usagef("%v", err))
	}
	if !ownsConnection[name] {
		start, err := os.Getwd()
		if err != nil {
			return e.fail(err)
		}
		c, _, err := loadConnection(start, visitedFlags(fs, connectionFlagNames))
		if err != nil {
			return e.fail(err)
		}
		e.cfg = c.Config(e.cfg)
	}
	return e.fail(runCmd(pos))
}

func isHelpFlag(a string) bool { return a == "-h" || a == "--help" || a == "-help" }

// unknownCommand answers words that are no command. If they are the start of commands, such as
// "meeting" or "admin tenant", it lists those commands: on stdout with exit 0 when help was
// asked for, else on stderr as a usage error. Otherwise it names the commands that were
// probably meant.
func (e *env) unknownCommand(args []string) int {
	var words []string
	help := false
	for _, a := range args {
		switch {
		case a == "--json" || a == "-json":
			e.json = true
		case isHelpFlag(a):
			help = true
		case !strings.HasPrefix(a, "-"):
			words = append(words, a)
		}
	}
	typed := strings.Join(words, " ")
	if typed != "" && len(commands.Subcommands(typed)) > 0 {
		if help {
			fmt.Fprint(e.stdout, commands.GroupHelp(typed))
			return commands.ExitOK
		}
		if !e.json {
			fmt.Fprintf(e.stderr, "office: %q needs a command\n\n%s", typed, commands.GroupHelp(typed))
			return commands.ExitUsage
		}
		return e.fail(usagef("%q needs a command (see \"office %s --help\")", typed, typed))
	}
	msg := fmt.Sprintf("unknown command %q", typed)
	if s := commands.Suggest(typed); len(s) > 0 {
		msg += "; did you mean \"" + strings.Join(s, "\" or \"") + "\"?"
	}
	if !e.json {
		fmt.Fprintf(e.stderr, "office: %s\nrun \"office help\" for the list of commands\n", msg)
		return commands.ExitUsage
	}
	return e.fail(usagef("%s (see \"office help\")", msg))
}

// commonFlags are the flags that every command takes, with the variable and the default that
// "office help config" explains.
var commonFlags = map[string]bool{"json": true, "session": true, "hub": true, "ca-cert": true, "tenant": true,
	"key": true, "hub-profile": true, "runtime-dir": true}

// printHelp prints the help of a command: purpose, usage, details, its own flags, the common
// flags and examples. fs holds the flags of the command; it is nil for a command without flags.
func printHelp(w io.Writer, name string, fs *flag.FlagSet) {
	h, ok := commands.Lookup(name)
	if !ok {
		fmt.Fprintf(w, "usage: office %s [flags]\n", name)
		return
	}
	fmt.Fprintf(w, "office %s: %s\n\nusage: office %s\n", h.Name, h.Summary, h.Usage)
	if h.Details != "" {
		fmt.Fprintf(w, "\n%s\n", h.Details)
	}
	if sub := commands.Subcommands(name); len(sub) > 0 {
		fmt.Fprintf(w, "\nCommands below %q:\n%s", name, commands.CommandList(sub))
	}
	if fs != nil {
		var own, common []*flag.Flag
		fs.VisitAll(func(f *flag.Flag) {
			if commonFlags[f.Name] {
				common = append(common, f)
			} else {
				own = append(own, f)
			}
		})
		if len(own) > 0 {
			fmt.Fprintf(w, "\nFlags:\n")
			printFlags(w, own)
		}
		fmt.Fprintf(w, "\nCommon flags (variables, files and defaults: \"office help config\"):\n")
		printFlags(w, common)
	}
	if len(h.Examples) > 0 {
		fmt.Fprintf(w, "\nExamples:\n")
		for _, ex := range h.Examples {
			fmt.Fprintf(w, "  %s\n", ex)
		}
	}
}

// printFlags prints flags as "  --name TYPE  usage (default D)".
func printFlags(w io.Writer, fl []*flag.Flag) {
	for _, f := range fl {
		typ, usage := flag.UnquoteUsage(f)
		line := "  --" + f.Name
		if typ != "" {
			line += " " + typ
		}
		line += "\n        " + usage
		switch f.DefValue {
		case "", "0", "false", "0s":
		default:
			line += fmt.Sprintf(" (default %s)", f.DefValue)
		}
		fmt.Fprintln(w, line)
	}
}

// ownsConnection are the commands that resolve the connection themselves, because they show or
// change the files that it comes from.
var ownsConnection = map[string]bool{"config show": true, "hub add": true, "hub list": true, "hub remove": true}

// connectionFlagNames are the flags of the connection, in the order of connectionFlags.
var connectionFlagNames = []string{"hub", "ca-cert", "tenant", "key", "hub-profile"}

// visitedFlags returns the values of the flags in names that were given.
func visitedFlags(fs *flag.FlagSet, names []string) map[string]string {
	out := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if contains(names, f.Name) {
			out[f.Name] = f.Value.String()
		}
	})
	return out
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
	if errors.As(err, new(refusedError)) {
		return commands.ExitError
	}
	code, exit := errorCode(err), commands.ExitError
	var u usageError
	if errors.As(err, &u) {
		exit = commands.ExitUsage
	}
	msg := err.Error()
	if e.json {
		e.printJSON(commands.ErrorJSON(code, msg, coreapi.ErrorDetailJSON(err)))
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

package cli

// The commands of the artifact sync (dec-artifact-sync): office sync init sets up a directory,
// office sync updates it, office sync follow keeps it up to date,
// office sync credential answers Git when it asks for a password. The work lies in the building
// block Artifact sync (client/sync); this file only connects it to the command line.

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsimpson/agentic-office/client/sync/control"
)

// NewSync creates the artifact sync (if-sync). The wiring of the participant client (client/) sets
// it.
var NewSync control.New

// sync creates the artifact sync for the connection of e.
func (e *env) sync(helper []string) (control.Sync, error) {
	if NewSync == nil {
		return nil, errors.New("no artifact sync: the wiring of the participant client sets cli.NewSync")
	}
	c, err := e.client()
	if err != nil {
		return nil, err
	}
	return NewSync(control.Options{Client: c, CredentialHelper: helper, Logf: func(format string, args ...any) {
		e.progress(fmt.Sprintf(format, args...))
	}}), nil
}

// credentialHelper returns the command that Git runs to get a token: this program with the
// connection of e as flags, so the helper works without the files and variables of the moment.
func (e *env) credentialHelper() ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return nil, err
	}
	key, err := filepath.Abs(e.cfg.KeyFile)
	if err != nil {
		return nil, err
	}
	cert := e.cfg.CACert
	if cert != "" {
		if cert, err = filepath.Abs(cert); err != nil {
			return nil, err
		}
	}
	return []string{exe, "sync", "credential", "--hub", e.cfg.Hub, "--tenant", e.cfg.Tenant,
		"--ca-cert", cert, "--key", key}, nil
}

func cmdSyncInit(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		dir := "."
		switch len(args) {
		case 0:
		case 1:
			dir = args[0]
		default:
			return usagef("usage: office sync init [<directory>]")
		}
		if err := guardCommandName(dir); err != nil {
			return err
		}
		helper, err := e.credentialHelper()
		if err != nil {
			return err
		}
		s, err := e.sync(helper)
		if err != nil {
			return err
		}
		res, err := s.Init(e.ctx, dir)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"directory": res.Dir, "repository": res.Repository, "cloned": res.Cloned,
				"author": res.Author})
			return nil
		}
		verb := "set up"
		if !res.Cloned {
			verb = "checked and fixed the configuration of"
		}
		fmt.Fprintf(e.stdout, "%s %s (%s)\ncommits in this directory are refused; work in a worktree: git worktree add ../my-change -b my-change\n",
			verb, res.Dir, res.Repository)
		return nil
	}
}

// printUpdate tells the participant what an update did.
func (e *env) printUpdate(res control.UpdateResult) {
	if e.json {
		e.printJSON(map[string]any{"directory": res.Dir, "updated": res.Updated, "from": res.From, "to": res.To,
			"rewritten": res.Rewritten})
		return
	}
	if res.Rewritten {
		fmt.Fprintf(e.stdout, "updated %s: the hub rewrote the history of main; main moved from %s to the new state %s\n",
			res.Dir, short(res.From), short(res.To))
		return
	}
	if res.Updated {
		fmt.Fprintf(e.stdout, "updated %s: main moved from %s to %s\n", res.Dir, short(res.From), short(res.To))
		return
	}
	fmt.Fprintf(e.stdout, "%s is up to date (%s)\n", res.Dir, short(res.To))
}

func short(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// cmdSyncUpdate is "office sync [<directory>]".
func cmdSyncUpdate(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		dir, err := syncDirectory(args, "usage: office sync [<directory>]")
		if err != nil {
			return err
		}
		s, err := e.sync(nil)
		if err != nil {
			return err
		}
		res, err := s.Update(e.ctx, dir)
		if err != nil {
			return err
		}
		e.printUpdate(res)
		return nil
	}
}

func syncDirectory(args []string, usage string) (string, error) {
	switch len(args) {
	case 0:
		return ".", nil
	case 1:
		return args[0], guardCommandName(args[0])
	}
	return "", usagef("%s", usage)
}

// syncCommandNames are the sub-commands of "office sync".
var syncCommandNames = []string{"init", "follow", "credential"}

// guardCommandName stops when the argument has the name of a sub-command of "office sync" and a
// directory with that name exists in the current directory: the participant may mean the command
// or the directory, and the program does not guess.
func guardCommandName(arg string) error {
	for _, name := range syncCommandNames {
		if arg != name {
			continue
		}
		if fi, err := os.Stat(arg); err == nil && fi.IsDir() {
			return usagef("%q is the name of a command (office sync %s) and of a directory here: "+
				"write ./%s to mean the directory", arg, name, name)
		}
	}
	return nil
}

// prefixList is a flag that may repeat.
type prefixList []string

func (p *prefixList) String() string     { return strings.Join(*p, ",") }
func (p *prefixList) Set(v string) error { *p = append(*p, v); return nil }

// cmdSyncFollow is "office sync follow [<directory>]": it runs until Ctrl-C or SIGTERM.
func cmdSyncFollow(e *env, fs *flag.FlagSet) func([]string) error {
	var prefixes prefixList
	fs.Var(&prefixes, "path", "only changes under this path start an update; repeat for more paths")
	return func(args []string) error {
		dir, err := syncDirectory(args, "usage: office sync follow [<directory>] [--path P]...")
		if err != nil {
			return err
		}
		s, err := e.sync(nil)
		if err != nil {
			return err
		}
		return s.Follow(e.ctx, dir, control.FollowOptions{PathPrefixes: prefixes,
			Updated: func(res control.UpdateResult, err error) {
				if err != nil {
					e.progress("update failed: " + err.Error())
					return
				}
				e.printUpdate(res)
			}})
	}
}

// cmdSyncCredential is the credential helper of Git (see "git help credential"). Git runs it as
// "office sync credential ... get" and writes the attributes of the request on its input.
func cmdSyncCredential(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office sync credential get|store|erase")
		}
		if err := guardCommandName(args[0]); err != nil {
			return err
		}
		s, err := e.sync([]string{"office"})
		if err != nil {
			return err
		}
		return s.Credential(e.ctx, args[0], e.stdin, e.stdout)
	}
}

package cli

// The commands of the artifact sync (dec-artifact-sync): office sync init sets up a directory,
// office sync credential answers Git when it asks for a password. The work lies in the building
// block Artifact sync (client/sync); this file only connects it to the command line.

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

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

// cmdSyncCredential is the credential helper of Git (see "git help credential"). Git runs it as
// "office sync credential ... get" and writes the attributes of the request on its input.
func cmdSyncCredential(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office sync credential get|store|erase")
		}
		s, err := e.sync([]string{"office"})
		if err != nil {
			return err
		}
		return s.Credential(e.ctx, args[0], e.stdin, e.stdout)
	}
}

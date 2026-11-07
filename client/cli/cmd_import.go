package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mrsimpson/agentic-office/client/core"
)

// cmdImport imports a conversation as a new session (dec-session-import). The file is in the
// import format of the client core (dec-import-format, README.md); "-" reads stdin.
func cmdImport(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office session import <conversation.json | -> [--session S]")
		}
		var r io.Reader = e.stdin
		if args[0] != "-" {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			r = f
		}
		conv, err := core.ParseConversation(r)
		if errors.Is(err, core.ErrBadInput) {
			return usagef("%s: %v", args[0], err)
		}
		if err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		im, err := c.Import(e.ctx, e.session, conv)
		if errors.Is(err, core.ErrBadInput) {
			return usagef("%s: %v", args[0], err)
		}
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(core.ImportedJSON(im))
			return nil
		}
		fmt.Fprintf(e.stdout, "%s\n", im.SessionID)
		fmt.Fprintf(e.stderr, "imported %d utterances of %d participants into session %s (events %d to %d)\n",
			im.Utterances, im.Participants, im.SessionID, im.FirstEventID, im.LastEventID)
		return nil
	}
}

package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// cmdImport imports a conversation as a new session, or extends an imported session with a longer
// version of the same conversation (dec-session-import). The file is in the
// import format of the client core (dec-import-format, "office help import-format"); "-" reads stdin.
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
		conv, err := coreapi.ParseConversation(r)
		if errors.Is(err, coreapi.ErrBadInput) {
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
		if errors.Is(err, coreapi.ErrBadInput) {
			return usagef("%s: %v", args[0], err)
		}
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(coreapi.ImportedJSON(im))
			return nil
		}
		fmt.Fprintf(e.stdout, "%s\n", im.SessionID)
		switch {
		case !im.Extended:
			fmt.Fprintf(e.stderr, "imported %d utterances of %d participants into session %s (events %d to %d)\n",
				im.Utterances, im.Participants, im.SessionID, im.FirstEventID, im.LastEventID)
		case im.Pages == 0:
			fmt.Fprintf(e.stderr, "session %s holds all %d utterances of the file already; nothing imported\n",
				im.SessionID, im.Previous)
		default:
			fmt.Fprintf(e.stderr, "extended session %s after its %d imported utterances: added %d utterances and %d participants (events %d to %d)\n",
				im.SessionID, im.Previous, im.Utterances, im.Participants, im.FirstEventID, im.LastEventID)
		}
		return nil
	}
}

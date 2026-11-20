package cli

import (
	"flag"
	"fmt"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
)

// The end and the release of a session (concept-session-release). The hub decides everything;
// these commands pass the call on and print the answer (dec-cli-and-mcp).

// releaseSessionID returns the session of a release command: the argument, else --session or the
// only session joined on this machine.
func (e *env) releaseSessionID(c coreapi.Client, args []string) (string, error) {
	switch len(args) {
	case 0:
		return e.sessionID(c)
	case 1:
		if e.session != "" && e.session != args[0] {
			return "", usagef("give the session once: as the argument or with --session")
		}
		return args[0], nil
	}
	return "", usagef("unexpected arguments: %v", args[1:])
}

// cmdMeetingEnd ends the session for everyone and, with --release, then releases it: two
// commands, because each session command creates one event.
func cmdMeetingEnd(e *env, fs *flag.FlagSet) func([]string) error {
	release := fs.Bool("release", false, "release what the session staged right after the end (humans only)")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		sid, err := e.sessionID(c)
		if err != nil {
			return err
		}
		end, err := c.EndSession(e.ctx, sid)
		if err != nil {
			return err
		}
		if !*release {
			if e.json {
				e.printJSON(coreapi.EndJSON(end))
				return nil
			}
			_ = e.printResult(end.GetResult())
			if hint := coreapi.ReleaseHint(sid, end.GetRelease()); hint != "" {
				fmt.Fprintln(e.stdout, hint)
			}
			return nil
		}
		rel, err := c.ReleaseSession(e.ctx, sid, "")
		if err != nil {
			return fmt.Errorf("the session has ended, but the release failed: %w", err)
		}
		if e.json {
			out := coreapi.EndJSON(end)
			out["released"] = coreapi.ReleaseJSON(rel)
			e.printJSON(out)
			if coreapi.Refused(rel) {
				return errRefused
			}
			return nil
		}
		_ = e.printResult(end.GetResult())
		return e.printRelease(sid, rel)
	}
}

// cmdSessionRelease releases what an ended session staged. While staged files quote sentences off
// the record, the hub refuses; the command prints the places and the token that releases anyway.
func cmdSessionRelease(e *env, fs *flag.FlagSet) func([]string) error {
	accept := fs.String("accept-quotes", "", "release although staged files quote sentences off the record: the token that a refused release printed")
	return func(args []string) error {
		c, err := e.client()
		if err != nil {
			return err
		}
		sid, err := e.releaseSessionID(c, args)
		if err != nil {
			return err
		}
		rel, err := c.ReleaseSession(e.ctx, sid, *accept)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(coreapi.ReleaseJSON(rel))
			if coreapi.Refused(rel) {
				return errRefused
			}
			return nil
		}
		return e.printRelease(sid, rel)
	}
}

// cmdSessionHold objects to the release of an ended session.
func cmdSessionHold(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		c, err := e.client()
		if err != nil {
			return err
		}
		sid, err := e.releaseSessionID(c, args)
		if err != nil {
			return err
		}
		res, err := c.ObjectToRelease(e.ctx, sid)
		if err != nil {
			return err
		}
		if err := e.printResult(res); err != nil || e.json {
			return err
		}
		fmt.Fprintf(e.stdout, "The session is held. A human who took part releases it with \"office session release %s\".\n", sid)
		return nil
	}
}

// printRelease prints the result of a release and every quote of a sentence off the record that
// it accepted, or the refusal because of quotes. A refusal returns errRefused.
func (e *env) printRelease(sid string, rel *sessioncommandsv1.ReleaseSessionResponse) error {
	if coreapi.Refused(rel) {
		fmt.Fprint(e.stdout, coreapi.RefusalText(sid, rel))
		return errRefused
	}
	_ = e.printResult(rel.GetResult())
	for _, q := range rel.GetQuotes() {
		fmt.Fprintf(e.stdout, "accepted: %s\n", coreapi.QuoteLine(q))
	}
	return nil
}

// errRefused ends a command whose refusal is printed already. It exits with an error and prints
// nothing more.
var errRefused = refusedError{}

type refusedError struct{}

func (refusedError) Error() string { return "refused" }

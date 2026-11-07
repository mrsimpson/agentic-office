package cli

import (
	"flag"
	"fmt"

	identityv1 "github.com/mrsimpson/agentic-office/server/identity/api"
	"github.com/mrsimpson/agentic-office/server/identity/api/auth"
)

func (e *env) printKey(path string, pub []byte, created bool) {
	pk, id := auth.FormatPublicKey(pub), auth.KeyID(pub)
	if e.json {
		e.printJSON(map[string]any{"keyFile": path, "publicKey": pk, "keyId": id, "created": created})
		return
	}
	if created {
		fmt.Fprintf(e.stderr, "created a new key in %s\n", path)
	}
	fmt.Fprintf(e.stdout, "public key: %s\nkey ID:     %s\n", pk, id)
}

func cmdKeyCreate(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		key, created, err := auth.LoadOrCreateKey(e.cfg.KeyFile)
		if err != nil {
			return err
		}
		if !created && !e.json {
			fmt.Fprintf(e.stderr, "%s exists; it is not changed\n", e.cfg.KeyFile)
		}
		e.printKey(e.cfg.KeyFile, auth.PublicKey(key), created)
		return nil
	}
}

func cmdKeyShow(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		key, err := auth.ReadKey(e.cfg.KeyFile)
		if err != nil {
			return err
		}
		e.printKey(e.cfg.KeyFile, auth.PublicKey(key), false)
		return nil
	}
}

func cmdWhoami(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		pid, err := c.ParticipantID(e.ctx)
		if err != nil {
			return err
		}
		id := auth.KeyID(auth.PublicKey(c.Key))
		if e.json {
			e.printJSON(map[string]any{"participantId": pid, "keyId": id})
		} else {
			fmt.Fprintln(e.stdout, pid)
		}
		return nil
	}
}

func parseKind(s string) (identityv1.ActorKind, error) {
	switch s {
	case "":
		return identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED, nil
	case "human":
		return identityv1.ActorKind_ACTOR_KIND_HUMAN, nil
	case "agent":
		return identityv1.ActorKind_ACTOR_KIND_AGENT, nil
	}
	return 0, usagef("--kind must be human or agent, not %q", s)
}

func cmdAdminRegister(e *env, fs *flag.FlagSet) func([]string) error {
	name := fs.String("name", "", "display name (required for a new participant)")
	kind := fs.String("kind", "", "human or agent (required for a new participant)")
	return func(args []string) error {
		if len(args) != 2 {
			return usagef("usage: office admin key register <participant> <public-key> [--name N] [--kind human|agent]")
		}
		k, err := parseKind(*kind)
		if err != nil {
			return err
		}
		pub, err := auth.ParsePublicKey(args[1])
		if err != nil {
			return usagef("%v", err)
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		res, err := c.RegisterKey(e.ctx, &identityv1.RegisterKeyRequest{ParticipantId: args[0], DisplayName: *name, Kind: k, PublicKey: pub})
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"participantId": args[0], "keyId": res.GetKeyId()})
		} else {
			fmt.Fprintf(e.stdout, "registered %s for %s\n", res.GetKeyId(), args[0])
		}
		return nil
	}
}

func cmdAdminRevoke(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office admin key revoke <key-id>")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		if err := c.RevokeKey(e.ctx, args[0]); err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"keyId": args[0], "revoked": true})
		} else {
			fmt.Fprintf(e.stdout, "revoked %s\n", args[0])
		}
		return nil
	}
}

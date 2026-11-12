package cli

import (
	"flag"
	"fmt"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
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
		id := c.KeyID()
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

func cmdAdminUserCreate(e *env, fs *flag.FlagSet) func([]string) error {
	name := fs.String("name", "", "display name (required)")
	kind := fs.String("kind", "", "human or agent (required)")
	return func(args []string) error {
		if len(args) != 2 {
			return usagef("usage: office admin user create <user> <public-key> --name N --kind human|agent")
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
		res, err := c.CreateUser(e.ctx, &userv1.CreateUserRequest{UserId: args[0], DisplayName: *name, Kind: k, PublicKey: pub})
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"userId": args[0], "keyId": res.GetKeyId()})
		} else {
			fmt.Fprintf(e.stdout, "created user %s with key %s\n", args[0], res.GetKeyId())
		}
		return nil
	}
}

func cmdAdminKeyAdd(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 2 {
			return usagef("usage: office admin key add <user> <public-key>")
		}
		pub, err := auth.ParsePublicKey(args[1])
		if err != nil {
			return usagef("%v", err)
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		keyID, err := c.AddKey(e.ctx, args[0], pub)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"userId": args[0], "keyId": keyID})
		} else {
			fmt.Fprintf(e.stdout, "added key %s to %s\n", keyID, args[0])
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

// The admin commands of the hub (concept-multi-tenancy): users, tenants, memberships and system
// admins. Who may call a command is decided by the hub; the CLI only passes the call on.

func cmdAdminUserList(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 0 {
			return usagef("usage: office admin user list")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		users, err := c.ListUsers(e.ctx)
		if err != nil {
			return err
		}
		if e.json {
			out := make([]any, 0, len(users))
			for _, u := range users {
				out = append(out, coreapi.MessageJSON(u))
			}
			e.printJSON(map[string]any{"users": out})
			return nil
		}
		for _, u := range users {
			state := "active"
			if u.GetValidUntil() != nil {
				state = "removed"
			}
			role := ""
			if u.GetSystemAdmin() {
				role = " system-admin"
			}
			fmt.Fprintf(e.stdout, "%s\t%s\t%s\t%s%s\n", u.GetUserId(), u.GetDisplayName(), kindWord(u.GetKind()), state, role)
		}
		return nil
	}
}

func kindWord(k identityv1.ActorKind) string {
	switch k {
	case identityv1.ActorKind_ACTOR_KIND_HUMAN:
		return "human"
	case identityv1.ActorKind_ACTOR_KIND_AGENT:
		return "agent"
	}
	return "unknown"
}

func cmdAdminUserRemove(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office admin user remove <user>")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		if err := c.RemoveUser(e.ctx, args[0]); err != nil {
			return err
		}
		e.done(map[string]any{"userId": args[0], "removed": true}, "removed user %s\n", args[0])
		return nil
	}
}

// done prints the result of a command that changes something: a JSON object, or one line.
func (e *env) done(v map[string]any, format string, args ...any) {
	if e.json {
		e.printJSON(v)
		return
	}
	fmt.Fprintf(e.stdout, format, args...)
}

func cmdAdminSystemAdmin(on bool) func(e *env, fs *flag.FlagSet) func([]string) error {
	verb := "remove"
	if on {
		verb = "add"
	}
	return func(e *env, fs *flag.FlagSet) func([]string) error {
		return func(args []string) error {
			if len(args) != 1 {
				return usagef("usage: office admin system-admin %s <user>", verb)
			}
			c, err := e.client()
			if err != nil {
				return err
			}
			if err := c.SetSystemAdmin(e.ctx, args[0], on); err != nil {
				return err
			}
			e.done(map[string]any{"userId": args[0], "systemAdmin": on}, "%s is a system admin: %t\n", args[0], on)
			return nil
		}
	}
}

func cmdAdminTenantCreate(e *env, fs *flag.FlagSet) func([]string) error {
	name := fs.String("name", "", "display name of the tenant (default: the tenant ID)")
	return func(args []string) error {
		if len(args) != 2 {
			return usagef("usage: office admin tenant create <tenant> <first-tenant-admin> [--name N]")
		}
		display := *name
		if display == "" {
			display = args[0]
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		if err := c.CreateTenant(e.ctx, args[0], display, args[1]); err != nil {
			return err
		}
		e.done(map[string]any{"tenantId": args[0], "firstTenantAdmin": args[1]}, "created tenant %s with %s as its tenant admin\n", args[0], args[1])
		return nil
	}
}

func cmdAdminTenantList(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 0 {
			return usagef("usage: office admin tenant list")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		ts, err := c.ListTenants(e.ctx)
		if err != nil {
			return err
		}
		if e.json {
			out := make([]any, 0, len(ts))
			for _, t := range ts {
				out = append(out, coreapi.MessageJSON(t))
			}
			e.printJSON(map[string]any{"tenants": out})
			return nil
		}
		for _, t := range ts {
			state := "active"
			if t.GetValidUntil() != nil {
				state = "removed"
			}
			fmt.Fprintf(e.stdout, "%s\t%s\t%s\n", t.GetTenantId(), t.GetDisplayName(), state)
		}
		return nil
	}
}

func cmdAdminTenantRemove(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office admin tenant remove <tenant>")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		if err := c.RemoveTenant(e.ctx, args[0]); err != nil {
			return err
		}
		e.done(map[string]any{"tenantId": args[0], "removed": true}, "removed tenant %s\n", args[0])
		return nil
	}
}

func cmdAdminMemberSet(e *env, fs *flag.FlagSet) func([]string) error {
	role := fs.String("role", "member", "member or tenant-admin")
	return func(args []string) error {
		if len(args) != 2 {
			return usagef("usage: office admin member set <tenant> <user> [--role member|tenant-admin]")
		}
		var r userv1.Role
		switch *role {
		case "member":
			r = userv1.Role_ROLE_MEMBER
		case "tenant-admin":
			r = userv1.Role_ROLE_TENANT_ADMIN
		default:
			return usagef("--role must be member or tenant-admin, not %q", *role)
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		if err := c.SetMembership(e.ctx, args[0], args[1], r); err != nil {
			return err
		}
		e.done(map[string]any{"tenantId": args[0], "userId": args[1], "role": *role}, "%s is %s of %s\n", args[1], *role, args[0])
		return nil
	}
}

func cmdAdminMemberRemove(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 2 {
			return usagef("usage: office admin member remove <tenant> <user>")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		if err := c.RemoveMembership(e.ctx, args[0], args[1]); err != nil {
			return err
		}
		e.done(map[string]any{"tenantId": args[0], "userId": args[1], "removed": true}, "%s is no member of %s any more\n", args[1], args[0])
		return nil
	}
}

func cmdAdminMemberList(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office admin member list <tenant>")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		ms, err := c.ListMemberships(e.ctx, args[0])
		if err != nil {
			return err
		}
		if e.json {
			out := make([]any, 0, len(ms))
			for _, m := range ms {
				out = append(out, coreapi.MessageJSON(m))
			}
			e.printJSON(map[string]any{"memberships": out})
			return nil
		}
		for _, m := range ms {
			state := "active"
			if m.GetValidUntil() != nil {
				state = "ended"
			}
			role := "member"
			if m.GetRole() == userv1.Role_ROLE_TENANT_ADMIN {
				role = "tenant-admin"
			}
			fmt.Fprintf(e.stdout, "%s\t%s\t%s\n", m.GetUserId(), role, state)
		}
		return nil
	}
}

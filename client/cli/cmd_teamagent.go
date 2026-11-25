package cli

// The commands for team agents (dec-team-agents): a tenant admin creates and removes them, every
// member lists them and starts one. The definition of a team agent is a file of the tenant that
// members change with Git; this file holds the confirmation of the agent launcher.

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

func cmdTeamAgentCreate(e *env, fs *flag.FlagSet) func([]string) error {
	name := fs.String("name", "", "display name of the team agent (default: the agent ID)")
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office team-agent create <agent> [--name N]")
		}
		display := *name
		if display == "" {
			display = args[0]
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		if err := c.CreateTeamAgent(e.ctx, args[0], display); err != nil {
			return err
		}
		e.done(map[string]any{"agentId": args[0], "tenantId": e.cfg.Tenant},
			"created the team agent %s in the tenant %s; write its definition as %s/%s.yaml in the clone of the tenant\n",
			args[0], e.cfg.Tenant, definitionDir, args[0])
		return nil
	}
}

func cmdTeamAgentList(e *env, _ *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 0 {
			return usagef("usage: office team-agent list")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		as, err := c.ListTeamAgents(e.ctx)
		if err != nil {
			return err
		}
		if e.json {
			type row struct {
				AgentID     string `json:"agentId"`
				DisplayName string `json:"displayName"`
				Removed     bool   `json:"removed"`
			}
			rows := []row{}
			for _, a := range as {
				rows = append(rows, row{a.GetAgentId(), a.GetDisplayName(), a.GetValidUntil() != nil})
			}
			e.printJSON(rows)
			return nil
		}
		for _, a := range as {
			state := ""
			if a.GetValidUntil() != nil {
				state = " (removed)"
			}
			fmt.Fprintf(e.stdout, "%s\t%s%s\n", a.GetAgentId(), a.GetDisplayName(), state)
		}
		return nil
	}
}

func cmdTeamAgentRemove(e *env, _ *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office team-agent remove <agent>")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		if err := c.RemoveTeamAgent(e.ctx, args[0]); err != nil {
			return err
		}
		e.done(map[string]any{"agentId": args[0]}, "removed the team agent %s; its tokens stop working at once\n", args[0])
		return nil
	}
}

// definitionDir is the directory of the definitions in the repository of a tenant.
const definitionDir = "agents"

// confirmedFile is the file in which the agent launcher keeps the definitions that they confirmed.
// It lies in the configuration folder of the client, so a new definition is confirmed once for every
// start, on every hub and tenant.
func confirmedFile() string {
	return filepath.Join(coreapi.ClientConfigDir(), "confirmed-agents.json")
}

// confirmation is a definition that the agent launcher confirmed.
type confirmation struct {
	Version string `json:"version"`
	Text    string `json:"text"`
}

func confirmationKey(cfg coreapi.Config, agent string) string {
	return strings.TrimRight(cfg.Hub, "/") + " " + cfg.Tenant + " " + agent
}

func readConfirmations() map[string]confirmation {
	out := map[string]confirmation{}
	if b, err := os.ReadFile(confirmedFile()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// confirmDefinition makes sure that the agent launcher has confirmed this version of the
// definition. With confirm set, the launcher names the version that they confirm. Without it, a
// definition that was confirmed already starts at once, and any other is shown with its changes,
// the added tools first, and confirmed with a line on standard input (dec-team-agents). The
// confirmation protects the agent launcher; the hub does not check it.
func confirmDefinition(e *env, def *coreapi.AgentDefinition, confirm string) error {
	all := readConfirmations()
	key := confirmationKey(e.cfg, def.Agent)
	old, had := all[key]
	// A version that the agent launcher names has to be the one that is there, also when it was
	// confirmed before: a server starts only with the version that its operator named.
	if confirm != "" && confirm != def.Version {
		return usagef("--confirm names the version %s, but the definition of %s is at version %s", confirm, def.Agent, def.Version)
	}
	if had && old.Version == def.Version {
		return nil
	}
	if confirm == "" {
		fmt.Fprintf(e.stderr, "The definition of the team agent %s is at version %s.\n", def.Agent, def.Version)
		if had {
			if added := addedTools(old.Text, def.Allow); len(added) > 0 {
				fmt.Fprintf(e.stderr, "It adds tools since the version that you confirmed (%s): %s\n", old.Version, strings.Join(added, ", "))
			} else {
				fmt.Fprintf(e.stderr, "It changed since the version that you confirmed (%s).\n", old.Version)
			}
		}
		fmt.Fprint(e.stderr, def.Summary())
		fmt.Fprintf(e.stderr, "The agent runs on this computer, with the harness that you chose, and writes as the agent in this tenant.\nRun it? [y/N] ")
		line, err := bufio.NewReader(e.stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("the definition of %s is not confirmed: answer y, or pass --confirm %s", def.Agent, def.Version)
		}
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			return errors.New("the definition of " + def.Agent + " is not confirmed; the agent did not start")
		}
	}
	all[key] = confirmation{Version: def.Version, Text: def.Text}
	b, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(confirmedFile()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(confirmedFile(), b, 0o600)
}

// addedTools returns the tools of now that the old definition did not allow.
func addedTools(oldText string, now []string) []string {
	var old struct {
		Allow []string `yaml:"allow"`
	}
	_ = yaml.Unmarshal([]byte(oldText), &old)
	had := map[string]bool{}
	for _, t := range old.Allow {
		had[t] = true
	}
	var added []string
	for _, t := range now {
		if !had[t] {
			added = append(added, t)
		}
	}
	return added
}

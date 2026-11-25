package contract

// Contract tests of the CLI for team agents (dec-team-agents): "office team-agent" and "office
// agent run --team-agent" with the ACP test double as the harness. The hub is real. The tests check
// what the CLI does and shows, never how long it takes.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// actorOf returns the actor for the key of a CLI user, so that a test can push with Git.
func (u *officeUser) actorOf(t *testing.T) *contracttest.Actor {
	t.Helper()
	key, err := auth.ReadKey(u.key)
	if err != nil {
		t.Fatal(err)
	}
	return u.m.srv.WithKey(t, key)
}

// definitionVersion returns the version of the definition of agent in the archive.
func definitionVersion(t *testing.T, a *contracttest.Actor, agent string) string {
	t.Helper()
	res, err := a.Archive.ReadFile(ctx10(t), &archivev1.ReadFileRequest{Path: "agents/" + agent + ".yaml"})
	if err != nil {
		t.Fatal(err)
	}
	return res.GetFile().GetVersion()
}

// A tenant admin creates and removes team agents with the CLI, and every member lists them.
func TestTeamAgentCLI(t *testing.T) {
	m := newMachine(t)
	ada, bob := m.user("ada", "Ada", "human"), m.user("bob", "Bob", "human")

	bob.fails(1, "permission_denied", "team-agent", "create", "minutes")
	created := ada.ok("team-agent", "create", "minutes", "--name", "Minutes keeper")
	if created["agentId"] != "minutes" || created["tenantId"] != contracttest.DefaultTenant {
		t.Fatalf("create: %v", created)
	}
	out, errOut, code := bob.run("team-agent", "list")
	if code != 0 || !strings.Contains(out, "minutes\tMinutes keeper") {
		t.Fatalf("list: %d %q %q", code, out, errOut)
	}
	bob.fails(1, "permission_denied", "team-agent", "remove", "minutes")
	ada.ok("team-agent", "remove", "minutes")
	out, _, _ = bob.run("team-agent", "list")
	if !strings.Contains(out, "(removed)") {
		t.Fatalf("list after the removal: %q", out)
	}
	ada.fails(1, "already_exists", "team-agent", "create", "bob")
}

// A member starts a team agent. The agent runs with the role of its definition, writes as the team
// agent, and its harness gets a token and never the key of the agent launcher. Another member who
// starts the same agent in the same session is told "already joined".
func TestTeamAgentRun(t *testing.T) {
	m := newMachine(t)
	ada, bob := m.user("ada", "Ada", "human"), m.user("bob", "Bob", "human")
	ada.ok("team-agent", "create", "minutes", "--name", "Minutes keeper")
	adaActor := ada.actorOf(t)
	p := newPusher(t, m.srv, adaActor, "Ada", contracttest.DefaultTenant)
	p.commitFile("agents/minutes.yaml", "schema: office.agent.v1\nrole: |\n  You keep the minutes. please say \"Minutes keeper here.\"\nallow: [fetch]\nwake:\n  strategy: turn-end\n")
	p.mustPush()
	version := definitionVersion(t, adaActor, "minutes")

	sid := newSession()
	adaLog := ada.join(sid).LogPath

	// The definition is not confirmed yet, and nobody answers the question.
	_, errOut, code := bob.run("agent", "run", "--session", sid, "--team-agent", "minutes", "--", contracttest.ACPDouble(t))
	if code == 0 || !strings.Contains(errOut, version) || !strings.Contains(errOut, "You keep the minutes.") || !strings.Contains(errOut, "fetch") {
		t.Fatalf("a definition that is not confirmed: exit %d\n%s", code, errOut)
	}
	_, errOut, code = bob.run("agent", "run", "--session", sid, "--team-agent", "minutes", "--confirm", "not-this-version", "--", contracttest.ACPDouble(t))
	if code == 0 || !strings.Contains(errOut, "not-this-version") {
		t.Fatalf("a wrong version: exit %d\n%s", code, errOut)
	}
	_, errOut, code = bob.run("agent", "run", "--session", sid, "--team-agent", "minutes", "--role", "x", "--", contracttest.ACPDouble(t))
	if code == 0 || !strings.Contains(errOut, "role") {
		t.Fatalf("a role with a team agent: exit %d\n%s", code, errOut)
	}

	run, rec := bob.agentRun(t, sid, "--team-agent", "minutes", "--confirm", version)
	lines := waitUtterance(t, adaLog, "minutes", "Minutes keeper here.")
	var join *json.RawMessage
	for _, l := range lines {
		if j := l.ev.GetParticipantJoined(); j != nil && j.GetParticipantId() == "minutes" {
			if j.GetAgentLauncher() != "bob" || j.GetDefinitionVersion() != version {
				t.Errorf("the join of the team agent: %v", j)
			}
			join = new(json.RawMessage)
		}
	}
	if join == nil {
		t.Fatal("the team agent did not join the session")
	}

	// The harness gets the MCP server with a token file and no key.
	_, recs := contracttest.WaitACPRecord(t, rec, 10*time.Second, func(r contracttest.ACPRecord) bool { return r.Kind() == "session/new" })
	env := map[string]string{}
	for _, r := range recs {
		if r.Kind() != "session/new" {
			continue
		}
		for _, e := range r["params"].(map[string]any)["mcpServers"].([]any)[0].(map[string]any)["env"].([]any) {
			kv := e.(map[string]any)
			env[kv["name"].(string)] = kv["value"].(string)
		}
	}
	if env["OFFICE_KEY"] != "" || env["OFFICE_TOKEN_FILE"] == "" {
		t.Fatalf("the harness must get a token file and no key: %v", env)
	}
	if b, err := os.ReadFile(env["OFFICE_TOKEN_FILE"]); err != nil || !strings.Contains(string(b), `"participant_id":"minutes"`) || strings.Contains(string(b), bob.key) {
		t.Errorf("the token file: %s, %v", b, err)
	}

	// Ada starts the same agent in the same session: refused.
	// Ada works on another computer: her own runtime directory, so that she shares no join state.
	second, _ := ada.agentRunIn(t, t.TempDir(), []string{"OFFICE_RUNTIME_DIR=" + t.TempDir()},
		"--session", sid, "--team-agent", "minutes", "--confirm", version, "--", contracttest.ACPDouble(t))
	select {
	case <-second.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("the second runner did not stop")
	}
	if !strings.Contains(second.Stderr(), "already joined") {
		t.Errorf("the second runner: %s", second.Stderr())
	}
	// The first one goes on.
	select {
	case <-run.Done():
		t.Fatalf("the first runner stopped: %s", run.Stderr())
	default:
	}
}

// A definition that changed since it was confirmed is shown again, with the added tools first.
func TestTeamAgentDefinitionChanged(t *testing.T) {
	m := newMachine(t)
	ada := m.user("ada", "Ada", "human")
	ada.ok("team-agent", "create", "minutes")
	adaActor := ada.actorOf(t)
	p := newPusher(t, m.srv, adaActor, "Ada", contracttest.DefaultTenant)
	p.commitFile("agents/minutes.yaml", "schema: office.agent.v1\nrole: You keep the minutes.\n")
	p.mustPush()
	v1 := definitionVersion(t, adaActor, "minutes")
	sid := newSession()
	ada.join(sid)

	// Confirm the first version and let the runner start once.
	first, _ := ada.agentRun(t, sid, "--team-agent", "minutes", "--confirm", v1)
	waitUntil(t, 20*time.Second, "the first run has started", func() bool { return strings.Contains(first.Stderr(), "joined") })
	first.Kill()

	p.commitFile("agents/minutes.yaml", "schema: office.agent.v1\nrole: You keep the minutes.\nallow: [fetch, shell]\n")
	p.mustPush()
	v2 := definitionVersion(t, adaActor, "minutes")
	_, errOut, code := ada.run("agent", "run", "--session", sid, "--team-agent", "minutes", "--", contracttest.ACPDouble(t))
	if code == 0 || !strings.Contains(errOut, v2) || !strings.Contains(errOut, "adds tools") || !strings.Contains(errOut, "fetch, shell") {
		t.Fatalf("a changed definition: exit %d\n%s", code, errOut)
	}
}

package contract

// Contract tests of team agents (dec-team-agents) through if-tenant-api, if-auth-api, the session
// commands and the push to the repository of the tenant: who creates and lists them, the token as
// the agent with its agent launcher and its version, "already joined", the end of a token, and the
// definition as a file that no agent may change. The tests check behaviour only.

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	tenantv1 "github.com/mrsimpson/agentic-office/hub/tenant/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const goodDefinition = "schema: office.agent.v1\nrole: You keep the minutes.\nallow: [fetch]\nwake:\n  strategy: turn-end\n"

func createTeamAgent(t *testing.T, a *contracttest.Actor, tenant, id, name string) error {
	t.Helper()
	_, err := a.Tenant.CreateTeamAgent(ctx10(t), &tenantv1.CreateTeamAgentRequest{CommandId: coreapi.NewCommandID(),
		TenantId: tenant, AgentId: id, DisplayName: name})
	return err
}

func issueAgentToken(t *testing.T, a *contracttest.Actor, tenant, id, version string) (*identityv1.IssueAgentTokenResponse, error) {
	t.Helper()
	return a.Identity.IssueAgentToken(ctx10(t), &identityv1.IssueAgentTokenRequest{TenantId: tenant, AgentId: id, DefinitionVersion: version})
}

// teamAgentOf returns clients that work as the team agent, started by a.
func teamAgentOf(t *testing.T, srv *contracttest.Hub, a *contracttest.Actor, id, version string) (contracttest.Clients, *identityv1.IssueAgentTokenResponse) {
	t.Helper()
	res, err := issueAgentToken(t, a, contracttest.DefaultTenant, id, version)
	if err != nil {
		t.Fatal(err)
	}
	return srv.WithToken(t, res.GetAccessToken()), res
}

// A tenant admin creates and removes team agents, every member lists them, and the ID is a
// participant ID of the whole hub.
func TestTeamAgentsCreateListRemove(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human) // tenant admin of acme
	bob := srv.Participant(t, "bob", "Bob", human) // member
	eve := srv.User(t, "eve", "Eve", human)        // no member

	wantCode(t, "create as a member", createTeamAgent(t, bob, "acme", "minutes", "Minutes"), connect.CodePermissionDenied)
	wantCode(t, "create as a user outside the tenant", createTeamAgent(t, eve, "acme", "minutes", "Minutes"), connect.CodeNotFound)
	wantCode(t, "create in a tenant that does not exist", createTeamAgent(t, ada, "nowhere", "minutes", "Minutes"), connect.CodeNotFound)
	wantCode(t, "bad agent ID", createTeamAgent(t, ada, "acme", "a b", "Minutes"), connect.CodeInvalidArgument)
	wantCode(t, "no display name", createTeamAgent(t, ada, "acme", "minutes", ""), connect.CodeInvalidArgument)

	req := &tenantv1.CreateTeamAgentRequest{CommandId: coreapi.NewCommandID(), TenantId: "acme", AgentId: "minutes", DisplayName: "Minutes"}
	if _, err := ada.Tenant.CreateTeamAgent(ctx10(t), req); err != nil {
		t.Fatal(err)
	}
	if _, err := ada.Tenant.CreateTeamAgent(ctx10(t), req); err != nil {
		t.Fatalf("repeated create with the same command_id: %v", err)
	}
	wantCode(t, "an ID that is taken by a team agent", createTeamAgent(t, ada, "acme", "minutes", "Other"), connect.CodeAlreadyExists)
	wantCode(t, "an ID that is taken by a user", createTeamAgent(t, ada, "acme", "bob", "Bob"), connect.CodeAlreadyExists)

	list := func(a *contracttest.Actor) ([]*tenantv1.TeamAgent, error) {
		res, err := a.Tenant.ListTeamAgents(ctx10(t), &tenantv1.ListTeamAgentsRequest{TenantId: "acme"})
		return res.GetAgents(), err
	}
	as, err := list(bob)
	if err != nil || len(as) != 1 || as[0].GetAgentId() != "minutes" || as[0].GetDisplayName() != "Minutes" || as[0].GetValidUntil() != nil {
		t.Fatalf("team agents as a member: %v, %v", as, err)
	}
	_, err = list(eve)
	wantCode(t, "list as a user outside the tenant", err, connect.CodeNotFound)

	_, err = bob.Tenant.RemoveTeamAgent(ctx10(t), &tenantv1.RemoveTeamAgentRequest{CommandId: coreapi.NewCommandID(), TenantId: "acme", AgentId: "minutes"})
	wantCode(t, "remove as a member", err, connect.CodePermissionDenied)
	if _, err := ada.Tenant.RemoveTeamAgent(ctx10(t), &tenantv1.RemoveTeamAgentRequest{CommandId: coreapi.NewCommandID(), TenantId: "acme", AgentId: "minutes"}); err != nil {
		t.Fatal(err)
	}
	as, err = list(ada)
	if err != nil || len(as) != 1 || as[0].GetValidUntil() == nil {
		t.Fatalf("team agents after the removal: %v, %v", as, err)
	}
	wantCode(t, "an ID that was removed is never used again", createTeamAgent(t, ada, "acme", "minutes", "Minutes"), connect.CodeAlreadyExists)
}

// Every member may start a team agent. The token names the agent as the participant, and its
// display name names the agent launcher and the version. Others cannot get one.
func TestTeamAgentToken(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bob := srv.Participant(t, "bob", "Bob", human)
	eve := srv.User(t, "eve", "Eve", human)
	bot := srv.Participant(t, "bot", "Bot", identityv1.ActorKind_ACTOR_KIND_AGENT)
	if err := createTeamAgent(t, ada, "acme", "minutes", "Minutes"); err != nil {
		t.Fatal(err)
	}

	_, err := issueAgentToken(t, eve, "acme", "minutes", "v1")
	wantCode(t, "a user outside the tenant", err, connect.CodeNotFound)
	_, err = issueAgentToken(t, bob, "acme", "nobody", "v1")
	wantCode(t, "an agent that does not exist", err, connect.CodeNotFound)
	_, err = issueAgentToken(t, bob, "acme", "ada", "v1")
	wantCode(t, "a user who is no team agent", err, connect.CodeNotFound)
	_, err = issueAgentToken(t, bot, "acme", "minutes", "v1")
	wantCode(t, "a personal agent as the launcher", err, connect.CodePermissionDenied)
	_, err = issueAgentToken(t, bob, "", "minutes", "v1")
	wantCode(t, "no tenant", err, connect.CodeInvalidArgument)
	_, err = issueAgentToken(t, bob, "acme", "minutes", "bad\nversion")
	wantCode(t, "a version with a line break", err, connect.CodeInvalidArgument)

	agent, res := teamAgentOf(t, srv, bob, "minutes", "0123456789abcdef0123")
	if res.GetParticipantId() != "minutes" || res.GetAccessToken() == "" || res.GetExpiresAt() == nil ||
		!strings.HasPrefix(res.GetDisplayName(), "Minutes") || !strings.Contains(res.GetDisplayName(), "Bob") ||
		!strings.Contains(res.GetDisplayName(), "0123456789ab") || strings.Contains(res.GetDisplayName(), "0123456789abc") {
		t.Fatalf("token as the team agent: %v", res)
	}
	who, err := agent.Identity.WhoAmI(ctx10(t), &identityv1.WhoAmIRequest{})
	if err != nil || who.GetParticipantId() != "minutes" || who.GetKind() != identityv1.ActorKind_ACTOR_KIND_AGENT ||
		who.GetSystemAdmin() || len(who.GetMemberships()) != 1 || who.GetMemberships()[0].GetTenantId() != "acme" || who.GetMemberships()[0].GetTenantAdmin() {
		t.Fatalf("whoami as the team agent: %v, %v", who, err)
	}

	// An agent starts no other team agent, manages no team agent and opens no portal.
	_, err = agent.Identity.IssueAgentToken(ctx10(t), &identityv1.IssueAgentTokenRequest{TenantId: "acme", AgentId: "minutes"})
	wantCode(t, "a team agent starts a team agent", err, connect.CodePermissionDenied)
	_, err = agent.Tenant.CreateTeamAgent(ctx10(t), &tenantv1.CreateTeamAgentRequest{CommandId: coreapi.NewCommandID(), TenantId: "acme", AgentId: "other", DisplayName: "Other"})
	wantCode(t, "a team agent creates a team agent", err, connect.CodeNotFound)
	_, err = agent.Identity.CreatePortalLink(ctx10(t), &identityv1.CreatePortalLinkRequest{CommandId: coreapi.NewCommandID()})
	wantCode(t, "a team agent opens a portal link", err, connect.CodePermissionDenied)
}

// A team agent is in a session once. The same agent launcher gets the first join again, another one
// is told "already joined". The join names the agent launcher and the version.
func TestTeamAgentJoinsOnce(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bob := srv.Participant(t, "bob", "Bob", human)
	if err := createTeamAgent(t, ada, "acme", "minutes", "Minutes"); err != nil {
		t.Fatal(err)
	}
	join := func(c contracttest.Clients, sid string) (*sessioncommandsv1.JoinResponse, error) {
		return c.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: sid, CommandId: coreapi.NewCommandID()})
	}

	byBob, _ := teamAgentOf(t, srv, bob, "minutes", "v-bob")
	first, err := join(byBob, "weekly")
	if err != nil {
		t.Fatal(err)
	}
	pj := first.GetResult().GetEvent().GetParticipantJoined()
	if pj.GetParticipantId() != "minutes" || pj.GetAgentLauncher() != "bob" || pj.GetDefinitionVersion() != "v-bob" {
		t.Fatalf("join of the team agent: %v", first)
	}

	byAda, _ := teamAgentOf(t, srv, ada, "minutes", "v-ada")
	_, err = join(byAda, "weekly")
	wantCode(t, "another agent launcher", err, connect.CodeFailedPrecondition)
	if err == nil || !strings.Contains(err.Error(), "already joined") {
		t.Fatalf("the answer does not say \"already joined\": %v", err)
	}

	again, _ := teamAgentOf(t, srv, bob, "minutes", "v-bob")
	res, err := join(again, "weekly")
	if err != nil || !res.GetResult().GetDuplicate() || res.GetResult().GetEvent().GetId() != first.GetResult().GetEvent().GetId() {
		t.Fatalf("the same agent launcher joins again: %v, %v", res, err)
	}
	// Another session is free.
	if _, err := join(byAda, "other"); err != nil {
		t.Fatalf("the same agent in another session: %v", err)
	}
	// After it left, another agent launcher may start it.
	if _, err := byBob.Commands.Leave(ctx10(t), &sessioncommandsv1.LeaveRequest{SessionId: "weekly", CommandId: coreapi.NewCommandID(), ParticipantId: "minutes"}); err != nil {
		t.Fatal(err)
	}
	if _, err := join(byAda, "weekly"); err != nil {
		t.Fatalf("join after the agent left: %v", err)
	}
}

// A token as a team agent ends when the team agent is removed, when its agent launcher leaves the
// tenant, and when the key of the agent launcher is revoked.
func TestTeamAgentTokenEnds(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	ada := srv.Participant(t, "ada", "Ada", human)
	bob := srv.Participant(t, "bob", "Bob", human)
	if err := createTeamAgent(t, ada, "acme", "minutes", "Minutes"); err != nil {
		t.Fatal(err)
	}
	alive := func(c contracttest.Clients) error {
		_, err := c.Identity.WhoAmI(ctx10(t), &identityv1.WhoAmIRequest{})
		return err
	}

	c1, _ := teamAgentOf(t, srv, ada, "minutes", "v1")
	if err := alive(c1); err != nil {
		t.Fatal(err)
	}
	if _, err := ada.Tenant.RemoveTeamAgent(ctx10(t), &tenantv1.RemoveTeamAgentRequest{CommandId: coreapi.NewCommandID(), TenantId: "acme", AgentId: "minutes"}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "after the team agent was removed", alive(c1), connect.CodeUnauthenticated)
	_, err := issueAgentToken(t, ada, "acme", "minutes", "v1")
	wantCode(t, "a new token for a removed team agent", err, connect.CodeNotFound)

	if err := createTeamAgent(t, ada, "acme", "tasks", "Tasks"); err != nil {
		t.Fatal(err)
	}
	cb, _ := teamAgentOf(t, srv, bob, "tasks", "v1")
	if err := alive(cb); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.User.RemoveMembership(ctx10(t), &userv1.RemoveMembershipRequest{CommandId: coreapi.NewCommandID(), TenantId: "acme", UserId: "bob"}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "after the agent launcher left the tenant", alive(cb), connect.CodeUnauthenticated)
}

// The definition is a file at agents/<agent ID>.yaml. A member pushes it with Git and the hub checks
// the schema; an agent, a team agent too, changes no definition and removes none.
func TestTeamAgentDefinitionFile(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bob := srv.Participant(t, "bob", "Bob", human)
	if err := createTeamAgent(t, ada, "acme", "minutes", "Minutes"); err != nil {
		t.Fatal(err)
	}
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)

	// The schema decides.
	for _, content := range map[string]string{
		"a field of the schema that does not exist": "schema: office.agent.v1\nrole: x\nharness: [rm]\n",
		"no role":                        "schema: office.agent.v1\nallow: [fetch]\n",
		"a blank role":                   "schema: office.agent.v1\nrole: \"  \"\n",
		"a strategy that does not exist": "schema: office.agent.v1\nrole: x\nwake: {strategy: sometimes}\n",
		"a command in the wake strategy": "schema: office.agent.v1\nrole: x\nwake: {strategy: llm, command: curl}\n",
		"a threshold above 1":            "schema: office.agent.v1\nrole: x\nwake: {strategy: llm, threshold: 2}\n",
		"a tool that is given twice":     "schema: office.agent.v1\nrole: x\nallow: [fetch, fetch]\n",
		"a role that is no text":         "schema: office.agent.v1\nrole: [x]\n",
	} {
		before := mainOf(t, srv, "acme")
		p.commitFile("agents/minutes.yaml", content)
		out, code := p.push()
		refused(t, srv, "acme", before, out, code, "agents/minutes.yaml")
		p.mustGit("reset", "-q", "--hard", "origin/main")
	}
	before := mainOf(t, srv, "acme")
	p.commitFile("agents/a/b.yaml", goodDefinition)
	out, code := p.push()
	refused(t, srv, "acme", before, out, code, "agents/<agent ID>.yaml")
	p.mustGit("reset", "-q", "--hard", "origin/main")

	p.commitFile("agents/minutes.yaml", goodDefinition)
	p.mustPush()

	// A member changes it, and a member reads it through the archive.
	pb := newPusher(t, srv, bob, "Bob", contracttest.DefaultTenant)
	pb.commitFile("agents/minutes.yaml", strings.Replace(goodDefinition, "[fetch]", "[fetch, search]", 1))
	pb.mustPush()
	file, err := bob.Archive.ReadFile(ctx10(t), &archivev1.ReadFileRequest{Path: "agents/minutes.yaml"})
	if err != nil || file.GetFile().GetVersion() == "" || !strings.Contains(string(file.GetFile().GetContent()), "search") {
		t.Fatalf("read the definition: %v, %v", file, err)
	}

	// A team agent changes nothing of it, but writes other files.
	_, res := teamAgentOf(t, srv, bob, "minutes", file.GetFile().GetVersion())
	pa := &pusher{t: t, srv: srv, helper: gitHelper(t), token: res.GetAccessToken(), name: res.GetDisplayName(), email: "minutes@office.invalid"}
	work := t.TempDir()
	pa.dir = work + "/clone"
	if out, code := gitAs(t, srv, pa.helper, pa.token, work, "clone", "-q", gitURL(srv, "acme"), pa.dir); code != 0 {
		t.Fatalf("clone as the team agent: %d\n%s", code, out)
	}
	for name, mutate := range map[string]func(){
		"a change":  func() { pa.write("agents/minutes.yaml", strings.Replace(goodDefinition, "minutes", "bosses", 1)) },
		"a removal": func() { pa.mustGit("rm", "-q", "agents/minutes.yaml") },
		"a copy":    func() { pa.write("agents/other.yaml", goodDefinition) },
	} {
		before := mainOf(t, srv, "acme")
		mutate()
		pa.commit(name)
		out, code := pa.push()
		refused(t, srv, "acme", before, out, code, "no agent may change this file")
		pa.mustGit("reset", "-q", "--hard", "origin/main")
	}
	pa.commitFile("documents/notes-of-the-agent.md", "# Notes\n")
	pa.mustPush()
	if got := contracttest.Git(t, srv.TenantRepoDir("acme"), "log", "-1", "--format=%an"); !strings.Contains(got, "Minutes (agent launcher Bob") {
		t.Errorf("the author of the commit of the team agent: %q", got)
	}
}

// A tenant admin registers a key for a team agent, so that it runs without a human, for example on
// a server. Only a tenant admin of its tenant, or a system admin, adds or revokes such a key. The
// agent with its own key works in its tenant like a member, and no more.
func TestTeamAgentWithKey(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	ada := srv.Participant(t, "ada", "Ada", human) // tenant admin of acme
	bob := srv.Participant(t, "bob", "Bob", human) // member
	if _, err := admin.Tenant.CreateTenant(ctx10(t), &tenantv1.CreateTenantRequest{CommandId: coreapi.NewCommandID(), TenantId: "other",
		DisplayName: "Other", FirstAdminUserId: "zed"}); err == nil {
		t.Fatal("create a tenant for a user that does not exist")
	}
	zed := srv.User(t, "zed", "Zed", human)
	if _, err := admin.Tenant.CreateTenant(ctx10(t), &tenantv1.CreateTenantRequest{CommandId: coreapi.NewCommandID(), TenantId: "other",
		DisplayName: "Other", FirstAdminUserId: "zed"}); err != nil {
		t.Fatal(err)
	}
	eve := srv.User(t, "eve", "Eve", human)
	if err := createTeamAgent(t, ada, "acme", "minutes", "Minutes"); err != nil {
		t.Fatal(err)
	}
	key, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	addKey := func(a *contracttest.Actor, user string, key ed25519.PrivateKey) error {
		_, err := a.User.AddKey(ctx10(t), &userv1.AddKeyRequest{CommandId: coreapi.NewCommandID(), UserId: user, PublicKey: auth.PublicKey(key)})
		return err
	}
	wantCode(t, "a member adds a key to a team agent", addKey(bob, "minutes", key), connect.CodePermissionDenied)
	wantCode(t, "a user outside the tenant", addKey(eve, "minutes", key), connect.CodePermissionDenied)
	wantCode(t, "the tenant admin of another tenant", addKey(zed, "minutes", key), connect.CodePermissionDenied)
	wantCode(t, "a tenant admin adds a key to a human", addKey(ada, "bob", key), connect.CodePermissionDenied)
	if err := addKey(ada, "minutes", key); err != nil {
		t.Fatalf("a tenant admin adds a key to a team agent of the tenant: %v", err)
	}

	agent := srv.WithKey(t, key)
	who, err := agent.Identity.WhoAmI(ctx10(t), &identityv1.WhoAmIRequest{})
	if err != nil || who.GetParticipantId() != "minutes" || who.GetKind() != identityv1.ActorKind_ACTOR_KIND_AGENT || who.GetSystemAdmin() ||
		len(who.GetMemberships()) != 1 || who.GetMemberships()[0].GetTenantId() != "acme" || who.GetMemberships()[0].GetTenantAdmin() {
		t.Fatalf("whoami of the team agent with a key: %v, %v", who, err)
	}
	// It does what an agent does in its tenant, and nothing that manages agents or keys.
	_, err = agent.Identity.IssueAgentToken(ctx10(t), &identityv1.IssueAgentTokenRequest{TenantId: "acme", AgentId: "minutes"})
	wantCode(t, "a team agent with a key starts a team agent", err, connect.CodePermissionDenied)
	wantCode(t, "a team agent with a key creates a team agent", createTeamAgent(t, agent, "acme", "other-agent", "Other"), connect.CodeNotFound)
	wantCode(t, "a team agent adds a key to itself", addKey(agent, "minutes", mustNewKey(t)), connect.CodePermissionDenied)

	// Its join names no agent launcher, and another agent launcher is told "already joined".
	join, err := agent.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: "weekly", CommandId: coreapi.NewCommandID()})
	if err != nil || join.GetResult().GetEvent().GetParticipantJoined().GetAgentLauncher() != "" {
		t.Fatalf("join of the team agent with a key: %v, %v", join, err)
	}
	byBob, _ := teamAgentOf(t, srv, bob, "minutes", "v1")
	_, err = byBob.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: "weekly", CommandId: coreapi.NewCommandID()})
	wantCode(t, "an agent launcher joins the agent that runs with its own key", err, connect.CodeFailedPrecondition)
	if err == nil || !strings.Contains(err.Error(), "already joined") {
		t.Fatalf("the answer does not say \"already joined\": %v", err)
	}

	// No agent changes a definition, also not with its own key.
	pa := newPusher(t, srv, agent, "Minutes", contracttest.DefaultTenant)
	pb := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
	pb.commitFile("agents/minutes.yaml", goodDefinition)
	pb.mustPush()
	pa.mustGit("pull", "-q", "--ff-only", "origin", "main")
	before := mainOf(t, srv, "acme")
	pa.commitFile("agents/minutes.yaml", strings.Replace(goodDefinition, "minutes", "bosses", 1))
	out, code := pa.push()
	refused(t, srv, "acme", before, out, code, "no agent may change this file")

	// Revoking the key ends the agent at once.
	if _, err := ada.User.RevokeKey(ctx10(t), &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(), KeyId: auth.KeyID(auth.PublicKey(key))}); err != nil {
		t.Fatalf("a tenant admin revokes the key of a team agent: %v", err)
	}
	_, err = agent.Identity.WhoAmI(ctx10(t), &identityv1.WhoAmIRequest{})
	wantCode(t, "after the key was revoked", err, connect.CodeUnauthenticated)
	key2 := mustNewKey(t)
	if err := addKey(ada, "minutes", key2); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.User.RevokeKey(ctx10(t), &userv1.RevokeKeyRequest{CommandId: coreapi.NewCommandID(), KeyId: auth.KeyID(auth.PublicKey(key2))}); err == nil {
		t.Fatal("a member revokes the key of a team agent")
	}
}

func mustNewKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	k, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

package contract

// Contract tests of Identity (bb-identity) through if-user-api: memberships, system admins, the
// validity of users and memberships, the first system admin and the rules for the hub
// repository (dec-multi-tenancy, concept-multi-tenancy).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const (
	roleMember = userv1.Role_ROLE_MEMBER
	roleAdmin  = userv1.Role_ROLE_TENANT_ADMIN
)

func setMember(t *testing.T, a *contracttest.Actor, tenant, user string, role userv1.Role) error {
	t.Helper()
	_, err := a.User.SetMembership(ctx10(t), &userv1.SetMembershipRequest{CommandId: coreapi.NewCommandID(),
		TenantId: tenant, UserId: user, Role: role})
	return err
}

func removeMember(t *testing.T, a *contracttest.Actor, tenant, user string) error {
	t.Helper()
	_, err := a.User.RemoveMembership(ctx10(t), &userv1.RemoveMembershipRequest{CommandId: coreapi.NewCommandID(),
		TenantId: tenant, UserId: user})
	return err
}

func listMembers(t *testing.T, a *contracttest.Actor, tenant string) (map[string]*userv1.Membership, error) {
	t.Helper()
	res, err := a.User.ListMemberships(ctx10(t), &userv1.ListMembershipsRequest{TenantId: tenant})
	out := map[string]*userv1.Membership{}
	for _, m := range res.GetMemberships() {
		out[m.GetUserId()] = m
	}
	return out, err
}

func active(m *userv1.Membership) bool { return m != nil && m.GetValidUntil() == nil }

// Step two: a system admin or the tenant admin assigns users to a tenant. The first member of a
// tenant is a tenant admin. A tenant admin manages only its own tenant.
func TestMembershipRights(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	ada := srv.Participant(t, "ada", "Ada", human)
	bob := srv.Participant(t, "bob", "Bob", human)
	cy := srv.Participant(t, "cy", "Cy", human)
	dee := srv.Participant(t, "dee", "Dee", human)

	wantCode(t, "first member as a plain member", setMember(t, admin, "acme", "ada", roleMember), connect.CodeFailedPrecondition)
	wantCode(t, "bad tenant ID", setMember(t, admin, "a b", "ada", roleAdmin), connect.CodeInvalidArgument)
	wantCode(t, "membership of an unknown user", setMember(t, admin, "acme", "nobody", roleAdmin), connect.CodeNotFound)
	wantCode(t, "membership without a role", setMember(t, admin, "acme", "ada", userv1.Role_ROLE_UNSPECIFIED), connect.CodeInvalidArgument)
	wantCode(t, "membership set by a user who is no manager", setMember(t, bob, "acme", "bob", roleAdmin), connect.CodePermissionDenied)

	if err := setMember(t, admin, "acme", "ada", roleAdmin); err != nil {
		t.Fatal(err)
	}
	// Ada is tenant admin of acme: she manages acme but not another tenant.
	if err := setMember(t, ada, "acme", "bob", roleMember); err != nil {
		t.Fatalf("tenant admin adds a member: %v", err)
	}
	wantCode(t, "tenant admin manages another tenant", setMember(t, ada, "globex", "cy", roleAdmin), connect.CodePermissionDenied)
	wantCode(t, "member adds a member", setMember(t, bob, "acme", "cy", roleMember), connect.CodePermissionDenied)
	wantCode(t, "member removes a member", removeMember(t, bob, "acme", "ada"), connect.CodePermissionDenied)

	// Listing: members and system admins see a tenant; others get "not found", as for a tenant
	// that does not exist.
	m, err := listMembers(t, bob, "acme")
	if err != nil || !active(m["ada"]) || m["ada"].GetRole() != roleAdmin || !active(m["bob"]) || m["bob"].GetRole() != roleMember {
		t.Fatalf("members as a member: %v, %v", m, err)
	}
	if _, err := listMembers(t, admin, "acme"); err != nil {
		t.Fatalf("members as a system admin: %v", err)
	}
	_, err = listMembers(t, cy, "acme")
	wantCode(t, "members as an outsider", err, connect.CodeNotFound)
	_, errMissing := listMembers(t, cy, "no-such-tenant")
	if connect.CodeOf(errMissing) != connect.CodeNotFound {
		t.Fatalf("members of an unknown tenant: %v", errMissing)
	}

	// A second tenant admin allows the first one to go; the last one stays.
	wantCode(t, "remove the last tenant admin", removeMember(t, admin, "acme", "ada"), connect.CodeFailedPrecondition)
	if err := setMember(t, ada, "acme", "bob", roleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := removeMember(t, bob, "acme", "ada"); err != nil {
		t.Fatalf("a tenant admin removes another tenant admin: %v", err)
	}
	wantCode(t, "demote the last tenant admin", setMember(t, bob, "acme", "bob", roleMember), connect.CodeFailedPrecondition)
	// Ada is no member any more: she sees nothing, and her membership is ended, not deleted.
	_, err = listMembers(t, ada, "acme")
	wantCode(t, "members as a removed member", err, connect.CodeNotFound)
	m, err = listMembers(t, admin, "acme")
	if err != nil || m["ada"] == nil || active(m["ada"]) || m["ada"].GetValidUntil() == nil {
		t.Fatalf("membership of ada after the removal: %v, %v", m["ada"], err)
	}
	wantCode(t, "end a membership that is ended", removeMember(t, bob, "acme", "ada"), connect.CodeNotFound)
	// A returning member starts a new period.
	if err := setMember(t, bob, "acme", "ada", roleMember); err != nil {
		t.Fatal(err)
	}
	if m, _ = listMembers(t, bob, "acme"); !active(m["ada"]) || m["ada"].GetRole() != roleMember {
		t.Fatalf("returning member: %v", m["ada"])
	}
	_ = cy
	_ = dee
}

// A system admin is never a member of a tenant, and no member is made a system admin.
func TestSystemAdminIsNeverMember(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)
	boss := srv.Participant(t, "boss", "Boss", human)
	setAdmin := func(a *contracttest.Actor, user string, on bool) error {
		_, err := a.User.SetSystemAdmin(ctx10(t), &userv1.SetSystemAdminRequest{CommandId: coreapi.NewCommandID(),
			UserId: user, SystemAdmin: on})
		return err
	}

	wantCode(t, "membership of a system admin", setMember(t, admin, "acme", contracttest.AdminID, roleAdmin), connect.CodeFailedPrecondition)
	if err := setMember(t, admin, "acme", "ada", roleAdmin); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "system admin role for a member", setAdmin(admin, "ada", true), connect.CodeFailedPrecondition)
	wantCode(t, "system admin role for an agent", setAdmin(admin, "bot", true), connect.CodeFailedPrecondition)
	wantCode(t, "system admin role set by a tenant admin", setAdmin(ada, "boss", true), connect.CodePermissionDenied)
	wantCode(t, "system admin role set by an agent", setAdmin(bot, "boss", true), connect.CodePermissionDenied)

	// A second system admin: boss. Then the first one may go, but never the last one.
	if err := setAdmin(admin, "boss", true); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "membership of the new system admin", setMember(t, ada, "acme", "boss", roleMember), connect.CodeFailedPrecondition)
	if _, err := boss.User.ListUsers(ctx10(t), &userv1.ListUsersRequest{}); err != nil {
		t.Fatalf("list users as a new system admin: %v", err)
	}
	if err := setAdmin(boss, contracttest.AdminID, false); err != nil {
		t.Fatalf("take the role from the first system admin: %v", err)
	}
	wantCode(t, "take the role from the last system admin", setAdmin(boss, "boss", false), connect.CodeFailedPrecondition)
	if _, err := admin.User.ListUsers(ctx10(t), &userv1.ListUsersRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("first system admin after the role was taken away: %v", err)
	}
}

// Removing a user ends its validity, its memberships and its keys at once, and keeps the record,
// so the ID is never used again.
func TestRemoveUserKeepsTheRecord(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	ada := srv.Participant(t, "ada", "Ada", human)
	bob := srv.Participant(t, "bob", "Bob", human)
	if err := setMember(t, admin, "acme", "ada", roleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := setMember(t, admin, "acme", "bob", roleMember); err != nil {
		t.Fatal(err)
	}
	remove := func(user string) error {
		_, err := admin.User.RemoveUser(ctx10(t), &userv1.RemoveUserRequest{CommandId: coreapi.NewCommandID(), UserId: user})
		return err
	}
	wantCode(t, "remove a user as a user", func() error {
		_, err := bob.User.RemoveUser(ctx10(t), &userv1.RemoveUserRequest{CommandId: coreapi.NewCommandID(), UserId: "ada"})
		return err
	}(), connect.CodePermissionDenied)
	wantCode(t, "remove an unknown user", remove("nobody"), connect.CodeNotFound)
	wantCode(t, "remove the last tenant admin by removing the user", remove("ada"), connect.CodeFailedPrecondition)
	wantCode(t, "remove the last system admin", remove(contracttest.AdminID), connect.CodeFailedPrecondition)

	if err := setMember(t, admin, "acme", "bob", roleAdmin); err != nil {
		t.Fatal(err)
	}
	adaToken, err := ada.Tokens.Token(ctx10(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := remove("ada"); err != nil {
		t.Fatal(err)
	}
	// Her token and key stop working at once.
	_, err = srv.WithToken(t, adaToken).Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: "s", CommandId: coreapi.NewCommandID()})
	wantCode(t, "token of a removed user", err, connect.CodeUnauthenticated)
	_, err = srv.Anonymous(t).Identity.GetChallenge(ctx10(t), &identityv1.GetChallengeRequest{PublicKey: auth.PublicKey(ada.Key)})
	wantCode(t, "challenge for a removed user", err, connect.CodeUnauthenticated)
	wantCode(t, "remove a user twice", remove("ada"), connect.CodeFailedPrecondition)

	// The record stays: the ID is taken, the membership is ended, the user is listed.
	_, err = admin.User.CreateUser(ctx10(t), &userv1.CreateUserRequest{CommandId: coreapi.NewCommandID(), UserId: "ada",
		DisplayName: "Ada II", Kind: human, PublicKey: auth.PublicKey(newKey(t))})
	wantCode(t, "create a user with the ID of a removed user", err, connect.CodeAlreadyExists)
	wantCode(t, "membership of a removed user", setMember(t, admin, "acme", "ada", roleMember), connect.CodeNotFound)
	m, err := listMembers(t, bob, "acme")
	if err != nil || m["ada"] == nil || active(m["ada"]) {
		t.Fatalf("membership of the removed user: %v, %v", m["ada"], err)
	}
	users, err := admin.User.ListUsers(ctx10(t), &userv1.ListUsersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, u := range users.GetUsers() {
		if u.GetUserId() == "ada" {
			found = true
			if u.GetValidUntil() == nil || u.GetSystemAdmin() {
				t.Errorf("removed user in the list: %v", u)
			}
		}
	}
	if !found {
		t.Error("the removed user is not listed")
	}
}

// At the first start the operator names the first system admin; without it the hub does not
// start. Later starts ignore the flag.
func TestFirstSystemAdmin(t *testing.T) {
	key := newKey(t)
	signing := filepath.Join(t.TempDir(), "signing.key")
	keygen(t, signing)
	base := func(data string, extra ...string) []string {
		return append([]string{"-addr", "127.0.0.1:0", "-data-dir", data, "-signing-key", signing}, extra...)
	}
	flag := "root=" + auth.FormatPublicKey(auth.PublicKey(key))

	for name, v := range map[string][]string{
		"no flag":     nil,
		"no key":      {"-first-system-admin", "root"},
		"bad key":     {"-first-system-admin", "root=nope"},
		"bad user ID": {"-first-system-admin", "ro ot=" + auth.FormatPublicKey(auth.PublicKey(key))},
	} {
		t.Run(name, func(t *testing.T) {
			_, errOut, code := contracttest.Run(t, "office-hub", nil, base(t.TempDir(), v...)...)
			if code == 0 || strings.Contains(errOut, "listening on") {
				t.Fatalf("hub started without a valid first system admin:\n%s", errOut)
			}
			if !strings.Contains(errOut, "first-system-admin") {
				t.Errorf("error does not name the flag:\n%s", errOut)
			}
		})
	}

	// First start with the flag, then a restart with another value: the first admin stays.
	srv := contracttest.StartHub(t, nil)
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	other := newKey(t)
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil,
		"-first-system-admin", "root="+auth.FormatPublicKey(auth.PublicKey(other)))
	users, err := srv.Admin(t).User.ListUsers(ctx10(t), &userv1.ListUsersRequest{})
	if err != nil || len(users.GetUsers()) != 1 || users.GetUsers()[0].GetUserId() != contracttest.AdminID {
		t.Fatalf("users after a restart with another first system admin: %v, %v", users, err)
	}
	_, err = srv.Anonymous(t).Identity.GetChallenge(ctx10(t), &identityv1.GetChallengeRequest{PublicKey: auth.PublicKey(other)})
	wantCode(t, "key of the ignored first system admin", err, connect.CodeUnauthenticated)
	_ = flag
}

// The hub loads the hub repository, checks the rules and does not start when a manual edit
// breaks one: nobody gets rights by editing a file.
func TestHubRepositoryBreakingARuleStopsTheHub(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	if err := setMember(t, srv.Admin(t), "acme", "ada", roleAdmin); err != nil {
		t.Fatal(err)
	}
	_ = ada
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	repo := srv.ArchiveDir()
	start := func() (string, int) {
		_, errOut, code := contracttest.Run(t, "office-hub", nil, "-addr", "127.0.0.1:0", "-data-dir", srv.DataDir,
			"-signing-key", srv.SigningKeyFile)
		return errOut, code
	}
	edit := func(file, content string) {
		p := filepath.Join(repo, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		contracttest.Git(t, repo, "add", "-A")
		contracttest.Git(t, repo, "-c", "user.name=editor", "-c", "user.email=e@example.org", "commit", "-q", "-m", "manual edit")
	}

	// The first system admin becomes a member of a tenant.
	edit("system/memberships/acme/"+contracttest.AdminID+".yaml",
		"role: member\nvalid_from: 2026-01-01T00:00:00Z\n")
	errOut, code := start()
	if code == 0 || !strings.Contains(errOut, "tenant member") {
		t.Fatalf("hub started with a system admin in a tenant (exit %d):\n%s", code, errOut)
	}
	contracttest.Git(t, repo, "reset", "-q", "--hard", "HEAD~1")

	// A file with a field that nobody knows (a typo) is not ignored.
	edit("system/memberships/acme/ada.yaml", "role: tenant_admin\nvalid_form: 2026-01-01T00:00:00Z\n")
	errOut, code = start()
	if code == 0 || !strings.Contains(errOut, "valid_form") {
		t.Fatalf("hub started with a file that has an unknown field (exit %d):\n%s", code, errOut)
	}
	contracttest.Git(t, repo, "reset", "-q", "--hard", "HEAD~1")

	// The repository as the hub wrote it starts again.
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	if _, err := listMembers(t, srv.Admin(t), "acme"); err != nil {
		t.Fatalf("hub after the edits were undone: %v", err)
	}
}

// Users, keys and memberships survive a restart, with the validity of the memberships.
func TestUsersSurviveRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	srv.Participant(t, "bob", "Bob", human)
	if err := setMember(t, srv.Admin(t), "acme", "ada", roleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := setMember(t, ada, "acme", "bob", roleMember); err != nil {
		t.Fatal(err)
	}
	if err := removeMember(t, ada, "acme", "bob"); err != nil {
		t.Fatal(err)
	}
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	again := srv.WithKey(t, ada.Key)
	m, err := listMembers(t, again, "acme")
	if err != nil || !active(m["ada"]) || m["ada"].GetRole() != roleAdmin || m["bob"] == nil || active(m["bob"]) {
		t.Fatalf("memberships after the restart: %v, %v", m, err)
	}
}

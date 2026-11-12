package contract

// Contract tests of Tenants (bb-tenants) through if-tenant-api, together with the tenant records
// that Identity keeps (if-tenant-roster, reached through the tenant API): who may create, list and
// remove a tenant, the first tenant admin, validity and the rules of the hub repository.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"

	tenantv1 "github.com/mrsimpson/agentic-office/hub/tenant/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

func removeTenant(t *testing.T, a *contracttest.Actor, id string) error {
	t.Helper()
	_, err := a.Tenant.RemoveTenant(ctx10(t), &tenantv1.RemoveTenantRequest{CommandId: coreapi.NewCommandID(), TenantId: id})
	return err
}

func tenantList(t *testing.T, a *contracttest.Actor) (map[string]*tenantv1.Tenant, error) {
	t.Helper()
	res, err := a.Tenant.ListTenants(ctx10(t), &tenantv1.ListTenantsRequest{})
	out := map[string]*tenantv1.Tenant{}
	for _, tn := range res.GetTenants() {
		out[tn.GetTenantId()] = tn
	}
	return out, err
}

// Only a system admin creates, lists and removes tenants. A tenant starts with its first tenant
// admin; the system admin does not become a member.
func TestTenantsCreateListRemove(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	ada := srv.Participant(t, "ada", "Ada", human)
	bob := srv.Participant(t, "bob", "Bob", human)

	wantCode(t, "create as a user", createTenant(t, ada, "acme", "ada"), connect.CodePermissionDenied)
	wantCode(t, "create without a token", func() error {
		_, err := srv.Anonymous(t).Tenant.CreateTenant(ctx10(t), &tenantv1.CreateTenantRequest{CommandId: coreapi.NewCommandID(),
			TenantId: "acme", DisplayName: "Acme", FirstAdminUserId: "ada"})
		return err
	}(), connect.CodeUnauthenticated)
	_, err := ada.Tenant.ListTenants(ctx10(t), &tenantv1.ListTenantsRequest{})
	wantCode(t, "list as a user", err, connect.CodePermissionDenied)
	wantCode(t, "remove as a user", removeTenant(t, ada, "acme"), connect.CodePermissionDenied)

	wantCode(t, "bad tenant ID", createTenant(t, admin, "a b", "ada"), connect.CodeInvalidArgument)
	wantCode(t, "no tenant ID", createTenant(t, admin, "", "ada"), connect.CodeInvalidArgument)
	_, err = admin.Tenant.CreateTenant(ctx10(t), &tenantv1.CreateTenantRequest{CommandId: coreapi.NewCommandID(),
		TenantId: "acme", FirstAdminUserId: "ada"})
	wantCode(t, "no display name", err, connect.CodeInvalidArgument)
	wantCode(t, "first admin who is unknown", createTenant(t, admin, "acme", "nobody"), connect.CodeNotFound)
	wantCode(t, "first admin who is a system admin", createTenant(t, admin, "acme", contracttest.AdminID), connect.CodeFailedPrecondition)

	req := &tenantv1.CreateTenantRequest{CommandId: coreapi.NewCommandID(), TenantId: "acme", DisplayName: "Acme", FirstAdminUserId: "ada"}
	if _, err := admin.Tenant.CreateTenant(ctx10(t), req); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Tenant.CreateTenant(ctx10(t), req); err != nil {
		t.Fatalf("repeated create with the same command_id: %v", err)
	}
	changed := &tenantv1.CreateTenantRequest{CommandId: req.GetCommandId(), TenantId: "acme", DisplayName: "Other", FirstAdminUserId: "ada"}
	_, err = admin.Tenant.CreateTenant(ctx10(t), changed)
	wantCode(t, "command_id reused for another request", err, connect.CodeAlreadyExists)
	wantCode(t, "create a tenant that exists", createTenant(t, admin, "acme", "bob"), connect.CodeAlreadyExists)

	ts, err := tenantList(t, admin)
	if err != nil || len(ts) != 1 || ts["acme"].GetDisplayName() != "Acme" || ts["acme"].GetValidUntil() != nil {
		t.Fatalf("tenants: %v, %v", ts, err)
	}
	// Ada is the tenant admin; the system admin is no member and sees the members.
	m, err := listMembers(t, admin, "acme")
	if err != nil || len(m) != 1 || !active(m["ada"]) || m["ada"].GetRole() != roleAdmin {
		t.Fatalf("members of the new tenant: %v, %v", m, err)
	}

	// Removing ends the tenant and its memberships and keeps the records. The ID stays taken.
	if err := setMember(t, ada, "acme", "bob", roleMember); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "remove an unknown tenant", removeTenant(t, admin, "nope"), connect.CodeNotFound)
	if err := removeTenant(t, admin, "acme"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "remove a tenant twice", removeTenant(t, admin, "acme"), connect.CodeFailedPrecondition)
	ts, _ = tenantList(t, admin)
	if ts["acme"].GetValidUntil() == nil {
		t.Fatalf("removed tenant: %v", ts["acme"])
	}
	m, err = listMembers(t, admin, "acme")
	if err != nil || len(m) != 2 || active(m["ada"]) || active(m["bob"]) {
		t.Fatalf("memberships of the removed tenant: %v, %v", m, err)
	}
	_, err = listMembers(t, bob, "acme")
	wantCode(t, "members of a removed tenant as a former member", err, connect.CodeNotFound)
	wantCode(t, "create a tenant with the ID of a removed tenant", createTenant(t, admin, "acme", "ada"), connect.CodeAlreadyExists)
	wantCode(t, "membership in a removed tenant", setMember(t, admin, "acme", "bob", roleMember), connect.CodeNotFound)
	// Ada is free to lead another tenant: her membership in acme ended.
	if err := createTenant(t, admin, "globex", "ada"); err != nil {
		t.Fatalf("first admin of another tenant: %v", err)
	}
}

// Tenants and memberships survive a restart, with their validity.
func TestTenantsSurviveRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	srv.Participant(t, "ada", "Ada", human)
	srv.Participant(t, "bob", "Bob", human)
	if err := createTenant(t, admin, "acme", "ada"); err != nil {
		t.Fatal(err)
	}
	if err := createTenant(t, admin, "globex", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := removeTenant(t, admin, "globex"); err != nil {
		t.Fatal(err)
	}
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	ts, err := tenantList(t, srv.Admin(t))
	if err != nil || len(ts) != 2 || ts["acme"].GetValidUntil() != nil || ts["globex"].GetValidUntil() == nil {
		t.Fatalf("tenants after the restart: %v, %v", ts, err)
	}
	wantCode(t, "tenant ID of a removed tenant after the restart", createTenant(t, srv.Admin(t), "globex", "ada"), connect.CodeAlreadyExists)
}

// A manual edit of the hub repository that breaks a rule of the tenants stops the hub.
func TestHubRepositoryWithBrokenTenantsStopsTheHub(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	srv.Participant(t, "ada", "Ada", human)
	if err := createTenant(t, srv.Admin(t), "acme", "ada"); err != nil {
		t.Fatal(err)
	}
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	repo := srv.HubRepoDir()
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
	undo := func() { contracttest.Git(t, repo, "reset", "-q", "--hard", "HEAD~1") }

	// A tenant without a tenant admin.
	edit("system/tenants/globex.yaml", "display_name: Globex\nvalid_from: 2026-01-01T00:00:00Z\n")
	if errOut, code := start(); code == 0 || !strings.Contains(errOut, "globex needs at least one tenant admin") {
		t.Fatalf("hub started with a tenant without a tenant admin (exit %d):\n%s", code, errOut)
	}
	undo()
	// A membership in a tenant that does not exist.
	edit("system/memberships/nowhere/ada.yaml", "role: tenant_admin\nvalid_from: 2026-01-01T00:00:00Z\n")
	if errOut, code := start(); code == 0 || !strings.Contains(errOut, "no such tenant") {
		t.Fatalf("hub started with a membership in an unknown tenant (exit %d):\n%s", code, errOut)
	}
	undo()
	// The hub still starts with the repository as it wrote it.
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	if _, err := listMembers(t, srv.Admin(t), "acme"); err != nil {
		t.Fatalf("hub after the edits were undone: %v", err)
	}
}

// Each tenant has a repository of its own. The users, tenants and memberships are in the hub
// repository and not in a tenant repository. A tenant repository is created with the tenant and
// stays when the tenant is removed.
func TestTenantRepositories(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	admin := srv.Admin(t)
	srv.Participant(t, "ada", "Ada", human)
	srv.Participant(t, "bob", "Bob", human)

	for _, bad := range []string{"..", "a/b", ".hidden", "a b"} {
		wantCode(t, "tenant ID "+bad, createTenant(t, admin, bad, "ada"), connect.CodeInvalidArgument)
	}
	if _, err := os.Stat(filepath.Join(srv.DataDir, "repos", "tenants")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(srv.DataDir, "repos", "tenants"))
		if len(entries) != 0 {
			t.Fatalf("a repository was created for a bad tenant ID: %v", entries)
		}
	}
	if err := createTenant(t, admin, "acme", "ada"); err != nil {
		t.Fatal(err)
	}
	if err := createTenant(t, admin, "globex", "bob"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"acme", "globex"} {
		dir := srv.TenantRepoDir(id)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			t.Fatalf("repository of %s: %v", id, err)
		}
		// Nothing of the users or memberships is in a tenant repository.
		if files := contracttest.Git(t, dir, "ls-files"); strings.TrimSpace(files) != "" {
			t.Errorf("files in the repository of %s: %s", id, files)
		}
	}
	// The hub repository has the users, tenants and memberships of both.
	files := contracttest.Git(t, srv.HubRepoDir(), "ls-files")
	for _, want := range []string{"system/tenants/acme.yaml", "system/tenants/globex.yaml", "system/users/ada.yaml",
		"system/memberships/acme/ada.yaml", "system/memberships/globex/bob.yaml"} {
		if !strings.Contains(files, want) {
			t.Errorf("hub repository has no %s:\n%s", want, files)
		}
	}
	// A removed tenant keeps its repository.
	if err := removeTenant(t, admin, "globex"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srv.TenantRepoDir("globex"), ".git")); err != nil {
		t.Fatalf("repository of a removed tenant: %v", err)
	}
	// Creating a tenant is one commit, with the tenant and its first membership.
	log := contracttest.Git(t, srv.HubRepoDir(), "log", "-1", "--name-only", "--format=%s", "--grep=Create tenant acme")
	if !strings.Contains(log, "Create tenant acme") || !strings.Contains(log, "system/memberships/acme/ada.yaml") {
		t.Errorf("commit that created acme:\n%s", log)
	}
}

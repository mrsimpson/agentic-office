//go:build e2e

package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// TestTwoTenantsWithTheCLI: a system admin sets up a hub with the office CLI: two tenants, each
// with its own tenant admin, and a user who is a member of both. The tenant admins manage their
// members. Each tenant sees only its own records, the user in both picks the tenant with --tenant,
// and the system admin, who is no member, reads no content.
func TestTwoTenantsWithTheCLI(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	runtime := filepath.Join(t.TempDir(), "run")
	envFor := func(key, tenant string) []string {
		return []string{"OFFICE_HUB=" + srv.URL, "OFFICE_CA_CERT=" + srv.CertFile, "OFFICE_TENANT=" + tenant, "OFFICE_KEY=" + key,
			"OFFICE_RUNTIME_DIR=" + runtime, "OFFICE_SESSION=", "OFFICE_JSON="}
	}
	run := func(env []string, args ...string) (map[string]any, string, int) {
		t.Helper()
		out, errOut, code := contracttest.Run(t, "office", env, append(args, "--json")...)
		var v map[string]any
		if out != "" {
			_ = json.Unmarshal([]byte(out), &v)
		}
		return v, errOut + out, code
	}
	ok := func(env []string, args ...string) map[string]any {
		t.Helper()
		v, text, code := run(env, args...)
		if code != 0 {
			t.Fatalf("office %v: exit %d: %s", args, code, text)
		}
		return v
	}
	adminKey := filepath.Join(t.TempDir(), "admin.pem")
	if err := auth.WriteKey(adminKey, srv.AdminKey); err != nil {
		t.Fatal(err)
	}
	admin := envFor(adminKey, "")
	keys := map[string]string{}
	user := func(id string) {
		key := filepath.Join(t.TempDir(), id+".pem")
		k := ok(envFor(key, ""), "key", "create")
		ok(admin, "admin", "user", "create", id, k["publicKey"].(string), "--name", id, "--kind", "human")
		keys[id] = key
	}
	for _, id := range []string{"ada", "bob", "cy"} {
		user(id)
	}

	ok(admin, "admin", "tenant", "create", "acme", "ada", "--name", "Acme Corp")
	ok(admin, "admin", "tenant", "create", "globex", "bob")
	ts := ok(admin, "admin", "tenant", "list")["tenants"].([]any)
	if len(ts) != 2 {
		t.Fatalf("tenants: %v", ts)
	}
	// The tenant admins add Cy to their tenants; the system admin did not do it.
	ada, bob := envFor(keys["ada"], "acme"), envFor(keys["bob"], "globex")
	ok(ada, "admin", "member", "set", "acme", "cy")
	ok(bob, "admin", "member", "set", "globex", "cy", "--role", "member")
	if _, text, code := run(ada, "admin", "member", "set", "globex", "cy"); code == 0 {
		t.Fatalf("the tenant admin of acme changed globex: %s", text)
	}
	ms := ok(ada, "admin", "member", "list", "acme")["memberships"].([]any)
	if len(ms) != 2 {
		t.Fatalf("members of acme: %v", ms)
	}

	// Records of each tenant. Cy picks the tenant with --tenant.
	ok(ada, "decision", "record", "Acme ships.")
	ok(bob, "decision", "record", "Globex waits.")
	cyIn := func(tenant string) []string { return envFor(keys["cy"], tenant) }
	for tenant, want := range map[string]string{"acme": "Acme ships.", "globex": "Globex waits."} {
		out, errOut, code := contracttest.Run(t, "office", cyIn(""), "decision", "list", "--tenant", tenant)
		if code != 0 || !strings.Contains(out, want) || strings.Contains(out, map[string]string{"acme": "Globex", "globex": "Acme"}[tenant]) {
			t.Fatalf("decisions of %s read by Cy: exit %d, %q, %q", tenant, code, out, errOut)
		}
	}
	// The system admin is no member: no content, and the same answer as for an unknown tenant.
	_, text, code := run(envFor(adminKey, "acme"), "decision", "list")
	if code == 0 || !strings.Contains(text, "not_found") {
		t.Fatalf("the system admin read the decisions of acme: exit %d, %s", code, text)
	}
	if _, text, code = run(cyIn(""), "decision", "list"); code == 0 || !strings.Contains(text, "tenant") {
		t.Fatalf("a call without a tenant: exit %d, %s", code, text)
	}

	// A removed member loses the tenant at once; a removed tenant is gone for everyone.
	ok(ada, "admin", "member", "remove", "acme", "cy")
	if _, text, code := run(cyIn("acme"), "decision", "list"); code == 0 || !strings.Contains(text, "not_found") {
		t.Fatalf("Cy after the removal from acme: exit %d, %s", code, text)
	}
	ok(admin, "admin", "tenant", "remove", "globex")
	if _, text, code := run(bob, "decision", "list"); code == 0 || !strings.Contains(text, "not_found") {
		t.Fatalf("Bob in the removed tenant: exit %d, %s", code, text)
	}
}

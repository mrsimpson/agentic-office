//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// TestTwoHubsWithProfiles: one participant works with two hubs from one client. The client has
// no OFFICE_HUB, OFFICE_CA_CERT or OFFICE_TENANT. "office hub add" sets up one profile for each
// hub, with the fingerprint from the log of the hub. The default profile, the variable, the flag
// and the project file choose the hub; each hub keeps only its own records. A meeting joined
// through a profile also reaches its hub from the replicator process that the join starts.
func TestTwoHubsWithProfiles(t *testing.T) {
	hubs := map[string]*contracttest.Hub{"one": contracttest.StartHub(t, nil), "two": contracttest.StartHub(t, nil)}

	// The participant has one key, registered at both hubs, and an empty configuration folder.
	home, start := t.TempDir(), t.TempDir()
	cfg := filepath.Join(home, "agentic-office", "client")
	key, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteKey(filepath.Join(cfg, "key.pem"), key); err != nil {
		t.Fatal(err)
	}
	for _, h := range hubs {
		h.RegisterKey(t, "ada", "Ada", identityv1.ActorKind_ACTOR_KIND_HUMAN, key)
		h.EnsureMember(t, "ada")
	}
	env := []string{"XDG_CONFIG_HOME=" + home, "OFFICE_RUNTIME_DIR=" + filepath.Join(t.TempDir(), "run"),
		"OFFICE_SESSION=", "OFFICE_JSON="}
	office := func(extraEnv []string, stdin string, args ...string) (string, string, int) {
		t.Helper()
		return contracttest.RunIn(t, start, "office", append(append([]string{}, env...), extraEnv...), strings.NewReader(stdin), args...)
	}
	ok := func(extraEnv []string, args ...string) string {
		t.Helper()
		out, errOut, code := office(extraEnv, "", args...)
		if code != 0 {
			t.Fatalf("office %v: exit %d: %s%s", args, code, out, errOut)
		}
		return out
	}

	// Set up once: the first profile with the fingerprint from the log, the second with an answer.
	fp := regexp.MustCompile(`sha256 ([0-9a-f]{64})`).FindStringSubmatch(hubs["one"].Stderr())[1]
	ok(nil, "hub", "add", "one", hubs["one"].URL, "--tenant", contracttest.DefaultTenant, "--fingerprint", fp, "--default")
	if out, errOut, code := office(nil, "yes\n", "hub", "add", "two", hubs["two"].URL, "--tenant", contracttest.DefaultTenant); code != 0 {
		t.Fatalf("hub add two: exit %d: %s%s", code, out, errOut)
	}
	var list struct {
		Active   string
		Profiles []struct{ Name string }
	}
	if err := json.Unmarshal([]byte(ok(nil, "hub", "list", "--json")), &list); err != nil || list.Active != "one" || len(list.Profiles) != 2 {
		t.Fatalf("hub list: %v, %+v", err, list)
	}

	// The default profile, the flag, the variable and the project file choose the hub.
	ok(nil, "decision", "record", "Hub one decides.")
	ok(nil, "decision", "record", "Hub two decides.", "--hub-profile", "two")
	decisions := func(extraEnv []string, args ...string) string {
		return ok(extraEnv, append([]string{"decision", "list"}, args...)...)
	}
	only := func(label, out, want, other string) {
		t.Helper()
		if !strings.Contains(out, want) || strings.Contains(out, other) {
			t.Errorf("%s: want only %q:\n%s", label, want, out)
		}
	}
	only("default profile", decisions(nil), "Hub one decides.", "Hub two")
	only("flag", decisions(nil, "--hub-profile", "two"), "Hub two decides.", "Hub one")
	only("variable", decisions([]string{"OFFICE_HUB_PROFILE=two"}), "Hub two decides.", "Hub one")
	only("flag over variable", decisions([]string{"OFFICE_HUB_PROFILE=two"}, "--hub-profile", "one"), "Hub one decides.", "Hub two")
	if err := os.MkdirAll(filepath.Join(start, ".office"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(start, ".office", "config.yaml"), []byte("hub-profile: two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	only("project file", decisions(nil), "Hub two decides.", "Hub one")

	// A meeting joined through the profile of the project file: the replicator reaches hub two.
	const sid = "two-hubs"
	var joined struct {
		LogPath       string
		ReplicatorPID int
	}
	if err := json.Unmarshal([]byte(ok(nil, "meeting", "join", sid, "--json")), &joined); err != nil || joined.ReplicatorPID == 0 {
		t.Fatalf("join: %v, %+v", err, joined)
	}
	t.Cleanup(func() { _ = syscall.Kill(joined.ReplicatorPID, syscall.SIGKILL) })
	ok(nil, "say", "Hello from hub two.")
	deadline := time.Now().Add(15 * time.Second)
	for {
		b, _ := os.ReadFile(joined.LogPath)
		if strings.Contains(string(b), "Hello from hub two.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the local log has no utterance:\n%s", b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Hub two has the utterance (the profile of the project file decided), hub one has not.
	if out := ok(nil, "meeting", "events", "--session", sid); !strings.Contains(out, "Hello from hub two.") {
		t.Errorf("hub two lacks the utterance:\n%s", out)
	}
	if out := ok(nil, "meeting", "events", "--session", sid, "--hub-profile", "one"); strings.Contains(out, "Hello from hub two.") {
		t.Errorf("hub one must not know the session of hub two:\n%s", out)
	}

	// Remove the profile one: the default is gone, and so is its certificate.
	ok(nil, "hub", "remove", "one")
	if _, err := os.Stat(filepath.Join(cfg, "hubs", "one.crt")); err == nil {
		t.Error("the certificate of the removed profile is still there")
	}
	if _, _, code := office([]string{"OFFICE_HUB_PROFILE=one"}, "", "decision", "list"); code != 2 {
		t.Errorf("a removed profile must be an error: exit %d", code)
	}
}

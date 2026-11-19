package contract

// Contract tests of the start of the office hub: a start that fails leaves nothing in the data
// directory, and a restart does not keep a self-signed certificate that misses a name of
// -tls-hosts.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// TestFailedStartWritesNothing: a start without the signing key or without a valid first system
// admin fails before it writes to the data directory, and says how to fix the call.
func TestFailedStartWritesNothing(t *testing.T) {
	signing := filepath.Join(t.TempDir(), "signing.key")
	keygen(t, signing)
	pub := auth.FormatPublicKey(auth.PublicKey(newKey(t)))
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no signing key":        {nil, "office-hub keygen"},
		"no first system admin": {[]string{"-signing-key", signing}, "office key create"},
		"bad first system admin": {[]string{"-signing-key", signing, "-first-system-admin", "root=nope"},
			"office key create"},
		"address not usable": {[]string{"-signing-key", signing, "-first-system-admin", "root=" + pub, "-addr", "256.0.0.1:1"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "data")
			args := append([]string{"-addr", "127.0.0.1:0", "-data-dir", data, "-tls-hosts", "hub.example.org"}, c.args...)
			_, errOut, code := contracttest.Run(t, "office-hub", nil, args...)
			if code == 0 || strings.Contains(errOut, "listening on") {
				t.Fatalf("hub started:\n%s", errOut)
			}
			if !strings.Contains(errOut, c.want) {
				t.Errorf("error does not name %q:\n%s", c.want, errOut)
			}
			if _, err := os.Stat(data); !os.IsNotExist(err) {
				entries, _ := os.ReadDir(data)
				t.Errorf("the failed start wrote to the data directory: %v", entries)
			}
		})
	}
}

// TestRestartWithOtherTLSHosts: a restart with -tls-hosts that the self-signed certificate does
// not cover does not start and names the fix. A name that the certificate covers starts.
func TestRestartWithOtherTLSHosts(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	args := []string{"-addr", "127.0.0.1:0", "-data-dir", srv.DataDir, "-signing-key", srv.SigningKeyFile}
	_, errOut, code := contracttest.Run(t, "office-hub", nil, append(args, "-tls-hosts", "localhost,hub.example.org")...)
	if code == 0 || strings.Contains(errOut, "listening on") {
		t.Fatalf("hub started with a certificate that misses a name of -tls-hosts:\n%s", errOut)
	}
	for _, want := range []string{"hub.example.org", "delete", "tls.crt", "tls.key"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("error does not name %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "does not cover localhost") {
		t.Errorf("error names a covered host:\n%s", errOut)
	}
	// The covered name starts.
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil, "-tls-hosts", "localhost")
	if _, err := srv.Stop(5 * time.Second); err != nil {
		t.Fatal(err)
	}
}

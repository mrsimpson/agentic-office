package contract

// Contract tests of the signing key of the hub (dec-hub-signing-key). The operator gives the hub
// its key file with -signing-key; the hub does not create a key by itself and does not start
// without one. "office-hub keygen FILE" creates a key file.

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/merkle"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// keygen creates a signing key file with "office-hub keygen" and returns its public key.
func keygen(t *testing.T, file string) ed25519.PublicKey {
	t.Helper()
	out, errOut, code := contracttest.Run(t, "office-hub", nil, "keygen", file)
	if code != 0 {
		t.Fatalf("keygen: exit %d: %s", code, errOut)
	}
	key, err := auth.ReadKey(file)
	if err != nil {
		t.Fatalf("read the new key file: %v", err)
	}
	pub := auth.PublicKey(key)
	if !strings.Contains(out, auth.FormatPublicKey(pub)) {
		t.Fatalf("keygen does not print the public key %s:\n%s", auth.FormatPublicKey(pub), out)
	}
	return pub
}

// The hub refuses to start without -signing-key, with a missing file and with a broken file. It
// says how to create a key and creates no key file itself.
func TestSigningKeyRequired(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.key")
	broken := filepath.Join(dir, "broken.key")
	if err := os.WriteFile(broken, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(dir, "open.key")
	keygen(t, open)
	if err := os.Chmod(open, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"no flag":            nil,
		"missing file":       {"-signing-key", missing},
		"broken file":        {"-signing-key", broken},
		"readable by others": {"-signing-key", open},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			data := t.TempDir()
			args := append([]string{"-addr", "127.0.0.1:0", "-data-dir", data}, extra...)
			_, errOut, code := contracttest.Run(t, "office-hub", nil, args...)
			if code == 0 {
				t.Fatalf("hub started without a valid signing key:\n%s", errOut)
			}
			if strings.Contains(errOut, "listening on") {
				t.Fatalf("hub listened without a valid signing key:\n%s", errOut)
			}
			if !strings.Contains(errOut, "office-hub keygen FILE") {
				t.Errorf("error does not say how to create a key:\n%s", errOut)
			}
		})
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("hub created the missing key file: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("files next to the keys changed: %v", entries)
	}
}

// keygen refuses to overwrite an existing file and needs exactly one file name.
func TestSigningKeygenRefusesOverwrite(t *testing.T) {
	file := filepath.Join(t.TempDir(), "signing.key")
	keygen(t, file)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, code := contracttest.Run(t, "office-hub", nil, "keygen", file)
	if code == 0 {
		t.Fatalf("keygen overwrote an existing file")
	}
	if !strings.Contains(errOut, "exists") {
		t.Errorf("error does not say that the file exists: %s", errOut)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("keygen changed an existing file")
	}
	if _, errOut, code := contracttest.Run(t, "office-hub", nil, "keygen"); code == 0 || !strings.Contains(errOut, "usage") {
		t.Errorf("keygen without a file: exit %d: %s", code, errOut)
	}
}

// The key from the file is the one that signs: GetHubKey returns its public key, and tree heads
// carry it and verify with it. A restart with the same file keeps old heads valid.
func TestSigningKeyFromFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "hub-signing.key")
	pub := keygen(t, file)

	m := newMeetingArgs(t, "-signing-key", file)
	if got := hubKey(t, m.reader.Commands); !bytes.Equal(got, pub) {
		t.Fatalf("GetHubKey: %x, want the key of the file %x", got, pub)
	}
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "Signed with the key of the operator.")
	ended := m.end(ada)
	log := m.readAll(0)
	heads := []*sessionstreamv1.SignedTreeHead{ended.GetSessionEnded().GetTreeHead(), m.head(0)}
	for _, h := range heads {
		if !bytes.Equal(h.GetPublicKey(), pub) {
			t.Fatalf("tree head of size %d names key %x, want %x", h.GetTreeSize(), h.GetPublicKey(), pub)
		}
		if err := merkle.VerifyEvents(log[:h.GetTreeSize()], h, pub); err != nil {
			t.Fatalf("head of size %d: %v", h.GetTreeSize(), err)
		}
	}

	m.srv.Kill()
	srv := contracttest.StartHubIn(t, m.srv.DataDir, m.srv.AdminKey, nil, "-signing-key", file)
	reader := srv.WithKey(t, m.reader.Key)
	if got := hubKey(t, reader.Commands); !bytes.Equal(got, pub) {
		t.Fatalf("GetHubKey after a restart: %x, want %x", got, pub)
	}
	for _, h := range heads {
		if err := merkle.VerifyEvents(log[:h.GetTreeSize()], h, pub); err != nil {
			t.Fatalf("head of size %d after a restart: %v", h.GetTreeSize(), err)
		}
	}
}

package contract

// Contract tests of the connection settings and the hub profiles of the office CLI (if-cli,
// concept-client-configuration): the keys hub, tenant, ca-cert, key and hub-profile and the
// mapping hubs in the configuration files, and the commands office hub add, list and remove. The
// tests run the real office binary against a real office-hub, with a temporary configuration
// folder (XDG_CONFIG_HOME) and a temporary start directory, and with none of the OFFICE_HUB,
// OFFICE_CA_CERT and OFFICE_TENANT variables unless a test sets them.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// profileClient is a participant with an empty configuration folder and start directory. Its
// environment has the key (unless key is "") and the runtime directory, but no hub, certificate
// or tenant.
type profileClient struct {
	t    *testing.T
	home string // XDG_CONFIG_HOME
	dir  string // the start directory
	env  []string
}

func (m *machine) profileClient(u *officeUser) *profileClient {
	m.t.Helper()
	return newProfileClient(m.t, u.key, m.runtime)
}

func newProfileClient(t *testing.T, key, runtime string) *profileClient {
	t.Helper()
	c := &profileClient{t: t, home: t.TempDir(), dir: t.TempDir()}
	c.env = []string{"OFFICE_RUNTIME_DIR=" + runtime, "OFFICE_SESSION=", "OFFICE_JSON=", "XDG_CONFIG_HOME=" + c.home}
	if key != "" {
		c.env = append(c.env, "OFFICE_KEY="+key)
	}
	return c
}

// configDir is the configuration folder of the client.
func (c *profileClient) configDir() string { return filepath.Join(c.home, "agentic-office", "client") }

// userFile is the path of the user file.
func (c *profileClient) userFile() string { return filepath.Join(c.configDir(), "config.yaml") }

// projectFile is the path of the project file.
func (c *profileClient) projectFile() string { return filepath.Join(c.dir, ".office", "config.yaml") }

func (c *profileClient) writeUser(content string, files map[string]string) {
	c.t.Helper()
	writeFiles(c.t, c.configDir(), map[string]string{"config.yaml": content})
	writeFiles(c.t, c.configDir(), files)
}

func (c *profileClient) writeProject(content string) {
	c.t.Helper()
	writeFiles(c.t, c.dir, map[string]string{filepath.Join(".office", "config.yaml"): content})
}

// run runs office with the extra environment env and the text stdin on standard input.
func (c *profileClient) run(env []string, stdin string, args ...string) (string, string, int) {
	c.t.Helper()
	return contracttest.RunIn(c.t, c.dir, "office", append(append([]string{}, c.env...), env...), strings.NewReader(stdin), args...)
}

// ok runs office and requires success.
func (c *profileClient) ok(args ...string) string {
	c.t.Helper()
	out, errOut, code := c.run(nil, "", args...)
	if code != 0 {
		c.t.Fatalf("office %v: exit %d: %s%s", args, code, out, errOut)
	}
	return out
}

// shown is one setting in the output of "office config show --json".
type shown struct {
	Value   any    `json:"value"`
	Source  string `json:"source"`
	From    string `json:"from"`
	Profile string `json:"profile"`
}

// show returns the settings of "office config show --json" by key, and the active profile.
func (c *profileClient) show(env []string, args ...string) (map[string]shown, string) {
	c.t.Helper()
	out, errOut, code := c.run(env, "", append([]string{"config", "show", "--json"}, args...)...)
	if code != 0 {
		c.t.Fatalf("config show %v: exit %d: %s%s", args, code, out, errOut)
	}
	var v struct {
		Profile  *string
		Settings []struct {
			Key string
			shown
		}
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		c.t.Fatalf("%v: %s", err, out)
	}
	m := map[string]shown{}
	for _, s := range v.Settings {
		m[s.Key] = s.shown
	}
	p := ""
	if v.Profile != nil {
		p = *v.Profile
	}
	return m, p
}

// hubFingerprint is the SHA-256 fingerprint that the hub logs at start.
func hubFingerprint(t *testing.T, hub *contracttest.Hub) string {
	t.Helper()
	m := regexp.MustCompile(`sha256 ([0-9a-f]{64})`).FindStringSubmatch(hub.Stderr())
	if m == nil {
		t.Fatalf("the hub did not log a fingerprint:\n%s", hub.Stderr())
	}
	return m[1]
}

// TestHubAddAndUse: "office hub add" reads the certificate of the hub, shows the fingerprint,
// stores the certificate and writes the profile. Then the profile alone is enough to reach the
// hub: the client has no OFFICE_HUB, OFFICE_CA_CERT or OFFICE_TENANT. The tenant of the profile
// takes part in the calls, and a flag overrides a single value of the profile.
func TestHubAddAndUse(t *testing.T) {
	m := newMachine(t)
	c := m.profileClient(m.user("ada", "Ada", "human"))
	fp := hubFingerprint(t, m.srv)

	out, errOut, code := c.run(nil, "yes\n", "hub", "add", "work", m.srv.URL, "--tenant", "acme", "--default")
	if code != 0 {
		t.Fatalf("hub add: exit %d: %s%s", code, out, errOut)
	}
	if !strings.Contains(errOut, fp) || !strings.Contains(errOut, "Trust this certificate") {
		t.Errorf("hub add must show the fingerprint of the hub log and ask:\n%s", errOut)
	}
	cert := filepath.Join(c.configDir(), "hubs", "work.crt")
	if b, err := os.ReadFile(cert); err != nil || !strings.Contains(string(b), "BEGIN CERTIFICATE") {
		t.Errorf("the certificate is not stored: %v", err)
	}
	file, _ := os.ReadFile(c.userFile())
	for _, want := range []string{"hubs:", "work:", "hub: " + m.srv.URL, "tenant: acme", "ca-cert: hubs/work.crt", "hub-profile: work"} {
		if !strings.Contains(string(file), want) {
			t.Errorf("the user file lacks %q:\n%s", want, file)
		}
	}
	if strings.Contains(string(file), "BEGIN") || strings.Contains(string(file), "PRIVATE") {
		t.Errorf("the user file holds a secret or a certificate:\n%s", file)
	}

	// The profile is the default: whoami reaches the hub, and the tenant is acme.
	if got := strings.TrimSpace(c.ok("whoami")); got != "ada" {
		t.Errorf("whoami through the profile: %q", got)
	}
	c.ok("decision", "record", "Profiles work.")
	if out := c.ok("decision", "list"); !strings.Contains(out, "Profiles work.") {
		t.Errorf("decision list in the tenant of the profile:\n%s", out)
	}
	// A flag overrides one value of the profile: no tenant, no records.
	if _, _, code := c.run(nil, "", "decision", "list", "--tenant", "globex"); code == 0 {
		t.Error("--tenant globex must override the tenant of the profile")
	}
}

// TestHubAddConfirmation: without --fingerprint, "office hub add" writes nothing unless the user
// confirms. With --fingerprint it compares the fingerprint instead of asking, in any notation of
// the hexadecimal digits, and a wrong fingerprint writes nothing.
func TestHubAddConfirmation(t *testing.T) {
	m := newMachine(t)
	fp := hubFingerprint(t, m.srv)
	nothing := func(c *profileClient, what string) {
		t.Helper()
		if _, err := os.Stat(c.userFile()); err == nil {
			t.Errorf("%s: the user file was written", what)
		}
		if _, err := os.Stat(filepath.Join(c.configDir(), "hubs")); err == nil {
			t.Errorf("%s: a certificate was stored", what)
		}
	}
	u := m.user("ada", "Ada", "human")

	for _, answer := range []string{"n\n", "\n", ""} {
		c := m.profileClient(u)
		_, errOut, code := c.run(nil, answer, "hub", "add", "work", m.srv.URL)
		if code != 1 || !strings.Contains(errOut, "not confirmed") {
			t.Errorf("answer %q: exit %d, %s", answer, code, errOut)
		}
		nothing(c, "answer "+strconvQuote(answer))
	}

	c := m.profileClient(u)
	wrong := strings.Repeat("0", 64)
	_, errOut, code := c.run(nil, "", "hub", "add", "work", m.srv.URL, "--fingerprint", wrong)
	if code != 1 || !strings.Contains(errOut, "does not match") || !strings.Contains(errOut, fp) {
		t.Errorf("wrong fingerprint: exit %d, %s", code, errOut)
	}
	nothing(c, "wrong fingerprint")

	// The right fingerprint, with colons and in capitals, needs no answer.
	var colons []string
	for i := 0; i < len(fp); i += 2 {
		colons = append(colons, strings.ToUpper(fp[i:i+2]))
	}
	for i, notation := range []string{fp, strings.Join(colons, ":"), "SHA256:" + strings.Join(colons, ":")} {
		c := m.profileClient(u)
		out, errOut, code := c.run(nil, "", "hub", "add", "work", m.srv.URL, "--fingerprint", notation)
		if code != 0 || !strings.Contains(out, "added the hub profile work") {
			t.Errorf("notation %d: exit %d: %s%s", i, code, out, errOut)
		}
	}

	// A bad name and a bad address are usage errors.
	c = m.profileClient(u)
	for _, args := range [][]string{{"hub", "add", "-x", m.srv.URL}, {"hub", "add", "a.b", m.srv.URL}, {"hub", "add", "work", "http://127.0.0.1:1"},
		{"hub", "add", "work", m.srv.URL + "/path"}, {"hub", "add", "work"}, {"hub", "add", "work", m.srv.URL, "--hub", "https://x"}} {
		if _, errOut, code := c.run(nil, "yes\n", args...); code != 2 {
			t.Errorf("office %v: exit %d, %s", args, code, errOut)
		}
	}
	nothing(c, "usage errors")
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// TestHubAddKeepsTheFile: "office hub add" keeps the comments and the other settings of the user
// file, replaces a profile of the same name, and refuses to change a file that is not valid.
func TestHubAddKeepsTheFile(t *testing.T) {
	m := newMachine(t)
	c := m.profileClient(m.user("ada", "Ada", "human"))
	fp := hubFingerprint(t, m.srv)
	c.writeUser("# my settings\nagent:\n  session: standup  # the daily\nhubs:\n  old:\n    hub: https://old.example.org:8740\n", nil)

	c.ok("hub", "add", "work", m.srv.URL, "--fingerprint", fp, "--key", "ada.pem")
	file, _ := os.ReadFile(c.userFile())
	for _, want := range []string{"# my settings", "session: standup", "# the daily", "old:", "https://old.example.org:8740", "work:", "key: " + filepath.Join(c.dir, "ada.pem")} {
		if !strings.Contains(string(file), want) {
			t.Errorf("the user file lacks %q:\n%s", want, file)
		}
	}
	out := c.ok("hub", "add", "work", m.srv.URL, "--fingerprint", fp, "--tenant", "acme")
	if !strings.Contains(out, "replaced the hub profile work") {
		t.Errorf("a second add of work must replace it: %s", out)
	}
	file, _ = os.ReadFile(c.userFile())
	if strings.Count(string(file), "work:") != 1 || strings.Contains(string(file), "ada.pem") || !strings.Contains(string(file), "tenant: acme") {
		t.Errorf("the profile work was not replaced:\n%s", file)
	}

	c.writeUser("hubs:\n  work:\n    secret: x\n", nil)
	_, errOut, code := c.run(nil, "", "hub", "add", "other", m.srv.URL, "--fingerprint", fp)
	if code != 2 || !strings.Contains(errOut, "unknown key hubs.work.secret") {
		t.Errorf("hub add with an invalid file: exit %d, %s", code, errOut)
	}
	if b, _ := os.ReadFile(c.userFile()); !strings.Contains(string(b), "secret: x") || strings.Contains(string(b), "other") {
		t.Errorf("an invalid file was changed:\n%s", b)
	}
}

// TestHubListAndRemove: "office hub list" shows the profiles of both files and marks the active
// one. "office hub remove" removes a profile of the user file, the default if it is the same
// profile, and its stored certificate, but not the certificate of another profile.
func TestHubListAndRemove(t *testing.T) {
	m := newMachine(t)
	c := m.profileClient(m.user("ada", "Ada", "human"))
	fp := hubFingerprint(t, m.srv)
	c.ok("hub", "add", "one", m.srv.URL, "--fingerprint", fp, "--tenant", "acme", "--default")
	c.ok("hub", "add", "two", m.srv.URL, "--fingerprint", fp)
	c.writeProject("hubs:\n  team:\n    hub: https://team.example.org:8740\n    tenant: ops\n")

	type profile struct {
		Name, Hub, Tenant, CACert string
		Active                    bool
		Files                     []string
	}
	list := func() (string, []profile) {
		out := c.ok("hub", "list", "--json")
		var v struct {
			Active   string
			Profiles []profile
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return v.Active, v.Profiles
	}
	active, ps := list()
	if active != "one" || len(ps) != 3 || ps[0].Name != "one" || !ps[0].Active || ps[0].Tenant != "acme" ||
		ps[1].Name != "team" || ps[1].Tenant != "ops" || ps[1].Files[0] != c.projectFile() || ps[2].Name != "two" || ps[2].Active {
		t.Errorf("hub list: active %q, %+v", active, ps)
	}
	text := c.ok("hub", "list")
	if !regexp.MustCompile(`(?m)^\*\s+one\s`).MatchString(text) || !strings.Contains(text, "team") {
		t.Errorf("hub list in text:\n%s", text)
	}

	out := c.ok("hub", "remove", "one")
	if !strings.Contains(out, "default profile") {
		t.Errorf("hub remove must say that the default is gone: %s", out)
	}
	if _, err := os.Stat(filepath.Join(c.configDir(), "hubs", "one.crt")); err == nil {
		t.Error("the certificate of the removed profile is still there")
	}
	if _, err := os.Stat(filepath.Join(c.configDir(), "hubs", "two.crt")); err != nil {
		t.Errorf("the certificate of another profile is gone: %v", err)
	}
	active, ps = list()
	if active != "" || len(ps) != 2 {
		t.Errorf("after remove: active %q, %+v", active, ps)
	}
	// The file stays valid: office works without a default profile.
	c.ok("config", "show")

	// A profile of the project file is edited there, an unknown one is a usage error.
	for _, name := range []string{"team", "nope"} {
		if _, errOut, code := c.run(nil, "", "hub", "remove", name); code != 2 || !strings.Contains(errOut, "hub profile \""+name+"\"") {
			t.Errorf("remove %s: exit %d, %s", name, code, errOut)
		}
	}
	if _, _, code := c.run(nil, "", "hub", "remove"); code != 2 {
		t.Errorf("remove without a name: exit %d", code)
	}
}

// TestHubPublicCertificate: a hub with a certificate that a trusted authority signed needs no
// stored certificate and no confirmation. The test makes its own authority and hands it to the
// client as the system roots (SSL_CERT_FILE, which Go reads on Linux).
func TestHubPublicCertificate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the test hands the authority to the client with SSL_CERT_FILE, which only Linux reads")
	}
	dir := t.TempDir()
	caPEM, certFile, keyFile := makeCertificate(t, dir, "127.0.0.1")
	hub := contracttest.StartHub(t, nil, "-tls-cert", certFile, "-tls-key", keyFile)
	adminKey := filepath.Join(t.TempDir(), "admin.pem")
	if err := auth.WriteKey(adminKey, hub.AdminKey); err != nil {
		t.Fatal(err)
	}
	c := newProfileClient(t, adminKey, filepath.Join(t.TempDir(), "run"))
	roots := []string{"SSL_CERT_FILE=" + caPEM}

	out, errOut, code := c.run(roots, "", "hub", "add", "public", hub.URL)
	if code != 0 || !strings.Contains(out, "no certificate is stored") {
		t.Fatalf("hub add: exit %d: %s%s", code, out, errOut)
	}
	if strings.Contains(errOut, "Trust this certificate") {
		t.Errorf("a trusted certificate needs no question:\n%s", errOut)
	}
	if _, err := os.Stat(filepath.Join(c.configDir(), "hubs")); err == nil {
		t.Error("a certificate was stored for a hub that the system roots trust")
	}
	if file, _ := os.ReadFile(c.userFile()); strings.Contains(string(file), "ca-cert") {
		t.Errorf("the profile has a certificate:\n%s", file)
	}
	// The profile reaches the hub with the system roots only.
	if _, errOut, code := c.run(roots, "", "whoami", "--hub-profile", "public"); code != 0 {
		t.Errorf("whoami through the profile: exit %d: %s", code, errOut)
	}
	// Without the authority, the same hub is not trusted.
	if _, _, code := c.run(nil, "", "whoami", "--hub-profile", "public"); code == 0 {
		t.Error("the hub must not be trusted without the authority")
	}
}

// makeCertificate writes a certificate for the IP address ip, signed by a new authority, and its
// key into dir. It returns the file of the authority and the files of the certificate and the key.
func makeCertificate(t *testing.T, dir, ip string) (caFile, certFile, keyFile string) {
	t.Helper()
	newKey := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	caKey, leafKey := newKey(), newKey()
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test authority"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "hub"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IPAddresses: []net.IP{net.ParseIP(ip)},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b *pem.Block) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(b), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	return write("ca.pem", &pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		write("hub.crt", &pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		write("hub.key", &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

// TestConnectionKeysInFiles: hub, tenant, ca-cert and key work in the user file and the project
// file with the order defaults, user file, project file, environment, flags. A relative path is
// relative to the root of its file. With only these keys in the user file, the client reaches
// the hub.
func TestConnectionKeysInFiles(t *testing.T) {
	m := newMachine(t)
	ada := m.user("ada", "Ada", "human")
	c := newProfileClient(t, "", m.runtime) // the key comes from the files
	certBytes, err := os.ReadFile(m.srv.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	c.writeUser("hub: "+m.srv.URL+"\ntenant: acme\nca-cert: certs/hub.pem\nkey: "+filepath.Join(c.dir, "no-such-key.pem")+"\n",
		map[string]string{"certs/hub.pem": string(certBytes)})

	s, profile := c.show(nil)
	if profile != "" {
		t.Errorf("active profile %q", profile)
	}
	want := map[string]shown{
		"hub":     {Value: m.srv.URL, Source: "user", From: c.userFile()},
		"tenant":  {Value: "acme", Source: "user", From: c.userFile()},
		"ca-cert": {Value: filepath.Join(c.configDir(), "certs", "hub.pem"), Source: "user", From: c.userFile()},
		"key":     {Value: filepath.Join(c.dir, "no-such-key.pem"), Source: "user", From: c.userFile()},
	}
	for k, w := range want {
		if s[k] != w {
			t.Errorf("%s: got %+v, want %+v", k, s[k], w)
		}
	}

	// The project file overrides single keys; its relative path is relative to the start directory.
	c.writeProject("tenant: globex\nca-cert: here.pem\nkey: " + ada.key + "\n")
	s, _ = c.show(nil)
	if s["tenant"].Source != "project" || s["tenant"].Value != "globex" || s["hub"].Source != "user" ||
		s["ca-cert"].Value != filepath.Join(c.dir, "here.pem") || s["ca-cert"].Source != "project" {
		t.Errorf("project file: %+v", s)
	}
	// The environment overrides both files, a flag overrides the environment.
	s, _ = c.show([]string{"OFFICE_TENANT=from-env", "OFFICE_HUB=https://env.example.org:1"})
	if s["tenant"] != (shown{Value: "from-env", Source: "env", From: "OFFICE_TENANT"}) || s["hub"].From != "OFFICE_HUB" {
		t.Errorf("environment: %+v", s)
	}
	s, _ = c.show([]string{"OFFICE_TENANT=from-env"}, "--tenant", "from-flag", "--hub", "https://flag.example.org:2")
	if s["tenant"] != (shown{Value: "from-flag", Source: "flag", From: "--tenant"}) || s["hub"].Value != "https://flag.example.org:2" || s["hub"].From != "--hub" {
		t.Errorf("flags: %+v", s)
	}
	// An empty tenant of a flag or of the environment means: no tenant.
	if s, _ = c.show([]string{"OFFICE_TENANT="}); s["tenant"].Value != nil {
		t.Errorf("an empty OFFICE_TENANT must remove the tenant: %+v", s["tenant"])
	}
	if s, _ = c.show(nil, "--ca-cert", ""); s["ca-cert"].Value != nil {
		t.Errorf("--ca-cert \"\" must mean the system roots: %+v", s["ca-cert"])
	}

	// With the key of the user, the user file alone reaches the hub.
	c.writeProject("key: " + ada.key + "\n")
	if got := strings.TrimSpace(c.ok("whoami")); got != "ada" {
		t.Errorf("whoami with hub, ca-cert and key from files: %q", got)
	}
	c.ok("decision", "record", "From the file.")
	if out := c.ok("decision", "list"); !strings.Contains(out, "From the file.") {
		t.Errorf("tenant from the file:\n%s", out)
	}
}

// TestHubProfileSelection: the order of the layers for profiles. A layer that selects a profile
// gives its values at that layer, and a single value of the same or a higher layer overrides
// them. The highest layer that selects a profile decides; an empty selection means no profile.
// Profiles of both files are merged key by key, and a profile without a certificate means the
// system roots, even if the folder has a hub.crt.
func TestHubProfileSelection(t *testing.T) {
	c := newProfileClient(t, "", filepath.Join(t.TempDir(), "run"))
	c.writeUser(`hub-profile: a
hubs:
  a:
    hub: https://a.example.org:8740
    tenant: ta
    ca-cert: a.crt
    key: /keys/a.pem
  b:
    hub: https://b.example.org:8740
`, map[string]string{"hub.crt": "x"})

	type want struct{ hub, tenant, cert, key, from string }
	check := func(label string, env []string, args []string, active string, w want) {
		t.Helper()
		s, profile := c.show(env, args...)
		got := want{hub: str(s["hub"].Value), tenant: str(s["tenant"].Value), cert: str(s["ca-cert"].Value), key: str(s["key"].Value), from: s["hub"].Source + " " + s["hub"].Profile}
		if profile != active || got != w {
			t.Errorf("%s: profile %q, got %+v, want %q, %+v", label, profile, got, active, w)
		}
	}
	cfg := c.configDir()
	a := want{"https://a.example.org:8740", "ta", filepath.Join(cfg, "a.crt"), "/keys/a.pem", "profile a"}
	// b has no tenant and no certificate (the folder has a hub.crt, which is not used) and keeps the key.
	b := want{"https://b.example.org:8740", "", "", filepath.Join(cfg, "key.pem"), "profile b"}

	check("default profile of the user file", nil, nil, "a", a)
	check("flag", nil, []string{"--hub-profile", "b"}, "b", b)
	check("variable", []string{"OFFICE_HUB_PROFILE=b"}, nil, "b", b)
	check("flag over variable", []string{"OFFICE_HUB_PROFILE=b"}, []string{"--hub-profile", "a"}, "a", a)
	check("empty variable: no profile", []string{"OFFICE_HUB_PROFILE="}, nil, "", want{coreDefaultHub, "", filepath.Join(cfg, "hub.crt"), filepath.Join(cfg, "key.pem"), "default "})

	c.writeProject("hub-profile: b\n")
	check("project file over user file", nil, nil, "b", b)
	check("variable over project file", []string{"OFFICE_HUB_PROFILE=a"}, nil, "a", a)

	// A single value overrides the profile of the same layer or a lower one, not the other way round.
	check("--tenant over the profile", nil, []string{"--tenant", "tx"}, "b", want{b.hub, "tx", "", b.key, b.from})
	check("OFFICE_HUB over the profile", []string{"OFFICE_HUB=https://x.example.org:1"}, nil, "b", want{"https://x.example.org:1", "", "", b.key, "env "})
	c.writeProject("hub-profile: b\nhub: https://same-layer.example.org:1\n")
	check("hub over the profile of the same file", nil, nil, "b", want{"https://same-layer.example.org:1", "", "", b.key, "project "})
	c.writeProject("hub: https://project.example.org:1\ntenant: tp\n")
	check("hub of the project file over the profile of the user file", nil, nil, "a", want{"https://project.example.org:1", "tp", filepath.Join(cfg, "a.crt"), "/keys/a.pem", "project "})
	c.writeUser("hub: https://user.example.org:1\ntenant: tu\nca-cert: u.crt\n", nil)
	c.writeProject("hub-profile: p\nhubs:\n  p:\n    hub: https://p.example.org:1\n    tenant: tp\n")
	check("profile of the project file over hub of the user file", nil, nil, "p", want{"https://p.example.org:1", "tp", "", filepath.Join(cfg, "key.pem"), "profile p"})

	// A profile in both files is merged key by key; the project file overrides single keys.
	c.writeUser("hubs:\n  m:\n    hub: https://user.example.org:1\n    tenant: tu\n    key: /keys/m.pem\n", nil)
	c.writeProject("hub-profile: m\nhubs:\n  m:\n    tenant: tp\n")
	s, _ := c.show(nil)
	if s["hub"].From != c.userFile() || s["tenant"].From != c.projectFile() || s["tenant"].Value != "tp" ||
		s["hub"].Value != "https://user.example.org:1" || s["key"].Value != "/keys/m.pem" || s["tenant"].Profile != "m" {
		t.Errorf("merged profile: %+v", s)
	}
}

const coreDefaultHub = "https://127.0.0.1:8740"

func str(v any) string { s, _ := v.(string); return s }

// TestHubProfileText: "office config show" names the active profile and the source of each value
// in text.
func TestHubProfileText(t *testing.T) {
	c := newProfileClient(t, filepath.Join(t.TempDir(), "k.pem"), filepath.Join(t.TempDir(), "run"))
	c.writeUser("hubs:\n  work:\n    hub: https://work.example.org:8740\n    tenant: acme\n", nil)
	out := c.ok("config", "show", "--hub-profile", "work")
	for _, s := range []string{"hub profile: work (--hub-profile)", "hub profile work (" + c.userFile() + ")"} {
		if !strings.Contains(out, s) {
			t.Errorf("config show lacks %q:\n%s", s, out)
		}
	}
	if out := c.ok("config", "show"); !strings.Contains(out, "hub profile: none") {
		t.Errorf("config show without a profile:\n%s", out)
	}
}

// TestHubProfileErrors: wrong connection settings are errors (exit 2) that name the file and the
// key, and a profile that is not defined names the profile and the source of the choice.
func TestHubProfileErrors(t *testing.T) {
	c := newProfileClient(t, filepath.Join(t.TempDir(), "k.pem"), filepath.Join(t.TempDir(), "run"))
	for _, tc := range []struct{ file, want string }{
		{"hub: ftp://x\n", `key hub: want the address of a hub`},
		{"hub: hub.example.org\n", `key hub: want the address of a hub`},
		{"hubs:\n  a:\n    hub: nope\n", `key hubs.a.hub: want the address of a hub`},
		{"hubs:\n  a:\n    tenant: t\n", `hub profile "a" has no hub address`},
		{"hubs:\n  a:\n", `hub profile "a" has no hub address`},
		{"hubs:\n  a:\n    hub: https://a:1\n    secret: x\n", `line 4: unknown key hubs.a.secret`},
		{"hubs:\n  a:\n    hub: https://a:1\n    api-key: x\n", `unknown key hubs.a.api-key`},
		{"hubs:\n  a.b:\n    hub: https://a:1\n", `key hubs.a.b: a name has letters`},
		{"hubs: [a]\n", `key hubs: want a mapping of names`},
		{"hubs:\n  a:\n    hub: 5\n", `key hubs.a.hub: want a text`},
		{"hub-profile: a.b\n", `key hub-profile: want the name of a hub profile`},
		{"hub-profile: nope\n", `hub profile "nope" (user file ` + c.userFile() + `) is not defined; there are no hub profiles`},
		{"hub-profile: nope\nhubs:\n  a:\n    hub: https://a:1\n", `is not defined; known: a`},
		{"connection:\n  hub: https://a:1\n", `line 1: unknown key connection`},
	} {
		c.writeUser(tc.file, nil)
		cmds := [][]string{{"config", "show"}, {"whoami"}}
		if !strings.Contains(tc.want, "is not defined") {
			cmds = append(cmds, []string{"hub", "list"}) // hub list does not need the selected profile
		}
		for _, args := range cmds {
			_, errOut, code := c.run(nil, "", args...)
			if code != 2 || !strings.Contains(errOut, tc.want) || (!strings.Contains(tc.want, "is not defined") && !strings.Contains(errOut, c.userFile())) {
				t.Errorf("office %v with %q: exit %d, want %q in %q", args, tc.file, code, tc.want, errOut)
			}
		}
	}
	// An undefined profile from the environment or a flag names that source.
	c.writeUser("hubs:\n  a:\n    hub: https://a:1\n", nil)
	_, errOut, code := c.run([]string{"OFFICE_HUB_PROFILE=x"}, "", "whoami")
	if code != 2 || !strings.Contains(errOut, `hub profile "x" (environment variable OFFICE_HUB_PROFILE) is not defined; known: a`) {
		t.Errorf("undefined profile from the environment: exit %d, %s", code, errOut)
	}
	_, errOut, code = c.run(nil, "", "whoami", "--hub-profile", "x")
	if code != 2 || !strings.Contains(errOut, `hub profile "x" (--hub-profile) is not defined`) {
		t.Errorf("undefined profile from the flag: exit %d, %s", code, errOut)
	}
}

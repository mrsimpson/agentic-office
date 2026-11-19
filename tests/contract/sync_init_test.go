package contract

// Contract tests of "office sync init" (bb-sync through if-cli, dec-artifact-sync): the real
// office binary sets up a directory against a real office-hub, and the plain git command line
// tool works in the result. A temporary HOME and GIT_CONFIG_GLOBAL stand in for the Git
// configuration of the user, so the tests can show that it stays as it was.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const syncTokenTTL = 2 * time.Second

// syncUser is a participant who runs office and git on a computer of its own.
type syncUser struct {
	t      *testing.T
	srv    *contracttest.Hub
	env    []string
	global string // the global Git configuration of this user
	work   string // a directory for the clones of this user
}

const globalGitConfig = "[user]\n\tname = Global Name\n\temail = global@example.org\n"

func newSyncUser(t *testing.T, srv *contracttest.Hub, keyFile string) *syncUser {
	t.Helper()
	home := t.TempDir()
	u := &syncUser{t: t, srv: srv, global: filepath.Join(home, ".gitconfig"), work: t.TempDir()}
	if err := os.WriteFile(u.global, []byte(globalGitConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	u.env = []string{"OFFICE_HUB=" + srv.URL, "OFFICE_CA_CERT=" + srv.CertFile, "OFFICE_TENANT=" + contracttest.DefaultTenant,
		"OFFICE_KEY=" + keyFile, "OFFICE_RUNTIME_DIR=" + filepath.Join(t.TempDir(), "run"), "OFFICE_SESSION=", "OFFICE_JSON=",
		"OFFICE_HUB_PROFILE=", "XDG_CONFIG_HOME=" + t.TempDir(), "HOME=" + home, "GIT_CONFIG_GLOBAL=" + u.global,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false"}
	return u
}

// member registers a participant in the default tenant and returns it with its key file.
func syncMember(t *testing.T, srv *contracttest.Hub, id, name string) *syncUser {
	t.Helper()
	a := srv.Participant(t, id, name, human)
	keyFile := filepath.Join(t.TempDir(), id+".pem")
	if err := auth.WriteKey(keyFile, a.Key); err != nil {
		t.Fatal(err)
	}
	return newSyncUser(t, srv, keyFile)
}

func (u *syncUser) office(dir string, args ...string) (string, string, int) {
	u.t.Helper()
	return contracttest.RunIn(u.t, dir, "office", u.env, nil, args...)
}

// git runs the plain git tool with the environment of this user and returns the output of both
// streams and the exit code.
func (u *syncUser) git(dir string, args ...string) (string, int) {
	u.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// The variable of the host would override the certificate in the configuration of the clone.
	for _, kv := range contracttest.Environ() {
		if !strings.HasPrefix(kv, "GIT_SSL_CAINFO=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, u.env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			u.t.Fatalf("git %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

func (u *syncUser) mustGit(dir string, args ...string) string {
	u.t.Helper()
	out, code := u.git(dir, args...)
	if code != 0 {
		u.t.Fatalf("git %v: exit %d\n%s", args, code, out)
	}
	return out
}

func (u *syncUser) mustInit(dir string, args ...string) {
	u.t.Helper()
	out, errOut, code := u.office(u.work, append([]string{"sync", "init", dir}, args...)...)
	if code != 0 {
		u.t.Fatalf("office sync init %s: exit %d\nstdout: %s\nstderr: %s", dir, code, out, errOut)
	}
}

func startSyncHub(t *testing.T) *contracttest.Hub {
	return contracttest.StartHub(t, nil, "-token-ttl", syncTokenTTL.String())
}

// The clone shows the documents of the tenant. Its own configuration holds the author, the way to
// get a token and the guard; the global configuration is not touched. The token is in no
// configuration value.
func TestSyncInitClonesTheTenant(t *testing.T) {
	srv := startSyncHub(t)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human)
	writeDoc(t, ada, "documents/plan/budget.md", "# Budget\n")
	u := syncMember(t, srv, "grace", "Grace Hopper")
	dir := filepath.Join(u.work, "artifacts")

	out, errOut, code := u.office(u.work, "sync", "init", dir, "--json")
	if code != 0 {
		t.Fatalf("sync init: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	var res struct {
		Directory, Repository, Author string
		Cloned                        bool
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || !res.Cloned || res.Repository != srv.URL+"/git/"+contracttest.DefaultTenant ||
		res.Author != "Grace Hopper <grace@office.invalid>" {
		t.Fatalf("result %q: %+v, %v", out, res, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "documents/plan/budget.md")); err != nil || string(got) != "# Budget\n" {
		t.Fatalf("document in the clone: %q, %v", got, err)
	}
	if b := strings.TrimSpace(u.mustGit(dir, "rev-parse", "--abbrev-ref", "HEAD")); b != "main" {
		t.Errorf("branch %q", b)
	}

	local := func(key string) string {
		out, _ := u.git(dir, "config", "--local", "--get-all", key)
		return strings.TrimSpace(out)
	}
	if got := local("user.name"); got != "Grace Hopper" {
		t.Errorf("local user.name %q", got)
	}
	if got := local("user.email"); got != "grace@office.invalid" {
		t.Errorf("local user.email %q", got)
	}
	if got := local("http.sslCAInfo"); got != srv.CertFile {
		t.Errorf("local http.sslCAInfo %q, want %q", got, srv.CertFile)
	}
	helper := local("credential.helper")
	if !strings.Contains(helper, "sync credential") || strings.Contains(helper, "oat_") {
		t.Errorf("local credential.helper %q", helper)
	}
	if got := local("office.sync"); got != res.Repository {
		t.Errorf("local office.sync %q", got)
	}
	config, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil || strings.Contains(string(config), "oat_") || strings.Contains(string(config), "password") {
		t.Errorf("the configuration of the clone holds a secret: %s (%v)", config, err)
	}
	if st, err := os.Stat(filepath.Join(dir, ".git", "hooks", "pre-commit")); err != nil || st.Mode().Perm()&0o100 == 0 {
		t.Errorf("no executable pre-commit hook: %v", err)
	}
	if got, err := os.ReadFile(u.global); err != nil || string(got) != globalGitConfig {
		t.Errorf("the global Git configuration changed: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(u.env2("HOME"), ".git-credentials")); err == nil {
		t.Errorf("a credential store was written")
	}
}

func (u *syncUser) env2(name string) string {
	for _, e := range u.env {
		if v, ok := strings.CutPrefix(e, name+"="); ok {
			return v
		}
	}
	return ""
}

// A plain git fetch in the clone works with no token in the address or the arguments, also after
// the token of the set-up has expired: the helper gets a fresh one from the hub each time.
func TestSyncInitHelperGivesFreshTokens(t *testing.T) {
	srv := startSyncHub(t)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human)
	writeDoc(t, ada, "documents/first.md", "# First\n")
	u := syncMember(t, srv, "grace", "Grace Hopper")
	dir := filepath.Join(u.work, "artifacts")
	u.mustInit(dir)
	if url := strings.TrimSpace(u.mustGit(dir, "remote", "get-url", "origin")); url != srv.URL+"/git/"+contracttest.DefaultTenant {
		t.Fatalf("remote URL %q holds more than the address", url)
	}

	time.Sleep(syncTokenTTL + 500*time.Millisecond)
	writeDoc(t, ada, "documents/later.md", "# Later\n")
	u.mustGit(dir, "fetch", "-q")
	if got := u.mustGit(dir, "show", "origin/main:documents/later.md"); got != "# Later\n" {
		t.Errorf("fetched document %q", got)
	}
	u.mustGit(dir, "pull", "-q", "--ff-only")
	if _, err := os.Stat(filepath.Join(dir, "documents/later.md")); err != nil {
		t.Errorf("document after the pull: %v", err)
	}

	// The helper hands the token to the hub of the clone and to no other host.
	stdin := func(host string) string { return "protocol=https\nhost=" + host + "\n\n" }
	hub := strings.TrimPrefix(srv.URL, "https://")
	args := []string{"sync", "credential", "get", "--hub", srv.URL, "--tenant", contracttest.DefaultTenant,
		"--ca-cert", srv.CertFile, "--key", u.env2("OFFICE_KEY")}
	out, errOut, code := contracttest.RunIn(t, u.work, "office", u.env, strings.NewReader(stdin(hub)), args...)
	if code != 0 || !strings.Contains(out, "username=") || !strings.Contains(out, "password=") {
		t.Errorf("credential get for the hub: exit %d, %q, %q", code, out, errOut)
	}
	out, errOut, code = contracttest.RunIn(t, u.work, "office", u.env, strings.NewReader(stdin("example.org")), args...)
	if code != 0 || out != "" {
		t.Errorf("credential get for another host: exit %d, %q, %q", code, out, errOut)
	}
}

// A commit in the main directory is refused with a hint to use a worktree. A commit in a linked
// worktree works and has the author of the participant.
func TestSyncInitGuardsTheMainDirectory(t *testing.T) {
	srv := startSyncHub(t)
	writeDoc(t, srv.Participant(t, "ada", "Ada Lovelace", human), "documents/first.md", "# First\n")
	u := syncMember(t, srv, "grace", "Grace Hopper")
	dir := filepath.Join(u.work, "artifacts")
	u.mustInit(dir)

	if err := os.WriteFile(filepath.Join(dir, "main.md"), []byte("# Main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.mustGit(dir, "add", "main.md")
	out, code := u.git(dir, "commit", "-q", "-m", "in the main directory")
	if code == 0 || !strings.Contains(out, "git worktree add") {
		t.Fatalf("commit in the main directory: exit %d, want a refusal with a hint\n%s", code, out)
	}
	// Also from a subdirectory.
	if err := os.MkdirAll(filepath.Join(dir, "documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, code := u.git(filepath.Join(dir, "documents"), "commit", "-q", "-m", "from a subdirectory"); code == 0 || !strings.Contains(out, "git worktree add") {
		t.Errorf("commit from a subdirectory: exit %d\n%s", code, out)
	}
	u.mustGit(dir, "reset", "-q")

	wt := filepath.Join(u.work, "my-change")
	u.mustGit(dir, "worktree", "add", "-q", wt, "-b", "my-change")
	if err := os.WriteFile(filepath.Join(wt, "documents-change.md"), []byte("# Change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.mustGit(wt, "add", "documents-change.md")
	u.mustGit(wt, "commit", "-q", "-m", "in a worktree")
	if got := strings.TrimSpace(u.mustGit(wt, "log", "-1", "--format=%an <%ae>")); got != "Grace Hopper <grace@office.invalid>" {
		t.Errorf("author in the worktree %q", got)
	}
}

// A directory with files that are not part of a clone of this tenant is refused, and nothing in
// it changes.
func TestSyncInitRefusesForeignDirectories(t *testing.T) {
	srv := startSyncHub(t)
	writeDoc(t, srv.Participant(t, "ada", "Ada Lovelace", human), "documents/first.md", "# First\n")
	u := syncMember(t, srv, "grace", "Grace Hopper")

	files := filepath.Join(u.work, "files")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "notes.txt"), []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := u.office(u.work, "sync", "init", files)
	if code == 0 || !strings.Contains(errOut, "not empty") || !strings.Contains(errOut, "notes.txt") {
		t.Errorf("directory with files: exit %d: %s", code, errOut)
	}
	if entries, _ := os.ReadDir(files); len(entries) != 1 {
		t.Errorf("the refused directory changed: %v", entries)
	}

	other := filepath.Join(u.work, "other")
	u.mustGit(u.work, "init", "-q", other)
	u.mustGit(other, "remote", "add", "origin", "https://example.org/some/repository")
	_, errOut, code = u.office(u.work, "sync", "init", other)
	if code == 0 || !strings.Contains(errOut, "example.org/some/repository") {
		t.Errorf("clone of another repository: exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(other, ".git", "hooks", "pre-commit")); err == nil {
		t.Errorf("the refused repository got a hook")
	}

	// The current directory is the default.
	_, errOut, code = u.office(files, "sync", "init")
	if code == 0 || !strings.Contains(errOut, "not empty") {
		t.Errorf("default directory with files: exit %d: %s", code, errOut)
	}
}

// Running init again on a set-up clone is fine: it repairs the configuration and touches no file.
func TestSyncInitTwice(t *testing.T) {
	srv := startSyncHub(t)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human)
	writeDoc(t, ada, "documents/a.md", "# A\n")
	u := syncMember(t, srv, "grace", "Grace Hopper")
	dir := filepath.Join(u.work, "artifacts")
	u.mustInit(dir)

	own := filepath.Join(dir, "my-own-file.txt")
	edited := filepath.Join(dir, "documents/a.md")
	if err := os.WriteFile(own, []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(edited, []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.mustGit(dir, "config", "--local", "user.name", "Somebody Else")
	u.mustGit(dir, "config", "--local", "--add", "credential.helper", "store")
	if err := os.Remove(filepath.Join(dir, ".git", "hooks", "pre-commit")); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := u.office(u.work, "sync", "init", dir, "--json")
	if code != 0 {
		t.Fatalf("second init: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	var res struct{ Cloned bool }
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Cloned {
		t.Errorf("second init result %q: %v", out, err)
	}
	for path, want := range map[string]string{own: "untracked\n", edited: "edited\n"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s after the second init: %q, %v", path, got, err)
		}
	}
	if got := strings.TrimSpace(u.mustGit(dir, "config", "--local", "user.name")); got != "Grace Hopper" {
		t.Errorf("user.name after the second init %q", got)
	}
	if helpers := strings.TrimSpace(u.mustGit(dir, "config", "--local", "--get-all", "credential.helper")); strings.Contains(helpers, "store") || strings.Count(helpers, "sync credential") != 1 {
		t.Errorf("credential helpers after the second init: %q", helpers)
	}
	if out, code := u.git(dir, "commit", "--allow-empty", "-q", "-m", "x"); code == 0 || !strings.Contains(out, "git worktree add") {
		t.Errorf("the guard was not repaired: exit %d\n%s", code, out)
	}
	// The default directory is the current one.
	if _, errOut, code := u.office(dir, "sync", "init"); code != 0 {
		t.Errorf("init in the current directory: exit %d: %s", code, errOut)
	}
}

// An empty directory that exists, and a directory that does not exist yet, both work.
func TestSyncInitEmptyAndNewDirectories(t *testing.T) {
	srv := startSyncHub(t)
	writeDoc(t, srv.Participant(t, "ada", "Ada Lovelace", human), "documents/first.md", "# First\n")
	u := syncMember(t, srv, "grace", "Grace Hopper")
	empty := filepath.Join(u.work, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	u.mustInit(empty)
	u.mustInit(filepath.Join(u.work, "deep", "new"))
	if _, err := os.Stat(filepath.Join(u.work, "deep", "new", ".git")); err != nil {
		t.Errorf("new directory: %v", err)
	}
}

// A participant who is no member of the tenant, and a tenant that does not exist, get a clear
// error, and no directory content is left behind.
func TestSyncInitNeedsMembership(t *testing.T) {
	srv := startSyncHub(t)
	srv.Participant(t, "ada", "Ada Lovelace", human) // creates the tenant
	bob := srv.User(t, "bob", "Bob", human)
	keyFile := filepath.Join(t.TempDir(), "bob.pem")
	if err := auth.WriteKey(keyFile, bob.Key); err != nil {
		t.Fatal(err)
	}
	u := newSyncUser(t, srv, keyFile)
	dir := filepath.Join(u.work, "artifacts")

	_, errOut, code := u.office(u.work, "sync", "init", dir)
	if code == 0 || !strings.Contains(errOut, "not a member") {
		t.Errorf("non-member: exit %d: %s", code, errOut)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the directory of the refused participant holds %v", entries)
	}
	_, errOut, code = u.office(u.work, "sync", "init", dir, "--tenant", "no-such-tenant")
	if code == 0 || !strings.Contains(errOut, "not a member") {
		t.Errorf("unknown tenant: exit %d: %s", code, errOut)
	}
	_, errOut, code = u.office(u.work, "sync", "init", dir, "--tenant", "")
	if code == 0 || !strings.Contains(errOut, "no tenant") {
		t.Errorf("no tenant: exit %d: %s", code, errOut)
	}
}

// "office sync init --help" explains the command.
func TestSyncInitHelp(t *testing.T) {
	out, errOut, code := contracttest.Run(t, "office", helpEnv(t), "sync", "init", "--help")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for _, want := range []string{"office sync init:", "usage: office sync init [<directory>]", "worktree", "Examples:", "office sync init "} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

// A new tenant has no artifact. The clone works and holds no document.
func TestSyncInitEmptyTenant(t *testing.T) {
	srv := startSyncHub(t)
	u := syncMember(t, srv, "grace", "Grace Hopper")
	dir := filepath.Join(u.work, "artifacts")
	if out, errOut, code := u.office(u.work, "sync", "init", dir); code != 0 {
		t.Fatalf("sync init: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if files := strings.TrimSpace(contracttest.Git(t, dir, "ls-files")); files != "" {
		t.Errorf("the clone of a new tenant holds files: %s", files)
	}
}

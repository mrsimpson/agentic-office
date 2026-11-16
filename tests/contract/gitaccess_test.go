package contract

// Contract tests of the Git access (if-git-access, dec-artifact-sync): the real git command line
// tool against the real office-hub. The token is the password of HTTP Basic. A credential helper
// script hands it to Git through the environment, so the token is in no URL and no argument.

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// gitHelper writes a credential helper that answers with the token in OFFICE_TEST_TOKEN. Git
// calls it with "get" and reads username and password from its output.
func gitHelper(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "helper.sh")
	script := "#!/bin/sh\nif [ \"$1\" = get ]; then\n  echo username=office\n  echo \"password=$OFFICE_TEST_TOKEN\"\nfi\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// gitAs runs git in dir with a token as the password. It returns the output and the exit code.
func gitAs(t *testing.T, srv *contracttest.Hub, helper, token, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "credential.helper=!" + helper,
		"-c", "user.name=tester", "-c", "user.email=tester@example.org"}, args...)...)
	cmd.Env = append(os.Environ(), "OFFICE_TEST_TOKEN="+token, "GIT_SSL_CAINFO="+srv.CertFile,
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out.String(), code
}

func gitURL(srv *contracttest.Hub, tenant string) string { return srv.URL + "/git/" + tenant }

func tokenOf(t *testing.T, a *contracttest.Actor) string {
	t.Helper()
	tok, err := a.Tokens.Token(ctx10(t))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func writeDoc(t *testing.T, a *contracttest.Actor, path, content string) {
	t.Helper()
	if _, err := a.Document.CreateDocument(ctx10(t), &documentv1.CreateDocumentRequest{Path: path, Content: content,
		CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
}

// A member clones the artifact repository of the tenant with the token as the password. The clone
// shows a document that the documents interface wrote, and a later fetch shows later changes.
func TestGitAccessCloneAndFetch(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human)
	helper, token := gitHelper(t), tokenOf(t, ada)
	writeDoc(t, ada, "documents/plan/budget.md", "# Budget\n")
	// Other references in the repository are not served: only the branch main exists for a client.
	head := strings.TrimSpace(contracttest.Git(t, srv.ArchiveDir(), "rev-parse", "main"))
	contracttest.Git(t, srv.ArchiveDir(), "update-ref", "refs/heads/other", head)
	contracttest.Git(t, srv.ArchiveDir(), "update-ref", "refs/tags/v1", head)

	work := t.TempDir()
	clone := filepath.Join(work, "clone")
	if out, code := gitAs(t, srv, helper, token, work, "clone", "-q", gitURL(srv, contracttest.DefaultTenant), clone); code != 0 {
		t.Fatalf("clone: %d\n%s", code, out)
	}
	got, err := os.ReadFile(filepath.Join(clone, "documents/plan/budget.md"))
	if err != nil || string(got) != "# Budget\n" {
		t.Fatalf("file in the clone: %q, %v", got, err)
	}
	if branch := strings.TrimSpace(contracttest.Git(t, clone, "rev-parse", "--abbrev-ref", "HEAD")); branch != "main" {
		t.Errorf("branch %q", branch)
	}
	if refs := strings.Fields(contracttest.Git(t, clone, "for-each-ref", "--format=%(refname)")); strings.Join(refs, " ") != "refs/heads/main refs/remotes/origin/HEAD refs/remotes/origin/main" {
		t.Errorf("refs in the clone: %v", refs)
	}

	// A change after the clone shows in the next fetch.
	writeDoc(t, ada, "documents/plan/second.md", "# Second\n")
	if out, code := gitAs(t, srv, helper, token, clone, "pull", "-q", "--ff-only"); code != 0 {
		t.Fatalf("pull: %d\n%s", code, out)
	}
	if got, err := os.ReadFile(filepath.Join(clone, "documents/plan/second.md")); err != nil || string(got) != "# Second\n" {
		t.Fatalf("file after the fetch: %q, %v", got, err)
	}
	// The same history as in the repository of the hub.
	if a, b := contracttest.Git(t, clone, "rev-parse", "HEAD"), contracttest.Git(t, srv.ArchiveDir(), "rev-parse", "main"); a != b {
		t.Errorf("clone at %s, hub at %s", strings.TrimSpace(a), strings.TrimSpace(b))
	}
}

// Without a token, with a garbled one and with an expired one Git gets 401 and a Basic challenge.
func TestGitAccessNeedsToken(t *testing.T) {
	const ttl = time.Second
	srv := contracttest.StartHub(t, nil, "-token-ttl", ttl.String())
	ada := srv.Participant(t, "ada", "Ada", human)
	helper := gitHelper(t)
	expired := tokenOf(t, ada)
	time.Sleep(ttl + 200*time.Millisecond)

	hc := srv.HTTPClient(t)
	probe := func(name, user, pass string, withAuth bool) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, gitURL(srv, contracttest.DefaultTenant)+"/info/refs?service=git-upload-pack", nil)
		if withAuth {
			req.SetBasicAuth(user, pass)
		}
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(res.Header.Get("WWW-Authenticate"), "Basic") {
			t.Errorf("%s: status %d, challenge %q", name, res.StatusCode, res.Header.Get("WWW-Authenticate"))
		}
	}
	probe("no credentials", "", "", false)
	probe("empty password", "office", "", true)
	probe("garbled token", "office", "not-a-token", true)
	probe("expired token", "office", expired, true)

	work := t.TempDir()
	for name, token := range map[string]string{"garbled": "not-a-token", "expired": expired} {
		if out, code := gitAs(t, srv, helper, token, work, "clone", "-q", gitURL(srv, contracttest.DefaultTenant), filepath.Join(work, name)); code == 0 {
			t.Errorf("%s token: clone worked\n%s", name, out)
		}
	}
}

// A member of another tenant, the token of a stage link, a system admin and a tenant that does not
// exist all get the same answer: 404 with the same body.
func TestGitAccessHidesTenants(t *testing.T) {
	m := newTwoTenants(t)
	join2(t, m.ada, "weekly")
	link := stageLink(t, m.ada, "weekly")
	hc := m.srv.HTTPClient(t)
	type answer struct {
		status int
		body   string
	}
	ask := func(tenant, token string) answer {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, gitURL(m.srv, tenant)+"/info/refs?service=git-upload-pack", nil)
		req.SetBasicAuth("office", token)
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var b bytes.Buffer
		_, _ = b.ReadFrom(res.Body)
		return answer{res.StatusCode, b.String()}
	}
	missing := ask("nowhere", tokenOf(t, m.ada))
	if missing.status != http.StatusNotFound {
		t.Fatalf("missing tenant: %+v", missing)
	}
	for name, got := range map[string]answer{
		"member of another tenant": ask("acme", tokenOf(t, m.bob)),
		"stage link token":         ask("acme", stageToken(t, link)),
		"system admin":             ask("acme", tokenOf(t, m.srv.Admin(t))),
		"other tenant of a member": ask("globex", tokenOf(t, m.ada)),
	} {
		if got != missing {
			t.Errorf("%s: %+v, want %+v", name, got, missing)
		}
	}
	// Cy is a member of both tenants and reads both.
	if got := ask("acme", tokenOf(t, m.cyAcme)); got.status != http.StatusOK {
		t.Errorf("member: %+v", got)
	}
	if got := ask("globex", tokenOf(t, m.cyGlobex)); got.status != http.StatusOK {
		t.Errorf("member: %+v", got)
	}
	// The same through Git.
	helper, work := gitHelper(t), t.TempDir()
	if out, code := gitAs(t, m.srv, helper, tokenOf(t, m.bob), work, "clone", "-q", gitURL(m.srv, "acme"), filepath.Join(work, "x")); code == 0 {
		t.Errorf("clone of another tenant worked\n%s", out)
	}
}

// Two tenants never see each other, also through a path in the address. Other addresses than the
// smart protocol of one repository answer 404.
func TestGitAccessSeparatesTenants(t *testing.T) {
	m := newTwoTenants(t)
	writeDoc(t, m.ada, "documents/acme.md", "acme only\n")
	writeDoc(t, m.bob, "documents/globex.md", "globex only\n")
	helper := gitHelper(t)
	for tenant, c := range map[string]struct {
		a         *contracttest.Actor
		have, not string
	}{"acme": {m.ada, "documents/acme.md", "documents/globex.md"}, "globex": {m.bob, "documents/globex.md", "documents/acme.md"}} {
		work := t.TempDir()
		clone := filepath.Join(work, "c")
		if out, code := gitAs(t, m.srv, helper, tokenOf(t, c.a), work, "clone", "-q", gitURL(m.srv, tenant), clone); code != 0 {
			t.Fatalf("%s: clone %d\n%s", tenant, code, out)
		}
		if _, err := os.Stat(filepath.Join(clone, c.have)); err != nil {
			t.Errorf("%s: %v", tenant, err)
		}
		if _, err := os.Stat(filepath.Join(clone, c.not)); err == nil {
			t.Errorf("%s: the clone has %s of the other tenant", tenant, c.not)
		}
	}
	hc := m.srv.HTTPClient(t)
	token := tokenOf(t, m.ada) // ada is no member of globex
	for _, p := range []string{
		"/git/acme/HEAD", "/git/acme/config", "/git/acme/objects/info/packs", "/git/acme/info/refs",
		"/git/acme/info/refs?service=git-foo", "/git/acme/../globex/info/refs?service=git-upload-pack",
		"/git/acme/%2e%2e/globex/info/refs?service=git-upload-pack", "/git/acme/sub/info/refs?service=git-upload-pack",
		"/git/acme/info/refs/x?service=git-upload-pack", "/git/",
	} {
		req, _ := http.NewRequest(http.MethodGet, m.srv.URL+p, nil)
		req.SetBasicAuth("office", token)
		res, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", p, res.StatusCode)
		}
	}
}

// A clone holds no session log. The text of an off-the-record sentence is in no file of the clone
// and in no object of its history, and no other reference than main is served.
func TestGitAccessServesNoSessionLogs(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-archive-interval", "100ms")
	ada := srv.Participant(t, "ada", "Ada", human)
	const secret, public = "Off the record sentence of Ada.", "Public sentence of Ada."
	join2(t, ada, "weekly")
	speak(t, ada, "weekly", public)
	id := speak(t, ada, "weekly", secret)
	if _, err := ada.Commands.MarkOffTheRecord(ctx10(t), &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: "weekly",
		CommandId: coreapi.NewCommandID(), ParticipantId: ada.ParticipantID, UtteranceEventId: id}); err != nil {
		t.Fatal(err)
	}
	recordDecision(t, ada, "A decision.", "weekly")
	if _, err := ada.Commands.EndSession(ctx10(t), &sessioncommandsv1.EndSessionRequest{SessionId: "weekly",
		CommandId: coreapi.NewCommandID(), ParticipantId: ada.ParticipantID}); err != nil {
		t.Fatal(err)
	}
	// The session is in the session-log repository before the clone.
	contracttest.WaitArchived(t, srv.TenantSessionLogsDir(contracttest.DefaultTenant), "weekly", 20*time.Second, (*contracttest.ArchivedSession).Ended)

	work := t.TempDir()
	clone := filepath.Join(work, "c")
	if out, code := gitAs(t, srv, gitHelper(t), tokenOf(t, ada), work, "clone", "-q", gitURL(srv, contracttest.DefaultTenant), clone); code != 0 {
		t.Fatalf("clone: %d\n%s", code, out)
	}
	all := contracttest.Git(t, clone, "log", "--all", "-p", "--format=%B")
	for _, f := range strings.Fields(contracttest.Git(t, clone, "ls-tree", "-r", "--name-only", "HEAD")) {
		if strings.HasPrefix(f, "sessions/") || strings.Contains(f, "log.jsonl") || strings.Contains(f, "tree-head") {
			t.Errorf("session file %s in the clone", f)
		}
	}
	for _, s := range []string{secret, public} {
		if strings.Contains(all, s) {
			t.Errorf("the clone holds the sentence %q", s)
		}
	}
	// Every object of the clone, not only the history of the branches.
	if objects := contracttest.Git(t, clone, "cat-file", "--batch-all-objects", "--batch"); strings.Contains(objects, secret) {
		t.Error("an object of the clone holds the off-the-record sentence")
	}
}

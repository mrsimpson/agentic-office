package contract

// Contract tests of the push to the artifact repository (if-git-access, dec-artifact-sync): the
// real git command line tool against the real office-hub. The rules run inside Git, in a hook
// of the hub, so a refused push shows its reasons as "remote:" lines and changes nothing.
// The tests check what is refused and what is accepted, never how long it takes.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	changesv1 "github.com/mrsimpson/agentic-office/hub/archive/changes/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// pusher is a clone of the artifact repository of a tenant, owned by one participant. Its commits
// have the author that the hub expects, unless a test changes name or email.
type pusher struct {
	t      *testing.T
	srv    *contracttest.Hub
	helper string
	token  string
	dir    string
	name   string
	email  string
}

func newPusher(t *testing.T, srv *contracttest.Hub, a *contracttest.Actor, name, tenant string) *pusher {
	t.Helper()
	p := &pusher{t: t, srv: srv, helper: gitHelper(t), token: tokenOf(t, a), name: name, email: a.ParticipantID + "@office.invalid"}
	work := t.TempDir()
	p.dir = filepath.Join(work, "clone")
	if out, code := gitAs(t, srv, p.helper, p.token, work, "clone", "-q", gitURL(srv, tenant), p.dir); code != 0 {
		t.Fatalf("clone: %d\n%s", code, out)
	}
	return p
}

// git runs git in the clone with the identity of the pusher.
func (p *pusher) git(args ...string) (string, int) {
	p.t.Helper()
	return gitAs(p.t, p.srv, p.helper, p.token, p.dir,
		append([]string{"-c", "user.name=" + p.name, "-c", "user.email=" + p.email}, args...)...)
}

func (p *pusher) mustGit(args ...string) {
	p.t.Helper()
	if out, code := p.git(args...); code != 0 {
		p.t.Fatalf("git %v: %d\n%s", args, code, out)
	}
}

// write writes a file in the clone.
func (p *pusher) write(file, content string) {
	p.t.Helper()
	full := filepath.Join(p.dir, file)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// commit stages everything and commits it.
func (p *pusher) commit(msg string) {
	p.t.Helper()
	p.mustGit("add", "-A")
	p.mustGit("commit", "-q", "-m", msg)
}

// commitFile writes one file and commits it.
func (p *pusher) commitFile(file, content string) {
	p.t.Helper()
	p.write(file, content)
	p.commit("change " + file)
}

// push pushes HEAD to main.
func (p *pusher) push(args ...string) (string, int) {
	p.t.Helper()
	return p.git(append(append([]string{"push"}, args...), "origin", "HEAD:main")...)
}

func (p *pusher) mustPush() {
	p.t.Helper()
	if out, code := p.push(); code != 0 {
		p.t.Fatalf("push: %d\n%s", code, out)
	}
}

func mainOf(t *testing.T, srv *contracttest.Hub, tenant string) string {
	t.Helper()
	// An empty repository has no main yet: the answer is then empty.
	out, _ := exec.Command("git", "-C", srv.TenantRepoDir(tenant), "rev-parse", "-q", "--verify", "refs/heads/main").Output()
	return strings.TrimSpace(string(out))
}

// refused checks that a push was refused, with the text in the output, and that main did not move.
func refused(t *testing.T, srv *contracttest.Hub, tenant, before, out string, code int, want ...string) {
	t.Helper()
	if code == 0 {
		t.Fatalf("the push worked\n%s", out)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("the output does not say %q\n%s", w, out)
		}
	}
	if after := mainOf(t, srv, tenant); after != before {
		t.Errorf("main moved from %s to %s", before, after)
	}
	if refs := strings.Fields(contracttest.Git(t, srv.TenantRepoDir(tenant), "for-each-ref", "--format=%(refname)")); strings.Join(refs, " ") != "refs/heads/main" {
		t.Errorf("references in the hub repository: %v", refs)
	}
}

// Valid pushes are accepted: a new Markdown file, a new YAML file with a valid schema, a change and
// a removal. Main moves, a second clone fetches the result, and the followers of the changes
// interface get one event for each push.
func TestGitPushAccepted(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human)
	cy := srv.Participant(t, "cy", "Cy", human)
	f := followChanges(t, cy.Clients)
	p := newPusher(t, srv, ada, "Ada Lovelace", contracttest.DefaultTenant)

	expect := func(kind changesv1.ChangeKind, path string) {
		t.Helper()
		ev := f.next(t)
		got := ev.GetChanges().GetChanges()
		if len(got) != 1 || got[0].GetKind() != kind || got[0].GetPath() != path {
			t.Fatalf("event %v, want %s %s", described(ev), kind, path)
		}
	}

	p.commitFile("docs/plan.md", "# Plan\n")
	p.mustPush()
	expect(changesv1.ChangeKind_CHANGE_KIND_CREATED, "docs/plan.md")

	p.commitFile("notes/000003.yaml", noteFile)
	p.mustPush()
	expect(changesv1.ChangeKind_CHANGE_KIND_CREATED, "notes/000003.yaml")

	p.commitFile("docs/plan.md", "# Plan\n\nMore.\n")
	p.mustPush()
	expect(changesv1.ChangeKind_CHANGE_KIND_CHANGED, "docs/plan.md")

	p.mustGit("rm", "-q", "docs/plan.md")
	p.mustGit("commit", "-q", "-m", "remove the plan")
	p.mustPush()
	expect(changesv1.ChangeKind_CHANGE_KIND_REMOVED, "docs/plan.md")

	if head := strings.TrimSpace(contracttest.Git(t, p.dir, "rev-parse", "HEAD")); mainOf(t, srv, contracttest.DefaultTenant) != head {
		t.Errorf("main of the hub is not the pushed commit %s", head)
	}
	// The author of the pushed commit is the participant.
	if a := strings.TrimSpace(contracttest.Git(t, srv.ArchiveDir(), "log", "-1", "--format=%an <%ae>", "main")); a != "Ada Lovelace <"+ada.ParticipantID+"@office.invalid>" {
		t.Errorf("author %q", a)
	}
	// A second clone has the result, and the hub still reads and writes through its interfaces.
	q := newPusher(t, srv, cy, "Cy", contracttest.DefaultTenant)
	if _, err := os.Stat(filepath.Join(q.dir, "notes/000003.yaml")); err != nil {
		t.Errorf("second clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(q.dir, "docs/plan.md")); err == nil {
		t.Error("second clone has the removed file")
	}
	writeDoc(t, ada, "documents/after.md", "after\n")
	expect(changesv1.ChangeKind_CHANGE_KIND_CREATED, "documents/after.md")
}

// The first push to an empty repository creates main.
func TestGitPushFirstCommit(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	f := followChanges(t, ada.Clients)
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
	if mainOf(t, srv, contracttest.DefaultTenant) != "" {
		t.Fatal("the new repository has a main")
	}
	p.mustGit("checkout", "-q", "-b", "main")
	p.commitFile("docs/first.md", "first\n")
	p.mustPush()
	if mainOf(t, srv, contracttest.DefaultTenant) == "" {
		t.Fatal("main was not created")
	}
	if got := f.next(t).GetChanges().GetChanges(); len(got) != 1 || got[0].GetPath() != "docs/first.md" {
		t.Errorf("event %v", got)
	}
}

// A push on an old base is refused, and the answer tells the participant to fetch and merge. After
// that the push works.
func TestGitPushOldBase(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	cy := srv.Participant(t, "cy", "Cy", human)
	writeDoc(t, ada, "documents/seed.md", "seed\n")
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
	q := newPusher(t, srv, cy, "Cy", contracttest.DefaultTenant)

	q.commitFile("docs/cy.md", "cy\n")
	q.mustPush()
	before := mainOf(t, srv, contracttest.DefaultTenant)

	p.commitFile("docs/ada.md", "ada\n")
	out, code := p.push()
	refused(t, srv, contracttest.DefaultTenant, before, out, code, "fetch first")

	// The same with the client forced to send it: the hub says what to do.
	out, code = p.push("--force")
	refused(t, srv, contracttest.DefaultTenant, before, out, code, "remote:", "git fetch")

	p.mustGit("pull", "-q", "--no-rebase", "--no-edit")
	p.mustPush()
	files := contracttest.Git(t, srv.ArchiveDir(), "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "docs/ada.md") || !strings.Contains(files, "docs/cy.md") {
		t.Errorf("files in main:\n%s", files)
	}
}

// Every new commit must have the participant of the token as its author. One commit with another
// name or address refuses the whole push.
func TestGitPushAuthor(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human)
	writeDoc(t, ada, "documents/seed.md", "seed\n")
	before := mainOf(t, srv, contracttest.DefaultTenant)
	for name, ident := range map[string][2]string{
		"wrong name":    {"Mallory", ada.ParticipantID + "@office.invalid"},
		"wrong address": {"Ada Lovelace", ada.ParticipantID + "@example.org"},
		"other user":    {"Ada Lovelace", "cy@office.invalid"},
		"real mail":     {"tester", "tester@example.org"},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPusher(t, srv, ada, "Ada Lovelace", contracttest.DefaultTenant)
			p.commitFile("docs/good.md", "good\n")
			p.name, p.email = ident[0], ident[1]
			p.commitFile("docs/bad.md", "bad\n")
			p.name, p.email = "Ada Lovelace", ada.ParticipantID+"@office.invalid"
			p.commitFile("docs/last.md", "last\n")
			out, code := p.push()
			refused(t, srv, contracttest.DefaultTenant, before, out, code, "remote:", "author")
		})
	}
	// The same commit with the right author is accepted.
	p := newPusher(t, srv, ada, "Ada Lovelace", contracttest.DefaultTenant)
	p.commitFile("docs/good.md", "good\n")
	p.mustPush()
}

// Only main can change, and only forward: no other branch, no tag, no deletion, no forced push.
func TestGitPushOnlyMainForward(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	writeDoc(t, ada, "documents/a.md", "a\n")
	writeDoc(t, ada, "documents/b.md", "b\n")
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
	p.commitFile("docs/new.md", "new\n")
	writeDoc(t, ada, "documents/seed.md", "seed\n")
	before := mainOf(t, srv, contracttest.DefaultTenant)
	p.mustGit("tag", "v1")

	cases := map[string][]string{
		"other branch":  {"push", "origin", "HEAD:refs/heads/other"},
		"tag":           {"push", "origin", "v1"},
		"delete main":   {"push", "origin", ":main"},
		"hidden ref":    {"push", "origin", "HEAD:refs/notes/x"},
		"all branches":  {"push", "origin", "--all"},
		"main and tag":  {"push", "origin", "HEAD:main", "v1"},
		"force rewrite": nil,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if args == nil {
				// Rewrite the history: drop the newest commit of the hub, add another one.
				p.mustGit("checkout", "-q", "-B", "rewrite", "HEAD~2")
				p.commitFile("docs/rewrite.md", "rewrite\n")
				args = []string{"push", "--force", "origin", "HEAD:main"}
			}
			out, code := p.git(args...)
			refused(t, srv, contracttest.DefaultTenant, before, out, code)
		})
	}
}

// Only Markdown and YAML files change. A removal of such a file is allowed; all other file types, links and
// hidden path elements are refused.
func TestGitPushFileTypesAndPaths(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	writeDoc(t, ada, "documents/seed.md", "seed\n")
	before := mainOf(t, srv, contracttest.DefaultTenant)
	for name, c := range map[string]struct{ file, content, want string }{
		"html":          {"docs/deck.html", "<html></html>", "only Markdown"},
		"png":           {"docs/pic.png", "\x89PNG\r\n", "only Markdown"},
		"no extension":  {"docs/README", "text", "only Markdown"},
		"upper case":    {"docs/plan.MD", "text", "only Markdown"},
		"hidden file":   {".hidden.md", "text", "hidden"},
		"hidden folder": {".git-like/plan.md", "text", "hidden"},
		"nested hidden": {"docs/.secret/plan.md", "text", "hidden"},
		"hook":          {".githooks/pre-commit.yaml", "a: b\n", "hidden"},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
			p.commitFile("docs/fine.md", "fine\n")
			p.commitFile(c.file, c.content)
			out, code := p.push()
			refused(t, srv, contracttest.DefaultTenant, before, out, code, "remote:", c.want)
		})
	}
	t.Run("symbolic link", func(t *testing.T) {
		p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
		if err := os.Symlink("/etc/passwd", filepath.Join(p.dir, "link.md")); err != nil {
			t.Fatal(err)
		}
		p.commit("link")
		out, code := p.push()
		refused(t, srv, contracttest.DefaultTenant, before, out, code, "remote:", "not a regular file")
	})
}

// A file that fails the check of its schema refuses the push with the reasons. Only the final
// state of the push counts: a file that is broken in one commit and fixed in the next is fine.
func TestGitPushInvalidFiles(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	writeDoc(t, ada, "documents/seed.md", "seed\n")
	before := mainOf(t, srv, contracttest.DefaultTenant)
	for name, c := range map[string]struct{ file, content, want string }{
		"broken yaml":    {"notes/x.yaml", "a: [unclosed\n", "notes/x.yaml"},
		"wrong schema":   {"notes/000003.yaml", strings.Replace(noteFile, "text: Call the bank.\n", "", 1), "text is missing"},
		"unknown schema": {"docs/x.yaml", "schema: office.nothing.v1\n", "not known"},
		"front matter":   {"docs/x.md", "---\nschema: office.nothing.v1\n---\n# x\n", "not known"},
		"not utf-8":      {"docs/x.md", "\xff\xfe\x00bad", "UTF-8"},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
			p.commitFile(c.file, c.content)
			out, code := p.push()
			refused(t, srv, contracttest.DefaultTenant, before, out, code, "remote:", c.want)
		})
	}
	t.Run("broken and fixed in one push", func(t *testing.T) {
		p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
		p.commitFile("notes/000004.yaml", "a: [unclosed\n")
		p.commitFile("notes/000004.yaml", strings.Replace(noteFile, "id: 3", "id: 4", 1))
		p.mustPush()
	})
}

func randomText(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n/2)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// A push that is too large is refused with a clear message: the pack, one file, and the number of
// files each have a limit that the hub takes as an option.
func TestGitPushLimits(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-git-max-push-mib", "1", "-git-max-file-kib", "64", "-git-max-files", "3")
	ada := srv.Participant(t, "ada", "Ada", human)
	writeDoc(t, ada, "documents/seed.md", "seed\n")
	before := mainOf(t, srv, contracttest.DefaultTenant)

	t.Run("pack", func(t *testing.T) {
		p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
		for i := range 6 {
			p.write(fmt.Sprintf("docs/big%d.md", i), randomText(t, 600<<10))
		}
		p.commit("big")
		out, code := p.push()
		refused(t, srv, contracttest.DefaultTenant, before, out, code, "remote:", "maximum allowed size")
	})
	t.Run("file", func(t *testing.T) {
		p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
		p.commitFile("docs/large.md", randomText(t, 100<<10))
		out, code := p.push()
		refused(t, srv, contracttest.DefaultTenant, before, out, code, "remote:", "docs/large.md", "at most 65536 bytes")
	})
	t.Run("files", func(t *testing.T) {
		p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
		for i := range 4 {
			p.write(fmt.Sprintf("docs/f%d.md", i), "x\n")
		}
		p.commit("many")
		out, code := p.push()
		refused(t, srv, contracttest.DefaultTenant, before, out, code, "remote:", "at most 3 files")
	})
	t.Run("within the limits", func(t *testing.T) {
		p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)
		p.write("docs/a.md", randomText(t, 50<<10))
		p.write("docs/b.md", "b\n")
		p.write("docs/c.md", "c\n")
		p.commit("fine")
		p.mustPush()
	})
}

// A push while the hub itself commits through its interfaces loses no change on either side.
func TestGitPushWhileHubCommits(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	cy := srv.Participant(t, "cy", "Cy", human)
	writeDoc(t, ada, "documents/first.md", "first\n")
	p := newPusher(t, srv, cy, "Cy", contracttest.DefaultTenant)
	const n = 12

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range n {
			if _, err := ada.Document.CreateDocument(ctx10(t), &documentv1.CreateDocumentRequest{Path: fmt.Sprintf("documents/hub%02d.md", i), Content: "hub\n", CommandId: coreapi.NewCommandID()}); err != nil {
				t.Errorf("create: %v", err)
				return
			}
		}
	}()
	for i := range n {
		p.commitFile(fmt.Sprintf("docs/push%02d.md", i), "push\n")
		// A push on an old base is refused by Git: fetch, merge and push again.
		for {
			out, code := p.push()
			if code == 0 {
				break
			}
			if !strings.Contains(out, "fetch first") && !strings.Contains(out, "git fetch") && !strings.Contains(out, "non-fast-forward") {
				t.Fatalf("push %d refused for another reason\n%s", i, out)
			}
			p.mustGit("pull", "-q", "--no-rebase", "--no-edit")
		}
	}
	wg.Wait()
	// Settle: the hub side is done, so one more push after a merge ends the race.
	p.mustGit("pull", "-q", "--no-rebase", "--no-edit")
	files := contracttest.Git(t, srv.ArchiveDir(), "ls-tree", "-r", "--name-only", "main")
	for i := range n {
		for _, f := range []string{fmt.Sprintf("documents/hub%02d.md", i), fmt.Sprintf("docs/push%02d.md", i)} {
			if !strings.Contains(files, f+"\n") {
				t.Errorf("%s is lost", f)
			}
		}
	}
	if out := contracttest.Git(t, srv.ArchiveDir(), "fsck", "--strict"); strings.Contains(out, "error") {
		t.Errorf("fsck: %s", out)
	}
}

// A token that is not a member of the tenant cannot push, and two tenants never mix.
func TestGitPushMembersAndTenants(t *testing.T) {
	m := newTwoTenants(t)
	writeDoc(t, m.ada, "documents/seed.md", "seed\n")
	writeDoc(t, m.bob, "documents/seed.md", "seed\n")
	acmeBefore, globexBefore := mainOf(t, m.srv, "acme"), mainOf(t, m.srv, "globex")

	p := newPusher(t, m.srv, m.ada, "Ada", "acme")
	p.commitFile("docs/acme.md", "acme\n")

	// Bob is a member of globex, not of acme.
	p.token = tokenOf(t, m.bob)
	out, code := p.push()
	refused(t, m.srv, "acme", acmeBefore, out, code)

	// Ada is a member of acme: her push to the address of globex fails, and globex does not change.
	p.token = tokenOf(t, m.ada)
	p.mustGit("remote", "add", "globex", gitURL(m.srv, "globex"))
	out, code = p.git("push", "globex", "HEAD:main")
	refused(t, m.srv, "globex", globexBefore, out, code)

	// The system admin is no member either.
	p.token = tokenOf(t, m.srv.Admin(t))
	out, code = p.push()
	refused(t, m.srv, "acme", acmeBefore, out, code)

	p.token = tokenOf(t, m.ada)
	p.mustPush()
	if got := contracttest.Git(t, m.srv.TenantRepoDir("globex"), "ls-tree", "-r", "--name-only", "main"); strings.Contains(got, "docs/acme.md") {
		t.Errorf("a file of acme is in globex:\n%s", got)
	}
	if mainOf(t, m.srv, "globex") != globexBefore {
		t.Error("main of globex moved")
	}
	// Cy is a member of both tenants and pushes to each; the files stay apart.
	cyA := newPusher(t, m.srv, m.cyAcme, "Cy", "acme")
	cyA.commitFile("docs/cy-acme.md", "x\n")
	cyA.mustPush()
	cyG := newPusher(t, m.srv, m.cyGlobex, "Cy", "globex")
	cyG.commitFile("docs/cy-globex.md", "y\n")
	cyG.mustPush()
	if got := contracttest.Git(t, m.srv.TenantRepoDir("acme"), "ls-tree", "-r", "--name-only", "main"); strings.Contains(got, "cy-globex") {
		t.Errorf("a file of globex is in acme:\n%s", got)
	}
	if got := contracttest.Git(t, m.srv.TenantRepoDir("globex"), "ls-tree", "-r", "--name-only", "main"); strings.Contains(got, "cy-acme") || strings.Contains(got, "docs/acme.md") {
		t.Errorf("a file of acme is in globex:\n%s", got)
	}
}

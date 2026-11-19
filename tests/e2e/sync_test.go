//go:build e2e

package e2e

// End-to-end tests of the whole artifact sync path (dec-artifact-sync, epic #146): the real
// office-hub, the real office binary with "office sync init", "office sync" and
// "office sync follow", and the plain git tool, with several participants and two tenants.
//
// The contract tests (tests/contract) already cover each step alone: "sync init" (clone, author,
// guard, foreign directories, empty tenant), "sync" and "sync follow" with one participant and one
// change at a time (by interface, by push, from a worktree, catch-up, hub restart, expired tokens,
// path prefixes, rewritten history), the push rules (what is refused and accepted), the Git access
// of two tenants, and the location of the session logs. These tests add what no single contract
// test combines:
//
//   - two participants (a human and an agent) with two clones and one hub, both with a running
//     "office sync follow", the first commit of the tenant, and the guard in the main directory
//   - a change by interface, a push from a worktree, and an agent that pushes many files at once
//   - two writers at the same time: a normal Git conflict that stays in the worktree
//   - a second tenant with its own participants and clones, and a participant of both tenants:
//     no file, event or error text crosses over, also not through "office sync follow"
//   - sessions with sentences off the record, over a hub restart and after the session ended
//
// Every wait is a wait for a condition with a generous deadline. No test measures time.

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	tenantv1 "github.com/mrsimpson/agentic-office/hub/tenant/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// syncDeadline is the longest a test waits for a condition. It is generous on purpose: a test
// fails when a condition never becomes true, never because it took long.
const syncDeadline = 120 * time.Second

func syncCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// waitCond polls until cond is true.
func waitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(syncDeadline)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("condition never became true: %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func readFileOr(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

// waitContent waits until the file has exactly the content.
func waitContent(t *testing.T, path, want string) {
	t.Helper()
	waitCond(t, path+" has the expected content", func() bool { return readFileOr(path) == want })
}

// world is one hub with two tenants: acme (ada the human, robo the agent) and globex (bob the
// human, bot the agent). Cy, a human, is a member of both.
type world struct {
	t     *testing.T
	srv   *contracttest.Hub
	users map[string]*contracttest.Actor // actors that name no tenant
	keys  map[string]string              // key files
	names map[string]string
}

func newWorld(t *testing.T, hubArgs ...string) *world {
	t.Helper()
	w := &world{t: t, srv: contracttest.StartHub(t, nil, hubArgs...), users: map[string]*contracttest.Actor{},
		keys: map[string]string{}, names: map[string]string{"ada": "Ada Lovelace", "robo": "Robo Agent", "bob": "Bob Builder",
			"bot": "Bot Agent", "cy": "Cy Fer"}}
	kinds := map[string]identityv1.ActorKind{"ada": identityv1.ActorKind_ACTOR_KIND_HUMAN, "robo": identityv1.ActorKind_ACTOR_KIND_AGENT,
		"bob": identityv1.ActorKind_ACTOR_KIND_HUMAN, "bot": identityv1.ActorKind_ACTOR_KIND_AGENT, "cy": identityv1.ActorKind_ACTOR_KIND_HUMAN}
	for id, kind := range kinds {
		a := w.srv.User(t, id, w.names[id], kind)
		w.users[id] = a
		w.keys[id] = filepath.Join(t.TempDir(), id+".pem")
		if err := auth.WriteKey(w.keys[id], a.Key); err != nil {
			t.Fatal(err)
		}
	}
	admin := w.srv.Admin(t)
	for tenant, first := range map[string]string{"acme": "ada", "globex": "bob"} {
		if _, err := admin.Tenant.CreateTenant(syncCtx(t), &tenantv1.CreateTenantRequest{CommandId: coreapi.NewCommandID(),
			TenantId: tenant, DisplayName: tenant, FirstAdminUserId: first}); err != nil {
			t.Fatalf("create tenant %s: %v", tenant, err)
		}
	}
	for tenant, members := range map[string][]string{"acme": {"robo", "cy"}, "globex": {"bot", "cy"}} {
		for _, id := range members {
			if _, err := admin.User.SetMembership(syncCtx(t), &userv1.SetMembershipRequest{CommandId: coreapi.NewCommandID(),
				TenantId: tenant, UserId: id, Role: userv1.Role_ROLE_MEMBER}); err != nil {
				t.Fatalf("add %s to %s: %v", id, tenant, err)
			}
		}
	}
	return w
}

// actor returns the clients of a user in a tenant, for the hub as it runs now.
func (w *world) actor(id, tenant string) *contracttest.Actor {
	w.t.Helper()
	return w.srv.WithTenant(w.t, w.users[id].Key, tenant)
}

func (w *world) mainOf(tenant string) string {
	w.t.Helper()
	return strings.TrimSpace(contracttest.Git(w.t, w.srv.TenantRepoDir(tenant), "rev-parse", "main"))
}

// person is a participant on a computer of its own: the office tool and git with a Git
// configuration of their own, so the tests show that the global one stays as it was.
type person struct {
	t      *testing.T
	w      *world
	id     string
	tenant string
	env    []string
	work   string
}

func (w *world) person(id, tenant string) *person {
	w.t.Helper()
	home := w.t.TempDir()
	global := filepath.Join(home, ".gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tname = Global Name\n\temail = global@example.org\n"), 0o600); err != nil {
		w.t.Fatal(err)
	}
	return &person{t: w.t, w: w, id: id, tenant: tenant, work: w.t.TempDir(), env: []string{"OFFICE_HUB=" + w.srv.URL,
		"OFFICE_CA_CERT=" + w.srv.CertFile, "OFFICE_TENANT=" + tenant, "OFFICE_KEY=" + w.keys[id],
		"OFFICE_RUNTIME_DIR=" + filepath.Join(w.t.TempDir(), "run"), "OFFICE_SESSION=", "OFFICE_JSON=", "OFFICE_HUB_PROFILE=",
		"XDG_CONFIG_HOME=" + w.t.TempDir(), "HOME=" + home, "GIT_CONFIG_GLOBAL=" + global, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false"}}
}

func (p *person) office(dir string, args ...string) (string, string, int) {
	p.t.Helper()
	return contracttest.RunIn(p.t, dir, "office", p.env, nil, args...)
}

// git runs the plain git tool with the environment of this participant. It returns both streams
// and the exit code.
func (p *person) git(dir string, args ...string) (string, int) {
	p.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	for _, kv := range contracttest.Environ() {
		if !strings.HasPrefix(kv, "GIT_SSL_CAINFO=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, p.env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			p.t.Fatalf("git %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

func (p *person) mustGit(dir string, args ...string) string {
	p.t.Helper()
	out, code := p.git(dir, args...)
	if code != 0 {
		p.t.Fatalf("%s: git %v in %s: exit %d\n%s", p.id, args, dir, code, out)
	}
	return out
}

// clone sets up a directory with "office sync init".
func (p *person) clone(name string) string {
	p.t.Helper()
	dir := filepath.Join(p.work, name)
	out, errOut, code := p.office(p.work, "sync", "init", dir)
	if code != 0 {
		p.t.Fatalf("%s: office sync init: exit %d\nstdout: %s\nstderr: %s", p.id, code, out, errOut)
	}
	return dir
}

// worktree makes a linked worktree of a clone, on a branch of its own.
func (p *person) worktree(dir, name string) string {
	p.t.Helper()
	wt := filepath.Join(p.work, name)
	p.mustGit(dir, "worktree", "add", "-q", wt, "-b", name)
	return wt
}

// commitAll stages everything in a worktree and commits it.
func (p *person) commitAll(wt, message string) {
	p.t.Helper()
	p.mustGit(wt, "add", "-A")
	p.mustGit(wt, "commit", "-q", "-m", message)
}

func (p *person) pushMain(wt string) {
	p.t.Helper()
	p.mustGit(wt, "push", "-q", "origin", "HEAD:main")
}

func (p *person) head(dir string) string {
	p.t.Helper()
	return strings.TrimSpace(p.mustGit(dir, "rev-parse", "HEAD"))
}

func put(t *testing.T, dir, file, content string) {
	t.Helper()
	full := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// createDoc and changeDoc write a document through the documents interface.
func createDoc(t *testing.T, a *contracttest.Actor, path, content string) {
	t.Helper()
	if _, err := a.Document.CreateDocument(syncCtx(t), &documentv1.CreateDocumentRequest{Path: path, Content: content,
		CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatalf("create %s as %s: %v", path, a.ParticipantID, err)
	}
}

func changeDoc(t *testing.T, a *contracttest.Actor, path, content string) {
	t.Helper()
	f, err := a.Archive.ReadFile(syncCtx(t), &archivev1.ReadFileRequest{Path: path})
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if _, err := a.Document.ChangeDocument(syncCtx(t), &documentv1.ChangeDocumentRequest{Path: path, Content: content,
		BaseVersion: f.GetFile().GetVersion(), CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatalf("change %s as %s: %v", path, a.ParticipantID, err)
	}
}

// follower is a running "office sync follow". It keeps everything the tool writes.
type follower struct {
	t    *testing.T
	p    *contracttest.Process
	mu   sync.Mutex
	out  bytes.Buffer
	done chan struct{}
}

func (p *person) follow(dir string) *follower {
	p.t.Helper()
	f := &follower{t: p.t, done: make(chan struct{})}
	f.p = contracttest.StartIn(p.t, p.work, "office", p.env, "sync", "follow", dir)
	go func() {
		defer close(f.done)
		buf := make([]byte, 4096)
		for {
			n, err := f.p.Stdout.Read(buf)
			f.mu.Lock()
			f.out.Write(buf[:n])
			f.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return f
}

// output is everything the follower wrote to its two streams so far.
func (f *follower) output() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.out.String() + f.p.Stderr()
}

// stop ends the follower like Ctrl-C does; it must end with success.
func (f *follower) stop() {
	f.t.Helper()
	code, err := f.p.Stop(30 * time.Second)
	if err != nil || code != 0 {
		f.t.Errorf("stop the follower: exit %d, %v\n%s", code, err, f.output())
	}
	<-f.done
}

// hubFiles lists the Markdown and YAML files of main of the hub, with their content.
func hubFiles(t *testing.T, w *world, tenant string) map[string]string {
	t.Helper()
	repo := w.srv.TenantRepoDir(tenant)
	files := map[string]string{}
	for _, f := range strings.Fields(contracttest.Git(t, repo, "ls-tree", "-r", "--name-only", "main")) {
		files[f] = contracttest.Git(t, repo, "show", "main:"+f)
	}
	return files
}

// workFiles lists the files of a directory, except the Git directory.
func workFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// sameFiles reports the first difference between two sets of files, or "".
func sameFiles(a, b map[string]string) string {
	for name, content := range a {
		if other, ok := b[name]; !ok {
			return name + " is missing in the second"
		} else if other != content {
			return name + " differs"
		}
	}
	for name := range b {
		if _, ok := a[name]; !ok {
			return name + " is missing in the first"
		}
	}
	return ""
}

// waitSameAsHub waits until the directory has exactly the files of main of the hub, and its main
// is the main of the hub.
func waitSameAsHub(t *testing.T, w *world, p *person, tenant, dir string) {
	t.Helper()
	waitCond(t, fmt.Sprintf("%s: %s is the same as main of the hub", p.id, dir), func() bool {
		return p.head(dir) == w.mainOf(tenant) && sameFiles(workFiles(t, dir), hubFiles(t, w, tenant)) == ""
	})
}

// noteYAML is a valid note file. The hub checks the schema of YAML files that are pushed.
func noteYAML(n int, author, kind string) string {
	return fmt.Sprintf("schema: office.note.v1\nid: %d\ntext: Note number %d of the agent.\nauthor_participant_id: %s\n"+
		"author_kind: %s\ntime: \"2026-11-16T10:00:00Z\"\ncommand_id: c-%d\n", n, n, author, kind, n)
}

// Two participants, a human and an agent, each with a clone and a running follower. The first
// commit of the tenant is the only commit of a fresh clone, and the guard refuses a commit in the
// main directory. Changes by interface, by a push from a worktree and by an agent with many
// files all end in both clones and at the hub.
func TestSyncTwoParticipantsWholePath(t *testing.T) {
	w := newWorld(t)
	ada, robo := w.person("ada", "acme"), w.person("robo", "acme")
	adaDir, roboDir := ada.clone("artifacts"), robo.clone("artifacts")

	// The first commit of the tenant is the only commit of a fresh clone: it has no file, it is
	// the same commit in both clones, and it is main of the hub.
	first := w.mainOf("acme")
	for _, c := range []struct {
		p   *person
		dir string
	}{{ada, adaDir}, {robo, roboDir}} {
		if n := strings.TrimSpace(c.p.mustGit(c.dir, "rev-list", "--count", "--all")); n != "1" {
			t.Errorf("%s: a fresh clone has %s commits, want 1", c.p.id, n)
		}
		if got := c.p.head(c.dir); got != first {
			t.Errorf("%s: the commit of the fresh clone is %s, main of the hub is %s", c.p.id, got, first)
		}
		if files := workFiles(t, c.dir); len(files) != 0 {
			t.Errorf("%s: a fresh clone has files: %v", c.p.id, files)
		}
	}

	fa, fr := ada.follow(adaDir), robo.follow(roboDir)

	// The guard in the main directory refuses a commit there, also while the follower runs. The
	// refusal tells what to do instead. Nothing reaches the hub.
	put(t, adaDir, "documents/in-main.md", "# In the main directory\n")
	ada.mustGit(adaDir, "add", "documents/in-main.md")
	if out, code := ada.git(adaDir, "commit", "-q", "-m", "in the main directory"); code == 0 || !strings.Contains(out, "git worktree add") {
		t.Fatalf("commit in the main directory: exit %d, want a refusal that names git worktree add\n%s", code, out)
	}
	ada.mustGit(adaDir, "reset", "-q")
	if err := os.Remove(filepath.Join(adaDir, "documents/in-main.md")); err != nil {
		t.Fatal(err)
	}
	if got := w.mainOf("acme"); got != first {
		t.Fatalf("main of the hub moved to %s after a refused commit", got)
	}

	// A change by interface reaches both clones.
	createDoc(t, w.actor("ada", "acme"), "documents/plan/by-interface.md", "# By interface\n")
	for _, c := range []struct {
		p   *person
		dir string
	}{{ada, adaDir}, {robo, roboDir}} {
		waitContent(t, filepath.Join(c.dir, "documents/plan/by-interface.md"), "# By interface\n")
	}

	// A push from a worktree of Ada reaches the hub and Robo's clone.
	adaWt := ada.worktree(adaDir, "ada-change")
	put(t, adaWt, "documents/plan/by-worktree.md", "# By worktree\n")
	ada.commitAll(adaWt, "add a document from a worktree")
	ada.pushMain(adaWt)
	if got := strings.TrimSpace(contracttest.Git(t, w.srv.TenantRepoDir("acme"), "log", "-1", "--format=%an <%ae>", "main")); got != "Ada Lovelace <ada@office.invalid>" {
		t.Errorf("author of the pushed commit: %q", got)
	}
	waitContent(t, filepath.Join(roboDir, "documents/plan/by-worktree.md"), "# By worktree\n")
	waitSameAsHub(t, w, robo, "acme", roboDir)
	waitSameAsHub(t, w, ada, "acme", adaDir)

	// The agent works in a worktree with a few dozen files in several commits, and pushes once.
	roboWt := robo.worktree(roboDir, "robo-work")
	roboID := w.actor("robo", "acme").ParticipantID
	for i := 0; i < 36; i++ {
		put(t, roboWt, fmt.Sprintf("documents/research/topic-%02d.md", i), fmt.Sprintf("# Topic %d\n\nFindings of the agent.\n", i))
		if i%12 == 11 {
			robo.commitAll(roboWt, fmt.Sprintf("research part %d", i/12+1))
		}
	}
	for i := 1; i <= 6; i++ {
		put(t, roboWt, fmt.Sprintf("notes/%06d.yaml", i), noteYAML(i, roboID, "AUTHOR_KIND_AGENT"))
	}
	robo.commitAll(roboWt, "notes of the agent")
	robo.pushMain(roboWt)
	waitSameAsHub(t, w, ada, "acme", adaDir)
	waitSameAsHub(t, w, robo, "acme", roboDir)
	files := workFiles(t, adaDir)
	if n := len(files); n != 2+36+6 {
		t.Errorf("Ada's clone has %d files, want 44: %v", n, keys(files))
	}
	if got := files["documents/research/topic-35.md"]; got != "# Topic 35\n\nFindings of the agent.\n" {
		t.Errorf("last research file in Ada's clone: %q", got)
	}
	if h, r := ada.head(adaDir), robo.head(roboDir); h != r {
		t.Errorf("the two clones are at %s and %s", h, r)
	}
	// The global Git configuration of both participants was never changed.
	for _, p := range []*person{ada, robo} {
		if got := readFileOr(p.envValue("GIT_CONFIG_GLOBAL")); got != "[user]\n\tname = Global Name\n\temail = global@example.org\n" {
			t.Errorf("%s: the global Git configuration changed:\n%s", p.id, got)
		}
	}
	fa.stop()
	fr.stop()
}

func (p *person) envValue(name string) string {
	for _, kv := range p.env {
		if strings.HasPrefix(kv, name+"=") {
			return strings.TrimPrefix(kv, name+"=")
		}
	}
	return ""
}

func keys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// Two writers at the same time: the hub interface changes a line while a stale worktree of an
// agent changes the same line. The push of the agent is refused, the hub does not change, and the
// rebase ends in a normal Git conflict that stays in the worktree. The main directory is not
// touched by it. The agent solves it with plain Git and pushes; everyone ends with the result.
func TestSyncTwoWritersConflictStaysInTheWorktree(t *testing.T) {
	w := newWorld(t)
	ada, robo := w.person("ada", "acme"), w.person("robo", "acme")
	adaDir, roboDir := ada.clone("artifacts"), robo.clone("artifacts")
	fa, fr := ada.follow(adaDir), robo.follow(roboDir)

	const shared = "documents/plan/shared.md"
	createDoc(t, w.actor("ada", "acme"), shared, "line one\nline two\nline three\n")
	waitContent(t, filepath.Join(roboDir, shared), "line one\nline two\nline three\n")

	// Robo starts from this state and commits a change that stays on its computer.
	wt := robo.worktree(roboDir, "robo-edit")
	put(t, wt, shared, "line one\nline two by robo\nline three\n")
	robo.commitAll(wt, "robo changes line two")

	// At the same time, Ada changes the same line through the hub interface.
	changeDoc(t, w.actor("ada", "acme"), shared, "line one\nline two by ada\nline three\n")
	adaCommit := w.mainOf("acme")
	waitContent(t, filepath.Join(roboDir, shared), "line one\nline two by ada\nline three\n")

	// The push of the stale worktree is refused, and the hub keeps Ada's commit.
	if out, code := robo.git(wt, "push", "origin", "HEAD:main"); code == 0 {
		t.Fatalf("the stale push worked\n%s", out)
	} else if !strings.Contains(out, "rejected") {
		t.Errorf("the refusal is no plain Git rejection:\n%s", out)
	}
	if got := w.mainOf("acme"); got != adaCommit {
		t.Fatalf("main of the hub is %s after a refused push, want %s", got, adaCommit)
	}

	// Fetch and rebase: a normal conflict in the worktree.
	robo.mustGit(wt, "fetch", "-q", "origin")
	if out, code := robo.git(wt, "rebase", "origin/main"); code == 0 {
		t.Fatalf("the rebase had no conflict\n%s", out)
	}
	if status := robo.mustGit(wt, "status", "--porcelain"); !strings.Contains(status, "UU "+shared) {
		t.Errorf("the worktree does not show the conflict:\n%s", status)
	}
	if got := readFileOr(filepath.Join(wt, shared)); !strings.Contains(got, "<<<<<<<") || !strings.Contains(got, "line two by robo") || !strings.Contains(got, "line two by ada") {
		t.Errorf("conflict markers in the worktree:\n%s", got)
	}
	// The main directory of the agent stays clean and has the hub's version; so has the hub.
	if status := robo.mustGit(roboDir, "status", "--porcelain"); status != "" {
		t.Errorf("the main directory of the agent is not clean:\n%s", status)
	}
	if got := readFileOr(filepath.Join(roboDir, shared)); got != "line one\nline two by ada\nline three\n" {
		t.Errorf("the main directory of the agent: %q", got)
	}
	if got := w.mainOf("acme"); got != adaCommit {
		t.Errorf("the conflict changed main of the hub")
	}

	// The agent solves it with plain Git and pushes.
	const merged = "line one\nline two by ada and robo\nline three\n"
	put(t, wt, shared, merged)
	robo.mustGit(wt, "add", shared)
	robo.mustGit(wt, "-c", "core.editor=true", "rebase", "--continue")
	robo.pushMain(wt)

	waitContent(t, filepath.Join(adaDir, shared), merged)
	waitContent(t, filepath.Join(roboDir, shared), merged)
	waitSameAsHub(t, w, ada, "acme", adaDir)
	waitSameAsHub(t, w, robo, "acme", roboDir)
	if got := hubFiles(t, w, "acme")[shared]; got != merged {
		t.Errorf("the hub has %q", got)
	}
	// Both commits are in the history, each with its author.
	authors := contracttest.Git(t, w.srv.TenantRepoDir("acme"), "log", "--format=%an", "main")
	if !strings.Contains(authors, "Ada Lovelace") || !strings.Contains(authors, "Robo Agent") {
		t.Errorf("authors in the history of the hub:\n%s", authors)
	}
	fa.stop()
	fr.stop()
}

// leakText is everything a participant has of a clone: its files, its history with all
// messages and authors, the names of all objects and its Git configuration.
func leakText(t *testing.T, p *person, dir string) string {
	t.Helper()
	var sb strings.Builder
	for name, content := range workFiles(t, dir) {
		sb.WriteString(name + "\n" + content + "\n")
	}
	sb.WriteString(p.mustGit(dir, "log", "--all", "-p", "--format=%H %an <%ae> %s%n%b"))
	sb.WriteString(p.mustGit(dir, "rev-list", "--objects", "--all"))
	sb.WriteString(p.mustGit(dir, "config", "--local", "--list"))
	return sb.String()
}

func mustNotContain(t *testing.T, what, text string, words ...string) {
	t.Helper()
	low := strings.ToLower(text)
	for _, w := range words {
		if strings.Contains(low, strings.ToLower(w)) {
			t.Errorf("%s contains %q", what, w)
		}
	}
}

// Two tenants on one hub, each with a human, an agent and clones with followers, and Cy, who is a
// member of both and has a clone of each. Both tenants use the same path for different content.
// Nothing of one tenant (file, path, record, commit, event or error text) shows up in a clone,
// a follower or an error of the other.
func TestSyncTwoTenantsNeverLeak(t *testing.T) {
	w := newWorld(t)
	type clone struct {
		p   *person
		dir string
		f   *follower
	}
	var acme, globex []*clone
	for _, id := range []string{"ada", "robo", "cy"} {
		p := w.person(id, "acme")
		c := &clone{p: p, dir: p.clone("acme-artifacts")}
		c.f = p.follow(c.dir)
		acme = append(acme, c)
	}
	for _, id := range []string{"bob", "bot", "cy"} {
		p := w.person(id, "globex")
		c := &clone{p: p, dir: p.clone("globex-artifacts")}
		c.f = p.follow(c.dir)
		globex = append(globex, c)
	}
	tenants := []struct {
		name    string
		marker  string
		clones  []*clone
		human   string
		agent   string
		foreign string // the marker of the other tenant
	}{
		{"acme", "ACME-SECRET", acme, "ada", "robo", "GLOBEX-SECRET"},
		{"globex", "GLOBEX-SECRET", globex, "bob", "bot", "ACME-SECRET"},
	}
	for _, tn := range tenants {
		human := w.actor(tn.human, tn.name)
		// The same path in both tenants, with different content.
		createDoc(t, human, "documents/plan/shared.md", "# Plan of "+tn.marker+"\n")
		createDoc(t, human, "documents/"+strings.ToLower(tn.marker)+"/only.md", "# "+tn.marker+" path\n")
		if _, err := human.Decision.RecordDecision(syncCtx(t), &decisionv1.RecordDecisionRequest{CommandId: coreapi.NewCommandID(),
			Text: "Decision with " + tn.marker + ".", AuthorParticipantId: human.ParticipantID, SessionId: "weekly"}); err != nil {
			t.Fatal(err)
		}
		// A push from a worktree of the human and one from the agent.
		hc, ac := tn.clones[0], tn.clones[1]
		waitSameAsHub(t, w, hc.p, tn.name, hc.dir) // a worktree starts from the current main
		wt := hc.p.worktree(hc.dir, tn.human+"-change")
		put(t, wt, "documents/pushed.md", "# Pushed with "+tn.marker+"\n")
		hc.p.commitAll(wt, "commit message with "+tn.marker)
		hc.p.pushMain(wt)
		awt := ac.p.worktree(ac.dir, tn.agent+"-change")
		waitCond(t, tn.agent+" has the push of the human", func() bool {
			return readFileOr(filepath.Join(ac.dir, "documents/pushed.md")) == "# Pushed with "+tn.marker+"\n"
		})
		ac.p.mustGit(awt, "fetch", "-q", "origin")
		ac.p.mustGit(awt, "rebase", "-q", "origin/main")
		put(t, awt, "documents/agent.md", "# Agent with "+tn.marker+"\n")
		ac.p.commitAll(awt, "agent commit with "+tn.marker)
		ac.p.pushMain(awt)
	}
	for _, tn := range tenants {
		for _, c := range tn.clones {
			waitSameAsHub(t, w, c.p, tn.name, c.dir)
		}
	}

	for _, tn := range tenants {
		want := hubFiles(t, w, tn.name)
		if got := want["documents/plan/shared.md"]; got != "# Plan of "+tn.marker+"\n" {
			t.Errorf("%s: shared path has %q", tn.name, got)
		}
		for _, c := range tn.clones {
			what := tn.name + " clone of " + c.p.id
			mustNotContain(t, what, leakText(t, c.p, c.dir), tn.foreign)
			// The own marker is there; the check is no empty promise. The tenant name of the other
			// tenant is nowhere, not even in the configuration.
			if !strings.Contains(leakText(t, c.p, c.dir), tn.marker) {
				t.Errorf("%s: the clone has no content of its own tenant", what)
			}
			other := map[string]string{"acme": "globex", "globex": "acme"}[tn.name]
			mustNotContain(t, what, leakText(t, c.p, c.dir), other)
			mustNotContain(t, what+" (follower output)", c.f.output(), other, "secret")
		}
	}
	// Cy has two clones, one for each tenant, and each has only its own files.
	if acme[2].p.id != "cy" || globex[2].p.id != "cy" {
		t.Fatal("test setup")
	}
	if got := workFiles(t, acme[2].dir)["documents/plan/shared.md"]; got != "# Plan of ACME-SECRET\n" {
		t.Errorf("Cy's clone of acme: %q", got)
	}
	if got := workFiles(t, globex[2].dir)["documents/plan/shared.md"]; got != "# Plan of GLOBEX-SECRET\n" {
		t.Errorf("Cy's clone of globex: %q", got)
	}

	// A participant of globex cannot set up a clone of acme, and the error says nothing of acme.
	bob := w.person("bob", "acme")
	out, errOut, code := bob.office(bob.work, "sync", "init", filepath.Join(bob.work, "foreign"))
	if code == 0 {
		t.Errorf("a member of globex set up a clone of acme:\n%s%s", out, errOut)
	}
	mustNotContain(t, "the error of the refused sync init", out+errOut, "ACME-SECRET", "ada", "robo")
	if _, err := os.Stat(filepath.Join(bob.work, "foreign", "documents")); err == nil {
		t.Error("the refused sync init left documents of acme")
	}
	for _, tn := range tenants {
		for _, c := range tn.clones {
			c.f.stop()
		}
	}
}

// Sessions with sentences off the record never put a sentence into a clone: not while the
// session runs, not after the hub restarted, and not after the session ended (which stores the
// minutes in the artifact repository). A new clone made after all this has no sentence either. The
// session of acme ends; the session of globex runs over the restart and ends after it.
func TestSyncSessionsNeverReachTheClones(t *testing.T) {
	slow(t) // the followers wait for the restarted hub in real time
	hubArgs := []string{"-archive-interval", "100ms"}
	w := newWorld(t, hubArgs...)
	type tenantRun struct {
		name, human, public, secret, after string
		p                                  *person
		dir                                string
		f                                  *follower
	}
	runs := []*tenantRun{
		{name: "acme", human: "ada", public: "Acme public sentence one.", secret: "Acme private sentence one.", after: "Acme public sentence two."},
		{name: "globex", human: "bob", public: "Globex public sentence one.", secret: "Globex private sentence one.", after: "Globex public sentence two."},
	}
	say := func(a *contracttest.Actor, text string) uint32 {
		t.Helper()
		res, err := a.Commands.SubmitUtterance(syncCtx(t), &sessioncommandsv1.SubmitUtteranceRequest{SessionId: "weekly",
			CommandId: coreapi.NewCommandID(), ParticipantId: a.ParticipantID, Text: text, Modality: sessionstreamv1.Modality_MODALITY_TEXT})
		if err != nil {
			t.Fatalf("say %q: %v", text, err)
		}
		return res.GetResult().GetEvent().GetId()
	}
	end := func(a *contracttest.Actor) {
		t.Helper()
		if _, err := a.Commands.EndSession(syncCtx(t), &sessioncommandsv1.EndSessionRequest{SessionId: "weekly",
			CommandId: coreapi.NewCommandID(), ParticipantId: a.ParticipantID}); err != nil {
			t.Fatalf("end the session: %v", err)
		}
	}
	for _, r := range runs {
		r.p = w.person(r.human, r.name)
		r.dir = r.p.clone("artifacts")
		r.f = r.p.follow(r.dir)
		a := w.actor(r.human, r.name)
		if _, err := a.Commands.Join(syncCtx(t), &sessioncommandsv1.JoinRequest{SessionId: "weekly", CommandId: coreapi.NewCommandID()}); err != nil {
			t.Fatal(err)
		}
		say(a, r.public)
		id := say(a, r.secret)
		if _, err := a.Commands.MarkOffTheRecord(syncCtx(t), &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: "weekly",
			CommandId: coreapi.NewCommandID(), ParticipantId: a.ParticipantID, UtteranceEventId: id}); err != nil {
			t.Fatal(err)
		}
		createDoc(t, a, "documents/before-restart.md", "# Before the restart of "+r.name+"\n")
	}
	// Acme ends its session before the restart; globex ends it after.
	end(w.actor("ada", "acme"))
	for _, r := range runs {
		contracttest.WaitArchived(t, w.srv.TenantSessionLogsDir(r.name), "weekly", 30*time.Second, func(a *contracttest.ArchivedSession) bool {
			return len(a.Events) >= 4
		})
		waitContent(t, filepath.Join(r.dir, "documents/before-restart.md"), "# Before the restart of "+r.name+"\n")
	}

	// The hub restarts on the same data and address.
	if code, err := w.srv.Stop(20 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop the hub: %d, %v", code, err)
	}
	w.srv = contracttest.StartHubIn(t, w.srv.DataDir, w.srv.AdminKey, nil, append([]string{"-addr", w.srv.Addr()}, hubArgs...)...)

	for _, r := range runs {
		a := w.actor(r.human, r.name)
		if r.name == "globex" {
			say(a, r.after)
			end(a)
		}
		createDoc(t, a, "documents/after-restart.md", "# After the restart of "+r.name+"\n")
	}
	// The followers bring the documents written after the restart.
	for _, r := range runs {
		waitContent(t, filepath.Join(r.dir, "documents/after-restart.md"), "# After the restart of "+r.name+"\n")
		waitSameAsHub(t, w, r.p, r.name, r.dir)
	}
	// The session logs hold the sentences: the check below is no empty promise.
	for _, r := range runs {
		logs := contracttest.AllText(t, w.srv.TenantSessionLogsDir(r.name))
		if !strings.Contains(logs, r.secret) || !strings.Contains(logs, r.public) {
			t.Errorf("%s: the session-log repository lacks the sentences", r.name)
		}
	}

	var sentences []string
	for _, r := range runs {
		sentences = append(sentences, r.public, r.secret, r.after)
	}
	for _, r := range runs {
		fresh := r.p.clone("fresh")
		for name, dir := range map[string]string{"the running clone": r.dir, "a new clone": fresh} {
			what := r.name + ": " + name
			mustNotContain(t, what, leakText(t, r.p, dir), sentences...)
			if _, err := os.Stat(filepath.Join(dir, "sessions")); err == nil {
				t.Errorf("%s has a directory sessions", what)
			}
		}
		mustNotContain(t, r.name+": output of the follower", r.f.output(), sentences...)
		r.f.stop()
	}
}

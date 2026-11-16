package contract

// Contract tests of "office sync" and "office sync follow" (bb-sync through if-cli,
// dec-artifact-sync): the real office binary updates and follows a clone against a real
// office-hub, and the plain git command line tool works in the clone and in worktrees. The tests
// wait for conditions with generous deadlines and never check how long something takes.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const syncWait = 90 * time.Second

// waitFor polls until cond is true.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(syncWait)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// waitFile waits until the file has the content.
func waitFile(t *testing.T, path, want string) {
	t.Helper()
	var got []byte
	var err error
	waitFor(t, path+" with the content "+want, func() bool {
		got, err = os.ReadFile(path)
		return err == nil && string(got) == want
	})
}

func fileContent(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(b)
}

func changeDoc(t *testing.T, a *contracttest.Actor, path, content string) {
	t.Helper()
	f := readDoc(t, a, path)
	if _, err := a.Document.ChangeDocument(ctx10(t), &documentv1.ChangeDocumentRequest{Path: path, Content: content,
		BaseVersion: f.GetVersion(), CommandId: coreapi.NewCommandID()}); err != nil {
		t.Fatalf("change %s: %v", path, err)
	}
}

// syncSet is a hub with a document and a participant with a set-up directory.
type syncSet struct {
	srv *contracttest.Hub
	ada *contracttest.Actor
	u   *syncUser
	dir string
}

func newSyncSet(t *testing.T, srv *contracttest.Hub) *syncSet {
	t.Helper()
	ada := srv.Participant(t, "ada", "Ada Lovelace", human)
	writeDoc(t, ada, "documents/plan/a.md", "# A\n")
	u := syncMember(t, srv, "grace", "Grace Hopper")
	dir := filepath.Join(u.work, "artifacts")
	u.mustInit(dir)
	return &syncSet{srv: srv, ada: ada, u: u, dir: dir}
}

type updateJSON struct {
	Directory string
	Updated   bool
	From, To  string
}

// sync runs "office sync" and returns the result of --json.
func (s *syncSet) sync(t *testing.T, args ...string) updateJSON {
	t.Helper()
	out, errOut, code := s.u.office(s.dir, append([]string{"sync", "--json"}, args...)...)
	if code != 0 {
		t.Fatalf("office sync: exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	var res updateJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("result %q: %v", out, err)
	}
	return res
}

// refused runs "office sync" and wants an error that holds all of the words.
func (s *syncSet) refused(t *testing.T, dir string, words ...string) string {
	t.Helper()
	out, errOut, code := s.u.office(dir, "sync")
	if code == 0 {
		t.Fatalf("office sync worked, want a refusal: %s", out)
	}
	for _, w := range words {
		if !strings.Contains(errOut, w) {
			t.Errorf("the error has no %q:\n%s", w, errOut)
		}
	}
	return errOut
}

func (s *syncSet) head(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(s.u.mustGit(dir, "rev-parse", "HEAD"))
}

// worktree makes a linked worktree of the set-up directory.
func (s *syncSet) worktree(t *testing.T, name string) string {
	t.Helper()
	wt := filepath.Join(s.u.work, name)
	s.u.mustGit(s.dir, "worktree", "add", "-q", wt, "-b", name)
	return wt
}

// pushFile commits a file in the worktree and pushes it to main of the hub.
func (s *syncSet) pushFile(t *testing.T, wt, file, content string) {
	t.Helper()
	contracttest.WriteFile(t, wt, file, content)
	s.u.mustGit(wt, "add", "-A")
	s.u.mustGit(wt, "commit", "-q", "-m", "change "+file)
	s.u.mustGit(wt, "push", "-q", "origin", "HEAD:main")
}

// A change by the documents interface reaches the directory through "office sync". A second call
// has nothing to do.
func TestSyncUpdateBringsChangeByInterface(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	before := s.head(t, s.dir)
	if res := s.sync(t); res.Updated || res.To != before {
		t.Errorf("nothing changed, got %+v", res)
	}
	changeDoc(t, s.ada, "documents/plan/a.md", "# A2\n")
	writeDoc(t, s.ada, "documents/plan/b.md", "# B\n")

	res := s.sync(t)
	if !res.Updated || res.From != before || res.To != s.head(t, s.dir) || res.To == before {
		t.Errorf("result %+v, head %s, before %s", res, s.head(t, s.dir), before)
	}
	if got := fileContent(filepath.Join(s.dir, "documents/plan/a.md")); got != "# A2\n" {
		t.Errorf("a.md: %q", got)
	}
	if got := fileContent(filepath.Join(s.dir, "documents/plan/b.md")); got != "# B\n" {
		t.Errorf("b.md: %q", got)
	}
	if res := s.sync(t); res.Updated {
		t.Errorf("second update: %+v", res)
	}
	// No merge commit and no other branch: main is a straight line.
	if out := s.u.mustGit(s.dir, "rev-list", "--merges", "HEAD"); strings.TrimSpace(out) != "" {
		t.Errorf("merge commits in main: %s", out)
	}
	// The text output and the directory argument work as well.
	changeDoc(t, s.ada, "documents/plan/a.md", "# A3\n")
	out, errOut, code := s.u.office(s.u.work, "sync", s.dir)
	if code != 0 || !strings.Contains(out, "updated") || !strings.Contains(out, s.dir) {
		t.Errorf("sync with a directory: exit %d, %q, %q", code, out, errOut)
	}
}

// A change by push from a second clone, and a change by push from a worktree of the first clone,
// reach the main directory.
func TestSyncUpdateBringsChangeByPush(t *testing.T) {
	srv := startSyncHub(t)
	s := newSyncSet(t, srv)

	other := syncMember(t, srv, "heidi", "Heidi Lamarr")
	otherDir := filepath.Join(other.work, "artifacts")
	other.mustInit(otherDir)
	otherWt := filepath.Join(other.work, "change")
	other.mustGit(otherDir, "worktree", "add", "-q", otherWt, "-b", "change")
	contracttest.WriteFile(t, otherWt, "documents/plan/pushed.md", "# Pushed\n")
	other.mustGit(otherWt, "add", "-A")
	other.mustGit(otherWt, "commit", "-q", "-m", "from the second clone")
	other.mustGit(otherWt, "push", "-q", "origin", "HEAD:main")

	if res := s.sync(t); !res.Updated {
		t.Errorf("result %+v", res)
	}
	if got := fileContent(filepath.Join(s.dir, "documents/plan/pushed.md")); got != "# Pushed\n" {
		t.Errorf("pushed.md in the first clone: %q", got)
	}

	// From a worktree of the first clone: the main directory moves, the worktree has its commit.
	wt := s.worktree(t, "mine")
	s.pushFile(t, wt, "documents/plan/mine.md", "# Mine\n")
	if _, err := os.Stat(filepath.Join(s.dir, "documents/plan/mine.md")); err == nil {
		t.Fatal("the push changed the main directory by itself")
	}
	if res := s.sync(t); !res.Updated {
		t.Errorf("result %+v", res)
	}
	if got := fileContent(filepath.Join(s.dir, "documents/plan/mine.md")); got != "# Mine\n" {
		t.Errorf("mine.md in the main directory: %q", got)
	}
	// The second clone gets both changes as well.
	if res, errOut, code := other.office(otherDir, "sync"); code != 0 {
		t.Fatalf("sync in the second clone: %d %s %s", code, res, errOut)
	}
	if got := fileContent(filepath.Join(otherDir, "documents/plan/mine.md")); got != "# Mine\n" {
		t.Errorf("mine.md in the second clone: %q", got)
	}
}

// Own commits in the main directory: sync stops, says so, and changes nothing.
func TestSyncUpdateStopsOnOwnCommits(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	contracttest.WriteFile(t, s.dir, "documents/own.md", "# Own\n")
	s.u.mustGit(s.dir, "add", "-A")
	// The guard refuses a commit; --no-verify is how a participant gets around it on purpose.
	s.u.mustGit(s.dir, "commit", "-q", "--no-verify", "-m", "own commit")
	own := s.head(t, s.dir)

	// Ahead of the hub, nothing new there.
	s.refused(t, s.dir, "commits of its own", "worktree")
	// The hub moves on: main and the hub went different ways.
	writeDoc(t, s.ada, "documents/plan/later.md", "# Later\n")
	s.refused(t, s.dir, "commits of its own", "Nothing was changed")
	if got := s.head(t, s.dir); got != own {
		t.Errorf("main moved to %s, was %s", got, own)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "documents/plan/later.md")); err == nil {
		t.Errorf("the refused update changed the files")
	}
	if got := fileContent(filepath.Join(s.dir, "documents/own.md")); got != "# Own\n" {
		t.Errorf("the own file: %q", got)
	}
}

// Files that a participant changed and did not commit are never touched: an incoming change that
// would overwrite them is refused, an unrelated change does not disturb them.
func TestSyncUpdateKeepsUncommittedFiles(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	a := filepath.Join(s.dir, "documents/plan/a.md")
	other := filepath.Join(s.dir, "notes.txt")
	if err := os.WriteFile(a, []byte("# my edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := s.head(t, s.dir)

	// An unrelated change comes in and both files stay.
	writeDoc(t, s.ada, "documents/plan/unrelated.md", "# Unrelated\n")
	if res := s.sync(t); !res.Updated {
		t.Errorf("result %+v", res)
	}
	if got := fileContent(a); got != "# my edit\n" || fileContent(other) != "mine\n" {
		t.Errorf("uncommitted files after an unrelated update: %q, %q", got, fileContent(other))
	}

	// A change of the file that was edited is refused, and nothing is changed.
	changeDoc(t, s.ada, "documents/plan/a.md", "# A from the hub\n")
	writeDoc(t, s.ada, "documents/plan/second.md", "# Second\n")
	mid := s.head(t, s.dir)
	if mid == before {
		t.Fatal("the unrelated update did not move main")
	}
	s.refused(t, s.dir, "uncommitted", "documents/plan/a.md", "Nothing was changed")
	if got := fileContent(a); got != "# my edit\n" {
		t.Errorf("the edited file after the refusal: %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "documents/plan/second.md")); err == nil || s.head(t, s.dir) != mid {
		t.Errorf("the refused update changed something")
	}

	// The same for an untracked file in the way of a new document.
	s.u.mustGit(s.dir, "checkout", "-q", "--", "documents/plan/a.md")
	if res := s.sync(t); !res.Updated {
		t.Fatalf("after the checkout: %+v", res)
	}
	writeDoc(t, s.ada, "documents/plan/new.md", "# New\n")
	untracked := filepath.Join(s.dir, "documents/plan/new.md")
	if err := os.WriteFile(untracked, []byte("in the way\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.refused(t, s.dir, "uncommitted", "new.md")
	if got := fileContent(untracked); got != "in the way\n" {
		t.Errorf("the untracked file after the refusal: %q", got)
	}
}

// When the hub rewrote the history of main, the update says so and changes nothing.
func TestSyncUpdateReportsRewrittenHistory(t *testing.T) {
	srv := startSyncHub(t)
	s := newSyncSet(t, srv)
	writeDoc(t, s.ada, "documents/plan/b.md", "# B\n")
	s.sync(t)
	old := s.head(t, s.dir)

	// A new commit on the parent of the old tip: the old tip is no ancestor of it.
	repo := srv.ArchiveDir()
	tip := strings.TrimSpace(contracttest.Git(t, repo, "rev-parse", "main"))
	parent := tip + "~1"
	if tip != old {
		t.Fatalf("the hub has %s, the clone %s", tip, old)
	}
	rewritten := strings.TrimSpace(contracttest.Git(t, repo, "-c", "user.name=Rewrite", "-c", "user.email=rewrite@example.org",
		"commit-tree", parent+"^{tree}", "-p", parent, "-m", "rewritten"))
	contracttest.Git(t, repo, "update-ref", "refs/heads/main", rewritten)

	s.refused(t, s.dir, "rewrote the history", "Nothing was changed")
	if got := s.head(t, s.dir); got != old {
		t.Errorf("main moved to %s, was %s", got, old)
	}
	if got := fileContent(filepath.Join(s.dir, "documents/plan/b.md")); got != "# B\n" {
		t.Errorf("file after the refusal: %q", got)
	}
	// Run again: the clone knows the new history now, and it still does not move.
	s.refused(t, s.dir, "rewrote the history", "Nothing was changed")
	if got := s.head(t, s.dir); got != old {
		t.Errorf("main moved to %s, was %s", got, old)
	}
}

// A directory that is not set up, a plain Git repository and a linked worktree are refused, by
// "office sync" and by "office sync follow".
func TestSyncRefusesWhatIsNotTheMainDirectory(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	empty := filepath.Join(s.u.work, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(s.u.work, "plain")
	s.u.mustGit(s.u.work, "init", "-q", plain)
	wt := s.worktree(t, "linked")
	sub := filepath.Join(s.dir, "documents")

	s.refused(t, empty, "not set up", "office sync init")
	s.refused(t, plain, "not set up", "office sync init")
	s.refused(t, wt, "linked worktree", "main directory")
	s.refused(t, sub, "not set up")
	// With the directory as an argument.
	if _, errOut, code := s.u.office(s.u.work, "sync", empty); code == 0 || !strings.Contains(errOut, "not set up") {
		t.Errorf("sync with a directory argument: exit %d: %s", code, errOut)
	}
	for _, dir := range []string{empty, plain, wt} {
		out, errOut, code := s.u.office(dir, "sync", "follow")
		if code == 0 || out != "" || (!strings.Contains(errOut, "not set up") && !strings.Contains(errOut, "linked worktree")) {
			t.Errorf("sync follow in %s: exit %d: %q %q", dir, code, out, errOut)
		}
	}
}

// follower is a running "office sync follow --json". It reads every update line.
type follower struct {
	t     *testing.T
	p     *contracttest.Process
	lines chan updateJSON
	seen  []updateJSON
}

func startFollow(t *testing.T, u *syncUser, dir string, args ...string) *follower {
	t.Helper()
	p := contracttest.StartIn(t, u.work, "office", u.env, append([]string{"sync", "follow", dir, "--json"}, args...)...)
	f := &follower{t: t, p: p, lines: make(chan updateJSON, 1000)}
	go func() {
		sc := bufio.NewScanner(p.Stdout)
		for sc.Scan() {
			var res updateJSON
			if json.Unmarshal(sc.Bytes(), &res) == nil && res.Directory != "" {
				f.lines <- res
			}
		}
	}()
	return f
}

// update waits for the next update.
func (f *follower) update() updateJSON {
	f.t.Helper()
	select {
	case res := <-f.lines:
		f.seen = append(f.seen, res)
		return res
	case <-f.p.Done():
		f.t.Fatalf("the follower exited:\n%s", f.p.Stderr())
	case <-time.After(syncWait):
		f.t.Fatalf("no update from the follower:\n%s", f.p.Stderr())
	}
	return updateJSON{}
}

// stop ends the follower with SIGTERM like a participant ends it with Ctrl-C. It must exit with
// success.
func (f *follower) stop() {
	f.t.Helper()
	code, err := f.p.Stop(30 * time.Second)
	if err != nil || code != 0 {
		f.t.Errorf("stop the follower: exit %d, %v\n%s", code, err, f.p.Stderr())
	}
}

// A follower brings changes of both kinds: by interface and by push, from a second clone and from
// a worktree of its own clone. It updates once at the start.
func TestSyncFollowBringsChanges(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	s := newSyncSet(t, srv)
	writeDoc(t, s.ada, "documents/plan/early.md", "# Early\n")
	f := startFollow(t, s.u, s.dir)
	// The first update comes after the hub confirmed the registration, and brings what happened
	// before the start.
	if res := f.update(); !res.Updated {
		t.Errorf("first update %+v", res)
	}
	waitFile(t, filepath.Join(s.dir, "documents/plan/early.md"), "# Early\n")

	writeDoc(t, s.ada, "documents/plan/by-interface.md", "# Interface\n")
	waitFile(t, filepath.Join(s.dir, "documents/plan/by-interface.md"), "# Interface\n")

	other := syncMember(t, srv, "heidi", "Heidi Lamarr")
	otherDir := filepath.Join(other.work, "artifacts")
	other.mustInit(otherDir)
	otherWt := filepath.Join(other.work, "change")
	other.mustGit(otherDir, "worktree", "add", "-q", otherWt, "-b", "change")
	contracttest.WriteFile(t, otherWt, "documents/plan/by-push.md", "# Push\n")
	other.mustGit(otherWt, "add", "-A")
	other.mustGit(otherWt, "commit", "-q", "-m", "push")
	other.mustGit(otherWt, "push", "-q", "origin", "HEAD:main")
	waitFile(t, filepath.Join(s.dir, "documents/plan/by-push.md"), "# Push\n")

	wt := s.worktree(t, "mine")
	s.pushFile(t, wt, "documents/plan/by-worktree.md", "# Worktree\n")
	waitFile(t, filepath.Join(s.dir, "documents/plan/by-worktree.md"), "# Worktree\n")
	waitFor(t, "main equal to the hub", func() bool {
		return s.head(t, s.dir) == strings.TrimSpace(contracttest.Git(t, srv.ArchiveDir(), "rev-parse", "main"))
	})
	f.stop()
}

// A change that happened while no follower ran is caught up by the update at the start.
func TestSyncFollowCatchesUpMissedEvents(t *testing.T) {
	s := newSyncSet(t, contracttest.StartHub(t, nil))
	f := startFollow(t, s.u, s.dir)
	f.update()
	writeDoc(t, s.ada, "documents/plan/one.md", "# One\n")
	waitFile(t, filepath.Join(s.dir, "documents/plan/one.md"), "# One\n")
	f.stop()

	writeDoc(t, s.ada, "documents/plan/two.md", "# Two\n")
	changeDoc(t, s.ada, "documents/plan/a.md", "# A changed\n")
	f = startFollow(t, s.u, s.dir)
	if res := f.update(); !res.Updated {
		t.Errorf("first update of the second run: %+v", res)
	}
	waitFile(t, filepath.Join(s.dir, "documents/plan/two.md"), "# Two\n")
	waitFile(t, filepath.Join(s.dir, "documents/plan/a.md"), "# A changed\n")
	f.stop()
}

// The hub stops and starts again on the same data directory and address: the follower connects
// again, updates, and follows.
func TestSyncFollowReconnectsAfterHubRestart(t *testing.T) {
	srv := startSyncHub(t)
	s := newSyncSet(t, srv)
	f := startFollow(t, s.u, s.dir)
	f.update()
	writeDoc(t, s.ada, "documents/plan/before.md", "# Before\n")
	waitFile(t, filepath.Join(s.dir, "documents/plan/before.md"), "# Before\n")

	if _, err := srv.Stop(20 * time.Second); err != nil {
		t.Fatal(err)
	}
	addr := strings.TrimPrefix(srv.URL, "https://")
	p := contracttest.Start(t, "office-hub", nil, "-addr", addr, "-data-dir", srv.DataDir,
		"-signing-key", srv.SigningKeyFile, "-token-ttl", syncTokenTTL.String(),
		"-first-system-admin", contracttest.AdminID+"="+auth.FormatPublicKey(auth.PublicKey(srv.AdminKey)))
	t.Cleanup(func() { _, _ = p.Stop(10 * time.Second) })
	p.WaitStderr(t, regexp.MustCompile(`listening on (https://\S+)`), 30*time.Second)

	// The restarted hub has the same data and address. A change made after the restart arrives.
	restarted := *srv
	restarted.Process = p
	ada := restarted.WithTenant(t, s.ada.Key, contracttest.DefaultTenant)
	writeDoc(t, ada, "documents/plan/after.md", "# After\n")
	waitFile(t, filepath.Join(s.dir, "documents/plan/after.md"), "# After\n")
	f.stop()
}

// The access token ends while the follower runs (the hub here gives tokens that last two seconds):
// the follower gets a new token, connects again, and changes still arrive.
func TestSyncFollowSurvivesExpiredTokens(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	f := startFollow(t, s.u, s.dir)
	f.update()
	for i, name := range []string{"first", "second", "third"} {
		if i > 0 {
			time.Sleep(syncTokenTTL + time.Second)
		}
		writeDoc(t, s.ada, "documents/plan/"+name+".md", "# "+name+"\n")
		waitFile(t, filepath.Join(s.dir, "documents/plan/"+name+".md"), "# "+name+"\n")
	}
	f.stop()
}

// Path prefixes limit the events that start an update. An update brings everything.
func TestSyncFollowPathPrefixes(t *testing.T) {
	s := newSyncSet(t, contracttest.StartHub(t, nil))
	f := startFollow(t, s.u, s.dir, "--path", "documents/watched", "--path", "documents/also/one.md")
	f.update()

	// No event for this one, so no update of its own.
	writeDoc(t, s.ada, "documents/plan/ignored.md", "# Ignored\n")
	writeDoc(t, s.ada, "documents/watched/w.md", "# Watched\n")
	res := f.update()
	if !res.Updated {
		t.Errorf("update after the event: %+v", res)
	}
	waitFile(t, filepath.Join(s.dir, "documents/watched/w.md"), "# Watched\n")
	// The update after the event brought the ignored change as well.
	waitFile(t, filepath.Join(s.dir, "documents/plan/ignored.md"), "# Ignored\n")
	// There were no more updates than the first and the one for the event.
	if len(f.seen) != 2 {
		t.Errorf("updates: %+v", f.seen)
	}
	writeDoc(t, s.ada, "documents/also/one.md", "# One\n")
	waitFile(t, filepath.Join(s.dir, "documents/also/one.md"), "# One\n")
	f.stop()
}

// An update that needs the participant does not stop the follower: after the participant has
// fixed the problem, the next change brings everything.
func TestSyncFollowGoesOnAfterAProblem(t *testing.T) {
	s := newSyncSet(t, contracttest.StartHub(t, nil))
	a := filepath.Join(s.dir, "documents/plan/a.md")
	if err := os.WriteFile(a, []byte("# my edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := startFollow(t, s.u, s.dir)
	f.update()
	changeDoc(t, s.ada, "documents/plan/a.md", "# A from the hub\n")
	waitFor(t, "the report of the problem", func() bool { return strings.Contains(f.p.Stderr(), "uncommitted") })
	if got := fileContent(a); got != "# my edit\n" {
		t.Errorf("the edited file: %q", got)
	}
	s.u.mustGit(s.dir, "checkout", "-q", "--", "documents/plan/a.md")
	writeDoc(t, s.ada, "documents/plan/b.md", "# B\n")
	waitFile(t, filepath.Join(s.dir, "documents/plan/b.md"), "# B\n")
	waitFile(t, a, "# A from the hub\n")
	f.stop()
}

// The help texts explain both commands in plain words, each with an example.
func TestSyncUpdateAndFollowHelp(t *testing.T) {
	for name, wants := range map[string][]string{
		"sync":        {"office sync:", "usage: office sync [<directory>]", "fast", "commits of its own", "rewrote", "Examples:", "office sync ~/artifacts", "office sync follow"},
		"sync follow": {"office sync follow:", "usage: office sync follow [<directory>]", "--path", "Ctrl-C", "reconnect", "Examples:", "office sync follow ~/artifacts"},
	} {
		args := append(strings.Fields(name), "--help")
		out, errOut, code := contracttest.Run(t, "office", helpEnv(t), args...)
		if code != 0 {
			t.Fatalf("%s --help: exit %d: %s", name, code, errOut)
		}
		for _, want := range wants {
			if want == "reconnect" {
				want = "connects again"
			}
			if want == "fast" {
				want = "moves main forward"
			}
			if !strings.Contains(out, want) {
				t.Errorf("no %q in the help of %s:\n%s", want, name, out)
			}
		}
	}
}

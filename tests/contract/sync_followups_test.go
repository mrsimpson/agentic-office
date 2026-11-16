package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// rewriteHub moves main of the hub to a new commit on the parent of its tip and returns it: the
// old tip is no ancestor of the new one.
func rewriteHub(t *testing.T, srv *contracttest.Hub) string {
	t.Helper()
	repo := srv.ArchiveDir()
	parent := strings.TrimSpace(contracttest.Git(t, repo, "rev-parse", "main")) + "~1"
	rewritten := strings.TrimSpace(contracttest.Git(t, repo, "-c", "user.name=Rewrite", "-c", "user.email=rewrite@example.org",
		"commit-tree", parent+"^{tree}", "-p", parent, "-m", "rewritten"))
	contracttest.Git(t, repo, "update-ref", "refs/heads/main", rewritten)
	return rewritten
}

// When the hub rewrote the history of main, nothing of the participant is on main: main moves to
// the new state, files of the old history disappear and an uncommitted unrelated file stays.
func TestSyncUpdateFollowsRewrittenHistory(t *testing.T) {
	srv := startSyncHub(t)
	s := newSyncSet(t, srv)
	writeDoc(t, s.ada, "documents/plan/b.md", "# B\n")
	s.sync(t)
	old := s.head(t, s.dir)
	mine := filepath.Join(s.dir, "notes.txt")
	if err := os.WriteFile(mine, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rewritten := rewriteHub(t, srv)
	res := s.sync(t)
	if !res.Updated || res.To != rewritten || res.From != old {
		t.Errorf("result %+v, want main moved from %s to %s", res, old, rewritten)
	}
	if got := s.head(t, s.dir); got != rewritten {
		t.Errorf("main is %s, want %s", got, rewritten)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "documents/plan/b.md")); err == nil {
		t.Errorf("a file of the old history is still there")
	}
	if got := fileContent(mine); got != "mine\n" {
		t.Errorf("the uncommitted file: %q", got)
	}
	// The next run has nothing to do.
	if res := s.sync(t); res.Updated {
		t.Errorf("second run: %+v", res)
	}
}

// A rewrite of the history that would overwrite an uncommitted file is refused and changes
// nothing, also when the command runs again.
func TestSyncUpdateRefusesRewriteOverUncommittedFile(t *testing.T) {
	srv := startSyncHub(t)
	s := newSyncSet(t, srv)
	changeDoc(t, s.ada, "documents/plan/a.md", "# A, second version\n")
	s.sync(t)
	old := s.head(t, s.dir)
	a := filepath.Join(s.dir, "documents/plan/a.md")
	if err := os.WriteFile(a, []byte("# my edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The new state has the first version of a.md again, so the edit is in the way.
	rewritten := rewriteHub(t, srv)
	for i := 0; i < 2; i++ {
		errOut := s.refused(t, s.dir, "rewrote the history", "uncommitted", "documents/plan/a.md", "Nothing was changed")
		if strings.Contains(errOut, "commits of its own") {
			t.Errorf("run %d names the wrong cause:\n%s", i, errOut)
		}
		if got := s.head(t, s.dir); got != old {
			t.Errorf("run %d: main moved to %s, was %s", i, got, old)
		}
		if got := fileContent(a); got != "# my edit\n" {
			t.Errorf("run %d: the edited file: %q", i, got)
		}
	}
	// With the file out of the way the move works.
	s.u.mustGit(s.dir, "checkout", "-q", "--", "documents/plan/a.md")
	if res := s.sync(t); !res.Updated || res.To != rewritten {
		t.Errorf("after the checkout: %+v", res)
	}
}

// Own commits are no rewrite of the hub: the update reports them with the single cause, also when
// the hub rewrote the history as well, and changes nothing.
func TestSyncUpdateReportsOwnCommitsBesideRewrite(t *testing.T) {
	srv := startSyncHub(t)
	s := newSyncSet(t, srv)
	writeDoc(t, s.ada, "documents/plan/b.md", "# B\n")
	s.sync(t)
	contracttest.WriteFile(t, s.dir, "documents/own.md", "# Own\n")
	s.u.mustGit(s.dir, "add", "-A")
	s.u.mustGit(s.dir, "commit", "-q", "--no-verify", "-m", "own commit")
	own := s.head(t, s.dir)

	rewriteHub(t, srv)
	for i := 0; i < 2; i++ {
		errOut := s.refused(t, s.dir, "commits of its own", "Nothing was changed")
		if strings.Contains(errOut, "rewrote") {
			t.Errorf("run %d names a second cause:\n%s", i, errOut)
		}
		if got := s.head(t, s.dir); got != own {
			t.Errorf("run %d: main moved to %s, was %s", i, got, own)
		}
	}
	if got := fileContent(filepath.Join(s.dir, "documents/own.md")); got != "# Own\n" {
		t.Errorf("the own file: %q", got)
	}
}

// A directory that has the name of a command is not guessed: the commands ask for ./name.
func TestSyncCommandNamedDirectoryIsNotGuessed(t *testing.T) {
	s := newSyncSet(t, startSyncHub(t))
	for _, name := range []string{"init", "follow", "credential"} {
		if err := os.MkdirAll(filepath.Join(s.u.work, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls := [][]string{
		{"sync", "init", "init"}, {"sync", "init", "follow"},
		{"sync", "follow", "init"}, {"sync", "follow", "credential"},
		{"sync", "credential", "init"},
	}
	for _, args := range calls {
		_, errOut, code := s.u.office(s.u.work, args...)
		name := args[len(args)-1]
		if code == 0 || !strings.Contains(errOut, "./"+name) || !strings.Contains(errOut, "name of a command") {
			t.Errorf("office %v: exit %d, stderr:\n%s", args, code, errOut)
		}
	}
	// With ./ the name is the directory.
	if out, errOut, code := s.u.office(s.u.work, "sync", "init", "./init"); code != 0 {
		t.Fatalf("office sync init ./init: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, err := os.Stat(filepath.Join(s.u.work, "init", ".git")); err != nil {
		t.Errorf("office sync init ./init did not set up the directory: %v", err)
	}
	out, errOut, code := s.u.office(s.u.work, "sync", "--json", "./init")
	if code != 0 {
		t.Errorf("office sync ./init: exit %d\n%s\n%s", code, out, errOut)
	}
}

// A GIT_SSL_CAINFO in the environment beats the certificate in the clone for plain Git. "office
// sync init" and "office sync" warn when it names another file, and keep working themselves.
func TestSyncWarnsAboutGitSSLCAInfo(t *testing.T) {
	srv := startSyncHub(t)
	ada := srv.Participant(t, "ada", "Ada Lovelace", human)
	writeDoc(t, ada, "documents/plan/budget.md", "# Budget\n")
	u := syncMember(t, srv, "grace", "Grace Hopper")
	dir := filepath.Join(u.work, "artifacts")
	other := filepath.Join(u.work, "other-ca.pem")
	if err := os.WriteFile(other, []byte("not the certificate of the hub\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Not set: no warning. (The host variable is cleared for the run.)
	clear := append([]string{"GIT_SSL_CAINFO="}, u.env...)
	_, errOut, code := contracttest.RunIn(t, u.work, "office", clear, nil, "sync", "init", dir)
	if code != 0 || strings.Contains(errOut, "GIT_SSL_CAINFO") {
		t.Fatalf("init without the variable: exit %d\n%s", code, errOut)
	}
	// Set to the certificate that the clone has: no warning.
	same := append([]string{"GIT_SSL_CAINFO=" + srv.CertFile}, u.env...)
	_, errOut, code = contracttest.RunIn(t, dir, "office", same, nil, "sync")
	if code != 0 || strings.Contains(errOut, "GIT_SSL_CAINFO") {
		t.Errorf("sync with the same certificate: exit %d\n%s", code, errOut)
	}
	// Set to another file: a warning in plain words, and the commands still work.
	env := append([]string{"GIT_SSL_CAINFO=" + other}, u.env...)
	for _, args := range [][]string{{"sync", "init", dir}, {"sync"}} {
		_, errOut, code = contracttest.RunIn(t, dir, "office", env, nil, args...)
		if code != 0 {
			t.Errorf("office %v: exit %d\n%s", args, code, errOut)
		}
		for _, w := range []string{"warning", "GIT_SSL_CAINFO=" + other, "Plain Git", "Unset the variable"} {
			if !strings.Contains(errOut, w) {
				t.Errorf("office %v: the warning has no %q:\n%s", args, w, errOut)
			}
		}
	}
	// A new clone gets the warning too.
	dir2 := filepath.Join(u.work, "second")
	_, errOut, code = contracttest.RunIn(t, u.work, "office", env, nil, "sync", "init", dir2)
	if code != 0 || !strings.Contains(errOut, "GIT_SSL_CAINFO") {
		t.Errorf("init of a new clone: exit %d\n%s", code, errOut)
	}
}

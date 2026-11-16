package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrsimpson/agentic-office/client/sync/control"
)

// mainDirectory checks that dir is the main directory of a clone that Init set up and returns its
// absolute path. A linked worktree has a file ".git"; the main directory has a directory.
func (s *syncer) mainDirectory(git, dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(filepath.Join(abs, ".git"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("%w: %s holds no Git repository; run \"office sync init %s\" first", control.ErrNotSetUp, abs, abs)
	case err != nil:
		return "", err
	case !fi.IsDir():
		return "", fmt.Errorf("%w: %s; \"office sync\" works on the main directory only, the one that \"office sync init\" set up", control.ErrLinkedWorktree, abs)
	}
	out, _ := run(context.Background(), git, "", "config", "--file", filepath.Join(abs, ".git", "config"), "--get", marker)
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("%w: %s is a Git repository that \"office sync init\" did not set up; run \"office sync init %s\" first", control.ErrNotSetUp, abs, abs)
	}
	return abs, nil
}

// Update implements control.Sync.
func (s *syncer) Update(ctx context.Context, dir string) (control.UpdateResult, error) {
	git, err := s.gitPath()
	if err != nil {
		return control.UpdateResult{}, err
	}
	abs, err := s.mainDirectory(git, dir)
	if err != nil {
		return control.UpdateResult{}, err
	}
	return s.update(ctx, git, abs)
}

// query runs a read-only Git command in dir and returns its trimmed output; ok is false if Git
// exits with an error.
func query(ctx context.Context, git, dir string, args ...string) (string, bool) {
	out, err := run(ctx, git, dir, args...)
	return strings.TrimSpace(out), err == nil
}

const remoteMain = "refs/remotes/origin/main"

func short(id string) string {
	if len(id) > 7 {
		return id[:7]
	}
	return id
}

// update fetches and moves main forward. The only commands that change anything are "git fetch"
// and "git merge --ff-only"; the others read.
func (s *syncer) update(ctx context.Context, git, dir string) (control.UpdateResult, error) {
	res := control.UpdateResult{Dir: dir}
	if branch, _ := query(ctx, git, dir, "symbolic-ref", "-q", "--short", "HEAD"); branch != "main" {
		if branch == "" {
			branch = "no branch"
		}
		return res, fmt.Errorf("the main directory %s is on %s, not on main: switch back with \"git switch main\" and run the command again", dir, branch)
	}
	before, _ := query(ctx, git, dir, "rev-parse", "--verify", "-q", remoteMain)
	if out, err := run(ctx, git, dir, "fetch", "--quiet", "origin"); err != nil {
		return res, fetchError(out, err)
	}
	after, ok := query(ctx, git, dir, "rev-parse", "--verify", "-q", remoteMain)
	if !ok {
		return res, fmt.Errorf("%w: the hub has no branch main for you yet (no artifacts)", control.ErrFetch)
	}
	head, _ := query(ctx, git, dir, "rev-parse", "--verify", "-q", "HEAD")
	res.From, res.To = head, head
	if head == after {
		return res, nil
	}
	ancestor := func(a, b string) bool {
		if a == "" {
			return false
		}
		_, ok := query(ctx, git, dir, "merge-base", "--is-ancestor", a, b)
		return ok
	}
	if head != "" && !ancestor(head, after) {
		switch {
		case before != "" && before != after && !ancestor(before, after):
			return res, fmt.Errorf("%w: the commit %s that main had is not part of the new history %s of the hub. "+
				"Nothing was changed. Ask who rewrote the history; to take the version of the hub, clone it again into a new directory",
				control.ErrHistoryRewritten, short(before), short(after))
		case ancestor(after, head):
			return res, fmt.Errorf("%w: main is ahead of the hub (main is %s, the hub has %s). Nothing was changed. "+
				"The main directory mirrors the hub: move your commits to a branch in a worktree and push that branch",
				control.ErrOwnCommits, short(head), short(after))
		default:
			return res, fmt.Errorf("main (%s) and the hub (%s) went different ways, so it is not a fast-forward: "+
				"%w, or %w. Nothing was changed. Move your own commits to a branch in a worktree; if the hub rewrote the history, clone it again into a new directory",
				short(head), short(after), control.ErrOwnCommits, control.ErrHistoryRewritten)
		}
	}
	if out, err := run(ctx, git, dir, "merge", "--ff-only", "--quiet", remoteMain); err != nil {
		if strings.Contains(out, "would be overwritten") {
			return res, fmt.Errorf("%w: commit or move these files, then run the command again. Nothing was changed.\n%s",
				control.ErrLocalChanges, strings.TrimSpace(out))
		}
		return res, fmt.Errorf("move main forward: %s (%v)", strings.TrimSpace(out), err)
	}
	res.Updated = true
	res.To, _ = query(ctx, git, dir, "rev-parse", "--verify", "-q", "HEAD")
	return res, nil
}

// fetchError explains a failed fetch in plain words. The error wraps control.ErrFetch.
func fetchError(out string, err error) error {
	low := strings.ToLower(out)
	var why string
	switch {
	case strings.Contains(low, "authentication failed") || strings.Contains(low, "401"):
		why = "the hub did not accept the access token: check your key with \"office whoami\""
	case strings.Contains(low, "not found") || strings.Contains(low, "404"):
		why = "the hub has no repository for you: the tenant does not exist or you are not a member of it"
	case strings.Contains(low, "certificate") || strings.Contains(low, "ssl"):
		why = "the certificate of the hub is not trusted: " + strings.TrimSpace(out)
	default:
		why = strings.TrimSpace(out)
		if why == "" {
			why = err.Error()
		}
	}
	return fmt.Errorf("%w: %s", control.ErrFetch, why)
}

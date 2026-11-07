package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
)

// Pusher pushes the local repository to a remote after a commit.
type Pusher interface {
	// Push sends the commits of the repository in dir to the remote.
	Push(ctx context.Context, dir string) error
}

// NoPush is the mocked Pusher: it does nothing and reports success (label "mocked", #43).
type NoPush struct{}

func (NoPush) Push(context.Context, string) error { return nil }

// Options configure an archive.
type Options struct {
	// Dir is the directory of the Git repository. It is created if it does not exist.
	Dir string
	// Push pushes after every commit. Nil means NoPush.
	Push Pusher
	// Logf logs errors that do not fail a commit. Nil means log.Printf.
	Logf func(format string, args ...any)
}

// Repo is an archive in a local Git repository. It implements storage.Archive.
type Repo struct {
	dir  string
	git  string // path of the git binary
	push Pusher
	logf func(string, ...any)

	mu sync.Mutex // one change at a time
}

var _ storage.Archive = (*Repo)(nil)

// Open opens the repository in opts.Dir and creates it if needed.
func Open(ctx context.Context, opts Options) (*Repo, error) {
	if opts.Dir == "" {
		return nil, errors.New("archive: no directory")
	}
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, err
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("archive: the git command line tool is needed: %w", err)
	}
	r := &Repo{dir: dir, git: git, push: opts.Push, logf: opts.Logf}
	if r.push == nil {
		r.push = NoPush{}
	}
	if r.logf == nil {
		r.logf = log.Printf
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("archive: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := r.run(ctx, "init", "-q", "-b", "main"); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("archive: %w", err)
	}
	// Only the hub writes the repository. A lock file left by a hub that died during a commit
	// would block every later commit.
	if err := os.Remove(filepath.Join(dir, ".git", "index.lock")); err == nil {
		r.logf("archive: removed the lock file of an interrupted commit in %s", dir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("archive: %w", err)
	}
	return r, nil
}

// Dir returns the directory of the repository.
func (r *Repo) Dir() string { return r.dir }

// run runs git in the repository and returns its standard output.
func (r *Repo) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, r.git, args...)
	cmd.Dir = r.dir
	dieWithHub(cmd)
	// No system or global configuration of the user: no hooks, no signing, no pager, no editor.
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=office-hub", "GIT_AUTHOR_EMAIL=office-hub@localhost",
		"GIT_COMMITTER_NAME=office-hub", "GIT_COMMITTER_EMAIL=office-hub@localhost")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return out.String(), &gitError{args: args, err: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return out.String(), nil
}

type gitError struct {
	args   []string
	err    error
	stderr string
}

func (e *gitError) Error() string {
	return fmt.Sprintf("archive: git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

// exitCode returns the exit code of a failed git run, or -1.
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// Commit implements storage.Archive.
func (r *Repo) Commit(ctx context.Context, c storage.Change) (string, error) {
	if strings.TrimSpace(c.Message) == "" {
		return "", errors.New("archive: a change needs a message")
	}
	seen := map[string]bool{}
	for _, f := range c.Files {
		if err := storage.CheckPath(f.Path); err != nil {
			return "", err
		}
		if seen[f.Path] {
			return "", fmt.Errorf("archive: path %q is twice in one change", f.Path)
		}
		seen[f.Path] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	paths := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		if err := writeFile(filepath.Join(r.dir, filepath.FromSlash(f.Path)), f.Data); err != nil {
			return "", fmt.Errorf("archive: %w", err)
		}
		paths = append(paths, f.Path)
	}
	if len(paths) > 0 {
		if _, err := r.run(ctx, append([]string{"add", "--"}, paths...)...); err != nil {
			return "", err
		}
	}
	// Exit code 1: something is staged, also from an earlier change whose commit failed.
	if _, err := r.run(ctx, "diff", "--cached", "--quiet"); err == nil {
		return r.head(ctx)
	} else if exitCode(err) != 1 {
		return "", err
	}
	if _, err := r.run(ctx, "commit", "-q", "--no-verify", "-m", c.Message); err != nil {
		return "", err
	}
	id, err := r.head(ctx)
	if err != nil {
		return "", err
	}
	if err := r.push.Push(ctx, r.dir); err != nil {
		r.logf("archive: push of %s failed, the next push takes it along: %v", id, err)
	}
	return id, nil
}

// head returns the ID of the current commit, or "" in a repository without commits.
func (r *Repo) head(ctx context.Context) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		if exitCode(err) == 1 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// writeFile replaces the file at p with data: it writes a temporary file and renames it.
func writeFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

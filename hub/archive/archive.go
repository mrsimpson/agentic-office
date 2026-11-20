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
	"slices"
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

	turn sync.Mutex // one change at a time

	changes changeLog // the followers of main (if-artifact-changes)
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
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); errors.Is(err, os.ErrNotExist) {
		if _, err := r.run(ctx, "init", "-q", "--bare", "-b", "main"); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("archive: %w", err)
	}
	// Only the hub writes the repository. Files that a hub left when it died during a commit
	// would block every later commit.
	for _, f := range []string{"refs/heads/main.lock", "HEAD.lock", "packed-refs.lock", indexFile} {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(f))); err == nil {
			r.logf("archive: removed %s of an interrupted commit in %s", f, dir)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("archive: %w", err)
		}
	}
	head, err := r.head(ctx)
	if err != nil {
		return nil, err
	}
	r.changes.reset(head)
	return r, nil
}

// indexFile is the temporary index of a commit, in the repository directory. The repository has
// no work tree; a commit uses the index only to build the new tree.
const indexFile = "office-index"

// maxAttempts is how often a commit builds its change on a new state of main when main moved
// in the meantime.
const maxAttempts = 20

// Dir returns the directory of the repository.
func (r *Repo) Dir() string { return r.dir }

// run runs git in the repository and returns its standard output.
func (r *Repo) run(ctx context.Context, args ...string) (string, error) {
	out, err := r.runInput(ctx, nil, args...)
	return string(out), err
}

// runInput runs git in the repository with stdin and returns its standard output.
func (r *Repo) runInput(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	return r.runEnv(ctx, stdin, nil, args...)
}

// runEnv is runInput with more environment variables; they come last and win.
func (r *Repo) runEnv(ctx context.Context, stdin []byte, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.git, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	cmd.Dir = r.dir
	dieWithHub(cmd)
	// No system or global configuration of the user: no hooks, no signing, no pager, no editor.
	cmd.Env = append(os.Environ(),
		"GIT_DIR="+r.dir, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0="+os.DevNull, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=office-hub", "GIT_AUTHOR_EMAIL=office-hub@localhost",
		"GIT_COMMITTER_NAME=office-hub", "GIT_COMMITTER_EMAIL=office-hub@localhost")
	cmd.Env = append(cmd.Env, env...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return out.Bytes(), &gitError{args: args, err: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return out.Bytes(), nil
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
	if err := checkChange(c); err != nil {
		return "", err
	}
	r.turn.Lock()
	defer r.turn.Unlock()
	return r.commitLocked(ctx, c, nil)
}

// checkChange checks the message, the paths and the author of a change.
func checkChange(c storage.Change) error {
	if strings.TrimSpace(c.Message) == "" {
		return errors.New("archive: a change needs a message")
	}
	seen := map[string]bool{}
	for _, f := range c.Files {
		if err := storage.CheckPath(f.Path); err != nil {
			return err
		}
		if seen[f.Path] {
			return fmt.Errorf("archive: path %q is twice in one change", f.Path)
		}
		seen[f.Path] = true
	}
	if a := c.Author; a != (storage.Author{}) && (a.Name == "" || a.Email == "") {
		return errors.New("archive: an author needs a name and an email")
	}
	return nil
}

// commitLocked carries out a change that checkChange accepted, or a change of the archive itself
// whose paths may lie in a directory that starts with a dot (the state of the staging areas). The
// caller holds r.turn. beforeRef, if not nil, runs with the ID of every commit that is built,
// before main moves to it; if it fails, main does not move.
func (r *Repo) commitLocked(ctx context.Context, c storage.Change, beforeRef func(id string) error) (string, error) {
	// Write the files as objects once; a retry reuses them. A removed file has no object.
	blobs := make([]string, len(c.Files))
	for i, f := range c.Files {
		if f.Remove {
			continue
		}
		out, err := r.runInput(ctx, f.Data, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		blobs[i] = strings.TrimSpace(string(out))
	}
	old, err := r.head(ctx)
	if err != nil {
		return "", err
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := r.checkBases(ctx, old, c.Files); err != nil {
			return "", err
		}
		if len(c.Files) == 0 && old == "" {
			return "", nil
		}
		tree, err := r.buildTree(ctx, old, c.Files, blobs)
		if err != nil {
			return "", err
		}
		if old != "" {
			if cur, err := r.rev(ctx, old+"^{tree}"); err != nil {
				return "", err
			} else if cur == tree {
				return old, nil // the change alters nothing
			}
		}
		args := []string{"commit-tree", tree}
		if old != "" {
			args = append(args, "-p", old)
		}
		var env []string
		if a := c.Author; a != (storage.Author{}) {
			env = []string{"GIT_AUTHOR_NAME=" + a.Name, "GIT_AUTHOR_EMAIL=" + a.Email}
		}
		out, err := r.runEnv(ctx, nil, env, append(args, "-m", c.Message)...)
		if err != nil {
			return "", err
		}
		id := strings.TrimSpace(string(out))
		if beforeRef != nil {
			if err := beforeRef(id); err != nil {
				return "", err
			}
		}
		// The one atomic step: main moves to the new commit only if it is still at old. An empty
		// old value means that main must not exist yet.
		if _, err := r.run(ctx, "update-ref", "-m", "office-hub commit", "refs/heads/main", id, old); err != nil {
			now, herr := r.head(ctx)
			if herr != nil {
				return "", errors.Join(err, herr)
			}
			if now == old {
				return "", err // main did not move: another problem
			}
			// Main moved in the meantime. A file of this change that someone else changed in
			// between is a conflict; otherwise build the same change on the new state.
			if err := r.checkMoved(ctx, old, now, c.Files, blobs); err != nil {
				return "", err
			}
			old = now
			continue
		}
		r.moved(old, id)
		if err := r.push.Push(ctx, r.dir); err != nil {
			r.logf("archive: push of %s failed, the next push takes it along: %v", id, err)
		}
		return id, nil
	}
	return "", fmt.Errorf("archive: main moved %d times during the commit; giving up", maxAttempts)
}

// rev returns the object ID that the revision expression names, or "" if there is none.
func (r *Repo) rev(ctx context.Context, expr string) (string, error) {
	out, err := r.run(ctx, "rev-parse", "--verify", "-q", expr)
	if err != nil {
		if exitCode(err) == 1 || exitCode(err) == 128 {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// buildTree returns the ID of the tree of commit old with the files replaced. It uses a
// temporary index file and no work tree.
func (r *Repo) buildTree(ctx context.Context, old string, files []storage.File, blobs []string) (string, error) {
	index := "GIT_INDEX_FILE=" + filepath.Join(r.dir, indexFile)
	defer os.Remove(filepath.Join(r.dir, indexFile))
	if old != "" {
		if _, err := r.runEnv(ctx, nil, []string{index}, "read-tree", old); err != nil {
			return "", err
		}
	}
	var entries bytes.Buffer
	for i, f := range files {
		if f.Remove {
			// Mode 0 removes the path from the index.
			fmt.Fprintf(&entries, "0 %s\t%s\x00", strings.Repeat("0", 40), f.Path)
			continue
		}
		fmt.Fprintf(&entries, "100644 %s\t%s\x00", blobs[i], f.Path)
	}
	if entries.Len() > 0 {
		if _, err := r.runEnv(ctx, entries.Bytes(), []string{index}, "update-index", "--add", "--replace", "-z", "--index-info"); err != nil {
			return "", err
		}
	}
	out, err := r.runEnv(ctx, nil, []string{index}, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// checkMoved fails with a *ConflictError when a file of the change differs between the commit
// the change was built on (old) and the new current commit, unless the new version is the
// content of the change itself.
func (r *Repo) checkMoved(ctx context.Context, old, now string, files []storage.File, blobs []string) error {
	for i, f := range files {
		was, err := r.rev(ctx, old+":"+f.Path)
		if old == "" {
			was = ""
		} else if err != nil {
			return err
		}
		cur, err := r.rev(ctx, now+":"+f.Path)
		if err != nil {
			return err
		}
		if cur != was && cur != blobs[i] {
			return &storage.ConflictError{Path: f.Path, Current: cur}
		}
	}
	return nil
}

// checkBases compares the Base of every file with its version in the commit old ("" for none).
func (r *Repo) checkBases(ctx context.Context, old string, files []storage.File) error {
	for _, f := range files {
		if f.Base == "" {
			continue
		}
		cur := ""
		if old != "" {
			var err error
			if cur, err = r.rev(ctx, old+":"+f.Path); err != nil {
				return err
			}
		}
		if (f.Base == storage.NoFile && cur != "") || (f.Base != storage.NoFile && f.Base != cur) {
			return &storage.ConflictError{Path: f.Path, Current: cur}
		}
	}
	return nil
}

// List implements storage.Archive.
func (r *Repo) List(ctx context.Context, dir string) ([]string, error) {
	paths, err := dirPaths(dir)
	if err != nil {
		return nil, err
	}
	head, err := r.head(ctx)
	if err != nil || head == "" {
		return nil, err
	}
	list, err := r.run(ctx, append([]string{"ls-tree", "-r", "-z", "--name-only", "--full-tree", head, "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range strings.Split(list, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

// Names implements storage.Archive. It asks git for the files that a commit of the history of
// main added to the directory (git log with the filter "added"). Renames are not detected, so a
// file that was moved into the directory counts as added there. No content is read.
func (r *Repo) Names(ctx context.Context, dir string) ([]string, error) {
	if err := storage.CheckPath(dir); err != nil {
		return nil, err
	}
	head, err := r.head(ctx)
	if err != nil || head == "" {
		return nil, err
	}
	out, err := r.run(ctx, "log", "-z", "--no-renames", "--diff-filter=A", "--name-only", "--format=", head, "--", dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			names = append(names, p)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// Read implements storage.Archive. It lists the files of dir (or reads the file dir) in the current commit and reads
// their content in one more run of git. It does not wait for a running change.
func (r *Repo) Read(ctx context.Context, dir string) ([]storage.File, error) {
	paths, err := dirPaths(dir)
	if err != nil {
		return nil, err
	}
	head, err := r.head(ctx)
	if err != nil || head == "" {
		return nil, err
	}
	return r.readAt(ctx, head, paths...)
}

// dirPaths returns the paths for git ls-tree that name dir: none for the whole repository ("").
func dirPaths(dir string) ([]string, error) {
	if dir == storage.Root {
		return nil, nil
	}
	if err := storage.CheckPath(dir); err != nil {
		return nil, err
	}
	return []string{dir}, nil
}

// readAt returns the files under the paths in the commit head, ordered by path.
func (r *Repo) readAt(ctx context.Context, head string, paths ...string) ([]storage.File, error) {
	// One entry per file: "<mode> <type> <object>\t<path>", separated by NUL.
	list, err := r.run(ctx, append([]string{"ls-tree", "-r", "-z", "--full-tree", head, "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	var files []storage.File
	var objects bytes.Buffer
	for _, entry := range strings.Split(list, "\x00") {
		meta, p, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 || f[1] != "blob" {
			continue
		}
		files = append(files, storage.File{Path: p, Version: f[2]})
		objects.WriteString(f[2] + "\n")
	}
	if len(files) == 0 {
		return nil, nil
	}
	out, err := r.runInput(ctx, objects.Bytes(), "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	// The output has per object a line "<object> blob <size>", the content and a newline.
	for i := range files {
		line, rest, ok := bytes.Cut(out, []byte("\n"))
		f := strings.Fields(string(line))
		var size int
		if ok && len(f) == 3 && f[1] == "blob" {
			_, err = fmt.Sscan(f[2], &size)
		}
		if !ok || len(f) != 3 || f[1] != "blob" || err != nil || len(rest) < size+1 {
			return nil, fmt.Errorf("archive: unexpected output of git cat-file for %s", files[i].Path)
		}
		files[i].Data = rest[:size:size]
		out = rest[size+1:]
	}
	slices.SortFunc(files, func(a, b storage.File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// firstCommit gives main its first commit if it has none: an empty commit that holds no file, by
// office-hub. The artifact repository of a tenant starts with it, so main exists from the start.
// The followers are not told: the state of main at start is not reported.
func (r *Repo) firstCommit(ctx context.Context) error {
	r.turn.Lock()
	defer r.turn.Unlock()
	old, err := r.head(ctx)
	if err != nil || old != "" {
		return err
	}
	tree, err := r.run(ctx, "mktree")
	if err != nil {
		return err
	}
	out, err := r.run(ctx, "commit-tree", strings.TrimSpace(tree), "-m", "Create the artifact repository")
	if err != nil {
		return err
	}
	id := strings.TrimSpace(out)
	if _, err := r.run(ctx, "update-ref", "-m", "office-hub first commit", "refs/heads/main", id, ""); err != nil {
		return err
	}
	r.changes.reset(id)
	return nil
}

// head returns the ID of the commit that main points to, or "" in a repository without commits.
func (r *Repo) head(ctx context.Context) (string, error) {
	return r.rev(ctx, "refs/heads/main")
}

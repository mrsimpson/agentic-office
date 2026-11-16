package sync

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/sync/control"
	gitweb "github.com/mrsimpson/agentic-office/hub/gitaccess/web"
)

// marker is the key in the own configuration of the clone that remembers the directory. Its value
// is the address of the repository.
const marker = "office.sync"

// guardMarker is the line in the hook that tells that this program wrote it.
const guardMarker = "# office-sync-guard"

// guardHook refuses a commit in the main directory. The main directory is the primary working
// tree: there the Git directory and the common Git directory are the same. A linked worktree
// ("git worktree add") has its own Git directory.
const guardHook = `#!/bin/sh
` + guardMarker + `
# Written by "office sync init". It keeps the main directory a mirror of the hub.
git_dir=$(cd "$(git rev-parse --git-dir)" && pwd -P)
common_dir=$(cd "$(git rev-parse --git-common-dir)" && pwd -P)
if [ "$git_dir" = "$common_dir" ]; then
  cat >&2 <<'EOF'
office: no commit in the main directory of the artifacts.
The main directory mirrors the hub. Work in a worktree instead:
  git worktree add ../my-change -b my-change
Commit there, then push the branch.
EOF
  exit 1
fi
`

type syncer struct {
	opts control.Options
}

// New returns a Sync (control.New).
func New(o control.Options) control.Sync { return &syncer{opts: o} }

var _ control.New = New

func (s *syncer) logf(format string, args ...any) {
	if s.opts.Logf != nil {
		s.opts.Logf(format, args...)
	}
}

// repository returns the address of the repository of the tenant: the hub address, the path prefix
// of Git access and the tenant. It holds no token.
func repository(cfg coreapi.Config) (string, error) {
	if cfg.Tenant == "" {
		return "", errors.New("no tenant: name the tenant of the artifacts with --tenant, OFFICE_TENANT or a hub profile")
	}
	return strings.TrimRight(cfg.Hub, "/") + gitweb.PathPrefix + url.PathEscape(cfg.Tenant), nil
}

func (s *syncer) gitPath() (string, error) {
	if s.opts.Git != "" {
		return s.opts.Git, nil
	}
	p, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("%w: install Git and make sure \"git\" is in PATH", control.ErrNoGit)
	}
	return p, nil
}

// Init implements control.Sync.
func (s *syncer) Init(ctx context.Context, dir string) (control.Result, error) {
	c := s.opts.Client
	cfg := c.Config()
	repo, err := repository(cfg)
	if err != nil {
		return control.Result{}, err
	}
	git, err := s.gitPath()
	if err != nil {
		return control.Result{}, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return control.Result{}, err
	}
	res := control.Result{Dir: abs, Repository: repo}

	existing, err := s.inspect(git, abs, repo)
	if err != nil {
		return res, err
	}

	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return res, fmt.Errorf("sign in to the hub with your key: %w", err)
	}
	name, err := c.DisplayName(ctx)
	if err != nil {
		return res, err
	}
	if name == "" {
		name = pid
	}
	email := pid + "@office.invalid"
	res.Author = name + " <" + email + ">"
	conf, err := s.settings(cfg, repo, name, email)
	if err != nil {
		return res, err
	}

	if !existing {
		s.logf("cloning %s into %s", repo, abs)
		args := []string{"clone", "--branch", "main", "--single-branch"}
		for _, kv := range conf {
			args = append(args, "-c", kv[0]+"="+kv[1])
		}
		args = append(args, "--", repo, abs)
		if out, err := run(ctx, git, "", args...); err != nil {
			return res, cloneError(repo, out, err)
		}
		res.Cloned = true
	} else {
		s.logf("%s is a clone of %s already; fixing its configuration", abs, repo)
	}
	if err := s.configure(ctx, git, abs, conf); err != nil {
		return res, err
	}
	return res, nil
}

// inspect checks the directory before anything is written. It returns true for a clone of this
// tenant that is set up already.
func (s *syncer) inspect(git, dir, repo string) (bool, error) {
	fi, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.IsDir() {
		return false, fmt.Errorf("%w: %s is a file, not a directory", control.ErrForeignDirectory, dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return false, nil
	}
	var names []string
	hasGit := false
	for _, e := range entries {
		names = append(names, e.Name())
		if e.Name() == ".git" {
			hasGit = true
			if !e.IsDir() {
				return false, fmt.Errorf("%w: %s is a linked worktree, not a main directory; set up the main directory instead",
					control.ErrForeignDirectory, dir)
			}
		}
	}
	if !hasGit {
		if len(names) > 5 {
			names = append(names[:5], "...")
		}
		return false, fmt.Errorf("%w: %s is not empty (%s) and is no clone; use an empty directory or a new name",
			control.ErrForeignDirectory, dir, strings.Join(names, ", "))
	}
	out, _ := run(context.Background(), git, "", "config", "--file", filepath.Join(dir, ".git", "config"), "--get", "remote.origin.url")
	if got := strings.TrimSpace(out); got != repo {
		if got == "" {
			got = "no remote"
		}
		return false, fmt.Errorf("%w: %s is a Git repository with %s, not a clone of %s",
			control.ErrForeignDirectory, dir, got, repo)
	}
	return true, nil
}

// settings returns the own configuration of the clone as key and value pairs. A key may repeat:
// the first credential.helper is empty and ends the helpers of the global configuration, so no
// other helper hands out an old token.
func (s *syncer) settings(cfg coreapi.Config, repo, name, email string) ([][2]string, error) {
	if len(s.opts.CredentialHelper) == 0 {
		return nil, errors.New("sync: no credential helper command")
	}
	conf := [][2]string{
		{"user.name", name},
		{"user.email", email},
		{"credential.helper", ""},
		{"credential.helper", "!" + shellJoin(s.opts.CredentialHelper)},
		{marker, repo},
	}
	if cfg.CACert != "" {
		ca, err := filepath.Abs(cfg.CACert)
		if err != nil {
			return nil, err
		}
		conf = append(conf, [2]string{"http.sslCAInfo", ca})
	}
	return conf, nil
}

// configure writes conf into the own configuration of the clone and the guard into its hooks
// directory. It touches no file of the working tree.
func (s *syncer) configure(ctx context.Context, git, dir string, conf [][2]string) error {
	file := filepath.Join(dir, ".git", "config")
	set := func(args ...string) error {
		out, err := run(ctx, git, "", append([]string{"config", "--file", file}, args...)...)
		if err != nil {
			return fmt.Errorf("write the configuration of the clone: %s", firstNonEmpty(strings.TrimSpace(out), err.Error()))
		}
		return nil
	}
	done := map[string]bool{}
	for _, kv := range conf {
		k := kv[0]
		if !done[k] {
			done[k] = true
			// Exit code 5 means that the key was not set; that is fine.
			_, _ = run(ctx, git, "", "config", "--file", file, "--unset-all", k)
		}
		if err := set("--add", k, kv[1]); err != nil {
			return err
		}
	}
	if !done["http.sslCAInfo"] {
		_, _ = run(ctx, git, "", "config", "--file", file, "--unset-all", "http.sslCAInfo")
	}

	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return err
	}
	hook := filepath.Join(hooks, "pre-commit")
	if old, err := os.ReadFile(hook); err == nil && !bytes.Contains(old, []byte(guardMarker)) {
		return fmt.Errorf("%s exists and was not written by \"office sync init\"; move it away and run the command again", hook)
	}
	if err := os.WriteFile(hook, []byte(guardHook), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(hook, 0o755); err != nil {
		return err
	}
	// A hooks path in the global configuration would hide the hook. Point this clone at its own
	// hooks directory then.
	if out, _ := run(ctx, git, dir, "config", "--get", "core.hooksPath"); strings.TrimSpace(out) != "" {
		return set("core.hooksPath", hooks)
	}
	return nil
}

// Credential implements control.Sync.
func (s *syncer) Credential(ctx context.Context, op string, in io.Reader, out io.Writer) error {
	attrs := map[string]string{}
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			attrs[k] = v
		}
	}
	if op != "get" {
		return nil
	}
	cfg := s.opts.Client.Config()
	hub, err := url.Parse(cfg.Hub)
	if err != nil {
		return err
	}
	// Never hand the token to another host.
	if attrs["host"] != hub.Host || (attrs["protocol"] != "" && attrs["protocol"] != hub.Scheme) {
		return nil
	}
	tok, err := s.opts.Client.AccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get an access token from the hub: %w", err)
	}
	_, err = fmt.Fprintf(out, "username=office\npassword=%s\n", tok)
	return err
}

// run runs a Git command and returns its output (standard output and error together). Git never
// asks on the terminal: the credential helper gives the token or the command fails.
func run(ctx context.Context, git, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	// End the command with SIGTERM, not SIGKILL, so that it removes its lock files.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	// GIT_SSL_CAINFO would override the certificate in the configuration of the clone.
	cmd.Env = []string{"GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_SSL_CAINFO=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// cloneError explains a failed clone in plain words.
func cloneError(repo, out string, err error) error {
	low := strings.ToLower(out)
	switch {
	case strings.Contains(low, "authentication failed") || strings.Contains(low, "401"):
		return fmt.Errorf("the hub did not accept the access token for %s: check your key with \"office whoami\"", repo)
	case strings.Contains(low, "not found") || strings.Contains(low, "404"):
		return fmt.Errorf("the hub has no repository at %s for you: the tenant does not exist or you are not a member of it", repo)
	case strings.Contains(low, "certificate") || strings.Contains(low, "ssl"):
		return fmt.Errorf("the certificate of the hub is not trusted: add the hub with \"office hub add\" or set --ca-cert: %s", strings.TrimSpace(out))
	}
	return fmt.Errorf("clone %s: %s (%v)", repo, strings.TrimSpace(out), err)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

var plainWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellJoin quotes the words for a POSIX shell.
func shellJoin(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		if plainWord.MatchString(w) {
			q[i] = w
			continue
		}
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

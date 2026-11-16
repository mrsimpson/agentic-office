package gitaccess

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mrsimpson/agentic-office/hub/archive/repository"
	"github.com/mrsimpson/agentic-office/hub/gitaccess/web"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
)

// TokenChecker is the token check (if-token-check) as Git access needs it: it returns the caller
// of an access token, or an error for an unknown, expired or revoked token.
type TokenChecker interface {
	CheckToken(token string) (tokencheck.Caller, error)
}

// Options configure the handler.
type Options struct {
	// Tokens checks the password of the request.
	Tokens TokenChecker
	// Repositories finds the artifact repository of a tenant.
	Repositories repository.Repositories
	// HooksDir is a directory that Git access owns. It writes the hook of Git into it when it
	// starts.
	HooksDir string
	// MaxPushBytes is the largest pack of one push. Zero means DefaultMaxPushBytes.
	MaxPushBytes int64
	// MaxFileBytes is the largest size of one artifact file. Zero means DefaultMaxFileBytes.
	MaxFileBytes int64
	// MaxFiles is the largest number of files that one push may change. Zero means
	// DefaultMaxFiles.
	MaxFiles int
	// Logf logs what Git writes to its error output. It may be nil.
	Logf func(format string, args ...any)
}

// The default limits of a push (risk-push-size).
const (
	DefaultMaxPushBytes = 32 << 20
	DefaultMaxFileBytes = 1 << 20
	DefaultMaxFiles     = 500
)

// Handler serves the repositories under web.PathPrefix.
type Handler struct {
	opts Options
	git  string
}

// New returns the handler. It fails if the "git" command line tool is not in PATH.
func New(o Options) (*Handler, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git access needs the git command line tool: %w", err)
	}
	if o.HooksDir == "" {
		return nil, errors.New("git access needs a directory for its hook")
	}
	if o.MaxPushBytes <= 0 {
		o.MaxPushBytes = DefaultMaxPushBytes
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if err := writeHook(o.HooksDir); err != nil {
		return nil, err
	}
	return &Handler{opts: o, git: git}, nil
}

const (
	uploadPack  = "git-upload-pack"
	receivePack = "git-receive-pack"
)

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, web.PathPrefix)
	if !ok {
		notFound(w)
		return
	}
	tenant, tail, _ := strings.Cut(rest, "/")

	// Gate 1: the token. The same answer for every failure.
	_, token, hasAuth := r.BasicAuth()
	if !hasAuth || token == "" {
		challenge(w)
		return
	}
	caller, err := h.opts.Tokens.CheckToken(token)
	if err != nil {
		challenge(w)
		return
	}
	// Gate 2: the tenant. A stage link token, a non-member and an unknown tenant look the same.
	if tenant == "" || caller.StageSessionID != "" {
		notFound(w)
		return
	}
	if _, member := caller.Member(tenant); !member {
		notFound(w)
		return
	}
	dir, err := h.opts.Repositories.Dir(tenant)
	if err != nil {
		if !errors.Is(err, repository.ErrNoTenant) && h.opts.Logf != nil {
			h.opts.Logf("git access: tenant %s: %v", tenant, err)
		}
		notFound(w)
		return
	}

	// Gate 3: only the smart protocol, for fetch and push. Git is never started for anything else.
	service := r.URL.Query().Get("service")
	push := false
	switch {
	case r.Method == http.MethodGet && tail == "info/refs" && service == receivePack,
		r.Method == http.MethodPost && tail == receivePack:
		push = true
	case r.Method == http.MethodGet && tail == "info/refs" && service == uploadPack,
		r.Method == http.MethodPost && tail == uploadPack:
	default:
		notFound(w)
		return
	}

	h.serveGit(w, r, tenant, dir, caller)
	if push && r.Method == http.MethodPost {
		// Git has finished the push. Tell the archive that main may have moved; it does
		// nothing if it did not. The request may be gone already, so it gets its own context.
		if err := h.opts.Repositories.MainMoved(context.WithoutCancel(r.Context()), tenant); err != nil && h.opts.Logf != nil {
			h.opts.Logf("git access: tenant %s: report of the push: %v", tenant, err)
		}
	}
}

// serveGit hands the request to git http-backend. GIT_PROJECT_ROOT is the repository of the
// tenant, and only the two addresses above reach it.
func (h *Handler) serveGit(w http.ResponseWriter, r *http.Request, tenant, dir string, caller tokencheck.Caller) {
	req := r.Clone(r.Context())
	req.Header.Del("Authorization")
	req.Header.Del("Proxy-Authorization")
	cfg := [][2]string{
		{"core.hooksPath", h.opts.HooksDir},
		{"http.receivepack", "true"},
		{"http.uploadpack", "true"},
		// Only the branch main is served.
		{"uploadpack.hideRefs", "refs/"},
		{"uploadpack.hideRefs", "!refs/heads/main"},
		{"receive.hideRefs", "refs/"},
		{"receive.hideRefs", "!refs/heads/main"},
		{"receive.denyNonFastForwards", "true"},
		{"receive.denyDeletes", "true"},
		{"receive.fsckObjects", "true"},
		{"receive.maxInputSize", strconv.FormatInt(h.opts.MaxPushBytes, 10)},
	}
	name := caller.DisplayName
	if name == "" {
		name = caller.ParticipantID
	}
	env := []string{
		"GIT_PROJECT_ROOT=" + dir,
		"GIT_HTTP_EXPORT_ALL=1",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		// The participant and the tenant come from the token check and the address, never from
		// the client. The hook reads them from here.
		"REMOTE_USER=" + caller.ParticipantID,
		"GIT_COMMITTER_NAME=" + name,
		"GIT_COMMITTER_EMAIL=" + AuthorEmail(caller.ParticipantID),
		EnvTenant + "=" + tenant,
		EnvParticipant + "=" + caller.ParticipantID,
		EnvName + "=" + name,
		EnvMaxFileBytes + "=" + strconv.FormatInt(h.opts.MaxFileBytes, 10),
		EnvMaxFiles + "=" + strconv.Itoa(h.opts.MaxFiles),
		fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(cfg)),
	}
	for i, kv := range cfg {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
	}
	c := &cgi.Handler{
		Path:       h.git,
		Args:       []string{"http-backend"},
		Root:       strings.TrimSuffix(web.PathPrefix, "/") + "/" + tenant,
		Dir:        dir,
		Env:        env,
		InheritEnv: []string{"PATH"},
	}
	if h.opts.Logf != nil {
		c.Stderr = logWriter{h.opts.Logf, tenant}
	}
	c.ServeHTTP(w, req)
}

type logWriter struct {
	logf   func(string, ...any)
	tenant string
}

func (l logWriter) Write(p []byte) (int, error) {
	l.logf("git access: tenant %s: %s", l.tenant, strings.TrimSpace(string(p)))
	return len(p), nil
}

func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Agentic office", charset="UTF-8"`)
	http.Error(w, "authentication needed: the password is your access token", http.StatusUnauthorized)
}

func notFound(w http.ResponseWriter) { http.Error(w, "not found", http.StatusNotFound) }

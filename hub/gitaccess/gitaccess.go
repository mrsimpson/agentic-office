package gitaccess

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
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
	// Logf logs what Git writes to its error output. It may be nil.
	Logf func(format string, args ...any)
}

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

	// Gate 3: only the fetch of the smart protocol. Git is never started for anything else.
	service := r.URL.Query().Get("service")
	switch {
	case r.Method == http.MethodGet && tail == "info/refs" && service == receivePack,
		r.Method == http.MethodPost && tail == receivePack:
		http.Error(w, "push is not allowed", http.StatusForbidden)
		return
	case r.Method == http.MethodGet && tail == "info/refs" && service == uploadPack,
		r.Method == http.MethodPost && tail == uploadPack:
	default:
		notFound(w)
		return
	}

	h.serveGit(w, r, tenant, dir)
}

// serveGit hands the request to git http-backend. GIT_PROJECT_ROOT is the repository of the
// tenant, and only the two addresses above reach it.
func (h *Handler) serveGit(w http.ResponseWriter, r *http.Request, tenant, dir string) {
	req := r.Clone(r.Context())
	req.Header.Del("Authorization")
	req.Header.Del("Proxy-Authorization")
	cfg := [][2]string{
		{"core.hooksPath", os.DevNull},
		{"http.receivepack", "false"},
		{"http.uploadpack", "true"},
		// Only the branch main is served.
		{"uploadpack.hideRefs", "refs/"},
		{"uploadpack.hideRefs", "!refs/heads/main"},
	}
	env := []string{
		"GIT_PROJECT_ROOT=" + dir,
		"GIT_HTTP_EXPORT_ALL=1",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
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

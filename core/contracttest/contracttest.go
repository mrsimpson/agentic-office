// Package contracttest is the harness for black-box contract tests (dec-contract-tests).
//
// A contract test starts a building block as a real process (or a fake that follows the same
// contract) and talks to it only through its provided interfaces. It must not import the
// packages that implement the block.
//
// Binaries are taken from $OFFICE_BIN_DIR (set by the Makefile). If it is not set, the harness
// builds them once per test binary with "go build".
package contracttest

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/mrsimpson/agentic-office/core/api/office/v1/officev1connect"
)

var (
	buildMu  sync.Mutex
	built    = map[string]string{}
	buildDir string
)

// Binary returns the path of a command from cmd/, for example "office-server".
func Binary(t testing.TB, name string) string {
	t.Helper()
	if dir := os.Getenv("OFFICE_BIN_DIR"); dir != "" {
		p, err := filepath.Abs(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("binary %s not found in OFFICE_BIN_DIR: %v (run make build)", name, err)
		}
		return p
	}
	buildMu.Lock()
	defer buildMu.Unlock()
	if p, ok := built[name]; ok {
		return p
	}
	if buildDir == "" {
		d, err := os.MkdirTemp("", "office-contracttest-")
		if err != nil {
			t.Fatal(err)
		}
		buildDir = d
	}
	root := moduleRoot(t)
	p := filepath.Join(buildDir, name)
	cmd := exec.Command("go", "build", "-o", p, "./cmd/"+name)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", name, err, out)
	}
	built[name] = p
	return p
}

func moduleRoot(t testing.TB) string {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

// Process is a running building block.
type Process struct {
	Cmd    *exec.Cmd
	Stdout *bufio.Reader

	mu     sync.Mutex
	stderr bytes.Buffer
	done   chan struct{}
}

// Start runs a binary from cmd/ with extra environment variables. The process is killed when
// the test ends.
func Start(t testing.TB, name string, env []string, args ...string) *Process {
	t.Helper()
	cmd := exec.Command(Binary(t, name), args...)
	cmd.Env = append(os.Environ(), env...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &Process{Cmd: cmd, Stdout: bufio.NewReader(stdout), done: make(chan struct{})}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			p.mu.Lock()
			p.stderr.Write(buf[:n])
			p.mu.Unlock()
			if err != nil {
				break
			}
		}
		_ = cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		p.Kill()
		if t.Failed() {
			t.Logf("stderr of %s:\n%s", name, p.Stderr())
		}
	})
	return p
}

// Stderr returns everything the process wrote to stderr so far.
func (p *Process) Stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr.String()
}

// Done is closed when the process has exited.
func (p *Process) Done() <-chan struct{} { return p.done }

// Kill stops the process with SIGKILL and waits for it.
func (p *Process) Kill() {
	_ = p.Cmd.Process.Signal(syscall.SIGKILL)
	<-p.done
}

// Stop sends SIGTERM and waits up to timeout. It returns the exit code, or an error on timeout.
func (p *Process) Stop(timeout time.Duration) (int, error) {
	_ = p.Cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
		return p.Cmd.ProcessState.ExitCode(), nil
	case <-time.After(timeout):
		return -1, errors.New("process did not stop in time")
	}
}

// WaitStderr waits until stderr matches re and returns the first submatch (or the match).
func (p *Process) WaitStderr(t testing.TB, re *regexp.Regexp, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m := re.FindStringSubmatch(p.Stderr()); m != nil {
			return m[len(m)-1]
		}
		select {
		case <-p.done:
			t.Fatalf("process exited before %q appeared:\n%s", re, p.Stderr())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("timeout waiting for %q:\n%s", re, p.Stderr())
	return ""
}

var listening = regexp.MustCompile(`listening on (http://\S+)`)

// Server is a running office-server.
type Server struct {
	*Process
	URL string
}

// StartServer starts office-server on a free port and waits until it listens.
func StartServer(t testing.TB, env []string, args ...string) *Server {
	t.Helper()
	p := Start(t, "office-server", env, append([]string{"-addr", "127.0.0.1:0"}, args...)...)
	return &Server{Process: p, URL: p.WaitStderr(t, listening, 10*time.Second)}
}

// Clients holds a Connect client for every office interface.
type Clients struct {
	Session  officev1connect.SessionServiceClient
	Identity officev1connect.IdentityServiceClient
	Journal  officev1connect.JournalServiceClient
	Artifact officev1connect.ArtifactServiceClient
}

// NewClients returns clients for baseURL. They use the Connect protocol over HTTP/1.1.
func NewClients(baseURL string) Clients {
	hc := &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	c := connect.NewClient(connecthttp.NewTransport(hc, baseURL))
	return Clients{
		Session:  officev1connect.NewSessionServiceClient(c),
		Identity: officev1connect.NewIdentityServiceClient(c),
		Journal:  officev1connect.NewJournalServiceClient(c),
		Artifact: officev1connect.NewArtifactServiceClient(c),
	}
}

// StartFake serves a fake in this process, for blocks whose test needs another block.
// register adds the fake handlers, for example officev1connect.RegisterSessionServiceHandler.
func StartFake(t testing.TB, register func(*connect.Server)) string {
	t.Helper()
	cs := connect.NewServer()
	register(cs)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, cs)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// Run runs a binary to completion and returns stdout, stderr and the exit code.
func Run(t testing.TB, name string, env []string, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, Binary(t, name), args...)
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatalf("run %s: %v", name, err)
	}
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}

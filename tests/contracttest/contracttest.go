// Package contracttest is the harness for black-box contract tests (dec-contract-tests).
//
// A contract test starts a building block as a real process (or a fake that follows the same
// contract) and talks to it only through its provided interfaces. It must not import the
// packages that implement the block.
//
// StartHub starts a real office-hub with TLS (a self-signed certificate in a temporary data
// directory), an admin key and a signing key file that the harness creates. Server.Participant registers a key for a test participant and
// returns an Actor with authenticated clients; Server.Anonymous returns clients without a token.
//
// Binaries are taken from $OFFICE_BIN_DIR (set by the Makefile). If it is not set, the harness
// builds them once per test binary with "go build".
package contracttest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
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

	"github.com/mrsimpson/agentic-office/client/core/ids"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	identityv1connect "github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
	decisionv1connect "github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	minutesv1connect "github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	notev1connect "github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	sessionv1connect "github.com/mrsimpson/agentic-office/hub/session/api/sessionv1connect"
	sessionimportv1connect "github.com/mrsimpson/agentic-office/hub/session/importapi/sessionimportv1connect"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
	taskv1connect "github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
)

var (
	buildMu  sync.Mutex
	built    = map[string]string{}
	buildDir string
)

// Binary returns the path of a command from cmd/, for example "office-hub".
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
	// Stdin is the standard input of the process, for example to send keys.
	Stdin io.WriteCloser

	mu     sync.Mutex
	stderr bytes.Buffer
	done   chan struct{}
}

// Start runs a binary from cmd/ with extra environment variables. The process is killed when
// the test ends.
func Start(t testing.TB, name string, env []string, args ...string) *Process {
	t.Helper()
	return StartIn(t, "", name, env, args...)
}

// StartIn is Start in the working directory dir ("" for the directory of the test).
func StartIn(t testing.TB, dir, name string, env []string, args ...string) *Process {
	t.Helper()
	cmd := exec.Command(Binary(t, name), args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Dir = dir
	// An own pipe for stdout, not StdoutPipe: Wait closes the pipes it created, and output that
	// the test has not read yet would be lost when the process exits.
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = stdoutW
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	err = cmd.Start()
	stdoutW.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdout.Close() })
	p := &Process{Cmd: cmd, Stdout: bufio.NewReader(stdout), Stdin: stdin, done: make(chan struct{})}
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

var listening = regexp.MustCompile(`listening on (https://\S+)`)

// AdminID is the participant ID of the admin that StartHub creates.
const AdminID = "admin"

// Server is a running office-hub with TLS and an admin key.
type Hub struct {
	*Process
	URL string
	// DataDir is the data directory of the server.
	DataDir string
	// CertFile is the self-signed TLS certificate of the server. Clients trust it.
	CertFile string
	// AdminKey is the private key of the admin (participant AdminID).
	AdminKey ed25519.PrivateKey
	// SigningKeyFile is the signing key file that the hub gets with -signing-key.
	SigningKeyFile string
}

// SigningKeyFile returns the signing key file of a hub with the data directory dir. StartHub
// creates it there; the hub itself does not know this name and gets it with -signing-key.
func SigningKeyFile(dir string) string { return filepath.Join(dir, "test-signing.key") }

// StartHub starts office-hub on a free port and waits until it listens. It creates a data
// directory with an admin key and a signing key file (SigningKeyFile). args are extra flags, for example "-token-ttl", "2s".
func StartHub(t testing.TB, env []string, args ...string) *Hub {
	t.Helper()
	dir := t.TempDir()
	key, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	line := AdminID + " " + auth.FormatPublicKey(auth.PublicKey(key)) + " Test Admin\n"
	if err := os.WriteFile(filepath.Join(dir, "admins"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	signing, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.WriteKey(SigningKeyFile(dir), signing); err != nil {
		t.Fatal(err)
	}
	return StartHubIn(t, dir, key, env, args...)
}

// StartHubIn starts office-hub with an existing data directory, for example to restart a
// server. adminKey is the key of AdminID in the admin file of dir. The hub gets the signing key
// file of dir (SigningKeyFile), so a restart signs with the same key.
func StartHubIn(t testing.TB, dir string, adminKey ed25519.PrivateKey, env []string, args ...string) *Hub {
	t.Helper()
	p := Start(t, "office-hub", env, append([]string{"-addr", "127.0.0.1:0", "-data-dir", dir,
		"-signing-key", SigningKeyFile(dir)}, args...)...)
	// Stop the hub gracefully first, so it ends its last write to the archive before the data
	// directory is removed. Start's cleanup kills it if it does not stop in time.
	t.Cleanup(func() { _, _ = p.Stop(10 * time.Second) })
	return &Hub{Process: p, URL: p.WaitStderr(t, listening, 10*time.Second), DataDir: dir,
		CertFile: filepath.Join(dir, "tls.crt"), AdminKey: adminKey, SigningKeyFile: SigningKeyFile(dir)}
}

// HTTPClient returns an HTTP client that trusts the certificate of the server.
func (s *Hub) HTTPClient(t testing.TB) *http.Client {
	t.Helper()
	hc, err := auth.HTTPClient(s.CertFile)
	if err != nil {
		t.Fatal(err)
	}
	return hc
}

// Anonymous returns clients without a token.
func (s *Hub) Anonymous(t testing.TB) Clients {
	return NewClients(s.URL, s.HTTPClient(t))
}

// WithToken returns clients that send a fixed token, for example the token of a stage link.
func (s *Hub) WithToken(t testing.TB, token string) Clients {
	return NewClients(s.URL, s.HTTPClient(t), auth.StaticToken(token))
}

// Actor is a participant with its own key and authenticated clients.
type Actor struct {
	Clients
	ParticipantID string
	Key           ed25519.PrivateKey
	KeyID         string
	Tokens        *auth.TokenSource
}

// WithKey returns an actor that authenticates with key. The key must be registered.
func (s *Hub) WithKey(t testing.TB, key ed25519.PrivateKey) *Actor {
	t.Helper()
	hc := s.HTTPClient(t)
	ts := auth.NewTokenSource(NewClients(s.URL, hc).Identity, key)
	a := &Actor{Clients: NewClients(s.URL, hc, auth.Bearer(ts)), Key: key,
		KeyID: auth.KeyID(auth.PublicKey(key)), Tokens: ts}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pid, err := ts.ParticipantID(ctx)
	if err != nil {
		t.Fatalf("authenticate key %s: %v", a.KeyID, err)
	}
	a.ParticipantID = pid
	return a
}

// Admin returns the admin actor.
func (s *Hub) Admin(t testing.TB) *Actor { return s.WithKey(t, s.AdminKey) }

// RegisterKey registers key for a participant as the admin and returns the key ID.
func (s *Hub) RegisterKey(t testing.TB, participantID, displayName string, kind identityv1.ActorKind, key ed25519.PrivateKey) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := s.Admin(t).Identity.RegisterKey(ctx, &identityv1.RegisterKeyRequest{CommandId: ids.NewCommandID(),
		ParticipantId: participantID, DisplayName: displayName, Kind: kind, PublicKey: auth.PublicKey(key)})
	if err != nil {
		t.Fatalf("register key of %s: %v", participantID, err)
	}
	return res.GetKeyId()
}

// Participant registers a new key for a participant and returns the authenticated actor. The
// participant is created if it is new; participantID can be called several times (several
// keys of one participant).
func (s *Hub) Participant(t testing.TB, participantID, displayName string, kind identityv1.ActorKind) *Actor {
	t.Helper()
	key, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	s.RegisterKey(t, participantID, displayName, kind, key)
	return s.WithKey(t, key)
}

// Clients holds a Connect client for every office interface.
type Clients struct {
	Session  sessionv1connect.SessionServiceClient
	Import   sessionimportv1connect.SessionImportServiceClient
	Identity identityv1connect.IdentityServiceClient
	Decision decisionv1connect.DecisionServiceClient
	Minutes  minutesv1connect.MinutesServiceClient
	Note     notev1connect.NoteServiceClient
	Task     taskv1connect.TaskServiceClient
	Slides   slidesv1connect.SlidesServiceClient
}

// NewClients returns clients for baseURL that use hc (nil: a new default client) and the
// interceptors, for example auth.Bearer. They use the Connect protocol.
func NewClients(baseURL string, hc *http.Client, interceptors ...connect.ClientInterceptor) Clients {
	if hc == nil {
		hc = &http.Client{Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	c := connect.NewClient(connecthttp.NewTransport(hc, baseURL), interceptors...)
	return Clients{
		Session:  sessionv1connect.NewSessionServiceClient(c),
		Import:   sessionimportv1connect.NewSessionImportServiceClient(c),
		Identity: identityv1connect.NewIdentityServiceClient(c),
		Decision: decisionv1connect.NewDecisionServiceClient(c),
		Minutes:  minutesv1connect.NewMinutesServiceClient(c),
		Note:     notev1connect.NewNoteServiceClient(c),
		Task:     taskv1connect.NewTaskServiceClient(c),
		Slides:   slidesv1connect.NewSlidesServiceClient(c),
	}
}

// StartFake serves a fake in this process, for blocks whose test needs another block.
// register adds the fake handlers, for example sessionv1connect.RegisterSessionServiceHandler.
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
	return RunStdin(t, name, env, nil, args...)
}

// RunStdin is Run with a standard input.
func RunStdin(t testing.TB, name string, env []string, stdin io.Reader, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, Binary(t, name), args...)
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr, cmd.Stdin = &out, &errb, stdin
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatalf("run %s: %v", name, err)
	}
	return out.String(), errb.String(), cmd.ProcessState.ExitCode()
}

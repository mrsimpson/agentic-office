package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"sync"

	"github.com/mrsimpson/agentic-office/client/mcp/tools"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/client/replicator/control"

	"connectrpc.com/connect/v2"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is the MCP server of one actor (one key). It serves the tools over one MCP
// connection and runs one replicator per joined session in its own process.
type Server struct {
	// Config tells where the server is, which key to use and where the local files live.
	Config coreapi.Config
	// Session is the default session of the tools (--session, OFFICE_SESSION). Empty: the only
	// session this participant joined from this machine.
	Session string
	// Version is reported to the MCP client.
	Version string
	// Stderr receives the log of the server and of its replicators. Nil means os.Stderr.
	Stderr io.Writer
	// Dial dials the office hub (if-client-core).
	Dial coreapi.Dial
	// NewReplicator creates the replicators of this process (if-replication).
	NewReplicator control.New

	log *log.Logger

	mu     sync.Mutex
	client coreapi.Client
	ctx    context.Context // lives as long as Run; the replicators run under it
	reps   map[string]*rep // by local log path
	wg     sync.WaitGroup
}

// rep is a replicator that runs in this process.
type rep struct {
	done chan struct{}
}

// Run serves MCP on in and out (stdio) until the client closes the connection or ctx ends.
// Then it stops all replicators of this process and waits for them.
func (s *Server) Run(ctx context.Context, in io.ReadCloser, out io.WriteCloser) error {
	if s.Stderr == nil {
		s.Stderr = os.Stderr
	}
	s.log = log.New(s.Stderr, "office mcp: ", log.LstdFlags|log.Lmicroseconds)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.ctx, s.reps = ctx, map[string]*rep{}
	s.mu.Unlock()

	srv := sdk.NewServer(&sdk.Implementation{Name: "agentic-office", Title: "Agentic office", Version: s.Version},
		&sdk.ServerOptions{Instructions: tools.Instructions})
	s.addTools(srv)
	srv.AddReceivingMiddleware(inputErrors)
	err := srv.Run(ctx, &sdk.IOTransport{Reader: in, Writer: out})
	cancel()
	s.wg.Wait()
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		err = nil
	}
	return err
}

// dial returns the client of this actor. A successful client is kept, so its token is reused.
func (s *Server) dial() (coreapi.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	c, err := s.Dial(s.Config)
	if err != nil {
		return nil, err
	}
	s.client = c
	return c, nil
}

// sessionID returns the session argument, else the default session.
func (s *Server) sessionID(ctx context.Context, c coreapi.Client, arg string) (string, error) {
	if arg != "" {
		return arg, nil
	}
	if s.Session != "" {
		return s.Session, nil
	}
	sid, err := c.DefaultSession(ctx)
	if errors.Is(err, coreapi.ErrNoSession) {
		// The message of the core names the CLI flag; name the tool argument instead.
		pid, _ := c.ParticipantID(ctx)
		return "", fmt.Errorf("%w: %s has not joined exactly one session on this machine; pass \"session\"", coreapi.ErrNoSession, pid)
	}
	return sid, err
}

// startReplicator makes sure that a replicator writes the local log of j. It returns the PID of
// the process that writes it: this process, or another one (for example a replicator that
// "office meeting join" started). The replicator of this process stops when Run ends, after
// session.ended, or after the participant's own participant.left.
func (s *Server) startReplicator(c coreapi.Client, j *coreapi.Joined) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := j.Paths
	if r, ok := s.reps[p.Log]; ok {
		select {
		case <-r.done:
		default:
			return os.Getpid(), nil
		}
	}
	r := s.NewReplicator(control.Options{Commands: c.Commands(), Stream: c.Stream(), SessionID: j.SessionID,
		ParticipantID: j.ParticipantID, Path: p.Log, Logf: s.log.Printf})
	if err := r.Open(); err != nil {
		if errors.Is(err, control.ErrRunning) {
			return readPID(p.PID), nil
		}
		return 0, err
	}
	_ = writePID(p.PID)
	s.log.Printf("replicator writes %s from cursor %d", p.Log, r.Cursor())
	rr := &rep{done: make(chan struct{})}
	s.reps[p.Log] = rr
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(rr.done)
		defer removePID(p.PID)
		reason, err := r.Run(s.ctx)
		if err != nil {
			s.log.Printf("replicator of %s stopped: %v", p.Log, err)
			return
		}
		s.log.Printf("replicator of %s stopped: %s", p.Log, reason)
	}()
	return os.Getpid(), nil
}

func readPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(string(trimNL(b)))
	return pid
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func writePID(path string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removePID(path string) {
	if readPID(path) == os.Getpid() {
		_ = os.Remove(path)
	}
}

// Errors of the tools have the form of tools.ErrorJSON, like the --json errors of the CLI.

// errUsage marks wrong tool arguments (code "usage").
type errUsage struct{ msg string }

func (e errUsage) Error() string { return e.msg }

func usagef(format string, args ...any) error { return errUsage{fmt.Sprintf(format, args...)} }

// errorCode returns the code of an error: the Connect code for errors of the server, else a code
// of the client (usage, no_key, no_session, error).
func errorCode(err error) string {
	var u errUsage
	var ce *connect.Error
	switch {
	case errors.As(err, &u), errors.Is(err, coreapi.ErrBadInput):
		return tools.CodeUsage
	case errors.Is(err, coreapi.ErrNoKey):
		return tools.CodeNoKey
	case errors.Is(err, coreapi.ErrNoSession):
		return tools.CodeNoSession
	case errors.As(err, &ce):
		return ce.Code().String()
	}
	return tools.CodeError
}

// errorResult is a tool result with IsError set and the error object as text and as structured
// content.
func errorResult(code, msg string) *sdk.CallToolResult {
	v := tools.ErrorJSON(code, msg)
	b, _ := json.Marshal(v)
	return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}, StructuredContent: v}
}

// okResult is a tool result with v as JSON text and as structured content.
func okResult(v any) *sdk.CallToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return errorResult(tools.CodeError, err.Error())
	}
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}, StructuredContent: json.RawMessage(b)}
}

// inputErrors turns the SDK's own errors for arguments that do not match the input schema into
// usage errors of the same form as all other errors.
func inputErrors(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		res, err := next(ctx, method, req)
		if method != "tools/call" || err != nil {
			return res, err
		}
		r, ok := res.(*sdk.CallToolResult)
		if !ok || !r.IsError || r.StructuredContent != nil {
			return res, err
		}
		msg := "invalid arguments"
		if len(r.Content) > 0 {
			if t, ok := r.Content[0].(*sdk.TextContent); ok {
				msg = t.Text
			}
		}
		return errorResult(tools.CodeUsage, msg), nil
	}
}

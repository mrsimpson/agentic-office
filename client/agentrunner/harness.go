package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// harness is a running agent harness and the ACP connection to it. The runner is the ACP client.
type harness struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	conn  *acp.ClientSideConnection
	cl    *acpClient
	done  chan struct{} // closed when the process has exited
	err   error         // exit error, valid after done
	sid   acp.SessionId
	logf  func(string, ...any)
}

// startHarness starts the harness command with its own process group, so that stopping it also
// stops its children (for example node under npx). Its stderr goes to stderr.
func startHarness(command []string, stderr io.Writer, logf func(string, ...any), allow permissionRule) (*harness, error) {
	if len(command) == 0 {
		return nil, errors.New("no harness command")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the harness %q: %w", command[0], err)
	}
	h := &harness{cmd: cmd, stdin: in, done: make(chan struct{}), logf: logf}
	h.cl = &acpClient{logf: logf, allow: allow, tools: map[acp.ToolCallId]toolInfo{}}
	h.conn = acp.NewClientSideConnection(h.cl, in, out)
	go func() {
		h.err = cmd.Wait()
		close(h.done)
	}()
	return h, nil
}

// open negotiates the protocol and opens one ACP session with the given MCP server.
func (h *harness) open(ctx context.Context, cwd string, office acp.McpServerStdio, version string) error {
	ini, err := h.call(ctx, func(ctx context.Context) (any, error) {
		return h.conn.Initialize(ctx, acp.InitializeRequest{
			ProtocolVersion:    acp.ProtocolVersionNumber,
			ClientCapabilities: acp.ClientCapabilities{}, // no file system, no terminal
			ClientInfo:         &acp.Implementation{Name: "agentic-office", Version: version},
		})
	})
	if err != nil {
		return fmt.Errorf("ACP initialize: %w", err)
	}
	ir := ini.(acp.InitializeResponse)
	if ir.ProtocolVersion != acp.ProtocolVersionNumber {
		return fmt.Errorf("the harness speaks ACP version %d; the runner speaks %d", ir.ProtocolVersion, acp.ProtocolVersionNumber)
	}
	name := "harness"
	if ir.AgentInfo != nil {
		name = ir.AgentInfo.Name + " " + ir.AgentInfo.Version
	}
	h.logf("ACP harness %s, protocol version %d", name, ir.ProtocolVersion)
	ns, err := h.call(ctx, func(ctx context.Context) (any, error) {
		return h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{{Stdio: &office}}})
	})
	if err != nil {
		if len(ir.AuthMethods) > 0 {
			return fmt.Errorf("ACP session/new: %w (the harness may need a login first; log in with the harness itself)", err)
		}
		return fmt.Errorf("ACP session/new: %w", err)
	}
	h.sid = ns.(acp.NewSessionResponse).SessionId
	h.logf("ACP session %s", h.sid)
	return nil
}

// prompt sends one prompt and waits for the end of the agent's turn. It returns the stop reason.
// If ctx ends first, it cancels the turn and waits a little for the harness to confirm.
func (h *harness) prompt(ctx context.Context, text string) (acp.StopReason, error) {
	h.cl.beginTurn()
	defer h.cl.endTurn()
	type result struct {
		r   acp.PromptResponse
		err error
	}
	res := make(chan result, 1)
	pctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	go func() {
		r, err := h.conn.Prompt(pctx, acp.PromptRequest{SessionId: h.sid, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
		res <- result{r, err}
	}()
	select {
	case r := <-res:
		return r.r.StopReason, r.err
	case <-h.done:
		return "", h.exitError()
	case <-ctx.Done():
	}
	_ = h.conn.Cancel(context.Background(), acp.CancelNotification{SessionId: h.sid})
	select {
	case r := <-res:
		return r.r.StopReason, ctx.Err()
	case <-h.done:
	case <-time.After(3 * time.Second):
	}
	return acp.StopReasonCancelled, ctx.Err()
}

// call runs f and gives up when the harness exits.
func (h *harness) call(ctx context.Context, f func(context.Context) (any, error)) (any, error) {
	type result struct {
		v   any
		err error
	}
	res := make(chan result, 1)
	go func() {
		v, err := f(ctx)
		res <- result{v, err}
	}()
	select {
	case r := <-res:
		return r.v, r.err
	case <-h.done:
		return nil, h.exitError()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *harness) exitError() error {
	if h.err != nil {
		return fmt.Errorf("the harness stopped: %w", h.err)
	}
	return errors.New("the harness stopped")
}

// stop ends the harness: it closes its stdin, then sends SIGTERM and at last SIGKILL to its
// process group.
func (h *harness) stop() {
	_ = h.stdin.Close()
	pgid := -h.cmd.Process.Pid
	for _, step := range []struct {
		wait time.Duration
		sig  syscall.Signal
	}{{3 * time.Second, syscall.SIGTERM}, {2 * time.Second, syscall.SIGKILL}} {
		select {
		case <-h.done:
			_ = syscall.Kill(pgid, syscall.SIGKILL) // children that are left
			return
		case <-time.After(step.wait):
		}
		_ = syscall.Kill(pgid, step.sig)
	}
	<-h.done
}

// toolInfo is what the client knows of a tool call from session/update.
type toolInfo struct {
	title string
	kind  acp.ToolKind
}

// acpClient is the client side of ACP: it answers the requests of the harness.
type acpClient struct {
	logf  func(string, ...any)
	allow permissionRule

	mu    sync.Mutex
	tools map[acp.ToolCallId]toolInfo
	text  strings.Builder // the agent's reply in the current turn, for the log
}

var _ acp.Client = (*acpClient)(nil)

func (c *acpClient) beginTurn() {
	c.mu.Lock()
	c.text.Reset()
	c.mu.Unlock()
}

func (c *acpClient) endTurn() {
	c.mu.Lock()
	t := strings.TrimSpace(c.text.String())
	c.text.Reset()
	c.mu.Unlock()
	if t != "" {
		c.logf("agent replied (not spoken): %s", oneLine(t, 300))
	}
}

func (c *acpClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	u := n.Update
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case u.AgentMessageChunk != nil:
		if t := u.AgentMessageChunk.Content.Text; t != nil {
			c.text.WriteString(t.Text)
		}
	case u.ToolCall != nil:
		c.tools[u.ToolCall.ToolCallId] = toolInfo{title: u.ToolCall.Title, kind: u.ToolCall.Kind}
		c.logf("tool call %s: %s", u.ToolCall.ToolCallId, u.ToolCall.Title)
	case u.ToolCallUpdate != nil:
		ti := c.tools[u.ToolCallUpdate.ToolCallId]
		if u.ToolCallUpdate.Title != nil {
			ti.title = *u.ToolCallUpdate.Title
		}
		if u.ToolCallUpdate.Kind != nil {
			ti.kind = *u.ToolCallUpdate.Kind
		}
		c.tools[u.ToolCallUpdate.ToolCallId] = ti
		if s := u.ToolCallUpdate.Status; s != nil && (*s == acp.ToolCallStatusCompleted || *s == acp.ToolCallStatusFailed) {
			c.logf("tool call %s %s", u.ToolCallUpdate.ToolCallId, *s)
		}
	}
	return nil
}

// RequestPermission answers by the permission rule: an allowed tool gets an allow option, every
// other tool a reject option (dec-agent-runner-delivery).
func (c *acpClient) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	ti := c.tools[p.ToolCall.ToolCallId]
	c.mu.Unlock()
	if p.ToolCall.Title != nil {
		ti.title = *p.ToolCall.Title
	}
	if p.ToolCall.Kind != nil {
		ti.kind = *p.ToolCall.Kind
	}
	allowed := c.allow.allows(ti.title, ti.kind)
	want := []acp.PermissionOptionKind{acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways}
	verb := "rejected"
	if allowed {
		want = []acp.PermissionOptionKind{acp.PermissionOptionKindAllowOnce, acp.PermissionOptionKindAllowAlways}
		verb = "allowed"
	}
	for _, k := range want {
		for _, o := range p.Options {
			if o.Kind == k {
				c.logf("permission %s: %q (%s)", verb, ti.title, ti.kind)
				return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
					Selected: &acp.RequestPermissionOutcomeSelected{OptionId: o.OptionId}}}, nil
			}
		}
	}
	c.logf("permission cancelled: %q (%s): no option to %s it", ti.title, ti.kind, strings.TrimSuffix(verb, "ed"))
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
}

var errNoCapability = errors.New("the agent runner offers no file system and no terminal")

func (c *acpClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errNoCapability
}

func (c *acpClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errNoCapability
}

func (c *acpClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errNoCapability
}

func (c *acpClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errNoCapability
}

func (c *acpClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errNoCapability
}

func (c *acpClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errNoCapability
}

func (c *acpClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errNoCapability
}

// permissionRule decides which tool calls of the harness the runner allows.
type permissionRule struct {
	// globs are patterns of the user ("*" matches any text), matched against the title of the
	// tool call without regard to case. With no globs, the default rule applies.
	globs []string
}

// officeTool matches the title of a call of an office tool, as harnesses name MCP tools: the
// server name "office", a separator and the tool name, for example "mcp__office__utterance_say"
// or "office_utterance_say".
var officeTool = regexp.MustCompile(`(?i)(^|[^a-z0-9])office[_\W]+(meeting|utterance|signal|hand|minutes|stage|slides|decision|note|task|session)_[a-z_]+|(^|[^a-z0-9])office[_\W]+whoami`)

// allows reports whether a tool call with this title and kind is allowed. The default rule
// allows the office tools only, and never a tool that executes, edits, deletes or moves.
func (r permissionRule) allows(title string, kind acp.ToolKind) bool {
	if len(r.globs) == 0 {
		switch kind {
		case acp.ToolKindExecute, acp.ToolKindEdit, acp.ToolKindDelete, acp.ToolKindMove:
			return false
		}
		return officeTool.MatchString(title)
	}
	for _, g := range r.globs {
		if globMatch(strings.ToLower(g), strings.ToLower(title)) {
			return true
		}
	}
	return false
}

// globMatch matches s against a pattern in which "*" stands for any text.
func globMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, p := range parts[1 : len(parts)-1] {
		i := strings.Index(s, p)
		if i < 0 {
			return false
		}
		s = s[i+len(p):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// absEnv returns the environment entry name=value.
func absEnv(name, value string) acp.EnvVariable { return acp.EnvVariable{Name: name, Value: value} }

// lookupOffice returns the path of the running office binary, for the MCP server.
func lookupOffice() (string, error) { return os.Executable() }

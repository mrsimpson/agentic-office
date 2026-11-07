//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/session/api/events"
	"github.com/mrsimpson/agentic-office/hub/session/api/logfile"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// TestAgentThroughMCP: a human uses the CLI, an agent uses only the MCP server. The agent
// follows its log with the filter that meeting_join describes (tail -f | grep), wakes up on
// turn.end, and answers through the tools. The human reads the answers in their own log.
func TestAgentThroughMCP(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	runtime := filepath.Join(t.TempDir(), "run")
	envFor := func(key string) []string {
		return []string{"OFFICE_HUB=" + srv.URL, "OFFICE_CA_CERT=" + srv.CertFile, "OFFICE_KEY=" + key,
			"OFFICE_RUNTIME_DIR=" + runtime, "OFFICE_SESSION=", "OFFICE_JSON="}
	}
	office := func(env []string, args ...string) map[string]any {
		t.Helper()
		out, errOut, code := contracttest.Run(t, "office", env, append(args, "--json")...)
		var v map[string]any
		if code != 0 || json.Unmarshal([]byte(out), &v) != nil {
			t.Fatalf("office %v: exit %d, %q, %q", args, code, out, errOut)
		}
		return v
	}
	adminKey := filepath.Join(t.TempDir(), "admin.pem")
	if err := auth.WriteKey(adminKey, srv.AdminKey); err != nil {
		t.Fatal(err)
	}
	admin := envFor(adminKey)
	user := func(pid, name, kind string) []string {
		env := envFor(filepath.Join(t.TempDir(), pid+".pem"))
		k := office(env, "key", "create")
		office(admin, "admin", "key", "register", pid, k["publicKey"].(string), "--name", name, "--kind", kind)
		return env
	}
	ada, bot := user("ada", "Ada", "human"), user("bot", "Bot", "agent")
	const sid = "weekly"

	// The agent's MCP client starts "office mcp".
	cmd := exec.Command(contracttest.Binary(t, "office"), "mcp")
	cmd.Env = append(os.Environ(), bot...)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", name, err, res)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	botLog := call("meeting_join", map[string]any{"session": sid})["logPath"].(string)
	adaJoin := office(ada, "meeting", "join", sid)
	adaLog := adaJoin["logPath"].(string)
	adaRep := int(adaJoin["replicatorPid"].(float64))
	t.Cleanup(func() { _ = syscall.Kill(adaRep, syscall.SIGKILL) })

	// The agent's background filter, as the tool description shows it.
	follow := exec.CommandContext(ctx, "sh", "-c", `tail -n +1 -f "$LOG" | grep --line-buffered '"type":"turn.end"\|"type":"addressed"'`)
	follow.Env = append(os.Environ(), "LOG="+botLog)
	out, err := follow.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	follow.Cancel = func() error { return follow.Process.Kill() }
	if err := follow.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = follow.Process.Kill(); _ = follow.Wait() })
	wake := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			wake <- sc.Text()
		}
	}()

	office(ada, "say", "Bot, what did we decide on the budget?")
	office(ada, "signal", "turn-end")
	select {
	case line := <-wake:
		ev, err := logfile.UnmarshalLine([]byte(line))
		if err != nil || ev.GetType() != events.TurnEnd || ev.GetTurnEnd().GetParticipantId() != "ada" {
			t.Fatalf("the filter woke the agent with %q (%v)", line, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the agent's filter did not see turn.end")
	}
	// The agent reads the context before the turn.end from the file, then answers.
	b, _ := os.ReadFile(botLog)
	if !strings.Contains(string(b), "what did we decide on the budget?") {
		t.Fatal("the question is not in the agent's log")
	}
	call("hand_raise", map[string]any{"topic": "budget"})
	answer := call("utterance_say", map[string]any{"text": "We agreed on the budget last week."})
	call("signal_turn_end", nil)
	call("minutes_update", map[string]any{"text": "Ada asked about the budget. Bot: agreed last week."})

	// Ada reads the answer in her own log.
	wantID := uint32(answer["event"].(map[string]any)["id"].(float64))
	deadline := time.Now().Add(10 * time.Second)
	for {
		found := false
		ab, _ := os.ReadFile(adaLog)
		for _, l := range strings.SplitAfter(string(ab), "\n") {
			if ev, err := logfile.UnmarshalLine([]byte(l)); strings.HasSuffix(l, "\n") && err == nil && ev.GetId() == wantID {
				if ev.GetUtteranceFinal().GetParticipantId() != "bot" {
					t.Fatalf("answer: %s", l)
				}
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ada did not get the agent's answer")
		}
		time.Sleep(20 * time.Millisecond)
	}

	office(ada, "meeting", "end")
	// Both logs end with session.ended and are equal.
	deadline = time.Now().Add(10 * time.Second)
	for {
		a, _ := os.ReadFile(adaLog)
		bb, _ := os.ReadFile(botLog)
		if len(a) > 0 && string(a) == string(bb) && strings.Contains(string(a), `"type":"session.ended"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the two local logs differ after session.ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The agent finds the stored minutes of the meeting.
	j := call("minutes_get", map[string]any{"session": sid})
	if mm, _ := j["minutes"].(map[string]any); mm["text"] != "Ada asked about the budget. Bot: agreed last week." {
		t.Fatalf("stored minutes: %v", j)
	}
	if err := cs.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 0 {
		t.Fatalf("office mcp did not exit cleanly: %v", cmd.ProcessState)
	}
}

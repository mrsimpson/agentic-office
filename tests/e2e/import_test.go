//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/logfile"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// TestImportedConversationToRecords (rs-import-conversation): a human imports a chat export with
// the CLI. An agent joins the imported session through MCP, reads the imported conversation from
// its local log, writes the minutes, records a decision and a task, and ends the session. The
// human reads the minutes, the decision and the task back with the CLI. The archive holds the
// session, marked as imported, with a tree head that covers it.
func TestImportedConversationToRecords(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-archive-interval", "1h")
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

	// A chat with 1200 messages: the client sends it in several pages.
	conv := map[string]any{"source": "Chat export, #planning", "title": "Release planning",
		"participants": []map[string]any{{"id": "kim", "name": "Kim", "kind": "human"}, {"id": "lee", "name": "Lee", "kind": "human"}}}
	var utts []map[string]any
	for i := range 1200 {
		speaker := []string{"kim", "lee"}[i%2]
		utts = append(utts, map[string]any{"speaker": speaker, "text": "Message " + strconv.Itoa(i+1) + ".",
			"time": time.Date(2026, 11, 2, 9, 0, i, 0, time.UTC).Format(time.RFC3339)})
	}
	utts[1199]["text"] = "We release on Monday."
	conv["utterances"] = utts
	b, _ := json.Marshal(conv)
	file := filepath.Join(t.TempDir(), "planning.json")
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	im := office(ada, "session", "import", file, "--session", "planning")
	if im["pages"] != 3.0 || im["lastEventId"] != float64(1+2+1200+1) {
		t.Fatalf("import: %v", im)
	}

	// The agent joins the imported session through MCP and reads it from its local log.
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
	logPath, _ := call("meeting_join", map[string]any{"session": "planning"})["logPath"].(string)
	var lastText string
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, _ := os.ReadFile(logPath)
		evs, _ := logfile.UnmarshalLines(data)
		if len(evs) >= 1205 { // the import and the join of the agent
			if evs[0].GetType() != events.SessionImported || evs[0].GetSessionImported().GetSource() != "Chat export, #planning" {
				t.Fatalf("first line of the log: %v", evs[0])
			}
			if evs[1203].GetType() != events.ImportCompleted {
				t.Fatalf("line 1204: %v", evs[1203])
			}
			lastText = evs[1202].GetUtteranceFinal().GetText()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the local log has %d events, want 1205", len(evs))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lastText != "We release on Monday." {
		t.Fatalf("last imported utterance %q", lastText)
	}
	call("minutes_update", map[string]any{"session": "planning", "text": "# Release planning\n\n- Release on Monday."})
	call("decision_record", map[string]any{"session": "planning", "text": "We release on Monday."})
	call("task_create", map[string]any{"session": "planning", "title": "Prepare the release notes", "assignee": "ada"})
	call("meeting_end", map[string]any{"session": "planning"})

	// The human reads the derived records.
	if m, _ := office(ada, "minutes", "show", "planning")["minutes"].(map[string]any); !strings.Contains(m["text"].(string), "Release on Monday") {
		t.Fatalf("minutes: %v", m)
	}
	if ds, _ := office(ada, "decision", "list", "--session", "planning")["decisions"].([]any); len(ds) != 1 {
		t.Fatalf("decisions: %v", ds)
	}
	if ts, _ := office(ada, "task", "list", "--session", "planning")["tasks"].([]any); len(ts) != 1 {
		t.Fatalf("tasks: %v", ts)
	}

	// The archive holds the ended session, marked as imported and covered by its tree head.
	a := contracttest.WaitArchived(t, srv.ArchiveDir(), "planning", 20*time.Second, (*contracttest.ArchivedSession).Ended)
	keyRes, err := srv.Admin(t).Commands.GetHubKey(ctx, &sessioncommandsv1.GetHubKeyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	a.Verify(t, "planning", keyRes.GetPublicKey())
	if a.Events[0].GetType() != events.SessionImported {
		t.Fatalf("archived session starts with %v", a.Events[0])
	}
}

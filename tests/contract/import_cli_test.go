package contract

// Contract tests of "office session import" (if-cli) and the MCP tool session_import (if-mcp):
// both read the import format (dec-import-format) and create a marked, imported session.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// conversationJSON is a conversation in the import format, as in README.md.
const conversationJSON = `{
  "source": "Slack #design, exported 2026-11-06",
  "title": "Design review",
  "participants": [
    {"id": "ada", "name": "Ada", "kind": "human"},
    {"id": "claude", "name": "Claude", "kind": "agent"}
  ],
  "utterances": [
    {"speaker": "ada", "text": "Shall we import old chats?", "time": "2026-11-06T10:00:00Z"},
    {"speaker": "claude", "text": "Yes, as marked sessions.", "time": "2026-11-06T10:00:30Z"},
    {"speaker": "ada", "text": "Decided.", "modality": "voice"}
  ]
}`

// eventTypes reads the events of a session with "office meeting events" and returns their types.
func eventTypes(t *testing.T, u *officeUser, sid string) ([]string, []map[string]any) {
	t.Helper()
	v := u.ok("meeting", "events", "--session", sid, "--limit", "1000")
	raw, _ := v["events"].([]any)
	var types []string
	var evs []map[string]any
	for _, e := range raw {
		m := e.(map[string]any)
		types = append(types, m["type"].(string))
		evs = append(evs, m)
	}
	return types, evs
}

var importedTypes = []string{events.SessionImported, events.ParticipantJoined, events.ParticipantJoined,
	events.UtteranceFinal, events.UtteranceFinal, events.UtteranceFinal, events.ImportCompleted}

func checkImported(t *testing.T, u *officeUser, sid string) {
	t.Helper()
	types, evs := eventTypes(t, u, sid)
	if strings.Join(types, " ") != strings.Join(importedTypes, " ") {
		t.Fatalf("events of the imported session: %v", types)
	}
	si, _ := evs[0]["sessionImported"].(map[string]any)
	if si["source"] != "Slack #design, exported 2026-11-06" || si["title"] != "Design review" {
		t.Errorf("session.imported: %v", evs[0])
	}
	uf, _ := evs[3]["utteranceFinal"].(map[string]any)
	if uf["participantId"] != "imported:ada" || uf["originalTime"] != "2026-11-06T10:00:00Z" || uf["modality"] != "MODALITY_TEXT" {
		t.Errorf("first utterance: %v", evs[3])
	}
	if uf, _ := evs[5]["utteranceFinal"].(map[string]any); uf["modality"] != "MODALITY_VOICE" {
		t.Errorf("voice utterance: %v", evs[5])
	}
}

// TestCLIImport: office session import reads a file or stdin, prints the new session ID, and
// --json gives the event range and the signed tree head. A file that is not in the format is a
// usage error, and nothing is imported.
func TestCLIImport(t *testing.T) {
	m := newMachine(t)
	ada := m.user("ada", "Ada", "human")
	file := filepath.Join(t.TempDir(), "conversation.json")
	if err := os.WriteFile(file, []byte(conversationJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	v := ada.ok("session", "import", file, "--session", "review-2026-11-06")
	if v["sessionId"] != "review-2026-11-06" || v["firstEventId"] != 1.0 || v["lastEventId"] != 7.0 ||
		v["utterances"] != 3.0 || v["participants"] != 2.0 || v["pages"] != 1.0 {
		t.Fatalf("import: %v", v)
	}
	if h, _ := v["treeHead"].(map[string]any); h["treeSize"] != 7.0 || h["sessionId"] != "review-2026-11-06" {
		t.Errorf("tree head: %v", v["treeHead"])
	}
	checkImported(t, ada, "review-2026-11-06")

	// Text output: the session ID on stdout; a new ID if none is given; stdin with "-".
	out, errOut, code := contracttest.RunStdin(t, "office", ada.env, strings.NewReader(conversationJSON), "session", "import", "-")
	sid := strings.TrimSpace(out)
	if code != 0 || !strings.HasPrefix(sid, "import-") || !strings.Contains(errOut, "imported 3 utterances of 2 participants") {
		t.Fatalf("import from stdin: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	checkImported(t, ada, sid)

	// The same session again: the hub refuses, because the session has events.
	ada.fails(1, "failed_precondition", "session", "import", file, "--session", "review-2026-11-06")
	// Bad input is a usage error.
	for name, body := range map[string]string{
		"not JSON":        "source: x",
		"unknown field":   `{"source": "x", "participants": [], "utterances": [], "speakers": []}`,
		"no source":       `{"participants": [], "utterances": []}`,
		"unknown speaker": `{"source": "x", "participants": [], "utterances": [{"speaker": "nobody", "text": "Hi."}]}`,
		"bad kind":        `{"source": "x", "participants": [{"id": "a", "name": "A", "kind": "robot"}], "utterances": []}`,
		"bad time":        `{"source": "x", "participants": [{"id": "a", "name": "A", "kind": "human"}], "utterances": [{"speaker": "a", "text": "Hi.", "time": "yesterday"}]}`,
	} {
		f := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		v, code := ada.call("session", "import", f, "--session", "bad")
		if e, _ := v["error"].(map[string]any); code != 2 || e["code"] != "usage" {
			t.Errorf("%s: want a usage error, got exit %d: %v", name, code, v)
		}
	}
	if types, _ := eventTypes(t, ada, "bad"); len(types) != 0 {
		t.Errorf("bad input was imported: %v", types)
	}
	ada.fails(2, "usage", "session", "import")
}

// TestMCPImport: session_import takes the conversation as an object or as a file and returns the
// same result as the CLI.
func TestMCPImport(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	s := startMCP(t, bot)
	var conv map[string]any
	if err := json.Unmarshal([]byte(conversationJSON), &conv); err != nil {
		t.Fatal(err)
	}
	v := s.ok("session_import", map[string]any{"conversation": conv, "session": "mcp-import"})
	if v["sessionId"] != "mcp-import" || v["lastEventId"] != 7.0 || v["utterances"] != 3.0 {
		t.Fatalf("session_import: %v", v)
	}
	checkImported(t, bot, "mcp-import")

	file := filepath.Join(t.TempDir(), "conversation.json")
	if err := os.WriteFile(file, []byte(conversationJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	v = s.ok("session_import", map[string]any{"file": file})
	sid, _ := v["sessionId"].(string)
	if !strings.HasPrefix(sid, "import-") {
		t.Fatalf("session_import from a file: %v", v)
	}
	checkImported(t, bot, sid)

	s.fails("usage", "session_import", map[string]any{})
	s.fails("usage", "session_import", map[string]any{"file": file, "conversation": conv})
	s.fails("usage", "session_import", map[string]any{"conversation": map[string]any{"source": "", "participants": []any{}, "utterances": []any{}}})
	s.fails("failed_precondition", "session_import", map[string]any{"conversation": conv, "session": "mcp-import"})
}

package contract

// The release of a session through the CLI and the MCP server (if-cli, if-mcp,
// concept-session-release). They pass the commands on to the hub and add no logic.

import (
	"encoding/json"
	"strings"
	"testing"
)

// A plain "meeting end" tells the humans how to release; "session hold" and "session release"
// pass the commands on, and the hub refuses an agent. "meeting end --release" ends and releases.
func TestReleaseWithTheCLI(t *testing.T) {
	m := newMachine(t)
	ada := m.user("ada", "Ada", "human")
	bot := m.user("bot", "Bot", "agent")
	sid := newSession()
	ada.join(sid)
	bot.join(sid)
	out, errOut, code := ada.run("meeting", "end", "--session", sid)
	if code != 0 || !strings.Contains(out, "session.ended") || !strings.Contains(out, "office session release "+sid) ||
		!strings.Contains(out, "office session hold "+sid) {
		t.Fatalf("meeting end: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	bot.fails(1, "permission_denied", "session", "release", sid)
	bot.fails(1, "permission_denied", "session", "hold", sid)
	if _, ev := eventOf(t, ada.ok("session", "hold", sid)); ev.GetType() != "release.objected" {
		t.Fatalf("session hold: %v", ev)
	}
	ada.fails(1, "failed_precondition", "session", "hold", sid)
	rel := ada.ok("session", "release", sid)
	if _, ev := eventOf(t, rel); ev.GetType() != "session.released" {
		t.Fatalf("session release: %v", rel)
	}
	if q, ok := rel["quotes"].([]any); !ok || len(q) != 0 {
		t.Errorf("quotes %v, want an empty list", rel["quotes"])
	}

	s2 := newSession()
	ada.join(s2)
	v := ada.ok("meeting", "end", "--release", "--session", s2)
	if r, _ := v["release"].(map[string]any); r["state"] != "waiting" || r["deadline"] == nil {
		t.Errorf("release status at the end: %v", v["release"])
	}
	if r, _ := v["released"].(map[string]any); r == nil {
		t.Fatalf("meeting end --release: %v", v)
	} else if _, ev := eventOf(t, r); ev.GetType() != "session.released" || ev.GetActorParticipantId() != "ada" {
		t.Errorf("released: %v", r)
	}

	// The MCP server offers the same: an agent is refused.
	s := startMCP(t, bot)
	s.fails("permission_denied", "session_release", map[string]any{"session": sid})
	s.fails("permission_denied", "session_hold", map[string]any{"session": sid})
	s3 := newSession()
	bot.join(s3)
	end := s.ok("meeting_end", map[string]any{"session": s3})
	if r, _ := end["release"].(map[string]any); r["state"] != "released" {
		t.Errorf("meeting_end of a session of agents: %v", end)
	}
}

// "session release" prints the places where staged files quote a sentence off the record and a
// token, and exits with 1; with --accept-quotes and that token it releases. With --json, the
// refusal is an error object with the code failed_precondition, the places and the token. The MCP
// tool session_release returns the same error object and takes the token as acceptQuotes.
func TestReleaseQuotesWithTheCLI(t *testing.T) {
	m := newMachine(t)
	ada := m.user("ada", "Ada", "human")
	sid := newSession()
	ada.join(sid)
	sentence := "Ben will leave the company in March."
	said := ada.ok("say", sentence, "--session", sid)
	_, ev := eventOf(t, said)
	ada.ok("utterance", "off-the-record", "--last", "--session", sid)
	ada.ok("document", "create", "documents/news.md", "# News\n\n"+sentence+"\n", "--session", sid)
	ada.ok("meeting", "end", "--session", sid)

	out, errOut, code := ada.run("session", "release", sid)
	if code != 1 || !strings.Contains(out, "documents/news.md:3") || !strings.Contains(out, "--accept-quotes ") || strings.Contains(out, sentence) {
		t.Fatalf("session release with quotes: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	refusal := func(what string, v map[string]any) string {
		t.Helper()
		e, _ := v["error"].(map[string]any)
		token, _ := e["quoteToken"].(string)
		if e["code"] != "failed_precondition" || token == "" || !strings.Contains(out, token) {
			t.Fatalf("%s: %v, want the error failed_precondition with the token of %q", what, v, out)
		}
		if q, _ := e["quotes"].([]any); len(q) != 1 || q[0].(map[string]any)["utteranceEventId"] != float64(ev.GetId()) ||
			q[0].(map[string]any)["path"] != "documents/news.md" {
			t.Errorf("%s: quotes %v, want the sentence of event %d", what, e["quotes"], ev.GetId())
		}
		if b, _ := json.Marshal(v); strings.Contains(string(b), sentence) {
			t.Errorf("%s names the sentence: %s", what, b)
		}
		return token
	}
	v, code := ada.call("session", "release", sid)
	if code != 1 || v["event"] != nil {
		t.Fatalf("session release --json with quotes: exit %d, %v", code, v)
	}
	token := refusal("session release --json", v)

	// The MCP tool returns an error with the places and the token.
	h := startMCP(t, ada)
	r, isErr := h.call("session_release", map[string]any{"session": sid})
	if !isErr {
		t.Fatalf("session_release with quotes: %v, want an error", r)
	}
	if got := refusal("session_release", r); got != token {
		t.Errorf("session_release gave the token %q, want %q", got, token)
	}

	// An agent cannot release, with or without the token.
	bot := m.user("bot", "Bot", "agent")
	s := startMCP(t, bot)
	s.fails("permission_denied", "session_release", map[string]any{"session": sid, "acceptQuotes": token})

	rel := ada.ok("session", "release", sid, "--accept-quotes", token)
	if _, ev := eventOf(t, rel); ev.GetType() != "session.released" {
		t.Fatalf("session release --accept-quotes: %v", rel)
	}
	if q, _ := rel["quotes"].([]any); len(q) != 1 {
		t.Errorf("accepted quotes %v, want one", rel["quotes"])
	}
}

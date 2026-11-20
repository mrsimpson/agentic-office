package contract

// The release of a session through the CLI and the MCP server (if-cli, if-mcp,
// concept-session-release). They pass the commands on to the hub and add no logic.

import (
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

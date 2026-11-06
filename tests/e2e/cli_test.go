//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
	"github.com/mrsimpson/agentic-office/core/auth"
	"github.com/mrsimpson/agentic-office/core/contracttest"
	"github.com/mrsimpson/agentic-office/core/eventlog"
	"github.com/mrsimpson/agentic-office/core/events"
)

// TestTwoCLIParticipants: a human and an agent on one machine join a session with the office
// CLI. The agent reacts to what it reads in its local log, only with CLI commands. Both local
// logs end with session.ended and are equal.
func TestTwoCLIParticipants(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	runtime := filepath.Join(t.TempDir(), "run")
	envFor := func(key string) []string {
		return []string{"OFFICE_SERVER=" + srv.URL, "OFFICE_CA_CERT=" + srv.CertFile, "OFFICE_KEY=" + key,
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
	join := func(env []string) (string, int) {
		v := office(env, "meeting", "join", sid)
		pid := int(v["replicatorPid"].(float64))
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		return v["logPath"].(string), pid
	}
	adaLog, adaRep := join(ada)
	botLog, botRep := join(bot)

	read := func(path string) []*officev1.Event {
		b, _ := os.ReadFile(path)
		var out []*officev1.Event
		for _, l := range strings.SplitAfter(string(b), "\n") {
			if ev, err := eventlog.UnmarshalLine([]byte(l)); strings.HasSuffix(l, "\n") && err == nil {
				out = append(out, ev)
			}
		}
		return out
	}
	waitFor := func(path, what string, match func(*officev1.Event) bool) *officev1.Event {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			for _, ev := range read(path) {
				if match(ev) {
					return ev
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timeout waiting for %s in %s", what, path)
		return nil
	}

	office(ada, "say", "Bot, can you show the budget?")
	office(ada, "signal", "turn-end")
	// The agent follows its log and wakes up on turn.end.
	turn := waitFor(botLog, "turn.end", func(ev *officev1.Event) bool { return ev.GetType() == events.TurnEnd })
	if turn.GetTurnEnd().GetParticipantId() != "ada" {
		t.Fatalf("turn.end of %q", turn.GetTurnEnd().GetParticipantId())
	}
	// The agent renders slides and shows them (rs-agent-shows-artifact).
	deck := filepath.Join(t.TempDir(), "budget.md")
	if err := os.WriteFile(deck, []byte("# Budget\n\n## Costs\n- Rent: 4,000 EUR\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifactID := office(bot, "slides", "render", deck)["artifactId"].(string)
	office(bot, "hand", "raise", "--topic", "budget")
	office(bot, "stage", "show", artifactID)
	office(bot, "minutes", "update", "Ada asked for the budget. Bot showed slides/1.")
	waitFor(adaLog, "hand.raised of bot", func(ev *officev1.Event) bool {
		return ev.GetType() == events.HandRaised && ev.GetHandRaised().GetParticipantId() == "bot"
	})
	// Ada reads the shown ID from her log and loads the slides.
	shown := waitFor(adaLog, "artifact.shown", func(ev *officev1.Event) bool { return ev.GetType() == events.ArtifactShown })
	if shown.GetArtifactShown().GetArtifactId() != "slides/1" {
		t.Fatalf("shown artifact %q, want slides/1", shown.GetArtifactShown().GetArtifactId())
	}
	html, errOut, code := contracttest.Run(t, "office", ada, "artifact", "get", shown.GetArtifactShown().GetArtifactId())
	if code != 0 || !strings.Contains(html, "<h2>Costs</h2>") || !strings.Contains(html, "<li>Rent: 4,000 EUR</li>") {
		t.Fatalf("artifact get: exit %d, %q, %q", code, html, errOut)
	}
	// The stage of the session, opened with the link from the CLI, shows the slides, the raised
	// hand and the minutes, and then the end of the session.
	stage := openStage(t, srv, office(ada, "stage", "link")["url"].(string))
	stage.waitFor(t, "slides, hand and minutes on the stage", `<iframe`, `data-artifact="slides/1"`,
		`data-participant="bot"`, `Ada asked for the budget.`)
	office(ada, "meeting", "end")
	stage.waitFor(t, "the end on the stage", `data-status="ended"`, "event: end")

	for _, pid := range []int{adaRep, botRep} {
		deadline := time.Now().Add(10 * time.Second)
		for syscall.Kill(pid, 0) == nil && !zombie(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("replicator %d did not stop after session.ended", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	a, b := read(adaLog), read(botLog)
	if len(a) == 0 || len(a) != len(b) || a[len(a)-1].GetType() != events.SessionEnded {
		t.Fatalf("logs: ada %d lines, bot %d lines", len(a), len(b))
	}
	ab, _ := os.ReadFile(adaLog)
	bb, _ := os.ReadFile(botLog)
	if string(ab) != string(bb) {
		t.Fatal("the two local logs differ")
	}

	// The minutes are in the journal (rs-session-ends), next to a decision of the agent.
	office(bot, "journal", "append", "--kind", "decision", "--session", sid, "Budget agreed.")
	list := office(ada, "journal", "list", "--session", sid)
	entries, _ := list["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("journal of the session: %v", list)
	}
	m, _ := entries[0].(map[string]any)
	d, _ := entries[1].(map[string]any)
	if m["kind"] != "ENTRY_KIND_MINUTES" || m["text"] != "Ada asked for the budget. Bot showed slides/1." ||
		m["authorParticipantId"] != "bot" || m["authorKind"] != "PARTICIPANT_KIND_AGENT" ||
		d["kind"] != "ENTRY_KIND_DECISION" || d["authorParticipantId"] != "bot" {
		t.Fatalf("journal of the session: %v", list)
	}
}

func zombie(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i > 0 && i+2 < len(s) && s[i+2] == 'Z'
}

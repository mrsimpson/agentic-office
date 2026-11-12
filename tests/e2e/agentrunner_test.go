//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/logfile"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// TestAgentThroughRunner: a human talks to an agent in plain words, without a keyword and
// without ending the turn. The agent runs in a harness (the ACP test double) that the agent
// runner drives with the wake strategy llm. An LLM judge (its test double) says after each
// utterance whether the agent should act; it wakes the agent in the middle of the monologue. The agent answers through the
// office MCP server, and the human reads the answer in their own log. When the human ends the
// meeting, the agent gets a last prompt and the runner stops.
func TestAgentThroughRunner(t *testing.T) {
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

	adaJoin := office(ada, "meeting", "join", sid)
	adaLog := adaJoin["logPath"].(string)
	adaRep := int(adaJoin["replicatorPid"].(float64))
	t.Cleanup(func() { _ = syscall.Kill(adaRep, syscall.SIGKILL) })

	rec := filepath.Join(t.TempDir(), "acp.jsonl")
	runner := contracttest.Start(t, "office", append(bot, "ACP_DOUBLE_RECORD="+rec), "agent", "run", "--session", sid,
		"--role", "You keep the minutes and answer questions.", "--wake", "llm", "--decide-cmd", contracttest.DecisionDouble(t),
		"--", contracttest.ACPDouble(t))
	contracttest.WaitACPRecord(t, rec, 20*time.Second, func(r contracttest.ACPRecord) bool { return r.Kind() == "prompt_end" })

	office(ada, "say", "We start with the release.", "--session", sid)
	office(ada, "say", `Bot, what did we decide? Could you please say "We ship on Friday."`, "--session", sid)
	waitFor := func(what string, cond func(string) bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			b, _ := os.ReadFile(adaLog)
			for _, l := range strings.SplitAfter(string(b), "\n") {
				if strings.HasSuffix(l, "\n") && cond(l) {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not reach Ada's log:\n%s\nrunner:\n%s", what, b, runner.Stderr())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitFor("the answer of the agent", func(l string) bool {
		ev, err := logfile.UnmarshalLine([]byte(l))
		return err == nil && ev.GetUtteranceFinal().GetParticipantId() == "bot" && ev.GetUtteranceFinal().GetText() == "We ship on Friday."
	})

	office(ada, "meeting", "end", "--session", sid)
	select {
	case <-runner.Done():
	case <-time.After(20 * time.Second):
		t.Fatalf("the runner did not stop after the end of the meeting:\n%s", runner.Stderr())
	}
	if code := runner.Cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("runner exit %d:\n%s", code, runner.Stderr())
	}
	var prompts []string
	for _, r := range contracttest.ReadACPRecords(t, rec) {
		if r.Kind() == "prompt" {
			prompts = append(prompts, r.Text())
		}
	}
	if len(prompts) != 3 || !strings.Contains(prompts[1], "Ada (human): We start with the release.") ||
		!strings.Contains(prompts[1], "Ada (human): Bot, what did we decide?") || !strings.Contains(prompts[2], "has ended") {
		t.Fatalf("prompts: %q", prompts)
	}
}

package contract

// Contract tests of the configuration files of the office CLI (if-cli,
// concept-client-configuration): the user file config.yaml in the configuration folder, the
// project file .office/config.yaml in the start directory, the environment and the flags, in
// this order from low to high. The tests run the real office binary with a temporary
// configuration folder (XDG_CONFIG_HOME) and a temporary start directory. The harness is the ACP
// test double; the decision model is the fake System One endpoint of
// agentrunner_systemone_test.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// userConfig writes the user file into a new configuration folder and returns the value for
// XDG_CONFIG_HOME. files are more files of the configuration folder, by relative path.
func userConfig(t *testing.T, content string, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "agentic-office", "client")
	writeFiles(t, dir, map[string]string{"config.yaml": content})
	writeFiles(t, dir, files)
	return home
}

// startDir returns a new start directory with the project file (if content is not empty) and
// more files, by relative path.
func startDir(t *testing.T, content string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if content != "" {
		writeFiles(t, dir, map[string]string{filepath.Join(".office", "config.yaml"): content})
	}
	writeFiles(t, dir, files)
	return dir
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// agentRunIn starts "office agent run" for u in the start directory dir, with the extra
// environment env and the arguments args (no harness is added). It returns the process and the
// record file of the ACP test double.
func (u *officeUser) agentRunIn(t *testing.T, dir string, env []string, args ...string) (*contracttest.Process, string) {
	t.Helper()
	rec := filepath.Join(t.TempDir(), "acp-record.jsonl")
	env = append(append(append([]string{}, u.env...), env...), "ACP_DOUBLE_RECORD="+rec, "ACP_DOUBLE_RELEASE="+rec+".release")
	return contracttest.StartIn(t, dir, "office", env, append([]string{"agent", "run"}, args...)...), rec
}

// yamlList is a YAML flow list of texts.
func yamlList(items ...string) string {
	b, _ := json.Marshal(items)
	return string(b)
}

// TestConfigUserFile: with only the user file, "office agent run" takes the session, the harness
// and the role file from it, with no flag and no command after "--". A relative path in the user
// file is relative to the configuration folder.
func TestConfigUserFile(t *testing.T) {
	m := newMachine(t)
	ada, bot := m.user("ada", "Ada", "human"), m.user("bot", "Bot", "agent")
	sid := newSession()
	adaLog := ada.join(sid).LogPath

	home := userConfig(t, "# the agent of the user\nagent:\n  session: "+sid+"\n  harness: "+yamlList(contracttest.ACPDouble(t))+
		"\n  role-file: roles/bot.md\n", map[string]string{"roles/bot.md": `You are Bot. please say "Bot from the user file."`})
	bot.agentRunIn(t, startDir(t, "", nil), []string{"XDG_CONFIG_HOME=" + home})
	waitUtterance(t, adaLog, "bot", "Bot from the user file.")
}

// TestConfigProjectFile: the project file overrides the user file key by key. A role file in
// the project file replaces the role of the user file, and its relative path is relative to the
// start directory. Keys that the project file does not set (the harness) still come from the
// user file.
func TestConfigProjectFile(t *testing.T) {
	m := newMachine(t)
	ada, bot := m.user("ada", "Ada", "human"), m.user("bot", "Bot", "agent")
	sid := newSession()
	adaLog := ada.join(sid).LogPath

	home := userConfig(t, "agent:\n  session: not-this-session\n  role: please say \"user role\"\n  harness: "+
		yamlList(contracttest.ACPDouble(t))+"\n", nil)
	start := startDir(t, "agent:\n  session: "+sid+"\n  role-file: roles/minutes.md\n",
		map[string]string{"roles/minutes.md": `You are Bot. please say "Bot from the project file."`})
	bot.agentRunIn(t, start, []string{"XDG_CONFIG_HOME=" + home})
	waitUtterance(t, adaLog, "bot", "Bot from the project file.")
}

// TestConfigEnvAndFlags: an environment variable overrides the files, and a flag overrides
// both. The command after "--" overrides the harness of a file, and --wake turn-end overrides the
// wake strategy of a file without an error about the model of that file.
func TestConfigEnvAndFlags(t *testing.T) {
	m := newMachine(t)
	ada, bot := m.user("ada", "Ada", "human"), m.user("bot", "Bot", "agent")
	sid := newSession()
	adaLog := ada.join(sid).LogPath

	home := userConfig(t, `agent:
  session: file-session
  role: please say "file role"
  harness: ["/nonexistent/harness"]
  wake:
    strategy: decision-model
    url: http://127.0.0.1:1/v1/systemone
    model: jev-1.14.0
`, nil)
	p, _ := bot.agentRunIn(t, startDir(t, "", nil), []string{"XDG_CONFIG_HOME=" + home, "OFFICE_SESSION=" + sid},
		"--role", `You are Bot. please say "flag role"`, "--wake", "turn-end", "--", contracttest.ACPDouble(t))
	waitUtterance(t, adaLog, "bot", "flag role")
	if !strings.Contains(p.Stderr(), "a new topic within a monologue is not detected") {
		t.Errorf("--wake turn-end must override the strategy of the file:\n%s", p.Stderr())
	}
}

// TestConfigDecisionModelKeyEnv: the wake strategy decision-model comes from the user file, and
// the key comes from the environment variable that agent.wake.key-env names. A key-env that
// names a variable that is not set is an error.
func TestConfigDecisionModelKeyEnv(t *testing.T) {
	s1 := newSystemOne(t, false)
	file := "agent:\n  wake:\n    strategy: decision-model\n    url: " + s1.srv.URL + "/v1/systemone\n    model: jev-1.14.0\n    threshold: 0.5\n    key-env: MY_DECIDE_KEY\n"
	home := userConfig(t, file, nil)
	mt := newMeetingWithRunner(t, []string{"XDG_CONFIG_HOME=" + home, "MY_DECIDE_KEY=key-from-env", "OFFICE_DECIDE_KEY=not-this-key"})
	mt.waitStderr("wake strategy decision-model: endpoint " + s1.srv.URL + "/v1/systemone, model jev-1.14.0, threshold 0.5")
	mt.say(mt.ada, `Bot, please say "Here."`)
	waitUtterance(t, mt.adaLog, "bot", "Here.")
	if c := s1.call(0); c.auth != "Bearer key-from-env" {
		t.Errorf("the key must come from MY_DECIDE_KEY: %q", c.auth)
	}
	if strings.Contains(mt.runner.Stderr(), "key-from-env") {
		t.Error("the runner logged the key")
	}

	_, errOut, code := contracttest.RunIn(t, startDir(t, "", nil), "office", append(mt.bot.env, "XDG_CONFIG_HOME="+home, "MY_DECIDE_KEY="),
		nil, "agent", "run", "--session", "s1", "--", "true")
	if code != 2 || !strings.Contains(errOut, "MY_DECIDE_KEY") || !strings.Contains(errOut, "not set") {
		t.Errorf("key-env without the variable: exit %d, %q", code, errOut)
	}
}

// TestConfigErrors: an unknown key, a wrong type, a value that does not fit and a key in a file
// are errors (exit 2) that name the file and the key, in "office config show" and in "office
// agent run".
func TestConfigErrors(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		{"agent:\n  key: sk-123\n", "line 2: unknown key agent.key"},
		{"agent:\n  wake:\n    api-key: sk-123\n", "line 3: unknown key agent.wake.api-key"},
		{"agent:\n  wake:\n    threshold: high\n", "line 3, key agent.wake.threshold: want a number"},
		{"agent:\n  harness: opencode acp\n", "line 2, key agent.harness: want a list of texts"},
		{"agent:\n  wake:\n    timeout: 5\n", "line 3, key agent.wake.timeout: want a duration"},
		{"agent:\n  wake:\n    key-env: sk-abc-123\n", "key agent.wake.key-env: want the name of an environment variable"},
		{"agent:\n  wake:\n    strategy: turn-end\n    url: http://x\n", "key agent.wake.url does not go with agent.wake.strategy"},
		{"agent:\n  wake:\n    strategy: always\n", "key agent.wake.strategy: must be"},
		{"agent:\n  role: a\n  role-file: b\n", "give agent.role or agent.role-file, not both"},
		{"session: s1\n", "line 1: unknown key session"},
	} {
		start := startDir(t, c.file, nil)
		path := filepath.Join(start, ".office", "config.yaml")
		env := []string{"XDG_CONFIG_HOME=" + t.TempDir(), "OFFICE_SESSION=", "OFFICE_JSON="}
		for _, args := range [][]string{{"config", "show"}, {"agent", "run", "--session", "s1", "--", "true"}} {
			_, errOut, code := contracttest.RunIn(t, start, "office", env, nil, args...)
			if code != 2 || !strings.Contains(errOut, path) || !strings.Contains(errOut, c.want) {
				t.Errorf("office %v with %q: exit %d, want %q in %q", args, c.file, code, c.want, errOut)
			}
		}
	}

	// The same error in the user file names the user file, also with --json.
	home := userConfig(t, "agent:\n  rolefile: x\n", nil)
	var v map[string]map[string]string
	out, _, code := contracttest.RunIn(t, startDir(t, "", nil), "office", []string{"XDG_CONFIG_HOME=" + home}, nil, "config", "show", "--json")
	if err := json.Unmarshal([]byte(out), &v); err != nil || code != 2 || v["error"]["code"] != "usage" ||
		!strings.Contains(v["error"]["message"], filepath.Join(home, "agentic-office", "client", "config.yaml")) ||
		!strings.Contains(v["error"]["message"], "unknown key agent.rolefile") {
		t.Errorf("user file error with --json: exit %d, %q", code, out)
	}
}

// TestConfigShow: "office config show" prints the effective settings of the files, the defaults
// and the environment, with the source of each, in text and with --json. It shows whether the
// key variable is set, never the key.
func TestConfigShow(t *testing.T) {
	home := userConfig(t, `agent:
  session: from-user
  harness: [opencode, acp]
  role-file: roles/minutes.md
  allow: ["office_*"]
  wake:
    strategy: decision-model
    url: https://api.typesafe.ai/v1/systemone
    model: jev-1.14.0
    threshold: 0.7
    key-env: OFFICE_DECIDE_KEY
`, nil)
	userFile := filepath.Join(home, "agentic-office", "client", "config.yaml")
	start := startDir(t, "agent:\n  role: You keep the minutes.\n  wake:\n    strategy: llm\n    command: ./judge\n", nil)
	projectFile := filepath.Join(start, ".office", "config.yaml")
	env := []string{"XDG_CONFIG_HOME=" + home, "OFFICE_SESSION=from-env", "OFFICE_DECIDE_KEY=very-secret", "OFFICE_JSON="}

	out, errOut, code := contracttest.RunIn(t, start, "office", env, nil, "config", "show", "--json")
	if code != 0 {
		t.Fatalf("config show --json: exit %d, %s", code, errOut)
	}
	if strings.Contains(out, "very-secret") {
		t.Fatal("config show printed the key")
	}
	var v struct {
		Files []struct {
			Kind, Path string
			Found      bool
		}
		Settings []struct {
			Key    string
			Value  any
			Unused bool
			Source string
			From   string
		}
		KeySet bool
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(v.Files) != 2 || v.Files[0].Kind != "user" || v.Files[0].Path != userFile || !v.Files[0].Found ||
		v.Files[1].Kind != "project" || v.Files[1].Path != projectFile || !v.Files[1].Found || !v.KeySet {
		t.Errorf("files and key: %s", out)
	}
	type want struct {
		value          any
		source, from   string
		unused, absent bool
	}
	for key, w := range map[string]want{
		"agent.session":        {value: "from-env", source: "env", from: "OFFICE_SESSION"},
		"agent.harness":        {value: []any{"opencode", "acp"}, source: "user", from: userFile},
		"agent.allow":          {value: []any{"office_*"}, source: "user", from: userFile},
		"agent.role":           {value: "You keep the minutes.", source: "project", from: projectFile},
		"agent.role-file":      {absent: true}, // replaced by agent.role of the project file
		"agent.wake.strategy":  {value: "llm", source: "project", from: projectFile},
		"agent.wake.command":   {value: "./judge", source: "project", from: projectFile},
		"agent.wake.url":       {absent: true}, // replaced by agent.wake.command of the project file
		"agent.wake.model":     {value: "jev-1.14.0", source: "user", from: userFile},
		"agent.wake.threshold": {value: 0.7, source: "user", from: userFile, unused: true},
		"agent.wake.timeout":   {value: "10s", source: "default"},
		"agent.wake.key-env":   {value: "OFFICE_DECIDE_KEY", source: "user", from: userFile},
	} {
		found := false
		for _, s := range v.Settings {
			if s.Key != key {
				continue
			}
			found = true
			gv, _ := json.Marshal(s.Value)
			wv, _ := json.Marshal(w.value)
			if string(gv) != string(wv) || s.Source != w.source || s.From != w.from || s.Unused != w.unused {
				t.Errorf("%s: got %s from %s %s (unused %t), want %s from %s %s (unused %t)", key, gv, s.Source, s.From, s.Unused, wv, w.source, w.from, w.unused)
			}
		}
		if found == w.absent {
			t.Errorf("%s: found %t, want %t", key, found, !w.absent)
		}
	}

	out, _, code = contracttest.RunIn(t, start, "office", env, nil, "config", "show")
	for _, s := range []string{"user file: " + userFile + " (read)", "project file: " + projectFile + " (read)",
		`agent.role`, `"You keep the minutes."`, "project file " + projectFile, "environment variable OFFICE_SESSION",
		"not used by the wake strategy llm", "the key variable OFFICE_DECIDE_KEY is set"} {
		if !strings.Contains(out, s) {
			t.Errorf("config show lacks %q:\n%s", s, out)
		}
	}
	if code != 0 || strings.Contains(out, "very-secret") {
		t.Errorf("config show: exit %d:\n%s", code, out)
	}

	// Without files, only the defaults remain.
	out, _, code = contracttest.RunIn(t, startDir(t, "", nil), "office", []string{"XDG_CONFIG_HOME=" + t.TempDir(), "OFFICE_SESSION=", "OFFICE_DECIDE_KEY="}, nil, "config", "show")
	if code != 0 || !strings.Contains(out, "(not found)") || !regexp.MustCompile(`agent\.wake\.strategy +"turn-end" +default\n`).MatchString(out) ||
		strings.Contains(out, "agent.session") || strings.Contains(out, "not used") || !strings.Contains(out, "is not set") {
		t.Errorf("config show without files: exit %d:\n%s", code, out)
	}
}

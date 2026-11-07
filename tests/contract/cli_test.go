package contract

// Contract tests of the office CLI (bb-cli) through if-cli: they run the real office binary
// against a real office-server and check its output, its exit codes and the local log it
// leaves (if-local-log). Helpers for both CLI and replicator tests are in this file.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	"github.com/mrsimpson/agentic-office/client/replicator/locallog"
	"github.com/mrsimpson/agentic-office/server/identity/api/auth"
	sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"
	"github.com/mrsimpson/agentic-office/server/session/api/events"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// machine is one computer with a runtime directory, shared by several CLI users.
type machine struct {
	t       *testing.T
	srv     *contracttest.Server
	runtime string
	admin   *officeUser
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	srv := contracttest.StartServer(t, nil)
	m := &machine{t: t, srv: srv, runtime: filepath.Join(t.TempDir(), "run")}
	adminKey := filepath.Join(t.TempDir(), "admin.pem")
	if err := auth.WriteKey(adminKey, srv.AdminKey); err != nil {
		t.Fatal(err)
	}
	m.admin = m.userWithKey(adminKey)
	return m
}

// officeUser runs the office CLI with its own key.
type officeUser struct {
	m   *machine
	key string
	env []string
}

func (m *machine) userWithKey(keyFile string) *officeUser {
	return &officeUser{m: m, key: keyFile, env: []string{"OFFICE_SERVER=" + m.srv.URL, "OFFICE_CA_CERT=" + m.srv.CertFile,
		"OFFICE_KEY=" + keyFile, "OFFICE_RUNTIME_DIR=" + m.runtime, "OFFICE_SESSION=", "OFFICE_JSON="}}
}

// user creates a key with the CLI and registers it with the admin CLI.
func (m *machine) user(pid, name, kind string) *officeUser {
	m.t.Helper()
	u := m.userWithKey(filepath.Join(m.t.TempDir(), pid+".pem"))
	k := u.ok("key", "create")
	if k["created"] != true {
		m.t.Fatalf("key create: %v", k)
	}
	m.admin.ok("admin", "key", "register", pid, k["publicKey"].(string), "--name", name, "--kind", kind)
	return u
}

// run runs office and returns stdout, stderr and the exit code.
func (u *officeUser) run(args ...string) (string, string, int) {
	u.m.t.Helper()
	return contracttest.Run(u.m.t, "office", u.env, args...)
}

// call runs office with --json and returns the parsed object and the exit code.
func (u *officeUser) call(args ...string) (map[string]any, int) {
	u.m.t.Helper()
	out, errOut, code := u.run(append(args, "--json")...)
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil || strings.Count(strings.TrimSpace(out), "\n") != 0 {
		u.m.t.Fatalf("office %v --json: want one JSON line on stdout, got %q (stderr %q, exit %d)", args, out, errOut, code)
	}
	return v, code
}

// ok is call that must succeed.
func (u *officeUser) ok(args ...string) map[string]any {
	u.m.t.Helper()
	v, code := u.call(args...)
	if code != 0 {
		u.m.t.Fatalf("office %v: exit %d: %v", args, code, v)
	}
	return v
}

// fails is call that must fail with exit code 1 or 2 and the given error code.
func (u *officeUser) fails(exit int, code string, args ...string) {
	u.m.t.Helper()
	v, got := u.call(args...)
	e, _ := v["error"].(map[string]any)
	if got != exit || e == nil || e["code"] != code {
		u.m.t.Fatalf("office %v: want exit %d and code %q, got exit %d: %v", args, exit, code, got, v)
	}
}

type joined struct {
	LogPath       string          `json:"logPath"`
	ParticipantID string          `json:"participantId"`
	ReplicatorPID int             `json:"replicatorPid"`
	Duplicate     bool            `json:"duplicate"`
	Event         json.RawMessage `json:"event"`
}

// join joins with --json. The replicator is killed when the test ends.
func (u *officeUser) join(sid string, args ...string) joined {
	u.m.t.Helper()
	out, errOut, code := u.run(append([]string{"meeting", "join", sid, "--json"}, args...)...)
	var j joined
	if code != 0 || json.Unmarshal([]byte(out), &j) != nil || j.LogPath == "" || j.ReplicatorPID == 0 {
		u.m.t.Fatalf("join %s: exit %d, stdout %q, stderr %q", sid, code, out, errOut)
	}
	killAtEnd(u.m.t, j.ReplicatorPID)
	return j
}

func killAtEnd(t *testing.T, pid int) {
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
}

// alive reports whether a process runs. A zombie counts as ended.
func alive(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	return i < 0 || i+2 >= len(s) || s[i+2] != 'Z'
}

func waitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout after %v: %s", timeout, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// logLine is one line of a local log.
type logLine struct {
	raw string
	ev  *sessionv1.Event
}

// readLog reads the complete lines of a local log. Every line must parse and carry the dotted
// type of its payload.
func readLog(t *testing.T, path string) []logLine {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []logLine
	for _, l := range strings.SplitAfter(string(b), "\n") {
		if !strings.HasSuffix(l, "\n") {
			break // incomplete last line
		}
		ev, err := locallog.UnmarshalLine([]byte(l))
		if err != nil {
			t.Fatalf("%s: bad line %q: %v", path, l, err)
		}
		if ev.GetType() == "" || ev.GetType() != events.TypeOf(ev) {
			t.Fatalf("%s: line without dotted type: %q", path, l)
		}
		out = append(out, logLine{raw: strings.TrimSuffix(l, "\n"), ev: ev})
	}
	return out
}

// waitLog waits until the local log has an event with ID id and returns all lines.
func waitLog(t *testing.T, path string, id uint32) []logLine {
	t.Helper()
	var lines []logLine
	waitUntil(t, 10*time.Second, fmt.Sprintf("event %d in %s", id, path), func() bool {
		lines = readLog(t, path)
		return len(lines) > 0 && lines[len(lines)-1].ev.GetId() >= id
	})
	return lines
}

// eventOf returns the event of a --json command result.
func eventOf(t *testing.T, v map[string]any) (string, *sessionv1.Event) {
	t.Helper()
	raw, err := json.Marshal(v["event"])
	if err != nil {
		t.Fatal(err)
	}
	ev, err := locallog.UnmarshalLine(raw)
	if err != nil {
		t.Fatalf("event %s: %v", raw, err)
	}
	line, _ := locallog.MarshalLine(ev)
	return strings.TrimSuffix(string(line), "\n"), ev
}

func newSession() string { return "s-" + ids.NewCommandID() }

func TestCLIKeysAndAdmin(t *testing.T) {
	m := newMachine(t)
	u := m.userWithKey(filepath.Join(t.TempDir(), "keys", "ada.pem"))
	k := u.ok("key", "create")
	if k["created"] != true || k["keyFile"] != u.key {
		t.Fatalf("key create: %v", k)
	}
	pub, err := auth.ParsePublicKey(k["publicKey"].(string))
	if err != nil || k["keyId"] != auth.KeyID(pub) {
		t.Fatalf("key create: bad public key or key ID: %v (%v)", k, err)
	}
	if fi, err := os.Stat(u.key); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v, %v", fi, err)
	}
	again := u.ok("key", "create")
	if again["created"] != false || again["publicKey"] != k["publicKey"] {
		t.Fatalf("second key create changed the key: %v", again)
	}
	if show := u.ok("key", "show"); show["publicKey"] != k["publicKey"] || show["keyId"] != k["keyId"] {
		t.Fatalf("key show: %v", show)
	}
	out, _, code := u.run("key", "show")
	if code != 0 || !strings.Contains(out, k["publicKey"].(string)) || !strings.Contains(out, k["keyId"].(string)) {
		t.Fatalf("key show (text): exit %d, %q", code, out)
	}

	// Not registered yet.
	if _, code := u.call("whoami"); code != 1 {
		t.Fatalf("whoami before registration: exit %d", code)
	}
	reg := m.admin.ok("admin", "key", "register", "ada", k["publicKey"].(string), "--name", "Ada", "--kind", "human")
	if reg["keyId"] != k["keyId"] {
		t.Fatalf("register: %v", reg)
	}
	if w := u.ok("whoami"); w["participantId"] != "ada" || w["keyId"] != k["keyId"] {
		t.Fatalf("whoami: %v", w)
	}
	if out, _, code := u.run("whoami"); code != 0 || strings.TrimSpace(out) != "ada" {
		t.Fatalf("whoami (text): exit %d, %q", code, out)
	}
	// Only admins may register keys.
	u.fails(1, "permission_denied", "admin", "key", "register", "eve", k["publicKey"].(string), "--name", "Eve", "--kind", "human")
	u.fails(2, "usage", "admin", "key", "register", "eve", "not-a-key")

	m.admin.ok("admin", "key", "revoke", k["keyId"].(string))
	if _, code := u.call("whoami"); code != 1 {
		t.Fatalf("whoami after revoke: exit %d", code)
	}

	// Usage errors.
	u.fails(2, "usage", "no-such-command")
	if _, _, code := u.run("meeting", "join"); code != 2 {
		t.Fatalf("join without session: exit %d", code)
	}
	if out, _, code := u.run("help"); code != 0 || !strings.Contains(out, "meeting join") {
		t.Fatalf("help: exit %d", code)
	}
	// A client without a key file says so.
	nokey := m.userWithKey(filepath.Join(t.TempDir(), "missing.pem"))
	nokey.fails(1, "no_key", "whoami")
}

func TestCLIJoinPrintsPath(t *testing.T) {
	m := newMachine(t)
	ada := m.user("ada", "Ada", "human")
	sid := newSession()

	start := time.Now()
	out, errOut, code := ada.run("meeting", "join", sid)
	if code != 0 {
		t.Fatalf("join: exit %d: %s", code, errOut)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("join took %v; it must return at once", d)
	}
	path := strings.TrimSpace(out)
	if strings.Count(out, "\n") != 1 || !filepath.IsAbs(path) || !strings.HasPrefix(path, m.runtime+string(filepath.Separator)) ||
		!strings.HasSuffix(path, ".jsonl") {
		t.Fatalf("join must print one absolute path in the runtime directory, got %q", out)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("log file: %v, %v", fi, err)
	}
	for _, d := range []string{m.runtime, filepath.Dir(path)} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("directory %s: %v, %v", d, fi, err)
		}
	}
	lines := waitLog(t, path, 1)
	if ev := lines[0].ev; ev.GetType() != events.ParticipantJoined || ev.GetParticipantJoined().GetParticipantId() != "ada" {
		t.Fatalf("first line: %s", lines[0].raw)
	}
	if !regexp.MustCompile(`^\{"id":1,`).MatchString(lines[0].raw) {
		t.Fatalf("the event ID must be a JSON number: %s", lines[0].raw)
	}

	// A second join is the same participant, the same file and the same replicator.
	j := ada.join(sid)
	if j.LogPath != path || !j.Duplicate || j.ParticipantID != "ada" {
		t.Fatalf("second join: %+v", j)
	}
	if again := ada.join(sid); again.ReplicatorPID != j.ReplicatorPID {
		t.Fatalf("a join while the replicator runs must reuse it: %d, then %d", j.ReplicatorPID, again.ReplicatorPID)
	}
	if v := ada.ok("meeting", "log"); v["logPath"] != path || v["replicatorRunning"] != true {
		t.Fatalf("meeting log: %v", v)
	}
	if out, _, _ := ada.run("meeting", "log"); strings.TrimSpace(out) != path {
		t.Fatalf("meeting log (text): %q", out)
	}
	ada.ok("meeting", "end")
	waitUntil(t, 5*time.Second, "replicator stops", func() bool { return !alive(j.ReplicatorPID) })
}

func TestCLICommandsAppearInLog(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")
	sid := newSession()
	j := bot.join(sid)
	ada.join(sid)

	results := map[uint32]string{}
	do := func(u *officeUser, wantType string, args ...string) *sessionv1.Event {
		t.Helper()
		line, ev := eventOf(t, u.ok(args...))
		if ev.GetType() != wantType {
			t.Fatalf("office %v: event type %s, want %s", args, ev.GetType(), wantType)
		}
		results[ev.GetId()] = line
		return ev
	}
	utt := do(bot, events.UtteranceFinal, "say", "I", "can", "summarize", "that")
	adaUtt := do(ada, events.UtteranceFinal, "say", "Hello bot")
	do(bot, events.HandRaised, "hand", "raise", "--topic", "budget")
	do(bot, events.MinutesUpdated, "minutes", "update", "Budget: agreed")
	do(bot, events.ArtifactShown, "stage", "show", "slides/42")
	do(ada, events.ParticipantPaused, "meeting", "pause")
	do(ada, events.ParticipantResumed, "meeting", "resume")
	do(bot, events.TurnEnd, "signal", "turn-end", "--participant", "ada")
	do(bot, events.SilenceLong, "signal", "silence-long", "--duration", "12s")
	do(bot, events.Addressed, "signal", "addressed", "--participant", "ada", "--utterance", strconv.Itoa(int(adaUtt.GetId())))
	ret := do(bot, events.UtteranceOffTheRecord, "utterance", "off-the-record", strconv.Itoa(int(utt.GetId())))
	if ret.GetUtteranceOffTheRecord().GetUtteranceEventId() != utt.GetId() {
		t.Fatalf("mark off the record points to %d, want %d", ret.GetUtteranceOffTheRecord().GetUtteranceEventId(), utt.GetId())
	}
	// Marking someone else's sentence off the record fails; the server's code is passed on.
	bot.fails(1, "permission_denied", "utterance", "off-the-record", strconv.Itoa(int(adaUtt.GetId())))
	// --last marks the newest own sentence from the local log off the record.
	last := do(ada, events.UtteranceOffTheRecord, "utterance", "off-the-record", "--last")
	if last.GetUtteranceOffTheRecord().GetUtteranceEventId() != adaUtt.GetId() {
		t.Fatalf("mark off the record --last points to %d, want %d", last.GetUtteranceOffTheRecord().GetUtteranceEventId(), adaUtt.GetId())
	}
	// meeting events reads the events after a cursor from the server. Ada's sentence is now off the
	// record, so it comes back as a placeholder with its ID and without its text.
	got := bot.ok("meeting", "events", "--after", strconv.Itoa(int(adaUtt.GetId()-1)), "--limit", "1")
	evs, _ := got["events"].([]any)
	if len(evs) != 1 || got["more"] != true {
		t.Fatalf("meeting events --limit 1: %v", got)
	}
	ev0, _ := evs[0].(map[string]any)
	if u, _ := ev0["utteranceFinal"].(map[string]any); ev0["id"] != float64(adaUtt.GetId()) || u["offTheRecord"] != true || u["text"] != nil {
		t.Fatalf("meeting events: placeholder expected, got %v", ev0)
	}

	// Minutes from stdin through --file -.
	out, errOut, code := contracttest.RunStdin(t, "office", bot.env, strings.NewReader("line 1\nline 2\n"),
		"minutes", "update", "--file", "-", "--json")
	if code != 0 {
		t.Fatalf("minutes from stdin: exit %d: %s", code, errOut)
	}
	var v map[string]any
	_ = json.Unmarshal([]byte(out), &v)
	line, ev := eventOf(t, v)
	if ev.GetMinutesUpdated().GetText() != "line 1\nline 2\n" {
		t.Fatalf("minutes from stdin: %s", line)
	}
	results[ev.GetId()] = line

	// Text output names the event.
	if out, _, code := bot.run("hand", "raise"); code != 0 || !regexp.MustCompile(`^event \d+ hand\.raised\n$`).MatchString(out) {
		t.Fatalf("hand raise (text): exit %d, %q", code, out)
	}

	// Every event appears exactly once, as the command returned it; the original utterance is
	// still there unchanged: an off-the-record mark is a new line.
	var maxID uint32
	for id := range results {
		maxID = max(maxID, id)
	}
	lines := waitLog(t, j.LogPath, maxID+1)
	seen := map[uint32]int{}
	for i, l := range lines {
		seen[l.ev.GetId()]++
		if l.ev.GetId() != uint32(i+1) {
			t.Fatalf("line %d has ID %d", i+1, l.ev.GetId())
		}
		if want, ok := results[l.ev.GetId()]; ok && want != l.raw {
			t.Fatalf("line %d differs from the command result:\n got %s\nwant %s", i+1, l.raw, want)
		}
	}
	for id := range results {
		if seen[id] != 1 {
			t.Fatalf("event %d appears %d times", id, seen[id])
		}
	}
	if lines[utt.GetId()-1].ev.GetUtteranceFinal().GetText() != "I can summarize that" {
		t.Fatalf("the off the record utterance changed: %s", lines[utt.GetId()-1].raw)
	}

	// Commands of a session that ended fail with the server's code.
	bot.ok("meeting", "end")
	bot.fails(1, "failed_precondition", "say", "--session", sid, "too late")
}

func TestCLISessionChoice(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	bot.fails(1, "no_session", "say", "hello")
	s1, s2 := newSession(), newSession()
	bot.join(s1)
	if _, ev := eventOf(t, bot.ok("say", "only one session")); ev.GetSessionId() != s1 {
		t.Fatalf("default session: %s, want %s", ev.GetSessionId(), s1)
	}
	bot.join(s2)
	bot.fails(1, "no_session", "say", "which one?")
	if _, ev := eventOf(t, bot.ok("say", "--session", s2, "this one")); ev.GetSessionId() != s2 {
		t.Fatalf("--session: %s, want %s", ev.GetSessionId(), s2)
	}
	// A session that ended locally is no longer the default.
	bot.ok("meeting", "end", "--session", s1)
	waitUntil(t, 5*time.Second, "session.ended in the log", func() bool {
		l := readLog(t, filepath.Join(m.runtime, s1, "bot.jsonl"))
		return len(l) > 0 && l[len(l)-1].ev.GetType() == events.SessionEnded
	})
	if _, ev := eventOf(t, bot.ok("say", "default again")); ev.GetSessionId() != s2 {
		t.Fatalf("default session after end: %s, want %s", ev.GetSessionId(), s2)
	}
}

// TestCLIArtifacts: slides render and artifact get against the real artifact tools.
func TestCLIArtifacts(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	md := write("deck.md", "# Budget\n\n## Plan\n- one\n- <two>\nsay this\n")
	js := write("deck.json", `{"title":"Budget","slides":[{"title":"Plan","bullets":["one","<two>"],"notes":"say this"}]}`)

	v := bot.ok("slides", "render", md)
	if v["artifactId"] != "slides/1" || v["mediaType"] != "text/html; charset=utf-8" || v["duplicate"] != false {
		t.Fatalf("render Markdown: %v", v)
	}
	// The same deck as JSON is the same input: a new artifact with the same content.
	if out, errOut, code := ada.run("slides", "render", js); code != 0 || out != "slides/2\n" {
		t.Fatalf("render JSON without --json: exit %d, %q %q", code, out, errOut)
	}

	html1, _, code := ada.run("artifact", "get", "slides/1")
	html2, _, code2 := bot.run("artifact", "get", "slides/2")
	if code != 0 || code2 != 0 || html1 != html2 || !strings.Contains(html1, "<li>&lt;two&gt;</li>") {
		t.Fatalf("artifact get: exit %d/%d, same %v:\n%s", code, code2, html1 == html2, html1)
	}
	a, _ := bot.ok("artifact", "get", "slides/1")["artifact"].(map[string]any)
	if a["id"] != "slides/1" || a["mediaType"] != "text/html; charset=utf-8" || a["authorParticipantId"] != "bot" ||
		a["content"] == nil || a["createdAt"] == nil {
		t.Fatalf("artifact get --json: %v", a)
	}
	out := filepath.Join(dir, "out.html")
	if v := ada.ok("artifact", "get", "slides/2", "--out", out); v["file"] != out || v["artifactId"] != "slides/2" {
		t.Fatalf("artifact get --out: %v", v)
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != html1 {
		t.Fatalf("--out file: %v", err)
	}

	bot.fails(1, "not_found", "artifact", "get", "slides/99")
	bot.fails(1, "invalid_argument", "slides", "render", write("blank.json", `{"slides":[{"title":" "}]}`))
	bot.fails(2, "usage", "slides", "render", write("bad.md", "text before any slide\n"))
	bot.fails(2, "usage", "artifact", "get")
}

// entriesOf returns the entries of a "journal list --json" result.
func entriesOf(t *testing.T, v map[string]any) []map[string]any {
	t.Helper()
	raw, _ := v["entries"].([]any)
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i], _ = r.(map[string]any)
	}
	return out
}

// TestCLIJournal: journal append and list against the real journal, and the minutes that a
// session stores when it ends.
func TestCLIJournal(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")

	v := bot.ok("journal", "append", "--kind", "decision", "We ship")
	e, _ := v["entry"].(map[string]any)
	if v["duplicate"] != false || e["id"] != float64(1) || e["kind"] != "ENTRY_KIND_DECISION" || e["text"] != "We ship" ||
		e["authorParticipantId"] != "bot" || e["authorKind"] != "AUTHOR_KIND_AGENT" || e["sessionId"] != nil ||
		e["commandId"] == nil || e["time"] == nil {
		t.Fatalf("append: %v", v)
	}
	ada.ok("journal", "append", "--kind", "note", "--session", "s1", "A note")
	f := filepath.Join(t.TempDir(), "minutes.md")
	if err := os.WriteFile(f, []byte("Line one\nline two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if e := ada.ok("journal", "append", "--kind", "minutes", "--file", f)["entry"].(map[string]any); e["text"] != "Line one\nline two" ||
		e["authorKind"] != "AUTHOR_KIND_HUMAN" {
		t.Fatalf("append from a file: %v", e)
	}
	if out, errOut, code := ada.run("journal", "append", "--kind", "task"); code != 2 || !strings.Contains(errOut, "usage") {
		t.Fatalf("append without text: exit %d, %q %q", code, out, errOut)
	}
	if out, _, code := ada.run("journal", "append", "--kind", "task", "Ada plans"); code != 0 || out != "entry 4\n" {
		t.Fatalf("append without --json: exit %d, %q", code, out)
	}

	ids := func(args ...string) string {
		t.Helper()
		var got []string
		for _, e := range entriesOf(t, bot.ok(append([]string{"journal", "list"}, args...)...)) {
			got = append(got, fmt.Sprint(e["id"]))
		}
		return strings.Join(got, ",")
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "1,2,3,4"},
		{[]string{"--kind", "note"}, "2"},
		{[]string{"--session", "s1"}, "2"},
		{[]string{"--after", "1", "--limit", "2"}, "2,3"},
		{[]string{"--after", "4"}, ""},
	} {
		if got := ids(c.args...); got != c.want {
			t.Fatalf("journal list %v: %s, want %s", c.args, got, c.want)
		}
	}
	out, _, code := ada.run("journal", "list", "--kind", "minutes")
	if code != 0 || out != "#3 minutes ada: Line one line two\n" {
		t.Fatalf("journal list without --json: exit %d, %q", code, out)
	}

	bot.fails(2, "usage", "journal", "append", "--kind", "gossip", "x")
	bot.fails(2, "usage", "journal", "append", "We ship")
	bot.fails(2, "usage", "journal", "list", "--kind", "gossip")

	// The minutes of a session go into the journal when it ends.
	sid := newSession()
	bot.join(sid)
	ada.join(sid)
	bot.ok("minutes", "update", "--session", sid, "Draft")
	bot.ok("minutes", "update", "--session", sid, "Final: we ship")
	ada.ok("meeting", "end", "--session", sid)
	got := entriesOf(t, ada.ok("journal", "list", "--session", sid, "--kind", "minutes"))
	if len(got) != 1 || got[0]["text"] != "Final: we ship" || got[0]["authorParticipantId"] != "bot" ||
		got[0]["authorKind"] != "AUTHOR_KIND_AGENT" {
		t.Fatalf("minutes of the session: %v", got)
	}
}

// TestCLIEarsCheck: join --mic and --wav check the ears before they join. If a model, the WAV
// file or a recorder is missing, join fails with a clear message and the participant does not
// join (so it is never shown as captured without ears).
func TestCLIEarsCheck(t *testing.T) {
	m := newMachine(t)
	ada := m.user("ada", "Ada", "human")
	dir := t.TempDir()
	notWAV := filepath.Join(dir, "notes.txt")
	model := filepath.Join(dir, "model.bin") // the check only looks whether the file is there
	for _, f := range []string{notWAV, model} {
		if err := os.WriteFile(f, []byte("not audio"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sid := newSession()
	cases := []struct {
		name string
		env  []string
		args []string
		msg  string
	}{
		{"no Whisper model", []string{"OFFICE_WHISPER_MODEL=" + filepath.Join(dir, "missing.bin"), "OFFICE_VAD_MODEL=" + model},
			[]string{"--wav", notWAV}, "Whisper model"},
		{"no VAD model", []string{"OFFICE_WHISPER_MODEL=" + model, "OFFICE_VAD_MODEL=" + filepath.Join(dir, "missing.bin")},
			[]string{"--wav", notWAV}, "VAD model"},
		{"not a WAV file", []string{"OFFICE_WHISPER_MODEL=" + model, "OFFICE_VAD_MODEL=" + model},
			[]string{"--wav", notWAV}, "not a WAV file"},
		{"no recorder", []string{"OFFICE_WHISPER_MODEL=" + model, "OFFICE_VAD_MODEL=" + model, "PATH=" + dir, "OFFICE_MIC_COMMAND="},
			[]string{"--mic"}, "no audio recorder"},
		{"flag before env", []string{"OFFICE_WHISPER_MODEL=" + model, "OFFICE_VAD_MODEL=" + model},
			[]string{"--wav", notWAV, "--model", filepath.Join(dir, "flag.bin")}, "flag.bin"},
	}
	for _, c := range cases {
		u := *ada
		u.env = append(append([]string(nil), ada.env...), c.env...)
		v, code := u.call(append([]string{"meeting", "join", sid}, c.args...)...)
		e, _ := v["error"].(map[string]any)
		if code != 1 || e == nil || !strings.Contains(fmt.Sprint(e["message"]), c.msg) {
			t.Fatalf("%s: want exit 1 and a message with %q, got exit %d: %v", c.name, c.msg, code, v)
		}
	}
	ada.fails(2, "usage", "meeting", "join", sid, "--mic", "--wav", notWAV)
	// None of the failed joins joined.
	ada.fails(1, "no_session", "say", "hello")
}

func TestCLILeaveAndRejoin(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	sid := newSession()
	j := bot.join(sid)
	bot.ok("say", "before")
	_, left := eventOf(t, bot.ok("meeting", "leave"))
	if left.GetType() != events.ParticipantLeft {
		t.Fatalf("leave: %v", left)
	}
	// The replicator writes participant.left and stops.
	waitUntil(t, 5*time.Second, "replicator stops after leave", func() bool { return !alive(j.ReplicatorPID) })
	lines := readLog(t, j.LogPath)
	if last := lines[len(lines)-1]; last.ev.GetId() != left.GetId() {
		t.Fatalf("last line after leave: %s", last.raw)
	}
	bot.fails(1, "no_session", "say", "after leave")

	// A new join is a new join, not the stored one, and the log goes on in the same file.
	again := bot.join(sid)
	if again.Duplicate || again.LogPath != j.LogPath || again.ReplicatorPID == j.ReplicatorPID {
		t.Fatalf("rejoin: %+v", again)
	}
	_, ev := eventOf(t, bot.ok("say", "after rejoin"))
	lines = waitLog(t, j.LogPath, ev.GetId())
	for i, l := range lines {
		if l.ev.GetId() != uint32(i+1) {
			t.Fatalf("line %d has ID %d after rejoin", i+1, l.ev.GetId())
		}
	}
}

func TestCLIGatewayView(t *testing.T) {
	m := newMachine(t)
	ada := m.user("ada", "Ada", "human")
	sid := newSession()
	j := ada.join(sid)
	ada.ok("say", "an old sentence")

	view := contracttest.Start(t, "office", ada.env, "meeting", "view", "--json")
	next := func(what string) map[string]any {
		t.Helper()
		type res struct {
			line string
			err  error
		}
		ch := make(chan res, 1)
		go func() {
			l, err := view.Stdout.ReadString('\n')
			ch <- res{l, err}
		}()
		select {
		case r := <-ch:
			var v map[string]any
			if r.err != nil || json.Unmarshal([]byte(r.line), &v) != nil {
				t.Fatalf("view: waiting for %s: %q, %v\n%s", what, r.line, r.err, view.Stderr())
			}
			return v
		case <-time.After(10 * time.Second):
			t.Fatalf("view: timeout waiting for %s\n%s", what, view.Stderr())
		}
		return nil
	}
	utt := func(v map[string]any) (float64, string, string) {
		t.Helper()
		u, ok := v["utterance"].(map[string]any)
		if !ok {
			t.Fatalf("want an utterance, got %v", v)
		}
		id, _ := u["eventId"].(float64)
		return id, u["text"].(string), u["status"].(string)
	}
	// The view starts with the history of the log.
	if _, text, st := utt(next("old sentence")); text != "an old sentence" || st != "sent" {
		t.Fatalf("history: %q %s", text, st)
	}
	_, ev := eventOf(t, ada.ok("say", "a new sentence"))
	id, text, st := utt(next("new sentence"))
	if uint32(id) != ev.GetId() || text != "a new sentence" || st != "sent" {
		t.Fatalf("new sentence: %v %q %s", id, text, st)
	}
	// Others' sentences are not shown.
	bot := m.user("bot", "Bot", "agent")
	bot.join(sid)
	bot.ok("say", "not for the view")

	// o marks the last sent sentence off the record: a new line in the log, and the view shows it.
	if _, err := view.Stdin.Write([]byte("o")); err != nil {
		t.Fatal(err)
	}
	if id, _, st := utt(next("off_the_record")); uint32(id) != ev.GetId() || st != "off_the_record" {
		t.Fatalf("after o: %v %s", id, st)
	}
	lines := readLog(t, j.LogPath)
	last := lines[len(lines)-1].ev
	if last.GetType() != events.UtteranceOffTheRecord || last.GetUtteranceOffTheRecord().GetUtteranceEventId() != ev.GetId() ||
		lines[ev.GetId()-1].ev.GetUtteranceFinal().GetText() != "a new sentence" {
		t.Fatalf("log after o: %s", lines[len(lines)-1].raw)
	}

	// p pauses, p again resumes.
	_, _ = view.Stdin.Write([]byte("p"))
	if v := next("paused"); v["paused"] != true {
		t.Fatalf("after p: %v", v)
	}
	_, _ = view.Stdin.Write([]byte("p"))
	if v := next("resumed"); v["paused"] != false {
		t.Fatalf("after second p: %v", v)
	}
	// q quits.
	_, _ = view.Stdin.Write([]byte("q"))
	select {
	case <-view.Done():
		if c := view.Cmd.ProcessState.ExitCode(); c != 0 {
			t.Fatalf("view exit %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("view did not quit on q")
	}

	// The text form shows the status, and the view ends with the session. --session, because
	// the end below can reach the local log before the view looks for the default session, and
	// a session that ended is no longer the default.
	view2 := contracttest.Start(t, "office", ada.env, "meeting", "view", "--session", sid)
	got := make(chan string, 1)
	go func() {
		var all strings.Builder
		for {
			l, err := view2.Stdout.ReadString('\n')
			all.WriteString(l)
			if err != nil || strings.Contains(l, "session ended") {
				got <- all.String()
				return
			}
		}
	}()
	ada.ok("meeting", "end")
	var s string
	select {
	case s = <-got:
	case <-time.After(10 * time.Second):
		t.Fatalf("text view did not show the end of the session\n%s", view2.Stderr())
	}
	if !regexp.MustCompile(`(?m)^#\d+\s+off_the_record\s+a new sentence$`).MatchString(s) ||
		!strings.Contains(s, "session ended") || strings.Contains(s, "not for the view") {
		t.Fatalf("text view:\n%s", s)
	}
	select {
	case <-view2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("view did not stop on session.ended")
	}
}

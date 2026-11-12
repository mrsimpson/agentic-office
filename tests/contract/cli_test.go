package contract

// Contract tests of the office CLI (bb-cli) through if-cli: they run the real office binary
// against a real office-hub and check its output, its exit codes and the local log it
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

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/logfile"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// machine is one computer with a runtime directory, shared by several CLI users.
type machine struct {
	t       *testing.T
	srv     *contracttest.Hub
	runtime string
	admin   *officeUser
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	srv := contracttest.StartHub(t, nil)
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

// userWithKey returns a user with this key. Its configuration folder is empty, so no user
// configuration file of the host takes part.
func (m *machine) userWithKey(keyFile string) *officeUser {
	return &officeUser{m: m, key: keyFile, env: []string{"OFFICE_HUB=" + m.srv.URL, "OFFICE_CA_CERT=" + m.srv.CertFile,
		"OFFICE_TENANT=" + contracttest.DefaultTenant, "OFFICE_KEY=" + keyFile, "OFFICE_RUNTIME_DIR=" + m.runtime, "OFFICE_SESSION=", "OFFICE_JSON=",
		"XDG_CONFIG_HOME=" + m.t.TempDir()}}
}

// user creates a key with the CLI and registers it with the admin CLI.
func (m *machine) user(pid, name, kind string) *officeUser {
	m.t.Helper()
	u := m.userWithKey(filepath.Join(m.t.TempDir(), pid+".pem"))
	k := u.ok("key", "create")
	if k["created"] != true {
		m.t.Fatalf("key create: %v", k)
	}
	m.admin.ok("admin", "user", "create", pid, k["publicKey"].(string), "--name", name, "--kind", kind)
	m.srv.EnsureMember(m.t, pid)
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
	ev  *sessionstreamv1.Event
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
		ev, err := logfile.UnmarshalLine([]byte(l))
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
func eventOf(t *testing.T, v map[string]any) (string, *sessionstreamv1.Event) {
	t.Helper()
	raw, err := json.Marshal(v["event"])
	if err != nil {
		t.Fatal(err)
	}
	ev, err := logfile.UnmarshalLine(raw)
	if err != nil {
		t.Fatalf("event %s: %v", raw, err)
	}
	line, _ := logfile.MarshalLine(ev)
	return strings.TrimSuffix(string(line), "\n"), ev
}

func newSession() string { return "s-" + coreapi.NewCommandID() }

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
	reg := m.admin.ok("admin", "user", "create", "ada", k["publicKey"].(string), "--name", "Ada", "--kind", "human")
	if reg["keyId"] != k["keyId"] {
		t.Fatalf("register: %v", reg)
	}
	if w := u.ok("whoami"); w["participantId"] != "ada" || w["keyId"] != k["keyId"] {
		t.Fatalf("whoami: %v", w)
	}
	if out, _, code := u.run("whoami"); code != 0 || strings.TrimSpace(out) != "ada" {
		t.Fatalf("whoami (text): exit %d, %q", code, out)
	}
	// Only a system admin may create users.
	u.fails(1, "permission_denied", "admin", "user", "create", "eve", k["publicKey"].(string), "--name", "Eve", "--kind", "human")
	u.fails(2, "usage", "admin", "user", "create", "eve", "not-a-key")

	// The last key of a user cannot be revoked; with a second key it can.
	m.admin.fails(1, "failed_precondition", "admin", "key", "revoke", k["keyId"].(string))
	second, err := auth.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	m.admin.ok("admin", "key", "add", "ada", auth.FormatPublicKey(auth.PublicKey(second)))
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
	do := func(u *officeUser, wantType string, args ...string) *sessionstreamv1.Event {
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
	do(bot, events.ArtifactFocused, "stage", "focus", "slides/42")
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

// TestCLISlides: slides render and slides get against the real slides part of the hub.
func TestCLISlides(t *testing.T) {
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
	if v["id"] != "slides/1" || v["mediaType"] != "text/html; charset=utf-8" || v["duplicate"] != false {
		t.Fatalf("render Markdown: %v", v)
	}
	// The same deck as JSON is the same input: a new deck with the same content.
	if out, errOut, code := ada.run("slides", "render", js); code != 0 || out != "slides/2\n" {
		t.Fatalf("render JSON without --json: exit %d, %q %q", code, out, errOut)
	}

	html1, _, code := ada.run("slides", "get", "slides/1")
	html2, _, code2 := bot.run("slides", "get", "slides/2")
	if code != 0 || code2 != 0 || html1 != html2 || !strings.Contains(html1, "<li>&lt;two&gt;</li>") {
		t.Fatalf("slides get: exit %d/%d, same %v:\n%s", code, code2, html1 == html2, html1)
	}
	// With --json: the input and the output.
	a, _ := bot.ok("slides", "get", "slides/1")["deck"].(map[string]any)
	sl, _ := a["slides"].([]any)
	if a["id"] != "slides/1" || a["title"] != "Budget" || len(sl) != 1 || a["mediaType"] != "text/html; charset=utf-8" ||
		a["authorParticipantId"] != "bot" || a["authorKind"] != "AUTHOR_KIND_AGENT" || a["content"] == nil || a["createTime"] == nil {
		t.Fatalf("slides get --json: %v", a)
	}
	if first, _ := sl[0].(map[string]any); first["title"] != "Plan" || first["notes"] != "say this" {
		t.Fatalf("slides get --json, first slide: %v", sl[0])
	}
	out := filepath.Join(dir, "out.html")
	if v := ada.ok("slides", "get", "slides/2", "--out", out); v["file"] != out || v["id"] != "slides/2" {
		t.Fatalf("slides get --out: %v", v)
	}
	if b, err := os.ReadFile(out); err != nil || string(b) != html1 {
		t.Fatalf("--out file: %v", err)
	}

	bot.fails(1, "not_found", "slides", "get", "slides/99")
	bot.fails(1, "invalid_argument", "slides", "render", write("blank.json", `{"slides":[{"title":" "}]}`))
	bot.fails(2, "usage", "slides", "render", write("bad.md", "text before any slide\n"))
	bot.fails(2, "usage", "slides", "get")
}

// listOf returns the records under key of a list result with --json.
func listOf(t *testing.T, v map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := v[key].([]any)
	if !ok && v[key] != nil {
		t.Fatalf("%s is not a list: %v", key, v)
	}
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i], _ = r.(map[string]any)
	}
	return out
}

// idsOf returns the IDs of records, joined with commas.
func idsOf(rs []map[string]any) string {
	var got []string
	for _, r := range rs {
		got = append(got, fmt.Sprint(r["id"]))
	}
	return strings.Join(got, ",")
}

// TestCLIRecords: decision record and list, note add and list, and minutes show and list
// against the real hub, with the minutes that a session stores when it ends.
func TestCLIRecords(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")

	v := bot.ok("decision", "record", "We ship")
	d, _ := v["decision"].(map[string]any)
	if v["duplicate"] != false || d["id"] != float64(1) || d["text"] != "We ship" ||
		d["authorParticipantId"] != "bot" || d["authorKind"] != "AUTHOR_KIND_AGENT" || d["sessionId"] != nil ||
		d["commandId"] == nil || d["time"] == nil {
		t.Fatalf("decision record: %v", v)
	}
	f := filepath.Join(t.TempDir(), "decision.md")
	if err := os.WriteFile(f, []byte("Line one\nline two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d := ada.ok("decision", "record", "--session", "s1", "--file", f)["decision"].(map[string]any); d["text"] != "Line one\nline two" ||
		d["authorKind"] != "AUTHOR_KIND_HUMAN" || d["sessionId"] != "s1" {
		t.Fatalf("decision record from a file: %v", d)
	}
	if out, _, code := ada.run("decision", "record", "We wait"); code != 0 || out != "decision 3\n" {
		t.Fatalf("decision record without --json: exit %d, %q", code, out)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "1,2,3"},
		{[]string{"--session", "s1"}, "2"},
		{[]string{"--after", "1", "--limit", "1"}, "2"},
		{[]string{"--after", "3"}, ""},
	} {
		if got := idsOf(listOf(t, bot.ok(append([]string{"decision", "list"}, c.args...)...), "decisions")); got != c.want {
			t.Fatalf("decision list %v: %s, want %s", c.args, got, c.want)
		}
	}
	if out, _, code := ada.run("decision", "list", "--session", "s1"); code != 0 || out != "#2 ada: Line one line two\n" {
		t.Fatalf("decision list without --json: exit %d, %q", code, out)
	}

	v = ada.ok("note", "add", "--session", "s1", "A note")
	n, _ := v["note"].(map[string]any)
	if v["duplicate"] != false || n["id"] != float64(1) || n["text"] != "A note" || n["authorParticipantId"] != "ada" ||
		n["authorKind"] != "AUTHOR_KIND_HUMAN" || n["sessionId"] != "s1" {
		t.Fatalf("note add: %v", v)
	}
	if out, _, code := bot.run("note", "add", "Another note"); code != 0 || out != "note 2\n" {
		t.Fatalf("note add without --json: exit %d, %q", code, out)
	}
	if got := idsOf(listOf(t, bot.ok("note", "list"), "notes")); got != "1,2" {
		t.Fatalf("note list: %s", got)
	}
	if got := idsOf(listOf(t, bot.ok("note", "list", "--session", "s1"), "notes")); got != "1" {
		t.Fatalf("note list --session: %s", got)
	}
	if out, _, code := bot.run("note", "list", "--after", "1"); code != 0 || out != "#2 bot: Another note\n" {
		t.Fatalf("note list without --json: exit %d, %q", code, out)
	}

	bot.fails(2, "usage", "decision", "record")
	bot.fails(2, "usage", "decision", "record", "  ")
	bot.fails(2, "usage", "note", "add")
	bot.fails(2, "usage", "decision", "list", "extra")
	bot.fails(2, "usage", "minutes", "show", "a", "b")
	bot.fails(2, "usage", "journal", "list")
	bot.fails(1, "not_found", "minutes", "show", "no-such-session")

	// The minutes of a session are stored when it ends.
	sid := newSession()
	bot.join(sid)
	ada.join(sid)
	bot.ok("minutes", "update", "--session", sid, "Draft")
	bot.ok("minutes", "update", "--session", sid, "Final: we ship")
	bot.fails(1, "not_found", "minutes", "show", sid)
	ada.ok("meeting", "end", "--session", sid)
	mm, _ := ada.ok("minutes", "show", sid)["minutes"].(map[string]any)
	if mm["text"] != "Final: we ship" || mm["authorParticipantId"] != "bot" || mm["authorKind"] != "AUTHOR_KIND_AGENT" ||
		mm["sessionId"] != sid || mm["id"] != float64(1) {
		t.Fatalf("minutes show: %v", mm)
	}
	if out, _, code := ada.run("minutes", "show", "--session", sid); code != 0 || out != "Final: we ship\n" {
		t.Fatalf("minutes show without --json: exit %d, %q", code, out)
	}
	if got := listOf(t, ada.ok("minutes", "list", "--session", sid), "minutes"); len(got) != 1 || got[0]["text"] != "Final: we ship" {
		t.Fatalf("minutes list --session: %v", got)
	}
	if out, _, code := ada.run("minutes", "list"); code != 0 || out != "#1 "+sid+" bot: Final: we ship\n" {
		t.Fatalf("minutes list without --json: exit %d, %q", code, out)
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

// refsOfJSON returns the refs of tasks in a --json or MCP result, joined with commas.
func refsOfJSON(ts []map[string]any) string {
	var got []string
	for _, tk := range ts {
		got = append(got, fmt.Sprint(tk["ref"]))
	}
	return strings.Join(got, ",")
}

// TestCLITasks: task create, get, list and update against the real hub. Refs are passed back as
// printed; statuses have short names.
func TestCLITasks(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")

	v := bot.ok("task", "create", "Write", "the", "notes", "--assignee", "ada", "--description", "Line one\nline two")
	t1, _ := v["task"].(map[string]any)
	ref1, _ := t1["ref"].(string)
	if v["duplicate"] != false || ref1 == "" || t1["title"] != "Write the notes" || t1["status"] != "TASK_STATUS_OPEN" ||
		t1["source"] != "TASK_SOURCE_LOCAL" || t1["assigneeParticipantId"] != "ada" || t1["authorParticipantId"] != "bot" ||
		t1["authorKind"] != "AUTHOR_KIND_AGENT" || t1["description"] != "Line one\nline two" || t1["sessionId"] != nil ||
		t1["revision"] != float64(1) {
		t.Fatalf("task create: %v", v)
	}
	f := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(f, []byte("From a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	t2, _ := ada.ok("task", "create", "Book the room", "--session", "s1", "--status", "in-progress", "--description-file", f)["task"].(map[string]any)
	ref2, _ := t2["ref"].(string)
	if t2["status"] != "TASK_STATUS_IN_PROGRESS" || t2["sessionId"] != "s1" || t2["description"] != "From a file" ||
		t2["authorKind"] != "AUTHOR_KIND_HUMAN" {
		t.Fatalf("task create with a session: %v", t2)
	}
	out, _, code := ada.run("task", "create", "Third")
	ref3 := strings.TrimPrefix(strings.TrimSpace(out), "task ")
	if code != 0 || !strings.HasPrefix(out, "task ") || ref3 == "" || ref3 == ref1 || ref3 == ref2 {
		t.Fatalf("task create without --json: exit %d, %q", code, out)
	}

	if g, _ := ada.ok("task", "get", ref1)["task"].(map[string]any); g["ref"] != ref1 || g["title"] != "Write the notes" {
		t.Fatalf("task get: %v", g)
	}
	if out, _, code := ada.run("task", "get", ref1); code != 0 ||
		out != ref1+" [open] Write the notes\nassignee: ada\nauthor: bot\nrevision: 1\n\nLine one\nline two\n" {
		t.Fatalf("task get without --json: exit %d, %q", code, out)
	}

	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, ref1 + "," + ref2 + "," + ref3},
		{[]string{"--status", "open"}, ref1 + "," + ref3},
		{[]string{"--status", "in_progress,done"}, ref2},
		{[]string{"--assignee", "ada"}, ref1},
		{[]string{"--session", "s1"}, ref2},
	} {
		if got := refsOfJSON(listOf(t, bot.ok(append([]string{"task", "list"}, c.args...)...), "tasks")); got != c.want {
			t.Fatalf("task list %v: %s, want %s", c.args, got, c.want)
		}
	}
	p1 := bot.ok("task", "list", "--limit", "2")
	next, _ := p1["nextPageToken"].(string)
	if refsOfJSON(listOf(t, p1, "tasks")) != ref1+","+ref2 || next == "" {
		t.Fatalf("task list --limit 2: %v", p1)
	}
	if p2 := bot.ok("task", "list", "--limit", "2", "--page-token", next); refsOfJSON(listOf(t, p2, "tasks")) != ref3 || p2["nextPageToken"] != "" {
		t.Fatalf("task list page 2: %v", p2)
	}
	if out, errOut, code := ada.run("task", "list", "--limit", "1"); code != 0 || out != ref1+" [open] @ada Write the notes\n" ||
		!strings.Contains(errOut, "--page-token") {
		t.Fatalf("task list without --json: exit %d, %q, %q", code, out, errOut)
	}

	u, _ := ada.ok("task", "update", ref1, "--status", "done", "--title", "Notes written", "--session", "s2")["task"].(map[string]any)
	if u["status"] != "TASK_STATUS_DONE" || u["title"] != "Notes written" || u["sessionId"] != "s2" || u["revision"] != float64(2) ||
		u["updatedByParticipantId"] != "ada" || u["assigneeParticipantId"] != "ada" || u["description"] != "Line one\nline two" {
		t.Fatalf("task update: %v", u)
	}
	u, _ = bot.ok("task", "update", ref1, "--assignee", "", "--session", "", "--description", "")["task"].(map[string]any)
	if u["assigneeParticipantId"] != nil || u["sessionId"] != nil || u["description"] != nil || u["revision"] != float64(3) {
		t.Fatalf("task update that removes fields: %v", u)
	}
	if out, _, code := bot.run("task", "update", ref2, "--status", "cancelled"); code != 0 || out != "task "+ref2+" revision 2\n" {
		t.Fatalf("task update without --json: exit %d, %q", code, out)
	}
	if got := refsOfJSON(listOf(t, bot.ok("task", "list", "--status", "done,cancelled"), "tasks")); got != ref1+","+ref2 {
		t.Fatalf("task list after updates: %s", got)
	}

	bot.fails(2, "usage", "task", "create")
	bot.fails(2, "usage", "task", "create", "x", "--status", "later")
	bot.fails(2, "usage", "task", "create", "x", "--description", "a", "--description-file", f)
	bot.fails(2, "usage", "task", "get")
	bot.fails(2, "usage", "task", "list", "extra")
	bot.fails(2, "usage", "task", "list", "--status", "someday")
	bot.fails(2, "usage", "task", "update", ref1)
	bot.fails(2, "usage", "task", "update", ref1, "--status", "later")
	bot.fails(1, "not_found", "task", "get", "local:999")
	bot.fails(1, "not_found", "task", "update", "github:owner/repo#1", "--status", "done")
	bot.fails(1, "invalid_argument", "task", "update", ref1, "--title", " ")
}

// TestCLIDocuments: document create, change, get and list against the real hub. A change needs
// the version that was read; a stale one fails with aborted.
func TestCLIDocuments(t *testing.T) {
	m := newMachine(t)
	bot := m.user("bot", "Bot", "agent")
	ada := m.user("ada", "Ada", "human")
	dir := t.TempDir()
	file := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(file, []byte("# Plan\n\nTwo lines.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	v := ada.ok("document", "create", "documents/plan.md", "--file", file)
	if v["path"] != "documents/plan.md" || v["version"] == "" || v["duplicate"] != false {
		t.Fatalf("create: %v", v)
	}
	base, _ := v["version"].(string)
	out, _, code := bot.run("document", "get", "documents/plan.md")
	if code != 0 || out != "# Plan\n\nTwo lines.\n" {
		t.Fatalf("get: exit %d, %q", code, out)
	}
	if g := bot.ok("document", "get", "documents/plan.md"); g["version"] != base || g["content"] != "# Plan\n\nTwo lines.\n" {
		t.Fatalf("get --json: %v", g)
	}
	// The bot changes it on the version it read; Ada's change on the same version is refused.
	if c := bot.ok("document", "change", "documents/plan.md", "--base", base, "Short."); c["version"] == base {
		t.Fatalf("change: %v", c)
	}
	ada.fails(1, "aborted", "document", "change", "documents/plan.md", "--base", base, "Mine.")
	ada.fails(1, "already_exists", "document", "create", "documents/plan.md", "again")
	ada.fails(2, "usage", "document", "change", "documents/plan.md", "Mine.")
	ada.fails(1, "not_found", "document", "get", "documents/none.md")
	list, _ := ada.ok("document", "list")["files"].([]any)
	if first, _ := list[0].(map[string]any); len(list) != 1 || first["path"] != "documents/plan.md" {
		t.Fatalf("list: %v", list)
	}
	outFile := filepath.Join(dir, "out.md")
	if g := ada.ok("document", "get", "documents/plan.md", "--out", outFile); g["file"] != outFile {
		t.Fatalf("get --out: %v", g)
	}
	if b, _ := os.ReadFile(outFile); string(b) != "Short." {
		t.Fatalf("file written by get --out: %q", b)
	}
}

// TestCLIAdminLink: office admin link gives a link for admin keys and refuses other participants.
func TestCLIAdminLink(t *testing.T) {
	m := newMachine(t)
	bob := m.user("bob", "Bob", "human")
	v := m.admin.ok("admin", "link")
	if u, _ := v["url"].(string); !strings.Contains(u, "/admin/login?code=") || v["expiresAt"] == nil {
		t.Fatalf("admin link: %v", v)
	}
	if out, _, code := m.admin.run("admin", "link"); code != 0 || !strings.Contains(out, "/admin/login?code=") {
		t.Errorf("without --json: exit %d, %q", code, out)
	}
	bob.fails(1, "permission_denied", "admin", "link")
	bob.fails(2, "usage", "admin", "link", "extra")
}

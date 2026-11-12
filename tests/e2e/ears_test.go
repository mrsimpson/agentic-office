//go:build e2e

package e2e

// End-to-end tests of the ears (bb-ears) with real processes: office-hub, an agent "bot"
// that only follows its local log, and a human "ada" who joins with "office meeting join
// --wav" (or --mic with a mock recorder). They need the Whisper and the VAD model (see
// earsModels). Without the models, the tests are skipped.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
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

const testWAV = "testdata/german-3-sentences.wav"

// keyWords of the three test sentences. A transcript matches a sentence if it contains all of
// them (case-insensitive). The words are chosen so that small Whisper errors elsewhere in the
// sentence do not matter (the small model writes "Pusines Plan" for "Businessplan").
var keyWords = [][]string{{"guten", "morgen"}, {"besprechen", "büro"}, {"zahlen", "freitag"}}

// sentenceOf returns the index of the test sentence that text matches, or -1.
func sentenceOf(text string) int {
	l := strings.ToLower(text)
	for i, words := range keyWords {
		ok := true
		for _, w := range words {
			ok = ok && strings.Contains(l, w)
		}
		if ok {
			return i
		}
	}
	return -1
}

// earsModels returns the environment with the model paths, or skips the test. A model is taken
// from its environment variable, else from the model directory of the ears ("make models"),
// else from where it is on the reference host.
func earsModels(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data := envOr("XDG_DATA_HOME", filepath.Join(os.Getenv("HOME"), ".local", "share"))
	dir := filepath.Join(data, "agentic-office", "models")
	find := func(env, what string, candidates ...string) string {
		if v := os.Getenv(env); v != "" {
			candidates = []string{v}
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return env + "=" + c
			}
		}
		t.Skipf("skipped: the ears test needs the %s (tried %v); run \"make models\" or set %s", what, candidates, env)
		return ""
	}
	return []string{
		find("OFFICE_WHISPER_MODEL", "Whisper model", filepath.Join(dir, "ggml-small.bin"), "/srv/localai/models/whisper/ggml-small.bin"),
		find("OFFICE_VAD_MODEL", "VAD model", filepath.Join(dir, "ggml-silero-v6.2.0.bin"),
			filepath.Join(root, "third_party/whisper.cpp/models/ggml-silero-v6.2.0.bin")),
	}
}

// meeting is a server with an admin, and a runtime directory on one machine.
type meeting struct {
	t       *testing.T
	srv     *contracttest.Hub
	runtime string
	admin   []string
	sid     string
}

func newMeeting(t *testing.T) *meeting {
	t.Helper()
	srv := contracttest.StartHub(t, nil)
	m := &meeting{t: t, srv: srv, runtime: filepath.Join(t.TempDir(), "run"), sid: "ears-" + coreapi.NewCommandID()[:8]}
	adminKey := filepath.Join(t.TempDir(), "admin.pem")
	if err := auth.WriteKey(adminKey, srv.AdminKey); err != nil {
		t.Fatal(err)
	}
	m.admin = m.envFor(adminKey, srv.URL)
	return m
}

func (m *meeting) envFor(key, server string) []string {
	return []string{"OFFICE_HUB=" + server, "OFFICE_CA_CERT=" + m.srv.CertFile, "OFFICE_KEY=" + key,
		"OFFICE_RUNTIME_DIR=" + m.runtime, "OFFICE_SESSION=", "OFFICE_JSON="}
}

func (m *meeting) office(env []string, args ...string) map[string]any {
	m.t.Helper()
	out, errOut, code := contracttest.Run(m.t, "office", env, append(args, "--json")...)
	var v map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil {
		m.t.Fatalf("office %v: exit %d, %q, %q", args, code, out, errOut)
	}
	return v
}

// user creates and registers a key. Its CLI talks to server (the server or a proxy).
func (m *meeting) user(pid, kind, server string) []string {
	m.t.Helper()
	env := m.envFor(filepath.Join(m.t.TempDir(), pid+".pem"), server)
	k := m.office(env, "key", "create")
	m.office(m.admin, "admin", "user", "create", pid, k["publicKey"].(string), "--name", pid, "--kind", kind)
	return env
}

// joinBot joins the agent without ears and follows its local log.
func (m *meeting) joinBot() *logWatch {
	m.t.Helper()
	env := m.user("bot", "agent", m.srv.URL)
	v := m.office(env, "meeting", "join", m.sid)
	pid := int(v["replicatorPid"].(float64))
	m.t.Cleanup(func() { killPID(pid) })
	return watchLog(m.t, v["logPath"].(string))
}

// logWatch follows a local log and records when each line appeared.
type logWatch struct {
	path string
	mu   sync.Mutex
	evs  []*sessionstreamv1.Event
	at   []time.Time
}

func watchLog(t *testing.T, path string) *logWatch {
	w := &logWatch{path: path}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		var off int64
		var partial []byte
		for {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Millisecond):
			}
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			st, _ := f.Stat()
			if st.Size() > off {
				buf := make([]byte, st.Size()-off)
				n, _ := f.ReadAt(buf, off)
				now := time.Now()
				off += int64(n)
				partial = append(partial, buf[:n]...)
				for {
					i := strings.IndexByte(string(partial), '\n')
					if i < 0 {
						break
					}
					if ev, err := logfile.UnmarshalLine(partial[:i+1]); err == nil {
						w.mu.Lock()
						w.evs, w.at = append(w.evs, ev), append(w.at, now)
						w.mu.Unlock()
					}
					partial = partial[i+1:]
				}
			}
			f.Close()
		}
	}()
	return w
}

func (w *logWatch) events() ([]*sessionstreamv1.Event, []time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.evs), slices.Clone(w.at)
}

// utterances returns the utterance.final events of participant and when they appeared.
func (w *logWatch) utterances(participant string) ([]*sessionstreamv1.Event, []time.Time) {
	evs, at := w.events()
	var out []*sessionstreamv1.Event
	var times []time.Time
	for i, ev := range evs {
		if ev.GetType() == events.UtteranceFinal && ev.GetUtteranceFinal().GetParticipantId() == participant {
			out, times = append(out, ev), append(times, at[i])
		}
	}
	return out, times
}

func (w *logWatch) waitUtterances(t *testing.T, participant string, n int, timeout time.Duration) ([]*sessionstreamv1.Event, []time.Time) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		evs, at := w.utterances(participant)
		if len(evs) >= n {
			return evs, at
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %d of %d utterances of %s in %s", len(evs), n, participant, w.path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// earsReport is one {"ears":{…}} line on stderr of join --wav --json.
type earsReport struct {
	Type                string     `json:"type"`
	CommandID           string     `json:"commandId"`
	Text                string     `json:"text"`
	EventID             uint32     `json:"eventId"`
	SpeechEnd           *time.Time `json:"speechEnd"`
	TranscribedAt       *time.Time `json:"transcribedAt"`
	SubmittedAt         *time.Time `json:"submittedAt"`
	AudioMs             int64      `json:"audioMs"`
	WhisperMs           int64      `json:"whisperMs"`
	SpeechEndToFinalMs  int64      `json:"speechEndToFinalMs"`
	SpeechEndToSubmitMs int64      `json:"speechEndToSubmitMs"`
	Attempts            int        `json:"attempts"`
	Duplicate           bool       `json:"duplicate"`
	Error               string     `json:"error"`
	Model               string     `json:"model"`
	Language            string     `json:"language"`
	VocabularyFile      string     `json:"vocabularyFile"`
	Terms               int        `json:"terms"`
	Names               int        `json:"names"`
	Cut                 int        `json:"cut"`
	Prompt              string     `json:"prompt"`
	seen                time.Time
}

// ears is a running "office meeting join --wav|--mic --json".
type ears struct {
	t    *testing.T
	p    *contracttest.Process
	mu   sync.Mutex
	view []map[string]any
	// viewAt is when the test read each line of view.
	viewAt []time.Time
	seen   map[int]time.Time // stderr line index -> when the test saw it
}

func (m *meeting) startEars(env []string, args ...string) *ears {
	m.t.Helper()
	return m.startEarsIn("", env, args...)
}

// startEarsIn is startEars in the working directory dir.
func (m *meeting) startEarsIn(dir string, env []string, args ...string) *ears {
	m.t.Helper()
	p := contracttest.StartIn(m.t, dir, "office", env, append([]string{"meeting", "join", m.sid, "--json"}, args...)...)
	e := &ears{t: m.t, p: p, seen: map[int]time.Time{}}
	line, err := p.Stdout.ReadString('\n')
	var j map[string]any
	if err != nil || json.Unmarshal([]byte(line), &j) != nil {
		m.t.Fatalf("join: %q %v\n%s", line, err, p.Stderr())
	}
	if pid, ok := j["replicatorPid"].(float64); ok {
		m.t.Cleanup(func() { killPID(int(pid)) })
	}
	go func() {
		r := bufio.NewReader(p.Stdout)
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				return
			}
			var v map[string]any
			if json.Unmarshal([]byte(l), &v) == nil {
				e.mu.Lock()
				e.view, e.viewAt = append(e.view, v), append(e.viewAt, time.Now())
				e.mu.Unlock()
			}
		}
	}()
	return e
}

// reports returns the ears reports so far, with the time the test first saw each one.
func (e *ears) reports() []earsReport {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []earsReport
	now := time.Now()
	for i, l := range strings.Split(e.p.Stderr(), "\n") {
		var v struct {
			Ears *earsReport `json:"ears"`
		}
		if !strings.HasPrefix(l, `{"ears":`) || json.Unmarshal([]byte(l), &v) != nil || v.Ears == nil {
			continue
		}
		if _, ok := e.seen[i]; !ok {
			e.seen[i] = now
		}
		v.Ears.seen = e.seen[i]
		out = append(out, *v.Ears)
	}
	return out
}

func (e *ears) ofType(typ string) []earsReport {
	var out []earsReport
	for _, r := range e.reports() {
		if r.Type == typ {
			out = append(out, r)
		}
	}
	return out
}

// wait waits until there are n reports of the type and returns them.
func (e *ears) wait(typ string, n int, timeout time.Duration) []earsReport {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if rs := e.ofType(typ); len(rs) >= n {
			return rs
		}
		select {
		case <-e.p.Done():
			e.t.Fatalf("ears exited before %d %q reports:\n%s", n, typ, e.p.Stderr())
		case <-time.After(10 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("timeout waiting for %d %q reports:\n%s", n, typ, e.p.Stderr())
		}
	}
}

// waitView waits for a change of the gateway view that matches.
func (e *ears) waitView(what string, match func(map[string]any) bool, timeout time.Duration) {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		found := slices.ContainsFunc(e.view, match)
		e.mu.Unlock()
		if found {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("timeout waiting for %s in the gateway view\n%s", what, e.p.Stderr())
}

func (e *ears) key(k string) {
	e.t.Helper()
	if _, err := e.p.Stdin.Write([]byte(k)); err != nil {
		e.t.Fatal(err)
	}
}

// quit presses q and waits until join has ended.
func (e *ears) quit() {
	e.t.Helper()
	e.key("q")
	select {
	case <-e.p.Done():
		if c := e.p.Cmd.ProcessState.ExitCode(); c != 0 {
			e.t.Fatalf("join exited with %d:\n%s", c, e.p.Stderr())
		}
	case <-time.After(10 * time.Second):
		e.t.Fatalf("join did not quit on q:\n%s", e.p.Stderr())
	}
}

// checkUtterances checks the utterances of ada in a log against the final reports of the ears:
// the same command IDs in the same order, each once, with modality voice. want are the
// expected test sentences in order.
func checkUtterances(t *testing.T, got []*sessionstreamv1.Event, finals []earsReport, want []int) {
	t.Helper()
	if len(got) != len(want) || len(finals) != len(want) {
		t.Fatalf("want %d utterances, got %d in the log and %d final in the ears: %v", len(want), len(got), len(finals), texts(got))
	}
	seen := map[string]bool{}
	for i, ev := range got {
		u := ev.GetUtteranceFinal()
		switch {
		case seen[ev.GetCommandId()]:
			t.Fatalf("utterance %d: command ID %s twice", i, ev.GetCommandId())
		case ev.GetCommandId() != finals[i].CommandID:
			t.Fatalf("utterance %d: command ID %s, the ears made %s (order differs)", i, ev.GetCommandId(), finals[i].CommandID)
		case u.GetModality() != sessionstreamv1.Modality_MODALITY_VOICE:
			t.Fatalf("utterance %d: modality %v", i, u.GetModality())
		case u.GetTranscribedAt() == nil:
			t.Fatalf("utterance %d: no transcribed_at", i)
		case sentenceOf(u.GetText()) != want[i]:
			t.Fatalf("utterance %d: %q is not test sentence %d (key words %v)", i, u.GetText(), want[i]+1, keyWords[want[i]])
		}
		seen[ev.GetCommandId()] = true
	}
}

func texts(evs []*sessionstreamv1.Event) []string {
	var out []string
	for _, ev := range evs {
		out = append(out, ev.GetUtteranceFinal().GetText())
	}
	return out
}

// printLatency prints, per sentence, Whisper time, end of speech to submit, transcript to the
// other participant's log and end of speech to that log. With bound, it fails if the pipeline is
// far too slow (not for utterances that were queued during a network drop).
func printLatency(t *testing.T, finals, sent []earsReport, got []*sessionstreamv1.Event, at []time.Time, bound bool) {
	t.Helper()
	byCmd := map[string]earsReport{}
	for _, s := range sent {
		byCmd[s.CommandID] = s
	}
	var toLog, speechToLog []time.Duration
	for i, f := range finals {
		s := byCmd[f.CommandID]
		var logAt time.Time
		for j, ev := range got {
			if ev.GetCommandId() == f.CommandID {
				logAt = at[j]
			}
		}
		tl, sl := logAt.Sub(*f.TranscribedAt), logAt.Sub(*f.SpeechEnd)
		toLog, speechToLog = append(toLog, tl), append(speechToLog, sl)
		t.Logf("sentence %d: audio %4d ms | whisper %4d ms | end of speech to final %4d ms, to submit %4d ms | "+
			"transcript to bot's log %5.1f ms | end of speech to bot's log %4d ms | %q",
			i+1, f.AudioMs, f.WhisperMs, f.SpeechEndToFinalMs, s.SpeechEndToSubmitMs,
			float64(tl.Microseconds())/1000, sl.Milliseconds(), f.Text)
	}
	t.Logf("max: transcript to bot's log %v, end of speech to bot's log %v", slices.Max(toLog).Round(100*time.Microsecond),
		slices.Max(speechToLog).Round(time.Millisecond))
	// Loose bounds that catch a broken pipeline, but only in the performance tests: the end-to-end
	// tests never fail on timing (dec-performance-tests). The quality scenarios are in quality_test.go.
	if assertTiming && bound && (slices.Max(speechToLog) > 5*time.Second || slices.Max(toLog) > time.Second) {
		t.Fatalf("too slow: end of speech to log %v, transcript to log %v", slices.Max(speechToLog), slices.Max(toLog))
	}
}

// TestEarsGermanWAV: Ada's ears play the German test WAV in real time. The three sentences reach
// the bot's local log in order, as voice utterances with the key words. Ada's client talks to
// the server only through a proxy that decrypts and records what she sends (qs-no-raw-audio):
// every byte must belong to a known RPC request with text and metadata only.
func TestEarsGermanWAV(t *testing.T) {
	models := earsModels(t)
	m := newMeeting(t)
	proxy := contracttest.StartDecryptingProxy(t, m.srv.URL, m.srv.CertFile, filepath.Join(m.srv.DataDir, "tls.key"))
	ada := append(m.user("ada", "human", proxy.URL), models...)
	bot := m.joinBot()

	e := m.startEars(ada, "--wav", testWAV)
	e.wait("done", 1, 60*time.Second)
	got, at := bot.waitUtterances(t, "ada", 3, 10*time.Second)
	finals, sent := e.ofType("final"), e.ofType("sent")
	checkUtterances(t, got, finals, []int{0, 1, 2})
	e.waitView("status sent", func(v map[string]any) bool {
		u, _ := v["utterance"].(map[string]any)
		return u != nil && u["status"] == "sent" && u["commandId"] == finals[2].CommandID
	}, 5*time.Second)
	e.quit()
	printLatency(t, finals, sent, got, at, true)

	// No raw audio: decode everything Ada's client sent.
	audio := readPCM(t, testWAV)
	pcm16k := int64(audio.duration().Seconds() * 16000 * 2)
	transcripts := map[string]bool{}
	for _, f := range finals {
		transcripts[f.Text] = true
	}
	var plain, bodies int64
	var largest int
	calls := map[string]int{}
	for i, conn := range proxy.Captured() {
		plain += int64(len(conn))
		reqs, err := contracttest.DecodeRequests(conn)
		if err != nil {
			t.Fatalf("connection %d: not every byte is a known request: %v", i, err)
		}
		for _, r := range reqs {
			calls[r.Path]++
			bodies += int64(len(r.Body))
			largest = max(largest, len(r.Body))
			for _, msg := range r.Messages {
				fields, unknown := contracttest.Fields(msg)
				if unknown > 0 {
					t.Fatalf("%s: %d bytes of unknown fields", r.Path, unknown)
				}
				for _, f := range fields {
					switch {
					case f.Name == "office.sessioncommands.v1.SubmitUtteranceRequest.text":
						if !transcripts[f.Value] {
							t.Fatalf("submitted text %q is not a transcript of the ears", f.Value)
						}
					case f.Size > 128:
						t.Fatalf("%s: field %s has %d bytes; only transcripts may be long", r.Path, f.Name, f.Size)
					}
				}
			}
		}
	}
	if calls["/office.sessioncommands.v1.SessionCommandsService/SubmitUtterance"] != 3 {
		t.Fatalf("want 3 SubmitUtterance requests, got %v", calls)
	}
	t.Logf("no raw audio: %d connections; requests %v", len(proxy.Captured()), calls)
	t.Logf("no raw audio: upstream %d bytes on the wire (TLS), %d bytes plaintext, %d bytes in request bodies "+
		"(largest %d); the audio is %v = %d bytes as 16 kHz 16-bit PCM", proxy.UpstreamBytes(), plain, bodies, largest,
		audio.duration().Round(10*time.Millisecond), pcm16k)
	if proxy.UpstreamBytes() > pcm16k/10 || bodies > 4096 {
		t.Fatalf("too much upstream traffic for text only: %d bytes on the wire, %d bytes of bodies", proxy.UpstreamBytes(), bodies)
	}
}

// TestEarsNetworkDrop: the network of Ada drops after her first sentence and comes back after
// two more sentences are final (rs-network-drop, qs-resume-after-drop). The ears queue them and
// send them in order with the same command IDs: no lost and no duplicate utterances. Ada's own
// local log has them within 2 s after the network is back.
func TestEarsNetworkDrop(t *testing.T) {
	slow(t)
	models := earsModels(t)
	m := newMeeting(t)
	proxy := contracttest.StartProxy(t, m.srv.URL)
	ada := append(m.user("ada", "human", proxy.URL), models...)
	bot := m.joinBot()

	// The three sentences twice: six utterances.
	s := readPCM(t, testWAV).sentences()
	if len(s) != 3 {
		t.Fatalf("test WAV: want 3 sentences, found %d", len(s))
	}
	wav := filepath.Join(t.TempDir(), "six.wav")
	join(s[0].rate, 1200*time.Millisecond, s[0], s[1], s[2], s[0], s[1], s[2]).writeWAV(t, wav)

	e := m.startEars(ada, "--wav", wav)
	adaLog := watchLog(t, filepath.Join(m.runtime, m.sid, "ada.jsonl"))
	j := e.wait("sent", 1, 30*time.Second)
	proxy.Cut()
	cutAt := time.Now()
	t.Logf("network cut after sentence 1 (event %d)", j[0].EventID)
	e.wait("final", 3, 30*time.Second)
	e.wait("retry", 1, 10*time.Second)
	if n := len(e.ofType("sent")); n != 1 {
		t.Fatalf("%d utterances sent while the network was cut", n)
	}
	queued := len(e.ofType("final")) - 1
	proxy.Restore()
	restoredAt := time.Now()
	t.Logf("network restored after %v with %d utterances queued", restoredAt.Sub(cutAt).Round(time.Millisecond), queued)

	sent := e.wait("sent", 1+queued, 10*time.Second)
	t.Logf("queued utterances sent %v after restore", sent[queued].seen.Sub(restoredAt).Round(time.Millisecond))
	e.wait("done", 1, 60*time.Second)
	got, at := bot.waitUtterances(t, "ada", 6, 10*time.Second)
	finals := e.ofType("final")
	checkUtterances(t, got, finals, []int{0, 1, 2, 0, 1, 2})
	dups := 0
	for _, s := range e.ofType("sent") {
		if s.Duplicate {
			dups++
		}
	}

	// Ada's own log (through the proxy) catches up with the bot's log.
	evs, _ := bot.events()
	last := evs[len(evs)-1].GetId()
	deadline := time.Now().Add(15 * time.Second)
	for {
		a, _ := adaLog.events()
		if len(a) > 0 && a[len(a)-1].GetId() >= last {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Ada's local log did not catch up: %d events, want up to ID %d", len(a), last)
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.quit()
	a, aAt := adaLog.events()
	catchUp := time.Duration(-1)
	for i, ev := range a {
		if ev.GetId() != uint32(i+1) {
			t.Fatalf("Ada's log: line %d has ID %d (a gap or a duplicate)", i+1, ev.GetId())
		}
		if ev.GetId() == sent[queued].EventID {
			catchUp = aAt[i].Sub(restoredAt)
		}
	}
	if catchUp < 0 {
		t.Fatalf("Ada's log does not have the last queued utterance (event %d)", sent[queued].EventID)
	}
	t.Logf("Ada's local log has the queued utterances %v after restore (replicator reconnect included)",
		catchUp.Round(time.Millisecond))
	// qs-resume-after-drop: the local log is complete within 2 s after the network is back.
	if catchUp > 2*time.Second {
		t.Fatalf("Ada's local log has the queued utterances only %v after restore, want at most 2 s", catchUp)
	}
	t.Logf("network drop: %d utterances, 0 lost, 0 duplicate; %d resends answered as duplicate; %d connections refused while cut",
		len(got), dups, proxy.Refused())
	printLatency(t, finals, e.ofType("sent"), got, at, false)
}

// TestEarsPause: Ada pauses after her first sentence, speaks the second one while paused and
// resumes before the third. The second sentence is never transcribed; the log has
// participant.paused and participant.resumed, and no utterance of Ada between them. The pause
// ends her first turn without turn.end; only the turn of the third sentence ends.
func TestEarsPause(t *testing.T) {
	slow(t)
	models := earsModels(t)
	m := newMeeting(t)
	ada := append(m.user("ada", "human", m.srv.URL), models...)
	bot := m.joinBot()

	s := readPCM(t, testWAV).sentences()
	// The key press comes after the first sentence is sent, so the gap must be longer than one
	// transcription plus the start of the model on a slow host (about 5 s on a 2-core CPU);
	// e2e tests never fail on timing (dec-performance-tests).
	const gap = 15 * time.Second
	// The turn silence is longer than the time to the key press, so the first turn can end only
	// after the pause, and it is shorter than the silence after the third sentence.
	const turnSilence = 16 * time.Second
	wav := filepath.Join(t.TempDir(), "pause.wav")
	audio := join(s[0].rate, gap, s[0], s[1])
	audio.samples = append(audio.samples, join(s[0].rate, turnSilence+4*time.Second, s[2]).samples...)
	audio.writeWAV(t, wav)
	// The second sentence starts at s0+gap and ends at s0+gap+s1.
	s2End := s[0].duration() + gap + s[1].duration()

	e := m.startEars(ada, "--wav", wav, "--turn-silence", turnSilence.String())
	start := e.wait("listening", 1, 30*time.Second)[0].seen
	e.wait("sent", 1, 30*time.Second)
	e.key("p")
	e.waitView("paused", func(v map[string]any) bool { return v["paused"] == true }, 5*time.Second)
	if since := time.Since(start); since > s[0].duration()+gap-500*time.Millisecond {
		t.Fatalf("paused only %v after the start; the second sentence starts at %v", since, s[0].duration()+gap)
	}
	time.Sleep(time.Until(start.Add(s2End + time.Second)))
	e.key("p")
	e.waitView("resumed", func(v map[string]any) bool { return v["paused"] == false }, 5*time.Second)
	e.wait("done", 1, 60*time.Second)
	got, _ := bot.waitUtterances(t, "ada", 2, 10*time.Second)
	checkUtterances(t, got, e.ofType("final"), []int{0, 2})
	// The WAV ends with a silence longer than the turn silence: the turn of the third sentence ends.
	bot.waitTurnEnds(t, "ada", 1, 10*time.Second)
	e.quit()

	// The pause ends the first turn without turn.end. Without that, its turn silence would be
	// over shortly after the resume, and a turn.end would come before the third sentence.
	evs, _ := bot.events()
	o := order(evs, "ada")
	want := []string{"utterance 1", events.ParticipantPaused, events.ParticipantResumed, "utterance 3", events.TurnEnd}
	if !slices.Equal(o, want) {
		t.Fatalf("log order %v, want %v", o, want)
	}
	t.Logf("pause: log order %v", o)
}

// TestEarsMicMockRecorder: --mic reads the microphone through a recorder program. There is no
// microphone on the test host, so OFFICE_MIC_COMMAND plays the test sentences as raw PCM (not in
// real time) and then stays open like a recorder. The sentences arrive in order.
func TestEarsMicMockRecorder(t *testing.T) {
	models := earsModels(t)
	m := newMeeting(t)
	raw := filepath.Join(t.TempDir(), "mic.s16")
	if err := os.WriteFile(raw, readPCM(t, testWAV).raw16k(), 0o600); err != nil {
		t.Fatal(err)
	}
	ada := append(m.user("ada", "human", m.srv.URL), models...)
	ada = append(ada, "OFFICE_MIC_COMMAND=cat '"+raw+"'; exec sleep 600")
	bot := m.joinBot()

	e := m.startEars(ada, "--mic")
	e.wait("listening", 1, 30*time.Second)
	got, _ := bot.waitUtterances(t, "ada", 3, 60*time.Second)
	checkUtterances(t, got, e.ofType("final")[:3], []int{0, 1, 2})
	e.quit()
	var ws []int64
	for _, f := range e.ofType("final") {
		ws = append(ws, f.WhisperMs)
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i] < ws[j] })
	t.Logf("mic (mock recorder): 3 sentences; whisper ms %v", ws)
}

// order returns, in the order of a log, the utterances of participant as "utterance N" (N is the
// test sentence), the turn.end events of participant, and participant.paused and resumed.
func order(evs []*sessionstreamv1.Event, participant string) []string {
	var out []string
	for _, ev := range evs {
		switch ev.GetType() {
		case events.ParticipantPaused, events.ParticipantResumed:
			out = append(out, ev.GetType())
		case events.UtteranceFinal:
			if ev.GetUtteranceFinal().GetParticipantId() == participant {
				out = append(out, fmt.Sprintf("utterance %d", sentenceOf(ev.GetUtteranceFinal().GetText())+1))
			}
		case events.TurnEnd:
			if ev.GetTurnEnd().GetParticipantId() == participant {
				out = append(out, events.TurnEnd)
			}
		}
	}
	return out
}

// waitTurnEnds waits until the log has n turn.end events of participant.
func (w *logWatch) waitTurnEnds(t *testing.T, participant string, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		evs, _ := w.events()
		if c := strings.Count(strings.Join(order(evs, participant), ","), events.TurnEnd); c >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: want %d turn.end of %s in %s, have %v", n, participant, w.path, order(evs, participant))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEarsTurnEnd: Ada speaks the first two test sentences 1.2 s apart, then waits 3 s, speaks
// the third sentence and waits 3 s again. With the default turn silence (1.8 s), these are two
// turns. The ears submit one turn.end of Ada after each turn, behind its sentences, under the
// command IDs of their turn-end reports. The short pause inside the first turn ends no turn.
func TestEarsTurnEnd(t *testing.T) {
	slow(t)
	models := earsModels(t)
	m := newMeeting(t)
	ada := append(m.user("ada", "human", m.srv.URL), models...)
	bot := m.joinBot()

	s := readPCM(t, testWAV).sentences()
	if len(s) != 3 {
		t.Fatalf("test WAV: want 3 sentences, found %d", len(s))
	}
	first := join(s[0].rate, 1200*time.Millisecond, s[0])
	rest := join(s[0].rate, 3*time.Second, s[1], s[2])
	wav := filepath.Join(t.TempDir(), "turns.wav")
	pcm{rate: s[0].rate, samples: append(first.samples, rest.samples...)}.writeWAV(t, wav)

	e := m.startEars(ada, "--wav", wav)
	e.wait("done", 1, 5*time.Minute)
	bot.waitTurnEnds(t, "ada", 2, 10*time.Second)
	got, _ := bot.waitUtterances(t, "ada", 3, 10*time.Second)
	checkUtterances(t, got, e.ofType("final"), []int{0, 1, 2})
	e.quit()

	evs, _ := bot.events()
	o := order(evs, "ada")
	want := []string{"utterance 1", "utterance 2", events.TurnEnd, "utterance 3", events.TurnEnd}
	if !slices.Equal(o, want) {
		t.Fatalf("log order %v, want %v", o, want)
	}
	reports := e.ofType("turn-end")
	var ids []string
	for _, ev := range evs {
		if ev.GetType() == events.TurnEnd {
			ids = append(ids, ev.GetCommandId())
		}
	}
	if len(reports) != 2 || reports[0].CommandID != ids[0] || reports[1].CommandID != ids[1] {
		t.Fatalf("turn-end reports %+v do not match the command IDs %v of the log", reports, ids)
	}
	t.Logf("turn end: log order %v", o)
}

func killPID(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

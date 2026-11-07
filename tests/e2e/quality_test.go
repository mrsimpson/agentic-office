//go:build e2e

package e2e

// Tests of the quality scenarios (chapter 10 of the architecture, concept-test-strategy). They
// use real processes only, like the other end-to-end tests. Each subtest is named after its
// scenario ID and prints one line per scenario that starts with "QS <id>", for example:
//
//	QS qs-speech-to-log n=21 p50=1243ms p95=1301ms max=1352ms target=p95<=1500ms
//
// In "make e2e" these tests check behaviour only and print the numbers; they never fail on a
// timing criterion. "make performance" (build tag performance) also checks the timing criteria and
// runs the lengths of the scenarios. Two environment variables set how long the tests run:
//
//	QS_SPEECH_DURATION  how long Ada speaks (the test sentences in a loop); default 60s, scenario 30m
//	QS_DROP_DURATION    how long the network of Ada is down; default 10s, scenario 60s

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/client/replicator/locallog"
	sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"
	"github.com/mrsimpson/agentic-office/server/session/api/events"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// Targets of the quality scenarios. Do not weaken them to make a test pass: open an issue.
const (
	targetTranscriptToLog = 200 * time.Millisecond  // qs-transcript-to-log, asserted on the maximum
	targetSpeechToLog     = 1500 * time.Millisecond // qs-speech-to-log, p95
	targetCatchUp         = 2 * time.Second         // qs-resume-after-drop
	targetStatusVisible   = time.Second             // qs-sender-sees-status
)

// qsDuration reads a duration from the environment, or returns def.
func qsDuration(t *testing.T, env string, def time.Duration) time.Duration {
	t.Helper()
	v := os.Getenv(env)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		t.Fatalf("%s=%q is not a positive duration (for example 30m)", env, v)
	}
	return d
}

// qs prints one result line of a quality scenario on stdout, so that it can be found with
// grep '^QS '.
func qs(_ *testing.T, id, format string, args ...any) {
	line := fmt.Sprintf("QS %s %s", id, fmt.Sprintf(format, args...))
	fmt.Println(line)
}

// latencies is a set of measured durations.
type latencies []time.Duration

// percentile returns the nearest-rank percentile p (0 < p <= 100).
func (l latencies) percentile(p float64) time.Duration {
	s := slices.Clone(l)
	slices.Sort(s)
	i := int(math.Ceil(p/100*float64(len(s)))) - 1
	return s[max(i, 0)]
}

func (l latencies) max() time.Duration { return slices.Max(l) }

func ms(d time.Duration) string { return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000) }

// summary is "n=… p50=… p95=… max=…".
func (l latencies) summary() string {
	return fmt.Sprintf("n=%d p50=%s p95=%s max=%s", len(l), ms(l.percentile(50)), ms(l.percentile(95)), ms(l.max()))
}

// speechWAV writes a WAV file with the three test sentences in a loop, 1.2 s apart, until it is
// at least d long. It returns the path and the expected test sentence of each utterance.
func speechWAV(t *testing.T, d time.Duration) (string, []int, time.Duration) {
	t.Helper()
	s := readPCM(t, testWAV).sentences()
	if len(s) != 3 {
		t.Fatalf("test WAV: want 3 sentences, found %d", len(s))
	}
	const gap = 1200 * time.Millisecond
	var parts []pcm
	var want []int
	var total time.Duration
	for i := 0; total < d; i++ {
		parts, want = append(parts, s[i%3]), append(want, i%3)
		total += s[i%3].duration() + gap
	}
	path := filepath.Join(t.TempDir(), "speech.wav")
	join(s[0].rate, gap, parts...).writeWAV(t, path)
	return path, want, total
}

// checkSent checks the utterances of Ada in a log against the final reports of the ears: the
// same command IDs in the same order, each once, as voice with transcribed_at. It returns how
// many texts miss the key words of their test sentence (a recognition error, not a lost
// utterance).
func checkSent(t *testing.T, got []*sessionv1.Event, finals []earsReport, want []int) int {
	t.Helper()
	if len(got) != len(want) || len(finals) != len(want) {
		t.Fatalf("want %d utterances, got %d in the log and %d final in the ears", len(want), len(got), len(finals))
	}
	seen := map[string]bool{}
	misses := 0
	for i, ev := range got {
		u := ev.GetUtteranceFinal()
		switch {
		case seen[ev.GetCommandId()]:
			t.Fatalf("utterance %d: command ID %s twice", i, ev.GetCommandId())
		case ev.GetCommandId() != finals[i].CommandID:
			t.Fatalf("utterance %d: command ID %s, the ears made %s (order differs)", i, ev.GetCommandId(), finals[i].CommandID)
		case u.GetModality() != sessionv1.Modality_MODALITY_VOICE || u.GetTranscribedAt() == nil:
			t.Fatalf("utterance %d: modality %v, transcribed_at %v", i, u.GetModality(), u.GetTranscribedAt())
		}
		if sentenceOf(u.GetText()) != want[i] {
			misses++
			t.Logf("utterance %d: %q misses the key words %v of test sentence %d", i, u.GetText(), keyWords[want[i]], want[i]+1)
		}
		seen[ev.GetCommandId()] = true
	}
	return misses
}

// viewSince returns the lines of the gateway view from index i on, with the time each was read.
func (e *ears) viewSince(i int) ([]map[string]any, []time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i > len(e.view) {
		i = len(e.view)
	}
	return slices.Clone(e.view[i:]), slices.Clone(e.viewAt[i:])
}

// waitViewAfter waits for a line of the view from index i on that matches, and returns when the
// test read it.
func (e *ears) waitViewAfter(what string, i int, match func(map[string]any) bool, timeout time.Duration) time.Time {
	e.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		lines, at := e.viewSince(i)
		for j, v := range lines {
			if match(v) {
				return at[j]
			}
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("timeout waiting for %s in the gateway view\n%s", what, e.p.Stderr())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func utteranceStatus(v map[string]any) (cmdID, status string) {
	u, _ := v["utterance"].(map[string]any)
	if u == nil {
		return "", ""
	}
	c, _ := u["commandId"].(string)
	s, _ := u["status"].(string)
	return c, s
}

// sseEvent is one server-sent event of the stage, with the time the test received it.
type sseEvent struct {
	name, data string
	at         time.Time
}

// stageEvents is the event stream of a stage page, parsed into events.
type stageEvents struct {
	mu  sync.Mutex
	evs []sseEvent
}

// openStageEvents opens the event stream of a stage link (like openStage) and records each event.
func openStageEvents(t *testing.T, srv *contracttest.Server, link string) *stageEvents {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	u.RawPath = u.EscapedPath() + "/events"
	u.Path += "/events"
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	res, err := srv.HTTPClient(t).Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("stage events: %v, %v", res, err)
	}
	s := &stageEvents{}
	go func() {
		defer res.Body.Close()
		r := bufio.NewReaderSize(res.Body, 1<<20)
		var name string
		var data []string
		for {
			l, err := r.ReadString('\n')
			if err != nil {
				return
			}
			l = strings.TrimSuffix(l, "\n")
			switch {
			case strings.HasPrefix(l, "event: "):
				name = strings.TrimPrefix(l, "event: ")
			case strings.HasPrefix(l, "data: "):
				data = append(data, strings.TrimPrefix(l, "data: "))
			case l == "":
				if name != "" {
					s.mu.Lock()
					s.evs = append(s.evs, sseEvent{name, strings.Join(data, "\n"), time.Now()})
					s.mu.Unlock()
				}
				name, data = "", nil
			}
		}
	}()
	return s
}

// waitCapture waits for the first participants event received after from that shows the
// capture status of participant, and returns when the test received it.
func (s *stageEvents) waitCapture(t *testing.T, participant, status string, from time.Time) time.Time {
	t.Helper()
	re := regexp.MustCompile(`<li data-participant="` + regexp.QuoteMeta(participant) + `"[^>]*data-capture="` +
		regexp.QuoteMeta(status) + `"`)
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.mu.Lock()
		evs := slices.Clone(s.evs)
		s.mu.Unlock()
		for _, ev := range evs {
			if ev.name == "participants" && !ev.at.Before(from) && re.MatchString(ev.data) {
				return ev.at
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("stage: %s is not %q after %v", participant, status, from.Format(time.StampMicro))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestQualitySpeech: Ada speaks with her ears on (the test sentences in a loop, see
// QS_SPEECH_DURATION) while the decrypting proxy records what her client sends. The bot follows
// its local log, and the stage of the session is open. The subtests evaluate the run:
// qs-no-raw-audio, qs-transcript-to-log, qs-speech-to-log and qs-sender-sees-status.
func TestQualitySpeech(t *testing.T) {
	models := earsModels(t)
	speech := qsDuration(t, "QS_SPEECH_DURATION", 60*time.Second)
	m := newMeeting(t)
	proxy := contracttest.StartDecryptingProxy(t, m.srv.URL, m.srv.CertFile, filepath.Join(m.srv.DataDir, "tls.key"))
	ada := append(m.user("ada", "human", proxy.URL), models...)
	ada = append(ada, "OFFICE_SESSION="+m.sid)
	botEnv := m.user("bot", "agent", m.srv.URL)
	v := m.office(botEnv, "meeting", "join", m.sid)
	m.t.Cleanup(func() { killPID(int(v["replicatorPid"].(float64))) })
	bot := watchLog(t, v["logPath"].(string))
	stage := openStageEvents(t, m.srv, m.office(append(botEnv, "OFFICE_SESSION="+m.sid), "stage", "link")["url"].(string))

	wav, want, audioLen := speechWAV(t, speech)
	t.Logf("Ada speaks %d sentences, %v of audio", len(want), audioLen.Round(time.Second))

	// Stage: Ada joins with ears and is shown as captured.
	var stageLat, stageLatStart latencies
	joinStart := time.Now()
	e := m.startEars(ada, "--wav", wav)
	joinResp := time.Now()
	shown := stage.waitCapture(t, "ada", "captured", joinStart)
	stageLat, stageLatStart = append(stageLat, shown.Sub(joinResp)), append(stageLatStart, shown.Sub(joinStart))
	t.Logf("stage: ada captured %v after the join response (%v after the start of join)",
		shown.Sub(joinResp).Round(time.Millisecond), shown.Sub(joinStart).Round(time.Millisecond))

	e.wait("done", 1, audioLen+2*time.Minute)
	got, at := bot.waitUtterances(t, "ada", len(want), 10*time.Second)
	finals, sent := e.ofType("final"), e.ofType("sent")
	misses := checkSent(t, got, finals, want)
	logAt := map[string]time.Time{}
	for i, ev := range got {
		logAt[ev.GetCommandId()] = at[i]
	}
	var toLog, speechToLog latencies
	for _, f := range finals {
		toLog = append(toLog, logAt[f.CommandID].Sub(*f.TranscribedAt))
		speechToLog = append(speechToLog, logAt[f.CommandID].Sub(*f.SpeechEnd))
	}

	// Gateway view: status sent after the server confirmed the submit (submittedAt).
	var sentLat latencies
	for _, s := range sent {
		cmd := s.CommandID
		seen := e.waitViewAfter("sent "+cmd, 0, func(v map[string]any) bool {
			c, st := utteranceStatus(v)
			return c == cmd && st == "sent"
		}, 5*time.Second)
		sentLat = append(sentLat, seen.Sub(*s.SubmittedAt))
	}

	// Gateway view: retract the last sentences with the r key. The view shows them as retracted.
	var retractLat latencies
	for range min(5, len(sent)) {
		i := len(e.viewSnapshot())
		pressed := time.Now()
		e.key("r")
		seen := e.waitViewAfter("retracted", i, func(v map[string]any) bool {
			_, st := utteranceStatus(v)
			return st == "retracted"
		}, 5*time.Second)
		retractLat = append(retractLat, seen.Sub(pressed))
	}

	// Stage: pause and resume with the p key, three times. The p key toggles what the view read
	// from the local log, so the next press waits until the view shows the change, too.
	var pauseViewLat latencies
	for range 3 {
		for _, status := range []string{"paused", "captured"} {
			i := len(e.viewSnapshot())
			pressed := time.Now()
			e.key("p")
			shown := stage.waitCapture(t, "ada", status, pressed)
			stageLat, stageLatStart = append(stageLat, shown.Sub(pressed)), append(stageLatStart, shown.Sub(pressed))
			inView := e.waitViewAfter(status, i, func(v map[string]any) bool { return v["paused"] == (status == "paused") }, 5*time.Second)
			pauseViewLat = append(pauseViewLat, inView.Sub(pressed))
		}
	}
	e.quit()
	// Stage: Ada leaves with the CLI.
	leaveStart := time.Now()
	m.office(ada, "meeting", "leave")
	leaveResp := time.Now()
	shown = stage.waitCapture(t, "ada", "left", leaveStart)
	stageLat, stageLatStart = append(stageLat, shown.Sub(leaveResp)), append(stageLatStart, shown.Sub(leaveStart))

	t.Run("qs-no-raw-audio", func(t *testing.T) {
		transcripts := map[string]bool{}
		for _, f := range finals {
			transcripts[f.Text] = true
		}
		var plain, bodies int64
		var largest, unknownTotal int
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
					unknownTotal += unknown
					for _, f := range fields {
						switch {
						case f.Name == "office.session.v1.SubmitUtteranceRequest.text":
							if !transcripts[f.Value] {
								t.Errorf("submitted text %q is not a transcript of the ears", f.Value)
							}
						case f.Size > 128:
							t.Errorf("%s: field %s has %d bytes; only transcripts may be long", r.Path, f.Name, f.Size)
						}
					}
				}
			}
		}
		if unknownTotal > 0 {
			t.Errorf("%d bytes of unknown fields in the requests", unknownTotal)
		}
		if n := calls["/office.session.v1.SessionService/SubmitUtterance"]; n != len(finals) {
			t.Errorf("want %d SubmitUtterance requests, got %v", len(finals), calls)
		}
		pcm16k := int64(audioLen.Seconds() * 16000 * 2)
		t.Logf("requests: %v", calls)
		qs(t, "qs-no-raw-audio", "audio=%v audio_bytes_16k=%d wire_up=%d plaintext_up=%d bodies=%d largest_body=%d "+
			"unknown_field_bytes=%d connections=%d sentences=%d target=0-audio-bytes", audioLen.Round(time.Second), pcm16k,
			proxy.UpstreamBytes(), plain, bodies, largest, unknownTotal, len(proxy.Captured()), len(finals))
	})

	t.Run("qs-transcript-to-log", func(t *testing.T) {
		qs(t, "qs-transcript-to-log", "%s target=max<%s", toLog.summary(), ms(targetTranscriptToLog))
		if assertTiming && toLog.max() >= targetTranscriptToLog {
			t.Errorf("end of transcription to the bot's log: max %v, want < %v", toLog.max(), targetTranscriptToLog)
		}
	})

	t.Run("qs-speech-to-log", func(t *testing.T) {
		var whisper latencies
		for _, f := range finals {
			whisper = append(whisper, time.Duration(f.WhisperMs)*time.Millisecond)
		}
		t.Logf("whisper per sentence: %s; recognition: %d of %d sentences miss their key words", whisper.summary(), misses, len(finals))
		qs(t, "qs-speech-to-log", "%s target=p95<=%s", speechToLog.summary(), ms(targetSpeechToLog))
		if p := speechToLog.percentile(95); assertTiming && p > targetSpeechToLog {
			t.Errorf("end of speech to the bot's log: p95 %v, want at most %v", p, targetSpeechToLog)
		}
	})

	t.Run("qs-sender-sees-status", func(t *testing.T) {
		qs(t, "qs-sender-sees-status", "view-sent %s target=max<=%s", sentLat.summary(), ms(targetStatusVisible))
		qs(t, "qs-sender-sees-status", "view-retracted %s target=max<=%s (from the key press)", retractLat.summary(), ms(targetStatusVisible))
		qs(t, "qs-sender-sees-status", "stage-capture %s target=max<=%s (join, 3x pause and resume, leave)", stageLat.summary(), ms(targetStatusVisible))
		t.Logf("stage capture status from the start of the command: %s", stageLatStart.summary())
		t.Logf("paused and resumed in the gateway view, from the key press: %s", pauseViewLat.summary())
		for name, l := range map[string]latencies{"sent in the view": sentLat, "retracted in the view": retractLat,
			"capture status on the stage": stageLatStart} {
			if assertTiming && l.max() > targetStatusVisible {
				t.Errorf("%s: max %v, want at most %v", name, l.max(), targetStatusVisible)
			}
		}
	})
}

func (e *ears) viewSnapshot() []map[string]any {
	v, _ := e.viewSince(0)
	return v
}

// TestQualityResumeAfterDrop: the network of Ada drops after her first sentence for
// QS_DROP_DURATION while she keeps speaking and the bot keeps writing. After the network is back,
// Ada's local log is complete again and her queued utterances are in the session log in order.
func TestQualityResumeAfterDrop(t *testing.T) {
	models := earsModels(t)
	drop := qsDuration(t, "QS_DROP_DURATION", 10*time.Second)
	m := newMeeting(t)
	proxy := contracttest.StartProxy(t, m.srv.URL)
	ada := append(m.user("ada", "human", proxy.URL), models...)
	botEnv := append(m.user("bot", "agent", m.srv.URL), "OFFICE_SESSION="+m.sid)
	v := m.office(botEnv, "meeting", "join", m.sid)
	m.t.Cleanup(func() { killPID(int(v["replicatorPid"].(float64))) })
	bot := watchLog(t, v["logPath"].(string))

	// Speech for the first sentence, the drop, and a few sentences after it.
	wav, want, audioLen := speechWAV(t, drop+15*time.Second)
	e := m.startEars(ada, "--wav", wav)
	adaLog := watchLog(t, filepath.Join(m.runtime, m.sid, "ada.jsonl"))
	e.wait("sent", 1, 30*time.Second)
	proxy.Cut()
	cutAt := time.Now()

	// While Ada is offline, the bot writes events that Ada's log misses.
	says := 0
	for time.Since(cutAt) < drop {
		says++
		m.office(botEnv, "say", fmt.Sprintf("While Ada is offline, number %d.", says))
		time.Sleep(min(time.Second, time.Until(cutAt.Add(drop))))
	}
	if n := len(e.ofType("sent")); n != 1 {
		t.Fatalf("%d utterances sent while the network was cut", n)
	}
	finalsAtRestore := len(e.ofType("final"))
	queued := finalsAtRestore - 1
	if queued < 1 {
		t.Fatalf("no utterance was queued during the drop of %v", drop)
	}
	botUtts := func() int {
		n := 0
		evs, _ := bot.events()
		for _, ev := range evs {
			if ev.GetType() == events.UtteranceFinal && ev.GetUtteranceFinal().GetParticipantId() == "bot" {
				n++
			}
		}
		return n
	}
	for deadline := time.Now().Add(5 * time.Second); botUtts() < says; {
		if time.Now().After(deadline) {
			t.Fatalf("the bot's log has %d of its %d utterances", botUtts(), says)
		}
		time.Sleep(5 * time.Millisecond)
	}
	evs, _ := bot.events()
	missedUpTo := evs[len(evs)-1].GetId()
	a, _ := adaLog.events()
	missed := int(missedUpTo) - len(a)
	proxy.Restore()
	restoredAt := time.Now()
	t.Logf("network down for %v; %d utterances of Ada queued; Ada's log misses %d events", restoredAt.Sub(cutAt).Round(time.Millisecond),
		queued, missed)

	// Catch-up: Ada's log has every event up to the restore and her queued utterances.
	sent := e.wait("sent", finalsAtRestore, 30*time.Second)
	lastQueued := sent[finalsAtRestore-1].EventID
	var missedAt, queuedAt time.Time
	for deadline := time.Now().Add(30 * time.Second); missedAt.IsZero() || queuedAt.IsZero(); {
		a, aAt := adaLog.events()
		for i, ev := range a {
			if missedAt.IsZero() && ev.GetId() >= missedUpTo {
				missedAt = aAt[i]
			}
			if queuedAt.IsZero() && ev.GetId() >= lastQueued {
				queuedAt = aAt[i]
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Ada's log did not catch up: %d events, want up to IDs %d and %d", len(a), missedUpTo, lastQueued)
		}
		time.Sleep(5 * time.Millisecond)
	}
	catchUp := missedAt.Sub(restoredAt)
	if queuedAt.After(missedAt) {
		catchUp = queuedAt.Sub(restoredAt)
	}

	// The rest of the speech; then both logs must be equal.
	e.wait("done", 1, audioLen+2*time.Minute)
	got, _ := bot.waitUtterances(t, "ada", len(want), 10*time.Second)
	finals := e.ofType("final")
	misses := checkSent(t, got, finals, want)
	dupResends := 0
	for _, s := range e.ofType("sent") {
		if s.Duplicate {
			dupResends++
		}
	}
	evs, _ = bot.events()
	last := evs[len(evs)-1].GetId()
	for deadline := time.Now().Add(10 * time.Second); ; {
		a, _ := adaLog.events()
		if len(a) > 0 && a[len(a)-1].GetId() >= last {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Ada's local log did not reach event %d", last)
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.quit()
	lost, dup := compareLogs(t, adaLog.path, bot.path, int(last))
	sayCount := map[string]int{}
	for _, ev := range evs {
		if ev.GetType() == events.UtteranceFinal && ev.GetUtteranceFinal().GetParticipantId() == "bot" {
			sayCount[ev.GetUtteranceFinal().GetText()]++
		}
	}
	for i := 1; i <= says; i++ {
		if n := sayCount[fmt.Sprintf("While Ada is offline, number %d.", i)]; n != 1 {
			t.Errorf("the bot's utterance %d is %d times in the log", i, n)
		}
	}

	t.Run("qs-resume-after-drop", func(t *testing.T) {
		qs(t, "qs-resume-after-drop", "drop=%v queued=%d missed=%d lost=%d duplicate=%d catchup=%s "+
			"(missed events %s, queued utterances %s) resends_answered_duplicate=%d target=catchup<=%s",
			drop, queued, missed, lost, dup, ms(catchUp), ms(missedAt.Sub(restoredAt)), ms(queuedAt.Sub(restoredAt)),
			dupResends, ms(targetCatchUp))
		t.Logf("recognition: %d of %d sentences miss their key words", misses, len(finals))
		if lost != 0 || dup != 0 {
			t.Errorf("%d lost and %d duplicate events", lost, dup)
		}
		if assertTiming && catchUp > targetCatchUp {
			t.Errorf("Ada's local log was complete %v after the network came back, want at most %v", catchUp, targetCatchUp)
		}
	})
}

// compareLogs checks that the first n events of Ada's log are the events of the bot's log, in
// order: each ID once, no gap, the same bytes. It returns the lost and the duplicate events.
func compareLogs(t *testing.T, adaPath, botPath string, n int) (lost, dup int) {
	t.Helper()
	lines := func(path string) []string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.SplitAfter(strings.TrimSuffix(string(b), "\n"), "\n")
	}
	a, b := lines(adaPath), lines(botPath)
	ids := map[uint32]int{}
	for _, l := range a {
		ev, err := locallog.UnmarshalLine([]byte(strings.TrimSuffix(l, "\n") + "\n"))
		if err != nil {
			t.Fatalf("Ada's log: %v", err)
		}
		ids[ev.GetId()]++
	}
	for id := uint32(1); id <= uint32(n); id++ {
		switch c := ids[id]; {
		case c == 0:
			lost++
		case c > 1:
			dup += c - 1
		}
	}
	if len(a) < n || len(b) < n {
		t.Fatalf("logs: Ada %d lines, bot %d lines, want %d", len(a), len(b), n)
	}
	for i := range n {
		if strings.TrimSuffix(a[i], "\n") != strings.TrimSuffix(b[i], "\n") {
			t.Errorf("line %d differs between Ada's and the bot's log", i+1)
			break
		}
	}
	return lost, dup
}

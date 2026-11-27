//go:build e2e

package e2e

// End-to-end tests of the voice print of the ears (dec-voice-print, dec-overlap-hint): the owner
// enrols with "office voice enrol", then joins with loudspeakers: a meeting in which the owner
// speaks close to the microphone and a second voice comes from the loudspeakers. Only the owner's
// sentences may reach the session. The voices are synthetic (see testdata/voiceprint/README.md).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const (
	enrolWAV   = "testdata/voiceprint/owner-enrol.wav"
	meetingWAV = "testdata/voiceprint/meeting.wav"
)

// ownerWords are key words of the owner's three sentences in meeting.wav, in order. The remote
// voice says "kunden kosten" and "entscheidung datenbank".
var (
	ownerWords  = [][]string{{"release", "woche"}, {"folien", "besprechung"}, {"aufgabe", "freitag"}}
	remoteWords = [][]string{{"kunde", "kosten"}, {"entscheidung", "datenbank"}}
)

func has(text string, words []string) bool {
	l := strings.ToLower(text)
	for _, w := range words {
		if !strings.Contains(l, w) {
			return false
		}
	}
	return true
}

// voiceEnv returns the environment with the models and a voice print file of this test.
func voiceEnv(t *testing.T, env []string) ([]string, string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "voiceprint.json")
	return append(env, "OFFICE_VOICE_PRINT="+file), file
}

// TestVoicePrintEnrolShowDelete: the voice print is made from speech, kept readable for the user
// only, described without its numbers, and deleted on request.
func TestVoicePrintEnrolShowDelete(t *testing.T) {
	models := earsModels(t)
	m := newMeeting(t)
	env, file := voiceEnv(t, append(m.user("ada", "human", m.srv.URL), models...))

	out, errOut, code := contracttest.Run(t, "office", env, "voice", "enrol", "--wav", enrolWAV, "--json")
	if code != 0 {
		t.Fatalf("voice enrol: exit %d, %q, %q", code, out, errOut)
	}
	var r struct {
		File    string  `json:"file"`
		Seconds float64 `json:"seconds"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.File != file || r.Seconds < 10 {
		t.Fatalf("voice enrol --json: %q (%v)", out, err)
	}
	st, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("the voice print has mode %v, want 0600", st.Mode().Perm())
	}

	// A second enrolment needs --replace: nobody loses a print by accident.
	if _, errOut, code := contracttest.Run(t, "office", env, "voice", "enrol", "--wav", enrolWAV); code == 0 || !strings.Contains(errOut, "--replace") {
		t.Fatalf("a second enrolment must fail and name --replace: exit %d, %q", code, errOut)
	}

	out, _, code = contracttest.Run(t, "office", env, "voice", "show", "--json")
	if code != 0 || !strings.Contains(out, `"exists":true`) || strings.Contains(out, "embedding") {
		t.Fatalf("voice show must say that it exists and must not show the numbers: %d %q", code, out)
	}

	if _, errOut, code := contracttest.Run(t, "office", env, "voice", "delete"); code != 0 {
		t.Fatalf("voice delete: %d %q", code, errOut)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the voice print is still there: %v", err)
	}
	out, _, _ = contracttest.Run(t, "office", env, "voice", "show", "--json")
	if !strings.Contains(out, `"exists":false`) {
		t.Fatalf("voice show after delete: %q", out)
	}
}

// TestVoicePrintRefusesTooLittleSpeech: a short recording makes no voice print.
func TestVoicePrintRefusesTooLittleSpeech(t *testing.T) {
	models := earsModels(t)
	m := newMeeting(t)
	env, file := voiceEnv(t, append(m.user("ada", "human", m.srv.URL), models...))
	_, errOut, code := contracttest.Run(t, "office", env, "voice", "enrol", "--wav", testWAV)
	if code == 0 || !strings.Contains(errOut, "not enough") {
		t.Fatalf("a recording of a few seconds must be refused: exit %d, %q", code, errOut)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("a refused enrolment must leave no voice print")
	}
}

// TestVoicePrintLoudspeakers: with a voice print, a second voice from the loudspeakers does not
// reach the session. Without it, it does (the headset constraint).
func TestVoicePrintLoudspeakers(t *testing.T) {
	slow(t)
	models := earsModels(t)
	m := newMeeting(t)
	env, _ := voiceEnv(t, append(m.user("ada", "human", m.srv.URL), models...))
	bot := m.joinBot()
	if _, errOut, code := contracttest.Run(t, "office", env, "voice", "enrol", "--wav", enrolWAV); code != 0 {
		t.Fatalf("voice enrol: %d %q", code, errOut)
	}

	e := m.startEars(env, "--wav", meetingWAV)
	e.wait("done", 1, 90*time.Second)
	if n := len(e.ofType("gate")); n != 0 {
		t.Fatalf("without --debug the ears must not report the gate, got %d reports", n)
	}
	got, _ := bot.waitUtterances(t, "ada", 3, 10*time.Second)
	e.quit()

	if len(got) != 3 {
		t.Fatalf("want the 3 sentences of the owner, got %d: %v", len(got), texts(got))
	}
	for i, ev := range got {
		u := ev.GetUtteranceFinal()
		if !has(u.GetText(), ownerWords[i]) {
			t.Fatalf("utterance %d: %q is not the owner's sentence %d (key words %v)", i, u.GetText(), i+1, ownerWords[i])
		}
		for _, w := range remoteWords {
			if has(u.GetText(), w) {
				t.Fatalf("utterance %d: %q is a sentence of the loudspeaker voice", i, u.GetText())
			}
		}
		if u.GetOverlap() {
			t.Fatalf("utterance %d: %q has the overlap hint, but nobody spoke at the same time", i, u.GetText())
		}
	}
	if n := len(e.ofType("other-voice")); n < 2 {
		t.Fatalf("the ears must report the 2 sentences of the other voice, got %d", n)
	}
}

// TestNoVoicePrintHearsEverything: --no-voice-print is the headset setup. With loudspeakers, the
// voice of the other person is transcribed too, and the ears say nothing about other voices.
func TestNoVoicePrintHearsEverything(t *testing.T) {
	slow(t)
	models := earsModels(t)
	m := newMeeting(t)
	env, _ := voiceEnv(t, append(m.user("ada", "human", m.srv.URL), models...))
	bot := m.joinBot()
	if _, errOut, code := contracttest.Run(t, "office", env, "voice", "enrol", "--wav", enrolWAV); code != 0 {
		t.Fatalf("voice enrol: %d %q", code, errOut)
	}
	e := m.startEars(env, "--wav", meetingWAV, "--no-voice-print")
	e.wait("done", 1, 90*time.Second)
	got, _ := bot.waitUtterances(t, "ada", 5, 10*time.Second)
	e.quit()
	remote := 0
	for _, ev := range got {
		for _, w := range remoteWords {
			if has(ev.GetUtteranceFinal().GetText(), w) {
				remote++
			}
		}
		if ev.GetUtteranceFinal().GetOverlap() {
			t.Fatalf("%q: without a voice print the ears must not set the overlap hint", ev.GetUtteranceFinal().GetText())
		}
	}
	if remote < 2 {
		t.Fatalf("without a voice print the loudspeaker voice must be transcribed too; got %v", texts(got))
	}
}

// TestVoicePrintOverlapHint: when another voice speaks at the same time as the owner, the ears
// mark the utterance (dec-overlap-hint). The owner's words must not be lost completely, and the
// hint must be in the log of an agent. The sentence of the loudspeaker voice may be partly
// there: that is exactly what the hint is for.
func TestVoicePrintOverlapHint(t *testing.T) {
	slow(t)
	models := earsModels(t)
	m := newMeeting(t)
	env, _ := voiceEnv(t, append(m.user("ada", "human", m.srv.URL), models...))
	bot := m.joinBot()
	if _, errOut, code := contracttest.Run(t, "office", env, "voice", "enrol", "--wav", enrolWAV); code != 0 {
		t.Fatalf("voice enrol: %d %q", code, errOut)
	}
	e := m.startEars(env, "--wav", "testdata/voiceprint/overlap.wav", "--debug")
	e.wait("done", 1, 90*time.Second)
	// --debug reports the measurements of the voice print for each sentence.
	gates := e.ofType("gate")
	if len(gates) == 0 || len(gates[0].Windows) == 0 || len(gates[0].KeptMs) == 0 || gates[0].Threshold == 0 {
		t.Fatalf("--debug must report the windows, the kept stretches and the limits: %+v", gates)
	}
	t.Logf("gate: best %.2f, windows %v, kept %v ms, overlap %v", gates[0].Score, gates[0].Windows, gates[0].KeptMs, gates[0].Overlap)
	got, _ := bot.waitUtterances(t, "ada", 1, 10*time.Second)
	e.quit()

	if !has(got[0].GetUtteranceFinal().GetText(), []string{"aufgabe"}) {
		t.Fatalf("the owner's words are missing: %v", texts(got))
	}
	for _, ev := range got {
		if !ev.GetUtteranceFinal().GetOverlap() {
			t.Fatalf("%q: another voice spoke at the same time, so the overlap hint must be set", ev.GetUtteranceFinal().GetText())
		}
	}
	var hinted bool
	for _, f := range e.ofType("final") {
		hinted = hinted || f.Overlap
	}
	if !hinted {
		t.Fatal("the ears must report the hint with the final sentence")
	}
}

//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// The vocabulary goes into Whisper's prompt: the terms of .office/vocabulary.txt in the current
// directory (or of --vocabulary), then the display names of the participants from the local log.
// The ears report what they use. A long vocabulary is cut at the end, never at the start.
//
// The test checks the prompt through the reports, not the spelling: with the small model, the
// prompt "Businessplan" changes "Pusines Plan" into "BuSinisplan", not into "Businessplan"; other
// forms of the prompt ("Glossar: …", one term per sentence, no commas) did no better.
func TestEarsVocabulary(t *testing.T) {
	models := earsModels(t)
	wav, err := filepath.Abs(testWAV)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("the default file and the names of the participants", func(t *testing.T) {
		m := newMeeting(t)
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ".office", "vocabulary.txt"), "# terms of the project\nBusinessplan\n\nQuartalszahlen\n")
		bot := m.joinBot()
		ada := append(m.user("ada", "human", m.srv.URL), models...)
		e := m.startEarsIn(dir, ada, "--wav", wav)
		e.wait("done", 1, 10*time.Minute)
		bot.waitUtterances(t, "ada", 3, 10*time.Second)
		e.quit()

		vs := e.ofType("vocabulary")
		if len(vs) == 0 {
			t.Fatalf("no vocabulary report:\n%s", e.p.Stderr())
		}
		if vs[0].Terms != 2 || !strings.HasPrefix(vs[0].Prompt, "Businessplan, Quartalszahlen") ||
			vs[0].VocabularyFile != filepath.Join(".office", "vocabulary.txt") {
			t.Errorf("first vocabulary report: %+v", vs[0])
		}
		// The names come from the local log: at the latest before the last sentence, ada and
		// bot have joined.
		last := vs[len(vs)-1]
		if last.Terms != 2 || last.Names != 2 || !strings.Contains(last.Prompt, "ada") || !strings.Contains(last.Prompt, "bot") {
			t.Errorf("last vocabulary report: %+v", last)
		}
		var texts []string
		for _, f := range e.ofType("final") {
			texts = append(texts, f.Text)
		}
		t.Logf("transcripts with the vocabulary: %q", texts)
	})

	t.Run("a long vocabulary is cut at the end", func(t *testing.T) {
		m := newMeeting(t)
		var b strings.Builder
		for i := range 300 {
			fmt.Fprintf(&b, "Fachbegriff%03d\n", i)
		}
		file := filepath.Join(t.TempDir(), "terms.txt")
		writeFile(t, file, b.String())
		ada := append(m.user("ada", "human", m.srv.URL), models...)
		e := m.startEars(ada, "--wav", wav, "--vocabulary", file)
		v := e.wait("vocabulary", 1, 2*time.Minute)[0]
		e.quit()
		if v.Terms == 0 || v.Terms >= 300 || v.Cut < 300-v.Terms ||
			!strings.HasPrefix(v.Prompt, "Fachbegriff000, Fachbegriff001,") || v.VocabularyFile != file {
			t.Errorf("vocabulary report: %+v", v)
		}
	})

	t.Run("a missing vocabulary file is an error", func(t *testing.T) {
		m := newMeeting(t)
		env := append(m.user("ada", "human", m.srv.URL), models...)
		missing := filepath.Join(t.TempDir(), "missing.txt")
		_, errOut, code := contracttest.Run(t, "office", env, "meeting", "join", m.sid, "--wav", wav, "--vocabulary", missing)
		if code == 0 || !strings.Contains(errOut, "missing.txt") {
			t.Fatalf("join with a missing vocabulary: exit %d, %q", code, errOut)
		}
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

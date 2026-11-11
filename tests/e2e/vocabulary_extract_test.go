//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// vocabularyDocs are German and English project documents with known names and terms.
const vocabularyDocs = "testdata/vocabulary"

type extracted struct {
	File      string `json:"file"`
	Documents int    `json:"documents"`
	Model     string `json:"model"`
	Terms     []struct {
		Term    string   `json:"term"`
		Score   int      `json:"score"`
		Sources []string `json:"sources"`
	} `json:"terms"`
}

func (x extracted) terms() []string {
	var out []string
	for _, t := range x.Terms {
		out = append(out, t.Term)
	}
	return out
}

// extractVocabulary runs "office vocabulary extract" in dir and returns its result and the
// lines of the vocabulary file that are not comments.
func extractVocabulary(t *testing.T, dir string, env []string, args ...string) (extracted, []string) {
	t.Helper()
	docs, err := filepath.Abs(vocabularyDocs)
	if err != nil {
		t.Fatal(err)
	}
	p := contracttest.StartIn(t, dir, "office", env, append([]string{"vocabulary", "extract", docs, "--json"}, args...)...)
	<-p.Done()
	var x extracted
	out, _ := p.Stdout.ReadString('\n')
	if c := p.Cmd.ProcessState.ExitCode(); c != 0 || json.Unmarshal([]byte(out), &x) != nil {
		t.Fatalf("vocabulary extract: exit %d, %q\n%s", c, out, p.Stderr())
	}
	b, err := os.ReadFile(filepath.Join(dir, x.File))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	return x, lines
}

// office vocabulary extract finds terms in Markdown and text files with heuristics: CamelCase
// words, acronyms (also in compounds with a hyphen) and words of headings. It skips code blocks
// and hidden directories, and writes .office/vocabulary.txt, which the ears read.
func TestVocabularyExtractHeuristics(t *testing.T) {
	dir := t.TempDir()
	x, lines := extractVocabulary(t, dir, nil, "--no-model")
	if x.Documents != 2 || x.Model != "" || x.File != filepath.Join(".office", "vocabulary.txt") {
		t.Errorf("result: %+v", x)
	}
	terms := x.terms()
	for _, want := range []string{"SyncEngine", "LernBrücke", "OAuth", "LMS", "API", "DSGVO", "Nordlicht", "Zeitplan"} {
		if !slices.Contains(terms, want) {
			t.Errorf("missing term %s in %v", want, terms)
		}
	}
	for _, not := range []string{"GeheimerBefehl", "VersteckterBegriff", "GmbH", "Katharina Weber"} {
		if slices.Contains(terms, not) {
			t.Errorf("unexpected term %s in %v", not, terms)
		}
	}
	if !slices.Equal(lines, terms) {
		t.Errorf("the file has %v, the result %v", lines, terms)
	}
	// Ranked: two sources beat a word of a heading.
	if slices.Index(terms, "SyncEngine") > slices.Index(terms, "Zeitplan") {
		t.Errorf("ranking: %v", terms)
	}

	t.Run("a file written by hand is kept", func(t *testing.T) {
		dir := t.TempDir()
		own := filepath.Join(dir, ".office", "vocabulary.txt")
		writeFile(t, own, "Mein Begriff\n")
		docs, _ := filepath.Abs(vocabularyDocs)
		p := contracttest.StartIn(t, dir, "office", nil, "vocabulary", "extract", docs, "--no-model")
		<-p.Done()
		if b, _ := os.ReadFile(own); p.Cmd.ProcessState.ExitCode() == 0 || string(b) != "Mein Begriff\n" ||
			!strings.Contains(p.Stderr(), "--force") {
			t.Fatalf("exit %d, file %q\n%s", p.Cmd.ProcessState.ExitCode(), b, p.Stderr())
		}
		x, _ := extractVocabulary(t, dir, nil, "--no-model", "--force", "--max", "3")
		if len(x.Terms) != 3 {
			t.Errorf("--max 3: %v", x.terms())
		}
		// A file that extract wrote is replaced without --force.
		extractVocabulary(t, dir, nil, "--no-model")
	})
}

// With the NER model, extract also finds persons, organisations and places in German and
// English text. Skipped without the model files in the model directory (about 540 MB; the CI
// does not download them).
func TestVocabularyExtractNER(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "agentic-office", "models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(envOr("XDG_DATA_HOME", filepath.Join(os.Getenv("HOME"), ".local", "share")), "agentic-office", "models")
	for _, f := range []string{"ner-distilbert-multilingual-cased-hrl.safetensors", "ner-distilbert-multilingual-cased-hrl.vocab.txt"} {
		if _, err := os.Stat(filepath.Join(src, f)); err != nil {
			t.Skipf("skipped: the NER model %s is not in %s; run \"office vocabulary extract\" once to download it", f, src)
		}
		if err := os.Symlink(filepath.Join(src, f), filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	x, _ := extractVocabulary(t, t.TempDir(), []string{"XDG_DATA_HOME=" + data, "OFFICE_MODEL_URL=http://127.0.0.1:1"})
	if x.Model != "ner-multilingual" {
		t.Errorf("model %q", x.Model)
	}
	terms := x.terms()
	sources := map[string][]string{}
	for _, tm := range x.Terms {
		sources[tm.Term] = tm.Sources
	}
	for _, want := range []struct{ term, source string }{
		{"Katharina Weber", "per"}, {"Jonas Becker", "per"}, {"Priya Raman", "per"}, {"Miriam Schulz", "per"},
		{"Regensburg", "loc"}, {"Nürnberg", "loc"}, {"Dublin", "loc"}, {"Bayern", "loc"},
		{"Contoso", "org"}, {"SyncEngine", "camelcase"}, {"LernBrücke", "camelcase"},
	} {
		if !slices.Contains(sources[want.term], want.source) {
			t.Errorf("%s: sources %v, want %s; all terms %v", want.term, sources[want.term], want.source, terms)
		}
	}
	if !slices.ContainsFunc(terms, func(s string) bool { return strings.Contains(s, "Sonnenstrahl") }) {
		t.Errorf("no Sonnenstrahl GmbH in %v", terms)
	}
	// The persons that occur most come first.
	if top := terms[:2]; !slices.Contains(top, "Jonas Becker") || !slices.Contains(top, "Katharina Weber") {
		t.Errorf("ranking: %v", terms)
	}
}

// The NER model is downloaded on first use from the source or a mirror, and checked against its
// pinned SHA-256. A wrong file is not kept.
func TestVocabularyExtractDownload(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		w.Write([]byte("not a model"))
	}))
	t.Cleanup(mirror.Close)
	data := t.TempDir()
	docs, _ := filepath.Abs(vocabularyDocs)
	env := []string{"XDG_DATA_HOME=" + data, "OFFICE_MODEL_URL=" + mirror.URL, "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1"}
	_, errOut, code := contracttest.Run(t, "office", env, "vocabulary", "extract", docs, "--out", filepath.Join(t.TempDir(), "v.txt"))
	mu.Lock()
	defer mu.Unlock()
	if code == 0 || !strings.Contains(errOut, "SHA-256") ||
		!slices.Equal(asked, []string{"/ner-distilbert-multilingual-cased-hrl.safetensors"}) {
		t.Fatalf("exit %d, asked %v, %q", code, asked, errOut)
	}
	if left, _ := filepath.Glob(filepath.Join(data, "agentic-office", "models", "*")); len(left) != 0 {
		t.Errorf("files left after a bad download: %v", left)
	}
}

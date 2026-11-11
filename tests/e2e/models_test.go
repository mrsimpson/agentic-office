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
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// The ears download a missing default model on the first join with --mic or --wav, check its
// SHA-256 and keep it. A mirror (OFFICE_MODEL_URL) serves the real model files, so the test needs
// no internet. A file with another checksum is thrown away. A model that the user chose and that
// is missing is an error, not a download.
func TestEarsDownloadMissingModels(t *testing.T) {
	var whisperFile, vadFile string
	for _, kv := range earsModels(t) {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "OFFICE_WHISPER_MODEL":
			whisperFile = v
		case "OFFICE_VAD_MODEL":
			vadFile = v
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/good/ggml-small.bin", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, whisperFile) })
	mux.HandleFunc("/good/ggml-silero-v6.2.0.bin", func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, vadFile) })
	mux.HandleFunc("/bad/ggml-silero-v6.2.0.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not the VAD model"))
	})
	mirror := httptest.NewServer(mux)
	t.Cleanup(mirror.Close)

	// No model chosen: the defaults in $XDG_DATA_HOME/agentic-office/models apply. Without a GPU,
	// the default is the fast model small.
	noModels := []string{"OFFICE_WHISPER_MODEL=", "OFFICE_VAD_MODEL=", "OFFICE_WHISPER_GPU=0", "OFFICE_WHISPER_LANGUAGE=",
		"NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1"}

	t.Run("first join downloads the models", func(t *testing.T) {
		m := newMeeting(t)
		data := t.TempDir()
		env := append(m.user("ada", "human", m.srv.URL), noModels...)
		env = append(env, "XDG_DATA_HOME="+data, "OFFICE_MODEL_URL="+mirror.URL+"/good")
		e := m.startEars(env, "--wav", testWAV)
		e.wait("listening", 1, 120*time.Second)
		e.quit()
		dir := filepath.Join(data, "agentic-office", "models")
		for _, f := range []struct{ name, src string }{{"ggml-small.bin", whisperFile}, {"ggml-silero-v6.2.0.bin", vadFile}} {
			got, err1 := os.Stat(filepath.Join(dir, f.name))
			want, err2 := os.Stat(f.src)
			if err1 != nil || err2 != nil || got.Size() != want.Size() {
				t.Errorf("%s: %v %v, size %v, want %v", f.name, err1, err2, got, want)
			}
		}
		if !strings.Contains(e.p.Stderr(), "checksum ok") {
			t.Errorf("join did not report the download:\n%s", e.p.Stderr())
		}
	})

	t.Run("a file with another checksum is not kept", func(t *testing.T) {
		data := t.TempDir()
		dir := filepath.Join(data, "agentic-office", "models")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(whisperFile, filepath.Join(dir, "ggml-small.bin")); err != nil {
			t.Fatal(err)
		}
		env := append(noModels, "XDG_DATA_HOME="+data, "OFFICE_MODEL_URL="+mirror.URL+"/bad")
		_, errOut, code := contracttest.Run(t, "office", env, "models", "download")
		if code == 0 || !strings.Contains(errOut, "SHA-256") {
			t.Fatalf("models download from a bad mirror: exit %d, %q", code, errOut)
		}
		left, _ := filepath.Glob(filepath.Join(dir, "ggml-silero-v6.2.0.bin*"))
		if len(left) != 0 {
			t.Errorf("files left after a bad download: %v", left)
		}
	})

	t.Run("a chosen model that is missing is not downloaded", func(t *testing.T) {
		m := newMeeting(t)
		data := t.TempDir()
		env := append(m.user("ada", "human", m.srv.URL), noModels...)
		env = append(env, "XDG_DATA_HOME="+data, "OFFICE_MODEL_URL="+mirror.URL+"/good",
			"OFFICE_VAD_MODEL="+filepath.Join(data, "missing.bin"))
		_, errOut, code := contracttest.Run(t, "office", env, "meeting", "join", m.sid, "--wav", testWAV)
		if code == 0 || !strings.Contains(errOut, "missing.bin not found") {
			t.Fatalf("join with a missing chosen model: exit %d, %q", code, errOut)
		}
		if _, err := os.Stat(filepath.Join(data, "agentic-office", "models", "ggml-silero-v6.2.0.bin")); err == nil {
			t.Error("the default VAD model was downloaded although another one was chosen")
		}
	})
}

// office models list shows which speech model join would use. The best model for the language,
// unless --fast, or there is no GPU (then the fast model, with a note), or a model is chosen by
// name or as a file. No model is downloaded and no hub is needed.
func TestModelSelection(t *testing.T) {
	base := []string{"OFFICE_WHISPER_MODEL=", "OFFICE_WHISPER_LANGUAGE=", "XDG_DATA_HOME=" + t.TempDir()}
	for _, c := range []struct {
		name     string
		env      []string
		args     []string
		selected string
		language string
		note     bool
	}{
		{"German with a GPU", []string{"OFFICE_WHISPER_GPU=1"}, []string{"--language", "german"}, "large-v3-turbo-german", "de", false},
		{"German is the default", []string{"OFFICE_WHISPER_GPU=1"}, nil, "large-v3-turbo-german", "de", false},
		{"English with a GPU", []string{"OFFICE_WHISPER_GPU=1"}, []string{"--language", "English"}, "large-v3-turbo", "en", false},
		{"a code", []string{"OFFICE_WHISPER_GPU=1", "OFFICE_WHISPER_LANGUAGE=fr"}, nil, "large-v3-turbo", "fr", false},
		{"fast", []string{"OFFICE_WHISPER_GPU=1"}, []string{"--language", "de", "--fast"}, "small", "de", false},
		{"no GPU", []string{"OFFICE_WHISPER_GPU=0"}, []string{"--language", "de"}, "small", "de", true},
		{"a name without a GPU", []string{"OFFICE_WHISPER_GPU=0"}, []string{"--model", "large-v3-turbo"}, "large-v3-turbo", "de", false},
		{"a name in the variable", []string{"OFFICE_WHISPER_GPU=1", "OFFICE_WHISPER_MODEL=small"}, nil, "small", "de", false},
		{"--fast before the variable", []string{"OFFICE_WHISPER_GPU=0", "OFFICE_WHISPER_MODEL=large-v3-turbo"}, []string{"--fast"}, "small", "de", false},
		{"a file", []string{"OFFICE_WHISPER_GPU=1"}, []string{"--model", "/models/own.bin"}, "/models/own.bin", "de", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, errOut, code := contracttest.Run(t, "office", append(append([]string{}, base...), c.env...),
				append([]string{"models", "list", "--json"}, c.args...)...)
			var l struct {
				Models []struct {
					Name     string `json:"name"`
					Selected bool   `json:"selected"`
				} `json:"models"`
				Selected, Language, Note string
			}
			if code != 0 || json.Unmarshal([]byte(out), &l) != nil {
				t.Fatalf("models list: exit %d, %q, %q", code, out, errOut)
			}
			if l.Selected != c.selected || l.Language != c.language || (l.Note != "") != c.note {
				t.Errorf("selected %q, language %q, note %q; want %q, %q, note %v", l.Selected, l.Language, l.Note,
					c.selected, c.language, c.note)
			}
			var names []string
			for _, m := range l.Models {
				names = append(names, m.Name)
				if m.Selected != (m.Name == c.selected) {
					t.Errorf("model %s: selected %v", m.Name, m.Selected)
				}
			}
			for _, want := range []string{"small", "large-v3-turbo", "large-v3-turbo-german"} {
				if !slices.Contains(names, want) {
					t.Errorf("the catalogue %v has no %s", names, want)
				}
			}
		})
	}

	t.Run("an unknown language", func(t *testing.T) {
		_, errOut, code := contracttest.Run(t, "office", base, "models", "list", "--language", "klingon")
		if code == 0 || !strings.Contains(errOut, "klingon") {
			t.Fatalf("exit %d, %q", code, errOut)
		}
	})
}

// office models download fetches the model that the options choose, from a mirror, and checks
// its SHA-256. A mirror with wrong files shows which file was asked for, without a download of
// a large model.
func TestModelDownloadChoice(t *testing.T) {
	var whisperFile, vadFile string
	for _, kv := range earsModels(t) {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "OFFICE_WHISPER_MODEL":
			whisperFile = v
		case "OFFICE_VAD_MODEL":
			vadFile = v
		}
	}
	var asked []string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch r.URL.Path {
		case "/ggml-small.bin":
			http.ServeFile(w, r, whisperFile)
		case "/ggml-silero-v6.2.0.bin":
			http.ServeFile(w, r, vadFile)
		default:
			w.Write([]byte("not a model"))
		}
	})
	mirror := httptest.NewServer(mux)
	t.Cleanup(mirror.Close)
	base := []string{"OFFICE_WHISPER_MODEL=", "OFFICE_VAD_MODEL=", "OFFICE_WHISPER_LANGUAGE=", "OFFICE_MODEL_URL=" + mirror.URL,
		"NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1"}

	t.Run("by name", func(t *testing.T) {
		data := t.TempDir()
		_, errOut, code := contracttest.Run(t, "office", append(base, "XDG_DATA_HOME="+data, "OFFICE_WHISPER_GPU=1"),
			"models", "download", "small")
		if code != 0 {
			t.Fatalf("models download small: exit %d, %q", code, errOut)
		}
		for _, f := range []string{"ggml-small.bin", "ggml-silero-v6.2.0.bin"} {
			if _, err := os.Stat(filepath.Join(data, "agentic-office", "models", f)); err != nil {
				t.Error(err)
			}
		}
	})

	for _, c := range []struct {
		name string
		args []string
		file string
	}{
		{"German", []string{"--language", "german"}, "/ggml-large-v3-turbo-german.bin"},
		{"English", []string{"--language", "en"}, "/ggml-large-v3-turbo.bin"},
	} {
		t.Run(c.name, func(t *testing.T) {
			asked = nil
			data := t.TempDir()
			_, errOut, code := contracttest.Run(t, "office", append(base, "XDG_DATA_HOME="+data, "OFFICE_WHISPER_GPU=1"),
				append([]string{"models", "download"}, c.args...)...)
			if code == 0 || !strings.Contains(errOut, "SHA-256") || !slices.Equal(asked, []string{c.file}) {
				t.Fatalf("models download %v: exit %d, asked %v, %q", c.args, code, asked, errOut)
			}
		})
	}
}

// join uses the model that the options choose: the best one for the language with a GPU, the
// fast one with --fast or without a GPU (and says so). A file in the model directory stands in
// for the large model, so nothing large is downloaded.
func TestEarsChooseModel(t *testing.T) {
	var whisperFile, vadFile string
	for _, kv := range earsModels(t) {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "OFFICE_WHISPER_MODEL":
			whisperFile = v
		case "OFFICE_VAD_MODEL":
			vadFile = v
		}
	}
	data := t.TempDir()
	dir := filepath.Join(data, "agentic-office", "models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ggml-small.bin", "ggml-large-v3-turbo-german.bin"} {
		if err := os.Symlink(whisperFile, filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, gpu string
		args      []string
		model     string
		note      bool
	}{
		{"German with a GPU", "1", []string{"--language", "german"}, "ggml-large-v3-turbo-german.bin", false},
		{"fast", "1", []string{"--language", "german", "--fast"}, "ggml-small.bin", false},
		{"no GPU", "0", []string{"--language", "german"}, "ggml-small.bin", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newMeeting(t)
			env := append(m.user("ada", "human", m.srv.URL), "XDG_DATA_HOME="+data, "OFFICE_WHISPER_MODEL=",
				"OFFICE_WHISPER_LANGUAGE=", "OFFICE_VAD_MODEL="+vadFile, "OFFICE_WHISPER_GPU="+c.gpu)
			e := m.startEars(env, append([]string{"--wav", testWAV}, c.args...)...)
			l := e.wait("listening", 1, 120*time.Second)[0]
			e.quit()
			if filepath.Base(l.Model) != c.model || l.Language != "de" {
				t.Errorf("listening with %s, language %q; want %s, de", l.Model, l.Language, c.model)
			}
			if got := strings.Contains(e.p.Stderr(), "no GPU"); got != c.note {
				t.Errorf("note about the GPU: %v, want %v\n%s", got, c.note, e.p.Stderr())
			}
		})
	}
}

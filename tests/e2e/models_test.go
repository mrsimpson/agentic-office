//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

	// No model chosen: the defaults in $XDG_DATA_HOME/agentic-office/models apply.
	noModels := []string{"OFFICE_WHISPER_MODEL=", "OFFICE_VAD_MODEL=", "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1"}

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

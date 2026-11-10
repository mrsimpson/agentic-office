package ears

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// model is one model file that the ears download when it is missing. The SHA-256 is pinned: a
// download with another checksum is thrown away, so the ears never load a file they do not know.
type model struct {
	what   string // shown to the user
	file   string // file name in ModelDir
	url    string // default source
	sha256 string
	size   int64
}

// models are the default models of the ears, from the Hugging Face repositories of whisper.cpp
// (the same files as "make models").
var models = []model{
	{
		what:   "Whisper model",
		file:   defaultModelFile,
		url:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin",
		sha256: "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b",
		size:   487601967,
	},
	{
		what:   "VAD model",
		file:   defaultVADFile,
		url:    "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v6.2.0.bin",
		sha256: "2aa269b785eeb53a82983a20501ddf7c1d9c48e33ab63a41391ac6c9f7fb6987",
		size:   885098,
	},
}

// DownloadModels downloads the default models into ModelDir that are not there yet, and checks
// their SHA-256. OFFICE_MODEL_URL replaces the default sources with one base URL (a mirror):
// the files are then read from <base>/<file name>. progress receives one line per step; it may
// be nil.
func DownloadModels(ctx context.Context, progress func(string)) error {
	for _, m := range models {
		if err := ensureModel(ctx, m, filepath.Join(ModelDir(), m.file), progress); err != nil {
			return err
		}
	}
	return nil
}

// ensureModel downloads m to path if path does not exist.
func ensureModel(ctx context.Context, m model, path string, progress func(string)) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	say := func(format string, a ...any) {
		if progress != nil {
			progress(fmt.Sprintf(format, a...))
		}
	}
	url := m.url
	if base := strings.TrimRight(os.Getenv("OFFICE_MODEL_URL"), "/"); base != "" {
		url = base + "/" + m.file
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	say("downloading the %s (%d MB, once) from %s to %s", m.what, (m.size+500_000)/1_000_000, url, path)
	tmp := path + ".part"
	defer os.Remove(tmp)
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download the %s: %w", m.what, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download the %s from %s: %s", m.what, url, res.Status)
	}

	h := sha256.New()
	w := &progressWriter{total: m.size, report: func(pct int) { say("  %s: %d%%", m.file, pct) }}
	if _, err := io.Copy(io.MultiWriter(f, h, w), res.Body); err != nil {
		return fmt.Errorf("download the %s: %w", m.what, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != m.sha256 {
		return fmt.Errorf("download the %s from %s: the SHA-256 is %s, expected %s; the file was not kept",
			m.what, url, got, m.sha256)
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	say("  %s: done, checksum ok", m.file)
	return nil
}

// progressWriter reports every 10 percent, but at most once in 2 seconds and only for files of
// more than 50 MB.
type progressWriter struct {
	total, done int64
	next        int
	last        time.Time
	report      func(pct int)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.total > 50_000_000 {
		pct := int(p.done * 100 / p.total)
		if pct >= p.next+10 && pct < 100 && time.Since(p.last) >= 2*time.Second {
			p.next = pct / 10 * 10
			p.last = time.Now()
			p.report(p.next)
		}
	}
	return len(b), nil
}

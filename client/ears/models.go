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
	"runtime"
	"strconv"
	"strings"
	"time"
)

// model is one model file that the ears download when it is missing. The SHA-256 is pinned: a
// download with another checksum is thrown away, so the ears never load a file they do not know.
type model struct {
	name   string // name in the catalogue, for --model
	what   string // shown to the user
	file   string // file name in ModelDir and on a mirror
	url    string // default source
	sha256 string
	size   int64
	// languages the model is made for; empty means every language of Whisper.
	languages []string
	about     string
}

// catalogue is the list of speech models that the ears know by name. The checksums and sizes are
// those that Hugging Face reports for the files (the x-linked-etag and x-linked-size headers).
var catalogue = []model{
	{
		name: "small", what: "Whisper model small", file: defaultModelFile,
		url:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin",
		sha256: "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b",
		size:   487601967,
		about:  "fast, multilingual; real time on a CPU",
	},
	{
		name: "large-v3-turbo", what: "Whisper model large-v3-turbo", file: "ggml-large-v3-turbo.bin",
		url:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin",
		sha256: "1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69",
		size:   1624555275,
		about:  "accurate, multilingual; needs a GPU for live use",
	},
	{
		// primeline/whisper-large-v3-turbo-german (Apache-2.0), converted to ggml by cstr. The
		// source is pinned to a commit, because the repository is not part of whisper.cpp.
		name: "large-v3-turbo-german", what: "Whisper model large-v3-turbo-german", file: "ggml-large-v3-turbo-german.bin",
		url:       "https://huggingface.co/cstr/whisper-large-v3-turbo-german-ggml/resolve/d9431a78952938533dc46e75395a9b0b7237fa69/ggml-model.bin",
		sha256:    "6eb2e025198a6cbac7bdb1e86e278f5de002e583aae7bdfcf5183ef8da16decd",
		size:      1624555275,
		languages: []string{"de"},
		about:     "accurate, fine-tuned for German; needs a GPU for live use",
	},
}

// fastModel is the model for --fast and for computers without a GPU.
const fastModel = "small"

// vadModel is the Silero VAD model of whisper.cpp.
var vadModel = model{
	name: "silero-v6.2.0", what: "VAD model", file: defaultVADFile,
	url:    "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v6.2.0.bin",
	sha256: "2aa269b785eeb53a82983a20501ddf7c1d9c48e33ab63a41391ac6c9f7fb6987",
	size:   885098,
	about:  "voice activity detection",
}

func catalogueModel(name string) (model, bool) {
	for _, m := range catalogue {
		if m.name == name {
			return m, true
		}
	}
	return model{}, false
}

// HasGPU reports whether this build of whisper.cpp runs on a GPU: Metal on Apple silicon. The
// builds for Linux and Intel Macs run on the CPU only. OFFICE_WHISPER_GPU=1 or 0 overrides it,
// for example for tests or for an own build with CUDA.
func HasGPU() bool {
	if v, err := strconv.ParseBool(os.Getenv("OFFICE_WHISPER_GPU")); err == nil {
		return v
	}
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

// Choice is the speech model and the language the ears use.
type Choice struct {
	// Name is the name in the catalogue, or "" for a file that the participant chose.
	Name string
	// Path is the model file.
	Path string
	// Language is the Whisper code of the language, for example "de", or "auto".
	Language string
	// Note says why the ears chose another model than the best one for the language, or "".
	Note string

	model    *model // nil for a file
	explicit bool   // chosen with --model, OFFICE_WHISPER_MODEL or --fast
}

// Choose picks the speech model. In this order:
//
//   - model, a name from the catalogue or a file (--model);
//   - fast: the fast model (--fast);
//   - OFFICE_WHISPER_MODEL, a name or a file;
//   - the best model for the language: large-v3-turbo-german for German, large-v3-turbo for
//     every other language. Without a GPU, the fast model instead, with a Note.
//
// language is a code or a name ("de", "german"); empty means OFFICE_WHISPER_LANGUAGE, then de.
func Choose(name, language string, fast bool) (Choice, error) {
	if language == "" {
		language = os.Getenv("OFFICE_WHISPER_LANGUAGE")
	}
	if language == "" {
		language = DefaultLanguage
	}
	code, err := languageCode(language)
	if err != nil {
		return Choice{}, err
	}
	c := Choice{Language: code, explicit: true}
	if name == "" && fast {
		name = fastModel
	}
	if name == "" {
		name = os.Getenv("OFFICE_WHISPER_MODEL")
	}
	if name == "" {
		c.explicit = false
		name = "large-v3-turbo"
		for _, m := range catalogue {
			if len(m.languages) == 1 && m.languages[0] == code {
				name = m.name
			}
		}
		if !HasGPU() {
			c.Note = fmt.Sprintf("no GPU in this build: the ears use the fast model %s instead of %s; "+
				"choose a model with --model", fastModel, name)
			name = fastModel
		}
	}
	if m, ok := catalogueModel(name); ok {
		c.Name, c.Path, c.model = m.name, filepath.Join(ModelDir(), m.file), &m
		return c, nil
	}
	c.Path = name
	return c, nil
}

// ensure downloads the chosen model if it is a model of the catalogue and missing. A file that
// the participant chose is never downloaded.
func (c Choice) ensure(ctx context.Context, progress func(string)) error {
	if _, err := os.Stat(c.Path); err == nil {
		return nil
	}
	if c.model == nil {
		return fmt.Errorf("Whisper model %s not found; set OFFICE_WHISPER_MODEL or --model to an existing file or to "+
			"a name of \"office models list\"", c.Path)
	}
	return ensureModel(ctx, *c.model, c.Path, progress)
}

// ModelInfo describes one model of the catalogue for "office models list".
type ModelInfo struct {
	Name       string   `json:"name"`
	File       string   `json:"file"`
	SizeBytes  int64    `json:"sizeBytes"`
	Languages  []string `json:"languages,omitempty"`
	About      string   `json:"about"`
	Downloaded bool     `json:"downloaded"`
	// Selected is true for the model that Choose picks with the same options.
	Selected bool `json:"selected"`
}

// Catalogue lists the speech models and the VAD model, and marks the one that Choose picks for
// name, language and fast. It returns that choice too.
func Catalogue(name, language string, fast bool) ([]ModelInfo, Choice, error) {
	c, err := Choose(name, language, fast)
	if err != nil {
		return nil, Choice{}, err
	}
	var out []ModelInfo
	for _, m := range append(append([]model{}, catalogue...), vadModel) {
		_, err := os.Stat(filepath.Join(ModelDir(), m.file))
		out = append(out, ModelInfo{Name: m.name, File: filepath.Join(ModelDir(), m.file), SizeBytes: m.size,
			Languages: m.languages, About: m.about, Downloaded: err == nil, Selected: m.name == c.Name})
	}
	return out, c, nil
}

// DownloadModels downloads the speech model that Choose picks for name, language and fast, and
// the VAD model, into ModelDir if they are not there yet, and checks their SHA-256.
// OFFICE_MODEL_URL replaces the default sources with one base URL (a mirror): the files are then
// read from <base>/<file name>. A VAD model chosen with OFFICE_VAD_MODEL is not downloaded.
// progress receives one line per step; it may be nil.
func DownloadModels(ctx context.Context, name, language string, fast bool, progress func(string)) error {
	c, err := Choose(name, language, fast)
	if err != nil {
		return err
	}
	if c.Note != "" && progress != nil {
		progress(c.Note)
	}
	if err := c.ensure(ctx, progress); err != nil {
		return err
	}
	if os.Getenv("OFFICE_VAD_MODEL") != "" {
		return nil
	}
	return ensureModel(ctx, vadModel, filepath.Join(ModelDir(), vadModel.file), progress)
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

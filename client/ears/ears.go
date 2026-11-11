package ears

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"

	"github.com/mrsimpson/agentic-office/client/core"
)

// Config says what the ears listen to and which models they use. Empty fields take their
// value from the environment, then from the defaults (see Defaults).
type Config struct {
	// Mic is true for the default microphone. WAV is a file to play instead.
	Mic bool
	WAV string

	// Model is the Whisper model: a name from the catalogue (see Catalogue) or a file in ggml
	// format. Env OFFICE_WHISPER_MODEL. Empty means the best model for the language (see Choose).
	// Prepare sets it to the file.
	Model string
	// Fast chooses the fast model (small) when Model is empty.
	Fast bool
	// VADModel is the Silero VAD model for whisper.cpp. Env OFFICE_VAD_MODEL.
	VADModel string
	// Language is the spoken language, a code or a name: "de", "german", "en", "english" or
	// "auto". Env OFFICE_WHISPER_LANGUAGE. Prepare sets it to the code.
	Language string
	// Threads is the number of CPU threads for Whisper. Env OFFICE_WHISPER_THREADS.
	Threads int
	// EndSilence is the silence that ends a sentence. Env OFFICE_EARS_END_SILENCE.
	EndSilence time.Duration
	// VADThreshold is the speech probability from which a window counts as speech.
	// Env OFFICE_VAD_THRESHOLD.
	VADThreshold float64

	recorder []string
	samples  []float32
}

// Defaults of Config.
const (
	DefaultLanguage     = "de"
	DefaultEndSilence   = 500 * time.Millisecond
	DefaultVADThreshold = 0.5
	defaultModelFile    = "ggml-small.bin"
	defaultVADFile      = "ggml-silero-v6.2.0.bin"
)

// DefaultThreads is the default number of Whisper threads: half of the logical CPUs (about the
// physical cores), at least 1 and at most 16. On the 32-thread reference host, the small model
// needs about 0.95 s per sentence with 8 threads and 0.7 s with 12 or 16; 4 threads need 1.5 s.
func DefaultThreads() int { return max(1, min(runtime.NumCPU()/2, 16)) }

// ModelDir is where the ears look for models by default: $XDG_DATA_HOME/agentic-office/models
// (usually ~/.local/share/agentic-office/models).
func ModelDir() string {
	d := os.Getenv("XDG_DATA_HOME")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(d, "agentic-office", "models")
}

// Prepare fills empty fields and checks that the models, the WAV file or a recorder are there.
// It chooses the speech model (see Choose) and reads the WAV file. A model of the catalogue that
// is missing is downloaded once (see DownloadModels); progress receives a line per step and may
// be nil. A model file that the user chose with a flag or a variable is never downloaded.
func (c *Config) Prepare(ctx context.Context, progress func(string)) error {
	str := func(v *string, env, def string) bool {
		if *v == "" {
			*v = os.Getenv(env)
		}
		if *v == "" {
			*v = def
			return true
		}
		return false
	}
	choice, err := Choose(c.Model, c.Language, c.Fast)
	if err != nil {
		return err
	}
	if choice.Note != "" && progress != nil {
		progress(choice.Note)
	}
	if err := choice.ensure(ctx, progress); err != nil {
		return err
	}
	c.Model, c.Language = choice.Path, choice.Language
	defVAD := str(&c.VADModel, "OFFICE_VAD_MODEL", filepath.Join(ModelDir(), defaultVADFile))
	if c.Threads <= 0 {
		if v := os.Getenv("OFFICE_WHISPER_THREADS"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return fmt.Errorf("OFFICE_WHISPER_THREADS=%q is not a positive number", v)
			}
			c.Threads = n
		} else {
			c.Threads = DefaultThreads()
		}
	}
	if c.EndSilence <= 0 {
		if v := os.Getenv("OFFICE_EARS_END_SILENCE"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return fmt.Errorf("OFFICE_EARS_END_SILENCE=%q is not a duration such as 500ms", v)
			}
			c.EndSilence = d
		} else {
			c.EndSilence = DefaultEndSilence
		}
	}
	if c.VADThreshold <= 0 {
		if v := os.Getenv("OFFICE_VAD_THRESHOLD"); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f <= 0 || f >= 1 {
				return fmt.Errorf("OFFICE_VAD_THRESHOLD=%q is not a number between 0 and 1", v)
			}
			c.VADThreshold = f
		} else {
			c.VADThreshold = DefaultVADThreshold
		}
	}
	if _, err := os.Stat(c.VADModel); err != nil {
		if !defVAD {
			return fmt.Errorf("VAD model %s not found; set OFFICE_VAD_MODEL or --vad-model to an existing file, "+
				"or unset it to use the default model", c.VADModel)
		}
		if err := ensureModel(ctx, vadModel, c.VADModel, progress); err != nil {
			return err
		}
	}
	switch {
	case c.Mic == (c.WAV != ""):
		return errors.New("use either the microphone or a WAV file")
	case c.WAV != "":
		s, err := readWAV(c.WAV)
		if err != nil {
			return err
		}
		c.samples = s
	default:
		r, err := findRecorder()
		if err != nil {
			return err
		}
		c.recorder = r
	}
	return nil
}

// Ears connect one audio source to one participant in one session.
type Ears struct {
	Config
	Client    *core.Client
	SessionID string
	// Status receives the local status of each utterance: final when Whisper has the text (the
	// command ID is created then), before it is sent.
	Status func(cmdID, text string, s core.Status)
	// Paused reports whether the participant is paused. While it is true, the ears neither
	// capture nor transcribe.
	Paused func() bool
	// Report receives progress and measurements (see Report). May be nil.
	Report func(Report)
}

// Report is one line of progress or measurement. Times are in milliseconds.
type Report struct {
	// Type is listening, final, sent, retry (not sent yet, repeating), dropped (the server
	// rejected it for good), skipped (Whisper failed), unsent (lost because the ears stopped) or
	// done (the WAV file is over and everything is sent).
	Type      string `json:"type"`
	CommandID string `json:"commandId,omitempty"`
	Text      string `json:"text,omitempty"`
	EventID   uint32 `json:"eventId,omitempty"`

	// SpeechEnd is the end of the last speech window of the sentence.
	SpeechEnd *time.Time `json:"speechEnd,omitempty"`
	// TranscribedAt is when Whisper returned the text (transcribed_at of the event).
	TranscribedAt *time.Time `json:"transcribedAt,omitempty"`
	// SubmittedAt is when the server confirmed the utterance.
	SubmittedAt *time.Time `json:"submittedAt,omitempty"`

	AudioMs             int64  `json:"audioMs,omitempty"`
	WhisperMs           int64  `json:"whisperMs,omitempty"`
	SpeechEndToFinalMs  int64  `json:"speechEndToFinalMs,omitempty"`
	SpeechEndToSubmitMs int64  `json:"speechEndToSubmitMs,omitempty"`
	Attempts            int    `json:"attempts,omitempty"`
	Duplicate           bool   `json:"duplicate,omitempty"`
	Queued              int    `json:"queued,omitempty"`
	Error               string `json:"error,omitempty"`

	// For type listening.
	Source   string `json:"source,omitempty"`
	Model    string `json:"model,omitempty"`
	Language string `json:"language,omitempty"`
	Threads  int    `json:"threads,omitempty"`
}

// String is the short text form of a report for the terminal.
func (r Report) String() string {
	switch r.Type {
	case "listening":
		return fmt.Sprintf("listening to %s (model %s, language %s, %d threads)", r.Source, filepath.Base(r.Model), r.Language, r.Threads)
	case "final":
		return fmt.Sprintf("final after %d ms (whisper %d ms for %d ms of audio)", r.SpeechEndToFinalMs, r.WhisperMs, r.AudioMs)
	case "sent":
		return fmt.Sprintf("sent #%d %d ms after the end of speech (attempts %d)", r.EventID, r.SpeechEndToSubmitMs, r.Attempts)
	case "retry":
		return fmt.Sprintf("not sent yet, %d queued, retrying: %s", r.Queued, r.Error)
	case "dropped":
		return fmt.Sprintf("dropped %q: %s", r.Text, r.Error)
	case "skipped":
		return "skipped a sentence: " + r.Error
	case "unsent":
		return fmt.Sprintf("%d sentences were not sent because the ears stopped", r.Queued)
	case "done":
		return "the audio has ended; all sentences are sent"
	}
	return r.Type
}

type utterance struct {
	cmdID, text   string
	speechEnd     time.Time
	transcribedAt time.Time
}

// Run captures, transcribes and sends until ctx ends or the audio is over. At the end of a WAV
// file it waits until every utterance is sent. Call Prepare first.
func (e *Ears) Run(ctx context.Context) error {
	if e.samples == nil && e.recorder == nil {
		if err := e.Prepare(ctx, nil); err != nil {
			return err
		}
	}
	report := func(r Report) {
		if e.Report != nil {
			e.Report(r)
		}
	}
	tr, err := newTranscriber(e.Model, e.Language, e.Threads)
	if err != nil {
		return fmt.Errorf("load Whisper model %s: %w", e.Model, err)
	}
	defer tr.close()
	vad, err := newSilero(e.VADModel, 1)
	if err != nil {
		return err
	}
	defer vad.close()
	seg := &segmenter{vad: vad, threshold: float32(e.VADThreshold), negThresh: float32(max(e.VADThreshold-0.15, 0.01)),
		endSilence: e.EndSilence, minSpeech: 250 * time.Millisecond, maxSpeech: 25 * time.Second, preRoll: 300 * time.Millisecond}

	var src source
	name := "the microphone"
	if e.WAV != "" {
		src, name = &wavSource{samples: e.samples}, e.WAV
	} else {
		src = &micSource{command: e.recorder}
	}

	frames := make(chan frame, 64)
	segs := make(chan *segment, 16)
	queue := newQueue()
	srcErr := make(chan error, 1)
	sendErr := make(chan error, 1)
	var wg sync.WaitGroup

	srcCtx, stopSrc := context.WithCancel(ctx)
	defer stopSrc()
	report(Report{Type: "listening", Source: name, Model: e.Model, Language: e.Language, Threads: e.Threads})
	go func() {
		defer close(frames)
		srcErr <- src.run(srcCtx, frames, e.Paused)
	}()
	go func() { sendErr <- e.send(ctx, queue, report) }()

	// Transcription: one sentence after the other.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for s := range segs {
			if e.Paused() {
				continue // paused before the sentence was transcribed: never transcribe it
			}
			start := time.Now()
			text, err := tr.transcribe(s.samples)
			done := time.Now()
			if err != nil {
				report(Report{Type: "skipped", Error: "whisper: " + err.Error()})
				continue
			}
			if text == "" || e.Paused() {
				continue
			}
			u := utterance{cmdID: ids.NewCommandID(), text: text, speechEnd: s.speechEnd, transcribedAt: done}
			e.Status(u.cmdID, u.text, core.StatusFinal)
			report(Report{Type: "final", CommandID: u.cmdID, Text: text, SpeechEnd: &u.speechEnd, TranscribedAt: &u.transcribedAt,
				AudioMs: int64(len(s.samples)) * 1000 / SampleRate, WhisperMs: done.Sub(start).Milliseconds(),
				SpeechEndToFinalMs: done.Sub(s.speechEnd).Milliseconds()})
			queue.push(u)
		}
	}()

	// Voice activity detection on the stream of frames.
	var last time.Time
	wasPaused := false
	var vadErr error
	for f := range frames {
		if p := e.Paused(); p || wasPaused {
			// Forget what was heard before the pause, and start fresh after it.
			if p != wasPaused {
				seg.drop()
			}
			wasPaused = p
			if p {
				continue
			}
		}
		last = f.at
		out, err := seg.push(f.samples, f.at)
		if err != nil {
			vadErr = err
			stopSrc()
			break
		}
		for _, s := range out {
			segs <- s
		}
	}
	for range frames {
	}
	if s := seg.flush(last); s != nil && vadErr == nil && ctx.Err() == nil {
		segs <- s
	}
	close(segs)
	wg.Wait()
	queue.close()
	err = errors.Join(vadErr, <-srcErr, <-sendErr)
	if err != nil || ctx.Err() != nil {
		return err
	}
	report(Report{Type: "done"})
	return nil
}

// send submits the utterances in order. A failed submit is repeated with the same command ID
// until it works, so a network drop neither loses nor duplicates an utterance
// (dec-idempotent-commands). Errors that a repeat cannot fix drop the utterance.
func (e *Ears) send(ctx context.Context, q *queue, report func(Report)) error {
	// stopped reports the utterances that are lost because the ears stop before they are sent.
	stopped := func(current int) error {
		if n := q.len() + current; n > 0 {
			report(Report{Type: "unsent", Queued: n})
		}
		return nil
	}
	for {
		u, ok := q.next(ctx)
		if !ok {
			return stopped(0)
		}
		backoff := 100 * time.Millisecond
		for attempt := 1; ; attempt++ {
			actx, cancel := context.WithTimeout(ctx, 10*time.Second)
			res, err := e.Client.SubmitUtterance(actx, e.SessionID, u.cmdID, u.text, sessionv1.Modality_MODALITY_VOICE, u.transcribedAt)
			cancel()
			if err == nil {
				at := time.Now()
				report(Report{Type: "sent", CommandID: u.cmdID, EventID: res.GetEvent().GetId(), SpeechEnd: &u.speechEnd,
					TranscribedAt: &u.transcribedAt, SubmittedAt: &at, SpeechEndToSubmitMs: at.Sub(u.speechEnd).Milliseconds(),
					Attempts: attempt, Duplicate: res.GetDuplicate()})
				break
			}
			if ctx.Err() != nil {
				return stopped(1)
			}
			if permanent(err) {
				report(Report{Type: "dropped", CommandID: u.cmdID, Text: u.text, Error: err.Error()})
				break
			}
			if attempt == 1 || attempt%10 == 0 {
				report(Report{Type: "retry", CommandID: u.cmdID, Attempts: attempt, Queued: q.len() + 1, Error: err.Error()})
			}
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return stopped(1)
			}
			backoff = min(backoff*2, time.Second)
		}
	}
}

// permanent is true for errors that a repeat of the same command cannot fix.
func permanent(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeInvalidArgument, connect.CodePermissionDenied, connect.CodeFailedPrecondition,
		connect.CodeNotFound, connect.CodeAlreadyExists:
		return true
	}
	return false
}

// queue is the local, ordered queue of utterances that are not sent yet. It holds text only.
type queue struct {
	mu     sync.Mutex
	items  []utterance
	closed bool
	wake   chan struct{}
}

func newQueue() *queue { return &queue{wake: make(chan struct{}, 1)} }

func (q *queue) push(u utterance) {
	q.mu.Lock()
	q.items = append(q.items, u)
	q.mu.Unlock()
	q.signal()
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

func (q *queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *queue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// next returns the oldest utterance, waiting for one. It returns false when the queue is closed
// and empty, or ctx ends.
func (q *queue) next(ctx context.Context) (utterance, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			u := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return u, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return utterance{}, false
		}
		select {
		case <-q.wake:
		case <-ctx.Done():
			return utterance{}, false
		}
	}
}

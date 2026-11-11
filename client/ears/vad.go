package ears

/*
#cgo LDFLAGS: -lwhisper -lggml -lggml-base -lggml-cpu -lm -lstdc++
#cgo linux LDFLAGS: -fopenmp
#cgo darwin LDFLAGS: -lggml-metal -lggml-blas
#cgo darwin LDFLAGS: -framework Accelerate -framework Metal -framework Foundation -framework CoreGraphics
#include <whisper.h>
#include <stdlib.h>

static void office_quiet_log(enum ggml_log_level level, const char * text, void * user_data) {
	(void)level; (void)text; (void)user_data;
}

// office_quiet silences the log of whisper.cpp and ggml. Without it, every VAD call prints
// several lines to stderr.
static void office_quiet(void) {
	whisper_log_set(office_quiet_log, NULL);
}

static struct whisper_vad_context * office_vad_init(const char * path, int threads) {
	struct whisper_vad_context_params p = whisper_vad_default_context_params();
	p.n_threads = threads;
	p.use_gpu = false;
	return whisper_vad_init_from_file_with_params(path, p);
}

// office_vad_prob returns the speech probability of one window, or -1 on failure. It keeps the
// state of the model between calls (streaming).
static float office_vad_prob(struct whisper_vad_context * v, const float * samples, int n) {
	if (!whisper_vad_detect_speech_no_reset(v, samples, n) || whisper_vad_n_probs(v) < 1) {
		return -1;
	}
	return whisper_vad_probs(v)[0];
}
*/
import "C"

import (
	"errors"
	"sync"
	"time"
	"unsafe"

	"github.com/mrsimpson/agentic-office/client/ears/audio"
)

// Voice activity detection.
//
// whisper.cpp ships the Silero VAD model. Its segment API (whisper_vad_segments_from_samples)
// works on a complete recording: it needs the audio after a segment to decide where the
// segment ends. A live stream has no "after". So we use the streaming part of the API: the
// model returns a speech probability for every window of 512 samples (32 ms) and keeps its
// state between windows. On top of that we apply a simple end-of-sentence rule: a sentence
// ends after EndSilence without speech. This is the same rule the segment API applies offline
// (min_silence_duration_ms), with the same thresholds.
//
// The end of a turn uses the same windows: when TurnSilence has passed since the last speech
// window and a sentence was cut since the last end of a turn, the segmenter returns a turn
// marker. The marker goes through the same channel as the sentences, so it stays behind them.

const vadWindow = 512 // samples per Silero window at 16 kHz

var quietOnce sync.Once

func quiet() { quietOnce.Do(func() { C.office_quiet() }) }

// silero is one Silero VAD context. It is not safe for parallel use.
type silero struct{ ctx *C.struct_whisper_vad_context }

func newSilero(path string, threads int) (*silero, error) {
	quiet()
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	ctx := C.office_vad_init(cp, C.int(threads))
	if ctx == nil {
		return nil, errors.New("cannot load the VAD model " + path)
	}
	// The state of the model is not zeroed when it is loaded. Without a reset, the first
	// sentences can get random probabilities (seen in tests: a lost sentence, two merged ones).
	C.whisper_vad_reset_state(ctx)
	return &silero{ctx: ctx}, nil
}

// prob returns the speech probability of one window of vadWindow samples.
func (s *silero) prob(w []float32) (float32, error) {
	p := C.office_vad_prob(s.ctx, (*C.float)(unsafe.Pointer(&w[0])), C.int(len(w)))
	if p < 0 {
		return 0, errors.New("VAD failed")
	}
	return float32(p), nil
}

func (s *silero) reset() { C.whisper_vad_reset_state(s.ctx) }

func (s *silero) close() {
	if s.ctx != nil {
		C.whisper_vad_free(s.ctx)
		s.ctx = nil
	}
}

// segment is the audio of one sentence.
type segment struct {
	samples []float32
	// speechStart and speechEnd are the times of the first and the last window with speech.
	speechStart, speechEnd time.Time
	// endedAt is when the end-of-sentence rule fired (speechEnd + EndSilence).
	endedAt time.Time
	// speech is the total duration of windows with speech.
	speech time.Duration
	// turnEnd marks the end of a turn: the segment has no audio, endedAt is when the turn
	// silence was over.
	turnEnd bool
}

func samplesOf(d time.Duration) int { return int(int64(d) * audio.SampleRate / int64(time.Second)) }

// segmenter cuts a stream of samples into sentences.
type segmenter struct {
	vad        *silero
	threshold  float32       // a window with at least this probability is speech
	negThresh  float32       // a window below this is silence (Silero: threshold - 0.15)
	endSilence time.Duration // silence that ends a sentence
	minSpeech  time.Duration // shorter sentences are dropped (clicks, coughs)
	maxSpeech  time.Duration // longer sentences are cut (Whisper takes at most 30 s)
	preRoll    time.Duration // audio kept before the first speech window
	// turnSilence is the silence after the last speech window that ends a turn. It is at least
	// endSilence.
	turnSilence time.Duration

	carry      []float32 // samples that do not fill a window yet
	pre        []float32 // last preRoll of audio while not speaking
	buf        []float32 // the current sentence
	speaking   bool
	silent     time.Duration // silence since the last speech window
	speech     time.Duration // speech in the current sentence
	start      time.Time
	lastVoiced time.Time
	// inTurn is true when a sentence was cut since the last end of a turn.
	inTurn bool
}

const windowDur = time.Duration(vadWindow) * time.Second / audio.SampleRate

// push adds samples whose last sample was captured at "at". It returns the sentences that
// ended.
func (g *segmenter) push(samples []float32, at time.Time) ([]*segment, error) {
	var out []*segment
	g.carry = append(g.carry, samples...)
	n := len(g.carry) / vadWindow * vadWindow
	for i := 0; i < n; i += vadWindow {
		w := g.carry[i : i+vadWindow]
		// The end time of this window: at, minus the samples that come after it.
		end := at.Add(-time.Duration(len(g.carry)-i-vadWindow) * time.Second / audio.SampleRate)
		p, err := g.vad.prob(w)
		if err != nil {
			return out, err
		}
		if s := g.window(w, p, end); s != nil {
			g.inTurn = true
			out = append(out, s)
		}
		if g.inTurn && !g.speaking && end.Sub(g.lastVoiced) >= g.turnSilence {
			g.inTurn = false
			out = append(out, &segment{turnEnd: true, speechEnd: g.lastVoiced, endedAt: end})
		}
	}
	g.carry = append(g.carry[:0], g.carry[n:]...)
	return out, nil
}

func (g *segmenter) window(w []float32, p float32, end time.Time) *segment {
	if !g.speaking {
		if p < g.threshold {
			g.pre = append(g.pre, w...)
			if keep := samplesOf(g.preRoll); len(g.pre) > keep {
				g.pre = append(g.pre[:0], g.pre[len(g.pre)-keep:]...)
			}
			return nil
		}
		g.speaking, g.silent, g.speech = true, 0, windowDur
		g.start, g.lastVoiced = end.Add(-windowDur), end
		g.buf = append(append([]float32(nil), g.pre...), w...)
		g.pre = g.pre[:0]
		return nil
	}
	g.buf = append(g.buf, w...)
	switch {
	case p >= g.threshold:
		g.silent = 0
		g.speech += windowDur
		g.lastVoiced = end
	case p < g.negThresh:
		g.silent += windowDur
	default:
		// Between both thresholds: neither speech nor silence (as in Silero's own rule).
	}
	cut := time.Duration(len(g.buf)) * time.Second / audio.SampleRate
	if g.silent < g.endSilence && cut < g.maxSpeech {
		return nil
	}
	s := &segment{samples: g.buf, speechStart: g.start, speechEnd: g.lastVoiced, endedAt: end, speech: g.speech}
	if g.silent < g.endSilence {
		// Too long: cut here; the speaker goes on.
		g.buf, g.start, g.speech = nil, end, 0
		return s
	}
	g.buf, g.speaking = nil, false
	g.vad.reset()
	if s.speech < g.minSpeech {
		return nil
	}
	return s
}

// flush returns the open sentence at the end of the audio, if it has enough speech.
func (g *segmenter) flush(at time.Time) *segment {
	if !g.speaking || g.speech < g.minSpeech {
		g.drop()
		return nil
	}
	s := &segment{samples: g.buf, speechStart: g.start, speechEnd: g.lastVoiced, endedAt: at, speech: g.speech}
	g.drop()
	return s
}

// drop forgets all audio and the open turn, for example when the participant pauses.
func (g *segmenter) drop() {
	g.carry, g.pre, g.buf = g.carry[:0], g.pre[:0], nil
	g.speaking, g.silent, g.speech, g.inTurn = false, 0, 0, false
	g.vad.reset()
}

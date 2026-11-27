package ears

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mrsimpson/agentic-office/client/ears/audio"
	"github.com/mrsimpson/agentic-office/client/ears/speaker"
)

// EnrolOptions say where the owner's speech comes from and where the voice print goes.
type EnrolOptions struct {
	// WAV is a recording of the owner to use instead of the microphone.
	WAV string
	// Out is the voice print file. Empty means DefaultVoicePrintFile.
	Out string
	// VADModel is the Silero VAD model (env OFFICE_VAD_MODEL), as for the ears.
	VADModel string
	// Seconds is the speech to collect. Zero means DefaultEnrolSeconds.
	Seconds int
	// Replace overwrites an existing voice print.
	Replace bool
}

// DefaultEnrolSeconds is the speech that an enrolment collects: more than the minimum, because
// a longer sample gives a steadier print.
const DefaultEnrolSeconds = 20

// EnrolResult is what Enrol did.
type EnrolResult struct {
	File    string  `json:"file"`
	Seconds float64 `json:"seconds"`
	Model   string  `json:"model"`
}

// Enrol makes the voice print of the owner from speech of the owner (dec-voice-print): from the
// microphone, or from a WAV file. The speech must be one voice. The print is written to a file
// that only the user can read; nothing is sent anywhere. progress receives one line per step.
func Enrol(ctx context.Context, o EnrolOptions, progress func(string)) (EnrolResult, error) {
	say := func(format string, a ...any) {
		if progress != nil {
			progress(fmt.Sprintf(format, a...))
		}
	}
	out := o.Out
	if out == "" {
		out = DefaultVoicePrintFile()
	}
	if _, err := os.Stat(out); err == nil && !o.Replace {
		return EnrolResult{}, fmt.Errorf("there is already a voice print at %s; use --replace to make a new one, "+
			"or \"office voice delete\" to remove it", out)
	}
	secs := o.Seconds
	if secs <= 0 {
		secs = DefaultEnrolSeconds
	}
	if secs < speaker.MinEnrolSpeech {
		return EnrolResult{}, fmt.Errorf("--seconds must be at least %d", speaker.MinEnrolSpeech)
	}

	vadPath, explicit := o.VADModel, o.VADModel != ""
	if vadPath == "" {
		vadPath = os.Getenv("OFFICE_VAD_MODEL")
		explicit = vadPath != ""
	}
	if vadPath == "" {
		vadPath = filepath.Join(ModelDir(), defaultVADFile)
	}
	if _, err := os.Stat(vadPath); err != nil {
		if explicit {
			return EnrolResult{}, fmt.Errorf("VAD model %s not found", vadPath)
		}
		if err := ensureModel(ctx, vadModel, vadPath, progress); err != nil {
			return EnrolResult{}, err
		}
	}
	spPath := filepath.Join(ModelDir(), speakerModel.file)
	if err := ensureModel(ctx, speakerModel, spPath, progress); err != nil {
		return EnrolResult{}, err
	}
	model, err := speaker.Load(spPath)
	if err != nil {
		return EnrolResult{}, fmt.Errorf("load the speaker model %s: %w", spPath, err)
	}
	vad, err := newSilero(vadPath, 1)
	if err != nil {
		return EnrolResult{}, err
	}
	defer vad.close()

	var src audio.Source
	if o.WAV != "" {
		samples, err := audio.ReadWAV(o.WAV)
		if err != nil {
			return EnrolResult{}, err
		}
		src = &fastWAV{samples: samples}
		say("reading your voice from %s", o.WAV)
	} else {
		rec, err := audio.FindRecorder()
		if err != nil {
			return EnrolResult{}, err
		}
		src = &audio.MicSource{Command: rec}
		say("Speak now, alone and in a quiet room, in your normal voice. Read anything aloud, for example a page of a book or an article. "+
			"The ears collect %d seconds of speech and keep only a voice print; the recording is not stored.", secs)
	}

	seg := &segmenter{vad: vad, threshold: DefaultVADThreshold, negThresh: DefaultVADThreshold - 0.15,
		endSilence: DefaultEndSilence, turnSilence: DefaultTurnSilence, minSpeech: 250 * time.Millisecond,
		maxSpeech: 25 * time.Second, preRoll: 100 * time.Millisecond}

	rctx, stop := context.WithCancel(ctx)
	defer stop()
	frames := make(chan audio.Frame, 64)
	srcErr := make(chan error, 1)
	go func() {
		defer close(frames)
		srcErr <- src.Run(rctx, frames, func() bool { return false })
	}()

	var speech []float32
	var last time.Time
	reported := 0
	collect := func(s *segment) {
		if s == nil || s.turnEnd {
			return
		}
		speech = append(speech, s.samples...)
		if sec := len(speech) / audio.SampleRate; sec/5 > reported/5 {
			reported = sec
			say("%d of %d seconds", min(sec, secs), secs)
		}
	}
	var vadErr error
	for f := range frames {
		last = f.At
		out, err := seg.push(f.Samples, f.At)
		if err != nil {
			vadErr = err
			stop()
			break
		}
		for _, s := range out {
			collect(s)
		}
		if len(speech) >= secs*audio.SampleRate {
			stop()
			break
		}
	}
	for range frames {
	}
	if len(speech) < secs*audio.SampleRate {
		collect(seg.flush(last))
	}
	if err := errors.Join(vadErr, <-srcErr); err != nil {
		return EnrolResult{}, err
	}
	if ctx.Err() != nil {
		return EnrolResult{}, ctx.Err()
	}
	say("making the voice print from %.1f seconds of speech", float64(len(speech))/audio.SampleRate)
	p, err := speaker.Enrol(model, speech)
	if err != nil {
		return EnrolResult{}, err
	}
	if err := p.Save(out); err != nil {
		return EnrolResult{}, err
	}
	say("voice print saved to %s (only you can read it; it never leaves this computer)", out)
	return EnrolResult{File: out, Seconds: p.Seconds, Model: p.Model}, nil
}

// fastWAV delivers a WAV file as fast as it is read, for the enrolment.
type fastWAV struct{ samples []float32 }

func (w *fastWAV) Run(ctx context.Context, out chan<- audio.Frame, _ func() bool) error {
	at := time.Now()
	for i := 0; i+audio.FrameSize <= len(w.samples); i += audio.FrameSize {
		at = at.Add(audio.FrameDur)
		select {
		case out <- audio.Frame{Samples: w.samples[i : i+audio.FrameSize], At: at}:
		case <-ctx.Done():
			return nil
		}
	}
	return nil
}

// VoicePrintInfo describes the voice print of the owner.
type VoicePrintInfo struct {
	File      string    `json:"file"`
	Exists    bool      `json:"exists"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitzero"`
	Seconds   float64   `json:"seconds,omitempty"`
}

// ShowVoicePrint says whether there is a voice print, and what it was made of. It never returns
// the numbers of the print.
func ShowVoicePrint(file string) (VoicePrintInfo, error) {
	if file == "" {
		file = DefaultVoicePrintFile()
	}
	p, err := speaker.LoadPrint(file)
	if errors.Is(err, speaker.ErrNoPrint) {
		return VoicePrintInfo{File: file}, nil
	}
	if err != nil {
		return VoicePrintInfo{}, err
	}
	return VoicePrintInfo{File: file, Exists: true, Model: p.Model, CreatedAt: p.CreatedAt, Seconds: p.Seconds}, nil
}

// DeleteVoicePrint removes the voice print. It is not an error if there is none. It reports
// whether a file was removed.
func DeleteVoicePrint(file string) (bool, error) {
	if file == "" {
		file = DefaultVoicePrintFile()
	}
	_, err := os.Stat(file)
	had := err == nil
	return had, speaker.DeletePrint(file)
}

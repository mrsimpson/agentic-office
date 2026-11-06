package ears

import (
	"math"
	"time"
)

// VAD is a simple energy-based voice activity detector (spike only).
// It cuts an utterance after EndSilence of quiet frames ("utterance.final" trigger, 300-700 ms).
type VAD struct {
	ThresholdDB float64       // frame is speech if its RMS is above this level (dBFS)
	EndSilence  time.Duration // silence that ends an utterance
	PreRoll     time.Duration // audio kept before speech starts
	MinSpeech   time.Duration // shorter bursts are dropped
	MaxSpeech   time.Duration // longer utterances are cut

	buf      []float32
	speaking bool
	voiced   int // consecutive voiced frames while not speaking
	silent   int // consecutive silent frames while speaking
	speechN  int // voiced frames in the current utterance
}

// Segment is the audio of one utterance.
type Segment struct {
	Samples []float32
	EndedAt time.Time // time of the frame that ended the utterance
}

func frames(d time.Duration) int { return int(d / frameDur) }

func rmsDB(s []float32) float64 {
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return 10 * math.Log10(sum/float64(len(s))+1e-12)
}

// Push adds one frame. It returns a segment when an utterance ends.
func (v *VAD) Push(f Frame) *Segment {
	loud := rmsDB(f.Samples) > v.ThresholdDB
	v.buf = append(v.buf, f.Samples...)
	if !v.speaking {
		if loud {
			v.voiced++
		} else {
			v.voiced = 0
		}
		if v.voiced >= 3 { // 60 ms of sound starts an utterance
			v.speaking, v.silent, v.speechN = true, 0, v.voiced
		} else if keep := (frames(v.PreRoll) + 3) * FrameSize; len(v.buf) > keep {
			v.buf = append(v.buf[:0], v.buf[len(v.buf)-keep:]...)
		}
		return nil
	}
	if loud {
		v.silent = 0
		v.speechN++
	} else {
		v.silent++
	}
	if v.silent < frames(v.EndSilence) && len(v.buf) < frames(v.MaxSpeech)*FrameSize {
		return nil
	}
	seg := &Segment{Samples: v.buf, EndedAt: f.At}
	v.buf, v.speaking, v.voiced = nil, false, 0
	if v.speechN < frames(v.MinSpeech) {
		return nil
	}
	return seg
}

// Flush returns the open utterance at the end of the input, if any.
func (v *VAD) Flush(at time.Time) *Segment {
	if !v.speaking || v.speechN < frames(v.MinSpeech) {
		return nil
	}
	seg := &Segment{Samples: v.buf, EndedAt: at}
	v.buf, v.speaking = nil, false
	return seg
}

package speaker

import (
	"math"
	"sort"
)

// The gate decides which parts of a sentence are the voice of the owner (dec-voice-print) and
// whether another voice may be in it (dec-overlap-hint). The numbers come from the measurements
// of the spike (WeSpeaker ResNet34 on German speech through simulated loudspeakers and one real
// voice); they are constants of the model and not settings.
const (
	// Threshold is the similarity from which a window counts as the owner's voice. In the
	// measurements, windows of the owner scored 0.5 or more in 100% of single sentences and
	// 97% of the owner's part in a merged one; windows of other voices scored 0.5 or more in
	// 0%.
	Threshold = 0.5
	// Doubt is the similarity below which a kept window counts as doubtful: the owner's
	// windows were below it in 3% of the cases, windows where two voices overlap in 73%.
	Doubt = 0.6

	windowFrames = 19 // 1.5 s of speech per window
	hopFrames    = 6  // the windows start every 0.5 s
	minRun       = 6  // a kept stretch shorter than this is dropped (0.5 s)
	foreignMin   = 6  // other speech of at least this length in a sentence sets the hint
	doubtShare   = 0.3
	frameSamples = frameStep * frameShift // samples that one frame of the map covers
	padSamples   = sampleRate / 10        // audio kept around a run, so no word is cut
)

// Gate holds the speaker model and the voice print of the owner.
type Gate struct {
	model *Model
	print []float32
}

// NewGate returns a gate for the voice print (a vector of EmbeddingSize values, length 1).
func NewGate(m *Model, print []float32) *Gate { return &Gate{model: m, print: print} }

// Run is a stretch of a sentence that is the owner's voice, as sample indexes [Start, End).
type Run struct{ Start, End int }

// Verdict is the result of Judge.
type Verdict struct {
	// Runs are the stretches to transcribe, in order. Empty means that the sentence is not the
	// owner's voice at all.
	Runs []Run
	// Overlap is true when another voice, or one that cannot be assigned, was heard at the same
	// time as the kept stretches or directly before or after them in this sentence.
	Overlap bool
	// Score is the best similarity of a window to the voice print.
	Score float32
	// ForeignSamples is the length of the speech that was dropped.
	ForeignSamples int
	// Windows are the similarities that the decision rests on, for debugging.
	Windows []Window
}

// Window is the similarity of one stretch of about 1.5 s to the voice print. AtMs is the middle
// of the stretch, counted from the start of the sentence.
type Window struct {
	AtMs  int64
	Score float32
}

// msPerFrame is the time that one frame of the feature map covers.
const msPerFrame = int64(frameSamples) * 1000 / sampleRate

// Judge compares the speech of one sentence (16 kHz mono, floats in -1..1) with the voice print.
func (g *Gate) Judge(samples []float32) Verdict {
	mp := g.model.Map(samples)
	if mp == nil {
		return Verdict{}
	}
	n := mp.Frames()
	voiced := voicedFrames(samples, n)
	v0, v1 := -1, 0
	for f, v := range voiced {
		if v {
			if v0 < 0 {
				v0 = f
			}
			v1 = f + 1
		}
	}
	if v0 < 0 {
		return Verdict{}
	}
	count := func(from, to int) int {
		c := 0
		for f := from; f < to; f++ {
			if voiced[f] {
				c++
			}
		}
		return c
	}
	win := func(from, to int, s float32) Window { return Window{AtMs: int64(from+to) * msPerFrame / 2, Score: s} }
	toRun := func(from, to int) Run {
		return Run{Start: max(from*frameSamples-padSamples, 0), End: min(to*frameSamples+400+padSamples, len(samples))}
	}

	if v1-v0 < windowFrames {
		e := g.model.Embed(mp, v0, v1)
		if e == nil {
			return Verdict{}
		}
		s := Similarity(e, g.print)
		if s < Threshold {
			return Verdict{Score: s, ForeignSamples: count(v0, v1) * frameSamples, Windows: []Window{win(v0, v1, s)}}
		}
		return Verdict{Runs: []Run{toRun(v0, v1)}, Overlap: s < Doubt, Score: s, Windows: []Window{win(v0, v1, s)}}
	}

	// Scores of overlapping windows.
	var starts []int
	for a := v0; a+windowFrames <= v1; a += hopFrames {
		starts = append(starts, a)
	}
	if last := v1 - windowFrames; starts[len(starts)-1] != last {
		starts = append(starts, last)
	}
	scores := make([]float32, len(starts))
	var windows []Window
	var best float32 = -1
	for i, a := range starts {
		scores[i] = Similarity(g.model.Embed(mp, a, a+windowFrames), g.print)
		best = max(best, scores[i])
		windows = append(windows, win(a, a+windowFrames, scores[i]))
	}
	// Each frame takes the score of the window whose centre is nearest.
	owner := make([]bool, n)
	doubt := make([]bool, n)
	for f := v0; f < v1; f++ {
		i := sort.Search(len(starts), func(i int) bool { return float64(starts[i])+float64(windowFrames)/2 >= float64(f)+0.5 })
		if i == len(starts) || (i > 0 && math.Abs(float64(starts[i-1])+float64(windowFrames)/2-float64(f)-0.5) <
			math.Abs(float64(starts[i])+float64(windowFrames)/2-float64(f)-0.5)) {
			i = max(i-1, 0)
		}
		owner[f], doubt[f] = scores[i] >= Threshold, scores[i] < Doubt
	}
	// Stretches of the owner's frames. A single dip inside a stretch is a doubt, not a gap.
	type span struct{ from, to int }
	var spans []span
	for f := v0; f < v1; f++ {
		if !owner[f] {
			continue
		}
		if k := len(spans) - 1; k >= 0 && spans[k].to == f {
			spans[k].to = f + 1
		} else {
			spans = append(spans, span{f, f + 1})
		}
	}
	filled := false
	for k := 1; k < len(spans); {
		if spans[k].from-spans[k-1].to <= hopFrames {
			spans[k-1].to = spans[k].to
			spans = append(spans[:k], spans[k+1:]...)
			filled = true
		} else {
			k++
		}
	}
	kept := spans[:0]
	for _, s := range spans {
		if s.to-s.from >= minRun {
			kept = append(kept, s)
		}
	}
	v := Verdict{Score: best, Windows: windows}
	inRun := make([]bool, n)
	var keptVoiced, keptDoubt int
	for _, s := range kept {
		v.Runs = append(v.Runs, toRun(s.from, s.to))
		for f := s.from; f < s.to; f++ {
			inRun[f] = true
			if voiced[f] {
				keptVoiced++
				if doubt[f] {
					keptDoubt++
				}
			}
		}
	}
	foreign := 0
	for f := v0; f < v1; f++ {
		if voiced[f] && !inRun[f] {
			foreign++
		}
	}
	v.ForeignSamples = foreign * frameSamples
	if len(kept) > 0 {
		v.Overlap = foreign >= foreignMin || filled || (keptVoiced > 0 && float64(keptDoubt)/float64(keptVoiced) > doubtShare)
	}
	return v
}

// voicedFrames marks the frames of the map that hold speech: those that are not much quieter
// than the loud ones. A gap of one frame inside speech counts as speech.
func voicedFrames(samples []float32, n int) []bool {
	rms := make([]float64, n)
	for f := range rms {
		a, b := f*frameSamples, min((f+1)*frameSamples, len(samples))
		if a >= b {
			continue
		}
		var s float64
		for _, x := range samples[a:b] {
			s += float64(x) * float64(x)
		}
		rms[f] = math.Sqrt(s / float64(b-a))
	}
	sorted := append([]float64(nil), rms...)
	sort.Float64s(sorted)
	limit := sorted[len(sorted)*9/10] * 0.1
	voiced := make([]bool, n)
	for f, r := range rms {
		voiced[f] = r > limit && r > 1e-4
	}
	for f := 1; f+1 < n; f++ {
		if !voiced[f] && voiced[f-1] && voiced[f+1] {
			voiced[f] = true
		}
	}
	return voiced
}

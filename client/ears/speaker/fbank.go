package speaker

import (
	"math"
	"math/cmplx"
	"sync"
)

// The features are Kaldi log-mel filterbanks, as the model was trained on them: 25 ms frames
// every 10 ms, 80 bands from 20 Hz to the Nyquist frequency, Povey window, pre-emphasis 0.97,
// no dither, frames that do not fit are dropped (snip_edges).
const (
	sampleRate = 16000
	frameLen   = 400 // 25 ms
	frameShift = 160 // 10 ms
	fftSize    = 512
	melBins    = 80
	lowFreq    = 20.0
	preemph    = 0.97
	epsilon    = 1.1920929e-07 // float32 epsilon, the floor of the log
)

var (
	fbOnce   sync.Once
	window   [frameLen]float64
	melBank  [melBins][fftSize/2 + 1]float64
	twiddles [fftSize / 2]complex128
)

func melScale(f float64) float64 { return 1127.0 * math.Log(1.0+f/700.0) }

func initFbank() {
	for i := range window {
		window[i] = math.Pow(0.5-0.5*math.Cos(2*math.Pi*float64(i)/float64(frameLen-1)), 0.85)
	}
	high := float64(sampleRate) / 2
	melLow, melHigh := melScale(lowFreq), melScale(high)
	delta := (melHigh - melLow) / float64(melBins+1)
	binWidth := float64(sampleRate) / fftSize
	for b := 0; b < melBins; b++ {
		left := melLow + float64(b)*delta
		center := melLow + float64(b+1)*delta
		right := melLow + float64(b+2)*delta
		for i := 0; i < fftSize/2; i++ { // the Nyquist bin has weight 0
			m := melScale(binWidth * float64(i))
			if m > left && m < right {
				if m <= center {
					melBank[b][i] = (m - left) / (center - left)
				} else {
					melBank[b][i] = (right - m) / (right - center)
				}
			}
		}
	}
	for i := range twiddles {
		twiddles[i] = cmplx.Rect(1, -2*math.Pi*float64(i)/fftSize)
	}
}

// fft is an in-place radix-2 FFT of x (length fftSize).
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, step := size/2, n/size
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				t := twiddles[k*step] * x[start+k+half]
				x[start+k+half] = x[start+k] - t
				x[start+k] += t
			}
		}
	}
}

// features returns the log-mel filterbanks of samples (16 kHz mono, floats in -1..1), one row of
// melBins values per frame. The rows are not normalised.
func features(samples []float32) [][melBins]float32 {
	fbOnce.Do(initFbank)
	if len(samples) < frameLen {
		return nil
	}
	n := 1 + (len(samples)-frameLen)/frameShift
	feats := make([][melBins]float32, n)
	var frame [frameLen]float64
	buf := make([]complex128, fftSize)
	var power [fftSize/2 + 1]float64
	for f := 0; f < n; f++ {
		src := samples[f*frameShift : f*frameShift+frameLen]
		var sum float64
		for i, s := range src {
			v := float64(s) * 32768
			frame[i] = v
			sum += v
		}
		mean := sum / frameLen
		for i := range frame {
			frame[i] -= mean
		}
		for i := frameLen - 1; i > 0; i-- {
			frame[i] -= preemph * frame[i-1]
		}
		frame[0] -= preemph * frame[0]
		for i := range buf {
			buf[i] = 0
		}
		for i := range frame {
			buf[i] = complex(frame[i]*window[i], 0)
		}
		fft(buf)
		for i := range power {
			re, im := real(buf[i]), imag(buf[i])
			power[i] = re*re + im*im
		}
		for b := 0; b < melBins; b++ {
			var e float64
			for i, w := range melBank[b] {
				if w != 0 {
					e += w * power[i]
				}
			}
			feats[f][b] = float32(math.Log(math.Max(e, epsilon)))
		}
	}
	return feats
}

// normalise subtracts the mean of each band over all frames (cepstral mean normalisation).
func normalise(feats [][melBins]float32) {
	var mean [melBins]float64
	for _, f := range feats {
		for b, v := range f {
			mean[b] += float64(v)
		}
	}
	for b := range mean {
		mean[b] /= float64(len(feats))
	}
	for i := range feats {
		for b := range feats[i] {
			feats[i][b] -= float32(mean[b])
		}
	}
}

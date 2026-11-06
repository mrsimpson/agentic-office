package ears

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

const (
	// SampleRate is the rate Whisper expects.
	SampleRate = 16000
	// FrameSize is 20 ms of audio at SampleRate.
	FrameSize = SampleRate / 50
	frameDur  = 20 * time.Millisecond
)

// Frame is 20 ms of mono audio with the time it was captured or delivered.
type Frame struct {
	Samples []float32
	At      time.Time
}

// ReadWAV reads a 16-bit PCM WAV file and returns mono samples at 16 kHz.
// Stereo is mixed down. Other rates are resampled linearly (good enough for the spike).
func ReadWAV(path string) ([]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("not a RIFF/WAVE file")
	}
	var channels, bits int
	var rate int
	var pcm []byte
	for p := 12; p+8 <= len(data); {
		id, size := string(data[p:p+4]), int(binary.LittleEndian.Uint32(data[p+4:p+8]))
		body := data[p+8 : min(p+8+size, len(data))]
		switch id {
		case "fmt ":
			if binary.LittleEndian.Uint16(body[0:2]) != 1 {
				return nil, errors.New("only PCM WAV is supported")
			}
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
		case "data":
			pcm = body
		}
		p += 8 + size + size%2
	}
	if bits != 16 || channels < 1 || rate == 0 {
		return nil, fmt.Errorf("unsupported WAV: %d bit, %d channels, %d Hz", bits, channels, rate)
	}
	n := len(pcm) / (2 * channels)
	mono := make([]float32, n)
	for i := 0; i < n; i++ {
		var sum float32
		for c := 0; c < channels; c++ {
			sum += float32(int16(binary.LittleEndian.Uint16(pcm[(i*channels+c)*2:]))) / 32768
		}
		mono[i] = sum / float32(channels)
	}
	if rate == SampleRate {
		return mono, nil
	}
	out := make([]float32, int(int64(n)*SampleRate/int64(rate)))
	step := float64(rate) / SampleRate
	for i := range out {
		x := float64(i) * step
		j := int(x)
		frac := float32(x - float64(j))
		a, b := mono[min(j, n-1)], mono[min(j+1, n-1)]
		out[i] = a + (b-a)*frac
	}
	return out, nil
}

// WAVSource sends the samples as 20 ms frames. If realtime is true, it paces them like a microphone.
func WAVSource(ctx context.Context, samples []float32, realtime bool) <-chan Frame {
	ch := make(chan Frame, 64)
	go func() {
		defer close(ch)
		start := time.Now()
		for i := 0; i+FrameSize <= len(samples); i += FrameSize {
			if realtime {
				due := start.Add(time.Duration(i/FrameSize+1) * frameDur)
				time.Sleep(time.Until(due))
			}
			select {
			case ch <- Frame{Samples: samples[i : i+FrameSize], At: time.Now()}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// MicSource captures the default ALSA device with arecord (spike only; a native binding is needed later).
func MicSource(ctx context.Context) (<-chan Frame, error) {
	cmd := exec.CommandContext(ctx, "arecord", "-q", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ch := make(chan Frame, 64)
	go func() {
		defer close(ch)
		defer cmd.Wait()
		r := bufio.NewReaderSize(out, 4*FrameSize*2)
		buf := make([]byte, FrameSize*2)
		for {
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			s := make([]float32, FrameSize)
			for i := range s {
				s[i] = float32(int16(binary.LittleEndian.Uint16(buf[i*2:]))) / 32768
			}
			select {
			case ch <- Frame{Samples: s, At: time.Now()}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

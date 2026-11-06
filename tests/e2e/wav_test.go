//go:build e2e

package e2e

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"
	"time"
)

// pcm is mono 16-bit audio, read from or written to a WAV file by the tests. The tests build
// their own WAV files from the test sentences, for example with longer pauses.
type pcm struct {
	rate    int
	samples []int16
}

func readPCM(t *testing.T, path string) pcm {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		t.Fatalf("%s is not a WAV file", path)
	}
	var out pcm
	for p := 12; p+8 <= len(b); {
		id, n := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:p+8]))
		body := b[p+8 : min(p+8+n, len(b))]
		switch id {
		case "fmt ":
			if ch, bits := binary.LittleEndian.Uint16(body[2:4]), binary.LittleEndian.Uint16(body[14:16]); ch != 1 || bits != 16 {
				t.Fatalf("%s: want mono 16-bit, got %d channels, %d bit", path, ch, bits)
			}
			out.rate = int(binary.LittleEndian.Uint32(body[4:8]))
		case "data":
			out.samples = make([]int16, len(body)/2)
			for i := range out.samples {
				out.samples[i] = int16(binary.LittleEndian.Uint16(body[i*2:]))
			}
		}
		p += 8 + n + n%2
	}
	return out
}

func (a pcm) duration() time.Duration {
	return time.Duration(len(a.samples)) * time.Second / time.Duration(a.rate)
}

// sentences splits the audio at pauses of at least 0.5 s of digital silence. The test WAV has
// exact silence between its sentences.
func (a pcm) sentences() []pcm {
	var out []pcm
	minGap := a.rate / 2
	start, quiet := -1, 0
	for i, v := range a.samples {
		if v >= -2 && v <= 2 {
			quiet++
			if quiet == minGap && start >= 0 {
				out = append(out, pcm{a.rate, a.samples[start : i-quiet+1]})
				start = -1
			}
			continue
		}
		quiet = 0
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, pcm{a.rate, a.samples[start:]})
	}
	return out
}

// join returns the parts with silence between them and after the last part.
func join(rate int, gap time.Duration, parts ...pcm) pcm {
	silence := make([]int16, int(gap.Seconds()*float64(rate)))
	out := pcm{rate: rate}
	for _, p := range parts {
		out.samples = append(out.samples, p.samples...)
		out.samples = append(out.samples, silence...)
	}
	return out
}

func (a pcm) writeWAV(t *testing.T, path string) {
	t.Helper()
	data := make([]byte, 44+2*len(a.samples))
	copy(data[0:], "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*len(a.samples)))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1) // PCM
	binary.LittleEndian.PutUint16(data[22:], 1) // mono
	binary.LittleEndian.PutUint32(data[24:], uint32(a.rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(a.rate*2))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*len(a.samples)))
	for i, v := range a.samples {
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(v))
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// raw16k returns the audio as raw 16 kHz s16le, the format of a microphone recorder.
func (a pcm) raw16k() []byte {
	n := int(int64(len(a.samples)) * 16000 / int64(a.rate))
	out := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		x := float64(i) * float64(a.rate) / 16000
		j := int(x)
		f := x - float64(j)
		s0, s1 := a.samples[min(j, len(a.samples)-1)], a.samples[min(j+1, len(a.samples)-1)]
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(float64(s0)+(float64(s1)-float64(s0))*f)))
	}
	return out
}

func (a pcm) String() string { return fmt.Sprintf("%d Hz, %v", a.rate, a.duration()) }

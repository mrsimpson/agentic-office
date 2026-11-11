// Package audio is the audio input of the ears (if-audio-in): what the ears take from the local
// audio device of the participant, or from a WAV file in its place.
//
// A Source delivers 20 ms frames of 16 kHz mono audio (SampleRate), each with the time its last
// sample was captured. While paused, a source captures nothing: the microphone recorder is
// stopped, so the device is closed; a WAV file plays on in real time, but its frames are dropped.
//
// The microphone is read through a recorder program that runs as the user and writes raw 16-bit
// little-endian PCM, 16 kHz mono, to its standard output: parec, arecord, rec or (on macOS)
// ffmpeg, in this order, or the command in OFFICE_MIC_COMMAND (FindRecorder). A WAV file must be
// 16-bit PCM; stereo is mixed down and other rates are resampled (ReadWAV).
package audio

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	// SampleRate is the rate that Whisper and the VAD expect: 16 kHz mono.
	SampleRate = 16000
	// FrameSize is the number of samples of a frame: 20 ms of audio.
	FrameSize = SampleRate / 50
	// FrameDur is the length of a frame.
	FrameDur = 20 * time.Millisecond
)

// Frame is 20 ms of mono audio and the time its last sample was captured.
type Frame struct {
	Samples []float32
	At      time.Time
}

// Source delivers audio frames. It stops capture while paused() is true: frames that a source
// would capture during a pause are never read.
type Source interface {
	// Run sends frames to out until ctx ends, the source ends or it fails.
	Run(ctx context.Context, out chan<- Frame, paused func() bool) error
}

// ReadWAV reads a 16-bit PCM WAV file and returns mono samples at 16 kHz. Stereo is mixed down;
// other rates are resampled linearly.
func ReadWAV(path string) ([]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("%s is not a WAV file", path)
	}
	var channels, bits, rate int
	var pcm []byte
	for p := 12; p+8 <= len(data); {
		id, size := string(data[p:p+4]), int(binary.LittleEndian.Uint32(data[p+4:p+8]))
		body := data[p+8 : min(p+8+size, len(data))]
		switch id {
		case "fmt ":
			if len(body) < 16 || binary.LittleEndian.Uint16(body[0:2]) != 1 {
				return nil, fmt.Errorf("%s: only PCM WAV is supported", path)
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
		return nil, fmt.Errorf("%s: unsupported WAV (%d bit, %d channels, %d Hz); use 16-bit PCM", path, bits, channels, rate)
	}
	n := len(pcm) / (2 * channels)
	mono := make([]float32, n)
	for i := range mono {
		var sum float32
		for c := 0; c < channels; c++ {
			sum += float32(int16(binary.LittleEndian.Uint16(pcm[(i*channels+c)*2:]))) / 32768
		}
		mono[i] = sum / float32(channels)
	}
	if rate == SampleRate || n == 0 {
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

// WAVSource plays a WAV file in real time, like a microphone. While paused, the file plays on
// but its frames are dropped: the speaker talks on, and nothing of it is captured.
type WAVSource struct{ Samples []float32 }

func (w *WAVSource) Run(ctx context.Context, out chan<- Frame, paused func() bool) error {
	start := time.Now()
	for i, k := 0, 0; i+FrameSize <= len(w.Samples); i, k = i+FrameSize, k+1 {
		due := start.Add(time.Duration(k+1) * FrameDur)
		select {
		case <-time.After(time.Until(due)):
		case <-ctx.Done():
			return nil
		}
		if paused() {
			continue
		}
		select {
		case out <- Frame{Samples: w.Samples[i : i+FrameSize], At: time.Now()}:
		case <-ctx.Done():
			return nil
		}
	}
	return nil
}

// MicSource records the default microphone with a recorder program that writes raw 16-bit
// little-endian PCM, 16 kHz mono, to stdout. The ears need no special rights: the recorder runs
// as the user, like any audio application. While paused, the recorder is stopped, so the
// device is closed and the system shows that the microphone is not in use.
type MicSource struct{ Command []string }

// recorders are tried in this order when OFFICE_MIC_COMMAND is not set.
var recorders = [][]string{
	{"parec", "--raw", "--rate=16000", "--channels=1", "--format=s16le"},                          // PulseAudio or PipeWire
	{"arecord", "-q", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw"},                      // ALSA
	{"rec", "-q", "-t", "raw", "-r", "16000", "-c", "1", "-b", "16", "-e", "signed-integer", "-"}, // SoX (Linux, macOS)
}

func init() {
	if runtime.GOOS == "darwin" {
		recorders = append(recorders,
			[]string{"ffmpeg", "-loglevel", "error", "-f", "avfoundation", "-i", ":0", "-ac", "1", "-ar", "16000", "-f", "s16le", "-"})
	}
}

// FindRecorder returns the recorder command: OFFICE_MIC_COMMAND (run with sh -c) or the first
// recorder that is installed.
func FindRecorder() ([]string, error) {
	if c := strings.TrimSpace(os.Getenv("OFFICE_MIC_COMMAND")); c != "" {
		return []string{"sh", "-c", c}, nil
	}
	var names []string
	for _, r := range recorders {
		if _, err := exec.LookPath(r[0]); err == nil {
			return r, nil
		}
		names = append(names, r[0])
	}
	return nil, fmt.Errorf("no audio recorder found (tried %s); install one, or set OFFICE_MIC_COMMAND "+
		"to a command that writes 16 kHz mono s16le PCM to stdout", strings.Join(names, ", "))
}

func (m *MicSource) Run(ctx context.Context, out chan<- Frame, paused func() bool) error {
	for ctx.Err() == nil {
		for paused() {
			select {
			case <-time.After(50 * time.Millisecond):
			case <-ctx.Done():
				return nil
			}
		}
		if err := m.record(ctx, out, paused); err != nil {
			return err
		}
	}
	return nil
}

// record runs the recorder until ctx ends or the participant pauses.
func (m *MicSource) record(ctx context.Context, out chan<- Frame, paused func() bool) error {
	rctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(rctx, m.Command[0], m.Command[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start recorder %s: %w", m.Command[0], err)
	}
	r := bufio.NewReaderSize(stdout, 8*FrameSize*2)
	buf := make([]byte, FrameSize*2)
	var readErr error
	for {
		if paused() {
			break
		}
		if _, err := io.ReadFull(r, buf); err != nil {
			readErr = err
			break
		}
		s := make([]float32, FrameSize)
		for i := range s {
			s[i] = float32(int16(binary.LittleEndian.Uint16(buf[i*2:]))) / 32768
		}
		select {
		case out <- Frame{Samples: s, At: time.Now()}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	stop()
	_ = cmd.Wait()
	if readErr != nil && ctx.Err() == nil && !paused() {
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			return fmt.Errorf("recorder %s stopped: %s", m.Command[0], strings.TrimSpace(stderr.String()))
		}
		return readErr
	}
	return nil
}

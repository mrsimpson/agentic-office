package speaker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ModelID names the speaker model. A voice print only fits the model that made it.
const ModelID = "wespeaker-voxceleb-resnet34-LM"

// minEnrolSpeech is the speech that an enrolment needs, and minConsistency the similarity that
// two halves of it must have: a recording with two voices or with much noise is refused.
const (
	MinEnrolSpeech = 10 // seconds
	minConsistency = 0.55
)

// Print is the voice print of the owner. It is biometric data (Art. 9 GDPR): it stays in this
// file on the device of the owner and is never sent.
type Print struct {
	Version   int       `json:"version"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"createdAt"`
	// Seconds is the speech that the print was made from.
	Seconds   float64   `json:"seconds"`
	Embedding []float32 `json:"embedding"`
}

// ErrNoPrint means that there is no voice print in the file.
var ErrNoPrint = errors.New("no voice print")

// Enrol makes a voice print from speech of the owner (16 kHz mono, floats in -1..1, silence
// already cut away). It needs at least MinEnrolSpeech seconds, and the first and the second
// half must sound like the same voice.
func Enrol(m *Model, speech []float32) (*Print, error) {
	secs := float64(len(speech)) / sampleRate
	if secs < MinEnrolSpeech {
		return nil, fmt.Errorf("%.1f s of speech is not enough for a voice print; speak for at least %d s", secs, MinEnrolSpeech)
	}
	mp := m.Map(speech)
	if mp == nil {
		return nil, errors.New("the recording is too short")
	}
	half := mp.Frames() / 2
	if s := Similarity(m.Embed(mp, 0, half), m.Embed(mp, half, mp.Frames())); s < minConsistency {
		return nil, fmt.Errorf("the two halves of the recording do not sound like the same voice (similarity %.2f, "+
			"needed %.2f): record again, alone and in a quiet room", s, minConsistency)
	}
	return &Print{Version: 1, Model: ModelID, CreatedAt: time.Now().UTC(), Seconds: secs, Embedding: m.Embed(mp, 0, mp.Frames())}, nil
}

// Save writes the print to path, readable only by the user.
func (p *Print) Save(path string) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".part"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadPrint reads a voice print. It returns ErrNoPrint if the file does not exist.
func LoadPrint(path string) (*Print, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNoPrint, path)
	}
	if err != nil {
		return nil, err
	}
	var p Print
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("read the voice print %s: %w", path, err)
	}
	if p.Version != 1 || len(p.Embedding) != EmbeddingSize {
		return nil, fmt.Errorf("the voice print %s has an unknown format; enrol again", path)
	}
	if p.Model != ModelID {
		return nil, fmt.Errorf("the voice print %s was made with the model %q, not %q; enrol again", path, p.Model, ModelID)
	}
	return &p, nil
}

// DeletePrint removes the voice print. It is not an error if there is none.
func DeletePrint(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

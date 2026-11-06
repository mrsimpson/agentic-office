package ears

import (
	"strings"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// Transcriber wraps one whisper.cpp model and context. It is not safe for parallel use.
type Transcriber struct {
	model whisper.Model
	ctx   whisper.Context
}

// NewTranscriber loads a ggml model. lang is for example "de".
func NewTranscriber(modelPath, lang string, threads uint) (*Transcriber, error) {
	m, err := whisper.New(modelPath)
	if err != nil {
		return nil, err
	}
	c, err := m.NewContext()
	if err != nil {
		m.Close()
		return nil, err
	}
	if err := c.SetLanguage(lang); err != nil {
		m.Close()
		return nil, err
	}
	c.SetThreads(threads)
	return &Transcriber{model: m, ctx: c}, nil
}

// Transcribe returns the text of one utterance.
func (t *Transcriber) Transcribe(samples []float32) (string, error) {
	if err := t.ctx.Process(samples, nil, nil, nil); err != nil {
		return "", err
	}
	var parts []string
	for {
		seg, err := t.ctx.NextSegment()
		if err != nil {
			break
		}
		parts = append(parts, strings.TrimSpace(seg.Text))
	}
	return strings.TrimSpace(strings.Join(parts, " ")), nil
}

func (t *Transcriber) Close() error { return t.model.Close() }

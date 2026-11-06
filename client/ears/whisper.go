package ears

import (
	"errors"
	"io"
	"strings"
	"unicode"

	"github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

// transcriber is one whisper.cpp model and context. It is not safe for parallel use.
type transcriber struct {
	model whisper.Model
	ctx   whisper.Context
}

func newTranscriber(modelPath, lang string, threads int) (*transcriber, error) {
	quiet()
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
	c.SetThreads(uint(threads))
	// The VAD of the ears already cut the sentence. Whisper's own VAD stays off.
	c.SetVAD(false)
	return &transcriber{model: m, ctx: c}, nil
}

// transcribe returns the text of one sentence.
func (t *transcriber) transcribe(samples []float32) (string, error) {
	if err := t.ctx.Process(samples, nil, nil, nil); err != nil {
		return "", err
	}
	var parts []string
	for {
		seg, err := t.ctx.NextSegment()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		parts = append(parts, strings.TrimSpace(seg.Text))
	}
	return clean(strings.Join(parts, " ")), nil
}

func (t *transcriber) close() { t.model.Close() }

// clean removes what Whisper writes for non-speech: annotations in brackets such as "[Musik]"
// or "(Lachen)", and text without any letter or digit. It returns "" if nothing is left.
func clean(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '[', '(', '*':
			if r == '*' && depth > 0 {
				depth--
				continue
			}
			depth++
			continue
		case ']', ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 {
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if strings.IndexFunc(out, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
		return ""
	}
	return out
}

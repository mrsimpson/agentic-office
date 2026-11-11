package ner

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// word is a word of the text after BERT's basic tokenizer, with its byte offsets.
type word struct {
	start, end int
	pieces     []int32
}

// words splits text as BERT's basic tokenizer does for a cased model: at white space, around
// punctuation and around CJK characters. Control characters are dropped. Nothing is lowercased.
// Each word gets its WordPiece IDs.
func (m *Model) words(text string) []word {
	var out []word
	start := -1
	flush := func(end int) {
		if start >= 0 {
			out = append(out, word{start: start, end: end})
			start = -1
		}
	}
	for i, r := range text {
		size := utf8.RuneLen(r)
		switch {
		case r == 0 || r == utf8.RuneError || isControl(r) || unicode.IsSpace(r):
			flush(i)
		case isPunct(r) || isCJK(r):
			flush(i)
			out = append(out, word{start: i, end: i + size})
		default:
			if start < 0 {
				start = i
			}
		}
	}
	flush(len(text))
	for i := range out {
		out[i].pieces = m.wordPieces(text[out[i].start:out[i].end])
	}
	return out
}

// wordPieces is the greedy longest-match WordPiece split of one word.
func (m *Model) wordPieces(w string) []int32 {
	if utf8.RuneCountInString(w) > 100 {
		return []int32{m.unk}
	}
	var ids []int32
	for start := 0; start < len(w); {
		end := len(w)
		found := int32(-1)
		for end > start {
			sub := w[start:end]
			if start > 0 {
				sub = "##" + sub
			}
			if id, ok := m.vocab[sub]; ok {
				found = id
				break
			}
			_, size := utf8.DecodeLastRuneInString(w[start:end])
			end -= size
		}
		if found < 0 {
			return []int32{m.unk}
		}
		ids = append(ids, found)
		start = end
	}
	return ids
}

func isControl(r rune) bool {
	if r == '\t' || r == '\n' || r == '\r' {
		return false
	}
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
}

func isPunct(r rune) bool {
	if r >= 33 && r <= 47 || r >= 58 && r <= 64 || r >= 91 && r <= 96 || r >= 123 && r <= 126 {
		return true
	}
	return unicode.IsPunct(r)
}

func isCJK(r rune) bool {
	return r >= 0x4E00 && r <= 0x9FFF || r >= 0x3400 && r <= 0x4DBF || r >= 0x20000 && r <= 0x2A6DF ||
		r >= 0x2A700 && r <= 0x2B73F || r >= 0x2B740 && r <= 0x2B81F || r >= 0x2B820 && r <= 0x2CEAF ||
		r >= 0xF900 && r <= 0xFAFF || r >= 0x2F800 && r <= 0x2FA1F
}

// Entity is a named entity in a text.
type Entity struct {
	// Text is the entity as it is written in the text.
	Text string
	// Type is PER (person), ORG (organisation) or LOC (place). The model also knows DATE.
	Type string
	// Start and End are the byte offsets in the text.
	Start, End int
}

// maxPieces is the number of word pieces per pass, without [CLS] and [SEP]. The model knows
// 512 positions; shorter passes are faster, because attention grows with the square.
const maxPieces = 254

// Entities returns the named entities of text, in order. The label of a word is the label of its
// first word piece; words with B- start an entity, following words with I- of the same type
// continue it. A long text is classified in passes of at most maxPieces word pieces.
func (m *Model) Entities(text string) ([]Entity, error) {
	ws := m.words(text)
	var out []Entity
	for start := 0; start < len(ws); {
		// Fill one pass with whole words.
		n, end := 0, start
		for end < len(ws) && (n+len(ws[end].pieces) <= maxPieces || end == start) {
			n += len(ws[end].pieces)
			end++
		}
		ids := make([]int32, 0, n+2)
		ids = append(ids, m.cls)
		first := make([]int, 0, end-start)
		for _, w := range ws[start:end] {
			first = append(first, len(ids))
			p := w.pieces
			if len(ids)+len(p) > maxPieces+1 {
				p = p[:maxPieces+1-len(ids)]
			}
			ids = append(ids, p...)
		}
		ids = append(ids, m.sep)
		cls, err := m.classify(ids)
		if err != nil {
			return nil, err
		}
		cur := -1 // index in out of the entity that a following I- word continues
		for i, w := range ws[start:end] {
			l := labels[cls[min(first[i], len(cls)-2)]]
			typ, inside := strings.TrimPrefix(strings.TrimPrefix(l, "B-"), "I-"), strings.HasPrefix(l, "I-")
			switch {
			case l == "O":
				cur = -1
			case inside && cur >= 0 && out[cur].Type == typ:
				out[cur].End = w.end
				out[cur].Text = text[out[cur].Start:out[cur].End]
			default:
				out = append(out, Entity{Text: text[w.start:w.end], Type: typ, Start: w.start, End: w.end})
				cur = len(out) - 1
			}
		}
		start = end
	}
	return out, nil
}

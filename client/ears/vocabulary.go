package ears

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The vocabulary is Whisper's initial prompt: terms of the project and the names of the
// participants. Whisper takes the prompt as text that came before the sentence, so these words
// become more likely, in this spelling.
//
// whisper.cpp keeps at most the last n_text_ctx/2 - 1 tokens of the prompt (223 for every
// Whisper model): a longer prompt loses its first terms, which are the most important ones. The
// ears therefore cut the prompt themselves. They cannot count tokens without the model, so they
// estimate them from above: one token per two bytes of a term, plus one for the separator. On
// German and English terms the estimate is 10 to 40 percent above the real count.

// DefaultVocabularyFile is the vocabulary file in the current directory that the ears read when
// no other file is given.
var DefaultVocabularyFile = filepath.Join(".office", "vocabulary.txt")

// promptTokens is the budget of the prompt in estimated tokens; it stays below the 223 tokens
// that whisper.cpp keeps.
const promptTokens = 200

// readVocabulary reads one term per line. Empty lines and lines that start with # are skipped.
// A missing file is no error when it is the default file.
func readVocabulary(path string, isDefault bool) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) && isDefault {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("vocabulary: %w", err)
	}
	defer f.Close()
	var terms []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		t := strings.Join(strings.Fields(s.Text()), " ")
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		terms = append(terms, t)
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("vocabulary %s: %w", path, err)
	}
	return terms, nil
}

// estimateTokens is the estimated number of tokens of a term in the prompt, from above.
func estimateTokens(term string) int { return (len(term)+1)/2 + 1 }

// prompt is the initial prompt and what went into it.
type prompt struct {
	text                 string
	terms, names, cut    int
	usedTerms, usedNames []string
}

// buildPrompt joins the terms in their order, then the names that are not terms already, and
// cuts what does not fit into the budget. With prev, the previous sentence follows as context;
// it gets the rest of the budget and loses its beginning if it is too long.
func buildPrompt(terms, names []string, prev string) prompt {
	var p prompt
	seen := map[string]bool{}
	budget := promptTokens
	full := false
	add := func(t string, used *[]string) {
		k := strings.ToLower(t)
		if t == "" || seen[k] {
			return
		}
		seen[k] = true
		if n := estimateTokens(t); !full && n <= budget {
			budget -= n
			*used = append(*used, t)
			return
		}
		full = true // keep the order: nothing after a term that does not fit
		p.cut++
	}
	for _, t := range terms {
		add(t, &p.usedTerms)
	}
	for _, n := range names {
		add(strings.Join(strings.Fields(n), " "), &p.usedNames)
	}
	p.terms, p.names = len(p.usedTerms), len(p.usedNames)
	all := append(append([]string{}, p.usedTerms...), p.usedNames...)
	if len(all) > 0 {
		p.text = strings.Join(all, ", ") + "."
	}
	if prev = strings.TrimSpace(prev); prev != "" && budget > 0 {
		words := strings.Fields(prev)
		for len(words) > 0 && estimateTokens(strings.Join(words, " ")) > budget {
			words = words[1:]
		}
		if len(words) > 0 {
			p.text = strings.TrimSpace(p.text + " " + strings.Join(words, " "))
		}
	}
	return p
}

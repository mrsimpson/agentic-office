package cli

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
)

// VocabularyOptions are the options of "office vocabulary extract".
type VocabularyOptions struct {
	// Dir is the directory with the project documents.
	Dir string
	// Out is the vocabulary file to write.
	Out string
	// Max is the number of terms to keep.
	Max int
	// NoModel uses the heuristics only, without the NER model.
	NoModel bool
	// Force replaces a file that "vocabulary extract" did not write.
	Force bool
}

// VocabularyTerm is one term of an extracted vocabulary.
type VocabularyTerm struct {
	Term    string   `json:"term"`
	Score   int      `json:"score"`
	Sources []string `json:"sources"`
}

// VocabularyResult is the result of "office vocabulary extract".
type VocabularyResult struct {
	File      string           `json:"file"`
	Documents int              `json:"documents"`
	Model     string           `json:"model,omitempty"`
	Terms     []VocabularyTerm `json:"terms"`
}

// ExtractVocabulary is the hook for "office vocabulary extract": it reads the documents, ranks
// their terms and writes the vocabulary file. progress receives one line per step. Nil means the
// ears are not built in.
var ExtractVocabulary func(ctx context.Context, o VocabularyOptions, progress func(string)) (VocabularyResult, error)

// cmdVocabularyExtract builds the vocabulary for Whisper's prompt from the project documents.
func cmdVocabularyExtract(e *env, fs *flag.FlagSet) func([]string) error {
	var o VocabularyOptions
	fs.StringVar(&o.Out, "out", filepath.Join(".office", "vocabulary.txt"), "vocabulary file to write")
	fs.IntVar(&o.Max, "max", 100, "number of terms to keep")
	fs.BoolVar(&o.NoModel, "no-model", false, "use the heuristics only, without the NER model (fast, no download)")
	fs.BoolVar(&o.Force, "force", false, "replace a vocabulary file that was written by hand")
	return func(args []string) error {
		if len(args) > 1 {
			return usagef("usage: office vocabulary extract [DIR] [--out FILE] [--max N] [--no-model] [--force]")
		}
		o.Dir = "."
		if len(args) == 1 {
			o.Dir = args[0]
		}
		if ExtractVocabulary == nil {
			return errEarsNotImplemented
		}
		r, err := ExtractVocabulary(e.ctx, o, e.progress)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(r)
			return nil
		}
		fmt.Fprintln(e.stdout, r.File)
		model := "the NER model " + r.Model
		if r.Model == "" {
			model = "the heuristics only"
		}
		e.progress(fmt.Sprintf("%d terms from %d documents, with %s", len(r.Terms), r.Documents, model))
		return nil
	}
}

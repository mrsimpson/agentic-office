package ears

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mrsimpson/agentic-office/client/ears/ner"
)

// Vocabulary from project documents ("office vocabulary extract", dec-ner-model).
//
// The documents are Markdown and text files. Code blocks, inline code, links and HTML are
// removed. Four sources give candidate terms, each with a weight per occurrence:
//
//   - named entities of the NER model: persons, organisations and places (3);
//   - CamelCase words such as ConnectRPC (2);
//   - acronyms such as MCP or GDPR (2);
//   - words of headings (1).
//
// A term's score is the sum of its weights. The terms are ranked by score, then by their first
// occurrence. Upper and lower case count as the same term; the most frequent spelling wins.

// nerModels are the files of the NER model: distilbert-base-multilingual-cased-ner-hrl by
// Davlan (AFL-3.0), pinned to a commit of its Hugging Face repository.
var nerModels = []model{
	{
		name: "ner-multilingual", what: "NER model", file: "ner-distilbert-multilingual-cased-hrl.safetensors",
		url:    "https://huggingface.co/Davlan/distilbert-base-multilingual-cased-ner-hrl/resolve/d421f57d5b1d36b375408588669e9340f9b11a89/model.safetensors",
		sha256: "c702d2d069b66236f57552155c90b50b0b89a7762190b423ded3910ef953ac29",
		size:   538976358,
		about:  "names of persons, organisations and places, for office vocabulary extract",
	},
	{
		name: "ner-multilingual-vocab", what: "NER vocabulary", file: "ner-distilbert-multilingual-cased-hrl.vocab.txt",
		url:    "https://huggingface.co/Davlan/distilbert-base-multilingual-cased-ner-hrl/resolve/d421f57d5b1d36b375408588669e9340f9b11a89/vocab.txt",
		sha256: "fe0fda7c425b48c516fc8f160d594c8022a0808447475c1a7c6d6479763f310c",
		size:   995526,
		about:  "word pieces of the NER model",
	},
}

// ExtractOptions say where the documents are and how many terms to keep.
type ExtractOptions struct {
	// Dir is the directory with the documents; its subdirectories are read too, except hidden
	// ones, node_modules and vendor.
	Dir string
	// Max is the number of terms to keep (default 100).
	Max int
	// NoModel uses the heuristics only, without the NER model.
	NoModel bool
}

// Term is one term of an extracted vocabulary.
type Term struct {
	Term  string `json:"term"`
	Score int    `json:"score"`
	// Sources are where the term came from: per, org, loc (NER), camelcase, acronym, heading.
	Sources []string `json:"sources"`
}

// Extraction is the result of ExtractVocabulary.
type Extraction struct {
	Documents int    `json:"documents"`
	Model     string `json:"model,omitempty"` // the NER model, or "" without
	Terms     []Term `json:"terms"`
}

// documentExts are the file types that ExtractVocabulary reads.
var documentExts = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".rst": true, ".adoc": true}

const maxDocumentSize = 2 << 20

// ExtractVocabulary reads the documents in o.Dir and ranks their terms. It downloads the NER
// model on first use (like the speech models, with a pinned SHA-256). progress receives one line
// per step; it may be nil.
func ExtractVocabulary(ctx context.Context, o ExtractOptions, progress func(string)) (Extraction, error) {
	say := func(format string, a ...any) {
		if progress != nil {
			progress(fmt.Sprintf(format, a...))
		}
	}
	if o.Dir == "" {
		o.Dir = "."
	}
	if o.Max <= 0 {
		o.Max = 100
	}
	var files []string
	err := filepath.WalkDir(o.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if p != o.Dir && (strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil && d.Type().IsRegular() && info.Size() <= maxDocumentSize &&
			documentExts[strings.ToLower(filepath.Ext(p))] {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return Extraction{}, err
	}
	if len(files) == 0 {
		return Extraction{}, fmt.Errorf("no documents (%s) in %s", strings.Join(sortedKeys(documentExts), ", "), o.Dir)
	}

	res := Extraction{Documents: len(files)}
	var m *ner.Model
	if !o.NoModel {
		var paths []string
		for _, nm := range nerModels {
			p := filepath.Join(ModelDir(), nm.file)
			if err := ensureModel(ctx, nm, p, progress); err != nil {
				return Extraction{}, err
			}
			paths = append(paths, p)
		}
		if m, err = ner.Load(paths[0], paths[1]); err != nil {
			return Extraction{}, fmt.Errorf("load the NER model: %w", err)
		}
		defer m.Close()
		res.Model = nerModels[0].name
	}

	c := newCandidates()
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return Extraction{}, err
		}
		say("reading %s (%d of %d)", f, i+1, len(files))
		b, err := os.ReadFile(f)
		if err != nil {
			return Extraction{}, err
		}
		if !utf8.Valid(b) {
			continue
		}
		for _, para := range paragraphs(string(b)) {
			if para.heading {
				for _, w := range strings.Fields(para.text) {
					if w = trimWord(w); isHeadingTerm(w) {
						c.add(w, "heading", 1)
					}
				}
			}
			for _, f := range strings.Fields(para.text) {
				// A compound with a hyphen counts as a whole and by its parts:
				// DSGVO-Beauftragte gives the acronym DSGVO.
				parts := []string{f}
				if strings.Contains(f, "-") {
					parts = append(parts, strings.Split(f, "-")...)
				}
				for _, w := range parts {
					w = trimWord(w)
					switch {
					case legalForms[w]:
					case camelCase.MatchString(w):
						c.add(w, "camelcase", 2)
					case acronym.MatchString(w):
						// The plural counts as the acronym: IDs is ID.
						if b := strings.TrimSuffix(w, "s"); b != w && acronym.MatchString(b) {
							w = b
						}
						c.add(w, "acronym", 2)
					}
				}
			}
			if m == nil {
				continue
			}
			es, err := m.Entities(para.text)
			if err != nil {
				return Extraction{}, fmt.Errorf("NER on %s: %w", f, err)
			}
			for _, e := range es {
				if e.Type == "DATE" || utf8.RuneCountInString(e.Text) < 2 || strings.IndexFunc(e.Text, unicode.IsLetter) < 0 {
					continue
				}
				c.add(e.Text, strings.ToLower(e.Type), 3)
			}
		}
	}
	res.Terms = c.ranked(o.Max)
	return res, nil
}

// VocabularyHeader starts every file that ExtractVocabulary writes. A file without it was
// written by hand, and WriteVocabulary does not replace it unless forced.
const VocabularyHeader = "# Vocabulary for Whisper, made by \"office vocabulary extract\""

// WriteVocabulary writes the terms to path, one per line, after a header. It does not replace a
// file that it did not write, unless force is true.
func WriteVocabulary(path, dir string, x Extraction, force bool) error {
	if b, err := os.ReadFile(path); err == nil && !force && !strings.HasPrefix(string(b), VocabularyHeader) {
		return fmt.Errorf("%s was not made by \"office vocabulary extract\"; use --force to replace it, or --out for another file", path)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s from %s (%d documents).\n", VocabularyHeader, dir, x.Documents)
	b.WriteString("# The most important terms first. Edit freely; lines that start with # are comments.\n")
	for _, t := range x.Terms {
		b.WriteString(t.Term + "\n")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var (
	camelCase  = regexp.MustCompile(`^\p{L}[\p{Ll}\d]*\p{Ll}\p{Lu}[\p{L}\d]*$|^\p{Lu}{2,}\p{Ll}+[\p{L}\d]*$`)
	acronym    = regexp.MustCompile(`^\p{Lu}[\p{Lu}\d&]{1,6}s?$`)
	mdLink     = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdRef      = regexp.MustCompile(`\[([^\]]*)\]\[[^\]]*\]`)
	inlineCode = regexp.MustCompile("`[^`]*`")
	htmlTag    = regexp.MustCompile(`<[^>]+>`)
	url        = regexp.MustCompile(`[a-z]+://\S+`)
	emphasis   = regexp.MustCompile(`[*_~]{1,3}`)
)

// trimWord removes punctuation at the start and the end of a word.
func trimWord(w string) string {
	return strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// isHeadingTerm is true for a word of a heading that may be a term: at least four letters, not
// a common word, and not only lower case letters after the first one in an English word.
func isHeadingTerm(w string) bool {
	if utf8.RuneCountInString(w) < 4 || stopWords[strings.ToLower(w)] {
		return false
	}
	r, _ := utf8.DecodeRuneInString(w)
	return unicode.IsUpper(r)
}

type paragraph struct {
	text    string
	heading bool
}

// paragraphs splits a Markdown or text document into paragraphs of plain text. Headings are
// paragraphs of their own. Code blocks are dropped.
func paragraphs(doc string) []paragraph {
	var out []paragraph
	var cur []string
	flush := func() {
		if t := strings.TrimSpace(strings.Join(cur, " ")); t != "" {
			out = append(out, paragraph{text: t})
		}
		cur = cur[:0]
	}
	fence := ""
	s := bufio.NewScanner(strings.NewReader(doc))
	s.Buffer(make([]byte, 1<<20), maxDocumentSize)
	for s.Scan() {
		line := s.Text()
		trim := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trim, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			flush()
			fence = trim[:3]
			continue
		}
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue // indented code
		}
		trim = plain(trim)
		switch {
		case trim == "":
			flush()
		case strings.HasPrefix(trim, "#"):
			flush()
			if h := strings.TrimSpace(strings.TrimLeft(trim, "#")); h != "" {
				out = append(out, paragraph{text: h, heading: true})
			}
		case strings.HasPrefix(trim, "|"):
			flush()
			for _, cell := range strings.Split(trim, "|") {
				if cell = strings.TrimSpace(cell); cell != "" && strings.Trim(cell, "-: ") != "" {
					out = append(out, paragraph{text: cell})
				}
			}
		case strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") || strings.HasPrefix(trim, "+ ") ||
			len(trim) > 2 && trim[0] >= '0' && trim[0] <= '9' && strings.Contains(trim[:min(4, len(trim))], ". "):
			flush()
			cur = append(cur, strings.TrimSpace(strings.TrimLeft(trim, "-*+0123456789.")))
		default:
			cur = append(cur, trim)
		}
	}
	flush()
	return out
}

// plain removes the Markdown and HTML markup of a line that carries no text.
func plain(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdRef.ReplaceAllString(s, "$1")
	s = inlineCode.ReplaceAllString(s, " ")
	s = htmlTag.ReplaceAllString(s, " ")
	s = url.ReplaceAllString(s, " ")
	s = emphasis.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.TrimPrefix(s, ">"))
}

// candidates counts the terms.
type candidates struct {
	byKey map[string]*candidate
	n     int
}

type candidate struct {
	first    int
	score    int
	sources  map[string]bool
	spelling map[string]int
}

func newCandidates() *candidates { return &candidates{byKey: map[string]*candidate{}} }

func (c *candidates) add(term, source string, weight int) {
	term = strings.Join(strings.Fields(term), " ")
	k := strings.ToLower(term)
	x, ok := c.byKey[k]
	if !ok {
		c.n++
		x = &candidate{first: c.n, sources: map[string]bool{}, spelling: map[string]int{}}
		c.byKey[k] = x
	}
	x.score += weight
	x.sources[source] = true
	x.spelling[term]++
}

func (c *candidates) ranked(n int) []Term {
	type entry struct {
		Term
		first int
	}
	var all []entry
	for _, x := range c.byKey {
		best, bestN := "", 0
		for s, k := range x.spelling {
			if k > bestN || k == bestN && s < best {
				best, bestN = s, k
			}
		}
		all = append(all, entry{Term{Term: best, Score: x.score, Sources: sortedKeys(x.sources)}, x.first})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].first < all[j].first
	})
	out := make([]Term, 0, min(n, len(all)))
	for _, e := range all[:min(n, len(all))] {
		out = append(out, e.Term)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// legalForms are parts of company names that look like acronyms or CamelCase words.
var legalForms = map[string]bool{"GmbH": true, "gGmbH": true, "AG": true, "KG": true, "KGaA": true, "OHG": true,
	"UG": true, "SE": true, "eV": true, "Ltd": true, "LLC": true, "LLP": true, "PLC": true, "Inc": true, "SA": true,
	"BV": true, "NV": true}

// stopWords are common German and English words that are no terms, even in a heading.
var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`
		about after again also aber alle allem allen aller alles also andere anderen auch auf aus
		before bei beim bevor bis what when where which while with within without from have this
		that these those their there then than they them into onto over under your yours what
		dass dann darum davon dazu dein deine dem den denn der des die dies diese diesem diesen
		dieser dieses doch dort durch eine einem einen einer eines einige euer eure für gegen
		haben hatte hier hinter ihre ihrem ihren ihrer jede jedem jeden jeder jedes kann kein
		keine können machen mehr mein meine mit muss nach nicht noch nur oder ohne sein seine
		sich sie sind soll über unser unsere unter viel vom von vor wann warum was weil welche
		wenn werden wie wird wir wo zum zur zwischen how why does done make makes made more most
		other some such only each every first next last step steps example examples note notes
		overview introduction einleitung beispiel beispiele übersicht hinweis hinweise
		`) {
		m[w] = true
	}
	return m
}()

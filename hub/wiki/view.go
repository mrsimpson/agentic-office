package wiki

import (
	"bytes"
	"math"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/mrsimpson/agentic-office/hub/artifacts/check"
)

// The weights of the fields in the search, and the parameters of BM25.
const (
	weightTitle   = 3.0
	weightTags    = 2.0
	weightSummary = 2.0
	weightText    = 1.0
	bm25K1        = 1.2
	bm25B         = 0.75
)

// entry is an artifact of the index.
type entry struct {
	path    string
	desc    check.Description
	sources []string           // the cited sources as clean paths
	terms   map[string]float64 // weighted frequency of each word
	length  float64            // weighted length of the fields
}

// view is the derived view of one repository. It is not safe for concurrent use; the service
// guards it.
type view struct {
	files    map[string]bool   // every file of the repository
	dirs     map[string]int    // every directory with the number of files below it
	entries  map[string]*entry // the described artifacts that are not stubs
	postings map[string]map[string]float64
	totalLen float64
}

func newView() *view {
	return &view{files: map[string]bool{}, dirs: map[string]int{}, entries: map[string]*entry{},
		postings: map[string]map[string]float64{}}
}

// set puts the file at p with its content into the view.
func (v *view) set(p string, content []byte) {
	if !v.files[p] {
		v.files[p] = true
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			v.dirs[d]++
		}
	}
	v.drop(p)
	d, ok := check.Describe(p, content)
	if !ok || d.Stub {
		return
	}
	e := &entry{path: p, desc: d, terms: map[string]float64{}}
	for _, s := range d.Sources {
		e.sources = append(e.sources, cleanPath(s))
	}
	add := func(text string, w float64) {
		for _, t := range words(text) {
			e.terms[t] += w
			e.length += w
		}
	}
	add(d.Title, weightTitle)
	add(strings.Join(d.Tags, " "), weightTags)
	add(d.Summary, weightSummary)
	add(string(bodyOf(p, content)), weightText)
	for t, f := range e.terms {
		if v.postings[t] == nil {
			v.postings[t] = map[string]float64{}
		}
		v.postings[t][p] = f
	}
	v.totalLen += e.length
	v.entries[p] = e
}

// remove takes the file at p out of the view.
func (v *view) remove(p string) {
	if v.files[p] {
		delete(v.files, p)
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			if v.dirs[d]--; v.dirs[d] <= 0 {
				delete(v.dirs, d)
			}
		}
	}
	v.drop(p)
}

// drop takes the entry of p out of the index and the search.
func (v *view) drop(p string) {
	e := v.entries[p]
	if e == nil {
		return
	}
	for t := range e.terms {
		delete(v.postings[t], p)
		if len(v.postings[t]) == 0 {
			delete(v.postings, t)
		}
	}
	v.totalLen -= e.length
	delete(v.entries, p)
}

// exists reports whether p is a file of the repository or a directory with files.
func (v *view) exists(p string) bool { return v.files[p] || v.dirs[p] > 0 }

// sorted returns the entries ordered by path.
func (v *view) sorted() []*entry {
	out := make([]*entry, 0, len(v.entries))
	for _, e := range v.entries {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b *entry) int { return strings.Compare(a.path, b.path) })
	return out
}

// cited returns the paths that an entry cites as a source.
func (v *view) cited() map[string]bool {
	out := map[string]bool{}
	for _, e := range v.entries {
		for _, s := range e.sources {
			out[s] = true
		}
	}
	return out
}

// hit is a result of a search.
type hit struct {
	e     *entry
	score float64
}

// search ranks the entries that contain a word of the query with BM25 over the weighted fields.
func (v *view) search(query []string) []hit {
	n := float64(len(v.entries))
	if n == 0 {
		return nil
	}
	avg := v.totalLen / n
	if avg <= 0 {
		avg = 1
	}
	scores := map[string]float64{}
	for _, t := range query {
		post := v.postings[t]
		if len(post) == 0 {
			continue
		}
		df := float64(len(post))
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		for p, f := range post {
			norm := bm25K1 * (1 - bm25B + bm25B*v.entries[p].length/avg)
			scores[p] += idf * f * (bm25K1 + 1) / (f + norm)
		}
	}
	hits := make([]hit, 0, len(scores))
	for p, s := range scores {
		hits = append(hits, hit{v.entries[p], s})
	}
	slices.SortFunc(hits, func(a, b hit) int {
		if a.score != b.score {
			if a.score > b.score {
				return -1
			}
			return 1
		}
		return strings.Compare(a.e.path, b.e.path)
	})
	return hits
}

// words splits a text into its words: runs of letters and digits, in lower case.
func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// queryWords returns the words of a query, each once.
func queryWords(q string) []string {
	ws := words(q)
	slices.Sort(ws)
	return slices.Compact(ws)
}

// bodyOf returns the content of a file without the front matter of a Markdown file.
func bodyOf(p string, content []byte) []byte {
	if path.Ext(p) != ".md" {
		return content
	}
	rest, ok := bytes.CutPrefix(content, []byte("---\n"))
	if !ok {
		if rest, ok = bytes.CutPrefix(content, []byte("---\r\n")); !ok {
			return content
		}
	}
	for off := 0; off < len(rest); {
		end := bytes.IndexByte(rest[off:], '\n')
		line := rest[off:]
		next := len(rest)
		if end >= 0 {
			line, next = rest[off:off+end], off+end+1
		}
		if string(bytes.TrimRight(line, "\r")) == "---" {
			return rest[next:]
		}
		off = next
	}
	return content
}

// cleanPath returns a path from the top of the repository in its clean form.
func cleanPath(p string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(p)), "/")
}

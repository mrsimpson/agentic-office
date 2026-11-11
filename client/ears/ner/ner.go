// Package ner finds named entities (persons, organisations, places) in German and English text.
// It is part of the building block Ears (bb-ears): "office vocabulary extract" uses it to build
// the vocabulary for Whisper's prompt (dec-vocabulary-prompt, dec-ner-model).
//
// The model is DistilBERT for token classification (distilbert-base-multilingual-cased-ner-hrl),
// read from its safetensors file and its WordPiece vocabulary. The forward pass is written in
// plain Go: no cgo and no native library. The model only classifies tokens; it does not generate
// text.
package ner

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
)

// Hyperparameters of distilbert-base-multilingual-cased.
const (
	dim       = 768
	hiddenDim = 3072
	heads     = 12
	layers    = 6
	maxPos    = 512
	lnEps     = 1e-12
)

// labels of the model, by class index (config.json of the model).
var labels = []string{"O", "B-DATE", "I-DATE", "B-PER", "I-PER", "B-ORG", "I-ORG", "B-LOC", "I-LOC"}

type linear struct {
	w   []float32 // [out][in]
	b   []float32 // [out]
	out int
	in  int
}

type layerNorm struct{ w, b []float32 }

type block struct {
	q, k, v, o  linear
	saLN, outLN layerNorm
	lin1, lin2  linear
}

// Model is a loaded NER model. It is safe for parallel use.
type Model struct {
	vocab   map[string]int32
	unk     int32
	cls     int32
	sep     int32
	embFile *os.File
	embOff  int64 // offset of the word embeddings in embFile
	nVocab  int
	pos     []float32 // [maxPos][dim]
	embLN   layerNorm
	blocks  [layers]block
	classif linear
}

// Load reads the weights (safetensors) and the vocabulary (vocab.txt, one WordPiece per line).
// The word embeddings stay in the file and are read per token.
func Load(weightsPath, vocabPath string) (*Model, error) {
	vb, err := os.ReadFile(vocabPath)
	if err != nil {
		return nil, err
	}
	m := &Model{vocab: map[string]int32{}}
	for i, l := range strings.Split(strings.TrimRight(string(vb), "\n"), "\n") {
		m.vocab[strings.TrimRight(l, "\r")] = int32(i)
	}
	var ok [3]bool
	m.unk, ok[0] = m.vocab["[UNK]"]
	m.cls, ok[1] = m.vocab["[CLS]"]
	m.sep, ok[2] = m.vocab["[SEP]"]
	if !ok[0] || !ok[1] || !ok[2] {
		return nil, fmt.Errorf("%s is not a WordPiece vocabulary of BERT", vocabPath)
	}

	f, err := os.Open(weightsPath)
	if err != nil {
		return nil, err
	}
	tensors, base, err := readHeader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("read %s: %w", weightsPath, err)
	}
	read := func(name string, shape ...int) ([]float32, error) {
		t, ok := tensors[name]
		if !ok {
			return nil, fmt.Errorf("%s: no tensor %s", weightsPath, name)
		}
		if t.Dtype != "F32" || fmt.Sprint(t.Shape) != fmt.Sprint(shape) {
			return nil, fmt.Errorf("%s: tensor %s is %s %v, expected F32 %v", weightsPath, name, t.Dtype, t.Shape, shape)
		}
		n := 1
		for _, s := range shape {
			n *= s
		}
		buf := make([]byte, 4*n)
		if _, err := f.ReadAt(buf, base+t.Offsets[0]); err != nil {
			return nil, err
		}
		out := make([]float32, n)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
		}
		return out, nil
	}
	var errs []error
	get := func(name string, shape ...int) []float32 {
		v, err := read(name, shape...)
		errs = append(errs, err)
		return v
	}
	lin := func(prefix string, out, in int) linear {
		return linear{w: get(prefix+".weight", out, in), b: get(prefix+".bias", out), out: out, in: in}
	}
	ln := func(prefix string) layerNorm {
		return layerNorm{w: get(prefix+".weight", dim), b: get(prefix+".bias", dim)}
	}
	emb, ok2 := tensors["distilbert.embeddings.word_embeddings.weight"]
	if !ok2 || emb.Dtype != "F32" || len(emb.Shape) != 2 || emb.Shape[1] != dim || emb.Shape[0] != len(m.vocab) {
		f.Close()
		return nil, fmt.Errorf("%s: the word embeddings do not fit the vocabulary %s", weightsPath, vocabPath)
	}
	m.embFile, m.embOff, m.nVocab = f, base+emb.Offsets[0], emb.Shape[0]
	m.pos = get("distilbert.embeddings.position_embeddings.weight", maxPos, dim)
	m.embLN = ln("distilbert.embeddings.LayerNorm")
	for i := range m.blocks {
		p := fmt.Sprintf("distilbert.transformer.layer.%d.", i)
		m.blocks[i] = block{
			q: lin(p+"attention.q_lin", dim, dim), k: lin(p+"attention.k_lin", dim, dim),
			v: lin(p+"attention.v_lin", dim, dim), o: lin(p+"attention.out_lin", dim, dim),
			saLN: ln(p + "sa_layer_norm"), outLN: ln(p + "output_layer_norm"),
			lin1: lin(p+"ffn.lin1", hiddenDim, dim), lin2: lin(p+"ffn.lin2", dim, hiddenDim),
		}
	}
	m.classif = lin("classifier", len(labels), dim)
	if err := errors.Join(errs...); err != nil {
		f.Close()
		return nil, err
	}
	return m, nil
}

// Close releases the weights file.
func (m *Model) Close() error { return m.embFile.Close() }

type tensorInfo struct {
	Dtype   string  `json:"dtype"`
	Shape   []int   `json:"shape"`
	Offsets []int64 `json:"data_offsets"`
}

// readHeader reads the JSON header of a safetensors file. base is where the data starts.
func readHeader(r io.ReaderAt) (map[string]tensorInfo, int64, error) {
	var n [8]byte
	if _, err := r.ReadAt(n[:], 0); err != nil {
		return nil, 0, err
	}
	size := binary.LittleEndian.Uint64(n[:])
	if size > 100<<20 {
		return nil, 0, errors.New("not a safetensors file")
	}
	h := make([]byte, size)
	if _, err := r.ReadAt(h, 8); err != nil {
		return nil, 0, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(h, &raw); err != nil {
		return nil, 0, fmt.Errorf("not a safetensors file: %w", err)
	}
	out := map[string]tensorInfo{}
	for k, v := range raw {
		if k == "__metadata__" {
			continue
		}
		var t tensorInfo
		if err := json.Unmarshal(v, &t); err != nil || len(t.Offsets) != 2 {
			return nil, 0, fmt.Errorf("tensor %s: bad header", k)
		}
		out[k] = t
	}
	return out, 8 + int64(size), nil
}

// classify returns the label index of every token of ids (with [CLS] and [SEP]).
func (m *Model) classify(ids []int32) ([]int, error) {
	n := len(ids)
	x := make([]float32, n*dim)
	row := make([]byte, 4*dim)
	for t, id := range ids {
		if int(id) >= m.nVocab {
			id = m.unk
		}
		if _, err := m.embFile.ReadAt(row, m.embOff+int64(id)*4*dim); err != nil {
			return nil, err
		}
		for i := range dim {
			x[t*dim+i] = math.Float32frombits(binary.LittleEndian.Uint32(row[4*i:])) + m.pos[t*dim+i]
		}
	}
	m.embLN.apply(x, n)
	q, k, v := make([]float32, n*dim), make([]float32, n*dim), make([]float32, n*dim)
	ctx, tmp := make([]float32, n*dim), make([]float32, n*dim)
	h := make([]float32, n*hiddenDim)
	for i := range m.blocks {
		b := &m.blocks[i]
		b.q.forward(x, q, n)
		b.k.forward(x, k, n)
		b.v.forward(x, v, n)
		attention(q, k, v, ctx, n)
		b.o.forward(ctx, tmp, n)
		for j := range x {
			x[j] += tmp[j]
		}
		b.saLN.apply(x, n)
		b.lin1.forward(x, h, n)
		for j, val := range h {
			h[j] = float32(0.5 * float64(val) * (1 + math.Erf(float64(val)/math.Sqrt2)))
		}
		b.lin2.forward(h, tmp, n)
		for j := range x {
			x[j] += tmp[j]
		}
		b.outLN.apply(x, n)
	}
	logits := make([]float32, n*len(labels))
	m.classif.forward(x, logits, n)
	out := make([]int, n)
	for t := range n {
		best := 0
		for c := range labels {
			if logits[t*len(labels)+c] > logits[t*len(labels)+best] {
				best = c
			}
		}
		out[t] = best
	}
	return out, nil
}

// forward computes y = x W^T + b for n rows. The output rows of W are split among the CPUs.
func (l *linear) forward(x, y []float32, n int) {
	workers := runtime.GOMAXPROCS(0)
	chunk := (l.out + workers - 1) / workers
	var wg sync.WaitGroup
	for start := 0; start < l.out; start += chunk {
		end := min(start+chunk, l.out)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for o := start; o < end; o++ {
				w := l.w[o*l.in : (o+1)*l.in]
				for t := range n {
					y[t*l.out+o] = dot(x[t*l.in:(t+1)*l.in], w) + l.b[o]
				}
			}
		}()
	}
	wg.Wait()
}

func dot(a, b []float32) float32 {
	var s0, s1, s2, s3 float32
	i := 0
	b = b[:len(a)]
	for ; i+4 <= len(a); i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < len(a); i++ {
		s0 += a[i] * b[i]
	}
	return s0 + s1 + s2 + s3
}

func (l *layerNorm) apply(x []float32, n int) {
	for t := range n {
		r := x[t*dim : (t+1)*dim]
		var mean, vari float64
		for _, v := range r {
			mean += float64(v)
		}
		mean /= dim
		for _, v := range r {
			d := float64(v) - mean
			vari += d * d
		}
		inv := 1 / math.Sqrt(vari/dim+lnEps)
		for i, v := range r {
			r[i] = float32((float64(v)-mean)*inv)*l.w[i] + l.b[i]
		}
	}
}

// attention is multi-head self-attention over all n tokens (no padding).
func attention(q, k, v, out []float32, n int) {
	const hd = dim / heads
	scale := float32(1 / math.Sqrt(hd))
	var wg sync.WaitGroup
	for h := range heads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scores := make([]float32, n)
			for i := range n {
				qi := q[i*dim+h*hd : i*dim+(h+1)*hd]
				maxS := float32(math.Inf(-1))
				for j := range n {
					s := dot(qi, k[j*dim+h*hd:j*dim+(h+1)*hd]) * scale
					scores[j] = s
					maxS = max(maxS, s)
				}
				var sum float32
				for j := range n {
					scores[j] = float32(math.Exp(float64(scores[j] - maxS)))
					sum += scores[j]
				}
				o := out[i*dim+h*hd : i*dim+(h+1)*hd]
				clear(o)
				for j := range n {
					p := scores[j] / sum
					vj := v[j*dim+h*hd : j*dim+(h+1)*hd]
					for d := range hd {
						o[d] += p * vj[d]
					}
				}
			}
		}()
	}
	wg.Wait()
}

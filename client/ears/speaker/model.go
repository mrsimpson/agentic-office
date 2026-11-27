package speaker

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
)

// Model is the speaker model: WeSpeaker ResNet34 (voxceleb, large-margin fine-tuned), 256
// numbers per voice. The forward pass is written in plain Go, like the NER model: no cgo and no
// native library. The weights are read from the ONNX file of the model.
type Model struct {
	stem    conv
	blocks  []block
	proj    linear
	mean    []float32 // mean vector that the model subtracts from every embedding
	threads int
}

// EmbeddingSize is the number of values of an embedding.
const EmbeddingSize = 256

// Layout of the network: four stages of residual blocks.
var (
	stageBlocks   = []int{3, 4, 6, 3}
	stageChannels = []int{32, 64, 128, 256}
)

const (
	statsDim  = 256 * 10 // channels times the 10 frequency rows that are left at the end
	frameStep = 8        // one frame of the feature map covers 8 feature frames (the strides 2*2*2)
)

// FrameDuration is the time that one frame of the feature map covers.
const FrameDuration = frameStep * frameShift * 1000 / sampleRate // milliseconds

type conv struct {
	w       []float32 // [out][in][kh][kw]
	b       []float32
	in, out int
	k       int // kernel size, 1 or 3
	stride  int
}

type block struct {
	c1, c2   conv
	shortcut *conv
}

type linear struct {
	w   []float32 // [out][in]
	b   []float32
	out int
	in  int
}

// Load reads the model from its ONNX file.
func Load(path string) (*Model, error) {
	f, err := readONNX(path)
	if err != nil {
		return nil, err
	}
	next := 0
	take := func(in, out, k, stride int) (conv, error) {
		if next >= len(f.convs) {
			return conv{}, errors.New("the model has fewer convolutions than expected")
		}
		names := f.convs[next]
		next++
		w, ok1 := f.weights[names[0]]
		b, ok2 := f.weights[names[1]]
		if !ok1 || !ok2 {
			return conv{}, fmt.Errorf("convolution %d has no weights", next)
		}
		if len(w.dims) != 4 || w.dims[0] != out || w.dims[1] != in || w.dims[2] != k || w.dims[3] != k || len(b.data) != out {
			return conv{}, fmt.Errorf("convolution %d has the shape %v, expected [%d %d %d %d]", next, w.dims, out, in, k, k)
		}
		return conv{w: w.data, b: b.data, in: in, out: out, k: k, stride: stride}, nil
	}
	m := &Model{threads: max(1, min(runtime.NumCPU(), 8))}
	if m.stem, err = take(1, stageChannels[0], 3, 1); err != nil {
		return nil, err
	}
	in := stageChannels[0]
	for s, n := range stageBlocks {
		for i := 0; i < n; i++ {
			stride, out := 1, stageChannels[s]
			if i == 0 && s > 0 {
				stride = 2
			}
			var bl block
			if bl.c1, err = take(in, out, 3, stride); err != nil {
				return nil, err
			}
			if bl.c2, err = take(out, out, 3, 1); err != nil {
				return nil, err
			}
			if stride != 1 || in != out {
				sc, err := take(in, out, 1, stride)
				if err != nil {
					return nil, err
				}
				bl.shortcut = &sc
			}
			m.blocks = append(m.blocks, bl)
			in = out
		}
	}
	if next != len(f.convs) {
		return nil, fmt.Errorf("the model has %d convolutions, expected %d", len(f.convs), next)
	}
	w, ok1 := f.weights["model.seg_1.weight"]
	b, ok2 := f.weights["model.seg_1.bias"]
	mv, ok3 := f.weights["mean_vec"]
	if !ok1 || !ok2 || !ok3 || len(w.dims) != 2 || w.dims[0] != EmbeddingSize || w.dims[1] != 2*statsDim {
		return nil, errors.New("the model has no embedding layer of the expected shape")
	}
	m.proj = linear{w: w.data, b: b.data, out: EmbeddingSize, in: 2 * statsDim}
	m.mean = mv.data
	return m, nil
}

// grid is a feature map of channels x rows x cols, stored channel by channel.
type grid struct {
	c, h, w int
	d       []float32
}

func newGrid(c, h, w int) grid { return grid{c: c, h: h, w: w, d: make([]float32, c*h*w)} }

// parallel runs fn for 0..n-1 on a few goroutines.
func (m *Model) parallel(n int, fn func(i int)) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	next := 0
	for t := 0; t < min(m.threads, n); t++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				i := next
				next++
				mu.Unlock()
				if i >= n {
					return
				}
				fn(i)
			}
		}()
	}
	wg.Wait()
}

// apply runs a convolution (padding k/2) and optionally adds a residual and applies ReLU.
func (m *Model) apply(c conv, x grid, residual *grid, relu bool) grid {
	oh := (x.h+2*(c.k/2)-c.k)/c.stride + 1
	ow := (x.w+2*(c.k/2)-c.k)/c.stride + 1
	y := newGrid(c.out, oh, ow)
	pad := c.k / 2
	m.parallel(c.out, func(o int) {
		out := y.d[o*oh*ow : (o+1)*oh*ow]
		bias := c.b[o]
		for i := range out {
			out[i] = bias
		}
		for i := 0; i < c.in; i++ {
			src := x.d[i*x.h*x.w : (i+1)*x.h*x.w]
			ker := c.w[(o*c.in+i)*c.k*c.k : (o*c.in+i+1)*c.k*c.k]
			for kh := 0; kh < c.k; kh++ {
				for kw := 0; kw < c.k; kw++ {
					wt := ker[kh*c.k+kw]
					if wt == 0 {
						continue
					}
					for r := 0; r < oh; r++ {
						ih := r*c.stride + kh - pad
						if ih < 0 || ih >= x.h {
							continue
						}
						row := src[ih*x.w : (ih+1)*x.w]
						dst := out[r*ow : (r+1)*ow]
						// Columns c with 0 <= c*stride+kw-pad < x.w.
						lo := 0
						if d := pad - kw; d > 0 {
							lo = (d + c.stride - 1) / c.stride
						}
						hi := ow
						if limit := x.w - 1 - kw + pad; limit < 0 {
							hi = 0
						} else if l := limit/c.stride + 1; l < hi {
							hi = l
						}
						if c.stride == 1 {
							s := row[lo+kw-pad : hi+kw-pad]
							d := dst[lo:hi]
							for j := range d {
								d[j] += wt * s[j]
							}
						} else {
							for j := lo; j < hi; j++ {
								dst[j] += wt * row[j*c.stride+kw-pad]
							}
						}
					}
				}
			}
		}
		if residual != nil {
			res := residual.d[o*oh*ow : (o+1)*oh*ow]
			for i := range out {
				out[i] += res[i]
			}
		}
		if relu {
			for i, v := range out {
				if v < 0 {
					out[i] = 0
				}
			}
		}
	})
	return y
}

// Map is the feature map of a stretch of speech: for each of its frames and for each of the
// 256*10 values, the output of the last convolution. Embeddings of any part of it are cheap.
type Map struct {
	g grid
}

// Frames is the number of frames of the map. A frame covers FrameDuration milliseconds.
func (p *Map) Frames() int { return p.g.w }

// Map runs the convolutions over samples (16 kHz mono, floats in -1..1). The features are
// normalised over the whole stretch, as the model expects. It returns nil for less than 0.4 s.
func (m *Model) Map(samples []float32) *Map {
	feats := features(samples)
	if len(feats) < 40 {
		return nil
	}
	normalise(feats)
	x := newGrid(1, melBins, len(feats))
	for t, f := range feats {
		for b, v := range f {
			x.d[b*len(feats)+t] = v
		}
	}
	x = m.apply(m.stem, x, nil, true)
	for _, bl := range m.blocks {
		h := m.apply(bl.c1, x, nil, true)
		res := x
		if bl.shortcut != nil {
			res = m.apply(*bl.shortcut, x, nil, false)
		}
		x = m.apply(bl.c2, h, &res, true)
	}
	return &Map{g: x}
}

// Embed returns the embedding (256 values, length 1) of frames [from, to) of the map. Statistics
// pooling gives the mean and the standard deviation over the frames of each of the 2560 values.
func (m *Model) Embed(p *Map, from, to int) []float32 {
	g := p.g
	from, to = max(from, 0), min(to, g.w)
	n := to - from
	if n < 2 {
		return nil
	}
	v := make([]float32, 2*statsDim)
	rows := g.h
	for c := 0; c < g.c; c++ {
		for r := 0; r < rows; r++ {
			row := g.d[(c*g.h+r)*g.w+from : (c*g.h+r)*g.w+to]
			var sum float64
			for _, x := range row {
				sum += float64(x)
			}
			mean := sum / float64(n)
			var sq float64
			for _, x := range row {
				d := float64(x) - mean
				sq += d * d
			}
			i := c*rows + r
			v[i] = float32(mean)
			v[statsDim+i] = float32(math.Sqrt(sq/float64(n-1) + 1e-8))
		}
	}
	e := make([]float32, EmbeddingSize)
	var norm float64
	for o := range e {
		w := m.proj.w[o*m.proj.in : (o+1)*m.proj.in]
		s := float64(m.proj.b[o])
		for i, x := range v {
			s += float64(w[i]) * float64(x)
		}
		s -= float64(m.mean[o])
		e[o] = float32(s)
		norm += s * s
	}
	norm = math.Sqrt(norm)
	for i := range e {
		e[i] = float32(float64(e[i]) / norm)
	}
	return e
}

// Similarity is the cosine similarity of two embeddings of length 1.
func Similarity(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

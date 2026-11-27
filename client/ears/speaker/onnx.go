package speaker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"

	"google.golang.org/protobuf/encoding/protowire"
)

// tensor is one weight of the model: float32 values in row-major order.
type tensor struct {
	dims []int
	data []float32
}

// onnxFile holds what the model needs from an ONNX file: the weights by name and the names of
// the weights of each convolution, in the order of the graph. The forward pass is written in Go;
// the file only supplies numbers.
type onnxFile struct {
	weights map[string]tensor
	convs   [][2]string // weight and bias of each Conv node, in graph order
}

// readONNX reads the weights of an ONNX file. It reads only the parts of the format that it
// needs (ModelProto.graph, GraphProto.node and .initializer, NodeProto, TensorProto).
func readONNX(path string) (*onnxFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var graph []byte
	if err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
		if num == 7 && typ == protowire.BytesType {
			graph = v
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("not an ONNX file: %w", err)
	}
	if graph == nil {
		return nil, errors.New("not an ONNX file: no graph")
	}
	f := &onnxFile{weights: map[string]tensor{}}
	err = fields(graph, func(num protowire.Number, typ protowire.Type, v []byte, _ uint64) error {
		if typ != protowire.BytesType {
			return nil
		}
		switch num {
		case 1: // node
			var op string
			var in []string
			if err := fields(v, func(n protowire.Number, t protowire.Type, s []byte, _ uint64) error {
				switch {
				case n == 1 && t == protowire.BytesType:
					in = append(in, string(s))
				case n == 4 && t == protowire.BytesType:
					op = string(s)
				}
				return nil
			}); err != nil {
				return err
			}
			if op == "Conv" {
				if len(in) != 3 {
					return fmt.Errorf("a convolution has %d inputs, expected 3 (input, weight, bias)", len(in))
				}
				f.convs = append(f.convs, [2]string{in[1], in[2]})
			}
		case 5: // initializer
			name, t, err := readTensor(v)
			if err != nil {
				return err
			}
			if t != nil {
				f.weights[name] = *t
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the ONNX graph: %w", err)
	}
	return f, nil
}

// readTensor reads a TensorProto. It returns nil for tensors that are not float32.
func readTensor(b []byte) (string, *tensor, error) {
	var name string
	var dims []int
	var dtype uint64
	var raw []byte
	err := fields(b, func(num protowire.Number, typ protowire.Type, v []byte, x uint64) error {
		switch {
		case num == 1 && typ == protowire.VarintType:
			dims = append(dims, int(x))
		case num == 1 && typ == protowire.BytesType: // packed
			for len(v) > 0 {
				d, n := protowire.ConsumeVarint(v)
				if n < 0 {
					return protowire.ParseError(n)
				}
				dims = append(dims, int(d))
				v = v[n:]
			}
		case num == 2 && typ == protowire.VarintType:
			dtype = x
		case num == 8 && typ == protowire.BytesType:
			name = string(v)
		case num == 9 && typ == protowire.BytesType:
			raw = v
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if dtype != 1 || raw == nil { // 1 is float32
		return name, nil, nil
	}
	n := 1
	for _, d := range dims {
		n *= d
	}
	if len(raw) != 4*n {
		return "", nil, fmt.Errorf("weight %s: %d bytes for %d values", name, len(raw), n)
	}
	t := &tensor{dims: dims, data: make([]float32, n)}
	for i := range t.data {
		t.data[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return name, t, nil
}

// fields calls fn for each field of a protobuf message. v is the payload of a length-delimited
// field; x is the value of a varint field.
func fields(b []byte, fn func(num protowire.Number, typ protowire.Type, v []byte, x uint64) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		switch typ {
		case protowire.VarintType:
			x, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			if err := fn(num, typ, nil, x); err != nil {
				return err
			}
			b = b[n:]
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			if err := fn(num, typ, v, 0); err != nil {
				return err
			}
			b = b[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return protowire.ParseError(n)
			}
			b = b[n:]
		}
	}
	return nil
}

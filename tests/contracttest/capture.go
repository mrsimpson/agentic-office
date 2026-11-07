package contracttest

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	// Register the office contracts, so that requests can be decoded by their path.
	_ "github.com/mrsimpson/agentic-office/hub/artifacts/api"
	_ "github.com/mrsimpson/agentic-office/hub/identity/api"
	_ "github.com/mrsimpson/agentic-office/hub/journal/api"
	_ "github.com/mrsimpson/agentic-office/hub/session/api"
)

// Request is one RPC request that a client sent, decoded from the plaintext that a decrypting
// Proxy recorded.
type Request struct {
	// Path is the HTTP path, for example /office.session.v1.SessionService/SubmitUtterance.
	Path        string
	ContentType string
	// Body is the request body as sent (after removing HTTP framing).
	Body []byte
	// Messages are the request messages in the body: one for a unary call, one or more for a
	// stream. They are decoded with the input type of the method.
	Messages []proto.Message
	// HeaderBytes is the size of the request headers as decoded (names and values).
	HeaderBytes int
}

// DecodeRequests decodes the plaintext of one client connection (HTTP/2 or HTTP/1.1) into RPC
// requests. Every request must be a known method of the office protocol, and every message must
// decode with its input type; otherwise it returns an error. So a successful decode accounts
// for every byte the client sent: HTTP framing, headers and request messages.
func DecodeRequests(stream []byte) ([]Request, error) {
	if bytes.HasPrefix(stream, []byte(http2.ClientPreface)) {
		return decodeHTTP2(stream[len(http2.ClientPreface):])
	}
	return decodeHTTP1(stream)
}

func decodeHTTP2(b []byte) ([]Request, error) {
	fr := http2.NewFramer(io.Discard, bytes.NewReader(b))
	fr.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	fr.MaxHeaderListSize = 1 << 20
	type stream struct {
		req  Request
		body bytes.Buffer
		done bool
	}
	streams := map[uint32]*stream{}
	var order []uint32
	for {
		f, err := fr.ReadFrame()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break // the connection ended, possibly in the middle of a frame (a cut)
		}
		if err != nil {
			return nil, fmt.Errorf("http2: %w", err)
		}
		switch f := f.(type) {
		case *http2.MetaHeadersFrame:
			s := streams[f.StreamID]
			if s == nil {
				s = &stream{}
				streams[f.StreamID] = s
				order = append(order, f.StreamID)
			}
			for _, h := range f.Fields {
				s.req.HeaderBytes += len(h.Name) + len(h.Value)
				switch h.Name {
				case ":path":
					s.req.Path = h.Value
				case "content-type":
					s.req.ContentType = h.Value
				}
			}
		case *http2.DataFrame:
			s := streams[f.StreamID]
			if s == nil {
				return nil, fmt.Errorf("http2: data for unknown stream %d", f.StreamID)
			}
			s.body.Write(f.Data())
		case *http2.SettingsFrame, *http2.WindowUpdateFrame, *http2.PingFrame, *http2.RSTStreamFrame,
			*http2.GoAwayFrame, *http2.PriorityFrame:
			// control frames carry no payload of the application
		default:
			return nil, fmt.Errorf("http2: unexpected frame %v", f.Header())
		}
	}
	var out []Request
	for _, id := range order {
		s := streams[id]
		s.req.Body = s.body.Bytes()
		if err := decodeBody(&s.req); err != nil {
			return out, err
		}
		out = append(out, s.req)
	}
	return out, nil
}

func decodeHTTP1(b []byte) ([]Request, error) {
	r := bufio.NewReader(bytes.NewReader(b))
	var out []Request
	for {
		hr, err := http.ReadRequest(r)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, fmt.Errorf("http/1.1: %w", err)
		}
		body, err := io.ReadAll(hr.Body)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return out, err
		}
		req := Request{Path: hr.URL.Path, ContentType: hr.Header.Get("Content-Type"), Body: body}
		for k, vs := range hr.Header {
			for _, v := range vs {
				req.HeaderBytes += len(k) + len(v)
			}
		}
		if err := decodeBody(&req); err != nil {
			return out, err
		}
		out = append(out, req)
	}
}

// decodeBody decodes the Connect body of req with the input type of its method.
func decodeBody(req *Request) error {
	in, err := inputType(req.Path)
	if err != nil {
		return err
	}
	ct := req.ContentType
	var raw [][]byte
	switch {
	case strings.HasPrefix(ct, "application/connect+"):
		// Streaming: envelopes of 1 byte flags, 4 bytes length, message.
		for b := req.Body; len(b) > 0; {
			if len(b) < 5 {
				return fmt.Errorf("%s: torn envelope", req.Path)
			}
			flags, n := b[0], int(binary.BigEndian.Uint32(b[1:5]))
			if len(b) < 5+n {
				break // cut in the middle of a message
			}
			m := b[5 : 5+n]
			if flags&1 != 0 {
				if m, err = gunzip(m); err != nil {
					return err
				}
			}
			if flags&2 == 0 {
				raw = append(raw, m)
			}
			b = b[5+n:]
		}
		ct = strings.TrimPrefix(ct, "application/connect+")
	case strings.HasPrefix(ct, "application/"):
		raw = [][]byte{req.Body}
		ct = strings.TrimPrefix(ct, "application/")
	default:
		return fmt.Errorf("%s: unexpected content type %q", req.Path, req.ContentType)
	}
	for _, m := range raw {
		msg := dynamicpb.NewMessage(in)
		var err error
		switch ct {
		case "proto":
			err = proto.UnmarshalOptions{}.Unmarshal(m, msg)
		case "json":
			err = protojson.Unmarshal(m, msg)
		default:
			err = fmt.Errorf("unexpected codec %q", ct)
		}
		if err != nil {
			return fmt.Errorf("%s: decode %s: %w", req.Path, in.FullName(), err)
		}
		req.Messages = append(req.Messages, msg)
	}
	return nil
}

func gunzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// inputType returns the request message type of an RPC path /<service>/<method>.
func inputType(path string) (protoreflect.MessageDescriptor, error) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("%q is not an RPC path", path)
	}
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(parts[0]))
	if err != nil {
		return nil, fmt.Errorf("%q: unknown service: %w", path, err)
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%q: %s is not a service", path, parts[0])
	}
	md := sd.Methods().ByName(protoreflect.Name(parts[1]))
	if md == nil {
		return nil, fmt.Errorf("%q: unknown method", path)
	}
	return md.Input(), nil
}

// Field is one populated scalar field of a decoded message.
type Field struct {
	// Name is the full name of the field, for example office.session.v1.SubmitUtteranceRequest.text.
	Name string
	// Kind is the protobuf kind (string, bytes, int64, …).
	Kind protoreflect.Kind
	// Size is the length of a string or bytes value, else 0.
	Size int
	// Value is the value of a string field.
	Value string
}

// Fields returns every populated scalar field of m and its nested messages. It also returns the
// total size of unknown fields: data in the message that its type does not describe.
func Fields(m proto.Message) (fields []Field, unknown int) {
	var walk func(protoreflect.Message)
	walk = func(m protoreflect.Message) {
		unknown += len(m.GetUnknown())
		m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			visit := func(v protoreflect.Value) {
				switch fd.Kind() {
				case protoreflect.MessageKind, protoreflect.GroupKind:
					walk(v.Message())
				case protoreflect.StringKind:
					fields = append(fields, Field{Name: string(fd.FullName()), Kind: fd.Kind(), Size: len(v.String()), Value: v.String()})
				case protoreflect.BytesKind:
					fields = append(fields, Field{Name: string(fd.FullName()), Kind: fd.Kind(), Size: len(v.Bytes())})
				default:
					fields = append(fields, Field{Name: string(fd.FullName()), Kind: fd.Kind()})
				}
			}
			switch {
			case fd.IsList():
				for i := 0; i < v.List().Len(); i++ {
					visit(v.List().Get(i))
				}
			case fd.IsMap():
				v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					visit(mv)
					return true
				})
			default:
				visit(v)
			}
			return true
		})
	}
	walk(m.ProtoReflect())
	return fields, unknown
}

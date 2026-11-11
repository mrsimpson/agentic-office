// Package logfile defines the stored form of a session log, as part of the session stream
// (if-session-stream): the line form of an event and the YAML form of a signed tree head.
//
// The local log of a participant (if-local-log) and the session log in the archive of the hub
// (dec-yaml-git-storage) use the same line form, so a copy can be compared line by line. A
// reader rebuilds every event from its line with the event types of the session interface, and
// the Merkle leaf of the event (package merkle) from the event.
//
// A line is the canonical JSON form of the event (protojson, field names in lowerCamelCase, the
// event ID as a number, the type in dotted form) without spaces, ending with a newline.
//
// A tree head file is YAML with the field names of the contract (session_id, schema, tree_size,
// root_hash, signature, public_key); bytes are base64. The schema in it is the schema of the
// session (dec-session-schema).
package logfile

import (
	"bytes"
	"encoding/json"

	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"

	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
)

// MarshalLine returns the line for ev, including the final newline.
// protojson adds random spaces on purpose; the line is compacted so it is stable.
func MarshalLine(ev *sessionstreamv1.Event) ([]byte, error) {
	raw, err := protojson.Marshal(ev)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// UnmarshalLine parses one line (with or without the final newline).
func UnmarshalLine(line []byte) (*sessionstreamv1.Event, error) {
	ev := &sessionstreamv1.Event{}
	if err := protojson.Unmarshal(bytes.TrimRight(line, "\n"), ev); err != nil {
		return nil, err
	}
	return ev, nil
}

// UnmarshalLines parses a whole log file: one event per line. A last line without a newline is
// parsed too.
func UnmarshalLines(data []byte) ([]*sessionstreamv1.Event, error) {
	var out []*sessionstreamv1.Event
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		if len(line) == 0 {
			continue
		}
		ev, err := UnmarshalLine(line)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

// MarshalTreeHead returns the YAML form of a signed tree head. The fields are in the order of
// the contract.
func MarshalTreeHead(h *sessionstreamv1.SignedTreeHead) ([]byte, error) {
	raw, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(h)
	if err != nil {
		return nil, err
	}
	// JSON is YAML: parse it into a node, which keeps the order of the fields, and write it in
	// block style with plain keys. Strings keep their quotes, so a session ID such as "123" stays a
	// string.
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	blockStyle(&doc)
	return yaml.Marshal(&doc)
}

func blockStyle(n *yaml.Node) {
	n.Style &^= yaml.FlowStyle
	for i, c := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			c.Style = 0
		}
		blockStyle(c)
	}
}

// UnmarshalTreeHead parses the YAML form of a signed tree head.
func UnmarshalTreeHead(data []byte) (*sessionstreamv1.SignedTreeHead, error) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	h := &sessionstreamv1.SignedTreeHead{}
	if err := protojson.Unmarshal(raw, h); err != nil {
		return nil, err
	}
	return h, nil
}

// Package locallog defines the line format of the local session log (if-local-log):
// one compact canonical JSON line per event, ending with a newline.
package locallog

import (
	"bytes"
	"encoding/json"

	"google.golang.org/protobuf/encoding/protojson"

	sessionv1 "github.com/mrsimpson/agentic-office/server/session/api"
)

// MarshalLine returns the line for ev, including the final newline.
// protojson adds random spaces on purpose; the line is compacted so it is stable.
func MarshalLine(ev *sessionv1.Event) ([]byte, error) {
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
func UnmarshalLine(line []byte) (*sessionv1.Event, error) {
	ev := &sessionv1.Event{}
	if err := protojson.Unmarshal(bytes.TrimRight(line, "\n"), ev); err != nil {
		return nil, err
	}
	return ev, nil
}

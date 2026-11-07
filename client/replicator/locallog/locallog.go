// Package locallog defines the line format of the local session log (if-local-log):
// one compact canonical JSON line per event, ending with a newline.
//
// The format belongs to the session stream (package logfile of the session interface), so the
// local log and the session log in the archive of the hub have the same lines. This package
// only passes it on.
package locallog

import (
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/logfile"
)

// MarshalLine returns the line for ev, including the final newline (logfile.MarshalLine).
func MarshalLine(ev *sessionv1.Event) ([]byte, error) { return logfile.MarshalLine(ev) }

// UnmarshalLine parses one line, with or without the final newline (logfile.UnmarshalLine).
func UnmarshalLine(line []byte) (*sessionv1.Event, error) { return logfile.UnmarshalLine(line) }

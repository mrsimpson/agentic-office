// Package metrics writes spike measurements as JSON lines to stderr
// when OFFICE_SPIKE_METRICS=1. It is only for the spike.
package metrics

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

var (
	enabled = os.Getenv("OFFICE_SPIKE_METRICS") == "1"
	mu      sync.Mutex
)

// Emit writes one metric line with the current time in Unix nanoseconds.
func Emit(name string, fields map[string]any) {
	if !enabled {
		return
	}
	m := map[string]any{"metric": name, "at_unix_nano": time.Now().UnixNano()}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	mu.Lock()
	defer mu.Unlock()
	os.Stderr.Write(append([]byte("METRIC "), append(b, '\n')...))
}

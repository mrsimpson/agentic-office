package contracttest

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var (
	acpDoubleOnce sync.Once
	acpDoublePath string
	acpDoubleErr  string
)

// ACPDouble builds the test double of an ACP agent harness (tests/acpdouble) once and returns
// the path of its binary. See the package comment of tests/acpdouble for what it does.
func ACPDouble(t testing.TB) string {
	t.Helper()
	acpDoubleOnce.Do(func() {
		dir, err := os.MkdirTemp("", "office-acpdouble-")
		if err != nil {
			acpDoubleErr = err.Error()
			return
		}
		p := filepath.Join(dir, "acpdouble")
		cmd := exec.Command("go", "build", "-o", p, "github.com/mrsimpson/agentic-office/tests/acpdouble")
		cmd.Dir = moduleRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			acpDoubleErr = err.Error() + "\n" + string(out)
			return
		}
		acpDoublePath = p
	})
	if acpDoubleErr != "" {
		t.Fatalf("build the ACP test double: %s", acpDoubleErr)
	}
	return acpDoublePath
}

// ACPRecord is one step that the ACP test double recorded.
type ACPRecord map[string]any

// Kind is the kind of the step: start, initialize, session/new, prompt, prompt_end,
// permission, cancel, mcp_error or exit.
func (r ACPRecord) Kind() string { s, _ := r["kind"].(string); return s }

// Text is the text of a prompt.
func (r ACPRecord) Text() string { s, _ := r["text"].(string); return s }

// ReadACPRecords reads the record file of the ACP test double. A missing file has no records.
func ReadACPRecords(t testing.TB, path string) []ACPRecord {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []ACPRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var r ACPRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			continue // a line in the middle of being written
		}
		out = append(out, r)
	}
	return out
}

// WaitACPRecord waits until a record matches and returns it with all records so far.
func WaitACPRecord(t testing.TB, path string, timeout time.Duration, match func(ACPRecord) bool) (ACPRecord, []ACPRecord) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		recs := ReadACPRecords(t, path)
		for _, r := range recs {
			if match(r) {
				return r, recs
			}
		}
		if time.Now().After(deadline) {
			b, _ := json.MarshalIndent(recs, "", "  ")
			t.Fatalf("the ACP test double did not record the expected step within %s; records:\n%s", timeout, b)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

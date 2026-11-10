//go:build e2e

package e2e

import "testing"

// slow marks a test that waits in real time on purpose: it plays long recordings, cuts the
// network for seconds or writes the archive slowly. The quick run ("make e2e", which runs
// "go test -short") skips it; "make e2e-all" and the CI run it.
func slow(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("slow: waits in real time on purpose; run it with make e2e-all")
	}
}

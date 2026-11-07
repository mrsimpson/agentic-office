//go:build e2e && performance

package e2e

// assertTiming is true in the performance tests (make performance, build tags e2e and
// performance): the timing criteria of the quality scenarios fail the test. Run them on the
// reference host.
const assertTiming = true

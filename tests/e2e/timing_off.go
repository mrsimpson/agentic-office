//go:build e2e && !performance

package e2e

// assertTiming is false in the end-to-end tests: they check behaviour only and never fail on a
// timing criterion. They still print the numbers. Build with the tag performance to check the
// timing criteria of the quality scenarios (make performance).
const assertTiming = false

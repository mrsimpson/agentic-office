// Package cli is the building block office CLI (bb-cli in docs/architecture/05-building-blocks.arc42.md).
//
// It is the shell interface for humans and agents and provides if-cli. It is a thin adapter over
// the shared client core in client/participant (dec-cli-and-mcp): it parses commands, prints
// text or --json output and runs the log replicator (client/replicator) as its own process.
//
// Conventions:
//
//   - Flags may come before or after the positional arguments. Every command takes the common
//     flags --json, --session, --server, --ca-cert, --key and --runtime-dir.
//   - With --json, stdout carries exactly one JSON object, also on failure
//     ({"error":{"code":…,"message":…}}). Events have the form of their line in the local log.
//   - Exit codes: 0 success, 1 error, 2 usage error.
//
// "office meeting join" starts the replicator detached and returns the log path at once; with
// --foreground the replicator is a child that stops when join ends (see replicator_proc.go).
// "office meeting view" is the gateway view (dec-gateway-cli). The ears (client/ears) plug in
// through CheckEars and StartEars, which cmd/office sets; "join --mic" or "--wav" runs them with
// the gateway view. Without them, --mic and --wav fail with the code unimplemented. README.md
// has the user documentation.
package cli

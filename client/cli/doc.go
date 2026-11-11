// Package cli is the building block office CLI (bb-cli in docs/architecture/05-building-blocks.arc42.md).
//
// It is the shell interface for humans and agents. It realizes if-cli, whose contract is package
// commands: the commands, the exit codes and the form of the --json output. It is a thin adapter
// over the client core (if-client-core, dec-cli-and-mcp): it parses commands, prints text or
// --json output and runs the log replicator (if-replication) as its own process.
//
// "office meeting join" starts the replicator detached and returns the log path at once; with
// --foreground the replicator is a child that stops when join ends (see replicator_proc.go).
// "office meeting view" is the gateway view (dec-gateway-cli). The ears (client/ears) plug in
// through CheckEars and StartEars, which the wiring in client/ sets; "join --mic" or "--wav" runs them with
// the gateway view. Without them, --mic and --wav fail with the code unimplemented. "office mcp"
// runs the MCP server (client/mcp) on stdin and stdout. README.md has the user documentation.
package cli

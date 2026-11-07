// Package core is the client core (bb-client-core, if-client-core in
// docs/architecture/05-building-blocks.arc42.md). The office CLI (client/cli) and the MCP server
// (client/mcp) are thin adapters on top of it (dec-cli-and-mcp).
//
// It holds everything that does not depend on the shell or on MCP:
//
//   - Config: where the server is, which certificate to trust, which key to use and where the
//     local files live. ConfigFromEnv reads the OFFICE_* environment variables.
//   - Client: authenticated Connect clients for all office interfaces (Dial). Every session
//     command creates its own command ID and is repeated once if the token was rejected
//     (dec-idempotent-commands).
//   - Join keeps its join command ID in a state file, so a restarted client stays the same
//     participant. Leave deletes it.
//   - Paths: the private runtime directory with one directory per session and one set of files
//     per participant (log, join state, PID file of the replicator).
//   - ReadLog and FollowLog read the local session log (if-local-log) that client/replicator
//     writes.
//   - The shared shapes of output and input: EventJSON, MessageJSON and ResultJSON (the JSON
//     of the CLI's --json and of the MCP tools), ParseDeck and ParseEntryKind.
//   - Gateway: the state behind the gateway view (dec-gateway-cli): own utterances with their
//     status, and whether the participant is paused. It is driven by the local log and, when the
//     ears run (client/ears), by their local status.
//
// Files in the runtime directory (default $XDG_RUNTIME_DIR/agentic-office, mode 0700):
//
//	<session>/                      one directory per session, mode 0700
//	<session>/<participant>.jsonl   local session log, mode 0600
//	<session>/<participant>.tree-head.yaml  latest signed tree head over the local log (see client/replicator)
//	<session>/<participant>.join    join state: session ID and join command ID
//	<session>/<participant>.pid     PID of the running replicator
//	<session>/<participant>.replicator.log  output of a detached replicator
//
// Session and participant IDs that are not safe as file names are encoded (see FileName).
package core

// Package coreapi is the client core API (if-client-core): the in-process interface of the client
// core for its adapters, the office CLI, the MCP server, the ears and the agent runner
// (dec-cli-and-mcp). The client core (client/core) implements it; the wiring of the participant
// client hands its Dial to the adapters.
//
// It holds the contract and its types:
//
//   - Config: where the server is, which certificate to trust, which key to use and where the
//     local files live. ConfigFromEnv reads the OFFICE_* environment variables.
//   - Client: an authenticated client of the office hub for one actor, with the commands of a
//     participant. Every session command creates its own command ID (NewCommandID) and is
//     repeated once if the token was rejected (dec-idempotent-commands). Join keeps its join
//     command ID in a state file, so a restarted client stays the same participant. Leave
//     deletes it.
//   - Paths: the private runtime directory with one directory per session and one set of files
//     per participant (log, join state, PID file of the replicator).
//   - The shared shapes of output and input: EventJSON, MessageJSON and ResultJSON (the JSON of
//     the CLI's --json and of the MCP tools), ParseDeck, the task statuses, and the import
//     format of conversations (ParseConversation, dec-import-format), which Client.Import sends
//     to the session import in pages.
//   - Gateway: the state behind the gateway view (dec-gateway-cli): own utterances with their
//     status, and whether the participant is paused. It is driven by the local log and, when the
//     ears run, by their local status.
//
// Files in the runtime directory (default $XDG_RUNTIME_DIR/agentic-office, mode 0700):
//
//	<session>/                      one directory per session, mode 0700
//	<session>/<participant>.jsonl   local session log, mode 0600 (if-local-log)
//	<session>/<participant>.tree-head.yaml  latest signed tree head over the local log
//	<session>/<participant>.join    join state: session ID and join command ID
//	<session>/<participant>.pid     PID of the running replicator
//	<session>/<participant>.replicator.log  output of a detached replicator
//
// Session and participant IDs that are not safe as file names are encoded (see FileName).
package coreapi

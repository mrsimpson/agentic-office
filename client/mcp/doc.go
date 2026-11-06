// Package mcp is the building block MCP server (bb-mcp in docs/architecture/05-building-blocks.arc42.md).
//
// It offers the features of the office CLI as MCP tools over stdio and provides if-mcp. Like
// the CLI, it is a thin adapter over the shared client core in client/participant
// (dec-cli-and-mcp). "office mcp" runs it (client/cli); an MCP client starts that command.
//
// Conventions:
//
//   - Tool names use "_" where the architecture writes "." (meeting_join for meeting.join),
//     because many MCP clients and model APIs allow only letters, digits, "_" and "-".
//   - A tool result is one JSON object, as text content and as structured content. It has the
//     same shape as the output of the matching CLI command with --json. Session commands return
//     {"event": …, "duplicate": …}, with the event in the form of its line in the local log.
//   - Errors set isError and have the form {"error":{"code":…,"message":…}}: the Connect code of
//     the server, or usage, no_key, no_session or error. Arguments that do not match the input
//     schema are usage errors, too.
//   - Session tools take an optional "session". Default: the --session flag or OFFICE_SESSION,
//     else the only session this participant joined from this machine.
//
// meeting_join runs client/replicator in this process (one per session), so the replication
// lives as long as the MCP process (dec-log-as-file): it stops when the client closes stdin,
// when the process ends or is killed, after session.ended, or after the participant's own
// participant.left. If another process (a replicator of "office meeting join") already holds
// the file, meeting_join reuses it and returns that PID.
//
// Not offered: key and admin commands (set up a key with the CLI), the gateway view and the ears
// (an agent has no microphone; humans use "office meeting join --mic"), and a long-poll tool
// (meeting.wait, an OPEN point of concept-read-file-write-tools). Agents read the log file.
package mcp

// Package mcp is the building block MCP server (bb-mcp in docs/architecture/05-building-blocks.arc42.md).
//
// It offers the features of the office CLI as MCP tools over stdio. It realizes if-mcp, whose
// contract is package tools: the instructions, the tools and their input types, and the form of
// results and errors. Like the CLI, it is a thin adapter over the client core (if-client-core,
// dec-cli-and-mcp). "office mcp" runs it (client/cli); an MCP client starts that command.
//
// meeting_join runs a log replicator (if-replication) in this process (one per session), so the replication
// lives as long as the MCP process (dec-log-as-file): it stops when the client closes stdin,
// when the process ends or is killed, after session.ended, or after the participant's own
// participant.left. If another process (a replicator of "office meeting join") already holds
// the file, meeting_join reuses it and returns that PID.
//
// Not offered: key and admin commands (set up a key with the CLI), the gateway view and the ears
// (an agent has no microphone; humans use "office meeting join --mic"), and a long-poll tool
// (meeting.wait, an OPEN point of concept-read-file-write-tools). Agents read the log file.
package mcp

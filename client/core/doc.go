// Package core is the client core (bb-client-core in docs/architecture/05-building-blocks.arc42.md).
// It implements the client core API (if-client-core, package coreapi in client/core/api): Dial
// returns a coreapi.Client. The office CLI, the MCP server, the ears and the agent runner are
// adapters on top of it (dec-cli-and-mcp); they use only package coreapi.
//
// The client holds authenticated Connect clients for the interfaces of the office hub. Join keeps
// its join command ID in the join state file of the runtime directory. DefaultSession and Join
// read the local session log (if-local-log) to find sessions that ended or that the participant
// left. Import sends a conversation to the session import (if-session-import) in pages, with a
// command ID per page.
package core

// Package mcp is the building block MCP server (bb-mcp in docs/architecture/05-building-blocks.arc42.md).
//
// It offers the features of the CLI as MCP tools over stdio. Provides if-mcp. Not implemented yet
// (#9). It is meant to be a thin adapter over client/participant, like the CLI, and to run
// client/replicator in its own process, so the replication lives as long as the MCP process.
package mcp

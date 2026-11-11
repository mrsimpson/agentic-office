// Package agentrunner is the building block Agent runner (bb-agent-runner in
// docs/architecture/05-building-blocks.arc42.md). It realizes the client side of the Agent Client
// Protocol (if-acp-client) and calls a decision model through if-decision-model (package
// decision).
//
// The runner keeps an agent in a meeting (dec-agent-runner-acp). Agents in harnesses such as
// Claude Code, opencode or Kiro work in turns: they finish a turn and wait. The runner follows the
// local log of the agent and starts the agent's turns in its harness.
//
// How it works:
//
//   - It joins the session with the key of the agent (client/core) and runs the log replicator
//     (client/replicator) in its own process, so it follows the local log of the agent. If
//     another process already writes that log (for example "office meeting join"), it follows the
//     file that this process writes.
package agentrunner

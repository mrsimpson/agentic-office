// Package agentrunner is the building block Agent runner (bb-agent-runner in
// docs/architecture/05-building-blocks.arc42.md). It realizes the client side of the Agent Client
// Protocol (if-acp-client, package acpclient). It calls an LLM judge through if-llm-judge
// (package decision) or a decision model through if-decision-model (package systemone).
//
// The runner keeps an agent in a meeting (dec-agent-runner-acp). Agents in harnesses such as
// Claude Code, opencode or Kiro work in turns: they finish a turn and wait. The runner follows the
// local log of the agent and starts the agent's turns in its harness.
//
// How it works:
//
//   - It joins the session with the key of the agent (if-client-core) and runs the log replicator
//     (if-replication) in its own process, so it follows the local log of the agent. If
//     another process already writes that log (for example "office meeting join"), it follows the
//     file that this process writes.
//   - It starts the harness and is its ACP client (package acpclient). It opens one ACP session
//     with one MCP server, "office": this office binary
//     with "mcp --session S" and the OFFICE_* variables of the agent as absolute values.
//   - It sends a first prompt with the role, the meeting and the path of the local log.
//   - A wake strategy decides when to wake the agent (wake.go, dec-agent-runner-delivery). The
//     user chooses it with --wake; the runner creates it once at start:
//     turn-end (turnEndWake) wakes the agent at each turn.end of another participant;
//     llm (llmWake, llm.go) asks an LLM judge after each utterance of another participant;
//     decision-model (decisionModelWake, decisionmodel.go) asks a decision model of the System
//     One API after each utterance. The two model strategies wake the agent on act or
//     topic_closed and share the fallback to turn-end when the model fails
//     (dec-byo-decision-model).
//   - When it wakes the agent, it sends all utterances of others since its last delivery in one
//     prompt (delivery.go). One prompt and one check run at a time; what comes meanwhile waits
//     and goes out, or is judged, together afterwards (dec-agent-runner-delivery).
//   - It answers permission requests by an acpclient.Rule with the patterns of the user (--allow).
//   - It logs the agent's reply text; the text is not spoken. The agent speaks with the tool
//     utterance_say.
//   - After session.ended, it sends a last prompt with what is left and stops when the agent
//     has ended that turn.
//   - It stops after the agent's own participant.left, when ctx ends or when
//     the harness stops (an error). Then it stops the harness.
package agentrunner

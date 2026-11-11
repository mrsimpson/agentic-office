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
//   - It starts the harness command in its own process group and is its ACP client over stdin
//     and stdout (github.com/coder/acp-go-sdk, protocol version 1). It offers no file system and
//     no terminal. It opens one ACP session with one MCP server, "office": this office binary
//     with "mcp --session S" and the OFFICE_* variables of the agent as absolute values.
//   - It sends a first prompt with the role, the meeting and the path of the local log.
//   - It answers permission requests by a permissionRule: by default the office tools only, and
//     never a tool of kind execute, edit, delete or move; with patterns of the user, exactly the
//     tools whose title matches. The title and kind come from the request, else from the
//     session/update of the same tool call.
//   - It logs the agent's reply text; the text is not spoken. The agent speaks with the tool
//     utterance_say.
//   - It stops after session.ended, after the agent's own participant.left, when ctx ends or when
//     the harness stops (an error). It closes the harness's stdin, then sends SIGTERM and at last
//     SIGKILL to its process group.
package agentrunner

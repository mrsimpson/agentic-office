// Package agentrunner is the building block Agent runner (bb-agent-runner in
// docs/architecture/05-building-blocks.arc42.md). It realizes the client side of the Agent Client
// Protocol (if-acp-client, package acpclient). It calls a forwarding judge through
// if-forwarding-judge (package forwardingjudge) or a decision model through if-decision-model
// (package systemone).
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
//   - Before it starts the harness, its skills injector (skills.go, dec-skills) writes the skills
//     that every agent needs (today office-memory, if-office-memory-skill) into .agent/skills/
//     of the working directory, where Kiro and opencode read them, and links
//     .claude/skills/<skill> to the same folder for Claude Code. It replaces only a folder with
//     its marker file; a folder of the same name from elsewhere stays, with a warning. In a Git
//     work tree, it adds what it wrote to the exclude file of the repository.
//   - It starts the harness and is its ACP client (package acpclient). It opens one ACP session
//     with one MCP server, "office": this office binary
//     with "mcp --session S" and the OFFICE_* variables of the agent as absolute values.
//   - It sends a first prompt with the role, the meeting, the session that everything from the
//     meeting must name, and the path of the local log. It does not mention the skills: the
//     harness offers them to the agent.
//   - A wake strategy decides when to wake the agent (wake.go, dec-agent-runner-delivery). The
//     runner knows only the interface wakeStrategy and its own types checkRequest and verdict
//     (check.go); each strategy maps them to the contract of its model. The user chooses the
//     strategy with --wake; the runner creates it once at start:
//     turn-end (turnEndWake) wakes the agent at each turn.end of another participant;
//     llm (forwardingJudgeWake, forwardingjudge.go) asks a forwarding judge after each
//     utterance of another participant; decision-model (decisionModelWake, decisionmodel.go)
//     asks a decision model of the System One API after each utterance. The two model
//     strategies wake the agent on act or topic_closed and share the fallback to turn-end when
//     the model fails (dec-byo-decision-model).
//   - When it wakes the agent, it sends all utterances of others since its last delivery in one
//     prompt (delivery.go). One prompt and one check run at a time; what comes meanwhile waits
//     and goes out, or is judged, together afterwards (dec-agent-runner-delivery).
//   - It answers permission requests by an acpclient.Rule with the patterns of the user (--allow).
//   - The agent speaks with the tool utterance_say; its reply text in the chat is not spoken.
//     If a turn ends with reply text and the agent neither spoke nor raised its hand (by the
//     titles of its tool calls and by its own events in the log), the runner logs a warning with
//     the text and asks the agent once, quoting the text, whether it was meant for the meeting.
//     It does not ask after that question nor after the last prompt.
//   - It reports the activity of the agent to the hub for the stage (if-session-activity,
//     dec-agent-activity, activity.go): working while a prompt runs, with the short name of the
//     tool it calls (the name of an office tool, else the kind of the tool, never the title or
//     the arguments); waiting when the turn has ended; and that a reply reached nobody. It
//     renews the report before it expires. A failing report is logged once and never stops it.
//   - After session.ended, it sends a last prompt with what is left and stops when the agent
//     has ended that turn.
//   - If the harness stops or fails a prompt (for example "Internal error" of its model service)
//     after a turn of the agent has ended well, it starts a new harness (runner.go, restart): it
//     opens a new ACP session and sends a first prompt that says the harness was restarted, so
//     that the agent reads the state of its documents before it changes them. It delivers again
//     the utterances that the failed prompt carried, except sentences that went off the record
//     meanwhile. It restarts at most three times within ten minutes, with a growing pause. A
//     harness that fails before any turn ended well is a setup error and ends the run at once.
//   - It stops after the agent's own participant.left, when ctx ends or when the harness stops
//     for good (an error, after the restarts). Then it stops the harness.
package agentrunner

# Solution Strategy

The hub is state and tools, the client adds senses, and neither is the brain. Agents and humans take part as
participants of the same kind. They read what happens from a locally replicated file and act
through CLI or MCP tools. Speech is transcribed at the edge. The office is present in the meeting
through a shared stage, not through an integration into the meeting tool.

| Quality goal | Approach |
|---|---|
| Confidentiality | Transcription at the edge; only text leaves the device |
| Performance | Staged events without partials; log as a locally replicated file; the agent filters what it reacts to |
| Resumability | Append-only log with cursor; resume from the last known event; idempotent commands |
| Usability | One participant model; everyone sees their own events; the stage shows who is captured |

```arc42
:::solution-strategy
id: strategy-architecture
title: Headless office with file-based participation and transcription at the edge
addresses: qg-confidentiality, qg-performance, qg-resumability, qg-usability
:::
```

## Bring your own agent

The agent makes every judgement about content. The hub stores, calculates, renders and checks
against rules. So any agent can connect, and the hub stays deterministic and testable (see
`dec-headless-byoa`). The client may contain models on the participant's side, for example the
speech model of the ears or a forwarding judge or a decision model that the user brings
(`dec-models-in-client`).

## Keep the agent in the meeting

Agents work in turns (`con-turn-based-agents`). The agent runner of the client follows the
local log for an agent and checks after each utterance whether to start the agent's next turn.
It talks to the agent harness over the Agent Client Protocol and hands it the office tools (see
`dec-agent-runner-acp`, `dec-wake-check-per-utterance`). The agent itself decides whether it is
addressed and how to act.

## Read from the file, write through tools

`join` returns the path to a locally replicated JSONL file. The agent reads it with standard tools
and sets its own pace with filters. Actions go through tools that answer at once. Every action then
shows up in the log as well (see `dec-log-as-file`, `concept-read-file-write-tools`).

## Resume instead of restart

The log is append-only, and every event has an ID. After a network drop, every client continues
from the last event it knows. Commands carry IDs that the client creates, so a repeated command
does not create a second event (see `concept-event-log`, `dec-idempotent-commands`).

## One core, two adapters

The same features are available through the CLI and through MCP. The CLI is not an add-on: agents
with a shell and humans both use it (see `dec-cli-and-mcp`).

## Transcription at the edge

Each human transcribes only themselves, locally, with a headset. Who is speaking follows from the
setup, without speaker recognition. Raw audio does not leave the device (see
`dec-local-single-ear`).

## Stage by screen share

We do not integrate meeting tools. Instead, the hub renders a stage as a web page, and someone
shares it in the meeting. The agent shows itself there: raised hands, minutes, artifacts (see
`dec-stage-screenshare`).

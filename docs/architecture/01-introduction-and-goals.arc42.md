# Introduction and Goals

## 1.1 Requirements Overview

Agentic Office is a **headless office**. It helps a company with administrative work, but its
hub is not "intelligent" itself. It keeps the team's state (decisions, minutes, notes, tasks, artifacts). It
offers deterministic tools (render slides, check business plans by calculation). And it opens
senses into the real world, starting with audio from meetings. The agent brings the thinking
("bring your own agent").

Agents and humans are equal **participants**. Both use the same CLI or the same MCP server. There is
one difference: humans can also switch on a microphone. Their speech is then transcribed locally.

| Use case | Summary |
|---|---|
| Join a meeting | A human or an agent joins a session and gets a local log file that is replicated continuously |
| Capture speech | Local Whisper transcribes only the device owner (headset) and sends text, never audio |
| Take minutes and raise a hand | An agent keeps the minutes, raises its hand on the stage and shows artifacts |
| Keep an agent in the meeting | The agent runner wakes an agent when the speaker changes, so people can address it in plain words |
| Show the stage | A web view of the hub is shared by screen share in any meeting tool |
| Create artifacts | Slides, plan checks, decisions and notes through CLI or MCP, without streaming |
| Track tasks | Create tasks in a meeting, assign them and change their status through CLI or MCP; for now they are drafts in the hub |
| Transparency | Every participant sees what was recorded and sent from them, and can pause or mark it off the record |

Explicitly **not** in the first stage: speech output (TTS), video, use with loudspeakers, meeting
rooms with several speakers at one microphone, and integration into meeting tools.

## 1.2 Quality Goals

The quality goals, in this order: **confidentiality** (raw audio never leaves the device, and only
people who consented are captured), **performance** (speech reaches the session log without
noticeable delay), **resumability** (after a network drop, everything continues without lost or
duplicated events) and **usability** (participants see what was captured from them).

See [10-quality-requirements.arc42.md](10-quality-requirements.arc42.md) for the complete
quality catalog with priorities and measurable scenarios.

## 1.3 Stakeholders

| Role | Contact | Expectations |
|---|---|---|
| Initiator / product owner | Repository owner (OPEN: name) | A sound overall design that the own team can use early |
| Human meeting participants | Team | Control over what is captured; no friction in the usual meeting tool |
| Agents (for example Claude Code, custom Agent SDK agents) | — | A simple, file-based interface; no special client features needed |
| Agent developers | OPEN | Stable CLI and MCP contracts, a machine-readable event format |
| Data protection (GDPR, § 201 German Criminal Code) | OPEN | Consent, data minimisation, deletion, traceability |
| Operations | OPEN | Easy self-hosting of the hub (OPEN: self-hosted or cloud?) |

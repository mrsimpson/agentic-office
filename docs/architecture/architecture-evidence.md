# Architecture Evidence

All facts come from the concept discussion on 2026-11-06 and the first review. There is no code
yet. "Maintainer" means the initiator decided it explicitly. "agent inference" means the agent
proposed it and it still needs confirmation.

| Source | Derived fact | Used in | Confidence | Open question / decision |
| --- | --- | --- | --- | --- |
| Maintainer, round 1 | Headless: the application only exposes APIs (MCP, CLI) and media connectors; the agent brings the intelligence | ch.2 con-no-intelligence, ch.9 dec-headless-byoa | high | — |
| Maintainer, round 2 | Connect Whisper ourselves; events per sentence; a pause triggers deeper processing | ch.8 concept-event-log, ch.9 dec-own-whisper | high | — |
| agent inference | Thresholds 300–700 ms / 1.5–2 s / > 5 s | ch.8 concept-event-log | low | OPEN: measure and set the values |
| Maintainer, round 3 | Several speakers are a problem; choose on purpose whom to listen to | ch.9 dec-local-single-ear, ch.11 risk-speaker-mode | high | — |
| Maintainer, rounds 4–5 | A local gateway transcribes and sends; the agent connects to the server; the stage is shown by screen share | ch.3, ch.5 bb-ears, bb-stage, ch.9 dec-stage-screenshare | high | — |
| Maintainer, round 6 | Text first, TTS later; the gateway is a CLI that shows what was recorded and sent | ch.9 dec-text-first, dec-gateway-cli | high | — |
| Maintainer, round 7 | Live interaction and tools are separate; tools through MCP and CLI | ch.9 dec-cli-and-mcp | high | — |
| Maintainer, round 8 | The agent starts taking part; a local socket replicates the log; the agent gets a file path and follows it | ch.5 bb-replicator, if-local-log, ch.9 dec-log-as-file | high | — |
| Maintainer, round 9 | The agent writes through tools; "listen" is the wrong word, use join | ch.8 concept-read-file-write-tools | high | — |
| Maintainer, round 10 | Humans and agents use the same CLI; humans are also transcribed | ch.8 concept-participant-model, ch.9 dec-unified-participant | high | — |
| Maintainer, review 1 | Headset required, no voice print, no echo cancellation; lifting it is issue #1 | ch.2 con-headset-only, ch.11 risk-speaker-mode | high | — |
| Maintainer, review 1 | No partials in the log | ch.9 dec-no-partials-in-log | high | — |
| Maintainer, review 1 | Quality goals are ilities, not features; order: confidentiality, performance, usability | ch.1, ch.10 | high | — |
| Maintainer, review 1 | Latency: end of transcription to event in the log < 200 ms | ch.10 qs-transcript-to-log | high | OPEN: p95 or maximum; server log or local replica |
| Maintainer, review 2 | Resumability after network drops is a high-priority goal, before usability | ch.1, ch.10 qg-resumability | high | — |
| Maintainer, review 2 | All architecture documents and artifacts in English, plain language per ISO 24495-1 | AGENTS.md | high | — |
| agent inference | Resume by cursor, idempotent commands with client IDs, ears queue utterances while offline | ch.5, ch.6 rs-network-drop, ch.8, ch.9 dec-idempotent-commands | medium | OPEN: confirm; drop duration and catch-up metric |
| agent inference | All four goals have priority high; the order is stated in the text (arc42 expects 3–5 high goals) | ch.10 | medium | — |
| agent inference | Protocols HTTPS/JSON and WebSocket | ch.5 if-session-commands, if-session-stream | low | OPEN: evaluate a typed alternative with streaming (for example Connect RPC) |
| agent inference | Data protection risks (retraction, consent) | ch.11 | medium | OPEN: clarify with data protection |
| — | Server operation (self-hosted or cloud), persistence, programming language | ch.7 | — | OPEN: not discussed yet |

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
| agent inference, web research 2026-11-06 | Connect RPC for commands and stream; maturity: CNCF sandbox since 2024-04, connect-go v2 stable, connect-python 0.11 | ch.5, ch.9 dec-connect-rpc, ch.11 risk-connect-maturity | medium | OPEN: confirm |
| buf.build/blog/connect-rust-joins-the-connect-project (2026-08-26) | connect-rust is official, 0.9.x pre-1.0, full conformance, all four RPC types, axum/hyper/tower, in production at Anthropic | ch.9 dec-languages, ch.11 risk-connect-maturity | high | — |
| Maintainer, review 3 | Everything in Go, one toolchain in the monorepo | ch.5 technology, ch.9 dec-languages, AGENTS.md | high | — |
| agent inference | Stage rendered by Go templates with server-sent events, no TypeScript | ch.5 bb-stage, ch.9 dec-languages | medium | OPEN: confirm |
| Maintainer, review 4 | Local key pair, public key sent to the server | ch.8 concept-identity, ch.9 dec-auth-keypair | high | — |
| Maintainer, review 4 | First contact out of scope: we meet the person; enrollment flow as issue #2 | ch.8 concept-identity | high | — |
| Maintainer, review 4 | Every agent is its own actor with its own key; acting on behalf is out of scope | ch.8, ch.9 dec-auth-keypair, ch.12 term-actor | high | — |
| agent inference | Challenge and response, short-lived token bound to the key, read-only stage link, several keys per participant, TLS | ch.5 bb-identity, ch.6 rs-authenticate, ch.8 concept-identity | medium | OPEN: confirm |
| agent inference | Data protection risks (retraction, consent) | ch.11 | medium | OPEN: clarify with data protection |
| — | Server operation (self-hosted or cloud), persistence | ch.7 | — | OPEN: not discussed yet |

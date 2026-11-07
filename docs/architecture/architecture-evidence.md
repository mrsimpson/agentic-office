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
| agent inference | Resume by cursor, idempotent commands with client IDs, ears queue utterances while offline | ch.5, ch.6 rs-network-drop, ch.8, ch.9 dec-idempotent-commands | high | accepted in review 5; OPEN: drop duration and catch-up metric |
| agent inference | All four goals have priority high; the order is stated in the text (arc42 expects 3–5 high goals) | ch.10 | medium | — |
| agent inference, web research 2026-11-06 | Connect RPC for commands and stream; maturity: CNCF sandbox since 2024-04, connect-go v2 stable, connect-python 0.11 | ch.5, ch.9 dec-connect-rpc, ch.11 risk-connect-maturity | high | — (accepted in review 5) |
| buf.build/blog/connect-rust-joins-the-connect-project (2026-08-26) | connect-rust is official, 0.9.x pre-1.0, full conformance, all four RPC types, axum/hyper/tower, in production at Anthropic | ch.9 dec-languages, ch.11 risk-connect-maturity | high | — |
| Maintainer, review 3 | Everything in Go, one toolchain in the monorepo | ch.5 technology, ch.9 dec-languages, AGENTS.md | high | — |
| agent inference | Stage rendered by Go templates with server-sent events, no TypeScript | ch.5 bb-stage, ch.9 dec-languages | medium | OPEN: confirm |
| Maintainer, review 4 | Local key pair, public key sent to the server | ch.8 concept-identity, ch.9 dec-auth-keypair | high | — |
| Maintainer, review 4 | First contact out of scope: we meet the person; enrollment flow as issue #2 | ch.8 concept-identity | high | — |
| Maintainer, review 4 | Every agent is its own actor with its own key; acting on behalf is out of scope | ch.8, ch.9 dec-auth-keypair, ch.12 term-actor | high | — |
| Maintainer, review 4 | Read-only link for the stage; several keys per participant, an admin can remove a key | ch.8 concept-identity, ch.9 dec-auth-keypair | high | — |
| agent inference | Challenge and response, short-lived token bound to the key, TLS | ch.5 bb-identity, ch.6 rs-authenticate, ch.8 concept-identity | medium | OPEN: confirm |
| agent inference | Data protection risks (retraction, consent) | ch.11 | medium | OPEN: clarify with data protection |
| — | Server operation (self-hosted or cloud), persistence | ch.7 | — | OPEN: not discussed yet |
| Spike spike/e2e (f5adb62), e2e tests | Resume and idempotency: 300 utterances, 30 repeated commands, 3 replicator kills: 0 lost, 0 duplicate events | ch.9 dec-idempotent-commands | high | — |
| Spike spike/e2e, latency test (localhost) | End of transcription to local log of another participant: p50 0.21 ms, p95 0.38 ms, max 1.1 ms | ch.10 qs-transcript-to-log | high | not measured over a real network with TLS |
| Spike spike/e2e | Whisper small on CPU about 0.9 s per sentence; large-v3-turbo 3–3.7 s; end of speech to log about 1.4 s; audio loop jitter p99 1.1 ms | ch.10 qs-speech-to-log, ch.11 risk-whisper-latency | high | — |
| Maintainer, review 5 | Target end of speech to log: p95 ≤ 1.5 s on the reference hardware | ch.10 qs-speech-to-log | high | — |
| Spike spike/e2e | Local log per session and participant in a private user directory; event type in dotted form and ID as a number in each line; rejoin keeps the participant | ch.5 bb-replicator, if-local-log, ch.8, ch.9 dec-idempotent-commands | high | — |
| Spike spike/e2e | whisper.cpp brings its own voice activity detection | ch.5 bb-ears | high | — |
| Maintainer, review 5 | Test strategy: WAV in automated tests, microphone tested manually with a headset, no virtual microphone | ch.8 concept-test-strategy, ch.10 qs-manual-headset | high | — |
| Maintainer, review 5 | No implementation details in the architecture | AGENTS.md | high | — |
| Maintainer, review 6 | No unit tests; black-box contract tests per building block | ch.2 con-no-unit-tests, con-contract-tests, ch.8 concept-test-strategy, ch.9 dec-contract-tests, AGENTS.md | high | — |
| Maintainer, review 7 | Exactly one path per building block; a test allows dependencies only through the paths of required interfaces | ch.5 paths, ch.8 concept-module-boundaries, ch.9 dec-architecture-test | high | — |
| Maintainer, review 7 | No common package; the types of an interface belong to its provider; a parent may wire its children; tests are actors; interfaces for actors only are marked by having only actors as consumers; unused requires fail | ch.8 concept-module-boundaries | high | — |
| Maintainer, review 7 | The architecture test reads the model from the arc42 CLI as JSON | ch.9 dec-architecture-test | high | — |
| Maintainer, review 7 | End-to-end tests never check performance; separate performance tag | ch.8 concept-test-strategy, ch.9 dec-performance-tests | high | — |
| Code, architecture test (#26, #27) | The client core is its own building block; the replicator offers replication control; the token check is its own in-process interface with its own types | ch.5 bb-client-core, if-client-core, if-replication, if-token-check | high | — |
| Code, architecture test (#26, #27) | CLI and MCP server use the server interfaces directly, not only through the client core | ch.5 requires of bb-cli and bb-mcp | high | OPEN: should the adapters use only the client core? |
| ch.5 if-session-commands, if-session-stream | H020 ignored: commands and stream are two contracts of one Connect service and share its package | ch.5 | high | — |
| Maintainer, review 8 | Interfaces across a boundary belong to the parent building block (black box) and are realized by a named child (resolves arc42 hint H004) | ch.3, ch.5, ch.8 concept-module-boundaries | high | — |
| Maintainer, review 9 | Headset decision mitigates risk-speaker-mode; dec-text-first serves usability | ch.9 | high | — |
| Maintainer, review 9 | An agent polls the events after a cursor when something is missing; the order must be conflict-free | ch.9 dec-events-from-cursor | high | — |
| agent inference | One writer per session log gives gap-free IDs; no predecessor ID needed | ch.8, ch.9 dec-events-from-cursor | medium | — (epoch replaced by dec-restore-from-peers) |
| Maintainer, review 9 | No retraction: what is said stays said; later marking as off the record | ch.6, ch.8, ch.9 dec-off-the-record | high | — |
| Maintainer, review 9 | Joining with --mic is consent to storage, also of the raw form; censorship only by later classification that suppresses the message in the interfaces | ch.8, ch.9 dec-consent-by-mic | high | OPEN: retention period, data protection review |
| Maintainer, review 10 | Everyone may mark only their own sentences off the record; own command, owner checked by the server; moderation out of scope (#31) | ch.6, ch.9 dec-off-the-record | high | — |
| Maintainer, review 11 | Session log stored at every peer; the server stays the only orderer | ch.7, ch.8, ch.9 dec-peer-storage | high | — |
| Maintainer, review 11 | Every peer publishes; target addressed by session ID; a lower cursor is rejected; storage is a Git repository | ch.9 dec-publish-session | high | OPEN: which repository; journal and artifacts too? |
| agent inference | An archive building block on the server checks publications and holds the Git credentials | ch.9 dec-publish-session | medium | OPEN: confirm |
| Maintainer, review 11 | Merkle tree; tree heads signed by the server | ch.9 dec-merkle-log | high | — |
| Maintainer, review 11 | Publication keeps the original and the off-the-record mark; reading gives placeholders | ch.9 dec-off-the-record | high | — |
| Maintainer, review 11 | Joining accepts that others keep and publish a copy | ch.9 dec-consent-by-mic | high | — |
| Maintainer, review 11 | The next peer in joining order restores a lost log; no epoch | ch.9 dec-restore-from-peers | high | — |
| Maintainer, review 12 | The hub is the gateway of the archive and brings the semantics; the external Git repository is pure storage; peers do not publish | ch.7, ch.8, ch.9 dec-session-persistence (replaces dec-peer-storage and dec-publish-session) | high | — |
| Maintainer, review 12 | Write interval on the hub is configurable; more often is better; writes must not cause a queue | ch.9 dec-session-persistence, ch.10 qs-archive-no-backpressure, ch.11 risk-archive-latency | high | OPEN: slow commit time for the scenario |
| agent inference | Writes in the background, at most one in progress, coalescing later events | ch.9 dec-session-persistence | medium | — |
| Maintainer, review 12 | Keep a separately deployed hub; call it hub instead of server | ch.5, ch.7, ch.12 | high | — |
| agent inference | The hub keeps identity and its signing key permanently | ch.7 | medium | — |

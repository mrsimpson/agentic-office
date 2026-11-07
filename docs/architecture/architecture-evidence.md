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
| Maintainer, review 13 | First version: commit into a local Git repository in the hub; the push is mocked | ch.7, ch.9 dec-yaml-git-storage | high | — |
| Maintainer, review 13 | Tasks belong to the task tools of the team; at first a local YAML draft, later synced and replaced | ch.9 dec-yaml-git-storage | high | — |
| Maintainer, review 13 | Everything as YAML in the Git repository, local and predictably fast | ch.9 dec-yaml-git-storage | high | — |
| Maintainer, review 13 | A dedicated interface for every type; notes, minutes and decisions are different things | ch.9 dec-records-per-type | high | — |
| Maintainer, review 14 | Building blocks: records part, tasks part, artifact tools, archive part; session log with one line per event | ch.9 dec-records-per-type, dec-yaml-git-storage | high | — |
| Issue #33, agent decision | The session commands return the signed tree head, consistency and inclusion proofs and the hub key; the verification code belongs to the session interface | ch.5 if-session-commands | medium | — |
| Issue #33, agent decision | Leaves in RFC 9162 style; the leaf of an event is its original form in the log; a placeholder carries the leaf hash of the original | ch.8 concept-event-log, ch.9 dec-merkle-log | medium | OPEN: a client with an older contract may not reproduce the leaf of an event with new fields |
| Issue #33, agent decision | session.ended carries the signed head of the log before it; the head that includes session.ended is available on request | ch.8 concept-event-log | medium | — |
| Issue #33, agent decision | The hub key that signs tree heads is its own key, kept in the data directory of the hub, and is fetched through the session commands | ch.8 concept-event-log | medium | OPEN: how clients pin the hub key (today they trust the key they fetch over TLS) |
| Issue #35, maintainer | The archive is a part of the hub with its own in-process interface that the meeting session requires; it commits into a local Git repository on the hub host; the push is mocked (#43) | ch.5 bb-archive, if-archive-storage, ch.6 rs-archive-session, ch.7 dn-office-hub | high | OPEN: the real remote (#43) |
| Issue #35, maintainer | Sessions are written at a configurable interval and at session.ended, in the background, at most one write at a time; every write carries the signed tree head; the stored log keeps originals and off-the-record marks | ch.5 bb-session-service, ch.6 rs-archive-session, ch.8 concept-event-log | high | — |
| Issue #35, agent decision | The line form of an event belongs to the session stream interface, not to the client, so the archive and the local log share it without the hub depending on client code | ch.5 if-session-stream, if-local-log | medium | — |
| Issue #35, agent decision | The archive knows no record formats: a caller writes a set of files as one commit; one write of the hub is one commit for all changed sessions | ch.5 bb-archive, if-archive-storage | medium | — |
| Issue #35, agent decision | The tree head is stored as YAML next to the log, in the same commit; it covers exactly the stored log | ch.5 bb-session-service | medium | — |
| Issue #35, test with a git wrapper (commits of 5 s, interval 500 ms, 12 s of speech) | 4 commits instead of 24 intervals, none overlapping; command and stream latency max about 4 ms | ch.10 qs-archive-no-backpressure | high | measured on the development host, not the reference host |
| Issue #35, agent decision | The hub host needs the Git command line tool | ch.7 dn-office-hub | medium | — |
| Issue #37, maintainer (dec-records-per-type) | One records part of the hub realizes three interfaces: decisions, minutes and notes; it replaces the journal; tasks are not part of it | ch.5 bb-records, if-decision-api, if-minutes-api, if-note-api, ch.7 dn-office-hub, ch.12 term-record | high | — |
| Issue #37, agent decision | Every record is one YAML file in the archive; every new record is one commit; the command returns after the commit | ch.5 bb-records, ch.6 rs-session-ends | medium | — |
| Issue #37, agent decision | The records part reads its records back from the current commit of the archive at start; the archive storage interface gets a read of a directory for this | ch.5 bb-records, bb-archive, if-archive-storage | medium | — |
| Issue #37, agent decision | Clients do not write minutes through the minutes interface: running minutes go through the session commands, and the meeting session stores the last version at the end | ch.5 if-minutes-api, ch.6 rs-session-ends | medium | OPEN: a separate confirm step for the minutes (#22) |
| Issue #37, agent decision | The meeting session requires the minutes interface instead of the journal and stores the minutes in-process, before session.ended | ch.5 bb-session-service, ch.6 rs-session-ends | medium | — |
| Issue #37, agent decision | The CLI and the MCP server reach decisions, minutes and notes only through the client core, so only the client core requires those interfaces | ch.5 bb-client-core, bb-cli, bb-mcp | medium | — |
| Issue #38, maintainer (dec-records-per-type, dec-yaml-git-storage) | Tasks are their own part of the hub with their own interface; for now local YAML drafts in the archive; later synced with the team's task tool, and the synced task replaces the draft | ch.5 bb-tasks, if-task-api, ch.7 dn-office-hub, ch.12 term-task | high | OPEN: how the sync works (#40) |
| Issue #38, agent decision | A task is named by an opaque reference with a scheme (local:42 today, an external scheme after the sync), carries its source, and the list pages with an opaque token, so the sync does not change the contract | ch.5 if-task-api, ch.12 term-task-reference | medium | — |
| Issue #38, agent decision | One YAML file per task; creating is one commit, every change rewrites the file in a new commit; the command returns after the commit; tasks are read back at start | ch.5 bb-tasks, ch.6 rs-task-lifecycle | medium | — |
| Issue #38, agent decision | Statuses open, in progress, done and cancelled; any participant may change any task; assignee and session are not checked | ch.5 if-task-api, ch.12 term-task | medium | OPEN: rights by role (#14, #15, #22) |
| Issue #38, agent decision | Task writes are synchronous like the records and share the wait behind a session write of the archive | ch.5 bb-tasks | medium | OPEN: #45 |
| Issue #38, agent decision | Only the client core requires the task interface; the CLI and the MCP server reach tasks through it | ch.5 bb-client-core | medium | — |
| Issue #39, maintainer (dec-records-per-type) | Slides get their own interface and their own part of the hub; the general artifact interface and the artifact tools are gone; "artifact" stays only the general word for what the stage shows by an ID with a type in front | ch.5 if-slides-api, bb-slides, bb-stage, ch.6 rs-agent-shows-artifact, ch.7 dn-office-hub, ch.9 dec-records-per-type (block name only), ch.12 term-artifact, term-slide-deck | high | — |
| Issue #39, agent decision | Every deck is two files in one commit of the archive: the input as YAML and the rendered output as HTML; the command returns after the commit; decks are read back at start | ch.5 bb-slides, ch.6 rs-agent-shows-artifact | medium | — |
| Issue #39, agent decision | The output is stored and not rendered again on read: a deck stays what was shown even if a later hub renders differently, and the archive can be read without the hub; rendering the stored input again gives the same output | ch.5 bb-slides | medium | OPEN: whether a later hub should render old decks again |
| Issue #39, agent decision | The stage requires the slides interface and chooses it by the type in front of the artifact ID; an ID of another type gives a note | ch.5 bb-stage, if-slides-api | medium | — |
| Issue #39, agent decision | The check of business plans is a type of its own with its own interface when it is built; it is no longer part of a general artifact block | ch.5 bb-slides | medium | OPEN: #3 |
| Maintainer, review 15 | No backward compatibility before go-live: the locallog forwarder, the journal.jsonl note and the journal wording are removed | AGENTS.md, ch.5, ch.9 | high | — |
| ch.5 if-local-log, if-replication | H020 ignored: the local log (file for actors) and replication control (in-process) are realized by the replicator and share its path | ch.5 | high | — |

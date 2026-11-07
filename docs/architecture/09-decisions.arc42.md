# Architecture Decisions

All decisions come from the concept discussion on 2026-11-06 and the first review. Decisions the
maintainer made explicitly are `accepted`. Proposals that still need confirmation are `proposed`.

## Headless, no intelligence in the application

**Context:** Agents improve fast, and every team uses different ones. **Decision:** The application
only exposes APIs (MCP, CLI) and media connectors. The agent makes the judgements.
**Consequences:** The application stays deterministic and testable. Features that need judgement
do not belong in the application.

```arc42
:::decision
id: dec-headless-byoa
title: Headless office, bring your own agent
status: accepted
date: 2026-11-06
addresses: con-no-intelligence
:::
```

## Own speech-to-text pipeline with Whisper

**Context:** No transcribed stream is available. **Decision:** We connect Whisper ourselves.
**Consequences:** We decide how fine-grained events are and when they are sent (end of sentence,
change of speaker). This allows the agent to process in stages. **Rejected:** transcription by the
meeting tool, because it is not available and cannot be controlled.

```arc42
:::decision
id: dec-own-whisper
title: Own speech-to-text pipeline with Whisper and staged events
status: accepted
date: 2026-11-06
addresses: qg-performance
:::
```

## Local gateway, single ear with a headset

**Context:** Whisper does not tell speakers apart. Loudspeakers and rooms cause echo and mixed
signals. **Decision:** Each human transcribes only themselves, locally, with a headset in the first
stage. **Consequences:** Who is speaking follows from the setup. Raw audio stays local.
Loudspeakers and rooms are not covered yet. **Rejected:** central speaker recognition (pyannote) on
a mixed signal.

```arc42
:::decision
id: dec-local-single-ear
title: Local transcription per participant (single ear, headset)
status: accepted
date: 2026-11-06
addresses: con-privacy-law, con-headset-only, qg-confidentiality, risk-speaker-mode
:::
```

## Stage by screen share instead of a meeting tool integration

**Context:** Bots for Zoom, Teams or Meet are costly and tied to one platform. **Decision:** The
server renders a stage as a web page that is shown by screen share. **Consequences:** It works with
any tool. The agent's presence is visual. People without a client still see what happens.

```arc42
:::decision
id: dec-stage-screenshare
title: Stage by screen share
status: accepted
date: 2026-11-06
addresses: con-any-meeting-tool, qg-usability
:::
```

## Text first, speech output later

**Decision:** At first, agents contribute only as text on the stage. **Consequences:** The humans
keep control of the conversation: an agent raises its hand visibly, and the moderator gives the
floor. Speech output can be added later through the audio of the
screen share, without changing the architecture. The person who shares will then need echo
cancellation.

```arc42
:::decision
id: dec-text-first
title: Text first, speech output later
status: accepted
date: 2026-11-06
addresses: qg-usability
:::
```

## Gateway as a CLI

**Decision:** At first, the gateway is a CLI. It shows what was recorded and sent, and it lets the
user pause and mark a sentence off the record. **Consequences:** There is no separate user interface. The display is a
filtered view of the local log.

```arc42
:::decision
id: dec-gateway-cli
title: Gateway as a CLI
status: accepted
date: 2026-11-06
addresses: qg-usability
:::
```

## Session log as a local file

**Context:** Agents work in turns, and MCP push rarely reaches the model. **Decision:** `join` starts
a local replicator that copies the log over a socket. It returns a file path at once, and the agent
follows that file. **Consequences:** The agent sets its pace with filters, the history is
searchable, and nothing depends on client features. It needs a shared file system.
**Rejected:** MCP notifications as the main channel; long polling as the main channel.

```arc42
:::decision
id: dec-log-as-file
title: Session log as a locally replicated file
status: accepted
date: 2026-11-06
addresses: con-turn-based-agents, qg-performance, qg-resumability
:::
```

## One participant model

**Decision:** Humans and agents are participants with the same tools. For humans with a microphone,
speech is also transcribed. **Consequences:** The gateway is `join --mic`. The whole system can be
tested without any agent.

```arc42
:::decision
id: dec-unified-participant
title: Humans and agents as participants of the same kind
status: accepted
date: 2026-11-06
addresses: qg-usability
:::
```

## CLI and MCP as equals

**Decision:** All features are available through the CLI and through MCP, built on one shared core.
This holds for the tools and for the live layer. **Consequences:** Two thin adapters. The CLI is not
an add-on.

```arc42
:::decision
id: dec-cli-and-mcp
title: CLI and MCP from one shared core
status: accepted
date: 2026-11-06
addresses: con-turn-based-agents
:::
```

## No partials in the log

**Decision:** Partial transcripts (`partial`) are not replicated. Only final utterances and signals
are. **Consequences:** Less noise, a smaller file, a shorter path into the log. Whoever needs
partials can get them later through a separate option.

```arc42
:::decision
id: dec-no-partials-in-log
title: No partials in the session log
status: accepted
date: 2026-11-06
addresses: qg-performance
:::
```

## Idempotent commands with client IDs

**Context:** After a network drop, a client does not know if its last command arrived.
**Decision:** Every command carries an ID that the client creates. The IDs are unique and sortable
by time. The server stores the ID with the event. If the same ID arrives again, the server returns
the existing event and creates no new one. This also applies to `join`: the client keeps its join
command ID, so a restarted client stays the same participant. **Consequences:** Clients can simply
repeat commands after a drop. The server must keep the IDs at least for the life of the session.
The spike confirmed this: 0 lost and 0 duplicate events while the replicator was killed three
times.

```arc42
:::decision
id: dec-idempotent-commands
title: Idempotent commands with client-generated IDs
status: accepted
date: 2026-11-06
addresses: qg-resumability
:::
```

## Connect RPC for commands and stream

**Context:** We want one typed contract for synchronous commands and for the session stream.
**Decision:** Define both interfaces in Protobuf and serve them with Connect RPC: unary calls for
commands, server streaming for the session stream. The lines of the local JSONL file use the
canonical JSON form of the same event messages. **Consequences:** One schema for server, clients
and file format. Connect also speaks gRPC and gRPC-Web, and its own protocol is plain HTTP with
JSON, so other clients can join without a special library. Maturity differs by language (see
`risk-connect-maturity`); with Go we use the stable connect-go. **Rejected:** plain gRPC (needs
grpc-web and a proxy in the browser); tRPC (TypeScript only); WebSocket with JSON Schema (typed only
by documentation).

```arc42
:::decision
id: dec-connect-rpc
title: Connect RPC with Protobuf for session commands and stream
status: accepted
date: 2026-11-06
addresses: qg-performance, qg-resumability, risk-connect-maturity
:::
```

## Go for everything, one monorepo

**Context:** We want one toolchain in one monorepo. The CLI should be easy to install. The ears need
local Whisper. **Decision:** All parts are written in **Go**: server, CLI, MCP server, log
replicator, ears and stage. They live in one monorepo with one shared core. The stage is rendered on
the server with Go templates and gets live updates through server-sent events. It needs no
TypeScript and no frontend build. **Reasons:** connect-go is stable (v2); there is an official MCP
SDK for Go; Go builds one static binary for the CLI; whisper.cpp has Go bindings, so the ears need
no Python. **Consequences:** One language, one build, shared types between all parts. The ears use
whisper.cpp, so building the ears also needs a C/C++ compiler. Rich interaction in the
browser would need a new decision. **Considered:** Rust (official connect-rust since 2026-08, but
still 0.x; strong for real-time audio, slower development); Python for the ears (connect-python not
stable); TypeScript for the stage (second toolchain).

```arc42
:::decision
id: dec-languages
title: Go for all parts in one monorepo
status: accepted
date: 2026-11-06
addresses: risk-connect-maturity, risk-whisper-latency, qg-performance
:::
```

## Local key pairs, one actor per agent

**Context:** Participants and agents must prove who they are. We want no secrets stored on the
server. **Decision:** Every participant, including every agent, has its own Ed25519 key pair,
created locally. Only the public key is sent to the server. In the first stage, keys are registered
by hand at first contact. Agents act as themselves, not on behalf of a human. The stage is opened
with a read-only link. A participant can have several keys (several devices), and an admin can
remove a key. **Proposed details:** challenge and response with a short-lived access token bound
to the key. **Consequences:** No shared secrets; agents
need only a key file. Enrollment needs a manual step for now (issue #2). **Considered:** OIDC with
an existing identity provider (standard, but heavy for a small team); mTLS with client certificates
(strong, but certificate management and awkward in the browser); static API tokens (secret on the
server, no binding to a device); passkeys (good for humans in the browser, not for CLI and agents).

```arc42
:::decision
id: dec-auth-keypair
title: Local Ed25519 key pairs, one actor per agent
status: accepted
date: 2026-11-06
addresses: qg-confidentiality, con-privacy-law
:::
```

## Contract tests instead of unit tests

**Context:** The architecture is still changing. Tests should survive changes inside a building
block. **Decision:** We write no unit tests. Each building block gets black-box contract tests
against its provided interfaces. End-to-end tests run real processes across several blocks.
**Consequences:** The interfaces in chapter 5 must be precise enough to test against. Tests stay
valid when the inside of a block changes. Errors deep inside a block are found through its
interfaces, which can make them harder to locate. **Rejected:** unit tests per function.

```arc42
:::decision
id: dec-contract-tests
title: Black-box contract tests instead of unit tests
status: accepted
date: 2026-11-06
addresses: con-no-unit-tests, con-contract-tests
:::
```

## Architecture test against the arc42 model

**Context:** Rules about module boundaries erode when nothing checks them. **Decision:** A test
checks the code against the building block view on every test run (`concept-module-boundaries`).
It reads the model from the arc42 tooling itself, not with an own parser, and the dependencies of
the code from the Go toolchain. It fails on code without a building block, on a dependency
between building blocks that does not go through a required interface, on a test that reaches
into a building block, and on a required interface that is never used. A small workspace with
deliberate violations proves that each rule fails when it should. **Consequences:** Every change
of a dependency needs a change of the model first. The tests need the arc42 tooling and fail
without it; they do not skip. **Rejected:** an own parser of the chapter files (it would drift
from the arc42 syntax); a shared package for common types (it becomes the place where boundaries
erode).

```arc42
:::decision
id: dec-architecture-test
title: The code is tested against the building block view
status: accepted
date: 2026-11-07
addresses: con-contract-tests
:::
```

## Performance tests separate from end-to-end tests

**Context:** A timing criterion made the end-to-end tests fail on a slower host, although every
function worked. **Decision:** End-to-end tests check behaviour only and never fail on timing.
The timing criteria of the quality scenarios run as separate performance tests on the reference
host. **Consequences:** The end-to-end tests run on every host. Performance needs its own run and
its own reference host.

```arc42
:::decision
id: dec-performance-tests
title: Performance tests separate from end-to-end tests
status: accepted
date: 2026-11-07
addresses: qg-performance
:::
```

## Events after a cursor

**Context:** A participant may miss events (a gap in its copy), and some agents have no file
system to follow. **Decision:** The session stream also answers a one-time request: "give me the
events after cursor N". The server is the only writer of a session log and gives every event an
ID that grows by one, so the order is free of conflicts and a gap is visible from the IDs alone.
A participant polls this request when it finds a gap, or instead of a local file. No event needs
the ID of its predecessor. **Consequences:** Agents without a file system can take part by
polling. A suppressed utterance must keep its ID (`dec-off-the-record`). If the server loses a
log, a peer restores it, so cursors stay valid (`dec-restore-from-peers`). **Rejected:** a long-poll tool only for MCP; a predecessor ID in every event (not needed with a
single writer).

```arc42
:::decision
id: dec-events-from-cursor
title: Events after a cursor, with gap-free IDs from one writer
status: accepted
date: 2026-11-07
addresses: risk-no-filesystem, qg-resumability
:::
```

## Off the record instead of retraction

**Context:** A spoken sentence has been heard by everyone in the meeting, and agents have already
read it. A retraction pretends that it was never said. **Decision:** What is said stays said.
Utterances are never retracted or removed. A participant can mark their own sentence off the
record afterwards. This is its own command; the server checks that the caller is the speaker. The
mark is a new event. The interfaces then suppress the text: they deliver a placeholder with the
same ID, the stage hides it, and it does not go into minutes or the journal. Agents that already
read it must respect the mark. The raw log keeps the original. Marking the sentences of others
(moderation) is out of scope for the first stage (issue #31). **Consequences:** The log stays
complete and honest. Suppression works for every reader that uses the interfaces, but not for
what an agent already holds in its context. The published session keeps the original and the
mark; reading it from the archive also gives the placeholder (`dec-publish-session`).
**Rejected:** retraction; a hold period before
publishing (it adds seconds of latency); a general classification command (not needed yet).

```arc42
:::decision
id: dec-off-the-record
title: Own sentences can be marked off the record; no retraction
status: accepted
date: 2026-11-07
addresses: risk-retraction-leak, qg-confidentiality
:::
```

## Consent by joining with the microphone

**Context:** Capture and storage of speech need consent (GDPR, § 201 German Criminal Code).
**Decision:** Joining with `--mic` is the consent to capture and to storage, also of the raw
transcript. Only participants who join with the microphone are transcribed. Joining a session at
all is the consent that every other participant keeps a copy of the session and may publish it
(`dec-peer-storage`, `dec-publish-session`). The stage shows the
capture status to everyone. Censorship is possible only by marking a sentence off the record later
(`dec-off-the-record`). **Consequences:** No extra step in the meeting. Raw audio still never
leaves the device. OPEN: a data protection review before the first real use, and how long raw
transcripts are kept.

```arc42
:::decision
id: dec-consent-by-mic
title: Joining with the microphone is the consent to storage
status: accepted
date: 2026-11-07
addresses: risk-consent, con-privacy-law, qg-confidentiality
:::
```

## The session log is stored by the peers

**Context:** The server keeps session logs only in memory, and every participant already holds a
complete copy. Speech is personal data. **Decision:** The participants (peers) store the session
log, each one a full copy. The server stays the only writer that orders the events and gives
them their IDs (`dec-events-from-cursor`); it keeps a log only while the session runs. Storing
is distributed, ordering is not. **Consequences:** No database for sessions on the server. The
data stays with the people who took part. A session survives only through its peers and its
publication. **Rejected:** fully peer-to-peer ordering without a server (it needs conflict-free
data types or logical clocks and breaks gap-free cursors).

```arc42
:::decision
id: dec-peer-storage
title: Peers store the session log; the server only orders it
status: accepted
date: 2026-11-07
addresses: qg-confidentiality, qg-resumability
:::
```

## The session log is a Merkle tree signed by the server

**Context:** Peers publish their copies and restore the server. A peer could change its copy.
**Decision:** The events of a session are the leaves of a Merkle tree, as in Certificate
Transparency. The server signs the tree head (root hash and size), at least in `session.ended`.
Anyone can check a copy against a signed tree head, and a consistency proof shows that a longer
log extends a shorter one and is not a fork. The leaf of an utterance that is off the record keeps
its hash, so a copy with a placeholder still verifies. **Consequences:** Copies and publications
cannot be forged without the server key. The server key needs protection. **Rejected:** a plain
hash chain (it cannot prove that one log extends another without the whole log).

```arc42
:::decision
id: dec-merkle-log
title: Merkle tree with tree heads signed by the server
status: accepted
date: 2026-11-07
addresses: risk-forged-copy, qg-confidentiality
:::
```

## Every peer publishes the session to a Git repository

**Context:** A session must outlive the meeting. Choosing one publisher is fragile: it may have
left or be offline. **Decision:** At the end of a session, every peer with a copy publishes it.
The target is addressed by the session ID, so all peers publish to the same place. A publication
with a lower cursor than the stored one is rejected; an equal or longer one that is consistent
with the stored one is accepted. The storage behind it is a Git repository. **Proposed, not yet
accepted:** an archive building block on the server side receives the publications, checks the
signed tree head and the consistency, and writes to Git. Only the archive holds the Git
credentials, so peers and agents need no write access to the repository. OPEN: which repository,
and whether the journal and the artifacts move to Git as well. **Consequences:** No election of a
publisher. A late or repeated publication is harmless.

```arc42
:::decision
id: dec-publish-session
title: Every peer publishes the session; the longest consistent copy wins
status: accepted
date: 2026-11-07
addresses: risk-publication-race, qg-resumability
:::
```

## Peers restore a lost session log

**Context:** The server keeps a session log only in memory. After a restart during a session, it
has lost it. **Decision:** The next peer in the order of joining (the first `participant.joined`
in its copy, then the next) restores the log to the server. The server accepts it only with a
valid signed tree head and continues the IDs after it. **Consequences:** Cursors stay valid, so
no epoch is needed. The server needs a restore command. **Rejected:** an epoch that only tells
peers that the log is lost.

```arc42
:::decision
id: dec-restore-from-peers
title: The next peer in joining order restores a lost log
status: accepted
date: 2026-11-07
addresses: qg-resumability
:::
```

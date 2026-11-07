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
hub renders a stage as a web page that is shown by screen share. **Consequences:** It works with
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
by time. The hub stores the ID with the event. If the same ID arrives again, the hub returns
the existing event and creates no new one. This also applies to `join`: the client keeps its join
command ID, so a restarted client stays the same participant. **Consequences:** Clients can simply
repeat commands after a drop. The hub must keep the IDs at least for the life of the session.
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
canonical JSON form of the same event messages. **Consequences:** One schema for hub, clients
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
the hub with Go templates and gets live updates through server-sent events. It needs no
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
hub. **Decision:** Every participant, including every agent, has its own Ed25519 key pair,
created locally. Only the public key is sent to the hub. In the first stage, keys are registered
by hand at first contact. Agents act as themselves, not on behalf of a human. The stage is opened
with a read-only link. A participant can have several keys (several devices), and an admin can
remove a key. **Proposed details:** challenge and response with a short-lived access token bound
to the key. **Consequences:** No shared secrets; agents
need only a key file. Enrollment needs a manual step for now (issue #2). **Considered:** OIDC with
an existing identity provider (standard, but heavy for a small team); mTLS with client certificates
(strong, but certificate management and awkward in the browser); static API tokens (secret on the
hub, no binding to a device); passkeys (good for humans in the browser, not for CLI and agents).

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
events after cursor N". The hub is the only writer of a session log and gives every event an
ID that grows by one, so the order is free of conflicts and a gap is visible from the IDs alone.
A participant polls this request when it finds a gap, or instead of a local file. No event needs
the ID of its predecessor. **Consequences:** Agents without a file system can take part by
polling. A suppressed utterance must keep its ID (`dec-off-the-record`). If the hub loses a
log, it restores it from the archive and the peers, so cursors stay valid
(`dec-restore-from-peers`). **Rejected:** a long-poll tool only for MCP; a predecessor ID in every event (not needed with a
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
record afterwards. This is its own command; the hub checks that the caller is the speaker. The
mark is a new event. The interfaces then suppress the text: they deliver a placeholder with the
same ID, the stage hides it, and it does not go into minutes or the journal. Agents that already
read it must respect the mark. The raw log keeps the original. Marking the sentences of others
(moderation) is out of scope for the first stage (issue #31). **Consequences:** The log stays
complete and honest. Suppression works for every reader that uses the interfaces, but not for
what an agent already holds in its context. The published session keeps the original and the
mark; reading it from the archive also gives the placeholder (`dec-session-persistence`).
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
all is the consent that every other participant keeps a copy of the session and that the hub
stores it in the archive (`dec-session-persistence`). The stage shows the
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

## The hub persists sessions through an archive

**Context:** The hub orders every event and holds the complete log while a session runs. Every
peer holds a copy too, but as the reading interface for agents, not as storage.
**Decision:** The hub is the gateway to the archive: it brings the semantics (order, checks,
signatures, placeholders), and an external Git repository is the pure storage behind it. The hub
writes the log of a session to the archive at a configurable interval and at `session.ended`.
Writing never holds up the session: it runs in the background, at most one write is in progress,
and a write that takes longer than the interval makes the next write take everything that has
come since. More frequent is better; the interval only has to stay above the usual commit time.
Peers do not publish. **Consequences:** One place writes, so there is no race between writers.
A Git commit is slow, so the archive is always a little behind; the peers' copies cover the
rest (`dec-restore-from-peers`). OPEN: which repository, and whether the journal and the
artifacts move to the archive as well. **Rejected:** every peer publishes (redundant when the
hub has the log); a commit per event that the session waits for (it slows every command);
fully peer-to-peer ordering without a hub.

```arc42
:::decision
id: dec-session-persistence
title: The hub persists sessions to a Git repository through an archive
status: accepted
date: 2026-11-07
addresses: risk-archive-latency, qg-resumability, qg-performance
:::
```

## The session log is a Merkle tree signed by the hub

**Context:** The Git repository is pure storage outside our control, and after a crash the hub
takes the missing events from a peer. Neither must be able to change a session unnoticed.
**Decision:** The events of a session are the leaves of a Merkle tree, as in Certificate
Transparency. The hub signs the tree head (root hash and size), at least with every write to the
archive and in `session.ended`. Anyone can check a stored session against its signed tree head,
and a consistency proof shows that a longer log extends a shorter one. The leaf of an utterance
that is off the record keeps its hash, so a copy with a placeholder still verifies.
**Consequences:** Changes in the repository and forged peer copies are visible. The signing key
of the hub needs protection. **Rejected:** a plain hash chain (it cannot prove that one log
extends another without the whole log).

```arc42
:::decision
id: dec-merkle-log
title: Merkle tree with tree heads signed by the hub
status: accepted
date: 2026-11-07
addresses: risk-forged-copy, qg-confidentiality
:::
```

## Peers fill in what the archive does not have yet

**Context:** After a crash, the hub has lost the events since its last write to the archive.
**Decision:** The hub restores a session from the archive. Any peer that still has a longer copy
supplies the missing events; the hub accepts them only if a consistency proof against the last
signed tree head holds, and continues the IDs after them. The first copy that fits wins.
**Consequences:** Cursors stay valid, so no epoch is needed. No order among the peers is needed.
**Rejected:** an epoch that only tells peers that the log is lost; restoring only from the peer
that joined first.

```arc42
:::decision
id: dec-restore-from-peers
title: The hub restores from the archive; peers fill in the rest
status: accepted
date: 2026-11-07
addresses: qg-resumability
:::
```

## One interface of the hub for every type of record

**Context:** Decisions, minutes, notes, tasks and slides are different things with different
lives. Teams already use tools of their own for some of them, for example issues for tasks.
**Decision:** The hub offers its own interface for every type: sessions, decisions, minutes,
notes, tasks and slides. Each interface owns its types (`concept-module-boundaries`). Behind
every interface, the hub decides where the records are stored; the storage can be exchanged per
type without changing the interface. The building blocks: one part of the hub for decisions,
minutes and notes (replacing the journal), one for tasks, the artifact tools for slides, and one
archive part that all of them use for storage.
**Consequences:** A new type is a new interface. An agent always knows what it writes. The
journal as one interface for all kinds goes away. **Rejected:** one journal for all kinds (it mixes
records with different lives); tasks only in the hub (teams have their own task tools).

```arc42
:::decision
id: dec-records-per-type
title: One interface of the hub for every type of record
status: accepted
date: 2026-11-07
addresses: qg-usability
:::
```

## Everything as YAML in a local Git repository first

**Context:** The storage behind the interfaces should be local and predictably fast at first, and
replaceable later. **Decision:** At first, the hub stores every type as YAML files in a local Git
repository on the hub host and commits locally. The push to a remote is mocked. Tasks are local
drafts too. Later, tasks are synced with the task tool of the team (for example GitHub or GitLab
issues), and the synced task replaces the local draft. **Consequences:** No external service is
needed to start, and commits stay fast. Sessions are written in the background at an interval
(`dec-session-persistence`). The session log is the exception to YAML: it is stored with one
line per event, in the same format as the local log of the peers, because it only grows and every
write appends lines. **Rejected:** a database (not needed yet);
external task tools from the start (slow and not predictable).

```arc42
:::decision
id: dec-yaml-git-storage
title: Every type as YAML in a local Git repository; push mocked
status: accepted
date: 2026-11-07
addresses: risk-archive-latency, qg-performance
:::
```


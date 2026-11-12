# Architecture Decisions

All decisions come from the concept discussion on 2026-11-06 and the first review. Decisions the
maintainer made explicitly are `accepted`. Proposals that still need confirmation are `proposed`.

## Headless, no intelligence in the application

**Context:** Agents improve fast, and every team uses different ones. **Decision:** The application
only exposes APIs (MCP, CLI) and media connectors. The agent makes the judgements.
**Consequences:** The hub stays deterministic and testable. Features that need judgement do not
belong in the hub (`dec-models-in-client`).

```arc42
:::decision
id: dec-headless-byoa
title: Headless office, bring your own agent
status: accepted
date: 2026-11-06
addresses: con-no-intelligence
:::
```

## The rule "no intelligence" binds the hub, not the client

**Context:** The rule "no intelligence" was written for the whole application. The hub holds the
state of the team and needs governance: its results must be deterministic and traceable. The
client runs on the computer of a participant, and the ears already use a speech model there. To
keep an agent in a meeting at a sensible cost, the client may want to ask a small model whether
the agent should act. **Decision:** `con-no-intelligence` applies to the hub only. The
participant client may contain models, generative or not, on the participant's side. The office
ships no generative model; a user brings a forwarding judge or a decision model
(`dec-byo-decision-model`). **Consequences:** The
hub stays as it is. A model in the client can only change what the client of that participant
does, never what the hub stores or how it decides. Tests of the client use test doubles of such
models and never call a real model. **Rejected:** a model in the hub (no governance); no model in
the client at all (the agent would have to be woken at every utterance, also when nobody needs
it).

```arc42
:::decision
id: dec-models-in-client
title: The rule "no intelligence" binds the hub, not the client
status: accepted
date: 2026-11-11
addresses: con-no-intelligence
:::
```

## An agent runner drives the agent harness over ACP

**Context:** Claude Code, opencode and Kiro work in turns (`con-turn-based-agents`). They finish
a turn and wait for a human. Following the local log with `tail -f` in a background task works
only while the agent is in a turn, and people must use a keyword so that a filter wakes the
agent. People want to address the agent in plain words. **Decision:** A new part of the client,
the agent runner, keeps the agent in the meeting. It follows the local log of the agent and
starts the agent's turns. Runner and harness are separate programs. They talk over the Agent
Client Protocol (ACP): the runner is the ACP client and starts the harness as its ACP agent over
standard input and output. The runner opens one ACP session per meeting and hands it the office
tools as an MCP server that runs with the key of the agent. **Consequences:** Every harness that
speaks ACP can take part without code for that harness in the office. The agent keeps its
context across the meeting in one ACP session. The agent still acts only through the office
tools, as itself. A harness that does not speak ACP still works the old way, through MCP or the
CLI and the log file. The runner depends on a protocol that is young and still changes.
**Rejected:** a runner per harness (code for every product); building the agent into the office
(`dec-headless-byoa`); MCP notifications to wake the agent (most clients do not pass them on,
`con-turn-based-agents`).

```arc42
:::decision
id: dec-agent-runner-acp
title: An agent runner drives the agent harness over ACP
status: accepted
date: 2026-11-11
addresses: con-turn-based-agents, qg-usability
:::
```

## Check after each utterance whether to wake the agent

**Context:** The agent should notice when it is addressed, without a keyword. It should also
notice when a speaker starts a new topic within a monologue, or talks about something that
concerns the role of the agent. The end of a turn (`turn.end`) comes too late for that.
**Decision:** After each utterance of another participant, the runner checks whether to wake the
agent. A forwarding judge or a decision model that the user brings makes the check
(`dec-byo-decision-model`). When it says the agent should act, the runner sends the agent what others said since its last delivery,
as one prompt. Each utterance says who spoke and whether that participant is a human or an
agent. The agent decides by itself how to act: answer, raise its hand, or update the minutes
silently. Without such a model, a check per utterance would wake the agent at every
utterance. So the wake strategy `turn-end` wakes the agent at each change of speaker instead, and
the runner says at start that it does not notice a new topic within a monologue
(`dec-agent-runner-delivery`). **Consequences:** People talk to the agent in plain words, and the
agent can follow topics within a long monologue. The check runs for every utterance, so the model
must be fast and cheap. An agent may act while someone still speaks; it should not interrupt
speech, but raise its hand or write. **Rejected:** a wake-up only at each change of speaker (it
misses a new topic within a monologue); a keyword (not natural).

```arc42
:::decision
id: dec-wake-check-per-utterance
title: Check after each utterance whether to wake the agent
status: accepted
date: 2026-11-12
addresses: con-turn-based-agents, qg-usability
:::
```

## Notification strategy of the agent runner

**Context:** The runner must deliver what was said reliably, while the agent may be busy and the
log may grow. The harness works in turns and does not follow the session by itself, so the runner
decides when to notify it over ACP. **Decision:** The runner has one notification strategy. A
wake strategy, chosen by the user at the start of the runner, decides when to wake the agent:

- `turn-end` (the default): at each change of speaker (`turn.end` of another participant). The
  runner says at start that it does not notice a new topic within a monologue.
- `llm`: a forwarding judge that the user brings decides after each utterance
  (`dec-byo-decision-model`).
- `decision-model`: a decision model that the user brings decides after each utterance
  (`dec-byo-decision-model`).

Each wake strategy is one exchangeable part of the runner. All other rules hold for all three:

- The runner joins the session with the key of the agent and runs the log replicator in its own
  process. The agent does not need to join; a join through the tools reuses the same participant
  and the same local log.
- It delivers utterances in the order of the log, from the cursor of its last delivery. It leaves
  out the agent's own utterances and sentences off the record. A sentence that goes off the record
  before it was delivered is never delivered. For a delivered one, the runner wakes the agent as
  soon as it is free and tells it that the sentence is off the record.
- With `llm` or `decision-model`, the runner checks after each utterance of another
  participant, and once more when that participant ends the turn. One check runs at a time. Utterances that come
  while a check runs wait; the next check judges all utterances that the agent has not got yet,
  together. An answer about utterances that the agent got meanwhile counts no more. The runner
  also checks while the agent works.
- Graceful fallback: with `llm` or `decision-model`, a check that fails (an error, a time-out or
  an answer outside the contract) does not wake the agent. The utterances wait for the next
  change of speaker, as with `turn-end`; if that change has come already, the runner wakes the
  agent now. After three failures within five minutes, with no answer between them, the runner
  works as `turn-end` and logs this once. It asks the model again at most once per change of
  speaker, at the first utterance after it, and switches back, with a log line, when the model
  answers. The numbers are fixed; the user cannot change them.
- When the runner wakes the agent, it sends all utterances of others since its last delivery as
  one prompt. One prompt runs at a time. While the agent works, the runner queues what comes
  and sends it together in one prompt afterwards. A change of speaker without new utterances of
  others wakes nobody.
- After `session.ended`, the runner sends what is left and a last prompt with the task of the
  role for the end, then stops the harness. It does not leave the session, because a session
  command would open it again (`dec-session-end-milestone`).
- The runner answers permission requests of the harness by a rule of the user: by default it
  allows the office tools and rejects every other tool. It offers the harness no access to its
  file system or terminal.
- A new runner opens a new ACP session. It starts its delivery after the join of the agent, so
  a new runner for the same agent delivers what was said since that join.

**Consequences:** Nothing is lost and nothing is sent twice within one ACP session. The agent
gets fewer, larger prompts when it is slow. A slow model sees several utterances in one
check instead of falling behind. A model that fails costs at most the wake-ups of `turn-end`, and
the agent never waits longer than for the next change of speaker. The permission rule keeps an unattended agent from running
other tools unless the user allows them. **Rejected:** cancelling a running prompt when a new
turn comes (the agent would lose its work); one prompt per turn while busy (the agent would lag
behind more and more); one check per utterance in parallel (answers could come out of order and
the model would be asked more often than it can answer); waking the agent at every utterance
without a model (too many wake-ups); waking the agent at every failed check (a broken model would
wake it at every utterance); branches on "is a model configured" spread through the runner
instead of one exchangeable wake strategy.

```arc42
:::decision
id: dec-agent-runner-delivery
title: Notification strategy of the agent runner towards the harness
status: accepted
date: 2026-11-12
addresses: qg-resumability, qg-usability, risk-retraction-leak, risk-agent-wakeups, risk-harness-drift
:::
```

## A forwarding judge or a decision model that the user brings

**Context:** The runner checks after each utterance whether to wake the agent
(`dec-wake-check-per-utterance`). Waking a harness with a large model for this costs time and
money, and it is slow to decide that it is not addressed. A small model can decide this faster
and cheaper. The hub must stay without intelligence (`con-no-intelligence`), and the office
should not pick a model for its users. **Decision:** The user brings one of two kinds of model,
each with its own interface:

- A forwarding judge (`if-forwarding-judge`, wake strategy `llm`): a model or program to which
  the runner forwards its question, typically a general language model that judges by a prompt.
  It is an OpenAI-compatible chat endpoint with structured output by JSON schema (for
  example llama.cpp, Ollama or a hosted model), or a command that reads the request as JSON and
  writes the answer as JSON. It answers `act`, `addressed`, `topic_closed` and `reason`.
- A decision model (`if-decision-model`, wake strategy `decision-model`): a small model made to
  answer questions about a state with probabilities, reached through the System One API, for
  example TypeSafe Jev or Cloudflare Clef. The runner sends the state of the meeting and asks
  whether the agent should act and whether a block of topics has ended, each as a probability,
  and whom the speaker addressed, as a choice among the participants and "nobody". A probability
  at or above a threshold of the user counts as yes. There is no reason, so the runner logs the
  probabilities.

Towards the runner, both kinds hide behind one wake strategy, with the runner's own request
and verdict; only the strategy maps them to the external contract. With either kind, the runner
wakes the agent when it should act. When a block of topics has
ended, it wakes the agent with the task of the role for the end of a topic, for example to update
the minutes or to record decisions. If the model fails, the runner falls back to the change of
speaker (`dec-agent-runner-delivery`). Without a model, the wake strategy `turn-end` wakes the
agent at each change of speaker. **Consequences:** The office ships no model and stays free of a
model vendor. Two contracts cover general chat models and dedicated decision models; each has its
own request and answer. A wrong "no" of the model can keep the agent silent when it was
addressed; the utterances stay queued and reach it with the next prompt, so nothing is lost. The
System One API comes from one vendor, and another vendor states that it is compatible
(`risk-decision-model-api`). **Rejected:** a model built into the office; one fixed vendor API;
one contract for both kinds (a chat model answers with booleans and a reason, a decision model
with probabilities); reaching a decision model through the chat endpoint of a model router (it
does not take the structured output that the forwarding judge needs).

```arc42
:::decision
id: dec-byo-decision-model
title: A forwarding judge or a decision model that the user brings
status: accepted
date: 2026-11-12
addresses: con-no-intelligence, qg-performance, qg-usability, risk-agent-wakeups, risk-decision-model-api
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
into a building block, on a required interface that is never used, and on two elements of the
model that share a path. A small workspace with
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
same ID, the stage hides it, and it does not go into minutes, decisions or notes. Agents that already
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
rest (`dec-restore-from-peers`). OPEN: which remote repository. **Rejected:** every peer publishes (redundant when the
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

## Every peer holds a signed tree head over its whole copy

**Context:** The hub accepts a peer copy only against a tree head it signed
(`dec-restore-from-peers`, `dec-merkle-log`). But the events that the hub lost in a crash are
exactly the ones after its last write to the archive, so the stored tree head does not cover them.
After the restart, the hub also does not know how long the longest copy is. **Decision:** The
session stream carries a tree head signed by the hub together with the last event the hub has.
So every peer holds a signed head over its whole copy, and keeps it next to its local log. A peer
whose cursor the hub does not know offers the events the hub misses with that head. The hub checks
the signature with its own key, that the events give the root hash, and a consistency proof
between its own head and the head of the copy. After a restart, a session that has not ended
takes no commands until a peer copy fits or a configurable wait is over, so no new event can take
the ID of a lost one. There is no wait if the hub stopped after a last write of every session.
**Consequences:** A forged or changed copy fails, and the hub needs no extra storage per event.
Every batch of the stream costs one signature. Commands of a restored session can fail for up to
the wait; clients repeat them, which is safe (`dec-idempotent-commands`). OPEN: a session that the
hub never wrote to the archive is unknown after a crash until a peer offers a whole copy; a peer
that comes back after the wait has a copy that forks. **Rejected:** a head signed only at every
write to the archive (it does not cover the lost events); accepting a tail without a signed head
(anyone could add events); persisting a head on the hub for every event (a write per event).

```arc42
:::decision
id: dec-signed-head-in-stream
title: The stream carries signed tree heads; a restored session waits for peers
status: accepted
date: 2026-11-07
addresses: qg-resumability, risk-forged-copy
:::
```

## One interface of the hub for every type of record

**Context:** Decisions, minutes, notes, tasks and slides are different things with different
lives. Teams already use tools of their own for some of them, for example issues for tasks.
**Decision:** The hub offers its own interface for every type: sessions, decisions, minutes,
notes, tasks and slides. Each interface owns its types (`concept-module-boundaries`). Behind
every interface, the hub decides where the records are stored; the storage can be exchanged per
type without changing the interface. The building blocks: one part of the hub for decisions,
minutes and notes, one for tasks, one for slides, and one
archive part that all of them use for storage.
**Consequences:** A new type is a new interface. An agent always knows what it writes.
**Rejected:** one journal for all kinds (it mixes
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

## The signing key of the hub is given at start

**Context:** The hub signs tree heads. Clients and readers of the archive trust these signatures.
A key that the hub creates by itself on first start is easy to lose or replace without anyone
noticing. **Decision:** The operator gives the hub its signing key when the hub starts, at first
as the name of a key file on the command line. The hub does not create a signing key by itself
and does not start without one. **Consequences:** The operator controls the key and can keep it
with other secrets. Replacing the key is a deliberate act. OPEN: how clients pin the public key
of the hub (#41). **Rejected:** a key that the hub creates in its data directory.

```arc42
:::decision
id: dec-hub-signing-key
title: The signing key is given to the hub at start
status: accepted
date: 2026-11-07
addresses: risk-forged-copy, qg-confidentiality
:::
```

## A session belongs to exactly one schema

**Context:** The leaves of the Merkle tree depend on the encoding of the events, and the encoding
depends on the version of the session contract. A client with another version could compute
other leaves and reject a correct copy. **Decision:** A session belongs to exactly one schema, the
version of the session contract it was started with. The session records its schema. Changes of
the schema are not taken over inside a running session: a hub with another schema does not
continue a session of an older schema, and verifiers check a session with its own schema.
**Consequences:** Copies and archived sessions stay verifiable after an update of the contract.
An update of the hub ends the use of running sessions of the old schema for writing.
**Rejected:** a leaf format that is independent of the contract (more work now, not needed yet).

```arc42
:::decision
id: dec-session-schema
title: A session belongs to exactly one schema
status: accepted
date: 2026-11-07
addresses: risk-forged-copy, qg-resumability
:::
```

## The end of a session is a milestone, not a stop

**Context:** People keep working after a meeting: artifacts arrive later, and a meeting can go on
another day. **Decision:** `session.ended` is a milestone that triggers other work, for example
the write to the archive. It never blocks anything new; the triggered work runs in the
background, and its result appears as an event in the session log. A later session command (join,
speak, raise a hand, show on the stage, pause or resume) opens the session again; the log shows
this as its own event. Artifacts through their own interfaces (decisions, minutes, notes, tasks,
slides) may refer to an ended session and never open it again. Minutes are such an artifact: they
are written only through the minutes interface and stored at once like every other artifact
(`dec-records-synchronous`). While a session is open, the hub adds a notice to its log when its
minutes change, so the stage and agents see them live. **Consequences:** Streams, replicators and
agents keep following a session after its end until the participant leaves. The session itself
no longer has a command to update minutes. **Rejected:** an end that rejects all later commands;
minutes as a session command.

```arc42
:::decision
id: dec-session-end-milestone
title: The end of a session is a milestone, not a stop
status: accepted
date: 2026-11-07
addresses: qg-usability, risk-archive-latency
:::
```

## Records are written synchronously and before session writes

**Context:** A client must learn whether its decision, note, task or slide deck was stored.
Session logs are written in the background, and a Git commit can be slow. **Decision:** Records
are written synchronously: a call returns only after the commit, and a failure reaches the
client. Writes of records go before waiting writes of session logs, so a record waits at most for
the one session write that is in progress, never for a queue. **Consequences:** Clients get a
clear result. A slow repository slows records, but not the session itself. **Rejected:** records
in the background (the client would not learn about a failure).

```arc42
:::decision
id: dec-records-synchronous
title: Records are written synchronously and before session writes
status: accepted
date: 2026-11-07
addresses: risk-archive-latency, qg-usability
:::
```


## Imported conversations become marked sessions

**Context:** Many conversations happen outside the office: in other meeting tools, in chats, or
with an agent. The team wants minutes, decisions, notes and tasks from them, like from a session.
The hub did not see these conversations happen, so it cannot order and sign their messages one by
one. Nobody may mistake such a session for a live one. **Decision:** The hub has its own
interface to import a whole conversation as a new session (`if-session-import`), in one call or in
pages. The meeting session realizes it, so every session log keeps one writer. The first event of
an imported session marks it as imported, with its source, its title and the participant who
imported it; a last event marks the end of the import. The speakers of the conversation become
imported participants with a name and a kind; their IDs are marked as imported, so they never
match a registered participant. The utterances keep their order and, if known, the time they were
made. Every page carries an ID created by the client (`dec-idempotent-commands`). Until the import
is complete, the session takes only the pages of the participant who started it. After that, it is
a session like any other: participants join, write minutes and end it, and the stream, the events
after a cursor, the archive and the restore treat it like every other session. The end of an
import starts a write to the archive, like the end of a session. The hub signs tree
heads over an imported log as over every log. The signature proves who imported the log and that
it has not changed since; it does not prove that the speakers said what it says.
**Consequences:** Agents and humans derive records from an imported session through the existing
interfaces, without a second path. Readers recognise an imported session by its first event. The
new events are a new revision of the session schema (`dec-session-schema`). OPEN: nobody can mark
an imported sentence off the record, because the speakers are not participants with a key (#50).
OPEN: an import that stops halfway leaves an incomplete session; it cannot be continued by a new
call of the client (#51). **Rejected:** a separate part of the hub with its own log (two writers,
or an extra internal interface for writing many events at once); a signature per imported
message (it would claim an order the hub never saw); ending the session with the import
(participants could not write minutes into it any more); matching imported speakers to registered
participants by name (the hub cannot check it).

```arc42
:::decision
id: dec-session-import
title: Imported conversations become marked sessions
status: proposed
date: 2026-11-07
addresses: qg-usability, risk-imported-conversation
:::
```

## An own JSON format for imported conversations, read by the client

**Context:** Conversations come from many tools, each with its own export. The import interface
should not change with every new tool. **Decision:** The first import format is our own: one JSON
document per conversation with its title, its source, its participants and its utterances in
order. The client core reads it and sends the conversation to the hub in pages, so the CLI and the
MCP server accept the same format. The README of the client documents the format with an example.
Adapters for other formats, for example chat exports or WebVTT transcripts of meeting tools, are
later work in the client and translate into the same pages. **Consequences:** The hub knows only
the import interface, not the formats of other tools. An agent can write a conversation as one
JSON object, in a file or directly as the argument of the MCP tool. A very long conversation is
one file, but goes to the hub in pages. **Rejected:** one line per message (the file then needs a
header line of another kind, and an agent cannot write it as one object); the JSON form of the
interface itself (paging and enum names would leak into a format for authors); reading the
formats in the hub (every new tool would change the hub).

```arc42
:::decision
id: dec-import-format
title: An own JSON format for imported conversations, read by the client
status: proposed
date: 2026-11-07
addresses: qg-usability, con-no-intelligence
:::
```

## The client chooses its speech model by language and speed and downloads it on first use

**Context:** The ears need a Whisper model and a voice activity model. The client is published as
a ready binary, so a participant has no build step that could fetch them. One model does not fit
every meeting: a large model transcribes names and terms better, a model fine-tuned for one
language is better still for that language, but both are too slow for live use without a GPU. A
small model is fast on every computer, but makes more mistakes. **Decision:** The client knows a
catalogue of speech models by name: a small multilingual model, a large multilingual model and a
large model fine-tuned for German. The participant names the spoken language. The client takes the
best model for that language: the German model for German, the large multilingual model for every
other language. The participant can ask for the fast model instead. When the build of the client
has no GPU support, the client takes the fast model by itself and says so. A participant can still
name any model of the catalogue or a model file. When the ears start and the chosen model of the
catalogue is missing, the client downloads it once from its public source, or from a mirror that
the participant configures. It checks every file against a checksum that is pinned in the client
and keeps only a matching file. Separate commands list the catalogue with what is downloaded and
which model would be used, and download models ahead of time. A model file that the participant
chose is never downloaded. **Consequences:** The first join with the microphone needs the network
and takes longer; a large model is about 1.6 GB. Later joins are offline as before. Only the
model files come in; no audio, text or identity goes out (`qg-confidentiality`). A changed or
damaged file at the source stops the download instead of changing the transcription. A new model
or a new version needs a new client. The client decides about the GPU from its build, not from
the hardware; a participant with an own build can override it. OPEN: whether the large models are
fast enough for live use on the GPUs of participant laptops (minimum hardware, chapter 7).
**Rejected:** models inside the binary (gigabytes for agents that never listen); a download
without a checksum (the client would load whatever the source serves); one fixed model for
everyone (worse for German, or too slow without a GPU); detecting the GPU from the hardware (the
binary can only use what it was built with).

```arc42
:::decision
id: dec-model-download
title: The client chooses its speech model by language and speed and downloads it on first use
status: proposed
date: 2026-11-10
addresses: qg-usability, qg-confidentiality
:::
```

## The ears give Whisper the vocabulary of the project and the names of the participants

**Context:** Whisper often misspells names and terms of a project, for example product names,
abbreviations and the names of the people in the meeting. These words matter most in minutes and
tasks. Whisper accepts a short text as a prompt before each sentence and then prefers its words
and spellings. The prompt is short: Whisper keeps only the last part of a long one.
**Decision:** The ears build the prompt from two sources: a vocabulary file of the project in the
working directory of the client (or a file the participant names), then the display names of the
participants of the session, which the client knows from its local log. The terms keep their
order in the file; the names follow. What does not fit is cut at the end, so the first terms of
the file always count. The ears report how many terms and names they use and how many they cut.
The previous sentence as additional context is an option and off by default, because nobody has
measured yet whether it helps. **Consequences:** A team improves the transcription by writing a
plain text file; no model is trained. The vocabulary and the names stay on the device like the
audio (`concept-edge-transcription`). A wrong or very long vocabulary can make Whisper write a
term that was not said. OPEN: the effect on the error rate is not measured; a measurement corpus
is out of scope of the epic (#66). **Rejected:** fine-tuning a model per team (training data and
compute that a team does not have); correcting the text after Whisper (it cannot recover a word
that Whisper did not hear as such).

```arc42
:::decision
id: dec-vocabulary-prompt
title: The ears give Whisper the vocabulary of the project and the names of the participants
status: proposed
date: 2026-11-11
addresses: qg-usability, qg-confidentiality
:::
```

## A multilingual NER model in plain Go finds the names in project documents

**Context:** A good vocabulary needs the names of people, organisations, places and products of
a project (`dec-vocabulary-prompt`). They are in the project's documents, in German and English.
Rules alone find words such as CamelCase names and acronyms, but not "Katharina Weber" or
"Sonnenstrahl GmbH". The maintainer allows models in the client, as long as they do not generate
text (`con-no-intelligence` applies to the hub). The client is Go, and its only native library
is whisper.cpp (`dec-languages`). **Options:**

- a pure-Go NER library: the known one (prose) has an English model only;
- an ONNX model through the ONNX runtime: fast, with small quantised models (about 135 MB), but
  a second native library that must be built or shipped for every platform of the client;
- Python tools such as spaCy or Flair: good German models, but a Python runtime next to the
  client;
- a generative model: excluded, it writes text.

**Decision:** The client uses the multilingual DistilBERT model for named-entity recognition by
Davlan (distilbert-base-multilingual-cased-ner-hrl, licence AFL-3.0). It knows German, English and
eight more languages, and the entity types person, organisation, place and date. The client runs
the model with its own forward pass in plain Go. It downloads the weights (about 540 MB) on first
use, like the speech models, with a pinned checksum (`dec-model-download`). Rules for CamelCase
words, acronyms and words of headings add terms that the model does not know. The command ranks
all terms and writes the vocabulary file, which the participant can edit.
**Consequences:** The client needs no new native library and runs the same on every platform. The
model only labels words; it never writes text. The forward pass in Go is slow: on a shared host
with 4 CPUs it handled about 60 to 100 words per second; 23,000 words took 4 minutes. The model
was trained on news, so it misses many product and technical terms; the rules cover part of that. OPEN: whether the speed is good enough for large projects; the ONNX runtime is
the faster option (#85). OPEN: the German training data (CoNLL 2003) has its own terms of use;
the model itself is published under AFL-3.0. **Rejected:** the options above, for the reasons
given.

```arc42
:::decision
id: dec-ner-model
title: A multilingual NER model in plain Go finds the names in project documents
status: proposed
date: 2026-11-11
addresses: qg-usability, qg-confidentiality, con-no-intelligence
:::
```

## The client reads its options from YAML files: a user file, overridden by a project file

**Context:** The agent runner alone has more than ten options, among them the URL and the model
of a decision model. A user types them again at every start, and a team cannot share them. A key
of a model must not end up in a file that a team checks in. **Decision:** The client reads its
options from two files in YAML (`concept-client-configuration`). The user file lies in the
configuration folder of the client, where the key and the certificate of the hub already are. The
project file in the folder `.office` of the start directory overrides it key by key, like the
vocabulary of the project. Environment variables override both files, and command-line options
override everything. Unknown keys and wrong types are errors that name the file and the key. A
file names the environment variable of a key, never the key. **Consequences:** An agent with its
role and its decision model starts with one short command, and a team can keep one project file
per role in its repository. A typo in a file stops the command instead of being ignored. The
client needs a YAML reader, which the hub already uses for its records. **Rejected:** TOML (the
maintainer chose YAML); options only on the command line (long commands, nothing to share);
keys in the file (a checked-in file would leak them).

```arc42
:::decision
id: dec-client-config-file
title: The client reads its options from YAML files, a user file overridden by a project file
status: accepted
date: 2026-11-12
addresses: qg-usability, qg-confidentiality
:::
```

## Multi-tenancy in one hub

**Context:** The hub has one archive repository, one signing key and one list of participants,
and one admin file. A team can use it, but several organisations cannot share a hub.
**Decision:** One hub serves several tenants. An organisation is one tenant, and a tenant has one
local git repository with a directory for each kind of content. Users are global: they are
registered at the hub first and then assigned to tenants. The hub keeps users, keys, tenants and
system admins and the memberships with their roles in its own git repository, and only Identity
writes there. The role per tenant is `member` or `tenant admin`; a separate system admin role runs
the hub. Authentication stays with Ed25519 key pairs (`dec-auth-keypair`); first contact for
further users works through an invitation (issue #2). One signing key of the hub signs the tree
heads of all tenants. Identity checks every change and the loaded state against fixed rules (`concept-multi-tenancy`).
**Consequences:** Every interface of the hub needs a tenant, and the archive
needs a tenant in every call. The file of admin keys is removed. At start, the hub reads the hub
repository and builds the index from keys to users in memory. Revoking a key happens once, at the
hub. **Rejected:** one hub per tenant (many processes to operate, no single user list); several
repositories per tenant (no use case; directories separate content); users that belong to exactly
one tenant (advisers and agents would need several accounts); keys or memberships inside each tenant repository
(a revoked key would have to be removed in every tenant, and files in a tenant repository cannot
be trusted for rights); OIDC (see `dec-auth-keypair`); one
signing key per tenant (only useful when tenants distrust the operator). **Out of scope:**
synchronisation of a repository with 0..n remotes (#43). OPEN: whether a tenant needs its own
signing key later.

```arc42
:::decision
id: dec-multi-tenancy
title: One hub serves several tenants, each with one git repository
status: accepted
date: 2026-11-12
addresses: qg-confidentiality
:::
```

## Ended sessions leave memory and load on demand

**Context:** The session log stays in memory after it is archived, and the restore loads every
session at start. With several tenants, memory grows with the sum of all sessions
(`risk-session-memory`, issue #113). **Decision:** A session that has ended and that nobody
follows leaves memory. A later read loads it again from the archive with the same events and the
same signed tree head. At start, the restore loads only sessions that have not ended.
**Consequences:** A read of an old session can take as long as a read from the archive. The
restore, the replication and the stage must work with a session that is loaded again.
**Rejected:** a memory limit per tenant (decide it later if a measurement shows the need); keeping
everything in memory and buying more memory (cost grows with every tenant).

```arc42
:::decision
id: dec-session-eviction
title: Ended sessions leave memory and load on demand
status: accepted
date: 2026-11-12
addresses: risk-session-memory
:::
```

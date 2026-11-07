# Cross-cutting Concepts

## Participant model

There are only **participants**. Each participant has an identity, a kind (`kind: human | agent`)
and roles, for example moderator, minute taker or stage control. Rights depend on roles, not on
the kind. The kind stays visible for two reasons: the stage marks agents as agents, and only speech
needs consent to be recorded.

An utterance is an utterance, whatever its channel. `utterance.final` carries
`modality: voice | text`. Human speech is `voice`. Typed remarks and an agent's contributions are
`text`. Later, speech output will voice text utterances, and the model stays the same.

```arc42
:::concept
id: concept-participant-model
title: One participant model
category: domain
:::
```

## Event log with cursor

Each session has an append-only log with exactly one writer, the hub. It gives every event an
ID that grows by one inside the session. So the order is free of conflicts, and the IDs have no
gaps: a participant that sees event 43 after 41 knows that 42 is missing and fetches the events
after its cursor (`dec-events-from-cursor`). The cursor is the last event ID a client has read,
which is also the last line of the local file. Utterances never change and are never removed.
Corrections and off-the-record marks are new events that point to the original
(`utterance.corrected`, `utterance.off_the_record`). An utterance off the record keeps its ID; the interfaces
deliver a placeholder without its text, so no false gap appears (`dec-off-the-record`).

**Storage and integrity.** The hub orders the events and is the gateway to the archive: it writes
the log to a Git repository at a configurable interval and at the end, without ever holding up
the session (`dec-session-persistence`). At first the repository is local on the hub host
(`dec-yaml-git-storage`). The peers keep copies as their reading interface; their copies and the
stored log have the same lines. The events are the leaves of a Merkle tree, and the hub signs its
tree head (`dec-merkle-log`). Every write to the archive carries the signed tree head over
exactly the stored log. After a crash, the hub restores a session from the archive, and a peer fills in
the rest (`dec-restore-from-peers`).

**Resuming after a network drop:**

- Readers (replicator, stage) reconnect with their last event ID and receive every event after it.
  Nothing is lost, and nothing arrives twice.
- Writers (ears, CLI, MCP) give every command an ID that they create themselves. If they send a
  command again after a drop, the hub recognises the ID and returns the existing event.
- This includes `join`: a client keeps its join command ID, so after a restart it is still the
  same participant.
- The ears queue utterances locally while offline (text only) and send them in order.

Event levels (guide values, configurable per participant):

| Event | Trigger |
|---|---|
| `utterance.final` | End of sentence, about 300–700 ms of silence |
| `turn.end` | Change of speaker, about 1.5–2 s of silence |
| `silence.long` | More than 5 s of silence |
| `addressed` | A participant's name is mentioned |
| `hand.raised`, `minutes.updated`, `artifact.shown` | Actions of participants |
| `participant.joined/left/paused`, `session.ended` | Life cycle |

Partials are not written to the log (`dec-no-partials-in-log`).

**Checking a log:** the events of a session are the leaves of a Merkle tree (`dec-merkle-log`).
The hub signs the head of the tree with its own key, which stays the same across restarts. It
puts the signed head of the log so far into `session.ended`, and returns a signed head, a
consistency proof or an inclusion proof on request. A placeholder of an utterance that is off
the record carries the leaf hash of the original, so a copy with placeholders still verifies.
A placeholder can only hide a text; it cannot change what the tree proves.

```arc42
:::concept
id: concept-event-log
title: Append-only session log with cursor and resume
category: integration
:::
```

## Read from the file, write through tools

Participants read what happens from the locally replicated file. Agents usually do this with a
background process and a filter (`tail -f … | grep '"turn.end"\|"addressed"'`). They act through
tools (CLI or MCP) that answer at once and check their input. Every action then appears in the log.
So agents can confirm their own actions, and several agents can coordinate without a protocol of
their own.

An agent can also fetch the events after a cursor itself: when it finds a gap in its copy, or
when it has no file system and polls instead of following a file (`dec-events-from-cursor`).

```arc42
:::concept
id: concept-read-file-write-tools
title: Read from the file, write through tools
category: integration
:::
```

## Transcription at the edge

Each human transcribes only themselves, locally. Raw audio never leaves the device. Only text and
metadata are sent. With a headset, the single ear comes for free. Use with loudspeakers (echo
cancellation, voice print) and the room ear (speaker recognition) come later. Voice prints are
biometric data (Art. 9 GDPR) and need explicit consent.

```arc42
:::concept
id: concept-edge-transcription
title: Local transcription; only text leaves the device
category: privacy
:::
```

## Transparency and consent

People who are captured see every sentence with its status in their terminal. They can pause and
mark a sentence off the record afterwards. The stage shows everyone who is captured, paused or not
captured. People without a client are not transcribed. Joining with the microphone is the consent
to capture and to storage, also of the raw transcript (`dec-consent-by-mic`). Only the user who
runs the client can read local log files.

```arc42
:::concept
id: concept-transparency
title: Transparency and consent
category: privacy
:::
```

## Identity and access

**Actors and keys.** Every participant is its own actor with its own Ed25519 key pair: each human
device and each agent. The key pair is created locally. Only the public key leaves the device. An
agent does not act on behalf of a human; it acts as itself.

**First contact.** In the first stage, we trust a new public key because we meet the person. An
admin registers the key by hand. A real enrollment flow is issue #2.

**Authentication.** Challenge and response: the client signs a challenge from the hub and gets
an access token that lives about 15 minutes and is bound to its key. Every call carries the token.
All connections use TLS.

**Stage.** A browser has no key. Identity creates a read-only link with a token that only allows
viewing the stage of one session.

**Revocation.** Each participant can have several keys (several devices). An admin can remove a
key; tokens bound to it stop working when they expire, at the latest.

**Out of scope for the first stage:** acting on behalf of someone else (delegation), self-service
enrollment (issue #2), encryption of stored data (OPEN).

```arc42
:::concept
id: concept-identity
title: Identity and access with local key pairs
category: security
:::
```

## Test strategy

**No unit tests.** Every building block is tested as a black box through its interfaces
(`con-no-unit-tests`, `con-contract-tests`). The interfaces in chapter 5 are the contracts.

**Automated end-to-end tests** run on a host without a microphone. They feed a recorded
WAV file through the ears in real time. This covers the whole pipeline after audio capture: voice
activity detection, Whisper, submitting the utterance, the session log and the local copy. The
tests also cover resume after the replicator is killed.

**End-to-end tests never check performance.** They check behaviour only: no audio leaves the
device, no event is lost or duplicated, the status of a sentence becomes visible. They pass on any
host that runs the system. **Performance tests** are separate. They check the timing criteria of
the quality scenarios (chapter 10) with their full lengths and run on the reference host.

**Architecture test.** A test checks the code against this model on every run of the tests (see
`concept-module-boundaries`).

**Manual tests** cover what a WAV file cannot: real audio capture from a device and Whisper
accuracy on real speech. A person joins with a headset on a participant laptop and speaks a few
sentences. We do this before we judge recognition quality, because the test WAV is synthetic and
cleaner than a headset in a meeting.

**Not planned:** a virtual microphone on the hub host. It needs administrator rights and adds
little.

```arc42
:::concept
id: concept-test-strategy
title: Test strategy for the audio pipeline
category: testing
:::
```

## Module boundaries

The building block view (chapter 5) is the map of the code, and a test enforces it.

- **One path per building block.** A building block is a black box with one responsibility. Its
  path is where its code lives. A child lies under the path of its parent.
- **An interface belongs to its provider.** Its path lies inside the path of the provider. The
  types of an interface are part of the interface: every provider defines its own types, even if
  another interface has a similar one (for example the kind of a participant, of an actor and of a
  journal author). Consumers map between them.
- **No shared package.** There is no common or core package that everyone may use. Code that
  several building blocks need is either an interface of one of them, or a missing building block,
  or acceptable duplication. Cross-cutting concepts (this chapter) are rules, not places.
- **`requires` is the permission.** A building block may only use the interfaces it provides or
  lists in `requires`. Every listed interface must be used. So a new dependency shows up in the
  model before it can exist in the code.
- **Black box and white box.** An interface that crosses the boundary of a parent building block
  belongs to that parent: it is part of its black box. One direct child realizes it, and the
  model names that child. Only the realizing child may use such an interface without requiring
  it. Interfaces that stay inside a parent belong to the child that provides them. So level 1 of
  the building block view shows the contracts across the boundary, and level 2 shows how the
  parts work together.
- **The white box knows its parts.** A parent building block may wire its direct children. A
  child never uses the inside of its parent, and siblings know each other only through
  interfaces.
- **Tests are external actors.** They may use interfaces only and never reach into a building
  block.
- **Interfaces for actors only** (CLI, MCP, stage view, audio input) also have a path: the place
  where their contract is defined. Building blocks do not use them.

```arc42
:::concept
id: concept-module-boundaries
title: Module boundaries follow the building blocks
category: structure
:::
```

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

Each session has an append-only log. Every event has an ID that only grows. The cursor is the last
event ID a client has read, which is also the last line of the local file. Corrections and
retractions are new events that point to the original (`utterance.retracted`,
`utterance.corrected`). Existing entries never change.

**Resuming after a network drop:**

- Readers (replicator, stage) reconnect with their last event ID and receive every event after it.
  Nothing is lost, and nothing arrives twice.
- Writers (ears, CLI, MCP) give every command an ID that they create themselves. If they send a
  command again after a drop, the server recognises the ID and returns the existing event.
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

As a fallback for agents without a file system, there is a long-poll tool (`meeting.wait`)
(OPEN: is it needed in the first stage?).

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
retract. The stage shows everyone who is captured, paused or not captured. People without a client
are not transcribed. Only the user who runs the client can read local log files. They are cleaned up after the session
according to a retention rule (OPEN: define the rule, also for retracted sentences).

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

**Authentication.** Challenge and response: the client signs a challenge from the server and gets
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

**Automated end-to-end tests** run on a server host without a microphone. They feed a recorded
WAV file through the ears in real time. This covers the whole pipeline after audio capture: voice
activity detection, Whisper, submitting the utterance, the session log and the local copy. The
tests also cover resume after the replicator is killed, and the latency into the local log.

**Manual tests** cover what a WAV file cannot: real audio capture from a device and Whisper
accuracy on real speech. A person joins with a headset on a participant laptop and speaks a few
sentences. We do this before we judge recognition quality, because the test WAV is synthetic and
cleaner than a headset in a meeting.

**Not planned:** a virtual microphone on the server host. It needs administrator rights and adds
little.

```arc42
:::concept
id: concept-test-strategy
title: Test strategy for the audio pipeline
category: testing
:::
```

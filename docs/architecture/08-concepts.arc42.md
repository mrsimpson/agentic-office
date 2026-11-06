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
are not transcribed. Local log files have permissions `0600`. They are cleaned up after the session
according to a retention rule (OPEN: define the rule, also for retracted sentences).

```arc42
:::concept
id: concept-transparency
title: Transparency and consent
category: privacy
:::
```

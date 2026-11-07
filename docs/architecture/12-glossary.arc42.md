# Glossary

## Participant

The central role in the system. Humans and agents are treated the same way.

```arc42
:::glossary-term
id: term-participant
title: Participant
definition: A human or an agent in a session, with an identity, a kind (human or agent) and roles
:::
```

## Session

The frame of one meeting in the office.

```arc42
:::glossary-term
id: term-session
title: Session
definition: One meeting in the office, with its own event log, participants and stage
:::
```

## Session log

The shared source of truth of a session.

```arc42
:::glossary-term
id: term-session-log
title: Session log
definition: The append-only event log of a session; replicated locally as a JSONL file
:::
```

## Cursor

How a client knows where it stopped reading.

```arc42
:::glossary-term
id: term-cursor
title: Cursor
definition: The ID of the last event a client has read; used to resume after a drop
:::
```

## Utterance

The basic unit of what is said or written in the log.

```arc42
:::glossary-term
id: term-utterance
title: Utterance
definition: One finished contribution of a participant, spoken (voice) or written (text)
:::
```

## Raised hand

How participants, especially agents, ask to speak.

```arc42
:::glossary-term
id: term-hand-raise
title: Raised hand
definition: A signal on the stage that a participant wants to contribute; the moderator gives the floor
:::
```

## Stage

The visible presence of the office in the meeting.

```arc42
:::glossary-term
id: term-stage
title: Stage
definition: The web view of a session (minutes, raised hands, artifacts, capture status), shown by screen share
:::
```

## Ears

The audio connector of the office.

```arc42
:::glossary-term
id: term-ears
title: Ears
definition: The local audio pipeline of a human (VAD, Whisper) that sends only text
:::
```

## Single ear

The listening mode of the first stage.

```arc42
:::glossary-term
id: term-single-ear
title: Single ear
definition: A listening mode in which only the owner of the device is transcribed
:::
```

## Room ear

A later extension for meeting rooms.

```arc42
:::glossary-term
id: term-room-ear
title: Room ear
definition: A later listening mode for several speakers at one microphone, with speaker recognition
:::
```

## Artifact

The result of the tool layer.

```arc42
:::glossary-term
id: term-artifact
title: Artifact
definition: A result created by the tools, with an ID (for example a slide deck or a plan check), that can be shown on the stage
:::
```

## Journal

The system of record of the team.

```arc42
:::glossary-term
id: term-journal
title: Journal
definition: The memory of the team (decisions, tasks, minutes), with the origin of each entry
:::
```

## Actor

Who authenticates against the office.

```arc42
:::glossary-term
id: term-actor
title: Actor
definition: A participant with its own key pair; every human device and every agent is one actor
:::
```

## Off the record

A later mark on an own sentence.

```arc42
:::glossary-term
id: term-off-the-record
title: Off the record
definition: A mark that the speaker puts on their own utterance after it was said; the interfaces then deliver it only as a placeholder without its text
:::
```

## Peer

A participant as a keeper of the session.

```arc42
:::glossary-term
id: term-peer
title: Peer
definition: A participant client that keeps a full copy of the session log and may publish it
:::
```

## Tree head

The fingerprint of a session log.

```arc42
:::glossary-term
id: term-tree-head
title: Tree head
definition: The root hash and size of the Merkle tree over the events of a session, signed by the hub
:::
```

## Hub

The central part of the office.

```arc42
:::glossary-term
id: term-hub
title: Office hub
definition: The separately deployed part that orders and distributes the events of a session, checks identities, renders the stage and is the gateway to the archive; it does not store sessions itself
:::
```


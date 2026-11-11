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

The general word for what the stage shows. Each type of artifact has its own interface
(`dec-records-per-type`); today slide decks are the only type.

```arc42
:::glossary-term
id: term-artifact
title: Artifact
definition: Anything that the stage can show by an ID with its type in front, for example the slide deck slides/42
:::
```

## Slide deck

One type of record of the hub, and today the only type of artifact.

```arc42
:::glossary-term
id: term-slide-deck
title: Slide deck
definition: A set of slides (title, bullets, speaker notes) that the hub renders deterministically to one HTML document; the hub keeps its input and the rendered document in the archive
:::
```

## Record

What the hub keeps for the team, one type per interface (`dec-records-per-type`).

```arc42
:::glossary-term
id: term-record
title: Record
definition: A decision, the minutes of a session or a note that the hub stores in the archive, with its author and whether the author is a human or an agent; it replaces the earlier journal entry
:::
```

## Task

A work item of the team (`dec-records-per-type`).

```arc42
:::glossary-term
id: term-task
title: Task
definition: A work item with a title, a description, an assignee, a status (open, in progress, done or cancelled) and an optional link to a session; for now a local draft that the hub stores in the archive, later synced with the task tool of the team
:::
```

## Task reference

How clients name a task.

```arc42
:::glossary-term
id: term-task-reference
title: Task reference
definition: The stable name of a task, a text with a scheme such as local:42 for a draft of the hub or github:owner/repo#12 for an issue; clients keep it and pass it back but never build or read its parts
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

## Agent harness

The program in which an agent runs and that holds its conversation with a model, for example
Claude Code, opencode or Kiro. We say "harness" and not "agent" when we mean the program that the
agent runner starts.

```arc42
:::glossary-term
id: term-agent-harness
title: Agent harness
definition: The program that runs an agent and its model, for example Claude Code, opencode or Kiro; the agent runner starts it and talks to it over ACP
:::
```

## Agent runner

The part of the participant client that keeps an agent in a meeting (`bb-agent-runner`).

```arc42
:::glossary-term
id: term-agent-runner
title: Agent runner
definition: The part of the client that follows the local log for an agent and starts the agent's turns in its harness over ACP, for example at each change of speaker
:::
```

## Agent Client Protocol (ACP)

An open protocol between a client and a coding agent, published at agentclientprotocol.com. The
client starts the agent and talks JSON-RPC with it over standard input and output.

```arc42
:::glossary-term
id: term-acp
title: Agent Client Protocol (ACP)
definition: An open JSON-RPC protocol over standard input and output between a client, here the agent runner, and an agent harness
:::
```

## Decision model

A model that the user brings for the agent runner (`dec-byo-decision-model`). It is not the agent:
it only decides whether the agent should be woken.

```arc42
:::glossary-term
id: term-decision-model
title: Decision model
definition: A model that the user brings and that tells the agent runner per change of speaker whether the agent should act and whether a block of topics has ended
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
definition: The separately deployed part that orders and distributes the events of a session, checks identities, renders the stage and is the gateway to the archive; its archive part writes sessions to a Git repository, which is the storage
:::
```


## Imported session

A session that was not held in the office.

```arc42
:::glossary-term
id: term-imported-session
title: Imported session
definition: A session whose log starts with a conversation that happened outside the office and that a participant imported; its first event marks it as imported and names the source
:::
```

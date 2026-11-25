# Runtime View

## Agent joins a meeting

The agent joins the meeting through the MCP server. The MCP server registers it as a participant, starts the log
replicator and returns the file path. The agent follows the file in the background. It filters for
the events that should wake it up, for example the end of a turn or being addressed by name.

```arc42
:::runtime-scenario
id: rs-agent-joins
title: Agent joins a meeting
trigger: Agent calls meeting.join
involves: bb-mcp, bb-client-core, bb-session-service, bb-replicator, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-agent-joins
notation: mermaid-sequence
scenario: rs-agent-joins
aliases: actor_agent=actor-agent, bb_mcp=bb-mcp, bb_session=bb-session-service, bb_replicator=bb-replicator
:::
```

```mermaid
sequenceDiagram
    participant actor_agent as Agent
    participant bb_mcp as MCP server
    participant bb_session as Meeting session
    participant bb_replicator as Log replicator
    actor_agent->>bb_mcp: join the meeting (session)
    bb_mcp->>bb_session: join (kind=agent)
    bb_session-->>bb_mcp: participant id
    bb_mcp->>bb_replicator: start(session, path)
    bb_replicator->>bb_session: subscribe(cursor=0)
    bb_mcp-->>actor_agent: participant, log path
    bb_session-->>bb_replicator: events (live)
    actor_agent->>bb_replicator: tail -f path | filter
```

## Human speaks

A human joined with the microphone. The ears detect the end of a sentence, Whisper transcribes it locally,
and the utterance is sent. The session appends it to the log. Every replicator writes it to its
local file. The speaker's terminal shows the status "sent". When the human stays silent for longer
after the last sentence, the ears submit the end of the turn. Agents that wait for the end of a turn wake up.

```arc42
:::runtime-scenario
id: rs-human-speaks
title: Human speaks, agent receives the sentence
trigger: End of sentence (VAD pause) in the headset signal
involves: bb-ears, bb-client-core, bb-session-service, bb-replicator, bb-cli, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-human-speaks
notation: mermaid-sequence
scenario: rs-human-speaks
aliases: actor_human=actor-human, bb_ears=bb-ears, bb_session=bb-session-service, bb_replicator=bb-replicator, bb_cli=bb-cli
:::
```

```mermaid
sequenceDiagram
    participant actor_human as Human
    participant bb_ears as Ears
    participant bb_session as Meeting session
    participant bb_replicator as Log replicator
    participant bb_cli as office CLI
    actor_human->>bb_ears: speaks (headset)
    bb_ears->>bb_ears: VAD + Whisper (local)
    bb_ears->>bb_session: submit the utterance (text, voice)
    bb_session-->>bb_ears: event id
    bb_session-->>bb_replicator: final utterance
    bb_replicator->>bb_replicator: append line
    bb_cli->>bb_replicator: read own events
    bb_cli-->>actor_human: status sent
    actor_human->>bb_ears: stays silent (turn silence)
    bb_ears->>bb_session: signal the end of the turn (own participant)
    bb_session-->>bb_replicator: end of the turn
```

## Network drop and resume

The network of a participant drops for a while. The ears keep transcribing and queue utterances
locally. The replicator loses the stream. When the network is back, the ears send the queued
utterances in order. Each one carries its client ID, so nothing is stored twice. The replicator
reconnects with the ID of its last line and receives everything it missed.

```arc42
:::runtime-scenario
id: rs-network-drop
title: Network drop and resume
trigger: Network connection of a participant is lost and comes back
involves: bb-ears, bb-session-service, bb-replicator, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-network-drop
notation: mermaid-sequence
scenario: rs-network-drop
aliases: bb_ears=bb-ears, bb_session=bb-session-service, bb_replicator=bb-replicator
:::
```

```mermaid
sequenceDiagram
    participant bb_ears as Ears
    participant bb_session as Meeting session
    participant bb_replicator as Log replicator
    Note over bb_ears,bb_replicator: network drops
    bb_ears->>bb_ears: queue utterances (text only)
    bb_replicator->>bb_replicator: retry with backoff
    Note over bb_ears,bb_replicator: network is back
    bb_ears->>bb_session: submit the utterances (client IDs, in order)
    bb_session-->>bb_ears: event id (existing id if already stored)
    bb_replicator->>bb_session: subscribe(cursor=last line)
    bb_session-->>bb_replicator: missed events, then live
```

## Participant authenticates

Before its first call, a client (CLI, MCP server or ears) asks Identity for a challenge. It signs
the challenge with its private key and gets a short-lived access token. It sends the token with
every call. The session checks the token and knows the participant. Shortly before the token
expires, the client repeats the steps. This also happens after a network drop.

```arc42
:::runtime-scenario
id: rs-authenticate
title: Participant authenticates with its key
trigger: First call of a client, or the access token is about to expire
involves: bb-cli, bb-mcp, bb-ears, bb-client-core, bb-identity, bb-session-service, bb-participant-client, bb-office-hub
:::
```

## Agent raises its hand and shows an artifact

The agent renders a slide deck through the CLI and raises its hand on the stage. The slides part
stores the input of the deck in the archive before it returns its path. When the agent gets the
floor, it shows the deck by its path. The stage reads the same events from the session
stream and loads the deck from the slides part inside the hub.

```arc42
:::runtime-scenario
id: rs-agent-shows-artifact
title: Agent raises its hand and shows an artifact
trigger: Agent decides after turn.end to raise its hand
involves: bb-cli, bb-slides, bb-archive, bb-session-service, bb-stage, bb-replicator, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-agent-shows-artifact
notation: mermaid-sequence
scenario: rs-agent-shows-artifact
aliases: actor_agent=actor-agent, bb_cli=bb-cli, bb_slides=bb-slides, bb_archive=bb-archive, bb_session=bb-session-service, bb_stage=bb-stage
:::
```

```mermaid
sequenceDiagram
    participant actor_agent as Agent
    participant bb_cli as office CLI
    participant bb_slides as Slides
    participant bb_archive as Archive
    participant bb_session as Meeting session
    participant bb_stage as Stage
    actor_agent->>bb_cli: render a slide deck
    bb_cli->>bb_slides: render
    bb_slides->>bb_archive: commit the input
    bb_slides-->>bb_cli: the deck with its path
    actor_agent->>bb_cli: raise the hand, with a topic
    bb_cli->>bb_session: raise the hand
    bb_session-->>bb_stage: the hand is raised
    Note over bb_session: the moderator gives the floor, event addressed
    actor_agent->>bb_cli: focus the deck on the stage
    bb_cli->>bb_session: focus the artifact
    bb_session-->>bb_stage: the artifact is in focus
    bb_stage->>bb_slides: load the deck by its path
```

## Participant changes a document in a session

A participant reads a document from the archive, changes it and sends the change with the version
that they read and the ID of the session. The documents part writes one commit with the participant
as author and tells the meeting session, which logs the change. When another change
came first, the documents part refuses the change and gives the current version, and the participant
reads again.

```arc42
:::runtime-scenario
id: rs-document-change
title: Participant changes a document in a session
trigger: A participant sends a change of a document with the version they read
involves: bb-documents, bb-archive, bb-session-service, bb-stage, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-document-change
notation: mermaid-sequence
scenario: rs-document-change
aliases: bb_documents=bb-documents, bb_archive=bb-archive, bb_session=bb-session-service, bb_stage=bb-stage
:::
```

```mermaid
sequenceDiagram
    participant bb_documents as Documents
    participant bb_archive as Archive
    participant bb_session as Meeting session
    participant bb_stage as Stage
    Note over bb_documents: change with base version and session ID
    bb_documents->>bb_archive: commit, refused if the base is stale
    bb_documents->>bb_session: artifact notice
    bb_session-->>bb_stage: the artifact changed
    bb_stage->>bb_archive: read the document by its path
```

## Participant sets up a local directory

A participant sets up a directory for the artifacts of the tenant. The sync part gets an access token with the
key of the participant and clones the branch main of the tenant through Git access. It writes the
author, the way to get a token and the check for the main directory into the own configuration of
the clone, and it remembers the directory.

```arc42
:::runtime-scenario
id: rs-sync-setup
title: Participant sets up a local directory
trigger: A participant runs the set-up of a directory
involves: bb-cli, bb-sync, bb-identity, bb-git-access, bb-archive, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-sync-setup
notation: mermaid-sequence
scenario: rs-sync-setup
aliases: bb_cli=bb-cli, bb_sync=bb-sync, bb_identity=bb-identity, bb_git_access=bb-git-access, bb_archive=bb-archive
:::
```

```mermaid
sequenceDiagram
    participant bb_cli as office CLI
    participant bb_sync as Artifact sync
    participant bb_identity as Identity
    participant bb_git_access as Git access
    participant bb_archive as Archive
    bb_cli->>bb_sync: set up the directory
    bb_sync->>bb_identity: prove the key
    bb_identity-->>bb_sync: access token
    bb_sync->>bb_git_access: clone main, token as password
    bb_git_access->>bb_identity: check token and tenant
    bb_git_access->>bb_archive: find the repository of the tenant
    bb_git_access-->>bb_sync: repository, branch main
    Note over bb_sync: writes author, token source and guard into the clone
```

## A client process starts in a set-up directory

The sync part brings the main directory up to date, and then it follows the changes. It keeps no
position: after a start and after every reconnect it fetches first, so no change is missed. When
the main directory has commits of its own, the sync part stops and reports it.

```arc42
:::runtime-scenario
id: rs-sync-start
title: A client process starts in a set-up directory
trigger: A client process starts, or its connection to the hub comes back
involves: bb-sync, bb-identity, bb-git-access, bb-archive, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-sync-start
notation: mermaid-sequence
scenario: rs-sync-start
aliases: bb_sync=bb-sync, bb_identity=bb-identity, bb_git_access=bb-git-access, bb_archive=bb-archive
:::
```

```mermaid
sequenceDiagram
    participant bb_sync as Artifact sync
    participant bb_identity as Identity
    participant bb_git_access as Git access
    participant bb_archive as Archive
    bb_sync->>bb_identity: prove the key
    bb_identity-->>bb_sync: access token
    bb_sync->>bb_git_access: fetch
    bb_git_access-->>bb_sync: new commits
    Note over bb_sync: main moves forward without a merge, or the sync stops and reports own commits
    bb_sync->>bb_archive: follow the changes, optionally for path prefixes
    bb_archive-->>bb_sync: stream open
    Note over bb_sync,bb_archive: after a drop: fetch again, then follow again
```

## A change reaches a local clone

A participant changes a document through the hub. The archive commits it, finds the artifacts that
changed and tells every client that follows the tenant. The sync part of another participant
fetches and moves its main directory forward. A change that came by a push gives the same event
(see the push scenario).

```arc42
:::runtime-scenario
id: rs-change-reaches-clone
title: A change reaches a local clone
trigger: A part of the hub commits a change of an artifact
involves: bb-documents, bb-archive, bb-sync, bb-git-access, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-change-reaches-clone
notation: mermaid-sequence
scenario: rs-change-reaches-clone
aliases: bb_documents=bb-documents, bb_archive=bb-archive, bb_sync=bb-sync, bb_git_access=bb-git-access
:::
```

```mermaid
sequenceDiagram
    participant bb_documents as Documents
    participant bb_archive as Archive
    participant bb_sync as Artifact sync
    participant bb_git_access as Git access
    bb_documents->>bb_archive: commit, author is the participant
    Note over bb_archive: compares the old and the new state of main, keeps artifacts only
    bb_archive-->>bb_sync: an artifact changed, with its new version
    bb_sync->>bb_git_access: fetch
    bb_git_access-->>bb_sync: new commit
    Note over bb_sync: main moves forward without a merge
```

## Participant pushes a change with Git

A participant commits in a worktree and pushes the branch main with plain Git. Git access checks
the token and the tenant before Git reads the upload. Then Git runs the rules of Git access on the
new commits. The artifacts part passes the check of a changed file to the part of the hub that owns its
schema; the diagram shows the records part. For a file that the push changes or removes, Git
access also asks the artifacts part whether the old file is the stub of a staged artifact, and
refuses the push if it is. Git access also asks the artifacts part for the hints of each changed
file, such as a link of a document that leads nowhere in the state after the push, and shows them
to the participant; a hint never refuses the push. When an agent pushes, Git access also asks
the artifacts part whether the old or the new file is one that no agent may change, such as the
definition of a team agent, which Tenants owns, and refuses the push if it is
(`dec-team-agents`). When all of the rules hold, Git moves main in one step, and Git access tells the archive,
which tells the clients that follow the tenant. A push that breaks a rule is refused as a whole and
shows the reason. A push on an old base is refused by Git, and the participant fetches and merges
by hand first.

```arc42
:::runtime-scenario
id: rs-git-push
title: Participant pushes a change with Git
trigger: A participant pushes the branch main of the tenant repository
involves: bb-git-access, bb-identity, bb-artifacts, bb-records, bb-tasks, bb-slides, bb-documents, bb-tenants, bb-archive, bb-sync, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-git-push
notation: mermaid-sequence
scenario: rs-git-push
aliases: actor_human=actor-human, bb_git_access=bb-git-access, bb_identity=bb-identity, bb_artifacts=bb-artifacts, bb_records=bb-records, bb_tenants=bb-tenants, bb_archive=bb-archive, bb_sync=bb-sync
:::
```

```mermaid
sequenceDiagram
    participant actor_human as Human participant
    participant bb_git_access as Git access
    participant bb_identity as Identity
    participant bb_artifacts as Artifacts
    participant bb_records as Records
    participant bb_tenants as Tenants
    participant bb_archive as Archive
    participant bb_sync as Artifact sync
    actor_human->>bb_git_access: git push, token as password
    bb_git_access->>bb_identity: check token and tenant
    bb_git_access->>bb_archive: find the repository of the tenant
    Note over bb_git_access: rules before Git accepts: author is the participant, only main, no rewrite, only artifact files and agent definitions, no agent changes a definition
    bb_git_access->>bb_artifacts: check a changed file with a schema
    bb_artifacts->>bb_records: pass the check to the owner of the schema, here a record type
    bb_records-->>bb_artifacts: valid, or invalid with reasons
    bb_artifacts-->>bb_git_access: the answer of the owner
    bb_git_access->>bb_artifacts: is the old file of a changed or removed path a stub?
    bb_artifacts-->>bb_git_access: the owner of its schema recognises its stub, or not
    bb_git_access->>bb_artifacts: hints for a changed file, links resolve in the new state
    bb_artifacts-->>bb_git_access: hints of the parts, for example a link of a document that leads nowhere
    bb_git_access->>bb_artifacts: for an agent: may no agent change this file?
    bb_artifacts->>bb_tenants: the owner of the schema of a team agent definition says so
    bb_artifacts-->>bb_git_access: refused for an agent, or free
    Note over bb_git_access: Git moves main in one step, or the push is refused with the reason
    bb_git_access->>bb_archive: main moved
    bb_archive-->>bb_records: changed artifacts, the part reloads its files
    bb_archive-->>bb_sync: changed artifacts, to the clients that follow
    bb_git_access-->>actor_human: push accepted, with the hints
```

## A user opens the portal

A user asks for a link with the CLI. The identity part creates a code that works once and returns
the link. The user opens it in a browser. The portal swaps the code for an access token of the
key, keeps the token and shows the start page of the tenant that the link names. Every page
then asks the interfaces of the hub with this token: for example the meeting session for the
sessions that the user may see. A used, expired or unknown code gives an error page.

```arc42
:::runtime-scenario
id: rs-user-opens-portal
title: A user opens the portal
trigger: A user asks for a portal link
involves: bb-cli, bb-client-core, bb-identity, bb-portal, bb-session-service, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-user-opens-portal
notation: mermaid-sequence
scenario: rs-user-opens-portal
aliases: actor_human=actor-human, bb_cli=bb-cli, bb_identity=bb-identity, bb_portal=bb-portal, bb_session_service=bb-session-service
:::
```

```mermaid
sequenceDiagram
    participant actor_human as Human participant
    participant bb_cli as office CLI
    participant bb_identity as Identity
    participant bb_portal as Portal
    participant bb_session_service as Meeting session
    actor_human->>bb_cli: ask for a portal link
    bb_cli->>bb_identity: create portal link
    bb_identity-->>bb_cli: link with a code that works once
    actor_human->>bb_portal: opens the link in a browser
    bb_portal->>bb_identity: exchange the code
    bb_identity-->>bb_portal: access token of the key
    bb_portal->>bb_session_service: list the sessions that I may see, with the token
    bb_session_service-->>bb_portal: sessions with start, end and participants
    bb_portal-->>actor_human: start page of the tenant
```

## A user reads a session in the portal

A user opens a session from the list on the page of a tenant. The portal reads the log of the
session from the session stream with the token of the user. The meeting session answers only who
may read the session (`concept-multi-tenancy`); for everybody else the session does not exist,
and the portal shows that. A sentence off the record comes as a placeholder and does not appear.
Then the portal asks the minutes, the decisions, the notes and the tasks for those of this
session, and renders everything on one page. A running session shows as it was when the page
was opened.

```arc42
:::runtime-scenario
id: rs-user-reads-session
title: A user reads a session in the portal
trigger: A user opens a session from the list of a tenant
involves: bb-portal, bb-session-service, bb-records, bb-tasks, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-user-reads-session
notation: mermaid-sequence
scenario: rs-user-reads-session
aliases: actor_human=actor-human, bb_portal=bb-portal, bb_session_service=bb-session-service, bb_records=bb-records, bb_tasks=bb-tasks
:::
```

```mermaid
sequenceDiagram
    participant actor_human as Human participant
    participant bb_portal as Portal
    participant bb_session_service as Meeting session
    participant bb_records as Records
    participant bb_tasks as Tasks
    actor_human->>bb_portal: opens a session of the list
    bb_portal->>bb_session_service: events of the session, with the token
    bb_session_service-->>bb_portal: events, sentences off the record as placeholders
    bb_portal->>bb_records: minutes, decisions and notes of the session
    bb_records-->>bb_portal: records
    bb_portal->>bb_tasks: tasks of the session
    bb_tasks-->>bb_portal: tasks
    bb_portal-->>actor_human: page with header, minutes, decisions, tasks, notes and transcript
```

## A member searches the wiki in the portal

A member of a tenant types words into the search form of the wiki in the portal. The form works
without scripts: the browser asks the portal for the page of the search. The portal asks the wiki
part for the hits with the token of the user and the tenant of the page. The wiki part answers
only a member of the tenant; for everybody else the wiki of the tenant does not exist. The member
opens a hit. The portal asks the wiki part for the artifact with its links, backlinks, sources and
citations, and the archive for its text. It renders the text, and a link to another artifact of the
wiki leads to the page of that artifact in the portal.

```arc42
:::runtime-scenario
id: rs-member-searches-wiki
title: A member searches the wiki in the portal
trigger: A member sends the search form of the wiki
involves: bb-portal, bb-wiki, bb-archive, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-member-searches-wiki
notation: mermaid-sequence
scenario: rs-member-searches-wiki
aliases: actor_human=actor-human, bb_portal=bb-portal, bb_wiki=bb-wiki, bb_archive=bb-archive
:::
```

```mermaid
sequenceDiagram
    participant actor_human as Human participant
    participant bb_portal as Portal
    participant bb_wiki as Wiki
    participant bb_archive as Archive
    actor_human->>bb_portal: sends the search form with words
    bb_portal->>bb_wiki: search the wiki of the tenant, with the token
    bb_wiki-->>bb_portal: hits, best first
    bb_portal-->>actor_human: page with the hits
    actor_human->>bb_portal: opens a hit
    bb_portal->>bb_wiki: the artifact with its links, backlinks, sources and citations
    bb_wiki-->>bb_portal: artifact and references
    bb_portal->>bb_archive: read the file of the artifact
    bb_archive-->>bb_portal: content
    bb_portal-->>actor_human: page with the rendered text, links to other artifact pages
```

## Human marks a sentence off the record

What is said stays said (`dec-off-the-record`). The human presses a key in the gateway terminal
to mark their own sentence off the record. The CLI sends the mark to the hub. The hub checks that
the caller is the speaker. The log gets a new event that points to the original utterance. Existing lines are never rewritten. From then on, the
interfaces deliver the utterance only as a placeholder without its text, the stage hides it, and
it does not go into minutes, decisions or notes. Agents that already read it must respect the
mark.

```arc42
:::runtime-scenario
id: rs-off-the-record
title: Human marks a sentence off the record
trigger: A key in the gateway terminal
involves: bb-cli, bb-session-service, bb-replicator, bb-stage, bb-participant-client, bb-office-hub
:::
```

## Session ends, the minutes are stored

The moderator ends the session. The agent that keeps the minutes writes their final version
through the session commands. The meeting session stores the last version through the minutes
interface. The records part writes the minutes into the staging area of the session and their stub
into the artifact repository, and commits both before it answers (`concept-session-release`). Only
then does the meeting session write the deadline of the release and log the end. If storing fails,
the session does not end, and the moderator can repeat the command. The replicators stop.
Afterwards every participant reads the stored minutes through the minutes interface and may change
them until the release.

```arc42
:::runtime-scenario
id: rs-session-ends
title: Session ends, the minutes are stored
trigger: The moderator ends the session
involves: bb-mcp, bb-cli, bb-session-service, bb-records, bb-archive, bb-replicator, bb-participant-client, bb-office-hub
:::
```

## A participant writes an artifact during a session

An agent writes a new document in a running session and names the session. The documents part asks
the meeting session whether the agent took part and whether the session is released. The session
is open, so the document goes into the staging area of the session, and its stub into the artifact
repository; the archive records the stub with its version (`concept-session-release`). The
documents part tells the meeting session, which logs the new artifact. The readers of the session
read the document; every other member, and every clone, sees only the stub. A participant who did
not take part is refused.

```arc42
:::runtime-scenario
id: rs-write-in-session
title: A participant writes an artifact during a session
trigger: A participant writes a document that names a running session
involves: bb-mcp, bb-documents, bb-session-service, bb-archive, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-write-in-session
notation: mermaid-sequence
scenario: rs-write-in-session
aliases: actor_agent=actor-agent, bb_mcp=bb-mcp, bb_documents=bb-documents, bb_session=bb-session-service, bb_archive=bb-archive
:::
```

```mermaid
sequenceDiagram
    participant actor_agent as Agent
    participant bb_mcp as MCP server
    participant bb_documents as Documents
    participant bb_session as Meeting session
    participant bb_archive as Archive
    actor_agent->>bb_mcp: write a new document for the session
    bb_mcp->>bb_documents: create the document (path, session)
    bb_documents->>bb_session: took part? released?
    bb_session-->>bb_documents: took part, open
    bb_documents->>bb_archive: write the document into the area of the session
    bb_documents->>bb_archive: write the stub into the artifact repository
    Note over bb_archive: the area records the stub with its version
    bb_documents->>bb_session: notice: new artifact
    bb_documents-->>bb_mcp: the document, with its hints
```

## A human releases a session

A session with humans has ended and waits for its release. A human who took part reads the staged
artifacts in the portal and releases. The meeting session finds a word-for-word quote of a
sentence off the record in a staged file and refuses the release. The human reads the places and
confirms the quotes (`if-session-commands`). The meeting session logs the release, and the archive
moves the area into the artifact repository in one commit with the human as its author
(`concept-session-release`). An agent that tries to release is refused.

```arc42
:::runtime-scenario
id: rs-human-releases
title: A human releases a session
trigger: A human who took part releases an ended session
involves: bb-portal, bb-session-service, bb-archive, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-human-releases
notation: mermaid-sequence
scenario: rs-human-releases
aliases: actor_human=actor-human, bb_portal=bb-portal, bb_session=bb-session-service, bb_archive=bb-archive
:::
```

```mermaid
sequenceDiagram
    participant actor_human as Human participant
    participant bb_portal as Portal
    participant bb_session as Meeting session
    participant bb_archive as Archive
    actor_human->>bb_portal: release the session
    bb_portal->>bb_session: release (command ID)
    bb_session->>bb_archive: read the staged files
    Note over bb_session: a staged file quotes a sentence off the record
    bb_session-->>bb_portal: refused: the places and a token, no event
    bb_portal-->>actor_human: the places of the quotes
    actor_human->>bb_portal: release anyway
    bb_portal->>bb_session: release with the token
    bb_session->>bb_archive: read the staged files
    Note over bb_session: the same quotes, so the token holds
    bb_session->>bb_session: log the release
    bb_session->>bb_archive: release the area, with the human as author
    bb_archive-->>bb_session: release commit
    bb_session-->>bb_portal: released
```

## The deadline passes, also after a restart

The grace period of a session ends, or it ended while the hub was down; then the meeting session
finds the deadline in the staging area at start. It searches the staged files for quotes off the
record. With a quote, it holds the session, and only a human can release it. Without one, it logs
the release and asks the archive to release the area, with the hub as the author. One path no
longer holds its stub: after a crash, a participant pushed another file there. The archive keeps
that file, releases the rest and reports the conflict. A release that a restart cut off is
completed by the next call and never done twice (`if-archive-storage`). The readers of the session
see the kept artifact as not released. A participant creates it again at a new path through the
interface of its type and then discards the kept one; with the last kept artifact, the area goes
(`concept-session-release`).

```arc42
:::runtime-scenario
id: rs-release-at-deadline
title: The deadline passes, also after a restart
trigger: The grace period of a session ends, or the hub starts after it ended
involves: bb-session-service, bb-archive, bb-documents, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-release-at-deadline
notation: mermaid-sequence
scenario: rs-release-at-deadline
aliases: actor_human=actor-human, bb_session=bb-session-service, bb_archive=bb-archive, bb_documents=bb-documents
:::
```

```mermaid
sequenceDiagram
    participant bb_session as Meeting session
    participant bb_archive as Archive
    participant bb_documents as Documents
    participant actor_human as Human participant
    Note over bb_session: at start, read the deadlines from the staging areas
    bb_session->>bb_archive: read the staged files
    Note over bb_session: no quote off the record
    bb_session->>bb_session: log the release, without an actor
    bb_session->>bb_archive: release the area, with the hub as author
    Note over bb_archive: one path holds another file, not the recorded stub
    bb_archive-->>bb_session: released, one conflict with its path
    actor_human->>bb_documents: create the document again at a new path
    actor_human->>bb_documents: discard the kept document
    bb_documents->>bb_archive: discard the kept file
    Note over bb_archive: the last kept file goes, and the area with it
```

## Agent creates a task, a human finishes it

In a meeting, a human asks the agent to note a task for a colleague. The agent creates
a task with a title, the assignee and the session. The tasks part checks the token, writes
the task as one YAML file into the archive and commits it before it answers with the reference of
the task (`dec-yaml-git-storage`). Later the assignee lists the open tasks assigned to them and
sets the status to done. The tasks part rewrites the file in a new commit, so the archive keeps
the history. Both commands carry an ID created by the client, so a repeat after a network drop
changes nothing.

```arc42
:::runtime-scenario
id: rs-task-lifecycle
title: Agent creates a task, a human finishes it
trigger: A human asks the agent in a meeting to note a task
involves: bb-mcp, bb-cli, bb-client-core, bb-tasks, bb-identity, bb-archive, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-task-lifecycle
notation: mermaid-sequence
scenario: rs-task-lifecycle
aliases: bb_mcp=bb-mcp, bb_cli=bb-cli, bb_tasks=bb-tasks, bb_archive=bb-archive
:::
```

```mermaid
sequenceDiagram
    participant bb_mcp as MCP server
    participant bb_cli as office CLI
    participant bb_tasks as Tasks
    participant bb_archive as Archive
    bb_mcp->>bb_tasks: create task (title, assignee, session, command ID)
    bb_tasks->>bb_archive: commit (one YAML file)
    bb_archive-->>bb_tasks: commit ID
    bb_tasks-->>bb_mcp: task with its reference
    bb_cli->>bb_tasks: list tasks (status open, assignee)
    bb_tasks-->>bb_cli: tasks
    bb_cli->>bb_tasks: update the task (status done, command ID)
    bb_tasks->>bb_archive: commit (the file again)
    bb_tasks-->>bb_cli: task, revision 2
```

## A member runs a team agent

A tenant admin has created the team agent "minutes" and a member changed its definition with
Git. Anna, a member, starts the agent runner for a session. She is the agent launcher. The client
core reads the definition and the runner shows it. Anna confirms it, and the runner keeps which
version she confirmed. The client core signs the challenge with her own key, as for any call, and
asks Identity for a token as the team agent, with the confirmed version. Identity checks that she
is a member of the tenant and issues a short-lived token that names the agent, her as the agent
launcher and the version. The harness command, the working directory and the model credentials
come from her own computer. The runner then joins the session as the agent. If Ben already runs
the same team agent in this session, the meeting session refuses the join with "already joined",
and Anna's runner stops. Later Ben changes the definition. At her next start, Anna sees that the
definition has changed and has to confirm it again. A push of the agent itself that changes a
definition is refused.

```arc42
:::runtime-scenario
id: rs-team-agent-run
title: A member runs a team agent
trigger: A member starts the agent runner for a team agent of the tenant
involves: bb-agent-runner, bb-client-core, bb-identity, bb-session-service, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-team-agent-run
notation: mermaid-sequence
scenario: rs-team-agent-run
aliases: bb_agent_runner=bb-agent-runner, bb_client_core=bb-client-core, bb_identity=bb-identity, bb_session_service=bb-session-service
:::
```

```mermaid
sequenceDiagram
    participant bb_agent_runner as Agent runner
    participant bb_client_core as Client core
    participant bb_identity as Identity
    participant bb_session_service as Meeting session
    bb_agent_runner->>bb_client_core: start team agent minutes
    bb_client_core-->>bb_agent_runner: definition and its version
    bb_agent_runner->>bb_agent_runner: show definition, member confirms
    bb_client_core->>bb_identity: token as team agent with the version (signed with the key of the member)
    bb_identity-->>bb_client_core: token for the agent, agent launcher and version named
    bb_agent_runner->>bb_session_service: join session as the agent
    bb_session_service-->>bb_agent_runner: joined, or already joined
```

## A tenant gets a team agent

A tenant admin creates the team agent "minutes" with the CLI. The client core calls the Tenant API,
and Tenants checks that the caller is a tenant admin of the tenant. Tenants asks Identity through
the tenant roster to create the agent: a user of kind agent that belongs to the tenant, has no key
and is no member (`dec-team-agents`). The agent ID is a participant ID of the whole hub, so Identity
refuses an ID that exists or existed. Later a member writes the definition of the agent as a file in
the clone of the tenant and pushes it with Git. Git access checks the token, the tenant and the
author. The artifacts part passes the check of the file to Tenants, which owns the schema of the
definition. A file that breaks the schema, such as a field with a command, refuses the whole push.
When the push is accepted, every member can start the agent.

```arc42
:::runtime-scenario
id: rs-team-agent-create
title: A tenant gets a team agent
trigger: A tenant admin creates a team agent and a member writes its definition
involves: bb-cli, bb-client-core, bb-tenants, bb-identity, bb-git-access, bb-artifacts, bb-sync, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-team-agent-create
notation: mermaid-sequence
scenario: rs-team-agent-create
aliases: bb_cli=bb-cli, bb_client_core=bb-client-core, bb_tenants=bb-tenants, bb_identity=bb-identity, bb_git_access=bb-git-access, bb_artifacts=bb-artifacts
:::
```

```mermaid
sequenceDiagram
    participant bb_cli as office CLI
    participant bb_client_core as Client core
    participant bb_tenants as Tenants
    participant bb_identity as Identity
    participant bb_git_access as Git access
    participant bb_artifacts as Artifacts
    bb_cli->>bb_client_core: team-agent create minutes
    bb_client_core->>bb_tenants: create team agent (tenant, ID, display name, command ID)
    bb_tenants->>bb_identity: the caller is a tenant admin? create the agent in the tenant roster
    bb_identity-->>bb_tenants: created, or refused because the ID is taken
    bb_tenants-->>bb_client_core: created
    bb_client_core-->>bb_cli: write agents/minutes.yaml in the clone
    bb_cli->>bb_git_access: git push of the definition, token as password
    bb_git_access->>bb_artifacts: check the file with its schema
    bb_artifacts->>bb_tenants: the owner of the schema checks the definition
    bb_tenants-->>bb_artifacts: valid, or invalid with reasons
    bb_artifacts-->>bb_git_access: the answer of the owner
    bb_git_access-->>bb_cli: push accepted, or refused with the reason
```

## A team agent runs on a server

A tenant admin has registered a key for the team agent "minutes", and a member has written its
definition. An operator puts the key on a server and starts the runner there, with the version of
the definition that the operator confirms. The client core signs the challenge with the key of the
agent, as for any participant, and gets a token for the agent itself. It reads the definition and
checks that the version is the one that the operator named; the runner stops if it is not. The
agent has no agent launcher, so the runner asks for no token as the agent. The runner joins the
session as the agent, and the join names no agent launcher. If a member already runs the same team
agent in this session with a token as the agent, the meeting session refuses the join with "already
joined". The harness of the agent gets the key, as the harness of a personal agent does.

```arc42
:::runtime-scenario
id: rs-team-agent-server
title: A team agent runs on a server
trigger: An operator starts the runner of a team agent with the key of the agent
involves: bb-agent-runner, bb-client-core, bb-identity, bb-session-service, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-team-agent-server
notation: mermaid-sequence
scenario: rs-team-agent-server
aliases: bb_agent_runner=bb-agent-runner, bb_client_core=bb-client-core, bb_identity=bb-identity, bb_session_service=bb-session-service
:::
```

```mermaid
sequenceDiagram
    participant bb_agent_runner as Agent runner
    participant bb_client_core as Client core
    participant bb_identity as Identity
    participant bb_session_service as Meeting session
    bb_agent_runner->>bb_client_core: start team agent minutes with its own key, version named
    bb_client_core->>bb_identity: prove the key of the agent
    bb_identity-->>bb_client_core: token for the agent, no agent launcher
    bb_client_core-->>bb_agent_runner: definition and its version
    bb_agent_runner->>bb_agent_runner: the version is the one that the operator named, or stop
    bb_agent_runner->>bb_session_service: join session as the agent
    bb_session_service-->>bb_agent_runner: joined, or already joined
```

## A team agent stops

A team agent stops working at once, and without a new key, when one of three things happens: a
tenant admin removes the agent, its agent launcher leaves the tenant, or the key of its agent
launcher is revoked. Identity checks these conditions at every call of the token as the agent. The
next call of the runner or of the harness fails with "unauthenticated". The runner renews the
token of its harness at short intervals. It does not get a new token: asking for one fails for a
removed agent, and for a member who left the tenant, with the answer for an agent that does not
exist. A refusal for good stops the runner with the reason, and the harness stops with it. The
agent stays in the session until it leaves; another member starts it again after that, or at once
in another session.

```arc42
:::runtime-scenario
id: rs-team-agent-stops
title: A team agent stops
trigger: A tenant admin removes a team agent, or its agent launcher leaves the tenant
involves: bb-agent-runner, bb-client-core, bb-tenants, bb-identity, bb-session-service, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-team-agent-stops
notation: mermaid-sequence
scenario: rs-team-agent-stops
aliases: bb_agent_runner=bb-agent-runner, bb_client_core=bb-client-core, bb_tenants=bb-tenants, bb_identity=bb-identity, bb_session_service=bb-session-service
:::
```

```mermaid
sequenceDiagram
    participant bb_agent_runner as Agent runner
    participant bb_client_core as Client core
    participant bb_tenants as Tenants
    participant bb_identity as Identity
    participant bb_session_service as Meeting session
    bb_client_core->>bb_tenants: remove team agent minutes (tenant admin)
    bb_tenants->>bb_identity: end the team agent in the tenant roster
    bb_identity-->>bb_tenants: ended, its record stays
    bb_agent_runner->>bb_session_service: next call with the token of the agent
    bb_session_service->>bb_identity: check token
    bb_identity-->>bb_session_service: refused, the agent is removed
    bb_session_service-->>bb_agent_runner: unauthenticated
    bb_agent_runner->>bb_client_core: ask for a new token as the agent
    bb_client_core->>bb_identity: token as team agent (key of the agent launcher)
    bb_identity-->>bb_client_core: no such team agent
    bb_agent_runner->>bb_agent_runner: stop with the reason
```

## Hub writes sessions to the archive

While sessions run, the meeting session writes every session that changed to the archive, at a
configurable interval and at once when a session ends (`dec-session-persistence`). It takes the
log as it is at that moment and signs the tree head over it. The archive writes the log and the
tree head and commits them as one commit; the push is mocked. Commands and the stream go on
during the commit. Events that arrive during a slow commit go into the next write. There is
never more than one write in progress (`qs-archive-no-backpressure`).

```arc42
:::runtime-scenario
id: rs-archive-session
title: Hub writes sessions to the archive
trigger: The write interval has passed, or a session has ended
involves: bb-session-service, bb-archive, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-archive-session
notation: mermaid-sequence
scenario: rs-archive-session
aliases: bb_session=bb-session-service, bb_archive=bb-archive
:::
```

```mermaid
sequenceDiagram
    participant bb_session as Meeting session
    participant bb_archive as Archive
    Note over bb_session: interval passed or session ended
    bb_session->>bb_session: take the changed sessions, sign their tree heads
    bb_session->>bb_archive: commit (log and tree head of each session)
    Note over bb_session: commands and the stream go on
    bb_archive->>bb_archive: write files, commit, push (mocked)
    bb_archive-->>bb_session: commit ID
    Note over bb_session: the next write takes everything since
```

## Hub restarts and restores a session

The hub crashes while a session runs. Its last write to the archive has the session up to some
event; the events after it are lost on the hub, but the peers still have them. When the hub starts
again, the meeting session reads the stored log of every session that has not ended back from the
archive and checks it against its signed tree head (`dec-restore-from-peers`). An ended session
stays in the archive: the first call that needs it loads and checks it then
(`dec-session-eviction`). A session that has not ended takes no commands yet.
The replicators reconnect with their cursors. The hub does not know these cursors and answers that
they are beyond the end. Each replicator then asks the hub how many events it has and offers the
events after that, with the tree head the hub signed over its copy before the crash
(`dec-signed-head-in-stream`). The hub checks the signature, the root hash and the consistency
with its own log, and appends the missing events with their IDs. The first copy that fits wins;
later copies have no effect. The session takes commands again, the replicators follow the stream
from their cursors, and the IDs continue without a gap. A copy that does not verify or forks
fails, and the replicator stops instead of continuing it.

```arc42
:::runtime-scenario
id: rs-hub-restart
title: Hub restarts and restores a session from the archive and the peers
trigger: The hub starts again after a crash while a session runs
involves: bb-session-service, bb-archive, bb-replicator, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-hub-restart
notation: mermaid-sequence
scenario: rs-hub-restart
aliases: bb_session=bb-session-service, bb_archive=bb-archive, bb_replicator=bb-replicator
:::
```

```mermaid
sequenceDiagram
    participant bb_archive as Archive
    participant bb_session as Meeting session
    participant bb_replicator as Log replicator
    Note over bb_session: hub starts after a crash
    bb_session->>bb_archive: read stored logs and tree heads
    bb_archive-->>bb_session: logs up to the last write
    bb_session->>bb_session: verify each log, wait for peers
    bb_replicator->>bb_session: subscribe(cursor=last line)
    bb_session-->>bb_replicator: cursor beyond the end
    bb_replicator->>bb_session: tree head (how many events?)
    bb_replicator->>bb_session: offer missing events + signed tree head
    bb_session->>bb_session: check signature, root hash, consistency
    bb_session-->>bb_replicator: accepted (a later copy: no effect)
    bb_replicator->>bb_session: tree head at cursor (same root as the local log?)
    bb_replicator->>bb_session: subscribe(cursor=last line)
    bb_session-->>bb_replicator: live events, IDs continue
```

## Agent imports a conversation and writes its minutes

An agent has a conversation from another tool as a file in the import format
(`dec-import-format`). It imports the file through the MCP server. The client core reads the file
and sends the conversation in pages to the session import. The first page creates the session:
the meeting session writes an event that marks the session as imported, the imported
participants and the first utterances. Every further page adds utterances, and the last page
writes the end of the import. The client core returns the session ID and the signed tree head
over the imported log. The hub writes the session to the archive at its next interval, like
every other session. The agent then reads the events after a cursor, joins the session like a
live one, writes the minutes and records decisions and tasks with the session ID. When it ends
the session, the meeting session stores the minutes (`rs-session-ends`).

```arc42
:::runtime-scenario
id: rs-import-conversation
title: Agent imports a conversation and writes its minutes
trigger: An agent calls session.import with a conversation file
involves: bb-mcp, bb-cli, bb-client-core, bb-session-service, bb-records, bb-archive, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-import-conversation
notation: mermaid-sequence
scenario: rs-import-conversation
aliases: actor_agent=actor-agent, bb_mcp=bb-mcp, bb_client_core=bb-client-core, bb_session=bb-session-service, bb_archive=bb-archive, bb_records=bb-records
:::
```

```mermaid
sequenceDiagram
    participant actor_agent as Agent
    participant bb_mcp as MCP server
    participant bb_client_core as Client core
    participant bb_session as Meeting session
    participant bb_archive as Archive
    participant bb_records as Records
    actor_agent->>bb_mcp: import the conversation (file)
    bb_mcp->>bb_client_core: read the import format
    bb_client_core->>bb_session: first page (source, participants, utterances)
    bb_session-->>bb_client_core: events stored, signed tree head
    bb_client_core->>bb_session: last page (complete)
    bb_session-->>bb_client_core: end of import, signed tree head
    bb_mcp-->>actor_agent: session ID, tree head
    bb_session->>bb_archive: write the log (next interval, background)
    actor_agent->>bb_session: events after a cursor, join, minutes
    actor_agent->>bb_records: decisions with the session ID
    actor_agent->>bb_session: end the session
    bb_session->>bb_records: store the minutes
```

## The agent runner checks after each utterance whether to wake an agent

An agent takes part through the agent runner (`dec-agent-runner-acp`). The runner joined the
session with the key of the agent and follows its local log. It started the harness, opened one
ACP session with the office tools and sent a first prompt with the role.

A human talks. Each sentence reaches the local log as a final utterance. The wake strategy that
the user chose decides when to wake the agent (`dec-agent-runner-delivery`). With a model that
the user brings, the runner asks it after each utterance whether the agent should act now, for
example because the speaker addressed it or started a new topic (`dec-wake-check-per-utterance`,
`dec-byo-decision-model`). Without a model, or when the model fails, the runner waits until the
speaker ends the turn. When the agent should act, the runner sends what others said since its
last delivery as one prompt, without the agent's own utterances and sentences off the record. The
agent decides how to act: it speaks through the office tools when the speaker asked it, raises its
hand instead of interrupting, or updates the minutes silently. If the agent is still busy, the
runner queues what comes and sends it together afterwards. When the session ends, the runner
sends a last prompt with what is left and stops the harness.

```arc42
:::runtime-scenario
id: rs-agent-runner-turn
title: The agent runner checks after each utterance whether to wake an agent
trigger: utterance.final of another participant in the local log of the agent
involves: bb-agent-runner, bb-client-core, bb-replicator, bb-mcp, bb-cli, bb-session-service, bb-participant-client, bb-office-hub
:::
```

```arc42
:::diagram
id: seq-agent-runner-turn
notation: mermaid-sequence
scenario: rs-agent-runner-turn
aliases: actor_human=actor-human, actor_agent=actor-agent, actor_forwarding_judge=actor-forwarding-judge, actor_decision_model=actor-decision-model, bb_cli=bb-cli, bb_session=bb-session-service, bb_replicator=bb-replicator, bb_agent_runner=bb-agent-runner, bb_mcp=bb-mcp
:::
```

```mermaid
sequenceDiagram
    participant actor_human as Human
    participant bb_cli as office CLI
    participant bb_session as Meeting session
    participant bb_replicator as Log replicator
    participant bb_agent_runner as Agent runner
    participant actor_forwarding_judge as Forwarding judge
    participant actor_decision_model as Decision model
    participant actor_agent as Agent harness
    participant bb_mcp as MCP server
    actor_human->>bb_cli: speaks or types a question
    bb_cli->>bb_session: submit the utterance
    bb_session-->>bb_replicator: final utterance
    bb_replicator->>bb_replicator: append lines to the local log of the agent
    bb_agent_runner->>bb_replicator: read new lines
    alt wake strategy llm
        bb_agent_runner->>actor_forwarding_judge: new utterances, context, role
        actor_forwarding_judge-->>bb_agent_runner: act, addressed, topic_closed, reason
    else wake strategy decision-model
        bb_agent_runner->>actor_decision_model: state of the meeting, questions
        actor_decision_model-->>bb_agent_runner: probabilities of act and topic_closed, addressed
    else wake strategy turn-end, or the model failed
        Note over bb_agent_runner: waits for the end of the turn of the speaker
    end
    bb_agent_runner->>actor_agent: session/prompt (who said what)
    actor_agent->>bb_mcp: utterance_say (answer)
    bb_mcp->>bb_session: submit the utterance (as the agent)
    actor_agent-->>bb_agent_runner: end of turn
    bb_session-->>bb_replicator: final utterance of the agent
    Note over bb_agent_runner: leaves out the agent's own utterance
```

## System admin creates a tenant and the tenant admin adds a member

A system admin creates a tenant and names an active user as its first tenant admin. Tenants checks
the token (a system admin only) and asks Identity through the tenant roster. Identity applies the
change to a copy of its state, checks all rules and writes the tenant and the first membership as
one commit in the hub repository. Later, the tenant admin adds a user to the tenant. Identity
checks that the caller is a tenant admin of this tenant and that the change keeps all rules. A
user who is no member gets the same answer as for a tenant that does not exist.

```arc42
:::runtime-scenario
id: rs-tenant-setup
title: A system admin creates a tenant, the tenant admin adds a member
trigger: A system admin creates a tenant
involves: bb-office-hub, bb-tenants, bb-identity, bb-archive
:::
```

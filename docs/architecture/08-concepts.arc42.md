# Cross-cutting Concepts

## Participant model

There are only **participants**. Each participant has an identity, a kind (human or agent)
and roles, for example moderator, minute taker or stage control. Rights depend on roles, not on
the kind. The kind stays visible for two reasons: the stage marks agents as agents, and only speech
needs consent to be recorded.

An utterance is an utterance, whatever its channel. Human speech is voice. Typed remarks and an
agent's contributions are text. Later, speech output will voice text utterances, and the model stays the same.

```arc42
:::concept
id: concept-participant-model
title: One participant model
category: domain
:::
```

## Event log with cursor

Each session has an append-only log with exactly one writer, the hub. It gives every event an ID
that grows by one inside the session. So the order is free of conflicts, and the IDs have no
gaps: a participant that sees event 43 after 41 knows that 42 is missing and fetches the events
after its cursor (`dec-events-from-cursor`). The cursor is the last event ID a client has read,
which is also the last line of its local file. Utterances never change and are never removed.
Corrections and off-the-record marks are new events that point to the original. An utterance off
the record keeps its ID; the interfaces deliver a placeholder without its text, so no false gap
appears (`dec-off-the-record`). Partials are not written to the log (`dec-no-partials-in-log`).

**Storage and integrity.** The hub orders the events and is the gateway to the archive: it writes
the log to a Git repository at intervals and at the end, without holding up the session
(`dec-session-persistence`, `dec-yaml-git-storage`). The peers keep copies as their reading
interface, with the same lines as the stored log. The events are the leaves of a Merkle tree, and
the hub signs its tree head; every write to the archive carries the signed head over exactly the
stored log (`dec-merkle-log`). After a crash, the hub restores a session from the archive, and a
peer fills in the rest. Every peer gets a signed tree head over its whole copy through the stream,
and a restored session waits for the peers before it takes commands, so IDs and cursors stay
valid (`dec-restore-from-peers`, `dec-signed-head-in-stream`, `rs-hub-restart`). A placeholder of
an utterance off the record carries the leaf hash of the original, so a copy with placeholders
still verifies. A placeholder can hide a text; it cannot change what the tree proves.

**Imported sessions.** A conversation from outside the office becomes a session through its own
interface (`dec-session-import`). The meeting session writes it like live commands, so the log
keeps one writer and gap-free IDs. The first event marks the session as imported and names its
source. The speakers are imported participants, not registered ones. The tree heads protect an
imported log from later changes, but do not prove what was said. The importer can extend the
session with a longer version of the same conversation until it ends; the log only grows.

**Resuming after a network drop:**

- Readers (replicator, stage) reconnect with their last event ID and receive every event after
  it. Nothing is lost, and nothing arrives twice.
- Writers (ears, CLI, MCP) give every command an ID that they create themselves. If they send a
  command again after a drop, the hub recognises the ID and returns the existing event. This
  includes `join`: a client keeps its join command ID, so after a restart it is still the same
  participant.
- The ears queue utterances locally while offline (text only) and send them in order.

**Event levels.** Events carry what agents need to decide when to act: the end of a sentence, the
change of speaker, a long silence, a participant addressed by name, the actions of participants
(raised hand, updated minutes, focused artifact) and the life cycle of participants and sessions.
The thresholds are guide values, configurable per participant. The ears submit the end of a turn
for their own participant. They hear only their own participant, so they cannot tell a silence of
the whole session. OPEN: which part submits the long-silence event; only the CLI and the MCP
server do, on request.

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

An agent in a harness that works in turns cannot follow a file by itself. The agent runner reads
the file for it and puts what was said into the agent's next prompt (`dec-agent-runner-acp`). The
agent still writes through the tools, and it can still read the file for earlier events.

```arc42
:::concept
id: concept-read-file-write-tools
title: Read from the file, write through tools
category: integration
:::
```

## Transcription at the edge

Each human transcribes only themselves, locally. Raw audio never leaves the device. Only text and
metadata are sent. With a headset, the single ear comes for free. With loudspeakers, a voice print
of the owner keeps it: it stays on the device, and the ears drop the speech of other voices
(`dec-voice-print`). Where voices overlap, the ears mark the utterance (`dec-overlap-hint`). The
room ear (speaker recognition for several people at one microphone) comes later. Voice prints are
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

**Actors and keys.** Every human device and every personal agent is its own actor with its own key
pair. The key pair is created locally. Only the public key leaves the device. An agent does not
act on behalf of a human; it acts as itself. A team agent is the one exception to the key pair
(see below).

**Team agents.** A team agent belongs to a tenant, not to a person, and has no private key
(`dec-team-agents`). Every member of the tenant may run it. A member proves their own key as usual
and asks Identity for a token as the team agent, naming the version of its definition that they
confirmed. Identity checks the membership and issues a short-lived token that names the agent, which
is the actor in every session, the member who starts it, the agent launcher, and the version. The
agent acts as itself, not as the agent launcher. The launcher and the version are recorded in the
token and in the author of what the agent writes, so a session shows who runs the agent and which
definition it follows. A run keeps its version: a change of the definition takes effect at the next
start, after the agent launcher has confirmed it again. The token ends at once when the agent is
removed, when the agent launcher leaves the tenant, or when their key is revoked. No agent changes
the definition of a team agent. A team agent is in a session at most once: another join of the same
team agent to the same session is refused with "already joined". For a run without a human, for
example on a server, a tenant admin registers a key for the agent; it then authenticates like any
participant, works in its tenant as a member who is no tenant admin, and has no agent launcher. A
tenant admin revokes the key like any key, and the agent ends at once.

**First contact.** In the first stage, we trust a new public key because we meet the person. An
admin registers the key by hand.

**Authentication.** Challenge and response: the client signs a challenge from the hub and gets
a short-lived access token that is bound to its key. Every call carries the token.
All connections use TLS.

**Stage.** A browser has no key. Identity creates a read-only link with a token that only allows
viewing the stage of one session.

**Revocation.** Each participant can have several keys (several devices). An admin can remove a
key; tokens bound to it stop working when they expire, at the latest.

**Admins and tenants.** The role of a user in a tenant and the system admin role are described in
`concept-multi-tenancy`.

**Out of scope for the first stage:** acting on behalf of someone else (delegation; a team agent
does not act on behalf of its agent launcher), self-service
enrollment, encryption of stored data (OPEN).

```arc42
:::concept
id: concept-identity
title: Identity and access with local key pairs
category: security
:::
```

## Tenants, repository and roles

The hub serves several **tenants**. A tenant is the unit of separation: one organisation, with one
git repository where it keeps all its information. One hub, one user list, many tenants.

**Users and memberships.** Users are global and registered at the hub first (name and keys). A
tenant admin or a system admin then assigns the user to a tenant. A user can be a member of
several tenants, with one role per membership: `member` or `tenant admin`. A membership names the
user ID and never a key, so rotating a key touches no membership. A team agent is a user of kind
agent without a key that belongs to exactly one tenant and is no member of it
(`concept-identity`). Identity keeps users, keys and
memberships in the hub repository, each in one place. Roles inside a session (moderator, minute
taker) stay as in `concept-participant-model`; they are not tenant roles.

**Admins.** A **system admin** runs the hub: creates tenants and users, names the first tenant
admin of each tenant and other system admins. A **tenant admin** runs one tenant and manages its
members. A system admin never reads the content of a tenant: a system admin is never a member of
a tenant, and a member is never a system admin. A person who does both uses two users with two
different keys. The hub always has at least one system admin, named by the operator at the first
start, and every tenant has at least one tenant admin.

**Who reads a session.** A session is read by everyone who ever joined it, by the participant who
imported it, by the tenant admins of its tenant, and by a stage link of exactly this session. For
every other member of the tenant the session does not exist. This holds for its events, its tree
heads and proofs, its activities and the list of sessions. A participant who joins a running
session reads its history from its join on. The records and other artifacts of a session are read by
every member of the tenant once they are released; until then, only the readers of the session
read them (`concept-session-release`).

**Rules for changes.** Identity is the only writer of users, keys, memberships and system admins.
It refuses a change when the caller may not make it, when the tenant or user does not exist, or
when the change would leave a tenant without a tenant admin or the hub without a system admin.
Removing a user, a membership or a tenant ends its period of validity and keeps its record, so an
ID is never used again and the history shows who was a member when. A compromised key is revoked
by a system admin and stops working in all tenants. Identity checks the same rules when it loads
the hub repository and refuses to start on a violation.

**Repository.** A tenant keeps its artifacts in one git repository, with a directory for each kind
of content; the archive keeps the session logs and what sessions have not released apart from it
(`bb-archive`). The definitions of its team agents are files in the artifact repository
(`dec-team-agents`); they are configuration, not artifacts, and no agent changes them. Every session, record, task and slide deck belongs to exactly one tenant, and the
archive writes to the repositories of that tenant only (`dec-yaml-git-storage`).

**Separation.** Every call of the meeting session, the records, the tasks, the slides and the
stage link names a tenant; a stage link carries the tenant of the user who created it. The hub
keeps one instance of each of these parts for each tenant, and each instance reads and writes
only the repository of its tenant. The hub checks the membership and the role of the caller before
it reads or writes. A caller who is no member gets the same answer as for something that does not
exist. Tenants share no data, and the log of one session is never visible in another tenant.

```arc42
:::concept
id: concept-multi-tenancy
title: Tenants, repository and roles
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
- **An interface belongs to its provider.** Its path lies inside the path of the provider, but
  is never the path of the provider itself: an interface is its own package. The types of an
  interface are part of the interface: every provider defines its own types, even if
  another interface has a similar one (for example the kind of a participant, of an actor and of
  the author of a decision). Consumers map between them.
- **No shared paths.** No two elements of the model share a path: every building block and every
  interface has its own. So the test can tell whether a consumer uses a contract or reaches into
  the building block behind it.
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
- **Interfaces for actors only** (CLI, MCP, stage view, audio input) also have their own path:
  the place where their contract is defined, for example the commands of the CLI or the tools of
  the MCP server. Building blocks do not use them.

```arc42
:::concept
id: concept-module-boundaries
title: Module boundaries follow the building blocks
category: structure
:::
```

## Client configuration

The participant client has many options, and a user starts the same agent or the same meeting
again and again. So the options can live in configuration files (`dec-client-config-file`). The
office CLI reads them; the other parts of the client get the resulting options.

**Two layers.** A user file, next to the key and the certificate of the hub, holds what is
personal. A project file in the folder `.office` of the start directory, next to the vocabulary of
the project (`dec-vocabulary-prompt`), holds what a team shares; a team can check it in, for
example one file per role of an agent. The project file overrides the user file, environment
variables override both, and command-line options override everything.

**Strict and without secrets.** An unknown key is an error and is never ignored. A file names the
environment variable that holds a key, never the key itself, so a project file can be checked in.

**Transparency.** A command shows the effective settings and the source of each value, without
any key.

**The connection to a hub.** The files also hold the address of the hub, the tenant, the
certificate and the path of the private key, as single settings and as named hub profiles
(`dec-hub-profiles`). So one client can work with several hubs. The participant confirms the
fingerprint of a new hub certificate once, before the client stores it.

**Scope.** The agent runner and the connection to a hub.

```arc42
:::concept
id: concept-client-configuration
title: Client configuration in a user file and a project file
category: usability
:::
```

## Wiki of the tenant

All artifacts of a tenant form one wiki (`dec-wiki`). It follows the pattern "LLM wiki": agents
read what the team leaves behind and keep linked pages up to date. Knowledge builds up in those
pages, so an agent does not have to find it again for every question, and a human can follow it
from page to page.

**Three layers.**

- **Sources** are memories: minutes, decisions and notes. They come into being as a side effect of
  a session or of other work. Once released, they never change (`concept-session-release`). A
  correction is a new record that refers to the old one. The transcript of a session is not a
  source: only the readers of the session read it, while every member reads the wiki.
- **Pages** are documents. Humans and agents write and change them. A page has a title. It may also
  have a one-line summary, tags, the sources it draws on, a status (draft, reviewed or outdated),
  an owner and its reviews, each with the reviewer and the day. Without these fields, its first
  heading is its title. A page links to other pages and to sources with plain relative Markdown
  links.
- **The office memory skill** tells agents how to work with the wiki (`if-office-memory-skill`).

**Three operations.** All of them are work of an agent, because the hub has no model
(`con-no-intelligence`).

- **Ingest.** An agent reads a new source and writes or updates the pages that the source touches.
  It links them and cites the source. The released sources that no page cites yet are the queue.
- **Query.** An agent answers a question from the pages, with the artifacts it used. A good answer
  may become a new page.
- **Lint.** An agent looks for contradictions, outdated statements, pages without links to them,
  topics without a page, and broken links. It fixes pages and reports the rest. It never changes a
  source.

**Claims.** A participant who takes on an artifact, for example the minutes or the ingest of a
session, says so in the session, and the others leave it to them. The session log records the
claim, and the first claim counts.

**Links and hints.** A link that leads nowhere never refuses a write. The part that owns the type
reports it as a hint, and a push shows the same hints. A stub counts as a file, so a link to a
staged artifact works.

**Who does what in the hub.**

- The part of each type writes, checks and describes its files: title, summary, tags and links.
- The archive stores every change in one commit and reports it; it knows no content.
- The wiki part keeps a derived view of the released artifacts for each tenant and writes nothing.
- The portal shows that view to humans; agents use their clone and their own tools.

**Example.** During a session, an agent added a note about a new price. The session ends, and a
human who took part releases its minutes and the note. The agent that claimed the ingest in the
session ingests both: it updates the page about the customer, adds the price to the page about
pricing, links both pages, and cites the minutes and the note. Later, a human searches for "price"
in the portal. The search finds the pricing page, its backlinks lead to the customer page, and its
sources lead to the minutes and the note.

```arc42
:::concept
id: concept-wiki
title: Wiki of the tenant
category: domain
:::
```

## Artifacts from a session and their release

This concept describes what a team leaves behind, who may read it at which time, and how it becomes
knowledge of the organization. The goal is to share as much as possible, but nothing that a speaker
took off the record (`dec-off-the-record`). Once an artifact is in the artifact repository, every
member has it in a clone and in history that is never rewritten. So what a session creates waits
for a release in a staging area (`dec-staging-repository`).

**What a team leaves behind.**

| Kind | Examples | Changes after release? |
| --- | --- | --- |
| Session log | every utterance and event of a session | the log grows; it is never shared with the tenant |
| Records | minutes, notes, decisions | never; a correction is a new record |
| Work artifacts | slides, documents | yes, with the version check of their type |

The records are the sources of the wiki, and documents are its pages (`concept-wiki`). There is no
public transcript: a session shares its minutes, if it has any, and the other artifacts it creates.
Tasks are not held: the real task lives in the task tool of the team, so holding its small artifact
would protect nothing.

**Three places, three groups of readers.**

| Place | What lies there | Who reads it |
| --- | --- | --- |
| Session logs | the log of every session, with the original of sentences off the record | the readers of the session (`concept-multi-tenancy`) |
| Staging area | what a session created that is not released yet | the readers of that session, and its stage |
| Artifact repository | everything that is released, and a stub for every staged artifact | every member of the tenant, and every clone |

**Release status.** The meeting session keeps a release status for every session, also for one
that staged nothing: **open** while it runs, **waiting** from its end until its deadline, **held**
after an objection or because of quotes, and **released**.

**Where a write goes.** A write that comes from a session names it, and only a participant of the
session may name it. The part that owns the type asks the meeting session for the release status:

- The artifact is new, or the same session staged it, and the session is not released: the
  staging area of the session.
- The session is released, or the write names no session: the artifact repository. An artifact
  that is created outside a session is therefore released at once.
- A change that names another session, or no session, to an artifact that waits for a release is
  refused, and so is a write when the release status and the staging area disagree.

**The staging area of a session.** The meeting session opens it when the session starts. Every
participant who joins, and the importer, becomes a reader of it; tenant admins read by their role.
Every part that reads a staged artifact checks this. Until the release, every participant may
change or remove a staged artifact, the minutes too, through the interface of its type; a removed
record comes back only as a new record. The same interface discards an artifact that the release
kept back.

**Stubs.** For each staged artifact, a stub with the same name lies in the artifact repository.
Every stub of a type has the same content: the artifact waits for a release. A stub names no session
and no time, and the hub is its author. The part that owns the type owns its stub and recognises it;
the archive records which stub it wrote, but knows no type and no content. The stub keeps the name
free for the release and lets links to the artifact work. No interface and no push may change or
remove a stub. Every member can still see that a session stages artifacts, how many, under which
names and when; an author who fears that a name says too much chooses a neutral one.

**The release.**

1. While the session runs, participants write minutes, notes, decisions and new artifacts. They go
   to the staging area. A meeting without minutes works the same way.
2. The session ends. If no human took part, the hub releases at once. Otherwise the human who ends
   it can release in the same step, or a **grace period** starts: 24 hours unless the tenant sets
   another length.
3. During the grace period, a speaker can still mark a sentence off the record. That mark reaches no
   local copy of the log and no agent, so the speaker also corrects the staged artifacts or asks
   their authors. Any human who took part may release, or object; an objection holds the session
   until a human releases it.
4. Before every release of a session with humans, the meeting session searches the staged files
   for word-for-word quotes of sentences off the record; it searches because it holds the
   originals, and they never leave it. While it finds one, a human must confirm the quotes
   (`if-session-commands`), and a deadline holds the session instead of releasing it. A session
   without humans is released at its end all the same, because nobody could confirm the quotes. A
   false match costs one confirmation; a missed one, such as a paraphrase, leaks a sentence.
5. The archive moves the area into the artifact repository in one commit. The author of the commit
   is the human who released, or the hub when the deadline passed, so the history says for good
   when and by whom each artifact was released. An artifact whose path holds anything but its stub is
   not released; it stays with the readers of the session until a participant discards it
   (`if-archive-storage`).
6. Every member sees the artifacts, and the wiki can ingest them. A record never changes again;
   other artifacts change like any other.

A session is never opened again; a later meeting is a new session (`dec-session-end-milestone`).

**Who releases.** Release and objection are commands of the meeting session. It accepts them only
from a human who took part, also in an imported session (`dec-session-import`). An agent never
releases or objects. An assistant that works with the key of a human counts as that human, so it
acts only when its human tells it to. The humans of a session are trusted: one of them may release
early or lift an objection.

**Off the record over the whole life.**

| When the sentence is marked | What protects it |
| --- | --- |
| During the session | The interfaces show a placeholder; agents are told to keep it out of what they write |
| After the end, before the release | The speaker marks the sentence, corrects the staged artifacts, or objects; a word-for-word quote stops the release until a human confirms it, and holds the session at the deadline |
| After the release | Nothing removes it from clones and history (`risk-retraction-leak`) |

**Gaps.** Five ways skip the staging area and rely on the people and agents who write: a change to
an artifact that is already released, a push during a session, a record that does not name its
session, a task, and what agents write after a release at the end of the session. The memory skill
tells agents to avoid the first three. The agent runner names the session that everything from the
meeting must name, and says that tasks are not held. A release at the end is accepted as a gap: the
team trusts the human who releases to know that agents may still write in their last turn.

**Into the wiki.** Agents ingest only released records. They use what the records say, not what
they heard in the session, because only the readers of a session read it but every member reads
the wiki. A page cites records, never a session.

**Example.** In the weekly meeting, Ada, Ben and an agent take part. The agent keeps the minutes and
adds a note about a new price. Ben says a sentence about a colleague and marks it off the record a
minute later; the agent takes it out of the running minutes. Ada ends the meeting, reads the staged
minutes in the portal and releases; the hub finds no quote of Ben's sentence. The minutes and the
note move into the artifact repository in one commit with Ada as its author. The agent that claimed
the ingest in the meeting updates the pricing page and cites both records.

OPEN: who releases when no human who took part is available, for example tenant admins as a
fallback or a quorum of the humans (#194).

```arc42
:::concept
id: concept-session-release
title: Artifacts from a session and their release
category: data
:::
```

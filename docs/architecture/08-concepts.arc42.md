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
imported log from later changes, but do not prove what was said.

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

**Actors and keys.** Every participant is its own actor with its own key pair: each human
device and each agent. The key pair is created locally. Only the public key leaves the device. An
agent does not act on behalf of a human; it acts as itself.

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

**Out of scope for the first stage:** acting on behalf of someone else (delegation), self-service
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
user ID and never a key, so rotating a key touches no membership. Identity keeps users, keys and
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
session reads its history from its join on. The records of a session (minutes, decisions, notes,
tasks) are read by every member of the tenant. OPEN: whether they follow the session (#15).

**Rules for changes.** Identity is the only writer of users, keys, memberships and system admins.
It refuses a change when the caller may not make it, when the tenant or user does not exist, or
when the change would leave a tenant without a tenant admin or the hub without a system admin.
Removing a user, a membership or a tenant ends its period of validity and keeps its record, so an
ID is never used again and the history shows who was a member when. A compromised key is revoked
by a system admin and stops working in all tenants. Identity checks the same rules when it loads
the hub repository and refuses to start on a violation.

**Repository.** A tenant has exactly one git repository, with a directory for each kind of
content. Every session, record, task and slide deck belongs to exactly one tenant, and the archive
writes to the repository of that tenant only (`dec-yaml-git-storage`).

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

**Scope.** The agent runner and the connection to a hub. OPEN: which other commands get a section
of their own.

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

- **Sources** are memories: minutes, decisions and notes. The transcript of a session is not a
  source: only its participants may read it, while every member reads the wiki. They come into being as a side effect of
  a session or of other work, and they never change. A session that is opened again stores its
  minutes again as a new version. A correction is a new record that refers to
  the old one.
- **Pages** are documents. Humans and agents write and change them. A page has a title, and it may
  have a one-line summary, tags, the sources that it draws on, a status (draft, reviewed or
  outdated), an owner and its reviews. It links to other pages and to
  sources.
- **The skill** tells agents how to work with the wiki: the layers, the front matter, and each
  operation. It ships with the office and is the same for every tenant; agents never change it.
  Rules of a team come from the role of an agent and from the overview pages of the wiki.

**Three operations.** All of them are work of an agent, because the hub has no model
(`con-no-intelligence`).

- **Ingest.** An agent reads a new source, for example the minutes of a session that just ended or
  a new note. It writes or updates the pages that the source touches, links them and cites the
  source. One source may touch many pages. The sources that no page cites yet are the queue. So that two
  agents do not ingest the same source, an agent that takes on the ingest of a session says so in
  the session, and the others leave it to it.
- **Query.** An agent answers a question from the pages, with the artifacts it used. A good answer
  may become a new page.
- **Lint.** An agent looks for contradictions, outdated statements, pages without links to them,
  topics without a page, and broken links. It fixes pages and reports the rest. It never changes a
  source.

**Who does what in the hub.**

- The part of each type writes its files, checks them and describes them: title, summary and
  outgoing links. When it writes a file, it reports links that lead nowhere as hints, but it
  accepts the write.
- The archive stores every change in one commit and reports it. It knows no content.
- The wiki part follows those changes and keeps a derived view for each tenant: the index, links
  and backlinks, broken links, sources without a page, and a search by words. It writes nothing,
  needs no model, and can rebuild the view from the archive at any time.
- The portal (`dec-web-portal`) shows the index, an artifact with its links and backlinks, and the
  search. It reads them through the interface of the wiki part with the token of the user. The stage
  shows only the artifacts of its session.

**Navigation and search.** Agents use their clone and their own tools. Humans use the portal.

**Example.** A session ends, and its minutes are stored. During the session, an agent added a note
about a new price. The agent of the participant who kept the minutes ingests both: it updates the
page about the customer, adds the price to the page about pricing, links both pages, and cites the
minutes and the note. Later, a human searches for "price" in the portal. The search finds the
pricing page, its backlinks lead to the customer page, and its sources lead to the minutes and the
note.

```arc42
:::concept
id: concept-wiki
title: Wiki of the tenant
category: domain
:::
```

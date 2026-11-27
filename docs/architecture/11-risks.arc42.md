# Risks and Technical Debt

## Classified sentences stay with the agent

A sentence that is marked off the record afterwards is already in the local files and in the
context of the agents that read it. It cannot be removed from an agent's context. Off the record is
a rule for the people and agents who write, not a mechanism. If an agent has written the sentence
into a record or a page of the wiki, it is visible to every member of the tenant once released and
stays in the history of every clone.

```arc42
:::risk
id: risk-retraction-leak
title: Sentences marked off the record stay in agent context
severity: high
mitigation: The interfaces hide the text for every later reader (dec-off-the-record), and the release of a session holds what it created and stops at word-for-word quotes until a human confirms them (concept-session-release)
:::
```

## Legal uncertainty about listening in

Consent, retention and how to handle people without a client are not settled yet.

```arc42
:::risk
id: risk-consent
title: Consent and retention not settled
severity: high
mitigation: Joining with the microphone is the consent (dec-consent-by-mic); capture status on the stage; data protection review before first use
:::
```

## Whisper too slow on laptops

Whisper is the real latency risk. In the spike, a small model needed about 0.9 s per sentence on
a CPU, whatever the length of the sentence. The large model was too slow for live use. From the end
of speech to the log it took about 1.4 s. The jitter of the Go audio loop is no problem (p99 about
1 ms), even while Whisper runs.

```arc42
:::risk
id: risk-whisper-latency
title: Local transcription too slow
severity: high
mitigation: Smaller or quantised models; a shorter audio window per sentence; a GPU where available; measure on real participant laptops; if echo cancellation later needs tighter timing, build it as a small Rust or C component behind a clear interface
:::
```

## Agents without a file system

Agents that only use an API cannot follow a local file.

```arc42
:::risk
id: risk-no-filesystem
title: Agents without a shared file system
severity: medium
mitigation: Events after a cursor, polled by the agent (dec-events-from-cursor)
:::
```

## The agent harnesses and ACP change fast

The Agent Client Protocol is young, and the harnesses implement it in different ways and versions.
Claude Code needs an adapter that is a separate package. Harnesses name tools differently in their
permission requests, so a rule that allows only the office tools can miss or block a tool. The
office cannot test real harnesses in its CI, because they need a model and a key.

```arc42
:::risk
id: risk-harness-drift
title: Agent harnesses and ACP change and differ
severity: medium
mitigation: The runner uses only the stable core of ACP (initialize, one session with MCP servers, prompt, cancel, permission requests) and tests it against a test double; the user guide names the tested harness commands; the permission rule is configurable (dec-agent-runner-delivery)
:::
```

## Waking the agent costs time and money

The runner checks after each utterance whether to wake the agent. If it wakes a harness with a
large model too often, the agent spends tokens on utterances that do not concern it, and it may
lag behind a fast meeting. The check itself also runs for every utterance.

```arc42
:::risk
id: risk-agent-wakeups
title: Checking and waking the agent per utterance is slow and costly
severity: medium
mitigation: A small, fast forwarding judge or decision model that the user brings makes the check and wakes the agent only when it should act (dec-byo-decision-model); a model that fails falls back to the change of speaker instead of waking the agent at every utterance and is asked again at most once per change of speaker, and queued utterances go out together in one prompt (dec-agent-runner-delivery)
:::
```

## The API of a decision model changes

The wake strategy `decision-model` uses the System One API. One vendor defines it, and another
vendor states that its model is compatible. The API is young; a change of the request, the
answer or the envelope of a platform would make every check fail. The runner was tested only
against a test double, not against a real endpoint.

```arc42
:::risk
id: risk-decision-model-api
title: The System One API of a decision model changes or differs between vendors
severity: low
mitigation: The runner checks every answer against the contract, accepts the bare answer and the envelope of the platform, and falls back to the change of speaker when checks fail (dec-agent-runner-delivery); a real run against Jev and Clef is planned (issue 87)
:::
```

## Loudspeakers instead of a headset

If someone uses loudspeakers, other people's voices end up in their transcript, and sentences
appear twice. A voice print removes most of this. It cannot separate voices that speak at the same
time: the louder voice wins, so words of the owner can be missing or words of someone else can
stay in.

```arc42
:::risk
id: risk-speaker-mode
title: Echo and duplicates with loudspeakers
severity: medium
mitigation: Without a headset the ears need a voice print that drops sentences of other voices (con-own-voice-only, dec-voice-print); utterances that probably hold another voice carry a hint for agents (dec-overlap-hint)
:::
```

## Maturity of Connect RPC

Connect RPC is a CNCF sandbox project. connect-go is stable. connect-python is still 0.x and was
renamed recently. connect-rust became an official
implementation in 2026-08; it passes the full conformance suite but is still 0.x. Few large
adopters are publicly known.

```arc42
:::risk
id: risk-connect-maturity
title: Connect RPC is not equally mature in all languages
severity: medium
mitigation: Use Go with the stable connect-go for all parts (dec-languages); keep the protocol code in one module of the shared core; the protocol is plain HTTP with JSON, so we could replace the library if needed
:::
```

## A changed session in the repository or in a peer copy

Someone with access to the Git repository could change a stored session, and a peer could change
the copy it supplies after a crash.

```arc42
:::risk
id: risk-forged-copy
title: Forged session copies
severity: high
mitigation: Merkle tree with tree heads signed by the hub; the hub checks every peer copy, and every reader can check the repository (dec-merkle-log)
:::
```

## Writing to Git is slow

A Git commit takes noticeable time. If the session waited for it, every command would be slow, and
a slow repository could build up a queue.

```arc42
:::risk
id: risk-archive-latency
title: Slow writes to the archive hold up the session
severity: medium
mitigation: Writes run in the background with a configurable interval; at most one write in progress; later events go into the next write (dec-session-persistence)
:::
```

## An imported conversation is taken for a live one

The hub did not see an imported conversation happen. Its messages may be incomplete or made up,
and the hub cannot check who said them. A reader who takes an imported session for a live one
trusts it more than it deserves.

```arc42
:::risk
id: risk-imported-conversation
title: An imported conversation is taken for a live session
severity: medium
mitigation: An imported session is marked by its first event, with its source and the participant who imported it; imported speakers have marked IDs; the signature over an imported log only proves who imported it and that it has not changed (dec-session-import); the stage names the source of an imported session and never shows it as live
:::
```

## A push goes around the rules of the types

A participant can write an artifact with Git and so does not pass the interface of its type. If
the rules of Git access differ from the rules of the interface, one way accepts what the other
refuses, or the hub shows an artifact that its type cannot read.

```arc42
:::risk
id: risk-push-bypass
title: A push goes around the rules of the types
severity: medium
mitigation: Every push passes the rules of Git access: author, ref, file types and the schema that a file names; the part that owns a type uses one check of its schemas for its own interface and for pushes (dec-artifact-sync)
:::
```

## A large push holds up the hub

A push can carry a large upload. Git reads it into the repository before the rules of Git access
run, so a participant, or a faulty tool, can use up disk space or slow the hub down.

```arc42
:::risk
id: risk-push-size
title: A large push holds up the hub
severity: medium
mitigation: Only participants of the tenant can push, and the access token lives one hour; a size limit for a push is still to be set (dec-artifact-sync)
:::
```

## The author of a pushed commit has no signature

The hub checks the author of every pushed commit against the access token. The commit itself carries
no signature. A copy of the repository outside the hub cannot prove who wrote a commit. Signed
commits with the key of the participant would close this. A login over SSH was left out.

```arc42
:::risk
id: risk-unsigned-commits
title: The author of a pushed commit has no signature
severity: low
mitigation: The hub accepts a push only for the participant that the token names, and the token is bound to the key; signed commits are a later option (dec-artifact-sync)
:::
```

## A push makes the memory of a part stale

The records, tasks and slides parts read their files from the archive when the hub starts and
answer reads from memory. The archive tells them after every change of main, also after a push
(`if-archive-watch`), and they reload. In the short time between a push and the reload, a part
answers from the old state, and a part that allocates IDs in memory may choose an ID that a pushed
file already uses; it then reloads and takes the next free ID.

```arc42
:::risk
id: risk-stale-memory
title: A push makes the memory of a part stale
severity: low
mitigation: The archive tells the parts after every change of main and they reload; a part never overwrites a file that a push put there first, it reloads and takes the next free ID (dec-artifact-sync)
:::
```

## Memory grows with all sessions of all tenants

The session log stays in memory after it is archived, and the restore loads every session at start.
With several tenants, memory grows with the sum of all sessions, and one busy tenant can take the
memory of the others. An ended session that is in the archive and that nobody reads leaves memory;
a later read loads it from the archive (`dec-session-eviction`). The restore at start
loads only the sessions that have not ended.

```arc42
:::risk
id: risk-session-memory
title: Memory grows with all sessions of all tenants
severity: medium
mitigation: Take ended sessions that nobody follows out of memory and load them from the archive on demand; restore only open sessions at start
:::
```

## A member changes a team agent that another member runs

Every member of a tenant may change the definition of a team agent (`dec-team-agents`). A change
of the role or of the tools it may use steers an agent on the computer of another member, and the
agent acts with that member's files and credentials in reach. A definition holds no command, so it
cannot start a program by itself, but a changed role can still lead an agent to misuse the tools it
may use.

```arc42
:::risk
id: risk-shared-agent-definition
title: A changed definition steers an agent that another member runs
severity: medium
mitigation: A client shows a definition and asks for confirmation before the first start and again whenever the definition has changed since, and shows added tools first; the confirmation protects the agent launcher and is no check of the hub; a run keeps its confirmed version, and the author of what the agent writes names it; no agent changes a definition, so a prompt-injected agent cannot rewrite itself or another agent; every change is one commit with its author; the harness, the working directory and the credentials of a model stay local (dec-team-agents)
:::
```

## The key of a team agent ties the agent to no person

A team agent with a key (`dec-team-agents`) runs without an agent launcher. Whoever holds the key
runs the agent, and what the agent writes does not name a person. The key lies on a server, and
anyone who can read it can start the agent with the role of its definition and act as a member of
the tenant who is no tenant admin.

```arc42
:::risk
id: risk-team-agent-key
title: The key of a team agent ties the agent to no person
severity: medium
mitigation: Only a tenant admin or a system admin registers or revokes the key, and revoking ends the agent at once; the runner needs the version of the definition that the operator names, so a change of the definition does not steer an unattended agent; the agent is no tenant admin, starts no agent, manages no key and changes no definition (dec-team-agents); the key stays in a file that only the operator of the server reads (concept-identity)
:::
```

## The wiki view grows with the tenant

The wiki part keeps a derived view of each tenant: the index, the links, the backlinks and the
search (`concept-wiki`). The decision for a derived view in the hub causes this risk (`dec-wiki`). It builds the view from the archive at start and holds it while the hub
runs. A tenant with many artifacts needs a lot of memory for it, and several large tenants share
one hub. It is not measured yet how much memory the view needs for a large tenant.

```arc42
:::ignore H007 dec-wiki causes this risk and no decision mitigates it yet; the mitigation is accepted until a measurement
:::
:::risk
id: risk-wiki-memory
title: The wiki view grows with the tenant
severity: low
mitigation: Accepted until a measurement with a large tenant shows the need; the view can be rebuilt from the archive at any time, so it can move to other storage without a change of the wiki interface
:::
```

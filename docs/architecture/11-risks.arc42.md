# Risks and Technical Debt

## Classified sentences stay with the agent

A sentence that is marked off the record afterwards is already in the local files and in the
context of the agents that read it. It cannot be removed from an agent's context.

```arc42
:::risk
id: risk-retraction-leak
title: Sentences marked off the record stay in agent context
severity: high
mitigation: The interfaces suppress the text of sentences off the record for every later reader (dec-off-the-record); the agent contract requires respecting the mark
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

## Loudspeakers instead of a headset

If someone uses loudspeakers, other people's voices end up in their transcript, and sentences
appear twice.

```arc42
:::risk
id: risk-speaker-mode
title: Echo and duplicates with loudspeakers
severity: medium
mitigation: Headset required (con-headset-only); echo cancellation, voice print and deduplication in issue #1
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

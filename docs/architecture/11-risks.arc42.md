# Risks and Technical Debt

## Classified sentences stay with the agent

A sentence that is marked off the record afterwards is already in the local files and in the
context of the agents that read it. It cannot be removed from an agent's context.

```arc42
:::risk
id: risk-retraction-leak
title: Sentences marked off the record stay in agent context
severity: high
mitigation: The interfaces suppress classified text for every later reader (dec-off-the-record); the agent contract requires respecting the classification
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

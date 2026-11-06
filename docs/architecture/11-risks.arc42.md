# Risks and Technical Debt

## Retracted sentences stay with the agent

A retracted sentence is already in the local files and in the context of the agents. It cannot be
removed from an agent's context.

```arc42
:::risk
id: risk-retraction-leak
title: Retracted sentences stay in files and in agent context
severity: high
mitigation: Optional hold period before publishing; clean up local files when the session ends; agent contract to respect retracted events
:::
```

## Legal uncertainty about listening in

Consent, retention and how to handle people without a client are not settled yet.

```arc42
:::risk
id: risk-consent
title: Consent and retention not settled
severity: high
mitigation: Capture only participants with a client; show capture status on the stage; data protection review before first use
:::
```

## Whisper too slow on laptops

Local Whisper without a GPU may be too slow for real time.

```arc42
:::risk
id: risk-whisper-latency
title: Local transcription too slow
severity: medium
mitigation: Smaller models, whisper.cpp with quantisation; measure before choosing hardware
:::
```

## Agents without a file system

Agents that only use an API cannot follow a local file.

```arc42
:::risk
id: risk-no-filesystem
title: Agents without a shared file system
severity: medium
mitigation: Long-poll tool meeting.wait as a fallback
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
renamed recently. The stable status of connect-es v2 must be checked. There is no official Rust
implementation. Few large adopters are publicly known.

```arc42
:::risk
id: risk-connect-maturity
title: Connect RPC is not equally mature in all languages
severity: medium
mitigation: Use Go (stable connect-go) for server and client; check connect-es v2 before the stage is built; the protocol is plain HTTP with JSON, so a client can speak it without a library if needed
:::
```

# Quality Requirements

Four quality goals drive the architecture, in this order: confidentiality, then performance, then
resumability, then usability. When two goals conflict, the one higher in the list wins. Properties
like "any agent can connect" are product features, not quality goals. They are covered by the
solution strategy and the decisions.

## Confidentiality

Speech needs special protection (ISO 25010: security, confidentiality). Raw audio must not leave
the speaker's device, and only people who consented are captured.

```arc42
:::quality-goal
id: qg-confidentiality
title: Confidentiality
priority: high
:::
```

## Performance

An agent must be able to react within the natural pause after a contribution (ISO 25010:
performance efficiency, time behaviour). For this, speech must reach the session log without
noticeable delay.

```arc42
:::quality-goal
id: qg-performance
title: Performance (latency)
priority: high
:::
```

## Resumability

Networks drop, especially on laptops in meetings (ISO 25010: reliability, recoverability). After a
drop, every client continues where it stopped. No event is lost, and no event is stored or
delivered twice.

```arc42
:::quality-goal
id: qg-resumability
title: Resumability after network drops
priority: high
:::
```

## Usability

Participants see at all times what was captured and sent from them, and they can act on it
(ISO 25010: usability, here mainly transparency and control).

```arc42
:::quality-goal
id: qg-usability
title: Usability (transparency of capture)
priority: high
:::
```

## No raw audio leaves the device

This is the acceptance test for confidentiality. In a test meeting, a participant speaks with the
ears switched on. We record the outgoing network traffic of their client, for example with a proxy
or a packet capture. We check that it contains only text events and no audio data.

```arc42
:::quality-scenario
id: qs-no-raw-audio
title: No raw audio in outgoing traffic
quality: qg-confidentiality
stimulus: A participant speaks for 30 minutes with --mic while outgoing traffic is recorded
response: The client sends only text events and metadata
metric: 0 bytes of audio data in the capture
:::
```

## From transcription to the session log

We measure from the end of the transcription of a sentence (Whisper returns the final utterance)
to the moment the event is in the local copy of the log at another participant. That is what an
agent actually sees. The time for voice activity detection and Whisper is not included; the next
scenario covers it.

```arc42
:::quality-scenario
id: qs-transcript-to-log
title: End of transcription to event in the log
quality: qg-performance
stimulus: Whisper returns a final utterance
response: utterance.final is in the local log of another participant
metric: < 200 ms (OPEN: p95 or maximum)
:::
```

## From end of speech to the log

This is what a participant experiences: the time from the end of a spoken sentence until the
sentence is in the local log of another participant. It includes the pause that ends a sentence,
Whisper and the way into the log. The reference hardware is the CPU-only host of the first stage.

```arc42
:::quality-scenario
id: qs-speech-to-log
title: End of speech to event in the local log
quality: qg-performance
stimulus: A participant with --mic finishes a sentence
response: utterance.final is in the local log of another participant
metric: p95 ≤ 1.5 s on the reference hardware
:::
```

## Audio capture with a real headset

The automated tests use a WAV file. This manual test checks real audio capture and recognition
quality (see `concept-test-strategy`).

```arc42
:::quality-scenario
id: qs-manual-headset
title: Manual test with a real headset
quality: qg-performance
stimulus: A person joins with --mic on a participant laptop with a headset and speaks a few sentences
response: Each sentence appears as utterance.final with readable text; no audio leaves the device
metric: All spoken sentences arrive; recognition is good enough to follow the meeting (OPEN: define a word error rate)
:::
```

## Slow archive does not slow the session

The hub writes to the archive in the background. Even with a slow Git repository, speech reaches
the local log of the other participants as fast as without it.

```arc42
:::quality-scenario
id: qs-archive-no-backpressure
title: A slow archive does not delay the session
quality: qg-performance
stimulus: Every write to the archive takes 5 s (OPEN) while participants speak
response: Commands and the stream run as usual; later events go into the next write
metric: qs-transcript-to-log still < 200 ms; no queue of writes builds up
:::
```

## Resume after a network drop

A participant's network drops in the middle of a meeting and comes back. While offline, the person
keeps speaking. After the connection is back, the local log is complete again, and the queued
utterances are in the session log in the right order.

```arc42
:::quality-scenario
id: qs-resume-after-drop
title: Resume after a network drop
quality: qg-resumability
stimulus: The network of a participant with --mic drops for 60 s (OPEN) and comes back
response: Queued utterances are stored in order; the local log receives all missed events
metric: 0 lost and 0 duplicate events; local log complete within 2 s after reconnect (OPEN)
:::
```

## Speaker sees what was sent

After every sentence, the speaker sees its status in the terminal.

```arc42
:::quality-scenario
id: qs-sender-sees-status
title: Status of own sentences is visible
quality: qg-usability
stimulus: A sentence is sent or marked off the record
response: The terminal shows the status; the stage shows the capture status
metric: Status visible ≤ 1 s after the server confirms it (OPEN)
:::
```

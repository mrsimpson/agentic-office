# Test data of the voice print

Synthetic speech, made with Piper TTS, for `voiceprint_test.go`. The voices are
`de_DE-thorsten-medium` (the owner) and `de_DE-kerstin-low` (the other person); the datasets of
both are CC0.

- `owner-enrol.wav`: the owner reads three sentences (16 s, 16 kHz mono, 16-bit PCM). This is what
  "office voice enrol --wav" makes the voice print from.
- `meeting.wav`: five sentences in turns, with 1.4 s of silence after each. They are in
  `meeting.txt`. The owner's sentences are close to the microphone, with a little room. The
  other person's sentences come from a laptop loudspeaker: band-limited, slightly distorted, with
  a room tail, a short delay and 6 dB quieter than the owner. A noise floor lies under everything.

Synthetic voices are more uniform than real people, and the room is simulated. The test shows
that the pieces work together; it does not say how accurate the voice print is with real people.
Judge that with real recordings (`docs/testing/manual-microphone-test.md`).

- `overlap.wav`: one sentence of the owner, and a sentence of the other person that starts 1.5 s
  before the owner ends. The pause between them is too short for the VAD to cut, so the ears see
  one stretch of speech with two voices in it (`overlap.txt`).

# Test data

`german-3-sentences.wav` contains the three sentences in `german-3-sentences.txt`, spoken by a
synthetic voice, with 1.2 s of silence between them (16-bit PCM, mono, 22.05 kHz).

How it was made:

- Speech: Piper TTS with the voice `de_DE-thorsten-medium`. The Thorsten dataset is CC0.
- Each sentence was synthesized on its own, then the parts were joined with silence, so the VAD
  has real pauses to find.

Synthetic speech is cleaner than a headset in a meeting. Use real recordings before you judge
Whisper accuracy.

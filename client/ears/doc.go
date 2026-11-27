// Package ears is the building block Ears (bb-ears in docs/architecture/05-building-blocks.arc42.md).
//
// It turns the participant's own speech into utterances with local Whisper
// (concept-edge-transcription, dec-local-single-ear). Only text and metadata leave the device.
// The pipeline is audio source, VAD, Whisper, local queue, SubmitUtterance with modality voice.
//
//   - Audio: the audio input (if-audio-in, package audio): the microphone through a recorder
//     program that runs as the user (parec, arecord, rec or ffmpeg; OFFICE_MIC_COMMAND overrides
//     it), or a WAV file played in real time. Both deliver 20 ms frames of 16 kHz mono audio.
//   - VAD: the Silero model of whisper.cpp gives a speech probability for every 32 ms window.
//     A sentence ends after EndSilence (default 500 ms) without speech. See vad.go for why the
//     streaming API is used instead of whisper.cpp's segment API.
//   - Voice print (package speaker, dec-voice-print): with a voice print of the owner, each sentence
//     goes through a speaker model (WeSpeaker ResNet34, plain Go, no cgo) before Whisper. The model
//     scores windows of 1.5 s against the print; only the stretches that score 0.50 or more go on.
//     The ears set the overlap hint on the sentence when other speech was dropped next to the kept
//     stretches or the kept stretches are doubtful (below 0.60), see dec-overlap-hint. The print is a
//     file that only the user can read (DefaultVoicePrintFile); Enrol makes it from speech of the
//     owner. Without a print, the ears need a headset and set no hint. Config.Debug reports the
//     scores of every sentence (type gate).
//   - Whisper: one whisper.cpp context transcribes one sentence after the other. Model, language
//     and threads are configurable (Config).
//   - Queue: when Whisper has the text, the ears create the command ID, report the status final
//     and put the utterance in an ordered, in-memory queue. One sender submits the queue in
//     order. After a network drop it repeats the same command ID, so nothing is lost or stored
//     twice (dec-idempotent-commands). Raw audio is never put in the queue.
//   - Turn: after TurnSilence (default 1.8 s) without speech after the last sentence, the ears
//     put a turn.end of their participant in the same queue, behind the sentences of the turn.
//     The sender submits it with SubmitTurnEnd under its own command ID. There is one turn.end
//     per turn: the next one comes only after a new sentence. A turn with no text (Whisper heard
//     nothing) has no turn.end. The ears do not send silence.long: that is a silence of the whole
//     session, and the ears hear only their own participant.
//   - Pause: while Paused() is true, the microphone recorder is stopped (a WAV file plays on, but
//     its frames are dropped), audio that was heard before the pause is forgotten, and no
//     sentence is transcribed. A pause also ends the open turn without a turn.end, and no
//     turn.end is submitted while paused. Each stage and a ticker count the pauses, because a
//     source drops its frames while paused: the VAD may see no frame of a pause at all.
//
// Vocabulary: before each sentence, Whisper gets a prompt with the terms of the vocabulary file
// and the names of the participants (vocabulary.go). ExtractVocabulary builds such a file from
// the documents of a project with rules and a NER model (extract.go, package ner).
//
// Report receives one Report per step with the measurements: Whisper time per sentence and the
// time from the end of speech to the text (final) and to the confirmed submit (sent).
//
// whisper.cpp is linked through cgo (see the Makefile and README.md).
package ears

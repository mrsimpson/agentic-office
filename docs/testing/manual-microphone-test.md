# Manual microphone test (`qs-manual-microphone`)

This checklist is for the person who tests the ears with a real microphone. The automated tests
play a synthetic WAV file and use a mock recorder (issue #18, label `mocked`). Only this test
checks real audio capture and recognition quality on a participant laptop.

Plan about 30 minutes. You need a second person or a second terminal to watch the meeting.

## What you need

- A participant laptop (Linux or macOS). Run the test twice: once with a **headset**, once with the
  laptop speakers and a voice print (`office voice enrol`, see `con-own-voice-only`). The ears have
  no echo cancellation, so without a headset and without a voice print the test is not valid.
- For the run with loudspeakers: a second person who speaks in the meeting, or a second device that
  plays a recording of another voice next to the laptop. Write down the volume and the distance.
- A recorder program for your user. On Linux: `parec` (package `pulseaudio-utils`, also for
  PipeWire), `arecord` (`alsa-utils`) or `rec` (`sox`). On macOS: `rec` (SoX) or `ffmpeg`.
- Go, make, git, a C/C++ compiler and cmake (see "Setup" in the README).
- Access to an office hub, and a registered key for the laptop user. An admin registers it
  (see "Create a tenant and register the participants" in the user guide).
- A second participant who follows the log: another person, or a second terminal on the same
  laptop with its own key (for example an agent key called `observer`).

## Setup on the laptop

1. Get the code and build it. This builds whisper.cpp and downloads the models (about 500 MB):

   ```sh
   git clone https://github.com/mrsimpson/agentic-office.git && cd agentic-office
   make whisper models build
   ```

2. Point the CLI to the hub and create your key:

   ```sh
   export OFFICE_HUB=https://<hub>:8740
   export OFFICE_CA_CERT=<path to the hub certificate, if it is self-signed>
   bin/office key create          # send the printed public key to an admin
   ```

3. Check the headset is the default input. On Linux: `pactl info | grep 'Default Source'`. To use
   another device, set `OFFICE_MIC_COMMAND`, for example
   `OFFICE_MIC_COMMAND='parec --raw --rate=16000 --channels=1 --format=s16le --device=<source>'`.
4. Write down the laptop: model, CPU, operating system, headset model, recorder program.

## Steps

Use a new session name, for example `headset-<date>`.

1. **Observer joins.** In the observer terminal:

   ```sh
   LOG=$(bin/office meeting join headset-<date>)
   tail -f "$LOG" | grep --line-buffered '"type":"utterance.final"'
   ```

   Optional: open the stage with `bin/office stage link` in a browser.

2. **Tester joins with the microphone.**

   ```sh
   bin/office meeting join headset-<date> --mic 2> ears.log
   ```

   Expect `listening` in `ears.log`. If join fails, note the error. It must fail before the
   join, so the stage must not show you as captured.

3. **Speak the three test sentences.** Speak normally, with a short pause after each one:
   - "Guten Morgen zusammen."
   - "Wir besprechen heute den Businessplan für das neue Büro."
   - "Bitte schickt mir die Zahlen bis Freitag."

4. **Speak five free sentences** as you would in a meeting, for example about the weather, a
   date, a number and a name. Write down what you said.

5. **Pause.** Press `p`. The view shows `-- paused`. Speak one sentence. Press `p` again. The
   view shows `-- resumed`.

6. **Mark off the record.** Speak one more sentence, wait for `sent`, then press `o`.

7. **Quit.** Press `q`. Then run `bin/office meeting leave`.

## What to observe

Mark each item as pass or fail.

| # | Check | How to see it |
|---|---|---|
| 1 | Every spoken sentence appears as `final`, then `sent` | the gateway view of the tester |
| 2 | Every sentence reaches the observer, in the order spoken | the observer's `tail` |
| 3 | The text is good enough to follow the meeting | compare with what you said |
| 4 | `sent` comes about 1 to 2 s after you stop speaking | `ears: sent … ms after the end of speech` in `ears.log` |
| 5 | Nothing said while paused appears anywhere | view and observer log |
| 6 | Pause closes the microphone | the system microphone indicator goes off; on Linux, `pactl list short source-outputs` has no `parec` entry while paused |
| 7 | The stage shows `captured`, `paused` and `left` at the right moments | stage page (optional) |
| 8 | The sentence off the record shows `off_the_record` in the view and disappears from the stage | view and stage |
| 9 | No sentence was merged with another one or split into two | compare with what you said |

That no audio leaves the device is checked automatically by `qs-no-raw-audio`
(`tests/e2e/quality_test.go`). You do not need to capture traffic by hand.

## Record the results

Post the results as a comment on issue #18, or on a new issue that links to #18 and #13. Use this
template:

```text
Laptop: <model, CPU, OS>   Headset: <model, or loudspeakers + voice print>   Recorder: <parec|arecord|rec|ffmpeg>
office version: <bin/office version>   Model: ggml-small   Threads: <from ears.log>

Checks 1–9: <pass/fail each, with a note for each fail>

Sentence | said | transcribed | word errors | end of speech → sent (ms)
1 | Guten Morgen zusammen. | … | … | …
…

Total: <words said>, <word errors>   Word error rate: <errors / words>
```

**Count word errors** like this: a word that is wrong, missing or added counts as one error.
Ignore case and punctuation. The word error rate is the number of errors divided by the number of
words you said. The target for it is still open (`OPEN` in `qs-manual-microphone`); the test records
it so that a target can be chosen.

Attach `ears.log` if a check fails.

## How this relates to issue #18

Issue #18 (`mocked`) says that no automated test uses a real microphone. This manual test is the
real thing that #18 asks for. When it passed once on a Linux laptop with PulseAudio or PipeWire,
post the results on #18. Close #18 only if the results show no problem. Open a new issue for
each failed check, and link it to #18.

## Run with loudspeakers and a voice print

Repeat the checks with the laptop speakers, after `bin/office voice enrol`. Join with `--debug`
and keep the `ears.log`: its `gate` lines hold the similarity of every window to your voice print.

While another person speaks through the loudspeakers (or a recording plays), say the test
sentences. Write down:

- every sentence of the other person that appears under your name (false accepts);
- every sentence of yours that is missing or cut (false drops);
- for each sentence with the `overlap` hint: was another voice really in it?
- the `gate` lines of each false accept and each false drop (best match and windows).

These numbers decide the open accuracy question of `dec-voice-print`. Do the same once with the
other person and you speaking at the same time.

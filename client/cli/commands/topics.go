package commands

import (
	"sort"
	"strings"
)

// Topic returns the text of a help topic: "office help <topic>".
func Topic(name string) (string, bool) {
	t, ok := topics[name]
	return t, ok
}

// TopicNames returns the names of the help topics, sorted.
func TopicNames() []string {
	names := make([]string, 0, len(topics))
	for n := range topics {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// topics are the help texts that belong to no single command.
var topics = map[string]string{
	"config": strings.TrimLeft(`
Configuration: flags, variables and files

Every command takes these flags. An environment variable sets the default of its flag; the
file key sets it below that. <config> is $XDG_CONFIG_HOME if it is set to an absolute path,
else ~/.config on Linux and ~/Library/Application Support on macOS.

  Flag            Variable             File key       Default
  --hub-profile   OFFICE_HUB_PROFILE   hub-profile    none; names a hub profile that sets the four values below
  --hub           OFFICE_HUB           hub            https://127.0.0.1:8740
  --ca-cert       OFFICE_CA_CERT       ca-cert        <config>/agentic-office/client/hub.crt if it exists (then
                                                      only it is trusted), else the system roots
  --tenant        OFFICE_TENANT        tenant         none; the calls of the meeting, records, tasks and
                                                      slides need it
  --key           OFFICE_KEY           key            <config>/agentic-office/client/key.pem
  --runtime-dir   OFFICE_RUNTIME_DIR                  $XDG_RUNTIME_DIR/agentic-office, else
                                                      /tmp/agentic-office-<uid>
  --session       OFFICE_SESSION       agent.session  the only session this participant joined on this machine
                                                      (the file key counts only for "agent run")
  --json          OFFICE_JSON=1                       off

An environment variable that is set but empty means: no tenant (OFFICE_TENANT=), the system
roots (OFFICE_CA_CERT=) or no hub profile (OFFICE_HUB_PROFILE=). An empty OFFICE_HUB or
OFFICE_KEY counts as not set. The same holds for an empty flag value.

Configuration files (YAML, strict)
  The connection to a hub and the options of "agent run" can live in two files. Each source
  overrides the one before it, key by key:
    1. the defaults of the client
    2. the user file <config>/agentic-office/client/config.yaml, next to the key
    3. the project file .office/config.yaml in the current directory
    4. environment variables
    5. command-line flags, and the harness command after "--"
  The connection keys (hub, tenant, ca-cert, key, hub-profile and the mapping hubs) are at the
  top level; the options of the agent runner are in the section agent. A few keys exclude each
  other: role and role-file, and wake.command and wake.url. When a higher source sets one of
  them, the other one from a lower source no longer counts.

  Key                     Flag                         Default
  agent.session           --session                    none
  agent.harness           the command after "--"       none
  agent.role              --role                       none
  agent.role-file         --role-file                  none
  agent.allow             --allow                      the office tools only
  agent.cwd               --cwd                        the current directory
  agent.wake.strategy     --wake                       turn-end
  agent.wake.command      --decide-cmd                 none
  agent.wake.url          --decide-url                 none
  agent.wake.model        --decide-model               none
  agent.wake.threshold    --decide-threshold           0.5
  agent.wake.timeout      --decide-timeout             10s
  agent.wake.key-env      (none)                       OFFICE_DECIDE_KEY, the name of the variable that
                                                       holds the key of the model

  Example of a project file:

    hub-profile: acme                   # a profile of the user file
    agent:
      session: standup
      harness: [opencode, acp]          # used when there is no command after --
      role-file: roles/minutes.md       # relative to the current directory
      allow: ["office_*"]
      wake:
        strategy: decision-model
        url: https://api.typesafe.ai/v1/systemone
        model: jev-1.14.0
        key-env: OFFICE_DECIDE_KEY      # the name of the variable, never the key

  Rules
  - No secrets in a file. key-env names the environment variable that holds the key of the
    model; key is the path of a key file, never the key. You can check a project file in. When
    key-env is set in a file, the variable must be set.
  - Strict. An unknown key or a wrong type stops the command with exit code 2 and names the file,
    the line and the key, for example "configuration file /work/.office/config.yaml, line 4:
    unknown key agent.wake.api-key". A wake setting that does not fit the strategy of the same
    file is an error too.
  - Paths. A relative ca-cert, key, role-file or cwd in the project file is relative to the
    current directory; in the user file, relative to <config>/agentic-office/client. A relative
    path in a flag is relative to the current directory.
  - Strategy from a flag. When a flag changes the wake strategy (for example "--wake turn-end"),
    the wake settings of a file that do not fit it are left out. A flag that does not fit the
    strategy is an error.

Hub profiles
  A hub profile is a named set of the four connection values: the address of the hub, the tenant,
  the certificate of the hub and, if you want, the path of the private key. One client can hold
  several profiles. A profile never holds a secret; the private key stays in its file. The
  commands "hub add", "hub list" and "hub remove" manage the profiles of the user file.

    # <config>/agentic-office/client/config.yaml
    hub-profile: acme                   # the default profile (optional)
    hubs:
      acme:
        hub: https://hub.acme.example:8740
        tenant: acme
        ca-cert: hubs/acme.crt          # relative to this folder; leave it out for a public CA
      lab:
        hub: https://lab.example.org:8740
        tenant: research
        key: /home/ada/.config/agentic-office/lab/key.pem   # optional: another key for this hub

  - Choose a profile with --hub-profile, OFFICE_HUB_PROFILE or hub-profile in a file. The highest
    source that chooses a profile wins. An empty choice means no profile. A profile that does not
    exist is an error (exit code 2) that lists the known profiles.
  - Profiles of both files merge: a profile of the user file and one of the project file with the
    same name are merged key by key, and the project file overrides single keys. Every profile
    needs a hub address after the merge.
  - A profile is a whole hub. It sets the hub, the tenant and the certificate: a value that the
    profile leaves out is empty (no tenant, the system roots; the default hub.crt is not used).
    The key is the exception: a profile without a key uses the key that is set without a profile
    (key, --key, OFFICE_KEY or the default key.pem).
  - Single values override a profile. The profile that a source chooses gives its values at the
    level of that source. A hub, tenant, ca-cert or key of the same or a higher source overrides
    them, for example "--tenant research" on top of a profile. A hub in the project file
    overrides a profile that the user file chooses, and a profile that the project file chooses
    overrides a hub of the user file.

"office config show" prints the effective settings and where each value comes from.
`, "\n"),

	"json": strings.TrimLeft(`
Machine-readable output: --json

With --json (or OFFICE_JSON=1) a command prints exactly one JSON object on one line on stdout,
also when it fails. Agents can pipe it to jq.

- Field names are camelCase, like the canonical JSON of the contracts.
- An event has the same form as its line in the local log.
- A session command prints {"event": {...}, "duplicate": false}.
- "meeting join" prints {"sessionId", "participantId", "logPath", "replicatorPid", "event",
  "duplicate", "stageUrl"}.
- On failure it prints {"error": {"code": "permission_denied", "message": "..."}}. The code is the
  Connect code of the hub (invalid_argument, not_found, failed_precondition, unimplemented, ...)
  or a code of the CLI: usage, no_key, no_session, error.
- "meeting view --json" prints one JSON object per change, for example
  {"utterance":{"eventId":12,"commandId":"...","text":"...","status":"sent"}}, {"paused":true}
  and {"ended":true}.
- "meeting join --mic --json" (or --wav) prints the join object, then the changes of the view,
  one per line. The reports of the ears go to stderr as {"ears":{...}}.
- Progress lines go to stderr as {"progress": "..."}.

The JSON of a command that has a result of its own is described in the help of that command.

Without --json, a session command prints "event <id> <type>"; "meeting join" and "meeting log"
print the path only. Errors go to stderr.

Exit codes: 0 success, 1 error (also from the hub), 2 usage error.
`, "\n"),

	"import-format": strings.TrimLeft(`
The import format of "office session import"

One JSON document:

  {
    "source": "Slack #design, exported 2026-11-06",
    "title": "Design review",
    "participants": [
      {"id": "ada", "name": "Ada", "kind": "human"},
      {"id": "claude", "name": "Claude", "kind": "agent"}
    ],
    "utterances": [
      {"speaker": "ada", "text": "Shall we import old chats?", "time": "2026-11-06T10:00:00Z"},
      {"speaker": "claude", "text": "Yes, as marked sessions.", "time": "2026-11-06T10:00:30Z"},
      {"speaker": "ada", "text": "Decided.", "modality": "voice"}
    ]
  }

  Field                    Required  Meaning
  source                   yes       Where the conversation comes from: a tool, a channel, a URL.
  title                    no        Title of the conversation.
  participants[].id        yes       Short ID inside the file: 1 to 64 letters, digits, ".", "_" or
                                     "-", starting with a letter or digit. Unique.
  participants[].name      yes       Display name.
  participants[].kind      yes       human or agent.
  utterances[]                       The messages in order. The order of the file is the order of
                                     the log.
  utterances[].speaker     yes       The id of a participant.
  utterances[].text        yes       What was said or written.
  utterances[].time        no        When, in RFC 3339. Kept as originalTime; not checked.
  utterances[].modality    no        text (default) or voice.

Unknown fields are an error, so a typo does not get lost. A participant becomes participant.joined
with the ID imported:<key>; registered IDs never contain a colon. Adapters for other formats (chat
exports, WebVTT) are not built yet: convert to this format first.

  office session import design.json --session design-review
  office meeting events --session design-review | jq -c 'select(.type == "utterance.final")'
`, "\n"),

	"deck-format": strings.TrimLeft(`
The deck files of "office slides render"

A deck in Markdown (a file that ends with .md):

  # Budget 2027               the deck title (optional)
  ## Plan                     starts a slide; every slide needs a title
  - Hire two people           "- " or "* " is a bullet
  - Open an office in Lyon
  Say the numbers slowly.     other text becomes the notes of the slide (hidden in the output)

A file that does not end with .md is JSON in the form of the render request:

  {"title": "Budget 2027", "slides": [{"title": "Plan", "bullets": ["Hire two people"], "notes": "Say the numbers slowly."}]}

A deck has at least one slide. The output is one self-contained HTML document: one section per
slide, black on white, large text, no scripts. All text is plain text; Markdown inside a bullet
is shown as it is. The same deck always gives byte-equal output. A deck title becomes a title slide;
a fragment such as #slide-2 shows that slide in the output.
`, "\n"),

	"ears": strings.TrimLeft(`
The ears: speak into the meeting

"office meeting join <session> --mic" joins as captured, runs the ears and shows the gateway view.
The ears transcribe your own speech locally with Whisper. Only the text and its metadata go to the
hub; the audio never leaves your computer.

- A sentence ends after a short silence (500 ms). Whisper then transcribes it and the view shows
  it as "final"; it becomes "sent" when it is in your local log.
- When you stay silent for longer after your last sentence (1.8 s), the ears end your turn: they
  submit turn.end for you, after the sentences of the turn. Agents that wait for the end of a turn
  wake up then. There is one turn.end per turn; the next one comes only after you spoke again.
  Typed text ("office say") does not end a turn; use "office signal turn-end".
- p pauses: the microphone is closed and nothing is transcribed until you press p again. What you
  said just before the pause and is not final yet is dropped. A pause also ends your turn without
  turn.end, and the ears send no turn.end while you are paused. o marks your last sent sentence
  off the record. q leaves.
- When the network drops, the ears keep transcribing and keep the sentences in a queue (in memory).
  They send them in order when the network is back. Sentences still queued when you quit are
  lost; the ears say how many.
- --wav FILE plays a WAV file in real time instead of the microphone (16-bit PCM, any rate, mono
  or stereo). The view stays open after the file ends; quit with q.
- Use a headset, or a voice print. Without a voice print, the ears hear everything that the
  microphone hears and have no echo cancellation. With one ("office voice enrol"), the ears
  compare your speech with it and drop the parts that are not your voice, so you can use
  loudspeakers. A dropped part is reported as "not your voice". Where another voice speaks at
  the same time as you, the louder voice wins: words of yours may be missing, or words of
  someone else may stay in. The ears then mark the utterance ("overlap" in the log), and agents
  that read it should ask before they act on a doubtful sentence. The voice print never leaves
  your computer; "office voice delete" removes it. --no-voice-print ignores it for one join.

  Flag            Variable                  Default
  --model         OFFICE_WHISPER_MODEL      the best model for the language ("office models list")
  --fast                                    off
  --vad-model     OFFICE_VAD_MODEL          <data dir>/models/ggml-silero-v6.2.0.bin
  --language      OFFICE_WHISPER_LANGUAGE   de
                  OFFICE_WHISPER_GPU        1 for the macOS build on Apple silicon, else 0
  --threads       OFFICE_WHISPER_THREADS    half of the CPUs, at most 16
  --end-silence   OFFICE_EARS_END_SILENCE   500ms
  --turn-silence  OFFICE_EARS_TURN_SILENCE  1.8s (at least the end silence)
                  OFFICE_VAD_THRESHOLD      0.5 (speech probability of the VAD)
  --vocabulary                              .office/vocabulary.txt in the current directory, if there
  --context                                 off
  --voice-print   OFFICE_VOICE_PRINT        <data dir>/voiceprint.json, if it is there
  --no-voice-print                          off
  --debug         OFFICE_EARS_DEBUG         off (see "Debug output" below)
                  OFFICE_MIC_COMMAND        the first recorder found (see below)
                  OFFICE_MODEL_URL          a mirror for model downloads

  <data dir> is ~/.local/share/agentic-office (or $XDG_DATA_HOME/agentic-office).

Vocabulary. Whisper often misspells names and terms of a project. Before each sentence the ears
give Whisper a prompt with the words it should prefer: 1. the terms of .office/vocabulary.txt in
the current directory, or of --vocabulary FILE (one term per line, the most important first;
empty lines and lines that start with # are skipped); 2. the display names of the participants of
the session, from the local log. The prompt holds about 200 tokens: Whisper keeps only the last
223 tokens of a longer prompt, and the ears estimate from above (one token per two bytes, plus one
per term). What does not fit is cut at the end, so the first lines always count. The ears report
the vocabulary at the start and whenever it changes, for example "vocabulary: 12 terms and 3
names in the prompt (terms from .office/vocabulary.txt); 5 cut, the prompt is full". --context also
gives Whisper the previous sentence; it is off by default because its effect is not measured yet.
"office vocabulary extract" writes the file from your documents.

Debug output. The office has no log levels. --debug (or OFFICE_EARS_DEBUG=1) adds one line per
sentence, "ears: gate: ...", when you use a voice print. It shows the similarity of each window
of the sentence to your voice print (1.5 s windows; with --json, the middle of each in ms from the
start of the sentence), the stretches that went to Whisper, the speech that was dropped, the
limits in use and whether the sentence got the overlap hint. A similarity is between -1 and 1:
your voice scored about 0.6 to 0.8 in tests, other voices below 0.4. The ears keep a window from
0.50 and call it doubtful below 0.60. The lines contain no text and no audio. With --json, each
line is {"ears": {"type": "gate", "windows": [...], "keptMs": [...], ...}} on stderr.

Microphone. The ears need no administrator rights. They read the microphone through a recorder
program that runs as your user and writes raw 16 kHz mono 16-bit PCM to stdout. They try, in this
order: parec (PulseAudio, and PipeWire through pipewire-pulse; package pulseaudio-utils), arecord
(ALSA; package alsa-utils), rec (SoX; package sox) and, on macOS, ffmpeg with AVFoundation (macOS
asks once for permission to use the microphone for the terminal app). Your user must be allowed to
use the audio device; on a server it may need the group "audio". Choose another device or program
with OFFICE_MIC_COMMAND, which is run with "sh -c", for example
  OFFICE_MIC_COMMAND='parec --raw --rate=16000 --channels=1 --format=s16le --device=my-headset'

What the ears report. On stderr, one line per step: listening, vocabulary, final (with the Whisper
time), sent (with the time from the end of speech to the confirmed submit), turn-end, retry while
the network is down, dropped, unsent and done (end of a WAV file). With --json each is an object
{"ears":{"type":"sent","commandId":...,"speechEndToSubmitMs":...}}. Stdout keeps the changes of
the view.
`, "\n"),
}

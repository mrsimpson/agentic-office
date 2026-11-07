# Agentic office

A headless office for teams of humans and agents. Humans and agents join meeting sessions as
participants of the same kind. Humans with a headset are transcribed locally. Every participant
gets a local copy of the session log. Agents act through a CLI and MCP. A stage shows the meeting
by screen share.

The architecture is in [`docs/architecture/`](docs/architecture/) (arc42). Rules for contributors
and agents are in [`AGENTS.md`](AGENTS.md).

## Setup

You need:

- **Go 1.27** or newer.
- **make** and **git**.
- A **C/C++ compiler** and **cmake** for **whisper.cpp**, which the `office` binary links through
  cgo (see below). The ears also need a multilingual **Whisper model** and the **Silero VAD
  model**.
- To change the protocol only: **buf**, **protoc-gen-go** and **protoc-gen-connect-go** (v2):

  ```sh
  go install github.com/bufbuild/buf/cmd/buf@latest
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install connectrpc.com/connect/v2/cmd/protoc-gen-connect-go@latest
  ```

  The generated code is committed, so a normal build does not need these tools.

### whisper.cpp and the models

The ears use whisper.cpp through cgo, also for voice activity detection. Every build needs it in
`third_party/whisper.cpp` (not committed), also the build of the server, because `go.mod` points
the Go bindings of whisper.cpp there:

```sh
make third_party/whisper.cpp   # clones whisper.cpp; or: ln -s /path/to/whisper.cpp third_party/whisper.cpp
make whisper                   # builds the static libraries (libwhisper.a, libggml*.a)
make models                    # downloads ggml-small.bin (about 490 MB) and the Silero VAD model
```

`make models` puts the models into `~/.local/share/agentic-office/models` (or
`$XDG_DATA_HOME/agentic-office/models`; change it with `MODEL_DIR=…`). The ears look there by
default. To use other files, set `OFFICE_WHISPER_MODEL` and `OFFICE_VAD_MODEL`. Use a
multilingual model, not a `.en` model: those only know English. On the reference host (CPU only),
`small` needs about 0.7 s per sentence with 16 threads; larger models are too slow for live use.

The Makefile sets `C_INCLUDE_PATH` and `LIBRARY_PATH` for cgo. To run `go build` or `go test`
without make, export them as in the Makefile.

## Build and test

| Command | What it does |
|---|---|
| `make build` | Builds all packages and the binaries `bin/office-server` and `bin/office` (needs whisper.cpp) |
| `make arch` | Validates the architecture with the arc42 CLI and runs the architecture test (`tests/architecture/`); needs Node.js |
| `make test` | Runs `make arch`, `go vet` and the contract tests (`tests/contract/`) |
| `make e2e` | Runs the end-to-end tests with real processes (`tests/e2e/`, build tag `e2e`). They check behaviour only and never fail on timing. The ears tests need the models and are skipped without them |
| `make performance` | Runs the quality scenarios with their timing criteria and their full lengths (build tags `e2e,performance`; 30 minutes of speech, a 60 s network drop; about 35 minutes) and lists their numbers. Meant for the reference host |
| `make lint` | Checks the Protobuf contracts with `buf lint` |
| `make generate` | Regenerates the Go code of the contracts next to their `.proto` files |

Run the server by hand:

```sh
bin/office-server -addr 127.0.0.1:8740
```

The server speaks only TLS and needs an access token for every call. See
[Server setup](#server-setup) for the first start.

## Layout

The layout follows the building blocks of chapter 5 of the architecture. Every building block and
every interface has exactly one path. An interface lies inside the path of its provider and owns
its types. An interface that crosses the boundary of the server or the client belongs to that
black box and is realized by one part (for example the meeting session realizes the session
commands of the office server). There is no shared package. `make arch` checks that every import between building
blocks goes through an interface that the importing block requires (concept-module-boundaries).

```
server/                   bb-office-server: wiring of the server (binary office-server)
  session/                bb-session-service
    api/                  if-session-commands, if-session-stream: contract, generated code,
                          event types in dotted form (api/events)
  identity/               bb-identity
    api/                  if-auth-api: contract, generated code, client side (api/auth: key files,
                          challenge signing, tokens, TLS trust)
    tokencheck/           if-token-check: who makes a call (in-process)
  journal/  journal/api/  bb-journal, if-journal-api
  artifacts/ artifacts/api/  bb-artifact-service, if-artifact-api
  stage/                  bb-stage, realizes if-stage-web
client/                   bb-participant-client: wiring of the client (binary office)
  core/                   bb-client-core, if-client-core: shared core of CLI, MCP server and ears;
                          command IDs (core/ids)
  cli/                    bb-cli, if-cli
  mcp/                    bb-mcp, if-mcp
  replicator/             bb-replicator, if-replication (start, find and stop a replicator)
    locallog/             if-local-log: line format of the local session log
  ears/                   bb-ears, if-audio-in
tests/                    external actors: may use interfaces only
  architecture/           architecture test
  contracttest/           harness for black-box contract tests
  contract/               contract tests, one file per building block
  e2e/                    end-to-end tests, also of the quality scenarios
docs/testing/             manual tests (the headset test)
```

## Contracts

All interfaces are defined in Protobuf and served with Connect RPC (`dec-connect-rpc`):

| Interface | Service | Kind |
|---|---|---|
| Session commands | `office.session.v1.SessionService` | unary |
| Session stream | `office.session.v1.SessionService/Subscribe` | server stream from a cursor |
| Auth API | `office.identity.v1.IdentityService` | unary |
| Journal API | `office.journal.v1.JournalService` | unary |
| Artifact API | `office.artifact.v1.ArtifactService` | unary |

Rules that hold for all of them:

- Every command carries a `command_id` that the client creates: a UUIDv7. A repeated command
  returns the stored result and sets `duplicate` (`dec-idempotent-commands`).
- Every event has an `id` that grows by one inside a session, and a `type` in dotted form. The ID
  is 32 bits wide, so its JSON form is a number.
- Every call carries an access token in the header `Authorization: Bearer <token>`, except
  `GetChallenge` and `ExchangeChallenge`. See [Identity](#identity).
- Connect's own protocol is plain HTTP with JSON. You can call any command with `curl`
  (`--cacert` trusts the self-signed certificate of the server):

  ```sh
  curl --cacert ~/.config/agentic-office/server/tls.crt \
    -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
    -d '{"sessionId":"demo","commandId":"…"}' \
    https://127.0.0.1:8740/office.session.v1.SessionService/Join
  ```

### Meeting session

The meeting session (`server/session`) is implemented. It keeps its logs in memory, so a restart
of the server loses all sessions. The package comment in `server/session/doc.go` has the details.
In short:

- A session exists as soon as someone joins or subscribes.
- Missing fields fail with `invalid_argument`. Unknown participants or utterances fail with
  `not_found`. Commands that do not fit the state fail with `failed_precondition`, for example
  every new command after `session.ended`.
- The session does not create signals itself. Clients submit `turn.end`, `silence.long` and
  `addressed` with `SubmitSignal` (see issue #14).
- `Subscribe` ends the stream after `session.ended`. A cursor beyond the last event fails with
  `out_of_range`.
- The participant in a session is the participant of the access token. `Join` adds it;
  `display_name` and `kind` may be empty (the registered ones are used). Every other command
  must carry that participant in `participant_id`, or it fails with `permission_denied`. A
  participant joins once: another join, also from a second device, returns the first join with
  `duplicate` set.
- `Subscribe` ends with `unauthenticated` when the token expires or its key is revoked. The client
  authenticates again and resumes from its cursor.

### Identity

Identity (`server/identity`) is implemented. The package comment in `server/identity/doc.go` has
the details. In short:

- Every actor (each human device, each agent) has its own Ed25519 key pair, created locally with
  `server/identity/api/auth`. Only the public key goes to the server.
- An admin registers a public key for a participant with `RegisterKey`. The admin chooses the
  participant ID, for example `ada`. A participant can have several keys. `RevokeKey` removes a
  key; its tokens stop working at once.
- A client gets a challenge (`GetChallenge`), signs it and exchanges it for an access token
  (`ExchangeChallenge`). The token lives 15 minutes (`-token-ttl`). `auth.TokenSource` does this
  and renews the token before it expires; `auth.Bearer` attaches it to every Connect call.
- `CreateStageLink` returns a read-only link `https://<server>/stage/<session>?token=<token>`
  for one session. Its token only allows `Subscribe` for that session. It lives 24 hours
  (`-stage-link-ttl`). Open it in a browser to see the stage (below).
- Registered keys are stored in `<data-dir>/identity.json`. Tokens are kept in memory only, so
  clients authenticate again after a restart of the server.

### Journal

The journal (`server/journal`) is implemented. The package comment in `server/journal/doc.go` has
the details. In short:

- An entry has a kind (`decision`, `task`, `minutes`, `note`), a text, an optional session ID, the
  author and the author's kind (`human` or `agent`). The server takes the author and the kind
  from the access token: `author_participant_id` must be the participant of the token. A stage
  link cannot use the journal.
- `AppendEntry` is idempotent by `command_id`. Command IDs that start with `office/` are reserved
  for the server.
- `ListEntries` returns entries after the cursor `after_entry_id`, oldest first, filtered by kind
  and session. It returns at most `limit` entries (default 100, at most 1000). To read the next
  page, pass the ID of the last entry.
- **Minutes at the end of a session.** `EndSession` stores the text of the last
  `minutes.updated` event as a `minutes` entry of the session, then writes `session.ended`. Its
  author is the participant who wrote that version. A session without minutes, or with blank
  minutes, stores nothing. What "confirmed minutes" means is still open (#22).
- The journal is stored in `<data-dir>/journal.jsonl`, so it survives a restart of the server.

### Artifact tools

The artifact tools (`server/artifacts`) render slides. The package comment in
`server/artifacts/doc.go` has the details. In short:

- `RenderSlides` takes a deck: an optional title and at least one slide. Every slide has a title,
  and may have bullets and notes. The result is an artifact with the ID `slides/<n>` (n starts at
  1) and the media type `text/html; charset=utf-8`.
- The content is one self-contained HTML document for a screen share: one section per slide,
  black on white, large text. It has no scripts and loads nothing. All text is HTML-escaped and
  shown as plain text. Notes are in the document but hidden.
- The same deck always gives byte-equal content (`con-no-intelligence`): no time or random value
  is in it. The ID and `created_at` are outside the content.
- `RenderSlides` is idempotent by `command_id`. The same `command_id` with another deck fails
  with `already_exists`. A new `command_id` with the same deck gives a new ID.
- `GetArtifact` fetches by ID; an unknown ID fails with `not_found`. Any participant can fetch
  every artifact. A stage link cannot use the artifact API: the stage renders on the server.
- The meeting session does not check that a shown artifact exists.
- Artifacts are stored in `<data-dir>/artifacts.jsonl`, so they survive a restart of the server.

### Stage

The stage (`server/stage`) is the web page of one session for the screen share. The package
comment in `server/stage/doc.go` has the details. In short:

- Create a link with `office stage link` (or `CreateStageLink`) and open it in a browser. Share
  that browser window in the meeting tool.
- The page shows the shown artifact, the recent contributions, all participants with their
  capture status (captured, paused, not captured, left; agents are marked "agent"), the raised
  hands and the running minutes. Off the record contributions disappear. Large text, white on black.
- It is rendered on the server and updated live through server-sent events
  (`/stage/<session>/events?token=…`). There is no frontend build and nothing is loaded from
  outside. A shown artifact is embedded in a sandboxed iframe.
- A hand stays raised until its participant speaks, raises it again or leaves (#24).
- Without a valid stage link for the session, the page fails with 401 (no, unknown or expired
  token) or 403 (a participant token, or a link for another session) and shows no session data.

## Server setup

All files of the server are in its data directory, by default `~/.config/agentic-office/server`
(`-data-dir`):

| File | What it is |
|---|---|
| `admins` | Admin keys. You write it by hand. |
| `identity.json` | Registered participants and keys. The server writes it. |
| `journal.jsonl` | Journal entries, one JSON object per line in the form of the API. The server appends to it. |
| `artifacts.jsonl` | Artifacts, one JSON object per line: command ID, input and artifact in the form of the API. The server appends to it. |
| `tls.crt`, `tls.key` | TLS certificate and key. The server creates a self-signed pair if neither exists. |

### First admin

An admin is a key in the `admins` file. Only admin keys may call `RegisterKey` and `RevokeKey`.
Each line has a participant ID, the public key (base64 of the 32 raw bytes, see `server/identity/api/auth`) and
an optional display name:

```
# participant  public key                                     display name
ada            hGs7aXk0n1V6lH1d3mFzWQ0bX2j7zv8nq3C4oD5pE6A=  Ada Lovelace
```

1. Create a key pair on your device: `office key create` prints the public key.
2. Write the public key into `admins` on the server host.
3. Start or restart the server. It reads the file at start.
4. Authenticate with your key; your token may now register the keys of the other participants
   with `office admin key register` (see [Participant client](#participant-client-office-cli)).

To remove an admin, delete the line and restart. Admin keys cannot be revoked through the API.

### TLS and your own certificate

On the first start, the server creates a self-signed certificate for `localhost`, `127.0.0.1`,
`::1` and the host name (`-tls-hosts` changes the list) and logs its SHA-256 fingerprint.
Clients trust it by its file: copy `tls.crt` to the client and pass it to `auth.TLSConfig` or
`auth.HTTPClient` (or to `curl --cacert`).

To use your own certificate, for example from your CA or Let's Encrypt:

```sh
bin/office-server -tls-cert /etc/office/fullchain.pem -tls-key /etc/office/privkey.pem \
  -public-url https://office.example.org:8740
```

- The certificate file is PEM and may hold the whole chain. The key file is PEM (PKCS #8, PKCS #1
  or SEC 1).
- Or put both into the data directory as `tls.crt` and `tls.key`. The server never overwrites
  existing files.
- Clients then trust the system roots: pass an empty certificate file to `auth.TLSConfig`.
- `-public-url` is the address in stage links. It defaults to `https://<listen address>`.
- The server reads the certificate at start. Restart it after you renew the certificate.

## Participant client (office CLI)

`bin/office` is the participant client for humans and agents (`bb-cli`, `bb-replicator`). The
MCP server (`office mcp`, see [MCP server](#mcp-server)) offers the same features; both build on
the shared core in `client/core` (`dec-cli-and-mcp`). Run `office help` for all commands.

### First start

```sh
office key create                  # creates ~/.config/agentic-office/client/key.pem, prints the public key
# an admin registers it:
office admin key register ada <public-key> --name "Ada Lovelace" --kind human
office whoami                      # authenticates and prints "ada"
```

`office key show` prints the public key and key ID again. `office admin key revoke <key-id>`
removes a key. Only admin keys may register and revoke (see [First admin](#first-admin)).

### Configuration

Every command takes these flags. The environment variables set the defaults.

| Flag | Variable | Default |
|---|---|---|
| `--server` | `OFFICE_SERVER` | `https://127.0.0.1:8740` |
| `--ca-cert` | `OFFICE_CA_CERT` | `~/.config/agentic-office/client/server.crt` if it exists, else the system roots |
| `--key` | `OFFICE_KEY` | `~/.config/agentic-office/client/key.pem` |
| `--runtime-dir` | `OFFICE_RUNTIME_DIR` | `$XDG_RUNTIME_DIR/agentic-office`, else `/tmp/agentic-office-<uid>` |
| `--session` | `OFFICE_SESSION` | the only session this participant joined on this machine |
| `--json` | `OFFICE_JSON=1` | off |

For a self-signed server, copy its `tls.crt` to `~/.config/agentic-office/client/server.crt`.
Several participants on one machine (for example a human and an agent) use one key file each.

### Join a meeting and read the log

```sh
LOG=$(office meeting join weekly)  # returns at once and prints the path of the local log
tail -f "$LOG" | grep --line-buffered '"type":"turn.end"\|"type":"addressed"'
office say "I can summarize that."
office hand raise --topic budget
office stage show slides/42
office minutes update --file minutes.md
office meeting leave               # or: office meeting end (for everyone)
```

Other session commands: `meeting pause | resume`, `utterance off-the-record <event-id> | --last`,
`signal turn-end | silence-long --duration D | addressed --participant P --utterance ID`,
`stage link`.

**The journal:**

```sh
office journal append --kind decision "We ship on Friday."
office journal append --kind minutes --file minutes.md --session weekly
office journal list --kind decision --after 10 --limit 20
office journal list --session weekly    # also the minutes stored when "weekly" ended
```

`journal append` links the entry to a session only with an explicit `--session`.

**Slides:**

```sh
ID=$(office slides render deck.md)   # prints the artifact ID, for example slides/42
office stage show "$ID"
office artifact get "$ID" --out deck.html
```

A deck in Markdown: `# ` is the deck title, `## ` starts a slide, `- ` or `* ` is a bullet, other
text becomes the notes of the slide. A file that does not end with `.md` is JSON in the form of
`RenderSlidesRequest`: `{"title": …, "slides": [{"title": …, "bullets": […], "notes": …}]}`.

**The local log** is `<runtime dir>/<session>/<participant>.jsonl` (directories 0700, file 0600):
one compact JSON line per event (`client/replicator/locallog`), with the dotted type and the event ID as a
number. It is append-only: marking a sentence off the record adds a new `utterance.off_the_record` line. The runtime
directory also holds the join state (`<participant>.join`, the stored join command ID, so a
restarted client is the same participant), the PID of the replicator (`<participant>.pid`) and
its output (`<participant>.replicator.log`). Session and participant IDs that are not safe as
file names are encoded as `=` plus base64url.

**How join runs the replicator.** The replicator is its own process (`office replicator run`).
It holds a lock on the log file, so there is at most one per session and participant; a second
`join` reuses it. After a crash it cuts an incomplete last line and resumes after the last
complete line; after a network drop it reconnects with a backoff.

- `office meeting join <session>` starts the replicator **detached** and returns as soon as the
  log file is open. This suits agents whose shell ends after each command. The detached
  replicator stops after `session.ended` or when the participant leaves.
- `office meeting join <session> --foreground` prints the path and stays. The replicator is its
  child and stops when `join` ends, also when it is killed. Use it when the caller runs the
  command as a background task, or with `&` in a shell that stays open.
- Run `office meeting join` again to restart a replicator that was killed. It resumes the file.
- While the server is unreachable, a replicator keeps trying. To stop a detached one without
  leaving, kill the PID in `<participant>.pid`.
- Local logs stay until the runtime directory is cleaned (at logout for `$XDG_RUNTIME_DIR`).
  The retention rule is still open (`concept-transparency`).

### Gateway view

`office meeting view` shows your own utterances with their status (`final`, `sent`,
`off_the_record`) and whether you are paused. It is a filtered view of the local log
(`dec-gateway-cli`) and prints one line per change. Keys: `p` pauses or resumes, `o` marks the
last sent sentence off the record, `q` quits. It ends with the session.

### Off the record and reading events after a cursor

What is said stays said (`dec-off-the-record`). A participant can mark **their own** sentence off
the record (`office utterance off-the-record <event-id> | --last`); the server checks that the
caller is the speaker. The log gets a new `utterance.off_the_record` line. Everyone who reads the
session later, through the stream or `meeting events`, gets the sentence as a placeholder: the
same ID, `"offTheRecord": true`, and no text. The stage hides it. Agents must not use such a
sentence anywhere.

Event IDs grow by one without gaps (`dec-events-from-cursor`). `office meeting events --after N
[--limit N]` reads the events after a cursor from the server, in the line format of the local log
(`--json`: `{"events": […], "more": bool}`). Use it to fill a gap in a copy, or poll it when you
cannot follow a file.

### Ears: speak into the meeting

`office meeting join <session> --mic` joins as captured, runs the ears and shows the gateway
view. The ears transcribe your own speech locally with Whisper. Only the text and its metadata go
to the server; the audio never leaves your computer (`concept-edge-transcription`).

- Each sentence ends after a short silence (default 500 ms). Whisper then transcribes it, and the
  view shows it as `final`. It becomes `sent` when it is in your local log.
- `p` pauses: the microphone is closed and nothing is transcribed until you press `p` again.
  What you said just before the pause and is not final yet is dropped.
- When the network drops, the ears keep transcribing and keep the sentences in a queue (in
  memory). They send them in order when the network is back. Sentences that are still queued when
  you quit are lost; the ears say how many.
- `--wav FILE` plays a WAV file in real time instead of the microphone (16-bit PCM, any rate,
  mono or stereo). The view stays open after the file ends; quit with `q`.
- Use a headset (`con-headset-only`): the ears have no echo cancellation.
- Before join, the ears check the models, the WAV file or the recorder. If something is missing,
  join fails and you do not join.

| Flag | Variable | Default |
|---|---|---|
| `--model` | `OFFICE_WHISPER_MODEL` | `~/.local/share/agentic-office/models/ggml-small.bin` |
| `--vad-model` | `OFFICE_VAD_MODEL` | `~/.local/share/agentic-office/models/ggml-silero-v6.2.0.bin` |
| `--language` | `OFFICE_WHISPER_LANGUAGE` | `de` |
| `--threads` | `OFFICE_WHISPER_THREADS` | half of the CPUs, at most 16 |
| `--end-silence` | `OFFICE_EARS_END_SILENCE` | `500ms` |
| | `OFFICE_VAD_THRESHOLD` | `0.5` (speech probability of the VAD) |
| | `OFFICE_MIC_COMMAND` | the first recorder found (see below) |

**Microphone on a laptop.** The ears need no administrator rights. They read the microphone
through a recorder program that runs as your user, like any audio application, and that writes
raw 16 kHz mono 16-bit PCM to stdout. They try, in this order:

- `parec` (PulseAudio, and PipeWire through `pipewire-pulse`): package `pulseaudio-utils`;
- `arecord` (ALSA): package `alsa-utils`;
- `rec` (SoX, Linux and macOS): package `sox`;
- on macOS also `ffmpeg` with AVFoundation. macOS asks once for permission to use the microphone
  for the terminal app.

Your user must be allowed to use the audio device. On desktop Linux this is the default for the
logged-in user; on a server, the user may need the group `audio`. Choose another device or
program with `OFFICE_MIC_COMMAND`, which is run with `sh -c`, for example
`OFFICE_MIC_COMMAND='parec --raw --rate=16000 --channels=1 --format=s16le --device=my-headset'`.

**What the ears report.** On stderr, the ears print one line per step: `listening`, `final`
(with the Whisper time), `sent` (with the time from the end of speech to the confirmed submit),
`retry` while the network is down, `dropped`, `unsent` and `done` (end of a WAV file). With
`--json`, each is a JSON object `{"ears":{"type":"sent","commandId":…,"speechEndToSubmitMs":…}}`
(see `Report` in `client/ears`). Stdout keeps the changes of the view.

### Machine-readable output (`--json`)

With `--json` (or `OFFICE_JSON=1`), a command prints **exactly one JSON object on one line** on
stdout, also when it fails. Agents can pipe it to `jq`.

- Field names are camelCase, like the canonical JSON of the contracts.
- An event is in the same form as its line in the local log.
- A session command prints `{"event": {…}, "duplicate": false}`.
- `meeting join` prints `{"sessionId", "participantId", "logPath", "replicatorPid", "event",
  "duplicate"}`.
- On failure: `{"error": {"code": "permission_denied", "message": "…"}}`. The code is the
  Connect code of the server (`invalid_argument`, `not_found`, `failed_precondition`,
  `unimplemented`, …) or a code of the CLI: `usage`, `no_key`, `no_session`, `error`.
- `meeting view --json` prints one JSON object per change, for example
  `{"utterance":{"eventId":12,"commandId":"…","text":"…","status":"sent"}}`, `{"paused":true}`,
  `{"ended":true}`.
- `meeting join --mic --json` (or `--wav`) prints the join object, then the changes of the view
  as above, one per line. The reports of the ears go to stderr as `{"ears":{…}}`.

Without `--json`, a session command prints `event <id> <type>`; `join` and `meeting log` print the
path only. Errors go to stderr.

Exit codes: `0` success, `1` error (also from the server), `2` usage error.

## MCP server

`office mcp` is a local MCP server on stdin and stdout (`bb-mcp`, `if-mcp`). An MCP client starts
it as a command. It offers the features of the CLI as tools, for agents that use MCP instead of
a shell. It takes the same flags and `OFFICE_*` variables as the CLI (see
[Configuration](#configuration)); `--session` sets the default session of the tools. The key
must exist and be registered first (`office key create`, then an admin registers it).

Add it to Claude Code:

```sh
claude mcp add office -e OFFICE_KEY=$HOME/.config/agentic-office/agent/key.pem \
  -e OFFICE_SERVER=https://office.example.org:8740 -- /path/to/bin/office mcp
```

Or in the JSON configuration of a client (for example `.mcp.json`):

```json
{
  "mcpServers": {
    "office": {
      "command": "/path/to/bin/office",
      "args": ["mcp"],
      "env": { "OFFICE_KEY": "/home/me/.config/agentic-office/agent/key.pem",
               "OFFICE_SERVER": "https://office.example.org:8740" }
    }
  }
}
```

**Tools.** The names use `_` where the architecture writes `.` (`meeting_join` for
`meeting.join`), because many clients and model APIs allow only letters, digits, `_` and `-` in
tool names.

| Tool | CLI command |
|---|---|
| `meeting_join` (session, name) | `meeting join` |
| `meeting_log` | `meeting log` |
| `meeting_leave`, `meeting_pause`, `meeting_resume`, `meeting_end` | `meeting leave`, `pause`, `resume`, `end` |
| `utterance_say` (text) | `say` |
| `utterance_off_the_record` (eventId or last) | `utterance off-the-record` |
| `meeting_events` (after, limit) | `meeting events` |
| `signal_turn_end`, `signal_silence_long`, `signal_addressed` | `signal …` |
| `hand_raise` (topic) | `hand raise` |
| `minutes_update` (text or file) | `minutes update` |
| `stage_show` (artifactId), `stage_link` | `stage show`, `stage link` |
| `slides_render` (title and slides, markdown, or file) | `slides render` |
| `artifact_get` (id, out) | `artifact get` |
| `journal_append` (kind, text, session), `journal_list` (kind, session, afterEntryId, limit) | `journal append`, `journal list` |
| `whoami` | `whoami` |

Session tools take an optional `session`. The default is `--session` or `OFFICE_SESSION`, else the
only session this participant joined from this machine.

**Results.** Each tool returns one JSON object, as text and as structured content, in the same
shape as the CLI's `--json` output (see [Machine-readable output](#machine-readable-output---json)).
Errors set `isError` and return `{"error":{"code","message"}}`. Arguments that do not match the
input schema of a tool are `usage` errors.

**The log.** `meeting_join` returns `logPath`. The replicator runs inside the MCP process, so the
replication lives as long as that process: it stops when the client closes the connection, when
the process ends or is killed, when you leave, or after `session.ended`. Call `meeting_join` again
to restart it. If `office meeting join` already runs a replicator for the same participant,
`meeting_join` reuses it and returns its PID. The agent follows the file, for example:

```sh
tail -n +1 -f "$LOG" | grep --line-buffered '"type":"turn.end"\|"type":"addressed"'
jq -c 'select(.id > 41 and (.type == "utterance.final" or .type == "addressed"))' "$LOG"
```

The description of `meeting_join` and the server instructions explain this and the event types
to the agent.

**Not in the MCP server:**

- Key and admin commands: set up the key with the CLI.
- The gateway view and the ears: an agent has no microphone. Humans use `office meeting join --mic`.
- A long-poll tool (`meeting.wait`, an open point of `concept-read-file-write-tools`): the
  agent reads the file (`dec-log-as-file`). It can be added later without changing the other
  tools.

## Tests

We write no unit tests (`dec-contract-tests`). Every building block is tested as a black box
through its interfaces:

- A contract test starts the block with `tests/contracttest` (as a real process, or as a fake that
  follows the same contract) and uses only its interfaces. `contracttest.StartServer` starts
  `office-server` with TLS and an admin key; `Server.Participant` registers a key for a test
  participant and returns authenticated clients. It never imports the package that
  implements the block.
- `tests/contract/stub_example_test.go` shows a test against a stub service.
- `tests/contract/mcp_test.go` starts `office mcp` and talks to it over stdio with the MCP Go SDK
  client: every tool and its result, the log path that receives the events of all participants,
  errors for bad input, server errors, the artifact tools, the journal, and
  that no writer is left
  when the MCP process closes or is killed. `tests/e2e/mcp_test.go`: an agent works only through
  MCP and follows its log with the `tail -f | grep` filter from the tool description.
- `tests/contract/cli_test.go` and `replicator_test.go` run the real `office` binary and check
  its output and the local log it leaves, including kills of the replicator and torn lines.
- `tests/e2e/ears_test.go` runs the ears with the German test WAV (`tests/e2e/testdata/`): the
  sentences reach another participant's local log in order; a network drop (a proxy from
  `contracttest.StartProxy` is cut and restored) loses and duplicates nothing; a pause
  transcribes nothing; `--mic` with a mock recorder. It also checks that no raw audio leaves the
  device: `contracttest.StartDecryptingProxy` ends TLS with the server's own test certificate and
  records the plaintext. Every recorded byte must decode as a known RPC request
  (`contracttest.DecodeRequests`) with no unknown fields and no long field except the
  transcripts. The tests print the measured times and byte counts (`go test -v`).
- `tests/e2e/quality_test.go` tests the quality scenarios of chapter 10 of the architecture.
  `TestQualitySpeech` lets Ada speak the test sentences in a loop through the decrypting proxy,
  with a bot that follows its log and an open stage. Its subtests are named after the scenarios:
  `qs-no-raw-audio`, `qs-transcript-to-log` (maximum < 200 ms), `qs-speech-to-log`
  (p95 ≤ 1.5 s) and `qs-sender-sees-status` (`sent` and `off_the_record` in the gateway view, and the
  capture status on the stage, each ≤ 1 s). `TestQualityResumeAfterDrop/qs-resume-after-drop`
  cuts Ada's network while she speaks and the bot writes: 0 lost and 0 duplicate events, and
  Ada's local log is complete ≤ 2 s after the network is back. Each scenario prints a line that
  starts with `QS <scenario>` (`grep '^QS '`). In `make e2e` these tests check behaviour only
  and never fail on a timing criterion. `make performance` (build tag `performance`) also checks
  the timing criteria. Two variables set the lengths: `QS_SPEECH_DURATION` (default `60s`) and
  `QS_DROP_DURATION` (default `10s`). `make performance` sets them to `30m` and `60s`, for example
  `make performance QS_SPEECH_DURATION=5m`.
- `tests/architecture/` checks the code against the arc42 model (`make arch`). It reads the model
  with `arc42 get --format json` and the imports with `go list`. A fixture workspace with
  deliberate violations proves that each rule fails when it should.
- The manual headset test (`qs-manual-headset`) is a checklist:
  [`docs/testing/manual-headset-test.md`](docs/testing/manual-headset-test.md).
- The harness uses the binaries in `$OFFICE_BIN_DIR` (the Makefile sets it). Without it, the
  harness builds them with `go build`, so `go test ./tests/contract/...` also works.

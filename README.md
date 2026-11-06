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
- For the ears only (later work package): a **C/C++ compiler**, **cmake**, **whisper.cpp** and a
  multilingual **Whisper model** (see below).
- To change the protocol only: **buf**, **protoc-gen-go** and **protoc-gen-connect-go** (v2):

  ```sh
  go install github.com/bufbuild/buf/cmd/buf@latest
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install connectrpc.com/connect/v2/cmd/protoc-gen-connect-go@latest
  ```

  The generated code is committed, so a normal build does not need these tools.

### whisper.cpp and a model

The ears use whisper.cpp through cgo. The build expects it in `third_party/whisper.cpp` (not
committed):

```sh
make third_party/whisper.cpp   # clones whisper.cpp; or: ln -s /path/to/whisper.cpp third_party/whisper.cpp
make whisper                   # builds the static library libwhisper.a
```

Get a multilingual ggml model, for example `ggml-small.bin` (about 490 MB), from
<https://huggingface.co/ggerganov/whisper.cpp>. Do not use the `.en` models: they only know
English. Point the ears to it with `OFFICE_WHISPER_MODEL=/path/to/ggml-small.bin`. On a CPU,
`small` needs about 0.9 s per sentence; larger models are too slow for live use there.

## Build and test

| Command | What it does |
|---|---|
| `make build` | Builds all packages and the binaries `bin/office-server` and `bin/office` |
| `make test` | Runs `go vet` and the contract tests (`tests/contract/`) |
| `make e2e` | Runs the end-to-end tests with real processes (`tests/e2e/`, build tag `e2e`) |
| `make lint` | Checks the Protobuf contracts with `buf lint` |
| `make generate` | Regenerates `core/api/` from `proto/` |

Run the server by hand:

```sh
bin/office-server -addr 127.0.0.1:8740
```

The server speaks only TLS and needs an access token for every call. See
[Server setup](#server-setup) for the first start.

## Layout

```
proto/office/v1/      Protobuf contracts of all interfaces that cross a process boundary
core/                 Shared core
  api/                Generated Go code for the contracts (Connect RPC)
  events/             Event types in dotted form, for example "utterance.final"
  eventlog/           Line format of the local session log (one compact JSON line per event)
  ids/                Client-generated command IDs (UUIDv7)
  auth/               Client side of identity: key files, challenge signing, tokens, TLS trust
  contracttest/       Harness for black-box contract tests
server/               Building blocks of the office server
  session/            Meeting session: session commands and session stream
  identity/           Identity: auth API and token check
  journal/            Journal: the memory of the team
  artifacts/          Artifact tools: slides
  stage/              Stage: web view of a session
client/               Building blocks of the participant client
  participant/        Shared client core of the CLI and the MCP server
  cli/                office CLI
  mcp/                MCP server
  replicator/         Log replicator: local copy of the session log
  ears/               Ears: local speech to text
cmd/office-server/    Binary of the office server
cmd/office/           Binary of the participant client (CLI, later MCP, replicator and ears)
tests/contract/       Contract tests, one file per building block
tests/e2e/            End-to-end tests
```

## Contracts

All interfaces are defined in Protobuf and served with Connect RPC (`dec-connect-rpc`):

| Interface | Service | Kind |
|---|---|---|
| Session commands | `office.v1.SessionService` | unary |
| Session stream | `office.v1.SessionService/Subscribe` | server stream from a cursor |
| Auth API | `office.v1.IdentityService` | unary |
| Journal API | `office.v1.JournalService` | unary |
| Artifact API | `office.v1.ArtifactService` | unary |

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
    https://127.0.0.1:8740/office.v1.SessionService/Join
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
  `core/auth`. Only the public key goes to the server.
- An admin registers a public key for a participant with `RegisterKey`. The admin chooses the
  participant ID, for example `ada`. A participant can have several keys. `RevokeKey` removes a
  key; its tokens stop working at once.
- A client gets a challenge (`GetChallenge`), signs it and exchanges it for an access token
  (`ExchangeChallenge`). The token lives 15 minutes (`-token-ttl`). `auth.TokenSource` does this
  and renews the token before it expires; `auth.Bearer` attaches it to every Connect call.
- `CreateStageLink` returns a read-only link `https://<server>/stage/<session>?token=<token>`
  for one session. Its token only allows `Subscribe` for that session. It lives 24 hours
  (`-stage-link-ttl`). The stage page itself comes with #12.
- Registered keys are stored in `<data-dir>/identity.json`. Tokens are kept in memory only, so
  clients authenticate again after a restart of the server.

## Server setup

All files of the server are in its data directory, by default `~/.config/agentic-office/server`
(`-data-dir`):

| File | What it is |
|---|---|
| `admins` | Admin keys. You write it by hand. |
| `identity.json` | Registered participants and keys. The server writes it. |
| `tls.crt`, `tls.key` | TLS certificate and key. The server creates a self-signed pair if neither exists. |

### First admin

An admin is a key in the `admins` file. Only admin keys may call `RegisterKey` and `RevokeKey`.
Each line has a participant ID, the public key (base64 of the 32 raw bytes, see `core/auth`) and
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
MCP server (#9) will offer the same features; both build on the shared core in
`client/participant` (`dec-cli-and-mcp`). Run `office help` for all commands.

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

Other session commands: `meeting pause | resume`, `utterance retract <event-id> | --last`,
`signal turn-end | silence-long --duration D | addressed --participant P --utterance ID`,
`stage link`. Journal and artifact commands (`journal append | list`, `slides render`,
`artifact get`) exist, but the server does not implement those services yet: they fail with
the code `unimplemented`.

**The local log** is `<runtime dir>/<session>/<participant>.jsonl` (directories 0700, file 0600):
one compact JSON line per event (`core/eventlog`), with the dotted type and the event ID as a
number. It is append-only: a retraction is a new `utterance.retracted` line. The runtime
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
`retracted`) and whether you are paused. It is a filtered view of the local log
(`dec-gateway-cli`) and prints one line per change. Keys: `p` pauses or resumes, `r` retracts the
last sent sentence, `q` quits. It ends with the session.

`office meeting join <session> --mic` (or `--wav FILE`) joins as captured, runs the ears and
shows the gateway view. The ears come with #8; until then, `--mic` and `--wav` fail with the
code `unimplemented`.

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

Without `--json`, a session command prints `event <id> <type>`; `join` and `meeting log` print the
path only. Errors go to stderr.

Exit codes: `0` success, `1` error (also from the server), `2` usage error.

## Tests

We write no unit tests (`dec-contract-tests`). Every building block is tested as a black box
through its interfaces:

- A contract test starts the block with `core/contracttest` (as a real process, or as a fake that
  follows the same contract) and uses only its interfaces. `contracttest.StartServer` starts
  `office-server` with TLS and an admin key; `Server.Participant` registers a key for a test
  participant and returns authenticated clients. It never imports the package that
  implements the block.
- `tests/contract/stub_example_test.go` shows a test against a stub service.
- `tests/contract/cli_test.go` and `replicator_test.go` run the real `office` binary and check
  its output and the local log it leaves, including kills of the replicator and torn lines.
- The harness uses the binaries in `$OFFICE_BIN_DIR` (the Makefile sets it). Without it, the
  harness builds them with `go build`, so `go test ./tests/contract/...` also works.

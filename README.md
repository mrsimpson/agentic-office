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

1. Create a key pair on your device with `core/auth` (the `office` CLI gets key commands with #7).
2. Write the public key into `admins` on the server host.
3. Start or restart the server. It reads the file at start.
4. Authenticate with your key; your token may now register the keys of the other participants.

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

## Tests

We write no unit tests (`dec-contract-tests`). Every building block is tested as a black box
through its interfaces:

- A contract test starts the block with `core/contracttest` (as a real process, or as a fake that
  follows the same contract) and uses only its interfaces. `contracttest.StartServer` starts
  `office-server` with TLS and an admin key; `Server.Participant` registers a key for a test
  participant and returns authenticated clients. It never imports the package that
  implements the block.
- `tests/contract/stub_example_test.go` shows a test against a stub service.
- The harness uses the binaries in `$OFFICE_BIN_DIR` (the Makefile sets it). Without it, the
  harness builds them with `go build`, so `go test ./tests/contract/...` also works.

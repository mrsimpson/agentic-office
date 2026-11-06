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

## Layout

```
proto/office/v1/      Protobuf contracts of all interfaces that cross a process boundary
core/                 Shared core
  api/                Generated Go code for the contracts (Connect RPC)
  events/             Event types in dotted form, for example "utterance.final"
  eventlog/           Line format of the local session log (one compact JSON line per event)
  ids/                Client-generated command IDs (UUIDv7)
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
- Connect's own protocol is plain HTTP with JSON. You can call any command with `curl`:

  ```sh
  curl -H 'Content-Type: application/json' -d '{"sessionId":"demo","commandId":"…"}' \
    http://127.0.0.1:8740/office.v1.SessionService/Join
  ```

## Tests

We write no unit tests (`dec-contract-tests`). Every building block is tested as a black box
through its interfaces:

- A contract test starts the block with `core/contracttest` (as a real process, or as a fake that
  follows the same contract) and uses only its interfaces. It never imports the package that
  implements the block.
- `tests/contract/stub_example_test.go` shows a test against a stub service.
- The harness uses the binaries in `$OFFICE_BIN_DIR` (the Makefile sets it). Without it, the
  harness builds them with `go build`, so `go test ./tests/contract/...` also works.

# Agentic office

A headless office for teams of humans and agents. Humans and agents join meeting sessions as
participants of the same kind. Humans with a headset are transcribed locally. Every participant
gets a local copy of the session log. Agents act through a CLI and MCP. A stage shows the meeting
by screen share.

This file helps you to work on the code. To use the office, read the user guide.

| You want to | Read |
|---|---|
| Read everything in one place | The documentation site: <https://mrsimpson.github.io/agentic-office/> (user guide, command reference, developer pages, architecture) |
| Let an agent set up the office for you | The setup guide for agents, <https://mrsimpson.github.io/agentic-office/agent-setup.md>, and `llms.txt` at <https://mrsimpson.github.io/agentic-office/llms.txt>. The build fails when the guide names a command that does not exist |
| Run a hub, join a meeting, bring in an agent | [`USER_GUIDE.md`](USER_GUIDE.md) |
| Know what a command does | `office help`, `office <command> --help`, `office-hub -h` |
| Understand the design and its decisions | [`docs/architecture/`](docs/architecture/) (arc42) |
| Contribute, or let an agent contribute | [`AGENTS.md`](AGENTS.md) |
| Know how the office is tested | [`tests/README.md`](tests/README.md) |

## Set up

Install the tools of [Built yourself](USER_GUIDE.md#built-yourself) in the user guide: Go, `make`,
`git`, `cmake` and a C/C++ compiler. Developers also need:

- **Node.js**, for `make arch` (the arc42 CLI, started with `npx`).
- To change the protocol only: **buf**, **protoc-gen-go** and **protoc-gen-connect-go** (v2). The
  generated code is committed, so a normal build does not need them.

  ```sh
  go install github.com/bufbuild/buf/cmd/buf@latest
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install connectrpc.com/connect/v2/cmd/protoc-gen-connect-go@latest
  ```

### whisper.cpp and the models

The ears use whisper.cpp through cgo, also for voice activity detection. Every build needs it in
`third_party/whisper.cpp` (not committed), also the build of the hub, because `go.mod` points the
Go bindings of whisper.cpp there:

```sh
make third_party/whisper.cpp   # clones whisper.cpp; or: ln -s /path/to/whisper.cpp third_party/whisper.cpp
make whisper                   # builds the static libraries (libwhisper.a, libggml*.a)
make models                    # downloads ggml-small.bin (about 490 MB) and the Silero VAD model
```

- `make models` puts the models where the client looks for them (see `office help models list`).
  Change the folder with `MODEL_DIR=…`. The ears tests need these models and skip without them.
- On the reference host (CPU only), `small` needs about 0.7 s per sentence with 16 threads. Larger
  models are too slow for live use there.
- `make whisper` builds for the CPU of your computer. A change of `WHISPER_NATIVE` rebuilds
  whisper.cpp from scratch. The CI, the release binaries and the container images use
  `WHISPER_NATIVE=OFF` (see the user guide, "Built yourself").
- The Makefile sets `CGO_CFLAGS` and `CGO_LDFLAGS` for cgo. To run `go build` or `go test`
  without make, export them as in the Makefile.

### Claude Code cloud sessions

In a Claude Code cloud session, the hook `.claude/hooks/session-start.sh` runs at the start. It
builds whisper.cpp (only when the `Makefile` or the hook changed) and the programs in `bin/`. If
the environment sets `CLAUDE_WEB_OFFICE_ADMIN_KEY` (a private key in PEM), the hook writes it to a
file and installs the wrapper `office-web`. The wrapper runs `bin/office` against the shared hub
`https://office.no-panic.org`, tenant `test` by default (override with `OFFICE_WEB_HUB` and
`OFFICE_WEB_TENANT`). The hook exports no `OFFICE_*` variable for the session, because the tests
take their environment from it.

## Build and test

| Command | What it does |
|---|---|
| `make build` | Builds all packages and the binaries `bin/office-hub` and `bin/office` (needs whisper.cpp) |
| `make arch` | Validates the architecture with the arc42 CLI and runs the architecture test (`tests/architecture/`) |
| `make test` | Runs `make arch` and `make contract` |
| `make contract` | Runs `go vet` and the contract tests (`tests/contract/`) |
| `make e2e` | Quick run of the end-to-end tests with real processes (`tests/e2e/`, build tag `e2e`). It skips the tests that wait in real time on purpose (`go test -short`) and takes less than a minute. `make e2e RUN=TestEarsPause` runs the named tests, slow ones included |
| `make e2e-all` | Runs every end-to-end test (about 4 minutes); the CI uses it. The ears tests need the models and are skipped without them |
| `make performance` | Runs the quality scenarios with their timing criteria and their full lengths (build tags `e2e,performance`; about 35 minutes) and lists their numbers. Meant for the reference host |
| `make docs` | Builds the documentation site (`docs/site/`, Starlight) into `docs/site/dist/`. It builds the programs first, because the command reference comes from their help (needs whisper.cpp and Node.js) |
| `make docs-serve` | Builds the site and serves it at `http://localhost:4321/agentic-office/` |
| `make lint` | Checks the Protobuf contracts with `buf lint` |
| `make generate` | Regenerates the Go code of the contracts next to their `.proto` files |
| `make check-generated` | Fails when the committed generated code differs from what the contracts produce |

End-to-end tests check behaviour only and never fail on timing; timing criteria belong in the
performance tests. [`tests/README.md`](tests/README.md) describes the tests.

### Try your build

```sh
bin/office help          # the commands; every command has --help
bin/office-hub -h        # the flags of the hub and how to start it
```

[Try everything on one computer](USER_GUIDE.md#5-try-everything-on-one-computer) in the user guide
starts a hub and three participants with one script.

### Continuous integration

The CI (`.github/workflows/ci.yml`) runs on every push to `main` and on every pull request. A job
runs only when files that matter to it changed:

| Job | Runs when these change |
|---|---|
| Architecture (`make arch`) | always |
| Contracts and contract tests (`make check-generated`, `make contract`) | Go code, Protobuf contracts, `go.mod`, `buf*.yaml`, `Makefile`, `tests/`, the CI workflow |
| End-to-end tests (`make e2e-all`) | the same as the contract tests |
| Docsite (`make docs`) | `docs/`, the user guide, the READMEs, `AGENTS.md`, `hub/`, `client/`, `go.mod`, `go.sum`, `Makefile`, the docsite scripts and workflows |
| Container images build | `Dockerfile`, `.dockerignore`, `hub/`, `client/`, `go.mod`, `go.sum`, `Makefile`, the CI workflow |

A skipped job counts as passed. A manual run of the workflow runs every job. The CI builds the
pinned whisper.cpp commit (`WHISPER_REF` in the Makefile) and caches it and the models. The
performance tests do not run in the CI.

The workflow "Architecture review" (`.github/workflows/architecture-review.yml`) shows on every
pull request whether it changes the architecture. Its job "Architecture diff" runs
`arc42 diff origin/main...HEAD` and writes the result as one comment on the pull request; later
pushes update the same comment. When the architecture changed, the job also builds a review site
with the changes marked (`arc42 build --diff`):

- on GitHub Pages at `https://<owner>.github.io/<repo>/pr-<number>/`, from the branch `gh-pages`.
  This needs Pages to deploy from the branch `gh-pages`, folder `/` (repository settings). The
  folder is removed when the pull request closes;
- as one HTML file in a workflow artifact, which works without Pages.

The workflow "Docsite" (`.github/workflows/docs.yml`) publishes the documentation site to the root of
the branch `gh-pages` on every push to `main` that changes what the site shows. The site reads
`USER_GUIDE.md`, `README.md`, `tests/README.md` and `AGENTS.md` at build time and builds the command
reference from `office help` and `office-hub -h`; none of these copies is committed. The
architecture is the section `/architecture/`. The build checks every link and fails on a broken one,
so pull requests build the site too (the job "Docsite"). The publish replaces the root of the branch
and leaves the folders `pr-<number>/` of the review sites alone; the review workflow never touches
the root. Both push to the same branch and try again when a push is rejected.

Consistency findings of the diff appear in the comment but do not fail the job; the job
Architecture validates the model. Pull requests from forks get the job summary only.

## Layout

The layout follows the building blocks of chapter 5 of the architecture. Every building block and
every interface has exactly one path (see [`AGENTS.md`](AGENTS.md)); `make arch` checks it.

```
hub/                      bb-office-hub: wiring of the hub (binary office-hub)
  session/                bb-session-service; writes sessions to the archive
    commandsapi/          if-session-commands: contract and generated code
    streamapi/            if-session-stream: contract, generated code, the event types and their
                          dotted form (streamapi/events), Merkle code (streamapi/merkle),
                          stored form of a session log: lines and tree head (streamapi/logfile)
    importapi/            if-session-import: contract and generated code
  archive/                bb-archive: local Git repositories (hub and tenants), push mocked
    storage/              if-archive-storage: write files as one commit, read them back (in-process)
  identity/               bb-identity
    api/                  if-auth-api: contract, generated code, client side (api/auth: key files,
                          challenge signing, tokens, TLS trust)
    tokencheck/           if-token-check: who makes a call (in-process)
  records/                bb-records: decisions, minutes and notes as YAML in the archive
    decision/api/         if-decision-api: contract and generated code
    minutes/api/          if-minutes-api: contract and generated code
    note/api/             if-note-api: contract and generated code
  tasks/                  bb-tasks: tasks as local YAML drafts in the archive
    api/                  if-task-api: contract and generated code
  slides/                 bb-slides: slide decks, input as YAML and output as HTML in the archive
    api/                  if-slides-api: contract and generated code
  documents/              bb-documents: text documents as Markdown files in the archive
    api/                  if-document-api: contract and generated code
  admin/                  bb-admin: the admin view, rendered on the server
    web/                  if-admin-web: addresses, pages and design of the admin view
  stage/                  bb-stage: renders the stage from the session stream
    web/                  if-stage-web: addresses, page, regions and event stream of the stage
client/                   bb-participant-client: wiring of the client (binary office)
  core/                   bb-client-core: shared core of CLI, MCP server and ears
    api/                  if-client-core: the Go interface of the core, its types, command IDs and
                          the shared shapes of input and output
  cli/                    bb-cli
    commands/             if-cli: commands, their help, exit codes and the form of --json output
  mcp/                    bb-mcp
    tools/                if-mcp: instructions, tools and their input types
  replicator/             bb-replicator
    control/              if-replication: start, find and stop a replicator (in-process)
    locallog/             if-local-log: read the local session log
  ears/                   bb-ears
    audio/                if-audio-in: microphone recorder and WAV input, 20 ms frames at 16 kHz
  agentrunner/            bb-agent-runner: keeps an agent in a meeting over ACP
    acpclient/            if-acp-client: client side of the Agent Client Protocol
    forwardingjudge/      if-forwarding-judge: request, answer and JSON schema of a forwarding judge
    systemone/            if-decision-model: request, state, questions and answers of the System One API
tests/                    external actors: may use interfaces only (see tests/README.md)
docs/architecture/        the arc42 architecture
docs/site/                the documentation site (Starlight); its pages are made at build time
docs/testing/             manual tests (the headset test)
scripts/                  try-local.sh, which the user guide describes
```

## Contracts

The contract of an interface lies with its provider (see the layout). The README does not copy it.

- **Protobuf contracts** are the `.proto` files in the packages of the interfaces above, with
  their generated code. All interfaces are Connect RPC services (`dec-connect-rpc`). The comments
  in the `.proto` files define the fields and the rules of each call.
- **Rules of a building block** are in the package comment of the block (`doc.go`, or the file
  `main.go` for the hub): `hub/session` (sessions, the import, the Merkle tree, the archive
  layout of a session, the restore after a restart and the schema of a session), `hub/archive`,
  `hub/identity` (users, keys, tokens, tenants), `hub/records`, `hub/tasks`, `hub/documents`,
  `hub/slides`, `hub/stage`, `hub/admin`, `client/replicator`, `client/ears`, `client/agentrunner`,
  `client/mcp` and `client/cli`. The package `hub/session/streamapi/merkle` has the exact bytes of
  the Merkle tree and the verification code.
- **The commands of the CLI** are in `client/cli/commands`: the help of every command, the
  topics, the exit codes and the `--json` output. The same text is what `office help` prints.
- **Hub flags** are in `hub/main.go`; `office-hub -h` prints them.

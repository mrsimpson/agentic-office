# Agentic office

A headless office for teams of humans and agents. Humans and agents join meeting sessions as
participants of the same kind. Humans with a headset are transcribed locally. Every participant
gets a local copy of the session log. Agents act through a CLI and MCP. A stage shows the meeting
by screen share.

To try it on your computer, see the [user guide](USER_GUIDE.md). The architecture is in [`docs/architecture/`](docs/architecture/) (arc42). Rules for contributors
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
`third_party/whisper.cpp` (not committed), also the build of the hub, because `go.mod` points
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
`small` needs about 0.7 s per sentence with 16 threads; larger models are too slow for live use
there. Which model the ears choose is described in
[Ears: speak into the meeting](#ears-speak-into-the-meeting).

`make whisper` builds whisper.cpp for the CPU of your computer, which is fastest. Such a build
can stop with `SIGILL` (illegal instruction) on another CPU. To build a client that you copy to
other computers, use `make whisper build WHISPER_NATIVE=OFF`: it needs x86-64 with AVX2, or
any ARMv8 CPU. The CI, the release binaries and the container images build this way. A change of
`WHISPER_NATIVE` rebuilds whisper.cpp from scratch.

The Makefile sets `CGO_CFLAGS` and `CGO_LDFLAGS` for cgo. To run `go build` or `go test`
without make, export them as in the Makefile.

## Build and test

| Command | What it does |
|---|---|
| `make build` | Builds all packages and the binaries `bin/office-hub` and `bin/office` (needs whisper.cpp) |
| `make arch` | Validates the architecture with the arc42 CLI and runs the architecture test (`tests/architecture/`); needs Node.js |
| `make test` | Runs `make arch` and `make contract` |
| `make contract` | Runs `go vet` and the contract tests (`tests/contract/`) |
| `make e2e` | Quick run of the end-to-end tests with real processes (`tests/e2e/`, build tag `e2e`). It skips the tests that wait in real time on purpose (`go test -short`) and takes less than a minute. `make e2e RUN=TestEarsPause` runs the named tests, slow ones included |
| `make e2e-all` | Runs every end-to-end test (about 4 minutes); the CI uses it. End-to-end tests check behaviour only and never fail on timing. The ears tests need the models and are skipped without them |
| `make performance` | Runs the quality scenarios with their timing criteria and their full lengths (build tags `e2e,performance`; 30 minutes of speech, a 60 s network drop; about 35 minutes) and lists their numbers. Meant for the reference host |
| `make lint` | Checks the Protobuf contracts with `buf lint` |
| `make generate` | Regenerates the Go code of the contracts next to their `.proto` files |
| `make check-generated` | Fails when the committed generated code differs from what the contracts produce |

The CI (`.github/workflows/ci.yml`) runs on every push to `main` and on every pull request. A job
runs only when files that matter to it changed:

| Job | Runs when these change |
|---|---|
| Architecture (`make arch`) | always |
| Contracts and contract tests (`make check-generated`, `make contract`) | Go code, Protobuf contracts, `go.mod`, `buf*.yaml`, `Makefile`, `tests/`, the CI workflow |
| End-to-end tests (`make e2e-all`) | the same as the contract tests |
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

Consistency findings of the diff appear in the comment but do not fail the job; the job
Architecture validates the model. Pull requests from forks get the job summary only.

Run the hub by hand:

```sh
bin/office-hub keygen ~/.config/agentic-office/hub-signing.key   # once
bin/office-hub -addr 127.0.0.1:8740 -signing-key ~/.config/agentic-office/hub-signing.key
```

The hub speaks only TLS and needs an access token for every call. See
[Hub setup](#hub-setup) for the first start.

## Layout

The layout follows the building blocks of chapter 5 of the architecture. Every building block and
every interface has exactly one path, and no two share a path. An interface is its own package
inside the path of its provider and owns its types. An interface that crosses the boundary of the hub or the client belongs to that
black box and is realized by one part (for example the meeting session realizes the session
commands of the office hub). There is no shared package. `make arch` checks that every import between building
blocks goes through an interface that the importing block requires, and that no two elements
share a path (concept-module-boundaries).

```
hub/                      bb-office-hub: wiring of the hub (binary office-hub)
  session/                bb-session-service; writes sessions to the archive
    commandsapi/          if-session-commands: contract and generated code
    streamapi/            if-session-stream: contract, generated code, the event types and their
                          dotted form (streamapi/events), Merkle code (streamapi/merkle),
                          stored form of a session log: lines and tree head (streamapi/logfile)
    importapi/            if-session-import: contract and generated code
  archive/                bb-archive: local Git repository, push mocked
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
  stage/                  bb-stage: renders the stage from the session stream
    web/                  if-stage-web: addresses, page, regions and event stream of the stage
client/                   bb-participant-client: wiring of the client (binary office)
  core/                   bb-client-core: shared core of CLI, MCP server and ears
    api/                  if-client-core: the Go interface of the core, its types, command IDs and
                          the shared shapes of input and output
  cli/                    bb-cli
    commands/             if-cli: commands, exit codes and the form of --json output
  mcp/                    bb-mcp
    tools/                if-mcp: instructions, tools and their input types
  replicator/             bb-replicator
    control/              if-replication: start, find and stop a replicator (in-process)
    locallog/             if-local-log: read the local session log
  ears/                   bb-ears
    audio/                if-audio-in: microphone recorder and WAV input, 20 ms frames at 16 kHz
  agentrunner/            bb-agent-runner: keeps an agent in a meeting over ACP
    acpclient/            if-acp-client: client side of the Agent Client Protocol
    decision/             if-llm-judge: request, answer and JSON schema of an LLM judge
    systemone/            if-decision-model: request, state, questions and answers of the System One API
tests/                    external actors: may use interfaces only
  acpdouble/              test double of an ACP agent harness (no model)
  decisiondouble/         test double of an LLM judge, command kind (no model)
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
| Session commands | `office.sessioncommands.v1.SessionCommandsService` | unary |
| Session stream | `office.sessionstream.v1.SessionStreamService` (`Subscribe`, `GetEvents`) | server stream from a cursor; events after a cursor |
| Session import | `office.sessionimport.v1.SessionImportService` | unary, one call per page |
| Auth API | `office.identity.v1.IdentityService` | unary |
| Decisions | `office.decision.v1.DecisionService` | unary |
| Minutes | `office.minutes.v1.MinutesService` | unary |
| Notes | `office.note.v1.NoteService` | unary |
| Tasks | `office.task.v1.TaskService` | unary |
| Slides | `office.slides.v1.SlidesService` | unary |

Rules that hold for all of them:

- Every command carries a `command_id` that the client creates: a UUIDv7. A repeated command
  returns the stored result and sets `duplicate` (`dec-idempotent-commands`).
- Every event has an `id` that grows by one inside a session, and a `type` in dotted form. The ID
  is 32 bits wide, so its JSON form is a number.
- Every call carries an access token in the header `Authorization: Bearer <token>`, except
  `GetChallenge` and `ExchangeChallenge`. See [Identity](#identity).
- Connect's own protocol is plain HTTP with JSON. You can call any command with `curl`
  (`--cacert` trusts the self-signed certificate of the hub):

  ```sh
  curl --cacert <data-dir>/tls.crt \
    -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
    -d '{"sessionId":"demo","commandId":"…"}' \
    https://127.0.0.1:8740/office.sessioncommands.v1.SessionCommandsService/Join
  ```

### Meeting session

The meeting session (`hub/session`) is implemented. It keeps its logs in memory and writes them
to the archive in the background (see [Archive](#archive)). After a restart it reads them back
and the peers fill in what the archive does not have yet (see
[Restore after a restart](#restore-after-a-restart)). The package comment in `hub/session/doc.go`
has the details.
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

### Session import

The meeting session also realizes the session import (`hub/session/import.go`,
`dec-session-import`). `ImportConversation` takes one page of a conversation from outside the
office; the proto comment in `hub/session/importapi/office/sessionimport/v1/import.proto` has
the full contract. In short:

- The first page creates the session; the session must have no events. It carries the
  conversation (`source` is required, `title` is optional) and writes `session.imported` as
  event 1.
- Each page may add participants (`key`, `display_name`, `kind`) and utterances (`speaker` is a
  key, `text`, `modality`, optional `time`), at most 1000 of each. A participant becomes
  `participant.joined` with `imported: true` and the ID `imported:<key>`; registered IDs never
  contain `:`. An utterance becomes `utterance.final` with `originalTime`.
- The page with `complete` writes `import.completed` (with the number of utterances). It also
  starts a write to the archive, like `session.ended`.
- Every page has its own `command_id`; a repeated page returns its stored result with
  `duplicate`. Every event of a page carries that command ID and the importer as actor. The
  response has the event range and a tree head signed over the log up to the page.
- Until the import is complete, the session takes only further pages of the importer; session
  commands fail with `failed_precondition`. After it, the session is a normal session: join,
  minutes, end. Nobody can mark an imported sentence off the record yet (#50).
- What the signature means: the tree heads over an imported log prove which participant imported
  it and that it has not changed since. They do not prove that the speakers said it.

The schema of the session is `office.sessionstream.v1/1`. Stored sessions of another schema are
readable but not continued (`dec-session-schema`).

### Checking a session log (Merkle tree)

The events of a session are the leaves of a Merkle tree in the style of RFC 9162 (Certificate
Transparency), and the hub signs its tree heads (`dec-merkle-log`). Package
`hub/session/streamapi/merkle` has the exact bytes and the verification code for clients:

- Leaf hash = SHA-256(0x00 || leaf), node hash = SHA-256(0x01 || left || right). Event `n` is
  leaf `n-1`. The leaf of an event is its deterministic binary Protobuf encoding, as it is in the
  log of the hub (with `leaf_hash` empty).
- A placeholder of an utterance that is off the record carries `leafHash`: the leaf hash of the
  original. Verifiers use it instead of hashing the placeholder, so a copy with placeholders still
  verifies. Any other event with a `leafHash` fails the check.
- A signed tree head has the session ID, the schema of the session, the tree size, the root hash,
  the Ed25519 signature and the public key that signed. The signed message is
  `merkle.TreeHeadMessage`; it covers the schema.
- Schema (`dec-session-schema`): a session belongs to the schema it was started with, the
  version of the session contract. The schema of this code is `sessionstreamv1.Schema`, for
  example `office.sessionstream.v1/1`: the Protobuf package of the events and a revision. The revision goes up with every
  change of the events or their encoding that changes a leaf. Every check in package `merkle`
  fails with `merkle.ErrSchema` (and `merkle.ErrVerify`) for a tree head of another schema, so a
  verifier checks a session only with code of its own schema.
- `session.ended` carries the signed tree head of the log before it (`sessionEnded.treeHead`).
- `GetTreeHead` returns the signed head of the current log, or of its first `tree_size` events.
  `GetConsistencyProof` proves that the log of `second_size` events extends the log of
  `first_size` events. `GetInclusionProof` proves that one event is in a tree. A size beyond the
  log fails with `out_of_range`.
- `GetHubKey` returns the public key of the hub. The operator gives the hub its key file at
  start (`-signing-key`, see [Signing key](#signing-key)). Start the hub with the same file
  every time, so heads signed before a restart still verify.
- Check a copy: `merkle.VerifyEvents(events, head, hubKey)` (all events from 1 to the tree size),
  `merkle.VerifyEventInclusion` and `merkle.VerifyTreeHeadConsistency`.

### Archive

The archive (`hub/archive`) stores the sessions, the records (decisions, minutes and notes,
see [Records](#records-decisions-minutes-and-notes)), the tasks (see [Tasks](#tasks)) and the slide decks (see [Slides](#slides)) of the hub in a local Git repository on the hub
host, by default `<data-dir>/archive` (`-archive-dir`). It creates the repository (branch `main`)
on the first start. Every write is one commit. The push to a remote is mocked: it does nothing
and reports success (#43). The hub needs the `git` command line tool in `PATH`. It runs git
without your system and global git configuration, so hooks and commit signing do not apply.

The meeting session writes every session that changed:

- every `-archive-interval` (default 30s), and at once when the session ends;
- once more when the hub stops (SIGINT or SIGTERM);
- in the background: commands and the stream never wait for Git. At most one write is in
  progress. A write that takes longer than the interval makes the next write take everything
  that came meanwhile, so writes never queue up. A failed write is logged and repeated with the
  next write.
- after the records: the decisions, minutes, notes, tasks and slide decks are committed before
  the call returns (`dec-records-synchronous`), and they go before a session write that waits.
  A record waits at most for the one session write that is in progress, never for a queue.

A session is stored in the directory `sessions/<name>/`. `<name>` is the session ID if it is safe
as a file name (letters, digits, `.`, `_`, `-`, at most 128 characters, not starting with `.`),
else `=` plus the base64url form of the ID without padding (`team/weekly 1` becomes
`=dGVhbS93ZWVrbHkgMQ`). Each write puts two files into one commit:

| File | What it is |
|---|---|
| `log.jsonl` | The raw session log: one line per event, the same lines as the local log of a participant (`hub/session/streamapi/logfile`). It keeps the original text of sentences that are off the record and their `utterance.off_the_record` marks. Reading a session through the interfaces gives placeholders. |
| `tree-head.yaml` | The tree head signed by the hub over exactly this log: `session_id`, `schema` (the schema of the session), `tree_size` (the number of lines), `root_hash`, `signature`, `public_key` (bytes in base64). |

Check a stored session:

```go
evs, _ := logfile.UnmarshalLines(logData)
head, _ := logfile.UnmarshalTreeHead(headData)
err := merkle.VerifyEvents(evs, head, hubKey) // hubKey from GetHubKey
```

`git log -- sessions/<name>/log.jsonl` lists the writes of a session; every commit extends the
log of the one before.

`sessions/+state.yaml` says whether the archive is complete: `complete: true` when the hub stopped
after a last write of every session, `false` from the next start on. No session name starts with
`+`.

### Restore after a restart

When the hub starts, it restores every session from the archive (`dec-restore-from-peers`):

1. It reads `log.jsonl` and `tree-head.yaml` of every session and checks the log against the
   head with its signing key (`merkle.VerifyEvents`). A log that verifies is the start of the
   session: the events keep their IDs, repeated command IDs still return their events, ended
   sessions stay ended, and the next write appends to the stored lines.
2. The events after the last write are lost on the hub, but the peers have them. To let a peer
   prove them, every `Subscribe` response with the last event the hub has carries `tree_head`:
   the head signed by the hub over the events 1 to that ID. The replicator keeps it next to the
   local log in `<participant>.tree-head.yaml`.
3. A peer whose cursor is beyond the end (`out_of_range`) calls `OfferLog` with the events after
   the last event of the hub (`after_event_id`) and its signed head. The hub checks the
   signature with its own key, that its events up to `after_event_id` and the offered events
   give the root hash, and a consistency proof between its own current head and the head of the
   copy. Then it appends the missing events with their IDs and answers `accepted: true`.
4. A copy that is not longer than the log of the hub is a no-op (`accepted: false`), so of two
   peers that offer at once the first wins. A copy that does not verify fails with
   `invalid_argument`. A copy signed by the hub that does not extend its log (a fork) fails with
   `failed_precondition`. A stage link cannot offer.
5. Until a copy is accepted, or for at most `-restore-wait` (default 10s), the commands of a
   restored session that has not ended fail with `unavailable`, so no new event can take the ID of
   a lost one. Repeat the command (it is idempotent). After a clean stop (`complete: true`) there
   is no wait. A stored log that does not verify is not used: its commands fail with `data_loss`
   until a peer offers a whole copy (`after_event_id` 0).
6. A session keeps its schema (`dec-session-schema`). A stored session of another schema than
   the hub's is readable if its signature verifies and the hub's leaves give its root hash:
   events, stream, tree heads (with the session's schema) and proofs. But the hub does not
   continue it: every new command and every `OfferLog` fails with `failed_precondition`, and the
   error names both schemas. A repeated command ID still returns its stored event. `OfferLog`
   also fails with `failed_precondition` for a copy whose tree head has another schema.

The replicator does steps 2 and 3 by itself. After the hub has its cursor again, it checks that
the head of the hub at the cursor has the root hash of the local log, and then follows the stream.
If the roots differ, or the hub rejects the copy, the replicator stops with an error: the local
log forks from the hub and cannot go on.

Open points (#47): a session that the hub never wrote to
the archive is unknown after a crash until a peer offers a whole copy, and a new join before that
starts a new log; a peer that comes back only after the wait has a copy that forks; the CLI and
MCP do not repeat a command that fails with `unavailable` by themselves.

### Identity

Identity (`hub/identity`) is implemented. The package comment in `hub/identity/doc.go` has
the details. In short:

- Every actor (each human device, each agent) has its own Ed25519 key pair, created locally with
  `hub/identity/api/auth`. Only the public key goes to the hub.
- An admin registers a public key for a participant with `RegisterKey`. The admin chooses the
  participant ID, for example `ada`. A participant can have several keys. `RevokeKey` removes a
  key; its tokens stop working at once.
- A client gets a challenge (`GetChallenge`), signs it and exchanges it for an access token
  (`ExchangeChallenge`). The token lives 15 minutes (`-token-ttl`). `auth.TokenSource` does this
  and renews the token before it expires; `auth.Bearer` attaches it to every Connect call.
- `CreateStageLink` returns a read-only link `https://<hub>/stage/<session>?token=<token>`
  for one session. Its token only allows `Subscribe` for that session. It lives 24 hours
  (`-stage-link-ttl`). Open it in a browser to see the stage (below).
- Registered keys are stored in `<data-dir>/identity.json`. Tokens are kept in memory only, so
  clients authenticate again after a restart of the hub.

### Records: decisions, minutes and notes

The records (`hub/records`) hold decisions, minutes and notes (`dec-records-per-type`). Each type
has its own interface. The package comment in `hub/records/doc.go` has the details. In short:

- **Decisions** (`RecordDecision`, `ListDecisions`) and **notes** (`AddNote`, `ListNotes`) have
  a text, an optional session ID, the author and the author's kind (`human` or `agent`). The hub
  takes the author and the kind from the access token: `author_participant_id` must be the
  participant of the token. A stage link cannot use the records.
- **Minutes** (`GetMinutes`, `ListMinutes`) are read only. While a session runs, its
  participants write the running minutes with `minutes update` (`minutes.updated` in the log).
  `EndSession` stores the text of the last `minutes.updated` event as the minutes of the
  session, then writes `session.ended`. Their author is the participant who wrote that version.
  A session without minutes, or with blank minutes, stores nothing. What "confirmed minutes"
  means is still open (#22). `GetMinutes` returns the newest stored minutes of a session, or
  `not_found`.
- Writes are idempotent by `command_id`, per type. The same `command_id` with another text,
  author or session fails with `already_exists`.
- The list calls return records after the cursor `after_id`, oldest first, optionally of one
  session. They return at most `limit` records (default 100, at most 1000). To read the next
  page, pass the ID of the last record. IDs start at 1 per type.
- Tasks are not records here; they have their own part of the hub (see [Tasks](#tasks)).
- Do not put a sentence that is off the record into minutes, decisions or notes
  (`dec-off-the-record`). The hub does not check this.

Every record is one YAML file in the archive, committed before the call returns, one commit per
record: `decisions/000001.yaml`, `minutes/000001.yaml`, `notes/000001.yaml` (the ID with at least
six digits). The fields are those of the contract in snake_case; a text with line breaks is a
literal block:

```yaml
id: 1
text: |-
    We ship on Friday.
    Ada writes the release notes.
author_participant_id: "ada"
author_kind: "AUTHOR_KIND_HUMAN"
session_id: "weekly"
time: "2026-11-07T18:23:46.117670700Z"
command_id: "01a1b740-97c5-733b-bc48-e7e52a691139"
```

Minutes have `session_id`, `text`, `author_participant_id`, `author_kind`, `time` and
`command_id` (`office/session-end/<command ID of EndSession>`). The hub reads all records from
the current commit of the archive when it starts, so they survive a restart.

### Tasks

The tasks (`hub/tasks`, `office.task.v1.TaskService`) are work items of the team. The package
comment in `hub/tasks/doc.go` has the details. In short:

- For now every task is a **local draft** of the hub (`dec-yaml-git-storage`). Later the hub syncs
  tasks with the task tool of the team, and the synced task replaces the draft (#40).
- A task is named by its **ref**, for example `local:42`. After the sync, refs of other schemes
  appear (for example `github:owner/repo#12`). Treat a ref as opaque: keep it and pass it back.
  `source` says where the task lives; today it is always `TASK_SOURCE_LOCAL`.
- A task has a title, a description, an assignee (a participant ID), a status (`open`,
  `in_progress`, `done`, `cancelled`) and an optional session ID. The hub checks neither the
  assignee nor the session. It records the author and its kind from the token, who changed the
  task last, the times, and a `revision` that starts at 1 and grows with every change.
- `CreateTask` needs a title; the status defaults to open. `UpdateTask` changes only the fields
  it sets; an empty assignee or session removes it. An update that changes nothing makes no new
  revision. `GetTask` and `UpdateTask` answer `not_found` for an unknown ref.
- `ListTasks` filters by statuses, assignee and session, oldest first, at most `limit` tasks
  (default 100, at most 1000). If more tasks match, `next_page_token` is set: pass it as
  `page_token` to read the next page.
- Writes are idempotent by `command_id`. A repeated create or update returns the task with
  `duplicate` set and changes nothing. A `command_id` used for another task or another kind of
  command fails with `already_exists`.
- Any participant may read and change every task. A stage link cannot use the tasks.

Every task is one YAML file in the archive, `tasks/000001.yaml` (the local number with at least
six digits). Creating it is one commit; every change rewrites the file in a new commit, so
`git log -p -- tasks/000001.yaml` shows the history of the task. The fields are those of the
contract in snake_case, plus `command_ids`: the commands that created and changed the task.
The call returns after the commit. The hub reads all tasks back when it starts.

```yaml
ref: "local:1"
source: "TASK_SOURCE_LOCAL"
title: "Write the release notes"
description: ""
assignee_participant_id: "bot"
status: "TASK_STATUS_DONE"
session_id: "weekly"
author_participant_id: "ada"
author_kind: "AUTHOR_KIND_HUMAN"
create_time: "2026-11-07T19:02:11.401253Z"
updated_by_participant_id: "bot"
update_time: "2026-11-07T19:40:03.118871Z"
revision: 2
command_id: "01a1b740-97c5-733b-bc48-e7e52a691139"
command_ids:
    - "01a1b740-97c5-733b-bc48-e7e52a691139"
    - "01a1b7a2-1c3e-7e0a-9a55-02c4b1f4d6e2"
```

Like the records, a task write waits for its commit. It goes before a waiting session write, so
it waits at most for the one session write that is in progress (see [Archive](#archive)).

### Slides

The slides part (`hub/slides`, `office.slides.v1.SlidesService`) renders slide decks and keeps
them. The package comment in `hub/slides/doc.go` has the details. In short:

- `RenderDeck` takes a deck: an optional title and at least one slide. Every slide has a title,
  and may have bullets and notes. The result is a deck with the ID `slides/<n>` (n starts at 1).
  It holds the input as the client sent it, the author and its kind (from the token), the time,
  the command ID, and the rendered output with the media type `text/html; charset=utf-8`.
- The output is one self-contained HTML document for a screen share: one section per slide,
  black on white, large text. It has no scripts and loads nothing. All text is HTML-escaped and
  shown as plain text. Notes are in the document but hidden.
- The same deck always gives byte-equal output (`con-no-intelligence`): no time or random value
  is in it. The ID and `create_time` are outside the output.
- `RenderDeck` is idempotent by `command_id`. The same `command_id` with another deck or another
  author fails with `already_exists`. A new `command_id` with the same deck gives a new ID.
- `GetDeck` fetches a deck with its input and output by ID; an unknown ID fails with
  `not_found`. Any participant can fetch every deck. A stage link cannot use the slides
  interface: the stage renders on the hub.
- **Artifact** is the general word for what the stage shows: `stage show` takes an ID with its
  type in front. Today `slides/<n>` is the only type. The meeting session does not check that a
  shown artifact exists; the stage shows a note for an unknown ID.

Every deck is two files in the archive, written in one commit: `slides/000001.yaml` holds the
input (the fields of the contract in snake_case, without the output), and `slides/000001.html`
holds the rendered deck. The call returns after the commit. The hub reads all decks back when it
starts. The output is stored, not rendered again: a deck stays exactly what was shown, even if a
later hub renders differently. Rendering the stored input again gives the same output.

```yaml
id: "slides/1"
title: "Budget 2027"
slides:
    - title: "Plan"
      bullets:
        - "Hire two people"
        - "Open an office in Lyon"
      notes: "Say the numbers slowly."
    - title: "Questions"
      bullets: []
      notes: ""
author_participant_id: "bot"
author_kind: "AUTHOR_KIND_AGENT"
create_time: "2026-11-07T18:59:31.48928053Z"
command_id: "01a1b761-5420-7b0d-82a7-6d9a249506f9"
```

Like the records and the tasks, a render waits for its commit. It goes before a waiting session
write, so it waits at most for the one session write that is in progress (see [Archive](#archive)).

### Stage

The stage (`hub/stage`) is the web page of one session for the screen share. The package
comment in `hub/stage/doc.go` has the details. In short:

- Create a link with `office stage link` (or `CreateStageLink`) and open it in a browser. Share
  that browser window in the meeting tool.
- The page shows the shown artifact, the recent contributions, all participants with their
  capture status (captured, paused, not captured, left; agents are marked "agent"), the raised
  hands and the running minutes. Off the record contributions disappear. Large text, white on black.
- An [imported session](#import-a-conversation) is marked: a line under the header names the
  source, the title and who imported it, and says that the office did not see the conversation
  happen. The status says "Imported" instead of "Live". Imported speakers are marked "imported",
  and imported contributions show their original time in UTC.
- It is rendered on the hub and updated live through server-sent events
  (`/stage/<session>/events?token=…`). There is no frontend build and nothing is loaded from
  outside. A shown artifact is embedded in a sandboxed iframe.
- A hand stays raised until its participant speaks, raises it again or leaves (#24).
- Without a valid stage link for the session, the page fails with 401 (no, unknown or expired
  token) or 403 (a participant token, or a link for another session) and shows no session data.

## Hub setup

All files of the hub are in its data directory (`-data-dir`), by default
`<config>/agentic-office/hub`. `<config>` is `~/.config` on Linux (or `$XDG_CONFIG_HOME`) and
`~/Library/Application Support` on macOS:

| File | What it is |
|---|---|
| `admins` | Admin keys. You write it by hand. |
| `identity.json` | Registered participants and keys. The hub writes it. |
| `tls.crt`, `tls.key` | TLS certificate and key. The hub creates a self-signed pair if neither exists. |
| `archive/` | The Git repository of the archive (`-archive-dir`): sessions, decisions, minutes, notes, tasks and slide decks. The hub creates it and commits into it. See [Archive](#archive). |

Flags of the hub (`bin/office-hub -h` lists them all):

| Flag | Default | What it sets |
|---|---|---|
| `-addr` | `127.0.0.1:8740` | Listen address |
| `-data-dir` | `<config>/agentic-office/hub` | Data directory |
| `-archive-dir` | `<data-dir>/archive` | Git repository of the archive |
| `-archive-interval` | `30s` | Time between two writes of changed sessions. Keep it above the usual commit time. |
| `-restore-wait` | `10s` | After a restart, how long a restored session that has not ended waits for a peer to fill in (see [Restore](#restore-after-a-restart)) |
| `-tls-cert`, `-tls-key`, `-tls-hosts` | see [TLS](#tls-and-your-own-certificate) | TLS certificate |
| `-signing-key` | none, required | Key file that signs tree heads (see [Signing key](#signing-key)) |
| `-admins` | `<data-dir>/admins` | Admin keys |
| `-public-url` | `https://<listen address>` | Base URL in stage links |
| `-token-ttl`, `-stage-link-ttl` | `15m`, `24h` | Lifetime of access tokens and stage links |

### Signing key

The hub signs the tree heads of session logs with an Ed25519 key. You, the operator, give the hub
this key at every start with `-signing-key FILE`. The hub never creates a signing key by itself.
It does not start without the flag, or if the file is missing, broken or readable by others.

Create a key file once:

```sh
bin/office-hub keygen /etc/office/hub-signing.key
```

- `keygen` writes the private key as PEM (PKCS #8) with mode 0600 and prints the public key.
- It refuses to overwrite an existing file.
- The hub logs the public key at start: `signing key of tree heads …`.
- Keep the file with your other secrets and back it up. Without it, old tree heads cannot be
  checked against the hub key. Replacing it is a deliberate act: heads signed with the old key
  then no longer verify against `GetHubKey`.
- How clients pin the public key is still open (#41).

### First admin

An admin is a key in the `admins` file. Only admin keys may call `RegisterKey` and `RevokeKey`.
Each line has a participant ID, the public key (base64 of the 32 raw bytes, see `hub/identity/api/auth`) and
an optional display name:

```
# participant  public key                                     display name
ada            hGs7aXk0n1V6lH1d3mFzWQ0bX2j7zv8nq3C4oD5pE6A=  Ada Lovelace
```

1. Create a key pair on your device: `office key create` prints the public key.
2. Write the public key into `admins` on the hub host.
3. Start or restart the hub. It reads the file at start.
4. Authenticate with your key; your token may now register the keys of the other participants
   with `office admin key register` (see [Participant client](#participant-client-office-cli)).

To remove an admin, delete the line and restart. Admin keys cannot be revoked through the API.

### TLS and your own certificate

On the first start, the hub creates a self-signed certificate for `localhost`, `127.0.0.1`,
`::1` and the host name (`-tls-hosts` changes the list) and logs its SHA-256 fingerprint. It is
valid for 820 days, because macOS and the browsers on it reject TLS server certificates that are
valid for more than 825 days. The hub warns at start when its certificate is valid for longer;
delete `tls.crt` and `tls.key` and restart to get a new one.

Clients trust it by its file: copy `tls.crt` to the client (`<config>/agentic-office/client/hub.crt`
or `OFFICE_CA_CERT`). With a certificate file, the client trusts only the certificates in it, not
the system roots, and checks them itself on every system (`auth.TLSConfig`, `auth.HTTPClient`;
or `curl --cacert`).

To use your own certificate, for example from your CA or Let's Encrypt:

```sh
bin/office-hub -tls-cert /etc/office/fullchain.pem -tls-key /etc/office/privkey.pem \
  -public-url https://office.example.org:8740
```

- The certificate file is PEM and may hold the whole chain. The key file is PEM (PKCS #8, PKCS #1
  or SEC 1).
- Or put both into the data directory as `tls.crt` and `tls.key`. The hub never overwrites
  existing files.
- Clients then trust the system roots: give them no certificate file.
- `-public-url` is the address in stage links. It defaults to `https://<listen address>`.
- The hub reads the certificate at start. Restart it after you renew the certificate.

## Binaries

Every release tag also publishes the client and the hub as ready binaries, each archive with a
`.sha256` file. The hub archives exist from `v0.0.4` on.

| Computer | Client | Hub |
|---|---|---|
| macOS, Apple silicon (the client with Metal) | `office-darwin-arm64.tar.gz` | `office-hub-darwin-arm64.tar.gz` |
| Linux, x86-64 | `office-linux-amd64.tar.gz` | `office-hub-linux-amd64.tar.gz` |
| Linux, ARM64 | `office-linux-arm64.tar.gz` | `office-hub-linux-arm64.tar.gz` |

```sh
curl -fsSL https://github.com/mrsimpson/agentic-office/releases/latest/download/office-darwin-arm64.tar.gz | tar -xz
./office version
```

- The Linux binaries need glibc 2.34 or newer. The client also needs `libstdc++6` and
  `libgomp1`, which most distributions install. The hub needs `git`.
- The binaries are portable builds (`WHISPER_NATIVE=OFF`): x86-64 with AVX2, or any ARM64 CPU.
- On macOS, a file downloaded with a browser is in quarantine; `curl` avoids that, or run
  `xattr -d com.apple.quarantine office`.
- The client contains no speech models. The first `meeting join --mic` or `--wav` downloads them
  (see [Ears: speak into the meeting](#ears-speak-into-the-meeting)).

The [user guide](USER_GUIDE.md) shows how a team sets up a hub and joins meetings.

## Container images

Every release tag (`v1.2.3`) publishes two images for `linux/amd64` and `linux/arm64`:

| Image | Content |
|---|---|
| `ghcr.io/mrsimpson/agentic-office/office-hub` | The hub. Data, keys and the archive live in the volume `/data` |
| `ghcr.io/mrsimpson/agentic-office/office` | The client CLI and MCP server, for agents that run in containers |

Tags: the version (`1.2.3`), the minor version (`1.2`), `latest` and `sha-<commit>`. Build them
yourself with `docker build --target hub .` or `docker build --target office .`.

Run the hub:

```sh
docker volume create office-hub
docker run --rm -v office-hub:/data ghcr.io/mrsimpson/agentic-office/office-hub keygen /data/signing.key
docker run --rm -v office-hub:/data --entrypoint sh ghcr.io/mrsimpson/agentic-office/office-hub \
  -c 'echo "ada <public key of Ada> Ada" > /data/admins'
docker run -d --name office-hub -p 8740:8740 -v office-hub:/data \
  ghcr.io/mrsimpson/agentic-office/office-hub \
  -data-dir /data -addr 0.0.0.0:8740 -signing-key /data/signing.key -tls-hosts hub.example.org
```

The default command is `-data-dir /data -addr 0.0.0.0:8740 -signing-key /data/signing.key`.
When you pass flags, pass all of them, as in the example. See [Hub setup](#hub-setup) for the
flags.

Run an agent with the client: keep the container running, because `meeting join` starts a
replicator in the background that writes the local log.

```sh
docker run -d --name bot -v bot-home:/home/office \
  -e OFFICE_HUB=https://hub.example.org:8740 -e OFFICE_CA_CERT=/home/office/hub.crt \
  --entrypoint sleep ghcr.io/mrsimpson/agentic-office/office infinity
docker cp hub.crt bot:/home/office/hub.crt # the hub's TLS certificate (<data-dir>/tls.crt)
docker exec bot office key create          # register the printed public key on the hub
docker exec bot office meeting join weekly # prints the path of the local log
docker exec bot office say "Hello"
```

The client image has no microphone recorder and no Whisper models. To transcribe, mount the
models and set `OFFICE_WHISPER_MODEL` and `OFFICE_VAD_MODEL`, or use `--wav`.

## Participant client (office CLI)

`bin/office` is the participant client for humans and agents (`bb-cli`, `bb-replicator`). The
MCP server (`office mcp`, see [MCP server](#mcp-server)) offers the same features; both build on
the shared core in `client/core` (`dec-cli-and-mcp`). Run `office help` for all commands.

### First start

```sh
office key create                  # creates <config>/agentic-office/client/key.pem, prints the public key
# an admin registers it:
office admin key register ada <public-key> --name "Ada Lovelace" --kind human
office whoami                      # authenticates and prints "ada"
```

`office key show` prints the public key and key ID again. `office admin key revoke <key-id>`
removes a key. Only admin keys may register and revoke (see [First admin](#first-admin)).

### Configuration

Every command takes these flags. The environment variables set the defaults. `<config>` is
`~/.config` on Linux (or `$XDG_CONFIG_HOME`) and `~/Library/Application Support` on macOS.

| Flag | Variable | Default |
|---|---|---|
| `--hub` | `OFFICE_HUB` | `https://127.0.0.1:8740` |
| `--ca-cert` | `OFFICE_CA_CERT` | `<config>/agentic-office/client/hub.crt` if it exists (then only it is trusted), else the system roots |
| `--key` | `OFFICE_KEY` | `<config>/agentic-office/client/key.pem` |
| `--runtime-dir` | `OFFICE_RUNTIME_DIR` | `$XDG_RUNTIME_DIR/agentic-office`, else `/tmp/agentic-office-<uid>` |
| `--session` | `OFFICE_SESSION` | the only session this participant joined on this machine |
| `--json` | `OFFICE_JSON=1` | off |

For a self-signed hub, copy its `tls.crt` to `<config>/agentic-office/client/hub.crt`.
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

**Decisions, minutes and notes:**

```sh
office decision record "We ship on Friday."
office decision record --session weekly --file decision.md
office decision list --after 10 --limit 20
office note add "Ask the bank about the loan."
office note list --session weekly
office minutes show weekly           # the minutes stored when "weekly" ended
office minutes list
```

`decision record` and `note add` link the record to a session only with an explicit `--session`.
With `--json` they print `{"decision": …, "duplicate": …}` or `{"note": …, "duplicate": …}`; the
lists print `{"decisions": […]}`, `{"notes": […]}` and `{"minutes": […]}`, and `minutes show`
prints `{"minutes": …}`. Without `--json`, `minutes show` prints the text of the minutes.

**Tasks** (local drafts in the hub for now, see [Tasks](#tasks)):

```sh
office task create "Write the release notes" --assignee bot --session weekly
office task create "Book the room" --description-file room.md --status in_progress
office task get local:1
office task list --status open,in_progress --assignee bot
office task list --limit 20 --page-token 20   # the token that the page before printed
office task update local:1 --status done
office task update local:1 --assignee "" --session ""   # remove the assignee and the session
```

Statuses are `open`, `in_progress` (or `in-progress`), `done` and `cancelled`. `task create`
links the task to a session with `--session`; `task list --session S` shows the tasks of one
session; `task update` changes the session only with an explicit `--session`. With `--json`,
`task create`, `task get` and `task update` print `{"task": …}` (create and update also
`"duplicate"`), and `task list` prints `{"tasks": […], "nextPageToken": …}`. Without `--json`,
`task list` prints the token of the next page on stderr.

**Slides:**

```sh
ID=$(office slides render deck.md)   # prints the deck ID, for example slides/42
office stage show "$ID"
office slides get "$ID" --out deck.html
office slides get "$ID" --json      # {"deck": …}: the input and the output (content in base64)
```

A deck in Markdown: `# ` is the deck title, `## ` starts a slide, `- ` or `* ` is a bullet, other
text becomes the notes of the slide. A file that does not end with `.md` is JSON in the form of
`RenderDeckRequest`: `{"title": …, "slides": [{"title": …, "bullets": […], "notes": …}]}`.
With `--json`, `slides render` prints `{"id", "mediaType", "duplicate"}`.

**The local log** is `<runtime dir>/<session>/<participant>.jsonl` (directories 0700, file 0600):
one compact JSON line per event (the line form of `hub/session/streamapi/logfile`, the same as in the
archive of the hub), with the dotted type and the event ID as a
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
- While the hub is unreachable, a replicator keeps trying. To stop a detached one without
  leaving, kill the PID in `<participant>.pid`.
- Local logs stay until the runtime directory is cleaned (at logout for `$XDG_RUNTIME_DIR`).
  The retention rule is still open (`concept-transparency`).

### Gateway view

`office meeting view` shows your own utterances with their status (`final`, `sent`,
`off_the_record`) and whether you are paused. It is a filtered view of the local log
(`dec-gateway-cli`) and prints one line per change. Keys: `p` pauses or resumes, `o` marks the
last sent sentence off the record, `q` quits. It ends with the session.

### Import a conversation

`office session import <file | -> [--session S]` imports a whole conversation from another tool
(a chat export, a meeting transcript, an agent conversation) as a new session
(`dec-session-import`). It prints the session ID; without `--session` it creates one
(`import-<uuid>`). `--json` prints `{"sessionId", "pages", "firstEventId", "lastEventId",
"participants", "utterances", "treeHead"}`. The CLI sends the conversation in pages of 500
utterances. If it stops halfway, the session stays incomplete; import again into a new session
(#51).

The import format (`dec-import-format`) is one JSON document:

```json
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
```

| Field | Required | Meaning |
|---|---|---|
| `source` | yes | Where the conversation comes from: a tool, a channel, a URL. |
| `title` | no | Title of the conversation. |
| `participants[].id` | yes | Short ID inside the file: 1 to 64 letters, digits, `.`, `_` or `-`, starting with a letter or digit. Unique. |
| `participants[].name` | yes | Display name. |
| `participants[].kind` | yes | `human` or `agent`. |
| `utterances[]` | | The messages in order. The order of the file is the order of the log. |
| `utterances[].speaker` | yes | The `id` of a participant. |
| `utterances[].text` | yes | What was said or written. |
| `utterances[].time` | no | When, in RFC 3339. Kept as `originalTime`; not checked. |
| `utterances[].modality` | no | `text` (default) or `voice`. |

Unknown fields are an error, so a typo does not get lost. Every error in the file is a usage
error (exit code 2), and nothing is imported. In the log, the session starts with
`session.imported`, then `participant.joined` (with `imported: true`) for every speaker, then
one `utterance.final` per message, and ends the import with `import.completed`:

```sh
office session import design.json --session design-review
office meeting events --session design-review | jq -c 'select(.type == "utterance.final") | .utteranceFinal.text'
office meeting join design-review     # then write minutes, record decisions and tasks, end it
```

Adapters for other formats (chat exports, WebVTT) are not built yet; convert to this format first.

### Off the record and reading events after a cursor

What is said stays said (`dec-off-the-record`). A participant can mark **their own** sentence off
the record (`office utterance off-the-record <event-id> | --last`); the hub checks that the
caller is the speaker. The log gets a new `utterance.off_the_record` line. Everyone who reads the
session later, through the stream or `meeting events`, gets the sentence as a placeholder: the
same ID, `"offTheRecord": true`, no text, and the `leafHash` of the original (see
[Checking a session log](#checking-a-session-log-merkle-tree)). The stage hides it. Agents must not use such a
sentence anywhere.

Event IDs grow by one without gaps (`dec-events-from-cursor`). `office meeting events --after N
[--limit N]` reads the events after a cursor from the hub, in the line format of the local log
(`--json`: `{"events": […], "more": bool}`). Use it to fill a gap in a copy, or poll it when you
cannot follow a file.

### Ears: speak into the meeting

`office meeting join <session> --mic` joins as captured, runs the ears and shows the gateway
view. The ears transcribe your own speech locally with Whisper. Only the text and its metadata go
to the hub; the audio never leaves your computer (`concept-edge-transcription`).

- Each sentence ends after a short silence (default 500 ms). Whisper then transcribes it, and the
  view shows it as `final`. It becomes `sent` when it is in your local log.
- When you stay silent for longer after your last sentence (default 1.8 s), the ears end your
  turn: they submit `turn.end` for you, after the sentences of the turn. Agents that wait for the
  end of a turn wake up then. There is one `turn.end` per turn; the next one comes only after you
  have spoken again. Typed text (`office say`) does not end a turn; use `office signal turn-end`.
- `p` pauses: the microphone is closed and nothing is transcribed until you press `p` again.
  What you said just before the pause and is not final yet is dropped. A pause also ends your
  turn without `turn.end`, and the ears send no `turn.end` while you are paused.
- When the network drops, the ears keep transcribing and keep the sentences in a queue (in
  memory). They send them in order when the network is back. Sentences that are still queued when
  you quit are lost; the ears say how many.
- `--wav FILE` plays a WAV file in real time instead of the microphone (16-bit PCM, any rate,
  mono or stereo). The view stays open after the file ends; quit with `q`.
- Use a headset (`con-headset-only`): the ears have no echo cancellation.
- Before join, the ears check the models, the WAV file or the recorder. If something is missing,
  join fails and you do not join.

**Models.** The ears need a Whisper model and the Silero VAD model. The client knows a catalogue
of Whisper models by name (`dec-model-download`):

| Name | Size | Use |
|---|---|---|
| `small` | 490 MB | fast, all languages; real time on a CPU |
| `large-v3-turbo` | 1.6 GB | accurate, all languages; needs a GPU for live use |
| `large-v3-turbo-german` | 1.6 GB | accurate, fine-tuned for German ([primeline](https://huggingface.co/primeline/whisper-large-v3-turbo-german), Apache-2.0); needs a GPU for live use |

The ears choose the model in this order:

1. `--model NAME|FILE`: a name of the catalogue or a model file.
2. `--fast`: `small`.
3. `OFFICE_WHISPER_MODEL`: a name or a file.
4. The best model for `--language`: `large-v3-turbo-german` for German, `large-v3-turbo` for every
   other language. If the build of the client has no GPU support, the ears take `small` instead
   and say so on stderr. Only the macOS build for Apple silicon (Metal) counts as a GPU build;
   `OFFICE_WHISPER_GPU=1` or `0` overrides this, for example for an own build with CUDA.

`--language` takes a code or an English name, such as `de`, `german`, `en` or `english`, or
`auto` (Whisper detects the language). `office models list [--language L] [--fast] [--model M]`
shows the catalogue, what is downloaded, and which model join would use (`--json`: `{"models":
[…], "selected", "language", "note"}`).

When the chosen model of the catalogue or the VAD model is missing, the first `meeting join --mic`
or `--wav` downloads it into the model directory and checks its SHA-256. `office models download
[--language L] [--fast] [<name>]` does the same ahead of time, for example before the first
meeting. `OFFICE_MODEL_URL` sets a mirror: the files are then read from `<mirror>/<file>`, for
example `<mirror>/ggml-large-v3-turbo-german.bin` and `<mirror>/ggml-silero-v6.2.0.bin`. A model
file that you choose with a flag or a variable is never downloaded; `make models` fetches `small`
and the VAD model with the scripts of whisper.cpp.

| Flag | Variable | Default |
|---|---|---|
| `--model` | `OFFICE_WHISPER_MODEL` | the best model for the language (see above), in `~/.local/share/agentic-office/models/` |
| `--fast` | | off |
| `--vad-model` | `OFFICE_VAD_MODEL` | `~/.local/share/agentic-office/models/ggml-silero-v6.2.0.bin` |
| `--language` | `OFFICE_WHISPER_LANGUAGE` | `de` |
| | `OFFICE_WHISPER_GPU` | `1` for the macOS build on Apple silicon, else `0` |
| `--threads` | `OFFICE_WHISPER_THREADS` | half of the CPUs, at most 16 |
| `--end-silence` | `OFFICE_EARS_END_SILENCE` | `500ms` |
| `--turn-silence` | `OFFICE_EARS_TURN_SILENCE` | `1.8s` (at least the end silence) |
| | `OFFICE_VAD_THRESHOLD` | `0.5` (speech probability of the VAD) |
| `--vocabulary` | | `.office/vocabulary.txt` in the current directory, if it is there |
| `--context` | | off |
| | `OFFICE_MIC_COMMAND` | the first recorder found (see below) |

**Vocabulary.** Whisper often misspells names and terms of a project. The ears give Whisper a
prompt with the words it should prefer (`dec-vocabulary-prompt`):

1. the terms of `.office/vocabulary.txt` in the current directory, or of `--vocabulary FILE`: one
   term per line, in the order of importance; empty lines and lines that start with `#` are
   skipped;
2. the display names of the participants of the session, from the local log.

```text
# .office/vocabulary.txt
Businessplan
agentic-office
Quartalszahlen
```

The prompt holds about 200 tokens: Whisper keeps only the last 223 tokens of a longer prompt,
and the ears estimate from above (one token per two bytes, plus one per term). What does not fit
is cut at the end, so the first lines always count. Before each sentence, the ears add names that
joined since; they report the vocabulary at the start and whenever it changes:
`vocabulary: 12 terms and 3 names in the prompt (terms from .office/vocabulary.txt); 5 cut, the
prompt is full` (`--json`: `{"ears":{"type":"vocabulary","terms":12,"names":3,"cut":5,"prompt":…}}`).
`--context` also gives Whisper the previous sentence. It is off by default, because its effect is
not measured yet.

**Vocabulary from documents.** `office vocabulary extract [DIR]` reads the Markdown and text
files of a project (`.md`, `.markdown`, `.txt`, `.rst`, `.adoc`; also in subdirectories, but not
in hidden ones, `node_modules` or `vendor`) and writes `.office/vocabulary.txt` (`dec-ner-model`):

- A model for named-entity recognition finds persons, organisations and places, in German,
  English and eight more languages. It is
  [distilbert-base-multilingual-cased-ner-hrl](https://huggingface.co/Davlan/distilbert-base-multilingual-cased-ner-hrl)
  (AFL-3.0). The client runs it in plain Go and downloads it on first use (about 540 MB, with a
  pinned SHA-256, from the source or `OFFICE_MODEL_URL`, like the speech models). The model only
  labels words; it writes no text.
- Rules add CamelCase words (`SyncEngine`), acronyms (`DSGVO`, also in `DSGVO-Beauftragte`) and
  words of headings. Code blocks, inline code, links and HTML are skipped.
- Each occurrence scores: 3 for a name of the model, 2 for CamelCase or an acronym, 1 for a word
  of a heading. The terms are ranked by score, then by their first occurrence.

| Flag | Default | |
|---|---|---|
| `--out FILE` | `.office/vocabulary.txt` | the file to write |
| `--max N` | `100` | the number of terms to keep |
| `--no-model` | off | rules only: fast, no download |
| `--force` | off | replace a file that was written by hand |

The command does not replace a vocabulary file that it did not write; it knows its own files by
their first line. `--json` prints `{"file", "documents", "model", "terms": [{"term", "score",
"sources"}]}`. The model is slow: about 60 to 100 words per second on 4 CPUs, so 20,000 words take
a few minutes. Check the file after the run: the most important terms belong at the top, because
only about 30 fit into Whisper's prompt.

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

**What the ears report.** On stderr, the ears print one line per step: `listening`, `vocabulary`, `final`
(with the Whisper time), `sent` (with the time from the end of speech to the confirmed submit),
`turn-end` (the `turn.end` of a turn is submitted), `retry` while the network is down, `dropped`, `unsent` and `done` (end of a WAV file). With
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
  Connect code of the hub (`invalid_argument`, `not_found`, `failed_precondition`,
  `unimplemented`, …) or a code of the CLI: `usage`, `no_key`, `no_session`, `error`.
- `meeting view --json` prints one JSON object per change, for example
  `{"utterance":{"eventId":12,"commandId":"…","text":"…","status":"sent"}}`, `{"paused":true}`,
  `{"ended":true}`.
- `meeting join --mic --json` (or `--wav`) prints the join object, then the changes of the view
  as above, one per line. The reports of the ears go to stderr as `{"ears":{…}}`.

Without `--json`, a session command prints `event <id> <type>`; `join` and `meeting log` print the
path only. Errors go to stderr.

Exit codes: `0` success, `1` error (also from the hub), `2` usage error.

## MCP server

`office mcp` is a local MCP server on stdin and stdout (`bb-mcp`, `if-mcp`). An MCP client starts
it as a command. It offers the features of the CLI as tools, for agents that use MCP instead of
a shell. It takes the same flags and `OFFICE_*` variables as the CLI (see
[Configuration](#configuration)); `--session` sets the default session of the tools. The key
must exist and be registered first (`office key create`, then an admin registers it).

Add it to Claude Code:

```sh
claude mcp add office -e OFFICE_KEY=$HOME/.config/agentic-office/agent/key.pem \
  -e OFFICE_HUB=https://office.example.org:8740 -- /path/to/bin/office mcp
```

Or in the JSON configuration of a client (for example `.mcp.json`):

```json
{
  "mcpServers": {
    "office": {
      "command": "/path/to/bin/office",
      "args": ["mcp"],
      "env": { "OFFICE_KEY": "/home/me/.config/agentic-office/agent/key.pem",
               "OFFICE_HUB": "https://office.example.org:8740" }
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
| `slides_get` (id, out) | `slides get` |
| `decision_record` (text, session), `decision_list` (afterId, limit, session) | `decision record`, `decision list` |
| `note_add` (text, session), `note_list` (afterId, limit, session) | `note add`, `note list` |
| `minutes_get` (session), `minutes_list` (afterId, limit, session) | `minutes show`, `minutes list` |
| `task_create` (title, description, assignee, status, session), `task_get` (ref) | `task create`, `task get` |
| `task_list` (statuses, assignee, session, pageToken, limit), `task_update` (ref and the fields to change) | `task list`, `task update` |
| `session_import` (conversation or file, session) | `session import` |
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

The description of `meeting_join` and the hub instructions explain this and the event types
to the agent.

**Not in the MCP server:**

- Key and admin commands: set up the key with the CLI.
- The gateway view and the ears: an agent has no microphone. Humans use `office meeting join --mic`.
- A long-poll tool (`meeting.wait`, an open point of `concept-read-file-write-tools`): the
  agent reads the file (`dec-log-as-file`). It can be added later without changing the other
  tools.

## Agent runner

`office agent run` keeps an agent in a meeting (`bb-agent-runner`, `dec-agent-runner-acp`).
Claude Code, opencode and Kiro work in turns: they finish a turn and wait. The runner starts the
agent's **harness** as an Agent Client Protocol (ACP) agent and starts its turns for it.

```sh
export OFFICE_KEY=$HOME/.config/agentic-office/bot/key.pem   # the agent's own, registered key
office agent run --session weekly --role "You keep the minutes." -- opencode acp
office agent run --session weekly --role-file minute-taker.md -- kiro-cli acp
office agent run --session weekly --role-file minute-taker.md -- npx -y @agentclientprotocol/claude-agent-acp
```

Everything after `--` is the harness command. Flags:

- `--session S` (or `OFFICE_SESSION`): the session. Required.
- `--role TEXT` or `--role-file F`: the role of the agent, in plain words. It goes into the first
  prompt.
- `--allow PATTERN` (repeatable): allow tool calls whose title matches the pattern (`*` is any
  text, without regard to case). `--allow '*'` allows every tool. See the permission rule below.
- `--cwd DIR`: the working directory of the agent's session (default: the current directory).
- `--wake turn-end|llm|decision-model` (default `turn-end`): the wake strategy, that is when the
  runner wakes the agent (see below).
- `--decide-cmd`, `--decide-url`, `--decide-model`, `--decide-threshold`, `--decide-timeout`,
  `--decide-retry`: the model of `--wake llm` or `--wake decision-model` (see below).
- `--topic-task TEXT`: the task of the role when a model says that a block of topics has ended.
- The common flags and `OFFICE_*` variables of the CLI (key, hub, certificate, runtime directory).

**What the runner does:**

1. It joins the session with the key and runs the log replicator in its own process. If
   `office meeting join` already runs a replicator for the same participant, it follows that
   log.
2. It starts the harness in its own process group and talks ACP (protocol version 1) on its
   stdin and stdout. The harness's stderr goes to the runner's stderr.
3. It opens one ACP session with the working directory and one MCP server, `office`:
   `<this office binary> mcp --session S`, with `OFFICE_HUB`, `OFFICE_CA_CERT`, `OFFICE_KEY` and
   `OFFICE_RUNTIME_DIR` as absolute values. So the agent acts as itself through the office tools.
   The agent does not need to call `meeting_join`; if it does, the MCP server reuses the log.
4. It sends a first prompt: the session, the agent's participant ID, its role, how the meeting
   works (speak with `utterance_say`; the reply in the chat reaches nobody; respect sentences
   off the record) and the path of the local log.
5. It wakes the agent by the wake strategy of `--wake` (`dec-agent-runner-delivery`). With
   `turn-end`, it wakes the agent at each change of speaker (`turn.end` of another participant)
   and says so at start: it does not notice a new topic within a monologue. With `llm` or
   `decision-model`, it asks your model after each utterance of another participant
   (`dec-wake-check-per-utterance`). Details below.
6. After `session.ended`, it sends a last prompt and then stops. It also stops after the agent's
   own `participant.left`, on SIGINT or SIGTERM, or when the harness stops. It closes the
   harness's stdin, then sends SIGTERM and at last SIGKILL to the process group. A harness that
   stops by itself is an error (exit 1).

The runner logs to stderr (`office agent: …`): the ACP session, the wake strategy, each decision
of the model with its reason or its probabilities, each failure and switch of the model, each
wake-up, each tool call, each permission decision and the agent's reply text, which
is not spoken.

**What the agent gets** (`dec-agent-runner-delivery`). The runner reads the local log from the
agent's join on. It keeps the utterances of others that the agent has not got yet:

- When it wakes the agent, it sends all of them, oldest first, one per line:
  `[<event ID>] <name> (<human|agent>): <text>`. Then it says whose turn has ended, or that the
  speaker may still be speaking (the agent should then raise its hand or write instead of
  interrupting). With `llm` or `decision-model`, it adds whom the speaker addressed and, when a
  block of topics has ended, the task for that (`--topic-task`, default: update the minutes and record
  the decisions and tasks of the block, if the role asks for it).
- It never sends the agent's own utterances, its own `turn.end`, or placeholders of sentences off
  the record. A sentence that goes off the record before it was sent is never sent. For a sent
  one, the runner wakes the agent as soon as it is free, names the event ID and says that it is
  off the record.
- One prompt runs at a time. While the agent works, what comes waits and goes out together in
  one prompt when the agent ends its turn. A `turn.end` without new utterances wakes nobody.
- After `session.ended`, the last prompt carries what is left and asks the agent to finish the
  work of its role (for example the minutes, decisions or tasks) without speaking, because a new
  utterance would open the session again (`dec-session-end-milestone`). The runner does not
  leave the session.

Example:

```
New in the meeting "weekly" since my last message (event ID, speaker, human or agent, text):

[41] Ada (human): We start with the budget.
[42] Ada (human): Bot, what did we decide last week?

The speaker may still be speaking. Do not interrupt: raise your hand or write instead, unless the speaker asked you to answer now.

The model of the runner thinks that the speaker addressed: bot.

Decide yourself whether someone addressed you or whether your role asks you to contribute now.
If not, do nothing and end your turn. To speak, call utterance_say.
```

**The wake strategy** (`--wake`, `dec-agent-runner-delivery`, `dec-byo-decision-model`).
Waking a harness with a large model costs time and money. You choose when the runner wakes it:

| `--wake` | When the runner wakes the agent | Flags |
| --- | --- | --- |
| `turn-end` (default) | at each change of speaker (`turn.end` of another participant) | none |
| `llm` | when an **LLM judge** that you bring says so, after each utterance | `--decide-cmd CMD`, or `--decide-url URL --decide-model M` |
| `decision-model` | when a **decision model** that you bring says so, after each utterance | `--decide-url URL --decide-model M`, `--decide-threshold P` |

The office ships no model. All model flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--decide-cmd CMD` | | `llm`: a shell command (`sh -c`), run once per check. It reads the request as JSON on stdin and writes the answer as JSON on stdout. |
| `--decide-url URL` | | `llm`: the base URL of an OpenAI-compatible API (it ends in `/v1`); the runner appends `/chat/completions`. `decision-model`: the full URL of a System One endpoint; nothing is appended. |
| `--decide-model M` | | the model name for `--decide-url` |
| `--decide-threshold P` | `0.5` | `decision-model`: a probability at or above `P` counts as yes |
| `--decide-timeout D` | `10s` | after this time without an answer, the check has failed |
| `--decide-retry D` | `30s` | after the switch to `turn-end`, wait this long before the runner asks the model again; the wait doubles after each failure, up to `5m`. `0s`: ask again at the next utterance. |

`OFFICE_DECIDE_KEY`, if set, goes to the endpoint as `Authorization: Bearer <key>`; it is never
logged. The runner refuses model flags with `--wake turn-end`, and `--decide-threshold` with
`--wake llm`.

```sh
# llm: a command
office agent run --session weekly --role-file role.md --wake llm --decide-cmd "python3 decide.py" -- opencode acp
# llm: an OpenAI-compatible endpoint with structured output, for example Ollama or llama.cpp
office agent run --session weekly --role-file role.md --wake llm \
  --decide-url http://127.0.0.1:11434/v1 --decide-model <model> -- opencode acp
# decision-model: TypeSafe Jev
OFFICE_DECIDE_KEY=... office agent run --session weekly --role-file role.md --wake decision-model \
  --decide-url https://api.typesafe.ai/v1/systemone --decide-model jev-1.14.0 -- opencode acp
# decision-model: Cloudflare Clef (Workers AI)
OFFICE_DECIDE_KEY=... office agent run --session weekly --role-file role.md --wake decision-model \
  --decide-url https://api.cloudflare.com/client/v4/accounts/<account id>/ai/run/@cf/cloudflare/clef \
  --decide-model clef -- opencode acp
```

When the runner checks (`llm` and `decision-model`):

- after each utterance of another participant, and once more when that participant ends the
  turn (`turn_ended` is then true);
- one check at a time. Utterances that come while a check runs wait; the next check gets all
  utterances that the agent has not got yet as `new`. An answer about utterances that the agent
  got meanwhile is dropped;
- also while the agent works. A wake-up then waits until the agent is free.

It wakes the agent when the answer says `act` or `topic_closed`. Utterances that did not wake the
agent stay and go out with the next wake-up.

**When the model fails** (graceful fallback). A check fails on an error, on a time-out
(`--decide-timeout`) or on an answer outside the contract; the log says why.

- One failure does not wake the agent. The utterances wait for the next change of speaker, as
  with `turn-end`. If the speaker has ended the turn already, the runner wakes the agent now.
  The next check that works judges all waiting utterances.
- After three failures in a row, the runner switches to `turn-end` and logs it once
  (`… 3 failures in a row: switching to turn-end …`). It asks the model again at an utterance
  after `--decide-retry`, then after twice that wait, up to five minutes. When the model answers,
  it switches back and logs it (`… answers again: switching back from turn-end to …`).
- No utterance is lost and none is sent twice.

**The LLM judge** (`--wake llm`, `if-llm-judge`, `client/agentrunner/decision`). With
`--decide-url`, the runner posts to `URL/chat/completions` with a system message, the request as
the user message, `temperature` 0 and `response_format` of type `json_schema` (strict). The
answer is the content of the first choice. The request (snake_case JSON):

```json
{"session": "weekly",
 "agent": {"participant_id": "bot", "name": "Bot", "kind": "agent"},
 "role": "You keep the minutes.",
 "participants": [{"participant_id": "ada", "name": "Ada", "kind": "human"}, …],
 "context": [{"event_id": 38, "participant_id": "bot", "name": "Bot", "kind": "agent", "text": "…"}, …],
 "new": [{"event_id": 41, "participant_id": "ada", "name": "Ada", "kind": "human", "text": "We start with the budget."},
         {"event_id": 42, "participant_id": "ada", "name": "Ada", "kind": "human", "text": "Bot, what did we decide last week?"}],
 "turn_ended": false}
```

`new` are the utterances of others that the agent has not got yet; the last one is the one just
said. `context` are up to 20 earlier utterances, the agent's own included. Sentences off the
record are never in a request. The answer:

```json
{"act": true, "addressed": "bot", "topic_closed": false, "reason": "Ada asked Bot by name"}
```

`act` and `topic_closed` must be there; `addressed` and `reason` may be empty.

**The decision model** (`--wake decision-model`, `if-decision-model`,
`client/agentrunner/systemone`). The runner speaks the System One API: TypeSafe Jev
(`https://api.typesafe.ai/v1/systemone`, models `jev-latest` and `jev-1.14.0`) and Cloudflare
Clef (`https://api.cloudflare.com/client/v4/accounts/<account id>/ai/run/@cf/cloudflare/clef`,
models `clef` and `clef-flash`; Cloudflare states that it is compatible with the Jev API). The
runner posts the model, the state of the meeting (the same facts as the request of the LLM judge;
`context` is called `earlier`) and three questions:

```json
{"model": "jev-1.14.0",
 "state": {"session": "weekly", "agent": {…}, "role": "…", "participants": […], "earlier": […], "new": […], "turn_ended": false},
 "questions": {
   "act": {"type": "noul", "instructions": "… Yes if a speaker addressed the agent …"},
   "topic_closed": {"type": "noul", "instructions": "Yes if the participants closed a topic …"},
   "addressed": {"type": "choice", "instructions": "Whom did the speaker of the last new utterance address?",
                 "criteria": {"ada": "Ada", "bot": "Bot", "nobody": "no one in particular"}}}}
```

The answer:

```json
{"model": "jev-1.14.0",
 "answers": {"act": {"type": "noul", "noul": 0.93},
             "topic_closed": {"type": "noul", "noul": 0.1},
             "addressed": {"type": "choice", "choice": "bot", "confidence": 0.8, "probabilities": {"ada": 0.1, "bot": 0.8, "nobody": 0.1}}},
 "usage": {…}}
```

A `noul` at or above `--decide-threshold` counts as yes. Every question must have an answer of
its type: a `noul` between 0 and 1, a `choice` that is one of the criteria. The answer may also
come wrapped as `{"result": {…}, "success": true}`, as the Cloudflare REST API sends it. There is
no reason; the runner logs the probabilities:

```
office agent: decision up to event 42: wake (act true, addressed "bot", topic closed false): act 0.93, topic_closed 0.10, addressed bot (0.80); ada 0.10, bot 0.80, nobody 0.10
```

**The permission rule.** The runner offers the harness no file system and no terminal of its
own. When the harness asks for permission to use a tool (`session/request_permission`), the
runner answers by a rule:

- Default: allow a call of an office tool (a title such as `mcp__office__utterance_say` or
  `office_utterance_say`), unless its kind is `execute`, `edit`, `delete` or `move`. Reject every
  other call.
- With `--allow`: allow exactly the calls whose title matches one of the patterns.

The runner picks an `allow_once` option (or `allow_always`) to allow and a `reject_once` option
(or `reject_always`) to reject. Harnesses that do not ask, because their own configuration
allows a tool, are not affected. Configure the harness itself for what it may do without asking.

**Harnesses.** Any program that speaks ACP over stdio works. Known commands:

| Harness | Command |
|---|---|
| opencode | `opencode acp` |
| Kiro | `kiro-cli acp` |
| Claude Code | `npx -y @agentclientprotocol/claude-agent-acp` (the ACP adapter; it needs Node.js and a login of Claude Code) |

Log in with the harness itself first. The runner does not authenticate a harness.

## Tests

We write no unit tests (`dec-contract-tests`). Every building block is tested as a black box
through its interfaces:

- A contract test starts the block with `tests/contracttest` (as a real process, or as a fake that
  follows the same contract) and uses only its interfaces. `contracttest.StartHub` starts
  `office-hub` with TLS, an admin key and a signing key file (`-signing-key`); `StartHubIn`
  restarts it with the same file. `Hub.Participant` registers a key for a test
  participant and returns authenticated clients. It never imports the package that
  implements the block.
- `tests/contract/stub_example_test.go` shows a test against a stub service.
- `tests/contract/mcp_test.go` starts `office mcp` and talks to it over stdio with the MCP Go SDK
  client: every tool and its result, the log path that receives the events of all participants,
  errors for bad input, server errors, the slides, decisions, minutes, notes and tasks,
  and that no writer is left
  when the MCP process closes or is killed. `tests/e2e/mcp_test.go`: an agent works only through
  MCP and follows its log with the `tail -f | grep` filter from the tool description.
- `tests/contract/agentrunner_test.go` runs `office agent run` with the ACP test double
  `tests/acpdouble` as the harness (`contracttest.ACPDouble` builds it). The double speaks raw
  ACP JSON-RPC without a library, starts the MCP server that the runner hands it, records every
  step in a file and follows instructions in the prompt text (`please say "…"`, `please run
  "…"`, `please wait 2s`, `please hold` until a release file exists). The tests check the ACP
  steps, the MCP server and its key, the permission rule and `--allow`, the role file, usage
  errors, a harness that stops, that the harness stops with the runner, and the last prompt.
  `agentrunner_wake_test.go` checks the wake strategy `turn-end` (a wake-up at `turn.end`),
  queueing while the agent works, and sentences off the record. `agentrunner_decision_test.go`
  checks `--wake llm` with the LLM judge double `tests/decisiondouble`
  (`contracttest.DecisionDouble`; it judges the last new utterance by fixed rules and records each
  request) and a fake OpenAI-compatible endpoint: a check per utterance, a wake-up in the middle
  of a monologue, no wake-up, `topic_closed`, queueing while a check runs, and the fallback on
  error, time-out and a bad answer. `agentrunner_systemone_test.go` checks `--wake
  decision-model` with a fake System One endpoint: the request, the threshold, `addressed`,
  `topic_closed`, bare and wrapped answers, the key, and the switch to `turn-end` and back. The tests
  wait for records and log lines of the runner, never for a time. `tests/e2e/agentrunner_test.go`
  combines both doubles with a real hub and a human. No real harness and no model run in the
  tests.
- `tests/contract/cli_test.go` and `replicator_test.go` run the real `office` binary and check
  its output and the local log it leaves, including kills of the replicator and torn lines.
- `tests/e2e/ears_test.go` runs the ears with the German test WAV (`tests/e2e/testdata/`): the
  sentences reach another participant's local log in order; a network drop (a proxy from
  `contracttest.StartProxy` is cut and restored) loses and duplicates nothing; a pause
  transcribes nothing and ends the turn without `turn.end`; a longer silence after a turn gives
  one `turn.end` of the speaker behind its sentences; `--mic` with a mock recorder. It also checks that no raw audio leaves the
  device: `contracttest.StartDecryptingProxy` ends TLS with the hub's own test certificate and
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
  starts with `QS <scenario>` (`grep '^QS '`). In `make e2e-all` these tests check behaviour only
  and never fail on a timing criterion; `make e2e` skips them. `make performance` (build tag `performance`) also checks
  the timing criteria. Two variables set the lengths: `QS_SPEECH_DURATION` (default `60s`) and
  `QS_DROP_DURATION` (default `10s`). `make performance` sets them to `30m` and `60s`, for example
  `make performance QS_SPEECH_DURATION=5m`.
- `tests/contract/archive_test.go` checks the archive through the real hub and its Git
  repository: after `session.ended` the session is in one commit with all events, the original
  of an off-the-record sentence and its mark, and a tree head that verifies; at a short interval
  every write is one commit whose log extends the one before; the hub writes once more when it
  stops; session IDs that are not safe as file names are encoded. The harness reads the
  repository with `contracttest.SessionCommits` and `ReadArchivedSession`.
- `tests/contract/restore_test.go` checks the restore after a restart. `contracttest.NewGitGate`
  puts a `git` wrapper first in the `PATH` of the hub that can make every commit fail; the test
  writes a session, closes the gate, adds events and kills the hub, so the hub loses the tail.
  After the restart: the archive part is back; commands fail with `unavailable`; a copy shorter
  than the archive is a no-op; a changed event, a forged signature and a wrong number of events
  fail; of two peers that offer at once (one with the original of a sentence off the record, one
  with its placeholder) exactly one fills in; then the IDs continue and the cursor of a peer is
  valid again. A copy of the old tail after the wait forks and fails. After a clean stop there is
  no wait, ended sessions stay ended and repeated command IDs still work.
- `tests/contract/schema_test.go` checks the schema of a session (`dec-session-schema`): every
  tree head of a new session carries the schema of the hub and its signature covers it; package
  `merkle` rejects a head of another schema; the hub rejects an offered copy of another schema.
  To get a stored session of another schema, the test stops the hub, signs the stored tree head
  again with the hub's signing key file but with another schema, and commits it to the archive.
  After the restart the session is readable but not continued.
  `tests/e2e/restore_test.go` does the same with two CLI participants and their replicators:
  the hub restores 4 events from the archive, a replicator fills in 5 to 7, and both local logs,
  the hub and the archive end with every event exactly once.
- `tests/e2e/archive_test.go` (`TestQualityArchiveNoBackpressure`, `qs-archive-no-backpressure`)
  puts a `git` wrapper first in the `PATH` of the hub that makes every commit take 5 s, and lets
  Ada speak every 100 ms at an interval of 500 ms. It checks that commits never overlap, that
  there are far fewer commits than intervals (no queue), and that the last commit has the whole
  session. Only `make performance` fails on the latency of commands and the stream (maximum
  < 200 ms). `QS_ARCHIVE_DURATION` sets how long Ada speaks (default `12s`).
  `TestArchiveRecordsBeforeSessionWrites` (`dec-records-synchronous`) uses the same wrapper with
  commits of 2 s, an interval of 100 ms and Ada speaking every 50 ms. In three rounds it records
  a decision while a session write runs and a note while the decision is committed. It checks
  from the commit log of the wrapper that no record waits for a session write that started after
  the call (the decision waits at most for the one in progress, the note for none), that session
  writes still run between the rounds, and that the last commit has the whole session. Only
  `make performance` fails on the time of a record call (< 4.5 s: one other commit and its own).
- `tests/architecture/` checks the code against the arc42 model (`make arch`). It reads the model
  with `arc42 get --format json` and the imports with `go list`. A fixture workspace with
  deliberate violations proves that each rule fails when it should.
- The manual headset test (`qs-manual-headset`) is a checklist:
  [`docs/testing/manual-headset-test.md`](docs/testing/manual-headset-test.md).
- The harness uses the binaries in `$OFFICE_BIN_DIR` (the Makefile sets it). Without it, the
  harness builds them with `go build`, so `go test ./tests/contract/...` also works.

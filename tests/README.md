# Tests

How the office is tested. The commands to run the tests are in the [README](../README.md).

We write no unit tests (`dec-contract-tests`). Every building block is tested as a black box
through its interfaces:

- A contract test starts the block with `tests/contracttest` (as a real process, or as a fake that
  follows the same contract) and uses only its interfaces. `contracttest.StartHub` starts
  `office-hub` with TLS, a first system admin and a signing key file (`-signing-key`); `StartHubIn`
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
  queueing while the agent works, and sentences off the record. `agentrunner_forwardingjudge_test.go`
  checks `--wake llm` with the forwarding judge double `tests/forwardingjudgedouble`
  (`contracttest.ForwardingJudgeDouble`; it judges the last new utterance by fixed rules and records each
  request) and a fake OpenAI-compatible endpoint: a check per utterance, a wake-up in the middle
  of a monologue, no wake-up, `topic_closed`, queueing while a check runs, and the fallback on
  error, time-out and a bad answer. `agentrunner_systemone_test.go` checks `--wake
  decision-model` with a fake System One endpoint: the request, the threshold, `addressed`,
  `topic_closed`, bare and wrapped answers, the key, and the switch to `turn-end` and back. The tests
  wait for records and log lines of the runner, never for a time. `tests/e2e/agentrunner_test.go`
  combines both doubles with a real hub and a human. `tests/contract/config_test.go` runs
  `office agent run` and `office config show` with a temporary configuration folder
  (`XDG_CONFIG_HOME`) and start directory (`contracttest.RunIn`, `contracttest.StartIn`): the user
  file alone, the project file over it, environment and flags over both, the key from the variable
  that `key-env` names, errors with file, line and key, and the output of `config show`.
  `tests/contract/hub_profile_test.go` checks the connection keys and the hub profiles with the
  same setup: `office hub add` (fingerprint from the log of the hub, confirmation, a wrong
  fingerprint, a certificate of a trusted authority), `hub list` and `hub remove`, the order of
  the layers with profiles, and the errors. `tests/e2e/hubprofile_test.go` works with two hubs
  from one client through profiles. The helpers of the contract and end-to-end tests point `XDG_CONFIG_HOME` to an empty folder, so a
  user file of the host never takes part. No real harness and no model run in the
  tests.
- `tests/contract/cli_test.go` and `replicator_test.go` run the real `office` binary and check
  its output and the local log it leaves, including kills of the replicator and torn lines.
- `tests/contract/help_test.go` checks that every command of `office` answers `--help` (and
  `office help <command>`) with its purpose, a usage line and an example, that `office help`
  lists every command and topic, and that `office-hub -h` and `office-hub keygen --help` explain
  themselves. It needs no hub and checks behaviour only.
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
  session-log repository of the tenant (`Hub.SessionLogsDir`, `TenantSessionLogsDir`) with
  `contracttest.SessionCommits` and `ReadArchivedSession`.
- `tests/contract/sessionlogs_repo_test.go` checks that the session logs lie in a second
  repository of the tenant: a clone of the artifact repository has no `sessions/` directory and no
  original of an off-the-record sentence, the session-log repository holds the logs and verifies,
  records land in the artifact repository, and each tenant has its own two repositories.
- `tests/contract/archive_changes_test.go` checks `if-artifact-changes` through the real hub: the
  event of a commit of the archive (created, changed, with the version of the read interface),
  every follower gets every event, path prefixes, only Markdown and YAML,
  outsiders, other tenants and stage links, and a follower that never reads does not hold up the
  commits. `hub/archive/changes_contract_test.go` tests the same service and
  `if-archive-repository` in one process, because the notice "main moved" is an in-process call:
  a change by the real `git` tool plus the notice gives the same event as a commit of the
  archive; created, changed and removed artifacts; a follower that cannot keep up is dropped.
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
  `TestArchiveRecordsDoNotWaitForSessionWrites` uses the same wrapper with
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
  [`docs/testing/manual-headset-test.md`](../docs/testing/manual-headset-test.md).
- The harness uses the binaries in `$OFFICE_BIN_DIR` (the Makefile sets it). Without it, the
  harness builds them with `go build`, so `go test ./tests/contract/...` also works.

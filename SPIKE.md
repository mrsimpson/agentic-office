# Spike: end-to-end slice

This branch (`spike/e2e`) is a throwaway spike. It checks the architecture in
`docs/architecture/` end to end on one machine with a local Whisper. It is not the final structure.

## What is in it

| Part | Where | Notes |
|---|---|---|
| Protocol | `proto/office/v1/session.proto`, generated code in `gen/` | Connect RPC (connect-go v2): `Join`, `SubmitUtterance` (unary), `Subscribe` (server stream from a cursor) |
| Office server | `cmd/office-server`, `internal/server` | In-memory, append-only log per session; idempotent commands by client ID; no auth |
| office CLI | `cmd/office` | `office meeting join <session> [--wav FILE \| --mic]` prints the log path and replicates |
| Log replicator | `internal/replicator` | One JSON line per event, file mode 0600; cursor = last line; cuts a torn last line |
| Ears | `internal/ears` | Energy VAD -> whisper.cpp (cgo) -> `SubmitUtterance` with a UUIDv7; ordered queue with retries |
| E2E tests | `e2e/` (build tag `e2e`) | Voice to agent log, resume with kills, latency distribution |

## Build and run

Needs Go 1.27, a C/C++ compiler, cmake, and a multilingual ggml Whisper model.

```sh
ln -s /path/to/whisper.cpp third_party/whisper.cpp   # or: make third_party/whisper.cpp
make whisper          # builds static libwhisper.a
make build            # bin/office-server, bin/office
make e2e              # OFFICE_WHISPER_MODEL=/path/ggml-small.bin by default
```

By hand:

```sh
bin/office-server &
bin/office meeting join demo --name agent &            # prints /tmp/office/demo.jsonl
tail -f /tmp/office/demo.jsonl | grep '"utterance.final"'
bin/office meeting join demo --name alice --wav e2e/testdata/german-3-sentences.wav --log-dir /tmp/office-alice
```

Set `OFFICE_SPIKE_METRICS=1` to get `METRIC` lines on stderr.

# Build and test entry points. See README.md.
GO ?= go
BIN := $(abspath bin)
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/mrsimpson/agentic-office/client/cli.Version=$(VERSION)

# whisper.cpp for the ears (cgo). The office binary links it statically.
WHISPER_DIR := $(abspath third_party/whisper.cpp)
WHISPER_BUILD := $(WHISPER_DIR)/build_go
export C_INCLUDE_PATH := $(WHISPER_DIR)/include:$(WHISPER_DIR)/ggml/include
export LIBRARY_PATH := $(WHISPER_BUILD)/src:$(WHISPER_BUILD)/ggml/src

# Models of the ears: a multilingual Whisper model and the Silero VAD model.
MODEL_DIR ?= $(or $(XDG_DATA_HOME),$(HOME)/.local/share)/agentic-office/models

.PHONY: all build arch test contract e2e e2e-all e2e-vet performance lint generate check-generated whisper models clean
all: build

build:
	$(GO) build ./...
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/office-hub ./hub
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/office ./client

# Architecture test: the code follows the building blocks and interfaces of docs/architecture/
# (concept-module-boundaries). Needs Node.js for the arc42 CLI.
arch:
	npx -y @doctc/arc42@0.38.0 --dir docs/architecture validate --strict
	$(GO) test -count=1 ./tests/architecture/...

# Contract tests: black-box tests per building block against real processes. make test runs
# them together with the architecture test.
test: arch contract

contract: build
	$(GO) vet ./...
	OFFICE_BIN_DIR=$(BIN) $(GO) test -count=1 ./tests/contract/...

# End-to-end tests: real processes across several building blocks. make e2e is the quick run for
# local work: it skips the tests that wait in real time on purpose (go test -short). make e2e-all
# runs every end-to-end test; the CI uses it. RUN selects tests by name, slow ones included, for
# example make e2e RUN=TestEarsPause.
E2E_FLAGS = -tags e2e -count=1 -v
e2e: build e2e-vet
	OFFICE_BIN_DIR=$(BIN) $(GO) test $(E2E_FLAGS) $(if $(RUN),-run '$(RUN)',-short) ./tests/e2e/...

e2e-all: build e2e-vet
	OFFICE_BIN_DIR=$(BIN) $(GO) test $(E2E_FLAGS) $(if $(RUN),-run '$(RUN)') ./tests/e2e/...

e2e-vet:
	$(GO) vet -tags e2e ./tests/e2e/...
	$(GO) vet -tags e2e,performance ./tests/e2e/...

# Performance tests: the quality scenarios with their timing criteria and their full lengths (30
# minutes of speech, a network drop of 60 s). Build tags e2e and performance. Run them on the
# reference host; the end-to-end tests never fail on timing.
QS_SPEECH_DURATION ?= 30m
QS_DROP_DURATION ?= 60s
performance: build
	{ OFFICE_BIN_DIR=$(BIN) QS_SPEECH_DURATION=$(QS_SPEECH_DURATION) QS_DROP_DURATION=$(QS_DROP_DURATION) \
		$(GO) test -tags e2e,performance -count=1 -v -timeout 90m -run '^TestQuality' ./tests/e2e/... 2>&1; \
		echo $$? > $(BIN)/performance.exit; } | tee $(BIN)/performance.log
	@echo "== quality scenarios (also in $(BIN)/performance.log)"; grep '^QS ' $(BIN)/performance.log; exit $$(cat $(BIN)/performance.exit)

lint:
	buf lint

# Regenerates core/api from proto/. Needs buf, protoc-gen-go and protoc-gen-connect-go (v2).
generate:
	buf lint
	buf generate

# Fails when the committed generated code differs from what the contracts produce.
check-generated: generate
	git diff --exit-code -- '*.pb.go' '*.connect.go'
	test -z "$$(git status --porcelain -- '*.pb.go' '*.connect.go')"

# The whisper.cpp commit we build and test with. Change it on purpose, never by a fresh clone.
WHISPER_REF ?= 727d3e7666485e12450746b523bfb4e7f057cc5e

third_party/whisper.cpp:
	git init -q $@
	git -C $@ fetch -q --depth 1 https://github.com/ggml-org/whisper.cpp.git $(WHISPER_REF)
	git -C $@ checkout -q FETCH_HEAD

whisper: third_party/whisper.cpp
	cmake -S $(WHISPER_DIR) -B $(WHISPER_BUILD) -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF
	cmake --build $(WHISPER_BUILD) -j --target whisper

# Downloads the models into MODEL_DIR (from Hugging Face), if they are not there yet.
models: third_party/whisper.cpp
	mkdir -p $(MODEL_DIR)
	test -f $(MODEL_DIR)/ggml-small.bin || sh $(WHISPER_DIR)/models/download-ggml-model.sh small $(MODEL_DIR)
	test -f $(MODEL_DIR)/ggml-silero-v6.2.0.bin || sh $(WHISPER_DIR)/models/download-vad-model.sh silero-v6.2.0 $(MODEL_DIR)

clean:
	rm -rf $(BIN)

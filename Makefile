# Build and test entry points. See README.md.
GO ?= go
BIN := $(abspath bin)
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/mrsimpson/agentic-office/client/cli.Version=$(VERSION)

# whisper.cpp for the ears (cgo). The office binary links it statically.
WHISPER_DIR := $(abspath third_party/whisper.cpp)
WHISPER_BUILD := $(WHISPER_DIR)/build_go
# The paths go to cgo only. As C_INCLUDE_PATH, CMake would take ggml/include for a built-in path of
# the compiler and drop it from the Objective-C files of the Metal backend on macOS.
# On macOS, the Go bindings also link the Metal and BLAS backends, which CMake builds in their own
# directories. On Linux these directories do not exist; that does no harm.
export CGO_CFLAGS := -O2 -g -I$(WHISPER_DIR)/include -I$(WHISPER_DIR)/ggml/include
export CGO_LDFLAGS := -O2 -g -L$(WHISPER_BUILD)/src -L$(WHISPER_BUILD)/ggml/src -L$(WHISPER_BUILD)/ggml/src/ggml-metal -L$(WHISPER_BUILD)/ggml/src/ggml-blas

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

# ON builds whisper.cpp for the CPU of this computer (fastest, for local work). OFF builds a
# portable binary (x86-64 with AVX2, or the ARMv8 baseline) that runs on other computers too; the
# CI, the release binaries and the container images use OFF. A binary built with ON can stop
# with SIGILL on another CPU.
WHISPER_NATIVE ?= ON

third_party/whisper.cpp:
	git init -q $@
	git -C $@ fetch -q --depth 1 https://github.com/ggml-org/whisper.cpp.git $(WHISPER_REF)
	git -C $@ checkout -q FETCH_HEAD

# Builds the static libraries. A checkout at another commit than WHISPER_REF (for example from an
# older clone) is moved to WHISPER_REF and built from scratch. A symbolic link to your own clone
# is left as it is, with a warning.
whisper: third_party/whisper.cpp
	@head=$$(git -C $(WHISPER_DIR) rev-parse HEAD 2>/dev/null); \
	if [ "$$head" != "$(WHISPER_REF)" ]; then \
	  if [ -L third_party/whisper.cpp ]; then \
	    echo "warning: third_party/whisper.cpp is a link to a clone at $$head, not at $(WHISPER_REF)"; \
	  else \
	    echo "whisper.cpp is at $$head; moving it to $(WHISPER_REF) and building from scratch"; \
	    git -C $(WHISPER_DIR) fetch -q --depth 1 https://github.com/ggml-org/whisper.cpp.git $(WHISPER_REF) && \
	    git -C $(WHISPER_DIR) checkout -q --force FETCH_HEAD && \
	    rm -rf $(WHISPER_BUILD); \
	  fi; \
	fi
	@# CMake keeps the CPU options of the last build in its cache, so a change of WHISPER_NATIVE
	@# needs a new build directory.
	@if [ -d $(WHISPER_BUILD) ] && [ ! -f $(WHISPER_BUILD)/.native-$(WHISPER_NATIVE) ]; then \
	  echo "WHISPER_NATIVE changed to $(WHISPER_NATIVE); building whisper.cpp from scratch"; \
	  rm -rf $(WHISPER_BUILD); \
	fi
	cmake -S $(WHISPER_DIR) -B $(WHISPER_BUILD) -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DGGML_NATIVE=$(WHISPER_NATIVE)
	touch $(WHISPER_BUILD)/.native-$(WHISPER_NATIVE)
	cmake --build $(WHISPER_BUILD) -j --target whisper

# Downloads the models into MODEL_DIR (from Hugging Face), if they are not there yet.
models: third_party/whisper.cpp
	mkdir -p $(MODEL_DIR)
	test -f $(MODEL_DIR)/ggml-small.bin || sh $(WHISPER_DIR)/models/download-ggml-model.sh small $(MODEL_DIR)
	test -f $(MODEL_DIR)/ggml-silero-v6.2.0.bin || sh $(WHISPER_DIR)/models/download-vad-model.sh silero-v6.2.0 $(MODEL_DIR)

clean:
	rm -rf $(BIN)

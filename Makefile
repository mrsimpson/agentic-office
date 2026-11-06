# Build and test entry points. See README.md.
GO ?= go
BIN := $(abspath bin)
VERSION ?= $(shell git describe --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/mrsimpson/agentic-office/client/cli.Version=$(VERSION)

# whisper.cpp for the ears (cgo). Only needed once the ears are built (#8).
WHISPER_DIR := $(abspath third_party/whisper.cpp)
WHISPER_BUILD := $(WHISPER_DIR)/build_go
export C_INCLUDE_PATH := $(WHISPER_DIR)/include:$(WHISPER_DIR)/ggml/include
export LIBRARY_PATH := $(WHISPER_BUILD)/src:$(WHISPER_BUILD)/ggml/src

.PHONY: all build test e2e lint generate whisper clean
all: build

build:
	$(GO) build ./...
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/office-server ./cmd/office-server
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN)/office ./cmd/office

# Contract tests: black-box tests per building block against real processes.
test: build
	$(GO) vet ./...
	OFFICE_BIN_DIR=$(BIN) $(GO) test -count=1 ./tests/contract/...

# End-to-end tests: real processes across several building blocks.
e2e: build
	$(GO) vet -tags e2e ./tests/e2e/...
	OFFICE_BIN_DIR=$(BIN) $(GO) test -tags e2e -count=1 -v ./tests/e2e/...

lint:
	buf lint

# Regenerates core/api from proto/. Needs buf, protoc-gen-go and protoc-gen-connect-go (v2).
generate:
	buf lint
	buf generate

third_party/whisper.cpp:
	git clone --depth 1 https://github.com/ggml-org/whisper.cpp.git $@

whisper: third_party/whisper.cpp
	cmake -S $(WHISPER_DIR) -B $(WHISPER_BUILD) -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF
	cmake --build $(WHISPER_BUILD) -j --target whisper

clean:
	rm -rf $(BIN)

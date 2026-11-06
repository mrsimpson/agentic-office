# Spike build. whisper.cpp is expected in third_party/whisper.cpp (a clone or a symlink).
WHISPER_DIR := $(abspath third_party/whisper.cpp)
WHISPER_BUILD := $(WHISPER_DIR)/build_go
export C_INCLUDE_PATH := $(WHISPER_DIR)/include:$(WHISPER_DIR)/ggml/include
export LIBRARY_PATH := $(WHISPER_BUILD)/src:$(WHISPER_BUILD)/ggml/src
export CGO_ENABLED := 1

.PHONY: all whisper generate build test e2e
all: build

third_party/whisper.cpp:
	git clone --depth 1 https://github.com/ggml-org/whisper.cpp.git $@

whisper: third_party/whisper.cpp
	cmake -S $(WHISPER_DIR) -B $(WHISPER_BUILD) -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF
	cmake --build $(WHISPER_BUILD) -j --target whisper

generate:
	buf lint && buf generate

build:
	mkdir -p bin
	go build -o bin/office-server ./cmd/office-server
	go build -o bin/office ./cmd/office

test:
	go test ./internal/...

# Needs a multilingual whisper model (OFFICE_WHISPER_MODEL, default ggml-small).
e2e: build
	go test -tags e2e -count=1 -v ./e2e/

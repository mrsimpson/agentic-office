# syntax=docker/dockerfile:1
# Two images, picked with --target:
#   hub     the office hub (office-hub)
#   office  the client CLI and MCP server (office), for agents that run in containers
# Both build natively per platform (linux/amd64, linux/arm64), because the client links
# whisper.cpp through cgo. See README.md, "Container images".

FROM golang:1.27-trixie AS build
RUN apt-get update && apt-get install -y --no-install-recommends cmake \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
# whisper.cpp first: this layer stays cached until WHISPER_REF in the Makefile changes.
COPY Makefile ./
# Portable build: the image runs on any CPU of its architecture.
RUN make third_party/whisper.cpp whisper WHISPER_NATIVE=OFF
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN make build VERSION=$VERSION

FROM debian:trixie-slim AS office
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libgomp1 libstdc++6 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 office
COPY --from=build /src/bin/office /usr/local/bin/office
USER office
WORKDIR /home/office
ENTRYPOINT ["office"]

FROM debian:trixie-slim AS hub
# The hub writes its archive with git.
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 office \
    && mkdir /data && chown office /data
COPY --from=build /src/bin/office-hub /usr/local/bin/office-hub
USER office
VOLUME /data
EXPOSE 8740
ENTRYPOINT ["office-hub"]
CMD ["-data-dir", "/data", "-addr", "0.0.0.0:8740", "-signing-key", "/data/signing.key"]

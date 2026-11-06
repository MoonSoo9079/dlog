GO ?= go
VERSION := $(shell sh internal/version/closest.sh)
LDFLAGS := -X dlog/internal/version.Version=$(VERSION)

.PHONY: build test fmt

build:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/dlog-server ./cmd/server
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/dlog-agent ./cmd/agent

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

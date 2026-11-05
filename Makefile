GO ?= go

.PHONY: build test fmt

build:
	mkdir -p bin
	$(GO) build -trimpath -o bin/dlog-server ./cmd/server
	$(GO) build -trimpath -o bin/dlog-agent ./cmd/agent

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

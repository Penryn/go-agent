.PHONY: build test check replay run
CONFIG ?= configs/config.yaml
BIN ?= bin/qqbotd
build:
	mkdir -p $(dir $(BIN))
	go build -o $(BIN) ./cmd/qqbotd

test:
	go test ./...

check:
	go vet ./...
	go test -race ./...
	go build ./...

replay:
	go run ./cmd/replay -fixture tests/replay/conversation.json
	go run ./cmd/replay -fixture tests/replay/memory.json

run:
	go run ./cmd/qqbotd -config $(CONFIG)

.PHONY: build test check check-go admin-install admin-dev admin-typecheck admin-build admin-check replay run
CONFIG ?= configs/config.yaml
BIN ?= bin/qqbotd
ADMIN_DIR ?= web/admin
build:
	mkdir -p $(dir $(BIN))
	go build -o $(BIN) ./cmd/qqbotd

test:
	go test ./...

check: admin-check check-go

check-go:
	go vet ./...
	go test -race ./...
	go build ./...

admin-install:
	cd $(ADMIN_DIR) && npm ci

admin-dev:
	cd $(ADMIN_DIR) && npm run dev

admin-typecheck:
	cd $(ADMIN_DIR) && npm run typecheck

admin-build:
	cd $(ADMIN_DIR) && npm run build

admin-check: admin-typecheck admin-build

replay:
	go run ./cmd/replay -fixture tests/replay/conversation.json
	go run ./cmd/replay -fixture tests/replay/memory.json

run:
	go run ./cmd/qqbotd -config $(CONFIG)

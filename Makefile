.PHONY: build web test run

CONFIG ?= configs/config.yaml
BIN ?= bin/qqbotd

build: web
	mkdir -p $(dir $(BIN))
	go build -o $(BIN) ./cmd/qqbotd

web: web/node_modules/.package-lock.json
	npm --prefix web run build

web/node_modules/.package-lock.json: web/package-lock.json
	npm --prefix web ci

test: web
	go test ./...

run: web
	go run ./cmd/qqbotd -config $(CONFIG)

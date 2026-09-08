.PHONY: build web test run

CONFIG ?= configs/config.yaml
BIN ?= bin/qqbotd

# 管理后台由 Vue 构建到 internal/app/adminui/dist，再经 go:embed 编进二进制。
# dist 不入库；go build / go test / go run 前必须先 make web。
build: web
	mkdir -p $(dir $(BIN))
	go build -o $(BIN) ./cmd/qqbotd

web: web/node_modules/.package-lock.json
	npm --prefix web run build
	test -f internal/app/adminui/dist/index.html

web/node_modules/.package-lock.json: web/package-lock.json
	npm --prefix web ci

test: web
	go test ./...

run: web
	go run ./cmd/qqbotd -config $(CONFIG)

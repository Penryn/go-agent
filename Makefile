.PHONY: build web test test-backend test-frontend test-all run

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

# 只测试后端（快速，不构建前端）
test-backend:
	@echo "Running backend tests..."
	go test ./internal/... ./cmd/... -v -cover

# 只测试前端
test-frontend:
	@echo "Running frontend tests..."
	cd web && npm test

# 默认测试（只后端，快速）
test: test-backend

# 完整测试（包括前端）
test-all: web test-backend test-frontend

run: web
	go run ./cmd/qqbotd -config $(CONFIG)

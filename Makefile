SHELL := /bin/bash

.PHONY: help frontend dev build run test vet test-frontend check clean

BINARY := feather-imgbed
GO_BUILD_FLAGS := -trimpath

help: ## 显示可用目标
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

frontend: ## 构建前端到 internal/app/web/dist（被 go:embed 嵌入）
	@if [ ! -d frontend/node_modules ]; then \
		echo "→ 安装前端依赖"; \
		cd frontend && npm install; \
	fi
	cd frontend && npm run build

dev: frontend ## 一键启动前后端开发服务（前端 :5173，后端 :8080）
	@set -eu; \
	cleanup() { \
		trap - EXIT INT TERM; \
		[ -z "$${backend_pid:-}" ] || kill $$backend_pid 2>/dev/null || true; \
		[ -z "$${frontend_pid:-}" ] || kill $$frontend_pid 2>/dev/null || true; \
		[ -z "$${backend_pid:-}" ] || wait $$backend_pid 2>/dev/null || true; \
		[ -z "$${frontend_pid:-}" ] || wait $$frontend_pid 2>/dev/null || true; \
	}; \
	trap cleanup EXIT INT TERM; \
	echo "→ 启动后端 http://127.0.0.1:8080"; \
	go run . -listen :8080 -data ./data & backend_pid=$$!; \
	echo "→ 启动前端 http://127.0.0.1:5173"; \
	(cd frontend && npm run dev) & frontend_pid=$$!; \
	wait -n $$backend_pid $$frontend_pid

build: frontend ## 构建前端并编译二进制 feather-imgbed
	go build $(GO_BUILD_FLAGS) -o $(BINARY) .

run: frontend ## 构建前端并启动服务（:8080，数据目录 ./data）
	go run . -listen :8080 -data ./data

test: frontend ## 构建前端并运行 Go 测试（含竞态检测）
	go test -race -count=1 ./...

vet: frontend ## 构建前端并运行 go vet
	go vet ./...

test-frontend: ## 仅运行前端单元测试
	cd frontend && npm test

check: frontend ## 提交前综合检查（前端构建 + Go 测试 + vet）
	go test -race -count=1 ./...
	go vet ./...

clean: ## 清理前端产物与二进制
	rm -rf internal/app/web/dist $(BINARY)

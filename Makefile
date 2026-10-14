GO ?= $(HOME)/sdk/go/bin/go

.PHONY: build run test vet compose-up compose-down start stop restart status help

build: ## 编译 bincat 到 bin/
	$(GO) build -o bin/bincat ./cmd/bincat

run: ## 前台运行（调试用；生产用 make start）
	$(GO) run ./cmd/bincat -config configs/config.yaml

start: ## 后台启动（bincat.sh 封装，日志 logs/bincat.log）
	./bincat.sh start

stop: ## 停止（优雅停机）
	./bincat.sh stop

restart: ## 重启
	./bincat.sh restart

status: ## 查看运行状态
	./bincat.sh status

test: ## 单元测试
	$(GO) test ./...

vet: ## 静态检查
	$(GO) vet ./...

compose-up: ## 启动本地 MySQL 环境
	cd deployments && docker compose up -d

compose-down: ## 停止本地环境
	cd deployments && docker compose down

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

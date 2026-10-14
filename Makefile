GO ?= $(HOME)/sdk/go/bin/go

.PHONY: build run test vet compose-up compose-down seed help

build: ## 编译 bincat 到 bin/
	$(GO) build -o bin/bincat ./cmd/bincat

run: ## 本地运行（需先 compose-up 并插入数据源/规则/sink）
	$(GO) run ./cmd/bincat -config configs/config.yaml

test: ## 单元测试
	$(GO) test ./...

vet: ## 静态检查
	$(GO) vet ./...

compose-up: ## 启动本地 MySQL 环境
	cd deployments && docker compose up -d

compose-down: ## 停止本地环境
	cd deployments && docker compose down

seed: ## 写入演示数据源/sink/规则（M1 阶段手动配置通道；密码需先加密，演示用空密钥明文链路）
	mysql -h127.0.0.1 -P33061 -uroot -proot123 bincat_meta -e "\
	INSERT INTO sink(name,type,params,enabled) VALUES('debug','console','{}',1); \
	INSERT INTO datasource(name,host,port,user,password,server_id,gtid_mode,enabled) \
	  VALUES('local','127.0.0.1',33061,'bincat',X'00',1001,1,1) \
	  ON DUPLICATE KEY UPDATE enabled=1;"

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

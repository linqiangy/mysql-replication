# bincat — MySQL Binlog 变更监听与消息投递服务

监听 MySQL binlog 行变更，按库表、操作和字段过滤，投递到 console / HTTP / Kafka / RabbitMQ，并将已确认位点保存到元数据库。Go 单二进制内嵌 Vue 3 + Element Plus 管理后台，前端资源本地提供，无需 Node.js 或运行时 CDN。

## 文档入口

- [部署与运维](docs/DEPLOYMENT.md)：本地演示、Linux systemd、首次验收、升级、回滚、备份、故障排查。
- [MySQL 检查与配置指南](docs/MYSQL_SETUP.md)：检查 SQL、版本差异、开启 binlog、权限、建库和重启复查。
- [HTTP 回调测试服务](docs/HTTP_CALLBACK_TEST.md)：Go 接收端启动、后台 HTTP 目标配置及控制台输出。
- [配置参考](docs/CONFIGURATION.md)：环境变量、静态参数、数据源、规则、Sink、消息格式和监控。
- [生产配置模板](configs/config.production.example.yaml)、[systemd 单元](deployments/systemd/bincat.service)、[环境文件模板](deployments/systemd/bincat.env.example)。
- [第三方前端资源说明](internal/server/frontend/vendor/README.md)。

## 功能与边界

- 多数据源，每个数据源独立 pipeline；库名精确匹配、表名模式过滤。
- insert / update / delete 操作过滤；watch_fields 控制 update 字段变化触发。
- Sink 批次确认后推进位点，失败时阻塞重试；故障恢复可能重复投递，下游须幂等。
- JWT 登录、管理员/普通用户权限、数据源/规则/目标/用户/位点/审计管理。
- 规则 reload、数据源新增/启停对账；已有数据源连接参数变更需重启确认生效。
- Prometheus 指标和 pprof。

当前不包含 HA、全量初始化、DLQ/死信重放及完整 broker 集成测试。默认大事务超限策略会丢弃事务并告警，生产模板使用 abort。at-least-once 设计不覆盖主动跳过超限事务或人为重置位点等操作。

## 快速开始

需要 Go 1.26.0 或更新的兼容版本。Docker Compose 用于启动本地演示 MySQL 8.4；也可直接连接已有 MySQL。

```bash
git clone git@github.com:linqiangy/mysql-replication.git
cd mysql-replication
go mod download
cd deployments
docker compose up -d
docker compose ps  # 等待 mysql-meta healthy
cd ..
cp configs/config.example.yaml configs/config.yaml
export BINCAT_ADMIN_PASSWORD='replace-with-a-local-admin-password'
make build GO="$(command -v go)"
./bin/bincat -config configs/config.yaml
```

打开 [本地控制台](http://localhost:8080/)，使用 `admin` 和首次启动时设置的密码登录。添加数据源 → 创建投递目标 → 配置规则 → 写入测试数据并检查实际投递。详细步骤见[部署文档](docs/DEPLOYMENT.md)。

演示 MySQL 地址为 `127.0.0.1:33061`，`bincat / bincat123`，演示数据库 `shop`。Compose 中的密码为公开演示值，不能用于生产。

前端通过 `go:embed` 编译进二进制，修改 HTML/资源后须重新构建并重启。`bincat.sh start` 和 `restart` 本身不构建。

## 开发与验证

```bash
make build GO="$(command -v go)"
make test GO="$(command -v go)"
make vet GO="$(command -v go)"
bash -n bincat.sh
```

Makefile 默认使用 `~/sdk/go/bin/go`；通过 `GO` 参数可使用系统工具链。现有自动化测试覆盖过滤引擎，不能代替源复制、断点恢复及各 Sink 的端到端验收。公开配置模板不含实际生产凭据；本地 `configs/config.yaml`、运行日志、二进制和个人协作记忆不应提交。

## 项目结构

```text
cmd/bincat/          主服务入口
cmd/http-callback-test/ 独立 HTTP 回调测试接收端
internal/app/        组件装配与生命周期
internal/config/     YAML、环境变量与启动校验
internal/meta/       元数据库表、账号/审计与数据源密码加密
internal/pipeline/   canal → 过滤 → 派发 → 位点确认
internal/sink/       console / http / kafka / rabbitmq
internal/checkpoint/ MySQL 位点存储
internal/server/     REST API、JWT/RBAC 与内嵌前端
internal/observability/ 日志、Prometheus、pprof
configs/            本地及生产配置模板
deployments/        演示 MySQL、systemd 模板
docs/               部署与配置说明
```

## MySQL 没配置时如何检查与修改

先连接**被监听的源 MySQL**，执行：

```sql
SELECT VERSION(), @@hostname, @@port;
SHOW GLOBAL VARIABLES WHERE Variable_name IN (
  'log_bin', 'server_id', 'binlog_format', 'binlog_row_image',
  'binlog_row_metadata', 'binlog_expire_logs_seconds', 'expire_logs_days',
  'gtid_mode', 'enforce_gtid_consistency'
);
SHOW BINARY LOGS;
```

预期 `log_bin=ON`、`server_id` 非 0 且唯一、`binlog_format=ROW`、`binlog_row_image=FULL`；支持 `binlog_row_metadata` 的版本建议设 FULL。5.7 没有该变量，不应将它写入配置。日志保留时间应覆盖停机及恢复窗口。bincat 当前使用文件位点恢复，不需要强制开启 GTID。

如果没有开启，在数据库服务器**实际加载的配置文件**的 `[mysqld]` 段设置以下内容，然后在维护窗口重启 MySQL：

```ini
# MySQL 8.0 / 8.4 示例：server_id 须按现有拓扑确认
[mysqld]
server_id=1
log_bin=mysql-bin
binlog_format=ROW
binlog_row_image=FULL
binlog_row_metadata=FULL
binlog_expire_logs_seconds=864000
```

MySQL 5.7 移除最后两行，改用 `expire_logs_days=10`。重启的是 MySQL 服务，不是仅重启 bincat；重启后重新连接执行上述检查，并核对业务写入会话的 ROW/FULL 值。配置文件定位、各环境重启命令、版本对应的位点检查及详细判断见 [MySQL 指南](docs/MYSQL_SETUP.md)。

监听账号另需全局 `REPLICATION SLAVE` / `REPLICATION CLIENT` 和实际业务表的 `SELECT` 权限；元数据库需预建库及有建表/读写权限的账号。具体的检查、CREATE USER / GRANT / CREATE DATABASE 示例均在上述指南中，不能只依靠后台“连接正常”判断完整链路就绪。

## 本地接收 HTTP 回调

另开一个终端，在项目根目录运行：

```bash
go run ./cmd/http-callback-test -addr 127.0.0.1:18080
```

在后台新增 http 投递目标，URL 填 `http://127.0.0.1:18080/callback`，点击“测试”，接收端控制台会打印真实探针事件。把规则目标设为此 Sink 后，再在测试库产生变更即可观察业务回调。curl、自定义端口和容器网络说明见 [HTTP 测试服务文档](docs/HTTP_CALLBACK_TEST.md)。

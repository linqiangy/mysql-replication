# bincat — MySQL Binlog 变更监听与消息投递服务

基于 [go-mysql](https://github.com/go-mysql-org/go-mysql) 的 MySQL binlog CDC 服务：
监听库表变更 → 表/操作/字段三级过滤 → 投递到可插拔目标（console/http/kafka/rabbitmq）→ 位点持久化断点续传。

## 特性

- **表级过滤**：schema 精确 + 表名通配（`orders_*`）
- **操作过滤**：insert / update / delete 任意组合
- **字段级过滤**：仅指定字段（watch_fields）变化才触发，其余变更静默忽略
- **可插拔 Sink**：console（调试）/ HTTP Webhook / Kafka / RabbitMQ
- **at-least-once 投递**：批次全部确认才推进位点；重启从位点续传，不丢消息（可能重，消息带唯一 ID 供下游幂等）
- **多数据源**：每个数据源独立 pipeline，互不影响（同进程）
- **热更新**（规划中）：规则/数据源/sink 后台变更即时生效，binlog 流不断

## 快速开始

```bash
# 1. 本地 Go 工具链（或用系统 go >= 1.25）
export PATH=$HOME/sdk/go/bin:$PATH

# 2. 准备 MySQL（需 binlog_format=ROW、binlog_row_image=FULL、
#    MySQL 8.0.14+ 需 binlog_row_metadata=FULL；账号需 REPLICATION SLAVE/CLIENT + SELECT）

# 3. 配置元数据库连接
cp configs/config.example.yaml configs/config.yaml

# 4. 启动（自动建元数据表）
make build && ./bin/bincat -config configs/config.yaml

# 5. 配置数据源/sink/规则（M1 阶段手工插库，Web 后台在 M3）
mysql $meta_db -e "
INSERT INTO sink(name,type,params,enabled) VALUES('debug','console','{}',1);
INSERT INTO datasource(name,host,port,user,password,server_id,gtid_mode,enabled)
  VALUES('local','127.0.0.1',3306,'user','pass',1001,1,1);
SET @ds := (SELECT id FROM datasource WHERE name='local');
INSERT INTO rule(datasource_id,name,schema_name,table_pattern,actions,watch_fields,sink_id,owner_id,enabled)
  VALUES(@ds,'orders-status','shop','orders_*','update','[\"status\"]',1,1,1);"

# 6. 写数据验证
mysql shop -e "UPDATE orders_2026 SET status='paid' WHERE id=1"
```

## 项目结构

```
cmd/bincat/          入口
internal/
  app/               组件装配与生命周期
  config/            启动配置（YAML + ${ENV} 插值）
  meta/              元数据库（数据源/规则/sink/位点/用户/审计）+ 密码 AES-GCM 加密
  model/             ChangeEvent 统一消息模型
  pipeline/          核心链路：过滤引擎、canal handler、dispatcher、PipelineManager
  sink/              投递目标抽象 + console 实现（http/kafka/rabbitmq 在 M2）
  checkpoint/        位点持久化（元数据库单行表）
  observability/     slog 日志 + Prometheus 指标 + pprof
deployments/         docker-compose（MySQL 演示环境）
```

## 消息格式

```json
{
  "id": "local:38004:1791966258",       // 幂等键
  "datasource": "local",
  "schema": "shop", "table": "orders_2026",
  "action": "update",
  "before": {"id": 1, "status": "created", ...},
  "after":  {"id": 1, "status": "paid", ...},
  "changed": ["status"],                 // watch_fields 中实际变化的字段
  "rule": "orders-status",
  "pk": ["id"],
  "ts": 1791966258, "emit_ts": "2026-10-14T16:24:18+08:00"
}
```

## 路线图

- [x] M1 核心管线：canal 接入、三级过滤、console sink、位点断点续传
- [ ] M2 生产投递：http/kafka/rabbitmq sink、重试、block/dlq、sink 热更新
- [ ] M3 Web 后台：登录、数据源/规则/sink/用户管理、监控面板、多用户隔离
- [ ] M4 可观测：Prometheus 指标、集成测试
- [ ] M5 增强：HA、全量初始化、DLQ 重放

## 前置条件（被监听 MySQL）

```ini
[mysqld]
server_id = 1
log_bin = mysql-bin
binlog_format = ROW
binlog_row_image = FULL
binlog_row_metadata = FULL   # MySQL 8.0.14+ 必需（无它拿不到列名）
gtid_mode = ON               # 推荐
enforce_gtid_consistency = ON
```

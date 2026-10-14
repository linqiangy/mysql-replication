# 配置参考

MySQL 参数与权限的具体检查/修改步骤见 [MySQL 准备指南](MYSQL_SETUP.md)，本地 HTTP 回调观察见 [测试服务说明](HTTP_CALLBACK_TEST.md)。

静态启动配置由 `internal/config` 读取 YAML，修改后需重启。数据源、规则、投递目标和位点存放在元数据库，由后台管理。

## 环境变量

只支持 `${VAR}`，不支持 shell 的 `${VAR:-default}`。变量未设置时插值为空；初始管理员密码为空时源码兜底为 `admin123`，生产必须显式设置。文件插值先于 YAML 解析，值须符合所在位置的 YAML 引号和转义要求。

| 配置 | 默认/约束 | 运维说明 |
| --- | --- | --- |
| `meta.dsn` | 必填 | 元数据库连接串，需建表和业务读写权限 |
| `meta.secret_key` | 空 | 32 字节原文或其 Base64；配置文件优先，否则使用 `BINCAT_SECRET_KEY`；未设置时数据源密码按明文存储 |
| `server.addr` | `:8080` | Web/API 监听地址；生产示例绑定 loopback |
| `server.initial_admin_password` | 空时 `admin123` | 仅首次创建 admin 使用，不会覆盖已有账号 |
| `server.jwt_secret` | 随机生成 | 未固定时重启令旧登录令牌失效 |
| `server.jwt_ttl` | `12h` | JWT 有效期 |
| `observability.log.level` | `info` | debug/info/warn/error |
| `observability.log.format` | `json` | json/text |
| `observability.metrics.enabled` | `true` | 是否开启指标 HTTP 服务 |
| `observability.metrics.addr` | `:9090` | `/metrics` 与 `/debug/pprof/*` 共用端口 |
| `pipeline_defaults.dispatch_queue_size` | `256`，须为正 | 待派发批次队列，满则背压 |
| `pipeline_defaults.ack_window_size` | `64`，须为正 | 参数传入 Dispatcher；当前串行 worker，不代表并行投递数 |
| `pipeline_defaults.max_tx_rows` | `100000`，须为正 | 单事务行数上限 |
| `pipeline_defaults.on_tx_overflow` | `skip_with_alert` | 允许 skip_with_alert/abort；前者丢弃超限事务并告警，生产示例使用 abort |
| `pipeline_defaults.checkpoint_flush_interval` | `2s` | 位点定时落盘间隔；另有事件触发及停机刷盘 |
| `pipeline_defaults.on_sink_exhausted` | `block` | 校验器接受 dlq，但 Dispatcher 当前只有阻塞重试实现，不应配置为 dlq 期待死信能力 |
| `shutdown.drain_timeout` | `30s` | 优雅停机排空等待上限；服务管理器超时需留出余量 |

## 数据源与规则

数据源包括名称、host、port、user、密码、复制客户端 server_id、GTID 模式和启用状态。当前实际恢复路径使用 binlog 文件名/位点和 RunFrom；GTID 开关尚未实现 GTID 集合恢复。元数据库账号和源复制账号是不同职责。数据源修改连接参数后应重启应用确认生效；当前 Reconcile 只处理实例新增和禁用/移除，不重建已有连接。

规则由库名、表名模式、操作集合、watch_fields 和 Sink 构成。普通用户只管理自己的规则；管理员可管理全部规则。watch_fields 用于 update 字段变化过滤，空列表监听全部变化。HTTP API 路由参考 `internal/server/server.go`，此版本未提供 OpenAPI 文档。

## 投递目标和消息

| 类型 | 关键参数 | 消息形态 |
| --- | --- | --- |
| console | 无 | 服务日志输出，供本地验证 |
| http | url、可选 timeout_seconds / method / headers | 默认 POST，整批 JSON 数组，2xx 确认 |
| kafka | brokers、topic、ack；可选 sasl_user / sasl_password | 每条事件 JSON；默认 ack=all，支持 schema/table 动态 topic |
| rabbitmq | url、exchange、routing_key、可选 declare / confirm | 每条事件 JSON；持久化发布，默认启用 confirm |

后台表单仅覆盖部分参数，高级字段以各 Sink 源码为准。Sink 配置中可能含凭据，应限制元数据库与管理员访问。数据源密码加密器不意味着所有 Sink 凭据都已加密。

```json
{
  "id": "local:38004:1791966258",
  "datasource": "local",
  "schema": "shop",
  "table": "orders_2026",
  "action": "update",
  "before": {"id": 1, "status": "created"},
  "after": {"id": 1, "status": "paid"},
  "changed": ["status"],
  "rule": "orders-status",
  "pk": ["id"],
  "ts": 1791966258,
  "emit_ts": "2026-10-14T16:24:18+08:00"
}
```

示例仅说明字段结构。当前 ID 由数据源名、事件位点和时间戳组装，没有行序号/文件名；同一 binlog 行事件的多行可能共享 ID，因此不能仅按 id 去重，否则可能漏处理。上线前应为业务确定可靠去重键，或另行完善事件标识。`pk` 为主键列名，值从 before/after 读取。insert 可能没有 before，delete 可能没有 after；空字段按 JSON omitempty 省略。

## 监控

核心指标：`bincat_pipeline_up`、`bincat_binlog_delay_seconds`、`bincat_events_total`、`bincat_filtered_total`、`bincat_sink_batches_total`、`bincat_sink_send_duration_seconds`、`bincat_reconnects_total`、`bincat_checkpoint_pos`。按数据源/目标等标签查看；尚无数据时部分标签序列不会出现。结合实际投递量和位点检查，不要仅用 HTTP 存活判断数据健康。

# bincat 项目记忆

## 项目定位

MySQL binlog 变更监听 + 消息投递服务（Go 单二进制 + Web 后台）。
仓库：`/Users/yanglinqiang/Projects/source/mysql-replication`，module 名 `bincat`。

## 架构关键决策（勿轻易改动）

1. 基于 go-mysql-org/go-mysql canal 包；**不设** IncludeTableRegex（表过滤自研以支持热更新）。
2. **at-least-once 核心语义**：所有批次（含无命中事件的空批）必须经 Dispatcher 单
   worker 队列串行确认后才推进位点。曾因 AdvanceIdle 直推位点越过阻塞批次导致
   丢消息风险，已修复——改动 dispatcher 时保持此不变量。
3. sink 失败 = 阻塞（位点冻结）直到恢复，无丢失；消息带唯一 id 下游幂等。
4. 配置分离：YAML 只放元数据库 DSN/端口/JWT（改需重启）；数据源/规则/sink 全在
   元数据库，后台改完热生效。
5. 前端：单文件 Vue3+Element Plus（CDN）+ go:embed，故意不用 Node 构建。

## 元数据库表

datasource / sink / rule（owner_id 隔离）/ checkpoint（每源一行）/ admin_user / audit_log。
建表在 internal/meta/store.go migrate()（幂等），改表结构直接改那里。

## 环境

- Go 在 `~/sdk/go`（已写入 ~/.zshrc 的 PATH），GOPROXY=goproxy.cn。
- 本机 MySQL 9.7 即测试环境（root 密码用户口述）；binlog 参数已固化到
  `/etc/my.cnf`（ROW/FULL/FULL/GTID），重启验证通过；MySQL 由 launchd 管理，
  重启：`sudo launchctl kickstart -k system/com.oracle.oss.mysql.mysqld`。
- Web 后台初始 admin 密码在 configs/config.yaml（gitignored）。

## 未完成（M5）

kafka/rabbitmq sink 端到端（本机无 broker，代码已编译）、testcontainers 集成测试、
HA、全量初始化、DLQ 重放。

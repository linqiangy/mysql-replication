# MySQL 前置条件：检查、配置与复查

本指南用于准备被监听的 MySQL 和 bincat 元数据库。SQL 中的地址、库名及密码均为示例，需要替换。本文提供操作说明，不会自动修改或重启你的 MySQL。

## 1. 先确认检查的是哪个实例

bincat 使用两类连接：源 MySQL 提供业务 binlog；元数据库保存数据源、规则、用户、审计和位点。生产建议分开。binlog 条件应在源上检查，创建元数据库的 SQL 应在元数据库实例执行。

```bash
# 改成实际源地址和管理账号，密码交互输入
mysql -h 127.0.0.1 -P 3306 -u root -p
# 仓库 Compose 演示实例使用宿主机端口 33061
mysql -h 127.0.0.1 -P 33061 -u root -p
```

```sql
SELECT VERSION() AS mysql_version, @@hostname AS server_host, @@port AS server_port;
SELECT USER() AS login_identity, CURRENT_USER() AS matched_account;
```

记录版本；不要将 MySQL 8.x 配置直接复制到 5.7，也不要把 MariaDB 当作同一套配置。

## 2. 检查 binlog 和行变更参数

以下检查用管理账号执行，使用 GLOBAL 检查服务端默认值。`SHOW ... WHERE` 对不存在的变量不会报 Unknown system variable，而是不返回该行：

```sql
SHOW GLOBAL VARIABLES WHERE Variable_name IN (
  'log_bin', 'log_bin_basename', 'server_id',
  'binlog_format', 'binlog_row_image', 'binlog_row_metadata',
  'binlog_expire_logs_seconds', 'expire_logs_days',
  'binlog_expire_logs_auto_purge',
  'gtid_mode', 'enforce_gtid_consistency'
);
```

| 项目 | 预期/判断 | 不符合时怎么处理 |
| --- | --- | --- |
| log_bin | ON | 在实际生效的 mysqld 配置加 log-bin，移除冲突的 skip-log-bin/disable-log-bin，重启 MySQL |
| server_id | 非 0，拓扑内唯一 | 在 mysqld 配置设置唯一编号；不要与后台填写的复制客户端 server_id 相同 |
| binlog_format | ROW | 配置 binlog_format=ROW，维护窗口重启；已有业务连接的会话值也须生效 |
| binlog_row_image | FULL | 配置 binlog_row_image=FULL；字段过滤需要完整前后行值 |
| binlog_row_metadata | 支持时建议 FULL | 8.x 使用该配置；5.7 没有此变量，不能添加，否则无法启动 |
| 日志保留时间 | 覆盖停机、下游故障和恢复所需时间 | 8.x 用 binlog_expire_logs_seconds；5.7 用 expire_logs_days；示例为 10 天，按容量/故障恢复目标调整 |
| binlog_expire_logs_auto_purge | 支持时核对 ON/OFF | OFF 时不按保留秒数自动清理；为 ON 且保留秒数为 0 时仍不自动过期 |
| gtid_mode / enforce_gtid_consistency | 记录实际值 | bincat 当前按文件位点恢复，GTID 不是启动必需条件；已有复制集不要为本项目随意切换 |
| 源 binlog 库过滤 | 查看第 3 节状态输出的 Binlog_Do_DB / Binlog_Ignore_DB 及启动配置 | 源未记录的变更无法由 bincat 补回；由 DBA 调整源过滤规则 |

`binlog_row_image` 是行值完整程度；`binlog_row_metadata` 是列名称等附加元数据，二者不是同一个选项。官方定义见 [MySQL 8.0 binlog 参数](https://dev.mysql.com/doc/refman/8.0/en/replication-options-binary-log.html)。

当前依赖的 canal 通过查询源表结构构造列信息。权限不足、表已删除等导致无法取得结构时，项目开启的 `DiscardNoMetaRowEvent` 会跳过相关行事件；不能仅凭 5.7 没有 `binlog_row_metadata` 就判断不支持监听。旧版本兼容性需用实际事件验证，本仓库未提供完整版本测试矩阵。

## 3. 检查是否有可读 binlog 文件

```sql
SHOW BINARY LOGS;
```

应返回文件列表及大小；未开启时先完成第 4 节，权限错误则看第 6 节。检查当前文件位点时按版本选择一条：

```sql
-- MySQL 5.7 / 8.0
SHOW MASTER STATUS;
```

```sql
-- MySQL 8.4 及使用新语法的版本
SHOW BINARY LOG STATUS;
```

状态结果中的 `Binlog_Do_DB` / `Binlog_Ignore_DB` 与启动配置中的 `binlog-do-db` / `binlog-ignore-db` 可用于检查源库过滤，不能用 SHOW VARIABLES 没有返回这两项就认定不存在过滤。

8.4 已不支持 SHOW MASTER STATUS。[官方状态语法](https://dev.mysql.com/doc/refman/8.4/en/show-binary-log-status.html)、[官方文件列表语法](https://dev.mysql.com/doc/refman/8.4/en/show-binary-logs.html)。

本项目首次启动源时从当前位点开始，不能监听开启 binlog 前的历史变更；也不会做全量初始化。已有 checkpoint 对应的源文件必须仍存在。不要使用 RESET MASTER / RESET BINARY LOGS AND GTIDS 清空文件来“修复”连接问题。

## 4. 未开启时修改 MySQL 持久配置

### 4.1 找到实际配置文件

在数据库服务器上检查启动参数/服务定义；可能存在 `--defaults-file` 或额外 include。常见路径为 `/etc/my.cnf`、`/etc/mysql/my.cnf`、`/etc/mysql/mysql.conf.d/mysqld.cnf`，但不能仅凭路径存在就判定已加载。

```bash
# 在数据库服务器执行；仅查看工具说明，不启动数据库
mysqld --verbose --help 2>/dev/null | head -n 30
# Linux：先确定实际服务名，再检查它的 ExecStart
systemctl list-units --type=service | rg 'mysql|mysqld'
systemctl cat mysql
# 若实际服务名是 mysqld，上一条改为 systemctl cat mysqld
```

在上述输出中查找默认配置搜索顺序和 `--defaults-file`；同时检查配置里的 `!include` / `!includedir`。Homebrew/macOS 或 Windows 安装同样需先确认实际服务配置和文件位置。[MySQL 配置文件规则](https://dev.mysql.com/doc/refman/8.4/en/option-files.html)。

改动前备份实际配置，按原权限保留；在已有 `[mysqld]` 段修改以下项，避免重复定义。

### 4.2 MySQL 8.0 / 8.4 示例

```ini
[mysqld]
# 示例编号，必须替换/确认拓扑中唯一；不要覆盖已有复制拓扑的编号
server_id=1
log_bin=mysql-bin
binlog_format=ROW
binlog_row_image=FULL
binlog_row_metadata=FULL
# 10 天；上线前按日志量、容量和最长停机恢复时间调整
binlog_expire_logs_seconds=864000
```

支持 `binlog_expire_logs_auto_purge` 的版本还需确认其值与清理策略一致，不要把不存在的选项加入较旧版本。8.4 不再接受 expire_logs_days，应使用秒数。[8.4 binlog 配置与清理](https://dev.mysql.com/doc/refman/8.4/en/replication-options-binary-log.html)。

### 4.3 MySQL 5.7 示例

```ini
[mysqld]
server_id=1
log_bin=mysql-bin
binlog_format=ROW
binlog_row_image=FULL
expire_logs_days=10
```

5.7 不添加 `binlog_row_metadata` 或 8.x 的保留时间参数。[5.7 开启 binlog](https://dev.mysql.com/doc/mysql-replication-excerpt/5.7/en/replication-howto-masterbaseconfig.html)、[5.7 binlog 变量](https://dev.mysql.com/doc/mysql-replication-excerpt/5.7/en/replication-options-binary-log.html)。

其他版本先用第 2 节检查其变量是否存在，再对照对应版本手册修改，不能假设每个选项都兼容。

### 4.4 GTID 没开启是否需要改

当前 bincat 使用 `RunFrom` 文件位点路径，没有实现 GTID 集合恢复，所以无需为了运行 bincat 强制开启 GTID。后台开关应与源实际配置一致，不能代替数据库设置。

若其他复制需求要求开启：新实例可在建复制拓扑前配置下列两项；已有实例/复制集需按官方迁移流程处理匿名事务和各节点状态，不能直接复制两项再重启上线。

```ini
# 仅用于已确认可以启用 GTID 的新实例，非本项目必需
[mysqld]
enforce_gtid_consistency=ON
gtid_mode=ON
```

在线迁移步骤见 [MySQL 官方启用 GTID 流程](https://dev.mysql.com/doc/refman/8.4/en/replication-mode-change-online-enable-gtids.html)。

### 4.5 临时 SQL 与持久配置的区别

下面仅用于管理员确认允许动态修改的环境；不是必须执行的步骤：

```sql
SET GLOBAL binlog_format = 'ROW';
SET GLOBAL binlog_row_image = 'FULL';
-- 只在确认变量存在的 8.x 实例执行
SET GLOBAL binlog_row_metadata = 'FULL';
```

GLOBAL 修改不会自动更新已经建立的业务会话。仅在 DBA 控制的测试会话中可用 `SET SESSION binlog_format='ROW'` / `SET SESSION binlog_row_image='FULL'` 验证；生产应安排业务连接重建或维护窗口重启，并复查持久配置。`SET GLOBAL` 通常不能保证重启后保留；8.x 的 SET PERSIST 是另一种持久方式，若已有 mysqld-auto.cnf 设置，应检查是否与配置文件冲突。不要用 `SET GLOBAL log_bin=ON`，log_bin 非动态变量。[SET 与会话范围](https://dev.mysql.com/doc/refman/8.4/en/set-variable.html)、[log_bin 非动态属性](https://dev.mysql.com/doc/refman/8.4/en/replication-options-binary-log.html#sysvar_log_bin)。

## 5. 重启与复查

先安排 MySQL 维护窗口。重启源会中断数据库连接和复制客户端；只重启 bincat 无法使 MySQL 配置生效。

```bash
# Linux 只执行与你实际服务名匹配的一条
sudo systemctl restart mysql
# 或：sudo systemctl restart mysqld
sudo systemctl status mysql --no-pager
sudo journalctl -u mysql -n 100 --no-pager
```

macOS Homebrew 安装先执行 `brew services list` 查服务名，再使用 `brew services restart <实际服务名>`。macOS 官方安装包先用 `sudo launchctl print system/com.oracle.oss.mysql.mysqld` 确认该 label 存在，再使用 `sudo launchctl kickstart -k system/com.oracle.oss.mysql.mysqld`；其他安装方式不能照搬该 label。Windows 在服务管理器检查实际 MySQL 服务名后重启。

仓库演示 Compose 的设置来自 `deployments/docker-compose.yml` 的 command，修改后需要重建容器，保留数据卷：

```bash
cd deployments
docker compose up -d --force-recreate mysql-meta
docker compose ps
docker compose logs --tail=100 mysql-meta
```

不要用 `docker compose down -v`，它会删除演示数据卷。初始化目录中的 SQL 只在首次初始化空数据目录时执行，已有卷不会通过重建自动创建新账号。

重新连接 MySQL，重复第 1—3 节，并在实际写数据的业务连接上检查：

```sql
SELECT @@session.binlog_format, @@session.binlog_row_image, @@session.sql_log_bin;
```

应为 ROW、FULL、1；SQL_LOG_BIN=0 的会话不会记录这次业务变更。最终再重启 bincat，按部署文档验收源状态、位点及真实 HTTP/其他下游消息。

## 6. 监听账号：检查权限与补齐

示例假设 bincat 从 `10.0.0.20` 连接源，业务库为 `shop`。使用实际客户端地址（容器/NAT 后的源可见地址），不要无条件使用 `%`。

### 已有账号先检查

管理员：

```sql
SELECT User, Host FROM mysql.user WHERE User = 'bincat_source';
SHOW GRANTS FOR 'bincat_source'@'10.0.0.20';
```

再从 bincat 所在机器使用监听账号登录：

```bash
mysql -h SOURCE_HOST -P 3306 -u bincat_source -p
```

```sql
SELECT USER(), CURRENT_USER();
SHOW GRANTS;
SHOW BINARY LOGS;
SELECT * FROM shop.orders_2026 LIMIT 0;
```

权限应包含全局 REPLICATION SLAVE / REPLICATION CLIENT，以及实际业务库/表的 SELECT。CURRENT_USER 可核对命中的账号 host，避免只给另一个同名账号授权。读取表结构失败时检查该源上业务表是否存在以及账号是否有权访问。

### 账号不存在时创建，已存在时只补权限

在源上用管理员执行，先替换示例密码和 host：

```sql
CREATE USER 'bincat_source'@'10.0.0.20'
  IDENTIFIED BY 'REPLACE_WITH_STRONG_SOURCE_PASSWORD';
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.*
  TO 'bincat_source'@'10.0.0.20';
GRANT SELECT ON shop.* TO 'bincat_source'@'10.0.0.20';
SHOW GRANTS FOR 'bincat_source'@'10.0.0.20';
```

账号已存在时跳过 CREATE USER。密码确需更换时用 ALTER USER ... IDENTIFIED BY ...，同步更新后台数据源密码并重启 bincat 验证连接。CREATE USER / GRANT 生效不需要额外 FLUSH PRIVILEGES。监听账号不需要向业务库写入或修改表结构的权限。[复制和 SELECT 权限](https://dev.mysql.com/doc/refman/8.4/en/privileges-provided.html)、[账号创建](https://dev.mysql.com/doc/refman/8.4/en/create-user.html)、[授权](https://dev.mysql.com/doc/refman/8.4/en/grant.html)。

若连接报 Access denied，核对密码、Host 匹配和账户锁定状态；如果超时，检查 bind-address、网络和防火墙；如果错误涉及 TLS/认证插件，按目标版本及当前客户端支持处理，不要直接关闭全局安全设置。

## 7. 元数据库：检查、创建与验证

在元数据库实例上用管理员检查：

```sql
SHOW DATABASES LIKE 'bincat_meta';
SELECT User, Host FROM mysql.user WHERE User = 'bincat_meta_user';
```

如果没有数据库或账号，创建缺少部分：

```sql
CREATE DATABASE IF NOT EXISTS bincat_meta
  DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'bincat_meta_user'@'10.0.0.20'
  IDENTIFIED BY 'REPLACE_WITH_STRONG_META_PASSWORD';
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE ON bincat_meta.*
  TO 'bincat_meta_user'@'10.0.0.20';
SHOW GRANTS FOR 'bincat_meta_user'@'10.0.0.20';
```

这些权限对应当前自动建表和 CRUD；未来版本若有 ALTER 迁移需重新评估。已有账号跳过 CREATE USER，不必赋予源复制权限。源码含 JSON 列，MySQL 5.6 不适合作为当前元数据库；5.7 需支持 JSON 类型的版本，且仍需目标环境验证。

将地址与账号填入 `configs/config.yaml` 的 `meta.dsn`，生产模板则填 `BINCAT_META_DSN`。若数据库和账号已存在，用实际服务账号检查连接和权限：

```bash
mysql -h META_HOST -P 3306 -u bincat_meta_user -p bincat_meta
```

```sql
SELECT DATABASE(), CURRENT_USER();
SHOW GRANTS;
SHOW TABLES;
```

bincat 首次成功启动后，应看到 datasource、sink、rule、checkpoint、admin_user、audit_log 六张表。首次启动前没有表是正常的；如果启动报 CREATE command denied，应补建表权限，而不是手工随意创建不完整表。

## 8. 完成标准与常见故障

完成条件：源参数符合要求 → 监听账号能列出 binlog 并读取业务表 → 元数据库账号可连接/自动建表 → bincat pipeline 启动 → 配置规则后新产生的事件实际送达 → 重启后能从位点继续。

| 问题 | 处理 |
| --- | --- |
| log_bin 仍是 OFF | 实际配置未加载、选项被覆盖、启动参数禁用了日志，或只重启了 bincat |
| Unknown variable binlog_row_metadata | 先看版本；5.7 移除此项；8.x 检查拼写/发行版，不要为 5.7 使用 8.x 配置 |
| 改 GLOBAL 后仍产生 statement/minimal 事件 | 检查实际业务连接 session 值和是否重连 |
| 源测试成功但无回调 | 检查规则、源过滤、读表权限、日志和 Sink；连接测试未验证完整复制链路 |
| binlog 旧文件找不到 | 检查源保留时间与保存位点；缺失历史不能由本项目自动补回 |
| 元数据库启动失败 | DSN/库是否存在、账号 host、网络和 CREATE/读写权限 |

回调测试服务见 [HTTP 接收端说明](HTTP_CALLBACK_TEST.md)，完整部署步骤见 [部署文档](DEPLOYMENT.md)。

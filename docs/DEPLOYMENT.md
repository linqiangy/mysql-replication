# 部署与运维

MySQL 尚未准备好时，先按 [检查与配置指南](MYSQL_SETUP.md) 完成检查 SQL、参数修改、账号/元数据库创建及重启复查；需要观察 HTTP 回调时使用 [独立接收端](HTTP_CALLBACK_TEST.md)。

本文对应仓库当前源码。bincat 是 Go 单二进制服务，Web 页面和本地 Vue / Element Plus 资源在编译时嵌入；部署无需 Node.js。Docker Compose 仅启动演示 MySQL，不包含 bincat 应用。

## 1. 环境和部署选择

- 构建：Go 1.26.0 或更新的兼容工具链（以 `go.mod` 为准）、Git；首次构建需要下载 Go modules。
- 本地演示：Docker Engine / Docker Desktop 与 Compose 插件。没有 Docker 时可以使用已有 MySQL。
- Linux 常驻运行：使用本文的 systemd 示例。macOS 本地开发可使用 `bincat.sh`。
- 元数据库：须预先创建数据库及账号，应用首次启动自动创建六张元数据表。示例环境使用 MySQL 8.4；未建立各 MySQL 版本的完整兼容性测试矩阵。
- 被监听源：开启 binlog，使用 ROW / FULL。GTID 模式需与源设置一致；每个复制客户端的 `server_id` 必须唯一。支持 `binlog_row_metadata` 的源建议设置 FULL；当前 canal 也会查询表结构，5.7 没有此变量不应添加。缺少表结构/读取权限时可能跳过事件，需按实际版本验收。

生产元数据库与业务源建议分开。单个数据源仅运行一个消费实例；当前没有主备选举和多实例位点协调。

## 2. 本地快速启动

```bash
git clone git@github.com:linqiangy/mysql-replication.git
cd mysql-replication
go version
go mod download
cd deployments
docker compose up -d
# 等待 mysql-meta 状态为 healthy
docker compose ps
cd ..
cp configs/config.example.yaml configs/config.yaml
export BINCAT_ADMIN_PASSWORD='replace-with-a-local-admin-password'
make build GO="$(command -v go)"
./bin/bincat -config configs/config.yaml
```

访问 `http://localhost:8080/`，账号 `admin`，密码为首次启动时的 `BINCAT_ADMIN_PASSWORD`。Compose 元数据库连接为 `127.0.0.1:33061/bincat_meta`，账号 `bincat`，演示密码 `bincat123`。这些公开密码只供本地演示；生产必须独立创建账号和强密码，不能直接使用此 Compose 暴露到公网。

如果没有 Docker，在已有 MySQL 中创建元数据库和有建表、读写权限的账号，再修改 `configs/config.yaml` 中的 DSN。被监听源账号另需 `REPLICATION SLAVE`、`REPLICATION CLIENT` 和目标表 `SELECT` 权限。如何确认缺少权限、补齐授权、创建元数据库和核对六张表，见 [MySQL 准备指南](MYSQL_SETUP.md)。

本地后台方式：

```bash
./bincat.sh start
./bincat.sh status
tail -f logs/bincat.log
./bincat.sh stop
```

`bincat.sh start/restart` 不会自动编译。首页或内嵌资源修改后必须先 `make build GO="$(command -v go)"` 再重启。终端工具可能清理后台子进程；若启动提示成功但端口未监听，改用前台终端运行，Linux 生产使用 systemd。

## 3. 首次业务配置与验收

首次启动数据源从当前源位点开始，不回放此前历史数据；规则配置完成后再产生验收数据。当前恢复使用文件位点，GTID 开关尚未实现 GTID 集合恢复。

1. 登录后修改管理员密码。初始密码配置只在创建 admin 时有效，不会重置已有账号。
2. 创建数据源：本地 Compose 示例为 `127.0.0.1:33061`、`bincat / bincat123`，server_id 可设 `91001`，GTID 开启。生产填实际源地址，不要把 HTTP 端口当 MySQL 端口。
3. 点击“测试”：连接成功及 ROW/FULL 参数检查通过后显示“连接正常”。此按钮不能替代复制权限、binlog 订阅和下游投递验收。
4. 创建投递目标：本地可选 console。HTTP / Kafka / RabbitMQ 需配置真实目标并验收。
5. 创建规则：选择数据源、`shop`、`orders_*`、目标 Sink，监听 insert/update/delete；按需要选择 watch_fields。
6. 在演示源中插入一条测试订单，再更新其 status。确认 console 日志或实际下游收到事件，监控面板的状态/位点有变化。不要在生产表执行演示数据操作。
7. 重启 bincat 后继续更新测试订单，核对断点恢复和下游幂等；确认失败目标恢复后可继续投递。

```bash
# 演示数据库客户端交互连接（按提示输入演示密码）
mysql -h 127.0.0.1 -P 33061 -u root -p
```

```sql
USE shop;
INSERT INTO orders_2026 (order_no, status, amount)
VALUES ('bincat-smoke-001', 'created', 100.00);
UPDATE orders_2026 SET status = 'paid' WHERE order_no = 'bincat-smoke-001';
```

## 4. Linux systemd 部署

仓库提供 `deployments/systemd/bincat.service`、`configs/config.production.example.yaml` 和 `deployments/systemd/bincat.env.example`。它们是待配置模板，不是已经部署的服务。

### 构建并上传

在 Linux 目标机上构建最直接：

```bash
go mod download
go test ./...
go vet ./...
go build -trimpath -o bin/bincat ./cmd/bincat
```

在 macOS 等构建机生成 Linux amd64 产物：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/bincat-linux-amd64 ./cmd/bincat
```

ARM64 服务器改为 `GOARCH=arm64`。本文交叉编译仅为产物构建；上线前仍需在目标机验证运行及连接。

### 安装目录与配置

以下命令在目标 Linux 服务器执行，需管理员权限。`20261014-1` 为示例发布目录名，后续升级使用新目录。

```bash
sudo useradd --system --home-dir /opt/bincat --shell /usr/sbin/nologin bincat
sudo install -d -m 755 /opt/bincat/releases/20261014-1/bin
sudo install -d -m 750 -o root -g bincat /etc/bincat
sudo install -m 755 bin/bincat /opt/bincat/releases/20261014-1/bin/bincat
sudo ln -s /opt/bincat/releases/20261014-1 /opt/bincat/current
sudo install -m 640 -o root -g bincat configs/config.production.example.yaml /etc/bincat/config.yaml
sudo install -m 640 -o root -g bincat deployments/systemd/bincat.env.example /etc/bincat/bincat.env
sudoedit /etc/bincat/bincat.env
sudoedit /etc/bincat/config.yaml
sudo install -m 644 deployments/systemd/bincat.service /etc/systemd/system/bincat.service
```

账号已存在时跳过 useradd。发行版的 nologin 路径可能不同。填写环境文件中的全部占位值后再启动：

- `BINCAT_META_DSN`：元数据库 DSN，模板账号仅用于提示，必须替换。
- `BINCAT_ADMIN_PASSWORD`：首次 admin 密码。
- `BINCAT_SECRET_KEY`：固定保管的 32 字节密钥或其 Base64。可用 `openssl rand -base64 32` 生成。
- `BINCAT_JWT_SECRET`：固定随机签名密钥。可用 `openssl rand -hex 32` 生成。

环境文件为 systemd EnvironmentFile 格式，不要写 `export`，它不会执行命令替换；将生成的密钥值填入文件。YAML 插值发生在 YAML 解析之前，建议使用生成的字母数字/Base64 密钥；含引号等特殊字符的 DSN 需验证 YAML 转义。

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now bincat
sudo systemctl status bincat --no-pager
sudo journalctl -u bincat -n 100 --no-pager
curl -f http://127.0.0.1:8080/ -o /dev/null
curl -f http://127.0.0.1:9090/metrics
```

生产模板默认仅监听 `127.0.0.1:8080` 和 `127.0.0.1:9090`。远程管理可通过 SSH 转发访问：

```bash
ssh -L 8080:127.0.0.1:8080 your-user@your-server
```

也可在组织现有 HTTPS 反向代理后访问后台。9090 同时暴露 pprof，仅允许运维/监控网络访问。应用当前没有独立 readiness/health 接口，首页 HTTP 200 只证明 Web 服务可达，必须结合日志、pipeline 状态、位点和真实投递判断业务健康。

## 5. 升级、回滚和备份

升级前记录旧 `current` 链接目标、提交号和位点，备份元数据库与 `/etc/bincat` 配置/密钥。位点保存在元数据库，不在本地二进制目录；需要保留足够长的源 binlog，避免停机期间位点对应文件被清理。

将新二进制上传至独立版本目录，在停机后切换链接：

```bash
sudo systemctl stop bincat
sudo ln -sfn /opt/bincat/releases/NEW_VERSION /opt/bincat/current
sudo systemctl start bincat
sudo systemctl status bincat --no-pager
sudo journalctl -u bincat -n 100 --no-pager
```

如果新版本异常，停止服务、将链接切回之前记录的目录，再启动并复核投递。使用同一元数据库和同一数据源加密密钥。涉及未来表结构变更时，需先确认旧程序能读取新表结构；不能仅靠切换二进制保证回滚。不要直接恢复旧位点或元数据库备份来回滚程序，否则可能重放或覆盖运行期间的配置变更。

备份元数据库可由 DBA 使用已有备份系统，或使用 `mysqldump --single-transaction` 对 `bincat_meta` 备份（连接凭据放客户端受限配置/交互输入中）。恢复前停止应用，在隔离环境验证恢复后的账号、规则、Sink 和位点。加密密钥丢失后，原加密数据源密码无法解密；开启加密也不会自动迁移之前以明文存储的密码，应逐个重新保存密码并验收。

应用当前提供 at-least-once 设计，故障恢复可能重复投递，下游必须按业务事件去重。默认大事务超限策略 `skip_with_alert` 会丢弃超限事务事件；生产模板改用 `abort`，出现超限后需运维介入调整阈值。DLQ、全量初始化、HA 尚未实现。

## 6. 常见问题

| 现象 | 检查与处理 |
| --- | --- |
| 启动报元数据库连接失败 | DSN、账号、网络、数据库是否已建，以及建表权限；容器首次初始化只在空数据卷执行 |
| 首页空白或脚本无法加载 | 确认 vendor 文件齐全，重新构建并重启；修改前端 HTML 不会更新运行中的 embed 内容 |
| 重启后被要求重新登录 | 未配置固定 JWT 密钥时，每次启动生成随机密钥；配置固定值后再重启 |
| 配了初始密码却登录失败 | admin 已存在时不会覆盖原密码；使用原密码或已有管理员重置 |
| 数据源测试成功但没有消息 | 检查复制权限、pipeline 日志、规则库表/操作/watch_fields、Sink 状态；测试按钮不验证完整订阅投递 |
| 修改已有数据源连接配置未生效 | 当前对账不重建已运行实例，修改连接参数后重启应用；规则有独立 reload 能力 |
| 位点停止推进 | Sink 失败会阻塞并冻结确认位点；恢复下游后观察重试，避免贸然重置位点跳过数据 |
| 找不到旧 binlog 文件 | 检查源 binlog 保留期与停机时间；源文件已清理需制定重新初始化方案，当前不支持自动全量补齐 |
| 提示 secret key invalid / decrypt failed | 确认密钥格式与历史部署一致，不要直接轮换加密密钥 |

本次仓库发布的构建/测试记录见发布说明；systemd、Docker Compose 和外部 broker 的完整生产验收需在目标环境执行。

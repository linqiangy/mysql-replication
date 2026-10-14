# HTTP 回调测试接收端

`cmd/http-callback-test` 是独立 Go 测试服务，接收 bincat HTTP Sink 的探针/业务事件，格式化打印到启动它的控制台。只依赖标准库，不需要数据库或额外服务；bincat 主服务仍单独运行。

## 1. 启动

在项目根目录开启一个终端：

```bash
go run ./cmd/http-callback-test
```

默认监听 `127.0.0.1:18080`，回调 URL 为 `http://127.0.0.1:18080/callback`。如果使用项目自带 Go 路径：

```bash
~/sdk/go/bin/go run ./cmd/http-callback-test
```

自定义监听地址：

```bash
go run ./cmd/http-callback-test -addr 127.0.0.1:18081
```

没有 Go 工具链的目标机可先构建二进制再上传：

```bash
go build -o bin/http-callback-test ./cmd/http-callback-test
./bin/http-callback-test -addr 127.0.0.1:18080
# 在构建机为 Linux amd64 编译；ARM64 改 GOARCH
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/http-callback-test-linux-amd64 ./cmd/http-callback-test
```

服务启动失败（例如端口被占用）会输出错误并退出。默认仅本机可访问；需要让其他机器或容器访问时，使用实际网络地址，例如 `-addr 0.0.0.0:18080`，并把 URL 主机部分改为接收端机器可达 IP。停止服务使用 Ctrl+C，或对接收端进程发送 SIGTERM。

这是调试接收端，收到的事件会打印在控制台，不应将含敏感业务内容的回调发送到不受控的测试环境。不会打印 Authorization/Cookie 请求头。

## 2. 用 curl 检查接收端

```bash
curl -i http://127.0.0.1:18080/health
curl -i http://127.0.0.1:18080/callback \
  -H 'Content-Type: application/json' \
  --data '[{"id":"manual-test-001","datasource":"local","schema":"shop","table":"orders_2026","action":"update","after":{"id":1,"status":"paid","remark":"回调测试"}}]'
```

健康接口返回 HTTP 200 和 `{"ok":true}`。回调返回 HTTP 200 和 `{"ok":true,"received":1}`，同时启动服务的终端打印类似：

```text
[http-callback-test] 2026/10/14 18:00:00 收到 HTTP 回调 remote=127.0.0.1:... path=/callback events=1
[
  {
    "id": "manual-test-001",
    "datasource": "local",
    "schema": "shop",
    "table": "orders_2026",
    "action": "update",
    "after": {
      "id": 1,
      "status": "paid",
      "remark": "回调测试"
    }
  }
]
```

请求正文可为 JSON 对象或数组；数组返回元素数量，对象记为 1。非法 JSON/标量 JSON 返回 400，超过 10 MiB 返回 413，错误路径返回 404，GET /callback 返回 405。不要把 /health 配成 Sink URL，它只能验证存活。

## 3. bincat 后台配置

在“投递目标”中新增：

| 表单项 | 示例值 |
| --- | --- |
| 名称 | local-http-test |
| 类型 | http |
| URL | http://127.0.0.1:18080/callback |
| 超时（秒） | 5 |
| 启用 | 开启 |

保存后点击该目标的“测试”。当前后端会真实发送一条探针事件；接收端打印一个 JSON 数组，后台应提示“连通正常”。这一步验证 HTTP 目标，并没有验证 MySQL 订阅。

再把监听规则的目标设为此 Sink，在规则配置完成后产生测试库 insert/update/delete。接收端看到业务库、表、动作和 before/after，才说明完整源 → 规则 → HTTP 回调链路跑通。

bincat 和接收端若不在同一机器，或其中一个在容器里，`127.0.0.1` 指向各自环境。后台填写的地址必须从 bincat 进程所在网络可达：容器之间可使用同网络服务名；容器访问宿主机需按部署环境选择可达地址；远程部署使用测试接收端 IP。`0.0.0.0` 是监听地址，不应写进回调 URL。

## 4. 常见情况

- connection refused：接收端没启动、端口填错或 URL 指向错误机器/容器。
- HTTP 404/405：URL 必须是 /callback，当前 HTTP Sink 默认使用 POST。
- HTTP 400：请求不是 JSON 对象/数组；检查请求体，不要使用表单编码。
- HTTP 413：当前测试服务限制每个请求 10 MiB，需使用较小测试批次；此限制不代表 bincat 自身的批次限制。
- 有探针但没业务事件：检查 MySQL 配置、复制权限和监听规则，见 [MySQL 准备指南](MYSQL_SETUP.md)。
- 关闭接收端后位点停止：HTTP 目标失败会使主服务阻塞重试。恢复接收端后观察重发；测试结束前应先调整/停止相关规则，并核对没有待确认批次，避免把临时接收端留成生产目标。

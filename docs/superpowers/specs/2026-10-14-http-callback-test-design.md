# Go HTTP 回调测试服务设计

用户确认使用 Go 独立测试服务。目标是在控制台观察 bincat HTTP Sink 的探针和批次事件，不连接数据库，不修改 bincat 主服务。

- 入口：`cmd/http-callback-test`，仅使用 Go 标准库。
- 默认监听：`127.0.0.1:18080`，通过 `-addr` 指定地址/端口。
- `POST /callback` 接收 JSON 对象或数组，输出时间、远端、路径、事件数和格式化 JSON，返回 `{"ok":true,"received":N}`。
- `GET /health` 返回 `{"ok":true}`，仅表示测试接收端可达。
- 非法 JSON 返回 400，超过 10 MiB 的请求返回 413，不误报接收成功；日志不打印 Authorization/Cookie。
- 并发请求使用同一个标准日志器输出，避免批次正文相互穿插。
- Ctrl+C / SIGTERM 停止服务；启动失败打印原因。
- httptest 验证 JSON 批次、中文内容、探针对象、非法 JSON、大小限制、健康检查和方法限制；再用真实 HTTP 请求验证启动和控制台输出。

使用文档放在 `docs/HTTP_CALLBACK_TEST.md`，README 和部署文档链接到它。实现与 MySQL 前置配置指南一并提交推送。

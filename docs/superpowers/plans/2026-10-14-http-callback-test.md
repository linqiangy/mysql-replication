# HTTP 回调测试服务 Implementation Plan

> 在当前会话直接执行已确认范围；无需委派或另外创建工作区。

**Goal:** 提供可直接运行的 Go HTTP 回调接收端，在控制台观察 bincat 投递内容。

**Architecture:** 独立 cmd 入口，标准库 ServeMux 处理 callback/health，标准 log.Logger 输出一个完整请求日志。主服务和数据库配置不改动。

**Tech Stack:** Go 1.26、net/http、encoding/json、log、httptest。

- [x] 创建 `cmd/http-callback-test/main_test.go`，请求真实 handler 并断言状态码、接收数量及控制台输出；运行 `go test ./cmd/http-callback-test` 确认缺少 handler 时失败。
- [x] 创建 `cmd/http-callback-test/main.go`：`newHandler(*log.Logger) http.Handler` 提供 POST /callback 与 GET /health，main 解析 -addr 并处理停机信号。
- [x] `gofmt` 后运行针对性测试、全量 Go 测试及 vet；构建到临时目录，通过 curl 发送 JSON 数组与健康请求，确认控制台日志。
- [x] 编写 `docs/HTTP_CALLBACK_TEST.md`：启动/构建命令、curl、后台表单、探针与实际数据的区别、容器网络地址说明。
- [x] 补齐 `docs/MYSQL_SETUP.md`，README 和部署文档接入检查/修改/复查指南；仅更新文档，不执行数据库修改或重启。
- [ ] 检查文档链接、差异、Git 状态后提交推送 main，核对远端提交号。

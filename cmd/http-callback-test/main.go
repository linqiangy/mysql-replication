// http-callback-test 是 bincat HTTP Sink 的本地回调接收端。
// 用法：go run ./cmd/http-callback-test -addr 127.0.0.1:18080
// 收到的事件仅打印到控制台，不保存或写入数据库。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// newHandler 创建测试路由，使用传入日志器串行输出每批请求正文。
// POST /callback 接受 JSON 对象或数组，非法正文返回 400，超过 10 MiB 返回 413。
func newHandler(logger *log.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		io.WriteString(w, `{"ok":true}`)
	})
	mux.HandleFunc("POST /callback", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "callback body exceeds 10 MiB", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "cannot read callback body", http.StatusBadRequest)
			}
			return
		}
		body = bytes.TrimSpace(body)
		if !json.Valid(body) || (body[0] != '[' && body[0] != '{') {
			http.Error(w, "callback body must be a JSON object or array", http.StatusBadRequest)
			return
		}
		count := 1
		if body[0] == '[' {
			var events []json.RawMessage
			if err := json.Unmarshal(body, &events); err != nil {
				http.Error(w, "cannot decode callback events", http.StatusBadRequest)
				return
			}
			count = len(events)
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, body, "", "  "); err != nil {
			http.Error(w, "cannot format callback JSON", http.StatusBadRequest)
			return
		}
		logger.Printf("收到 HTTP 回调 remote=%s path=%s events=%d\n%s", r.RemoteAddr, r.URL.Path, count, pretty.String())
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(struct {
			OK       bool `json:"ok"`
			Received int  `json:"received"`
		}{OK: true, Received: count})
	})
	return mux
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "测试服务监听地址")
	flag.Parse()
	logger := log.New(os.Stdout, "[http-callback-test] ", log.LstdFlags)
	server := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	logger.Printf("启动接收端 addr=%s callback=/callback health=/health；Ctrl+C 停止", *addr)
	select {
	case err := <-done:
		logger.Fatalf("HTTP 测试服务启动或运行失败: %v", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("HTTP 测试服务停止失败: %v", err)
			server.Close()
		}
		logger.Print("HTTP 测试服务已停止")
	}
}

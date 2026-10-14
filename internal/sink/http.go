package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"bincat/internal/meta"
	"bincat/internal/model"
)

// HTTPSink Webhook 投递：POST JSON 数组到目标 URL；2xx 视为确认，
// 4xx 不重试（请求本身有问题，交由上层 block/dlq 策略），5xx/超时随上层重试。
type HTTPSink struct {
	name    string
	url     string
	method  string
	headers map[string]string
	timeout time.Duration
	client  *http.Client
}

// newHTTPSink 工厂：params = { url, method?, headers?, timeout_seconds? }
func newHTTPSink(cfg *meta.Sink) (Sink, error) {
	urlStr, _ := cfg.Params["url"].(string)
	if urlStr == "" {
		return nil, fmt.Errorf("http sink %q: params.url is required", cfg.Name)
	}
	method, _ := cfg.Params["method"].(string)
	if method == "" {
		method = http.MethodPost
	}
	timeout := 5 * time.Second
	if v, ok := cfg.Params["timeout_seconds"]; ok {
		switch t := v.(type) {
		case float64:
			timeout = time.Duration(t * float64(time.Second))
		case string:
			if n, err := strconv.Atoi(t); err == nil {
				timeout = time.Duration(n) * time.Second
			}
		}
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if hs, ok := cfg.Params["headers"].(map[string]any); ok {
		for k, v := range hs {
			headers[k] = fmt.Sprintf("%v", v)
		}
	}
	return &HTTPSink{
		name: cfg.Name, url: urlStr, method: method, headers: headers, timeout: timeout,
		client: &http.Client{Timeout: timeout},
	}, nil
}

// Name 实现 Sink
func (h *HTTPSink) Name() string { return h.name }

// Type 实现 Sink
func (h *HTTPSink) Type() string { return "http" }

// Open 实现 Sink（无长连接，直接可用）
func (h *HTTPSink) Open(ctx context.Context) error { return nil }

// SendBatch 实现 Sink：整批一个请求（数组体）
func (h *HTTPSink) SendBatch(ctx context.Context, events []*model.ChangeEvent) error {
	if len(events) == 0 {
		return nil
	}
	body, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("marshal events: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, h.method, h.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16)) // 丢弃响应体复用连接
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	// 4xx：请求自身问题，重试无意义——返回标记错误交上层策略处理
	return &ClientError{Code: resp.StatusCode, Msg: fmt.Sprintf("webhook responded %s", resp.Status)}
}

// ClientError 4xx 类错误（不重试）
type ClientError struct {
	Code int
	Msg  string
}

// Error 实现 error
func (e *ClientError) Error() string { return e.Msg }

// Close 实现 Sink
func (h *HTTPSink) Close(ctx context.Context) error {
	h.client.CloseIdleConnections()
	return nil
}

func init() { Register("http", newHTTPSink) }

// Package observability 提供结构化日志（slog）与 Prometheus 指标、pprof。
package observability

import (
	"log/slog"
	"os"
)

// NewLogger 按 config 初始化 slog（json / text），输出 stdout
func NewLogger(level, format string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	} else {
		h = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv})
	}
	return slog.New(h)
}

package observability

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/pprof"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsServer Prometheus 指标 + pprof HTTP 服务（与 Web 后台分端口）
type MetricsServer struct {
	srv *http.Server
}

// NewMetricsServer 构造（/metrics 与 /debug/pprof/*）
func NewMetricsServer(addr string) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return &MetricsServer{srv: &http.Server{Addr: addr, Handler: mux}}
}

// Start 异步启动（监听失败记日志）
func (s *MetricsServer) Start(log *slog.Logger) {
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("metrics server exited", "err", err)
		}
	}()
}

// Stop 优雅关闭
func (s *MetricsServer) Stop(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

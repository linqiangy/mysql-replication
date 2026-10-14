// bincat — MySQL binlog 变更监听与消息投递服务。
// 用法：bincat -config configs/config.yaml
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bincat/internal/app"
	"bincat/internal/config"
	"bincat/internal/observability"
)

func main() {
	cfgPath := flag.String("config", "configs/config.yaml", "启动配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("load config failed", "err", err, "path", *cfgPath)
		os.Exit(1)
	}
	logger := observability.NewLogger(cfg.Observability.Log.Level, cfg.Observability.Log.Format)

	a, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("init app failed", "err", err)
		os.Exit(1)
	}
	if err := a.Start(context.Background()); err != nil {
		logger.Error("start failed", "err", err)
		os.Exit(1)
	}

	// 优雅停机：SIGTERM/SIGINT
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	s := <-sig
	logger.Info("shutting down", "signal", s.String())
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Shutdown.DrainTimeout+10*time.Second)
	defer cancel()
	a.Stop(ctx)
}

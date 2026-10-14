// Package app 装配 bincat 各组件并管理进程生命周期。
package app

import (
	"context"
	"fmt"
	"log/slog"

	"bincat/internal/checkpoint"
	"bincat/internal/config"
	"bincat/internal/meta"
	"bincat/internal/observability"
	"bincat/internal/pipeline"
	"bincat/internal/server"
	"bincat/internal/sink"
)

// App 顶层应用：持有全部组件引用，负责启动顺序与优雅停机
type App struct {
	cfg      *config.Config
	log      *slog.Logger
	store    *meta.Store
	cpStore  checkpoint.Store
	cipher   *meta.Cipher
	sinkPool *sink.Pool
	manager  *pipeline.PipelineManager
	server   *server.Server
	metrics  *observability.MetricsServer
}

// New 装配（不启动）
func New(cfg *config.Config, log *slog.Logger) (*App, error) {
	store, err := meta.Open(cfg.Meta.DSN)
	if err != nil {
		return nil, fmt.Errorf("open meta store: %w", err)
	}
	cpStore := checkpoint.NewMySQLStore(store.DB())
	// 密钥未配置时数据源密码功能降级（无加密数据源则不影响运行）
	var cipher *meta.Cipher
	if key := cfg.SecretKeyOrEnv(); key != "" {
		cipher, err = meta.NewCipher(key)
		if err != nil {
			store.Close()
			return nil, fmt.Errorf("init cipher: %w", err)
		}
	} else {
		log.Warn("secret key not set, datasource password encryption disabled")
	}
	return &App{
		cfg: cfg, log: log, store: store, cpStore: cpStore, cipher: cipher, sinkPool: sink.NewPool(),
	}, nil
}
// Start 启动全部组件（pipeline 管理器 → Web 后台）
func (a *App) Start(ctx context.Context) error {
	if a.cfg.Observability.Metrics.Enabled {
		a.metrics = observability.NewMetricsServer(a.cfg.Observability.Metrics.Addr)
		a.metrics.Start(a.log)
		a.log.Info("metrics server started", "addr", a.cfg.Observability.Metrics.Addr)
	}
	mgr, err := pipeline.NewManager(ctx, a.store, a.cpStore, a.sinkPool, a.cipher, a.cfg.PipelineDefaults, a.cfg.Shutdown, a.log)
	if err != nil {
		return fmt.Errorf("start pipeline manager: %w", err)
	}
	a.manager = mgr

	// Web 后台
	srv := server.New(a.cfg, a.store, mgr, a.log)
	srv.SetDependencies(a.cpStore, a.cipher)
	if err := srv.EnsureAdmin(ctx); err != nil {
		a.log.Warn("ensure admin user failed", "err", err)
	}
	if err := srv.Start(); err != nil {
		return fmt.Errorf("start admin server: %w", err)
	}
	a.server = srv
	a.log.Info("bincat started", "addr", a.cfg.Server.Addr)
	return nil
}

// Stop 优雅停机：Web → pipeline → sink → 元数据库 → 指标
func (a *App) Stop(ctx context.Context) {
	if a.server != nil {
		_ = a.server.Stop(ctx)
	}
	if a.manager != nil {
		a.manager.StopAll()
	}
	a.sinkPool.CloseAll(ctx)
	if a.metrics != nil {
		_ = a.metrics.Stop(ctx)
	}
	if err := a.store.Close(); err != nil {
		a.log.Warn("close meta store", "err", err)
	}
	a.log.Info("bincat stopped")
}

// Package server 提供 Web 管理后台：REST API（gin + JWT）+ 前端静态资源（go:embed）。
// 权限模型：admin（数据源/用户/sink/位点/审计）+ 普通用户（自己的规则，按 owner 隔离）。
package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"bincat/internal/checkpoint"
	"bincat/internal/config"
	"bincat/internal/meta"
)

// Server Web 后台
type Server struct {
	cfg             *config.Config
	store           *meta.Store
	manager         HotReloader
	checkpointStore checkpoint.Store
	dsCipher        *meta.Cipher
	log             *slog.Logger
	router          *gin.Engine
	srv             *http.Server
}

// HotReloader 热更新入口（PipelineManager 实现）
type HotReloader interface {
	// Reconcile 数据源/sink 变更后对账（增删启停 pipeline、重建 sink 连接）
	Reconcile(ctx context.Context) error
	// ReloadRules 规则变更后重编译对应源的过滤引擎
	ReloadRules(ctx context.Context, datasourceID int64) error
	// Status 各数据源运行状态（监控面板用）
	Status() []map[string]any
}

// New 构造路由
func New(cfg *config.Config, store *meta.Store, manager HotReloader, log *slog.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	s := &Server{cfg: cfg, store: store, manager: manager, log: log, router: r}
	s.routes()
	return s
}

// routes 注册全部路由
func (s *Server) routes() {
	api := s.router.Group("/api/v1")
	// 公开
	api.POST("/login", s.handleLogin)
	// JWT 保护
	auth := api.Group("", s.authMiddleware())
	{
		auth.GET("/me", s.handleMe)
		auth.PUT("/me/password", s.handleChangeMyPassword)

		// 规则：admin 全部；user 仅自己的
		auth.GET("/rules", s.requireAny, s.handleListRules)
		auth.POST("/rules", s.requireAny, s.handleCreateRule)
		auth.PUT("/rules/:id", s.requireAny, s.handleUpdateRule)
		auth.DELETE("/rules/:id", s.requireAny, s.handleDeleteRule)
		// 元数据视图（建规则用）：数据源/sink 列表（密码不回显）
		auth.GET("/datasources", s.requireAny, s.handleListDatasources)
		auth.GET("/sinks", s.requireAny, s.handleListSinks)
		// 建表单需要的库/表/列信息
		auth.GET("/datasources/:id/schemas", s.requireAny, s.handleListSchemas)
		auth.GET("/datasources/:id/tables", s.requireAny, s.handleListTables)
		auth.GET("/datasources/:id/columns", s.requireAny, s.handleListColumns)

		// admin 专属
		admin := auth.Group("", s.requireAdmin)
		{
			admin.POST("/datasources", s.handleCreateDatasource)
			admin.PUT("/datasources/:id", s.handleUpdateDatasource)
			admin.DELETE("/datasources/:id", s.handleDeleteDatasource)
			admin.POST("/datasources/:id/test", s.handleTestDatasource)

			admin.POST("/sinks", s.handleCreateSink)
			admin.PUT("/sinks/:id", s.handleUpdateSink)
			admin.DELETE("/sinks/:id", s.handleDeleteSink)
			admin.POST("/sinks/:id/test", s.handleTestSink)

			admin.GET("/users", s.handleListUsers)
			admin.POST("/users", s.handleCreateUser)
			admin.PUT("/users/:id/password", s.handleResetUserPassword)
			admin.PUT("/users/:id/enabled", s.handleSetUserEnabled)

			admin.GET("/status", s.handleStatus)
			admin.GET("/checkpoints", s.handleListCheckpoints)
			admin.PUT("/checkpoints/:dsId/reset", s.handleResetCheckpoint)
			admin.GET("/audit", s.handleListAudit)
		}
		// 监控面板：user 也能看（仅自己规则涉及的数据源，Status 内按角色过滤）
		auth.GET("/dashboard", s.handleStatus)
	}
	// 前端静态资源
	s.mountFrontend()
}

// Start 异步启动 HTTP 服务
func (s *Server) Start() error {
	s.srv = &http.Server{Addr: s.cfg.Server.Addr, Handler: s.router}
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("admin server exited", "err", err)
		}
	}()
	s.log.Info("admin server started", "addr", s.cfg.Server.Addr)
	return nil
}

// Stop 优雅关闭
func (s *Server) Stop(ctx context.Context) error {
	if s.srv != nil {
		return s.srv.Shutdown(ctx)
	}
	return nil
}

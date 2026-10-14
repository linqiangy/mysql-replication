package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"bincat/internal/meta"
	"bincat/internal/model"
	"bincat/internal/sink"
)

// handleListSinks GET /api/v1/sinks（user 可见，建规则下拉用；params 中的敏感字段过滤可选）
func (s *Server) handleListSinks(c *gin.Context) {
	list, err := s.store.ListSinks(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

// handleCreateSink POST /api/v1/sinks（admin）
func (s *Server) handleCreateSink(c *gin.Context) {
	var req struct {
		Name   string         `json:"name" binding:"required"`
		Type   string         `json:"type" binding:"required"`
		Params map[string]any `json:"params"`
		Enabled *bool         `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	sk := &meta.Sink{Name: req.Name, Type: req.Type, Params: req.Params, Enabled: enabled}
	id, err := s.store.CreateSink(c.Request.Context(), sk)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "create", "sink", req.Name, nil)
	s.reconcileAsync(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// handleUpdateSink PUT /api/v1/sinks/:id（admin）
func (s *Server) handleUpdateSink(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx := c.Request.Context()
	sk, err := s.store.GetSink(ctx, id)
	if err != nil || sk == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "sink 不存在"})
		return
	}
	var req struct {
		Name   *string        `json:"name"`
		Type   *string        `json:"type"`
		Params *map[string]any `json:"params"`
		Enabled *bool         `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name != nil {
		sk.Name = *req.Name
	}
	if req.Type != nil {
		sk.Type = *req.Type
	}
	if req.Params != nil {
		sk.Params = *req.Params
	}
	if req.Enabled != nil {
		sk.Enabled = *req.Enabled
	}
	if err := s.store.UpdateSink(ctx, sk); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "update", "sink", sk.Name, nil)
	s.reconcileAsync(ctx)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleDeleteSink DELETE /api/v1/sinks/:id（admin；有规则引用则拒绝）
func (s *Server) handleDeleteSink(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx := c.Request.Context()
	// 校验无引用
	rules, err := s.store.ListRules(ctx, 0, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, r := range rules {
		if r.SinkID == id {
			c.JSON(http.StatusBadRequest, gin.H{"error": "规则 " + r.Name + " 仍引用该 sink，请先修改"})
			return
		}
	}
	sk, _ := s.store.GetSink(ctx, id)
	if err := s.store.DeleteSink(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if sk != nil {
		s.audit(c, "delete", "sink", sk.Name, nil)
	}
	s.reconcileAsync(ctx)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleTestSink POST /api/v1/sinks/:id/test（admin）：构建临时实例发探针
func (s *Server) handleTestSink(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx := c.Request.Context()
	cfg, err := s.store.GetSink(ctx, id)
	if err != nil || cfg == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "sink 不存在"})
		return
	}
	// console 直接成功
	if cfg.Type == "console" {
		c.JSON(http.StatusOK, gin.H{"ok": true, "message": "console sink 无需连接"})
		return
	}
	// 构建临时实例（探针带 test_ 前缀，不污染真实数据——kafka topic 例外，直接探测建连）
	probe := &meta.Sink{Name: "test_" + cfg.Name, Type: cfg.Type, Params: cfg.Params, Enabled: true}
	inst, err := sink.Build(probe)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := inst.Open(tctx); err != nil {
		s.audit(c, "test", "sink", cfg.Name, map[string]any{"ok": false})
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	// 发一条测试事件（kafka/rmq/http 均真实送达，便于端到端验证）
	err = inst.SendBatch(tctx, []*model.ChangeEvent{probeEvent()})
	inst.Close(context.Background())
	s.audit(c, "test", "sink", cfg.Name, map[string]any{"ok": err == nil})
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleListUsers GET /api/v1/users（admin）
func (s *Server) handleListUsers(c *gin.Context) {
	list, err := s.store.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type userView struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Role     string `json:"role"`
		Enabled  bool   `json:"enabled"`
	}
	out := make([]userView, 0, len(list))
	for _, u := range list {
		out = append(out, userView{ID: u.ID, Username: u.Username, Role: u.Role, Enabled: u.Enabled})
	}
	c.JSON(http.StatusOK, out)
}

// handleCreateUser POST /api/v1/users（admin）
func (s *Server) handleCreateUser(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required,min=6"`
		Role     string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	if req.Role != "user" && req.Role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role 仅支持 user/admin"})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	id, err := s.store.CreateUser(c.Request.Context(), &meta.User{
		Username: req.Username, PasswordHash: string(hash), Role: req.Role, Enabled: true,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "create", "user", req.Username, map[string]any{"role": req.Role})
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// handleResetUserPassword PUT /api/v1/users/:id/password（admin）
func (s *Server) handleResetUserPassword(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Password string `json:"password" binding:"required,min=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err := s.store.UpdatePassword(c.Request.Context(), id, string(hash)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "reset", "user", c.Param("id"), nil)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleSetUserEnabled PUT /api/v1/users/:id/enabled（admin）
func (s *Server) handleSetUserEnabled(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.store.SetUserEnabled(c.Request.Context(), id, req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "update", "user", c.Param("id"), map[string]any{"enabled": req.Enabled})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

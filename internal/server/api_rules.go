package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"bincat/internal/meta"
)

// handleListRules GET /api/v1/rules（user 仅自己的；admin 全部）
func (s *Server) handleListRules(c *gin.Context) {
	owner := int64(0)
	if !isAdmin(c) {
		owner = uid(c)
	}
	dsID, _ := strconv.ParseInt(c.Query("datasource_id"), 10, 64)
	rules, err := s.store.ListRules(c.Request.Context(), owner, dsID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type ruleView struct {
		ID           int64    `json:"id"`
		DatasourceID int64    `json:"datasource_id"`
		Name         string   `json:"name"`
		SchemaName   string   `json:"schema"`
		TablePattern string   `json:"table_pattern"`
		Actions      []string `json:"actions"`
		WatchFields  []string `json:"watch_fields"`
		SinkID       int64    `json:"sink_id"`
		OwnerID      int64    `json:"owner_id"`
		Enabled      bool     `json:"enabled"`
	}
	out := make([]ruleView, 0, len(rules))
	for _, r := range rules {
		out = append(out, ruleView{
			ID: r.ID, DatasourceID: r.DatasourceID, Name: r.Name, SchemaName: r.SchemaName,
			TablePattern: r.TablePattern, Actions: r.Actions, WatchFields: r.WatchFields,
			SinkID: r.SinkID, OwnerID: r.OwnerID, Enabled: r.Enabled,
		})
	}
	c.JSON(http.StatusOK, out)
}

// bindRule 规则请求体
type ruleReq struct {
	DatasourceID int64    `json:"datasource_id" binding:"required"`
	Name         string   `json:"name" binding:"required"`
	SchemaName   string   `json:"schema" binding:"required"`
	TablePattern string   `json:"table_pattern" binding:"required"`
	Actions      []string `json:"actions" binding:"required"`
	WatchFields  []string `json:"watch_fields"`
	SinkID       int64    `json:"sink_id" binding:"required"`
	Enabled      *bool    `json:"enabled"`
}

// validateRule 公共校验（sink 存在、actions 合法）
func (s *Server) validateRule(ctx context.Context, r *ruleReq) error {
	sk, err := s.store.GetSink(ctx, r.SinkID)
	if err != nil {
		return err
	}
	if sk == nil || !sk.Enabled {
		return fmt.Errorf("sink 不存在或已禁用 (id=%d)", r.SinkID)
	}
	ds, err := s.store.GetDataSource(ctx, r.DatasourceID)
	if err != nil {
		return err
	}
	if ds == nil {
		return fmt.Errorf("数据源不存在 (id=%d)", r.DatasourceID)
	}
	validActions := map[string]bool{"insert": true, "update": true, "delete": true}
	for _, a := range r.Actions {
		if !validActions[a] {
			return fmt.Errorf("非法操作类型 %q（仅 insert/update/delete）", a)
		}
	}
	return nil
}

// handleCreateRule POST /api/v1/rules
func (s *Server) handleCreateRule(c *gin.Context) {
	var req ruleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	if err := s.validateRule(ctx, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	r := &meta.Rule{
		DatasourceID: req.DatasourceID, Name: req.Name, SchemaName: req.SchemaName,
		TablePattern: req.TablePattern, Actions: req.Actions, WatchFields: req.WatchFields,
		SinkID: req.SinkID, OwnerID: uid(c), Enabled: enabled,
	}
	id, err := s.store.CreateRule(ctx, r)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "create", "rule", req.Name, nil)
	s.reloadRulesAsync(ctx, req.DatasourceID)
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// handleUpdateRule PUT /api/v1/rules/:id（admin 或 owner）
func (s *Server) handleUpdateRule(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx := c.Request.Context()
	r, err := s.store.GetRule(ctx, id)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "规则不存在"})
		return
	}
	if !isAdmin(c) && r.OwnerID != uid(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只能操作自己的规则"})
		return
	}
	var req ruleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.validateRule(ctx, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	oldDS := r.DatasourceID
	r.DatasourceID, r.Name, r.SchemaName = req.DatasourceID, req.Name, req.SchemaName
	r.TablePattern, r.Actions, r.WatchFields = req.TablePattern, req.Actions, req.WatchFields
	r.SinkID = req.SinkID
	if req.Enabled != nil {
		r.Enabled = *req.Enabled
	}
	if err := s.store.UpdateRule(ctx, r); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "update", "rule", r.Name, nil)
	s.reloadRulesAsync(ctx, oldDS)
	if oldDS != r.DatasourceID {
		s.reloadRulesAsync(ctx, r.DatasourceID)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleDeleteRule DELETE /api/v1/rules/:id（admin 或 owner）
func (s *Server) handleDeleteRule(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx := c.Request.Context()
	r, err := s.store.GetRule(ctx, id)
	if err != nil || r == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "规则不存在"})
		return
	}
	if !isAdmin(c) && r.OwnerID != uid(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "只能操作自己的规则"})
		return
	}
	if err := s.store.DeleteRule(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "delete", "rule", r.Name, nil)
	s.reloadRulesAsync(ctx, r.DatasourceID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// reconcileAsync 异步对账（API 快速返回；失败仅记日志）
func (s *Server) reconcileAsync(ctx context.Context) {
	if s.manager == nil {
		return
	}
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), requestTimeout*6)
		defer cancel()
		if err := s.manager.Reconcile(cctx); err != nil {
			s.log.Error("reconcile failed", "err", err)
		}
	}()
}

// reloadRulesAsync 异步热更新规则
func (s *Server) reloadRulesAsync(ctx context.Context, dsID int64) {
	if s.manager == nil {
		return
	}
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), requestTimeout*3)
		defer cancel()
		if err := s.manager.ReloadRules(cctx, dsID); err != nil {
			s.log.Error("reload rules failed", "datasource_id", dsID, "err", err)
		}
	}()
}

// mustJSON 序列化（失败返回 nil，审计用）
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

const requestTimeout = 5 * 1e9 // 5s（ns 字面量避免额外 import）

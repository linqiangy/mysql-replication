package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"bincat/internal/model"
)

// handleStatus GET /api/v1/status（监控面板；admin 全部，user 自己规则涉及的数据源）
func (s *Server) handleStatus(c *gin.Context) {
	if s.manager == nil {
		c.JSON(http.StatusOK, []map[string]any{})
		return
	}
	status := s.manager.Status()
	if isAdmin(c) {
		c.JSON(http.StatusOK, status)
		return
	}
	// user：仅自己规则涉及的数据源
	allowed := map[int64]bool{}
	rules, err := s.store.ListRules(c.Request.Context(), uid(c), 0)
	if err == nil {
		for _, r := range rules {
			allowed[r.DatasourceID] = true
		}
	}
	filtered := make([]map[string]any, 0)
	for _, st := range status {
		if id, ok := st["datasource_id"].(int64); ok && allowed[id] {
			filtered = append(filtered, st)
		}
	}
	c.JSON(http.StatusOK, filtered)
}

// handleListCheckpoints GET /api/v1/checkpoints（admin）
func (s *Server) handleListCheckpoints(c *gin.Context) {
	dss, err := s.store.ListDataSources(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type cpView struct {
		DatasourceID int64     `json:"datasource_id"`
		Datasource   string    `json:"datasource"`
		BinlogFile   string    `json:"binlog_file"`
		BinlogPos    uint32    `json:"binlog_pos"`
		GTIDSet      string    `json:"gtid_set"`
		UpdatedAt    time.Time `json:"updated_at"`
	}
	out := make([]cpView, 0, len(dss))
	for _, ds := range dss {
		v := cpView{DatasourceID: ds.ID, Datasource: ds.Name}
		if s.checkpointStore != nil {
			if cp, err := s.checkpointStore.Load(c.Request.Context(), ds.ID); err == nil && cp != nil {
				v.BinlogFile, v.BinlogPos, v.GTIDSet, v.UpdatedAt = cp.BinlogFile, cp.BinlogPos, cp.GTIDSet, cp.UpdatedAt
			}
		}
		out = append(out, v)
	}
	c.JSON(http.StatusOK, out)
}

// handleResetCheckpoint PUT /api/v1/checkpoints/:dsId/reset（admin；跳到当前 master，接受窗口丢失）
func (s *Server) handleResetCheckpoint(c *gin.Context) {
	dsID, _ := strconv.ParseInt(c.Param("dsId"), 10, 64)
	ds, err := s.store.GetDataSource(c.Request.Context(), dsID)
	if err != nil || ds == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "数据源不存在"})
		return
	}
	if s.checkpointStore != nil {
		if err := s.checkpointStore.Reset(c.Request.Context(), dsID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	s.audit(c, "reset", "checkpoint", ds.Name, map[string]any{"note": "跳到当前 master 位点，跳过期间变更"})
	c.JSON(http.StatusOK, gin.H{"ok": true, "note": "已重置，重启服务或等 pipeline 重连后从当前位点继续"})
}

// handleListAudit GET /api/v1/audit?limit=50&offset=0（admin）
func (s *Server) handleListAudit(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	list, err := s.store.ListAudit(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

// probeEvent 构造 sink 连通性测试事件
func probeEvent() *model.ChangeEvent {
	return &model.ChangeEvent{
		ID:         "bincat-probe",
		Datasource: "probe",
		Schema:     "bincat_test",
		Table:      "probe",
		Action:     model.ActionInsert,
		After:      map[string]any{"ok": true},
		Ts:         time.Now().Unix(),
		EmitTs:     time.Now(),
	}
}

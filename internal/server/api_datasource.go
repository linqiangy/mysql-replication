package server

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"bincat/internal/meta"
)

// dsView 数据源视图（密码永不回显）
type dsView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	User      string    `json:"user"`
	ServerID  uint32    `json:"server_id"`
	GTIDMode  bool      `json:"gtid_mode"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toDSView(ds *meta.DataSource) dsView {
	return dsView{ID: ds.ID, Name: ds.Name, Host: ds.Host, Port: ds.Port, User: ds.User,
		ServerID: ds.ServerID, GTIDMode: ds.GTIDMode, Enabled: ds.Enabled, UpdatedAt: ds.UpdatedAt}
}

// handleListDatasources GET /api/v1/datasources（user 也可见，建规则下拉用）
func (s *Server) handleListDatasources(c *gin.Context) {
	list, err := s.store.ListDataSources(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]dsView, 0, len(list))
	for _, ds := range list {
		out = append(out, toDSView(ds))
	}
	c.JSON(http.StatusOK, out)
}

// handleCreateDatasource POST /api/v1/datasources（admin）
func (s *Server) handleCreateDatasource(c *gin.Context) {
	var req struct {
		Name     string `json:"name" binding:"required"`
		Host     string `json:"host" binding:"required"`
		Port     int    `json:"port"`
		User     string `json:"user" binding:"required"`
		Password string `json:"password" binding:"required"`
		ServerID uint32 `json:"server_id" binding:"required"`
		GTIDMode *bool  `json:"gtid_mode"`
		Enabled  *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Port == 0 {
		req.Port = 3306
	}
	gtid := true
	if req.GTIDMode != nil {
		gtid = *req.GTIDMode
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	ds := &meta.DataSource{
		Name: req.Name, Host: req.Host, Port: req.Port, User: req.User,
		ServerID: req.ServerID, GTIDMode: gtid, Enabled: enabled,
	}
	// 密码加密（未配密钥则明文降级）
	if s.cipher() != nil {
		enc, err := s.cipher().Encrypt(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		ds.PasswordEnc = enc
	} else {
		ds.PasswordEnc = []byte(req.Password)
	}
	ctx := c.Request.Context()
	id, err := s.store.CreateDataSource(ctx, ds)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "create", "datasource", req.Name, map[string]any{"host": req.Host})
	s.reconcileAsync(ctx)
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// handleUpdateDatasource PUT /api/v1/datasources/:id（admin）
func (s *Server) handleUpdateDatasource(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx := c.Request.Context()
	ds, err := s.store.GetDataSource(ctx, id)
	if err != nil || ds == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "数据源不存在"})
		return
	}
	var req struct {
		Name     *string `json:"name"`
		Host     *string `json:"host"`
		Port     *int    `json:"port"`
		User     *string `json:"user"`
		Password *string `json:"password"`
		ServerID *uint32 `json:"server_id"`
		GTIDMode *bool   `json:"gtid_mode"`
		Enabled  *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name != nil {
		ds.Name = *req.Name
	}
	if req.Host != nil {
		ds.Host = *req.Host
	}
	if req.Port != nil {
		ds.Port = *req.Port
	}
	if req.User != nil {
		ds.User = *req.User
	}
	if req.ServerID != nil {
		ds.ServerID = *req.ServerID
	}
	if req.GTIDMode != nil {
		ds.GTIDMode = *req.GTIDMode
	}
	if req.Enabled != nil {
		ds.Enabled = *req.Enabled
	}
	ds.PasswordEnc = nil // 默认不改密码
	if req.Password != nil && *req.Password != "" {
		if s.cipher() != nil {
			if enc, err := s.cipher().Encrypt(*req.Password); err == nil {
				ds.PasswordEnc = enc
			}
		} else {
			ds.PasswordEnc = []byte(*req.Password)
		}
	}
	if err := s.store.UpdateDataSource(ctx, ds); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 连接参数变更需重建 pipeline（reconcile 目前只做增删，更新场景：先删内存实例再对账）
	s.audit(c, "update", "datasource", ds.Name, nil)
	s.reconcileAsync(ctx)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleDeleteDatasource DELETE /api/v1/datasources/:id（admin）
func (s *Server) handleDeleteDatasource(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx := c.Request.Context()
	// 校验无关联规则
	rules, err := s.store.ListRules(ctx, 0, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(rules) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("仍有 %d 条规则引用该数据源，请先删除", len(rules))})
		return
	}
	ds, _ := s.store.GetDataSource(ctx, id)
	if err := s.store.DeleteDataSource(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if ds != nil {
		s.audit(c, "delete", "datasource", ds.Name, nil)
	}
	s.reconcileAsync(ctx)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleTestDatasource POST /api/v1/datasources/:id/test（admin）：探活 + binlog 前置校验
func (s *Server) handleTestDatasource(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	ds, err := s.store.GetDataSource(ctx, id)
	if err != nil || ds == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "数据源不存在"})
		return
	}
	password := s.decryptDS(ds)
	result, err := testMySQL(ds.Host, ds.Port, ds.User, password)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	s.audit(c, "test", "datasource", ds.Name, nil)
	c.JSON(http.StatusOK, gin.H{"ok": result.OK, "error": result.Error, "checks": result.Checks})
}

// mysqlTestResult 连接测试结果
type mysqlTestResult struct {
	OK     bool              `json:"ok"`
	Error  string            `json:"error,omitempty"`
	Checks map[string]string `json:"checks"`
}

// testMySQL 探活 + binlog 参数校验（ROW/FULL/权限）
func testMySQL(host string, port int, user, password string) (*mysqlTestResult, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?parseTime=true&timeout=5s&readTimeout=5s",
		user, password, host, port)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	res := &mysqlTestResult{Checks: map[string]string{}}
	if err := db.Ping(); err != nil {
		res.Error = err.Error()
		return res, nil
	}
	res.OK = true
	checks := []struct{ k, q string }{
		{"binlog_format", "SELECT @@binlog_format"},
		{"binlog_row_image", "SELECT @@binlog_row_image"},
		{"binlog_row_metadata", "SELECT @@binlog_row_metadata"},
		{"log_bin", "SELECT @@log_bin"},
		{"server_id", "SELECT @@server_id"},
	}
	for _, ck := range checks {
		var v string
		if err := db.QueryRow(ck.q).Scan(&v); err != nil {
			res.Checks[ck.k] = "查询失败: " + err.Error()
			continue
		}
		res.Checks[ck.k] = v
	}
	// 关键校验
	if res.Checks["binlog_format"] != "ROW" {
		res.OK = false
		res.Error = "binlog_format 必须为 ROW"
	}
	if res.Checks["binlog_row_image"] != "FULL" {
		res.OK = false
		res.Error += "; binlog_row_image 建议 FULL（字段级过滤需要 before 全列）"
	}
	return res, nil
}

// handleListSchemas GET /api/v1/datasources/:id/schemas?like=（建规则表单）
func (s *Server) handleListSchemas(c *gin.Context) {
	s.withTargetDB(c, func(db *sql.DB) {
		rows, err := db.Query(`SELECT schema_name FROM information_schema.schemata
			WHERE schema_name NOT IN ('information_schema','mysql','performance_schema','sys') ORDER BY schema_name`)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var v string
			if rows.Scan(&v) == nil {
				out = append(out, v)
			}
		}
		c.JSON(http.StatusOK, out)
	})
}

// handleListTables GET /api/v1/datasources/:id/tables?schema=shop
func (s *Server) handleListTables(c *gin.Context) {
	schema := c.Query("schema")
	if schema == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "schema 参数必填"})
		return
	}
	s.withTargetDB(c, func(db *sql.DB) {
		rows, err := db.Query(`SELECT table_name FROM information_schema.tables
			WHERE table_schema=? ORDER BY table_name`, schema)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var v string
			if rows.Scan(&v) == nil {
				out = append(out, v)
			}
		}
		c.JSON(http.StatusOK, out)
	})
}

// handleListColumns GET /api/v1/datasources/:id/columns?schema=shop&table=user
func (s *Server) handleListColumns(c *gin.Context) {
	schema, table := c.Query("schema"), c.Query("table")
	if schema == "" || table == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "schema 与 table 参数必填"})
		return
	}
	s.withTargetDB(c, func(db *sql.DB) {
		rows, err := db.Query(`SELECT column_name, column_key FROM information_schema.columns
			WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`, schema, table)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()
		type col struct {
			Name string `json:"name"`
			Key  string `json:"key"`
		}
		var out []col
		for rows.Next() {
			var v col
			if rows.Scan(&v.Name, &v.Key) == nil {
				out = append(out, v)
			}
		}
		c.JSON(http.StatusOK, out)
	})
}

// withTargetDB 用指定数据源连接执行（只读元数据查询；密码不回传前端）
func (s *Server) withTargetDB(c *gin.Context, fn func(db *sql.DB)) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ds, err := s.store.GetDataSource(c.Request.Context(), id)
	if err != nil || ds == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "数据源不存在"})
		return
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?parseTime=true&timeout=5s&readTimeout=10s",
		ds.User, s.decryptDS(ds), ds.Host, ds.Port)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer db.Close()
	fn(db)
}

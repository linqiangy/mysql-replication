package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"bincat/internal/meta"
)

// Claims JWT 载荷
type Claims struct {
	UserID   int64  `json:"uid"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// handleLogin POST /api/v1/login {username, password} → {token}
func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	u, err := s.store.GetUser(ctx, req.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if u == nil || !u.Enabled || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	claims := Claims{
		UserID: u.ID, Username: u.Username, Role: u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.cfg.Server.JWTTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.Server.JWTSecret))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "role": u.Role, "username": u.Username})
}

// authMiddleware JWT 校验，注入用户信息
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		if len(h) < 8 || h[:7] != "Bearer " {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
			return
		}
		claims := &Claims{}
		_, err := jwt.ParseWithClaims(h[7:], claims, func(t *jwt.Token) (any, error) {
			return []byte(s.cfg.Server.JWTSecret), nil
		})
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "登录已过期"})
			return
		}
		c.Set("uid", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// requireAdmin 仅 admin
func (s *Server) requireAdmin(c *gin.Context) {
	if c.GetString("role") != "admin" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
		return
	}
	c.Next()
}

// requireAny 已登录即可（admin 或 user）
func (s *Server) requireAny(c *gin.Context) { c.Next() }

// isAdmin 当前请求是否 admin
func isAdmin(c *gin.Context) bool { return c.GetString("role") == "admin" }

// uid 当前用户 ID
func uid(c *gin.Context) int64 { return c.GetInt64("uid") }

// handleMe GET /api/v1/me
func (s *Server) handleMe(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"user_id": uid(c), "username": c.GetString("username"), "role": c.GetString("role"),
	})
}

// handleChangeMyPassword PUT /api/v1/me/password {old_password, new_password}
func (s *Server) handleChangeMyPassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=6"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx := c.Request.Context()
	u, err := s.store.GetUserByID(ctx, uid(c))
	if err != nil || u == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.OldPassword)) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "原密码错误"})
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err := s.store.UpdatePassword(ctx, u.ID, string(hash)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.audit(c, "update", "user", u.Username, nil)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// audit 写审计日志（失败仅记日志不阻断）
func (s *Server) audit(c *gin.Context, action, resource, resourceID string, detail map[string]any) {
	var detailBytes []byte
	if detail != nil {
		detailBytes = mustJSON(detail)
	}
	if err := s.store.Audit(c.Request.Context(), uid(c), action, resource, resourceID, detailBytes); err != nil {
		s.log.Warn("audit write failed", "err", err)
	}
}

// ensureAdminUser 启动时确保 admin 存在（首启按配置初始密码创建）
func ensureAdminUser(ctx context.Context, store *meta.Store, username, password string, log interface{ Warn(string, ...any) }) error {
	u, err := store.GetUser(ctx, username)
	if err != nil {
		return err
	}
	if u != nil {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = store.CreateUser(ctx, &meta.User{
		Username: username, PasswordHash: string(hash), Role: "admin", Enabled: true,
	})
	return err
}

var errNotFound = errors.New("not found")

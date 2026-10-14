package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"bincat/internal/meta"
)

//go:embed all:frontend
var frontendFS embed.FS

// mountFrontend 挂载内嵌前端（SPA：非 /api 路径全部回退 index.html）
func (s *Server) mountFrontend() {
	sub, err := fs.Sub(frontendFS, "frontend")
	if err != nil {
		return // 无前端产物时仅提供 API
	}
	fileServer := http.FileServer(http.FS(sub))
	s.router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		// API 404 保持 JSON
		if strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		// 静态资源存在则直出，否则回退 index.html（SPA 路由）
		trimmed := strings.TrimPrefix(path, "/")
		if trimmed != "" {
			if f, err := sub.Open(trimmed); err == nil {
				f.Close()
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		// SPA fallback
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			c.String(http.StatusNotFound, "frontend not built")
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}

// cipher 返回数据源密码加密器（app 注入；nil = 未启用）
func (s *Server) cipher() *meta.Cipher { return s.dsCipher }

// decryptDS 解密数据源密码（未启用加密则 PasswordEnc 即明文）
func (s *Server) decryptDS(ds *meta.DataSource) string {
	if s.dsCipher != nil && len(ds.PasswordEnc) > 0 {
		if pw, err := s.dsCipher.Decrypt(ds.PasswordEnc); err == nil {
			return pw
		}
	}
	return string(ds.PasswordEnc)
}

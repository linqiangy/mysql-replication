package server

import (
	"context"

	"bincat/internal/checkpoint"
	"bincat/internal/meta"
)

// SetDependencies 注入位点存储与加密器（app 装配时调用）
func (s *Server) SetDependencies(cpStore checkpoint.Store, cipher *meta.Cipher) {
	s.checkpointStore = cpStore
	s.dsCipher = cipher
}

// EnsureAdmin 启动时确保 admin 存在（对外导出给 app 调用）
func (s *Server) EnsureAdmin(ctx context.Context) error {
	return ensureAdminUser(ctx, s.store, "admin", s.cfg.Server.InitialAdminPassword, s.log)
}

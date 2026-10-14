package config

import (
	"errors"
	"fmt"
	"os"
)

// Validate 校验启动配置完整性；返回首个错误
func (c *Config) Validate() error {
	if c.Meta.DSN == "" {
		return errors.New("meta.dsn is required")
	}
	if c.Server.Addr == "" {
		return errors.New("server.addr is required")
	}
	if c.Server.JWTSecret == "" {
		// 未配置则自动生成随机密钥（进程重启后旧 JWT 失效，可接受）
		c.Server.JWTSecret = randomHex(32)
	}
	if c.Server.InitialAdminPassword == "" {
		c.Server.InitialAdminPassword = "admin123" // 首次启动强制改密逻辑在 M3 用户管理实现
	}
	if c.PipelineDefaults.OnTxOverflow != "skip_with_alert" && c.PipelineDefaults.OnTxOverflow != "abort" {
		return fmt.Errorf("pipeline_defaults.on_tx_overflow must be skip_with_alert or abort, got %q", c.PipelineDefaults.OnTxOverflow)
	}
	if c.PipelineDefaults.OnSinkExhausted != "block" && c.PipelineDefaults.OnSinkExhausted != "dlq" {
		return fmt.Errorf("pipeline_defaults.on_sink_exhausted must be block or dlq, got %q", c.PipelineDefaults.OnSinkExhausted)
	}
	if c.PipelineDefaults.DispatchQueueSize <= 0 || c.PipelineDefaults.AckWindowSize <= 0 || c.PipelineDefaults.MaxTxRows <= 0 {
		return errors.New("pipeline_defaults queue/window/tx-rows must be positive")
	}
	return nil
}

// SecretKeyOrEnv 返回数据源密码加密密钥：配置文件优先，其次 BINCAT_SECRET_KEY 环境变量
func (c *Config) SecretKeyOrEnv() string {
	if c.Meta.SecretKey != "" {
		return c.Meta.SecretKey
	}
	return os.Getenv("BINCAT_SECRET_KEY")
}

// randomHex 生成 n 字节随机数的 hex 字符串（JWT 密钥兜底）
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := randRead(b); err != nil {
		panic(fmt.Sprintf("generate random secret: %v", err))
	}
	return hexEncode(b)
}

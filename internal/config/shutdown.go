package config

import "time"

// ShutdownDrain 返回优雅停机 drain 超时（供 pipeline 层调用）
func (c *Config) ShutdownDrain() time.Duration {
	return c.Shutdown.DrainTimeout
}

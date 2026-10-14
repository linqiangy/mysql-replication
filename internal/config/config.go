// Package config 负责 bincat 启动配置（静态部分）的加载、环境变量插值与校验。
// 数据源、规则、sink 等动态配置存元数据库，由 Web 后台管理。
package config

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// Config bincat 启动配置（configs/config.yaml）
type Config struct {
	// Meta 元数据库连接与位点存储配置
	Meta MetaConfig `yaml:"meta"`
	// Server Web 后台服务配置
	Server ServerConfig `yaml:"server"`
	// Observability 日志与指标配置
	Observability ObservabilityConfig `yaml:"observability"`
	// PipelineDefaults 各数据源 pipeline 的默认运行参数
	PipelineDefaults PipelineDefaults `yaml:"pipeline_defaults"`
	// Shutdown 优雅停机配置
	Shutdown ShutdownConfig `yaml:"shutdown"`
}

// MetaConfig 元数据库配置（存数据源/规则/sink/位点/用户，独立于被监听源）
type MetaConfig struct {
	// DSN 元数据库连接串，如 "bincat:pass@tcp(127.0.0.1:3306)/bincat_meta"
	DSN string `yaml:"dsn"`
	// SecretKey 数据源密码加密密钥（AES-GCM，32 字节 base64 或原文）；也可用环境变量 BINCAT_SECRET_KEY
	SecretKey string `yaml:"secret_key"`
}

// ServerConfig Web 后台配置
type ServerConfig struct {
	// Addr HTTP 监听地址，如 ":8080"
	Addr string `yaml:"addr"`
	// InitialAdminPassword 首次启动创建 admin 账号的密码；支持 ${ENV} 插值
	InitialAdminPassword string `yaml:"initial_admin_password"`
	// JWTSecret JWT 签名密钥；支持 ${ENV} 插值
	JWTSecret string `yaml:"jwt_secret"`
	// JWTTTL JWT 有效期，默认 12h
	JWTTTL time.Duration `yaml:"jwt_ttl"`
}

// ObservabilityConfig 日志与指标配置
type ObservabilityConfig struct {
	Log struct {
		// Level 日志级别：debug/info/warn/error
		Level string `yaml:"level"`
		// Format 日志格式：json / text
		Format string `yaml:"format"`
	} `yaml:"log"`
	Metrics struct {
		// Enabled 是否启用 Prometheus 指标
		Enabled bool `yaml:"enabled"`
		// Addr 指标与 pprof HTTP 监听地址，如 ":9090"
		Addr string `yaml:"addr"`
	} `yaml:"metrics"`
}

// PipelineDefaults pipeline 默认运行参数（数据源级可在元数据库覆盖，暂以全局默认生效）
type PipelineDefaults struct {
	// DispatchQueueSize 待派发批次队列长度（背压阈值）
	DispatchQueueSize int `yaml:"dispatch_queue_size"`
	// AckWindowSize 最大在途（未全部确认）批次数
	AckWindowSize int `yaml:"ack_window_size"`
	// MaxTxRows 单事务行数上限，超过按 OnTxOverflow 策略处理
	MaxTxRows int `yaml:"max_tx_rows"`
	// OnTxOverflow 大事务策略：skip_with_alert（丢弃整事务并告警）/ abort
	OnTxOverflow string `yaml:"on_tx_overflow"`
	// CheckpointFlushInterval 位点落盘最小间隔（兜底定时刷）
	CheckpointFlushInterval time.Duration `yaml:"checkpoint_flush_interval"`
	// OnSinkExhausted sink 重试耗尽策略：block（阻塞保不丢）/ dlq（死信文件继续）
	OnSinkExhausted string `yaml:"on_sink_exhausted"`
}

// ShutdownConfig 优雅停机配置
type ShutdownConfig struct {
	// DrainTimeout 停机排空在途批次的时间上限
	DrainTimeout time.Duration `yaml:"drain_timeout"`
}

// Default 返回带默认值的配置
func Default() Config {
	var c Config
	c.Server.Addr = ":8080"
	c.Server.JWTTTL = 12 * time.Hour
	c.Observability.Log.Level = "info"
	c.Observability.Log.Format = "json"
	c.Observability.Metrics.Enabled = true
	c.Observability.Metrics.Addr = ":9090"
	c.PipelineDefaults.DispatchQueueSize = 256
	c.PipelineDefaults.AckWindowSize = 64
	c.PipelineDefaults.MaxTxRows = 100000
	c.PipelineDefaults.OnTxOverflow = "skip_with_alert"
	c.PipelineDefaults.CheckpointFlushInterval = 2 * time.Second
	c.PipelineDefaults.OnSinkExhausted = "block"
	c.Shutdown.DrainTimeout = 30 * time.Second
	return c
}

// envPattern ${VAR} 形式环境变量引用
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnv 展开 YAML 值中的 ${VAR} 环境变量引用；未定义变量展开为空串并警告
func expandEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(m string) string {
		name := envPattern.FindStringSubmatch(m)[1]
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return ""
	})
}

// Load 从 path 读取 YAML 配置并应用默认值与环境变量插值
func Load(path string) (*Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	expanded := expandEnv(string(data))
	if err := yaml.Unmarshal([]byte(expanded), &c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

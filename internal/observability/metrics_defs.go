// Package observability 提供结构化日志（slog）与 Prometheus 指标、pprof。
package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics 全局指标集合（按 datasource/sink 维度打标）
type Metrics struct {
	// BinlogDelay binlog 复制延迟（当前事件时间戳与本地时间差，秒）
	BinlogDelay *prometheus.GaugeVec
	// EventsTotal 已投递事件数
	EventsTotal *prometheus.CounterVec
	// FilteredTotal 被过滤事件数（reason: table/action/field）
	FilteredTotal *prometheus.CounterVec
	// SinkBatches sink 批次投递结果（status: ack/exhausted）
	SinkBatches *prometheus.CounterVec
	// SinkSendDuration sink 发送耗时
	SinkSendDuration *prometheus.HistogramVec
	// Reconnects pipeline 重连次数
	Reconnects *prometheus.CounterVec
	// PipelineUp pipeline 存活状态
	PipelineUp *prometheus.GaugeVec
	// CheckpointPos 已确认位点（数值化，仅文件位点模式有参考意义）
	CheckpointPos *prometheus.GaugeVec
}

// globalMetrics 全局单例（各包通过 observability.M 引用）
var globalMetrics = newMetrics()

// M 返回全局指标
func M() *Metrics { return globalMetrics }

func newMetrics() *Metrics {
	ns := "bincat"
	return &Metrics{
		BinlogDelay: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "binlog_delay_seconds",
			Help: "Replication delay: now minus binlog event timestamp",
		}, []string{"datasource"}),
		EventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "events_total",
			Help: "Change events delivered to sinks",
		}, []string{"datasource", "schema", "table", "action"}),
		FilteredTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "filtered_total",
			Help: "Events dropped by filter (reason: table/action/field)",
		}, []string{"datasource", "rule", "reason"}),
		SinkBatches: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "sink_batches_total",
			Help: "Sink batch delivery results (status: ack/exhausted)",
		}, []string{"datasource", "sink", "status"}),
		SinkSendDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: ns, Name: "sink_send_duration_seconds",
			Help: "Sink send duration",
			Buckets: []float64{.001, .005, .01, .05, .1, .5, 1, 5, 30},
		}, []string{"sink"}),
		Reconnects: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: ns, Name: "reconnects_total",
			Help: "Pipeline restarts (canal run loop re-entry)",
		}, []string{"datasource"}),
		PipelineUp: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "pipeline_up",
			Help: "Pipeline liveness (1 running)",
		}, []string{"datasource"}),
		CheckpointPos: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: ns, Name: "checkpoint_pos",
			Help: "Last acked binlog file position",
		}, []string{"datasource"}),
	}
}

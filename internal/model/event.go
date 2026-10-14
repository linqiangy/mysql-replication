package model

import "time"

// Action 类型常量：数据库变更操作类型
const (
	ActionInsert = "insert"
	ActionUpdate = "update"
	ActionDelete = "delete"
)

// ChangeEvent 统一消息模型：一条 binlog 行变更经规则过滤后封装为此结构，
// 序列化为 JSON 投递到各 sink。下游按 ID 幂等去重。
type ChangeEvent struct {
	// ID 全局唯一事件标识：binlog文件名:位点（GTID 模式追加 gtid），下游幂等键
	ID         string `json:"id"`
	// Datasource 数据源名称（bincat 内配置的 datasource.name）
	Datasource string `json:"datasource"`
	// Schema 库名
	Schema string `json:"schema"`
	// Table 表名
	Table string `json:"table"`
	// Action 操作类型：insert / update / delete
	Action string `json:"action"`
	// Before 变更前行数据（map[列名]值；insert 时为 nil）
	Before map[string]any `json:"before,omitempty"`
	// After 变更后行数据（map[列名]值；delete 时为 nil）
	After map[string]any `json:"after,omitempty"`
	// Changed 命中的 watch_fields 中实际发生变化的字段列表
	Changed []string `json:"changed,omitempty"`
	// Rule 命中的规则名称
	Rule string `json:"rule"`
	// PK 主键列名（可能多个），用于下游定位行
	PK []string `json:"pk,omitempty"`
	// Ts 事件产生时间（binlog 事件时间戳）
	Ts int64 `json:"ts"`
	// EmitTs 本服务封装该事件的本地时间
	EmitTs time.Time `json:"emit_ts"`
}

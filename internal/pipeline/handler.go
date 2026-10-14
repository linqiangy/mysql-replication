package pipeline

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"

	"bincat/internal/model"
)

// binlogHandler 实现 canal.EventHandler：过滤 → 封装 ChangeEvent → 批次缓冲。
// 每个 pipeline（数据源）一个实例；canal 事件循环单线程回调，无并发。
type binlogHandler struct {
	canal.DummyEventHandler
	dsName       string
	engine       *FilterEngine
	disp         *Dispatcher
	log          *slog.Logger
	curGTIDSet   string
	txEvents     []*sinkEvent
	txRows       int
	maxTxRows    int
	onTxOverflow string // skip_with_alert / abort
}

// String 实现 canal.EventHandler
func (h *binlogHandler) String() string { return "bincat:" + h.dsName }

// OnRow 行变更事件：三级过滤 → 封装 → 事务缓冲
func (h *binlogHandler) OnRow(e *canal.RowsEvent) error {
	defer h.recoverPanic("OnRow")
	action := rowsAction(e)
	h.txRows += len(e.Rows)
	if h.maxTxRows > 0 && h.txRows > h.maxTxRows {
		return h.handleTxOverflow()
	}
	before, after := extractRows(e, action)
	for _, m := range h.engine.Match(e.Table.Schema, e.Table.Name, action, before, after) {
		h.txEvents = append(h.txEvents, h.buildSinkEvent(e, action, m, before, after))
	}
	return nil
}

// extractRows 按 action 提取 before/after（UPDATE 的 Rows 为成对布局，取第一对）
func extractRows(e *canal.RowsEvent, action string) (before, after map[string]any) {
	switch action {
	case model.ActionUpdate:
		if len(e.Rows) >= 2 {
			before = RowsToMap(e.Table, e.Rows[0])
			after = RowsToMap(e.Table, e.Rows[1])
		}
	case model.ActionInsert:
		if len(e.Rows) >= 1 {
			after = RowsToMap(e.Table, e.Rows[0])
		}
	case model.ActionDelete:
		if len(e.Rows) >= 1 {
			before = RowsToMap(e.Table, e.Rows[0])
		}
	}
	return before, after
}

// buildSinkEvent 封装统一消息模型（含目标 sink 名）
func (h *binlogHandler) buildSinkEvent(e *canal.RowsEvent, action string, m MatchResult, before, after map[string]any) *sinkEvent {
	id := fmt.Sprintf("%s:%d:%d", h.dsName, e.Header.LogPos, e.Header.Timestamp)
	pk := make([]string, 0, len(e.Table.PKColumns))
	for _, i := range e.Table.PKColumns {
		if i < len(e.Table.Columns) {
			pk = append(pk, e.Table.Columns[i].Name)
		}
	}
	return &sinkEvent{
		ChangeEvent: &model.ChangeEvent{
			ID:         id,
			Datasource: h.dsName,
			Schema:     e.Table.Schema,
			Table:      e.Table.Name,
			Action:     action,
			Before:     before,
			After:      after,
			Changed:    m.Changed,
			Rule:       m.Rule.Name,
			PK:         pk,
			Ts:         int64(e.Header.Timestamp),
			EmitTs:     time.Now(),
		},
		SinkName: m.SinkName,
	}
}

// OnXID 事务提交：封批交给 dispatcher（空批直接推进位点）
func (h *binlogHandler) OnXID(header *replication.EventHeader, nextPos mysql.Position) error {
	defer h.recoverPanic("OnXID")
	if len(h.txEvents) > 0 {
		h.disp.Enqueue(Batch{
			EndPos:  Position{Name: nextPos.Name, Pos: nextPos.Pos},
			GTIDSet: h.curGTIDSet,
			Events:  h.txEvents,
		})
		h.txEvents = nil
	} else {
		h.disp.AdvanceIdle(Position{Name: nextPos.Name, Pos: nextPos.Pos})
	}
	h.txRows = 0
	return nil
}

// OnGTID GTID 事件：记录当前 GTID set 字符串（随批次位点附带保存）
func (h *binlogHandler) OnGTID(header *replication.EventHeader, gtidEvent mysql.BinlogGTIDEvent) error {
	if gs, err := gtidEvent.GTIDNext(); err == nil && gs != nil {
		h.curGTIDSet = gs.String()
	}
	return nil
}

// OnDDL DDL：canal 已自动刷新表结构缓存；记日志即可
func (h *binlogHandler) OnDDL(header *replication.EventHeader, nextPos mysql.Position, queryEvent *replication.QueryEvent) error {
	h.log.Info("ddl detected",
		"schema", string(queryEvent.Schema),
		"query", truncate(string(queryEvent.Query), 200))
	return nil
}

// OnTableChanged 表结构变化（canal 回调）
func (h *binlogHandler) OnTableChanged(header *replication.EventHeader, schemaName, table string) error {
	h.log.Info("table schema changed", "schema", schemaName, "table", table)
	return nil
}

// OnRowsQueryEvent 原始 SQL（binlog_rows_query_log_events 开启时）暂不处理
func (h *binlogHandler) OnRowsQueryEvent(e *replication.RowsQueryEvent) error { return nil }

// OnTableNotFound 行事件引用已不存在的表：丢弃并告警（不阻断）
func (h *binlogHandler) OnTableNotFound(header *replication.EventHeader, e *replication.RowsEvent) error {
	h.log.Warn("rows event references missing table, discarded",
		"schema", string(e.Table.Schema), "table", string(e.Table.Table))
	return nil
}

// OnPosSynced canal 内部位位同步回调：忽略（位点由 dispatcher 按投递 ack 推进）
func (h *binlogHandler) OnPosSynced(header *replication.EventHeader, pos mysql.Position, set mysql.GTIDSet, force bool) error {
	return nil
}

// handleTxOverflow 大事务保护
func (h *binlogHandler) handleTxOverflow() error {
	if h.onTxOverflow == "abort" {
		return fmt.Errorf("tx exceeds max_tx_rows=%d, aborting (on_tx_overflow=abort)", h.maxTxRows)
	}
	// 丢弃整个事务的已缓冲事件，等待该事务 XID 清零
	h.txEvents = nil
	h.log.Error("tx overflow, dropping tx events", "max_tx_rows", h.maxTxRows, "rows_so_far", h.txRows)
	return nil
}

// recoverPanic canal 回调 panic 防护：recover 并记日志（事件循环继续）
func (h *binlogHandler) recoverPanic(where string) {
	if r := recover(); r != nil {
		h.log.Error("panic in handler recovered", "where", where, "panic", r)
	}
}

// rowsAction canal action 常量转模型常量
func rowsAction(e *canal.RowsEvent) string {
	switch e.Action {
	case canal.InsertAction:
		return model.ActionInsert
	case canal.UpdateAction:
		return model.ActionUpdate
	case canal.DeleteAction:
		return model.ActionDelete
	}
	return strings.ToLower(e.Action)
}

// truncate 截断长字符串
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

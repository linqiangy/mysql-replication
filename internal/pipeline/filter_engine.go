// Package pipeline 实现 binlog 事件的处理链路：过滤引擎、dispatcher、
// PipelineManager。每个数据源一个独立 pipeline（canal 实例 + handler）。
package pipeline

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/go-mysql-org/go-mysql/canal"
	"github.com/go-mysql-org/go-mysql/schema"

	"bincat/internal/meta"
	"bincat/internal/model"
)

// FilterEngine 规则过滤引擎：只读快照 + atomic 换载实现热更新。
// 三级过滤：表（schema 精确 + table 通配）→ 操作 → 字段（update 时 before/after 对比）。
type FilterEngine struct {
	rules atomic.Pointer[[]*compiledRule]
}

// compiledRule 预编译规则（并发安全只读）
type compiledRule struct {
	rule        *meta.Rule
	sinkName    string
	tableRE     *regexp.Regexp
	actions     map[string]bool
	watchFields map[string]struct{}
}

// NewFilterEngine 按规则列表编译引擎
func NewFilterEngine(rules []*meta.Rule, sinks map[int64]*meta.Sink) (*FilterEngine, error) {
	compiled, err := compileRules(rules, sinks)
	if err != nil {
		return nil, err
	}
	e := &FilterEngine{}
	e.rules.Store(&compiled)
	return e, nil
}

// Reload 热更新：重新编译并原子换载；失败保留旧规则
func (e *FilterEngine) Reload(rules []*meta.Rule, sinks map[int64]*meta.Sink) error {
	compiled, err := compileRules(rules, sinks)
	if err != nil {
		return err
	}
	e.rules.Store(&compiled)
	return nil
}

func compileRules(rules []*meta.Rule, sinks map[int64]*meta.Sink) ([]*compiledRule, error) {
	out := make([]*compiledRule, 0, len(rules))
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		sink := sinks[r.SinkID]
		if sink == nil || !sink.Enabled {
			return nil, fmt.Errorf("rule %q references missing or disabled sink id=%d", r.Name, r.SinkID)
		}
		re, err := globToRegexp(r.TablePattern)
		if err != nil {
			return nil, fmt.Errorf("rule %q table pattern %q: %w", r.Name, r.TablePattern, err)
		}
		actions := make(map[string]bool, len(r.Actions))
		for _, a := range r.Actions {
			actions[strings.ToLower(strings.TrimSpace(a))] = true
		}
		if len(actions) == 0 {
			actions = map[string]bool{model.ActionInsert: true, model.ActionUpdate: true, model.ActionDelete: true}
		}
		watch := make(map[string]struct{}, len(r.WatchFields))
		for _, f := range r.WatchFields {
			watch[strings.ToLower(f)] = struct{}{}
		}
		out = append(out, &compiledRule{
			rule: r, sinkName: sink.Name, tableRE: re, actions: actions, watchFields: watch,
		})
	}
	return out, nil
}

// globToRegexp 表名通配转正则：* → [^.]*，? → [^.]
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString(`^`)
	for _, ch := range pattern {
		switch ch {
		case '*':
			b.WriteString(`[^.]*`)
		case '?':
			b.WriteString(`[^.]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString(`$`)
	return regexp.Compile(b.String())
}

// MatchResult 单行事件的过滤结果
type MatchResult struct {
	// Rule 命中的规则
	Rule *meta.Rule
	// SinkName 目标 sink 名称
	SinkName string
	// Changed watch_fields 中实际变化的字段（仅 update 且配置了 watch_fields 时非空）
	Changed []string
}

// Match 对一个 RowsEvent 做三级过滤；返回命中的规则（同表多规则可返回多条），未命中返回 nil
// 事件由调用方（handler）转换为列名→值 map 后传入；action 为 insert/update/delete。
func (e *FilterEngine) Match(schemaName, table, action string, before, after map[string]any) []MatchResult {
	rules := *e.rules.Load()
	var out []MatchResult
	for _, cr := range rules {
		// 1) 表级：schema 精确 + table 通配
		if !strings.EqualFold(cr.rule.SchemaName, schemaName) || !cr.tableRE.MatchString(table) {
			continue
		}
		// 2) 操作级
		if !cr.actions[action] {
			continue
		}
		// 3) 字段级（仅 update 且配置了 watch_fields）
		var changed []string
		if action == model.ActionUpdate && len(cr.watchFields) > 0 {
			changed = diffWatchFields(cr.watchFields, before, after)
			if len(changed) == 0 {
				continue // watch 字段未变化，不触发
			}
		}
		out = append(out, MatchResult{Rule: cr.rule, SinkName: cr.sinkName, Changed: changed})
	}
	return out
}

// diffWatchFields 对比 before/after 中被观察字段，返回发生变化的字段名
func diffWatchFields(watch map[string]struct{}, before, after map[string]any) []string {
	var changed []string
	for f := range watch {
		bv, bok := before[f]
		av, aok := after[f]
		if bok != aok || !valueEqual(bv, av) {
			changed = append(changed, f)
		}
	}
	return changed
}

// valueEqual 类型归一化对比（decimal/时间/字节串在 handler 已归一化为 JSON 兼容值）
func valueEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	as, ok1 := a.(string)
	bs, ok2 := b.(string)
	if ok1 && ok2 {
		return as == bs
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// RowsToMap 将 canal RowsEvent 的一行数据转为 列名→值 map（列数不齐按 min(len) 防御）
func RowsToMap(table *schema.Table, row []any) map[string]any {
	if table == nil || len(row) == 0 {
		return nil
	}
	n := len(table.Columns)
	if len(row) < n {
		n = len(row)
	}
	m := make(map[string]any, n)
	for i := 0; i < n; i++ {
		m[table.Columns[i].Name] = normalizeValue(row[i])
	}
	return m
}

// normalizeValue 归一化为 JSON 友好类型（[]byte → string，其余保持原样）
func normalizeValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return v
	}
}

// ensure canal import used（RowsToMap 签名引用 schema.Table）
var _ = canal.InsertAction

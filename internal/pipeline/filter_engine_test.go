package pipeline

import (
	"testing"

	"bincat/internal/meta"
)

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		pattern string
		input   string
		want    bool
	}{
		{"orders_*", "orders_2026", true},
		{"orders_*", "orders_", true},
		{"orders_*", "orders", false},
		{"orders_*", "other", false},
		{"user", "user", true},
		{"user", "users", false},
		{"t_?", "t_1", true},
		{"t_?", "t_12", false},
		{"order%", "order%", true}, // % 非通配，字面量
	}
	for _, c := range cases {
		re, err := globToRegexp(c.pattern)
		if err != nil {
			t.Fatalf("globToRegexp(%q): %v", c.pattern, err)
		}
		if got := re.MatchString(c.input); got != c.want {
			t.Errorf("glob %q match %q = %v, want %v", c.pattern, c.input, got, c.want)
		}
	}
}

func TestFilterEngineMatch(t *testing.T) {
	rules := []*meta.Rule{
		{ID: 1, DatasourceID: 1, Name: "orders-status", SchemaName: "shop", TablePattern: "orders_*",
			Actions: []string{"update"}, WatchFields: []string{"status", "pay_time"}, SinkID: 10, OwnerID: 1, Enabled: true},
		{ID: 2, DatasourceID: 1, Name: "user-all", SchemaName: "shop", TablePattern: "user",
			Actions: []string{"insert", "delete"}, SinkID: 11, OwnerID: 2, Enabled: true},
		{ID: 3, DatasourceID: 1, Name: "disabled-rule", SchemaName: "shop", TablePattern: "x",
			Actions: []string{"insert"}, SinkID: 11, OwnerID: 2, Enabled: false},
	}
	sinks := map[int64]*meta.Sink{
		10: {ID: 10, Name: "kafka-orders", Type: "kafka", Enabled: true},
		11: {ID: 11, Name: "debug", Type: "console", Enabled: true},
	}
	e, err := NewFilterEngine(rules, sinks)
	if err != nil {
		t.Fatal(err)
	}

	// 1) 表级 + 操作级 + 字段级：watch 字段变化 → 命中
	got := e.Match("shop", "orders_2026", "update",
		map[string]any{"status": "created", "pay_time": nil, "remark": "a"},
		map[string]any{"status": "paid", "pay_time": nil, "remark": "a"})
	if len(got) != 1 || got[0].Rule.Name != "orders-status" {
		t.Fatalf("update status change: got %+v", got)
	}

	// 2) watch 字段未变（只改 remark）→ 不触发
	got = e.Match("shop", "orders_2026", "update",
		map[string]any{"status": "paid", "remark": "a"},
		map[string]any{"status": "paid", "remark": "b"})
	if len(got) != 0 {
		t.Fatalf("non-watch field change should not trigger, got %+v", got)
	}

	// 3) insert 不在 orders 规则 actions → 不触发
	got = e.Match("shop", "orders_2026", "insert", nil, map[string]any{"status": "x"})
	if len(got) != 0 {
		t.Fatalf("insert on orders should not trigger, got %+v", got)
	}

	// 4) user 表 insert → 命中 user-all
	got = e.Match("shop", "user", "insert", nil, map[string]any{"id": 1})
	if len(got) != 1 || got[0].SinkName != "debug" {
		t.Fatalf("user insert: got %+v", got)
	}

	// 5) user 表 update → 不触发（actions 无 update）
	got = e.Match("shop", "user", "update", map[string]any{"id": 1}, map[string]any{"id": 2})
	if len(got) != 0 {
		t.Fatalf("user update should not trigger, got %+v", got)
	}

	// 6) 其他 schema → 不触发
	got = e.Match("other_db", "user", "insert", nil, nil)
	if len(got) != 0 {
		t.Fatalf("other schema should not trigger, got %+v", got)
	}
}

func TestDiffWatchFields(t *testing.T) {
	watch := map[string]struct{}{"status": {}, "pay_time": {}}
	changed := diffWatchFields(watch,
		map[string]any{"status": "a", "pay_time": nil},
		map[string]any{"status": "b", "pay_time": nil})
	if len(changed) != 1 || changed[0] != "status" {
		t.Fatalf("changed = %v", changed)
	}
	// 字段缺失 vs 存在也算变化
	changed = diffWatchFields(watch,
		map[string]any{"status": "a"},
		map[string]any{"status": "a", "pay_time": "2026-01-01"})
	if len(changed) != 1 || changed[0] != "pay_time" {
		t.Fatalf("changed = %v", changed)
	}
}

func TestEngineReload(t *testing.T) {
	rules := []*meta.Rule{
		{ID: 1, SchemaName: "shop", TablePattern: "t1", Actions: []string{"insert"}, SinkID: 10, Enabled: true},
	}
	sinks := map[int64]*meta.Sink{10: {ID: 10, Name: "debug", Enabled: true}}
	e, _ := NewFilterEngine(rules, sinks)
	if got := e.Match("shop", "t1", "insert", nil, nil); len(got) != 1 {
		t.Fatal("before reload should match")
	}
	// 热更新：换成 t2
	newRules := []*meta.Rule{
		{ID: 2, SchemaName: "shop", TablePattern: "t2", Actions: []string{"insert"}, SinkID: 10, Enabled: true},
	}
	if err := e.Reload(newRules, sinks); err != nil {
		t.Fatal(err)
	}
	if got := e.Match("shop", "t1", "insert", nil, nil); len(got) != 0 {
		t.Fatal("after reload t1 should not match")
	}
	if got := e.Match("shop", "t2", "insert", nil, nil); len(got) != 1 {
		t.Fatal("after reload t2 should match")
	}
}

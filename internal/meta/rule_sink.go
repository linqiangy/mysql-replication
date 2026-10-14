package meta

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Sink 投递目标（admin 在后台配置；params 为各类型专属参数 JSON）
type Sink struct {
	ID      int64
	Name    string
	Type    string // console / http / kafka / rabbitmq
	Params  map[string]any
	Enabled bool
}

// CreateSink 新增 sink
func (s *Store) CreateSink(ctx context.Context, sk *Sink) (int64, error) {
	params, err := json.Marshal(sk.Params)
	if err != nil {
		return 0, fmt.Errorf("marshal sink params: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sink (name, type, params, enabled) VALUES (?, ?, ?, ?)`,
		sk.Name, sk.Type, params, sk.Enabled)
	if err != nil {
		return 0, fmt.Errorf("insert sink: %w", err)
	}
	return res.LastInsertId()
}

// UpdateSink 更新 sink
func (s *Store) UpdateSink(ctx context.Context, sk *Sink) error {
	params, err := json.Marshal(sk.Params)
	if err != nil {
		return fmt.Errorf("marshal sink params: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE sink SET name=?, type=?, params=?, enabled=? WHERE id=?`,
		sk.Name, sk.Type, params, sk.Enabled, sk.ID); err != nil {
		return fmt.Errorf("update sink: %w", err)
	}
	return nil
}

// DeleteSink 删除 sink（关联规则由上层先校验）
func (s *Store) DeleteSink(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sink WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete sink: %w", err)
	}
	return nil
}

// GetSink 按 ID 查 sink
func (s *Store) GetSink(ctx context.Context, id int64) (*Sink, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, type, params, enabled FROM sink WHERE id=?`, id)
	return scanSink(row)
}

// GetSinkByName 按名称查 sink
func (s *Store) GetSinkByName(ctx context.Context, name string) (*Sink, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, type, params, enabled FROM sink WHERE name=?`, name)
	return scanSink(row)
}

// ListSinks 列出全部 sink
func (s *Store) ListSinks(ctx context.Context) ([]*Sink, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, type, params, enabled FROM sink ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list sinks: %w", err)
	}
	defer rows.Close()
	var out []*Sink
	for rows.Next() {
		sk, err := scanSink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

func scanSink(r row) (*Sink, error) {
	sk := &Sink{}
	var params []byte
	if err := r.Scan(&sk.ID, &sk.Name, &sk.Type, &params, &sk.Enabled); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("scan sink: %w", err)
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &sk.Params); err != nil {
			return nil, fmt.Errorf("unmarshal sink params: %w", err)
		}
	}
	return sk, nil
}

// Rule 监听规则（普通用户创建，按 owner 隔离）
type Rule struct {
	ID           int64
	DatasourceID int64
	Name         string
	SchemaName   string
	TablePattern string
	Actions      []string // insert/update/delete 子集
	WatchFields  []string // 空 = 任意变更触发
	SinkID       int64
	OwnerID      int64
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateRule 新增规则
func (s *Store) CreateRule(ctx context.Context, r *Rule) (int64, error) {
	actions := strings.Join(r.Actions, ",")
	watch, err := json.Marshal(r.WatchFields)
	if err != nil {
		return 0, fmt.Errorf("marshal watch fields: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO rule (datasource_id, name, schema_name, table_pattern, actions, watch_fields, sink_id, owner_id, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.DatasourceID, r.Name, r.SchemaName, r.TablePattern, actions, watch, r.SinkID, r.OwnerID, r.Enabled)
	if err != nil {
		return 0, fmt.Errorf("insert rule: %w", err)
	}
	return res.LastInsertId()
}

// UpdateRule 更新规则
func (s *Store) UpdateRule(ctx context.Context, r *Rule) error {
	actions := strings.Join(r.Actions, ",")
	watch, err := json.Marshal(r.WatchFields)
	if err != nil {
		return fmt.Errorf("marshal watch fields: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE rule SET datasource_id=?, name=?, schema_name=?, table_pattern=?, actions=?, watch_fields=?, sink_id=?, owner_id=?, enabled=? WHERE id=?`,
		r.DatasourceID, r.Name, r.SchemaName, r.TablePattern, actions, watch, r.SinkID, r.OwnerID, r.Enabled, r.ID); err != nil {
		return fmt.Errorf("update rule: %w", err)
	}
	return nil
}

// DeleteRule 删除规则
func (s *Store) DeleteRule(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM rule WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	return nil
}

// GetRule 按 ID 查规则
func (s *Store) GetRule(ctx context.Context, id int64) (*Rule, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, datasource_id, name, schema_name, table_pattern, actions, watch_fields, sink_id, owner_id, enabled, created_at, updated_at
		 FROM rule WHERE id=?`, id)
	return scanRule(row)
}

// ListRules 列出规则；ownerID > 0 时仅返回该用户的（owner 隔离），datasourceID > 0 时再按源过滤
func (s *Store) ListRules(ctx context.Context, ownerID, datasourceID int64) ([]*Rule, error) {
	q := `SELECT id, datasource_id, name, schema_name, table_pattern, actions, watch_fields, sink_id, owner_id, enabled, created_at, updated_at FROM rule`
	var conds []string
	var args []any
	if ownerID > 0 {
		conds = append(conds, "owner_id=?")
		args = append(args, ownerID)
	}
	if datasourceID > 0 {
		conds = append(conds, "datasource_id=?")
		args = append(args, datasourceID)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY id"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()
	var out []*Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRule(r row) (*Rule, error) {
	rl := &Rule{}
	var actions string
	var watch []byte
	var watchNull sql.NullString
	if err := r.Scan(&rl.ID, &rl.DatasourceID, &rl.Name, &rl.SchemaName, &rl.TablePattern,
		&actions, &watchNull, &rl.SinkID, &rl.OwnerID, &rl.Enabled, &rl.CreatedAt, &rl.UpdatedAt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("scan rule: %w", err)
	}
	rl.Actions = strings.Split(actions, ",")
	if watchNull.Valid {
		watch = []byte(watchNull.String)
		if len(watch) > 0 && string(watch) != "null" {
			if err := json.Unmarshal(watch, &rl.WatchFields); err != nil {
				return nil, fmt.Errorf("unmarshal watch fields: %w", err)
			}
		}
	}
	return rl, nil
}

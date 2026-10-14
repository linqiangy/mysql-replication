package meta

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DataSource 被监听的 MySQL 数据源（admin 在后台配置）
type DataSource struct {
	ID        int64
	Name      string
	Host      string
	Port      int
	User      string
	// PasswordEnc AES-GCM 加密后的密码（不回显给前端）
	PasswordEnc []byte
	// Password 解密后的明文密码（仅在启动 pipeline 时填充，不落库不回显）
	Password  string
	ServerID  uint32
	GTIDMode  bool
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateDataSource 新增数据源
func (s *Store) CreateDataSource(ctx context.Context, ds *DataSource) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO datasource (name, host, port, user, password, server_id, gtid_mode, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ds.Name, ds.Host, ds.Port, ds.User, ds.PasswordEnc, ds.ServerID, ds.GTIDMode, ds.Enabled)
	if err != nil {
		return 0, fmt.Errorf("insert datasource: %w", err)
	}
	return res.LastInsertId()
}

// UpdateDataSource 更新数据源（password 为 nil 表示不修改密码）
func (s *Store) UpdateDataSource(ctx context.Context, ds *DataSource) error {
	var err error
	if ds.PasswordEnc == nil {
		_, err = s.db.ExecContext(ctx,
			`UPDATE datasource SET name=?, host=?, port=?, user=?, server_id=?, gtid_mode=?, enabled=? WHERE id=?`,
			ds.Name, ds.Host, ds.Port, ds.User, ds.ServerID, ds.GTIDMode, ds.Enabled, ds.ID)
	} else {
		_, err = s.db.ExecContext(ctx,
			`UPDATE datasource SET name=?, host=?, port=?, user=?, password=?, server_id=?, gtid_mode=?, enabled=? WHERE id=?`,
			ds.Name, ds.Host, ds.Port, ds.User, ds.PasswordEnc, ds.ServerID, ds.GTIDMode, ds.Enabled, ds.ID)
	}
	if err != nil {
		return fmt.Errorf("update datasource: %w", err)
	}
	return nil
}

// DeleteDataSource 删除数据源（关联规则由上层先校验/级联删除）
func (s *Store) DeleteDataSource(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM datasource WHERE id=?`, id); err != nil {
		return fmt.Errorf("delete datasource: %w", err)
	}
	return nil
}

// GetDataSource 按 ID 查数据源
func (s *Store) GetDataSource(ctx context.Context, id int64) (*DataSource, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, host, port, user, password, server_id, gtid_mode, enabled, created_at, updated_at
		 FROM datasource WHERE id=?`, id)
	return scanDataSource(row)
}

// GetDataSourceByName 按名称查数据源
func (s *Store) GetDataSourceByName(ctx context.Context, name string) (*DataSource, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, host, port, user, password, server_id, gtid_mode, enabled, created_at, updated_at
		 FROM datasource WHERE name=?`, name)
	return scanDataSource(row)
}

// ListDataSources 列出全部数据源
func (s *Store) ListDataSources(ctx context.Context) ([]*DataSource, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, host, port, user, password, server_id, gtid_mode, enabled, created_at, updated_at
		 FROM datasource ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list datasources: %w", err)
	}
	defer rows.Close()
	var out []*DataSource
	for rows.Next() {
		ds, err := scanDataSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ds)
	}
	return out, rows.Err()
}

func scanDataSource(r row) (*DataSource, error) {
	ds := &DataSource{}
	err := r.Scan(&ds.ID, &ds.Name, &ds.Host, &ds.Port, &ds.User, &ds.PasswordEnc,
		&ds.ServerID, &ds.GTIDMode, &ds.Enabled, &ds.CreatedAt, &ds.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan datasource: %w", err)
	}
	return ds, nil
}

// row 统一 *sql.Row 与 *sql.Rows 的 Scan
type row interface{ Scan(dest ...any) error }

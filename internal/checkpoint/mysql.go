// Package checkpoint 负责 binlog 位点的持久化与恢复。
// 位点存元数据库 checkpoint 表（每数据源一行），由 dispatcher 在批次全部
// ack 后推进、checkpoint worker 定时落盘，实现 at-least-once。
package checkpoint

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Position binlog 位点（文件位点 + GTID set，恢复时 GTID 优先）
type Position struct {
	// BinlogFile binlog 文件名，如 "mysql-bin.000123"
	BinlogFile string
	// BinlogPos 文件内偏移
	BinlogPos uint32
	// GTIDSet GTID 集合字符串（gtid_mode 数据源使用）
	GTIDSet string
	// UpdatedAt 落盘时间
	UpdatedAt time.Time
}

// Store 位点存储接口
type Store interface {
	// Load 读取数据源位点；无记录返回 (nil, nil)
	Load(ctx context.Context, datasourceID int64) (*Position, error)
	// Save 幂等覆盖保存位点
	Save(ctx context.Context, datasourceID int64, p Position) error
	// Reset 删除位点记录（重置=下次启动从当前 master 位点开始）
	Reset(ctx context.Context, datasourceID int64) error
}

// MySQLStore 元数据库 checkpoint 表实现
type MySQLStore struct {
	db *sql.DB
}

// NewMySQLStore 构造（表已在 meta.migrate 中创建）
func NewMySQLStore(db *sql.DB) *MySQLStore { return &MySQLStore{db: db} }

// Load 实现 Store
func (s *MySQLStore) Load(ctx context.Context, datasourceID int64) (*Position, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT binlog_file, binlog_pos, gtid_set, updated_at FROM checkpoint WHERE datasource_id=?`, datasourceID)
	p := &Position{}
	var gtid sql.NullString
	if err := row.Scan(&p.BinlogFile, &p.BinlogPos, &gtid, &p.UpdatedAt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("load checkpoint: %w", err)
	}
	p.GTIDSet = gtid.String
	return p, nil
}

// Save 实现 Store（UPSERT）
func (s *MySQLStore) Save(ctx context.Context, datasourceID int64, p Position) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO checkpoint (datasource_id, binlog_file, binlog_pos, gtid_set) VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE binlog_file=VALUES(binlog_file), binlog_pos=VALUES(binlog_pos), gtid_set=VALUES(gtid_set)`,
		datasourceID, p.BinlogFile, p.BinlogPos, nullable(p.GTIDSet))
	if err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}
	return nil
}

// Reset 实现 Store
func (s *MySQLStore) Reset(ctx context.Context, datasourceID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM checkpoint WHERE datasource_id=?`, datasourceID); err != nil {
		return fmt.Errorf("reset checkpoint: %w", err)
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

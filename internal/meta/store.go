// Package meta 封装 bincat 元数据库访问：数据源、规则、sink、位点、用户、审计的
// 表结构迁移与 CRUD。元数据库独立于被监听的 MySQL 数据源。
package meta

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Store 元数据库访问入口
type Store struct {
	db *sql.DB
}

// Open 打开元数据库并执行表结构迁移（幂等）。DSN 需带 parseTime=true 以支持时间列扫描。
func Open(dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("meta dsn is empty")
	}
	// 自动补齐常用参数（用户 DSN 未带时）
	if !strings.Contains(dsn, "parseTime") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + "parseTime=true&loc=Local&charset=utf8mb4"
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open meta db: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping meta db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate meta db: %w", err)
	}
	return s, nil
}

// Close 关闭元数据库连接池
func (s *Store) Close() error { return s.db.Close() }

// DB 暴露底层 *sql.DB（仅限内部模块使用，如 checkpoint 事务写入）
func (s *Store) DB() *sql.DB { return s.db }

// migrate 建表（IF NOT EXISTS，幂等）。版本演进采用追加列 + migration 表记录，M1 仅建表。
func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS datasource (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(128) NOT NULL UNIQUE,
			host VARCHAR(255) NOT NULL,
			port SMALLINT UNSIGNED NOT NULL DEFAULT 3306,
			user VARCHAR(128) NOT NULL,
			password VARBINARY(1024) NOT NULL COMMENT 'AES-GCM encrypted',
			server_id INT UNSIGNED NOT NULL,
			gtid_mode TINYINT(1) NOT NULL DEFAULT 1,
			enabled TINYINT(1) NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS sink (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(128) NOT NULL UNIQUE,
			type VARCHAR(32) NOT NULL COMMENT 'console/http/kafka/rabbitmq',
			params JSON NOT NULL,
			enabled TINYINT(1) NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS rule (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			datasource_id BIGINT UNSIGNED NOT NULL,
			name VARCHAR(128) NOT NULL,
			schema_name VARCHAR(128) NOT NULL,
			table_pattern VARCHAR(255) NOT NULL COMMENT 'exact name or glob like orders_*',
			actions VARCHAR(64) NOT NULL DEFAULT 'insert,update,delete',
			watch_fields JSON NULL COMMENT 'JSON array; empty = all changes trigger',
			sink_id BIGINT UNSIGNED NOT NULL,
			owner_id BIGINT UNSIGNED NOT NULL,
			enabled TINYINT(1) NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uk_datasource_name (datasource_id, name),
			KEY idx_owner (owner_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS checkpoint (
			datasource_id BIGINT UNSIGNED PRIMARY KEY,
			binlog_file VARCHAR(255) NOT NULL,
			binlog_pos INT UNSIGNED NOT NULL,
			gtid_set TEXT NULL,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS admin_user (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(64) NOT NULL UNIQUE,
			password_hash VARCHAR(255) NOT NULL COMMENT 'bcrypt',
			role VARCHAR(16) NOT NULL DEFAULT 'user' COMMENT 'admin/user',
			enabled TINYINT(1) NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
			user_id BIGINT UNSIGNED NOT NULL,
			action VARCHAR(64) NOT NULL COMMENT 'create/update/delete/reset',
			resource VARCHAR(64) NOT NULL COMMENT 'datasource/rule/sink/user/checkpoint',
			resource_id VARCHAR(64) NOT NULL,
			detail JSON NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", firstN(stmt, 60), err)
		}
	}
	return nil
}

// firstN 截断字符串用于日志
func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

package meta

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// User 后台用户（admin 创建；role: admin/user）
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateUser 新增用户
func (s *Store) CreateUser(ctx context.Context, u *User) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_user (username, password_hash, role, enabled) VALUES (?, ?, ?, ?)`,
		u.Username, u.PasswordHash, u.Role, u.Enabled)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	return res.LastInsertId()
}

// GetUser 按用户名查用户（登录用）
func (s *Store) GetUser(ctx context.Context, username string) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, enabled, created_at, updated_at FROM admin_user WHERE username=?`, username)
	return scanUser(row)
}

// GetUserByID 按 ID 查用户
func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, enabled, created_at, updated_at FROM admin_user WHERE id=?`, id)
	return scanUser(row)
}

// ListUsers 列出全部用户
func (s *Store) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, password_hash, role, enabled, created_at, updated_at FROM admin_user ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdatePassword 更新用户密码哈希
func (s *Store) UpdatePassword(ctx context.Context, userID int64, hash string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE admin_user SET password_hash=? WHERE id=?`, hash, userID); err != nil {
		return fmt.Errorf("update user password: %w", err)
	}
	return nil
}

// SetUserEnabled 启用/禁用用户
func (s *Store) SetUserEnabled(ctx context.Context, userID int64, enabled bool) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE admin_user SET enabled=? WHERE id=?`, enabled, userID); err != nil {
		return fmt.Errorf("set user enabled: %w", err)
	}
	return nil
}

func scanUser(r row) (*User, error) {
	u := &User{}
	if err := r.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.CreatedAt, &u.UpdatedAt); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return u, nil
}

// Audit 审计日志（规则/sink/数据源/用户/位点变更均记录）
func (s *Store) Audit(ctx context.Context, userID int64, action, resource, resourceID string, detail []byte) error {
	var detailArg any
	if len(detail) > 0 {
		detailArg = detail
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_log (user_id, action, resource, resource_id, detail) VALUES (?, ?, ?, ?, ?)`,
		userID, action, resource, resourceID, detailArg); err != nil {
		// 审计失败不阻断业务，记日志由调用方处理
		return fmt.Errorf("insert audit: %w", err)
	}
	return nil
}

// ListAudit 分页列出审计日志（新→旧）
func (s *Store) ListAudit(ctx context.Context, limit, offset int) ([]map[string]any, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, action, resource, resource_id, detail, created_at FROM audit_log ORDER BY id DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list audit: %w", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, userID int64
		var action, resource, resourceID string
		var detail sql.NullString
		var createdAt time.Time
		if err := rows.Scan(&id, &userID, &action, &resource, &resourceID, &detail, &createdAt); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		out = append(out, map[string]any{
			"id": id, "user_id": userID, "action": action, "resource": resource,
			"resource_id": resourceID, "detail": detail.String, "created_at": createdAt,
		})
	}
	return out, rows.Err()
}

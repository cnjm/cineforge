package repository

// users 表仓储：auth / user 管理共用的数据访问层。
// 单库 monolith 中 users 是唯一 master identity（无 sys_user 投影）。

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/model"
)

// ErrPhoneExists 手机号唯一冲突（不区分 username/phone，契约 409 "手机号已存在"）。
var ErrPhoneExists = errors.New("phone already exists")

// Users 基于共享连接池的 users 仓储。
type Users struct {
	pool *pgxpool.Pool
}

// NewUsers 创建 users 仓储。
func NewUsers(pool *pgxpool.Pool) *Users { return &Users{pool: pool} }

const (
	userColumns = `id, username, phone, name, display_name, role, password_hash, is_active, sync_version, created_at, updated_at`

	userSelectByPhone = `SELECT ` + userColumns + ` FROM users WHERE phone = $1`
	userSelectByID    = `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	userSelectAll     = `SELECT ` + userColumns + ` FROM users ORDER BY created_at`
)

func scanUser(row pgx.Row) (*model.Users, error) {
	var u model.Users
	err := row.Scan(&u.ID, &u.Username, &u.Phone, &u.Name, &u.DisplayName,
		&u.Role, &u.PasswordHash, &u.IsActive, &u.SyncVersion, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// FindByPhone 按 phone 精确查询；不存在返回 (nil, nil)。
func (r *Users) FindByPhone(ctx context.Context, phone string) (*model.Users, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, userSelectByPhone, phone))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by phone: %w", err)
	}
	return u, nil
}

// FindByID 按主键查询；不存在返回 (nil, nil)。
func (r *Users) FindByID(ctx context.Context, id string) (*model.Users, error) {
	u, err := scanUser(r.pool.QueryRow(ctx, userSelectByID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return u, nil
}

// List 返回全部用户（按 created_at 升序，对齐跨 legacy list_users）。
func (r *Users) List(ctx context.Context) ([]model.Users, error) {
	rows, err := r.pool.Query(ctx, userSelectAll)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var out []model.Users
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user row: %w", err)
		}
		out = append(out, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return out, nil
}

// Create 插入用户；手机号/用户名唯一冲突返回 ErrPhoneExists。
func (r *Users) Create(ctx context.Context, u *model.Users) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO users (id, username, phone, name, display_name, role, password_hash, is_active, sync_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		u.ID, u.Username, u.Phone, u.Name, u.DisplayName, u.Role, u.PasswordHash, u.IsActive, u.SyncVersion)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrPhoneExists
		}
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// SetActive 更新启用状态；影响 0 行说明用户不存在。
func (r *Users) SetActive(ctx context.Context, id string, active bool) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET is_active = $2, updated_at = now() WHERE id = $1`, id, active)
	if err != nil {
		return false, fmt.Errorf("set user active: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetPasswordHash 更新密码哈希；影响 0 行说明用户不存在。
func (r *Users) SetPasswordHash(ctx context.Context, id, hash string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	if err != nil {
		return false, fmt.Errorf("set password hash: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

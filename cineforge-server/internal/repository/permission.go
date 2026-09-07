package repository

// permissions 表仓储：项目级细粒度角色（project_role:{owner|manager|lead|member|viewer}）。
// 与 casbin 粗粒度 capability 并存：项目路由先过角色判定，再按需查 casbin。

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProjectRolePrefix 是 permission_type 的角色前缀（对齐 legacy PROJECT_ROLE_PREFIX）。
const ProjectRolePrefix = "project_role:"

// Permissions 基于共享连接池的 permissions 仓储。
type Permissions struct {
	pool *pgxpool.Pool
}

// NewPermissions 创建 permissions 仓储。
func NewPermissions(pool *pgxpool.Pool) *Permissions { return &Permissions{pool: pool} }

// FindProjectRole 返回 user 在 project 的项目角色；未绑定返回 ""。
func (r *Permissions) FindProjectRole(ctx context.Context, userID, projectID string) (string, error) {
	var permissionType string
	err := r.pool.QueryRow(ctx,
		`SELECT permission_type FROM permissions
		 WHERE user_id = $1 AND project_id = $2 AND permission_type LIKE '`+ProjectRolePrefix+`%'
		 ORDER BY created_at LIMIT 1`,
		userID, projectID).Scan(&permissionType)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find project role: %w", err)
	}
	return strings.TrimPrefix(permissionType, ProjectRolePrefix), nil
}

// UpsertRole 写入/覆盖用户的项目角色（幂等：同 user+project+type 更新）。
// permissionType 形如 "project_role:owner"。
func (r *Permissions) UpsertRole(ctx context.Context, userID, projectID, permissionType string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO permissions (user_id, project_id, permission_type)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, project_id, permission_type)
		 DO UPDATE SET updated_at = now()`,
		userID, projectID, permissionType)
	if err != nil {
		return fmt.Errorf("upsert project role: %w", err)
	}
	return nil
}

// RemoveRole 移除用户的项目角色；不存在时不报错。
func (r *Permissions) RemoveRole(ctx context.Context, userID, projectID, permissionType string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM permissions WHERE user_id = $1 AND project_id = $2 AND permission_type = $3`,
		userID, projectID, permissionType)
	if err != nil {
		return fmt.Errorf("remove project role: %w", err)
	}
	return nil
}

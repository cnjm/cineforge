package repository

// P3g 通知读仓储（对齐 legacy repositories.py list_user_notifications /
// mark_user_notifications_read）。写路径（改派/审核定版通知）在任务事务内，
// 见 tasks_persist.go insertNotification（事务内插入）。

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NotifyView 对齐 NotificationRead（id/title/content/type/is_read/created_at）。
type NotifyView struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Type      string    `json:"type"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}

// Notifications 基于共享连接池的通知读仓储。
type Notifications struct {
	pool *pgxpool.Pool
}

// NewNotifications 创建通知仓储。
func NewNotifications(pool *pgxpool.Pool) *Notifications { return &Notifications{pool: pool} }

// ListUserNotifications 对齐 list_user_notifications：本人最近 100 条，倒序。
func (r *Notifications) ListUserNotifications(ctx context.Context, userID string) ([]NotifyView, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, title, content, type, is_read, created_at
		   FROM notifications WHERE user_id = $1
		  ORDER BY created_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user notifications: %w", err)
	}
	defer rows.Close()
	out := []NotifyView{}
	for rows.Next() {
		var n NotifyView
		if err := rows.Scan(&n.ID, &n.Title, &n.Content, &n.Type, &n.IsRead, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkUserNotificationsRead 对齐 mark_user_notifications_read：本人全部未读置已读，返回更新行数。
func (r *Notifications) MarkUserNotificationsRead(ctx context.Context, userID string) (int, error) {
	ct, err := r.pool.Exec(ctx,
		`UPDATE notifications SET is_read = true, updated_at = now()
		  WHERE user_id = $1 AND is_read = false`, userID)
	if err != nil {
		return 0, fmt.Errorf("mark user notifications read: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

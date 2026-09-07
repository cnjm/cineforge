package service

// P3g 通知读服务（对齐 legacy app/api/routes/tasks.py get_workspace_notifications /
// read_all_workspace_notifications）。

import (
	"context"

	"cineforge/server/internal/repository"
)

// NotifyService 通知域读服务（写路径属于任务事务，任务域服务负责）。
type NotifyService struct {
	notify *repository.Notifications
}

// NewNotifyService 创建通知服务。
func NewNotifyService(notify *repository.Notifications) *NotifyService {
	return &NotifyService{notify: notify}
}

// List 对齐 get_workspace_notifications：本人最近 100 条通知。
func (s *NotifyService) List(ctx context.Context, userID string) ([]repository.NotifyView, error) {
	return s.notify.ListUserNotifications(ctx, userID)
}

// MarkAllRead 对齐 read_all_workspace_notifications：本人全部未读置已读，返回更新行数。
func (s *NotifyService) MarkAllRead(ctx context.Context, userID string) (int, error) {
	return s.notify.MarkUserNotificationsRead(ctx, userID)
}

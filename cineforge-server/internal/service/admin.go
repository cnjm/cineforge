package service

// P3g admin 看板服务（对齐 legacy app/api/routes/admin.py get_admin_overview /
// get_admin_probe）。纯读聚合，不做事务编排。

import (
	"context"
	"time"

	"cineforge/server/internal/repository"
)

// AdminOverview 对齐 get_admin_overview 响应。
type AdminOverview struct {
	Stats            map[string]int                 `json:"stats"`
	OverdueTasks     []repository.TaskView          `json:"overdue_tasks"`
	RecentActivities []string                       `json:"recent_activities"`
	RecentReadings   []repository.RecentReadingView `json:"recent_readings"`
}

// AdminProbe 对齐 get_admin_probe 响应。
type AdminProbe struct {
	Status              string                         `json:"status"`
	GeneratedAt         string                         `json:"generated_at"`
	Counts              map[string]int                 `json:"counts"`
	RecentProjects      []repository.ProbeProject      `json:"recent_projects"`
	RecentAgentRuns     []repository.ProbeAgentRun     `json:"recent_agent_runs"`
	RecentOperationLogs []repository.ProbeOperationLog `json:"recent_operation_logs"`
}

// AdminService admin 看板域服务。
type AdminService struct {
	admin *repository.Admin
}

// NewAdminService 创建 admin 看板服务。
func NewAdminService(admin *repository.Admin) *AdminService { return &AdminService{admin: admin} }

// Overview 对齐 get_admin_overview：stats 短键 + extended 键合并，overdue_tasks 取 Top5，
// recent_activities 为静态占位，recent_readings 为 script_reading 最近 3 条。
func (s *AdminService) Overview(ctx context.Context) (AdminOverview, error) {
	counts, err := s.admin.Counts(ctx)
	if err != nil {
		return AdminOverview{}, err
	}
	overdue, err := s.admin.ListOverdueTasks(ctx, time.Now().UTC(), 5)
	if err != nil {
		return AdminOverview{}, err
	}
	readings, err := s.admin.ListRecentReadings(ctx)
	if err != nil {
		return AdminOverview{}, err
	}
	stats := map[string]int{
		"users": counts.Users, "projects": counts.Projects, "tasks": counts.Tasks,
		"overdue": counts.Overdue, "assets": counts.Assets, "submissions": counts.Submissions,
		"agentRuns": counts.AgentRunsActive,
		// extended 键（前端 AdminOverview 类型的兼容别名）
		"user_count": counts.Users, "active_project_count": counts.Projects,
		"total_task_count": counts.Tasks, "overdue_task_count": counts.Overdue,
		"asset_count": counts.Assets, "submission_count": counts.Submissions,
		"agent_run_count": counts.AgentRunsActive,
	}
	return AdminOverview{
		Stats:            stats,
		OverdueTasks:     overdue,
		RecentActivities: []string{"管理概览已接入 PostgreSQL", "任务、资产、提交统计来自真实数据库"},
		RecentReadings:   readings,
	}, nil
}

// Probe 对齐 get_admin_probe：27 张表行数 + 最近项目/Agent run/操作日志。
func (s *AdminService) Probe(ctx context.Context) (AdminProbe, error) {
	counts, err := s.admin.ProbeCounts(ctx)
	if err != nil {
		return AdminProbe{}, err
	}
	projects, runs, logs, err := s.admin.ProbeRecent(ctx)
	if err != nil {
		return AdminProbe{}, err
	}
	return AdminProbe{
		Status:              "ok",
		GeneratedAt:         time.Now().UTC().Format(time.RFC3339),
		Counts:              counts,
		RecentProjects:      projects,
		RecentAgentRuns:     runs,
		RecentOperationLogs: logs,
	}, nil
}

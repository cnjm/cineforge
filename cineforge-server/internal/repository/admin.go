package repository

// P3g admin 看板仓储（对齐 legacy app/api/routes/admin.py get_admin_overview /
// get_admin_probe，数据源为真实 DB 聚合，不做任何模型/历史决策）。

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminCounts 对齐 get_admin_overview 的 stats 计数。
type AdminCounts struct {
	Users           int
	Projects        int
	Tasks           int
	Overdue         int
	Assets          int
	Submissions     int
	AgentRunsActive int // agentRuns：queued/running 行数（等价 legacy orchestrator.list_runs() 在途数）
}

// RecentReadingView 对齐 get_admin_overview.recent_readings 条目。
type RecentReadingView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Meta        string `json:"meta"`
	ProjectId   string `json:"projectId"`
	Title       string `json:"title"`
	Genre       string `json:"genre"`
	EpisodeCode string `json:"episodeCode"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

// ProbeProject 对齐 get_admin_probe.recent_projects 条目。
type ProbeProject struct {
	ID            string  `json:"id"`
	ProjectPrefix *string `json:"project_prefix"`
	Title         string  `json:"title"`
	Status        string  `json:"status"`
	CreatedAt     *string `json:"created_at"`
}

// ProbeAgentRun 对齐 get_admin_probe.recent_agent_runs 条目。
type ProbeAgentRun struct {
	ID           string  `json:"id"`
	ProjectId    *string `json:"project_id"`
	AgentType    string  `json:"agent_type"`
	Status       string  `json:"status"`
	ErrorMessage *string `json:"error_message"`
	CreatedAt    *string `json:"created_at"`
}

// ProbeOperationLog 对齐 get_admin_probe.recent_operation_logs 条目。
type ProbeOperationLog struct {
	ID         string  `json:"id"`
	ProjectId  *string `json:"project_id"`
	Action     string  `json:"action"`
	TargetType string  `json:"target_type"`
	CreatedAt  *string `json:"created_at"`
}

// Admin 基于共享连接池的 admin 看板仓储（复用 Tasks.buildTaskViews 渲染 overdue 任务）。
type Admin struct {
	pool  *pgxpool.Pool
	tasks *Tasks
}

// NewAdmin 创建 admin 看板仓储。
func NewAdmin(pool *pgxpool.Pool, tasks *Tasks) *Admin { return &Admin{pool: pool, tasks: tasks} }

// Counts 对齐 get_admin_overview 的 7 项 stats（真实 DB 计数）。
func (r *Admin) Counts(ctx context.Context) (AdminCounts, error) {
	c := AdminCounts{}
	scans := []struct {
		dest *int
		sql  string
	}{
		{&c.Users, `SELECT COUNT(*) FROM users`},
		{&c.Projects, `SELECT COUNT(*) FROM projects`},
		{&c.Tasks, `SELECT COUNT(*) FROM tasks WHERE is_retired = false`},
		{&c.Overdue, `SELECT COUNT(*) FROM tasks
			 WHERE is_retired = false AND status <> 'completed'
			   AND due_at IS NOT NULL AND due_at < now()`},
		{&c.Assets, `SELECT COUNT(*) FROM assets`},
		{&c.Submissions, `SELECT COUNT(*) FROM submissions`},
		{&c.AgentRunsActive, `SELECT COUNT(*) FROM agent_runs WHERE status IN ('queued','running')`},
	}
	for _, s := range scans {
		if err := r.pool.QueryRow(ctx, s.sql).Scan(s.dest); err != nil {
			return c, fmt.Errorf("admin counts: %w", err)
		}
	}
	return c, nil
}

// ListOverdueTasks 对齐 get_admin_overview 的 overdue_rows（top N，按 due_at 升序）。
// 复用 buildTaskViews（assignee / submissions / batches 预载，无场景锁——对齐 legacy
// 仅 selectinload(assignee, submissions, submission_batches)）。
func (r *Admin) ListOverdueTasks(ctx context.Context, now time.Time, limit int) ([]TaskView, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+taskColumns+` FROM tasks
		   WHERE is_retired = false AND status <> 'completed'
		     AND due_at IS NOT NULL AND due_at < $1
		   ORDER BY due_at ASC LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list overdue tasks: %w", err)
	}
	tasks, err := scanTaskRows(rows)
	if err != nil {
		return nil, err
	}
	return r.tasks.buildTaskViews(ctx, r.pool, tasks, false)
}

// ListRecentReadings 对齐 get_admin_overview 的 recent_readings：
// agent_runs 中 agent_type=script_reading 最近 3 条 + 分集 episode_code + 项目标题/题材。
func (r *Admin) ListRecentReadings(ctx context.Context) ([]RecentReadingView, error) {
	type readingRun struct {
		id         string
		episodeID  *string
		projectID  *string
		status     string
		durationMs *int32
		skillName  *string
		createdAt  time.Time
	}
	runs := []readingRun{}
	rows, err := r.pool.Query(ctx,
		`SELECT id, episode_id, project_id, status, duration_ms, skill_name, created_at
		   FROM agent_runs WHERE agent_type = 'script_reading' AND project_id IS NOT NULL
		  ORDER BY created_at DESC LIMIT 3`)
	if err != nil {
		return nil, fmt.Errorf("recent readings: %w", err)
	}
	for rows.Next() {
		var rr readingRun
		if err := rows.Scan(&rr.id, &rr.episodeID, &rr.projectID, &rr.status,
			&rr.durationMs, &rr.skillName, &rr.createdAt); err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, rr)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	episodeIDs := []string{}
	projectIDs := []string{}
	for _, rr := range runs {
		if rr.episodeID != nil {
			episodeIDs = appendStrDedup(episodeIDs, *rr.episodeID)
		}
		if rr.projectID != nil {
			projectIDs = appendStrDedup(projectIDs, *rr.projectID)
		}
	}
	epCodeByID, err := r.tasks.episodeCodesByID(ctx, r.pool, episodeIDs)
	if err != nil {
		return nil, err
	}
	projTitle := map[string]struct {
		title string
		genre *string
	}{}
	if len(projectIDs) > 0 {
		prows, err := r.pool.Query(ctx,
			`SELECT id, title, genre FROM projects WHERE id = ANY($1)`, projectIDs)
		if err != nil {
			return nil, fmt.Errorf("recent readings projects: %w", err)
		}
		for prows.Next() {
			var id, title string
			var genre *string
			if err := prows.Scan(&id, &title, &genre); err != nil {
				prows.Close()
				return nil, err
			}
			projTitle[id] = struct {
				title string
				genre *string
			}{title, genre}
		}
		if err := prows.Err(); err != nil {
			prows.Close()
			return nil, err
		}
		prows.Close()
	}

	out := make([]RecentReadingView, 0, len(runs))
	for _, rr := range runs {
		epCode := ""
		if rr.episodeID != nil {
			if c, ok := epCodeByID[*rr.episodeID]; ok {
				epCode = c
			}
		}
		nameLabel := ""
		if epCode != "" {
			nameLabel = epCode
		} else if rr.skillName != nil {
			nameLabel = *rr.skillName
		}
		statusLabel := "围读失败"
		switch rr.status {
		case "succeeded":
			statusLabel = "围读完成"
		case "running":
			statusLabel = "运行中"
		case "queued":
			statusLabel = "排队中"
		}
		metaParts := []string{}
		if rr.durationMs != nil {
			metaParts = append(metaParts, fmt.Sprintf("%ds", *rr.durationMs/1000))
		}
		metaParts = append(metaParts, statusLabel)
		meta := ""
		for i, p := range metaParts {
			if i > 0 {
				meta += " · "
			}
			meta += p
		}
		name := "剧本围读"
		if nameLabel != "" {
			name += "_" + nameLabel
		}
		view := RecentReadingView{
			ID:        rr.id,
			Name:      name,
			Meta:      meta,
			Status:    rr.status,
			CreatedAt: rr.createdAt.Format(time.RFC3339),
		}
		if rr.projectID != nil {
			view.ProjectId = *rr.projectID
			if p, ok := projTitle[*rr.projectID]; ok {
				view.Title = p.title
				if p.genre != nil {
					view.Genre = *p.genre
				}
			}
		}
		if epCode != "" {
			view.EpisodeCode = epCode
		} else {
			view.EpisodeCode = nameLabel
		}
		out = append(out, view)
	}
	return out, nil
}

// ProbeCounts 对齐 get_admin_probe 的 counts（27 张业务表行数；表名来自硬编码白名单）。
func (r *Admin) ProbeCounts(ctx context.Context) (map[string]int, error) {
	tables := []string{
		"users", "projects", "project_episodes", "scripts", "script_versions", "script_segments",
		"storyboards", "storyboard_versions", "tasks", "task_prompts", "submissions", "assets",
		"project_assets", "project_asset_lists", "project_asset_list_items", "asset_versions",
		"asset_relations", "image_outputs", "video_outputs", "final_outputs", "files", "agent_runs",
		"agent_feedback", "agent_training_samples", "script_breakdowns", "entity_versions",
		"operation_logs",
	}
	out := map[string]int{}
	for _, t := range tables {
		var n int
		if err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+t).Scan(&n); err != nil {
			return nil, fmt.Errorf("probe count %s: %w", t, err)
		}
		out[t] = n
	}
	return out, nil
}

// ProbeRecent 对齐 get_admin_probe 的 recent_projects / recent_agent_runs / recent_operation_logs。
func (r *Admin) ProbeRecent(ctx context.Context) ([]ProbeProject, []ProbeAgentRun, []ProbeOperationLog, error) {
	projects, err := r.probeRecentProjects(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	runs, err := r.probeRecentAgentRuns(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	logs, err := r.probeRecentOperationLogs(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	return projects, runs, logs, nil
}

func (r *Admin) probeRecentProjects(ctx context.Context) ([]ProbeProject, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, project_prefix, title, status, created_at
		   FROM projects ORDER BY created_at DESC LIMIT 5`)
	if err != nil {
		return nil, fmt.Errorf("probe recent projects: %w", err)
	}
	defer rows.Close()
	out := []ProbeProject{}
	for rows.Next() {
		var p ProbeProject
		var created time.Time
		if err := rows.Scan(&p.ID, &p.ProjectPrefix, &p.Title, &p.Status, &created); err != nil {
			return nil, err
		}
		iso := created.Format(time.RFC3339)
		p.CreatedAt = &iso
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Admin) probeRecentAgentRuns(ctx context.Context) ([]ProbeAgentRun, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, project_id, agent_type, status, error_message, created_at
		   FROM agent_runs ORDER BY created_at DESC LIMIT 8`)
	if err != nil {
		return nil, fmt.Errorf("probe recent agent runs: %w", err)
	}
	defer rows.Close()
	out := []ProbeAgentRun{}
	for rows.Next() {
		var a ProbeAgentRun
		var created time.Time
		if err := rows.Scan(&a.ID, &a.ProjectId, &a.AgentType, &a.Status, &a.ErrorMessage, &created); err != nil {
			return nil, err
		}
		iso := created.Format(time.RFC3339)
		a.CreatedAt = &iso
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Admin) probeRecentOperationLogs(ctx context.Context) ([]ProbeOperationLog, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, project_id, action, target_type, created_at
		   FROM operation_logs ORDER BY created_at DESC LIMIT 8`)
	if err != nil {
		return nil, fmt.Errorf("probe recent operation logs: %w", err)
	}
	defer rows.Close()
	out := []ProbeOperationLog{}
	for rows.Next() {
		var o ProbeOperationLog
		var created time.Time
		if err := rows.Scan(&o.ID, &o.ProjectId, &o.Action, &o.TargetType, &created); err != nil {
			return nil, err
		}
		iso := created.Format(time.RFC3339)
		o.CreatedAt = &iso
		out = append(out, o)
	}
	return out, rows.Err()
}

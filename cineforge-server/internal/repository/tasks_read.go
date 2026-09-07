package repository

// P3e task 域读路径（对齐 legacy repositories.py list_tasks / list_review_tasks /
// get_task / get_task_model / ensure_task_update_access + 批量预载助手）。
// 读路径统一接收 queryer：可同时用于 pool 与事务内。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// TaskActor 是对外读/写动作的调用方身份（user id + role）。
type TaskActor struct {
	ID   string
	Role string
}

// ---- 批量预载助手 ----

// storyboardByID 单分镜查询（get_task 详情路径）。
func (r *Tasks) storyboardByID(ctx context.Context, q queryer, id string) (*model.Storyboards, error) {
	var s model.Storyboards
	err := q.QueryRow(ctx, `SELECT id, project_id, episode_num, order_num, title, description, dialogue,
		 camera, duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
		 scene_code, scene_name, context_code, render_mode, script_id, script_segment_id, narration,
		 shot_type, current_version_id, created_at, updated_at
		 FROM storyboards WHERE id = $1`, id).
		Scan(&s.ID, &s.ProjectId, &s.EpisodeNum, &s.OrderNum, &s.Title, &s.Description,
			&s.Dialogue, &s.Camera, &s.DurationSeconds, &s.Characters, &s.Keyframes, &s.MirrorShots,
			&s.Status, &s.StoryboardCode, &s.SceneCode, &s.SceneName, &s.ContextCode, &s.RenderMode,
			&s.ScriptId, &s.ScriptSegmentId, &s.Narration, &s.ShotType, &s.CurrentVersionId,
			&s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("storyboard by id: %w", err)
	}
	return &s, nil
}

// storyboardsByIDs 批量按 id 取分镜（列表路径）。
func (r *Tasks) storyboardsByIDs(ctx context.Context, q queryer, ids []string) (map[string]*model.Storyboards, error) {
	out := map[string]*model.Storyboards{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, project_id, episode_num, order_num, title, description, dialogue,
		 camera, duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
		 scene_code, scene_name, context_code, render_mode, script_id, script_segment_id, narration,
		 shot_type, current_version_id, created_at, updated_at
		 FROM storyboards WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("storyboards by ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s model.Storyboards
		if err := rows.Scan(&s.ID, &s.ProjectId, &s.EpisodeNum, &s.OrderNum, &s.Title, &s.Description,
			&s.Dialogue, &s.Camera, &s.DurationSeconds, &s.Characters, &s.Keyframes, &s.MirrorShots,
			&s.Status, &s.StoryboardCode, &s.SceneCode, &s.SceneName, &s.ContextCode, &s.RenderMode,
			&s.ScriptId, &s.ScriptSegmentId, &s.Narration, &s.ShotType, &s.CurrentVersionId,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out[s.ID] = &s
	}
	return out, rows.Err()
}

// episodeCodesByID 批量取分集 episode_code（列表路径，对齐 _episode_codes_by_id_for_tasks）。
func (r *Tasks) episodeCodesByID(ctx context.Context, q queryer, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, episode_code FROM project_episodes WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("episode codes by ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, code string
		if err := rows.Scan(&id, &code); err != nil {
			return nil, err
		}
		out[id] = code
	}
	return out, rows.Err()
}

// episodeCodeByID 单分集 episode_code（get_task 详情路径）。不存在返回 ("", nil)。
func (r *Tasks) episodeCodeByID(ctx context.Context, q queryer, id string) (string, error) {
	var code string
	err := q.QueryRow(ctx, `SELECT episode_code FROM project_episodes WHERE id = $1`, id).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("episode code by id: %w", err)
	}
	return code, nil
}

// userDisplayNameMap 批量取用户 display_name。
func (r *Tasks) userDisplayNameMap(ctx context.Context, q queryer, userIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, display_name FROM users WHERE id = ANY($1)`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("user display names: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// submissionsByTaskIDs 批量取任务 submissions（task_id → 列表，created_at 稳定排序）。
func (r *Tasks) submissionsByTaskIDs(ctx context.Context, q queryer, tasks []*model.Tasks) (map[string][]*model.Submissions, error) {
	out := map[string][]*model.Submissions{}
	taskIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t != nil {
			taskIDs = append(taskIDs, t.ID)
		}
	}
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+submissionColumns+`
		 FROM submissions WHERE task_id = ANY($1) ORDER BY created_at, id`, taskIDs)
	if err != nil {
		return nil, fmt.Errorf("submissions by task ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out[s.TaskId] = append(out[s.TaskId], s)
	}
	return out, rows.Err()
}

// batchesByTaskIDs 批量取任务 submission batches（task_id → 列表，created_at 稳定排序）。
func (r *Tasks) batchesByTaskIDs(ctx context.Context, q queryer, tasks []*model.Tasks) (map[string][]*model.TaskSubmissionBatches, error) {
	out := map[string][]*model.TaskSubmissionBatches{}
	taskIDs := make([]string, 0, len(tasks))
	for _, t := range tasks {
		if t != nil {
			taskIDs = append(taskIDs, t.ID)
		}
	}
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+batchColumns+`
		 FROM task_submission_batches WHERE task_id = ANY($1) ORDER BY created_at, id`, taskIDs)
	if err != nil {
		return nil, fmt.Errorf("batches by task ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out[b.TaskId] = append(out[b.TaskId], b)
	}
	return out, rows.Err()
}

// ---- 可见性谓词（对齐 _visible_task_stmt / _active_asset_task_condition） ----

// visibleTaskWhere 追加任务的可见性过滤器：
// 基础条件 + variant_plan_id IS NULL；非 director/admin 还需项目权限 OR 执行人可见窗。
// 返回 SQL 片段与占位参数（调用方按序拼接参数）。
func visibleTaskWhere(actor TaskActor, now time.Time) (string, []any) {
	if isDirectorOrAdminRole(actor.Role) {
		return `tasks.variant_plan_id IS NULL`, nil
	}
	return `tasks.variant_plan_id IS NULL AND (
			tasks.project_id IN (SELECT project_id FROM permissions
				WHERE user_id = $1 AND project_id IS NOT NULL AND permission_type LIKE $2)
			OR (
				tasks.assignee_id = $3
				AND (tasks.status <> 'completed' OR tasks.visible_until IS NULL OR tasks.visible_until >= $4)
			)
		)`,
		[]any{actor.ID, projectRolePrefix + "%", actor.ID, now}
}

func visibleTaskCondition(actor TaskActor, now time.Time) (string, []any) {
	base := `(` + activeAssetTaskCondition() + `)`
	extra, args := visibleTaskWhere(actor, now)
	return base + ` AND ` + extra, args
}

// ---- 批量任务 → TaskView（列表/看板共用） ----

// buildTaskViews 对齐 list_tasks / list_review_tasks 的用户/分镜/分集/依赖/提交/批次批量预载循环。
// includeSceneLock=false 时 locked 仅代表依赖锁定（对齐 get_task / list_review_tasks）。
func (r *Tasks) buildTaskViews(ctx context.Context, q queryer, tasks []*model.Tasks, includeSceneLock bool) ([]TaskView, error) {
	if len(tasks) == 0 {
		return []TaskView{}, nil
	}
	userIDs := []string{}
	storyboardIDs := []string{}
	episodeIDs := []string{}
	for _, t := range tasks {
		if t == nil {
			continue
		}
		if t.AssigneeId != nil {
			userIDs = appendStrDedup(userIDs, *t.AssigneeId)
		}
		if t.StoryboardId != nil {
			storyboardIDs = appendStrDedup(storyboardIDs, *t.StoryboardId)
		}
		if t.EpisodeId != nil {
			episodeIDs = appendStrDedup(episodeIDs, *t.EpisodeId)
		}
	}
	namesByID, err := r.userDisplayNameMap(ctx, q, userIDs)
	if err != nil {
		return nil, err
	}
	sbsByID, err := r.storyboardsByIDs(ctx, q, storyboardIDs)
	if err != nil {
		return nil, err
	}
	epCodeByID, err := r.episodeCodesByID(ctx, q, episodeIDs)
	if err != nil {
		return nil, err
	}
	states, err := r.BatchTaskDependencyStates(ctx, q, tasks)
	if err != nil {
		return nil, err
	}
	contracts, err := r.BatchDependencyContracts(ctx, q, tasks, states)
	if err != nil {
		return nil, err
	}
	sceneLocks := map[string]bool{}
	if includeSceneLock {
		sceneLocks, err = r.BatchSceneLockStates(ctx, q, tasks)
		if err != nil {
			return nil, err
		}
	}
	subsByTask, err := r.submissionsByTaskIDs(ctx, q, tasks)
	if err != nil {
		return nil, err
	}
	batchesByTask, err := r.batchesByTaskIDs(ctx, q, tasks)
	if err != nil {
		return nil, err
	}
	out := make([]TaskView, 0, len(tasks))
	for _, t := range tasks {
		if t == nil {
			continue
		}
		state := states[t.ID]
		locked := state.Locked
		if includeSceneLock {
			locked = locked || sceneLocks[t.ID]
		}
		summary := DependencySummaryView{}
		var deps []DependencyAssetView
		if c, ok := contracts[t.ID]; ok {
			summary = c.Summary
			deps = c.Assets
		}
		var sb *model.Storyboards
		if t.StoryboardId != nil {
			sb = sbsByID[*t.StoryboardId]
		}
		var epCode *string
		if t.EpisodeId != nil {
			if c, ok := epCodeByID[*t.EpisodeId]; ok && c != "" {
				epCode = &c
			}
		}
		var assigneeName *string
		if t.AssigneeId != nil {
			if n, ok := namesByID[*t.AssigneeId]; ok && n != "" {
				assigneeName = &n
			}
		}
		out = append(out, taskToRead(t, sb, epCode, assigneeName, locked,
			state.IDs, state.AssetCodes, summary, deps, subsByTask[t.ID], batchesByTask[t.ID]))
	}
	return out, nil
}

// ---- ListTasks / ListReviewTasks ----

// ListTasks 对齐 list_tasks：可见任务 + 可选 project/status/assignee 过滤。
func (r *Tasks) ListTasks(ctx context.Context, q queryer, actor TaskActor, projectID *string,
	status *string, assigneeID *string, now time.Time) ([]TaskView, error) {
	sqlCond, args := visibleTaskCondition(actor, now)
	where := []string{sqlCond}
	if projectID != nil && *projectID != "" {
		args = append(args, *projectID)
		where = append(where, `tasks.project_id = $`+strconv.Itoa(len(args)))
	}
	if status != nil && *status != "" {
		args = append(args, *status)
		where = append(where, `tasks.status = $`+strconv.Itoa(len(args)))
	}
	if assigneeID != nil && *assigneeID != "" {
		args = append(args, *assigneeID)
		where = append(where, `tasks.assignee_id = $`+strconv.Itoa(len(args)))
	}
	joined := ""
	for i, w := range where {
		if i == 0 {
			joined = w
		} else {
			joined += ` AND ` + w
		}
	}
	rows, err := q.Query(ctx, `SELECT `+taskColumns+` FROM tasks
		WHERE `+joined+` ORDER BY tasks.status, tasks.due_at`, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	tasks, err := scanTaskRows(rows)
	if err != nil {
		return nil, err
	}
	return r.buildTaskViews(ctx, q, tasks, true)
}

// ListReviewTasks 对齐 list_review_tasks：导演/admin 审核工作台，跨项目。
func (r *Tasks) ListReviewTasks(ctx context.Context, q queryer, actor TaskActor) ([]TaskView, error) {
	if !isDirectorOrAdminRole(actor.Role) {
		return nil, forbiddenError("task_review")
	}
	rows, err := q.Query(ctx, `SELECT `+taskColumns+` FROM tasks
		WHERE `+activeAssetTaskCondition()+`
		  AND tasks.status IN ('submitted','reviewing','rejected','completed')
		  AND EXISTS (SELECT 1 FROM task_submission_batches
			WHERE task_id = tasks.id AND status IN ('submitted','approved','rework'))
		  AND tasks.project_id IN (SELECT id FROM projects WHERE deleted_at IS NULL)
		ORDER BY tasks.status, tasks.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list review tasks: %w", err)
	}
	defer rows.Close()
	tasks, err := scanTaskRows(rows)
	if err != nil {
		return nil, err
	}
	return r.buildTaskViews(ctx, q, tasks, false)
}

// ---- GetTask / GetTaskModel / EnsureTaskUpdateAccess ----

// GetTask 对齐 get_task：KeyError/PermissionError 语义 + 仅依赖锁定（无场景门）。
func (r *Tasks) GetTask(ctx context.Context, q queryer, taskID string, actor TaskActor) (TaskView, error) {
	t, err := r.GetActiveAssetTask(ctx, q, taskID)
	if err != nil {
		return TaskView{}, err
	}
	if t == nil {
		return TaskView{}, notFoundError(taskID)
	}
	if !isDirectorOrAdminRole(actor.Role) {
		hasPerm, err := r.HasProjectPermission(ctx, q, actor.ID, t.ProjectId)
		if err != nil {
			return TaskView{}, err
		}
		isAssignee := t.AssigneeId != nil && *t.AssigneeId == actor.ID
		if !isAssignee && !hasPerm {
			return TaskView{}, forbiddenError(taskID)
		}
	}
	locked, depIDs, depCodes, err := r.TaskDependencyState(ctx, q, t)
	if err != nil {
		return TaskView{}, err
	}
	summary, deps, err := r.DependencyContractForTask(ctx, q, t, depIDs, depCodes)
	if err != nil {
		return TaskView{}, err
	}
	var sb *model.Storyboards
	if t.StoryboardId != nil {
		sb, err = r.storyboardByID(ctx, q, *t.StoryboardId)
		if err != nil {
			return TaskView{}, err
		}
	}
	var epCode *string
	if t.EpisodeId != nil {
		code, err := r.episodeCodeByID(ctx, q, *t.EpisodeId)
		if err != nil {
			return TaskView{}, err
		}
		if code != "" {
			epCode = &code
		}
	}
	var assigneeName *string
	if t.AssigneeId != nil {
		names, err := r.userDisplayNameMap(ctx, q, []string{*t.AssigneeId})
		if err != nil {
			return TaskView{}, err
		}
		if n, ok := names[*t.AssigneeId]; ok && n != "" {
			assigneeName = &n
		}
	}
	subs, err := r.listTaskSubmissions(ctx, q, t.ID)
	if err != nil {
		return TaskView{}, err
	}
	batches, err := r.listTaskBatches(ctx, q, t.ID)
	if err != nil {
		return TaskView{}, err
	}
	return taskToRead(t, sb, epCode, assigneeName, locked, depIDs, depCodes, summary, deps, subs, batches), nil
}

// GetTaskModel 对齐 get_task_model：返回 *model.Tasks（路由需要时再序列化）。
func (r *Tasks) GetTaskModel(ctx context.Context, q queryer, taskID string, actor TaskActor) (*model.Tasks, error) {
	t, err := r.GetActiveAssetTask(ctx, q, taskID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, notFoundError(taskID)
	}
	if !isDirectorOrAdminRole(actor.Role) {
		hasPerm, err := r.HasProjectPermission(ctx, q, actor.ID, t.ProjectId)
		if err != nil {
			return nil, err
		}
		isAssignee := t.AssigneeId != nil && *t.AssigneeId == actor.ID
		if !isAssignee && !hasPerm {
			return nil, forbiddenError(taskID)
		}
	}
	return t, nil
}

// EnsureTaskUpdateAccess 对齐 ensure_task_update_access：assignee-or-write 访问 + GetTask 返回值。
func (r *Tasks) EnsureTaskUpdateAccess(ctx context.Context, q queryer, taskID string, actor TaskActor) (TaskView, error) {
	t, err := r.GetActiveAssetTask(ctx, q, taskID)
	if err != nil {
		return TaskView{}, err
	}
	if t == nil {
		return TaskView{}, notFoundError(taskID)
	}
	canUpdate, err := r.canUpdateTask(ctx, q, t, actor.ID, actor.Role)
	if err != nil {
		return TaskView{}, err
	}
	if !canUpdate {
		return TaskView{}, forbiddenError(taskID)
	}
	return r.GetTask(ctx, q, taskID, actor)
}
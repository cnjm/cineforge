package repository

// P4e project-query 读侧（对齐 legacy app/services/query_index.py 的数据来源）：
//
//   - ListStoryboardsForIndex：全量或按可见分镜 id 集合过滤（非 director 仅可见任务关联的分镜）
//   - ListTasksForIndex：全量或按可见任务 id 集合过滤（active asset task 条件，含 submissions）
//   - ListVisibleTaskIDs：非 director 用户的可见任务 id（assignee_id 匹配 + active 条件）→ 空则 query 返回空文档集
//
// 文档构建与检索算法在 internal/service/project_query.go。

import (
	"context"
	"fmt"

	"cineforge/server/internal/model"
)

// ListStoryboardsForIndex 列出项目分镜（按 episode_num, order_num）。restrict 时仅返回 ids 中的分镜；
// restrict=false（director/admin）返回全量。
func (r *Projects) ListStoryboardsForIndex(ctx context.Context, projectID string, storyboardIDs []string, restrict bool) ([]model.Storyboards, error) {
	where := `project_id = $1`
	args := []any{projectID}
	if restrict {
		args = append(args, storyboardIDs)
		where += ` AND id = ANY($2)`
	}
	rows, err := r.pool.Query(ctx, `SELECT id, project_id, episode_num, order_num, title, description, dialogue,
		 camera, duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
		 scene_code, scene_name, context_code, render_mode, script_id, script_segment_id, narration,
		 shot_type, current_version_id, created_at, updated_at
		 FROM storyboards WHERE `+where+` ORDER BY episode_num, order_num`, args...)
	if err != nil {
		return nil, fmt.Errorf("list storyboards for index: %w", err)
	}
	defer rows.Close()
	var out []model.Storyboards
	for rows.Next() {
		var s model.Storyboards
		if err := rows.Scan(&s.ID, &s.ProjectId, &s.EpisodeNum, &s.OrderNum, &s.Title, &s.Description,
			&s.Dialogue, &s.Camera, &s.DurationSeconds, &s.Characters, &s.Keyframes, &s.MirrorShots,
			&s.Status, &s.StoryboardCode, &s.SceneCode, &s.SceneName, &s.ContextCode, &s.RenderMode,
			&s.ScriptId, &s.ScriptSegmentId, &s.Narration, &s.ShotType, &s.CurrentVersionId,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan storyboard for index: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate storyboards for index: %w", err)
	}
	return out, nil
}

// ListVisibleTaskIDs 返回项目内用户可见（assignee_id 匹配且非退役、资产未删除/未排除）的任务 id。
// 空结果由调用方处理：非 director 用户 → query 文档集为空。
func (r *Projects) ListVisibleTaskIDs(ctx context.Context, projectID, userID string) ([]string, error) {
	ids, err := r.listQueryTaskIDs(ctx,
		`project_id = $1 AND assignee_id = $2 AND t.is_retired = false AND
		 (t.asset_id IS NULL OR t.asset_id IN (SELECT id FROM assets WHERE status NOT IN ('deleted','excluded')))`,
		projectID, userID)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// ListStoryboardIDsForTasks 返回这些任务引用的非空 storyboard id 集合（query 非 director 分镜过滤）。
func (r *Projects) ListStoryboardIDsForTasks(ctx context.Context, taskIDs []string) ([]string, error) {
	if len(taskIDs) == 0 {
		return []string{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT storyboard_id FROM tasks WHERE storyboard_id IS NOT NULL AND id = ANY($1)`, taskIDs)
	if err != nil {
		return nil, fmt.Errorf("list storyboard ids for tasks: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan storyboard id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate storyboard ids: %w", err)
	}
	return out, nil
}

// ListTasksForIndex 列出项目任务（对齐 query_index 的 task 文档来源）：
// active asset task 条件内，restrict 时仅返回 taskIDs 中的任务，并加载全部 submissions。
func (r *Projects) ListTasksForIndex(ctx context.Context, projectID string, taskIDs []string, restrict bool) ([]*model.Tasks, map[string][]*model.Submissions, error) {
	where := `t.project_id = $1 AND ` + activeAssetTaskCondition()
	args := []any{projectID}
	if restrict {
		args = append(args, taskIDs)
		where += ` AND t.id = ANY($2)`
	}
	rows, err := r.pool.Query(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE `+where+` ORDER BY t.created_at`, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list tasks for index: %w", err)
	}
	defer rows.Close()
	var tasks []*model.Tasks
	var taskIDList []string
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("scan task for index: %w", err)
		}
		tasks = append(tasks, t)
		taskIDList = append(taskIDList, t.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate tasks for index: %w", err)
	}
	subs, err := r.listSubmissionsForIndex(ctx, taskIDList)
	if err != nil {
		return nil, nil, err
	}
	return tasks, subs, nil
}

// listSubmissionsForIndex 一次查询加载多任务全量 submissions，按 task_id 分组（对齐 eager load）。
func (r *Projects) listSubmissionsForIndex(ctx context.Context, taskIDs []string) (map[string][]*model.Submissions, error) {
	out := make(map[string][]*model.Submissions)
	if len(taskIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT `+submissionColumns+` FROM submissions WHERE task_id = ANY($1)`, taskIDs)
	if err != nil {
		return nil, fmt.Errorf("list submissions for index: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, fmt.Errorf("scan submission for index: %w", err)
		}
		out[s.TaskId] = append(out[s.TaskId], s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate submissions for index: %w", err)
	}
	return out, nil
}

// listQueryTaskIDs 通用 id 查询（可见任务/分镜 id 集合）。
func (r *Projects) listQueryTaskIDs(ctx context.Context, where string, args ...any) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT t.id FROM tasks t WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list query task ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan query task id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate query task ids: %w", err)
	}
	return out, nil
}
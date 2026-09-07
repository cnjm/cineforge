package repository

// P3e task 域分组 / 生产看板 / 批量分配（对齐 legacy repositories.py list_storyboards /
// get_board / _scene_gating_core / bulk_assign_tasks / create_storyboard_video_tasks，
// 以及 production_ ... 的分镜与看板读路径）。
// 读路径接收 queryer；写路径接收 pgx.Tx（事务由服务层拥有，不自行 commit）。
// 注：generate_asset_tasks / generate_storyboard_tasks 的“已确认资产/提示词”前置校验
// ……依赖 P3d 期服务侧 inventory 基建，在服务层实现（见 service/tasks.py → P3e 服务 todo）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// ---- storyboard → StoryboardRead（对齐 storyboard_to_read） ----

// storyboardToView 对齐 storyboard_to_read（characters/keyframes/mirror_shots 归一化）。
func storyboardToView(s *model.Storyboards) StoryboardView {
	return StoryboardView{
		ID:                      s.ID,
		ProjectId:               s.ProjectId,
		ScriptSegmentId:         s.ScriptSegmentId,
		EpisodeNum:              s.EpisodeNum,
		OrderNum:                s.OrderNum,
		StoryboardCode:          s.StoryboardCode,
		Title:                   s.Title,
		Description:             s.Description,
		Dialogue:                s.Dialogue,
		DialogueBackTranslation: storyboardBackTranslation(s),
		Camera:                  s.Camera,
		ShotType:                s.ShotType,
		DurationSeconds:         storyboardDuration(s),
		Characters:              jsonStringSlice(s.Characters),
		Keyframes:               jsonMapList(s.Keyframes),
		MirrorShots:             jsonMapList(s.MirrorShots),
		SceneCode:               s.SceneCode,
		SceneName:               s.SceneName,
		ContextCode:             s.ContextCode,
		RenderMode:              s.RenderMode,
		Status:                  s.Status,
	}
}

func jsonStringSlice(raw json.RawMessage) []string {
	var out []string
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = []string{}
	}
	return out
}

func jsonMapList(raw json.RawMessage) []map[string]any {
	var items []map[string]any
	_ = json.Unmarshal(raw, &items)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if item != nil {
			out = append(out, item)
		}
	}
	return out
}

// ---- ListStoryboards（GET /projects/{id}/storyboards） ----

// visibleStoryboardIDsForProject 对齐 _visible_storyboard_ids_for_project。
func (r *Tasks) visibleStoryboardIDsForProject(ctx context.Context, q queryer, projectID string, actor TaskActor, now time.Time) ([]string, error) {
	hasPerm := false
	var err error
	if !isDirectorOrAdminRole(actor.Role) {
		hasPerm, err = r.HasProjectPermission(ctx, q, actor.ID, projectID)
		if err != nil {
			return nil, err
		}
	}
	if isDirectorOrAdminRole(actor.Role) || hasPerm {
		rows, err := q.Query(ctx, `SELECT id FROM storyboards WHERE project_id = $1`, projectID)
		if err != nil {
			return nil, fmt.Errorf("visible storyboard ids: %w", err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	cond, args := visibleTaskWhere(actor, now)
	args = append(args, projectID)
	rows, err := q.Query(ctx, `SELECT DISTINCT tasks.storyboard_id FROM tasks
		WHERE tasks.storyboard_id IS NOT NULL AND `+cond+` AND tasks.project_id = $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("visible storyboard ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListStoryboards 对齐 list_storyboards。
func (r *Tasks) ListStoryboards(ctx context.Context, q queryer, projectID string, episodeID *string,
	actor TaskActor, now time.Time) ([]StoryboardView, error) {
	where := `project_id = $1`
	args := []any{projectID}
	argNo := 1
	if episodeID != nil && *episodeID != "" {
		var episodeNo *int32
		err := q.QueryRow(ctx, `SELECT episode_no FROM project_episodes WHERE id = $1`, *episodeID).Scan(&episodeNo)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, notFoundError(*episodeID)
		}
		if err != nil {
			return nil, fmt.Errorf("find episode: %w", err)
		}
		if episodeNo == nil {
			return nil, notFoundError(*episodeID)
		}
		argNo++
		args = append(args, *episodeNo)
		where += ` AND episode_num = $` + strconv.Itoa(argNo)
	}
	if !isDirectorOrAdminRole(actor.Role) {
		ids, err := r.visibleStoryboardIDsForProject(ctx, q, projectID, actor, now)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []StoryboardView{}, nil
		}
		argNo++
		args = append(args, ids)
		where += ` AND id = ANY($` + strconv.Itoa(argNo) + `)`
	}
	rows, err := q.Query(ctx, `SELECT id, project_id, episode_num, order_num, title, description, dialogue,
		 camera, duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
		 scene_code, scene_name, context_code, render_mode, script_id, script_segment_id, narration,
		 shot_type, current_version_id, created_at, updated_at
		 FROM storyboards WHERE `+where+` ORDER BY episode_num, order_num`, args...)
	if err != nil {
		return nil, fmt.Errorf("list storyboards: %w", err)
	}
	defer rows.Close()
	var out []StoryboardView
	for rows.Next() {
		var s model.Storyboards
		if err := rows.Scan(&s.ID, &s.ProjectId, &s.EpisodeNum, &s.OrderNum, &s.Title, &s.Description,
			&s.Dialogue, &s.Camera, &s.DurationSeconds, &s.Characters, &s.Keyframes, &s.MirrorShots,
			&s.Status, &s.StoryboardCode, &s.SceneCode, &s.SceneName, &s.ContextCode, &s.RenderMode,
			&s.ScriptId, &s.ScriptSegmentId, &s.Narration, &s.ShotType, &s.CurrentVersionId,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, storyboardToView(&s))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- GetProductionBoard（GET /projects/{id}/board） ----

// GetProductionBoard 对齐 get_board：按分镜输出生产看板行。
// 注意：board 的 TaskRead 不带依赖/场景锁与分集 code（episode_code 走 EP## 回退），
// 与 legacy 完全一致。
func (r *Tasks) GetProductionBoard(ctx context.Context, q queryer, projectID string,
	actor TaskActor, now time.Time) ([]ProductionBoardRow, error) {
	if !isDirectorOrAdminRole(actor.Role) {
		access, err := r.CanAccessProject(ctx, q, projectID, actor.ID, actor.Role)
		if err != nil {
			return nil, err
		}
		if !access {
			return nil, forbiddenError(projectID)
		}
	}
	storyboardRows, err := q.Query(ctx, `SELECT id, project_id, episode_num, order_num, title, description, dialogue,
		 camera, duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
		 scene_code, scene_name, context_code, render_mode, script_id, script_segment_id, narration,
		 shot_type, current_version_id, created_at, updated_at
		 FROM storyboards WHERE project_id = $1 ORDER BY episode_num, order_num`, projectID)
	if err != nil {
		return nil, fmt.Errorf("board storyboards: %w", err)
	}
	var storyboards []*model.Storyboards
	for storyboardRows.Next() {
		var s model.Storyboards
		if err := storyboardRows.Scan(&s.ID, &s.ProjectId, &s.EpisodeNum, &s.OrderNum, &s.Title, &s.Description,
			&s.Dialogue, &s.Camera, &s.DurationSeconds, &s.Characters, &s.Keyframes, &s.MirrorShots,
			&s.Status, &s.StoryboardCode, &s.SceneCode, &s.SceneName, &s.ContextCode, &s.RenderMode,
			&s.ScriptId, &s.ScriptSegmentId, &s.Narration, &s.ShotType, &s.CurrentVersionId,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			storyboardRows.Close()
			return nil, err
		}
		storyboards = append(storyboards, &s)
	}
	storyboardRows.Close()
	if err := storyboardRows.Err(); err != nil {
		return nil, err
	}
	if !isDirectorOrAdminRole(actor.Role) {
		ids, err := r.visibleStoryboardIDsForProject(ctx, q, projectID, actor, now)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return []ProductionBoardRow{}, nil
		}
		idSet := map[string]bool{}
		for _, id := range ids {
			idSet[id] = true
		}
		kept := storyboards[:0]
		for _, s := range storyboards {
			if idSet[s.ID] {
				kept = append(kept, s)
			}
		}
		storyboards = kept
	}
	cond, args := visibleTaskCondition(actor, now)
	args = append(args, projectID)
	predicate := cond + ` AND tasks.project_id = $` + strconv.Itoa(len(args))
	taskRows, err := q.Query(ctx, `SELECT `+taskColumns+` FROM tasks WHERE `+predicate, args...)
	if err != nil {
		return nil, fmt.Errorf("board tasks: %w", err)
	}
	tasks, err := scanTaskRows(taskRows)
	if err != nil {
		return nil, err
	}
	subsByTask, err := r.submissionsByTaskIDs(ctx, q, tasks)
	if err != nil {
		return nil, err
	}
	batchesByTask, err := r.batchesByTaskIDs(ctx, q, tasks)
	if err != nil {
		return nil, err
	}
	storyboardByID := map[string]*model.Storyboards{}
	for _, s := range storyboards {
		storyboardByID[s.ID] = s
	}
	byStoryboard := map[string][]TaskView{}
	subsByStoryboard := map[string][]SubmissionView{}
	for _, t := range tasks {
		if t == nil || t.StoryboardId == nil {
			continue
		}
		sb := storyboardByID[*t.StoryboardId]
		subs := subsByTask[t.ID]
		batches := batchesByTask[t.ID]
		byStoryboard[*t.StoryboardId] = append(byStoryboard[*t.StoryboardId],
			taskToRead(t, sb, nil, nil, false, []string{}, []string{}, DependencySummaryView{}, []DependencyAssetView{}, subs, batches))
		for _, sub := range subs {
			if sub != nil {
				subsByStoryboard[*t.StoryboardId] = append(subsByStoryboard[*t.StoryboardId], submissionToView(sub))
			}
		}
	}
	out := make([]ProductionBoardRow, 0, len(storyboards))
	for _, s := range storyboards {
		tasks := byStoryboard[s.ID]
		if tasks == nil {
			tasks = []TaskView{}
		}
		rowSubs := subsByStoryboard[s.ID]
		if rowSubs == nil {
			rowSubs = []SubmissionView{}
		}
		out = append(out, ProductionBoardRow{
			StoryboardId: s.ID,
			EpisodeNum:   s.EpisodeNum,
			OrderNum:     s.OrderNum,
			Title:        s.Title,
			Description:  s.Description,
			Characters:   jsonStringSlice(s.Characters),
			Status:       s.Status,
			Tasks:        tasks,
			Submissions:  rowSubs,
		})
	}
	return out, nil
}

// ---- SceneGating（GET /projects/{id}/scene-gating） ----

// SceneGating 对齐 scene_gating：权限 + _scene_gating_core。
func (r *Tasks) SceneGating(ctx context.Context, q queryer, projectID string, actor TaskActor) ([]SceneGatingRow, error) {
	if !isDirectorOrAdminRole(actor.Role) {
		access, err := r.CanAccessProject(ctx, q, projectID, actor.ID, actor.Role)
		if err != nil {
			return nil, err
		}
		if !access {
			return nil, forbiddenError(projectID)
		}
	}
	return r.SceneGatingCore(ctx, q, projectID)
}

// ---- 共享确定性助手 ----

// taskIdempotencyKey 对齐 _task_idempotency_key 的 canonical JSON：
// sorted keys 紧凑（, :）序列化（Python sort_keys + separators），字段缺失时编为 null。
func taskIdempotencyKey(identity map[string]any) string {
	if identity == nil {
		identity = map[string]any{}
	}
	b, err := json.Marshal(identity)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// normalizeEpisodeCode 对齐 _normalize_episode_code（空值输出 nil）。
func normalizeEpisodeCode(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	normalized := NormalizeEpisodeCode(*value)
	return &normalized
}

// episodeNumFromCode 对齐 _episode_num_from_code。
func episodeNumFromCode(code *string) int {
	if code == nil {
		return 1
	}
	return int(EpisodeNumFromCode(*code))
}

// storyboardSceneCodes 对齐 _storyboard_scene_codes。
func storyboardSceneCodes(item map[string]any) []string {
	var raw any
	for _, k := range []string{"scene_codes", "scene_code", "scene_id", "scene_name"} {
		if v, ok := item[k]; ok {
			raw = v
			break
		}
	}
	values := anyToStringList(raw)
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		s := strings.TrimSpace(v)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// 分集分镜的 episode 定位复用共享 storyboardEpisodeCode（projects_admin.go）。

// firstArtistUserID 取第一个活动 artist 用户（created_at 升序）。
func (r *Tasks) firstArtistUserID(ctx context.Context, q queryer) (string, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM users WHERE role = 'artist' AND is_active = true ORDER BY created_at LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("first artist user: %w", err)
	}
	return id, nil
}

// ---- BulkAssignTasks（POST /projects/{id}/task-assignments） ----

// BulkAssignType 对齐 BulkAssignRequest。
type BulkAssignType struct {
	Scope              string
	Key                string
	AssigneeID         *string
	EpisodeID          *string
	ReassignmentReason *string
}

// CompletedAssignment 对齐返回列表项。
type CompletedAssignment struct {
	TaskID       string  `json:"task_id"`
	Title        string  `json:"title"`
	AssigneeID   *string `json:"assignee_id"`
	AssigneeName *string `json:"assignee_name"`
}

// BulkAssignResult 对齐 bulk_assign_tasks 返回 dict。
type BulkAssignResult struct {
	Assigned              int                     `json:"assigned"`
	Reassigned            int                     `json:"reassigned"`
	Scope                 string                  `json:"scope"`
	Key                   string                  `json:"key"`
	Skipped               int                     `json:"skipped"`
	SkippedByStatus       map[string]int          `json:"skipped_by_status"`
	CompletedAssignments  []CompletedAssignment   `json:"completed_assignments"`
}

// BulkAssignTasks 对齐 bulk_assign_tasks（事务内执行，不提交）。
func (r *Tasks) BulkAssignTasks(ctx context.Context, tx pgx.Tx, projectID string, in BulkAssignType,
	actor TaskActor, now time.Time) (*BulkAssignResult, error) {
	// ensure_project_write_access
	var projectExists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1 AND deleted_at IS NULL)`, projectID).Scan(&projectExists)
	if err != nil {
		return nil, fmt.Errorf("check project: %w", err)
	}
	if !projectExists {
		return nil, notFoundError(projectID)
	}
	canWrite, err := r.CanWriteProject(ctx, tx, projectID, actor.ID, actor.Role)
	if err != nil {
		return nil, err
	}
	if !canWrite {
		return nil, forbiddenError(projectID)
	}
	if in.AssigneeID != nil {
		assignee, err := r.userByID(ctx, tx, *in.AssigneeID)
		if err != nil {
			return nil, err
		}
		if assignee == nil || !assignee.IsActive || isDirectorOrAdminRole(assignee.Role) {
			return nil, valueError("执行人无效、已停用或不是可分配的制作人员。")
		}
	}
	where := []string{`project_id = $1`, activeAssetTaskCondition()}
	args := []any{projectID}

	// 每个 scope 用绝对占位符编号（$1 固定为 projectID），避免子查询内
	// 既有 $1（project_id）被二次重排。
	switch in.Scope {
	case "asset_category":
		if in.Key == "music" {
			where = append(where, `task_type IN ('audio') AND asset_id IN (
				SELECT id FROM assets WHERE project_id = $1 AND asset_type IN ('music','voice_profile')
				  AND status NOT IN ('deleted','excluded'))`)
		} else {
			args = append(args, normalizeAssetTypeKey(in.Key))
			where = append(where, `task_type IN ('asset') AND asset_id IN (
				SELECT id FROM assets WHERE project_id = $1 AND asset_type = $2
				  AND status NOT IN ('deleted','excluded'))`)
		}
	case "asset_context":
		parts := splitKeyParts(in.Key)
		if len(parts) != 3 {
			return nil, valueError("资产任务上下文格式错误。")
		}
		asset := model.Assets{}
		err := tx.QueryRow(ctx, `SELECT id, project_id, asset_type, status FROM assets WHERE id = $1`, parts[0]).
			Scan(&asset.ID, &asset.ProjectId, &asset.AssetType, &asset.Status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, valueError("资产任务上下文不存在。")
		}
		if err != nil {
			return nil, fmt.Errorf("asset context asset: %w", err)
		}
		if asset.ProjectId == nil || *asset.ProjectId != projectID || inactiveTaskAssetStatuses[asset.Status] {
			return nil, valueError("资产任务上下文不存在。")
		}
		taskTypes := []string{taskTypeAsset}
		if asset.AssetType == assetTypeMusic || asset.AssetType == assetTypeVoiceProfile {
			taskTypes = []string{taskTypeAudio}
		}
		args = append(args, taskTypes, parts[0])
		cond := `task_type = ANY($2) AND asset_id = $3`
		if asset.AssetType != assetTypeCharacter {
			args = append(args, parts[1], parts[2])
			cond += ` AND COALESCE(age_stage_code,'') = $4 AND COALESCE(costume_variant_code,'') = $5`
		}
		where = append(where, cond)
	case "all_assets":
		where = append(where, `task_type IN ('asset','audio')`)
	case "scene":
		args = append(args, in.Key)
		where = append(where, `task_type IN ('storyboard_shot','text_to_image','image_to_video')
			AND storyboard_id IS NOT NULL AND (scene_code = $2 OR scene_name = $2)`)
		if in.EpisodeID != nil && *in.EpisodeID != "" {
			args = append(args, *in.EpisodeID)
			where = append(where, `episode_id = $3`)
		}
	case "all_storyboards":
		where = append(where, `task_type IN ('storyboard_shot','text_to_image','image_to_video')
			AND storyboard_id IS NOT NULL`)
		if in.EpisodeID != nil && *in.EpisodeID != "" {
			args = append(args, *in.EpisodeID)
			where = append(where, `episode_id = $2`)
		}
	default:
		return nil, valueError(fmt.Sprintf("未知的分配范围: %s", in.Scope))
	}

	stmt := `SELECT ` + taskColumns + ` FROM tasks WHERE ` + strings.Join(where, " AND ")
	candidates, err := r.scanTasks(ctx, tx, stmt, args...)
	if err != nil {
		return nil, err
	}
	reason := ""
	if in.ReassignmentReason != nil {
		reason = strings.TrimSpace(*in.ReassignmentReason)
	}
	assignable := map[string]bool{taskStatusTodo: true, taskStatusRejected: true}
	if reason != "" {
		assignable[taskStatusInProgress] = true
		assignable[taskStatusOverdue] = true
	}
	tasks := []*model.Tasks{}
	skippedByStatus := map[string]int{}
	completedTasks := []*model.Tasks{}
	for _, t := range candidates {
		if assignable[t.Status] {
			tasks = append(tasks, t)
		} else {
			skippedByStatus[t.Status]++
		}
		if t.Status == taskStatusCompleted {
			completedTasks = append(completedTasks, t)
		}
	}
	completedUserIDs := []string{}
	for _, t := range completedTasks {
		if t.AssigneeId != nil {
			completedUserIDs = appendStrDedup(completedUserIDs, *t.AssigneeId)
		}
	}
	completedUsers, err := r.userDisplayNameMap(ctx, tx, completedUserIDs)
	if err != nil {
		return nil, err
	}
	reassignments := []taskReassignment{}
	for _, t := range tasks {
		if t.AssigneeId != nil && !sameAssignee(t.AssigneeId, in.AssigneeID) {
			reassignments = append(reassignments, taskReassignment{task: t, oldAssignee: *t.AssigneeId})
		}
	}
	if len(reassignments) > 0 && reason == "" {
		return nil, valueError("批量改派必须填写改派原因。")
	}
	for _, t := range tasks {
		t.AssigneeId = in.AssigneeID
		t.AssignedBy = ptrToStr(actor.ID)
		t.AssignedAt = &now
		t.UpdatedAt = now
		if err := persistTask(ctx, tx, t); err != nil {
			return nil, err
		}
	}
	if len(reassignments) > 0 {
		if err := r.addTaskReassignmentNotifications(ctx, tx, reassignments, in.AssigneeID, reason); err != nil {
			return nil, err
		}
	}
	completedAssignments := []CompletedAssignment{}
	for _, t := range completedTasks {
		var name *string
		if t.AssigneeId != nil {
			if n, ok := completedUsers[*t.AssigneeId]; ok && n != "" {
				name = &n
			}
		}
		completedAssignments = append(completedAssignments, CompletedAssignment{
			TaskID: t.ID, Title: t.Title, AssigneeID: t.AssigneeId, AssigneeName: name,
		})
	}
	return &BulkAssignResult{
		Assigned:             len(tasks),
		Reassigned:           len(reassignments),
		Scope:                in.Scope,
		Key:                  in.Key,
		Skipped:              len(candidates) - len(tasks),
		SkippedByStatus:      skippedByStatus,
		CompletedAssignments: completedAssignments,
	}, nil
}

func (r *Tasks) scanTasks(ctx context.Context, q queryer, sql string, args ...any) ([]*model.Tasks, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("scan tasks: %w", err)
	}
	defer rows.Close()
	return scanTaskRows(rows)
}

func normalizeAssetTypeKey(key string) string {
	raw := strings.ToLower(strings.TrimSpace(key))
	switch raw {
	case "人物", "角色", "character":
		return assetTypeCharacter
	case "scene", "场景":
		return assetTypeScene
	case "prop", "道具":
		return assetTypeProp
	default:
		return assetTypeProp
	}
}

func splitKeyParts(key string) []string {
	parts := strings.SplitN(key, "|", 3)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// ---- CreateStoryboardVideoTasks（POST /projects/{id}/storyboard-video-tasks） ----

// StoryboardVideoTaskCreate 对齐 StoryboardVideoTaskCreate。
type StoryboardVideoTaskCreate struct {
	EpisodeCode *string
	SceneCode   *string
	SceneName   *string
	AssigneeID  *string
}

// StoryboardVideoTaskResult 对齐 StoryboardVideoTaskCreateResponse。
type StoryboardVideoTaskResult struct {
	ProjectID    string  `json:"project_id"`
	EpisodeCode  *string `json:"episode_code"`
	SceneCode    *string `json:"scene_code"`
	CreatedTasks int     `json:"created_tasks"`
	TaskCount    int     `json:"task_count"`
}

// CreateStoryboardVideoTasks 对齐 create_storyboard_video_tasks 的确定性构建部分
// （_require_asset_prompt_designs_for_scope 前置校验由服务层在调用前完成）。
func (r *Tasks) CreateStoryboardVideoTasks(ctx context.Context, tx pgx.Tx, projectID string,
	in StoryboardVideoTaskCreate, actor TaskActor, now time.Time) (*StoryboardVideoTaskResult, error) {
	var projectDeleted *time.Time
	projectExists := true
	err := tx.QueryRow(ctx, `SELECT deleted_at FROM projects WHERE id = $1`, projectID).Scan(&projectDeleted)
	if errors.Is(err, pgx.ErrNoRows) {
		projectExists = false
	} else if err != nil {
		return nil, fmt.Errorf("find project: %w", err)
	}
	if !projectExists || projectDeleted != nil {
		return nil, notFoundError(projectID)
	}
	canWrite, err := r.CanWriteProject(ctx, tx, projectID, actor.ID, actor.Role)
	if err != nil {
		return nil, err
	}
	if !canWrite {
		return nil, forbiddenError(projectID)
	}
	episodeCode := normalizeEpisodeCode(in.EpisodeCode)
	sceneCode := strings.TrimSpace(derefValue(in.SceneCode))
	if sceneCode == "" {
		sceneCode = ""
	}
	var sceneCodePtr *string
	if sceneCode != "" {
		sceneCodePtr = &sceneCode
	}

	// 最新 ScriptBreakdown → content_json.storyboards 匹配 (episode_num, order_num)。
	var contentJSON json.RawMessage
	err = tx.QueryRow(ctx, `SELECT content_json FROM script_breakdowns
		WHERE project_id = $1 ORDER BY version DESC LIMIT 1`, projectID).Scan(&contentJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		contentJSON = nil
	} else if err != nil {
		return nil, fmt.Errorf("latest breakdown: %w", err)
	}
	var breakdown struct {
		Storyboards []map[string]any `json:"storyboards"`
	}
	if contentJSON != nil {
		_ = json.Unmarshal(contentJSON, &breakdown)
	}
	matched := false
	matchedKeys := map[[2]int]bool{}
	if episodeCode != nil {
		for _, item := range breakdown.Storyboards {
			if storyboardEpisodeCode(item) != *episodeCode {
				continue
			}
			if sceneCodePtr != nil && !containsStr(storyboardSceneCodes(item), *sceneCodePtr) {
				continue
			}
			epNum := episodeNumFromCode(episodeCode)
			orderNum := SafeInt(firstNonNil(firstNonNil(item["order_num"], item["order_no"]), item["shot_no"]), 0)
			if epNum > 0 && orderNum > 0 {
				matchedKeys[[2]int{epNum, orderNum}] = true
				matched = true
			}
		}
	}
	if sceneCodePtr != nil && !matched {
		taskCount, err := r.countActiveTasks(ctx, tx, projectID)
		if err != nil {
			return nil, err
		}
		return &StoryboardVideoTaskResult{
			ProjectID: projectID, EpisodeCode: episodeCode, SceneCode: sceneCodePtr,
			CreatedTasks: 0, TaskCount: taskCount,
		}, nil
	}

	storyboardWhere := `project_id = $1`
	args := []any{projectID}
	if episodeCode != nil {
		args = append(args, episodeNumFromCode(episodeCode))
		storyboardWhere += ` AND episode_num = $` + strconv.Itoa(len(args))
	}
	if len(matchedKeys) > 0 {
		orClauses := []string{}
		for key := range matchedKeys {
			args = append(args, key[0], key[1])
			n := len(args)
			orClauses = append(orClauses, `(episode_num = $`+strconv.Itoa(n-1)+` AND order_num = $`+strconv.Itoa(n)+`)`)
		}
		storyboardWhere += ` AND (` + strings.Join(orClauses, " OR ") + `)`
	}
	rows, err := tx.Query(ctx, `SELECT id, project_id, episode_num, order_num, title, description, dialogue,
		 camera, duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
		 scene_code, scene_name, context_code, render_mode, script_id, script_segment_id, narration,
		 shot_type, current_version_id, created_at, updated_at
		 FROM storyboards WHERE `+storyboardWhere+` ORDER BY episode_num, order_num`, args...)
	if err != nil {
		return nil, fmt.Errorf("storyboard-video storyboards: %w", err)
	}
	var storyboards []*model.Storyboards
	for rows.Next() {
		var s model.Storyboards
		if err := rows.Scan(&s.ID, &s.ProjectId, &s.EpisodeNum, &s.OrderNum, &s.Title, &s.Description,
			&s.Dialogue, &s.Camera, &s.DurationSeconds, &s.Characters, &s.Keyframes, &s.MirrorShots,
			&s.Status, &s.StoryboardCode, &s.SceneCode, &s.SceneName, &s.ContextCode, &s.RenderMode,
			&s.ScriptId, &s.ScriptSegmentId, &s.Narration, &s.ShotType, &s.CurrentVersionId,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		storyboards = append(storyboards, &s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	assigneeID := in.AssigneeID
	if assigneeID == nil {
		first, err := r.firstArtistUserID(ctx, tx)
		if err != nil {
			return nil, err
		}
		if first != "" {
			assigneeID = &first
		}
	}
	created := 0
	taskPromptVersion := int32(1)
	for _, storyboard := range storyboards {
		var exists bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tasks
			WHERE project_id = $1 AND storyboard_id = $2 AND task_type = 'image_to_video' AND is_retired = false)`,
			projectID, storyboard.ID).Scan(&exists)
		if err != nil {
			return nil, fmt.Errorf("check existing video task: %w", err)
		}
		if exists {
			continue
		}
		sceneLabel := derefValue(in.SceneName)
		if sceneLabel == "" && sceneCodePtr != nil {
			sceneLabel = *sceneCodePtr
		}
		if sceneLabel == "" {
			sceneLabel = fmt.Sprintf("EP%02d", storyboard.EpisodeNum)
		}
		prompt := storyboardVideoPrompt(storyboard)
		task := &model.Tasks{
			ID:               newUUIDString(),
			ProjectId:        projectID,
			ScriptSegmentId:  storyboard.ScriptSegmentId,
			StoryboardId:     ptrToStr(storyboard.ID),
			TaskType:         taskTypeImageToVideo,
			Title:            fmt.Sprintf("%s 分镜 %03d 图生视频", sceneLabel, storyboard.OrderNum),
			AssigneeId:       assigneeID,
			AssignedBy:       ptrToStr(actor.ID),
			AssignedAt:       &now,
			Status:           taskStatusTodo,
			PromptText:       ptrToStr(prompt),
			LatestPromptText: ptrToStr(prompt),
			DueAt:            ptrToTime(now.Add(2 * 24 * time.Hour)),
			ProductionModel:  ptrToStr("Seedance"),
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := r.insertTask(ctx, tx, task); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO task_prompts (
			id, task_id, prompt_type, prompt_text, version_no, source, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			newUUIDString(), task.ID, "storyboard_video_initial", prompt, taskPromptVersion,
			"human_locked_breakdown", actor.ID)
		if err != nil {
			return nil, fmt.Errorf("insert video task prompt: %w", err)
		}
		created++
	}
	var taskCount int32
	err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id = $1 AND is_retired = false`, projectID).Scan(&taskCount)
	if err != nil {
		return nil, fmt.Errorf("count active tasks: %w", err)
	}
	return &StoryboardVideoTaskResult{
		ProjectID: projectID, EpisodeCode: episodeCode, SceneCode: sceneCodePtr,
		CreatedTasks: created, TaskCount: int(taskCount),
	}, nil
}

func derefValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func ptrToTime(v time.Time) *time.Time { return &v }

// storyboardKeyframePrompt 对齐 _storyboard_keyframe_prompt。
func storyboardKeyframePrompt(s *model.Storyboards) string {
	parts := []string{}
	if s.StoryboardCode != nil && *s.StoryboardCode != "" {
		parts = append(parts, "分镜："+*s.StoryboardCode)
	}
	if s.Description != "" {
		parts = append(parts, "画面："+s.Description)
	}
	if s.Camera != nil && *s.Camera != "" {
		parts = append(parts, "镜头："+*s.Camera)
	}
	if s.ShotType != nil && *s.ShotType != "" {
		parts = append(parts, "景别："+*s.ShotType)
	}
	if charActs := jsonStringSlice(s.Characters); len(charActs) > 0 {
		parts = append(parts, "角色："+strings.Join(charActs, "、"))
	}
	return strings.Join(parts, "\n")
}

// storyboardVideoPrompt 对齐 _storyboard_video_prompt。
func storyboardVideoPrompt(s *model.Storyboards) string {
	section := storyboardKeyframePrompt(s)
	shotItems := jsonMapList(s.MirrorShots)
	var mirrorLines []string
	for index, item := range shotItems {
		code := strings.TrimSpace(fmt.Sprintf("%v", firstNonNil(firstNonNil(item["id"], item["label"]), string(rune(64+index+1)))))
		params := strings.TrimSpace(joinNonEmpty(
			fmt.Sprintf("%v", item["shot_size"]),
			durationSecondsLabel(item["duration_seconds"]),
		))
		lines := []string{}
		codeLine := code
		if params != "" {
			codeLine += "（" + params + "）"
		}
		lines = append(lines, codeLine)
		if v, ok := item["description"]; ok && fmt.Sprintf("%v", v) != "" {
			lines = append(lines, "画面："+fmt.Sprintf("%v", v))
		}
		if v, ok := item["camera"]; ok && fmt.Sprintf("%v", v) != "" {
			lines = append(lines, "镜头："+fmt.Sprintf("%v", v))
		}
		if v, ok := item["characters"]; ok {
			names := anyToStringList(v)
			if len(names) > 0 {
				lines = append(lines, "角色："+strings.Join(names, "、"))
			}
		}
		if v, ok := item["dialogue"]; ok && fmt.Sprintf("%v", v) != "" {
			lines = append(lines, "台词："+fmt.Sprintf("%v", v))
		}
		mirrorLines = append(mirrorLines, strings.Join(lines, "\n"))
	}
	if len(mirrorLines) > 0 {
		section = strings.Join([]string{section, "镜中分镜（按顺序执行）：\n" + strings.Join(mirrorLines, "\n\n")}, "\n\n")
	}
	return section
}

func durationSecondsLabel(v any) string {
	s := fmt.Sprintf("%v", v)
	if s == "" || s == "<nil>" {
		return ""
	}
	return s + "秒"
}

// insertTask 插入 tasks 行（created_at/updated_at 由调用方填充，DB 无默认覆盖）。
func (r *Tasks) insertTask(ctx context.Context, tx queryer, t *model.Tasks) error {
	_, err := tx.Exec(ctx, `INSERT INTO tasks (
		id, project_id, episode_id, script_id, script_version_id, idempotency_key,
		script_segment_id, storyboard_id, asset_id, scene_code, scene_name, task_type, title,
		assignee_id, assigned_by, assigned_at, status, prompt_text, latest_prompt_text, due_at,
		completed_at, visible_until, production_model, media_type, task_variant, age_stage_code,
		costume_variant_code, variant_plan_id, variant_kind, variant_title_zh, variant_description_zh,
		prompt_revision_id, asset_context_outdated, depends_on_task_id, is_retired, retired_at,
		retired_by, retired_reason, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40)`,
		t.ID, t.ProjectId, t.EpisodeId, t.ScriptId, t.ScriptVersionId, t.IDempotencyKey,
		t.ScriptSegmentId, t.StoryboardId, t.AssetId, t.SceneCode, t.SceneName, t.TaskType, t.Title,
		t.AssigneeId, t.AssignedBy, t.AssignedAt, t.Status, t.PromptText, t.LatestPromptText, t.DueAt,
		t.CompletedAt, t.VisibleUntil, t.ProductionModel, t.MediaType, t.TaskVariant, t.AgeStageCode,
		t.CostumeVariantCode, t.VariantPlanId, t.VariantKind, t.VariantTitleZh, t.VariantDescriptionZh,
		t.PromptRevisionId, t.AssetContextOutdated, t.DependsOnTaskId, t.IsRetired, t.RetiredAt,
		t.RetiredBy, t.RetiredReason, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	return nil
}

func (r *Tasks) countActiveTasks(ctx context.Context, q queryer, projectID string) (int, error) {
	var n int32
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id = $1 AND is_retired = false`, projectID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active tasks: %w", err)
	}
	return int(n), nil
}
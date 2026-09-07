package repository

// P3e task 域提示词 + 更新路径（对齐 legacy repositories.py list_task_prompts /
// add_task_prompt / update_task / _add_task_reassignment_notifications）。
// 写路径接收 pgx.Tx，事务由服务层拥有；读路径接收 queryer。

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// ---- TaskPrompt 行 IO ----

const promptColumns = `id, task_id, prompt_type, prompt_text, version_no, source, copied_at, created_by, created_at, updated_at`

func scanPrompt(row pgx.Row) (*model.TaskPrompts, error) {
	var p model.TaskPrompts
	err := row.Scan(&p.ID, &p.TaskId, &p.PromptType, &p.PromptText, &p.VersionNo, &p.Source,
		&p.CopiedAt, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func scanPromptRows(rows pgx.Rows) ([]*model.TaskPrompts, error) {
	defer rows.Close()
	var out []*model.TaskPrompts
	for rows.Next() {
		p, err := scanPrompt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// promptToView 对齐 task_prompt_to_read（copied = bool(copied_at)）。
func promptToView(p *model.TaskPrompts) PromptView {
	return PromptView{
		ID: p.ID, TaskId: p.TaskId, PromptType: p.PromptType, PromptText: p.PromptText,
		Source: p.Source, Copied: p.CopiedAt != nil, CreatedBy: p.CreatedBy,
		VersionNo: p.VersionNo, CreatedAt: p.CreatedAt,
	}
}

// PromptToView 导出单条 prompt 视图（服务层写路径用）。
func PromptToView(p *model.TaskPrompts) PromptView { return promptToView(p) }

// ListTaskPrompts 对齐 list_task_prompts：仅活动任务；缺失/不活动返回空列表。
func (r *Tasks) ListTaskPrompts(ctx context.Context, q queryer, taskID string) ([]PromptView, error) {
	rows, err := q.Query(ctx, `SELECT `+promptColumns+` FROM task_prompts
		WHERE task_id = $1
		  AND EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND `+activeAssetTaskCondition()+`)
		ORDER BY version_no`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task prompts: %w", err)
	}
	items, err := scanPromptRows(rows)
	if err != nil {
		return nil, err
	}
	out := make([]PromptView, 0, len(items))
	for _, p := range items {
		if p != nil {
			out = append(out, promptToView(p))
		}
	}
	return out, nil
}

// AddTaskPrompt 对齐 add_task_prompt：version_no = max+1；更新 latest_prompt_text。
func (r *Tasks) AddTaskPrompt(ctx context.Context, tx pgx.Tx, taskID string, in TaskPromptCreate) (*model.TaskPrompts, error) {
	task, err := r.GetActiveAssetTask(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, notFoundError(taskID)
	}
	var maxNo int32
	err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_no), 0) FROM task_prompts WHERE task_id = $1`, taskID).Scan(&maxNo)
	if err != nil {
		return nil, fmt.Errorf("max task prompt version: %w", err)
	}
	now := time.Now().UTC()
	item := &model.TaskPrompts{
		ID:         newUUIDString(),
		TaskId:     taskID,
		PromptType: in.PromptType,
		PromptText: in.PromptText,
		VersionNo:  maxNo + 1,
		Source:     in.Source,
		CopiedAt:   nil,
		CreatedBy:  in.CreatedBy,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if in.Copied {
		item.CopiedAt = &now
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_prompts (
		id, task_id, prompt_type, prompt_text, version_no, source, copied_at, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		item.ID, item.TaskId, item.PromptType, item.PromptText, item.VersionNo, item.Source,
		item.CopiedAt, item.CreatedBy)
	if err != nil {
		return nil, fmt.Errorf("insert task prompt: %w", err)
	}
	task.LatestPromptText = ptrToStr(in.PromptText)
	task.UpdatedAt = now
	if err := persistTask(ctx, tx, task); err != nil {
		return nil, err
	}
	return item, nil
}

// TaskPromptCreate 对齐 TaskPromptCreate（schema 层默认值由 handler/service 填充）。
type TaskPromptCreate struct {
	PromptType string
	PromptText string
	Source     string
	Copied     bool
	CreatedBy  *string
}

func ptrToStr(v string) *string { return &v }

// ---- update_task ----

// TaskUpdateInput 对齐 TaskUpdate.model_dump(exclude_unset=True) 的三态语义：
// 各字段携带 Set 标志区分“未提供”与“显式 null”。reassignment_reason 仅消费不落库。
type TaskUpdateInput struct {
	Status             *string
	StatusSet          bool
	AssigneeID         *string
	AssigneeIDSet      bool
	ReassignmentReason *string
	LatestPromptText   *string
	LatestPromptSet    bool
	ProductionModel    *string
	ProductionModelSet bool
}

var protectedTaskStatuses = map[string]bool{
	taskStatusCompleted: true, taskStatusSubmitted: true, taskStatusReviewing: true,
}

// taskReassignment 一条（task, 旧执行人）改派对。
type taskReassignment struct {
	task      *model.Tasks
	oldAssignee string
}

// UpdateTask 对齐 update_task：完成守卫 + 级联改派 + 通知，事务内执行不提交。
func (r *Tasks) UpdateTask(ctx context.Context, tx pgx.Tx, taskID string, in TaskUpdateInput,
	actor TaskActor, now time.Time) (*model.Tasks, error) {
	task, err := r.GetActiveAssetTaskForUpdate(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, notFoundError(taskID)
	}
	canUpdate, err := r.canUpdateTask(ctx, tx, task, actor.ID, actor.Role)
	if err != nil {
		return nil, err
	}
	if !canUpdate {
		return nil, forbiddenError(taskID)
	}
	reason := ""
	if in.ReassignmentReason != nil {
		reason = strings.TrimSpace(*in.ReassignmentReason)
	}
	if in.StatusSet && in.Status != nil {
		if !isDirectorOrAdminRole(actor.Role) {
			return nil, forbiddenError(taskID)
		}
		if *in.Status == taskStatusCompleted && task.Status != taskStatusCompleted {
			var exists bool
			err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM submissions
				WHERE task_id = $1 AND status = $2 AND is_primary = true
				  AND is_archived = false AND is_invalidated = false)`,
				task.ID, submissionStatusPrimaryMaster).Scan(&exists)
			if err != nil {
				return nil, fmt.Errorf("check approved primary master: %w", err)
			}
			if !exists {
				return nil, valueError("任务必须先完成成果审核并指定主母版。")
			}
		}
		task.Status = *in.Status
		if *in.Status == taskStatusCompleted && task.CompletedAt == nil {
			task.CompletedAt = &now
			visible := now.Add(30 * 24 * time.Hour)
			task.VisibleUntil = &visible
		}
	}
	if in.AssigneeIDSet && isDirectorOrAdminRole(actor.Role) {
		newAssigneeID := in.AssigneeID
		if newAssigneeID != nil {
			assignee, err := r.userByID(ctx, tx, *newAssigneeID)
			if err != nil {
				return nil, err
			}
			if assignee == nil || !assignee.IsActive || isDirectorOrAdminRole(assignee.Role) {
				return nil, valueError("执行人无效、已停用或不是可分配的制作人员。")
			}
		}
		if !sameAssignee(task.AssigneeId, newAssigneeID) {
			if protectedTaskStatuses[task.Status] {
				return nil, valueError("待审核或已完成任务不能改派执行人。")
			}
		}
		assignmentTasks := []*model.Tasks{task}
		if task.StoryboardId != nil && (task.TaskType == taskTypeTextToImage || task.TaskType == taskTypeImageToVideo) {
			siblings, err := r.listTasksWhere(ctx, tx,
				`project_id = $1 AND episode_id = $2 AND storyboard_id = $3
				 AND task_type IN ('text_to_image','image_to_video') AND id <> $4 AND is_retired = false`,
				task.ProjectId, task.EpisodeId, task.StoryboardId, task.ID)
			if err != nil {
				return nil, err
			}
			assignmentTasks = append(assignmentTasks, siblings...)
		}
		if task.TaskType == taskTypeAsset && task.AssetId != nil {
			var assetType *string
			err := tx.QueryRow(ctx, `SELECT asset_type FROM assets WHERE id = $1`, *task.AssetId).Scan(&assetType)
			if errors.Is(err, pgx.ErrNoRows) {
				assetType = nil
			} else if err != nil {
				return nil, fmt.Errorf("read asset type: %w", err)
			}
			if assetType != nil && *assetType == assetTypeCharacter {
				siblings, err := r.listTasksWhere(ctx, tx,
					`project_id = $1 AND episode_id = $2 AND asset_id = $3 AND task_type = 'asset'
					 AND id <> $4 AND is_retired = false`,
					task.ProjectId, task.EpisodeId, task.AssetId, task.ID)
				if err != nil {
					return nil, err
				}
				assignmentTasks = append(assignmentTasks, siblings...)
			}
		}
		mutable := make([]*model.Tasks, 0, len(assignmentTasks))
		for _, item := range assignmentTasks {
			if item.ID == task.ID || !protectedTaskStatuses[item.Status] {
				mutable = append(mutable, item)
			}
		}
		reassignments := make([]taskReassignment, 0)
		for _, item := range mutable {
			if item.AssigneeId != nil && !sameAssignee(item.AssigneeId, newAssigneeID) {
				reassignments = append(reassignments, taskReassignment{task: item, oldAssignee: *item.AssigneeId})
			}
		}
		if len(reassignments) > 0 && reason == "" {
			return nil, valueError("改派任务必须填写改派原因。")
		}
		for _, item := range mutable {
			if sameAssignee(item.AssigneeId, newAssigneeID) {
				continue
			}
			item.AssigneeId = newAssigneeID
			item.AssignedBy = ptrToStr(actor.ID)
			if newAssigneeID != nil {
				item.AssignedAt = &now
			} else {
				item.AssignedAt = nil
			}
			item.UpdatedAt = now
		}
		if len(reassignments) > 0 {
			if err := r.addTaskReassignmentNotifications(ctx, tx, reassignments, newAssigneeID, reason); err != nil {
				return nil, err
			}
		}
	}
	if in.LatestPromptSet && in.LatestPromptText != nil {
		task.LatestPromptText = in.LatestPromptText
	}
	if in.ProductionModelSet {
		task.ProductionModel = in.ProductionModel
	}
	task.UpdatedAt = now
	if err := persistTask(ctx, tx, task); err != nil {
		return nil, err
	}
	return task, nil
}

// listTasksWhere 在调用方事务/连接上按片段查询非查询条件任务。
func (r *Tasks) listTasksWhere(ctx context.Context, q queryer, where string, args ...any) ([]*model.Tasks, error) {
	rows, err := q.Query(ctx, `SELECT `+taskColumns+` FROM tasks WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks where: %w", err)
	}
	defer rows.Close()
	return scanTaskRows(rows)
}

// userByID 事务/连接兼容用户查询（不存在返回 (nil, nil)）。
func (r *Tasks) userByID(ctx context.Context, q queryer, id string) (*model.Users, error) {
	u, err := scanUser(q.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return u, nil
}

// addTaskReassignmentNotifications 对齐 _add_task_reassignment_notifications（事务内插入通知）。
func (r *Tasks) addTaskReassignmentNotifications(ctx context.Context, tx pgx.Tx,
	reassignments []taskReassignment, newAssigneeID *string, reason string) error {
	if len(reassignments) == 0 {
		return nil
	}
	userIDs := []string{}
	for _, rr := range reassignments {
		userIDs = appendStrDedup(userIDs, rr.oldAssignee)
	}
	if newAssigneeID != nil {
		userIDs = appendStrDedup(userIDs, *newAssigneeID)
	}
	nameMap, err := r.userDisplayNameMap(ctx, tx, userIDs)
	if err != nil {
		return err
	}
	newAssigneeName := "待分配"
	if newAssigneeID != nil {
		if n, ok := nameMap[*newAssigneeID]; ok && n != "" {
			newAssigneeName = n
		}
	}
	grouped := map[string][]*model.Tasks{}
	for _, rr := range reassignments {
		grouped[rr.oldAssignee] = append(grouped[rr.oldAssignee], rr.task)
	}
	for oldID, tasks := range grouped {
		names := joinReassignmentTitles(tasks)
		if err := insertNotification(ctx, tx, oldID, "任务已改派",
			fmt.Sprintf("%s 已改派给 %s。改派原因：%s", names, newAssigneeName, reason),
			"task_reassigned_out"); err != nil {
			return err
		}
	}
	if newAssigneeID != nil {
		unique := map[string]*model.Tasks{}
		for _, rr := range reassignments {
			unique[rr.task.ID] = rr.task
		}
		uniqueTasks := make([]*model.Tasks, 0, len(unique))
		for _, t := range unique {
			uniqueTasks = append(uniqueTasks, t)
		}
		names := joinReassignmentTitles(uniqueTasks)
		if err := insertNotification(ctx, tx, *newAssigneeID, "收到改派任务",
			fmt.Sprintf("%s 已改派给你。改派原因：%s", names, reason),
			"task_reassigned_in"); err != nil {
			return err
		}
	}
	return nil
}

func joinReassignmentTitles(tasks []*model.Tasks) string {
	head := tasks
	if len(head) > 3 {
		head = head[:3]
	}
	parts := make([]string, 0, len(head))
	for _, t := range head {
		parts = append(parts, t.Title)
	}
	names := joinNonEmpty(parts...)
	if len(tasks) > 3 {
		return names + " 等 " + strconv.Itoa(len(tasks)) + " 项任务"
	}
	return names
}

func sameAssignee(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
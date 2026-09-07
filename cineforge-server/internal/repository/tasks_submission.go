package repository

// P3e task 域：submission 创建 / 编辑 / 软删 / 归档 / 换主母版。
// 对齐 legacy app/services/repositories.py:7959-8123 (add_submission)、8126-8156
// (update_submission_metadata)、8249-8284 (delete_draft_submission)、8797-8834
// (set_submission_archived)、8837-8904 (set_primary_submission)。
// 仓储变更函数在调用方事务（pgx.Tx）内执行，不 commit/rollback。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// submissionColumnsPrefixed 供 join 查询使用（避免歧义列）。
const submissionColumnsPrefixed = `s.id, s.task_id, s.batch_id, s.file_id, s.storyboard_id, s.file_path,
	s.file_type, s.step, s.prompt_text, s.original_prompt_text, s.revised_prompt_text, s.view_label,
	s.state_label, s.description, s.model_name, s.tool_names, s.submitted_by_id, s.status, s.is_selected,
	s.is_primary, s.is_archived, s.archived_by_id, s.archived_at, s.source_master_submission_id,
	s.is_invalidated, s.invalidated_at, s.invalidated_by_id, s.invalidated_reason,
	s.invalidated_source_id, s.purged_at, s.created_at, s.updated_at`

// ---- 入参结构（对齐 SubmissionCreate / SubmissionMetadataUpdate / 请求表单） ----

// SubmissionCreate 对齐 SubmissionCreate。
type SubmissionCreate struct {
	FileId             *string  `json:"file_id"`
	FilePath           string   `json:"file_path"`
	FileType           string   `json:"file_type"`
	Step               *string  `json:"step"`
	PromptText         *string  `json:"prompt_text"`
	OriginalPromptText *string  `json:"original_prompt_text"`
	RevisedPromptText  *string  `json:"revised_prompt_text"`
	ViewLabel          *string  `json:"view_label"`
	StateLabel         *string  `json:"state_label"`
	Description        *string  `json:"description"`
	ModelName          *string  `json:"model_name"`
	ToolNames          []string `json:"tool_names"`
	SubmittedById      *string  `json:"submitted_by_id"`
}

// SubmissionMetadataUpdate 对齐 SubmissionMetadataUpdate（exclude_unset 语义：字段带 *_set 才生效）。
type SubmissionMetadataUpdate struct {
	ViewLabel      *string
	ViewLabelSet   bool
	StateLabel     *string
	StateLabelSet  bool
	Description    *string
	DescriptionSet bool
}

// ---- 权限 / 步骤 / 字符母版助手 ----

// canUpdateTask 对齐 _can_update_task：assignee 本人或项目写角色。
func (r *Tasks) canUpdateTask(ctx context.Context, q queryer, t *model.Tasks, userID, role string) (bool, error) {
	if t.AssigneeId != nil && *t.AssigneeId == userID {
		return true, nil
	}
	return r.CanWriteProject(ctx, q, t.ProjectId, userID, role)
}

// canReviewProjectTasks 对齐 can_review_project_tasks：仅 director/admin。
func canReviewProjectTasks(role string) bool { return isDirectorOrAdminRole(role) }

// submissionStep 对齐 _submission_step（repositories.py:8159-8171）。
func submissionStep(t *model.Tasks, requested *string) (string, error) {
	step := strings.ToLower(strings.TrimSpace(ptrOr(requested, "")))
	switch t.TaskType {
	case taskTypeAudio:
		return "audio", nil
	case taskTypeStoryboardShot:
		if step != "keyframe" && step != "video" {
			return "", valueError("分镜任务必须标明 keyframe 或 video 步骤。")
		}
		return step, nil
	case taskTypeTextToImage:
		if t.StoryboardId != nil {
			return "keyframe", nil
		}
		return "result", nil
	case taskTypeImageToVideo, taskTypeVideoGeneration:
		if t.StoryboardId != nil {
			return "video", nil
		}
		return "result", nil
	default:
		return "result", nil
	}
}

// characterTaskRequiresMasterLineage 对齐 _character_task_requires_master_lineage
// （repositories.py:6819-6829）：character asset 的 B-E 图位需要 A 母版血缘。
func (r *Tasks) characterTaskRequiresMasterLineage(ctx context.Context, q queryer, t *model.Tasks) (bool, error) {
	variant := strings.ToUpper(strings.TrimSpace(ptrOr(t.TaskVariant, "")))
	if t.AssetId == nil || (t.TaskType != taskTypeAsset && t.TaskType != taskTypeTextToImage) ||
		variant == "" || variant == "A" || variant == "MASTER" {
		return false, nil
	}
	var assetType string
	err := q.QueryRow(ctx, `SELECT asset_type FROM assets WHERE id = $1`, *t.AssetId).Scan(&assetType)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("character lineage asset type: %w", err)
	}
	return assetType == assetTypeCharacter, nil
}

// currentCharacterMasterSubmission 对齐 _current_character_master_submission
// （repositories.py:6832-6861）：解析任务精确角色上下文下已过审的 A 主母版 submission。
func (r *Tasks) currentCharacterMasterSubmission(ctx context.Context, q queryer, t *model.Tasks) (*model.Submissions, error) {
	if t.AssetId == nil {
		return nil, nil
	}
	sub, err := scanSubmission(q.QueryRow(ctx,
		`SELECT `+submissionColumnsPrefixed+` FROM submissions s
		 JOIN tasks t ON t.id = s.task_id
		 JOIN assets a ON a.id = t.asset_id
		 WHERE t.project_id = $1 AND t.asset_id = $2
		   AND t.task_type IN ('`+taskTypeAsset+`','`+taskTypeTextToImage+`')
		   AND coalesce(t.age_stage_code,'') = $3
		   AND coalesce(t.costume_variant_code,'') = $4
		   AND upper(coalesce(t.task_variant,'MASTER')) IN ('A','MASTER')
		   AND t.status = '`+taskStatusCompleted+`' AND t.is_retired = false
		   AND a.asset_type = '`+assetTypeCharacter+`'
		   AND s.status = '`+submissionStatusPrimaryMaster+`'
		   AND s.is_selected = true AND s.is_primary = true
		   AND s.is_archived = false AND s.is_invalidated = false
		 ORDER BY s.created_at DESC, s.id DESC LIMIT 1`,
		t.ProjectId, *t.AssetId, ptrOr(t.AgeStageCode, ""), ptrOr(t.CostumeVariantCode, "")))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("current character master submission: %w", err)
	}
	return sub, nil
}

// ---- submission 行查询 ----

func (r *Tasks) findSubmissionByFileStep(ctx context.Context, q queryer, taskID, fileID, step string) (*model.Submissions, error) {
	sub, err := scanSubmission(q.QueryRow(ctx,
		`SELECT `+submissionColumns+` FROM submissions
		 WHERE task_id = $1 AND file_id = $2 AND coalesce(step,'result') = $3
		 ORDER BY created_at LIMIT 1`, taskID, fileID, step))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find submission by file step: %w", err)
	}
	return sub, nil
}

// findSubmissionByID 按 (task_id, submission_id) 查 submission；不存在返回 (nil, nil)。
func (r *Tasks) findSubmissionByID(ctx context.Context, q queryer, taskID, submissionID string) (*model.Submissions, error) {
	sub, err := scanSubmission(q.QueryRow(ctx,
		`SELECT `+submissionColumns+` FROM submissions WHERE id = $1 AND task_id = $2`,
		submissionID, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find submission by id: %w", err)
	}
	return sub, nil
}

func (r *Tasks) getSubmissionByID(ctx context.Context, q queryer, id string) (*model.Submissions, error) {
	sub, err := scanSubmission(q.QueryRow(ctx,
		`SELECT `+submissionColumns+` FROM submissions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get submission by id: %w", err)
	}
	return sub, nil
}

// ---- 批次行查询 ----

func (r *Tasks) maxBatchVersionNo(ctx context.Context, q queryer, taskID, step string) (int32, error) {
	var n int32
	if err := q.QueryRow(ctx,
		`SELECT coalesce(max(version_no),0) FROM task_submission_batches WHERE task_id = $1 AND step = $2`,
		taskID, step).Scan(&n); err != nil {
		return 0, fmt.Errorf("max batch version no: %w", err)
	}
	return n, nil
}

func (r *Tasks) findDraftBatch(ctx context.Context, q queryer, taskID, step string) (*model.TaskSubmissionBatches, error) {
	b, err := scanBatch(q.QueryRow(ctx,
		`SELECT `+batchColumns+` FROM task_submission_batches
		 WHERE task_id = $1 AND step = $2 AND status = '`+batchStatusDraft+`'
		 ORDER BY version_no DESC LIMIT 1`, taskID, step))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find draft batch: %w", err)
	}
	return b, nil
}

func (r *Tasks) getBatchByID(ctx context.Context, q queryer, batchID string) (*model.TaskSubmissionBatches, error) {
	b, err := scanBatch(q.QueryRow(ctx,
		`SELECT `+batchColumns+` FROM task_submission_batches WHERE id = $1`, batchID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get batch by id: %w", err)
	}
	return b, nil
}

// listBatchSubmissions 列出某批次的 submissions（按 created_at, id 排序，对齐 selectinload 顺序）。
func (r *Tasks) listBatchSubmissions(ctx context.Context, q queryer, batchID string) ([]*model.Submissions, error) {
	rows, err := q.Query(ctx,
		`SELECT `+submissionColumns+` FROM submissions WHERE batch_id = $1 ORDER BY created_at, id`, batchID)
	if err != nil {
		return nil, fmt.Errorf("list batch submissions: %w", err)
	}
	defer rows.Close()
	var out []*model.Submissions
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// listTaskSubmissions 列出某任务全部 submissions（对齐 selectinload(Task.submissions) 全量返回）。
func (r *Tasks) listTaskSubmissions(ctx context.Context, q queryer, taskID string) ([]*model.Submissions, error) {
	rows, err := q.Query(ctx,
		`SELECT `+submissionColumns+` FROM submissions WHERE task_id = $1 ORDER BY created_at, id`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task submissions: %w", err)
	}
	defer rows.Close()
	var out []*model.Submissions
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Tasks) listTaskBatches(ctx context.Context, q queryer, taskID string) ([]*model.TaskSubmissionBatches, error) {
	rows, err := q.Query(ctx,
		`SELECT `+batchColumns+` FROM task_submission_batches WHERE task_id = $1 ORDER BY version_no, created_at`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task batches: %w", err)
	}
	defer rows.Close()
	var out []*model.TaskSubmissionBatches
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ---- add_submission（repositories.py:7959-8123） ----

// AddSubmission 登记候选成果；幂等键 (task_id, file_id, step)。在调用方事务内执行。
func (r *Tasks) AddSubmission(ctx context.Context, tx pgx.Tx, taskID string, in SubmissionCreate,
	userID, role string) (*SubmissionView, error) {
	task, err := r.GetActiveAssetTask(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, notFoundError(taskID)
	}
	canUpdate, err := r.canUpdateTask(ctx, tx, task, userID, role)
	if err != nil {
		return nil, err
	}
	if !canUpdate {
		return nil, forbiddenError(taskID)
	}
	step, err := submissionStep(task, in.Step)
	if err != nil {
		return nil, err
	}
	if task.TaskType == taskTypeAudio && in.FileType != "audio" {
		return nil, valueError("音频任务只能登记 WAV 或 MP3 音频成果。")
	}
	// 乐观幂等检查：holding task row lock 前先查一次。
	if in.FileId != nil {
		existing, err := r.findSubmissionByFileStep(ctx, tx, task.ID, *in.FileId, step)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if existing.IsArchived || existing.IsInvalidated {
				return nil, valueError("该上传文件已被移除或作废，不能重新登记；请重新上传新文件。")
			}
			view := submissionToView(existing)
			return &view, nil
		}
	}
	// 同一任务并发上传需串行：加任务行锁后重查。
	task, err = r.GetActiveAssetTaskForUpdate(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, notFoundError(taskID)
	}
	if in.FileId != nil {
		existing, err := r.findSubmissionByFileStep(ctx, tx, task.ID, *in.FileId, step)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if existing.IsArchived || existing.IsInvalidated {
				return nil, valueError("该上传文件已被移除或作废，不能重新登记；请重新上传新文件。")
			}
			view := submissionToView(existing)
			return &view, nil
		}
	}
	if task.Status == taskStatusReviewing || task.Status == taskStatusSubmitted {
		return nil, valueError("当前任务已有待审核批次，审核完成前不能继续上传。")
	}
	if task.Status == taskStatusCompleted {
		return nil, valueError("已完成任务不能继续上传；如需换版，请先由导演发起返工。")
	}
	dependencyLocked, _, dependencyCodes, err := r.TaskDependencyState(ctx, tx, task)
	if err != nil {
		return nil, err
	}
	if dependencyLocked {
		labels := strings.Join(dependencyCodes, "、")
		if labels == "" {
			labels = "前置任务"
		}
		return nil, valueError(fmt.Sprintf("%s 尚未完成，当前任务暂未解锁。", labels))
	}
	var sourceMaster *model.Submissions
	requiresLineage, err := r.characterTaskRequiresMasterLineage(ctx, tx, task)
	if err != nil {
		return nil, err
	}
	if requiresLineage {
		sourceMaster, err = r.currentCharacterMasterSubmission(ctx, tx, task)
		if err != nil {
			return nil, err
		}
		if sourceMaster == nil {
			return nil, valueError("当前人物图位缺少有效的 A 身份母版，请先完成 A 图审批。")
		}
	}
	if task.StoryboardId != nil && (task.TaskType == taskTypeStoryboardShot ||
		task.TaskType == taskTypeTextToImage || task.TaskType == taskTypeImageToVideo) {
		sceneLocked, err := r.IsSceneLocked(ctx, tx, task.ProjectId, task.SceneCode, task.SceneName)
		if err != nil {
			return nil, err
		}
		if sceneLocked {
			return nil, valueError("该场景所需资产尚未全部完成，分镜任务暂未解锁，不能提交。")
		}
		if task.TaskType == taskTypeStoryboardShot && step == "video" {
			var keyframeID *string
			err := tx.QueryRow(ctx,
				`SELECT id FROM submissions
				 WHERE task_id = $1 AND step = 'keyframe' AND status = '`+submissionStatusPrimaryMaster+
					`' AND is_primary = true AND is_archived = false AND is_invalidated = false
				 LIMIT 1`, task.ID).Scan(&keyframeID)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && keyframeID == nil) {
				return nil, valueError("关键帧尚未审核定版，视频任务暂未解锁。")
			}
			if err != nil {
				return nil, fmt.Errorf("approved keyframe check: %w", err)
			}
		}
		if task.TaskType == taskTypeImageToVideo && task.DependsOnTaskId != nil {
			var depStatus string
			err := tx.QueryRow(ctx,
				`SELECT status FROM tasks WHERE id = $1 AND is_retired = false`,
				*task.DependsOnTaskId).Scan(&depStatus)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, valueError("关键帧任务尚未完成，视频任务暂未解锁。")
			}
			if err != nil {
				return nil, fmt.Errorf("keyframe task status: %w", err)
			}
			if depStatus != taskStatusCompleted {
				return nil, valueError("关键帧任务尚未完成，视频任务暂未解锁。")
			}
		}
	}
	if in.FileId != nil {
		var fileTaskID, fileProjectID *string
		err := tx.QueryRow(ctx,
			`SELECT task_id, project_id FROM files WHERE id = $1`, *in.FileId).Scan(&fileTaskID, &fileProjectID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, valueError("上传文件与当前任务不匹配。")
		}
		if err != nil {
			return nil, fmt.Errorf("file ownership check: %w", err)
		}
		if fileTaskID == nil || *fileTaskID != task.ID || fileProjectID == nil || *fileProjectID != task.ProjectId {
			return nil, valueError("上传文件与当前任务不匹配。")
		}
	}

	// 草稿批次：复用最新 draft；无则 version_no = max+1 新建。
	batch, err := r.findDraftBatch(ctx, tx, task.ID, step)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		latest, err := r.maxBatchVersionNo(ctx, tx, task.ID, step)
		if err != nil {
			return nil, err
		}
		submittedBy := ptrOr(in.SubmittedById, userID)
		batch = &model.TaskSubmissionBatches{
			ID:            newUUIDString(),
			TaskId:        task.ID,
			Step:          step,
			VersionNo:     latest + 1,
			Status:        batchStatusDraft,
			SubmittedById: &submittedBy,
		}
		if err := insertBatch(ctx, tx, batch); err != nil {
			return nil, err
		}
	}

	now := time.Now().UTC()
	submittedBy := ptrOr(in.SubmittedById, userID)
	var sourceMasterID *string
	if sourceMaster != nil {
		sourceMasterID = &sourceMaster.ID
	}
	toolNames, _ := json.Marshal(in.ToolNames)
	if toolNames == nil {
		toolNames = json.RawMessage(`[]`)
	}
	submission := &model.Submissions{
		ID:                       newUUIDString(),
		TaskId:                   task.ID,
		BatchId:                  &batch.ID,
		FileId:                   in.FileId,
		StoryboardId:             task.StoryboardId,
		FilePath:                 in.FilePath,
		FileType:                 in.FileType,
		Step:                     &step,
		PromptText:               in.PromptText,
		OriginalPromptText:       in.OriginalPromptText,
		RevisedPromptText:        in.RevisedPromptText,
		ViewLabel:                in.ViewLabel,
		StateLabel:               in.StateLabel,
		Description:              in.Description,
		ModelName:                in.ModelName,
		ToolNames:                toolNames,
		SubmittedById:            &submittedBy,
		Status:                   submissionStatusDraft,
		SourceMasterSubmissionId: sourceMasterID,
	}
	if err := insertSubmission(ctx, tx, submission); err != nil {
		return nil, err
	}

	finalPrompt := in.RevisedPromptText
	if finalPrompt == nil {
		finalPrompt = in.PromptText
	}
	if finalPrompt == nil {
		finalPrompt = in.OriginalPromptText
	}
	if finalPrompt == nil {
		finalPrompt = task.LatestPromptText
	}
	if finalPrompt != nil {
		task.LatestPromptText = finalPrompt
	}
	if in.ModelName != nil {
		task.ProductionModel = in.ModelName
	}
	if sourceMaster != nil {
		task.AssetContextOutdated = false
	}
	task.Status = taskStatusInProgress
	task.CompletedAt = nil
	task.VisibleUntil = nil
	task.UpdatedAt = now
	if err := persistTask(ctx, tx, task); err != nil {
		return nil, err
	}
	if err := insertOperationLog(ctx, tx, userID, &task.ProjectId, &submission.ID,
		targetTypeSubmission, opLogTaskSubmissionCreated, map[string]any{
			"task_id":   task.ID,
			"file_type": submission.FileType,
			"model_name": submission.ModelName,
		}); err != nil {
		return nil, err
	}

	created, err := r.getSubmissionByID(ctx, tx, submission.ID)
	if err != nil {
		return nil, err
	}
	if created == nil {
		created = submission
	}
	view := submissionToView(created)
	return &view, nil
}

// ---- update_submission_metadata（repositories.py:8126-8156） ----

// UpdateSubmissionMetadata 编辑草稿/已驳回成果的展示字段。在调用方事务内执行。
func (r *Tasks) UpdateSubmissionMetadata(ctx context.Context, tx pgx.Tx, taskID, submissionID string,
	in SubmissionMetadataUpdate, userID, role string) (*SubmissionView, error) {
	task, err := r.GetActiveAssetTask(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, notFoundError(taskID)
	}
	canUpdate, err := r.canUpdateTask(ctx, tx, task, userID, role)
	if err != nil {
		return nil, err
	}
	if !canUpdate {
		return nil, forbiddenError(taskID)
	}
	submission, err := r.findSubmissionByID(ctx, tx, taskID, submissionID)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, notFoundError(submissionID)
	}
	if submission.IsArchived || submission.IsInvalidated {
		return nil, valueError("已归档或已作废候选不能编辑说明。")
	}
	if submission.Status != submissionStatusDraft && submission.Status != submissionStatusRejected {
		return nil, valueError("只有未提交草稿或已驳回成果可以编辑说明。")
	}
	if in.ViewLabelSet {
		submission.ViewLabel = stripOrNil(in.ViewLabel)
	}
	if in.StateLabelSet {
		submission.StateLabel = stripOrNil(in.StateLabel)
	}
	if in.DescriptionSet {
		submission.Description = stripOrNil(in.Description)
	}
	now := time.Now().UTC()
	submission.UpdatedAt = now
	if err := persistSubmission(ctx, tx, submission); err != nil {
		return nil, err
	}
	view := submissionToView(submission)
	return &view, nil
}

// stripOrNil 对齐 str(value).strip() or None。
func stripOrNil(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// ---- delete_draft_submission（repositories.py:8249-8284） ----

// DeleteDraftSubmission 仅软删草稿/已驳回候选；返回 {"archived": "<submission_id>"}。
func (r *Tasks) DeleteDraftSubmission(ctx context.Context, tx pgx.Tx, taskID, submissionID string,
	userID, role string) (map[string]string, error) {
	task, err := r.GetActiveAssetTaskForUpdate(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, notFoundError(taskID)
	}
	canUpdate, err := r.canUpdateTask(ctx, tx, task, userID, role)
	if err != nil {
		return nil, err
	}
	if !canUpdate {
		return nil, forbiddenError(taskID)
	}
	submission, err := r.findSubmissionByID(ctx, tx, task.ID, submissionID)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, notFoundError(submissionID)
	}
	if submission.Status != submissionStatusDraft && submission.Status != submissionStatusRejected {
		return nil, valueError("只有未提交草稿或已驳回成果可以移除；待审核和已定版成果必须保留。")
	}
	if submission.IsArchived || submission.IsInvalidated {
		return map[string]string{"archived": submission.ID}, nil
	}
	now := time.Now().UTC()
	submission.IsArchived = true
	submission.ArchivedById = &userID
	submission.ArchivedAt = &now
	submission.UpdatedAt = now
	if err := persistSubmission(ctx, tx, submission); err != nil {
		return nil, err
	}
	if err := insertOperationLog(ctx, tx, userID, &task.ProjectId, &submission.ID,
		targetTypeSubmission, opLogTaskDraftSubmissionRemoved, map[string]any{
			"task_id":    task.ID,
			"soft_delete": true,
		}); err != nil {
		return nil, err
	}
	return map[string]string{"archived": submission.ID}, nil
}

// ---- set_submission_archived（repositories.py:8797-8834） ----

// SetSubmissionArchived 归档/恢复候选（导演/管理员）。在调用方事务内执行。
func (r *Tasks) SetSubmissionArchived(ctx context.Context, tx pgx.Tx, taskID, submissionID string,
	userID, role string, archived bool) (*SubmissionView, error) {
	task, err := r.GetActiveAssetTaskForUpdate(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, notFoundError(taskID)
	}
	if !canReviewProjectTasks(role) {
		return nil, forbiddenError(taskID)
	}
	submission, err := r.findSubmissionByID(ctx, tx, task.ID, submissionID)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, notFoundError(submissionID)
	}
	if submission.IsInvalidated {
		return nil, valueError("已作废成果仅保留审计记录，不能归档或恢复。")
	}
	if archived && submission.IsPrimary {
		return nil, valueError("主母版在被替换前不能归档。")
	}
	now := time.Now().UTC()
	submission.IsArchived = archived
	if archived {
		submission.ArchivedById = &userID
		submission.ArchivedAt = &now
	} else {
		submission.ArchivedById = nil
		submission.ArchivedAt = nil
	}
	submission.UpdatedAt = now
	if err := persistSubmission(ctx, tx, submission); err != nil {
		return nil, err
	}
	action := opLogTaskSubmissionArchived
	if !archived {
		action = opLogTaskSubmissionRestored
	}
	if err := insertOperationLog(ctx, tx, userID, &task.ProjectId, &submission.ID,
		targetTypeSubmission, action, map[string]any{
			"task_id":  task.ID,
			"archived": archived,
		}); err != nil {
		return nil, err
	}
	view := submissionToView(submission)
	return &view, nil
}

// ---- set_primary_submission（repositories.py:8837-8904） ----

// SetPrimarySubmission 指定主母版；触发 AssetVersion 物化与依赖刷新。返回前不读 TaskRead，
// 由服务层在 commit 后调用 GetTask。
func (r *Tasks) SetPrimarySubmission(ctx context.Context, tx pgx.Tx, taskID, submissionID string,
	userID, role string) error {
	task, err := r.GetActiveAssetTaskForUpdate(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return notFoundError(taskID)
	}
	if !canReviewProjectTasks(role) {
		return forbiddenError(taskID)
	}
	selected, err := r.findSubmissionByID(ctx, tx, task.ID, submissionID)
	if err != nil {
		return err
	}
	if selected == nil {
		return notFoundError(submissionID)
	}
	if selected.IsArchived || selected.IsInvalidated ||
		selected.Status == submissionStatusDraft || selected.Status == submissionStatusSubmitted ||
		selected.Status == submissionStatusRejected {
		return valueError("草稿、待审核、已驳回、已归档或已作废成果不能设为主母版。")
	}
	step := nonNil(selected.Step)
	if step == "" {
		step = "result"
	}
	now := time.Now().UTC()

	// 同 step 现有主母版全部降级（保持 is_selected）。
	existingPrimaries, err := r.listSubmissionsByStepPrimary(ctx, tx, task.ID, step, "")
	if err != nil {
		return err
	}
	demoted := make([]*model.Submissions, 0, len(existingPrimaries))
	for _, item := range existingPrimaries {
		item.IsPrimary = false
		item.IsSelected = true
		item.Status = submissionStatusAlternateMaster
		item.UpdatedAt = now
		if err := persistSubmission(ctx, tx, item); err != nil {
			return err
		}
		demoted = append(demoted, item)
	}

	selected.IsPrimary = true
	selected.IsSelected = true
	selected.Status = submissionStatusPrimaryMaster
	selected.UpdatedAt = now
	if err := persistSubmission(ctx, tx, selected); err != nil {
		return err
	}

	// 物化：现有主母版 + 新主母版。
	toMaterialize := append(demoted, selected)
	if err := r.materializeApprovedAssetVersions(ctx, tx, task, toMaterialize, &userID); err != nil {
		return err
	}

	// 直接依赖进入 context 失效。
	if err := markDependentTasksOutdated(ctx, tx, task.ID, now); err != nil {
		return err
	}

	if err := insertOperationLog(ctx, tx, userID, &task.ProjectId, &selected.ID,
		targetTypeSubmission, opLogTaskPrimarySubmissionChanged, map[string]any{
			"task_id":       task.ID,
			"submission_id": selected.ID,
		}); err != nil {
		return err
	}
	return nil
}

// listSubmissionsByStepPrimary 列出同 task+step 且 is_primary 的 submissions；
// excludeBatchID 非空时排除该批次（对齐 review approve 的 previous_primaries 过滤），空串不排除。
func (r *Tasks) listSubmissionsByStepPrimary(ctx context.Context, q queryer, taskID, step, excludeBatchID string) ([]*model.Submissions, error) {
	qSQL := `SELECT ` + submissionColumns + ` FROM submissions
		 WHERE task_id = $1 AND coalesce(step,'result') = $2 AND is_primary = true AND is_invalidated = false`
	args := []any{taskID, step}
	if excludeBatchID != "" {
		qSQL += ` AND (batch_id IS NULL OR batch_id <> $3)`
		args = append(args, excludeBatchID)
	}
	qSQL += ` ORDER BY created_at, id`
	rows, err := q.Query(ctx, qSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("list step primaries: %w", err)
	}
	defer rows.Close()
	var out []*model.Submissions
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// markDependentTasksOutdated 直接依赖（Task.depends_on_task_id + TaskDependency）进入
// asset_context_outdated（状态 in_progress/submitted/reviewing）。
func markDependentTasksOutdated(ctx context.Context, tx queryer, taskID string, now time.Time) error {
	depIDs, err := directDependentTaskIDs(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if len(depIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE tasks SET asset_context_outdated = true, updated_at = $2
		 WHERE id = ANY($1) AND status IN ('in_progress','submitted','reviewing') AND is_retired = false`,
		depIDs, now); err != nil {
		return fmt.Errorf("mark dependent tasks outdated: %w", err)
	}
	return nil
}

// directDependentTaskIDs 收集依赖本任务的直接依赖任务 id（两者关系全查）。
func directDependentTaskIDs(ctx context.Context, q queryer, taskID string) ([]string, error) {
	ids := []string{}
	seen := map[string]bool{}
	rows, err := q.Query(ctx,
		`SELECT id FROM tasks WHERE depends_on_task_id = $1 AND is_retired = false`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list direct dependents: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.Query(ctx,
		`SELECT task_id FROM task_dependencies WHERE depends_on_task_id = $1`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list dependency-graph dependents: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
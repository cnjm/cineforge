package repository

// P3e task 域并发批：submit / review / bulk（一套共享事务，all-or-nothing）。
// 对齐 legacy repositories.py 8174-8604 + tasks.py 路由错误映射 + workbench_rpc 语义。
// 幂等键：uuid5(NAMESPACE_URL, "task-submit:{request_id}:{task_id}:{step or 'result'}")、
// uuid5(NAMESPACE_URL, "task-review:{request_id}:{task_id}:{batch_id}")。

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// ---- 输入契约 ----

// TaskSubmitInput 对齐 TaskSubmitRequest。
type TaskSubmitInput struct {
	Step      *string // 原始请求 step（提交时由 submissionStep 归一化）
	RequestID *string // 幂等键
}

// TaskReviewInput 对齐 TaskReviewRequest。
type TaskReviewInput struct {
	Decision              string
	PrimarySubmissionID   *string
	SelectedSubmissionIDs []string
	Comment               *string
	RequestID             *string
}

// TaskBulkSubmitEntry 对齐 TaskBulkSubmitEntry。
type TaskBulkSubmitEntry struct {
	TaskID string
	Step   *string
}

// TaskBulkReviewEntry 对齐 TaskBulkReviewEntry。
type TaskBulkReviewEntry struct {
	TaskID                string
	BatchID               string
	PrimarySubmissionID   *string
	SelectedSubmissionIDs []string
}

// ---- uuid5（对齐 Python uuid.uuid5(NAMESPACE_URL, name)） ----

const uuidNamespaceURL = "6ba7b811-9dad-11d1-80b4-00c04fd430c8"

// uuid5Value 实现 SHA-1 版 UUID-5（与 Python 兼容的 canonical 字符串）。
func uuid5Value(name string) string {
	ns, _ := hex.DecodeString(strings.ReplaceAll(uuidNamespaceURL, "-", ""))
	h := sha1.New()
	h.Write(ns)
	h.Write([]byte(name))
	sum := h.Sum(nil)
	b := make([]byte, 16)
	copy(b, sum[:16])
	b[6] = (b[6] & 0x0f) | 0x50 // version 5
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// bulkSubmitKey 对齐 bulk 幂等键推导。
func bulkSubmitKey(requestID, taskID string, step *string) string {
	return uuid5Value(fmt.Sprintf("task-submit:%s:%s:%s", requestID, taskID, ptrOr(step, "result")))
}

// bulkReviewKey 对齐 bulk 幂等键推导。
func bulkReviewKey(requestID, taskID, batchID string) string {
	return uuid5Value(fmt.Sprintf("task-review:%s:%s:%s", requestID, taskID, batchID))
}

// ---- query 助手 ----

// findBatchBySubmitRequestID 对齐 submit request_id 幂等查询。
func (r *Tasks) findBatchBySubmitRequestID(ctx context.Context, q queryer, requestID string) (*model.TaskSubmissionBatches, error) {
	b, err := scanBatch(q.QueryRow(ctx,
		`SELECT `+batchColumns+` FROM task_submission_batches WHERE submit_request_id = $1`, requestID))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find batch by submit request id: %w", err)
	}
	return b, nil
}

// findBatchByReviewRequestID 对齐 review request_id 幂等查询。
func (r *Tasks) findBatchByReviewRequestID(ctx context.Context, q queryer, requestID string) (*model.TaskSubmissionBatches, error) {
	b, err := scanBatch(q.QueryRow(ctx,
		`SELECT `+batchColumns+` FROM task_submission_batches WHERE review_request_id = $1`, requestID))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find batch by review request id: %w", err)
	}
	return b, nil
}

// latestApprovedBatchIDByStep 查询 (task, step) 最新 approved 批次 id。
func (r *Tasks) latestApprovedBatchIDByStep(ctx context.Context, q queryer, taskID, step string) (*string, error) {
	var id string
	err := q.QueryRow(ctx,
		`SELECT id FROM task_submission_batches
		 WHERE task_id = $1 AND step = $2 AND status = '`+batchStatusApproved+`'
		 ORDER BY version_no DESC LIMIT 1`, taskID, step).Scan(&id)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest approved batch: %w", err)
	}
	return &id, nil
}

// ---- submit_task_batch（repositories.py:8174-8246） ----

// SubmitTaskBatch 将 (task, step) 最新 draft 批提交。返回 batch（service 在 commit 后转 view）。
func (r *Tasks) SubmitTaskBatch(ctx context.Context, tx pgx.Tx, taskID string, in TaskSubmitInput,
	userID, role string) (*model.TaskSubmissionBatches, error) {
	task, err := r.GetActiveAssetTaskForUpdate(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, notFoundError(taskID)
	}
	allowed, err := r.canUpdateTask(ctx, tx, task, userID, role)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, forbiddenError(taskID)
	}
	step, err := submissionStep(task, in.Step)
	if err != nil {
		return nil, err
	}
	if in.RequestID != nil && *in.RequestID != "" {
		existing, err := r.findBatchBySubmitRequestID(ctx, tx, *in.RequestID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if existing.TaskId != task.ID || existing.Step != step {
				return nil, valueError("提交请求标识已用于其他任务或生产步骤。")
			}
			// 幂等回放：直接返回既有批次。
			return existing, nil
		}
	}
	if task.Status == taskStatusReviewing || task.Status == taskStatusSubmitted || task.Status == taskStatusCompleted {
		return nil, valueError("当前任务状态不能重复提交。")
	}
	batch, err := r.latestDraftBatch(ctx, tx, task.ID, step)
	if err != nil {
		return nil, err
	}
	if batch == nil {
		return nil, valueError("请先上传至少一个候选成果。")
	}
	submissions, err := r.listBatchSubmissions(ctx, tx, batch.ID)
	if err != nil {
		return nil, err
	}
	if len(submissions) == 0 {
		return nil, valueError("请先上传至少一个候选成果。")
	}
	active := make([]*model.Submissions, 0, len(submissions))
	for _, item := range submissions {
		if !item.IsArchived && !item.IsInvalidated {
			active = append(active, item)
		}
	}
	if len(active) == 0 {
		return nil, valueError("当前批次没有可提交的候选成果。")
	}
	now := time.Now().UTC()
	batch.Status = batchStatusSubmitted
	batch.SubmittedById = &userID
	batch.SubmittedAt = &now
	batch.SubmitRequestId = nonNilStringPtr(in.RequestID)
	batch.UpdatedAt = now
	if err := persistBatch(ctx, tx, batch); err != nil {
		return nil, err
	}
	for _, submission := range active {
		submission.Status = submissionStatusSubmitted
		submission.UpdatedAt = now
		if err := persistSubmission(ctx, tx, submission); err != nil {
			return nil, err
		}
	}
	task.Status = taskStatusReviewing
	task.UpdatedAt = now
	if err := persistTask(ctx, tx, task); err != nil {
		return nil, err
	}
	if err := insertOperationLog(ctx, tx, userID, &task.ProjectId, &batch.ID,
		targetTypeTaskSubmissionBatch, opLogTaskBatchSubmitted, map[string]any{
			"task_id":    task.ID,
			"step":       batch.Step,
			"version_no": batch.VersionNo,
			"request_id": ptrOrStrToAny(in.RequestID),
		}); err != nil {
		return nil, err
	}
	return batch, nil
}

// latestDraftBatch 对齐 submit 的 draft 批查询（version_no DESC limit 1）。
func (r *Tasks) latestDraftBatch(ctx context.Context, q queryer, taskID, step string) (*model.TaskSubmissionBatches, error) {
	b, err := scanBatch(q.QueryRow(ctx,
		`SELECT `+batchColumns+` FROM task_submission_batches
		 WHERE task_id = $1 AND step = $2 AND status = '`+batchStatusDraft+`'
		 ORDER BY version_no DESC LIMIT 1`, taskID, step))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("latest draft batch: %w", err)
	}
	return b, nil
}

// ---- review_task_batch（repositories.py:8287-8527） ----

// ReviewTaskBatch 执行审批状态机（approve/rework）。不 commit/rollback，返回值仅用于服务层幂等回放标记。
func (r *Tasks) ReviewTaskBatch(ctx context.Context, tx pgx.Tx, taskID, batchID string, in TaskReviewInput,
	userID, role string) (replayed bool, err error) {
	task, err := r.GetActiveAssetTaskForUpdate(ctx, tx, taskID)
	if err != nil {
		return false, err
	}
	if task == nil {
		return false, notFoundError(taskID)
	}
	if !canReviewProjectTasks(role) {
		return false, forbiddenError(taskID)
	}
	batch, err := scanBatch(tx.QueryRow(ctx,
		`SELECT `+batchColumns+` FROM task_submission_batches
		 WHERE id = $1 AND task_id = $2 FOR UPDATE`, batchID, task.ID))
	if err == pgx.ErrNoRows {
		return false, notFoundError(batchID)
	}
	if err != nil {
		return false, fmt.Errorf("lock batch: %w", err)
	}
	batchSubmissions, err := r.listBatchSubmissions(ctx, tx, batch.ID)
	if err != nil {
		return false, err
	}
	if in.RequestID != nil && *in.RequestID != "" {
		existing, err := r.findBatchByReviewRequestID(ctx, tx, *in.RequestID)
		if err != nil {
			return false, err
		}
		if existing != nil {
			if existing.ID != batch.ID || existing.TaskId != task.ID {
				return false, valueError("审核请求标识已用于其他任务批次。")
			}
			return true, nil // 幂等回放
		}
	}
	previousBatchStatus := batch.Status
	switch {
	case in.Decision == "approve" && previousBatchStatus != batchStatusSubmitted:
		return false, valueError("只有待审核批次可以审核通过。")
	case in.Decision == "rework" && previousBatchStatus != batchStatusSubmitted && previousBatchStatus != batchStatusApproved:
		return false, valueError("只有待审核或已通过批次可以退回返工。")
	}
	reopeningApprovedBatch := in.Decision == "rework" && previousBatchStatus == batchStatusApproved
	if in.Decision == "rework" && strings.TrimSpace(ptrOr(in.Comment, "")) == "" {
		return false, valueError("退回返工时必须填写具体修改原因。")
	}
	if reopeningApprovedBatch {
		latest, err := r.latestApprovedBatchIDByStep(ctx, tx, task.ID, batch.Step)
		if err != nil {
			return false, err
		}
		if latest == nil || *latest != batch.ID {
			return false, valueError("只能退回当前步骤的最新已通过版本，历史版本仅供查看。")
		}
	}
	if batch.SubmittedById != nil && *batch.SubmittedById == userID {
		return false, valueError("审批人不能审核自己提交的成果。")
	}
	lockedScenesBefore := map[string]bool{}
	if task.TaskType == taskTypeAsset {
		gating, err := r.SceneGatingCore(ctx, tx, task.ProjectId)
		if err != nil {
			return false, err
		}
		for _, entry := range gating {
			if entry.Locked {
				lockedScenesBefore[entry.SceneCode] = true
			}
		}
	}
	var revokedMasterSubmissionID *string
	for _, item := range batchSubmissions {
		if item.IsPrimary {
			revokedMasterSubmissionID = &item.ID
			break
		}
	}
	previousReview := map[string]any{
		"reviewed_by_id":  ptrOrStrToAny(batch.ReviewedById),
		"reviewed_at":     isoFormatOrNil(batch.ReviewedAt),
		"review_comment":  batch.ReviewComment,
	}
	comment := strings.TrimSpace(ptrOr(in.Comment, ""))
	var commentPtr *string
	if comment != "" {
		commentPtr = &comment
	}
	now := time.Now().UTC()
	batch.ReviewedById = &userID
	batch.ReviewedAt = &now
	batch.ReviewComment = commentPtr
	batch.ReviewRequestId = nonNilStringPtr(in.RequestID)
	batch.UpdatedAt = now

	if in.Decision == "rework" {
		if err := r.applyRework(ctx, tx, task, batch, batchSubmissions, in, userID, now,
			reopeningApprovedBatch, revokedMasterSubmissionID, previousBatchStatus, previousReview); err != nil {
			return false, err
		}
	} else {
		if err := r.applyApprove(ctx, tx, task, batch, batchSubmissions, in, userID, now); err != nil {
			return false, err
		}
	}
	task.UpdatedAt = now
	if err := persistTask(ctx, tx, task); err != nil {
		return false, err
	}
	if err := persistBatch(ctx, tx, batch); err != nil {
		return false, err
	}
	if err := insertOperationLog(ctx, tx, userID, &task.ProjectId, &batch.ID,
		targetTypeTaskSubmissionBatch, opLogTaskBatchReviewed, map[string]any{
			"task_id":                task.ID,
			"decision":               in.Decision,
			"primary_submission_id":  ptrOrStrToAny(in.PrimarySubmissionID),
			"selected_submission_ids": in.SelectedSubmissionIDs,
			"comment":                in.Comment,
			"request_id":             ptrOrStrToAny(in.RequestID),
		}); err != nil {
		return false, err
	}
	if in.Decision == "approve" {
		if err := r.notifyUnlockedDependents(ctx, tx, task, lockedScenesBefore); err != nil {
			return false, err
		}
	}
	return false, nil
}

// applyRework 审批 rework 分支。
func (r *Tasks) applyRework(ctx context.Context, tx pgx.Tx, task *model.Tasks, batch *model.TaskSubmissionBatches,
	batchSubmissions []*model.Submissions, in TaskReviewInput, userID string, now time.Time,
	reopeningApprovedBatch bool, revokedMasterSubmissionID *string, previousBatchStatus string,
	previousReview map[string]any) error {
	batch.Status = batchStatusRework
	for _, submission := range batchSubmissions {
		submission.Status = submissionStatusRejected
		submission.IsSelected = false
		submission.IsPrimary = false
		submission.UpdatedAt = now
		if err := persistSubmission(ctx, tx, submission); err != nil {
			return err
		}
	}
	if reopeningApprovedBatch {
		if err := r.invalidateCharacterDependentViews(ctx, tx, task, &userID, now, revokedMasterSubmissionID); err != nil {
			return err
		}
		if err := r.revokeMaterializedAssetVersions(ctx, tx, task, batch, batchSubmissions, &userID, now, batch.ReviewComment); err != nil {
			return err
		}
		if err := insertOperationLog(ctx, tx, userID, &task.ProjectId, &batch.ID,
			targetTypeTaskSubmissionBatch, opLogTaskApprovalReopened, map[string]any{
				"task_id":          task.ID,
				"batch_id":         batch.ID,
				"batch_version_no": batch.VersionNo,
				"step":             batch.Step,
				"previous_status":  previousBatchStatus,
				"previous_review":  previousReview,
				"rework_comment":   batch.ReviewComment,
			}); err != nil {
			return err
		}
	}
	task.Status = taskStatusRejected
	task.CompletedAt = nil
	task.VisibleUntil = nil
	if task.TaskType == taskTypeAsset || task.TaskType == taskTypeAudio {
		if task.AssetId != nil {
			if err := r.refreshAssetCompletionStatus(ctx, tx, *task.AssetId, now); err != nil {
				return err
			}
		}
	}
	if reopeningApprovedBatch {
		depIDs, err := directDependentTaskIDs(ctx, tx, task.ID)
		if err != nil {
			return err
		}
		if len(depIDs) > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE tasks SET asset_context_outdated = true, updated_at = $2
				 WHERE id = ANY($1) AND status IN ('`+taskStatusInProgress+`','`+taskStatusSubmitted+`','`+taskStatusReviewing+`')
				 AND is_retired = false`, depIDs, now); err != nil {
				return fmt.Errorf("mark dependents outdated on rework: %w", err)
			}
		}
	}
	if task.AssigneeId != nil {
		content := task.Title + "：" + ptrOr(batch.ReviewComment, "请根据审核意见重新提交候选成果。")
		if err := insertNotification(ctx, tx, *task.AssigneeId, "任务已退回返工", content,
			notificationTypeTaskRework); err != nil {
			return err
		}
	}
	return nil
}

// applyApprove 审批 approve 分支。
func (r *Tasks) applyApprove(ctx context.Context, tx pgx.Tx, task *model.Tasks, batch *model.TaskSubmissionBatches,
	batchSubmissions []*model.Submissions, in TaskReviewInput, userID string, now time.Time) error {
	selectedIDs := make([]string, 0, len(in.SelectedSubmissionIDs))
	seen := map[string]bool{}
	for _, id := range in.SelectedSubmissionIDs {
		if !seen[id] {
			seen[id] = true
			selectedIDs = append(selectedIDs, id)
		}
	}
	if len(selectedIDs) == 0 {
		return valueError("审核通过时必须选择至少一个定版成果。")
	}
	var reviewedAsset *model.Assets
	if task.AssetId != nil {
		a, err := r.getAssetByID(ctx, tx, *task.AssetId)
		if err != nil {
			return err
		}
		reviewedAsset = a
	}
	variantUpper := strings.ToUpper(strings.TrimSpace(ptrOr(task.TaskVariant, "MASTER")))
	requiresPrimary := reviewedAsset != nil && reviewedAsset.AssetType == assetTypeCharacter &&
		(variantUpper == "A" || variantUpper == "MASTER")
	requiresMasterLineage := reviewedAsset != nil && reviewedAsset.AssetType == assetTypeCharacter &&
		variantUpper != "A" && variantUpper != "MASTER"
	if requiresPrimary && in.PrimarySubmissionID == nil {
		return valueError("人物定装审核通过时必须指定主母版。")
	}
	if in.PrimarySubmissionID != nil {
		found := false
		for _, id := range selectedIDs {
			if id == *in.PrimarySubmissionID {
				found = true
				break
			}
		}
		if !found {
			return valueError("主母版必须包含在定版成果中。")
		}
	}
	batchSubmissionIDs := map[string]bool{}
	for _, item := range batchSubmissions {
		if !item.IsArchived && !item.IsInvalidated {
			batchSubmissionIDs[item.ID] = true
		}
	}
	for _, id := range selectedIDs {
		if !batchSubmissionIDs[id] {
			return valueError("定版成果必须属于当前待审核批次。")
		}
	}
	if requiresMasterLineage {
		currentMaster, err := r.currentCharacterMasterSubmission(ctx, tx, task)
		if err != nil {
			return err
		}
		if currentMaster == nil || !r.allSelectedMatchMaster(ctx, tx, batchSubmissions, selectedIDs, *currentMaster) {
			return valueError("A 身份母版已变更，当前 B-E 成果已失效，请重新生产并提交。")
		}
	}
	batch.Status = batchStatusApproved
	// 其他批次/无批次的历史主母版降级。
	previousPrimaries, err := r.listSubmissionsByStepPrimary(ctx, tx, task.ID, batch.Step, batch.ID)
	if err != nil {
		return err
	}
	for _, submission := range previousPrimaries {
		submission.IsPrimary = false
		submission.IsSelected = true
		submission.Status = submissionStatusAlternateMaster
		submission.UpdatedAt = now
		if err := persistSubmission(ctx, tx, submission); err != nil {
			return err
		}
	}
	for _, submission := range batchSubmissions {
		isSelected := containsStr(selectedIDs, submission.ID)
		isPrimary := in.PrimarySubmissionID != nil && *in.PrimarySubmissionID == submission.ID
		switch {
		case isPrimary:
			submission.Status = submissionStatusPrimaryMaster
		case isSelected:
			submission.Status = submissionStatusAlternateMaster
		default:
			submission.Status = submissionStatusNotSelected
		}
		submission.IsSelected = isSelected
		submission.IsPrimary = isPrimary
		submission.UpdatedAt = now
		if err := persistSubmission(ctx, tx, submission); err != nil {
			return err
		}
	}
	if err := r.materializeApprovedAssetVersions(ctx, tx, task, batchSubmissions, &userID); err != nil {
		return err
	}
	if task.TaskType == taskTypeStoryboardShot && batch.Step == "keyframe" {
		task.Status = taskStatusInProgress
		task.CompletedAt = nil
		task.VisibleUntil = nil
	} else {
		task.Status = taskStatusCompleted
		task.CompletedAt = &now
		visible := now.AddDate(0, 0, 30)
		task.VisibleUntil = &visible
	}
	if task.TaskType == taskTypeAsset || task.TaskType == taskTypeAudio {
		if task.AssetId != nil {
			if err := r.refreshAssetCompletionStatus(ctx, tx, *task.AssetId, now); err != nil {
				return err
			}
		}
	}
	if task.AssigneeId != nil {
		if err := insertNotification(ctx, tx, *task.AssigneeId, "成果已审核定版",
			fmt.Sprintf("%s 已通过审核，主母版已确定。", task.Title),
			notificationTypeTaskApproved); err != nil {
			return err
		}
	}
	return nil
}

// allSelectedMatchMaster 对齐 requires_master_lineage 校验：
// 每个选定成果的 source_master_submission_id 必须等于当前 A 母版。
func (r *Tasks) allSelectedMatchMaster(ctx context.Context, q queryer, batchSubmissions []*model.Submissions,
	selectedIDs []string, master model.Submissions) bool {
	for _, item := range batchSubmissions {
		if !containsStr(selectedIDs, item.ID) {
			continue
		}
		if item.SourceMasterSubmissionId == nil || *item.SourceMasterSubmissionId != master.ID {
			return false
		}
	}
	return true
}

// ---- bulk（repositories.py:8530-8604） ----

// BulkSubmitTaskBatches 单事务批量提交：重复任务/A 女性隔离校验前置，逐项幂等键提交。
func (r *Tasks) BulkSubmitTaskBatches(ctx context.Context, tx pgx.Tx, requestID string,
	entries []TaskBulkSubmitEntry, userID, role string) ([]*model.TaskSubmissionBatches, error) {
	taskIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		taskIDs = append(taskIDs, entry.TaskID)
	}
	if hasDuplicates(taskIDs) {
		return nil, valueError("批量提交不能包含重复任务。")
	}
	variantSet, err := r.bulkVariantIntersectsA(ctx, tx, taskIDs)
	if err != nil {
		return nil, err
	}
	if len(taskIDs) > 1 && variantSet {
		return nil, valueError("人物 A 身份母版必须独立提交，不能与 B-E 合并操作。")
	}
	results := make([]*model.TaskSubmissionBatches, 0, len(entries))
	for _, entry := range entries {
		key := bulkSubmitKey(requestID, entry.TaskID, entry.Step)
		batch, err := r.SubmitTaskBatch(ctx, tx, entry.TaskID, TaskSubmitInput{
			Step:      entry.Step,
			RequestID: &key,
		}, userID, role)
		if err != nil {
			return nil, err
		}
		results = append(results, batch)
	}
	return results, nil
}

// BulkReviewTaskBatches 单事务批量审核（all-or-nothing）。
func (r *Tasks) BulkReviewTaskBatches(ctx context.Context, tx pgx.Tx, requestID, decision string,
	comment *string, entries []TaskBulkReviewEntry, userID, role string) error {
	taskIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		taskIDs = append(taskIDs, entry.TaskID)
	}
	if hasDuplicates(taskIDs) {
		return valueError("批量审核不能包含重复任务。")
	}
	variantSet, err := r.bulkVariantIntersectsA(ctx, tx, taskIDs)
	if err != nil {
		return err
	}
	if len(taskIDs) > 1 && variantSet {
		return valueError("人物 A 身份母版必须独立审核，不能与 B-E 合并操作。")
	}
	for _, entry := range entries {
		var primary *string
		var selected []string
		if decision == "approve" {
			primary = entry.PrimarySubmissionID
			selected = entry.SelectedSubmissionIDs
		}
		key := bulkReviewKey(requestID, entry.TaskID, entry.BatchID)
		if _, err := r.ReviewTaskBatch(ctx, tx, entry.TaskID, entry.BatchID, TaskReviewInput{
			Decision:              decision,
			PrimarySubmissionID:   primary,
			SelectedSubmissionIDs: selected,
			Comment:               comment,
			RequestID:             &key,
		}, userID, role); err != nil {
			return err
		}
	}
	return nil
}

// bulkVariantIntersectsA 加载批量任务并判定是否存在 A/MASTER 变体；缺失抛 KeyError("task")。
func (r *Tasks) bulkVariantIntersectsA(ctx context.Context, tx pgx.Tx, taskIDs []string) (bool, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, coalesce(task_variant,'') FROM tasks WHERE id = ANY($1) AND `+activeAssetTaskCondition(), taskIDs)
	if err != nil {
		return false, fmt.Errorf("load bulk tasks: %w", err)
	}
	defer rows.Close()
	found := map[string]bool{}
	intersects := false
	for rows.Next() {
		var id, variant string
		if err := rows.Scan(&id, &variant); err != nil {
			return false, err
		}
		found[id] = true
		if v := strings.ToUpper(strings.TrimSpace(variant)); v == "A" || v == "MASTER" {
			intersects = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(found) != len(taskIDs) {
		return false, notFoundError("task")
	}
	return intersects, nil
}

// ---- 小工具 ----

func hasDuplicates(items []string) bool {
	seen := map[string]bool{}
	for _, item := range items {
		if seen[item] {
			return true
		}
		seen[item] = true
	}
	return false
}

// nonNilStringPtr 将 *string 转为可写 SQL 值（nil → nil，非空 → 解引用）。
func nonNilStringPtr(v *string) *string {
	if v != nil && *v != "" {
		return v
	}
	return nil
}

// ptrOrStrToAny 将 *string 转为 map 值（nil → nil）。
func ptrOrStrToAny(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

// isoFormatOrNil 将 *time.Time 转为 isoformat 字符串（nil → nil）。
func isoFormatOrNil(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.UTC().Format("2006-01-02T15:04:05Z07:00")
}
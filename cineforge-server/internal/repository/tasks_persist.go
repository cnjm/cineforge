package repository

// P3e task 域持久化助手：仓储变更函数接收 pgx.Tx，不自行 commit/rollback；
// 这里提供整行 UPDATE / INSERT 助手与 tx-aware 审计/通知插入。

import (
	"context"
	"encoding/json"
	"fmt"

	"cineforge/server/internal/model"
)

// ---- operation_logs / notifications（tx-aware，服务层拥有事务） ----

// insertOperationLog 在调用方事务内追加 audit 行（对齐 legacy OperationLog 插入的字段）。
func insertOperationLog(ctx context.Context, q queryer, operatorID string, projectID, targetID *string,
	targetType, action string, detail any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, err = q.Exec(ctx,
		`INSERT INTO operation_logs (id, operator_id, project_id, target_type, target_id, action, detail)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		newUUIDString(), operatorID, projectID, targetType, targetID, action, raw)
	if err != nil {
		return fmt.Errorf("insert operation log: %w", err)
	}
	return nil
}

// insertNotification 在调用方事务内追加通知行。
// is_read 列没有 DB server default；legacy 在 ORM 层用 default=False 兜底，这里显式写 false。
func insertNotification(ctx context.Context, q queryer, userID, title, content, ntype string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO notifications (id, user_id, title, content, type, is_read)
		 VALUES ($1,$2,$3,$4,$5,false)`,
		newUUIDString(), userID, title, content, ntype)
	if err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}
	return nil
}

// InsertOperationLog tx-aware 审计插入（服务层任务域写路径在同一事务内写入 audit）。
func (r *Tasks) InsertOperationLog(ctx context.Context, q queryer, operatorID string,
	projectID, targetID *string, targetType, action string, detail any) error {
	return insertOperationLog(ctx, q, operatorID, projectID, targetID, targetType, action, detail)
}

// ---- 整行持久化助手（调用方已持有完整行；仅写变动的列语义由列清单保证） ----

// persistTask 整行 UPDATE tasks。
func persistTask(ctx context.Context, tx queryer, t *model.Tasks) error {
	_, err := tx.Exec(ctx, `UPDATE tasks SET
		episode_id=$2, script_id=$3, script_version_id=$4, idempotency_key=$5,
		script_segment_id=$6, storyboard_id=$7, asset_id=$8, scene_code=$9, scene_name=$10,
		task_type=$11, title=$12, assignee_id=$13, assigned_by=$14, assigned_at=$15, status=$16,
		prompt_text=$17, latest_prompt_text=$18, due_at=$19, completed_at=$20, visible_until=$21,
		production_model=$22, media_type=$23, task_variant=$24, age_stage_code=$25, costume_variant_code=$26,
		variant_plan_id=$27, variant_kind=$28, variant_title_zh=$29, variant_description_zh=$30,
		prompt_revision_id=$31, asset_context_outdated=$32, depends_on_task_id=$33, is_retired=$34,
		retired_at=$35, retired_by=$36, retired_reason=$37, updated_at=$38
		WHERE id=$1`,
		t.ID, t.EpisodeId, t.ScriptId, t.ScriptVersionId, t.IDempotencyKey,
		t.ScriptSegmentId, t.StoryboardId, t.AssetId, t.SceneCode, t.SceneName,
		t.TaskType, t.Title, t.AssigneeId, t.AssignedBy, t.AssignedAt, t.Status,
		t.PromptText, t.LatestPromptText, t.DueAt, t.CompletedAt, t.VisibleUntil,
		t.ProductionModel, t.MediaType, t.TaskVariant, t.AgeStageCode, t.CostumeVariantCode,
		t.VariantPlanId, t.VariantKind, t.VariantTitleZh, t.VariantDescriptionZh,
		t.PromptRevisionId, t.AssetContextOutdated, t.DependsOnTaskId, t.IsRetired,
		t.RetiredAt, t.RetiredBy, t.RetiredReason, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("persist task: %w", err)
	}
	return nil
}

// persistSubmission 整行 UPDATE submissions。
func persistSubmission(ctx context.Context, tx queryer, s *model.Submissions) error {
	_, err := tx.Exec(ctx, `UPDATE submissions SET
		task_id=$2, batch_id=$3, file_id=$4, storyboard_id=$5, file_path=$6,
		file_type=$7, step=$8, prompt_text=$9, original_prompt_text=$10, revised_prompt_text=$11,
		view_label=$12, state_label=$13, description=$14, model_name=$15, tool_names=$16,
		submitted_by_id=$17, status=$18, is_selected=$19, is_primary=$20, is_archived=$21,
		archived_by_id=$22, archived_at=$23, source_master_submission_id=$24, is_invalidated=$25,
		invalidated_at=$26, invalidated_by_id=$27, invalidated_reason=$28, invalidated_source_id=$29,
		purged_at=$30, updated_at=$31
		WHERE id=$1`,
		s.ID, s.TaskId, s.BatchId, s.FileId, s.StoryboardId, s.FilePath,
		s.FileType, s.Step, s.PromptText, s.OriginalPromptText, s.RevisedPromptText,
		s.ViewLabel, s.StateLabel, s.Description, s.ModelName, s.ToolNames,
		s.SubmittedById, s.Status, s.IsSelected, s.IsPrimary, s.IsArchived,
		s.ArchivedById, s.ArchivedAt, s.SourceMasterSubmissionId, s.IsInvalidated,
		s.InvalidatedAt, s.InvalidatedById, s.InvalidatedReason, s.InvalidatedSourceId,
		s.PurgedAt, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("persist submission: %w", err)
	}
	return nil
}

// insertSubmission 插入 submissions 行（created_at/updated_at 走 DB 默认）。
func insertSubmission(ctx context.Context, tx queryer, s *model.Submissions) error {
	_, err := tx.Exec(ctx, `INSERT INTO submissions (
		id, task_id, batch_id, file_id, storyboard_id, file_path, file_type, step,
		prompt_text, original_prompt_text, revised_prompt_text, view_label, state_label, description,
		model_name, tool_names, submitted_by_id, status, is_selected, is_primary, is_archived,
		archived_by_id, archived_at, source_master_submission_id, is_invalidated, invalidated_at,
		invalidated_by_id, invalidated_reason, invalidated_source_id, purged_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)`,
		s.ID, s.TaskId, s.BatchId, s.FileId, s.StoryboardId, s.FilePath, s.FileType, s.Step,
		s.PromptText, s.OriginalPromptText, s.RevisedPromptText, s.ViewLabel, s.StateLabel,
		s.Description, s.ModelName, s.ToolNames, s.SubmittedById, s.Status, s.IsSelected,
		s.IsPrimary, s.IsArchived, s.ArchivedById, s.ArchivedAt, s.SourceMasterSubmissionId,
		s.IsInvalidated, s.InvalidatedAt, s.InvalidatedById, s.InvalidatedReason,
		s.InvalidatedSourceId, s.PurgedAt)
	if err != nil {
		return fmt.Errorf("insert submission: %w", err)
	}
	return nil
}

// insertBatch 插入 task_submission_batches 行。
func insertBatch(ctx context.Context, tx queryer, b *model.TaskSubmissionBatches) error {
	_, err := tx.Exec(ctx, `INSERT INTO task_submission_batches (
		id, task_id, step, version_no, status, submitted_by_id, submitted_at,
		reviewed_by_id, reviewed_at, review_comment, submit_request_id, review_request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		b.ID, b.TaskId, b.Step, b.VersionNo, b.Status, b.SubmittedById, b.SubmittedAt,
		b.ReviewedById, b.ReviewedAt, b.ReviewComment, b.SubmitRequestId, b.ReviewRequestId)
	if err != nil {
		return fmt.Errorf("insert batch: %w", err)
	}
	return nil
}

// persistBatch 整行 UPDATE task_submission_batches。
func persistBatch(ctx context.Context, tx queryer, b *model.TaskSubmissionBatches) error {
	_, err := tx.Exec(ctx, `UPDATE task_submission_batches SET
		task_id=$2, step=$3, version_no=$4, status=$5,
		submitted_by_id=$6, submitted_at=$7, reviewed_by_id=$8, reviewed_at=$9, review_comment=$10,
		submit_request_id=$11, review_request_id=$12, updated_at=$13
		WHERE id=$1`,
		b.ID, b.TaskId, b.Step, b.VersionNo, b.Status, b.SubmittedById, b.SubmittedAt,
		b.ReviewedById, b.ReviewedAt, b.ReviewComment, b.SubmitRequestId, b.ReviewRequestId, b.UpdatedAt)
	if err != nil {
		return fmt.Errorf("persist batch: %w", err)
	}
	return nil
}

// insertAssetVersion 插入 asset_versions 行。
func insertAssetVersion(ctx context.Context, tx queryer, v *model.AssetVersions) error {
	_, err := tx.Exec(ctx, `INSERT INTO asset_versions (
		id, asset_id, version_no, asset_version_code, file_id, preview_file_id, prompt_text,
		negative_prompt_text, base_model, tool_name, metadata_json, is_current, source_task_id,
		source_agent_run_id, created_by, source_submission_id, source_master_submission_id,
		is_invalidated, invalidated_at, invalidated_by_id, invalidated_reason, invalidated_source_id,
		purged_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
		v.ID, v.AssetId, v.VersionNo, v.AssetVersionCode, v.FileId, v.PreviewFileId, v.PromptText,
		v.NegativePromptText, v.BaseModel, v.ToolName, v.MetadataJson, v.IsCurrent, v.SourceTaskId,
		v.SourceAgentRunId, v.CreatedBy, v.SourceSubmissionId, v.SourceMasterSubmissionId,
		v.IsInvalidated, v.InvalidatedAt, v.InvalidatedById, v.InvalidatedReason,
		v.InvalidatedSourceId, v.PurgedAt)
	if err != nil {
		return fmt.Errorf("insert asset version: %w", err)
	}
	return nil
}

// persistAssetVersion 整行 UPDATE asset_versions。
func persistAssetVersion(ctx context.Context, tx queryer, v *model.AssetVersions) error {
	_, err := tx.Exec(ctx, `UPDATE asset_versions SET
		asset_id=$2, version_no=$3, asset_version_code=$4, file_id=$5, preview_file_id=$6,
		prompt_text=$7, negative_prompt_text=$8, base_model=$9, tool_name=$10, metadata_json=$11,
		is_current=$12, source_task_id=$13, source_agent_run_id=$14, created_by=$15,
		source_submission_id=$16, source_master_submission_id=$17, is_invalidated=$18,
		invalidated_at=$19, invalidated_by_id=$20, invalidated_reason=$21, invalidated_source_id=$22,
		purged_at=$23, updated_at=$24
		WHERE id=$1`,
		v.ID, v.AssetId, v.VersionNo, v.AssetVersionCode, v.FileId, v.PreviewFileId, v.PromptText,
		v.NegativePromptText, v.BaseModel, v.ToolName, v.MetadataJson, v.IsCurrent, v.SourceTaskId,
		v.SourceAgentRunId, v.CreatedBy, v.SourceSubmissionId, v.SourceMasterSubmissionId,
		v.IsInvalidated, v.InvalidatedAt, v.InvalidatedById, v.InvalidatedReason,
		v.InvalidatedSourceId, v.PurgedAt, v.UpdatedAt)
	if err != nil {
		return fmt.Errorf("persist asset version: %w", err)
	}
	return nil
}

// persistAsset 整行 UPDATE assets（project_id 不变）。
func persistAsset(ctx context.Context, tx queryer, a *model.Assets) error {
	_, err := tx.Exec(ctx, `UPDATE assets SET
		asset_code=$2, asset_type=$3, name=$4, description=$5, status=$6, tags=$7,
		preview_path=$8, file_path=$9, prompt_text=$10, base_model=$11, metadata_json=$12,
		version=$13, current_version_id=$14, current_revision_id=$15, is_locked=$16,
		locked_by=$17, locked_at=$18, created_by_id=$19, updated_at=$20
		WHERE id=$1`,
		a.ID, a.AssetCode, a.AssetType, a.Name, a.Description, a.Status, a.Tags,
		a.PreviewPath, a.FilePath, a.PromptText, a.BaseModel, a.MetadataJson, a.Version,
		a.CurrentVersionId, a.CurrentRevisionId, a.IsLocked, a.LockedBy, a.LockedAt,
		a.CreatedById, a.UpdatedAt)
	if err != nil {
		return fmt.Errorf("persist asset: %w", err)
	}
	return nil
}
// Code generated from cineforge single-db schema (00001 migration). DO NOT EDIT manually;
// regenerate via scripts if needed. Column → Go field mapping is mechanical.
package model

import (
	"encoding/json"
	"time"
)

type Tasks struct {
	ID                   string     `db:"id" json:"id"`
	ProjectId            string     `db:"project_id" json:"project_id"`
	EpisodeId            *string    `db:"episode_id" json:"episode_id"`
	ScriptId             *string    `db:"script_id" json:"script_id"`
	ScriptVersionId      *string    `db:"script_version_id" json:"script_version_id"`
	IDempotencyKey       *string    `db:"idempotency_key" json:"idempotency_key"`
	ScriptSegmentId      *string    `db:"script_segment_id" json:"script_segment_id"`
	StoryboardId         *string    `db:"storyboard_id" json:"storyboard_id"`
	AssetId              *string    `db:"asset_id" json:"asset_id"`
	SceneCode            *string    `db:"scene_code" json:"scene_code"`
	SceneName            *string    `db:"scene_name" json:"scene_name"`
	TaskType             string     `db:"task_type" json:"task_type"`
	Title                string     `db:"title" json:"title"`
	AssigneeId           *string    `db:"assignee_id" json:"assignee_id"`
	AssignedBy           *string    `db:"assigned_by" json:"assigned_by"`
	AssignedAt           *time.Time `db:"assigned_at" json:"assigned_at"`
	Status               string     `db:"status" json:"status"`
	PromptText           *string    `db:"prompt_text" json:"prompt_text"`
	LatestPromptText     *string    `db:"latest_prompt_text" json:"latest_prompt_text"`
	DueAt                *time.Time `db:"due_at" json:"due_at"`
	CompletedAt          *time.Time `db:"completed_at" json:"completed_at"`
	VisibleUntil         *time.Time `db:"visible_until" json:"visible_until"`
	ProductionModel      *string    `db:"production_model" json:"production_model"`
	MediaType            *string    `db:"media_type" json:"media_type"`
	TaskVariant          *string    `db:"task_variant" json:"task_variant"`
	AgeStageCode         *string    `db:"age_stage_code" json:"age_stage_code"`
	CostumeVariantCode   *string    `db:"costume_variant_code" json:"costume_variant_code"`
	VariantPlanId        *string    `db:"variant_plan_id" json:"variant_plan_id"`
	VariantKind          *string    `db:"variant_kind" json:"variant_kind"`
	VariantTitleZh       *string    `db:"variant_title_zh" json:"variant_title_zh"`
	VariantDescriptionZh *string    `db:"variant_description_zh" json:"variant_description_zh"`
	PromptRevisionId     *string    `db:"prompt_revision_id" json:"prompt_revision_id"`
	AssetContextOutdated bool       `db:"asset_context_outdated" json:"asset_context_outdated"`
	DependsOnTaskId      *string    `db:"depends_on_task_id" json:"depends_on_task_id"`
	IsRetired            bool       `db:"is_retired" json:"is_retired"`
	RetiredAt            *time.Time `db:"retired_at" json:"retired_at"`
	RetiredBy            *string    `db:"retired_by" json:"retired_by"`
	RetiredReason        *string    `db:"retired_reason" json:"retired_reason"`
	CreatedAt            time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt            time.Time  `db:"updated_at" json:"updated_at"`
}

type TaskDependencies struct {
	ID              string    `db:"id" json:"id"`
	TaskId          string    `db:"task_id" json:"task_id"`
	DependsOnTaskId string    `db:"depends_on_task_id" json:"depends_on_task_id"`
	DependencyType  string    `db:"dependency_type" json:"dependency_type"`
	CreatedAt       time.Time `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time `db:"updated_at" json:"updated_at"`
}

type TaskSubmissionBatches struct {
	ID              string     `db:"id" json:"id"`
	TaskId          string     `db:"task_id" json:"task_id"`
	Step            string     `db:"step" json:"step"`
	VersionNo       int32      `db:"version_no" json:"version_no"`
	Status          string     `db:"status" json:"status"`
	SubmittedById   *string    `db:"submitted_by_id" json:"submitted_by_id"`
	SubmittedAt     *time.Time `db:"submitted_at" json:"submitted_at"`
	ReviewedById    *string    `db:"reviewed_by_id" json:"reviewed_by_id"`
	ReviewedAt      *time.Time `db:"reviewed_at" json:"reviewed_at"`
	ReviewComment   *string    `db:"review_comment" json:"review_comment"`
	SubmitRequestId *string    `db:"submit_request_id" json:"submit_request_id"`
	ReviewRequestId *string    `db:"review_request_id" json:"review_request_id"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at" json:"updated_at"`
}

type Submissions struct {
	ID                       string          `db:"id" json:"id"`
	TaskId                   string          `db:"task_id" json:"task_id"`
	BatchId                  *string         `db:"batch_id" json:"batch_id"`
	FileId                   *string         `db:"file_id" json:"file_id"`
	StoryboardId             *string         `db:"storyboard_id" json:"storyboard_id"`
	FilePath                 string          `db:"file_path" json:"file_path"`
	FileType                 string          `db:"file_type" json:"file_type"`
	Step                     *string         `db:"step" json:"step"`
	PromptText               *string         `db:"prompt_text" json:"prompt_text"`
	OriginalPromptText       *string         `db:"original_prompt_text" json:"original_prompt_text"`
	RevisedPromptText        *string         `db:"revised_prompt_text" json:"revised_prompt_text"`
	ViewLabel                *string         `db:"view_label" json:"view_label"`
	StateLabel               *string         `db:"state_label" json:"state_label"`
	Description              *string         `db:"description" json:"description"`
	ModelName                *string         `db:"model_name" json:"model_name"`
	ToolNames                json.RawMessage `db:"tool_names" json:"tool_names"`
	SubmittedById            *string         `db:"submitted_by_id" json:"submitted_by_id"`
	Status                   string          `db:"status" json:"status"`
	IsSelected               bool            `db:"is_selected" json:"is_selected"`
	IsPrimary                bool            `db:"is_primary" json:"is_primary"`
	IsArchived               bool            `db:"is_archived" json:"is_archived"`
	ArchivedById             *string         `db:"archived_by_id" json:"archived_by_id"`
	ArchivedAt               *time.Time      `db:"archived_at" json:"archived_at"`
	SourceMasterSubmissionId *string         `db:"source_master_submission_id" json:"source_master_submission_id"`
	IsInvalidated            bool            `db:"is_invalidated" json:"is_invalidated"`
	InvalidatedAt            *time.Time      `db:"invalidated_at" json:"invalidated_at"`
	InvalidatedById          *string         `db:"invalidated_by_id" json:"invalidated_by_id"`
	InvalidatedReason        *string         `db:"invalidated_reason" json:"invalidated_reason"`
	InvalidatedSourceId      *string         `db:"invalidated_source_id" json:"invalidated_source_id"`
	PurgedAt                 *time.Time      `db:"purged_at" json:"purged_at"`
	CreatedAt                time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt                time.Time       `db:"updated_at" json:"updated_at"`
}

type TaskCandidateImports struct {
	ID                string    `db:"id" json:"id"`
	IDempotencyKey    string    `db:"idempotency_key" json:"idempotency_key"`
	TaskId            string    `db:"task_id" json:"task_id"`
	SubmissionId      *string   `db:"submission_id" json:"submission_id"`
	SourceSystem      string    `db:"source_system" json:"source_system"`
	SourceProjectId   string    `db:"source_project_id" json:"source_project_id"`
	SourceFlowId      string    `db:"source_flow_id" json:"source_flow_id"`
	SourceNodeId      string    `db:"source_node_id" json:"source_node_id"`
	SourceAssetId     string    `db:"source_asset_id" json:"source_asset_id"`
	SourceOutputIndex int32     `db:"source_output_index" json:"source_output_index"`
	SourceSha256      *string   `db:"source_sha256" json:"source_sha256"`
	SourceContentType *string   `db:"source_content_type" json:"source_content_type"`
	SourceSizeBytes   *int64    `db:"source_size_bytes" json:"source_size_bytes"`
	Status            string    `db:"status" json:"status"`
	ErrorCode         *string   `db:"error_code" json:"error_code"`
	CreatedBy         string    `db:"created_by" json:"created_by"`
	CreatedAt         time.Time `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time `db:"updated_at" json:"updated_at"`
	Step              string    `db:"step" json:"step"`
}

type TaskPrompts struct {
	ID         string     `db:"id" json:"id"`
	TaskId     string     `db:"task_id" json:"task_id"`
	PromptType string     `db:"prompt_type" json:"prompt_type"`
	PromptText string     `db:"prompt_text" json:"prompt_text"`
	VersionNo  int32      `db:"version_no" json:"version_no"`
	Source     string     `db:"source" json:"source"`
	CopiedAt   *time.Time `db:"copied_at" json:"copied_at"`
	CreatedBy  *string    `db:"created_by" json:"created_by"`
	CreatedAt  time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at" json:"updated_at"`
}

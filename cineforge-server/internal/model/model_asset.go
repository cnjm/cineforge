// Code generated from cineforge single-db schema (00001 migration). DO NOT EDIT manually;
// regenerate via scripts if needed. Column → Go field mapping is mechanical.
package model

import (
	"encoding/json"
	"time"
)

type Assets struct {
	ID                string          `db:"id" json:"id"`
	ProjectId         *string         `db:"project_id" json:"project_id"`
	AssetCode         *string         `db:"asset_code" json:"asset_code"`
	AssetType         string          `db:"asset_type" json:"asset_type"`
	Name              string          `db:"name" json:"name"`
	Description       *string         `db:"description" json:"description"`
	Status            string          `db:"status" json:"status"`
	Tags              json.RawMessage `db:"tags" json:"tags"`
	PreviewPath       *string         `db:"preview_path" json:"preview_path"`
	FilePath          *string         `db:"file_path" json:"file_path"`
	PromptText        *string         `db:"prompt_text" json:"prompt_text"`
	BaseModel         *string         `db:"base_model" json:"base_model"`
	MetadataJson      json.RawMessage `db:"metadata_json" json:"metadata_json"`
	Version           int32           `db:"version" json:"version"`
	CurrentVersionId  *string         `db:"current_version_id" json:"current_version_id"`
	CurrentRevisionId *string         `db:"current_revision_id" json:"current_revision_id"`
	IsLocked          bool            `db:"is_locked" json:"is_locked"`
	LockedBy          *string         `db:"locked_by" json:"locked_by"`
	LockedAt          *time.Time      `db:"locked_at" json:"locked_at"`
	CreatedById       *string         `db:"created_by_id" json:"created_by_id"`
	CreatedAt         time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at" json:"updated_at"`
}

type AssetVersions struct {
	ID                       string          `db:"id" json:"id"`
	AssetId                  string          `db:"asset_id" json:"asset_id"`
	VersionNo                int32           `db:"version_no" json:"version_no"`
	AssetVersionCode         string          `db:"asset_version_code" json:"asset_version_code"`
	FileId                   *string         `db:"file_id" json:"file_id"`
	PreviewFileId            *string         `db:"preview_file_id" json:"preview_file_id"`
	PromptText               *string         `db:"prompt_text" json:"prompt_text"`
	NegativePromptText       *string         `db:"negative_prompt_text" json:"negative_prompt_text"`
	BaseModel                *string         `db:"base_model" json:"base_model"`
	ToolName                 *string         `db:"tool_name" json:"tool_name"`
	MetadataJson             json.RawMessage `db:"metadata_json" json:"metadata_json"`
	IsCurrent                bool            `db:"is_current" json:"is_current"`
	SourceTaskId             *string         `db:"source_task_id" json:"source_task_id"`
	SourceAgentRunId         *string         `db:"source_agent_run_id" json:"source_agent_run_id"`
	CreatedBy                *string         `db:"created_by" json:"created_by"`
	SourceSubmissionId       *string         `db:"source_submission_id" json:"source_submission_id"`
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

type AssetRelations struct {
	ID            string    `db:"id" json:"id"`
	AssetId       string    `db:"asset_id" json:"asset_id"`
	ProjectId     *string   `db:"project_id" json:"project_id"`
	EpisodeId     *string   `db:"episode_id" json:"episode_id"`
	ScriptId      *string   `db:"script_id" json:"script_id"`
	StoryboardId  *string   `db:"storyboard_id" json:"storyboard_id"`
	ImageOutputId *string   `db:"image_output_id" json:"image_output_id"`
	VideoOutputId *string   `db:"video_output_id" json:"video_output_id"`
	FinalOutputId *string   `db:"final_output_id" json:"final_output_id"`
	RelationType  string    `db:"relation_type" json:"relation_type"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time `db:"updated_at" json:"updated_at"`
}

type AssetVariantPlans struct {
	ID              string          `db:"id" json:"id"`
	ProjectId       string          `db:"project_id" json:"project_id"`
	EpisodeId       *string         `db:"episode_id" json:"episode_id"`
	ScriptVersionId *string         `db:"script_version_id" json:"script_version_id"`
	AssetId         string          `db:"asset_id" json:"asset_id"`
	VariantCode     string          `db:"variant_code" json:"variant_code"`
	VariantKind     string          `db:"variant_kind" json:"variant_kind"`
	TitleZh         string          `db:"title_zh" json:"title_zh"`
	DescriptionZh   string          `db:"description_zh" json:"description_zh"`
	Source          string          `db:"source" json:"source"`
	Confidence      *float64        `db:"confidence" json:"confidence"`
	Status          string          `db:"status" json:"status"`
	TaskId          *string         `db:"task_id" json:"task_id"`
	CreatedBy       *string         `db:"created_by" json:"created_by"`
	MetadataJson    json.RawMessage `db:"metadata_json" json:"metadata_json"`
	CreatedAt       time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at" json:"updated_at"`
}

type AssetVariantStoryboardLinks struct {
	ID            string    `db:"id" json:"id"`
	VariantPlanId string    `db:"variant_plan_id" json:"variant_plan_id"`
	StoryboardId  string    `db:"storyboard_id" json:"storyboard_id"`
	Source        string    `db:"source" json:"source"`
	Confidence    *float64  `db:"confidence" json:"confidence"`
	Status        string    `db:"status" json:"status"`
	CreatedBy     *string   `db:"created_by" json:"created_by"`
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time `db:"updated_at" json:"updated_at"`
}

type AssetCompletionRecords struct {
	ID                     string          `db:"id" json:"id"`
	ProjectId              string          `db:"project_id" json:"project_id"`
	AssetId                string          `db:"asset_id" json:"asset_id"`
	AssetCode              *string         `db:"asset_code" json:"asset_code"`
	AgeStageCode           *string         `db:"age_stage_code" json:"age_stage_code"`
	CostumeVariantCode     *string         `db:"costume_variant_code" json:"costume_variant_code"`
	TargetField            string          `db:"target_field" json:"target_field"`
	MissingReason          *string         `db:"missing_reason" json:"missing_reason"`
	OriginalUncertainty    *string         `db:"original_uncertainty" json:"original_uncertainty"`
	AiSuggestion           *string         `db:"ai_suggestion" json:"ai_suggestion"`
	HumanDescription       *string         `db:"human_description" json:"human_description"`
	SelectedScenes         json.RawMessage `db:"selected_scenes" json:"selected_scenes"`
	CorrectionType         string          `db:"correction_type" json:"correction_type"`
	ResolutionMode         string          `db:"resolution_mode" json:"resolution_mode"`
	Status                 string          `db:"status" json:"status"`
	SourceAgentRevisionId  *string         `db:"source_agent_revision_id" json:"source_agent_revision_id"`
	BaseAssetRevisionId    *string         `db:"base_asset_revision_id" json:"base_asset_revision_id"`
	AppliedAssetRevisionId *string         `db:"applied_asset_revision_id" json:"applied_asset_revision_id"`
	DownstreamStatus       string          `db:"downstream_status" json:"downstream_status"`
	CreatedBy              *string         `db:"created_by" json:"created_by"`
	CreatedAt              time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt              time.Time       `db:"updated_at" json:"updated_at"`
}

type EntityVersions struct {
	ID            string          `db:"id" json:"id"`
	ProjectId     *string         `db:"project_id" json:"project_id"`
	EntityType    string          `db:"entity_type" json:"entity_type"`
	EntityId      *string         `db:"entity_id" json:"entity_id"`
	EntityCode    string          `db:"entity_code" json:"entity_code"`
	VersionNo     int32           `db:"version_no" json:"version_no"`
	VersionCode   string          `db:"version_code" json:"version_code"`
	Source        string          `db:"source" json:"source"`
	Status        string          `db:"status" json:"status"`
	ContentJson   json.RawMessage `db:"content_json" json:"content_json"`
	ChangeSummary *string         `db:"change_summary" json:"change_summary"`
	ChangedFields json.RawMessage `db:"changed_fields" json:"changed_fields"`
	BaseVersionId *string         `db:"base_version_id" json:"base_version_id"`
	AgentRunId    *string         `db:"agent_run_id" json:"agent_run_id"`
	CreatedBy     *string         `db:"created_by" json:"created_by"`
	IsCurrent     bool            `db:"is_current" json:"is_current"`
	CreatedAt     time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at" json:"updated_at"`
}

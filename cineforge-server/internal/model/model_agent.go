// Code generated from cineforge single-db schema (00001 migration). DO NOT EDIT manually;
// regenerate via scripts if needed. Column → Go field mapping is mechanical.
package model

import (
	"encoding/json"
	"time"
)

type AgentRuns struct {
	ID               string           `db:"id" json:"id"`
	ProjectId        *string          `db:"project_id" json:"project_id"`
	ParentRunId      *string          `db:"parent_run_id" json:"parent_run_id"`
	WorkflowRunId    *string          `db:"workflow_run_id" json:"workflow_run_id"`
	NodeKey          *string          `db:"node_key" json:"node_key"`
	EpisodeId        *string          `db:"episode_id" json:"episode_id"`
	ScriptId         *string          `db:"script_id" json:"script_id"`
	StoryboardId     *string          `db:"storyboard_id" json:"storyboard_id"`
	TaskId           *string          `db:"task_id" json:"task_id"`
	AgentType        string           `db:"agent_type" json:"agent_type"`
	Status           string           `db:"status" json:"status"`
	InputJson        json.RawMessage  `db:"input_json" json:"input_json"`
	OutputJson       *json.RawMessage `db:"output_json" json:"output_json"`
	SummaryJson      *json.RawMessage `db:"summary_json" json:"summary_json"`
	PayloadRef       *string          `db:"payload_ref" json:"payload_ref"`
	PayloadSizeBytes *int32           `db:"payload_size_bytes" json:"payload_size_bytes"`
	PayloadSha256    *string          `db:"payload_sha256" json:"payload_sha256"`
	ErrorMessage     *string          `db:"error_message" json:"error_message"`
	TokenUsage       *json.RawMessage `db:"token_usage" json:"token_usage"`
	DurationMs       *int32           `db:"duration_ms" json:"duration_ms"`
	FeedbackJson     *json.RawMessage `db:"feedback_json" json:"feedback_json"`
	VersionNo        int32            `db:"version_no" json:"version_no"`
	Model            *string          `db:"model" json:"model"`
	SkillName        *string          `db:"skill_name" json:"skill_name"`
	SkillVersion     *string          `db:"skill_version" json:"skill_version"`
	ContractVersion  *string          `db:"contract_version" json:"contract_version"`
	ContractHash     *string          `db:"contract_hash" json:"contract_hash"`
	PromptVersion    *string          `db:"prompt_version" json:"prompt_version"`
	InputHash        *string          `db:"input_hash" json:"input_hash"`
	DataState        string           `db:"data_state" json:"data_state"`
	CreatedAt        time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time        `db:"updated_at" json:"updated_at"`
}

type AgentIssues struct {
	ID         string    `db:"id" json:"id"`
	RunId      string    `db:"run_id" json:"run_id"`
	Level      string    `db:"level" json:"level"`
	Code       string    `db:"code" json:"code"`
	Message    string    `db:"message" json:"message"`
	TargetPath *string   `db:"target_path" json:"target_path"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time `db:"updated_at" json:"updated_at"`
}

type AgentFeedback struct {
	ID           string    `db:"id" json:"id"`
	RunId        *string   `db:"run_id" json:"run_id"`
	TaskId       *string   `db:"task_id" json:"task_id"`
	AssetId      *string   `db:"asset_id" json:"asset_id"`
	BeforeText   *string   `db:"before_text" json:"before_text"`
	AfterText    *string   `db:"after_text" json:"after_text"`
	FeedbackType string    `db:"feedback_type" json:"feedback_type"`
	CreatedBy    *string   `db:"created_by" json:"created_by"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

type AgentTrainingSamples struct {
	ID                      string          `db:"id" json:"id"`
	ProjectId               *string         `db:"project_id" json:"project_id"`
	AgentType               *string         `db:"agent_type" json:"agent_type"`
	SampleType              string          `db:"sample_type" json:"sample_type"`
	EntityType              *string         `db:"entity_type" json:"entity_type"`
	EntityCode              *string         `db:"entity_code" json:"entity_code"`
	InputJson               json.RawMessage `db:"input_json" json:"input_json"`
	AgentOutputJson         json.RawMessage `db:"agent_output_json" json:"agent_output_json"`
	HumanModifiedOutputJson json.RawMessage `db:"human_modified_output_json" json:"human_modified_output_json"`
	ConfirmedOutputJson     json.RawMessage `db:"confirmed_output_json" json:"confirmed_output_json"`
	ChangeSummary           json.RawMessage `db:"change_summary" json:"change_summary"`
	QualityScore            *int32          `db:"quality_score" json:"quality_score"`
	TrainingTags            json.RawMessage `db:"training_tags" json:"training_tags"`
	TrainingReady           bool            `db:"training_ready" json:"training_ready"`
	CreatedBy               *string         `db:"created_by" json:"created_by"`
	SourceRunId             *string         `db:"source_run_id" json:"source_run_id"`
	PayloadRef              *string         `db:"payload_ref" json:"payload_ref"`
	PayloadSizeBytes        *int32          `db:"payload_size_bytes" json:"payload_size_bytes"`
	PayloadSha256           *string         `db:"payload_sha256" json:"payload_sha256"`
	DataState               string          `db:"data_state" json:"data_state"`
	LineageKey              *string         `db:"lineage_key" json:"lineage_key"`
	CreatedAt               time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt               time.Time       `db:"updated_at" json:"updated_at"`
}

type ArtifactRevisions struct {
	ID                string          `db:"id" json:"id"`
	ProjectId         string          `db:"project_id" json:"project_id"`
	ArtifactType      string          `db:"artifact_type" json:"artifact_type"`
	ArtifactId        string          `db:"artifact_id" json:"artifact_id"`
	VersionNo         int32           `db:"version_no" json:"version_no"`
	ParentRevisionId  *string         `db:"parent_revision_id" json:"parent_revision_id"`
	SourceType        string          `db:"source_type" json:"source_type"`
	SourceRunId       *string         `db:"source_run_id" json:"source_run_id"`
	SkillName         *string         `db:"skill_name" json:"skill_name"`
	SkillVersion      *string         `db:"skill_version" json:"skill_version"`
	ContractVersion   *string         `db:"contract_version" json:"contract_version"`
	ContractHash      *string         `db:"contract_hash" json:"contract_hash"`
	ModelName         *string         `db:"model_name" json:"model_name"`
	PromptVersion     *string         `db:"prompt_version" json:"prompt_version"`
	InputHash         *string         `db:"input_hash" json:"input_hash"`
	InputSnapshot     json.RawMessage `db:"input_snapshot" json:"input_snapshot"`
	RawOutput         json.RawMessage `db:"raw_output" json:"raw_output"`
	NormalizedContent json.RawMessage `db:"normalized_content" json:"normalized_content"`
	ChangeDiff        json.RawMessage `db:"change_diff" json:"change_diff"`
	ContentHash       string          `db:"content_hash" json:"content_hash"`
	Status            string          `db:"status" json:"status"`
	CreatedBy         *string         `db:"created_by" json:"created_by"`
	DataState         string          `db:"data_state" json:"data_state"`
	PayloadRef        *string         `db:"payload_ref" json:"payload_ref"`
	PayloadSizeBytes  *int32          `db:"payload_size_bytes" json:"payload_size_bytes"`
	PayloadSha256     *string         `db:"payload_sha256" json:"payload_sha256"`
	IDempotencyKey    *string         `db:"idempotency_key" json:"idempotency_key"`
	CreatedAt         time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at" json:"updated_at"`
}

type WorkflowRuns struct {
	ID               string           `db:"id" json:"id"`
	ProjectId        string           `db:"project_id" json:"project_id"`
	AgentRunId       *string          `db:"agent_run_id" json:"agent_run_id"`
	WorkflowType     string           `db:"workflow_type" json:"workflow_type"`
	Status           string           `db:"status" json:"status"`
	NodeStatusJson   json.RawMessage  `db:"node_status_json" json:"node_status_json"`
	ResultJson       json.RawMessage  `db:"result_json" json:"result_json"`
	SummaryJson      *json.RawMessage `db:"summary_json" json:"summary_json"`
	PayloadRef       *string          `db:"payload_ref" json:"payload_ref"`
	PayloadSizeBytes *int32           `db:"payload_size_bytes" json:"payload_size_bytes"`
	PayloadSha256    *string          `db:"payload_sha256" json:"payload_sha256"`
	ErrorJson        json.RawMessage  `db:"error_json" json:"error_json"`
	InputJson        json.RawMessage  `db:"input_json" json:"input_json"`
	CreatedBy        *string          `db:"created_by" json:"created_by"`
	LockKey          *string          `db:"lock_key" json:"lock_key"`
	LockToken        *string          `db:"lock_token" json:"lock_token"`
	IDempotencyKey   *string          `db:"idempotency_key" json:"idempotency_key"`
	CreatedAt        time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time        `db:"updated_at" json:"updated_at"`
}

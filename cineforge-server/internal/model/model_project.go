// Code generated from cineforge single-db schema (00001 migration). DO NOT EDIT manually;
// regenerate via scripts if needed. Column → Go field mapping is mechanical.
package model

import (
	"encoding/json"
	"time"
)

type Projects struct {
	ID              string           `db:"id" json:"id"`
	ProjectNo       *string          `db:"project_no" json:"project_no"`
	ProjectPrefix   *string          `db:"project_prefix" json:"project_prefix"`
	Name            *string          `db:"name" json:"name"`
	Title           string           `db:"title" json:"title"`
	Genre           *string          `db:"genre" json:"genre"`
	Style           *string          `db:"style" json:"style"`
	Status          string           `db:"status" json:"status"`
	CurrentStage    string           `db:"current_stage" json:"current_stage"`
	ManagerId       *string          `db:"manager_id" json:"manager_id"`
	CreatedById     *string          `db:"created_by_id" json:"created_by_id"`
	ScriptText      *string          `db:"script_text" json:"script_text"`
	ProductionBrief *json.RawMessage `db:"production_brief" json:"production_brief"`
	LockedAt        *time.Time       `db:"locked_at" json:"locked_at"`
	ArchivedAt      *time.Time       `db:"archived_at" json:"archived_at"`
	DeletedAt       *time.Time       `db:"deleted_at" json:"deleted_at"`
	DeletedById     *string          `db:"deleted_by_id" json:"deleted_by_id"`
	DeleteReason    *string          `db:"delete_reason" json:"delete_reason"`
	CreatedAt       time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time        `db:"updated_at" json:"updated_at"`
}

type ProjectEpisodes struct {
	ID              string           `db:"id" json:"id"`
	ProjectId       string           `db:"project_id" json:"project_id"`
	EpisodeNo       int32            `db:"episode_no" json:"episode_no"`
	EpisodeCode     string           `db:"episode_code" json:"episode_code"`
	Title           *string          `db:"title" json:"title"`
	Summary         *string          `db:"summary" json:"summary"`
	ProductionBrief *json.RawMessage `db:"production_brief" json:"production_brief"`
	CreatedAt       time.Time        `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time        `db:"updated_at" json:"updated_at"`
}

type Scripts struct {
	ID               string    `db:"id" json:"id"`
	ProjectId        string    `db:"project_id" json:"project_id"`
	EpisodeId        *string   `db:"episode_id" json:"episode_id"`
	ScriptCode       string    `db:"script_code" json:"script_code"`
	Title            *string   `db:"title" json:"title"`
	Content          string    `db:"content" json:"content"`
	Status           string    `db:"status" json:"status"`
	CurrentVersionId *string   `db:"current_version_id" json:"current_version_id"`
	CreatedBy        *string   `db:"created_by" json:"created_by"`
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time `db:"updated_at" json:"updated_at"`
}

type ScriptVersions struct {
	ID               string    `db:"id" json:"id"`
	ScriptId         string    `db:"script_id" json:"script_id"`
	VersionNo        int32     `db:"version_no" json:"version_no"`
	Source           string    `db:"source" json:"source"`
	Content          string    `db:"content" json:"content"`
	ContentHash      *string   `db:"content_hash" json:"content_hash"`
	SourceFileId     *string   `db:"source_file_id" json:"source_file_id"`
	OriginalFilename *string   `db:"original_filename" json:"original_filename"`
	ParserName       *string   `db:"parser_name" json:"parser_name"`
	Language         *string   `db:"language" json:"language"`
	SourceAgentRunId *string   `db:"source_agent_run_id" json:"source_agent_run_id"`
	CreatedBy        *string   `db:"created_by" json:"created_by"`
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time `db:"updated_at" json:"updated_at"`
}

type ScriptSegments struct {
	ID                string          `db:"id" json:"id"`
	ProjectId         string          `db:"project_id" json:"project_id"`
	EpisodeId         *string         `db:"episode_id" json:"episode_id"`
	ScriptSegmentCode string          `db:"script_segment_code" json:"script_segment_code"`
	EpisodeCode       *string         `db:"episode_code" json:"episode_code"`
	OrderNo           int32           `db:"order_no" json:"order_no"`
	Title             *string         `db:"title" json:"title"`
	SourceText        string          `db:"source_text" json:"source_text"`
	Summary           *string         `db:"summary" json:"summary"`
	StoryFunction     *string         `db:"story_function" json:"story_function"`
	DominantEmotion   *string         `db:"dominant_emotion" json:"dominant_emotion"`
	Rhythm            *string         `db:"rhythm" json:"rhythm"`
	Viewpoint         *string         `db:"viewpoint" json:"viewpoint"`
	ContextCode       *string         `db:"context_code" json:"context_code"`
	RenderMode        *string         `db:"render_mode" json:"render_mode"`
	MetadataJson      json.RawMessage `db:"metadata_json" json:"metadata_json"`
	CurrentVersionId  *string         `db:"current_version_id" json:"current_version_id"`
	CreatedBy         *string         `db:"created_by" json:"created_by"`
	CreatedAt         time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at" json:"updated_at"`
}

type Storyboards struct {
	ID               string          `db:"id" json:"id"`
	ProjectId        string          `db:"project_id" json:"project_id"`
	EpisodeNum       int32           `db:"episode_num" json:"episode_num"`
	OrderNum         int32           `db:"order_num" json:"order_num"`
	Title            *string         `db:"title" json:"title"`
	Description      string          `db:"description" json:"description"`
	Dialogue         *string         `db:"dialogue" json:"dialogue"`
	Camera           *string         `db:"camera" json:"camera"`
	DurationSeconds  *int32          `db:"duration_seconds" json:"duration_seconds"`
	Characters       json.RawMessage `db:"characters" json:"characters"`
	Keyframes        json.RawMessage `db:"keyframes" json:"keyframes"`
	MirrorShots      json.RawMessage `db:"mirror_shots" json:"mirror_shots"`
	Status           string          `db:"status" json:"status"`
	StoryboardCode   *string         `db:"storyboard_code" json:"storyboard_code"`
	SceneCode        *string         `db:"scene_code" json:"scene_code"`
	SceneName        *string         `db:"scene_name" json:"scene_name"`
	ContextCode      *string         `db:"context_code" json:"context_code"`
	RenderMode       *string         `db:"render_mode" json:"render_mode"`
	ScriptId         *string         `db:"script_id" json:"script_id"`
	ScriptSegmentId  *string         `db:"script_segment_id" json:"script_segment_id"`
	Narration        *string         `db:"narration" json:"narration"`
	ShotType         *string         `db:"shot_type" json:"shot_type"`
	CurrentVersionId *string         `db:"current_version_id" json:"current_version_id"`
	CreatedAt        time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at" json:"updated_at"`
}

type StoryboardVersions struct {
	ID               string          `db:"id" json:"id"`
	StoryboardId     string          `db:"storyboard_id" json:"storyboard_id"`
	VersionNo        int32           `db:"version_no" json:"version_no"`
	Source           string          `db:"source" json:"source"`
	SourceText       *string         `db:"source_text" json:"source_text"`
	Description      *string         `db:"description" json:"description"`
	Dialogue         *string         `db:"dialogue" json:"dialogue"`
	Narration        *string         `db:"narration" json:"narration"`
	Camera           *string         `db:"camera" json:"camera"`
	ShotType         *string         `db:"shot_type" json:"shot_type"`
	DurationSeconds  *int32          `db:"duration_seconds" json:"duration_seconds"`
	AssetSnapshot    json.RawMessage `db:"asset_snapshot" json:"asset_snapshot"`
	SourceAgentRunId *string         `db:"source_agent_run_id" json:"source_agent_run_id"`
	CreatedBy        *string         `db:"created_by" json:"created_by"`
	CreatedAt        time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time       `db:"updated_at" json:"updated_at"`
}

type ScriptBreakdowns struct {
	ID          string          `db:"id" json:"id"`
	ProjectId   string          `db:"project_id" json:"project_id"`
	Version     int32           `db:"version" json:"version"`
	ContentJson json.RawMessage `db:"content_json" json:"content_json"`
	AgentRunId  *string         `db:"agent_run_id" json:"agent_run_id"`
	EditedById  *string         `db:"edited_by_id" json:"edited_by_id"`
	DataState   string          `db:"data_state" json:"data_state"`
	CreatedAt   time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time       `db:"updated_at" json:"updated_at"`
}

type ProjectAssets struct {
	ProjectId    string    `db:"project_id" json:"project_id"`
	AssetId      string    `db:"asset_id" json:"asset_id"`
	Relation     string    `db:"relation" json:"relation"`
	StoryboardId *string   `db:"storyboard_id" json:"storyboard_id"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

type ProjectAssetLists struct {
	ID               string    `db:"id" json:"id"`
	ProjectId        string    `db:"project_id" json:"project_id"`
	SourceScriptId   *string   `db:"source_script_id" json:"source_script_id"`
	SourceAgentRunId *string   `db:"source_agent_run_id" json:"source_agent_run_id"`
	VersionNo        int32     `db:"version_no" json:"version_no"`
	Status           string    `db:"status" json:"status"`
	CreatedBy        *string   `db:"created_by" json:"created_by"`
	CreatedAt        time.Time `db:"created_at" json:"created_at"`
	UpdatedAt        time.Time `db:"updated_at" json:"updated_at"`
}

type ProjectAssetListItems struct {
	ID              string          `db:"id" json:"id"`
	AssetListId     string          `db:"asset_list_id" json:"asset_list_id"`
	ProjectId       string          `db:"project_id" json:"project_id"`
	AssetId         *string         `db:"asset_id" json:"asset_id"`
	AssetType       string          `db:"asset_type" json:"asset_type"`
	AssetCode       *string         `db:"asset_code" json:"asset_code"`
	Name            string          `db:"name" json:"name"`
	Description     *string         `db:"description" json:"description"`
	MetadataJson    json.RawMessage `db:"metadata_json" json:"metadata_json"`
	SourceText      *string         `db:"source_text" json:"source_text"`
	EpisodeId       *string         `db:"episode_id" json:"episode_id"`
	ScriptId        *string         `db:"script_id" json:"script_id"`
	ScriptSegmentId *string         `db:"script_segment_id" json:"script_segment_id"`
	StoryboardId    *string         `db:"storyboard_id" json:"storyboard_id"`
	Status          string          `db:"status" json:"status"`
	CreatedAt       time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at" json:"updated_at"`
}

type EpisodeAssetBindings struct {
	ID                         string          `db:"id" json:"id"`
	ProjectId                  string          `db:"project_id" json:"project_id"`
	EpisodeId                  string          `db:"episode_id" json:"episode_id"`
	ScriptVersionId            string          `db:"script_version_id" json:"script_version_id"`
	ConfirmedReadingRevisionId string          `db:"confirmed_reading_revision_id" json:"confirmed_reading_revision_id"`
	ApplicationRevisionId      string          `db:"application_revision_id" json:"application_revision_id"`
	AssetId                    string          `db:"asset_id" json:"asset_id"`
	AssetRevisionId            *string         `db:"asset_revision_id" json:"asset_revision_id"`
	ProposalIndex              int32           `db:"proposal_index" json:"proposal_index"`
	Action                     string          `db:"action" json:"action"`
	Status                     string          `db:"status" json:"status"`
	AgeStageCode               *string         `db:"age_stage_code" json:"age_stage_code"`
	CostumeVariantCode         *string         `db:"costume_variant_code" json:"costume_variant_code"`
	ProposalJson               json.RawMessage `db:"proposal_json" json:"proposal_json"`
	CreatedAt                  time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt                  time.Time       `db:"updated_at" json:"updated_at"`
}

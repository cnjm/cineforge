// Code generated from cineforge single-db schema (00001 migration). DO NOT EDIT manually;
// regenerate via scripts if needed. Column → Go field mapping is mechanical.
package model

import (
	"encoding/json"
	"time"
)

type Files struct {
	ID         string    `db:"id" json:"id"`
	FileCode   *string   `db:"file_code" json:"file_code"`
	Bucket     string    `db:"bucket" json:"bucket"`
	ObjectKey  string    `db:"object_key" json:"object_key"`
	FileName   string    `db:"file_name" json:"file_name"`
	MimeType   *string   `db:"mime_type" json:"mime_type"`
	FileSize   *int32    `db:"file_size" json:"file_size"`
	Checksum   *string   `db:"checksum" json:"checksum"`
	UploadedBy *string   `db:"uploaded_by" json:"uploaded_by"`
	ProjectId  *string   `db:"project_id" json:"project_id"`
	EpisodeId  *string   `db:"episode_id" json:"episode_id"`
	TaskId     *string   `db:"task_id" json:"task_id"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time `db:"updated_at" json:"updated_at"`
}

type ImageOutputs struct {
	ID           string    `db:"id" json:"id"`
	ProjectId    string    `db:"project_id" json:"project_id"`
	EpisodeId    *string   `db:"episode_id" json:"episode_id"`
	ScriptId     *string   `db:"script_id" json:"script_id"`
	StoryboardId *string   `db:"storyboard_id" json:"storyboard_id"`
	TaskId       *string   `db:"task_id" json:"task_id"`
	AssetId      *string   `db:"asset_id" json:"asset_id"`
	ImageCode    string    `db:"image_code" json:"image_code"`
	FileId       *string   `db:"file_id" json:"file_id"`
	PromptText   *string   `db:"prompt_text" json:"prompt_text"`
	ModelName    *string   `db:"model_name" json:"model_name"`
	Status       string    `db:"status" json:"status"`
	VersionNo    int32     `db:"version_no" json:"version_no"`
	CreatedBy    *string   `db:"created_by" json:"created_by"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

type VideoOutputs struct {
	ID           string    `db:"id" json:"id"`
	ProjectId    string    `db:"project_id" json:"project_id"`
	EpisodeId    *string   `db:"episode_id" json:"episode_id"`
	ScriptId     *string   `db:"script_id" json:"script_id"`
	StoryboardId *string   `db:"storyboard_id" json:"storyboard_id"`
	TaskId       *string   `db:"task_id" json:"task_id"`
	AssetId      *string   `db:"asset_id" json:"asset_id"`
	VideoCode    string    `db:"video_code" json:"video_code"`
	VideoType    string    `db:"video_type" json:"video_type"`
	FileId       *string   `db:"file_id" json:"file_id"`
	PromptText   *string   `db:"prompt_text" json:"prompt_text"`
	ModelName    *string   `db:"model_name" json:"model_name"`
	ToolName     *string   `db:"tool_name" json:"tool_name"`
	Status       string    `db:"status" json:"status"`
	VersionNo    int32     `db:"version_no" json:"version_no"`
	CreatedBy    *string   `db:"created_by" json:"created_by"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

type FinalOutputs struct {
	ID        string          `db:"id" json:"id"`
	ProjectId string          `db:"project_id" json:"project_id"`
	EpisodeId *string         `db:"episode_id" json:"episode_id"`
	TaskId    *string         `db:"task_id" json:"task_id"`
	AssetId   *string         `db:"asset_id" json:"asset_id"`
	FinalCode string          `db:"final_code" json:"final_code"`
	Name      string          `db:"name" json:"name"`
	FileId    *string         `db:"file_id" json:"file_id"`
	ToolNames json.RawMessage `db:"tool_names" json:"tool_names"`
	Status    string          `db:"status" json:"status"`
	VersionNo int32           `db:"version_no" json:"version_no"`
	CreatedBy *string         `db:"created_by" json:"created_by"`
	CreatedAt time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt time.Time       `db:"updated_at" json:"updated_at"`
}

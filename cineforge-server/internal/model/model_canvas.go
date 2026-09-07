// Code generated from cineforge single-db schema (00001 migration). DO NOT EDIT manually;
// regenerate via scripts if needed. Column → Go field mapping is mechanical.
package model

type CanvasProjects struct {
	ID                string  `db:"id" json:"id"`
	Name              string  `db:"name" json:"name"`
	TeamId            *string `db:"team_id" json:"team_id"`
	OwnerId           *string `db:"owner_id" json:"owner_id"`
	ManagedExternally int32   `db:"managed_externally" json:"managed_externally"`
	ManagedBy         *string `db:"managed_by" json:"managed_by"`
	SourceSystem      *string `db:"source_system" json:"source_system"`
	ExternalProjectId *string `db:"external_project_id" json:"external_project_id"`
	ExternalUpdatedAt *string `db:"external_updated_at" json:"external_updated_at"`
	CreatedAt         string  `db:"created_at" json:"created_at"`
	UpdatedAt         string  `db:"updated_at" json:"updated_at"`
}

type CanvasAssets struct {
	ID        string  `db:"id" json:"id"`
	Name      string  `db:"name" json:"name"`
	Data      *string `db:"data" json:"data"`
	OwnerId   string  `db:"owner_id" json:"owner_id"`
	ProjectId *string `db:"project_id" json:"project_id"`
	CreatedAt string  `db:"created_at" json:"created_at"`
	UpdatedAt string  `db:"updated_at" json:"updated_at"`
}

type Flows struct {
	ID        string  `db:"id" json:"id"`
	Name      string  `db:"name" json:"name"`
	Data      string  `db:"data" json:"data"`
	OwnerId   string  `db:"owner_id" json:"owner_id"`
	ProjectId string  `db:"project_id" json:"project_id"`
	EpisodeId *string `db:"episode_id" json:"episode_id"`
	Revision  int32   `db:"revision" json:"revision"`
	ManagedBy *string `db:"managed_by" json:"managed_by"`
	DeletedAt *string `db:"deleted_at" json:"deleted_at"`
	CreatedAt string  `db:"created_at" json:"created_at"`
	UpdatedAt string  `db:"updated_at" json:"updated_at"`
}

type CineForgeTaskCanvasBindings struct {
	ID                 string  `db:"id" json:"id"`
	CineForgeProjectId   string  `db:"cineforge_project_id" json:"cineforge_project_id"`
	CineForgeTaskId      string  `db:"cineforge_task_id" json:"cineforge_task_id"`
	CineForgeEpisodeId   *string `db:"cineforge_episode_id" json:"cineforge_episode_id"`
	FlowId             string  `db:"flow_id" json:"flow_id"`
	CreatedByUserId    string  `db:"created_by_user_id" json:"created_by_user_id"`
	TaskTitleSnapshot  string  `db:"task_title_snapshot" json:"task_title_snapshot"`
	TaskStatusSnapshot string  `db:"task_status_snapshot" json:"task_status_snapshot"`
	TaskUpdatedAt      *string `db:"task_updated_at" json:"task_updated_at"`
	GroupKey           string  `db:"group_key" json:"group_key"`
	GroupType          string  `db:"group_type" json:"group_type"`
	CreatedAt          string  `db:"created_at" json:"created_at"`
	UpdatedAt          string  `db:"updated_at" json:"updated_at"`
}

type CineForgeTaskCanvasMembers struct {
	GroupKey      string `db:"group_key" json:"group_key"`
	CineForgeTaskId string `db:"cineforge_task_id" json:"cineforge_task_id"`
	Role          string `db:"role" json:"role"`
	CreatedAt     string `db:"created_at" json:"created_at"`
	UpdatedAt     string `db:"updated_at" json:"updated_at"`
}

type CanvasNodeOutputs struct {
	ID           string  `db:"id" json:"id"`
	TaskRecordId string  `db:"task_record_id" json:"task_record_id"`
	FlowId       string  `db:"flow_id" json:"flow_id"`
	NodeId       string  `db:"node_id" json:"node_id"`
	AssetId      string  `db:"asset_id" json:"asset_id"`
	TaskId       string  `db:"task_id" json:"task_id"`
	MediaType    string  `db:"media_type" json:"media_type"`
	OutputIndex  int32   `db:"output_index" json:"output_index"`
	AssetName    *string `db:"asset_name" json:"asset_name"`
	CreatedAt    string  `db:"created_at" json:"created_at"`
}

type AssetStorageLocations struct {
	ID          string  `db:"id" json:"id"`
	AssetId     string  `db:"asset_id" json:"asset_id"`
	StorageType string  `db:"storage_type" json:"storage_type"`
	ObjectKey   string  `db:"object_key" json:"object_key"`
	Url         *string `db:"url" json:"url"`
	ExpiresAt   *string `db:"expires_at" json:"expires_at"`
	Status      string  `db:"status" json:"status"`
	ContentType *string `db:"content_type" json:"content_type"`
	SizeBytes   *int64  `db:"size_bytes" json:"size_bytes"`
	Etag        *string `db:"etag" json:"etag"`
	CreatedAt   string  `db:"created_at" json:"created_at"`
	UpdatedAt   string  `db:"updated_at" json:"updated_at"`
}

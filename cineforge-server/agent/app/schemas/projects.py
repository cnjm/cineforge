from datetime import datetime
from typing import Any
from uuid import UUID

from pydantic import BaseModel, Field

from app.models.enums import ProjectStatus, TaskStatus
from app.schemas.production_brief import EpisodeProductionBrief, ProjectProductionBrief


class ProjectCreate(BaseModel):
    title: str = Field(min_length=1, max_length=160)
    genre: str | None = None
    script_text: str | None = None
    project_prefix: str | None = Field(default=None, max_length=5)
    manager_id: UUID | None = None
    production_brief: ProjectProductionBrief


class ProjectUpdate(BaseModel):
    title: str | None = Field(default=None, min_length=1, max_length=160)
    genre: str | None = None
    script_text: str | None = None
    project_prefix: str | None = Field(default=None, max_length=5)
    manager_id: UUID | None = None
    production_brief: ProjectProductionBrief | None = None


class ProjectEpisodeRead(BaseModel):
    id: UUID
    project_id: UUID
    episode_no: int
    episode_code: str
    title: str | None = None
    summary: str | None = None
    breakdown_status: str = "script_imported"
    stale_stages: list[str] = Field(default_factory=list)
    script_id: UUID | None = None
    current_version_id: UUID | None = None
    current_version_no: int | None = None
    version_count: int = 0
    current_content_hash: str | None = None
    current_original_filename: str | None = None
    production_brief: dict[str, Any] | None = None
    updated_at: datetime


class ScriptVersionSummary(BaseModel):
    id: UUID
    version_no: int
    content_hash: str | None = None
    original_filename: str | None = None
    parser_name: str | None = None
    language: str | None = None
    created_at: datetime
    is_current: bool = False


class ScriptVersionDetail(ScriptVersionSummary):
    script_id: UUID
    script_text: str


class EpisodeAssetBindingRead(BaseModel):
    id: UUID
    proposal_index: int
    asset_id: UUID
    asset_revision_id: UUID | None = None
    asset_code: str | None = None
    asset_type: str
    asset_name: str
    action: str
    status: str
    age_stage_code: str | None = None
    costume_variant_code: str | None = None
    proposal: dict[str, Any] = Field(default_factory=dict)


class ProjectEpisodeDetail(ProjectEpisodeRead):
    versions: list[ScriptVersionSummary] = Field(default_factory=list)
    assets: list[EpisodeAssetBindingRead] = Field(default_factory=list)


class ProjectEpisodeDeleteRequest(BaseModel):
    confirm_episode_code: str = Field(min_length=1, max_length=16)
    delete_files: bool = True


class ProjectEpisodeDeleteResponse(BaseModel):
    project_id: UUID
    episode_id: UUID
    episode_code: str
    deleted_counts: dict[str, int] = Field(default_factory=dict)
    file_cleanup: dict[str, Any] = Field(default_factory=dict)


class ProjectImportContext(BaseModel):
    episode_id: UUID
    episode_no: int
    episode_code: str
    script_id: UUID
    script_version_id: UUID
    script_version_no: int
    created_episode: bool
    created_script: bool
    replaced_existing_episode: bool
    content_hash: str


class ProjectRead(BaseModel):
    id: UUID
    project_no: str | None = None
    project_prefix: str | None = None
    name: str | None = None
    title: str
    genre: str | None = None
    status: ProjectStatus
    current_stage: ProjectStatus | None = None
    manager_id: UUID | None = None
    script_text: str | None = None
    production_brief: dict[str, Any] | None = None
    archived_at: datetime | None = None
    deleted_at: datetime | None = None
    storyboard_count: int = 0
    task_count: int = 0
    latest_import: ProjectImportContext | None = None
    created_at: datetime
    updated_at: datetime


class ProjectAdminActionRequest(BaseModel):
    admin_password: str = Field(min_length=1, max_length=64)
    reason: str | None = Field(default=None, max_length=500)


class StoryboardRead(BaseModel):
    id: UUID
    project_id: UUID
    script_segment_id: UUID | None = None
    episode_num: int
    order_num: int
    storyboard_code: str | None = None
    title: str | None = None
    description: str
    dialogue: str | None = None
    dialogue_back_translation: str | None = None
    camera: str | None = None
    shot_type: str | None = None
    duration_seconds: int | None = None
    characters: list[str] = Field(default_factory=list)
    keyframes: list[dict[str, Any]] = Field(default_factory=list)
    mirror_shots: list[dict[str, Any]] = Field(default_factory=list)
    scene_code: str | None = None
    scene_name: str | None = None
    context_code: str | None = None
    render_mode: str | None = None
    status: TaskStatus

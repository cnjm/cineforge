from datetime import datetime
from typing import Any
from uuid import UUID

from pydantic import BaseModel, Field

from app.models.enums import ProductionAgentKind


class SourceLocation(BaseModel):
    source_file_id: UUID | None = None
    episode_no: int | None = None
    scene_no: str | None = ""
    paragraph_start: int | None = None
    paragraph_end: int | None = None
    char_start: int | None = None
    char_end: int | None = None


class SourceContext(BaseModel):
    before: str = ""
    current: str = ""
    after: str = ""
    start_anchor: str = ""
    end_anchor: str = ""
    source_location: SourceLocation = Field(default_factory=SourceLocation)


class ScriptSegmentCreate(BaseModel):
    project_id: UUID
    episode_id: UUID | None = None
    script_segment_code: str = Field(min_length=1, max_length=80)
    episode_code: str | None = None
    order_no: int
    title: str | None = None
    source_text: str = Field(min_length=1)
    summary: str | None = None
    story_function: str | None = None
    dominant_emotion: str | None = None
    rhythm: str | None = None
    viewpoint: str | None = None
    context_code: str | None = None
    render_mode: str | None = None
    metadata_json: dict[str, Any] = Field(default_factory=dict)
    created_by: UUID | None = None


class ScriptSegmentRead(ScriptSegmentCreate):
    id: UUID
    current_version_id: UUID | None = None
    created_at: datetime
    updated_at: datetime


class EntityVersionCreate(BaseModel):
    project_id: UUID | None = None
    entity_type: str = Field(min_length=1, max_length=80)
    entity_id: UUID | None = None
    entity_code: str = Field(min_length=1, max_length=120)
    source: str = "agent"
    status: str = "draft"
    content_json: dict[str, Any] = Field(default_factory=dict)
    change_summary: str | None = None
    changed_fields: list[str] = Field(default_factory=list)
    base_version_id: UUID | None = None
    agent_run_id: UUID | None = None
    created_by: UUID | None = None
    is_current: bool = True


class EntityVersionRead(EntityVersionCreate):
    id: UUID
    version_no: int
    version_code: str
    created_at: datetime
    updated_at: datetime


class AgentTrainingSampleCreate(BaseModel):
    project_id: UUID | None = None
    agent_type: ProductionAgentKind | None = None
    sample_type: str = Field(min_length=1, max_length=80)
    entity_type: str | None = None
    entity_code: str | None = None
    input_json: dict[str, Any] = Field(default_factory=dict)
    agent_output_json: dict[str, Any] = Field(default_factory=dict)
    human_modified_output_json: dict[str, Any] = Field(default_factory=dict)
    confirmed_output_json: dict[str, Any] = Field(default_factory=dict)
    change_summary: list[str] = Field(default_factory=list)
    quality_score: int | None = Field(default=None, ge=1, le=10)
    training_tags: list[str] = Field(default_factory=list)
    training_ready: bool = False
    created_by: UUID | None = None

from __future__ import annotations

from datetime import UTC, datetime
from typing import Any, Literal
from uuid import UUID

from pydantic import BaseModel, Field


SourceLabel = Literal["agent", "human_modified", "import", "human", "system", "migration", "locked"]
AssetItemType = Literal["character", "scene", "prop", "final_video"]
PromptType = Literal[
    "script_breakdown",
    "asset_confirm",
    "text_to_image",
    "image_to_video",
    "text_to_video",
    "empty_shot_video",
    "final_output",
]


class JsonBase(BaseModel):
    schema_version: str = "1.0.0"
    json_id: UUID | None = None
    source_label: SourceLabel
    created_at: datetime = Field(default_factory=lambda: datetime.now(UTC))
    created_by: UUID | None = None
    source_agent_run_id: UUID | None = None
    notes: str | None = None


class FileRef(BaseModel):
    file_id: UUID | None = None
    bucket: str | None = None
    object_key: str | None = None
    file_name: str | None = None
    mime_type: str | None = None
    file_size: int | None = None
    checksum: str | None = None


class VersionRef(BaseModel):
    version_no: int = Field(default=1, ge=1)
    version_code: str | None = None
    parent_version_code: str | None = None
    is_current: bool = True


class TraceRef(BaseModel):
    project_id: UUID | None = None
    project_code: str | None = None
    project_prefix: str | None = Field(default=None, max_length=5)
    episode_id: UUID | None = None
    episode_code: str | None = None
    script_id: UUID | None = None
    script_code: str | None = None
    storyboard_id: UUID | None = None
    storyboard_code: str | None = None
    task_id: UUID | None = None
    asset_id: UUID | None = None
    asset_code: str | None = None


class CharacterSpec(BaseModel):
    character_code: str | None = None
    role_name: str
    gender: str | None = None
    age: str | None = None
    appearance: str | None = None
    personality: str | None = None
    background: str | None = None
    remark: str | None = None
    source_text: str | None = None
    related_storyboard_codes: list[str] = Field(default_factory=list)


class SceneSpec(BaseModel):
    scene_code: str | None = None
    scene_no: str | None = None
    scene_name: str
    interior_exterior: str | None = None
    time: str | None = None
    location: str | None = None
    atmosphere: str | None = None
    remark: str | None = None
    source_text: str | None = None
    related_storyboard_codes: list[str] = Field(default_factory=list)


class PropSpec(BaseModel):
    prop_code: str | None = None
    prop_name: str
    usage_scene: str | None = None
    scene_description: str | None = None
    source_text: str | None = None
    related_storyboard_codes: list[str] = Field(default_factory=list)


class ScriptSegment(BaseModel):
    segment_code: str | None = None
    order_no: int = Field(ge=1)
    title: str | None = None
    content: str
    summary: str | None = None


class StoryboardDraft(BaseModel):
    storyboard_code: str | None = None
    order_no: int = Field(ge=1)
    title: str | None = None
    source_text: str | None = None
    description: str
    dialogue: str | None = None
    narration: str | None = None
    camera: str | None = None
    shot_type: str | None = None
    scene_code: str | None = None
    scene_name: str | None = None
    duration_seconds: int | None = Field(default=None, ge=10, le=15)
    character_codes: list[str] = Field(default_factory=list)
    scene_codes: list[str] = Field(default_factory=list)
    prop_codes: list[str] = Field(default_factory=list)


class EpisodeBreakdown(BaseModel):
    episode_code: str
    episode_no: int = Field(ge=1)
    title: str | None = None
    summary: str | None = None
    script_segments: list[ScriptSegment] = Field(default_factory=list)
    storyboards: list[StoryboardDraft] = Field(default_factory=list)


class ScriptBreakdownJson(JsonBase):
    json_type: Literal["script_breakdown_json"] = "script_breakdown_json"
    trace: TraceRef
    source_file: FileRef | None = None
    script_title: str
    script_text_digest: str | None = None
    episodes: list[EpisodeBreakdown] = Field(default_factory=list)
    characters: list[CharacterSpec] = Field(default_factory=list)
    scenes: list[SceneSpec] = Field(default_factory=list)
    props: list[PropSpec] = Field(default_factory=list)
    warnings: list[str] = Field(default_factory=list)


class AssetListItem(BaseModel):
    asset_type: AssetItemType
    asset_code: str | None = None
    name: str
    description: str | None = None
    status: Literal["draft", "pending", "confirmed", "locked", "rejected"] = "draft"
    production_stage: Literal["asset_production", "storyboard_production", "assembly"] = "asset_production"
    source_text: str | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)
    related_script_codes: list[str] = Field(default_factory=list)
    related_storyboard_codes: list[str] = Field(default_factory=list)
    confirmed_asset_id: UUID | None = None
    confirmed_asset_version_code: str | None = None
    asset_image_task_id: UUID | None = None


class AssetListJson(JsonBase):
    json_type: Literal["asset_list_json"] = "asset_list_json"
    trace: TraceRef
    asset_list_code: str
    version: VersionRef = Field(default_factory=VersionRef)
    items: list[AssetListItem] = Field(default_factory=list)
    locked_item_codes: list[str] = Field(default_factory=list)


class StoryboardJson(JsonBase):
    json_type: Literal["storyboard_json"] = "storyboard_json"
    trace: TraceRef
    version: VersionRef = Field(default_factory=VersionRef)
    storyboard_code: str
    order_no: int = Field(ge=1)
    title: str | None = None
    source_text: str | None = None
    description: str
    dialogue: str | None = None
    narration: str | None = None
    camera: str | None = None
    shot_type: str | None = None
    scene_code: str | None = None
    scene_name: str | None = None
    duration_seconds: int | None = Field(default=None, ge=10, le=15)
    character_codes: list[str] = Field(default_factory=list)
    scene_codes: list[str] = Field(default_factory=list)
    prop_codes: list[str] = Field(default_factory=list)
    image_task_id: UUID | None = None
    video_task_id: UUID | None = None


class PromptVersion(BaseModel):
    version_no: int = Field(ge=1)
    source_label: SourceLabel
    prompt_text: str
    negative_prompt_text: str | None = None
    copied: bool = False
    model_provider: str | None = None
    model_name: str | None = None
    tool_names: list[str] = Field(default_factory=list)
    parameters: dict[str, Any] = Field(default_factory=dict)
    created_at: datetime = Field(default_factory=lambda: datetime.now(UTC))
    created_by: UUID | None = None
    source_agent_run_id: UUID | None = None


class PromptJson(JsonBase):
    json_type: Literal["prompt_json"] = "prompt_json"
    trace: TraceRef
    prompt_code: str | None = None
    prompt_type: PromptType
    current_version_no: int = Field(default=1, ge=1)
    agent_prompt_text: str | None = None
    current_prompt_text: str
    final_prompt_text: str | None = None
    negative_prompt_text: str | None = None
    model_provider: str | None = None
    model_name: str | None = None
    tool_names: list[str] = Field(default_factory=list)
    parameters: dict[str, Any] = Field(default_factory=dict)
    asset_codes: list[str] = Field(default_factory=list)
    versions: list[PromptVersion] = Field(default_factory=list)


class SubmissionJson(JsonBase):
    json_type: Literal["submission_json"] = "submission_json"
    trace: TraceRef
    submission_code: str | None = None
    file_type: Literal["image", "video", "final_video", "json", "other"]
    files: list[FileRef] = Field(default_factory=list)
    original_prompt_text: str | None = None
    revised_prompt_text: str | None = None
    final_prompt_text: str | None = None
    model_provider: str | None = None
    model_name: str | None = None
    tool_names: list[str] = Field(default_factory=list)
    parameters: dict[str, Any] = Field(default_factory=dict)
    asset_version_code: str | None = None
    quality_score: int | None = Field(default=None, ge=1, le=10)
    review_note: str | None = None


class AgentTrainingSampleJson(JsonBase):
    json_type: Literal["agent_training_sample_json"] = "agent_training_sample_json"
    sample_id: str
    sample_type: Literal[
        "script_breakdown",
        "asset_extract",
        "storyboard",
        "prompt_edit",
        "submission",
        "review",
        "query",
    ]
    trace: TraceRef
    agent_type: str
    model_provider: str | None = None
    model_name: str | None = None
    input_json: dict[str, Any] = Field(default_factory=dict)
    agent_output_json: dict[str, Any] = Field(default_factory=dict)
    human_modified_json: dict[str, Any] | None = None
    accepted_output_json: dict[str, Any] | None = None
    rating: int | None = Field(default=None, ge=1, le=10)
    failure_reason: str | None = None
    tags: list[str] = Field(default_factory=list)

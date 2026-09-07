from datetime import datetime
from enum import StrEnum
from typing import Any
from uuid import UUID

from pydantic import BaseModel, Field

from app.models.enums import AssetType, TaskStatus, TaskType, UserRole


class ProjectMemberRole(StrEnum):
    owner = "owner"
    manager = "manager"
    lead = "lead"
    member = "member"
    viewer = "viewer"


class UserRead(BaseModel):
    id: UUID
    username: str
    phone: str | None = None
    name: str | None = None
    display_name: str
    role: UserRole
    is_active: bool = True


class UserCreate(BaseModel):
    phone: str = Field(min_length=6, max_length=20)
    name: str | None = Field(default=None, min_length=1, max_length=80)
    display_name: str | None = Field(default=None, min_length=1, max_length=80)
    username: str | None = None
    role: UserRole
    password: str | None = Field(default=None, min_length=6, max_length=64)
    is_active: bool = True


class UserStatusUpdate(BaseModel):
    is_active: bool


class UserPasswordResetRequest(BaseModel):
    password: str | None = Field(default=None, min_length=6, max_length=64)


class UserPasswordResetResponse(BaseModel):
    user: UserRead
    temporary_password: str


class NotificationRead(BaseModel):
    id: UUID
    title: str
    content: str
    type: str
    is_read: bool
    created_at: datetime


class LoginRequest(BaseModel):
    phone: str = Field(min_length=6, max_length=20)
    password: str = Field(min_length=1, max_length=64)


class PasswordVerifyRequest(BaseModel):
    password: str = Field(min_length=1, max_length=64)


class PasswordVerifyResponse(BaseModel):
    ok: bool


class PasswordChangeRequest(BaseModel):
    current_password: str = Field(min_length=1, max_length=64)
    new_password: str = Field(min_length=6, max_length=64)


class ProjectMemberRead(BaseModel):
    project_id: UUID
    user_id: UUID
    role: ProjectMemberRole
    user: UserRead | None = None


class ProjectMemberUpsert(BaseModel):
    user_id: UUID
    role: ProjectMemberRole


class AuthResponse(BaseModel):
    access_token: str
    token_type: str = "bearer"
    user: UserRead
    tapcanvas_token: str
    platform_token: str
    session_expires_at: int


class AssetCreate(BaseModel):
    asset_type: AssetType
    name: str = Field(min_length=1, max_length=160)
    description: str | None = None
    tags: list[str] = Field(default_factory=list)
    preview_path: str | None = None
    file_path: str | None = None
    prompt_text: str | None = None
    base_model: str | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)


class TemporaryAssetCreate(BaseModel):
    request_id: UUID
    project_id: UUID
    episode_id: UUID
    script_version_id: UUID
    asset_type: AssetType
    name: str = Field(min_length=1, max_length=160)
    description: str | None = None
    reason: str = Field(min_length=1, max_length=500)
    production_requirement: str = Field(min_length=1, max_length=2000)
    task_variant: str = Field(default="MASTER", min_length=1, max_length=20)


class AssetRead(AssetCreate):
    id: UUID
    project_id: UUID | None = None
    asset_code: str | None = None
    status: str = "draft"
    version: int = 1
    created_by_id: UUID | None = None
    created_at: datetime
    updated_at: datetime


class AssetLibraryHierarchy(BaseModel):
    project_id: UUID
    project_prefix: str
    episode_id: UUID | None = None
    episode_code: str | None = None
    scene_code: str | None = None
    scene_name: str | None = None
    storyboard_id: UUID | None = None
    storyboard_code: str | None = None
    script_segment_id: UUID | None = None
    script_segment_code: str | None = None
    mirror_shot_code: str | None = None
    asset_id: UUID | None = None
    asset_code: str | None = None
    asset_type: AssetType | None = None
    output_type: str
    artifact_code: str
    task_id: UUID | None = None
    task_type: TaskType | None = None
    depends_on_task_id: UUID | None = None
    task_variant: str | None = None
    age_stage_code: str | None = None
    costume_variant_code: str | None = None


class AssetLibraryVersionRead(BaseModel):
    id: UUID
    source_type: str
    version_no: int
    context_version_no: int | None = None
    canonical_display_name: str
    original_file_name: str | None = None
    file_id: UUID | None = None
    file_path: str | None = None
    mime_type: str | None = None
    file_size: int | None = None
    model_name: str | None = None
    tool_name: str | None = None
    view_label: str | None = None
    state_label: str | None = None
    description: str | None = None
    batch_id: UUID | None = None
    batch_version_no: int | None = None
    status: str
    is_primary: bool = False
    is_current: bool = False
    is_context_current: bool = False
    is_card_master: bool = False
    hierarchy: AssetLibraryHierarchy
    created_at: datetime


class AssetLibraryItemRead(BaseModel):
    library_key: str
    source_type: str
    source_id: UUID
    project_id: UUID | None = None
    project_code: str | None = None
    project_name: str | None = None
    episode_codes: list[str] = Field(default_factory=list)
    source_label: str | None = None
    name: str
    description: str | None = None
    asset_type: AssetType | None = None
    output_type: str
    current_master: AssetLibraryVersionRead | None = None
    latest_version_at: datetime
    version_count: int = 0
    versions: list[AssetLibraryVersionRead] = Field(default_factory=list)


class SceneAssetOption(BaseModel):
    scene_asset_id: UUID
    scene_code: str
    scene_name: str
    episode_code: str | None = None
    revision_id: UUID | None = None
    status: str


class SelectedSceneReference(BaseModel):
    scene_asset_id: UUID
    scene_revision_id: UUID | None = None


class AssetCompletionCreate(BaseModel):
    target_field: str = Field(min_length=1, max_length=120)
    human_description: str | None = None
    age_stage_code: str | None = Field(default=None, max_length=20)
    costume_variant_code: str | None = Field(default=None, max_length=20)
    missing_reason: str | None = Field(default=None, max_length=120)
    original_uncertainty: str | None = None
    ai_suggestion: str | None = None
    selected_scenes: list[SelectedSceneReference] = Field(default_factory=list)
    base_asset_revision_id: UUID | None = None
    resolution_mode: str = Field(default="human_revision", pattern="^(human_revision|uncertain)$")


class AssetCompletionRead(BaseModel):
    id: UUID
    asset_id: UUID
    target_field: str
    status: str
    applied_asset_revision_id: UUID | None = None
    downstream_status: str
    resolution_mode: str = "human_revision"


class SubmissionCreate(BaseModel):
    file_id: UUID | None = None
    file_path: str = Field(min_length=1, max_length=512)
    file_type: str = Field(min_length=1, max_length=40)
    step: str | None = None
    prompt_text: str | None = None
    original_prompt_text: str | None = None
    revised_prompt_text: str | None = None
    view_label: str | None = Field(default=None, max_length=120)
    state_label: str | None = Field(default=None, max_length=120)
    description: str | None = None
    model_name: str | None = None
    tool_names: list[str] = Field(default_factory=list)
    submitted_by_id: UUID | None = None


class SubmissionRead(SubmissionCreate):
    id: UUID
    task_id: UUID
    batch_id: UUID | None = None
    storyboard_id: UUID | None = None
    status: str = "draft"
    is_selected: bool = False
    is_primary: bool = False
    is_archived: bool = False
    archived_by_id: UUID | None = None
    archived_at: datetime | None = None
    source_master_submission_id: UUID | None = None
    is_invalidated: bool = False
    invalidated_at: datetime | None = None
    invalidated_by_id: UUID | None = None
    invalidated_reason: str | None = None
    invalidated_source_id: UUID | None = None
    purged_at: datetime | None = None
    created_at: datetime


class SubmissionMetadataUpdate(BaseModel):
    view_label: str | None = Field(default=None, max_length=120)
    state_label: str | None = Field(default=None, max_length=120)
    description: str | None = None


class SubmissionBatchRead(BaseModel):
    id: UUID
    task_id: UUID
    step: str
    version_no: int
    status: str
    submitted_by_id: UUID | None = None
    submitted_at: datetime | None = None
    reviewed_by_id: UUID | None = None
    reviewed_at: datetime | None = None
    review_comment: str | None = None
    submit_request_id: UUID | None = None
    review_request_id: UUID | None = None
    created_at: datetime
    updated_at: datetime


class TaskSubmitRequest(BaseModel):
    step: str | None = None
    request_id: UUID | None = None


class TaskReviewRequest(BaseModel):
    decision: str = Field(pattern="^(approve|rework)$")
    primary_submission_id: UUID | None = None
    selected_submission_ids: list[UUID] = Field(default_factory=list)
    comment: str | None = None
    request_id: UUID | None = None


class TaskBulkSubmitEntry(BaseModel):
    task_id: UUID
    step: str | None = None


class TaskBulkSubmitRequest(BaseModel):
    request_id: UUID
    entries: list[TaskBulkSubmitEntry] = Field(min_length=1)


class TaskBulkReviewEntry(BaseModel):
    task_id: UUID
    batch_id: UUID
    primary_submission_id: UUID | None = None
    selected_submission_ids: list[UUID] = Field(default_factory=list)


class TaskBulkReviewRequest(BaseModel):
    request_id: UUID
    decision: str = Field(pattern="^(approve|rework)$")
    comment: str | None = None
    entries: list[TaskBulkReviewEntry] = Field(min_length=1)


class DependencyFileRead(BaseModel):
    """A formal, downloadable file attached to a prerequisite asset."""

    file_id: UUID
    file_name: str | None = None
    mime_type: str | None = None
    file_size: int | None = None
    asset_version_id: UUID | None = None
    download_name: str | None = None
    view_label: str | None = None
    submission_id: UUID | None = None
    step: str | None = None


class DependencyAssetRead(BaseModel):
    asset_id: UUID | None = None
    asset_code: str
    asset_name: str | None = None
    asset_type: AssetType | None = None
    required_for: str | None = None
    production_status: str | None = None
    approval_status: str | None = None
    ready: bool = False
    reason: str | None = None
    files: list[DependencyFileRead] = Field(default_factory=list)


class DependencySummaryRead(BaseModel):
    required: int = 0
    ready: int = 0
    pending: int = 0


class SubmissionArchiveRequest(BaseModel):
    archived: bool = True


class PrimarySubmissionRequest(BaseModel):
    submission_id: UUID


class TaskRead(BaseModel):
    id: UUID
    project_id: UUID
    episode_id: UUID | None = None
    episode_code: str | None = None
    script_id: UUID | None = None
    script_version_id: UUID | None = None
    storyboard_id: UUID | None = None
    asset_id: UUID | None = None
    script_segment_id: UUID | None = None
    scene_code: str | None = None
    scene_name: str | None = None
    storyboard_code: str | None = None
    storyboard_duration_seconds: int | None = None
    storyboard_dialogue: str | None = None
    storyboard_dialogue_back_translation: str | None = None
    storyboard_mirror_shots: list[dict[str, Any]] = Field(default_factory=list)
    task_type: TaskType
    title: str
    assignee_id: UUID | None = None
    assignee_name: str | None = None
    status: TaskStatus
    prompt_text: str | None = None
    latest_prompt_text: str | None = None
    due_at: datetime | None = None
    completed_at: datetime | None = None
    production_model: str | None = None
    media_type: str | None = None
    task_variant: str | None = None
    age_stage_code: str | None = None
    costume_variant_code: str | None = None
    variant_plan_id: UUID | None = None
    variant_kind: str | None = None
    variant_title_zh: str | None = None
    variant_description_zh: str | None = None
    linked_storyboard_ids: list[UUID] = Field(default_factory=list)
    prompt_revision_id: UUID | None = None
    asset_context_outdated: bool = False
    depends_on_task_id: UUID | None = None
    is_retired: bool = False
    retired_at: datetime | None = None
    retired_by: UUID | None = None
    retired_reason: str | None = None
    dependency_task_ids: list[UUID] = Field(default_factory=list)
    dependency_asset_codes: list[str] = Field(default_factory=list)
    dependency_summary: DependencySummaryRead = Field(default_factory=DependencySummaryRead)
    dependency_assets: list[DependencyAssetRead] = Field(default_factory=list)
    keyframe_done: bool = False
    video_done: bool = False
    locked: bool = False
    submissions: list[SubmissionRead] = Field(default_factory=list)
    submission_batches: list[SubmissionBatchRead] = Field(default_factory=list)
    created_at: datetime
    updated_at: datetime


class TemporaryAssetProductionRead(BaseModel):
    asset: AssetRead
    task: TaskRead


class TaskUpdate(BaseModel):
    status: TaskStatus | None = None
    assignee_id: UUID | None = None
    reassignment_reason: str | None = Field(default=None, max_length=500)
    latest_prompt_text: str | None = None
    production_model: str | None = None


class AssetVariantPlanCreate(BaseModel):
    asset_id: UUID
    episode_id: UUID | None = None
    script_version_id: UUID | None = None
    variant_kind: str = Field(pattern="^(scene_view|prop_state)$")
    title_zh: str = Field(min_length=1, max_length=160)
    description_zh: str = Field(min_length=1)
    storyboard_ids: list[UUID] = Field(default_factory=list)
    source: str = Field(default="director", max_length=40)
    confidence: float | None = Field(default=None, ge=0, le=1)


class AssetVariantPlanUpdate(BaseModel):
    title_zh: str | None = Field(default=None, min_length=1, max_length=160)
    description_zh: str | None = Field(default=None, min_length=1)
    status: str | None = Field(default=None, pattern="^(suggested|confirmed|retired)$")
    storyboard_ids: list[UUID] | None = None


class AssetVariantPlanRead(BaseModel):
    id: UUID
    project_id: UUID
    episode_id: UUID | None = None
    script_version_id: UUID | None = None
    asset_id: UUID
    asset_code: str | None = None
    asset_name: str | None = None
    variant_code: str
    task_code: str | None = None
    variant_kind: str
    title_zh: str
    description_zh: str
    source: str
    confidence: float | None = None
    status: str
    task_id: UUID | None = None
    storyboard_ids: list[UUID] = Field(default_factory=list)
    storyboard_codes: list[str] = Field(default_factory=list)
    created_at: datetime
    updated_at: datetime


class BulkAssignRequest(BaseModel):
    scope: str = Field(description="asset_category | asset_context | scene | all_assets | all_storyboards")
    key: str = ""
    assignee_id: UUID | None = None
    episode_id: UUID | None = None
    reassignment_reason: str | None = Field(default=None, max_length=500)


class TaskPromptCreate(BaseModel):
    prompt_type: str = Field(default="human_edit", max_length=60)
    prompt_text: str = Field(min_length=1)
    source: str = Field(default="human", max_length=40)
    copied: bool = False
    created_by: UUID | None = None


class TaskPromptRead(TaskPromptCreate):
    id: UUID
    task_id: UUID
    version_no: int
    created_at: datetime


class StoryboardUpdate(BaseModel):
    title: str | None = None
    description: str | None = None
    dialogue: str | None = None
    camera: str | None = None
    duration_seconds: int | None = Field(default=None, ge=10, le=15)
    characters: list[str] | None = None
    mirror_shots: list[dict[str, Any]] | None = None


class ProductionBoardRow(BaseModel):
    storyboard_id: UUID
    episode_num: int
    order_num: int
    title: str | None = None
    description: str
    characters: list[str]
    status: TaskStatus
    tasks: list[TaskRead]
    submissions: list[SubmissionRead]


class DashboardSummary(BaseModel):
    project_id: UUID
    storyboard_count: int
    asset_count: int
    task_count: int
    completed_task_count: int
    overdue_task_count: int
    completion_rate: float
    recent_activity: list[str]


class BreakdownRead(BaseModel):
    project_id: UUID
    version: int
    view: dict[str, Any] = Field(default_factory=dict)
    data_state: str
    agent_run_id: UUID | None = None
    updated_at: datetime


class BreakdownSaveRequest(BaseModel):
    view: dict[str, Any] = Field(default_factory=dict)
    episode_code: str | None = None
    episode_id: UUID | None = None
    script_version_id: UUID | None = None
    change_summary: list[str] = Field(default_factory=list)
    source_label: str = "human_modified"
    data_state: str = Field(default="human_revision", pattern="^(agent_raw|normalized|human_revision|final)$")
    # Optimistic-lock token: the ScriptBreakdown.version the client loaded and edited.
    # When provided, the save is rejected with 409 if a newer version exists, so a
    # concurrent editor's revision is not silently overwritten. Optional for backward
    # compatibility (clients that omit it keep the previous last-write-wins behavior).
    expected_version: int | None = None


class AssetChangeApplicationItem(BaseModel):
    proposal_index: int
    candidate_code: str | None = None
    candidate_name: str | None = None
    action: str
    decision: str
    application_status: str
    asset_id: UUID | None = None
    asset_code: str | None = None
    asset_revision_id: UUID | None = None
    message: str | None = None


class AssetChangeApplicationRead(BaseModel):
    project_id: UUID
    confirmed_reading_revision_id: UUID
    application_revision_id: UUID
    status: str
    created_assets: int = 0
    updated_assets: int = 0
    reused_assets: int = 0
    rejected_proposals: int = 0
    items: list[AssetChangeApplicationItem] = Field(default_factory=list)


class BreakdownLockRequest(BreakdownSaveRequest):
    create_asset_tasks: bool = True


class ScriptSegmentsConfirmResponse(BaseModel):
    project_id: UUID
    episode_code: str | None = None
    script_segment_count: int
    breakdown_version: int
    next_stage: str


class BreakdownLockResponse(BaseModel):
    project_id: UUID
    episode_code: str | None = None
    breakdown_version: int
    training_sample_count: int
    asset_count: int
    created_asset_tasks: int
    task_count: int


class StoryboardVideoTaskCreate(BaseModel):
    episode_code: str | None = None
    scene_code: str | None = None
    scene_name: str | None = None
    assignee_id: UUID | None = None


class StoryboardVideoTaskCreateResponse(BaseModel):
    project_id: UUID
    episode_code: str | None = None
    scene_code: str | None = None
    created_tasks: int
    task_count: int

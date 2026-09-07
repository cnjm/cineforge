import uuid
from datetime import datetime
from typing import Any

from sqlalchemy import BigInteger, Boolean, DateTime, Enum, ForeignKey, Index, Integer, String, Text, UniqueConstraint, func
from sqlalchemy.dialects.postgresql import JSONB, UUID
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.db.base import Base
from app.models.enums import (
    AgentKind,
    AgentRunStatus,
    AssetType,
    ProjectStatus,
    TaskStatus,
    TaskType,
    UserRole,
)


class TimestampMixin:
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), onupdate=func.now()
    )


class User(Base, TimestampMixin):
    __tablename__ = "users"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    username: Mapped[str] = mapped_column(String(64), unique=True, index=True)
    phone: Mapped[str | None] = mapped_column(String(20), unique=True, nullable=True, index=True)
    name: Mapped[str | None] = mapped_column(String(80), nullable=True)
    display_name: Mapped[str] = mapped_column(String(80))
    role: Mapped[UserRole] = mapped_column(Enum(UserRole, name="user_role"))
    password_hash: Mapped[str | None] = mapped_column(String(255), nullable=True)
    is_active: Mapped[bool] = mapped_column(default=True)
    sync_version: Mapped[int] = mapped_column(BigInteger, nullable=False, default=0, server_default="0")



class IntegrationSyncOutbox(Base):
    """Outbox of integration projection events, one row per target domain.

    Rows are written in the same transaction as the source mutation and share
    the same source_version. tenant_id and source_domain are implicit in the
    database, never repeated. The Worker leases rows via locked_by/locked_until.
    """

    __tablename__ = "integration_sync_outbox"
    __table_args__ = (
        UniqueConstraint(
            "aggregate_id", "target_domain", "source_version",
            name="uq_integration_sync_outbox_aggregate_target_version",
        ),
        Index("ix_integration_sync_outbox_status_next_attempt", "status", "next_attempt_at"),
    )

    id: Mapped[int] = mapped_column(BigInteger, primary_key=True, autoincrement=True)
    target_domain: Mapped[str] = mapped_column(String(32), nullable=False)
    event_type: Mapped[str] = mapped_column(String(64), nullable=False)
    aggregate_type: Mapped[str] = mapped_column(String(32), nullable=False)
    aggregate_id: Mapped[str] = mapped_column(String(128), nullable=False, index=True)
    source_version: Mapped[int] = mapped_column(BigInteger, nullable=False)
    payload: Mapped[dict[str, Any]] = mapped_column(JSONB, nullable=False)
    status: Mapped[str] = mapped_column(String(16), nullable=False, default="pending", server_default="pending")
    attempt_count: Mapped[int] = mapped_column(Integer, nullable=False, default=0, server_default="0")
    locked_by: Mapped[str | None] = mapped_column(String(128), nullable=True)
    locked_until: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    last_error: Mapped[str | None] = mapped_column(Text, nullable=True)
    next_attempt_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    created_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), nullable=False
    )
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=True), server_default=func.now(), onupdate=func.now(), nullable=False
    )


class Project(Base, TimestampMixin):
    __tablename__ = "projects"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_no: Mapped[str | None] = mapped_column(String(32), unique=True, nullable=True, index=True)
    project_prefix: Mapped[str | None] = mapped_column(String(5), unique=True, nullable=True, index=True)
    name: Mapped[str | None] = mapped_column(String(160), nullable=True)
    title: Mapped[str] = mapped_column(String(160), index=True)
    genre: Mapped[str | None] = mapped_column(String(80), nullable=True)
    style: Mapped[str | None] = mapped_column(String(120), nullable=True)
    status: Mapped[ProjectStatus] = mapped_column(
        Enum(ProjectStatus, name="project_status"), default=ProjectStatus.draft
    )
    current_stage: Mapped[ProjectStatus] = mapped_column(
        Enum(ProjectStatus, name="project_stage"), default=ProjectStatus.draft
    )
    manager_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    created_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    script_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    production_brief: Mapped[dict[str, Any] | None] = mapped_column(JSONB, nullable=True)
    locked_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    archived_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    deleted_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True, index=True)
    deleted_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    delete_reason: Mapped[str | None] = mapped_column(Text, nullable=True)

    storyboards: Mapped[list["Storyboard"]] = relationship(back_populates="project")
    tasks: Mapped[list["Task"]] = relationship(back_populates="project")
    agent_runs: Mapped[list["AgentRun"]] = relationship(back_populates="project")
    workflow_runs: Mapped[list["WorkflowRun"]] = relationship(back_populates="project")
    script_segments: Mapped[list["ScriptSegment"]] = relationship(back_populates="project")


class ScriptSegment(Base, TimestampMixin):
    __tablename__ = "script_segments"
    __table_args__ = (UniqueConstraint("project_id", "script_segment_code"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True, index=True)
    script_segment_code: Mapped[str] = mapped_column(String(80), index=True)
    episode_code: Mapped[str | None] = mapped_column(String(32), nullable=True, index=True)
    order_no: Mapped[int] = mapped_column(Integer)
    title: Mapped[str | None] = mapped_column(String(160), nullable=True)
    source_text: Mapped[str] = mapped_column(Text)
    summary: Mapped[str | None] = mapped_column(Text, nullable=True)
    story_function: Mapped[str | None] = mapped_column(Text, nullable=True)
    dominant_emotion: Mapped[str | None] = mapped_column(Text, nullable=True)
    rhythm: Mapped[str | None] = mapped_column(Text, nullable=True)
    viewpoint: Mapped[str | None] = mapped_column(Text, nullable=True)
    context_code: Mapped[str | None] = mapped_column(String(48), nullable=True, index=True)
    render_mode: Mapped[str | None] = mapped_column(String(48), nullable=True, index=True)
    metadata_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    current_version_id: Mapped[uuid.UUID | None] = mapped_column(nullable=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)

    project: Mapped["Project"] = relationship(back_populates="script_segments")
    storyboards: Mapped[list["Storyboard"]] = relationship(back_populates="script_segment")
    tasks: Mapped[list["Task"]] = relationship(back_populates="script_segment")


class Storyboard(Base, TimestampMixin):
    __tablename__ = "storyboards"
    __table_args__ = (UniqueConstraint("project_id", "episode_num", "order_num"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    episode_num: Mapped[int] = mapped_column(Integer, default=1)
    order_num: Mapped[int] = mapped_column(Integer)
    title: Mapped[str | None] = mapped_column(String(160), nullable=True)
    description: Mapped[str] = mapped_column(Text)
    dialogue: Mapped[str | None] = mapped_column(Text, nullable=True)
    camera: Mapped[str | None] = mapped_column(Text, nullable=True)
    duration_seconds: Mapped[int | None] = mapped_column(Integer, nullable=True)
    characters: Mapped[list[str]] = mapped_column(JSONB, default=list)
    keyframes: Mapped[list[dict[str, Any]]] = mapped_column(JSONB, default=list)
    mirror_shots: Mapped[list[dict[str, Any]]] = mapped_column(JSONB, default=list)
    status: Mapped[TaskStatus] = mapped_column(Enum(TaskStatus, name="storyboard_status"), default=TaskStatus.todo)
    storyboard_code: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    scene_code: Mapped[str | None] = mapped_column(String(80), nullable=True, index=True)
    scene_name: Mapped[str | None] = mapped_column(String(160), nullable=True)
    context_code: Mapped[str | None] = mapped_column(String(48), nullable=True, index=True)
    render_mode: Mapped[str | None] = mapped_column(String(48), nullable=True, index=True)
    script_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("scripts.id"), nullable=True)
    script_segment_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("script_segments.id"), nullable=True, index=True)
    narration: Mapped[str | None] = mapped_column(Text, nullable=True)
    shot_type: Mapped[str | None] = mapped_column(String(80), nullable=True)
    current_version_id: Mapped[uuid.UUID | None] = mapped_column(nullable=True)

    project: Mapped["Project"] = relationship(back_populates="storyboards")
    script_segment: Mapped["ScriptSegment | None"] = relationship(back_populates="storyboards")
    tasks: Mapped[list["Task"]] = relationship(back_populates="storyboard")


class Asset(Base, TimestampMixin):
    __tablename__ = "assets"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("projects.id"), nullable=True, index=True)
    asset_code: Mapped[str | None] = mapped_column(String(80), unique=True, nullable=True, index=True)
    asset_type: Mapped[AssetType] = mapped_column(Enum(AssetType, name="asset_type"), index=True)
    name: Mapped[str] = mapped_column(String(160), index=True)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    status: Mapped[str] = mapped_column(String(40), default="draft")
    tags: Mapped[list[str]] = mapped_column(JSONB, default=list)
    preview_path: Mapped[str | None] = mapped_column(String(512), nullable=True)
    file_path: Mapped[str | None] = mapped_column(String(512), nullable=True)
    prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    base_model: Mapped[str | None] = mapped_column(String(120), nullable=True)
    metadata_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    version: Mapped[int] = mapped_column(Integer, default=1)
    current_version_id: Mapped[uuid.UUID | None] = mapped_column(nullable=True)
    current_revision_id: Mapped[uuid.UUID | None] = mapped_column(nullable=True, index=True)
    is_locked: Mapped[bool] = mapped_column(Boolean, default=False)
    locked_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    locked_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    created_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class AssetVariantPlan(Base, TimestampMixin):
    """A director-visible scene view or prop state requirement."""

    __tablename__ = "asset_variant_plans"
    __table_args__ = (UniqueConstraint("asset_id", "episode_id", "variant_code"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id", ondelete="CASCADE"), index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id", ondelete="CASCADE"), nullable=True, index=True)
    script_version_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("script_versions.id"), nullable=True, index=True)
    asset_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("assets.id", ondelete="CASCADE"), index=True)
    variant_code: Mapped[str] = mapped_column(String(20), index=True)
    variant_kind: Mapped[str] = mapped_column(String(40), index=True)
    title_zh: Mapped[str] = mapped_column(String(160))
    description_zh: Mapped[str] = mapped_column(Text)
    source: Mapped[str] = mapped_column(String(40), default="auto", index=True)
    confidence: Mapped[float | None] = mapped_column(nullable=True)
    status: Mapped[str] = mapped_column(String(40), default="suggested", index=True)
    task_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("tasks.id", ondelete="SET NULL"), nullable=True, index=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    metadata_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)


class AssetVariantStoryboardLink(Base, TimestampMixin):
    __tablename__ = "asset_variant_storyboard_links"
    __table_args__ = (UniqueConstraint("variant_plan_id", "storyboard_id"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    variant_plan_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("asset_variant_plans.id", ondelete="CASCADE"), index=True)
    storyboard_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("storyboards.id", ondelete="CASCADE"), index=True)
    source: Mapped[str] = mapped_column(String(40), default="auto", index=True)
    confidence: Mapped[float | None] = mapped_column(nullable=True)
    status: Mapped[str] = mapped_column(String(40), default="active", index=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class ProjectAsset(Base, TimestampMixin):
    __tablename__ = "project_assets"
    __table_args__ = (UniqueConstraint("project_id", "asset_id", "relation"),)

    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), primary_key=True)
    asset_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("assets.id"), primary_key=True)
    relation: Mapped[str] = mapped_column(String(80), default="referenced")
    storyboard_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("storyboards.id"), nullable=True)


class Task(Base, TimestampMixin):
    __tablename__ = "tasks"
    __table_args__ = (
        Index("ix_tasks_project_status_due", "project_id", "status", "due_at"),
    )

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True)
    script_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("scripts.id"), nullable=True)
    script_version_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("script_versions.id"), nullable=True, index=True)
    idempotency_key: Mapped[str | None] = mapped_column(String(64), nullable=True, unique=True)
    script_segment_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("script_segments.id"), nullable=True)
    storyboard_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("storyboards.id"), nullable=True)
    asset_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("assets.id"), nullable=True)
    scene_code: Mapped[str | None] = mapped_column(String(80), nullable=True, index=True)
    scene_name: Mapped[str | None] = mapped_column(String(160), nullable=True)
    task_type: Mapped[TaskType] = mapped_column(Enum(TaskType, name="task_type"), index=True)
    title: Mapped[str] = mapped_column(String(160))
    assignee_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    assigned_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    assigned_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    status: Mapped[TaskStatus] = mapped_column(Enum(TaskStatus, name="task_status"), default=TaskStatus.todo)
    prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    latest_prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    due_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    completed_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    visible_until: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    production_model: Mapped[str | None] = mapped_column(String(120), nullable=True)
    media_type: Mapped[str | None] = mapped_column(String(40), nullable=True, index=True)
    task_variant: Mapped[str | None] = mapped_column(String(20), nullable=True, index=True)
    age_stage_code: Mapped[str | None] = mapped_column(String(20), nullable=True, index=True)
    costume_variant_code: Mapped[str | None] = mapped_column(String(20), nullable=True, index=True)
    variant_plan_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("asset_variant_plans.id", ondelete="SET NULL"), nullable=True, index=True)
    variant_kind: Mapped[str | None] = mapped_column(String(40), nullable=True, index=True)
    variant_title_zh: Mapped[str | None] = mapped_column(String(160), nullable=True)
    variant_description_zh: Mapped[str | None] = mapped_column(Text, nullable=True)
    prompt_revision_id: Mapped[uuid.UUID | None] = mapped_column(nullable=True, index=True)
    asset_context_outdated: Mapped[bool] = mapped_column(Boolean, default=False)
    depends_on_task_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("tasks.id"), nullable=True, index=True)
    is_retired: Mapped[bool] = mapped_column(Boolean, default=False, index=True)
    retired_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    retired_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"), nullable=True)
    retired_reason: Mapped[str | None] = mapped_column(Text, nullable=True)

    project: Mapped["Project"] = relationship(back_populates="tasks")
    script_segment: Mapped["ScriptSegment | None"] = relationship(back_populates="tasks")
    storyboard: Mapped["Storyboard"] = relationship(back_populates="tasks")
    assignee: Mapped["User | None"] = relationship(foreign_keys=[assignee_id])
    submissions: Mapped[list["Submission"]] = relationship(back_populates="task")
    submission_batches: Mapped[list["TaskSubmissionBatch"]] = relationship(back_populates="task")


class TaskDependency(Base, TimestampMixin):
    __tablename__ = "task_dependencies"
    __table_args__ = (UniqueConstraint("task_id", "depends_on_task_id"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    task_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("tasks.id", ondelete="CASCADE"), index=True)
    depends_on_task_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("tasks.id", ondelete="CASCADE"), index=True)
    dependency_type: Mapped[str] = mapped_column(String(40), default="asset_prerequisite", index=True)


class TaskSubmissionBatch(Base, TimestampMixin):
    __tablename__ = "task_submission_batches"
    __table_args__ = (UniqueConstraint("task_id", "step", "version_no"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    task_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("tasks.id", ondelete="CASCADE"), index=True)
    step: Mapped[str] = mapped_column(String(20), default="result", index=True)
    version_no: Mapped[int] = mapped_column(Integer)
    status: Mapped[str] = mapped_column(String(30), default="draft", index=True)
    submitted_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    submitted_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    reviewed_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    reviewed_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    review_comment: Mapped[str | None] = mapped_column(Text, nullable=True)
    submit_request_id: Mapped[uuid.UUID | None] = mapped_column(UUID(as_uuid=True), nullable=True, unique=True, index=True)
    review_request_id: Mapped[uuid.UUID | None] = mapped_column(UUID(as_uuid=True), nullable=True, unique=True, index=True)

    task: Mapped["Task"] = relationship(back_populates="submission_batches")
    submissions: Mapped[list["Submission"]] = relationship(back_populates="batch")


class Submission(Base, TimestampMixin):
    __tablename__ = "submissions"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    task_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("tasks.id"), index=True)
    batch_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("task_submission_batches.id", ondelete="SET NULL"), nullable=True, index=True)
    file_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("files.id", ondelete="SET NULL"), nullable=True, index=True)
    storyboard_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("storyboards.id"), nullable=True)
    file_path: Mapped[str] = mapped_column(String(512))
    file_type: Mapped[str] = mapped_column(String(40))
    step: Mapped[str | None] = mapped_column(String(20), nullable=True)
    prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    original_prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    revised_prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    view_label: Mapped[str | None] = mapped_column(String(120), nullable=True)
    state_label: Mapped[str | None] = mapped_column(String(120), nullable=True)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    model_name: Mapped[str | None] = mapped_column(String(120), nullable=True)
    tool_names: Mapped[list[str]] = mapped_column(JSONB, default=list)
    submitted_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    status: Mapped[str] = mapped_column(String(30), default="draft", index=True)
    is_selected: Mapped[bool] = mapped_column(Boolean, default=False)
    is_primary: Mapped[bool] = mapped_column(Boolean, default=False)
    is_archived: Mapped[bool] = mapped_column(Boolean, default=False, index=True)
    archived_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    archived_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    source_master_submission_id: Mapped[uuid.UUID | None] = mapped_column(
        ForeignKey("submissions.id", ondelete="SET NULL"), nullable=True, index=True
    )
    is_invalidated: Mapped[bool] = mapped_column(Boolean, default=False, index=True)
    invalidated_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    invalidated_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"), nullable=True)
    invalidated_reason: Mapped[str | None] = mapped_column(Text, nullable=True)
    invalidated_source_id: Mapped[uuid.UUID | None] = mapped_column(
        ForeignKey("submissions.id", ondelete="SET NULL"), nullable=True
    )
    purged_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)

    task: Mapped["Task"] = relationship(back_populates="submissions")
    batch: Mapped["TaskSubmissionBatch | None"] = relationship(back_populates="submissions")


class ScriptBreakdown(Base, TimestampMixin):
    __tablename__ = "script_breakdowns"
    __table_args__ = (UniqueConstraint("project_id", "version"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    version: Mapped[int] = mapped_column(Integer, default=1)
    content_json: Mapped[dict[str, Any]] = mapped_column(JSONB)
    agent_run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id"), nullable=True)
    edited_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    data_state: Mapped[str] = mapped_column(String(40), default="human_revision", index=True)


class AgentRun(Base, TimestampMixin):
    __tablename__ = "agent_runs"
    __table_args__ = (
        Index("ix_agent_runs_project_created_id", "project_id", "created_at", "id"),
    )

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("projects.id"), nullable=True, index=True)
    parent_run_id: Mapped[uuid.UUID | None] = mapped_column(
        ForeignKey("agent_runs.id", name="fk_agent_runs_parent_run_id"), nullable=True, index=True
    )
    workflow_run_id: Mapped[uuid.UUID | None] = mapped_column(
        ForeignKey("workflow_runs.id", name="fk_agent_runs_workflow_run_id"), nullable=True, index=True
    )
    node_key: Mapped[str | None] = mapped_column(String(120), nullable=True, index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True)
    script_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("scripts.id"), nullable=True)
    storyboard_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("storyboards.id"), nullable=True)
    task_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("tasks.id"), nullable=True)
    agent_type: Mapped[AgentKind] = mapped_column(Enum(AgentKind, name="agent_kind"), index=True)
    status: Mapped[AgentRunStatus] = mapped_column(
        Enum(AgentRunStatus, name="agent_run_status"), default=AgentRunStatus.queued
    )
    input_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    output_json: Mapped[dict[str, Any] | None] = mapped_column(JSONB, nullable=True)
    summary_json: Mapped[dict[str, Any] | None] = mapped_column(JSONB, nullable=True)
    payload_ref: Mapped[str | None] = mapped_column(String(512), nullable=True)
    payload_size_bytes: Mapped[int | None] = mapped_column(Integer, nullable=True)
    payload_sha256: Mapped[str | None] = mapped_column(String(64), nullable=True)
    error_message: Mapped[str | None] = mapped_column(Text, nullable=True)
    token_usage: Mapped[dict[str, Any] | None] = mapped_column(JSONB, nullable=True)
    duration_ms: Mapped[int | None] = mapped_column(Integer, nullable=True)
    feedback_json: Mapped[dict[str, Any] | None] = mapped_column(JSONB, nullable=True)
    version_no: Mapped[int] = mapped_column(Integer, default=1)
    model: Mapped[str | None] = mapped_column(String(120), nullable=True)
    skill_name: Mapped[str | None] = mapped_column(String(120), nullable=True, index=True)
    skill_version: Mapped[str | None] = mapped_column(String(80), nullable=True)
    contract_version: Mapped[str | None] = mapped_column(String(80), nullable=True)
    contract_hash: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    prompt_version: Mapped[str | None] = mapped_column(String(80), nullable=True)
    input_hash: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    data_state: Mapped[str] = mapped_column(String(40), default="agent_raw", index=True)

    project: Mapped["Project"] = relationship(back_populates="agent_runs")


class WorkflowRun(Base, TimestampMixin):
    __tablename__ = "workflow_runs"
    __table_args__ = (
        Index("ix_workflow_runs_project_created_id", "project_id", "created_at", "id"),
    )

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    agent_run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id", use_alter=True), nullable=True, index=True)
    workflow_type: Mapped[str] = mapped_column(String(80), index=True)
    status: Mapped[str] = mapped_column(String(40), default="queued", index=True)
    node_status_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    result_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    summary_json: Mapped[dict[str, Any] | None] = mapped_column(JSONB, nullable=True)
    payload_ref: Mapped[str | None] = mapped_column(String(512), nullable=True)
    payload_size_bytes: Mapped[int | None] = mapped_column(Integer, nullable=True)
    payload_sha256: Mapped[str | None] = mapped_column(String(64), nullable=True)
    error_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    input_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    lock_key: Mapped[str | None] = mapped_column(String(240), nullable=True, index=True)
    lock_token: Mapped[str | None] = mapped_column(String(120), nullable=True)
    idempotency_key: Mapped[str | None] = mapped_column(String(64), nullable=True, unique=True)

    project: Mapped["Project"] = relationship(back_populates="workflow_runs")


class Permission(Base, TimestampMixin):
    __tablename__ = "permissions"
    __table_args__ = (UniqueConstraint("user_id", "project_id", "permission_type"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    user_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("users.id"), index=True)
    project_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("projects.id"), nullable=True, index=True)
    permission_type: Mapped[str] = mapped_column(String(80), index=True)


class ProjectEpisode(Base, TimestampMixin):
    __tablename__ = "project_episodes"
    __table_args__ = (UniqueConstraint("project_id", "episode_no"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    episode_no: Mapped[int] = mapped_column(Integer)
    episode_code: Mapped[str] = mapped_column(String(16), index=True)
    title: Mapped[str | None] = mapped_column(String(160), nullable=True)
    summary: Mapped[str | None] = mapped_column(Text, nullable=True)
    production_brief: Mapped[dict[str, Any] | None] = mapped_column(JSONB, nullable=True)


class Script(Base, TimestampMixin):
    __tablename__ = "scripts"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True)
    script_code: Mapped[str] = mapped_column(String(80), unique=True, index=True)
    title: Mapped[str | None] = mapped_column(String(160), nullable=True)
    content: Mapped[str] = mapped_column(Text)
    status: Mapped[str] = mapped_column(String(40), default="draft")
    current_version_id: Mapped[uuid.UUID | None] = mapped_column(nullable=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class ScriptVersion(Base, TimestampMixin):
    __tablename__ = "script_versions"
    __table_args__ = (UniqueConstraint("script_id", "version_no"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    script_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("scripts.id"), index=True)
    version_no: Mapped[int] = mapped_column(Integer)
    source: Mapped[str] = mapped_column(String(40), default="import")
    content: Mapped[str] = mapped_column(Text)
    content_hash: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    source_file_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("files.id"), nullable=True)
    original_filename: Mapped[str | None] = mapped_column(String(255), nullable=True)
    parser_name: Mapped[str | None] = mapped_column(String(80), nullable=True)
    language: Mapped[str | None] = mapped_column(String(20), nullable=True)
    source_agent_run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id"), nullable=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class StoryboardVersion(Base, TimestampMixin):
    __tablename__ = "storyboard_versions"
    __table_args__ = (UniqueConstraint("storyboard_id", "version_no"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    storyboard_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("storyboards.id"), index=True)
    version_no: Mapped[int] = mapped_column(Integer)
    source: Mapped[str] = mapped_column(String(40), default="agent")
    source_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    dialogue: Mapped[str | None] = mapped_column(Text, nullable=True)
    narration: Mapped[str | None] = mapped_column(Text, nullable=True)
    camera: Mapped[str | None] = mapped_column(Text, nullable=True)
    shot_type: Mapped[str | None] = mapped_column(String(80), nullable=True)
    duration_seconds: Mapped[int | None] = mapped_column(Integer, nullable=True)
    asset_snapshot: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    source_agent_run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id"), nullable=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class ProjectAssetList(Base, TimestampMixin):
    __tablename__ = "project_asset_lists"
    __table_args__ = (UniqueConstraint("project_id", "version_no"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    source_script_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("scripts.id"), nullable=True)
    source_agent_run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id"), nullable=True)
    version_no: Mapped[int] = mapped_column(Integer, default=1)
    status: Mapped[str] = mapped_column(String(40), default="draft")
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class ProjectAssetListItem(Base, TimestampMixin):
    __tablename__ = "project_asset_list_items"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    asset_list_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("project_asset_lists.id"), index=True)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    asset_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("assets.id"), nullable=True)
    asset_type: Mapped[AssetType] = mapped_column(Enum(AssetType, name="asset_list_item_type"), index=True)
    asset_code: Mapped[str | None] = mapped_column(String(80), nullable=True, index=True)
    name: Mapped[str] = mapped_column(String(160))
    description: Mapped[str | None] = mapped_column(Text, nullable=True)
    metadata_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    source_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True)
    script_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("scripts.id"), nullable=True)
    script_segment_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("script_segments.id"), nullable=True)
    storyboard_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("storyboards.id"), nullable=True)
    status: Mapped[str] = mapped_column(String(40), default="pending")


class EpisodeAssetBinding(Base, TimestampMixin):
    __tablename__ = "episode_asset_bindings"
    __table_args__ = (UniqueConstraint("application_revision_id", "proposal_index"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    episode_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("project_episodes.id"), index=True)
    script_version_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("script_versions.id"), index=True)
    confirmed_reading_revision_id: Mapped[uuid.UUID] = mapped_column(
        ForeignKey("artifact_revisions.id"), index=True
    )
    application_revision_id: Mapped[uuid.UUID] = mapped_column(
        ForeignKey("artifact_revisions.id"), index=True
    )
    asset_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("assets.id"), index=True)
    asset_revision_id: Mapped[uuid.UUID | None] = mapped_column(
        ForeignKey("artifact_revisions.id"), nullable=True, index=True
    )
    proposal_index: Mapped[int] = mapped_column(Integer)
    action: Mapped[str] = mapped_column(String(40), index=True)
    status: Mapped[str] = mapped_column(String(40), default="active", index=True)
    age_stage_code: Mapped[str | None] = mapped_column(String(20), nullable=True, index=True)
    costume_variant_code: Mapped[str | None] = mapped_column(String(20), nullable=True, index=True)
    proposal_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)


class EntityVersion(Base, TimestampMixin):
    __tablename__ = "entity_versions"
    __table_args__ = (UniqueConstraint("entity_type", "entity_code", "version_no"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("projects.id"), nullable=True, index=True)
    entity_type: Mapped[str] = mapped_column(String(80), index=True)
    entity_id: Mapped[uuid.UUID | None] = mapped_column(UUID(as_uuid=True), nullable=True, index=True)
    entity_code: Mapped[str] = mapped_column(String(120), index=True)
    version_no: Mapped[int] = mapped_column(Integer)
    version_code: Mapped[str] = mapped_column(String(140), unique=True, index=True)
    source: Mapped[str] = mapped_column(String(40), default="agent")
    status: Mapped[str] = mapped_column(String(40), default="draft")
    content_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    change_summary: Mapped[str | None] = mapped_column(Text, nullable=True)
    changed_fields: Mapped[list[str]] = mapped_column(JSONB, default=list)
    base_version_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("entity_versions.id"), nullable=True)
    agent_run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id"), nullable=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    is_current: Mapped[bool] = mapped_column(Boolean, default=True)


class ArtifactRevision(Base, TimestampMixin):
    __tablename__ = "artifact_revisions"
    __table_args__ = (UniqueConstraint("artifact_type", "artifact_id", "version_no"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    artifact_type: Mapped[str] = mapped_column(String(80), index=True)
    artifact_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), index=True)
    version_no: Mapped[int] = mapped_column(Integer)
    parent_revision_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("artifact_revisions.id"), nullable=True)
    source_type: Mapped[str] = mapped_column(String(40), default="agent", index=True)
    source_run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id"), nullable=True)
    skill_name: Mapped[str | None] = mapped_column(String(120), nullable=True)
    skill_version: Mapped[str | None] = mapped_column(String(80), nullable=True)
    contract_version: Mapped[str | None] = mapped_column(String(80), nullable=True)
    contract_hash: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    model_name: Mapped[str | None] = mapped_column(String(120), nullable=True)
    prompt_version: Mapped[str | None] = mapped_column(String(80), nullable=True)
    input_hash: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    input_snapshot: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    raw_output: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    normalized_content: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    change_diff: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    content_hash: Mapped[str] = mapped_column(String(64), index=True)
    status: Mapped[str] = mapped_column(String(40), default="draft", index=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    data_state: Mapped[str] = mapped_column(String(40), default="normalized", index=True)
    payload_ref: Mapped[str | None] = mapped_column(String(512), nullable=True)
    payload_size_bytes: Mapped[int | None] = mapped_column(Integer, nullable=True)
    payload_sha256: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    idempotency_key: Mapped[str | None] = mapped_column(String(64), nullable=True, unique=True)


class AssetCompletionRecord(Base, TimestampMixin):
    __tablename__ = "asset_completion_records"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    asset_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("assets.id"), index=True)
    asset_code: Mapped[str | None] = mapped_column(String(80), nullable=True, index=True)
    age_stage_code: Mapped[str | None] = mapped_column(String(20), nullable=True, index=True)
    costume_variant_code: Mapped[str | None] = mapped_column(String(20), nullable=True, index=True)
    target_field: Mapped[str] = mapped_column(String(120), index=True)
    missing_reason: Mapped[str | None] = mapped_column(String(120), nullable=True)
    original_uncertainty: Mapped[str | None] = mapped_column(Text, nullable=True)
    ai_suggestion: Mapped[str | None] = mapped_column(Text, nullable=True)
    human_description: Mapped[str | None] = mapped_column(Text, nullable=True)
    selected_scenes: Mapped[list[dict[str, Any]]] = mapped_column(JSONB, default=list)
    correction_type: Mapped[str] = mapped_column(String(60), default="field_completion")
    resolution_mode: Mapped[str] = mapped_column(String(40), default="human_revision", index=True)
    status: Mapped[str] = mapped_column(String(40), default="completed", index=True)
    source_agent_revision_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("artifact_revisions.id"), nullable=True)
    base_asset_revision_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("artifact_revisions.id"), nullable=True)
    applied_asset_revision_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("artifact_revisions.id"), nullable=True)
    downstream_status: Mapped[str] = mapped_column(String(40), default="pending")
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class AgentTrainingSample(Base, TimestampMixin):
    __tablename__ = "agent_training_samples"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("projects.id"), nullable=True, index=True)
    agent_type: Mapped[AgentKind | None] = mapped_column(Enum(AgentKind, name="agent_kind"), nullable=True, index=True)
    sample_type: Mapped[str] = mapped_column(String(80), index=True)
    entity_type: Mapped[str | None] = mapped_column(String(80), nullable=True, index=True)
    entity_code: Mapped[str | None] = mapped_column(String(120), nullable=True, index=True)
    input_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    agent_output_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    human_modified_output_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    confirmed_output_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    change_summary: Mapped[list[str]] = mapped_column(JSONB, default=list)
    quality_score: Mapped[int | None] = mapped_column(Integer, nullable=True)
    training_tags: Mapped[list[str]] = mapped_column(JSONB, default=list)
    training_ready: Mapped[bool] = mapped_column(Boolean, default=False)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    source_run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id"), nullable=True, index=True)
    payload_ref: Mapped[str | None] = mapped_column(String(512), nullable=True)
    payload_size_bytes: Mapped[int | None] = mapped_column(Integer, nullable=True)
    payload_sha256: Mapped[str | None] = mapped_column(String(64), nullable=True, index=True)
    data_state: Mapped[str] = mapped_column(String(40), default="final", index=True)
    # P3 配对键：把 agent 原始候选与人工终版按 (作用域, step, skill) 关联，供二期训练
    # 按 skill 分组配对 agent↔human 差异。episode 作用域为 p:{project}|e:{episode}|v:{version}|s:{step}|k:{skill}；
    # 资产作用域(项目级母版，无 episode/version)为 p:{project}|a:{asset_code}|s:asset_extract|k:asset-extract。
    lineage_key: Mapped[str | None] = mapped_column(String(200), nullable=True, index=True)


class FileObject(Base, TimestampMixin):
    __tablename__ = "files"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    file_code: Mapped[str | None] = mapped_column(String(80), unique=True, nullable=True, index=True)
    bucket: Mapped[str] = mapped_column(String(120))
    object_key: Mapped[str] = mapped_column(String(512), unique=True)
    file_name: Mapped[str] = mapped_column(String(255))
    mime_type: Mapped[str | None] = mapped_column(String(120), nullable=True)
    file_size: Mapped[int | None] = mapped_column(Integer, nullable=True)
    checksum: Mapped[str | None] = mapped_column(String(128), nullable=True)
    uploaded_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    project_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("projects.id"), nullable=True, index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True, index=True)
    task_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("tasks.id"), nullable=True, index=True)


class AssetVersion(Base, TimestampMixin):
    __tablename__ = "asset_versions"
    __table_args__ = (UniqueConstraint("asset_id", "version_no"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    asset_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("assets.id"), index=True)
    version_no: Mapped[int] = mapped_column(Integer)
    asset_version_code: Mapped[str] = mapped_column(String(100), unique=True, index=True)
    file_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("files.id"), nullable=True)
    preview_file_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("files.id"), nullable=True)
    prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    negative_prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    base_model: Mapped[str | None] = mapped_column(String(120), nullable=True)
    tool_name: Mapped[str | None] = mapped_column(String(120), nullable=True)
    metadata_json: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)
    is_current: Mapped[bool] = mapped_column(Boolean, default=True)
    source_task_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("tasks.id"), nullable=True)
    source_agent_run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id"), nullable=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)
    source_submission_id: Mapped[uuid.UUID | None] = mapped_column(
        ForeignKey("submissions.id", ondelete="SET NULL"), nullable=True, unique=True, index=True
    )
    source_master_submission_id: Mapped[uuid.UUID | None] = mapped_column(
        ForeignKey("submissions.id", ondelete="SET NULL"), nullable=True, index=True
    )
    is_invalidated: Mapped[bool] = mapped_column(Boolean, default=False, index=True)
    invalidated_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    invalidated_by_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"), nullable=True)
    invalidated_reason: Mapped[str | None] = mapped_column(Text, nullable=True)
    invalidated_source_id: Mapped[uuid.UUID | None] = mapped_column(
        ForeignKey("submissions.id", ondelete="SET NULL"), nullable=True
    )
    purged_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)


class ImageOutput(Base, TimestampMixin):
    __tablename__ = "image_outputs"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True)
    script_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("scripts.id"), nullable=True)
    storyboard_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("storyboards.id"), nullable=True)
    task_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("tasks.id"), nullable=True)
    asset_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("assets.id"), nullable=True)
    image_code: Mapped[str] = mapped_column(String(100), unique=True, index=True)
    file_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("files.id"), nullable=True)
    prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    model_name: Mapped[str | None] = mapped_column(String(120), nullable=True)
    status: Mapped[str] = mapped_column(String(40), default="uploaded")
    version_no: Mapped[int] = mapped_column(Integer, default=1)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class VideoOutput(Base, TimestampMixin):
    __tablename__ = "video_outputs"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True)
    script_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("scripts.id"), nullable=True)
    storyboard_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("storyboards.id"), nullable=True)
    task_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("tasks.id"), nullable=True)
    asset_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("assets.id"), nullable=True)
    video_code: Mapped[str] = mapped_column(String(100), unique=True, index=True)
    video_type: Mapped[str] = mapped_column(String(40), default="image_to_video")
    file_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("files.id"), nullable=True)
    prompt_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    model_name: Mapped[str | None] = mapped_column(String(120), nullable=True)
    tool_name: Mapped[str | None] = mapped_column(String(120), nullable=True)
    status: Mapped[str] = mapped_column(String(40), default="uploaded")
    version_no: Mapped[int] = mapped_column(Integer, default=1)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class FinalOutput(Base, TimestampMixin):
    __tablename__ = "final_outputs"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    project_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("projects.id"), index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True)
    task_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("tasks.id"), nullable=True)
    asset_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("assets.id"), nullable=True)
    final_code: Mapped[str] = mapped_column(String(100), unique=True, index=True)
    name: Mapped[str] = mapped_column(String(160))
    file_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("files.id"), nullable=True)
    tool_names: Mapped[list[str]] = mapped_column(JSONB, default=list)
    status: Mapped[str] = mapped_column(String(40), default="submitted")
    version_no: Mapped[int] = mapped_column(Integer, default=1)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class AssetRelation(Base, TimestampMixin):
    __tablename__ = "asset_relations"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    asset_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("assets.id"), index=True)
    project_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("projects.id"), nullable=True, index=True)
    episode_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("project_episodes.id"), nullable=True)
    script_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("scripts.id"), nullable=True)
    storyboard_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("storyboards.id"), nullable=True)
    image_output_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("image_outputs.id"), nullable=True)
    video_output_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("video_outputs.id"), nullable=True)
    final_output_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("final_outputs.id"), nullable=True)
    relation_type: Mapped[str] = mapped_column(String(80), default="reference_asset")


class TaskPrompt(Base, TimestampMixin):
    __tablename__ = "task_prompts"
    __table_args__ = (UniqueConstraint("task_id", "version_no"),)

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    task_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("tasks.id"), index=True)
    prompt_type: Mapped[str] = mapped_column(String(60), default="agent_initial")
    prompt_text: Mapped[str] = mapped_column(Text)
    version_no: Mapped[int] = mapped_column(Integer)
    source: Mapped[str] = mapped_column(String(40), default="agent")
    copied_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class AgentIssue(Base, TimestampMixin):
    __tablename__ = "agent_issues"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    run_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("agent_runs.id"), index=True)
    level: Mapped[str] = mapped_column(String(40), default="warning")
    code: Mapped[str] = mapped_column(String(80))
    message: Mapped[str] = mapped_column(Text)
    target_path: Mapped[str | None] = mapped_column(String(255), nullable=True)


class AgentFeedback(Base, TimestampMixin):
    __tablename__ = "agent_feedback"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    run_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("agent_runs.id"), nullable=True, index=True)
    task_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("tasks.id"), nullable=True, index=True)
    asset_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("assets.id"), nullable=True)
    before_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    after_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    feedback_type: Mapped[str] = mapped_column(String(60), default="prompt_edit")
    created_by: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True)


class Notification(Base, TimestampMixin):
    __tablename__ = "notifications"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    user_id: Mapped[uuid.UUID] = mapped_column(ForeignKey("users.id"), index=True)
    title: Mapped[str] = mapped_column(String(160))
    content: Mapped[str] = mapped_column(Text)
    type: Mapped[str] = mapped_column(String(60), default="system")
    is_read: Mapped[bool] = mapped_column(Boolean, default=False)


class OperationLog(Base, TimestampMixin):
    __tablename__ = "operation_logs"

    id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    operator_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("users.id"), nullable=True, index=True)
    project_id: Mapped[uuid.UUID | None] = mapped_column(ForeignKey("projects.id"), nullable=True, index=True)
    target_type: Mapped[str] = mapped_column(String(80))
    target_id: Mapped[uuid.UUID | None] = mapped_column(UUID(as_uuid=True), nullable=True)
    action: Mapped[str] = mapped_column(String(80))
    detail: Mapped[dict[str, Any]] = mapped_column(JSONB, default=dict)

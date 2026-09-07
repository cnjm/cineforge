from __future__ import annotations

import copy
import base64
import hashlib
import json
import re
import uuid
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Any
from uuid import UUID

from sqlalchemy import and_, delete, func, or_, select, tuple_, update
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.orm import defer, selectinload

from app.agents.production_context import scope_character_production_context
from app.agents.execution_context import build_resolved_production_brief
from app.agents.breakdown_contract import validate_breakdown_view
from app.models.core import (
    AgentFeedback,
    AgentRun,
    AgentTrainingSample,
    AgentIssue,
    Asset,
    AssetVariantPlan,
    AssetVariantStoryboardLink,
    AssetCompletionRecord,
    AssetRelation,
    AssetVersion,
    ArtifactRevision,
    EpisodeAssetBinding,
    EntityVersion,
    FileObject,
    FinalOutput,
    ImageOutput,
    Notification,
    OperationLog,
    Permission,
    Project,
    ProjectEpisode,
    ProjectAsset,
    ProjectAssetList,
    ProjectAssetListItem,
    Script,
    ScriptVersion,
    ScriptBreakdown,
    ScriptSegment,
    Storyboard,
    StoryboardVersion,
    Submission,
    Task,
    TaskDependency,
    TaskSubmissionBatch,
    TaskPrompt,
    User,
    WorkflowRun,
    VideoOutput,
)
from app.models.enums import AgentKind, AgentRunStatus, AssetType, ProjectStatus, TaskStatus, TaskType, UserRole
from app.schemas.agents import (
    AgentFeedbackCreate,
    AgentFeedbackRead,
    AgentRunMaterializeResponse,
    AgentRunPage,
    AgentRunRead,
    AgentRunSummaryRead,
    AgentTrainingSampleRead,
    WorkflowRunPage,
    WorkflowRunRead,
    WorkflowRunSummaryRead,
)
from app.services.cache import (
    acquire_workflow_lock,
    cache_json_get,
    cache_json_set,
    invalidate_project_status,
    publish_project_event,
    release_workflow_lock,
)
from app.services.storage import load_json_payload, store_json_payload
from app.schemas.asset_normalization import AssetNormalizationResultV1
from app.services.asset_normalization import formalize_asset_inventory_v1
from app.schemas.domain import (
    AssetCreate,
    TemporaryAssetCreate,
    TemporaryAssetProductionRead,
    AssetLibraryHierarchy,
    AssetLibraryItemRead,
    AssetLibraryVersionRead,
    AssetCompletionCreate,
    AssetCompletionRead,
    AssetChangeApplicationRead,
    AssetRead,
    AssetVariantPlanCreate,
    AssetVariantPlanRead,
    AssetVariantPlanUpdate,
    BreakdownLockRequest,
    BreakdownLockResponse,
    BreakdownRead,
    BreakdownSaveRequest,
    DashboardSummary,
    DependencyAssetRead,
    DependencyFileRead,
    DependencySummaryRead,
    ProductionBoardRow,
    ProjectMemberRead,
    ProjectMemberRole,
    ProjectMemberUpsert,
    PrimarySubmissionRequest,
    NotificationRead,
    SubmissionCreate,
    SubmissionMetadataUpdate,
    SubmissionArchiveRequest,
    SubmissionBatchRead,
    SubmissionRead,
    StoryboardVideoTaskCreate,
    StoryboardVideoTaskCreateResponse,
    StoryboardUpdate,
    ScriptSegmentsConfirmResponse,
    SceneAssetOption,
    TaskPromptCreate,
    TaskPromptRead,
    TaskRead,
    TaskBulkReviewRequest,
    TaskBulkSubmitRequest,
    TaskReviewRequest,
    TaskSubmitRequest,
    TaskUpdate,
    UserCreate,
    UserPasswordResetResponse,
    UserRead,
    UserStatusUpdate,
)
from app.schemas.projects import (
    EpisodeAssetBindingRead,
    ProjectCreate,
    ProjectEpisodeDeleteResponse,
    ProjectEpisodeDetail,
    ProjectEpisodeRead,
    ProjectImportContext,
    ProjectRead,
    ProjectUpdate,
    ScriptVersionSummary,
    ScriptVersionDetail,
    StoryboardRead,
)
from app.schemas.script_breakdown import (
    AgentTrainingSampleCreate,
    EntityVersionCreate,
    EntityVersionRead,
    ScriptSegmentCreate,
    ScriptSegmentRead,
)
from app.services.user_sync import emit_user_sync_events
from app.config import settings
from app.security import default_password_for_phone, hash_password, verify_password


PROJECT_ROLE_PREFIX = "project_role:"
PROJECT_MANAGE_ROLES = {ProjectMemberRole.owner, ProjectMemberRole.manager}
PROJECT_WRITE_ROLES = {ProjectMemberRole.owner, ProjectMemberRole.manager, ProjectMemberRole.lead}
APPROVED_SUBMISSION_STATUSES = {"primary_master", "alternate_master"}


class BreakdownVersionConflict(Exception):
    """Raised when a breakdown save targets a stale version — a newer revision for
    the same scope already exists, so accepting the write would silently overwrite
    a concurrent editor's changes. Mapped to HTTP 409 at the route layer."""

    def __init__(self, expected_version: int, current_version: int) -> None:
        self.expected_version = expected_version
        self.current_version = current_version
        super().__init__(
            f"breakdown version conflict: expected {expected_version}, current {current_version}"
        )


def user_to_read(user: User) -> UserRead:
    return UserRead(
        id=user.id,
        username=user.username,
        phone=user.phone,
        name=user.name,
        display_name=user.display_name,
        role=user.role,
        is_active=user.is_active,
    )


def project_to_read(project: Project, storyboard_count: int = 0, task_count: int = 0) -> ProjectRead:
    return ProjectRead(
        id=project.id,
        project_no=project.project_no,
        project_prefix=project.project_prefix,
        name=project.name,
        title=project.title,
        genre=project.genre,
        status=project.status,
        current_stage=project.current_stage,
        manager_id=project.manager_id,
        script_text=project.script_text,
        production_brief=project.production_brief,
        archived_at=project.archived_at,
        deleted_at=project.deleted_at,
        storyboard_count=storyboard_count,
        task_count=task_count,
        created_at=project.created_at,
        updated_at=project.updated_at,
    )


def _storyboard_back_translation(storyboard: Storyboard | None) -> str | None:
    if storyboard is None:
        return None
    return "\n".join(
        str(item.get("dialogue_back_translation") or "").strip()
        for item in (storyboard.mirror_shots or [])
        if isinstance(item, dict) and str(item.get("dialogue_back_translation") or "").strip()
    ) or None


def storyboard_to_read(storyboard: Storyboard) -> StoryboardRead:
    return StoryboardRead(
        id=storyboard.id,
        project_id=storyboard.project_id,
        script_segment_id=storyboard.script_segment_id,
        episode_num=storyboard.episode_num,
        order_num=storyboard.order_num,
        storyboard_code=storyboard.storyboard_code,
        title=storyboard.title,
        description=storyboard.description,
        dialogue=storyboard.dialogue,
        dialogue_back_translation=_storyboard_back_translation(storyboard),
        camera=storyboard.camera,
        shot_type=storyboard.shot_type,
        duration_seconds=storyboard.duration_seconds,
        characters=storyboard.characters or [],
        keyframes=storyboard.keyframes or [],
        mirror_shots=storyboard.mirror_shots or [],
        scene_code=storyboard.scene_code,
        scene_name=storyboard.scene_name,
        context_code=storyboard.context_code,
        render_mode=storyboard.render_mode,
        status=storyboard.status,
    )


def submission_to_read(submission: Submission) -> SubmissionRead:
    return SubmissionRead(
        id=submission.id,
        task_id=submission.task_id,
        batch_id=submission.batch_id,
        file_id=submission.file_id,
        storyboard_id=submission.storyboard_id,
        file_path=submission.file_path,
        file_type=submission.file_type,
        step=submission.step,
        prompt_text=submission.prompt_text,
        original_prompt_text=submission.original_prompt_text,
        revised_prompt_text=submission.revised_prompt_text,
        view_label=submission.view_label,
        state_label=submission.state_label,
        description=submission.description,
        model_name=submission.model_name,
        tool_names=submission.tool_names or [],
        submitted_by_id=submission.submitted_by_id,
        status=submission.status,
        is_selected=submission.is_selected,
        is_primary=submission.is_primary,
        is_archived=submission.is_archived,
        archived_by_id=submission.archived_by_id,
        archived_at=submission.archived_at,
        source_master_submission_id=submission.source_master_submission_id,
        is_invalidated=bool(submission.is_invalidated),
        invalidated_at=submission.invalidated_at,
        invalidated_by_id=submission.invalidated_by_id,
        invalidated_reason=submission.invalidated_reason,
        invalidated_source_id=submission.invalidated_source_id,
        purged_at=submission.purged_at,
        created_at=submission.created_at,
    )


def submission_batch_to_read(batch: TaskSubmissionBatch) -> SubmissionBatchRead:
    return SubmissionBatchRead(
        id=batch.id,
        task_id=batch.task_id,
        step=batch.step,
        version_no=batch.version_no,
        status=batch.status,
        submitted_by_id=batch.submitted_by_id,
        submitted_at=batch.submitted_at,
        reviewed_by_id=batch.reviewed_by_id,
        reviewed_at=batch.reviewed_at,
        review_comment=batch.review_comment,
        submit_request_id=batch.submit_request_id,
        review_request_id=batch.review_request_id,
        created_at=batch.created_at,
        updated_at=batch.updated_at,
    )


def task_to_read(
    task: Task,
    *,
    storyboard: Storyboard | None = None,
    episode_code: str | None = None,
    locked: bool = False,
    dependency_task_ids: list[UUID] | None = None,
    dependency_asset_codes: list[str] | None = None,
    linked_storyboard_ids: list[UUID] | None = None,
    dependency_summary: DependencySummaryRead | None = None,
    dependency_assets: list[DependencyAssetRead] | None = None,
) -> TaskRead:
    assignee_name = task.assignee.display_name if hasattr(task, "assignee") and task.assignee else None
    submissions = [submission_to_read(item) for item in getattr(task, "submissions", [])]
    submission_batches = [submission_batch_to_read(item) for item in getattr(task, "submission_batches", [])]
    steps = {
        str(getattr(item, "step", None) or "").lower()
        for item in getattr(task, "submissions", [])
        if (
            item.status in APPROVED_SUBMISSION_STATUSES
            and item.is_selected
            and not item.is_archived
            and not item.is_invalidated
        )
    }
    keyframe_done = "keyframe" in steps
    video_done = "video" in steps
    return TaskRead(
        id=task.id,
        project_id=task.project_id,
        episode_id=task.episode_id,
        episode_code=episode_code or (f"EP{storyboard.episode_num:02d}" if storyboard else None),
        script_id=task.script_id,
        script_version_id=task.script_version_id,
        storyboard_id=task.storyboard_id,
        asset_id=task.asset_id,
        script_segment_id=task.script_segment_id,
        scene_code=task.scene_code,
        scene_name=task.scene_name,
        storyboard_code=storyboard.storyboard_code if storyboard else None,
        storyboard_duration_seconds=storyboard.duration_seconds if storyboard else None,
        storyboard_dialogue=storyboard.dialogue if storyboard else None,
        storyboard_dialogue_back_translation=_storyboard_back_translation(storyboard),
        storyboard_mirror_shots=[
            dict(item) for item in (storyboard.mirror_shots or []) if isinstance(item, dict)
        ] if storyboard else [],
        task_type=task.task_type,
        title=task.title,
        assignee_id=task.assignee_id,
        assignee_name=assignee_name,
        status=task.status,
        prompt_text=task.prompt_text,
        latest_prompt_text=task.latest_prompt_text,
        due_at=task.due_at,
        completed_at=task.completed_at,
        production_model=task.production_model,
        media_type=task.media_type,
        task_variant=task.task_variant,
        age_stage_code=task.age_stage_code,
        costume_variant_code=task.costume_variant_code,
        variant_plan_id=task.variant_plan_id,
        variant_kind=task.variant_kind,
        variant_title_zh=task.variant_title_zh,
        variant_description_zh=task.variant_description_zh,
        linked_storyboard_ids=linked_storyboard_ids or [],
        prompt_revision_id=task.prompt_revision_id,
        asset_context_outdated=task.asset_context_outdated,
        depends_on_task_id=task.depends_on_task_id,
        is_retired=bool(task.is_retired),
        retired_at=task.retired_at,
        retired_by=task.retired_by,
        retired_reason=task.retired_reason,
        dependency_task_ids=dependency_task_ids or [],
        dependency_asset_codes=dependency_asset_codes or [],
        dependency_summary=dependency_summary or DependencySummaryRead(),
        dependency_assets=dependency_assets or [],
        keyframe_done=keyframe_done,
        video_done=video_done,
        locked=locked,
        submissions=submissions,
        submission_batches=submission_batches,
        created_at=task.created_at,
        updated_at=task.updated_at,
    )


def task_prompt_to_read(prompt: TaskPrompt) -> TaskPromptRead:
    return TaskPromptRead(
        id=prompt.id,
        task_id=prompt.task_id,
        prompt_type=prompt.prompt_type,
        prompt_text=prompt.prompt_text,
        source=prompt.source,
        copied=bool(prompt.copied_at),
        created_by=prompt.created_by,
        version_no=prompt.version_no,
        created_at=prompt.created_at,
    )


_ASSET_BREAKDOWN_FIELDS = (
    "normalization_version",
    "client_asset_key",
    "display_code",
    "display_label",
    "code_state",
    "attributes",
    "relations",
    "review_items",
    "source",
    "repairs",
    "priority",
)


def _normalized_asset_metadata(item: dict[str, Any]) -> dict[str, Any]:
    return {
        key: item[key]
        for key in _ASSET_BREAKDOWN_FIELDS
        if key in item and item[key] is not None
    }


def _metadata_without_breakdown_asset(metadata: dict[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in metadata.items() if key != "breakdown_asset"}


def _asset_list_age_stages(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    stages: list[dict[str, Any]] = []
    for item in value:
        if not isinstance(item, dict):
            continue
        stage = {
            key: item[key]
            for key in ("stage_code", "name")
            if item.get(key) is not None
        }
        variants = []
        for variant in item.get("costume_variants") or []:
            if isinstance(variant, dict):
                variants.append({
                    key: variant[key]
                    for key in ("variant_code", "name")
                    if variant.get(key) is not None
                })
        if variants:
            stage["costume_variants"] = variants
        if stage:
            stages.append(stage)
    return stages


def _asset_list_metadata(metadata: dict[str, Any]) -> dict[str, Any]:
    """Keep list payloads useful without repeating nested agent artifacts."""
    summary: dict[str, Any] = {}
    for key, value in metadata.items():
        if key == "age_stages":
            stages = _asset_list_age_stages(value)
            if stages:
                summary[key] = stages
            continue
        if value is None or isinstance(value, (bool, int, float)):
            summary[key] = value
        elif isinstance(value, str) and len(value) <= 2048:
            summary[key] = value
        elif (
            isinstance(value, list)
            and len(value) <= 50
            and all(
                item is None
                or isinstance(item, (bool, int, float))
                or (isinstance(item, str) and len(item) <= 512)
                for item in value
            )
        ):
            summary[key] = value
    return summary


def asset_to_read(asset: Asset, *, include_metadata: bool = True) -> AssetRead:
    return AssetRead(
        id=asset.id,
        project_id=asset.project_id,
        asset_code=asset.asset_code,
        status=asset.status,
        asset_type=asset.asset_type,
        name=asset.name,
        description=asset.description,
        tags=asset.tags or [],
        preview_path=asset.preview_path,
        file_path=asset.file_path,
        prompt_text=asset.prompt_text,
        base_model=asset.base_model,
        metadata=(asset.metadata_json or {}) if include_metadata else _asset_list_metadata(asset.metadata_json or {}),
        version=asset.version,
        created_by_id=asset.created_by_id,
        created_at=asset.created_at,
        updated_at=asset.updated_at,
    )


def script_segment_to_read(segment: ScriptSegment) -> ScriptSegmentRead:
    return ScriptSegmentRead(
        id=segment.id,
        project_id=segment.project_id,
        episode_id=segment.episode_id,
        script_segment_code=segment.script_segment_code,
        episode_code=segment.episode_code,
        order_no=segment.order_no,
        title=segment.title,
        source_text=segment.source_text,
        summary=segment.summary,
        story_function=segment.story_function,
        dominant_emotion=segment.dominant_emotion,
        rhythm=segment.rhythm,
        viewpoint=segment.viewpoint,
        context_code=segment.context_code,
        render_mode=segment.render_mode,
        metadata_json=segment.metadata_json or {},
        current_version_id=segment.current_version_id,
        created_by=segment.created_by,
        created_at=segment.created_at,
        updated_at=segment.updated_at,
    )


def entity_version_to_read(version: EntityVersion) -> EntityVersionRead:
    return EntityVersionRead(
        id=version.id,
        project_id=version.project_id,
        entity_type=version.entity_type,
        entity_id=version.entity_id,
        entity_code=version.entity_code,
        version_no=version.version_no,
        version_code=version.version_code,
        source=version.source,
        status=version.status,
        content_json=version.content_json or {},
        change_summary=version.change_summary,
        changed_fields=version.changed_fields or [],
        base_version_id=version.base_version_id,
        agent_run_id=version.agent_run_id,
        created_by=version.created_by,
        is_current=version.is_current,
        created_at=version.created_at,
        updated_at=version.updated_at,
    )


async def add_operation_log(
    session: AsyncSession,
    *,
    action: str,
    target_type: str,
    operator_id: UUID | None = None,
    project_id: UUID | None = None,
    target_id: UUID | None = None,
    detail: dict[str, Any] | None = None,
    commit: bool = True,
) -> None:
    session.add(
        OperationLog(
            operator_id=operator_id,
            project_id=project_id,
            target_type=target_type,
            target_id=target_id,
            action=action,
            detail=detail or {},
        )
    )
    if commit:
        await session.commit()


async def ensure_initial_users(session: AsyncSession) -> None:
    initial_users = [
        ("18066639366", "戴磊", UserRole.admin),
        ("15829753113", "王定泽", UserRole.director),
        ("18152013605", "张海洋", UserRole.director),
        ("15249182271", "王穆君", UserRole.artist),
        ("15675620379", "王祥", UserRole.artist),
    ]
    created_users: dict[UserRole, User] = {}
    changed_users: list[User] = []
    for phone, name, role in initial_users:
        user = await session.scalar(select(User).where(User.phone == phone))
        if user is None:
            user = User(
                username=phone,
                phone=phone,
                name=name,
                display_name=name,
                role=role,
                password_hash=hash_password(default_password_for_phone(phone)),
                is_active=True,
            )
            session.add(user)
            changed_users.append(user)
        else:
            before = (
                user.username or "",
                user.phone,
                user.name or "",
                user.display_name or "",
                user.role,
                user.is_active,
            )
            user.username = user.username or phone
            user.phone = phone
            user.name = user.name or name
            user.display_name = user.display_name or name
            user.role = role
            user.password_hash = user.password_hash or hash_password(default_password_for_phone(phone))
            user.is_active = True
            after = (
                user.username or "",
                user.phone,
                user.name or "",
                user.display_name or "",
                user.role,
                user.is_active,
            )
            if before != after:
                changed_users.append(user)
        created_users.setdefault(role, user)
    for user in changed_users:
        await emit_user_sync_events(session, user)
    await session.commit()

async def list_users(session: AsyncSession) -> list[UserRead]:
    result = await session.execute(select(User).order_by(User.created_at))
    return [user_to_read(item) for item in result.scalars().all()]


async def create_user(session: AsyncSession, data: UserCreate) -> UserRead:
    phone = data.phone.strip()
    name = (data.name or data.display_name or phone).strip()
    password = data.password or default_password_for_phone(phone)
    user = User(
        username=phone,
        phone=phone,
        name=name,
        display_name=name,
        role=data.role,
        password_hash=hash_password(password),
        is_active=data.is_active,
    )
    session.add(user)
    await emit_user_sync_events(session, user)
    await session.commit()
    await session.refresh(user)
    return user_to_read(user)


async def set_user_active(session: AsyncSession, user_id: UUID, data: UserStatusUpdate) -> UserRead:
    user = await session.get(User, user_id)
    if user is None:
        raise KeyError(user_id)
    user.is_active = data.is_active
    await emit_user_sync_events(session, user)
    await session.commit()
    await session.refresh(user)
    return user_to_read(user)


async def reset_user_password(session: AsyncSession, user_id: UUID, password: str | None = None) -> UserPasswordResetResponse:
    user = await session.get(User, user_id)
    if user is None:
        raise KeyError(user_id)
    temporary_password = password or default_password_for_phone(user.phone or user.username)
    user.password_hash = hash_password(temporary_password)
    await session.commit()
    await session.refresh(user)
    return UserPasswordResetResponse(user=user_to_read(user), temporary_password=temporary_password)


async def change_user_password(session: AsyncSession, user: User, new_password: str) -> UserRead:
    user.password_hash = hash_password(new_password)
    await session.commit()
    await session.refresh(user)
    return user_to_read(user)


async def list_project_members(session: AsyncSession, project_id: UUID, user: User) -> list[ProjectMemberRead]:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    rows = (
        await session.execute(
            select(Permission, User)
            .join(User, User.id == Permission.user_id)
            .where(Permission.project_id == project_id, Permission.permission_type.like(f"{PROJECT_ROLE_PREFIX}%"))
            .order_by(User.created_at)
        )
    ).all()
    members: list[ProjectMemberRead] = []
    for permission, member in rows:
        members.append(
            ProjectMemberRead(
                project_id=project_id,
                user_id=member.id,
                role=ProjectMemberRole(permission.permission_type.split(":", 1)[1]),
                user=user_to_read(member),
            )
        )
    return members


async def upsert_project_member(session: AsyncSession, project_id: UUID, data: ProjectMemberUpsert, user: User) -> ProjectMemberRead:
    project = await session.get(Project, project_id)
    member = await session.get(User, data.user_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    if member is None:
        raise KeyError(data.user_id)
    if not await _can_manage_project_members(session, project_id, user):
        raise PermissionError(project_id)
    existing_roles = (
        await session.execute(
            select(Permission).where(
                Permission.project_id == project_id,
                Permission.user_id == data.user_id,
                Permission.permission_type.like(f"{PROJECT_ROLE_PREFIX}%"),
            )
        )
    ).scalars().all()
    permission_type = f"{PROJECT_ROLE_PREFIX}{data.role.value}"
    if existing_roles:
        primary = existing_roles[0]
        primary.permission_type = permission_type
        for duplicate in existing_roles[1:]:
            await session.delete(duplicate)
    else:
        session.add(Permission(user_id=data.user_id, project_id=project_id, permission_type=permission_type))
    await session.commit()
    return ProjectMemberRead(project_id=project_id, user_id=member.id, role=data.role, user=user_to_read(member))


async def remove_project_member(session: AsyncSession, project_id: UUID, user_id: UUID, user: User) -> None:
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    if not await _can_manage_project_members(session, project_id, user):
        raise PermissionError(project_id)
    permissions = (
        await session.execute(
            select(Permission).where(
                Permission.project_id == project_id,
                Permission.user_id == user_id,
                Permission.permission_type.like(f"{PROJECT_ROLE_PREFIX}%"),
            )
        )
    ).scalars().all()
    if not permissions:
        raise KeyError(user_id)
    for permission in permissions:
        await session.delete(permission)
    await session.commit()


async def ensure_project_write_access(session: AsyncSession, project_id: UUID, user: User) -> None:
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    if not await _can_write_project(session, project_id, user):
        raise PermissionError(project_id)


async def ensure_task_update_access(session: AsyncSession, task_id: UUID, user: User) -> TaskRead:
    task = await _get_active_asset_task(session, task_id)
    if task is None:
        raise KeyError(task_id)
    if not await _can_update_task(session, task, user):
        raise PermissionError(task_id)
    return await get_task(session, task_id, user)


async def list_assets(
    session: AsyncSession,
    *,
    user: User,
    asset_type: AssetType | None = None,
    search: str | None = None,
    project_id: UUID | None = None,
    include_metadata: bool = True,
) -> list[AssetRead]:
    stmt = select(Asset).where(Asset.status.notin_(["deleted", "excluded"])).order_by(Asset.created_at.desc())
    if asset_type:
        stmt = stmt.where(Asset.asset_type == asset_type)
    if search:
        pattern = f"%{search.strip()}%"
        stmt = stmt.where(or_(Asset.name.ilike(pattern), Asset.description.ilike(pattern), Asset.asset_code.ilike(pattern)))
    if project_id:
        if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
            raise PermissionError(project_id)
        stmt = stmt.where(Asset.project_id == project_id)
    elif not is_director_or_admin(user):
        project_ids = await _visible_task_project_ids(session, user)
        if not project_ids:
            stmt = stmt.where(Asset.project_id.is_(None))
        else:
            stmt = stmt.where(or_(Asset.project_id.in_(project_ids), Asset.project_id.is_(None)))
    result = await session.execute(stmt)
    return [asset_to_read(item, include_metadata=include_metadata) for item in result.scalars().all()]


async def list_asset_prompt_revisions(
    session: AsyncSession,
    asset_id: UUID,
    user: User,
) -> list[dict[str, Any]]:
    asset = await session.get(Asset, asset_id)
    if asset is None:
        raise KeyError(asset_id)
    if str(asset.status) in _INACTIVE_TASK_ASSET_STATUSES:
        raise KeyError(asset_id)
    if not is_director_or_admin(user) and not await _can_access_project(session, asset.project_id, user):
        raise PermissionError(asset_id)
    revisions = list((await session.scalars(
        select(ArtifactRevision)
        .where(ArtifactRevision.artifact_type == "asset_prompt", ArtifactRevision.artifact_id == asset_id)
        .order_by(ArtifactRevision.version_no)
    )).all())
    current_id = revisions[-1].id if revisions else None
    return [
        {
            "id": str(item.id),
            "asset_id": str(asset_id),
            "version_no": item.version_no,
            "context_key": (item.input_snapshot or {}).get("context_key"),
            "asset_prompt_input_fingerprint": (item.input_snapshot or {}).get("asset_prompt_input_fingerprint"),
            "skill": item.skill_name,
            "skill_version": item.skill_version,
            "contract_version": item.contract_version,
            "contract_hash": item.contract_hash,
            "prompt_version": item.prompt_version,
            "input_hash": item.input_hash,
            "brief_trace": (item.input_snapshot or {}).get("brief_trace") or {},
            "input_fields": ((item.input_snapshot or {}).get("brief_trace") or {}).get("input_fields") or [],
            "resolved_production_brief": (item.input_snapshot or {}).get("resolved_production_brief") or {},
            "production_context": (item.input_snapshot or {}).get("production_context") or {},
            "parent_revision_id": str(item.parent_revision_id) if item.parent_revision_id else None,
            "supersedes_revision_id": (item.change_diff or {}).get("supersedes_revision_id"),
            "rerun_of_revision_id": (
                (item.change_diff or {}).get("rerun_of_revision_id")
                or (item.input_snapshot or {}).get("rerun_of_revision_id")
            ),
            "is_current": item.id == current_id,
            "prompt": (item.normalized_content or {}).get("prompt"),
            "negative_prompt": (item.normalized_content or {}).get("negative_prompt"),
            "aspect_ratio": (item.normalized_content or {}).get("aspect_ratio"),
            "view_prompts": (item.normalized_content or {}).get("view_prompts") or [],
            "state_prompts": (item.normalized_content or {}).get("state_prompts") or [],
            "spatial_bible": (item.normalized_content or {}).get("spatial_bible") or {},
            "material_bible": (item.normalized_content or {}).get("material_bible") or {},
            "status": item.status,
            "created_at": item.created_at.isoformat() if item.created_at else None,
        }
        for item in revisions
    ]


async def get_asset_prompt_revision(
    session: AsyncSession,
    asset_id: UUID,
    revision_id: UUID,
    user: User,
) -> tuple[Asset, ArtifactRevision]:
    asset = await session.get(Asset, asset_id)
    if asset is None:
        raise KeyError(asset_id)
    if str(asset.status) in _INACTIVE_TASK_ASSET_STATUSES:
        raise KeyError(asset_id)
    if not is_director_or_admin(user) and not await _can_access_project(session, asset.project_id, user):
        raise PermissionError(asset_id)
    revision = await session.get(ArtifactRevision, revision_id)
    if revision is None or revision.artifact_type != "asset_prompt" or revision.artifact_id != asset_id:
        raise KeyError(revision_id)
    return asset, revision


async def asset_task_contexts_by_asset(
    session: AsyncSession,
    *,
    project_id: UUID,
    asset_ids: list[UUID],
) -> dict[UUID, list[dict[str, Any]]]:
    """Return existing production-task contexts for a project asset snapshot."""
    if not asset_ids:
        return {}
    tasks = (
        await session.execute(
            select(Task).where(
                Task.project_id == project_id,
                Task.asset_id.in_(asset_ids),
                Task.task_type.in_((TaskType.asset, TaskType.text_to_image)),
                Task.is_retired.is_(False),
            ).order_by(Task.created_at, Task.id)
        )
    ).scalars().all()
    contexts: dict[UUID, list[dict[str, Any]]] = {}
    for task in tasks:
        if task.asset_id is None:
            continue
        contexts.setdefault(task.asset_id, []).append({
            "task_id": str(task.id),
            "task_type": task.task_type.value if hasattr(task.task_type, "value") else str(task.task_type),
            "task_variant": task.task_variant,
            "age_stage_code": task.age_stage_code,
            "costume_variant_code": task.costume_variant_code,
            "status": task.status.value if hasattr(task.status, "value") else str(task.status),
        })
    return contexts


ASSET_TYPE_DISPLAY_CODES = {
    AssetType.character: "CHAR",
    AssetType.scene: "SCENE",
    AssetType.prop: "PROP",
    AssetType.music: "MUSIC",
    AssetType.voice_profile: "VOICE",
    AssetType.storyboard: "STORYBOARD",
    AssetType.storyboard_image: "KEYFRAME",
    AssetType.storyboard_video: "VIDEO",
    AssetType.effect: "EFFECT",
    AssetType.final_video: "FINAL",
}


def _resolve_music_audio_type(audio_type: str | None, asset_code: str | None) -> str:
    """Prefer the explicit audio_type from asset metadata; fall back to asset_code
    parsing only for legacy assets that never stored metadata.audio_type."""
    normalized = str(audio_type or "").strip().lower()
    if normalized in {"theme_music", "theme"}:
        return "theme_music"
    if normalized in {"background_music", "back_music", "background", "back"}:
        return "back_music"
    return "theme_music" if "THEME" in _library_code(asset_code, "") else "back_music"


def _episode_code_from_asset_code(asset_code: str | None) -> str | None:
    """Extract the EPNN segment embedded in an episode-scoped audio asset_code
    (e.g. RF-EP01-BACK-MUSIC -> EP01). Returns None when absent (project-level)."""
    match = re.search(r"(EP\d{2,})", _library_code(asset_code, ""))
    return match.group(1) if match else None


def canonical_asset_display_name(
    *,
    project_prefix: str | None,
    asset_type: AssetType,
    asset_code: str | None,
    version_no: int,
    extension: str,
    task_variant: str | None = None,
    age_stage_code: str | None = None,
    costume_variant_code: str | None = None,
    context_version_no: int | None = None,
    alternate_no: int | None = None,
    audio_type: str | None = None,
    episode_code: str | None = None,
) -> str:
    display_version_no = max(1, context_version_no or version_no)
    alternate = f"-ALT{alternate_no:02d}" if alternate_no else ""
    if asset_type == AssetType.character:
        prefix = _library_code(project_prefix, "RF")
        code = _role_asset_code(asset_code)
        context_parts: list[str] = []
        normalized_age = _age_stage_display_code(age_stage_code)
        normalized_costume = _costume_display_code(costume_variant_code)
        if normalized_age:
            context_parts.append(normalized_age)
        if normalized_costume:
            context_parts.extend(["COSTUME", normalized_costume])
        view = _character_view_code(task_variant)
        context = f"-{'-'.join(context_parts)}" if context_parts else ""
        return (
            f"{prefix}-{code}{context}-VIEW-{view}"
            f"-V{display_version_no:03d}{alternate}.{_library_extension(extension)}"
        )
    if asset_type == AssetType.music:
        resolved_audio_type = _resolve_music_audio_type(audio_type, asset_code)
        return canonical_audio_display_name(
            project_prefix=project_prefix,
            audio_type=resolved_audio_type,
            version_no=display_version_no,
            extension=extension,
            alternate_no=alternate_no,
            episode_code=episode_code or _episode_code_from_asset_code(asset_code),
        )
    if asset_type == AssetType.voice_profile:
        return canonical_audio_display_name(
            project_prefix=project_prefix,
            audio_type="voice",
            role_code=_voice_role_code(asset_code),
            context_code="BASE",
            version_no=display_version_no,
            extension=extension,
            alternate_no=alternate_no,
        )
    artifact_code = asset_artifact_code(
        project_prefix=project_prefix,
        asset_type=asset_type,
        asset_code=asset_code,
    )
    return f"{artifact_code}-V{display_version_no:03d}{alternate}.{_library_extension(extension)}"


def _character_view_code(value: Any) -> str:
    view = _library_code(str(value or "A"), "A")
    return "A" if view in {"MASTER", "A"} else view


def _asset_context_version(
    asset_type: AssetType,
    metadata: dict[str, Any],
    fallback_version_no: int,
    counters: dict[tuple[str, str, str], int],
) -> tuple[int, tuple[str, str, str] | None]:
    if asset_type != AssetType.character:
        return max(1, fallback_version_no), None
    key = (
        _library_code(str(metadata.get("age_stage_code") or "BASE"), "BASE"),
        _library_code(str(metadata.get("costume_variant_code") or "BASE"), "BASE"),
        _character_view_code(metadata.get("task_variant")),
    )
    explicit = _safe_int(
        metadata.get("context_version_no") or metadata.get("batch_version_no"),
        0,
    )
    if explicit > 0:
        counters[key] = max(counters.get(key, 0), explicit)
        return explicit, key
    counters[key] = counters.get(key, 0) + 1
    return counters[key], key


def _asset_alternate_number(
    *,
    is_primary: bool,
    context_key: tuple[str, str, str] | None,
    context_version_no: int,
    counters: dict[tuple[tuple[str, str, str], int], int],
) -> int | None:
    if is_primary or context_key is None:
        return None
    key = (context_key, context_version_no)
    counters[key] = counters.get(key, 0) + 1
    return counters[key]


def asset_artifact_code(
    *,
    project_prefix: str | None,
    asset_type: AssetType,
    asset_code: str | None,
    audio_type: str | None = None,
    episode_code: str | None = None,
) -> str:
    prefix = _library_code(project_prefix, "RF")
    if asset_type == AssetType.character:
        return f"{prefix}-{_role_asset_code(asset_code)}"
    if asset_type == AssetType.scene:
        return f"{prefix}-{_scene_asset_code(asset_code)}-MASTER"
    if asset_type == AssetType.prop:
        return f"{prefix}-{_prop_asset_code(asset_code)}-MASTER"
    if asset_type == AssetType.music:
        return audio_artifact_code(
            project_prefix=project_prefix,
            audio_type=_resolve_music_audio_type(audio_type, asset_code),
            episode_code=episode_code or _episode_code_from_asset_code(asset_code),
        )
    if asset_type == AssetType.voice_profile:
        return audio_artifact_code(
            project_prefix=project_prefix,
            audio_type="voice",
            role_code=_voice_role_code(asset_code),
            context_code="BASE",
        )
    type_code = ASSET_TYPE_DISPLAY_CODES.get(asset_type, "ASSET")
    return f"{prefix}-{type_code}-{_library_leaf_code(asset_code, 'ASSET')}-MASTER"


def canonical_storyboard_output_display_name(
    *,
    project_prefix: str | None,
    episode_code: str | None,
    scene_code: str | None,
    storyboard_code: str | None,
    output_type: str,
    version_no: int,
    extension: str,
    script_segment_code: str | None = None,
    mirror_shot_code: str | None = None,
) -> str:
    artifact_code = storyboard_output_artifact_code(
        project_prefix=project_prefix,
        episode_code=episode_code,
        scene_code=scene_code,
        storyboard_code=storyboard_code,
        output_type=output_type,
        script_segment_code=script_segment_code,
        mirror_shot_code=mirror_shot_code,
    )
    return f"{artifact_code}-V{max(1, version_no):03d}.{_library_extension(extension)}"


def storyboard_output_artifact_code(
    *,
    project_prefix: str | None,
    episode_code: str | None,
    scene_code: str | None,
    storyboard_code: str | None,
    output_type: str,
    script_segment_code: str | None = None,
    mirror_shot_code: str | None = None,
) -> str:
    parts = [
        _library_code(project_prefix, "RF"),
        _library_code(episode_code, "EP00"),
    ]
    if scene_code:
        parts.append(_library_leaf_code(scene_code, "SCENE"))
    parts.extend([
        _script_segment_display_code(script_segment_code),
        _library_leaf_code(storyboard_code, "F000"),
    ])
    normalized_mirror = _mirror_shot_display_code(mirror_shot_code)
    if normalized_mirror:
        parts.append(normalized_mirror)
    if output_type == "video":
        parts.append("VIDEO")
    elif normalized_mirror:
        parts.append("KEYFRAME")
    else:
        parts.extend(["KEYFRAME", "MASTER"])
    return "-".join(parts)


def canonical_audio_display_name(
    *,
    project_prefix: str | None,
    audio_type: str,
    version_no: int,
    extension: str,
    role_code: str | None = None,
    context_code: str | None = None,
    alternate_no: int | None = None,
    episode_code: str | None = None,
) -> str:
    artifact_code = audio_artifact_code(
        project_prefix=project_prefix,
        audio_type=audio_type,
        role_code=role_code,
        context_code=context_code,
        episode_code=episode_code,
    )
    alternate = f"-ALT{alternate_no:02d}" if alternate_no else ""
    return f"{artifact_code}-V{max(1, version_no):03d}{alternate}.{_library_extension(extension)}"


def audio_artifact_code(
    *,
    project_prefix: str | None,
    audio_type: str,
    role_code: str | None = None,
    context_code: str | None = None,
    episode_code: str | None = None,
) -> str:
    prefix = _library_code(project_prefix, "RF")
    normalized_type = _library_code(audio_type, "AUDIO").replace("_", "-")
    if normalized_type in {"THEME", "THEME-MUSIC"}:
        # Theme music is project-level: one master for the whole project, no episode.
        return f"{prefix}-THEME-MUSIC"
    if normalized_type in {"BACK", "BACK-MUSIC", "BACKGROUND", "BACKGROUND-MUSIC"}:
        # Background music is episode-level: keep the episode segment so masters from
        # different episodes do not collide in the asset library (RF-EP01-BACK-MUSIC).
        episode = _library_code(episode_code, "") if episode_code else ""
        return f"{prefix}-{episode}-BACK-MUSIC" if episode else f"{prefix}-BACK-MUSIC"
    if normalized_type in {"VOICE", "ROLE-VOICE", "CHARACTER-VOICE"}:
        role = _role_asset_code(role_code)
        context = _library_code(context_code, "BASE")
        return f"{prefix}-AUDIO-VOICE-{role}-{context}"
    return f"{prefix}-AUDIO-{normalized_type}"


def _role_asset_code(value: str | None) -> str:
    code = _library_leaf_code(value, "R000")
    match = re.fullmatch(r"(?:CHAR|ROLE|R)?0*(\d+)", code)
    return f"R{int(match.group(1)):03d}" if match else code


def _voice_role_code(value: str | None) -> str:
    match = re.search(r"(?:^|-)(R\d+)(?:-|$)", _library_code(value, ""))
    return _role_asset_code(match.group(1) if match else value)


def _scene_asset_code(value: str | None) -> str:
    code = _library_leaf_code(value, "SC000")
    match = re.fullmatch(r"(?:SC|C|S)?0*(\d+)", code)
    return f"SC{int(match.group(1)):03d}" if match else code


def _prop_asset_code(value: str | None) -> str:
    code = _library_leaf_code(value, "P000")
    match = re.fullmatch(r"(?:PROP|P)?0*(\d+)", code)
    return f"P{int(match.group(1)):03d}" if match else code


def _age_stage_display_code(value: str | None) -> str | None:
    if not str(value or "").strip():
        return None
    code = _library_code(value, "AGE00")
    if code == "BASE":
        return None
    match = re.fullmatch(r"(?:AGE)?0*(\d+)", code.replace("-", ""))
    return f"AGE{int(match.group(1)):02d}" if match else code


def _costume_display_code(value: str | None) -> str | None:
    if not str(value or "").strip():
        return None
    code = _library_code(value, "V00")
    if code == "BASE":
        return None
    match = re.fullmatch(r"(?:COSTUME)?(?:V)?0*(\d+)", code.replace("-", ""))
    return f"V{int(match.group(1)):02d}" if match else code


def _script_segment_display_code(value: str | None) -> str:
    code = _library_leaf_code(value, "J000")
    match = re.fullmatch(r"(?:J)?0*(\d+)", code)
    return f"J{int(match.group(1)):03d}" if match else code


def _mirror_shot_display_code(value: str | None) -> str | None:
    if not str(value or "").strip():
        return None
    code = _library_code(value, "")
    match = re.fullmatch(r"(?:MIR|MIRROR)-?([A-Z0-9]+)", code)
    if match:
        return f"MIR-{match.group(1)}"
    return f"MIR-{code}" if code else None


def _library_code(value: str | None, fallback: str) -> str:
    code = re.sub(r"[^A-Za-z0-9]+", "-", str(value or "").upper()).strip("-")
    return code or fallback


def _library_leaf_code(value: str | None, fallback: str) -> str:
    code = _library_code(value, fallback)
    return code.rsplit("-", 1)[-1] or fallback


def _library_extension(value: str | None, fallback: str = "bin") -> str:
    name = str(value or "").split("?", 1)[0].rsplit("/", 1)[-1]
    extension = name.rsplit(".", 1)[-1].lower() if "." in name else name.lower().lstrip(".")
    extension = re.sub(r"[^a-z0-9]+", "", extension)
    return extension or fallback


def _file_location(file_object: FileObject | None, fallback: str | None = None) -> str | None:
    if file_object is not None:
        return f"minio://{file_object.bucket}/{file_object.object_key}"
    return fallback


async def list_asset_library(
    session: AsyncSession,
    *,
    project_id: UUID | None,
    user: User,
    include_versions: bool = False,
) -> list[AssetLibraryItemRead]:
    if project_id is not None:
        project = await session.get(Project, project_id)
        if project is None or project.deleted_at is not None:
            raise KeyError(project_id)
        if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
            raise PermissionError(project_id)
        projects = [project]
    else:
        projects = list((await session.scalars(
            select(Project).where(Project.deleted_at.is_(None)).order_by(Project.created_at)
        )).all())
    items = await _list_projects_asset_library(
        session,
        projects=projects,
        include_versions=include_versions,
    )
    visible = [item for item in items if item.current_master is not None]
    return sorted(visible, key=lambda item: item.latest_version_at, reverse=True)


async def _list_projects_asset_library(
    session: AsyncSession,
    *,
    projects: list[Project],
    include_versions: bool,
) -> list[AssetLibraryItemRead]:
    if not projects:
        return []
    project_ids = [project.id for project in projects]
    prefixes = {
        project.id: project.project_prefix or project.project_no or "RF"
        for project in projects
    }
    project_names = {
        project.id: project.title or project.name or project.project_no or "未命名项目"
        for project in projects
    }
    assets = list((await session.scalars(
        select(Asset).where(
            Asset.project_id.in_(project_ids),
            Asset.status.notin_(["deleted", "excluded"]),
        ).order_by(Asset.project_id, Asset.asset_type, Asset.asset_code, Asset.name)
    )).all())
    asset_ids = [asset.id for asset in assets]
    episode_binding_rows = (await session.execute(
        select(EpisodeAssetBinding.asset_id, ProjectEpisode.episode_code)
        .join(ProjectEpisode, ProjectEpisode.id == EpisodeAssetBinding.episode_id)
        .where(
            EpisodeAssetBinding.asset_id.in_(asset_ids),
            EpisodeAssetBinding.status == "active",
        )
        .distinct()
    )).all() if asset_ids else []
    episode_task_rows = (await session.execute(
        select(Task.asset_id, ProjectEpisode.episode_code)
        .join(ProjectEpisode, ProjectEpisode.id == Task.episode_id)
        .where(
            Task.asset_id.in_(asset_ids),
            Task.episode_id.is_not(None),
            Task.is_retired.is_(False),
        )
        .distinct()
    )).all() if asset_ids else []
    episode_codes_by_asset: dict[UUID, list[str]] = {}
    for asset_id, episode_code in [*episode_binding_rows, *episode_task_rows]:
        codes = episode_codes_by_asset.setdefault(asset_id, [])
        if episode_code and episode_code not in codes:
            codes.append(episode_code)
    for codes in episode_codes_by_asset.values():
        codes.sort()
    for asset in assets:
        temporary_episode = str((asset.metadata_json or {}).get("episode_code") or "").strip()
        if temporary_episode:
            codes = episode_codes_by_asset.setdefault(asset.id, [])
            if temporary_episode not in codes:
                codes.append(temporary_episode)
                codes.sort()
    version_rows = (await session.execute(
        select(AssetVersion, FileObject)
        .outerjoin(FileObject, FileObject.id == func.coalesce(AssetVersion.file_id, AssetVersion.preview_file_id))
        .where(
            AssetVersion.asset_id.in_(asset_ids),
            AssetVersion.is_invalidated.is_(False),
            AssetVersion.purged_at.is_(None),
        )
        .order_by(AssetVersion.asset_id, AssetVersion.version_no)
    )).all() if asset_ids else []
    submission_rows = (await session.execute(
        select(Submission, Task, Storyboard, FileObject, TaskSubmissionBatch)
        .join(Task, Task.id == Submission.task_id)
        .outerjoin(Storyboard, Storyboard.id == Task.storyboard_id)
        .outerjoin(FileObject, FileObject.id == Submission.file_id)
        .outerjoin(TaskSubmissionBatch, TaskSubmissionBatch.id == Submission.batch_id)
        .where(
            Task.project_id.in_(project_ids),
            Task.is_retired.is_(False),
            Submission.status.in_(APPROVED_SUBMISSION_STATUSES),
            Submission.is_selected.is_(True),
            Submission.is_archived.is_(False),
            Submission.is_invalidated.is_(False),
        )
        .order_by(Submission.created_at, Submission.id)
    )).all()
    script_segment_codes = await _script_segment_codes_for_submission_rows(session, submission_rows)
    versions_by_asset: dict[UUID, list[tuple[AssetVersion, FileObject | None]]] = {}
    for version, file_object in version_rows:
        versions_by_asset.setdefault(version.asset_id, []).append((version, file_object))
    submissions_by_asset: dict[UUID, list[tuple[Any, ...]]] = {}
    output_groups: dict[tuple[UUID, UUID, str | None, str], list[tuple[Any, ...]]] = {}
    asset_id_set = set(asset_ids)
    for row in submission_rows:
        submission, task, storyboard, _file_object, _batch = row
        if not _is_approved_library_submission(submission):
            continue
        if task.asset_id in asset_id_set:
            submissions_by_asset.setdefault(task.asset_id, []).append(row)
            continue
        if storyboard is None:
            continue
        normalized_step = str(submission.step or "").lower()
        output_type = "video" if "video" in normalized_step or submission.file_type == "video" else "keyframe"
        mirror_shot_code = _submission_mirror_shot_code(submission, task)
        output_groups.setdefault((task.project_id, storyboard.id, mirror_shot_code, output_type), []).append(row)

    items = [
        _asset_library_item(
            asset,
            prefixes.get(asset.project_id, "RF"),
            versions_by_asset.get(asset.id, []),
            submissions_by_asset.get(asset.id, []),
            project_name=project_names.get(asset.project_id),
            episode_codes=episode_codes_by_asset.get(asset.id, []),
            include_versions=include_versions,
        )
        for asset in assets
        if submissions_by_asset.get(asset.id)
    ]
    items.extend(
        _storyboard_library_item(
            prefixes.get(project_key, "RF"),
            output_type,
            rows,
            project_name=project_names.get(project_key),
            script_segment_codes=script_segment_codes,
            include_versions=include_versions,
        )
        for (project_key, _storyboard_id, _mirror_shot_code, output_type), rows in output_groups.items()
    )
    return items


async def _script_segment_codes_for_submission_rows(
    session: AsyncSession,
    submission_rows: list[tuple[Any, ...]],
) -> dict[UUID, str]:
    script_segment_ids = {
        task.script_segment_id or (storyboard.script_segment_id if storyboard else None)
        for _submission, task, storyboard, _file_object, _batch in submission_rows
    }
    script_segment_ids.discard(None)
    if not script_segment_ids:
        return {}
    rows = (await session.execute(
        select(ScriptSegment.id, ScriptSegment.script_segment_code)
        .where(ScriptSegment.id.in_(script_segment_ids))
    )).all()
    return {segment_id: segment_code for segment_id, segment_code in rows}


def _is_approved_library_submission(submission: Submission) -> bool:
    return (
        submission.status in APPROVED_SUBMISSION_STATUSES
        and submission.is_selected
        and not submission.is_archived
        and not submission.is_invalidated
    )


async def is_company_asset_library_file(session: AsyncSession, file_id: UUID) -> bool:
    return await session.scalar(
        select(Submission.id)
        .join(Task, Task.id == Submission.task_id)
        .join(Project, Project.id == Task.project_id)
        .outerjoin(Asset, Asset.id == Task.asset_id)
        .where(
            Submission.file_id == file_id,
            Task.is_retired.is_(False),
            Submission.status.in_(APPROVED_SUBMISSION_STATUSES),
            Submission.is_selected.is_(True),
            Submission.is_archived.is_(False),
            Submission.is_invalidated.is_(False),
            Project.deleted_at.is_(None),
            or_(
                and_(Task.asset_id.is_not(None), Asset.status.notin_(["deleted", "excluded"])),
                and_(Task.asset_id.is_(None), Task.storyboard_id.is_not(None)),
            ),
        )
        .limit(1)
    ) is not None


def _asset_library_item(
    asset: Asset,
    project_prefix: str,
    stored_versions: list[tuple[AssetVersion, FileObject | None]],
    submission_rows: list[tuple[Any, ...]],
    *,
    project_name: str | None = None,
    episode_codes: list[str] | None = None,
    include_versions: bool,
) -> AssetLibraryItemRead:
    versions: list[AssetLibraryVersionRead] = []
    context_version_counters: dict[tuple[str, str, str], int] = {}
    alternate_counters: dict[tuple[tuple[str, str, str], int], int] = {}
    approved_submission_ids = {row[0].id for row in submission_rows}
    represented_submission_ids: set[UUID] = set()
    for version, file_object in stored_versions:
        metadata = version.metadata_json or {}
        file_id = version.file_id or version.preview_file_id
        if version.source_submission_id not in approved_submission_ids:
            continue
        represented_submission_ids.add(version.source_submission_id)
        hierarchy = _asset_version_hierarchy(asset, project_prefix, metadata, version.source_task_id)
        context_version_no, context_key = _asset_context_version(
            asset.asset_type,
            metadata,
            version.version_no,
            context_version_counters,
        )
        is_primary = bool(metadata.get("is_primary", version.is_current))
        alternate_no = _asset_alternate_number(
            is_primary=is_primary,
            context_key=context_key,
            context_version_no=context_version_no,
            counters=alternate_counters,
        )
        versions.append(AssetLibraryVersionRead(
            id=version.id,
            source_type="asset_version",
            version_no=version.version_no,
            context_version_no=context_version_no if asset.asset_type == AssetType.character else None,
            canonical_display_name=canonical_asset_display_name(
                project_prefix=project_prefix,
                asset_type=asset.asset_type,
                asset_code=asset.asset_code,
                version_no=version.version_no,
                extension=(file_object.file_name if file_object else asset.file_path or "bin"),
                task_variant=metadata.get("task_variant"),
                age_stage_code=metadata.get("age_stage_code"),
                costume_variant_code=metadata.get("costume_variant_code"),
                context_version_no=context_version_no,
                alternate_no=alternate_no,
                audio_type=(asset.metadata_json or {}).get("audio_type"),
                episode_code=(asset.metadata_json or {}).get("episode_code"),
            ),
            original_file_name=file_object.file_name if file_object else None,
            file_id=file_id,
            file_path=_file_location(file_object, asset.file_path if asset.current_version_id == version.id else None),
            mime_type=file_object.mime_type if file_object else None,
            file_size=file_object.file_size if file_object else None,
            model_name=version.base_model,
            tool_name=version.tool_name,
            view_label=str(metadata.get("view_label") or "").strip() or None,
            state_label=str(metadata.get("state_label") or "").strip() or None,
            description=str(metadata.get("submission_description") or "").strip() or None,
            batch_id=_uuid_or_none(metadata.get("batch_id")),
            batch_version_no=_safe_int(metadata.get("batch_version_no"), 0) or None,
            status=str(metadata.get("submission_status") or ("current" if version.is_current else "version")),
            is_primary=is_primary,
            is_current=version.is_current,
            is_context_current=version.is_current,
            is_card_master=asset.current_version_id == version.id,
            hierarchy=hierarchy,
            created_at=version.created_at,
        ))
    next_version = max((item.version_no for item in versions), default=0)
    for submission, task, _storyboard, file_object, _batch in submission_rows:
        if submission.id in represented_submission_ids:
            continue
        next_version += 1
        hierarchy = _asset_task_hierarchy(asset, project_prefix, task)
        submission_metadata = {
            "task_variant": task.task_variant,
            "age_stage_code": task.age_stage_code,
            "costume_variant_code": task.costume_variant_code,
            "batch_version_no": _batch.version_no if _batch else None,
        }
        context_version_no, context_key = _asset_context_version(
            asset.asset_type,
            submission_metadata,
            next_version,
            context_version_counters,
        )
        alternate_no = _asset_alternate_number(
            is_primary=submission.is_primary,
            context_key=context_key,
            context_version_no=context_version_no,
            counters=alternate_counters,
        )
        versions.append(AssetLibraryVersionRead(
            id=submission.id,
            source_type="submission",
            version_no=next_version,
            context_version_no=context_version_no if asset.asset_type == AssetType.character else None,
            canonical_display_name=canonical_asset_display_name(
                project_prefix=project_prefix,
                asset_type=asset.asset_type,
                asset_code=asset.asset_code,
                version_no=next_version,
                extension=file_object.file_name if file_object else submission.file_path,
                task_variant=task.task_variant,
                age_stage_code=task.age_stage_code,
                costume_variant_code=task.costume_variant_code,
                context_version_no=context_version_no,
                alternate_no=alternate_no,
                audio_type=(asset.metadata_json or {}).get("audio_type"),
                episode_code=(asset.metadata_json or {}).get("episode_code"),
            ),
            original_file_name=file_object.file_name if file_object else None,
            file_id=submission.file_id,
            file_path=_file_location(file_object, submission.file_path),
            mime_type=file_object.mime_type if file_object else None,
            file_size=file_object.file_size if file_object else None,
            model_name=submission.model_name or task.production_model,
            tool_name=", ".join(submission.tool_names or []) or None,
            view_label=submission.view_label,
            state_label=submission.state_label,
            description=submission.description,
            batch_id=submission.batch_id,
            batch_version_no=_batch.version_no if _batch else None,
            status=submission.status,
            is_primary=submission.is_primary,
            is_current=submission.is_primary,
            is_context_current=submission.is_primary,
            is_card_master=False,
            hierarchy=hierarchy,
            created_at=submission.created_at,
        ))
    principal_variants = {"A", "MASTER"} if asset.asset_type == AssetType.character else {None, "A", "MASTER"}
    principal = [item for item in versions if item.hierarchy.task_variant in principal_variants]
    card_master = next((
        item for item in versions
        if item.is_card_master and (asset.asset_type != AssetType.character or item in principal)
    ), None)
    if asset.asset_type == AssetType.character:
        for item in versions:
            item.is_card_master = item is card_master
    if card_master is None:
        card_master = max(
            (item for item in principal if item.is_primary or item.is_context_current),
            key=lambda item: (item.created_at, item.version_no),
            default=None,
        )
        if card_master is not None:
            card_master.is_card_master = True
    versions.sort(key=lambda item: (item.version_no, item.created_at), reverse=True)
    return AssetLibraryItemRead(
        library_key=f"asset:{asset.id}",
        source_type="human_temporary" if (asset.metadata_json or {}).get("source") == "human_temporary" else "reusable_asset",
        source_id=asset.id,
        project_id=asset.project_id,
        project_code=_library_code(project_prefix, "RF"),
        project_name=project_name,
        episode_codes=episode_codes or [],
        source_label="人工临时添加" if (asset.metadata_json or {}).get("source") == "human_temporary" else "项目资产定版",
        name=asset.name,
        description=asset.description,
        asset_type=asset.asset_type,
        output_type=_asset_library_output_type(asset.asset_type),
        current_master=card_master,
        latest_version_at=max(item.created_at for item in versions),
        version_count=len(versions),
        versions=versions if include_versions else [],
    )


def _asset_version_hierarchy(
    asset: Asset,
    project_prefix: str,
    metadata: dict[str, Any],
    source_task_id: UUID | None,
) -> AssetLibraryHierarchy:
    return AssetLibraryHierarchy(
        project_id=asset.project_id,
        project_prefix=_library_code(project_prefix, "RF"),
        episode_id=_uuid_or_none(metadata.get("episode_id")),
        episode_code=str(metadata.get("episode_code") or "").strip() or None,
        asset_id=asset.id,
        asset_code=asset.asset_code,
        asset_type=asset.asset_type,
        output_type=_asset_library_output_type(asset.asset_type),
        artifact_code=asset_artifact_code(
            project_prefix=project_prefix,
            asset_type=asset.asset_type,
            asset_code=asset.asset_code,
            audio_type=(asset.metadata_json or {}).get("audio_type"),
            episode_code=(asset.metadata_json or {}).get("episode_code"),
        ),
        task_id=source_task_id,
        task_type=metadata.get("task_type"),
        task_variant=metadata.get("task_variant") or metadata.get("view_label"),
        age_stage_code=metadata.get("age_stage_code"),
        costume_variant_code=metadata.get("costume_variant_code"),
    )


def _asset_task_hierarchy(asset: Asset, project_prefix: str, task: Task) -> AssetLibraryHierarchy:
    return AssetLibraryHierarchy(
        project_id=asset.project_id,
        project_prefix=_library_code(project_prefix, "RF"),
        episode_id=task.episode_id,
        scene_code=task.scene_code,
        scene_name=task.scene_name,
        asset_id=asset.id,
        asset_code=asset.asset_code,
        asset_type=asset.asset_type,
        output_type=_asset_library_output_type(asset.asset_type),
        artifact_code=asset_artifact_code(
            project_prefix=project_prefix,
            asset_type=asset.asset_type,
            asset_code=asset.asset_code,
            audio_type=(asset.metadata_json or {}).get("audio_type"),
            episode_code=(asset.metadata_json or {}).get("episode_code"),
        ),
        task_id=task.id,
        task_type=task.task_type,
        task_variant=task.task_variant,
        age_stage_code=task.age_stage_code,
        costume_variant_code=task.costume_variant_code,
    )


def _asset_library_output_type(asset_type: AssetType) -> str:
    return "audio" if asset_type in {AssetType.music, AssetType.voice_profile} else "asset_master"


def _storyboard_library_item(
    project_prefix: str,
    output_type: str,
    rows: list[tuple[Any, ...]],
    *,
    project_name: str | None = None,
    script_segment_codes: dict[UUID, str] | None = None,
    include_versions: bool,
) -> AssetLibraryItemRead:
    versions: list[AssetLibraryVersionRead] = []
    for version_no, (submission, task, storyboard, file_object, _batch) in enumerate(rows, start=1):
        episode_code = f"EP{storyboard.episode_num:02d}"
        storyboard_code = storyboard.storyboard_code or f"F{storyboard.order_num:03d}"
        scene_code = task.scene_code or storyboard.scene_code
        script_segment_id = task.script_segment_id or storyboard.script_segment_id
        script_segment_code = (script_segment_codes or {}).get(script_segment_id)
        mirror_shot_code = _submission_mirror_shot_code(submission, task)
        hierarchy = AssetLibraryHierarchy(
            project_id=task.project_id,
            project_prefix=_library_code(project_prefix, "RF"),
            episode_id=task.episode_id,
            episode_code=episode_code,
            scene_code=scene_code,
            scene_name=task.scene_name or storyboard.scene_name,
            storyboard_id=storyboard.id,
            storyboard_code=storyboard_code,
            script_segment_id=script_segment_id,
            script_segment_code=script_segment_code,
            mirror_shot_code=mirror_shot_code,
            output_type=output_type,
            artifact_code=storyboard_output_artifact_code(
                project_prefix=project_prefix,
                episode_code=episode_code,
                scene_code=scene_code,
                storyboard_code=storyboard_code,
                output_type=output_type,
                script_segment_code=script_segment_code,
                mirror_shot_code=mirror_shot_code,
            ),
            task_id=task.id,
            task_type=task.task_type,
            depends_on_task_id=task.depends_on_task_id,
        )
        versions.append(AssetLibraryVersionRead(
            id=submission.id,
            source_type="submission",
            version_no=version_no,
            canonical_display_name=canonical_storyboard_output_display_name(
                project_prefix=project_prefix,
                episode_code=episode_code,
                scene_code=scene_code,
                storyboard_code=storyboard_code,
                output_type=output_type,
                script_segment_code=script_segment_code,
                mirror_shot_code=mirror_shot_code,
                version_no=version_no,
                extension=file_object.file_name if file_object else submission.file_path,
            ),
            original_file_name=file_object.file_name if file_object else None,
            file_id=submission.file_id,
            file_path=_file_location(file_object, submission.file_path),
            mime_type=file_object.mime_type if file_object else None,
            file_size=file_object.file_size if file_object else None,
            model_name=submission.model_name or task.production_model,
            tool_name=", ".join(submission.tool_names or []) or None,
            batch_id=submission.batch_id,
            batch_version_no=_batch.version_no if _batch else None,
            status=submission.status,
            is_primary=submission.is_primary,
            is_current=submission.is_primary,
            is_context_current=submission.is_primary,
            is_card_master=submission.is_primary,
            hierarchy=hierarchy,
            created_at=submission.created_at,
        ))
    current_master = max(
        (item for item in versions if item.is_primary),
        key=lambda item: (item.created_at, item.version_no),
        default=None,
    )
    for item in versions:
        item.is_card_master = current_master is not None and item.id == current_master.id
    versions.sort(key=lambda item: (item.version_no, item.created_at), reverse=True)
    _submission, task, storyboard, _file_object, _batch = rows[-1]
    mirror_shot_code = _submission_mirror_shot_code(_submission, task)
    return AssetLibraryItemRead(
        library_key=f"storyboard:{storyboard.id}:{mirror_shot_code or 'parent'}:{output_type}",
        source_type="storyboard_output",
        source_id=storyboard.id,
        project_id=task.project_id,
        project_code=_library_code(project_prefix, "RF"),
        project_name=project_name,
        episode_codes=[f"EP{storyboard.episode_num:02d}"],
        source_label="视频定版" if output_type == "video" else "关键帧定版",
        name=storyboard.title or task.title,
        description=storyboard.description,
        asset_type=AssetType.storyboard_video if output_type == "video" else AssetType.storyboard_image,
        output_type=output_type,
        current_master=current_master,
        latest_version_at=max(item.created_at for item in versions),
        version_count=len(versions),
        versions=versions if include_versions else [],
    )


def _submission_mirror_shot_code(submission: Submission, task: Task) -> str | None:
    for value in (task.task_variant, submission.step):
        text = str(value or "").strip().upper().replace("_", "-")
        match = re.search(r"(?:^|-)(MIR(?:ROR)?-?[A-Z0-9]+)(?:-|$)", text)
        if match:
            return _mirror_shot_display_code(match.group(1))
    return None


async def create_asset(session: AsyncSession, data: AssetCreate, user: User) -> AssetRead:
    if not is_director_or_admin(user):
        raise PermissionError("asset")
    if data.asset_type not in _VISUAL_ASSET_TYPES:
        raise ValueError("POST /assets only supports character, scene, and prop assets")
    metadata = dict(data.metadata or {})
    raw_project_id = metadata.get("project_id")
    if not raw_project_id:
        raise ValueError("project_id is required to allocate a formal asset code")
    try:
        project_id = UUID(str(raw_project_id))
    except (TypeError, ValueError, AttributeError) as exc:
        raise ValueError("project_id must be a valid UUID") from exc
    project = await session.scalar(
        select(Project).where(Project.id == project_id).with_for_update()
    )
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    project_code = project.project_prefix or project.project_no
    if not project_code:
        raise ValueError("Project prefix is required to allocate a formal asset code")
    reserved_codes = await _reserved_project_asset_codes(session, project)
    allocated_code = _next_formal_asset_code(project_code, data.asset_type, reserved_codes)
    metadata.pop("asset_code", None)
    metadata.update({
        "project_id": str(project_id),
        "asset_code_source": "backend",
    })
    asset = Asset(
        project_id=project_id,
        asset_code=allocated_code,
        asset_type=data.asset_type,
        name=data.name,
        description=data.description,
        tags=data.tags,
        preview_path=data.preview_path,
        file_path=data.file_path,
        prompt_text=data.prompt_text,
        base_model=data.base_model,
        metadata_json=metadata,
        version=1,
        created_by_id=user.id,
    )
    session.add(asset)
    await session.commit()
    await session.refresh(asset)
    return asset_to_read(asset)


async def create_temporary_asset_production(
    session: AsyncSession, data: TemporaryAssetCreate, user: User
) -> TemporaryAssetProductionRead:
    if not is_director_or_admin(user):
        raise PermissionError("asset")
    if data.asset_type not in {AssetType.character, AssetType.scene, AssetType.prop, AssetType.music, AssetType.voice_profile}:
        raise ValueError("人工临时添加仅支持人物、场景、道具和音频资产。")
    task_idempotency_key = hashlib.sha256(
        f"human_temporary_production:{data.project_id}:{data.request_id}".encode("utf-8")
    ).hexdigest()

    async def existing_response() -> TemporaryAssetProductionRead | None:
        existing_task = await session.scalar(
            select(Task)
            .where(
                Task.idempotency_key == task_idempotency_key,
                Task.is_retired.is_(False),
            )
            .options(selectinload(Task.assignee), selectinload(Task.submissions), selectinload(Task.submission_batches))
        )
        if existing_task is None:
            return None
        existing_asset = await session.get(Asset, existing_task.asset_id) if existing_task.asset_id else None
        if existing_asset is None or existing_task.project_id != data.project_id:
            raise ValueError("人工临时生产请求幂等记录异常。")
        existing_episode_code = str((existing_asset.metadata_json or {}).get("episode_code") or "").strip() or None
        return TemporaryAssetProductionRead(
            asset=asset_to_read(existing_asset),
            task=task_to_read(existing_task, episode_code=existing_episode_code),
        )

    existing = await existing_response()
    if existing is not None:
        return existing
    project = await session.scalar(select(Project).where(Project.id == data.project_id).with_for_update())
    if project is None or project.deleted_at is not None:
        raise KeyError(data.project_id)
    # The project row lock serializes the asset-code allocator. Re-check the
    # request key after acquiring it so two concurrent retries cannot create
    # separate assets before the task unique constraint is reached.
    existing = await existing_response()
    if existing is not None:
        return existing
    episode = await session.get(ProjectEpisode, data.episode_id)
    script_version = await session.get(ScriptVersion, data.script_version_id)
    script = await session.get(Script, script_version.script_id) if script_version else None
    if (
        episode is None
        or episode.project_id != data.project_id
        or script is None
        or script.project_id != data.project_id
        or script.episode_id != data.episode_id
        or script.current_version_id != data.script_version_id
    ):
        raise ValueError("只能在当前分集的最新剧本版本中追加人工生产资产。")
    # Temporary production is available only while the current clean view
    # points at a scoped human-final AssetNormalization.v1 inventory.
    if hasattr(session, "execute"):
        scoped_view = await latest_breakdown_view(
            session, data.project_id, user, episode_id=data.episode_id, script_version_id=data.script_version_id
        ) or {}
        review_state = scoped_view.get("asset_review_state") if isinstance(scoped_view.get("asset_review_state"), dict) else {}
        revision_id = _uuid_or_none(review_state.get("confirmed_revision_id"))
        revision = await session.get(ArtifactRevision, revision_id) if revision_id else None
        snapshot = dict(revision.input_snapshot or {}) if revision is not None else {}
        try:
            inventory = AssetNormalizationResultV1.model_validate({
                "normalization_version": "AssetNormalization.v1",
                "assets": list((revision.normalized_content or {}).get("assets") or []),
            }) if revision is not None else None
        except ValueError:
            inventory = None
        if (
            str(review_state.get("status") or "") != "confirmed"
            or revision is None
            or revision.project_id != data.project_id
            or revision.artifact_type != "asset_inventory"
            or revision.source_type != "human"
            or revision.status != "confirmed"
            or revision.data_state != "final"
            or str(snapshot.get("episode_id") or "") != str(data.episode_id)
            or str(snapshot.get("script_version_id") or "") != str(data.script_version_id)
            or inventory is None
            or not inventory.assets
        ):
            raise ValueError("请先确认资产拆解，确认后才能新增人工临时生产资产。")
    project_code = project.project_prefix or project.project_no
    if not project_code:
        raise ValueError("项目缺少业务前缀，不能分配正式资产编码。")
    reserved_codes = await _reserved_project_asset_codes(session, project)
    if data.asset_type in _VISUAL_ASSET_TYPES:
        allocated_code = _next_formal_asset_code(project_code, data.asset_type, reserved_codes)
    else:
        code_type = "MUSIC" if data.asset_type == AssetType.music else "VOICE"
        prefix = _safe_code_prefix(project_code)
        sequence = 1
        while f"{prefix}-{code_type}-TEMP{sequence:03d}" in reserved_codes:
            sequence += 1
        allocated_code = f"{prefix}-{code_type}-TEMP{sequence:03d}"
    metadata = {
        "project_id": str(data.project_id),
        "episode_id": str(data.episode_id) if data.episode_id else None,
        "episode_code": episode.episode_code if episode else None,
        "source": "human_temporary",
        "source_label": "人工临时添加",
        "temporary_reason": data.reason,
        "production_requirement": data.production_requirement,
        "task_variant": data.task_variant,
        "prompt_skipped": True,
        "workflow_isolated": True,
        "request_id": str(data.request_id),
    }
    asset = Asset(
        project_id=data.project_id, asset_code=allocated_code,
        asset_type=data.asset_type, name=data.name, description=data.description,
        status="in_progress", tags=["人工临时添加", "待生产"], metadata_json=metadata,
        version=1, created_by_id=user.id,
    )
    session.add(asset)
    await session.flush()
    task_type = TaskType.audio if data.asset_type in {AssetType.music, AssetType.voice_profile} else TaskType.asset
    media_type = (
        "voice" if data.asset_type == AssetType.voice_profile
        else "music" if data.asset_type == AssetType.music
        else "image"
    )
    task = Task(
        project_id=data.project_id,
        episode_id=data.episode_id,
        script_id=script.id,
        script_version_id=data.script_version_id,
        asset_id=asset.id,
        task_type=task_type,
        title=f"人工临时资产生产 · {asset.asset_code} {asset.name}"[:160],
        status=TaskStatus.todo,
        prompt_text=None,
        latest_prompt_text=None,
        media_type=media_type,
        task_variant=data.task_variant,
        variant_kind="human_temporary",
        variant_title_zh="人工临时添加",
        variant_description_zh=data.production_requirement,
        asset_context_outdated=False,
        idempotency_key=task_idempotency_key,
        assignee=None,
        submissions=[],
        submission_batches=[],
    )
    session.add(task)
    await session.commit()
    await session.refresh(asset)
    await session.refresh(task)
    return TemporaryAssetProductionRead(
        asset=asset_to_read(asset),
        task=task_to_read(task, episode_code=episode.episode_code),
    )


async def get_asset(session: AsyncSession, asset_id: UUID, user: User) -> AssetRead:
    asset = await session.get(Asset, asset_id)
    if asset is None:
        raise KeyError(asset_id)
    if asset.project_id and not is_director_or_admin(user) and not await _can_access_project(session, asset.project_id, user):
        raise PermissionError(asset_id)
    return asset_to_read(asset)


async def list_scene_asset_options(
    session: AsyncSession, project_id: UUID, user: User, search: str | None = None
) -> list[SceneAssetOption]:
    await ensure_project_write_access(session, project_id, user)
    stmt = select(Asset).where(
        Asset.project_id == project_id,
        Asset.asset_type == AssetType.scene,
        Asset.status.notin_(["deleted", "excluded"]),
    ).order_by(Asset.asset_code.asc(), Asset.name.asc())
    if search and search.strip():
        pattern = f"%{search.strip()}%"
        stmt = stmt.where(or_(Asset.asset_code.ilike(pattern), Asset.name.ilike(pattern)))
    scenes = (await session.execute(stmt)).scalars().all()
    options: list[SceneAssetOption] = []
    for scene in scenes:
        revision = await session.get(ArtifactRevision, scene.current_revision_id) if scene.current_revision_id else None
        if revision is None or revision.source_type != "human":
            continue
        options.append(
            SceneAssetOption(
                scene_asset_id=scene.id,
                scene_code=scene.asset_code or str(scene.id),
                scene_name=scene.name,
                episode_code=str((scene.metadata_json or {}).get("episode_code") or "") or None,
                revision_id=revision.id,
                status=_asset_confirmation_status(scene),
            )
        )
    return options


async def complete_asset_field(
    session: AsyncSession,
    asset_id: UUID,
    data: AssetCompletionCreate,
    user: User,
) -> AssetCompletionRead:
    asset = await session.get(Asset, asset_id)
    if asset is None or asset.project_id is None:
        raise KeyError(asset_id)
    await ensure_project_write_access(session, asset.project_id, user)
    allowed_fields = {
        "role_type", "identity_setting", "visual_features", "costume_plan", "continuity_constraints",
        "age_range", "timeline", "scene_ids", "face_changes", "body_changes", "hair_skin_changes",
        "identity_anchors", "forbidden_changes", "output_spec", "description", "prompt",
        "appearance_material", "state_plan", "owner_character", "prop_type",
    }
    if data.target_field not in allowed_fields:
        raise ValueError("Unsupported asset completion target_field")
    if data.resolution_mode == "uncertain":
        existing_record = await session.scalar(
            select(AssetCompletionRecord)
            .where(
                AssetCompletionRecord.asset_id == asset.id,
                AssetCompletionRecord.target_field == data.target_field,
                AssetCompletionRecord.age_stage_code == data.age_stage_code,
                AssetCompletionRecord.costume_variant_code == data.costume_variant_code,
                AssetCompletionRecord.resolution_mode == "uncertain",
            )
            .order_by(AssetCompletionRecord.updated_at.desc())
            .limit(1)
        )
        record = existing_record or AssetCompletionRecord(
            project_id=asset.project_id,
            asset_id=asset.id,
            asset_code=asset.asset_code,
            age_stage_code=data.age_stage_code,
            costume_variant_code=data.costume_variant_code,
            target_field=data.target_field,
            created_by=user.id,
        )
        if existing_record is None:
            session.add(record)
        record.missing_reason = data.missing_reason
        record.original_uncertainty = data.original_uncertainty
        record.ai_suggestion = data.ai_suggestion
        record.human_description = None
        record.selected_scenes = []
        record.resolution_mode = "uncertain"
        record.status = "uncertain"
        record.downstream_status = "pending"
        record.applied_asset_revision_id = None
        await session.commit()
        await session.refresh(record)
        return AssetCompletionRead(
            id=record.id,
            asset_id=asset.id,
            target_field=record.target_field,
            status=record.status,
            applied_asset_revision_id=None,
            downstream_status=record.downstream_status,
            resolution_mode=record.resolution_mode,
        )
    if not (data.human_description or "").strip() and not data.selected_scenes:
        raise ValueError("Completion requires a description or selected scenes")

    selected_scenes = await _validated_scene_references(session, asset.project_id, data.selected_scenes)
    before = _asset_revision_content(asset)
    after = json.loads(json.dumps(before, ensure_ascii=False))
    _apply_asset_completion(after, data, selected_scenes)
    if data.target_field == "prompt":
        metadata = dict(after.get("metadata") or {})
        fingerprint = _content_hash(_asset_prompt_input_projection(after))
        metadata.update({
            "confirmation_status": "pending_confirmation",
            "prompt_validity": "current",
            "prompt_input_fingerprint": fingerprint,
            "prompt_generated_for_fingerprint": fingerprint,
        })
        metadata.pop("prompt_invalidated_at", None)
        metadata.pop("prompt_invalidated_fields", None)
        after["metadata"] = metadata
        after["status"] = "pending_confirmation"
    elif data.target_field == "scene_ids":
        metadata = dict(after.get("metadata") or {})
        metadata["confirmation_status"] = _asset_confirmation_status(asset)
        after["metadata"] = metadata
        after["status"] = _asset_confirmation_status(asset)
    else:
        after = _apply_asset_lifecycle([after], previous_assets=[before], raw_output={})[0]
    version_no = (
        await session.scalar(
            select(func.max(ArtifactRevision.version_no)).where(
                ArtifactRevision.artifact_type == "asset", ArtifactRevision.artifact_id == asset.id
            )
        )
        or 0
    ) + 1
    revision = ArtifactRevision(
        project_id=asset.project_id,
        artifact_type="asset",
        artifact_id=asset.id,
        version_no=version_no,
        parent_revision_id=asset.current_revision_id,
        source_type="human",
        input_snapshot={"base_asset_revision_id": str(data.base_asset_revision_id) if data.base_asset_revision_id else None},
        normalized_content=after,
        change_diff={"target_field": data.target_field, "before": before, "after": after},
        content_hash=_content_hash(after),
        status="pending_confirmation",
        created_by=user.id,
        data_state="human_revision",
    )
    session.add(revision)
    await session.flush()
    asset.description = str(after.get("description") or asset.description or "") or None
    asset.prompt_text = str(after.get("prompt_text") or asset.prompt_text or "") or None
    asset.metadata_json = dict(after.get("metadata") or {})
    asset.current_revision_id = revision.id
    asset.version = max(asset.version + 1, version_no)
    asset.status = str(after.get("status") or "pending_confirmation")

    record = AssetCompletionRecord(
        project_id=asset.project_id,
        asset_id=asset.id,
        asset_code=asset.asset_code,
        age_stage_code=data.age_stage_code,
        costume_variant_code=data.costume_variant_code,
        target_field=data.target_field,
        missing_reason=data.missing_reason,
        original_uncertainty=data.original_uncertainty,
        ai_suggestion=data.ai_suggestion,
        human_description=(data.human_description or "").strip() or None,
        selected_scenes=selected_scenes,
        resolution_mode=data.resolution_mode,
        base_asset_revision_id=data.base_asset_revision_id or revision.parent_revision_id,
        applied_asset_revision_id=revision.id,
        downstream_status="applied",
        created_by=user.id,
    )
    session.add(record)
    await _propagate_asset_revision_to_tasks(session, asset, revision)
    await session.commit()
    return AssetCompletionRead(
        id=record.id,
        asset_id=asset.id,
        target_field=record.target_field,
        status=record.status,
        applied_asset_revision_id=revision.id,
        downstream_status=record.downstream_status,
        resolution_mode=record.resolution_mode,
    )


def _asset_confirmation_status(asset: Asset) -> str:
    raw = str((asset.metadata_json or {}).get("confirmation_status") or asset.status or "pending_confirmation")
    return raw if raw in {"needs_completion", "prompt_pending", "pending_confirmation", "confirmed"} else "pending_confirmation"


def _asset_revision_content(asset: Asset) -> dict[str, Any]:
    return {
        "asset_code": asset.asset_code,
        "asset_type": asset.asset_type.value,
        "name": asset.name,
        "description": asset.description,
        "prompt_text": asset.prompt_text,
        "metadata": asset.metadata_json or {},
    }


def _content_hash(content: dict[str, Any]) -> str:
    canonical = json.dumps(content, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _artifact_revision_idempotency_key(
    artifact_type: str,
    artifact_id: UUID,
    source_run_id: UUID | None,
    source_type: str,
    data_state: str,
    content_hash: str,
) -> str:
    return _content_hash({
        "artifact_type": artifact_type,
        "artifact_id": str(artifact_id),
        "source_run_id": str(source_run_id) if source_run_id else None,
        "source_type": source_type,
        "data_state": data_state,
        "content_hash": content_hash,
    })


async def _insert_artifact_revision_idempotently(
    session: AsyncSession,
    revision: ArtifactRevision,
) -> tuple[ArtifactRevision, bool]:
    """Insert a revision without turning a concurrent duplicate into a failed transaction."""
    if not revision.idempotency_key:
        session.add(revision)
        await session.flush()
        return revision, True
    try:
        async with session.begin_nested():
            session.add(revision)
            await session.flush()
    except IntegrityError:
        existing = await session.scalar(
            select(ArtifactRevision)
            .where(ArtifactRevision.idempotency_key == revision.idempotency_key)
            .limit(1)
        )
        if existing is not None:
            return existing, False
        raise
    return revision, True


async def _validated_scene_references(session: AsyncSession, project_id: UUID, refs: list[Any]) -> list[dict[str, Any]]:
    output: list[dict[str, Any]] = []
    for ref in refs:
        scene = await session.get(Asset, ref.scene_asset_id)
        if scene is None or scene.project_id != project_id or scene.asset_type != AssetType.scene or scene.status in {"deleted", "excluded"}:
            raise ValueError("Selected scene is not available in the current project asset revision")
        output.append({
            "scene_asset_id": str(scene.id),
            "scene_revision_id": str(scene.current_revision_id) if scene.current_revision_id else None,
            "scene_code": scene.asset_code,
            "scene_name": scene.name,
        })
    return output


def _apply_asset_completion(content: dict[str, Any], data: AssetCompletionCreate, scenes: list[dict[str, Any]]) -> None:
    metadata = dict(content.get("metadata") or {})
    target: dict[str, Any] = metadata
    if data.age_stage_code:
        stages = [dict(item) for item in metadata.get("age_stages") or [] if isinstance(item, dict)]
        stage = next((item for item in stages if item.get("stage_code") == data.age_stage_code), None)
        if stage is None:
            raise ValueError("Age stage does not belong to the asset")
        target = stage
        if data.costume_variant_code:
            variants = [dict(item) for item in stage.get("costume_variants") or [] if isinstance(item, dict)]
            variant = next((item for item in variants if item.get("variant_code") == data.costume_variant_code), None)
            if variant is None:
                raise ValueError("Costume variant does not belong to the age stage")
            target = variant
            stage["costume_variants"] = variants
        metadata["age_stages"] = stages
    if data.target_field == "description" and target is metadata:
        content["description"] = (data.human_description or "").strip()
    elif data.target_field == "prompt" and target is metadata:
        content["prompt_text"] = (data.human_description or "").strip()
    elif data.target_field == "scene_ids":
        target["scene_ids"] = [item["scene_asset_id"] for item in scenes]
        target["scene_revision_ids"] = [item["scene_revision_id"] for item in scenes if item["scene_revision_id"]]
        target["scene_codes"] = [item["scene_code"] for item in scenes if item["scene_code"]]
        target["scene_names"] = [item["scene_name"] for item in scenes]
    else:
        target[data.target_field] = (data.human_description or "").strip()
    if data.target_field != "scene_ids":
        target["status"] = "pending_confirmation"
    content["metadata"] = metadata


async def _propagate_asset_revision_to_tasks(session: AsyncSession, asset: Asset, revision: ArtifactRevision) -> None:
    tasks = (await session.execute(select(Task).where(
        Task.asset_id == asset.id,
        Task.is_retired.is_(False),
    ))).scalars().all()
    for task in tasks:
        if task.status == TaskStatus.todo:
            version_no = (await session.scalar(select(func.max(TaskPrompt.version_no)).where(TaskPrompt.task_id == task.id)) or 0) + 1
            prompt = _asset_task_prompt_for_existing(asset, task)
            session.add(TaskPrompt(task_id=task.id, prompt_type="asset_revision", prompt_text=prompt, version_no=version_no, source="human", created_by=revision.created_by))
            task.latest_prompt_text = prompt
            task.prompt_revision_id = revision.id
            task.asset_context_outdated = False
        elif task.status in {TaskStatus.in_progress, TaskStatus.submitted, TaskStatus.reviewing, TaskStatus.overdue}:
            task.asset_context_outdated = True


async def list_projects(session: AsyncSession, user: User) -> list[ProjectRead]:
    stmt = select(Project).where(Project.deleted_at.is_(None)).order_by(Project.created_at.desc())
    if not is_director_or_admin(user):
        project_ids = await _visible_project_ids(session, user)
        if not project_ids:
            return []
        stmt = stmt.where(Project.id.in_(project_ids))
    result = await session.execute(stmt)
    projects = result.scalars().all()
    output: list[ProjectRead] = []
    for project in projects:
        visible_storyboard_ids = await _visible_storyboard_ids_for_project(session, project.id, user)
        if is_director_or_admin(user):
            storyboard_count = await session.scalar(select(func.count(Storyboard.id)).where(Storyboard.project_id == project.id)) or 0
            task_count = await session.scalar(select(func.count(Task.id)).where(
                Task.project_id == project.id,
                Task.is_retired.is_(False),
            )) or 0
        else:
            storyboard_count = len(visible_storyboard_ids)
            task_count = await session.scalar(_visible_task_stmt(user).where(Task.project_id == project.id).with_only_columns(func.count(Task.id))) or 0
        output.append(project_to_read(project, storyboard_count, task_count))
    return output


async def create_project(session: AsyncSession, data: ProjectCreate, user: User) -> ProjectRead:
    prefix = (data.project_prefix or "").upper()[:5] or None
    production_brief = data.production_brief.model_dump(mode="json")
    project = Project(
        project_no=_project_no(),
        project_prefix=prefix,
        title=data.title,
        name=data.title,
        genre=data.genre,
        script_text=data.script_text,
        production_brief=production_brief,
        status=ProjectStatus.draft,
        current_stage=ProjectStatus.draft,
        manager_id=data.manager_id or user.id,
        created_by_id=user.id,
    )
    session.add(project)
    await session.commit()
    await session.refresh(project)
    return project_to_read(project)


async def update_project(session: AsyncSession, project_id: UUID, data: ProjectUpdate, user: User) -> ProjectRead:
    if not is_director_or_admin(user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    update_data = data.model_dump(exclude_unset=True)
    if "production_brief" in update_data and update_data["production_brief"] is not None:
        active_task_statuses = {
            TaskStatus.todo, TaskStatus.in_progress, TaskStatus.submitted,
            TaskStatus.reviewing, TaskStatus.overdue,
        }
        has_active_tasks = bool(await session.scalar(
            select(Task.id).where(
                Task.project_id == project_id,
                Task.status.in_(active_task_statuses),
                Task.is_retired.is_(False),
            ).limit(1)
        ))
        if has_active_tasks:
            raise ValueError("项目存在已分发或执行中的任务，请先撤回或关闭受影响任务后再修改制作设置。")
    if "project_prefix" in update_data:
        requested_prefix = (update_data["project_prefix"] or "").upper()[:5] or None
        has_scripts = bool(await session.scalar(select(Script.id).where(Script.project_id == project_id).limit(1)))
        if has_scripts and requested_prefix != project.project_prefix:
            raise ValueError("项目已导入剧本，项目缩写已锁定；修改编号体系需使用专用迁移流程。")
    old_profile = _project_profile_content(project)
    if "title" in update_data and update_data["title"] is not None:
        project.title = update_data["title"]
        project.name = update_data["title"]
    if "genre" in update_data:
        project.genre = update_data["genre"]
    if "script_text" in update_data:
        project.script_text = update_data["script_text"]
    if "project_prefix" in update_data:
        project.project_prefix = (update_data["project_prefix"] or "").upper()[:5] or None
    if "manager_id" in update_data:
        project.manager_id = update_data["manager_id"]
    if "production_brief" in update_data:
        project.production_brief = update_data["production_brief"]
    new_profile = _project_profile_content(project)
    if old_profile != new_profile:
        previous = await session.scalar(
            select(ArtifactRevision)
            .where(ArtifactRevision.artifact_type == "project_profile", ArtifactRevision.artifact_id == project.id)
            .order_by(ArtifactRevision.version_no.desc())
            .limit(1)
        )
        changed_fields = [key for key in new_profile if old_profile.get(key) != new_profile.get(key)]
        prompt_input_changes = _project_prompt_input_changes(old_profile, new_profile)
        if prompt_input_changes:
            await _invalidate_project_asset_prompts(
                session,
                project.id,
                changed_fields=prompt_input_changes,
                created_by=user.id,
            )
        session.add(ArtifactRevision(
            project_id=project.id,
            artifact_type="project_profile",
            artifact_id=project.id,
            version_no=(previous.version_no if previous else 0) + 1,
            parent_revision_id=previous.id if previous else None,
            source_type="human",
            input_snapshot=old_profile,
            normalized_content=new_profile,
            change_diff={"changed_fields": changed_fields, "before": old_profile, "after": new_profile},
            content_hash=_content_hash(new_profile),
            status="confirmed",
            created_by=user.id,
        ))
    project.status = ProjectStatus.draft if project.status == ProjectStatus.archived else project.status
    project.updated_at = datetime.now(UTC)
    await session.commit()
    await session.refresh(project)
    await invalidate_project_status(str(project.id))
    return project_to_read(project)


def _project_profile_content(project: Project) -> dict[str, Any]:
    return {
        "genre": project.genre,
        "production_brief": getattr(project, "production_brief", None) or {},
    }


def _project_profile_hashes(project: Project) -> dict[str, str]:
    content = _project_profile_content(project)
    brief = content["production_brief"]
    return {
        "project_profile_hash": _content_hash(content),
        "genre_hash": _content_hash({"genre": content["genre"]}),
        "production_brief_hash": _content_hash(brief),
        "content_type_hash": _content_hash(brief.get("content_type")),
        "delivery_aspect_ratio_hash": _content_hash(brief.get("delivery_aspect_ratio")),
        "primary_style_hash": _content_hash({
            "primary_style_id": brief.get("primary_style_id"),
            "style_catalog_version": brief.get("style_catalog_version"),
        }),
        "cultural_contexts_hash": _content_hash({
            "cultural_contexts": brief.get("cultural_contexts") or [],
            "primary_cultural_context_code": brief.get("primary_cultural_context_code"),
        }),
    }


async def list_project_episodes(session: AsyncSession, project_id: UUID, user: User) -> list[ProjectEpisodeRead]:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    episodes = list((await session.scalars(
        select(ProjectEpisode)
        .where(ProjectEpisode.project_id == project_id)
        .order_by(ProjectEpisode.episode_no)
    )).all())
    output: list[ProjectEpisodeRead] = []
    for episode in episodes:
        script = await session.scalar(
            select(Script)
            .where(Script.project_id == project_id, Script.episode_id == episode.id)
            .order_by(Script.created_at)
            .limit(1)
        )
        current_version = await session.get(ScriptVersion, script.current_version_id) if script and script.current_version_id else None
        version_count = int(await session.scalar(
            select(func.count(ScriptVersion.id)).where(ScriptVersion.script_id == script.id)
        ) or 0) if script else 0
        reading_revision = await _latest_episode_reading_revision(
            session, project_id, episode.id, current_version.id if current_version else None
        )
        summary = _stored_episode_summary(episode.summary) or _reading_summary(
            reading_revision.normalized_content if reading_revision else {}
        )
        has_asset_bindings = bool(current_version and await session.scalar(
            select(EpisodeAssetBinding.id).where(
                EpisodeAssetBinding.project_id == project_id,
                EpisodeAssetBinding.episode_id == episode.id,
                EpisodeAssetBinding.script_version_id == current_version.id,
                EpisodeAssetBinding.status == "active",
            ).limit(1)
        ))
        breakdown_view = {
            "assets": [{}],
            "source_label": "human_modified",
        } if has_asset_bindings else {}
        stale_stages = await _episode_stale_stages_for_revision(session, project, reading_revision)
        has_segments = bool(await session.scalar(
            select(ScriptSegment.id).where(ScriptSegment.episode_id == episode.id).limit(1)
        ))
        has_storyboards = bool(await session.scalar(
            select(Storyboard.id).where(
                Storyboard.project_id == project_id, Storyboard.episode_num == episode.episode_no
            ).limit(1)
        ))
        has_tasks = bool(await session.scalar(select(Task.id).where(
            Task.episode_id == episode.id,
            Task.is_retired.is_(False),
        ).limit(1)))
        breakdown_status = _episode_breakdown_status(
            reading_revision=reading_revision,
            breakdown_view=breakdown_view,
            has_segments=has_segments,
            has_storyboards=has_storyboards,
            has_tasks=has_tasks,
            stale_stages=stale_stages,
        )
        output.append(ProjectEpisodeRead(
            id=episode.id,
            project_id=episode.project_id,
            episode_no=episode.episode_no,
            episode_code=episode.episode_code,
            title=_stored_episode_title(
                episode.title,
                current_version.original_filename if current_version else None,
            ),
            summary=summary,
            breakdown_status=breakdown_status,
            stale_stages=stale_stages,
            script_id=script.id if script else None,
            current_version_id=script.current_version_id if script else None,
            current_version_no=current_version.version_no if current_version else None,
            version_count=version_count,
            current_content_hash=current_version.content_hash if current_version else None,
            current_original_filename=current_version.original_filename if current_version else None,
            production_brief=episode.production_brief or {},
            updated_at=episode.updated_at,
        ))
    return output


async def get_project_episode_detail(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    user: User,
) -> ProjectEpisodeDetail:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    episode = await session.get(ProjectEpisode, episode_id)
    if project is None or project.deleted_at is not None or episode is None or episode.project_id != project_id:
        raise KeyError(episode_id)
    script = await session.scalar(
        select(Script).where(Script.project_id == project_id, Script.episode_id == episode_id).limit(1)
    )
    versions = list((await session.scalars(
        select(ScriptVersion)
        .where(ScriptVersion.script_id == script.id)
        .order_by(ScriptVersion.version_no.desc())
    )).all()) if script else []
    current = next((item for item in versions if item.id == script.current_version_id), None) if script else None
    bindings = list((await session.scalars(
        select(EpisodeAssetBinding)
        .where(
            EpisodeAssetBinding.project_id == project_id,
            EpisodeAssetBinding.episode_id == episode_id,
            EpisodeAssetBinding.script_version_id == current.id,
            EpisodeAssetBinding.status == "active",
        )
        .order_by(EpisodeAssetBinding.proposal_index)
    )).all()) if current else []
    assets_by_id = {
        item.id: item for item in (await session.scalars(
            select(Asset).where(Asset.id.in_([binding.asset_id for binding in bindings]))
        )).all()
    } if bindings else {}
    reading_revision = await _latest_episode_reading_revision(session, project_id, episode_id, current.id if current else None)
    summary = _stored_episode_summary(episode.summary) or _reading_summary(
        reading_revision.normalized_content if reading_revision else {}
    )
    breakdown_view = await latest_breakdown_view(
        session,
        project_id,
        user,
        episode_id=episode.id,
        script_version_id=current.id if current else None,
    ) or {}
    stale_stages = await _episode_stale_stages_for_revision(session, project, reading_revision)
    breakdown_status = _episode_breakdown_status(
        reading_revision=reading_revision,
        breakdown_view=breakdown_view,
        has_segments=bool(await session.scalar(
            select(ScriptSegment.id).where(ScriptSegment.episode_id == episode.id).limit(1)
        )),
        has_storyboards=bool(await session.scalar(
            select(Storyboard.id).where(
                Storyboard.project_id == project_id,
                Storyboard.episode_num == episode.episode_no,
            ).limit(1)
        )),
        has_tasks=bool(await session.scalar(
            select(Task.id).where(
                Task.episode_id == episode.id,
                Task.is_retired.is_(False),
            ).limit(1)
        )),
        stale_stages=stale_stages,
    )
    return ProjectEpisodeDetail(
        id=episode.id,
        project_id=episode.project_id,
        episode_no=episode.episode_no,
        episode_code=episode.episode_code,
        title=_stored_episode_title(episode.title, current.original_filename if current else None),
        summary=summary,
        breakdown_status=breakdown_status,
        script_id=script.id if script else None,
        current_version_id=script.current_version_id if script else None,
        current_version_no=current.version_no if current else None,
        version_count=len(versions),
        current_content_hash=current.content_hash if current else None,
        current_original_filename=current.original_filename if current else None,
        production_brief=episode.production_brief or {},
        versions=[ScriptVersionSummary(
            id=version.id,
            version_no=version.version_no,
            content_hash=version.content_hash,
            original_filename=version.original_filename,
            parser_name=version.parser_name,
            language=version.language,
            created_at=version.created_at,
            is_current=version.id == (script.current_version_id if script else None),
        ) for version in versions],
        assets=[EpisodeAssetBindingRead(
            id=binding.id,
            proposal_index=binding.proposal_index,
            asset_id=binding.asset_id,
            asset_revision_id=binding.asset_revision_id,
            asset_code=assets_by_id[binding.asset_id].asset_code,
            asset_type=assets_by_id[binding.asset_id].asset_type.value,
            asset_name=assets_by_id[binding.asset_id].name,
            action=binding.action,
            status=binding.status,
            age_stage_code=binding.age_stage_code,
            costume_variant_code=binding.costume_variant_code,
            proposal=binding.proposal_json or {},
        ) for binding in bindings if binding.asset_id in assets_by_id],
        stale_stages=stale_stages,
        updated_at=episode.updated_at,
    )


async def update_episode_production_brief(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    production_brief: dict[str, Any],
    user: User,
) -> dict[str, Any]:
    if not is_director_or_admin(user):
        raise PermissionError(project_id)
    episode = await session.get(ProjectEpisode, episode_id)
    if episode is None or episode.project_id != project_id:
        raise KeyError(episode_id)
    active_statuses = {
        TaskStatus.todo, TaskStatus.in_progress, TaskStatus.submitted,
        TaskStatus.reviewing, TaskStatus.overdue,
    }
    if await session.scalar(
        select(Task.id).where(
            Task.episode_id == episode_id,
            Task.status.in_(active_statuses),
            Task.is_retired.is_(False),
        ).limit(1)
    ):
        raise ValueError("当前分集存在已分发或执行中的任务，请先撤回或关闭后再修改目标时长。")
    before = dict(episode.production_brief or {})
    episode.production_brief = dict(production_brief)
    episode.updated_at = datetime.now(UTC)
    session.add(ArtifactRevision(
        project_id=project_id,
        artifact_type="episode_production_brief",
        artifact_id=episode.id,
        version_no=int(await session.scalar(select(func.count(ArtifactRevision.id)).where(
            ArtifactRevision.artifact_type == "episode_production_brief",
            ArtifactRevision.artifact_id == episode.id,
        )) or 0) + 1,
        source_type="human",
        input_snapshot=before,
        normalized_content=episode.production_brief,
        change_diff={"before": before, "after": episode.production_brief, "changed_fields": ["target_duration_seconds"]},
        content_hash=_content_hash(episode.production_brief),
        status="confirmed",
        created_by=user.id,
    ))
    await session.commit()
    return dict(episode.production_brief)


async def get_project_episode_script_version(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    version_id: UUID,
    user: User,
) -> ScriptVersionDetail:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    version = await session.get(ScriptVersion, version_id)
    script = await session.get(Script, version.script_id) if version else None
    if script is None or script.project_id != project_id or script.episode_id != episode_id:
        raise KeyError(version_id)
    return ScriptVersionDetail(
        id=version.id,
        script_id=script.id,
        version_no=version.version_no,
        content_hash=version.content_hash,
        original_filename=version.original_filename,
        parser_name=version.parser_name,
        language=version.language,
        created_at=version.created_at,
        is_current=version.id == script.current_version_id,
        script_text=version.content,
    )


async def _latest_episode_reading_revision(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    script_version_id: UUID | None,
) -> ArtifactRevision | None:
    revisions = list((await session.scalars(
        select(ArtifactRevision).where(
            ArtifactRevision.project_id == project_id,
            ArtifactRevision.artifact_type == "reading_report",
            ArtifactRevision.source_type == "human",
            ArtifactRevision.status == "confirmed",
            ArtifactRevision.data_state == "final",
        ).order_by(ArtifactRevision.version_no.desc(), ArtifactRevision.created_at.desc())
    )).all())
    for revision in revisions:
        snapshot = revision.input_snapshot or {}
        if str(snapshot.get("episode_id") or "") != str(episode_id):
            continue
        if script_version_id and str(snapshot.get("script_version_id") or "") != str(script_version_id):
            continue
        return revision
    return None


async def _overlay_authoritative_reading_revision(
    session: AsyncSession,
    project_id: UUID,
    view: dict[str, Any],
    *,
    episode_id: UUID | None,
    script_version_id: UUID | None,
) -> dict[str, Any]:
    """Expose one confirmed reading version instead of merging run snapshots."""
    authoritative = dict(view)
    for key in (
        "confirmed_reading_report",
        "reading_output",
        "reading_revision_id",
        "confirmed_reading_revision_id",
        "reading_data_state",
        "reading_source",
    ):
        authoritative.pop(key, None)
    review_state = authoritative.get("reading_review_state")
    review_state = review_state if isinstance(review_state, dict) else {}
    revision_id = _uuid_or_none(review_state.get("confirmed_revision_id"))
    if episode_id is None or str(review_state.get("status") or "") != "confirmed" or revision_id is None:
        authoritative["reading_review_state"] = _normalized_breakdown_review_state(review_state)
        return authoritative
    revision = await session.get(ArtifactRevision, revision_id)
    if (
        revision is None
        or revision.project_id != project_id
        or revision.artifact_type != "reading_report"
        or revision.source_type != "human"
        or revision.status != "confirmed"
        or revision.data_state != "final"
    ):
        authoritative["reading_review_state"] = _pending_confirmation_state()
        return authoritative
    context = dict(revision.input_snapshot or {})
    if (
        str(context.get("episode_id") or "") != str(episode_id)
        or (script_version_id is not None and str(context.get("script_version_id") or "") != str(script_version_id))
    ):
        authoritative["reading_review_state"] = _pending_confirmation_state()
        return authoritative
    authoritative.update({
        "reading_report": dict(revision.normalized_content or {}),
        "reading_artifact_id": str(revision.artifact_id),
        "reading_revision_context": context,
        "reading_review_state": {
            "status": "confirmed",
            "confirmed_revision_id": str(revision.id),
            "confirmation_mode": str(review_state.get("confirmation_mode") or "human_confirmed"),
        },
    })
    return authoritative


def _reading_summary(report: dict[str, Any]) -> str | None:
    overview = report.get("story_overview") if isinstance(report, dict) else None
    if not isinstance(overview, dict):
        return None
    synopsis = str(overview.get("synopsis") or "").strip()
    if synopsis:
        return synopsis[:2000]
    logline = str(overview.get("logline") or "").strip()
    conflict = str(overview.get("core_conflict") or "").strip()
    if logline and conflict and conflict not in logline:
        return f"{logline}；{conflict}"[:500]
    return (logline or conflict)[:500] or None


def _stored_episode_summary(value: str | None) -> str | None:
    normalized = str(value or "").strip()
    if not normalized or normalized == "由 projects.script_text 回填":
        return None
    return normalized


def _stored_episode_title(value: str | None, original_filename: str | None = None) -> str | None:
    normalized = str(value or "").strip()
    if not normalized or normalized == "历史导入剧本":
        return None
    filename = str(original_filename or "").strip().replace("\\", "/").rsplit("/", 1)[-1]
    filename_stem = filename.rsplit(".", 1)[0].strip() if "." in filename else filename
    if filename_stem and normalized.casefold() == filename_stem.casefold():
        return None
    return normalized


def _episode_breakdown_status(
    *,
    reading_revision: ArtifactRevision | None,
    breakdown_view: dict[str, Any],
    has_segments: bool,
    has_storyboards: bool,
    has_tasks: bool,
    stale_stages: list[str],
) -> str:
    status = "script_imported"
    reading_state = breakdown_view.get("reading_review_state") if isinstance(breakdown_view.get("reading_review_state"), dict) else {}
    if reading_revision or (
        str(reading_state.get("status") or "") == "confirmed"
        and isinstance(breakdown_view.get("reading_report"), dict)
    ):
        status = "reading_confirmed"
    if any(breakdown_view.get(key) for key in ("asset_prompt_designs", "costume_designs", "asset_masters", "assets")):
        status = "asset_saved" if breakdown_view.get("source_label") in {
            "human_modified", "human_confirmed"
        } else "asset_draft"
    if has_segments:
        status = "breakdown_confirmed"
    if has_storyboards:
        status = "storyboard_ready"
    if has_tasks:
        status = "production"
    if stale_stages:
        status = "outdated"
    return status


def _episode_stale_stages(project: Project, revision: ArtifactRevision | None) -> list[str]:
    if revision is None:
        return []
    snapshot = revision.input_snapshot or {}
    if not snapshot.get("project_profile_hash"):
        return []
    current = _project_profile_hashes(project)
    stages: list[str] = []
    if snapshot.get("content_type_hash") and snapshot.get("content_type_hash") != current["content_type_hash"]:
        stages.extend(["reading", "breakdown", "assets", "storyboards", "tasks"])
    if snapshot.get("cultural_contexts_hash") and snapshot.get("cultural_contexts_hash") != current["cultural_contexts_hash"]:
        stages.extend(["reading", "breakdown", "assets", "storyboards", "tasks"])
    if snapshot.get("delivery_aspect_ratio_hash") and snapshot.get("delivery_aspect_ratio_hash") != current["delivery_aspect_ratio_hash"]:
        stages.extend(["storyboards", "tasks"])
    if snapshot.get("primary_style_hash") and snapshot.get("primary_style_hash") != current["primary_style_hash"]:
        stages.extend(["assets", "storyboards", "tasks"])
    if not snapshot.get("production_brief_hash"):
        if snapshot.get("genre_hash") != current["genre_hash"]:
            stages.extend(["assets", "storyboards", "tasks"])
    return list(dict.fromkeys(stages))


async def _episode_stale_stages_for_revision(
    session: AsyncSession,
    project: Project,
    revision: ArtifactRevision | None,
) -> list[str]:
    stages = _episode_stale_stages(project, revision)
    if revision is not None:
        snapshot = revision.input_snapshot or {}
        episode_id = _uuid_or_none(snapshot.get("episode_id"))
        if episode_id and snapshot.get("episode_production_brief_hash"):
            episode = await session.get(ProjectEpisode, episode_id)
            current_episode_hash = _content_hash(
                episode.production_brief or {}
            ) if episode and episode.project_id == project.id else ""
            if current_episode_hash != snapshot.get("episode_production_brief_hash"):
                stages.extend(["storyboards", "tasks"])
                stages = list(dict.fromkeys(stages))
    if revision is None or (revision.input_snapshot or {}).get("project_profile_hash"):
        return stages
    profile_revision = await session.scalar(
        select(ArtifactRevision).where(
            ArtifactRevision.project_id == project.id,
            ArtifactRevision.artifact_type == "project_profile",
            ArtifactRevision.created_at > revision.created_at,
        ).order_by(ArtifactRevision.created_at.desc()).limit(1)
    )
    if profile_revision is None:
        return []
    changed_fields = set((profile_revision.change_diff or {}).get("changed_fields") or [])
    if changed_fields & {"genre"}:
        return ["reading", "breakdown", "assets", "storyboards", "tasks"]
    if "production_brief" in changed_fields:
        before = (profile_revision.change_diff or {}).get("before") or {}
        after = (profile_revision.change_diff or {}).get("after") or {}
        previous_brief = before.get("production_brief") or {}
        next_brief = after.get("production_brief") or {}
        if previous_brief.get("content_type") != next_brief.get("content_type"):
            return ["reading", "breakdown", "assets", "storyboards", "tasks"]
        if (
            previous_brief.get("cultural_contexts") != next_brief.get("cultural_contexts")
            or previous_brief.get("primary_cultural_context_code") != next_brief.get("primary_cultural_context_code")
        ):
            return ["reading", "breakdown", "assets", "storyboards", "tasks"]
        if previous_brief.get("delivery_aspect_ratio") != next_brief.get("delivery_aspect_ratio"):
            stages.extend(["storyboards", "tasks"])
        if previous_brief.get("primary_style_id") != next_brief.get("primary_style_id"):
            stages.extend(["assets", "storyboards", "tasks"])
        return list(dict.fromkeys(stages))
    return []


async def hard_delete_project_episode(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    confirm_episode_code: str,
    delete_files: bool,
    user: User,
) -> tuple[ProjectEpisodeDeleteResponse, list[dict[str, Any]], UUID]:
    if not is_director_or_admin(user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    episode = await session.get(ProjectEpisode, episode_id)
    if project is None or project.deleted_at is not None or episode is None or episode.project_id != project_id:
        raise KeyError(episode_id)
    if confirm_episode_code.strip().upper() != episode.episode_code.upper():
        raise ValueError(f"删除确认不匹配，请输入分集编号 {episode.episode_code}。")

    scripts = list((await session.scalars(select(Script).where(
        Script.project_id == project_id, Script.episode_id == episode_id
    ))).all())
    script_ids = {item.id for item in scripts}
    script_versions = list((await session.scalars(select(ScriptVersion).where(
        ScriptVersion.script_id.in_(script_ids)
    ))).all()) if script_ids else []
    script_version_ids = {item.id for item in script_versions}
    source_file_ids = {item.source_file_id for item in script_versions if item.source_file_id}

    segments = list((await session.scalars(select(ScriptSegment).where(
        ScriptSegment.project_id == project_id,
        or_(ScriptSegment.episode_id == episode_id, ScriptSegment.episode_code == episode.episode_code),
    ))).all())
    segment_ids = {item.id for item in segments}
    storyboard_conditions = [
        Storyboard.project_id == project_id,
        Storyboard.episode_num == episode.episode_no,
    ]
    if script_ids:
        storyboard_conditions.append(Storyboard.script_id.in_(script_ids))
    if segment_ids:
        storyboard_conditions.append(Storyboard.script_segment_id.in_(segment_ids))
    storyboards = list((await session.scalars(select(Storyboard).where(
        Storyboard.project_id == project_id, or_(*storyboard_conditions[1:])
    ))).all())
    storyboard_ids = {item.id for item in storyboards if item.project_id == project_id}

    task_filters = [Task.episode_id == episode_id]
    if script_ids:
        task_filters.append(Task.script_id.in_(script_ids))
    if segment_ids:
        task_filters.append(Task.script_segment_id.in_(segment_ids))
    if storyboard_ids:
        task_filters.append(Task.storyboard_id.in_(storyboard_ids))
    task_ids = set((await session.scalars(select(Task.id).where(
        Task.project_id == project_id, or_(*task_filters)
    ))).all())

    output_filter_parts: list[Any] = [ImageOutput.episode_id == episode_id]
    if script_ids:
        output_filter_parts.append(ImageOutput.script_id.in_(script_ids))
    if storyboard_ids:
        output_filter_parts.append(ImageOutput.storyboard_id.in_(storyboard_ids))
    if task_ids:
        output_filter_parts.append(ImageOutput.task_id.in_(task_ids))
    image_outputs = list((await session.scalars(select(ImageOutput).where(
        ImageOutput.project_id == project_id, or_(*output_filter_parts)
    ))).all())
    video_filter_parts: list[Any] = [VideoOutput.episode_id == episode_id]
    if script_ids:
        video_filter_parts.append(VideoOutput.script_id.in_(script_ids))
    if storyboard_ids:
        video_filter_parts.append(VideoOutput.storyboard_id.in_(storyboard_ids))
    if task_ids:
        video_filter_parts.append(VideoOutput.task_id.in_(task_ids))
    video_outputs = list((await session.scalars(select(VideoOutput).where(
        VideoOutput.project_id == project_id, or_(*video_filter_parts)
    ))).all())
    final_filter_parts: list[Any] = [FinalOutput.episode_id == episode_id]
    if task_ids:
        final_filter_parts.append(FinalOutput.task_id.in_(task_ids))
    final_outputs = list((await session.scalars(select(FinalOutput).where(
        FinalOutput.project_id == project_id, or_(*final_filter_parts)
    ))).all())
    image_ids = {item.id for item in image_outputs}
    video_ids = {item.id for item in video_outputs}
    final_ids = {item.id for item in final_outputs}
    candidate_file_ids = source_file_ids | {
        item.file_id for item in [*image_outputs, *video_outputs, *final_outputs] if item.file_id
    }
    candidate_file_ids.update((await session.scalars(select(FileObject.id).where(
        or_(FileObject.episode_id == episode_id, FileObject.task_id.in_(task_ids))
    ))).all())

    submissions = list((await session.scalars(select(Submission).where(
        or_(Submission.task_id.in_(task_ids), Submission.storyboard_id.in_(storyboard_ids))
    ))).all()) if task_ids or storyboard_ids else []
    submission_locations = {item.file_path for item in submissions if item.file_path}
    if submission_locations:
        all_files = list((await session.scalars(select(FileObject))).all())
        for file_object in all_files:
            uri = f"minio://{file_object.bucket}/{file_object.object_key}"
            if uri in submission_locations or file_object.object_key in submission_locations:
                candidate_file_ids.add(file_object.id)

    runs = list((await session.scalars(select(AgentRun).where(AgentRun.project_id == project_id))).all())
    agent_run_ids = {
        run.id for run in runs
        if run.episode_id == episode_id
        or run.script_id in script_ids
        or run.storyboard_id in storyboard_ids
        or run.task_id in task_ids
        or _lineage_matches_episode(run.input_json, episode_id, script_ids, script_version_ids)
    }
    reading_revisions = list((await session.scalars(select(ArtifactRevision).where(
        ArtifactRevision.project_id == project_id,
        ArtifactRevision.artifact_type == "reading_report",
    ))).all())
    reading_revision_ids = {
        item.id for item in reading_revisions
        if _lineage_matches_episode(item.input_snapshot, episode_id, script_ids, script_version_ids)
    }
    application_revisions = list((await session.scalars(select(ArtifactRevision).where(
        ArtifactRevision.project_id == project_id,
        ArtifactRevision.artifact_type == "reading_asset_application",
        ArtifactRevision.artifact_id.in_(reading_revision_ids),
    ))).all()) if reading_revision_ids else []
    application_revision_ids = {item.id for item in application_revisions}
    lineage_artifact_ids = reading_revision_ids | application_revision_ids

    breakdowns = list((await session.scalars(select(ScriptBreakdown).where(
        ScriptBreakdown.project_id == project_id
    ))).all())
    breakdown_ids = {
        item.id for item in breakdowns
        if item.agent_run_id in agent_run_ids
        or _lineage_matches_episode(item.content_json, episode_id, script_ids, script_version_ids, episode.episode_code)
    }
    entity_versions = list((await session.scalars(select(EntityVersion).where(
        EntityVersion.project_id == project_id
    ))).all())
    entity_version_ids = {
        item.id for item in entity_versions
        if item.entity_id in segment_ids | storyboard_ids
        or _lineage_matches_episode(item.content_json, episode_id, script_ids, script_version_ids, episode.episode_code)
    }
    training_samples = list((await session.scalars(select(AgentTrainingSample).where(
        AgentTrainingSample.project_id == project_id
    ))).all())
    training_sample_ids = {
        item.id for item in training_samples
        if item.sample_type != "reading_report_confirmed"
        and (
            _lineage_matches_episode(item.input_json, episode_id, script_ids, script_version_ids, episode.episode_code)
            or _lineage_matches_episode(item.confirmed_output_json, episode_id, script_ids, script_version_ids, episode.episode_code)
        )
    }
    workflows = list((await session.scalars(select(WorkflowRun).where(
        WorkflowRun.project_id == project_id
    ))).all())
    workflow_ids = {
        item.id for item in workflows
        if item.agent_run_id in agent_run_ids
        or _lineage_matches_episode(item.input_json, episode_id, script_ids, script_version_ids)
    }

    counts: dict[str, int] = {}

    async def delete_ids(model: Any, ids: set[UUID], label: str) -> None:
        if not ids:
            counts[label] = 0
            return
        result = await session.execute(delete(model).where(model.id.in_(ids)))
        counts[label] = int(result.rowcount or 0)

    await delete_ids(EpisodeAssetBinding, set((await session.scalars(select(EpisodeAssetBinding.id).where(
        EpisodeAssetBinding.episode_id == episode_id
    ))).all()), "asset_bindings")
    relation_filters: list[Any] = [AssetRelation.episode_id == episode_id]
    if script_ids:
        relation_filters.append(AssetRelation.script_id.in_(script_ids))
    if storyboard_ids:
        relation_filters.append(AssetRelation.storyboard_id.in_(storyboard_ids))
    if image_ids:
        relation_filters.append(AssetRelation.image_output_id.in_(image_ids))
    if video_ids:
        relation_filters.append(AssetRelation.video_output_id.in_(video_ids))
    if final_ids:
        relation_filters.append(AssetRelation.final_output_id.in_(final_ids))
    relation_delete = await session.execute(delete(AssetRelation).where(or_(*relation_filters)))
    counts["asset_relations"] = int(relation_delete.rowcount or 0)
    if storyboard_ids:
        await session.execute(update(ProjectAsset).where(ProjectAsset.storyboard_id.in_(storyboard_ids)).values(storyboard_id=None))
    item_filters: list[Any] = [ProjectAssetListItem.episode_id == episode_id]
    if script_ids:
        item_filters.append(ProjectAssetListItem.script_id.in_(script_ids))
    if segment_ids:
        item_filters.append(ProjectAssetListItem.script_segment_id.in_(segment_ids))
    if storyboard_ids:
        item_filters.append(ProjectAssetListItem.storyboard_id.in_(storyboard_ids))
    item_delete = await session.execute(delete(ProjectAssetListItem).where(or_(*item_filters)))
    counts["asset_list_items"] = int(item_delete.rowcount or 0)
    if script_ids or agent_run_ids:
        list_filters: list[Any] = []
        if script_ids:
            list_filters.append(ProjectAssetList.source_script_id.in_(script_ids))
        if agent_run_ids:
            list_filters.append(ProjectAssetList.source_agent_run_id.in_(agent_run_ids))
        episode_asset_list_ids = set((await session.scalars(
            select(ProjectAssetList.id).where(or_(*list_filters))
        )).all())
        await session.execute(update(ProjectAssetList).where(ProjectAssetList.id.in_(episode_asset_list_ids)).values(
            source_script_id=None, source_agent_run_id=None
        ))
        empty_list_ids: set[UUID] = set()
        for asset_list_id in episode_asset_list_ids:
            has_items = bool(await session.scalar(select(ProjectAssetListItem.id).where(
                ProjectAssetListItem.asset_list_id == asset_list_id
            ).limit(1)))
            if not has_items:
                empty_list_ids.add(asset_list_id)
        await delete_ids(ProjectAssetList, empty_list_ids, "empty_asset_lists")

    if task_ids:
        await session.execute(update(Task).where(Task.depends_on_task_id.in_(task_ids)).values(depends_on_task_id=None))
        await delete_ids(TaskPrompt, set((await session.scalars(select(TaskPrompt.id).where(TaskPrompt.task_id.in_(task_ids)))).all()), "task_prompts")
    await delete_ids(Submission, {item.id for item in submissions}, "submissions")
    await delete_ids(AssetRelation, set(), "asset_relations_extra")
    await delete_ids(ImageOutput, image_ids, "image_outputs")
    await delete_ids(VideoOutput, video_ids, "video_outputs")
    await delete_ids(FinalOutput, final_ids, "final_outputs")
    if task_ids:
        await session.execute(update(AssetVersion).where(AssetVersion.source_task_id.in_(task_ids)).values(source_task_id=None))
    if agent_run_ids:
        await session.execute(update(AssetVersion).where(AssetVersion.source_agent_run_id.in_(agent_run_ids)).values(source_agent_run_id=None))
        await session.execute(update(StoryboardVersion).where(
            StoryboardVersion.source_agent_run_id.in_(agent_run_ids),
            StoryboardVersion.storyboard_id.not_in(storyboard_ids),
        ).values(source_agent_run_id=None))
        await session.execute(update(ScriptVersion).where(
            ScriptVersion.source_agent_run_id.in_(agent_run_ids),
            ScriptVersion.id.not_in(script_version_ids),
        ).values(source_agent_run_id=None))
        await session.execute(update(EntityVersion).where(
            EntityVersion.agent_run_id.in_(agent_run_ids),
            EntityVersion.id.not_in(entity_version_ids),
        ).values(agent_run_id=None))
        await session.execute(update(ArtifactRevision).where(
            ArtifactRevision.source_run_id.in_(agent_run_ids),
        ).values(source_run_id=None))
    await delete_ids(AgentFeedback, set((await session.scalars(select(AgentFeedback.id).where(
        or_(AgentFeedback.run_id.in_(agent_run_ids), AgentFeedback.task_id.in_(task_ids))
    ))).all()) if agent_run_ids or task_ids else set(), "agent_feedback")
    await delete_ids(AgentIssue, set((await session.scalars(select(AgentIssue.id).where(
        AgentIssue.run_id.in_(agent_run_ids)
    ))).all()) if agent_run_ids else set(), "agent_issues")
    await delete_ids(StoryboardVersion, set((await session.scalars(select(StoryboardVersion.id).where(
        StoryboardVersion.storyboard_id.in_(storyboard_ids)
    ))).all()) if storyboard_ids else set(), "storyboard_versions")
    await delete_ids(WorkflowRun, workflow_ids, "workflow_runs")
    await delete_ids(ScriptBreakdown, breakdown_ids, "breakdown_revisions")
    if entity_version_ids:
        await session.execute(update(EntityVersion).where(EntityVersion.base_version_id.in_(entity_version_ids)).values(base_version_id=None))
    await delete_ids(EntityVersion, entity_version_ids, "entity_versions")
    await delete_ids(AgentTrainingSample, training_sample_ids, "training_samples")
    counts["reading_revisions_retained_for_asset_audit"] = len(lineage_artifact_ids)
    if candidate_file_ids:
        await session.execute(update(FileObject).where(FileObject.id.in_(candidate_file_ids)).values(
            task_id=None, episode_id=None
        ))
    await delete_ids(ScriptVersion, script_version_ids, "script_versions")
    await delete_ids(AgentRun, agent_run_ids, "agent_runs")
    await delete_ids(Task, task_ids, "tasks")
    await delete_ids(Storyboard, storyboard_ids, "storyboards")
    await delete_ids(ScriptSegment, segment_ids, "script_segments")
    await delete_ids(Script, script_ids, "scripts")
    await session.delete(episode)
    counts["episodes"] = 1
    await session.flush()

    remaining_scripts = list((await session.scalars(
        select(Script)
        .join(ProjectEpisode, Script.episode_id == ProjectEpisode.id)
        .where(Script.project_id == project_id)
        .order_by(ProjectEpisode.episode_no)
    )).all())
    project.script_text = "\n\n".join(
        f"--- {item.script_code} / {item.title or ''} ---\n{item.content.strip()}".strip()
        for item in remaining_scripts if item.content.strip()
    )
    project.updated_at = datetime.now(UTC)

    cleanup_objects: list[dict[str, Any]] = []
    if delete_files and candidate_file_ids:
        file_objects = list((await session.scalars(select(FileObject).where(FileObject.id.in_(candidate_file_ids)))).all())
        cleanup_objects = [
            {"file_id": str(item.id), "bucket": item.bucket, "object_key": item.object_key, "file_name": item.file_name}
            for item in file_objects
        ]
    operation_log = OperationLog(
        operator_id=user.id,
        project_id=project_id,
        target_type="project_episode",
        target_id=episode_id,
        action="project_episode_hard_deleted",
        detail={
            "episode_code": episode.episode_code,
            "deleted_counts": counts,
            "delete_files": delete_files,
            "file_cleanup": {"status": "pending" if cleanup_objects else "not_requested", "objects": cleanup_objects},
        },
    )
    session.add(operation_log)
    await session.commit()
    return ProjectEpisodeDeleteResponse(
        project_id=project_id,
        episode_id=episode_id,
        episode_code=episode.episode_code,
        deleted_counts=counts,
        file_cleanup={"pending": len(cleanup_objects)},
    ), cleanup_objects, operation_log.id


def _lineage_matches_episode(
    payload: Any,
    episode_id: UUID,
    script_ids: set[UUID],
    script_version_ids: set[UUID],
    episode_code: str | None = None,
) -> bool:
    if isinstance(payload, list):
        return any(
            _lineage_matches_episode(item, episode_id, script_ids, script_version_ids, episode_code)
            for item in payload
        )
    if not isinstance(payload, dict):
        return False
    episode_ids = {str(episode_id)}
    script_id_values = {str(value) for value in script_ids}
    version_id_values = {str(value) for value in script_version_ids}
    for key, value in payload.items():
        if key == "episode_id" and str(value) in episode_ids:
            return True
        if key == "script_id" and str(value) in script_id_values:
            return True
        if key == "script_version_id" and str(value) in version_id_values:
            return True
        if episode_code and key == "episode_code" and str(value).upper() == episode_code.upper():
            return True
        if isinstance(value, (dict, list)) and _lineage_matches_episode(
            value, episode_id, script_ids, script_version_ids, episode_code
        ):
            return True
    return False


async def cleanup_deleted_episode_files(
    session: AsyncSession,
    operation_log_id: UUID,
    cleanup_objects: list[dict[str, Any]],
) -> dict[str, Any]:
    from app.services.storage import delete_stored_object

    succeeded: list[dict[str, Any]] = []
    failed: list[dict[str, Any]] = []
    shared: list[dict[str, Any]] = []
    for item in cleanup_objects:
        file_id = _uuid_or_none(item.get("file_id"))
        if file_id is None or await session.get(FileObject, file_id) is None:
            continue
        references = 0
        for model, column in (
            (ScriptVersion, ScriptVersion.source_file_id),
            (AssetVersion, AssetVersion.file_id),
            (AssetVersion, AssetVersion.preview_file_id),
            (ImageOutput, ImageOutput.file_id),
            (VideoOutput, VideoOutput.file_id),
            (FinalOutput, FinalOutput.file_id),
        ):
            references += int(await session.scalar(select(func.count(model.id)).where(column == file_id)) or 0)
        if references:
            shared.append({**item, "reason": "仍被项目级或其他分集数据引用"})
            continue
        try:
            delete_stored_object(str(item["bucket"]), str(item["object_key"]))
            succeeded.append(item)
        except Exception as exc:
            failed.append({**item, "error": str(exc)})
    if succeeded:
        await session.execute(update(FileObject).where(FileObject.id.in_([
            UUID(str(item["file_id"])) for item in succeeded
        ])).values(task_id=None, episode_id=None))
        await session.execute(delete(FileObject).where(FileObject.id.in_([
            UUID(str(item["file_id"])) for item in succeeded
        ])))
    operation_log = await session.get(OperationLog, operation_log_id)
    result = {
        "succeeded": len(succeeded),
        "failed": len(failed),
        "shared_retained": len(shared),
        "pending_retry": len(failed),
        "failed_objects": failed,
        "shared_objects": shared,
    }
    if operation_log:
        detail = dict(operation_log.detail or {})
        detail["file_cleanup"] = result
        operation_log.detail = detail
        operation_log.updated_at = datetime.now(UTC)
    await session.commit()
    return result


async def project_episode_exists(session: AsyncSession, project_id: UUID, episode_no: int) -> bool:
    return bool(await session.scalar(
        select(ProjectEpisode.id)
        .where(ProjectEpisode.project_id == project_id, ProjectEpisode.episode_no == episode_no)
        .limit(1)
    ))


async def find_duplicate_script_version(
    session: AsyncSession,
    project_id: UUID,
    episode_no: int,
    script_text: str,
) -> ScriptVersion | None:
    content_hash = hashlib.sha256(script_text.strip().encode("utf-8")).hexdigest()
    return await session.scalar(
        select(ScriptVersion)
        .join(Script, ScriptVersion.script_id == Script.id)
        .join(ProjectEpisode, Script.episode_id == ProjectEpisode.id)
        .where(
            Script.project_id == project_id,
            ProjectEpisode.episode_no == episode_no,
            ScriptVersion.content_hash == content_hash,
        )
        .order_by(ScriptVersion.version_no.desc())
        .limit(1)
    )


async def next_project_episode_no(session: AsyncSession, project_id: UUID) -> int:
    maximum = await session.scalar(
        select(func.max(ProjectEpisode.episode_no)).where(ProjectEpisode.project_id == project_id)
    )
    return int(maximum or 0) + 1


async def get_project_script_execution_context(
    session: AsyncSession,
    project_id: UUID,
    user: User,
    *,
    episode_id: UUID | None = None,
    script_version_id: UUID | None = None,
) -> dict[str, Any] | None:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    version = await session.get(ScriptVersion, script_version_id) if script_version_id else None
    script = await session.get(Script, version.script_id) if version else None
    if script is None and episode_id:
        script = await session.scalar(
            select(Script)
            .where(Script.project_id == project_id, Script.episode_id == episode_id)
            .order_by(Script.created_at)
            .limit(1)
        )
        version = await session.get(ScriptVersion, script.current_version_id) if script and script.current_version_id else None
    if script is None and not episode_id and not script_version_id:
        return None
    if script is None or script.project_id != project_id or version is None or version.script_id != script.id:
        raise KeyError(script_version_id or episode_id or project_id)
    episode = await session.get(ProjectEpisode, script.episode_id) if script.episode_id else None
    if episode is None or episode.project_id != project_id:
        raise KeyError(script.episode_id or project_id)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    return {
        "episode_id": str(episode.id),
        "episode_no": episode.episode_no,
        "episode_code": episode.episode_code,
        "script_id": str(script.id),
        "script_code": script.script_code,
        "script_version_id": str(version.id),
        "script_version_no": version.version_no,
        "script_text": version.content,
        "content_hash": version.content_hash,
        "source_file_id": str(version.source_file_id) if version.source_file_id else None,
        "original_filename": version.original_filename,
        "parser_name": version.parser_name,
        "language": version.language,
        "episode_production_brief": episode.production_brief or {},
    }


async def import_project_script_version(
    session: AsyncSession,
    project_id: UUID,
    *,
    episode_no: int,
    script_text: str,
    filename: str,
    parser_name: str,
    language: str,
    source_file_id: UUID | None,
    user: User,
    production_brief: dict[str, Any] | None = None,
    episode_brief: dict[str, Any] | None = None,
) -> tuple[ProjectRead, ProjectImportContext]:
    if not is_director_or_admin(user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    if episode_no < 1:
        raise ValueError("episode_no must be greater than zero")

    episode = await session.scalar(
        select(ProjectEpisode)
        .where(ProjectEpisode.project_id == project_id, ProjectEpisode.episode_no == episode_no)
        .limit(1)
    )
    created_episode = episode is None
    if episode is None:
        episode = ProjectEpisode(
            project_id=project_id,
            episode_no=episode_no,
            episode_code=f"EP{episode_no:02d}",
            title=None,
            production_brief=episode_brief or {},
        )
        session.add(episode)
        await session.flush()

    script = await session.scalar(
        select(Script)
        .where(Script.project_id == project_id, Script.episode_id == episode.id)
        .order_by(Script.created_at)
        .limit(1)
    )
    created_script = script is None
    if script is None:
        prefix = project.project_prefix or project.project_no or "RF"
        script = Script(
            project_id=project_id,
            episode_id=episode.id,
            script_code=f"{prefix}-EP{episode_no:02d}-SCRIPT",
            title=f"第{episode_no}集",
            content=script_text.strip(),
            status="draft",
            created_by=user.id,
        )
        session.add(script)
        await session.flush()

    version_no = int(await session.scalar(
        select(func.max(ScriptVersion.version_no)).where(ScriptVersion.script_id == script.id)
    ) or 0) + 1
    content = script_text.strip()
    content_hash = hashlib.sha256(content.encode("utf-8")).hexdigest()
    duplicate = await session.scalar(
        select(ScriptVersion).where(
            ScriptVersion.script_id == script.id,
            ScriptVersion.content_hash == content_hash,
        ).limit(1)
    )
    if duplicate is not None:
        raise ValueError(f"该分集已存在相同内容的 v{duplicate.version_no}，未创建重复版本。")
    version = ScriptVersion(
        script_id=script.id,
        version_no=version_no,
        source="import",
        content=content,
        content_hash=content_hash,
        source_file_id=source_file_id,
        original_filename=filename[:255],
        parser_name=parser_name[:80],
        language=language[:20],
        created_by=user.id,
    )
    session.add(version)
    await session.flush()

    script.content = content
    script.current_version_id = version.id
    script.updated_at = datetime.now(UTC)
    episode.updated_at = datetime.now(UTC)
    if episode_brief is not None:
        episode.production_brief = episode_brief
    if episode_brief is not None:
        episode.production_brief = dict(episode_brief)
    if production_brief is not None:
        project.production_brief = dict(production_brief)

    current_scripts = list((await session.scalars(
        select(Script)
        .join(ProjectEpisode, Script.episode_id == ProjectEpisode.id)
        .where(Script.project_id == project_id)
        .order_by(ProjectEpisode.episode_no)
    )).all())
    project.script_text = "\n\n".join(
        f"--- {item.script_code} / {item.title or ''} ---\n{item.content.strip()}".strip()
        for item in current_scripts
        if item.content.strip()
    )
    project.status = ProjectStatus.draft if project.status == ProjectStatus.archived else project.status
    project.updated_at = datetime.now(UTC)
    await session.commit()
    await session.refresh(project)
    return project_to_read(project), ProjectImportContext(
        episode_id=episode.id,
        episode_no=episode.episode_no,
        episode_code=episode.episode_code,
        script_id=script.id,
        script_version_id=version.id,
        script_version_no=version.version_no,
        created_episode=created_episode,
        created_script=created_script,
        replaced_existing_episode=not created_episode,
        content_hash=content_hash,
    )


async def merge_project_script(
    session: AsyncSession,
    project_id: UUID,
    *,
    script_text: str,
    filename: str,
    user: User,
    production_brief: dict[str, Any] | None = None,
) -> ProjectRead:
    if not is_director_or_admin(user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    imported_text = script_text.strip()
    if imported_text:
        marker = f"\n\n--- 导入合并：{filename} / {datetime.now(UTC).isoformat()} ---\n"
        project.script_text = f"{project.script_text or ''}{marker}{imported_text}".strip()
    if production_brief is not None:
        project.production_brief = dict(production_brief)
    project.status = ProjectStatus.draft if project.status == ProjectStatus.archived else project.status
    project.updated_at = datetime.now(UTC)
    await session.commit()
    await session.refresh(project)
    await invalidate_project_status(str(project.id))
    return project_to_read(project)


async def get_project(session: AsyncSession, project_id: UUID, user: User) -> ProjectRead:
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    if is_director_or_admin(user):
        storyboard_count = await session.scalar(select(func.count(Storyboard.id)).where(Storyboard.project_id == project.id)) or 0
        task_count = await session.scalar(select(func.count(Task.id)).where(
            Task.project_id == project.id,
            Task.is_retired.is_(False),
        )) or 0
    else:
        storyboard_count = len(await _visible_storyboard_ids_for_project(session, project_id, user))
        task_count = await session.scalar(_visible_task_stmt(user).where(Task.project_id == project_id).with_only_columns(func.count(Task.id))) or 0
    return project_to_read(project, storyboard_count, task_count)


async def archive_project(
    session: AsyncSession,
    project_id: UUID,
    *,
    admin_password: str,
    reason: str | None,
    user: User,
) -> ProjectRead:
    if not is_director_or_admin(user):
        raise PermissionError(project_id)
    await _verify_admin_password(session, admin_password, user)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    now = datetime.now(UTC)
    project.status = ProjectStatus.archived
    project.current_stage = ProjectStatus.archived
    project.archived_at = project.archived_at or now
    project.updated_at = now
    await add_operation_log(
        session,
        action="project_archived",
        target_type="project",
        operator_id=user.id,
        project_id=project.id,
        target_id=project.id,
        detail={"reason": reason},
        commit=False,
    )
    await session.commit()
    await session.refresh(project)
    await invalidate_project_status(str(project.id))
    return project_to_read(project)


async def soft_delete_project(
    session: AsyncSession,
    project_id: UUID,
    *,
    admin_password: str,
    reason: str | None,
    user: User,
) -> ProjectRead:
    if not is_director_or_admin(user):
        raise PermissionError(project_id)
    await _verify_admin_password(session, admin_password, user)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    now = datetime.now(UTC)
    project.status = ProjectStatus.archived
    project.current_stage = ProjectStatus.archived
    project.archived_at = project.archived_at or now
    project.deleted_at = now
    project.deleted_by_id = user.id
    project.delete_reason = reason
    project.updated_at = now
    await add_operation_log(
        session,
        action="project_soft_deleted",
        target_type="project",
        operator_id=user.id,
        project_id=project.id,
        target_id=project.id,
        detail={"reason": reason},
        commit=False,
    )
    await session.commit()
    await session.refresh(project)
    await invalidate_project_status(str(project.id))
    return project_to_read(project)


async def list_storyboards(
    session: AsyncSession,
    project_id: UUID,
    user: User | None = None,
    *,
    episode_id: UUID | None = None,
) -> list[StoryboardRead]:
    stmt = select(Storyboard).where(Storyboard.project_id == project_id).order_by(Storyboard.episode_num, Storyboard.order_num)
    if episode_id:
        episode = await session.get(ProjectEpisode, episode_id)
        if episode is None or episode.project_id != project_id:
            raise KeyError(episode_id)
        stmt = stmt.where(Storyboard.episode_num == episode.episode_no)
    if user is not None and not is_director_or_admin(user):
        storyboard_ids = await _visible_storyboard_ids_for_project(session, project_id, user)
        if not storyboard_ids:
            return []
        stmt = stmt.where(Storyboard.id.in_(storyboard_ids))
    result = await session.execute(stmt)
    return [storyboard_to_read(item) for item in result.scalars().all()]


async def list_script_segments(
    session: AsyncSession,
    project_id: UUID,
    user: User,
    *,
    episode_id: UUID | None = None,
    status: str | None = None,
) -> list[ScriptSegmentRead]:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    stmt = select(ScriptSegment).where(ScriptSegment.project_id == project_id)
    if episode_id:
        stmt = stmt.where(ScriptSegment.episode_id == episode_id)
    if status:
        # Enforce the confirmed-segment filter in SQL so downstream callers cannot
        # accidentally read draft segments by forgetting an application-layer filter.
        stmt = stmt.where(ScriptSegment.metadata_json.op("->>")("status") == status)
    result = await session.execute(stmt.order_by(ScriptSegment.episode_code, ScriptSegment.order_no))
    return [script_segment_to_read(item) for item in result.scalars().all()]


async def create_script_segment(session: AsyncSession, data: ScriptSegmentCreate, user: User) -> ScriptSegmentRead:
    if not is_director_or_admin(user):
        raise PermissionError(data.project_id)
    segment = ScriptSegment(
        project_id=data.project_id,
        episode_id=data.episode_id,
        script_segment_code=data.script_segment_code,
        episode_code=data.episode_code,
        order_no=data.order_no,
        title=data.title,
        source_text=data.source_text,
        summary=data.summary,
        story_function=data.story_function,
        dominant_emotion=data.dominant_emotion,
        rhythm=data.rhythm,
        viewpoint=data.viewpoint,
        context_code=data.context_code,
        render_mode=data.render_mode,
        metadata_json=data.metadata_json,
        created_by=data.created_by or user.id,
    )
    session.add(segment)
    await session.flush()
    version = await create_entity_version(
        session,
        EntityVersionCreate(
            project_id=segment.project_id,
            entity_type="script_segment",
            entity_id=segment.id,
            entity_code=segment.script_segment_code,
            source=(segment.metadata_json or {}).get("source", "agent"),
            status=(segment.metadata_json or {}).get("status", "draft"),
            content_json=data.model_dump(mode="json"),
            created_by=segment.created_by,
        ),
        commit=False,
    )
    segment.current_version_id = version.id
    await session.commit()
    await session.refresh(segment)
    return script_segment_to_read(segment)


async def create_entity_version(
    session: AsyncSession,
    data: EntityVersionCreate,
    *,
    commit: bool = True,
) -> EntityVersionRead:
    if data.is_current:
        await session.execute(
            EntityVersion.__table__.update()
            .where(EntityVersion.entity_type == data.entity_type, EntityVersion.entity_code == data.entity_code)
            .values(is_current=False)
        )
    version_no = (
        await session.scalar(
            select(func.max(EntityVersion.version_no)).where(
                EntityVersion.entity_type == data.entity_type,
                EntityVersion.entity_code == data.entity_code,
            )
        )
        or 0
    ) + 1
    version = EntityVersion(
        project_id=data.project_id,
        entity_type=data.entity_type,
        entity_id=data.entity_id,
        entity_code=data.entity_code,
        version_no=version_no,
        version_code=f"{data.entity_code}-V{version_no:03d}",
        source=data.source,
        status=data.status,
        content_json=data.content_json,
        change_summary=data.change_summary,
        changed_fields=data.changed_fields,
        base_version_id=data.base_version_id,
        agent_run_id=data.agent_run_id,
        created_by=data.created_by,
        is_current=data.is_current,
    )
    session.add(version)
    if commit:
        await session.commit()
        await session.refresh(version)
    else:
        await session.flush()
    return entity_version_to_read(version)


async def create_training_sample(
    session: AsyncSession,
    data: AgentTrainingSampleCreate,
    user: User,
) -> AgentTrainingSampleRead:
    if not is_director_or_admin(user):
        raise PermissionError(data.project_id)
    sample = AgentTrainingSample(
        project_id=data.project_id,
        agent_type=AgentKind(data.agent_type.value) if data.agent_type else None,
        sample_type=data.sample_type,
        entity_type=data.entity_type,
        entity_code=data.entity_code,
        input_json=data.input_json,
        agent_output_json=data.agent_output_json,
        human_modified_output_json=data.human_modified_output_json,
        confirmed_output_json=data.confirmed_output_json,
        change_summary=data.change_summary,
        quality_score=data.quality_score,
        training_tags=data.training_tags,
        training_ready=data.training_ready,
        created_by=data.created_by or user.id,
    )
    session.add(sample)
    await session.commit()
    await session.refresh(sample)
    return AgentTrainingSampleRead(
        sample_id=str(sample.id),
        sample_type=sample.sample_type,
        schema_version="1.0",
        run_id=None,
        agent_type=sample.agent_type,
        input=sample.input_json or {},
        original_output=sample.agent_output_json or {},
        edited_output=sample.human_modified_output_json or {},
        confirmed_output=sample.confirmed_output_json or {},
        rating=sample.quality_score,
        note=None,
        entity_type=sample.entity_type,
        entity_code=sample.entity_code,
        change_summary=sample.change_summary or [],
        training_tags=sample.training_tags or [],
        training_ready=sample.training_ready,
        created_at=sample.created_at,
    )


def _lineage_text(*values: Any) -> str | None:
    for value in values:
        if value is not None and str(value).strip():
            return str(value).strip()
    return None


def _agent_run_lineage(run: AgentRunRead) -> dict[str, str | None]:
    run_input = run.input if isinstance(run.input, dict) else {}
    run_output = run.output if isinstance(run.output, dict) else {}
    input_runtime = run_input.get("runtime_metadata") if isinstance(run_input.get("runtime_metadata"), dict) else {}
    output_runtime = run_output.get("runtime_metadata") if isinstance(run_output.get("runtime_metadata"), dict) else {}
    output_source = run_output.get("source") if isinstance(run_output.get("source"), dict) else {}
    agent_value = run.agent_type.value if hasattr(run.agent_type, "value") else str(run.agent_type)
    input_hash = _lineage_text(
        run.input_hash,
        run_input.get("input_hash"),
        input_runtime.get("input_hash"),
        output_runtime.get("input_hash"),
        output_source.get("input_hash"),
    )
    if input_hash is None:
        input_hash = _content_hash({key: value for key, value in run_input.items() if key != "input_hash"})
    return {
        "skill_name": _lineage_text(
            run.skill_name,
            run_input.get("skill"),
            input_runtime.get("skill"),
            run_output.get("skill"),
            output_runtime.get("skill"),
            agent_value.replace("_", "-"),
        ),
        "skill_version": _lineage_text(
            run.skill_version,
            run_input.get("skill_version"),
            input_runtime.get("skill_version"),
            run_output.get("skill_version"),
            output_runtime.get("skill_version"),
        ),
        "contract_version": _lineage_text(
            run.contract_version,
            run_input.get("contract_version"),
            input_runtime.get("contract_version"),
            run_output.get("contract_version"),
            output_runtime.get("contract_version"),
        ),
        "contract_hash": _lineage_text(
            run.contract_hash,
            run_input.get("contract_hash"),
            input_runtime.get("contract_hash"),
            run_output.get("contract_hash"),
            output_runtime.get("contract_hash"),
        ),
        "prompt_version": _lineage_text(
            run.prompt_version,
            run_input.get("prompt_version"),
            input_runtime.get("prompt_version"),
            run_output.get("prompt_version"),
            output_runtime.get("prompt_version"),
        ),
        "input_hash": input_hash,
        "model": _lineage_text(
            run.model,
            run_output.get("llm_model"),
            output_runtime.get("model"),
            output_source.get("model"),
        ),
    }


async def save_agent_run(session: AsyncSession, run: AgentRunRead) -> AgentRunRead:
    existing = await session.get(AgentRun, run.id)
    if existing is None:
        existing = AgentRun(
            id=run.id,
            project_id=run.project_id,
            parent_run_id=run.parent_run_id,
            workflow_run_id=run.workflow_run_id,
            node_key=run.node_key,
            storyboard_id=run.storyboard_id,
            task_id=run.task_id,
            agent_type=run.agent_type,
            created_at=run.created_at,
        )
        session.add(existing)
    existing.status = run.status
    existing.parent_run_id = run.parent_run_id or existing.parent_run_id
    existing.workflow_run_id = run.workflow_run_id or existing.workflow_run_id
    existing.node_key = run.node_key or existing.node_key
    full_input = run.input or {}
    full_output = run.output
    existing.input_json = full_input
    existing.output_json = full_output
    _apply_agent_run_payload_metadata(
        existing,
        input_payload=existing.input_json,
        output_payload=existing.output_json,
    )
    existing.error_message = run.error_message
    existing.duration_ms = run.duration_ms
    existing.token_usage = run.token_usage
    existing.feedback_json = run.feedback_json
    existing.data_state = "agent_raw"
    lineage = _agent_run_lineage(run)
    existing.model = run.model or lineage["model"]
    existing.skill_name = run.skill_name or lineage["skill_name"]
    existing.skill_version = run.skill_version or lineage["skill_version"]
    existing.contract_version = run.contract_version or lineage["contract_version"]
    existing.contract_hash = run.contract_hash or lineage["contract_hash"]
    existing.prompt_version = run.prompt_version or lineage["prompt_version"]
    existing.input_hash = run.input_hash or lineage["input_hash"]
    # P0-4: debug_output 是 breakdown_output 的整份重复（feedback_candidate 已保留同源
    # 内容供训练），且无任何生产读点、归一化写库时本就剔除。仅从进 MinIO 的 payload_object
    # 剔除它以省对象存储；未 externalize 的小 run 其 output_json(=full_output, 3985行) 仍保留
    # debug_output 供排障。agent_raw_asset_output/raw_model_response 保留（唯一 raw 训练样本）。
    externalized_output = full_output
    if isinstance(full_output, dict) and "debug_output" in full_output:
        externalized_output = {key: value for key, value in full_output.items() if key != "debug_output"}
    payload_object = {
        "schema_version": "CineForgeAgentPayload.v1",
        "input": full_input,
        "output": externalized_output,
        "token_usage": run.token_usage,
        "feedback": run.feedback_json,
    }
    payload_size = len(json.dumps(payload_object, ensure_ascii=False, sort_keys=True, default=str, separators=(",", ":")).encode("utf-8"))
    if payload_size > settings.agent_payload_externalize_threshold_bytes:
        stored = store_json_payload(payload_object)
        existing.payload_ref = stored.ref
        existing.payload_size_bytes = stored.size_bytes
        existing.payload_sha256 = stored.sha256
        existing.input_json = {**_lineage_summary(full_input), "payload_externalized": True}
        existing.output_json = {"summary": dict(existing.summary_json or {}), "payload_externalized": True}
        existing.token_usage = None
        existing.feedback_json = None
    else:
        existing.payload_ref = None
    existing.updated_at = run.updated_at
    await session.commit()
    await session.refresh(existing)
    if existing.project_id:
        await publish_project_event(str(existing.project_id), "agent_run")
    return agent_run_to_read(existing)


async def create_workflow_run(
    session: AsyncSession,
    *,
    project_id: UUID,
    workflow_type: str,
    input_json: dict[str, Any] | None = None,
    created_by: UUID | None = None,
    idempotency_key: str | None = None,
) -> tuple[WorkflowRunRead, bool]:
    if idempotency_key:
        existing = await session.scalar(
            select(WorkflowRun).where(WorkflowRun.idempotency_key == idempotency_key).limit(1)
        )
        if existing is not None:
            # 幂等去重：running/queued/needs_review/succeeded 直接返回，防止重复执行。
            # 但 failed 终态不能永久占用幂等键、把同输入的重跑挡死——释放其键（保留失败
            # 记录本身供审计），放行新建。清键与下方新建在同一事务提交，原子安全。
            if str(existing.status or "") == "failed":
                existing.idempotency_key = None
                await session.flush()
            else:
                return workflow_run_to_read(existing), False
    lock_key = f"{project_id}:{workflow_type}"
    lock_token = await acquire_workflow_lock(lock_key)
    if settings.workflow_lock_enabled and lock_token is None:
        if idempotency_key:
            existing = await session.scalar(
                select(WorkflowRun).where(WorkflowRun.idempotency_key == idempotency_key).limit(1)
            )
            if existing is not None:
                return workflow_run_to_read(existing), False
        raise ValueError("当前项目已有同类工作流正在运行，请等待完成后再提交。")
    workflow = WorkflowRun(
        project_id=project_id,
        workflow_type=workflow_type,
        status="running",
        input_json=input_json or {},
        created_by=created_by,
        lock_key=lock_key,
        lock_token=lock_token,
        idempotency_key=idempotency_key,
    )
    _apply_workflow_payload_metadata(workflow)
    session.add(workflow)
    try:
        await session.commit()
    except IntegrityError:
        await session.rollback()
        await release_workflow_lock(lock_key, lock_token)
        if idempotency_key:
            existing = await session.scalar(
                select(WorkflowRun).where(WorkflowRun.idempotency_key == idempotency_key).limit(1)
            )
            if existing is not None:
                return workflow_run_to_read(existing), False
        raise
    except Exception:
        await release_workflow_lock(lock_key, lock_token)
        raise
    await session.refresh(workflow)
    return workflow_run_to_read(workflow), True


async def update_workflow_run_progress(
    session: AsyncSession,
    workflow_run_id: UUID,
    *,
    status: str | None = None,
    node_status: dict[str, Any] | None = None,
    progress: dict[str, Any] | None = None,
    error: dict[str, Any] | None = None,
    node_runtime: dict[str, Any] | None = None,
    workflow_output: dict[str, Any] | None = None,
) -> WorkflowRunRead | None:
    workflow = await session.get(WorkflowRun, workflow_run_id)
    if workflow is None:
        return None

    if status:
        workflow.status = status
    if node_status is not None:
        workflow.node_status_json = dict(node_status)
    agent_run_ids = await _sync_workflow_node_agent_runs(
        session,
        workflow,
        node_status or workflow.node_status_json or {},
        node_runtime=node_runtime,
        workflow_output=workflow_output,
    )
    if progress is not None:
        previous_result = dict(workflow.result_json or {})
        progress = _workflow_progress_with_timing(
            dict(progress),
            previous_result.get("progress") if isinstance(previous_result.get("progress"), dict) else {},
        )
        workflow.result_json = {
            **previous_result,
            "progress": dict(progress),
            "agent_run_ids": agent_run_ids,
        }
    if error is not None:
        workflow.error_json = {
            **dict(workflow.error_json or {}),
            "progress_error": dict(error),
        }
    _apply_workflow_payload_metadata(workflow)
    if workflow.status in {"succeeded", "failed", "needs_review", "materialized"}:
        await release_workflow_lock(workflow.lock_key or "", workflow.lock_token)
        workflow.lock_token = None
    await session.commit()
    await session.refresh(workflow)
    await publish_project_event(str(workflow.project_id), "workflow_run")
    return workflow_run_to_read(workflow)


_WORKFLOW_NODE_AGENT_TYPES: dict[str, AgentKind] = {
    "run_script_reading": AgentKind.script_reading,
    "run_asset_extract": AgentKind.asset_extract,
    "run_asset_costume_design": AgentKind.text_to_image_prompt,
    "run_script_segmentation": AgentKind.script_segmentation,
    "run_script_breakdown": AgentKind.script_breakdown,
    "run_relation_check": AgentKind.relation_check,
    "run_content_review": AgentKind.content_compliance_review,
}

_WORKFLOW_NODE_ERROR_TERMS: dict[str, tuple[str, ...]] = {
    "run_script_reading": ("剧本阅读", "剧本围读", "script-reading"),
    "run_asset_extract": ("资产候选", "资产提取", "asset-extract"),
    "run_asset_costume_design": (
        "资产预定稿", "资产提示", "服装设计", "asset-costume-design",
        "character-design-prompt", "scene-design-prompt", "prop-design-prompt",
    ),
    "run_script_segmentation": ("脚本段拆分", "script-segmentation"),
    "run_script_breakdown": ("分镜拆解", "script-breakdown"),
    "run_relation_check": ("关系校验", "relation-check"),
    "run_content_review": ("内容审查", "content-compliance-review"),
}


def _workflow_node_run_status(status: str) -> AgentRunStatus:
    if status == "running":
        return AgentRunStatus.running
    if status in {"failed", "failed_non_blocking"}:
        return AgentRunStatus.failed
    return AgentRunStatus.succeeded


async def _sync_workflow_node_agent_runs(
    session: AsyncSession,
    workflow: WorkflowRun,
    node_status: dict[str, Any],
    *,
    node_runtime: dict[str, Any] | None = None,
    workflow_output: dict[str, Any] | None = None,
) -> dict[str, str]:
    existing_runs = list((await session.scalars(
        select(AgentRun).where(
            AgentRun.workflow_run_id == workflow.id,
            AgentRun.node_key.is_not(None),
        )
    )).all())
    existing_by_node = {str(item.node_key): item for item in existing_runs if item.node_key}
    parent = await session.get(AgentRun, workflow.agent_run_id) if workflow.agent_run_id else None
    parent_input = dict(parent.input_json or {}) if parent is not None else dict(workflow.input_json or {})
    runtime_by_node = node_runtime or {}
    for node, agent_type in _WORKFLOW_NODE_AGENT_TYPES.items():
        raw_status = str(node_status.get(node) or "")
        if not raw_status or raw_status in {"skipped", "reused_confirmed"}:
            continue
        runtime = runtime_by_node.get(node) if isinstance(runtime_by_node.get(node), dict) else {}
        child = existing_by_node.get(node)
        if child is None:
            child_input = {
                **parent_input,
                "workflow_run_id": str(workflow.id),
                "parent_run_id": str(parent.id) if parent else None,
                "node_key": node,
                "skill": runtime.get("skill"),
                "skill_version": runtime.get("skill_version"),
                "contract_version": runtime.get("contract_version"),
                "contract_hash": runtime.get("contract_hash"),
                "input_hash": runtime.get("input_hash"),
                "runtime_metadata": runtime,
            }
            child = AgentRun(
                project_id=workflow.project_id,
                parent_run_id=parent.id if parent else None,
                workflow_run_id=workflow.id,
                node_key=node,
                episode_id=parent.episode_id if parent else None,
                script_id=parent.script_id if parent else None,
                agent_type=agent_type,
                status=_workflow_node_run_status(raw_status),
                input_json=child_input,
                model=parent.model if parent else None,
            )
            session.add(child)
            await session.flush()
            existing_by_node[node] = child
        child.status = _workflow_node_run_status(raw_status)
        child.updated_at = datetime.now(UTC)
        child.error_message = (
            _workflow_node_error_message(node, workflow_output or {})
            if child.status == AgentRunStatus.failed
            else None
        )
        if runtime:
            child.input_json = {
                **dict(child.input_json or {}),
                "skill": runtime.get("skill"),
                "skill_version": runtime.get("skill_version"),
                "contract_version": runtime.get("contract_version"),
                "contract_hash": runtime.get("contract_hash"),
                "input_hash": runtime.get("input_hash"),
                "runtime_metadata": runtime,
            }
            child.skill_name = _lineage_text(runtime.get("skill"), child.skill_name)
            child.skill_version = _lineage_text(runtime.get("skill_version"), child.skill_version)
            child.contract_version = _lineage_text(runtime.get("contract_version"), child.contract_version)
            child.contract_hash = _lineage_text(runtime.get("contract_hash"), child.contract_hash)
            child.prompt_version = _lineage_text(runtime.get("prompt_version"), child.prompt_version)
            child.input_hash = _lineage_text(runtime.get("input_hash"), child.input_hash)
        if workflow_output is not None:
            child.output_json = _workflow_node_output(node, workflow_output)
        _apply_agent_run_payload_metadata(
            child,
            input_payload=child.input_json,
            output_payload=child.output_json,
        )
        child_payload = {
            "schema_version": "CineForgeAgentPayload.v1",
            "input": child.input_json or {},
            "output": child.output_json,
            "token_usage": child.token_usage,
            "feedback": child.feedback_json,
        }
        child_payload_size = len(json.dumps(
            child_payload, ensure_ascii=False, sort_keys=True, default=str, separators=(",", ":")
        ).encode("utf-8"))
        if child_payload_size > settings.agent_payload_externalize_threshold_bytes:
            stored = store_json_payload(child_payload)
            child.payload_ref = stored.ref
            child.payload_size_bytes = stored.size_bytes
            child.payload_sha256 = stored.sha256
            child.input_json = {
                **_lineage_summary(child.input_json),
                "node_key": node,
                "payload_externalized": True,
            }
            child.output_json = {
                "summary": dict(child.summary_json or {}),
                "payload_externalized": True,
            }
            child.token_usage = None
            child.feedback_json = None
    return {node: str(child.id) for node, child in existing_by_node.items()}


def _workflow_node_error_message(node: str, output: dict[str, Any]) -> str | None:
    candidates = [*(output.get("errors") or []), *(output.get("warnings") or [])]
    terms = _WORKFLOW_NODE_ERROR_TERMS.get(node, (node,))
    for candidate in candidates:
        if isinstance(candidate, dict):
            candidate_node = str(candidate.get("node") or "")
            message = str(candidate.get("message") or candidate.get("error") or candidate)
            if candidate_node == node or any(term in message for term in terms):
                return message
            continue
        message = str(candidate)
        if any(term in message for term in terms):
            return message
    return None


def _workflow_node_output(node: str, output: dict[str, Any]) -> dict[str, Any]:
    view = output.get("breakdown_view") if isinstance(output.get("breakdown_view"), dict) else output
    if node == "run_script_reading":
        reading_output = output.get("reading_output")
        if isinstance(reading_output, dict) and reading_output:
            return dict(reading_output)
        return {"reading_report": view.get("reading_report") or {}}
    if node == "run_asset_extract":
        return dict(output.get("agent_raw_asset_output") or {})
    if node == "run_asset_costume_design":
        return {
            "asset_prompt_designs": list(view.get("asset_prompt_designs") or view.get("costume_designs") or []),
            "asset_prompt_processing_summary": list(view.get("asset_prompt_processing_summary") or view.get("costume_processing_summary") or []),
        }
    if node == "run_script_segmentation":
        return {"script_segments": list(view.get("script_segments") or [])}
    if node == "run_script_breakdown":
        # Keyframes/videos are embedded in and derived from storyboards; no separate
        # keyframe_plan/video_plan is persisted.
        return {"storyboards": list(view.get("storyboards") or [])}
    if node == "run_relation_check":
        relation = view.get("relation_check") if isinstance(view.get("relation_check"), dict) else {}
        return relation or {"relation_report": view.get("relation_report") or {}}
    if node == "run_content_review":
        return dict(view.get("content_review") or {})
    return {}


def _workflow_progress_with_timing(progress: dict[str, Any], previous: dict[str, Any]) -> dict[str, Any]:
    now = datetime.now(UTC)
    previous_nodes = {
        str(node.get("key")): dict(node)
        for node in previous.get("nodes", [])
        if isinstance(node, dict) and node.get("key")
    }
    timed_nodes: list[dict[str, Any]] = []
    for node in progress.get("nodes", []):
        if not isinstance(node, dict):
            continue
        key = str(node.get("key") or "")
        status = str(node.get("status") or "pending")
        previous_node = previous_nodes.get(key, {})
        started_at = previous_node.get("started_at")
        finished_at = previous_node.get("finished_at")
        if status == "running" and not started_at:
            started_at = now.isoformat()
            finished_at = None
        if status in {"succeeded", "failed", "failed_non_blocking", "skipped", "needs_review"}:
            if not started_at:
                started_at = previous_node.get("updated_at") or now.isoformat()
            if not finished_at:
                finished_at = now.isoformat()
        duration_seconds = _duration_seconds(started_at, finished_at if finished_at else now.isoformat()) if started_at else 0
        timed_nodes.append(
            {
                **node,
                "started_at": started_at,
                "finished_at": finished_at,
                "duration_seconds": duration_seconds,
            }
        )
    current_node = str(progress.get("current_node") or "")
    current = next((node for node in timed_nodes if node.get("key") == current_node), None)
    if current:
        progress["current_duration_seconds"] = current.get("duration_seconds", 0)
    progress["nodes"] = timed_nodes
    progress["elapsed_seconds"] = sum(int(node.get("duration_seconds") or 0) for node in timed_nodes if node.get("started_at"))
    return progress


def _duration_seconds(started_at: Any, ended_at: Any) -> int:
    try:
        started = datetime.fromisoformat(str(started_at).replace("Z", "+00:00"))
        ended = datetime.fromisoformat(str(ended_at).replace("Z", "+00:00"))
    except ValueError:
        return 0
    return max(0, int((ended - started).total_seconds()))


async def update_workflow_run_from_agent_run(session: AsyncSession, run: AgentRunRead) -> WorkflowRunRead | None:
    workflow_id = (run.input or {}).get("workflow_run_id")
    output = run.output or {}
    workflow_meta = output.get("workflow") if isinstance(output.get("workflow"), dict) else {}
    workflow_id = workflow_id or workflow_meta.get("workflow_run_id")
    if not workflow_id:
        return None
    try:
        workflow_uuid = UUID(str(workflow_id))
    except ValueError:
        return None

    workflow = await session.get(WorkflowRun, workflow_uuid)
    if workflow is None:
        if run.project_id is None:
            return None
        workflow = WorkflowRun(
            id=workflow_uuid,
            project_id=run.project_id,
            workflow_type=str(workflow_meta.get("name") or (run.input or {}).get("workflow") or "script_breakdown_workflow"),
            input_json=run.input or {},
        )
        session.add(workflow)

    workflow.agent_run_id = run.id
    workflow.workflow_type = str(workflow_meta.get("name") or workflow.workflow_type or "script_breakdown_workflow")
    workflow.input_json = run.input or workflow.input_json or {}
    workflow.node_status_json = output.get("node_status") if isinstance(output.get("node_status"), dict) else workflow.node_status_json or {}
    node_runtime = output.get("node_runtime") if isinstance(output.get("node_runtime"), dict) else {}
    agent_run_ids = await _sync_workflow_node_agent_runs(
        session,
        workflow,
        workflow.node_status_json or {},
        node_runtime=node_runtime,
        workflow_output=output,
    )
    previous_result = dict(workflow.result_json or {})
    breakdown_result = output.get("breakdown_view") if isinstance(output.get("breakdown_view"), dict) else {}
    workflow.result_json = {
        **previous_result,
        **breakdown_result,
        "agent_run_ids": agent_run_ids,
    }
    workflow.error_json = {
        "error_message": run.error_message,
        "errors": output.get("errors") if isinstance(output.get("errors"), list) else [],
        "warnings": output.get("warnings") if isinstance(output.get("warnings"), list) else [],
        "validation_report": output.get("validation_report") if isinstance(output.get("validation_report"), dict) else {},
        "node_runtime": node_runtime,
        "agent_run_ids": agent_run_ids,
    }
    run_row = await session.get(AgentRun, run.id)
    if run_row is not None and not run_row.payload_ref:
        run_row.output_json = {**output, "agent_run_ids": agent_run_ids}
    if run.status == AgentRunStatus.running:
        workflow.status = "running"
    elif run.status == AgentRunStatus.failed:
        workflow.status = "failed"
    else:
        workflow.status = str(workflow_meta.get("status") or "succeeded")
    feedback_candidate_sample_id = await _upsert_workflow_feedback_candidate(session, workflow, run, output)
    if feedback_candidate_sample_id:
        workflow.error_json = {
            **dict(workflow.error_json or {}),
            "feedback_candidate_sample_id": feedback_candidate_sample_id,
        }
    await _append_normalized_workflow_breakdown(session, run, output)
    _apply_workflow_payload_metadata(workflow)
    if run_row is not None and run_row.payload_ref:
        workflow.payload_ref = run_row.payload_ref
        workflow.payload_size_bytes = run_row.payload_size_bytes
        workflow.payload_sha256 = run_row.payload_sha256
        workflow.input_json = {**_lineage_summary(run.input), "payload_externalized": True}
        workflow.result_json = {
            "progress": dict(workflow.result_json.get("progress") or {}),
            "agent_run_ids": agent_run_ids,
            "payload_externalized": True,
        }
        workflow.error_json = {
            "errors_count": len(output.get("errors") or []),
            "warnings_count": len(output.get("warnings") or []),
            "feedback_candidate_sample_id": feedback_candidate_sample_id,
            "payload_externalized": True,
        }
    if workflow.status in {"succeeded", "failed", "needs_review", "materialized"}:
        await release_workflow_lock(workflow.lock_key or "", workflow.lock_token)
        workflow.lock_token = None
    workflow.updated_at = datetime.now(UTC)
    await session.commit()
    await session.refresh(workflow)
    await publish_project_event(str(workflow.project_id), "workflow_run")
    return workflow_run_to_read(workflow)


_NORMALIZED_WORKFLOW_VIEW_KEYS = frozenset({
    "schema_version",
    "normalization_version",
    "source_label",
    "project_id",
    "project_no",
    "project_prefix",
    "episode_code",
    "episode_id",
    "script_version_id",
    "reading_report",
    "reading_review_state",
    "reading_artifact_id",
    "reading_revision_context",
    "readthrough",
    "dialogue_script",
    "assets",
    "global_review_items",
    "asset_review_state",
    "asset_prompt_designs",
    "asset_prompt_processing_summary",
    "asset_prompt_finalization",
    "costume_designs",
    "costume_processing_summary",
    "script_segments",
    "storyboards",
    "storyboard_shots",
    "coverage_checks",
    "scene_packages",
    "cross_references",
    "delivery_checklist",
    "content_review",
    "notes",
    "llm_provider",
    "llm_model",
})

_RAW_WORKFLOW_PAYLOAD_KEYS = frozenset({
    "raw_output",
    "raw_model_response",
    "agent_raw_asset_output",
    "debug_output",
})

_BREAKDOWN_REVIEW_STATUSES = frozenset({"needs_review", "pending_confirmation", "confirmed"})
_BREAKDOWN_REVIEW_STATE_KEYS = frozenset({"status", "confirmed_revision_id", "confirmation_mode"})


def _clean_normalized_workflow_value(value: Any) -> Any:
    if isinstance(value, dict):
        return {
            key: _clean_normalized_workflow_value(item)
            for key, item in value.items()
            if key not in _RAW_WORKFLOW_PAYLOAD_KEYS
        }
    if isinstance(value, list):
        return [_clean_normalized_workflow_value(item) for item in value]
    return copy.deepcopy(value)


def _clean_normalized_workflow_view(view: dict[str, Any]) -> dict[str, Any]:
    return {
        key: _clean_normalized_workflow_value(value)
        for key, value in view.items()
        if key in _NORMALIZED_WORKFLOW_VIEW_KEYS
    }


def _normalized_breakdown_review_state(value: Any) -> dict[str, Any]:
    state = value if isinstance(value, dict) else {}
    status = str(state.get("status") or "")
    if status not in _BREAKDOWN_REVIEW_STATUSES:
        status = "needs_review"
    normalized: dict[str, Any] = {"status": status}
    if state.get("confirmed_revision_id") is not None:
        normalized["confirmed_revision_id"] = str(state["confirmed_revision_id"])
    elif "confirmed_revision_id" in state:
        normalized["confirmed_revision_id"] = None
    if state.get("confirmation_mode") is not None:
        normalized["confirmation_mode"] = str(state["confirmation_mode"])
    elif "confirmation_mode" in state:
        normalized["confirmation_mode"] = None
    return normalized


def _validate_breakdown_view_for_save(view: dict[str, Any]) -> None:
    unknown = sorted(set(view) - _NORMALIZED_WORKFLOW_VIEW_KEYS)
    if unknown:
        raise ValueError(f"拆解 view 包含未定义字段：{', '.join(unknown)}")
    for key in ("reading_review_state", "asset_review_state"):
        state = view.get(key)
        if not isinstance(state, dict):
            raise ValueError(f"拆解 view 缺少有效的 {key}")
        extra = sorted(set(state) - _BREAKDOWN_REVIEW_STATE_KEYS)
        if extra:
            raise ValueError(f"{key} 包含未定义字段：{', '.join(extra)}")
        if str(state.get("status") or "") not in _BREAKDOWN_REVIEW_STATUSES:
            raise ValueError(f"{key}.status 不是有效审核状态")


def _is_explicit_human_confirmation(review_state: dict[str, Any]) -> bool:
    return (
        str(review_state.get("status") or "") == "confirmed"
        and "confirmed_revision_id" in review_state
        and review_state.get("confirmed_revision_id") is None
        and str(review_state.get("confirmation_mode") or "") == "human_confirmed"
    )


def _pending_confirmation_state() -> dict[str, str]:
    return {"status": "pending_confirmation"}


async def _append_normalized_workflow_breakdown(
    session: AsyncSession,
    run: AgentRunRead,
    output: dict[str, Any],
) -> ScriptBreakdown | None:
    workflow_meta = output.get("workflow") if isinstance(output.get("workflow"), dict) else {}
    if (
        run.project_id is None
        or workflow_meta.get("name") != "script_breakdown_workflow"
        or workflow_meta.get("status") not in {"succeeded", "needs_review"}
    ):
        return None
    run_input = dict(run.input or {})
    episode_id = _uuid_or_none(run_input.get("episode_id"))
    script_version_id = _uuid_or_none(run_input.get("script_version_id"))
    step = str(run_input.get("single_step") or output.get("workflow_phase") or "").strip()
    if episode_id is None or script_version_id is None or run.workflow_run_id is None or not step:
        return None
    existing = await session.scalar(
        select(ScriptBreakdown)
        .join(AgentRun, AgentRun.id == ScriptBreakdown.agent_run_id)
        .where(
            ScriptBreakdown.project_id == run.project_id,
            ScriptBreakdown.data_state == "normalized",
            ScriptBreakdown.content_json["episode_id"].as_string() == str(episode_id),
            ScriptBreakdown.content_json["script_version_id"].as_string() == str(script_version_id),
            AgentRun.workflow_run_id == run.workflow_run_id,
            AgentRun.input_json["single_step"].as_string() == step,
        )
        .order_by(ScriptBreakdown.version.desc())
        .limit(1)
    )
    if existing is not None:
        return existing

    source_view = output.get("breakdown_view") if isinstance(output.get("breakdown_view"), dict) else {}
    rows = list((await session.scalars(
        select(ScriptBreakdown)
        .where(ScriptBreakdown.project_id == run.project_id)
        .order_by(ScriptBreakdown.version.desc())
    )).all())
    previous = next((
        _clean_normalized_workflow_view(dict(row.content_json or {}))
        for row in rows
        if _payload_matches_lineage_values(row.content_json, episode_id, script_version_id)
    ), {})
    content = {
        **previous,
        "normalization_version": "AssetNormalization.v1",
        "source_label": "agent_normalized",
        "project_id": str(run.project_id),
        "episode_code": str(run_input.get("episode_code") or previous.get("episode_code") or ""),
        "episode_id": str(episode_id),
        "script_version_id": str(script_version_id),
        "assets": list(previous.get("assets") or []),
        "global_review_items": list(previous.get("global_review_items") or []),
        "reading_review_state": _normalized_breakdown_review_state(previous.get("reading_review_state")),
        "asset_review_state": _normalized_breakdown_review_state(previous.get("asset_review_state")),
    }
    if step == "script_reading":
        content["reading_report"] = dict(output.get("reading_report") or source_view.get("reading_report") or {})
        content["reading_review_state"] = {"status": "needs_review"}
    elif step == "asset_extract":
        inventory = AssetNormalizationResultV1.model_validate({
            "normalization_version": output.get("normalization_version"),
            "assets": output.get("assets") or [],
            "global_review_items": output.get("global_review_items") or [],
        })
        content["assets"] = [asset.model_dump(mode="json") for asset in inventory.assets]
        content["global_review_items"] = [
            item.model_dump(mode="json") for item in inventory.global_review_items
        ]
        content["asset_review_state"] = {"status": "needs_review"}
    elif step in {"asset_prompt_generation", "asset_costume_design"}:
        content["asset_prompt_designs"] = list(output.get("asset_prompt_designs") or source_view.get("asset_prompt_designs") or [])
        content["asset_prompt_processing_summary"] = list(output.get("asset_prompt_processing_summary") or source_view.get("asset_prompt_processing_summary") or [])
    elif step == "script_segmentation":
        content["script_segments"] = list(output.get("script_segments") or source_view.get("script_segments") or [])
    elif step in {"storyboard_breakdown", "script_breakdown"}:
        for key in ("storyboards", "storyboard_shots", "coverage_checks", "scene_packages"):
            content[key] = list(source_view.get(key) or output.get(key) or [])
    else:
        return None
    content = _clean_normalized_workflow_view(content)
    version = (
        await session.scalar(
            select(func.max(ScriptBreakdown.version)).where(ScriptBreakdown.project_id == run.project_id)
        )
        or 0
    ) + 1
    breakdown = ScriptBreakdown(
        project_id=run.project_id,
        version=version,
        content_json=content,
        agent_run_id=run.id,
        edited_by_id=None,
        data_state="normalized",
    )
    session.add(breakdown)
    await session.flush()
    return breakdown


async def _upsert_workflow_feedback_candidate(
    session: AsyncSession,
    workflow: WorkflowRun,
    run: AgentRunRead,
    output: dict[str, Any],
) -> str | None:
    candidate = output.get("feedback_candidate") if isinstance(output.get("feedback_candidate"), dict) else None
    if not candidate:
        return None

    existing = await session.scalar(
        select(AgentTrainingSample)
        .where(
            AgentTrainingSample.sample_type == "workflow_feedback_candidate",
            AgentTrainingSample.entity_type == "workflow_run",
            AgentTrainingSample.entity_code == str(workflow.id),
        )
        .limit(1)
    )
    sample = existing or _build_workflow_feedback_training_sample(workflow, run, candidate)
    if existing is None:
        session.add(sample)
    else:
        sample.project_id = workflow.project_id
        sample.agent_type = run.agent_type
        sample.input_json = run.input or workflow.input_json or {}
        sample.agent_output_json = candidate
        sample.human_modified_output_json = {}
        sample.confirmed_output_json = {}
        sample.change_summary = ["工作流完成后生成反馈候选样本，等待人工确认或修正"]
        sample.quality_score = None
        sample.training_tags = _workflow_feedback_tags(workflow, candidate)
        sample.training_ready = False
        sample.created_by = workflow.created_by
        sample.source_run_id = run.id
        sample.lineage_key = _workflow_candidate_lineage_key(workflow, run)
        sample.payload_ref = workflow.payload_ref or run.payload_ref
        sample.payload_size_bytes = workflow.payload_size_bytes or run.payload_size_bytes
        sample.payload_sha256 = workflow.payload_sha256 or run.payload_sha256
        if sample.payload_ref:
            sample.input_json = _lineage_summary(run.input)
            sample.agent_output_json = _feedback_candidate_summary(candidate)

    await session.flush()
    run_row = await session.get(AgentRun, run.id)
    if run_row is not None:
        feedback_json = dict(run_row.feedback_json or {})
        feedback_json.update(
            {
                "workflow_feedback_candidate_sample_id": str(sample.id),
                "workflow_run_id": str(workflow.id),
                "workflow_feedback_candidate_at": datetime.now(UTC).isoformat(),
            }
        )
        run_row.feedback_json = feedback_json
        run_row.updated_at = datetime.now(UTC)
    return str(sample.id)


def _build_workflow_feedback_training_sample(
    workflow: WorkflowRun,
    run: AgentRunRead,
    candidate: dict[str, Any],
) -> AgentTrainingSample:
    externalized = bool(workflow.payload_ref or run.payload_ref)
    return AgentTrainingSample(
        project_id=workflow.project_id,
        agent_type=run.agent_type,
        sample_type="workflow_feedback_candidate",
        entity_type="workflow_run",
        entity_code=str(workflow.id),
        input_json=_lineage_summary(run.input) if externalized else run.input or workflow.input_json or {},
        agent_output_json=_feedback_candidate_summary(candidate) if externalized else candidate,
        human_modified_output_json={},
        confirmed_output_json={},
        change_summary=["工作流完成后生成反馈候选样本，等待人工确认或修正"],
        quality_score=None,
        training_tags=_workflow_feedback_tags(workflow, candidate),
        training_ready=False,
        created_by=workflow.created_by,
        source_run_id=run.id,
        payload_ref=workflow.payload_ref or run.payload_ref,
        payload_size_bytes=workflow.payload_size_bytes or run.payload_size_bytes,
        payload_sha256=workflow.payload_sha256 or run.payload_sha256,
        data_state="agent_raw",
        lineage_key=_workflow_candidate_lineage_key(workflow, run),
    )


def _workflow_candidate_lineage_key(workflow: WorkflowRun, run: AgentRunRead) -> str | None:
    """workflow_feedback_candidate 的 episode 作用域配对键：step 取本次 single_step。

    正式拆解恒为单步（single_step 必设），故每个候选对应一个 step，与人工终版按
    (集,版本,step,skill) 对齐。血缘取自 run.input，回退 workflow.input_json。
    """
    run_input = run.input if isinstance(run.input, dict) else {}
    source = run_input or (workflow.input_json if isinstance(workflow.input_json, dict) else {})
    step = str(source.get("single_step") or "").strip()
    if not step:
        return None
    return _lineage_key_from_run_input(source, step)


def _feedback_candidate_summary(candidate: dict[str, Any]) -> dict[str, Any]:
    summary, _, digest = _payload_metadata(candidate)
    return {
        **summary,
        "candidate_sha256": digest,
        "payload_externalized": True,
    }


def _workflow_feedback_tags(workflow: WorkflowRun, candidate: dict[str, Any]) -> list[str]:
    tags = ["feedback_candidate", "workflow", str(workflow.status or candidate.get("status") or "unknown")]
    primary_skill = str(candidate.get("primary_skill") or "").strip()
    if primary_skill:
        tags.append(primary_skill)
    return tags


async def list_workflow_runs(
    session: AsyncSession,
    project_id: UUID,
    user: User,
    *,
    include_payload: bool = True,
) -> list[WorkflowRunRead]:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    stmt = (
        select(WorkflowRun)
        .where(WorkflowRun.project_id == project_id)
        .order_by(WorkflowRun.created_at.desc())
    )
    if not include_payload:
        stmt = stmt.options(
            defer(WorkflowRun.node_status_json),
            defer(WorkflowRun.result_json),
            defer(WorkflowRun.error_json),
            defer(WorkflowRun.input_json),
        )
    result = await session.execute(stmt)
    return [workflow_run_to_read(item, include_payload=include_payload) for item in result.scalars().all()]


def _encode_page_cursor(created_at: datetime, item_id: UUID) -> str:
    raw = f"{created_at.isoformat()}|{item_id}"
    return base64.urlsafe_b64encode(raw.encode("utf-8")).decode("ascii").rstrip("=")


def _decode_page_cursor(cursor: str) -> tuple[datetime, UUID]:
    try:
        padded = cursor + "=" * (-len(cursor) % 4)
        raw = base64.urlsafe_b64decode(padded.encode("ascii")).decode("utf-8")
        timestamp, item_id = raw.rsplit("|", 1)
        return datetime.fromisoformat(timestamp), UUID(item_id)
    except (ValueError, TypeError, UnicodeError) as exc:
        raise ValueError("分页游标无效。") from exc


async def list_workflow_run_page(
    session: AsyncSession,
    project_id: UUID,
    user: User,
    *,
    limit: int,
    cursor: str | None = None,
) -> WorkflowRunPage:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    stmt = (
        select(WorkflowRun)
        .where(WorkflowRun.project_id == project_id)
        .order_by(WorkflowRun.created_at.desc(), WorkflowRun.id.desc())
        .options(
            defer(WorkflowRun.node_status_json),
            defer(WorkflowRun.result_json),
            defer(WorkflowRun.error_json),
            defer(WorkflowRun.input_json),
        )
    )
    if cursor:
        created_at, item_id = _decode_page_cursor(cursor)
        stmt = stmt.where(
            or_(
                WorkflowRun.created_at < created_at,
                and_(WorkflowRun.created_at == created_at, WorkflowRun.id < item_id),
            )
        )
    stmt = stmt.limit(limit + 1)
    result = await session.execute(stmt)
    rows = list(result.scalars().all())
    has_more = len(rows) > limit
    rows = rows[:limit]
    next_cursor = _encode_page_cursor(rows[-1].created_at, rows[-1].id) if has_more and rows else None
    return WorkflowRunPage(
        items=[workflow_run_to_summary(item) for item in rows],
        next_cursor=next_cursor,
        has_more=has_more,
        limit=limit,
    )


async def get_workflow_run(session: AsyncSession, workflow_run_id: UUID, user: User) -> WorkflowRunRead:
    workflow = await session.get(WorkflowRun, workflow_run_id)
    if workflow is None:
        raise KeyError(workflow_run_id)
    if not is_director_or_admin(user) and not await _can_access_project(session, workflow.project_id, user):
        raise PermissionError(workflow_run_id)
    return workflow_run_to_read(workflow)


async def mark_workflow_materialized(
    session: AsyncSession,
    workflow_run_id: UUID,
    result: AgentRunMaterializeResponse,
    user: User,
) -> WorkflowRunRead:
    workflow = await session.get(WorkflowRun, workflow_run_id)
    if workflow is None:
        raise KeyError(workflow_run_id)
    if not is_director_or_admin(user) and not await _can_access_project(session, workflow.project_id, user):
        raise PermissionError(workflow_run_id)
    result_json = dict(workflow.result_json or {})
    result_json["materialize"] = {
        "status": "succeeded",
        "materialized_at": datetime.now(UTC).isoformat(),
        "materialized_by": str(user.id),
        "response": result.model_dump(mode="json"),
    }
    workflow.result_json = result_json
    workflow.status = "materialized"
    _apply_workflow_payload_metadata(workflow)
    await release_workflow_lock(workflow.lock_key or "", workflow.lock_token)
    workflow.lock_token = None
    workflow.updated_at = datetime.now(UTC)
    await session.commit()
    await session.refresh(workflow)
    await publish_project_event(str(workflow.project_id), "workflow_run")
    return workflow_run_to_read(workflow)


async def latest_breakdown_view(
    session: AsyncSession,
    project_id: UUID,
    user: User,
    *,
    episode_id: UUID | None = None,
    script_version_id: UUID | None = None,
) -> dict[str, Any] | None:
    views = await recent_breakdown_views(
        session, project_id, user, episode_id=episode_id, script_version_id=script_version_id
    )
    if not views:
        return None
    current = await _overlay_authoritative_reading_revision(
        session,
        project_id,
        dict(views[0]),
        episode_id=episode_id,
        script_version_id=script_version_id,
    )
    return current


async def overlay_current_asset_lifecycle(
    session: AsyncSession,
    project_id: UUID,
    value: dict[str, Any] | list[dict[str, Any]],
) -> dict[str, Any] | list[dict[str, Any]]:
    """Overlay formal production lists; clean Breakdown views stay immutable."""
    if isinstance(value, dict):
        return dict(value)

    rows = list((await session.scalars(
        select(Asset).where(
            Asset.project_id == project_id,
            Asset.asset_type.in_(_VISUAL_ASSET_TYPES),
        )
    )).all())
    by_id = {str(row.id): row for row in rows}
    by_code = {str(row.asset_code or "").strip().upper(): row for row in rows if row.asset_code}

    def overlay_items(items: list[Any]) -> list[dict[str, Any]]:
        output: list[dict[str, Any]] = []
        for raw in items:
            if not isinstance(raw, dict):
                continue
            item = dict(raw)
            asset_id = str(item.get("asset_id") or item.get("id") or "").strip()
            asset_code = str(item.get("asset_code") or "").strip().upper()
            row = by_id.get(asset_id) or by_code.get(asset_code)
            if row is not None and row.status in {"deleted", "excluded"}:
                continue
            if row is None:
                output.append(item)
                continue
            row_metadata = {
                key: nested
                for key, nested in dict(row.metadata_json or {}).items()
                if key != "breakdown_asset"
            }
            metadata = {**dict(item.get("metadata") or {}), **row_metadata}
            status = _asset_confirmation_status(row)
            metadata["confirmation_status"] = status
            item.update({
                "asset_id": str(row.id),
                "asset_code": row.asset_code,
                "asset_type": row.asset_type.value if hasattr(row.asset_type, "value") else str(row.asset_type),
                "name": row.name,
                "description": row.description,
                "status": status,
                "metadata": metadata,
                "asset_revision_id": str(row.current_revision_id) if row.current_revision_id else item.get("asset_revision_id"),
            })
            if row.prompt_text:
                item["prompt"] = row.prompt_text
                item["prompt_text"] = row.prompt_text
            output.append(item)
        return output

    return overlay_items(value)


def _aggregate_asset_lifecycle_status(assets: list[dict[str, Any]]) -> str:
    statuses = {
        _asset_lifecycle_status(item)
        for item in assets
        if isinstance(item, dict)
        and not (
            str(item.get("asset_type") or item.get("type") or "").strip().lower() == "character"
            and _character_is_non_visual(item)
        )
    }
    if not statuses:
        return "confirmed"
    for status in ("needs_completion", "prompt_pending", "pending_confirmation"):
        if status in statuses:
            return status
    return "confirmed" if statuses == {"confirmed"} else "pending_confirmation"


async def confirmed_asset_inventory(
    session: AsyncSession,
    project_id: UUID,
    *,
    episode_id: UUID | None = None,
    script_version_id: UUID | None = None,
    strict_scope: bool = False,
) -> list[dict[str, Any]]:
    """Return only assets backed by a human-confirmed or applied source.

    With ``strict_scope`` enabled, an episode/version miss does not fall back to
    unrelated project-level locked assets.
    """
    confirmed_revisions = list((await session.scalars(
        select(ArtifactRevision)
        .where(
            ArtifactRevision.project_id == project_id,
            ArtifactRevision.artifact_type == "asset_inventory",
            ArtifactRevision.source_type == "human",
            ArtifactRevision.status == "confirmed",
        )
        .order_by(ArtifactRevision.created_at.desc(), ArtifactRevision.version_no.desc())
    )).all())
    for revision in confirmed_revisions:
        snapshot = revision.input_snapshot or {}
        if episode_id is not None and str(snapshot.get("episode_id") or "") != str(episode_id):
            continue
        if script_version_id is not None and str(snapshot.get("script_version_id") or "") != str(script_version_id):
            continue
        revision_content = revision.normalized_content or {}
        raw_items = revision_content.get("assets") or revision_content.get("items") or []
        inventory = [dict(item) for item in raw_items if isinstance(item, dict) and item.get("asset_code")]
        if inventory:
            inventory = await overlay_current_asset_lifecycle(session, project_id, inventory)
            if assets_not_confirmed(inventory):
                return []
            return [
                {
                    **item,
                    "asset_inventory_revision_id": str(revision.id),
                    "confirmation_source": "confirmed_asset_inventory",
                }
                for item in inventory
            ]

    binding_filters = [
        EpisodeAssetBinding.project_id == project_id,
        EpisodeAssetBinding.status == "active",
    ]
    if episode_id is not None:
        binding_filters.append(EpisodeAssetBinding.episode_id == episode_id)
    if script_version_id is not None:
        binding_filters.append(EpisodeAssetBinding.script_version_id == script_version_id)
    bindings = list((await session.scalars(select(EpisodeAssetBinding).where(*binding_filters))).all())
    if bindings:
        application_ids = {item.application_revision_id for item in bindings}
        applied_ids = set((await session.scalars(
            select(ArtifactRevision.id).where(
                ArtifactRevision.id.in_(application_ids),
                ArtifactRevision.artifact_type == "reading_asset_application",
                ArtifactRevision.status == "applied",
            )
        )).all())
        valid_bindings = [item for item in bindings if item.application_revision_id in applied_ids]
        asset_ids = {item.asset_id for item in valid_bindings}
        assets = {
            item.id: item
            for item in (await session.scalars(select(Asset).where(
                Asset.project_id == project_id,
                Asset.id.in_(asset_ids),
            ))).all()
        } if asset_ids else {}
        inventory = []
        for binding in valid_bindings:
            asset = assets.get(binding.asset_id)
            if asset is None or asset.status in {"deleted", "excluded"}:
                continue
            inventory.append({
                "asset_id": str(asset.id),
                "asset_code": asset.asset_code,
                "asset_type": asset.asset_type.value if hasattr(asset.asset_type, "value") else str(asset.asset_type),
                "name": asset.name,
                "description": asset.description,
                "prompt": asset.prompt_text,
                "status": _asset_confirmation_status(asset),
                "metadata": dict(asset.metadata_json or {}),
                "asset_revision_id": str(binding.asset_revision_id) if binding.asset_revision_id else None,
                "application_revision_id": str(binding.application_revision_id),
                "confirmation_source": "applied_reading_asset_application",
            })
        if inventory:
            return inventory if not assets_not_confirmed(inventory) else []

    if strict_scope and (episode_id is not None or script_version_id is not None):
        return []

    assets = list((await session.scalars(
        select(Asset).where(
            Asset.project_id == project_id,
            Asset.status.notin_(["deleted", "excluded"]),
        ).order_by(Asset.asset_code, Asset.name)
    )).all())
    inventory = []
    for asset in assets:
        revision = await session.get(ArtifactRevision, asset.current_revision_id) if asset.current_revision_id else None
        confirmed = _asset_confirmation_status(asset) == "confirmed"
        if not confirmed:
            continue
        inventory.append({
            "asset_id": str(asset.id),
            "asset_code": asset.asset_code,
            "asset_type": asset.asset_type.value if hasattr(asset.asset_type, "value") else str(asset.asset_type),
            "name": asset.name,
            "description": asset.description,
            "prompt": asset.prompt_text,
            "status": _asset_confirmation_status(asset),
            "metadata": dict(asset.metadata_json or {}),
            "asset_revision_id": str(revision.id) if revision else None,
            "confirmation_source": "confirmed_asset_revision" if revision else "locked_asset",
        })
    return inventory


async def recent_breakdown_views(
    session: AsyncSession,
    project_id: UUID,
    user: User,
    limit: int = 20,
    *,
    episode_id: UUID | None = None,
    script_version_id: UUID | None = None,
) -> list[dict[str, Any]]:
    """Return the one current normalized view for the requested script scope."""
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    breakdown_result = await session.execute(
        select(ScriptBreakdown)
        .where(ScriptBreakdown.project_id == project_id)
        .order_by(ScriptBreakdown.updated_at.desc())
        .limit(limit)
    )
    views: list[tuple[datetime, dict[str, Any]]] = list(
        (breakdown.updated_at, _breakdown_view_from_saved_content(breakdown.content_json or {}))
        for breakdown in breakdown_result.scalars().all()
        if breakdown.content_json and _payload_matches_lineage_values(
            breakdown.content_json,
            episode_id,
            script_version_id,
        )
    )
    if views:
        views.sort(key=lambda item: item[0] or datetime.min.replace(tzinfo=UTC), reverse=True)
        return [views[0][1]]
    return []


def _payload_matches_lineage_values(
    payload: Any,
    episode_id: UUID | None,
    script_version_id: UUID | None,
    *,
    allow_unscoped: bool = False,
) -> bool:
    if episode_id is None and script_version_id is None:
        return True
    if isinstance(payload, dict) and "input" in payload and "result" in payload:
        if not _payload_matches_lineage_values(
            payload.get("input"), episode_id, script_version_id, allow_unscoped=allow_unscoped
        ):
            return False
        return not _result_lineage_conflicts(payload.get("result"), episode_id, script_version_id)
    if isinstance(payload, dict):
        direct_episode = payload.get("episode_id")
        direct_version = payload.get("script_version_id")
        if direct_episode or direct_version:
            if episode_id is not None and str(direct_episode or "") != str(episode_id):
                return False
            if script_version_id is not None and str(direct_version or "") != str(script_version_id):
                return False
            return True
    found_episode = episode_id is None
    found_version = script_version_id is None
    has_lineage = False

    def scan(value: Any) -> None:
        nonlocal found_episode, found_version, has_lineage
        if isinstance(value, dict):
            for key, nested in value.items():
                if key in {"episode_id", "episode_code", "script_version_id"} and nested:
                    has_lineage = True
                if key == "episode_id" and episode_id and str(nested) == str(episode_id):
                    found_episode = True
                elif key == "script_version_id" and script_version_id and str(nested) == str(script_version_id):
                    found_version = True
                if isinstance(nested, (dict, list)):
                    scan(nested)
        elif isinstance(value, list):
            for nested in value:
                scan(nested)

    scan(payload)
    return (found_episode and found_version) or (allow_unscoped and not has_lineage)


def _result_lineage_conflicts(
    payload: Any,
    episode_id: UUID | None,
    script_version_id: UUID | None,
) -> bool:
    if not isinstance(payload, dict):
        return False
    direct_episode = payload.get("episode_id")
    direct_version = payload.get("script_version_id")
    if direct_episode and episode_id is not None and str(direct_episode) != str(episode_id):
        return True
    if direct_version and script_version_id is not None and str(direct_version) != str(script_version_id):
        return True
    for key in (
        "reading_revision_context", "breakdown_view", "raw_output", "reading_output",
        "reading_report", "confirmed_reading_report",
    ):
        nested = payload.get(key)
        if isinstance(nested, dict) and _result_lineage_conflicts(nested, episode_id, script_version_id):
            return True
    return False


async def list_agent_runs(
    session: AsyncSession,
    project_id: UUID | None = None,
    *,
    include_payload: bool = True,
) -> list[AgentRunRead]:
    stmt = select(AgentRun).order_by(AgentRun.created_at.desc())
    if project_id:
        stmt = stmt.where(AgentRun.project_id == project_id)
    if not include_payload:
        stmt = stmt.options(
            defer(AgentRun.input_json),
            defer(AgentRun.output_json),
            defer(AgentRun.token_usage),
            defer(AgentRun.feedback_json),
        )
    result = await session.execute(stmt)
    return [agent_run_to_read(item, include_payload=include_payload) for item in result.scalars().all()]


async def list_agent_run_page(
    session: AsyncSession,
    *,
    project_id: UUID | None,
    limit: int,
    cursor: str | None = None,
) -> AgentRunPage:
    stmt = (
        select(AgentRun)
        .order_by(AgentRun.created_at.desc(), AgentRun.id.desc())
        .options(
            defer(AgentRun.input_json),
            defer(AgentRun.output_json),
            defer(AgentRun.token_usage),
            defer(AgentRun.feedback_json),
        )
    )
    if project_id:
        stmt = stmt.where(AgentRun.project_id == project_id)
    if cursor:
        created_at, item_id = _decode_page_cursor(cursor)
        stmt = stmt.where(
            or_(
                AgentRun.created_at < created_at,
                and_(AgentRun.created_at == created_at, AgentRun.id < item_id),
            )
        )
    stmt = stmt.limit(limit + 1)
    result = await session.execute(stmt)
    rows = list(result.scalars().all())
    has_more = len(rows) > limit
    rows = rows[:limit]
    next_cursor = _encode_page_cursor(rows[-1].created_at, rows[-1].id) if has_more and rows else None
    return AgentRunPage(
        items=[agent_run_to_summary(item) for item in rows],
        next_cursor=next_cursor,
        has_more=has_more,
        limit=limit,
    )


async def get_pending_agent_run(
    session: AsyncSession,
    project_id: UUID,
    agent_type: AgentKind,
    *,
    episode_id: UUID | None = None,
    script_version_id: UUID | None = None,
) -> AgentRunRead | None:
    runs = list((await session.scalars(
        select(AgentRun)
        .where(
            AgentRun.project_id == project_id,
            AgentRun.agent_type == agent_type,
            AgentRun.parent_run_id.is_(None),
            AgentRun.status.in_([AgentRunStatus.queued, AgentRunStatus.running]),
        )
        .order_by(AgentRun.created_at.desc())
    )).all())
    run = next((item for item in runs if _payload_matches_lineage_values(
        item.input_json, episode_id, script_version_id
    )), None)
    return agent_run_to_read(run) if run else None


async def get_agent_run(session: AsyncSession, run_id: UUID) -> AgentRunRead:
    run = await session.get(AgentRun, run_id)
    if run is None:
        raise KeyError(run_id)
    return agent_run_to_read(run)


async def get_agent_run_for_user(session: AsyncSession, run_id: UUID, user: User) -> AgentRunRead:
    run = await session.get(AgentRun, run_id)
    if run is None:
        raise KeyError(run_id)
    if run.task_id is not None:
        visible_task = await session.scalar(_visible_task_stmt(user).where(Task.id == run.task_id).limit(1))
        if visible_task is None:
            raise PermissionError(run_id)
    elif run.project_id is not None:
        if not await _can_access_project(session, run.project_id, user):
            raise PermissionError(run_id)
    elif not is_director_or_admin(user):
        raise PermissionError(run_id)
    return agent_run_to_read(run)


async def user_for_agent_run_materialization(session: AsyncSession, run: AgentRunRead) -> User | None:
    workflow_id = (run.input or {}).get("workflow_run_id")
    if workflow_id:
        workflow_uuid = _uuid_or_none(workflow_id)
        workflow = await session.get(WorkflowRun, workflow_uuid) if workflow_uuid else None
        if workflow and workflow.created_by:
            user = await session.get(User, workflow.created_by)
            if user is not None:
                return user
    if run.project_id:
        project = await session.get(Project, run.project_id)
        if project is not None:
            for user_id in (project.manager_id, project.created_by_id):
                if user_id:
                    user = await session.get(User, user_id)
                    if user is not None:
                        return user
    return await session.scalar(select(User).where(User.role == UserRole.admin).order_by(User.created_at).limit(1))


async def finalize_asset_prompt_agent_run(
    session: AsyncSession,
    run: AgentRunRead,
    user: User,
) -> dict[str, Any]:
    """Persist one completed prompt batch and create all scoped asset tasks once."""
    if run.project_id is None:
        raise ValueError("资产提示词任务缺少项目范围。")
    run_input = dict(run.input or {})
    if run_input.get("single_step") != "asset_prompt_generation":
        raise ValueError("当前 AgentRun 不是正式资产提示词步骤。")
    episode_id = _uuid_or_none(run_input.get("episode_id"))
    script_version_id = _uuid_or_none(run_input.get("script_version_id"))
    if episode_id is None or script_version_id is None:
        raise ValueError("资产提示词任务缺少 episode_id 或 script_version_id。")
    output = dict(run.output or {})
    prompt_view = dict(output.get("breakdown_view") or {})
    prompt_assets = _asset_items_from_output(prompt_view or output)
    if not prompt_assets:
        raise ValueError("资产提示词步骤没有返回可分发的资产。")
    missing = assets_missing_prompt_design(prompt_assets, prompt_view or output)
    if missing:
        raise ValueError(f"以下资产提示词未完成：{'、'.join(missing)}。")

    normalized_revision, _created = await _append_agent_output_revision(
        session,
        run,
        artifact_type="asset_prompt_batch",
        normalized_content=prompt_view or output,
        status="succeeded",
    )
    current = await latest_breakdown_view(
        session,
        run.project_id,
        user,
        episode_id=episode_id,
        script_version_id=script_version_id,
    ) or {}
    # 合并当前视图 + 本批提示词产物。current 已是通过校验的合法归一化视图
    # （含 reading_review_state/asset_review_state），prompt_view 覆盖提示词相关字段。
    # 注意：BreakdownSaveRequest 只接受 view 字段（历史遗留 bug：曾用 raw_output，
    # 24b0ecd 重构改为 view 后此调用点未同步，raw_output/storyboards/assets 被 Pydantic
    # extra=ignore 静默丢弃 → view 恒空 → 校验必报"缺少 reading_review_state"）。
    # 这里显式构造 view，并用 _clean_normalized_workflow_view 过滤掉非法键
    # （characters/scenes/props/agent_run_id 等下游 content builder 不需要，只从
    # view.assets 派生）。资产 confirmed 态由 current.asset_review_state 决定（save 路径
    # 走 _sync_materialized_assets_from_breakdown 读该字段），不依赖 prompt_confirmation_required。
    merged_view = _clean_normalized_workflow_view({
        **current,
        **prompt_view,
        "assets": prompt_assets,
        "episode_id": str(episode_id),
        "script_version_id": str(script_version_id),
    })
    await save_breakdown_revision(
        session,
        run.project_id,
        BreakdownSaveRequest(
            episode_code=str(run_input.get("episode_code") or current.get("episode_code") or "EP01"),
            episode_id=episode_id,
            script_version_id=script_version_id,
            view=merged_view,
            change_summary=["资产提示词批次完成并自动进入任务分发"],
            source_label="agent_prompt_finalized",
            data_state="final",
        ),
        user,
    )
    await session.commit()
    try:
        task_distribution = await generate_asset_tasks(
            session,
            run.project_id,
            episode_id,
            script_version_id,
            user,
        )
    except Exception as exc:
        await session.rollback()
        return {
            "status": "distribution_failed",
            "prompt_status": "succeeded",
            "prompt_revision_id": str(normalized_revision.id),
            "error": str(exc),
        }
    return {
        "status": "succeeded",
        "prompt_status": "succeeded",
        "prompt_revision_id": str(normalized_revision.id),
        "task_distribution": task_distribution,
    }


async def retry_asset_prompt_task_distribution(
    session: AsyncSession,
    project_id: UUID,
    run_id: UUID,
    user: User,
) -> AgentRunRead:
    await ensure_project_write_access(session, project_id, user)
    run = await get_agent_run_for_user(session, run_id, user)
    if run.project_id != project_id:
        raise ValueError("AgentRun 不属于当前项目。")
    run_input = dict(run.input or {})
    if run_input.get("single_step") != "asset_prompt_generation":
        raise ValueError("当前 AgentRun 不是正式资产提示词步骤。")
    episode_id = _uuid_or_none(run_input.get("episode_id"))
    script_version_id = _uuid_or_none(run_input.get("script_version_id"))
    if episode_id is None or script_version_id is None:
        raise ValueError("资产提示词任务缺少 episode_id 或 script_version_id。")
    episode = await session.get(ProjectEpisode, episode_id)
    if episode is None or episode.project_id != project_id or episode.current_version_id != script_version_id:
        raise ValueError("只能为当前分集的最新剧本版本重试任务分发。")
    finalization = dict((run.output or {}).get("asset_prompt_finalization") or {})
    if finalization.get("prompt_status") != "succeeded" and finalization.get("status") != "succeeded":
        raise ValueError("资产提示词产物尚未完成，不能仅重试任务分发。")
    task_distribution = await generate_asset_tasks(
        session,
        project_id,
        episode_id,
        script_version_id,
        user,
    )
    output = dict(run.output or {})
    output["asset_prompt_finalization"] = {
        **finalization,
        "status": "succeeded",
        "prompt_status": "succeeded",
        "task_distribution": task_distribution,
        "error": None,
    }
    saved = await save_agent_run(session, run.model_copy(update={
        "status": AgentRunStatus.succeeded,
        "output": output,
        "error_message": None,
        "updated_at": datetime.now(UTC),
    }))
    await add_operation_log(
        session,
        action="asset_prompt_task_distribution_retried",
        target_type="agent_run",
        operator_id=user.id,
        project_id=project_id,
        target_id=run_id,
        detail={"episode_id": str(episode_id), "script_version_id": str(script_version_id)},
    )
    return saved


async def add_agent_feedback(
    session: AsyncSession,
    run_id: UUID,
    feedback: AgentFeedbackCreate,
    user: User,
) -> AgentFeedbackRead:
    run = await session.get(AgentRun, run_id)
    if run is None:
        raise KeyError(run_id)
    item = AgentFeedback(
        run_id=run_id,
        before_text=str(feedback.original_output or {}) if feedback.original_output is not None else None,
        after_text=str(feedback.edited_output or {}) if feedback.edited_output is not None else None,
        feedback_type="agent_feedback",
        created_by=user.id,
    )
    sample = AgentTrainingSample(
        project_id=run.project_id,
        agent_type=run.agent_type,
        sample_type="agent_feedback",
        entity_type="agent_run",
        entity_code=str(run_id),
        input_json=run.input_json or {},
        agent_output_json=feedback.original_output or run.output_json or {},
        human_modified_output_json=feedback.edited_output or {},
        confirmed_output_json=feedback.edited_output or {},
        change_summary=[feedback.note] if feedback.note else ["人工反馈已记录"],
        quality_score=feedback.rating,
        training_tags=["human_modified", "feedback"],
        training_ready=bool(feedback.edited_output),
        created_by=user.id,
    )
    session.add(item)
    session.add(sample)
    await session.flush()
    feedback_count = await session.scalar(select(func.count(AgentFeedback.id)).where(AgentFeedback.run_id == run_id)) or 0
    run.feedback_json = {
        "feedback_count": feedback_count,
        "latest_feedback_at": item.created_at.isoformat() if item.created_at else datetime.now(UTC).isoformat(),
        "latest_sample_id": str(sample.id),
    }
    run.updated_at = datetime.now(UTC)
    await session.commit()
    return AgentFeedbackRead(
        run_id=run_id,
        sample_id=str(sample.id),
        rating=feedback.rating,
        original_output=feedback.original_output,
        edited_output=feedback.edited_output,
        note=feedback.note,
        created_at=item.created_at,
    )


async def list_agent_feedback(session: AsyncSession, run_id: UUID) -> list[AgentFeedbackRead]:
    if await session.get(AgentRun, run_id) is None:
        raise KeyError(run_id)
    result = await session.execute(select(AgentFeedback).where(AgentFeedback.run_id == run_id).order_by(AgentFeedback.created_at.desc()))
    output: list[AgentFeedbackRead] = []
    for item in result.scalars().all():
        output.append(
            AgentFeedbackRead(
                run_id=run_id,
                sample_id=None,
                rating=None,
                original_output={"text": item.before_text} if item.before_text else None,
                edited_output={"text": item.after_text} if item.after_text else None,
                note=None,
                created_at=item.created_at,
            )
        )
    return output


async def list_training_samples(
    session: AsyncSession,
    *,
    project_id: UUID | None = None,
    agent_type: AgentKind | None = None,
) -> list[AgentTrainingSampleRead]:
    stmt = select(AgentTrainingSample).order_by(AgentTrainingSample.created_at.desc())
    if project_id:
        stmt = stmt.where(AgentTrainingSample.project_id == project_id)
    if agent_type:
        stmt = stmt.where(AgentTrainingSample.agent_type == agent_type)
    result = await session.execute(stmt)
    return [training_sample_to_read(item) for item in result.scalars().all()]


def training_sample_to_read(sample: AgentTrainingSample) -> AgentTrainingSampleRead:
    run_id = None
    if sample.entity_type == "agent_run" and sample.entity_code:
        try:
            run_id = UUID(sample.entity_code)
        except ValueError:
            run_id = None
    return AgentTrainingSampleRead(
        sample_id=str(sample.id),
        sample_type=sample.sample_type,
        schema_version="1.0",
        run_id=run_id,
        agent_type=sample.agent_type,
        input=sample.input_json or {},
        original_output=sample.agent_output_json or {},
        edited_output=sample.human_modified_output_json or {},
        confirmed_output=sample.confirmed_output_json or {},
        rating=sample.quality_score,
        note=None,
        entity_type=sample.entity_type,
        entity_code=sample.entity_code,
        change_summary=sample.change_summary or [],
        training_tags=sample.training_tags or [],
        training_ready=sample.training_ready,
        payload_ref=sample.payload_ref,
        payload_size_bytes=sample.payload_size_bytes,
        payload_sha256=sample.payload_sha256,
        data_state=sample.data_state,
        created_at=sample.created_at,
    )


async def get_current_breakdown(
    session: AsyncSession,
    project_id: UUID,
    user: User,
    *,
    episode_id: UUID | None = None,
    script_version_id: UUID | None = None,
) -> BreakdownRead | None:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    breakdowns = list((await session.scalars(
        select(ScriptBreakdown).where(ScriptBreakdown.project_id == project_id).order_by(ScriptBreakdown.version.desc())
    )).all())
    breakdown = next((item for item in breakdowns if _payload_matches_lineage_values(
        item.content_json, episode_id, script_version_id
    )), None)
    if breakdown is not None:
        content = breakdown.content_json or {}
        view = await _overlay_authoritative_reading_revision(
            session,
            project_id,
            _breakdown_view_from_saved_content(content),
            episode_id=episode_id,
            script_version_id=script_version_id,
        )
        return _breakdown_read_from_view(
            project_id, breakdown.version, view, breakdown.data_state, breakdown.agent_run_id, breakdown.updated_at
        )
    return None


async def _latest_scoped_breakdown_version(
    session: AsyncSession,
    project_id: UUID,
    *,
    episode_id: UUID | None,
    script_version_id: UUID | None,
) -> int | None:
    """Version of the most recent ScriptBreakdown row matching the given scope, or
    None if the scope has no saved breakdown yet. Mirrors recent_breakdown_views'
    lineage filter so the optimistic-lock token matches what the client last read."""
    rows = (await session.execute(
        select(ScriptBreakdown)
        .where(ScriptBreakdown.project_id == project_id)
        .order_by(ScriptBreakdown.updated_at.desc())
        .limit(20)
    )).scalars().all()
    for breakdown in rows:
        if breakdown.content_json and _payload_matches_lineage_values(
            breakdown.content_json, episode_id, script_version_id
        ):
            return breakdown.version
    return None


async def save_breakdown_revision(
    session: AsyncSession,
    project_id: UUID,
    data: BreakdownSaveRequest,
    user: User,
) -> BreakdownRead:
    project = await session.scalar(
        select(Project).where(Project.id == project_id).with_for_update()
    )
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    if not await _can_write_project(session, project_id, user):
        raise PermissionError(project_id)
    # Optimistic lock: reject the save if the client edited a stale scoped version.
    # The project row lock above serializes concurrent saves, so this check + the
    # new-version insert below are atomic. Only enforced when the client sends a token.
    if data.expected_version is not None:
        current_version = await _latest_scoped_breakdown_version(
            session, project_id, episode_id=data.episode_id, script_version_id=data.script_version_id
        )
        if current_version is not None and current_version != data.expected_version:
            raise BreakdownVersionConflict(data.expected_version, current_version)
    reserved_asset_codes = await _reserved_project_asset_codes(session, project)
    previous_view = await latest_breakdown_view(
        session,
        project_id,
        user,
        episode_id=data.episode_id,
        script_version_id=data.script_version_id,
    ) or {}
    content = _breakdown_content_from_request(
        project,
        data,
        user,
        reserved_asset_codes=reserved_asset_codes,
        previous_assets=_asset_items_from_output(previous_view),
        previous_output=previous_view,
    )
    await _append_reading_report_revisions(session, project, content, user)
    await _inherit_confirmed_asset_inventory(session, project, content)
    asset_revision = await _append_confirmed_asset_inventory_revision(session, project, content, user)
    if asset_revision is not None:
        await _materialize_assets_from_view(session, project, content, user)
        await _sync_materialized_assets_from_breakdown(session, project, content, user)
        await _sync_confirmed_inventory_bindings(session, project, content, asset_revision)
    version = (await session.scalar(select(func.max(ScriptBreakdown.version)).where(ScriptBreakdown.project_id == project_id)) or 0) + 1
    breakdown = ScriptBreakdown(
        project_id=project_id,
        version=version,
        content_json=content,
        agent_run_id=None,
        edited_by_id=user.id,
        data_state=data.data_state,
    )
    session.add(breakdown)
    project.status = ProjectStatus.breakdown_review
    project.current_stage = ProjectStatus.breakdown_review
    project.updated_at = datetime.now(UTC)
    await session.commit()
    await session.refresh(breakdown)
    return _breakdown_read_from_content(
        project_id, breakdown.version, content, breakdown.data_state, breakdown.agent_run_id, breakdown.updated_at
    )


async def get_asset_change_application(
    session: AsyncSession,
    project_id: UUID,
    confirmed_reading_revision_id: UUID,
    user: User,
) -> AssetChangeApplicationRead | None:
    if not await _can_write_project(session, project_id, user):
        raise PermissionError(project_id)
    application = await session.scalar(
        select(ArtifactRevision).where(
            ArtifactRevision.project_id == project_id,
            ArtifactRevision.artifact_type == "reading_asset_application",
            ArtifactRevision.artifact_id == confirmed_reading_revision_id,
        )
    )
    if application is None:
        return None
    return AssetChangeApplicationRead.model_validate(application.normalized_content)


async def apply_reading_asset_changes(
    session: AsyncSession,
    project_id: UUID,
    confirmed_reading_revision_id: UUID,
    user: User,
) -> AssetChangeApplicationRead:
    if not await _can_write_project(session, project_id, user):
        raise PermissionError(project_id)
    existing = await get_asset_change_application(session, project_id, confirmed_reading_revision_id, user)
    if existing is not None:
        reading_revision = await session.get(ArtifactRevision, confirmed_reading_revision_id)
        snapshot = dict(reading_revision.input_snapshot or {}) if reading_revision else {}
        episode_id = _uuid_or_none(snapshot.get("episode_id"))
        script_version_id = _uuid_or_none(snapshot.get("script_version_id"))
        proposals = list((reading_revision.normalized_content or {}).get("asset_change_proposals") or []) if reading_revision else []
        if episode_id and script_version_id:
            await session.execute(
                update(EpisodeAssetBinding)
                .where(
                    EpisodeAssetBinding.project_id == project_id,
                    EpisodeAssetBinding.episode_id == episode_id,
                    EpisodeAssetBinding.script_version_id != script_version_id,
                    EpisodeAssetBinding.status == "active",
                )
                .values(status="superseded", updated_at=datetime.now(UTC))
            )
            for index, (proposal, item) in enumerate(zip(proposals, existing.items)):
                result = item.model_dump(mode="json")
                if result.get("application_status") == "rejected":
                    continue
                await _upsert_episode_asset_binding(
                    session,
                    project_id=project_id,
                    episode_id=episode_id,
                    script_version_id=script_version_id,
                    confirmed_reading_revision_id=confirmed_reading_revision_id,
                    application_revision_id=existing.application_revision_id,
                    proposal_index=index,
                    proposal=dict(proposal),
                    result=result,
                )
            await session.commit()
        return existing

    reading_revision = await session.get(ArtifactRevision, confirmed_reading_revision_id)
    if (
        reading_revision is None
        or reading_revision.project_id != project_id
        or reading_revision.artifact_type != "reading_report"
        or reading_revision.source_type != "human"
        or reading_revision.status != "confirmed"
    ):
        raise KeyError(confirmed_reading_revision_id)
    report = dict(reading_revision.normalized_content or {})
    proposals = [dict(item) for item in report.get("asset_change_proposals") or [] if isinstance(item, dict)]
    unresolved = [item for item in proposals if str(item.get("decision") or "pending") not in {"accepted", "rejected"}]
    if unresolved:
        raise ValueError("仍有资产差异未接受或驳回，不能应用。")
    unresolved_conflicts = [
        item for item in proposals
        if item.get("decision") == "accepted"
        and item.get("action") == "conflict"
        and not isinstance(item.get("human_resolution"), dict)
    ]
    if unresolved_conflicts:
        raise ValueError("已接受的冲突项必须先填写人工解决方案。")

    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    source_snapshot = dict(reading_revision.input_snapshot or {})
    episode_id = _uuid_or_none(source_snapshot.get("episode_id"))
    script_version_id = _uuid_or_none(source_snapshot.get("script_version_id"))
    if episode_id is None or script_version_id is None:
        raise ValueError("围读确认版本缺少分集或剧本版本来源，不能应用资产差异。")
    candidate_maps = _reading_candidate_maps(report)
    items: list[dict[str, Any]] = []
    counts = {"created_assets": 0, "updated_assets": 0, "reused_assets": 0, "rejected_proposals": 0}
    application_revision_id = uuid.uuid4()

    for index, proposal in enumerate(proposals):
        result = await _apply_reading_asset_change_proposal(
            session,
            project,
            proposal,
            index,
            candidate_maps,
            reading_revision,
            source_snapshot,
            user,
        )
        items.append(result)
        status = result["application_status"]
        if status == "created":
            counts["created_assets"] += 1
        elif status == "updated":
            counts["updated_assets"] += 1
        elif status == "reused":
            counts["reused_assets"] += 1
        elif status == "rejected":
            counts["rejected_proposals"] += 1

    response = AssetChangeApplicationRead(
        project_id=project_id,
        confirmed_reading_revision_id=confirmed_reading_revision_id,
        application_revision_id=application_revision_id,
        status="applied",
        items=items,
        **counts,
    )
    application_content = response.model_dump(mode="json")
    application_revision = ArtifactRevision(
            id=application_revision_id,
            project_id=project_id,
            artifact_type="reading_asset_application",
            artifact_id=confirmed_reading_revision_id,
            version_no=1,
            source_type="system",
            input_snapshot={
                **source_snapshot,
                "confirmed_reading_revision_id": str(confirmed_reading_revision_id),
            },
            normalized_content=application_content,
            change_diff={"asset_change_results": items},
            content_hash=_content_hash(application_content),
            status="applied",
            created_by=user.id,
        )
    session.add(application_revision)
    await session.flush()
    await session.execute(
        update(EpisodeAssetBinding)
        .where(
            EpisodeAssetBinding.project_id == project_id,
            EpisodeAssetBinding.episode_id == episode_id,
            EpisodeAssetBinding.script_version_id != script_version_id,
            EpisodeAssetBinding.status == "active",
        )
        .values(status="superseded", updated_at=datetime.now(UTC))
    )
    for index, (proposal, result) in enumerate(zip(proposals, items, strict=True)):
        if result["application_status"] == "rejected":
            continue
        await _upsert_episode_asset_binding(
            session,
            project_id=project_id,
            episode_id=episode_id,
            script_version_id=script_version_id,
            confirmed_reading_revision_id=confirmed_reading_revision_id,
            application_revision_id=application_revision_id,
            proposal_index=index,
            proposal=proposal,
            result=result,
        )
    await add_operation_log(
        session,
        action="reading_asset_changes_applied",
        target_type="reading_report",
        operator_id=user.id,
        project_id=project_id,
        target_id=confirmed_reading_revision_id,
        detail=application_content,
        commit=False,
    )
    await session.commit()
    return response


async def _upsert_episode_asset_binding(
    session: AsyncSession,
    *,
    project_id: UUID,
    episode_id: UUID,
    script_version_id: UUID,
    confirmed_reading_revision_id: UUID,
    application_revision_id: UUID,
    proposal_index: int,
    proposal: dict[str, Any],
    result: dict[str, Any],
) -> EpisodeAssetBinding:
    asset_id = _uuid_or_none(result.get("asset_id"))
    if asset_id is None:
        raise ValueError(f"已接受的资产差异 {proposal_index + 1} 没有资产结果。")
    binding = await session.scalar(
        select(EpisodeAssetBinding).where(
            EpisodeAssetBinding.application_revision_id == application_revision_id,
            EpisodeAssetBinding.proposal_index == proposal_index,
        )
    )
    values = {
        "project_id": project_id,
        "episode_id": episode_id,
        "script_version_id": script_version_id,
        "confirmed_reading_revision_id": confirmed_reading_revision_id,
        "application_revision_id": application_revision_id,
        "asset_id": asset_id,
        "asset_revision_id": _uuid_or_none(result.get("asset_revision_id")),
        "proposal_index": proposal_index,
        "action": str(proposal.get("action") or "reuse"),
        "status": "active",
        "age_stage_code": str(proposal.get("age_stage_code") or "").strip() or None,
        "costume_variant_code": str(proposal.get("costume_variant_code") or "").strip() or None,
        "proposal_json": json.loads(json.dumps(proposal, ensure_ascii=False)),
    }
    if binding is None:
        binding = EpisodeAssetBinding(**values)
        session.add(binding)
    else:
        for key, value in values.items():
            setattr(binding, key, value)
        binding.updated_at = datetime.now(UTC)
    await session.flush()
    return binding


async def confirm_script_segments(
    session: AsyncSession,
    project_id: UUID,
    data: BreakdownSaveRequest,
    user: User,
) -> ScriptSegmentsConfirmResponse:
    if not is_director_or_admin(user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)

    previous_view = await latest_breakdown_view(
        session,
        project_id,
        user,
        episode_id=data.episode_id,
        script_version_id=data.script_version_id,
    ) or {}
    content = _breakdown_content_from_request(project, data, user, source_label="human_confirmed", previous_output=previous_view)
    await _require_asset_prompt_designs_from_content(session, content, user, project_id=project_id)
    episode_code = _normalize_episode_code(data.episode_code)
    if episode_code:
        content["episode_code"] = episode_code
        content["script_segments"] = [
            item for item in list(content.get("script_segments") or []) if _normalize_episode_code(item.get("episode_code")) == episode_code
        ]
    if not content.get("script_segments"):
        raise ValueError("当前没有脚本原文段，不能确认并进入分镜/资产拆分。请先运行脚本段拆分或人工新增脚本段。")

    final_revision = await _append_human_final_artifact_revision(
        session,
        project=project,
        artifact_type="script_segmentation",
        content=content,
        final_content={"script_segments": list(content.get("script_segments") or [])},
        user=user,
    )
    content["raw_output"] = {
        **dict(content.get("raw_output") or {}),
        "confirmed_script_segmentation_revision_id": str(final_revision.id),
    }

    existing_confirmed_version = await _latest_script_confirmed_breakdown_version(session, project_id, episode_code)
    if project.current_stage == ProjectStatus.asset_locking and existing_confirmed_version:
        return ScriptSegmentsConfirmResponse(
            project_id=project_id,
            episode_code=episode_code,
            script_segment_count=len(content.get("script_segments") or []),
            breakdown_version=existing_confirmed_version,
            next_stage=ProjectStatus.asset_locking.value,
        )

    confirmed_segments = await _upsert_confirmed_script_segments(session, project, content, user)
    version = (await session.scalar(select(func.max(ScriptBreakdown.version)).where(ScriptBreakdown.project_id == project_id)) or 0) + 1
    breakdown = ScriptBreakdown(
        project_id=project_id,
        version=version,
        content_json=content,
        agent_run_id=None,
        edited_by_id=user.id,
    )
    session.add(breakdown)
    entity_code = _breakdown_entity_code(project, episode_code, version)
    await create_entity_version(
        session,
        _build_breakdown_entity_version(
            project=project,
            entity_code=entity_code,
            content=content,
            status="script_confirmed",
            created_by=user.id,
        ),
        commit=False,
    )
    now = datetime.now(UTC)
    project.status = ProjectStatus.asset_locking
    project.current_stage = ProjectStatus.asset_locking
    project.updated_at = now
    await add_operation_log(
        session,
        action="script_segments_confirmed",
        target_type="script_segments",
        operator_id=user.id,
        project_id=project_id,
        target_id=None,
        detail={
            "episode_code": episode_code,
            "breakdown_version": version,
            "script_segment_count": confirmed_segments,
            "next_stage": ProjectStatus.asset_locking.value,
        },
        commit=False,
    )
    await session.commit()
    return ScriptSegmentsConfirmResponse(
        project_id=project_id,
        episode_code=episode_code,
        script_segment_count=confirmed_segments,
        breakdown_version=version,
        next_stage=ProjectStatus.asset_locking.value,
    )


async def lock_breakdown_episode(
    session: AsyncSession,
    project_id: UUID,
    data: BreakdownLockRequest,
    user: User,
) -> BreakdownLockResponse:
    if not is_director_or_admin(user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)

    previous_view = await latest_breakdown_view(
        session,
        project_id,
        user,
        episode_id=data.episode_id,
        script_version_id=data.script_version_id,
    ) or {}
    content = _breakdown_content_from_request(project, data, user, source_label="human_modified", previous_output=previous_view)
    await _require_asset_prompt_designs_from_content(session, content, user, project_id=project_id)
    episode_code = _normalize_episode_code(data.episode_code)
    if episode_code:
        content["episode_code"] = episode_code
        content["script_segments"] = [
            item for item in list(content.get("script_segments") or []) if _normalize_episode_code(item.get("episode_code")) == episode_code
        ]
        content["storyboards"] = [
            item for item in list(content.get("storyboards") or []) if _storyboard_episode_code(item) == episode_code
        ]
        content["assets"] = [
            item for item in list(content.get("assets") or []) if _asset_belongs_to_episode(item, episode_code)
        ]

    if not _breakdown_has_lockable_outputs(content):
        raise ValueError("当前只有脚本原文段，尚未生成分镜或资产清单，不能锁定本集并生成任务。请先完成分镜拆解或人工补充分镜/资产。")
    validation = validate_breakdown_view(content)
    if validation.blocking_errors:
        details = "；".join(item.message for item in validation.blocking_errors[:8])
        raise ValueError(f"分镜终版校验失败：{details}")
    final_revision = await _append_human_final_artifact_revision(
        session,
        project=project,
        artifact_type="storyboard_breakdown",
        content=content,
        final_content={
            "script_segments": list(content.get("script_segments") or []),
            "storyboards": list(content.get("storyboards") or []),
        },
        user=user,
        status="locked",
    )
    content["raw_output"] = {
        **dict(content.get("raw_output") or {}),
        "confirmed_storyboard_revision_id": str(final_revision.id),
    }

    created_storyboards = await _upsert_storyboards_from_breakdown(session, project, content, user)
    created_asset_tasks = 0
    if data.create_asset_tasks:
        created_asset_tasks = await _create_asset_text_to_image_tasks(session, project, content, user)

    version = (await session.scalar(select(func.max(ScriptBreakdown.version)).where(ScriptBreakdown.project_id == project_id)) or 0) + 1
    breakdown = ScriptBreakdown(
        project_id=project_id,
        version=version,
        content_json=content,
        agent_run_id=None,
        edited_by_id=user.id,
    )
    session.add(breakdown)
    entity_code = _breakdown_entity_code(project, episode_code, version)
    await create_entity_version(
        session,
        _build_breakdown_entity_version(
            project=project,
            entity_code=entity_code,
            content=content,
            status="locked",
            created_by=user.id,
        ),
        commit=False,
    )
    session.add(
        _build_breakdown_feedback_training_sample(
            project=project,
            entity_code=entity_code,
            content=content,
            user=user,
            sample_type="script_breakdown_locked",
            status_tag="locked",
            training_ready=True,
            confirmed=True,
            default_summary="人工修正版本已锁定",
        )
    )
    now = datetime.now(UTC)
    project.status = ProjectStatus.task_assignment
    project.current_stage = ProjectStatus.task_assignment
    project.locked_at = project.locked_at or now
    project.updated_at = now
    await add_operation_log(
        session,
        action="breakdown_episode_locked",
        target_type="script_breakdown",
        operator_id=user.id,
        project_id=project_id,
        target_id=None,
        detail={
            "episode_code": episode_code,
            "breakdown_version": version,
            "storyboards": created_storyboards,
            "asset_tasks": created_asset_tasks,
        },
        commit=False,
    )
    await session.commit()
    return BreakdownLockResponse(
        project_id=project_id,
        episode_code=episode_code,
        breakdown_version=version,
        training_sample_count=1,
        asset_count=len(content.get("assets") or []),
        created_asset_tasks=created_asset_tasks,
        task_count=await session.scalar(select(func.count(Task.id)).where(
            Task.project_id == project_id,
            Task.is_retired.is_(False),
        )) or 0,
    )


async def create_storyboard_video_tasks(
    session: AsyncSession,
    project_id: UUID,
    data: StoryboardVideoTaskCreate,
    user: User,
) -> StoryboardVideoTaskCreateResponse:
    if not await _can_write_project(session, project_id, user):
        raise PermissionError(project_id)
    project = await session.get(Project, project_id)
    if project is None or project.deleted_at is not None:
        raise KeyError(project_id)
    if data.episode_code:
        episode = await session.scalar(select(ProjectEpisode).where(
            ProjectEpisode.project_id == project_id,
            ProjectEpisode.episode_code == _normalize_episode_code(data.episode_code),
        ))
        if episode is not None and episode.current_version_id is not None:
            await _require_asset_prompt_designs_for_scope(
                session,
                project_id=project_id,
                episode_id=episode.id,
                script_version_id=episode.current_version_id,
                user=user,
            )
    breakdown = await session.scalar(
        select(ScriptBreakdown).where(ScriptBreakdown.project_id == project_id).order_by(ScriptBreakdown.version.desc()).limit(1)
    )
    content_storyboards = list((breakdown.content_json or {}).get("storyboards") or []) if breakdown else []
    episode_code = _normalize_episode_code(data.episode_code)
    scene_code = (data.scene_code or "").strip() or None
    matched_keys: set[tuple[int, int]] = set()
    for item in content_storyboards:
        if episode_code and _storyboard_episode_code(item) != episode_code:
            continue
        if scene_code and scene_code not in _storyboard_scene_codes(item):
            continue
        episode_num = _episode_num_from_code(_storyboard_episode_code(item))
        order_num = _safe_int(item.get("order_num") or item.get("order_no") or item.get("shot_no"), 0)
        if episode_num and order_num:
            matched_keys.add((episode_num, order_num))
    if scene_code and not matched_keys:
        return StoryboardVideoTaskCreateResponse(
            project_id=project_id,
            episode_code=episode_code,
            scene_code=scene_code,
            created_tasks=0,
            task_count=await session.scalar(select(func.count(Task.id)).where(
                Task.project_id == project_id,
                Task.is_retired.is_(False),
            )) or 0,
        )

    stmt = select(Storyboard).where(Storyboard.project_id == project_id).order_by(Storyboard.episode_num, Storyboard.order_num)
    if episode_code:
        stmt = stmt.where(Storyboard.episode_num == _episode_num_from_code(episode_code))
    if matched_keys:
        stmt = stmt.where(
            tuple_(Storyboard.episode_num, Storyboard.order_num).in_(matched_keys)
        )
    result = await session.execute(stmt)
    storyboards = result.scalars().all()
    assignee_id = data.assignee_id or await _first_user_id_by_role(session, UserRole.artist)
    created = 0
    now = datetime.now(UTC)
    for storyboard in storyboards:
        exists = await session.scalar(
            select(Task.id).where(
                Task.project_id == project_id,
                Task.storyboard_id == storyboard.id,
                Task.task_type == TaskType.image_to_video,
                Task.is_retired.is_(False),
            )
        )
        if exists:
            continue
        scene_label = data.scene_name or scene_code or f"EP{storyboard.episode_num:02d}"
        prompt = _storyboard_video_prompt(storyboard)
        task = Task(
            project_id=project_id,
            script_segment_id=storyboard.script_segment_id,
            storyboard_id=storyboard.id,
            task_type=TaskType.image_to_video,
            title=f"{scene_label} 分镜 {storyboard.order_num:03d} 图生视频",
            assignee_id=assignee_id,
            assigned_by=user.id,
            assigned_at=now,
            status=TaskStatus.todo,
            prompt_text=prompt,
            latest_prompt_text=prompt,
            due_at=now + timedelta(days=2),
            production_model="Seedance",
        )
        session.add(task)
        await session.flush()
        session.add(
            TaskPrompt(
                task_id=task.id,
                prompt_type="storyboard_video_initial",
                prompt_text=prompt,
                source="human_locked_breakdown",
                version_no=1,
                created_by=user.id,
            )
        )
        created += 1
    await session.commit()
    return StoryboardVideoTaskCreateResponse(
        project_id=project_id,
        episode_code=episode_code,
        scene_code=scene_code,
        created_tasks=created,
        task_count=await session.scalar(select(func.count(Task.id)).where(
            Task.project_id == project_id,
            Task.is_retired.is_(False),
        )) or 0,
    )


async def update_storyboard(session: AsyncSession, storyboard_id: UUID, data: StoryboardUpdate, user: User) -> StoryboardRead:
    storyboard = await session.get(Storyboard, storyboard_id)
    if storyboard is None:
        raise KeyError(storyboard_id)
    if not await _can_write_project(session, storyboard.project_id, user):
        raise PermissionError(storyboard_id)
    updates = data.model_dump(exclude_unset=True)
    for field, value in updates.items():
        if value is not None:
            setattr(storyboard, field, value)
    storyboard.updated_at = datetime.now(UTC)
    await session.commit()
    await session.refresh(storyboard)
    return storyboard_to_read(storyboard)


async def materialize_agent_run(
    session: AsyncSession,
    run: AgentRunRead,
    user: User,
) -> AgentRunMaterializeResponse:
    if not run.project_id:
        raise ValueError("Agent run has no project_id")
    if not await _can_write_project(session, run.project_id, user):
        raise PermissionError(run.project_id)
    if not run.output:
        raise ValueError("Agent run has no output")
    project = await session.get(Project, run.project_id)
    if project is None:
        raise KeyError(run.project_id)

    created_segments = 0
    created_versions = 0
    created_artifacts = 0
    created_samples = 0

    supported_types = {
        AgentKind.relation_check,
        AgentKind.content_compliance_review,
    }
    if run.agent_type not in supported_types:
        raise ValueError(f"Agent type does not support materialization: {run.agent_type.value}")

    if run.agent_type == AgentKind.relation_check:
        materialize_source = _relation_check_materialize_source(run.output)
        if not isinstance(materialize_source.get("relation_report"), dict):
            raise ValueError("关系校验输出缺少 relation_report")
        _revision, was_created = await _append_agent_output_revision(
            session,
            run,
            artifact_type="relation_report",
            normalized_content=materialize_source,
            status="needs_review",
        )
        created_artifacts += int(was_created)

    if run.agent_type == AgentKind.content_compliance_review:
        entity_code = f"REVIEW-{run.project_id}-{run.id.hex[:8]}"
        await create_entity_version(
            session,
            EntityVersionCreate(
                project_id=run.project_id,
                entity_type="agent_review",
                entity_id=run.id,
                entity_code=entity_code,
                source="agent",
                status="needs_review",
                content_json=run.output,
                change_summary="内容合理性审查结果",
                changed_fields=[
                    "asset_review",
                    "storyboard_review",
                    "dialogue_review",
                    "prompt_safety_review",
                ],
                agent_run_id=run.id,
                created_by=user.id,
            ),
            commit=False,
        )
        created_versions += 1

    if run.agent_type in supported_types:
        sample_type = f"{run.agent_type.value}_materialized"
        existing_sample = await session.scalar(
            select(AgentTrainingSample.id).where(
                AgentTrainingSample.project_id == run.project_id,
                AgentTrainingSample.sample_type == sample_type,
                AgentTrainingSample.entity_type == "agent_run",
                AgentTrainingSample.entity_code == str(run.id),
            )
        )
        if existing_sample is None:
            sample = AgentTrainingSample(
                project_id=run.project_id,
                agent_type=run.agent_type,
                sample_type=sample_type,
                entity_type="agent_run",
                entity_code=str(run.id),
                input_json=run.input or {},
                agent_output_json=run.output or {},
                human_modified_output_json={},
                confirmed_output_json={},
                change_summary=["Agent 输出已写入业务草案，等待人工修正或导演锁定"],
                training_tags=["agent_output", "materialized", "needs_human_review"],
                training_ready=False,
                created_by=user.id,
            )
            session.add(sample)
            created_samples += 1

    await session.commit()
    return AgentRunMaterializeResponse(
        run_id=run.id,
        project_id=run.project_id,
        agent_type=run.agent_type,
        created_script_segments=created_segments,
        created_entity_versions=created_versions,
        created_artifact_revisions=created_artifacts,
        created_training_samples=created_samples,
        message="Agent 输出已写入业务草案，等待人工修正或导演锁定。",
    )


def _agent_artifact_id(artifact_type: str, run: AgentRunRead) -> UUID:
    run_input = run.input if isinstance(run.input, dict) else {}
    scope = (
        run_input.get("script_version_id")
        or run_input.get("episode_id")
        or run_input.get("storyboard_id")
        or run.project_id
    )
    return uuid.uuid5(
        uuid.NAMESPACE_URL,
        f"cineforge:{artifact_type}:{run.project_id}:{scope}",
    )


async def _append_agent_output_revision(
    session: AsyncSession,
    run: AgentRunRead,
    *,
    artifact_type: str,
    normalized_content: dict[str, Any],
    status: str,
) -> tuple[ArtifactRevision, bool]:
    if run.project_id is None:
        raise ValueError("Agent run has no project_id")
    artifact_id = _agent_artifact_id(artifact_type, run)
    current = await session.scalar(
        select(ArtifactRevision)
        .where(
            ArtifactRevision.artifact_type == artifact_type,
            ArtifactRevision.artifact_id == artifact_id,
        )
        .order_by(ArtifactRevision.version_no.desc())
        .limit(1)
    )
    normalized_digest = _content_hash(normalized_content)
    normalized_revision = await session.scalar(
        select(ArtifactRevision)
        .where(
            ArtifactRevision.artifact_type == artifact_type,
            ArtifactRevision.artifact_id == artifact_id,
            ArtifactRevision.source_run_id == run.id,
            ArtifactRevision.source_type == "system",
            ArtifactRevision.data_state == "normalized",
            ArtifactRevision.content_hash == normalized_digest,
        )
        .order_by(ArtifactRevision.version_no.desc())
        .limit(1)
    )
    if normalized_revision is not None:
        return normalized_revision, False
    lineage = _agent_run_lineage(run)
    raw_payload = run.output or {}
    raw_digest = _content_hash(raw_payload)
    raw_revision = await session.scalar(
        select(ArtifactRevision)
        .where(
            ArtifactRevision.artifact_type == artifact_type,
            ArtifactRevision.artifact_id == artifact_id,
            ArtifactRevision.source_run_id == run.id,
            ArtifactRevision.source_type == "agent",
            ArtifactRevision.data_state == "agent_raw",
            ArtifactRevision.content_hash == raw_digest,
        )
        .order_by(ArtifactRevision.version_no.desc())
        .limit(1)
    )
    if raw_revision is None:
        raw_revision = ArtifactRevision(
            project_id=run.project_id,
            artifact_type=artifact_type,
            artifact_id=artifact_id,
            version_no=(current.version_no if current else 0) + 1,
            parent_revision_id=current.id if current else None,
            source_type="agent",
            source_run_id=run.id,
            skill_name=lineage["skill_name"],
            skill_version=lineage["skill_version"],
            contract_version=lineage["contract_version"],
            contract_hash=lineage["contract_hash"],
            model_name=lineage["model"],
            prompt_version=lineage["prompt_version"],
            input_hash=lineage["input_hash"],
            input_snapshot=run.input or {},
            raw_output=raw_payload,
            normalized_content={},
            change_diff={"previous_content_hash": current.content_hash if current else None},
            content_hash=raw_digest,
            status="agent_raw",
            data_state="agent_raw",
            payload_ref=run.payload_ref,
            payload_size_bytes=run.payload_size_bytes,
            payload_sha256=run.payload_sha256,
            idempotency_key=_artifact_revision_idempotency_key(
                artifact_type, artifact_id, run.id, "agent", "agent_raw", raw_digest
            ),
        )
        if run.payload_ref:
            raw_revision.raw_output = {
                "summary": dict(run.summary or {}),
                "payload_externalized": True,
            }
        raw_revision, _ = await _insert_artifact_revision_idempotently(session, raw_revision)

    next_version = max(current.version_no if current else 0, raw_revision.version_no) + 1
    normalized_revision = ArtifactRevision(
        project_id=run.project_id,
        artifact_type=artifact_type,
        artifact_id=artifact_id,
        version_no=next_version,
        parent_revision_id=raw_revision.id,
        source_type="system",
        source_run_id=run.id,
        skill_name=lineage["skill_name"],
        skill_version=lineage["skill_version"],
        contract_version=lineage["contract_version"],
        contract_hash=lineage["contract_hash"],
        model_name=lineage["model"],
        prompt_version=lineage["prompt_version"],
        input_hash=lineage["input_hash"],
        input_snapshot={**(run.input or {}), "agent_raw_revision_id": str(raw_revision.id)},
        raw_output={},
        normalized_content=normalized_content,
        change_diff={
            "previous_content_hash": raw_revision.content_hash,
            "source_transition": "agent_raw->normalized",
        },
        content_hash=normalized_digest,
        status=status,
        data_state="normalized",
        idempotency_key=_artifact_revision_idempotency_key(
            artifact_type, artifact_id, run.id, "system", "normalized", normalized_digest
        ),
    )
    normalized_revision, created = await _insert_artifact_revision_idempotently(session, normalized_revision)
    return normalized_revision, created


async def _append_human_final_artifact_revision(
    session: AsyncSession,
    *,
    project: Project,
    artifact_type: str,
    content: dict[str, Any],
    final_content: dict[str, Any],
    user: User,
    status: str = "confirmed",
) -> ArtifactRevision:
    raw_output = dict(content.get("raw_output") or {})
    scope = content.get("script_version_id") or content.get("episode_id") or project.id
    artifact_id = uuid.uuid5(uuid.NAMESPACE_URL, f"cineforge:{artifact_type}:{project.id}:{scope}")
    source_run_id = _uuid_or_none(raw_output.get("agent_run_id") or raw_output.get("run_id"))
    source_run = await session.get(AgentRun, source_run_id) if source_run_id else None
    if source_run is not None:
        source_read = agent_run_to_read(source_run)
        source_output = dict(source_read.output or {})
        normalized_source = dict(source_output.get("breakdown_view") or source_output)
        await _append_agent_output_revision(
            session,
            source_read,
            artifact_type=artifact_type,
            normalized_content=normalized_source,
            status="needs_review",
        )
    current = await session.scalar(
        select(ArtifactRevision)
        .where(
            ArtifactRevision.artifact_type == artifact_type,
            ArtifactRevision.artifact_id == artifact_id,
        )
        .order_by(ArtifactRevision.version_no.desc())
        .limit(1)
    )
    digest = _content_hash(final_content)
    final_conditions = [
        ArtifactRevision.artifact_type == artifact_type,
        ArtifactRevision.artifact_id == artifact_id,
        ArtifactRevision.source_type == "human",
        ArtifactRevision.data_state == "final",
        ArtifactRevision.content_hash == digest,
    ]
    if source_run is not None:
        final_conditions.append(ArtifactRevision.source_run_id == source_run.id)
    else:
        final_conditions.append(ArtifactRevision.source_run_id.is_(None))
    existing_final = await session.scalar(
        select(ArtifactRevision)
        .where(*final_conditions)
        .order_by(ArtifactRevision.version_no.desc())
        .limit(1)
    )
    if existing_final is not None:
        return existing_final
    revision = ArtifactRevision(
        project_id=project.id,
        artifact_type=artifact_type,
        artifact_id=artifact_id,
        version_no=(current.version_no if current else 0) + 1,
        parent_revision_id=current.id if current else None,
        source_type="human",
        source_run_id=source_run_id if source_run is not None else None,
        input_snapshot={
            "project_id": str(project.id),
            "episode_id": content.get("episode_id"),
            "script_version_id": content.get("script_version_id"),
            "normalized_revision_id": str(current.id) if current else None,
        },
        raw_output={},
        normalized_content=final_content,
        change_diff={
            "previous_content_hash": current.content_hash if current else None,
            "source_transition": f"{current.data_state if current else 'none'}->final",
        },
        content_hash=digest,
        status=status,
        created_by=user.id,
        data_state="final",
        idempotency_key=_artifact_revision_idempotency_key(
            artifact_type,
            artifact_id,
            source_run.id if source_run is not None else None,
            "human",
            "final",
            digest,
        ),
    )
    revision, _ = await _insert_artifact_revision_idempotently(session, revision)
    return revision


def _relation_check_materialize_source(output: dict[str, Any]) -> dict[str, Any]:
    breakdown_view = output.get("breakdown_view") if isinstance(output.get("breakdown_view"), dict) else {}
    candidates = (
        output.get("relation_check"),
        breakdown_view.get("relation_check"),
    )
    source = next((item for item in candidates if isinstance(item, dict) and item), None)
    if source is not None:
        return dict(source)
    if isinstance(output.get("relation_report"), dict):
        return dict(output)
    if isinstance(breakdown_view.get("relation_report"), dict):
        return dict(breakdown_view)
    return dict(output)


async def _task_dependency_state(
    session: AsyncSession,
    task: Task,
) -> tuple[bool, list[UUID], list[str]]:
    dependency_ids: list[UUID] = []
    if task.depends_on_task_id:
        dependency_ids.append(task.depends_on_task_id)
    if task.asset_id and task.task_type in {TaskType.asset, TaskType.text_to_image} and task.task_variant in {"B", "C", "D", "E"}:
        primary_task_id = await session.scalar(select(Task.id).where(
            Task.project_id == task.project_id,
            Task.asset_id == task.asset_id,
            Task.task_type.in_([TaskType.asset, TaskType.text_to_image]),
            Task.is_retired.is_(False),
            func.coalesce(Task.age_stage_code, "") == (task.age_stage_code or ""),
            func.coalesce(Task.costume_variant_code, "") == (task.costume_variant_code or ""),
            Task.task_variant == "A",
        ))
        if primary_task_id:
            dependency_ids.append(primary_task_id)
    dependency_ids.extend(list((await session.scalars(
        select(TaskDependency.depends_on_task_id).where(TaskDependency.task_id == task.id)
    )).all()))
    dependency_ids = list(dict.fromkeys(dependency_ids))
    if not dependency_ids:
        return False, [], []
    rows = (await session.execute(
        select(Task.id, Task.status, Asset.asset_code)
        .outerjoin(Asset, Asset.id == Task.asset_id)
        .where(Task.id.in_(dependency_ids), _active_asset_task_condition())
    )).all()
    status_by_id = {task_id: status for task_id, status, _ in rows}
    asset_codes = list(dict.fromkeys(
        str(asset_code) for _, _, asset_code in rows if asset_code
    ))
    active_dependency_ids = [task_id for task_id in dependency_ids if task_id in status_by_id]
    locked = any(status_by_id[task_id] != TaskStatus.completed for task_id in active_dependency_ids)
    return locked, active_dependency_ids, asset_codes


async def _character_task_requires_master_lineage(session: AsyncSession, task: Task) -> bool:
    """Return whether this task produces a B-E view under a character A master."""
    variant = str(task.task_variant or "").strip().upper()
    if (
        task.asset_id is None
        or task.task_type not in {TaskType.asset, TaskType.text_to_image}
        or variant in {"", "A", "MASTER"}
    ):
        return False
    asset_type = await session.scalar(select(Asset.asset_type).where(Asset.id == task.asset_id))
    return asset_type == AssetType.character


async def _current_character_master_submission(
    session: AsyncSession,
    task: Task,
) -> Submission | None:
    """Resolve the approved A submission for the task's exact role context."""
    if task.asset_id is None:
        return None
    return await session.scalar(
        select(Submission)
        .join(Task, Task.id == Submission.task_id)
        .join(Asset, Asset.id == Task.asset_id)
        .where(
            Task.project_id == task.project_id,
            Task.asset_id == task.asset_id,
            Task.task_type.in_([TaskType.asset, TaskType.text_to_image]),
            func.coalesce(Task.age_stage_code, "") == (task.age_stage_code or ""),
            func.coalesce(Task.costume_variant_code, "") == (task.costume_variant_code or ""),
            func.upper(func.coalesce(Task.task_variant, "MASTER")).in_(["A", "MASTER"]),
            Task.status == TaskStatus.completed,
            Task.is_retired.is_(False),
            Asset.asset_type == AssetType.character,
            Submission.status == "primary_master",
            Submission.is_selected.is_(True),
            Submission.is_primary.is_(True),
            Submission.is_archived.is_(False),
            Submission.is_invalidated.is_(False),
        )
        .order_by(Submission.created_at.desc(), Submission.id.desc())
        .limit(1)
    )


def _dependency_asset_contract_entry(
    *,
    asset: Asset,
    asset_code: str,
    asset_tasks: list[Task],
    all_submissions_by_task: dict[UUID, list[Submission]],
    formal_by_task: dict[UUID, list[Submission]],
    formal_versions_by_submission: dict[UUID, AssetVersion],
    files_by_id: dict[UUID, FileObject],
    required_for: str,
) -> DependencyAssetRead:
    """Build one dependency status so list and detail responses cannot drift."""
    formal = [submission for item in asset_tasks for submission in formal_by_task.get(item.id, [])]
    formal_pairs = [
        (submission, formal_versions_by_submission[submission.id])
        for submission in formal
        if submission.id in formal_versions_by_submission
    ]
    files: list[DependencyFileRead] = []
    for submission, version in formal_pairs:
        file_record = files_by_id.get(version.file_id)
        if file_record is None:
            continue
        files.append(DependencyFileRead(
            file_id=version.file_id,
            file_name=file_record.file_name,
            mime_type=file_record.mime_type,
            file_size=file_record.file_size,
            asset_version_id=version.id,
            download_name=_dependency_download_name(asset, submission, file_record, len(files) + 1),
            view_label=submission.view_label,
            submission_id=submission.id,
            step=submission.step,
        ))
    # An approved, non-invalidated Submission materialized as a valid
    # AssetVersion is the formal production source of truth. Historical or
    # cross-episode task rows without such a pair must not relock this asset.
    formal_task_ids = {submission.task_id for submission, _ in formal_pairs}
    completion_scope = [item for item in asset_tasks if item.id in formal_task_ids] if formal_pairs else asset_tasks
    completed = bool(completion_scope) and all(item.status == TaskStatus.completed for item in completion_scope)
    pending = any(
        submission.status in {"submitted", "reviewing"}
        for item in asset_tasks
        for submission in all_submissions_by_task.get(item.id, [])
    ) or any(item.status in {TaskStatus.submitted, TaskStatus.reviewing} for item in asset_tasks)
    rework = any(
        submission.status in {"rejected", "rework"}
        for item in asset_tasks
        for submission in all_submissions_by_task.get(item.id, [])
    ) or any(item.status == TaskStatus.rejected for item in asset_tasks)
    ready = completed and bool(files)
    reason = (
        "ready" if ready
        else "unassigned" if not asset_tasks
        else "rework_required" if rework
        else "pending_review" if pending
        else "production_incomplete" if not completed
        else "approved_file_missing"
    )
    return DependencyAssetRead(
        asset_id=asset.id,
        asset_code=asset_code,
        asset_name=asset.name,
        asset_type=asset.asset_type,
        required_for=required_for,
        production_status=(
            "completed" if completed
            else "rework" if rework
            else "in_progress" if asset_tasks
            else "unassigned"
        ),
        approval_status=(
            "approved" if ready
            else "rework" if rework
            else "pending_review" if pending
            else "approved" if files
            else "pending"
        ),
        ready=ready,
        reason=reason,
        files=files,
    )


def _dependency_download_name(
    asset: Asset,
    submission: Submission,
    file_record: FileObject,
    ordinal: int,
) -> str:
    original_name = str(file_record.file_name or "")
    suffix = f".{original_name.rsplit('.', 1)[1]}" if "." in original_name else ""
    label = str(submission.view_label or submission.state_label or "").strip()
    if not label:
        label = f"{ordinal:02d}"
    stem = "-".join(part for part in (str(asset.asset_code or "").strip(), asset.name.strip(), label) if part)
    stem = re.sub(r"[<>:\"/\\|?*\r\n]+", "_", stem).strip(" ._") or "asset"
    return f"{stem[:180]}{suffix}"


def _valid_formal_version_maps(
    submissions: list[Submission],
    versions: list[AssetVersion],
) -> tuple[dict[UUID, list[Submission]], dict[UUID, AssetVersion]]:
    """Match approved submissions to valid materialized asset versions."""
    version_by_submission: dict[UUID, AssetVersion] = {}
    for version in versions:
        if version.is_invalidated or version.purged_at is not None or version.source_submission_id is None or version.file_id is None:
            continue
        previous = version_by_submission.get(version.source_submission_id)
        if previous is None or version.version_no > previous.version_no:
            version_by_submission[version.source_submission_id] = version
    formal_by_task: dict[UUID, list[Submission]] = {}
    versions_by_submission: dict[UUID, AssetVersion] = {}
    for submission in submissions:
        if not (
            submission.status in APPROVED_SUBMISSION_STATUSES
            and submission.is_selected
            and not submission.is_archived
            and not submission.is_invalidated
            and submission.file_id is not None
        ):
            continue
        version = version_by_submission.get(submission.id)
        if version is None or version.file_id != submission.file_id:
            continue
        formal_by_task.setdefault(submission.task_id, []).append(submission)
        versions_by_submission[submission.id] = version
    return formal_by_task, versions_by_submission


async def _dependency_contract_for_task(
    session: AsyncSession,
    task: Task,
    *,
    dependency_ids: list[UUID] | None = None,
    dependency_codes: list[str] | None = None,
) -> tuple[DependencySummaryRead, list[DependencyAssetRead]]:
    """Return the maker-facing prerequisite contract for a task.

    This intentionally treats an asset as one unit (character views are not
    separate assets) and requires both completed production tasks and a
    selected, approved formal file.  The resolver is shared by task detail and
    list responses so the lock badge and its explanation cannot drift apart.
    """
    if dependency_ids is None or dependency_codes is None:
        _, dependency_ids, dependency_codes = await _task_dependency_state(session, task)
    codes = list(dict.fromkeys(str(code) for code in (dependency_codes or []) if code))
    if task.storyboard_id and (task.scene_code or task.scene_name):
        scene_dependencies = await compute_scene_asset_dependencies(session, task.project_id)
        for scene_code, info in scene_dependencies.items():
            if scene_code == task.scene_code or (task.scene_name and info.get("scene_name") == task.scene_name):
                codes.extend(str(code) for code in info.get("required_asset_codes") or [] if code)
                break
    codes = list(dict.fromkeys(codes))
    if not codes:
        return DependencySummaryRead(), []

    assets = list((await session.scalars(
        select(Asset).where(Asset.project_id == task.project_id, Asset.asset_code.in_(codes))
    )).all())
    assets_by_code = {str(asset.asset_code): asset for asset in assets if asset.asset_code}
    asset_ids = [asset.id for asset in assets]
    task_rows = list((await session.scalars(
        select(Task).where(
            Task.project_id == task.project_id,
            Task.asset_id.in_(asset_ids),
            _active_asset_task_condition(),
        )
    )).all()) if asset_ids else []
    tasks_by_asset: dict[UUID, list[Task]] = {}
    for item in task_rows:
        tasks_by_asset.setdefault(item.asset_id, []).append(item)
    task_ids = [item.id for item in task_rows]
    all_submissions_by_task: dict[UUID, list[Submission]] = {}
    submissions: list[Submission] = []
    if task_ids:
        submissions = list((await session.scalars(
            select(Submission).where(
                Submission.task_id.in_(task_ids),
                Submission.is_archived.is_(False),
                Submission.is_invalidated.is_(False),
            )
        )).all())
        for submission in submissions:
            all_submissions_by_task.setdefault(submission.task_id, []).append(submission)
    versions = list((await session.scalars(select(AssetVersion).where(
        AssetVersion.asset_id.in_(asset_ids),
        AssetVersion.is_invalidated.is_(False),
        AssetVersion.purged_at.is_(None),
    ))).all()) if asset_ids else []
    formal_by_task, formal_versions_by_submission = _valid_formal_version_maps(submissions, versions)
    file_ids = [version.file_id for version in formal_versions_by_submission.values() if version.file_id]
    files_by_id = {}
    if file_ids:
        files_by_id = {item.id: item for item in (await session.scalars(select(FileObject).where(FileObject.id.in_(file_ids)))).all()}

    output: list[DependencyAssetRead] = []
    for code in codes:
        asset = assets_by_code.get(code)
        if asset is None:
            output.append(DependencyAssetRead(asset_code=code, ready=False, reason="asset_not_found"))
            continue
        asset_tasks = tasks_by_asset.get(asset.id, [])
        # For an asset-variant task (for example character view B), expose the
        # direct prerequisite view A rather than counting the current task as a
        # prerequisite of itself. Storyboard tasks continue to aggregate all
        # active tasks for the referenced asset.
        if task.asset_id == asset.id and task.id not in (dependency_ids or []):
            direct_tasks = [item for item in asset_tasks if item.id in (dependency_ids or [])]
            if direct_tasks:
                asset_tasks = direct_tasks
        output.append(_dependency_asset_contract_entry(
            asset=asset,
            asset_code=code,
            asset_tasks=asset_tasks,
            all_submissions_by_task=all_submissions_by_task,
            formal_by_task=formal_by_task,
            formal_versions_by_submission=formal_versions_by_submission,
            files_by_id=files_by_id,
            required_for="storyboard" if task.storyboard_id else "task",
        ))
    ready_count = sum(1 for item in output if item.ready)
    return DependencySummaryRead(required=len(output), ready=ready_count, pending=len(output) - ready_count), output


async def _batch_dependency_contracts(
    session: AsyncSession,
    tasks: list[Task],
    dependency_states: dict[UUID, tuple[bool, list[UUID], list[str]]],
) -> dict[UUID, tuple[DependencySummaryRead, list[DependencyAssetRead]]]:
    """Resolve dependency contracts for a task list with bounded round trips."""
    if not tasks:
        return {}
    projects = {task.project_id for task in tasks}
    scene_maps = {project_id: await compute_scene_asset_dependencies(session, project_id) for project_id in projects}
    codes_by_task: dict[UUID, list[str]] = {}
    all_codes_by_project: dict[UUID, set[str]] = {}
    for task in tasks:
        _, _, dependency_codes = dependency_states.get(task.id, (False, [], []))
        codes = list(dict.fromkeys(str(code) for code in dependency_codes if code))
        if task.storyboard_id and (task.scene_code or task.scene_name):
            for scene_code, info in scene_maps.get(task.project_id, {}).items():
                if scene_code == task.scene_code or (task.scene_name and info.get("scene_name") == task.scene_name):
                    codes.extend(str(code) for code in info.get("required_asset_codes") or [] if code)
                    break
        codes = list(dict.fromkeys(codes))
        codes_by_task[task.id] = codes
        all_codes_by_project.setdefault(task.project_id, set()).update(codes)
    all_assets: list[Asset] = []
    for project_id, codes in all_codes_by_project.items():
        if codes:
            all_assets.extend((await session.scalars(select(Asset).where(Asset.project_id == project_id, Asset.asset_code.in_(codes)))).all())
    assets_by_project_code = {(asset.project_id, str(asset.asset_code)): asset for asset in all_assets if asset.asset_code}
    asset_ids = [asset.id for asset in all_assets]
    all_asset_tasks = list((await session.scalars(select(Task).where(Task.asset_id.in_(asset_ids), _active_asset_task_condition()))).all()) if asset_ids else []
    tasks_by_asset: dict[UUID, list[Task]] = {}
    for item in all_asset_tasks:
        tasks_by_asset.setdefault(item.asset_id, []).append(item)
    task_ids = [item.id for item in all_asset_tasks]
    all_submissions = list((await session.scalars(select(Submission).where(
        Submission.task_id.in_(task_ids),
        Submission.is_archived.is_(False),
        Submission.is_invalidated.is_(False),
    ))).all()) if task_ids else []
    all_submissions_by_task: dict[UUID, list[Submission]] = {}
    for submission in all_submissions:
        all_submissions_by_task.setdefault(submission.task_id, []).append(submission)
    all_versions = list((await session.scalars(select(AssetVersion).where(
        AssetVersion.asset_id.in_(asset_ids),
        AssetVersion.is_invalidated.is_(False),
        AssetVersion.purged_at.is_(None),
    ))).all()) if asset_ids else []
    formal_by_task, formal_versions_by_submission = _valid_formal_version_maps(all_submissions, all_versions)
    file_ids = [version.file_id for version in formal_versions_by_submission.values() if version.file_id]
    files_by_id = {item.id: item for item in (await session.scalars(select(FileObject).where(FileObject.id.in_(file_ids)))).all()} if file_ids else {}
    output: dict[UUID, tuple[DependencySummaryRead, list[DependencyAssetRead]]] = {}
    for task in tasks:
        _, dependency_ids, _ = dependency_states.get(task.id, (False, [], []))
        entries: list[DependencyAssetRead] = []
        for code in codes_by_task[task.id]:
            asset = assets_by_project_code.get((task.project_id, code))
            if asset is None:
                entries.append(DependencyAssetRead(asset_code=code, ready=False, reason="asset_not_found"))
                continue
            asset_tasks = tasks_by_asset.get(asset.id, [])
            if task.asset_id == asset.id and task.id not in dependency_ids:
                direct_tasks = [item for item in asset_tasks if item.id in dependency_ids]
                if direct_tasks:
                    asset_tasks = direct_tasks
            entries.append(_dependency_asset_contract_entry(
                asset=asset,
                asset_code=code,
                asset_tasks=asset_tasks,
                all_submissions_by_task=all_submissions_by_task,
                formal_by_task=formal_by_task,
                formal_versions_by_submission=formal_versions_by_submission,
                files_by_id=files_by_id,
                required_for="storyboard" if task.storyboard_id else "task",
            ))
        ready_count = sum(1 for item in entries if item.ready)
        output[task.id] = (DependencySummaryRead(required=len(entries), ready=ready_count, pending=len(entries) - ready_count), entries)
    return output


async def _batch_task_dependency_states(
    session: AsyncSession,
    tasks: list[Task],
) -> dict[UUID, tuple[bool, list[UUID], list[str]]]:
    """Resolve dependency state for a task list with bounded SQL round trips.

    The single-task helper is still used by task detail and mutation paths. The
    list endpoint needs the same semantics without running up to three queries
    per task, so all implicit primary tasks, explicit dependencies, and active
    dependency rows are loaded in batches here.
    """
    if not tasks:
        return {}

    task_ids = {task.id for task in tasks}
    dependency_ids_by_task: dict[UUID, list[UUID]] = {}
    for task in tasks:
        dependency_ids_by_task[task.id] = [
            task.depends_on_task_id
        ] if task.depends_on_task_id else []

    variant_tasks = [
        task for task in tasks
        if task.asset_id
        and task.task_type in {TaskType.asset, TaskType.text_to_image}
        and task.task_variant in {"B", "C", "D", "E"}
    ]
    if variant_tasks:
        asset_ids = {task.asset_id for task in variant_tasks if task.asset_id}
        primary_rows = (await session.execute(
            select(
                Task.id,
                Task.project_id,
                Task.asset_id,
                Task.age_stage_code,
                Task.costume_variant_code,
            )
            .where(
                Task.project_id.in_({task.project_id for task in variant_tasks}),
                Task.asset_id.in_(asset_ids),
                Task.task_type.in_([TaskType.asset, TaskType.text_to_image]),
                Task.task_variant == "A",
            )
        )).all()
        primary_by_key = {
            (
                project_id,
                asset_id,
                str(age_stage_code or ""),
                str(costume_variant_code or ""),
            ): primary_id
            for primary_id, project_id, asset_id, age_stage_code, costume_variant_code in primary_rows
        }
        for task in variant_tasks:
            primary_id = primary_by_key.get((
                task.project_id,
                task.asset_id,
                str(task.age_stage_code or ""),
                str(task.costume_variant_code or ""),
            ))
            if primary_id:
                dependency_ids_by_task[task.id].append(primary_id)

    explicit_rows = (await session.execute(
        select(TaskDependency.task_id, TaskDependency.depends_on_task_id)
        .where(TaskDependency.task_id.in_(task_ids))
    )).all()
    for task_id, depends_on_task_id in explicit_rows:
        dependency_ids_by_task[task_id].append(depends_on_task_id)

    ordered_dependency_ids = {
        task_id: list(dict.fromkeys(ids))
        for task_id, ids in dependency_ids_by_task.items()
    }
    all_dependency_ids = {
        dependency_id
        for ids in ordered_dependency_ids.values()
        for dependency_id in ids
    }
    if not all_dependency_ids:
        return {task.id: (False, [], []) for task in tasks}

    dependency_rows = (await session.execute(
        select(Task.id, Task.status, Asset.asset_code)
        .outerjoin(Asset, Asset.id == Task.asset_id)
        .where(Task.id.in_(all_dependency_ids), _active_asset_task_condition())
    )).all()
    status_by_id = {task_id: status for task_id, status, _ in dependency_rows}
    asset_code_by_id = {
        task_id: str(asset_code)
        for task_id, _, asset_code in dependency_rows
        if asset_code
    }
    output: dict[UUID, tuple[bool, list[UUID], list[str]]] = {}
    for task in tasks:
        dependency_ids = [
            dependency_id
            for dependency_id in ordered_dependency_ids[task.id]
            if dependency_id in status_by_id
        ]
        asset_codes = list(dict.fromkeys(
            asset_code_by_id[dependency_id]
            for dependency_id in dependency_ids
            if dependency_id in asset_code_by_id
        ))
        output[task.id] = (
            any(status_by_id[dependency_id] != TaskStatus.completed for dependency_id in dependency_ids),
            dependency_ids,
            asset_codes,
        )
    return output


async def _batch_scene_lock_states(
    session: AsyncSession,
    tasks: list[Task],
) -> dict[UUID, bool]:
    """Compute scene locks once per project instead of once per storyboard task."""
    scene_tasks_by_project: dict[UUID, list[Task]] = {}
    for task in tasks:
        if (
            task.storyboard_id
            and task.task_type in {TaskType.storyboard_shot, TaskType.text_to_image, TaskType.image_to_video}
            and (task.scene_code or task.scene_name)
        ):
            scene_tasks_by_project.setdefault(task.project_id, []).append(task)

    output: dict[UUID, bool] = {}
    for project_id, project_tasks in scene_tasks_by_project.items():
        entries = await _scene_gating_core(session, project_id)
        for task in project_tasks:
            output[task.id] = next(
                (
                    bool(entry["locked"])
                    for entry in entries
                    if entry["scene_code"] == task.scene_code
                    or (task.scene_name and entry["scene_name"] == task.scene_name)
                ),
                False,
            )
    return output


async def production_references_for_task(session: AsyncSession, task_id: UUID) -> list[dict[str, Any]]:
    task = await _get_active_asset_task(session, task_id)
    if task is None:
        raise KeyError(task_id)
    _, dependency_ids, _ = await _task_dependency_state(session, task)
    reference_task_ids = set(dependency_ids)
    sibling_a_task_id: UUID | None = None
    if task.task_type == TaskType.asset and task.asset_id and task.task_variant in {"B", "C", "D", "E"}:
        sibling_a_task_id = await session.scalar(select(Task.id).where(
            Task.project_id == task.project_id,
            Task.asset_id == task.asset_id,
            Task.task_type == TaskType.asset,
            Task.is_retired.is_(False),
            func.coalesce(Task.age_stage_code, "") == (task.age_stage_code or ""),
            func.coalesce(Task.costume_variant_code, "") == (task.costume_variant_code or ""),
            Task.task_variant == "A",
        ))
        if sibling_a_task_id:
            reference_task_ids.add(sibling_a_task_id)
    if task.storyboard_id and (task.scene_code or task.scene_name):
        scene_dependencies = await compute_scene_asset_dependencies(session, task.project_id)
        required_codes: set[str] = set()
        for scene_code, info in scene_dependencies.items():
            if scene_code == task.scene_code or (task.scene_name and info.get("scene_name") == task.scene_name):
                required_codes.update(info.get("required_asset_codes") or [])
        if required_codes:
            reference_task_ids.update(list((await session.scalars(
                select(Task.id)
                .join(Asset, Asset.id == Task.asset_id)
                .where(
                    Task.project_id == task.project_id,
                    Task.task_type.in_([TaskType.asset, TaskType.text_to_image]),
                    Task.status == TaskStatus.completed,
                    Task.is_retired.is_(False),
                    Asset.asset_code.in_(required_codes),
                )
            )).all()))
    if not reference_task_ids:
        return []
    rows = (await session.execute(
        select(Submission, Task, Asset.asset_code, AssetVersion)
        .join(Task, Task.id == Submission.task_id)
        .outerjoin(Asset, Asset.id == Task.asset_id)
        .join(AssetVersion, AssetVersion.source_submission_id == Submission.id)
        .where(
            Submission.task_id.in_(reference_task_ids),
            Task.is_retired.is_(False),
            Submission.status.in_(APPROVED_SUBMISSION_STATUSES),
            Submission.is_selected.is_(True),
            Submission.is_archived.is_(False),
            Submission.is_invalidated.is_(False),
            Submission.file_id.is_not(None),
            AssetVersion.is_invalidated.is_(False),
            AssetVersion.purged_at.is_(None),
            AssetVersion.asset_id == Task.asset_id,
            AssetVersion.file_id == Submission.file_id,
        )
        .order_by(Asset.asset_code, Task.age_stage_code, Task.costume_variant_code, Task.task_variant, AssetVersion.version_no)
    )).all()
    output_by_submission: dict[UUID, dict[str, Any]] = {}
    for submission, source_task, asset_code, version in rows:
        output_by_submission[submission.id] = {
            "task_id": str(source_task.id),
            "asset_code": asset_code,
            "age_stage_code": source_task.age_stage_code,
            "costume_variant_code": source_task.costume_variant_code,
            "task_variant": source_task.task_variant,
            "step": submission.step,
            "submission_id": str(submission.id),
            "asset_version_id": str(version.id),
            "file_id": str(version.file_id) if version.file_id else None,
            "file_path": submission.file_path,
        }
    return list(output_by_submission.values())


def build_task_prompt_context_fingerprint(
    *,
    task_id: UUID,
    step: str | None,
    production_references: list[dict[str, Any]],
    primary_submissions: list[dict[str, Any]],
    asset_snapshot: dict[str, Any] | None,
    resolved_production_brief: dict[str, Any] | None,
    prompt_revision_id: UUID | None,
    latest_prompt_text: str | None,
) -> str:
    references = sorted(
        (dict(item) for item in production_references),
        key=lambda item: (
            str(item.get("task_id") or ""),
            str(item.get("step") or ""),
            str(item.get("submission_id") or ""),
        ),
    )
    masters = sorted(
        (dict(item) for item in primary_submissions),
        key=lambda item: (str(item.get("step") or ""), str(item.get("submission_id") or "")),
    )
    return _content_hash({
        "task_id": str(task_id),
        "step": str(step or "").strip().lower() or None,
        "production_references": references,
        "primary_submissions": masters,
        "asset_snapshot": asset_snapshot or {},
        "resolved_production_brief": resolved_production_brief or {},
        "prompt_revision_id": str(prompt_revision_id) if prompt_revision_id else None,
        "latest_prompt_text": latest_prompt_text,
    })


def task_production_context(asset: dict[str, Any], task: TaskRead) -> dict[str, Any]:
    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}
    breakdown_asset = metadata.get("breakdown_asset") if isinstance(metadata.get("breakdown_asset"), dict) else {}
    breakdown_metadata = breakdown_asset.get("metadata") if isinstance(breakdown_asset.get("metadata"), dict) else {}
    contexts = metadata.get("production_contexts") or breakdown_metadata.get("production_contexts") or {}
    if not isinstance(contexts, dict):
        return {}
    asset_code = str(asset.get("asset_code") or "").strip()
    context_key = "/".join(filter(None, (asset_code, task.age_stage_code, task.costume_variant_code)))
    candidate = contexts.get(context_key)
    if not isinstance(candidate, dict) and len(contexts) == 1:
        candidate = next(iter(contexts.values()))
    if not isinstance(candidate, dict):
        return {}
    snapshot = candidate.get("input_snapshot") if isinstance(candidate.get("input_snapshot"), dict) else candidate
    context = dict(snapshot)
    view_code = str(task.task_variant or "").strip().upper()
    if asset.get("asset_type") == "character" and view_code in {"A", "B", "C", "D", "E"}:
        return scope_character_production_context(context, view_code)
    return context


async def task_prompt_context_fingerprint(
    session: AsyncSession,
    task_id: UUID,
    step: str | None,
    *,
    production_references: list[dict[str, Any]] | None = None,
) -> str:
    task = await _get_active_asset_task(session, task_id)
    if task is None:
        raise KeyError(task_id)
    references = (
        production_references
        if production_references is not None
        else await production_references_for_task(session, task_id)
    )
    primary_rows = (await session.execute(
        select(Submission.id, Submission.step)
        .where(
            Submission.task_id == task_id,
            Submission.status == "primary_master",
            Submission.is_primary.is_(True),
            Submission.is_archived.is_(False),
            Submission.is_invalidated.is_(False),
        )
        .order_by(Submission.step, Submission.id)
    )).all()
    primary_submissions = [
        {"submission_id": str(submission_id), "step": submission_step}
        for submission_id, submission_step in primary_rows
    ]
    asset = await session.get(Asset, task.asset_id) if task.asset_id else None
    asset_snapshot = None
    if asset is not None:
        asset_snapshot = {
            "id": str(asset.id),
            "version": asset.version,
            "current_revision_id": str(asset.current_revision_id) if asset.current_revision_id else None,
            "metadata": asset.metadata_json or {},
        }
    project = await session.get(Project, task.project_id)
    resolved_production_brief = None
    if project is not None and task.episode_id and project.production_brief:
        episode = await session.get(ProjectEpisode, task.episode_id)
        if episode is not None and episode.production_brief:
            try:
                resolved_production_brief = build_resolved_production_brief(
                    project_title=project.title,
                    project_prefix=project.project_prefix or "RF",
                    production_brief=project.production_brief,
                    script_context={
                        "episode_no": episode.episode_no,
                        "episode_code": episode.episode_code,
                        "episode_production_brief": episode.production_brief,
                    },
                )
            except (TypeError, ValueError):
                # Task fingerprinting remains readable while an incomplete
                # project setup is being repaired; Agent execution itself
                # still rejects a missing ResolvedProductionBrief.
                resolved_production_brief = None
    return build_task_prompt_context_fingerprint(
        task_id=task.id,
        step=step,
        production_references=references,
        primary_submissions=primary_submissions,
        asset_snapshot=asset_snapshot,
        resolved_production_brief=resolved_production_brief,
        prompt_revision_id=task.prompt_revision_id,
        latest_prompt_text=task.latest_prompt_text,
    )


async def _storyboards_by_id_for_tasks(session: AsyncSession, tasks: list[Task]) -> dict[UUID, Storyboard]:
    storyboard_ids = {task.storyboard_id for task in tasks if task.storyboard_id}
    if not storyboard_ids:
        return {}
    rows = await session.scalars(select(Storyboard).where(Storyboard.id.in_(storyboard_ids)))
    return {storyboard.id: storyboard for storyboard in rows.all()}


async def _episode_codes_by_id_for_tasks(session: AsyncSession, tasks: list[Task]) -> dict[UUID, str]:
    episode_ids = {task.episode_id for task in tasks if task.episode_id}
    if not episode_ids:
        return {}
    rows = await session.execute(
        select(ProjectEpisode.id, ProjectEpisode.episode_code).where(ProjectEpisode.id.in_(episode_ids))
    )
    return {episode_id: episode_code for episode_id, episode_code in rows.all()}


async def list_tasks(
    session: AsyncSession,
    *,
    user: User,
    project_id: UUID | None = None,
    status: TaskStatus | None = None,
    assignee_id: UUID | None = None,
) -> list[TaskRead]:
    stmt = _visible_task_stmt(user).options(
        selectinload(Task.submissions), selectinload(Task.submission_batches)
    ).order_by(Task.status, Task.due_at)
    if project_id:
        stmt = stmt.where(Task.project_id == project_id)
    if status:
        stmt = stmt.where(Task.status == status)
    if assignee_id:
        stmt = stmt.where(Task.assignee_id == assignee_id)
    result = await session.execute(stmt)
    tasks = result.scalars().all()
    user_ids = {task.assignee_id for task in tasks if task.assignee_id}
    users = {}
    if user_ids:
        user_result = await session.execute(select(User).where(User.id.in_(user_ids)))
        users = {item.id: item for item in user_result.scalars().all()}
    dependency_states = await _batch_task_dependency_states(session, list(tasks))
    dependency_contracts = await _batch_dependency_contracts(session, list(tasks), dependency_states)
    scene_lock_states = await _batch_scene_lock_states(session, list(tasks))
    storyboards_by_id = await _storyboards_by_id_for_tasks(session, list(tasks))
    episode_codes_by_id = await _episode_codes_by_id_for_tasks(session, list(tasks))
    output = []
    for task in tasks:
        task.assignee = users.get(task.assignee_id)  # type: ignore[attr-defined]
        dependency_locked, dependency_ids, dependency_codes = dependency_states[task.id]
        locked = dependency_locked or scene_lock_states.get(task.id, False)
        dependency_summary, dependency_assets = dependency_contracts.get(task.id, (DependencySummaryRead(), []))
        output.append(task_to_read(
            task,
            storyboard=storyboards_by_id.get(task.storyboard_id) if task.storyboard_id else None,
            episode_code=episode_codes_by_id.get(task.episode_id) if task.episode_id else None,
            locked=locked,
            dependency_task_ids=dependency_ids,
            dependency_asset_codes=dependency_codes,
            dependency_summary=dependency_summary,
            dependency_assets=dependency_assets,
        ))
    return output


async def list_review_tasks(session: AsyncSession, *, user: User) -> list[TaskRead]:
    if user.role not in {UserRole.director, UserRole.admin}:
        raise PermissionError("task_review")
    stmt = select(Task).options(
        selectinload(Task.submissions), selectinload(Task.submission_batches)
    ).where(
        _active_asset_task_condition(),
        Task.status.in_([
            TaskStatus.submitted,
            TaskStatus.reviewing,
            TaskStatus.rejected,
            TaskStatus.completed,
        ]),
        Task.submission_batches.any(TaskSubmissionBatch.status.in_(["submitted", "approved", "rework"])),
    ).order_by(Task.status, Task.updated_at.desc())
    active_project_ids = select(Project.id).where(Project.deleted_at.is_(None))
    stmt = stmt.where(Task.project_id.in_(active_project_ids))
    result = await session.execute(stmt)
    tasks = result.scalars().all()
    user_ids = {task.assignee_id for task in tasks if task.assignee_id}
    users: dict[UUID, User] = {}
    if user_ids:
        user_result = await session.execute(select(User).where(User.id.in_(user_ids)))
        users = {item.id: item for item in user_result.scalars().all()}
    storyboards_by_id = await _storyboards_by_id_for_tasks(session, list(tasks))
    episode_codes_by_id = await _episode_codes_by_id_for_tasks(session, list(tasks))
    dependency_states = await _batch_task_dependency_states(session, list(tasks))
    dependency_contracts = await _batch_dependency_contracts(session, list(tasks), dependency_states)
    output: list[TaskRead] = []
    for task in tasks:
        task.assignee = users.get(task.assignee_id)  # type: ignore[attr-defined]
        dependency_locked, dependency_ids, dependency_codes = dependency_states[task.id]
        dependency_summary, dependency_assets = dependency_contracts.get(task.id, (DependencySummaryRead(), []))
        output.append(task_to_read(
            task,
            storyboard=storyboards_by_id.get(task.storyboard_id) if task.storyboard_id else None,
            episode_code=episode_codes_by_id.get(task.episode_id) if task.episode_id else None,
            locked=dependency_locked,
            dependency_task_ids=dependency_ids,
            dependency_asset_codes=dependency_codes,
            dependency_summary=dependency_summary,
            dependency_assets=dependency_assets,
        ))
    return output


async def list_user_notifications(session: AsyncSession, *, user: User) -> list[NotificationRead]:
    rows = list((await session.scalars(
        select(Notification)
        .where(Notification.user_id == user.id)
        .order_by(Notification.created_at.desc())
        .limit(100)
    )).all())
    return [NotificationRead(
        id=item.id,
        title=item.title,
        content=item.content,
        type=item.type,
        is_read=item.is_read,
        created_at=item.created_at,
    ) for item in rows]


async def mark_user_notifications_read(session: AsyncSession, *, user: User) -> int:
    result = await session.execute(
        update(Notification)
        .where(Notification.user_id == user.id, Notification.is_read.is_(False))
        .values(is_read=True, updated_at=datetime.now(UTC))
    )
    await session.commit()
    return int(result.rowcount or 0)


async def get_task(session: AsyncSession, task_id: UUID, user: User) -> TaskRead:
    result = await session.execute(select(Task).options(
        selectinload(Task.assignee), selectinload(Task.submissions), selectinload(Task.submission_batches)
    ).where(Task.id == task_id, _active_asset_task_condition()))
    task = result.scalar_one_or_none()
    if task is None:
        raise KeyError(task_id)
    if not is_director_or_admin(user) and task.assignee_id != user.id and not await _has_project_permission(session, task.project_id, user):
        raise PermissionError(task_id)
    locked, dependency_ids, dependency_codes = await _task_dependency_state(session, task)
    dependency_summary, dependency_assets = await _dependency_contract_for_task(
        session, task, dependency_ids=dependency_ids, dependency_codes=dependency_codes
    )
    storyboard = await session.get(Storyboard, task.storyboard_id) if task.storyboard_id else None
    episode = await session.get(ProjectEpisode, task.episode_id) if task.episode_id else None
    return task_to_read(
        task,
        storyboard=storyboard,
        episode_code=episode.episode_code if episode else None,
        locked=locked,
        dependency_task_ids=dependency_ids,
        dependency_asset_codes=dependency_codes,
        dependency_summary=dependency_summary,
        dependency_assets=dependency_assets,
    )


async def get_task_model(session: AsyncSession, task_id: UUID, user: User) -> Task:
    task = await _get_active_asset_task(session, task_id)
    if task is None:
        raise KeyError(task_id)
    if not is_director_or_admin(user) and task.assignee_id != user.id and not await _has_project_permission(session, task.project_id, user):
        raise PermissionError(task_id)
    return task


async def update_task(session: AsyncSession, task_id: UUID, data: TaskUpdate, user: User) -> TaskRead:
    task = await _get_active_asset_task(session, task_id)
    if task is None:
        raise KeyError(task_id)
    if not await _can_update_task(session, task, user):
        raise PermissionError(task_id)
    updates = data.model_dump(exclude_unset=True)
    reassignment_reason = str(updates.pop("reassignment_reason", "") or "").strip()
    if "status" in updates and updates["status"] is not None:
        if not is_director_or_admin(user):
            raise PermissionError(task_id)
        if updates["status"] == TaskStatus.completed and task.status != TaskStatus.completed:
            approved_primary = await session.scalar(select(Submission.id).where(
                Submission.task_id == task.id,
                Submission.status == "primary_master",
                Submission.is_primary.is_(True),
                Submission.is_archived.is_(False),
                Submission.is_invalidated.is_(False),
            ))
            if approved_primary is None:
                raise ValueError("任务必须先完成成果审核并指定主母版。")
        task.status = updates["status"]
        if updates["status"] == TaskStatus.completed and task.completed_at is None:
            task.completed_at = datetime.now(UTC)
            task.visible_until = task.completed_at + timedelta(days=30)
    if "assignee_id" in updates and user.role in {UserRole.director, UserRole.admin}:
        new_assignee_id = updates["assignee_id"]
        if new_assignee_id is not None:
            assignee = await session.get(User, new_assignee_id)
            if assignee is None or not assignee.is_active or assignee.role in {UserRole.director, UserRole.admin}:
                raise ValueError("执行人无效、已停用或不是可分配的制作人员。")
        if task.assignee_id != new_assignee_id and task.status in {TaskStatus.completed, TaskStatus.submitted, TaskStatus.reviewing}:
            raise ValueError("待审核或已完成任务不能改派执行人。")

        assignment_tasks = [task]
        if task.storyboard_id and task.task_type in {TaskType.text_to_image, TaskType.image_to_video}:
            assignment_tasks.extend(list((await session.scalars(select(Task).where(
                Task.project_id == task.project_id,
                Task.episode_id == task.episode_id,
                Task.storyboard_id == task.storyboard_id,
                Task.task_type.in_([TaskType.text_to_image, TaskType.image_to_video]),
                Task.id != task.id,
                Task.is_retired.is_(False),
            ))).all()))
        if task.task_type == TaskType.asset and task.asset_id:
            asset_type = await session.scalar(select(Asset.asset_type).where(Asset.id == task.asset_id))
            if asset_type == AssetType.character:
                assignment_tasks.extend(list((await session.scalars(select(Task).where(
                    Task.project_id == task.project_id,
                    Task.episode_id == task.episode_id,
                    Task.asset_id == task.asset_id,
                    Task.task_type == TaskType.asset,
                    Task.id != task.id,
                    Task.is_retired.is_(False),
                ))).all()))

        protected_statuses = {TaskStatus.completed, TaskStatus.submitted, TaskStatus.reviewing}
        mutable_tasks = [item for item in assignment_tasks if item.id == task.id or item.status not in protected_statuses]
        reassignments = [
            (item, item.assignee_id)
            for item in mutable_tasks
            if item.assignee_id is not None and item.assignee_id != new_assignee_id
        ]
        if reassignments and not reassignment_reason:
            raise ValueError("改派任务必须填写改派原因。")
        now = datetime.now(UTC)
        for assignment_task in mutable_tasks:
            if assignment_task.assignee_id == new_assignee_id:
                continue
            assignment_task.assignee_id = new_assignee_id
            assignment_task.assigned_by = user.id
            assignment_task.assigned_at = now if new_assignee_id else None
            assignment_task.updated_at = now
        if reassignments:
            await _add_task_reassignment_notifications(
                session,
                reassignments=reassignments,
                new_assignee_id=new_assignee_id,
                reason=reassignment_reason,
            )
    if "latest_prompt_text" in updates and updates["latest_prompt_text"] is not None:
        task.latest_prompt_text = updates["latest_prompt_text"]
    if "production_model" in updates:
        task.production_model = updates["production_model"]
    task.updated_at = datetime.now(UTC)
    await session.commit()
    return await get_task(session, task_id, user)


async def _add_task_reassignment_notifications(
    session: AsyncSession,
    *,
    reassignments: list[tuple[Task, UUID]],
    new_assignee_id: UUID | None,
    reason: str,
) -> None:
    if not reassignments:
        return
    user_ids = {old_assignee_id for _, old_assignee_id in reassignments}
    if new_assignee_id:
        user_ids.add(new_assignee_id)
    users = {
        item.id: item.display_name
        for item in (await session.scalars(select(User).where(User.id.in_(user_ids)))).all()
    }
    new_assignee_name = users.get(new_assignee_id, "待分配") if new_assignee_id else "待分配"
    grouped: dict[UUID, list[Task]] = {}
    for reassigned_task, old_assignee_id in reassignments:
        grouped.setdefault(old_assignee_id, []).append(reassigned_task)
    for old_assignee_id, reassigned_tasks in grouped.items():
        task_names = "、".join(item.title for item in reassigned_tasks[:3])
        remainder = f"等 {len(reassigned_tasks)} 项任务" if len(reassigned_tasks) > 3 else ""
        session.add(Notification(
            user_id=old_assignee_id,
            title="任务已改派",
            content=f"{task_names}{remainder} 已改派给 {new_assignee_name}。改派原因：{reason}",
            type="task_reassigned_out",
        ))
    if new_assignee_id:
        unique_tasks = list({item.id: item for item, _ in reassignments}.values())
        task_names = "、".join(item.title for item in unique_tasks[:3])
        remainder = f"等 {len(unique_tasks)} 项任务" if len(unique_tasks) > 3 else ""
        session.add(Notification(
            user_id=new_assignee_id,
            title="收到改派任务",
            content=f"{task_names}{remainder} 已改派给你。改派原因：{reason}",
            type="task_reassigned_in",
        ))


async def list_task_prompts(session: AsyncSession, task_id: UUID) -> list[TaskPromptRead]:
    active_task_ids = select(Task.id).where(Task.id == task_id, _active_asset_task_condition())
    result = await session.execute(
        select(TaskPrompt)
        .where(TaskPrompt.task_id == task_id, TaskPrompt.task_id.in_(active_task_ids))
        .order_by(TaskPrompt.version_no)
    )
    return [task_prompt_to_read(item) for item in result.scalars().all()]


async def add_task_prompt(session: AsyncSession, task_id: UUID, data: TaskPromptCreate) -> TaskPromptRead:
    task = await _get_active_asset_task(session, task_id)
    if task is None:
        raise KeyError(task_id)
    version = (await session.scalar(select(func.max(TaskPrompt.version_no)).where(TaskPrompt.task_id == task_id)) or 0) + 1
    now = datetime.now(UTC)
    item = TaskPrompt(
        task_id=task_id,
        prompt_type=data.prompt_type,
        prompt_text=data.prompt_text,
        source=data.source,
        copied_at=now if data.copied else None,
        created_by=data.created_by,
        version_no=version,
    )
    task.latest_prompt_text = data.prompt_text
    task.updated_at = now
    session.add(item)
    await session.commit()
    await session.refresh(item)
    return task_prompt_to_read(item)


PROMPT_AGENT_KINDS = {AgentKind.text_to_image_prompt, AgentKind.image_to_video_prompt}
PROMPT_EDITABLE_TASK_STATUSES = {
    TaskStatus.todo,
    TaskStatus.in_progress,
    TaskStatus.rejected,
    TaskStatus.overdue,
}


def _agent_prompt_text(run: AgentRunRead) -> str:
    output = run.output if isinstance(run.output, dict) else {}
    value = output.get("prompt") if run.agent_type == AgentKind.text_to_image_prompt else output.get("motion_description")
    return str(value or "").strip()


async def materialize_task_prompt_agent_run(session: AsyncSession, run: AgentRunRead) -> dict[str, Any]:
    if run.status != AgentRunStatus.succeeded or run.agent_type not in PROMPT_AGENT_KINDS or run.task_id is None:
        return {"applied": False, "context_current": False, "reason": "not_applicable"}
    task = await _get_active_asset_task(session, run.task_id, with_for_update=True)
    if task is None:
        return {"applied": False, "context_current": False, "reason": "task_not_found"}
    output = run.output if isinstance(run.output, dict) else {}
    prompt_type = f"agent_run:{run.id}"
    existing = await session.scalar(select(TaskPrompt.id).where(
        TaskPrompt.task_id == task.id,
        TaskPrompt.prompt_type == prompt_type,
        TaskPrompt.source == "agent",
    ))
    if existing is not None:
        saved_metadata = (run.output or {}).get("task_prompt_materialization")
        if isinstance(saved_metadata, dict):
            return dict(saved_metadata)
        context_current = not task.asset_context_outdated
        return {
            "applied": True,
            "context_current": context_current,
            "reason": "already_applied" if context_current else "context_changed",
        }
    prompt_text = _agent_prompt_text(run)
    if not prompt_text:
        return {"applied": False, "context_current": False, "reason": "empty_prompt"}
    if task.status not in PROMPT_EDITABLE_TASK_STATUSES:
        return {"applied": False, "context_current": False, "reason": "task_not_editable"}
    dependency_locked, _, _ = await _task_dependency_state(session, task)
    if dependency_locked:
        return {"applied": False, "context_current": False, "reason": "task_locked"}
    input_fingerprint = str((run.input or {}).get("context_fingerprint") or "")
    current_fingerprint = await task_prompt_context_fingerprint(
        session,
        task.id,
        (run.input or {}).get("step"),
    )
    context_current = bool(input_fingerprint and input_fingerprint == current_fingerprint)
    version = (await session.scalar(
        select(func.max(TaskPrompt.version_no)).where(TaskPrompt.task_id == task.id)
    ) or 0) + 1
    now = datetime.now(UTC)
    session.add(TaskPrompt(
        task_id=task.id,
        prompt_type=prompt_type,
        prompt_text=prompt_text,
        source="agent",
        version_no=version,
    ))
    if task.asset_id:
        asset = await session.get(Asset, task.asset_id)
        if asset is not None:
            await _append_prompt_revision(
                session,
                asset,
                {
                    "asset_code": asset.asset_code,
                    "context_key": (run.input or {}).get("context_key") or "/".join(filter(None, (
                        asset.asset_code,
                        task.age_stage_code,
                        task.costume_variant_code,
                    ))) or asset.asset_code,
                    "output_spec": task.task_variant,
                    "prompt": prompt_text,
                    "negative_prompt": output.get("negative_prompt"),
                    "view_prompts": output.get("view_prompts") or [],
                    "state_prompts": output.get("state_prompts") or [],
                    "brief_trace": output.get("brief_trace") or {},
                    "input_snapshot": (run.input or {}).get("production_context") or {},
                    "resolved_production_brief": (run.input or {}).get("resolved_production_brief") or {},
                    "skill": output.get("skill") or (run.input or {}).get("skill"),
                    "skill_version": output.get("skill_version"),
                    "contract_version": output.get("contract_version"),
                    "input_hash": output.get("input_hash") or (run.input or {}).get("context_fingerprint"),
                },
                source_run_id=run.id,
                created_by=None,
            )
    task.latest_prompt_text = prompt_text
    task.asset_context_outdated = not context_current
    task.updated_at = now
    await session.commit()
    return {
        "applied": True,
        "context_current": context_current,
        "reason": "applied" if context_current else "context_changed",
    }


async def add_submission(session: AsyncSession, task_id: UUID, data: SubmissionCreate, user: User) -> SubmissionRead:
    task = await _get_active_asset_task(session, task_id)
    if task is None:
        raise KeyError(task_id)
    if not await _can_update_task(session, task, user):
        raise PermissionError(task_id)
    step = _submission_step(task, data.step)
    if task.task_type == TaskType.audio and data.file_type != "audio":
        raise ValueError("音频任务只能登记 WAV 或 MP3 音频成果。")
    if data.file_id:
        existing_submission = await session.scalar(
            select(Submission)
            .where(
                Submission.task_id == task.id,
                Submission.file_id == data.file_id,
                func.coalesce(Submission.step, "result") == step,
            )
            .order_by(Submission.created_at)
            .limit(1)
        )
        if existing_submission is not None:
            if existing_submission.is_archived or existing_submission.is_invalidated:
                raise ValueError("该上传文件已被移除或作废，不能重新登记；请重新上传新文件。")
            return submission_to_read(existing_submission)
    # Candidate uploads can finish concurrently. Serialize draft-batch lookup
    # and creation per task so every new file is attached to one review batch.
    task = await session.scalar(
        select(Task)
        .where(Task.id == task_id, _active_asset_task_condition())
        .with_for_update()
    )
    if task is None:
        raise KeyError(task_id)
    # A retry for the same uploaded file can race past the optimistic lookup
    # above. Recheck while holding the task row lock so one file is registered
    # at most once in the active submission step.
    if data.file_id:
        existing_submission = await session.scalar(
            select(Submission)
            .where(
                Submission.task_id == task.id,
                Submission.file_id == data.file_id,
                func.coalesce(Submission.step, "result") == step,
            )
            .order_by(Submission.created_at)
            .limit(1)
        )
        if existing_submission is not None:
            if existing_submission.is_archived or existing_submission.is_invalidated:
                raise ValueError("该上传文件已被移除或作废，不能重新登记；请重新上传新文件。")
            return submission_to_read(existing_submission)
    if task.status in {TaskStatus.reviewing, TaskStatus.submitted}:
        raise ValueError("当前任务已有待审核批次，审核完成前不能继续上传。")
    if task.status == TaskStatus.completed:
        raise ValueError("已完成任务不能继续上传；如需换版，请先由导演发起返工。")
    dependency_locked, _, dependency_codes = await _task_dependency_state(session, task)
    if dependency_locked:
        labels = "、".join(dependency_codes) or "前置任务"
        raise ValueError(f"{labels} 尚未完成，当前任务暂未解锁。")
    source_master_submission: Submission | None = None
    if await _character_task_requires_master_lineage(session, task):
        source_master_submission = await _current_character_master_submission(session, task)
        if source_master_submission is None:
            raise ValueError("当前人物图位缺少有效的 A 身份母版，请先完成 A 图审批。")
    if task.storyboard_id and task.task_type in {
        TaskType.storyboard_shot, TaskType.text_to_image, TaskType.image_to_video
    }:
        if await _scene_is_locked(session, task.project_id, task.scene_code, task.scene_name):
            raise ValueError("该场景所需资产尚未全部完成，分镜任务暂未解锁，不能提交。")
        if task.task_type == TaskType.storyboard_shot and step == "video":
            approved_keyframe = await session.scalar(select(Submission.id).where(
                Submission.task_id == task.id,
                Submission.step == "keyframe",
                Submission.status == "primary_master",
                Submission.is_primary.is_(True),
                Submission.is_archived.is_(False),
                Submission.is_invalidated.is_(False),
            ))
            if approved_keyframe is None:
                raise ValueError("关键帧尚未审核定版，视频任务暂未解锁。")
        if task.task_type == TaskType.image_to_video and task.depends_on_task_id:
            dependency_status = await session.scalar(
                select(Task.status).where(
                    Task.id == task.depends_on_task_id,
                    Task.is_retired.is_(False),
                )
            )
            if dependency_status != TaskStatus.completed:
                raise ValueError("关键帧任务尚未完成，视频任务暂未解锁。")

    if data.file_id:
        file_object = await session.get(FileObject, data.file_id)
        if file_object is None or file_object.task_id != task.id or file_object.project_id != task.project_id:
            raise ValueError("上传文件与当前任务不匹配。")

    batch = await session.scalar(
        select(TaskSubmissionBatch)
        .where(
            TaskSubmissionBatch.task_id == task.id,
            TaskSubmissionBatch.step == step,
            TaskSubmissionBatch.status == "draft",
        )
        .order_by(TaskSubmissionBatch.version_no.desc())
        .limit(1)
    )
    if batch is None:
        latest_version = await session.scalar(select(func.max(TaskSubmissionBatch.version_no)).where(
            TaskSubmissionBatch.task_id == task.id,
            TaskSubmissionBatch.step == step,
        )) or 0
        batch = TaskSubmissionBatch(
            task_id=task.id,
            step=step,
            version_no=latest_version + 1,
            status="draft",
            submitted_by_id=data.submitted_by_id or user.id,
        )
        session.add(batch)
        await session.flush()

    now = datetime.now(UTC)
    submission = Submission(
        task_id=task_id,
        batch_id=batch.id,
        file_id=data.file_id,
        storyboard_id=task.storyboard_id if task else None,
        file_path=data.file_path,
        file_type=data.file_type,
        step=step,
        prompt_text=data.prompt_text,
        original_prompt_text=data.original_prompt_text,
        revised_prompt_text=data.revised_prompt_text,
        view_label=data.view_label,
        state_label=data.state_label,
        description=data.description,
        model_name=data.model_name,
        tool_names=data.tool_names,
        submitted_by_id=data.submitted_by_id or user.id,
        status="draft",
        source_master_submission_id=(
            source_master_submission.id if source_master_submission is not None else None
        ),
    )
    session.add(submission)
    await session.flush()
    final_prompt = data.revised_prompt_text or data.prompt_text or data.original_prompt_text or task.latest_prompt_text
    task.latest_prompt_text = final_prompt or task.latest_prompt_text
    task.production_model = data.model_name or task.production_model
    if source_master_submission is not None:
        task.asset_context_outdated = False
    task.status = TaskStatus.in_progress
    task.completed_at = None
    task.visible_until = None
    task.updated_at = now
    session.add(OperationLog(
        operator_id=user.id,
        project_id=task.project_id,
        target_type="submission",
        target_id=submission.id,
        action="task_submission_created",
        detail={"task_id": str(task.id), "file_type": submission.file_type, "model_name": submission.model_name},
    ))
    await session.commit()
    await session.refresh(submission)
    return submission_to_read(submission)


async def update_submission_metadata(
    session: AsyncSession,
    task_id: UUID,
    submission_id: UUID,
    data: SubmissionMetadataUpdate,
    user: User,
) -> SubmissionRead:
    task = await _get_active_asset_task(session, task_id)
    if task is None:
        raise KeyError(task_id)
    if not await _can_update_task(session, task, user):
        raise PermissionError(task_id)
    submission = await session.scalar(select(Submission).where(
        Submission.id == submission_id,
        Submission.task_id == task_id,
    ))
    if submission is None:
        raise KeyError(submission_id)
    if submission.is_archived or submission.is_invalidated:
        raise ValueError("已归档或已作废候选不能编辑说明。")
    if submission.status not in {"draft", "rejected"}:
        raise ValueError("只有未提交草稿或已驳回成果可以编辑说明。")
    updates = data.model_dump(exclude_unset=True)
    for field in ("view_label", "state_label", "description"):
        if field in updates:
            value = updates[field]
            setattr(submission, field, str(value).strip() or None if value is not None else None)
    submission.updated_at = datetime.now(UTC)
    await session.commit()
    await session.refresh(submission)
    return submission_to_read(submission)


def _submission_step(task: Task, requested_step: str | None) -> str:
    step = str(requested_step or "").strip().lower()
    if task.task_type == TaskType.audio:
        return "audio"
    if task.task_type == TaskType.storyboard_shot:
        if step not in {"keyframe", "video"}:
            raise ValueError("分镜任务必须标明 keyframe 或 video 步骤。")
        return step
    if task.task_type == TaskType.text_to_image:
        return "keyframe" if task.storyboard_id else "result"
    if task.task_type in {TaskType.image_to_video, TaskType.video_generation}:
        return "video" if task.storyboard_id else "result"
    return "result"


async def submit_task_batch(
    session: AsyncSession,
    task_id: UUID,
    data: TaskSubmitRequest,
    user: User,
    *,
    commit: bool = True,
) -> SubmissionBatchRead:
    task = await _get_active_asset_task(session, task_id, with_for_update=True)
    if task is None:
        raise KeyError(task_id)
    if not await _can_update_task(session, task, user):
        raise PermissionError(task_id)
    step = _submission_step(task, data.step)
    if data.request_id:
        existing_request_batch = await session.scalar(select(TaskSubmissionBatch).where(
            TaskSubmissionBatch.submit_request_id == data.request_id
        ))
        if existing_request_batch is not None:
            if existing_request_batch.task_id != task.id or existing_request_batch.step != step:
                raise ValueError("提交请求标识已用于其他任务或生产步骤。")
            return submission_batch_to_read(existing_request_batch)
    if task.status in {TaskStatus.reviewing, TaskStatus.submitted, TaskStatus.completed}:
        raise ValueError("当前任务状态不能重复提交。")
    batch = await session.scalar(
        select(TaskSubmissionBatch)
        .options(selectinload(TaskSubmissionBatch.submissions))
        .where(
            TaskSubmissionBatch.task_id == task.id,
            TaskSubmissionBatch.step == step,
            TaskSubmissionBatch.status == "draft",
        )
        .order_by(TaskSubmissionBatch.version_no.desc())
        .limit(1)
    )
    if batch is None or not batch.submissions:
        raise ValueError("请先上传至少一个候选成果。")
    active_submissions = [
        item for item in batch.submissions
        if not item.is_archived and not item.is_invalidated
    ]
    if not active_submissions:
        raise ValueError("当前批次没有可提交的候选成果。")
    now = datetime.now(UTC)
    batch.status = "submitted"
    batch.submitted_by_id = user.id
    batch.submitted_at = now
    batch.submit_request_id = data.request_id
    batch.updated_at = now
    for submission in active_submissions:
        submission.status = "submitted"
        submission.updated_at = now
    task.status = TaskStatus.reviewing
    task.updated_at = now
    session.add(OperationLog(
        operator_id=user.id,
        project_id=task.project_id,
        target_type="task_submission_batch",
        target_id=batch.id,
        action="task_batch_submitted",
        detail={
            "task_id": str(task.id),
            "step": batch.step,
            "version_no": batch.version_no,
            "request_id": str(data.request_id) if data.request_id else None,
        },
    ))
    if commit:
        await session.commit()
        await session.refresh(batch)
    else:
        await session.flush()
    return submission_batch_to_read(batch)


async def delete_draft_submission(
    session: AsyncSession,
    task_id: UUID,
    submission_id: UUID,
    user: User,
) -> dict[str, str]:
    task = await _get_active_asset_task(session, task_id, with_for_update=True)
    if task is None:
        raise KeyError(task_id)
    if not await _can_update_task(session, task, user):
        raise PermissionError(task_id)
    submission = await session.scalar(select(Submission).where(
        Submission.id == submission_id,
        Submission.task_id == task.id,
    ))
    if submission is None:
        raise KeyError(submission_id)
    if submission.status not in {"draft", "rejected"}:
        raise ValueError("只有未提交草稿或已驳回成果可以移除；待审核和已定版成果必须保留。")
    if submission.is_archived or submission.is_invalidated:
        return {"archived": str(submission_id)}
    now = datetime.now(UTC)
    submission.is_archived = True
    submission.archived_by_id = user.id
    submission.archived_at = now
    submission.updated_at = now
    session.add(OperationLog(
        operator_id=user.id,
        project_id=task.project_id,
        target_type="submission",
        target_id=submission.id,
        action="task_draft_submission_removed",
        detail={"task_id": str(task.id), "soft_delete": True},
    ))
    await session.commit()
    return {"archived": str(submission_id)}


async def review_task_batch(
    session: AsyncSession,
    task_id: UUID,
    batch_id: UUID,
    data: TaskReviewRequest,
    user: User,
    *,
    commit: bool = True,
) -> TaskRead:
    task = await _get_active_asset_task(session, task_id, with_for_update=True)
    if task is None:
        raise KeyError(task_id)
    if not await can_review_project_tasks(session, task.project_id, user):
        raise PermissionError(task_id)
    batch = await session.scalar(
        select(TaskSubmissionBatch)
        .options(selectinload(TaskSubmissionBatch.submissions))
        .where(TaskSubmissionBatch.id == batch_id, TaskSubmissionBatch.task_id == task.id)
        .with_for_update()
    )
    if batch is None:
        raise KeyError(batch_id)
    if data.request_id:
        existing_request_batch = await session.scalar(select(TaskSubmissionBatch).where(
            TaskSubmissionBatch.review_request_id == data.request_id
        ))
        if existing_request_batch is not None:
            if existing_request_batch.id != batch.id or existing_request_batch.task_id != task.id:
                raise ValueError("审核请求标识已用于其他任务批次。")
            return await get_task(session, task.id, user)
    previous_batch_status = batch.status
    if data.decision == "approve" and previous_batch_status != "submitted":
        raise ValueError("只有待审核批次可以审核通过。")
    if data.decision == "rework" and previous_batch_status not in {"submitted", "approved"}:
        raise ValueError("只有待审核或已通过批次可以退回返工。")
    reopening_approved_batch = data.decision == "rework" and previous_batch_status == "approved"
    if data.decision == "rework" and not str(data.comment or "").strip():
        raise ValueError("退回返工时必须填写具体修改原因。")
    if reopening_approved_batch:
        latest_approved_batch_id = await session.scalar(
            select(TaskSubmissionBatch.id)
            .where(
                TaskSubmissionBatch.task_id == task.id,
                TaskSubmissionBatch.step == batch.step,
                TaskSubmissionBatch.status == "approved",
            )
            .order_by(TaskSubmissionBatch.version_no.desc())
            .limit(1)
        )
        if latest_approved_batch_id != batch.id:
            raise ValueError("只能退回当前步骤的最新已通过版本，历史版本仅供查看。")
    if batch.submitted_by_id == user.id:
        raise ValueError("审批人不能审核自己提交的成果。")
    locked_scenes_before = {
        str(item.get("scene_code") or item.get("scene_name"))
        for item in await _scene_gating_core(session, task.project_id)
        if item.get("locked")
    } if task.task_type == TaskType.asset else set()
    now = datetime.now(UTC)
    revoked_master_submission_id = next(
        (item.id for item in batch.submissions if item.is_primary),
        None,
    )
    previous_review = {
        "reviewed_by_id": str(batch.reviewed_by_id) if batch.reviewed_by_id else None,
        "reviewed_at": batch.reviewed_at.isoformat() if batch.reviewed_at else None,
        "review_comment": batch.review_comment,
    }
    batch.reviewed_by_id = user.id
    batch.reviewed_at = now
    batch.review_comment = str(data.comment or "").strip() or None
    batch.review_request_id = data.request_id
    batch.updated_at = now

    if data.decision == "rework":
        batch.status = "rework"
        for submission in batch.submissions:
            submission.status = "rejected"
            submission.is_selected = False
            submission.is_primary = False
            submission.updated_at = now
        if reopening_approved_batch:
            await _invalidate_character_dependent_views(
                session,
                task,
                invalidated_by=user.id,
                invalidated_at=now,
                invalidated_source_id=revoked_master_submission_id,
            )
            await _revoke_materialized_asset_versions(
                session,
                task,
                batch,
                revoked_by=user.id,
                revoked_at=now,
                reason=batch.review_comment,
            )
            # Keep the original approval fields in an append-only record before
            # the batch's latest-review projection is updated to this rework.
            session.add(OperationLog(
                operator_id=user.id,
                project_id=task.project_id,
                target_type="task_submission_batch",
                target_id=batch.id,
                action="task_approval_reopened",
                detail={
                    "task_id": str(task.id),
                    "batch_id": str(batch.id),
                    "batch_version_no": batch.version_no,
                    "step": batch.step,
                    "previous_status": previous_batch_status,
                    "previous_review": previous_review,
                    "rework_comment": batch.review_comment,
                },
            ))
        task.status = TaskStatus.rejected
        task.completed_at = None
        task.visible_until = None
        if task.task_type in {TaskType.asset, TaskType.audio} and task.asset_id:
            await _refresh_asset_completion_status(session, task.asset_id, now)
        if reopening_approved_batch:
            dependent_ids = set((await session.scalars(
                select(Task.id).where(
                    Task.depends_on_task_id == task.id,
                    Task.is_retired.is_(False),
                )
            )).all())
            dependent_ids.update((await session.scalars(
                select(TaskDependency.task_id).where(TaskDependency.depends_on_task_id == task.id)
            )).all())
            if dependent_ids:
                await session.execute(update(Task).where(
                    Task.id.in_(dependent_ids),
                    Task.status.in_([TaskStatus.in_progress, TaskStatus.submitted, TaskStatus.reviewing]),
                    Task.is_retired.is_(False),
                ).values(asset_context_outdated=True, updated_at=now))
        if task.assignee_id:
            session.add(Notification(
                user_id=task.assignee_id,
                title="任务已退回返工",
                content=f"{task.title}：{batch.review_comment or '请根据审核意见重新提交候选成果。'}",
                type="task_rework",
            ))
    else:
        selected_ids = list(dict.fromkeys(data.selected_submission_ids))
        if not selected_ids:
            raise ValueError("审核通过时必须选择至少一个定版成果。")
        # Characters keep the explicit A/master choice. Scene/prop multi-angle
        # uploads do not require a master designation; every selected angle is
        # materialized into the formal asset library.
        reviewed_asset = await session.get(Asset, task.asset_id) if task.asset_id else None
        requires_primary = bool(
            reviewed_asset is not None
            and reviewed_asset.asset_type == AssetType.character
            and (task.task_variant or "MASTER").upper() in {"A", "MASTER"}
        )
        requires_master_lineage = bool(
            reviewed_asset is not None
            and reviewed_asset.asset_type == AssetType.character
            and (task.task_variant or "MASTER").upper() not in {"A", "MASTER"}
        )
        if requires_primary and data.primary_submission_id is None:
            raise ValueError("人物定装审核通过时必须指定主母版。")
        if data.primary_submission_id is not None and data.primary_submission_id not in selected_ids:
            raise ValueError("主母版必须包含在定版成果中。")
        batch_submission_ids = {
            item.id for item in batch.submissions
            if not item.is_archived and not item.is_invalidated
        }
        if not set(selected_ids) <= batch_submission_ids:
            raise ValueError("定版成果必须属于当前待审核批次。")
        if requires_master_lineage:
            current_master = await _current_character_master_submission(session, task)
            selected_submissions = [item for item in batch.submissions if item.id in selected_ids]
            if current_master is None or any(
                item.source_master_submission_id != current_master.id
                for item in selected_submissions
            ):
                raise ValueError("A 身份母版已变更，当前 B-E 成果已失效，请重新生产并提交。")
        batch.status = "approved"
        previous_primaries = list((await session.scalars(select(Submission).where(
            Submission.task_id == task.id,
            func.coalesce(Submission.step, "result") == batch.step,
            Submission.is_primary.is_(True),
            Submission.is_invalidated.is_(False),
            or_(Submission.batch_id.is_(None), Submission.batch_id != batch.id),
        ))).all())
        for submission in previous_primaries:
            submission.is_primary = False
            submission.is_selected = True
            submission.status = "alternate_master"
            submission.updated_at = now
        for submission in batch.submissions:
            submission.is_selected = submission.id in selected_ids
            submission.is_primary = data.primary_submission_id is not None and submission.id == data.primary_submission_id
            submission.status = (
                "primary_master" if submission.is_primary
                else "alternate_master" if submission.is_selected
                else "not_selected"
            )
            submission.updated_at = now
        await _materialize_approved_asset_versions(session, task, batch.submissions, user.id)
        if task.task_type == TaskType.storyboard_shot and batch.step == "keyframe":
            task.status = TaskStatus.in_progress
            task.completed_at = None
            task.visible_until = None
        else:
            task.status = TaskStatus.completed
            task.completed_at = now
            task.visible_until = now + timedelta(days=30)
        if task.task_type in {TaskType.asset, TaskType.audio} and task.asset_id:
            await _refresh_asset_completion_status(session, task.asset_id, now)
        if task.assignee_id:
            session.add(Notification(
                user_id=task.assignee_id,
                title="成果已审核定版",
                content=f"{task.title} 已通过审核，主母版已确定。",
                type="task_approved",
            ))
    task.updated_at = now
    session.add(OperationLog(
        operator_id=user.id,
        project_id=task.project_id,
        target_type="task_submission_batch",
        target_id=batch.id,
        action="task_batch_reviewed",
        detail={
            "task_id": str(task.id),
            "decision": data.decision,
            "primary_submission_id": str(data.primary_submission_id) if data.primary_submission_id else None,
            "selected_submission_ids": [str(item) for item in data.selected_submission_ids],
            "comment": data.comment,
            "request_id": str(data.request_id) if data.request_id else None,
        },
    ))
    await session.flush()
    if data.decision == "approve":
        await _notify_unlocked_dependents(session, task, locked_scenes_before)
    if commit:
        await session.commit()
    return await get_task(session, task.id, user)


async def bulk_submit_task_batches(
    session: AsyncSession,
    data: TaskBulkSubmitRequest,
    user: User,
) -> list[SubmissionBatchRead]:
    task_ids = [entry.task_id for entry in data.entries]
    if len(task_ids) != len(set(task_ids)):
        raise ValueError("批量提交不能包含重复任务。")
    tasks = list((await session.scalars(select(Task).where(
        Task.id.in_(task_ids),
        _active_asset_task_condition(),
    ))).all())
    if len(tasks) != len(task_ids):
        raise KeyError("task")
    variants = {str(item.task_variant or "").upper() for item in tasks}
    if len(tasks) > 1 and variants & {"A", "MASTER"}:
        raise ValueError("人物 A 身份母版必须独立提交，不能与 B-E 合并操作。")
    results: list[SubmissionBatchRead] = []
    try:
        for entry in data.entries:
            request_id = uuid.uuid5(uuid.NAMESPACE_URL, f"task-submit:{data.request_id}:{entry.task_id}:{entry.step or 'result'}")
            results.append(await submit_task_batch(
                session,
                entry.task_id,
                TaskSubmitRequest(step=entry.step, request_id=request_id),
                user,
                commit=False,
            ))
        await session.commit()
        return results
    except Exception:
        await session.rollback()
        raise


async def bulk_review_task_batches(
    session: AsyncSession,
    data: TaskBulkReviewRequest,
    user: User,
) -> list[TaskRead]:
    task_ids = [entry.task_id for entry in data.entries]
    if len(task_ids) != len(set(task_ids)):
        raise ValueError("批量审核不能包含重复任务。")
    tasks = list((await session.scalars(select(Task).where(
        Task.id.in_(task_ids),
        _active_asset_task_condition(),
    ))).all())
    if len(tasks) != len(task_ids):
        raise KeyError("task")
    variants = {str(item.task_variant or "").upper() for item in tasks}
    if len(tasks) > 1 and variants & {"A", "MASTER"}:
        raise ValueError("人物 A 身份母版必须独立审核，不能与 B-E 合并操作。")
    results: list[TaskRead] = []
    try:
        for entry in data.entries:
            request_id = uuid.uuid5(uuid.NAMESPACE_URL, f"task-review:{data.request_id}:{entry.task_id}:{entry.batch_id}")
            results.append(await review_task_batch(
                session,
                entry.task_id,
                entry.batch_id,
                TaskReviewRequest(
                    decision=data.decision,
                    primary_submission_id=entry.primary_submission_id if data.decision == "approve" else None,
                    selected_submission_ids=entry.selected_submission_ids if data.decision == "approve" else [],
                    comment=data.comment,
                    request_id=request_id,
                ),
                user,
                commit=False,
            ))
        await session.commit()
        return results
    except Exception:
        await session.rollback()
        raise


async def _invalidate_character_dependent_views(
    session: AsyncSession,
    task: Task,
    *,
    invalidated_by: UUID,
    invalidated_at: datetime,
    invalidated_source_id: UUID | None,
) -> None:
    """Invalidate B-E production when an approved character A is reopened."""
    if (
        task.asset_id is None
        or task.task_type not in {TaskType.asset, TaskType.text_to_image}
        or str(task.task_variant or "MASTER").upper() not in {"A", "MASTER"}
    ):
        return
    asset = await session.get(Asset, task.asset_id, with_for_update=True)
    if asset is None or asset.asset_type != AssetType.character:
        return

    dependent_tasks = list((await session.scalars(
        select(Task)
        .where(
            Task.project_id == task.project_id,
            Task.asset_id == task.asset_id,
            Task.id != task.id,
            Task.task_type.in_([TaskType.asset, TaskType.text_to_image]),
            func.coalesce(Task.age_stage_code, "") == (task.age_stage_code or ""),
            func.coalesce(Task.costume_variant_code, "") == (task.costume_variant_code or ""),
            func.upper(func.coalesce(Task.task_variant, "MASTER")).notin_(["A", "MASTER"]),
            _active_asset_task_condition(),
        )
        .with_for_update()
    )).all())
    if not dependent_tasks:
        return
    dependent_task_ids = {item.id for item in dependent_tasks}
    invalidated_submissions = list((await session.scalars(
        select(Submission).where(
            Submission.task_id.in_(dependent_task_ids),
            Submission.is_invalidated.is_(False),
        )
    )).all())
    for submission in invalidated_submissions:
        submission.is_invalidated = True
        submission.invalidated_at = invalidated_at
        submission.invalidated_by_id = invalidated_by
        submission.invalidated_reason = "parent_master_revoked"
        submission.invalidated_source_id = invalidated_source_id
        submission.is_selected = False
        submission.is_primary = False
        submission.updated_at = invalidated_at

    invalidated_versions = list((await session.scalars(
        select(AssetVersion).where(
            AssetVersion.asset_id == task.asset_id,
            AssetVersion.is_invalidated.is_(False),
            or_(
                AssetVersion.source_task_id.in_(dependent_task_ids),
                and_(
                    invalidated_source_id is not None,
                    AssetVersion.source_master_submission_id == invalidated_source_id,
                ),
            ),
        )
    )).all())
    invalidated_version_ids = {item.id for item in invalidated_versions}
    for version in invalidated_versions:
        version.is_invalidated = True
        version.invalidated_at = invalidated_at
        version.invalidated_by_id = invalidated_by
        version.invalidated_reason = "parent_master_revoked"
        version.invalidated_source_id = invalidated_source_id
        version.is_current = False
        version.updated_at = invalidated_at

    if asset.current_version_id in invalidated_version_ids:
        asset.current_version_id = None
        asset.file_path = None
        asset.preview_path = None
    asset.status = "in_progress"
    asset.updated_at = invalidated_at

    for dependent in dependent_tasks:
        dependent.status = TaskStatus.rejected
        dependent.completed_at = None
        dependent.visible_until = None
        dependent.asset_context_outdated = True
        dependent.updated_at = invalidated_at
        if dependent.assignee_id:
            session.add(Notification(
                user_id=dependent.assignee_id,
                title="人物身份母版已更新",
                content=f"{dependent.title} 的旧成果已作废，请基于新的 A 图重新生产。",
                type="task_rework",
            ))
    session.add(OperationLog(
        operator_id=invalidated_by,
        project_id=task.project_id,
        target_type="character_asset",
        target_id=task.asset_id,
        action="character_dependent_views_invalidated",
        detail={
            "master_task_id": str(task.id),
            "master_submission_id": str(invalidated_source_id) if invalidated_source_id else None,
            "reason": "parent_master_revoked",
            "dependent_task_ids": [str(item.id) for item in dependent_tasks],
            "submission_count": len(invalidated_submissions),
            "asset_version_count": len(invalidated_versions),
        },
    ))


async def _revoke_materialized_asset_versions(
    session: AsyncSession,
    task: Task,
    batch: TaskSubmissionBatch,
    *,
    revoked_by: UUID,
    revoked_at: datetime,
    reason: str | None,
) -> None:
    """Revoke formal asset projections without deleting approval history."""
    if task.asset_id is None or task.task_type not in {TaskType.asset, TaskType.text_to_image, TaskType.audio}:
        return
    submission_ids = {str(item.id) for item in batch.submissions}
    versions = list((await session.scalars(
        select(AssetVersion).where(AssetVersion.asset_id == task.asset_id).order_by(AssetVersion.version_no)
    )).all())
    revoked_versions: list[AssetVersion] = []
    for version in versions:
        metadata = dict(version.metadata_json or {})
        if (
            str(metadata.get("batch_id") or "") != str(batch.id)
            and str(metadata.get("submission_id") or "") not in submission_ids
        ):
            continue
        metadata.update({
            "submission_status": "rejected",
            "is_primary": False,
            "approval_revoked_at": revoked_at.isoformat(),
            "approval_revoked_by": str(revoked_by),
            "approval_revoked_reason": reason,
        })
        version.metadata_json = metadata
        version.is_current = False
        revoked_versions.append(version)
    if not revoked_versions:
        return

    asset = await session.get(Asset, task.asset_id, with_for_update=True)
    if asset is None or asset.current_version_id not in {item.id for item in revoked_versions}:
        return
    remaining_submissions = list((await session.scalars(
        select(Submission)
        .join(Task, Task.id == Submission.task_id)
        .where(
            Task.asset_id == task.asset_id,
            Task.is_retired.is_(False),
            Submission.status.in_(APPROVED_SUBMISSION_STATUSES),
            Submission.is_selected.is_(True),
            Submission.is_archived.is_(False),
            Submission.is_invalidated.is_(False),
            Submission.file_id.is_not(None),
        )
    )).all())
    remaining_by_id = {str(item.id): item for item in remaining_submissions}
    fallback = max(
        (
            version for version in versions
            if not version.is_invalidated
            if str((version.metadata_json or {}).get("submission_id") or "") in remaining_by_id
        ),
        key=lambda item: (
            bool(remaining_by_id[str((item.metadata_json or {}).get("submission_id"))].is_primary),
            item.version_no,
        ),
        default=None,
    )
    if fallback is None:
        asset.current_version_id = None
        asset.file_path = None
        asset.preview_path = None
        return
    fallback_submission = remaining_by_id[str((fallback.metadata_json or {}).get("submission_id"))]
    fallback.is_current = True
    asset.current_version_id = fallback.id
    asset.file_path = fallback_submission.file_path
    asset.preview_path = fallback_submission.file_path if fallback_submission.file_type == "image" else None


async def set_submission_archived(
    session: AsyncSession,
    task_id: UUID,
    submission_id: UUID,
    data: SubmissionArchiveRequest,
    user: User,
) -> SubmissionRead:
    task = await _get_active_asset_task(session, task_id, with_for_update=True)
    if task is None:
        raise KeyError(task_id)
    if not await can_review_project_tasks(session, task.project_id, user):
        raise PermissionError(task_id)
    submission = await session.scalar(select(Submission).where(
        Submission.id == submission_id,
        Submission.task_id == task.id,
    ))
    if submission is None:
        raise KeyError(submission_id)
    if submission.is_invalidated:
        raise ValueError("已作废成果仅保留审计记录，不能归档或恢复。")
    if data.archived and submission.is_primary:
        raise ValueError("主母版在被替换前不能归档。")
    now = datetime.now(UTC)
    submission.is_archived = data.archived
    submission.archived_by_id = user.id if data.archived else None
    submission.archived_at = now if data.archived else None
    submission.updated_at = now
    session.add(OperationLog(
        operator_id=user.id,
        project_id=task.project_id,
        target_type="submission",
        target_id=submission.id,
        action="task_submission_archived" if data.archived else "task_submission_restored",
        detail={"task_id": str(task.id), "archived": data.archived},
    ))
    await session.commit()
    await session.refresh(submission)
    return submission_to_read(submission)


async def set_primary_submission(
    session: AsyncSession,
    task_id: UUID,
    data: PrimarySubmissionRequest,
    user: User,
) -> TaskRead:
    task = await _get_active_asset_task(session, task_id, with_for_update=True)
    if task is None:
        raise KeyError(task_id)
    if not await can_review_project_tasks(session, task.project_id, user):
        raise PermissionError(task_id)
    selected = await session.scalar(select(Submission).where(
        Submission.id == data.submission_id,
        Submission.task_id == task.id,
    ))
    if selected is None:
        raise KeyError(data.submission_id)
    if selected.is_archived or selected.is_invalidated or selected.status in {"draft", "submitted", "rejected"}:
        raise ValueError("草稿、待审核、已驳回、已归档或已作废成果不能设为主母版。")
    step = str(selected.step or "result")
    now = datetime.now(UTC)
    existing_primary = list((await session.scalars(select(Submission).where(
        Submission.task_id == task.id,
        func.coalesce(Submission.step, "result") == step,
        Submission.is_primary.is_(True),
        Submission.is_invalidated.is_(False),
    ))).all())
    for item in existing_primary:
        item.is_primary = False
        item.is_selected = True
        item.status = "alternate_master"
        item.updated_at = now
    selected.is_primary = True
    selected.is_selected = True
    selected.status = "primary_master"
    selected.updated_at = now
    await _materialize_approved_asset_versions(
        session,
        task,
        [*existing_primary, selected],
        user.id,
    )

    dependent_ids = list((await session.scalars(
        select(Task.id).where(
            Task.depends_on_task_id == task.id,
            Task.is_retired.is_(False),
        )
    )).all())
    dependent_ids.extend(list((await session.scalars(
        select(TaskDependency.task_id).where(TaskDependency.depends_on_task_id == task.id)
    )).all()))
    if dependent_ids:
        await session.execute(update(Task).where(
            Task.id.in_(set(dependent_ids)),
            Task.status.in_([TaskStatus.in_progress, TaskStatus.submitted, TaskStatus.reviewing]),
            Task.is_retired.is_(False),
        ).values(asset_context_outdated=True, updated_at=now))
    session.add(OperationLog(
        operator_id=user.id,
        project_id=task.project_id,
        target_type="submission",
        target_id=selected.id,
        action="task_primary_submission_changed",
        detail={"task_id": str(task.id), "submission_id": str(selected.id)},
    ))
    await session.commit()
    return await get_task(session, task.id, user)


async def _materialize_approved_asset_versions(
    session: AsyncSession,
    task: Task,
    submissions: list[Submission],
    created_by: UUID | None,
) -> None:
    if task.asset_id is None or task.task_type not in {TaskType.asset, TaskType.text_to_image, TaskType.audio}:
        return
    selected = [
        item for item in submissions
        if (
            item.is_selected
            and not item.is_archived
            and not item.is_invalidated
            and item.status in APPROVED_SUBMISSION_STATUSES
        )
    ]
    if not selected:
        return
    asset = await session.get(Asset, task.asset_id, with_for_update=True)
    if asset is None:
        return
    existing = list((await session.scalars(
        select(AssetVersion).where(AssetVersion.asset_id == asset.id).order_by(AssetVersion.version_no)
    )).all())
    by_submission_id = {
        str((version.metadata_json or {}).get("submission_id")): version
        for version in existing
        if (version.metadata_json or {}).get("submission_id")
    }
    version_no = max((version.version_no for version in existing), default=0)
    context_version_counters: dict[tuple[str, str, str], int] = {}
    for previous in existing:
        previous_metadata = dict(previous.metadata_json or {})
        previous_context_version, _ = _asset_context_version(
            asset.asset_type,
            previous_metadata,
            previous.version_no,
            context_version_counters,
        )
        if asset.asset_type == AssetType.character:
            previous_metadata["context_version_no"] = previous_context_version
            previous.metadata_json = previous_metadata
    context = (
        task.age_stage_code or "",
        task.costume_variant_code or "",
        _asset_context_variant(task.task_variant),
    )
    batch_versions: dict[UUID, int] = {}
    for batch_id in {item.batch_id for item in selected if item.batch_id}:
        batch = await session.get(TaskSubmissionBatch, batch_id)
        if batch is not None:
            batch_versions[batch_id] = batch.version_no
    card_master: AssetVersion | None = None
    for submission in selected:
        if submission.is_primary:
            for previous in existing:
                metadata = previous.metadata_json or {}
                previous_context = (
                    str(metadata.get("age_stage_code") or ""),
                    str(metadata.get("costume_variant_code") or ""),
                    _asset_context_variant(metadata.get("task_variant")),
                )
                if previous_context == context:
                    previous.is_current = False
        item = by_submission_id.get(str(submission.id))
        if item is not None:
            context_version_no = _safe_int((item.metadata_json or {}).get("context_version_no"), 0)
        else:
            context_version_no, _ = _asset_context_version(
                asset.asset_type,
                {
                    "task_variant": task.task_variant,
                    "age_stage_code": task.age_stage_code,
                    "costume_variant_code": task.costume_variant_code,
                    "batch_version_no": batch_versions.get(submission.batch_id) if submission.batch_id else None,
                },
                version_no + 1,
                context_version_counters,
            )
        metadata = {
            "submission_id": str(submission.id),
            "submission_status": submission.status,
            "is_primary": submission.is_primary,
            "view_label": submission.view_label,
            "state_label": submission.state_label,
            "submission_description": submission.description,
            "task_variant": task.task_variant,
            "age_stage_code": task.age_stage_code,
            "costume_variant_code": task.costume_variant_code,
            "step": submission.step,
            "file_type": submission.file_type,
            "batch_id": str(submission.batch_id) if submission.batch_id else None,
            "batch_version_no": batch_versions.get(submission.batch_id) if submission.batch_id else None,
            "task_id": str(task.id),
            "task_type": task.task_type.value,
            "media_type": task.media_type,
            "context_version_no": context_version_no if asset.asset_type == AssetType.character else None,
        }
        if item is None:
            version_no += 1
            base_code = _library_code(asset.asset_code, ASSET_TYPE_DISPLAY_CODES.get(asset.asset_type, "ASSET"))
            item = AssetVersion(
                asset_id=asset.id,
                version_no=version_no,
                asset_version_code=f"{base_code}-V{version_no:03d}",
                file_id=submission.file_id,
                preview_file_id=submission.file_id if submission.file_type == "image" else None,
                prompt_text=submission.revised_prompt_text or submission.prompt_text or task.latest_prompt_text,
                base_model=submission.model_name or task.production_model,
                tool_name=", ".join(submission.tool_names or []) or None,
                metadata_json=metadata,
                is_current=submission.is_primary,
                source_task_id=task.id,
                source_submission_id=submission.id,
                source_master_submission_id=(
                    submission.id
                    if asset.asset_type == AssetType.character
                    and (task.task_variant or "MASTER").upper() in {"A", "MASTER"}
                    else submission.source_master_submission_id
                ),
                created_by=created_by,
            )
            session.add(item)
            existing.append(item)
            by_submission_id[str(submission.id)] = item
        else:
            item.file_id = submission.file_id
            item.preview_file_id = submission.file_id if submission.file_type == "image" else item.preview_file_id
            item.metadata_json = metadata
            item.is_current = submission.is_primary
            item.source_submission_id = submission.id
            item.source_master_submission_id = (
                submission.id
                if asset.asset_type == AssetType.character
                and (task.task_variant or "MASTER").upper() in {"A", "MASTER"}
                else submission.source_master_submission_id
            )
            item.is_invalidated = False
            item.invalidated_at = None
            item.invalidated_by_id = None
            item.invalidated_reason = None
            item.invalidated_source_id = None
        # Character B-E views are versioned under the same asset but must not
        # replace the card-level master.  Scene/prop multi-angle submissions
        # have no required primary, so the first selected angle is used only
        # as the card preview while every selected angle remains a version.
        is_character_card_master = (
            asset.asset_type == AssetType.character
            and (task.task_variant or "MASTER").upper() in {"A", "MASTER"}
            and submission.is_primary
        )
        is_audio_card_master = task.task_type == TaskType.audio and submission.is_primary
        is_scene_prop_preview = (
            card_master is None
            and asset.asset_type in {AssetType.scene, AssetType.prop}
            and submission.is_selected
        )
        if is_character_card_master or is_audio_card_master or is_scene_prop_preview:
            card_master = item
            asset.file_path = submission.file_path
            asset.preview_path = submission.file_path if submission.file_type == "image" else asset.preview_path
    await session.flush()
    if card_master is not None:
        asset.current_version_id = card_master.id
    asset.version = max(asset.version, version_no)


def _asset_context_variant(value: Any) -> str:
    variant = str(value or "MASTER").strip().upper()
    return "MASTER" if variant in {"A", "MASTER"} else variant


async def _refresh_asset_completion_status(session: AsyncSession, asset_id: UUID, now: datetime) -> None:
    statuses = list((await session.scalars(select(Task.status).where(
        Task.asset_id == asset_id,
        Task.task_type.in_([TaskType.asset, TaskType.audio]),
        Task.is_retired.is_(False),
    ))).all())
    asset = await session.get(Asset, asset_id)
    if asset is None:
        return
    asset.status = "completed" if statuses and all(status == TaskStatus.completed for status in statuses) else "in_progress"
    asset.updated_at = now


async def _notify_unlocked_dependents(
    session: AsyncSession,
    source_task: Task,
    locked_scenes_before: set[str],
) -> None:
    dependent_ids = set((await session.scalars(select(Task.id).where(
        Task.depends_on_task_id == source_task.id,
        Task.is_retired.is_(False),
    ))).all())
    dependent_ids.update((await session.scalars(select(TaskDependency.task_id).where(
        TaskDependency.depends_on_task_id == source_task.id
    ))).all())
    if dependent_ids:
        dependents = list((await session.scalars(select(Task).where(
            Task.id.in_(dependent_ids),
            _active_asset_task_condition(),
        ))).all())
        for dependent in dependents:
            locked, _, _ = await _task_dependency_state(session, dependent)
            if not locked and dependent.assignee_id and dependent.status != TaskStatus.completed:
                session.add(Notification(
                    user_id=dependent.assignee_id,
                    title="任务已解锁",
                    content=f"前置成果已审核定版，可以开始：{dependent.title}",
                    type="task_unlocked",
                ))

    if source_task.task_type != TaskType.asset or not locked_scenes_before:
        return
    locked_scenes_after = {
        str(item.get("scene_code") or item.get("scene_name"))
        for item in await _scene_gating_core(session, source_task.project_id)
        if item.get("locked")
    }
    newly_unlocked = locked_scenes_before - locked_scenes_after
    if not newly_unlocked:
        return
    scene_tasks = list((await session.scalars(select(Task).where(
        Task.project_id == source_task.project_id,
        Task.storyboard_id.is_not(None),
        or_(Task.scene_code.in_(newly_unlocked), Task.scene_name.in_(newly_unlocked)),
        Task.status != TaskStatus.completed,
        Task.assignee_id.is_not(None),
        Task.is_retired.is_(False),
    ))).all())
    for scene_task in scene_tasks:
        session.add(Notification(
            user_id=scene_task.assignee_id,
            title="场景生产任务已解锁",
            content=f"场景资产已全部审核定版，可以开始：{scene_task.title}",
            type="task_unlocked",
        ))


async def get_dashboard(session: AsyncSession, project_id: UUID, user: User) -> DashboardSummary:
    project = await session.get(Project, project_id)
    if project is None:
        raise KeyError(project_id)
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    cache_key = f"cineforge:project-dashboard:v1:{project_id}"
    if settings.project_status_cache_enabled and is_director_or_admin(user):
        try:
            cached = await cache_json_get(cache_key)
            if cached is not None:
                return DashboardSummary.model_validate(cached)
        except Exception:
            pass
    tasks = (await session.execute(_visible_task_stmt(user).where(Task.project_id == project_id))).scalars().all()
    completed = [task for task in tasks if task.status == TaskStatus.completed]
    now = datetime.now(UTC)
    overdue = [task for task in tasks if task.status != TaskStatus.completed and task.due_at and task.due_at < now]
    if is_director_or_admin(user):
        storyboard_count = await session.scalar(select(func.count(Storyboard.id)).where(Storyboard.project_id == project_id)) or 0
    else:
        storyboard_count = len(await _visible_storyboard_ids_for_project(session, project_id, user))
    asset_count_stmt = select(func.count(Asset.id)).where(or_(Asset.project_id == project_id, Asset.project_id.is_(None)))
    asset_count = await session.scalar(asset_count_stmt) or 0
    dashboard = DashboardSummary(
        project_id=project_id,
        storyboard_count=storyboard_count,
        asset_count=asset_count,
        task_count=len(tasks),
        completed_task_count=len(completed),
        overdue_task_count=len(overdue),
        completion_rate=round(len(completed) / len(tasks), 2) if tasks else 0,
        recent_activity=["PostgreSQL 项目数据已启用", "DeepSeek Agent 提示词链路已接入"],
    )
    if settings.project_status_cache_enabled and is_director_or_admin(user):
        try:
            await cache_json_set(
                cache_key,
                dashboard.model_dump(mode="json"),
                settings.project_status_cache_ttl_seconds,
            )
        except Exception:
            pass
    return dashboard


async def get_board(session: AsyncSession, project_id: UUID, user: User) -> list[ProductionBoardRow]:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    storyboard_stmt = select(Storyboard).where(Storyboard.project_id == project_id).order_by(Storyboard.episode_num, Storyboard.order_num)
    task_stmt = _visible_task_stmt(user).options(
        selectinload(Task.submissions), selectinload(Task.submission_batches)
    ).where(Task.project_id == project_id)
    if not is_director_or_admin(user):
        storyboard_ids = await _visible_storyboard_ids_for_project(session, project_id, user)
        if not storyboard_ids:
            return []
        storyboard_stmt = storyboard_stmt.where(Storyboard.id.in_(storyboard_ids))
    storyboards = (await session.execute(storyboard_stmt)).scalars().all()
    tasks = (await session.execute(task_stmt)).scalars().all()
    by_storyboard: dict[UUID, list[TaskRead]] = {}
    submissions_by_storyboard: dict[UUID, list[SubmissionRead]] = {}
    for task in tasks:
        if task.storyboard_id:
            storyboard = next((item for item in storyboards if item.id == task.storyboard_id), None)
            by_storyboard.setdefault(task.storyboard_id, []).append(task_to_read(task, storyboard=storyboard))
            for submission in task.submissions:
                submissions_by_storyboard.setdefault(task.storyboard_id, []).append(submission_to_read(submission))
    return [
        ProductionBoardRow(
            storyboard_id=storyboard.id,
            episode_num=storyboard.episode_num,
            order_num=storyboard.order_num,
            title=storyboard.title,
            description=storyboard.description,
            characters=storyboard.characters or [],
            status=storyboard.status,
            tasks=by_storyboard.get(storyboard.id, []),
            submissions=submissions_by_storyboard.get(storyboard.id, []),
        )
        for storyboard in storyboards
    ]


def _lineage_summary(payload: dict[str, Any] | None) -> dict[str, Any]:
    if not isinstance(payload, dict):
        return {}
    return {
        key: payload[key]
        for key in ("episode_id", "episode_code", "script_version_id", "script_version_no", "workflow_run_id")
        if payload.get(key) not in (None, "", [], {})
    }


# P3 配对键：拆解各 step 的规范 skill 名。agent 原始候选(workflow_feedback_candidate)与
# 人工终版样本两侧按同一映射生成 lineage_key，使 agent↔human 差异可按 step+skill 配对。
_BREAKDOWN_STEP_SKILL = {
    "script_reading": "script-reading",
    "asset_extract": "asset-extract",
    "asset_prompt_generation": "asset-design-prompt",
    "asset_costume_design": "asset-design-prompt",
    "script_segmentation": "script-segmentation",
    "storyboard_breakdown": "script-breakdown",
    "script_breakdown": "script-breakdown",
}


def _training_lineage_key_episode(
    project_id: Any,
    episode_id: Any,
    script_version_id: Any,
    step: str,
    skill: str | None = None,
) -> str | None:
    """集+版本作用域配对键：p:{project}|e:{episode}|v:{version}|s:{step}|k:{skill}。

    缺 project/episode/version 任一则返回 None（不生成半截键，避免误配对）。
    """
    parts = [str(project_id or "").strip(), str(episode_id or "").strip(), str(script_version_id or "").strip()]
    if not all(parts):
        return None
    resolved_skill = (skill or _BREAKDOWN_STEP_SKILL.get(step) or step or "").strip()
    return f"p:{parts[0]}|e:{parts[1]}|v:{parts[2]}|s:{step}|k:{resolved_skill}"


def _training_lineage_key_asset(project_id: Any, asset_code: Any) -> str | None:
    """资产作用域配对键：项目级母版跨集复用，无 episode/version，用 asset_code 定位。

    p:{project}|a:{asset_code}|s:asset_extract|k:asset-extract。缺 project/asset_code 返回 None。
    """
    project = str(project_id or "").strip()
    code = str(asset_code or "").strip()
    if not project or not code:
        return None
    return f"p:{project}|a:{code}|s:asset_extract|k:asset-extract"


def _lineage_key_from_run_input(payload: dict[str, Any] | None, step: str, skill: str | None = None) -> str | None:
    """从 run.input / content.input_json 提取 episode/version 生成 episode 作用域配对键。"""
    if not isinstance(payload, dict):
        return None
    return _training_lineage_key_episode(
        payload.get("project_id"),
        payload.get("episode_id"),
        payload.get("script_version_id"),
        step,
        skill,
    )


def _payload_metadata(
    payload: dict[str, Any] | None,
    *,
    lineage: dict[str, Any] | None = None,
) -> tuple[dict[str, Any], int | None, str | None]:
    if not isinstance(payload, dict):
        return dict(lineage or {}), None, None
    encoded = json.dumps(payload, ensure_ascii=False, default=str, separators=(",", ":")).encode("utf-8")
    summary: dict[str, Any] = {}
    for key in ("skill", "status", "risk_level", "source_label"):
        if payload.get(key) not in (None, "", [], {}):
            summary[key] = payload[key]
    workflow = payload.get("workflow")
    if isinstance(workflow, dict) and workflow.get("status"):
        summary["workflow_status"] = workflow["status"]
    for key in ("assets", "storyboards", "script_segments", "characters", "scenes", "props", "errors", "warnings", "manual_review_items"):
        value = payload.get(key)
        if isinstance(value, list):
            summary[f"{key}_count"] = len(value)
    return {**(lineage or {}), **summary}, len(encoded), hashlib.sha256(encoded).hexdigest()


def _apply_agent_run_payload_metadata(
    run: AgentRun,
    *,
    input_payload: dict[str, Any] | None,
    output_payload: dict[str, Any] | None,
) -> None:
    summary, size, digest = _payload_metadata(output_payload, lineage=_lineage_summary(input_payload))
    run.summary_json = summary
    run.payload_size_bytes = size
    run.payload_sha256 = digest


def _apply_workflow_payload_metadata(workflow: WorkflowRun) -> None:
    summary, size, digest = _payload_metadata(
        workflow.result_json,
        lineage=_lineage_summary(workflow.input_json),
    )
    workflow.summary_json = summary
    workflow.payload_size_bytes = size
    workflow.payload_sha256 = digest


def _agent_summary(run: AgentRun) -> dict[str, Any]:
    return dict(run.summary_json or {})


def agent_run_to_read(run: AgentRun, *, include_payload: bool = True) -> AgentRunRead:
    # Extract workflow_run_id from input if present
    workflow_run_id = run.workflow_run_id
    if include_payload and run.input_json and isinstance(run.input_json.get("workflow_run_id"), str):
        try:
            workflow_run_id = UUID(run.input_json["workflow_run_id"])
        except (ValueError, TypeError):
            pass

    summary = _agent_summary(run)
    hydrated: dict[str, Any] = {}
    if include_payload and run.payload_ref:
        hydrated = load_json_payload(run.payload_ref)
    input_payload = hydrated.get("input") if isinstance(hydrated.get("input"), dict) else run.input_json or {}
    output_payload = hydrated.get("output") if isinstance(hydrated.get("output"), dict) else run.output_json
    return AgentRunRead(
        id=run.id,
        project_id=run.project_id,
        parent_run_id=run.parent_run_id,
        storyboard_id=run.storyboard_id,
        task_id=run.task_id,
        agent_type=run.agent_type,
        status=run.status,
        input=input_payload if include_payload else {},
        output=output_payload if include_payload else None,
        error_message=run.error_message,
        duration_ms=run.duration_ms,
        token_usage=(hydrated.get("token_usage") if hydrated else run.token_usage) if include_payload else None,
        feedback_json=(hydrated.get("feedback") if hydrated else run.feedback_json) if include_payload else None,
        model=run.model,
        skill_name=run.skill_name,
        skill_version=run.skill_version,
        contract_version=run.contract_version,
        contract_hash=run.contract_hash,
        prompt_version=run.prompt_version,
        input_hash=run.input_hash,
        node_key=run.node_key,
        workflow_run_id=workflow_run_id,
        summary=summary,
        payload_ref=run.payload_ref,
        payload_size_bytes=run.payload_size_bytes,
        payload_sha256=run.payload_sha256,
        data_state=run.data_state or "agent_raw",
        created_at=run.created_at,
        updated_at=run.updated_at,
    )


def workflow_run_to_read(workflow: WorkflowRun, *, include_payload: bool = True) -> WorkflowRunRead:
    hydrated: dict[str, Any] = {}
    if include_payload and workflow.payload_ref:
        hydrated = load_json_payload(workflow.payload_ref)
    full_output = hydrated.get("output") if isinstance(hydrated.get("output"), dict) else {}
    external_result = full_output.get("breakdown_view") if isinstance(full_output.get("breakdown_view"), dict) else full_output
    result_json = ({**external_result, **(workflow.result_json or {})} if hydrated else (workflow.result_json or {})) if include_payload else {}
    input_json = hydrated.get("input") if isinstance(hydrated.get("input"), dict) else workflow.input_json or {}
    hydrated_errors = {
        "errors": full_output.get("errors") or [],
        "warnings": full_output.get("warnings") or [],
        "validation_report": full_output.get("validation_report") or {},
    } if hydrated else {}
    materialize_meta = result_json.get("materialize") if isinstance(result_json.get("materialize"), dict) else {}
    materialized = workflow.status == "materialized" or materialize_meta.get("status") == "succeeded"
    has_result = bool(result_json) if include_payload else workflow.status in {"succeeded", "needs_review", "materialized"}
    terminal_for_retry = workflow.status in {"failed", "needs_review"}
    return WorkflowRunRead(
        id=workflow.id,
        project_id=workflow.project_id,
        agent_run_id=workflow.agent_run_id,
        workflow_type=workflow.workflow_type,
        status=workflow.status,
        node_status=(workflow.node_status_json or {}) if include_payload else {},
        result=result_json,
        errors={**hydrated_errors, **(workflow.error_json or {})} if include_payload else {},
        input=input_json if include_payload else {},
        can_retry=terminal_for_retry and not materialized,
        can_rerun=False,
        can_materialize=has_result and not materialized and workflow.status in {"succeeded", "needs_review"},
        materialized=materialized,
        summary=dict(workflow.summary_json or {}),
        payload_ref=workflow.payload_ref,
        payload_size_bytes=workflow.payload_size_bytes,
        payload_sha256=workflow.payload_sha256,
        created_by=workflow.created_by,
        created_at=workflow.created_at,
        updated_at=workflow.updated_at,
    )


def agent_run_to_summary(run: AgentRun) -> AgentRunSummaryRead:
    summary = _agent_summary(run)
    workflow_run_id = run.workflow_run_id
    if workflow_run_id is None and summary.get("workflow_run_id"):
        try:
            workflow_run_id = UUID(str(summary["workflow_run_id"]))
        except (ValueError, TypeError):
            workflow_run_id = None
    return AgentRunSummaryRead(
        id=run.id,
        project_id=run.project_id,
        parent_run_id=run.parent_run_id,
        storyboard_id=run.storyboard_id,
        task_id=run.task_id,
        agent_type=run.agent_type,
        status=run.status,
        error_message=run.error_message,
        duration_ms=run.duration_ms,
        model=run.model,
        skill_name=run.skill_name,
        skill_version=run.skill_version,
        contract_version=run.contract_version,
        contract_hash=run.contract_hash,
        prompt_version=run.prompt_version,
        input_hash=run.input_hash,
        node_key=run.node_key,
        workflow_run_id=workflow_run_id,
        episode_id=summary.get("episode_id"),
        episode_code=summary.get("episode_code"),
        script_version_id=summary.get("script_version_id"),
        summary=summary,
        payload_ref=run.payload_ref,
        payload_size_bytes=run.payload_size_bytes,
        payload_sha256=run.payload_sha256,
        created_at=run.created_at,
        updated_at=run.updated_at,
    )


def workflow_run_to_summary(workflow: WorkflowRun) -> WorkflowRunSummaryRead:
    summary = dict(workflow.summary_json or {})
    materialized = workflow.status == "materialized"
    terminal_for_retry = workflow.status in {"failed", "needs_review"}
    return WorkflowRunSummaryRead(
        id=workflow.id,
        project_id=workflow.project_id,
        agent_run_id=workflow.agent_run_id,
        workflow_type=workflow.workflow_type,
        status=workflow.status,
        can_retry=terminal_for_retry and not materialized,
        can_rerun=False,
        can_materialize=workflow.status in {"succeeded", "needs_review"} and not materialized,
        materialized=materialized,
        created_by=workflow.created_by,
        episode_id=summary.get("episode_id"),
        episode_code=summary.get("episode_code"),
        script_version_id=summary.get("script_version_id"),
        summary=summary,
        payload_ref=workflow.payload_ref,
        payload_size_bytes=workflow.payload_size_bytes,
        payload_sha256=workflow.payload_sha256,
        created_at=workflow.created_at,
        updated_at=workflow.updated_at,
    )


def _project_no() -> str:
    return f"RF-{datetime.now(UTC).strftime('%Y%m%d')}-{uuid.uuid4().hex[:6].upper()}"


def is_director_or_admin(user: User) -> bool:
    return user.role in {UserRole.director, UserRole.admin}


_INACTIVE_TASK_ASSET_STATUSES = ("deleted", "excluded")


def _active_asset_task_condition():
    active_asset_ids = select(Asset.id).where(Asset.status.notin_(_INACTIVE_TASK_ASSET_STATUSES))
    return and_(
        Task.is_retired.is_(False),
        or_(Task.asset_id.is_(None), Task.asset_id.in_(active_asset_ids)),
    )


async def _get_active_asset_task(
    session: AsyncSession,
    task_id: UUID,
    *,
    with_for_update: bool = False,
) -> Task | None:
    get_options = {"with_for_update": True} if with_for_update else {}
    task = await session.get(Task, task_id, **get_options)
    if task is None or task.is_retired:
        return None
    if task.asset_id is None:
        return task
    asset_status = await session.scalar(select(Asset.status).where(Asset.id == task.asset_id))
    if asset_status is None or str(asset_status) in _INACTIVE_TASK_ASSET_STATUSES:
        return None
    return task


def _visible_task_stmt(user: User):
    stmt = select(Task).where(_active_asset_task_condition(), Task.variant_plan_id.is_(None))
    if not is_director_or_admin(user):
        now = datetime.now(UTC)
        project_permission_ids = select(Permission.project_id).where(
            Permission.user_id == user.id,
            Permission.project_id.is_not(None),
            Permission.permission_type.like(f"{PROJECT_ROLE_PREFIX}%"),
        )
        stmt = stmt.where(
            or_(
                Task.project_id.in_(project_permission_ids),
                (
                    (Task.assignee_id == user.id)
                    & or_(Task.status != TaskStatus.completed, Task.visible_until.is_(None), Task.visible_until >= now)
                ),
            )
        )
    return stmt


async def _visible_task_project_ids(session: AsyncSession, user: User) -> list[UUID]:
    result = await session.execute(_visible_task_stmt(user).with_only_columns(Task.project_id).distinct())
    return list(result.scalars().all())


async def _project_permission_ids(session: AsyncSession, user: User) -> list[UUID]:
    result = await session.execute(
        select(Permission.project_id)
        .where(
            Permission.user_id == user.id,
            Permission.project_id.is_not(None),
            Permission.permission_type.like(f"{PROJECT_ROLE_PREFIX}%"),
        )
        .distinct()
    )
    return [item for item in result.scalars().all() if item is not None]


async def _visible_project_ids(session: AsyncSession, user: User) -> list[UUID]:
    return list({*await _visible_task_project_ids(session, user), *await _project_permission_ids(session, user)})


async def _visible_storyboard_ids_for_project(session: AsyncSession, project_id: UUID, user: User) -> list[UUID]:
    if is_director_or_admin(user) or await _has_project_permission(session, project_id, user):
        result = await session.execute(select(Storyboard.id).where(Storyboard.project_id == project_id))
    else:
        result = await session.execute(
            _visible_task_stmt(user)
            .where(Task.project_id == project_id, Task.storyboard_id.is_not(None))
            .with_only_columns(Task.storyboard_id)
            .distinct()
        )
    return [item for item in result.scalars().all() if item is not None]


async def _can_access_project(session: AsyncSession, project_id: UUID, user: User) -> bool:
    if is_director_or_admin(user):
        return True
    if await _has_project_permission(session, project_id, user):
        return True
    result = await session.execute(_visible_task_stmt(user).where(Task.project_id == project_id).limit(1))
    return result.scalar_one_or_none() is not None


async def can_review_project_tasks(session: AsyncSession, project_id: UUID, user: User) -> bool:
    return user.role in {UserRole.director, UserRole.admin}


async def _has_project_permission(session: AsyncSession, project_id: UUID, user: User) -> bool:
    return (
        await session.scalar(
            select(Permission.id)
            .where(
                Permission.user_id == user.id,
                Permission.project_id == project_id,
                Permission.permission_type.like(f"{PROJECT_ROLE_PREFIX}%"),
            )
            .limit(1)
        )
        is not None
    )


async def _project_member_role(session: AsyncSession, project_id: UUID, user: User) -> ProjectMemberRole | None:
    permission_type = await session.scalar(
        select(Permission.permission_type)
        .where(
            Permission.user_id == user.id,
            Permission.project_id == project_id,
            Permission.permission_type.like(f"{PROJECT_ROLE_PREFIX}%"),
        )
        .order_by(Permission.created_at)
        .limit(1)
    )
    if not permission_type:
        return None
    try:
        return ProjectMemberRole(str(permission_type).split(":", 1)[1])
    except ValueError:
        return None


async def _can_manage_project_members(session: AsyncSession, project_id: UUID, user: User) -> bool:
    if is_director_or_admin(user):
        return True
    return await _project_member_role(session, project_id, user) in PROJECT_MANAGE_ROLES


async def _can_write_project(session: AsyncSession, project_id: UUID, user: User) -> bool:
    if is_director_or_admin(user):
        return True
    return await _project_member_role(session, project_id, user) in PROJECT_WRITE_ROLES


async def _can_update_task(session: AsyncSession, task: Task, user: User) -> bool:
    return task.assignee_id == user.id or await _can_write_project(session, task.project_id, user)


async def _verify_admin_password(session: AsyncSession, admin_password: str, user: User) -> None:
    if user.role == UserRole.admin and verify_password(admin_password, user.password_hash):
        return
    result = await session.execute(select(User).where(User.role == UserRole.admin, User.is_active.is_(True)))
    for admin in result.scalars().all():
        if verify_password(admin_password, admin.password_hash):
            return
    raise PermissionError("admin_password")


def _breakdown_content_from_request(
    project: Project,
    data: BreakdownSaveRequest,
    user: User,
    *,
    source_label: str | None = None,
    reserved_asset_codes: set[str] | None = None,
    previous_assets: list[dict[str, Any]] | None = None,
    previous_output: dict[str, Any] | None = None,
) -> dict[str, Any]:
    view = copy.deepcopy(data.view or {})
    _validate_breakdown_view_for_save(view)
    raw_assets = list(view.get("assets") or [])
    inventory = AssetNormalizationResultV1.model_validate({
        "normalization_version": view.get("normalization_version"),
        "assets": raw_assets,
        "global_review_items": view.get("global_review_items") or [],
    })
    normalized_view = _clean_normalized_workflow_view(view)
    normalized_view.pop("assets", None)
    content: dict[str, Any] = {
        **normalized_view,
        "normalization_version": inventory.normalization_version,
        "source_label": source_label or data.source_label or "human_modified",
        "project_id": str(project.id),
        "project_no": project.project_no,
        "project_prefix": project.project_prefix,
        "episode_code": _normalize_episode_code(data.episode_code or view.get("episode_code")),
        "episode_id": str(data.episode_id or view.get("episode_id")) if data.episode_id or view.get("episode_id") else None,
        "script_version_id": str(data.script_version_id or view.get("script_version_id")) if data.script_version_id or view.get("script_version_id") else None,
        "script_segments": list(view.get("script_segments") or []),
        "storyboards": _sorted_storyboards(list(view.get("storyboards") or [])),
        "assets": [asset.model_dump(mode="json") for asset in inventory.assets],
        "global_review_items": [item.model_dump(mode="json") for item in inventory.global_review_items],
        "change_summary": data.change_summary or [],
        "edited_by": str(user.id),
        "edited_at": datetime.now(UTC).isoformat(),
    }
    # Inherit agent-derived display fields from previous_output when not present in the new view
    if previous_output:
        for key in (
            "asset_prompt_designs",
            "asset_prompt_processing_summary",
            "costume_designs",
            "costume_processing_summary",
            "asset_prompt_finalization",
        ):
            if key not in content and key in previous_output:
                content[key] = previous_output[key]
    return content


async def _append_confirmed_asset_inventory_revision(
    session: AsyncSession,
    project: Project,
    content: dict[str, Any],
    user: User,
) -> ArtifactRevision | None:
    review_state = content.get("asset_review_state") if isinstance(content.get("asset_review_state"), dict) else {}
    if str(review_state.get("status") or "") != "confirmed":
        return None
    confirmed_revision_id = _uuid_or_none(review_state.get("confirmed_revision_id"))
    if confirmed_revision_id is None and not _is_explicit_human_confirmation(review_state):
        content["asset_review_state"] = _pending_confirmation_state()
        return None
    inventory = AssetNormalizationResultV1.model_validate({
        "normalization_version": content.get("normalization_version"),
        "assets": content.get("assets") or [],
        "global_review_items": content.get("global_review_items") or [],
    })
    if not inventory.assets:
        return None
    episode_id = content.get("episode_id")
    script_version_id = content.get("script_version_id")
    if not episode_id or not script_version_id:
        raise ValueError("资产确认稿缺少分集或剧本版本来源，不能保存正式资产清单。")
    formal_codes = await _formal_asset_code_mapping(session, project, inventory)
    formal_inventory = formalize_asset_inventory_v1(inventory, formal_codes)
    assets = [asset.model_dump(mode="json") for asset in formal_inventory.assets]
    content["assets"] = assets
    scope = script_version_id or episode_id or project.id
    artifact_id = uuid.uuid5(uuid.NAMESPACE_URL, f"cineforge:asset_inventory:{project.id}:{scope}")
    source_run_id = _uuid_or_none(content.get("agent_run_id"))
    source_run = await session.get(AgentRun, source_run_id) if source_run_id is not None else None
    current = await session.scalar(
        select(ArtifactRevision)
        .where(
            ArtifactRevision.artifact_type == "asset_inventory",
            ArtifactRevision.artifact_id == artifact_id,
        )
        .order_by(ArtifactRevision.version_no.desc())
        .limit(1)
    )
    normalized_content = {
        "status": "confirmed",
        "assets": assets,
        "global_review_items": [item.model_dump(mode="json") for item in formal_inventory.global_review_items],
    }
    digest = _content_hash(normalized_content)
    if current is not None and current.source_type == "human" and current.content_hash == digest:
        content["asset_review_state"] = {
            "status": "confirmed",
            "confirmed_revision_id": str(current.id),
            "confirmation_mode": "human_confirmed",
        }
        return current
    input_snapshot = {
        "project_id": str(project.id),
        "episode_id": str(episode_id) if episode_id else None,
        "script_version_id": str(script_version_id) if script_version_id else None,
        "confirmed_reading_revision_id": _lineage_text(
            review_state.get("confirmed_reading_revision_id"),
            (content.get("reading_review_state") or {}).get("confirmed_revision_id")
            if isinstance(content.get("reading_review_state"), dict) else None,
        ),
        **_project_profile_hashes(project),
    }
    if source_run is None:
        source_run_id = None
    revision = ArtifactRevision(
        project_id=project.id,
        artifact_type="asset_inventory",
        artifact_id=artifact_id,
        version_no=(current.version_no if current else 0) + 1,
        parent_revision_id=current.id if current else None,
        source_type="human",
        source_run_id=source_run_id,
        skill_name="asset-extract",
        skill_version=_lineage_text(review_state.get("skill_version")),
        contract_version=_lineage_text(review_state.get("contract_version")),
        contract_hash=_lineage_text(review_state.get("contract_hash")),
        prompt_version=_lineage_text(review_state.get("prompt_version")),
        input_hash=_lineage_text(review_state.get("input_hash"), _content_hash(input_snapshot)),
        input_snapshot=input_snapshot,
        raw_output={},
        normalized_content=normalized_content,
        change_diff={
            "previous_content_hash": current.content_hash if current else None,
            "confirmation": "human_confirmed",
        },
        content_hash=digest,
        status="confirmed",
        created_by=user.id,
        data_state="final",
    )
    session.add(revision)
    await session.flush()
    content["asset_review_state"] = {
        "status": "confirmed",
        "confirmed_revision_id": str(revision.id),
        "confirmation_mode": "human_confirmed",
    }
    return revision


async def _formal_asset_code_mapping(
    session: AsyncSession,
    project: Project,
    inventory: AssetNormalizationResultV1,
) -> dict[str, str]:
    prefix = _safe_code_prefix(project.project_prefix or project.project_no)
    rows = list((await session.scalars(select(Asset).where(Asset.project_id == project.id))).all())
    by_client_key = {
        str((row.metadata_json or {}).get("client_asset_key")): row
        for row in rows
        if (row.metadata_json or {}).get("client_asset_key")
    }
    by_code = {str(row.asset_code or "").strip().upper(): row for row in rows if row.asset_code}
    occupied = set(by_code)
    counters = {"character": 0, "scene": 0, "prop": 0}
    for code in occupied:
        code_type, sequence = _formal_asset_code_parts(code, prefix=prefix)
        if code_type:
            counters[code_type] = max(counters[code_type], sequence)

    mapping: dict[str, str] = {}
    for asset in inventory.assets:
        existing = by_client_key.get(asset.client_asset_key)
        if existing is not None:
            mapping[asset.client_asset_key] = str(existing.asset_code)
            continue
        candidate = _canonical_formal_asset_code(
            asset.asset_code,
            prefix=prefix,
            asset_type=asset.asset_type,
        )
        candidate_row = by_code.get(str(candidate or ""))
        if candidate and (
            candidate_row is None
            or (
                str(candidate_row.name or "").strip().casefold() == asset.name.strip().casefold()
                and _normalize_asset_type(candidate_row.asset_type.value) == asset.asset_type
            )
        ):
            mapping[asset.client_asset_key] = candidate
            occupied.add(candidate)
            _, sequence = _formal_asset_code_parts(candidate, prefix=prefix)
            counters[asset.asset_type] = max(counters[asset.asset_type], sequence)
            continue
        code_prefix = _TYPE_PREFIX_BY_ASSET_TYPE[asset.asset_type]
        while True:
            counters[asset.asset_type] += 1
            formal_code = f"{prefix}-{code_prefix}{counters[asset.asset_type]:03d}"
            if formal_code not in occupied:
                break
        occupied.add(formal_code)
        mapping[asset.client_asset_key] = formal_code
    return mapping


_TYPE_PREFIX_BY_ASSET_TYPE = {"character": "R", "scene": "SC", "prop": "P"}


async def _inherit_confirmed_asset_inventory(
    session: AsyncSession,
    project: Project,
    content: dict[str, Any],
) -> None:
    """Inherit only an unchanged scoped final inventory into the clean view."""
    episode_id = str(content.get("episode_id") or "").strip()
    script_version_id = str(content.get("script_version_id") or "").strip()
    if not episode_id or not script_version_id:
        return
    scope = script_version_id or episode_id or project.id
    artifact_id = uuid.uuid5(uuid.NAMESPACE_URL, f"cineforge:asset_inventory:{project.id}:{scope}")
    previous = await session.scalar(
        select(ArtifactRevision)
        .where(
            ArtifactRevision.project_id == project.id,
            ArtifactRevision.artifact_type == "asset_inventory",
            ArtifactRevision.artifact_id == artifact_id,
            ArtifactRevision.source_type == "human",
            ArtifactRevision.status == "confirmed",
        )
        .order_by(ArtifactRevision.version_no.desc())
        .limit(1)
    )
    if previous is None:
        return
    snapshot = previous.input_snapshot or {}
    if (
        str(snapshot.get("episode_id") or "") != episode_id
        or str(snapshot.get("script_version_id") or "") != script_version_id
    ):
        return

    previous_content = dict(previous.normalized_content or {})
    previous_inventory = AssetNormalizationResultV1.model_validate({
        "normalization_version": "AssetNormalization.v1",
        "assets": previous_content.get("assets") or [],
        "global_review_items": previous_content.get("global_review_items") or [],
    })
    current_inventory = AssetNormalizationResultV1.model_validate({
        "normalization_version": content.get("normalization_version"),
        "assets": content.get("assets") or [],
        "global_review_items": content.get("global_review_items") or [],
    })
    if not previous_inventory.assets or not current_inventory.assets:
        return
    review_state = content.get("asset_review_state") if isinstance(content.get("asset_review_state"), dict) else {}
    confirmed_revision_id = _uuid_or_none(review_state.get("confirmed_revision_id"))
    if _is_explicit_human_confirmation(review_state):
        return
    if str(review_state.get("status") or "") == "confirmed" and confirmed_revision_id is None:
        content["asset_review_state"] = _pending_confirmation_state()
        return
    if confirmed_revision_id is not None:
        requested_revision = await session.get(ArtifactRevision, confirmed_revision_id)
        if (
            requested_revision is None
            or requested_revision.artifact_type != "asset_inventory"
            or requested_revision.project_id != project.id
            or requested_revision.status != "confirmed"
        ):
            content["asset_review_state"] = _pending_confirmation_state()
            return
        previous = requested_revision
        previous_content = dict(previous.normalized_content or {})
        previous_inventory = AssetNormalizationResultV1.model_validate({
            "normalization_version": "AssetNormalization.v1",
            "assets": previous_content.get("assets") or [],
            "global_review_items": previous_content.get("global_review_items") or [],
        })
    previous_codes = {
        asset.client_asset_key: asset.asset_code for asset in previous_inventory.assets
    }
    try:
        comparable_current = formalize_asset_inventory_v1(current_inventory, previous_codes)
    except ValueError:
        content["asset_review_state"] = _pending_confirmation_state()
        return
    if comparable_current.assets != previous_inventory.assets:
        content["asset_review_state"] = _pending_confirmation_state()
        return
    content["assets"] = [asset.model_dump(mode="json") for asset in previous_inventory.assets]
    content["global_review_items"] = [
        item.model_dump(mode="json") for item in previous_inventory.global_review_items
    ]
    content["asset_review_state"] = {
        **review_state,
        "status": "confirmed",
        "confirmed_revision_id": str(previous.id),
        "confirmation_mode": "inherited_unchanged_revision",
    }


async def _sync_confirmed_inventory_bindings(
    session: AsyncSession,
    project: Project,
    content: dict[str, Any],
    inventory_revision: ArtifactRevision,
) -> None:
    episode_id = _uuid_or_none(content.get("episode_id"))
    script_version_id = _uuid_or_none(content.get("script_version_id"))
    reading_revision_id = _uuid_or_none((inventory_revision.input_snapshot or {}).get("confirmed_reading_revision_id"))
    if episode_id is None or script_version_id is None or reading_revision_id is None:
        raise ValueError("资产确认稿缺少围读终版、分集或剧本版本修订来源。")
    await session.execute(
        update(EpisodeAssetBinding)
        .where(
            EpisodeAssetBinding.project_id == project.id,
            EpisodeAssetBinding.episode_id == episode_id,
            EpisodeAssetBinding.script_version_id == script_version_id,
            EpisodeAssetBinding.application_revision_id != inventory_revision.id,
            EpisodeAssetBinding.status == "active",
        )
        .values(status="superseded", updated_at=datetime.now(UTC))
    )
    assets_by_code = {
        str(item.asset_code or "").strip(): item
        for item in (await session.scalars(select(Asset).where(
            Asset.project_id == project.id,
            Asset.asset_type.in_(_VISUAL_ASSET_TYPES),
            Asset.status.notin_(["deleted", "excluded"]),
        ))).all()
        if str(item.asset_code or "").strip()
    }
    inventory = dict(inventory_revision.normalized_content or {})
    for index, item in enumerate(inventory.get("assets") or []):
        if not isinstance(item, dict):
            continue
        asset = assets_by_code.get(str(item.get("asset_code") or "").strip())
        if asset is None:
            raise ValueError(f"资产 {item.get('asset_code') or item.get('name') or index + 1} 未落库，不能建立分集绑定。")
        binding = await session.scalar(select(EpisodeAssetBinding).where(
            EpisodeAssetBinding.application_revision_id == inventory_revision.id,
            EpisodeAssetBinding.proposal_index == index,
        ))
        values = {
            "project_id": project.id,
            "episode_id": episode_id,
            "script_version_id": script_version_id,
            "confirmed_reading_revision_id": reading_revision_id,
            "application_revision_id": inventory_revision.id,
            "asset_id": asset.id,
            "asset_revision_id": asset.current_revision_id,
            "proposal_index": index,
            "action": str(item.get("resolution_action") or item.get("match_action") or "confirmed"),
            "status": "active",
            "age_stage_code": None,
            "costume_variant_code": None,
            "proposal_json": item,
        }
        if binding is None:
            session.add(EpisodeAssetBinding(**values))
        else:
            for key, value in values.items():
                setattr(binding, key, value)
    await session.flush()


async def _append_reading_report_revisions(
    session: AsyncSession,
    project: Project,
    content: dict[str, Any],
    user: User,
) -> None:
    reading_report = content.get("reading_report")
    review_state = content.get("reading_review_state") if isinstance(content.get("reading_review_state"), dict) else {}
    if not isinstance(reading_report, dict) or str(review_state.get("status") or "") != "confirmed":
        return
    confirmed_report = _normalize_confirmed_reading_candidate_codes(reading_report)
    confirmed_revision_id = _uuid_or_none(review_state.get("confirmed_revision_id"))
    explicit_confirmation = _is_explicit_human_confirmation(review_state)
    if confirmed_revision_id is not None:
        previous = await session.get(ArtifactRevision, confirmed_revision_id)
        if (
            previous is None
            or previous.project_id != project.id
            or previous.artifact_type != "reading_report"
            or previous.status != "confirmed"
            or _content_hash(previous.normalized_content or {}) != _content_hash(confirmed_report)
        ):
            content["reading_review_state"] = _pending_confirmation_state()
        return
    if not explicit_confirmation:
        content["reading_review_state"] = _pending_confirmation_state()
        return
    normalized_report = confirmed_report
    content["reading_report"] = confirmed_report

    episode_code = _normalize_episode_code(content.get("episode_code")) or "EP01"
    requested_episode_id = _uuid_or_none(content.get("episode_id"))
    requested_version_id = _uuid_or_none(content.get("script_version_id"))
    episode = await session.get(ProjectEpisode, requested_episode_id) if requested_episode_id else await session.scalar(
        select(ProjectEpisode).where(
            ProjectEpisode.project_id == project.id,
            ProjectEpisode.episode_code == episode_code,
        )
    )
    script_version = await session.get(ScriptVersion, requested_version_id) if requested_version_id else None
    script = await session.get(Script, script_version.script_id) if script_version else (
        await session.scalar(select(Script).where(Script.project_id == project.id, Script.episode_id == episode.id))
        if episode else None
    )
    if script_version is None and script and script.current_version_id:
        script_version = await session.get(ScriptVersion, script.current_version_id)
    if episode is None or episode.project_id != project.id or script is None or script.episode_id != episode.id:
        raise ValueError("围读确认稿的分集与剧本版本来源不匹配。")
    if script_version is None or script_version.script_id != script.id:
        raise ValueError("围读确认稿缺少有效的剧本版本来源。")
    artifact_seed = str(script_version.id) if script_version else f"{project.id}:{episode_code}"
    artifact_id = uuid.uuid5(uuid.NAMESPACE_URL, f"cineforge:reading-report:{artifact_seed}")
    input_snapshot = {
        "project_id": str(project.id),
        "episode_id": str(episode.id) if episode else None,
        "episode_code": episode_code,
        "script_id": str(script.id) if script else None,
        "script_version_id": str(script_version.id) if script_version else None,
        "script_version_no": script_version.version_no if script_version else None,
        "content_hash": script_version.content_hash if script_version else None,
        "style_catalog_version": (project.production_brief or {}).get("style_catalog_version"),
        "primary_style_id": (project.production_brief or {}).get("primary_style_id"),
        "production_brief": project.production_brief or {},
        "episode_production_brief_hash": _content_hash(
            episode.production_brief or {}
        ),
        **_project_profile_hashes(project),
    }
    revisions = (
        await session.execute(
            select(ArtifactRevision)
            .where(ArtifactRevision.artifact_type == "reading_report", ArtifactRevision.artifact_id == artifact_id)
            .order_by(ArtifactRevision.version_no)
        )
    ).scalars().all()
    source_run = await _reading_source_agent_run(session, project.id, script, script_version)
    source_run_read = agent_run_to_read(source_run) if source_run else None
    agent_output = dict(source_run_read.output or {}) if source_run_read and isinstance(source_run_read.output, dict) else {"reading_report": normalized_report}

    async def append_revision(
        *,
        source_type: str,
        payload: dict[str, Any],
        status: str,
        raw_payload: dict[str, Any] | None = None,
        hash_payload: dict[str, Any] | None = None,
    ) -> ArtifactRevision:
        nonlocal revisions
        digest = _content_hash(hash_payload or payload)
        same = next((item for item in reversed(revisions) if item.source_type == source_type and item.content_hash == digest), None)
        if same is not None:
            return same
        parent = revisions[-1] if revisions else None
        revision = ArtifactRevision(
            project_id=project.id,
            artifact_type="reading_report",
            artifact_id=artifact_id,
            version_no=(parent.version_no if parent else 0) + 1,
            parent_revision_id=parent.id if parent else None,
            source_type=source_type,
            source_run_id=source_run.id if source_run else None,
            skill_name="script-reading",
            skill_version=str(agent_output.get("skill_version") or "") or None,
            model_name=source_run.model if source_run else None,
            prompt_version=str(agent_output.get("prompt_version") or "") or None,
            input_snapshot=input_snapshot,
            raw_output=raw_payload or {},
            normalized_content=payload,
            change_diff={
                "previous_content_hash": parent.content_hash if parent else None,
                "source_transition": f"{parent.source_type if parent else 'none'}->{source_type}",
            },
            content_hash=digest,
            status=status,
            created_by=user.id if source_type == "human" else None,
            data_state={"agent": "agent_raw", "system": "normalized", "human": "final"}[source_type],
            payload_ref=source_run.payload_ref if source_type == "agent" and source_run else None,
            payload_size_bytes=source_run.payload_size_bytes if source_type == "agent" and source_run else None,
            payload_sha256=source_run.payload_sha256 if source_type == "agent" and source_run else None,
        )
        if source_type == "agent" and source_run and source_run.payload_ref:
            revision.raw_output = {
                "summary": dict(source_run.summary_json or {}),
                "payload_externalized": True,
            }
        session.add(revision)
        await session.flush()
        revisions.append(revision)
        return revision

    await append_revision(
        source_type="agent",
        payload=normalized_report,
        status="agent_raw",
        hash_payload=agent_output,
    )
    normalized_revision = await append_revision(source_type="system", payload=normalized_report, status="normalized")
    confirmed_revision = await append_revision(source_type="human", payload=confirmed_report, status="confirmed")
    if episode:
        episode.summary = _reading_summary(confirmed_report)
        episode.updated_at = datetime.now(UTC)
    existing_sample = await session.scalar(
        select(AgentTrainingSample.id).where(
            AgentTrainingSample.sample_type == "reading_report_confirmed",
            AgentTrainingSample.entity_code == str(confirmed_revision.id),
        )
    )
    if existing_sample is None:
        changed_fields = sorted(
            key for key in set(normalized_report) | set(confirmed_report)
            if normalized_report.get(key) != confirmed_report.get(key)
        )
        externalized = bool(source_run and source_run.payload_ref)
        session.add(
            AgentTrainingSample(
                project_id=project.id,
                agent_type=AgentKind.script_breakdown,
                sample_type="reading_report_confirmed",
                entity_type="reading_report",
                entity_code=str(confirmed_revision.id),
                input_json=_lineage_summary(source_run_read.input) if externalized and source_run_read else input_snapshot,
                agent_output_json={"summary": dict(source_run.summary_json or {})} if externalized and source_run else agent_output,
                human_modified_output_json={"content_hash": _content_hash(confirmed_report), "changed_fields": changed_fields},
                confirmed_output_json={"content_hash": _content_hash(confirmed_report), "status": "confirmed"},
                change_summary=[f"围读报告人工确认；顶层变更字段：{'、'.join(changed_fields) or '无'}"],
                training_tags=["script-reading", "human_confirmed", "version_control"],
                training_ready=True,
                lineage_key=_lineage_key_from_run_input(
                    (source_run_read.input if source_run_read else None) or input_snapshot,
                    "script_reading",
                ),
                created_by=user.id,
                source_run_id=source_run.id if source_run else None,
                payload_ref=source_run.payload_ref if source_run else None,
                payload_size_bytes=source_run.payload_size_bytes if source_run else None,
                payload_sha256=source_run.payload_sha256 if source_run else None,
                data_state="final",
            )
        )
    content["reading_review_state"] = {
        "status": "confirmed",
        "confirmed_revision_id": str(confirmed_revision.id),
        "confirmation_mode": "human_confirmed",
    }


def _normalize_confirmed_reading_candidate_codes(report: dict[str, Any]) -> dict[str, Any]:
    """Sort confirmed candidates, renumber them, and keep candidate references consistent."""
    normalized = copy.deepcopy(report)
    specifications = (
        ("role_candidates", "role_code", "R", "character"),
        ("scene_candidates", "scene_code", "SC", "scene"),
        ("prop_candidates", "prop_code", "P", "prop"),
    )
    code_maps: dict[str, dict[str, str]] = {}
    old_codes: dict[str, set[str]] = {}

    for list_key, code_key, prefix, asset_type in specifications:
        candidates = [dict(item) for item in normalized.get(list_key) or [] if isinstance(item, dict)]
        old_codes[asset_type] = {
            code
            for item in candidates
            if (code := _reading_candidate_code(item.get(code_key) or item.get("asset_code")))
        }
        candidates.sort(
            key=lambda item: (
                _reading_candidate_priority_rank(item.get("priority")),
                _reading_candidate_code_number(item.get(code_key) or item.get("asset_code")),
            )
        )
        mapping: dict[str, str] = {}
        for index, candidate in enumerate(candidates, start=1):
            old_code = _reading_candidate_code(candidate.get(code_key) or candidate.get("asset_code"))
            new_code = f"{prefix}{index:03d}"
            if old_code:
                mapping[old_code] = new_code
            candidate["priority"] = _reading_candidate_priority(candidate.get("priority"))
            candidate[code_key] = new_code
            candidate["asset_code"] = new_code
        normalized[list_key] = candidates
        code_maps[asset_type] = mapping

    role_codes = old_codes["character"]
    scene_codes = old_codes["scene"]
    role_map = code_maps["character"]
    scene_map = code_maps["scene"]

    relationships: list[dict[str, Any]] = []
    for raw_relationship in normalized.get("relationship_map") or []:
        if not isinstance(raw_relationship, dict):
            continue
        relationship = dict(raw_relationship)
        from_code = _reading_candidate_code(relationship.get("from_role_code"))
        to_code = _reading_candidate_code(relationship.get("to_role_code"))
        if from_code not in role_codes or to_code not in role_codes:
            continue
        relationship["from_role_code"] = role_map[from_code]
        relationship["to_role_code"] = role_map[to_code]
        relationships.append(relationship)
    normalized["relationship_map"] = relationships

    for candidate in normalized["role_candidates"]:
        candidate["related_scene_codes"] = _remap_reading_candidate_code_list(
            candidate.get("related_scene_codes"), scene_codes, scene_map
        )
    for candidate in normalized["prop_candidates"]:
        owner_code = _reading_candidate_code(candidate.get("owner_role_code"))
        candidate["owner_role_code"] = role_map.get(owner_code, "") if owner_code else ""
        candidate["related_scene_codes"] = _remap_reading_candidate_code_list(
            candidate.get("related_scene_codes"), scene_codes, scene_map
        )

    proposals: list[dict[str, Any]] = []
    for raw_proposal in normalized.get("asset_change_proposals") or []:
        if not isinstance(raw_proposal, dict):
            continue
        proposal = dict(raw_proposal)
        asset_type = _reading_proposal_asset_type(proposal.get("asset_type"))
        candidate_code = _reading_candidate_code(proposal.get("candidate_code"))
        if not asset_type or candidate_code not in old_codes[asset_type]:
            continue
        proposal["candidate_code"] = code_maps[asset_type][candidate_code]
        raw_resolution = proposal.get("human_resolution")
        if isinstance(raw_resolution, dict):
            resolution = dict(raw_resolution)
            if "owner_role_code" in resolution:
                owner_code = _reading_candidate_code(resolution.get("owner_role_code"))
                resolution["owner_role_code"] = role_map.get(owner_code, "") if owner_code else ""
            if "related_scene_codes" in resolution:
                resolution["related_scene_codes"] = _remap_reading_affected_assets(
                    resolution.get("related_scene_codes"), scene_codes, scene_map
                )
            proposal["human_resolution"] = resolution
        proposals.append(proposal)
    normalized["asset_change_proposals"] = proposals

    risks: list[dict[str, Any]] = []
    candidate_code_map = {
        old_code: new_code
        for asset_type_map in code_maps.values()
        for old_code, new_code in asset_type_map.items()
    }
    valid_candidate_codes = set().union(*old_codes.values())
    for raw_risk in normalized.get("production_risks") or []:
        if not isinstance(raw_risk, dict):
            continue
        risk = dict(raw_risk)
        if "affected_assets" in risk:
            remapped_assets = _remap_reading_affected_assets(
                risk.get("affected_assets"), valid_candidate_codes, candidate_code_map
            )
            risk["affected_assets"] = remapped_assets if remapped_assets is not None else ""
        risks.append(risk)
    normalized["production_risks"] = risks

    raw_inventory = normalized.get("asset_inventory_summary")
    inventory = dict(raw_inventory) if isinstance(raw_inventory, dict) else {}
    inventory.update({
        "roles": _reading_codes_by_priority(normalized["role_candidates"], "role_code"),
        "scenes": _reading_codes_by_priority(normalized["scene_candidates"], "scene_code"),
        "props": _reading_codes_by_priority(normalized["prop_candidates"], "prop_code"),
    })
    inventory["must_lock_before_storyboard"] = [
        code
        for group_key in ("roles", "scenes", "props")
        for priority in ("S", "A")
        for code in inventory[group_key][priority]
    ]
    normalized["asset_inventory_summary"] = inventory
    return normalized


def _reading_candidate_priority(value: Any) -> str:
    priority = str(value or "").strip().upper()
    return priority if priority in {"S", "A", "B", "C"} else "C"


def _reading_candidate_priority_rank(value: Any) -> int:
    return {"S": 0, "A": 1, "B": 2, "C": 3}[_reading_candidate_priority(value)]


def _reading_candidate_code(value: Any) -> str:
    return str(value or "").strip().upper()


def _reading_candidate_code_number(value: Any) -> int:
    match = re.search(r"(\d+)", _reading_candidate_code(value))
    return int(match.group(1)) if match else 10**9


def _remap_reading_candidate_code_list(
    values: Any,
    valid_old_codes: set[str],
    code_map: dict[str, str],
) -> list[str]:
    if not isinstance(values, list):
        return []
    remapped: list[str] = []
    for value in values:
        old_code = _reading_candidate_code(value)
        if old_code in valid_old_codes:
            new_code = code_map[old_code]
            if new_code not in remapped:
                remapped.append(new_code)
    return remapped


def _reading_proposal_asset_type(value: Any) -> str | None:
    normalized = str(value or "").strip().lower()
    return {
        "character": "character",
        "characters": "character",
        "role": "character",
        "scene": "scene",
        "scenes": "scene",
        "prop": "prop",
        "props": "prop",
    }.get(normalized)


def _reading_codes_by_priority(candidates: list[dict[str, Any]], code_key: str) -> dict[str, list[str]]:
    grouped = {"S": [], "A": [], "B": [], "C": []}
    for candidate in candidates:
        code = _reading_candidate_code(candidate.get(code_key) or candidate.get("asset_code"))
        if code:
            grouped[_reading_candidate_priority(candidate.get("priority"))].append(code)
    return grouped


def _remap_reading_affected_assets(
    value: Any,
    valid_old_codes: set[str],
    code_map: dict[str, str],
) -> Any:
    if isinstance(value, list):
        remapped_items = [
            remapped
            for item in value
            if (remapped := _remap_reading_affected_assets(item, valid_old_codes, code_map))
            not in (None, "", [], {})
        ]
        return remapped_items
    if isinstance(value, dict):
        return {
            key: remapped
            for key, item in value.items()
            if (remapped := _remap_reading_affected_assets(item, valid_old_codes, code_map))
            not in (None, "", [], {})
        }
    if not isinstance(value, str):
        return value

    text = value.strip()
    if not text:
        return value
    if text[:1] in {"[", "{"}:
        try:
            parsed = json.loads(text)
        except (TypeError, ValueError):
            pass
        else:
            if isinstance(parsed, (list, dict)):
                remapped = _remap_reading_affected_assets(parsed, valid_old_codes, code_map)
                return json.dumps(remapped, ensure_ascii=False, separators=(",", ":"))

    exact_code = _reading_candidate_code(text)
    if re.fullmatch(r"(?:SC|R|P)\d+", exact_code):
        return code_map.get(exact_code) if exact_code in valid_old_codes else None

    token_pattern = re.compile(r"(?<![A-Z0-9_-])(?:SC|R|P)\d+(?![A-Z0-9_-])", flags=re.I)
    matches = list(token_pattern.finditer(text))
    if not matches:
        return value
    delimiter_remainder = token_pattern.sub("", text)
    if not delimiter_remainder.strip(" \t\r\n,，、;；|/"):
        return "、".join(
            code_map[code]
            for match in matches
            if (code := _reading_candidate_code(match.group())) in valid_old_codes
        )

    def replace_code(match: re.Match[str]) -> str:
        code = _reading_candidate_code(match.group())
        return code_map.get(code, "") if code in valid_old_codes else ""

    remapped_text = token_pattern.sub(replace_code, text)
    remapped_text = re.sub(r"([,，、;；|/])\s*(?:[,，、;；|/]\s*)+", r"\1", remapped_text)
    return remapped_text.strip(" \t\r\n,，、;；|/")


async def _reading_source_agent_run(
    session: AsyncSession,
    project_id: UUID,
    script: Script | None,
    script_version: ScriptVersion | None,
) -> AgentRun | None:
    runs = (
        await session.execute(
            select(AgentRun)
            .where(
                AgentRun.project_id == project_id,
                AgentRun.agent_type == AgentKind.script_breakdown,
                AgentRun.status == AgentRunStatus.succeeded,
            )
            .order_by(AgentRun.created_at.desc())
        )
    ).scalars().all()
    expected_version = str(script_version.id) if script_version else None
    for run in runs:
        if script and run.script_id and run.script_id != script.id:
            continue
        run_version = str((run.input_json or {}).get("script_version_id") or "") or None
        if expected_version and run_version != expected_version:
            continue
        return run
    return None


def _reading_candidate_maps(report: dict[str, Any]) -> dict[tuple[str, str], dict[str, Any]]:
    output: dict[tuple[str, str], dict[str, Any]] = {}
    for list_key, asset_type, code_key in (
        ("role_candidates", "character", "role_code"),
        ("scene_candidates", "scene", "scene_code"),
        ("prop_candidates", "prop", "prop_code"),
    ):
        for candidate in report.get(list_key) or []:
            if not isinstance(candidate, dict):
                continue
            code = str(candidate.get(code_key) or candidate.get("asset_code") or "").strip()
            if code:
                output[(asset_type, code)] = dict(candidate)
            name = str(candidate.get("name") or "").strip()
            if name:
                output[(asset_type, f"name:{name}")] = dict(candidate)
    return output


async def _apply_reading_asset_change_proposal(
    session: AsyncSession,
    project: Project,
    proposal: dict[str, Any],
    index: int,
    candidate_maps: dict[tuple[str, str], dict[str, Any]],
    reading_revision: ArtifactRevision,
    source_snapshot: dict[str, Any],
    user: User,
) -> dict[str, Any]:
    action = str(proposal.get("action") or "").strip()
    decision = str(proposal.get("decision") or "pending").strip()
    candidate_code = str(proposal.get("candidate_code") or "").strip()
    candidate_name = str(proposal.get("candidate_name") or "").strip()
    asset_type = str(proposal.get("asset_type") or "").strip()
    base = {
        "proposal_index": index,
        "candidate_code": candidate_code or None,
        "candidate_name": candidate_name or None,
        "action": action,
        "decision": decision,
    }
    if decision == "rejected":
        return {**base, "application_status": "rejected", "message": "人工已驳回，仅保留审计记录。"}
    if action not in {"new", "supplement", "conflict", "reuse"}:
        raise ValueError(f"资产差异 {candidate_code or index + 1} 的操作类型无效。")
    candidate = candidate_maps.get((asset_type, candidate_code), {})
    if not candidate and candidate_name:
        candidate = candidate_maps.get((asset_type, f"name:{candidate_name}"), {})
    if not candidate and action == "new":
        raise ValueError(f"新增资产 {candidate_code or candidate_name} 找不到对应围读候选。")

    asset = await _reading_proposal_target_asset(session, project.id, proposal)
    if action == "new":
        if asset is not None:
            raise ValueError(f"新增资产 {candidate_code or candidate_name} 已匹配到现有母版，请改为复用或补充。")
        code = await _available_asset_code(session, project, candidate_code or _candidate_code_fallback(asset_type, index + 1))
        enum_type = _asset_type_enum(asset_type)
        metadata = {
            **candidate,
            "reading_candidate": candidate,
            "confirmation_status": "pending_confirmation",
            "reading_sources": [_reading_asset_source(reading_revision, source_snapshot, proposal)],
        }
        asset = Asset(
            project_id=project.id,
            asset_code=code,
            asset_type=enum_type,
            name=candidate_name or str(candidate.get("name") or code),
            description=_reading_candidate_description(asset_type, candidate),
            status="pending_confirmation",
            tags=[enum_type.value, "reading_confirmed"],
            prompt_text=str(candidate.get("prompt_usage") or "").strip() or None,
            base_model="Seedream",
            metadata_json=metadata,
            created_by_id=user.id,
        )
        input_fingerprint = _content_hash(_asset_prompt_input_projection(_asset_revision_content(asset)))
        _set_asset_row_lifecycle(
            asset,
            "prompt_pending",
            prompt_validity="missing",
            prompt_input_fingerprint=input_fingerprint,
        )
        session.add(asset)
        await session.flush()
        revision = await _append_asset_revision(session, asset, source_type="human", created_by=user.id, source_run_id=reading_revision.source_run_id)
        _annotate_reading_asset_revision(
            revision,
            reading_revision,
            source_snapshot,
            proposal,
            None,
            status=_asset_confirmation_status(asset),
        )
        return {
            **base,
            "application_status": "created",
            "asset_id": str(asset.id),
            "asset_code": asset.asset_code,
            "asset_revision_id": str(revision.id),
            "message": "已创建资产母版草稿，等待补齐专用 Prompt。",
        }

    if asset is None:
        raise ValueError(f"资产差异 {candidate_code or candidate_name} 未找到匹配母版。")
    if action == "reuse":
        return {
            **base,
            "application_status": "reused",
            "asset_id": str(asset.id),
            "asset_code": asset.asset_code,
            "asset_revision_id": str(asset.current_revision_id) if asset.current_revision_id else None,
            "message": "复用现有母版，未修改资产内容。",
        }

    before = _asset_revision_content(asset)
    patch_source = proposal.get("human_resolution") if action == "conflict" else candidate
    patch_source = dict(patch_source or {})
    changed_fields = [str(item) for item in proposal.get("changed_fields") or [] if str(item).strip()]
    if action == "conflict" and not changed_fields:
        changed_fields = list(patch_source)
    applied_fields = _apply_reading_candidate_fields(asset, patch_source, changed_fields)
    if not applied_fields:
        raise ValueError(f"资产差异 {candidate_code or candidate_name} 没有可应用的字段内容。")
    metadata = dict(asset.metadata_json or {})
    sources = [dict(item) for item in metadata.get("reading_sources") or [] if isinstance(item, dict)]
    sources.append(_reading_asset_source(reading_revision, source_snapshot, proposal))
    metadata["reading_sources"] = sources
    asset.metadata_json = metadata
    input_fingerprint = _content_hash(_asset_prompt_input_projection(_asset_revision_content(asset)))
    prompt_only = all(field in {"prompt", "prompt_text", "prompt_usage"} for field in applied_fields)
    if prompt_only:
        _set_asset_row_lifecycle(
            asset,
            "pending_confirmation",
            prompt_validity="current",
            prompt_input_fingerprint=input_fingerprint,
            prompt_generated_for_fingerprint=input_fingerprint,
        )
    else:
        has_prompt = bool(asset.prompt_text or metadata.get("prompt_design") or metadata.get("prompt_lineage"))
        _set_asset_row_lifecycle(
            asset,
            "prompt_pending",
            prompt_validity="stale" if has_prompt else "missing",
            prompt_input_fingerprint=input_fingerprint,
            invalidated_fields=applied_fields if has_prompt else None,
        )
    revision = await _append_asset_revision(session, asset, source_type="human", created_by=user.id, source_run_id=reading_revision.source_run_id)
    _annotate_reading_asset_revision(
        revision,
        reading_revision,
        source_snapshot,
        proposal,
        before,
        status=_asset_confirmation_status(asset),
    )
    return {
        **base,
        "application_status": "updated",
        "asset_id": str(asset.id),
        "asset_code": asset.asset_code,
        "asset_revision_id": str(revision.id),
        "message": f"已写入字段：{'、'.join(applied_fields)}。",
    }


async def _reading_proposal_target_asset(session: AsyncSession, project_id: UUID, proposal: dict[str, Any]) -> Asset | None:
    matched_id = _uuid_or_none(proposal.get("matched_asset_id"))
    asset = await session.get(Asset, matched_id) if matched_id else None
    if asset is not None and asset.project_id == project_id:
        return asset
    matched_code = str(proposal.get("matched_asset_code") or "").strip()
    if matched_code:
        return await session.scalar(select(Asset).where(Asset.project_id == project_id, Asset.asset_code == matched_code))
    candidate_code = str(proposal.get("candidate_code") or "").strip()
    if candidate_code:
        return await session.scalar(select(Asset).where(Asset.project_id == project_id, Asset.asset_code == candidate_code))
    return None


async def _available_asset_code(session: AsyncSession, project: Project, desired: str) -> str:
    base = desired.strip().upper()[:80] or "ASSET"
    if await session.scalar(select(Asset.id).where(Asset.asset_code == base)) is None:
        return base
    prefix = (project.project_prefix or "RF").strip().upper()[:12]
    candidate = f"{prefix}-{base}"[:80]
    suffix = 2
    while await session.scalar(select(Asset.id).where(Asset.asset_code == candidate)) is not None:
        candidate = f"{prefix}-{base}-{suffix}"[:80]
        suffix += 1
    return candidate


def _candidate_code_fallback(asset_type: str, index: int) -> str:
    prefix = {"character": "R", "scene": "SC", "prop": "P"}.get(asset_type, "A")
    return f"{prefix}{index:03d}"


def _reading_candidate_description(asset_type: str, candidate: dict[str, Any]) -> str | None:
    fields = {
        "character": ("dramatic_function", "appearance_clues", "costume_direction"),
        "scene": ("story_function", "visual_direction", "lighting", "mood"),
        "prop": ("story_function", "visual_anchor", "status_changes"),
    }.get(asset_type, ("evidence",))
    parts = [str(candidate.get(key) or "").strip() for key in fields]
    text = "；".join(part for part in parts if part)
    return text or None


def _apply_reading_candidate_fields(asset: Asset, patch: dict[str, Any], changed_fields: list[str]) -> list[str]:
    metadata = dict(asset.metadata_json or {})
    applied: list[str] = []
    for field in changed_fields:
        if field not in patch or patch[field] in (None, "", [], {}):
            continue
        value = json.loads(json.dumps(patch[field], ensure_ascii=False))
        if field == "name":
            asset.name = str(value)
        elif field == "description":
            asset.description = str(value)
        elif field in {"prompt", "prompt_text", "prompt_usage"}:
            asset.prompt_text = str(value)
        elif field == "tags" and isinstance(value, list):
            asset.tags = [str(item) for item in value]
        elif field in {"asset_code", "role_code", "scene_code", "prop_code", "asset_type"}:
            continue
        else:
            metadata[field] = value
        applied.append(field)
    asset.metadata_json = metadata
    return applied


def _reading_asset_source(
    reading_revision: ArtifactRevision,
    source_snapshot: dict[str, Any],
    proposal: dict[str, Any],
) -> dict[str, Any]:
    return {
        "confirmed_reading_revision_id": str(reading_revision.id),
        "episode_id": source_snapshot.get("episode_id"),
        "episode_code": source_snapshot.get("episode_code"),
        "script_version_id": source_snapshot.get("script_version_id"),
        "script_version_no": source_snapshot.get("script_version_no"),
        "proposal": proposal,
    }


def _annotate_reading_asset_revision(
    revision: ArtifactRevision,
    reading_revision: ArtifactRevision,
    source_snapshot: dict[str, Any],
    proposal: dict[str, Any],
    before: dict[str, Any] | None,
    *,
    status: str,
) -> None:
    revision.input_snapshot = {
        **source_snapshot,
        "confirmed_reading_revision_id": str(reading_revision.id),
        "asset_change_proposal": proposal,
    }
    revision.change_diff = {
        "source": "accepted_reading_asset_change",
        "before": before,
        "after": revision.normalized_content,
        "changed_fields": proposal.get("changed_fields") or [],
    }
    revision.status = status


_BREAKDOWN_PUBLIC_FORBIDDEN_FIELDS = {
    "raw_output",
    "raw_model_response",
    "agent_raw_asset_output",
    "debug_output",
    "characters",
    "scenes",
    "props",
    "agent_run_id",
    "data_state",
    "reading_output",
    "asset_output",
    "asset_reference_index",
    "confirmed_reading_report",
    "confirmed_asset_inventory",
    "confirmed_asset_inventory_status",
}


def _breakdown_view_from_saved_content(content: dict[str, Any]) -> dict[str, Any]:
    return {
        key: copy.deepcopy(value)
        for key, value in content.items()
        if key not in _BREAKDOWN_PUBLIC_FORBIDDEN_FIELDS
    }


def _breakdown_read_from_content(
    project_id: UUID,
    version: int,
    content: dict[str, Any],
    data_state: str,
    agent_run_id: UUID | None,
    updated_at: datetime,
) -> BreakdownRead:
    view = _breakdown_view_from_saved_content(content)
    return _breakdown_read_from_view(project_id, version, view, data_state, agent_run_id, updated_at)


def _breakdown_read_from_view(
    project_id: UUID,
    version: int,
    view: dict[str, Any],
    data_state: str,
    agent_run_id: UUID | None,
    updated_at: datetime,
) -> BreakdownRead:
    public_view = {
        key: copy.deepcopy(value)
        for key, value in view.items()
        if key not in _BREAKDOWN_PUBLIC_FORBIDDEN_FIELDS
    }
    for key in ("reading_review_state", "asset_review_state"):
        if key in public_view:
            public_view[key] = _normalized_breakdown_review_state(public_view[key])
    return BreakdownRead(
        project_id=project_id,
        version=version,
        view=public_view,
        data_state=data_state,
        agent_run_id=agent_run_id,
        updated_at=updated_at,
    )


def _breakdown_has_lockable_outputs(content: dict[str, Any]) -> bool:
    return bool(content.get("storyboards") or content.get("assets"))


async def _latest_script_confirmed_breakdown_version(session: AsyncSession, project_id: UUID, episode_code: str | None) -> int | None:
    stmt = (
        select(ScriptBreakdown)
        .where(ScriptBreakdown.project_id == project_id)
        .order_by(ScriptBreakdown.version.desc())
    )
    result = await session.execute(stmt)
    for breakdown in result.scalars().all():
        content = breakdown.content_json or {}
        if content.get("source_label") != "human_confirmed":
            continue
        if episode_code and _normalize_episode_code(content.get("episode_code")) != episode_code:
            continue
        return breakdown.version
    return None


async def _upsert_confirmed_script_segments(
    session: AsyncSession,
    project: Project,
    content: dict[str, Any],
    user: User,
) -> int:
    # Resolve episode_id per episode_code so confirmed segments carry the scope the
    # downstream storyboard gate filters on (list_script_segments WHERE episode_id=:id).
    # Without this the rows land with episode_id=NULL and the gate reports "请先确认脚本段"
    # even though segments exist. Seed the cache with the authoritative content-level
    # episode_id for the content's episode_code; fall back to a lookup by code.
    episode_id_by_code: dict[str, UUID | None] = {}
    content_episode_id = _uuid_or_none(content.get("episode_id"))
    content_episode_code = _normalize_episode_code(content.get("episode_code"))
    if content_episode_id is not None and content_episode_code:
        episode_id_by_code[content_episode_code] = content_episode_id

    async def _resolve_episode_id(episode_code: str) -> UUID | None:
        normalized = _normalize_episode_code(episode_code) or "EP01"
        if normalized in episode_id_by_code:
            return episode_id_by_code[normalized]
        episode = await session.scalar(
            select(ProjectEpisode).where(
                ProjectEpisode.project_id == project.id,
                ProjectEpisode.episode_code == normalized,
            )
        )
        resolved = episode.id if episode is not None else None
        episode_id_by_code[normalized] = resolved
        return resolved

    confirmed = 0
    for index, segment_data in enumerate(_segments_from_breakdown_output(project, content), start=1):
        segment_id = _uuid_or_none(segment_data.get("id"))
        segment = await session.get(ScriptSegment, segment_id) if segment_id else None
        if segment is None:
            segment = await session.scalar(
                select(ScriptSegment).where(
                    ScriptSegment.project_id == project.id,
                    ScriptSegment.script_segment_code == segment_data["script_segment_code"],
                )
            )
        if segment is None:
            segment = ScriptSegment(
                project_id=project.id,
                script_segment_code=segment_data["script_segment_code"],
                created_by=user.id,
            )
            session.add(segment)
        segment.episode_code = segment_data.get("episode_code") or segment.episode_code or "EP01"
        segment.episode_id = (
            _uuid_or_none(segment_data.get("episode_id"))
            or await _resolve_episode_id(segment.episode_code)
            or segment.episode_id
        )
        segment.order_no = _safe_int(segment_data.get("order_no"), index)
        segment.title = segment_data.get("title") or segment.title
        segment.source_text = segment_data.get("source_text") or segment.source_text or "未提供原文依据"
        segment.summary = segment_data.get("summary")
        segment.story_function = segment_data.get("story_function")
        segment.dominant_emotion = segment_data.get("dominant_emotion")
        segment.rhythm = segment_data.get("rhythm")
        segment.viewpoint = segment_data.get("viewpoint")
        segment.context_code = segment_data.get("context_code") or dict(segment_data.get("metadata") or {}).get("context_code")
        segment.render_mode = segment_data.get("render_mode") or dict(segment_data.get("metadata") or {}).get("render_mode")
        segment.metadata_json = {
            **dict(segment.metadata_json or {}),
            **dict(segment_data.get("metadata_json") or segment_data.get("metadata") or {}),
            **_script_segment_extended_metadata(segment_data),
            "source": "human_confirmed",
            "status": "script_confirmed",
        }
        segment.updated_at = datetime.now(UTC)
        await session.flush()
        version = await create_entity_version(
            session,
            EntityVersionCreate(
                project_id=project.id,
                entity_type="script_segment",
                entity_id=segment.id,
                entity_code=segment.script_segment_code,
                source="human_confirmed",
                status="script_confirmed",
                content_json=script_segment_to_read(segment).model_dump(mode="json"),
                change_summary="导演确认脚本段，进入分镜和资产拆分阶段",
                changed_fields=["script_segment", "source_text", "summary"],
                created_by=user.id,
            ),
            commit=False,
        )
        segment.current_version_id = version.id
        confirmed += 1
    return confirmed


def _breakdown_entity_code(project: Project, episode_code: Any, version: int) -> str:
    prefix = project.project_prefix or project.project_no or str(project.id)
    normalized_episode = _normalize_episode_code(episode_code) or "ALL"
    return f"{prefix}-{normalized_episode}-BREAKDOWN-V{version:03d}"


def _build_breakdown_entity_version(
    *,
    project: Project,
    entity_code: str,
    content: dict[str, Any],
    status: str,
    created_by: UUID | None,
) -> EntityVersionCreate:
    change_summary = list(content.get("change_summary") or [])
    return EntityVersionCreate(
        project_id=project.id,
        entity_type="script_breakdown",
        entity_id=None,
        entity_code=entity_code,
        source=str(content.get("source_label") or "human_modified"),
        status=status,
        content_json=content,
        change_summary="；".join(change_summary) if change_summary else None,
        changed_fields=["storyboards", "assets", "breakdown"],
        agent_run_id=_uuid_or_none(content.get("agent_run_id")),
        created_by=created_by,
    )


def _build_breakdown_feedback_training_sample(
    *,
    project: Project,
    entity_code: str,
    content: dict[str, Any],
    user: User,
    sample_type: str,
    status_tag: str,
    training_ready: bool,
    confirmed: bool,
    default_summary: str,
) -> AgentTrainingSample:
    sample_payload = {
        "schema_version": "CineForgeTrainingPayload.v1",
        "input": dict(content.get("input_json") or {}),
        "agent_output": dict(content.get("raw_output") or {}),
        "human_revision": content,
        "final": content if confirmed else {},
    }
    encoded_size = len(json.dumps(sample_payload, ensure_ascii=False, sort_keys=True, default=str, separators=(",", ":")).encode("utf-8"))
    stored = store_json_payload(sample_payload, namespace="training-payloads") if encoded_size > settings.agent_payload_externalize_threshold_bytes else None
    content_summary = {
        "content_hash": _content_hash(content),
        "storyboards_count": len(content.get("storyboards") or []),
        "assets_count": len(content.get("assets") or []),
    }
    return AgentTrainingSample(
        project_id=project.id,
        agent_type=AgentKind.script_breakdown,
        sample_type=sample_type,
        entity_type="script_breakdown",
        entity_code=entity_code,
        input_json=_lineage_summary(dict(content.get("input_json") or {})) if stored else sample_payload["input"],
        agent_output_json={**content_summary, "payload_externalized": True} if stored else sample_payload["agent_output"],
        human_modified_output_json=content_summary if stored else content,
        confirmed_output_json={**content_summary, "status": "confirmed"} if stored and confirmed else content if confirmed else {},
        change_summary=list(content.get("change_summary") or [default_summary]),
        training_tags=["human_modified", status_tag, "version_control", "episode_breakdown"],
        training_ready=training_ready,
        lineage_key=_lineage_key_from_run_input(dict(content.get("input_json") or {}), "storyboard_breakdown"),
        created_by=user.id,
        source_run_id=None,
        payload_ref=stored.ref if stored else None,
        payload_size_bytes=stored.size_bytes if stored else None,
        payload_sha256=stored.sha256 if stored else None,
        data_state="final" if confirmed else "human_revision",
    )


def _sorted_storyboards(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
    normalized = [dict(item) for item in items if isinstance(item, dict)]
    for index, item in enumerate(normalized, start=1):
        item.setdefault("order_num", item.get("order_no") or item.get("shot_no") or index)
        item.setdefault("episode_code", _storyboard_episode_code(item))
    return sorted(normalized, key=lambda item: (_episode_num_from_code(_storyboard_episode_code(item)), _safe_int(item.get("order_num"), 999999)))


def _asset_items_from_output(output: dict[str, Any]) -> list[dict[str, Any]]:
    return [dict(value) for value in output.get("assets") or [] if isinstance(value, dict)]


def _next_formal_asset_code(
    project_prefix: str,
    asset_type: AssetType,
    reserved_codes: set[str],
) -> str:
    if asset_type not in _VISUAL_ASSET_TYPES:
        raise ValueError(f"Unsupported visual asset type: {asset_type.value}")
    prefix = _safe_code_prefix(project_prefix)
    type_name = asset_type.value
    code_prefix = {"character": "R", "scene": "SC", "prop": "P"}[type_name]
    occupied = {str(value or "").strip().upper() for value in reserved_codes if str(value or "").strip()}
    sequence = 0
    for code in occupied:
        existing_type, existing_sequence = _formal_asset_code_parts(code, prefix=prefix)
        if existing_type == type_name:
            sequence = max(sequence, existing_sequence)
    while True:
        sequence += 1
        candidate = f"{prefix}-{code_prefix}{sequence:03d}"
        if candidate not in occupied:
            return candidate


_ASSET_LIFECYCLE_STATES = {
    "needs_completion",
    "prompt_pending",
    "pending_confirmation",
    "confirmed",
}

_VISUAL_ASSET_TYPES = {AssetType.character, AssetType.scene, AssetType.prop}

_PROMPT_NON_INPUT_FIELDS = {
    "id", "asset_id", "asset_code", "agent_asset_code", "client_asset_key", "asset_code_source",
    "character_code", "scene_code", "prop_code", "role_code", "candidate_asset_code",
    "status", "confirmation_status", "prompt_validity", "prompt_input_fingerprint",
    "prompt_generated_for_fingerprint", "prompt_invalidated_at", "prompt_invalidated_fields",
    "prompt", "prompt_text", "negative_prompt", "input_hash", "skill", "skill_version", "contract_version",
    "contract_hash", "prompt_version", "context_key", "context_schema_version", "brief_trace",
    "resolved_production_brief", "production_context", "production_contexts", "prompt_lineage",
    "prompt_design", "costume_design", "view_prompts", "state_prompts", "spatial_bible",
    "material_bible", "material_layers", "character_apose", "manual_review_items",
    "generated_output_spec", "generated_view_codes", "deferred_view_codes", "generation_strategy",
    "runtime_policy_execution", "structured_output_mode", "error", "processing_status",
    "name", "display_name", "localized_name", "description", "description_zh", "description_th",
    "translated_name", "translated_description", "back_translation", "translation_notes",
    "priority", "order", "order_no", "index", "episode_id", "episode_code", "script_id",
    "script_version_id", "storyboard_id", "task_id", "revision_id", "asset_revision_id",
    "source_run_id", "agent_run_id", "created_at", "updated_at", "confirmed_at", "confirmed_by",
    "asset_task_contexts", "has_asset_tasks", "task_count", "notes", "human_notes",
    "_contract_repairs",
}

_ASSET_CONFIRMATION_CRITICAL_FIELDS = {
    "asset_code", "agent_asset_code", "candidate_asset_code", "asset_type", "type",
    "character_code", "scene_code", "prop_code", "role_code", "character_type",
    "visual_presence", "role", "relationship", "appearance", "core_requirement",
    "identity_setting", "visual_features", "continuity_constraints", "forbidden_variations",
    "visual_effects", "material_layers", "age_stages", "key_prop_ids", "key_props",
    "scene_ids", "scene_names", "related_scene_codes", "related_scenes", "usage_scenes",
    "owner", "owner_id", "owner_character", "owner_character_id", "owner_role_code",
    "state", "production_status", "is_excluded", "excluded",
}


def _asset_confirmation_projection(asset: dict[str, Any] | None) -> dict[str, Any]:
    """Return the stable production identity used by the asset review gate.

    Localized/display copy deliberately stays outside this projection. Those edits
    are versioned, but they do not erase an already completed human review.
    """
    if not isinstance(asset, dict):
        return {}

    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}

    def canonical(value: Any) -> Any:
        if isinstance(value, dict):
            return {key: canonical(nested) for key, nested in sorted(value.items())}
        if isinstance(value, list):
            normalized = [canonical(nested) for nested in value]
            if all(not isinstance(item, (dict, list)) for item in normalized):
                return sorted(normalized, key=lambda item: str(item))
            return normalized
        if isinstance(value, str):
            return value.strip()
        return value

    projection: dict[str, Any] = {}
    for key in sorted(_ASSET_CONFIRMATION_CRITICAL_FIELDS):
        value = asset.get(key) if key in asset else metadata.get(key)
        if value not in (None, "", [], {}):
            projection[key] = canonical(value)
    return projection


def _asset_confirmation_impact(
    previous: dict[str, Any] | None,
    current: dict[str, Any] | None,
) -> dict[str, Any]:
    before = _asset_confirmation_projection(previous)
    after = _asset_confirmation_projection(current)
    changed_fields = sorted(
        key for key in set(before) | set(after) if before.get(key) != after.get(key)
    )
    return {
        "requires_review": bool(changed_fields),
        "changed_fields": changed_fields,
    }


def _confirmed_inventory_inheritance_impact(
    previous_assets: list[dict[str, Any]],
    current_assets: list[dict[str, Any]],
) -> dict[str, Any]:
    critical_changes: list[dict[str, Any]] = []
    matched_previous: set[int] = set()
    for current in current_assets:
        previous = _previous_asset_for_lifecycle(current, previous_assets)
        if previous is None:
            critical_changes.append({
                "asset": current.get("asset_code") or current.get("name"),
                "changed_fields": ["asset_added"],
            })
            continue
        matched_previous.add(id(previous))
        impact = _asset_confirmation_impact(previous, current)
        if impact["requires_review"]:
            critical_changes.append({
                "asset": current.get("asset_code") or current.get("name"),
                "changed_fields": impact["changed_fields"],
            })
    for previous in previous_assets:
        if id(previous) not in matched_previous:
            critical_changes.append({
                "asset": previous.get("asset_code") or previous.get("name"),
                "changed_fields": ["asset_removed"],
            })
    return {
        "can_inherit": not critical_changes,
        "critical_changes": critical_changes,
    }


def _project_prompt_input_changes(
    before: dict[str, Any],
    after: dict[str, Any],
) -> list[str]:
    changes: list[str] = []
    if before.get("genre") != after.get("genre"):
        changes.append("genre")
    before_brief = before.get("production_brief") if isinstance(before.get("production_brief"), dict) else {}
    after_brief = after.get("production_brief") if isinstance(after.get("production_brief"), dict) else {}
    ignored = {"schema_version", "field_sources"}
    changes.extend(
        f"production_brief.{key}"
        for key in sorted(set(before_brief) | set(after_brief))
        if key not in ignored and before_brief.get(key) != after_brief.get(key)
    )
    return changes


def _set_asset_row_lifecycle(
    asset: Asset,
    status: str,
    *,
    prompt_validity: str | None = None,
    prompt_input_fingerprint: str | None = None,
    prompt_generated_for_fingerprint: str | None = None,
    invalidated_fields: list[str] | None = None,
) -> None:
    if status not in _ASSET_LIFECYCLE_STATES:
        raise ValueError(f"Unsupported asset lifecycle status: {status}")
    metadata = dict(asset.metadata_json or {})
    metadata["confirmation_status"] = status
    if prompt_validity is not None:
        metadata["prompt_validity"] = prompt_validity
    if prompt_input_fingerprint:
        metadata["prompt_input_fingerprint"] = prompt_input_fingerprint
    if prompt_generated_for_fingerprint:
        metadata["prompt_generated_for_fingerprint"] = prompt_generated_for_fingerprint
    if invalidated_fields:
        metadata["prompt_invalidated_at"] = datetime.now(UTC).isoformat()
        metadata["prompt_invalidated_fields"] = sorted(set(invalidated_fields))
    elif prompt_validity == "current":
        metadata.pop("prompt_invalidated_at", None)
        metadata.pop("prompt_invalidated_fields", None)
    asset.metadata_json = metadata
    asset.status = status
    asset.updated_at = datetime.now(UTC)


def _asset_row_prompt_input_fingerprint(asset: Asset) -> str:
    metadata = asset.metadata_json or {}
    stored = str(metadata.get("prompt_input_fingerprint") or "").strip()
    if stored:
        return stored
    return _content_hash(_asset_prompt_input_projection({
        "asset_type": asset.asset_type.value if hasattr(asset.asset_type, "value") else str(asset.asset_type),
        "name": asset.name,
        "description": asset.description,
        "metadata": metadata,
    }))


async def _invalidate_project_asset_prompts(
    session: AsyncSession,
    project_id: UUID,
    *,
    changed_fields: list[str],
    created_by: UUID | None,
) -> None:
    assets = list((await session.scalars(
        select(Asset).where(
            Asset.project_id == project_id,
            Asset.asset_type.in_(_VISUAL_ASSET_TYPES),
            Asset.status.notin_(["deleted", "excluded"]),
        )
    )).all())
    for asset in assets:
        current_status = _asset_confirmation_status(asset)
        if current_status == "needs_completion":
            continue
        metadata = asset.metadata_json or {}
        has_prompt = bool(asset.prompt_text or metadata.get("prompt_design") or metadata.get("prompt_lineage"))
        _set_asset_row_lifecycle(
            asset,
            "prompt_pending",
            prompt_validity="stale" if has_prompt else "missing",
            prompt_input_fingerprint=_asset_row_prompt_input_fingerprint(asset),
            invalidated_fields=changed_fields if has_prompt else None,
        )
        await _append_asset_revision(
            session,
            asset,
            source_type="human",
            created_by=created_by,
        )


def _apply_asset_lifecycle(
    assets: list[dict[str, Any]],
    *,
    previous_assets: list[dict[str, Any]],
    raw_output: dict[str, Any],
    previous_output: dict[str, Any] | None = None,
) -> list[dict[str, Any]]:
    output: list[dict[str, Any]] = []
    prompt_confirmation_required = raw_output.get("prompt_confirmation_required") is not False
    for asset in assets:
        normalized = dict(asset)
        metadata = dict(normalized.get("metadata") or {})
        previous = _previous_asset_for_lifecycle(normalized, previous_assets)
        previous_metadata = dict(previous.get("metadata") or {}) if previous else {}
        current_projection = _asset_prompt_input_projection(normalized)
        current_fingerprint = _content_hash(current_projection)
        previous_projection = _asset_prompt_input_projection(previous) if previous else {}
        previous_fingerprint = (
            str(previous_metadata.get("prompt_input_fingerprint") or "").strip()
            or (_content_hash(previous_projection) if previous else "")
        )
        changed_fields = sorted(
            key
            for key in set(current_projection) | set(previous_projection)
            if current_projection.get(key) != previous_projection.get(key)
        ) if previous else []
        production_changed = bool(previous and current_fingerprint != previous_fingerprint)
        requested_status = _asset_lifecycle_status(normalized)
        previous_status = _asset_lifecycle_status(previous) if previous else ""
        prompt_evidence = _asset_has_prompt_evidence(normalized, raw_output)
        prompt_signature = _asset_prompt_signature(normalized, raw_output)
        previous_prompt_signature = _asset_prompt_signature(previous, previous_output or {}) if previous else ""
        prompt_was_regenerated = bool(
            previous
            and prompt_signature
            and prompt_signature != previous_prompt_signature
        )

        metadata["prompt_input_fingerprint"] = current_fingerprint
        if not _asset_has_required_prompt_content(normalized, current_projection):
            resolved_status = "needs_completion"
        elif production_changed and prompt_was_regenerated:
            resolved_status = "pending_confirmation" if prompt_confirmation_required else "confirmed"
            metadata["prompt_validity"] = "current"
            metadata["prompt_generated_for_fingerprint"] = current_fingerprint
            metadata.pop("prompt_invalidated_at", None)
            metadata.pop("prompt_invalidated_fields", None)
        elif production_changed:
            resolved_status = "prompt_pending"
            metadata["prompt_validity"] = "stale"
            metadata["prompt_invalidated_at"] = datetime.now(UTC).isoformat()
            metadata["prompt_invalidated_fields"] = changed_fields
            if previous_metadata.get("prompt_generated_for_fingerprint"):
                metadata["prompt_generated_for_fingerprint"] = previous_metadata["prompt_generated_for_fingerprint"]
            elif previous_prompt_signature:
                metadata["prompt_generated_for_fingerprint"] = previous_fingerprint
        elif not prompt_evidence:
            resolved_status = "prompt_pending"
            metadata["prompt_validity"] = "missing"
        else:
            if prompt_was_regenerated:
                resolved_status = "pending_confirmation" if prompt_confirmation_required else "confirmed"
                metadata["prompt_validity"] = "current"
                metadata["prompt_generated_for_fingerprint"] = current_fingerprint
            elif previous_status == "prompt_pending":
                resolved_status = "prompt_pending"
                metadata["prompt_validity"] = "stale"
            elif requested_status == "confirmed":
                resolved_status = "confirmed"
                metadata["prompt_validity"] = "current"
                metadata["prompt_generated_for_fingerprint"] = current_fingerprint
            else:
                resolved_status = "pending_confirmation"
                metadata["prompt_validity"] = "current"
                metadata["prompt_generated_for_fingerprint"] = current_fingerprint
            metadata.pop("prompt_invalidated_at", None)
            metadata.pop("prompt_invalidated_fields", None)

        normalized["status"] = resolved_status
        metadata["confirmation_status"] = resolved_status
        normalized["metadata"] = metadata
        output.append(normalized)
    return output


def _asset_lifecycle_status(asset: dict[str, Any] | None) -> str:
    if not isinstance(asset, dict):
        return ""
    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}
    value = str(metadata.get("confirmation_status") or asset.get("status") or "").strip().lower()
    return value if value in _ASSET_LIFECYCLE_STATES else "pending_confirmation"


def _previous_asset_for_lifecycle(
    asset: dict[str, Any],
    previous_assets: list[dict[str, Any]],
) -> dict[str, Any] | None:
    candidates = [item for item in previous_assets if isinstance(item, dict)]
    asset_id = str(asset.get("asset_id") or asset.get("id") or "").strip()
    client_key = str(asset.get("client_asset_key") or "").strip()
    codes = {
        str(asset.get(key) or "").strip().upper()
        for key in ("asset_code", "agent_asset_code", "candidate_asset_code")
        if str(asset.get(key) or "").strip()
    }
    name = str(asset.get("name") or "").strip().casefold()

    for previous in candidates:
        previous_id = str(previous.get("asset_id") or previous.get("id") or "").strip()
        if asset_id and asset_id == previous_id:
            return previous
    for previous in candidates:
        previous_client_key = str(previous.get("client_asset_key") or "").strip()
        if client_key and client_key == previous_client_key:
            return previous
    for previous in candidates:
        previous_codes = {
            str(previous.get(key) or "").strip().upper()
            for key in ("asset_code", "agent_asset_code", "candidate_asset_code")
            if str(previous.get(key) or "").strip()
        }
        if codes & previous_codes:
            return previous

    # A type edit can also replace the formal code prefix. A unique name is the
    # final stable identity in that case; ambiguous names are intentionally not
    # guessed.
    same_name = [
        previous
        for previous in candidates
        if name and str(previous.get("name") or "").strip().casefold() == name
    ]
    if len(same_name) == 1:
        return same_name[0]
    return None


def _asset_prompt_input_projection(asset: dict[str, Any] | None) -> dict[str, Any]:
    if not isinstance(asset, dict):
        return {}

    def sanitize(value: Any) -> Any:
        if isinstance(value, dict):
            return {
                key: sanitized
                for key, nested in sorted(value.items())
                if key not in _PROMPT_NON_INPUT_FIELDS
                and (sanitized := sanitize(nested)) not in (None, "", [], {})
            }
        if isinstance(value, list):
            return [sanitized for nested in value if (sanitized := sanitize(nested)) not in (None, "", [], {})]
        return value

    return sanitize(asset)


def _asset_has_required_prompt_content(asset: dict[str, Any], projection: dict[str, Any]) -> bool:
    if not str(asset.get("name") or "").strip():
        return False
    optional_owner_fields = {"owner_character", "owner_character_id", "owner_role_code"}

    def without_optional_owner(value: Any) -> Any:
        if isinstance(value, dict):
            return {
                key: filtered
                for key, nested in value.items()
                if key not in optional_owner_fields
                and (filtered := without_optional_owner(nested)) not in (None, "", [], {})
            }
        if isinstance(value, list):
            return [filtered for nested in value if (filtered := without_optional_owner(nested)) not in (None, "", [], {})]
        return value

    required_projection = without_optional_owner(projection)
    meaningful = {
        key: value
        for key, value in required_projection.items()
        if key not in {"asset_type", "type", "name"} and value not in (None, "", [], {})
    }
    return bool(str(asset.get("description") or "").strip() or meaningful)


def _asset_has_prompt_evidence(asset: dict[str, Any], view: dict[str, Any]) -> bool:
    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}
    prompt_design = metadata.get("prompt_design") if isinstance(metadata.get("prompt_design"), dict) else {}
    if any((
        asset.get("prompt"), asset.get("prompt_text"), asset.get("input_hash"),
        prompt_design.get("prompt"), prompt_design.get("input_hash"),
    )):
        return True
    for key in ("asset_prompt_designs", "costume_designs"):
        for record in view.get(key) or []:
            if isinstance(record, dict) and not record.get("error") and prompt_record_matches_asset(record, asset):
                if any((record.get("prompt"), record.get("input_hash"))):
                    return True
    for key in ("asset_prompt_processing_summary", "costume_processing_summary"):
        for record in view.get(key) or []:
            if (
                isinstance(record, dict)
                and prompt_record_matches_asset(record, asset)
                and str(record.get("processing_status") or "").lower() == "reused"
            ):
                return True
    return False


def _asset_prompt_signature(asset: dict[str, Any] | None, view: dict[str, Any]) -> str:
    if not isinstance(asset, dict):
        return ""
    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}
    prompt_design = metadata.get("prompt_design") if isinstance(metadata.get("prompt_design"), dict) else {}
    prompt_fields = (
        "context_key",
        "prompt",
        "positive_prompt",
        "negative_prompt",
        "aspect_ratio",
        "material_layers",
        "character_apose",
        "output_spec",
        "generated_output_spec",
        "generated_view_codes",
        "deferred_view_codes",
        "generation_strategy",
        "view_prompts",
        "state_prompts",
        "spatial_bible",
        "material_bible",
        "brief_trace",
        # P2: resolved_production_brief 不再纳入提示词签名。它是生成输入而非输出，
        # brief 变更由 brief_current(_resolved_prompt_brief_matches_project) + fingerprint
        # 独立检测；纳入签名会使 design 去内嵌后新旧签名不一致而误判重生成。
        "production_context",
        "input_snapshot",
        "skill",
        "skill_version",
        "contract_version",
        "contract_hash",
        "prompt_version",
        "input_hash",
        "context_schema_version",
        "source_type",
    )
    records = [
        record
        for key in ("asset_prompt_designs", "costume_designs")
        for record in (view.get(key) or [])
        if isinstance(record, dict) and prompt_record_matches_asset(record, asset)
    ]
    records.sort(key=lambda record: (
        str(record.get("context_key") or ""),
        str(record.get("asset_code") or record.get("character_id") or ""),
        str(record.get("asset_name") or record.get("character_name") or record.get("name") or ""),
    ))
    record_prompt = next(
        (record.get("prompt") for record in records if record.get("prompt") not in (None, "")),
        None,
    )
    signature: dict[str, Any] = {}
    for key in prompt_fields:
        value = asset.get(key) or prompt_design.get(key)
        if key == "prompt" and record_prompt and _is_derived_asset_prompt(value, record_prompt):
            value = record_prompt
        if value not in (None, "", [], {}):
            signature[key] = value
    signature["records"] = [
        {
            key: record.get(key)
            for key in prompt_fields
            if record.get(key) not in (None, "", [], {})
        }
        for record in records
    ]
    return _content_hash(signature) if any(value not in (None, "", [], {}) for value in signature.values()) else ""


def _is_derived_asset_prompt(value: Any, canonical_prompt: Any) -> bool:
    """Ignore prompt text expanded with backend-only negative/hint sections."""
    text = str(value or "").strip()
    canonical = str(canonical_prompt or "").strip()
    if not text or not canonical or text == canonical or not text.startswith(canonical):
        return False
    suffix = text[len(canonical):]
    return "负向要求：" in suffix or "生产补充提示：" in suffix


def prompt_record_matches_asset(record: dict[str, Any], asset: dict[str, Any]) -> bool:
    asset_key = str(asset.get("client_asset_key") or "").strip()
    record_key = str(record.get("client_asset_key") or "").strip()
    if asset_key and record_key:
        return asset_key == record_key
    asset_code = str(asset.get("asset_code") or "").strip().upper()
    record_codes = {
        str(record.get(key) or "").strip().upper()
        for key in ("asset_code", "matched_asset_code")
        if str(record.get(key) or "").strip()
    }
    return bool(asset_code and asset_code in record_codes)


def assets_missing_prompt_design(raw_assets: list[Any], view: dict[str, Any]) -> list[str]:
    designs = [
        item
        for key in ("asset_prompt_designs", "costume_designs")
        for item in (view.get(key) or [])
        if isinstance(item, dict)
    ]
    summaries = [
        item
        for key in ("asset_prompt_processing_summary", "costume_processing_summary")
        for item in (view.get(key) or [])
        if isinstance(item, dict)
    ]
    missing: list[str] = []
    for raw in raw_assets:
        if not isinstance(raw, dict):
            continue
        if str(raw.get("asset_type") or raw.get("type") or "").strip().lower() == "character" and _character_is_non_visual(raw):
            continue
        if _asset_lifecycle_status(raw) in {"needs_completion", "prompt_pending"}:
            missing.append(_asset_lifecycle_label(raw))
            continue
        metadata = raw.get("metadata") if isinstance(raw.get("metadata"), dict) else {}
        prompt_design = metadata.get("prompt_design") if isinstance(metadata.get("prompt_design"), dict) else {}
        prompt_lineage = metadata.get("prompt_lineage") if isinstance(metadata.get("prompt_lineage"), dict) else {}
        if any((
            raw.get("prompt"), raw.get("input_hash"), raw.get("skill"),
            prompt_design.get("prompt"), prompt_lineage.get("skill"),
        )):
            continue
        matched_design = next((item for item in designs if prompt_record_matches_asset(item, raw)), None)
        if matched_design and not matched_design.get("error") and any((
            matched_design.get("prompt"),
            matched_design.get("input_hash"),
            matched_design.get("skill"),
        )):
            continue
        matched_summary = next((item for item in summaries if prompt_record_matches_asset(item, raw)), None)
        if matched_summary and str(matched_summary.get("processing_status") or "").lower() == "reused":
            continue
        missing.append(_asset_lifecycle_label(raw))
    return missing


def assets_not_confirmed(raw_assets: list[Any]) -> list[str]:
    return [
        _asset_lifecycle_label(raw)
        for raw in raw_assets
        if isinstance(raw, dict)
        and not (
            str(raw.get("asset_type") or raw.get("type") or "").strip().lower() == "character"
            and _character_is_non_visual(raw)
        )
        and _asset_lifecycle_status(raw) != "confirmed"
    ]


def _asset_lifecycle_label(asset: dict[str, Any]) -> str:
    code = str(asset.get("asset_code") or "").strip()
    short_code = code.rsplit("-", 1)[-1] if code else ""
    return " ".join(
        value for value in (short_code, str(asset.get("name") or "").strip()) if value
    ) or "未命名资产"


def _require_asset_prompt_designs(content: dict[str, Any]) -> None:
    assets = [item for item in (content.get("assets") or []) if isinstance(item, dict)]
    if not assets:
        raise ValueError("当前没有已确认正式资产，不能进入脚本段、分镜或任务阶段。")
    raw_output = content.get("raw_output") if isinstance(content.get("raw_output"), dict) else {}
    missing = assets_missing_prompt_design(assets, raw_output)
    if missing:
        raise ValueError(f"以下资产尚未经过专用 Prompt Skill：{'、'.join(missing)}。请先补齐资产 Prompt。")
    unconfirmed = assets_not_confirmed(assets)
    if unconfirmed:
        raise ValueError(f"以下资产尚未完成人工确认：{'、'.join(unconfirmed)}。请逐项确认后再继续。")


async def _require_asset_prompt_designs_from_content(
    session: AsyncSession,
    content: dict[str, Any],
    user: User,
    *,
    project_id: UUID,
) -> None:
    """Gate: every visual asset must have a prompt design before segment/lock.

    The prompt designs live in the DB-authoritative breakdown view
    (``asset_prompt_designs``), NOT in the human-confirm request payload —
    ``breakdownPayload`` deliberately drops them (they are Agent-derived,
    display-only fields) and ``_clean_normalized_workflow_value`` strips
    ``raw_output``. So the legacy sync gate (which reads ``content.raw_output``)
    always sees an empty view and falsely flags every visual asset as missing.
    When the request carries scope (episode_id + script_version_id, sent by the
    frontend), delegate to the scope gate that loads the authoritative view and
    confirmed inventory from the DB (same contract as the storyboard-video gate).
    Fall back to the sync content gate only for legacy episodes without scope.
    """
    episode_id = _uuid_or_none(content.get("episode_id"))
    script_version_id = _uuid_or_none(content.get("script_version_id"))
    if episode_id is not None and script_version_id is not None:
        await _require_asset_prompt_designs_for_scope(
            session,
            project_id=project_id,
            episode_id=episode_id,
            script_version_id=script_version_id,
            user=user,
        )
        return
    _require_asset_prompt_designs(content)


async def _upsert_storyboards_from_breakdown(
    session: AsyncSession,
    project: Project,
    content: dict[str, Any],
    user: User,
) -> int:
    storyboard_items = [
        item for item in list(content.get("storyboards") or []) if isinstance(item, dict)
    ]
    segment_ids_by_code, scenes_by_code = await _resolve_breakdown_storyboard_references(
        session,
        project,
        storyboard_items,
    )
    created_or_updated = 0
    for index, item in enumerate(storyboard_items, start=1):
        episode_code = _storyboard_episode_code(item)
        episode_num = _episode_num_from_code(episode_code)
        order_num = _safe_int(item.get("order_num") or item.get("order_no") or item.get("shot_no"), index)
        script_segment_id = _resolved_segment_reference(
            segment_ids_by_code,
            item.get("script_segment_code"),
            episode_code,
        )
        storyboard = await session.scalar(
            select(Storyboard).where(
                Storyboard.project_id == project.id,
                Storyboard.episode_num == episode_num,
                Storyboard.order_num == order_num,
            )
        )
        description = str(item.get("description") or item.get("content") or item.get("source_text") or item.get("title") or "")
        scene_code, scene_name = _storyboard_scene_code_name(item)
        resolved_scene = _resolved_reference_value(scenes_by_code, scene_code)
        if resolved_scene is not None:
            scene_code, scene_name = resolved_scene
            item["scene_code"] = scene_code
            item["scene_name"] = scene_name
        if script_segment_id is not None:
            item["script_segment_id"] = str(script_segment_id)
        if storyboard is None:
            storyboard = Storyboard(
                project_id=project.id,
                script_segment_id=script_segment_id,
                episode_num=episode_num,
                order_num=order_num,
                title=item.get("title") or item.get("shot_no") or f"分镜 {order_num:03d}",
                description=description or "未提供分镜描述",
                dialogue=item.get("dialogue"),
                narration=item.get("narration"),
                camera=item.get("camera"),
                shot_type=item.get("shot_type") or item.get("shot_size"),
                duration_seconds=_safe_int(item.get("duration_seconds"), 10),
                characters=list(item.get("characters") or item.get("character_codes") or []),
                keyframes=list(item.get("keyframes") or [{"order": 1, "description": description[:120]}]),
                mirror_shots=list(item.get("mirror_shots") or []),
                storyboard_code=item.get("storyboard_code") or f"{project.project_prefix or 'RF'}-{episode_code}-F{order_num:03d}",
                scene_code=scene_code,
                scene_name=scene_name,
                context_code=item.get("context_code"),
                render_mode=item.get("render_mode"),
                status=TaskStatus.todo,
            )
            session.add(storyboard)
            await session.flush()
        else:
            storyboard.script_segment_id = script_segment_id or storyboard.script_segment_id
            storyboard.title = item.get("title") or storyboard.title
            storyboard.description = description or storyboard.description
            storyboard.dialogue = item.get("dialogue")
            storyboard.narration = item.get("narration")
            storyboard.camera = item.get("camera") or storyboard.camera
            storyboard.shot_type = item.get("shot_type") or item.get("shot_size") or storyboard.shot_type
            storyboard.duration_seconds = _safe_int(item.get("duration_seconds"), storyboard.duration_seconds or 10)
            storyboard.characters = list(item.get("characters") or item.get("character_codes") or storyboard.characters or [])
            storyboard.keyframes = list(item.get("keyframes") or storyboard.keyframes or [])
            storyboard.mirror_shots = list(item.get("mirror_shots") or [])
            storyboard.scene_code = scene_code or storyboard.scene_code
            storyboard.scene_name = scene_name or storyboard.scene_name
            storyboard.context_code = item.get("context_code") or storyboard.context_code
            storyboard.render_mode = item.get("render_mode") or storyboard.render_mode
            storyboard.updated_at = datetime.now(UTC)
        item["storyboard_id"] = str(storyboard.id)
        await create_entity_version(
            session,
            EntityVersionCreate(
                project_id=project.id,
                entity_type="storyboard",
                entity_id=storyboard.id,
                entity_code=storyboard.storyboard_code or f"{project.id}-F-{episode_num}-{order_num}",
                source="human_modified",
                status="locked",
                content_json=item,
                change_summary="人工修正分镜锁定",
                changed_fields=["storyboard"],
                created_by=user.id,
            ),
            commit=False,
        )
        created_or_updated += 1
    return created_or_updated


def _reference_code_aliases(value: Any) -> set[str]:
    code = str(value or "").strip().upper()
    if not code:
        return set()
    aliases = {code}
    suffix_match = re.search(r"(?:^|-)((?:EP\d+-)?J\d+|SC\d+)$", code)
    if suffix_match:
        aliases.add(suffix_match.group(1))
        if suffix_match.group(1).startswith("EP"):
            aliases.add(suffix_match.group(1).split("-")[-1])
    return aliases


def _resolved_reference_value(mapping: dict[str, Any], value: Any) -> Any:
    return next((mapping[alias] for alias in _reference_code_aliases(value) if alias in mapping), None)


def _resolved_segment_reference(mapping: dict[str, UUID], value: Any, episode_code: str) -> UUID | None:
    code = str(value or "").strip().upper()
    if re.fullmatch(r"J\d+", code):
        episode_value = mapping.get(f"{episode_code}-{code}")
        if episode_value is not None:
            return episode_value
    return _resolved_reference_value(mapping, code)


async def _resolve_breakdown_storyboard_references(
    session: AsyncSession,
    project: Project,
    storyboard_items: list[dict[str, Any]],
) -> tuple[dict[str, UUID], dict[str, tuple[str, str]]]:
    """Resolve human-final storyboard references against materialized project records."""
    segment_codes = {
        str(item.get("script_segment_code") or "").strip()
        for item in storyboard_items
        if str(item.get("script_segment_code") or "").strip()
    }
    scene_codes = {
        scene_code
        for item in storyboard_items
        if (scene_code := _storyboard_scene_code_name(item)[0])
    }

    segment_rows = []
    if segment_codes:
        segment_rows = (
            await session.execute(
                select(ScriptSegment.script_segment_code, ScriptSegment.id).where(
                    ScriptSegment.project_id == project.id,
                )
            )
        ).all()
    scene_rows = []
    if scene_codes:
        scene_rows = (
            await session.execute(
                select(Asset.asset_code, Asset.name).where(
                    Asset.project_id == project.id,
                    Asset.asset_type == AssetType.scene,
                    Asset.status.notin_(["deleted", "excluded"]),
                )
            )
        ).all()

    segment_ids_by_code: dict[str, UUID] = {}
    for code, segment_id in segment_rows:
        for alias in _reference_code_aliases(code):
            segment_ids_by_code.setdefault(alias, segment_id)
    scenes_by_code: dict[str, tuple[str, str]] = {}
    for code, name in scene_rows:
        if not code or not name:
            continue
        resolved = (str(code).strip(), str(name).strip())
        for alias in _reference_code_aliases(code):
            scenes_by_code.setdefault(alias, resolved)
    return segment_ids_by_code, scenes_by_code


async def _create_asset_text_to_image_tasks(
    session: AsyncSession,
    project: Project,
    content: dict[str, Any],
    user: User,
) -> int:
    content = _merge_asset_task_context(content)
    created = 0
    assignee_id = await _first_user_id_by_role(session, UserRole.artist)
    now = datetime.now(UTC)
    for index, item in enumerate(list(content.get("assets") or []), start=1):
        if not isinstance(item, dict):
            continue
        asset_type = _asset_type_enum(item.get("asset_type"))
        if asset_type == AssetType.character and _character_is_non_visual(item):
            # Keep the formal asset in the locked snapshot, but defer any visual
            # task until a later episode marks the character as on-screen.
            item["task_ids"] = {}
            item["task_id"] = None
            continue
        asset_code = str(item.get("asset_code") or _asset_item_code(item, index))
        name = str(item.get("name") or f"资产 {index:03d}")
        description = _asset_description(item)
        asset = await session.scalar(select(Asset).where(Asset.project_id == project.id, Asset.asset_code == asset_code))
        if asset is None:
            asset = Asset(
                project_id=project.id,
                asset_code=asset_code,
                asset_type=asset_type,
                name=name,
                description=description,
                status="locked",
                tags=[asset_type.value, "human_modified"],
                prompt_text=_asset_prompt_text(item),
                base_model=str(item.get("model") or "lib-image"),
                metadata_json=_normalized_asset_metadata(item),
                is_locked=True,
                locked_by=user.id,
                locked_at=now,
                created_by_id=user.id,
            )
            session.add(asset)
            await session.flush()
        prompt_text = _asset_prompt_text(item)
        existing_tasks = (
            await session.execute(
                select(Task).where(
                    Task.project_id == project.id,
                    Task.task_type == TaskType.text_to_image,
                    Task.asset_id == asset.id,
                    Task.is_retired.is_(False),
                )
            )
        ).scalars().all()
        legacy_title = f"资产生产 {asset_code} {name} 文生图"
        legacy_task = await session.scalar(
            select(Task).where(
                Task.project_id == project.id,
                Task.task_type == TaskType.text_to_image,
                Task.title == legacy_title,
                Task.is_retired.is_(False),
            )
        )
        if legacy_task is not None and not existing_tasks:
            existing_tasks = [legacy_task]
        existing_by_variant = {str(task.task_variant): task for task in existing_tasks if task.task_variant}
        primary_task = existing_by_variant.get("A")
        task_ids: dict[str, str] = {}
        if any(task.task_variant is None for task in existing_tasks):
            task_ids["LEGACY"] = str(existing_tasks[0].id)
        else:
            for variant, variant_label, instruction in _asset_task_specs(asset):
                task = existing_by_variant.get(variant)
                if task is None:
                    variant_prompt = _asset_item_variant_prompt(item, prompt_text, variant, instruction)
                    task = Task(
                        project_id=project.id,
                        asset_id=asset.id,
                        task_type=TaskType.text_to_image,
                        task_variant=variant,
                        depends_on_task_id=primary_task.id if primary_task and variant in {"B", "C", "D", "E"} else None,
                        title=f"资产生产 {asset_code} {name} [{variant}] {variant_label}",
                        assignee_id=assignee_id,
                        assigned_by=user.id,
                        assigned_at=now,
                        status=TaskStatus.todo,
                        prompt_text=variant_prompt,
                        latest_prompt_text=variant_prompt,
                        due_at=now + timedelta(days=1 if variant in {"A", "MASTER"} else 2),
                        production_model=str(item.get("model") or "lib-image"),
                    )
                    session.add(task)
                    await session.flush()
                    session.add(
                        TaskPrompt(
                            task_id=task.id,
                            prompt_type=f"asset_text_to_image_{variant.lower()}",
                            prompt_text=variant_prompt,
                            source="locked_asset_breakdown",
                            version_no=1,
                            created_by=user.id,
                        )
                    )
                    created += 1
                if variant == "A":
                    primary_task = task
                task_ids[variant] = str(task.id)
        item["asset_id"] = str(asset.id)
        item["task_ids"] = task_ids
        item["task_id"] = task_ids.get("A") or task_ids.get("MASTER") or task_ids.get("LEGACY")
    return created


def _character_is_non_visual(asset: dict[str, Any] | None) -> bool:
    if not isinstance(asset, dict):
        return False
    # Merge attributes + metadata so this matches the generation-side gate
    # (_character_requires_visual_master), which defers non-visual characters. The
    # visual_presence signal ("mentioned_only"/"voice_only") is stored under
    # attributes; reading metadata alone missed it and wrongly demanded a prompt for
    # deferred background characters, breaking prompt-batch distribution.
    metadata = {
        **(asset.get("attributes") if isinstance(asset.get("attributes"), dict) else {}),
        **(asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}),
    }
    value = asset.get("visual_presence") or asset.get("presence_mode") or asset.get("appearance_scope") or metadata.get("visual_presence")
    normalized = str(value or "").strip().lower().replace("-", "_").replace(" ", "_")
    if normalized in {"mentioned_only", "mentioned", "背景提及", "voice_only", "voice", "off_screen"} or any(
        marker in normalized for marker in ("仅提及", "未出场", "不出镜", "画外音", "仅声音")
    ):
        return True
    if normalized:
        return False
    character_type = asset.get("character_type") or metadata.get("character_type")
    type_text = str(character_type or "").strip().lower().replace("-", "_").replace(" ", "_")
    return any(
        marker in type_text
        for marker in ("mentioned_only", "背景提及", "仅提及", "未出场", "不出镜", "voice_only", "off_screen", "画外音", "仅声音")
    )


def _normalize_episode_code(value: Any) -> str | None:
    if value is None or str(value).strip() == "":
        return None
    code = str(value).strip().upper()
    if code.isdigit():
        return f"EP{int(code):02d}"
    if not code.startswith("EP"):
        return f"EP{code}"
    return code


def _storyboard_episode_code(item: dict[str, Any]) -> str:
    code = _normalize_episode_code(item.get("episode_code"))
    if code:
        return code
    return f"EP{_safe_int(item.get('episode_num'), 1):02d}"


def _episode_num_from_code(code: str | None) -> int:
    normalized = _normalize_episode_code(code) or "EP01"
    return _safe_int(normalized.replace("EP", ""), 1)


def _asset_belongs_to_episode(item: dict[str, Any], episode_code: str) -> bool:
    for key in ("episode_code", "corresponding_episode", "episode"):
        if _normalize_episode_code(item.get(key)) == episode_code:
            return True
    related = item.get("related_storyboard_codes") or item.get("storyboard_codes") or item.get("corresponding_storyboards") or []
    if isinstance(related, str):
        related = [related]
    if related and any(episode_code in str(value).upper() for value in related):
        return True
    return not any(item.get(key) for key in ("episode_code", "corresponding_episode", "episode"))


def _storyboard_scene_codes(item: dict[str, Any]) -> set[str]:
    values = item.get("scene_codes") or item.get("scene_code") or item.get("scene_id") or item.get("scene_name") or []
    if isinstance(values, str):
        values = [values]
    return {str(value).strip() for value in values if str(value).strip()}


def _storyboard_scene_code_name(item: dict[str, Any]) -> tuple[str | None, str | None]:
    """Extract a single scene code + display name from a breakdown storyboard item."""
    raw_code = item.get("scene_code") or item.get("scene_id") or item.get("scene_codes")
    if isinstance(raw_code, (list, tuple)):
        raw_code = raw_code[0] if raw_code else None
    code = str(raw_code).strip() if raw_code not in (None, "") else None
    raw_name = item.get("scene_name") or item.get("scene")
    if isinstance(raw_name, (list, tuple)):
        raw_name = raw_name[0] if raw_name else None
    name = str(raw_name).strip() if raw_name not in (None, "") else None
    return code, name


def _normalize_asset_type(value: Any) -> str:
    raw = str(value or "prop").strip().lower()
    mapping = {
        "人物": "character",
        "角色": "character",
        "character": "character",
        "scene": "scene",
        "场景": "scene",
        "prop": "prop",
        "道具": "prop",
    }
    return mapping.get(raw, raw if raw in {"character", "scene", "prop"} else "prop")


def _asset_type_enum(value: Any) -> AssetType:
    normalized = _normalize_asset_type(value)
    if normalized == "character":
        return AssetType.character
    if normalized == "scene":
        return AssetType.scene
    return AssetType.prop


async def _reserved_project_asset_codes(session: AsyncSession, project: Project) -> set[str]:
    prefix = _safe_code_prefix(project.project_prefix or project.project_no)
    values: set[str] = set()
    persisted_codes = await session.scalars(
        select(Asset.asset_code).where(Asset.project_id == project.id, Asset.asset_code.is_not(None))
    )
    values.update(str(value) for value in persisted_codes.all() if value)

    breakdown_contents = await session.scalars(
        select(ScriptBreakdown.content_json).where(ScriptBreakdown.project_id == project.id)
    )
    for content in breakdown_contents.all():
        values.update(_formal_asset_codes_in_payload(content, prefix=prefix))

    inventory_contents = await session.scalars(
        select(ArtifactRevision.normalized_content).where(
            ArtifactRevision.project_id == project.id,
            ArtifactRevision.artifact_type == "asset_inventory",
        )
    )
    for content in inventory_contents.all():
        values.update(_formal_asset_codes_in_payload(content, prefix=prefix))
    return values


def _formal_asset_codes_in_payload(value: Any, *, prefix: str) -> set[str]:
    codes: set[str] = set()

    def scan(current: Any) -> None:
        if isinstance(current, dict):
            for key, nested in current.items():
                if key in {"asset_code", "character_code", "scene_code", "prop_code"}:
                    code = _canonical_formal_asset_code(nested, prefix=prefix)
                    if code:
                        codes.add(code)
                elif isinstance(nested, (dict, list)):
                    scan(nested)
        elif isinstance(current, list):
            for nested in current:
                scan(nested)

    scan(value)
    return codes


def _existing_asset_item_code(item: dict[str, Any]) -> str | None:
    existing = item.get("asset_code") or item.get("character_code") or item.get("scene_code") or item.get("prop_code")
    if existing:
        return str(existing)
    return None


def _canonical_formal_asset_code(
    value: Any,
    *,
    prefix: str,
    asset_type: str | None = None,
) -> str | None:
    match = re.fullmatch(
        rf"{re.escape(_safe_code_prefix(prefix))}-(R|SC|S|P)(\d+)",
        str(value or "").strip().upper(),
    )
    if not match:
        return None
    code_prefix, number = match.groups()
    normalized_type = {"R": "character", "SC": "scene", "S": "scene", "P": "prop"}[code_prefix]
    if asset_type and _normalize_asset_type(asset_type) != normalized_type:
        return None
    canonical_prefix = {"character": "R", "scene": "SC", "prop": "P"}[normalized_type]
    return f"{_safe_code_prefix(prefix)}-{canonical_prefix}{int(number):03d}"


def _formal_asset_code_parts(value: Any, *, prefix: str) -> tuple[str, int]:
    code = _canonical_formal_asset_code(value, prefix=prefix)
    if not code:
        return "", 0
    match = re.fullmatch(r"[A-Z0-9]+-(R|SC|P)(\d+)", code)
    if not match:
        return "", 0
    return {"R": "character", "SC": "scene", "P": "prop"}[match.group(1)], int(match.group(2))


def _is_trusted_formal_asset_code(item: dict[str, Any]) -> bool:
    if str(item.get("asset_code_source") or "").strip().lower() == "agent_normalized":
        return False
    repairs = item.get("_contract_repairs") or []
    return "asset_code" not in repairs


def _asset_item_code(item: dict[str, Any], index: int, *, project_prefix: str | None = None) -> str:
    existing = _existing_asset_item_code(item)
    if existing and not project_prefix:
        return existing
    if existing and project_prefix:
        canonical = re.fullmatch(
            rf"{re.escape(_safe_code_prefix(project_prefix))}-(?:R|S|SC|P)\d+",
            str(existing).strip().upper(),
        )
        if canonical:
            return str(existing).strip().upper()
    prefix = {"character": "R", "scene": "SC", "prop": "P"}.get(_normalize_asset_type(item.get("asset_type")), "P")
    if project_prefix:
        return f"{_safe_code_prefix(project_prefix)}-{prefix}{index:03d}"
    return f"{prefix}{index:03d}"


def _safe_code_prefix(value: Any) -> str:
    prefix = re.sub(r"[^A-Z0-9]", "", str(value or "").strip().upper())
    return prefix or "RF"


def _dep_token(value: Any) -> str:
    return str(value or "").strip().lower()


async def compute_scene_asset_dependencies(
    session: AsyncSession, project_id: UUID
) -> dict[str, dict[str, Any]]:
    """Per-scene asset gating map.

    For each scene, the asset codes that must be completed before that scene's
    storyboard tasks can start = the scene asset itself + the character/prop assets
    that appear in the scene's storyboards (matched by asset_code or name) + the
    scene's own key props from its metadata.

    Returns {scene_code: {"scene_name": str|None, "required_asset_codes": [...]}}.
    """
    assets = (
        await session.execute(
            select(Asset).where(
                Asset.project_id == project_id,
                Asset.status.notin_(["deleted", "excluded"]),
            )
        )
    ).scalars().all()
    storyboards = (
        await session.execute(select(Storyboard).where(Storyboard.project_id == project_id))
    ).scalars().all()

    char_prop_by_token: dict[str, str] = {}
    scene_assets: list[Asset] = []

    def _index_asset(token: str, code: str) -> None:
        token = _dep_token(token)
        if token:
            char_prop_by_token.setdefault(token, code)

    for asset in assets:
        if asset.asset_type == AssetType.scene:
            scene_assets.append(asset)
            continue
        if asset.asset_type in (AssetType.character, AssetType.prop):
            code = asset.asset_code or asset.name
            if not code:
                continue
            if asset.asset_code:
                # index full code AND the suffix after the project prefix (e.g. ZSH-R001 -> r001),
                # since storyboards often reference bare codes like "R001".
                char_prop_by_token[_dep_token(asset.asset_code)] = asset.asset_code
                _index_asset(asset.asset_code.split("-")[-1], asset.asset_code)
            if asset.name:
                _index_asset(asset.name, code)

    result: dict[str, dict[str, Any]] = {}
    for scene in scene_assets:
        scene_code = scene.asset_code or scene.name
        if not scene_code:
            continue
        scene_tokens = {tok for tok in (_dep_token(scene.asset_code), _dep_token(scene.name)) if tok}
        required: set[str] = set()
        if scene.asset_code:
            required.add(scene.asset_code)
        meta = scene.metadata_json or {}
        breakdown_meta = meta.get("breakdown_asset") if isinstance(meta.get("breakdown_asset"), dict) else {}
        for source in (meta, breakdown_meta):
            for key in ("key_prop_ids", "key_props"):
                for value in source.get(key) or []:
                    code = char_prop_by_token.get(_dep_token(value))
                    if code:
                        required.add(code)
        for storyboard in storyboards:
            sb_tokens = {tok for tok in (_dep_token(storyboard.scene_code), _dep_token(storyboard.scene_name)) if tok}
            if not (sb_tokens & scene_tokens):
                continue
            for token in storyboard.characters or []:
                code = char_prop_by_token.get(_dep_token(token))
                if code:
                    required.add(code)
        result[scene_code] = {
            "scene_name": scene.name,
            "required_asset_codes": sorted(required),
        }
    return result


# ---------------------------------------------------------------------------
# Two-phase task assignment (assets first, then scene-gated storyboard shots)
# ---------------------------------------------------------------------------

async def _materialize_assets_from_view(
    session: AsyncSession, project: Project, view: dict[str, Any], user: User
) -> int:
    """Ensure Asset rows exist for every asset in the breakdown view. Returns created count."""
    created = 0
    now = datetime.now(UTC)
    scope_key = _breakdown_asset_scope_key(view)
    review_state = view.get("asset_review_state") if isinstance(view.get("asset_review_state"), dict) else {}
    inventory_confirmed = str(review_state.get("status") or "") == "confirmed"
    for index, item in enumerate(list(view.get("assets") or []), start=1):
        if not isinstance(item, dict):
            continue
        asset_type = _asset_type_enum(item.get("asset_type"))
        asset_code = str(item.get("asset_code") or _asset_item_code(item, index))
        name = str(item.get("name") or f"资产 {index:03d}")
        lifecycle_status = "confirmed" if inventory_confirmed else _asset_lifecycle_status(item)
        item_metadata = _normalized_asset_metadata(item)
        item_metadata["confirmation_status"] = lifecycle_status
        if scope_key:
            item_metadata["breakdown_scope_keys"] = sorted({
                *[str(value) for value in item_metadata.get("breakdown_scope_keys") or [] if str(value)],
                scope_key,
            })
        item_metadata.setdefault(
            "prompt_input_fingerprint",
            _content_hash(_asset_prompt_input_projection(item)),
        )
        asset = await session.scalar(
            select(Asset).where(Asset.project_id == project.id, Asset.asset_code == asset_code)
        )
        if asset is None:
            asset = Asset(
                project_id=project.id,
                asset_code=asset_code,
                asset_type=asset_type,
                name=name,
                description=_asset_description(item),
                status=lifecycle_status,
                tags=[asset_type.value],
                prompt_text=None,
                base_model=str(item.get("model") or "Seedream"),
                metadata_json=item_metadata,
                created_by_id=user.id,
            )
            session.add(asset)
            await session.flush()
            created += 1
        elif not asset.is_locked:
            existing_metadata = _metadata_without_breakdown_asset(dict(asset.metadata_json or {}))
            if scope_key:
                item_metadata["breakdown_scope_keys"] = sorted({
                    *[str(value) for value in existing_metadata.get("breakdown_scope_keys") or [] if str(value)],
                    *[str(value) for value in item_metadata.get("breakdown_scope_keys") or [] if str(value)],
                    scope_key,
                })
            asset.name = name
            asset.description = _asset_description(item)
            asset.base_model = str(item.get("model") or asset.base_model or "Seedream")
            asset.metadata_json = {
                **existing_metadata,
                **item_metadata,
            }
        if not asset.is_locked:
            _set_asset_row_lifecycle(
                asset,
                lifecycle_status,
                prompt_validity=str(item_metadata.get("prompt_validity") or "") or None,
                prompt_input_fingerprint=str(item_metadata.get("prompt_input_fingerprint") or "") or None,
                prompt_generated_for_fingerprint=str(item_metadata.get("prompt_generated_for_fingerprint") or "") or None,
                invalidated_fields=list(item_metadata.get("prompt_invalidated_fields") or []),
            )
        if not asset.is_locked:
            await _append_asset_revision(session, asset, source_type="human", created_by=user.id)
    return created


async def _sync_materialized_assets_from_breakdown(
    session: AsyncSession,
    project: Project,
    content: dict[str, Any],
    user: User,
) -> None:
    """Keep already-materialized Asset rows aligned with a saved human draft."""
    items = [dict(item) for item in content.get("assets") or [] if isinstance(item, dict)]
    review_state = content.get("asset_review_state") if isinstance(content.get("asset_review_state"), dict) else {}
    inventory_confirmed = str(review_state.get("status") or "") == "confirmed"
    scope_key = _breakdown_asset_scope_key(content)
    rows = list((await session.scalars(
        select(Asset).where(
            Asset.project_id == project.id,
            Asset.asset_type.in_(_VISUAL_ASSET_TYPES),
            Asset.status.notin_(["deleted", "excluded"]),
        )
    )).all())
    row_payloads = [{
        "id": str(row.id),
        "asset_id": str(row.id),
        "asset_code": row.asset_code,
        "asset_type": row.asset_type.value if hasattr(row.asset_type, "value") else str(row.asset_type),
        "name": row.name,
    } for row in rows]
    rows_by_id = {str(row.id): row for row in rows}
    matched_row_ids: set[UUID] = set()
    for item in items:
        matched = _previous_asset_for_lifecycle(item, row_payloads)
        asset = rows_by_id.get(str((matched or {}).get("asset_id") or ""))
        if asset is None:
            continue
        matched_row_ids.add(asset.id)
        metadata = {
            **_metadata_without_breakdown_asset(dict(asset.metadata_json or {})),
            **_normalized_asset_metadata(item),
        }
        if scope_key:
            metadata["breakdown_scope_keys"] = sorted({
                *[str(value) for value in metadata.get("breakdown_scope_keys") or [] if str(value)],
                scope_key,
            })
        lifecycle_status = "confirmed" if inventory_confirmed else _asset_lifecycle_status(item)
        metadata["confirmation_status"] = lifecycle_status
        asset.asset_code = str(item.get("asset_code") or asset.asset_code)
        asset.asset_type = _asset_type_enum(item.get("asset_type") or item.get("type"))
        asset.name = str(item.get("name") or asset.name)
        asset.description = _asset_description(item)
        asset.metadata_json = metadata
        _set_asset_row_lifecycle(
            asset,
            lifecycle_status,
            prompt_validity=str(metadata.get("prompt_validity") or "") or None,
            prompt_input_fingerprint=str(metadata.get("prompt_input_fingerprint") or "") or None,
            prompt_generated_for_fingerprint=str(metadata.get("prompt_generated_for_fingerprint") or "") or None,
            invalidated_fields=list(metadata.get("prompt_invalidated_fields") or []),
        )
        revision = await _append_asset_revision(
            session,
            asset,
            source_type="human",
            created_by=user.id,
        )
        await _propagate_asset_revision_to_tasks(session, asset, revision)

    if scope_key:
        for asset in rows:
            if asset.id in matched_row_ids or asset.is_locked:
                continue
            metadata = dict(asset.metadata_json or {})
            scope_keys = {
                str(value) for value in metadata.get("breakdown_scope_keys") or [] if str(value)
            }
            if scope_key not in scope_keys:
                continue
            scope_keys.discard(scope_key)
            metadata["breakdown_scope_keys"] = sorted(scope_keys)
            if not scope_keys:
                asset.status = "excluded"
                metadata.update({
                    "confirmation_status": "excluded",
                    "exclusion_reason": "human_removed_from_breakdown",
                    "excluded_at": datetime.now(UTC).isoformat(),
                })
            asset.metadata_json = metadata


def _breakdown_asset_scope_key(content: dict[str, Any]) -> str:
    episode_id = str(content.get("episode_id") or "").strip()
    script_version_id = str(content.get("script_version_id") or "").strip()
    if not episode_id or not script_version_id:
        return ""
    return f"episode:{episode_id}:script-version:{script_version_id}"


async def _task_generation_assets_without_bindings(
    session: AsyncSession,
    project: Project,
    episode: ProjectEpisode,
    script_version_id: UUID,
    user: User,
) -> list[Asset]:
    inventory_items = await confirmed_asset_inventory(
        session,
        project.id,
        episode_id=episode.id,
        script_version_id=script_version_id,
        strict_scope=True,
    )
    task_assets = [dict(item) for item in inventory_items if isinstance(item, dict)]
    if not task_assets:
        raise ValueError("当前分集没有已确认的正式资产清单，不能生成资产任务。")
    await _materialize_assets_from_view(
        session,
        project,
        {
            "assets": task_assets,
            "episode_id": str(episode.id),
            "script_version_id": str(script_version_id),
            "asset_review_state": {"status": "confirmed"},
        },
        user,
    )
    await session.flush()
    asset_codes = {str(item.get("asset_code") or "").strip() for item in task_assets}
    asset_codes.discard("")
    if not asset_codes:
        return []
    return list((await session.scalars(
        select(Asset).where(
            Asset.project_id == project.id,
            Asset.asset_code.in_(asset_codes),
        ).order_by(Asset.asset_code)
    )).all())


async def _append_asset_revision(
    session: AsyncSession,
    asset: Asset,
    *,
    source_type: str,
    created_by: UUID | None,
    source_run_id: UUID | None = None,
) -> ArtifactRevision:
    content = _asset_revision_content(asset)
    digest = _content_hash(content)
    current = await session.get(ArtifactRevision, asset.current_revision_id) if asset.current_revision_id else None
    if current is not None and current.content_hash == digest:
        return current
    version_no = (
        await session.scalar(
            select(func.max(ArtifactRevision.version_no)).where(
                ArtifactRevision.artifact_type == "asset", ArtifactRevision.artifact_id == asset.id
            )
        )
        or 0
    ) + 1
    revision = ArtifactRevision(
        project_id=asset.project_id,
        artifact_type="asset",
        artifact_id=asset.id,
        version_no=version_no,
        parent_revision_id=asset.current_revision_id,
        source_type=source_type,
        source_run_id=source_run_id,
        normalized_content=content,
        change_diff={"previous_content_hash": current.content_hash if current else None},
        content_hash=digest,
        status=_asset_confirmation_status(asset),
        created_by=created_by,
    )
    session.add(revision)
    await session.flush()
    asset.current_revision_id = revision.id
    asset.version = max(asset.version, version_no)
    if revision.status == "confirmed":
        parent_content = current.normalized_content if current is not None else {}
        session.add(
            AgentTrainingSample(
                project_id=asset.project_id,
                agent_type=None,
                sample_type="asset_revision_confirmed",
                entity_type="asset",
                entity_code=asset.asset_code or str(asset.id),
                input_json=current.input_snapshot if current is not None else {},
                agent_output_json=parent_content,
                human_modified_output_json=content,
                confirmed_output_json=content,
                change_summary=[f"资产修订 v{version_no} 已人工确认"],
                training_tags=["asset_revision", "human_confirmed", source_type],
                training_ready=True,
                lineage_key=_training_lineage_key_asset(asset.project_id, asset.asset_code or str(asset.id)),
                created_by=created_by,
            )
        )
    return revision


async def _append_prompt_revision(
    session: AsyncSession,
    asset: Asset,
    item: dict[str, Any],
    *,
    source_run_id: UUID | None,
    created_by: UUID | None,
    rerun_of_revision_id: UUID | None = None,
) -> ArtifactRevision | None:
    metadata = item.get("metadata") if isinstance(item.get("metadata"), dict) else {}
    design = metadata.get("prompt_design") if isinstance(metadata.get("prompt_design"), dict) else {}
    if not design and any(item.get(key) not in (None, "", [], {}) for key in ("prompt", "negative_prompt", "view_prompts", "state_prompts")):
        design = {
            key: item.get(key)
            for key in (
                "prompt", "negative_prompt", "aspect_ratio", "view_prompts", "state_prompts",
                "spatial_bible", "material_bible", "brief_trace", "skill", "skill_version",
                "contract_version", "contract_hash", "prompt_version", "input_hash",
            )
            if item.get(key) not in (None, "", [], {})
        }
    if not design:
        return None
    production_context = (
        metadata.get("production_context")
        or item.get("production_context")
        or item.get("input_snapshot")
        or {}
    )
    if not isinstance(production_context, dict):
        production_context = {}
    resolved_brief = (
        design.get("resolved_production_brief")
        or metadata.get("resolved_production_brief")
        or item.get("resolved_production_brief")
        or {}
    )
    if not isinstance(resolved_brief, dict):
        resolved_brief = {}
    brief_trace = design.get("brief_trace") or item.get("brief_trace") or {}
    if not isinstance(brief_trace, dict):
        brief_trace = {}
    context_key = str(item.get("context_key") or asset.asset_code or asset.id)
    content = {
        "asset_id": str(asset.id),
        "asset_code": asset.asset_code,
        "context_key": context_key,
        "view_code": item.get("generated_output_spec") or item.get("output_spec") or "MASTER",
        "skill": design.get("skill"),
        "skill_version": design.get("skill_version"),
        "contract_version": design.get("contract_version"),
        "input_hash": design.get("input_hash"),
        "aspect_ratio": design.get("aspect_ratio"),
        "prompt": design.get("prompt"),
        "negative_prompt": design.get("negative_prompt"),
        "view_prompts": design.get("view_prompts") or [],
        "state_prompts": design.get("state_prompts") or [],
        "spatial_bible": design.get("spatial_bible") or {},
        "material_bible": design.get("material_bible") or {},
        "brief_trace": brief_trace,
    }
    digest = _content_hash(content)
    if source_run_id is not None:
        same_run = await session.scalar(
            select(ArtifactRevision)
            .where(
                ArtifactRevision.artifact_type == "asset_prompt",
                ArtifactRevision.artifact_id == asset.id,
                ArtifactRevision.source_run_id == source_run_id,
            )
            .limit(1)
        )
        if same_run is not None:
            return same_run
    if rerun_of_revision_id is None:
        existing = await session.scalar(
            select(ArtifactRevision)
            .where(
                ArtifactRevision.artifact_type == "asset_prompt",
                ArtifactRevision.artifact_id == asset.id,
                ArtifactRevision.content_hash == digest,
            )
            .limit(1)
        )
        if existing is not None:
            return existing
    current = await session.scalar(
        select(ArtifactRevision)
        .where(ArtifactRevision.artifact_type == "asset_prompt", ArtifactRevision.artifact_id == asset.id)
        .order_by(ArtifactRevision.version_no.desc())
        .limit(1)
    )
    version_no = (current.version_no if current else 0) + 1
    rerun_of = str(rerun_of_revision_id) if rerun_of_revision_id else None
    current_hash = current.content_hash if current else None
    revision = ArtifactRevision(
        project_id=asset.project_id,
        artifact_type="asset_prompt",
        artifact_id=asset.id,
        version_no=version_no,
        parent_revision_id=current.id if current else None,
        source_type="agent",
        source_run_id=source_run_id,
        skill_name=design.get("skill"),
        skill_version=design.get("skill_version"),
        contract_version=design.get("contract_version"),
        contract_hash=design.get("contract_hash"),
        prompt_version=design.get("prompt_version"),
        input_hash=design.get("input_hash"),
        input_snapshot={
            "context_key": context_key,
            "asset_prompt_input_fingerprint": _asset_row_prompt_input_fingerprint(asset),
            "production_context": production_context,
            "resolved_production_brief": resolved_brief,
            "brief_trace": brief_trace,
            "rerun_of_revision_id": rerun_of,
        },
        raw_output=dict(item),
        normalized_content=content,
        change_diff={
            "supersedes_revision_id": str(current.id) if current else None,
            "rerun_of_revision_id": rerun_of,
            "before_content_hash": current_hash,
            "after_content_hash": digest,
            "content_changed": current_hash != digest,
        },
        content_hash=digest,
        status="draft",
        created_by=created_by,
    )
    session.add(revision)
    await session.flush()
    return revision


async def materialize_asset_prompt_agent_run(
    session: AsyncSession,
    run: AgentRunRead,
) -> dict[str, Any]:
    """Apply a dedicated asset prompt run while retaining every prior revision."""
    if run.status != AgentRunStatus.succeeded or run.agent_type not in {
        AgentKind.character_design_prompt,
        AgentKind.scene_design_prompt,
        AgentKind.prop_design_prompt,
    }:
        return {"applied": False, "context_current": False, "reason": "not_applicable"}
    asset_id = _uuid_or_none((run.input or {}).get("asset_id"))
    if asset_id is None:
        return {"applied": False, "context_current": False, "reason": "asset_not_found"}
    asset = await session.get(Asset, asset_id, with_for_update=True)
    if asset is None or str(asset.status) in _INACTIVE_TASK_ASSET_STATUSES:
        return {"applied": False, "context_current": False, "reason": "asset_not_found"}
    output = run.output if isinstance(run.output, dict) else {}
    run_input = run.input if isinstance(run.input, dict) else {}
    raw_rerun_of_revision_id = run_input.get("rerun_of_revision_id")
    rerun_of_revision_id = _uuid_or_none(raw_rerun_of_revision_id)
    if raw_rerun_of_revision_id and rerun_of_revision_id is None:
        raise ValueError("Invalid rerun_of_revision_id in asset Prompt run input")
    run_asset = run_input.get("asset") if isinstance(run_input.get("asset"), dict) else {}
    run_asset_metadata = run_asset.get("metadata") if isinstance(run_asset.get("metadata"), dict) else {}
    current_fingerprint = _asset_row_prompt_input_fingerprint(asset)
    run_fingerprint = str(run_asset_metadata.get("prompt_input_fingerprint") or "").strip()
    if not run_fingerprint and run_asset:
        run_fingerprint = _content_hash(_asset_prompt_input_projection(run_asset))
    project = await session.get(Project, asset.project_id) if asset.project_id else None
    resolved_brief = run_input.get("resolved_production_brief")
    brief_current = (
        _resolved_prompt_brief_matches_project(resolved_brief, project)
        if isinstance(resolved_brief, dict) and resolved_brief
        else True
    )
    context_current = bool((not run_fingerprint or run_fingerprint == current_fingerprint) and brief_current)
    skill_by_agent = {
        AgentKind.character_design_prompt: "character-design-prompt",
        AgentKind.scene_design_prompt: "scene-design-prompt",
        AgentKind.prop_design_prompt: "prop-design-prompt",
    }
    item = {
        **output,
        "asset_id": str(asset.id),
        "asset_code": output.get("asset_code") or asset.asset_code,
        "context_key": output.get("context_key") or (run.input or {}).get("context_key") or asset.asset_code,
        "production_context": run_input.get("production_context") or {},
        "resolved_production_brief": resolved_brief or {},
        "brief_trace": output.get("brief_trace") or run_input.get("brief_trace") or {},
        "skill": output.get("skill") or run.skill_name or skill_by_agent.get(run.agent_type),
        "skill_version": output.get("skill_version") or run.skill_version,
        "contract_version": output.get("contract_version") or run.contract_version,
        "contract_hash": output.get("contract_hash") or run.contract_hash,
        "input_hash": output.get("input_hash") or run.input_hash,
    }
    revision = await _append_prompt_revision(
        session,
        asset,
        item,
        source_run_id=run.id,
        created_by=None,
        rerun_of_revision_id=rerun_of_revision_id,
    )
    if revision is None:
        return {"applied": False, "context_current": False, "reason": "empty_prompt"}
    revision.status = "pending_confirmation" if context_current else "stale"
    metadata = dict(asset.metadata_json or {})
    design: dict[str, Any] = {}
    for key in (
            "prompt", "negative_prompt", "aspect_ratio", "view_prompts", "state_prompts",
            "spatial_bible", "material_bible", "brief_trace", "skill", "skill_version",
            "contract_version", "contract_hash", "input_hash",
    ):
        value = output.get(key) if output.get(key) not in (None, "", [], {}) else item.get(key)
        if value not in (None, "", [], {}):
            design[key] = value
    design["production_context"] = item["production_context"]
    design["resolved_production_brief"] = item["resolved_production_brief"]
    if context_current:
        metadata["prompt_design"] = design
        metadata["prompt_lineage"] = item["brief_trace"]
        metadata["production_context"] = item["production_context"]
        metadata["resolved_production_brief"] = item["resolved_production_brief"]
        asset.metadata_json = metadata
        _set_asset_row_lifecycle(
            asset,
            "pending_confirmation",
            prompt_validity="current",
            prompt_input_fingerprint=current_fingerprint,
            prompt_generated_for_fingerprint=current_fingerprint,
        )
    else:
        _set_asset_row_lifecycle(
            asset,
            "prompt_pending",
            prompt_validity="stale",
            prompt_input_fingerprint=current_fingerprint,
            invalidated_fields=["asset_prompt_input" if not run_fingerprint or run_fingerprint != current_fingerprint else "production_brief"],
        )
    if context_current and output.get("prompt"):
        asset.prompt_text = str(output["prompt"])
        if run.task_id:
            task = await _get_active_asset_task(session, run.task_id, with_for_update=True)
            if task is not None:
                prompt_type = f"agent_run:{run.id}"
                existing_prompt = await session.scalar(select(TaskPrompt.id).where(
                    TaskPrompt.task_id == task.id,
                    TaskPrompt.prompt_type == prompt_type,
                    TaskPrompt.source == "agent",
                ))
                if existing_prompt is None:
                    version_no = (await session.scalar(
                        select(func.max(TaskPrompt.version_no)).where(TaskPrompt.task_id == task.id)
                    ) or 0) + 1
                    session.add(TaskPrompt(
                        task_id=task.id,
                        prompt_type=prompt_type,
                        prompt_text=str(output["prompt"]),
                        source="agent",
                        version_no=version_no,
                    ))
                task.latest_prompt_text = str(output["prompt"])
                task.updated_at = datetime.now(UTC)
    asset.base_model = asset.base_model or "seedream"
    await session.commit()
    revision_diff = revision.change_diff if isinstance(revision.change_diff, dict) else {}
    return {
        "applied": True,
        "revision_id": str(revision.id),
        "version_no": revision.version_no,
        "parent_revision_id": str(revision.parent_revision_id) if revision.parent_revision_id else None,
        "supersedes_revision_id": (
            revision_diff.get("supersedes_revision_id")
            or (str(revision.parent_revision_id) if revision.parent_revision_id else None)
        ),
        "rerun_of_revision_id": revision_diff.get("rerun_of_revision_id"),
        "context_current": context_current,
        "reason": "applied" if context_current else "context_changed",
    }


def _resolved_prompt_brief_matches_project(
    resolved_brief: dict[str, Any],
    project: Project | None,
) -> bool:
    if project is None:
        return True
    current = project.production_brief or {}
    comparable_fields = (
        "content_type",
        "delivery_aspect_ratio",
        "primary_style_id",
        "style_catalog_version",
        "cultural_contexts",
        "primary_cultural_context_code",
    )
    return all(
        resolved_brief.get(key) == current.get(key)
        for key in comparable_fields
        if key in current
    )


@dataclass(frozen=True)
class _AudioTaskSpec:
    asset_type: AssetType
    asset_code: str
    audio_type: str
    name: str
    description: str
    prompt: str
    # "project": one asset/task for the whole project (theme music, voice profiles).
    # "episode": one asset/task per episode (background music).
    scope: str = "episode"
    # Episode code stored on episode-scoped audio (background music) so downstream
    # naming/display can keep the episode segment and avoid cross-episode collisions.
    episode_code: str | None = None
    role_asset_id: UUID | None = None
    role_code: str | None = None
    role_priority: str | None = None
    dialogue_scene_count: int = 0


def _asset_priority(asset: Asset) -> str:
    metadata = asset.metadata_json or {}
    breakdown = metadata.get("breakdown_asset") if isinstance(metadata.get("breakdown_asset"), dict) else {}
    breakdown_metadata = breakdown.get("metadata") if isinstance(breakdown.get("metadata"), dict) else {}
    reading = metadata.get("reading_candidate") if isinstance(metadata.get("reading_candidate"), dict) else {}
    priority = str(
        metadata.get("priority")
        or breakdown.get("priority")
        or breakdown_metadata.get("priority")
        or reading.get("priority")
        or "C"
    ).strip().upper()
    return priority if priority in {"S", "A", "B", "C"} else "C"


def _asset_has_dialogue(asset: Asset) -> bool:
    metadata = asset.metadata_json or {}
    breakdown = metadata.get("breakdown_asset") if isinstance(metadata.get("breakdown_asset"), dict) else {}
    breakdown_metadata = breakdown.get("metadata") if isinstance(breakdown.get("metadata"), dict) else {}
    reading = metadata.get("reading_candidate") if isinstance(metadata.get("reading_candidate"), dict) else {}
    for source in (metadata, breakdown, breakdown_metadata, reading):
        value = source.get("has_dialogue")
        if value is True or str(value or "").strip().lower() in {"true", "yes", "1", "有台词"}:
            return True
        for key in ("dialogue_evidence", "dialogue_scene_codes", "speaking_scene_codes"):
            if isinstance(source.get(key), list) and source[key]:
                return True
        for key in ("dialogue_scene_count", "dialogue_count"):
            try:
                if int(source.get(key) or 0) > 0:
                    return True
            except (TypeError, ValueError):
                pass
    return False


def _audio_match_token(value: Any) -> str:
    return re.sub(r"[^a-z0-9\u4e00-\u9fff]+", "", str(value or "").casefold())


def _character_audio_aliases(asset: Asset) -> set[str]:
    metadata = asset.metadata_json or {}
    breakdown = metadata.get("breakdown_asset") if isinstance(metadata.get("breakdown_asset"), dict) else {}
    raw_aliases = list(metadata.get("aliases") or []) + list(breakdown.get("aliases") or [])
    values = [asset.asset_code, _library_leaf_code(asset.asset_code, ""), asset.name, *raw_aliases]
    for part in re.split(r"[\s·._-]+", asset.name or ""):
        if len(part.strip()) >= 3:
            values.append(part)
    return {token for value in values if (token := _audio_match_token(value))}


def _dialogue_scene_counts(
    character_assets: list[Asset],
    storyboards: list[Storyboard],
    dialogue_script: list[dict[str, Any]],
) -> dict[UUID, int]:
    aliases_by_asset = {asset.id: _character_audio_aliases(asset) for asset in character_assets}
    assets_by_alias: dict[str, set[UUID]] = {}
    for asset_id, aliases in aliases_by_asset.items():
        for alias in aliases:
            assets_by_alias.setdefault(alias, set()).add(asset_id)
    dialogue_scenes: dict[UUID, set[str]] = {asset.id: set() for asset in character_assets}

    def matched_assets(value: Any) -> set[UUID]:
        token = _audio_match_token(value)
        return set(assets_by_alias.get(token, set()))

    def scene_key(scene_code: Any, scene_name: Any) -> str:
        code = _library_leaf_code(str(scene_code or ""), "")
        return code or (f"NAME:{_audio_match_token(scene_name)}" if scene_name else "")

    for scene in dialogue_script:
        if not isinstance(scene, dict):
            continue
        key = scene_key(scene.get("scene_code"), scene.get("scene_name"))
        if not key:
            continue
        for beat in scene.get("beats") or []:
            if not isinstance(beat, dict) or not str(beat.get("text") or "").strip():
                continue
            for asset_id in matched_assets(beat.get("speaker")):
                dialogue_scenes[asset_id].add(key)

    for storyboard in storyboards:
        key = scene_key(storyboard.scene_code, storyboard.scene_name)
        if not key:
            continue
        referenced: set[UUID] = set()
        for reference in storyboard.characters or []:
            referenced.update(matched_assets(reference))
        dialogue_values = [storyboard.dialogue]
        dialogue_values.extend(
            item.get("dialogue") for item in (storyboard.mirror_shots or []) if isinstance(item, dict)
        )
        for dialogue in dialogue_values:
            text = str(dialogue or "").strip()
            if not text:
                continue
            if len(referenced) == 1:
                dialogue_scenes[next(iter(referenced))].add(key)
            for line in text.splitlines():
                speaker = re.split(r"[:：]", line, maxsplit=1)[0]
                for asset_id in matched_assets(speaker):
                    dialogue_scenes[asset_id].add(key)
    return {asset_id: len(scene_keys) for asset_id, scene_keys in dialogue_scenes.items()}


def _planned_audio_tasks(
    project_prefix: str,
    character_assets: list[Asset],
    storyboards: list[Storyboard],
    dialogue_script: list[dict[str, Any]],
    episode_code: str = "EP01",
) -> list[_AudioTaskSpec]:
    prefix = _library_code(project_prefix, "RF")
    episode = _library_code(episode_code, "EP")
    specs = [
        _AudioTaskSpec(
            asset_type=AssetType.music,
            asset_code=f"{prefix}-THEME-MUSIC",
            audio_type="theme_music",
            name="主题音乐",
            description="项目主题音乐母版（项目级，全项目共用一首）。",
            prompt="制作项目主题音乐母版，建立稳定的核心旋律、情绪识别和品牌记忆，供整个项目复用；交付 WAV 或 MP3。",
            scope="project",
        ),
        _AudioTaskSpec(
            asset_type=AssetType.music,
            asset_code=f"{prefix}-{episode}-BACK-MUSIC",
            audio_type="background_music",
            name="背景音乐",
            description=f"{episode} 分集背景音乐母版。",
            prompt=f"制作 {episode} 分集背景音乐母版，支持对白场景铺底和本集跨场景复用，控制动态范围并避免遮蔽人声；交付 WAV 或 MP3。",
            scope="episode",
            episode_code=episode,
        ),
    ]
    dialogue_scene_counts = _dialogue_scene_counts(character_assets, storyboards, dialogue_script)
    for asset in sorted(character_assets, key=lambda item: (item.asset_code or "", item.name)):
        priority = _asset_priority(asset)
        dialogue_scene_count = dialogue_scene_counts.get(asset.id, 0)
        if priority not in {"S", "A"} or not (dialogue_scene_count > 0 or _asset_has_dialogue(asset)):
            continue
        role_code = _role_asset_code(asset.asset_code)
        reason = f"{priority}级角色，{dialogue_scene_count} 个已归一化对白场景" if dialogue_scene_count else f"{priority}级角色，资产抽取已确认本集有台词"
        specs.append(_AudioTaskSpec(
            asset_type=AssetType.voice_profile,
            asset_code=f"{prefix}-AUDIO-VOICE-{role_code}-BASE",
            audio_type="voice",
            name=f"{asset.name} 角色音色",
            description=f"{asset.name} 的项目级基础音色母版；生成依据：{reason}。",
            prompt=(
                f"为角色 {asset.name}（{role_code}，{reason}）制作稳定的基础音色母版。"
                "保持年龄、性格、情绪张力和语言习惯一致，提供可用于多场景对白的清晰干声；交付 WAV 或 MP3。"
            ),
            scope="project",
            role_asset_id=asset.id,
            role_code=role_code,
            role_priority=priority,
            dialogue_scene_count=dialogue_scene_count,
        ))
    return specs


async def _create_audio_asset_tasks(
    session: AsyncSession,
    *,
    project: Project,
    episode: ProjectEpisode,
    script: Script,
    script_version_id: UUID,
    character_assets: list[Asset],
    storyboards: list[Storyboard],
    dialogue_script: list[dict[str, Any]],
    user: User,
    now: datetime,
) -> dict[str, Any]:
    specs = _planned_audio_tasks(
        project.project_prefix or project.project_no or "RF",
        character_assets,
        storyboards,
        dialogue_script,
        episode_code=episode.episode_code,
    )
    asset_codes = [spec.asset_code for spec in specs]
    existing_assets = {
        asset.asset_code: asset
        for asset in (await session.scalars(select(Asset).where(
            Asset.project_id == project.id,
            Asset.asset_code.in_(asset_codes),
        ))).all()
    }
    # Project-wide audio task lookup. Uniqueness is keyed on asset_id: theme music
    # and voice profiles share a single project-level asset_code (so an existing task
    # anywhere in the project correctly suppresses a duplicate), while background music
    # uses a per-episode asset_code (so a newly finalized episode still gets its own).
    existing_audio_tasks = list((await session.scalars(select(Task).where(
        Task.project_id == project.id,
        Task.task_type == TaskType.audio,
        Task.is_retired.is_(False),
    ))).all())
    task_asset_ids = {task.asset_id for task in existing_audio_tasks if task.asset_id}
    created_assets = 0
    created_tasks = 0
    voice_tasks = 0
    theme_music_tasks = 0
    background_music_tasks = 0
    for spec in specs:
        asset = existing_assets.get(spec.asset_code)
        metadata = {
            "audio_type": spec.audio_type,
            "audio_scope": spec.scope,
            "episode_code": spec.episode_code,
            "role_asset_id": str(spec.role_asset_id) if spec.role_asset_id else None,
            "role_code": spec.role_code,
            "role_priority": spec.role_priority,
            "dialogue_scene_count": spec.dialogue_scene_count,
            "source": "asset_finalization",
        }
        if asset is None:
            asset = Asset(
                project_id=project.id,
                asset_code=spec.asset_code,
                asset_type=spec.asset_type,
                name=spec.name,
                description=spec.description,
                status="in_progress",
                tags=["audio", spec.audio_type],
                metadata_json=metadata,
                version=1,
                created_by_id=user.id,
            )
            session.add(asset)
            await session.flush()
            existing_assets[spec.asset_code] = asset
            created_assets += 1
        else:
            asset.metadata_json = {**(asset.metadata_json or {}), **metadata}
            asset.description = spec.description
        if asset.id in task_asset_ids:
            continue
        # Project-scoped audio (theme music, voice) must resolve to one task for the
        # whole project, so its idempotency key omits episode/version. Episode-scoped
        # audio (background music) keeps them to allow one task per episode.
        is_project_scope = spec.scope == "project"
        session.add(Task(
            project_id=project.id,
            episode_id=script.episode_id,
            script_id=script.id,
            script_version_id=script_version_id,
            idempotency_key=_task_idempotency_key(
                project_id=project.id,
                episode_id=None if is_project_scope else episode.id,
                script_version_id=None if is_project_scope else script_version_id,
                task_type=TaskType.audio,
                asset_id=asset.id,
                media_type=spec.audio_type,
                task_variant="MASTER",
            ),
            asset_id=asset.id,
            task_type=TaskType.audio,
            media_type=spec.audio_type,
            task_variant="MASTER",
            title=f"音频资产 {spec.name}",
            status=TaskStatus.todo,
            prompt_text=spec.prompt,
            latest_prompt_text=spec.prompt,
            production_model="音频制作",
            due_at=now + timedelta(days=3),
        ))
        task_asset_ids.add(asset.id)
        created_tasks += 1
        if spec.audio_type == "voice":
            voice_tasks += 1
        elif spec.audio_type == "theme_music":
            theme_music_tasks += 1
        elif spec.audio_type == "background_music":
            background_music_tasks += 1
    await session.flush()
    return {
        "created_assets": created_assets,
        "created_tasks": created_tasks,
        "voice_tasks": voice_tasks,
        "theme_music_tasks": theme_music_tasks,
        "background_music_tasks": background_music_tasks,
        "planned_tasks": len(specs),
    }


async def storyboard_tasks_distributed(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    script_version_id: UUID,
) -> bool:
    """Return whether this script version already has production storyboard tasks."""
    task_id = await session.scalar(select(Task.id).where(
        Task.project_id == project_id,
        Task.episode_id == episode_id,
        Task.script_version_id == script_version_id,
        Task.is_retired.is_(False),
        or_(
            Task.task_type == TaskType.storyboard_shot,
            and_(
                Task.storyboard_id.is_not(None),
                Task.task_type.in_([TaskType.text_to_image, TaskType.image_to_video]),
            ),
        ),
    ).limit(1))
    return task_id is not None


def _breakdown_dialogue_script(content: dict[str, Any]) -> list[dict[str, Any]]:
    raw_output = content.get("raw_output") if isinstance(content.get("raw_output"), dict) else {}
    values = raw_output.get("dialogue_script") or content.get("dialogue_script") or []
    return [dict(item) for item in values if isinstance(item, dict)]


def _task_idempotency_key(
    *,
    project_id: UUID,
    episode_id: UUID | None,
    script_version_id: UUID | None,
    task_type: TaskType,
    storyboard_id: UUID | None = None,
    asset_id: UUID | None = None,
    task_variant: str | None = None,
    age_stage_code: str | None = None,
    costume_variant_code: str | None = None,
    media_type: str | None = None,
) -> str:
    identity = {
        "project_id": str(project_id),
        "episode_id": str(episode_id) if episode_id else None,
        "script_version_id": str(script_version_id) if script_version_id else None,
        "task_type": task_type.value if hasattr(task_type, "value") else str(task_type),
        "storyboard_id": str(storyboard_id) if storyboard_id else None,
        "asset_id": str(asset_id) if asset_id else None,
        "task_variant": task_variant or None,
        "age_stage_code": age_stage_code or None,
        "costume_variant_code": costume_variant_code or None,
        "media_type": media_type or None,
    }
    encoded = json.dumps(identity, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(encoded.encode("utf-8")).hexdigest()


async def generate_asset_tasks(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    script_version_id: UUID,
    user: User,
) -> dict[str, Any]:
    """Create tasks only for assets explicitly bound to this script version."""
    await ensure_project_write_access(session, project_id, user)
    project = await session.get(Project, project_id, with_for_update=True)
    if project is None:
        raise KeyError(project_id)
    episode = await session.get(ProjectEpisode, episode_id)
    script_version = await session.get(ScriptVersion, script_version_id)
    script = await session.get(Script, script_version.script_id) if script_version else None
    if episode is None or episode.project_id != project_id or script is None or script.project_id != project_id or script.episode_id != episode_id:
        raise ValueError("分集与剧本版本不匹配。")
    confirmed_inventory = await _require_asset_prompt_designs_for_scope(
        session,
        project_id=project_id,
        episode_id=episode_id,
        script_version_id=script_version_id,
        user=user,
    )
    confirmed_asset_ids = {
        asset_id
        for item in confirmed_inventory
        if isinstance(item, dict)
        for asset_id in [_uuid_or_none(item.get("asset_id") or item.get("id"))]
        if asset_id is not None
    }
    bindings = list((await session.scalars(select(EpisodeAssetBinding).where(
        EpisodeAssetBinding.project_id == project_id,
        EpisodeAssetBinding.episode_id == episode_id,
        EpisodeAssetBinding.script_version_id == script_version_id,
        EpisodeAssetBinding.status == "active",
    ).order_by(EpisodeAssetBinding.proposal_index))).all())
    bindings = [item for item in bindings if item.asset_id in confirmed_asset_ids]
    bindings_by_asset: dict[UUID, list[EpisodeAssetBinding]] = {}
    for binding in bindings:
        bindings_by_asset.setdefault(binding.asset_id, []).append(binding)
    if bindings:
        assets_by_id = {
            asset.id: asset for asset in (await session.scalars(select(Asset).where(
                Asset.project_id == project_id,
                Asset.id.in_({item.asset_id for item in bindings}),
                Asset.status.notin_(["deleted", "excluded"]),
            ))).all()
        }
        assets = [assets_by_id[asset_id] for asset_id in bindings_by_asset if asset_id in assets_by_id]
    else:
        assets = await _task_generation_assets_without_bindings(
            session,
            project,
            episode,
            script_version_id,
            user,
        )
    if not assets:
        raise ValueError("当前分集没有已确认资产或可用于兼容回填的资产定稿，不能生成资产任务。")
    now = datetime.now(UTC)
    created = 0
    created_by_type = {"character": 0, "scene": 0, "prop": 0}
    reused_tasks = 0
    for asset in assets:
        existing_tasks = (
            await session.execute(
                select(Task).where(
                    Task.project_id == project_id,
                    Task.asset_id == asset.id,
                    Task.script_version_id == script_version_id,
                    Task.storyboard_id.is_(None),
                    Task.task_type.in_([TaskType.asset, TaskType.text_to_image]),
                    Task.is_retired.is_(False),
                )
            )
        ).scalars().all()
        for task in existing_tasks:
            if task.task_type == TaskType.text_to_image:
                task.task_type = TaskType.asset
        if any(task.task_variant is None and not task.age_stage_code for task in existing_tasks):
            reused_tasks += len(existing_tasks)
            continue
        existing_by_context = {
            (task.age_stage_code or "", task.costume_variant_code or "", str(task.task_variant or "")): task
            for task in existing_tasks
        }
        specs = _asset_task_context_specs(asset)
        scoped_bindings = bindings_by_asset.get(asset.id, [])
        scoped = [item for item in scoped_bindings if item.age_stage_code or item.costume_variant_code]
        if scoped:
            specs = [
                spec for spec in specs
                if any(
                    (not binding.age_stage_code or spec[0] == binding.age_stage_code)
                    and (not binding.costume_variant_code or spec[1] == binding.costume_variant_code)
                    for binding in scoped
                )
            ]
        binding_revision_id = next(
            (item.asset_revision_id for item in scoped_bindings if item.asset_revision_id), asset.current_revision_id
        )
        prompt_revisions = list((await session.scalars(
            select(ArtifactRevision)
            .where(ArtifactRevision.artifact_type == "asset_prompt", ArtifactRevision.artifact_id == asset.id)
            .order_by(ArtifactRevision.version_no.desc())
        )).all())
        prompt_revision_by_context = {
            str((revision.input_snapshot or {}).get("context_key") or ""): revision.id
            for revision in prompt_revisions
            if str((revision.input_snapshot or {}).get("context_key") or "").strip()
        }
        for age_stage, costume_variant, variant, variant_label, instruction in specs:
            context_key = (age_stage or "", costume_variant or "", variant)
            if context_key in existing_by_context:
                reused_tasks += 1
                continue
            primary_task = existing_by_context.get((age_stage or "", costume_variant or "", "A"))
            type_label = {"character": "人物", "scene": "场景", "prop": "道具"}.get(asset.asset_type.value, "资产")
            prompt = _asset_context_prompt(asset, age_stage, costume_variant, variant, instruction)
            context_label = " ".join(part for part in [age_stage, costume_variant] if part)
            prompt_context_key = "/".join(filter(None, (asset.asset_code, age_stage, costume_variant))) or str(asset.asset_code or asset.id)
            task = Task(
                project_id=project_id,
                episode_id=episode_id,
                script_id=script.id,
                script_version_id=script_version_id,
                asset_id=asset.id,
                task_type=TaskType.asset,
                task_variant=variant,
                age_stage_code=age_stage,
                costume_variant_code=costume_variant,
                idempotency_key=_task_idempotency_key(
                    project_id=project_id,
                    episode_id=episode_id,
                    script_version_id=script_version_id,
                    task_type=TaskType.asset,
                    asset_id=asset.id,
                    task_variant=variant,
                    age_stage_code=age_stage,
                    costume_variant_code=costume_variant,
                ),
                prompt_revision_id=prompt_revision_by_context.get(prompt_context_key) or binding_revision_id,
                depends_on_task_id=primary_task.id if primary_task and variant in {"B", "C", "D", "E"} else None,
                title=f"{type_label}资产 {asset.asset_code or ''} {asset.name} {context_label} [{variant}] {variant_label}".strip(),
                status=TaskStatus.todo,
                prompt_text=prompt,
                latest_prompt_text=prompt,
                production_model=asset.base_model or "Seedream",
                due_at=now + timedelta(days=2 if variant in {"A", "MASTER"} else 3),
            )
            session.add(task)
            await session.flush()
            existing_by_context[context_key] = task
            created += 1
            if asset.asset_type.value in created_by_type:
                created_by_type[asset.asset_type.value] += 1

    character_assets = [asset for asset in (await session.scalars(select(Asset).where(
        Asset.project_id == project_id,
        Asset.asset_type == AssetType.character,
        Asset.status.notin_(["deleted", "excluded"]),
    ).order_by(Asset.asset_code, Asset.name))).all() if (asset.metadata_json or {}).get("source") != "human_temporary"]
    storyboards = list((await session.scalars(select(Storyboard).where(
        Storyboard.project_id == project_id,
    ).order_by(Storyboard.episode_num, Storyboard.order_num))).all())
    latest_breakdown = await session.scalar(
        select(ScriptBreakdown)
        .where(ScriptBreakdown.project_id == project_id)
        .order_by(ScriptBreakdown.version.desc())
        .limit(1)
    )
    audio_result = await _create_audio_asset_tasks(
        session,
        project=project,
        episode=episode,
        script=script,
        script_version_id=script_version_id,
        character_assets=character_assets,
        storyboards=storyboards,
        dialogue_script=_breakdown_dialogue_script(dict(latest_breakdown.content_json or {}) if latest_breakdown else {}),
        user=user,
        now=now,
    )
    created += audio_result["created_tasks"]
    dependency_links = await _link_character_costume_dependencies(session, project_id, episode_id)
    variant_dependencies = {"plans": 0, "variant_tasks": 0, "dependencies": 0}
    refreshed_prompts = await refresh_pending_asset_task_prompts(session, project_id, created_by=user.id)
    current_stage = project.current_stage or project.status or ProjectStatus.breakdown_review
    await add_operation_log(
        session,
        action="asset_tasks_generated",
        target_type="project",
        operator_id=user.id,
        project_id=project_id,
        detail={
            "created_tasks": created,
            "assets": len(assets),
            "episode_id": str(episode_id),
            "script_version_id": str(script_version_id),
            "dependency_links": dependency_links,
            "variant_dependencies": variant_dependencies,
            "refreshed_prompts": refreshed_prompts,
            "audio": audio_result,
        },
    )
    await session.commit()
    return {
        "created_tasks": created,
        "character_tasks": created_by_type["character"],
        "scene_tasks": created_by_type["scene"],
        "prop_tasks": created_by_type["prop"],
        "theme_music_tasks": audio_result["theme_music_tasks"],
        "background_music_tasks": audio_result["background_music_tasks"],
        "character_voice_tasks": audio_result["voice_tasks"],
        "reused_tasks": reused_tasks,
        "failed_items": [],
        "assets": len(assets),
        "episode_id": str(episode_id),
        "script_version_id": str(script_version_id),
        "dependency_links": dependency_links,
        "variant_dependencies": variant_dependencies,
        "refreshed_prompts": refreshed_prompts,
        "audio": audio_result,
        "stage": current_stage.value,
    }


async def _require_asset_prompt_designs_for_scope(
    session: AsyncSession,
    *,
    project_id: UUID,
    episode_id: UUID,
    script_version_id: UUID,
    user: User,
) -> list[dict[str, Any]]:
    assets = await confirmed_asset_inventory(
        session,
        project_id,
        episode_id=episode_id,
        script_version_id=script_version_id,
        strict_scope=True,
    )
    if not assets:
        raise ValueError("当前分集没有已确认正式资产，不能进入任务阶段。")
    view = await latest_breakdown_view(
        session,
        project_id,
        user,
        episode_id=episode_id,
        script_version_id=script_version_id,
    ) or {}
    missing = assets_missing_prompt_design(assets, view)
    if missing:
        raise ValueError(f"以下资产尚未经过专用 Prompt Skill：{'、'.join(missing)}。请先补齐资产 Prompt。")
    unconfirmed = assets_not_confirmed(assets)
    if unconfirmed:
        raise ValueError(f"以下资产尚未完成人工确认：{'、'.join(unconfirmed)}。请逐项确认后再进入任务阶段。")
    return assets


async def refresh_pending_asset_task_prompts(
    session: AsyncSession,
    project_id: UUID,
    *,
    created_by: UUID | None = None,
) -> int:
    rows = list((await session.execute(
        select(Task, Asset)
        .join(Asset, Asset.id == Task.asset_id)
        .where(
            Task.project_id == project_id,
            Task.task_type == TaskType.asset,
            Task.status == TaskStatus.todo,
            Task.is_retired.is_(False),
            or_(Task.variant_kind.is_(None), Task.variant_kind != "human_temporary"),
        )
    )).all())
    task_ids = [task.id for task, _asset in rows]
    human_prompt_task_ids = set((await session.scalars(
        select(TaskPrompt.task_id)
        .where(TaskPrompt.task_id.in_(task_ids), TaskPrompt.source == "human")
        .distinct()
    )).all()) if task_ids else set()
    refreshed = 0
    for task, asset in rows:
        if task.variant_plan_id:
            # Variant prompts are authored from the plan's Chinese view/state
            # description and must not be normalized as a MASTER asset prompt.
            continue
        current_prompt = task.latest_prompt_text or task.prompt_text or ""
        if task.id in human_prompt_task_ids and not _has_foreign_asset_view_sections(
            current_prompt,
            str(task.task_variant or "MASTER"),
        ):
            continue
        prompt = _asset_task_prompt_for_existing(asset, task)
        if not prompt or prompt == current_prompt:
            continue
        version_no = (await session.scalar(
            select(func.max(TaskPrompt.version_no)).where(TaskPrompt.task_id == task.id)
        ) or 0) + 1
        session.add(TaskPrompt(
            task_id=task.id,
            prompt_type=f"asset_view_{str(task.task_variant or 'master').lower()}",
            prompt_text=prompt,
            source="asset_prompt_normalization",
            version_no=version_no,
            created_by=created_by,
        ))
        task.latest_prompt_text = prompt
        task.asset_context_outdated = False
        task.updated_at = datetime.now(UTC)
        refreshed += 1
    await session.flush()
    return refreshed


def _asset_task_prompt_for_existing(asset: Asset, task: Task) -> str:
    variant = str(task.task_variant or "MASTER")
    specs = _asset_task_context_specs(asset)
    instruction = next((
        spec[4] for spec in specs
        if (spec[0] or "") == (task.age_stage_code or "")
        and (spec[1] or "") == (task.costume_variant_code or "")
        and spec[2] == variant
    ), "使用最新已确认资产规范生成当前图位。")
    return _asset_context_prompt(
        asset,
        task.age_stage_code,
        task.costume_variant_code,
        variant,
        instruction,
    )


async def _link_character_costume_dependencies(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
) -> int:
    assets = list((await session.scalars(select(Asset).where(Asset.project_id == project_id))).all())
    assets_by_code = {str(asset.asset_code or "").upper(): asset for asset in assets if asset.asset_code}
    assets_by_name = {asset.name.strip(): asset for asset in assets if asset.name.strip()}
    tasks = list((await session.scalars(select(Task).where(
        Task.project_id == project_id,
        Task.task_type == TaskType.asset,
        Task.is_retired.is_(False),
    ))).all())
    tasks_by_asset: dict[UUID, list[Task]] = {}
    for task in tasks:
        if task.asset_id:
            tasks_by_asset.setdefault(task.asset_id, []).append(task)
    created = 0
    now = datetime.now(UTC)
    for asset in assets:
        if asset.asset_type != AssetType.character:
            continue
        character_tasks = tasks_by_asset.get(asset.id, [])
        base_task = next((task for task in character_tasks if not task.age_stage_code and not task.costume_variant_code and task.task_variant == "A"), None)
        if base_task and base_task.assignee_id:
            for task in character_tasks:
                task.assignee_id = base_task.assignee_id
                task.assigned_by = base_task.assigned_by
                task.assigned_at = base_task.assigned_at or now
        metadata = asset.metadata_json or {}
        source = metadata.get("breakdown_asset") if isinstance(metadata.get("breakdown_asset"), dict) else {}
        stages = source.get("age_stages") or metadata.get("age_stages") or []
        for stage in stages if isinstance(stages, list) else []:
            if not isinstance(stage, dict):
                continue
            stage_code = str(stage.get("stage_code") or "").strip()
            variants = stage.get("costume_variants") if isinstance(stage.get("costume_variants"), list) else []
            for costume in variants:
                if not isinstance(costume, dict):
                    continue
                costume_code = str(costume.get("variant_code") or "").strip()
                context_tasks = [
                    task for task in character_tasks
                    if (task.age_stage_code or "") == stage_code
                    and (task.costume_variant_code or "") == costume_code
                    and task.task_variant in {"A", "B", "C", "D", "E"}
                ]
                if not context_tasks:
                    continue
                prop_assets: list[Asset] = []
                for raw in list(costume.get("costume_prop_codes") or []) + list(costume.get("costume_prop_ids") or []):
                    key = str(raw or "").strip().upper()
                    candidate = assets_by_code.get(key)
                    if candidate and candidate.asset_type == AssetType.prop and candidate not in prop_assets:
                        prop_assets.append(candidate)
                for raw_name in costume.get("costume_prop_names") or []:
                    candidate = assets_by_name.get(str(raw_name or "").strip())
                    if candidate and candidate.asset_type == AssetType.prop and candidate not in prop_assets:
                        prop_assets.append(candidate)
                for prop_asset in prop_assets:
                    prop_task = next((
                        task for task in tasks_by_asset.get(prop_asset.id, [])
                        if task.task_variant in {"MASTER", "A", None}
                    ), None)
                    if prop_task is None:
                        continue
                    for context_task in context_tasks:
                        exists = await session.scalar(select(TaskDependency.id).where(
                            TaskDependency.task_id == context_task.id,
                            TaskDependency.depends_on_task_id == prop_task.id,
                        ))
                        if exists:
                            continue
                        session.add(TaskDependency(
                            task_id=context_task.id,
                            depends_on_task_id=prop_task.id,
                            dependency_type="costume_prop",
                        ))
                        created += 1
    await session.flush()
    return created


def _variant_code_number(value: str | None) -> int:
    match = re.search(r"(\d+)$", str(value or ""))
    return int(match.group(1)) if match else 0


def _asset_business_code(value: str | None) -> str:
    """Drop the project prefix for director-facing variant task identifiers."""
    text = str(value or "").strip().upper()
    parts = text.split("-", 1)
    if len(parts) == 2 and re.fullmatch(r"[A-Z0-9]{1,5}", parts[0]) and re.fullmatch(r"(?:R|SC|P)\d+", parts[1]):
        return parts[1]
    return text


async def _next_asset_variant_code(
    session: AsyncSession, asset_id: UUID, episode_id: UUID | None
) -> str:
    rows = list((await session.scalars(select(AssetVariantPlan).where(
        AssetVariantPlan.asset_id == asset_id,
        AssetVariantPlan.episode_id == episode_id,
    ))).all())
    return f"V{max((_variant_code_number(item.variant_code) for item in rows), default=0) + 1:03d}"


async def _variant_plan_read(
    session: AsyncSession, plan: AssetVariantPlan, *, asset: Asset | None = None
) -> AssetVariantPlanRead:
    asset = asset or await session.get(Asset, plan.asset_id)
    links = list((await session.scalars(select(AssetVariantStoryboardLink).where(
        AssetVariantStoryboardLink.variant_plan_id == plan.id,
        AssetVariantStoryboardLink.status == "active",
    ))).all())
    storyboard_ids = [item.storyboard_id for item in links]
    storyboard_codes: list[str] = []
    if storyboard_ids:
        storyboard_codes = list((await session.scalars(select(Storyboard.storyboard_code).where(
            Storyboard.id.in_(storyboard_ids)
        ).order_by(Storyboard.episode_num, Storyboard.order_num))).all())
    task_code = None
    if asset:
        task_code = f"{_asset_business_code(asset.asset_code)}-V{_variant_code_number(plan.variant_code):03d}" if asset.asset_code else plan.variant_code
    return AssetVariantPlanRead(
        id=plan.id,
        project_id=plan.project_id,
        episode_id=plan.episode_id,
        script_version_id=plan.script_version_id,
        asset_id=plan.asset_id,
        asset_code=asset.asset_code if asset else None,
        asset_name=asset.name if asset else None,
        variant_code=plan.variant_code,
        task_code=task_code,
        variant_kind=plan.variant_kind,
        title_zh=plan.title_zh,
        description_zh=plan.description_zh,
        source=plan.source,
        confidence=plan.confidence,
        status=plan.status,
        task_id=plan.task_id,
        storyboard_ids=storyboard_ids,
        storyboard_codes=[str(item) for item in storyboard_codes if item],
        created_at=plan.created_at,
        updated_at=plan.updated_at,
    )


async def list_asset_variant_plans(
    session: AsyncSession,
    project_id: UUID,
    user: User,
    *,
    episode_id: UUID | None = None,
) -> list[AssetVariantPlanRead]:
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    stmt = select(AssetVariantPlan, Asset).join(Asset, Asset.id == AssetVariantPlan.asset_id).where(
        AssetVariantPlan.project_id == project_id,
        Asset.status.notin_(_INACTIVE_TASK_ASSET_STATUSES),
    ).order_by(Asset.asset_code, AssetVariantPlan.variant_code)
    if episode_id:
        stmt = stmt.where(AssetVariantPlan.episode_id == episode_id)
    rows = list((await session.execute(stmt)).all())
    return [await _variant_plan_read(session, plan, asset=asset) for plan, asset in rows]


async def _validate_variant_storyboards(
    session: AsyncSession,
    project_id: UUID,
    storyboard_ids: list[UUID],
    episode_id: UUID | None,
) -> list[Storyboard]:
    if not storyboard_ids:
        return []
    storyboards = list((await session.scalars(select(Storyboard).where(
        Storyboard.project_id == project_id,
        Storyboard.id.in_(set(storyboard_ids)),
    ))).all())
    if len(storyboards) != len(set(storyboard_ids)):
        raise ValueError("存在不属于当前项目的分镜关联。")
    if episode_id:
        episode = await session.get(ProjectEpisode, episode_id)
        if episode is None or any(item.episode_num != episode.episode_no for item in storyboards):
            raise ValueError("分镜与变体计划不属于同一分集。")
    return storyboards


async def _materialize_asset_variant_task(
    session: AsyncSession,
    plan: AssetVariantPlan,
    asset: Asset,
    *,
    user_id: UUID | None = None,
) -> Task | None:
    if plan.status == "retired" or str(asset.status) in _INACTIVE_TASK_ASSET_STATUSES:
        return None
    if plan.task_id:
        task = await session.get(Task, plan.task_id)
        if task:
            return task
    base_task = await session.scalar(select(Task).where(
        Task.project_id == plan.project_id,
        Task.episode_id == plan.episode_id,
        Task.asset_id == asset.id,
        Task.task_type.in_([TaskType.asset, TaskType.text_to_image]),
        Task.task_variant == "MASTER",
        Task.age_stage_code.is_(None),
        Task.costume_variant_code.is_(None),
        Task.is_retired.is_(False),
    ).order_by(Task.created_at))
    if base_task is None:
        return None
    code = f"{_asset_business_code(asset.asset_code)}-{plan.variant_code}" if asset.asset_code else plan.variant_code
    prompt = "\n".join(part for part in [
        f"资产变体：{code}",
        f"资产：{asset.name}",
        f"类型：{'场景视角' if plan.variant_kind == 'scene_view' else '道具状态'}",
        f"中文要求：{plan.description_zh}",
        "请保持资产母版的身份、材质和制作策略一致，只改变本变体要求的视角或状态。",
    ] if part)
    task = Task(
        project_id=plan.project_id,
        episode_id=plan.episode_id,
        script_id=base_task.script_id,
        asset_id=asset.id,
        task_type=TaskType.asset,
        task_variant=plan.variant_code,
        variant_plan_id=plan.id,
        variant_kind=plan.variant_kind,
        variant_title_zh=plan.title_zh,
        variant_description_zh=plan.description_zh,
        title=f"{code} {plan.title_zh}",
        status=TaskStatus.todo,
        prompt_text=prompt,
        latest_prompt_text=prompt,
        production_model=asset.base_model or "Seedream",
        due_at=datetime.now(UTC) + timedelta(days=3),
    )
    session.add(task)
    await session.flush()
    plan.task_id = task.id
    plan.updated_at = datetime.now(UTC)
    existing_dependency = await session.scalar(select(TaskDependency.id).where(
        TaskDependency.task_id == task.id,
        TaskDependency.depends_on_task_id == base_task.id,
    ))
    if existing_dependency is None:
        session.add(TaskDependency(
            task_id=task.id,
            depends_on_task_id=base_task.id,
            dependency_type="asset_variant_master",
        ))
    return task


async def reconcile_asset_variant_dependencies(
    session: AsyncSession,
    project_id: UUID,
    *,
    episode_id: UUID | None = None,
) -> dict[str, int]:
    # Scene and prop variants are now uploaded as candidates on the base asset task.
    # Keep this compatibility entry point inert so old callers cannot create tasks.
    return {"plans": 0, "variant_tasks": 0, "dependencies": 0}


async def _legacy_reconcile_asset_variant_dependencies(
    session: AsyncSession,
    project_id: UUID,
    *,
    episode_id: UUID | None = None,
) -> dict[str, int]:
    stmt = select(AssetVariantPlan, Asset).join(Asset, Asset.id == AssetVariantPlan.asset_id).where(
        AssetVariantPlan.project_id == project_id,
        AssetVariantPlan.status != "retired",
        Asset.status.notin_(_INACTIVE_TASK_ASSET_STATUSES),
    )
    if episode_id:
        stmt = stmt.where(AssetVariantPlan.episode_id == episode_id)
    plans = list((await session.execute(stmt)).all())
    plan_ids = {plan.id for plan, _asset in plans}
    all_plan_stmt = select(AssetVariantPlan.id).where(AssetVariantPlan.project_id == project_id)
    if episode_id:
        all_plan_stmt = all_plan_stmt.where(AssetVariantPlan.episode_id == episode_id)
    all_plan_ids = set((await session.scalars(all_plan_stmt)).all())
    variant_tasks: dict[UUID, Task] = {}
    for plan, asset in plans:
        task = await _materialize_asset_variant_task(session, plan, asset)
        if task:
            variant_tasks[plan.id] = task
    relevant_task_ids = {task.id for task in variant_tasks.values()}
    relevant_task_ids.update((await session.scalars(select(Task.id).where(
        Task.project_id == project_id,
        Task.variant_plan_id.in_(all_plan_ids or {uuid.uuid4()}),
    ))).all() if all_plan_ids else set())
    storyboard_ids = set((await session.scalars(select(AssetVariantStoryboardLink.storyboard_id).where(
        AssetVariantStoryboardLink.variant_plan_id.in_(all_plan_ids or {uuid.uuid4()}),
    ))).all()) if all_plan_ids else set()
    storyboard_task_ids = set((await session.scalars(select(Task.id).where(
        Task.project_id == project_id,
        Task.storyboard_id.in_(storyboard_ids or {uuid.uuid4()}),
        Task.task_type.in_([TaskType.storyboard_shot, TaskType.text_to_image]),
    ))).all()) if storyboard_ids else set()
    relevant_task_ids.update(storyboard_task_ids)
    if relevant_task_ids:
        await session.execute(delete(TaskDependency).where(
            TaskDependency.task_id.in_(relevant_task_ids),
            TaskDependency.dependency_type.in_(["asset_variant", "asset_variant_master"]),
        ))
    added = 0
    for plan, _asset in plans:
        variant_task = variant_tasks.get(plan.id)
        if variant_task is None:
            continue
        base_task_id = await session.scalar(select(Task.id).where(
            Task.project_id == project_id,
            Task.episode_id == plan.episode_id,
            Task.asset_id == plan.asset_id,
            Task.task_type.in_([TaskType.asset, TaskType.text_to_image]),
            Task.task_variant == "MASTER",
            Task.age_stage_code.is_(None),
            Task.costume_variant_code.is_(None),
            Task.is_retired.is_(False),
        ).order_by(Task.created_at))
        if base_task_id:
            exists = await session.scalar(select(TaskDependency.id).where(
                TaskDependency.task_id == variant_task.id,
                TaskDependency.depends_on_task_id == base_task_id,
            ))
            if exists is None:
                session.add(TaskDependency(
                    task_id=variant_task.id,
                    depends_on_task_id=base_task_id,
                    dependency_type="asset_variant_master",
                ))
                added += 1
        links = list((await session.scalars(select(AssetVariantStoryboardLink).where(
            AssetVariantStoryboardLink.variant_plan_id == plan.id,
            AssetVariantStoryboardLink.status == "active",
        ))).all())
        linked_ids = {item.storyboard_id for item in links}
        if not linked_ids:
            continue
        shot_tasks = list((await session.scalars(select(Task).where(
            Task.project_id == project_id,
            Task.storyboard_id.in_(linked_ids),
            Task.task_type.in_([TaskType.storyboard_shot, TaskType.text_to_image]),
            Task.is_retired.is_(False),
        ))).all())
        for shot_task in shot_tasks:
            exists = await session.scalar(select(TaskDependency.id).where(
                TaskDependency.task_id == shot_task.id,
                TaskDependency.depends_on_task_id == variant_task.id,
            ))
            if exists is None:
                session.add(TaskDependency(
                    task_id=shot_task.id,
                    depends_on_task_id=variant_task.id,
                    dependency_type="asset_variant",
                ))
                added += 1
    await session.flush()
    return {"plans": len(plans), "variant_tasks": len(variant_tasks), "dependencies": added}


async def create_asset_variant_plan(
    session: AsyncSession,
    project_id: UUID,
    data: AssetVariantPlanCreate,
    user: User,
) -> AssetVariantPlanRead:
    raise ValueError("场景和道具不再按分镜创建变体；请在基础资产任务中上传多角度、多状态候选图片。")


async def _legacy_create_asset_variant_plan(
    session: AsyncSession,
    project_id: UUID,
    data: AssetVariantPlanCreate,
    user: User,
) -> AssetVariantPlanRead:
    await ensure_project_write_access(session, project_id, user)
    asset = await session.get(Asset, data.asset_id)
    if (
        asset is None
        or asset.project_id != project_id
        or asset.asset_type not in {AssetType.scene, AssetType.prop}
        or str(asset.status) in _INACTIVE_TASK_ASSET_STATUSES
    ):
        raise ValueError("变体计划只能绑定当前项目的场景或道具资产。")
    await _validate_variant_storyboards(session, project_id, data.storyboard_ids, data.episode_id)
    plan = AssetVariantPlan(
        project_id=project_id,
        episode_id=data.episode_id,
        script_version_id=data.script_version_id,
        asset_id=asset.id,
        variant_code=await _next_asset_variant_code(session, asset.id, data.episode_id),
        variant_kind=data.variant_kind,
        title_zh=data.title_zh.strip(),
        description_zh=data.description_zh.strip(),
        source=data.source,
        confidence=data.confidence,
        status="confirmed" if data.source == "director" else "suggested",
        created_by=user.id,
    )
    session.add(plan)
    await session.flush()
    for storyboard_id in dict.fromkeys(data.storyboard_ids):
        session.add(AssetVariantStoryboardLink(
            variant_plan_id=plan.id,
            storyboard_id=storyboard_id,
            source="director",
            confidence=data.confidence,
            created_by=user.id,
        ))
    await reconcile_asset_variant_dependencies(session, project_id, episode_id=data.episode_id)
    await session.commit()
    return await _variant_plan_read(session, plan, asset=asset)


async def update_asset_variant_plan(
    session: AsyncSession,
    plan_id: UUID,
    data: AssetVariantPlanUpdate,
    user: User,
) -> AssetVariantPlanRead:
    plan = await session.get(AssetVariantPlan, plan_id)
    if plan is None:
        raise KeyError(plan_id)
    await ensure_project_write_access(session, plan.project_id, user)
    asset = await session.get(Asset, plan.asset_id)
    if asset is None or str(asset.status) in _INACTIVE_TASK_ASSET_STATUSES:
        raise KeyError(plan_id)
    updates = data.model_dump(exclude_unset=True)
    storyboard_ids = updates.pop("storyboard_ids", None)
    for key, value in updates.items():
        setattr(plan, key, value)
    if storyboard_ids is not None:
        await _validate_variant_storyboards(session, plan.project_id, storyboard_ids, plan.episode_id)
        existing = list((await session.scalars(select(AssetVariantStoryboardLink).where(
            AssetVariantStoryboardLink.variant_plan_id == plan.id
        ))).all())
        requested = set(storyboard_ids)
        for link in existing:
            if link.storyboard_id in requested:
                link.status = "active"
                link.source = "director"
            elif link.status == "active":
                link.status = "removed"
                link.source = "director"
        existing_ids = {link.storyboard_id for link in existing}
        for storyboard_id in requested - existing_ids:
            session.add(AssetVariantStoryboardLink(
                variant_plan_id=plan.id,
                storyboard_id=storyboard_id,
                source="director",
                created_by=user.id,
            ))
    plan.updated_at = datetime.now(UTC)
    await reconcile_asset_variant_dependencies(session, plan.project_id, episode_id=plan.episode_id)
    await session.commit()
    return await _variant_plan_read(session, plan, asset=asset)


async def ensure_asset_variant_plans_for_storyboards(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    script_version_id: UUID | None = None,
    *,
    storyboard_ids: set[UUID] | None = None,
) -> int:
    """Compatibility no-op: storyboard-driven asset variants are retired."""
    return 0


async def _legacy_ensure_asset_variant_plans_for_storyboards(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    script_version_id: UUID | None = None,
    *,
    storyboard_ids: set[UUID] | None = None,
) -> int:
    episode = await session.get(ProjectEpisode, episode_id)
    if episode is None:
        raise ValueError("分集不存在。")
    storyboards = list((await session.scalars(select(Storyboard).where(
        Storyboard.project_id == project_id,
        Storyboard.episode_num == episode.episode_no,
    ).order_by(Storyboard.order_num))).all())
    if storyboard_ids is not None:
        storyboards = [item for item in storyboards if item.id in storyboard_ids]
    assets = list((await session.scalars(select(Asset).where(
        Asset.project_id == project_id,
        Asset.asset_type.in_([AssetType.scene, AssetType.prop]),
        Asset.status.notin_(["deleted", "excluded"]),
    ))).all())
    by_token: dict[str, Asset] = {}
    for asset in assets:
        for token in (asset.asset_code, asset.name, (asset.asset_code or "").split("-")[-1]):
            if token:
                by_token[_dep_token(token)] = asset
    desired: set[tuple[UUID, UUID]] = set()
    created = 0
    for storyboard in storyboards:
        candidates: list[tuple[Asset, str, str, str]] = []
        for asset in assets:
            if asset.asset_type == AssetType.scene:
                if not ({_dep_token(storyboard.scene_code), _dep_token(storyboard.scene_name)} & {
                    _dep_token(asset.asset_code), _dep_token(asset.name)
                } - {""}):
                    continue
                descriptor = str(storyboard.camera or storyboard.shot_type or "标准主视角").strip()
                title = f"{asset.name} {descriptor}"
                description = f"镜头视角：{descriptor}。画面内容：{storyboard.description or '保持场景母版布局和制作策略一致。'}"
                candidates.append((asset, "scene_view", descriptor, title + "｜" + description))
            else:
                tokens = {_dep_token(value) for value in (storyboard.characters or []) if value}
                for scene_asset in assets:
                    if scene_asset.asset_type != AssetType.scene:
                        continue
                    scene_matches = {
                        _dep_token(storyboard.scene_code),
                        _dep_token(storyboard.scene_name),
                    } & {
                        _dep_token(scene_asset.asset_code),
                        _dep_token(scene_asset.name),
                    } - {""}
                    if not scene_matches:
                        continue
                    scene_meta = scene_asset.metadata_json or {}
                    scene_breakdown = scene_meta.get("breakdown_asset") if isinstance(scene_meta.get("breakdown_asset"), dict) else {}
                    for value in list(scene_meta.get("key_prop_ids") or []) + list(scene_meta.get("key_props") or []):
                        tokens.add(_dep_token(value))
                    for value in list(scene_breakdown.get("key_prop_ids") or []) + list(scene_breakdown.get("key_props") or []):
                        tokens.add(_dep_token(value))
                if not tokens & {_dep_token(asset.asset_code), _dep_token(asset.name), _dep_token((asset.asset_code or "").split("-")[-1])}:
                    continue
                descriptor = str(storyboard.description or "分镜使用状态").strip()[:120]
                title = f"{asset.name} 分镜状态"
                description = f"道具状态：{descriptor}。保持道具母版的外形、材质和比例一致。"
                candidates.append((asset, "prop_state", descriptor, title + "｜" + description))
        for asset, kind, descriptor, combined in candidates:
            auto_key = f"{kind}:{hashlib.sha1(combined.encode('utf-8')).hexdigest()[:16]}"
            plan = await session.scalar(select(AssetVariantPlan).where(
                AssetVariantPlan.project_id == project_id,
                AssetVariantPlan.episode_id == episode_id,
                AssetVariantPlan.asset_id == asset.id,
                AssetVariantPlan.metadata_json["auto_key"].astext == auto_key,
                AssetVariantPlan.status != "retired",
            ))
            if plan is None:
                title, description = combined.split("｜", 1)
                plan = AssetVariantPlan(
                    project_id=project_id,
                    episode_id=episode_id,
                    script_version_id=script_version_id,
                    asset_id=asset.id,
                    variant_code=await _next_asset_variant_code(session, asset.id, episode_id),
                    variant_kind=kind,
                    title_zh=title,
                    description_zh=description,
                    source="auto",
                    confidence=0.82,
                    status="suggested",
                    metadata_json={"auto_key": auto_key, "descriptor_zh": descriptor},
                )
                session.add(plan)
                await session.flush()
                created += 1
            desired.add((plan.id, storyboard.id))
            link = await session.scalar(select(AssetVariantStoryboardLink).where(
                AssetVariantStoryboardLink.variant_plan_id == plan.id,
                AssetVariantStoryboardLink.storyboard_id == storyboard.id,
            ))
            if link is None:
                session.add(AssetVariantStoryboardLink(
                    variant_plan_id=plan.id,
                    storyboard_id=storyboard.id,
                    source="auto",
                    confidence=0.82,
                ))
            elif link.status != "active" and link.source == "auto":
                link.status = "active"
    auto_links = list((await session.scalars(select(AssetVariantStoryboardLink).join(
        AssetVariantPlan, AssetVariantPlan.id == AssetVariantStoryboardLink.variant_plan_id
    ).where(
        AssetVariantPlan.project_id == project_id,
        AssetVariantPlan.episode_id == episode_id,
        AssetVariantStoryboardLink.source == "auto",
        AssetVariantStoryboardLink.status == "active",
    ))).all())
    for link in auto_links:
        if (link.variant_plan_id, link.storyboard_id) not in desired:
            link.status = "removed"
    await session.flush()
    return created


def _asset_task_specs(asset: Asset) -> list[tuple[str, str, str]]:
    if asset.asset_type.value == "character":
        priority = _asset_priority(asset)
        specs = [
            ("A", "正面全身 A-Pose", "正面平视，全身完整，人物居中，双臂自然展开，纯净背景，作为后续视图的主参考图。"),
        ]
        if priority in {"S", "A"}:
            specs.extend(
                [
                    ("B", "侧面或 3/4 全身", "引用并严格保持图 A 的脸型、发型、体型和服装，生成侧面或 3/4 全身视图。"),
                    ("C", "背面全身", "引用并严格保持图 A，完整展示发型、服装背部结构和背面配饰。"),
                    ("D", "面部与表情参考", "引用图 A，生成面部近景与核心表情参考，保持五官和发型一致。"),
                    ("E", "服装与材质细节", "引用图 A，展示服装、配饰、纹理和关键材质细节，不改变整体设计。"),
                ]
            )
        return specs
    if asset.asset_type.value == "prop":
        return [("MASTER", "道具母版", "生成道具标准视图，明确外形、比例、材质、颜色、纹理和关键状态。")]
    return [("MASTER", "场景母版", "生成场景标准母版，明确空间布局、关键区域、主机位、光照和氛围。")]


def _asset_task_context_specs(asset: Asset) -> list[tuple[str | None, str | None, str, str, str]]:
    if asset.asset_type != AssetType.character:
        return [(None, None, *spec) for spec in _asset_task_specs(asset)]
    metadata = asset.metadata_json or {}
    source = metadata.get("breakdown_asset") if isinstance(metadata.get("breakdown_asset"), dict) else {}
    stages = source.get("age_stages") or metadata.get("age_stages") or []
    if not isinstance(stages, list) or not stages:
        return [(None, None, *spec) for spec in _asset_task_specs(asset)]
    output: list[tuple[str | None, str | None, str, str, str]] = []
    for stage in stages:
        if not isinstance(stage, dict):
            continue
        stage_code = str(stage.get("stage_code") or "").strip() or None
        variants = stage.get("costume_variants") if isinstance(stage.get("costume_variants"), list) else []
        scoped_variants = variants or [None]
        output_spec = str(stage.get("output_spec") or "A-E").upper()
        specs = _character_view_specs(full=output_spec != "A")
        for costume in scoped_variants:
            costume_code = str(costume.get("variant_code") or "").strip() if isinstance(costume, dict) else ""
            output.extend((stage_code, costume_code or None, *spec) for spec in specs)
    return output or [(None, None, *spec) for spec in _asset_task_specs(asset)]


def _character_view_specs(*, full: bool) -> list[tuple[str, str, str]]:
    specs = [("A", "正面全身 A-Pose", "正面平视，全身完整，人物居中，双臂自然展开，纯净背景，作为后续视图的主参考图。")]
    if full:
        specs.extend([
            ("B", "侧面或 3/4 全身", "引用并严格保持图 A 的脸型、发型、体型和服装，生成侧面或 3/4 全身视图。"),
            ("C", "背面全身", "引用并严格保持图 A，完整展示发型、服装背部结构和背面配饰。"),
            ("D", "面部与表情参考", "引用图 A，生成面部近景与核心表情参考，保持五官和发型一致。"),
            ("E", "服装与材质细节", "引用图 A，展示服装、配饰、纹理和关键材质细节，不改变整体设计。"),
        ])
    return specs


def _asset_context_prompt(
    asset: Asset,
    age_stage_code: str | None,
    costume_variant_code: str | None,
    variant: str,
    instruction: str,
) -> str:
    metadata = asset.metadata_json or {}
    source = metadata.get("breakdown_asset") if isinstance(metadata.get("breakdown_asset"), dict) else {}
    stages = source.get("age_stages") or metadata.get("age_stages") or []
    stage = next((item for item in stages if isinstance(item, dict) and item.get("stage_code") == age_stage_code), {})
    costumes = stage.get("costume_variants") if isinstance(stage, dict) and isinstance(stage.get("costume_variants"), list) else []
    costume = next((item for item in costumes if isinstance(item, dict) and item.get("variant_code") == costume_variant_code), {})
    view_prompt = _asset_view_prompt(source, metadata, variant, stage=stage, costume=costume)
    identity = _asset_character_identity_prompt(asset, source, metadata)
    age_change = _asset_age_stage_prompt(stage)
    costume_change = _asset_costume_variant_prompt(costume)
    context_sections = [
        f"角色身份锚点：{identity}" if identity else "",
        f"年龄阶段变化（{age_stage_code}）：{age_change}" if age_change else "",
        f"当前装扮变化（{costume_variant_code}）：{costume_change}" if costume_change else "",
    ]
    if view_prompt:
        negative_prompt = str(view_prompt.get("negative_prompt") or "").strip() or "避免身份与年龄漂移、骨相五官和体型变化、发型变化、服装结构或材质变化、错误肢体、裁切遮挡、透视畸变、模糊、文字和水印。"
        sections = [
            *context_sections,
            str(view_prompt.get("prompt") or "").strip(),
            f"负向要求：{negative_prompt}",
            f"姿态要求：{_asset_view_pose(variant, view_prompt.get('pose'))}" if view_prompt.get("pose") or variant in {"A", "B", "C", "D", "E"} else "",
            f"图位说明：{view_prompt.get('description')}" if view_prompt.get("description") else "",
            f"参考图依赖：{view_prompt.get('reference_requirement')}" if view_prompt.get("reference_requirement") else "",
            "生产要求：中性纯色摄影棚背景，柔和均匀布光，角色与服装色彩准确，轮廓和材质边缘清晰，无场景叙事元素干扰。",
        ]
        return "\n\n".join(item for item in sections if item)
    if asset.asset_type != AssetType.character:
        return _asset_variant_prompt(asset.prompt_text or "", variant, instruction)
    fallback_view = {
        "prompt": f"图位 {variant}：{_ASSET_VIEW_DEFINITIONS.get(variant, ('', instruction))[1] or instruction}",
        "negative_prompt": "避免身份与年龄漂移、骨相五官和体型变化、发型变化、服装结构或材质变化、错误肢体、裁切遮挡、透视畸变、模糊、文字和水印。",
        "pose": _ASSET_VIEW_POSES.get(variant, ""),
        "description": _ASSET_VIEW_DEFINITIONS.get(variant, ("", instruction))[1] or instruction,
        "reference_requirement": _asset_view_reference_requirement(variant),
    }
    sections = [
        *context_sections,
        fallback_view["prompt"],
        f"负向要求：{fallback_view['negative_prompt']}",
        f"姿态要求：{fallback_view['pose']}" if fallback_view["pose"] else "",
        f"图位说明：{fallback_view['description']}",
        f"参考图依赖：{fallback_view['reference_requirement']}",
        "生产要求：中性纯色摄影棚背景，柔和均匀布光，角色与服装色彩准确，轮廓和材质边缘清晰，无场景叙事元素干扰。",
    ]
    return "\n\n".join(item for item in sections if item)


def _asset_character_identity_prompt(asset: Asset, source: dict[str, Any], metadata: dict[str, Any]) -> str:
    source_metadata = source.get("metadata") if isinstance(source.get("metadata"), dict) else {}
    costume_design = metadata.get("costume_design") if isinstance(metadata.get("costume_design"), dict) else {}
    if not costume_design and isinstance(source_metadata.get("costume_design"), dict):
        costume_design = source_metadata["costume_design"]
    parts = [
        f"{asset.name}角色定装",
        _prompt_value_text(source.get("identity_setting") or metadata.get("identity_setting") or metadata.get("dramatic_function")),
        _prompt_value_text(source.get("visual_features") or metadata.get("visual_features") or metadata.get("appearance_clues")),
        str(asset.description or "").strip(),
        _prompt_value_text(source.get("material_layers") or costume_design.get("material_layers")),
        _prompt_value_text(source.get("continuity_constraints") or metadata.get("continuity_constraints") or metadata.get("continuity_anchors")),
        _prompt_value_text(source.get("forbidden_variations") or metadata.get("forbidden_variations")),
        _prompt_value_text(source.get("visual_effects") or metadata.get("visual_effects")),
    ]
    design_prompt = str(costume_design.get("prompt") or "").strip()
    if design_prompt and not _is_fused_asset_view_text(design_prompt):
        parts.append(design_prompt)
    asset_prompt = str(asset.prompt_text or "").strip()
    if asset_prompt and not _is_fused_asset_view_text(asset_prompt):
        parts.append(asset_prompt)
    return "，".join(dict.fromkeys(part for part in parts if part and part not in {"None", "null"}))


def _asset_age_stage_prompt(stage: dict[str, Any]) -> str:
    if not stage:
        return ""
    return _prompt_value_text({
        "阶段编号": stage.get("stage_code"),
        "阶段名称": stage.get("name"),
        "年龄范围": stage.get("age_range"),
        "时间线": stage.get("timeline"),
        "面部变化": stage.get("face_changes"),
        "体型变化": stage.get("body_changes"),
        "发型与肤质变化": stage.get("hair_skin_changes"),
        "跨年龄识别锚点": stage.get("identity_anchors"),
        "禁止变化": stage.get("forbidden_changes"),
    })


def _asset_costume_variant_prompt(costume: dict[str, Any]) -> str:
    if not costume:
        return ""
    return _prompt_value_text({
        "装扮编号": costume.get("variant_code"),
        "装扮名称": costume.get("name"),
        "装扮说明": costume.get("description"),
        "服饰道具": costume.get("costume_prop_snapshot") or costume.get("costume_prop_names"),
        "材质分层": costume.get("material_layers"),
    })


def _asset_view_prompt(
    source: dict[str, Any],
    metadata: dict[str, Any],
    variant: str,
    *,
    stage: dict[str, Any] | None = None,
    costume: dict[str, Any] | None = None,
) -> dict[str, Any]:
    source_metadata = source.get("metadata") if isinstance(source.get("metadata"), dict) else {}
    base_costume = metadata.get("costume_design") if isinstance(metadata.get("costume_design"), dict) else {}
    if not base_costume and isinstance(source_metadata.get("costume_design"), dict):
        base_costume = source_metadata["costume_design"]
    scoped_costume = costume if isinstance(costume, dict) and ("variant_code" in costume or "costume_prop_snapshot" in costume) else {}
    raw = (
        scoped_costume.get("view_prompts")
        or (stage or {}).get("view_prompts")
        or base_costume.get("view_prompts")
        or source.get("view_prompts")
        or metadata.get("view_prompts")
        or []
    )
    if not raw:
        character_apose = base_costume.get("character_apose") if isinstance(base_costume.get("character_apose"), dict) else {}
        raw = character_apose.get("angles") if isinstance(character_apose.get("angles"), list) else []
    if isinstance(raw, dict):
        candidate = raw.get(variant)
        source_view = dict(candidate) if isinstance(candidate, dict) else {}
    else:
        source_view = next(
            (dict(item) for item in raw if isinstance(item, dict) and str(item.get("code") or item.get("view_code") or "").upper() == variant.upper()),
            {},
        )
    if variant.upper() not in _ASSET_VIEW_DEFINITIONS and not source_view:
        return {}
    title, default_description = _ASSET_VIEW_DEFINITIONS.get(variant.upper(), (variant, ""))
    description = str(source_view.get("description") or default_description).strip()
    pose = _asset_view_pose(variant, source_view.get("pose"))
    prompt = str(source_view.get("prompt") or "").strip()
    if _is_fused_asset_view_text(prompt):
        prompt = ""
    if not _is_production_ready_asset_view_prompt(prompt):
        prompt = "\n".join(part for part in (
            prompt,
            f"图位 {variant.upper()}：{description}",
            f"姿态要求：{pose}" if pose else "",
        ) if part)
    negative_prompt = str(
        source_view.get("negative_prompt")
        or base_costume.get("negative_prompt")
        or source.get("negative_prompt")
        or metadata.get("negative_prompt")
        or "避免改变角色身份、年龄、骨相、五官、体型、发型、服装结构和材质；避免错误肢体、手指异常、重复身体、裁切、遮挡、透视畸变、模糊、低清晰度、文字、水印和标识。"
    ).strip()
    return {
        "code": variant.upper(),
        "title": str(source_view.get("title") or source_view.get("angle") or title).strip(),
        "prompt": prompt,
        "negative_prompt": negative_prompt,
        "pose": pose,
        "description": description,
        "reference_requirement": str(
            source_view.get("reference_requirement") or _asset_view_reference_requirement(variant)
        ).strip(),
        "source_type": str(source_view.get("source_type") or "backend_normalized"),
    }


_ASSET_VIEW_DEFINITIONS = {
    "A": ("正面全身 A-Pose", "正面平视，全身从头到脚完整呈现，人物居中，作为后续图位的主参考图。"),
    "B": ("侧面或 3/4 全身", "侧面或 45 度半侧全身构图，从头到脚完整呈现，保持图 A 的角色与服装一致。"),
    "C": ("背面全身", "背面平视全身构图，完整展示发型、服装和配饰背面，保持图 A 设计。"),
    "D": ("面部与表情参考", "面部近景构图，清晰展示五官、发型、肤色和核心表情，保持图 A 一致。"),
    "E": ("服装与材质细节", "服装与配饰细节构图，清晰展示层次、纹理、接缝和关键材质。"),
}


def _asset_view_reference_requirement(variant: str) -> str:
    if variant.upper() == "A":
        return "无前置参考图；输出将作为 B-E 的主参考图。"
    return "必须使用已审核通过的图 A，保持角色身份、骨相、发型、体型和服装一致。"


def _is_fused_asset_view_text(value: str) -> bool:
    markers = ("正面", "侧面", "45", "背面", "面部近景", "服装细节", "材质细节")
    codes = ("图位 A", "图位 B", "图位 C", "图位 D", "图位 E", "图A", "图B", "图C", "图D", "图E")
    return sum(marker in value for marker in markers) >= 3 or sum(code in value for code in codes) >= 2


def _has_foreign_asset_view_sections(value: str, variant: str) -> bool:
    current = variant.upper()
    codes = {
        match.upper()
        for match in re.findall(r"图(?:位)?\s*([A-E])\s*[:：]", value, flags=re.IGNORECASE)
    }
    return any(code != current for code in codes)


def _is_production_ready_asset_view_prompt(value: str) -> bool:
    return len("".join(value.split())) >= 80


_ASSET_VIEW_POSES = {
    "A": "标准 A-Pose 站立，双臂从身体两侧自然展开约 30 度，手指放松，双脚与肩同宽，身体重心居中，神态自然。",
    "B": "自然直立，四肢放松且不遮挡服装轮廓，头部与身体朝向一致，神态保持与图 A 连续。",
    "C": "背向镜头自然直立，双臂稍离躯干，手部放松，不遮挡服装背部结构和配饰。",
    "D": "头部自然稳定，双唇自然闭合，目光与角色设定一致，避免夸张表情和遮挡五官。",
    "E": "保持静止且服装自然垂落，手臂和配饰不遮挡需要展示的材质、纹理与结构细节。",
}


def _asset_view_pose(variant: str, value: Any) -> str:
    pose = str(value or "").strip()
    markers = ("正面", "侧面", "45", "背面", "面部近景", "服装细节", "材质细节")
    codes = ("图位 A", "图位 B", "图位 C", "图位 D", "图位 E")
    if not pose or sum(marker in pose for marker in markers) >= 3 or sum(code in pose for code in codes) >= 2:
        return _ASSET_VIEW_POSES.get(variant.upper(), pose)
    return pose


def _asset_variant_prompt(base_prompt: str, variant: str, instruction: str) -> str:
    dependency = "\n一致性要求：使用当前生产单元的图 A 作为角色一致性参考，A-E 统一提交审核。" if variant in {"B", "C", "D", "E"} else ""
    return f"{base_prompt}\n\n任务图位：{variant}\n图位要求：{instruction}{dependency}".strip()


def _asset_item_variant_prompt(item: dict[str, Any], base_prompt: str, variant: str, instruction: str) -> str:
    metadata = item.get("metadata") if isinstance(item.get("metadata"), dict) else {}
    view = _asset_view_prompt(item, metadata, variant)
    if not view:
        return _asset_variant_prompt(base_prompt, variant, instruction)
    return "\n\n".join(part for part in [
        str(view.get("prompt") or "").strip(),
        f"负向要求：{view.get('negative_prompt')}" if view.get("negative_prompt") else "",
        f"姿态要求：{view.get('pose')}" if view.get("pose") else "",
        f"图位说明：{view.get('description')}" if view.get("description") else "",
        f"参考图依赖：{view.get('reference_requirement')}" if view.get("reference_requirement") else "",
    ] if part)


async def generate_storyboard_tasks(
    session: AsyncSession,
    project_id: UUID,
    episode_id: UUID,
    script_version_id: UUID,
    user: User,
) -> dict[str, Any]:
    """Create flat keyframe/video task pairs for each storyboard."""
    await ensure_project_write_access(session, project_id, user)
    project = await session.get(Project, project_id, with_for_update=True)
    if project is None:
        raise KeyError(project_id)
    episode = await session.get(ProjectEpisode, episode_id)
    script_version = await session.get(ScriptVersion, script_version_id)
    script = await session.get(Script, script_version.script_id) if script_version else None
    if episode is None or episode.project_id != project_id or script is None or script.project_id != project_id or script.episode_id != episode_id:
        raise ValueError("分集与剧本版本不匹配。")
    storyboards = (
        await session.execute(
            select(Storyboard).where(
                Storyboard.project_id == project_id,
                Storyboard.episode_num == episode.episode_no,
            ).order_by(Storyboard.order_num)
        )
    ).scalars().all()
    breakdowns = list((await session.scalars(
        select(ScriptBreakdown)
        .where(ScriptBreakdown.project_id == project_id)
        .order_by(ScriptBreakdown.version.desc())
    )).all())
    breakdown = next((item for item in breakdowns if _payload_matches_lineage_values(
        item.content_json, episode_id, script_version_id
    )), None)
    breakdown_content = dict(breakdown.content_json or {}) if breakdown else {}
    final_revision_id = _uuid_or_none(dict(breakdown_content.get("raw_output") or {}).get("confirmed_storyboard_revision_id"))
    final_revision = await session.get(ArtifactRevision, final_revision_id) if final_revision_id else None
    if final_revision is None or final_revision.data_state != "final":
        raise ValueError("分镜尚未形成 human_final 修订，不能生成分镜任务。")
    validation = validate_breakdown_view(breakdown_content)
    if validation.blocking_errors:
        details = "；".join(item.message for item in validation.blocking_errors[:8])
        raise ValueError(f"分镜任务生成前校验失败：{details}")
    current_items = [
        item for item in list(breakdown_content.get("storyboards") or [])
        if isinstance(item, dict) and _storyboard_episode_code(item) == episode.episode_code
    ] if breakdown else []
    if current_items:
        current_codes = {str(item.get("storyboard_code") or "").strip() for item in current_items}
        current_codes.discard("")
        current_orders = {
            _safe_int(item.get("order_num") or item.get("order_no"), index)
            for index, item in enumerate(current_items, start=1)
        }
        storyboards = [
            item for item in storyboards
            if (item.storyboard_code and item.storyboard_code in current_codes) or item.order_num in current_orders
        ]
    if not storyboards:
        raise ValueError("没有分镜数据，请先完成并保存分镜拆解。")
    current_storyboard_ids = {item.id for item in storyboards}
    episode_storyboard_tasks = list((await session.scalars(
        select(Task).where(
            Task.project_id == project_id,
            Task.episode_id == episode_id,
            Task.script_version_id == script_version_id,
            Task.storyboard_id.is_not(None),
            Task.is_retired.is_(False),
            Task.task_type.in_([
                TaskType.storyboard_shot, TaskType.text_to_image, TaskType.image_to_video
            ]),
        )
    )).all())
    stale_tasks = [
        item for item in episode_storyboard_tasks
        if item.storyboard_id not in current_storyboard_ids
    ]
    if stale_tasks:
        raise ValueError(
            f"新分镜版本移除了 {len({item.storyboard_id for item in stale_tasks})} 个已分发分镜，"
            f"关联 {len(stale_tasks)} 条旧任务；为避免新旧任务并存，已停止自动同步，请先处理任务迁移。"
        )
    now = datetime.now(UTC)
    created = 0
    updated = 0
    protected_conflicts: list[str] = []
    for storyboard in storyboards:
        existing_tasks = list((await session.scalars(
            select(Task).where(
                Task.project_id == project_id,
                Task.episode_id == episode_id,
                Task.script_version_id == script_version_id,
                Task.storyboard_id == storyboard.id,
                Task.is_retired.is_(False),
                Task.task_type.in_([
                    TaskType.storyboard_shot, TaskType.text_to_image, TaskType.image_to_video
                ]),
            )
        )).all())
        if any(item.task_type == TaskType.storyboard_shot for item in existing_tasks):
            continue
        existing_by_type = {item.task_type: item for item in existing_tasks}
        scene_label = storyboard.scene_name or storyboard.scene_code or f"EP{storyboard.episode_num:02d}"
        keyframe_prompt = _storyboard_keyframe_prompt(storyboard)
        video_prompt = _storyboard_video_prompt(storyboard)
        keyframe_task = existing_by_type.get(TaskType.text_to_image)
        if keyframe_task is None:
            keyframe_task = Task(
                project_id=project_id,
                episode_id=episode_id,
                script_id=script.id,
                script_version_id=script_version_id,
                storyboard_id=storyboard.id,
                script_segment_id=storyboard.script_segment_id,
                scene_code=storyboard.scene_code,
                scene_name=storyboard.scene_name,
                task_type=TaskType.text_to_image,
                idempotency_key=_task_idempotency_key(
                    project_id=project_id,
                    episode_id=episode_id,
                    script_version_id=script_version_id,
                    task_type=TaskType.text_to_image,
                    storyboard_id=storyboard.id,
                ),
                title=f"{scene_label} 分镜 {storyboard.order_num:03d} 关键帧",
                status=TaskStatus.todo,
                prompt_text=keyframe_prompt,
                latest_prompt_text=keyframe_prompt,
                production_model="Seedream",
                due_at=now + timedelta(days=2),
            )
            session.add(keyframe_task)
            await session.flush()
            created += 1
        else:
            keyframe_updated, keyframe_blocked = _sync_existing_storyboard_task(
                keyframe_task,
                storyboard,
                title=f"{scene_label} 分镜 {storyboard.order_num:03d} 关键帧",
                prompt=keyframe_prompt,
                now=now,
            )
            if keyframe_blocked:
                protected_conflicts.append(keyframe_task.title)
            elif keyframe_updated:
                updated += 1
        if TaskType.image_to_video not in existing_by_type:
            session.add(Task(
                project_id=project_id,
                episode_id=episode_id,
                script_id=script.id,
                script_version_id=script_version_id,
                storyboard_id=storyboard.id,
                script_segment_id=storyboard.script_segment_id,
                scene_code=storyboard.scene_code,
                scene_name=storyboard.scene_name,
                task_type=TaskType.image_to_video,
                idempotency_key=_task_idempotency_key(
                    project_id=project_id,
                    episode_id=episode_id,
                    script_version_id=script_version_id,
                    task_type=TaskType.image_to_video,
                    storyboard_id=storyboard.id,
                ),
                depends_on_task_id=keyframe_task.id,
                assignee_id=keyframe_task.assignee_id,
                title=f"{scene_label} 分镜 {storyboard.order_num:03d} 视频",
                status=TaskStatus.todo,
                prompt_text=video_prompt,
                latest_prompt_text=video_prompt,
                production_model="Seedance",
                due_at=now + timedelta(days=3),
            ))
            created += 1
        else:
            video_task = existing_by_type[TaskType.image_to_video]
            video_updated, video_blocked = _sync_existing_storyboard_task(
                video_task,
                storyboard,
                title=f"{scene_label} 分镜 {storyboard.order_num:03d} 视频",
                prompt=video_prompt,
                now=now,
            )
            if video_blocked:
                protected_conflicts.append(video_task.title)
            elif video_updated:
                updated += 1
    if protected_conflicts:
        samples = "、".join(protected_conflicts[:5])
        suffix = f" 等 {len(protected_conflicts)} 条" if len(protected_conflicts) > 5 else ""
        raise ValueError(
            f"新分镜与已指派、已开工或人工修改的任务存在 Prompt 冲突：{samples}{suffix}。"
            "系统未覆盖任何旧任务，请先确认任务迁移方案。"
        )
    auto_variant_plans = 0
    variant_dependencies = {"plans": 0, "variant_tasks": 0, "dependencies": 0}
    await add_operation_log(
        session,
        action="storyboard_tasks_generated",
        target_type="project",
        operator_id=user.id,
        project_id=project_id,
        detail={
            "created_tasks": created,
            "updated_tasks": updated,
            "storyboards": len(storyboards),
            "episode_id": str(episode_id),
            "script_version_id": str(script_version_id),
            "auto_variant_plans": auto_variant_plans,
            "variant_dependencies": variant_dependencies,
        },
    )
    await session.commit()
    return {
        "created_tasks": created,
        "updated_tasks": updated,
        "storyboards": len(storyboards),
        "episode_id": str(episode_id),
        "script_version_id": str(script_version_id),
        "auto_variant_plans": auto_variant_plans,
        "variant_dependencies": variant_dependencies,
    }


def _sync_existing_storyboard_task(
    task: Task,
    storyboard: Storyboard,
    *,
    title: str,
    prompt: str,
    now: datetime,
) -> tuple[bool, bool]:
    needs_update = any((
        task.script_segment_id != storyboard.script_segment_id,
        task.scene_code != storyboard.scene_code,
        task.scene_name != storyboard.scene_name,
        task.title != title,
        task.prompt_text != prompt,
        task.latest_prompt_text != prompt,
    ))
    if not needs_update:
        return False, False
    protected = (
        task.status != TaskStatus.todo
        or task.assignee_id is not None
        or task.prompt_text != task.latest_prompt_text
    )
    if protected:
        return False, True
    task.script_segment_id = storyboard.script_segment_id
    task.scene_code = storyboard.scene_code
    task.scene_name = storyboard.scene_name
    task.title = title
    task.prompt_text = prompt
    task.latest_prompt_text = prompt
    task.updated_at = now
    return True, False


def _storyboard_keyframe_prompt(storyboard: Storyboard) -> str:
    return "\n".join(part for part in [
        f"分镜：{storyboard.storyboard_code}" if storyboard.storyboard_code else "",
        f"画面：{storyboard.description}" if storyboard.description else "",
        f"镜头：{storyboard.camera}" if storyboard.camera else "",
        f"景别：{storyboard.shot_type}" if storyboard.shot_type else "",
        f"角色：{'、'.join(storyboard.characters or [])}" if storyboard.characters else "",
    ] if part)


def _storyboard_video_prompt(storyboard: Storyboard) -> str:
    sections = [_storyboard_keyframe_prompt(storyboard)]
    mirror_lines: list[str] = []
    for index, item in enumerate(storyboard.mirror_shots or [], start=1):
        if not isinstance(item, dict):
            continue
        code = str(item.get("id") or item.get("label") or chr(64 + index)).strip()
        parameters = " / ".join(str(value).strip() for value in (
            item.get("shot_size"),
            f"{item.get('duration_seconds')}秒" if item.get("duration_seconds") not in (None, "") else "",
        ) if str(value or "").strip())
        mirror_lines.append("\n".join(part for part in [
            f"{code}{f'（{parameters}）' if parameters else ''}",
            f"画面：{item.get('description')}" if item.get("description") else "",
            f"镜头：{item.get('camera')}" if item.get("camera") else "",
            f"角色：{'、'.join(str(value) for value in item.get('characters') or [])}" if item.get("characters") else "",
            f"台词：{item.get('dialogue')}" if item.get("dialogue") else "",
        ] if part))
    if mirror_lines:
        sections.append("镜中分镜（按顺序执行）：\n" + "\n\n".join(mirror_lines))
    return "\n\n".join(section for section in sections if section)


async def bulk_assign_tasks(
    session: AsyncSession,
    project_id: UUID,
    user: User,
    *,
    scope: str,
    key: str,
    assignee_id: UUID | None,
    episode_id: UUID | None = None,
    reassignment_reason: str | None = None,
) -> dict[str, Any]:
    """Assign tasks in bulk. scope='asset_category' (key=character|scene|prop|music),
    scope='asset_context' (key=asset_id|age_stage|costume_variant),
    scope='scene' (key=scene_code or scene_name), scope='all_assets', scope='all_storyboards'."""
    await ensure_project_write_access(session, project_id, user)
    if assignee_id is not None:
        assignee = await session.get(User, assignee_id)
        if assignee is None or not assignee.is_active or assignee.role in {UserRole.director, UserRole.admin}:
            raise ValueError("执行人无效、已停用或不是可分配的制作人员。")
    stmt = select(Task).where(Task.project_id == project_id, _active_asset_task_condition())
    if scope == "asset_category":
        asset_types = [AssetType.music, AssetType.voice_profile] if key == "music" else [_asset_type_enum(key)]
        asset_ids = (
            await session.execute(
                select(Asset.id).where(
                    Asset.project_id == project_id,
                    Asset.asset_type.in_(asset_types),
                    Asset.status.notin_(_INACTIVE_TASK_ASSET_STATUSES),
                )
            )
        ).scalars().all()
        task_types = [TaskType.audio] if key == "music" else [TaskType.asset]
        stmt = stmt.where(Task.task_type.in_(task_types), Task.asset_id.in_(asset_ids or [None]))
    elif scope == "asset_context":
        parts = key.split("|", 2)
        if len(parts) != 3:
            raise ValueError("资产任务上下文格式错误。")
        asset_id = UUID(parts[0])
        asset = await session.get(Asset, asset_id)
        if (
            asset is None
            or asset.project_id != project_id
            or str(asset.status) in _INACTIVE_TASK_ASSET_STATUSES
        ):
            raise ValueError("资产任务上下文不存在。")
        task_types = [TaskType.audio] if asset.asset_type in {AssetType.music, AssetType.voice_profile} else [TaskType.asset]
        stmt = stmt.where(Task.task_type.in_(task_types), Task.asset_id == asset_id)
        if asset.asset_type != AssetType.character:
            stmt = stmt.where(
                func.coalesce(Task.age_stage_code, "") == parts[1],
                func.coalesce(Task.costume_variant_code, "") == parts[2],
            )
    elif scope == "all_assets":
        stmt = stmt.where(Task.task_type.in_([TaskType.asset, TaskType.audio]))
    elif scope == "scene":
        stmt = stmt.where(
            Task.task_type.in_([TaskType.storyboard_shot, TaskType.text_to_image, TaskType.image_to_video]),
            Task.storyboard_id.is_not(None),
            or_(Task.scene_code == key, Task.scene_name == key),
        )
        if episode_id:
            stmt = stmt.where(Task.episode_id == episode_id)
    elif scope == "all_storyboards":
        stmt = stmt.where(
            Task.task_type.in_([TaskType.storyboard_shot, TaskType.text_to_image, TaskType.image_to_video]),
            Task.storyboard_id.is_not(None),
        )
        if episode_id:
            stmt = stmt.where(Task.episode_id == episode_id)
    else:
        raise ValueError(f"未知的分配范围: {scope}")

    candidates = list((await session.execute(stmt)).scalars().all())
    reason = str(reassignment_reason or "").strip()
    assignable_statuses = {TaskStatus.todo, TaskStatus.rejected}
    if reason:
        assignable_statuses.update({TaskStatus.in_progress, TaskStatus.overdue})
    tasks = [task for task in candidates if task.status in assignable_statuses]
    skipped_by_status: dict[str, int] = {}
    for task in candidates:
        if task.status in assignable_statuses:
            continue
        status_key = task.status.value if hasattr(task.status, "value") else str(task.status)
        skipped_by_status[status_key] = skipped_by_status.get(status_key, 0) + 1
    completed_tasks = [task for task in candidates if task.status == TaskStatus.completed]
    completed_user_ids = {task.assignee_id for task in completed_tasks if task.assignee_id}
    completed_users = {
        item.id: item.display_name for item in (await session.scalars(select(User).where(User.id.in_(completed_user_ids)))).all()
    } if completed_user_ids else {}
    reassignments = [
        (task, task.assignee_id)
        for task in tasks
        if task.assignee_id is not None and task.assignee_id != assignee_id
    ]
    if reassignments and not reason:
        raise ValueError("批量改派必须填写改派原因。")
    now = datetime.now(UTC)
    for task in tasks:
        task.assignee_id = assignee_id
        task.assigned_by = user.id
        task.assigned_at = now
        task.updated_at = now
    if reassignments:
        await _add_task_reassignment_notifications(
            session,
            reassignments=reassignments,
            new_assignee_id=assignee_id,
            reason=reason,
        )
    await add_operation_log(
        session,
        action="tasks_bulk_assigned",
        target_type="project",
        operator_id=user.id,
        project_id=project_id,
        detail={
            "scope": scope,
            "key": key,
            "episode_id": str(episode_id) if episode_id else None,
            "assignee_id": str(assignee_id) if assignee_id else None,
            "count": len(tasks),
            "reassigned_count": len(reassignments),
            "reassignment_reason": reason or None,
            "skipped_by_status": skipped_by_status,
        },
    )
    await session.commit()
    return {
        "assigned": len(tasks),
        "reassigned": len(reassignments),
        "scope": scope,
        "key": key,
        "skipped": len(candidates) - len(tasks),
        "skipped_by_status": skipped_by_status,
        "completed_assignments": [
            {
                "task_id": str(task.id),
                "title": task.title,
                "assignee_id": str(task.assignee_id) if task.assignee_id else None,
                "assignee_name": completed_users.get(task.assignee_id),
            }
            for task in completed_tasks
        ],
    }


async def _scene_gating_core(session: AsyncSession, project_id: UUID) -> list[dict[str, Any]]:
    deps = await compute_scene_asset_dependencies(session, project_id)
    completed_codes = set((await session.scalars(
        select(Asset.asset_code)
        .join(Task, Task.asset_id == Asset.id)
        .join(Submission, Submission.task_id == Task.id)
        .join(AssetVersion, AssetVersion.source_submission_id == Submission.id)
        .where(
            Asset.project_id == project_id,
            Asset.status.notin_(_INACTIVE_TASK_ASSET_STATUSES),
            Task.task_type.in_([TaskType.asset, TaskType.text_to_image]),
            Task.status == TaskStatus.completed,
            Task.is_retired.is_(False),
            Submission.status.in_(APPROVED_SUBMISSION_STATUSES),
            Submission.is_selected.is_(True),
            Submission.is_archived.is_(False),
            Submission.is_invalidated.is_(False),
            Submission.file_id.is_not(None),
            AssetVersion.is_invalidated.is_(False),
            AssetVersion.purged_at.is_(None),
            AssetVersion.asset_id == Asset.id,
            AssetVersion.file_id == Submission.file_id,
        )
        .distinct()
    )).all())

    output: list[dict[str, Any]] = []
    for scene_code, info in deps.items():
        required = list(info["required_asset_codes"])
        pending = [c for c in required if c not in completed_codes]
        output.append(
            {
                "scene_code": scene_code,
                "scene_name": info.get("scene_name"),
                "required_asset_codes": info["required_asset_codes"],
                "pending_asset_codes": pending,
                "locked": bool(pending),
            }
        )
    return output


async def scene_gating(session: AsyncSession, project_id: UUID, user: User) -> list[dict[str, Any]]:
    """Per-scene lock status: a scene is unlocked once all its required asset tasks are completed."""
    if not is_director_or_admin(user) and not await _can_access_project(session, project_id, user):
        raise PermissionError(project_id)
    return await _scene_gating_core(session, project_id)


async def _scene_is_locked(session: AsyncSession, project_id: UUID, scene_code: str | None, scene_name: str | None) -> bool:
    if not scene_code and not scene_name:
        return False
    for entry in await _scene_gating_core(session, project_id):
        if entry["scene_code"] == scene_code or (scene_name and entry["scene_name"] == scene_name):
            return entry["locked"]
    return False


def _asset_description(item: dict[str, Any]) -> str:
    attributes = item.get("attributes") if isinstance(item.get("attributes"), dict) else {}
    parts = [
        item.get("description"),
        attributes.get("appearance"),
        attributes.get("visual_goal"),
        attributes.get("scene_description"),
        attributes.get("core_requirement"),
    ]
    return "\n".join(str(part) for part in parts if part)[:2000]


def _merge_asset_task_context(content: dict[str, Any]) -> dict[str, Any]:
    assets = [dict(item) for item in content.get("assets") or [] if isinstance(item, dict)]
    designs = [item for item in (content.get("asset_prompt_designs") or content.get("costume_designs") or []) if isinstance(item, dict)]
    review_items = [item for item in content.get("manual_review_items") or [] if isinstance(item, dict)]
    designs_by_code = {
        str(item.get("asset_code") or item.get("character_id") or "").strip(): item
        for item in designs
        if str(item.get("asset_code") or item.get("character_id") or "").strip()
    }
    designs_by_name = {
        str(item.get("asset_name") or item.get("character_name") or item.get("name") or "").strip(): item
        for item in designs
        if str(item.get("asset_name") or item.get("character_name") or item.get("name") or "").strip()
    }
    merged_assets: list[dict[str, Any]] = []
    for asset in assets:
        code = str(asset.get("asset_code") or "").strip()
        name = str(asset.get("name") or "").strip()
        design = designs_by_code.get(code) or designs_by_name.get(name)
        hints = [item for item in review_items if code and str(item.get("code") or "").strip() == code]
        metadata = dict(asset.get("metadata") or {})
        if design:
            costume = {
                key: design.get(key)
                for key in ("prompt", "negative_prompt", "aspect_ratio", "material_layers", "character_apose", "spatial_bible", "material_bible", "state_prompts", "output_spec", "view_prompts", "error", "brief_trace", "resolved_production_brief", "production_context", "skill", "skill_version", "contract_version", "contract_hash", "prompt_version", "input_hash")
                if design.get(key) not in (None, "", [], {})
            }
            metadata["costume_design"] = costume
            asset.update(
                {
                    "prompt": design.get("prompt") or design.get("positive_prompt") or asset.get("prompt") or "",
                    "negative_prompt": design.get("negative_prompt") or asset.get("negative_prompt") or "",
                    "material_layers": design.get("material_layers") or asset.get("material_layers") or {},
                    "character_apose": design.get("character_apose") or asset.get("character_apose") or {},
                    "aspect_ratio": design.get("aspect_ratio") or asset.get("aspect_ratio") or "1:1",
                    "view_prompts": design.get("view_prompts") or asset.get("view_prompts") or [],
                    "state_prompts": design.get("state_prompts") or asset.get("state_prompts") or [],
                    "spatial_bible": design.get("spatial_bible") or asset.get("spatial_bible") or {},
                    "material_bible": design.get("material_bible") or asset.get("material_bible") or {},
                }
            )
        if hints:
            asset["production_hints"] = hints
            metadata["production_hints"] = hints
        asset["metadata"] = metadata
        merged_assets.append(asset)
    return {**content, "assets": merged_assets}


def _asset_prompt_text(item: dict[str, Any]) -> str:
    prompt = item.get("prompt") or item.get("prompt_text") or item.get("stable_prompt")
    name = item.get("name") or item.get("role_name") or item.get("scene_name") or item.get("prop_name") or item.get("asset_code")
    base_prompt = str(prompt) if prompt else "\n".join(
        part
        for part in [
            f"请生成资产文生图：{name}",
            f"资产类型：{_normalize_asset_type(item.get('asset_type'))}",
            f"制作要求：{_asset_description(item) or '保持与项目视觉风格一致，便于后续分镜生产复用。'}",
            f"原文依据：{item.get('source_text') or item.get('source') or ''}",
        ]
        if part
    )
    metadata = item.get("metadata") if isinstance(item.get("metadata"), dict) else {}
    costume = metadata.get("costume_design") if isinstance(metadata.get("costume_design"), dict) else {}
    negative_prompt = item.get("negative_prompt") or costume.get("negative_prompt")
    material_layers = item.get("material_layers") or costume.get("material_layers")
    character_apose = item.get("character_apose") or costume.get("character_apose")
    hints = item.get("production_hints") or metadata.get("production_hints") or []
    hint_lines = []
    for hint in hints if isinstance(hints, list) else [hints]:
        if isinstance(hint, dict):
            text = str(hint.get("human_input") or hint.get("detail") or hint.get("item") or hint.get("message") or "").strip()
        else:
            text = str(hint or "").strip()
        if text and text not in hint_lines:
            hint_lines.append(text)
    sections = [base_prompt]
    if negative_prompt:
        sections.append(f"负向要求：{negative_prompt}")
    if character_apose:
        sections.append(f"定装姿态：{_prompt_value_text(character_apose)}")
    if material_layers:
        sections.append(f"材质要求：{_prompt_value_text(material_layers)}")
    if hint_lines:
        sections.append("生产补充提示：\n" + "\n".join(f"- {line}" for line in hint_lines))
    return "\n\n".join(section for section in sections if section)


def _prompt_value_text(value: Any) -> str:
    if isinstance(value, dict):
        return "；".join(f"{key}：{_prompt_value_text(item)}" for key, item in value.items() if item not in (None, "", [], {}))
    if isinstance(value, list):
        return "、".join(_prompt_value_text(item) for item in value if item not in (None, "", [], {}))
    return str(value)


def _uuid_or_none(value: Any) -> UUID | None:
    if not value:
        return None
    try:
        return UUID(str(value))
    except ValueError:
        return None


def _segments_from_breakdown_output(project: Project, output: dict[str, Any]) -> list[dict[str, Any]]:
    explicit = output.get("script_segments")
    if isinstance(explicit, list) and explicit:
        segments: list[dict[str, Any]] = []
        code_prefix = _safe_code_prefix(project.project_prefix or project.project_no or "RF")
        for index, item in enumerate(explicit, start=1):
            if not isinstance(item, dict):
                continue
            episode_code = str(item.get("episode_code") or "EP01").strip().upper() or "EP01"
            if episode_code.isdigit():
                episode_code = f"EP{int(episode_code):02d}"
            if not episode_code.startswith("EP"):
                episode_code = "EP01"
            order_no = _safe_int(item.get("order_no") or item.get("order_num") or item.get("storyboard_order_num"), index)
            description = str(
                item.get("description")
                or item.get("summary")
                or item.get("source_text")
                or item.get("title")
                or "未提供原文依据"
            )
            normalized = dict(item)
            agent_script_segment_code = normalized.get("script_segment_code")
            generated_code = f"{code_prefix}-{episode_code}-J{order_no:03d}"
            if agent_script_segment_code and str(agent_script_segment_code) != generated_code:
                normalized.setdefault("agent_script_segment_code", str(agent_script_segment_code))
            normalized["script_segment_code"] = generated_code
            normalized["episode_code"] = episode_code
            normalized.setdefault("order_no", order_no)
            normalized.setdefault("title", f"脚本段 {order_no:03d}")
            normalized.setdefault("source_text", description[:1200])
            normalized.setdefault("summary", description[:240])
            normalized.setdefault("story_function", "未明确")
            normalized.setdefault("dominant_emotion", "未明确")
            normalized.setdefault("rhythm", str(normalized.get("camera") or "未明确")[:80])
            normalized.setdefault("viewpoint", "未明确")
            normalized.setdefault("storyboard_order_num", order_no)
            normalized.setdefault("description", description)
            normalized.setdefault("dialogue", "")
            normalized.setdefault("camera", "中景，稳定镜头，保留角色动作和场景关系")
            normalized.setdefault("duration_seconds", 5)
            normalized.setdefault("characters", [])
            normalized.update(_script_segment_extended_metadata(normalized))
            normalized.setdefault("keyframes", [{"order": 1, "description": description[:120]}])
            normalized.setdefault("source_context", {"before": "", "current": normalized["source_text"], "after": ""})
            segments.append(normalized)
        if segments:
            return segments

    storyboards = output.get("storyboards")
    if not isinstance(storyboards, list) or not storyboards:
        source_text = project.script_text or "未提供剧本文本"
        return [
            {
                "script_segment_code": f"{_safe_code_prefix(project.project_prefix or project.project_no or 'RF')}-EP01-J001",
                "episode_code": "EP01",
                "order_no": 1,
                "title": project.title,
                "source_text": source_text[:1200],
                "summary": source_text[:240],
                "story_function": "未明确",
                "dominant_emotion": "未明确",
                "rhythm": "未明确",
                "viewpoint": "未明确",
            }
        ]

    segments: list[dict[str, Any]] = []
    for index, storyboard in enumerate(storyboards, start=1):
        if not isinstance(storyboard, dict):
            continue
        episode_num = int(storyboard.get("episode_num") or 1)
        episode_code = f"EP{episode_num:02d}"
        order_no = int(storyboard.get("order_num") or index)
        code_prefix = _safe_code_prefix(project.project_prefix or project.project_no or "RF")
        description = str(storyboard.get("description") or storyboard.get("title") or "未提供原文依据")
        segments.append(
            {
                "script_segment_code": f"{code_prefix}-{episode_code}-J{order_no:03d}",
                "episode_code": episode_code,
                "order_no": order_no,
                "title": storyboard.get("title") or f"脚本段 {order_no:03d}",
                "source_text": description,
                "summary": description[:240],
                "story_function": "未明确",
                "dominant_emotion": "未明确",
                "rhythm": str(storyboard.get("camera") or "未明确")[:80],
                "viewpoint": "未明确",
                "source_context": {"before": "", "current": description, "after": ""},
            }
        )
    return segments


def _script_segment_extended_metadata(item: dict[str, Any]) -> dict[str, Any]:
    existing = item.get("metadata_json") if isinstance(item.get("metadata_json"), dict) else item.get("metadata")
    metadata = dict(existing) if isinstance(existing, dict) else {}

    def first_value(*keys: str) -> Any:
        for key in keys:
            if item.get(key) not in (None, ""):
                return item.get(key)
            if metadata.get(key) not in (None, ""):
                return metadata.get(key)
        return None

    def text_list(*keys: str) -> list[str]:
        value = first_value(*keys)
        if isinstance(value, list):
            return [str(entry).strip() for entry in value if str(entry).strip()]
        if isinstance(value, str) and value.strip():
            return [value.strip()]
        return []

    duration_value = first_value("estimated_duration_seconds", "duration_seconds", "duration")
    duration = _safe_int(duration_value, 0) if duration_value not in (None, "") else None
    return {
        "estimated_duration_seconds": duration,
        "role_refs": text_list("role_refs", "characters", "character_refs"),
        "scene_refs": text_list("scene_refs", "locations", "location_refs"),
        "prop_refs": text_list("prop_refs", "props", "prop_codes"),
        "production_notes": text_list("production_notes", "notes"),
        "dialogue_lines": text_list("dialogue_lines"),
        "context_code": first_value("context_code"),
        "render_mode": first_value("render_mode"),
    }


def _safe_int(value: Any, fallback: int) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return fallback


async def _ensure_storyboard_and_tasks_for_segment(
    session: AsyncSession,
    project: Project,
    segment: ScriptSegment,
    segment_data: dict[str, Any],
    user: User,
) -> None:
    episode_num = int(str(segment.episode_code or "EP01").replace("EP", "") or 1)
    order_num = int(segment_data.get("storyboard_order_num") or segment.order_no)
    existing_storyboard = await session.scalar(
        select(Storyboard).where(
            Storyboard.project_id == project.id,
            Storyboard.episode_num == episode_num,
            Storyboard.order_num == order_num,
        )
    )
    storyboard = existing_storyboard
    if storyboard is None:
        storyboard = Storyboard(
            project_id=project.id,
            script_segment_id=segment.id,
            episode_num=episode_num,
            order_num=order_num,
            title=segment.title or f"分镜 {order_num:03d}",
            description=segment.summary or segment.source_text,
            dialogue=str(segment_data.get("dialogue") or ""),
            camera=str(segment_data.get("camera") or segment.rhythm or "中景，稳定镜头，保留角色动作和场景关系"),
            duration_seconds=int(segment_data.get("duration_seconds") or 10),
            characters=list(segment_data.get("characters") or []),
            keyframes=list(segment_data.get("keyframes") or [{"order": 1, "description": segment.source_text[:120]}]),
            status=TaskStatus.todo,
            storyboard_code=f"{_safe_code_prefix(project.project_prefix or project.project_no or 'RF')}-{segment.episode_code or 'EP01'}-F{order_num:03d}",
            context_code=segment.context_code,
            render_mode=segment.render_mode,
        )
        session.add(storyboard)
        await session.flush()

    image_user_id = await _first_user_id_by_role(session, UserRole.artist)
    video_user_id = image_user_id
    now = datetime.now(UTC)
    specs = [
        (
            TaskType.text_to_image,
            f"{storyboard.title or f'分镜 {order_num:03d}'} 文生图",
            image_user_id,
            segment.summary or segment.source_text,
            "Seedream",
            1,
        ),
        (
            TaskType.image_to_video,
            f"{storyboard.title or f'分镜 {order_num:03d}'} 图生视频",
            video_user_id,
            storyboard.camera or segment.summary or segment.source_text,
            "可灵",
            2,
        ),
    ]
    for task_type, title, assignee_id, prompt_text, model_name, due_days in specs:
        exists = await session.scalar(
            select(Task.id).where(
                Task.project_id == project.id,
                Task.storyboard_id == storyboard.id,
                Task.task_type == task_type,
                Task.is_retired.is_(False),
            )
        )
        if exists:
            continue
        task = Task(
            project_id=project.id,
            script_segment_id=segment.id,
            storyboard_id=storyboard.id,
            task_type=task_type,
            title=title,
            assignee_id=assignee_id,
            assigned_by=user.id,
            assigned_at=now,
            status=TaskStatus.todo,
            prompt_text=prompt_text,
            latest_prompt_text=prompt_text,
            due_at=now + timedelta(days=due_days),
            production_model=model_name,
        )
        session.add(task)
        await session.flush()
        session.add(
            TaskPrompt(
                task_id=task.id,
                prompt_type="agent_initial",
                prompt_text=prompt_text,
                source="agent",
                version_no=1,
                created_by=user.id,
            )
        )


async def _first_user_id_by_role(session: AsyncSession, role: UserRole) -> UUID | None:
    return await session.scalar(select(User.id).where(User.role == role, User.is_active.is_(True)).order_by(User.created_at))

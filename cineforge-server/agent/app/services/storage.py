from __future__ import annotations

import mimetypes
import gzip
import hashlib
import io
import json
import re
import uuid
from dataclasses import dataclass
from datetime import UTC, datetime
from functools import lru_cache
from pathlib import Path
from typing import BinaryIO
from uuid import UUID

from minio import Minio
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from app.config import settings
from app.models.core import Asset, FileObject, Project, Storyboard, Task
from app.models.enums import TaskType


IMAGE_EXTENSIONS = {"jpg", "jpeg", "png"}
VIDEO_EXTENSIONS = {"mp4", "mov"}
AUDIO_EXTENSIONS = {"wav", "mp3"}
DOCUMENT_EXTENSIONS = {"docx", "pdf", "txt", "md"}


@dataclass(frozen=True)
class UploadedFileRecord:
    id: UUID
    file_code: str
    bucket: str
    object_key: str
    file_name: str
    mime_type: str | None
    file_size: int | None
    url: str


@dataclass(frozen=True)
class PayloadObjectRecord:
    ref: str
    sha256: str
    size_bytes: int


def allowed_extensions_for(file_type: str) -> set[str]:
    normalized = file_type.lower().strip()
    if normalized in {"image", "text_to_image", "storyboard_image"}:
        return IMAGE_EXTENSIONS
    if normalized in {"video", "image_to_video", "video_generation", "final_video"}:
        return VIDEO_EXTENSIONS
    if normalized in {"audio", "voice", "music", "theme_music", "background_music"}:
        return AUDIO_EXTENSIONS
    if normalized in {"document", "script", "reference"}:
        return DOCUMENT_EXTENSIONS
    if normalized == "media":
        return IMAGE_EXTENSIONS | VIDEO_EXTENSIONS
    return IMAGE_EXTENSIONS | VIDEO_EXTENSIONS | AUDIO_EXTENSIONS | DOCUMENT_EXTENSIONS


def storage_file_type_for_task(task_type: TaskType) -> str:
    if task_type == TaskType.audio:
        return "audio"
    if task_type in {TaskType.asset, TaskType.text_to_image}:
        return "image"
    if task_type == TaskType.storyboard_shot:
        return "media"
    if task_type in {TaskType.image_to_video, TaskType.video_generation, TaskType.assembly, TaskType.final_output}:
        return "video"
    return "reference"


async def upload_task_file(
    session: AsyncSession,
    *,
    task: Task,
    file: BinaryIO,
    original_filename: str,
    content_type: str | None,
    file_size: int | None,
    uploaded_by: UUID | None,
) -> UploadedFileRecord:
    file_type = storage_file_type_for_task(task.task_type)
    extension = _extension(original_filename)
    if extension not in allowed_extensions_for(file_type):
        raise ValueError(f"不支持的文件类型 .{extension}，当前任务允许：{', '.join(sorted(allowed_extensions_for(file_type)))}")

    project = await session.get(Project, task.project_id)
    if project is None:
        raise KeyError(task.project_id)
    storyboard = await session.get(Storyboard, task.storyboard_id) if task.storyboard_id else None
    asset = await session.get(Asset, task.asset_id) if task.asset_id else None
    version_no = await _next_file_version(session, task.id)
    object_key = _task_object_key(
        project=project,
        task=task,
        storyboard=storyboard,
        version_no=version_no,
        original_filename=original_filename,
        extension=extension,
        asset_code=asset.asset_code if asset else None,
    )
    bucket = settings.minio_bucket
    client = _client()
    _ensure_bucket(client, bucket)
    resolved_content_type = content_type or mimetypes.guess_type(original_filename)[0] or "application/octet-stream"
    try:
        client.put_object(
            bucket,
            object_key,
            file,
            length=file_size if file_size is not None else -1,
            part_size=10 * 1024 * 1024,
            content_type=resolved_content_type,
        )

        file_code = _file_code(project.project_prefix, task.task_type, version_no)
        file_object = FileObject(
            file_code=file_code,
            bucket=bucket,
            object_key=object_key,
            file_name=_safe_filename(original_filename),
            mime_type=resolved_content_type,
            file_size=file_size,
            uploaded_by=uploaded_by,
            project_id=task.project_id,
            episode_id=task.episode_id,
            task_id=task.id,
        )
        session.add(file_object)
        await session.commit()
    except Exception:
        await session.rollback()
        try:
            client.remove_object(bucket, object_key)
        except Exception:
            pass
        raise
    await session.refresh(file_object)
    return UploadedFileRecord(
        id=file_object.id,
        file_code=file_object.file_code or file_code,
        bucket=file_object.bucket,
        object_key=file_object.object_key,
        file_name=file_object.file_name,
        mime_type=file_object.mime_type,
        file_size=file_object.file_size,
        url=f"minio://{file_object.bucket}/{file_object.object_key}",
    )


async def upload_project_import_file(
    session: AsyncSession,
    *,
    project: Project,
    file: BinaryIO,
    original_filename: str,
    content_type: str | None,
    file_size: int | None,
    uploaded_by: UUID | None,
) -> UploadedFileRecord:
    extension = _extension(original_filename)
    if extension not in {"docx", "txt", "md"}:
        raise ValueError("项目导入只支持 .docx、.txt、.md 文件")

    bucket = settings.minio_bucket
    client = _client()
    _ensure_bucket(client, bucket)
    object_key = _project_import_object_key(project, original_filename)
    resolved_content_type = content_type or mimetypes.guess_type(original_filename)[0] or "application/octet-stream"
    client.put_object(
        bucket,
        object_key,
        file,
        length=file_size if file_size is not None else -1,
        part_size=10 * 1024 * 1024,
        content_type=resolved_content_type,
    )

    file_code = f"{_slug(project.project_prefix or project.project_no or 'RF', fallback='RF')[:16]}-SCRIPT-{uuid.uuid4().hex[:8].upper()}"
    file_object = FileObject(
        file_code=file_code,
        bucket=bucket,
        object_key=object_key,
        file_name=_safe_filename(original_filename),
        mime_type=resolved_content_type,
        file_size=file_size,
        uploaded_by=uploaded_by,
        project_id=project.id,
    )
    session.add(file_object)
    await session.flush()
    return UploadedFileRecord(
        id=file_object.id,
        file_code=file_object.file_code or file_code,
        bucket=file_object.bucket,
        object_key=file_object.object_key,
        file_name=file_object.file_name,
        mime_type=file_object.mime_type,
        file_size=file_object.file_size,
        url=f"minio://{file_object.bucket}/{file_object.object_key}",
    )


@lru_cache(maxsize=1)
def _client() -> Minio:
    return Minio(
        settings.minio_endpoint,
        access_key=settings.minio_access_key,
        secret_key=settings.minio_secret_key,
        secure=settings.minio_secure,
    )


def _ensure_bucket(client: Minio, bucket: str) -> None:
    if not client.bucket_exists(bucket):
        client.make_bucket(bucket)


def delete_stored_object(bucket: str, object_key: str) -> None:
    _client().remove_object(bucket, object_key)


def stored_object_size(bucket: str, object_key: str) -> int:
    return int(_client().stat_object(bucket, object_key).size)


def open_stored_object(
    bucket: str,
    object_key: str,
    *,
    offset: int = 0,
    length: int | None = None,
):
    if length is None:
        return _client().get_object(bucket, object_key, offset=offset)
    return _client().get_object(bucket, object_key, offset=offset, length=length)


def store_json_payload(payload: dict, *, namespace: str = "agent-payloads") -> PayloadObjectRecord:
    encoded = json.dumps(payload, ensure_ascii=False, sort_keys=True, default=str, separators=(",", ":")).encode("utf-8")
    digest = hashlib.sha256(encoded).hexdigest()
    object_key = f"{namespace}/{digest[:2]}/{digest}.json.gz"
    bucket = settings.minio_bucket
    client = _client()
    _ensure_bucket(client, bucket)
    try:
        client.stat_object(bucket, object_key)
    except Exception:
        compressed = gzip.compress(encoded, compresslevel=6, mtime=0)
        client.put_object(
            bucket,
            object_key,
            io.BytesIO(compressed),
            length=len(compressed),
            content_type="application/gzip",
            metadata={"x-amz-meta-sha256": digest, "x-amz-meta-uncompressed-size": str(len(encoded))},
        )
    return PayloadObjectRecord(ref=f"minio://{bucket}/{object_key}", sha256=digest, size_bytes=len(encoded))


def load_json_payload(ref: str) -> dict:
    bucket, object_key = _parse_minio_ref(ref)
    response = _client().get_object(bucket, object_key)
    try:
        compressed = response.read()
    finally:
        response.close()
        response.release_conn()
    payload = json.loads(gzip.decompress(compressed).decode("utf-8"))
    if not isinstance(payload, dict):
        raise ValueError("Stored payload must be a JSON object")
    return payload


def _parse_minio_ref(ref: str) -> tuple[str, str]:
    if not ref.startswith("minio://"):
        raise ValueError("Unsupported payload reference")
    bucket, separator, object_key = ref[8:].partition("/")
    if not separator or not bucket or not object_key:
        raise ValueError("Invalid MinIO payload reference")
    return bucket, object_key


async def _next_file_version(session: AsyncSession, task_id: UUID) -> int:
    prefix = f"%/TASK-{str(task_id)[:8]}/V%"
    count = await session.scalar(select(func.count(FileObject.id)).where(FileObject.object_key.like(prefix)))
    return int(count or 0) + 1


def _task_object_key(
    *,
    project: Project,
    task: Task,
    storyboard: Storyboard | None,
    version_no: int,
    original_filename: str,
    extension: str,
    asset_code: str | None = None,
) -> str:
    project_prefix = _slug(project.project_prefix or project.project_no or project.title, fallback="RF")[:16]
    episode_code = f"EP{storyboard.episode_num:02d}" if storyboard else "EP00"
    shot_code = f"S{storyboard.order_num:03d}" if storyboard else "S000"
    task_type = task.task_type.value.replace("_", "-")
    variant_code = None
    if getattr(task, "variant_plan_id", None) and asset_code and getattr(task, "task_variant", None):
        # Variant file names are business identifiers, independent of the
        # project prefix used by database asset codes: SC001-V001 / P001-V001.
        business_code = str(asset_code).strip().upper()
        project_code = _slug(project.project_prefix or project.project_no, fallback="")
        if project_code and business_code.startswith(f"{project_code}-"):
            business_code = business_code[len(project_code) + 1:]
        variant_code = f"{_slug(business_code, fallback='ASSET')}-{_slug(task.task_variant, fallback='V001')}"
    task_code = variant_code or f"TASK-{str(task.id)[:8]}"
    version_code = f"V{version_no:03d}"
    timestamp = datetime.now(UTC).strftime("%Y%m%d%H%M%S%f")
    upload_id = uuid.uuid4().hex[:12]
    stem = ("result" if variant_code else _safe_stem(original_filename)[:48]) or "upload"
    filename = f"{project_prefix}-{episode_code}-{shot_code}-{task_type}-{task_code}-{version_code}-{timestamp}-{upload_id}-{stem}.{extension}"
    folder = "asset-variants" if variant_code else task_type
    return "/".join([project_prefix, episode_code, folder, task_code, version_code, filename])


def _project_import_object_key(project: Project, original_filename: str) -> str:
    project_prefix = _slug(project.project_prefix or project.project_no or project.title, fallback="RF")[:16]
    timestamp = datetime.now(UTC).strftime("%Y%m%d%H%M%S")
    filename = f"{timestamp}-{_safe_filename(original_filename)}"
    return "/".join([project_prefix, "script", "imports", filename])


def _file_code(project_prefix: str | None, task_type: TaskType, version_no: int) -> str:
    prefix = _slug(project_prefix or "RF", fallback="RF")[:16]
    return f"{prefix}-{task_type.value.replace('_', '-').upper()}-{uuid.uuid4().hex[:8].upper()}-V{version_no:03d}"


def _extension(filename: str) -> str:
    suffix = Path(filename or "").suffix.lower().lstrip(".")
    return "bin" if not suffix else suffix


def _safe_filename(filename: str) -> str:
    name = Path(filename or "upload").name
    return re.sub(r"[^A-Za-z0-9._\-\u4e00-\u9fa5]+", "_", name).strip("._") or "upload"


def _safe_stem(filename: str) -> str:
    return Path(_safe_filename(filename)).stem


def _slug(value: str | None, *, fallback: str) -> str:
    text = re.sub(r"[^A-Za-z0-9]+", "", (value or "").upper())
    return text or fallback

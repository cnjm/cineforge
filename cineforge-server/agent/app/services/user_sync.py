"""CineForge user sync Application Service.

Single source of truth for building the user snapshot payload and enqueueing
the dual-target outbox events. Login sync and the integration worker must both
consume the same payload/version semantics defined here.
"""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Any, Sequence

from sqlalchemy.ext.asyncio import AsyncSession

from app.models.core import IntegrationSyncOutbox, User
from app.models.enums import UserRole

SYSTEM_CORE_TARGET = "system"
REACTFLOW_CORE_TARGET = "reactflow"
ALL_TARGETS: tuple[str, ...] = (SYSTEM_CORE_TARGET, REACTFLOW_CORE_TARGET)


def user_role_key(role: UserRole) -> str:
    value = getattr(role, "value", role)
    return str(value)


def build_user_snapshot_payload(user: User) -> dict[str, Any]:
    """Project the cine-forge user fact into the cross-service snapshot payload."""
    # Read from __dict__ to avoid triggering a lazy refresh right after flush;
    # external_updated_at is audit-only and never used for version ordering.
    updated_at = user.__dict__.get("updated_at")
    return {
        "external_user_id": str(user.id),
        "username": user.username,
        "display_name": user.display_name,
        "phone": user.phone,
        "email": None,
        "avatar": None,
        "role_key": user_role_key(user.role),
        "disabled": not user.is_active,
        "source_version": user.sync_version,
        "external_updated_at": updated_at.isoformat() if updated_at is not None else None,
    }


async def emit_user_sync_events(
    session: AsyncSession,
    user: User,
    targets: Sequence[str] = ALL_TARGETS,
) -> int:
    """Increment users.sync_version and write one outbox row per target.

    The caller owns the surrounding transaction: a later rollback or failed
    commit discards both the version bump and the outbox rows together.
    """
    if user.id is None:
        await session.flush()
    user.sync_version = (user.sync_version or 0) + 1
    await session.flush()
    version = user.sync_version
    payload = build_user_snapshot_payload(user)
    now = datetime.now(UTC)
    for target in targets:
        session.add(
            IntegrationSyncOutbox(
                target_domain=target,
                event_type="user.upsert",
                aggregate_type="user",
                aggregate_id=str(user.id),
                source_version=version,
                payload=payload,
                status="pending",
                attempt_count=0,
                created_at=now,
                updated_at=now,
            )
        )
    return version
"""Shared Redis clients and small coordination primitives.

Redis clients are scoped to the current event loop. This keeps connection pools
reusable in the API process while avoiding cross-loop futures in Celery jobs that
create a short-lived asyncio loop for each task.
"""

from __future__ import annotations

import asyncio
import json
import secrets
import weakref
from contextlib import asynccontextmanager
from typing import Any, AsyncIterator

from redis.asyncio import Redis

from app.config import settings


_clients: dict[int, tuple[weakref.ReferenceType[asyncio.AbstractEventLoop], Redis]] = {}
_LOCK_RELEASE_SCRIPT = """
if redis.call('get', KEYS[1]) == ARGV[1] then
    return redis.call('del', KEYS[1])
end
return 0
"""


def project_event_channel(project_id: str) -> str:
    return f"cineforge:project-events:v1:{project_id}"


def project_dashboard_cache_key(project_id: str) -> str:
    return f"cineforge:project-dashboard:v1:{project_id}"


def redis_client() -> Redis:
    """Return a pooled Redis client for the currently running event loop."""
    loop = asyncio.get_running_loop()
    loop_id = id(loop)
    for stale_id, (stale_loop_ref, _stale_client) in list(_clients.items()):
        stale_loop = stale_loop_ref()
        if stale_loop is None or stale_loop.is_closed():
            _clients.pop(stale_id, None)
    entry = _clients.get(loop_id)
    client = entry[1] if entry and entry[0]() is loop else None
    if client is None:
        client = Redis.from_url(
            settings.redis_url,
            decode_responses=True,
            max_connections=settings.redis_max_connections,
            socket_connect_timeout=settings.redis_socket_timeout_seconds,
            socket_timeout=settings.redis_socket_timeout_seconds,
        )
        _clients[loop_id] = (weakref.ref(loop), client)
    return client


async def close_stale_redis_clients() -> None:
    current_loop = asyncio.get_running_loop()
    stale_clients: list[Redis] = []
    for stale_id, (loop_ref, client) in list(_clients.items()):
        loop = loop_ref()
        if loop is None or loop.is_closed() or loop is not current_loop:
            stale_clients.append(client)
            _clients.pop(stale_id, None)
    for client in stale_clients:
        try:
            await client.aclose()
        except Exception:
            pass


async def close_redis_clients() -> None:
    clients = [client for _loop_ref, client in _clients.values()]
    _clients.clear()
    for client in clients:
        await client.aclose()


async def cache_json_get(key: str) -> dict[str, Any] | None:
    await close_stale_redis_clients()
    raw = await redis_client().get(key)
    if not raw:
        return None
    try:
        value = json.loads(raw)
    except (TypeError, json.JSONDecodeError):
        return None
    return value if isinstance(value, dict) else None


async def cache_json_set(key: str, value: dict[str, Any], ttl_seconds: int) -> bool:
    encoded = json.dumps(value, ensure_ascii=False, default=str).encode("utf-8")
    if len(encoded) > settings.runtime_policy_cache_max_payload_bytes:
        return False
    await close_stale_redis_clients()
    await redis_client().set(key, encoded.decode("utf-8"), ex=ttl_seconds)
    return True


async def get_project_status(project_id: str) -> dict[str, Any] | None:
    if not settings.project_status_cache_enabled:
        return None
    try:
        return await cache_json_get(f"cineforge:project-status:v1:{project_id}")
    except Exception:
        return None


async def set_project_status(project_id: str, value: dict[str, Any]) -> bool:
    if not settings.project_status_cache_enabled:
        return False
    try:
        return await cache_json_set(
            f"cineforge:project-status:v1:{project_id}",
            value,
            settings.project_status_cache_ttl_seconds,
        )
    except Exception:
        return False


async def invalidate_project_status(project_id: str) -> None:
    if not settings.project_status_cache_enabled:
        return
    try:
        await redis_client().delete(
            f"cineforge:project-status:v1:{project_id}",
            project_dashboard_cache_key(project_id),
        )
    except Exception:
        return


async def publish_project_event(project_id: str, resource: str) -> None:
    """Publish a small invalidation event; never put production payloads in Redis."""
    message = json.dumps(
        {"event": "invalidation", "project_id": project_id, "resource": resource},
        ensure_ascii=False,
    )
    try:
        await close_stale_redis_clients()
        await redis_client().publish(project_event_channel(project_id), message)
    except Exception:
        return


def schedule_project_event(project_id: str, resource: str) -> None:
    """Schedule event delivery without adding Redis latency to a DB commit."""
    try:
        task = asyncio.create_task(publish_project_event(project_id, resource))
    except RuntimeError:
        return
    task.add_done_callback(lambda completed: completed.exception() if not completed.cancelled() else None)


async def acquire_workflow_lock(lock_key: str, *, ttl_seconds: int | None = None) -> str | None:
    token = secrets.token_urlsafe(24)
    try:
        await close_stale_redis_clients()
        acquired = await redis_client().set(
            f"cineforge:workflow-lock:v1:{lock_key}",
            token,
            nx=True,
            ex=ttl_seconds or settings.workflow_lock_ttl_seconds,
        )
    except Exception:
        # Fail open when Redis is unavailable; DB state remains authoritative.
        return f"local:{token}"
    return token if acquired else None


async def release_workflow_lock(lock_key: str, token: str | None) -> None:
    if not token or token.startswith("local:"):
        return
    try:
        await redis_client().eval(
            _LOCK_RELEASE_SCRIPT,
            1,
            f"cineforge:workflow-lock:v1:{lock_key}",
            token,
        )
    except Exception:
        return


@asynccontextmanager
async def workflow_lock(lock_key: str, *, ttl_seconds: int | None = None) -> AsyncIterator[bool]:
    """Acquire a best-effort distributed workflow lock for one operation."""
    if not settings.workflow_lock_enabled:
        yield True
        return
    key = f"cineforge:workflow-lock:v1:{lock_key}"
    token = secrets.token_urlsafe(24)
    acquired = False
    try:
        acquired = bool(await redis_client().set(
            key,
            token,
            nx=True,
            ex=ttl_seconds or settings.workflow_lock_ttl_seconds,
        ))
    except Exception:
        # Redis is coordination infrastructure; a cache outage must not make
        # the production workflow unavailable.
        acquired = True
    try:
        yield acquired
    finally:
        if acquired:
            try:
                await redis_client().eval(_LOCK_RELEASE_SCRIPT, 1, key, token)
            except Exception:
                pass

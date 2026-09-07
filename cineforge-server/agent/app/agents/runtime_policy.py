from __future__ import annotations

import asyncio
import copy
import logging
import time
from contextlib import suppress
from collections.abc import Awaitable, Callable
from typing import Any, Protocol, TypeVar

from app.agents.hermes import hermes_client
from app.agents.skill_contracts import build_skill_runtime_metadata, get_skill_runtime_policy
from app.config import settings
from app.services.cache import cache_json_get, cache_json_set


T = TypeVar("T", bound=dict[str, Any])
logger = logging.getLogger(__name__)


class SkillCache(Protocol):
    async def get(self, key: str) -> dict[str, Any] | None:
        ...

    async def set(self, key: str, value: dict[str, Any], ttl_seconds: int) -> None:
        ...


class RuntimePolicyExhaustedError(RuntimeError):
    def __init__(self, skill: str, attempts: int, cause: Exception) -> None:
        super().__init__(f"{skill} transient execution failed after {attempts} attempts: {cause}")
        self.skill = skill
        self.attempts = attempts


class RedisSkillCache:
    async def get(self, key: str) -> dict[str, Any] | None:
        return await cache_json_get(key)

    async def set(self, key: str, value: dict[str, Any], ttl_seconds: int) -> None:
        await cache_json_set(key, value, ttl_seconds)


class RuntimePolicyExecutor:
    def __init__(
        self,
        *,
        cache: SkillCache | None = None,
        sleep: Callable[[float], Awaitable[None]] = asyncio.sleep,
    ) -> None:
        self.cache = cache or RedisSkillCache()
        self.sleep = sleep
        self._cache_disabled_until = 0.0

    async def execute(
        self,
        skill: str,
        payload: dict[str, Any],
        operation: Callable[[], Awaitable[T]],
        *,
        cache_payload: dict[str, Any] | None = None,
    ) -> T:
        runtime = build_skill_runtime_metadata(skill, cache_payload if cache_payload is not None else payload)
        cache_key = self._cache_key(skill, runtime)
        cache_enabled = settings.runtime_policy_cache_enabled and not bool(payload.get("force_rerun"))
        if cache_enabled:
            cached = await self._cache_get(cache_key)
            if cached is not None:
                logger.info("skill runtime cache hit skill=%s input_hash=%s", skill, runtime["input_hash"])
                output = copy.deepcopy(cached)
                output["runtime_policy_execution"] = self._execution_metadata(
                    runtime, cache_key=cache_key, cache_hit=True, attempt_count=0
                )
                return output  # type: ignore[return-value]
            logger.info("skill runtime cache miss skill=%s input_hash=%s", skill, runtime["input_hash"])

        attempts = self._attempts(skill)
        max_elapsed_seconds = self._max_elapsed_seconds(skill)
        deadline = time.monotonic() + max_elapsed_seconds
        for attempt in range(1, attempts + 1):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimePolicyExhaustedError(
                    skill,
                    attempt - 1,
                    TimeoutError(f"runtime policy exceeded {max_elapsed_seconds}s total budget"),
                )
            attempt_started = time.monotonic()
            try:
                operation_task = asyncio.ensure_future(operation())
                done, _ = await asyncio.wait({operation_task}, timeout=remaining)
                if not done:
                    operation_task.cancel()
                    with suppress(asyncio.CancelledError):
                        await operation_task
                    raise RuntimePolicyExhaustedError(
                        skill,
                        attempt,
                        TimeoutError(
                            f"runtime policy exceeded {max_elapsed_seconds}s total budget"
                        ),
                    )
                output = operation_task.result()
                output["runtime_policy_execution"] = self._execution_metadata(
                    runtime, cache_key=cache_key, cache_hit=False, attempt_count=attempt
                )
                if cache_enabled:
                    await self._cache_set(cache_key, output)
                return output
            except Exception as exc:
                if isinstance(exc, RuntimePolicyExhaustedError):
                    raise
                now = time.monotonic()
                budget_expired = now >= deadline or (
                    isinstance(exc, TimeoutError)
                    and now - attempt_started >= max(0.0, remaining - 0.001)
                )
                if budget_expired:
                    raise RuntimePolicyExhaustedError(
                        skill,
                        attempt,
                        TimeoutError(
                            f"runtime policy exceeded {max_elapsed_seconds}s total budget"
                        ),
                    ) from exc
                retryable = is_retryable_runtime_error(exc)
                if attempt >= attempts:
                    if retryable:
                        raise RuntimePolicyExhaustedError(skill, attempts, exc) from exc
                    raise
                if not retryable:
                    raise
                delay = settings.runtime_policy_retry_base_seconds * (2 ** (attempt - 1))
                if time.monotonic() + delay >= deadline:
                    raise RuntimePolicyExhaustedError(
                        skill,
                        attempt,
                        TimeoutError(
                            f"runtime policy exceeded {max_elapsed_seconds}s total budget"
                        ),
                    ) from exc
                await self.sleep(delay)
        raise RuntimeError(f"{skill} runtime policy executor reached an invalid terminal state")

    async def _cache_get(self, key: str) -> dict[str, Any] | None:
        if time.monotonic() < self._cache_disabled_until:
            return None
        try:
            return await self.cache.get(key)
        except Exception:
            self._cache_disabled_until = time.monotonic() + settings.runtime_policy_cache_circuit_seconds
            return None

    async def _cache_set(self, key: str, output: dict[str, Any]) -> None:
        if time.monotonic() < self._cache_disabled_until:
            return
        try:
            await self.cache.set(key, output, settings.runtime_policy_cache_ttl_seconds)
        except Exception:
            self._cache_disabled_until = time.monotonic() + settings.runtime_policy_cache_circuit_seconds
            return

    @staticmethod
    def _attempts(skill: str) -> int:
        if skill == "script-reading":
            return 1
        retry_policy = get_skill_runtime_policy(skill).retry_policy
        if retry_policy in {"backend_retry_allowed", "single_item_or_batch_retry"}:
            return 3
        if retry_policy == "manual_or_backend_retry":
            return 2
        return 1

    @staticmethod
    def _max_elapsed_seconds(skill: str) -> float:
        if skill == "script-reading":
            return settings.script_reading_max_elapsed_seconds
        return settings.runtime_policy_max_elapsed_seconds

    @staticmethod
    def _cache_key(skill: str, runtime: dict[str, Any]) -> str:
        model = hermes_client.model or "unknown"
        return (
            "cineforge:skill-cache:v1:"
            f"{skill}:{runtime['skill_version']}:{runtime['contract_hash']}:{model}:{runtime['input_hash']}"
        )

    @staticmethod
    def _execution_metadata(
        runtime: dict[str, Any],
        *,
        cache_key: str,
        cache_hit: bool,
        attempt_count: int,
    ) -> dict[str, Any]:
        return {
            "cache_policy": runtime["cache_policy"],
            "retry_policy": runtime["retry_policy"],
            "cache_key": cache_key,
            "cache_hit": cache_hit,
            "attempt_count": attempt_count,
        }


def is_retryable_runtime_error(exc: Exception) -> bool:
    if isinstance(exc, RuntimePolicyExhaustedError):
        return False
    if exc.__class__.__name__ == "AgentExecutionError":
        return False
    if isinstance(exc, (TimeoutError, ConnectionError)):
        return True
    if not isinstance(exc, RuntimeError):
        return False
    message = str(exc).lower()
    if "hermes is disabled" in message:
        return False
    if "hermes chat request failed" not in message:
        return False
    non_retryable_statuses = (" 400 ", " 401 ", " 403 ", " 404 ", " 422 ")
    return not any(status in f" {message} " for status in non_retryable_statuses)


runtime_policy_executor = RuntimePolicyExecutor()

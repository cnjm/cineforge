from __future__ import annotations

import asyncio
import time
from datetime import UTC, datetime
from uuid import UUID

from celery.exceptions import SoftTimeLimitExceeded

from app.agents.run_status import agent_output_error_message, agent_output_failed
from app.agents.runner_factory import get_runner
from app.agents.runtime_policy import RuntimePolicyExhaustedError, is_retryable_runtime_error
from app.agents.runners import AgentExecutionError
from app.db.base import async_session
from app.models.enums import AgentKind, AgentRunStatus
from app.services import repositories as repo
from app.worker import celery_app


@celery_app.task(
    bind=True,
    name="app.tasks.execute_agent_run",
)
def execute_agent_run(self, run_id: str) -> str:
    retry_count = int(self.request.retries or 0)
    try:
        return asyncio.run(_execute_agent_run(UUID(run_id), retry_count=retry_count))
    except RetryableAgentTaskError as exc:
        raise self.retry(exc=exc, countdown=min(30, 2 ** retry_count), max_retries=2)


class RetryableAgentTaskError(RuntimeError):
    pass


async def _execute_agent_run(run_id: UUID, *, retry_count: int) -> str:
    async with async_session() as session:
        run = await repo.get_agent_run(session, run_id)
        if run.status == AgentRunStatus.succeeded:
            if _is_asset_prompt_generation_run(run):
                await _finalize_asset_prompt_generation_run(session, run)
            elif run.agent_type in {
                AgentKind.character_design_prompt,
                AgentKind.scene_design_prompt,
                AgentKind.prop_design_prompt,
            }:
                await _materialize_asset_prompt_run(session, run, retry_count=retry_count)
            elif run.agent_type in {AgentKind.text_to_image_prompt, AgentKind.image_to_video_prompt}:
                await _materialize_task_prompt_run(session, run, retry_count=retry_count)
            return str(run.id)
        started = time.perf_counter()
        retry_error: RetryableAgentTaskError | None = None
        try:
            execution_input = {**(run.input or {}), "_runtime_retry_count": retry_count}
            output = await get_runner(run.agent_type).run(execution_input)
            output_failed = agent_output_failed(output)
            updated = run.model_copy(
                update={
                    "status": AgentRunStatus.failed if output_failed else AgentRunStatus.succeeded,
                    "output": output,
                    "error_message": agent_output_error_message(output),
                    "duration_ms": int((time.perf_counter() - started) * 1000),
                    "token_usage": {
                        "provider": output.get("llm_provider") or output.get("workflow", {}).get("backend") or "local",
                        "input_chars": len(str(run.input)),
                        "worker": "celery",
                        "retry_count": retry_count,
                    },
                    "updated_at": datetime.now(UTC),
                }
            )
        except SoftTimeLimitExceeded:
            updated = run.model_copy(
                update={
                    "status": AgentRunStatus.failed,
                    "error_message": "Agent 执行超过 30 分钟超时限制。",
                    "duration_ms": int((time.perf_counter() - started) * 1000),
                    "token_usage": {"worker": "celery", "retry_count": retry_count, "will_retry": False},
                    "updated_at": datetime.now(UTC),
                }
            )
        except AgentExecutionError as exc:
            output = {
                **dict(exc.output or {}),
                "errors": list((exc.output or {}).get("errors") or [{"code": "agent_execution_error", "message": str(exc)}]),
            }
            updated = run.model_copy(
                update={
                    "status": AgentRunStatus.failed,
                    "output": output,
                    "error_message": str(exc),
                    "duration_ms": int((time.perf_counter() - started) * 1000),
                    "token_usage": {"worker": "celery", "retry_count": retry_count, "will_retry": False},
                    "updated_at": datetime.now(UTC),
                }
            )
        except Exception as exc:
            will_retry = retry_count < 2 and _is_retryable_task_error(exc)
            updated = run.model_copy(
                update={
                    "status": AgentRunStatus.running if will_retry else AgentRunStatus.failed,
                    "error_message": str(exc),
                    "duration_ms": int((time.perf_counter() - started) * 1000),
                    "token_usage": {"worker": "celery", "retry_count": retry_count, "will_retry": will_retry},
                    "updated_at": datetime.now(UTC),
                }
            )
            if will_retry:
                retry_error = RetryableAgentTaskError(str(exc))

        saved = await repo.save_agent_run(session, updated)
        await repo.update_workflow_run_from_agent_run(session, saved)
        if saved.status == AgentRunStatus.succeeded and _is_asset_prompt_generation_run(saved):
            saved = await _finalize_asset_prompt_generation_run(session, saved)
            await repo.update_workflow_run_from_agent_run(session, saved)
        if saved.status == AgentRunStatus.succeeded and saved.agent_type in {
            AgentKind.text_to_image_prompt,
            AgentKind.image_to_video_prompt,
        }:
            await _materialize_task_prompt_run(session, saved, retry_count=retry_count)
        if saved.status == AgentRunStatus.succeeded and saved.agent_type in {
            AgentKind.character_design_prompt,
            AgentKind.scene_design_prompt,
            AgentKind.prop_design_prompt,
        }:
            await _materialize_asset_prompt_run(session, saved, retry_count=retry_count)
        if retry_error is not None:
            raise retry_error
        return str(saved.id)


async def _materialize_task_prompt_run(session, run, *, retry_count: int = 0):
    try:
        metadata = await repo.materialize_task_prompt_agent_run(session, run)
        output = dict(run.output or {})
        output["task_prompt_materialization"] = metadata
        updated = run.model_copy(update={"output": output, "updated_at": datetime.now(UTC)})
        return await repo.save_agent_run(session, updated)
    except Exception as exc:
        await session.rollback()
        retryable = _is_retryable_task_error(exc)
        if retryable and retry_count < 2:
            raise RetryableAgentTaskError(str(exc)) from exc
        metadata = {
            "applied": False,
            "context_current": False,
            "reason": "materialization_retries_exhausted" if retryable else "materialization_failed",
            "error": str(exc),
        }
        output = dict(run.output or {})
        output["task_prompt_materialization"] = metadata
        failed = run.model_copy(update={
            "status": AgentRunStatus.failed,
            "output": output,
            "error_message": f"Prompt 物化失败：{exc}",
            "updated_at": datetime.now(UTC),
        })
        return await repo.save_agent_run(session, failed)


def _is_asset_prompt_generation_run(run) -> bool:
    return (
        run.agent_type == AgentKind.script_breakdown
        and isinstance(run.input, dict)
        and run.input.get("single_step") == "asset_prompt_generation"
    )


async def _finalize_asset_prompt_generation_run(session, run):
    output = dict(run.output or {})
    existing = output.get("asset_prompt_finalization")
    if isinstance(existing, dict) and existing.get("status") in {"succeeded", "distribution_failed"}:
        return run
    try:
        user = await repo.user_for_agent_run_materialization(session, run)
        if user is None:
            raise ValueError("找不到资产提示词任务的创建人。")
        result = await repo.finalize_asset_prompt_agent_run(session, run, user)
        output["asset_prompt_finalization"] = result
        return await repo.save_agent_run(
            session,
            run.model_copy(update={"output": output, "updated_at": datetime.now(UTC)}),
        )
    except Exception as exc:
        await session.rollback()
        output["asset_prompt_finalization"] = {
            "status": "failed",
            "prompt_status": "failed",
            "error": str(exc),
        }
        return await repo.save_agent_run(
            session,
            run.model_copy(update={
                "status": AgentRunStatus.failed,
                "output": output,
                "error_message": f"资产提示词已生成，但批量任务分发失败：{exc}",
                "updated_at": datetime.now(UTC),
            }),
        )


async def _materialize_asset_prompt_run(session, run, *, retry_count: int = 0):
    try:
        metadata = await repo.materialize_asset_prompt_agent_run(session, run)
        output = dict(run.output or {})
        output["asset_prompt_materialization"] = metadata
        updated = run.model_copy(update={"output": output, "updated_at": datetime.now(UTC)})
        return await repo.save_agent_run(session, updated)
    except Exception as exc:
        await session.rollback()
        retryable = _is_retryable_task_error(exc)
        if retryable and retry_count < 2:
            raise RetryableAgentTaskError(str(exc)) from exc
        metadata = {
            "applied": False,
            "context_current": False,
            "reason": "materialization_retries_exhausted" if retryable else "materialization_failed",
            "error": str(exc),
        }
        output = dict(run.output or {})
        output["asset_prompt_materialization"] = metadata
        failed = run.model_copy(update={
            "status": AgentRunStatus.failed,
            "output": output,
            "error_message": f"资产 Prompt 物化失败：{exc}",
            "updated_at": datetime.now(UTC),
        })
        return await repo.save_agent_run(session, failed)


def _is_retryable_task_error(exc: Exception) -> bool:
    if isinstance(exc, RuntimePolicyExhaustedError):
        return False
    if is_retryable_runtime_error(exc):
        return True
    module = exc.__class__.__module__
    retryable = exc.__class__.__name__ in {
        "ConnectionTimeout",
        "InterfaceError",
        "OperationalError",
        "PoolTimeout",
        "TimeoutError",
    }
    if retryable and (module.startswith("psycopg") or module.startswith("sqlalchemy")):
        return True
    cause = exc.__cause__ or exc.__context__
    return isinstance(cause, Exception) and cause is not exc and _is_retryable_task_error(cause)

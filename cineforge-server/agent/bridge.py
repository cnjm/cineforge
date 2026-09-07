"""CineForge agent 子模块 HTTP bridge。

Go 单体（cineforge-server）创建 agent_run/workflow_run 落库后，调用本服务
把 run 投递给 Celery（task_id=run_id 幂等，配合 Celery result backend 去重）。

端点：
  - GET  /health                     → 存活 + hermes 启用态
  - POST /api/runs/{run_id}/enqueue  → 投递 execute_agent_run（202；终态 409；broker 不可用 503）

错误体统一 `{"detail": "..."}`（与 FastAPI 默认一致）。
"""

from __future__ import annotations

import uuid

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from sqlalchemy import select
from starlette.status import HTTP_202_ACCEPTED, HTTP_409_CONFLICT, HTTP_503_SERVICE_UNAVAILABLE

from app.config import settings
from app.db.base import async_session
from app.models.core import AgentRun
from app.models.enums import AgentRunStatus

app = FastAPI(title="cineforge-agent", version="0.1.0")


@app.get("/health")
async def health() -> dict[str, object]:
    return {
        "status": "ok",
        "hermes_enabled": settings.hermes_enabled,
        "hermes_model": settings.hermes_model,
    }


class EnqueueRequest(BaseModel):
    """预留无需参数；body 可空。"""

    pass


@app.post("/api/runs/{run_id}/enqueue", status_code=HTTP_202_ACCEPTED)
async def enqueue_run(run_id: uuid.UUID) -> dict[str, object]:
    from app.tasks import execute_agent_run

    async with async_session() as session:
        try:
            run = (
                await session.execute(select(AgentRun).where(AgentRun.id == run_id))
            ).scalar_one_or_none()
        except Exception as exc:  # DB 不可达
            raise HTTPException(
                status_code=HTTP_503_SERVICE_UNAVAILABLE, detail=f"Agent 数据库不可用：{exc}"
            ) from exc
        if run is None:
            raise HTTPException(status_code=HTTP_409_CONFLICT, detail="agent_run 不存在")
        if run.status in {AgentRunStatus.succeeded, AgentRunStatus.failed}:
            raise HTTPException(status_code=HTTP_409_CONFLICT, detail="agent_run 已处于终态，不可重复入队")

    # task_id = run_id：Celery 同 task_id 只投递一次（result 存活期内），对齐 legacy
    # orchestrator.submit_background 的幂等入队语义。
    try:
        execute_agent_run.apply_async(args=[str(run_id)], task_id=str(run_id))
    except Exception as exc:  # broker 不可达等
        raise HTTPException(status_code=HTTP_503_SERVICE_UNAVAILABLE, detail=f"Agent 队列入队失败：{exc}") from exc

    return {"id": str(run_id), "status": "queued"}


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8091)
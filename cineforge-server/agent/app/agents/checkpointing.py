from __future__ import annotations

from contextlib import asynccontextmanager
from typing import Any, AsyncIterator

from app.config import settings


def checkpoint_database_url() -> str:
    return settings.database_url.replace("postgresql+asyncpg://", "postgresql://", 1)


@asynccontextmanager
async def open_workflow_checkpointer() -> AsyncIterator[Any | None]:
    if not settings.langgraph_checkpoint_enabled:
        yield None
        return

    from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver

    async with AsyncPostgresSaver.from_conn_string(checkpoint_database_url()) as checkpointer:
        await checkpointer.setup()
        yield checkpointer

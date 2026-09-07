"""CineForge agent 子模块数据库会话。

对齐 legacy `backend/app/db/base.py`：NullPool 不回事件循环复用连接。
schema 完全由 cineforge-server goose 迁移管理（migrations/00001_initial…），
本模块**不做** create_all / ALTER（停用 legacy init_db 自建表入口）。
"""

from collections.abc import AsyncGenerator

from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine
from sqlalchemy.orm import DeclarativeBase
from sqlalchemy.pool import NullPool

from app.config import settings


class Base(DeclarativeBase):
    pass


# NullPool：不跨事件循环复用连接。Celery worker 每个任务用 asyncio.run() 新建事件循环，
# 连接池复用会导致 "Future attached to a different loop" 错误使任务卡在 running。
# 每次操作开新连接、用完即关，保证在当前事件循环内创建，彻底规避该问题。
engine = create_async_engine(settings.database_url, pool_pre_ping=True, poolclass=NullPool)
async_session = async_sessionmaker(engine, expire_on_commit=False)


async def get_session() -> AsyncGenerator[AsyncSession, None]:
    async with async_session() as session:
        yield session
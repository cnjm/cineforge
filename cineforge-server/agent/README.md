# agent/ — Python Agent 执行子模块（cineforge-agent）

> **P4b 完成**。本目录是 `cineforge-server` 的 Python 3.11 子模块，按既定执行契约实现
> Agent 执行闭包，指向同一 `cineforge` 数据库。
> 「剧本围读 → 资产拆解 → 提示词生成 → 脚本段拆分 → 分镜拆解」通过 Python 执行。

## 边界

- Go 单体负责：HTTP 边界、SSE、确定性后台（权限/版本/任务创建/审核状态）、agent_run/workflow_run 落库。
- Python 子模块负责：**执行 agent_run**（LangGraph 编排 → Hermes → 归一化 → 落库/物化）。
- Go 经 `bridge.py` `POST /api/runs/{run_id}/enqueue` 把已落库的 run 投递给 Celery，
  `task_id=run_id` 幂等（对齐既定契约 submit_background 语义）。
- **schema 属于 goose**（`migrations/00001_initial_single_db_schema.sql`）。本模块 `db/base.py`
  不做 create_all / ALTER（不使用 `init_db`，由 CINEFORGE_* env 直接映射配置）。

## 布局

```
agent/
  app/                    # 按既定执行契约实现，保留 from app.xxx 导入不变
    config.py             # CINEFORGE_* env → 字段映射（见 _CINEFORGE_ENV_MAP）
    db/base.py            # 改写：cineforge DSN（asyncpg），NullPool，无 init_db
    models/ schemas/      # 按既定契约落地
    agents/               # hermes / runners / workflow / registry / skill_contracts
                          #   / runtime_policy / breakdown_workflow / production_context …
    services/repositories.py cache.py storage.py asset_normalization.py
      asset_matching.py relation_validation.py user_sync.py
    security.py           # 按既定契约落地（hash_password/verify_password，依赖 fastapi）
    tasks.py worker.py    # Celery：execute_agent_run（materialize 钩子在 exec 后）
  bridge.py               # FastAPI :8091：GET /health + POST /api/runs/{run_id}/enqueue
  requirements.txt        # 按需裁剪：无 alembic/grpcio/protobuf/PyJWT/python-multipart
  .venv/                  # uv 管理的 python 3.10 虚拟环境
```

## 运行

```bash
cd agent
uv venv .venv --python 3.10
uv pip install -r requirements.txt

# bridge（Go enqueue 入口）
.venv/bin/python -m uvicorn bridge:app --host 0.0.0.0 --port 8091

# Celery worker
.venv/bin/celery -A app.worker:celery_app worker --loglevel=info -Q cineforge
```

需要可达的依赖底座：PostgreSQL（`CINEFORGE_DB_DSN`）、Redis（`CINEFORGE_REDIS_URL`，
Celery broker/backend）、MinIO（`CINEFORGE_MINIO_*`）、Hermes（`CINEFORGE_HERMES_*`）。
env 注入通道与 Go 单体同源 `CINEFORGE_*`（`CINEFORGE_DB_DSN` 的 `postgres://` 自动换算为
`postgresql+asyncpg://`）。

## 执行契约（run 生命周期）

```
Go: POST /api/projects/{pid}/breakdown-steps/{step}
  → 闸口校验 → workflow_runs(running) + agent_runs(queued) 落库 → :8091 enqueue
Python: Celery execute_agent_run(run_id)
  → get_runner(agent_type).run(input_json) → 归一化/物化 → agent_runs(succeeded|failed) + workflow_runs 终态
Go SSE: /api/projects/{pid}/run-events 轮询 DB 快照 → 前端（snapshot 形状对齐既定契约 streaming 语义）
```

## 状态

- P4b：Python 子模块落地完成（config/db 改写 + 闭包按既定执行契约实现 + bridge + venv）。
- 下一步：P4c Go agent-client + 五步 submit 接入 enqueue；P4d SSE；P4e 其余端点；P4f 契约测试与文档。
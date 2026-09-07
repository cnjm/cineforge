# cineforge-server（后端）

> CineForge 后端：**Go 单体单服务（gin）** + **Python `agent/` 子模块**（五步拆解 Agent 编排 + Hermes）。
> 单库 `cineforge`；goose 迁移；云效 CICD 编译产物直接部署 ECS。

## 状态

- **P0～P2 完成**：仓库骨架、config 双通道（config.yaml + CINEFORGE_* env）、gin 空壳（/health /ready）、goose migrate CLI（`migrations/00001_initial_single_db_schema.sql` 46 业务表）、dev compose 底座、.env.example 全部就位。迁移已端到端验证：空库 up 到 head、down 干净回滚、`/ready` 200（配好 `CINEFORGE_DB_DSN` 时）。
- **P3c 完成（auth 域）**：PBKDF2 密码（Python 互操作）、三 token（access HMAC / tapcanvas HS256 / platform RS256，共享 8h 会话）、Casbin RBAC（migration 00002 种子）、登录/me/改密/用户管理接口与契约测试全绿。`go build / vet / test ./...` 通过。契约见 `docs/后端/auth契约.md`。
- **P3d 完成（项目域）**：项目/分集/剧本版本 CRUD、剧本导入（含 Go 原生 docx 解析）、五步拆解 submit（闸口+幂等+queued 落库，执行留 P4）、拆解草稿保存/查询（乐观锁+人工确认 revision）、Dashboard 聚合、production-brief catalog、agent-runs。契约测试全绿。契约见 `docs/后端/项目域契约.md`。
- **P3e 完成（任务域）**：任务列表/详情/更新、提示词、候选登记/编辑/软删/归档/主母版、submit/review/bulk（uuid5 request_id 幂等）、workspace 工作台、分组路由（storyboards / board / scene-gating / storyboard-video-tasks / bulk-assign）。权限模型对齐既定契约 `_can_access_project`（含「可见任务」回退）；改派/定版通知落库。契约测试全绿。契约见 `docs/后端/任务域契约.md`。
- **P3f 完成（文件 / 资产 / 任务生成器 / 分集删除清理）**：真实 MinIO（minio-go）归档 + 服务端流式文件代理（Range/206/416 逐字节对齐、HMAC 文件 token 字节兼容、**消除 import 503**）；资产域 CRUD/library/variant-plans/temporary-production/scene-options/资产绑定/AssetVersion 物化；`POST asset-tasks` 与 `POST storyboard-tasks`（行锁幂等 + 守卫逐字）；分集删除 `delete_files` 门控 + MinIO 对象清理 + 操作日志终局。契约测试全绿。契约见 `docs/后端/文件与资产域契约.md`。
- **P3g 完成（admin 看板 / 通知读端 / Hermes 静态端点）**：`/workspace/notifications` 列表+read-all（写路径沿用 P3e 任务事务内通知）；`/admin/overview`（stats 短键+extended 别名、overdue_tasks Top5、recent_readings）与 `/admin/probe`（27 表 counts + recent 三元组）；`/agents/hermes/config|health` 静态配置派生（P4 接真实探活）。契约测试全绿。契约见 `docs/后端/admin与通知域契约.md`。
- **P4 完成（agent 接入）**：Python `agent/` 子模块（bridge.py + Celery `execute_agent_run`，`task_id=run_id` 幂等）；Go `internal/agentclient` 投递已落库运行（`saveRunningRun`，入队失败 run 落 failed 仍按 202/200）；SSE run-events（快照对齐 `_project_run_snapshot`，Redis 不可用 503 兜底）；agents definitions/stage-map/`/agent-jobs`/feedback/training-samples/materialize/retry-distribution/query-agent；`/agents/hermes/health` **真实探活**（health→/models 探测链，timeout=min(hermes_timeout,8s)）；**task prompt-jobs**（`POST /tasks/{id}/prompt-jobs`，守卫/输入契约/指纹/production context/op-log 全对齐既定契约）。契约测试全绿。契约见 `docs/后端/agent域契约.md` 与 `docs/后端/任务域契约.md`。
- **待办链**：P5 画布并入 → P6 CICD。

## 快速开始

```bash
# 1. 起本地底座（PostgreSQL16+pgvector / Redis7 / MinIO）
docker compose up -d
cp .env.example .env   # 填 CINEFORGE_DB_PASSWORD 等私有值

# 2. 迁移（P2b 后有效）
export CINEFORGE_DB_DSN='postgres://cineforge:CHANGE_ME@127.0.0.1:5432/cineforge'
go run ./cmd/migrate -action up      # 迁移到 head
go run ./cmd/migrate -action status

# 3. 起服务（/ready 变 200）
go run ./cmd/server      # 配置了 CINEFORGE_DEVSEED_* 时，启动自动创建开发账号（见 docs/开发账号.md）

# 4. 验证
curl localhost:8080/health
curl localhost:8080/ready
```

## 进程拓扑（目标）

```
cineforge-server（ECS）
├─ Go 单体 binary    HTTP :8080 / gRPC :8090
│   internal/{identity,project,task,asset,notify,dashboard,agent-client,canvas}
└─ agent/（Python）  五步拆解编排 + Hermes 调用
```

## 快速开始

```bash
go build ./...          # 编译（P2 起有效）
go run ./cmd/server     # 起服务，/ready 健康检查（P2）
go run ./cmd/migrate deploy  # goose 迁移到 head（P2）
```

## 边界

- 数据库 schema 只能 goose 迁移；禁止启动建表 / 手工 DDL。
- 域名 / 库名 / bucket / 密钥均为配置项（config.yaml + env 双通道，见 `.env.example`）。
- 详细约束：先读 [CLAUDE.md](CLAUDE.md) 与 [AGENTS.md](AGENTS.md)。
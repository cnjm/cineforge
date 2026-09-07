# Admin 看板 / 通知 / Agents 静态端点契约（P3g 规范化记录）

> 以既定契约为基线（FastAPI 参考实现）：
> - `/admin/overview`、`/admin/probe`（users_router 的用户管理在 P3c 已落地；本阶段补齐 admin 看板端点）
> - `internal/api/task_handlers.go`：`get_workspace_notifications` / `read_all_workspace_notifications`
> - `internal/api/agents_handlers.go`：`GET /agents/hermes/config`、`GET /agents/hermes/health`
>
> 实现见 `cineforge-server/internal/repository/{notifications,admin}.go`、
> `internal/service/{notify,admin}.go`、`internal/api/{task,admin,agents}_handlers.go`。
> 契约测试：`internal/api/admin_contract_test.go`（5 个用例，全部通过）。
> 前置依赖：P3c users（`/users` CRUD）、P3e 改派/定版通知落库（写路径）。

## P3g 决策（与用户确认）

- **范围 A**：通知读端 + admin 看板（overview/probe）+ Hermes 静态端点。
- `/agents/hermes/health` **静态配置派生**：P4 接入真实 Hermes 探活前，`enabled=true → status=unreachable + error`，
  `enabled=false → status=disabled`，不做网络探活。
- `stats.agentRuns` 用 DB 等价量：`agent_runs` 表 `status IN ('queued','running')` 行数
  （参考实现取内存 `list_runs()` 在途数，现以 DB 行数为准）。
- 前端未使用的参考实现端点（`/admin/probe` 前端无调用，按既定契约完整实现）；
  agents 定义端点（`/agents`、`/agents/stage-map`、`/agents/{type}`）、agent-jobs、
  feedback、training-samples、`/projects/{id}/agents/*/jobs` 属 **P4 agent 接入**。

## 通知读端（workspace 路由）

路由前缀 `/api/workspace`，requireAuth（本人作用域，无跨用户访问）。

### GET /workspace/notifications

- `list_user_notifications`：本人最近 100 条，`created_at DESC`。
- 响应 `NotificationRead[]`：`{id, title, content, type, is_read, created_at}`。

### POST /workspace/notifications/read-all

- `mark_user_notifications_read`：本人全部未读置已读（`updated_at=now()`），返回 `{"updated": N}`（N=本次更新行数）。
- 空运行安全：无未读返回 `{"updated": 0}`。

写路径（改派/审核定版通知）仍在任务事务内（P3e `insertNotification`），读端只读不写。

## Admin 看板（`/api/admin` 路由）

路由前缀 `/api/admin`，requireDirectorOrAdmin（逐字 403 `Director or admin permission required`）。

### GET /admin/overview

```json
{
  "stats": {
    "users": 5, "projects": 3, "tasks": 42, "overdue": 2, "assets": 12,
    "submissions": 30, "agentRuns": 1,
    "user_count": 5, "active_project_count": 3, "total_task_count": 42,
    "overdue_task_count": 2, "asset_count": 12, "submission_count": 30, "agent_run_count": 1
  },
  "overdue_tasks": [ "TaskRead（56 字段，同 /tasks 列表）" ],
  "recent_activities": ["管理概览已接入 PostgreSQL", "任务、资产、提交统计来自真实数据库"],
  "recent_readings": [ { "id","name","meta","projectId","title","genre","episodeCode","status","created_at" } ]
}
```

- **stats 短键**（既定实现原始键）：`users/projects/tasks/overdue/assets/submissions/agentRuns`；
  **extended 键**（前端 AdminOverview 别名字段）：`*_count` 与短键同值。
- 计数口径：
  - `tasks`：`is_retired=false` 行数。
  - `overdue`：`is_retired=false AND status<>'completed' AND due_at IS NOT NULL AND due_at < now()`。
  - `agentRuns`：`agent_runs WHERE status IN ('queued','running')`。
- `overdue_tasks`：同口径 Top5（`ORDER BY due_at ASC`），渲染走任务域 `TaskView` 全字段（assignee/submissions/batches 预载）。
- `recent_readings`：`agent_runs WHERE agent_type='script_reading' AND project_id IS NOT NULL` 最近 3 条，
  `name = "剧本围读" + ("_"+episode_code|skill_name)`，`meta = "{秒}s · {状态}"`，状态文案逐字：
  succeeded→`围读完成`、running→`运行中`、queued→`排队中`、其他→`围读失败`。

### GET /admin/probe

```json
{
  "status": "ok",
  "generated_at": "<iso8601>",
  "counts": { "users": 5, "projects": 3, "operation_logs": 88, "<27 张表>": "<行数>" },
  "recent_projects": [ { "id","project_prefix","title","status","created_at" } ],
  "recent_agent_runs": [ { "id","project_id","agent_type","status","error_message","created_at" } ],
  "recent_operation_logs": [ { "id","project_id","action","target_type","created_at" } ]
}
```

- `counts` 覆盖 27 张业务表行数（表名白名单，硬编码；含 `users→operation_logs`）。
- `recent_projects` Top5 / `recent_agent_runs` Top8 / `recent_operation_logs` Top8（按 `created_at DESC`）。

## Agents 静态端点（`/api/agents` 路由）

路由前缀 `/api/agents`。**无鉴权**（对齐既定契约的无鉴权设计；前端也在登录上下文内调用）。

### GET /agents/hermes/config

```json
{
  "enabled": true, "base_url": "http://localhost:8642/v1", "health_url": ".../health",
  "model": "hermes-agent", "dashboard_url": "...:9119", "webui_url": "...:3000",
  "api_key_configured": false
}
```

- `api_key_configured = api_key != "" && api_key != "change-me-hermes-local"`。
- `dashboard_url/webui_url` 为空时按 `cfg.Domains.Server` 填充 `:9119` / `:3000`（对齐既定契约 `_fill_hermes_urls`）。

### GET /agents/hermes/health（静态配置派生）

- `enabled=false` → `status=disabled`（与既定实现无网络分支完全一致）。
- `enabled=true` → `status=unreachable` + `error:"Hermes 探活未接入（P4 提供真实连通性检查）"`（P4 接真实探活）。
- 其余字段与 config 相同。

## config 双通道扩展

| 配置 | env | 默认 |
|---|---|---|
| `hermes.enabled` | `CINEFORGE_HERMES_ENABLED` | true |
| `hermes.addr` | `CINEFORGE_HERMES_ADDR` | `http://localhost:8642/v1` |
| `hermes.health_url` | `CINEFORGE_HERMES_HEALTH_URL` | `http://localhost:8642/health` |
| `hermes.model` | `CINEFORGE_HERMES_MODEL` | `hermes-agent` |
| `hermes.dashboard_url` | `CINEFORGE_HERMES_DASHBOARD_URL` | 空（handler 填充） |
| `hermes.webui_url` | `CINEFORGE_HERMES_WEBUI_URL` | 空（handler 填充） |
| `hermes.api_key` | `CINEFORGE_HERMES_API_KEY` | `change-me-hermes-local` |

## 契约测试

`internal/api/admin_contract_test.go`：

| 用例 | 覆盖 |
|---|---|
| `TestWorkspaceNotificationsReadMarkAll` | 本人通知列表（未读/已读混合）、401、read-all `updated=1`、再读全已读 |
| `TestAdminOverviewShapeAnd403` | stats 短键+extended 键一致、recent_activities 两条逐字、director 可读、artist 403 逐字 |
| `TestAdminOverviewOverdueAndReadings` | 逾期任务入 overview（Top5）、reading 的 name/meta/status/episodeCode/projectId/genre/created_at |
| `TestAdminProbe` | `status=ok`、27 表 counts、recent_* 三元组、artist 403 |
| `TestHermesStaticEndpoints` | config 默认 enabled+占位 key、health unreachable+error、disabled 分支（重建 router） |

## 后续（P4 / P4 后）

- `/admin/overview` 前端 TODO 的 `monthly_completed_episodes / pending_items_count / team_creator_count / pending_items`：参考实现当前未返回，前端类型为可选，保持现状。
- agents 定义 / stage-map / agent-jobs / feedback / training-samples / materialize：随 P4 agent 接入。
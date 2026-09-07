# CineForge · AI 实现过程

> 这套系统不是一次性成型，而是**分阶段、以契约测试为验收门，一步步通过 AI 辅助开发实现的**。
> 本文按时间线还原每个阶段的：目标 → 关键决策 → 落地内容 → 验证结果，作为实现过程的事实记录。
> 想看当前进度请读 [进度.md](进度.md)、看技术决策请读 [开发总方案.md](开发总方案.md)。

## 工作方式（贯穿始终）

1. **契约先行**：每个业务域先定接口契约（路由、状态码、错误文案逐字），契约测试全绿才进入下一域。
2. **阶段门禁**：P0→P6 每阶段有独立验收标准，未达成的以红色待办列在 `进度.md`，不向下游含糊推进。
3. **歧义即问**：关键决策（执行引擎选型、权限模型、MinIO 方案、P4 范围）先跟负责人逐项确认，确认后锁死在方案文档。
4. **确定性后台不交给模型**：权限、版本号、任务创建、时长/引用硬校验、文件登记、审核状态全部由 Go 后台实现；模型只负责拆解推理。
5. **全量回归**：每个阶段收尾跑 `go build / go vet / go test ./...`，仓库始终可编译、可测试、可构建。

## 阶段时间线

| 阶段 | 时间段 | 核心产出 |
|---|---|---|
| P0 骨架 | 2026-09-01 | 根入口 + 两仓库 init + AGENTS/CLAUDE + skill |
| P1 前端工程化 | 2026-09-01 | 前端源码落地、env/API 改造、删 nginx，样式 100% 不变 |
| P2 后端骨架 | 2026-09-01 | 单库 schema + goose + gin 空跑 + dev compose |
| P3c 认证域 | 2026-09-01 | PBKDF2 密码 / 三 token / Casbin RBAC |
| P3d 项目域 | 2026-09-01 | 项目/分集/剧本 CRUD、Go 原生 docx 解析、五步拆解 submit |
| P3e 任务域 | 2026-09-02 | 任务 15 路由 / 分组 / 权限模型 / 事务内通知 |
| P3f 文件/资产/生成器 | 2026-09-03 | 真实 MinIO + 服务端流式代理、资产域、任务生成器、分集删除清理 |
| P3g 看板/通知/Hermes | 2026-09-03 | admin overview/probe、通知读端、Hermes 静态端点 |
| P4 agent 接入 | 2026-09-01 立项 · 09-03 闭环 | Python agent 子模块、SSE run-events、prompt-jobs、materialize |
| P5 画布与编排 | 未开始 | canvas 并入、integration 收敛 |
| P6 CICD 收尾 | 未开始 | 云效流水线 + deploy.sh + docs 补全 |

---

## P0 骨架（2026-09-01）

**目标**：一个能让任意 AI 智能体从任意入口读全貌的工作区。

**落地内容**
- 根目录 README.md / AGENTS.md / docs/（索引、方案、规范三件套）
- `cineforge-h5`（前端）与 `cineforge-server`（后端）两个独立仓库各自初始化，带 CLAUDE.md / AGENTS.md
- 全局 skill（`~/.claude/skills/cineforge-home` / `cineforge-docs` / `cineforge-code-style`）

**验证**：方案文档定稿 v1.0，命名全链路统一（仓库 / Go module / 数据库 / 域名 / bucket 均为配置项）。

## P1 前端工程化落地（2026-09-01）

**目标**：前端页面在样式 100% 不变的前提下，完成工程化适配。

**关键决策**：样式/页面结构/交互一概不改；只点名调整四处 —— env/API 寻址、token 交互、SSE 域名、删 nginx。

**落地内容**
- 前端全量源码落地（119 个 src 文件 + public/config），样式与交互未动
- API 寻址 env 化：`api.ts` 新增 `API_BASE`，SSE（`useCineForgeApp`）、媒体读取（Assets/AssetLibrary/review）全部前缀化
- `tapApi.ts` 新增 `TAPAPI_BASE`；`goApi.ts` 保留 `VITE_GO_API_BASE`
- `vite.config.ts`：`VITE_CANVAS_URL` 注入，proxy 默认指向 localhost，支持 env 覆盖
- 品牌文案统一为 CineForge（login/AppShell/审片台/AgentAdmin 等 8 处）
- `.env.example / .env.development / .env.production` 就位（域名占位）
- 移除 nginx 反代与 Dockerfile（改 OSS 静态托管）

**验证**：`npm test` 66/66 通过；`npm run build` 通过。

## P2 后端骨架与单库（2026-09-01）

**目标**：Go 单体后端从零可跑，数据库单库 schema 可端到端迁移。

**落地内容**
- gin 路由（/health /ready）、config 双通道（config.yaml + CINEFORGE_* env）、goose migrate CLI
- dev compose 底座（PG16+pgvector / Redis7 / MinIO）
- 单库 schema 映射定稿（`docs/后端/单库schema映射.md`）：四项关键决策全部确认（身份主表唯一化 / outbox 移除 / 画布前缀统一 / 迁移版本从 00001 起）
- 生成 `migrations/00001_initial_single_db_schema.sql`（46 业务表 + goose_db_version）：按定稿 schema 完整构建 → `pg_dump --no-owner` 实测导出，非手抄

**验证（端到端）**：空库 `goose up` → 48 表（46 业务 + goose 版本表）与定稿清单逐一对齐；`down` 干净回滚到 0；重 `up` 恢复；`/ready` 200。
**修过的坑**：剥离 pg_dump 输出的 `\restrict` / `set_config search_path` 等 psql 元命令壳。

## P3c 认证域（2026-09-01）

**目标**：登录鉴权闭环，密码与新平台互操作。

**落地内容**
- 迁移 `00002_auth_casbin_baseline.sql`：5 个平台角色 + Casbin 能力种子（projects.read / tasks.read / flows.write / tasks.submit 全员，tasks.review 仅 admin/director），已在 postgres 验证
- `internal/config` auth 块 + env 双通道（SECRET / SESSION_TTL / TAPCANVAS_JWT / PLATFORM_JWT 键族）
- `internal/auth/password.go`：PBKDF2-HMAC-SHA256 120k 轮（`pbkdf2_sha256${salt-hex}${digest-hex}`），默认密码=手机号后 6 位；互操作固定向量通过
- `internal/auth/tokens.go`：三 token 签发/校验（access 2-part HMAC / tapcanvas HS256 / platform RS256），TTL 上限 8h，共享 `session_expires_at`；互操作向量通过
- `internal/casbin`：基于 sys_casbin_rule 种子的 enforcer（60s 缓存）
- repo/service/handlers：POST /api/auth/login|me|verify-password|password、GET/POST /api/users、PATCH /api/users/:id/status、POST /api/users/:id/reset-password

**验证**：`go test ./...`（auth / casbin / api 三包）全绿。

## P3d 项目域（2026-09-01）

**目标**：项目/分集/剧本版本的完整数据链，五步拆解可提交、草稿可人工确认。

**落地内容**
- repository：项目/分集/剧本版本 CRUD、`LatestScopedBreakdown`、`GetArtifactRevisionByID`、`PendingAgentRun`（input 顶层层级匹配）、`FindWorkflowByIdempotencyKey`、脚本段视图、operation log
- 剧本文件解析（`internal/service/script_parse.go`）：扩展名/大小校验、UTF-8/BOM→GB18030、**docx Go 原生 zip+XML 解析**（安全限制）、rune 计数控制字符校验（修复 CJK 误判）、单集标题解析
- Import/POST 全流程：校验顺序、项目建档/合并、episode/script/version 落库、归档（P3f 起接真实 MinIO）
- 五步拆解 submit（`SubmitBreakdownStep`）：步骤映射 + 作用域解析 + 前置闸口 + 幂等 + `workflow_runs/agent_runs` 落库（queued）+ 操作日志；**执行引擎在 P4 接入**
- 拆解草稿：GET current（200 或 null）、POST save（乐观锁 409 + 显式人工确认 → agent/system/human 三级 reading_report revision）、确认后闸口 409
- Dashboard 项目健康度聚合（storyboard/asset/task/completed/overdue/completion_rate）
- `GET /api/production-brief/catalog`、`GET /api/agent-runs`、`GET /api/agent-runs/{id}`、`/projects/{id}/script-segments`

**验证**：`internal/api/project_contract_test.go`（import 校验、CRUD、episode 状态、breakdown 202+闸口、乐观锁 409、dashboard、catalog、agent-runs）全绿。

## P3e 任务域（2026-09-02）

**目标**：制作任务、多候选上传审核、分组工作台全链路。

**落地内容**
- repository：`tasks_read.go`（TaskView 56 字段、列表/详情/审核工作台可见性）、`tasks_prompt.go`、`tasks_batch.go`（submit/review/bulk + uuid5 request_id 幂等）、`tasks_grouping.go`（storyboards / board / scene-gating / storyboard-video-tasks / bulk-assign）、`tasks_submission.go`（候选登记/编辑/软删/归档/主母版）
- service 层：`TaskService` + `StatusError` 映射（404/403/400 逐字）、workspace 统计、分组读写；事务 owner（`withTx`）
- handlers 路由：`/tasks` 15 条、`/workspace` 2 条、分组 `/projects/{id}` 5 条（task-assignments 走 director/admin middleware）；request_id 四消费点 UUID 硬校验（非 UUID → 422）
- **权限模型补齐（决策）**：分组读权限三处一致（`GetProjectForTask` / `canRead` / `CanAccessProject`），含「可见任务」回退，修复非成员执行人 403 的偏差；`GetProductionBoard`/`SceneGating` 内部防御门统一到 `CanAccessProject`；顺带修复 `visibleStoryboardIDsForProject` 占位符冲突（$1 复用）潜在 500
- 通知：改派/审核定版通知在任务事务内落 `notifications`（`is_read=false` 显式写）；顺带修复 seed 用户清理的 FK

**验证**：`internal/api/task_contract_test.go` 11 个用例（含 visible-task fallback 新覆盖）全绿。

## P3f 文件 / 资产 / 生成器 / 分集删除清理（2026-09-03）

**关键决策（与负责人确认）**：**真实 MinIO（minio-go SDK）+ 服务端流式代理**（无预签名 URL，Range/206/416 与参考实现逐字节对齐）；分集删除 `delete_files=true` 真正删对象；前端样式 100% 不变。

- **P3f-a 存储域**：`internal/storage`（minio-go：Put/Stat/Open/Remove/EnsureBucket）+ 真实 `NewMinioArchiver` 归档器（扩展名白名单、对象键 `{prefix}/script/imports/...`、`file_code {PREFIX}-SCRIPT-{uuid8}`）——**消除 import 503**；MinIO 未配置保持 503 兜底契约
- **P3f-b 文件域**：`/files/task-upload`（multipart 手动解析保 part Content-Type、流式上交、对象键 V{n} 布点、`minio://` url）；content/stream/download/playback-url/download-url（**HMAC 文件 token 字节兼容** `file-playback:` 前缀，Range 200/206/416 + `Content-Disposition filename*=UTF-8''`，`is_company_asset_library_file` 公司资产库兜底）
- **P3f-c 资产域**：`/assets` CRUD（正式码分配、project_id 校验路径）、`/assets/library`（include_versions）、variant-plans（创建恒 400 契约）、`/assets/temporary-production`（request_id 幂等 + 未确认 400 + 跨版本守卫）、`/assets/{id}` 详情、`scene-options`（human 终版修正）、分集详情 asset_bindings、AssetVersion 物化接线
- **P3f-d 任务生成器**：`POST /projects/{id}/asset-tasks` 与 `POST /projects/{id}/storyboard-tasks`（query 必填 `episode_id`+`script_version_id`；数据链守卫逐字对齐——含 tier-1 `assets_not_confirmed` 短路、缺 Prompt Skill 409、无 final revision 400；**行锁幂等** `task_idempotency_key` + verbatim 守卫；修复 JOIN 62 列单次 Scan）
- **P3f-e 分集删除文件清理**：级联删除事务内 `delete_files` 门控收集 clean/shared → 提交后 `storage.Remove` 真删对象 → `FinalizeEpisodeDeleteFileCleanupTx` 删文件行 + 操作日志 `detail.file_cleanup` 回填最终计数（修复 `shared_retained` 错用可删数、响应 key 对齐 `succeeded/failed/shared_retained/pending_retry/failed_objects/shared_objects`）；共享文件保留（`仍被项目级或其他分集数据引用`）

**验证**：`internal/api` file(4) + asset(8) + task_generators(10) + 共 **7 个 episode delete 用例**全绿（MinIO 用例按 `minioAvailable` 探针门控，真实闭环保留）。

## P3g admin 看板 / 通知读端 / Hermes 静态端点（2026-09-03）

**关键决策**：`/agents/hermes/health` 先静态配置派生（P4 接入真实 Hermes 探活前 status=unreachable/disabled）；`stats.agentRuns` 用 `agent_runs` queued/running 行数作为在途运行数。

- 通知读端：`GET /workspace/notifications`（本人最近 100 条倒序）+ `POST /workspace/notifications/read-all`
- `/admin/overview`：stats 短键（users/projects/tasks/overdue/assets/submissions/agentRuns）+ extended 别名、`overdue_tasks` Top5（复用 TaskView 56 字段）、`recent_readings`（script_reading 最近 3 条 + 分集/项目信息）
- `/admin/probe`：27 张表行数 counts + `recent_projects`(5) / `recent_agent_runs`(8) / `recent_operation_logs`(8)
- `/agents/hermes/config` + `/agents/hermes/health`（config 双通道扩展 `CINEFORGE_HERMES_{ENABLED,HEALTH_URL,MODEL,DASHBOARD_URL,WEBUI_URL,API_KEY}`；空 dashboard/webui 按域名填充）

**验证**：`internal/api/admin_contract_test.go` 5 个用例（通知读写、overview 形状+403、overdue+readings、probe 27 表、hermes enabled/disabled 分支）全绿。

## P4 agent 接入（2026-09-01 立项，09-03 闭环）

**目标**：五步拆解从「落库即可」到「真实执行」，前端硬契约全部闭环。

**关键决策（与负责人确认）**：
- Q1 **执行引擎 = Python `agent/` 子模块（推荐）**——Go 经 `internal/agentclient` 把已落库的运行投递给 `bridge.py`（Celery `task_id=run_id` 幂等），Python 侧完成 LangGraph→Hermes→归一化→物化执行闭包，Go 侧仍占确定性后台。
- Q2 **P4 范围 = 前端硬契约全落地（推荐）**——五步真实执行、SSE run-events、task prompt-jobs、retry-distribution、materialize、project-query、非正式 agent jobs、内置真实探针全部落地；`/agents` definitions/stage-map/`/agent-jobs`/feedback/training-samples 按既定契约实现精简集。

**落地内容**
- **P4a/b agent 子模块**：`agent/` Python 3.11/uv 子模块（config.py 以 `CINEFORGE_*` env 映射；`db/base.py` 统一连 `cineforge` 库且无 create_all/ALTER；requirements 按需裁剪无 alembic/grpcio/PyJWT）；`bridge.py` `:8091`（GET /health + POST /api/runs/{run_id}/enqueue）；Celery `execute_agent_run`（materialize 钩子在 exec 后）
- **P4c agent-client 接线**：运行落库（`saveRunningRun` 无 op-log）→ EnqueueRun → 入队失败 run 落 `failed` 但仍按 202/200 返回
- **P4d SSE run-events**：`GET /projects/{id}/run-events`——快照形状逐字段对齐 `_project_run_snapshot`（AgentRunSummaryRead/WorkflowRunSummaryRead，episode/version 来自 summary_json）；Redis 不可用 → 503 + Retry-After + polling fallback；401/404/403 逐字。契约测试 `run_events_contract_test.go`（`disconnectRedis` 把测试 Redis 指向死端口，不依赖开发机 6379）
- **P4e agent 域**：agents definitions/stage-map/`{agent_type}`/hermes config+**health 真实探活**（timeout=min(hermes_timeout_seconds,8s)，health→/models 探测链）；`POST /agent-jobs`（director/admin 202，未注册类型 404 `Agent not found`）；`POST /projects/{pid}/agents/{type}/jobs`（400 stage gate + pending 去重）；materialize（409 FORMAL gate/400 类型/403/404 + relation_check/content_compliance_review 确定性分支 + 幂等 training sample）；retry-distribution（404/403/409 逐字）；`POST /projects/{pid}/query-agent`（本地向量 `token_hash_v1`，非导演可见任务过滤）；feedback/training-samples（POST 201 / GET 列表，403 `Training sample permission denied`）
- **P4e-6 task prompt-jobs**：`POST /tasks/{task_id}/prompt-jobs?step=keyframe|video` → 202 AgentRunRead。守卫阶梯/类型映射/input 契约/`context_fingerprint`/生产上下文引用/keyframe_reference/op-log（`agent_run_created` detail 含 task_id）全部对齐既定契约；实现 `service/tasks_prompt_jobs.go` + `repository/tasks_prompt_jobs.go` + `service/task_prompt_context.go` + `productionbrief`（ResolvedProductionBrief.v1 / BuildAgentExecutionContext）
- **P4e-7 接线验证**：`p4e_routes_test.go` 路由注册守卫（9 条 P4e 路径共存不 panic）+ 全量契约测试

**验证**：`internal/api` run_events(3) + task_prompt_jobs(4) + p4e_routes(1) 等全量用例全绿；端到端 smoke 验证完整链路（真实 TCP 服务 → 登录 → 建项目+任务 → `POST /tasks/{id}/prompt-jobs` → 202 + input 契约 + `agent_run_created` op-log；入队失败时 run 落 failed 但 HTTP 仍 202）。
**待补充**：真实 Hermes 联调冒烟（本地无 hermes 时 health 走 unreachable 兜底，P5/P6 部署环境验证）。

## P5 / P6 展望

- **P5 画布与编排**：canvas 并入、integration 收敛，验收 = 画布导入/绑定可用。
- **P6 收尾**：云效流水线 + deploy.sh + docs 补全，验收 = 从流水线发一版到 ECS 健康。

## 沉淀下来的方法论

1. **契约测试即验收单**：每个域的「错误阶梯 + 幂等 + 状态机」都固化为测试文件，行为改动必须背书测试。
2. **确定性/生成性分离**：Go 管业务事实，Python 管模型推理，边界在 `agentclient` 和 `saveRunningRun` 的登记链上。
3. **决策档案化**：负责人确认过的每个决策（Q1/Q2、MinIO、权限模型）都回写进方案/契约文档，避免返工。
4. **兜底不崩塌**：外部依赖（Redis、MinIO、Hermes）不可用时接口仍返回可解析的失败态（503 兜底 / unreachable 状态），前端永不白屏。
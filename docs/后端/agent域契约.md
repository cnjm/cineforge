# Agent 域契约（P4/P4e 规范化记录）

> 以既定契约为基线（FastAPI 参考实现）：agents 定义 / jobs / runs / training 路由、
> 项目级 agent 子路由；后台投递语义对齐既定契约 `submit_background`。
> 实现见 `cineforge-server/internal/api/{agents_handlers,agents_registry,run_events_handlers}.go`、
> `internal/service/{agents_exec,agents_feedback,run_events}.go`、
> `internal/repository/{projects_feedback,projects_materialize}.go`、`internal/agentclient`。
> 契约测试见 `internal/api/run_events_contract_test.go`、`p4e_routes_test.go`、admin 域 feedback/training 用例。
> 执行引擎 = **Python `agent/` 子模块**（bridge.py `POST /api/runs/{run_id}/enqueue` → Celery
> `execute_agent_run`，`task_id=run_id` 幂等，对齐既定契约投递链）。

## 执行登记约定

- 所有后台 job 统一 `saveRunningRun`（无 op-log）登记：CreateAgentRun(status=`running`) →
  `agentclient.EnqueueRun(run_id)`；入队失败对齐 `submit_background`：run 落 `failed` + error_message，
  但 HTTP 仍按 202/200 返回。
- 分集/项目级 agent job（`SubmitProjectAgentJob` 等 agent 路径）额外登记 one
  op-log `agent_run_created`，detail=`{agent_type, status}`；task prompt-jobs 登记的
  detail 多 `task_id`（见 任务域契约）。
- Go 侧不执行模型推理：确定性逻辑（权限/版本/任务/幂等/审核）归 Go，模型执行归 `agent/`。

## Agents 定义与舞台映射（静态，无鉴权）

- `GET /api/agents` → `AgentDefinitionRead[]`；`GET /api/agents/{agent_type}` → 单定义（注册表见
  `agents_registry.go`，按既定契约裁剪的清单，与 Python `agent/` 注册表一致）。
- `GET /api/agents/stage-map` → `[{stage, agents[]}]`，供项目级 agent job 的 stage 校验。

## Hermes 配置与真实探活

- `GET /api/agents/hermes/config` → 双通道配置派生（P3g；`CINEFORGE_HERMES_*`）。
- `GET /api/agents/hermes/health` → **P4e 真实探活**：探测链对齐既定契约 `HermesClient.health()`
  先 `health_url`、后 `{base_url}/models`；`timeout=min(hermes_timeout_seconds, 8.0)`；
  status 优先级 `unreachable`（health 网络失败）> `ok/unhealthy`（health 状态码）>
  `auth_failed/unhealthy`（models 状态码）。

## 非正式 Agent job

- `POST /api/agent-jobs`（director/admin）→ 202 AgentRunRead：`agent_type` 未注册 → 404 `Agent not found`；
  请求体校验失败 → 422；status=`running` 落库后立即入队。
- `POST /api/projects/{project_id}/agents/{agent_type}/jobs` → 项目级 job：
  项目不存在 → 404 `Project not found`；无写权限 → 403 `Project permission denied`；
  stage 不在注册表放行集合 → 400 `Agent is not available for this project stage`；
  `PendingAgentRun`（project+agent_type+无 parent）命中 → 直接返回既有 run（去重，不新开）；
  否则新建 running run（input 顶层 `{project_id, skill}`）+ op-log。

## 运行物化（deterministic branches）

- `POST /api/projects/{project_id}/agents/runs/{run_id}/materialize`：
  - 项目/run 不存在 → 404 `Project or run not found`；无写权限 → 403 `Materialize permission denied`；
    run 不属于项目 → 400 `Agent run does not belong to this project`；
    FORMAL_BREAKDOWN 类型 → 409 `正式拆解步骤由分步流程自动写入，不能从 Agent 管理区手工物化。`；
    不支持的类型 → 400 `Agent type does not support materialization: {type}`；
    无输出 → 400 `Agent run has no output`；relation_check 缺 relation_report → 400 `关系校验输出缺少 relation_report`。
  - 确定性写入（单事务）：`relation_check` → 追加归一化 artifact revision
    （agent_raw→normalized 幂等链）；`content_compliance_review` → 生成 `agent_review` entity version
    （`REVIEW-{project}-{run[:8]}`）；两者恒幂等写 `{agent_type}_materialized` training sample。

## 重试分发

- `POST /api/projects/{project_id}/breakdown-steps/prompts/{run_id}/retry-distribution`（director）：
  run 不存在 → 404 `AgentRun not found`；无写权限 → 403 `Project permission denied`；
  run 不属于项目 → 409 `AgentRun 不属于当前项目。`；
  非 `asset_prompt_generation` → 409 `当前 AgentRun 不是正式资产提示词步骤。`。

## 项目问答（project-query）

- `POST /api/projects/{project_id}/query-agent`（任意已认证用户）：
  404 `Project not found` / 403 `Project permission denied`（与 run-events 同源）；
  `question` 1..1000 字符、`limit` 1..10（超界 422）。
  本地向量检索 `token_hash_v1`，文档来源 = 项目 + 脚本段 + 分镜 + 任务（含 submissions）；
  非 director 仅检索自己可见任务及关联分镜（可见任务为空 → 文档集为空）。
  `retrieval` 固定常量 `{mode:"local_vector", embedding:"token_hash_v1", service:"project_query", agent:false}`；
  `score=round(score,4)`，answer 模板与既定实现逐字一致。

## Feedback / 训练样本

- `POST /api/agent-runs/{run_id}/feedback` → 201 AgentFeedbackRead（run 不存在 → 404 `Agent run not found`）；
  单事务写 agent_feedback + `agent_feedback` training sample，回写 `agent_runs.feedback_json`
  （feedback_count/latest_feedback_at/latest_sample_id）。
- `GET /api/agent-runs/{run_id}/feedback` → `AgentFeedbackRead[]`（created_at DESC，original/edited 包装 `{"text":...}`）。
- `GET /api/agent-training-samples` → `AgentTrainingSampleRead[]`（`project_id`/`agent_type` 过滤）。
- `POST /api/agent-training-samples`（director/admin）→ 201；非导演 → 403 `Training sample permission denied`。

## Run 事件流（SSE）

- `GET /api/projects/{project_id}/run-events`（P4d）：快照形状对齐既定契约 `_project_run_snapshot`
  ——`agent_runs → AgentRunSummaryRead`（episode/version 取自 summary_json）、`workflows → WorkflowRunSummaryRead`；
  未认证 401、幽灵项目 404 `Project not found`、无权限 403 `Project permission denied`；
  Redis 不可用 → 503 + Retry-After + polling fallback 契约（不依赖开发机 Redis）。
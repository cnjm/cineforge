# CineForge 单库 schema 映射 · 草稿（待你确认）

> 事实来源：2026-09-01 对源码模型的全量盘点
> （Python 业务模型 39 表 + 枚举；
>  Go 身份域、业务域与画布流图域统一入单库 `cineforge`）。
> 状态：**已确认** ✅（2026-09-01，四项决策由你拍板，见文末；画布后补决策见「决策补充 4」）。
> 按本表生成 goose 迁移（版本从 00001 起，实际迁移 `migrations/00001_initial_single_db_schema.sql`）。

## 0. 各域表统一映射进单库 `cineforge`

| 域库 | 内容 | 归并去向 |
|---|---|---|
| 业务域 | Python agent 模型 + Go 共享业务表 | 全量并入（39+1 表） |
| 身份域 | Go 身份/授权模型 | 收敛并入（sys_* 授权保留） |
| 画布流图域 | Go 画布实现 | 并入（同名表去重合并，投影表移除） |

> 命名约定：**表名保持既定 snake_case 原样**（只有同名冲突时合并，不重命名），
> 前端接口契约与业务语义零变化；`goose_db_version` 唯一（新库独立版本号）。

## 1. 业务域（全保留）

### 1.1 identity 域
| 表 | 原属主 | 说明 |
|---|---|---|
| `users` | Python / Go 共享 | 身份主（电话登录、role、sync_version） |
| `permissions` | Python / Go 共享 | 用户↔项目授权 |

### 1.2 project 域
| 表 | 说明 |
|---|---|
| `projects` | 项目主（stage/lock/archive） |
| `project_episodes` | 分集（episode_no/code、production_brief） |
| `scripts` · `script_versions` | 剧本 + 不可变版本（content_hash） |
| `script_segments` · `storyboards` · `storyboard_versions` | 脚本段/分镜/分镜快照 |
| `script_breakdowns` | 版本化拆解 JSON（data_state） |

### 1.3 asset 域
| 表 | 说明 |
|---|---|
| `assets` · `asset_versions` | 资产主 + 不可变渲染版本 |
| `project_assets` · `asset_relations` | 项目↔资产、资产↔产出 连接 |
| `asset_variant_plans` · `asset_variant_storyboard_links` | 导演可见变体需求 + 分镜关联 |
| `episode_asset_bindings` | 分集↔资产绑定（confirmed_reading_revision） |
| `project_asset_lists` · `project_asset_list_items` | 版本化资产清单 |
| `asset_completion_records` | 人工补全/纠正记录 |
| `entity_versions` | 通用实体版本快照 |

### 1.4 task 域
| 表 | 说明 |
|---|---|
| `tasks` · `task_dependencies` | 任务 + DAG 边 |
| `task_submission_batches` · `submissions` | 提交批次 + 多候选提交/审核 |
| `task_candidate_imports` | 候选导入记录（Go 侧写入） |
| `task_prompts` | 任务提示词版本历史 |

### 1.5 agent/media/ops 域
| 表 | 说明 |
|---|---|
| `agent_runs` · `agent_issues` · `agent_feedback` · `agent_training_samples` | Agent 运行、诊断、反馈、训练样本 |
| `artifact_revisions` | 归一化→人工 final 版本链（围读也在此，artifact_type 区分） |
| `workflow_runs` | LangGraph 编排状态 |
| `files` · `image_outputs` · `video_outputs` · `final_outputs` | 文件登记 + 各类产出 |
| `notifications` · `operation_logs` | 通知（原样保留）+ 操作审计 |

### 1.6 待定
| 表 | 说明 |
|---|---|
| `integration_sync_outbox` | 跨服务用户同步 outbox —— **单进程后建议移除**（见 待确认①） |

## 2. 身份域（收敛并入）

| 表 | 去向 | 说明 |
|---|---|---|
| `sys_user` | **待定** | 平台管理用户的身份投影 —— 身份收敛决策见 待确认① |
| `sys_role` | 保留 | 角色定义 |
| `sys_casbin_rule` | 保留 | Casbin RBAC 策略（授权由 Go 单体直接读） |
| `sys_user_role`（如其存在） | 保留 | 用户↔角色连接；未在盘点中确认，进入首版迁移前复核 |
| `goose_db_version` | 移除 | 并入新库唯一版本表 |

## 3. 画布流图域（并入）

| 表 | 去向 | 说明 |
|---|---|---|
| `flows` | 并入（保留） | 画布定义；**无独立 flow_revisions 表**，版本在 flows 字段上（修正原方案措辞） |
| `cineforge_task_canvas_bindings` · `cineforge_task_canvas_members` | 并入 | 任务↔画布绑定 + 组成员 |
| `canvas_node_outputs` | 并入 | 画布节点执行输出 |
| `asset_storage_locations` | 并入 | 画布资产存储位 |
| `assets` → **`canvas_assets`** | **改名** | 与业务库 `assets` 列结构完全不同（非同实体），前缀改名保留两套 |
| `projects` → **`canvas_projects`** | **改名** | 同上，与业务库 `projects` 不同实体 |
| `users`（投影） | **移除** | 由 `users` 唯一身份主承担（决策 1） |
| `integration_sync_outbox`（画布域名表） | 移除 | 同 待确认② |

## 4. 单库最终表清单（48 张，goose 00001 实建成）

- 业务 39 → 移除 `alembic_version`、`integration_sync_outbox` → **37**
- 系统 2：`sys_role`、`sys_casbin_rule`；移除 `sys_user`、`goose_db_version`
- 画布 7：`flows`、`cineforge_task_canvas_bindings`、`cineforge_task_canvas_members`、`canvas_node_outputs`、`asset_storage_locations`、`canvas_assets`、`canvas_projects`
- 合计 `37 + 2 + 7 = 46` 业务表 + goose 自身 `goose_db_version`（运行时仅此表 +1）

## 待确认（3 项）→ 全部确认 ✅

1. **身份收敛方式** → **users 唯一主**：`users` 为唯一身份主（含 phone/password/role），`sys_user` 及画布 `users` 投影全部并入 `users`；`sys_role` + `sys_casbin_rule` 保留作授权。单进程直接读，无需投影。
2. **outbox 移除** → **移除**：单进程 + 单库后不再跨服务投影，`integration_sync_outbox`（两套合一张）删除，用户写入直接落库。
3. **画布投影去重** → **见决策补充 4**：清点时发现画布 `assets`/`projects` 与业务库同名表**列结构完全不同、并非同一实体**，无法合并去重，改为前缀改名。

> 由此 `sys_user`、画布 `users` 投影、`integration_sync_outbox`（×2）、`goose_db_version`（原各域版本表）均不进新库。
> 授权用 `sys_role` + `sys_casbin_rule`，`users.role` 保持既定角色语义。

## 决策补充 4（画布同名冲突 → 前缀改名）✅

清点画布模型后发现：画布侧 `assets` / `projects` 表为 `text` ID + 投影结构，与业务库（自增 ID + 规范化结构）完全不是同一实体，「同名合并去重」预案不成立。你已拍板改为：

- 画布 `assets` → **`canvas_assets`**，画布 `projects` → **`canvas_projects`**（仅改名，结构按既定画布模型原样入库）
- 其余画布表（`flows`、`cineforge_task_canvas_bindings`、`cineforge_task_canvas_members`、`canvas_node_outputs`、`asset_storage_locations`）重名即保留原表名
- 画布 `users` 投影表仍按决策 1 移除
- 前端/Go 画布代码引用随之同步（P5 并入画布时落地，`canvas_` 表名作为 Go model 的 `TableName()` 返回值）
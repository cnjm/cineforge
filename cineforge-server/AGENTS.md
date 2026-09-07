# AGENTS.md — cineforge-server（后端）

> 面向任意 AI 智能体。**先读根目录 `../AGENTS.md` 和本项目 `CLAUDE.md`，再读本文件。**

## 入口

- 根入口：`../README.md` · `../AGENTS.md` · `../docs/`
- 本项目规范：`CLAUDE.md`（含硬规则）
- 方案（唯一事实源）：`../docs/开发总方案.md`
- 全局规范：`../docs/规范与约定.md`

## 项目全貌

- **Go 单体** 接管：登录/项目/拆解元数据/任务/资产/文件/通知/操作日志/看板，以及画布与编排（内部包），HTTP：`/api/...`。
- **Python `agent/`** 仅承载五步拆解 Agent 编排 + Hermes 调用；Go 经 `agent-client` 调它。
- **数据库 `cineforge` 单库**：身份、业务、画布各域统一入库；完整 schema 见 goose 迁移 `migrations/00001_initial_single_db_schema.sql`（P2）。
- **Go module**：`cineforge/server`（内部包 `cineforge/server/internal/...`）。

## 关键约束

- 改 schema → 写 goose migration（版本递增），**不改历史 DDL**、不启动建表。
- 业务字段变更顺序：后端 schema/归一化/测试 → 前端 types → 页面。
- Agent 编排改动集中在 `agent/`（P4 起），Go 侧只做边界与编排调用。
- 配置：只改 `.env.example` / `config.yaml` 占位，不动代码硬编码。
- 不确定时向用户确认，不擅自做主。

## 验收自检（改动后）

```bash
go build ./...
go test ./...
go vet ./...
```
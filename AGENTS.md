# AGENTS.md — CineForge 工作区总入口

> 本文件面向任意 AI 智能体（Claude Code、Codex、其他）。**开工前先读完本文。**
> 你要做的是 CineForge 的代码/文档工作，从这里得知「系统长什么样、该往哪看、禁止碰什么」。

## 1. 必读顺序

```text
README.md            → 是什么、两仓库在哪、新手 5 步
docs/README.md       → 全局文档索引
docs/开发总方案.md    → 唯一事实源（决策表 / 命名 / 边界 / 实施阶段）
docs/进度.md          → 当前实施到哪一阶段
```

## 2. 仓库跳转

| 任务归属 | 去哪读 | 说明 |
|---|---|---|
| 前端页面 / 样式 / H5 | `cineforge-h5/AGENTS.md` | 样式红线、分层、构建 |
| 后端 API / DB / Agent | `cineforge-server/AGENTS.md` | Go 分层、goose、配置双通道 |
| 跨仓库 / 全局方案 | `docs/` | 只有根目录能回答 |

## 3. 全局约定

- **单 git 仓库**工作区：`cineforge-h5/` 与 `cineforge-server/` 同在一个仓库（`git@github.com:cnjm/cineforge.git`）提交、推送；两目录各司其职，但**跨目录改动同一次提交是正常的**。
- 产品名统一 **CineForge**；命名见 `docs/开发总方案.md` §2。
- 前端视觉 **100% 保持不变**，只动被明确点名允许的逻辑点（见 `cineforge-h5/CLAUDE.md`）。
- 后端 schema **只能**通过 goose 迁移演进，禁止启动建表、禁止手工 DDL。
- 域名、库名、bucket、密钥均为**配置项**（config.yaml / .env），改配置不改代码。
- **外部契约勿单侧改名**：画布 SSO 消息（`CINEFORGE_SSO_SESSION_V1`、`source:"cine-forge"`、`PROTOCOL_SOURCE="cineforge-group-canvas"`）与 tapcanvas/platform_token 声明（`sub="cineforge:"+id`、`iss=cine-forge-platform`、`aud=cine-forge-go`）由**仓库外**的 React-Flow 画布（本地 :5174）与 TapCanvas 后端消费。要改必须先同步对方，否则画布 SSO 握手失效（画布 P5 并入后在此仓库内同步）。
- 不确定、有歧义时，先向用户确认，不要擅自做主（用户明确要求）。

## 4. 常见入口问题

- “样式或组件改一下” → 先读 `cineforge-h5/CLAUDE.md` 红线再动手。
- “接口行为要变” → 定位 `cineforge-server` 对应 internal 包。
- “数据库要加字段” → 写 goose migration，不改已有 DDL 文件。
- “这套系统是怎么逐步搭出来的” → 读 [docs/AI 实现过程.md](docs/AI 实现过程.md)（分阶段实现叙事）。
- “某接口/行为以什么为基线” → 读对应的 `docs/后端/*契约.md`。
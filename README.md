# CineForge 🎬

> 人人都有自己的短剧制片厂 —— AI 漫剧生产工作台。

从剧本到**可执行制作任务**的五步工业化拆解闭环（剧本围读 → 资产拆解 → 提示词生成 → 脚本段拆分 → 分镜拆解），每一件作品都有可追溯的生产链。

本工作区包含同一产品的两个独立项目：

| 项目 | 目录 | 技术栈 | 发布方式 |
|---|---|---|---|
| 前端 | [cineforge-h5/](cineforge-h5/) | React 19 · TS 5.8 · Vite 6 | `npm run build` → OSS 静态托管 |
| 后端 | [cineforge-server/](cineforge-server/) | Go 单体(gin) + Python `agent/` 子模块 | 云效 CICD → 编译产物 → ECS |

## 🚀 新手 6 步

1. 读 **文档索引** — [docs/README.md](docs/README.md)
2. 读 **开发总方案**（唯一事实源）— [docs/开发总方案.md](docs/开发总方案.md)
3. 起 **前端** — [cineforge-h5/README.md](cineforge-h5/README.md)
4. 起 **后端** — [cineforge-server/README.md](cineforge-server/README.md)
5. 取 **开发账号**（后端启动时自动种子创建）— [docs/开发账号.md](docs/开发账号.md)
6. 看 **进度** — [docs/进度.md](docs/进度.md)

> 工作区为**单 git 仓库**（`git@github.com:cnjm/cineforge.git`），h5 与 server 两个目录同仓提交。

## 📍 当前位置

- 方案定稿：**v1.0**（2026-09-01）
- 当前阶段：**P4 已完成**（agent 执行接入），P5 画布并入、P6 CICD 待办
- 想看这套系统怎么一步步用 AI 搭起来的 → [docs/AI 实现过程.md](docs/AI 实现过程.md)

## 🤖 面向 AI 智能体

从根目录 [AGENTS.md](AGENTS.md) 进入，读完全局约定再开工。
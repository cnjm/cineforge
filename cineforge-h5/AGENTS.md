# AGENTS.md — cineforge-h5（前端）

> 面向任意 AI 智能体。**先读根目录 `../AGENTS.md` 和本项目 `CLAUDE.md`，再读本文件。**

## 入口

- 根入口：`../README.md` · `../AGENTS.md` · `../docs/`
- 本项目规范：`CLAUDE.md`（含红线）
- 全局规范：`../docs/规范与约定.md`

## 项目全貌

- 单页应用做“生产工作台”：项目导入 → 五步拆解 → 任务分发 → 多候选提交 → 导演审核 → 资产库。
- 页面域：`features/projects`（五步拆解/看板）、`features/tasks`（提交/审核）、`features/assets`（资产库）、`features/admin`（Agent/员工/日志）。
- 状态编排集中在 `hooks/useCineForgeApp.ts`，页面组件不重新实现 API 状态规则。

## 关键约束

- 打开任何组件前，先定位它属于哪个 feature 域；不要新建全局状态容器。
- 改业务字段：先核对后端 schema/归一化/测试，再改 `types/` 与转换函数（契约顺序）。
- 视觉红线见 CLAUDE.md；不确定时向用户确认，不擅自做主。

## 验收自检（改动后）

```bash
npm test
npm run build
```

样式变更不做 < 红线；跑构建保证产出正常。
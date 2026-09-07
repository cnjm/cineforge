# CLAUDE.md — cineforge-h5（前端）

## 项目

React 19 · TypeScript 5.8 · Vite 6 · **原生 Fetch 封装，无全局状态库**。
P1 已完成前端工程化落地；样式 100% 不变。

## 红线（CRITICAL）

- **样式 / 页面结构 / 交互 100% 不变**。除下表外一律不改动视觉。
- 页面禁止写网络层（统一走 `hooks/useCineForgeApp` 与 api 封装）。
- 前端只消费后端归一化接口，禁止直读 Agent 原始 JSON 当展示数据。

## 允许的四处逻辑改动（P1）

1. **API 寻址**：新增 `.env.development` / `.env.production` → `VITE_API_BASE=https://cine-forge-server.cnjm.top`
2. **token 交互**：access / tapcanvas / platform 三个 token 存储与携带方式不变，仅目标地址变化
3. **SSE / 长连接**：`run-events` 跟随 API 域名，CORS 由 Go 放行
4. **删除 nginx**：`nginx.conf`、Dockerfile nginx 托管段不进本仓库（OSS 托管）

## 目录分层（P1 已落地）

```
src/
  features/{projects,tasks,assets,admin,review,query,editing,create,assetLibrary,teamMembers}/  页面域
  hooks/              应用状态与 API 动作（useCineForgeApp）
  shared/             共用展示组件
  api.ts / goApi.ts / tapApi.ts   fetch 封装（API_BASE / GO_API_BASE / TAPAPI_BASE 由 env 注入）
  styles/ · layout/ · pages/ · public/   样式与布局（品牌文案统一为 CineForge）
```

## 命令

```bash
npm install
npm run dev       # VITE_API_BASE 指向后端，本地联调
npm test          # Node 内置 test runner
npm run build     # → dist/，自行上传 OSS
```

## env 约定

- `.env.example` 是模板，`.env.development` / `.env.production` 为公开域名配置（可入库）；`.env.local` 个人变体不入库。
- 新增变量必须同步根目录 `docs/环境变量模板.md`。
- 本机若 npm 私有源 403，可临时用 `--registry=https://registry.npmmirror.com` 安装。

## 红线之外：改动前置

任何“视觉上的”变更请求，先向用户确认是**有意改样式**还是**业务数据没喂对**后，再动手。
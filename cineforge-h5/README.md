# cineforge-h5（前端）

> CineForge 前端：React 19 · TypeScript 5.8 · Vite 6 · 原生 Fetch · 无全局状态库。
> 构建产物推 OSS 静态托管（SPA rewrite），不走容器反代。

## 状态

- **P0～P1 完成**：前端源码全量落地；样式 100% 不变；API 寻址已改为 env 驱动（`VITE_API_BASE` / `VITE_TAPAPI_BASE` / `VITE_CANVAS_URL`）；nginx 相关已移除；`npm test` 66/66 通过，`npm run build` 通过。

## 快速开始（P1 后生效）

```bash
npm install
npm run dev        # 本地开发，VITE_API_BASE 指向后端
npm test
npm run build      # 产物 dist/ → 自行上传 OSS
```

## 命名与地址（配置项，见 .env.example）

| 用途 | 值 | 说明 |
|---|---|---|
| 前端域名 | `cine-forge.cnjm.top` | OSS 托管 |
| API 域名 | `cine-forge-server.cnjm.top` | 后端，Go 处理 CORS |

## 红线

- 样式 / 页面结构 / 交互 **100% 不变**。
- 页面禁止写网络层；不引入全局状态库。
- 详细约束：先读 [CLAUDE.md](CLAUDE.md) 与 [AGENTS.md](AGENTS.md)。
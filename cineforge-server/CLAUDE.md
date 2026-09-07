# CLAUDE.md — cineforge-server（后端）

## 项目

Go 单体单服务（HTTP 框架 **gin**）+ Python `agent/` 子模块（五步拆解 Agent 编排 + Hermes）。
单库 `cineforge`（PostgreSQL 16 + pgvector）；goose 迁移；云效 CICD 编译产物上线 ECS（前生命周期用 docker-compose dev 底座）。

## 结构（P2 起填充）

```
cmd/                入口：server / migrate / agent
internal/
  api/              HTTP 层（gin 路由、中间件、参数校验、统一错误映射）
  service/          业务服务（确定性后台：权限、版本、任务创建、审核状态…）
  repository/       数据访问（单库 cineforge）
  model/            表模型
  agent-client/     调用 agent 子模块（HTTP/gRPC）
  canvas/           画布内部包（画布/流程编排职责）
agent/              Python：五步拆解编排 + Hermes + Celery（P4 填充）
```

## 规则（CRITICAL）

- **数据库 schema 只能 goose 迁移**；禁止启动建表、禁止手工 DDL、禁止改历史 migration。
- **配置双通道**：`config.yaml` + 环境变量，`.env.example` 是唯一模板，无真实密钥入 git。
- 域名（`cine-forge-server.cnjm.top` / `cine-forge.cnjm.top`）与 bucket（`cineforge-assets` 占位）都是配置项。
- **确定性逻辑不交给模型**：权限 / 版本号 / 任务创建 / 时长与引用硬校验 / 文件登记 / 审核状态。
- 四层数据链：agent 原始 → 归一化 → 人工 final → 下游；前端只读正规化接口。

## 命令

```bash
go build ./...        # 编译单体
go test ./...         # 测试
go vet ./...          # 静态检查
go run ./cmd/server   # 起服务 → GET /health 200；/ready 需 DB 就绪
go run ./cmd/migrate -action up      # goose 执行迁移到 head
go run ./cmd/migrate -action status  # 查看迁移版本
docker compose up -d  # 本地底座（postgres16+pgvector / redis7 / minio）
```

## 配置

- 双通道：`config.yaml`（可跑默认值）+ `CINEFORGE_*` 环境变量（覆盖）。优先级 env > yaml。
- `.env.example` 是唯一模板；生产在云效/ECS 注入，不落真实密钥进 git。
- 关键键：`CINEFORGE_DB_DSN`、`CINEFORGE_SERVER_DOMAIN`、`CINEFORGE_WEB_DOMAIN`、`CINEFORGE_MINIO_BUCKET`。

## 本地开发注意事项

- **不要用 zsh `source .env` 加载配置**：`.env` 中 `CINEFORGE_PLATFORM_JWT_PRIVATE_KEY`
  的值含空格，zsh `source` 会把它按词拆成命令执行（报 `command not found: RSA`），
  私钥被截断后 `POST /api/auth/login` 返回 503 `登录令牌签发暂不可用`。正确加载方式：

  ```bash
  # 方式一：python 加载 .env 后起服务（推荐，兼容 zsh）
  python3 - <<'PY'
  import os, subprocess
  env = dict(os.environ)
  for line in open('.env'):
      line = line.strip()
      if not line or line.startswith('#') or '=' not in line:
          continue
      k, v = line.split('=', 1)
      env[k] = v
  subprocess.run(['go', 'run', './cmd/server'], env=env)
  PY

  # 方式二：bash + set -a（仅 bash，zsh 不适用）
  bash -c 'set -a; . ./.env; set +a; go run ./cmd/server'
  ```

- 签名三件套（`CINEFORGE_AUTH_SECRET` / `CINEFORGE_TAPCANVAS_JWT_SECRET` /
  `CINEFORGE_PLATFORM_JWT_PRIVATE_KEY`）任一为空或私钥格式损坏时，登录一律 503；

## 契约顺序

改字段：后端 schema/归一化/测试 → 前端 types/转换函数 → 页面；不得先动前端。
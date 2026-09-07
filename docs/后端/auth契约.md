# auth 域契约（P3c 规范化记录）

> 以既定 FastAPI 参考实现为规范来源。实现见 `cineforge-server/internal/auth|casbin|api`，契约测试见 `internal/auth/tokens_test.go`、`internal/casbin/enforcer_test.go`、`internal/api/auth_contract_test.go`。
> 错误 detail 文案与既定实现逐字透传；前端契约：`fetch(${API_BASE}/api${path})`、错误取 `body.detail` 字符串、401 → AuthExpiredError。

## 密码与 hash（PBKDF2）

- 存储格式：`pbkdf2_sha256${salt-hex}${digest-hex}`；salt 为 16 字节 hex 文本（UTF-8），PBKDF2-HMAC-SHA256，120,000 轮，输出 32 字节 hex。
- **默认密码 = 手机号后 6 位**（`DefaultPasswordForPhone`）。
- 与既定 Python 实现互操作验证通过（固定向量测试）。

## 三 token 束（login 一次性返回，共享 session_expires_at）

| token | 算法 | 关键声明 |
|---|---|---|
| `access_token` | 私有 2-part `{body}.{signature}`，HMAC-SHA256 作用于 body | `body` = b64url(json{sub, role, exp})；无 header |
| `tapcanvas_token` | HS256 JWT | `sub="cineforge:"+user.id`、`role` 映射（admin/director→platformAdmin，editor/script_editor→editor，artist→member）、`iat/exp` |
| `platform_token` | RS256 JWT（kid） | `iss=cine-forge-platform`、`aud=cine-forge-go`、`sub="cineforge:"+user.id`、`tenant_id=default`、`jti`、`iat/exp` |

- **TTL**：`session_expires_at` = Unix 秒；TTL ∈ (0, 28800]，cap 8h（`auth.MaxSessionSeconds`）。
- 3 个 secret 一起才算签发可用（缺失 → 登录 500 "令牌签发失败"）。

## 接口清单

| 接口 | 鉴权 | 成功 | 失败（状态码 / detail） |
|---|---|---|---|
| `POST /api/auth/login` `{phone, password}` | 无 | 200 三 token + user 视图 + `token_type=bearer` | 401 `手机号或密码错误`（未知/密码错/禁用统一） |
| `GET /api/auth/me` | Bearer | 200 user 视图 | 401 `Not authenticated` / `Invalid token` / `Token expired` / `User disabled or not found` |
| `POST /api/auth/verify-password` `{password}` | Bearer | 200 `{ok:true}` | 401 `当前账号密码错误` |
| `POST /api/auth/password` `{current_password, new_password}` | Bearer | 200 | 401 `当前账号密码错误` |
| `GET /api/users` | director/admin | 200 用户列表（无 password_hash） | 403 `Director or admin permission required` |
| `POST /api/users` `{phone, role, display_name?}` | director/admin | 201（username 回填 phone，未传密码→手机号后 6 位） | 409 `手机号已存在`；422 校验 |
| `PATCH /api/users/:id/status` `{is_active}` | director/admin | 200 | 404 `User not found` |
| `POST /api/users/:id/reset-password` `{password?}` | director/admin | 200 `{temporary_password}`（缺省=手机号后 6 位） | 404 `User not found` |

## user 视图（安全 DTO）

`{id, phone, username, name, display_name, role, is_active, created_at?}` —— **永不返回 password_hash**；400 参数校验即 422 块。

## Casbin 权限（migration 00002 种子）

```
5 平台角色（admin/director/script_editor/editor/artist）
× capabilities（projects.read / tasks.read / flows.write / tasks.submit）→ execute 全员
任务审核：tasks.review → execute 仅 admin/director
```

- 校验来源 `sys_casbin_rule` 表（普通 users 信任此表），`permissions` 表承载项目级角色（project_role:*，P3d+ 消费）。
- `Enforce(ctx, role, obj, act)` 60s 缓存重载。

## 密钥配置（双通道）

| config 键 | env | 说明 |
|---|---|---|
| `auth.secret` | `CINEFORGE_AUTH_SECRET` | access_token HMAC secret（必填） |
| `auth.session_ttl_seconds` | `CINEFORGE_AUTH_SESSION_TTL_SECONDS` | 默认 28800 |
| `auth.tapcanvas_jwt_secret` | `CINEFORGE_TAPCANVAS_JWT_SECRET` | tapcanvas HS256（必填） |
| `auth.platform_jwt_private_key` | `CINEFORGE_PLATFORM_JWT_PRIVATE_KEY` | RS256 PKCS8/PKCS1 PEM 单行（\n 转义）（必填） |
| `auth.platform_jwt_kid` | `CINEFORGE_PLATFORM_JWT_KID` | platform token kid（必填） |

生产必须替换全部 CHANGE_ME 密钥，且 3 secret 生命周期与用户会话（≤8h）对齐。
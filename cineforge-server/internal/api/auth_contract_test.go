package api_test

// auth 域 HTTP 契约测试（对齐 legacy FastAPI 逐字 detail）：
//   - POST /api/auth/login / me / verify-password / password
//   - GET /api/users、POST /api/users、PATCH /status、POST /reset-password
//   - 三 token 声明契约（exp 一致、platform iss/aud/sub/tenant/kid）
// 需要 dev DB + .env 的 auth secret（缺失自动 skip）。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/api"
	"cineforge/server/internal/auth"
	"cineforge/server/internal/config"
	"cineforge/server/internal/testutil"
)

var phoneSeq atomic.Int64

// uniquePhone 生成 6-20 位唯一 phone（数字），避免跨测试冲突：
// 进程内递增序号 + 本秒 nano 后 6 位，确保跨运行不撞 dev 库残留。
func uniquePhone(prefix string) string {
	n := phoneSeq.Add(1)
	run := time.Now().UnixNano() % 1_000_000
	return fmt.Sprintf("%s%06d%03d", prefix, run, n%1000)
}

// ---- 测试环境 ----

type testEnv struct {
	r    http.Handler
	pool *pgxpool.Pool
	cfg  *config.Config
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	pool := testutil.Pool(t)
	env := testutil.EnvMap(t)
	if env["CINEFORGE_AUTH_SECRET"] == "" {
		t.Skip("缺少 CINEFORGE_AUTH_SECRET（.env），跳过 auth 契约测试")
	}
	cfg := newTestConfig(t, env)
	stubAgentBridge(t, cfg)
	return &testEnv{r: api.NewRouter(cfg, pool), pool: pool, cfg: cfg}
}

// stubAgentBridge 把 cfg.Agent.Addr 指向本地 stub（enqueue→202、health→ok），
// 让 breakdown-steps 的 queued 断言不依赖真实 agent 子模块。
func stubAgentBridge(t *testing.T, cfg *config.Config) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/enqueue") {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"","status":"queued"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","hermes_enabled":true,"hermes_model":"hermes-agent"}`))
	}))
	cfg.Agent.Addr = srv.URL
	t.Cleanup(srv.Close)
}

// newTestConfig 从 .env 构造测试配置（auth + MinIO 双通道）。
func newTestConfig(t *testing.T, env map[string]string) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	cfg.Default()
	cfg.Auth.Secret = env["CINEFORGE_AUTH_SECRET"]
	cfg.Auth.TapcanvasJWTSecret = env["CINEFORGE_TAPCANVAS_JWT_SECRET"]
	cfg.Auth.PlatformJWTPrivateKey = env["CINEFORGE_PLATFORM_JWT_PRIVATE_KEY"]
	cfg.Auth.PlatformJWTKID = env["CINEFORGE_PLATFORM_JWT_KID"]
	if v := env["CINEFORGE_AUTH_SESSION_TTL_SECONDS"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Auth.SessionTTLSeconds = n
		}
	}
	// MinIO（P3f）：凭证未配置时 NewRouter 自动降级不可用归档器（import 503 契约保持）。
	if v := env["CINEFORGE_MINIO_ENDPOINT"]; v != "" {
		cfg.MinIO.Endpoint = v
	}
	if v := env["CINEFORGE_MINIO_BUCKET"]; v != "" {
		cfg.MinIO.Bucket = v
	}
	if v := env["CINEFORGE_MINIO_ACCESS_KEY"]; v != "" {
		cfg.MinIO.AccessKey = v
	}
	if v := env["CINEFORGE_MINIO_SECRET_KEY"]; v != "" {
		cfg.MinIO.SecretKey = v
	}
	return cfg
}

// newEnvWithMinIO 以是否启用真实 MinIO 归档器的方式构造测试环境。
// enabled=false 强制 MinIO 未配置（内部使用 archiveUnavailableArchiver → import 503 契约）。
func newEnvWithMinIO(t *testing.T, enabled bool) *testEnv {
	t.Helper()
	pool := testutil.Pool(t)
	env := testutil.EnvMap(t)
	if env["CINEFORGE_AUTH_SECRET"] == "" {
		t.Skip("缺少 CINEFORGE_AUTH_SECRET（.env），跳过 auth 契约测试")
	}
	cfg := newTestConfig(t, env)
	if !enabled {
		cfg.MinIO.AccessKey = ""
		cfg.MinIO.SecretKey = ""
	}
	stubAgentBridge(t, cfg)
	return &testEnv{r: api.NewRouter(cfg, pool), pool: pool, cfg: cfg}
}

// seedUser 创建测试用户并在测试结束清理。
func (e *testEnv) seedUser(t *testing.T, phone, role, password string) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO users (id, username, phone, name, display_name, role, password_hash, is_active, sync_version)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,true,0)`,
		id, phone, phone, phone, phone, role, hash); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		// notifications 以 user_id 强引用 users（迁移 00001 无 ON DELETE），须先清理。
		if _, err := e.pool.Exec(ctx, `DELETE FROM notifications WHERE user_id = $1`, id); err != nil {
			t.Errorf("cleanup notifications %s: %v", id, err)
		}
		// 资产/批次等创建人引用（assets.created_by_id / asset_versions.created_by 等）强引用用户；
		// 这些行的项目作用域数据由 cleanupProject 随后清理，此处删除失败时降级为停用用户，
		// 不给测试留 FK 阻断。
		if _, err := e.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
			if _, upErr := e.pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1`, id); upErr != nil {
				t.Errorf("cleanup user %s: %v", id, err)
			}
		}
	})
	return id
}

// seedUserAs 创建用户并返回其 access_token。
func (e *testEnv) seedUserAs(t *testing.T, phone, role, password string) string {
	t.Helper()
	e.seedUser(t, phone, role, password)
	return e.accessToken(t, phone, password)
}

func (e *testEnv) login(t *testing.T, phone, password string) (int, map[string]any) {
	t.Helper()
	return e.doJSON(t, http.MethodPost, "/api/auth/login",
		map[string]string{"phone": phone, "password": password}, "")
}

func (e *testEnv) accessToken(t *testing.T, phone, password string) string {
	t.Helper()
	code, body := e.login(t, phone, password)
	if code != http.StatusOK {
		t.Fatalf("login status %d body %v", code, body)
	}
	tok, _ := body["access_token"].(string)
	return tok
}

func (e *testEnv) doJSON(t *testing.T, method, path string, body any, token string) (int, map[string]any) {
	t.Helper()
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		raw = b
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (e *testEnv) setUserActive(t *testing.T, id string, active bool) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE users SET is_active = $2 WHERE id = $1`, id, active); err != nil {
		t.Fatalf("set active: %v", err)
	}
}

func detail(t *testing.T, body map[string]any, want string) {
	t.Helper()
	d, _ := body["detail"].(string)
	if !strings.Contains(d, want) {
		t.Fatalf("detail=%q 应包含 %q", d, want)
	}
}

func assertStatus(t *testing.T, got, want int, body map[string]any) {
	t.Helper()
	if got != want {
		t.Fatalf("status=%d want=%d body=%v", got, want, body)
	}
}

// ---- 登录 ----

func TestAuthLoginSuccessContract(t *testing.T) {
	e := newEnv(t)
	phone := uniquePhone("901")
	e.seedUser(t, phone, "director", "secret123")

	code, body := e.login(t, phone, "secret123")
	assertStatus(t, code, http.StatusOK, body)
	if body["token_type"] != "bearer" {
		t.Errorf("token_type=%v", body["token_type"])
	}
	for _, k := range []string{"access_token", "tapcanvas_token", "platform_token"} {
		if body[k] == "" {
			t.Errorf("缺少 %s", k)
		}
	}
	user, _ := body["user"].(map[string]any)
	if user["role"] != "director" || user["phone"] != phone {
		t.Errorf("user: %v", user)
	}
	if user["password_hash"] != nil {
		t.Error("响应不得泄漏 password_hash")
	}
	if exp, _ := body["session_expires_at"].(float64); exp <= 0 {
		t.Errorf("session_expires_at=%v", body["session_expires_at"])
	}
}

func TestAuthLoginWrongPassword(t *testing.T) {
	e := newEnv(t)
	phone := uniquePhone("902")
	e.seedUser(t, phone, "artist", "secret123")
	code, body := e.login(t, phone, "wrongpw")
	assertStatus(t, code, http.StatusUnauthorized, body)
	detail(t, body, "手机号或密码错误")
}

func TestAuthLoginUnknownUser(t *testing.T) {
	e := newEnv(t)
	code, body := e.login(t, uniquePhone("903"), "secret123")
	assertStatus(t, code, http.StatusUnauthorized, body)
	detail(t, body, "手机号或密码错误")
}

func TestAuthLoginDisabledUser(t *testing.T) {
	e := newEnv(t)
	phone := uniquePhone("904")
	id := e.seedUser(t, phone, "editor", "secret123")
	e.setUserActive(t, id, false)
	code, body := e.login(t, phone, "secret123")
	assertStatus(t, code, http.StatusUnauthorized, body)
	detail(t, body, "手机号或密码错误")
}

// ---- me ----

func TestAuthMeWithToken(t *testing.T) {
	e := newEnv(t)
	phone := uniquePhone("905")
	e.seedUser(t, phone, "script_editor", "secret123")
	tok := e.accessToken(t, phone, "secret123")

	code, body := e.doJSON(t, http.MethodGet, "/api/auth/me", nil, tok)
	assertStatus(t, code, http.StatusOK, body)
	if body["phone"] != phone || body["role"] != "script_editor" {
		t.Errorf("me body: %v", body)
	}
}

func TestAuthMeMissingToken(t *testing.T) {
	e := newEnv(t)
	_, body := e.doJSON(t, http.MethodGet, "/api/auth/me", nil, "")
	detail(t, body, "Not authenticated")
}

func TestAuthMeInvalidToken(t *testing.T) {
	e := newEnv(t)
	code, body := e.doJSON(t, http.MethodGet, "/api/auth/me", nil, "not.a-real-token")
	assertStatus(t, code, http.StatusUnauthorized, body)
	detail(t, body, "Invalid token")
}

func TestAuthMeDisabledUserRejected(t *testing.T) {
	e := newEnv(t)
	phone := uniquePhone("906")
	id := e.seedUser(t, phone, "artist", "secret123")
	tok := e.accessToken(t, phone, "secret123")
	e.setUserActive(t, id, false)

	code, body := e.doJSON(t, http.MethodGet, "/api/auth/me", nil, tok)
	assertStatus(t, code, http.StatusUnauthorized, body)
	detail(t, body, "User disabled or not found")
}

// ---- verify-password / password ----

func TestAuthVerifyPassword(t *testing.T) {
	e := newEnv(t)
	phone := uniquePhone("907")
	e.seedUser(t, phone, "artist", "secret123")
	tok := e.accessToken(t, phone, "secret123")

	code, body := e.doJSON(t, http.MethodPost, "/api/auth/verify-password",
		map[string]string{"password": "secret123"}, tok)
	if code != http.StatusOK || body["ok"] != true {
		t.Fatalf("verify ok: %d %v", code, body)
	}

	code, body = e.doJSON(t, http.MethodPost, "/api/auth/verify-password",
		map[string]string{"password": "wrongpw"}, tok)
	assertStatus(t, code, http.StatusUnauthorized, body)
	detail(t, body, "当前账号密码错误")
}

func TestAuthChangePasswordFlow(t *testing.T) {
	e := newEnv(t)
	phone := uniquePhone("908")
	e.seedUser(t, phone, "artist", "oldpass99")
	tok := e.accessToken(t, phone, "oldpass99")

	_, body := e.doJSON(t, http.MethodPost, "/api/auth/password",
		map[string]string{"current_password": "bad", "new_password": "newpass99"}, tok)
	detail(t, body, "当前账号密码错误")

	code, _ := e.doJSON(t, http.MethodPost, "/api/auth/password",
		map[string]string{"current_password": "oldpass99", "new_password": "newpass99"}, tok)
	assertStatus(t, code, http.StatusOK, body)

	if code, _ := e.login(t, phone, "oldpass99"); code != http.StatusUnauthorized {
		t.Errorf("旧密码应失效")
	}
	if code, _ := e.login(t, phone, "newpass99"); code != http.StatusOK {
		t.Errorf("新密码应可登录")
	}
}

// ---- users（director/admin）----

func TestUsersRequireDirectorOrAdmin(t *testing.T) {
	e := newEnv(t)
	artistTok := e.seedUserAs(t, uniquePhone("909"), "artist", "secret123")
	dirTok := e.seedUserAs(t, uniquePhone("910"), "director", "secret123")

	code, body := e.doJSON(t, http.MethodGet, "/api/users", nil, artistTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director or admin permission required")

	code, _ = e.doJSON(t, http.MethodGet, "/api/users", nil, dirTok)
	assertStatus(t, code, http.StatusOK, body)
}

func TestUsersCreateAndDuplicate(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("911"), "admin", "secret123")
	phone := uniquePhone("912")

	code, body := e.doJSON(t, http.MethodPost, "/api/users", map[string]any{
		"phone": phone, "role": "artist", "display_name": "阿测",
	}, dirTok)
	assertStatus(t, code, http.StatusCreated, body)
	if body["username"] != phone {
		t.Errorf("username 应回填 phone（对齐 legacy）: %v", body["username"])
	}
	if body["display_name"] != "阿测" {
		t.Errorf("display_name: %v", body["display_name"])
	}

	code, body = e.doJSON(t, http.MethodPost, "/api/users", map[string]any{
		"phone": phone, "role": "editor",
	}, dirTok)
	assertStatus(t, code, http.StatusConflict, body)
	detail(t, body, "手机号已存在")
}

func TestUsersCreateDefaultPasswordIsPhoneSuffix(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("913"), "admin", "secret123")
	phone := uniquePhone("914")

	code, body := e.doJSON(t, http.MethodPost, "/api/users", map[string]any{
		"phone": phone, "role": "artist",
	}, dirTok)
	assertStatus(t, code, http.StatusCreated, body)

	if code, _ := e.login(t, phone, phone[len(phone)-6:]); code != http.StatusOK {
		t.Errorf("未指定密码时应以手机号后6位为默认密码登录")
	}
}

func TestUsersSetStatus(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("915"), "admin", "secret123")
	target := e.seedUser(t, uniquePhone("916"), "editor", "secret123")

	code, body := e.doJSON(t, http.MethodPatch, "/api/users/"+target+"/status",
		map[string]bool{"is_active": false}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["is_active"] == true {
		t.Errorf("is_active 应为 false: %v", body)
	}

	_, body = e.doJSON(t, http.MethodPatch, "/api/users/00000000-0000-0000-0000-000000000000/status",
		map[string]bool{"is_active": true}, dirTok)
	detail(t, body, "User not found")
}

func TestUsersResetPassword(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("917"), "admin", "secret123")
	phone := uniquePhone("918")
	target := e.seedUser(t, phone, "artist", "secret123")

	// 不传密码 → 默认手机号后6位
	code, body := e.doJSON(t, http.MethodPost, "/api/users/"+target+"/reset-password",
		map[string]any{"password": nil}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if temp, _ := body["temporary_password"].(string); temp != phone[len(phone)-6:] {
		t.Fatalf("temporary_password=%q, 应为 %q", temp, phone[len(phone)-6:])
	}

	// 传密码 → 指定临时密码
	code, body = e.doJSON(t, http.MethodPost, "/api/users/"+target+"/reset-password",
		map[string]string{"password": "newsecret"}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["temporary_password"] != "newsecret" {
		t.Fatalf("explicit temporary_password=%v", body["temporary_password"])
	}
	if code, _ := e.login(t, phone, "newsecret"); code != http.StatusOK {
		t.Errorf("reset 后应可登录")
	}

	_, body = e.doJSON(t, http.MethodPost, "/api/users/00000000-0000-0000-0000-000000000000/reset-password",
		map[string]any{"password": nil}, dirTok)
	detail(t, body, "User not found")
}

// ---- token 声明契约 ----

func TestTokensShareSessionExpiry(t *testing.T) {
	e := newEnv(t)
	phone := uniquePhone("919")
	e.seedUser(t, phone, "director", "secret123")
	code, body := e.login(t, phone, "secret123")
	assertStatus(t, code, http.StatusOK, body)
	exp, _ := body["session_expires_at"].(float64)

	tapHeader, tapClaims := decodeJWS(t, body["tapcanvas_token"].(string))
	if tapHeader["alg"] != "HS256" || tapHeader["typ"] != "JWT" {
		t.Errorf("tapcanvas header: %v", tapHeader)
	}
	if got := tapClaims["exp"].(float64); got != exp {
		t.Errorf("tap exp=%v != session %v", got, exp)
	}
	if got := tapClaims["sub"].(string); !strings.HasPrefix(got, "cineforge:") {
		t.Errorf("tap sub=%q 缺 cineforge: 前缀", got)
	}
	if tapClaims["role"] != "platformAdmin" {
		t.Errorf("director 应映射 platformAdmin, got %v", tapClaims["role"])
	}

	platHeader, platClaims := decodeJWS(t, body["platform_token"].(string))
	if platHeader["alg"] != "RS256" || platHeader["kid"] != e.cfg.Auth.PlatformJWTKID {
		t.Errorf("platform header: %v", platHeader)
	}
	if got := platClaims["exp"].(float64); got != exp {
		t.Errorf("platform exp=%v != session %v", got, exp)
	}
	if platClaims["iss"] != "cine-forge-platform" || platClaims["aud"] != "cine-forge-go" {
		t.Errorf("platform iss/aud: %v", platClaims)
	}
	u, _ := body["user"].(map[string]any)
	uid, _ := u["id"].(string)
	if platClaims["sub"] != "cineforge:"+uid {
		t.Errorf("platform sub: %v", platClaims["sub"])
	}
	if platClaims["tenant_id"] != "default" {
		t.Errorf("platform tenant_id: %v", platClaims["tenant_id"])
	}
	if platClaims["jti"] == "" {
		t.Errorf("platform jti 应非空")
	}

	claims, err := auth.VerifyAccessToken(e.cfg.Auth.Secret, body["access_token"].(string))
	if err != nil {
		t.Fatalf("verify access: %v", err)
	}
	if claims.Exp != int64(exp) || claims.Role != "director" {
		t.Errorf("access claims: %+v", claims)
	}
}

func TestSessionTTLCapAtEightHours(t *testing.T) {
	e := newEnv(t)
	phone := uniquePhone("920")
	e.seedUser(t, phone, "editor", "secret123")
	_, body := e.login(t, phone, "secret123")
	exp, _ := body["session_expires_at"].(float64)
	ttl := exp - float64(time.Now().Unix())
	if ttl > float64(auth.MaxSessionSeconds)+99 {
		t.Fatalf("session 不得超过 8h: ttl=%v", ttl)
	}
}

// ---- 内部辅助 ----

func decodeJWS(t *testing.T, tok string) (map[string]any, map[string]any) {
	t.Helper()
	seg := strings.Split(tok, ".")
	if len(seg) != 3 {
		t.Fatalf("jws 段数: %d", len(seg))
	}
	var h, p map[string]any
	hb, err := base64.RawURLEncoding.DecodeString(seg[0])
	if err != nil {
		t.Fatalf("header b64: %v", err)
	}
	pb, err := base64.RawURLEncoding.DecodeString(seg[1])
	if err != nil {
		t.Fatalf("payload b64: %v", err)
	}
	if err := json.Unmarshal(hb, &h); err != nil {
		t.Fatalf("header: %v", err)
	}
	if err := json.Unmarshal(pb, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	return h, p
}

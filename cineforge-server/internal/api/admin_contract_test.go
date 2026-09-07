package api_test

// P3g admin / 通知 / agents 静态端点 HTTP 契约测试（对齐 legacy app/api/routes/admin.py +
// tasks.py 的 workspace 通知 + agents.py 的 hermes 静态端点）：
//   - GET /workspace/notifications + POST /workspace/notifications/read-all（本人作用域、updated 行数）
//   - GET /admin/overview（stats 短键+extended 键、overdue_tasks、recent_activities、recent_readings）403 逐字
//   - GET /admin/probe（27 张表 counts + recent_* 三元组）
//   - GET /agents/hermes/config + /agents/hermes/health（静态配置派生，无鉴权对齐 legacy）
// 需要 dev DB + .env 的 auth secret（缺失自动 skip，复用 newEnv）。

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"cineforge/server/internal/api"
	"cineforge/server/internal/auth"
	"cineforge/server/internal/testutil"
)

// seedNotification 直接写 dev DB 插入一条通知。
func (e *testEnv) seedNotification(t *testing.T, userID, title, ntype string, isRead bool) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO notifications (id, user_id, title, content, type, is_read, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,now(),now())`,
		id, userID, title, "测试通知内容-"+id[:6], ntype, isRead); err != nil {
		t.Fatalf("seed notification: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM notifications WHERE id = $1`, id)
	})
	return id
}

// seedOverdue 把待办任务 due_at 置为过去（触发逾期统计）。
func (e *testEnv) markTaskOverdue(t *testing.T, taskID string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE tasks SET due_at = now() - interval '1 hour' WHERE id = $1`, taskID); err != nil {
		t.Fatalf("mark task overdue: %v", err)
	}
}

// seedReadingRun 直接写 dev DB 插入一条剧本围读 agent run。
func (e *testEnv) seedReadingRun(t *testing.T, projectID, episodeID string) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO agent_runs (id, project_id, episode_id, agent_type, status, input_json, version_no, data_state, duration_ms, skill_name, created_at, updated_at)
		 VALUES ($1,$2,$3,'script_reading','succeeded','{}',1,'final',1500,'script-reading-skill',now(),now())`,
		id, projectID, episodeID); err != nil {
		t.Fatalf("seed reading run: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM agent_runs WHERE id = $1`, id)
	})
	return id
}

// ---- 通知读端 ----

func TestWorkspaceNotificationsReadMarkAll(t *testing.T) {
	e := newEnv(t)
	userID, token := e.seedUserAsID(t, uniquePhone("notify"), "artist", "test-pass-1")

	// 一条未读 + 一条已读；read-all 只统计未读。
	n1 := e.seedNotification(t, userID, "改派通知", "task_rework", false)
	e.seedNotification(t, userID, "已读通知", "task_approved", true)

	code, raw := e.do(t, http.MethodGet, "/api/workspace/notifications", nil, token)
	if code != http.StatusOK {
		t.Fatalf("list notifications code=%d raw=%s", code, raw)
	}
	items := arrData(t, raw)
	if len(items) != 2 {
		t.Fatalf("notifications 应有 2 条，实际 %d: %v", len(items), items)
	}
	ids := map[string]bool{}
	for _, it := range items {
		ids[it["id"].(string)] = true
		if !it["is_read"].(bool) && it["title"] == "已读通知" {
			t.Fatalf("已读通知被错误返回为未读: %v", it)
		}
		if it["content"] == "" || it["type"] == "" || it["created_at"] == "" {
			t.Fatalf("通知缺字段: %v", it)
		}
	}
	if !ids[n1] {
		t.Fatalf("通知列表缺未读通知: ids=%v items=%v", ids, items)
	}

	// 未登录 → 401。
	c401, b401 := e.doJSON(t, http.MethodGet, "/api/workspace/notifications", nil, "")
	assertStatus(t, c401, http.StatusUnauthorized, b401)

	// read-all：仅 1 条未读。
	code, body := e.doJSON(t, http.MethodPost, "/api/workspace/notifications/read-all", nil, token)
	assertStatus(t, code, http.StatusOK, body)
	if upd, _ := body["updated"].(float64); int(upd) != 1 {
		t.Fatalf("updated=%v 应为 1", body["updated"])
	}

	// 再读：全部已读。
	code, raw = e.do(t, http.MethodGet, "/api/workspace/notifications", nil, token)
	items = arrData(t, raw)
	for _, it := range items {
		if !it["is_read"].(bool) {
			t.Fatalf("read-all 后仍有未读: %v", it)
		}
	}
}

// ---- /admin/overview ----

func TestAdminOverviewShapeAnd403(t *testing.T) {
	e := newEnv(t)
	_, adminToken := e.seedUserAsID(t, uniquePhone("adminov"), "admin", "test-pass-1")

	_, directorToken := e.seedUserAsID(t, uniquePhone("adminov2"), "director", "test-pass-1")

	code, body := e.doJSON(t, http.MethodGet, "/api/admin/overview", nil, adminToken)
	assertStatus(t, code, http.StatusOK, body)

	stats, ok := body["stats"].(map[string]any)
	if !ok {
		t.Fatalf("overview 缺 stats: %v", body)
	}
	for _, k := range []string{"users", "projects", "tasks", "overdue", "assets", "submissions", "agentRuns"} {
		if _, ok := stats[k]; !ok {
			t.Fatalf("stats 缺短键 %s: %v", k, stats)
		}
	}
	// extended 键 = 短键别名。
	pairs := [][2]string{
		{"user_count", "users"}, {"active_project_count", "projects"},
		{"total_task_count", "tasks"}, {"overdue_task_count", "overdue"},
		{"asset_count", "assets"}, {"submission_count", "submissions"},
		{"agent_run_count", "agentRuns"},
	}
	for _, p := range pairs {
		short, _ := stats[p[1]].(float64)
		ext, _ := stats[p[0]].(float64)
		if int(short) != int(ext) {
			t.Fatalf("stats[%s]=%v 应与 stats[%s]=%v 一致", p[0], ext, p[1], short)
		}
	}
	if acts, ok := body["recent_activities"].([]any); !ok || len(acts) != 2 {
		t.Fatalf("recent_activities 应含 2 条: %v", body["recent_activities"])
	}
	for _, need := range []string{"管理概览已接入 PostgreSQL", "任务、资产、提交统计来自真实数据库"} {
		found := false
		for _, a := range body["recent_activities"].([]any) {
			if a == need {
				found = true
			}
		}
		if !found {
			t.Fatalf("recent_activities 缺 %q: %v", need, body["recent_activities"])
		}
	}
	if _, ok := body["overdue_tasks"].([]any); !ok {
		t.Fatalf("overdue_tasks 应为数组: %v", body["overdue_tasks"])
	}
	if _, ok := body["recent_readings"].([]any); !ok {
		t.Fatalf("recent_readings 应为数组: %v", body["recent_readings"])
	}

	// director 也可以访问。
	code, _ = e.doJSON(t, http.MethodGet, "/api/admin/overview", nil, directorToken)
	assertStatus(t, code, http.StatusOK, body)

	// 非导演/管理员 → 403 逐字。
	_, artistToken := e.seedUserAsID(t, uniquePhone("adminov3"), "artist", "test-pass-1")
	code, b403 := e.doJSON(t, http.MethodGet, "/api/admin/overview", nil, artistToken)
	assertStatus(t, code, http.StatusForbidden, b403)
	detail(t, b403, "Director or admin permission required")
}

func TestAdminOverviewOverdueAndReadings(t *testing.T) {
	e := newEnv(t)
	_, adminToken := e.seedUserAsID(t, uniquePhone("adminov4"), "admin", "test-pass-1")

	pid := e.seedProject(t, "总控看板逾期项目", "", seedProjectOpts{})
	epID := e.seedEpisode(t, pid, "EP01", 1)
	e.seedReadingRun(t, pid, epID)
	taskID := e.seedTask(t, pid, "", "asset", "todo", seedTaskOpts{})
	e.markTaskOverdue(t, taskID)

	code, body := e.doJSON(t, http.MethodGet, "/api/admin/overview", nil, adminToken)
	assertStatus(t, code, http.StatusOK, body)

	stats := body["stats"].(map[string]any)
	if v, _ := stats["overdue"].(float64); int(v) < 1 {
		t.Fatalf("overdue 应 >=1: %v", stats["overdue"])
	}

	overdue := body["overdue_tasks"].([]any)
	if len(overdue) == 0 {
		t.Fatalf("overdue_tasks 应非空: %v", body["overdue_tasks"])
	}
	found := false
	for _, ot := range overdue {
		if m, ok := ot.(map[string]any); ok && m["id"] == taskID {
			found = true
		}
	}
	if !found {
		t.Fatalf("overdue_tasks 缺任务 %s: %v", taskID, overdue)
	}

	readings := body["recent_readings"].([]any)
	if len(readings) == 0 {
		t.Fatalf("recent_readings 应非空: %v", body["recent_readings"])
	}
	r0 := readings[0].(map[string]any)
	if r0["name"] != "剧本围读_EP01" {
		t.Fatalf("reading name=%v 应为 剧本围读_EP01", r0["name"])
	}
	if !strings.Contains(r0["meta"].(string), "1s") || !strings.Contains(r0["meta"].(string), "围读完成") {
		t.Fatalf("reading meta=%v 应含 1s 与 围读完成", r0["meta"])
	}
	if r0["status"] != "succeeded" || r0["episodeCode"] != "EP01" || r0["projectId"] != pid {
		t.Fatalf("reading struct=%v", r0)
	}
	if r0["title"] != "总控看板逾期项目" || r0["genre"] != "测试" {
		t.Fatalf("reading 项目信息=%v", r0)
	}
	if r0["created_at"] == "" {
		t.Fatalf("reading 缺 created_at: %v", r0)
	}
}

// ---- /admin/probe ----

func TestAdminProbe(t *testing.T) {
	e := newEnv(t)
	_, adminToken := e.seedUserAsID(t, uniquePhone("probe"), "admin", "test-pass-1")

	code, body := e.doJSON(t, http.MethodGet, "/api/admin/probe", nil, adminToken)
	assertStatus(t, code, http.StatusOK, body)
	if body["status"] != "ok" || body["generated_at"] == "" {
		t.Fatalf("probe 顶层字段: %v", body)
	}
	counts, ok := body["counts"].(map[string]any)
	if !ok {
		t.Fatalf("probe 缺 counts: %v", body)
	}
	expectTables := []string{
		"users", "projects", "project_episodes", "scripts", "script_versions", "script_segments",
		"storyboards", "storyboard_versions", "tasks", "task_prompts", "submissions", "assets",
		"project_assets", "project_asset_lists", "project_asset_list_items", "asset_versions",
		"asset_relations", "image_outputs", "video_outputs", "final_outputs", "files", "agent_runs",
		"agent_feedback", "agent_training_samples", "script_breakdowns", "entity_versions",
		"operation_logs",
	}
	if len(counts) != len(expectTables) {
		t.Fatalf("counts 应含 %d 张表，实际 %d: %v", len(expectTables), len(counts), counts)
	}
	for _, tbl := range expectTables {
		if _, ok := counts[tbl]; !ok {
			t.Fatalf("counts 缺表 %s: %v", tbl, counts)
		}
	}
	for _, key := range []string{"recent_projects", "recent_agent_runs", "recent_operation_logs"} {
		if _, ok := body[key].([]any); !ok {
			t.Fatalf("probe 缺 %s 数组: %v", key, body)
		}
	}

	// 员工页面调用方登录态才可访问。
	_, artistToken := e.seedUserAsID(t, uniquePhone("probe2"), "artist", "test-pass-1")
	c403, _ := e.doJSON(t, http.MethodGet, "/api/admin/probe", nil, artistToken)
	assertStatus(t, c403, http.StatusForbidden, nil)
}

// ---- /agents/hermes 静态端点 ----

func TestHermesStaticEndpoints(t *testing.T) {
	e := newEnv(t)

	// config（无鉴权，对齐 legacy）：默认 enabled + 占位 key → api_key_configured=false。
	code, body := e.doJSON(t, http.MethodGet, "/api/agents/hermes/config", nil, "")
	assertStatus(t, code, http.StatusOK, body)
	for _, k := range []string{"enabled", "base_url", "health_url", "model", "dashboard_url", "webui_url", "api_key_configured"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("hermes config 缺 %s: %v", k, body)
		}
	}
	if enabled, _ := body["enabled"].(bool); !enabled {
		t.Fatalf("默认 hermes 应 enabled: %v", body)
	}
	if akc, _ := body["api_key_configured"].(bool); akc {
		t.Fatalf("占位 key 应 api_key_configured=false: %v", body)
	}
	if body["base_url"] == "" || body["health_url"] == "" || body["model"] == "" {
		t.Fatalf("hermes config 必填字段为空: %v", body)
	}

	// health（enabled 默认 true，静态派生 unreachable）。
	code, body = e.doJSON(t, http.MethodGet, "/api/agents/hermes/health", nil, "")
	assertStatus(t, code, http.StatusOK, body)
	if body["status"] != "unreachable" || body["enabled"] != true || body["base_url"] == "" {
		t.Fatalf("hermes health 静态派生错误: %v", body)
	}
	if body["error"] == nil || body["error"] == "" {
		t.Fatalf("hermes health unreachable 应带 error: %v", body)
	}

	// disabled 分支：构造关闭的配置重建 router。
	pool := testutil.Pool(t)
	env := testutil.EnvMap(t)
	cfg := newTestConfig(t, env)
	cfg.Hermes.Enabled = false
	e2 := &testEnv{r: api.NewRouter(cfg, pool), pool: pool, cfg: cfg}
	code, body = e2.doJSON(t, http.MethodGet, "/api/agents/hermes/health", nil, "")
	assertStatus(t, code, http.StatusOK, body)
	if body["status"] != "disabled" {
		t.Fatalf("hermes disabled 应 status=disabled: %v", body)
	}
	code, body = e2.doJSON(t, http.MethodGet, "/api/agents/hermes/config", nil, "")
	if enabled, _ := body["enabled"].(bool); enabled {
		t.Fatalf("disabled 配置应 enabled=false: %v", body)
	}
}

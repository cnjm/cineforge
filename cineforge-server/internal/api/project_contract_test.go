package api_test

// P3d 项目域 HTTP 契约测试（对齐 legacy app/api/routes/projects.py + production_brief.py +
// agents.py agent-runs 子路由）：
//   - 导入校验：400 扩展名 / 413 超限 / 503 归档器未接线 / 403 角色
//   - 项目 CRUD + 可见性 404/403
//   - 分集列表 breakdown_status、分集详情版本列表
//   - 五步拆解：reading 202 queued、幂等 202、资产前置闸口 400、围读确认后 409
//   - 草稿保存：乐观锁、确认态围读追加人类 revision、保存后再跑 reading → 409
//   - dashboard 聚合、production-brief catalog、agent-runs 列表/详情/权限
// 需要 dev DB + .env 的 auth secret（缺失自动 skip，复用 newEnv）。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/testutil"
)

// validProjectBrief 对齐 legacy ProjectProductionBrief 校验的最简合法简报。
const validProjectBrief = `{"schema_version":"ProjectProductionBrief.v1","content_type":"animated_drama",` +
	`"delivery_aspect_ratio":"9:16","primary_style_id":"anime_2d_cel",` +
	`"cultural_contexts":[{"context_code":"CN-MODERN","name":"现代中国","region":"中国大陆",` +
	`"era":"当代","world_type":"real_world"}],"primary_cultural_context_code":"CN-MODERN"}`

func briefQuery() string { return url.QueryEscape(validProjectBrief) }

// uniquePrefix 生成测试专属 4 字符项目缩写，避免 dev DB 残留冲突（project_prefix 唯一约束）。
func uniquePrefix() string {
	id, err := auth.NewUUID()
	if err != nil {
		return "PTST"
	}
	return "P" + strings.ToUpper(id[:3])
}

// ---- 种子辅助（直接写 dev DB，模拟 legacy 检查点已确认状态）----

type seedProjectOpts struct {
	scriptText      string
	productionBrief string
}

func (e *testEnv) seedProject(t *testing.T, title, _ string, opts seedProjectOpts) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	var brief any
	if opts.productionBrief != "" {
		_ = json.Unmarshal([]byte(opts.productionBrief), &brief)
	}
	ctx := context.Background()
	unique := strings.ToUpper(id[:4])
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO projects (id, project_no, project_prefix, name, title, genre, status, current_stage, script_text, production_brief, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,'draft','draft',$7,$8,now(),now())`,
		id, "RF-TEST-"+strings.ToUpper(id[:6]), "T"+unique, title, title, "测试",
		opts.scriptText, brief); err != nil {
		t.Fatalf("seed project (%s): %v", title, err)
	}
	e.cleanupProject(t, id)
	return id
}

// cleanupProject 按依赖序删除项目及其引用行（操作日志/拆解/运行/剧本等），供 API 创建的项目使用。
// 幂等：未写过的表 DELETE 影响零行。
func (e *testEnv) cleanupProject(t *testing.T, id string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := e.pool.Exec(ctx, `UPDATE agent_runs SET workflow_run_id = NULL WHERE project_id = $1`, id); err != nil {
			t.Errorf("cleanup project %s: %v", id, err)
		}
		if _, err := e.pool.Exec(ctx, `UPDATE workflow_runs SET agent_run_id = NULL WHERE project_id = $1`, id); err != nil {
			t.Errorf("cleanup project %s: %v", id, err)
		}
		for _, q := range []string{
			`DELETE FROM operation_logs WHERE project_id = $1`,
			`DELETE FROM script_breakdowns WHERE project_id = $1`,
			`DELETE FROM episode_asset_bindings WHERE project_id = $1`,
			`DELETE FROM asset_completion_records WHERE project_id = $1`,
			`DELETE FROM asset_versions WHERE asset_id IN (SELECT id FROM assets WHERE project_id = $1)`,
			`DELETE FROM artifact_revisions WHERE project_id = $1`,
			`DELETE FROM agent_runs WHERE project_id = $1`,
			`DELETE FROM workflow_runs WHERE project_id = $1`,
			`DELETE FROM task_prompts WHERE task_id IN (SELECT id FROM tasks WHERE project_id = $1)`,
			`DELETE FROM task_dependencies WHERE task_id IN (SELECT id FROM tasks WHERE project_id = $1) OR depends_on_task_id IN (SELECT id FROM tasks WHERE project_id = $1)`,
			`DELETE FROM submissions WHERE task_id IN (SELECT id FROM tasks WHERE project_id = $1)`,
			`DELETE FROM task_submission_batches WHERE task_id IN (SELECT id FROM tasks WHERE project_id = $1)`,
			`DELETE FROM asset_variant_storyboard_links WHERE variant_plan_id IN (SELECT id FROM asset_variant_plans WHERE project_id = $1)`,
			`DELETE FROM asset_variant_plans WHERE project_id = $1`,
			`DELETE FROM tasks WHERE project_id = $1`,
			`DELETE FROM files WHERE project_id = $1`,
			`DELETE FROM assets WHERE project_id = $1`,
			`DELETE FROM storyboard_versions WHERE storyboard_id IN (SELECT id FROM storyboards WHERE project_id = $1)`,
			`DELETE FROM storyboards WHERE project_id = $1`,
			`DELETE FROM agent_training_samples WHERE project_id = $1`,
			`DELETE FROM entity_versions WHERE project_id = $1`,
			`DELETE FROM script_segments WHERE project_id = $1`,
			`DELETE FROM script_versions WHERE script_id IN (SELECT id FROM scripts WHERE project_id = $1)`,
			`DELETE FROM scripts WHERE project_id = $1`,
			`DELETE FROM project_episodes WHERE project_id = $1`,
			`DELETE FROM projects WHERE id = $1`,
		} {
			if _, err := e.pool.Exec(ctx, q, id); err != nil {
				t.Errorf("cleanup project %s: %v", id, err)
			}
		}
	})
}

// cleanupProjectByTitle 删除服务端创建的、无法拿到 id 的残留项目（按 title 精确匹配）。
func (e *testEnv) cleanupProjectByTitle(t *testing.T, title string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		var ids []string
		if rows, err := e.pool.Query(ctx, `SELECT id FROM projects WHERE title = $1`, title); err == nil {
			for rows.Next() {
				var id string
				_ = rows.Scan(&id)
				ids = append(ids, id)
			}
			rows.Close()
		}
		for _, id := range ids {
			_, _ = e.pool.Exec(ctx, `UPDATE agent_runs SET workflow_run_id = NULL WHERE project_id = $1`, id)
			_, _ = e.pool.Exec(ctx, `UPDATE workflow_runs SET agent_run_id = NULL WHERE project_id = $1`, id)
			for _, q := range []string{
				`DELETE FROM operation_logs WHERE project_id = $1`,
				`DELETE FROM script_breakdowns WHERE project_id = $1`,
				`DELETE FROM artifact_revisions WHERE project_id = $1`,
				`DELETE FROM agent_runs WHERE project_id = $1`,
				`DELETE FROM workflow_runs WHERE project_id = $1`,
				`DELETE FROM task_prompts WHERE task_id IN (SELECT id FROM tasks WHERE project_id = $1)`,
				`DELETE FROM task_dependencies WHERE task_id IN (SELECT id FROM tasks WHERE project_id = $1) OR depends_on_task_id IN (SELECT id FROM tasks WHERE project_id = $1)`,
				`DELETE FROM submissions WHERE task_id IN (SELECT id FROM tasks WHERE project_id = $1)`,
				`DELETE FROM task_submission_batches WHERE task_id IN (SELECT id FROM tasks WHERE project_id = $1)`,
				`DELETE FROM asset_variant_storyboard_links WHERE variant_plan_id IN (SELECT id FROM asset_variant_plans WHERE project_id = $1)`,
				`DELETE FROM asset_variant_plans WHERE project_id = $1`,
				`DELETE FROM tasks WHERE project_id = $1`,
				`DELETE FROM episode_asset_bindings WHERE project_id = $1`,
				`DELETE FROM asset_versions WHERE asset_id IN (SELECT id FROM assets WHERE project_id = $1)`,
				`DELETE FROM asset_completion_records WHERE project_id = $1`,
				`DELETE FROM files WHERE project_id = $1`,
				`DELETE FROM assets WHERE project_id = $1`,
				`DELETE FROM storyboard_versions WHERE storyboard_id IN (SELECT id FROM storyboards WHERE project_id = $1)`,
				`DELETE FROM storyboards WHERE project_id = $1`,
				`DELETE FROM agent_training_samples WHERE project_id = $1`,
				`DELETE FROM entity_versions WHERE project_id = $1`,
				`DELETE FROM script_segments WHERE project_id = $1`,
				`DELETE FROM script_versions WHERE script_id IN (SELECT id FROM scripts WHERE project_id = $1)`,
				`DELETE FROM scripts WHERE project_id = $1`,
				`DELETE FROM project_episodes WHERE project_id = $1`,
				`DELETE FROM projects WHERE id = $1`,
			} {
				_, _ = e.pool.Exec(ctx, q, id)
			}
		}
	})
}

func (e *testEnv) seedEpisode(t *testing.T, projectID, code string, no int32) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO project_episodes (id, project_id, episode_no, episode_code, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,now(),now())`, id, projectID, no, code); err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM project_episodes WHERE id = $1`, id)
	})
	return id
}

func (e *testEnv) seedScript(t *testing.T, projectID, episodeID, content string) string {
	t.Helper()
	scriptID, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	versionID, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	scriptCode := "SC-" + strings.ToUpper(versionID[:6])
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO scripts (id, project_id, episode_id, script_code, title, content, status, current_version_id, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,'imported',$7,now(),now())`,
		scriptID, projectID, episodeID, scriptCode, "测试剧本", content, versionID); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO script_versions (id, script_id, version_no, source, content, content_hash, original_filename, parser_name, language, created_at, updated_at)
		 VALUES ($1,$2,1,'import',$3,encode(sha256(convert_to($3,'UTF8')),'hex'),'script.txt','plain','zh',now(),now())`,
		versionID, scriptID, content); err != nil {
		t.Fatalf("seed script version: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM script_versions WHERE id = $1`, versionID)
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM scripts WHERE id = $1`, scriptID)
	})
	return versionID
}

// consumePendingBreakdownRuns 模拟 worker 执行完毕：把指定分集作用域下 queued 的
// script_breakdown 运行置为 succeeded，使 PendingAgentRun 不再短路闸口（测试进入后续闸口断言）。
func (e *testEnv) consumePendingBreakdownRuns(t *testing.T, projectID, episodeID string) {
	t.Helper()
	rows, err := e.pool.Exec(context.Background(),
		`UPDATE agent_runs SET status='succeeded', updated_at=now()
		 WHERE project_id=$1 AND episode_id=$2
		   AND agent_type='script_breakdown' AND status='queued' AND parent_run_id IS NULL`,
		projectID, episodeID)
	if err != nil {
		t.Fatalf("consume pending runs: %v", err)
	}
	if rows.RowsAffected() == 0 {
		t.Fatalf("consume pending runs: 无 queued 运行可消费")
	}
}

// ---- 导入校验 ----

func TestImportValidationBadExtension(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("911"), "director", "secret123")
	code, body := e.doRaw(t, http.MethodPost,
		"/api/projects/import?title=测试项目&project_prefix=TEST&production_brief=%7B%7D&filename=script.pdf",
		[]byte("你好世界"), "application/octet-stream", token)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "剧本导入仅支持 .txt、.md、.docx 文件。")
}

func TestImportValidationOversize(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("912"), "director", "secret123")
	big := bytes.Repeat([]byte("a"), 21*1024*1024)
	code, body := e.doRaw(t, http.MethodPost,
		"/api/projects/import?title=超大项目&project_prefix=BIG&filename=script.txt",
		big, "application/octet-stream", token)
	assertStatus(t, code, http.StatusRequestEntityTooLarge, body)
	detail(t, body, "剧本文件不能超过 20MB。")
}

func TestImportArchiverUnavailableReturns503(t *testing.T) {
	// 强制 MinIO 未配置 → NewRouter 降级不可用归档器（隔离，不依赖 dev MinIO）。
	e := newEnvWithMinIO(t, false)
	token := e.seedUserAs(t, uniquePhone("913"), "director", "secret123")
	title := "导入测试" + uniquePhone("93")
	e.cleanupProjectByTitle(t, title)
	body := []byte("第一幕。\n角色：小明。\n小明说：【去看看。】\n")
	code, resp := e.doRaw(t, http.MethodPost,
		"/api/projects/import?title="+url.QueryEscape(title)+"&project_prefix="+uniquePrefix()+"&production_brief="+briefQuery()+"&filename=script.txt",
		body, "application/octet-stream", token)
	assertStatus(t, code, http.StatusServiceUnavailable, resp)
	detail(t, resp, "剧本原文件归档失败，未创建正式剧本版本，请稍后重试。")
}

// TestImportWithRealMinIO 真实归档成功路径：201 + files 登记 + script_versions.source_file_id。
// 依赖 dev MinIO 可达且 .env 配置凭证；否则 t.Skip（不成为 CI 阻塞）。
func TestImportWithRealMinIO(t *testing.T) {
	env := testutil.EnvMap(t)
	if env["CINEFORGE_MINIO_ACCESS_KEY"] == "" {
		t.Skip("未配置 CINEFORGE_MINIO_ACCESS_KEY，跳过真实归档测试")
	}
	endpoint := env["CINEFORGE_MINIO_ENDPOINT"]
	if endpoint == "" {
		endpoint = "127.0.0.1:9000"
	}
	resp, err := http.Get("http://" + endpoint + "/minio/health/live")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Skipf("dev MinIO 不可达，跳过真实归档测试: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
	e := newEnvWithMinIO(t, true)
	token := e.seedUserAs(t, uniquePhone("914"), "director", "secret123")
	title := "真实归档" + uniquePhone("94")
	e.cleanupProjectByTitle(t, title)
	prefix := uniquePrefix()
	body := []byte("第一幕。\n角色：小明。\n小明说：【去看看。】\n")
	code, respBody := e.doRaw(t, http.MethodPost,
		"/api/projects/import?title="+url.QueryEscape(title)+"&project_prefix="+prefix+"&production_brief="+briefQuery()+"&filename=script.txt",
		body, "application/octet-stream", token)
	assertStatus(t, code, http.StatusCreated, respBody)

	latest, ok := respBody["latest_import"].(map[string]any)
	if !ok {
		t.Fatalf("latest_import 缺失: %v", respBody)
	}
	svID, _ := latest["script_version_id"].(string)
	if svID == "" {
		t.Fatalf("script_version_id 缺失: %v", latest)
	}

	// files 登记：project 归属、object_key 前缀、source_file_id 指向该文件。
	var fileID, objectKey, bucket string
	var fileName string
	ctx := context.Background()
	svRow := e.pool.QueryRow(ctx,
		`SELECT source_file_id FROM script_versions WHERE id = $1`, svID).Scan(&fileID)
	if svRow != nil {
		t.Fatalf("script_versions.source_file_id 查询失败: %v", svRow)
	}
	fRow := e.pool.QueryRow(ctx,
		`SELECT bucket, object_key, file_name FROM files WHERE id = $1`, fileID).Scan(&bucket, &objectKey, &fileName)
	if fRow != nil {
		t.Fatalf("files 登记缺失: %v", fRow)
	}
	wantPrefix := "script/imports/"
	if !strings.Contains(objectKey, wantPrefix) {
		t.Errorf("object_key=%q 期望包含 %q", objectKey, wantPrefix)
	}
	if !strings.HasPrefix(objectKey, prefix) {
		t.Errorf("object_key=%q 期望以项目缩写 %q 开头", objectKey, prefix)
	}
	if fileName != "script.txt" {
		t.Errorf("file_name=%q 期望 script.txt", fileName)
	}
	if bucket == "" {
		t.Errorf("bucket 为空")
	}
	// 清理：先摘除 script_versions 的 FK 再删 files 行（cleanupProject 只删 script_versions）。
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := e.pool.Exec(ctx, `UPDATE script_versions SET source_file_id = NULL WHERE id = $1`, svID); err != nil {
			t.Errorf("cleanup script_version fk %s: %v", svID, err)
		}
		if _, err := e.pool.Exec(ctx, `DELETE FROM files WHERE id = $1`, fileID); err != nil {
			t.Errorf("cleanup file %s: %v", fileID, err)
		}
	})
}

func TestImportRequiresDirectorOrAdmin(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("914"), "artist", "secret123")
	code, resp := e.doRaw(t, http.MethodPost,
		"/api/projects/import?title=越权项目&project_prefix=XSS&filename=script.txt",
		[]byte("hello"), "application/octet-stream", token)
	assertStatus(t, code, http.StatusForbidden, resp)
	detail(t, resp, "Director or admin permission required")
}

// ---- 项目 CRUD ----

func TestProjectCreateGetUpdateList(t *testing.T) {
	e := newEnv(t)
	director := e.seedUserAs(t, uniquePhone("915"), "director", "secret123")
	artist := e.seedUserAs(t, uniquePhone("916"), "artist", "secret123")

	// artist 不能创建
	code, body := e.doJSON(t, http.MethodPost, "/api/projects", map[string]any{"title": "越权"}, artist)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director or admin permission required")

	// director 创建 → 201
	var brief any
	_ = json.Unmarshal([]byte(validProjectBrief), &brief)
	code, body = e.doJSON(t, http.MethodPost, "/api/projects",
		map[string]any{"title": "测试项目", "project_prefix": uniquePrefix(), "production_brief": brief}, director)
	assertStatus(t, code, http.StatusCreated, body)
	projectID, _ := body["id"].(string)
	if projectID == "" {
		t.Fatalf("create project 缺 id: %v", body)
	}
	e.cleanupProject(t, projectID)

	// artist 列表（任意用户可读）→ 200（可见项目集合为空）
	code, body = e.doJSON(t, http.MethodGet, "/api/projects", nil, artist)
	assertStatus(t, code, http.StatusOK, body)

	// director get 项目 → 200
	code, body = e.doJSON(t, http.MethodGet, "/api/projects/"+projectID, nil, director)
	assertStatus(t, code, http.StatusOK, body)
	if body["title"] != "测试项目" {
		t.Errorf("title=%v", body["title"])
	}

	// artist（非成员）get 他人项目 → 403
	code, body = e.doJSON(t, http.MethodGet, "/api/projects/"+projectID, nil, artist)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Project permission denied")

	// update → director 200；artist 403
	code, body = e.doJSON(t, http.MethodPatch, "/api/projects/"+projectID,
		map[string]any{"title": "新标题"}, artist)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director or admin permission required")

	code, body = e.doJSON(t, http.MethodPatch, "/api/projects/"+projectID,
		map[string]any{"title": "新标题"}, director)
	assertStatus(t, code, http.StatusOK, body)
	if body["title"] != "新标题" {
		t.Errorf("update 后 title=%v", body["title"])
	}

	// 不存在项目 → 404
	code, body = e.doJSON(t, http.MethodGet, "/api/projects/00000000-0000-0000-0000-000000000000", nil, artist)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Project not found")
}

// ---- 分集 ----

func TestEpisodeListBreakdownStatus(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("917"), "director", "secret123")
	projectID := e.seedProject(t, "分集项目", "EPI", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	e.seedScript(t, projectID, episodeID, "第一幕。\n")

	code, raw := e.do(t, http.MethodGet, "/api/projects/"+projectID+"/episodes", nil, token)
	assertStatus(t, code, http.StatusOK, map[string]any{})
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("episodes 响应非数组: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("episodes=%v", string(raw))
	}
	item := items[0]
	if item["episode_code"] != "EP01" {
		t.Errorf("episode_code=%v", item["episode_code"])
	}
	if item["breakdown_status"] != "script_imported" {
		t.Errorf("breakdown_status=%v 期望 script_imported", item["breakdown_status"])
	}

	// 分集详情版本列表
	code, body := e.doJSON(t, http.MethodGet, fmt.Sprintf("/api/projects/%s/episodes/%s", projectID, episodeID), nil, token)
	assertStatus(t, code, http.StatusOK, body)
	versions, _ := body["versions"].([]any)
	if len(versions) != 1 {
		t.Fatalf("versions=%v", body["versions"])
	}
	v0 := versions[0].(map[string]any)
	if v0["version_no"] != float64(1) || v0["is_current"] != true {
		t.Errorf("version=%v", v0)
	}

	// 幽灵分集 → 404 "Episode not found"
	code, body = e.doJSON(t, http.MethodGet,
		fmt.Sprintf("/api/projects/%s/episodes/00000000-0000-0000-0000-000000000000", projectID), nil, token)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Episode not found")
}

// ---- 五步拆解：reading 203 幂等 + 资产前置闸口 ----

func TestBreakdownReadingStep202Idempotent(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("918"), "director", "secret123")
	projectID := e.seedProject(t, "拆解项目", "BRK", seedProjectOpts{scriptText: "第一幕。\n小明：【去看看。】\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n小明：【去看看。】\n")

	path := fmt.Sprintf("/api/projects/%s/breakdown-steps/reading?episode_id=%s&script_version_id=%s", projectID, episodeID, versionID)
	code, body := e.doJSON(t, http.MethodPost, path, map[string]any{}, token)
	assertStatus(t, code, http.StatusAccepted, body)
	if body["agent_type"] != "script_breakdown" {
		t.Errorf("agent_type=%v", body["agent_type"])
	}
	if body["status"] != "queued" {
		t.Errorf("status=%v", body["status"])
	}
	runID, _ := body["id"].(string)
	if runID == "" {
		t.Fatalf("run 缺 id: %v", body)
	}

	// 幂等：同作用域 pending run 直接返回同一 run，不再提交流程
	code, body = e.doJSON(t, http.MethodPost, path, map[string]any{}, token)
	assertStatus(t, code, http.StatusAccepted, body)
	if body["id"] != runID {
		t.Errorf("幂等失败：首次 id=%s 再次 id=%v", runID, body["id"])
	}
}

func TestBreakdownAssetGateRequiresReading(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("919"), "director", "secret123")
	projectID := e.seedProject(t, "拆解前置", "GATE", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")

	code, body := e.doJSON(t, http.MethodPost,
		fmt.Sprintf("/api/projects/%s/breakdown-steps/assets?episode_id=%s&script_version_id=%s", projectID, episodeID, versionID),
		map[string]any{}, token)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "请先确认围读结果，再运行资产拆解。")
}

func TestBreakdownUnknownStep(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("920"), "director", "secret123")
	projectID := e.seedProject(t, "未知步骤", "STEP", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")

	code, body := e.doJSON(t, http.MethodPost,
		fmt.Sprintf("/api/projects/%s/breakdown-steps/hocus?episode_id=%s&script_version_id=%s", projectID, episodeID, versionID),
		map[string]any{}, token)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "未知的拆解步骤")
}

func TestBreakdownNoScriptText(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("921"), "director", "secret123")
	projectID := e.seedProject(t, "无剧本", "NOSCRIPT", seedProjectOpts{})

	code, body := e.doJSON(t, http.MethodPost,
		"/api/projects/"+projectID+"/breakdown-steps/reading", map[string]any{}, token)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "项目暂无剧本文本，请先导入剧本。")
}

func TestBreakdownWriteRequiresMembership(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("922"), "director", "secret123")
	artist := e.seedUserAs(t, uniquePhone("923"), "artist", "secret123")
	projectID := e.seedProject(t, "权限项目", "PERM", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")

	code, body := e.doJSON(t, http.MethodPost,
		fmt.Sprintf("/api/projects/%s/breakdown-steps/reading?episode_id=%s&script_version_id=%s", projectID, episodeID, versionID),
		map[string]any{}, artist)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Project permission denied")
	_ = token
}

// ---- 围读确认 → 草稿保存 → reading 409 ----

func TestBreakdownSaveConfirmedReadingThenGate409(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("924"), "director", "secret123")
	projectID := e.seedProject(t, "确认链", "CONF", seedProjectOpts{scriptText: "第一幕。\n小明：【去看看。】\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n小明：【去看看。】\n")

	// 第一步：reading → 202 queued
	stepPath := fmt.Sprintf("/api/projects/%s/breakdown-steps/reading?episode_id=%s&script_version_id=%s", projectID, episodeID, versionID)
	code, body := e.doJSON(t, http.MethodPost, stepPath, map[string]any{}, token)
	assertStatus(t, code, http.StatusAccepted, body)

	// 第二步：人工确认围读并保存终稿 → 201，reading_review_state 落到 confirmed + revision id
	savePath := "/api/projects/" + projectID + "/breakdowns/current"
	payload := map[string]any{
		"episode_id": episodeID, "script_version_id": versionID,
		"episode_code":     "EP01",
		"expected_version": float64(0),
		"view": map[string]any{
			"schema_version":    "BreakdownWorkflow.v1",
			"episode_id":        episodeID,
			"script_version_id": versionID,
			"reading_review_state": map[string]any{
				"status": "confirmed", "confirmed_revision_id": nil, "confirmation_mode": "human_confirmed",
			},
			"asset_review_state": map[string]any{"status": "needs_review"},
			"reading_report": map[string]any{
				"episode_summary": "小明想去看看。", "logline": "出发", "core_conflict": "好奇 vs 安全",
				"synopsis": "小明决定出门。",
			},
		},
	}
	code, body = e.doJSON(t, http.MethodPost, savePath, payload, token)
	assertStatus(t, code, http.StatusCreated, body)

	// 模拟 worker 消费第 1 步排队的 reading run：否则 PendingAgentRun 短路闸口，POST 只会幂等返回同一 run。
	e.consumePendingBreakdownRuns(t, projectID, episodeID)

	// 第三步：围读已确认 → reading 409
	code, body = e.doJSON(t, http.MethodPost, stepPath, map[string]any{}, token)
	assertStatus(t, code, http.StatusConflict, body)
	detail(t, body, "围读结果已确认，不能重复运行剧本围读。")

	// 第四步：资产拆解在围读已确认后可以通过 reading 闸口 → 202（资产清单本身未确认）
	code, body = e.doJSON(t, http.MethodPost,
		fmt.Sprintf("/api/projects/%s/breakdown-steps/assets?episode_id=%s&script_version_id=%s", projectID, episodeID, versionID),
		map[string]any{}, token)
	assertStatus(t, code, http.StatusAccepted, body)

	// 第五步：GET current → reading_review_state confirmed + reading_report 回填
	code, body = e.doJSON(t, http.MethodGet, savePath+"?episode_id="+episodeID+"&script_version_id="+versionID, nil, token)
	assertStatus(t, code, http.StatusOK, body)
	if body == nil {
		t.Fatalf("GET current 返回 null")
	}
	view, _ := body["view"].(map[string]any)
	state, _ := view["reading_review_state"].(map[string]any)
	if state["status"] != "confirmed" {
		t.Errorf("reading_review_state=%v", state)
	}
	if _, ok := state["confirmed_revision_id"].(string); !ok {
		t.Errorf("confirm 后应带 confirmed_revision_id: %v", state)
	}
	report, _ := view["reading_report"].(map[string]any)
	if report["episode_summary"] != "小明想去看看。" {
		t.Errorf("GET current reading_report=%v", report)
	}
}

func TestBreakdownSaveOptimisticLockConflict(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("925"), "director", "secret123")
	projectID := e.seedProject(t, "乐观锁", "LOCK", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")

	savePath := "/api/projects/" + projectID + "/breakdowns/current"
	payload := map[string]any{
		"episode_id": episodeID, "script_version_id": versionID,
		"expected_version": float64(0),
		"view": map[string]any{
			"schema_version":       "BreakdownWorkflow.v1",
			"episode_id":           episodeID,
			"script_version_id":    versionID,
			"reading_review_state": map[string]any{"status": "pending_confirmation"},
			"asset_review_state":   map[string]any{"status": "needs_review"},
		},
	}
	code, body := e.doJSON(t, http.MethodPost, savePath, payload, token)
	assertStatus(t, code, http.StatusCreated, body)

	// 版本已升至 1；继续用 expected_version=0 保存 → 409 乐观锁
	code, body = e.doJSON(t, http.MethodPost, savePath, payload, token)
	assertStatus(t, code, http.StatusConflict, body)
	detail(t, body, "拆解草稿已被其他保存更新")
}

// ---- dashboard / catalog / agent-runs ----

func TestProjectDashboard(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("926"), "director", "secret123")
	projectID := e.seedProject(t, "看板项目", "DASH", seedProjectOpts{scriptText: "第一幕。\n"})
	e.seedEpisode(t, projectID, "EP01", 1)

	code, body := e.doJSON(t, http.MethodGet, "/api/projects/"+projectID+"/dashboard", nil, token)
	assertStatus(t, code, http.StatusOK, body)
	for _, key := range []string{"project_id", "storyboard_count", "asset_count", "task_count", "completion_rate"} {
		if _, ok := body[key]; !ok {
			t.Errorf("dashboard 缺 %s: %v", key, body)
		}
	}
	if body["project_id"] != projectID {
		t.Errorf("project_id=%v", body["project_id"])
	}

	// 幽灵项目 → 404
	code, body = e.doJSON(t, http.MethodGet, "/api/projects/00000000-0000-0000-0000-000000000000/dashboard", nil, token)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Project not found")
}

func TestProductionBriefCatalog(t *testing.T) {
	e := newEnv(t)
	token := e.seedUserAs(t, uniquePhone("927"), "director", "secret123")
	code, body := e.doJSON(t, http.MethodGet, "/api/production-brief/catalog", nil, token)
	assertStatus(t, code, http.StatusOK, body)
	if _, ok := body["styles"]; !ok {
		t.Errorf("catalog 缺 styles: %v", body)
	}
}

func TestAgentRunsListAndGet(t *testing.T) {
	e := newEnv(t)
	director := e.seedUserAs(t, uniquePhone("928"), "director", "secret123")
	artist := e.seedUserAs(t, uniquePhone("929"), "artist", "secret123")
	projectID := e.seedProject(t, "运行记录", "RUNS", seedProjectOpts{scriptText: "第一幕。\n小明：【去看看。】\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n小明：【去看看。】\n")

	// 触发一次 reading 运行
	code, body := e.doJSON(t, http.MethodPost,
		fmt.Sprintf("/api/projects/%s/breakdown-steps/reading?episode_id=%s&script_version_id=%s", projectID, episodeID, versionID),
		map[string]any{}, director)
	assertStatus(t, code, http.StatusAccepted, body)
	runID, _ := body["id"].(string)

	// 列表（任意用户）→ 含该 run
	code, body = e.doJSON(t, http.MethodGet, "/api/agent-runs?project_id="+projectID+"&limit=10", nil, artist)
	assertStatus(t, code, http.StatusOK, body)
	items, _ := body["items"].([]any)
	found := false
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if m["id"] == runID {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("agent-runs 列表缺 run %s: %v", runID, body)
	}

	// 详情：项目成员可读 → 200，output 为 null
	code, body = e.doJSON(t, http.MethodGet, "/api/agent-runs/"+runID, nil, director)
	assertStatus(t, code, http.StatusOK, body)
	if body["id"] != runID || body["output"] != nil {
		t.Errorf("run detail=%v", body)
	}

	// 不存在 → 404
	code, body = e.doJSON(t, http.MethodGet, "/api/agent-runs/00000000-0000-0000-0000-000000000000", nil, director)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Agent run not found")
}

// ---- 手写 raw 请求辅助 ----

// do 发出 HTTP 请求并返回原始响应体（供数组等非对象响应断言）。
func (e *testEnv) do(t *testing.T, method, path string, body any, token string) (int, []byte) {
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
	return w.Code, w.Body.Bytes()
}

func (e *testEnv) doRaw(t *testing.T, method, path string, body []byte, contentType, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
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

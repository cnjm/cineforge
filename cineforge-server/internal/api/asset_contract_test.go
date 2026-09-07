package api_test

// P3f-c 资产域 HTTP 契约测试（对齐 legacy app/api/routes/assets.py + projects.py scene-options）：
//   - POST /assets：正式码分配、project_id 校验路径、角色守卫
//   - GET /assets、GET /assets/{id}：列表/详情权限 403、404
//   - GET /assets/library：project 404 / 403 / 空列表
//   - variant-plans：创建恒 400、列表、PATCH/DELETE 退役、404
//   - scene-options：human 终版修正 → 选项、权限 403、404
//   - temporary-production：未确认 400、成功 201 + 幂等 + 跨版本守卫
// 需要 dev DB + .env 的 auth secret（缺失自动 skip，复用 newEnv）。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"cineforge/server/internal/auth"
)

// ---- 资产种子辅助（直接写 dev DB）----

// seedConfirmedAssetInventory 插入 human confirmed final 的 asset_inventory 修订。
func (e *testEnv) seedConfirmedAssetInventory(t *testing.T, projectID, episodeID, scriptVersionID string) string {
	t.Helper()
	revID, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO artifact_revisions
			(id, project_id, artifact_id, artifact_type, version_no, source_type,
			 input_snapshot, raw_output, normalized_content, change_diff, content_hash,
			 status, data_state, created_at, updated_at)
		 VALUES ($1,$2,$1,'asset_inventory',1,'human',
		 	 jsonb_build_object('episode_id',$3::text,'script_version_id',$4::text),
			 '[]', '{"assets":[]}', '{}', 'test-hash', 'confirmed', 'final', now(), now())`,
		revID, projectID, episodeID, scriptVersionID); err != nil {
		t.Fatalf("seed asset inventory revision: %v", err)
	}
	return revID
}

// seedSceneAsset 插入带 human 修订的场景资产（scene-options 契约）。
func (e *testEnv) seedSceneAsset(t *testing.T, projectID string) (assetID, sceneCode string) {
	t.Helper()
	assetID, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	revID, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	sceneCode = "SC-Option-01"
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO artifact_revisions
			(id, project_id, artifact_id, artifact_type, version_no, source_type,
			 input_snapshot, raw_output, normalized_content, change_diff, content_hash,
			 status, data_state, created_at, updated_at)
		 VALUES ($1,$2,$1,'asset_inventory',1,'human','{}','{}','{}','{}',
		 	 'scene-hash','confirmed','final',now(),now())`,
		revID, projectID); err != nil {
		t.Fatalf("seed scene revision: %v", err)
	}
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, asset_code, asset_type, name, status, tags, metadata_json,
			version, current_revision_id, is_locked, created_at, updated_at)
		 VALUES ($1,$2,$3,'scene',$4,'active','[]','{}',1,$5,false,now(),now())`,
		assetID, projectID, sceneCode, "街景-01", revID); err != nil {
		t.Fatalf("seed scene asset: %v", err)
	}
	return assetID, sceneCode
}

func assetDetail(t *testing.T, body map[string]any, key string) string {
	t.Helper()
	v, _ := body[key].(string)
	return v
}

// ---- POST /assets ----

func TestAssetCreateFormalCodeAndRead(t *testing.T) {
	e := newEnv(t)
	projectID := e.seedProject(t, "资产正式码测试", "", seedProjectOpts{})
	director := e.seedUserAs(t, uniquePhone("1300"), "director", "test1234")

	code, body := e.doJSON(t, http.MethodPost, "/api/assets", map[string]any{
		"asset_type":  "character",
		"name":        "主角",
		"description": "第一位主角",
		"tags":        []string{"主角", "青年"},
		"metadata":    map[string]any{"project_id": projectID},
	}, director)
	if code != http.StatusCreated {
		t.Fatalf("create asset status=%d body=%v", code, body)
	}
	assetID := assetDetail(t, body, "id")
	if assetID == "" {
		t.Fatalf("missing asset id: %v", body)
	}
	if assetDetail(t, body, "project_id") != projectID {
		t.Fatalf("project_id mismatch: %v", body)
	}
	if code := assetDetail(t, body, "asset_code"); !strings.HasPrefix(code, "T") || !strings.HasSuffix(code, "-R001") {
		t.Fatalf("asset_code=%q 应为 {prefix}-R001", code)
	}
	if status := assetDetail(t, body, "status"); status != "draft" {
		t.Fatalf("status=%q want draft", status)
	}

	// GET /assets/{id}
	code, body = e.doJSON(t, http.MethodGet, "/api/assets/"+assetID, nil, director)
	if code != http.StatusOK || assetDetail(t, body, "id") != assetID {
		t.Fatalf("get asset status=%d body=%v", code, body)
	}
	// GET /assets?project_id=
	code, list := e.doList(t, http.MethodGet, "/api/assets?project_id="+projectID, director)
	if code != http.StatusOK {
		t.Fatalf("list assets status=%d body=%v", code, list)
	}
	if len(list) == 0 {
		t.Fatalf("list assets empty")
	}
	// GET /assets?search=主角
	code, list = e.doList(t, http.MethodGet, "/api/assets?project_id="+projectID+"&search=%E4%B8%BB%E8%A7%92", director)
	if code != http.StatusOK {
		t.Fatalf("search assets status=%d body=%v", code, list)
	}
	if len(list) == 0 {
		t.Fatalf("search assets empty")
	}
}

func TestAssetCreateValidationErrors(t *testing.T) {
	e := newEnv(t)
	projectID := e.seedProject(t, "资产校验测试", "", seedProjectOpts{})
	director := e.seedUserAs(t, uniquePhone("1301"), "director", "test1234")
	producer := e.seedUserAs(t, uniquePhone("1302"), "artist", "test1234")

	// 非 director → 403（中间件逐字）
	code, body := e.doJSON(t, http.MethodPost, "/api/assets", map[string]any{
		"asset_type": "character", "name": "X", "metadata": map[string]any{"project_id": projectID},
	}, producer)
	if code != http.StatusForbidden {
		t.Fatalf("producer create status=%d want 403: %v", code, body)
	}
	detail(t, body, "Director or admin permission required")

	cases := []struct {
		name   string
		body   map[string]any
		status int
		want   string
	}{
		{"ineligible type", map[string]any{"asset_type": "music", "name": "配乐", "metadata": map[string]any{"project_id": projectID}}, 400,
			"POST /assets only supports character, scene, and prop assets"},
		{"missing project_id", map[string]any{"asset_type": "character", "name": "角色"}, 400,
			"project_id is required to allocate a formal asset code"},
		{"invalid uuid", map[string]any{"asset_type": "character", "name": "角色", "metadata": map[string]any{"project_id": "not-a-uuid"}}, 400,
			"project_id must be a valid UUID"},
		{"unknown project", map[string]any{"asset_type": "prop", "name": "道具", "metadata": map[string]any{"project_id": "00000000-0000-0000-0000-000000000000"}}, 404,
			"Project not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := e.doJSON(t, http.MethodPost, "/api/assets", tc.body, director)
			if code != tc.status {
				t.Fatalf("status=%d want %d: %v", code, tc.status, body)
			}
			detail(t, body, tc.want)
		})
	}
}

// ---- GET /assets 权限 ----

func TestAssetPermissionDenied(t *testing.T) {
	e := newEnv(t)
	projectID := e.seedProject(t, "资产权限测试", "", seedProjectOpts{})
	director := e.seedUserAs(t, uniquePhone("1303"), "director", "test1234")
	outsider := e.seedUserAs(t, uniquePhone("1304"), "artist", "test1234")

	code, body := e.doJSON(t, http.MethodPost, "/api/assets", map[string]any{
		"asset_type": "character", "name": "看不见的角色", "metadata": map[string]any{"project_id": projectID},
	}, director)
	if code != http.StatusCreated {
		t.Fatalf("seed asset status=%d %v", code, body)
	}
	assetID := assetDetail(t, body, "id")

	code, body = e.doJSON(t, http.MethodGet, "/api/assets?project_id="+projectID, nil, outsider)
	if code != http.StatusForbidden {
		t.Fatalf("list outsider status=%d want 403: %v", code, body)
	}
	detail(t, body, "Asset permission denied")

	code, body = e.doJSON(t, http.MethodGet, "/api/assets/"+assetID, nil, outsider)
	if code != http.StatusForbidden {
		t.Fatalf("get outsider status=%d want 403: %v", code, body)
	}
	detail(t, body, "Asset permission denied")

	// 未知资产 → 404 "Asset not found"
	code, body = e.doJSON(t, http.MethodGet, "/api/assets/00000000-0000-0000-0000-000000000000", nil, director)
	if code != http.StatusNotFound {
		t.Fatalf("unknown asset status=%d want 404: %v", code, body)
	}
	detail(t, body, "Asset not found")
}

// ---- GET /assets/library ----

func TestAssetLibraryPermissions(t *testing.T) {
	e := newEnv(t)
	projectID := e.seedProject(t, "资产库权限测试", "", seedProjectOpts{})
	director := e.seedUserAs(t, uniquePhone("1305"), "director", "test1234")
	outsider := e.seedUserAs(t, uniquePhone("1306"), "artist", "test1234")

	// 未知项目 → 404
	code, body := e.doJSON(t, http.MethodGet, "/api/assets/library?project_id=00000000-0000-0000-0000-000000000000", nil, director)
	if code != http.StatusNotFound {
		t.Fatalf("unknown project status=%d want 404: %v", code, body)
	}
	detail(t, body, "Project not found")

	// 无权限项目 → 403 "Asset library permission denied"
	code, body = e.doJSON(t, http.MethodGet, "/api/assets/library?project_id="+projectID, nil, outsider)
	if code != http.StatusForbidden {
		t.Fatalf("library outsider status=%d want 403: %v", code, body)
	}
	detail(t, body, "Asset library permission denied")

	// director：项目内空库 → 200 []
	code, lib := e.doList(t, http.MethodGet, "/api/assets/library?project_id="+projectID, director)
	if code != http.StatusOK {
		t.Fatalf("library status=%d body=%v", code, lib)
	}
	// 全项目库 → 200（数组）
	code, lib = e.doList(t, http.MethodGet, "/api/assets/library?include_versions=true", director)
	if code != http.StatusOK {
		t.Fatalf("library all status=%d body=%v", code, lib)
	}
}

// doList 请求返回 JSON 数组的端点并解析为 []any（doJSON 只适合对象响应）。
func (e *testEnv) doList(t *testing.T, method, path, token string) (int, []any) {
	t.Helper()
	code, raw := e.do(t, method, path, nil, token)
	var out []any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal list %s: %v (raw=%s)", path, err, raw)
		}
	}
	return code, out
}

// ---- variant-plans ----

func TestVariantPlanContract(t *testing.T) {
	e := newEnv(t)
	projectID := e.seedProject(t, "变体计划测试", "", seedProjectOpts{})
	director := e.seedUserAs(t, uniquePhone("1307"), "director", "test1234")
	outsider := e.seedUserAs(t, uniquePhone("1308"), "artist", "test1234")

	// POST 恒 400（逐字）
	code, body := e.doJSON(t, http.MethodPost, "/api/assets/variant-plans?project_id="+projectID, map[string]any{
		"asset_id": "00000000-0000-0000-0000-000000000000", "variant_kind": "scene_view",
		"title_zh": "近景", "description_zh": "描述",
	}, director)
	if code != http.StatusBadRequest {
		t.Fatalf("create variant plan status=%d want 400: %v", code, body)
	}
	detail(t, body, "场景和道具不再按分镜创建变体")

	// 创建资产作为 plan 的 asset 引用
	code, body = e.doJSON(t, http.MethodPost, "/api/assets", map[string]any{
		"asset_type": "prop", "name": "道具A", "metadata": map[string]any{"project_id": projectID},
	}, director)
	if code != http.StatusCreated {
		t.Fatalf("seed prop status=%d %v", code, body)
	}
	assetID := assetDetail(t, body, "id")

	code, body = e.doJSON(t, http.MethodGet, "/api/assets/variant-plans?project_id="+projectID, nil, director)
	if code != http.StatusOK {
		t.Fatalf("list variant plans status=%d: %v", code, body)
	}

	// 无权限查看 → 403 "Asset variant permission denied"
	code, body = e.doJSON(t, http.MethodGet, "/api/assets/variant-plans?project_id="+projectID, nil, outsider)
	if code != http.StatusForbidden {
		t.Fatalf("list outsider status=%d want 403: %v", code, body)
	}
	detail(t, body, "Asset variant permission denied")

	// PATCH 未知 plan → 404
	code, body = e.doJSON(t, http.MethodPatch, "/api/assets/variant-plans/00000000-0000-0000-0000-000000000000",
		map[string]any{"status": "confirmed"}, director)
	if code != http.StatusNotFound {
		t.Fatalf("patch unknown plan status=%d want 404: %v", code, body)
	}
	detail(t, body, "Variant plan not found")

	// PATCH 已有 plan → 200
	planID := e.seedVariantPlan(t, projectID, assetID)
	code, body = e.doJSON(t, http.MethodPatch, "/api/assets/variant-plans/"+planID,
		map[string]any{"title_zh": "近景-改", "status": "confirmed"}, director)
	if code != http.StatusOK {
		t.Fatalf("patch plan status=%d: %v", code, body)
	}
	if got := assetDetail(t, body, "status"); got != "confirmed" {
		t.Fatalf("patched status=%q want confirmed", got)
	}

	// DELETE → retired
	code, body = e.doJSON(t, http.MethodDelete, "/api/assets/variant-plans/"+planID, nil, director)
	if code != http.StatusOK {
		t.Fatalf("delete plan status=%d: %v", code, body)
	}
	if got := assetDetail(t, body, "status"); got != "retired" {
		t.Fatalf("retired status=%q want retired", got)
	}
}

// ---- scene-options ----

func TestSceneAssetOptions(t *testing.T) {
	e := newEnv(t)
	projectID := e.seedProject(t, "场景选项测试", "", seedProjectOpts{})
	director := e.seedUserAs(t, uniquePhone("1309"), "director", "test1234")
	outsider := e.seedUserAs(t, uniquePhone("1310"), "artist", "test1234")

	e.seedSceneAsset(t, projectID)
	path := "/api/projects/" + projectID + "/asset-revisions/current/scene-options"

	code, items := e.doList(t, http.MethodGet, path, director)
	if code != http.StatusOK {
		t.Fatalf("scene options status=%d: %v", code, items)
	}
	if len(items) == 0 {
		t.Fatalf("scene options empty: %v", items)
	}
	first, _ := items[0].(map[string]any)
	if got := first["scene_asset_id"].(string); got == "" {
		t.Fatalf("missing scene_asset_id: %v", first)
	}

	// 无权限 → 403 "Project permission denied"
	var body map[string]any
	code, body = e.doJSON(t, http.MethodGet, path, nil, outsider)
	if code != http.StatusForbidden {
		t.Fatalf("scene options outsider status=%d want 403: %v", code, body)
	}
	detail(t, body, "Project permission denied")

	// 未知项目 → 404 "Project not found"
	code, body = e.doJSON(t, http.MethodGet, "/api/projects/00000000-0000-0000-0000-000000000000/asset-revisions/current/scene-options",
		nil, director)
	if code != http.StatusNotFound {
		t.Fatalf("scene options unknown status=%d want 404: %v", code, body)
	}
	detail(t, body, "Project not found")
}

// ---- POST /assets/temporary-production ----

func TestTemporaryProductionGuardsAndIdempotency(t *testing.T) {
	e := newEnv(t)
	projectID := e.seedProject(t, "人工临时生产测试", "", seedProjectOpts{})
	director := e.seedUserAs(t, uniquePhone("1311"), "director", "test1234")
	producer := e.seedUserAs(t, uniquePhone("1312"), "artist", "test1234")

	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	scriptVersionID := e.seedScript(t, projectID, episodeID, "第一集剧本……")

	requestID, _ := auth.NewUUID()
	payload := func(rid string) map[string]any {
		return map[string]any{
			"request_id": rid, "project_id": projectID, "episode_id": episodeID,
			"script_version_id": scriptVersionID, "asset_type": "prop", "name": "临时代具",
			"reason": "临时追加", "production_requirement": "老式皮箱",
		}
	}

	// 未确认资产拆解 → 400 逐字
	code, body := e.doJSON(t, http.MethodPost, "/api/assets/temporary-production", payload(requestID), director)
	if code != http.StatusBadRequest {
		t.Fatalf("unconfirmed status=%d want 400: %v", code, body)
	}
	detail(t, body, "请先确认资产拆解，确认后才能新增人工临时生产资产。")

	// 非当前版本守卫：先确认，再用一个不存在的版本 → 400 跨版本逐字
	unknownVersion := "00000000-0000-0000-0000-000000000000"
	code, body = e.doJSON(t, http.MethodPost, "/api/assets/temporary-production",
		map[string]any{"request_id": requestID, "project_id": projectID, "episode_id": episodeID,
			"script_version_id": unknownVersion, "asset_type": "prop", "name": "临时代具",
			"reason": "临时追加", "production_requirement": "老式皮箱"}, director)
	if code != http.StatusBadRequest {
		t.Fatalf("wrong version status=%d want 400: %v", code, body)
	}
	detail(t, body, "只能在当前分集的最新剧本版本中追加人工生产资产。")

	// 确认资产拆解 → 成功 201
	e.seedConfirmedAssetInventory(t, projectID, episodeID, scriptVersionID)
	code, body = e.doJSON(t, http.MethodPost, "/api/assets/temporary-production", payload(requestID), director)
	if code != http.StatusCreated {
		t.Fatalf("temp production status=%d: %v", code, body)
	}
	asset, _ := body["asset"].(map[string]any)
	task, _ := body["task"].(map[string]any)
	if asset == nil || task == nil {
		t.Fatalf("temp production missing asset/task: %v", body)
	}
	assetID := asset["id"].(string)
	taskID := task["id"].(string)
	if assetID == "" || taskID == "" {
		t.Fatalf("empty asset/task id: %v", body)
	}
	if typ := asset["asset_type"].(string); typ != "prop" {
		t.Fatalf("asset_type=%q", typ)
	}

	// 幂等：相同 request_id 重放 → 201 且 id 不变
	code, body = e.doJSON(t, http.MethodPost, "/api/assets/temporary-production", payload(requestID), director)
	if code != http.StatusCreated {
		t.Fatalf("idempotent status=%d: %v", code, body)
	}
	asset2, _ := body["asset"].(map[string]any)
	task2, _ := body["task"].(map[string]any)
	if asset2["id"].(string) != assetID || task2["id"].(string) != taskID {
		t.Fatalf("idempotent mismatch: %v vs %v", body, assetID)
	}

	// 非 director → 403
	code, body = e.doJSON(t, http.MethodPost, "/api/assets/temporary-production", payload(requestID), producer)
	if code != http.StatusForbidden {
		t.Fatalf("producer temp status=%d want 403: %v", code, body)
	}
	detail(t, body, "Director or admin permission required")
}

// ---- episode detail 资产绑定 ----

func TestEpisodeDetailAssetBindings(t *testing.T) {
	e := newEnv(t)
	projectID := e.seedProject(t, "分集资产绑定测试", "", seedProjectOpts{})
	director := e.seedUserAs(t, uniquePhone("1313"), "director", "test1234")

	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	scriptVersionID := e.seedScript(t, projectID, episodeID, "第一集剧本……")
	e.seedConfirmedAssetInventory(t, projectID, episodeID, scriptVersionID)

	// 创建一个 prop 资产
	code, body := e.doJSON(t, http.MethodPost, "/api/assets", map[string]any{
		"asset_type": "prop", "name": "绑定道具", "metadata": map[string]any{"project_id": projectID},
	}, director)
	if code != http.StatusCreated {
		t.Fatalf("seed prop status=%d: %v", code, body)
	}
	assetID := assetDetail(t, body, "id")

	// 插入 active 绑定（FK：confirmed_reading/application → artifact_revisions reading_report）
	readingRevID, _ := auth.NewUUID()
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO artifact_revisions
			(id, project_id, artifact_id, artifact_type, version_no, source_type,
			 input_snapshot, raw_output, normalized_content, change_diff, content_hash,
			 status, data_state, created_at, updated_at)
		 VALUES ($1,$2,$1,'reading_report',1,'human','{}','{}','{}','{}',
		 	 'reading-hash','confirmed','final',now(),now())`,
		readingRevID, projectID); err != nil {
		t.Fatalf("seed reading revision: %v", err)
	}
	bindingID, _ := auth.NewUUID()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO episode_asset_bindings
			(id, project_id, episode_id, script_version_id, confirmed_reading_revision_id,
			 application_revision_id, asset_id, proposal_index, action, status, proposal_json, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$5,$6,0,'add','active','{}',now(),now())`,
		bindingID, projectID, episodeID, scriptVersionID, readingRevID, assetID); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	code, body = e.doJSON(t, http.MethodGet, "/api/projects/"+projectID+"/episodes/"+episodeID, nil, director)
	if code != http.StatusOK {
		t.Fatalf("episode detail status=%d: %v", code, body)
	}
	assetsRaw, _ := json.Marshal(body["assets"])
	var bindings []map[string]any
	_ = json.Unmarshal(assetsRaw, &bindings)
	if len(bindings) != 1 {
		t.Fatalf("bindings len=%d want 1: %v", len(bindings), body)
	}
	if bindings[0]["asset_id"].(string) != assetID {
		t.Fatalf("binding asset_id mismatch: %v", bindings[0])
	}
	if bindings[0]["asset_code"].(string) == "" {
		t.Fatalf("binding missing asset_code: %v", bindings[0])
	}
}

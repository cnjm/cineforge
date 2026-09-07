package api_test

// P3f-d 生成器 HTTP 契约测试（对齐 legacy api/routes/projects.py 的
// POST /projects/{project_id}/asset-tasks 与 /storyboard-tasks，均 require_director_or_admin + 201）：
//   - 参数/角色/项目可见性守卫：422 请求体格式错误 / 403 Director or admin permission required / 404 Project not found
//   - 作用域与状态守卫（逐字）：分集与剧本版本不匹配 / 当前分集没有已确认正式资产 /
//     尚未经过专用 Prompt Skill / 未确认资产短路整体清单（legacy tier-1 assets_not_confirmed→[]）
//     / 分镜尚未形成 human_final 修订
//   - 成功路径 201：asset-tasks 全量结果键 + audio 子键 + 再跑幂等（reused，不重复建任务）
//   - storyboard-tasks：创建关键帧+视频任务、二次跑幂等（不重复、无冲突）
// 需要 dev DB + .env 的 auth secret（缺失自动 skip，复用 newEnv）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"cineforge/server/internal/auth"
)

// genPath 拼接资产/分镜任务生成路径（kind: asset / storyboard）。
func genPath(projectID, kind, episodeID, versionID string) string {
	return fmt.Sprintf("/api/projects/%s/%s-tasks?episode_id=%s&script_version_id=%s",
		projectID, kind, episodeID, versionID)
}

// countActiveTasks 统计项目下未退休任务数（幂等断言用）。
func (e *testEnv) countActiveTasks(t *testing.T, projectID string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM tasks WHERE project_id = $1 AND is_retired = FALSE`, projectID).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return n
}

// seedAssetInventoryItems 写入一条 human confirmed 的 asset_inventory 修订作为已确认资产清单。
// 注意与 asset_contract_test.go 的 seedConfirmedAssetInventory（空清单）区分。
// items 为 normalized_content.assets 条目；作用域挂在 input_snapshot。
func (e *testEnv) seedAssetInventoryItems(t *testing.T, projectID, episodeID, versionID string, items []any) {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	snapshot, _ := json.Marshal(map[string]any{
		"episode_id": episodeID, "script_version_id": versionID,
	})
	content, _ := json.Marshal(map[string]any{"assets": items})
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `INSERT INTO artifact_revisions (
		id, project_id, artifact_type, artifact_id, version_no, source_type,
		input_snapshot, raw_output, normalized_content, change_diff, content_hash,
		status, data_state, created_at, updated_at)
		VALUES ($1,$2,'asset_inventory',$2,1,'human',$3,'{}',$4,'{}',$5,'confirmed','final',now(),now())`,
		id, projectID, []byte(snapshot), []byte(content), strings.Repeat("a", 64)); err != nil {
		t.Fatalf("seed confirmed asset inventory: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM artifact_revisions WHERE id = $1`, id)
	})
}

// charInventoryItem 构造确认态/未确认态人物资产清单条目。prompt 为空表示尚未经过专用 Prompt Skill。
func charInventoryItem(code, name, prompt string, confirmed bool) map[string]any {
	status := "pending_confirmation"
	if confirmed {
		status = "confirmed"
	}
	item := map[string]any{
		"asset_code":  code,
		"asset_type":  "character",
		"name":        name,
		"description": "测试角色",
		"metadata": map[string]any{
			"confirmation_status": status,
			"breakdown_asset": map[string]any{
				"identity_setting": "少年主角，温和坚定",
				"age_stages": []any{
					map[string]any{
						"stage_code":  "S1",
						"name":        "少年期",
						"age_range":   "12-16岁",
						"output_spec": "A",
					},
				},
			},
		},
	}
	if prompt != "" {
		item["prompt"] = prompt
	}
	return item
}

// seedStoryboardBreakdown 写入 storyboard human_final 修订 + 对应 script_breakdowns 内容，
// 并插入匹配的 storyboards 行。content 通过 validate_breakdown_view 的全部阻塞校验。
// 返回 (storyboardID, revisionID)。
func (e *testEnv) seedStoryboardBreakdown(t *testing.T, projectID, episodeID, versionID string,
	episodeNo int32, episodeCode, sbCode, segmentCode string) (string, string) {
	t.Helper()
	sbID, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	revID, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	breakdownID, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()

	// storyboard human_final 修订（finalRevisionID 来源）。
	if _, err := e.pool.Exec(ctx, `INSERT INTO artifact_revisions (
		id, project_id, artifact_type, artifact_id, version_no, source_type,
		input_snapshot, raw_output, normalized_content, change_diff, content_hash,
		status, data_state, created_at, updated_at)
		VALUES ($1,$2,'storyboard',$2,1,'human',$3,$4,'{}','{}',$5,'confirmed','final',now(),now())`,
		revID, projectID, []byte(`{}`),
		[]byte(fmt.Sprintf(`{"confirmed_storyboard_revision_id":"%s"}`, revID)),
		strings.Repeat("b", 64)); err != nil {
		t.Fatalf("seed storyboard human_final revision: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM artifact_revisions WHERE id = $1`, revID)
	})

	// breakdown 视图内容（lineage + 阻塞校验全过）。
	content := map[string]any{
		"episode_id":        episodeID,
		"script_version_id": versionID,
		"script_segments": []any{
			map[string]any{
				"script_segment_code": segmentCode,
				"source_text":         "小明推门走进房间，环顾四周。",
			},
		},
		"storyboards": []any{
			map[string]any{
				"storyboard_code":     sbCode,
				"episode_code":        episodeCode,
				"script_segment_code": segmentCode,
				"description":         "小明站在门口，犹豫片刻后推门走进房间。",
				"duration_seconds":    12,
				"shot_size":           "中景",
				"order_num":           1,
				"dialogue":            "",
			},
		},
		"raw_output": map[string]any{
			"confirmed_storyboard_revision_id": revID,
		},
	}
	contentJSON, _ := json.Marshal(content)
	if _, err := e.pool.Exec(ctx, `INSERT INTO script_breakdowns (
		id, project_id, version, content_json, data_state, created_at, updated_at)
		VALUES ($1,$2,1,$3,'final',now(),now())`,
		breakdownID, projectID, []byte(contentJSON)); err != nil {
		t.Fatalf("seed storyboard breakdown: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM script_breakdowns WHERE id = $1`, breakdownID)
	})

	// 分镜表行（与 content.current_items 对齐；NOT NULL jsonb/enum 列齐全）。
	if _, err := e.pool.Exec(ctx, `INSERT INTO storyboards (
		id, project_id, episode_num, order_num, title, description, dialogue, camera,
		duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
		scene_code, scene_name, narration, shot_type, created_at, updated_at)
		VALUES ($1,$2,$3,1,$4,$5,$6,$7,12,'[]','[]','[]','todo',$8,'SCENE-01','第一场',$9,'中景',now(),now())`,
		sbID, projectID, episodeNo, "分镜标题-"+sbCode, "小明站在门口，犹豫片刻后推门走进房间。",
		"", "固定机位", sbCode, "画面描述：小明推门。"+sbCode); err != nil {
		t.Fatalf("seed storyboard row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM storyboards WHERE id = $1`, sbID)
	})
	return sbID, revID
}

// ---- 参数 / 角色 / 项目可见性 ----

func TestTaskGeneratorsMissingParams422(t *testing.T) {
	e := newEnv(t)
	dir := e.seedUserAs(t, uniquePhone("961"), "director", "secret123")
	projectID := e.seedProject(t, "生成器缺参", "GEN", seedProjectOpts{})

	for _, kind := range []string{"asset", "storyboard"} {
		code, body := e.doJSON(t, http.MethodPost,
			fmt.Sprintf("/api/projects/%s/%s-tasks", projectID, kind), map[string]any{}, dir)
		assertStatus(t, code, http.StatusUnprocessableEntity, body)
		detail(t, body, "请求体格式错误")
	}
}

func TestTaskGeneratorsRequireDirectorOrAdmin(t *testing.T) {
	e := newEnv(t)
	artist := e.seedUserAs(t, uniquePhone("962"), "artist", "secret123")
	projectID := e.seedProject(t, "生成器版本", "GEN", seedProjectOpts{})

	for _, kind := range []string{"asset", "storyboard"} {
		code, body := e.doJSON(t, http.MethodPost,
			genPath(projectID, kind, "00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000001"),
			map[string]any{}, artist)
		assertStatus(t, code, http.StatusForbidden, body)
		detail(t, body, "Director or admin permission required")
	}
}

func TestTaskGeneratorsProjectNotFound(t *testing.T) {
	e := newEnv(t)
	dir := e.seedUserAs(t, uniquePhone("963"), "director", "secret123")

	for _, kind := range []string{"asset", "storyboard"} {
		code, body := e.doJSON(t, http.MethodPost,
			genPath("00000000-0000-0000-0000-000000000000", kind,
				"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"),
			map[string]any{}, dir)
		assertStatus(t, code, http.StatusNotFound, body)
		detail(t, body, "Project not found")
	}
}

// ---- 作用域与状态守卫（逐字） ----

func TestAssetTasksGenerateScopeMismatch(t *testing.T) {
	e := newEnv(t)
	dir := e.seedUserAs(t, uniquePhone("964"), "director", "secret123")
	projectID := e.seedProject(t, "生成器作用域", "GEN", seedProjectOpts{})
	ep1 := e.seedEpisode(t, projectID, "EP01", 1)
	ep2 := e.seedEpisode(t, projectID, "EP02", 2)
	versionID := e.seedScript(t, projectID, ep1, "第一幕。\n")

	code, body := e.doJSON(t, http.MethodPost,
		genPath(projectID, "asset", ep2, versionID), map[string]any{}, dir)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "分集与剧本版本不匹配。")
}

func TestAssetTasksGenerateNoConfirmedAssets(t *testing.T) {
	e := newEnv(t)
	dir := e.seedUserAs(t, uniquePhone("965"), "director", "secret123")
	projectID := e.seedProject(t, "生成器无资产", "GEN", seedProjectOpts{})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")

	code, body := e.doJSON(t, http.MethodPost,
		genPath(projectID, "asset", episodeID, versionID), map[string]any{}, dir)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "当前分集没有已确认正式资产，不能进入任务阶段。")
}

func TestAssetTasksGenerateMissingPromptSkill(t *testing.T) {
	e := newEnv(t)
	dir := e.seedUserAs(t, uniquePhone("966"), "director", "secret123")
	projectID := e.seedProject(t, "生成器缺Prompt", "GEN", seedProjectOpts{})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")
	// 已人工确认但无 prompt / 无输入指纹 → 视为未经过专用 Prompt Skill。
	e.seedAssetInventoryItems(t, projectID, episodeID, versionID,
		[]any{charInventoryItem("T-C01", "小明", "", true)})

	code, body := e.doJSON(t, http.MethodPost,
		genPath(projectID, "asset", episodeID, versionID), map[string]any{}, dir)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "以下资产尚未经过专用 Prompt Skill：C01 小明。请先补齐资产 Prompt。")
}

func TestAssetTasksGenerateAnyUnconfirmedBlocksInventory(t *testing.T) {
	e := newEnv(t)
	dir := e.seedUserAs(t, uniquePhone("967"), "director", "secret123")
	projectID := e.seedProject(t, "生成器未确认", "GEN", seedProjectOpts{})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")
	// legacy confirmed_asset_inventory 的 tier-1 短路：确认修订中只要有一个未确认资产
	// 就整体返回 []（repositories.py assets_not_confirmed→[]），不产出部分清单。
	e.seedAssetInventoryItems(t, projectID, episodeID, versionID, []any{
		charInventoryItem("T-C01", "小明", "小明形象：短发少年。", true),
		charInventoryItem("T-C02", "小红", "小红形象：双马尾少女。", false),
	})

	code, body := e.doJSON(t, http.MethodPost,
		genPath(projectID, "asset", episodeID, versionID), map[string]any{}, dir)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "当前分集没有已确认正式资产，不能进入任务阶段。")
}

func TestStoryboardTasksGenerateNoHumanFinalRevision(t *testing.T) {
	e := newEnv(t)
	dir := e.seedUserAs(t, uniquePhone("968"), "director", "secret123")
	projectID := e.seedProject(t, "分镜守卫", "GEN", seedProjectOpts{})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")

	code, body := e.doJSON(t, http.MethodPost,
		genPath(projectID, "storyboard", episodeID, versionID), map[string]any{}, dir)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "分镜尚未形成 human_final 修订，不能生成分镜任务。")
}

// ---- 成功路径 + 幂等 ----

func TestAssetTasksGenerateSuccessAndIdempotent(t *testing.T) {
	e := newEnv(t)
	dir := e.seedUserAs(t, uniquePhone("971"), "director", "secret123")
	projectID := e.seedProject(t, "生成器成功", "GEN", seedProjectOpts{})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n小明：【去看看。】\n")
	e.seedAssetInventoryItems(t, projectID, episodeID, versionID,
		[]any{charInventoryItem("T-C03", "阿毛", "阿毛形象：圆脸短发少年。", true)})
	path := genPath(projectID, "asset", episodeID, versionID)

	code, body := e.doJSON(t, http.MethodPost, path, map[string]any{}, dir)
	assertStatus(t, code, http.StatusCreated, body)
	for _, key := range []string{"created_tasks", "character_tasks", "scene_tasks", "prop_tasks",
		"theme_music_tasks", "background_music_tasks", "character_voice_tasks",
		"reused_tasks", "failed_items", "assets", "episode_id", "script_version_id",
		"dependency_links", "variant_dependencies", "refreshed_prompts", "audio", "stage"} {
		if _, ok := body[key]; !ok {
			t.Errorf("响应缺少 %s: %v", key, body)
		}
	}
	// 人物 1 张（A）+ 主题音乐 + 背景音乐 = 3；无音色（角色 C 级且无台词来源）。
	if body["created_tasks"] != float64(3) {
		t.Errorf("created_tasks=%v 期望 3", body["created_tasks"])
	}
	if body["character_tasks"] != float64(1) {
		t.Errorf("character_tasks=%v 期望 1", body["character_tasks"])
	}
	if body["scene_tasks"] != float64(0) || body["prop_tasks"] != float64(0) {
		t.Errorf("scene/prop tasks=%v/%v", body["scene_tasks"], body["prop_tasks"])
	}
	if body["reused_tasks"] != float64(0) {
		t.Errorf("首次 reused_tasks=%v", body["reused_tasks"])
	}
	audio, _ := body["audio"].(map[string]any)
	if audio == nil {
		t.Fatalf("audio 缺失: %v", body)
	}
	if audio["created_tasks"] != float64(2) || audio["planned_tasks"] != float64(2) {
		t.Errorf("audio created/planned=%v/%v", audio["created_tasks"], audio["planned_tasks"])
	}
	if audio["theme_music_tasks"] != float64(1) || audio["background_music_tasks"] != float64(1) {
		t.Errorf("audio theme/bgm=%v/%v", audio["theme_music_tasks"], audio["background_music_tasks"])
	}
	if audio["voice_tasks"] != float64(0) {
		t.Errorf("audio voice_tasks=%v 期望 0（未 S/A 且有台词）", audio["voice_tasks"])
	}
	if stage, _ := body["stage"].(string); stage == "" {
		t.Errorf("stage 为空: %v", body["stage"])
	}
	afterFirst := e.countActiveTasks(t, projectID)

	// 幂等：再次生成 → 全部复用，不新增任务。
	code, body = e.doJSON(t, http.MethodPost, path, map[string]any{}, dir)
	assertStatus(t, code, http.StatusCreated, body)
	if body["created_tasks"] != float64(0) {
		t.Errorf("二次 created_tasks=%v 期望 0（全部复用）", body["created_tasks"])
	}
	if body["reused_tasks"] == float64(0) {
		t.Errorf("二次 reused_tasks=%v 期望 >0", body["reused_tasks"])
	}
	if got := e.countActiveTasks(t, projectID); got != afterFirst {
		t.Errorf("幂等后任务数=%d 期望 %d（不重复创建）", got, afterFirst)
	}
}

func TestStoryboardTasksGenerateSuccessAndIdempotent(t *testing.T) {
	e := newEnv(t)
	dir := e.seedUserAs(t, uniquePhone("972"), "director", "secret123")
	projectID := e.seedProject(t, "分镜成功", "GEN", seedProjectOpts{})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")
	sbCode := "T-GEN-EP01-F001"
	_, _ = e.seedStoryboardBreakdown(t, projectID, episodeID, versionID, 1, "EP01", sbCode, "SEG-001")
	path := genPath(projectID, "storyboard", episodeID, versionID)

	code, body := e.doJSON(t, http.MethodPost, path, map[string]any{}, dir)
	assertStatus(t, code, http.StatusCreated, body)
	if body["created_tasks"] != float64(2) {
		t.Errorf("created_tasks=%v 期望 2（关键帧+视频）", body["created_tasks"])
	}
	for _, key := range []string{"updated_tasks", "storyboards", "episode_id",
		"script_version_id", "auto_variant_plans", "variant_dependencies"} {
		if _, ok := body[key]; !ok {
			t.Errorf("响应缺少 %s: %v", key, body)
		}
	}
	// 幂等：任务字段与分镜一致 → 无更新/无冲突，任务数不变。
	code, body = e.doJSON(t, http.MethodPost, path, map[string]any{}, dir)
	assertStatus(t, code, http.StatusCreated, body)
	if body["updated_tasks"] != float64(0) {
		t.Errorf("二次 updated_tasks=%v 期望 0", body["updated_tasks"])
	}
	if body["created_tasks"] != float64(0) {
		t.Errorf("二次 created_tasks=%v 期望 0", body["created_tasks"])
	}
	if got := e.countActiveTasks(t, projectID); got != 2 {
		t.Errorf("幂等后任务数=%d 期望 2", got)
	}
}
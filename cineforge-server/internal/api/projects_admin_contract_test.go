package api_test

// P3d 回补契约测试（对齐 legacy app/api/routes/projects.py 对应路径）：
//   - POST /projects/{id}/soft-delete（403 密码校验失败 / 404 幽灵项目 / 200 软删）
//   - POST /projects/{id}/script-segments/confirm（400 无脚本段 / 200 确认+幂等早退）
//   - POST /projects/{id}/breakdowns/lock（400 无可锁定输出 / 400 终版校验 / 200 锁定）
//   - DELETE /projects/{id}/episodes/{episode_id}（404 分集不存在 / 400 确认码不匹配 / 200 级联）
//     P3f-e：delete_files 门控、共享文件保留、真实 MinIO 对象清理（对齐 cleanup_deleted_episode_files）。
// 需要 dev DB + .env 的 auth secret（缺失自动 skip，复用 newEnv）。

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"cineforge/server/internal/storage"
)

// ---- soft-delete ----

func TestSoftDeleteProjectWrongPassword(t *testing.T) {
	e := newEnv(t)
	// admin 用密码 secret123；director 提供的 admin_password 错误。
	e.seedUser(t, uniquePhone("930"), "admin", "secret123")
	adminTok := e.seedUserAs(t, uniquePhone("931"), "admin", "secret456")
	projectID := e.seedProject(t, "软删-密码错误", "SDEL", seedProjectOpts{scriptText: "第一幕。\n"})

	code, body := e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/soft-delete",
		map[string]any{"admin_password": "wrongpw"}, adminTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Admin password verification failed")
}

func TestSoftDeleteProjectSuccessAndGone(t *testing.T) {
	e := newEnv(t)
	e.seedUser(t, uniquePhone("932"), "admin", "secret123")
	adminTok := e.seedUserAs(t, uniquePhone("933"), "admin", "secret123")
	projectID := e.seedProject(t, "软删-成功", "SDOK", seedProjectOpts{scriptText: "第一幕。\n"})

	code, body := e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/soft-delete",
		map[string]any{"admin_password": "secret123"}, adminTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["id"] != projectID {
		t.Errorf("id=%v", body["id"])
	}

	// 软删后 GET 项目 → 404
	code, body = e.doJSON(t, http.MethodGet, "/api/projects/"+projectID, nil, adminTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Project not found")
}

func TestSoftDeleteProjectGhostProject(t *testing.T) {
	e := newEnv(t)
	e.seedUser(t, uniquePhone("934"), "admin", "secret123")
	adminTok := e.seedUserAs(t, uniquePhone("935"), "admin", "secret123")

	code, body := e.doJSON(t, http.MethodPost, "/api/projects/00000000-0000-0000-0000-000000000000/soft-delete",
		map[string]any{"admin_password": "secret123"}, adminTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Project not found")
}

func TestSoftDeleteProjectRequiresDirectorOrAdmin(t *testing.T) {
	e := newEnv(t)
	artistTok := e.seedUserAs(t, uniquePhone("936"), "artist", "secret123")
	projectID := e.seedProject(t, "软删-角色", "SDRO", seedProjectOpts{scriptText: "第一幕。\n"})

	code, body := e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/soft-delete",
		map[string]any{"admin_password": "secret123"}, artistTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director or admin permission required")
}

// ---- script-segments / confirm ----

func segmentView(episodeID, versionID string) map[string]any {
	return map[string]any{
		"schema_version": "BreakdownWorkflow.v1",
		"episode_id":     episodeID,
		"script_version_id": versionID,
		"reading_review_state": map[string]any{"status": "confirmed"},
		"asset_review_state":   map[string]any{"status": "needs_review"},
		"script_segments": []map[string]any{
			{
				"script_segment_code": "SEG-EP01-01",
				"episode_code":        "EP01",
				"order_no":            1,
				"title":               "开篇",
				"source_text":         "第一幕。\n小明：【去看看。】\n",
				"summary":             "小明决定出门。",
				"story_function":      "引入主角与动机",
			},
		},
	}
}

func TestConfirmScriptSegmentsEmptySegments400(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("937"), "director", "secret123")
	projectID := e.seedProject(t, "脚本段-空", "SEGR", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")

	code, body := e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/script-segments/confirm",
		map[string]any{
			"episode_id": episodeID, "script_version_id": versionID,
			"episode_code": "EP01",
			"view": map[string]any{
				"schema_version":      "BreakdownWorkflow.v1",
				"episode_id":          episodeID,
				"script_version_id":   versionID,
				"asset_review_state": map[string]any{"status": "needs_review"},
			},
		}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "当前没有脚本原文段，不能确认并进入分镜/资产拆分。请先运行脚本段拆分或人工新增脚本段。")
}

func TestConfirmScriptSegmentsFlow(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("938"), "director", "secret123")
	projectID := e.seedProject(t, "脚本段-确认", "SEGC", seedProjectOpts{scriptText: "第一幕。\n小明：【去看看。】\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n小明：【去看看。】\n")

	path := "/api/projects/" + projectID + "/script-segments/confirm"
	payload := map[string]any{"episode_id": episodeID, "script_version_id": versionID, "episode_code": "EP01"}
	code, body := e.doJSON(t, http.MethodPost, path, payload, dirTok)
	// 未提供 view 且无脚本段 → 400（对齐空段闸口）
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "当前没有脚本原文段")

	code, body = e.doJSON(t, http.MethodPost, path, map[string]any{
		"episode_id": episodeID, "script_version_id": versionID, "episode_code": "EP01",
		"view": segmentView(episodeID, versionID),
	}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["script_segment_count"] != float64(1) {
		t.Errorf("script_segment_count=%v", body["script_segment_count"])
	}
	if body["next_stage"] != "asset_locking" {
		t.Errorf("next_stage=%v", body["next_stage"])
	}
	firstVersion, _ := body["breakdown_version"].(float64)
	if firstVersion <= 0 {
		t.Errorf("breakdown_version=%v", body["breakdown_version"])
	}

	// 幂等再确认 → 同一 breakdown_version（早退）
	code, body = e.doJSON(t, http.MethodPost, path, map[string]any{
		"episode_id": episodeID, "script_version_id": versionID, "episode_code": "EP01",
		"view": segmentView(episodeID, versionID),
	}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if got, _ := body["breakdown_version"].(float64); got != firstVersion {
		t.Errorf("幂等确认应返回同一 breakdown_version: got=%v want=%v", got, firstVersion)
	}
}

// ---- breakdowns / lock ----

func lockdownStoryboardView(episodeID, versionID string) map[string]any {
	return map[string]any{
		"schema_version":      "BreakdownWorkflow.v1",
		"episode_id":          episodeID,
		"script_version_id":   versionID,
		"episode_code":        "EP01",
		"reading_review_state": map[string]any{"status": "confirmed"},
		"asset_review_state":   map[string]any{"status": "needs_review"},
		"script_segments": []map[string]any{
			{
				"script_segment_code": "SEG-EP01-01",
				"episode_code":        "EP01",
				"order_no":            1,
				"title":               "开篇",
				"source_text":         "第一幕。\n小明：【去看看。】\n",
			},
		},
		"storyboards": []map[string]any{
			{
				"storyboard_code":     "T-SEG-EP01-01-F001",
				"episode_code":        "EP01",
				"order_num":           1,
				"script_segment_code": "SEG-EP01-01",
				"title":               "分镜 1",
				"description":         "小明站在门前准备出门。",
				"duration_seconds":    12,
				"shot_type":           "中景",
				"camera":              "固定机位, 平视",
				"dialogue":            "去看看。",
			},
		},
		"assets": []map[string]any{},
	}
}

func TestLockBreakdownNoLockableOutputs400(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("939"), "director", "secret123")
	projectID := e.seedProject(t, "锁定-空", "LCK0", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")

	code, body := e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/breakdowns/lock",
		map[string]any{
			"episode_id": episodeID, "script_version_id": versionID, "episode_code": "EP01",
			"create_asset_tasks": false,
			"view": map[string]any{
				"schema_version":      "BreakdownWorkflow.v1",
				"episode_id":          episodeID,
				"script_version_id":   versionID,
				"episode_code":        "EP01",
				"asset_review_state": map[string]any{"status": "needs_review"},
				"script_segments": []map[string]any{
					{"script_segment_code": "SEG-EP01-01", "episode_code": "EP01", "order_no": 1, "source_text": "x"},
				},
			},
		}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "当前只有脚本原文段，尚未生成分镜或资产清单，不能锁定本集并生成任务。请先完成分镜拆解或人工补充分镜/资产。")
}

func TestLockBreakdownValidationFail400(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("940"), "director", "secret123")
	projectID := e.seedProject(t, "锁定-校验", "LCKV", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")

	view := lockdownStoryboardView(episodeID, versionID)
	// 时长越界 → blocking error
	sbs := view["storyboards"].([]map[string]any)
	sbs[0]["duration_seconds"] = 8
	code, body := e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/breakdowns/lock",
		map[string]any{"episode_id": episodeID, "script_version_id": versionID, "episode_code": "EP01",
			"create_asset_tasks": false, "view": view}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "分镜终版校验失败：")
}

func TestLockBreakdownFlow(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("941"), "director", "secret123")
	projectID := e.seedProject(t, "锁定-成功", "LCKO", seedProjectOpts{scriptText: "第一幕。\n小明：【去看看。】\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n小明：【去看看。】\n")

	code, body := e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/breakdowns/lock",
		map[string]any{
			"episode_id": episodeID, "script_version_id": versionID, "episode_code": "EP01",
			"create_asset_tasks": false,
			"view":               lockdownStoryboardView(episodeID, versionID),
		}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["project_id"] != projectID {
		t.Errorf("project_id=%v", body["project_id"])
	}
	if body["training_sample_count"] != float64(1) {
		t.Errorf("training_sample_count=%v", body["training_sample_count"])
	}
	version, _ := body["breakdown_version"].(float64)
	if version <= 0 {
		t.Errorf("breakdown_version=%v", body["breakdown_version"])
	}

	// 锁定后项目进入 task_assignment
	code, body = e.doJSON(t, http.MethodGet, "/api/projects/"+projectID, nil, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["current_stage"] != "task_assignment" {
		t.Errorf("current_stage=%v", body["current_stage"])
	}
}

// ---- episode hard delete ----

func TestDeleteEpisodeNotFound(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("942"), "director", "secret123")
	projectID := e.seedProject(t, "删集-404", "DELE", seedProjectOpts{scriptText: "第一幕。\n"})

	code, body := e.doJSON(t, http.MethodDelete, "/api/projects/"+projectID+"/episodes/"+bellUUID(),
		map[string]any{"confirm_episode_code": "EP01"}, dirTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Episode not found")
}

func TestDeleteEpisodeConfirmCodeMismatch(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("943"), "director", "secret123")
	projectID := e.seedProject(t, "删集-验证码", "DELC", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)

	code, body := e.doJSON(t, http.MethodDelete, "/api/projects/"+projectID+"/episodes/"+episodeID,
		map[string]any{"confirm_episode_code": "EP99"}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, fmt.Sprintf("删除确认不匹配，请输入分集编号 %s。", "EP01"))
}

func TestDeleteEpisodeFlow(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("944"), "director", "secret123")
	projectID := e.seedProject(t, "删集-成功", "DELS", seedProjectOpts{scriptText: "第一幕。\n小明：【去看看。】\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n小明：【去看看。】\n")

	code, body := e.doJSON(t, http.MethodDelete, "/api/projects/"+projectID+"/episodes/"+episodeID,
		map[string]any{"confirm_episode_code": "EP01", "delete_files": true}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["episode_code"] != "EP01" {
		t.Errorf("episode_code=%v", body["episode_code"])
	}
	counts, _ := body["deleted_counts"].(map[string]any)
	if counts == nil {
		t.Fatalf("deleted_counts 不存在: %v", body)
	}
	fc, _ := body["file_cleanup"].(map[string]any)
	if fc == nil {
		t.Fatalf("file_cleanup 不存在: %v", body)
	}

	// 删除后分集与剧本版本不可再读
	code, body = e.doJSON(t, http.MethodGet, fmt.Sprintf("/api/projects/%s/episodes/%s", projectID, episodeID), nil, dirTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Episode not found")

	// 剧本版本也随级联删除 → 404
	code, body = e.doJSON(t, http.MethodGet,
		fmt.Sprintf("/api/projects/%s/episodes/%s/versions/%s", projectID, episodeID, versionID), nil, dirTok)
	assertStatus(t, code, http.StatusNotFound, body)
}

func TestDeleteEpisodeRequiresDirectorOrAdmin(t *testing.T) {
	e := newEnv(t)
	artistTok := e.seedUserAs(t, uniquePhone("945"), "artist", "secret123")
	projectID := e.seedProject(t, "删集-角色", "DELR", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)

	code, body := e.doJSON(t, http.MethodDelete, "/api/projects/"+projectID+"/episodes/"+episodeID,
		map[string]any{"confirm_episode_code": "EP01"}, artistTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director or admin permission required")
}

// ---- P3f-e：分集删除文件清理（对齐 legacy cleanup_deleted_episode_files）----

// fileCleanupCounts 解析 file_cleanup 计数并断言 key 形状对齐 legacy。
func fileCleanupCounts(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	fc, _ := body["file_cleanup"].(map[string]any)
	if fc == nil {
		t.Fatalf("file_cleanup 不存在: %v", body)
	}
	for _, key := range []string{"succeeded", "failed", "shared_retained", "pending_retry", "failed_objects", "shared_objects"} {
		if _, ok := fc[key]; !ok {
			t.Fatalf("file_cleanup 缺字段 %s: %v", key, fc)
		}
	}
	return fc
}

func fcFloat(t *testing.T, fc map[string]any, key string) float64 {
	t.Helper()
	v, ok := fc[key].(float64)
	if !ok {
		t.Fatalf("file_cleanup[%s] 非数值: %#v", key, fc[key])
	}
	return v
}

func TestDeleteEpisodeKeepsFilesWhenDeleteFilesFalse(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("946"), "director", "secret123")
	projectID := e.seedProject(t, "删集-保留文件", "DELK", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕。\n")
	fileID := e.seedFile(t, projectID, nil, "DELK-F1")
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE script_versions SET source_file_id=$1 WHERE id=$2`, fileID, versionID); err != nil {
		t.Fatalf("link source file: %v", err)
	}

	code, body := e.doJSON(t, http.MethodDelete, "/api/projects/"+projectID+"/episodes/"+episodeID,
		map[string]any{"confirm_episode_code": "EP01", "delete_files": false}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	fc := fileCleanupCounts(t, body)
	if got := fcFloat(t, fc, "succeeded"); got != 0 {
		t.Errorf("delete_files=false succeeded=%v", got)
	}
	if got := fcFloat(t, fc, "pending_retry"); got != 0 {
		t.Errorf("delete_files=false pending_retry=%v", got)
	}

	// 文件行保留，但已解除 task_id/episode_id 绑定（对齐 legacy 无条件置空）。
	var still int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE id=$1`, fileID).Scan(&still); err != nil {
		t.Fatalf("query file: %v", err)
	}
	if still != 1 {
		t.Errorf("delete_files=false 应保留 files 行: count=%d", still)
	}
	var detached bool
	if err := e.pool.QueryRow(context.Background(),
		`SELECT (task_id IS NULL AND episode_id IS NULL) FROM files WHERE id=$1`, fileID).Scan(&detached); err != nil {
		t.Fatalf("query detach: %v", err)
	}
	if !detached {
		t.Errorf("files 行应已解除 task_id/episode_id 绑定")
	}
}

func TestDeleteEpisodeSharedFileRetained(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("947"), "director", "secret123")
	projectID := e.seedProject(t, "删集-共享保留", "DELS", seedProjectOpts{scriptText: "第一幕。\n"})
	ep1 := e.seedEpisode(t, projectID, "EP01", 1)
	v1 := e.seedScript(t, projectID, ep1, "第一幕。\n")
	ep2 := e.seedEpisode(t, projectID, "EP02", 2)
	v2 := e.seedScript(t, projectID, ep2, "第二幕。\n")
	fileID := e.seedFile(t, projectID, nil, "DELS-F1")
	for _, vid := range []string{v1, v2} {
		if _, err := e.pool.Exec(context.Background(),
			`UPDATE script_versions SET source_file_id=$1 WHERE id=$2`, fileID, vid); err != nil {
			t.Fatalf("link source file %s: %v", vid, err)
		}
	}

	code, body := e.doJSON(t, http.MethodDelete, "/api/projects/"+projectID+"/episodes/"+ep1,
		map[string]any{"confirm_episode_code": "EP01", "delete_files": true}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	fc := fileCleanupCounts(t, body)
	if got := fcFloat(t, fc, "shared_retained"); got != 1 {
		t.Errorf("shared_retained=%v", got)
	}
	if got := fcFloat(t, fc, "succeeded"); got != 0 {
		t.Errorf("succeeded=%v", got)
	}
	sharedObjs, _ := fc["shared_objects"].([]any)
	if len(sharedObjs) != 1 {
		t.Fatalf("shared_objects=%v", fc["shared_objects"])
	}
	first, _ := sharedObjs[0].(map[string]any)
	if first["reason"] != "仍被项目级或其他分集数据引用" {
		t.Errorf("shared reason=%v", first["reason"])
	}

	// 被 EP02 继续引用的文件行保留。
	var still int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE id=$1`, fileID).Scan(&still); err != nil {
		t.Fatalf("query file: %v", err)
	}
	if still != 1 {
		t.Errorf("被其他分集引用的文件行应保留: count=%d", still)
	}
	var linked bool
	if err := e.pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM script_versions WHERE id=$1 AND source_file_id=$2)`, v2, fileID).Scan(&linked); err != nil {
		t.Fatalf("query v2 link: %v", err)
	}
	if !linked {
		t.Errorf("EP02 剧本版本应继续引用该文件")
	}
}

func TestDeleteEpisodeCleansMinIOFiles(t *testing.T) {
	if !minioAvailable(t) {
		t.Skip("dev MinIO 不可达或未配置凭证，跳过分集删除对象清理契约测试")
	}
	e := newEnvWithMinIO(t, true)
	dirTok := e.seedUserAs(t, uniquePhone("948"), "director", "secret123")
	title := "删集-MinIO" + uniquePhone("94")
	e.cleanupProjectByTitle(t, title)
	projectID := e.seedProject(t, title, uniquePrefix(), seedProjectOpts{scriptText: "第一幕。\n小明：【去看看。】\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	sbID := e.seedStoryboard(t, projectID, 1, 1)
	taskID := e.seedFileTask(t, projectID, "text_to_image", "todo", seedFileTaskOpts{StoryboardID: sbID})

	body, ct := taskUploadForm(taskID, "删.png", []byte("episode cleanup"), nil)
	code, resp := e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, dirTok)
	assertStatus(t, code, http.StatusCreated, resp)
	fileID := str(resp["id"])
	objectKey := str(resp["object_key"])
	if fileID == "" || objectKey == "" {
		t.Fatalf("上传响应缺 id/object_key: %v", resp)
	}

	code, delBody := e.doJSON(t, http.MethodDelete, "/api/projects/"+projectID+"/episodes/"+episodeID,
		map[string]any{"confirm_episode_code": "EP01", "delete_files": true}, dirTok)
	assertStatus(t, code, http.StatusOK, delBody)
	fc := fileCleanupCounts(t, delBody)
	if got := fcFloat(t, fc, "succeeded"); got != 1 {
		t.Errorf("succeeded=%v (期望 1)", got)
	}
	if got := fcFloat(t, fc, "failed"); got != 0 {
		t.Errorf("failed=%v (期望 0)", got)
	}

	// files 行删除 + MinIO 对象删除。
	var filesLeft int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM files WHERE project_id=$1`, projectID).Scan(&filesLeft); err != nil {
		t.Fatalf("query files: %v", err)
	}
	if filesLeft != 0 {
		t.Errorf("清理成功后 files 行应删除: got=%d", filesLeft)
	}
	client, err := storage.New(e.cfg.MinIO)
	if err != nil {
		t.Fatalf("build storage client: %v", err)
	}
	if _, err := client.Stat(context.Background(), e.cfg.MinIO.Bucket, objectKey); err == nil {
		t.Errorf("MinIO 对象应已被删除: %s/%s", e.cfg.MinIO.Bucket, objectKey)
	}
}

// bellUUID 稳定假 UUID（避免空串触发 pgx 校验异常）。
func bellUUID() string { return strings.Repeat("0", 8) + "-0000-0000-0000-" + strings.Repeat("0", 12) }
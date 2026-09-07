package api_test

// P3e task 域 HTTP 契约测试（对齐 legacy app/api/routes/tasks.py + projects.py 分组路由）：
//   - /tasks 列表可见性（assignee / 项目权限 / director 全量）、status 枚举 422、404/403 逐字
//   - PATCH /tasks/{id}：非 director 改状态 403、完成守卫、改派
//   - /tasks/{id}/prompts：POST 201 默认字段、completed/human_temporary 守卫 400
//   - submissions 登记/编辑/软删；submit/review 状态机 + uuid5 request_id 幂等
//   - bulk-submit / bulk-review 单一事务 + 重复任务 400
//   - archive（导演）与 primary-submission（导演）守卫
//   - /workspace/tasks（执行人作用域）+ /workspace/reviews（导演 403 逐字）
//   - /projects/{id}/storyboards、/board、/scene-gating、/storyboard-video-tasks、/task-assignments
//
// 注：分组读路由的权限模型对齐 legacy _can_access_project——director/admin、任意 project_role
// 之外，项目内有「可见任务」的执行人（assignee）也可读（结果仍过滤到自己的责任范围）。
// 需要 dev DB + .env 的 auth secret（缺失自动 skip，复用 newEnv）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"cineforge/server/internal/auth"
)

// ---- 种子辅助 ----

// seedUserAsID 创建用户并返回 (id, access_token)。
func (e *testEnv) seedUserAsID(t *testing.T, phone, role, password string) (string, string) {
	t.Helper()
	id := e.seedUser(t, phone, role, password)
	return id, e.accessToken(t, phone, password)
}

// seedTaskOpts 可选项（task_variant / variant_kind / storyboard_id）。
type seedTaskOpts struct {
	TaskVariant string
	VariantKind string
	StoryboardID string
}

// seedTask 直接写 dev DB 插入最小任务行（NOT NULL 列齐全，其余默认）。
func (e *testEnv) seedTask(t *testing.T, projectID, assigneeID, taskType, status string, opts seedTaskOpts) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, storyboard_id, task_type, title, assignee_id, status,
		   task_variant, variant_kind, asset_context_outdated, is_retired, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,false,false,now(),now())`,
		id, projectID, nzStr(opts.StoryboardID), taskType, "测试任务-"+strings.ToUpper(id[:6]),
		nzStr(assigneeID), status, nzStr(opts.TaskVariant), nzStr(opts.VariantKind)); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM submissions WHERE task_id = $1`, id)
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM task_submission_batches WHERE task_id = $1`, id)
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM task_prompts WHERE task_id = $1`, id)
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM tasks WHERE id = $1`, id)
	})
	return id
}

// seedStoryboard 直接写 dev DB 插入分镜行（characters/'[]' 等 NOT NULL 列齐全）。
func (e *testEnv) seedStoryboard(t *testing.T, projectID string, episodeNo, orderNum int32) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO storyboards (id, project_id, episode_num, order_num, description, characters,
		   keyframes, mirror_shots, status, storyboard_code, scene_code, scene_name, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,now(),now())`,
		id, projectID, episodeNo, orderNum, "小明站在门前准备出门。", `["小明"]`, `[]`, `[]`, "todo",
		"T-SEG-EP01-01-F001", "SCENE-01", "街道"); err != nil {
		t.Fatalf("seed storyboard: %v", err)
	}
	t.Cleanup(func() { _, _ = e.pool.Exec(context.Background(), `DELETE FROM storyboards WHERE id = $1`, id) })
	return id
}

// seedPermission 给用户加项目角色权限行（project_role:owner/manager/lead/member/viewer）。
func (e *testEnv) seedPermission(t *testing.T, userID, projectID, ptype string) {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO permissions (id, user_id, project_id, permission_type, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,now(),now())`, id, userID, projectID, ptype); err != nil {
		t.Fatalf("seed permission: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(),
			`DELETE FROM permissions WHERE user_id = $1 AND project_id = $2 AND permission_type = $3`,
			userID, projectID, ptype)
	})
}

func nzStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// createSubmission 走 HTTP 登记一个草稿候选并返回 submission id。
func (e *testEnv) createSubmission(t *testing.T, taskID, token string, no int) string {
	t.Helper()
	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/submissions",
		map[string]any{
			"file_path":   fmt.Sprintf("s3://bucket/f%d.png", no),
			"file_type":   "image",
			"prompt_text": "prompt-" + strconv.Itoa(no),
		}, token)
	if code != http.StatusCreated {
		t.Fatalf("create submission status=%d body=%v", code, body)
	}
	sid, _ := body["id"].(string)
	if sid == "" {
		t.Fatalf("create submission 无 id: %v", body)
	}
	return sid
}

// arrData 解码数组响应（doJSON 只支持对象）。
func arrData(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode array: %v (raw=%s)", err, raw)
	}
	return out
}

// assertStatusRaw 对原始字节响应断言状态码。
func assertStatusRaw(t *testing.T, got, want int, raw []byte) {
	t.Helper()
	if got != want {
		t.Fatalf("status=%d want=%d raw=%s", got, want, raw)
	}
}

// num 转 float64（JSON 数字统一）。
func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

// str 转 string。
func str(v any) string {
	s, _ := v.(string)
	return s
}

// requestUUID 生成唯一 request_id（submit/review/bulk 幂等键要求合法 UUID，逐字对齐 legacy schema）。
func requestUUID(t *testing.T) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen request uuid: %v", err)
	}
	return id
}

// ---- /tasks 列表 / 详情可见性 ----

func TestTaskListGetVisibilityGuards(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("251"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("252"), "artist", "secret123")
	artistBTok := e.seedUserAs(t, uniquePhone("253"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-任务可见性", "E3VT", seedProjectOpts{scriptText: "第一幕。\n"})
	taskID := e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})

	// artist A（执行人）列表与详情可见
	code, raw := e.do(t, http.MethodGet, "/api/tasks?project_id="+projectID, nil, artistATok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if n := len(arrData(t, raw)); n != 1 {
		t.Fatalf("artist assignee 列表任务数=%d 期望1", n)
	}
	code, body := e.doJSON(t, http.MethodGet, "/api/tasks/"+taskID, nil, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	if body["id"] != taskID {
		t.Errorf("task id=%v", body["id"])
	}
	if body["status"] != "todo" {
		t.Errorf("status=%v", body["status"])
	}

	// artist B（无执行人、非成员）列表为空、详情 403
	code, raw = e.do(t, http.MethodGet, "/api/tasks?project_id="+projectID, nil, artistBTok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if n := len(arrData(t, raw)); n != 0 {
		t.Fatalf("非成员 artist 列表任务数=%d 期望0", n)
	}
	code, body = e.doJSON(t, http.MethodGet, "/api/tasks/"+taskID, nil, artistBTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Task permission denied")

	// director 全量
	code, raw = e.do(t, http.MethodGet, "/api/tasks?project_id="+projectID, nil, dirTok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if n := len(arrData(t, raw)); n != 1 {
		t.Fatalf("director 列表任务数=%d 期望1", n)
	}

	// status 参数非法 → 422
	code, body = e.doJSON(t, http.MethodGet, "/api/tasks?project_id="+projectID+"&status=warp", nil, dirTok)
	assertStatus(t, code, http.StatusUnprocessableEntity, body)
	detail(t, body, "请求体格式错误")

	// 找不到任务 → 404
	code, body = e.doJSON(t, http.MethodGet, "/api/tasks/"+bellUUID(), nil, dirTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Task not found")
}

// ---- PATCH /tasks/{id} 状态与改派守卫 ----

func TestTaskUpdateGuardsAndFields(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("261"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("262"), "artist", "secret123")
	artistBID := e.seedUser(t, uniquePhone("263"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-更新守卫", "E3UG", seedProjectOpts{scriptText: "第一幕。\n"})
	taskID := e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})

	// 执行人不能改状态（director/admin 专有）
	code, body := e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID, map[string]any{"status": "completed"}, artistATok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Task permission denied")

	// director 置 completed：无主母版 → 400
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID, map[string]any{"status": "completed"}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "任务必须先完成成果审核并指定主母版。")

	// director 改字段 → 200
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID,
		map[string]any{"status": "todo", "production_model": "Seedream"}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if str(body["production_model"]) != "Seedream" {
		t.Errorf("production_model=%v", body["production_model"])
	}

	// 状态枚举非法 → 422
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID, map[string]any{"status": "warp"}, dirTok)
	assertStatus(t, code, http.StatusUnprocessableEntity, body)
	detail(t, body, "请求体格式错误")

	// director 改派需要原因
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID,
		map[string]any{"assignee_id": artistBID}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "改派任务必须填写改派原因。")

	// director 改派成功 → 执行人切换
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID,
		map[string]any{"assignee_id": artistBID, "reassignment_reason": "工作量不均"}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["assignee_id"] != artistBID {
		t.Errorf("assignee_id=%v 期望 %s", body["assignee_id"], artistBID)
	}
}

// ---- /tasks/{id}/prompts 生命周期与守卫 ----

func TestTaskPromptLifecycleAndGuards(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("271"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("272"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-提示词", "E3PR", seedProjectOpts{scriptText: "第一幕。\n"})
	todoID := e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})

	// POST 201，默认字段对齐 TaskPromptCreate
	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/"+todoID+"/prompts",
		map[string]any{"prompt_type": "human_edit", "prompt_text": "先生，请画。"}, artistATok)
	assertStatus(t, code, http.StatusCreated, body)
	if pid := str(body["id"]); pid == "" {
		t.Fatalf("prompt 无 id: %v", body)
	}
	if str(body["prompt_type"]) != "human_edit" {
		t.Errorf("prompt_type=%v", body["prompt_type"])
	}
	if str(body["source"]) != "human" {
		t.Errorf("source=%v", body["source"])
	}
	if body["copied"] != false {
		t.Errorf("copied=%v", body["copied"])
	}
	if num(body["version_no"]) != 1 {
		t.Errorf("version_no=%v", body["version_no"])
	}

	// GET 列表
	code, raw := e.do(t, http.MethodGet, "/api/tasks/"+todoID+"/prompts", nil, artistATok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	items := arrData(t, raw)
	if len(items) != 1 {
		t.Fatalf("prompts 数量=%d 期望1", len(items))
	}
	if str(items[0]["prompt_text"]) != "先生，请画。" {
		t.Errorf("prompt_text=%v", items[0]["prompt_text"])
	}

	// completed → 400
	doneID := e.seedTask(t, projectID, artistAID, "text_to_image", "completed", seedTaskOpts{})
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+doneID+"/prompts",
		map[string]any{"prompt_text": "x"}, artistATok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "已完成任务不能保存或重新生成提示词；如需换版，请先由导演发起返工。")

	// human_temporary → 400
	tempID := e.seedTask(t, projectID, artistAID, "text_to_image", "todo",
		seedTaskOpts{VariantKind: "human_temporary"})
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+tempID+"/prompts",
		map[string]any{"prompt_text": "x"}, artistATok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "人工临时生产任务已跳过提示词流程，不能保存任务提示词。")

	// 不存在任务 prompts → 404
	code, body = e.doJSON(t, http.MethodGet, "/api/tasks/"+bellUUID()+"/prompts", nil, dirTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Task not found")
}

// ---- submissions 登记 / 编辑 / 软删 ----

func TestTaskSubmissionMetadataAndDelete(t *testing.T) {
	e := newEnv(t)
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("281"), "artist", "secret123")
	artistBTok := e.seedUserAs(t, uniquePhone("282"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-候选编辑", "E3ML", seedProjectOpts{scriptText: "第一幕。\n"})
	taskID := e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})

	// 登记 201
	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/submissions",
		map[string]any{"file_path": "s3://bucket/1.png", "file_type": "image", "prompt_text": "p"},
		artistATok)
	assertStatus(t, code, http.StatusCreated, body)
	sid := str(body["id"])
	if sid == "" {
		t.Fatalf("submission 无 id: %v", body)
	}
	if body["status"] != "draft" {
		t.Errorf("status=%v", body["status"])
	}

	// 缺 file_path/file_type → 422
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/submissions",
		map[string]any{"file_path": "x"}, artistATok)
	assertStatus(t, code, http.StatusUnprocessableEntity, body)
	detail(t, body, "请求体格式错误")

	// GET 列表
	code, raw := e.do(t, http.MethodGet, "/api/tasks/"+taskID+"/submissions", nil, artistATok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if n := len(arrData(t, raw)); n != 1 {
		t.Fatalf("submissions 数量=%d 期望1", n)
	}

	// PATCH 展示字段
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID+"/submissions/"+sid,
		map[string]any{"view_label": "正面", "description": "新说明"}, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	if str(body["view_label"]) != "正面" || str(body["description"]) != "新说明" {
		t.Errorf("patch view=%v desc=%v", body["view_label"], body["description"])
	}

	// 非本人/非写角色改 → 403
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID+"/submissions/"+sid,
		map[string]any{"view_label": "x"}, artistBTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Task permission denied")

	// 软删草稿 → {"archived": sid}
	code, body = e.doJSON(t, http.MethodDelete, "/api/tasks/"+taskID+"/submissions/"+sid, nil, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	if str(body["archived"]) != sid {
		t.Errorf("archived=%v 期望 %s", body["archived"], sid)
	}
	// 已归档重复删 → 幂等 200
	code, body = e.doJSON(t, http.MethodDelete, "/api/tasks/"+taskID+"/submissions/"+sid, nil, artistATok)
	assertStatus(t, code, http.StatusOK, body)
}

// ---- submit / review 状态机 + 幂等 ----

func TestTaskSubmitReviewFlow(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("291"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("292"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-提交审核", "E3SR", seedProjectOpts{scriptText: "第一幕。\n"})
	taskID := e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})

	// 无候选提交 → 400（request_id 可选，省略即走唯一性/状态守卫）
	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/submit",
		map[string]any{}, artistATok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "请先上传至少一个候选成果。")

	sid1 := e.createSubmission(t, taskID, artistATok, 1)
	sid2 := e.createSubmission(t, taskID, artistATok, 2)

	submitReq := requestUUID(t)
	// 提交 → 200 batch
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/submit",
		map[string]any{"step": "result", "request_id": submitReq}, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	batchID := str(body["id"])
	if batchID == "" || str(body["status"]) != "submitted" {
		t.Fatalf("submit batch=%v status=%v", batchID, body["status"])
	}
	if num(body["version_no"]) != 1 {
		t.Errorf("version_no=%v", body["version_no"])
	}

	// 同一 request_id 幂等回放 → 同 batch
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/submit",
		map[string]any{"step": "result", "request_id": submitReq}, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	if str(body["id"]) != batchID {
		t.Errorf("幂等回放 batch=%v 期望 %s", body["id"], batchID)
	}

	// 不同 request_id 且已提交 → 400
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/submit",
		map[string]any{"request_id": requestUUID(t)}, artistATok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "当前任务状态不能重复提交。")

	// 任务进入 reviewing
	code, body = e.doJSON(t, http.MethodGet, "/api/tasks/"+taskID, nil, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	if str(body["status"]) != "reviewing" {
		t.Errorf("task status=%v", body["status"])
	}

	// 执行人审核 → 403 director 逐字
	code, body = e.doJSON(t, http.MethodPost, fmt.Sprintf("/api/tasks/%s/submission-batches/%s/review", taskID, batchID),
		map[string]any{"decision": "approve", "selected_submission_ids": []string{sid1}}, artistATok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director review permission required")

	// 导演 rework 缺原因 → 400
	code, body = e.doJSON(t, http.MethodPost, fmt.Sprintf("/api/tasks/%s/submission-batches/%s/review", taskID, batchID),
		map[string]any{"decision": "rework"}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "退回返工时必须填写具体修改原因。")

	// 导演 approve 无候选 → 400
	code, body = e.doJSON(t, http.MethodPost, fmt.Sprintf("/api/tasks/%s/submission-batches/%s/review", taskID, batchID),
		map[string]any{"decision": "approve", "selected_submission_ids": []string{}}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "审核通过时必须选择至少一个定版成果。")

	// 选中的候选不属于本批次 → 400
	code, body = e.doJSON(t, http.MethodPost, fmt.Sprintf("/api/tasks/%s/submission-batches/%s/review", taskID, batchID),
		map[string]any{"decision": "approve", "selected_submission_ids": []string{bellUUID()}}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "定版成果必须属于当前待审核批次。")

	// approve 成功 → 任务 completed；s1 主母版
	code, body = e.doJSON(t, http.MethodPost, fmt.Sprintf("/api/tasks/%s/submission-batches/%s/review", taskID, batchID),
		map[string]any{
			"decision": "approve", "selected_submission_ids": []string{sid1, sid2},
			"primary_submission_id": sid1, "comment": "OK", "request_id": requestUUID(t),
		}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if str(body["status"]) != "completed" {
		t.Errorf("review 后 task status=%v", body["status"])
	}
	code, body = e.doJSON(t, http.MethodGet, "/api/tasks/"+taskID, nil, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	subs := body["submissions"].([]any)
	for _, item := range subs {
		s := item.(map[string]any)
		if str(s["id"]) == sid1 {
			if str(s["status"]) != "primary_master" || s["is_primary"] != true {
				t.Errorf("s1 status=%v primary=%v", s["status"], s["is_primary"])
			}
		} else if str(s["id"]) == sid2 {
			if str(s["status"]) != "alternate_master" || s["is_primary"] != false {
				t.Errorf("s2 status=%v primary=%v", s["status"], s["is_primary"])
			}
		}
	}
}

// ---- 批次幂等 / bulk ----

func TestTaskBulkSubmitReviewIdempotency(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("301"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("302"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-批量", "E3BL", seedProjectOpts{scriptText: "第一幕。\n"})
	t1 := e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})
	t2 := e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})
	s1 := e.createSubmission(t, t1, artistATok, 1)
	s2 := e.createSubmission(t, t2, artistATok, 1)

	// 重复任务 → 400
	dupSubmitReq := requestUUID(t)
	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/submission-batches/bulk-submit",
		map[string]any{"request_id": dupSubmitReq, "entries": []map[string]any{{"task_id": t1}, {"task_id": t1}}},
		artistATok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "批量提交不能包含重复任务。")

	// 正常批量提交 → 两个 batch
	submitReq := requestUUID(t)
	code, raw := e.do(t, http.MethodPost, "/api/tasks/submission-batches/bulk-submit",
		map[string]any{"request_id": submitReq, "entries": []map[string]any{{"task_id": t1}, {"task_id": t2}}}, artistATok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	batches := arrData(t, raw)
	if len(batches) != 2 {
		t.Fatalf("bulk submit 批次数=%d 期望2", len(batches))
	}
	b1, b2 := str(batches[0]["id"]), str(batches[1]["id"])

	// 同 request_id 幂等回放 → 仍 200
	code, raw = e.do(t, http.MethodPost, "/api/tasks/submission-batches/bulk-submit",
		map[string]any{"request_id": submitReq, "entries": []map[string]any{{"task_id": t1}, {"task_id": t2}}}, artistATok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if n := len(arrData(t, raw)); n != 2 {
		t.Fatalf("幂等回放批次数=%d 期望2", n)
	}

	// 执行人批量审核 → 403
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/submission-batches/bulk-review",
		map[string]any{"request_id": requestUUID(t), "decision": "approve", "comment": "ok",
			"entries": []map[string]any{{"task_id": t1, "batch_id": b1, "selected_submission_ids": []string{s1}}}},
		artistATok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director review permission required")

	// 重复任务 → 400
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/submission-batches/bulk-review",
		map[string]any{"request_id": requestUUID(t), "decision": "approve",
			"entries": []map[string]any{
				{"task_id": t1, "batch_id": b1},
				{"task_id": t1, "batch_id": b1},
			}}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "批量审核不能包含重复任务。")

	// 导演批量审核通过 → 两个任务 completed
	code, raw = e.do(t, http.MethodPost, "/api/tasks/submission-batches/bulk-review",
		map[string]any{"request_id": requestUUID(t), "decision": "approve", "comment": "都过",
			"entries": []map[string]any{
				{"task_id": t1, "batch_id": b1, "primary_submission_id": s1, "selected_submission_ids": []string{s1}},
				{"task_id": t2, "batch_id": b2, "primary_submission_id": s2, "selected_submission_ids": []string{s2}},
			}}, dirTok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	views := arrData(t, raw)
	if len(views) != 2 {
		t.Fatalf("bulk review 返回任务数=%d 期望2", len(views))
	}
	for _, v := range views {
		if str(v["status"]) != "completed" {
			t.Errorf("bulk review 后 task status=%v", v["status"])
		}
	}
}

// ---- archive / primary 守卫 ----

func TestTaskArchiveAndPrimaryGuards(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("311"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("312"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-归档守卫", "E3AR", seedProjectOpts{scriptText: "第一幕。\n"})
	taskID := e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})
	sid := e.createSubmission(t, taskID, artistATok, 1)

	// 执行人不能归档 → 403 director archive 逐字
	code, body := e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID+"/submissions/"+sid+"/archive",
		map[string]any{"archived": true}, artistATok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director archive permission required")

	// 导演归档草稿 → 200 is_archived=true；恢复 → false
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID+"/submissions/"+sid+"/archive",
		map[string]any{"archived": true}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["is_archived"] != true {
		t.Errorf("is_archived=%v", body["is_archived"])
	}
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID+"/submissions/"+sid+"/archive",
		map[string]any{"archived": false}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if body["is_archived"] != false {
		t.Errorf("is_archived=%v", body["is_archived"])
	}

	// 草稿不能设为主母版 → 400
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID+"/primary-submission",
		map[string]any{"submission_id": sid}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "草稿、待审核、已驳回、已归档或已作废成果不能设为主母版。")

	// 找不到候选 → 404
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID+"/primary-submission",
		map[string]any{"submission_id": bellUUID()}, dirTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Task or submission not found")

	// 执行人指定主母版 → 403 director review 逐字
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID+"/primary-submission",
		map[string]any{"submission_id": sid}, artistATok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director review permission required")
}

// ---- 通过后换主母版 ----

func TestTaskChangePrimarySubmission(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("321"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("322"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-换主母版", "E3CP", seedProjectOpts{scriptText: "第一幕。\n"})
	taskID := e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})
	s1 := e.createSubmission(t, taskID, artistATok, 1)
	s2 := e.createSubmission(t, taskID, artistATok, 2)

	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/submit",
		map[string]any{"request_id": requestUUID(t)}, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	batchID := str(body["id"])

	// approve s1 为主母版
	code, body = e.doJSON(t, http.MethodPost, fmt.Sprintf("/api/tasks/%s/submission-batches/%s/review", taskID, batchID),
		map[string]any{"decision": "approve", "selected_submission_ids": []string{s1, s2},
			"primary_submission_id": s1, "request_id": requestUUID(t)}, dirTok)
	assertStatus(t, code, http.StatusOK, body)

	// 换主母版 → s2 主、s1 备
	code, body = e.doJSON(t, http.MethodPatch, "/api/tasks/"+taskID+"/primary-submission",
		map[string]any{"submission_id": s2}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	subs := body["submissions"].([]any)
	byID := map[string]map[string]any{}
	for _, item := range subs {
		s := item.(map[string]any)
		byID[str(s["id"])] = s
	}
	if str(byID[s2]["status"]) != "primary_master" || byID[s2]["is_primary"] != true {
		t.Errorf("s2 status=%v primary=%v", byID[s2]["status"], byID[s2]["is_primary"])
	}
	if str(byID[s1]["status"]) != "alternate_master" || byID[s1]["is_primary"] != false {
		t.Errorf("s1 status=%v primary=%v", byID[s1]["status"], byID[s1]["is_primary"])
	}
}

// ---- 工作台 ----

func TestWorkspaceScoping(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("331"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("332"), "artist", "secret123")
	artistBTok := e.seedUserAs(t, uniquePhone("333"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-工作台", "E3WS", seedProjectOpts{scriptText: "第一幕。\n"})
	e.seedTask(t, projectID, artistAID, "text_to_image", "todo", seedTaskOpts{})

	// artist A：只有自己 todo 任务
	code, body := e.doJSON(t, http.MethodGet, "/api/workspace/tasks", nil, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	stats, _ := body["stats"].(map[string]any)
	if num(stats["todo"]) != 1 || num(stats["completed"]) != 0 {
		t.Errorf("A workspace stats=%v", stats)
	}

	// artist B：无任务
	code, body = e.doJSON(t, http.MethodGet, "/api/workspace/tasks", nil, artistBTok)
	assertStatus(t, code, http.StatusOK, body)
	stats, _ = body["stats"].(map[string]any)
	if num(stats["todo"]) != 0 {
		t.Errorf("B workspace stats=%v", stats)
	}

	// director 指定 assignee 过滤 → 命中
	code, body = e.doJSON(t, http.MethodGet, "/api/workspace/tasks?assignee_id="+artistAID, nil, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	stats, _ = body["stats"].(map[string]any)
	if num(stats["todo"]) != 1 {
		t.Errorf("director 过滤 assignee stats=%v", stats)
	}

	// 非 director 不能访问审核工作台
	code, body = e.doJSON(t, http.MethodGet, "/api/workspace/reviews", nil, artistATok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Project review permission required")

	// director 审核工作台可访问
	code, body = e.doJSON(t, http.MethodGet, "/api/workspace/reviews", nil, dirTok)
	assertStatus(t, code, http.StatusOK, body)
}

// ---- 分组：storyboards / board / scene-gating ----

func TestProjectGroupingStoryboardsBoardGating(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("341"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("342"), "artist", "secret123")
	artistBTok := e.seedUserAs(t, uniquePhone("343"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-分组读", "E3GR", seedProjectOpts{scriptText: "第一幕。\n"})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	sbID := e.seedStoryboard(t, projectID, 1, 1)
	taskID := e.seedTask(t, projectID, artistAID, "text_to_image", "todo",
		seedTaskOpts{StoryboardID: sbID})
	// artist A 作为项目成员可读分组接口
	e.seedPermission(t, artistAID, projectID, "project_role:member")

	// storyboards
	code, raw := e.do(t, http.MethodGet, "/api/projects/"+projectID+"/storyboards", nil, artistATok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	rows := arrData(t, raw)
	if len(rows) != 1 || str(rows[0]["id"]) != sbID {
		t.Fatalf("storyboards=%v 期望包含 %s", rows, sbID)
	}
	if str(rows[0]["storyboard_code"]) != "T-SEG-EP01-01-F001" {
		t.Errorf("storyboard_code=%v", rows[0]["storyboard_code"])
	}

	// episode_id 过滤
	code, raw = e.do(t, http.MethodGet, "/api/projects/"+projectID+"/storyboards?episode_id="+episodeID, nil, dirTok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if n := len(arrData(t, raw)); n != 1 {
		t.Fatalf("按分集过滤 storyboards=%d 期望1", n)
	}

	// board（导演全量）
	code, raw = e.do(t, http.MethodGet, "/api/projects/"+projectID+"/board", nil, dirTok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	boardRows := arrData(t, raw)
	if len(boardRows) != 1 || str(boardRows[0]["storyboard_id"]) != sbID {
		t.Fatalf("board=%v", boardRows)
	}
	tasks, _ := boardRows[0]["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("board tasks=%v", boardRows[0]["tasks"])
	}
	if str(tasks[0].(map[string]any)["id"]) != taskID {
		t.Errorf("board task id=%v", tasks[0])
	}

	// scene-gating：无资产 → 空数组
	code, raw = e.do(t, http.MethodGet, "/api/projects/"+projectID+"/scene-gating", nil, dirTok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if n := len(arrData(t, raw)); n != 0 {
		t.Fatalf("scene-gating=%s 期望空数组", raw)
	}

	// 非成员 artist（无 project_role、无任务）→ 403
	for _, path := range []string{
		"/api/projects/" + projectID + "/storyboards",
		"/api/projects/" + projectID + "/board",
		"/api/projects/" + projectID + "/scene-gating",
	} {
		code, body := e.doJSON(t, http.MethodGet, path, nil, artistBTok)
		assertStatus(t, code, http.StatusForbidden, body)
		detail(t, body, "Project permission denied")
	}

	// 非成员执行人（无 project_role，但项目内有可见任务）→ visible-task fallback 放行
	artistCid, artistCTok := e.seedUserAsID(t, uniquePhone("344"), "artist", "secret123")
	sbC := e.seedStoryboard(t, projectID, 1, 2)
	taskC := e.seedTask(t, projectID, artistCid, "text_to_image", "todo", seedTaskOpts{StoryboardID: sbC})
	// storyboards：C 只看到自己的分镜
	code, raw = e.do(t, http.MethodGet, "/api/projects/"+projectID+"/storyboards", nil, artistCTok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if rows := arrData(t, raw); len(rows) != 1 || str(rows[0]["id"]) != sbC {
		t.Fatalf("非成员执行人 storyboards=%v 期望只含自己的 %s", rows, sbC)
	}
	// board：可读且只含 C 的责任分镜与任务
	code, raw = e.do(t, http.MethodGet, "/api/projects/"+projectID+"/board", nil, artistCTok)
	assertStatusRaw(t, code, http.StatusOK, raw)
	boardRows = arrData(t, raw)
	if len(boardRows) != 1 || str(boardRows[0]["storyboard_id"]) != sbC {
		t.Fatalf("非成员执行人 board=%v 期望只含自己的 %s", boardRows, sbC)
	}
	btasks, _ := boardRows[0]["tasks"].([]any)
	if len(btasks) != 1 || str(btasks[0].(map[string]any)["id"]) != taskC {
		t.Fatalf("非成员执行人 board tasks=%v 期望含 %s", boardRows[0]["tasks"], taskC)
	}
	// scene-gating：放行（不 403）
	code, raw = e.do(t, http.MethodGet, "/api/projects/"+projectID+"/scene-gating", nil, artistCTok)
	assertStatusRaw(t, code, http.StatusOK, raw)

	// 项目不存在 → 404
	code, body := e.doJSON(t, http.MethodGet, "/api/projects/"+bellUUID()+"/storyboards", nil, dirTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Project not found")
}

// ---- 分组：storyboard-video-tasks + bulk-assign ----

func TestCreateStoryboardVideoTasksAndBulkAssign(t *testing.T) {
	e := newEnv(t)
	dirTok := e.seedUserAs(t, uniquePhone("351"), "director", "secret123")
	artistAID, artistATok := e.seedUserAsID(t, uniquePhone("352"), "artist", "secret123")
	artistBTok := e.seedUserAs(t, uniquePhone("353"), "artist", "secret123")

	projectID := e.seedProject(t, "P3e-生成分镜任务", "E3SV", seedProjectOpts{scriptText: "第一幕。\n"})
	e.seedStoryboard(t, projectID, 1, 1)

	// 导演创建分镜视频任务 → 201 created_tasks=1
	code, body := e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/storyboard-video-tasks",
		map[string]any{"episode_code": "EP01", "scene_name": "街道"}, dirTok)
	assertStatus(t, code, http.StatusCreated, body)
	if num(body["created_tasks"]) != 1 {
		t.Fatalf("created_tasks=%v", body["created_tasks"])
	}
	if num(body["task_count"]) < 1 {
		t.Errorf("task_count=%v", body["task_count"])
	}
	if body["episode_code"] != "EP01" {
		t.Errorf("episode_code=%v", body["episode_code"])
	}

	// 再次调用幂等 → 不再新建
	code, body = e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/storyboard-video-tasks",
		map[string]any{"episode_code": "EP01", "scene_name": "街道"}, dirTok)
	assertStatus(t, code, http.StatusCreated, body)
	if num(body["created_tasks"]) != 0 {
		t.Errorf("二次 created_tasks=%v 期望0", body["created_tasks"])
	}

	// 项目不存在 → 404
	code, body = e.doJSON(t, http.MethodPost, "/api/projects/"+bellUUID()+"/storyboard-video-tasks",
		map[string]any{"episode_code": "EP01"}, dirTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Project not found")

	// 非成员 artist → 403 "Project permission denied"
	code, body = e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/storyboard-video-tasks",
		map[string]any{"episode_code": "EP01"}, artistBTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Project permission denied")

	// ---- bulk assign ----

	// 非 director → middleware 403
	code, body = e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/task-assignments",
		map[string]any{"scope": "all_storyboards", "key": ""}, artistATok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Director or admin permission required")

	// 未知范围 → 400
	code, body = e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/task-assignments",
		map[string]any{"scope": "bogus", "key": "x", "assignee_id": artistAID}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "未知的分配范围: bogus")

	// 项目不存在 → 404
	code, body = e.doJSON(t, http.MethodPost, "/api/projects/"+bellUUID()+"/task-assignments",
		map[string]any{"scope": "all_assets", "key": ""}, dirTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Project not found")

	// 两个未分配资产任务 → 批量分配 2 个给 artist A
	t1 := e.seedTask(t, projectID, "", "asset", "todo", seedTaskOpts{})
	t2 := e.seedTask(t, projectID, "", "asset", "todo", seedTaskOpts{})
	code, body = e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/task-assignments",
		map[string]any{"scope": "all_assets", "key": "", "assignee_id": artistAID}, dirTok)
	assertStatus(t, code, http.StatusOK, body)
	if num(body["assigned"]) != 2 || num(body["reassigned"]) != 0 {
		t.Errorf("assign assigned=%v reassigned=%v", body["assigned"], body["reassigned"])
	}
	code, body = e.doJSON(t, http.MethodGet, "/api/tasks/"+t1, nil, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	if body["assignee_id"] != artistAID {
		t.Errorf("t1 assignee=%v", body["assignee_id"])
	}
	code, body = e.doJSON(t, http.MethodGet, "/api/tasks/"+t2, nil, artistATok)
	assertStatus(t, code, http.StatusOK, body)
	if body["assignee_id"] != artistAID {
		t.Errorf("t2 assignee=%v", body["assignee_id"])
	}

	// 改派给他人缺原因 → 400
	artistBID := e.seedUser(t, uniquePhone("354"), "artist", "secret123")
	code, body = e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/task-assignments",
		map[string]any{"scope": "all_assets", "key": "", "assignee_id": artistBID}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "批量改派必须填写改派原因。")

	// 执行人无效 → 400
	code, body = e.doJSON(t, http.MethodPost, "/api/projects/"+projectID+"/task-assignments",
		map[string]any{"scope": "all_assets", "key": "", "assignee_id": "00000000-0000-0000-0000-000000000001", "reassignment_reason": "x"}, dirTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "执行人无效、已停用或不是可分配的制作人员。")
}
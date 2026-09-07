package api_test

// P3f-b 文件域 HTTP 契约测试（对齐 legacy app/api/routes/files.py + services/storage.py）：
//   - POST /files/task-upload 守卫逐字：404/403 Task、locked/status 400、扩展名 400、422、401
//   - token 字节兼容：错误/过期/换目的 → 401 逐字；user disabled → 401 逐字
//   - _authorized_file：404 / task_id NULL 403 / 越权 403 / 公司资产库放行
//   - content/stream/download Range 200/206/416、Content-Disposition（MinIO 门控）
//
// 需要 dev DB + .env 的 auth secret；涉及对象存储的用例在 dev MinIO 不可达时 t.Skip。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/testutil"
)

// minioAvailable dev MinIO 是否可归档（凭证 + 存活探针）。缺一 → 跳过对象存储契约用例。
func minioAvailable(t *testing.T) bool {
	t.Helper()
	env := testutil.EnvMap(t)
	if env["CINEFORGE_MINIO_ACCESS_KEY"] == "" || env["CINEFORGE_MINIO_SECRET_KEY"] == "" {
		return false
	}
	endpoint := env["CINEFORGE_MINIO_ENDPOINT"]
	if endpoint == "" {
		endpoint = "127.0.0.1:9000"
	}
	resp, err := http.Get("http://" + endpoint + "/minio/health/live")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// seedFileTaskOpts 文件域任务种子选项（asset/storyboard/depends_on/variant 可空）。
type seedFileTaskOpts struct {
	AssigneeID    string
	StoryboardID  string
	AssetID       string
	DependsOn     string
	TaskVariant   string
	VariantPlanID string
}

// seedFileTask 直接写 dev DB 插入任务行（文件域字段齐全）。
func (e *testEnv) seedFileTask(t *testing.T, projectID, taskType, status string, o seedFileTaskOpts) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO tasks (id, project_id, storyboard_id, asset_id, task_type, title, assignee_id, status,
		   task_variant, variant_plan_id, depends_on_task_id, asset_context_outdated, is_retired,
		   created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,false,false,now(),now())`,
		id, projectID, nzStr(o.StoryboardID), nzStr(o.AssetID), taskType,
		"文件任务-"+strings.ToUpper(id[:6]), nzStr(o.AssigneeID), status,
		nzStr(o.TaskVariant), nzStr(o.VariantPlanID), nzStr(o.DependsOn)); err != nil {
		t.Fatalf("seed file task: %v", err)
	}
	t.Cleanup(func(singleID string) func() {
		return func() {
			ctx := context.Background()
			_, _ = e.pool.Exec(ctx, `DELETE FROM submissions WHERE task_id = $1`, singleID)
			_, _ = e.pool.Exec(ctx, `DELETE FROM task_submission_batches WHERE task_id = $1`, singleID)
			_, _ = e.pool.Exec(ctx, `DELETE FROM task_prompts WHERE task_id = $1`, singleID)
			_, _ = e.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, singleID)
		}
	}(id))
	return id
}

// seedAsset 插入正式资产行（active），供公司资产库回退与 variant 用例。
func (e *testEnv) seedAsset(t *testing.T, projectID, assetType, code, name string) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO assets (id, project_id, asset_code, asset_type, name, status, tags, metadata_json,
		   version, is_locked, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,'active','[]','{}',1,false,now(),now())`,
		id, projectID, code, assetType, name); err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	t.Cleanup(func(singleID string) func() {
		return func() { _, _ = e.pool.Exec(context.Background(), `DELETE FROM assets WHERE id = $1`, singleID) }
	}(id))
	return id
}

// seedFile 插入 files 行（taskID 空 → task_id NULL，模拟导入/未绑定文件）。
func (e *testEnv) seedFile(t *testing.T, projectID string, taskID *string, code string) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO files (id, file_code, bucket, object_key, file_name, mime_type, project_id, task_id,
		   created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now(),now())`,
		id, code, e.cfg.MinIO.Bucket, "fake/"+id+".png", "fake-"+id[:6]+".png", "image/png", projectID,
		nzStr(ptrOrEmpty(taskID))); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	t.Cleanup(func(singleID string) func() {
		return func() { _, _ = e.pool.Exec(context.Background(), `DELETE FROM files WHERE id = $1`, singleID) }
	}(id))
	return id
}

func ptrOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// seedVariantPlan 插入变体计划行（variant_plan_id 有 FK，需真正的一行）。
func (e *testEnv) seedVariantPlan(t *testing.T, projectID, assetID string) string {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO asset_variant_plans (id, project_id, asset_id, variant_code, variant_kind,
		   title_zh, description_zh, source, status, metadata_json, created_at, updated_at)
		 VALUES ($1,$2,$3,'V-A','age','变体A','测试变体','ai','planned','{}',now(),now())`,
		id, projectID, assetID); err != nil {
		t.Fatalf("seed variant plan: %v", err)
	}
	t.Cleanup(func() { _, _ = e.pool.Exec(context.Background(), `DELETE FROM asset_variant_plans WHERE id = $1`, id) })
	return id
}

// seedApprovedMaster 登记 file 的 approved main master 提交（公司资产库回退前置）。
func (e *testEnv) seedApprovedMaster(t *testing.T, taskID, fileID string) {
	t.Helper()
	id, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen uuid: %v", err)
	}
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO submissions (id, task_id, file_id, file_path, file_type, tool_names, status,
		   is_selected, is_primary, is_archived, is_invalidated, created_at, updated_at)
		 VALUES ($1,$2,$3,'fake/path.png','image','[]','primary_master',true,true,false,false,now(),now())`,
		id, taskID, fileID); err != nil {
		t.Fatalf("seed approved master: %v", err)
	}
	t.Cleanup(func(singleID string) func() {
		return func() { _, _ = e.pool.Exec(context.Background(), `DELETE FROM submissions WHERE id = $1`, singleID) }
	}(id))
}

// ---- HTTP 辅助 ----

// doForm 发送 multipart 表单请求（返回 JSON）。
func (e *testEnv) doForm(t *testing.T, method, path, contentType string, body *bytes.Buffer, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// doReq 发送原始请求返回 (status, headers, body)（流式/Range 断言用）。
func (e *testEnv) doReq(t *testing.T, method, path string, headers map[string]string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w.Code, w.Header(), w.Body.Bytes()
}

// decodeJSON 解码 JSON 字节（detail 断言用）。
func decodeJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode json: %v (raw=%s)", err, raw)
	}
	return out
}

// taskUploadForm 构造 task-upload multipart（1 个 file part）。
func taskUploadForm(taskID, filename string, content []byte, fileSize *int64) (*bytes.Buffer, string) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("task_id", taskID)
	if fileSize != nil {
		_ = mw.WriteField("file_size", strconv.FormatInt(*fileSize, 10))
	}
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(content)
	_ = mw.Close()
	return &body, mw.FormDataContentType()
}

// taskUploadFormNoFile 构造缺少 file part 的 multipart（422 用例）。
func taskUploadFormNoFile(taskID string) (*bytes.Buffer, string) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("task_id", taskID)
	_ = mw.Close()
	return &body, mw.FormDataContentType()
}

// testFileToken 在测试侧复刻 legacy _create_file_token（验证 Go 端字节兼容）。
func testFileToken(t *testing.T, secret, purpose, fileID, userID string, exp int64, downloadName string) string {
	t.Helper()
	payload := map[string]any{"purpose": purpose, "file_id": fileID, "user_id": userID, "exp": exp}
	if downloadName != "" {
		payload["download_name"] = downloadName
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal token payload: %v", err)
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("file-playback:" + body))
	bodySig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + bodySig
}

// ---- 守卫用例（MinIO 无关） ----

func TestTaskUploadGuards(t *testing.T) {
	e := newEnv(t)
	directorTok := e.seedUserAs(t, uniquePhone("951"), "director", "secret123")
	artistID, _ := e.seedUserAsID(t, uniquePhone("952"), "artist", "secret123")
	unrelatedTok := e.seedUserAs(t, uniquePhone("956"), "artist", "secret123")
	title := "上传-守卫" + uniquePhone("95")
	e.cleanupProjectByTitle(t, title)
	projectID := e.seedProject(t, title, uniquePrefix(), seedProjectOpts{scriptText: "第一幕。\n"})
	taskID := e.seedFileTask(t, projectID, "text_to_image", "todo", seedFileTaskOpts{AssigneeID: artistID})

	// 404：任务不存在
	body, ct := taskUploadForm("00000000-0000-0000-0000-000000000000", "a.png", []byte("x"), nil)
	code, resp := e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, directorTok)
	assertStatus(t, code, http.StatusNotFound, resp)
	detail(t, resp, "Task not found")

	// 403：无关 artist（非执行人、无项目角色）
	body, ct = taskUploadForm(taskID, "a.png", []byte("x"), nil)
	code, resp = e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, unrelatedTok)
	assertStatus(t, code, http.StatusForbidden, resp)
	detail(t, resp, "Task permission denied")

	// 400：locked（依赖未完成任务）→ "前置任务 尚未完成"
	depID := e.seedFileTask(t, projectID, "text_to_image", "todo", seedFileTaskOpts{AssigneeID: artistID})
	lockedID := e.seedFileTask(t, projectID, "text_to_image", "todo",
		seedFileTaskOpts{AssigneeID: artistID, DependsOn: depID})
	body, ct = taskUploadForm(lockedID, "a.png", []byte("x"), nil)
	code, resp = e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, directorTok)
	assertStatus(t, code, http.StatusBadRequest, resp)
	detail(t, resp, "前置任务 尚未完成，当前任务暂未解锁。")

	// 400：status submitted / reviewing / completed
	for _, st := range []string{"submitted", "reviewing", "completed"} {
		sid := e.seedFileTask(t, projectID, "text_to_image", st, seedFileTaskOpts{AssigneeID: artistID})
		body, ct = taskUploadForm(sid, "a.png", []byte("x"), nil)
		code, resp = e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, directorTok)
		assertStatus(t, code, http.StatusBadRequest, resp)
		detail(t, resp, "当前任务状态不能继续上传候选成果。")
	}

	// 400：扩展名白名单（text_to_image → 仅 jpg/jpeg/png）
	body, ct = taskUploadForm(taskID, "evil.exe", []byte("x"), nil)
	code, resp = e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, directorTok)
	assertStatus(t, code, http.StatusBadRequest, resp)
	detail(t, resp, "不支持的文件类型 .exe，当前任务允许：jpeg, jpg, png")

	// 422：缺 task_id / 缺 file
	body, ct = taskUploadForm("", "a.png", []byte("x"), nil)
	code, resp = e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, directorTok)
	assertStatus(t, code, http.StatusUnprocessableEntity, resp)
	body, ct = taskUploadFormNoFile(taskID)
	code, resp = e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, directorTok)
	assertStatus(t, code, http.StatusUnprocessableEntity, resp)

	// 401：无 Bearer
	code, _, _ = e.doReq(t, http.MethodPost, "/api/files/task-upload", nil, []byte("x"))
	if code != http.StatusUnauthorized {
		t.Errorf("无 Bearer 上传 status=%d 期望 401", code)
	}
}

// ---- MinIO 门控：上传 + 流式读取闭环 ----

func TestTaskUploadAndStreamFlow(t *testing.T) {
	if !minioAvailable(t) {
		t.Skip("dev MinIO 不可达或未配置凭证，跳过上传/流式契约测试")
	}
	e := newEnvWithMinIO(t, true)
	directorTok := e.seedUserAs(t, uniquePhone("953"), "director", "secret123")
	title := "上传-流程" + uniquePhone("95")
	e.cleanupProjectByTitle(t, title)
	projectID := e.seedProject(t, title, uniquePrefix(), seedProjectOpts{scriptText: "第一幕。\n"})
	sbID := e.seedStoryboard(t, projectID, 1, 1)
	taskID := e.seedFileTask(t, projectID, "text_to_image", "todo",
		seedFileTaskOpts{StoryboardID: sbID})

	// 上传产生的 files 行走独立清理（cleanupProject 不删 files，且 FK 阻止删任务）。
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM files WHERE project_id = $1`, projectID)
	})

	content := []byte("hello minioworld")
	size := int64(len(content))

	// ---- V001 上传（中文名 → 折叠空格、保留中文）----
	body, ct := taskUploadForm(taskID, "草稿 图.png", content, nil)
	code, resp := e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, directorTok)
	assertStatus(t, code, http.StatusCreated, resp)
	if got := str(resp["file_name"]); got != "草稿_图.png" {
		t.Errorf("file_name=%q 期望 草稿_图.png", got)
	}
	if got := str(resp["url"]); !strings.HasPrefix(got, "minio://") {
		t.Errorf("url=%q 期望 minio:// 开头", got)
	}
	objectKey := str(resp["object_key"])
	// 路径 = {prefix}/{EP}/{tasktype 目录}/TASK-{id8}/V001/；镜码 S001 只出现在文件名段。
	if !strings.Contains(objectKey, "/EP01/text-to-image/TASK-"+taskID[:8]+"/V001/") {
		t.Errorf("object_key=%q 期望包含 /EP01/text-to-image/TASK-%s/V001/", objectKey, taskID[:8])
	}
	if !strings.Contains(objectKey, "EP01-S001-text-to-image-TASK-"+taskID[:8]+"-") {
		t.Errorf("object_key=%q 期望文件名含 EP01-S001-text-to-image 镜像码", objectKey)
	}
	if !strings.HasPrefix(str(resp["file_code"]), "T") || !strings.Contains(str(resp["file_code"]), "-TEXT-TO-IMAGE-") {
		t.Errorf("file_code=%q 期望 {PREFIX}-TEXT-TO-IMAGE-UUID8-V001", str(resp["file_code"]))
	}
	fileID := str(resp["id"])
	if fileID == "" {
		t.Fatalf("上传响应缺 id: %v", resp)
	}

	// ---- V002 上传（同任务布点递增）----
	body, ct = taskUploadForm(taskID, "b.png", []byte("second"), nil)
	code, resp = e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, directorTok)
	assertStatus(t, code, http.StatusCreated, resp)
	if !strings.Contains(str(resp["object_key"]), "/V002/") {
		t.Errorf("第二次上传 object_key=%q 期望 V002", str(resp["object_key"]))
	}

	// ---- 变体子用例：asset + variant_plan + task_variant → asset-variants/result ----
	assetCode := "SC" + strings.ToUpper(requestUUID(t)[:4])
	assetID := e.seedAsset(t, projectID, "character", assetCode, "小明")
	planID := e.seedVariantPlan(t, projectID, assetID)
	variantTaskID := e.seedFileTask(t, projectID, "text_to_image", "todo", seedFileTaskOpts{
		AssetID:       assetID,
		TaskVariant:   "A",
		VariantPlanID: planID,
	})
	body, ct = taskUploadForm(variantTaskID, "草稿.png", content, nil)
	code, resp = e.doForm(t, http.MethodPost, "/api/files/task-upload", ct, body, directorTok)
	assertStatus(t, code, http.StatusCreated, resp)
	if !strings.Contains(str(resp["object_key"]), "/asset-variants/"+assetCode+"-A/") {
		t.Errorf("variant object_key=%q 期望包含 /asset-variants/%s-A/", str(resp["object_key"]), assetCode)
	}
	if !strings.Contains(str(resp["object_key"]), "-result.png") {
		t.Errorf("variant object_key=%q 期望 result 文件名", str(resp["object_key"]))
	}

	// ---- 操作日志 file_uploaded ----
	var opCount int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM operation_logs WHERE action = 'file_uploaded' AND target_type = 'file' AND project_id = $1`,
		projectID).Scan(&opCount); err != nil {
		t.Fatalf("op log count: %v", err)
	}
	if opCount != 3 {
		t.Errorf("operation_logs.file_uploaded=%d 期望 3", opCount)
	}

	// ---- content：全量 200 ----
	code, hdr, raw := e.doReq(t, http.MethodGet, "/api/files/"+fileID+"/content",
		map[string]string{"Authorization": "Bearer " + directorTok}, nil)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if !bytes.Equal(raw, content) {
		t.Errorf("content full body=%q", raw)
	}
	if hdr.Get("Content-Length") != strconv.FormatInt(size, 10) {
		t.Errorf("Content-Length=%q 期望 %d", hdr.Get("Content-Length"), size)
	}
	if !strings.Contains(hdr.Get("Cache-Control"), "max-age=300") {
		t.Errorf("Cache-Control=%q 期望 private, max-age=300", hdr.Get("Cache-Control"))
	}

	// ---- content：Range bytes=0-3 → 206 ----
	code, hdr, raw = e.doReq(t, http.MethodGet, "/api/files/"+fileID+"/content",
		map[string]string{"Authorization": "Bearer " + directorTok, "Range": "bytes=0-3"}, nil)
	assertStatusRaw(t, code, http.StatusPartialContent, raw)
	if !bytes.Equal(raw, content[:4]) {
		t.Errorf("range 0-3 body=%q", raw)
	}
	if got := hdr.Get("Content-Range"); got != "bytes 0-3/"+strconv.FormatInt(size, 10) {
		t.Errorf("Content-Range=%q 期望 bytes 0-3/%d", got, size)
	}
	if hdr.Get("Content-Length") != "4" {
		t.Errorf("Content-Length=%q 期望 4", hdr.Get("Content-Length"))
	}
	if !strings.Contains(hdr.Get("Accept-Ranges"), "bytes") {
		t.Errorf("Accept-Ranges=%q 期望包含 bytes", hdr.Get("Accept-Ranges"))
	}

	// ---- content：后缀 bytes=-3 → 206 末 3 字节 ----
	code, hdr, raw = e.doReq(t, http.MethodGet, "/api/files/"+fileID+"/content",
		map[string]string{"Authorization": "Bearer " + directorTok, "Range": "bytes=-3"}, nil)
	assertStatusRaw(t, code, http.StatusPartialContent, raw)
	if !bytes.Equal(raw, content[size-3:]) {
		t.Errorf("suffix body=%q", raw)
	}
	wantStart := size - 3
	if got := hdr.Get("Content-Range"); got != fmt.Sprintf("bytes %d-%d/%d", wantStart, size-1, size) {
		t.Errorf("suffix Content-Range=%q 期望 bytes %d-%d/%d", got, wantStart, size-1, size)
	}

	// ---- content：越界 → 416 空体 + bytes */{size} ----
	code, hdr, raw = e.doReq(t, http.MethodGet, "/api/files/"+fileID+"/content",
		map[string]string{"Authorization": "Bearer " + directorTok, "Range": "bytes=999999-"}, nil)
	assertStatusRaw(t, code, http.StatusRequestedRangeNotSatisfiable, raw)
	if len(raw) != 0 {
		t.Errorf("416 body 应为空，实际=%q", raw)
	}
	if got := hdr.Get("Content-Range"); got != "bytes */"+strconv.FormatInt(size, 10) {
		t.Errorf("416 Content-Range=%q 期望 bytes */%d", got, size)
	}

	// ---- playback-url + stream 闭环 ----
	code, resp = e.doJSON(t, http.MethodGet, "/api/files/"+fileID+"/playback-url", nil, directorTok)
	assertStatus(t, code, http.StatusOK, resp)
	if num(resp["expires_in_seconds"]) != 300 {
		t.Errorf("expires_in_seconds=%v 期望 300", resp["expires_in_seconds"])
	}
	if expAt, err := time.Parse(time.RFC3339Nano, str(resp["expires_at"])); err != nil || expAt.Before(time.Now().Add(4*time.Minute)) {
		t.Errorf("expires_at=%q 解析异常或 TTL 不足", str(resp["expires_at"]))
	}
	streamURL := str(resp["url"])
	if !strings.HasPrefix(streamURL, "/api/files/"+fileID+"/stream?token=") {
		t.Fatalf("stream url=%q 格式错误", streamURL)
	}
	code, _, raw = e.doReq(t, http.MethodGet, streamURL, map[string]string{"Range": "bytes=0-4"}, nil)
	assertStatusRaw(t, code, http.StatusPartialContent, raw)
	if !bytes.Equal(raw, content[:5]) {
		t.Errorf("stream range body=%q", raw)
	}

	// ---- download-url + download 闭环 ----
	code, resp = e.doJSON(t, http.MethodGet,
		"/api/files/"+fileID+"/download-url?file_name="+url.QueryEscape("分镜 巨幕1.png"), nil, directorTok)
	assertStatus(t, code, http.StatusOK, resp)
	if got := str(resp["file_name"]); got != "分镜 巨幕1.png" {
		t.Errorf("download file_name=%q 期望 分镜 巨幕1.png", got)
	}
	dlURL := str(resp["url"])
	if !strings.HasPrefix(dlURL, "/api/files/"+fileID+"/download?token=") {
		t.Fatalf("download url=%q 格式错误", dlURL)
	}
	code, hdr, raw = e.doReq(t, http.MethodGet, dlURL, nil, nil)
	assertStatusRaw(t, code, http.StatusOK, raw)
	if !bytes.Equal(raw, content) {
		t.Errorf("download body=%q", raw)
	}
	if got := hdr.Get("Content-Disposition"); !strings.HasPrefix(got, "attachment; filename*=UTF-8''") {
		t.Errorf("Content-Disposition=%q 期望 attachment; filename*=UTF-8''", got)
	}

	// ---- content 无 Bearer → 401（token 走 stream/download，不走 content/playback-url）----
	code, _, _ = e.doReq(t, http.MethodGet, "/api/files/"+fileID+"/content", nil, nil)
	if code != http.StatusUnauthorized {
		t.Errorf("无 Bearer content status=%d 期望 401", code)
	}
}

// ---- token 错误语义（DB 仅依赖） ----

func TestFileTokenErrors(t *testing.T) {
	e := newEnv(t)
	directorID, directorTok := e.seedUserAsID(t, uniquePhone("957"), "director", "secret123")
	artistID, _ := e.seedUserAsID(t, uniquePhone("958"), "artist", "secret123")
	title := "token-错误" + uniquePhone("95")
	e.cleanupProjectByTitle(t, title)
	projectID := e.seedProject(t, title, uniquePrefix(), seedProjectOpts{scriptText: "第一幕。\n"})
	taskID := e.seedFileTask(t, projectID, "text_to_image", "todo", seedFileTaskOpts{AssigneeID: artistID})
	fileID := e.seedFile(t, projectID, &taskID, "T-TOKEN-001")
	otherFile := e.seedFile(t, projectID, &taskID, "T-TOKEN-002")
	secret := e.cfg.Auth.Secret
	now := time.Now().UTC().Unix()
	streamURL := func(tok string) string {
		return "/api/files/" + fileID + "/stream?token=" + url.QueryEscape(tok)
	}
	code := func(url string) int {
		got, _, _ := e.doReq(t, http.MethodGet, url, nil, nil)
		return got
	}

	t.Run("expired", func(t *testing.T) {
		tok := testFileToken(t, secret, "file-playback", fileID, directorID, now-10, "")
		got, _, raw := e.doReq(t, http.MethodGet, streamURL(tok), nil, nil)
		assertStatusRaw(t, got, http.StatusUnauthorized, raw)
		detail(t, decodeJSON(t, raw), "Playback token expired")
	})
	t.Run("wrong purpose", func(t *testing.T) {
		tok := testFileToken(t, secret, "file-download", fileID, directorID, now+3600, "")
		got, _, raw := e.doReq(t, http.MethodGet, streamURL(tok), nil, nil)
		assertStatusRaw(t, got, http.StatusUnauthorized, raw)
		detail(t, decodeJSON(t, raw), "Invalid playback token")
	})
	t.Run("tampered", func(t *testing.T) {
		tok := testFileToken(t, secret, "file-playback", fileID, directorID, now+3600, "") + "tampered"
		got, _, raw := e.doReq(t, http.MethodGet, streamURL(tok), nil, nil)
		assertStatusRaw(t, got, http.StatusUnauthorized, raw)
		detail(t, decodeJSON(t, raw), "Invalid playback token")
	})
	t.Run("cross file", func(t *testing.T) {
		got, _, raw := e.doReq(t, http.MethodGet, "/api/files/"+otherFile+"/stream?token="+
			url.QueryEscape(testFileToken(t, secret, "file-playback", fileID, directorID, now+3600, "")), nil, nil)
		assertStatusRaw(t, got, http.StatusUnauthorized, raw)
		detail(t, decodeJSON(t, raw), "Invalid playback token")
	})
	t.Run("user disabled", func(t *testing.T) {
		inactiveID := e.seedUser(t, uniquePhone("96"), "artist", "secret123")
		e.setUserActive(t, inactiveID, false)
		tok := testFileToken(t, secret, "file-playback", fileID, inactiveID, now+3600, "")
		got, _, raw := e.doReq(t, http.MethodGet, streamURL(tok), nil, nil)
		assertStatusRaw(t, got, http.StatusUnauthorized, raw)
		detail(t, decodeJSON(t, raw), "Playback user disabled or not found")
	})
	t.Run("missing token", func(t *testing.T) {
		if got := code("/api/files/" + fileID + "/stream"); got != http.StatusUnauthorized {
			t.Errorf("缺 token 的 stream status=%d 期望 401", got)
		}
	})

	// director 本人 + 没被 consume 的 playback 票在授权测试里验证；这里只验证 token 层。
	_ = directorTok
	_ = now
}

// ---- _authorized_file 权限（DB 仅依赖，走 playback-url 无存储依赖） ----

func TestFileAuthorizedRules(t *testing.T) {
	e := newEnv(t)
	directorTok := e.seedUserAs(t, uniquePhone("961"), "director", "secret123")
	assigneeID, assigneeTok := e.seedUserAsID(t, uniquePhone("962"), "artist", "secret123")
	unrelatedTok := e.seedUserAs(t, uniquePhone("963"), "artist", "secret123")
	title := "授权-规则" + uniquePhone("96")
	e.cleanupProjectByTitle(t, title)
	projectID := e.seedProject(t, title, uniquePrefix(), seedProjectOpts{scriptText: "第一幕。\n"})

	// 404：文件不存在
	code, body := e.doJSON(t, http.MethodGet,
		"/api/files/00000000-0000-0000-0000-000000000000/playback-url", nil, directorTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "File not found")

	// 403：task_id NULL（导入类文件，对公司任何人都不放行）
	importFile := e.seedFile(t, projectID, nil, "T-IMPORT-001")
	code, body = e.doJSON(t, http.MethodGet, "/api/files/"+importFile+"/playback-url", nil, directorTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "File permission denied")

	// 403：任务归属但越权（无执行人/项目角色）→ 403
	taskID := e.seedFileTask(t, projectID, "text_to_image", "todo", seedFileTaskOpts{AssigneeID: assigneeID})
	taskFile := e.seedFile(t, projectID, &taskID, "T-TASK-001")
	code, body = e.doJSON(t, http.MethodGet, "/api/files/"+taskFile+"/playback-url", nil, unrelatedTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "File permission denied")

	// director 恒放行；执行人放行
	code, body = e.doJSON(t, http.MethodGet, "/api/files/"+taskFile+"/playback-url", nil, directorTok)
	assertStatus(t, code, http.StatusOK, body)
	if !strings.HasPrefix(str(body["url"]), "/api/files/"+taskFile+"/stream?token=") {
		t.Errorf("playback url=%q 格式错误", str(body["url"]))
	}
	code, body = e.doJSON(t, http.MethodGet, "/api/files/"+taskFile+"/playback-url", nil, assigneeTok)
	assertStatus(t, code, http.StatusOK, body)

	// 公司资产库回退:正式资产 + approved primary_master 提交 → 无关用户可读
	assetID := e.seedAsset(t, projectID, "character", "LBL"+strings.ToUpper(requestUUID(t)[:4]), "小明")
	libTaskID := e.seedFileTask(t, projectID, "text_to_image", "todo", seedFileTaskOpts{AssetID: assetID})
	libFile := e.seedFile(t, projectID, &libTaskID, "T-LIB-001")
	code, body = e.doJSON(t, http.MethodGet, "/api/files/"+libFile+"/playback-url", nil, unrelatedTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "File permission denied")
	e.seedApprovedMaster(t, libTaskID, libFile)
	code, body = e.doJSON(t, http.MethodGet, "/api/files/"+libFile+"/playback-url", nil, unrelatedTok)
	assertStatus(t, code, http.StatusOK, body)
}


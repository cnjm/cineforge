package api_test

// P4e-6 task prompt-jobs 契约测试（对齐 legacy app/api/routes/tasks.py create_task_prompt_job）：
//   - POST /tasks/{id}/prompt-jobs：404/403/400 阶梯 detail 逐字
//   - 快乐路径：202 AgentRunRead + input 契约 + agent_run_created op-log（detail 含 task_id/agent_type/status）
//   - storyboard_shot：step=keyframe / step=video（前置 primary_master 关键帧）两分支
//   - 分集作用域任务：制作上下文不完整 → 400（production_brief 缺失，逐字 detail）
//
// 测试环境 stubAgentBridge 让 enqueue 返回 202，运行保持 running，断言不依赖真实 agent 子模块。

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"cineforge/server/internal/auth"
)

func jsonContains(s, frag string) bool { return strings.Contains(s, frag) }

// TestTaskPromptJobGuardContract 校验阶梯守卫（409 无、404/403/400 各分支逐字）。
func TestTaskPromptJobGuardContract(t *testing.T) {
	e := newEnv(t)
	directorID, directorTok := e.seedUserAsID(t, uniquePhone("pj1"), "director", "secret123")
	_, outsiderTok := e.seedUserAsID(t, uniquePhone("pj2"), "artist", "secret123")
	projectID := e.seedProject(t, "prompt-jobs守卫", "", seedProjectOpts{})

	// 幽灵任务 → 404
	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/"+bellUUID()+"/prompt-jobs", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusNotFound, body)
	detail(t, body, "Task not found")

	// 非负责人且无项目权限 → 403
	taskID := e.seedTask(t, projectID, directorID, "text_to_image", "todo", seedTaskOpts{})
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/prompt-jobs", map[string]any{}, outsiderTok)
	assertStatus(t, code, http.StatusForbidden, body)
	detail(t, body, "Task permission denied")

	// completed → 400（返工提示）
	completedID := e.seedTask(t, projectID, directorID, "text_to_image", "completed", seedTaskOpts{})
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+completedID+"/prompt-jobs", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "已完成任务不能保存或重新生成提示词；如需换版，请先由导演发起返工。")

	// reviewing → 400
	reviewingID := e.seedTask(t, projectID, directorID, "text_to_image", "reviewing", seedTaskOpts{})
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+reviewingID+"/prompt-jobs", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "当前任务正在审核，不能保存或重新生成提示词。")

	// 人工临时生产任务 → 400
	humanID := e.seedTask(t, projectID, directorID, "text_to_image", "todo",
		seedTaskOpts{VariantKind: "human_temporary"})
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+humanID+"/prompt-jobs", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "人工临时生产任务已跳过提示词流程，不能运行提示词 Agent。")

	// storyboard_shot 缺 step → 400
	sbID := e.seedTask(t, projectID, directorID, "storyboard_shot", "todo", seedTaskOpts{})
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+sbID+"/prompt-jobs", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "分镜任务需指定 step=keyframe 或 step=video")

	// storyboard_shot step=video 无 primary_master 关键帧 → 400
	sbVideoID := e.seedTask(t, projectID, directorID, "storyboard_shot", "todo", seedTaskOpts{})
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+sbVideoID+"/prompt-jobs?step=video", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "关键帧尚未审核定版，视频提示词暂不能生成。")

	// 非可提示词类型 → 400（audio 是合法枚举但不在 prompt-jobs switch 内）
	audioID := e.seedTask(t, projectID, directorID, "audio", "todo", seedTaskOpts{})
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+audioID+"/prompt-jobs", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "Task type does not support prompt generation")
}

// TestTaskPromptJobHappyPathContract text_to_image 快乐路径：202 + input 契约 + op-log。
func TestTaskPromptJobHappyPathContract(t *testing.T) {
	e := newEnv(t)
	directorID, directorTok := e.seedUserAsID(t, uniquePhone("pj3"), "director", "secret123")
	projectID := e.seedProject(t, "prompt-jobs快乐路径", "", seedProjectOpts{})
	taskID := e.seedTask(t, projectID, directorID, "text_to_image", "todo", seedTaskOpts{})

	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/prompt-jobs", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusAccepted, body)
	if got := body["agent_type"]; got != "text_to_image_prompt" {
		t.Fatalf("agent_type=%v want text_to_image_prompt", got)
	}
	if body["task_id"] != taskID {
		t.Fatalf("task_id=%v want %s", body["task_id"], taskID)
	}
	if body["status"] != "running" {
		t.Fatalf("status=%v want running（enqueue stub 202）", body["status"])
	}
	in, ok := body["input"].(map[string]any)
	if !ok {
		t.Fatalf("input=%v 不是对象", body["input"])
	}
	if in["skill"] != "text-to-image-prompt" {
		t.Fatalf("input.skill=%v want text-to-image-prompt", in["skill"])
	}
	if in["task_id"] != taskID {
		t.Fatalf("input.task_id=%v want %s", in["task_id"], taskID)
	}
	if in["task_type"] != "text_to_image" {
		t.Fatalf("input.task_type=%v want text_to_image", in["task_type"])
	}
	if in["step"] != nil {
		t.Fatalf("input.step=%v want null（未传 step）", in["step"])
	}
	if in["context_fingerprint"] == nil || in["context_fingerprint"] == "" {
		t.Fatalf("input.context_fingerprint=%v 应为指纹哈希", in["context_fingerprint"])
	}
	if _, ok := in["assets"].([]any); !ok {
		t.Fatalf("input.assets=%v 应为数组", in["assets"])
	}
	if _, ok := in["asset"].(map[string]any); !ok {
		t.Fatalf("input.asset=%v 应为对象", in["asset"])
	}
	if _, ok := in["production_context"].(map[string]any); !ok {
		t.Fatalf("input.production_context=%v 应为对象", in["production_context"])
	}
	if _, ok := in["storyboard"].(map[string]any); !ok {
		t.Fatalf("input.storyboard=%v 应为对象", in["storyboard"])
	}
	if in["duration"] != nil {
		t.Fatalf("input.duration=%v want null（无分镜）", in["duration"])
	}

	// op-log：agent_run_created，detail 含 task_id + agent_type + status。
	runID, _ := body["id"].(string)
	var detailJSON string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT detail FROM operation_logs
		 WHERE action = 'agent_run_created' AND target_type = 'agent_run' AND target_id = $1`,
		runID).Scan(&detailJSON); err != nil {
		t.Fatalf("query op-log: %v", err)
	}
	want := []string{`"` + taskID + `"`, `"text_to_image_prompt"`, `"running"`}
	for _, frag := range want {
		if !jsonContains(detailJSON, frag) {
			t.Fatalf("op-log detail=%s 缺少 %s", detailJSON, frag)
		}
	}
}

// TestTaskPromptJobStoryboardSteps storyboard_shot keyframe/video 两分支。
func TestTaskPromptJobStoryboardSteps(t *testing.T) {
	e := newEnv(t)
	directorID, directorTok := e.seedUserAsID(t, uniquePhone("pj4"), "director", "secret123")
	projectID := e.seedProject(t, "prompt-jobs分镜步骤", "", seedProjectOpts{})

	// step=keyframe → text_to_image_prompt，task_type 覆盖为 keyframe
	keyID := e.seedTask(t, projectID, directorID, "storyboard_shot", "todo", seedTaskOpts{})
	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/"+keyID+"/prompt-jobs?step=keyframe", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusAccepted, body)
	if got := body["agent_type"]; got != "text_to_image_prompt" {
		t.Fatalf("storyboard keyframe agent_type=%v want text_to_image_prompt", got)
	}
	inKey := body["input"].(map[string]any)
	if inKey["step"] != "keyframe" {
		t.Fatalf("input.step=%v want keyframe", inKey["step"])
	}
	if inKey["task_type"] != "keyframe" {
		t.Fatalf("input.task_type=%v want keyframe", inKey["task_type"])
	}
	if inKey["output_spec"] != nil {
		t.Fatalf("input.output_spec=%v want null（无生产上下文 task_variant 时）", inKey["output_spec"])
	}

	// step=video：先造 primary_master 关键帧 submission（step='keyframe', file_type='image'）
	videoID := e.seedTask(t, projectID, directorID, "storyboard_shot", "todo", seedTaskOpts{})
	subID, err := auth.NewUUID()
	if err != nil {
		t.Fatalf("gen sub uuid: %v", err)
	}
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO submissions (id, task_id, status, is_primary, is_archived, is_invalidated,
		   step, file_type, file_path, tool_names, is_selected, created_at, updated_at)
		 VALUES ($1,$2,'primary_master',true,false,false,'keyframe','image','/kf/1.png','[]',false,now(),now())`,
		subID, videoID); err != nil {
		t.Fatalf("seed keyframe submission: %v", err)
	}
	code, body = e.doJSON(t, http.MethodPost, "/api/tasks/"+videoID+"/prompt-jobs?step=video", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusAccepted, body)
	if got := body["agent_type"]; got != "image_to_video_prompt" {
		t.Fatalf("storyboard video agent_type=%v want image_to_video_prompt", got)
	}
	inVideo := body["input"].(map[string]any)
	if inVideo["step"] != "video" {
		t.Fatalf("input.step=%v want video", inVideo["step"])
	}
	if inVideo["task_type"] != "video" {
		t.Fatalf("input.task_type=%v want video", inVideo["task_type"])
	}
	if inVideo["skill"] != "image-to-video-prompt" {
		t.Fatalf("input.skill=%v want image-to-video-prompt", inVideo["skill"])
	}
	kf, ok := inVideo["keyframe_reference"].(map[string]any)
	if !ok {
		t.Fatalf("input.keyframe_reference=%v 应为对象", inVideo["keyframe_reference"])
	}
	if kf["submission_id"] != subID {
		t.Fatalf("keyframe_reference.submission_id=%v want %s", kf["submission_id"], subID)
	}
	if kf["file_path"] != "/kf/1.png" {
		t.Fatalf("keyframe_reference.file_path=%v want /kf/1.png", kf["file_path"])
	}
}

// TestTaskPromptJobEpisodeContextBroken 分集作用域任务：制作上下文不完整 → 400（production_brief 缺失）。
func TestTaskPromptJobEpisodeContextBroken(t *testing.T) {
	e := newEnv(t)
	directorID, directorTok := e.seedUserAsID(t, uniquePhone("pj5"), "director", "secret123")
	projectID := e.seedProject(t, "prompt-jobs分集上下文", "", seedProjectOpts{})
	episodeID := e.seedEpisode(t, projectID, "EP01", 1)
	versionID := e.seedScript(t, projectID, episodeID, "第一幕\n小明决定出发。")
	taskID := e.seedTask(t, projectID, directorID, "text_to_image", "todo", seedTaskOpts{})
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE tasks SET episode_id = $2, script_version_id = $3 WHERE id = $1`,
		taskID, episodeID, versionID); err != nil {
		t.Fatalf("set task episode scope: %v", err)
	}

	code, body := e.doJSON(t, http.MethodPost, "/api/tasks/"+taskID+"/prompt-jobs", map[string]any{}, directorTok)
	assertStatus(t, code, http.StatusBadRequest, body)
	detail(t, body, "任务制作上下文不完整：production_brief is required")
}

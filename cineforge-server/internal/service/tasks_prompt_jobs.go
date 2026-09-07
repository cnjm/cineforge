package service

// P4e-6 task prompt-jobs（对齐 legacy app/api/routes/tasks.py create_task_prompt_job）：
//
//	POST /tasks/{task_id}/prompt-jobs?step=keyframe|video → 202 AgentRunRead
//
// 校验阶梯（detail 逐字对齐 legacy）：
//
//	任务不存在/无更新权限        → 404 "Task not found" / 403 "Task permission denied"
//	锁定/审核中/已完成           → assertTaskPromptEditable 逐字
//	人工临时任务                → 400 "人工临时生产任务已跳过提示词流程，不能运行提示词 Agent。"
//	分镜任务缺 step             → 400 "分镜任务需指定 step=keyframe 或 step=video"
//	video 缺关键帧               → 400 "关键帧尚未审核定版，视频提示词暂不能生成。"
//	非可提示词类型              → 400 "Task type does not support prompt generation"
//	资产缺专用生产上下文        → 400 "资产任务缺少专用生产上下文，无法生成资产 Prompt"
//	image_to_video 前置未完成   → 400 "关键帧任务尚未完成，视频任务暂未解锁。"
//	执行上下文构建失败          → 400 "任务制作上下文不完整：{err}"
//
// op-log 单独登记 detail={task_id, agent_type, status}（legacy 的
// orchestrator.submit_background + _save_background_agent_run 不记 op-log）。

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"cineforge/server/internal/model"
	"cineforge/server/internal/productionbrief"
	"cineforge/server/internal/repository"
)

// assetAgentByType 对齐 legacy create_task_prompt_job 的 asset_agent_by_type：
// character/scene/prop → 专用 element 设计 Prompt Agent + skill。
var assetAgentByType = map[string][2]string{
	"character": {"character_design_prompt", "character-design-prompt"},
	"scene":     {"scene_design_prompt", "scene-design-prompt"},
	"prop":      {"prop_design_prompt", "prop-design-prompt"},
}

// CreateTaskPromptJob 对齐 legacy create_task_prompt_job。step 为空指针等价
// 未传 Query 参数（仅 storyboard_shot 需要）。
func (s *TaskService) CreateTaskPromptJob(ctx context.Context, userID, role, taskID string, step *string) (*AgentRunItem, error) {
	task, err := s.tasks.EnsureTaskUpdateAccess(ctx, s.pool, taskID, taskActor(userID, role))
	if err != nil {
		return nil, mapTaskError(err, "Task not found", "Task permission denied")
	}
	if err := assertTaskPromptEditable(task); err != nil {
		return nil, err
	}
	if task.VariantKind != nil && *task.VariantKind == "human_temporary" {
		return nil, badRequest400("人工临时生产任务已跳过提示词流程，不能运行提示词 Agent。")
	}

	normalizedStep := ""
	if step != nil {
		normalizedStep = strings.ToLower(strings.TrimSpace(*step))
	}
	var agentType string
	switch task.TaskType {
	case "text_to_image", "asset":
		agentType = "text_to_image_prompt"
	case "image_to_video":
		agentType = "image_to_video_prompt"
	case "storyboard_shot":
		if normalizedStep != "keyframe" && normalizedStep != "video" {
			return nil, badRequest400("分镜任务需指定 step=keyframe 或 step=video")
		}
		if normalizedStep == "keyframe" {
			agentType = "text_to_image_prompt"
		} else {
			agentType = "image_to_video_prompt"
			if taskViewKeyframeSubmission(task.Submissions, "keyframe") == nil {
				return nil, badRequest400("关键帧尚未审核定版，视频提示词暂不能生成。")
			}
		}
	default:
		return nil, badRequest400("Task type does not support prompt generation")
	}

	storyboards, err := s.tasks.ListStoryboards(ctx, s.pool, task.ProjectId, nil, taskActor(userID, role), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	var storyboard *repository.StoryboardView
	if task.StoryboardId != nil {
		for i := range storyboards {
			if storyboards[i].ID == *task.StoryboardId {
				storyboard = &storyboards[i]
				break
			}
		}
	}

	assets, err := s.tasks.ListProjectAssetsFull(ctx, s.pool, task.ProjectId)
	if err != nil {
		return nil, err
	}
	var currentAsset *model.Assets
	if task.AssetId != nil {
		for _, a := range assets {
			if a.ID == *task.AssetId {
				currentAsset = a
				break
			}
		}
	}
	assetPayload := map[string]any{}
	if currentAsset != nil {
		assetPayload = AssetToPayload(currentAsset)
	}
	productionContext := TaskProductionContext(assetPayload, task)
	if currentAsset != nil && task.VariantKind != nil && *task.VariantKind != "" {
		merged := make(map[string]any, len(productionContext)+1)
		for k, v := range productionContext {
			merged[k] = v
		}
		merged["variant_requirement"] = map[string]any{
			"variant_code":   viewStrPtr(task.TaskVariant),
			"variant_kind":   viewStrPtr(task.VariantKind),
			"title_zh":       viewStrPtr(task.VariantTitleZh),
			"description_zh": viewStrPtr(task.VariantDescriptionZh),
		}
		productionContext = merged
	}
	specializedSkill := ""
	if (task.TaskType == "asset" || task.TaskType == "text_to_image") && currentAsset != nil {
		if spec, ok := assetAgentByType[currentAsset.AssetType]; ok {
			agentType, specializedSkill = spec[0], spec[1]
			if viewText(productionContext["schema_version"]) == "" {
				return nil, badRequest400("资产任务缺少专用生产上下文，无法生成资产 Prompt")
			}
		}
	}

	project, err := s.projects.FindByID(ctx, task.ProjectId)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	executionContext := map[string]any{}
	if task.EpisodeId != nil {
		scriptContext, err := s.projects.GetProjectScriptExecutionContext(ctx,
			task.ProjectId, userID, role, task.EpisodeId, task.ScriptVersionId)
		if err != nil {
			if key, ok := repository.MissingScriptContextKey(err); ok {
				return nil, badRequest400("任务制作上下文不完整：" + key)
			}
			return nil, err
		}
		if scriptContext == nil {
			scriptContext = map[string]any{}
		}
		prefix := "RF"
		if project.ProjectPrefix != nil && *project.ProjectPrefix != "" {
			prefix = *project.ProjectPrefix
		}
		brief := json.RawMessage{}
		if project.ProductionBrief != nil {
			brief = *project.ProductionBrief
		}
		executionContext, err = productionbrief.BuildAgentExecutionContext(productionbrief.ResolveInput{
			ProjectID:       task.ProjectId,
			ProjectTitle:    project.Title,
			ProjectPrefix:   prefix,
			ProductionBrief: brief,
			ScriptContext:   scriptContext,
		})
		if err != nil {
			return nil, badRequest400("任务制作上下文不完整：" + err.Error())
		}
	}

	agentReminders := s.tasks.BreakdownAgentReminders(ctx, s.pool, task.ProjectId, task.EpisodeId)
	productionReferences, err := s.tasks.ProductionReferencesForTask(ctx, s.pool, task.ID)
	if err != nil {
		return nil, err
	}
	var keyframeRef map[string]any
	if task.TaskType == "storyboard_shot" && normalizedStep == "video" {
		if sub := taskViewKeyframeSubmission(task.Submissions, "keyframe"); sub != nil {
			keyframeRef = promptKeyframeReference(task.ID, *sub)
		}
	}
	if task.TaskType == "image_to_video" && task.DependsOnTaskId != nil && *task.DependsOnTaskId != "" {
		dep, err := s.GetTask(ctx, userID, role, *task.DependsOnTaskId)
		if err != nil {
			return nil, err
		}
		if dep.Status != "completed" {
			return nil, badRequest400("关键帧任务尚未完成，视频任务暂未解锁。")
		}
		if sub := taskViewKeyframeSubmission(dep.Submissions, ""); sub != nil {
			keyframeRef = promptKeyframeReference(dep.ID, *sub)
		}
	}

	skill := specializedSkill
	if skill == "" {
		if agentType == "text_to_image_prompt" {
			skill = "text-to-image-prompt"
		} else {
			skill = "image-to-video-prompt"
		}
	}
	var fingerprintStep *string
	if normalizedStep != "" {
		fingerprintStep = &normalizedStep
	}
	contextFingerprint, err := s.tasks.TaskPromptContextFingerprint(ctx, s.pool, task.ID, fingerprintStep, productionReferences)
	if err != nil {
		return nil, err
	}

	taskTypeValue := task.TaskType
	if normalizedStep != "" {
		taskTypeValue = normalizedStep
	}
	if specializedSkill != "" && currentAsset != nil {
		taskTypeValue = currentAsset.AssetType
	}
	outputSpec := stringOf(productionContext, "output_spec")
	if outputSpec == "" && task.TaskVariant != nil {
		outputSpec = *task.TaskVariant
	}
	description := ""
	if task.LatestPromptText != nil && *task.LatestPromptText != "" {
		description = *task.LatestPromptText
	} else if task.PromptText != nil {
		description = *task.PromptText
	}

	input := make(map[string]any, 24+len(executionContext))
	for k, v := range executionContext {
		input[k] = v
	}
	input["task_id"] = task.ID
	input["skill"] = skill
	input["step"] = emptyToNil(normalizedStep)
	input["context_fingerprint"] = contextFingerprint
	input["storyboard_id"] = viewStrPtr(task.StoryboardId)
	input["task_type"] = taskTypeValue
	input["output_spec"] = emptyToNil(outputSpec)
	input["description"] = emptyToNil(description)
	if storyboard != nil {
		input["storyboard"] = StoryboardToPayload(*storyboard)
	} else {
		input["storyboard"] = map[string]any{}
	}
	assetPayloads := make([]map[string]any, 0, len(assets))
	for _, a := range assets {
		assetPayloads = append(assetPayloads, AssetToPayload(a))
	}
	input["assets"] = assetPayloads
	input["asset"] = assetPayload
	input["production_context"] = productionContext
	input["agent_reminders"] = agentReminders
	input["keyframe_reference"] = keyframeRef
	input["production_references"] = productionReferences
	input["style_guide"] = map[string]any{
		"resolved_style":            viewDict(productionContext["resolved_style"]),
		"cultural_origin":           viewDict(productionContext["cultural_origin"]),
		"global_visual_constraints": viewDict(productionContext["global_visual_constraints"]),
	}
	input["resolved_production_brief"] = viewDict(executionContext["resolved_production_brief"])
	if storyboard != nil {
		input["duration"] = storyboard.DurationSeconds
	} else {
		input["duration"] = nil
	}

	run, err := saveRunningRun(ctx, s.projects, s.agents, userID, runningJobSpec{
		agentType:    agentType,
		projectID:    &task.ProjectId,
		storyboardID: task.StoryboardId,
		taskID:       &task.ID,
		input:        input,
	})
	if err != nil {
		return nil, err
	}
	status := run.Status
	if status == "" {
		status = "running"
	}
	if err := s.projects.RegisterOperationLog(ctx, task.ProjectId, userID, "agent_run", run.ID,
		"agent_run_created", map[string]any{
			"task_id": task.ID, "agent_type": run.AgentType, "status": status,
		}); err != nil {
		return nil, err
	}
	return run, nil
}

// taskViewKeyframeSubmission 对齐 legacy `reversed(task.submissions)` 的 next(...)：
// Submissions 升序（created_at, id），此处从尾部往前取首个匹配。step 非空按
// step 匹配（storyboard_shot video），为空按 file_type=="image" 匹配（前置依赖）。
func taskViewKeyframeSubmission(subs []repository.SubmissionView, step string) *repository.SubmissionView {
	for i := len(subs) - 1; i >= 0; i-- {
		item := &subs[i]
		if item.Status != "primary_master" || !item.IsPrimary || item.IsArchived || item.IsInvalidated {
			continue
		}
		if step != "" {
			if item.Step != nil && *item.Step == step {
				return item
			}
		} else if item.FileType == "image" {
			return item
		}
	}
	return nil
}

// promptKeyframeReference 对齐 legacy keyframe_reference dict。
func promptKeyframeReference(taskID string, sub repository.SubmissionView) map[string]any {
	return map[string]any{
		"task_id":       taskID,
		"submission_id": sub.ID,
		"file_path":     sub.FilePath,
	}
}

// emptyToNil 对齐 `x or None`：空字符串 → JSON null。
func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

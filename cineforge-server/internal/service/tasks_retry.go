package service

// P4e 提示词任务重试分发（对齐 legacy retry_asset_prompt_task_distribution）：
//
//	POST /projects/{project_id}/breakdown-steps/prompts/{run_id}/retry-distribution
//
// 校验阶梯（逐字保留 legacy detail）：
//
//	项目/run 不存在    → 404 "AgentRun not found"
//	无写权限          → 403 "Project permission denied"
//	run 不属于项目     → 409 "AgentRun 不属于当前项目。"
//	非 asset_prompt_generation → 409 "当前 AgentRun 不是正式资产提示词步骤。"
//	缺 episode/version → 409 "资产提示词任务缺少 episode_id 或 script_version_id。"
//	非当前最新版本      → 409 "只能为当前分集的最新剧本版本重试任务分发。"
//	产物未完成          → 409 "资产提示词产物尚未完成，不能仅重试任务分发。"
//
// 成功路径复用 GenerateAssetTasks（P3f-d），并把 asset_prompt_finalization
// 合并为 succeeded + task_distribution 后回写 run。

import (
	"context"
	"encoding/json"

	"cineforge/server/internal/model"
)

// RetryAssetPromptTaskDistribution 对齐 legacy retry_asset_prompt_task_distribution。
func (s *TaskService) RetryAssetPromptTaskDistribution(ctx context.Context, userID, role, projectID, runID string) (*AgentRunItem, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("AgentRun not found")
	}
	canWrite, err := s.tasks.CanWriteProject(ctx, s.pool, projectID, userID, role)
	if err != nil {
		return nil, err
	}
	if !canWrite {
		return nil, forbidden403("Project permission denied")
	}
	run, err := s.projects.GetAgentRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, notFound404("AgentRun not found")
	}
	if run.ProjectId == nil || *run.ProjectId != projectID {
		return nil, conflict409("AgentRun 不属于当前项目。")
	}
	runInput := agentRunInputMap(run)
	if stringOf(runInput, "single_step") != "asset_prompt_generation" {
		return nil, conflict409("当前 AgentRun 不是正式资产提示词步骤。")
	}
	episodeID := stringOf(runInput, "episode_id")
	scriptVersionID := stringOf(runInput, "script_version_id")
	if episodeID == "" || scriptVersionID == "" {
		return nil, conflict409("资产提示词任务缺少 episode_id 或 script_version_id。")
	}
	episode, err := s.projects.FindEpisodeByID(ctx, episodeID)
	if err != nil {
		return nil, err
	}
	script, err := s.projects.FindScriptForEpisode(ctx, projectID, episodeID)
	if err != nil {
		return nil, err
	}
	if episode == nil || episode.ProjectId != projectID ||
		script == nil || script.CurrentVersionId == nil || *script.CurrentVersionId != scriptVersionID {
		return nil, conflict409("只能为当前分集的最新剧本版本重试任务分发。")
	}
	finalization := map[string]any{}
	if fin := asMapOf(agentRunOutputMap(run)["asset_prompt_finalization"]); fin != nil {
		finalization = fin
	}
	if stringOf(finalization, "prompt_status") != "succeeded" && stringOf(finalization, "status") != "succeeded" {
		return nil, conflict409("资产提示词产物尚未完成，不能仅重试任务分发。")
	}
	taskDistribution, err := s.GenerateAssetTasks(ctx, userID, role, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	output := agentRunOutputMap(run)
	output["asset_prompt_finalization"] = map[string]any{
		"status":            "succeeded",
		"prompt_status":     "succeeded",
		"task_distribution": taskDistribution,
		"error":             nil,
	}
	saved, err := s.projects.UpdateAgentRunResult(ctx, run.ID, "succeeded", output, nil)
	if err != nil {
		return nil, err
	}
	if err := s.projects.RegisterOperationLog(ctx, projectID, userID, "agent_run", run.ID, "asset_prompt_task_distribution_retried", map[string]any{
		"episode_id": episodeID, "script_version_id": scriptVersionID,
	}); err != nil {
		return nil, err
	}
	return toAgentRunItem(saved), nil
}

func agentRunInputMap(run *model.AgentRuns) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(run.InputJson, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func agentRunOutputMap(run *model.AgentRuns) map[string]any {
	if run.OutputJson == nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(*run.OutputJson, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// asMapOf 返回 map[string]any 视图；nil/非 map 返回 nil。
func asMapOf(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

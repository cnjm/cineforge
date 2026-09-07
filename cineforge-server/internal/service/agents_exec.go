package service

// P4e Agent 执行相关服务（对齐 legacy agents.py/projects.py/orchestrator.submit_background 契约）：
//
//   - SubmitAgentJob        → POST /agent-jobs（director/admin，status=running，202）
//   - SubmitProjectAgentJob → POST /projects/{pid}/agents/{type}/jobs（409 FORMAL gate、stage gate、pending 去重）
//   - MaterializeAgentRun   → POST /projects/{pid}/agents/runs/{run_id}/materialize（409/400/403/404 阶梯）
//   - RetryAssetPromptTaskDistribution → tasks_retry.go（TaskService）
//
// 非正式 job 统一走 saveRunningAgentJob：CreateAgentRun(status=running) → enqueue，
// 入队失败对齐 orchestrator.submit_background：run 置 failed 但仍按 202/200 返回。

import (
	"context"
	"encoding/json"
	"fmt"

	"cineforge/server/internal/agentclient"
	"cineforge/server/internal/repository"
)

// formalBreakdownAgentTypeSet 对齐 legacy FORMAL_BREAKDOWN_AGENT_TYPES。
var formalBreakdownAgentTypeSet = map[string]bool{
	"script_reading":            true,
	"asset_extract":             true,
	"script_segmentation":       true,
	"script_breakdown":          true,
	"relation_check":            true,
	"content_compliance_review": true,
}

// IsFormalBreakdownAgentType 是否正式拆解步骤 Agent（只能走分步流程，不能手工 job/物化）。
func IsFormalBreakdownAgentType(agentType string) bool {
	return formalBreakdownAgentTypeSet[agentType]
}

// materializationSupportedAgentTypeSet 对齐 legacy materialize_agent_run 的 supported_types。
var materializationSupportedAgentTypeSet = map[string]bool{
	"relation_check":            true,
	"content_compliance_review": true,
}

// materializeMessage 对齐 AgentRunMaterializeResponse.message。
const materializeMessage = "Agent 输出已写入业务草案，等待人工修正或导演锁定。"

// AgentJobInput 对齐 AgentJobCreate（POST /agent-jobs 请求体）。
type AgentJobInput struct {
	AgentType      string         `json:"agent_type"`
	ProjectID      *string        `json:"project_id"`
	StoryboardID   *string        `json:"storyboard_id"`
	TaskID         *string        `json:"task_id"`
	Input          map[string]any `json:"input"`
	IdempotencyKey *string        `json:"idempotency_key"`
}

// AgentRunMaterializeResult 对齐 AgentRunMaterializeResponse。
type AgentRunMaterializeResult struct {
	RunID                    string `json:"run_id"`
	ProjectID                string `json:"project_id"`
	AgentType                string `json:"agent_type"`
	CreatedScriptSegments    int    `json:"created_script_segments"`
	CreatedEntityVersions    int    `json:"created_entity_versions"`
	CreatedArtifactRevisions int    `json:"created_artifact_revisions"`
	CreatedTrainingSamples   int    `json:"created_training_samples"`
	Message                  string `json:"message"`
}

// runningJobSpec 非正式 job 的落库规格。
type runningJobSpec struct {
	agentType    string
	projectID    *string
	storyboardID *string
	taskID       *string
	episodeID    *string
	scriptID     *string
	workflowID   *string
	skillName    *string
	input        map[string]any
}

// SubmitAgentJob 对齐 legacy POST /agent-jobs → orchestrator.submit_background。
// agent_type 合法性（registry）由 api 层校验；本函数只做落库+入队。
// workflow_run_id 从 input 顶层提取（legacy submit_background 的 workflow_run_id 提取逻辑）。
func (s *ProjectService) SubmitAgentJob(ctx context.Context, userID string, job AgentJobInput) (*AgentRunItem, error) {
	if job.Input == nil {
		job.Input = map[string]any{}
	}
	var workflowID *string
	if raw, ok := job.Input["workflow_run_id"].(string); ok && raw != "" {
		workflowID = &raw
	}
	return s.saveRunningAgentJob(ctx, userID, runningJobSpec{
		agentType:    job.AgentType,
		projectID:    job.ProjectID,
		storyboardID: job.StoryboardID,
		taskID:       job.TaskID,
		workflowID:   workflowID,
		input:        job.Input,
	})
}

// SubmitProjectAgentJob 对齐 legacy POST /projects/{pid}/agents/{type}/jobs。
// stageOK 注入 api 层 stage-map 查询（服务层不依赖 registry 静态数据）。
func (s *ProjectService) SubmitProjectAgentJob(ctx context.Context, userID, role, projectID, agentType string, stageOK func(stage string) bool) (*AgentRunItem, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	ok, err := s.canWrite(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	stage := project.CurrentStage
	if stage == "" {
		stage = project.Status
	}
	if !stageOK(stage) {
		return nil, badRequest400("Agent is not available for this project stage")
	}
	pending, err := s.projects.PendingAgentRun(ctx, projectID, agentType, nil, nil)
	if err != nil {
		return nil, err
	}
	if pending != nil {
		return toAgentRunItem(pending), nil
	}
	return s.saveRunningAgentJob(ctx, userID, runningJobSpec{
		agentType: agentType,
		projectID: &project.ID,
		input: map[string]any{
			"project_id": project.ID,
			"skill":      agentType,
		},
	})
}

// saveRunningAgentJob 对齐 orchestrator.submit_background + agents 路由的 op-log：
// 落库 status=running → 入队 → 登记 op-log {agent_type, status}。op-log 在入队后登记，
// status 反映最终状态。
func (s *ProjectService) saveRunningAgentJob(ctx context.Context, userID string, spec runningJobSpec) (*AgentRunItem, error) {
	run, err := saveRunningRun(ctx, s.projects, s.agents, userID, spec)
	if err != nil {
		return nil, err
	}
	projectID := ""
	if spec.projectID != nil {
		projectID = *spec.projectID
	}
	status := run.Status
	if status == "" {
		status = "running"
	}
	if err := s.projects.RegisterOperationLog(ctx, projectID, userID, "agent_run", run.ID, "agent_run_created", map[string]any{
		"agent_type": spec.agentType, "status": status,
	}); err != nil {
		return nil, err
	}
	return run, nil
}

// saveRunningRun 对齐 orchestrator.submit_background（_save_background_agent_run 路径）：
// CreateAgentRun(status=running) → enqueue（P4c 后 agents 恒注入；为空时静默跳过）。
// 不登记 op-log——任务 prompt-jobs 路由按 legacy create_task_prompt_job 单独登记
// detail={task_id, agent_type, status}。入队失败对齐 legacy：run/workflow 置 failed，
// 但照常返回 AgentRunRead（202）。
func saveRunningRun(ctx context.Context, projects *repository.Projects, agents *agentclient.Client, userID string, spec runningJobSpec) (*AgentRunItem, error) {
	inputJSON, err := json.Marshal(spec.input)
	if err != nil {
		inputJSON = json.RawMessage(`{}`)
	}
	run, err := projects.CreateAgentRun(ctx, repository.AgentRunCreate{
		ID:            newUUIDOrErr(),
		ProjectID:     spec.projectID,
		EpisodeID:     spec.episodeID,
		ScriptID:      spec.scriptID,
		StoryboardID:  spec.storyboardID,
		TaskID:        spec.taskID,
		WorkflowRunID: spec.workflowID,
		AgentType:     spec.agentType,
		Status:        "running",
		InputJSON:     inputJSON,
		VersionNo:     1,
		DataState:     "agent_raw",
		SkillName:     spec.skillName,
	})
	if err != nil {
		return nil, err
	}
	if spec.workflowID != nil {
		if err := projects.UpdateWorkflowRunAgentRun(ctx, *spec.workflowID, run.ID); err != nil {
			return nil, err
		}
	}
	if agents != nil {
		if err := agents.EnqueueRun(ctx, run.ID); err != nil {
			msg := fmt.Sprintf("Agent 队列入队失败：%s", err)
			_ = projects.FailAgentRunEnqueue(ctx, run.ID, "", msg)
			if reloaded, err := projects.GetAgentRun(ctx, run.ID); err == nil && reloaded != nil {
				run = reloaded
			} else {
				run.Status = "failed"
				run.ErrorMessage = &msg
			}
		}
	}
	return toAgentRunItem(run), nil
}

// MaterializeAgentRun 对齐 legacy POST /projects/{pid}/agents/runs/{run_id}/materialize。
// 校验阶梯（逐字保留 legacy detail）：
//
//	项目/run 不存在         → 404 "Project or run not found"
//	无写权限               → 403 "Materialize permission denied"（legacy 该路径为未处理 500，此处收敛为 403）
//	run 不属于当前项目      → 400 "Agent run does not belong to this project"
//	FORMAL_BREAKDOWN 类型  → 409 "正式拆解步骤由分步流程自动写入，不能从 Agent 管理区手工物化。"
//	不支持的类型           → 400 "Agent type does not support materialization: {type}"
//	无输出                 → 400 "Agent run has no output"
//	关系校验缺 relation_report → 400 "关系校验输出缺少 relation_report"
func (s *ProjectService) MaterializeAgentRun(ctx context.Context, userID, role, projectID, runID string) (*AgentRunMaterializeResult, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project or run not found")
	}
	ok, err := s.canWrite(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Materialize permission denied")
	}
	run, err := s.projects.GetAgentRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, notFound404("Project or run not found")
	}
	if run.ProjectId == nil || *run.ProjectId != projectID {
		return nil, badRequest400("Agent run does not belong to this project")
	}
	if IsFormalBreakdownAgentType(run.AgentType) {
		return nil, conflict409("正式拆解步骤由分步流程自动写入，不能从 Agent 管理区手工物化。")
	}
	if !materializationSupportedAgentTypeSet[run.AgentType] {
		return nil, badRequest400("Agent type does not support materialization: " + run.AgentType)
	}
	if run.OutputJson == nil {
		return nil, badRequest400("Agent run has no output")
	}
	if run.AgentType == "relation_check" {
		output := repository.ArtifactOutput(run.OutputJson)
		if _, ok := output["relation_report"].(map[string]any); !ok {
			return nil, badRequest400("关系校验输出缺少 relation_report")
		}
	}
	result, err := s.projects.MaterializeAgentRun(ctx, repository.MaterializeAgentRunInput{
		Run: run, ProjectID: projectID, UserID: userID,
	})
	if err != nil {
		return nil, err
	}
	if err := s.projects.RegisterOperationLog(ctx, projectID, userID, "agent_run", run.ID, "agent_run_materialized", map[string]any{
		"agent_type":                 run.AgentType,
		"created_script_segments":    0,
		"created_entity_versions":    result.EntityVersionsCreated,
		"created_artifact_revisions": result.ArtifactRevisionsCreated,
		"created_training_samples":   result.TrainingSamplesCreated,
	}); err != nil {
		return nil, err
	}
	return &AgentRunMaterializeResult{
		RunID:                    run.ID,
		ProjectID:                projectID,
		AgentType:                run.AgentType,
		CreatedScriptSegments:    0,
		CreatedEntityVersions:    result.EntityVersionsCreated,
		CreatedArtifactRevisions: result.ArtifactRevisionsCreated,
		CreatedTrainingSamples:   result.TrainingSamplesCreated,
		Message:                  materializeMessage,
	}, nil
}

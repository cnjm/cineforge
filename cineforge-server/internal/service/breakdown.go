package service

// 五步拆解闸口 + 拆解落库（对齐 legacy projects.py breakdown-steps/{step} +
// breakdowns/current GET/POST）。
// P3d 是“闸口 + 落库”：agent_run 保持 queued、workflow_run 保持 running，
// 真正的模型执行在 P4 接 Agent 子模块。所有闸口错误文案与 legacy HTTPException 逐字一致。

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
)

// BREAKDOWN_STEP_MAP 对齐 legacy BREAKDOWN_STEP_MAP（正式五步）。
func breakdownStepMap() map[string]string {
	return map[string]string{
		"reading":      "script_reading",
		"assets":       "asset_extract",
		"prompts":      "asset_prompt_generation",
		"segmentation": "script_segmentation",
		"storyboard":   "storyboard_breakdown",
	}
}

// scriptScope 一次拆解执行作用域的分集/剧本/版本行。
type scriptScope struct {
	Episode       *model.ProjectEpisodes
	EpisodeNo     int32
	Script        *model.Scripts
	ScriptCode    string
	ScriptVersion *model.ScriptVersions
}

// AgentRunItem 对齐 AgentRunRead。
type AgentRunItem struct {
	ID               string           `json:"id"`
	ProjectID        *string          `json:"project_id"`
	ParentRunID      *string          `json:"parent_run_id"`
	StoryboardID     *string          `json:"storyboard_id"`
	TaskID           *string          `json:"task_id"`
	AgentType        string           `json:"agent_type"`
	Status           string           `json:"status"`
	Input            json.RawMessage  `json:"input"`
	Output           *json.RawMessage `json:"output"`
	ErrorMessage     *string          `json:"error_message"`
	DurationMs       *int32           `json:"duration_ms"`
	TokenUsage       *json.RawMessage `json:"token_usage"`
	FeedbackJSON     *json.RawMessage `json:"feedback_json"`
	Model            *string          `json:"model"`
	SkillName        *string          `json:"skill_name"`
	SkillVersion     *string          `json:"skill_version"`
	ContractVersion  *string          `json:"contract_version"`
	ContractHash     *string          `json:"contract_hash"`
	PromptVersion    *string          `json:"prompt_version"`
	InputHash        *string          `json:"input_hash"`
	NodeKey          *string          `json:"node_key"`
	WorkflowRunID    *string          `json:"workflow_run_id"`
	Summary          json.RawMessage  `json:"summary"`
	PayloadRef       *string          `json:"payload_ref"`
	PayloadSizeBytes *int32           `json:"payload_size_bytes"`
	PayloadSha256    *string          `json:"payload_sha256"`
	DataState        string           `json:"data_state"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

// BreakdownReadItem 对齐 BreakdownRead。
type BreakdownReadItem struct {
	ProjectID  string         `json:"project_id"`
	Version    int32          `json:"version"`
	View       map[string]any `json:"view"`
	DataState  string         `json:"data_state"`
	AgentRunID *string        `json:"agent_run_id"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// BreakdownSaveRequest 对齐 BreakdownSaveRequest。
type BreakdownSaveRequest struct {
	View            map[string]any `json:"view"`
	EpisodeCode     *string        `json:"episode_code"`
	EpisodeID       *string        `json:"episode_id"`
	ScriptVersionID *string        `json:"script_version_id"`
	ChangeSummary   []string       `json:"change_summary"`
	SourceLabel     string         `json:"source_label"`
	DataState       string         `json:"data_state"`
	ExpectedVersion *int32         `json:"expected_version"`
}

var breakdownPublicForbidden = map[string]bool{
	"raw_output":                       true,
	"raw_model_response":               true,
	"agent_raw_asset_output":           true,
	"debug_output":                     true,
	"characters":                       true,
	"scenes":                           true,
	"props":                            true,
	"agent_run_id":                     true,
	"data_state":                       true,
	"reading_output":                   true,
	"asset_output":                     true,
	"asset_reference_index":            true,
	"confirmed_reading_report":         true,
	"confirmed_asset_inventory":        true,
	"confirmed_asset_inventory_status": true,
}

// savedBreakdownKeys 对齐 legacy _NORMALIZED_WORKFLOW_VIEW_KEYS（保存时白名单）。
var savedBreakdownKeys = map[string]bool{
	"schema_version": true, "normalization_version": true, "source_label": true,
	"project_id": true, "project_no": true, "project_prefix": true,
	"episode_code": true, "episode_id": true, "script_version_id": true,
	"reading_report": true, "reading_review_state": true, "reading_artifact_id": true,
	"reading_revision_context": true, "readthrough": true, "dialogue_script": true,
	"assets": true, "global_review_items": true, "asset_review_state": true,
	"asset_prompt_designs": true, "asset_prompt_processing_summary": true,
	"asset_prompt_finalization": true, "costume_designs": true, "costume_processing_summary": true,
	"script_segments": true, "storyboards": true, "storyboard_shots": true,
	"coverage_checks": true, "scene_packages": true, "cross_references": true,
	"delivery_checklist": true, "content_review": true, "notes": true,
	"llm_provider": true, "llm_model": true,
}

var breakdownReviewStateKeys = map[string]bool{"status": true, "confirmed_revision_id": true, "confirmation_mode": true}

var breakdownReviewStatuses = map[string]bool{"needs_review": true, "pending_confirmation": true, "confirmed": true}

// ---- 拆解步骤提交（闸口 + 落库）----

// SubmitBreakdownStep 校验生产检查点，通过后创建 queued agent_run + running workflow_run。
// 返回值即 AgentRunRead（handler 回 202）。
func (s *ProjectService) SubmitBreakdownStep(ctx context.Context, userID, role, projectID, step string, episodeID, scriptVersionID *string) (*AgentRunItem, error) {
	singleStep, ok := breakdownStepMap()[step]
	if !ok {
		return nil, badRequest400(fmt.Sprintf("未知的拆解步骤：%s", step))
	}
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	ok, err = s.canWrite(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	if strings.TrimSpace(ptrOr(project.ScriptText, "")) == "" {
		return nil, badRequest400("项目暂无剧本文本，请先导入剧本。")
	}
	sc, err := s.resolveBreakdownScope(ctx, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	agentType := "script_segmentation"
	if singleStep != "script_segmentation" {
		agentType = "script_breakdown"
	}
	pending, err := s.projects.PendingAgentRun(ctx, projectID, agentType, &sc.Episode.ID, &sc.ScriptVersion.ID)
	if err != nil {
		return nil, err
	}
	if pending != nil {
		return toAgentRunItem(pending), nil
	}

	view := s.latestScopedView(ctx, projectID, &sc.Episode.ID, &sc.ScriptVersion.ID)
	if err := s.applyBreakdownStepGates(ctx, projectID, singleStep, sc, view); err != nil {
		return nil, err
	}
	return s.submitBreakdownWorkflowJob(ctx, project, sc, singleStep, view, userID)
}

// applyBreakdownStepGates 按步骤执行生产检查点（错误文案逐字对齐 legacy）。
func (s *ProjectService) applyBreakdownStepGates(ctx context.Context, projectID string, singleStep string, sc *scriptScope, view map[string]any) error {
	switch singleStep {
	case "script_reading":
		confirmed, err := s.confirmedReadingReportForScope(ctx, projectID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
		if err != nil {
			return err
		}
		if len(confirmed) > 0 {
			return conflict409("围读结果已确认，不能重复运行剧本围读。")
		}
	case "asset_extract":
		confirmedReading, err := s.confirmedReadingReportForScope(ctx, projectID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
		if err != nil {
			return err
		}
		if len(confirmedReading) == 0 {
			return badRequest400("请先确认围读结果，再运行资产拆解。")
		}
		inventory, err := s.confirmedAssetInventoryForScope(ctx, projectID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
		if err != nil {
			return err
		}
		if inventory.Status == "confirmed" {
			return conflict409("资产拆解结果已确认，当前剧本版本不能重复运行资产拆解。")
		}
	case "asset_prompt_generation", "asset_costume_design":
		inventory, err := s.confirmedAssetInventoryForScope(ctx, projectID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
		if err != nil {
			return err
		}
		if len(inventory.Assets) == 0 {
			return badRequest400("请先确认资产拆解结果，再生成资产提示词。")
		}
	case "storyboard_breakdown", "script_breakdown":
		if err := s.storyboardBreakdownGates(ctx, projectID, singleStep, sc, view); err != nil {
			return err
		}
	case "script_segmentation":
		confirmedSegments := confirmedSegmentsInView(view)
		if len(confirmedSegments) > 0 {
			return conflict409("脚本段已经确认，当前剧本版本不能重复运行脚本段拆分。")
		}
		confirmedReading, err := s.confirmedReadingReportForScope(ctx, projectID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
		if err != nil {
			return err
		}
		if len(confirmedReading) == 0 {
			return badRequest400("请先确认围读结果，再运行脚本段拆分。")
		}
		inventory, err := s.confirmedAssetInventoryForScope(ctx, projectID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
		if err != nil {
			return err
		}
		if len(inventory.Assets) == 0 {
			return badRequest400("请先人工确认资产清单，再运行脚本段拆分。")
		}
		missing := assetsMissingPromptDesign(inventory.Assets, view)
		if len(missing) > 0 {
			return conflict409(fmt.Sprintf("以下资产尚未经过专用 Prompt Skill：%s。请先补齐资产 Prompt。", strings.Join(missing, "、")))
		}
	case "content_review":
		// P4 接入；本次无额外闸口。
	default:
		return badRequest400(fmt.Sprintf("未知的正式拆解步骤：%s", singleStep))
	}
	return nil
}

// storyboardBreakdownGates 分镜拆解闸口（storyboard_breakdown/script_breakdown 共用）。
func (s *ProjectService) storyboardBreakdownGates(ctx context.Context, projectID string, singleStep string, sc *scriptScope, view map[string]any) error {
	distributed, err := s.projects.StoryboardTasksDistributed(ctx, projectID, sc.Episode.ID)
	if err != nil {
		return err
	}
	if distributed {
		return conflict409("本集分镜任务已经分发，不能再次运行分镜拆解。请先处理现有任务或建立新的分集。")
	}
	confirmedSegments, err := s.projects.ListScriptSegments(ctx, projectID, &sc.Episode.ID, "script_confirmed")
	if err != nil {
		return err
	}
	if len(confirmedSegments) == 0 {
		return badRequest400("请先运行脚本段拆分并确认脚本段，再运行分镜拆解。")
	}
	missingDialogue := scriptSegmentsMissingDialogueLines(confirmedSegments)
	if len(missingDialogue) > 0 {
		return conflict409(fmt.Sprintf("已确认脚本段缺少 dialogue_lines：%s。请重新运行并人工确认脚本段拆分。", strings.Join(missingDialogue, "、")))
	}
	inventory, err := s.confirmedAssetInventoryForScope(ctx, projectID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
	if err != nil {
		return err
	}
	if len(inventory.Assets) == 0 {
		return badRequest400("请先人工确认正式资产，再运行分镜拆解。")
	}
	missing := assetsMissingPromptDesign(inventory.Assets, view)
	if len(missing) > 0 {
		return conflict409(fmt.Sprintf("以下资产尚未经过专用 Prompt Skill：%s。请先补齐资产 Prompt。", strings.Join(missing, "、")))
	}
	return nil
}

// submitBreakdownWorkflowJob 组装 agent 载荷 + workflow 行 + agent_run 行（queued）。
func (s *ProjectService) submitBreakdownWorkflowJob(ctx context.Context, project *model.Projects, sc *scriptScope, singleStep string, view map[string]any, userID string) (*AgentRunItem, error) {
	workflowPhase := "single:" + singleStep
	executionContext := s.scriptExecutionContext(project, sc)
	_ = executionContext

	stepInputs := map[string]any{}
	confirmedReading, err := s.confirmedReadingReportForScope(ctx, project.ID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
	if err != nil {
		return nil, err
	}
	readingState := pendingConfirmationState()
	if rs := asMap(view["reading_review_state"]); rs != nil {
		readingState = normalizedBreakdownReviewState(rs)
	}

	// asset_extract / prompts / segmentation / storyboard 需要接已确认候选（step_inputs 对齐 legacy）。
	confirmedAssets := []map[string]any{}
	switch singleStep {
	case "asset_extract":
		stepInputs["reading_report"] = confirmedReading
		stepInputs["reading_review_state"] = readingState
		stepInputs["force_asset_extract"] = true
	case "asset_prompt_generation", "asset_costume_design":
		inventory, err := s.confirmedAssetInventoryForScope(ctx, project.ID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
		if err != nil {
			return nil, err
		}
		confirmedAssets = inventory.Assets
		stepInputs["reading_report"] = confirmedReading
		stepInputs["reading_review_state"] = readingState
		stepInputs["normalized_asset_inventory"] = map[string]any{"normalization_version": "AssetNormalization.v1", "assets": confirmedAssets}
		stepInputs["asset_review_state"] = asMapOrNil(view["asset_review_state"])
	case "script_segmentation":
		inventory, err := s.confirmedAssetInventoryForScope(ctx, project.ID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
		if err != nil {
			return nil, err
		}
		confirmedAssets = inventory.Assets
		stepInputs["reading_report"] = confirmedReading
		stepInputs["reading_review_state"] = readingState
		stepInputs["normalized_asset_inventory"] = map[string]any{"normalization_version": "AssetNormalization.v1", "assets": confirmedAssets}
		stepInputs["asset_review_state"] = asMapOrNil(view["asset_review_state"])
	case "storyboard_breakdown", "script_breakdown":
		confirmedSegments, err := s.projects.ListScriptSegments(ctx, project.ID, &sc.Episode.ID, "script_confirmed")
		if err != nil {
			return nil, err
		}
		segments := make([]map[string]any, 0, len(confirmedSegments))
		for i := range confirmedSegments {
			segments = append(segments, scriptSegmentPublic(&confirmedSegments[i]))
		}
		stepInputs["script_segments"] = segments
		inventory, err := s.confirmedAssetInventoryForScope(ctx, project.ID, &sc.Episode.ID, &sc.ScriptVersion.ID, view)
		if err != nil {
			return nil, err
		}
		confirmedAssets = inventory.Assets
		stepInputs["normalized_asset_inventory"] = map[string]any{"normalization_version": "AssetNormalization.v1", "assets": confirmedAssets}
		stepInputs["asset_review_state"] = asMapOrNil(view["asset_review_state"])
	}

	scriptText := storeString(executionContext["script_text"])
	var scriptSegmentsField []any
	if segs, ok := stepInputs["script_segments"].([]map[string]any); ok {
		for _, seg := range segs {
			scriptSegmentsField = append(scriptSegmentsField, seg)
		}
	}
	outputTarget := "full_breakdown"
	if singleStep == "script_segmentation" {
		outputTarget = "script_segments"
	}
	skill := "script-breakdown"
	if singleStep == "script_segmentation" {
		skill = "script-segmentation"
	}
	episodeNumber := sc.EpisodeNo
	if episodeNumber == 0 {
		episodeNumber = 1
	}
	style := "RF"
	if project.ProjectPrefix != nil && *project.ProjectPrefix != "" {
		style = *project.ProjectPrefix
	} else if project.ProjectNo != nil {
		style = *project.ProjectNo
	}
	semanticPayload := map[string]any{
		"project_id":                     project.ID,
		"episode_id":                     sc.Episode.ID,
		"episode_no":                     episodeNumber,
		"episode_code":                   sc.Episode.EpisodeCode,
		"script_id":                      sc.Script.ID,
		"script_code":                    sc.ScriptCode,
		"script_version_id":              sc.ScriptVersion.ID,
		"script_version_no":              sc.ScriptVersion.VersionNo,
		"script_text":                    scriptText,
		"script_text_chars":              len(scriptText),
		"content_hash":                   ptrOr(sc.ScriptVersion.ContentHash, ""),
		"production_brief":               briefOrEmpty(project.ProductionBrief),
		"episode_production_brief":       briefOrEmpty(sc.Episode.ProductionBrief),
		"project_title":                  project.Title,
		"project_prefix":                 style,
		"genre":                          project.Genre,
		"workflow":                       "script_breakdown_workflow",
		"workflow_phase":                 workflowPhase,
		"single_step":                    singleStep,
		"output_target":                  outputTarget,
		"persist_progress":               true,
		"existing_asset_master_snapshot": []any{}, // P3f 接入资产母版快照
	}
	for key, value := range stepInputs {
		semanticPayload[key] = value
	}

	fingerprint := repository.ContentHashString(semanticPayload)
	identity := map[string]any{
		"project_id": project.ID, "episode_id": sc.Episode.ID, "script_version_id": sc.ScriptVersion.ID,
		"workflow_phase": workflowPhase, "single_step": singleStep,
		"execution_fingerprint": fingerprint, "parent_workflow_id": nil,
	}
	idempotencyKey, err := canonicalHash(identity)
	if err != nil {
		return nil, err
	}

	// 幂等键命中：已存在相同输入的工作流，直接返回其 agent_run（未回填则 409）。
	existing, err := s.projects.FindWorkflowByIdempotencyKey(ctx, idempotencyKey)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.AgentRunId == nil {
			return nil, conflict409("相同输入的工作流正在提交，请稍后刷新运行状态。")
		}
		run, err := s.projects.GetAgentRun(ctx, *existing.AgentRunId)
		if err != nil {
			return nil, err
		}
		if run != nil {
			return toAgentRunItem(run), nil
		}
	}

	workflowInput := map[string]any{
		"script_text_chars":         len(scriptText),
		"script_segment_count":      len(scriptSegmentsField),
		"episode_num":               episodeNumber,
		"project_prefix":            style,
		"project_title":             project.Title,
		"genre":                     project.Genre,
		"production_brief":          briefOrEmpty(project.ProductionBrief),
		"episode_production_brief":  briefOrEmpty(sc.Episode.ProductionBrief),
		"resolved_production_brief": briefOrEmpty(project.ProductionBrief),
		"script_source":             executionContext,
		"trigger":                   "formal_step",
		"workflow_phase":            workflowPhase,
		"single_step":               singleStep,
		"force_asset_extract":       singleStep == "asset_extract",
		"execution_fingerprint":     fingerprint,
		"idempotency_key":           idempotencyKey,
	}
	workflowInputJSON, err := json.Marshal(workflowInput)
	if err != nil {
		return nil, err
	}
	workflow, err := s.projects.CreateWorkflowRun(ctx, repository.WorkflowRunCreate{
		ID:             newUUIDOrErr(),
		ProjectID:      project.ID,
		WorkflowType:   "script_breakdown_workflow",
		Status:         "running",
		Input:          workflowInputJSON,
		CreatedBy:      &userID,
		IDempotencyKey: &idempotencyKey,
		NodeStatus:     json.RawMessage(`{}`),
		Result:         json.RawMessage(`{}`),
		Error:          json.RawMessage(`{}`),
	})
	if err != nil {
		return nil, err
	}

	agentPayload := map[string]any{}
	for key, value := range semanticPayload {
		agentPayload[key] = value
	}
	agentPayload["workflow_run_id"] = workflow.ID
	agentPayload["trigger"] = "formal_step"
	agentInputJSON, err := json.Marshal(agentPayload)
	if err != nil {
		return nil, err
	}
	agentType := "script_breakdown"
	if singleStep == "script_segmentation" {
		agentType = "script_segmentation"
	}
	skillName := skill
	inputHash := fingerprint
	run, err := s.projects.CreateAgentRun(ctx, repository.AgentRunCreate{
		ID:              newUUIDOrErr(),
		ProjectID:       &project.ID,
		EpisodeID:       &sc.Episode.ID,
		ScriptID:        &sc.Script.ID,
		WorkflowRunID:   &workflow.ID,
		AgentType:       agentType,
		Status:          "queued",
		InputJSON:       agentInputJSON,
		VersionNo:       1,
		DataState:       "agent_raw",
		SkillName:       &skillName,
		SkillVersion:    nil,
		ContractVersion: nil,
		PromptVersion:   nil,
		InputHash:       &inputHash,
		NodeKey:         &workflowPhase,
	})
	if err != nil {
		return nil, err
	}
	if err := s.projects.UpdateWorkflowRunAgentRun(ctx, workflow.ID, run.ID); err != nil {
		return nil, err
	}
	if err := s.projects.RegisterOperationLog(ctx, project.ID, userID, "agent_run", run.ID, "breakdown_step_created", map[string]any{
		"single_step": singleStep, "status": "queued",
	}); err != nil {
		return nil, err
	}
	// P4：把 queued run 投递给 Python agent 子模块（bridge :8091 → Celery，
	// task_id=run_id 幂等）。入队失败对齐 legacy orchestrator.submit_background：
	// run/workflow 置为 failed 但仍按 202 返回（前端显示失败并允许合法重试）。
	if s.agents != nil {
		if err := s.agents.EnqueueRun(ctx, run.ID); err != nil {
			msg := fmt.Sprintf("Agent 队列入队失败：%s", err)
			_ = s.projects.FailAgentRunEnqueue(ctx, run.ID, workflow.ID, msg)
			run.Status = "failed"
			run.ErrorMessage = &msg
		}
	}
	return toAgentRunItem(run), nil
}

// ---- 拆解读侧 ----

// CurrentBreakdown GET /breakdowns/current；无匹配作用域时返回 (nil, nil)（200 null）。
func (s *ProjectService) CurrentBreakdown(ctx context.Context, userID, role, projectID string, episodeID, scriptVersionID *string) (*BreakdownReadItem, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	ok, err := s.canRead(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	b, err := s.projects.LatestScopedBreakdown(ctx, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	view := savedContentToView(b.ContentJson)
	overlay, err := s.overlayAuthoritativeReading(ctx, projectID, episodeID, scriptVersionID, view)
	if err != nil {
		return nil, err
	}
	return &BreakdownReadItem{
		ProjectID:  projectID,
		Version:    b.Version,
		View:       publicBreakdownView(overlay),
		DataState:  b.DataState,
		AgentRunID: b.AgentRunId,
		UpdatedAt:  b.UpdatedAt,
	}, nil
}

// SaveBreakdown POST /breakdowns/current；乐观锁 + 围读/资产确认 revision 追加 + 落库。
func (s *ProjectService) SaveBreakdown(ctx context.Context, userID, role, projectID string, data BreakdownSaveRequest) (*BreakdownReadItem, error) {
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
	if err := validateBreakdownViewForSave(data.View); err != nil {
		return nil, badRequest400(err.Error())
	}
	// 乐观锁：并发保存保护（expected_version 缺省时按 last-write-wins）。
	if data.ExpectedVersion != nil {
		current, err := s.projects.LatestScopedBreakdown(ctx, projectID, data.EpisodeID, data.ScriptVersionID)
		if err != nil {
			return nil, err
		}
		if current != nil && current.Version != *data.ExpectedVersion {
			return nil, conflict409(fmt.Sprintf("拆解草稿已被其他保存更新（当前版本 v%d，你编辑的是 v%d）。请刷新后重试，避免覆盖他人修改。", current.Version, *data.ExpectedVersion))
		}
	}
	previous := s.latestScopedView(ctx, projectID, data.EpisodeID, data.ScriptVersionID)
	content := s.buildBreakdownContent(project, data, userID, previous)
	if err := s.appendReadingReportRevisions(ctx, project, content, userID); err != nil {
		return nil, err
	}
	if err := s.appendConfirmedAssetInventoryRevision(ctx, project, content, userID); err != nil {
		return nil, err
	}
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	dataState := data.DataState
	if dataState == "" {
		dataState = "human_revision"
	}
	if dataState != "agent_raw" && dataState != "normalized" && dataState != "human_revision" && dataState != "final" {
		dataState = "human_revision"
	}
	saved, err := s.projects.CreateBreakdownVersion(ctx, contentJSON, projectID, &userID, dataState)
	if err != nil {
		return nil, err
	}
	if err := s.projects.UpdateProjectStage(ctx, projectID, "breakdown_review"); err != nil {
		return nil, err
	}
	if err := s.projects.RegisterOperationLog(ctx, projectID, userID, "script_breakdown", saved.ID, "breakdown_revision_saved", map[string]any{
		"version":     saved.Version,
		"storyboards": len(sliceOfMapAny(content["storyboards"])),
		"assets":      len(mapSliceOfMapAny(content["assets"])),
	}); err != nil {
		return nil, err
	}
	view := savedContentToView(contentJSON)
	overlay, err := s.overlayAuthoritativeReading(ctx, projectID, data.EpisodeID, data.ScriptVersionID, view)
	if err != nil {
		return nil, err
	}
	return &BreakdownReadItem{
		ProjectID:  projectID,
		Version:    saved.Version,
		View:       publicBreakdownView(overlay),
		DataState:  saved.DataState,
		AgentRunID: nil,
		UpdatedAt:  saved.UpdatedAt,
	}, nil
}

// ---- 作用域解析 ----

// resolveBreakdownScope 对齐 legacy _resolve_breakdown_scope。
func (s *ProjectService) resolveBreakdownScope(ctx context.Context, projectID string, episodeID, scriptVersionID *string) (*scriptScope, error) {
	if episodeID == nil && scriptVersionID == nil {
		episodes, err := s.projects.ListEpisodes(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if len(episodes) != 1 {
			return nil, badRequest400("请明确选择分集和剧本版本后再执行拆解。")
		}
		script, err := s.projects.FindScriptForEpisode(ctx, projectID, episodes[0].ID)
		if err != nil {
			return nil, err
		}
		if script == nil || script.CurrentVersionId == nil {
			return nil, badRequest400("请明确选择分集和剧本版本后再执行拆解。")
		}
		episodeID = &episodes[0].ID
		versionID := *script.CurrentVersionId
		scriptVersionID = &versionID
	} else if episodeID == nil || scriptVersionID == nil {
		return nil, badRequest400("episode_id 和 script_version_id 必须同时提供。")
	}
	sc, err := s.resolveScriptScope(ctx, projectID, *episodeID, *scriptVersionID)
	if err != nil {
		return nil, badRequest400("分集与剧本版本不匹配。")
	}
	return sc, nil
}

// resolveScriptScope 校验 episode/script/script_version 三者匹配（对齐 get_project_script_execution_context 的 KeyError）。
func (s *ProjectService) resolveScriptScope(ctx context.Context, projectID, episodeID, scriptVersionID string) (*scriptScope, error) {
	episode, err := s.projects.FindEpisodeByID(ctx, episodeID)
	if err != nil {
		return nil, err
	}
	if episode == nil || episode.ProjectId != projectID {
		return nil, fmt.Errorf("scope episode mismatch")
	}
	script, err := s.projects.FindScriptForEpisode(ctx, projectID, episodeID)
	if err != nil {
		return nil, err
	}
	version, err := s.projects.FindScriptVersionByID(ctx, scriptVersionID)
	if err != nil {
		return nil, err
	}
	if script == nil || version == nil || version.ScriptId != script.ID {
		return nil, fmt.Errorf("scope version mismatch")
	}
	return &scriptScope{
		Episode: episode, EpisodeNo: episode.EpisodeNo, Script: script,
		ScriptCode: script.ScriptCode, ScriptVersion: version,
	}, nil
}

// scriptExecutionContext 对齐 _build_agent_execution_context 的剧本上下文。
func (s *ProjectService) scriptExecutionContext(project *model.Projects, sc *scriptScope) map[string]any {
	return map[string]any{
		"episode_id":                sc.Episode.ID,
		"episode_no":                sc.EpisodeNo,
		"episode_code":              sc.Episode.EpisodeCode,
		"script_id":                 sc.Script.ID,
		"script_code":               sc.ScriptCode,
		"script_version_id":         sc.ScriptVersion.ID,
		"script_version_no":         sc.ScriptVersion.VersionNo,
		"script_text":               sc.ScriptVersion.Content,
		"content_hash":              ptrOr(sc.ScriptVersion.ContentHash, ""),
		"source_file_id":            ptrOr(sc.ScriptVersion.SourceFileId, ""),
		"original_filename":         ptrOr(sc.ScriptVersion.OriginalFilename, ""),
		"parser_name":               ptrOr(sc.ScriptVersion.ParserName, ""),
		"language":                  ptrOr(sc.ScriptVersion.Language, ""),
		"episode_production_brief":  briefOrEmpty(sc.Episode.ProductionBrief),
		"production_brief":          briefOrEmpty(project.ProductionBrief),
		"resolved_production_brief": briefOrEmpty(project.ProductionBrief),
	}
}

// ---- 已确认读源（DB 权威）----

// confirmedReadingReportForScope 对齐 legacy _confirmed_reading_report_for_scope：
// view.reading_review_state confirmed + confirmed_revision_id 命中 human/final reading_report revision。
func (s *ProjectService) confirmedReadingReportForScope(ctx context.Context, projectID string, episodeID, scriptVersionID *string, view map[string]any) (map[string]any, error) {
	reviewState := asMap(view["reading_review_state"])
	revisionID := storeString(mapStringAny(reviewState)["confirmed_revision_id"])
	if stringOf(reviewState, "status") != "confirmed" || revisionID == "" {
		return map[string]any{}, nil
	}
	revision, err := s.projects.GetArtifactRevisionByID(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	if !confirmedReadingRevisionValid(revision, projectID, episodeID, scriptVersionID) {
		return map[string]any{}, nil
	}
	report := map[string]any{}
	_ = json.Unmarshal(revision.NormalizedContent, &report)
	return report, nil
}

func confirmedReadingRevisionValid(rev *model.ArtifactRevisions, projectID string, episodeID, scriptVersionID *string) bool {
	if rev == nil || rev.ProjectId != projectID || rev.ArtifactType != "reading_report" ||
		rev.SourceType != "human" || rev.Status != "confirmed" || rev.DataState != "final" {
		return false
	}
	context := map[string]any{}
	_ = json.Unmarshal(rev.InputSnapshot, &context)
	if episodeID != nil && storeString(context["episode_id"]) != *episodeID {
		return false
	}
	if scriptVersionID != nil && storeString(context["script_version_id"]) != *scriptVersionID {
		return false
	}
	return true
}

// confirmedAssetInventoryForScope 对齐 legacy _confirmed_asset_inventory_for_scope。
func (s *ProjectService) confirmedAssetInventoryForScope(ctx context.Context, projectID string, episodeID, scriptVersionID *string, view map[string]any) (*confirmedInventory, error) {
	reviewState := asMap(view["asset_review_state"])
	status := stringOf(reviewState, "status")
	revisionID := storeString(mapStringAny(reviewState)["confirmed_revision_id"])
	if status != "confirmed" || revisionID == "" {
		return &confirmedInventory{Status: orDefault(status, "missing"), Assets: nil}, nil
	}
	revision, err := s.projects.GetArtifactRevisionByID(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	if revision == nil || revision.ProjectId != projectID || revision.ArtifactType != "asset_inventory" ||
		revision.SourceType != "human" || revision.Status != "confirmed" || revision.DataState != "final" {
		return &confirmedInventory{Status: "missing", Assets: nil}, nil
	}
	context := map[string]any{}
	_ = json.Unmarshal(revision.InputSnapshot, &context)
	if episodeID != nil && storeString(context["episode_id"]) != *episodeID {
		return &confirmedInventory{Status: "missing", Assets: nil}, nil
	}
	if scriptVersionID != nil && storeString(context["script_version_id"]) != *scriptVersionID {
		return &confirmedInventory{Status: "missing", Assets: nil}, nil
	}
	normalized := map[string]any{}
	_ = json.Unmarshal(revision.NormalizedContent, &normalized)
	assets := mapSliceOfMapAny(normalized["assets"])
	return &confirmedInventory{
		Status:     "confirmed",
		Assets:     assets,
		RevisionID: &revision.ID,
	}, nil
}

type confirmedInventory struct {
	Status     string
	Assets     []map[string]any
	RevisionID *string
}

// overlayAuthoritativeReading 对齐 legacy _overlay_authoritative_reading_revision（GET 读侧回填）。
func (s *ProjectService) overlayAuthoritativeReading(ctx context.Context, projectID string, episodeID, scriptVersionID *string, view map[string]any) (map[string]any, error) {
	authoritative := copyMap(view)
	for _, key := range []string{
		"confirmed_reading_report", "reading_output", "reading_revision_id",
		"confirmed_reading_revision_id", "reading_data_state", "reading_source",
	} {
		delete(authoritative, key)
	}
	reviewState := asMap(authoritative["reading_review_state"])
	revisionID := storeString(mapStringAny(reviewState)["confirmed_revision_id"])
	if episodeID == nil || stringOf(reviewState, "status") != "confirmed" || revisionID == "" {
		authoritative["reading_review_state"] = normalizedBreakdownReviewState(reviewState)
		return authoritative, nil
	}
	revision, err := s.projects.GetArtifactRevisionByID(ctx, revisionID)
	if err != nil {
		return nil, err
	}
	if !confirmedReadingRevisionValid(revision, projectID, episodeID, scriptVersionID) {
		authoritative["reading_review_state"] = pendingConfirmationState()
		return authoritative, nil
	}
	report := map[string]any{}
	_ = json.Unmarshal(revision.NormalizedContent, &report)
	context := map[string]any{}
	_ = json.Unmarshal(revision.InputSnapshot, &context)
	authoritative["reading_report"] = report
	authoritative["reading_artifact_id"] = revision.ArtifactId
	authoritative["reading_revision_context"] = context
	authoritative["reading_review_state"] = map[string]any{
		"status": "confirmed", "confirmed_revision_id": revision.ID,
		"confirmation_mode": "human_confirmed",
	}
	return authoritative, nil
}

// ---- 拆解保存 ----

// buildBreakdownContent 对齐 legacy _breakdown_content_from_request 的字段落库形态。
func (s *ProjectService) buildBreakdownContent(project *model.Projects, data BreakdownSaveRequest, userID string, previous map[string]any) map[string]any {
	view := copyMap(data.View)
	episodeID := strRefOr(data.EpisodeID, viewStringPtr(view, "episode_id"))
	scriptVersionID := strRefOr(data.ScriptVersionID, viewStringPtr(view, "script_version_id"))
	episodeCode := ""
	if data.EpisodeCode != nil && strings.TrimSpace(*data.EpisodeCode) != "" {
		episodeCode = strings.TrimSpace(*data.EpisodeCode)
	}
	assets := mapSliceOfMapAny(view["assets"])
	globalReviewItems := mapSliceOfMapAny(view["global_review_items"])
	content := map[string]any{
		"normalization_version": orDefault(viewString(view, "normalization_version"), "AssetNormalization.v1"),
		"source_label":          orDefault(data.SourceLabel, "human_modified"),
		"project_id":            project.ID,
		"project_no":            project.ProjectNo,
		"project_prefix":        project.ProjectPrefix,
		"episode_code":          ifStrNonEmpty(episodeCode, viewString(view, "episode_code")),
		"episode_id":            strRefOrString(episodeID),
		"script_version_id":     strRefOrString(scriptVersionID),
		"script_segments":       mapSliceOfMapAny(view["script_segments"]),
		"storyboards":           sortStoryboards(mapSliceOfMapAny(view["storyboards"])),
		"assets":                assets,
		"global_review_items":   globalReviewItems,
		"change_summary":        strSliceOrEmpty(data.ChangeSummary),
		"edited_by":             userID,
		"edited_at":             time.Now().UTC().Format(time.RFC3339),
	}
	// 保留检查点视图字段：确认状态不重建；由 _append_reading/asset 在保存时
	// 把显式人工确认写入 confirmed_revision_id 再落库。
	for _, key := range []string{"reading_report", "reading_review_state", "asset_review_state"} {
		if value, ok := view[key]; ok {
			content[key] = value
		}
	}
	inherited := []string{
		"asset_prompt_designs", "asset_prompt_processing_summary", "costume_designs",
		"costume_processing_summary", "asset_prompt_finalization",
	}
	for _, key := range inherited {
		if _, exists := content[key]; !exists {
			if value, ok := previous[key]; ok {
				content[key] = value
			}
		}
	}
	return content
}

// appendReadingReportRevisions 对齐 legacy _append_reading_report_revisions
// （显式人工确认 → agent/system/human 三级 revision + episode.summary）。
func (s *ProjectService) appendReadingReportRevisions(ctx context.Context, project *model.Projects, content map[string]any, userID string) error {
	readingReport := asMap(content["reading_report"])
	reviewState := asMap(content["reading_review_state"])
	if readingReport == nil || stringOf(reviewState, "status") != "confirmed" {
		return nil
	}
	confirmedReport := readingReport // P3d 不做候选 code 重排（P3f 编号）
	confirmedRevisionID := storeString(mapStringAny(reviewState)["confirmed_revision_id"])
	if confirmedRevisionID != "" {
		previous, err := s.projects.GetArtifactRevisionByID(ctx, confirmedRevisionID)
		if err != nil {
			return err
		}
		if previous == nil || previous.ProjectId != project.ID || previous.ArtifactType != "reading_report" ||
			previous.Status != "confirmed" ||
			repository.ContentHashString(previous.NormalizedContent) != repository.ContentHashString(confirmedReport) {
			content["reading_review_state"] = pendingConfirmationState()
		}
		return nil
	}
	if !explicitHumanConfirmation(reviewState) {
		content["reading_review_state"] = pendingConfirmationState()
		return nil
	}
	content["reading_report"] = confirmedReport

	episodeID := storeString(content["episode_id"])
	scriptVersionID := storeString(content["script_version_id"])
	episodeCode := viewString(content, "episode_code")
	if episodeCode == "" {
		episodeCode = "EP01"
	}
	resolved, err := s.resolveConfirmedReadingScope(ctx, project, episodeID, scriptVersionID, episodeCode)
	if err != nil {
		return err
	}
	seed := resolved.scriptVersion.ID
	artifactSeed := "cineforge:reading-report:" + seed
	artifactID := uuid5URL(artifactSeed)
	type revisionKey struct {
		sourceType string
		payload    map[string]any
		status     string
		dataState  string
	}
	inputSnapshot := map[string]any{
		"project_id":        project.ID,
		"episode_id":        resolved.episode.ID,
		"episode_code":      episodeCode,
		"script_id":         resolved.script.ID,
		"script_version_id": resolved.scriptVersion.ID,
		"script_version_no": resolved.scriptVersion.VersionNo,
		"content_hash":      resolved.scriptVersion.ContentHash,
	}
	profiles := projectProfileHashes(project)
	for key, value := range profiles {
		inputSnapshot[key] = value
	}
	brief := map[string]any{}
	if project.ProductionBrief != nil {
		_ = json.Unmarshal(*project.ProductionBrief, &brief)
	}
	inputSnapshot["style_catalog_version"] = brief["style_catalog_version"]
	inputSnapshot["primary_style_id"] = brief["primary_style_id"]
	inputSnapshot["production_brief"] = brief
	inputSnapshot["episode_production_brief_hash"] = repository.ContentHashString(briefOrEmpty(resolved.episode.ProductionBrief))

	revisions, err := s.projects.ListArtifactRevisionsForArtifact(ctx, "reading_report", artifactID)
	if err != nil {
		return err
	}
	appendOne := func(sourceType string, payload map[string]any, status, dataState string) (*model.ArtifactRevisions, error) {
		digest := repository.ContentHashString(payload)
		for _, item := range revisions {
			if item.SourceType == sourceType && item.ContentHash == digest {
				return &item, nil
			}
		}
		var parentID *string
		var parentVersion int32
		if len(revisions) > 0 {
			last := revisions[len(revisions)-1]
			parentID = &last.ID
			parentVersion = last.VersionNo
		}
		var previousContentHash any
		if len(revisions) > 0 {
			pre := revisions[len(revisions)-1].ContentHash
			previousContentHash = pre
		}
		changeDiff, _ := json.Marshal(map[string]any{
			"previous_content_hash": previousContentHash,
			"source_transition":     fmt.Sprintf("%s->%s", revisionSourceOf(revisions), sourceType),
		})
		rev := &model.ArtifactRevisions{
			ID:                newUUIDOrErr(),
			ProjectId:         project.ID,
			ArtifactType:      "reading_report",
			ArtifactId:        artifactID,
			VersionNo:         parentVersion + 1,
			ParentRevisionId:  parentID,
			SourceType:        sourceType,
			SkillName:         strPtr("script-reading"),
			InputSnapshot:     mustJSON(inputSnapshot),
			RawOutput:         json.RawMessage(`{}`),
			NormalizedContent: mustJSON(payload),
			ChangeDiff:        changeDiff,
			ContentHash:       digest,
			Status:            status,
			DataState:         dataState,
			IDempotencyKey:    nil,
		}
		if sourceType == "human" {
			rev.CreatedBy = &userID
		}
		if err := s.projects.AppendArtifactRevision(ctx, rev); err != nil {
			return nil, err
		}
		revisions = append(revisions, *rev)
		return rev, nil
	}
	if _, err := appendOne("agent", confirmedReport, "agent_raw", "agent_raw"); err != nil {
		return err
	}
	if _, err := appendOne("system", confirmedReport, "normalized", "normalized"); err != nil {
		return err
	}
	confirmedRev, err := appendOne("human", confirmedReport, "confirmed", "final")
	if err != nil {
		return err
	}
	// episode.summary 回填（_reading_summary(confirmed_report)）。
	if summary := readingSummaryFromMap(confirmedReport); summary != nil {
		if err := s.projects.UpdateEpisodeSummary(ctx, resolved.episode.ID, *summary); err != nil {
			return err
		}
	}
	content["reading_review_state"] = map[string]any{
		"status": "confirmed", "confirmed_revision_id": confirmedRev.ID, "confirmation_mode": "human_confirmed",
	}
	return nil
}

type confirmedReadingScope struct {
	episode       *model.ProjectEpisodes
	script        *model.Scripts
	scriptVersion *model.ScriptVersions
}

// resolveConfirmedReadingScope 对齐 legacy 围读确认稿的来源解析。
func (s *ProjectService) resolveConfirmedReadingScope(ctx context.Context, project *model.Projects, episodeID, scriptVersionID, episodeCode string) (*confirmedReadingScope, error) {
	episode := (*model.ProjectEpisodes)(nil)
	if episodeID != "" {
		ep, err := s.projects.FindEpisodeByID(ctx, episodeID)
		if err != nil {
			return nil, err
		}
		episode = ep
	} else {
		episodes, err := s.projects.ListEpisodes(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		for i := range episodes {
			if episodes[i].EpisodeCode == episodeCode {
				episode = &episodes[i]
				break
			}
		}
	}
	if episode == nil || episode.ProjectId != project.ID {
		return nil, badRequest400("围读确认稿的分集与剧本版本来源不匹配。")
	}
	script, err := s.projects.FindScriptForEpisode(ctx, project.ID, episode.ID)
	if err != nil {
		return nil, err
	}
	version := (*model.ScriptVersions)(nil)
	if scriptVersionID != "" {
		v, err := s.projects.FindScriptVersionByID(ctx, scriptVersionID)
		if err != nil {
			return nil, err
		}
		version = v
	} else if script != nil && script.CurrentVersionId != nil {
		v, err := s.projects.FindScriptVersionByID(ctx, *script.CurrentVersionId)
		if err != nil {
			return nil, err
		}
		version = v
	}
	if script == nil || version == nil || !episodeMatchesScript(episode, script) {
		return nil, badRequest400("围读确认稿的分集与剧本版本来源不匹配。")
	}
	if version.ScriptId != script.ID {
		return nil, badRequest400("围读确认稿缺少有效的剧本版本来源。")
	}
	return &confirmedReadingScope{episode: episode, script: script, scriptVersion: version}, nil
}

func episodeMatchesScript(episode *model.ProjectEpisodes, script *model.Scripts) bool {
	return script.EpisodeId != nil && *script.EpisodeId == episode.ID
}

// appendConfirmedAssetInventoryRevision 对齐 legacy _append_confirmed_asset_inventory_revision
// （显式人工确认 assets → 落 human/final/confirmed asset_inventory revision；Asset 行物化在 P3f）。
func (s *ProjectService) appendConfirmedAssetInventoryRevision(ctx context.Context, project *model.Projects, content map[string]any, userID string) error {
	reviewState := asMap(content["asset_review_state"])
	if stringOf(reviewState, "status") != "confirmed" {
		return nil
	}
	confirmedRevisionID := storeString(mapStringAny(reviewState)["confirmed_revision_id"])
	if confirmedRevisionID == "" && !explicitHumanConfirmation(reviewState) {
		content["asset_review_state"] = pendingConfirmationState()
		return nil
	}
	assets := mapSliceOfMapAny(content["assets"])
	if len(assets) == 0 {
		return nil
	}
	episodeID := storeString(content["episode_id"])
	scriptVersionID := storeString(content["script_version_id"])
	if episodeID == "" || scriptVersionID == "" {
		return badRequest400("资产确认稿缺少分集或剧本版本来源，不能保存正式资产清单。")
	}
	scope := scriptVersionID
	if scope == "" {
		scope = episodeID
	}
	if scope == "" {
		scope = project.ID
	}
	artifactID := uuid5URL("cineforge:asset_inventory:" + project.ID + ":" + scope)
	globalReviewItems := mapSliceOfMapAny(content["global_review_items"])
	normalizedContent := map[string]any{"status": "confirmed", "assets": assets, "global_review_items": globalReviewItems}
	digest := repository.ContentHashString(normalizedContent)

	revisions, err := s.projects.ListArtifactRevisionsForArtifact(ctx, "asset_inventory", artifactID)
	if err != nil {
		return err
	}
	var currentHuman *model.ArtifactRevisions
	for i := range revisions {
		rev := revisions[i]
		if rev.SourceType == "human" && rev.Status == "confirmed" {
			currentHuman = &rev
		}
	}
	if currentHuman != nil && currentHuman.ContentHash == digest {
		content["asset_review_state"] = map[string]any{
			"status": "confirmed", "confirmed_revision_id": currentHuman.ID, "confirmation_mode": "human_confirmed",
		}
		return nil
	}
	inputSnapshot := map[string]any{
		"project_id": project.ID, "episode_id": episodeID, "script_version_id": scriptVersionID,
		"confirmed_reading_revision_id": lineageText(mapStringAny(reviewState)["confirmed_reading_revision_id"], asMap(content["reading_review_state"])),
	}
	profiles := projectProfileHashes(project)
	for key, value := range profiles {
		inputSnapshot[key] = value
	}
	var parentID *string
	var parentVersion int32
	var previousContentHash any
	if currentHuman != nil {
		parentID = &currentHuman.ID
		parentVersion = currentHuman.VersionNo
		previousContentHash = currentHuman.ContentHash
	} else if len(revisions) > 0 {
		last := revisions[len(revisions)-1]
		parentID = &last.ID
		parentVersion = last.VersionNo
		previousContentHash = last.ContentHash
	}
	changeDiff, _ := json.Marshal(map[string]any{
		"previous_content_hash": previousContentHash, "confirmation": "human_confirmed",
	})
	rev := &model.ArtifactRevisions{
		ID:                newUUIDOrErr(),
		ProjectId:         project.ID,
		ArtifactType:      "asset_inventory",
		ArtifactId:        artifactID,
		VersionNo:         parentVersion + 1,
		ParentRevisionId:  parentID,
		SourceType:        "human",
		SkillName:         strPtr("asset-extract"),
		InputSnapshot:     mustJSON(inputSnapshot),
		RawOutput:         json.RawMessage(`{}`),
		NormalizedContent: mustJSON(normalizedContent),
		ChangeDiff:        changeDiff,
		ContentHash:       digest,
		Status:            "confirmed",
		CreatedBy:         &userID,
		DataState:         "final",
	}
	if err := s.projects.AppendArtifactRevision(ctx, rev); err != nil {
		return err
	}
	content["asset_review_state"] = map[string]any{
		"status": "confirmed", "confirmed_revision_id": rev.ID, "confirmation_mode": "human_confirmed",
	}
	return nil
}

// ---- 拆解判定 / 资产提示词闸口 ----

// confirmedSegmentsInView 读取 view.script_segments 中 status=script_confirmed 的分段（对齐 segmentation 闸口）。
func confirmedSegmentsInView(view map[string]any) []map[string]any {
	var out []map[string]any
	for _, raw := range mapSliceOfAny(view["script_segments"]) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		metadata := asMap(item["metadata_json"])
		if metadata == nil {
			metadata = asMap(item["metadata"])
		}
		if stringOf(metadata, "status") == "script_confirmed" {
			out = append(out, item)
		}
	}
	return out
}

// scriptSegmentsMissingDialogueLines 对齐 legacy _script_segments_missing_dialogue_lines。
func scriptSegmentsMissingDialogueLines(segments []model.ScriptSegments) []string {
	missing := []string{}
	for index, segment := range segments {
		metadata := map[string]any{}
		_ = json.Unmarshal(segment.MetadataJson, &metadata)
		if dialogueLines, exists := metadata["dialogue_lines"]; exists {
			if _, ok := dialogueLines.([]any); ok {
				continue
			}
		}
		code := strings.TrimSpace(segment.ScriptSegmentCode)
		if code == "" {
			code = fmt.Sprintf("第 %d 段", index+1)
		}
		missing = append(missing, code)
	}
	return missing
}

// assetsMissingPromptDesign 对齐 legacy assets_missing_prompt_design（视图设计/摘要 + 资产内 prompt 判定）。
func assetsMissingPromptDesign(rawAssets []map[string]any, view map[string]any) []string {
	designs := []map[string]any{}
	for _, key := range []string{"asset_prompt_designs", "costume_designs"} {
		designs = append(designs, mapSliceOfMapAny(view[key])...)
	}
	summaries := []map[string]any{}
	for _, key := range []string{"asset_prompt_processing_summary", "costume_processing_summary"} {
		summaries = append(summaries, mapSliceOfMapAny(view[key])...)
	}
	missing := []string{}
	for _, asset := range rawAssets {
		assetType := strings.ToLower(strings.TrimSpace(orDefault(viewString(asset, "asset_type"), viewString(asset, "type"))))
		if assetType == "character" && characterIsNonVisual(asset) {
			continue
		}
		if lifecycleStatus := assetLifecycleStatus(asset); lifecycleStatus == "needs_completion" || lifecycleStatus == "prompt_pending" {
			missing = append(missing, assetLifecycleLabel(asset))
			continue
		}
		metadata := asMap(asset["metadata"])
		if metadata == nil {
			metadata = map[string]any{}
		}
		promptDesign := asMap(metadata["prompt_design"])
		promptLineage := asMap(metadata["prompt_lineage"])
		if anyStr(asset, "prompt") || anyStr(asset, "input_hash") || anyStr(asset, "skill") ||
			anyStr(promptDesign, "prompt") || anyStr(promptLineage, "skill") {
			continue
		}
		matchedDesign := firstMatchingRecord(designs, asset)
		if matchedDesign != nil && !boolOf(matchedDesign["error"]) &&
			(anyStr(matchedDesign, "prompt") || anyStr(matchedDesign, "input_hash") || anyStr(matchedDesign, "skill")) {
			continue
		}
		matchedSummary := firstMatchingRecord(summaries, asset)
		if matchedSummary != nil && strings.ToLower(strings.TrimSpace(viewString(matchedSummary, "processing_status"))) == "reused" {
			continue
		}
		missing = append(missing, assetLifecycleLabel(asset))
	}
	return missing
}

// promptRecordMatchesAsset 对齐 legacy prompt_record_matches_asset。
func promptRecordMatchesAsset(record, asset map[string]any) bool {
	assetKey := strings.TrimSpace(viewString(asset, "client_asset_key"))
	recordKey := strings.TrimSpace(viewString(record, "client_asset_key"))
	if assetKey != "" && recordKey != "" {
		return assetKey == recordKey
	}
	assetCode := strings.ToUpper(strings.TrimSpace(viewString(asset, "asset_code")))
	recordCodes := map[string]bool{}
	for _, key := range []string{"asset_code", "matched_asset_code"} {
		if value := strings.ToUpper(strings.TrimSpace(viewString(record, key))); value != "" {
			recordCodes[value] = true
		}
	}
	return assetCode != "" && recordCodes[assetCode]
}

func firstMatchingRecord(records []map[string]any, asset map[string]any) map[string]any {
	for _, record := range records {
		if promptRecordMatchesAsset(record, asset) {
			return record
		}
	}
	return nil
}

// assetLifecycleStatus 对齐 legacy _asset_lifecycle_status。
func assetLifecycleStatus(asset map[string]any) string {
	metadata := asMap(asset["metadata"])
	if metadata == nil {
		metadata = map[string]any{}
	}
	value := strings.ToLower(strings.TrimSpace(orDefault(viewString(metadata, "confirmation_status"), viewString(asset, "status"))))
	switch value {
	case "needs_completion", "prompt_pending", "pending_confirmation", "confirmed":
		return value
	default:
		return "pending_confirmation"
	}
}

// assetLifecycleLabel 对齐 legacy _asset_lifecycle_label。
func assetLifecycleLabel(asset map[string]any) string {
	code := strings.TrimSpace(viewString(asset, "asset_code"))
	shortCode := code
	if idx := strings.LastIndex(code, "-"); idx >= 0 {
		shortCode = code[idx+1:]
	}
	name := strings.TrimSpace(viewString(asset, "name"))
	parts := []string{}
	if shortCode != "" {
		parts = append(parts, shortCode)
	}
	if name != "" {
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return "未命名资产"
	}
	return strings.Join(parts, " ")
}

// characterIsNonVisual 对齐 legacy _character_is_non_visual（合并 attributes+metadata 信号）。
func characterIsNonVisual(asset map[string]any) bool {
	merged := map[string]any{}
	for key, value := range asMap(asset["attributes"]) {
		merged[key] = value
	}
	for key, value := range asMap(asset["metadata"]) {
		merged[key] = value
	}
	value := orDefault(viewString(asset, "visual_presence"),
		orDefault(viewString(asset, "presence_mode"),
			orDefault(viewString(asset, "appearance_scope"), viewString(merged, "visual_presence"))))
	normalized := normalizeVisualToken(value)
	markers := []string{"mentioned_only", "mentioned", "背景提及", "voice_only", "voice", "off_screen",
		"仅提及", "未出场", "不出镜", "画外音", "仅声音"}
	for _, marker := range markers {
		if normalized == marker || strings.Contains(normalized, marker) {
			return true
		}
	}
	if normalized != "" {
		return false
	}
	typeText := normalizeVisualToken(orDefault(viewString(asset, "character_type"), viewString(merged, "character_type")))
	for _, marker := range markers {
		if strings.Contains(typeText, marker) {
			return true
		}
	}
	return false
}

func normalizeVisualToken(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized
}

// ---- 读视图工具 ----

// savedContentToView 对齐 legacy _breakdown_view_from_saved_content（剔除公开禁写字段）。
func savedContentToView(content json.RawMessage) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(content, &m); err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	for key, value := range m {
		if !breakdownPublicForbidden[key] {
			out[key] = value
		}
	}
	return out
}

// publicBreakdownView 对齐 legacy _breakdown_read_from_view（review_state 归一化）。
func publicBreakdownView(view map[string]any) map[string]any {
	out := copyMap(view)
	for _, key := range []string{"reading_review_state", "asset_review_state"} {
		if state, ok := out[key].(map[string]any); ok {
			out[key] = normalizedBreakdownReviewState(state)
		}
	}
	return out
}

// normalizedBreakdownReviewState 对齐 legacy _normalized_breakdown_review_state。
func normalizedBreakdownReviewState(state map[string]any) map[string]any {
	status := stringOf(state, "status")
	if !breakdownReviewStatuses[status] {
		status = "needs_review"
	}
	normalized := map[string]any{"status": status}
	if value, exists := state["confirmed_revision_id"]; exists {
		if value == nil {
			normalized["confirmed_revision_id"] = nil
		} else {
			normalized["confirmed_revision_id"] = fmt.Sprintf("%v", value)
		}
	}
	if value, exists := state["confirmation_mode"]; exists {
		if value == nil {
			normalized["confirmation_mode"] = nil
		} else {
			normalized["confirmation_mode"] = fmt.Sprintf("%v", value)
		}
	}
	return normalized
}

// validateBreakdownViewForSave 对齐 legacy _validate_breakdown_view_for_save。
func validateBreakdownViewForSave(view map[string]any) error {
	unknown := []string{}
	for key := range view {
		if !savedBreakdownKeys[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("拆解 view 包含未定义字段：%s", strings.Join(unknown, ", "))
	}
	for _, key := range []string{"reading_review_state", "asset_review_state"} {
		state := asMap(view[key])
		if state == nil {
			return fmt.Errorf("拆解 view 缺少有效的 %s", key)
		}
		extra := []string{}
		for stateKey := range state {
			if !breakdownReviewStateKeys[stateKey] {
				extra = append(extra, stateKey)
			}
		}
		if len(extra) > 0 {
			sort.Strings(extra)
			return fmt.Errorf("%s 包含未定义字段：%s", key, strings.Join(extra, ", "))
		}
		if status := stringOf(state, "status"); !breakdownReviewStatuses[status] {
			return fmt.Errorf("%s.status 不是有效审核状态", key)
		}
	}
	return nil
}

// latestScopedView 项目最新匹配作用域拆解视图（失败→空 map）。
func (s *ProjectService) latestScopedView(ctx context.Context, projectID string, episodeID, scriptVersionID *string) map[string]any {
	b, err := s.projects.LatestScopedBreakdown(ctx, projectID, episodeID, scriptVersionID)
	if err != nil || b == nil {
		return map[string]any{}
	}
	return savedContentToView(b.ContentJson)
}

// ---- 工具 ----

// toAgentRunItem 对齐 AgentRunRead（output 缺省 null；summary 缺省 {}）。
func toAgentRunItem(run *model.AgentRuns) *AgentRunItem {
	if run == nil {
		return nil
	}
	summary := run.SummaryJson
	if summary == nil {
		summary = &[]json.RawMessage{{'{', '}'}}[0]
	}
	return &AgentRunItem{
		ID: run.ID, ProjectID: run.ProjectId, ParentRunID: run.ParentRunId,
		StoryboardID: run.StoryboardId, TaskID: run.TaskId, AgentType: run.AgentType,
		Status: run.Status, Input: run.InputJson, Output: run.OutputJson,
		ErrorMessage: run.ErrorMessage, DurationMs: run.DurationMs, TokenUsage: run.TokenUsage,
		FeedbackJSON: run.FeedbackJson, Model: run.Model, SkillName: run.SkillName,
		SkillVersion: run.SkillVersion, ContractVersion: run.ContractVersion, ContractHash: run.ContractHash,
		PromptVersion: run.PromptVersion, InputHash: run.InputHash, NodeKey: run.NodeKey,
		WorkflowRunID: run.WorkflowRunId, Summary: *summary, PayloadRef: run.PayloadRef,
		PayloadSizeBytes: run.PayloadSizeBytes, PayloadSha256: run.PayloadSha256,
		DataState: run.DataState, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

func explicitHumanConfirmation(reviewState map[string]any) bool {
	if stringOf(reviewState, "status") != "confirmed" {
		return false
	}
	value, exists := reviewState["confirmed_revision_id"]
	if !exists || value != nil {
		return false
	}
	return stringOf(reviewState, "confirmation_mode") == "human_confirmed"
}

func pendingConfirmationState() map[string]any {
	return map[string]any{"status": "pending_confirmation"}
}

// uuid5URL 实现 RFC 4122 UUIDv5（结合 NAMESPACE_URL 与 Python uuid.uuid5 一致）。
func uuid5URL(name string) string {
	namespace := []byte{
		0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1,
		0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8,
	}
	h := sha1.New()
	h.Write(namespace)
	h.Write([]byte(name))
	sum := h.Sum(nil)
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// canonicalHash sha256(紧凑排序 JSON)。
func canonicalHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var compact map[string]any
	if err := json.Unmarshal(raw, &compact); err != nil {
		return "", err
	}
	buf, err := json.Marshal(compact)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

func newUUIDOrErr() string {
	id, err := auth.NewUUID()
	if err != nil {
		return ""
	}
	return id
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for key, value := range m {
		out[key] = value
	}
	return out
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asMapOrNil(v any) any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func mapStringAny(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func mapSliceOfAny(v any) []any {
	if items, ok := v.([]any); ok {
		return items
	}
	return nil
}

func mapSliceOfMapAny(v any) []map[string]any {
	// buildBreakdownContent 等路径存储的是 []map[string]any（经 mapSliceOfMapAny 归一化后的切片）；
	// 直接命中该类型避免 mapSliceOfAny 的 []any 断言失败。
	if typed, ok := v.([]map[string]any); ok {
		return typed
	}
	var out []map[string]any
	for _, raw := range mapSliceOfAny(v) {
		if item, ok := raw.(map[string]any); ok {
			out = append(out, item)
		}
	}
	return out
}

func sliceOfMapAny(v any) []map[string]any {
	return mapSliceOfMapAny(v)
}

func viewString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case *string:
		if v == nil {
			return ""
		}
		return *v
	default:
		return ""
	}
}

func viewStringPtr(m map[string]any, key string) *string {
	if m == nil {
		return nil
	}
	switch v := m[key].(type) {
	case string:
		if v == "" {
			return nil
		}
		return &v
	default:
		return nil
	}
}

func strRefOr(a, b *string) *string {
	if a != nil {
		return a
	}
	return b
}

// strRefOrString 解引用 *string 用于 content 落库（避免把指针地址写进 JSON/视图）。
func strRefOrString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func anyStr(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	switch v := m[key].(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case json.RawMessage:
		return len(v) > 0 && strings.TrimSpace(string(v)) != "null"
	default:
		return v != nil
	}
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

func strSliceOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func storeString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func ifStrNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func lineageText(values ...any) string {
	for _, value := range values {
		if v := storeString(value); v != "" {
			return v
		}
	}
	return ""
}

func revisionSourceOf(revisions []model.ArtifactRevisions) string {
	if len(revisions) == 0 {
		return "none"
	}
	return revisions[len(revisions)-1].SourceType
}

func sortStoryboards(in []map[string]any) []map[string]any {
	return in // P3d 保持读取顺序；排序在 P3e 分镜物料化时处理
}

func scriptSegmentPublic(segment *model.ScriptSegments) map[string]any {
	return map[string]any{
		"id":                  segment.ID,
		"project_id":          segment.ProjectId,
		"episode_id":          segment.EpisodeId,
		"script_segment_code": segment.ScriptSegmentCode,
		"episode_code":        segment.EpisodeCode,
		"order_no":            segment.OrderNo,
		"title":               segment.Title,
		"source_text":         segment.SourceText,
		"summary":             segment.Summary,
		"story_function":      segment.StoryFunction,
		"dominant_emotion":    segment.DominantEmotion,
		"rhythm":              segment.Rhythm,
		"viewpoint":           segment.Viewpoint,
		"context_code":        segment.ContextCode,
		"render_mode":         segment.RenderMode,
		"metadata_json":       segment.MetadataJson,
		"current_version_id":  segment.CurrentVersionId,
		"created_by":          segment.CreatedBy,
		"created_at":          segment.CreatedAt,
		"updated_at":          segment.UpdatedAt,
	}
}

package service

// P3d 回补：项目域管理动作服务（soft-delete / script-segments confirm /
// breakdown lock / episode hard delete）。
// 对齐 legacy repositories.py soft_delete_project、confirm_script_segments、
// lock_breakdown_episode、hard_delete_project_episode + 公共 _append_human_final_artifact_revision。
// 契约约定：KeyError→404、PermissionError→403、ValueError→400、HTTPException 逐字 detail。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
)

// ScriptSegmentsConfirmItem 对齐 ScriptSegmentsConfirmResponse。
type ScriptSegmentsConfirmItem struct {
	ProjectID          string  `json:"project_id"`
	EpisodeCode        *string `json:"episode_code"`
	ScriptSegmentCount int     `json:"script_segment_count"`
	BreakdownVersion   int32   `json:"breakdown_version"`
	NextStage          string  `json:"next_stage"`
}

// BreakdownLockItem 对齐 BreakdownLockResponse。
type BreakdownLockItem struct {
	ProjectID           string  `json:"project_id"`
	EpisodeCode         *string `json:"episode_code"`
	BreakdownVersion    int32   `json:"breakdown_version"`
	TrainingSampleCount int     `json:"training_sample_count"`
	AssetCount          int     `json:"asset_count"`
	CreatedAssetTasks   int     `json:"created_asset_tasks"`
	TaskCount           int     `json:"task_count"`
}

// ProjectEpisodeDeleteItem 对齐 ProjectEpisodeDeleteResponse。
type ProjectEpisodeDeleteItem struct {
	ProjectID     string         `json:"project_id"`
	EpisodeID     string         `json:"episode_id"`
	EpisodeCode   string         `json:"episode_code"`
	DeletedCounts map[string]int `json:"deleted_counts"`
	FileCleanup   map[string]any `json:"file_cleanup"`
}

// BreakdownLockRequest 对齐 BreakdownLockRequest（BreakdownSaveRequest + create_asset_tasks）。
type BreakdownLockRequest struct {
	BreakdownSaveRequest
	CreateAssetTasks bool `json:"create_asset_tasks"`
}

// ---- SoftDeleteProject ----

// SoftDeleteProject 对齐 legacy soft_delete_project：admin 密码校验（先）→ 404（后）→ 软删。
func (s *ProjectService) SoftDeleteProject(ctx context.Context, userID, role, projectID, adminPassword string, reason *string) (*ProjectItem, error) {
	if !isDirectorOrAdminRole(role) {
		return nil, forbidden403("Admin password verification failed")
	}
	if err := s.verifyAdminPassword(ctx, userID, adminPassword); err != nil {
		return nil, err
	}
	if reason != nil && len(*reason) > 500 {
		return nil, badRequest400("reason 不能超过 500 个字符")
	}
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	if err := s.projects.SoftDeleteProject(ctx, projectID, userID, reason); err != nil {
		return nil, err
	}
	refreshed, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if refreshed == nil {
		// 软删后理论上仍存在；兜底返回原始信息。
		refreshed = project
	}
	counts, err := s.projects.CountsDirector(ctx, refreshed.ID)
	if err != nil {
		return nil, err
	}
	item := toProjectItem(refreshed, counts.StoryboardCount, counts.TaskCount)
	return &item, nil
}

// verifyAdminPassword 对齐 legacy _verify_admin_password：admin 本人密码或任一活跃 admin 密码。
func (s *ProjectService) verifyAdminPassword(ctx context.Context, userID, adminPassword string) error {
	var ownHash *string
	user, err := s.users.FindByID(ctx, userID)
	if err == nil && user != nil {
		ownHash = user.PasswordHash
	}
	if user != nil && user.Role == "admin" && ownHash != nil && auth.VerifyPassword(*ownHash, adminPassword) {
		return nil
	}
	hashes, err := s.projects.ActiveAdminPasswordHashes(ctx)
	if err != nil {
		return err
	}
	for _, hash := range hashes {
		if auth.VerifyPassword(hash, adminPassword) {
			return nil
		}
	}
	return forbidden403("Admin password verification failed")
}

// ---- ConfirmScriptSegments ----

// ConfirmScriptSegments 对齐 legacy confirm_script_segments。
func (s *ProjectService) ConfirmScriptSegments(ctx context.Context, userID, role, projectID string, data BreakdownSaveRequest) (*ScriptSegmentsConfirmItem, error) {
	if !isDirectorOrAdminRole(role) {
		return nil, forbidden403("Project permission denied")
	}
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	previous := s.latestScopedView(ctx, projectID, data.EpisodeID, data.ScriptVersionID)
	data.SourceLabel = "human_confirmed"
	content := s.buildBreakdownContent(project, data, userID, previous)
	if err := s.requireAssetPromptDesignsFromContent(ctx, projectID, content); err != nil {
		return nil, err
	}
	episodeCode := ""
	if content["episode_code"] != nil {
		episodeCode = repository.NormalizeEpisodeCode(fmt.Sprintf("%v", content["episode_code"]))
	}
	if episodeCode != "" {
		content["episode_code"] = episodeCode
		filtered := make([]map[string]any, 0)
		for _, item := range mapSliceOfMapAny(content["script_segments"]) {
			if repository.NormalizeEpisodeCode(viewString(item, "episode_code")) == episodeCode {
				filtered = append(filtered, item)
			}
		}
		content["script_segments"] = filtered
	}
	if len(mapSliceOfMapAny(content["script_segments"])) == 0 {
		return nil, badRequest400("当前没有脚本原文段，不能确认并进入分镜/资产拆分。请先运行脚本段拆分或人工新增脚本段。")
	}

	finalContent := map[string]any{"script_segments": mapSliceOfMapAny(content["script_segments"])}
	finalRevision, err := s.appendHumanFinalArtifactRevision(ctx, project, "script_segmentation", content, finalContent, userID, "confirmed")
	if err != nil {
		return nil, err
	}
	rawOutput := mapStringAny(asMap(content["raw_output"]))
	rawOutput["confirmed_script_segmentation_revision_id"] = finalRevision.ID
	content["raw_output"] = rawOutput

	if project.CurrentStage == "asset_locking" {
		existingVersion, ok, err := s.projects.LatestScriptConfirmedBreakdownVersion(ctx, projectID, strPtrOrNil(episodeCode))
		if err != nil {
			return nil, err
		}
		if ok {
			return &ScriptSegmentsConfirmItem{
				ProjectID:          projectID,
				EpisodeCode:        strPtrOrNil(episodeCode),
				ScriptSegmentCount: len(mapSliceOfMapAny(content["script_segments"])),
				BreakdownVersion:   existingVersion,
				NextStage:          "asset_locking",
			}, nil
		}
	}

	result, err := s.projects.ConfirmScriptSegmentsTx(ctx, projectID, content, userID)
	if err != nil {
		return nil, err
	}
	return &ScriptSegmentsConfirmItem{
		ProjectID:          projectID,
		EpisodeCode:        strPtrOrNil(episodeCode),
		ScriptSegmentCount: result.ConfirmedCount,
		BreakdownVersion:   result.Version,
		NextStage:          "asset_locking",
	}, nil
}

// ---- LockBreakdownEpisode ----

// LockBreakdownEpisode 对齐 legacy lock_breakdown_episode。
func (s *ProjectService) LockBreakdownEpisode(ctx context.Context, userID, role, projectID string, data BreakdownLockRequest) (*BreakdownLockItem, error) {
	if !isDirectorOrAdminRole(role) {
		return nil, forbidden403("Project permission denied")
	}
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	previous := s.latestScopedView(ctx, projectID, data.EpisodeID, data.ScriptVersionID)
	data.SourceLabel = "human_modified"
	content := s.buildBreakdownContent(project, data.BreakdownSaveRequest, userID, previous)
	if err := s.requireAssetPromptDesignsFromContent(ctx, projectID, content); err != nil {
		return nil, err
	}
	episodeCode := ""
	if content["episode_code"] != nil {
		episodeCode = repository.NormalizeEpisodeCode(fmt.Sprintf("%v", content["episode_code"]))
	}
	if episodeCode != "" {
		content["episode_code"] = episodeCode
		content["script_segments"] = filterMapSlice(content["script_segments"], func(item map[string]any) bool {
			return repository.NormalizeEpisodeCode(viewString(item, "episode_code")) == episodeCode
		})
		content["storyboards"] = filterMapSlice(content["storyboards"], func(item map[string]any) bool {
			return storyboardEpisodeCodeOf(item) == episodeCode
		})
		content["assets"] = filterMapSlice(content["assets"], func(item map[string]any) bool {
			return assetBelongsToEpisodeOf(item, episodeCode)
		})
	}
	if !hasLockableOutputs(content) {
		return nil, badRequest400("当前只有脚本原文段，尚未生成分镜或资产清单，不能锁定本集并生成任务。请先完成分镜拆解或人工补充分镜/资产。")
	}
	blocking := validateBreakdownViewForLock(content)
	if len(blocking) > 0 {
		details := strings.Join(takeStr(blocking, 8), "；")
		return nil, badRequest400("分镜终版校验失败：" + details)
	}

	finalContent := map[string]any{
		"script_segments": mapSliceOfMapAny(content["script_segments"]),
		"storyboards":     mapSliceOfMapAny(content["storyboards"]),
	}
	finalRevision, err := s.appendHumanFinalArtifactRevision(ctx, project, "storyboard_breakdown", content, finalContent, userID, "locked")
	if err != nil {
		return nil, err
	}
	rawOutput := mapStringAny(asMap(content["raw_output"]))
	rawOutput["confirmed_storyboard_revision_id"] = finalRevision.ID
	content["raw_output"] = rawOutput

	result, err := s.projects.LockBreakdownEpisodeTx(ctx, projectID, content, userID, data.CreateAssetTasks)
	if err != nil {
		return nil, err
	}
	return &BreakdownLockItem{
		ProjectID:           projectID,
		EpisodeCode:         strPtrOrNil(episodeCode),
		BreakdownVersion:    result.Version,
		TrainingSampleCount: 1,
		AssetCount:          result.AssetCount,
		CreatedAssetTasks:   result.CreatedAssetTasks,
		TaskCount:           result.TaskCount,
	}, nil
}

// ---- HardDeleteProjectEpisode ----

// HardDeleteProjectEpisode 对齐 legacy delete_project_episode 路由 +
// hard_delete_project_episode + cleanup_deleted_episode_files（P3f-e：真实 MinIO 对象删除）。
func (s *ProjectService) HardDeleteProjectEpisode(ctx context.Context, userID, role, projectID, episodeID, confirmEpisodeCode string, deleteFiles bool) (*ProjectEpisodeDeleteItem, error) {
	if !isDirectorOrAdminRole(role) {
		return nil, forbidden403("Episode deletion permission denied")
	}
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	snap, ok, err := s.projects.FindEpisodeForDelete(ctx, projectID, episodeID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notFound404("Episode not found")
	}
	if strings.ToUpper(strings.TrimSpace(confirmEpisodeCode)) != strings.ToUpper(snap.EpisodeCode) {
		return nil, badRequest400(fmt.Sprintf("删除确认不匹配，请输入分集编号 %s。", snap.EpisodeCode))
	}
	result, err := s.projects.HardDeleteProjectEpisodeTx(ctx, projectID, episodeID, snap.EpisodeCode, deleteFiles, userID)
	if err != nil {
		if strings.Contains(err.Error(), pgxErrNoRowsText) {
			return nil, notFound404("Episode not found")
		}
		return nil, err
	}
	succeeded := []map[string]any{}
	failed := []map[string]any{}
	for _, item := range result.CleanupObjects {
		if s.objects == nil {
			failed = append(failed, mapItemWithError(item, "minio not configured"))
			continue
		}
		bucket, _ := item["bucket"].(string)
		objectKey, _ := item["object_key"].(string)
		if err := s.objects.Remove(ctx, bucket, objectKey); err != nil {
			failed = append(failed, mapItemWithError(item, err.Error()))
			continue
		}
		succeeded = append(succeeded, item)
	}
	fileCleanup := map[string]any{
		"succeeded":       len(succeeded),
		"failed":          len(failed),
		"shared_retained": len(result.SharedObjects),
		"pending_retry":   len(failed),
		"failed_objects":  failed,
		"shared_objects":  result.SharedObjects,
	}
	// 无论 delete_files 真假都跑终局（对齐 legacy cleanup_deleted_episode_files 恒执行）：
	// 删除成功清理的文件行，并把操作日志 file_cleanup 回填为最终计数形状。
	if err := s.projects.FinalizeEpisodeDeleteFileCleanupTx(ctx, result.OpLogID, succeeded, failed, result.SharedObjects); err != nil {
		return nil, err
	}
	return &ProjectEpisodeDeleteItem{
		ProjectID:     result.ProjectID,
		EpisodeID:     result.EpisodeID,
		EpisodeCode:   result.EpisodeCode,
		DeletedCounts: result.DeletedCounts,
		FileCleanup:   fileCleanup,
	}, nil
}

func mapItemWithError(item map[string]any, message string) map[string]any {
	out := make(map[string]any, len(item)+1)
	for k, v := range item {
		out[k] = v
	}
	out["error"] = message
	return out
}

const pgxErrNoRowsText = "no rows in result set"

// ---- 公共 ----

// appendHumanFinalArtifactRevision 对齐 legacy _append_human_final_artifact_revision：
// source_run 存在时先补 agent needs_review 水系？——补 agent normalized revision，再写 human final revision（dedup by digest）。
func (s *ProjectService) appendHumanFinalArtifactRevision(
	ctx context.Context,
	project *model.Projects,
	artifactType string,
	content, finalContent map[string]any,
	userID, status string,
) (*model.ArtifactRevisions, error) {
	rawOutput := asMap(content["raw_output"])
	scope := storeString(content["script_version_id"])
	if scope == "" {
		scope = storeString(content["episode_id"])
	}
	if scope == "" {
		scope = project.ID
	}
	artifactID := uuid5URL("cineforge:" + artifactType + ":" + project.ID + ":" + scope)

	sourceRunID := storeString(rawOutput["agent_run_id"])
	if sourceRunID == "" {
		sourceRunID = storeString(rawOutput["run_id"])
	}
	var sourceRun *model.AgentRuns
	if sourceRunID != "" {
		sourceRun, err := s.projects.GetAgentRun(ctx, sourceRunID)
		if err == nil && sourceRun != nil {
			if err := s.appendAgentOutputRevision(ctx, project, artifactType, artifactID, sourceRun, userID); err != nil {
				return nil, err
			}
		}
	}

	revisions, err := s.projects.ListArtifactRevisionsForArtifact(ctx, artifactType, artifactID)
	if err != nil {
		return nil, err
	}
	var current *model.ArtifactRevisions
	if len(revisions) > 0 {
		last := revisions[len(revisions)-1]
		current = &last
	}
	digest := repository.ContentHashString(finalContent)
	for _, item := range revisions {
		if item.SourceType == "human" && item.DataState == "final" && item.ContentHash == digest {
			if sourceRun == nil && item.SourceRunId == nil {
				return &item, nil
			}
			if sourceRun != nil && item.SourceRunId != nil && *item.SourceRunId == sourceRun.ID {
				return &item, nil
			}
		}
	}
	var parentID *string
	var parentVersion int32
	var previousHash any
	if current != nil {
		parentID = &current.ID
		parentVersion = current.VersionNo
		previousHash = current.ContentHash
	}
	var idempotencyKey string
	if sourceRun != nil {
		idempotencyKey = artifactRevisionIdempotencyKey(artifactType, artifactID, sourceRun.ID, "human", "final", digest)
	} else {
		idempotencyKey = artifactRevisionIdempotencyKey(artifactType, artifactID, "", "human", "final", digest)
	}
	changeDiff, _ := json.Marshal(map[string]any{
		"previous_content_hash": previousHash,
		"source_transition":     fmt.Sprintf("%s->final", currentDataState(current)),
	})
	inputSnapshot := map[string]any{
		"project_id":           project.ID,
		"episode_id":           content["episode_id"],
		"script_version_id":    content["script_version_id"],
		"normalized_revision_id": nil,
	}
	if current != nil {
		inputSnapshot["normalized_revision_id"] = current.ID
	}
	rev := &model.ArtifactRevisions{
		ID:                newUUIDOrErr(),
		ProjectId:         project.ID,
		ArtifactType:      artifactType,
		ArtifactId:        artifactID,
		VersionNo:         parentVersion + 1,
		ParentRevisionId:  parentID,
		SourceType:        "human",
		SkillName:         nil,
		InputSnapshot:     mustJSON(inputSnapshot),
		RawOutput:         json.RawMessage(`{}`),
		NormalizedContent: mustJSON(finalContent),
		ChangeDiff:        changeDiff,
		ContentHash:       digest,
		Status:            status,
		CreatedBy:         &userID,
		DataState:         "final",
		IDempotencyKey:    &idempotencyKey,
	}
	if sourceRun != nil {
		rev.SourceRunId = &sourceRun.ID
	}
	if err := s.projects.AppendArtifactRevision(ctx, rev); err != nil {
		return nil, err
	}
	return rev, nil
}

// appendAgentOutputRevision 对齐 legacy _append_agent_output_revision：为 source_run 补 agent normalized revision。
func (s *ProjectService) appendAgentOutputRevision(
	ctx context.Context,
	project *model.Projects,
	artifactType, artifactID string,
	sourceRun *model.AgentRuns,
	userID string,
) error {
	revisions, err := s.projects.ListArtifactRevisionsForArtifact(ctx, artifactType, artifactID)
	if err != nil {
		return err
	}
	normalizedSource := map[string]any{}
	var rawOutputBytes []byte
	if sourceRun.OutputJson != nil {
		rawOutputBytes = *sourceRun.OutputJson
	}
	if len(rawOutputBytes) > 0 {
		var output map[string]any
		if err := json.Unmarshal(rawOutputBytes, &output); err == nil {
			if bv, ok := output["breakdown_view"].(map[string]any); ok {
				normalizedSource = bv
			} else {
				normalizedSource = output
			}
		}
	}
	digest := repository.ContentHashString(normalizedSource)
	for _, item := range revisions {
		if item.SourceType == "agent" && item.SourceRunId != nil && *item.SourceRunId == sourceRun.ID && item.ContentHash == digest {
			return nil
		}
	}
	var parentID *string
	var parentVersion int32
	if len(revisions) > 0 {
		last := revisions[len(revisions)-1]
		parentID = &last.ID
		parentVersion = last.VersionNo
	}
	rev := &model.ArtifactRevisions{
		ID:                newUUIDOrErr(),
		ProjectId:         project.ID,
		ArtifactType:      artifactType,
		ArtifactId:        artifactID,
		VersionNo:         parentVersion + 1,
		ParentRevisionId:  parentID,
		SourceType:        "agent",
		SourceRunId:       &sourceRun.ID,
		SkillName:         sourceRun.SkillName,
		InputSnapshot:     json.RawMessage(`{}`),
		RawOutput:         json.RawMessage(rawOutputBytes),
		NormalizedContent: mustJSON(normalizedSource),
		ChangeDiff:        json.RawMessage(`{}`),
		ContentHash:       digest,
		Status:            "needs_review",
		CreatedBy:         nil,
		DataState:         "agent_raw",
		IDempotencyKey:    nil,
	}
	return s.projects.AppendArtifactRevision(ctx, rev)
}

func artifactRevisionIdempotencyKey(artifactType, artifactID, runID, sourceType, dataState, contentHash string) string {
	// 对齐 legacy _artifact_revision_idempotency_key：对字段字典做内容哈希，保证落在 varchar(64)。
	if runID == "" {
		runID = "" // json 里保持 key 存在
	}
	return repository.ContentHashString(map[string]any{
		"artifact_type": artifactType,
		"artifact_id":   artifactID,
		"source_run_id": orNilString(runID),
		"source_type":   sourceType,
		"data_state":    dataState,
		"content_hash":  contentHash,
	})
}

func orNilString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func currentDataState(rev *model.ArtifactRevisions) string {
	if rev == nil {
		return "none"
	}
	return rev.DataState
}

// 基础请求类型：legacy soft_delete 用 ProjectAdminActionRequest。

// requireAssetPromptDesignsFromContent 对齐 legacy _require_asset_prompt_designs_from_content：
// 有 scope 走权威 DB 视图 + 已确认库存；无 scope 退化为 content 内联检查。
func (s *ProjectService) requireAssetPromptDesignsFromContent(ctx context.Context, projectID string, content map[string]any) error {
	episodeID := storeString(content["episode_id"])
	scriptVersionID := storeString(content["script_version_id"])
	if episodeID != "" && scriptVersionID != "" {
		view := s.latestScopedView(ctx, projectID, &episodeID, &scriptVersionID)
		inventory, err := s.confirmedAssetInventoryForScope(ctx, projectID, &episodeID, &scriptVersionID, view)
		if err != nil {
			return err
		}
		missing := assetsMissingPromptDesign(inventory.Assets, view)
		if len(missing) > 0 {
			return conflict409(fmt.Sprintf("以下资产尚未经过专用 Prompt Skill：%s。请先补齐资产 Prompt。", strings.Join(missing, "、")))
		}
		return nil
	}
	missing := assetsMissingPromptDesign(mapSliceOfMapAny(content["assets"]), content)
	if len(missing) > 0 {
		return conflict409(fmt.Sprintf("以下资产尚未经过专用 Prompt Skill：%s。请先补齐资产 Prompt。", strings.Join(missing, "、")))
	}
	return nil
}

func hasLockableOutputs(content map[string]any) bool {
	return len(mapSliceOfMapAny(content["storyboards"])) > 0 || len(mapSliceOfMapAny(content["assets"])) > 0
}

// validateBreakdownViewForLock 对齐 legacy validate_breakdown_view blocking_errors。
// P3d 覆盖核心硬校验：脚本段/分镜存在、描述/时长/景别/段落引用、镜像分镜、资产名称。
func validateBreakdownViewForLock(content map[string]any) []string {
	var errors []string
	segments := mapSliceOfMapAny(content["script_segments"])
	storyboards := mapSliceOfMapAny(content["storyboards"])
	assets := mapSliceOfMapAny(content["assets"])
	if len(segments) == 0 {
		errors = append(errors, "缺少已确认脚本段")
	}
	if len(storyboards) == 0 {
		errors = append(errors, "缺少分镜清单")
	}
	for _, sb := range storyboards {
		order := safeInt32Of(sb["order_num"], safeInt32Of(sb["order_no"], int32(0)))
		label := fmt.Sprintf("分镜 #%d", order)
		if strings.TrimSpace(viewString(sb, "description")) == "" {
			errors = append(errors, label+" 缺少描述")
		}
		duration := safeInt32Of(sb["duration_seconds"], int32(0))
		if duration < 10 || duration > 15 {
			errors = append(errors, fmt.Sprintf("%s 时长 %d 秒，需在 10-15 秒之间", label, duration))
		}
		if strings.TrimSpace(viewString(sb, "shot_type")) == "" {
			errors = append(errors, label+" 缺少景别")
		}
		if strings.TrimSpace(viewString(sb, "script_segment_code")) == "" {
			errors = append(errors, label+" 缺少脚本段引用")
		}
	}
	for _, asset := range assets {
		if strings.TrimSpace(viewString(asset, "name")) == "" {
			errors = append(errors, "存在缺少名称的资产")
			break
		}
	}
	return errors
}

// safeInt32Of 对齐 repository.safeInt32：安全数值转换，缺失回退 def。
func safeInt32Of(v any, def int32) int32 {
	switch typed := v.(type) {
	case nil:
		return def
	case int:
		return int32(typed)
	case int32:
		return typed
	case int64:
		return int32(typed)
	case float64:
		return int32(typed)
	case json.Number:
		n, err := typed.Int64()
		if err != nil {
			return def
		}
		return int32(n)
	case string:
		var n int64
		if _, err := fmt.Sscanf(typed, "%d", &n); err == nil {
			return int32(n)
		}
	}
	return def
}

func filterMapSlice(v any, keep func(map[string]any) bool) []map[string]any {
	out := make([]map[string]any, 0)
	for _, item := range mapSliceOfMapAny(v) {
		if keep(item) {
			out = append(out, item)
		}
	}
	return out
}

func takeStr(in []string, n int) []string {
	if len(in) > n {
		return in[:n]
	}
	return in
}

func storyboardEpisodeCodeOf(item map[string]any) string {
	code := repository.NormalizeEpisodeCode(viewString(item, "episode_code"))
	if code != "" {
		return code
	}
	return fmt.Sprintf("EP%02d", safeInt32Of(item["episode_num"], 1))
}

// assetBelongsToEpisodeOf 对齐 legacy _asset_belongs_to_episode。
func assetBelongsToEpisodeOf(item map[string]any, episodeCode string) bool {
	for _, key := range []string{"episode_code", "corresponding_episode", "episode"} {
		if repository.NormalizeEpisodeCode(viewString(item, key)) == repository.NormalizeEpisodeCode(episodeCode) {
			return true
		}
	}
	related := item["related_storyboard_codes"]
	if related == nil {
		related = item["storyboard_codes"]
	}
	if related == nil {
		related = item["corresponding_storyboards"]
	}
	switch typed := related.(type) {
	case string:
		if typed != "" && strings.Contains(strings.ToUpper(typed), strings.ToUpper(repository.NormalizeEpisodeCode(episodeCode))) {
			return true
		}
	case []any:
		for _, value := range typed {
			if strings.Contains(strings.ToUpper(fmt.Sprintf("%v", value)), strings.ToUpper(repository.NormalizeEpisodeCode(episodeCode))) {
				return true
			}
		}
	}
	for _, key := range []string{"episode_code", "corresponding_episode", "episode"} {
		if viewString(item, key) != "" {
			return false
		}
	}
	return true
}

func strPtrOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
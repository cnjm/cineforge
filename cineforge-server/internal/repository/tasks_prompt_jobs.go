package repository

// P4e-6 task prompt-jobs 持久化（对齐 legacy repositories.py 的
// production_references_for_task / task_prompt_context_fingerprint /
// build_task_prompt_context_fingerprint / get_project_script_execution_context，
// 外加 prompt-jobs 需要的资产全行查询与拆解 manual_review_items 抽取）：
//
//	ProductionReferencesForTask    → 参考任务集的已审核主母版引用
//	TaskPromptContextFingerprint   → 提示词上下文指纹（先查主母版资产快照再解析简报）
//	BuildTaskPromptContextFingerprint → 纯函数排序/哈希（service 可复用）
//	GetProjectScriptExecutionContext → 分集剧本执行上下文（KeyError/PermissionError 沿用）
//	ListProjectAssetsFull          → 项目全量资产（AssetRead 全字段）
//	BreakdownAgentReminders        → 最新匹配作用域拆解视图的 manual_review_items

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"cineforge/server/internal/model"
	"cineforge/server/internal/productionbrief"
)

// assetFullColumns 资产全行（对齐 legacy Asset 实体）。
const assetFullColumns = `id, project_id, asset_code, asset_type, name, description, status, tags,
	preview_path, file_path, prompt_text, base_model, metadata_json, version,
	current_version_id, current_revision_id, is_locked, locked_by, locked_at,
	created_by_id, created_at, updated_at`

func scanAssetFull(row pgxRow) (*model.Assets, error) {
	var a model.Assets
	err := row.Scan(&a.ID, &a.ProjectId, &a.AssetCode, &a.AssetType, &a.Name, &a.Description,
		&a.Status, &a.Tags, &a.PreviewPath, &a.FilePath, &a.PromptText, &a.BaseModel,
		&a.MetadataJson, &a.Version, &a.CurrentVersionId, &a.CurrentRevisionId,
		&a.IsLocked, &a.LockedBy, &a.LockedAt, &a.CreatedById, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListProjectAssetsFull 对齐 legacy list_assets(project_id)：项目活跃资产，全字段。
// 与 listAssetsByProject 的窄列查询不同，这里覆盖 AssetRead 序列化所需全部字段。
func (r *Tasks) ListProjectAssetsFull(ctx context.Context, q queryer, projectID string) ([]*model.Assets, error) {
	rows, err := q.Query(ctx,
		`SELECT `+assetFullColumns+` FROM assets
		 WHERE project_id = $1 AND status NOT IN ('deleted','excluded')
		 ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project assets full: %w", err)
	}
	defer rows.Close()
	var out []*model.Assets
	for rows.Next() {
		a, err := scanAssetFull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- production_references_for_task ----

// ProductionReferencesForTask 对齐 legacy production_references_for_task：
// 参数(依赖∪同资产 variant A∪场景资产已完成任务)中每条已审定的主/备用母版。
// 引用顺序保持 DB 行序（legacy dict 插入序），按 submission_id 去重。
func (r *Tasks) ProductionReferencesForTask(ctx context.Context, q queryer, taskID string) ([]map[string]any, error) {
	task, err := r.GetActiveAssetTask(ctx, q, taskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, notFoundError(taskID)
	}
	_, dependencyIDs, _, err := r.TaskDependencyState(ctx, q, task)
	if err != nil {
		return nil, err
	}
	referenceTaskIDs := strListDedup(dependencyIDs)
	if task.TaskType == "asset" && task.AssetId != nil &&
		map[string]bool{"B": true, "C": true, "D": true, "E": true}[nonNil(task.TaskVariant)] {
		var siblingA *string
		err := q.QueryRow(ctx,
			`SELECT id FROM tasks
			 WHERE project_id = $1 AND asset_id = $2 AND task_type = 'asset'
			   AND is_retired = false
			   AND COALESCE(age_stage_code,'') = $3 AND COALESCE(costume_variant_code,'') = $4
			   AND task_variant = 'A'`,
			task.ProjectId, *task.AssetId, nonNil(task.AgeStageCode), nonNil(task.CostumeVariantCode)).
			Scan(&siblingA)
		if err != nil && !errorsIsNoRows(err) {
			return nil, fmt.Errorf("resolve sibling A task: %w", err)
		}
		if siblingA != nil {
			referenceTaskIDs = appendStrDedup(referenceTaskIDs, *siblingA)
		}
	}
	if task.StoryboardId != nil && (nonNil(task.SceneCode) != "" || nonNil(task.SceneName) != "") {
		sceneDeps, err := r.ComputeSceneAssetDependencies(ctx, q, task.ProjectId)
		if err != nil {
			return nil, err
		}
		required := map[string]bool{}
		for sceneCode, info := range sceneDeps {
			if (nonNil(task.SceneCode) != "" && sceneCode == *task.SceneCode) ||
				(nonNil(task.SceneName) != "" && info.SceneName != nil && *info.SceneName == *task.SceneName) {
				for _, c := range info.RequiredAssetCodes {
					if c != "" {
						required[c] = true
					}
				}
			}
		}
		if len(required) > 0 {
			codes := make([]string, 0, len(required))
			for c := range required {
				codes = append(codes, c)
			}
			sort.Strings(codes)
			rows, err := q.Query(ctx,
				`SELECT t.id
				 FROM tasks t JOIN assets a ON a.id = t.asset_id
				 WHERE t.project_id = $1
				   AND t.task_type IN ('asset','text_to_image')
				   AND t.status = 'completed' AND t.is_retired = false
				   AND a.asset_code = ANY($2)`, task.ProjectId, codes)
			if err != nil {
				return nil, fmt.Errorf("scene dependency tasks: %w", err)
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return nil, err
				}
				referenceTaskIDs = appendStrDedup(referenceTaskIDs, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
	}
	if len(referenceTaskIDs) == 0 {
		return []map[string]any{}, nil
	}
	rows, err := q.Query(ctx,
		`SELECT s.id, t.id, a.asset_code, t.age_stage_code, t.costume_variant_code, t.task_variant,
		        s.step, s.file_path, av.id, av.file_id
		 FROM submissions s
		 JOIN tasks t ON t.id = s.task_id
		 LEFT JOIN assets a ON a.id = t.asset_id
		 JOIN asset_versions av ON av.source_submission_id = s.id
		 WHERE s.task_id = ANY($1)
		   AND t.is_retired = false
		   AND s.status = ANY($2)
		   AND s.is_selected = true AND s.is_archived = false AND s.is_invalidated = false
		   AND s.file_id IS NOT NULL
		   AND av.is_invalidated = false AND av.purged_at IS NULL
		   AND av.asset_id = t.asset_id
		   AND av.file_id = s.file_id
		 ORDER BY a.asset_code, t.age_stage_code, t.costume_variant_code, t.task_variant, av.version_no`,
		referenceTaskIDs, approvedSubmissionStatusSlice())
	if err != nil {
		return nil, fmt.Errorf("production references rows: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	out := []map[string]any{}
	for rows.Next() {
		var submissionID, sourceTaskID string
		var assetCode, ageStage, costume, variant, step, filePath, versionID, fileID *string
		if err := rows.Scan(&submissionID, &sourceTaskID, &assetCode, &ageStage, &costume,
			&variant, &step, &filePath, &versionID, &fileID); err != nil {
			return nil, err
		}
		if seen[submissionID] {
			continue
		}
		seen[submissionID] = true
		out = append(out, map[string]any{
			"task_id":              sourceTaskID,
			"asset_code":           strOrNil(assetCode),
			"age_stage_code":       strOrNil(ageStage),
			"costume_variant_code": strOrNil(costume),
			"task_variant":         strOrNil(variant),
			"step":                 strOrNil(step),
			"submission_id":        submissionID,
			"asset_version_id":     strOrNil(versionID),
			"file_id":              strOrNil(fileID),
			"file_path":            strOrNil(filePath),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate production references: %w", err)
	}
	return out, nil
}

// approvedSubmissionStatusSlice 对齐 APPROVED_SUBMISSION_STATUSES 的值列表（ANY 查询）。
func approvedSubmissionStatusSlice() []string {
	out := make([]string, 0, len(approvedSubmissionStatuses))
	for status := range approvedSubmissionStatuses {
		out = append(out, status)
	}
	return out
}

// ---- task_prompt_context_fingerprint ----

// TaskPromptContextFingerprint 对齐 legacy task_prompt_context_fingerprint。
// references 传 nil 时内部解析；resolved_production_brief 构建失败（TypeError/ValueError
// 路径，如不完整的项目/分集简报）静默回退 None——指纹保持可读，Agent 执行仍会拒缺。
func (r *Tasks) TaskPromptContextFingerprint(ctx context.Context, q queryer,
	taskID string, step *string, references []map[string]any) (string, error) {
	task, err := r.GetActiveAssetTask(ctx, q, taskID)
	if err != nil {
		return "", err
	}
	if task == nil {
		return "", notFoundError(taskID)
	}
	if references == nil {
		references, err = r.ProductionReferencesForTask(ctx, q, taskID)
		if err != nil {
			return "", err
		}
	}
	rows, err := q.Query(ctx,
		`SELECT id, step FROM submissions
		 WHERE task_id = $1 AND status = 'primary_master' AND is_primary = true
		   AND is_archived = false AND is_invalidated = false
		 ORDER BY step, id`, taskID)
	if err != nil {
		return "", fmt.Errorf("primary submissions: %w", err)
	}
	primarySubmissions := []map[string]any{}
	for rows.Next() {
		var id string
		var stepValue *string
		if err := rows.Scan(&id, &stepValue); err != nil {
			rows.Close()
			return "", err
		}
		primarySubmissions = append(primarySubmissions, map[string]any{
			"submission_id": id,
			"step":          strOrNil(stepValue),
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate primary submissions: %w", err)
	}
	var asset *model.Assets
	if task.AssetId != nil {
		asset, err = scanAssetFull(q.QueryRow(ctx,
			`SELECT `+assetFullColumns+` FROM assets WHERE id = $1`, *task.AssetId))
		if err != nil && !errorsIsNoRows(err) {
			return "", fmt.Errorf("asset snapshot: %w", err)
		}
	}
	var assetSnapshot map[string]any
	if asset != nil {
		assetSnapshot = map[string]any{
			"id":                  asset.ID,
			"version":             asset.Version,
			"current_revision_id": strOrNil(asset.CurrentRevisionId),
			"metadata":            rawMapOrEmpty(asset.MetadataJson),
		}
	}
	var resolved map[string]any
	if task.EpisodeId != nil {
		var projectTitle string
		var projectPrefix *string
		var projectBrief json.RawMessage
		err := q.QueryRow(ctx,
			`SELECT title, project_prefix, production_brief FROM projects WHERE id = $1`, task.ProjectId).
			Scan(&projectTitle, &projectPrefix, &projectBrief)
		if err != nil && !errorsIsNoRows(err) {
			return "", fmt.Errorf("project brief: %w", err)
		}
		if err == nil && len(projectBrief) > 0 {
			var epNo int32
			var epCode string
			var epBrief json.RawMessage
			err := q.QueryRow(ctx,
				`SELECT episode_no, episode_code, COALESCE(production_brief, '{}') FROM project_episodes WHERE id = $1`,
				*task.EpisodeId).Scan(&epNo, &epCode, &epBrief)
			if err == nil && len(epBrief) > 0 {
				prefix := strOrEmpty(projectPrefix)
				if prefix == "" {
					prefix = "RF"
				}
				resolved, err = productionbrief.BuildResolvedProductionBrief(productionbrief.ResolveInput{
					ProjectTitle:    projectTitle,
					ProjectPrefix:   prefix,
					ProductionBrief: projectBrief,
					ScriptContext: map[string]any{
						"episode_no":               epNo,
						"episode_code":             epCode,
						"episode_production_brief": rawMapOrEmpty(epBrief),
					},
				})
				if err != nil {
					// Task fingerprinting remains readable while an incomplete
					// project setup is being repaired（注释同 legacy）。
					resolved = nil
				}
			}
		}
	}
	return BuildTaskPromptContextFingerprint(FingerprintInput{
		TaskID:                  task.ID,
		Step:                    step,
		ProductionReferences:    references,
		PrimarySubmissions:      primarySubmissions,
		AssetSnapshot:           assetSnapshot,
		ResolvedProductionBrief: resolved,
		PromptRevisionID:        task.PromptRevisionId,
		LatestPromptText:        task.LatestPromptText,
	}), nil
}

// FingerprintInput build_task_prompt_context_fingerprint 入参。
type FingerprintInput struct {
	TaskID                  string
	Step                    *string
	ProductionReferences    []map[string]any
	PrimarySubmissions      []map[string]any
	AssetSnapshot           map[string]any
	ResolvedProductionBrief map[string]any
	PromptRevisionID        *string
	LatestPromptText        *string
}

// BuildTaskPromptContextFingerprint 对齐 legacy build_task_prompt_context_fingerprint：
// references/masters 先按各自 key 排序，再对整份 dict 做 ContentHashString。
func BuildTaskPromptContextFingerprint(in FingerprintInput) string {
	references := cloneMaps(in.ProductionReferences)
	sort.Slice(references, func(i, j int) bool {
		return referenceLess(references[i], references[j])
	})
	masters := cloneMaps(in.PrimarySubmissions)
	sort.Slice(masters, func(i, j int) bool {
		return masterLess(masters[i], masters[j])
	})
	var stepValue any
	if in.Step != nil {
		stepValue = strings.ToLower(strings.TrimSpace(*in.Step))
		if s, _ := stepValue.(string); s == "" {
			stepValue = nil
		}
	}
	var revisionValue any
	if in.PromptRevisionID != nil {
		revisionValue = *in.PromptRevisionID
	}
	var snapshot map[string]any
	if in.AssetSnapshot != nil {
		snapshot = in.AssetSnapshot
	} else {
		snapshot = map[string]any{}
	}
	var brief map[string]any
	if in.ResolvedProductionBrief != nil {
		brief = in.ResolvedProductionBrief
	} else {
		brief = map[string]any{}
	}
	return ContentHashString(map[string]any{
		"task_id":                   in.TaskID,
		"step":                      stepValue,
		"production_references":     references,
		"primary_submissions":       masters,
		"asset_snapshot":            snapshot,
		"resolved_production_brief": brief,
		"prompt_revision_id":        revisionValue,
		"latest_prompt_text":        in.LatestPromptText,
	})
}

func cloneMaps(in []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, item := range in {
		cp := make(map[string]any, len(item))
		for k, v := range item {
			cp[k] = v
		}
		out = append(out, cp)
	}
	return out
}

// referenceLess 对齐 (str(task_id or ”), str(step or ”), str(submission_id or ”)) 字典序。
func referenceLess(a, b map[string]any) bool {
	at, bt := strKey(a, "task_id"), strKey(b, "task_id")
	if at != bt {
		return at < bt
	}
	as, bs := strKey(a, "step"), strKey(b, "step")
	if as != bs {
		return as < bs
	}
	return strKey(a, "submission_id") < strKey(b, "submission_id")
}

// masterLess 对齐 (str(step or ”), str(submission_id or ”)) 字典序。
func masterLess(a, b map[string]any) bool {
	as, bs := strKey(a, "step"), strKey(b, "step")
	if as != bs {
		return as < bs
	}
	return strKey(a, "submission_id") < strKey(b, "submission_id")
}

// strKey 对齐 str(item.get(key) or "")。
func strKey(m map[string]any, key string) string {
	v := m[key]
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// ---- get_project_script_execution_context ----

// ScriptByID 按主键查询剧本；不存在返回 (nil, nil)。
func (r *Projects) ScriptByID(ctx context.Context, id string) (*model.Scripts, error) {
	s, err := scanScript(r.pool.QueryRow(ctx,
		`SELECT `+scriptColumns+` FROM scripts WHERE id = $1`, id))
	if errorsIsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load script: %w", err)
	}
	return s, nil
}

// GetProjectScriptExecutionContext 对齐 legacy get_project_script_execution_context。
// 缺失分支返回 KeyError（arg 为触发键），服务层按 legacy 内层 try 映射 400。
// 返回 (nil, nil) 表示该任务无可解析作用域（无 episode 且无版本）。
func (r *Projects) GetProjectScriptExecutionContext(ctx context.Context,
	projectID, userID, role string, episodeID, scriptVersionID *string) (map[string]any, error) {
	if !isDirectorOrAdminRole(role) {
		access, err := r.ProjectReadAccess(ctx, userID, projectID)
		if err != nil {
			return nil, err
		}
		if !access {
			return nil, forbiddenError(projectID)
		}
	}
	var version *model.ScriptVersions
	var script *model.Scripts
	var err error
	if scriptVersionID != nil {
		version, err = r.FindScriptVersionByID(ctx, *scriptVersionID)
		if err != nil {
			return nil, err
		}
	}
	if version != nil {
		script, err = r.ScriptByID(ctx, version.ScriptId)
		if err != nil {
			return nil, err
		}
	}
	if script == nil && episodeID != nil {
		script, err = r.FindScriptForEpisode(ctx, projectID, *episodeID)
		if err != nil {
			return nil, err
		}
		if script != nil && script.CurrentVersionId != nil {
			version, err = r.FindScriptVersionByID(ctx, *script.CurrentVersionId)
			if err != nil {
				return nil, err
			}
		} else {
			version = nil
		}
	}
	if script == nil && episodeID == nil && scriptVersionID == nil {
		return nil, nil
	}
	if script == nil || script.ProjectId != projectID || version == nil || version.ScriptId != script.ID {
		return nil, notFoundError(firstNonEmptyStr(scriptVersionID, episodeID, &projectID))
	}
	var episode *model.ProjectEpisodes
	if script.EpisodeId != nil {
		episode, err = r.FindEpisodeByID(ctx, *script.EpisodeId)
		if err != nil {
			return nil, err
		}
	}
	if episode == nil || episode.ProjectId != projectID {
		return nil, notFoundError(firstNonEmptyStr(script.EpisodeId, nil, &projectID))
	}
	project, err := r.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFoundError(projectID)
	}
	return map[string]any{
		"episode_id":               episode.ID,
		"episode_no":               episode.EpisodeNo,
		"episode_code":             episode.EpisodeCode,
		"script_id":                script.ID,
		"script_code":              script.ScriptCode,
		"script_version_id":        version.ID,
		"script_version_no":        version.VersionNo,
		"script_text":              version.Content,
		"content_hash":             strOrNil(version.ContentHash),
		"source_file_id":           strOrNil(version.SourceFileId),
		"original_filename":        strOrNil(version.OriginalFilename),
		"parser_name":              strOrNil(version.ParserName),
		"language":                 strOrNil(version.Language),
		"episode_production_brief": rawMapOrEmpty(emptyRawIfNil(episode.ProductionBrief)),
	}, nil
}

// MissingScriptContextKey 抽取 GetProjectScriptExecutionContext 的 KeyError 参数；
// 非 ok 返回时错误不是"上下文缺失"。
func MissingScriptContextKey(err error) (string, bool) {
	var ke *KeyError
	if errors.As(err, &ke) {
		return fmt.Sprintf("%v", ke.Arg), true
	}
	return "", false
}

// ProjectReadAccess 对齐 legacy _can_access_project（含可见任务回退）。
func (r *Projects) ProjectReadAccess(ctx context.Context, userID, projectID string) (bool, error) {
	has, err := r.HasProjectPermission(ctx, userID, projectID)
	if err != nil {
		return false, err
	}
	if has {
		return true, nil
	}
	return r.HasVisibleTaskInProject(ctx, userID, projectID)
}

// ---- BreakdownAgentReminders ----

// BreakdownAgentReminders 对齐 latest_breakdown_view 的 manual_review_items 用途：
// 取最新匹配作用域（episode 过滤，同 breakdownContentMatchesLineage）的拆解视图，
// 返回其 manual_review_items 数组（legacy `breakdown_view.get("manual_review_items")`）。
func (r *Tasks) BreakdownAgentReminders(ctx context.Context, q queryer, projectID string, episodeID *string) []any {
	rows, err := q.Query(ctx,
		`SELECT content_json FROM script_breakdowns WHERE project_id = $1
		 ORDER BY version DESC, created_at DESC LIMIT 100`, projectID)
	if err != nil {
		return []any{}
	}
	defer rows.Close()
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return []any{}
		}
		if !breakdownContentMatchesLineage(raw, episodeID, nil) {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return []any{}
		}
		switch items := m["manual_review_items"].(type) {
		case []any:
			return items
		case []map[string]any:
			out := make([]any, 0, len(items))
			for _, item := range items {
				out = append(out, item)
			}
			return out
		default:
			return []any{}
		}
	}
	if err := rows.Err(); err != nil {
		return []any{}
	}
	return []any{}
}

// ---- 纯值工具 ----

// strOrNil *string → any（nil → JSON null，legacy None 保持 null 不被省略）。
func strOrNil(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func strOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// firstNonEmptyStr 取首个非 nil 非空字符串（legacy `a or b or c` 语义）。
func firstNonEmptyStr(parts ...*string) string {
	for _, p := range parts {
		if p != nil && *p != "" {
			return *p
		}
	}
	return ""
}

func emptyRawIfNil(v *json.RawMessage) json.RawMessage {
	if v == nil {
		return json.RawMessage{}
	}
	return *v
}

func rawMapOrEmpty(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// pgxRow 缓存少量“可 Scan（无资格审查）”的行类型（QueryRow / Query 的行）。
type pgxRow = interface {
	Scan(dest ...any) error
}

package repository

// P3e task 依赖状态与依赖契约（对齐 legacy repositories.py：_task_dependency_state /
// _batch_task_dependency_states / _dependency_contract_for_task / _batch_dependency_contracts /
// _valid_formal_version_maps / _dependency_asset_contract_entry / _dependency_download_name /
// compute_scene_asset_dependencies / _scene_gating_core / _scene_is_locked /
// _batch_scene_lock_states）。读路径均为打包查询，列表与详情共享同一解析器，锁定徽标与说明不漂移。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// depToken 对齐 _dep_token：str(value or '').strip().lower()。
func depToken(value any) string {
	if value == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", value)))
}

// SafeInt 对齐 _safe_int。
func SafeInt(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	}
	return fallback
}

func errorsIsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// joinNonEmpty 对齐 Python 的 "-".join(part for part in (... ) if part)。
func joinNonEmpty(parts ...string) string {
	out := []string{}
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "-")
}

// ---- _task_dependency_state（单任务详情 / 变更路径） ----

// TaskDependencyState 对齐 _task_dependency_state；返回 (locked, activeDependencyIDs, assetCodes)。
func (r *Tasks) TaskDependencyState(ctx context.Context, q queryer, t *model.Tasks) (bool, []string, []string, error) {
	dependencyIDs := []string{}
	if t.DependsOnTaskId != nil && *t.DependsOnTaskId != "" {
		dependencyIDs = append(dependencyIDs, *t.DependsOnTaskId)
	}
	if t.AssetId != nil && (t.TaskType == "asset" || t.TaskType == "text_to_image") &&
		map[string]bool{"B": true, "C": true, "D": true, "E": true}[nonNil(t.TaskVariant)] {
		var primaryID *string
		err := q.QueryRow(ctx,
			`SELECT id FROM tasks
			 WHERE project_id = $1 AND asset_id = $2
			   AND task_type IN ('asset','text_to_image') AND is_retired = false
			   AND COALESCE(age_stage_code,'') = $3 AND COALESCE(costume_variant_code,'') = $4
			   AND task_variant = 'A'`,
			t.ProjectId, *t.AssetId, nonNil(t.AgeStageCode), nonNil(t.CostumeVariantCode)).Scan(&primaryID)
		if errorsIsNoRows(err) {
			// 无 primary A 任务
		} else if err != nil {
			return false, nil, nil, fmt.Errorf("resolve primary A task: %w", err)
		} else if primaryID != nil {
			dependencyIDs = appendStrDedup(dependencyIDs, *primaryID)
		}
	}
	rows, err := r.queryDependencyRows(ctx, q, t.ID)
	if err != nil {
		return false, nil, nil, err
	}
	for _, id := range rows {
		dependencyIDs = appendStrDedup(dependencyIDs, id)
	}
	dependencyIDs = strListDedup(dependencyIDs)
	if len(dependencyIDs) == 0 {
		return false, []string{}, []string{}, nil
	}
	statusByID, codeByID, err := r.activeDependencyMap(ctx, q, dependencyIDs)
	if err != nil {
		return false, nil, nil, err
	}
	locked := false
	activeIDs := []string{}
	assetCodes := []string{}
	for _, id := range dependencyIDs {
		status, ok := statusByID[id]
		if !ok {
			continue // 缺失/停用依赖不锁定
		}
		activeIDs = append(activeIDs, id)
		if code := codeByID[id]; code != "" {
			assetCodes = appendStrDedup(assetCodes, code)
		}
		if status != "completed" {
			locked = true
		}
	}
	return locked, activeIDs, assetCodes, nil
}

func (r *Tasks) queryDependencyRows(ctx context.Context, q queryer, taskID string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT depends_on_task_id FROM task_dependencies WHERE task_id = $1`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query task dependencies: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// activeDependencyMap 返回依赖任务 id → status / asset_code（仅保留活跃任务）。
func (r *Tasks) activeDependencyMap(ctx context.Context, q queryer, ids []string) (map[string]string, map[string]string, error) {
	statusByID := map[string]string{}
	codeByID := map[string]string{}
	if len(ids) == 0 {
		return statusByID, codeByID, nil
	}
	rows, err := q.Query(ctx,
		`SELECT t.id, t.status, a.asset_code
		 FROM tasks t LEFT JOIN assets a ON a.id = t.asset_id
		 WHERE t.id = ANY($1)
		   AND t.is_retired = false
		   AND (t.asset_id IS NULL OR t.asset_id IN (
		     SELECT id FROM assets WHERE status NOT IN ('deleted','excluded')))`, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("active dependency map: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var code *string
		if err := rows.Scan(&id, &status, &code); err != nil {
			return nil, nil, err
		}
		statusByID[id] = status
		if code != nil {
			codeByID[id] = *code
		}
	}
	return statusByID, codeByID, rows.Err()
}

// ---- _batch_task_dependency_states（列表路径，有界往返） ----

// BatchTaskDependencyStates 返回 task id → (locked, dependencyIDs, assetCodes)。
func (r *Tasks) BatchTaskDependencyStates(ctx context.Context, q queryer, tasks []*model.Tasks) (map[string]taskDepState, error) {
	out := map[string]taskDepState{}
	if len(tasks) == 0 {
		return out, nil
	}
	dependencyIDsByTask := map[string][]string{}
	for _, t := range tasks {
		if t.DependsOnTaskId != nil && *t.DependsOnTaskId != "" {
			dependencyIDsByTask[t.ID] = []string{*t.DependsOnTaskId}
		} else {
			dependencyIDsByTask[t.ID] = []string{}
		}
	}
	variantTasks := []*model.Tasks{}
	for _, t := range tasks {
		if t.AssetId != nil && (t.TaskType == "asset" || t.TaskType == "text_to_image") &&
			map[string]bool{"B": true, "C": true, "D": true, "E": true}[nonNil(t.TaskVariant)] {
			variantTasks = append(variantTasks, t)
		}
	}
	if len(variantTasks) > 0 {
		// 对齐 legacy：批量主查询不带 is_retired=false（与单任务路径的差异按原样保留，
		// 最终仍会被 activeDependencyMap 的活跃条件过滤掉停用任务）。
		projectIDs := []string{}
		assetIDs := []string{}
		for _, t := range variantTasks {
			projectIDs = appendStrDedup(projectIDs, t.ProjectId)
			if t.AssetId != nil {
				assetIDs = appendStrDedup(assetIDs, *t.AssetId)
			}
		}
		type primaryKey struct {
			project, asset, age, costume string
		}
		primaryByKey := map[primaryKey]string{}
		rows, err := q.Query(ctx,
			`SELECT id, project_id, asset_id, COALESCE(age_stage_code,''), COALESCE(costume_variant_code,'')
			 FROM tasks
			 WHERE project_id = ANY($1) AND asset_id = ANY($2)
			   AND task_type IN ('asset','text_to_image') AND task_variant = 'A'`,
			projectIDs, assetIDs)
		if err != nil {
			return nil, fmt.Errorf("batch primary A tasks: %w", err)
		}
		for rows.Next() {
			var id string
			var pid, aid, age, costume string
			if err := rows.Scan(&id, &pid, &aid, &age, &costume); err != nil {
				rows.Close()
				return nil, err
			}
			primaryByKey[primaryKey{project: pid, asset: aid, age: age, costume: costume}] = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		for _, t := range variantTasks {
			key := primaryKey{project: t.ProjectId, asset: nonNil(t.AssetId),
				age: nonNil(t.AgeStageCode), costume: nonNil(t.CostumeVariantCode)}
			if id, ok := primaryByKey[key]; ok {
				dependencyIDsByTask[t.ID] = appendStrDedup(dependencyIDsByTask[t.ID], id)
			}
		}
	}
	taskIDs := []string{}
	for _, t := range tasks {
		taskIDs = append(taskIDs, t.ID)
	}
	// 显式依赖表
	rows, err := q.Query(ctx,
		`SELECT task_id, depends_on_task_id FROM task_dependencies WHERE task_id = ANY($1)`, taskIDs)
	if err != nil {
		return nil, fmt.Errorf("batch explicit dependencies: %w", err)
	}
	for rows.Next() {
		var tid, dependsOn string
		if err := rows.Scan(&tid, &dependsOn); err != nil {
			rows.Close()
			return nil, err
		}
		dependencyIDsByTask[tid] = appendStrDedup(dependencyIDsByTask[tid], dependsOn)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ordered := map[string][]string{}
	allDepIDs := []string{}
	for tid, ids := range dependencyIDsByTask {
		keep := strListDedup(ids)
		ordered[tid] = keep
		for _, id := range keep {
			allDepIDs = appendStrDedup(allDepIDs, id)
		}
	}
	if len(allDepIDs) == 0 {
		for _, t := range tasks {
			out[t.ID] = taskDepState{}
		}
		return out, nil
	}
	statusByID, codeByID, err := r.activeDependencyMap(ctx, q, allDepIDs)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		dependencyIDs := []string{}
		for _, id := range ordered[t.ID] {
			if _, ok := statusByID[id]; ok {
				dependencyIDs = append(dependencyIDs, id)
			}
		}
		assetCodes := []string{}
		for _, id := range dependencyIDs {
			if code := codeByID[id]; code != "" {
				assetCodes = appendStrDedup(assetCodes, code)
			}
		}
		locked := false
		for _, id := range dependencyIDs {
			if statusByID[id] != "completed" {
				locked = true
				break
			}
		}
		out[t.ID] = taskDepState{
			Locked:     locked,
			IDs:        dependencyIDs,
			AssetCodes: assetCodes,
		}
	}
	return out, nil
}

type taskDepState struct {
	Locked     bool
	IDs        []string
	AssetCodes []string
}

// strListDedup 保留首现顺序去重且去空。
func strListDedup(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func appendStrDedup(in []string, v string) []string {
	for _, item := range in {
		if item == v {
			return in
		}
	}
	return append(in, v)
}

// ---- compute_scene_asset_dependencies ----

// SceneAssetDep 单场景依赖信息。
type SceneAssetDep struct {
	SceneName          *string
	RequiredAssetCodes []string
}

var depLabelSuffixRe = regexp.MustCompile(`-([^-]+)$`)

// ComputeSceneAssetDependencies 对齐 compute_scene_asset_dependencies。人物/道具按 token（首win优先）索引。
func (r *Tasks) ComputeSceneAssetDependencies(ctx context.Context, q queryer, projectID string) (map[string]SceneAssetDep, error) {
	assets, err := r.listAssetsByProject(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	storyboards, err := r.listProjectStoryboards(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	charPropByToken := map[string]string{}
	sceneAssets := []*model.Assets{}
	for _, asset := range assets {
		if asset.AssetType == "scene" {
			sceneAssets = append(sceneAssets, asset)
			continue
		}
		if asset.AssetType != "character" && asset.AssetType != "prop" {
			continue
		}
		code := assetCodeOr(asset.AssetCode, asset.Name)
		if code == "" {
			continue
		}
		if asset.AssetCode != nil && *asset.AssetCode != "" {
			charPropByToken[depToken(*asset.AssetCode)] = *asset.AssetCode
			if m := depLabelSuffixRe.FindString(*asset.AssetCode); m != "" {
				charPropByToken[depToken(m[1:])] = *asset.AssetCode
			}
		}
		if asset.Name != "" {
			charPropByToken[depToken(asset.Name)] = code
		}
	}
	result := map[string]SceneAssetDep{}
	for _, scene := range sceneAssets {
		sceneCode := assetCodeOr(scene.AssetCode, scene.Name)
		if sceneCode == "" {
			continue
		}
		sceneTokens := map[string]bool{}
		if v := depToken(scene.AssetCode); v != "" {
			sceneTokens[v] = true
		}
		if v := depToken(scene.Name); v != "" {
			sceneTokens[v] = true
		}
		required := map[string]bool{}
		if scene.AssetCode != nil && *scene.AssetCode != "" {
			required[*scene.AssetCode] = true
		}
		meta := map[string]any{}
		_ = json.Unmarshal(scene.MetadataJson, &meta)
		var breakdownMeta map[string]any
		if bm, ok := meta["breakdown_asset"].(map[string]any); ok {
			breakdownMeta = bm
		}
		for _, source := range []map[string]any{meta, breakdownMeta} {
			for _, key := range []string{"key_prop_ids", "key_props"} {
				values := anyToStringList(source[key])
				for _, value := range values {
					if code := charPropByToken[depToken(value)]; code != "" {
						required[code] = true
					}
				}
			}
		}
		for _, sb := range storyboards {
			sbTokens := map[string]bool{}
			if v := depToken(sb.SceneCode); v != "" {
				sbTokens[v] = true
			}
			if v := depToken(sb.SceneName); v != "" {
				sbTokens[v] = true
			}
			matched := false
			for tok := range sbTokens {
				if sceneTokens[tok] {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			var chars []string
			_ = json.Unmarshal(sb.Characters, &chars)
			for _, token := range chars {
				if code := charPropByToken[depToken(token)]; code != "" {
					required[code] = true
				}
			}
		}
		codes := make([]string, 0, len(required))
		for c := range required {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		var sceneName *string
		if scene.Name != "" {
			sceneName = &scene.Name
		}
		result[sceneCode] = SceneAssetDep{SceneName: sceneName, RequiredAssetCodes: codes}
	}
	return result, nil
}

func assetCodeOr(code *string, name string) string {
	if code != nil && *code != "" {
		return *code
	}
	return name
}

// ---- _scene_gating_core ----

// SceneGatingCore 对齐 _scene_gating_core。
func (r *Tasks) SceneGatingCore(ctx context.Context, q queryer, projectID string) ([]SceneGatingRow, error) {
	deps, err := r.ComputeSceneAssetDependencies(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	completedCodes, err := r.sceneCompletedAssetCodes(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(deps))
	for k := range deps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]SceneGatingRow, 0, len(keys))
	for _, sceneCode := range keys {
		info := deps[sceneCode]
		pending := []string{}
		for _, c := range info.RequiredAssetCodes {
			if !completedCodes[c] {
				pending = append(pending, c)
			}
		}
		out = append(out, SceneGatingRow{
			SceneCode:          sceneCode,
			SceneName:          info.SceneName,
			RequiredAssetCodes: info.RequiredAssetCodes,
			PendingAssetCodes:  pending,
			Locked:             len(pending) > 0,
		})
	}
	return out, nil
}

// sceneCompletedAssetCodes 对齐 _scene_gating_core 的 completed_codes 查询。
func (r *Tasks) sceneCompletedAssetCodes(ctx context.Context, q queryer, projectID string) (map[string]bool, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT a.asset_code
		 FROM assets a
		 JOIN tasks t ON t.asset_id = a.id
		 JOIN submissions s ON s.task_id = t.id
		 JOIN asset_versions av ON av.source_submission_id = s.id
		 WHERE a.project_id = $1
		   AND a.status NOT IN ('deleted','excluded')
		   AND t.task_type IN ('asset','text_to_image')
		   AND t.status = 'completed' AND t.is_retired = false
		   AND s.status IN ('primary_master','alternate_master')
		   AND s.is_selected = true AND s.is_archived = false AND s.is_invalidated = false
		   AND s.file_id IS NOT NULL
		   AND av.is_invalidated = false AND av.purged_at IS NULL
		   AND av.asset_id = a.id AND av.file_id = s.file_id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("scene completed codes: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var code *string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		if code != nil {
			out[*code] = true
		}
	}
	return out, rows.Err()
}

// IsSceneLocked 对齐 _scene_is_locked。scene_code/scene_name 都可为 nil。
func (r *Tasks) IsSceneLocked(ctx context.Context, q queryer, projectID string, sceneCode, sceneName *string) (bool, error) {
	if (sceneCode == nil || *sceneCode == "") && (sceneName == nil || *sceneName == "") {
		return false, nil
	}
	entries, err := r.SceneGatingCore(ctx, q, projectID)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if nonNil(sceneCode) != "" && entry.SceneCode == *sceneCode {
			return entry.Locked, nil
		}
		if nonNil(sceneName) != "" && entry.SceneName != nil && *entry.SceneName == *sceneName {
			return entry.Locked, nil
		}
	}
	return false, nil
}

// BatchSceneLockStates 对齐 _batch_scene_lock_states（要求 storyboard_id 非空）。
func (r *Tasks) BatchSceneLockStates(ctx context.Context, q queryer, tasks []*model.Tasks) (map[string]bool, error) {
	out := map[string]bool{}
	sceneTypes := map[string]bool{"storyboard_shot": true, "text_to_image": true, "image_to_video": true}
	sceneTasksByProject := map[string][]*model.Tasks{}
	for _, t := range tasks {
		if t.StoryboardId != nil && sceneTypes[t.TaskType] && (nonNil(t.SceneCode) != "" || nonNil(t.SceneName) != "") {
			sceneTasksByProject[t.ProjectId] = append(sceneTasksByProject[t.ProjectId], t)
		}
	}
	for projectID, projectTasks := range sceneTasksByProject {
		entries, err := r.SceneGatingCore(ctx, q, projectID)
		if err != nil {
			return nil, err
		}
		for _, t := range projectTasks {
			locked := false
			for _, entry := range entries {
				if nonNil(t.SceneCode) != "" && entry.SceneCode == *t.SceneCode {
					locked = entry.Locked
					break
				}
				if nonNil(t.SceneName) != "" && entry.SceneName != nil && *entry.SceneName == *t.SceneName {
					locked = entry.Locked
					break
				}
			}
			out[t.ID] = locked
		}
	}
	return out, nil
}

// ---- _dependency_contract_for_task / _batch_dependency_contracts ----

// ContractResult 单任务依赖契约输出。
type ContractResult struct {
	Summary DependencySummaryView
	Assets  []DependencyAssetView
}

// DependencyContractForTask 对齐 _dependency_contract_for_task（单任务详情）。
// depIDs/depCodes 传 nil 时内部解析依赖状态。
func (r *Tasks) DependencyContractForTask(ctx context.Context, q queryer, t *model.Tasks,
	depIDs, depCodes []string) (DependencySummaryView, []DependencyAssetView, error) {
	var err error
	if depIDs == nil || depCodes == nil {
		var locked bool
		_, depIDs, depCodes, err = r.TaskDependencyState(ctx, q, t)
		if err != nil {
			return DependencySummaryView{}, nil, err
		}
		_ = locked
	}
	codes := []string{}
	for _, c := range depCodes {
		if c != "" {
			codes = appendStrDedup(codes, c)
		}
	}
	if t.StoryboardId != nil && (nonNil(t.SceneCode) != "" || nonNil(t.SceneName) != "") {
		sceneDeps, err := r.ComputeSceneAssetDependencies(ctx, q, t.ProjectId)
		if err != nil {
			return DependencySummaryView{}, nil, err
		}
		for sceneCode, info := range sceneDeps {
			if (nonNil(t.SceneCode) != "" && sceneCode == *t.SceneCode) ||
				(nonNil(t.SceneName) != "" && info.SceneName != nil && *info.SceneName == *t.SceneName) {
				codes = append(codes, info.RequiredAssetCodes...)
				break
			}
		}
	}
	codes = strListDedup(codes)
	if len(codes) == 0 {
		return DependencySummaryView{}, []DependencyAssetView{}, nil
	}
	return r.buildDependencyContract(ctx, q, t, codes, depIDs)
}

// BatchDependencyContracts 对齐 _batch_dependency_contracts（列表路径）。
func (r *Tasks) BatchDependencyContracts(ctx context.Context, q queryer, tasks []*model.Tasks,
	states map[string]taskDepState) (map[string]ContractResult, error) {
	out := map[string]ContractResult{}
	if len(tasks) == 0 {
		return out, nil
	}
	projectIDs := []string{}
	for _, t := range tasks {
		projectIDs = appendStrDedup(projectIDs, t.ProjectId)
	}
	sceneMaps := map[string]map[string]SceneAssetDep{}
	for _, pid := range projectIDs {
		m, err := r.ComputeSceneAssetDependencies(ctx, q, pid)
		if err != nil {
			return nil, err
		}
		sceneMaps[pid] = m
	}
	codesByTask := map[string][]string{}
	allCodesByProject := map[string][]string{}
	for _, t := range tasks {
		state := states[t.ID]
		codes := []string{}
		for _, c := range state.AssetCodes {
			if c != "" {
				codes = appendStrDedup(codes, c)
			}
		}
		if t.StoryboardId != nil && (nonNil(t.SceneCode) != "" || nonNil(t.SceneName) != "") {
			for sceneCode, info := range sceneMaps[t.ProjectId] {
				if (nonNil(t.SceneCode) != "" && sceneCode == *t.SceneCode) ||
					(nonNil(t.SceneName) != "" && info.SceneName != nil && *info.SceneName == *t.SceneName) {
					codes = append(codes, info.RequiredAssetCodes...)
					break
				}
			}
		}
		codes = strListDedup(codes)
		codesByTask[t.ID] = codes
		for _, c := range codes {
			allCodesByProject[t.ProjectId] = appendStrDedup(allCodesByProject[t.ProjectId], c)
		}
	}
	allAssets := []*model.Assets{}
	for pid, codes := range allCodesByProject {
		if len(codes) == 0 {
			continue
		}
		assets, err := r.listAssetsByCodes(ctx, q, pid, codes)
		if err != nil {
			return nil, err
		}
		allAssets = append(allAssets, assets...)
	}
	assetsByProjectCode := map[string]*model.Assets{} // key = projectID + "\x00" + code
	assetIDs := []string{}
	for _, a := range allAssets {
		if a.AssetCode != nil && *a.AssetCode != "" {
			assetsByProjectCode[nilableStr(a.ProjectId)+"\x00"+*a.AssetCode] = a
			assetIDs = appendStrDedup(assetIDs, a.ID)
		}
	}
	tasksByAsset := map[string][]*model.Tasks{}
	if len(assetIDs) > 0 {
		assetTasks, err := r.listTasksByAssetIDs(ctx, q, assetIDs)
		if err != nil {
			return nil, err
		}
		for _, item := range assetTasks {
			tasksByAsset[nonNil(item.AssetId)] = append(tasksByAsset[nonNil(item.AssetId)], item)
		}
	}
	allSubmissions, err := r.listContractSubmissionsForTasks(ctx, q, r.assetTaskIDs(tasksByAsset))
	if err != nil {
		return nil, err
	}
	allSubmissionsByTask := map[string][]*model.Submissions{}
	for _, s := range allSubmissions {
		allSubmissionsByTask[s.TaskId] = append(allSubmissionsByTask[s.TaskId], s)
	}
	allVersions, err := r.listAssetVersionsPending(ctx, q, assetIDs)
	if err != nil {
		return nil, err
	}
	formalByTask, formalVersBySub := validFormalVersionMaps(allSubmissions, allVersions)
	fileIDs := []string{}
	for _, v := range formalVersBySub {
		if v.FileId != nil {
			fileIDs = appendStrDedup(fileIDs, *v.FileId)
		}
	}
	filesByID, err := r.listFilesByIDs(ctx, q, fileIDs)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		state := states[t.ID]
		entries := []DependencyAssetView{}
		for _, code := range codesByTask[t.ID] {
			asset := assetsByProjectCode[t.ProjectId+"\x00"+code]
			if asset == nil {
				entries = append(entries, DependencyAssetView{AssetCode: code, Ready: false, Reason: strPtr("asset_not_found")})
				continue
			}
			assetTasks := tasksByAsset[asset.ID]
			if t.AssetId != nil && *t.AssetId == asset.ID && !containsStr(state.IDs, t.ID) {
				directTasks := []*model.Tasks{}
				for _, item := range assetTasks {
					if containsStr(state.IDs, item.ID) {
						directTasks = append(directTasks, item)
					}
				}
				if len(directTasks) > 0 {
					assetTasks = directTasks
				}
			}
			entries = append(entries, dependencyAssetContractEntry(asset, code, assetTasks,
				allSubmissionsByTask, formalByTask, formalVersBySub, filesByID, taskRequiredFor(t)))
		}
		readyCount := 0
		for _, e := range entries {
			if e.Ready {
				readyCount++
			}
		}
		out[t.ID] = ContractResult{
			Summary: DependencySummaryView{Required: len(entries), Ready: readyCount, Pending: len(entries) - readyCount},
			Assets:  entries,
		}
	}
	return out, nil
}

func (r *Tasks) assetTaskIDs(tasksByAsset map[string][]*model.Tasks) []string {
	ids := []string{}
	for _, items := range tasksByAsset {
		for _, item := range items {
			ids = appendStrDedup(ids, item.ID)
		}
	}
	return ids
}

func taskRequiredFor(t *model.Tasks) string {
	if t.StoryboardId != nil {
		return "storyboard"
	}
	return "task"
}

func containsStr(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// buildDependencyContract 单任务依赖契约装配（对齐 _dependency_contract_for_task 主体）。
func (r *Tasks) buildDependencyContract(ctx context.Context, q queryer, t *model.Tasks, codes, depIDs []string) (DependencySummaryView, []DependencyAssetView, error) {
	assets, err := r.listAssetsByCodes(ctx, q, t.ProjectId, codes)
	if err != nil {
		return DependencySummaryView{}, nil, err
	}
	assetsByCode := map[string]*model.Assets{}
	assetIDs := []string{}
	for _, a := range assets {
		if a.AssetCode != nil && *a.AssetCode != "" {
			assetsByCode[*a.AssetCode] = a
			assetIDs = append(assetIDs, a.ID)
		}
	}
	tasksByAsset := map[string][]*model.Tasks{}
	if len(assetIDs) > 0 {
		assetTasks, err := r.listTasksByAssetIDsForProject(ctx, q, t.ProjectId, assetIDs)
		if err != nil {
			return DependencySummaryView{}, nil, err
		}
		for _, item := range assetTasks {
			tasksByAsset[nonNil(item.AssetId)] = append(tasksByAsset[nonNil(item.AssetId)], item)
		}
	}
	submissions, err := r.listContractSubmissionsForTasks(ctx, q, r.assetTaskIDs(tasksByAsset))
	if err != nil {
		return DependencySummaryView{}, nil, err
	}
	submissionsByTask := map[string][]*model.Submissions{}
	for _, s := range submissions {
		submissionsByTask[s.TaskId] = append(submissionsByTask[s.TaskId], s)
	}
	versions, err := r.listAssetVersionsPending(ctx, q, assetIDs)
	if err != nil {
		return DependencySummaryView{}, nil, err
	}
	formalByTask, formalVersBySub := validFormalVersionMaps(submissions, versions)
	fileIDs := []string{}
	for _, v := range formalVersBySub {
		if v.FileId != nil {
			fileIDs = append(fileIDs, *v.FileId)
		}
	}
	filesByID, err := r.listFilesByIDs(ctx, q, fileIDs)
	if err != nil {
		return DependencySummaryView{}, nil, err
	}
	entries := []DependencyAssetView{}
	for _, code := range codes {
		asset := assetsByCode[code]
		if asset == nil {
			entries = append(entries, DependencyAssetView{AssetCode: code, Ready: false, Reason: strPtr("asset_not_found")})
			continue
		}
		assetTasks := tasksByAsset[asset.ID]
		if t.AssetId != nil && *t.AssetId == asset.ID && !containsStr(depIDs, t.ID) {
			directTasks := []*model.Tasks{}
			for _, item := range assetTasks {
				if containsStr(depIDs, item.ID) {
					directTasks = append(directTasks, item)
				}
			}
			if len(directTasks) > 0 {
				assetTasks = directTasks
			}
		}
		entries = append(entries, dependencyAssetContractEntry(asset, code, assetTasks,
			submissionsByTask, formalByTask, formalVersBySub, filesByID, taskRequiredFor(t)))
	}
	readyCount := 0
	for _, e := range entries {
		if e.Ready {
			readyCount++
		}
	}
	return DependencySummaryView{Required: len(entries), Ready: readyCount, Pending: len(entries) - readyCount}, entries, nil
}

// validFormalVersionMaps 对齐 _valid_formal_version_maps。
func validFormalVersionMaps(submissions []*model.Submissions,
	versions []*model.AssetVersions) (map[string][]*model.Submissions, map[string]*model.AssetVersions) {
	versionsBySubmission := map[string]*model.AssetVersions{}
	for _, v := range versions {
		if v == nil || v.IsInvalidated || v.PurgedAt != nil || v.SourceSubmissionId == nil || v.FileId == nil {
			continue
		}
		existing := versionsBySubmission[*v.SourceSubmissionId]
		if existing == nil || v.VersionNo > existing.VersionNo {
			versionsBySubmission[*v.SourceSubmissionId] = v
		}
	}
	formalByTask := map[string][]*model.Submissions{}
	versionsBySub := map[string]*model.AssetVersions{}
	for _, s := range submissions {
		if s == nil || !approvedSubmissionStatuses[s.Status] || !s.IsSelected || s.IsArchived ||
			s.IsInvalidated || s.FileId == nil {
			continue
		}
		version := versionsBySubmission[s.ID]
		if version == nil || version.FileId == nil || *version.FileId != *s.FileId {
			continue
		}
		formalByTask[s.TaskId] = append(formalByTask[s.TaskId], s)
		versionsBySub[s.ID] = version
	}
	return formalByTask, versionsBySub
}

var downloadNameCleanRe = regexp.MustCompile(`[<>:\x22/\\|?*\r\n]+`)

// dependencyDownloadName 对齐 _dependency_download_name。
func dependencyDownloadName(asset *model.Assets, submission *model.Submissions, fileRecord *model.Files, ordinal int) string {
	originalName := strings.TrimSpace(fileRecord.FileName)
	suffix := ""
	if idx := strings.LastIndex(originalName, "."); idx >= 0 {
		suffix = originalName[idx:]
	}
	label := strings.TrimSpace(nonNil(submission.ViewLabel))
	if label == "" {
		label = strings.TrimSpace(nonNil(submission.StateLabel))
	}
	if label == "" {
		label = fmt.Sprintf("%02d", ordinal)
	}
	stem := joinNonEmpty(assetCodeStr(asset), strings.TrimSpace(asset.Name), label)
	stem = downloadNameCleanRe.ReplaceAllString(stem, "_")
	stem = strings.Trim(stem, " ._")
	if stem == "" {
		stem = "asset"
	}
	if len(stem) > 180 {
		stem = stem[:180]
	}
	return stem + suffix
}

func assetCodeStr(asset *model.Assets) string {
	if asset == nil || asset.AssetCode == nil {
		return ""
	}
	return *asset.AssetCode
}

// dependencyAssetContractEntry 对齐 _dependency_asset_contract_entry（纯函数）。
func dependencyAssetContractEntry(asset *model.Assets, assetCode string, assetTasks []*model.Tasks,
	allSubmissionsByTask map[string][]*model.Submissions,
	formalByTask map[string][]*model.Submissions,
	formalVersBySub map[string]*model.AssetVersions,
	filesByID map[string]*model.Files,
	requiredFor string) DependencyAssetView {
	formal := []*model.Submissions{}
	for _, item := range assetTasks {
		formal = append(formal, formalByTask[item.ID]...)
	}
	type pair struct {
		sub *model.Submissions
		ver *model.AssetVersions
	}
	formalPairs := []pair{}
	for _, s := range formal {
		if v, ok := formalVersBySub[s.ID]; ok {
			formalPairs = append(formalPairs, pair{sub: s, ver: v})
		}
	}
	files := []DependencyFileView{}
	for _, p := range formalPairs {
		if p.ver.FileId == nil {
			continue
		}
		fileRecord := filesByID[*p.ver.FileId]
		if fileRecord == nil {
			continue
		}
		versionID := p.ver.ID
		subID := p.sub.ID
		step := nonNil(p.sub.Step)
		downloadName := dependencyDownloadName(asset, p.sub, fileRecord, len(files)+1)
		files = append(files, DependencyFileView{
			FileId: *p.ver.FileId, FileName: fileStr(fileRecord.FileName),
			MimeType: fileRecord.MimeType, FileSize: fileRecord.FileSize,
			AssetVersionId: &versionID, DownloadName: &downloadName,
			ViewLabel: p.sub.ViewLabel, SubmissionId: &subID, Step: &step,
		})
	}
	formalTaskIDs := map[string]bool{}
	for _, p := range formalPairs {
		formalTaskIDs[p.sub.TaskId] = true
	}
	completionScope := assetTasks
	if len(formalPairs) > 0 {
		completionScope = []*model.Tasks{}
		for _, item := range assetTasks {
			if formalTaskIDs[item.ID] {
				completionScope = append(completionScope, item)
			}
		}
	}
	completed := false
	if len(completionScope) > 0 {
		completed = true
		for _, item := range completionScope {
			if item.Status != "completed" {
				completed = false
				break
			}
		}
	}
	pending := anySubmissionStatus(assetTasks, allSubmissionsByTask, map[string]bool{"submitted": true, "reviewing": true}) ||
		anyTaskStatus(assetTasks, map[string]bool{"submitted": true, "reviewing": true})
	rework := anySubmissionStatus(assetTasks, allSubmissionsByTask, map[string]bool{"rejected": true, "rework": true}) ||
		anyTaskStatus(assetTasks, map[string]bool{"rejected": true})
	ready := completed && len(files) > 0
	reason := "ready"
	switch {
	case ready:
	case len(assetTasks) == 0:
		reason = "unassigned"
	case rework:
		reason = "rework_required"
	case pending:
		reason = "pending_review"
	case !completed:
		reason = "production_incomplete"
	default:
		reason = "approved_file_missing"
	}
	productionStatus := "completed"
	switch {
	case !completed && rework:
		productionStatus = "rework"
	case !completed && len(assetTasks) > 0:
		productionStatus = "in_progress"
	case !completed && len(assetTasks) == 0:
		productionStatus = "unassigned"
	}
	approvalStatus := "pending"
	switch {
	case ready:
		approvalStatus = "approved"
	case rework:
		approvalStatus = "rework"
	case pending:
		approvalStatus = "pending_review"
	case len(files) > 0:
		approvalStatus = "approved"
	}
	assetID := asset.ID
	assetType := asset.AssetType
	return DependencyAssetView{
		AssetId: &assetID, AssetCode: assetCode, AssetName: &asset.Name, AssetType: &assetType,
		RequiredFor: &requiredFor, ProductionStatus: &productionStatus, ApprovalStatus: &approvalStatus,
		Ready: ready, Reason: &reason, Files: files,
	}
}

func anySubmissionStatus(tasks []*model.Tasks, all map[string][]*model.Submissions, want map[string]bool) bool {
	for _, t := range tasks {
		for _, s := range all[t.ID] {
			if want[s.Status] {
				return true
			}
		}
	}
	return false
}

func anyTaskStatus(tasks []*model.Tasks, want map[string]bool) bool {
	for _, t := range tasks {
		if want[t.Status] {
			return true
		}
	}
	return false
}

func fileStr(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func nilableStr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// ---- 打包查询辅助 ----

// listAssetsByProject 对齐 compute_scene_asset_dependencies 的 assets 查询（活跃资产）。
func (r *Tasks) listAssetsByProject(ctx context.Context, q queryer, projectID string) ([]*model.Assets, error) {
	rows, err := q.Query(ctx, `SELECT id, project_id, asset_code, asset_type, name, status, metadata_json
		 FROM assets WHERE project_id = $1 AND status NOT IN ('deleted','excluded')`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project assets: %w", err)
	}
	defer rows.Close()
	var out []*model.Assets
	for rows.Next() {
		var a model.Assets
		if err := rows.Scan(&a.ID, &a.ProjectId, &a.AssetCode, &a.AssetType, &a.Name, &a.Status, &a.MetadataJson); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// listAssetsByCodes 对齐 _dependency_contract_for_task 的 assets 查询（无状态过滤）。
func (r *Tasks) listAssetsByCodes(ctx context.Context, q queryer, projectID string, codes []string) ([]*model.Assets, error) {
	if len(codes) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `SELECT id, project_id, asset_code, asset_type, name, status, metadata_json
		 FROM assets WHERE project_id = $1 AND asset_code = ANY($2)`, projectID, codes)
	if err != nil {
		return nil, fmt.Errorf("list assets by codes: %w", err)
	}
	defer rows.Close()
	var out []*model.Assets
	for rows.Next() {
		var a model.Assets
		if err := rows.Scan(&a.ID, &a.ProjectId, &a.AssetCode, &a.AssetType, &a.Name, &a.Status, &a.MetadataJson); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// listTasksByAssetIDs 对齐 _batch_dependency_contracts 的 all_asset_tasks 查询（跨项目，无 project 过滤）。
func (r *Tasks) listTasksByAssetIDs(ctx context.Context, q queryer, assetIDs []string) ([]*model.Tasks, error) {
	if len(assetIDs) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE asset_id = ANY($1) AND `+activeAssetTaskCondition(), assetIDs)
	if err != nil {
		return nil, fmt.Errorf("list tasks by asset ids: %w", err)
	}
	return scanTaskRows(rows)
}

// listTasksByAssetIDsForProject 对齐 _dependency_contract_for_task 的 task_rows 查询。
func (r *Tasks) listTasksByAssetIDsForProject(ctx context.Context, q queryer, projectID string, assetIDs []string) ([]*model.Tasks, error) {
	if len(assetIDs) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx,
		`SELECT `+taskColumns+` FROM tasks
		 WHERE project_id = $1 AND asset_id = ANY($2) AND `+activeAssetTaskCondition(), projectID, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("list project asset tasks: %w", err)
	}
	return scanTaskRows(rows)
}

// listContractSubmissionsForTasks 对齐合约查询的 submissions 加载（未归档未失效，无状态过滤）。
func (r *Tasks) listContractSubmissionsForTasks(ctx context.Context, q queryer, taskIDs []string) ([]*model.Submissions, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `SELECT `+submissionColumns+` FROM submissions
		 WHERE task_id = ANY($1) AND is_archived = false AND is_invalidated = false`, taskIDs)
	if err != nil {
		return nil, fmt.Errorf("list contract submissions: %w", err)
	}
	return scanSubmissionRows(rows)
}

// listAssetVersionsPending 对齐合约查询的 versions 加载（未失效未清理，无状态过滤）。
func (r *Tasks) listAssetVersionsPending(ctx context.Context, q queryer, assetIDs []string) ([]*model.AssetVersions, error) {
	if len(assetIDs) == 0 {
		return nil, nil
	}
	rows, err := q.Query(ctx, `SELECT * FROM asset_versions
		 WHERE asset_id = ANY($1) AND is_invalidated = false AND purged_at IS NULL`, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("list asset versions pending: %w", err)
	}
	defer rows.Close()
	var out []*model.AssetVersions
	for rows.Next() {
		var v model.AssetVersions
		if err := rows.Scan(&v.ID, &v.AssetId, &v.VersionNo, &v.AssetVersionCode, &v.FileId, &v.PreviewFileId,
			&v.PromptText, &v.NegativePromptText, &v.BaseModel, &v.ToolName, &v.MetadataJson, &v.IsCurrent,
			&v.SourceTaskId, &v.SourceAgentRunId, &v.CreatedBy, &v.SourceSubmissionId,
			&v.SourceMasterSubmissionId, &v.IsInvalidated, &v.InvalidatedAt, &v.InvalidatedById,
			&v.InvalidatedReason, &v.InvalidatedSourceId, &v.PurgedAt, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}

// listFilesByIDs 对齐合约查询的 files 加载。
func (r *Tasks) listFilesByIDs(ctx context.Context, q queryer, fileIDs []string) (map[string]*model.Files, error) {
	out := map[string]*model.Files{}
	if len(fileIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, file_code, bucket, object_key, file_name, mime_type, file_size,
		 checksum, uploaded_by, project_id, episode_id, task_id, created_at, updated_at
		 FROM files WHERE id = ANY($1)`, fileIDs)
	if err != nil {
		return nil, fmt.Errorf("list files by ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var f model.Files
		if err := rows.Scan(&f.ID, &f.FileCode, &f.Bucket, &f.ObjectKey, &f.FileName, &f.MimeType,
			&f.FileSize, &f.Checksum, &f.UploadedBy, &f.ProjectId, &f.EpisodeId, &f.TaskId,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out[f.ID] = &f
	}
	return out, rows.Err()
}

// listProjectStoryboards 对齐 compute_scene_asset_dependencies 的 storyboards 查询（无状态过滤）。
func (r *Tasks) listProjectStoryboards(ctx context.Context, q queryer, projectID string) ([]*model.Storyboards, error) {
	rows, err := q.Query(ctx, `SELECT id, project_id, episode_num, order_num, title, description, dialogue,
		 camera, duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
		 scene_code, scene_name, context_code, render_mode, script_id, script_segment_id, narration,
		 shot_type, current_version_id, created_at, updated_at
		 FROM storyboards WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project storyboards: %w", err)
	}
	defer rows.Close()
	var out []*model.Storyboards
	for rows.Next() {
		var s model.Storyboards
		if err := rows.Scan(&s.ID, &s.ProjectId, &s.EpisodeNum, &s.OrderNum, &s.Title, &s.Description,
			&s.Dialogue, &s.Camera, &s.DurationSeconds, &s.Characters, &s.Keyframes, &s.MirrorShots,
			&s.Status, &s.StoryboardCode, &s.SceneCode, &s.SceneName, &s.ContextCode, &s.RenderMode,
			&s.ScriptId, &s.ScriptSegmentId, &s.Narration, &s.ShotType, &s.CurrentVersionId,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func anyToStringList(v any) []string {
	if v == nil {
		return nil
	}
	switch vals := v.(type) {
	case []any:
		out := []string{}
		for _, item := range vals {
			s := strings.TrimSpace(fmt.Sprintf("%v", item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return vals
	case string:
		if vals != "" {
			return []string{vals}
		}
	}
	return nil
}
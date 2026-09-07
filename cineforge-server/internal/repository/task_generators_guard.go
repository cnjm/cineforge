package repository

// P3f-d 生成器公共部分：作用域守卫、确认资产清单、生命周期、视图校验、视图物化。
// 对齐 legacy app/services/repositories.py 的：
//   - _require_asset_prompt_designs_for_scope / _assets_missing_prompt_design / _assets_not_confirmed
//   - confirmed_asset_inventory / _overlay_current_asset_lifecycle
//   - _materialize_assets_from_view / _task_generation_assets_without_bindings / _append_asset_revision
//   - validate_breakdown_view 的 blocking 段（含逐字文案）
//
// 事务约定与 BulkAssignTasks/CreateStoryboardVideoTasks 一致：服务层拥有事务，
// 本层方法接收 pgx.Tx，不自行 commit/rollback。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// 生命周期状态集（legacy _ASSET_LIFECYCLE_STATES）。
var assetLifecycleStateSet = map[string]bool{
	"needs_completion":    true,
	"prompt_pending":      true,
	"pending_confirmation": true,
	"confirmed":           true,
}

// assetBreakdownFields 对齐 _ASSET_BREAKDOWN_FIELDS。
var assetBreakdownFields = []string{
	"normalization_version", "client_asset_key", "display_code", "display_label", "code_state",
	"attributes", "relations", "review_items", "source", "repairs", "priority",
}

// promptNonInputFields 对齐 _PROMPT_NON_INPUT_FIELDS。
var promptNonInputFields = map[string]bool{
	"id": true, "asset_id": true, "asset_code": true, "agent_asset_code": true,
	"client_asset_key": true, "asset_code_source": true, "character_code": true,
	"scene_code": true, "prop_code": true, "role_code": true, "candidate_asset_code": true,
	"status": true, "confirmation_status": true, "prompt_validity": true,
	"prompt_input_fingerprint": true, "prompt_generated_for_fingerprint": true,
	"prompt_invalidated_at": true, "prompt_invalidated_fields": true, "prompt": true,
	"prompt_text": true, "negative_prompt": true, "input_hash": true, "skill": true,
	"skill_version": true, "contract_version": true, "contract_hash": true,
	"prompt_version": true, "context_key": true, "context_schema_version": true,
	"brief_trace": true, "resolved_production_brief": true, "production_context": true,
	"production_contexts": true, "prompt_lineage": true, "prompt_design": true,
	"costume_design": true, "view_prompts": true, "state_prompts": true, "spatial_bible": true,
	"material_bible": true, "material_layers": true, "character_apose": true,
	"manual_review_items": true, "generated_output_spec": true, "generated_view_codes": true,
	"deferred_view_codes": true, "generation_strategy": true, "runtime_policy_execution": true,
	"structured_output_mode": true, "error": true, "processing_status": true, "name": true,
	"display_name": true, "localized_name": true, "description": true, "description_zh": true,
	"description_th": true, "translated_name": true, "translated_description": true,
	"back_translation": true, "translation_notes": true, "priority": true, "order": true,
	"order_no": true, "index": true, "episode_id": true, "episode_code": true,
	"script_id": true, "script_version_id": true, "storyboard_id": true, "task_id": true,
	"revision_id": true, "asset_revision_id": true, "source_run_id": true, "agent_run_id": true,
	"created_at": true, "updated_at": true, "confirmed_at": true, "confirmed_by": true,
	"asset_task_contexts": true, "has_asset_tasks": true, "task_count": true, "notes": true,
	"human_notes": true, "_contract_repairs": true,
}

// ---- 生成器公共前置：项目锁 + 作用域 ----

// lockGenerationProjectTx 对齐 ensure_project_write_access + Project(for_update=True)。
// 项目不存在或已删除 → notFoundError；非写权限 → forbiddenError。
func (r *Tasks) lockGenerationProjectTx(ctx context.Context, tx pgx.Tx, projectID string, actor TaskActor) (*model.Projects, error) {
	var p model.Projects
	var deletedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT id, project_no, project_prefix, name, title, genre, style, status,
			current_stage, manager_id, created_by_id, script_text, production_brief, locked_at, archived_at,
			deleted_at, deleted_by_id, delete_reason, created_at, updated_at
		 FROM projects WHERE id = $1 FOR UPDATE`, projectID).
		Scan(&p.ID, &p.ProjectNo, &p.ProjectPrefix, &p.Name, &p.Title, &p.Genre, &p.Style,
			&p.Status, &p.CurrentStage, &p.ManagerId, &p.CreatedById, &p.ScriptText,
			&p.ProductionBrief, &p.LockedAt, &p.ArchivedAt, &deletedAt, &p.DeletedById,
			&p.DeleteReason, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFoundError(projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("lock project for generation: %w", err)
	}
	if deletedAt != nil {
		return nil, notFoundError(projectID)
	}
	canWrite, err := r.CanWriteProject(ctx, tx, projectID, actor.ID, actor.Role)
	if err != nil {
		return nil, err
	}
	if !canWrite {
		return nil, forbiddenError(projectID)
	}
	return &p, nil
}

// loadGenerationScriptScopeTx 加载并校验 episode/script_version/script 与项目的匹配关系。
// 不匹配时逐字返回 "分集与剧本版本不匹配。"。
func (r *Tasks) loadGenerationScriptScopeTx(ctx context.Context, tx pgx.Tx,
	projectID, episodeID, scriptVersionID string) (*model.ProjectEpisodes, *model.Scripts, error) {
	episode, err := scanEpisode(tx.QueryRow(ctx,
		`SELECT `+episodeColumns+` FROM project_episodes WHERE id = $1`, episodeID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("load generation episode: %w", err)
	}
	scriptVersion, err := scanScriptVersion(tx.QueryRow(ctx,
		`SELECT `+scriptVersionColumns+` FROM script_versions WHERE id = $1`, scriptVersionID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, fmt.Errorf("load generation script version: %w", err)
	}
	var script *model.Scripts
	if scriptVersion != nil {
		script, err = scanScript(tx.QueryRow(ctx,
			`SELECT `+scriptColumns+` FROM scripts WHERE id = $1`, scriptVersion.ScriptId))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, fmt.Errorf("load generation script: %w", err)
		}
	}
	if episode == nil || episode.ProjectId != projectID ||
		script == nil || script.ProjectId != projectID ||
		script.EpisodeId == nil || *script.EpisodeId != episodeID {
		return nil, nil, valueError("分集与剧本版本不匹配。")
	}
	return episode, script, nil
}

// ---- 生成器守卫：资产 Prompt 设计就绪 ----

// requireAssetPromptDesignsForScopeTx 对齐 _require_asset_prompt_designs_for_scope。
// 返回该作用域已确认资产清单，守卫失败时逐字 ValueError。
func (r *Tasks) requireAssetPromptDesignsForScopeTx(ctx context.Context, tx pgx.Tx,
	projectID, episodeID, scriptVersionID string) ([]map[string]any, error) {
	inventory, err := r.confirmedAssetInventoryTx(ctx, tx, projectID, &episodeID, &scriptVersionID, true)
	if err != nil {
		return nil, err
	}
	if len(inventory) == 0 {
		return nil, valueError("当前分集没有已确认正式资产，不能进入任务阶段。")
	}
	view := map[string]any{}
	if content := r.latestScopedBreakdownContentTx(ctx, tx, projectID, episodeID, scriptVersionID); content != nil {
		view = content
	}
	if missing := assetsMissingPromptDesign(inventory, view); len(missing) > 0 {
		return nil, valueError(fmt.Sprintf("以下资产尚未经过专用 Prompt Skill：%s。请先补齐资产 Prompt。",
			strings.Join(missing, "、")))
	}
	if unconfirmed := assetsNotConfirmed(inventory); len(unconfirmed) > 0 {
		return nil, valueError(fmt.Sprintf("以下资产尚未完成人工确认：%s。请逐项确认后再进入任务阶段。",
			strings.Join(unconfirmed, "、")))
	}
	return inventory, nil
}

// confirmedAssetInventoryTx 对齐 confirmed_asset_inventory。
// 仅返回有人工确认或已应用来源背书的资产；strict_scope 时禁用具集性回退。
func (r *Tasks) confirmedAssetInventoryTx(ctx context.Context, q queryer, projectID string,
	episodeID, scriptVersionID *string, strictScope bool) ([]map[string]any, error) {
	// (1) 人工确认的 asset_inventory 修订
	// (1) 人工确认的 asset_inventory 修订
	rows, err := q.Query(ctx, `SELECT `+artifactRevisionColumns+` FROM artifact_revisions
		WHERE project_id = $1 AND artifact_type = 'asset_inventory' AND source_type = 'human' AND status = 'confirmed'
		ORDER BY created_at DESC, version_no DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("confirmed asset inventory revisions: %w", err)
	}
	var revisions []*model.ArtifactRevisions
	for rows.Next() {
		rev, err := scanArtifactRevision(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		revisions = append(revisions, rev)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("confirmed asset inventory revisions: %w", err)
	}
	for _, rev := range revisions {
		snapshot := metadataAsMap(rev.InputSnapshot)
		if episodeID != nil && strOf(snapshot, "episode_id") != *episodeID {
			continue
		}
		if scriptVersionID != nil && strOf(snapshot, "script_version_id") != *scriptVersionID {
			continue
		}
		content := metadataAsMap(rev.NormalizedContent)
		rawItems := anySlice(content["assets"])
		if rawItems == nil {
			rawItems = anySlice(content["items"])
		}
		var inventory []map[string]any
		for _, raw := range rawItems {
			item, ok := raw.(map[string]any)
			if !ok || strOf(item, "asset_code") == "" {
				continue
			}
			inventory = append(inventory, dictCopy(item))
		}
		if len(inventory) == 0 {
			continue
		}
		overlaid, err := r.overlayCurrentAssetLifecycleTx(ctx, q, projectID, inventory)
		if err != nil {
			return nil, err
		}
		if len(assetsNotConfirmed(overlaid)) > 0 {
			return []map[string]any{}, nil
		}
		out := make([]map[string]any, 0, len(overlaid))
		for _, item := range overlaid {
			out = append(out, dictCopy(map[string]any{
				"asset_inventory_revision_id": rev.ID,
				"confirmation_source":         "confirmed_asset_inventory",
			}, item))
		}
		return out, nil
	}

	// (2) applied reading_asset_application 绑定的资产
	bindings, err := r.listActiveAssetBindingsTx(ctx, q, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	if len(bindings) > 0 {
		applicationIDs := map[string]bool{}
		for _, binding := range bindings {
			applicationIDs[binding.ApplicationRevisionId] = true
		}
		ids := make([]string, 0, len(applicationIDs))
		for id := range applicationIDs {
			ids = append(ids, id)
		}
		applied := map[string]bool{}
		arows, err := q.Query(ctx, `SELECT id FROM artifact_revisions
			WHERE id = ANY($1) AND artifact_type = 'reading_asset_application' AND status = 'applied'`, ids)
		if err != nil {
			return nil, fmt.Errorf("applied reading asset applications: %w", err)
		}
		for arows.Next() {
			var id string
			if err := arows.Scan(&id); err != nil {
				arows.Close()
				return nil, err
			}
			applied[id] = true
		}
		arows.Close()
		if err := arows.Err(); err != nil {
			return nil, fmt.Errorf("applied reading asset applications: %w", err)
		}
		var valid []*model.EpisodeAssetBindings
		assetIDs := map[string]bool{}
		for _, binding := range bindings {
			if !applied[binding.ApplicationRevisionId] {
				continue
			}
			valid = append(valid, binding)
			assetIDs[binding.AssetId] = true
		}
		assets := map[string]*model.Assets{}
		if len(assetIDs) > 0 {
			ids := make([]string, 0, len(assetIDs))
			for id := range assetIDs {
				ids = append(ids, id)
			}
			_loadAssetRows(ctx, q, projectID, ids, assets)
		}
		var inventory []map[string]any
		for _, binding := range valid {
			asset := assets[binding.AssetId]
			if asset == nil || asset.Status == "deleted" || asset.Status == "excluded" {
				continue
			}
			inventory = append(inventory, inventoryItemFromAsset(asset, binding.AssetRevisionId,
				"applied_reading_asset_application", binding.ApplicationRevisionId))
		}
		if len(inventory) > 0 {
			if len(assetsNotConfirmed(inventory)) > 0 {
				return []map[string]any{}, nil
			}
			return inventory, nil
		}
	}

	// (3) strict_scope：作用域命中失败则不允许回退
	if strictScope && (episodeID != nil || scriptVersionID != nil) {
		return []map[string]any{}, nil
	}

	// (4) 项目级确认资产
	assets, err := r.listProjectAssetsOrderedTx(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	var inventory []map[string]any
	for _, asset := range assets {
		if assetConfirmationStatus(asset) != "confirmed" {
			continue
		}
		var revisionID *string
		if asset.CurrentRevisionId != nil {
			rev, err := r.getArtifactRevisionByIDTx(ctx, q, *asset.CurrentRevisionId)
			if err != nil {
				return nil, err
			}
			if rev != nil {
				id := rev.ID
				revisionID = &id
			}
		}
		source := "confirmed_asset_revision"
		if revisionID == nil {
			source = "locked_asset"
		}
		inventory = append(inventory, inventoryItemFromAsset(asset, revisionID, source, ""))
	}
	return inventory, nil
}

func (r *Tasks) listActiveAssetBindingsTx(ctx context.Context, q queryer, projectID string,
	episodeID, scriptVersionID *string) ([]*model.EpisodeAssetBindings, error) {
	where := `project_id = $1 AND status = 'active'`
	args := []any{projectID}
	if episodeID != nil {
		args = append(args, *episodeID)
		where += fmt.Sprintf(" AND episode_id = $%d", len(args))
	}
	if scriptVersionID != nil {
		args = append(args, *scriptVersionID)
		where += fmt.Sprintf(" AND script_version_id = $%d", len(args))
	}
	rows, err := q.Query(ctx, `SELECT `+episodeAssetBindingColumns+` FROM episode_asset_bindings
		WHERE `+where+` ORDER BY proposal_index`, args...)
	if err != nil {
		return nil, fmt.Errorf("list active asset bindings: %w", err)
	}
	defer rows.Close()
	var out []*model.EpisodeAssetBindings
	for rows.Next() {
		b, err := scanEpisodeAssetBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *Tasks) listProjectAssetsOrderedTx(ctx context.Context, q queryer,
	projectID string) ([]*model.Assets, error) {
	rows, err := q.Query(ctx, `SELECT `+assetColumns+` FROM assets
		WHERE project_id = $1 AND status NOT IN ('deleted','excluded')
		ORDER BY asset_code, name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project assets: %w", err)
	}
	defer rows.Close()
	var out []*model.Assets
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, asset)
	}
	return out, rows.Err()
}

func (r *Tasks) getArtifactRevisionByIDTx(ctx context.Context, q queryer, id string) (*model.ArtifactRevisions, error) {
	rev, err := scanArtifactRevision(q.QueryRow(ctx,
		`SELECT `+artifactRevisionColumns+` FROM artifact_revisions WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return rev, nil
}

func _loadAssetRows(ctx context.Context, q queryer, projectID string, ids []string, into map[string]*model.Assets) {
	rows, err := q.Query(ctx, `SELECT `+assetColumns+` FROM assets
		WHERE project_id = $1 AND id = ANY($2)`, projectID, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return
		}
		into[asset.ID] = asset
	}
}

// inventoryItemFromAsset 由资产行构建 inventory 条目（对齐 confirmed_asset_inventory 的条目形状）。
func inventoryItemFromAsset(asset *model.Assets, assetRevisionID *string, source, applicationID string) map[string]any {
	var code, name, description, prompt *string
	code = asset.AssetCode
	name = &asset.Name
	description = asset.Description
	prompt = asset.PromptText
	item := map[string]any{
		"asset_id":     asset.ID,
		"asset_code":   code,
		"asset_type":   asset.AssetType,
		"name":         name,
		"description":  description,
		"prompt":       prompt,
		"status":       assetConfirmationStatus(asset),
		"metadata":     metadataAsMap(asset.MetadataJson),
		"confirmation_source": source,
	}
	if assetRevisionID != nil {
		item["asset_revision_id"] = *assetRevisionID
	} else {
		item["asset_revision_id"] = nil
	}
	if applicationID != "" {
		item["application_revision_id"] = applicationID
	}
	return item
}

// overlayCurrentAssetLifecycleTx 对齐 _overlay_current_asset_lifecycle。
// 用正式资产库行覆盖清单条目；干净 Breakdown 视图保持不可变。
func (r *Tasks) overlayCurrentAssetLifecycleTx(ctx context.Context, q queryer, projectID string,
	items []map[string]any) ([]map[string]any, error) {
	rows, err := q.Query(ctx, `SELECT `+assetColumns+` FROM assets
		WHERE project_id = $1 AND asset_type = ANY($2)`, projectID, visualAssetTypeKeys())
	if err != nil {
		return nil, fmt.Errorf("overlay current asset lifecycle: %w", err)
	}
	var all []*model.Assets
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, asset)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("overlay current asset lifecycle: %w", err)
	}
	byID := map[string]*model.Assets{}
	byCode := map[string]*model.Assets{}
	for _, asset := range all {
		byID[asset.ID] = asset
		if asset.AssetCode != nil {
			byCode[strings.ToUpper(strings.TrimSpace(*asset.AssetCode))] = asset
		}
	}
	var out []map[string]any
	for _, raw := range items {
		item := dictCopy(raw)
		assetID := strings.TrimSpace(strAny(firstNonNil(item["asset_id"], item["id"]), ""))
		assetCode := strings.ToUpper(strings.TrimSpace(strOf(item, "asset_code")))
		row := byID[assetID]
		if row == nil && assetCode != "" {
			row = byCode[assetCode]
		}
		if row != nil && (row.Status == "deleted" || row.Status == "excluded") {
			continue
		}
		if row == nil {
			out = append(out, item)
			continue
		}
		rowMetadata := metadataWithoutBreakdownAsset(metadataAsMap(row.MetadataJson))
		metadata := map[string]any{}
		if m, ok := item["metadata"].(map[string]any); ok {
			for k, v := range m {
				metadata[k] = v
			}
		}
		for k, v := range rowMetadata {
			metadata[k] = v
		}
		status := assetConfirmationStatus(row)
		metadata["confirmation_status"] = status
		revisionID := item["asset_revision_id"]
		if row.CurrentRevisionId != nil {
			revisionID = *row.CurrentRevisionId
		}
		item["asset_id"] = row.ID
		item["asset_code"] = row.AssetCode
		item["asset_type"] = row.AssetType
		item["name"] = row.Name
		item["description"] = row.Description
		item["status"] = status
		item["metadata"] = metadata
		item["asset_revision_id"] = revisionID
		if row.PromptText != nil {
			item["prompt"] = *row.PromptText
			item["prompt_text"] = *row.PromptText
		}
		out = append(out, item)
	}
	return out, nil
}

// ---- 生命周期纯函数 ----

// assetLifecycleStatus 对齐 _asset_lifecycle_status。
func assetLifecycleStatus(asset map[string]any) string {
	metadata := map[string]any{}
	if m, ok := asset["metadata"].(map[string]any); ok {
		metadata = m
	}
	value := strings.ToLower(strings.TrimSpace(strAny(
		firstNonNil(metadata["confirmation_status"], asset["status"]), "")))
	if value == "" {
		return "pending_confirmation"
	}
	if assetLifecycleStateSet[value] {
		return value
	}
	return "pending_confirmation"
}

// assetLifecycleLabel 对齐 _asset_lifecycle_label。
func assetLifecycleLabel(asset map[string]any) string {
	code := strings.TrimSpace(strOf(asset, "asset_code"))
	shortCode := ""
	if code != "" {
		shortCode = code
		if idx := strings.LastIndex(code, "-"); idx >= 0 {
			shortCode = code[idx+1:]
		}
	}
	parts := []string{}
	if shortCode != "" {
		parts = append(parts, shortCode)
	}
	if name := strings.TrimSpace(strOf(asset, "name")); name != "" {
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return "未命名资产"
	}
	return strings.Join(parts, " ")
}

// promptRecordMatchesAsset 对齐 prompt_record_matches_asset。
func promptRecordMatchesAsset(record, asset map[string]any) bool {
	assetKey := strings.TrimSpace(strOf(asset, "client_asset_key"))
	recordKey := strings.TrimSpace(strOf(record, "client_asset_key"))
	if assetKey != "" && recordKey != "" {
		return assetKey == recordKey
	}
	assetCode := strings.ToUpper(strings.TrimSpace(strOf(asset, "asset_code")))
	recordCodes := map[string]bool{}
	for _, key := range []string{"asset_code", "matched_asset_code"} {
		if value := strings.TrimSpace(strOf(record, key)); value != "" {
			recordCodes[strings.ToUpper(value)] = true
		}
	}
	return assetCode != "" && recordCodes[assetCode]
}

// assetsMissingPromptDesign 对齐 assets_missing_prompt_design（逐字文案）。
func assetsMissingPromptDesign(rawAssets []map[string]any, view map[string]any) []string {
	var designs []map[string]any
	for _, key := range []string{"asset_prompt_designs", "costume_designs"} {
		for _, item := range mapList(view[key]) {
			designs = append(designs, item)
		}
	}
	var summaries []map[string]any
	for _, key := range []string{"asset_prompt_processing_summary", "costume_processing_summary"} {
		for _, item := range mapList(view[key]) {
			summaries = append(summaries, item)
		}
	}
	var missing []string
	for _, raw := range rawAssets {
		if strings.ToLower(strings.TrimSpace(strAny(firstNonNil(raw["asset_type"], raw["type"]), ""))) == "character" &&
			characterIsNonVisual(raw) {
			continue
		}
		if lifecycle := assetLifecycleStatus(raw); lifecycle == "needs_completion" || lifecycle == "prompt_pending" {
			missing = append(missing, assetLifecycleLabel(raw))
			continue
		}
		metadata := map[string]any{}
		if m, ok := raw["metadata"].(map[string]any); ok {
			metadata = m
		}
		promptDesign := map[string]any{}
		if m, ok := metadata["prompt_design"].(map[string]any); ok {
			promptDesign = m
		}
		promptLineage := map[string]any{}
		if m, ok := metadata["prompt_lineage"].(map[string]any); ok {
			promptLineage = m
		}
		if hasAny(raw["prompt"]) || hasAny(raw["input_hash"]) || hasAny(raw["skill"]) ||
			hasAny(promptDesign["prompt"]) || hasAny(promptLineage["skill"]) {
			continue
		}
		matchedDesign := first(promptRecordMatchesAsset, designs, raw)
		if matchedDesign != nil && !hasAny((*matchedDesign)["error"]) &&
			(hasAny((*matchedDesign)["prompt"]) || hasAny((*matchedDesign)["input_hash"]) || hasAny((*matchedDesign)["skill"])) {
			continue
		}
		matchedSummary := first(promptRecordMatchesAsset, summaries, raw)
		if matchedSummary != nil && strings.ToLower(strings.TrimSpace(strOf(*matchedSummary, "processing_status"))) == "reused" {
			continue
		}
		missing = append(missing, assetLifecycleLabel(raw))
	}
	return missing
}

// assetsNotConfirmed 对齐 assets_not_confirmed。
func assetsNotConfirmed(rawAssets []map[string]any) []string {
	var out []string
	for _, raw := range rawAssets {
		if strings.ToLower(strings.TrimSpace(strAny(firstNonNil(raw["asset_type"], raw["type"]), ""))) == "character" &&
			characterIsNonVisual(raw) {
			continue
		}
		if assetLifecycleStatus(raw) != "confirmed" {
			out = append(out, assetLifecycleLabel(raw))
		}
	}
	return out
}

func hasAny(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(t) != ""
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	case bool:
		return t
	default:
		return true
	}
}

func first(pred func(map[string]any, map[string]any) bool,
	items []map[string]any, asset map[string]any) *map[string]any {
	for i := range items {
		if pred(items[i], asset) {
			return &items[i]
		}
	}
	return nil
}

// ---- 视图校验：blocking 错误逐字 ----

// validateBreakdownViewBlocking 对齐 validate_breakdown_view 的 blocking_errors 段。
// 返回逐字错误文案；只用内容 map（script_segments/storyboards/assets）。
func validateBreakdownViewBlocking(content map[string]any) []string {
	var errors_ []string
	scriptSegments := mapList(content["script_segments"])
	storyboards := mapList(content["storyboards"])
	assets := mapList(content["assets"])

	if len(scriptSegments) == 0 {
		errors_ = append(errors_, "缺少脚本段，不能生成可追溯分镜。")
	}
	if len(storyboards) == 0 {
		errors_ = append(errors_, "缺少分镜，不能进入业务展示。")
	}
	segmentCodes := map[string]bool{}
	for _, segment := range scriptSegments {
		if code := strOf(segment, "script_segment_code"); code != "" {
			segmentCodes[code] = true
		}
	}
	for _, segment := range scriptSegments {
		if strings.TrimSpace(strAny(firstNonNil(segment["source_text"], segment["summary"]), "")) == "" {
			errors_ = append(errors_, "脚本段缺少原文或摘要。")
		}
	}
	for _, storyboard := range storyboards {
		description := strings.TrimSpace(strOf(storyboard, "description"))
		if description == "" || strings.Contains(description, "模型未返回画面描述") {
			errors_ = append(errors_, "分镜缺少有效画面描述。")
		}
		segmentCode := strOf(storyboard, "script_segment_code")
		if segmentCode == "" || !segmentCodes[segmentCode] {
			errors_ = append(errors_, "分镜没有关联到有效脚本段。")
		}
		duration := intOrNone(storyboard["duration_seconds"])
		if duration == nil {
			errors_ = append(errors_, "分镜缺少时长。")
		} else if *duration < 10 || *duration > 15 {
			errors_ = append(errors_, fmt.Sprintf("父分镜时长 %ds 超出 10-15 秒硬性范围。", *duration))
		}
		if strings.TrimSpace(strOf(storyboard, "shot_size")) == "" {
			errors_ = append(errors_, "分镜缺少明确的中文景别。")
		} else if strings.TrimSpace(strOf(storyboard, "shot_size")) == "未明确" {
			errors_ = append(errors_, "分镜缺少明确的中文景别。")
		}
		mirrorShots := mapList(storyboard["mirror_shots"])
		if len(mirrorShots) > 0 && len(mirrorShots) < 2 {
			errors_ = append(errors_, "镜中分镜必须至少包含 A/B 两个完整内部镜头。")
		}
		if len(mirrorShots) > 6 {
			errors_ = append(errors_, "镜中分镜最多包含 A-F 六个内部镜头。")
		}
		mirrorDurationTotal := 0.0
		mirrorDurationsComplete := len(mirrorShots) > 0
		for _, mirror := range mirrorShots {
			if strings.TrimSpace(strOf(mirror, "description")) == "" {
				errors_ = append(errors_, "镜中分镜缺少画面描述。")
			}
			if strings.TrimSpace(strOf(mirror, "camera")) == "" {
				errors_ = append(errors_, "镜中分镜缺少镜头执行描述。")
			}
			if shot := strings.TrimSpace(strOf(mirror, "shot_size")); shot == "" || shot == "未明确" {
				errors_ = append(errors_, "镜中分镜缺少明确的中文景别。")
			}
			for _, field := range []struct{ key, msg string }{
				{"shot_function", "镜中分镜缺少镜头职能。"},
				{"movement_reason", "镜中分镜缺少运镜动机。"},
				{"environmental_pressure", "镜中分镜缺少环境压力细节。"},
				{"micro_action", "镜中分镜缺少人物身体微动作。"},
				{"sound_or_motif", "镜中分镜缺少声音或视觉母题。"},
			} {
				if strings.TrimSpace(strOf(mirror, field.key)) == "" {
					errors_ = append(errors_, field.msg)
				}
			}
			mirrorDuration := floatOrNone(mirror["duration_seconds"])
			if mirrorDuration == nil || *mirrorDuration <= 0 {
				mirrorDurationsComplete = false
				errors_ = append(errors_, "镜中分镜缺少有效时长。")
			} else {
				mirrorDurationTotal += *mirrorDuration
			}
		}
		if mirrorDurationsComplete && duration != nil && absFloat64(mirrorDurationTotal-float64(*duration)) > 0.1 {
			errors_ = append(errors_, fmt.Sprintf("镜中分镜总时长 %gs 与父分镜 %ds 不一致。",
				mirrorDurationTotal, *duration))
		}
		parentDialogueLines := dialogueLines(storyboard["dialogue"])
		if len(mirrorShots) > 0 && len(parentDialogueLines) > 0 {
			var childDialogueLines []string
			for _, mirror := range mirrorShots {
				childDialogueLines = append(childDialogueLines, dialogueLines(mirror["dialogue"])...)
			}
			if len(childDialogueLines) == 0 {
				errors_ = append(errors_, "父分镜包含台词，但镜中分镜 A/B 未按完整原文台词行分配台词。")
			} else if !stringSliceEqual(childDialogueLines, parentDialogueLines) {
				errors_ = append(errors_, "镜中分镜台词合并后与父分镜台词不一致，存在漏句、重复、乱序或改写。")
			}
		}
	}
	for _, asset := range assets {
		if strings.TrimSpace(strOf(asset, "name")) == "" {
			errors_ = append(errors_, "资产缺少名称。")
		}
	}
	return errors_
}

func dialogueLines(value any) []string {
	if list, ok := value.([]any); ok {
		var out []string
		for _, item := range list {
			out = append(out, dialogueLines(item)...)
		}
		return out
	}
	var out []string
	for _, line := range strings.Split(strAny(value, ""), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func intOrNone(value any) *int {
	switch t := value.(type) {
	case int:
		v := t
		return &v
	case int64:
		v := int(t)
		return &v
	case float64:
		v := int(t)
		return &v
	case string:
		var v int
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%d", &v); err == nil {
			return &v
		}
	}
	return nil
}

func floatOrNone(value any) *float64 {
	switch t := value.(type) {
	case int:
		v := float64(t)
		return &v
	case int64:
		v := float64(t)
		return &v
	case float64:
		v := t
		return &v
	case string:
		var v float64
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%g", &v); err == nil {
			return &v
		}
	}
	return nil
}

func absFloat64(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- 视图物化 ----

// taskGenerationAssetsWithoutBindingsTx 对齐 _task_generation_assets_without_bindings。
// 将确认资产清单物化为 Asset 行并返回资产列表。
func (r *Tasks) taskGenerationAssetsWithoutBindingsTx(ctx context.Context, tx pgx.Tx,
	project *model.Projects, episode *model.ProjectEpisodes, scriptVersionID, userID string) ([]*model.Assets, error) {
	inventoryItems, err := r.confirmedAssetInventoryTx(ctx, tx, project.ID, &episode.ID, &scriptVersionID, true)
	if err != nil {
		return nil, err
	}
	var taskAssets []map[string]any
	for _, item := range inventoryItems {
		taskAssets = append(taskAssets, item)
	}
	if len(taskAssets) == 0 {
		return nil, valueError("当前分集没有已确认的正式资产清单，不能生成资产任务。")
	}
	if _, err := r.materializeAssetsFromViewTx(ctx, tx, project, map[string]any{
		"assets":               taskAssets,
		"episode_id":           episode.ID,
		"script_version_id":    scriptVersionID,
		"asset_review_state":   map[string]any{"status": "confirmed"},
	}, userID); err != nil {
		return nil, err
	}
	assetCodes := map[string]bool{}
	for _, item := range taskAssets {
		if code := strings.TrimSpace(strOf(item, "asset_code")); code != "" {
			assetCodes[code] = true
		}
	}
	if len(assetCodes) == 0 {
		return []*model.Assets{}, nil
	}
	ids := make([]string, 0, len(assetCodes))
	for code := range assetCodes {
		ids = append(ids, code)
	}
	return r.listAssetsByCodesTx(ctx, tx, project.ID, ids)
}

func (r *Tasks) listAssetsByCodesTx(ctx context.Context, q queryer, projectID string,
	codes []string) ([]*model.Assets, error) {
	rows, err := q.Query(ctx, `SELECT `+assetColumns+` FROM assets
		WHERE project_id = $1 AND asset_code = ANY($2) ORDER BY asset_code`, projectID, codes)
	if err != nil {
		return nil, fmt.Errorf("list assets by codes: %w", err)
	}
	defer rows.Close()
	var out []*model.Assets
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, asset)
	}
	return out, rows.Err()
}

// materializeAssetsFromViewTx 对齐 _materialize_assets_from_view。返回新增资产行数。
func (r *Tasks) materializeAssetsFromViewTx(ctx context.Context, tx pgx.Tx,
	project *model.Projects, view map[string]any, userID string) (int, error) {
	created := 0
	now := time.Now().UTC()
	scopeKey := breakdownAssetScopeKey(view)
	reviewState := map[string]any{}
	if m, ok := view["asset_review_state"].(map[string]any); ok {
		reviewState = m
	}
	inventoryConfirmed := strOf(reviewState, "status") == "confirmed"
	items := mapList(view["assets"])
	for index, item := range items {
		assetType := normalizeAssetType(strAny(firstNonNil(item["asset_type"], item["type"]), ""))
		assetCode := strAny(firstNonNil(item["asset_code"], assetItemCode(item)), "")
		name := strAny(item["name"], fmt.Sprintf("资产 %03d", index+1))
		lifecycleStatus := "confirmed"
		if !inventoryConfirmed {
			lifecycleStatus = assetLifecycleStatus(item)
		}
		itemMetadata := normalizedAssetMetadata(item)
		itemMetadata["confirmation_status"] = lifecycleStatus
		if scopeKey != "" {
			keys := stringSet(itemMetadata["breakdown_scope_keys"])
			keys[scopeKey] = true
			sortedKeys := sortedStringSet(keys)
			if len(sortedKeys) > 0 {
				itemMetadata["breakdown_scope_keys"] = sortedKeys
			}
		}
		if _, ok := itemMetadata["prompt_input_fingerprint"]; !ok {
			itemMetadata["prompt_input_fingerprint"] = ContentHashString(assetPromptInputProjection(item))
		}
		asset, err := scanAsset(tx.QueryRow(ctx,
			`SELECT `+assetColumns+` FROM assets WHERE project_id = $1 AND asset_code = $2`,
			project.ID, assetCode))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return created, fmt.Errorf("find materialized asset: %w", err)
		}
		if asset == nil {
			tags := jsonB([]string{assetType})
			description := ptrToStr(assetDescription(item))
			baseModel := "Seedream"
			if v := strAny(item["model"], ""); v != "" {
				baseModel = v
			}
			asset = &model.Assets{
				ID:           newUUIDString(),
				ProjectId:    &project.ID,
				AssetCode:    &assetCode,
				AssetType:    assetType,
				Name:         name,
				Description:  description,
				Status:       lifecycleStatus,
				Tags:         tags,
				BaseModel:    &baseModel,
				MetadataJson: jsonB(itemMetadata),
				CreatedById:  &userID,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			_, err := tx.Exec(ctx, `INSERT INTO assets (id, project_id, asset_code, asset_type, name, description,
				status, tags, base_model, metadata_json, version, created_by_id, is_locked, created_at, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,0,$11,FALSE,$12,$13)`,
				asset.ID, asset.ProjectId, asset.AssetCode, asset.AssetType, asset.Name, asset.Description,
				asset.Status, asset.Tags, asset.BaseModel, asset.MetadataJson, asset.CreatedById,
				asset.CreatedAt, asset.UpdatedAt)
			if err != nil {
				return created, fmt.Errorf("insert materialized asset: %w", err)
			}
			created++
		} else if !asset.IsLocked {
			existingMetadata := metadataWithoutBreakdownAsset(metadataAsMap(asset.MetadataJson))
			if scopeKey != "" {
				keys := stringSet(existingMetadata["breakdown_scope_keys"])
				for k := range stringSet(itemMetadata["breakdown_scope_keys"]) {
					keys[k] = true
				}
				keys[scopeKey] = true
				sortedKeys := sortedStringSet(keys)
				if len(sortedKeys) > 0 {
					itemMetadata["breakdown_scope_keys"] = sortedKeys
				}
			}
			asset.Name = name
			asset.Description = ptrToStr(assetDescription(item))
			baseModel := "Seedream"
			if v := strAny(item["model"], ""); v != "" {
				baseModel = v
			} else if asset.BaseModel != nil {
				baseModel = *asset.BaseModel
			}
			asset.BaseModel = &baseModel
			merged := map[string]any{}
			for k, v := range existingMetadata {
				merged[k] = v
			}
			for k, v := range itemMetadata {
				merged[k] = v
			}
			asset.MetadataJson = jsonB(merged)
		}
		if !asset.IsLocked {
			r.setAssetRowLifecycle(asset, lifecycleStatus,
				strPtrOrNone(strOr(itemMetadata, "prompt_validity")),
				strPtrOrNone(strOr(itemMetadata, "prompt_input_fingerprint")),
				strPtrOrNone(strOr(itemMetadata, "prompt_generated_for_fingerprint")),
				anyStringList(itemMetadata["prompt_invalidated_fields"]), now)
		}
		if !asset.IsLocked {
			if _, err := r.appendAssetRevisionTx(ctx, tx, asset, "human", &userID, now); err != nil {
				return created, err
			}
			if err := r.persistMaterializedAssetTx(ctx, tx, asset); err != nil {
				return created, err
			}
		}
	}
	return created, nil
}

func (r *Tasks) persistMaterializedAssetTx(ctx context.Context, tx pgx.Tx, asset *model.Assets) error {
	_, err := tx.Exec(ctx, `UPDATE assets SET name=$2, description=$3, status=$4, base_model=$5,
		metadata_json=$6, version=$7, current_revision_id=$8, updated_at=$9 WHERE id=$1`,
		asset.ID, asset.Name, asset.Description, asset.Status, asset.BaseModel,
		asset.MetadataJson, asset.Version, asset.CurrentRevisionId, asset.UpdatedAt)
	if err != nil {
		return fmt.Errorf("persist materialized asset: %w", err)
	}
	return nil
}

func (r *Tasks) setAssetRowLifecycle(asset *model.Assets, status string, promptValidity *string,
	promptInputFingerprint *string, promptGeneratedForFingerprint *string,
	invalidatedFields []string, now time.Time) {
	if !assetLifecycleStateSet[status] {
		asset.Status = status
		return
	}
	metadata := metadataAsMap(asset.MetadataJson)
	metadata["confirmation_status"] = status
	if promptValidity != nil {
		metadata["prompt_validity"] = *promptValidity
	}
	if promptInputFingerprint != nil && *promptInputFingerprint != "" {
		metadata["prompt_input_fingerprint"] = *promptInputFingerprint
	}
	if promptGeneratedForFingerprint != nil && *promptGeneratedForFingerprint != "" {
		metadata["prompt_generated_for_fingerprint"] = *promptGeneratedForFingerprint
	}
	if len(invalidatedFields) > 0 {
		metadata["prompt_invalidated_at"] = now.UTC().Format(time.RFC3339)
		metadata["prompt_invalidated_fields"] = sortedUnique(invalidatedFields)
	} else if promptValidity != nil && *promptValidity == "current" {
		delete(metadata, "prompt_invalidated_at")
		delete(metadata, "prompt_invalidated_fields")
	}
	asset.MetadataJson = jsonB(metadata)
	asset.Status = status
	asset.UpdatedAt = now.UTC()
}

// appendAssetRevisionTx 对齐 _append_asset_revision（数据状态 normalized）。
// 内容未变时复用当前修订；资产行 current_revision_id/version 在调用方事务内更新。
func (r *Tasks) appendAssetRevisionTx(ctx context.Context, tx pgx.Tx, asset *model.Assets,
	sourceType string, createdBy *string, now time.Time) (*model.ArtifactRevisions, error) {
	content := assetRevisionContent(asset)
	digest := ContentHashString(content)
	var current *model.ArtifactRevisions
	if asset.CurrentRevisionId != nil {
		rev, err := r.getArtifactRevisionByIDTx(ctx, tx, *asset.CurrentRevisionId)
		if err != nil {
			return nil, err
		}
		current = rev
	}
	if current != nil && current.ContentHash == digest {
		return current, nil
	}
	versionNo := r.nextAssetRevisionVersionTx(ctx, tx, asset.ID)
	var prevHash any
	if current != nil {
		prevHash = current.ContentHash
	}
	revisionID := newUUIDString()
	revision := &model.ArtifactRevisions{
		ID:                revisionID,
		ProjectId:         strAny(asset.ProjectId, ""),
		ArtifactType:      "asset",
		ArtifactId:        asset.ID,
		VersionNo:         int32(versionNo),
		ParentRevisionId:  asset.CurrentRevisionId,
		SourceType:        sourceType,
		NormalizedContent: jsonB(content),
		ChangeDiff:        jsonB(map[string]any{"previous_content_hash": prevHash}),
		ContentHash:       digest,
		Status:            assetConfirmationStatus(asset),
		CreatedBy:         createdBy,
		DataState:         "normalized",
		CreatedAt:         now.UTC(),
		UpdatedAt:         now.UTC(),
	}
	_, err := tx.Exec(ctx, `INSERT INTO artifact_revisions (
		id, project_id, artifact_type, artifact_id, version_no, parent_revision_id, source_type,
		input_snapshot, raw_output, normalized_content, change_diff, content_hash, status,
		created_by, data_state, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'{}','{}',$8,$9,$10,$11,$12,$13,$14,$15)`,
		revision.ID, revision.ProjectId, revision.ArtifactType, revision.ArtifactId, revision.VersionNo,
		revision.ParentRevisionId, revision.SourceType, revision.NormalizedContent, revision.ChangeDiff,
		revision.ContentHash, revision.Status, revision.CreatedBy, revision.DataState,
		revision.CreatedAt, revision.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert asset revision: %w", err)
	}
	asset.CurrentRevisionId = &revisionID
	if int32(versionNo) > asset.Version {
		asset.Version = int32(versionNo)
	}
	asset.UpdatedAt = now.UTC()
	if revision.Status == "confirmed" {
		if err := r.insertAssetConfirmedTrainingSampleTx(ctx, tx, asset, current, content, versionNo, sourceType, createdBy, now); err != nil {
			return nil, err
		}
	}
	return revision, nil
}

func (r *Tasks) nextAssetRevisionVersionTx(ctx context.Context, q queryer, assetID string) int {
	var n *int32
	err := q.QueryRow(ctx, `SELECT MAX(version_no) FROM artifact_revisions
		WHERE artifact_type = 'asset' AND artifact_id = $1`, assetID).Scan(&n)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 1
	}
	if n == nil {
		return 1
	}
	return int(*n) + 1
}

func (r *Tasks) insertAssetConfirmedTrainingSampleTx(ctx context.Context, tx pgx.Tx,
	asset *model.Assets, current *model.ArtifactRevisions, content map[string]any, versionNo int,
	sourceType string, createdBy *string, now time.Time) error {
	inputJSON := map[string]any{}
	agentOutputJSON := map[string]any{}
	if current != nil {
		inputJSON = metadataAsMap(current.InputSnapshot)
		agentOutputJSON = metadataAsMap(current.NormalizedContent)
	}
	entityCode := asset.ID
	if asset.AssetCode != nil {
		entityCode = *asset.AssetCode
	}
	lineage := fmt.Sprintf("p:%s|a:%s|s:asset_extract|k:asset-extract",
		strAny(asset.ProjectId, ""), entityCode)
	_, err := tx.Exec(ctx, `INSERT INTO agent_training_samples (
		id, project_id, agent_type, sample_type, entity_type, entity_code, input_json,
		agent_output_json, human_modified_output_json, confirmed_output_json, change_summary,
		training_tags, training_ready, created_by, data_state, lineage_key, created_at, updated_at)
		VALUES ($1,$2,NULL,'asset_revision_confirmed','asset',$3,$4,$5,$6,$7,$8,$9,TRUE,$10,'final',$11,$12,$12)`,
		newUUIDString(), strAny(asset.ProjectId, ""), entityCode,
		jsonB(inputJSON), jsonB(agentOutputJSON), jsonB(content), jsonB(content),
		jsonB([]string{fmt.Sprintf("资产修订 v%d 已人工确认", versionNo)}),
		jsonB([]string{"asset_revision", "human_confirmed", sourceType}),
		createdBy, lineage, now)
	if err != nil {
		return fmt.Errorf("insert asset confirmed training sample: %w", err)
	}
	return nil
}

func assetRevisionContent(asset *model.Assets) map[string]any {
	return map[string]any{
		"asset_code":  asset.AssetCode,
		"asset_type":  asset.AssetType,
		"name":        asset.Name,
		"description": asset.Description,
		"prompt_text": asset.PromptText,
		"metadata":    metadataAsMap(asset.MetadataJson),
	}
}

// breakdownAssetScopeKey 对齐 _breakdown_asset_scope_key。
func breakdownAssetScopeKey(content map[string]any) string {
	episodeID := strings.TrimSpace(strOf(content, "episode_id"))
	scriptVersionID := strings.TrimSpace(strOf(content, "script_version_id"))
	if episodeID == "" || scriptVersionID == "" {
		return ""
	}
	return fmt.Sprintf("episode:%s:script-version:%s", episodeID, scriptVersionID)
}

// normalizedAssetMetadata 对齐 _normalized_asset_metadata。
func normalizedAssetMetadata(item map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range assetBreakdownFields {
		if value, ok := item[key]; ok && value != nil {
			out[key] = value
		}
	}
	return out
}

func metadataWithoutBreakdownAsset(metadata map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range metadata {
		if k != "breakdown_asset" {
			out[k] = v
		}
	}
	return out
}

func assetDescription(item map[string]any) string {
	attributes := map[string]any{}
	if a, ok := item["attributes"].(map[string]any); ok {
		attributes = a
	}
	var parts []string
	for _, key := range []string{"description"} {
		if value := strOf(item, key); value != "" {
			parts = append(parts, value)
		}
	}
	for _, key := range []string{"appearance", "visual_goal", "scene_description", "core_requirement"} {
		if value := strOf(attributes, key); value != "" {
			parts = append(parts, value)
		}
	}
	text := strings.Join(parts, "\n")
	if runes := []rune(text); len(runes) > 2000 {
		return string(runes[:2000])
	}
	return text
}

// assetPromptInputProjection 对齐 _asset_prompt_input_projection（fingerprint 投影）。
func assetPromptInputProjection(asset map[string]any) map[string]any {
	sanitized, _ := sanitizePromptProjection(asset).(map[string]any)
	return sanitized
}

func sanitizePromptProjection(value any) any {
	switch t := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := map[string]any{}
		for _, k := range keys {
			if promptNonInputFields[k] {
				continue
			}
			nested := sanitizePromptProjection(t[k])
			if n, ok := nested.(map[string]any); ok {
				if len(n) == 0 {
					continue
				}
			} else if l, ok := nested.([]any); ok {
				if len(l) == 0 {
					continue
				}
			} else if s, ok := nested.(string); ok && s == "" {
				continue
			} else if nested == nil {
				continue
			}
			out[k] = nested
		}
		return out
	case []any:
		var out []any
		for _, item := range t {
			nested := sanitizePromptProjection(item)
			if n, ok := nested.(map[string]any); ok {
				if len(n) == 0 {
					continue
				}
			} else if l, ok := nested.([]any); ok {
				if len(l) == 0 {
					continue
				}
			} else if s, ok := nested.(string); ok && s == "" {
				continue
			} else if nested == nil {
				continue
			}
			out = append(out, nested)
		}
		return out
	default:
		return value
	}
}

func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			if s := fmt.Sprintf("%v", item); strings.TrimSpace(s) != "" {
				out[strings.TrimSpace(s)] = true
			}
		}
	case []string:
		for _, s := range t {
			if strings.TrimSpace(s) != "" {
				out[strings.TrimSpace(s)] = true
			}
		}
	}
	return out
}

func sortedStringSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			seen[s] = true
		}
	}
	return sortedStringSet(seen)
}

func anyStringList(v any) []string {
	var out []string
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			out = append(out, fmt.Sprintf("%v", item))
		}
	case []string:
		out = append(out, t...)
	}
	return out
}

func strPtrOrNone(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strOr(m map[string]any, key string) string {
	return strOf(m, key)
}

// visualAssetTypeKeys 返回视觉资产类型集合（对齐 _VISUAL_ASSET_TYPES）作为 ANY 查询参数。
func visualAssetTypeKeys() []string {
	out := make([]string, 0, len(visualAssetTypes))
	for t := range visualAssetTypes {
		out = append(out, t)
	}
	return out
}

// ---- breakdown 视图（事务内） ----

// latestScopedBreakdownContentTx 取事务内最新且命作用域的 breakdown content map；
// 未命中作用域返回 nil。
func (r *Tasks) latestScopedBreakdownContentTx(ctx context.Context, tx pgx.Tx,
	projectID, episodeID, scriptVersionID string) map[string]any {
	content := r.latestBreakdownContentLineageTx(ctx, tx, projectID, episodeID, scriptVersionID)
	if content == nil {
		return nil
	}
	return content
}

func (r *Tasks) latestBreakdownContentLineageTx(ctx context.Context, tx pgx.Tx,
	projectID, episodeID, scriptVersionID string) map[string]any {
	rows, err := tx.Query(ctx, `SELECT content_json FROM script_breakdowns
		WHERE project_id = $1 ORDER BY version DESC, created_at DESC LIMIT 100`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var content json.RawMessage
		if err := rows.Scan(&content); err != nil {
			return nil
		}
		if breakdownContentMatchesLineage(content, &episodeID, &scriptVersionID) {
			return metadataAsMap(content)
		}
	}
	return nil
}

// latestBreakdownTxFieldless 取事务内最新 breakdown 原始行（无条件 lineage），供小说视图校验。
func (r *Tasks) latestBreakdownContentTx(ctx context.Context, tx pgx.Tx, projectID string) map[string]any {
	rows, err := tx.Query(ctx, `SELECT content_json FROM script_breakdowns
		WHERE project_id = $1 ORDER BY version DESC, created_at DESC LIMIT 1`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	if rows.Next() {
		var content json.RawMessage
		if err := rows.Scan(&content); err != nil {
			return nil
		}
		return metadataAsMap(content)
	}
	return nil
}

// dictCopy 返回 dict 的浅拷贝，并按顺序并入后续 dict 的键。
func dictCopy(m map[string]any, extras ...map[string]any) map[string]any {
	out := make(map[string]any, len(m)+len(extras))
	for k, v := range m {
		out[k] = v
	}
	for _, extra := range extras {
		for k, v := range extra {
			out[k] = v
		}
	}
	return out
}

// anySlice 将 any 值安全转为 []any；非切片时返回 nil（对齐返回 None）。
func anySlice(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return nil
}
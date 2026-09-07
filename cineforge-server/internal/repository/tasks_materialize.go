package repository

// P3e task 域：AssetVersion 物化 / 回收 / 字符依赖作废 / 资产完成状态 / 解锁通知。
// 对齐 legacy repositories.py 8907-9072 (_materialize_approved_asset_versions)、
// 8719-8794 (_revoke_materialized_asset_versions)、8607-8716
// (_invalidate_character_dependent_views)、9080-9090 (_refresh_asset_completion_status)、
// 9093-9143 (_notify_unlocked_dependents) 及上下文编码助手
// (_asset_context_version / _asset_context_variant / _character_view_code / _library_code)。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

var librarySepRegexp = regexp.MustCompile(`[^A-Za-z0-9]+`)

// ---- asset / asset_version 行 IO ----

const assetColumns = `id, project_id, asset_code, asset_type, name, description, status, tags,
	preview_path, file_path, prompt_text, base_model, metadata_json, version, current_version_id,
	current_revision_id, is_locked, locked_by, locked_at, created_by_id, created_at, updated_at`

func scanAsset(row pgx.Row) (*model.Assets, error) {
	var a model.Assets
	err := row.Scan(&a.ID, &a.ProjectId, &a.AssetCode, &a.AssetType, &a.Name, &a.Description,
		&a.Status, &a.Tags, &a.PreviewPath, &a.FilePath, &a.PromptText, &a.BaseModel,
		&a.MetadataJson, &a.Version, &a.CurrentVersionId, &a.CurrentRevisionId, &a.IsLocked,
		&a.LockedBy, &a.LockedAt, &a.CreatedById, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

const assetVersionColumns = `id, asset_id, version_no, asset_version_code, file_id, preview_file_id,
	prompt_text, negative_prompt_text, base_model, tool_name, metadata_json, is_current,
	source_task_id, source_agent_run_id, created_by, source_submission_id,
	source_master_submission_id, is_invalidated, invalidated_at, invalidated_by_id,
	invalidated_reason, invalidated_source_id, purged_at, created_at, updated_at`

func scanAssetVersion(row pgx.Row) (*model.AssetVersions, error) {
	var v model.AssetVersions
	err := row.Scan(&v.ID, &v.AssetId, &v.VersionNo, &v.AssetVersionCode, &v.FileId,
		&v.PreviewFileId, &v.PromptText, &v.NegativePromptText, &v.BaseModel, &v.ToolName,
		&v.MetadataJson, &v.IsCurrent, &v.SourceTaskId, &v.SourceAgentRunId, &v.CreatedBy,
		&v.SourceSubmissionId, &v.SourceMasterSubmissionId, &v.IsInvalidated, &v.InvalidatedAt,
		&v.InvalidatedById, &v.InvalidatedReason, &v.InvalidatedSourceId, &v.PurgedAt,
		&v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *Tasks) getAssetByID(ctx context.Context, q queryer, id string) (*model.Assets, error) {
	a, err := scanAsset(q.QueryRow(ctx, `SELECT `+assetColumns+` FROM assets WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get asset: %w", err)
	}
	return a, nil
}

func (r *Tasks) getAssetForUpdate(ctx context.Context, tx pgx.Tx, id string) (*model.Assets, error) {
	a, err := scanAsset(tx.QueryRow(ctx, `SELECT `+assetColumns+` FROM assets WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get asset for update: %w", err)
	}
	return a, nil
}

func (r *Tasks) listAssetVersionsByAsset(ctx context.Context, q queryer, assetID string) ([]*model.AssetVersions, error) {
	rows, err := q.Query(ctx,
		`SELECT `+assetVersionColumns+` FROM asset_versions WHERE asset_id = $1 ORDER BY version_no`, assetID)
	if err != nil {
		return nil, fmt.Errorf("list asset versions: %w", err)
	}
	defer rows.Close()
	var out []*model.AssetVersions
	for rows.Next() {
		v, err := scanAssetVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- 编码助手（对齐 _library_code / _character_view_code / _asset_context_variant） ----

// libraryCode 对齐 _library_code：非字母数字折叠为 "-"，去头尾 "-"，空则取 fallback。
func libraryCode(value string, fallback string) string {
	code := librarySepRegexp.ReplaceAllString(strings.ToUpper(value), "-")
	code = strings.Trim(code, "-")
	if code == "" {
		return fallback
	}
	return code
}

// strAny 对齐 str(value or fallback)：nil/空串 → fallback。
func strAny(v any, fallback string) string {
	if v == nil {
		return fallback
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return fallback
		}
		return t
	case *string:
		if t == nil || *t == "" {
			return fallback
		}
		return *t
	default:
		s := fmt.Sprintf("%v", t)
		if s == "" {
			return fallback
		}
		return s
	}
}

// safeInt 对齐 _safe_int。
func safeInt(v any, fallback int32) int32 {
	switch t := v.(type) {
	case nil:
		return fallback
	case int:
		return int32(t)
	case int32:
		return t
	case int64:
		return int32(t)
	case float64:
		return int32(t)
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 32); err == nil {
			return int32(n)
		}
	}
	return fallback
}

func firstNonNil(a, b any) any {
	if a != nil {
		return a
	}
	return b
}

// characterViewCode 对齐 _character_view_code（MASTER/A → "A"）。
func characterViewCode(v any) string {
	view := libraryCode(strAny(v, "A"), "A")
	if view == "A" || view == "MASTER" {
		return "A"
	}
	return view
}

// assetContextVariant 对齐 _asset_context_variant（A/MASTER → "MASTER"）。
func assetContextVariant(v any) string {
	variant := strings.ToUpper(strings.TrimSpace(strAny(v, "MASTER")))
	if variant == "A" || variant == "MASTER" {
		return "MASTER"
	}
	return variant
}

// assetContextVersion 对齐 _asset_context_version；返回 (version_no, context_key|nil)。
// context key 用 "\x1f" 连接三元组（library 编码不含该字符）。
func assetContextVersion(assetType string, md map[string]any, fallbackVersionNo int32,
	counters map[string]int32) (int32, *string) {
	if assetType != assetTypeCharacter {
		if fallbackVersionNo < 1 {
			return 1, nil
		}
		return fallbackVersionNo, nil
	}
	key := strings.Join([]string{
		libraryCode(strAny(md["age_stage_code"], "BASE"), "BASE"),
		libraryCode(strAny(md["costume_variant_code"], "BASE"), "BASE"),
		characterViewCode(md["task_variant"]),
	}, "\x1f")
	explicit := safeInt(firstNonNil(md["context_version_no"], md["batch_version_no"]), 0)
	if explicit > 0 {
		if explicit > counters[key] {
			counters[key] = explicit
		}
		return explicit, &key
	}
	counters[key]++
	return counters[key], &key
}

// metadataAsMap 解析 metadata_json 为 map。
func metadataAsMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func strSlicesEqual(a, b []string) bool {
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

// materializeFactor 判定任务类型是否符合物化条件。
func materializeFactor(t *model.Tasks) bool {
	return t.TaskType == taskTypeAsset || t.TaskType == taskTypeTextToImage || t.TaskType == taskTypeAudio
}

// taskVariantsIn master 判定的任务变体（A 或 MASTER）。
func isCharMasterVariant(t *model.Tasks) bool {
	v := strings.ToUpper(strings.TrimSpace(ptrOr(t.TaskVariant, "MASTER")))
	return v == "A" || v == "MASTER"
}

// ---- _materialize_approved_asset_versions（repositories.py:8907-9072） ----

// materializeApprovedAssetVersions 将选中定版成果物化为正式 AssetVersion。
func (r *Tasks) materializeApprovedAssetVersions(ctx context.Context, tx pgx.Tx, task *model.Tasks,
	submissions []*model.Submissions, createdBy *string) error {
	if task.AssetId == nil || !materializeFactor(task) {
		return nil
	}
	selected := []*model.Submissions{}
	for _, item := range submissions {
		if item == nil {
			continue
		}
		if item.IsSelected && !item.IsArchived && !item.IsInvalidated &&
			approvedSubmissionStatuses[item.Status] {
			selected = append(selected, item)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	asset, err := r.getAssetForUpdate(ctx, tx, *task.AssetId)
	if err != nil {
		return err
	}
	if asset == nil {
		return nil
	}
	existing, err := r.listAssetVersionsByAsset(ctx, tx, asset.ID)
	if err != nil {
		return err
	}
	bySubmissionID := map[string]*model.AssetVersions{}
	for _, v := range existing {
		md := metadataAsMap(v.MetadataJson)
		if sid, ok := md["submission_id"].(string); ok && sid != "" {
			bySubmissionID[sid] = v
		}
	}
	versionNo := int32(0)
	for _, v := range existing {
		if v.VersionNo > versionNo {
			versionNo = v.VersionNo
		}
	}
	contextCounters := map[string]int32{}
	if asset.AssetType == assetTypeCharacter {
		for _, previous := range existing {
			previousMetadata := metadataAsMap(previous.MetadataJson)
			previousContextVersion, _ := assetContextVersion(asset.AssetType, previousMetadata,
				previous.VersionNo, contextCounters)
			previousMetadata["context_version_no"] = previousContextVersion
			previous.MetadataJson, _ = json.Marshal(previousMetadata)
			previous.UpdatedAt = time.Now().UTC()
			if err := persistAssetVersion(ctx, tx, previous); err != nil {
				return err
			}
		}
	}
	contextKey := []string{
		ptrOr(task.AgeStageCode, ""),
		ptrOr(task.CostumeVariantCode, ""),
		assetContextVariant(ptrOr(task.TaskVariant, "")),
	}
	batchVersions := map[string]int32{}
	for _, item := range selected {
		if item.BatchId == nil {
			continue
		}
		if _, ok := batchVersions[*item.BatchId]; ok {
			continue
		}
		b, err := r.getBatchByID(ctx, tx, *item.BatchId)
		if err != nil {
			return err
		}
		if b != nil {
			batchVersions[*item.BatchId] = b.VersionNo
		}
	}
	var cardMaster *model.AssetVersions
	for _, submission := range selected {
		if submission.IsPrimary {
			for _, previous := range existing {
				pmd := metadataAsMap(previous.MetadataJson)
				pc := []string{
					strAny(pmd["age_stage_code"], ""),
					strAny(pmd["costume_variant_code"], ""),
					assetContextVariant(pmd["task_variant"]),
				}
				if strSlicesEqual(pc, contextKey) {
					previous.IsCurrent = false
					previous.UpdatedAt = time.Now().UTC()
					if err := persistAssetVersion(ctx, tx, previous); err != nil {
						return err
					}
				}
			}
		}
		item := bySubmissionID[submission.ID]
		var contextVersionNo int32
		if item != nil {
			contextVersionNo = safeInt(metadataAsMap(item.MetadataJson)["context_version_no"], 0)
		} else {
			var batchVersionNo any
			if submission.BatchId != nil {
				if n, ok := batchVersions[*submission.BatchId]; ok {
					batchVersionNo = n
				}
			}
			ctxNo, _ := assetContextVersion(asset.AssetType, map[string]any{
				"task_variant":        strAny(task.TaskVariant, ""),
				"age_stage_code":      strAny(task.AgeStageCode, ""),
				"costume_variant_code": strAny(task.CostumeVariantCode, ""),
				"batch_version_no":    batchVersionNo,
			}, versionNo+1, contextCounters)
			contextVersionNo = ctxNo
		}
		var contextVersionValue any
		if asset.AssetType == assetTypeCharacter {
			contextVersionValue = contextVersionNo
		}
		metadata := map[string]any{
			"submission_id":          submission.ID,
			"submission_status":      submission.Status,
			"is_primary":             submission.IsPrimary,
			"view_label":             submission.ViewLabel,
			"state_label":            submission.StateLabel,
			"submission_description": submission.Description,
			"task_variant":           task.TaskVariant,
			"age_stage_code":         task.AgeStageCode,
			"costume_variant_code":   task.CostumeVariantCode,
			"step":                   submission.Step,
			"file_type":              submission.FileType,
			"batch_id":               submission.BatchId,
			"batch_version_no": func() any {
				if submission.BatchId == nil {
					return nil
				}
				return batchVersions[*submission.BatchId]
			}(),
			"task_id":            task.ID,
			"task_type":          task.TaskType,
			"media_type":         task.MediaType,
			"context_version_no": contextVersionValue,
		}
		metadataJSON, _ := json.Marshal(metadata)

		var sourceMaster *string
		if asset.AssetType == assetTypeCharacter && isCharMasterVariant(task) {
			sourceMaster = &submission.ID
		} else {
			sourceMaster = submission.SourceMasterSubmissionId
		}

		if item == nil {
			versionNo++
			baseCode := libraryCode(ptrOr(asset.AssetCode, ""), assetTypeDisplayCodes[asset.AssetType])
			if baseCode == "" {
				baseCode = "ASSET"
			}
			var previewFileID *string
			if submission.FileType == "image" {
				previewFileID = submission.FileId
			}
			promptText := submission.RevisedPromptText
			if promptText == nil {
				promptText = submission.PromptText
			}
			if promptText == nil {
				promptText = task.LatestPromptText
			}
			baseModel := submission.ModelName
			if baseModel == nil {
				baseModel = task.ProductionModel
			}
			var toolName *string
			var tools []string
			_ = json.Unmarshal(submission.ToolNames, &tools)
			if len(tools) > 0 {
				toolName = ptr(strings.Join(tools, ", "))
			}
			item = &model.AssetVersions{
				ID:                       newUUIDString(),
				AssetId:                  asset.ID,
				VersionNo:                versionNo,
				AssetVersionCode:         fmt.Sprintf("%s-V%03d", baseCode, versionNo),
				FileId:                   submission.FileId,
				PreviewFileId:            previewFileID,
				PromptText:               promptText,
				BaseModel:                baseModel,
				ToolName:                 toolName,
				MetadataJson:             metadataJSON,
				IsCurrent:                submission.IsPrimary,
				SourceTaskId:             &task.ID,
				CreatedBy:                createdBy,
				SourceSubmissionId:       &submission.ID,
				SourceMasterSubmissionId: sourceMaster,
			}
			if err := insertAssetVersion(ctx, tx, item); err != nil {
				return err
			}
			existing = append(existing, item)
			bySubmissionID[submission.ID] = item
		} else {
			item.FileId = submission.FileId
			if submission.FileType == "image" {
				item.PreviewFileId = submission.FileId
			}
			item.MetadataJson = metadataJSON
			item.IsCurrent = submission.IsPrimary
			item.SourceSubmissionId = &submission.ID
			item.SourceMasterSubmissionId = sourceMaster
			item.IsInvalidated = false
			item.InvalidatedAt = nil
			item.InvalidatedById = nil
			item.InvalidatedReason = nil
			item.InvalidatedSourceId = nil
			item.UpdatedAt = time.Now().UTC()
			if err := persistAssetVersion(ctx, tx, item); err != nil {
				return err
			}
		}
		isCharacterCardMaster := asset.AssetType == assetTypeCharacter &&
			isCharMasterVariant(task) && submission.IsPrimary
		isAudioCardMaster := task.TaskType == taskTypeAudio && submission.IsPrimary
		isScenePropPreview := cardMaster == nil &&
			(asset.AssetType == assetTypeScene || asset.AssetType == assetTypeProp) &&
			submission.IsSelected
		if isCharacterCardMaster || isAudioCardMaster || isScenePropPreview {
			cardMaster = item
			asset.FilePath = &submission.FilePath
			if submission.FileType == "image" {
				asset.PreviewPath = &submission.FilePath
			}
		}
	}
	if cardMaster != nil {
		asset.CurrentVersionId = &cardMaster.ID
	}
	if versionNo > asset.Version {
		asset.Version = versionNo
	}
	asset.UpdatedAt = time.Now().UTC()
	return persistAsset(ctx, tx, asset)
}

// ---- _revoke_materialized_asset_versions（repositories.py:8719-8794） ----

// revokeMaterializedAssetVersions 回收正式资产投影（保留审批历史）。
func (r *Tasks) revokeMaterializedAssetVersions(ctx context.Context, tx pgx.Tx, task *model.Tasks,
	batch *model.TaskSubmissionBatches, batchSubmissions []*model.Submissions,
	revokedBy *string, revokedAt time.Time, reason *string) error {
	if task.AssetId == nil || !materializeFactor(task) {
		return nil
	}
	submissionIDs := map[string]bool{}
	for _, item := range batchSubmissions {
		if item != nil {
			submissionIDs[item.ID] = true
		}
	}
	versions, err := r.listAssetVersionsByAsset(ctx, tx, *task.AssetId)
	if err != nil {
		return err
	}
	revoked := []*model.AssetVersions{}
	for _, version := range versions {
		md := metadataAsMap(version.MetadataJson)
		batchMatch := fmt.Sprintf("%v", md["batch_id"]) == batch.ID
		submissionMatch := submissionIDs[fmt.Sprintf("%v", md["submission_id"])]
		if !batchMatch && !submissionMatch {
			continue
		}
		md["submission_status"] = "rejected"
		md["is_primary"] = false
		md["approval_revoked_at"] = revokedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		md["approval_revoked_by"] = func() any {
			if revokedBy == nil {
				return nil
			}
			return *revokedBy
		}()
		md["approval_revoked_reason"] = func() any {
			if reason == nil {
				return nil
			}
			return *reason
		}()
		version.MetadataJson, _ = json.Marshal(md)
		version.IsCurrent = false
		version.UpdatedAt = revokedAt
		revoked = append(revoked, version)
	}
	if len(revoked) == 0 {
		return nil
	}
	for _, version := range revoked {
		if err := persistAssetVersion(ctx, tx, version); err != nil {
			return err
		}
	}
	asset, err := r.getAssetForUpdate(ctx, tx, *task.AssetId)
	if err != nil {
		return err
	}
	if asset == nil {
		return nil
	}
	revokedIDs := map[string]bool{}
	for _, version := range revoked {
		revokedIDs[version.ID] = true
	}
	if asset.CurrentVersionId == nil || !revokedIDs[*asset.CurrentVersionId] {
		return nil
	}
	// 重新物化未被作废的仍过审 selected 成果作为卡面回退。
	remaining, err := r.listStillApprovedSubmissionsForAsset(ctx, tx, *task.AssetId)
	if err != nil {
		return err
	}
	remainingByID := map[string]*model.Submissions{}
	for _, sub := range remaining {
		remainingByID[sub.ID] = sub
	}
	var fallback *model.AssetVersions
	for _, version := range versions {
		if version.IsInvalidated {
			continue
		}
		md := metadataAsMap(version.MetadataJson)
		sid := fmt.Sprintf("%v", md["submission_id"])
		if remainingByID[sid] == nil {
			continue
		}
		if fallback == nil {
			fallback = version
			continue
		}
		fallbackPrimary := remainingByID[fmt.Sprintf("%v", metadataAsMap(fallback.MetadataJson)["submission_id"])].IsPrimary
		curPrimary := remainingByID[sid].IsPrimary
		// 对齐 max(... key=(submission.is_primary, version_no))：primary 优先，其次版本号。
		if (curPrimary && !fallbackPrimary) ||
			(fallbackPrimary == curPrimary && version.VersionNo > fallback.VersionNo) {
			fallback = version
		}
	}
	if fallback == nil {
		asset.CurrentVersionId = nil
		asset.FilePath = nil
		asset.PreviewPath = nil
		asset.UpdatedAt = revokedAt
		return persistAsset(ctx, tx, asset)
	}
	fallbackSub := remainingByID[fmt.Sprintf("%v", metadataAsMap(fallback.MetadataJson)["submission_id"])]
	fallback.IsCurrent = true
	fallback.UpdatedAt = revokedAt
	if err := persistAssetVersion(ctx, tx, fallback); err != nil {
		return err
	}
	asset.CurrentVersionId = &fallback.ID
	asset.FilePath = &fallbackSub.FilePath
	if fallbackSub.FileType == "image" {
		asset.PreviewPath = &fallbackSub.FilePath
	} else {
		asset.PreviewPath = nil
	}
	asset.UpdatedAt = revokedAt
	return persistAsset(ctx, tx, asset)
}

// listStillApprovedSubmissionsForAsset 对齐 revoke 中的 remaining_submissions 查询。
func (r *Tasks) listStillApprovedSubmissionsForAsset(ctx context.Context, q queryer, assetID string) ([]*model.Submissions, error) {
	rows, err := q.Query(ctx,
		`SELECT `+submissionColumnsPrefixed+` FROM submissions s
		 JOIN tasks t ON t.id = s.task_id
		 WHERE t.asset_id = $1 AND t.is_retired = false
		   AND s.status IN ('`+submissionStatusPrimaryMaster+`','`+submissionStatusAlternateMaster+`')
		   AND s.is_selected = true AND s.is_archived = false AND s.is_invalidated = false
		   AND s.file_id IS NOT NULL`, assetID)
	if err != nil {
		return nil, fmt.Errorf("list still approved submissions: %w", err)
	}
	defer rows.Close()
	var out []*model.Submissions
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---- _invalidate_character_dependent_views（repositories.py:8607-8716） ----

// invalidateCharacterDependentViews A/MASTER 图位重开（rework approved 批次）时
// 作废 B-E 依赖成果。
func (r *Tasks) invalidateCharacterDependentViews(ctx context.Context, tx pgx.Tx, task *model.Tasks,
	invalidatedBy *string, invalidatedAt time.Time, invalidatedSourceID *string) error {
	variant := strings.ToUpper(strings.TrimSpace(ptrOr(task.TaskVariant, "MASTER")))
	if task.AssetId == nil || (task.TaskType != taskTypeAsset && task.TaskType != taskTypeTextToImage) ||
		(variant != "A" && variant != "MASTER") {
		return nil
	}
	asset, err := r.getAssetForUpdate(ctx, tx, *task.AssetId)
	if err != nil {
		return err
	}
	if asset == nil || asset.AssetType != assetTypeCharacter {
		return nil
	}
	dependentTasks, err := r.listCharacterDependentTasks(ctx, tx, task)
	if err != nil {
		return err
	}
	if len(dependentTasks) == 0 {
		return nil
	}
	dependentIDs := make([]string, 0, len(dependentTasks))
	for _, dep := range dependentTasks {
		dependentIDs = append(dependentIDs, dep.ID)
	}
	// 作废依赖任务的 submissions。
	invalidatedSubmissions, err := r.listInvalidatableSubmissions(ctx, tx, dependentIDs)
	if err != nil {
		return err
	}
	for _, submission := range invalidatedSubmissions {
		submission.IsInvalidated = true
		submission.InvalidatedAt = &invalidatedAt
		submission.InvalidatedById = invalidatedBy
		submission.InvalidatedReason = ptr("parent_master_revoked")
		submission.InvalidatedSourceId = invalidatedSourceID
		submission.IsSelected = false
		submission.IsPrimary = false
		submission.UpdatedAt = invalidatedAt
		if err := persistSubmission(ctx, tx, submission); err != nil {
			return err
		}
	}
	// 作废匹配的 AssetVersion。
	invalidatedVersions, err := r.listInvalidatableAssetVersions(ctx, tx, *task.AssetId,
		dependentIDs, invalidatedSourceID)
	if err != nil {
		return err
	}
	for _, version := range invalidatedVersions {
		version.IsInvalidated = true
		version.InvalidatedAt = &invalidatedAt
		version.InvalidatedById = invalidatedBy
		version.InvalidatedReason = ptr("parent_master_revoked")
		version.InvalidatedSourceId = invalidatedSourceID
		version.IsCurrent = false
		version.UpdatedAt = invalidatedAt
		if err := persistAssetVersion(ctx, tx, version); err != nil {
			return err
		}
	}
	invalidatedVersionIDs := map[string]bool{}
	for _, version := range invalidatedVersions {
		invalidatedVersionIDs[version.ID] = true
	}
	if asset.CurrentVersionId != nil && invalidatedVersionIDs[*asset.CurrentVersionId] {
		asset.CurrentVersionId = nil
		asset.FilePath = nil
		asset.PreviewPath = nil
	}
	asset.Status = "in_progress"
	asset.UpdatedAt = invalidatedAt
	if err := persistAsset(ctx, tx, asset); err != nil {
		return err
	}
	for _, dep := range dependentTasks {
		dep.Status = taskStatusRejected
		dep.CompletedAt = nil
		dep.VisibleUntil = nil
		dep.AssetContextOutdated = true
		dep.UpdatedAt = invalidatedAt
		if err := persistTask(ctx, tx, dep); err != nil {
			return err
		}
		if dep.AssigneeId != nil {
			if err := insertNotification(ctx, tx, *dep.AssigneeId, "人物身份母版已更新",
				fmt.Sprintf("%s 的旧成果已作废，请基于新的 A 图重新生产。", dep.Title),
				notificationTypeTaskRework); err != nil {
				return err
			}
		}
	}
	dependentIDsForLog := make([]string, 0, len(dependentTasks))
	for _, dep := range dependentTasks {
		dependentIDsForLog = append(dependentIDsForLog, dep.ID)
	}
	detail := map[string]any{
		"master_task_id":       task.ID,
		"master_submission_id": invalidatedSourceID,
		"reason":               "parent_master_revoked",
		"dependent_task_ids":   dependentIDsForLog,
		"submission_count":     len(invalidatedSubmissions),
		"asset_version_count":  len(invalidatedVersions),
	}
	return insertOperationLog(ctx, tx, *invalidatedBy, &task.ProjectId, task.AssetId,
		targetTypeCharacterAsset, opLogCharacterDependentViewsInvalidated, detail)
}

// listCharacterDependentTasks 对齐 invalidate 的 dependent_tasks 查询（FOR UPDATE）。
func (r *Tasks) listCharacterDependentTasks(ctx context.Context, tx pgx.Tx, task *model.Tasks) ([]*model.Tasks, error) {
	rows, err := tx.Query(ctx,
		`SELECT `+taskColumns+` FROM tasks
		 WHERE project_id = $1 AND asset_id = $2 AND id <> $3
		   AND task_type IN ('`+taskTypeAsset+`','`+taskTypeTextToImage+`')
		   AND coalesce(age_stage_code,'') = $4
		   AND coalesce(costume_variant_code,'') = $5
		   AND upper(coalesce(task_variant,'MASTER')) NOT IN ('A','MASTER')
		   AND `+activeAssetTaskCondition()+`
		 FOR UPDATE`, task.ProjectId, *task.AssetId, task.ID,
		ptrOr(task.AgeStageCode, ""), ptrOr(task.CostumeVariantCode, ""))
	if err != nil {
		return nil, fmt.Errorf("list character dependent tasks: %w", err)
	}
	defer rows.Close()
	var out []*model.Tasks
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Tasks) listInvalidatableSubmissions(ctx context.Context, q queryer, dependentIDs []string) ([]*model.Submissions, error) {
	rows, err := q.Query(ctx,
		`SELECT `+submissionColumns+` FROM submissions
		 WHERE task_id = ANY($1) AND is_invalidated = false`, dependentIDs)
	if err != nil {
		return nil, fmt.Errorf("list invalidatable submissions: %w", err)
	}
	defer rows.Close()
	var out []*model.Submissions
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Tasks) listInvalidatableAssetVersions(ctx context.Context, q queryer, assetID string,
	dependentIDs []string, sourceMasterID *string) ([]*model.AssetVersions, error) {
	args := []any{assetID, dependentIDs}
	qSQL := `SELECT ` + assetVersionColumns + ` FROM asset_versions
		 WHERE asset_id = $1 AND is_invalidated = false
		   AND (source_task_id = ANY($2)`
	n := len(args)
	if sourceMasterID != nil {
		n++
		args = append(args, *sourceMasterID)
		qSQL += fmt.Sprintf(` OR source_master_submission_id = $%d`, n)
	}
	qSQL += `)`
	rows, err := q.Query(ctx, qSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("list invalidatable asset versions: %w", err)
	}
	defer rows.Close()
	var out []*model.AssetVersions
	for rows.Next() {
		v, err := scanAssetVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- _refresh_asset_completion_status（repositories.py:9080-9090） ----

// refreshAssetCompletionStatus asset status = completed iff 全部非退役 asset/audio 任务完成。
func (r *Tasks) refreshAssetCompletionStatus(ctx context.Context, q queryer, assetID string, now time.Time) error {
	rows, err := q.Query(ctx,
		`SELECT status FROM tasks WHERE asset_id = $1 AND task_type IN ('`+taskTypeAsset+
			`','`+taskTypeAudio+`') AND is_retired = false`, assetID)
	if err != nil {
		return fmt.Errorf("refresh asset completion statuses: %w", err)
	}
	statuses := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		statuses = append(statuses, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	asset, err := r.getAssetByID(ctx, q, assetID)
	if err != nil {
		return err
	}
	if asset == nil {
		return nil
	}
	status := "in_progress"
	if len(statuses) > 0 {
		allCompleted := true
		for _, s := range statuses {
			if s != taskStatusCompleted {
				allCompleted = false
				break
			}
		}
		if allCompleted {
			status = taskStatusCompleted
		}
	}
	asset.Status = status
	asset.UpdatedAt = now
	return persistAsset(ctx, q, asset)
}

// ---- _notify_unlocked_dependents（repositories.py:9093-9143） ----

// notifyUnlockedDependents approve 后对新解锁依赖/场景任务发通知。
func (r *Tasks) notifyUnlockedDependents(ctx context.Context, tx pgx.Tx, sourceTask *model.Tasks,
	lockedScenesBefore map[string]bool) error {
	depIDs, err := directDependentTaskIDs(ctx, tx, sourceTask.ID)
	if err != nil {
		return err
	}
	if len(depIDs) > 0 {
		dependents, err := r.listDependentsByIDs(ctx, tx, depIDs)
		if err != nil {
			return err
		}
		for _, dep := range dependents {
			locked, _, _, err := r.TaskDependencyState(ctx, tx, dep)
			if err != nil {
				return err
			}
			if !locked && dep.AssigneeId != nil && dep.Status != taskStatusCompleted {
				if err := insertNotification(ctx, tx, *dep.AssigneeId, "任务已解锁",
					fmt.Sprintf("前置成果已审核定版，可以开始：%s", dep.Title),
					notificationTypeTaskUnlocked); err != nil {
					return err
				}
			}
		}
	}
	if sourceTask.TaskType != taskTypeAsset || len(lockedScenesBefore) == 0 {
		return nil
	}
	after, err := r.SceneGatingCore(ctx, tx, sourceTask.ProjectId)
	if err != nil {
		return err
	}
	lockedScenesAfter := map[string]bool{}
	for _, entry := range after {
		if entry.Locked {
			lockedScenesAfter[entry.SceneCode] = true
		}
	}
	newlyUnlocked := []string{}
	for scene := range lockedScenesBefore {
		if !lockedScenesAfter[scene] {
			newlyUnlocked = append(newlyUnlocked, scene)
		}
	}
	if len(newlyUnlocked) == 0 {
		return nil
	}
	sceneTasks, err := r.listUnlockedSceneTasks(ctx, tx, sourceTask.ProjectId, newlyUnlocked)
	if err != nil {
		return err
	}
	for _, sceneTask := range sceneTasks {
		if sceneTask.AssigneeId == nil {
			continue
		}
		if err := insertNotification(ctx, tx, *sceneTask.AssigneeId, "场景生产任务已解锁",
			fmt.Sprintf("场景资产已全部审核定版，可以开始：%s", sceneTask.Title),
			notificationTypeTaskUnlocked); err != nil {
			return err
		}
	}
	return nil
}

// listDependentsByIDs 加载依赖任务（active condition 过滤）。
func (r *Tasks) listDependentsByIDs(ctx context.Context, q queryer, ids []string) ([]*model.Tasks, error) {
	rows, err := q.Query(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = ANY($1) AND `+activeAssetTaskCondition(), ids)
	if err != nil {
		return nil, fmt.Errorf("list dependents: %w", err)
	}
	defer rows.Close()
	var out []*model.Tasks
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// listUnlockedSceneTasks 场景资产全部定版后，新解锁的非完成场景生产任务。
func (r *Tasks) listUnlockedSceneTasks(ctx context.Context, q queryer, projectID string,
	newlyUnlocked []string) ([]*model.Tasks, error) {
	rows, err := q.Query(ctx,
		`SELECT `+taskColumns+` FROM tasks
		 WHERE project_id = $1 AND storyboard_id IS NOT NULL
		   AND (scene_code = ANY($2) OR scene_name = ANY($2))
		   AND status <> '`+taskStatusCompleted+`' AND assignee_id IS NOT NULL
		   AND is_retired = false`, projectID, newlyUnlocked)
	if err != nil {
		return nil, fmt.Errorf("list unlocked scene tasks: %w", err)
	}
	defer rows.Close()
	var out []*model.Tasks
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
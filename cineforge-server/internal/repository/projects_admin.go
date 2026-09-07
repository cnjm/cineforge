package repository

// P3d 回补：项目域管理动作仓储（soft-delete / script-segments confirm /
// breakdown lock / episode hard delete）。
// 对齐 legacy repositories.py：soft_delete_project、confirm_script_segments、
// lock_breakdown_episode、hard_delete_project_episode。
// 契约约定：KeyError→404、PermissionError→403、ValueError→400（由 service 层映射）。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// ConfirmSegmentsResult 对齐 confirm_script_segments 的落库结果。
type ConfirmSegmentsResult struct {
	ConfirmedCount int
	Version        int32
}

// LockEpisodeResult 对齐 lock_breakdown_episode 的落库结果。
type LockEpisodeResult struct {
	CreatedStoryboards int
	CreatedAssetTasks  int
	Version            int32
	AssetCount         int
	TaskCount          int
}

// EpisodeDeleteResult 对齐 hard_delete_project_episode 的落库结果 + 清理候选。
type EpisodeDeleteResult struct {
	ProjectID      string
	EpisodeID      string
	EpisodeCode    string
	DeletedCounts  map[string]int
	CleanupObjects []map[string]any
	SharedObjects  []map[string]any
	DeleteFiles    bool
	OpLogID        string
}

// ActiveAdminPasswordHashes 返回活跃 admin 用户的密码哈希（_verify_admin_password 用）。
func (r *Projects) ActiveAdminPasswordHashes(ctx context.Context) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT password_hash FROM users WHERE role = 'admin' AND is_active = TRUE AND password_hash IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("active admins: %w", err)
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var h *string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan admin hash: %w", err)
		}
		if h != nil {
			hashes = append(hashes, *h)
		}
	}
	return hashes, rows.Err()
}

// SoftDeleteProject 软删除项目（status/current_stage→archived + deleted_at + op log）。
func (r *Projects) SoftDeleteProject(ctx context.Context, projectID, userID string, reason *string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin soft-delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	var deletedAt *time.Time
	if err := tx.QueryRow(ctx,
		`SELECT deleted_at FROM projects WHERE id = $1 FOR UPDATE`, projectID).Scan(&deletedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return fmt.Errorf("lock project: %w", err)
	}
	if deletedAt != nil {
		return nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE projects SET status='archived', current_stage='archived', archived_at=COALESCE(archived_at,$2),
		 deleted_at=$2, deleted_by_id=$3, delete_reason=$4, updated_at=$2 WHERE id=$1`,
		projectID, now, userID, reason); err != nil {
		return fmt.Errorf("soft delete project: %w", err)
	}
	detail, _ := json.Marshal(map[string]any{"reason": reason})
	if _, err := r.insertOperationLogTx(ctx, tx, userID, projectID, "project", projectID, "project_soft_deleted", detail); err != nil {
		return fmt.Errorf("op log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit soft-delete: %w", err)
	}
	return nil
}

// ---- 通用 tx 辅助 ----

// insertOperationLogTx 写入操作日志并返回新行 id（分集删除的文件清理终局需回填 detail）。
func (r *Projects) insertOperationLogTx(ctx context.Context, tx pgx.Tx, operatorID, projectID, targetType, targetID, action string, detail []byte) (string, error) {
	id := newUUIDString()
	_, err := tx.Exec(ctx,
		`INSERT INTO operation_logs (id, operator_id, project_id, target_type, target_id, action, detail)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		id, nullableStr(operatorID), nullableStr(projectID), targetType, nullableStr(targetID), action, orEmptyJSON(detail))
	return id, err
}

func nullableStr(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func strPtr(value string) *string {
	return &value
}

// insertEntityVersionTx 在 tx 内写 entity_versions（版本号/版本码自动生成；is_current=false）。
func (r *Projects) insertEntityVersionTx(ctx context.Context, tx pgx.Tx, ev model.EntityVersions) (string, error) {
	var maxNo int32
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version_no),0) FROM entity_versions WHERE entity_type=$1 AND entity_code=$2`,
		ev.EntityType, ev.EntityCode).Scan(&maxNo); err != nil {
		return "", fmt.Errorf("max entity version: %w", err)
	}
	ev.VersionNo = maxNo + 1
	ev.VersionCode = fmt.Sprintf("%s-V%03d", ev.EntityCode, ev.VersionNo)
	if ev.ID == "" {
		ev.ID = newUUIDString()
	}
	if ev.IsCurrent {
		if _, err := tx.Exec(ctx,
			`UPDATE entity_versions SET is_current=FALSE WHERE entity_type=$1 AND entity_code=$2`,
			ev.EntityType, ev.EntityCode); err != nil {
			return "", err
		}
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO entity_versions (id, project_id, entity_type, entity_id, entity_code, version_no, version_code,
		   source, status, content_json, change_summary, changed_fields, base_version_id, agent_run_id, created_by, is_current)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		ev.ID, ev.ProjectId, ev.EntityType, ev.EntityId, ev.EntityCode, ev.VersionNo, ev.VersionCode,
		ev.Source, ev.Status, orEmptyJSON(asRaw(ev.ContentJson)), ev.ChangeSummary, ev.ChangedFields,
		ev.BaseVersionId, ev.AgentRunId, ev.CreatedBy, ev.IsCurrent)
	return ev.ID, err
}

func asRaw(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage(`{}`)
	}
	return v
}

// jsonB 将任意值编码为标准 JSONB 参数。
func jsonB(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte(`null`)
	}
	return raw
}

func jsonBOrEmpty(v any) []byte {
	raw := jsonB(v)
	if string(raw) == "null" {
		return []byte(`{}`)
	}
	return raw
}

// mapList 归一化 view 中的数组字段（[{...},{...}]）。
func mapList(v any) []map[string]any {
	switch typed := v.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		return nil
	}
}

func strOf(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case *string:
		return derefValue(v)
	case json.RawMessage:
		return strings.Trim(string(v), `"`)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func strPtrOf(m map[string]any, key string) any {
	value := strOf(m, key)
	if value == "" {
		return nil
	}
	return value
}

func timeOf(m map[string]any, key string) time.Time {
	if v, ok := m[key].(time.Time); ok {
		return v
	}
	return time.Time{}
}

// intOf 读取整型字段（int32/int64/float64/json.Number）。
func intOf(v any) int32 {
	switch typed := v.(type) {
	case int:
		return int32(typed)
	case int32:
		return typed
	case int64:
		return int32(typed)
	case float64:
		return int32(typed)
	case json.Number:
		n, _ := typed.Int64()
		return int32(n)
	case string:
		var n int64
		fmt.Sscanf(typed, "%d", &n)
		return int32(n)
	default:
		return 0
	}
}

// safeInt32 返回 v 的 int32，缺失/非法回退 default（对齐 legacy _safe_int）。
func safeInt32(v any, def int32) int32 {
	switch typed := v.(type) {
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

// NormalizeEpisodeCode 对齐 legacy _normalize_episode_code。
func NormalizeEpisodeCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	code := strings.ToUpper(value)
	if code == "EP" {
		return code
	}
	if digits := code; digits != "" && allDigits(digits) {
		return fmt.Sprintf("EP%02d", atoiOr(digits, 1))
	}
	if !strings.HasPrefix(code, "EP") {
		return "EP" + code
	}
	return code
}

func allDigits(value string) bool {
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	return value != ""
}

func atoiOr(value string, def int) int {
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
		return def
	}
	return n
}

// EpisodeNumFromCode 对齐 legacy _episode_num_from_code。
func EpisodeNumFromCode(code string) int32 {
	normalized := NormalizeEpisodeCode(code)
	if normalized == "" {
		normalized = "EP01"
	}
	return int32(atoiOr(strings.TrimPrefix(normalized, "EP"), 1))
}

func storyboardEpisodeCode(item map[string]any) string {
	code := NormalizeEpisodeCode(strOf(item, "episode_code"))
	if code != "" {
		return code
	}
	return fmt.Sprintf("EP%02d", safeInt32(item["episode_num"], 1))
}

func kitEpisodeCode(item map[string]any, def string) string {
	if code := NormalizeEpisodeCode(strOf(item, "episode_code")); code != "" {
		return code
	}
	if code := NormalizeEpisodeCode(strOf(item, "corresponding_episode")); code != "" {
		return code
	}
	if code := NormalizeEpisodeCode(strOf(item, "episode")); code != "" {
		return code
	}
	return NormalizeEpisodeCode(def)
}

// segmentEpisodeCode 定位一条脚本段的 episode_code（缺失回退内容级）。
func segmentEpisodeCode(item map[string]any, contentEpisodeCode string) string {
	code := NormalizeEpisodeCode(strOf(item, "episode_code"))
	if code == "" {
		code = contentEpisodeCode
	}
	if code == "" {
		code = "EP01"
	}
	if code == "EP" {
		code = "EP01"
	}
	return code
}

// mergeMetadata 对齐 legacy 元数据合并：现有 + item 的 metadata_json/metadata + 扩展。
func mergeMetadata(segment, item map[string]any) []byte {
	base := map[string]any{}
	if existing, ok := item["metadata_json"].(map[string]any); ok {
		for k, v := range existing {
			base[k] = v
		}
	}
	if existing, ok := item["metadata"].(map[string]any); ok {
		for k, v := range existing {
			base[k] = v
		}
	}
	segmentFields := segment
	for _, key := range []string{"context_code", "render_mode"} {
		if _, ok := base[key]; !ok {
			if v := strOf(segmentFields, key); v != "" {
				base[key] = v
			}
		}
	}
	base["source"] = "human_confirmed"
	base["status"] = "script_confirmed"
	return jsonB(base)
}

// sortedStoryboards 对齐 legacy _sorted_storyboards。
func sortedStoryboards(items []map[string]any) []map[string]any {
	normalized := make([]map[string]any, 0, len(items))
	for _, item := range items {
		copyItem := map[string]any{}
		for k, v := range item {
			copyItem[k] = v
		}
		if _, ok := copyItem["order_num"]; !ok {
			order := safeInt32(copyItem["order_no"], 999999)
			if order == 999999 {
				order = safeInt32(copyItem["shot_no"], 999999)
			}
			copyItem["order_num"] = order
		}
		if _, ok := copyItem["episode_code"]; !ok {
			copyItem["episode_code"] = storyboardEpisodeCode(copyItem)
		}
		normalized = append(normalized, copyItem)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		ei := EpisodeNumFromCode(strOf(normalized[i], "episode_code"))
		ej := EpisodeNumFromCode(strOf(normalized[j], "episode_code"))
		if ei != ej {
			return ei < ej
		}
		return safeInt32(normalized[i]["order_num"], 999999) < safeInt32(normalized[j]["order_num"], 999999)
	})
	return normalized
}

// ConfirmScriptSegmentsTx 在单事务内完成 script-segments/confirm 落库：
// upsert 已确认脚本段（含实体版本）→ 新拆解版本 + script_breakdown 实体版本 →
// 项目阶段置 asset_locking → 操作日志。对齐 legacy confirm_script_segments。
func (r *Projects) ConfirmScriptSegmentsTx(ctx context.Context, projectID string, content map[string]any, userID string) (*ConfirmSegmentsResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin confirm segments: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	project, err := lockProjectTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}

	contentEpisodeCode := NormalizeEpisodeCode(strOf(content, "episode_code"))
	episodeIDByCode := map[string]string{}
	contentEpisodeID := strOf(content, "episode_id")
	if contentEpisodeID != "" && contentEpisodeCode != "" {
		episodeIDByCode[contentEpisodeCode] = contentEpisodeID
	}
	resolveEpisodeID := func(code string) string {
		normalized := NormalizeEpisodeCode(code)
		if normalized == "" {
			normalized = "EP01"
		}
		if value, ok := episodeIDByCode[normalized]; ok {
			return value
		}
		var episodeID *string
		_ = tx.QueryRow(ctx,
			`SELECT id FROM project_episodes WHERE project_id=$1 AND episode_code=$2`,
			projectID, normalized).Scan(&episodeID)
		resolved := ""
		if episodeID != nil {
			resolved = *episodeID
		}
		episodeIDByCode[normalized] = resolved
		return resolved
	}

	confirmed := 0
	segments := mapList(content["script_segments"])
	for _, segmentItem := range segments {
		segmentCode := strOf(segmentItem, "script_segment_code")
		if segmentCode == "" {
			continue
		}
		// 查找已有行（先按 id，再按 code）。
		var id string
		if segmentID := strOf(segmentItem, "id"); segmentID != "" {
			_ = tx.QueryRow(ctx, `SELECT id FROM script_segments WHERE id=$1 AND project_id=$2`, segmentID, projectID).Scan(&id)
		}
		if id == "" {
			_ = tx.QueryRow(ctx,
				`SELECT id FROM script_segments WHERE project_id=$1 AND script_segment_code=$2`,
				projectID, segmentCode).Scan(&id)
		}
		episodeCode := segmentEpisodeCode(segmentItem, contentEpisodeCode)
		episodeID := resolveEpisodeID(episodeCode)
		metadata := mergeMetadata(segmentItem, segmentItem)
		now := time.Now().UTC()
		if id == "" {
			id = newUUIDString()
			if _, err := tx.Exec(ctx,
				`INSERT INTO script_segments (id, project_id, episode_id, script_segment_code, episode_code,
				   order_no, title, source_text, summary, story_function, dominant_emotion, rhythm, viewpoint,
				   context_code, render_mode, metadata_json, created_by, created_at, updated_at)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
				id, projectID, nullableStr(episodeID), segmentCode, episodeCode,
				safeInt32(segmentItem["order_no"], int32(confirmed+1)),
				strPtrOf(segmentItem, "title"),
				firstNonEmpty(strOf(segmentItem, "source_text"), "未提供原文依据"),
				strPtrOf(segmentItem, "summary"), strPtrOf(segmentItem, "story_function"),
				strPtrOf(segmentItem, "dominant_emotion"), strPtrOf(segmentItem, "rhythm"),
				strPtrOf(segmentItem, "viewpoint"),
				orElse(strPtrOf(segmentItem, "context_code"), nestedStr(segmentItem, "metadata", "context_code")),
				orElse(strPtrOf(segmentItem, "render_mode"), nestedStr(segmentItem, "metadata", "render_mode")),
				metadata, userID, now, now); err != nil {
				return nil, fmt.Errorf("insert script segment: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx,
				`UPDATE script_segments SET episode_code=$3, episode_id=$4, order_no=$5, title=$6,
				   source_text=$7, summary=$8, story_function=$9, dominant_emotion=$10, rhythm=$11, viewpoint=$12,
				   context_code=$13, render_mode=$14, metadata_json=$15, updated_at=$16
				 WHERE id=$1 AND project_id=$2`,
				id, projectID, episodeCode, nullableStr(episodeID),
				safeInt32(segmentItem["order_no"], int32(confirmed+1)),
				strPtrOf(segmentItem, "title"),
				firstNonEmpty(strOf(segmentItem, "source_text"), "未提供原文依据"),
				strPtrOf(segmentItem, "summary"), strPtrOf(segmentItem, "story_function"),
				strPtrOf(segmentItem, "dominant_emotion"), strPtrOf(segmentItem, "rhythm"),
				strPtrOf(segmentItem, "viewpoint"),
				orElse(strPtrOf(segmentItem, "context_code"), nestedStr(segmentItem, "metadata", "context_code")),
				orElse(strPtrOf(segmentItem, "render_mode"), nestedStr(segmentItem, "metadata", "render_mode")),
				metadata, now); err != nil {
				return nil, fmt.Errorf("update script segment: %w", err)
			}
		}
		view := map[string]any{
			"id": id, "project_id": projectID, "episode_id": nullableStr(episodeID),
			"script_segment_code": segmentCode, "episode_code": episodeCode,
			"order_no": safeInt32(segmentItem["order_no"], int32(confirmed+1)),
			"title":    strPtrOf(segmentItem, "title"), "source_text": strOf(segmentItem, "source_text"),
			"summary": strPtrOf(segmentItem, "summary"), "story_function": strPtrOf(segmentItem, "story_function"),
			"dominant_emotion": strPtrOf(segmentItem, "dominant_emotion"), "rhythm": strPtrOf(segmentItem, "rhythm"),
			"viewpoint": strPtrOf(segmentItem, "viewpoint"), "context_code": strPtrOf(segmentItem, "context_code"),
			"render_mode": strPtrOf(segmentItem, "render_mode"), "metadata_json": json.RawMessage(metadata),
			"current_version_id": nil, "created_by": userID, "created_at": nil, "updated_at": nil,
		}
		entityVersionID, err := r.insertEntityVersionTx(ctx, tx, model.EntityVersions{
			ProjectId:     &projectID,
			EntityType:    "script_segment",
			EntityId:      strPtr(id),
			EntityCode:    segmentCode,
			Source:        "human_confirmed",
			Status:        "script_confirmed",
			ContentJson:   jsonB(view),
			ChangeSummary: strPtr("导演确认脚本段，进入分镜和资产拆分阶段"),
			ChangedFields: json.RawMessage(jsonB([]string{"script_segment", "source_text", "summary"})),
			CreatedBy:     &userID,
			IsCurrent:     true,
		})
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE script_segments SET current_version_id=$2, updated_at=$3 WHERE id=$1`,
			id, entityVersionID, now); err != nil {
			return nil, fmt.Errorf("set segment current version: %w", err)
		}
		confirmed++
	}

	var version int32
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version),0) FROM script_breakdowns WHERE project_id=$1`, projectID).Scan(&version); err != nil {
		return nil, err
	}
	version++
	episodeCode := NormalizeEpisodeCode(strOf(content, "episode_code"))
	entityCode := BreakdownEntityCode(project, episodeCode, version)
	contentJSON := jsonB(content)
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx,
		`INSERT INTO script_breakdowns (id, project_id, version, content_json, agent_run_id, edited_by_id, data_state, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,'final',$7,$8)`,
		newUUIDString(), projectID, version, contentJSON, nil, userID, now, now); err != nil {
		return nil, fmt.Errorf("insert breakdown: %w", err)
	}
	if _, err := r.insertEntityVersionTx(ctx, tx, model.EntityVersions{
		ProjectId:     &projectID,
		EntityType:    "script_breakdown",
		EntityId:      nil,
		EntityCode:    entityCode,
		Source:        strOf(content, "source_label"),
		Status:        "script_confirmed",
		ContentJson:   contentJSON,
		ChangeSummary: joinChangeSummary(content),
		ChangedFields: json.RawMessage(jsonB([]string{"storyboards", "assets", "breakdown"})),
		AgentRunId:    nil,
		CreatedBy:     &userID,
		IsCurrent:     true,
	}); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE projects SET status='asset_locking', current_stage='asset_locking', updated_at=$2 WHERE id=$1`,
		projectID, now); err != nil {
		return nil, fmt.Errorf("stage asset_locking: %w", err)
	}
	detail, _ := json.Marshal(map[string]any{
		"episode_code": episodeCode, "breakdown_version": version,
		"script_segment_count": confirmed, "next_stage": "asset_locking",
	})
	if _, err := r.insertOperationLogTx(ctx, tx, userID, projectID, "script_segments", "", "script_segments_confirmed", detail); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit confirm segments: %w", err)
	}
	return &ConfirmSegmentsResult{ConfirmedCount: confirmed, Version: version}, nil
}

// LatestScriptConfirmedBreakdownVersion 对齐 legacy _latest_script_confirmed_breakdown_version。
func (r *Projects) LatestScriptConfirmedBreakdownVersion(ctx context.Context, projectID string, episodeCode *string) (int32, bool, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT version, content_json FROM script_breakdowns WHERE project_id=$1 ORDER BY version DESC`, projectID)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var version int32
		var content []byte
		if err := rows.Scan(&version, &content); err != nil {
			return 0, false, err
		}
		var parsed map[string]any
		if err := json.Unmarshal(content, &parsed); err != nil {
			continue
		}
		if strOf(parsed, "source_label") != "human_confirmed" {
			continue
		}
		if episodeCode != nil && *episodeCode != "" &&
			NormalizeEpisodeCode(strOf(parsed, "episode_code")) != NormalizeEpisodeCode(*episodeCode) {
			continue
		}
		return version, true, nil
	}
	return 0, false, rows.Err()
}

// BreakdownEntityCode 对齐 legacy _breakdown_entity_code。
func BreakdownEntityCode(project *model.Projects, episodeCode string, version int32) string {
	prefix := "RF"
	if project.ProjectPrefix != nil && *project.ProjectPrefix != "" {
		prefix = *project.ProjectPrefix
	} else if project.ProjectNo != nil && *project.ProjectNo != "" {
		prefix = *project.ProjectNo
	} else if project.ID != "" {
		prefix = project.ID
	}
	normalized := NormalizeEpisodeCode(episodeCode)
	if normalized == "" || normalized == "EP" {
		normalized = "ALL"
	}
	return fmt.Sprintf("%s-%s-BREAKDOWN-V%03d", prefix, normalized, version)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func orElse(primary any, fallback any) any {
	if primary != nil && fmt.Sprintf("%v", primary) != "" {
		return primary
	}
	if fallback != nil && fmt.Sprintf("%v", fallback) != "" {
		return fallback
	}
	return nil
}

func nestedStr(m map[string]any, outer, inner string) any {
	outerMap, _ := m[outer].(map[string]any)
	if outerMap == nil {
		return nil
	}
	return strPtrOf(outerMap, inner)
}

func joinChangeSummary(content map[string]any) *string {
	var items []string
	switch typed := content["change_summary"].(type) {
	case []string:
		items = typed
	case []any:
		for _, item := range typed {
			items = append(items, fmt.Sprintf("%v", item))
		}
	}
	if len(items) == 0 {
		return nil
	}
	joined := strings.Join(items, "；")
	return &joined
}

func lockProjectTx(ctx context.Context, tx pgx.Tx, projectID string) (*model.Projects, error) {
	var project model.Projects
	err := tx.QueryRow(ctx,
		`SELECT id, project_no, project_prefix, name, title, genre, style, status, current_stage, manager_id,
		   created_by_id, script_text, production_brief, locked_at, archived_at, deleted_at, deleted_by_id, delete_reason,
		   created_at, updated_at FROM projects WHERE id=$1 FOR UPDATE`, projectID).
		Scan(&project.ID, &project.ProjectNo, &project.ProjectPrefix, &project.Name, &project.Title, &project.Genre,
			&project.Style, &project.Status, &project.CurrentStage, &project.ManagerId, &project.CreatedById,
			&project.ScriptText, &project.ProductionBrief, &project.LockedAt, &project.ArchivedAt, &project.DeletedAt,
			&project.DeletedById, &project.DeleteReason, &project.CreatedAt, &project.UpdatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("lock project: %w", err)
	}
	return &project, nil
}

// LockBreakdownEpisodeTx 在单事务内完成 breakdowns/lock 落库：
// upsert 分镜（含实体版本）→ 可选资产文生图任务 → 新拆解版本 + 实体版本 → 训练样本 →
// 项目阶段置 task_assignment → 操作日志。对齐 legacy lock_breakdown_episode。
func (r *Projects) LockBreakdownEpisodeTx(ctx context.Context, projectID string, content map[string]any, userID string, createAssetTasks bool) (*LockEpisodeResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin lock: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	project, err := lockProjectTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}

	createdStoryboards, err := r.upsertStoryboardsTx(ctx, tx, project, content, userID)
	if err != nil {
		return nil, err
	}
	createdAssetTasks := 0
	if createAssetTasks {
		createdAssetTasks, err = r.createAssetTextToImageTasksTx(ctx, tx, projectID, project, content, userID)
		if err != nil {
			return nil, err
		}
	}

	var version int32
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version),0) FROM script_breakdowns WHERE project_id=$1`, projectID).Scan(&version); err != nil {
		return nil, err
	}
	version++
	episodeCode := NormalizeEpisodeCode(strOf(content, "episode_code"))
	entityCode := BreakdownEntityCode(project, episodeCode, version)
	contentJSON := jsonB(content)
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx,
		`INSERT INTO script_breakdowns (id, project_id, version, content_json, agent_run_id, edited_by_id, data_state, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,'final',$7,$8)`,
		newUUIDString(), projectID, version, contentJSON, nil, userID, now, now); err != nil {
		return nil, fmt.Errorf("insert breakdown: %w", err)
	}
	if _, err := r.insertEntityVersionTx(ctx, tx, model.EntityVersions{
		ProjectId:     &projectID,
		EntityType:    "script_breakdown",
		EntityId:      nil,
		EntityCode:    entityCode,
		Source:        strOf(content, "source_label"),
		Status:        "locked",
		ContentJson:   contentJSON,
		ChangeSummary: joinChangeSummary(content),
		ChangedFields: json.RawMessage(jsonB([]string{"storyboards", "assets", "breakdown"})),
		AgentRunId:    nil,
		CreatedBy:     &userID,
		IsCurrent:     true,
	}); err != nil {
		return nil, err
	}
	if err := r.insertLockedTrainingSampleTx(ctx, tx, projectID, entityCode, content, userID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE projects SET status='task_assignment', current_stage='task_assignment', locked_at=COALESCE(locked_at,$2), updated_at=$2 WHERE id=$1`,
		projectID, now); err != nil {
		return nil, fmt.Errorf("stage task_assignment: %w", err)
	}
	detail, _ := json.Marshal(map[string]any{
		"episode_code": episodeCode, "breakdown_version": version,
		"storyboards": createdStoryboards, "asset_tasks": createdAssetTasks,
	})
	if _, err := r.insertOperationLogTx(ctx, tx, userID, projectID, "script_breakdown", "", "breakdown_episode_locked", detail); err != nil {
		return nil, err
	}
	var taskCount int32
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM tasks WHERE project_id=$1 AND is_retired=FALSE`, projectID).Scan(&taskCount); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit lock: %w", err)
	}
	return &LockEpisodeResult{
		CreatedStoryboards: createdStoryboards,
		CreatedAssetTasks:  createdAssetTasks,
		Version:            version,
		AssetCount:         len(mapList(content["assets"])),
		TaskCount:          int(taskCount),
	}, nil
}

// storyboardSegmentRefs 对齐 legacy _resolve_breakdown_storyboard_references：
// 项目内脚本段 code→id、场景资产 code→(code,name) 映射。
func storyboardSegmentRefs(ctx context.Context, tx pgx.Tx, projectID string, items []map[string]any) (map[string]string, map[string][2]string) {
	segmentCodes := map[string]bool{}
	sceneCodes := map[string]bool{}
	for _, item := range items {
		if code := strOf(item, "script_segment_code"); code != "" {
			segmentCodes[strings.TrimSpace(code)] = true
		}
		sceneCode, _ := sceneCodeName(item)
		if sceneCode != "" {
			sceneCodes[sceneCode] = true
		}
	}
	segmentIDsByCode := map[string]string{}
	if len(segmentCodes) > 0 {
		rows, err := tx.Query(ctx, `SELECT script_segment_code, id FROM script_segments WHERE project_id=$1`, projectID)
		if err == nil {
			for rows.Next() {
				var code, id string
				if err := rows.Scan(&code, &id); err == nil {
					for alias := range referenceCodeAliases(code) {
						if _, ok := segmentIDsByCode[alias]; !ok {
							segmentIDsByCode[alias] = id
						}
					}
				}
			}
			rows.Close()
		}
	}
	scenesByCode := map[string][2]string{}
	sceneCodeList := make([]string, 0, len(sceneCodes))
	for code := range sceneCodes {
		sceneCodeList = append(sceneCodeList, code)
	}
	if len(sceneCodeList) > 0 {
		rows, err := tx.Query(ctx,
			`SELECT asset_code, name FROM assets WHERE project_id=$1 AND asset_type='scene' AND status NOT IN ('deleted','excluded')`,
			projectID)
		if err == nil {
			for rows.Next() {
				var code, name string
				if err := rows.Scan(&code, &name); err == nil {
					if code == "" || name == "" {
						continue
					}
					resolved := [2]string{strings.TrimSpace(code), strings.TrimSpace(name)}
					for alias := range referenceCodeAliases(code) {
						if _, ok := scenesByCode[alias]; !ok {
							scenesByCode[alias] = resolved
						}
					}
				}
			}
			rows.Close()
		}
	}
	return segmentIDsByCode, scenesByCode
}

// referenceCodeAliases 对齐 legacy _reference_code_aliases。
func referenceCodeAliases(value string) map[string]bool {
	code := strings.ToUpper(strings.TrimSpace(value))
	aliases := map[string]bool{code: true}
	if code == "" {
		return aliases
	}
	// 仅支持尾段形如 (-J123 / -SC12) 的别名。
	lastDash := strings.LastIndex(code, "-")
	if lastDash > 0 && lastDash < len(code)-1 {
		alias := code[lastDash+1:]
		aliases[alias] = true
		if strings.HasPrefix(alias, "EP") && strings.Contains(alias, "-") {
			aliases[alias[strings.LastIndex(alias, "-")+1:]] = true
		}
	}
	return aliases
}

func resolvedReference(mapping map[string][2]string, value any) ([2]string, bool) {
	for alias := range referenceCodeAliases(fmt.Sprintf("%v", value)) {
		if resolved, ok := mapping[alias]; ok {
			return resolved, true
		}
	}
	return [2]string{}, false
}

func resolvedSegmentRef(mapping map[string]string, value string, episodeCode string) string {
	code := strings.ToUpper(strings.TrimSpace(value))
	if len(code) > 0 && code[0] == 'J' {
		if id, ok := mapping[normalizeJO(code, episodeCode)]; ok {
			return id
		}
	}
	for alias := range referenceCodeAliases(code) {
		if id, ok := mapping[alias]; ok {
			return id
		}
	}
	return ""
}

func normalizeJO(code, episodeCode string) string {
	return strings.ToUpper(episodeCode) + "-" + code
}

// upsertStoryboardsTx 对齐 legacy _upsert_storyboards_from_breakdown。
func (r *Projects) upsertStoryboardsTx(ctx context.Context, tx pgx.Tx, project *model.Projects, content map[string]any, userID string) (int, error) {
	items := mapList(content["storyboards"])
	if len(items) == 0 {
		return 0, nil
	}
	segmentIDsByCode, scenesByCode := storyboardSegmentRefs(ctx, tx, project.ID, items)
	createdOrUpdated := 0
	now := time.Now().UTC()
	for _, item := range items {
		episodeCode := storyboardEpisodeCode(item)
		episodeNum := EpisodeNumFromCode(episodeCode)
		orderNum := intOf(item["order_num"])
		if orderNum == 0 {
			orderNum = intOf(item["order_no"])
		}
		if orderNum == 0 {
			orderNum = intOf(item["shot_no"])
		}
		if orderNum == 0 {
			orderNum = int32(createdOrUpdated + 1)
		}
		scriptSegmentID := resolvedSegmentRef(segmentIDsByCode, strOf(item, "script_segment_code"), episodeCode)
		var storyboardID string
		err := tx.QueryRow(ctx,
			`SELECT id FROM storyboards WHERE project_id=$1 AND episode_num=$2 AND order_num=$3`,
			project.ID, episodeNum, orderNum).Scan(&storyboardID)
		exists := err == nil
		if err != nil && err != pgx.ErrNoRows {
			return 0, err
		}
		description := firstNonEmpty(
			strOf(item, "description"), strOf(item, "content"),
			strOf(item, "source_text"), strOf(item, "title"))
		sceneCode, sceneName := sceneCodeName(item)
		if resolved, ok := resolvedReference(scenesByCode, sceneCode); ok {
			sceneCode, sceneName = resolved[0], resolved[1]
			item["scene_code"] = sceneCode
			item["scene_name"] = sceneName
		}
		if scriptSegmentID != "" {
			item["script_segment_id"] = scriptSegmentID
		}
		storyboardCode := strOf(item, "storyboard_code")
		if storyboardCode == "" {
			prefix := "RF"
			if project.ProjectPrefix != nil && *project.ProjectPrefix != "" {
				prefix = *project.ProjectPrefix
			}
			storyboardCode = fmt.Sprintf("%s-%s-F%03d", prefix, episodeCode, orderNum)
		}
		chars := jsonArrayField(item, "characters", "character_codes")
		keyframes := item["keyframes"]
		if keyframes == nil {
			keyframes = []map[string]any{{"order": 1, "description": truncate(description, 120)}}
		}
		mirrors := item["mirror_shots"]
		if mirrors == nil {
			mirrors = []any{}
		}
		title := firstNonEmpty(strOf(item, "title"), strOf(item, "shot_no"), fmt.Sprintf("分镜 %03d", orderNum))
		description = firstNonEmpty(description, "未提供分镜描述")
		if exists {
			if _, err := tx.Exec(ctx,
				`UPDATE storyboards SET script_segment_id=$3, title=$4, description=$5, dialogue=$6, narration=$7,
				   camera=$8, shot_type=$9, duration_seconds=$10, characters=$11, keyframes=$12, mirror_shots=$13,
				   storyboard_code=$14, scene_code=$15, scene_name=$16, context_code=$17, render_mode=$18,
				   updated_at=$19 WHERE id=$1 AND project_id=$2`,
				storyboardID, project.ID, nullableStr(scriptSegmentID), title, description,
				strPtrOf(item, "dialogue"), strPtrOf(item, "narration"), strPtrOf(item, "camera"),
				orElse(strPtrOf(item, "shot_type"), strPtrOf(item, "shot_size")),
				safeInt32(item["duration_seconds"], 10), chars, keyframes, mirrors, storyboardCode,
				nullableOrStr(sceneCode), nullableOrStr(sceneName), strPtrOf(item, "context_code"),
				strPtrOf(item, "render_mode"), now); err != nil {
				return 0, fmt.Errorf("update storyboard: %w", err)
			}
		} else {
			storyboardID = newUUIDString()
			if _, err := tx.Exec(ctx,
				`INSERT INTO storyboards (id, project_id, script_segment_id, episode_num, order_num, title, description,
				   dialogue, camera, duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
				   scene_code, scene_name, context_code, render_mode, narration, shot_type, created_at, updated_at)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`,
				storyboardID, project.ID, nullableStr(scriptSegmentID), episodeNum, orderNum, title, description,
				strPtrOf(item, "dialogue"), strPtrOf(item, "camera"), safeInt32(item["duration_seconds"], 10),
				chars, keyframes, mirrors, todoTaskStatus, storyboardCode,
				nullableOrStr(sceneCode), nullableOrStr(sceneName), strPtrOf(item, "context_code"),
				strPtrOf(item, "render_mode"), strPtrOf(item, "narration"),
				orElse(strPtrOf(item, "shot_type"), strPtrOf(item, "shot_size")), now, now); err != nil {
				return 0, fmt.Errorf("insert storyboard: %w", err)
			}
		}
		item["storyboard_id"] = storyboardID
		entityCode := storyboardCode
		if entityCode == "" {
			entityCode = fmt.Sprintf("%s-F-%d-%d", project.ID, episodeNum, orderNum)
		}
		if _, err := r.insertEntityVersionTx(ctx, tx, model.EntityVersions{
			ProjectId:     &project.ID,
			EntityType:    "storyboard",
			EntityId:      &storyboardID,
			EntityCode:    entityCode,
			Source:        "human_modified",
			Status:        "locked",
			ContentJson:   jsonB(item),
			ChangeSummary: strPtr("人工修正分镜锁定"),
			ChangedFields: json.RawMessage(jsonB([]string{"storyboard"})),
			CreatedBy:     &userID,
			IsCurrent:     true,
		}); err != nil {
			return 0, err
		}
		createdOrUpdated++
	}
	return createdOrUpdated, nil
}

const todoTaskStatus = "todo"

func jsonArrayField(item map[string]any, keys ...string) []byte {
	for _, key := range keys {
		if value, ok := item[key]; ok && value != nil {
			return jsonB(value)
		}
	}
	return []byte(`[]`)
}

func nullableOrStr(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func truncate(value string, n int) string {
	runes := []rune(value)
	if len(runes) <= n {
		return value
	}
	return string(runes[:n])
}

// characterIsNonVisual 对齐 legacy _character_is_non_visual。
func characterIsNonVisual(item map[string]any) bool {
	metadata := map[string]any{}
	if attributes, ok := item["attributes"].(map[string]any); ok {
		for k, v := range attributes {
			metadata[k] = v
		}
	}
	if meta, ok := item["metadata"].(map[string]any); ok {
		for k, v := range meta {
			metadata[k] = v
		}
	}
	value := item["visual_presence"]
	if value == nil {
		value = item["presence_mode"]
	}
	if value == nil {
		value = item["appearance_scope"]
	}
	if metadata["visual_presence"] != nil {
		value = metadata["visual_presence"]
	}
	normalized := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", value)))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	if normalized != "" {
		for _, marker := range []string{"mentioned_only", "mentioned", "背景提及", "voice_only", "voice", "off_screen", "仅提及", "未出场", "不出镜", "画外音", "仅声音"} {
			if strings.Contains(normalized, marker) {
				return true
			}
		}
		return false
	}
	characterType := item["character_type"]
	if characterType == nil {
		characterType = metadata["character_type"]
	}
	typeText := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", characterType)))
	typeText = strings.ReplaceAll(typeText, "-", "_")
	for _, marker := range []string{"mentioned_only", "背景提及", "仅提及", "未出场", "不出镜", "voice_only", "off_screen", "画外音", "仅声音"} {
		if strings.Contains(typeText, marker) {
			return true
		}
	}
	return false
}

// normalizeAssetType 对齐 legacy _normalize_asset_type。
func normalizeAssetType(value string) string {
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "" {
		return "prop"
	}
	switch raw {
	case "人物", "角色":
		return "character"
	case "场景":
		return "scene"
	case "道具":
		return "prop"
	case "character", "scene", "prop":
		return raw
	default:
		return "prop"
	}
}

// assetItemDescription 对齐 legacy _asset_description。
func assetItemDescription(item map[string]any) string {
	attributes := item["attributes"]
	attr, _ := attributes.(map[string]any)
	var parts []string
	if value := strOf(item, "description"); value != "" {
		parts = append(parts, value)
	}
	for _, key := range []string{"appearance", "visual_goal", "scene_description", "core_requirement"} {
		if value := strOf(attr, key); value != "" {
			parts = append(parts, value)
		}
	}
	text := strings.Join(parts, "\n")
	runes := []rune(text)
	if len(runes) > 2000 {
		text = string(runes[:2000])
	}
	return text
}

// assetPromptText 对齐 legacy _asset_prompt_text。
func assetPromptText(item map[string]any) string {
	prompt := strOf(item, "prompt")
	if prompt == "" {
		prompt = strOf(item, "prompt_text")
	}
	if prompt == "" {
		prompt = strOf(item, "stable_prompt")
	}
	name := firstNonEmpty(strOf(item, "name"), strOf(item, "role_name"), strOf(item, "scene_name"), strOf(item, "prop_name"), strOf(item, "asset_code"))
	basePrompt := ""
	if prompt != "" {
		basePrompt = prompt
	} else {
		var parts []string
		if name != "" {
			parts = append(parts, fmt.Sprintf("请生成资产文生图：%s", name))
		}
		parts = append(parts, fmt.Sprintf("资产类型：%s", normalizeAssetType(strOf(item, "asset_type"))))
		requirement := assetItemDescription(item)
		if requirement == "" {
			requirement = "保持与项目视觉风格一致，便于后续分镜生产复用。"
		}
		parts = append(parts, fmt.Sprintf("制作要求：%s", requirement))
		parts = append(parts, fmt.Sprintf("原文依据：%s", firstNonEmpty(strOf(item, "source_text"), strOf(item, "source"))))
		basePrompt = strings.Join(parts, "\n")
	}
	metadata, _ := item["metadata"].(map[string]any)
	costume, _ := metadata["costume_design"].(map[string]any)
	sections := []string{basePrompt}
	if negative := firstNonEmpty(strOf(item, "negative_prompt"), strOf(costume, "negative_prompt")); negative != "" {
		sections = append(sections, fmt.Sprintf("负向要求：%s", negative))
	}
	if characterAPose := firstNonEmptyAny(item["character_apose"], costume["character_apose"]); characterAPose != nil {
		sections = append(sections, fmt.Sprintf("定装姿态：%s", promptValueText(characterAPose)))
	}
	if materialLayers := firstNonEmptyAny(item["material_layers"], costume["material_layers"]); materialLayers != nil {
		sections = append(sections, fmt.Sprintf("材质要求：%s", promptValueText(materialLayers)))
	}
	var hints []map[string]any
	if item["production_hints"] != nil {
		hints = mapList(item["production_hints"])
	}
	if len(hints) > 0 {
		var lines []string
		for _, hint := range hints {
			text := firstNonEmpty(strOf(hint, "human_input"), strOf(hint, "detail"), strOf(hint, "item"), strOf(hint, "message"))
			if text != "" {
				lines = append(lines, text)
			}
		}
		if len(lines) > 0 {
			sections = append(sections, "生产补充提示：\n"+strings.Join(shoutLines(lines), "\n"))
		}
	}
	joined := make([]string, 0, len(sections))
	for _, section := range sections {
		if section != "" {
			joined = append(joined, section)
		}
	}
	return strings.Join(joined, "\n\n")
}

func shoutLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, "- "+line)
	}
	return out
}

func promptValueText(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		parts := make([]string, 0, len(typed))
		for key, item := range typed {
			if item == nil || item == "" || len(fmt.Sprintf("%v", item)) == 0 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s：%s", key, promptValueText(item)))
		}
		return strings.Join(parts, "；")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if item != nil && fmt.Sprintf("%v", item) != "" {
				parts = append(parts, promptValueText(item))
			}
		}
		return strings.Join(parts, "、")
	case []map[string]any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if len(item) > 0 {
				parts = append(parts, promptValueText(item))
			}
		}
		return strings.Join(parts, "、")
	default:
		return fmt.Sprintf("%v", value)
	}
}

func firstNonEmptyAny(values ...any) any {
	for _, value := range values {
		if value != nil && fmt.Sprintf("%v", value) != "" {
			return value
		}
	}
	return nil
}

// assetTaskSpecs 对齐 legacy _asset_task_specs + _asset_priority（S/A 角色 A-E，其余 A）。
func assetTaskSpecs(assetType, priority string) [][3]string {
	if assetType == "character" {
		specs := [][3]string{{"A", "正面全身 A-Pose", "正面平视，全身完整，人物居中，双臂自然展开，纯净背景，作为后续视图的主参考图。"}}
		if priority == "S" || priority == "A" {
			specs = append(specs,
				[3]string{"B", "侧面或 3/4 全身", "引用并严格保持图 A 的脸型、发型、体型和服装，生成侧面或 3/4 全身视图。"},
				[3]string{"C", "背面全身", "引用并严格保持图 A，完整展示发型、服装背部结构和背面配饰。"},
				[3]string{"D", "面部与表情参考", "引用图 A，生成面部近景与核心表情参考，保持五官和发型一致。"},
				[3]string{"E", "服装与材质细节", "引用图 A，展示服装、配饰、纹理和关键材质细节，不改变整体设计。"},
			)
		}
		return specs
	}
	if assetType == "prop" {
		return [][3]string{{"MASTER", "道具母版", "生成道具标准视图，明确外形、比例、材质、颜色、纹理和关键状态。"}}
	}
	return [][3]string{{"MASTER", "场景母版", "生成场景标准母版，明确空间布局、关键区域、主机位、光照和氛围。"}}
}

// assetItemVariantPrompt 对齐 legacy _asset_item_variant_prompt + _asset_variant_prompt。
func assetItemVariantPrompt(item map[string]any, basePrompt, variant, instruction string) string {
	metadata, _ := item["metadata"].(map[string]any)
	if view := assetViewPrompt(item, metadata, variant, nil, nil); len(view) > 0 {
		var parts []string
		viewStr := func(key string) string { return strOf(view, key) }
		if value := viewStr("prompt"); value != "" {
			parts = append(parts, value)
		}
		if value := viewStr("negative_prompt"); value != "" {
			parts = append(parts, fmt.Sprintf("负向要求：%s", value))
		}
		if value := viewStr("pose"); value != "" {
			parts = append(parts, fmt.Sprintf("姿态要求：%s", value))
		}
		if value := viewStr("description"); value != "" {
			parts = append(parts, fmt.Sprintf("图位说明：%s", value))
		}
		if value := viewStr("reference_requirement"); value != "" {
			parts = append(parts, fmt.Sprintf("参考图依赖：%s", value))
		}
		joined := make([]string, 0, len(parts))
		for _, part := range parts {
			if part != "" {
				joined = append(joined, part)
			}
		}
		return strings.Join(joined, "\n\n")
	}
	return assetVariantPrompt(basePrompt, variant, instruction)
}

func assetVariantPrompt(basePrompt, variant, instruction string) string {
	dependency := ""
	if variant == "B" || variant == "C" || variant == "D" || variant == "E" {
		dependency = "\n一致性要求：使用当前生产单元的图 A 作为角色一致性参考，A-E 统一提交审核。"
	}
	return strings.TrimSpace(basePrompt + "\n\n任务图位：" + variant + "\n图位要求：" + instruction + dependency)
}

// createAssetTextToImageTasksTx 对齐 legacy _create_asset_text_to_image_tasks。
func (r *Projects) createAssetTextToImageTasksTx(ctx context.Context, tx pgx.Tx, projectID string, project *model.Projects, content map[string]any, userID string) (int, error) {
	assets := mapList(content["assets"])
	assigneeID, err := r.firstUserIDByRoleTx(ctx, tx, "artist")
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	created := 0
	for _, item := range assets {
		assetType := normalizeAssetType(strOf(item, "asset_type"))
		if assetType == "character" && characterIsNonVisual(item) {
			item["task_ids"] = map[string]any{}
			item["task_id"] = nil
			continue
		}
		assetCode := firstNonEmpty(strOf(item, "asset_code"), assetItemCode(item))
		name := firstNonEmpty(strOf(item, "name"), fmt.Sprintf("资产 %03d", created+1))
		description := assetItemDescription(item)
		promptText := assetPromptText(item)
		var assetID string
		err := tx.QueryRow(ctx,
			`SELECT id FROM assets WHERE project_id=$1 AND asset_code=$2`, projectID, assetCode).Scan(&assetID)
		if err == pgx.ErrNoRows {
			assetID = newUUIDString()
			if _, err := tx.Exec(ctx,
				`INSERT INTO assets (id, project_id, asset_code, asset_type, name, description, status, tags,
				   prompt_text, base_model, metadata_json, version, is_locked, locked_by, locked_at, created_by_id, created_at, updated_at)
				 VALUES ($1,$2,$3,$4,$5,$6,'locked',$7,$8,$9,$10,1,TRUE,$11,$12,$13,$14,$15)`,
				assetID, projectID, assetCode, assetType, name, description, jsonB([]string{assetType, "human_modified"}),
				promptText, firstNonEmpty(strOf(item, "model"), "lib-image"), jsonBOrEmpty(item["metadata"]),
				userID, now, userID, now, now); err != nil {
				return 0, fmt.Errorf("insert asset: %w", err)
			}
		} else if err != nil {
			return 0, err
		}
		// 已有未退役 text_to_image 任务（variant → task id）。
		existingByVariant := map[string]string{}
		rows, err := tx.Query(ctx,
			`SELECT task_variant, id FROM tasks WHERE project_id=$1 AND task_type='text_to_image' AND asset_id=$2 AND is_retired=FALSE`,
			projectID, assetID)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var variant *string
			var taskID string
			if err := rows.Scan(&variant, &taskID); err == nil {
				if variant != nil {
					existingByVariant[*variant] = taskID
				} else {
					existingByVariant["LEGACY"] = taskID
				}
			}
		}
		rows.Close()
		primaryID := existingByVariant["A"]
		priority := assetPriority(item)
		specs := assetTaskSpecs(assetType, priority)
		taskIDs := map[string]any{}
		for _, spec := range specs {
			variant, variantLabel, instruction := spec[0], spec[1], spec[2]
			taskID := ""
			if existingByVariant[variant] != "" {
				taskID = existingByVariant[variant]
			}
			if taskID == "" {
				variantPrompt := assetItemVariantPrompt(item, promptText, variant, instruction)
				taskID = newUUIDString()
				title := fmt.Sprintf("资产生产 %s %s [%s] %s", assetCode, name, variant, variantLabel)
				dependsOn := any(nil)
				if primaryID != "" && (variant == "B" || variant == "C" || variant == "D" || variant == "E") {
					dependsOn = primaryID
				}
				dueOffset := 1
				if variant != "A" && variant != "MASTER" {
					dueOffset = 2
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO tasks (id, project_id, asset_id, task_type, task_variant, depends_on_task_id, title,
					   assignee_id, assigned_by, assigned_at, status, prompt_text, latest_prompt_text, due_at,
					   production_model, is_retired, created_at, updated_at)
					 VALUES ($1,$2,$3,'text_to_image',$4,$5,$6,$7,$8,$9,'todo',$10,$10,$11,$12,FALSE,$13,$13)`,
					taskID, projectID, assetID, variant, dependsOn, title, nullableStr(assigneeID), userID, now,
					variantPrompt, now.AddDate(0, 0, dueOffset), firstNonEmpty(strOf(item, "model"), "lib-image"),
					now); err != nil {
					return 0, fmt.Errorf("insert asset task: %w", err)
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO task_prompts (id, task_id, prompt_type, prompt_text, version_no, source, created_by, created_at, updated_at)
					 VALUES ($1,$2,$3,$4,1,'locked_asset_breakdown',$5,$6,$6)`,
					newUUIDString(), taskID, fmt.Sprintf("asset_text_to_image_%s", strings.ToLower(variant)),
					variantPrompt, userID, now); err != nil {
					return 0, fmt.Errorf("insert task prompt: %w", err)
				}
				created++
			}
			if variant == "A" {
				primaryID = taskID
			}
			taskIDs[variant] = taskID
		}
		item["asset_id"] = assetID
		item["task_ids"] = taskIDs
		defaultTaskID := taskIDs["A"]
		if defaultTaskID == nil {
			defaultTaskID = taskIDs["MASTER"]
		}
		if defaultTaskID == nil {
			defaultTaskID = taskIDs["LEGACY"]
		}
		item["task_id"] = defaultTaskID
	}
	return created, nil
}

func assetItemCode(item map[string]any) string {
	base := strOf(item, "asset_code")
	if base == "" {
		base = strOf(item, "client_asset_key")
	}
	return base
}

func assetPriority(item map[string]any) string {
	metadata, _ := item["metadata"].(map[string]any)
	priority := ""
	for _, source := range []any{metadata["priority"], item["priority"]} {
		if m, ok := source.(map[string]any); ok {
			priority = strOf(m, "priority")
			if priority != "" {
				break
			}
		}
		if _, ok := source.(string); ok {
			priority = fmt.Sprintf("%v", source)
			break
		}
	}
	priority = strings.ToUpper(strings.TrimSpace(priority))
	if priority == "" {
		priority = "C"
	}
	if priority != "S" && priority != "A" && priority != "B" && priority != "C" {
		priority = "C"
	}
	return priority
}

func (r *Projects) firstUserIDByRoleTx(ctx context.Context, tx pgx.Tx, role string) (string, error) {
	var id *string
	err := tx.QueryRow(ctx,
		`SELECT id FROM users WHERE role=$1 AND is_active=TRUE ORDER BY created_at LIMIT 1`, role).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if id == nil {
		return "", nil
	}
	return *id, nil
}

// insertLockedTrainingSampleTx 对齐 legacy _build_breakdown_feedback_training_sample：
// 落 agent_training_samples 一条 script_breakdown_locked 样本（payload 内联，不做 MinIO 外化，P3f 接入）。
func (r *Projects) insertLockedTrainingSampleTx(ctx context.Context, tx pgx.Tx, projectID, entityCode string, content map[string]any, userID string) error {
	contentSummary := map[string]any{
		"content_hash":      ContentHashString(content),
		"storyboards_count": len(mapList(content["storyboards"])),
		"assets_count":      len(mapList(content["assets"])),
	}
	summaryList := []any{"人工修正版本已锁定"}
	switch typed := content["change_summary"].(type) {
	case []string:
		if len(typed) > 0 {
			summaryList = []any{}
			for _, s := range typed {
				summaryList = append(summaryList, s)
			}
		}
	case []any:
		if len(typed) > 0 {
			summaryList = typed
		}
	}
	now := time.Now().UTC()
	_, err := tx.Exec(ctx,
		`INSERT INTO agent_training_samples (id, project_id, agent_type, sample_type, entity_type, entity_code,
		   input_json, agent_output_json, human_modified_output_json, confirmed_output_json, change_summary,
		   training_tags, training_ready, created_by, data_state, created_at, updated_at)
		 VALUES ($1,$2,'script_breakdown','script_breakdown_locked','script_breakdown',$3,$4,$5,$6,$7,$8,$9,TRUE,$10,'final',$11,$11)`,
		newUUIDString(), projectID, entityCode,
		jsonB(mapValue(content["input_json"])), jsonB(mapValue(content["raw_output"])),
		jsonB(contentSummary), jsonB(content),
		jsonB(summaryList),
		jsonB([]string{"human_modified", "locked", "version_control", "episode_breakdown"}),
		userID, now)
	if err != nil {
		return fmt.Errorf("insert training sample: %w", err)
	}
	return nil
}

func mapValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// assetBelongsToEpisode 对齐 legacy _asset_belongs_to_episode。
func assetBelongsToEpisode(item map[string]any, episodeCode string) bool {
	for _, key := range []string{"episode_code", "corresponding_episode", "episode"} {
		if NormalizeEpisodeCode(strOf(item, key)) == NormalizeEpisodeCode(episodeCode) {
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
		if typed != "" && strings.Contains(strings.ToUpper(typed), strings.ToUpper(NormalizeEpisodeCode(episodeCode))) {
			return true
		}
	case []any:
		for _, value := range typed {
			if strings.Contains(strings.ToUpper(fmt.Sprintf("%v", value)), strings.ToUpper(NormalizeEpisodeCode(episodeCode))) {
				return true
			}
		}
	}
	for _, key := range []string{"episode_code", "corresponding_episode", "episode"} {
		if strOf(item, key) != "" {
			return false
		}
	}
	return true
}

// sceneCodeName 对齐 legacy _storyboard_scene_code_name。
func sceneCodeName(item map[string]any) (string, string) {
	rawCode := item["scene_code"]
	if rawCode == nil {
		rawCode = item["scene_id"]
	}
	if rawCode == nil {
		rawCode = item["scene_codes"]
	}
	switch typed := rawCode.(type) {
	case []any:
		if len(typed) > 0 {
			rawCode = typed[0]
		} else {
			rawCode = nil
		}
	case string:
		if typed == "" {
			rawCode = nil
		}
	}
	code := ""
	if rawCode != nil {
		code = strings.TrimSpace(fmt.Sprintf("%v", rawCode))
	}
	rawName := item["scene_name"]
	if rawName == nil {
		rawName = item["scene"]
	}
	switch typed := rawName.(type) {
	case []any:
		if len(typed) > 0 {
			rawName = typed[0]
		} else {
			rawName = nil
		}
	case string:
		if typed == "" {
			rawName = nil
		}
	}
	name := ""
	if rawName != nil {
		name = strings.TrimSpace(fmt.Sprintf("%v", rawName))
	}
	return code, name
}

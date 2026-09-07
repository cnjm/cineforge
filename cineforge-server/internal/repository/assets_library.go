package repository

// P3f-c 资产库聚合（对齐 legacy list_asset_library / _list_projects_asset_library /
// _asset_library_item / _storyboard_library_item）：把已定版 AssetVersion 与已审核候选
//（primary/alternate submissions）组装成统一 AssetLibraryItemRead，含规范显示名。

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"cineforge/server/internal/model"
)

// ---- 视图模型（对齐 AssetLibraryItemRead / AssetLibraryVersionRead / AssetLibraryHierarchy） ----

type AssetLibraryHierarchyView struct {
	ProjectId          *string `json:"project_id"`
	ProjectPrefix      string  `json:"project_prefix"`
	EpisodeId          *string `json:"episode_id"`
	EpisodeCode        *string `json:"episode_code"`
	SceneCode          *string `json:"scene_code"`
	SceneName          *string `json:"scene_name"`
	StoryboardId       *string `json:"storyboard_id"`
	StoryboardCode     *string `json:"storyboard_code"`
	ScriptSegmentId    *string `json:"script_segment_id"`
	ScriptSegmentCode  *string `json:"script_segment_code"`
	MirrorShotCode     *string `json:"mirror_shot_code"`
	AssetId            *string `json:"asset_id"`
	AssetCode          *string `json:"asset_code"`
	AssetType          *string `json:"asset_type"`
	OutputType         string  `json:"output_type"`
	ArtifactCode       string  `json:"artifact_code"`
	TaskId             *string `json:"task_id"`
	TaskType           *string `json:"task_type"`
	DependsOnTaskId    *string `json:"depends_on_task_id"`
	TaskVariant        *string `json:"task_variant"`
	AgeStageCode       *string `json:"age_stage_code"`
	CostumeVariantCode *string `json:"costume_variant_code"`
}

type AssetLibraryVersionView struct {
	ID                   string                    `json:"id"`
	SourceType           string                    `json:"source_type"`
	VersionNo            int32                     `json:"version_no"`
	ContextVersionNo     *int32                    `json:"context_version_no"`
	CanonicalDisplayName string                    `json:"canonical_display_name"`
	OriginalFileName     *string                   `json:"original_file_name"`
	FileId               *string                   `json:"file_id"`
	FilePath             *string                   `json:"file_path"`
	MimeType             *string                   `json:"mime_type"`
	FileSize             *int32                    `json:"file_size"`
	ModelName            *string                   `json:"model_name"`
	ToolName             *string                   `json:"tool_name"`
	ViewLabel            *string                   `json:"view_label"`
	StateLabel           *string                   `json:"state_label"`
	Description          *string                   `json:"description"`
	BatchId              *string                   `json:"batch_id"`
	BatchVersionNo       *int32                    `json:"batch_version_no"`
	Status               string                    `json:"status"`
	IsPrimary            bool                      `json:"is_primary"`
	IsCurrent            bool                      `json:"is_current"`
	IsContextCurrent     bool                      `json:"is_context_current"`
	IsCardMaster         bool                      `json:"is_card_master"`
	Hierarchy            AssetLibraryHierarchyView `json:"hierarchy"`
	CreatedAt            time.Time                 `json:"created_at"`
}

type AssetLibraryItemView struct {
	LibraryKey      string                    `json:"library_key"`
	SourceType      string                    `json:"source_type"`
	SourceId        string                    `json:"source_id"`
	ProjectId       *string                   `json:"project_id"`
	ProjectCode     *string                   `json:"project_code"`
	ProjectName     *string                   `json:"project_name"`
	EpisodeCodes    []string                  `json:"episode_codes"`
	SourceLabel     *string                   `json:"source_label"`
	Name            string                    `json:"name"`
	Description     *string                   `json:"description"`
	AssetType       *string                   `json:"asset_type"`
	OutputType      string                    `json:"output_type"`
	CurrentMaster   *AssetLibraryVersionView  `json:"current_master"`
	LatestVersionAt time.Time                 `json:"latest_version_at"`
	VersionCount    int                       `json:"version_count"`
	Versions        []AssetLibraryVersionView `json:"versions"`
}

var uuidPatternRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// uuidOrNone 对齐 _uuid_or_none：非 UUID 文本 → nil。
func uuidOrNone(value any) *string {
	text := strings.TrimSpace(strAny(value, ""))
	if !uuidPatternRe.MatchString(text) {
		return nil
	}
	return &text
}

func toolNamesToList(raw json.RawMessage) []string {
	var names []string
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &names); err != nil {
			// tool_names 可能是字符串或对象；不合法时回退空。
			var single string
			if err2 := json.Unmarshal(raw, &single); err2 == nil && single != "" {
				return []string{single}
			}
			return nil
		}
	}
	return names
}

// ---- GET /assets/library ----

// ListAssetLibrary 对齐 list_asset_library。
func (r *Assets) ListAssetLibrary(ctx context.Context, actor TaskActor, projectID *string, includeVersions bool) ([]AssetLibraryItemView, error) {
	var projects []*model.Projects
	if projectID != nil {
		project, err := r.projects.FindByID(ctx, *projectID)
		if err != nil {
			return nil, err
		}
		if project == nil || project.DeletedAt != nil {
			return nil, &KeyError{Arg: *projectID}
		}
		if !isDirectorOrAdminRole(actor.Role) {
			ok, canErr := r.canAccessProject(ctx, actor, *projectID)
			if canErr != nil {
				return nil, canErr
			}
			if !ok {
				return nil, &PermissionError{Arg: *projectID}
			}
		}
		projects = []*model.Projects{project}
	} else {
		rows, err := r.pool.Query(ctx,
			`SELECT `+projectColumns+` FROM projects WHERE deleted_at IS NULL ORDER BY created_at`)
		if err != nil {
			return nil, fmt.Errorf("list asset library: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			p, scanErr := scanProject(rows)
			if scanErr != nil {
				return nil, fmt.Errorf("list asset library: %w", scanErr)
			}
			projects = append(projects, p)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("list asset library: %w", err)
		}
	}
	items, err := r.listProjectsAssetLibrary(ctx, projects, includeVersions)
	if err != nil {
		return nil, err
	}
	visible := make([]AssetLibraryItemView, 0, len(items))
	for _, item := range items {
		if item.CurrentMaster != nil {
			visible = append(visible, item)
		}
	}
	// 按 latest_version_at 倒序（stable：同时间保持组装顺序 → 直接遍历后冒泡按时间降序）。
	for i := 1; i < len(visible); i++ {
		for j := i; j > 0 && visible[j-1].LatestVersionAt.Before(visible[j].LatestVersionAt); j-- {
			visible[j-1], visible[j] = visible[j], visible[j-1]
		}
	}
	return visible, nil
}

// storyboardGroupKey 对齐 _list_projects_asset_library 的 output_groups 键
// (project_id, storyboard_id, mirror_shot_code, output_type)。
func storyboardGroupKey(projectID, storyboardID string, mirrorShotCode *string, outputType string) string {
	mirror := ""
	if mirrorShotCode != nil {
		mirror = *mirrorShotCode
	}
	return strings.Join([]string{projectID, storyboardID, mirror, outputType}, "\x1f")
}

// listProjectsAssetLibrary 对齐 _list_projects_asset_library。
func (r *Assets) listProjectsAssetLibrary(ctx context.Context, projects []*model.Projects, includeVersions bool) ([]AssetLibraryItemView, error) {
	if len(projects) == 0 {
		return nil, nil
	}
	projectIDs := make([]string, len(projects))
	prefixes := map[string]string{}
	projectNames := map[string]string{}
	for i, p := range projects {
		projectIDs[i] = p.ID
		prefix := ""
		if p.ProjectPrefix != nil && *p.ProjectPrefix != "" {
			prefix = *p.ProjectPrefix
		} else if p.ProjectNo != nil {
			prefix = *p.ProjectNo
		}
		if prefix == "" {
			prefix = "RF"
		}
		prefixes[p.ID] = prefix
		name := p.Title
		if p.Name != nil && *p.Name != "" {
			name = *p.Name
		}
		if name == "" && p.ProjectNo != nil {
			name = *p.ProjectNo
		}
		if name == "" {
			name = "未命名项目"
		}
		projectNames[p.ID] = name
	}

	assets, err := r.assetsByProjectIDs(ctx, projectIDs)
	if err != nil {
		return nil, err
	}
	assetIDs := make([]string, 0, len(assets))
	assetIndex := map[string]*model.Assets{}
	for _, asset := range assets {
		assetIDs = append(assetIDs, asset.ID)
		assetIndex[asset.ID] = asset
	}

	episodeCodesByAsset, err := r.episodeCodesByAssetIDs(ctx, assetIDs)
	if err != nil {
		return nil, err
	}
	// 临时资产业务前缀的 episode_code 来自 asset.metadata_json（对齐 legacy 收尾段）。
	for _, asset := range assets {
		assetMetadata := metadataAsMap(asset.MetadataJson)
		temporaryEpisode := strings.TrimSpace(strAny(assetMetadata["episode_code"], ""))
		if temporaryEpisode != "" {
			codeList := episodeCodesByAsset[asset.ID]
			has := false
			for _, existing := range codeList {
				if existing == temporaryEpisode {
					has = true
					break
				}
			}
			if !has {
				codeList = append(codeList, temporaryEpisode)
				sortStrings(codeList)
				episodeCodesByAsset[asset.ID] = codeList
			}
		}
	}

	versionRows, err := r.assetVersionRows(ctx, assetIDs)
	if err != nil {
		return nil, err
	}
	versionsByAsset := map[string][]*assetVersionRowForLibrary{}
	for _, row := range versionRows {
		versionsByAsset[row.version.AssetId] = append(versionsByAsset[row.version.AssetId], row)
	}

	submissionRows, err := r.projectSubmissionRows(ctx, projectIDs)
	if err != nil {
		return nil, err
	}
	scriptSegmentCodes, err := r.scriptSegmentCodesForRows(ctx, submissionRows)
	if err != nil {
		return nil, err
	}
	submissionsByAsset := map[string][]*librarySubmissionRow{}
	outputGroups := map[string][]*librarySubmissionRow{}
	assetIDSet := map[string]bool{}
	for _, assetID := range assetIDs {
		assetIDSet[assetID] = true
	}
	for _, row := range submissionRows {
		if !isApprovedLibrarySubmission(row.sub) {
			continue
		}
		if row.task.AssetId != nil && assetIDSet[*row.task.AssetId] {
			submissionsByAsset[*row.task.AssetId] = append(submissionsByAsset[*row.task.AssetId], row)
			continue
		}
		if row.sb == nil {
			continue
		}
		normalizedStep := strings.ToLower(strAny(row.sub.Step, ""))
		outputType := "keyframe"
		if strings.Contains(normalizedStep, "video") || row.sub.FileType == "video" {
			outputType = "video"
		}
		mirrorShotCode := submissionMirrorShotCode(row.task.TaskVariant, row.sub.Step)
		key := storyboardGroupKey(row.task.ProjectId, row.sb.ID, mirrorShotCode, outputType)
		outputGroups[key] = append(outputGroups[key], row)
	}

	var items []AssetLibraryItemView
	for _, asset := range assets {
		if len(submissionsByAsset[asset.ID]) == 0 {
			continue
		}
		item := r.assetLibraryItem(prefixes[*asset.ProjectId], projectNames[*asset.ProjectId], episodeCodesByAsset[asset.ID],
			versionsByAsset[asset.ID], submissionsByAsset[asset.ID], includeVersions, asset)
		items = append(items, item)
	}
	for key, rows := range outputGroups {
		parts := strings.Split(key, "\x1f")
		projectID := parts[0]
		outputType := parts[3]
		// 重建行指针以还原 storyboard group 键（project/type 存储于键中）。
		for _, row := range rows {
			if row.sb == nil {
				continue
			}
			item := r.storyboardLibraryItem(prefixes[projectID], outputType, rows,
				projectNames[projectID], scriptSegmentCodes, includeVersions, row.sb)
			items = append(items, item)
			break
		}
	}
	return items, nil
}

func isApprovedLibrarySubmission(sub *model.Submissions) bool {
	return approvedSubmissionStatuses[sub.Status] && sub.IsSelected && !sub.IsArchived && !sub.IsInvalidated
}

// ---- 聚合子查询 ----

func (r *Assets) assetsByProjectIDs(ctx context.Context, projectIDs []string) ([]*model.Assets, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+assetColumns+` FROM assets
		 WHERE project_id = ANY($1) AND status NOT IN ('deleted','excluded')
		 ORDER BY project_id, asset_type, asset_code, name`, projectIDs)
	if err != nil {
		return nil, fmt.Errorf("library assets: %w", err)
	}
	defer rows.Close()
	var out []*model.Assets
	for rows.Next() {
		a, scanErr := scanAsset(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("library assets: %w", scanErr)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// episodeCodesByAssetIDs 聚合 active 绑定 + 任务的 episode_code（去重后用返回上下文中排序）。
func (r *Assets) episodeCodesByAssetIDs(ctx context.Context, assetIDs []string) (map[string][]string, error) {
	result := map[string][]string{}
	add := func(assetID, code string) {
		if code == "" {
			return
		}
		list := result[assetID]
		for _, existing := range list {
			if existing == code {
				return
			}
		}
		result[assetID] = append(list, code)
	}
	if len(assetIDs) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT b.asset_id, e.episode_code
		 FROM episode_asset_bindings b JOIN project_episodes e ON e.id = b.episode_id
		 WHERE b.asset_id = ANY($1) AND b.status = 'active'`, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("library episode codes: %w", err)
	}
	for rows.Next() {
		var assetID, code string
		if scanErr := rows.Scan(&assetID, &code); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("library episode codes: %w", scanErr)
		}
		add(assetID, code)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library episode codes: %w", err)
	}
	rows, err = r.pool.Query(ctx,
		`SELECT DISTINCT t.asset_id, e.episode_code
		 FROM tasks t JOIN project_episodes e ON e.id = t.episode_id
		 WHERE t.asset_id = ANY($1) AND t.episode_id IS NOT NULL AND t.is_retired = FALSE`, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("library episode codes: %w", err)
	}
	for rows.Next() {
		var assetID, code string
		if scanErr := rows.Scan(&assetID, &code); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("library episode codes: %w", scanErr)
		}
		add(assetID, code)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library episode codes: %w", err)
	}
	for _, list := range result {
		// 排序（对齐 legacy 的 codes.sort()）。
		sortStrings(list)
	}
	// 临时资产 episode_code 由 Aasset.Metadata 提供 → 在调用方补入。
	return result, nil
}

func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j-1] > list[j]; j-- {
			list[j-1], list[j] = list[j], list[j-1]
		}
	}
}

type assetVersionRowForLibrary struct {
	version *model.AssetVersions
	file    *model.Files
}

func (r *Assets) assetVersionRows(ctx context.Context, assetIDs []string) ([]*assetVersionRowForLibrary, error) {
	query := `SELECT v.` + strings.ReplaceAll(assetVersionColumns, ",", ", v.") +
		`, f.id, f.file_name, f.mime_type, f.file_size
		FROM asset_versions v LEFT OUTER JOIN files f ON f.id = COALESCE(v.file_id, v.preview_file_id)
		WHERE v.asset_id = ANY($1) AND v.is_invalidated = FALSE AND v.purged_at IS NULL
		ORDER BY v.asset_id, v.version_no`
	rows, err := r.pool.Query(ctx, query, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("library versions: %w", err)
	}
	defer rows.Close()
	var out []*assetVersionRowForLibrary
	for rows.Next() {
		var v model.AssetVersions
		var f model.Files
		err := rows.Scan(&v.ID, &v.AssetId, &v.VersionNo, &v.AssetVersionCode, &v.FileId,
			&v.PreviewFileId, &v.PromptText, &v.NegativePromptText, &v.BaseModel, &v.ToolName,
			&v.MetadataJson, &v.IsCurrent, &v.SourceTaskId, &v.SourceAgentRunId, &v.CreatedBy,
			&v.SourceSubmissionId, &v.SourceMasterSubmissionId, &v.IsInvalidated, &v.InvalidatedAt,
			&v.InvalidatedById, &v.InvalidatedReason, &v.InvalidatedSourceId, &v.PurgedAt,
			&v.CreatedAt, &v.UpdatedAt,
			&f.ID, &f.FileName, &f.MimeType, &f.FileSize)
		if err != nil {
			return nil, fmt.Errorf("library versions: %w", err)
		}
		out = append(out, &assetVersionRowForLibrary{version: &v, file: &f})
	}
	return out, rows.Err()
}

type librarySubmissionRow struct {
	sub   *model.Submissions
	task  *model.Tasks
	sb    *model.Storyboards
	file  *model.Files
	batch *model.TaskSubmissionBatches
}

// libraryStoryboardCols storyboards 表该查询需要的字段子集。
const libraryStoryboardCols = `sb.id, sb.project_id, sb.episode_num, sb.order_num, sb.title, sb.description,
	sb.scene_code, sb.scene_name, sb.script_segment_id, sb.storyboard_code`

func (r *Assets) projectSubmissionRows(ctx context.Context, projectIDs []string) ([]*librarySubmissionRow, error) {
	query := `SELECT s.` + strings.ReplaceAll(submissionColumns, ",", ", s.") +
		`, t.` + strings.ReplaceAll(taskColumns, ",", ", t.") +
		`, ` + libraryStoryboardCols +
		`, f.id, f.file_name, f.mime_type, f.file_size` +
		`, b.id, b.task_id, b.step, b.version_no, b.status, b.submitted_by_id, b.submitted_at,
		 b.reviewed_by_id, b.reviewed_at, b.review_comment, b.submit_request_id, b.review_request_id,
		 b.created_at, b.updated_at` +
		`
		FROM submissions s
		JOIN tasks t ON t.id = s.task_id
		LEFT OUTER JOIN storyboards sb ON sb.id = t.storyboard_id
		LEFT OUTER JOIN files f ON f.id = s.file_id
		LEFT OUTER JOIN task_submission_batches b ON b.id = s.batch_id
		WHERE t.project_id = ANY($1) AND t.is_retired = FALSE
		  AND s.status = ANY($2) AND s.is_selected = TRUE
		  AND s.is_archived = FALSE AND s.is_invalidated = FALSE
		ORDER BY s.created_at, s.id`
	args := []any{projectIDs, []string{submissionStatusPrimaryMaster, submissionStatusAlternateMaster}}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("library submissions: %w", err)
	}
	defer rows.Close()
	var out []*librarySubmissionRow
	for rows.Next() {
		row := &librarySubmissionRow{sub: &model.Submissions{}, task: &model.Tasks{}, sb: &model.Storyboards{}, file: &model.Files{}, batch: &model.TaskSubmissionBatches{}}
		s := row.sub
		t := row.task
		sb := row.sb
		f := row.file
		b := row.batch
		cols := scanLibraryRowDestinations(s, t, sb, f, b)
		if err := rows.Scan(cols...); err != nil {
			return nil, fmt.Errorf("library submissions: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func scanLibraryRowDestinations(s *model.Submissions, t *model.Tasks, sb *model.Storyboards, f *model.Files, b *model.TaskSubmissionBatches) []any {
	return []any{
		// submissions
		&s.ID, &s.TaskId, &s.BatchId, &s.FileId, &s.StoryboardId, &s.FilePath, &s.FileType,
		&s.Step, &s.PromptText, &s.OriginalPromptText, &s.RevisedPromptText, &s.ViewLabel,
		&s.StateLabel, &s.Description, &s.ModelName, &s.ToolNames, &s.SubmittedById, &s.Status,
		&s.IsSelected, &s.IsPrimary, &s.IsArchived, &s.ArchivedById, &s.ArchivedAt,
		&s.SourceMasterSubmissionId, &s.IsInvalidated, &s.InvalidatedAt, &s.InvalidatedById,
		&s.InvalidatedReason, &s.InvalidatedSourceId, &s.PurgedAt, &s.CreatedAt, &s.UpdatedAt,
		// tasks
		&t.ID, &t.ProjectId, &t.EpisodeId, &t.ScriptId, &t.ScriptVersionId, &t.IDempotencyKey,
		&t.ScriptSegmentId, &t.StoryboardId, &t.AssetId, &t.SceneCode, &t.SceneName, &t.TaskType,
		&t.Title, &t.AssigneeId, &t.AssignedBy, &t.AssignedAt, &t.Status, &t.PromptText,
		&t.LatestPromptText, &t.DueAt, &t.CompletedAt, &t.VisibleUntil, &t.ProductionModel,
		&t.MediaType, &t.TaskVariant, &t.AgeStageCode, &t.CostumeVariantCode, &t.VariantPlanId,
		&t.VariantKind, &t.VariantTitleZh, &t.VariantDescriptionZh, &t.PromptRevisionId,
		&t.AssetContextOutdated, &t.DependsOnTaskId, &t.IsRetired, &t.RetiredAt, &t.RetiredBy,
		&t.RetiredReason, &t.CreatedAt, &t.UpdatedAt,
		// storyboards（子集）
		&sb.ID, &sb.ProjectId, &sb.EpisodeNum, &sb.OrderNum, &sb.Title, &sb.Description,
		&sb.SceneCode, &sb.SceneName, &sb.ScriptSegmentId, &sb.StoryboardCode,
		// files（子集）
		&f.ID, &f.FileName, &f.MimeType, &f.FileSize,
		// batches
		&b.ID, &b.TaskId, &b.Step, &b.VersionNo, &b.Status, &b.SubmittedById, &b.SubmittedAt,
		&b.ReviewedById, &b.ReviewedAt, &b.ReviewComment, &b.SubmitRequestId, &b.ReviewRequestId,
		&b.CreatedAt, &b.UpdatedAt,
	}
}

// scriptSegmentCodesForRows 对齐 _script_segment_codes_for_submission_rows。
func (r *Assets) scriptSegmentCodesForRows(ctx context.Context, rows []*librarySubmissionRow) (map[string]string, error) {
	ids := map[string]bool{}
	for _, row := range rows {
		var id *string
		if row.task.ScriptSegmentId != nil {
			id = row.task.ScriptSegmentId
		} else if row.sb != nil {
			id = row.sb.ScriptSegmentId
		}
		if id != nil {
			ids[*id] = true
		}
	}
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	idList := make([]string, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	qr, err := r.pool.Query(ctx,
		`SELECT id, script_segment_code FROM script_segments WHERE id = ANY($1)`, idList)
	if err != nil {
		return nil, fmt.Errorf("library script segment codes: %w", err)
	}
	defer qr.Close()
	result := map[string]string{}
	for qr.Next() {
		var id, code string
		if scanErr := qr.Scan(&id, &code); scanErr != nil {
			return nil, fmt.Errorf("library script segment codes: %w", scanErr)
		}
		result[id] = code
	}
	return result, qr.Err()
}

// ---- 条目组装 ----

// assetLibraryItem 对齐 _asset_library_item。
func (r *Assets) assetLibraryItem(projectPrefix, projectName string, episodeCodes []string,
	storedVersions []*assetVersionRowForLibrary, submissionRows []*librarySubmissionRow,
	includeVersions bool, asset *model.Assets) AssetLibraryItemView {
	var versions []*AssetLibraryVersionView
	contextVersionCounters := map[string]int32{}
	alternateCounters := map[string]int32{}
	approvedSubmissionIDs := map[string]bool{}
	for _, row := range submissionRows {
		approvedSubmissionIDs[row.sub.ID] = true
	}
	representedSubmissionIDs := map[string]bool{}
	assetMetadata := metadataAsMap(asset.MetadataJson)
	episodeCodeAsset := strAny(assetMetadata["episode_code"], "")
	var episodeCodePtr *string
	if episodeCodeAsset != "" {
		episodeCodePtr = &episodeCodeAsset
	}
	for _, row := range storedVersions {
		v := row.version
		metadata := metadataAsMap(v.MetadataJson)
		fileID := v.FileId
		if fileID == nil {
			fileID = v.PreviewFileId
		}
		if v.SourceSubmissionId == nil || !approvedSubmissionIDs[*v.SourceSubmissionId] {
			continue
		}
		representedSubmissionIDs[*v.SourceSubmissionId] = true
		hierarchy := assetVersionHierarchy(projectPrefix, metadata, asset, v.SourceTaskId)
		contextVersionNo, contextKey := assetContextVersion(asset.AssetType, metadata, v.VersionNo, contextVersionCounters)
		isPrimary := bool(metadata["is_primary"] == true)
		if !isPrimary {
			isPrimary = v.IsCurrent
		}
		alternateNo := assetAlternateNumber(isPrimary, contextKey, contextVersionNo, alternateCounters)
		extension := strAny(asset.FilePath, "bin")
		if row.file != nil && row.file.FileName != "" {
			extension = row.file.FileName
		}
		canonicalName := canonicalAssetDisplayName(
			orStringPtr(projectPrefix), asset.AssetType, asset.AssetCode, v.VersionNo, extension,
			orString(metadata["task_variant"], ""), orStringPtr(metadata["age_stage_code"]),
			orStringPtr(metadata["costume_variant_code"]), contextVersionNo, alternateNo,
			orStringPtr(assetMetadata["audio_type"]), episodeCodePtr)
		fileName := ""
		if row.file != nil {
			fileName = row.file.FileName
		}
		var originalName *string
		if fileName != "" {
			originalName = &fileName
		}
		var filePath *string
		if asset.CurrentVersionId != nil && v.ID == *asset.CurrentVersionId {
			filePath = fileLocation(tRowFile(row.file), asset.FilePath)
		} else {
			filePath = fileLocation(tRowFile(row.file), nil)
		}
		var contextVersionNoPtr *int32
		if asset.AssetType == assetTypeCharacter {
			contextVersionNoPtr = &contextVersionNo
		}
		viewLabel := strings.TrimSpace(strAny(metadata["view_label"], ""))
		stateLabel := strings.TrimSpace(strAny(metadata["state_label"], ""))
		description := strings.TrimSpace(strAny(metadata["submission_description"], ""))
		statusStr := strAny(metadata["submission_status"], "")
		if statusStr == "" {
			if v.IsCurrent {
				statusStr = "current"
			} else {
				statusStr = "version"
			}
		}
		var batchVersionNo *int32
		if n := safeInt(firstNonNil(metadata["batch_version_no"], nil), 0); n > 0 {
			batchVersionNo = &n
		}
		versions = append(versions, &AssetLibraryVersionView{
			ID:                   v.ID,
			SourceType:           "asset_version",
			VersionNo:            v.VersionNo,
			ContextVersionNo:     contextVersionNoPtr,
			CanonicalDisplayName: canonicalName,
			OriginalFileName:     originalName,
			FileId:               fileID,
			FilePath:             filePath,
			MimeType:             tRowMime(row.file),
			FileSize:             tRowSize(row.file),
			ModelName:            v.BaseModel,
			ToolName:             v.ToolName,
			ViewLabel:            emptyToNil(viewLabel),
			StateLabel:           emptyToNil(stateLabel),
			Description:          emptyToNil(description),
			BatchId:              uuidOrNone(metadata["batch_id"]),
			BatchVersionNo:       batchVersionNo,
			Status:               statusStr,
			IsPrimary:            isPrimary,
			IsCurrent:            v.IsCurrent,
			IsContextCurrent:     v.IsCurrent,
			IsCardMaster:         asset.CurrentVersionId != nil && v.ID == *asset.CurrentVersionId,
			Hierarchy:            hierarchy,
			CreatedAt:            v.CreatedAt,
		})
	}
	nextVersion := int32(0)
	for _, v := range versions {
		if v.VersionNo > nextVersion {
			nextVersion = v.VersionNo
		}
	}
	for _, row := range submissionRows {
		if representedSubmissionIDs[row.sub.ID] {
			continue
		}
		nextVersion++
		hierarchy := assetTaskHierarchy(projectPrefix, asset, row.task)
		submissionMetadata := map[string]any{
			"task_variant":         row.task.TaskVariant,
			"age_stage_code":       row.task.AgeStageCode,
			"costume_variant_code": row.task.CostumeVariantCode,
		}
		if row.batch != nil {
			submissionMetadata["batch_version_no"] = row.batch.VersionNo
		}
		contextVersionNo, contextKey := assetContextVersion(asset.AssetType, submissionMetadata, nextVersion, contextVersionCounters)
		alternateNo := assetAlternateNumber(row.sub.IsPrimary, contextKey, contextVersionNo, alternateCounters)
		extension := row.sub.FilePath
		if row.file != nil && row.file.FileName != "" {
			extension = row.file.FileName
		}
		canonicalName := canonicalAssetDisplayName(
			orStringPtr(projectPrefix), asset.AssetType, asset.AssetCode, nextVersion, extension,
			row.task.TaskVariant, row.task.AgeStageCode, row.task.CostumeVariantCode,
			contextVersionNo, alternateNo, orStringPtr(assetMetadata["audio_type"]), episodeCodePtr)
		fullName := ""
		if row.file != nil {
			fullName = row.file.FileName
		}
		var originalName *string
		if fullName != "" {
			originalName = &fullName
		}
		var contextVersionNoPtr *int32
		if asset.AssetType == assetTypeCharacter {
			contextVersionNoPtr = &contextVersionNo
		}
		modelName := strAny(row.sub.ModelName, "")
		if modelName == "" {
			modelName = strAny(row.task.ProductionModel, "")
		}
		toolNames := strings.Join(toolNamesToList(row.sub.ToolNames), ", ")
		var batchVersionNoPtr *int32
		if row.batch != nil {
			batchVersionNoPtr = &row.batch.VersionNo
		}
		versions = append(versions, &AssetLibraryVersionView{
			ID:                   row.sub.ID,
			SourceType:           "submission",
			VersionNo:            nextVersion,
			ContextVersionNo:     contextVersionNoPtr,
			CanonicalDisplayName: canonicalName,
			OriginalFileName:     originalName,
			FileId:               row.sub.FileId,
			FilePath:             fileLocation(tRowFile(row.file), orStringPtr(row.sub.FilePath)),
			MimeType:             tRowMime(row.file),
			FileSize:             tRowSize(row.file),
			ModelName:            orNilString(emptyToNil(modelName)),
			ToolName:             orNilString(emptyToNil(toolNames)),
			ViewLabel:            submissionStrPtr(row.sub.ViewLabel),
			StateLabel:           submissionStrPtr(row.sub.StateLabel),
			Description:          submissionStrPtr(row.sub.Description),
			BatchId:              row.sub.BatchId,
			BatchVersionNo:       batchVersionNoPtr,
			Status:               row.sub.Status,
			IsPrimary:            row.sub.IsPrimary,
			IsCurrent:            row.sub.IsPrimary,
			IsContextCurrent:     row.sub.IsPrimary,
			IsCardMaster:         false,
			Hierarchy:            hierarchy,
			CreatedAt:            row.sub.CreatedAt,
		})
	}
	principalVariants := map[string]bool{"A": true, "MASTER": true}
	var principal []*AssetLibraryVersionView
	for _, v := range versions {
		taskVariant := strAny(v.Hierarchy.TaskVariant, "")
		if asset.AssetType != assetTypeCharacter {
			if taskVariant == "" || principalVariants[taskVariant] {
				principal = append(principal, v)
			}
		} else if principalVariants[taskVariant] {
			principal = append(principal, v)
		}
	}
	inPrincipal := func(v *AssetLibraryVersionView) bool {
		for _, p := range principal {
			if p == v {
				return true
			}
		}
		return false
	}
	var cardMaster *AssetLibraryVersionView
	if asset.AssetType == assetTypeCharacter {
		for _, v := range versions {
			v.IsCardMaster = false
		}
		for _, v := range versions {
			if v.IsCardMaster && inPrincipal(v) {
				cardMaster = v
				break
			}
		}
	} else {
		for _, v := range versions {
			if v.IsCardMaster {
				cardMaster = v
				break
			}
		}
	}
	if cardMaster == nil {
		for _, v := range principal {
			if !v.IsPrimary && !v.IsContextCurrent {
				continue
			}
			if cardMaster == nil ||
				v.CreatedAt.After(cardMaster.CreatedAt) ||
				(v.CreatedAt.Equal(cardMaster.CreatedAt) && v.VersionNo > cardMaster.VersionNo) {
				cardMaster = v
			}
		}
		if cardMaster != nil {
			cardMaster.IsCardMaster = true
		}
	}
	// 排序：version_no desc, created_at desc（stable → 反转加入顺序以保持同键顺序稳定）。
	stableSortVersions(versions)
	// 汇出值切片。
	versionValues := make([]AssetLibraryVersionView, len(versions))
	for i, v := range versions {
		versionValues[i] = *v
	}
	source := "reusable_asset"
	if strAny(assetMetadata["source"], "") == "human_temporary" {
		source = "human_temporary"
	}
	sourceLabel := "项目资产定版"
	if source == "human_temporary" {
		sourceLabel = "人工临时添加"
	}
	latestVersionAt := time.Time{}
	versionCount := len(versions)
	for _, v := range versions {
		if v.CreatedAt.After(latestVersionAt) {
			latestVersionAt = v.CreatedAt
		}
	}
	episodeCodesOut := episodeCodes
	if episodeCodesOut == nil {
		episodeCodesOut = []string{}
	}
	assetType := asset.AssetType
	projectID := asset.ProjectId
	projectCode := libraryCode(projectPrefix, "RF")
	item := AssetLibraryItemView{
		LibraryKey:      "asset:" + asset.ID,
		SourceType:      source,
		SourceId:        asset.ID,
		ProjectId:       projectID,
		ProjectCode:     orStringPtr(projectCode),
		ProjectName:     orStringPtr(projectName),
		EpisodeCodes:    episodeCodesOut,
		SourceLabel:     orStringPtr(sourceLabel),
		Name:            asset.Name,
		Description:     asset.Description,
		AssetType:       &assetType,
		OutputType:      assetLibraryOutputType(asset.AssetType),
		CurrentMaster:   cardMaster,
		LatestVersionAt: latestVersionAt,
		VersionCount:    versionCount,
	}
	if includeVersions {
		item.Versions = versionValues
	}
	return item
}

func stableSortVersions(versions []*AssetLibraryVersionView) {
	// 保持等键相对顺序：插入排序 + 仅当 (version_no, created_at) 更大时前移（不倒换相等项）。
	for i := 1; i < len(versions); i++ {
		j := i
		for j > 0 {
			a, b := versions[j-1], versions[j]
			if b.VersionNo > a.VersionNo ||
				(b.VersionNo == a.VersionNo && b.CreatedAt.After(a.CreatedAt)) {
				versions[j-1], versions[j] = versions[j], versions[j-1]
				j--
			} else {
				break
			}
		}
	}
}

// submissionStrPtr 对齐 legacy submission 原始字符串字段直传：DB ” 视为空串，不回退 None。
func submissionStrPtr(s *string) *string {
	if s == nil {
		empty := ""
		return &empty
	}
	return s
}

func tRowFile(f *model.Files) *model.Files { return f }
func tRowMime(f *model.Files) *string {
	if f == nil {
		return nil
	}
	return f.MimeType
}
func tRowSize(f *model.Files) *int32 {
	if f == nil {
		return nil
	}
	return f.FileSize
}

func orNilString(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// orString 把 any 转 string；nil → fallback 参数返回所指地址（用于指针参数便利）。
func orStringPtr(v any) *string { return emptyToNil(strAny(v, "")) }

// orString 返回 v 的字符串值（nil → fallback）。
func orString(v any, fallback string) *string { return orStringPtr(strAny(v, fallback)) }

// assetVersionHierarchy 对齐 _asset_version_hierarchy。
func assetVersionHierarchy(projectPrefix string, metadata map[string]any, asset *model.Assets, sourceTaskID *string) AssetLibraryHierarchyView {
	assetMetadata := metadataAsMap(asset.MetadataJson)
	assetType := asset.AssetType
	taskVariant := strAny(metadata["task_variant"], "")
	if taskVariant == "" {
		taskVariant = strAny(metadata["view_label"], "")
	}
	return AssetLibraryHierarchyView{
		ProjectId:          asset.ProjectId,
		ProjectPrefix:      libraryCode(projectPrefix, "RF"),
		EpisodeId:          uuidOrNone(metadata["episode_id"]),
		EpisodeCode:        emptyToNil(strings.TrimSpace(strAny(metadata["episode_code"], ""))),
		AssetId:            &asset.ID,
		AssetCode:          asset.AssetCode,
		AssetType:          &assetType,
		OutputType:         assetLibraryOutputType(asset.AssetType),
		ArtifactCode:       assetArtifactCode(orStringPtr(projectPrefix), asset.AssetType, asset.AssetCode, orStringPtr(assetMetadata["audio_type"]), orStringPtr(assetMetadata["episode_code"])),
		TaskId:             sourceTaskID,
		TaskType:           orStringPtr(metadata["task_type"]),
		TaskVariant:        orNilString(emptyToNil(taskVariant)),
		AgeStageCode:       orStringPtr(metadata["age_stage_code"]),
		CostumeVariantCode: orStringPtr(metadata["costume_variant_code"]),
	}
}

// assetTaskHierarchy 对齐 _asset_task_hierarchy。
func assetTaskHierarchy(projectPrefix string, asset *model.Assets, task *model.Tasks) AssetLibraryHierarchyView {
	assetMetadata := metadataAsMap(asset.MetadataJson)
	assetType := asset.AssetType
	return AssetLibraryHierarchyView{
		ProjectId:          asset.ProjectId,
		ProjectPrefix:      libraryCode(projectPrefix, "RF"),
		EpisodeId:          task.EpisodeId,
		SceneCode:          task.SceneCode,
		SceneName:          task.SceneName,
		AssetId:            &asset.ID,
		AssetCode:          asset.AssetCode,
		AssetType:          &assetType,
		OutputType:         assetLibraryOutputType(asset.AssetType),
		ArtifactCode:       assetArtifactCode(orStringPtr(projectPrefix), asset.AssetType, asset.AssetCode, orStringPtr(assetMetadata["audio_type"]), orStringPtr(assetMetadata["episode_code"])),
		TaskId:             &task.ID,
		TaskType:           orStringPtr(task.TaskType),
		TaskVariant:        task.TaskVariant,
		AgeStageCode:       task.AgeStageCode,
		CostumeVariantCode: task.CostumeVariantCode,
	}
}

// storyboardLibraryItem 对齐 _storyboard_library_item。
func (r *Assets) storyboardLibraryItem(projectPrefix, outputType string, rows []*librarySubmissionRow,
	projectName string, scriptSegmentCodes map[string]string, includeVersions bool, anchor *model.Storyboards) AssetLibraryItemView {
	var versions []*AssetLibraryVersionView
	for versionNo, row := range rows {
		no := int32(versionNo + 1)
		t := row.task
		sb := row.sb
		if sb == nil {
			sb = anchor
		}
		episodeCode := fmt.Sprintf("EP%02d", sb.EpisodeNum)
		storyboardCode := strAny(sb.StoryboardCode, "")
		if storyboardCode == "" {
			storyboardCode = fmt.Sprintf("F%03d", sb.OrderNum)
		}
		sceneCode := strAny(t.SceneCode, "")
		if sceneCode == "" {
			sceneCode = strAny(sb.SceneCode, "")
		}
		var scriptSegmentID *string
		if t.ScriptSegmentId != nil {
			scriptSegmentID = t.ScriptSegmentId
		} else {
			scriptSegmentID = sb.ScriptSegmentId
		}
		var scriptSegmentCode *string
		if scriptSegmentID != nil {
			if code, ok := scriptSegmentCodes[*scriptSegmentID]; ok {
				scriptSegmentCode = &code
			}
		}
		mirrorShotCode := submissionMirrorShotCode(t.TaskVariant, row.sub.Step)
		sceneCodeStr := ""
		if sceneCode != "" {
			sceneCodeStr = sceneCode
		}
		var scenePtr *string
		if sceneCodeStr != "" {
			scenePtr = &sceneCodeStr
		}
		sceneName := strAny(t.SceneName, "")
		if sceneName == "" {
			sceneName = strAny(sb.SceneName, "")
		}
		var sceneNamePtr *string
		if sceneName != "" {
			sceneNamePtr = &sceneName
		}
		hierarchy := AssetLibraryHierarchyView{
			ProjectId:         &t.ProjectId,
			ProjectPrefix:     libraryCode(projectPrefix, "RF"),
			EpisodeId:         t.EpisodeId,
			EpisodeCode:       orStringPtr(episodeCode),
			SceneCode:         scenePtr,
			SceneName:         sceneNamePtr,
			StoryboardId:      &sb.ID,
			StoryboardCode:    orStringPtr(storyboardCode),
			ScriptSegmentId:   scriptSegmentID,
			ScriptSegmentCode: scriptSegmentCode,
			MirrorShotCode:    mirrorShotCode,
			OutputType:        outputType,
			ArtifactCode:      storyboardOutputArtifactCode(orStringPtr(projectPrefix), orStringPtr(episodeCode), scenePtr, orStringPtr(storyboardCode), outputType, scriptSegmentCode, mirrorShotCode),
			TaskId:            &t.ID,
			TaskType:          orStringPtr(t.TaskType),
			DependsOnTaskId:   t.DependsOnTaskId,
		}
		extension := row.sub.FilePath
		if row.file != nil && row.file.FileName != "" {
			extension = row.file.FileName
		}
		canonicalName := canonicalStoryboardOutputDisplayName(
			orStringPtr(projectPrefix), orStringPtr(episodeCode), scenePtr, orStringPtr(storyboardCode),
			outputType, no, extension, scriptSegmentCode, mirrorShotCode)
		fullName := ""
		if row.file != nil {
			fullName = row.file.FileName
		}
		var originalName *string
		if fullName != "" {
			originalName = &fullName
		}
		modelName := strAny(row.sub.ModelName, "")
		if modelName == "" {
			modelName = strAny(t.ProductionModel, "")
		}
		toolNames := strings.Join(toolNamesToList(row.sub.ToolNames), ", ")
		var batchVersionNoPtr *int32
		if row.batch != nil {
			batchVersionNoPtr = &row.batch.VersionNo
		}
		versions = append(versions, &AssetLibraryVersionView{
			ID:                   row.sub.ID,
			SourceType:           "submission",
			VersionNo:            no,
			CanonicalDisplayName: canonicalName,
			OriginalFileName:     originalName,
			FileId:               row.sub.FileId,
			FilePath:             fileLocation(tRowFile(row.file), orStringPtr(row.sub.FilePath)),
			MimeType:             tRowMime(row.file),
			FileSize:             tRowSize(row.file),
			ModelName:            orNilString(emptyToNil(modelName)),
			ToolName:             orNilString(emptyToNil(toolNames)),
			BatchId:              row.sub.BatchId,
			BatchVersionNo:       batchVersionNoPtr,
			Status:               row.sub.Status,
			IsPrimary:            row.sub.IsPrimary,
			IsCurrent:            row.sub.IsPrimary,
			IsContextCurrent:     row.sub.IsPrimary,
			IsCardMaster:         row.sub.IsPrimary,
			Hierarchy:            hierarchy,
			CreatedAt:            row.sub.CreatedAt,
		})
	}
	var currentMaster *AssetLibraryVersionView
	for _, v := range versions {
		if !v.IsPrimary {
			continue
		}
		if currentMaster == nil ||
			v.CreatedAt.After(currentMaster.CreatedAt) ||
			(v.CreatedAt.Equal(currentMaster.CreatedAt) && v.VersionNo > currentMaster.VersionNo) {
			currentMaster = v
		}
	}
	for _, v := range versions {
		v.IsCardMaster = currentMaster != nil && v.ID == currentMaster.ID
	}
	stableSortVersions(versions)
	versionValues := make([]AssetLibraryVersionView, len(versions))
	for i, v := range versions {
		versionValues[i] = *v
	}
	latest := time.Time{}
	for _, v := range versions {
		if v.CreatedAt.After(latest) {
			latest = v.CreatedAt
		}
	}
	last := rows[len(rows)-1]
	sb := last.sb
	if sb == nil {
		sb = anchor
	}
	mirrorShotCode := submissionMirrorShotCode(last.task.TaskVariant, last.sub.Step)
	libraryKey := fmt.Sprintf("storyboard:%s:%s:%s", sb.ID, strAny(mirrorShotCode, "parent"), outputType)
	episodeCode := fmt.Sprintf("EP%02d", sb.EpisodeNum)
	sourceLabel := "关键帧定版"
	if outputType == "video" {
		sourceLabel = "视频定版"
	}
	name := strAny(sb.Title, "")
	if name == "" {
		name = strAny(last.task.Title, "")
	}
	assetType := assetTypeStoryboardImage
	if outputType == "video" {
		assetType = assetTypeStoryboardVideo
	}
	projectID := last.task.ProjectId
	projectCode := libraryCode(projectPrefix, "RF")
	description := strAny(sb.Description, "")
	var descriptionPtr *string
	if description != "" {
		descriptionPtr = &description
	}
	item := AssetLibraryItemView{
		LibraryKey:      libraryKey,
		SourceType:      "storyboard_output",
		SourceId:        sb.ID,
		ProjectId:       &projectID,
		ProjectCode:     orStringPtr(projectCode),
		ProjectName:     orStringPtr(projectName),
		EpisodeCodes:    []string{episodeCode},
		SourceLabel:     orStringPtr(sourceLabel),
		Name:            name,
		Description:     descriptionPtr,
		AssetType:       &assetType,
		OutputType:      outputType,
		CurrentMaster:   currentMaster,
		LatestVersionAt: latest,
		VersionCount:    len(versions),
	}
	if includeVersions {
		item.Versions = versionValues
	}
	return item
}

// assetAlternateNumber 对齐 _asset_alternate_number。
func assetAlternateNumber(isPrimary bool, contextKey *string, contextVersionNo int32, counters map[string]int32) int32 {
	if isPrimary || contextKey == nil {
		return 0
	}
	key := fmt.Sprintf("%s\x1f%d", *contextKey, contextVersionNo)
	counters[key]++
	return counters[key]
}

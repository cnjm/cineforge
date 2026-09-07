package repository

// P3f-c 资产域仓储（对齐 legacy repositories.py 的 assets 仓库函数）：
//   - ListAssets     → list_assets              （状态过滤 + 权限 + include_metadata 摘要）
//   - GetAsset       → get_asset
//   - CreateAsset    → create_asset             （视觉资产正式码分配，FOR UPDATE 项目行）
//   - CreateTemporaryAssetProduction → create_temporary_asset_production（幂等 + 共押锁 + op log）
//   - ListSceneAssetOptions → list_scene_asset_options（需 human 终版修订）
//   - ListAssetVariantPlans / UpdateAssetVariantPlan / CreateAssetVariantPlan（恒 400）
//   - ConfirmedAssetInventory  → 人工临时生产守卫的确认资产清单查询
// 权限对齐 Tasks.CanAccessProject / CanWriteProject（director/admin、project_role、可见任务）。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/model"
)

// Assets 基于共享连接池的资产域仓储。
type Assets struct {
	pool     *pgxpool.Pool
	projects *Projects
}

// NewAssets 创建 Assets 仓储（需注入 Projects 以复用项目权限/读取辅助）。
func NewAssets(pool *pgxpool.Pool, projects *Projects) *Assets {
	return &Assets{pool: pool, projects: projects}
}

// ---- 视图模型（对齐 AssetRead / AssetVariantPlanRead / SceneAssetOption） ----

// AssetView 对齐 AssetRead。
type AssetView struct {
	ID          string         `json:"id"`
	ProjectId   *string        `json:"project_id"`
	AssetCode   *string        `json:"asset_code"`
	Status      string         `json:"status"`
	AssetType   string         `json:"asset_type"`
	Name        string         `json:"name"`
	Description *string        `json:"description"`
	Tags        []string       `json:"tags"`
	PreviewPath *string        `json:"preview_path"`
	FilePath    *string        `json:"file_path"`
	PromptText  *string        `json:"prompt_text"`
	BaseModel   *string        `json:"base_model"`
	Metadata    map[string]any `json:"metadata"`
	Version     int32          `json:"version"`
	CreatedById *string        `json:"created_by_id"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// AssetVariantPlanView 对齐 AssetVariantPlanRead。
type AssetVariantPlanView struct {
	ID              string    `json:"id"`
	ProjectId       string    `json:"project_id"`
	EpisodeId       *string   `json:"episode_id"`
	ScriptVersionId *string   `json:"script_version_id"`
	AssetId         string    `json:"asset_id"`
	AssetCode       *string   `json:"asset_code"`
	AssetName       *string   `json:"asset_name"`
	VariantCode     string    `json:"variant_code"`
	TaskCode        *string   `json:"task_code"`
	VariantKind     string    `json:"variant_kind"`
	TitleZh         string    `json:"title_zh"`
	DescriptionZh   string    `json:"description_zh"`
	Source          string    `json:"source"`
	Confidence      *float64  `json:"confidence"`
	Status          string    `json:"status"`
	TaskId          *string   `json:"task_id"`
	StoryboardIDs   []string  `json:"storyboard_ids"`
	StoryboardCodes []string  `json:"storyboard_codes"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// SceneAssetOptionView 对齐 SceneAssetOption。
type SceneAssetOptionView struct {
	SceneAssetID string  `json:"scene_asset_id"`
	SceneCode    string  `json:"scene_code"`
	SceneName    string  `json:"scene_name"`
	EpisodeCode  *string `json:"episode_code"`
	RevisionID   *string `json:"revision_id"`
	Status       string  `json:"status"`
}

// EpisodeAssetBindingView 对齐 EpisodeAssetBindingRead（项目分集详情 Assets）。
type EpisodeAssetBindingView struct {
	ID                 string         `json:"id"`
	ProposalIndex      int32          `json:"proposal_index"`
	AssetId            string         `json:"asset_id"`
	AssetRevisionId    *string        `json:"asset_revision_id,omitempty"`
	AssetCode          *string        `json:"asset_code,omitempty"`
	AssetType          string         `json:"asset_type"`
	AssetName          string         `json:"asset_name"`
	Action             string         `json:"action"`
	Status             string         `json:"status"`
	AgeStageCode       *string        `json:"age_stage_code,omitempty"`
	CostumeVariantCode *string        `json:"costume_variant_code,omitempty"`
	Proposal           map[string]any `json:"proposal"`
}

// ListAssetsParams list_assets 过滤入参。
type ListAssetsParams struct {
	AssetType       string
	Search          string
	ProjectID       *string
	IncludeMetadata bool
}

// assetToReadView 对齐 asset_to_read（include_metadata=false 时返回列表摘要）。
func assetToReadView(a *model.Assets, includeMetadata bool) AssetView {
	metadata := metadataAsMap(a.MetadataJson)
	if !includeMetadata {
		metadata = assetListMetadata(metadata)
	}
	return AssetView{
		ID:          a.ID,
		ProjectId:   a.ProjectId,
		AssetCode:   a.AssetCode,
		Status:      a.Status,
		AssetType:   a.AssetType,
		Name:        a.Name,
		Description: a.Description,
		Tags:        tagsToStrings(a.Tags),
		PreviewPath: a.PreviewPath,
		FilePath:    a.FilePath,
		PromptText:  a.PromptText,
		BaseModel:   a.BaseModel,
		Metadata:    metadata,
		Version:     a.Version,
		CreatedById: a.CreatedById,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

func tagsToStrings(raw json.RawMessage) []string {
	var tags []string
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &tags)
	}
	if tags == nil {
		return []string{}
	}
	return tags
}

// canAccessProject 对齐 legacy _can_access_project。
func (r *Assets) canAccessProject(ctx context.Context, actor TaskActor, projectID string) (bool, error) {
	if isDirectorOrAdminRole(actor.Role) {
		return true, nil
	}
	has, err := r.projects.HasProjectPermission(ctx, actor.ID, projectID)
	if err != nil {
		return false, err
	}
	if has {
		return true, nil
	}
	return r.projects.HasVisibleTaskInProject(ctx, actor.ID, projectID)
}

// ensureProjectWriteAccess 对齐 ensure_project_write_access（项目不存在/已删除 → KeyError，写权限不足 → PermissionError）。
func (r *Assets) ensureProjectWriteAccess(ctx context.Context, actor TaskActor, projectID string) error {
	project, err := r.projects.FindByID(ctx, projectID)
	if err != nil {
		return err
	}
	if project == nil || project.DeletedAt != nil {
		return &KeyError{Arg: projectID}
	}
	if isDirectorOrAdminRole(actor.Role) {
		return nil
	}
	roleName, err := r.projects.ProjectMemberRole(ctx, actor.ID, projectID)
	if err != nil {
		return err
	}
	if !projectWriteRoles[roleName] {
		return &PermissionError{Arg: projectID}
	}
	return nil
}

// ---- GET /assets ----

// ListAssets 对齐 list_assets。
func (r *Assets) ListAssets(ctx context.Context, actor TaskActor, params ListAssetsParams) ([]AssetView, error) {
	conds := []string{`status NOT IN ('deleted','excluded')`}
	args := []any{}
	addArg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if params.AssetType != "" {
		conds = append(conds, "asset_type = "+addArg(params.AssetType))
	}
	if strings.TrimSpace(params.Search) != "" {
		pattern := "%" + strings.TrimSpace(params.Search) + "%"
		conds = append(conds, fmt.Sprintf("(name ILIKE %s OR description ILIKE %s OR asset_code ILIKE %s)",
			addArg(pattern), addArg(pattern), addArg(pattern)))
	}
	if params.ProjectID != nil {
		if !isDirectorOrAdminRole(actor.Role) {
			ok, err := r.canAccessProject(ctx, actor, *params.ProjectID)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, &PermissionError{Arg: *params.ProjectID}
			}
		}
		conds = append(conds, "project_id = "+addArg(*params.ProjectID))
	} else if !isDirectorOrAdminRole(actor.Role) {
		projectIDs, err := r.projects.VisibleTaskProjectIDs(ctx, actor.ID)
		if err != nil {
			return nil, err
		}
		if len(projectIDs) == 0 {
			conds = append(conds, "project_id IS NULL")
		} else {
			placeholders := make([]string, len(projectIDs))
			for i, id := range projectIDs {
				placeholders[i] = addArg(id)
			}
			conds = append(conds, fmt.Sprintf("(project_id IN (%s) OR project_id IS NULL)", strings.Join(placeholders, ",")))
		}
	}
	query := "SELECT " + assetColumns + " FROM assets WHERE " + strings.Join(conds, " AND ") + " ORDER BY created_at DESC"
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()
	var out []AssetView
	for rows.Next() {
		a, scanErr := scanAsset(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list assets: %w", scanErr)
		}
		out = append(out, assetToReadView(a, params.IncludeMetadata))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	return out, nil
}

// ---- GET /assets/{id} ----

// GetAsset 对齐 get_asset。
func (r *Assets) GetAsset(ctx context.Context, actor TaskActor, assetID string) (*AssetView, error) {
	a, err := scanAsset(r.pool.QueryRow(ctx, `SELECT `+assetColumns+` FROM assets WHERE id = $1`, assetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &KeyError{Arg: assetID}
	}
	if err != nil {
		return nil, fmt.Errorf("get asset: %w", err)
	}
	if a.ProjectId != nil && !isDirectorOrAdminRole(actor.Role) {
		ok, canErr := r.canAccessProject(ctx, actor, *a.ProjectId)
		if canErr != nil {
			return nil, canErr
		}
		if !ok {
			return nil, &PermissionError{Arg: assetID}
		}
	}
	view := assetToReadView(a, true)
	return &view, nil
}

// ---- POST /assets ----

// CreateAssetInput 对齐 AssetCreate。
type CreateAssetInput struct {
	AssetType   string
	Name        string
	Description *string
	Tags        []string
	PreviewPath *string
	FilePath    *string
	PromptText  *string
	BaseModel   *string
	Metadata    map[string]any
}

// CreateAsset 对齐 create_asset。
func (r *Assets) CreateAsset(ctx context.Context, actor TaskActor, input CreateAssetInput) (*AssetView, error) {
	if !isDirectorOrAdminRole(actor.Role) {
		return nil, &PermissionError{Arg: "asset"}
	}
	if !visualAssetTypes[input.AssetType] {
		return nil, &ValueError{Detail: "POST /assets only supports character, scene, and prop assets"}
	}
	metadata := make(map[string]any, len(input.Metadata))
	for k, v := range input.Metadata {
		metadata[k] = v
	}
	rawProjectID := strings.TrimSpace(strAny(metadata["project_id"], ""))
	if rawProjectID == "" {
		return nil, &ValueError{Detail: "project_id is required to allocate a formal asset code"}
	}
	if !uuidPatternRe.MatchString(rawProjectID) {
		return nil, &ValueError{Detail: "project_id must be a valid UUID"}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("create asset: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	project, err := scanProject(tx.QueryRow(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = $1 FOR UPDATE`, rawProjectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &KeyError{Arg: rawProjectID}
	}
	if err != nil {
		return nil, fmt.Errorf("create asset: %w", err)
	}
	if project == nil || project.DeletedAt != nil {
		return nil, &KeyError{Arg: rawProjectID}
	}
	projectCode := projectBusinessCode(project)
	if projectCode == "" {
		return nil, &ValueError{Detail: "Project prefix is required to allocate a formal asset code"}
	}
	reservedCodes, err := r.reservedProjectAssetCodes(ctx, tx, project.ID, safeCodePrefix(projectCode))
	if err != nil {
		return nil, err
	}
	allocatedCode, err := nextFormalAssetCode(projectCode, input.AssetType, reservedCodes)
	if err != nil {
		return nil, &ValueError{Detail: err.Error()}
	}
	delete(metadata, "asset_code")
	metadata["project_id"] = rawProjectID
	metadata["asset_code_source"] = "backend"
	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, _ := json.Marshal(tags)
	metadataJSON, _ := json.Marshal(metadata)
	now := time.Now().UTC()
	asset := &model.Assets{
		ID:           newUUIDString(),
		ProjectId:    &rawProjectID,
		AssetCode:    &allocatedCode,
		AssetType:    input.AssetType,
		Name:         input.Name,
		Description:  input.Description,
		Status:       "draft",
		Tags:         tagsJSON,
		PreviewPath:  input.PreviewPath,
		FilePath:     input.FilePath,
		PromptText:   input.PromptText,
		BaseModel:    input.BaseModel,
		MetadataJson: metadataJSON,
		Version:      1,
		CreatedById:  &actor.ID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO assets (id, project_id, asset_code, asset_type, name, description, status, tags,
			preview_path, file_path, prompt_text, base_model, metadata_json, version, created_by_id,
			is_locked, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,TRUE,$16,$17)`,
		asset.ID, asset.ProjectId, asset.AssetCode, asset.AssetType, asset.Name, asset.Description,
		asset.Status, asset.Tags, asset.PreviewPath, asset.FilePath, asset.PromptText, asset.BaseModel,
		asset.MetadataJson, asset.Version, asset.CreatedById, asset.CreatedAt, asset.UpdatedAt); err != nil {
		return nil, fmt.Errorf("create asset: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create asset: %w", err)
	}
	view := assetToReadView(asset, true)
	return &view, nil
}

// ---- POST /assets/temporary-production ----

// TemporaryProductionInput 对齐 TemporaryAssetCreate。
type TemporaryProductionInput struct {
	RequestID             string
	ProjectID             string
	EpisodeID             string
	ScriptVersionID       string
	AssetType             string
	Name                  string
	Description           *string
	Reason                string
	ProductionRequirement string
	TaskVariant           string
}

// TemporaryProductionResult 对齐 TemporaryAssetProductionRead。
type TemporaryProductionResult struct {
	Asset AssetView `json:"asset"`
	Task  TaskView  `json:"task"`
}

// CreateTemporaryAssetProduction 对齐 create_temporary_asset_production。
func (r *Assets) CreateTemporaryAssetProduction(ctx context.Context, actor TaskActor, input TemporaryProductionInput) (*TemporaryProductionResult, error) {
	if !isDirectorOrAdminRole(actor.Role) {
		return nil, &PermissionError{Arg: "asset"}
	}
	if !temporaryAssetTypeAllowed[input.AssetType] {
		return nil, &ValueError{Detail: "人工临时添加仅支持人物、场景、道具和音频资产。"}
	}
	keyHash := sha256.Sum256([]byte("human_temporary_production:" + input.ProjectID + ":" + input.RequestID))
	taskIDempotencyKey := hex.EncodeToString(keyHash[:])

	findExisting := func(q queryer) (*TemporaryProductionResult, error) {
		existing, err := scanTask(q.QueryRow(ctx,
			`SELECT `+taskColumns+` FROM tasks WHERE idempotency_key = $1 AND is_retired = FALSE`, taskIDempotencyKey))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return nil, fmt.Errorf("find temporary task: %w", err)
		}
		var existingAsset *model.Assets
		if existing.AssetId != nil {
			existingAsset, err = scanAsset(r.pool.QueryRow(ctx,
				`SELECT `+assetColumns+` FROM assets WHERE id = $1`, *existing.AssetId))
			if err != nil {
				return nil, fmt.Errorf("find temporary asset: %w", err)
			}
		}
		if existingAsset == nil || existing.ProjectId != input.ProjectID {
			return nil, &ValueError{Detail: "人工临时生产请求幂等记录异常。"}
		}
		episodeCode := strAny(metadataAsMap(existingAsset.MetadataJson)["episode_code"], "")
		var ep *string
		if episodeCode != "" {
			ep = &episodeCode
		}
		view := assetToReadView(existingAsset, true)
		task := taskToRead(existing, nil, ep, nil, false, nil, nil, DependencySummaryView{}, nil, nil, nil)
		return &TemporaryProductionResult{Asset: view, Task: task}, nil
	}

	existing, err := findExisting(r.pool)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("create temporary asset: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	project, err := scanProject(tx.QueryRow(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = $1 FOR UPDATE`, input.ProjectID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &KeyError{Arg: input.ProjectID}
	}
	if err != nil {
		return nil, fmt.Errorf("create temporary asset: %w", err)
	}
	if project == nil || project.DeletedAt != nil {
		return nil, &KeyError{Arg: input.ProjectID}
	}
	// 项目行锁后重查幂等，避免并发重试各自创建。
	existing, err = findExisting(tx)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("create temporary asset: %w", err)
		}
		return existing, nil
	}

	episode, err := scanEpisode(tx.QueryRow(ctx,
		`SELECT `+episodeColumns+` FROM project_episodes WHERE id = $1`, input.EpisodeID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("create temporary asset: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		episode = nil
	}
	script, err := scanScript(tx.QueryRow(ctx,
		`SELECT `+scriptColumns+` FROM scripts WHERE id = (SELECT script_id FROM script_versions WHERE id = $1)`,
		input.ScriptVersionID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("create temporary asset: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		script = nil
	}
	if !scriptVersionMatches(episode, script, input) {
		return nil, &ValueError{Detail: "只能在当前分集的最新剧本版本中追加人工生产资产。"}
	}

	confirmed, err := r.confirmedAssetInventory(ctx, tx, input.ProjectID, input.EpisodeID, input.ScriptVersionID)
	if err != nil {
		return nil, err
	}
	if !confirmed {
		return nil, &ValueError{Detail: "请先确认资产拆解，确认后才能新增人工临时生产资产。"}
	}

	projectCode := projectBusinessCode(project)
	if projectCode == "" {
		return nil, &ValueError{Detail: "项目缺少业务前缀，不能分配正式资产编码。"}
	}
	reservedCodes, err := r.reservedProjectAssetCodes(ctx, tx, project.ID, safeCodePrefix(projectCode))
	if err != nil {
		return nil, err
	}
	var allocatedCode string
	if visualAssetTypes[input.AssetType] {
		allocatedCode, err = nextFormalAssetCode(projectCode, input.AssetType, reservedCodes)
		if err != nil {
			return nil, &ValueError{Detail: err.Error()}
		}
	} else {
		codeType := "MUSIC"
		if input.AssetType != assetTypeMusic {
			codeType = "VOICE"
		}
		prefix := safeCodePrefix(projectCode)
		sequence := 1
		occupied := map[string]bool{}
		for _, code := range reservedCodes {
			occupied[code] = true
		}
		for occupied[curTemporaryCode(prefix, codeType, sequence)] {
			sequence++
		}
		allocatedCode = curTemporaryCode(prefix, codeType, sequence)
	}
	var episodeCodePtr *string
	if episode != nil {
		episodeCodePtr = &episode.EpisodeCode
	}
	metadata := map[string]any{
		"project_id":             input.ProjectID,
		"episode_id":             input.EpisodeID,
		"episode_code":           episodeCodeString(episodeCodePtr),
		"source":                 "human_temporary",
		"source_label":           "人工临时添加",
		"temporary_reason":       input.Reason,
		"production_requirement": input.ProductionRequirement,
		"task_variant":           input.TaskVariant,
		"prompt_skipped":         true,
		"workflow_isolated":      true,
		"request_id":             input.RequestID,
	}
	metadataJSON, _ := json.Marshal(metadata)
	tagsJSON, _ := json.Marshal([]string{"人工临时添加", "待生产"})
	now := time.Now().UTC()
	asset := &model.Assets{
		ID:           newUUIDString(),
		ProjectId:    &input.ProjectID,
		AssetCode:    &allocatedCode,
		AssetType:    input.AssetType,
		Name:         input.Name,
		Description:  input.Description,
		Status:       "in_progress",
		Tags:         tagsJSON,
		MetadataJson: metadataJSON,
		Version:      1,
		CreatedById:  &actor.ID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO assets (id, project_id, asset_code, asset_type, name, description, status, tags,
			metadata_json, version, created_by_id, is_locked, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,TRUE,$12,$13)`,
		asset.ID, asset.ProjectId, asset.AssetCode, asset.AssetType, asset.Name, asset.Description,
		asset.Status, asset.Tags, asset.MetadataJson, asset.Version, asset.CreatedById,
		asset.CreatedAt, asset.UpdatedAt); err != nil {
		return nil, fmt.Errorf("create temporary asset: %w", err)
	}

	taskType := "asset"
	if input.AssetType == assetTypeMusic || input.AssetType == assetTypeVoiceProfile {
		taskType = "audio"
	}
	mediaType := "image"
	switch input.AssetType {
	case assetTypeVoiceProfile:
		mediaType = "voice"
	case assetTypeMusic:
		mediaType = "music"
	}
	title := fmt.Sprintf("人工临时资产生产 · %s %s", allocatedCode, input.Name)
	if runes := []rune(title); len(runes) > 160 {
		title = string(runes[:160])
	}
	task := &model.Tasks{
		ID:                   newUUIDString(),
		ProjectId:            input.ProjectID,
		EpisodeId:            &input.EpisodeID,
		ScriptId:             scriptIDPtr(script),
		ScriptVersionId:      &input.ScriptVersionID,
		IDempotencyKey:       &taskIDempotencyKey,
		AssetId:              &asset.ID,
		TaskType:             taskType,
		Title:                title,
		Status:               "todo",
		MediaType:            &mediaType,
		TaskVariant:          strPtr(input.TaskVariant),
		VariantKind:          strPtr("human_temporary"),
		VariantTitleZh:       strPtr("人工临时添加"),
		VariantDescriptionZh: &input.ProductionRequirement,
		AssetContextOutdated: false,
		IsRetired:            false,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO tasks (id, project_id, episode_id, script_id, script_version_id, idempotency_key,
			asset_id, task_type, title, status, media_type, task_variant, variant_kind, variant_title_zh,
			variant_description_zh, asset_context_outdated, is_retired, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,FALSE,FALSE,$16,$17)`,
		task.ID, task.ProjectId, task.EpisodeId, task.ScriptId, task.ScriptVersionId, task.IDempotencyKey,
		task.AssetId, task.TaskType, task.Title, task.Status, task.MediaType, task.TaskVariant,
		task.VariantKind, task.VariantTitleZh, task.VariantDescriptionZh, task.CreatedAt, task.UpdatedAt); err != nil {
		return nil, fmt.Errorf("create temporary task: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("create temporary asset: %w", err)
	}
	result := &TemporaryProductionResult{
		Asset: assetToReadView(asset, true),
		Task:  taskToRead(task, nil, episodeCodePtr, nil, false, nil, nil, DependencySummaryView{}, nil, nil, nil),
	}
	if err := r.insertOperationLog(ctx, input.ProjectID, actor.ID, "asset", asset.ID,
		"human_temporary_asset_created", map[string]any{
			"asset_code":        allocatedCode,
			"asset_type":        input.AssetType,
			"episode_id":        input.EpisodeID,
			"script_version_id": input.ScriptVersionID,
			"task_id":           task.ID,
			"reason":            input.Reason,
			"prompt_skipped":    true,
		}); err != nil {
		return nil, err
	}
	return result, nil
}

// ---- 守卫与分配帮助 ----

var temporaryAssetTypeAllowed = map[string]bool{
	assetTypeCharacter:    true,
	assetTypeScene:        true,
	assetTypeProp:         true,
	assetTypeMusic:        true,
	assetTypeVoiceProfile: true,
}

func curTemporaryCode(prefix, codeType string, sequence int) string {
	return fmt.Sprintf("%s-%s-TEMP%03d", prefix, codeType, sequence)
}

func episodeCodeString(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func scriptIDPtr(script *model.Scripts) *string {
	if script == nil {
		return nil
	}
	return &script.ID
}

// projectBusinessCode 对齐 legacy project_prefix or project_no 业务前缀。
func projectBusinessCode(project *model.Projects) string {
	if project.ProjectPrefix != nil && *project.ProjectPrefix != "" {
		return *project.ProjectPrefix
	}
	if project.ProjectNo != nil {
		return *project.ProjectNo
	}
	return ""
}

// scriptVersionMatches 校验 episode/script/当前版本（对齐 create_temporary_asset_production 前置守卫）。
func scriptVersionMatches(episode *model.ProjectEpisodes, script *model.Scripts, input TemporaryProductionInput) bool {
	if episode == nil || episode.ProjectId != input.ProjectID {
		return false
	}
	if script == nil || script.ProjectId != input.ProjectID {
		return false
	}
	if script.EpisodeId == nil || *script.EpisodeId != input.EpisodeID {
		return false
	}
	if script.CurrentVersionId == nil || *script.CurrentVersionId != input.ScriptVersionID {
		return false
	}
	return true
}

// confirmedAssetInventory 对齐 temporary-production 守卫的有效条件：
// 存在 (project, episode, script_version) 作用域的 human 终版已确认 asset_inventory 修订。
// 完整 breakdown-view 的 asset_review_state 同步随拆解阶段（P4）接入；此处按持久化修订判定。
func (r *Assets) confirmedAssetInventory(ctx context.Context, q queryer, projectID, episodeID, scriptVersionID string) (bool, error) {
	var id string
	err := q.QueryRow(ctx,
		`SELECT id FROM artifact_revisions
		 WHERE project_id = $1 AND artifact_type = 'asset_inventory' AND source_type = 'human'
		   AND status = 'confirmed' AND data_state = 'final'
		   AND input_snapshot->>'episode_id' = $2
		   AND input_snapshot->>'script_version_id' = $3
		 ORDER BY version_no DESC, created_at DESC LIMIT 1`,
		projectID, episodeID, scriptVersionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("confirmed asset inventory: %w", err)
	}
	return id != "", nil
}

// reservedProjectAssetCodes 对齐 _reserved_project_asset_codes（正式资产码占用集合）。
func (r *Assets) reservedProjectAssetCodes(ctx context.Context, q queryer, projectID, prefix string) ([]string, error) {
	values := map[string]bool{}
	add := func(code string) {
		trimmed := strings.TrimSpace(strings.ToUpper(code))
		if trimmed != "" {
			values[trimmed] = true
		}
	}
	rows, err := q.Query(ctx,
		`SELECT asset_code FROM assets WHERE project_id = $1 AND asset_code IS NOT NULL`, projectID)
	if err != nil {
		return nil, fmt.Errorf("reserved asset codes: %w", err)
	}
	for rows.Next() {
		var code *string
		if scanErr := rows.Scan(&code); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("reserved asset codes: %w", scanErr)
		}
		if code != nil {
			add(*code)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reserved asset codes: %w", err)
	}

	breakdownRows, err := q.Query(ctx,
		`SELECT content_json FROM script_breakdowns WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("reserved asset codes: %w", err)
	}
	for breakdownRows.Next() {
		var raw []byte
		if scanErr := breakdownRows.Scan(&raw); scanErr != nil {
			breakdownRows.Close()
			return nil, fmt.Errorf("reserved asset codes: %w", scanErr)
		}
		scanFormalAssetCodes(unmarshalAny(raw), prefix, add)
	}
	breakdownRows.Close()
	if err := breakdownRows.Err(); err != nil {
		return nil, fmt.Errorf("reserved asset codes: %w", err)
	}

	inventoryRows, err := q.Query(ctx,
		`SELECT normalized_content FROM artifact_revisions
		 WHERE project_id = $1 AND artifact_type = 'asset_inventory'`, projectID)
	if err != nil {
		return nil, fmt.Errorf("reserved asset codes: %w", err)
	}
	for inventoryRows.Next() {
		var raw []byte
		if scanErr := inventoryRows.Scan(&raw); scanErr != nil {
			inventoryRows.Close()
			return nil, fmt.Errorf("reserved asset codes: %w", scanErr)
		}
		scanFormalAssetCodes(unmarshalAny(raw), prefix, add)
	}
	inventoryRows.Close()
	if err := inventoryRows.Err(); err != nil {
		return nil, fmt.Errorf("reserved asset codes: %w", err)
	}

	codes := make([]string, 0, len(values))
	for code := range values {
		codes = append(codes, code)
	}
	return codes, nil
}

func unmarshalAny(raw []byte) any {
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}

// scanFormalAssetCodes 对齐 legacy _formal_asset_codes_in_payload（递归扫描 asset_code/character_code/scene_code/prop_code）。
func scanFormalAssetCodes(value any, prefix string, add func(string)) {
	switch t := value.(type) {
	case map[string]any:
		for key, nested := range t {
			switch key {
			case "asset_code", "character_code", "scene_code", "prop_code":
				if code := canonicalFormalAssetCode(strAny(nested, ""), prefix, ""); code != nil {
					add(*code)
				}
			default:
				scanFormalAssetCodes(nested, prefix, add)
			}
		}
	case []any:
		for _, item := range t {
			scanFormalAssetCodes(item, prefix, add)
		}
	}
}

// insertOperationLog 追加 audit 行（对齐 repo.add_operation_log 最小字段）。
func (r *Assets) insertOperationLog(ctx context.Context, projectID, operatorID, targetType, targetID, action string, detail any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO operation_logs (id, operator_id, project_id, target_type, target_id, action, detail)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		newUUIDString(), operatorID, projectID, targetType, targetID, action, raw)
	if err != nil {
		return fmt.Errorf("insert operation log: %w", err)
	}
	return nil
}

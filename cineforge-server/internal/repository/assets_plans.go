package repository

// P3f-c 变体计划 / 场景选项 / 资产绑定的仓储层（对齐 legacy repositories.py 对应函数）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

const assetVariantPlanColumns = `id, project_id, episode_id, script_version_id, asset_id, variant_code,
	variant_kind, title_zh, description_zh, source, confidence, status, task_id, metadata_json,
	created_by, created_at, updated_at`

func scanAssetVariantPlan(row pgx.Row) (*model.AssetVariantPlans, error) {
	var p model.AssetVariantPlans
	err := row.Scan(&p.ID, &p.ProjectId, &p.EpisodeId, &p.ScriptVersionId, &p.AssetId, &p.VariantCode,
		&p.VariantKind, &p.TitleZh, &p.DescriptionZh, &p.Source, &p.Confidence, &p.Status, &p.TaskId,
		&p.MetadataJson, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

const assetVariantLinkColumns = `id, variant_plan_id, storyboard_id, source, confidence, status, created_by, created_at, updated_at`

func scanAssetVariantLink(row pgx.Row) (*model.AssetVariantStoryboardLinks, error) {
	var l model.AssetVariantStoryboardLinks
	err := row.Scan(&l.ID, &l.VariantPlanId, &l.StoryboardId, &l.Source, &l.Confidence, &l.Status,
		&l.CreatedBy, &l.CreatedAt, &l.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// ---- GET /assets/variant-plans ----

// ListAssetVariantPlans 对齐 list_asset_variant_plans。
func (r *Assets) ListAssetVariantPlans(ctx context.Context, actor TaskActor, projectID string, episodeID *string) ([]AssetVariantPlanView, error) {
	if !isDirectorOrAdminRole(actor.Role) {
		ok, err := r.canAccessProject(ctx, actor, projectID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &PermissionError{Arg: projectID}
		}
	}
	query := `SELECT p.` + strings.ReplaceAll(assetVariantPlanColumns, ",", ", p.") +
		`, a.asset_code, a.name FROM asset_variant_plans p JOIN assets a ON a.id = p.asset_id
		 WHERE p.project_id = $1 AND a.status NOT IN ('deleted','excluded')`
	args := []any{projectID}
	if episodeID != nil {
		args = append(args, *episodeID)
		query += fmt.Sprintf(" AND p.episode_id = $%d", len(args))
	}
	query += " ORDER BY a.asset_code, p.variant_code"
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list variant plans: %w", err)
	}
	defer rows.Close()
	type planRow struct {
		plan      *model.AssetVariantPlans
		assetCode *string
		assetName string
	}
	var planRows []planRow
	for rows.Next() {
		var p model.AssetVariantPlans
		var code *string
		var name string
		if err := rows.Scan(&p.ID, &p.ProjectId, &p.EpisodeId, &p.ScriptVersionId, &p.AssetId, &p.VariantCode,
			&p.VariantKind, &p.TitleZh, &p.DescriptionZh, &p.Source, &p.Confidence, &p.Status, &p.TaskId,
			&p.MetadataJson, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt, &code, &name); err != nil {
			return nil, fmt.Errorf("list variant plans: %w", err)
		}
		planRows = append(planRows, planRow{plan: &p, assetCode: code, assetName: name})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list variant plans: %w", err)
	}
	planIDs := make([]string, len(planRows))
	for i := range planRows {
		planIDs[i] = planRows[i].plan.ID
	}
	linksByPlan, err := r.linksByPlanIDs(ctx, planIDs)
	if err != nil {
		return nil, err
	}
	out := make([]AssetVariantPlanView, 0, len(planRows))
	for _, row := range planRows {
		view := r.variantPlanView(ctx, row.plan, row.assetCode, &row.assetName)
		view.StoryboardIDs = linksByPlan[row.plan.ID].ids
		view.StoryboardCodes = linksByPlan[row.plan.ID].codes
		if view.StoryboardIDs == nil {
			view.StoryboardIDs = []string{}
		}
		if view.StoryboardCodes == nil {
			view.StoryboardCodes = []string{}
		}
		out = append(out, view)
	}
	return out, nil
}

// planLinks plan id → (storyboard ids, codes)（order: episode_num, order_num）。
type planLinks struct {
	ids   []string
	codes []string
}

func (r *Assets) linksByPlanIDs(ctx context.Context, planIDs []string) (map[string]planLinks, error) {
	result := map[string]planLinks{}
	if len(planIDs) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT l.variant_plan_id, sb.id, sb.storyboard_code
		 FROM asset_variant_storyboard_links l
		 JOIN storyboards sb ON sb.id = l.storyboard_id
		 WHERE l.variant_plan_id = ANY($1) AND l.status = 'active'
		 ORDER BY l.variant_plan_id, sb.episode_num, sb.order_num`, planIDs)
	if err != nil {
		return nil, fmt.Errorf("storyboard codes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var planID, storyboardID string
		var code *string
		if err := rows.Scan(&planID, &storyboardID, &code); err != nil {
			return nil, fmt.Errorf("storyboard codes: %w", err)
		}
		links := result[planID]
		links.ids = append(links.ids, storyboardID)
		links.codes = append(links.codes, strAny(code, ""))
		result[planID] = links
	}
	return result, rows.Err()
}

// variantPlanView 对齐 _variant_plan_read。
func (r *Assets) variantPlanView(ctx context.Context, plan *model.AssetVariantPlans, assetCode *string, assetName *string) AssetVariantPlanView {
	var taskCode *string
	if assetCode != nil {
		taskCode = new(string)
		*taskCode = fmt.Sprintf("%s-V%03d", assetBusinessCode(assetCode), variantCodeNumber(&plan.VariantCode))
	}
	return AssetVariantPlanView{
		ID:              plan.ID,
		ProjectId:       plan.ProjectId,
		EpisodeId:       plan.EpisodeId,
		ScriptVersionId: plan.ScriptVersionId,
		AssetId:         plan.AssetId,
		AssetCode:       assetCode,
		AssetName:       assetName,
		VariantCode:     plan.VariantCode,
		TaskCode:        taskCode,
		VariantKind:     plan.VariantKind,
		TitleZh:         plan.TitleZh,
		DescriptionZh:   plan.DescriptionZh,
		Source:          plan.Source,
		Confidence:      plan.Confidence,
		Status:          plan.Status,
		TaskId:          plan.TaskId,
		StoryboardIDs:   []string{},
		StoryboardCodes: []string{},
		CreatedAt:       plan.CreatedAt,
		UpdatedAt:       plan.UpdatedAt,
	}
}

// ---- POST /assets/variant-plans ----

// CreateAssetVariantPlan 对齐 create_asset_variant_plan（场景/道具变体已退役 → 恒 400）。
func (r *Assets) CreateAssetVariantPlan(ctx context.Context) (*AssetVariantPlanView, error) {
	return nil, &ValueError{Detail: "场景和道具不再按分镜创建变体；请在基础资产任务中上传多角度、多状态候选图片。"}
}

// ---- PATCH /assets/variant-plans/{id} ----

// UpdateAssetVariantPlanInput 对齐 AssetVariantPlanUpdate（仅持久化非零字段）。
type UpdateAssetVariantPlanInput struct {
	TitleZh       *string
	DescriptionZh *string
	Status        *string
	StoryboardIDs []string
	// HasStoryboardIDs 区分「未提供」与「空列表」。
	HasStoryboardIDs bool
}

// UpdateAssetVariantPlan 对齐 update_asset_variant_plan。
func (r *Assets) UpdateAssetVariantPlan(ctx context.Context, actor TaskActor, planID string, input UpdateAssetVariantPlanInput) (*AssetVariantPlanView, error) {
	plan, err := scanAssetVariantPlan(r.pool.QueryRow(ctx,
		`SELECT `+assetVariantPlanColumns+` FROM asset_variant_plans WHERE id = $1`, planID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &KeyError{Arg: planID}
	}
	if err != nil {
		return nil, fmt.Errorf("update variant plan: %w", err)
	}
	if err := r.ensureProjectWriteAccess(ctx, actor, plan.ProjectId); err != nil {
		return nil, err
	}
	asset, err := scanAsset(r.pool.QueryRow(ctx,
		`SELECT `+assetColumns+` FROM assets WHERE id = $1`, plan.AssetId))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &KeyError{Arg: planID}
	}
	if err != nil {
		return nil, fmt.Errorf("update variant plan: %w", err)
	}
	if asset == nil || isInactiveAssetStatus(asset.Status) {
		return nil, &KeyError{Arg: planID}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("update variant plan: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if input.TitleZh != nil {
		plan.TitleZh = *input.TitleZh
	}
	if input.DescriptionZh != nil {
		plan.DescriptionZh = *input.DescriptionZh
	}
	if input.Status != nil {
		plan.Status = *input.Status
	}
	if input.HasStoryboardIDs {
		if err := r.validateVariantStoryboards(ctx, tx, plan.ProjectId, input.StoryboardIDs, plan.EpisodeId); err != nil {
			return nil, err
		}
		existing, err := r.linksByPlanID(ctx, tx, plan.ID)
		if err != nil {
			return nil, err
		}
		requested := map[string]bool{}
		for _, id := range input.StoryboardIDs {
			requested[id] = true
		}
		existingIDs := map[string]bool{}
		for _, link := range existing {
			existingIDs[link.StoryboardId] = true
			if requested[link.StoryboardId] {
				if _, err := tx.Exec(ctx,
					`UPDATE asset_variant_storyboard_links SET status = 'active', source = 'director', updated_at = $1 WHERE id = $2`,
					time.Now().UTC(), link.ID); err != nil {
					return nil, fmt.Errorf("update variant link: %w", err)
				}
			} else if link.Status == "active" {
				if _, err := tx.Exec(ctx,
					`UPDATE asset_variant_storyboard_links SET status = 'removed', source = 'director', updated_at = $1 WHERE id = $2`,
					time.Now().UTC(), link.ID); err != nil {
					return nil, fmt.Errorf("update variant link: %w", err)
				}
			}
		}
		for _, storyboardID := range input.StoryboardIDs {
			if existingIDs[storyboardID] {
				continue
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO asset_variant_storyboard_links (id, variant_plan_id, storyboard_id, source, status, created_by, created_at, updated_at)
				 VALUES ($1,$2,$3,'director','active',$4,$5,$5)`,
				newUUIDString(), plan.ID, storyboardID, actor.ID, time.Now().UTC()); err != nil {
				return nil, fmt.Errorf("insert variant link: %w", err)
			}
		}
	}
	plan.UpdatedAt = time.Now().UTC()
	if _, err := tx.Exec(ctx,
		`UPDATE asset_variant_plans SET title_zh = $1, description_zh = $2, status = $3, updated_at = $4 WHERE id = $5`,
		plan.TitleZh, plan.DescriptionZh, plan.Status, plan.UpdatedAt, plan.ID); err != nil {
		return nil, fmt.Errorf("update variant plan: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("update variant plan: %w", err)
	}
	linksByPlan, err := r.linksByPlanIDs(ctx, []string{plan.ID})
	if err != nil {
		return nil, err
	}
	view := r.variantPlanView(ctx, plan, asset.AssetCode, &asset.Name)
	view.StoryboardCodes = linksByPlan[plan.ID].codes
	if input.HasStoryboardIDs {
		view.StoryboardIDs = input.StoryboardIDs
	}
	return &view, nil
}

func (r *Assets) linksByPlanID(ctx context.Context, q queryer, planID string) ([]*model.AssetVariantStoryboardLinks, error) {
	rows, err := q.Query(ctx,
		`SELECT `+assetVariantLinkColumns+` FROM asset_variant_storyboard_links WHERE variant_plan_id = $1`, planID)
	if err != nil {
		return nil, fmt.Errorf("variant links: %w", err)
	}
	defer rows.Close()
	var out []*model.AssetVariantStoryboardLinks
	for rows.Next() {
		link, scanErr := scanAssetVariantLink(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("variant links: %w", scanErr)
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// validateVariantStoryboards 对齐 _validate_variant_storyboards。
func (r *Assets) validateVariantStoryboards(ctx context.Context, q queryer, projectID string, storyboardIDs []string, episodeID *string) error {
	if len(storyboardIDs) == 0 {
		return nil
	}
	rows, err := q.Query(ctx,
		`SELECT id, episode_num FROM storyboards WHERE project_id = $1 AND id = ANY($2)`, projectID, storyboardIDs)
	if err != nil {
		return fmt.Errorf("validate variant storyboards: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		var episodeNum int32
		if err := rows.Scan(&id, &episodeNum); err != nil {
			return fmt.Errorf("validate variant storyboards: %w", err)
		}
		seen[id] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("validate variant storyboards: %w", err)
	}
	for _, id := range storyboardIDs {
		if !seen[id] {
			return &ValueError{Detail: "存在不属于当前项目的分镜关联。"}
		}
	}
	if episodeID == nil {
		return nil
	}
	episode, err := scanEpisode(q.QueryRow(ctx,
		`SELECT `+episodeColumns+` FROM project_episodes WHERE id = $1`, *episodeID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &ValueError{Detail: "分镜与变体计划不属于同一分集。"}
		}
		return fmt.Errorf("validate variant storyboards: %w", err)
	}
	// 二次扫描校验 episode 匹配（episode_num 在首次查询中未保留，重查一次）。
	found, err := r.allStoryboardsOnEpisode(ctx, q, projectID, storyboardIDs, episode.EpisodeNo)
	if err != nil {
		return err
	}
	if !found {
		return &ValueError{Detail: "分镜与变体计划不属于同一分集。"}
	}
	return nil
}

func (r *Assets) allStoryboardsOnEpisode(ctx context.Context, q queryer, projectID string, storyboardIDs []string, episodeNo int32) (bool, error) {
	rows, err := q.Query(ctx,
		`SELECT count(*) = $3 FROM storyboards WHERE project_id = $1 AND id = ANY($2) AND episode_num <> $4`,
		projectID, storyboardIDs, len(storyboardIDs), episodeNo)
	if err != nil {
		return false, fmt.Errorf("validate variant storyboards: %w", err)
	}
	defer rows.Close()
	var ok bool
	if rows.Next() {
		if err := rows.Scan(&ok); err != nil {
			return false, fmt.Errorf("validate variant storyboards: %w", err)
		}
	}
	return ok, rows.Err()
}

// ---- GET /projects/{pid}/asset-revisions/current/scene-options ----

// ListSceneAssetOptions 对齐 list_scene_asset_options。
func (r *Assets) ListSceneAssetOptions(ctx context.Context, actor TaskActor, projectID, search string) ([]SceneAssetOptionView, error) {
	if err := r.ensureProjectWriteAccess(ctx, actor, projectID); err != nil {
		return nil, err
	}
	conds := []string{"project_id = $1", "asset_type = 'scene'", `status NOT IN ('deleted','excluded')`}
	args := []any{projectID}
	if strings.TrimSpace(search) != "" {
		pattern := "%" + strings.TrimSpace(search) + "%"
		args = append(args, pattern)
		conds = append(conds, fmt.Sprintf("(asset_code ILIKE $%d OR name ILIKE $%d)", len(args), len(args)))
	}
	query := "SELECT " + assetColumns + " FROM assets WHERE " + strings.Join(conds, " AND ") + " ORDER BY asset_code ASC, name ASC"
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list scene options: %w", err)
	}
	defer rows.Close()
	var scenes []*model.Assets
	for rows.Next() {
		a, scanErr := scanAsset(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list scene options: %w", scanErr)
		}
		scenes = append(scenes, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list scene options: %w", err)
	}
	var options []SceneAssetOptionView
	for _, scene := range scenes {
		revision, err := r.artifactRevisionByID(ctx, scene.CurrentRevisionId)
		if err != nil {
			return nil, err
		}
		if revision == nil || revision.SourceType != "human" {
			continue
		}
		sceneCode := strAny(scene.AssetCode, scene.ID)
		episodeCode := strAny(metadataAsMap(scene.MetadataJson)["episode_code"], "")
		var episodePtr *string
		if episodeCode != "" {
			episodePtr = &episodeCode
		}
		revisionID := &revision.ID
		options = append(options, SceneAssetOptionView{
			SceneAssetID: scene.ID,
			SceneCode:    sceneCode,
			SceneName:    scene.Name,
			EpisodeCode:  episodePtr,
			RevisionID:   revisionID,
			Status:       assetConfirmationStatus(scene),
		})
	}
	return options, nil
}

func (r *Assets) artifactRevisionByID(ctx context.Context, id *string) (*model.ArtifactRevisions, error) {
	if id == nil {
		return nil, nil
	}
	rev, err := scanArtifactRevision(r.pool.QueryRow(ctx,
		`SELECT `+artifactRevisionColumns+` FROM artifact_revisions WHERE id = $1`, *id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("artifact revision: %w", err)
	}
	return rev, nil
}

// ---- 项目分集详情：资产绑定读取（对齐 get_project_episode_detail 的 assets 段） ----

const episodeAssetBindingColumns = `id, project_id, episode_id, script_version_id, confirmed_reading_revision_id,
	application_revision_id, asset_id, asset_revision_id, proposal_index, action, status, age_stage_code,
	costume_variant_code, proposal_json, created_at, updated_at`

func scanEpisodeAssetBinding(row pgx.Row) (*model.EpisodeAssetBindings, error) {
	var b model.EpisodeAssetBindings
	err := row.Scan(&b.ID, &b.ProjectId, &b.EpisodeId, &b.ScriptVersionId,
		&b.ConfirmedReadingRevisionId, &b.ApplicationRevisionId, &b.AssetId, &b.AssetRevisionId,
		&b.ProposalIndex, &b.Action, &b.Status, &b.AgeStageCode, &b.CostumeVariantCode,
		&b.ProposalJson, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListEpisodeAssetBindings 对齐 episode detail 的绑定查询（status=active，按 proposal_index 排序）。
func (r *Assets) ListEpisodeAssetBindings(ctx context.Context, projectID, episodeID, scriptVersionID string) ([]*model.EpisodeAssetBindings, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+episodeAssetBindingColumns+` FROM episode_asset_bindings
		 WHERE project_id = $1 AND episode_id = $2 AND script_version_id = $3 AND status = 'active'
		 ORDER BY proposal_index`, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, fmt.Errorf("list episode asset bindings: %w", err)
	}
	defer rows.Close()
	var out []*model.EpisodeAssetBindings
	for rows.Next() {
		b, scanErr := scanEpisodeAssetBinding(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list episode asset bindings: %w", scanErr)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// assetsByIDs 按 ID 批量取资产行。
func (r *Assets) assetsByIDs(ctx context.Context, ids []string) (map[string]*model.Assets, error) {
	result := map[string]*model.Assets{}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT `+assetColumns+` FROM assets WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("assets by ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		a, scanErr := scanAsset(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("assets by ids: %w", scanErr)
		}
		result[a.ID] = a
	}
	return result, rows.Err()
}

// ListEpisodeAssetBindingViews 分集详情 Assets：active 绑定 + 预取资产（绑定资产缺失时跳过）。
func (r *Assets) ListEpisodeAssetBindingViews(ctx context.Context, projectID, episodeID, scriptVersionID string) ([]EpisodeAssetBindingView, error) {
	bindings, err := r.ListEpisodeAssetBindings(ctx, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	idSet := map[string]bool{}
	ids := []string{}
	for _, b := range bindings {
		if b.AssetId != "" && !idSet[b.AssetId] {
			idSet[b.AssetId] = true
			ids = append(ids, b.AssetId)
		}
	}
	assetsByID, err := r.assetsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]EpisodeAssetBindingView, 0, len(bindings))
	for _, b := range bindings {
		asset, ok := assetsByID[b.AssetId]
		if !ok {
			continue
		}
		out = append(out, episodeAssetBindingView(b, asset))
	}
	return out, nil
}

// episodeAssetBindingView 对齐 EpisodeAssetBindingRead 组装（assets 行预取）。
func episodeAssetBindingView(b *model.EpisodeAssetBindings, asset *model.Assets) EpisodeAssetBindingView {
	proposal := map[string]any{}
	if len(b.ProposalJson) > 0 {
		_ = json.Unmarshal(b.ProposalJson, &proposal)
	}
	var assetCode *string
	var assetType string
	var assetName string
	if asset != nil {
		assetCode = asset.AssetCode
		assetType = asset.AssetType
		assetName = asset.Name
	}
	return EpisodeAssetBindingView{
		ID:                 b.ID,
		ProposalIndex:      b.ProposalIndex,
		AssetId:            b.AssetId,
		AssetRevisionId:    b.AssetRevisionId,
		AssetCode:          assetCode,
		AssetType:          assetType,
		AssetName:          assetName,
		Action:             b.Action,
		Status:             b.Status,
		AgeStageCode:       b.AgeStageCode,
		CostumeVariantCode: b.CostumeVariantCode,
		Proposal:           proposal,
	}
}

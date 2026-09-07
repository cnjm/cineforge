package repository

// P3f-d 生成器网络层部分：角色服装依赖、未决资产任务 Prompt 归一化、分镜任务同步。
// 对齐 legacy _link_character_costume_dependencies / refresh_pending_asset_task_prompts /
// _asset_task_prompt_for_existing / _sync_existing_storyboard_task。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// linkCharacterCostumeDependenciesTx 对齐 _link_character_costume_dependencies。
// 将角色基础任务的执行人同步到同角色上下文任务，并为服装道具建立 costume_prop 依赖。
func (r *Tasks) linkCharacterCostumeDependenciesTx(ctx context.Context, tx pgx.Tx,
	projectID string, now time.Time) (int, error) {
	assets, err := r.listAllAssetsTx(ctx, tx, projectID)
	if err != nil {
		return 0, err
	}
	assetsByCode := map[string]*model.Assets{}
	assetsByName := map[string]*model.Assets{}
	for _, asset := range assets {
		if asset.AssetCode != nil {
			assetsByCode[strings.ToUpper(strings.TrimSpace(*asset.AssetCode))] = asset
		}
		if name := strings.TrimSpace(asset.Name); name != "" {
			assetsByName[name] = asset
		}
	}
	tasks, err := r.listAssetTasksTx(ctx, tx, projectID)
	if err != nil {
		return 0, err
	}
	tasksByAsset := map[string][]*model.Tasks{}
	for _, task := range tasks {
		if task.AssetId != nil {
			tasksByAsset[*task.AssetId] = append(tasksByAsset[*task.AssetId], task)
		}
	}
	created := 0
	for _, asset := range assets {
		if asset.AssetType != assetTypeCharacter {
			continue
		}
		characterTasks := tasksByAsset[asset.ID]
		var baseTask *model.Tasks
		for _, task := range characterTasks {
			if task.AgeStageCode == nil && task.CostumeVariantCode == nil &&
				derefValue(task.TaskVariant) == "A" {
				baseTask = task
				break
			}
		}
		if baseTask != nil && baseTask.AssigneeId != nil {
			for _, task := range characterTasks {
				task.AssigneeId = baseTask.AssigneeId
				task.AssignedBy = baseTask.AssignedBy
				assignedAt := baseTask.AssignedAt
				if assignedAt == nil {
					assignedAt = &now
				}
				task.AssignedAt = assignedAt
				if err := persistTask(ctx, tx, task); err != nil {
					return 0, err
				}
			}
		}
		metadata := metadataAsMap(asset.MetadataJson)
		source := map[string]any{}
		if m, ok := metadata["breakdown_asset"].(map[string]any); ok {
			source = m
		}
		stages := anySlice(source["age_stages"])
		if stages == nil {
			stages = anySlice(metadata["age_stages"])
		}
		for _, rawStage := range stages {
			stage, ok := rawStage.(map[string]any)
			if !ok {
				continue
			}
			stageCode := strings.TrimSpace(strOf(stage, "stage_code"))
			costumes := anySlice(stage["costume_variants"])
			for _, rawCostume := range costumes {
				costume, ok := rawCostume.(map[string]any)
				if !ok {
					continue
				}
				costumeCode := strings.TrimSpace(strOf(costume, "variant_code"))
				var contextTasks []*model.Tasks
				for _, task := range characterTasks {
					if derefValue(task.AgeStageCode) == stageCode &&
						derefValue(task.CostumeVariantCode) == costumeCode &&
						isTargetVariantTask(task.TaskVariant) {
						contextTasks = append(contextTasks, task)
					}
				}
				if len(contextTasks) == 0 {
					continue
				}
				var propAssets []*model.Assets
				for _, raw := range append(anyStringList(costume["costume_prop_codes"]),
					anyStringList(costume["costume_prop_ids"])...) {
					key := strings.ToUpper(strings.TrimSpace(raw))
					if key == "" {
						continue
					}
					candidate := assetsByCode[key]
					if candidate != nil && candidate.AssetType == assetTypeProp &&
						!containsAsset(propAssets, candidate) {
						propAssets = append(propAssets, candidate)
					}
				}
				for _, rawName := range anyStringList(costume["costume_prop_names"]) {
					candidate := assetsByName[strings.TrimSpace(rawName)]
					if candidate != nil && candidate.AssetType == assetTypeProp &&
						!containsAsset(propAssets, candidate) {
						propAssets = append(propAssets, candidate)
					}
				}
				for _, propAsset := range propAssets {
					var propTask *model.Tasks
					for _, task := range tasksByAsset[propAsset.ID] {
						variant := derefValue(task.TaskVariant)
						if variant == "MASTER" || variant == "A" || variant == "" {
							propTask = task
							break
						}
					}
					if propTask == nil {
						continue
					}
					for _, contextTask := range contextTasks {
						exists, err := r.taskDependencyExists(ctx, tx,
							contextTask.ID, propTask.ID)
						if err != nil {
							return 0, err
						}
						if exists {
							continue
						}
						if err := r.insertTaskDependencyTx(ctx, tx,
							contextTask.ID, propTask.ID, "costume_prop"); err != nil {
							return 0, err
						}
						created++
					}
				}
			}
		}
	}
	return created, nil
}

func isTargetVariantTask(variant *string) bool {
	switch derefValue(variant) {
	case "A", "B", "C", "D", "E":
		return true
	}
	return false
}

func containsAsset(list []*model.Assets, asset *model.Assets) bool {
	for _, item := range list {
		if item.ID == asset.ID {
			return true
		}
	}
	return false
}

func (r *Tasks) listAllAssetsTx(ctx context.Context, q queryer, projectID string) ([]*model.Assets, error) {
	rows, err := q.Query(ctx, `SELECT `+assetColumns+` FROM assets WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list all assets: %w", err)
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

func (r *Tasks) listAssetTasksTx(ctx context.Context, q queryer, projectID string) ([]*model.Tasks, error) {
	rows, err := q.Query(ctx, `SELECT `+taskColumns+` FROM tasks
		WHERE project_id = $1 AND task_type = 'asset' AND is_retired = FALSE`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list asset tasks: %w", err)
	}
	defer rows.Close()
	return scanTaskRows(rows)
}

func (r *Tasks) taskDependencyExists(ctx context.Context, q queryer, taskID, dependsOnTaskID string) (bool, error) {
	var id string
	err := q.QueryRow(ctx, `SELECT id FROM task_dependencies
		WHERE task_id = $1 AND depends_on_task_id = $2`, taskID, dependsOnTaskID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("task dependency exists: %w", err)
	}
	return true, nil
}

func (r *Tasks) insertTaskDependencyTx(ctx context.Context, q queryer,
	taskID, dependsOnTaskID, dependencyType string) error {
	_, err := q.Exec(ctx, `INSERT INTO task_dependencies (id, task_id, depends_on_task_id, dependency_type)
		VALUES ($1,$2,$3,$4)`, newUUIDString(), taskID, dependsOnTaskID, dependencyType)
	if err != nil {
		return fmt.Errorf("insert task dependency: %w", err)
	}
	return nil
}

// refreshPendingAssetTaskPromptsTx 对齐 refresh_pending_asset_task_prompts。
// 仅覆盖 todo、非 human_temporary、无变体计划的资产任务；返回刷新条数。
func (r *Tasks) refreshPendingAssetTaskPromptsTx(ctx context.Context, tx pgx.Tx,
	projectID string, createdBy string, now time.Time) (int, error) {
	// 两表字段加表前缀扫描（列名与 scanTask/scanAsset 按位对齐，as 别名无需给出）。
	taskFields := "tasks." + strings.Join(strings.Split(taskColumns, ", "), ", tasks.")
	assetFields := "assets." + strings.Join(strings.Split(assetColumns, ", "), ", assets.")
	rows, err := tx.Query(ctx, `SELECT `+taskFields+`, `+assetFields+` FROM tasks
		JOIN assets ON assets.id = tasks.asset_id
		WHERE tasks.project_id = $1 AND tasks.task_type = 'asset' AND tasks.status = 'todo'
			AND tasks.is_retired = FALSE
			AND (tasks.variant_kind IS NULL OR tasks.variant_kind != 'human_temporary')`, projectID)
	if err != nil {
		return 0, fmt.Errorf("refresh prompts rows: %w", err)
	}
	var pairs []taskAssetPair
	for rows.Next() {
		task, asset, err := scanTaskAssetRow(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		pairs = append(pairs, taskAssetPair{task: task, asset: asset})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("refresh prompts rows: %w", err)
	}
	taskIDs := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		taskIDs = append(taskIDs, pair.task.ID)
	}
	humanPromptTaskIDs := map[string]bool{}
	if len(taskIDs) > 0 {
		hrows, err := tx.Query(ctx, `SELECT DISTINCT task_id FROM task_prompts
			WHERE task_id = ANY($1) AND source = 'human'`, taskIDs)
		if err != nil {
			return 0, fmt.Errorf("human prompt task ids: %w", err)
		}
		for hrows.Next() {
			var id string
			if err := hrows.Scan(&id); err != nil {
				hrows.Close()
				return 0, err
			}
			humanPromptTaskIDs[id] = true
		}
		hrows.Close()
		if err := hrows.Err(); err != nil {
			return 0, fmt.Errorf("human prompt task ids: %w", err)
		}
	}
	refreshed := 0
	for _, pair := range pairs {
		task := pair.task
		if task.VariantPlanId != nil {
			continue
		}
		currentPrompt := derefValue(task.LatestPromptText)
		if currentPrompt == "" {
			currentPrompt = derefValue(task.PromptText)
		}
		variant := derefValue(task.TaskVariant)
		if variant == "" {
			variant = "MASTER"
		}
		if humanPromptTaskIDs[task.ID] && !hasForeignAssetViewSections(currentPrompt, variant) {
			continue
		}
		prompt := assetTaskPromptForExisting(pair.asset, task)
		if prompt == "" || prompt == currentPrompt {
			continue
		}
		var maxNo int32
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(version_no), 0) FROM task_prompts
			WHERE task_id = $1`, task.ID).Scan(&maxNo); err != nil {
			return 0, fmt.Errorf("max task prompt version: %w", err)
		}
		promptType := "asset_view_" + strings.ToLower(variant)
		if _, err := tx.Exec(ctx, `INSERT INTO task_prompts (
			id, task_id, prompt_type, prompt_text, version_no, source, created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			newUUIDString(), task.ID, promptType, prompt, maxNo+1,
			"asset_prompt_normalization", strPtrOrNone(createdBy)); err != nil {
			return 0, fmt.Errorf("insert normalized task prompt: %w", err)
		}
		task.LatestPromptText = strPtrOrNone(prompt)
		task.AssetContextOutdated = false
		task.UpdatedAt = now
		if err := persistTask(ctx, tx, task); err != nil {
			return 0, err
		}
		refreshed++
	}
	return refreshed, nil
}

type taskAssetPair struct {
	task  *model.Tasks
	asset *model.Assets
}

// scanTaskAssetRow 扫描 JOIN 后的 62 个结果列（8a task 列 + 8b asset 列）为一个配对。
// pgx.Rows.Scan 要求一次调用消费全部字段，不能对同一行先 scanTask 再 scanAsset。
func scanTaskAssetRow(row pgx.Row) (*model.Tasks, *model.Assets, error) {
	var t model.Tasks
	var a model.Assets
	err := row.Scan(
		&t.ID, &t.ProjectId, &t.EpisodeId, &t.ScriptId, &t.ScriptVersionId, &t.IDempotencyKey,
		&t.ScriptSegmentId, &t.StoryboardId, &t.AssetId, &t.SceneCode, &t.SceneName, &t.TaskType,
		&t.Title, &t.AssigneeId, &t.AssignedBy, &t.AssignedAt, &t.Status, &t.PromptText,
		&t.LatestPromptText, &t.DueAt, &t.CompletedAt, &t.VisibleUntil, &t.ProductionModel,
		&t.MediaType, &t.TaskVariant, &t.AgeStageCode, &t.CostumeVariantCode, &t.VariantPlanId,
		&t.VariantKind, &t.VariantTitleZh, &t.VariantDescriptionZh, &t.PromptRevisionId,
		&t.AssetContextOutdated, &t.DependsOnTaskId, &t.IsRetired, &t.RetiredAt, &t.RetiredBy,
		&t.RetiredReason, &t.CreatedAt, &t.UpdatedAt,
		&a.ID, &a.ProjectId, &a.AssetCode, &a.AssetType, &a.Name, &a.Description,
		&a.Status, &a.Tags, &a.PreviewPath, &a.FilePath, &a.PromptText, &a.BaseModel,
		&a.MetadataJson, &a.Version, &a.CurrentVersionId, &a.CurrentRevisionId, &a.IsLocked,
		&a.LockedBy, &a.LockedAt, &a.CreatedById, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, nil, err
	}
	return &t, &a, nil
}

// assetTaskPromptForExisting 对齐 _asset_task_prompt_for_existing。
func assetTaskPromptForExisting(asset *model.Assets, task *model.Tasks) string {
	variant := derefValue(task.TaskVariant)
	if variant == "" {
		variant = "MASTER"
	}
	specs := assetTaskContextSpecs(asset)
	instruction := "使用最新已确认资产规范生成当前图位。"
	for _, spec := range specs {
		if (spec[0] == derefValue(task.AgeStageCode)) &&
			(spec[1] == derefValue(task.CostumeVariantCode)) &&
			spec[2] == variant {
			instruction = spec[4]
			break
		}
	}
	return assetContextPrompt(asset, derefValue(task.AgeStageCode),
		derefValue(task.CostumeVariantCode), variant, instruction)
}

// syncExistingStoryboardTask 对齐 _sync_existing_storyboard_task。
// 人工/开工/指派任务视为受保护：返回 (false, true)，调用方计入冲突。
func syncExistingStoryboardTask(task *model.Tasks, storyboard *model.Storyboards,
	title, prompt string, now time.Time) (updated, blocked bool) {
	needsUpdate := anyNeedsStoryboardUpdate(task, storyboard, title, prompt)
	if !needsUpdate {
		return false, false
	}
	protected := task.Status != taskStatusTodo ||
		task.AssigneeId != nil ||
		derefValue(task.PromptText) != derefValue(task.LatestPromptText)
	if protected {
		return false, true
	}
	task.ScriptSegmentId = storyboard.ScriptSegmentId
	task.SceneCode = storyboard.SceneCode
	task.SceneName = storyboard.SceneName
	task.Title = title
	task.PromptText = strPtrOrNone(prompt)
	task.LatestPromptText = strPtrOrNone(prompt)
	task.UpdatedAt = now
	return true, false
}

func anyNeedsStoryboardUpdate(task *model.Tasks, storyboard *model.Storyboards,
	title, prompt string) bool {
	return ptrStringNE(task.ScriptSegmentId, storyboard.ScriptSegmentId) ||
		ptrStringNE(task.SceneCode, storyboard.SceneCode) ||
		ptrStringNE(task.SceneName, storyboard.SceneName) ||
		task.Title != title ||
		derefValue(task.PromptText) != prompt ||
		derefValue(task.LatestPromptText) != prompt
}

func ptrStringNE(a, b *string) bool {
	return derefValue(a) != derefValue(b)
}
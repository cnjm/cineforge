package repository

// P3f-d 生成器主入口：GenerateAssetTasks / GenerateStoryboardTasks。
// 对齐 legacy generate_asset_tasks / generate_storyboard_tasks：
//   - 行锁（projects FOR UPDATE）+ 作用域守卫逐字文案
//   - 幂等键 10 键 canonical JSON（缺省 → null）
//   - 确定性后台：不调用任何 Agent 模型
//   - 事务由服务层持有（tx pgx.Tx），本层不 commit/rollback

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

const storyboardColumns = `id, project_id, episode_num, order_num, title, description, dialogue,
	camera, duration_seconds, characters, keyframes, mirror_shots, status, storyboard_code,
	scene_code, scene_name, context_code, render_mode, script_id, script_segment_id, narration,
	shot_type, current_version_id, created_at, updated_at`

func scanStoryboardRow(row pgx.Row) (*model.Storyboards, error) {
	var s model.Storyboards
	if err := row.Scan(&s.ID, &s.ProjectId, &s.EpisodeNum, &s.OrderNum, &s.Title, &s.Description,
		&s.Dialogue, &s.Camera, &s.DurationSeconds, &s.Characters, &s.Keyframes, &s.MirrorShots,
		&s.Status, &s.StoryboardCode, &s.SceneCode, &s.SceneName, &s.ContextCode, &s.RenderMode,
		&s.ScriptId, &s.ScriptSegmentId, &s.Narration, &s.ShotType, &s.CurrentVersionId,
		&s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func scanStoryboardRows(rows pgx.Rows) ([]*model.Storyboards, error) {
	defer rows.Close()
	var out []*model.Storyboards
	for rows.Next() {
		s, err := scanStoryboardRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Tasks) listEpisodeStoryboardsTx(ctx context.Context, q queryer,
	projectID string, episodeNum int32) ([]*model.Storyboards, error) {
	rows, err := q.Query(ctx, `SELECT `+storyboardColumns+` FROM storyboards
		WHERE project_id = $1 AND episode_num = $2 ORDER BY order_num`, projectID, episodeNum)
	if err != nil {
		return nil, fmt.Errorf("list episode storyboards: %w", err)
	}
	return scanStoryboardRows(rows)
}

func (r *Tasks) listAllStoryboardsTx(ctx context.Context, q queryer,
	projectID string) ([]*model.Storyboards, error) {
	rows, err := q.Query(ctx, `SELECT `+storyboardColumns+` FROM storyboards
		WHERE project_id = $1 ORDER BY episode_num, order_num`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list storyboards: %w", err)
	}
	return scanStoryboardRows(rows)
}

// GenerateAssetTasks 对齐 generate_asset_tasks，返回与 legacy 一致的结果 dict。
func (r *Tasks) GenerateAssetTasks(ctx context.Context, tx pgx.Tx,
	projectID, episodeID, scriptVersionID string, actor TaskActor) (map[string]any, error) {
	project, err := r.lockGenerationProjectTx(ctx, tx, projectID, actor)
	if err != nil {
		return nil, err
	}
	episode, script, err := r.loadGenerationScriptScopeTx(ctx, tx, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	confirmedInventory, err := r.requireAssetPromptDesignsForScopeTx(ctx, tx, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	confirmedAssetIDs := map[string]bool{}
	for _, item := range confirmedInventory {
		if id := strings.TrimSpace(strAny(firstNonNil(item["asset_id"], item["id"]), "")); id != "" {
			confirmedAssetIDs[id] = true
		}
	}
	bindings, err := r.listActiveAssetBindingsTx(ctx, tx, projectID, &episodeID, &scriptVersionID)
	if err != nil {
		return nil, err
	}
	filteredBindings := bindings[:0]
	for _, binding := range bindings {
		if confirmedAssetIDs[binding.AssetId] {
			filteredBindings = append(filteredBindings, binding)
		}
	}
	bindings = filteredBindings
	bindingsByAsset := map[string][]*model.EpisodeAssetBindings{}
	var bindingAssetIDs []string
	for _, binding := range bindings {
		if _, ok := bindingsByAsset[binding.AssetId]; !ok {
			bindingAssetIDs = append(bindingAssetIDs, binding.AssetId)
		}
		bindingsByAsset[binding.AssetId] = append(bindingsByAsset[binding.AssetId], binding)
	}
	var assets []*model.Assets
	if len(bindings) > 0 {
		assetsByID := map[string]*model.Assets{}
		_loadAssetRows(ctx, tx, projectID, bindingAssetIDs, assetsByID)
		for _, id := range bindingAssetIDs {
			if asset := assetsByID[id]; asset != nil {
				assets = append(assets, asset)
			}
		}
	} else {
		assets, err = r.taskGenerationAssetsWithoutBindingsTx(ctx, tx,
			project, episode, scriptVersionID, actor.ID)
		if err != nil {
			return nil, err
		}
	}
	if len(assets) == 0 {
		return nil, valueError("当前分集没有已确认资产或可用于兼容回填的资产定稿，不能生成资产任务。")
	}
	now := time.Now().UTC()
	created := 0
	createdByType := map[string]int{"character": 0, "scene": 0, "prop": 0}
	reusedTasks := 0
	for _, asset := range assets {
		existingTasks, err := r.listAssetTasksForAssetTx(ctx, tx, projectID, asset.ID, scriptVersionID)
		if err != nil {
			return nil, err
		}
		for _, task := range existingTasks {
			if task.TaskType == taskTypeTextToImage {
				task.TaskType = taskTypeAsset
				if _, err := tx.Exec(ctx,
					`UPDATE tasks SET task_type = 'asset' WHERE id = $1`, task.ID); err != nil {
					return nil, fmt.Errorf("migrate asset task type: %w", err)
				}
			}
		}
		anyVariantNone := false
		for _, task := range existingTasks {
			if task.TaskVariant == nil && task.AgeStageCode == nil {
				anyVariantNone = true
				break
			}
		}
		if anyVariantNone {
			reusedTasks += len(existingTasks)
			continue
		}
		existingByContext := map[[3]string]*model.Tasks{}
		for _, task := range existingTasks {
			existingByContext[[3]string{
				derefValue(task.AgeStageCode), derefValue(task.CostumeVariantCode),
				derefValue(task.TaskVariant)}] = task
		}
		specs := assetTaskContextSpecs(asset)
		scopedBindings := bindingsByAsset[asset.ID]
		var scoped []*model.EpisodeAssetBindings
		for _, binding := range scopedBindings {
			if binding.AgeStageCode != nil || binding.CostumeVariantCode != nil {
				scoped = append(scoped, binding)
			}
		}
		if len(scoped) > 0 {
			var filtered [][5]string
			for _, spec := range specs {
				matched := false
				for _, binding := range scoped {
					bAge, bCostume := derefValue(binding.AgeStageCode), derefValue(binding.CostumeVariantCode)
					if (bAge == "" || spec[0] == bAge) && (bCostume == "" || spec[1] == bCostume) {
						matched = true
						break
					}
				}
				if matched {
					filtered = append(filtered, spec)
				}
			}
			specs = filtered
		}
		bindingRevisionID := asset.CurrentRevisionId
		for _, binding := range scopedBindings {
			if binding.AssetRevisionId != nil {
				bindingRevisionID = binding.AssetRevisionId
				break
			}
		}
		promptRevisions, err := r.listAssetPromptRevisionsTx(ctx, tx, asset.ID)
		if err != nil {
			return nil, err
		}
		promptRevisionByContext := map[string]string{}
		for _, rev := range promptRevisions {
			if key := strings.TrimSpace(strOf(metadataAsMap(rev.InputSnapshot), "context_key")); key != "" {
				promptRevisionByContext[key] = rev.ID
			}
		}
		for _, spec := range specs {
			ageStage, costumeVariant, variant, variantLabel, instruction := spec[0], spec[1], spec[2], spec[3], spec[4]
			contextKey := [3]string{ageStage, costumeVariant, variant}
			if existingByContext[contextKey] != nil {
				reusedTasks++
				continue
			}
			primaryTask := existingByContext[[3]string{ageStage, costumeVariant, "A"}]
			typeLabel := map[string]string{
				assetTypeCharacter: "人物",
				assetTypeScene:     "场景",
				assetTypeProp:      "道具",
			}[asset.AssetType]
			if typeLabel == "" {
				typeLabel = "资产"
			}
			prompt := assetContextPrompt(asset, ageStage, costumeVariant, variant, instruction)
			contextLabel := strings.TrimSpace(strings.Join(filterNonEmptyStrings(ageStage, costumeVariant), " "))
			promptContextKey := strings.Join(filterNonEmptyStrings(derefValue(asset.AssetCode), ageStage, costumeVariant), "/")
			if promptContextKey == "" {
				promptContextKey = strAny(asset.AssetCode, asset.ID)
			}
			var dependsOn *string
			if primaryTask != nil && (variant == "B" || variant == "C" || variant == "D" || variant == "E") {
				v := primaryTask.ID
				dependsOn = &v
			}
			dueDays := 2
			if variant != "A" && variant != "MASTER" {
				dueDays = 3
			}
			identity := map[string]any{
				"project_id":        projectID,
				"episode_id":        episodeID,
				"script_version_id": scriptVersionID,
				"task_type":         taskTypeAsset,
				"storyboard_id":     nil,
				"asset_id":          asset.ID,
				"task_variant":      variant,
				"age_stage_code":    ageStage,
				"costume_variant_code": costumeVariant,
				"media_type":        nil,
			}
			promptRevisionID := promptRevisionIdOf(promptRevisionByContext, promptContextKey, bindingRevisionID)
			task := &model.Tasks{
				ID:                newUUIDString(),
				ProjectId:         projectID,
				EpisodeId:         &episodeID,
				ScriptId:          &script.ID,
				ScriptVersionId:   &scriptVersionID,
				IDempotencyKey:    strPtrOrNone(taskIdempotencyKey(identity)),
				AssetId:           &asset.ID,
				TaskType:          taskTypeAsset,
				TaskVariant:       strPtrOrNone(variant),
				AgeStageCode:      strPtrOrNone(ageStage),
				CostumeVariantCode: strPtrOrNone(costumeVariant),
				PromptRevisionId:  &promptRevisionID,
				DependsOnTaskId:   dependsOn,
				Title:             strings.TrimSpace(fmt.Sprintf("%s资产 %s %s %s [%s] %s", typeLabel, derefValue(asset.AssetCode), asset.Name, contextLabel, variant, variantLabel)),
				Status:            taskStatusTodo,
				PromptText:        strPtrOrNone(prompt),
				LatestPromptText:  strPtrOrNone(prompt),
				ProductionModel:   strPtrOrNone(strAny(asset.BaseModel, "Seedream")),
				DueAt:             ptrToTime(now.AddDate(0, 0, dueDays)),
				CreatedAt:         now,
				UpdatedAt:         now,
			}
			if err := r.insertTask(ctx, tx, task); err != nil {
				return nil, err
			}
			existingByContext[contextKey] = task
			created++
			if _, ok := createdByType[asset.AssetType]; ok {
				createdByType[asset.AssetType]++
			}
		}
	}
	characterAssets, err := r.listCharacterAssetsForAudioTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	storyboards, err := r.listAllStoryboardsTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	latestBreakdownContent := r.latestUnscopedBreakdownContentTx(ctx, tx, projectID)
	audioResult, err := r.createAudioAssetTasksTx(ctx, tx, project, episode, script, scriptVersionID,
		characterAssets, storyboards, breakdownDialogueScript(latestBreakdownContent), actor.ID, now)
	if err != nil {
		return nil, err
	}
	created += int(audioResult["created_tasks"].(int))
	dependencyLinks, err := r.linkCharacterCostumeDependenciesTx(ctx, tx, projectID, now)
	if err != nil {
		return nil, err
	}
	variantDependencies := map[string]any{"plans": 0, "variant_tasks": 0, "dependencies": 0}
	refreshedPrompts, err := r.refreshPendingAssetTaskPromptsTx(ctx, tx, projectID, actor.ID, now)
	if err != nil {
		return nil, err
	}
	currentStage := project.CurrentStage
	if currentStage == "" {
		currentStage = project.Status
	}
	if currentStage == "" {
		currentStage = "breakdown_review"
	}
	detail := map[string]any{
		"created_tasks":       created,
		"assets":              len(assets),
		"episode_id":          episodeID,
		"script_version_id":   scriptVersionID,
		"dependency_links":    dependencyLinks,
		"variant_dependencies": variantDependencies,
		"refreshed_prompts":   refreshedPrompts,
		"audio":               audioResult,
	}
	if err := insertOperationLog(ctx, tx, actor.ID, &projectID, nil, "project",
		opLogAssetTasksGenerated, detail); err != nil {
		return nil, err
	}
	return map[string]any{
		"created_tasks":           created,
		"character_tasks":         createdByType[assetTypeCharacter],
		"scene_tasks":             createdByType[assetTypeScene],
		"prop_tasks":              createdByType[assetTypeProp],
		"theme_music_tasks":       audioResult["theme_music_tasks"],
		"background_music_tasks":  audioResult["background_music_tasks"],
		"character_voice_tasks":   audioResult["voice_tasks"],
		"reused_tasks":            reusedTasks,
		"failed_items":            []any{},
		"assets":                  len(assets),
		"episode_id":              episodeID,
		"script_version_id":       scriptVersionID,
		"dependency_links":        dependencyLinks,
		"variant_dependencies":    variantDependencies,
		"refreshed_prompts":       refreshedPrompts,
		"audio":                   audioResult,
		"stage":                   currentStage,
	}, nil
}

func promptRevisionIdOf(byContext map[string]string, key string, fallback *string) string {
	if id, ok := byContext[key]; ok {
		return id
	}
	return derefValue(fallback)
}

func (r *Tasks) listAssetTasksForAssetTx(ctx context.Context, q queryer,
	projectID, assetID, scriptVersionID string) ([]*model.Tasks, error) {
	rows, err := q.Query(ctx, `SELECT `+taskColumns+` FROM tasks
		WHERE project_id = $1 AND asset_id = $2 AND script_version_id = $3
			AND storyboard_id IS NULL AND task_type IN ('asset','text_to_image')
			AND is_retired = FALSE`, projectID, assetID, scriptVersionID)
	if err != nil {
		return nil, fmt.Errorf("list asset tasks for asset: %w", err)
	}
	return scanTaskRows(rows)
}

func (r *Tasks) listAssetPromptRevisionsTx(ctx context.Context, q queryer, assetID string) ([]*model.ArtifactRevisions, error) {
	rows, err := q.Query(ctx, `SELECT `+artifactRevisionColumns+` FROM artifact_revisions
		WHERE artifact_type = 'asset_prompt' AND artifact_id = $1 ORDER BY version_no DESC`, assetID)
	if err != nil {
		return nil, fmt.Errorf("list asset prompt revisions: %w", err)
	}
	defer rows.Close()
	var out []*model.ArtifactRevisions
	for rows.Next() {
		rev, err := scanArtifactRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

func (r *Tasks) listCharacterAssetsForAudioTx(ctx context.Context, q queryer,
	projectID string) ([]*model.Assets, error) {
	rows, err := q.Query(ctx, `SELECT `+assetColumns+` FROM assets
		WHERE project_id = $1 AND asset_type = 'character' AND status NOT IN ('deleted','excluded')
		ORDER BY asset_code, name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list character assets for audio: %w", err)
	}
	defer rows.Close()
	var out []*model.Assets
	for rows.Next() {
		asset, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		if metadataAsMap(asset.MetadataJson)["source"] != "human_temporary" {
			out = append(out, asset)
		}
	}
	return out, rows.Err()
}

func (r *Tasks) latestUnscopedBreakdownContentTx(ctx context.Context, tx pgx.Tx,
	projectID string) map[string]any {
	return r.latestBreakdownContentTx(ctx, tx, projectID)
}

// GenerateStoryboardTasks 对齐 generate_storyboard_tasks，返回与 legacy 一致的结果 dict。
func (r *Tasks) GenerateStoryboardTasks(ctx context.Context, tx pgx.Tx,
	projectID, episodeID, scriptVersionID string, actor TaskActor) (map[string]any, error) {
	if _, err := r.lockGenerationProjectTx(ctx, tx, projectID, actor); err != nil {
		return nil, err
	}
	episode, _, err := r.loadGenerationScriptScopeTx(ctx, tx, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	storyboards, err := r.listEpisodeStoryboardsTx(ctx, tx, projectID, episode.EpisodeNo)
	if err != nil {
		return nil, err
	}
	breakdownContent := r.latestScopedBreakdownContentTx(ctx, tx, projectID, episodeID, scriptVersionID)
	content := map[string]any{}
	if breakdownContent != nil {
		content = breakdownContent
	}
	finalRevisionID := uuidOrNone(strAny(firstNonNil(
		mapNestedValue(content, "raw_output", "confirmed_storyboard_revision_id"),
		"",
	), ""))
	if finalRevisionID != nil {
		finalRevision, err := r.getArtifactRevisionByIDTx(ctx, tx, *finalRevisionID)
		if err != nil {
			return nil, err
		}
		if finalRevision == nil || finalRevision.DataState != "final" {
			return nil, valueError("分镜尚未形成 human_final 修订，不能生成分镜任务。")
		}
	} else {
		return nil, valueError("分镜尚未形成 human_final 修订，不能生成分镜任务。")
	}
	blocking := validateBreakdownViewBlocking(content)
	if len(blocking) > 0 {
		details := strings.Join(takeStrings(blocking, 8), "；")
		return nil, valueError("分镜任务生成前校验失败：" + details)
	}
	currentItems := []map[string]any{}
	if breakdownContent != nil {
		for _, item := range mapList(content["storyboards"]) {
			if storyboardEpisodeCode(item) == episode.EpisodeCode {
				currentItems = append(currentItems, item)
			}
		}
	}
	if len(currentItems) > 0 {
		currentCodes := map[string]bool{}
		for _, item := range currentItems {
			if code := strings.TrimSpace(strOf(item, "storyboard_code")); code != "" {
				currentCodes[code] = true
			}
		}
		currentOrders := map[int32]bool{}
		for index, item := range currentItems {
			currentOrders[safeInt32(firstNonNil(item["order_num"], item["order_no"]), int32(index+1))] = true
		}
		filtered := storyboards[:0]
		for _, item := range storyboards {
			if (item.StoryboardCode != nil && currentCodes[*item.StoryboardCode]) ||
				currentOrders[item.OrderNum] {
				filtered = append(filtered, item)
			}
		}
		storyboards = filtered
	}
	if len(storyboards) == 0 {
		return nil, valueError("没有分镜数据，请先完成并保存分镜拆解。")
	}
	currentStoryboardIDs := map[string]bool{}
	for _, item := range storyboards {
		currentStoryboardIDs[item.ID] = true
	}
	episodeStoryboardTasks, err := r.listEpisodeStoryboardTasksTx(ctx, tx, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	var staleTasks []*model.Tasks
	for _, task := range episodeStoryboardTasks {
		if task.StoryboardId != nil && !currentStoryboardIDs[*task.StoryboardId] {
			staleTasks = append(staleTasks, task)
		}
	}
	if len(staleTasks) > 0 {
		staleStoryboardIDs := map[string]bool{}
		for _, task := range staleTasks {
			if task.StoryboardId != nil {
				staleStoryboardIDs[*task.StoryboardId] = true
			}
		}
		return nil, valueError(fmt.Sprintf(
			"新分镜版本移除了 %d 个已分发分镜，关联 %d 条旧任务；为避免新旧任务并存，已停止自动同步，请先处理任务迁移。",
			len(staleStoryboardIDs), len(staleTasks)))
	}
	now := time.Now().UTC()
	created := 0
	updated := 0
	var protectedConflicts []string
	for _, storyboard := range storyboards {
		existingTasks, err := r.listStoryboardTasksTx(ctx, tx, projectID, episodeID, scriptVersionID, storyboard.ID)
		if err != nil {
			return nil, err
		}
		hasStoryboardShot := false
		for _, task := range existingTasks {
			if task.TaskType == taskTypeStoryboardShot {
				hasStoryboardShot = true
				break
			}
		}
		if hasStoryboardShot {
			continue
		}
		existingByType := map[string]*model.Tasks{}
		for _, task := range existingTasks {
			existingByType[task.TaskType] = task
		}
		sceneLabel := strAny(storyboard.SceneName, strAny(storyboard.SceneCode, fmt.Sprintf("EP%02d", storyboard.EpisodeNum)))
		keyframePrompt := storyboardKeyframePrompt(storyboard)
		videoPrompt := storyboardVideoPrompt(storyboard)
		keyframeTitle := fmt.Sprintf("%s 分镜 %03d 关键帧", sceneLabel, storyboard.OrderNum)
		videoTitle := fmt.Sprintf("%s 分镜 %03d 视频", sceneLabel, storyboard.OrderNum)
		keyframeTask := existingByType[taskTypeTextToImage]
		if keyframeTask == nil {
			keyframeTask = &model.Tasks{
				ID:               newUUIDString(),
				ProjectId:        projectID,
				EpisodeId:        &episodeID,
				ScriptId:         storyboard.ScriptId,
				ScriptVersionId:  &scriptVersionID,
				StoryboardId:     &storyboard.ID,
				ScriptSegmentId:  storyboard.ScriptSegmentId,
				SceneCode:        storyboard.SceneCode,
				SceneName:        storyboard.SceneName,
				TaskType:         taskTypeTextToImage,
				IDempotencyKey:   strPtrOrNone(taskIdempotencyKey(storyboardIdentity(projectID, episodeID, scriptVersionID, taskTypeTextToImage, storyboard.ID, nil, nil))),
				Title:            keyframeTitle,
				Status:           taskStatusTodo,
				PromptText:       strPtrOrNone(keyframePrompt),
				LatestPromptText: strPtrOrNone(keyframePrompt),
				ProductionModel:  strPtrOrNone("Seedream"),
				DueAt:            ptrToTime(now.AddDate(0, 0, 2)),
				CreatedAt:        now,
				UpdatedAt:        now,
			}
			if err := r.insertTask(ctx, tx, keyframeTask); err != nil {
				return nil, err
			}
			created++
		} else {
			keyframeUpdated, keyframeBlocked := syncExistingStoryboardTask(keyframeTask, storyboard,
				keyframeTitle, keyframePrompt, now)
			if keyframeBlocked {
				protectedConflicts = append(protectedConflicts, keyframeTask.Title)
			} else if keyframeUpdated {
				updated++
				if err := persistTask(ctx, tx, keyframeTask); err != nil {
					return nil, err
				}
			}
		}
		if existingByType[taskTypeImageToVideo] == nil {
			videoTask := &model.Tasks{
				ID:               newUUIDString(),
				ProjectId:        projectID,
				EpisodeId:        &episodeID,
				ScriptId:         storyboard.ScriptId,
				ScriptVersionId:  &scriptVersionID,
				StoryboardId:     &storyboard.ID,
				ScriptSegmentId:  storyboard.ScriptSegmentId,
				SceneCode:        storyboard.SceneCode,
				SceneName:        storyboard.SceneName,
				TaskType:         taskTypeImageToVideo,
				IDempotencyKey:   strPtrOrNone(taskIdempotencyKey(storyboardIdentity(projectID, episodeID, scriptVersionID, taskTypeImageToVideo, storyboard.ID, &keyframeTask.ID, nil))),
				DependsOnTaskId:  &keyframeTask.ID,
				AssigneeId:       keyframeTask.AssigneeId,
				Title:            videoTitle,
				Status:           taskStatusTodo,
				PromptText:       strPtrOrNone(videoPrompt),
				LatestPromptText: strPtrOrNone(videoPrompt),
				ProductionModel:  strPtrOrNone("Seedance"),
				DueAt:            ptrToTime(now.AddDate(0, 0, 3)),
				CreatedAt:        now,
				UpdatedAt:        now,
			}
			if err := r.insertTask(ctx, tx, videoTask); err != nil {
				return nil, err
			}
			created++
		} else {
			videoTask := existingByType[taskTypeImageToVideo]
			videoUpdated, videoBlocked := syncExistingStoryboardTask(videoTask, storyboard,
				videoTitle, videoPrompt, now)
			if videoBlocked {
				protectedConflicts = append(protectedConflicts, videoTask.Title)
			} else if videoUpdated {
				updated++
				if err := persistTask(ctx, tx, videoTask); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(protectedConflicts) > 0 {
		samples := strings.Join(takeStrings(protectedConflicts, 5), "、")
		suffix := ""
		if len(protectedConflicts) > 5 {
			suffix = fmt.Sprintf(" 等 %d 条", len(protectedConflicts))
		}
		return nil, valueError(fmt.Sprintf(
			"新分镜与已指派、已开工或人工修改的任务存在 Prompt 冲突：%s%s。系统未覆盖任何旧任务，请先确认任务迁移方案。",
			samples, suffix))
	}
	autoVariantPlans := 0
	variantDependencies := map[string]any{"plans": 0, "variant_tasks": 0, "dependencies": 0}
	detail := map[string]any{
		"created_tasks":        created,
		"updated_tasks":        updated,
		"storyboards":          len(storyboards),
		"episode_id":           episodeID,
		"script_version_id":    scriptVersionID,
		"auto_variant_plans":   autoVariantPlans,
		"variant_dependencies": variantDependencies,
	}
	if err := insertOperationLog(ctx, tx, actor.ID, &projectID, nil, "project",
		opLogStoryboardTasksGenerated, detail); err != nil {
		return nil, err
	}
	return map[string]any{
		"created_tasks":        created,
		"updated_tasks":        updated,
		"storyboards":          len(storyboards),
		"episode_id":           episodeID,
		"script_version_id":    scriptVersionID,
		"auto_variant_plans":   autoVariantPlans,
		"variant_dependencies": variantDependencies,
	}, nil
}

func (r *Tasks) listEpisodeStoryboardTasksTx(ctx context.Context, q queryer,
	projectID, episodeID, scriptVersionID string) ([]*model.Tasks, error) {
	rows, err := q.Query(ctx, `SELECT `+taskColumns+` FROM tasks
		WHERE project_id = $1 AND episode_id = $2 AND script_version_id = $3
			AND storyboard_id IS NOT NULL AND is_retired = FALSE
			AND task_type IN ('storyboard_shot','text_to_image','image_to_video')`,
		projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, fmt.Errorf("list episode storyboard tasks: %w", err)
	}
	return scanTaskRows(rows)
}

func (r *Tasks) listStoryboardTasksTx(ctx context.Context, q queryer,
	projectID, episodeID, scriptVersionID, storyboardID string) ([]*model.Tasks, error) {
	rows, err := q.Query(ctx, `SELECT `+taskColumns+` FROM tasks
		WHERE project_id = $1 AND episode_id = $2 AND script_version_id = $3
			AND storyboard_id = $4 AND is_retired = FALSE
			AND task_type IN ('storyboard_shot','text_to_image','image_to_video')`,
		projectID, episodeID, scriptVersionID, storyboardID)
	if err != nil {
		return nil, fmt.Errorf("list storyboard tasks: %w", err)
	}
	return scanTaskRows(rows)
}

// storyboardIdentity 构造分镜任务幂等键 10 键 identity。
func storyboardIdentity(projectID, episodeID, scriptVersionID, taskType, storyboardID string,
	assetID *string, _ any) map[string]any {
	return map[string]any{
		"project_id":         projectID,
		"episode_id":         episodeID,
		"script_version_id":  scriptVersionID,
		"task_type":          taskType,
		"storyboard_id":      storyboardID,
		"asset_id":           assetID,
		"task_variant":       nil,
		"age_stage_code":     nil,
		"costume_variant_code": nil,
		"media_type":         nil,
	}
}

func mapNestedValue(m map[string]any, keys ...string) any {
	var current any = m
	for _, key := range keys {
		d, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = d[key]
	}
	return current
}

func takeStrings(in []string, n int) []string {
	if len(in) > n {
		return in[:n]
	}
	return in
}

func filterNonEmptyStrings(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
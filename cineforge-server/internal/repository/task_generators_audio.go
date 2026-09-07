package repository

// P3f-d 生成器音频部分：计划与创建音频资产任务。
// 对齐 legacy _planned_audio_tasks / _create_audio_asset_tasks 及辅助函数：
//   - 主题音乐（项目级）、背景音乐（分集级）、S/A 级有台词角色音色（项目级）
//   - 项目级音频幂等键省略 episode/script_version（_task_idempotency_key 的 None）
//   - 纯确定性：不含任何模型调用

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// audioTaskSpec 对齐 legacy _AudioTaskSpec。
type audioTaskSpec struct {
	AssetType          string
	AssetCode          string
	AudioType          string
	Name               string
	Description        string
	Prompt             string
	Scope              string
	EpisodeCode        string
	RoleAssetID        string
	RoleCode           string
	RolePriority       string
	DialogueSceneCount int
}

// assetHasDialogue 对齐 _asset_has_dialogue（metadata/breakdown/reading 三级来源）。
func assetHasDialogue(asset *model.Assets) bool {
	metadata := metadataAsMap(asset.MetadataJson)
	fallbackBreakdown := map[string]any{}
	if m, ok := metadata["breakdown_asset"].(map[string]any); ok {
		fallbackBreakdown = m
	}
	breakdownMetadata := map[string]any{}
	if m, ok := fallbackBreakdown["metadata"].(map[string]any); ok {
		breakdownMetadata = m
	}
	reading := map[string]any{}
	if m, ok := metadata["reading_candidate"].(map[string]any); ok {
		reading = m
	}
	sources := []map[string]any{metadata, fallbackBreakdown, breakdownMetadata, reading}
	for _, source := range sources {
		value := source["has_dialogue"]
		if b, ok := value.(bool); ok && b {
			return true
		}
		if s := strings.ToLower(strings.TrimSpace(strAny(value, ""))); s != "" {
			switch s {
			case "true", "yes", "1", "有台词":
				return true
			}
		}
		for _, key := range []string{"dialogue_evidence", "dialogue_scene_codes", "speaking_scene_codes"} {
			if list, ok := source[key].([]any); ok && len(list) > 0 {
				return true
			}
		}
		for _, key := range []string{"dialogue_scene_count", "dialogue_count"} {
			n, ok := safeIntTry(source[key])
			if ok && n > 0 {
				return true
			}
		}
	}
	return false
}

func safeIntTry(v any) (int, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case int:
		return t, true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}

// audioMatchToken 对齐 _audio_match_token（casefold + 保留 a-z0-9 与中文）。
func audioMatchToken(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (r >= 0x4e00 && r <= 0x9fff) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// characterAudioAliases 对齐 _character_audio_aliases。
func characterAudioAliases(asset *model.Assets) map[string]bool {
	metadata := metadataAsMap(asset.MetadataJson)
	breakdown := map[string]any{}
	if m, ok := metadata["breakdown_asset"].(map[string]any); ok {
		breakdown = m
	}
	aliases := map[string]bool{}
	collect := func(v any) {
		if list, ok := v.([]any); ok {
			for _, item := range list {
				if s := audioMatchToken(fmt.Sprintf("%v", item)); s != "" {
					aliases[s] = true
				}
			}
		}
	}
	collect(metadata["aliases"])
	collect(breakdown["aliases"])
	values := []string{strAny(asset.AssetCode, ""), libraryLeafCode(asset.AssetCode, ""), asset.Name}
	for _, value := range values {
		if t := audioMatchToken(value); t != "" {
			aliases[t] = true
		}
	}
	for _, part := range strings.FieldsFunc(asset.Name, func(r rune) bool {
		return strings.ContainsRune(" ·._-", r)
	}) {
		if len([]rune(strings.TrimSpace(part))) >= 3 {
			if t := audioMatchToken(part); t != "" {
				aliases[t] = true
			}
		}
	}
	return aliases
}

// dialogueSceneCounts 对齐 _dialogue_scene_counts。
func dialogueSceneCounts(characterAssets []*model.Assets, storyboards []*model.Storyboards,
	dialogueScript []map[string]any) map[string]int {
	aliasesByAsset := map[string]map[string]bool{}
	assetsByAlias := map[string]map[string]bool{}
	for _, asset := range characterAssets {
		aliases := characterAudioAliases(asset)
		aliasesByAsset[asset.ID] = aliases
		for alias := range aliases {
			if assetsByAlias[alias] == nil {
				assetsByAlias[alias] = map[string]bool{}
			}
			assetsByAlias[alias][asset.ID] = true
		}
	}
	dialogueScenes := map[string]map[string]bool{}
	for _, asset := range characterAssets {
		dialogueScenes[asset.ID] = map[string]bool{}
	}
	matchedAssets := func(value string) map[string]bool {
		token := audioMatchToken(value)
		return assetsByAlias[token]
	}
	sceneKey := func(sceneCode, sceneName any) string {
		code := libraryLeafCode(nil, "")
		if s := strings.TrimSpace(strAny(sceneCode, "")); s != "" {
			code = libraryLeafCode(&s, "")
		}
		if code != "" {
			return code
		}
		if sceneName != nil {
			if t := audioMatchToken(strAny(sceneName, "")); t != "" {
				return "NAME:" + t
			}
		}
		return ""
	}
	for _, scene := range dialogueScript {
		if scene == nil {
			continue
		}
		key := sceneKey(scene["scene_code"], scene["scene_name"])
		if key == "" {
			continue
		}
		for _, beat := range anySlice(scene["beats"]) {
			beatMap, ok := beat.(map[string]any)
			if !ok || strings.TrimSpace(strAny(beatMap["text"], "")) == "" {
				continue
			}
			for assetID := range matchedAssets(strAny(beatMap["speaker"], "")) {
				dialogueScenes[assetID][key] = true
			}
		}
	}
	for _, storyboard := range storyboards {
		key := sceneKey(strAny(storyboard.SceneCode, ""), strAny(storyboard.SceneName, ""))
		if key == "" {
			continue
		}
		referenced := map[string]bool{}
		for _, reference := range jsonStringSlice(storyboard.Characters) {
			for id := range matchedAssets(reference) {
				referenced[id] = true
			}
		}
		dialogueValues := []*string{storyboard.Dialogue}
		for _, item := range jsonMapList(storyboard.MirrorShots) {
			dialogueValues = append(dialogueValues, valueStringPtr(item["dialogue"]))
		}
		for _, dialogue := range dialogueValues {
			text := strings.TrimSpace(strAny(dialogue, ""))
			if text == "" {
				continue
			}
			if len(referenced) == 1 {
				for id := range referenced {
					dialogueScenes[id][key] = true
				}
			}
			for _, line := range strings.Split(text, "\n") {
				speaker := strings.TrimSpace(line)
				if idx := strings.IndexAny(speaker, "::"); idx >= 0 {
					speaker = speaker[:idx]
				}
				for assetID := range matchedAssets(speaker) {
					dialogueScenes[assetID][key] = true
				}
			}
		}
	}
	out := map[string]int{}
	for assetID, keys := range dialogueScenes {
		out[assetID] = len(keys)
	}
	return out
}

func valueStringPtr(v any) *string {
	s := fmt.Sprintf("%v", v)
	if v == nil {
		return nil
	}
	return &s
}

// breakdownDialogueScript 对齐 _breakdown_dialogue_script。
func breakdownDialogueScript(content map[string]any) []map[string]any {
	rawOutput := map[string]any{}
	if m, ok := content["raw_output"].(map[string]any); ok {
		rawOutput = m
	}
	values := anySlice(rawOutput["dialogue_script"])
	if values == nil {
		values = anySlice(content["dialogue_script"])
	}
	var out []map[string]any
	for _, item := range values {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// plannedAudioTasks 对齐 _planned_audio_tasks。
func plannedAudioTasks(projectPrefix string, characterAssets []*model.Assets,
	storyboards []*model.Storyboards, dialogueScript []map[string]any, episodeCode string) []audioTaskSpec {
	prefix := libraryCode(projectPrefix, "RF")
	episode := libraryCode(episodeCode, "EP")
	specs := []audioTaskSpec{
		{
			AssetType:   assetTypeMusic,
			AssetCode:   prefix + "-THEME-MUSIC",
			AudioType:   "theme_music",
			Name:        "主题音乐",
			Description: "项目主题音乐母版（项目级，全项目共用一首）。",
			Prompt:      "制作项目主题音乐母版，建立稳定的核心旋律、情绪识别和品牌记忆，供整个项目复用；交付 WAV 或 MP3。",
			Scope:       "project",
		},
		{
			AssetType:   assetTypeMusic,
			AssetCode:   prefix + "-" + episode + "-BACK-MUSIC",
			AudioType:   "background_music",
			Name:        "背景音乐",
			Description: episode + " 分集背景音乐母版。",
			Prompt:      fmt.Sprintf("制作 %s 分集背景音乐母版，支持对白场景铺底和本集跨场景复用，控制动态范围并避免遮蔽人声；交付 WAV 或 MP3。", episode),
			Scope:       "episode",
			EpisodeCode: episode,
		},
	}
	counts := dialogueSceneCounts(characterAssets, storyboards, dialogueScript)
	sorted := append([]*model.Assets(nil), characterAssets...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ci, cj := strAny(sorted[i].AssetCode, ""), strAny(sorted[j].AssetCode, "")
		if ci != cj {
			return ci < cj
		}
		return sorted[i].Name < sorted[j].Name
	})
	for _, asset := range sorted {
		priority := assetPriority(map[string]any{"metadata": metadataAsMap(asset.MetadataJson)})
		dialogueSceneCount := counts[asset.ID]
		if priority != "S" && priority != "A" {
			continue
		}
		if !(dialogueSceneCount > 0 || assetHasDialogue(asset)) {
			continue
		}
		roleCode := roleAssetCode(asset.AssetCode)
		reason := ""
		if dialogueSceneCount > 0 {
			reason = fmt.Sprintf("%s级角色，%d 个已归一化对白场景", priority, dialogueSceneCount)
		} else {
			reason = fmt.Sprintf("%s级角色，资产抽取已确认本集有台词", priority)
		}
		name := asset.Name
		specs = append(specs, audioTaskSpec{
			AssetType:          assetTypeVoiceProfile,
			AssetCode:          fmt.Sprintf("%s-AUDIO-VOICE-%s-BASE", prefix, roleCode),
			AudioType:          "voice",
			Name:               name + " 角色音色",
			Description:        fmt.Sprintf("%s 的项目级基础音色母版；生成依据：%s。", name, reason),
			Prompt:             fmt.Sprintf("为角色 %s（%s，%s）制作稳定的基础音色母版。保持年龄、性格、情绪张力和语言习惯一致，提供可用于多场景对白的清晰干声；交付 WAV 或 MP3。", name, roleCode, reason),
			Scope:              "project",
			RoleAssetID:        asset.ID,
			RoleCode:           roleCode,
			RolePriority:       priority,
			DialogueSceneCount: dialogueSceneCount,
		})
	}
	return specs
}

// createAudioAssetTasksTx 对齐 _create_audio_asset_tasks。
func (r *Tasks) createAudioAssetTasksTx(ctx context.Context, tx pgx.Tx,
	project *model.Projects, episode *model.ProjectEpisodes, script *model.Scripts,
	scriptVersionID string, characterAssets []*model.Assets, storyboards []*model.Storyboards,
	dialogueScript []map[string]any, userID string, now time.Time) (map[string]any, error) {
	specs := plannedAudioTasks(
		strAny(project.ProjectPrefix, strAny(project.ProjectNo, "RF")),
		characterAssets, storyboards, dialogueScript,
		episode.EpisodeCode,
	)
	assetCodes := make([]string, 0, len(specs))
	for _, spec := range specs {
		assetCodes = append(assetCodes, spec.AssetCode)
	}
	existingAssets := map[string]*model.Assets{}
	if len(assetCodes) > 0 {
		rows, err := tx.Query(ctx, `SELECT `+assetColumns+` FROM assets
			WHERE project_id = $1 AND asset_code = ANY($2)`, project.ID, assetCodes)
		if err != nil {
			return nil, fmt.Errorf("existing audio assets: %w", err)
		}
		for rows.Next() {
			asset, err := scanAsset(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			existingAssets[strAny(asset.AssetCode, "")] = asset
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("existing audio assets: %w", err)
		}
	}
	existingAudioTasks, err := r.listAudioTasksTx(ctx, tx, project.ID)
	if err != nil {
		return nil, err
	}
	taskAssetIDs := map[string]bool{}
	for _, task := range existingAudioTasks {
		if task.AssetId != nil {
			taskAssetIDs[*task.AssetId] = true
		}
	}
	createdAssets := 0
	createdTasks := 0
	voiceTasks := 0
	themeMusicTasks := 0
	backgroundMusicTasks := 0
	for _, spec := range specs {
		asset := existingAssets[spec.AssetCode]
		roleAssetID := any(nil)
		if spec.RoleAssetID != "" {
			roleAssetID = spec.RoleAssetID
		}
		metadata := map[string]any{
			"audio_type":           spec.AudioType,
			"audio_scope":          spec.Scope,
			"episode_code":         strPtrOrNone(spec.EpisodeCode),
			"role_asset_id":        roleAssetID,
			"role_code":            strPtrOrNone(spec.RoleCode),
			"role_priority":        strPtrOrNone(spec.RolePriority),
			"dialogue_scene_count": spec.DialogueSceneCount,
			"source":               "asset_finalization",
		}
		if asset == nil {
			asset = &model.Assets{
				ID:          newUUIDString(),
				ProjectId:   &project.ID,
				AssetCode:   &spec.AssetCode,
				AssetType:   spec.AssetType,
				Name:        spec.Name,
				Description: strPtrOrNone(spec.Description),
				Status:      "in_progress",
				Tags:        jsonB([]string{"audio", spec.AudioType}),
				MetadataJson: jsonB(metadata),
				Version:     1,
				CreatedById: strPtrOrNone(userID),
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			_, err := tx.Exec(ctx, `INSERT INTO assets (id, project_id, asset_code, asset_type, name, description,
				status, tags, metadata_json, version, created_by_id, is_locked, created_at, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,FALSE,$12,$13)`,
				asset.ID, asset.ProjectId, asset.AssetCode, asset.AssetType, asset.Name, asset.Description,
				asset.Status, asset.Tags, asset.MetadataJson, asset.Version, asset.CreatedById,
				asset.CreatedAt, asset.UpdatedAt)
			if err != nil {
				return nil, fmt.Errorf("insert audio asset: %w", err)
			}
			existingAssets[spec.AssetCode] = asset
			createdAssets++
		} else {
			merged := map[string]any{}
			for k, v := range metadataAsMap(asset.MetadataJson) {
				merged[k] = v
			}
			for k, v := range metadata {
				merged[k] = v
			}
			asset.MetadataJson = jsonB(merged)
			asset.Description = strPtrOrNone(spec.Description)
			asset.UpdatedAt = now
			if _, err := tx.Exec(ctx, `UPDATE assets SET metadata_json=$2, description=$3, updated_at=$4
				WHERE id=$1`, asset.ID, asset.MetadataJson, asset.Description, asset.UpdatedAt); err != nil {
				return nil, fmt.Errorf("update audio asset: %w", err)
			}
		}
		if taskAssetIDs[asset.ID] {
			continue
		}
		isProjectScope := spec.Scope == "project"
		identity := map[string]any{
			"project_id":        project.ID,
			"episode_id":        nil,
			"script_version_id": nil,
			"task_type":         "audio",
			"storyboard_id":     nil,
			"asset_id":          asset.ID,
			"task_variant":      "MASTER",
			"age_stage_code":    nil,
			"costume_variant_code": nil,
			"media_type":        spec.AudioType,
		}
		if !isProjectScope {
			identity["episode_id"] = episode.ID
			identity["script_version_id"] = scriptVersionID
		}
		task := &model.Tasks{
			ID:               newUUIDString(),
			ProjectId:        project.ID,
			EpisodeId:        script.EpisodeId,
			ScriptId:         &script.ID,
			ScriptVersionId:  &scriptVersionID,
			IDempotencyKey:   strPtrOrNone(taskIdempotencyKey(identity)),
			AssetId:          &asset.ID,
			TaskType:         "audio",
			Title:            "音频资产 " + spec.Name,
			Status:           taskStatusTodo,
			PromptText:       strPtrOrNone(spec.Prompt),
			LatestPromptText: strPtrOrNone(spec.Prompt),
			ProductionModel:  strPtrOrNone("音频制作"),
			MediaType:        strPtrOrNone(spec.AudioType),
			TaskVariant:      strPtrOrNone("MASTER"),
			DueAt:            ptrToTime(now.AddDate(0, 0, 3)),
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := r.insertTask(ctx, tx, task); err != nil {
			return nil, err
		}
		taskAssetIDs[asset.ID] = true
		createdTasks++
		switch spec.AudioType {
		case "voice":
			voiceTasks++
		case "theme_music":
			themeMusicTasks++
		case "background_music":
			backgroundMusicTasks++
		}
	}
	return map[string]any{
		"created_assets":          createdAssets,
		"created_tasks":           createdTasks,
		"voice_tasks":             voiceTasks,
		"theme_music_tasks":       themeMusicTasks,
		"background_music_tasks":  backgroundMusicTasks,
		"planned_tasks":           len(specs),
	}, nil
}

func (r *Tasks) listAudioTasksTx(ctx context.Context, q queryer, projectID string) ([]*model.Tasks, error) {
	rows, err := q.Query(ctx, `SELECT `+taskColumns+` FROM tasks
		WHERE project_id = $1 AND task_type = 'audio' AND is_retired = FALSE`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list audio tasks: %w", err)
	}
	defer rows.Close()
	return scanTaskRows(rows)
}
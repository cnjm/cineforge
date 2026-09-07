package service

// P4e-6 任务提示词上下文（对齐 legacy app/services/repositories.py 的
// task_production_context + app/agents/production_context.py 的
// CHARACTER_VIEW_REQUIREMENTS / scope_character_production_context）：
//
//	TaskProductionContext      → 任务制作生产上下文（asset_task 专用契约）
//	AssetToPayload             → AssetRead 序列化字典（list_assets 的 model_dump(mode="json")）
//	StoryboardToPayload        → StoryboardRead 序列化字典
//
// 人物 A-E 视图要求与 field_sources 覆盖逐字对齐 legacy；快照取
// candidate.input_snapshot（含则取快照，否则整份 candidate）。

import (
	"encoding/json"
	"fmt"
	"strings"

	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
)

// characterViewRequirements 对齐 CHARACTER_VIEW_REQUIREMENTS（A-E）。
var characterViewRequirements = map[string]map[string]string{
	"A": {
		"title":                 "正面全身 A-Pose",
		"framing":               "正面平视，全身从头到脚完整呈现，人物居中，作为后续图位的主参考图。",
		"pose":                  "标准 A-Pose 站立，双臂从身体两侧自然展开约 30 度，手指放松，双脚与肩同宽，身体重心居中，神态自然。",
		"reference_requirement": "无前置参考图；本图作为 B-E 的角色一致性主参考图。",
	},
	"B": {
		"title":                 "侧面或 3/4 全身",
		"framing":               "侧面或 45 度半侧全身构图，从头到脚完整呈现。",
		"pose":                  "自然直立，四肢放松且不遮挡服装轮廓，头部与身体朝向一致。",
		"reference_requirement": "必须使用已审核通过的图 A，保持身份、骨相、发型、体型和服装一致。",
	},
	"C": {
		"title":                 "背面全身",
		"framing":               "背面平视全身构图，完整展示发型、服装和配饰背面。",
		"pose":                  "背向镜头自然直立，双臂稍离躯干，不遮挡服装背部结构和配饰。",
		"reference_requirement": "必须使用已审核通过的图 A，保持身份、骨相、发型、体型和服装一致。",
	},
	"D": {
		"title":                 "面部与表情参考",
		"framing":               "面部近景构图，清晰展示五官、发型、肤色和核心表情。",
		"pose":                  "头部自然稳定，双唇自然闭合，目光与角色设定一致，不遮挡五官。",
		"reference_requirement": "必须使用已审核通过的图 A，保持身份、骨相、发型和肤色一致。",
	},
	"E": {
		"title":                 "服装与材质细节",
		"framing":               "服装与配饰细节构图，清晰展示层次、纹理、接缝和关键材质。",
		"pose":                  "保持静止且服装自然垂落，不遮挡需要展示的材质、纹理与结构细节。",
		"reference_requirement": "必须使用已审核通过的图 A，保持当前装扮设计和材质一致。",
	},
}

// AssetToPayload 对齐 asset_to_read(...).model_dump(mode="json")（prompt-jobs 输入用）。
// 不包含版本/锁内部字段——legacy AssetRead 序列化即此形状。
func AssetToPayload(a *model.Assets) map[string]any {
	return map[string]any{
		"id":            a.ID,
		"project_id":    viewStrPtr(a.ProjectId),
		"asset_code":    viewStrPtr(a.AssetCode),
		"status":        a.Status,
		"asset_type":    a.AssetType,
		"name":          a.Name,
		"description":   viewStrPtr(a.Description),
		"tags":          viewStrings(a.Tags),
		"preview_path":  viewStrPtr(a.PreviewPath),
		"file_path":     viewStrPtr(a.FilePath),
		"prompt_text":   viewStrPtr(a.PromptText),
		"base_model":    viewStrPtr(a.BaseModel),
		"metadata":      viewMap(a.MetadataJson),
		"version":       a.Version,
		"created_by_id": viewStrPtr(a.CreatedById),
		"created_at":    a.CreatedAt,
		"updated_at":    a.UpdatedAt,
	}
}

// StoryboardToPayload 对齐 StoryboardRead.model_dump(mode="json")。
func StoryboardToPayload(s repository.StoryboardView) map[string]any {
	return map[string]any{
		"id":                        s.ID,
		"project_id":                s.ProjectId,
		"script_segment_id":         viewStrPtr(s.ScriptSegmentId),
		"episode_num":               s.EpisodeNum,
		"order_num":                 s.OrderNum,
		"storyboard_code":           viewStrPtr(s.StoryboardCode),
		"title":                     viewStrPtr(s.Title),
		"description":               s.Description,
		"dialogue":                  viewStrPtr(s.Dialogue),
		"dialogue_back_translation": viewStrPtr(s.DialogueBackTranslation),
		"camera":                    viewStrPtr(s.Camera),
		"shot_type":                 viewStrPtr(s.ShotType),
		"duration_seconds":          s.DurationSeconds,
		"characters":                nonNilSlice(s.Characters),
		"keyframes":                 nonNilSlices(s.Keyframes),
		"mirror_shots":              nonNilSlices(s.MirrorShots),
		"scene_code":                viewStrPtr(s.SceneCode),
		"scene_name":                viewStrPtr(s.SceneName),
		"context_code":              viewStrPtr(s.ContextCode),
		"render_mode":               viewStrPtr(s.RenderMode),
		"status":                    s.Status,
	}
}

// TaskProductionContext 对齐 task_production_context（asset_payload + task → dict）。
// 空/不可用输入返回 {}（legacy 直接 return {}）。
func TaskProductionContext(asset map[string]any, task repository.TaskView) map[string]any {
	metadata := viewDict(asset["metadata"])
	breakdownAsset := viewDict(metadata["breakdown_asset"])
	breakdownMetadata := viewDict(breakdownAsset["metadata"])
	contexts := metadata["production_contexts"]
	if contexts == nil {
		contexts = breakdownMetadata["production_contexts"]
	}
	pc, ok := contexts.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	assetCode := strings.TrimSpace(viewText(asset["asset_code"]))
	keyParts := []string{}
	if assetCode != "" {
		keyParts = append(keyParts, assetCode)
	}
	if task.AgeStageCode != nil && *task.AgeStageCode != "" {
		keyParts = append(keyParts, *task.AgeStageCode)
	}
	if task.CostumeVariantCode != nil && *task.CostumeVariantCode != "" {
		keyParts = append(keyParts, *task.CostumeVariantCode)
	}
	contextKey := strings.Join(keyParts, "/")
	candidate := pc[contextKey]
	if _, isDict := candidate.(map[string]any); !isDict && len(pc) == 1 {
		for _, v := range pc {
			candidate = v
		}
	}
	candidateMap, ok := candidate.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	snapshot := candidateMap
	if in, ok := candidateMap["input_snapshot"].(map[string]any); ok {
		snapshot = in
	}
	context := viewCopy(snapshot)
	viewCode := strings.ToUpper(strings.TrimSpace(viewText(task.TaskVariant)))
	if viewText(asset["asset_type"]) == "character" {
		if _, ok := characterViewRequirements[viewCode]; ok {
			return scopeCharacterProductionContext(context, viewCode)
		}
	}
	return context
}

// scopeCharacterProductionContext 对齐 scope_character_production_context。
// 单视图调用（view 必须是 CHARACTER_VIEW_REQUIREMENTS key，否则返回 dict(context)）。
func scopeCharacterProductionContext(ctx map[string]any, viewCode string) map[string]any {
	allowed := strings.ToUpper(strings.TrimSpace(viewCode))
	if _, ok := characterViewRequirements[allowed]; !ok {
		return viewCopy(ctx)
	}
	var requirements []map[string]any
	for _, item := range viewListOfDicts(ctx["view_requirements"]) {
		if strings.ToUpper(viewText(item["code"])) == allowed {
			requirements = append(requirements, item)
		}
	}
	if len(requirements) == 0 {
		req := map[string]any{"code": allowed}
		for k, v := range characterViewRequirements[allowed] {
			req[k] = v
		}
		requirements = append(requirements, req)
	}
	scoped := viewCopy(ctx)
	scoped["output_spec"] = allowed
	scoped["view_requirements"] = requirements
	scoped["generation_scope"] = map[string]any{
		"view_codes": []string{allowed},
		"mode":       "single_view",
	}
	lineage := viewDict(scoped["lineage"])
	fs := viewDict(lineage["field_sources"])
	newFS := viewCopy(fs)
	newFS["output_spec"] = "system_runtime_scope"
	newFS["view_requirements"] = "system_runtime_scope"
	newFS["generation_scope"] = "system_runtime_scope"
	lineage["field_sources"] = newFS
	scoped["lineage"] = lineage
	return scoped
}

// ---- 纯值工具（对齐 legacy _text / _dict / _list_of_dicts / _text_list）----

// viewText 对齐 _text(value) = str(value or "").strip()。
func viewText(v any) string {
	if !viewTruthy(v) {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// viewTruthy 对齐 Python truthiness（0/0.0/False/""/None → false）。
func viewTruthy(v any) bool {
	if v == nil {
		return false
	}
	switch t := v.(type) {
	case string:
		return t != ""
	case bool:
		return t
	case float64:
		return t != 0
	case int:
		return t != 0
	default:
		return true
	}
}

// viewDict 对齐 _dict(value) = dict(value) if dict else {}。
func viewDict(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// viewListOfDicts 对齐 _list_of_dicts(value)：list 中 dict 项的浅拷贝。
func viewListOfDicts(v any) []map[string]any {
	if list, ok := v.([]any); ok {
		out := make([]map[string]any, 0, len(list))
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, viewCopy(m))
			}
		}
		return out
	}
	if list, ok := v.([]map[string]any); ok {
		out := make([]map[string]any, 0, len(list))
		for _, m := range list {
			out = append(out, viewCopy(m))
		}
		return out
	}
	return nil
}

func viewCopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func viewStrPtr(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func viewStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}

func viewMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func nonNilSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func nonNilSlices(in []map[string]any) []map[string]any {
	if in == nil {
		return []map[string]any{}
	}
	return in
}

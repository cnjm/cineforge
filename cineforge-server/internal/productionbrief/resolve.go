package productionbrief

// P4e-6 解析生产上下文（对齐 legacy app/agents/execution_context.py）：
//
//	BuildResolvedProductionBrief  → ResolvedProductionBrief.v1（field_sources 逐字对齐）
//	BuildAgentExecutionContext   → task prompt-jobs 的顶层输入上下文
//	AspectProfile / CultureContextByCode → brief catalog 查询（deep copy 语义）
//
// 错误文案（"Unsupported delivery aspect ratio: {r}" / "Unknown production style: {id}"）
// 在 prompt-jobs 路由侧映射为 400 "任务制作上下文不完整：..."。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// AspectProfile 对齐 legacy aspect_profile：按 delivery_aspect_ratio value 取条目。
func AspectProfile(aspectRatio string) map[string]any {
	initCatalogs()
	target := strings.TrimSpace(aspectRatio)
	for _, item := range _briefCatalog.DeliveryAspectRatios {
		if item.Value == target {
			return structToMap(item)
		}
	}
	return nil
}

// CultureContextByCode 对齐 legacy culture_context_by_code：按大写 context_code 取条目。
func CultureContextByCode(code string) map[string]any {
	target := strings.ToUpper(strings.TrimSpace(code))
	for _, item := range _briefCatalog.CulturalContexts {
		if item.ContextCode == target {
			return structToMap(item)
		}
	}
	return nil
}

// ResolveInput build_resolved_production_brief / build_agent_execution_context 入参。
type ResolveInput struct {
	ProjectID       string
	ProjectTitle    string
	ProjectPrefix   string
	ProductionBrief json.RawMessage
	ScriptContext   map[string]any
}

// BuildResolvedProductionBrief 对齐 legacy execution_context.build_resolved_production_brief。
// 中间 model_validate 的 ValidationError 收敛为普通 error（路由侧统一 400）。
func BuildResolvedProductionBrief(in ResolveInput) (map[string]any, error) {
	if len(in.ProductionBrief) == 0 {
		return nil, errors.New("production_brief is required")
	}
	project, err := ParseProjectBrief(in.ProductionBrief)
	if err != nil {
		return nil, err
	}
	episodeMap := scriptMapValue(in.ScriptContext["episode_production_brief"])
	if episodeMap == nil {
		return nil, errors.New("episode_production_brief is required")
	}
	episodeRaw, err := json.Marshal(episodeMap)
	if err != nil {
		return nil, err
	}
	episode, err := ParseEpisodeBrief(episodeRaw)
	if err != nil {
		return nil, err
	}
	aspect := AspectProfile(project.DeliveryAspectRatio)
	if aspect == nil {
		return nil, fmt.Errorf("Unsupported delivery aspect ratio: %s", project.DeliveryAspectRatio)
	}
	style := GetPrimaryStyle(project.PrimaryStyleID)
	if style == nil {
		return nil, fmt.Errorf("Unknown production style: %s", project.PrimaryStyleID)
	}

	contexts := make([]map[string]any, 0, len(project.CulturalContexts))
	grammars := map[string]string{}
	for _, item := range project.CulturalContexts {
		catalogItem := CultureContextByCode(item.ContextCode)
		grammar := optionalString(item.NarrativeGrammar)
		if grammar == "" {
			grammar = stringMapValue(catalogItem, "narrative_grammar")
		}
		if grammar == "" {
			grammar = "cn_drama"
		}
		grammars[item.ContextCode] = grammar
		contexts = append(contexts, map[string]any{
			"context_code":      item.ContextCode,
			"name":              item.Name,
			"region":            item.Region,
			"era":               item.Era,
			"world_type":        item.WorldType,
			"timeline":          item.Timeline,
			"narrative_grammar": grammar,
		})
	}

	openingHook := 3
	if v, ok := numberValue(aspect["opening_hook_seconds"]); ok {
		openingHook = v
	}

	fields := map[string]string{
		"project_title":                 "project_record",
		"project_prefix":                "project_record",
		"episode_code":                  "system_default",
		"content_type":                  "project_production_brief",
		"delivery_aspect_ratio":         "project_production_brief",
		"target_duration_seconds":       "episode_production_brief",
		"primary_style_id":              "project_production_brief",
		"style_catalog_version":         "project_production_brief",
		"cultural_contexts":             "project_production_brief",
		"primary_cultural_context_code": "project_production_brief",
	}
	if rawTruthy(in.ScriptContext["episode_code"]) {
		fields["episode_code"] = "script_version"
	}
	for k, v := range project.FieldSources {
		fields[k] = v
	}
	for k, v := range episode.FieldSources {
		fields[k] = v
	}
	fields["document_type"] = "system_default"
	fields["fidelity_mode"] = "system_default"
	fields["asset_master_aspect_ratio"] = "system_default"
	fields["opening_hook_seconds"] = "system_derived"
	fields["script_language"] = "system_default"
	if rawTruthy(in.ScriptContext["language"]) {
		fields["script_language"] = "script_version"
	}
	fields["resolved_style"] = "production_style_catalog"
	fields["narrative_grammar_by_context"] = "system_derived"

	return map[string]any{
		"schema_version":                "ResolvedProductionBrief.v1",
		"project_title":                 in.ProjectTitle,
		"project_prefix":                in.ProjectPrefix,
		"episode_code":                  scriptString(in.ScriptContext, "episode_code", "EP01"),
		"script_language":               scriptString(in.ScriptContext, "language", "unknown"),
		"content_type":                  project.ContentType,
		"delivery_aspect_ratio":         project.DeliveryAspectRatio,
		"target_duration_seconds":       episode.TargetDurationSeconds,
		"asset_master_aspect_ratio":     "16:9",
		"opening_hook_seconds":          openingHook,
		"primary_style_id":              project.PrimaryStyleID,
		"style_catalog_version":         project.StyleCatalogVersion,
		"resolved_style":                structToMap(style),
		"cultural_contexts":             contexts,
		"primary_cultural_context_code": project.PrimaryCulturalContextCode,
		"narrative_grammar_by_context":  grammars,
		"document_type":                 "non_standard_script",
		"fidelity_mode":                 "strict_source",
		"field_sources":                 fields,
	}, nil
}

// BuildAgentExecutionContext 对齐 legacy execution_context.build_agent_execution_context。
func BuildAgentExecutionContext(in ResolveInput) (map[string]any, error) {
	resolved, err := BuildResolvedProductionBrief(in)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"project_id":                in.ProjectID,
		"project_title":             in.ProjectTitle,
		"project_prefix":            in.ProjectPrefix,
		"episode_num":               scriptInt(in.ScriptContext, "episode_no", 1),
		"episode_id":                in.ScriptContext["episode_id"],
		"episode_code":              scriptString(in.ScriptContext, "episode_code", "EP01"),
		"script_id":                 in.ScriptContext["script_id"],
		"script_version_id":         in.ScriptContext["script_version_id"],
		"script_version_no":         in.ScriptContext["script_version_no"],
		"script_source":             mcopy(in.ScriptContext),
		"script_text":               scriptString(in.ScriptContext, "script_text", ""),
		"production_brief":          rawOrEmptyMap(in.ProductionBrief),
		"episode_production_brief":  scriptMapValue(in.ScriptContext["episode_production_brief"]),
		"resolved_production_brief": resolved,
	}, nil
}

// ---- 纯函数 ----

// scriptString 对齐 str(script_context.get(key) or fallback)：空值按 fallback。
func scriptString(m map[string]any, key, fallback string) string {
	if v, ok := m[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			if s != "" {
				return s
			}
		} else {
			return fmt.Sprintf("%v", v)
		}
	}
	return fallback
}

// rawTruthy 对齐 `if script_context.get(key):` 的 Python truthiness。
func rawTruthy(v any) bool {
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

// scriptInt 对齐 int(script_context.get(key) or fallback)。
func scriptInt(m map[string]any, key string, fallback int) int {
	if v, ok := m[key]; ok && v != nil {
		return intFromAny(v, fallback)
	}
	return fallback
}

// scriptMapValue map 形态值（nil/非 map → nil）。
func scriptMapValue(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// stringMapValue 取 map 的字符串值。
func stringMapValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func optionalString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// numberValue 读取 JSON 数字字段（catalog unmarshal 后为 float64）。
func numberValue(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

func intFromAny(v any, fallback int) int {
	if n, ok := numberValue(v); ok {
		return n
	}
	return fallback
}

// structToMap 通过 JSON 深拷贝为合法字典（map 也可用；密钥不含二进制）。
func structToMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func rawOrEmptyMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		return map[string]any{}
	}
	return m
}

// mcopy 浅拷贝 map（dict(script_context) 语义；值保持原样）。
func mcopy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

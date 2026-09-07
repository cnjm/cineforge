package repository

// P3f-d 任务生成器 prompt 构造套件（对齐 legacy repositories.py 的
// _asset_task_specs / _asset_task_context_specs / _asset_context_prompt /
// _asset_view_prompt / _asset_variant_prompt 及 _ASSET_VIEW_DEFINITIONS / _POSES
// 等确定性纯函数）。仅做字符串拼装，不触碰 DB。

import (
	"encoding/json"
	"regexp"
	"strings"

	"cineforge/server/internal/model"
)

// assetViewDefinitions 对齐 _ASSET_VIEW_DEFINITIONS：variant upper → (title, description)。
var assetViewDefinitions = map[string][2]string{
	"A": {"正面全身 A-Pose", "正面平视，全身从头到脚完整呈现，人物居中，作为后续图位的主参考图。"},
	"B": {"侧面或 3/4 全身", "侧面或 45 度半侧全身构图，从头到脚完整呈现，保持图 A 的角色与服装一致。"},
	"C": {"背面全身", "背面平视全身构图，完整展示发型、服装和配饰背面，保持图 A 设计。"},
	"D": {"面部与表情参考", "面部近景构图，清晰展示五官、发型、肤色和核心表情，保持图 A 一致。"},
	"E": {"服装与材质细节", "服装与配饰细节构图，清晰展示层次、纹理、接缝和关键材质。"},
}

// assetViewPoses 对齐 _ASSET_VIEW_POSES。
var assetViewPoses = map[string]string{
	"A": "标准 A-Pose 站立，双臂从身体两侧自然展开约 30 度，手指放松，双脚与肩同宽，身体重心居中，神态自然。",
	"B": "自然直立，四肢放松且不遮挡服装轮廓，头部与身体朝向一致，神态保持与图 A 连续。",
	"C": "背向镜头自然直立，双臂稍离躯干，手部放松，不遮挡服装背部结构和配饰。",
	"D": "头部自然稳定，双唇自然闭合，目光与角色设定一致，避免夸张表情和遮挡五官。",
	"E": "保持静止且服装自然垂落，手臂和配饰不遮挡需要展示的材质、纹理与结构细节。",
}

// assetViewFusionMarkers / assetViewFusionCodes 对齐 _is_fused_asset_view_text 的判定词。
var (
	assetViewFusionMarkers = []string{"正面", "侧面", "45", "背面", "面部近景", "服装细节", "材质细节"}
	assetViewFusionCodes   = []string{"图位 A", "图位 B", "图位 C", "图位 D", "图位 E", "图A", "图B", "图C", "图D", "图E"}
	foreignViewCodeRe      = regexp.MustCompile(`图(?:位)?\s*([A-E])\s*[:：]`)
)

// ---- 资产行元数据读取（对齐 Asset.metadata_json 解析）----

// assetMetadata 解析 assets.metadata_json 为 map（失败→空 map）。
func assetMetadata(asset *model.Assets) map[string]any {
	out := map[string]any{}
	if asset.MetadataJson == nil {
		return out
	}
	_ = json.Unmarshal(asset.MetadataJson, &out)
	return out
}

// metadataFieldMap 取 metadata 下 nested key 的 dict（对齐 isinstance 判定）。
func metadataFieldMap(metadata map[string]any, key string) map[string]any {
	if m, ok := metadata[key].(map[string]any); ok {
		return m
	}
	return nil
}

// assetBreakdownSource 对齐 breakdown_asset 字段。
func assetBreakdownSource(metadata map[string]any) map[string]any {
	return metadataFieldMap(metadata, "breakdown_asset")
}

// assetBreakdownStages 对齐 age_stages（breakdown_asset.age_stages || metadata.age_stages）。
func assetBreakdownStages(asset *model.Assets) []map[string]any {
	metadata := assetMetadata(asset)
	source := assetBreakdownSource(metadata)
	stages := listOfMaps(source["age_stages"])
	if len(stages) == 0 {
		stages = listOfMaps(metadata["age_stages"])
	}
	return stages
}

func assetCostumeVariants(stage map[string]any) []map[string]any {
	return listOfMaps(stage["costume_variants"])
}

func listOfMaps(v any) []map[string]any {
	switch typed := v.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := []map[string]any{}
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// ---- 任务规格 ----

// assetTaskContextSpecs 对齐 _asset_task_context_specs：返回
// (age_stage_code, costume_variant_code, variant, variant_label, instruction)。
func assetTaskContextSpecs(asset *model.Assets) [][5]string {
	if asset.AssetType != "character" {
		return flatTaskSpecs(asset)
	}
	stages := assetBreakdownStages(asset)
	if len(stages) == 0 {
		return flatTaskSpecs(asset)
	}
	var out [][5]string
	for _, stage := range stages {
		stageCode := strings.TrimSpace(strOf(stage, "stage_code"))
		if stageCode == "" {
			stageCode = ""
		}
		variants := assetCostumeVariants(stage)
		if len(variants) == 0 {
			variants = []map[string]any{{}}
		}
		outputSpec := strings.ToUpper(strOf(stage, "output_spec"))
		if outputSpec == "" {
			outputSpec = "A-E"
		}
		specs := characterViewSpecs(outputSpec != "A")
		for _, costume := range variants {
			costumeCode := ""
			if costume != nil {
				costumeCode = strings.TrimSpace(strOf(costume, "variant_code"))
			}
			for _, spec := range specs {
				out = append(out, [5]string{stageCode, costumeCode, spec[0], spec[1], spec[2]})
			}
		}
	}
	if len(out) == 0 {
		return flatTaskSpecs(asset)
	}
	return out
}

func flatTaskSpecs(asset *model.Assets) [][5]string {
	specs := assetTaskSpecs(asset.AssetType, assetPriorityRow(asset))
	out := make([][5]string, 0, len(specs))
	for _, spec := range specs {
		out = append(out, [5]string{"", "", spec[0], spec[1], spec[2]})
	}
	return out
}

// characterViewSpecs 对齐 _character_view_specs。
func characterViewSpecs(full bool) [][3]string {
	specs := [][3]string{
		{"A", "正面全身 A-Pose", "正面平视，全身完整，人物居中，双臂自然展开，纯净背景，作为后续视图的主参考图。"},
	}
	if full {
		specs = append(specs, [][3]string{
			{"B", "侧面或 3/4 全身", "引用并严格保持图 A 的脸型、发型、体型和服装，生成侧面或 3/4 全身视图。"},
			{"C", "背面全身", "引用并严格保持图 A，完整展示发型、服装背部结构和背面配饰。"},
			{"D", "面部与表情参考", "引用图 A，生成面部近景与核心表情参考，保持五官和发型一致。"},
			{"E", "服装与材质细节", "引用图 A，展示服装、配饰、纹理和关键材质细节，不改变整体设计。"},
		}...)
	}
	return specs
}

// ---- 上下文 prompt ----

// assetContextPrompt 对齐 _asset_context_prompt。
func assetContextPrompt(asset *model.Assets, ageStageCode, costumeVariantCode, variant, instruction string) string {
	metadata := assetMetadata(asset)
	source := assetBreakdownSource(metadata)
	var stage map[string]any
	for _, item := range assetBreakdownStages(asset) {
		if item != nil && strOf(item, "stage_code") == ageStageCode {
			stage = item
			break
		}
	}
	var costume map[string]any
	if stage != nil {
		for _, item := range assetCostumeVariants(stage) {
			if item != nil && strOf(item, "variant_code") == costumeVariantCode {
				costume = item
				break
			}
		}
	}
	viewPrompt := assetViewPrompt(source, metadata, variant, stage, costume)
	identity := assetCharacterIdentityPrompt(asset, source, metadata)
	ageChange := assetAgeStagePrompt(stage)
	costumeChange := assetCostumeVariantPrompt(costume)
	var contextSections []string
	if identity != "" {
		contextSections = append(contextSections, "角色身份锚点："+identity)
	}
	if ageChange != "" {
		contextSections = append(contextSections, "年龄阶段变化（"+ageStageCode+"）："+ageChange)
	}
	if costumeChange != "" {
		contextSections = append(contextSections, "当前装扮变化（"+costumeVariantCode+"）："+costumeChange)
	}
	const defaultNegative = "避免身份与年龄漂移、骨相五官和体型变化、发型变化、服装结构或材质变化、错误肢体、裁切遮挡、透视畸变、模糊、文字和水印。"
	if viewPrompt != nil {
		negative := strings.TrimSpace(strOf(viewPrompt, "negative_prompt"))
		if negative == "" {
			negative = defaultNegative
		}
		sections := append([]string{}, contextSections...)
		if prompt := strings.TrimSpace(strOf(viewPrompt, "prompt")); prompt != "" {
			sections = append(sections, prompt)
		}
		sections = append(sections, "负向要求："+negative)
		if strAny(viewPrompt["pose"], "") != "" || variantRuneSet[strings.ToUpper(variant)] {
			sections = append(sections, "姿态要求："+assetViewPose(variant, viewPrompt["pose"]))
		}
		if description := strOf(viewPrompt, "description"); description != "" {
			sections = append(sections, "图位说明："+description)
		}
		if reference := strOf(viewPrompt, "reference_requirement"); reference != "" {
			sections = append(sections, "参考图依赖："+reference)
		}
		sections = append(sections, "生产要求：中性纯色摄影棚背景，柔和均匀布光，角色与服装色彩准确，轮廓和材质边缘清晰，无场景叙事元素干扰。")
		return joinPromptSections(sections)
	}
	if asset.AssetType != "character" {
		return assetVariantPrompt(strAny(asset.PromptText, ""), variant, instruction)
	}
	definition := assetViewDefinitions[strings.ToUpper(variant)]
	fallbackDescription := definition[1]
	if fallbackDescription == "" {
		fallbackDescription = instruction
	}
	sections := append([]string{}, contextSections...)
	sections = append(sections, "图位 "+variant+"："+fallbackDescription)
	sections = append(sections, "负向要求："+defaultNegative)
	if pose := assetViewPoses[strings.ToUpper(variant)]; pose != "" {
		sections = append(sections, "姿态要求："+pose)
	}
	sections = append(sections, "图位说明："+fallbackDescription)
	sections = append(sections, "参考图依赖："+assetViewReferenceRequirement(variant))
	sections = append(sections, "生产要求：中性纯色摄影棚背景，柔和均匀布光，角色与服装色彩准确，轮廓和材质边缘清晰，无场景叙事元素干扰。")
	return joinPromptSections(sections)
}

// variantRuneSet = {A,B,C,D,E}（legacy variant in {...} 判定）。
var variantRuneSet = map[string]bool{"A": true, "B": true, "C": true, "D": true, "E": true}

// assetCharacterIdentityPrompt 对齐 _asset_character_identity_prompt。
func assetCharacterIdentityPrompt(asset *model.Assets, source, metadata map[string]any) string {
	var sourceMetadata map[string]any
	if m := metadataFieldMap(source, "metadata"); m != nil {
		sourceMetadata = m
	}
	costumeDesign := metadataFieldMap(metadata, "costume_design")
	if costumeDesign == nil && sourceMetadata != nil {
		costumeDesign = metadataFieldMap(sourceMetadata, "costume_design")
	}
	var costumeMaterial any
	if costumeDesign != nil {
		costumeMaterial = costumeDesign["material_layers"]
	}
	parts := []string{
		asset.Name + "角色定装",
		promptTextFirstAny(source["identity_setting"], metadata["identity_setting"], metadata["dramatic_function"]),
		promptTextFirstAny(source["visual_features"], metadata["visual_features"], metadata["appearance_clues"]),
		strAny(asset.Description, ""),
		promptTextFirstAny(source["material_layers"], costumeMaterial),
		promptTextFirstAny(source["continuity_constraints"], metadata["continuity_constraints"], metadata["continuity_anchors"]),
		promptTextFirstAny(source["forbidden_variations"], metadata["forbidden_variations"]),
		promptTextFirstAny(source["visual_effects"], metadata["visual_effects"]),
	}
	if costumeDesign != nil {
		if designPrompt := strings.TrimSpace(strOf(costumeDesign, "prompt")); designPrompt != "" && !isFusedAssetViewText(designPrompt) {
			parts = append(parts, designPrompt)
		}
	}
	if assetPrompt := strings.TrimSpace(strAny(asset.PromptText, "")); assetPrompt != "" && !isFusedAssetViewText(assetPrompt) {
		parts = append(parts, assetPrompt)
	}
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "None" || part == "null" {
			continue
		}
		out = append(out, part)
	}
	return strings.Join(uniqueStrings(out), "，")
}

// assetAgeStagePrompt 对齐 _asset_age_stage_prompt（保序 dict）。
func assetAgeStagePrompt(stage map[string]any) string {
	if len(stage) == 0 {
		return ""
	}
	return promptValueDict(
		promptKV{"阶段编号", stage["stage_code"]},
		promptKV{"阶段名称", stage["name"]},
		promptKV{"年龄范围", stage["age_range"]},
		promptKV{"时间线", stage["timeline"]},
		promptKV{"面部变化", stage["face_changes"]},
		promptKV{"体型变化", stage["body_changes"]},
		promptKV{"发型与肤质变化", stage["hair_skin_changes"]},
		promptKV{"跨年龄识别锚点", stage["identity_anchors"]},
		promptKV{"禁止变化", stage["forbidden_changes"]},
	)
}

// assetCostumeVariantPrompt 对齐 _asset_costume_variant_prompt（保序 dict）。
func assetCostumeVariantPrompt(costume map[string]any) string {
	if len(costume) == 0 {
		return ""
	}
	return promptValueDict(
		promptKV{"装扮编号", costume["variant_code"]},
		promptKV{"装扮名称", costume["name"]},
		promptKV{"装扮说明", costume["description"]},
		promptKV{"服饰道具", firstAny(costume["costume_prop_snapshot"], costume["costume_prop_names"])},
		promptKV{"材质分层", costume["material_layers"]},
	)
}

// assetViewPrompt 对齐 _asset_view_prompt。
func assetViewPrompt(source, metadata map[string]any, variant string, stage, costume map[string]any) map[string]any {
	var sourceMetadata map[string]any
	if m := metadataFieldMap(source, "metadata"); m != nil {
		sourceMetadata = m
	}
	baseCostume := metadataFieldMap(metadata, "costume_design")
	if baseCostume == nil && sourceMetadata != nil {
		baseCostume = metadataFieldMap(sourceMetadata, "costume_design")
	}
	var scopedCostume map[string]any
	if len(costume) > 0 && (costume["variant_code"] != nil || costume["costume_prop_snapshot"] != nil) {
		scopedCostume = costume
	}
	var raw any
	if len(scopedCostume) > 0 {
		raw = firstAny(scopedCostume["view_prompts"], stage["view_prompts"], baseCostume["view_prompts"], source["view_prompts"], metadata["view_prompts"])
	} else {
		raw = firstAny(stage["view_prompts"], baseCostume["view_prompts"], source["view_prompts"], metadata["view_prompts"])
	}
	if raw == nil {
		characterAPose := metadataFieldMap(baseCostume, "character_apose")
		if characterAPose != nil {
			raw = listOfMaps(characterAPose["angles"])
		}
	}
	var sourceView map[string]any
	if rawMap, ok := raw.(map[string]any); ok {
		sourceView, _ = rawMap[variant].(map[string]any)
	} else if rawSlice, ok := raw.([]map[string]any); ok {
		upper := strings.ToUpper(variant)
		for _, item := range rawSlice {
			code := strings.ToUpper(strings.TrimSpace(strOf(item, "code")))
			if code == "" {
				code = strings.ToUpper(strings.TrimSpace(strOf(item, "view_code")))
			}
			if code == upper {
				sourceView = item
				break
			}
		}
	} else if rawSlice, ok := raw.([]any); ok {
		upper := strings.ToUpper(variant)
		for _, item := range rawSlice {
			dict, isMap := item.(map[string]any)
			if !isMap {
				continue
			}
			code := strings.ToUpper(strings.TrimSpace(strOf(dict, "code")))
			if code == "" {
				code = strings.ToUpper(strings.TrimSpace(strOf(dict, "view_code")))
			}
			if code == upper {
				sourceView = dict
				break
			}
		}
	}
	upper := strings.ToUpper(variant)
	definition, defined := assetViewDefinitions[upper]
	if !defined && len(sourceView) == 0 {
		return nil
	}
	title := definition[0]
	if !defined {
		title = variant
	}
	defaultDescription := definition[1]
	description := strings.TrimSpace(strOf(sourceView, "description"))
	if description == "" {
		description = defaultDescription
	}
	pose := assetViewPose(variant, sourceView["pose"])
	prompt := strings.TrimSpace(strOf(sourceView, "prompt"))
	if isFusedAssetViewText(prompt) {
		prompt = ""
	}
	if !isProductionReadyAssetViewPrompt(prompt) {
		segments := []string{}
		if prompt != "" {
			segments = append(segments, prompt)
		}
		segments = append(segments, "图位 "+upper+"："+description)
		if pose != "" {
			segments = append(segments, "姿态要求："+pose)
		}
		prompt = strings.Join(segments, "\n")
	}
	negativePrompt := strings.TrimSpace(strOf(sourceView, "negative_prompt"))
	if negativePrompt == "" && baseCostume != nil {
		negativePrompt = strings.TrimSpace(strOf(baseCostume, "negative_prompt"))
	}
	if negativePrompt == "" {
		negativePrompt = strings.TrimSpace(strOf(source, "negative_prompt"))
	}
	if negativePrompt == "" {
		negativePrompt = strings.TrimSpace(strOf(metadata, "negative_prompt"))
	}
	if negativePrompt == "" {
		negativePrompt = "避免改变角色身份、年龄、骨相、五官、体型、发型、服装结构和材质；避免错误肢体、手指异常、重复身体、裁切、遮挡、透视畸变、模糊、低清晰度、文字、水印和标识。"
	}
	sourceType := strOf(sourceView, "source_type")
	if sourceType == "" {
		sourceType = "backend_normalized"
	}
	ref := strings.TrimSpace(strOf(sourceView, "reference_requirement"))
	if ref == "" {
		ref = assetViewReferenceRequirement(variant)
	}
	viewTitle := strings.TrimSpace(strAny(firstAny(sourceView["title"], sourceView["angle"]), title))
	return map[string]any{
		"code":                  upper,
		"title":                 viewTitle,
		"prompt":                prompt,
		"negative_prompt":       negativePrompt,
		"pose":                  pose,
		"description":           description,
		"reference_requirement": ref,
		"source_type":           sourceType,
	}
}

// assetViewReferenceRequirement 对齐 _asset_view_reference_requirement。
func assetViewReferenceRequirement(variant string) string {
	if strings.ToUpper(variant) == "A" {
		return "无前置参考图；输出将作为 B-E 的主参考图。"
	}
	return "必须使用已审核通过的图 A，保持角色身份、骨相、发型、体型和服装一致。"
}

// isFusedAssetViewText 对齐 _is_fused_asset_view_text。
func isFusedAssetViewText(value string) bool {
	markerHits := 0
	for _, marker := range assetViewFusionMarkers {
		if strings.Contains(value, marker) {
			markerHits++
		}
	}
	codeHits := 0
	for _, code := range assetViewFusionCodes {
		if strings.Contains(value, code) {
			codeHits++
		}
	}
	return markerHits >= 3 || codeHits >= 2
}

// hasForeignAssetViewSections 对齐 _has_foreign_asset_view_sections。
func hasForeignAssetViewSections(value, variant string) bool {
	current := strings.ToUpper(variant)
	for _, match := range foreignViewCodeRe.FindAllStringSubmatch(value, -1) {
		if len(match) >= 2 && strings.ToUpper(match[1]) != current {
			return true
		}
	}
	return false
}

// isProductionReadyAssetViewPrompt 对齐 _is_production_ready_asset_view_prompt（去空白 ≥80 字符）。
func isProductionReadyAssetViewPrompt(value string) bool {
	return len(strings.Join(strings.Fields(value), "")) >= 80
}

// assetViewPose 对齐 _asset_view_pose。
func assetViewPose(variant string, value any) string {
	pose := strings.TrimSpace(strAny(value, ""))
	markerHits := 0
	for _, marker := range assetViewFusionMarkers {
		if strings.Contains(pose, marker) {
			markerHits++
		}
	}
	codeHits := 0
	for _, code := range []string{"图位 A", "图位 B", "图位 C", "图位 D", "图位 E"} {
		if strings.Contains(pose, code) {
			codeHits++
		}
	}
	if pose == "" || markerHits >= 3 || codeHits >= 2 {
		if definition, ok := assetViewPoses[strings.ToUpper(variant)]; ok {
			return definition
		}
		return pose
	}
	return pose
}

// ---- 通用小工具 ----

// joinPromptSections 用空行连接非空 prompt 段落。
func joinPromptSections(parts []string) string {
	var out []string
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "\n\n")
}

func uniqueStrings(parts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range parts {
		if seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

// orDefault 对齐 Python `value or fallback`；strOf 在 repository/tasks_materialize.go 定义。
func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// promptValueTextAny 对齐 _prompt_value_text，但 nil → ""（Go 的 %v(nil) 是 <nil>，P3f 不渲染）。
func promptValueTextAny(value any) string {
	if value == nil {
		return ""
	}
	return promptValueText(value)
}

// promptTextFirstAny 对齐 `_prompt_value_text(a or b or c)`（nil 安全）。
func promptTextFirstAny(values ...any) string {
	return promptValueTextAny(firstAny(values...))
}

// promptKV 保序键值对，用于对齐 legacy dict 的插入序。
type promptKV struct {
	key   string
	value any
}

// promptValueDict 对齐 `_prompt_value_text({...dict...})`：保序且跳过 None/""/[]/{}。
func promptValueDict(pairs ...promptKV) string {
	var parts []string
	for _, kv := range pairs {
		text := promptValueTextAny(kv.value)
		if text == "" || text == "None" || text == "null" {
			continue
		}
		parts = append(parts, kv.key+"："+text)
	}
	return strings.Join(parts, "；")
}

// firstAny 对齐 Python `a or b or c`（跳过 nil / 空串 / 空 map / 空 slice）。
func firstAny(values ...any) any {
	for _, v := range values {
		if v == nil {
			continue
		}
		switch typed := v.(type) {
		case string:
			if typed == "" {
				continue
			}
		case map[string]any:
			if len(typed) == 0 {
				continue
			}
		case []any:
			if len(typed) == 0 {
				continue
			}
		case []map[string]any:
			if len(typed) == 0 {
				continue
			}
		}
		return v
	}
	return nil
}

// assetPriorityRow 对齐 _asset_priority（Asset 行 + metadata 优先级来源链）。
func assetPriorityRow(asset *model.Assets) string {
	metadata := assetMetadata(asset)
	source := assetBreakdownSource(metadata)
	var sourceMetadata map[string]any
	if m := metadataFieldMap(source, "metadata"); m != nil {
		sourceMetadata = m
	}
	reading := metadataFieldMap(metadata, "reading_candidate")
	priority := orDefault(strOf(metadata, "priority"),
		orDefault(strOf(source, "priority"),
			orDefault(strOf(sourceMetadata, "priority"),
				orDefault(strOf(reading, "priority"), "C"))))
	priority = strings.ToUpper(strings.TrimSpace(priority))
	switch priority {
	case "S", "A", "B", "C":
		return priority
	}
	return "C"
}

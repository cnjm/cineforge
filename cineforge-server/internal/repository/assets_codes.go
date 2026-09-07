package repository

// P3f-c 资产域：显示编码 / 规范文件名 / 资产码分配助手（对齐 legacy repositories.py
// 的 _library_* / canonical_* / asset_artifact_code / _next_formal_asset_code 等字符串函数）。
// 纯函数，无 IO；复用 tasks_materialize.go 已有的 libraryCode / strAny / safeInt /
// characterViewCode / assetContextVersion。

import (
	"fmt"
	"regexp"
	"strings"

	"cineforge/server/internal/model"
)

// ---- 资产状态 / 类型常量 ----

// inactiveAssetStatuses 已在 tasks.go 定义（inactiveTaskAssetStatuses）。

func isInactiveAssetStatus(status string) bool { return inactiveTaskAssetStatuses[status] }

// approvedSubmissionStatuses 已在 tasks.go 定义（对齐 APPROVED_SUBMISSION_STATUSES）。

// visualAssetTypes 对齐 _VISUAL_ASSET_TYPES（POST /assets 仅支持可视资产）。
var visualAssetTypes = map[string]bool{
	assetTypeCharacter: true,
	assetTypeScene:     true,
	assetTypeProp:      true,
}

// assetTypeDisplayCodes 已在 tasks_constants.go 定义（对齐 ASSET_TYPE_DISPLAY_CODES）。

var (
	roleAssetCodeRe       = regexp.MustCompile(`^(?:CHAR|ROLE|R)?0*(\d+)$`)
	sceneAssetCodeRe      = regexp.MustCompile(`^(?:SC|C|S)?0*(\d+)$`)
	propAssetCodeRe       = regexp.MustCompile(`^(?:PROP|P)?0*(\d+)$`)
	ageStageCodeRe        = regexp.MustCompile(`^(?:AGE)?0*(\d+)$`)
	costumeCodeRe         = regexp.MustCompile(`^(?:COSTUME)?(?:V)?0*(\d+)$`)
	scriptSegmentCodeRe   = regexp.MustCompile(`^(?:J)?0*(\d+)$`)
	mirrorShotCodeRe      = regexp.MustCompile(`^(?:MIR|MIRROR)-?([A-Z0-9]+)$`)
	voiceRoleSearchRe     = regexp.MustCompile(`(?:^|-)(R\d+)(?:-|$)`)
	episodeSegmentRe      = regexp.MustCompile(`(EP\d{2,})`)
	formalAssetCodeTrail = regexp.MustCompile(`^[A-Z0-9]+-(R|SC|P)(\d+)$`)
	businessTrailRe      = regexp.MustCompile(`(?:R|SC|P)\d+`)
	shortPrefixRe         = regexp.MustCompile(`^[A-Z0-9]{1,5}$`)
	mirrorShotScanRe      = regexp.MustCompile(`(?:^|-)(MIR(?:ROR)?-?[A-Z0-9]+)(?:-|$)`)
)

// assetLibraryOutputType 对齐 _asset_library_output_type。
func assetLibraryOutputType(assetType string) string {
	if assetType == assetTypeMusic || assetType == assetTypeVoiceProfile {
		return "audio"
	}
	return "asset_master"
}

// normalizeAssetType 已在 projects_admin.go 定义（对齐 legacy _normalize_asset_type）。

// safeCodePrefix 对齐 _safe_code_prefix：去除非 [A-Z0-9]，空则 "RF"。
func safeCodePrefix(value any) string {
	prefix := librarySepRegexp.ReplaceAllString(strings.ToUpper(strAny(value, "")), "")
	if prefix == "" {
		return "RF"
	}
	return prefix
}

// libraryLeafCode 对齐 _library_leaf_code（父编码中 "-" 分隔的末段）。
func libraryLeafCode(value *string, fallback string) string {
	code := libraryCode(strAny(value, fallback), fallback)
	idx := strings.LastIndex(code, "-")
	if idx < 0 {
		if code == "" {
			return fallback
		}
		return code
	}
	if leaf := code[idx+1:]; leaf != "" {
		return leaf
	}
	return fallback
}

// libraryExtension 对齐 _library_extension。
func libraryExtension(value *string, fallback string) string {
	name := strings.Split(strAny(value, ""), "?")[0]
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	var extension string
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		extension = strings.ToLower(name[dot+1:])
	} else {
		extension = strings.ToLower(strings.TrimLeft(name, "."))
	}
	extension = librarySepRegexp.ReplaceAllString(extension, "")
	if extension == "" {
		return fallback
	}
	return extension
}

// resolveMusicAudioType 对齐 _resolve_music_audio_type。
func resolveMusicAudioType(audioType *string, assetCode *string) string {
	normalized := strings.ToLower(strings.TrimSpace(strAny(audioType, "")))
	switch normalized {
	case "theme_music", "theme":
		return "theme_music"
	case "background_music", "back_music", "background", "back":
		return "back_music"
	}
	if strings.Contains(libraryCode(strAny(assetCode, ""), ""), "THEME") {
		return "theme_music"
	}
	return "back_music"
}

// episodeCodeFromAssetCode 对齐 _episode_code_from_asset_code。
func episodeCodeFromAssetCode(assetCode *string) *string {
	match := episodeSegmentRe.FindString(libraryCode(strAny(assetCode, ""), ""))
	if match == "" {
		return nil
	}
	return &match
}

// roleAssetCode 对齐 _role_asset_code。
func roleAssetCode(value *string) string {
	code := libraryLeafCode(value, "R000")
	if m := roleAssetCodeRe.FindStringSubmatch(code); m != nil {
		return fmt.Sprintf("R%03d", parseIntDigits(m[1]))
	}
	return code
}

// voiceRoleCode 对齐 _voice_role_code。
func voiceRoleCode(value *string) string {
	code := libraryCode(strAny(value, ""), "")
	match := voiceRoleSearchRe.FindStringSubmatch(code)
	if match == nil {
		return roleAssetCode(value)
	}
	role := match[1]
	return roleAssetCode(&role)
}

// sceneAssetCode 对齐 _scene_asset_code。
func sceneAssetCode(value *string) string {
	code := libraryLeafCode(value, "SC000")
	if m := sceneAssetCodeRe.FindStringSubmatch(code); m != nil {
		return fmt.Sprintf("SC%03d", parseIntDigits(m[1]))
	}
	return code
}

// propAssetCode 对齐 _prop_asset_code。
func propAssetCode(value *string) string {
	code := libraryLeafCode(value, "P000")
	if m := propAssetCodeRe.FindStringSubmatch(code); m != nil {
		return fmt.Sprintf("P%03d", parseIntDigits(m[1]))
	}
	return code
}

// ageStageDisplayCode 对齐 _age_stage_display_code。
func ageStageDisplayCode(value *string) *string {
	if strings.TrimSpace(strAny(value, "")) == "" {
		return nil
	}
	code := libraryCode(strAny(value, "AGE00"), "AGE00")
	if code == "BASE" {
		return nil
	}
	cleaned := strings.ReplaceAll(code, "-", "")
	if m := ageStageCodeRe.FindStringSubmatch(cleaned); m != nil {
		out := fmt.Sprintf("AGE%02d", parseIntDigits(m[1]))
		return &out
	}
	out := code
	return &out
}

// costumeDisplayCode 对齐 _costume_display_code。
func costumeDisplayCode(value *string) *string {
	if strings.TrimSpace(strAny(value, "")) == "" {
		return nil
	}
	code := libraryCode(strAny(value, "V00"), "V00")
	if code == "BASE" {
		return nil
	}
	cleaned := strings.ReplaceAll(code, "-", "")
	if m := costumeCodeRe.FindStringSubmatch(cleaned); m != nil {
		out := fmt.Sprintf("V%02d", parseIntDigits(m[1]))
		return &out
	}
	out := code
	return &out
}

// scriptSegmentDisplayCode 对齐 _script_segment_display_code。
func scriptSegmentDisplayCode(value *string) string {
	code := libraryLeafCode(value, "J000")
	if m := scriptSegmentCodeRe.FindStringSubmatch(code); m != nil {
		return fmt.Sprintf("J%03d", parseIntDigits(m[1]))
	}
	return code
}

// mirrorShotDisplayCode 对齐 _mirror_shot_display_code。
func mirrorShotDisplayCode(value *string) *string {
	if strings.TrimSpace(strAny(value, "")) == "" {
		return nil
	}
	code := libraryCode(strAny(value, ""), "")
	if m := mirrorShotCodeRe.FindStringSubmatch(code); m != nil {
		out := "MIR-" + m[1]
		return &out
	}
	if code == "" {
		return nil
	}
	out := "MIR-" + code
	return &out
}

// audioArtifactCode 对齐 audio_artifact_code。
func audioArtifactCode(projectPrefix *string, audioType string, roleCode, contextCode, episodeCode *string) string {
	prefix := libraryCode(strAny(projectPrefix, ""), "RF")
	normalizedType := strings.ReplaceAll(libraryCode(audioType, "AUDIO"), "_", "-")
	upper := strings.ToUpper(normalizedType)
	switch upper {
	case "THEME", "THEME-MUSIC":
		return prefix + "-THEME-MUSIC"
	case "BACK", "BACK-MUSIC", "BACKGROUND", "BACKGROUND-MUSIC":
		if episodeCode != nil && strings.TrimSpace(*episodeCode) != "" {
			episode := libraryCode(*episodeCode, "")
			if episode != "" {
				return prefix + "-" + episode + "-BACK-MUSIC"
			}
		}
		return prefix + "-BACK-MUSIC"
	case "VOICE", "ROLE-VOICE", "CHARACTER-VOICE":
		role := roleAssetCode(roleCode)
		context := libraryCode(strAny(contextCode, ""), "BASE")
		return fmt.Sprintf("%s-AUDIO-VOICE-%s-%s", prefix, role, context)
	}
	return prefix + "-AUDIO-" + normalizedType
}

// canonicalAudioDisplayName 对齐 canonical_audio_display_name。
func canonicalAudioDisplayName(projectPrefix *string, audioType string, versionNo int32, extension string,
	roleCode, contextCode *string, alternateNo int32, episodeCode *string) string {
	artifactCode := audioArtifactCode(projectPrefix, audioType, roleCode, contextCode, episodeCode)
	alternate := ""
	if alternateNo > 0 {
		alternate = fmt.Sprintf("-ALT%02d", alternateNo)
	}
	displayVersion := versionNo
	if displayVersion < 1 {
		displayVersion = 1
	}
	return fmt.Sprintf("%s-V%03d%s.%s", artifactCode, displayVersion, alternate, libraryExtension(orEmpty(extension), "bin"))
}

// assetArtifactCode 对齐 asset_artifact_code。
func assetArtifactCode(projectPrefix *string, assetType string, assetCode *string, audioType, episodeCode *string) string {
	prefix := libraryCode(strAny(projectPrefix, ""), "RF")
	switch assetType {
	case assetTypeCharacter:
		return prefix + "-" + roleAssetCode(assetCode)
	case assetTypeScene:
		return fmt.Sprintf("%s-%s-MASTER", prefix, sceneAssetCode(assetCode))
	case assetTypeProp:
		return fmt.Sprintf("%s-%s-MASTER", prefix, propAssetCode(assetCode))
	case assetTypeMusic:
		resolved := resolveMusicAudioType(audioType, assetCode)
		ep := episodeCode
		if ep == nil {
			ep = episodeCodeFromAssetCode(assetCode)
		}
		return audioArtifactCode(projectPrefix, resolved, nil, nil, ep)
	case assetTypeVoiceProfile:
		return audioArtifactCode(projectPrefix, "voice", orEmpty(voiceRoleCode(assetCode)), &constantBaseCode, nil)
	}
	typeCode := assetTypeDisplayCodes[assetType]
	if typeCode == "" {
		typeCode = "ASSET"
	}
	return fmt.Sprintf("%s-%s-%s-MASTER", prefix, typeCode, libraryLeafCode(assetCode, "ASSET"))
}

var constantBaseCode = "BASE"

// canonicalAssetDisplayName 对齐 canonical_asset_display_name。
func canonicalAssetDisplayName(projectPrefix *string, assetType string, assetCode *string, versionNo int32,
	extension string, taskVariant *string, ageStageCode, costumeVariantCode *string, contextVersionNo int32,
	alternateNo int32, audioType, episodeCode *string) string {
	displayVersionNo := versionNo
	if contextVersionNo > 0 && contextVersionNo > displayVersionNo {
		displayVersionNo = contextVersionNo
	} else if displayVersionNo < 1 {
		displayVersionNo = 1
	}
	alternate := ""
	if alternateNo > 0 {
		alternate = fmt.Sprintf("-ALT%02d", alternateNo)
	}
	if assetType == assetTypeCharacter {
		prefix := libraryCode(strAny(projectPrefix, ""), "RF")
		code := roleAssetCode(assetCode)
		var contextParts []string
		if age := ageStageDisplayCode(ageStageCode); age != nil {
			contextParts = append(contextParts, *age)
		}
		if costume := costumeDisplayCode(costumeVariantCode); costume != nil {
			contextParts = append(contextParts, "COSTUME", *costume)
		}
		view := characterViewCode(strAny(taskVariant, ""))
		context := ""
		if len(contextParts) > 0 {
			context = "-" + strings.Join(contextParts, "-")
		}
		return fmt.Sprintf("%s-%s%s-VIEW-%s-V%03d%s.%s",
			prefix, code, context, view, displayVersionNo, alternate, libraryExtension(orEmpty(extension), "bin"))
	}
	if assetType == assetTypeMusic {
		resolved := resolveMusicAudioType(audioType, assetCode)
		ep := episodeCode
		if ep == nil {
			ep = episodeCodeFromAssetCode(assetCode)
		}
		return canonicalAudioDisplayName(projectPrefix, resolved, displayVersionNo, extension, nil, nil, alternateNo, ep)
	}
	if assetType == assetTypeVoiceProfile {
		return canonicalAudioDisplayName(projectPrefix, "voice", displayVersionNo, extension,
			orEmpty(voiceRoleCode(assetCode)), &constantBaseCode, alternateNo, nil)
	}
	artifactCode := assetArtifactCode(projectPrefix, assetType, assetCode, audioType, episodeCode)
	return fmt.Sprintf("%s-V%03d%s.%s", artifactCode, displayVersionNo, alternate, libraryExtension(orEmpty(extension), "bin"))
}

// storyboardOutputArtifactCode 对齐 storyboard_output_artifact_code。
func storyboardOutputArtifactCode(projectPrefix *string, episodeCode, sceneCode, storyboardCode *string,
	outputType string, scriptSegmentCode, mirrorShotCode *string) string {
	parts := []string{
		libraryCode(strAny(projectPrefix, ""), "RF"),
		libraryCode(strAny(episodeCode, ""), "EP00"),
	}
	if sceneCode != nil && strings.TrimSpace(*sceneCode) != "" {
		parts = append(parts, libraryLeafCode(sceneCode, "SCENE"))
	}
	parts = append(parts,
		scriptSegmentDisplayCode(scriptSegmentCode),
		libraryLeafCode(storyboardCode, "F000"),
	)
	if normalizedMirror := mirrorShotDisplayCode(mirrorShotCode); normalizedMirror != nil {
		parts = append(parts, *normalizedMirror)
	}
	if outputType == "video" {
		parts = append(parts, "VIDEO")
	} else if normalizedMirror := mirrorShotDisplayCode(mirrorShotCode); normalizedMirror != nil {
		parts = append(parts, "KEYFRAME")
	} else {
		parts = append(parts, "KEYFRAME", "MASTER")
	}
	return strings.Join(parts, "-")
}

// canonicalStoryboardOutputDisplayName 对齐 canonical_storyboard_output_display_name。
func canonicalStoryboardOutputDisplayName(projectPrefix *string, episodeCode, sceneCode, storyboardCode *string,
	outputType string, versionNo int32, extension string, scriptSegmentCode, mirrorShotCode *string) string {
	artifactCode := storyboardOutputArtifactCode(projectPrefix, episodeCode, sceneCode, storyboardCode,
		outputType, scriptSegmentCode, mirrorShotCode)
	displayVersion := versionNo
	if displayVersion < 1 {
		displayVersion = 1
	}
	return fmt.Sprintf("%s-V%03d.%s", artifactCode, displayVersion, libraryExtension(orEmpty(extension), "bin"))
}

// ---- 资产正式码分配（create_asset / temporary-production） ----

// canonicalFormalAssetCode 对齐 _canonical_formal_asset_code。
func canonicalFormalAssetCode(value string, prefix string, assetType string) *string {
	if value == "" {
		return nil
	}
	pattern := "^" + prefix + "-(R|SC|S|P)(\\d+)$"
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}
	match := re.FindStringSubmatch(strings.TrimSpace(strings.ToUpper(value)))
	if match == nil {
		return nil
	}
	normalizedType := "character"
	switch match[1] {
	case "SC", "S":
		normalizedType = "scene"
	case "P":
		normalizedType = "prop"
	}
	if assetType != "" && normalizeAssetType(assetType) != normalizedType {
		return nil
	}
	canonicalPrefix := "R"
	switch normalizedType {
	case "scene":
		canonicalPrefix = "SC"
	case "prop":
		canonicalPrefix = "P"
	}
	out := fmt.Sprintf("%s-%s%03d", prefix, canonicalPrefix, parseIntDigits(match[2]))
	return &out
}

// formalAssetCodeParts 对齐 _formal_asset_code_parts。
func formalAssetCodeParts(value string, prefix string) (string, int) {
	code := canonicalFormalAssetCode(value, prefix, "")
	if code == nil {
		return "", 0
	}
	match := formalAssetCodeTrail.FindStringSubmatch(*code)
	if match == nil {
		return "", 0
	}
	assetType := "character"
	switch match[1] {
	case "SC":
		assetType = "scene"
	case "P":
		assetType = "prop"
	}
	return assetType, parseIntDigits(match[2])
}

// parseIntDigits 提取数字字符串为 int（前导零去除；非法返回 0）。
func parseIntDigits(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// nextFormalAssetCode 对齐 _next_formal_asset_code（非可视资产 → error）。
func nextFormalAssetCode(projectPrefix string, assetType string, reservedCodes []string) (string, error) {
	if !visualAssetTypes[assetType] {
		return "", fmt.Errorf("Unsupported visual asset type: %s", assetType)
	}
	prefix := safeCodePrefix(projectPrefix)
	codePrefix := "R"
	switch assetType {
	case assetTypeScene:
		codePrefix = "SC"
	case assetTypeProp:
		codePrefix = "P"
	}
	occupied := map[string]bool{}
	for _, code := range reservedCodes {
		trimmed := strings.TrimSpace(strings.ToUpper(code))
		if trimmed != "" {
			occupied[trimmed] = true
		}
	}
	sequence := 0
	for code := range occupied {
		existingType, existingSequence := formalAssetCodeParts(code, prefix)
		if existingType == assetType && existingSequence > sequence {
			sequence = existingSequence
		}
	}
	for {
		sequence++
		candidate := fmt.Sprintf("%s-%s%03d", prefix, codePrefix, sequence)
		if !occupied[candidate] {
			return candidate, nil
		}
	}
}

// assetBusinessCode 对齐 _asset_business_code。
func assetBusinessCode(value *string) string {
	text := strings.ToUpper(strings.TrimSpace(strAny(value, "")))
	parts := strings.SplitN(text, "-", 2)
	if len(parts) == 2 && shortPrefixRe.MatchString(parts[0]) && businessTrailRe.MatchString(parts[1]) {
		return parts[1]
	}
	return text
}

// variantCodeNumber 对齐 _variant_code_number。
func variantCodeNumber(value *string) int32 {
	text := strings.TrimSpace(strAny(value, ""))
	idx := strings.LastIndexAny(text, "0123456789")
	if idx < 0 {
		return 0
	}
	start := idx
	for start > 0 && text[start-1] >= '0' && text[start-1] <= '9' {
		start--
	}
	return int32(parseIntDigits(text[start:]))
}

// assetConfirmationStatus 对齐 _asset_confirmation_status。
func assetConfirmationStatus(asset *model.Assets) string {
	metadata := metadataAsMap(asset.MetadataJson)
	raw := strAny(metadata["confirmation_status"], asset.Status)
	if raw == "" {
		raw = "pending_confirmation"
	}
	switch raw {
	case "needs_completion", "prompt_pending", "pending_confirmation", "confirmed":
		return raw
	}
	return "pending_confirmation"
}

// ---- list 元数据摘要（include_metadata=false） ----

// assetListAgeStages 对齐 _asset_list_age_stages。
func assetListAgeStages(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	var stages []any
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		stage := map[string]any{}
		if v, ok := item["stage_code"]; ok && v != nil {
			stage["stage_code"] = v
		}
		if v, ok := item["name"]; ok && v != nil {
			stage["name"] = v
		}
		var variants []any
		for _, vraw := range listOrNil(item["costume_variants"]) {
			variant, ok := vraw.(map[string]any)
			if !ok {
				continue
			}
			v := map[string]any{}
			if code, ok := variant["variant_code"]; ok && code != nil {
				v["variant_code"] = code
			}
			if name, ok := variant["name"]; ok && name != nil {
				v["name"] = name
			}
			variants = append(variants, v)
		}
		if len(variants) > 0 {
			stage["costume_variants"] = variants
		}
		if len(stage) > 0 {
			stages = append(stages, stage)
		}
	}
	if len(stages) == 0 {
		return nil
	}
	return stages
}

// assetListMetadata 对齐 _asset_list_metadata。
func assetListMetadata(metadata map[string]any) map[string]any {
	summary := map[string]any{}
	for key, value := range metadata {
		if key == "age_stages" {
			if stages := assetListAgeStages(value); stages != nil {
				summary[key] = stages
			}
			continue
		}
		switch t := value.(type) {
		case nil:
			summary[key] = nil
		case bool, int, int32, int64, float32, float64:
			summary[key] = t
		case string:
			if len(t) <= 2048 {
				summary[key] = t
			}
		case []any:
			if len(t) <= 50 && allScalarShort(t) {
				summary[key] = t
			}
		}
	}
	return summary
}

func allScalarShort(items []any) bool {
	for _, item := range items {
		switch t := item.(type) {
		case nil:
		case bool, int, int32, int64, float32, float64:
		case string:
			if len(t) > 512 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func listOrNil(v any) []any {
	if items, ok := v.([]any); ok {
		return items
	}
	return nil
}

func orEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// fileLocation 对齐 _file_location。
func fileLocation(f *model.Files, fallback *string) *string {
	if f != nil {
		out := "minio://" + f.Bucket + "/" + f.ObjectKey
		return &out
	}
	return fallback
}

// submissionMirrorShotCode 对齐 _submission_mirror_shot_code。
func submissionMirrorShotCode(taskVariant, step *string) *string {
	for _, value := range []*string{taskVariant, step} {
		text := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(strAny(value, "")), "_", "-"))
		match := mirrorShotScanRe.FindStringSubmatch(text)
		if match != nil {
			return mirrorShotDisplayCode(orEmpty(match[1]))
		}
	}
	return nil
}
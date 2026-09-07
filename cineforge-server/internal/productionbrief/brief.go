package productionbrief

// ProjectProductionBrief.v1 / EpisodeProductionBrief.v1 输入校验。
// 错误文案与 legacy `schemas/production_brief.py` 的 model_validator 逐字对齐。

import (
	"encoding/json"
	"errors"
	"fmt"
)

// LIVE_ACTION_STYLE_ID 对齐 legacy app.agents.production_brief.LIVE_ACTION_STYLE_ID。
const LIVE_ACTION_STYLE_ID = "live_action_cinematic"

var (
	// ErrUnknownStyleId 对齐 legacy "Unknown production style: {id}"。
	ErrUnknownStyleId = errors.New("Unknown production style: unknown")
	// ErrLiveActionStyleMismatch 对齐 legacy "live_action_drama must use live_action_cinematic"。
	ErrLiveActionStyleMismatch = errors.New("live_action_drama must use live_action_cinematic")
	// ErrAnimatedRequiresNonLive 对齐 legacy "animated and hybrid projects must select a non-live-action style"。
	ErrAnimatedRequiresNonLive = errors.New("animated and hybrid projects must select a non-live-action style")
)

// CulturalContextBrief 对应 CulturalContextBrief schema。
type CulturalContextBrief struct {
	ContextCode      string  `json:"context_code"`
	Name             string  `json:"name"`
	Region           string  `json:"region"`
	Era              string  `json:"era"`
	WorldType        string  `json:"world_type"`
	Timeline         string  `json:"timeline"`
	NarrativeGrammar *string `json:"narrative_grammar"`
}

// DefaultTimeline 对齐 legacy CulturalContextBrief.timeline 默认值。
const DefaultTimeline = "待围读识别"

// ProjectBrief 对应 ProjectProductionBrief schema；校验后模型字段带默认值补全。
type ProjectBrief struct {
	SchemaVersion              string                 `json:"schema_version"`
	ContentType                string                 `json:"content_type"`
	DeliveryAspectRatio        string                 `json:"delivery_aspect_ratio"`
	PrimaryStyleID             string                 `json:"primary_style_id"`
	StyleCatalogVersion        string                 `json:"style_catalog_version"`
	CulturalContexts           []CulturalContextBrief `json:"cultural_contexts"`
	PrimaryCulturalContextCode string                 `json:"primary_cultural_context_code"`
	FieldSources               map[string]string      `json:"field_sources"`
}

// EpisodeBrief 对应 EpisodeProductionBrief schema。
type EpisodeBrief struct {
	SchemaVersion         string            `json:"schema_version"`
	TargetDurationSeconds int               `json:"target_duration_seconds"`
	FieldSources          map[string]string `json:"field_sources"`
}

// OptionalString 便于 JSON 反序列化时区分“缺省”（nil）与“显式 null”。
type OptionalString = *string

// ParseProjectBrief 反序列化并做契约校验；style_catalog_version 缺省时回填 catalog 版本。
// 返回的错误 message 与 legacy 校准，供 handler 组装 `production_brief: {err}`。
func ParseProjectBrief(raw []byte) (*ProjectBrief, error) {
	initCatalogs()
	var in struct {
		SchemaVersion              *string                `json:"schema_version"`
		ContentType                *string                `json:"content_type"`
		DeliveryAspectRatio        *string                `json:"delivery_aspect_ratio"`
		PrimaryStyleID             *string                `json:"primary_style_id"`
		StyleCatalogVersion        *string                `json:"style_catalog_version"`
		CulturalContexts           []CulturalContextBrief `json:"cultural_contexts"`
		PrimaryCulturalContextCode *string                `json:"primary_cultural_context_code"`
		FieldSources               map[string]string      `json:"field_sources"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("JSON object 解析失败: %v", err)
	}
	b := &ProjectBrief{
		SchemaVersion:              "ProjectProductionBrief.v1",
		ContentType:                strOr(in.ContentType, ""),
		DeliveryAspectRatio:        strOr(in.DeliveryAspectRatio, ""),
		PrimaryStyleID:             strOr(in.PrimaryStyleID, ""),
		StyleCatalogVersion:        strOr(in.StyleCatalogVersion, _styleCatalog.CatalogVersion),
		CulturalContexts:           in.CulturalContexts,
		PrimaryCulturalContextCode: strOr(in.PrimaryCulturalContextCode, ""),
		FieldSources:               in.FieldSources,
	}
	if b.FieldSources == nil {
		b.FieldSources = map[string]string{}
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return b, nil
}

// Validate 执行领域校验：唯一 code、主语境引用、风格 catalog 与内容类型约束。
func (b *ProjectBrief) Validate() error {
	if len(b.CulturalContexts) < 1 {
		return errors.New("cultural_contexts 至少需要 1 个语境")
	}
	codes := make(map[string]bool, len(b.CulturalContexts))
	for _, cc := range b.CulturalContexts {
		codes[cc.ContextCode] = true
	}
	if len(codes) != len(b.CulturalContexts) {
		return errors.New("cultural_contexts must use unique context_code values")
	}
	if !codes[b.PrimaryCulturalContextCode] {
		return errors.New("primary_cultural_context_code must reference cultural_contexts")
	}
	style := GetPrimaryStyle(b.PrimaryStyleID)
	if style == nil {
		return fmt.Errorf("Unknown production style: %s", b.PrimaryStyleID)
	}
	isLive := style.Medium == "live_action"
	if b.ContentType == "live_action_drama" && b.PrimaryStyleID != LIVE_ACTION_STYLE_ID {
		return ErrLiveActionStyleMismatch
	}
	if (b.ContentType == "animated_drama" || b.ContentType == "hybrid_drama") && isLive {
		return ErrAnimatedRequiresNonLive
	}
	return nil
}

// ParseEpisodeBrief 反序列化 EpisodeProductionBrief；缺省字段按 schema 默认补。
func ParseEpisodeBrief(raw []byte) (*EpisodeBrief, error) {
	var in struct {
		SchemaVersion         *string           `json:"schema_version"`
		TargetDurationSeconds *int              `json:"target_duration_seconds"`
		FieldSources          map[string]string `json:"field_sources"`
	}
	// 缺省 schema_version 也可接受（legacy name 校验），保持容错。
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("JSON object 解析失败: %v", err)
	}
	b := &EpisodeBrief{
		SchemaVersion:         "EpisodeProductionBrief.v1",
		TargetDurationSeconds: intOr(in.TargetDurationSeconds, 120),
		FieldSources:          in.FieldSources,
	}
	if b.FieldSources == nil {
		b.FieldSources = map[string]string{}
	}
	if b.TargetDurationSeconds < 1 || b.TargetDurationSeconds > 7200 {
		return nil, fmt.Errorf("target_duration_seconds 必须在 1~7200 之间")
	}
	return b, nil
}

func strOr(v *string, def string) string {
	if v == nil {
		return def
	}
	return *v
}

// ValidateProjectBriefMap 供 import 查询参数走同一契约校验（map 形态）。
func ValidateProjectBriefMap(m map[string]any) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("JSON object 解析失败: %v", err)
	}
	_, err = ParseProjectBrief(raw)
	return err
}

// ValidateEpisodeBriefMap 同上（episode_brief 查询参数）。
func ValidateEpisodeBriefMap(m map[string]any) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("JSON object 解析失败: %v", err)
	}
	_, err = ParseEpisodeBrief(raw)
	return err
}

func intOr(v *int, def int) int {
	if v == nil {
		return def
	}
	return *v
}

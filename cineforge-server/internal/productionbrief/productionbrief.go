package productionbrief

// 正式制作简报（ProjectProductionBrief.v1 / EpisodeProductionBrief.v1）契约与
// 风格/简报 catalog（vendor 自 legacy `backend/app/agents/references/*.json`）。
// 校验规则与 legacy `schemas/production_brief.py` 的 `model_validator` 逐字一致。

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	_ "embed"
)

var (
	//go:embed catalog/production_style_catalog.v1.json
	styleCatalogBytes []byte

	//go:embed catalog/production_brief_catalog.v1.json
	briefCatalogBytes []byte
)

var (
	_styleCatalog StyleCatalog
	_briefCatalog BriefCatalog
	_catalogOnce  sync.Once
)

// PrimaryStyle 对应 production_style_catalog.v1.json 中 primary_styles 元素。
type PrimaryStyle struct {
	StyleID          string         `json:"style_id"`
	Label            string         `json:"label"`
	Medium           string         `json:"medium"`
	Rendering        string         `json:"rendering"`
	RealismLevel     string         `json:"realism_level"`
	Description      string         `json:"description"`
	PromptTokens     []string       `json:"prompt_tokens"`
	NegativeTokens   []string       `json:"negative_tokens"`
	ProviderMappings map[string]any `json:"provider_mappings"`
}

type styleModifier struct {
	ModifierID       string         `json:"modifier_id"`
	Label            string         `json:"label"`
	Group            string         `json:"group"`
	PromptTokens     []string       `json:"prompt_tokens"`
	ProviderMappings map[string]any `json:"provider_mappings,omitempty"`
}

// StyleCatalog 生产风格 catalog（schema production_style_catalog.v1）。
type StyleCatalog struct {
	SchemaVersion   string                       `json:"schema_version"`
	CatalogVersion  string                       `json:"catalog_version"`
	MaxModifiers    int                          `json:"max_modifiers"`
	PrimaryStyles   []PrimaryStyle               `json:"primary_styles"`
	Modifiers       []styleModifier              `json:"modifiers"`
	ProviderPresets map[string]map[string]string `json:"provider_presets"`
}

// BriefCatalog 生产简报 catalog（schema production_brief_catalog.v1）。
type BriefCatalog struct {
	SchemaVersion  string `json:"schema_version"`
	CatalogVersion string `json:"catalog_version"`
	ContentTypes   []struct {
		Value          string `json:"value"`
		Label          string `json:"label"`
		DefaultStyleID any    `json:"default_style_id"`
	} `json:"content_types"`
	DeliveryAspectRatios []struct {
		Value                    string  `json:"value"`
		Label                    string  `json:"label"`
		SuggestedDurationSeconds int     `json:"suggested_duration_seconds"`
		OpeningHookSeconds       int     `json:"opening_hook_seconds"`
		RhythmMultiplier         float64 `json:"rhythm_multiplier"`
	} `json:"delivery_aspect_ratios"`
	RhythmProfiles []struct {
		ProfileID       string   `json:"profile_id"`
		Label           string   `json:"label"`
		BaseShotSeconds int      `json:"base_shot_seconds"`
		CameraRules     []string `json:"camera_rules"`
	} `json:"rhythm_profiles"`
	CulturalContexts []struct {
		ContextCode      string `json:"context_code"`
		Name             string `json:"name"`
		Region           string `json:"region"`
		Era              string `json:"era"`
		WorldType        string `json:"world_type"`
		NarrativeGrammar string `json:"narrative_grammar"`
	} `json:"cultural_contexts"`
}

// initCatalogs 懒加载并校验两份 vendor catalog；失败 panic（开发期缺陷）。
func initCatalogs() {
	_catalogOnce.Do(func() {
		if err := json.Unmarshal(styleCatalogBytes, &_styleCatalog); err != nil {
			panic(fmt.Sprintf("productionbrief: parse style catalog: %v", err))
		}
		if err := json.Unmarshal(briefCatalogBytes, &_briefCatalog); err != nil {
			panic(fmt.Sprintf("productionbrief: parse brief catalog: %v", err))
		}
		if _styleCatalog.SchemaVersion != "production_style_catalog.v1" {
			panic("productionbrief: unsupported production style catalog schema")
		}
		seenPrimary := map[string]bool{}
		for _, s := range _styleCatalog.PrimaryStyles {
			if seenPrimary[s.StyleID] || s.StyleID == "" {
				panic("productionbrief: production style IDs must be non-empty and unique")
			}
			seenPrimary[s.StyleID] = true
		}
	})
}

// StyleCatalogJSON 返回风格 catalog 完整 JSON（供 GET /production-brief/catalog 透传）。
func StyleCatalogJSON() json.RawMessage {
	initCatalogs()
	return json.RawMessage(styleCatalogBytes)
}

// BriefCatalogJSON 返回简报 catalog 完整 JSON。
func BriefCatalogJSON() json.RawMessage {
	initCatalogs()
	return json.RawMessage(briefCatalogBytes)
}

// StyleCatalogVersion 返回风格 catalog 版本号（“2026.07.11”）。
func StyleCatalogVersion() string {
	initCatalogs()
	return _styleCatalog.CatalogVersion
}

// GetPrimaryStyle 按 style_id 返回主风格；未知返回 nil。
func GetPrimaryStyle(styleID string) *PrimaryStyle {
	initCatalogs()
	target := strings.TrimSpace(styleID)
	for i := range _styleCatalog.PrimaryStyles {
		if _styleCatalog.PrimaryStyles[i].StyleID == target {
			cp := _styleCatalog.PrimaryStyles[i]
			return &cp
		}
	}
	return nil
}

// ContentTypes 返回简报 catalog 的 content_type 全集（value 列表）。
func ContentTypes() []map[string]any {
	initCatalogs()
	out := make([]map[string]any, 0, len(_briefCatalog.ContentTypes))
	for _, ct := range _briefCatalog.ContentTypes {
		out = append(out, map[string]any{
			"value":            ct.Value,
			"label":            ct.Label,
			"default_style_id": ct.DefaultStyleID,
		})
	}
	return out
}

// CulturalContexts 返回简报 catalog 的文化语境全集。
func CulturalContexts() []map[string]any {
	initCatalogs()
	out := make([]map[string]any, 0, len(_briefCatalog.CulturalContexts))
	for _, cc := range _briefCatalog.CulturalContexts {
		out = append(out, map[string]any{
			"context_code":      cc.ContextCode,
			"name":              cc.Name,
			"region":            cc.Region,
			"era":               cc.Era,
			"world_type":        cc.WorldType,
			"narrative_grammar": cc.NarrativeGrammar,
		})
	}
	return out
}

import type { AssetNormalizationV1, AssetTypeV1 } from "./types.ts";

export function normalizedTestAsset(
  assetType: AssetTypeV1,
  patch: Partial<AssetNormalizationV1> = {},
): AssetNormalizationV1 {
  const suffix = assetType === "character" ? "R001" : assetType === "scene" ? "SC001" : "P001";
  const attributes = assetType === "character" ? {
    character_type: null, visual_presence: "on_screen" as const, role: null, gender: null, age: null,
    appearance: null, core_requirement: null, personality: null, background: null,
    has_dialogue: false, dialogue_evidence: [], context_codes: [],
  } : assetType === "scene" ? {
    context_code: null, render_mode: null, scene_no: null, interior_exterior: null, time: null,
    location: null, atmosphere: null, narrative_function: null, spatial_zones: [], visual_goal: null,
    key_props: [], key_prop_codes: [], continuity_risks: [], episode_code: null,
  } : {
    level: null, prop_type: null, appearance: null, source: null, status_change: null,
    variant_states: [], usage_scene: null, scene_description: null, context_codes: [],
  };
  return {
    normalization_version: "AssetNormalization.v1",
    client_asset_key: `test-${assetType}-${suffix}`,
    asset_code: `TEST-${suffix}`,
    display_code: suffix,
    display_label: `${suffix}-测试资产`,
    code_state: "formal",
    asset_type: assetType,
    name: "测试资产",
    description: null,
    priority: "C",
    attributes,
    relations: { relationship_text: null, scene_links: [], owner_character: null, storyboard_codes: [], script_segment_codes: [], evidence: [], confidence: "unknown" },
    review_items: [],
    source: { source_kind: "agent", agent_run_id: null, skill_name: "test", raw_group: "assets", raw_index: 0 },
    repairs: [],
    ...patch,
  } as AssetNormalizationV1;
}

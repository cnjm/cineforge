import type { BreakdownDraftAsset } from "./types";

const PROMPT_ASSET_FIELDS: Array<keyof BreakdownDraftAsset> = [
  "prompt", "negative_prompt", "aspect_ratio", "material_layers", "character_apose",
  "view_prompts", "spatial_bible", "material_bible", "state_prompts", "brief_trace",
  "skill", "skill_version", "contract_version", "input_hash", "context_key",
  "context_schema_version", "input_snapshot", "manual_review_items", "generated_output_spec",
  "generated_view_codes", "deferred_view_codes", "generation_strategy",
];

export function mergeSavedAssetsWithLatestPrompts(saved: BreakdownDraftAsset[], latest: BreakdownDraftAsset[]) {
  if (!saved.length) return latest;
  const merged = saved.map((asset) => {
    const promptAsset = latest.find((candidate) => sameDraftAsset(asset, candidate));
    if (!promptAsset) return asset;
    const promptFields: Partial<BreakdownDraftAsset> = {};
    PROMPT_ASSET_FIELDS.forEach((key) => {
      const value = promptAsset[key];
      if (value !== undefined && value !== null && value !== "") {
        (promptFields as Record<string, unknown>)[key] = value;
      }
    });
    const promptMetadata = pickPromptMetadata(promptAsset.metadata);
    const hasCurrentPrompt = hasPromptPayload(promptAsset, promptMetadata);
    const promptChanged = hasCurrentPrompt && !samePromptRevision(asset, promptAsset);
    const nextMetadata: Record<string, unknown> = {
      ...(asset.metadata || {}),
      ...promptMetadata,
      ...(hasCurrentPrompt ? { prompt_validity: "current" } : {}),
      ...(promptChanged ? { confirmation_status: "pending_confirmation" } : {}),
    };
    if (hasCurrentPrompt) {
      delete nextMetadata.prompt_invalidated_at;
      delete nextMetadata.prompt_invalidated_fields;
    }
    return {
      ...asset,
      ...promptFields,
      ...(promptChanged ? { status: "pending_confirmation" as const } : {}),
      metadata: nextMetadata,
    };
  });
  const newAssets = latest.filter((candidate) => !saved.some((asset) => sameDraftAsset(asset, candidate)));
  return [...merged, ...newAssets];
}

export function sameDraftAsset(left: BreakdownDraftAsset, right: BreakdownDraftAsset) {
  if (left.asset_code && right.asset_code) return left.asset_code === right.asset_code;
  if (left.client_asset_key && right.client_asset_key) return left.client_asset_key === right.client_asset_key;
  return (left.asset_type || left.type) === (right.asset_type || right.type) && Boolean(left.name && left.name === right.name);
}

function hasPromptPayload(asset: BreakdownDraftAsset, promptMetadata: Record<string, unknown>) {
  return Boolean(
    asset.prompt
    || asset.input_hash
    || (promptMetadata.prompt_design && typeof promptMetadata.prompt_design === "object")
    || (promptMetadata.prompt_lineage && typeof promptMetadata.prompt_lineage === "object"),
  );
}

function samePromptRevision(saved: BreakdownDraftAsset, latest: BreakdownDraftAsset) {
  const savedHash = String(saved.input_hash || "").trim();
  const latestHash = String(latest.input_hash || "").trim();
  if (savedHash && latestHash) return savedHash === latestHash;
  if (savedHash !== latestHash) return false;
  return stableJson(promptSignature(saved)) === stableJson(promptSignature(latest));
}

function promptSignature(asset: BreakdownDraftAsset) {
  return {
    prompt: asset.prompt || "",
    negative_prompt: asset.negative_prompt || "",
    aspect_ratio: asset.aspect_ratio || "",
    view_prompts: asset.view_prompts || [],
    state_prompts: asset.state_prompts || [],
    spatial_bible: asset.spatial_bible || {},
    material_bible: asset.material_bible || {},
    skill: asset.skill || "",
    skill_version: asset.skill_version || "",
    contract_version: asset.contract_version || "",
    context_key: asset.context_key || "",
  };
}

function pickPromptMetadata(metadata?: Record<string, unknown>) {
  if (!metadata) return {};
  return Object.fromEntries(
    ["prompt_design", "production_context", "production_contexts", "prompt_lineage", "costume_design"]
      .filter((key) => metadata[key] !== undefined)
      .map((key) => [key, metadata[key]]),
  );
}

function stableJson(value: unknown): string {
  return JSON.stringify(stableValue(value));
}

function stableValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableValue);
  if (value && typeof value === "object") {
    const record = value as Record<string, unknown>;
    return Object.fromEntries(Object.keys(record).sort().map((key) => [key, stableValue(record[key])]));
  }
  return value ?? null;
}

import type { BreakdownDraftAsset } from "./types";

export type OwnerCharacterOption = {
  key: string;
  name: string;
  shortCode: string;
  asset: BreakdownDraftAsset;
};

const OWNER_PROP_TYPES = new Set([
  "服饰", "服装", "衣物", "配饰", "饰品", "首饰",
  "costume", "clothing", "apparel", "garment", "accessory", "accessories", "jewelry", "jewellery", "wearable",
]);

export function isOwnerAssignableProp(asset: BreakdownDraftAsset) {
  return asset.asset_type === "prop" && OWNER_PROP_TYPES.has(String(asset.attributes.prop_type || "").trim().toLowerCase());
}

export function currentDraftCharacterOptions(assets: BreakdownDraftAsset[]): OwnerCharacterOption[] {
  const seen = new Set<string>();
  return assets.flatMap((asset) => {
    if (asset.asset_type !== "character") return [];
    if (asset.code_state !== "formal") return [];
    const name = String(asset.name || "").trim();
    if (!name) return [];
    const shortCode = asset.code_state === "formal" && /^R\d{3,}$/.test(asset.display_code) ? asset.display_code : "";
    const key = asset.client_asset_key;
    const dedupeKey = `${shortCode}|${name}`.toUpperCase();
    if (seen.has(dedupeKey)) return [];
    seen.add(dedupeKey);
    return [{ key, name, shortCode, asset }];
  });
}

export function selectedOwnerCharacterKey(asset: BreakdownDraftAsset, options: OwnerCharacterOption[]) {
  const ownerKey = asset.relations.owner_character?.target_asset_key;
  return options.find((option) => option.key === ownerKey)?.key || "";
}

export function ownerCharacterRelation(option?: OwnerCharacterOption) {
  if (!option) return null;
  return {
    target_asset_key: option.asset.client_asset_key,
    target_asset_code: option.asset.asset_code,
    target_display_code: option.asset.display_code,
    target_display_label: option.asset.display_label,
  };
}

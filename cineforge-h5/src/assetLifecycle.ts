import type { BreakdownDraftAsset } from "./types";

export function characterAssetIsNonVisual(asset: BreakdownDraftAsset) {
  return asset.asset_type === "character" && asset.attributes.visual_presence !== "on_screen";
}

export function assetParticipatesInLifecycleGate(asset: BreakdownDraftAsset) {
  return !characterAssetIsNonVisual(asset);
}

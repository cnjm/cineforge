import type { BreakdownDraftAsset } from "./types";

// 与后端 prompt_record_matches_asset 对齐：两边 client_asset_key 都有时按其匹配；
// 否则回退按 asset_code / matched_asset_code 匹配。design 记录历史上不带
// client_asset_key、只带 asset_code，前端此前只认 client_asset_key 导致 0 匹配，
// 生产提示为空且 hasPromptDraft 恒 false，卡住提示词步骤的确认。
export function promptRecordMatchesAsset(record: Record<string, unknown>, asset: BreakdownDraftAsset) {
  const recordKey = String(record.client_asset_key || "").trim();
  const assetKey = String(asset.client_asset_key || "").trim();
  if (recordKey && assetKey) return recordKey === assetKey;
  const assetCode = String(asset.asset_code || "").trim().toUpperCase();
  if (!assetCode) return false;
  const recordCodes = [record.asset_code, record.matched_asset_code]
    .map((value) => String(value || "").trim().toUpperCase())
    .filter(Boolean);
  return recordCodes.includes(assetCode);
}

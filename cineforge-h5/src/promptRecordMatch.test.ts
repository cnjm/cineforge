import assert from "node:assert/strict";
import test from "node:test";
import { promptRecordMatchesAsset } from "./promptRecordMatch.ts";
import { normalizedTestAsset } from "./assetTestFixture.ts";

// 回归：design 记录历史上不带 client_asset_key（只带 asset_code），前端此前仅按
// client_asset_key 匹配 → 0 匹配 → 生产提示为空、hasPromptDraft 恒 false 卡住确认。
// 修复后与后端 prompt_record_matches_asset 对齐：回退按 asset_code / matched_asset_code。

test("falls back to asset_code when design lacks client_asset_key", () => {
  const asset = normalizedTestAsset("character", {});
  (asset as { asset_code: string }).asset_code = "BG-R001";
  const design = { asset_code: "BG-R001", prompt: "x" };
  assert.equal(promptRecordMatchesAsset(design, asset), true);
});

test("matches by matched_asset_code fallback too", () => {
  const asset = normalizedTestAsset("prop", {});
  (asset as { asset_code: string }).asset_code = "BG-P003";
  const design = { matched_asset_code: "bg-p003" }; // 大小写不敏感
  assert.equal(promptRecordMatchesAsset(design, asset), true);
});

test("prefers client_asset_key when both present", () => {
  const asset = normalizedTestAsset("character", {});
  (asset as { client_asset_key: string; asset_code: string }).client_asset_key = "asset:character:aaa";
  (asset as { asset_code: string }).asset_code = "BG-R001";
  // 键都在但不同 → 不匹配（即便 asset_code 相同），保持强绑定语义
  const design = { client_asset_key: "asset:character:bbb", asset_code: "BG-R001" };
  assert.equal(promptRecordMatchesAsset(design, asset), false);
});

test("does not match unrelated asset_code", () => {
  const asset = normalizedTestAsset("character", {});
  (asset as { asset_code: string }).asset_code = "BG-R001";
  const design = { asset_code: "BG-R999" };
  assert.equal(promptRecordMatchesAsset(design, asset), false);
});

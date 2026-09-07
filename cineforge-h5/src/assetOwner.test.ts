import assert from "node:assert/strict";
import test from "node:test";
import { shortAssetCode } from "./assetCode.ts";
import { currentDraftCharacterOptions, isOwnerAssignableProp, ownerCharacterRelation, selectedOwnerCharacterKey } from "./assetOwner.ts";
import type { BreakdownDraftAsset } from "./types.ts";
import { normalizedTestAsset } from "./assetTestFixture.ts";

test("owner association only applies to clothing and accessory props", () => {
  const prop = (propType: string): BreakdownDraftAsset => normalizedTestAsset("prop", { attributes: { ...normalizedTestAsset("prop").attributes, prop_type: propType } });
  assert.equal(isOwnerAssignableProp(prop("服饰")), true);
  assert.equal(isOwnerAssignableProp(prop("配饰")), true);
  assert.equal(isOwnerAssignableProp(prop("accessory")), true);
  assert.equal(isOwnerAssignableProp(prop("首饰")), true);
  assert.equal(isOwnerAssignableProp(prop("武器")), false);
  assert.equal(isOwnerAssignableProp(prop("生活用品")), false);
  assert.equal(isOwnerAssignableProp(normalizedTestAsset("character")), false);
});

test("formal short codes accept full codes and backend-issued short codes", () => {
  assert.equal(shortAssetCode("HJBG-R005", "backend"), "R005");
  assert.equal(shortAssetCode("R006", "backend"), "R006");
  assert.equal(shortAssetCode("R006"), "");
  assert.equal(shortAssetCode("R006", "agent_normalized"), "");
  assert.equal(shortAssetCode("HJBG-R006", "agent_normalized"), "R006");
});

test("owner candidates only include formally coded current draft characters", () => {
  const currentAssets: BreakdownDraftAsset[] = [
    normalizedTestAsset("character", { client_asset_key: "r5", asset_code: "HJBG-R005", display_code: "R005", display_label: "R005-阿宁", name: "阿宁" }),
    normalizedTestAsset("character", { client_asset_key: "r6", asset_code: "HJBG-R006", display_code: "R006", display_label: "R006-小满", name: "小满" }),
    normalizedTestAsset("character", { client_asset_key: "manual-role", code_state: "candidate", display_code: "待分配", name: "小满" }),
    normalizedTestAsset("scene", { client_asset_key: "scene", name: "庭院" }),
    normalizedTestAsset("prop", { client_asset_key: "prop", name: "发簪" }),
  ];
  const options = currentDraftCharacterOptions(currentAssets);

  assert.deepEqual(options.map(({ key, name, shortCode }) => ({ key, name, shortCode })), [
    { key: "r5", name: "阿宁", shortCode: "R005" },
    { key: "r6", name: "小满", shortCode: "R006" },
  ]);
  const owned = normalizedTestAsset("prop", { relations: { ...normalizedTestAsset("prop").relations, owner_character: ownerCharacterRelation(options[0]) } });
  assert.equal(selectedOwnerCharacterKey(owned, options), "r5");
  assert.deepEqual(ownerCharacterRelation(options[0]), {
    target_asset_key: "r5",
    target_asset_code: "HJBG-R005",
    target_display_code: "R005",
    target_display_label: "R005-阿宁",
  });
});

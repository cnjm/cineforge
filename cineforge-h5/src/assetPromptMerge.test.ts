import assert from "node:assert/strict";
import test from "node:test";
import { mergeSavedAssetsWithLatestPrompts } from "./assetPromptMerge.ts";
import type { BreakdownDraftAsset } from "./types.ts";

function asset(patch: Partial<BreakdownDraftAsset>): BreakdownDraftAsset {
  return { asset_type: "prop", asset_code: "P001", name: "令牌", ...patch };
}

test("preserves confirmation and manual metadata for the same prompt revision", () => {
  const [merged] = mergeSavedAssetsWithLatestPrompts([
    asset({ status: "confirmed", input_hash: "hash-1", prompt: "saved", metadata: { confirmation_status: "confirmed", owner_character: "R001" } }),
  ], [
    asset({ status: "pending_confirmation", input_hash: "hash-1", prompt: "latest", metadata: { prompt_design: { version: 1 } } }),
  ]);

  assert.equal(merged.status, "confirmed");
  assert.equal(merged.metadata?.confirmation_status, "confirmed");
  assert.equal(merged.metadata?.owner_character, "R001");
  assert.equal(merged.prompt, "latest");
});

test("resets confirmation only when the prompt revision changes", () => {
  const [merged] = mergeSavedAssetsWithLatestPrompts([
    asset({ status: "confirmed", input_hash: "hash-1", metadata: { confirmation_status: "confirmed", prop_type: "法器" } }),
  ], [
    asset({ input_hash: "hash-2", prompt: "new prompt" }),
  ]);

  assert.equal(merged.status, "pending_confirmation");
  assert.equal(merged.metadata?.confirmation_status, "pending_confirmation");
  assert.equal(merged.metadata?.prop_type, "法器");
});

test("keeps manually added assets and appends new Agent assets", () => {
  const merged = mergeSavedAssetsWithLatestPrompts([
    asset({ asset_code: "P001", name: "人工道具" }),
  ], [
    asset({ asset_code: "P002", name: "Agent 新道具", prompt: "prompt" }),
  ]);

  assert.deepEqual(merged.map((item) => item.asset_code), ["P001", "P002"]);
});

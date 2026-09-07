import assert from "node:assert/strict";
import test from "node:test";
import { isAssetCompletionResolved, isOptionalAssetCompletionField } from "./assetCompletion.ts";

test("requires text for ordinary completion fields", () => {
  assert.equal(isAssetCompletionResolved("appearance_material", { mode: "modify", text: "" }), false);
  assert.equal(isAssetCompletionResolved("appearance_material", { mode: "modify", text: "金属材质" }), true);
});

test("requires selected scenes for inferred scene fields", () => {
  assert.equal(isAssetCompletionResolved("scene_names", { mode: "modify", text: "场景一", sceneIds: [] }), false);
  assert.equal(isAssetCompletionResolved("scene_names", { mode: "modify", sceneIds: ["scene-1"] }), true);
});

test("allows an item to remain deferred", () => {
  assert.equal(isAssetCompletionResolved("state_plan", { mode: "defer" }), true);
});

test("treats owner character as an optional non-blocking field", () => {
  assert.equal(isOptionalAssetCompletionField("owner_character"), true);
  assert.equal(isAssetCompletionResolved("owner_character", { mode: "modify", text: "" }), true);
});

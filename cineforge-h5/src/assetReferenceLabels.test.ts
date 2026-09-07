import assert from "node:assert/strict";
import test from "node:test";
import { assetReferenceDisplayLabel, hydrateStoryboardSceneNames } from "./assetCode.ts";

const assets = [
  { asset_code: "TJBL-R001", display_code: "R001", display_label: "R001-Ran", asset_type: "character", name: "Ran" },
  { asset_code: "TJBL-SC001", display_code: "SC001", display_label: "SC001-Ran家卧室", asset_type: "scene", name: "Ran家卧室" },
];

test("formats formal character and scene references with short code and name", () => {
  assert.equal(assetReferenceDisplayLabel("TJBL-R001", assets, "character"), "R001-Ran");
  assert.equal(assetReferenceDisplayLabel("TJBL-SC001", assets, "scene"), "SC001-Ran家卧室");
});

test("does not infer asset identity from a name-only reference", () => {
  assert.equal(assetReferenceDisplayLabel("Ran", assets, "character"), "未匹配资产（Ran）");
  assert.equal(assetReferenceDisplayLabel("R001-Ran", assets, "character"), "R001-Ran");
});

test("hydrates a missing storyboard scene name from the formal asset inventory", () => {
  const hydrated = hydrateStoryboardSceneNames([{ scene_code: "TJBL-SC001", scene_name: "" }], assets);
  assert.equal(hydrated[0].scene_name, "Ran家卧室");
});

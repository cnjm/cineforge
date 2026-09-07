import assert from "node:assert/strict";
import test from "node:test";
import { readingLabel, readingLabels } from "./readingLabels.ts";

test("translates reading report enum values to Chinese", () => {
  assert.equal(readingLabel("shot_density", "high"), "高密度");
  assert.equal(readingLabel("culture", "contemporary"), "当代");
  assert.equal(readingLabel("relationship", "mentor-student"), "师徒");
  assert.equal(readingLabel("emotion", "tension"), "紧张");
  assert.equal(readingLabel("risk", "visual_complexity"), "视觉实现难度");
});

test("preserves free-form Chinese and unknown values", () => {
  assert.equal(readingLabel("culture", "山海经神话体系"), "山海经神话体系");
  assert.equal(readingLabel("visual_style", "custom_style_v2"), "custom_style_v2");
  assert.deepEqual(readingLabels("visual_style", ["cinematic", "水墨重彩"]), ["电影感", "水墨重彩"]);
});

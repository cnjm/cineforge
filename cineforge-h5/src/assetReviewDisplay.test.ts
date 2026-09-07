import assert from "node:assert/strict";
import test from "node:test";
import { aggregateGlobalReviewSuggestions, assetReviewFieldLabel, assetWorkflowStatus } from "./assetReviewDisplay.ts";
import type { AssetNormalizationGlobalReviewItem } from "./types.ts";

function globalReview(suggestion: string | null): AssetNormalizationGlobalReviewItem {
  return {
    review_id: `review-${suggestion || "none"}`,
    target_scope: "inventory",
    target_field: "cultural_origin.language",
    issue: "确认对白语言",
    suggestion,
    status: "待确认",
    severity: "warning",
    requirement_level: "required",
    source: "script-reading",
    source_path: "cultural_origin.language",
    human_input: null,
  };
}

test("maps normalized review fields to Chinese display labels", () => {
  assert.equal(assetReviewFieldLabel("attributes.appearance"), "外观");
  assert.equal(assetReviewFieldLabel("material"), "材质");
  assert.equal(assetReviewFieldLabel("buttons_and_accessories"), "纽扣与配饰");
  assert.equal(assetReviewFieldLabel("relations.owner_character"), "所属角色");
  assert.equal(assetReviewFieldLabel("cultural_origin.language"), "文化背景 / 语言");
  assert.equal(assetReviewFieldLabel("校徽样式"), "校徽样式");
  assert.equal(assetReviewFieldLabel("unknown_backend_field"), "字段");
});

test("global review hints contain only unique non-empty suggestions", () => {
  assert.deepEqual(aggregateGlobalReviewSuggestions([
    globalReview("建议统一使用泰语对白"),
    globalReview("建议统一使用泰语对白"),
    globalReview(null),
  ]), ["建议统一使用泰语对白"]);
});

test("asset workflow status follows review completion instead of internal code lifecycle", () => {
  assert.equal(assetWorkflowStatus(2, false), "待完善 2");
  assert.equal(assetWorkflowStatus(0, false), "待确认");
  assert.equal(assetWorkflowStatus(0, true), "已确认");
});

import assert from "node:assert/strict";
import test from "node:test";
import { canAccessReviewCenter, dependencyAssetLabel, dependencyAssetTypeLabel, dependencyReasonLabel, displayDependencyAssetCodes } from "./taskAccess.ts";

test("all directors can access the review center", () => {
  assert.equal(canAccessReviewCenter({ id: "director-1", display_name: "导演", role: "director" }), true);
  assert.equal(canAccessReviewCenter({ id: "admin-1", display_name: "管理员", role: "admin" }), true);
  assert.equal(canAccessReviewCenter({ id: "artist-1", display_name: "制作", role: "artist" }), false);
});

test("dependency asset labels are available to task modals", () => {
  assert.equal(displayDependencyAssetCodes(["TBL-R001", "TBL-SC002"]), "R001、SC002");
  assert.equal(displayDependencyAssetCodes([]), "");
});

test("structured dependency states use one maker-facing vocabulary", () => {
  const base = { asset_code: "TBL-R001", asset_name: "Ran", asset_type: "character", ready: false, files: [] };
  assert.equal(dependencyAssetLabel(base), "TBL-R001-Ran");
  assert.equal(dependencyAssetTypeLabel(base.asset_type), "人物");
  assert.equal(dependencyReasonLabel({ ...base, reason: "pending_review", approval_status: "pending_review" }), "待导演审核");
  assert.equal(dependencyReasonLabel({ ...base, reason: "rework_required", production_status: "rework" }), "已退回，正在返工");
  assert.equal(dependencyReasonLabel({ ...base, reason: "approved_file_missing" }), "已通过但无有效正式文件");
  assert.equal(dependencyReasonLabel({ ...base, ready: true, files: [{ file_id: "file-1" }, { file_id: "file-2" }] }), "2 个正式文件");
});

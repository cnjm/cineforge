import assert from "node:assert/strict";
import test from "node:test";
import { normalizeManualReviewItems } from "./reviewNormalization.ts";

test("preserves agent review field and exposes item text as its suggestion", () => {
  const items = normalizeManualReviewItems([{
    field: "character.室长.name",
    item: "室长未给出全名，建议补充正式姓名。",
    severity: "中",
    source: "asset-extract",
    status: "待确认",
  }]);

  assert.deepEqual(items[0], {
    item: "室长 · 名称",
    detail: "",
    status: "待确认",
    severity: "中",
    source: "asset-extract",
    path: "character.室长.name",
    code: "",
    asset_code: "",
    asset_name: "室长",
    issue_type: "",
    uncertainty: "",
    suggestion: "室长未给出全名，建议补充正式姓名。",
    target: "",
    target_field: "name",
    missing_reason: "",
    requirement_level: "",
    human_input: "",
    applied_at: "",
    match_status: "",
    matched_prop_code: "",
    matched_prop_name: "",
    match_action: "",
    match_confidence: "",
  });
});

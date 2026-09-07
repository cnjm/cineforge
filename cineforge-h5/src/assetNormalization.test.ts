import assert from "node:assert/strict";
import test from "node:test";
import {
  assetByClientKey,
  assetByType,
  assetCodeIsFormal,
  assetDisplayCode,
  assetDisplayLabel,
  parseAssetNormalizationV1,
  parseAssetNormalizations,
  parseBreakdownRead,
  parseGlobalAssetReviewItems,
  relationsByClientAssetKey,
  reviewItemsByClientAssetKey,
} from "./assetNormalization.ts";
import type { AssetNormalizationV1 } from "./types.ts";

function normalizedAsset(patch: Partial<AssetNormalizationV1> = {}): AssetNormalizationV1 {
  return {
    normalization_version: "AssetNormalization.v1",
    client_asset_key: "character-ran-1",
    asset_code: "TJBL-R001",
    display_code: "R001",
    display_label: "R001-Ran",
    code_state: "formal",
    asset_type: "character",
    name: "Ran",
    description: null,
    priority: "S",
    attributes: {
      character_type: "S",
      visual_presence: "on_screen",
      role: "主角",
      gender: null,
      age: null,
      appearance: null,
      core_requirement: null,
      personality: null,
      background: null,
      has_dialogue: true,
      dialogue_evidence: ["EP01-F001"],
      context_codes: ["EP01"],
    },
    relations: {
      relationship_text: null,
      scene_links: [],
      owner_character: null,
      storyboard_codes: ["TJBL-EP01-F001"],
      script_segment_codes: ["TJBL-EP01-J001"],
      evidence: [{
        target_code: "TJBL-SC001",
        storyboard_code: "TJBL-EP01-F001",
        script_segment_code: "TJBL-EP01-J001",
        source_field: "script",
        excerpt: null,
      }],
      confidence: "high",
    },
    review_items: [{
      review_id: "review-ran-role",
      asset_key: "character-ran-1",
      target_field: "attributes.role_type",
      issue: "角色定位需要确认",
      suggestion: "主角",
      status: "pending",
      severity: "warning",
      requirement_level: "required",
      source: "asset_extract",
      source_path: "assets.character-ran-1.attributes.role_type",
      human_input: null,
    }],
    source: {
      source_kind: "agent",
      agent_run_id: "run-1",
      skill_name: "asset-extract",
      raw_group: "characters",
      raw_index: 0,
    },
    repairs: [{
      field: "asset_code",
      action: "assign_formal_code",
      before: "R1",
      after: "TJBL-R001",
      reason: "统一为完整项目资产编号",
    }],
    ...patch,
  };
}

function reviewItemsFor(assetKey: string, reviewId = "review-ran-role") {
  return [{ ...normalizedAsset().review_items[0], review_id: reviewId, asset_key: assetKey }];
}

test("parses the frozen v1 contract and consumes display fields verbatim", () => {
  const asset = parseAssetNormalizationV1(normalizedAsset({
    code_state: "candidate",
  }));

  assert.equal(assetDisplayCode(asset), "R001");
  assert.equal(assetDisplayLabel(asset), "R001-Ran");
  assert.equal(assetCodeIsFormal(asset), false);
});

test("distinguishes same-name assets only by client_asset_key", () => {
  const first = normalizedAsset({ client_asset_key: "ran-age-1", review_items: reviewItemsFor("ran-age-1") });
  const second = normalizedAsset({
    client_asset_key: "ran-age-2",
    asset_code: "TJBL-R002",
    display_code: "R002",
    display_label: "R002-Ran",
    review_items: reviewItemsFor("ran-age-2", "review-ran-2"),
  });
  const assets = parseAssetNormalizations([first, second]);

  assert.equal(assetByClientKey(assets, "ran-age-1"), first);
  assert.equal(assetByClientKey(assets, "ran-age-2"), second);
  assert.equal(assetByClientKey(assets, "Ran"), undefined);
  assert.deepEqual(assetByType(assets, "character"), [first, second]);
});

test("selects relations and review items through the exact asset key", () => {
  const first = normalizedAsset({ client_asset_key: "ran-age-1", review_items: reviewItemsFor("ran-age-1") });
  const second = normalizedAsset({
    client_asset_key: "ran-age-2",
    asset_code: "TJBL-R002",
    display_code: "R002",
    display_label: "R002-Ran",
    relations: { ...first.relations, storyboard_codes: ["TJBL-EP01-F009"] },
    review_items: reviewItemsFor("ran-age-2", "review-ran-2"),
  });
  const assets = [first, second];

  assert.deepEqual(relationsByClientAssetKey(assets, "ran-age-2")?.storyboard_codes, ["TJBL-EP01-F009"]);
  assert.equal(reviewItemsByClientAssetKey(assets, "ran-age-2")?.[0].review_id, "review-ran-2");
  assert.equal(relationsByClientAssetKey(assets, "Ran"), undefined);
  assert.equal(reviewItemsByClientAssetKey(assets, "assets[1]"), undefined);
});

test("rejects wrong versions and missing required nested fields", () => {
  assert.throws(
    () => parseAssetNormalizationV1({ ...normalizedAsset(), normalization_version: "AssetNormalization.v2" }),
    /asset\.normalization_version/,
  );
  assert.throws(
    () => parseAssetNormalizationV1({ ...normalizedAsset(), display_label: "" }),
    /asset\.display_label/,
  );
  assert.throws(
    () => parseAssetNormalizationV1({
      ...normalizedAsset(),
      relations: { ...normalizedAsset().relations, owner_character: { target_asset_key: "missing-fields" } },
    }),
    /owner_character\.target_asset_code/,
  );
  assert.throws(
    () => parseAssetNormalizationV1({ ...normalizedAsset(), repairs: [{ field: "asset_code", action: "repair", after: "TJBL-R001", reason: "补全" }] }),
    /repairs\[0\]\.before/,
  );
  assert.throws(
    () => parseAssetNormalizationV1({
      ...normalizedAsset(),
      review_items: [{ ...normalizedAsset().review_items[0], asset_key: "another-asset" }],
    }),
    /review_items\[0\]\.asset_key/,
  );
  assert.throws(
    () => parseAssetNormalizations([normalizedAsset(), normalizedAsset()]),
    /unique client_asset_key/,
  );
});

test("rejects relation targets outside the inventory and target identity drift", () => {
  const scene = normalizedAsset({
    client_asset_key: "scene-bedroom-1",
    asset_code: "TJBL-SC001",
    display_code: "SC001",
    display_label: "SC001-Ran家卧室",
    asset_type: "scene",
    name: "Ran家卧室",
    attributes: {
      context_code: null, render_mode: null, scene_no: null, interior_exterior: null,
      time: null, location: null, atmosphere: null, narrative_function: null,
      spatial_zones: [], visual_goal: null, key_props: [], key_prop_codes: [],
      continuity_risks: [], episode_code: null,
    },
    review_items: [],
  });
  const link = {
    target_asset_key: scene.client_asset_key,
    target_asset_code: scene.asset_code,
    target_display_code: scene.display_code,
    target_display_label: scene.display_label,
  };
  const character = normalizedAsset({ relations: { ...normalizedAsset().relations, scene_links: [link] } });
  assert.equal(parseAssetNormalizations([character, scene]).length, 2);
  assert.throws(() => parseAssetNormalizations([character]), /same inventory/);
  assert.throws(
    () => parseAssetNormalizations([
      normalizedAsset({ relations: { ...character.relations, scene_links: [{ ...link, target_display_label: "SC001-错误" }] } }),
      scene,
    ]),
    /exact target asset identity/,
  );
});

test("parses global review items without inventing an asset owner", () => {
  const reviews = parseGlobalAssetReviewItems([{
    review_id: "global-review-language",
    target_scope: "inventory",
    target_field: "cultural_origin.language",
    issue: "确认对白语言",
    suggestion: null,
    status: "待确认",
    severity: "warning",
    requirement_level: "required",
    source: "script-reading",
    source_path: "cultural_origin.language",
    human_input: null,
  }]);
  assert.equal(reviews[0].target_scope, "inventory");
  assert.equal("asset_key" in reviews[0], false);
});

test("accepts only the view-based BreakdownRead contract", () => {
  const asset = normalizedAsset();
  const breakdown = parseBreakdownRead({
    project_id: "project-1",
    version: 3,
    data_state: "normalized",
    view: {
      normalization_version: "AssetNormalization.v1",
      assets: [asset],
      global_review_items: [],
      storyboards: [],
      reading_review_state: { status: "confirmed", confirmed_revision_id: "rev-reading", confirmation_mode: "human_confirmed" },
      asset_review_state: { status: "needs_review" },
    },
    agent_run_id: "run-1",
    updated_at: "2026-07-23T12:00:00Z",
  });

  assert.equal(breakdown.view.assets[0], asset);
  assert.throws(() => parseBreakdownRead({ ...breakdown, raw_output: {} }), /breakdown\.raw_output/);
  assert.throws(() => parseBreakdownRead({ ...breakdown, assets: [asset] }), /breakdown\.assets/);
  assert.throws(() => parseBreakdownRead({ ...breakdown, storyboards: [] }), /breakdown\.storyboards/);
});

test("fails closed when the breakdown view normalization version is missing or wrong", () => {
  const base = {
    project_id: "project-1",
    version: 1,
    data_state: "normalized",
    agent_run_id: null,
    updated_at: "2026-07-23T12:00:00Z",
  };
  const reviewStates = {
    reading_review_state: { status: "needs_review" },
    asset_review_state: { status: "needs_review" },
    global_review_items: [],
  };
  assert.throws(
    () => parseBreakdownRead({ ...base, view: { ...reviewStates, assets: [] } }),
    /view\.normalization_version/,
  );
  assert.throws(
    () => parseBreakdownRead({ ...base, view: { ...reviewStates, normalization_version: "AssetNormalization.v2", assets: [] } }),
    /view\.normalization_version/,
  );
  assert.throws(
    () => parseBreakdownRead({ ...base, view: { normalization_version: "AssetNormalization.v1", assets: [], global_review_items: [], reading_review_state: "confirmed", asset_review_state: { status: "needs_review" } } }),
    /reading_review_state/,
  );
});

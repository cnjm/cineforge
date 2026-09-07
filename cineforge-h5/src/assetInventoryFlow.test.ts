import assert from "node:assert/strict";
import test from "node:test";

import { normalizedTestAsset } from "./assetTestFixture.ts";
import {
  breakdownPayload,
  withEditedAssetInventory,
  withFreshAssetConfirmation,
} from "./assetNormalization.ts";
import type { BreakdownDraftData } from "./types.ts";

function emptyDraft(): BreakdownDraftData {
  return {
    scriptSegments: [], storyboards: [], assets: [],
    readingReviewState: { status: "needs_review" }, assetReviewState: { status: "needs_review" },
    readthrough: [], dialogueScript: [], costumeDesigns: [], costumeProcessingSummary: [],
    assetPromptDesigns: [], assetPromptProcessingSummary: [], storyboardShots: [], coverageChecks: [],
    scenePackages: [], deliveryChecklist: [], manualReviewItems: [], globalAssetReviewItems: [], notes: [],
  };
}

test("asset edits preserve confirmed revision lineage and require confirmation again", () => {
  const original = normalizedTestAsset("character");
  const draft = {
    ...emptyDraft(),
    assets: [original],
    assetReviewState: {
      status: "confirmed" as const,
      confirmed_revision_id: "asset-revision-7",
      confirmation_mode: "human_confirmed",
    },
  };
  const editedAsset = { ...original, name: "修订角色", display_label: "R001-修订角色" };

  const edited = withEditedAssetInventory(draft, [editedAsset]);

  assert.equal(edited.assets[0].client_asset_key, original.client_asset_key);
  assert.equal(edited.assets[0].display_label, "R001-修订角色");
  assert.equal(edited.assetReviewState.status, "pending_confirmation");
  assert.equal(edited.assetReviewState.confirmed_revision_id, "asset-revision-7");
  assert.equal(edited.assetReviewState.confirmation_mode, "human_confirmed");
});

test("breakdownPayload does not round-trip agent-derived prompt/design fields", () => {
  // Contract guard: the human-save payload must NOT echo agent display fields
  // back to the backend. Echoing them re-persists stale agent output into the
  // human revision. The backend carries these forward across versions instead
  // (see _breakdown_content_from_request inheritance). If someone adds these to
  // the payload, the backend inheritance + this test both must be reconsidered.
  const draft = {
    ...emptyDraft(),
    assetPromptDesigns: [{ asset_code: "HJBG-R001", prompt: "master" }],
    assetPromptProcessingSummary: [{ asset_code: "HJBG-R001", processing_status: "new" }],
    costumeDesigns: [{ asset_code: "HJBG-R001" }],
    costumeProcessingSummary: [{ asset_code: "HJBG-R001", processing_status: "reused" }],
  };

  const payload = breakdownPayload(draft);

  assert.equal("asset_prompt_designs" in payload.view, false);
  assert.equal("asset_prompt_processing_summary" in payload.view, false);
  assert.equal("costume_designs" in payload.view, false);
  assert.equal("costume_processing_summary" in payload.view, false);
});

test("fresh asset confirmation clears the prior revision id", () => {
  const draft = {
    ...emptyDraft(),
    assetReviewState: {
      status: "pending_confirmation" as const,
      confirmed_revision_id: "asset-revision-7",
      confirmation_mode: "human_confirmed",
    },
  };

  const confirmed = withFreshAssetConfirmation(draft);

  assert.deepEqual(confirmed.assetReviewState, {
    status: "confirmed",
    confirmed_revision_id: null,
    confirmation_mode: "human_confirmed",
  });
});

test("breakdown save emits one normalized view and scope metadata only", () => {
  const asset = normalizedTestAsset("scene");
  const draft = {
    ...emptyDraft(),
    assets: [asset],
    readingRevisionContext: {
      episode_id: "episode-1",
      episode_code: "EP03",
      script_version_id: "script-version-4",
    },
  };

  const payload = breakdownPayload(draft);

  assert.deepEqual(Object.keys(payload).sort(), [
    "change_summary",
    "data_state",
    "episode_code",
    "episode_id",
    "expected_version",
    "script_version_id",
    "source_label",
    "view",
  ]);
  assert.equal(payload.view.normalization_version, "AssetNormalization.v1");
  assert.deepEqual(payload.view.assets, [asset]);
  assert.equal(Object.prototype.hasOwnProperty.call(payload, "raw_output"), false);
  assert.equal(Object.prototype.hasOwnProperty.call(payload, "assets"), false);
  assert.equal(Object.prototype.hasOwnProperty.call(payload, "storyboards"), false);
});

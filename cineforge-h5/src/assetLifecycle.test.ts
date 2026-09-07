import assert from "node:assert/strict";
import test from "node:test";
import { assetParticipatesInLifecycleGate, characterAssetIsNonVisual } from "./assetLifecycle.ts";
import type { BreakdownDraftAsset } from "./types.ts";
import { normalizedTestAsset } from "./assetTestFixture.ts";

test("defers explicitly mentioned characters from frontend asset gates", () => {
  const deferred: BreakdownDraftAsset = normalizedTestAsset("character", {
    name: "背景中提及的人物",
    attributes: { ...normalizedTestAsset("character").attributes, visual_presence: "mentioned_only" },
  });
  assert.equal(characterAssetIsNonVisual(deferred), true);
  assert.equal(assetParticipatesInLifecycleGate(deferred), false);
});

test("does not infer that a background group is off screen", () => {
  const visible = normalizedTestAsset("character", { name: "宴会宾客" });

  assert.equal(characterAssetIsNonVisual(visible), false);
  assert.equal(assetParticipatesInLifecycleGate(visible), true);
});

test("honors explicit on-screen presence over historical character type text", () => {
  const visible = normalizedTestAsset("character", { name: "本集正式出场的亲属" });

  assert.equal(characterAssetIsNonVisual(visible), false);
  assert.equal(assetParticipatesInLifecycleGate(visible), true);
});

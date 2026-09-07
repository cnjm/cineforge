import assert from "node:assert/strict";
import test from "node:test";

import { characterReviewScopeReady, characterReviewSelectionReady, expectedCharacterReviewTasks } from "./characterTaskFlow.ts";

test("A can be approved while B-E are still locked", () => {
  const tasks = [
    { id: "A", status: "reviewing", locked: false },
    { id: "B", status: "todo", locked: true },
    { id: "C", status: "todo", locked: true },
    { id: "D", status: "todo", locked: true },
    { id: "E", status: "todo", locked: true },
  ] as const;

  assert.deepEqual(expectedCharacterReviewTasks(tasks), [tasks[0]]);
  assert.equal(characterReviewScopeReady(tasks, ["A"]), true);
});

test("every unlocked reviewing pose must have a submitted batch", () => {
  const tasks = [
    { id: "B", status: "reviewing", locked: false },
    { id: "C", status: "reviewing", locked: false },
    { id: "D", status: "todo", locked: false },
  ] as const;

  assert.equal(characterReviewScopeReady(tasks, ["B"]), false);
  assert.equal(characterReviewScopeReady(tasks, ["B", "C"]), true);
  assert.equal(characterReviewScopeReady(tasks, []), false);
});

test("only A requires an identity master while B-E require a selected result", () => {
  assert.equal(characterReviewSelectionReady("A", ["a-1"], null), false);
  assert.equal(characterReviewSelectionReady("A", ["a-1"], "a-1"), true);
  assert.equal(characterReviewSelectionReady("B", ["b-1"], null), true);
  assert.equal(characterReviewSelectionReady("E", [], null), false);
});

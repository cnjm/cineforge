import assert from "node:assert/strict";
import test from "node:test";
import { productionProgressByEntity, productionProgressSummary } from "./projectProgress.ts";
import type { Task } from "./types.ts";

function task(id: string, input: Partial<Task>): Task {
  return {
    id,
    project_id: "project-1",
    title: id,
    task_type: "asset",
    status: "todo",
    submissions: [],
    ...input,
  };
}

test("aggregates physical keyframe and video tasks as one parent storyboard", () => {
  const rows = productionProgressByEntity([
    task("kf-1", { storyboard_id: "shot-1", task_type: "text_to_image", status: "completed" }),
    task("vid-1", { storyboard_id: "shot-1", task_type: "image_to_video", status: "completed" }),
    task("kf-2", { storyboard_id: "shot-2", task_type: "text_to_image", status: "completed" }),
    task("vid-2", { storyboard_id: "shot-2", task_type: "image_to_video", status: "in_progress" }),
  ]);

  assert.deepEqual(rows.find((row) => row.key === "storyboard"), {
    key: "storyboard", label: "分镜", total: 2, done: 1,
  });
});

test("aggregates character views and other tasks as one asset", () => {
  const summary = productionProgressSummary([
    task("char-a", { asset_id: "asset-1", task_type: "asset", task_variant: "A", status: "completed" }),
    task("char-b", { asset_id: "asset-1", task_type: "asset", task_variant: "B", status: "completed" }),
    task("scene", { asset_id: "asset-2", task_type: "text_to_image", status: "submitted" }),
    task("edit", { task_type: "editing", status: "completed" }),
  ]);

  assert.deepEqual(summary.rows.find((row) => row.key === "asset"), {
    key: "asset", label: "资产", total: 2, done: 1,
  });
  assert.equal(summary.total, 2);
  assert.equal(summary.done, 1);
});

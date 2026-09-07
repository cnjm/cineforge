import type { Task } from "./types";

export type ProductionProgressRow = {
  key: "asset" | "storyboard";
  label: "资产" | "分镜";
  total: number;
  done: number;
};

const ASSET_TASK_TYPES = new Set(["asset", "asset_confirm", "audio", "text_to_image"]);
const STORYBOARD_TASK_TYPES = new Set(["storyboard_shot", "text_to_image", "image_to_video", "video_generation"]);

function groupedProgress(tasks: Task[], kind: ProductionProgressRow["key"]) {
  const grouped = new Map<string, Task[]>();
  tasks.forEach((task) => {
    const isStoryboard = Boolean(task.storyboard_id) && STORYBOARD_TASK_TYPES.has(task.task_type);
    const isAsset = !task.storyboard_id && Boolean(task.asset_id) && ASSET_TASK_TYPES.has(task.task_type);
    if ((kind === "storyboard" && !isStoryboard) || (kind === "asset" && !isAsset)) return;
    const key = kind === "storyboard" ? task.storyboard_id! : task.asset_id!;
    grouped.set(key, [...(grouped.get(key) || []), task]);
  });
  return {
    total: grouped.size,
    done: Array.from(grouped.values()).filter((group) => (
      group.length > 0 && group.every((task) => task.status === "completed")
    )).length,
  };
}

export function productionProgressByEntity(tasks: Task[]): ProductionProgressRow[] {
  const asset = groupedProgress(tasks, "asset");
  const storyboard = groupedProgress(tasks, "storyboard");
  return [
    { key: "asset", label: "资产", ...asset },
    { key: "storyboard", label: "分镜", ...storyboard },
  ];
}

export function productionProgressSummary(tasks: Task[]) {
  const rows = productionProgressByEntity(tasks);
  return {
    rows,
    total: rows.reduce((sum, row) => sum + row.total, 0),
    done: rows.reduce((sum, row) => sum + row.done, 0),
  };
}

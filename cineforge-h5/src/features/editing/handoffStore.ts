import type { Task } from "../../types";
import { countOuttakes } from "./outtakeStore";

// TODO(审批中心-后端接口需求文档 需求3 - 交接剪辑/废片池 P2):
//   当前实现: 本文件为「交接剪辑」实验性功能的 localStorage 过渡实现,数据仅存本机,
//            不跨设备、不持久化到服务端,刷新/换机后状态会丢失。
//   待后端接口:
//     - POST /api/tasks/{task_id}/submissions/{submission_id}/handoff
//       body: { target_step, note? }  → 标记候选移交剪辑工序
//     - GET  /api/episodes/{episode_id}/handoff  → 查询本集交接状态(替代 getHandoff)
//     - PATCH /api/handoffs/{handoff_id}  → 更新交接状态(替代 updateHandoffStatus)
//   后端就绪后处理步骤:
//     1. 删除本文件全部 localStorage 读写逻辑(readAll/writeAll/STORAGE_KEY)
//     2. 将 createHandoff / getHandoff / updateHandoffStatus / submitEditorFeedback
//        改为调用上述真实 API(用 api.post/api.get/api.patch)
//     3. ReviewWorkbenchPage.tsx 中 alreadyHandedOff / handleHandoff 改为从接口读取
//     4. 移除「实验」角标(handoff-btn 上的 badge--experiment)
//     5. 删除本文件,或保留为类型定义(EpisodeHandoff/HandoffStatus)的导出口
//   详见: docs/需求文档/审批中心-后端接口需求文档.md 需求 3

/** 剪辑过料队列状态（不做站内粗剪） */
export type HandoffStatus = "handed_off" | "need_resupply" | "downloaded";

export type EpisodeHandoff = {
  id: string;
  projectId: string;
  projectTitle: string;
  episodeCode: string;
  handedOffAt: string;
  handedOffBy: string;
  shotTaskIds: string[];
  /** 交接时快照：本集废片池条数（补镜料） */
  outtakeCount: number;
  /** 已定版镜数 / 包内镜数 */
  readyCount: number;
  totalCount: number;
  status: HandoffStatus;
  /** 剪辑建议导演补料的说明 */
  editorFeedback?: string | null;
  feedbackAt?: string | null;
};

const STORAGE_KEY = "cineforge.episodeHandoffs.v1";

function normalizeStatus(raw: string | undefined): HandoffStatus {
  if (raw === "need_resupply" || raw === "downloaded") return raw;
  if (raw === "submitted_final") return "downloaded";
  // in_edit / handed_off / 其它 → 待过料
  return "handed_off";
}

function readAll(): EpisodeHandoff[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as EpisodeHandoff[];
    if (!Array.isArray(parsed)) return [];
    return parsed.map((item) => ({
      ...item,
      outtakeCount: item.outtakeCount ?? 0,
      status: normalizeStatus(item.status as string),
      editorFeedback: item.editorFeedback ?? null,
      feedbackAt: item.feedbackAt ?? null,
    }));
  } catch {
    return [];
  }
}

function writeAll(items: EpisodeHandoff[]) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(items));
  } catch {
    /* ignore */
  }
}

export function listHandoffs(): EpisodeHandoff[] {
  return readAll().sort((a, b) => b.handedOffAt.localeCompare(a.handedOffAt));
}

export function getHandoff(projectId: string, episodeCode: string): EpisodeHandoff | null {
  return readAll().find((item) => item.projectId === projectId && item.episodeCode === episodeCode) || null;
}

/** 分镜生产类任务（用于本集是否齐套） */
export function isEpisodeShotTask(task: Task): boolean {
  if (task.task_type === "storyboard_shot") return true;
  if (["image_to_video", "video_generation"].includes(task.task_type)) return true;
  if (task.task_type === "text_to_image" && (task.storyboard_id || task.storyboard_code)) return true;
  return Boolean(task.storyboard_id || task.storyboard_code);
}

export function shotReady(task: Task): boolean {
  if (task.status === "completed") return true;
  if (task.task_type === "storyboard_shot" && task.video_done) return true;
  return false;
}

export type EpisodeReadySummary = {
  projectId: string;
  episodeCode: string;
  shots: Task[];
  ready: Task[];
  pending: Task[];
  reviewing: Task[];
  allReady: boolean;
  alreadyHandedOff: boolean;
};

export function summarizeEpisodeReady(
  tasks: Task[],
  projectId: string,
  episodeCode: string,
): EpisodeReadySummary {
  const shots = tasks.filter(
    (task) =>
      task.project_id === projectId &&
      String(task.episode_code || "") === episodeCode &&
      isEpisodeShotTask(task),
  );
  const ready = shots.filter(shotReady);
  const reviewing = shots.filter((task) => ["submitted", "reviewing"].includes(task.status));
  const pending = shots.filter((task) => !shotReady(task));
  const handoff = getHandoff(projectId, episodeCode);
  return {
    projectId,
    episodeCode,
    shots,
    ready,
    pending,
    reviewing,
    allReady: shots.length > 0 && pending.length === 0 && reviewing.length === 0,
    alreadyHandedOff: Boolean(handoff && handoff.status !== "downloaded"),
  };
}

export function createHandoff(input: {
  projectId: string;
  projectTitle: string;
  episodeCode: string;
  handedOffBy: string;
  shots: Task[];
}): EpisodeHandoff {
  const ready = input.shots.filter(shotReady);
  const outtakeCount = countOuttakes(input.projectId, input.episodeCode);
  const record: EpisodeHandoff = {
    id: `handoff:${input.projectId}:${input.episodeCode}:${Date.now()}`,
    projectId: input.projectId,
    projectTitle: input.projectTitle,
    episodeCode: input.episodeCode,
    handedOffAt: new Date().toISOString(),
    handedOffBy: input.handedOffBy,
    shotTaskIds: ready.map((task) => task.id),
    outtakeCount,
    readyCount: ready.length,
    totalCount: input.shots.length,
    status: "handed_off",
    editorFeedback: null,
    feedbackAt: null,
  };
  const rest = readAll().filter(
    (item) => !(item.projectId === input.projectId && item.episodeCode === input.episodeCode),
  );
  writeAll([record, ...rest]);
  return record;
}

export function updateHandoffStatus(
  projectId: string,
  episodeCode: string,
  status: HandoffStatus,
  extra?: Partial<Pick<EpisodeHandoff, "editorFeedback" | "feedbackAt">>,
): EpisodeHandoff | null {
  const all = readAll();
  const idx = all.findIndex((item) => item.projectId === projectId && item.episodeCode === episodeCode);
  if (idx < 0) return null;
  all[idx] = { ...all[idx], status, ...extra };
  writeAll(all);
  return all[idx];
}

export function submitEditorFeedback(
  projectId: string,
  episodeCode: string,
  feedback: string,
): EpisodeHandoff | null {
  return updateHandoffStatus(projectId, episodeCode, "need_resupply", {
    editorFeedback: feedback.trim(),
    feedbackAt: new Date().toISOString(),
  });
}

import type { Submission, Task } from "../../types";

// TODO(审批中心-后端接口需求文档 需求3 - 交接剪辑/废片池 P2):
//   当前实现: 本文件为「废片池」实验性功能的 localStorage 过渡实现,数据仅存本机,
//            不跨设备、不持久化到服务端,刷新/换机后废片记录会丢失。
//   待后端接口:
//     - POST   /api/tasks/{task_id}/submissions/{submission_id}/waste
//       body: { comment }  → 投入废片池(替代 addOuttakeFromReview)
//     - DELETE /api/tasks/{task_id}/submissions/{submission_id}/waste  → 移出废片池
//     - GET    /api/tasks/{task_id}/waste-pool  → 查询任务废片池候选列表(替代 listOuttakes)
//     - GET    /api/episodes/{episode_id}/waste-pool/count  → 本集废片计数(替代 countOuttakes)
//   后端就绪后处理步骤:
//     1. 删除本文件全部 localStorage 读写逻辑(readAll/writeAll/STORAGE_KEY)
//     2. 将 addOuttakeFromReview / listOuttakes / countOuttakes 改为调用上述真实 API
//     3. ReviewWorkbenchPage.tsx 中 outtakeCount / handleOuttakeAndRework 改为接口读写
//     4. 移除「实验」角标(outtake-counter 相关 badge--experiment)
//     5. 删除本文件,或保留为类型定义(OuttakeClip)的导出口
//   详见: docs/需求文档/审批中心-后端接口需求文档.md 需求 3

/** 导演「打回并进废片池」留下的补镜料（本机演示；待后端正式 API） */
export type OuttakeClip = {
  id: string;
  projectId: string;
  episodeCode: string;
  taskId: string;
  taskTitle: string;
  storyboardCode?: string | null;
  submissionId: string;
  fileId?: string | null;
  fileType?: string | null;
  mediaType?: string | null;
  comment: string;
  addedAt: string;
  addedBy: string;
};

const STORAGE_KEY = "cineforge.outtakePool.v1";

function readAll(): OuttakeClip[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as OuttakeClip[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function writeAll(items: OuttakeClip[]) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(items));
  } catch {
    /* ignore */
  }
}

export function listOuttakes(projectId?: string, episodeCode?: string): OuttakeClip[] {
  return readAll()
    .filter((item) => {
      if (projectId && item.projectId !== projectId) return false;
      if (episodeCode && item.episodeCode !== episodeCode) return false;
      return true;
    })
    .sort((a, b) => b.addedAt.localeCompare(a.addedAt));
}

export function countOuttakes(projectId: string, episodeCode: string): number {
  return listOuttakes(projectId, episodeCode).length;
}

/** 把当前审片预览的候选写入废片池，并仍走「打回返工」任务流 */
export function addOuttakeFromReview(input: {
  task: Task;
  submission: Submission;
  comment: string;
  addedBy: string;
}): OuttakeClip {
  const episodeCode = input.task.episode_code || "EP01";
  const clip: OuttakeClip = {
    id: `outtake:${input.submission.id}:${Date.now()}`,
    projectId: input.task.project_id,
    episodeCode,
    taskId: input.task.id,
    taskTitle: input.task.title,
    storyboardCode: input.task.storyboard_code,
    submissionId: input.submission.id,
    fileId: input.submission.file_id,
    fileType: input.submission.file_type,
    mediaType: input.submission.media_type,
    comment: input.comment.trim(),
    addedAt: new Date().toISOString(),
    addedBy: input.addedBy,
  };
  const rest = readAll().filter(
    (item) => !(item.submissionId === clip.submissionId && item.projectId === clip.projectId),
  );
  writeAll([clip, ...rest]);
  return clip;
}

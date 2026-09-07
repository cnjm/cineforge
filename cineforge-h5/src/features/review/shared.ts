import React from "react";
import { api, API_BASE, getAuthToken } from "../../api";
import type { Submission, SubmissionBatch, Task } from "../../types";

/** 提交批次解析：优先取 submitted / reviewing，否则取最新版本 */
export function resolveBatch(task: Task): SubmissionBatch | null {
  const batches = task.submission_batches || [];
  return (
    batches.find((b) => b.status === "submitted") ||
    batches.find((b) => b.status === "reviewing") ||
    [...batches].sort((a, b) => b.version_no - a.version_no)[0] ||
    null
  );
}

/** 从某个批次筛出候选成果（若无批次则返回全部非归档） */
export function candidatesOf(task: Task, batch: SubmissionBatch | null): Submission[] {
  const all = (task.submissions || []).filter((s) => !s.is_archived);
  if (!batch) return all;
  const inBatch = all.filter((s) => s.batch_id === batch.id);
  return inBatch.length ? inBatch : all;
}

/** 媒体类型分类：image / video / audio / other */
export function mediaKind(submission?: Submission): "image" | "video" | "audio" | "other" {
  const raw = String(submission?.file_type || "").toLowerCase();
  if (raw.includes("video") || raw === "mp4" || raw === "mov") return "video";
  if (raw.includes("audio") || raw === "wav" || raw === "mp3") return "audio";
  if (raw.includes("image") || raw === "png" || raw === "jpg" || raw === "jpeg" || raw === "webp") return "image";
  return "other";
}

/** 任务标题短版：去前缀，截 10 字 */
export function shortTitle(task: Task): string {
  const raw = task.title || task.storyboard_code || "分镜";
  return raw.replace(/^.*?[·\-—]\s*/, "").slice(0, 10) || raw.slice(0, 10);
}

/** 相对时间字符串：提交于1分钟前 / 提交于x分钟前 / 提交于x小时前 / 提交于x天前 */
export function relativeTime(at?: string | null): string {
  if (!at) return "—";
  const ms = Date.now() - new Date(at).getTime();
  if (!isFinite(ms) || ms < 0) return "提交于刚刚";
  const min = Math.max(1, Math.floor(ms / 60000));
  if (min < 60) return `提交于${min}分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `提交于${hr}小时前`;
  const day = Math.floor(hr / 24);
  return `提交于${day}天前`;
}

/** 相对时间值（不含"提交于"前缀）：1分钟前 / x分钟前 / x小时前 / x天前 */
export function relativeTimeValue(at?: string | null): string {
  if (!at) return "—";
  const ms = Date.now() - new Date(at).getTime();
  if (!isFinite(ms) || ms < 0) return "刚刚";
  const min = Math.max(1, Math.floor(ms / 60000));
  if (min < 60) return `${min}分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}小时前`;
  const day = Math.floor(hr / 24);
  return `${day}天前`;
}

/** 绝对时间格式化：YYYY-MM-DD HH:mm:ss */
export function formatDateTime(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  if (!isFinite(date.getTime())) return value;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

/** 任务资产分类：character / scene / prop / music / storyboard / keyframe / other */
export type ReviewAssetKind = "character" | "scene" | "prop" | "music" | "storyboard" | "keyframe" | "other";

export function reviewAssetKind(task: Task): ReviewAssetKind {
  const code = (
    (task.storyboard_code || "")
    || (task.scene_code || "")
    || ""
  ).toUpperCase();
  const sceneName = task.scene_name || "";
  const title = task.title || "";
  const variantTitle = task.variant_title_zh || "";

  // 音乐优先：M 开头 / 场景名含"音乐" / 标题含"音乐"
  if (/^M\d/i.test(code) || sceneName.includes("音乐") || title.includes("音乐") || variantTitle.includes("音乐")) return "music";
  // 道具：P 开头 / 场景名/标题/变体标题含"道具"
  if (/^P\d/i.test(code) || sceneName.includes("道具") || title.includes("道具") || variantTitle.includes("道具")) return "prop";
  // 场景：SC 开头 / 场景名含"场景" / 标题含"场景"
  if (/^SC\d/i.test(code) || sceneName.includes("场景") || title.includes("场景") || variantTitle.includes("场景")) return "scene";
  // 分镜/关键帧：BG- 开头 / 场景名/标题含"分镜"
  if (/^BG-/i.test(code) || sceneName.includes("分镜") || title.includes("分镜") || variantTitle.includes("分镜")) {
    const mt = String(task.media_type || "").toLowerCase();
    const tt = String(task.task_type || "").toLowerCase();
    if (mt === "video" || tt === "image_to_video" || title.includes("视频")) return "storyboard";
    return "keyframe";
  }
  // 人物：R 开头 / 场景名/标题含"人物"或"定装"
  if (/^R\d/i.test(code) || sceneName.includes("人物") || sceneName.includes("定装") || title.includes("人物") || variantTitle.includes("人物")) return "character";

  // 兜底：基于 task_type 和 variant_kind
  const tt = String(task.task_type || "");
  const vk = String(task.variant_kind || "");
  const mt = String(task.media_type || "").toLowerCase();
  if (tt === "storyboard_shot") {
    return mt === "video" ? "storyboard" : "keyframe";
  }
  if (tt === "asset") {
    if (vk === "scene_view") return "scene";
    if (vk === "prop_state") return "prop";
    if (vk === "character_view" || vk === "") return "character";
  }
  if (mt.includes("audio") || mt === "mp3" || mt === "wav") return "music";
  if (tt === "text_to_image" && !task.storyboard_id && !task.storyboard_code) return "keyframe";
  return "other";
}

export const REVIEW_ASSET_FILTERS: { key: ReviewAssetKind | "all"; label: string }[] = [
  { key: "all", label: "全部任务" },
  { key: "character", label: "人物" },
  { key: "scene", label: "场景" },
  { key: "prop", label: "道具" },
  { key: "storyboard", label: "分镜" },
  { key: "keyframe", label: "关键帧" },
];

/** 项目标题查找（兼容 name / title） */
export function projectTitle(projects: { id: string; title?: string | null; name?: string | null }[], id: string): string {
  const p = projects.find((x) => x.id === id);
  return p?.title || p?.name || id;
}

/** 取文件预览 URL：图片走 /files/{id}/content（带鉴权fetch -> blob），视频/音频走 playback-url */
export function useMediaUrl(
  submission: Submission | null | undefined,
  opts?: { contentTypeOnly?: boolean }
): { url: string | null; loading: boolean; error: string | null } {
  const [url, setUrl] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState<boolean>(false);
  const [error, setError] = React.useState<string | null>(null);
  const token = getAuthToken();

  React.useEffect(() => {
    let canceled = false;
    const ctrl = new AbortController();
    if (!submission) {
      setUrl(null);
      setLoading(false);
      setError(null);
      return;
    }
    // 兜底：静态路径如 /demo-assets/... 或 https:// 外部资源直接用
    if (submission.file_path && (submission.file_path.startsWith("/") || submission.file_path.startsWith("http"))) {
      setUrl(submission.file_path);
      setLoading(false);
      setError(null);
      return;
    }
    const kind = mediaKind(submission);
    setLoading(true);
    setError(null);
    (async () => {
      try {
        let out: string | null = null;
        if (kind === "video" || kind === "audio") {
          if (submission.file_id) {
            const resp = await api.getFilePlaybackUrl(submission.file_id, ctrl.signal);
            out = resp.url;
          } else if (submission.file_path && (submission.file_path.startsWith("/") || submission.file_path.startsWith("http"))) {
            out = submission.file_path;
          }
        } else {
          // image / other — 用 fetch + blob 拿 content（走 Bearer 鉴权）
          if (submission.file_id && token) {
            const resp = await fetch(`${API_BASE || "/api"}/files/${submission.file_id}/content`, {
              headers: { Authorization: `Bearer ${token}` },
              signal: ctrl.signal,
            });
            if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
            const blob = await resp.blob();
            out = URL.createObjectURL(blob);
          } else if (submission.file_path && (submission.file_path.startsWith("/") || submission.file_path.startsWith("http"))) {
            out = submission.file_path;
          }
        }
        if (!canceled) {
          setUrl(out);
          setLoading(false);
        }
      } catch (e: any) {
        if (!canceled && e?.name !== "AbortError") {
          setError(e?.message || "获取预览失败");
          setLoading(false);
        }
      }
    })();
    return () => {
      canceled = true;
      ctrl.abort();
      if (url && url.startsWith("blob:")) {
        try { URL.revokeObjectURL(url); } catch { /* ignore */ }
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [submission?.id, submission?.file_id, submission?.file_path, token]);

  return { url, loading, error };
}

/** 构建版本轮次卡片：只展示真实 submission_batches，不补造历史 */
export type ReviewVersionStatus = "pending" | "rework" | "final";
export type ReviewVersion = {
  id: string;
  versionNo: number;
  status: ReviewVersionStatus;
  submittedAt: string | null;
  reviewedAt: string | null;
  reviewComment: string | null;
  reviewedById: string | null;
  submissions: Submission[];
  isLatest: boolean;
};
export function buildReviewVersions(task: Task): ReviewVersion[] {
  const batches = [...(task.submission_batches || [])].sort(
    (a, b) => Number(a.version_no || 0) - Number(b.version_no || 0)
  );
  const allSubs = (task.submissions || []).filter((s) => !s.is_archived);
  if (!batches.length) {
    // 未制作任务（无提交记录）返回空数组，由调用方展示空状态
    if (!allSubs.length) return [];
    // 无批次但有提交时仍返回一轮「当前版本」，让当前候选可用
    return [{
      id: "latest",
      versionNo: 1,
      status: "pending",
      submittedAt: null,
      reviewedAt: null,
      reviewComment: null,
      reviewedById: null,
      submissions: allSubs,
      isLatest: true,
    }];
  }
  return batches.map((batch, index) => {
    const isLatest = index === batches.length - 1;
    const inBatch = allSubs.filter((s) => s.batch_id === batch.id);
    const status: ReviewVersionStatus =
      batch.status === "approved" ? "final"
      : batch.status === "rejected" ? "rework"
      : "pending";
    return {
      id: batch.id,
      versionNo: Number(batch.version_no || index + 1),
      status,
      submittedAt: batch.submitted_at || null,
      reviewedAt: batch.reviewed_at || null,
      reviewComment: batch.review_comment || null,
      reviewedById: batch.reviewed_by_id || null,
      submissions: inBatch.length ? inBatch : (isLatest ? allSubs : []),
      isLatest,
    };
  });
}
export function versionStatusLabel(status: ReviewVersionStatus): string {
  if (status === "final") return "已定版";
  if (status === "rework") return "已退回";
  return "当前版本";
}

/** 人物 A-E 图位 tab */
export const VIEW_SHOT_TABS: { key: string; label: string; hint: string; frames: number }[] = [
  { key: "A", label: "正面全身 A-Pose", hint: "A-Pose 定装参考", frames: 1 },
  { key: "B", label: "侧面/3/4 全身", hint: "侧面 3/4 全身定装", frames: 1 },
  { key: "C", label: "半身全身", hint: "半身/全身比例参考", frames: 2 },
  { key: "D", label: "面部与表情参考", hint: "面部细节 + 表情参考", frames: 1 },
  { key: "E", label: "服装与材质细节", hint: "服装与材质细节特写", frames: 1 },
];

/** 计算某个图位 tab 的图片数量（至少 1） */
export function shotTabImageCount(tab: { frames?: number } | undefined | null) {
  return Math.max(1, tab?.frames ?? 1);
}

/** 计算某个图位 tab 的图片起始 offset（按 VIEW_SHOT_TABS 顺序累加） */
export function shotTabImageOffset(tabKey: string | null | undefined) {
  if (!tabKey) return 0;
  let offset = 0;
  for (const tab of VIEW_SHOT_TABS) {
    if (tab.key === tabKey) return offset;
    offset += shotTabImageCount(tab);
  }
  return 0;
}

/** 快捷审阅意见标签，点击追加到 textarea */
export const QUICK_TAGS: { key: string; label: string }[] = [
  { key: "composition", label: "构图偏移" },
  { key: "perspective", label: "透视有误" },
  { key: "expression", label: "表情调整" },
  { key: "tone", label: "色调偏差" },
  { key: "motion", label: "动态不足" },
];

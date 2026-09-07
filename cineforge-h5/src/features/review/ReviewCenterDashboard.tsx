import React from "react";
import { createPortal } from "react-dom";
import { ChevronDown, ChevronLeft, ChevronRight, History, Maximize, Maximize2, Search, X, ZoomIn, ZoomOut } from "lucide-react";
import { OutlineSelect } from "../../shared/OutlineSelect";
import { Button } from "../../shared/components";
import type { Project, Submission, Task, WorkspaceResponse } from "../../types";
import {
  resolveBatch,
  candidatesOf,
  buildReviewVersions,
  versionStatusLabel,
  useMediaUrl,
  mediaKind,
  shortTitle,
  relativeTime,
  relativeTimeValue,
  formatDateTime,
  reviewAssetKind,
  REVIEW_ASSET_FILTERS,
  projectTitle,
  VIEW_SHOT_TABS,
  shotTabImageCount,
  shotTabImageOffset,
  QUICK_TAGS,
  type ReviewAssetKind,
  type ReviewVersion,
} from "./shared";

type Props = {
  workspace: WorkspaceResponse | null;
  projects: Project[];
  tasks?: Task[];
  projectId?: string | null;
  loading: boolean;
  canReview?: boolean;
  onReviewTask: (
    task: Task,
    batchId: string,
    decision: "approve" | "rework",
    primarySubmissionId: string | null,
    selectedSubmissionIds: string[],
    comment: string,
  ) => Promise<void>;
  // 定版：独立锁定主母版（不触发审核决策），对应后端 PATCH /tasks/{task_id}/primary-submission
  onSetPrimarySubmission?: (task: Task, submissionId: string) => Promise<void>;
};

const ASSET_KIND_LABELS: Record<ReviewAssetKind, string> = {
  character: "人物",
  scene: "场景",
  prop: "道具",
  music: "音乐",
  storyboard: "分镜",
  keyframe: "关键帧",
  other: "其他",
};

const QUEUE_PAGE_SIZE = 6;

function taskAssetCode(task: Task): string {
  return task.storyboard_code || task.asset_id || task.scene_code || task.id.slice(0, 8);
}

function latestBatchSubmittedAt(task: Task): string | null | undefined {
  const batches = task.submission_batches || [];
  if (!batches.length) return undefined;
  const sorted = [...batches].sort((a, b) => {
    const ta = a.submitted_at ? new Date(a.submitted_at).getTime() : 0;
    const tb = b.submitted_at ? new Date(b.submitted_at).getTime() : 0;
    return tb - ta;
  });
  return sorted[0].submitted_at;
}

function isTaskUrgent(task: Task): boolean {
  if (!task.due_at) return false;
  const due = new Date(task.due_at).getTime();
  if (!isFinite(due)) return false;
  return due - Date.now() < 24 * 60 * 60 * 1000;
}

function taskStatusMatches(task: Task, filter: "pending" | "approve" | "rework"): boolean {
  const status = String(task.status || "");
  if (filter === "pending") return status === "submitted" || status === "reviewing";
  if (filter === "approve") return status === "approved";
  if (filter === "rework") return status === "rejected";
  return true;
}

function QueueThumb({ task }: { task: Task }) {
  const batch = resolveBatch(task);
  const subs = candidatesOf(task, batch);
  const thumbSub = subs[0] || null;
  const result = useMediaUrl(thumbSub);
  if (result.url && mediaKind(thumbSub) === "image") {
    return <img src={result.url} alt="" loading="lazy" />;
  }
  return <i aria-hidden="true" />;
}

function MediaGridItem({
  submission,
  index,
  isSelected,
  onSelect,
  onDoubleClick,
}: {
  submission: Submission;
  index: number;
  isSelected: boolean;
  onSelect: () => void;
  onDoubleClick: () => void;
}) {
  const result = useMediaUrl(submission);
  const kind = mediaKind(submission);
  return (
    <div
      className={`media-cell ${isSelected ? "is-selected" : ""}`}
      onClick={onSelect}
      onDoubleClick={onDoubleClick}
      style={{
        position: "relative",
        borderRadius: 8,
        overflow: "hidden",
        border: isSelected ? "2px solid #5b8def" : "2px solid transparent",
        cursor: "pointer",
        background: "#050709",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        minHeight: 180,
      }}
    >
      {result.loading ? (
        <span style={{ color: "#6b7280" }}>加载中…</span>
      ) : result.url && kind === "image" ? (
        <img
          src={result.url}
          alt={submission.file_name || `候选 ${index + 1}`}
          style={{ width: "100%", height: "100%", objectFit: "contain" }}
        />
      ) : result.url && kind === "video" ? (
        <video
          src={result.url}
          muted
          style={{ width: "100%", height: "100%", objectFit: "contain" }}
        />
      ) : result.url && kind === "audio" ? (
        <span style={{ fontSize: 32 }}>🎵</span>
      ) : (
        <span style={{ color: "#6b7280" }}>无预览</span>
      )}
      <span
        className="review-center-tag"
        style={{
          position: "absolute",
          top: 8,
          left: 8,
          background: isSelected ? "#5b8def" : "rgba(0,0,0,0.6)",
          color: "#fff",
        }}
      >
        {index + 1}
      </span>
      {isSelected ? (
        <span
          className="review-center-tag is-ok"
          style={{
            position: "absolute",
            bottom: 8,
            right: 8,
            background: "rgba(15,157,88,0.85)",
            color: "#fff",
          }}
        >
          主母版
        </span>
      ) : null}
    </div>
  );
}

export function ReviewCenterDashboard({ workspace, projects, tasks: allTasksProp, projectId, loading, canReview = false, onReviewTask, onSetPrimarySubmission }: Props) {
  const [selectedProject, setSelectedProject] = React.useState<string>(projectId ?? "all");
  const [selectedEpisode, setSelectedEpisode] = React.useState<string>("all");
  const [selectedAssetFilter, setSelectedAssetFilter] = React.useState<ReviewAssetKind | "all">("all");
  const [searchQuery, setSearchQuery] = React.useState("");
  const [selectedTaskId, setSelectedTaskId] = React.useState<string | null>(null);
  const [currentVersionIndex, setCurrentVersionIndex] = React.useState<number>(0);
  const [currentVariantKey, setCurrentVariantKey] = React.useState<string | null>("A");
  const [selectedSubmissionIndex, setSelectedSubmissionIndex] = React.useState<number>(0);
  const [primarySubmissionId, setPrimarySubmissionId] = React.useState<string | null>(null);
  const [selectedSubmissionIds, setSelectedSubmissionIds] = React.useState<string[]>([]);
  const [comment, setComment] = React.useState("");
  const [lightboxOpen, setLightboxOpen] = React.useState<boolean>(false);
  const [lightboxZoom, setLightboxZoom] = React.useState<number>(1);
  const [zoomMode, setZoomMode] = React.useState<"fit" | "manual">("fit");
  const [submitting, setSubmitting] = React.useState<boolean>(false);
  const [statusFilter, setStatusFilter] = React.useState<"all" | "pending" | "approve" | "rework">("all");
  const [urgencyFilter, setUrgencyFilter] = React.useState<"all" | "urgent" | "normal">("all");
  const [queueTab, setQueueTab] = React.useState<"pending" | "done">("pending");
  const [queuePage, setQueuePage] = React.useState<number>(1);
  const [titleExtraHost, setTitleExtraHost] = React.useState<HTMLElement | null>(null);

  React.useEffect(() => {
    setSelectedProject(projectId ?? "all");
  }, [projectId]);

  React.useEffect(() => {
    setTitleExtraHost(document.getElementById("admin-title-extra"));
  }, []);

  React.useEffect(() => {
    setQueuePage(1);
  }, [queueTab, statusFilter, urgencyFilter, selectedAssetFilter, selectedProject, selectedEpisode, searchQuery]);

  const allTasks = React.useMemo(() => {
    // Priority order:
    // 1) Use reviewWorkspace (from /workspace/reviews) if it has data — this is the
    //    authoritative backend-filtered review list (only tasks with submission_batches
    //    in submitted/approved/rework states).
    const reviewTodo = workspace?.groups?.todo ?? [];
    const reviewCompleted = workspace?.groups?.completed ?? [];
    const reviewMerged = [...reviewTodo, ...reviewCompleted];
    if (reviewMerged.length > 0) return reviewMerged;

    // 2) Fallback: build review data from the global tasks list.
    //    Include ALL non-retired tasks so the review center always has content
    //    to display, even when the strict /workspace/reviews filter returns empty.
    if (allTasksProp && allTasksProp.length > 0) {
      const filtered = allTasksProp.filter((task) => {
        if (task.is_retired) return false;
        return true;
      });
      console.log("[ReviewCenterDashboard] Fallback: built review data from tasks, count =", filtered.length);
      return filtered;
    }
    return [];
  }, [workspace, allTasksProp]);

  const todoTasks = React.useMemo(() => {
    const wsTodo = workspace?.groups?.todo ?? [];
    if (wsTodo.length > 0) return wsTodo;
    if (allTasksProp && allTasksProp.length > 0) {
      return allTasksProp.filter((task) => {
        if (task.is_retired) return false;
        const status = String(task.status || "");
        // Non-completed tasks — show everything that isn't done
        return status !== "completed";
      });
    }
    return [];
  }, [workspace, allTasksProp]);

  const doneTasks = React.useMemo(() => {
    const wsDone = workspace?.groups?.completed ?? [];
    if (wsDone.length > 0) return wsDone;
    if (allTasksProp && allTasksProp.length > 0) {
      return allTasksProp.filter((task) => {
        if (task.is_retired) return false;
        const status = String(task.status || "");
        return status === "completed";
      });
    }
    return [];
  }, [workspace, allTasksProp]);

  const episodeOptions = React.useMemo(() => {
    const codes = new Set<string>();
    allTasks.forEach((task) => {
      if (selectedProject !== "all" && task.project_id !== selectedProject) return;
      if (task.episode_code) codes.add(task.episode_code);
    });
    const opts = [{ value: "all", label: "全部分集" }];
    Array.from(codes).sort().forEach((code) => opts.push({ value: code, label: code }));
    return opts;
  }, [allTasks, selectedProject]);

  const projectOptions = React.useMemo(() => {
    const opts = [{ value: "all", label: "全部项目" }];
    projects.forEach((p) => opts.push({ value: p.id, label: p.title }));
    return opts;
  }, [projects]);

  const filteredTodo = React.useMemo(() => {
    return todoTasks.filter((task) => {
      if (selectedProject !== "all" && task.project_id !== selectedProject) return false;
      if (selectedEpisode !== "all" && task.episode_code !== selectedEpisode) return false;
      if (selectedAssetFilter !== "all" && reviewAssetKind(task) !== selectedAssetFilter) return false;
      if (statusFilter !== "all" && !taskStatusMatches(task, statusFilter as "pending" | "approve" | "rework")) return false;
      if (urgencyFilter !== "all") {
        const urgent = isTaskUrgent(task);
        if (urgencyFilter === "urgent" && !urgent) return false;
        if (urgencyFilter === "normal" && urgent) return false;
      }
      if (searchQuery.trim()) {
        const q = searchQuery.trim().toLowerCase();
        const haystacks = [
          taskAssetCode(task).toLowerCase(),
          (task.title || "").toLowerCase(),
          (task.variant_title_zh || "").toLowerCase(),
          (task.variant_description_zh || "").toLowerCase(),
          (task.assignee_name || "").toLowerCase(),
          (task.storyboard_code || "").toLowerCase(),
          (task.scene_code || "").toLowerCase(),
          (task.asset_id || "").toLowerCase(),
        ];
        const prompt = (task.latest_prompt_text || task.prompt_text || "").toLowerCase();
        if (prompt) haystacks.push(prompt);
        const matched = haystacks.some((h) => h.includes(q));
        if (!matched) return false;
      }
      return true;
    });
  }, [todoTasks, selectedProject, selectedEpisode, selectedAssetFilter, statusFilter, urgencyFilter, searchQuery]);

  const countBaseTodo = React.useMemo(() => {
    return todoTasks.filter((task) => {
      if (selectedProject !== "all" && task.project_id !== selectedProject) return false;
      if (selectedEpisode !== "all" && task.episode_code !== selectedEpisode) return false;
      if (statusFilter !== "all" && !taskStatusMatches(task, statusFilter as "pending" | "approve" | "rework")) return false;
      if (urgencyFilter !== "all") {
        const urgent = isTaskUrgent(task);
        if (urgencyFilter === "urgent" && !urgent) return false;
        if (urgencyFilter === "normal" && urgent) return false;
      }
      if (searchQuery.trim()) {
        const q = searchQuery.trim().toLowerCase();
        const haystacks = [
          taskAssetCode(task).toLowerCase(),
          (task.title || "").toLowerCase(),
          (task.variant_title_zh || "").toLowerCase(),
          (task.variant_description_zh || "").toLowerCase(),
          (task.assignee_name || "").toLowerCase(),
          (task.storyboard_code || "").toLowerCase(),
          (task.scene_code || "").toLowerCase(),
          (task.asset_id || "").toLowerCase(),
        ];
        const prompt = (task.latest_prompt_text || task.prompt_text || "").toLowerCase();
        if (prompt) haystacks.push(prompt);
        const matched = haystacks.some((h) => h.includes(q));
        if (!matched) return false;
      }
      return true;
    });
  }, [todoTasks, selectedProject, selectedEpisode, statusFilter, urgencyFilter, searchQuery]);

  const filteredDone = React.useMemo(() => {
    return doneTasks.filter((task) => {
      if (selectedProject !== "all" && task.project_id !== selectedProject) return false;
      if (selectedEpisode !== "all" && task.episode_code !== selectedEpisode) return false;
      if (selectedAssetFilter !== "all" && reviewAssetKind(task) !== selectedAssetFilter) return false;
      if (searchQuery.trim()) {
        const q = searchQuery.trim().toLowerCase();
        const haystacks = [
          taskAssetCode(task).toLowerCase(),
          (task.title || "").toLowerCase(),
          (task.variant_title_zh || "").toLowerCase(),
          (task.assignee_name || "").toLowerCase(),
          (task.storyboard_code || "").toLowerCase(),
          (task.scene_code || "").toLowerCase(),
          (task.asset_id || "").toLowerCase(),
        ];
        const matched = haystacks.some((h) => h.includes(q));
        if (!matched) return false;
      }
      return true;
    });
  }, [doneTasks, selectedProject, selectedEpisode, selectedAssetFilter, searchQuery]);

  React.useEffect(() => {
    const source = queueTab === "pending" ? filteredTodo : filteredDone;
    // 默认选中：1) 当前没有选中任务；或 2) 当前选中任务不在当前数据源中（如数据刷新后旧ID消失）；
    // 且当源有数据时执行。用户需求：进入页面待审任务有数据则默认选中第1条。
    const needAutoSelect =
      !selectedTaskId ||
      !source.some((t) => t.id === selectedTaskId);
    if (needAutoSelect && source.length > 0) {
      setSelectedTaskId(source[0].id);
    } else if (needAutoSelect && source.length === 0) {
      // 当前队列已清空，同步清除选中态避免右侧显示旧任务
      setSelectedTaskId(null);
    }
  }, [filteredTodo, filteredDone, queueTab, selectedTaskId]);

  const totalQueuePages = React.useMemo(() => {
    const source = queueTab === "pending" ? filteredTodo : filteredDone;
    return Math.max(1, Math.ceil(source.length / QUEUE_PAGE_SIZE));
  }, [queueTab, filteredTodo, filteredDone]);

  const pagedQueueRows = React.useMemo(() => {
    const source = queueTab === "pending" ? filteredTodo : filteredDone;
    const start = (queuePage - 1) * QUEUE_PAGE_SIZE;
    return source.slice(start, start + QUEUE_PAGE_SIZE);
  }, [queueTab, filteredTodo, filteredDone, queuePage]);

  const selectedTask = React.useMemo(() => {
    if (!selectedTaskId) return null;
    return allTasks.find((t) => t.id === selectedTaskId) || null;
  }, [selectedTaskId, allTasks]);

  const effectiveTask = React.useMemo(() => {
    return selectedTask || null;
  }, [selectedTask]);

  const versions = React.useMemo(() => {
    if (!effectiveTask) return [];
    return buildReviewVersions(effectiveTask);
  }, [effectiveTask]);

  const currentVersion: ReviewVersion | null = React.useMemo(() => {
    if (!versions.length) return null;
    const idx = Math.min(currentVersionIndex, versions.length - 1);
    return versions[idx] || null;
  }, [versions, currentVersionIndex]);

  const currentBatch = React.useMemo(() => {
    if (!effectiveTask) return null;
    return resolveBatch(effectiveTask);
  }, [effectiveTask]);

  const isCharacterTask = effectiveTask ? reviewAssetKind(effectiveTask) === "character" : false;
  const mediaType = String(effectiveTask?.media_type || "").toLowerCase();
  const isVideoTask = mediaType === "video" || effectiveTask?.task_type === "image_to_video";
  const showShotTabs = isCharacterTask && !isVideoTask;
  const stageProduced = effectiveTask ? (effectiveTask.submissions || []).length > 0 : false;

  const activeShotTab = React.useMemo(() => {
    return VIEW_SHOT_TABS.find((tab) => tab.key === currentVariantKey) || VIEW_SHOT_TABS[0];
  }, [currentVariantKey]);

  const versionSubmissions = React.useMemo(() => {
    if (!effectiveTask) return [] as Submission[];
    if (currentVersion) return currentVersion.submissions;
    const batch = currentBatch;
    return candidatesOf(effectiveTask, batch);
  }, [effectiveTask, currentVersion, currentBatch]);

  const currentSubmissions = React.useMemo(() => {
    if (!versionSubmissions.length) return [] as Submission[];
    if (!showShotTabs) return versionSubmissions;
    const tabFrameCount = shotTabImageCount(activeShotTab);
    const tabFrameOffset = shotTabImageOffset(activeShotTab?.key);
    return Array.from({ length: tabFrameCount }, (_, index) => {
      const slot = tabFrameOffset + index;
      return versionSubmissions[slot] || versionSubmissions[slot % versionSubmissions.length];
    });
  }, [versionSubmissions, showShotTabs, activeShotTab]);

  const lightboxSubmission = currentSubmissions[selectedSubmissionIndex] ?? null;
  const lightboxMedia = useMediaUrl(lightboxSubmission);

  React.useEffect(() => {
    setCurrentVersionIndex(Math.max(0, versions.length - 1));
    setCurrentVariantKey("A");
    setSelectedSubmissionIndex(0);
    setZoomMode("fit");
    setLightboxZoom(1);
  }, [selectedTaskId]);

  React.useEffect(() => {
    setSelectedSubmissionIndex(0);
    if (currentSubmissions.length) {
      const primary = currentSubmissions.find((s) => s.is_primary);
      setPrimarySubmissionId(primary?.id ?? currentSubmissions[0]?.id ?? null);
    } else {
      setPrimarySubmissionId(null);
    }
  }, [currentVersionIndex, currentVariantKey]);

  const variantTaskMap = React.useMemo(() => {
    const map = new Map<string, Task>();
    if (!selectedTask) return map;
    // 按 submissions 的总长度判断每个 tab 是否有对应的数据（不需要独立 variantTask）
    const total = versionSubmissions.length;
    VIEW_SHOT_TABS.forEach((tab) => {
      const offset = shotTabImageOffset(tab.key);
      if (offset < total) map.set(tab.key, selectedTask);
    });
    return map;
  }, [selectedTask, versionSubmissions]);

  const goNextTodo = React.useCallback(() => {
    if (!filteredTodo.length) {
      setSelectedTaskId(null);
      return;
    }
    const currentIdx = filteredTodo.findIndex((t) => t.id === selectedTaskId);
    if (currentIdx === -1 || currentIdx >= filteredTodo.length - 1) {
      setSelectedTaskId(filteredTodo[0]?.id ?? null);
    } else {
      setSelectedTaskId(filteredTodo[currentIdx + 1].id);
    }
  }, [filteredTodo, selectedTaskId]);

  const handleDecision = async (decision: "approve" | "rework") => {
    if (!effectiveTask || !currentVersion || submitting) return;
    const batchId = currentVersion.id !== "latest" ? currentVersion.id : currentBatch?.id || "";
    if (!batchId) return;

    let finalPrimaryId = primarySubmissionId;
    let finalSelectedIds = [...selectedSubmissionIds];

    if (decision === "approve") {
      if (!finalPrimaryId) return;
      if (!currentSubmissions.some((s) => s.id === finalPrimaryId)) return;
      if (finalSelectedIds.length === 0) {
        finalSelectedIds = currentSubmissions.map((s) => s.id);
      }
    } else {
      finalSelectedIds = [];
    }

    try {
      setSubmitting(true);
      await onReviewTask(
        effectiveTask,
        batchId,
        decision,
        decision === "approve" ? finalPrimaryId : null,
        finalSelectedIds,
        comment,
      );
      goNextTodo();
    } finally {
      setSubmitting(false);
    }
  };

  const toggleSelectedSubmission = (id: string) => {
    setSelectedSubmissionIds((prev) => {
      if (prev.includes(id)) return prev.filter((x) => x !== id);
      return [...prev, id];
    });
  };

  const appendQuickTag = (label: string) => {
    const tag = `【${label}】`;
    setComment((prev) => (prev ? `${prev} ${tag}` : tag));
  };

  const canApprove = Boolean(primarySubmissionId && !submitting && currentSubmissions.some((s) => s.id === primarySubmissionId));

  const gridCols = React.useMemo(() => {
    const n = currentSubmissions.length;
    if (n <= 1) return "1fr";
    if (n <= 2) return "1fr 1fr";
    if (n <= 4) return "1fr 1fr";
    return "1fr 1fr 1fr";
  }, [currentSubmissions.length]);

  const specText = React.useMemo(() => {
    if (!effectiveTask) return "";
    const parts: string[] = [];
    if (effectiveTask.variant_title_zh) parts.push(`【角色设定】${effectiveTask.variant_title_zh}`);
    if (effectiveTask.variant_description_zh) parts.push(effectiveTask.variant_description_zh);
    const prompt =
      effectiveTask.latest_prompt_text ||
      (currentSubmissions[0]?.revised_prompt_text ||
        currentSubmissions[0]?.prompt_text ||
        effectiveTask.prompt_text ||
        "");
    if (prompt) parts.push(`\n【提示词】${prompt}`);
    if (effectiveTask.storyboard_dialogue) parts.push(`\n【对白】${effectiveTask.storyboard_dialogue}`);
    return parts.join("\n");
  }, [effectiveTask, currentSubmissions]);

  const COMMENT_MAX = 200;

  type ReviewAssetKindLabel = "人物" | "分镜" | "道具" | "场景" | "音乐" | "其他";

  const reviewAssetKindLabel = (kind: string): ReviewAssetKindLabel => {
    switch (kind) {
      case "character":
        return "人物";
      case "storyboard":
        return "分镜";
      case "prop":
        return "道具";
      case "scene":
        return "场景";
      case "music":
        return "音乐";
      default:
        return "其他";
    }
  };

  // 与新版 UI 对应的制作要求 references 数据
  const references = React.useMemo(() => {
    const empty = {
      brief: "",
      phaseLabel: "制作要求",
      visual: "",
      camera: "",
      shotSize: "",
      dialogue: "",
      prompt: "",
      characters: "",
    };
    if (!effectiveTask) return empty;
    const assetKind = reviewAssetKind(effectiveTask);
    const phaseLabel = isVideoTask
      ? assetKind === "character"
        ? "人物视频要求"
        : "视频制作要求"
      : assetKind === "character"
        ? "人物定装要求"
        : assetKind === "storyboard" || assetKind === "keyframe"
          ? "分镜关键帧要求"
          : "关键帧制作要求";
    // 画面要求：优先取变体描述 → 场景名 → 任务标题 → specText 兜底
    const visual =
      effectiveTask.variant_description_zh
      || effectiveTask.scene_name
      || effectiveTask.title
      || specText;
    // 制作提示词：优先取任务级 prompt → 最新 prompt → 当前候选 prompt → 画面要求兜底
    const prompt =
      effectiveTask.prompt_text
      || effectiveTask.latest_prompt_text
      || currentSubmissions[0]?.prompt_text
      || visual;
    // 出场角色：取变体中文名（人物任务）
    const characters = effectiveTask.variant_title_zh || "";
    // 对白：取分镜对白原文或回译
    const dialogue =
      effectiveTask.storyboard_dialogue
      || effectiveTask.storyboard_dialogue_back_translation
      || "";
    // 镜头/景别：从 storyboard_mirror_shots 首个镜头取（TaskRead 已嵌入 mirror_shots）
    const firstMirror = effectiveTask.storyboard_mirror_shots?.[0];
    const camera = firstMirror?.camera || "";
    const shotSize = firstMirror?.shot_size || "";
    return {
      ...empty,
      brief: visual,
      phaseLabel,
      visual,
      camera,
      shotSize,
      prompt,
      characters,
      dialogue,
    };
  }, [effectiveTask, isVideoTask, specText, currentSubmissions]);

  const characterTags = React.useMemo(() => {
    // 优先从 references.characters（出场角色）拆出标签
    if (references.characters) {
      const fromRefs = references.characters
        .split(/[、,，/|]+/u)
        .map((s) => s.trim())
        .filter(Boolean);
      if (fromRefs.length) return fromRefs;
    }
    if (!effectiveTask || !isCharacterTask) return [];
    const tags: string[] = [];
    if (effectiveTask.variant_title_zh) tags.push(effectiveTask.variant_title_zh);
    return tags;
  }, [references.characters, effectiveTask, isCharacterTask]);

  const taskResult: "approve" | "rework" | null = React.useMemo(() => {
    if (!effectiveTask) return null;
    const status = String(effectiveTask.status || "").toLowerCase();
    if (status === "approved" || status === "approve" || status === "final") return "approve";
    if (status === "rework" || status === "rejected" || status === "returned") return "rework";
    return null;
  }, [effectiveTask]);

  // 历史记录：从 submission_batches 真实事件推导，不再伪造“修改后重新提交”等记录
  const taskHistory = React.useMemo(() => {
    type HistoryRow = { title: string; user: string; time: string; tone: "ok" | "info" | "warn"; idle?: boolean };
    if (!effectiveTask) return [] as HistoryRow[];
    const rows: HistoryRow[] = [];
    const maker = effectiveTask.assignee_name || "制作人员";
    // 任务创建（真实 created_at）
    if (effectiveTask.created_at) {
      rows.push({ title: "创建任务", user: "系统", time: relativeTime(effectiveTask.created_at), tone: "info" });
    }
    // 按 submitted_at 时间线还原每个批次的“提交审核 → 审核结论”
    const batches = [...(effectiveTask.submission_batches || [])].sort((a, b) => {
      const ta = a.submitted_at ? new Date(a.submitted_at).getTime() : 0;
      const tb = b.submitted_at ? new Date(b.submitted_at).getTime() : 0;
      return ta - tb;
    });
    let hasReview = false;
    for (const batch of batches) {
      if (batch.submitted_at) {
        const ver = batch.version_no ? ` v${batch.version_no}` : "";
        rows.push({
          title: `提交审核${ver}`,
          user: maker,
          time: relativeTime(batch.submitted_at),
          tone: "ok",
        });
      }
      if (batch.reviewed_at) {
        hasReview = true;
        // TODO(审批中心-后端接口需求文档): reviewed_by_id 仅有用户 ID，无 reviewed_by_name，
        //   需后端在 SubmissionBatch 返回 reviewed_by_name 后替换此处占位。
        const isApprove = batch.status === "approved";
        const isReject = batch.status === "rejected";
        rows.push({
          title: isApprove ? "审核通过" : isReject ? "退回修改" : "审核意见",
          user: "审核人",
          time: relativeTime(batch.reviewed_at),
          tone: isApprove ? "ok" : isReject ? "warn" : "info",
        });
      }
    }
    // 当前任务已提交但尚无审核结论 → 展示“等待审核”占位
    if (!hasReview && taskResult === null && effectiveTask.status === "submitted") {
      rows.push({ title: "等待审核", user: "项目负责人", time: "—", tone: "info", idle: true });
    }
    return rows;
  }, [effectiveTask, taskResult]);
  const previewHistory = React.useMemo(() => taskHistory.slice(0, 3), [taskHistory]);
  const [historyOpen, setHistoryOpen] = React.useState<boolean>(false);

  return (
    <div className="review-hub review-hub--page">
      {titleExtraHost
        ? createPortal(
            <div className="review-hub__topbar-tools">
              <OutlineSelect
                value={selectedProject}
                options={projectOptions}
                onChange={(v) => { setSelectedProject(v); setSelectedEpisode("all"); }}
                ariaLabel="项目筛选"
                placeholder="全部项目"
              />
              <label className="review-hub__search">
                <Search aria-hidden="true" size={15} className="icon" />
                <input
                  type="text"
                  placeholder="搜索人物编号/角色/场景/道具/分镜/提交人等"
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                />
              </label>
            </div>,
            titleExtraHost,
          )
        : null}

      <div className="review-hub__filters">
        <div className="review-hub__type-tabs" role="tablist" aria-label="资产类型筛选">
          {REVIEW_ASSET_FILTERS.map((filter) => {
            const count =
              filter.key === "all"
                ? countBaseTodo.length
                : countBaseTodo.filter((t) => reviewAssetKind(t) === filter.key).length;
            return (
              <button
                key={filter.key}
                role="tab"
                type="button"
                aria-selected={selectedAssetFilter === filter.key}
                className={selectedAssetFilter === filter.key ? "is-active" : ""}
                onClick={() => setSelectedAssetFilter(filter.key)}
              >
                {filter.label}
                <b className="cnt">{count}</b>
              </button>
            );
          })}
        </div>
        <div className="review-hub__filter-selects">
          <OutlineSelect
            value={statusFilter}
            onChange={(v) => setStatusFilter(v as "all" | "pending" | "approve" | "rework")}
            options={[
              { value: "all", label: "全部状态" },
              { value: "pending", label: "待审批" },
              { value: "approve", label: "已通过" },
              { value: "rework", label: "已退回" },
            ]}
            ariaLabel="状态筛选"
          />
          <OutlineSelect
            value={urgencyFilter}
            onChange={(v) => setUrgencyFilter(v as "all" | "urgent" | "normal")}
            options={[
              { value: "all", label: "紧急程度" },
              { value: "urgent", label: "紧急" },
              { value: "normal", label: "普通" },
            ]}
            ariaLabel="紧急程度筛选"
          />
        </div>
      </div>

      <div className="review-hub__workspace">
        <aside className="review-hub__queue">
          <div className="review-hub__queue-tabs">
            <button
              type="button"
              className={queueTab === "pending" ? "is-active" : ""}
              onClick={() => { setQueueTab("pending"); setQueuePage(1); }}
            >
              待审任务 <b className="cnt">{filteredTodo.length}</b>
            </button>
            <button
              type="button"
              className={queueTab === "done" ? "is-active" : ""}
              onClick={() => { setQueueTab("done"); setQueuePage(1); }}
            >
              已处理 <b className="cnt">{filteredDone.length}</b>
            </button>
          </div>

          <div className="review-hub__queue-list">
            {loading ? (
              <div className="review-center-empty">加载中…</div>
            ) : pagedQueueRows.length === 0 ? (
              <div className="review-center-empty">暂无任务</div>
            ) : (
              <>
                {pagedQueueRows.map((task) => {
                const kind = reviewAssetKind(task);
                const assetCode = taskAssetCode(task);
                const active = task.id === selectedTaskId;
                const urgent = isTaskUrgent(task);
                const done = (task.submissions || []).length > 0;
                const status = String(task.status || "");
                const isApproved = status === "approved";
                const isRework = status === "rejected";
                const mediaType = String(task.media_type || "").toLowerCase();
                const phaseLabel = mediaType === "video" || task.task_type === "image_to_video" ? "视频" : "关键帧";
                const projectLabel = projectTitle(projects, task.project_id);
                const episodeLabel = task.episode_code || "EP01";
                const makerName = task.assignee_name || "—";
                const submittedAt = relativeTime(task.created_at);
                return (
                  <button
                    key={task.id}
                    type="button"
                    className={`review-hub__card ${active ? "is-active" : ""}`}
                    onClick={() => setSelectedTaskId(task.id)}
                  >
                    <span className="review-hub__card-thumb">
                      <QueueThumb task={task} />
                      <em className={`is-${kind}`}>{ASSET_KIND_LABELS[kind]}</em>
                    </span>
                    <span className="review-hub__card-body">
                      <span className="review-hub__card-main">
                        <span className="review-hub__card-title">
                          <strong className="review-hub__card-title-text">
                            {task.variant_title_zh || task.title || "未命名任务"}
                          </strong>
                        </span>
                        <span className="review-hub__card-foot">
                          <span className="review-hub__card-project">
                            {projectLabel} · {episodeLabel}
                          </span>
                          <span className="review-hub__card-maker">
                            <span>{makerName}</span>
                            <span>{submittedAt}</span>
                          </span>
                        </span>
                      </span>
                      <span className="review-hub__card-rail">
                        {isApproved ? (
                          <span className="review-hub__op-status is-approve">已过审</span>
                        ) : isRework ? (
                          <span className="review-hub__op-status is-rework">已退回</span>
                        ) : urgent ? (
                          <span className="review-center-tag is-urgent">紧急</span>
                        ) : (
                          <i aria-hidden="true" />
                        )}
                        <span className={`review-hub__produce is-${done ? "done" : "todo"}`}>
                          {done ? "已制作" : "未制作"}
                        </span>
                      </span>
                    </span>
                  </button>
                )
              })}
                {!loading && pagedQueueRows.length > 0 && pagedQueueRows.length < QUEUE_PAGE_SIZE &&
                  Array.from({ length: QUEUE_PAGE_SIZE - pagedQueueRows.length }).map((_, i) => (
                    <div key={`spacer-${i}`} className="review-hub__card-spacer" aria-hidden="true" />
                  ))}
              </>
            )}
          </div>

          {pagedQueueRows.length > 0 ? (
            <div className="review-hub__pager">
              <button
                type="button"
                disabled={queuePage <= 1}
                onClick={() => setQueuePage((p) => Math.max(1, p - 1))}
                aria-label="上一页"
              >
                <ChevronLeft size={14} strokeWidth={2.2} />
              </button>
              <span>
                {queuePage} / {totalQueuePages}
              </span>
              <button
                type="button"
                disabled={queuePage >= totalQueuePages}
                onClick={() => setQueuePage((p) => Math.min(totalQueuePages, p + 1))}
                aria-label="下一页"
              >
                <ChevronRight size={14} strokeWidth={2.2} />
              </button>
            </div>
          ) : null}
        </aside>

        <section className="review-hub__stage">
          {!selectedTask || !effectiveTask ? (
            <div className="review-hub__empty-stage">
              <div className="review-center-empty">请选择待审任务</div>
            </div>
          ) : (
            <>
              {(() => {
                const stageMediaType = String(effectiveTask!.media_type || "").toLowerCase();
                const phaseLabel = stageMediaType === "video" || effectiveTask!.task_type === "image_to_video" ? "视频" : "关键帧";
                return (
                  <>
                    <div className="review-hub__stage-head">
                      <div className="review-hub__stage-title">
                        <strong>
                          {effectiveTask!.variant_title_zh || effectiveTask!.title || taskAssetCode(effectiveTask!)}
                        </strong>
                        <span className="review-center-stage__tags">
                          <span className={`review-center-tag is-kind is-${reviewAssetKind(effectiveTask!)}`}>
                            {ASSET_KIND_LABELS[reviewAssetKind(effectiveTask!)]}
                          </span>
                          {isTaskUrgent(effectiveTask!) ? (
                            <span className="review-center-tag is-urgent">紧急</span>
                          ) : null}
                          <span className={`review-hub__produce is-${stageProduced ? "done" : "todo"}`}>
                            {stageProduced ? "已制作" : "未制作"}
                          </span>
                        </span>
                      </div>
                      <div className="review-hub__stage-meta">
                        <span>提交人：{effectiveTask!.assignee_name || "—"}</span>
                        <span>提交时间：{relativeTimeValue(effectiveTask!.created_at)}</span>
                      </div>
                    </div>
                    {stageProduced ? (
                      <>
                        {showShotTabs ? (
                      <div className="review-hub__shot-row">
                        <div className="review-hub__shot-tabs" role="tablist" aria-label="人物图位">
                          {VIEW_SHOT_TABS.map((tab) => {
                            const hasVariant = variantTaskMap.has(tab.key);
                            const isActive = currentVariantKey === tab.key;
                            return (
                              <button
                                key={tab.key}
                                role="tab"
                                type="button"
                                aria-selected={isActive}
                                title={tab.hint}
                                disabled={!hasVariant}
                                className={isActive ? "is-active" : ""}
                                onClick={() => setCurrentVariantKey(tab.key)}
                              >
                                <span className="review-hub__shot-tabs-label">{tab.label}</span>
                                {tab.frames >= 2 ? <em>{tab.frames}帧</em> : null}
                              </button>
                            );
                          })}
                        </div>
                        <div className="review-hub__viewer-tools" aria-label="预览工具">
                          <button
                            type="button"
                            className={`review-hub__viewer-fit${zoomMode === "fit" ? " is-active" : ""}`}
                            aria-label="适应容器"
                            aria-pressed={zoomMode === "fit"}
                            data-tip="适应当前容器尺寸"
                            onClick={() => {
                              setZoomMode("fit");
                              setLightboxZoom(1);
                            }}
                          >
                            <Maximize size={15} strokeWidth={1.8} />
                          </button>
                          <button type="button" title="缩小" aria-label="缩小"
                            onClick={() => {
                              setZoomMode("manual");
                              setLightboxZoom((z) => Math.max(z - 0.25, 0.25));
                            }}>
                            <ZoomOut size={15} />
                          </button>
                          <span className={`review-hub__viewer-zoom-label${zoomMode === "manual" && lightboxZoom === 1 ? " is-active" : ""}`}>
                            {Math.round(lightboxZoom * 100)}%
                          </span>
                          <button type="button" title="放大" aria-label="放大"
                            onClick={() => {
                              setZoomMode("manual");
                              setLightboxZoom((z) => Math.min(z + 0.25, 4));
                            }}>
                            <ZoomIn size={15} />
                          </button>
                          <button type="button" title="全屏查看" aria-label="全屏查看"
                            onClick={() => { setLightboxOpen(true); setLightboxZoom(1); }}>
                            <Maximize2 size={15} />
                          </button>
                        </div>
                      </div>
                    ) : (
                      <div className="review-hub__shot-row is-plain">
                        <div className="review-hub__shot-plain">
                          <span>{isVideoTask ? "视频" : "素材"}</span>
                          {versionSubmissions.length > 1 ? <em>{versionSubmissions.length} 候选</em> : null}
                        </div>
                        <div className="review-hub__viewer-tools" aria-label="预览工具">
                          <button
                            type="button"
                            className={`review-hub__viewer-fit${zoomMode === "fit" ? " is-active" : ""}`}
                            aria-label="适应容器"
                            aria-pressed={zoomMode === "fit"}
                            data-tip="适应当前容器尺寸"
                            onClick={() => {
                              setZoomMode("fit");
                              setLightboxZoom(1);
                            }}
                          >
                            <Maximize size={15} strokeWidth={1.8} />
                          </button>
                          <button type="button" title="缩小" aria-label="缩小"
                            onClick={() => {
                              setZoomMode("manual");
                              setLightboxZoom((z) => Math.max(z - 0.25, 0.25));
                            }}>
                            <ZoomOut size={15} />
                          </button>
                          <span className={`review-hub__viewer-zoom-label${zoomMode === "manual" && lightboxZoom === 1 ? " is-active" : ""}`}>
                            {Math.round(lightboxZoom * 100)}%
                          </span>
                          <button type="button" title="放大" aria-label="放大"
                            onClick={() => {
                              setZoomMode("manual");
                              setLightboxZoom((z) => Math.min(z + 0.25, 4));
                            }}>
                            <ZoomIn size={15} />
                          </button>
                          <button type="button" title="全屏查看" aria-label="全屏查看"
                            onClick={() => { setLightboxOpen(true); setLightboxZoom(1); }}>
                            <Maximize2 size={15} />
                          </button>
                        </div>
                      </div>
                    )}
                      </>
                    ) : null}
                  </>
                );
              })()}

              <div className="review-hub__viewer">
                <div className="review-hub__viewer-stage">
                  {!stageProduced ? (
                    <div className="review-hub__empty-stage">
                      <strong>未开始制作</strong>
                      <span>该任务尚未提交候选内容，制作完成后再进行审核</span>
                    </div>
                  ) : currentSubmissions.length === 0 ? (
                    <div className="review-hub__empty-stage">
                      {showShotTabs && currentVariantKey ? "当前图位暂无图片" : "暂无媒体内容"}
                    </div>
                  ) : (
                    <>
                      <div
                        className="media-grid-wrapper"
                        style={{
                          width: "100%",
                          height: "100%",
                          overflow: zoomMode === "manual" && lightboxZoom > 1 ? "auto" : "hidden",
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "center",
                        }}
                      >
                        <div
                          className="media-grid"
                          style={{
                            gridTemplateColumns: gridCols,
                            width: zoomMode === "manual" ? `${lightboxZoom * 100}%` : "100%",
                            height: zoomMode === "manual" ? `${lightboxZoom * 100}%` : "100%",
                            maxWidth: "none",
                            maxHeight: "none",
                            minWidth: zoomMode === "manual" && lightboxZoom < 1 ? `${lightboxZoom * 100}%` : undefined,
                            minHeight: zoomMode === "manual" && lightboxZoom < 1 ? `${lightboxZoom * 100}%` : undefined,
                            transition: "width 0.12s ease-out, height 0.12s ease-out",
                          }}
                        >
                        {currentSubmissions.map((sub, idx) => (
                          <MediaGridItem
                            key={sub.id}
                            submission={sub}
                            index={idx}
                            isSelected={primarySubmissionId === sub.id}
                            onSelect={() => {
                              setSelectedSubmissionIndex(idx);
                              setPrimarySubmissionId(sub.id);
                            }}
                            onDoubleClick={() => {
                              setSelectedSubmissionIndex(idx);
                              setLightboxOpen(true);
                              setLightboxZoom(1);
                            }}
                          />
                        ))}
                        </div>
                      </div>
                      {currentSubmissions.length > 0 ? (
                        <div className="review-hub__finalize">
                          <button
                            type="button"
                            className={`review-hub__finalize-btn${
                              primarySubmissionId === currentSubmissions[selectedSubmissionIndex]?.id ? " is-active" : ""
                            }`}
                            disabled={
                              submitting ||
                              !currentVersion ||
                              primarySubmissionId === currentSubmissions[selectedSubmissionIndex]?.id
                            }
                            title={
                              primarySubmissionId === currentSubmissions[selectedSubmissionIndex]?.id
                                ? "已定版"
                                : "定版当前选中图片"
                            }
                            onClick={() => {
                              const submissionId = currentSubmissions[selectedSubmissionIndex]?.id;
                              if (!submissionId || !effectiveTask) return;
                              if (onSetPrimarySubmission) {
                                setSubmitting(true);
                                onSetPrimarySubmission(effectiveTask, submissionId)
                                  .catch(() => {})
                                  .finally(() => setSubmitting(false));
                              } else {
                                // 兜底：后端定版接口未注入时，走审核通过流程
                                handleDecision("approve");
                              }
                            }}
                          >
                            {primarySubmissionId === currentSubmissions[selectedSubmissionIndex]?.id
                              ? "已定版"
                              : "定版"}
                          </button>
                        </div>
                      ) : null}
                    </>
                  )}
                </div>

                <div className="review-hub__versions">
                    <div className="review-hub__versions-head">
                      <h4>版本历史</h4>
                    </div>
                    {versions.length > 0 ? (
                    <div className="review-hub__versions-rail">
                      <button type="button" className="review-hub__versions-nav" aria-label="向左滚动版本">
                        <ChevronLeft size={16} strokeWidth={2} />
                      </button>
                      <div className="review-hub__versions-track">
                        {versions.slice().reverse().map((ver) => {
                          const isActive = ver.id === versions[currentVersionIndex]?.id;
                          const timeStr = ver.submittedAt
                            ? new Date(ver.submittedAt).toLocaleString("zh-CN", {
                                month: "2-digit",
                                day: "2-digit",
                                hour: "2-digit",
                                minute: "2-digit",
                              })
                            : "—";
                          const reviewedStr = ver.reviewedAt
                            ? new Date(ver.reviewedAt).toLocaleString("zh-CN", {
                                month: "2-digit",
                                day: "2-digit",
                                hour: "2-digit",
                                minute: "2-digit",
                              })
                            : "";
                          const commentText = (ver.reviewComment || "").trim();
                          const statusClass =
                            ver.status === "final"
                              ? "is-final"
                              : ver.status === "rework"
                                ? "is-rework"
                                : "is-pending";
                          return (
                            <button
                              key={ver.id}
                              type="button"
                              className={`review-hub__version-card ${isActive ? "is-active" : ""}`}
                              title={commentText || undefined}
                              onClick={() => {
                                const idx = versions.findIndex((v) => v.id === ver.id);
                                if (idx >= 0) setCurrentVersionIndex(idx);
                              }}
                            >
                              <span className="review-hub__version-card-top">
                                <strong>V{String(ver.versionNo).padStart(3, "0")}</strong>
                                <em className={`is-${statusClass}`}>{versionStatusLabel(ver.status)}</em>
                              </span>
                              <span className="review-hub__version-card-time">{timeStr}</span>
                              {reviewedStr ? (
                                <span className="review-hub__version-card-reviewed">审核 {reviewedStr}</span>
                              ) : null}
                              {commentText ? (
                                <span className="review-hub__version-card-comment">{commentText}</span>
                              ) : null}
                            </button>
                          );
                        })}
                      </div>
                      <button type="button" className="review-hub__versions-nav" aria-label="向右滚动版本">
                        <ChevronRight size={16} strokeWidth={2} />
                      </button>
                    </div>
                  ) : (
                    <div className="review-hub__versions-empty">
                      <History size={28} strokeWidth={1.5} aria-hidden="true" />
                      <span>暂无历史</span>
                    </div>
                  )}
                </div>
              </div>
            </>
          )}
        </section>

        <aside className="review-hub__side" aria-label="制作要求与历史">
          {effectiveTask ? (
            <>
              <section className="review-hub__panel">
                <div className="review-center-side__title-row">
                  <h3>制作要求</h3>
                </div>
                <dl className="review-center-spec">
                  {references.visual ? (
                    <div>
                      <dt>画面要求</dt>
                      <dd>{references.visual}</dd>
                    </div>
                  ) : null}
                  {characterTags.length ? (
                    <div>
                      <dt>出场角色</dt>
                      <dd className="is-roles">
                        {characterTags.map((tag) => (
                          <span key={tag}>{tag}</span>
                        ))}
                      </dd>
                    </div>
                  ) : null}
                  {references.prompt ? (
                    <div>
                      <dt>制作提示词</dt>
                      <dd className="is-prompt">{references.prompt}</dd>
                    </div>
                  ) : null}
                  {references.dialogue ? (
                    <div>
                      <dt>对白</dt>
                      <dd>{references.dialogue}</dd>
                    </div>
                  ) : null}
                  {references.camera ? (
                    <div>
                      <dt>镜头</dt>
                      <dd>{references.camera}</dd>
                    </div>
                  ) : null}
                  {references.shotSize ? (
                    <div>
                      <dt>景别</dt>
                      <dd>{references.shotSize}</dd>
                    </div>
                  ) : null}
                </dl>
                {!references.visual && !references.prompt ? (
                  <p className="review-center-side__empty">暂无制作说明，可按标题与类型审核。</p>
                ) : null}
              </section>

              <section className="review-hub__panel is-actions" aria-label="审核意见与操作">
                <div className="review-center-side__title-row">
                  <h3>审核意见</h3>
                </div>
                <div className="review-hub__comment-block">
                  <div className={`review-hub__comment is-search${taskResult ? " is-disabled" : ""}`}>
                    <textarea
                      value={comment}
                      placeholder="输入审核意见"
                      disabled={Boolean(taskResult)}
                      maxLength={COMMENT_MAX}
                      rows={3}
                      onChange={(event) => setComment(event.target.value.slice(0, COMMENT_MAX))}
                    />
                    <div className="review-hub__comment-tools">
                      <em className="review-hub__comment-count">{comment.length}/{COMMENT_MAX}</em>
                    </div>
                  </div>
                </div>
                <div className="review-hub__foot">
                  {canReview ? (
                    <>
                      <button
                        type="button"
                        className="is-approve"
                        disabled={
                          submitting
                          || !currentVersion
                          || Boolean(taskResult)
                          || !primarySubmissionId
                        }
                        onClick={() => handleDecision("approve")}
                      >
                        审核通过
                      </button>
                      <button
                        type="button"
                        className="is-reject"
                        disabled={
                          submitting
                          || !currentVersion
                          || Boolean(taskResult)
                          || !comment.trim()
                        }
                        onClick={() => handleDecision("rework")}
                      >
                        退回修改
                      </button>
                    </>
                  ) : (
                    <div className="review-center-no-permission">您没有审批权限，仅可查看</div>
                  )}
                </div>
              </section>

              <section className="review-hub__panel">
                <div className="review-center-side__title-row">
                  <h3>审核信息</h3>
                </div>
                <dl className="review-hub__info">
                  <div>
                    <dt>当前图位</dt>
                    <dd>{reviewAssetKindLabel(reviewAssetKind(effectiveTask))}</dd>
                  </div>
                  <div>
                    <dt>文件类型</dt>
                    <dd>{isVideoTask ? "视频" : "图片"}</dd>
                  </div>
                  <div>
                    <dt>使用模型</dt>
                    <dd>{currentSubmissions[0]?.model_name || effectiveTask.production_model || "—"}</dd>
                  </div>
                  <div>
                    <dt>项目名称</dt>
                    <dd>{projectTitle(projects, effectiveTask.project_id)}</dd>
                  </div>
                  <div>
                    <dt>审批状态</dt>
                    <dd>
                      <span className={`review-hub__status is-${taskResult || "pending"}`}>
                        <i />
                        {taskResult === "approve" ? "已过审" : taskResult === "rework" ? "已退回" : "待审"}
                      </span>
                    </dd>
                  </div>
                </dl>
              </section>


            </>
          ) : (
            <div className="review-center-empty">选择待审条目后显示制作要求</div>
          )}
        </aside>
      </div>

      {lightboxOpen && currentSubmissions[selectedSubmissionIndex]
        ? createPortal(
            <Lightbox
              url={lightboxMedia.url || ""}
              kind={mediaKind(currentSubmissions[selectedSubmissionIndex])}
              fileName={currentSubmissions[selectedSubmissionIndex].file_name || "预览"}
              submissions={currentSubmissions}
              currentIndex={selectedSubmissionIndex}
              onClose={() => setLightboxOpen(false)}
              onChangeIndex={setSelectedSubmissionIndex}
              zoom={lightboxZoom}
              setZoom={setLightboxZoom}
            />,
            document.body,
          )
        : null}
    </div>
  );
}

function Lightbox({
  url,
  kind,
  fileName,
  submissions,
  currentIndex,
  onClose,
  onChangeIndex,
  zoom,
  setZoom,
}: {
  url: string;
  kind: ReturnType<typeof mediaKind>;
  fileName: string;
  submissions: Submission[];
  currentIndex: number;
  onClose: () => void;
  onChangeIndex: (idx: number) => void;
  zoom: number;
  setZoom: (z: number) => void;
}) {
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      else if (e.key === "ArrowLeft") {
        onChangeIndex(currentIndex <= 0 ? submissions.length - 1 : currentIndex - 1);
      } else if (e.key === "ArrowRight") {
        onChangeIndex(currentIndex >= submissions.length - 1 ? 0 : currentIndex + 1);
      } else if (e.key === "+" || e.key === "=") {
        setZoom(Math.min(zoom + 0.25, 4));
      } else if (e.key === "-" || e.key === "_") {
        setZoom(Math.max(zoom - 0.25, 0.25));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [currentIndex, submissions.length, onClose, onChangeIndex, zoom, setZoom]);

  const zoomIn = () => setZoom(Math.min(zoom + 0.25, 4));
  const zoomOut = () => setZoom(Math.max(zoom - 0.25, 0.25));
  const resetZoom = () => setZoom(1);

  return (
    <div className="review-lightbox" role="dialog" aria-modal="true" aria-label="媒体预览">
      <div className="review-lightbox__backdrop" onClick={onClose} />
      <div className="review-lightbox__toolbar">
        <span className="lightbox-title" title={fileName}>
          {fileName} · {currentIndex + 1}/{submissions.length}
        </span>
        <div className="lightbox-toolbar-actions">
          <button type="button" className="lb-tool-btn" title="缩小 (-)" onClick={zoomOut} aria-label="缩小">
            <ZoomOut size={18} />
          </button>
          <button type="button" className="lb-tool-btn" title="重置 (100%)" onClick={resetZoom} aria-label="重置缩放">
            <span className="zoom-percent">{Math.round(zoom * 100)}%</span>
          </button>
          <button type="button" className="lb-tool-btn" title="放大 (+)" onClick={zoomIn} aria-label="放大">
            <ZoomIn size={18} />
          </button>
          <button type="button" className="lb-tool-btn lb-close-btn" title="关闭 (ESC)" onClick={onClose} aria-label="关闭">
            <X size={20} />
          </button>
        </div>
      </div>
      <div className="review-lightbox__stage">
        {submissions.length > 1 ? (
          <>
            <button
              type="button"
              className="lb-nav-btn lb-nav-btn--left"
              aria-label="上一张"
              onClick={() => onChangeIndex(currentIndex <= 0 ? submissions.length - 1 : currentIndex - 1)}
            >
              <ChevronLeft size={36} />
            </button>
            <button
              type="button"
              className="lb-nav-btn lb-nav-btn--right"
              aria-label="下一张"
              onClick={() => onChangeIndex(currentIndex >= submissions.length - 1 ? 0 : currentIndex + 1)}
            >
              <ChevronRight size={36} />
            </button>
          </>
        ) : null}
        <div
          className="review-lightbox__media"
          style={{ transform: `scale(${zoom})` }}
          onClick={(e) => e.stopPropagation()}
        >
          {kind === "image" ? (
            <img src={url} alt={fileName} />
          ) : kind === "video" ? (
            <video src={url} controls autoPlay />
          ) : kind === "audio" ? (
            <div className="lb-audio-container">
              <span className="lb-audio-icon">🎵</span>
              <audio src={url} controls autoPlay />
            </div>
          ) : (
            <div className="lb-other">该文件类型暂不支持预览</div>
          )}
        </div>
      </div>
    </div>
  );
}
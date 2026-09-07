import React from "react";
import { ChevronLeft, LoaderCircle, CheckCircle2, XCircle, Trash2, Film, Video, Image, Music, AlertTriangle } from "lucide-react";
import type { Project, Submission, Task, WorkspaceResponse } from "../../types";
import {
  useMediaUrl,
  mediaKind,
  shortTitle,
  relativeTime,
  projectTitle,
  resolveBatch,
  candidatesOf,
} from "./shared";
// TODO(审批中心-后端接口需求文档 需求3 - 交接剪辑/废片池 P2):
//   以下两个 store 为 localStorage 过渡实现,后端就绪后需改为真实 API 调用。
//   详见 handoffStore.ts / outtakeStore.ts 文件顶部 TODO 注释。
import {
  createHandoff,
  getHandoff,
  isEpisodeShotTask,
  summarizeEpisodeReady,
  type EpisodeReadySummary,
} from "../editing/handoffStore";
import { addOuttakeFromReview, countOuttakes } from "../editing/outtakeStore";

export type ClipStatus =
  | "pending"
  | "submitted"
  | "reviewing"
  | "approved"
  | "rejected"
  | "completed"
  | "in_progress"
  | "blocked";

export const STATUS_META: Record<
  ClipStatus,
  { label: string; color: string; bg: string }
> = {
  pending: { label: "待开始", color: "#94a3b8", bg: "#f1f5f9" },
  submitted: { label: "已提交", color: "#f59e0b", bg: "#fffbeb" },
  reviewing: { label: "审阅中", color: "#3b82f6", bg: "#eff6ff" },
  approved: { label: "已通过", color: "#10b981", bg: "#ecfdf5" },
  rejected: { label: "已打回", color: "#ef4444", bg: "#fef2f2" },
  completed: { label: "已定版", color: "#10b981", bg: "#ecfdf5" },
  in_progress: { label: "进行中", color: "#6366f1", bg: "#eef2ff" },
  blocked: { label: "阻塞", color: "#dc2626", bg: "#fef2f2" },
};

export function clipStatus(task: Task): ClipStatus {
  const s = String(task.status || "").toLowerCase();
  if (s.includes("approve") || s === "completed" || s.includes("final")) return "approved";
  if (s === "rejected" || s.includes("rework")) return "rejected";
  if (s === "reviewing") return "reviewing";
  if (s === "submitted") return "submitted";
  if (s.includes("progress") || s === "running" || s === "assigned") return "in_progress";
  if (s === "blocked") return "blocked";
  if (s === "pending" || s === "todo" || s === "created") return "pending";
  return "pending";
}

export function makerTaskStatusLabel(task: Task): string {
  const meta = STATUS_META[clipStatus(task)];
  return meta?.label ?? (task.status || "待开始");
}

export type TrackClip = {
  task: Task;
  thumb: Submission | null;
  status: ClipStatus;
};

export function buildTrack(tasks: Task[]): TrackClip[] {
  return tasks.map((task) => {
    const batch = resolveBatch(task);
    const candidates = candidatesOf(task, batch);
    const thumb =
      candidates.find((s) => s.is_primary) ||
      candidates[0] ||
      null;
    return {
      task,
      thumb,
      status: clipStatus(task),
    };
  });
}

type Props = {
  workspace: WorkspaceResponse | null;
  projects: Project[];
  tasks: Task[];
  currentUserName?: string;
  loading: boolean;
  onReviewTask: (
    task: Task,
    batchId: string,
    decision: "approve" | "rework",
    primarySubmissionId: string | null,
    selectedSubmissionIds: string[],
    comment: string,
  ) => Promise<void>;
  onOpenLegacyQueue: () => void;
  onExit?: () => void;
  onOpenEditorDesk?: () => void;
};

type ReviewPreviewProps = {
  task: Task | null;
};

function ReviewPreview({ task }: ReviewPreviewProps) {
  if (!task) {
    return (
      <div className="review-preview review-preview--empty">
        <div className="review-preview__empty">
          <Film size={64} strokeWidth={1.2} opacity={0.35} />
          <div className="review-preview__empty-text">暂无选中分镜</div>
        </div>
      </div>
    );
  }

  const batch = resolveBatch(task);
  const candidates = candidatesOf(task, batch);

  if (!candidates.length) {
    return (
      <div className="review-preview review-preview--empty">
        <div className="review-preview__empty">
          <Film size={64} strokeWidth={1.2} opacity={0.35} />
          <div className="review-preview__empty-text">暂无候选成果</div>
        </div>
      </div>
    );
  }

  const primary = candidates.find((s) => s.is_primary) || candidates[0];
  const kind = mediaKind(primary);
  const { url, loading, error } = useMediaUrl(primary);

  const kindBadge = (() => {
    switch (kind) {
      case "video":
        return (
          <span className="media-kind media-kind--video">
            <Video size={14} /> 视频
          </span>
        );
      case "audio":
        return (
          <span className="media-kind media-kind--audio">
            <Music size={14} /> 音频
          </span>
        );
      case "image":
      default:
        return (
          <span className="media-kind media-kind--image">
            <Image size={14} /> 图片
          </span>
        );
    }
  })();

  return (
    <div className="review-preview">
      <div className="media-kind-wrap">{kindBadge}</div>
      {loading ? (
        <div className="review-preview__loading">
          <LoaderCircle className="spinner" size={40} strokeWidth={1.8} />
          <span>加载预览中…</span>
        </div>
      ) : error ? (
        <div className="review-preview__error">
          <AlertTriangle size={36} opacity={0.5} />
          <span>{error}</span>
        </div>
      ) : !url ? (
        <div className="review-preview__empty">
          <Film size={56} strokeWidth={1.2} opacity={0.3} />
          <span>预览不可用</span>
        </div>
      ) : kind === "video" ? (
        <video
          key={primary.id}
          className="review-preview__video"
          src={url}
          controls
          playsInline
        />
      ) : kind === "audio" ? (
        <div className="review-preview__audio">
          <div className="audio-visual" />
          <audio key={primary.id} src={url} controls />
        </div>
      ) : (
        <img
          key={primary.id}
          className="review-preview__image"
          src={url}
          alt={task.title || "preview"}
        />
      )}
    </div>
  );
}

export function ReviewWorkbenchPage({
  workspace,
  projects,
  tasks,
  currentUserName,
  loading,
  onReviewTask,
  onOpenLegacyQueue,
  onExit,
  onOpenEditorDesk,
}: Props) {
  const pending = React.useMemo(
    () =>
      tasks.filter((t) =>
        ["submitted", "reviewing"].includes(String(t.status || "").toLowerCase()),
      ),
    [tasks],
  );

  const completed = React.useMemo(
    () =>
      tasks.filter((t) =>
        ["approved", "completed"].includes(String(t.status || "").toLowerCase()),
      ),
    [tasks],
  );

  const [currentTaskId, setCurrentTaskId] = React.useState<string | null>(() => {
    if (pending.length) return pending[0].id;
    if (completed.length) return completed[0].id;
    return tasks[0]?.id ?? null;
  });

  const currentTask =
    tasks.find((t) => t.id === currentTaskId) ||
    pending[0] ||
    completed[0] ||
    tasks[0] ||
    null;

  const batch = currentTask ? resolveBatch(currentTask) : null;
  const candidates = currentTask ? candidatesOf(currentTask, batch) : [];
  const [primarySubmissionId, setPrimarySubmissionId] = React.useState<string | null>(() => {
    const p = candidates.find((s) => s.is_primary) || candidates[0];
    return p?.id ?? null;
  });

  React.useEffect(() => {
    const p = candidates.find((s) => s.is_primary) || candidates[0];
    setPrimarySubmissionId(p?.id ?? null);
  }, [currentTask?.id, candidates.length]);

  const [selectedSubmissionId, setSelectedSubmissionId] = React.useState<string | null>(() => {
    const p = candidates.find((s) => s.is_primary) || candidates[0];
    return p?.id ?? null;
  });

  React.useEffect(() => {
    const p = candidates.find((s) => s.is_primary) || candidates[0];
    setSelectedSubmissionId(p?.id ?? null);
  }, [currentTask?.id, candidates.length]);

  const [comment, setComment] = React.useState("");
  const [decisionLoading, setDecisionLoading] = React.useState(false);
  // TODO(需求3): outtakeCount 当前从 localStorage 读取,后端就绪后改为
  //   GET /api/episodes/{episode_id}/waste-pool/count,并在任务切换时重新拉取
  const [outtakeCount, setOuttakeCount] = React.useState(() => {
    if (!currentTask) return 0;
    return countOuttakes(currentTask.project_id, currentTask.episode_code || "—");
  });

  React.useEffect(() => {
    if (!currentTask) {
      setOuttakeCount(0);
      return;
    }
    setOuttakeCount(countOuttakes(currentTask.project_id, currentTask.episode_code || "—"));
  }, [currentTask?.project_id, currentTask?.episode_code]);

  const projectId = currentTask?.project_id ?? "";
  const episodeCode = currentTask?.episode_code ?? "";
  const storyboardCode = currentTask?.storyboard_code ?? "";
  const pTitle = projectId ? projectTitle(projects, projectId) : "";

  const episodeSummary: EpisodeReadySummary | null = React.useMemo(() => {
    if (!projectId || !episodeCode) return null;
    return summarizeEpisodeReady(tasks, projectId, episodeCode);
  }, [tasks, projectId, episodeCode]);

  // TODO(需求3): alreadyHandedOff 当前从 localStorage 读取,后端就绪后改为
  //   GET /api/episodes/{episode_id}/handoff,根据返回的 status 判断是否已交接
  const alreadyHandedOff = React.useMemo(() => {
    if (!projectId || !episodeCode) return false;
    const handoff = getHandoff(projectId, episodeCode);
    return Boolean(handoff && handoff.status !== "downloaded");
  }, [projectId, episodeCode]);

  const allReady = episodeSummary?.allReady ?? false;

  const isMigrationNotice = React.useMemo(() => {
    if (!workspace?.groups?.todo && !workspace?.groups?.completed) {
      return projects.some((p) => p.title === "迁移测试项目");
    }
    const allTasks = [
      ...(workspace.groups.todo || []),
      ...(workspace.groups.completed || []),
    ];
    const pids = new Set(allTasks.map((t) => t.project_id));
    return projects.some((p) => pids.has(p.id) && p.title === "迁移测试项目");
  }, [workspace, projects]);

  const trackTasks = React.useMemo(() => {
    const sameProjectCompleted = projectId
      ? completed.filter((t) => t.project_id === projectId)
      : [];
    return [...pending, ...sameProjectCompleted];
  }, [pending, completed, projectId]);

  const track = React.useMemo(() => buildTrack(trackTasks), [trackTasks]);

  function handleNextTask() {
    const idx = pending.findIndex((t) => t.id === currentTaskId);
    if (idx >= 0 && idx + 1 < pending.length) {
      setCurrentTaskId(pending[idx + 1].id);
      setComment("");
    } else if (idx === pending.length - 1 || pending.length === 0) {
      window.setTimeout(() => {
        alert("审片完成！本批次待审镜已处理完毕。");
      }, 80);
    }
  }

  async function handleApprove() {
    if (!currentTask || !batch || decisionLoading) return;
    if (!primarySubmissionId) {
      alert("请先选择一个主母版后再通过定版。");
      return;
    }
    const allIds = candidates.map((s) => s.id);
    setDecisionLoading(true);
    try {
      await onReviewTask(
        currentTask,
        batch.id,
        "approve",
        primarySubmissionId,
        allIds,
        comment,
      );
      handleNextTask();
    } finally {
      setDecisionLoading(false);
    }
  }

  async function handleRework() {
    if (!currentTask || !batch || decisionLoading) return;
    setDecisionLoading(true);
    try {
      await onReviewTask(currentTask, batch.id, "rework", null, [], comment);
      handleNextTask();
    } finally {
      setDecisionLoading(false);
    }
  }

  async function handleOuttakeAndRework() {
    if (!currentTask || !batch || decisionLoading) return;
    const selected = candidates.find((s) => s.id === selectedSubmissionId);
    if (!selected) {
      alert("请选择需要纳入废片池的候选。");
      return;
    }
    if (!comment.trim()) {
      alert("废片原因不能为空。请填写导演意见后再操作。");
      return;
    }
    setDecisionLoading(true);
    try {
      // TODO(需求3): 后端就绪后改为
      //   await api.post(`/tasks/${currentTask.id}/submissions/${selected.id}/waste`, { comment: comment.trim() });
      //   并移除下方 addOuttakeFromReview 本地写入;outtakeCount 改为接口返回后刷新
      addOuttakeFromReview({
        task: currentTask,
        submission: selected,
        comment: comment.trim(),
        addedBy: currentUserName || "导演",
      });
      setOuttakeCount((c) => c + 1);
      await onReviewTask(currentTask, batch.id, "rework", null, [], comment.trim());
      handleNextTask();
    } finally {
      setDecisionLoading(false);
    }
  }

  function handleHandoff() {
    if (!episodeSummary || !episodeSummary.allReady || alreadyHandedOff) return;
    // TODO(需求3): 后端就绪后改为
    //   await api.post(`/tasks/${currentTask.id}/submissions/${primarySubmissionId}/handoff`,
    //     { target_step: "editing", note: "" });
    //   并移除下方 createHandoff 本地写入;alreadyHandedOff 改为接口返回后刷新
    createHandoff({
      projectId: episodeSummary.projectId,
      projectTitle: pTitle || "项目",
      episodeCode: episodeSummary.episodeCode,
      handedOffBy: currentUserName || "导演",
      shots: episodeSummary.shots,
    });
    alert(`已交接剪辑：${episodeSummary.episodeCode}，共 ${episodeSummary.shots.length} 镜。`);
  }

  const working = loading || decisionLoading;

  return (
    <div className="review-station">
      {isMigrationNotice ? (
        <div className="notice notice--warning">
          <AlertTriangle size={16} />
          <span>当前为模拟数据展示</span>
        </div>
      ) : null}

      <div className="review-station__top">
        <div className="review-station__brand">
          <button
            type="button"
            className="btn btn--ghost back-btn"
            onClick={onOpenLegacyQueue}
            disabled={working}
          >
            <ChevronLeft size={18} />
            退回资产审批
          </button>
          <div className="brand-title">
            <span className="brand-mark" />
            <strong>CineForge 审片台</strong>
          </div>
        </div>

        <div className="review-station__stats">
          <span className="stats-pending">
            <CheckCircle2 size={15} />
            {pending.length} 镜待审
          </span>
          {pTitle ? (
            <>
              <span className="stats-sep">·</span>
              <span className="stats-project">{pTitle}</span>
              {episodeCode ? (
                <>
                  <span className="stats-sep">·</span>
                  <span className="stats-episode">{episodeCode}</span>
                </>
              ) : null}
              {storyboardCode ? (
                <>
                  <span className="stats-sep">·</span>
                  <span className="stats-shot">{storyboardCode}</span>
                </>
              ) : null}
            </>
          ) : null}
        </div>

        <div className="review-station__actions">
          <button
            type="button"
            className="btn editor-desk-btn"
            onClick={onOpenEditorDesk}
            disabled
          >
            剪辑台
            <span className="badge badge--experiment">实验</span>
          </button>
          <span className="outtake-counter" title="本集废片池">
            <Trash2 size={14} />
            {outtakeCount}
          </span>
          <button
            type="button"
            className="btn btn--ghost exit-btn"
            onClick={onExit}
            disabled={working}
          >
            退出审片
          </button>
        </div>
      </div>

      <div className="review-station__stage-wrap">
        <div className="review-station__stage">
          <ReviewPreview task={currentTask} />
          {currentTask ? (
            <div className="review-station__meta">
              <div className="meta-title">
                <strong>{currentTask.storyboard_code || shortTitle(currentTask)}</strong>
                <span className="meta-status chip" style={{
                  background: STATUS_META[clipStatus(currentTask)].bg,
                  color: STATUS_META[clipStatus(currentTask)].color,
                }}>
                  {makerTaskStatusLabel(currentTask)}
                </span>
              </div>
              {currentTask.storyboard_dialogue ? (
                <p className="meta-dialogue">{currentTask.storyboard_dialogue}</p>
              ) : null}
              <div className="meta-foot">
                <span>
                  提交人：{currentTask.assignee_name || "未指派"}
                </span>
                <span className="meta-time">
                  {relativeTime(batch?.submitted_at || currentTask.updated_at)}
                </span>
              </div>
            </div>
          ) : null}
        </div>

        <aside className="review-station__decide" style={{ zIndex: 30 }}>
          <div className="decision-card">
            <div className="decision-card__head">
              <strong>通过定版</strong>
              <span className="decision-hint">选择主母版，后续剪辑以此为准</span>
            </div>

            <div className="decision-card__body">
              <div className="master-picker">
                <div className="picker-label">主母版（必须选择）</div>
                <div className="master-list">
                  {candidates.length === 0 ? (
                    <div className="empty-hint small">当前版本无候选成果。</div>
                  ) : (
                    candidates.map((sub, idx) => {
                      const k = mediaKind(sub);
                      const checked = primarySubmissionId === sub.id;
                      return (
                        <label
                          key={sub.id}
                          className={`master-item ${checked ? "is-selected" : ""}`}
                        >
                          <input
                            type="radio"
                            name="primary-master"
                            value={sub.id}
                            checked={checked}
                            onChange={() => setPrimarySubmissionId(sub.id)}
                            disabled={working}
                          />
                          <div className="master-preview">
                            <MasterThumb submission={sub} />
                          </div>
                          <div className="master-meta">
                            <span className="master-name">
                              #{idx + 1} {k === "video" ? "视频" : k === "audio" ? "音频" : "图片"}
                              {sub.is_primary ? (
                                <span className="tag tag--primary">原始主母版</span>
                              ) : null}
                            </span>
                            <span className="master-sub">
                              {sub.version_no ? `v${sub.version_no}` : ""}
                            </span>
                          </div>
                        </label>
                      );
                    })
                  )}
                </div>
              </div>

              <div className="comment-block">
                <label className="field-label">导演意见（可选）</label>
                <textarea
                  className="comment-input"
                  placeholder="通过时可补充说明；打回时请务必填写返工方向…"
                  value={comment}
                  onChange={(e) => setComment(e.target.value)}
                  rows={3}
                  disabled={working}
                />
              </div>

              <button
                type="button"
                className="btn primary btn--block"
                onClick={handleApprove}
                disabled={working || !primarySubmissionId}
              >
                {decisionLoading ? (
                  <>
                    <LoaderCircle className="spinner" size={16} strokeWidth={2} />
                    提交中…
                  </>
                ) : (
                  <>
                    <CheckCircle2 size={16} />
                    通过定版
                  </>
                )}
              </button>
            </div>
          </div>

          <div className="decision-card decision-card--secondary">
            <div className="decision-card__head">
              <strong>打回返工</strong>
              <span className="decision-hint">任务回到创作者重做</span>
            </div>
            <div className="decision-card__body">
              <button
                type="button"
                className="btn btn--block"
                onClick={handleRework}
                disabled={working}
              >
                {decisionLoading ? (
                  <LoaderCircle className="spinner" size={16} strokeWidth={2} />
                ) : (
                  <XCircle size={16} />
                )}
                打回返工
              </button>
            </div>
          </div>

          <div className="decision-card decision-card--danger">
            <div className="decision-card__head">
              <strong>打回并进废片池</strong>
              <span className="decision-hint danger">
                此候选将存入废片池，供后续补镜参考
              </span>
            </div>
            <div className="decision-card__body">
              <div className="outtake-picker">
                <div className="picker-label">作为废片存档的候选</div>
                <div className="outtake-list">
                  {candidates.length === 0 ? (
                    <div className="empty-hint small">暂无可选候选。</div>
                  ) : (
                    candidates.map((sub, idx) => {
                      const checked = selectedSubmissionId === sub.id;
                      return (
                        <label
                          key={sub.id}
                          className={`outtake-item ${checked ? "is-selected" : ""}`}
                        >
                          <input
                            type="radio"
                            name="outtake-pick"
                            value={sub.id}
                            checked={checked}
                            onChange={() => setSelectedSubmissionId(sub.id)}
                            disabled={working}
                          />
                          <MasterThumb submission={sub} />
                          <span>#{idx + 1}</span>
                        </label>
                      );
                    })
                  )}
                </div>
              </div>

              <button
                type="button"
                className="btn danger btn--block"
                onClick={handleOuttakeAndRework}
                disabled={working || !comment.trim() || !selectedSubmissionId}
              >
                {decisionLoading ? (
                  <LoaderCircle className="spinner" size={16} strokeWidth={2} />
                ) : (
                  <Trash2 size={16} />
                )}
                打回并进废片池
              </button>
            </div>
          </div>

          {episodeSummary ? (
            <div className="episode-progress-card">
              <div className="progress-head">
                <strong>本集进度</strong>
                <span>
                  {episodeSummary.ready.length} / {episodeSummary.shots.length} 镜
                </span>
              </div>
              <div className="progress-bar">
                <div
                  className={`progress-fill ${allReady ? "is-full" : ""}`}
                  style={{
                    width: `${episodeSummary.shots.length
                      ? Math.round(
                          (episodeSummary.ready.length / episodeSummary.shots.length) * 100,
                        )
                      : 0}%`,
                  }}
                />
              </div>
              <div className="progress-sub">
                <span>待审 {episodeSummary.reviewing.length}</span>
                <span>未就绪 {episodeSummary.pending.length}</span>
              </div>

              <button
                type="button"
                className={`btn btn--block handoff-btn ${alreadyHandedOff ? "is-handed" : ""}`}
                onClick={handleHandoff}
                disabled={working || !allReady || alreadyHandedOff}
              >
                {alreadyHandedOff ? (
                  <>
                    <CheckCircle2 size={15} />
                    已交接剪辑
                  </>
                ) : (
                  <>
                    <Film size={15} />
                    交接剪辑
                    <span className="badge badge--experiment">实验</span>
                  </>
                )}
              </button>
            </div>
          ) : null}
        </aside>
      </div>

      <div className="story-strip" style={{ zIndex: 40 }}>
        <div className="story-strip__label">分镜序列</div>
        <div className="story-strip__track">
          {track.length === 0 ? (
            <div className="empty-hint">暂无分镜任务。</div>
          ) : (
            track.map((clip, idx) => {
              const total = track.length;
              const meta = STATUS_META[clip.status];
              const active = clip.task.id === currentTaskId;
              return (
                <button
                  key={clip.task.id}
                  type="button"
                  className={`story-strip__clip ${active ? "story-strip__clip--active" : ""}`}
                  style={{ background: meta.bg }}
                  onClick={() => {
                    if (!working) setCurrentTaskId(clip.task.id);
                  }}
                  disabled={working}
                >
                  <div className="clip-index">
                    <span style={{ color: meta.color, fontWeight: 800 }}>
                      {idx + 1} / {total}
                    </span>
                    <span className="clip-code">{clip.task.storyboard_code || ""}</span>
                  </div>
                  <div className="clip-thumb">
                    <MasterThumb submission={clip.thumb} />
                  </div>
                  <div
                    className="clip-status-dot"
                    style={{ background: meta.color }}
                    title={meta.label}
                  />
                </button>
              );
            })
          )}
        </div>
      </div>
    </div>
  );
}

function MasterThumb({ submission }: { submission: Submission | null }) {
  const { url } = useMediaUrl(submission);
  const kind = submission ? mediaKind(submission) : "other";
  if (!submission || !url) {
    return <div className="thumb-placeholder" />;
  }
  if (kind === "video" || kind === "audio") {
    return (
      <div className="thumb-media">
        {kind === "video" ? (
          <Video size={16} opacity={0.7} />
        ) : (
          <Music size={16} opacity={0.7} />
        )}
      </div>
    );
  }
  return <img src={url} alt="" className="thumb-image" />;
}

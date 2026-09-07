import React from "react";
import { Plus, Upload, X } from "lucide-react";
import { api } from "../../api";
import type {
  AgentRun,
  BreakdownDraftData,
  Project,
  ProjectCreatePayload,
  ProjectEpisode,
  ProjectImportPayload,
  ProjectUpdatePayload,
  ScriptSegment,
  Storyboard,
  Task,
  TemporaryAssetProductionPayload,
  User,
} from "../../types";
import { AssetBreakdownPage } from "./AssetBreakdownPage";
import { NewProjectPage } from "./NewProjectPage";

/**
 * 分集工作台组件 —— 迁移自 legacy-ui ProjectDetailPage 的 episode-workbench 区段。
 * 展示分集列表、选中分集的梗概 / 版本记录 / 制作流程，并对接当前项目已有后端接口。
 */
export type EpisodeWorkbenchProps = {
  project: Project;
  episodes: ProjectEpisode[];
  selectedEpisodeId: string;
  onSelectEpisode: (id: string) => void;
  episodesLoading: boolean;
  selectedEpisode: ProjectEpisode | undefined;
  selectedDraft: BreakdownDraftData | undefined;
  selectedDraftSyncing: boolean;
  selectedRuns: AgentRun[];
  selectedScriptSegments: ScriptSegment[];
  selectedStoryboards: Storyboard[];
  selectedTasks: Task[];
  loading: boolean;
  readonly: boolean;
  currentUser: User;
  /** 新增下一集（打开弹框） */
  onAddEpisode: () => void;
  /** 关闭新增分集弹框 */
  onCloseAddEpisode: () => void;
  /** 当前新增分集弹框是否打开 */
  addEpisodeOpen: boolean;
  /** 创建项目（新增分集时可能需要） */
  onCreateProject?: (payload: ProjectCreatePayload) => Promise<Project | null>;
  /** 更新项目 */
  onUpdateProject?: (id: string, payload: ProjectUpdatePayload) => Promise<Project | null>;
  /** 导入项目（上传剧本文件） */
  onImportProject: (payload: ProjectImportPayload, file: File) => Promise<Project | null>;
  /** 打开剧本新版本上传弹框 */
  onOpenScriptUpload: (episodeId: string) => void;
  /** 删除当前选中分集 */
  onDeleteEpisode: () => void;
  /** 保存拆解草稿 */
  onSaveDraft: (draft: BreakdownDraftData) => Promise<boolean>;
  /** 确认脚本分段 */
  onConfirmScriptSegments: (draft: BreakdownDraftData) => Promise<void>;
  /** 运行拆解步骤 */
  onRunBreakdownStep: (step: string, projectId?: string, episodeId?: string, scriptVersionId?: string) => Promise<boolean>;
  /** 重试提示词分发 */
  onRetryPromptDistribution: (projectId: string, runId: string) => Promise<void>;
  /** 创建临时资产 */
  onCreateTemporaryAssetProduction: (payload: TemporaryAssetProductionPayload) => Promise<boolean>;
  /** 锁定分集拆解 */
  onLockEpisodeBreakdown: (episodeCode: string, draft: BreakdownDraftData) => Promise<boolean>;
  /** 生成分镜任务 */
  onGenerateStoryboardTasks: (projectId: string, episodeId: string, scriptVersionId: string) => Promise<void>;
  /** 跳转到任务列表 */
  onJumpToTasks: () => void;
  /** 打开任务分发模块 */
  onOpenTaskAssignment: () => void;
};

export function EpisodeWorkbench({
  project,
  episodes,
  selectedEpisodeId,
  onSelectEpisode,
  episodesLoading,
  selectedEpisode,
  selectedDraft,
  selectedDraftSyncing,
  selectedRuns,
  selectedScriptSegments,
  selectedStoryboards,
  selectedTasks,
  loading,
  readonly,
  currentUser,
  onAddEpisode,
  onCloseAddEpisode,
  addEpisodeOpen,
  onCreateProject,
  onUpdateProject,
  onImportProject,
  onOpenScriptUpload,
  onDeleteEpisode,
  onSaveDraft,
  onConfirmScriptSegments,
  onRunBreakdownStep,
  onRetryPromptDistribution,
  onCreateTemporaryAssetProduction,
  onLockEpisodeBreakdown,
  onGenerateStoryboardTasks,
  onJumpToTasks,
  onOpenTaskAssignment,
}: EpisodeWorkbenchProps) {
  // 基于运行中的 AgentRun 动态计算分集管道状态
  const selectedPipelineState = episodePipelineRuntimeState(
    selectedRuns,
    [],
    selectedEpisode?.breakdown_status,
  );

  const selectedSynopsis = selectedEpisode
    ? episodeSynopsis(selectedEpisode, selectedDraft, selectedScriptSegments)
    : "";

  return (
    <section className={`project-module admin-panel ${readonly ? "module-readonly" : ""}`}>
      <div className="episode-workbench">
        <nav className="episode-sidebar" aria-label="分集列表">
          <div className="episode-sidebar-head">
            <div>
              <strong>分集</strong>
              <span>{episodes.length} 集</span>
            </div>
            {episodesLoading ? (
              <span className="muted">加载中</span>
            ) : (
              <button
                type="button"
                className="episode-add-button"
                disabled={readonly}
                title="新增下一集"
                onClick={onAddEpisode}
              >
                <Plus size={13} strokeWidth={2.2} />
                新增
              </button>
            )}
          </div>
          <div className="episode-list">
            {episodes.map((item) => (
              <button
                className={`episode-list-item ${item.id === selectedEpisodeId ? "active" : ""}`}
                key={item.id}
                type="button"
                onClick={() => onSelectEpisode(item.id)}
              >
                <span className="episode-list-title">
                  <strong>{item.episode_code}</strong>
                  <b>{`第 ${item.episode_no} 集`}</b>
                </span>
                <span className={episodeStatusTagClass(item.id === selectedEpisodeId ? selectedPipelineState.tone : episodeStatusTone(item.breakdown_status))}>
                  {item.id === selectedEpisodeId ? selectedPipelineState.label : episodeStatusLabel(item.breakdown_status)}
                </span>
              </button>
            ))}
            {!episodesLoading && !episodes.length ? <div className="empty-hint">暂无分集，请从项目总览点击"新增分集"。</div> : null}
          </div>
        </nav>
        <div className="episode-detail">
          {selectedEpisode ? (
            <>
              <header className="episode-detail-head">
                <div className="episode-detail-heading">
                  <span className="episode-detail-code">{selectedEpisode.episode_code}</span>
                  <div>
                    <h2>{`第 ${selectedEpisode.episode_no} 集`}</h2>
                    <span className={episodeStatusTagClass(selectedPipelineState.tone)}>
                      {selectedPipelineState.label}
                    </span>
                  </div>
                </div>
                <div className="form-actions compact-actions">
                  <button className="project-detail-summary__edit" disabled={readonly || loading} type="button" onClick={() => onOpenScriptUpload(selectedEpisode.id)}>
                    <Upload size={12} strokeWidth={2} />
                    上传新版本
                  </button>
                  <button className="project-detail-summary__edit project-detail-summary__edit--danger" disabled={readonly} type="button" onClick={onDeleteEpisode}>
                    删除分集
                  </button>
                </div>
              </header>
              <section className="episode-summary-band">
                <div className="episode-summary-title">
                  <strong>故事梗概</strong>
                  <span className={`episode-summary-status is-${episodeStatusTone(selectedEpisode.breakdown_status)}`}>
                    {episodeStatusLabel(selectedEpisode.breakdown_status)}
                  </span>
                </div>
                <p>{selectedSynopsis || "围读完成后在此展示当前分集的故事梗概。"}</p>
              </section>
              <details className="episode-version-detail">
                <summary>剧本版本与文件记录</summary>
                <div className="episode-version-list">
                  {(selectedEpisode.versions?.length ? selectedEpisode.versions : [{
                    id: selectedEpisode.current_version_id || selectedEpisode.id,
                    version_no: selectedEpisode.current_version_no || 1,
                    original_filename: selectedEpisode.current_original_filename,
                    is_current: true,
                  }]).map((version) => (
                    <div className="episode-version-row" key={version.id}>
                      <strong>v{version.version_no}</strong>
                      <span>{version.original_filename || "原始文件"}</span>
                      <span>
                        <EpisodeVersionTextButton
                          projectId={project.id}
                          episodeId={selectedEpisode.id}
                          episodeCode={selectedEpisode.episode_code}
                          versionId={version.id}
                          versionNo={version.version_no}
                        />
                      </span>
                    </div>
                  ))}
                  {!selectedEpisode.versions?.length && selectedEpisode.version_count > 1 ? (
                    <span className="field-hint">共 {selectedEpisode.version_count} 个版本，展开历史记录需后端返回版本明细。</span>
                  ) : null}
                </div>
              </details>
              <section className="episode-production-flow">
                <header>
                  <div>
                    <h3>制作流程</h3>
                  </div>
                </header>
                <AssetBreakdownPage
                  embedded
                  project={project}
                  episodeId={selectedEpisode.id}
                  episodeCode={selectedEpisode.episode_code}
                  scriptVersionId={selectedEpisode.current_version_id || undefined}
                  runs={selectedRuns}
                  scriptSegments={selectedScriptSegments}
                  storyboards={selectedStoryboards}
                  tasks={selectedTasks}
                  draftOverride={selectedDraft}
                  draftSyncing={selectedDraftSyncing}
                  loading={loading}
                  readOnly={readonly}
                  onOpenTaskAssignment={onOpenTaskAssignment}
                  onSaveDraft={onSaveDraft}
                  onConfirmScriptSegments={onConfirmScriptSegments}
                  onRunStep={(step) => onRunBreakdownStep(step, project.id, selectedEpisode.id, selectedEpisode.current_version_id || undefined)}
                  onRetryPromptDistribution={(runId) => onRetryPromptDistribution(project.id, runId)}
                  canCreateTemporaryAsset={["director", "admin"].includes(currentUser.role)}
                  onCreateTemporaryAssetProduction={onCreateTemporaryAssetProduction}
                  onLockEpisode={async (episodeCode, draft) => {
                    const locked = await onLockEpisodeBreakdown(episodeCode, draft);
                    if (!locked) return;
                    if (!selectedEpisode.current_version_id) throw new Error("当前分集缺少剧本版本，不能生成分镜任务。");
                    await onGenerateStoryboardTasks(project.id, selectedEpisode.id, selectedEpisode.current_version_id);
                  }}
                />
              </section>
            </>
          ) : (
            <div className="empty-hint wide">选择一个分集查看故事梗概和拆解流程。</div>
          )}
        </div>
      </div>

      {/* 新增分集弹框 */}
      {addEpisodeOpen ? (
        <NewProjectPage
          key={`add-episode-${project.id}-${episodes.map((item) => item.episode_no).join("-") || "0"}`}
          asModal
          embedded
          currentProject={project}
          mode="add_episode"
          seedEpisodes={episodes}
          loading={loading}
          readOnly={false}
          onClose={onCloseAddEpisode}
          onCreateProject={onCreateProject}
          onUpdateProject={onUpdateProject}
          onImportProject={onImportProject}
        />
      ) : null}
    </section>
  );
}

/* ===== 辅助函数 ===== */

function recordValue(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

export function episodeStatusLabel(status?: string | null) {
  const labels: Record<string, string> = {
    pending: "待围读",
    script_imported: "待围读",
    reading: "围读中",
    reading_confirm: "围读待确认",
    reading_confirmed: "围读已确认",
    asset_draft: "资产预定稿",
    asset_saved: "资产已保存",
    segmentation: "脚本段拆分",
    breakdown_confirmed: "拆解已确认",
    storyboard: "分镜预定稿",
    storyboard_ready: "分镜已就绪",
    production: "生产中",
    completed: "拆解完成",
    stale: "结果已过期",
    outdated: "结果已过期",
  };
  return labels[String(status || "pending")] || String(status || "待围读");
}

export function episodeStatusTone(status?: string | null) {
  if (status === "completed") return "complete";
  if (status === "stale" || status === "outdated") return "stale";
  if (status && !["pending", "script_imported"].includes(status)) return "active";
  return "pending";
}

export function episodeStatusTagClass(tone: string) {
  // 使用 legacy-ui 统一的 episode-breakdown-status 系列标签样式
  return `episode-breakdown-status ${tone}`;
}

// 基于运行中的 AgentRun 动态计算分集管道状态
// TODO: 后端支持 WorkflowRun 后，需将 workflows 参数传入实际的工作流运行记录
function episodePipelineRuntimeState(
  runs: AgentRun[],
  workflows: Array<{ status?: string; result?: Record<string, unknown> }>,
  fallbackStatus?: string | null,
): { label: string; tone: string } {
  const activeRun = runs.find((run) => (
    ["script_reading", "script_breakdown"].includes(String(run.agent_type || ""))
    && ["queued", "pending", "running", "processing"].includes(String(run.status || ""))
  ));
  const activeWorkflow = workflows.find((run) => (
    ["queued", "pending", "running", "processing"].includes(String(run.status || ""))
  ));
  if (activeRun || activeWorkflow) {
    const input = recordValue(activeRun?.input);
    const progress = recordValue(activeWorkflow?.result?.progress);
    const rawStep = String(
      input.single_step
      || input.step
      || progress.current_label
      || progress.phase
      || (activeRun?.agent_type === "script_reading" ? "reading" : "reading"),
    ).toLowerCase();
    const label = rawStep.includes("asset")
      ? "资产拆解中"
      : rawStep.includes("prompt")
        ? "提示词生成中"
        : rawStep.includes("segment")
          ? "脚本段拆分中"
          : rawStep.includes("storyboard")
            ? "分镜拆解中"
            : "剧本围读中";
    return { label, tone: "active" };
  }

  const latestPipelineRun = runs.find((run) => (
    ["script_reading", "script_breakdown"].includes(String(run.agent_type || ""))
  ));
  if (latestPipelineRun && String(latestPipelineRun.status || "") === "failed") {
    return { label: "流程执行失败", tone: "stale" };
  }
  return {
    label: episodeStatusLabel(fallbackStatus),
    tone: episodeStatusTone(fallbackStatus),
  };
}

function episodeSynopsis(episode: ProjectEpisode, draft: BreakdownDraftData | undefined, segments: ScriptSegment[]) {
  if (episode.summary?.trim() && episode.summary.trim() !== "由 projects.script_text 回填") return episode.summary.trim();
  const report = draft?.readingReport;
  const overview = report?.story_overview as Record<string, unknown> | undefined;
  const synopsis = overview?.synopsis;
  if (typeof synopsis === "string" && synopsis.trim()) return synopsis.trim();
  return segments.map((item) => item.summary).filter(Boolean).slice(0, 3).join("；");
}

/* ===== 剧本原文查看按钮 ===== */

function EpisodeVersionTextButton({
  projectId,
  episodeId,
  episodeCode,
  versionId,
  versionNo,
}: {
  projectId: string;
  episodeId: string;
  episodeCode: string;
  versionId: string;
  versionNo: number;
}) {
  const [open, setOpen] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [text, setText] = React.useState<string | null>(null);
  const [error, setError] = React.useState("");

  React.useEffect(() => {
    if (!open) return undefined;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);

  async function openVersionText() {
    setOpen(true);
    if (text !== null || loading) return;
    setLoading(true);
    setError("");
    try {
      const result = await api.get<Record<string, unknown>>(`/projects/${projectId}/episodes/${episodeId}/versions/${versionId}`);
      const content = result.script_text ?? result.source_text ?? result.content ?? result.text;
      setText(typeof content === "string" ? content : "");
    } catch (err) {
      setError(err instanceof Error ? err.message : "剧本原文加载失败");
    } finally {
      setLoading(false);
    }
  }

  return (
    <>
      <button className="text-link-btn" type="button" onClick={() => void openVersionText()}>查看原文</button>
      {open ? (
        <div className="modal-backdrop episode-script-modal-backdrop" role="presentation" onClick={() => setOpen(false)}>
          <section className="long-text-modal episode-script-modal" role="dialog" aria-modal="true" aria-label={`${episodeCode} v${versionNo} · 剧本原文`} onClick={(event) => event.stopPropagation()}>
            <div className="modal-head episode-script-modal__head">
              <div>
                <h3>{episodeCode} v{versionNo} · 剧本原文</h3>
                <p>{loading ? "正在读取文档..." : error || `${text?.length || 0} 字符`}</p>
              </div>
              <button className="episode-script-modal__close" type="button" aria-label="关闭" title="关闭" onClick={() => setOpen(false)}>
                <X size={18} strokeWidth={1.8} />
              </button>
            </div>
            {error ? <div className="notice error">{error}<button className="btn" type="button" onClick={() => { setText(null); void openVersionText(); }}>重新加载</button></div> : null}
            {!error ? <pre className="long-text-modal-body episode-script-modal__body">{loading ? "正在读取剧本原文..." : text || "暂无剧本原文"}</pre> : null}
          </section>
        </div>
      ) : null}
    </>
  );
}

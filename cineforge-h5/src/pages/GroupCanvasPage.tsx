import React from "react";
import { ChevronLeft, RefreshCw } from "lucide-react";
import { getPlatformToken, getSessionExpiresAt, getTapToken } from "../api";
import { platformFetch } from "../goApi";
import { createCineForgeSsoSessionMessage } from "../ssoProtocol";
import { createClientUuid } from "../clientId";
import {
  buildTaskUnits,
  CharacterTaskTable,
  queueLabel,
  StoryboardProductionWorkbench,
  TaskWorkItem,
  type MyTasksPageProps,
  type TaskUnit,
} from "../features/tasks/MyTasksPage";
import type { Asset, Project, Task, WorkspaceResponse } from "../types";

/** 画布项目嵌入地址（由 vite.config.ts 中 define.__CANVAS_URL__ 注入） */
declare const __CANVAS_URL__: string;

/** 分组画布 ↔ iframe 通信协议（画布分组设计 3.5） */
const PROTOCOL_SOURCE = "cineforge-group-canvas";
type GroupContextTask = {
  taskId: string;
  title: string;
  taskVariant?: string | null;
  status: string;
  locked?: boolean;
  keyframeDone?: boolean;
  taskType: string;
};
type NodeCandidateRequest = {
  source: typeof PROTOCOL_SOURCE;
  type: "NODE_CANDIDATE_REQUEST";
  nodeId: string;
  assetId: string;
  flowId: string;
  mediaType?: string;
};
type PendingCandidateRequest = {
  nodeId: string;
  assetId: string;
  flowId: string;
};

type GroupType = "character" | "storyboard" | "task";

type CandidateTargetOption = {
  taskId: string;
  badge: string;
  title: string;
  step: "keyframe" | "video" | null;
  disabled: boolean;
  disabledReason: string | null;
};

function unitGroupType(unit: TaskUnit): GroupType {
  if (unit.character) return "character";
  if (unit.key.startsWith("storyboard:")) return "storyboard";
  return "task";
}

function groupTypeLabel(groupType: GroupType): string {
  if (groupType === "character") return "角色定装";
  if (groupType === "storyboard") return "分镜制作";
  return "单任务";
}

/** 候选图位选项（设计 6.1：locked 灰显 / storyboard step / 单任务免选） */
function buildCandidateTargetOptions(unit: TaskUnit): CandidateTargetOption[] {
  const groupType = unitGroupType(unit);
  if (groupType === "task") return [];
  if (groupType === "character") {
    return unit.tasks.map((task) => {
      const badge = task.task_variant?.trim() || task.title || task.id.slice(0, 8);
      let disabledReason: string | null = null;
      if (task.status === "completed") disabledReason = "该图位已定版";
      else if (task.locked) disabledReason = "A 图未定版前该图位锁定";
      else if (["submitted", "reviewing"].includes(task.status)) disabledReason = "该图位审核中";
      return {
        taskId: task.id,
        badge,
        title: task.title,
        step: null,
        disabled: disabledReason !== null,
        disabledReason,
      };
    });
  }
  const options: CandidateTargetOption[] = [];
  unit.tasks.forEach((task) => {
    let step: "keyframe" | "video" | null = null;
    if (task.task_type === "text_to_image") step = "keyframe";
    else if (["image_to_video", "video_generation"].includes(task.task_type)) step = "video";
    if (!step) return;
    let disabledReason: string | null = null;
    if (task.status === "completed") disabledReason = "该步骤已定版";
    else if (task.locked) disabledReason = "上游步骤未通过前锁定";
    else if (["submitted", "reviewing"].includes(task.status)) disabledReason = "该步骤审核中";
    else if (step === "video" && !task.keyframe_done) disabledReason = "关键帧审核通过前视频不可导入";
    options.push({
      taskId: task.id,
      badge: step === "keyframe" ? "关键帧" : "视频",
      title: task.title,
      step,
      disabled: disabledReason !== null,
      disabledReason,
    });
  });
  return options;
}

function unitDisplayTitle(unit: TaskUnit): string {
  const groupType = unitGroupType(unit);
  if (groupType === "character") {
    const code = unit.asset?.asset_code || unit.tasks[0].title;
    return unit.asset?.name ? `${unit.asset.name}（${code}）` : code;
  }
  if (groupType === "storyboard") return unit.tasks[0].title || "分镜任务组";
  return unit.tasks[0].title || "任务";
}

type GroupCanvasPageProps = {
  canReview: boolean;
  assets: Asset[];
  projects: Project[];
  workspace: WorkspaceResponse | null;
  tasks: Task[];
  loading: boolean;
  onUploadCandidates: MyTasksPageProps["onUploadCandidates"];
  onSubmitTask: MyTasksPageProps["onSubmitTask"];
  onSubmitCharacterUnit: MyTasksPageProps["onSubmitCharacterUnit"];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onReviewTask: MyTasksPageProps["onReviewTask"];
  onReviewCharacterUnit: MyTasksPageProps["onReviewCharacterUnit"];
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
  onSavePrompt: MyTasksPageProps["onSavePrompt"];
  onGeneratePrompt: MyTasksPageProps["onGeneratePrompt"];
};

export function GroupCanvasPage({
  canReview,
  assets,
  projects,
  workspace,
  tasks,
  loading,
  onUploadCandidates,
  onSubmitTask,
  onSubmitCharacterUnit,
  onDeleteDraftSubmission,
  onReviewTask,
  onReviewCharacterUnit,
  onArchiveSubmission,
  onSetPrimarySubmission,
  onSavePrompt,
  onGeneratePrompt,
}: GroupCanvasPageProps) {
  const visibleTasks = React.useMemo(() => {
    if (!workspace) return tasks;
    const rows = [...(workspace.groups.todo || []), ...(workspace.groups.completed || [])];
    return Array.from(new Map(rows.map((task) => [task.id, task])).values());
  }, [tasks, workspace]);
  const availableProjects = React.useMemo(() => {
    const ids = new Set(visibleTasks.map((task) => task.project_id));
    return projects.filter((project) => ids.has(project.id));
  }, [projects, visibleTasks]);
  const [selectedProjectId, setSelectedProjectId] = React.useState<string>("all");
  const availableEpisodes = React.useMemo(() => {
    const source = selectedProjectId === "all"
      ? visibleTasks
      : visibleTasks.filter((task) => task.project_id === selectedProjectId);
    return Array.from(new Set(source.map((task) => task.episode_code).filter(Boolean) as string[]))
      .sort((left, right) => left.localeCompare(right, "zh-CN", { numeric: true }));
  }, [selectedProjectId, visibleTasks]);
  const [selectedEpisodeCode, setSelectedEpisodeCode] = React.useState<string>("all");
  React.useEffect(() => {
    if (selectedProjectId !== "all" && !availableProjects.some((project) => project.id === selectedProjectId)) {
      setSelectedProjectId("all");
    }
  }, [availableProjects, selectedProjectId]);
  React.useEffect(() => {
    if (selectedEpisodeCode !== "all" && !availableEpisodes.includes(selectedEpisodeCode)) {
      setSelectedEpisodeCode("all");
    }
  }, [availableEpisodes, selectedEpisodeCode]);
  const scopedTasks = React.useMemo(() => visibleTasks.filter((task) => (
    (selectedProjectId === "all" || task.project_id === selectedProjectId)
    && (selectedEpisodeCode === "all" || task.episode_code === selectedEpisodeCode)
  )), [selectedEpisodeCode, selectedProjectId, visibleTasks]);

  const units = React.useMemo(() => buildTaskUnits(scopedTasks, assets), [assets, scopedTasks]);
  const projectNames = React.useMemo(
    () => new Map(projects.map((project) => [project.id, project.title || project.name || "未命名项目"])),
    [projects],
  );

  const [openUnitKey, setOpenUnitKey] = React.useState<string | null>(null);
  const openUnit = React.useMemo(
    () => units.find((unit) => unit.key === openUnitKey) || null,
    [openUnitKey, units],
  );

  const iframeRef = React.useRef<HTMLIFrameElement>(null);
  const [iframeLoaded, setIframeLoaded] = React.useState(false);
  const [loadError, setLoadError] = React.useState(false);
  const [pendingRequest, setPendingRequest] = React.useState<PendingCandidateRequest | null>(null);
  const [importing, setImporting] = React.useState(false);
  const [importError, setImportError] = React.useState<string | null>(null);
  const [candidateNotice, setCandidateNotice] = React.useState<string | null>(null);

  const openGroup = React.useCallback((unitKey: string) => {
    setOpenUnitKey(unitKey);
    setIframeLoaded(false);
    setLoadError(false);
    setPendingRequest(null);
    setImportError(null);
    setCandidateNotice(null);
  }, []);

  const closeGroup = React.useCallback(() => {
    setOpenUnitKey(null);
    setPendingRequest(null);
    setImportError(null);
  }, []);

  const postToIframe = React.useCallback((message: Record<string, unknown>): void => {
    const iframe = iframeRef.current;
    if (!iframe?.contentWindow) return;
    iframe.contentWindow.postMessage({ ...message, source: PROTOCOL_SOURCE }, "*");
  }, []);

  const canvasSrc = React.useMemo(() => {
    if (!openUnit) return "";
    const primaryTaskId = openUnit.tasks[0].id;
    return `${__CANVAS_URL__}/task-canvas?task=${encodeURIComponent(primaryTaskId)}`;
  }, [openUnit]);

  /** iframe 加载完成：注入 SSO 会话 + 推送 GROUP_CONTEXT（设计 3.5 / 风险 R4 重发） */
  const handleIframeLoad = React.useCallback(() => {
    setIframeLoaded(true);
    setLoadError(false);
    const tapcanvasToken = getTapToken();
    const platformToken = getPlatformToken();
    const sessionExpiresAt = getSessionExpiresAt();
    const iframe = iframeRef.current;
    if (!iframe?.contentWindow || !tapcanvasToken || !platformToken || !sessionExpiresAt || !openUnit) return;
    const targetOrigin = new URL(canvasSrc, window.location.origin).origin;
    iframe.contentWindow.postMessage(
      createCineForgeSsoSessionMessage(
        tapcanvasToken,
        platformToken,
        sessionExpiresAt,
        `/task-canvas?task=${encodeURIComponent(openUnit.tasks[0].id)}`,
      ),
      targetOrigin,
    );
    const groupTasks: GroupContextTask[] = openUnit.tasks.map((task) => ({
      taskId: task.id,
      title: task.title,
      taskVariant: task.task_variant ?? null,
      status: task.status,
      locked: task.locked ?? false,
      keyframeDone: task.keyframe_done ?? false,
      taskType: task.task_type,
    }));
    const pushContext = () => postToIframe({ type: "GROUP_CONTEXT", tasks: groupTasks });
    pushContext();
    const retry = window.setInterval(pushContext, 2000);
    const stopRetry = () => {
      window.clearInterval(retry);
      window.removeEventListener("message", onAck);
    };
    const onAck = (event: MessageEvent) => {
      if (event.source !== iframe.contentWindow) return;
      const data = event.data as { source?: string; type?: string } | null;
      if (data?.source === PROTOCOL_SOURCE && data.type === "GROUP_CONTEXT_ACK") stopRetry();
    };
    window.addEventListener("message", onAck);
  }, [canvasSrc, openUnit, postToIframe]);

  /** iframe 加载失败兜底（对齐 CanvasPage 15s 超时） */
  React.useEffect(() => {
    if (!openUnit || iframeLoaded) return;
    const timer = window.setTimeout(() => setLoadError(true), 15000);
    return () => window.clearTimeout(timer);
  }, [iframeLoaded, openUnit]);

  /** 监听 iframe 候选请求（NODE_CANDIDATE_REQUEST → 图位菜单 → go-api 导入 → RESULT 回推） */
  React.useEffect(() => {
    const handler = (event: MessageEvent) => {
      if (openUnit && iframeRef.current && event.source !== iframeRef.current.contentWindow) return;
      const data = event.data as NodeCandidateRequest | { source?: string; type?: string } | null;
      if (!data || data.source !== PROTOCOL_SOURCE) return;
      if (data.type === "NODE_CANDIDATE_REQUEST") {
        const request = data as NodeCandidateRequest;
        setImportError(null);
        setCandidateNotice(null);
        setPendingRequest({
          nodeId: request.nodeId,
          assetId: request.assetId,
          flowId: request.flowId,
        });
      }
    };
    window.addEventListener("message", handler);
    return () => window.removeEventListener("message", handler);
  }, [openUnit]);

  /** 执行候选导入（go-api candidates:import，设计 6.2） */
  const runImport = React.useCallback(async (
    unit: TaskUnit,
    request: PendingCandidateRequest,
    targetTaskId: string | null,
    step: "keyframe" | "video" | null,
  ): Promise<void> => {
    setImporting(true);
    setImportError(null);
    const fail = (message: string) => {
      postToIframe({
        type: "NODE_CANDIDATE_RESULT",
        nodeId: request.nodeId,
        ok: false,
        error: message,
      });
      setImportError(message);
    };
    try {
      const body: Record<string, unknown> = {
        assetId: request.assetId,
        nodeId: request.nodeId,
        flowId: request.flowId,
        idempotencyKey: createClientUuid(),
      };
      if (targetTaskId) body.targetTaskId = targetTaskId;
      if (step) body.step = step;
      const response = await platformFetch(
        `/workbench/tasks/${encodeURIComponent(unit.tasks[0].id)}/candidates:import`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        },
      );
      if (!response.ok) {
        let message = `候选导入失败（${response.status}）`;
        try {
          const payload = (await response.json()) as { message?: string };
          if (payload?.message) message = payload.message;
        } catch {
          // keep the status-based message
        }
        fail(message);
        return;
      }
      const result = (await response.json()) as { submissionId?: string; taskId?: string };
      postToIframe({
        type: "NODE_CANDIDATE_RESULT",
        nodeId: request.nodeId,
        ok: true,
        submissionId: result.submissionId || null,
        targetTaskId: targetTaskId || result.taskId || unit.tasks[0].id,
      });
      setCandidateNotice(`候选已导入任务（${targetTaskId ? "指定图位" : "当前任务"}）`);
      setPendingRequest(null);
    } catch (reason: unknown) {
      fail(reason instanceof Error ? reason.message : "候选导入失败");
    } finally {
      setImporting(false);
    }
  }, [postToIframe]);

  /** 单任务组免选直接导入（设计 6.1） */
  React.useEffect(() => {
    if (!openUnit || !pendingRequest || importing) return;
    if (unitGroupType(openUnit) !== "task") return;
    void runImport(openUnit, pendingRequest, null, null);
  }, [importing, openUnit, pendingRequest, runImport]);

  const cancelRequest = React.useCallback((): void => {
    if (pendingRequest) {
      postToIframe({
        type: "NODE_CANDIDATE_RESULT",
        nodeId: pendingRequest.nodeId,
        ok: false,
        error: "cancelled",
      });
    }
    setPendingRequest(null);
    setImportError(null);
  }, [pendingRequest, postToIframe]);

  const refreshIframe = React.useCallback(() => {
    setIframeLoaded(false);
    setLoadError(false);
    if (iframeRef.current) {
      const src = iframeRef.current.src;
      iframeRef.current.src = "about:blank";
      window.setTimeout(() => {
        if (iframeRef.current) iframeRef.current.src = src;
      }, 50);
    }
  }, []);

  /* ---------------- 分组列表视图 ---------------- */
  if (!openUnit) {
    return (
      <section className="panel" style={{ padding: 20 }}>
        <div className="admin-filter-row" style={{ display: "flex", gap: 8, flexWrap: "wrap", marginBottom: 16 }}>
          <label className="muted" style={{ fontSize: 12 }}>项目</label>
          <button
            type="button"
            className={`btn ${selectedProjectId === "all" ? "primary" : ""}`}
            style={{ padding: "4px 12px", fontSize: 12 }}
            onClick={() => { setSelectedProjectId("all"); setSelectedEpisodeCode("all"); }}
          >
            全部项目
          </button>
          {availableProjects.map((project) => (
            <button
              key={project.id}
              type="button"
              className={`btn ${selectedProjectId === project.id ? "primary" : ""}`}
              style={{ padding: "4px 12px", fontSize: 12 }}
              onClick={() => { setSelectedProjectId(project.id); setSelectedEpisodeCode("all"); }}
            >
              {project.title || project.name}
            </button>
          ))}
          {availableEpisodes.length > 0 ? (
            <>
              <span className="muted" style={{ fontSize: 12, marginLeft: 12 }}>分集</span>
              <button
                type="button"
                className={`btn ${selectedEpisodeCode === "all" ? "primary" : ""}`}
                style={{ padding: "4px 12px", fontSize: 12 }}
                onClick={() => setSelectedEpisodeCode("all")}
              >
                全部分集
              </button>
              {availableEpisodes.map((episodeCode) => (
                <button
                  key={episodeCode}
                  type="button"
                  className={`btn ${selectedEpisodeCode === episodeCode ? "primary" : ""}`}
                  style={{ padding: "4px 12px", fontSize: 12 }}
                  onClick={() => setSelectedEpisodeCode(episodeCode)}
                >
                  {episodeCode}
                </button>
              ))}
            </>
          ) : null}
        </div>
        {loading ? <div className="empty-hint">任务加载中…</div> : null}
        {!loading && units.length === 0 ? <div className="empty-hint">当前筛选下没有任务分组。</div> : null}
        <div style={{ display: "grid", gap: 10 }}>
          {units.map((unit) => {
            const groupType = unitGroupType(unit);
            const completed = unit.tasks.filter((task) => task.status === "completed").length;
            return (
              <article
                key={unit.key}
                className="single-production-unit"
                style={{ display: "flex", alignItems: "center", gap: 14, padding: "12px 14px" }}
              >
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                    <strong>{unitDisplayTitle(unit)}</strong>
                    <span className={`badge ${unit.queue === "completed" ? "mint" : unit.queue === "reviewing" ? "sky" : "sun"}`}>
                      {queueLabel(unit.queue)}
                    </span>
                    <span className="badge">{groupTypeLabel(groupType)}</span>
                  </div>
                  <div className="muted" style={{ fontSize: 12, marginTop: 4 }}>
                    {projectNames.get(unit.tasks[0].project_id) || "未命名项目"}
                    {unit.tasks[0].episode_code ? ` · ${unit.tasks[0].episode_code}` : ""}
                    {" · "}{unit.tasks.length} 个成员任务 · {completed}/{unit.tasks.length} 已定版
                  </div>
                </div>
                <button type="button" className="btn primary" onClick={() => openGroup(unit.key)}>
                  打开画布
                </button>
              </article>
            );
          })}
        </div>
      </section>
    );
  }

  /* ---------------- 分组画布视图 ---------------- */
  const groupType = unitGroupType(openUnit);
  const targetOptions = buildCandidateTargetOptions(openUnit);

  return (
    <section style={{ width: "100%", height: "100%", display: "flex", flexDirection: "column", gap: 10 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <button type="button" className="btn" onClick={closeGroup}>
          <ChevronLeft size={14} /> 返回分组列表
        </button>
        <strong>{unitDisplayTitle(openUnit)}</strong>
        <span className={`badge ${openUnit.queue === "completed" ? "mint" : openUnit.queue === "reviewing" ? "sky" : "sun"}`}>
          {queueLabel(openUnit.queue)}
        </span>
        <span className="badge">{groupTypeLabel(groupType)}</span>
        <button type="button" className="btn" onClick={refreshIframe} title="刷新画布">
          <RefreshCw size={14} />
        </button>
        {candidateNotice ? <span className="muted" style={{ fontSize: 12 }}>{candidateNotice}</span> : null}
        {importError ? <span style={{ fontSize: 12, color: "#dc2626" }}>{importError}</span> : null}
      </div>

      <div style={{ position: "relative", flex: "1 1 56%", minHeight: 320, border: "1px solid rgba(15,23,42,0.08)", borderRadius: 8, overflow: "hidden", background: "#fff" }}>
        {!iframeLoaded && !loadError ? (
          <div className="empty-hint" style={{ position: "absolute", inset: 0, display: "flex", alignItems: "center", justifyContent: "center", zIndex: 5, background: "rgba(248,250,252,0.95)" }}>
            画布加载中…
          </div>
        ) : null}
        {loadError && !iframeLoaded ? (
          <div style={{ position: "absolute", inset: 0, display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: 10, zIndex: 5, background: "rgba(248,250,252,0.95)" }}>
            <strong style={{ color: "#dc2626" }}>画布加载失败</strong>
            <span className="muted" style={{ fontSize: 12 }}>请确认 React-Flow 画布服务可用：{canvasSrc}</span>
            <button type="button" className="btn" onClick={refreshIframe}>重试</button>
          </div>
        ) : null}
        <iframe
          ref={iframeRef}
          src={canvasSrc}
          title="React-Flow Group Canvas"
          onLoad={handleIframeLoad}
          style={{ width: "100%", height: "100%", border: "none", display: "block" }}
          allow="clipboard-read; clipboard-write; fullscreen"
        />
      </div>

      <div style={{ flex: "1 1 44%", minHeight: 220, overflow: "auto", padding: "0 2px" }}>
        {groupType === "character" ? (
          <CharacterTaskTable
            units={[openUnit]}
            canReview={canReview}
            loading={loading}
            projectNames={projectNames}
            onUploadCandidates={onUploadCandidates}
            onSubmitCharacterUnit={onSubmitCharacterUnit}
            onReviewCharacterUnit={onReviewCharacterUnit}
            onDeleteDraftSubmission={onDeleteDraftSubmission}
            onArchiveSubmission={onArchiveSubmission}
            onSetPrimarySubmission={onSetPrimarySubmission}
            onSavePrompt={onSavePrompt}
            onGeneratePrompt={onGeneratePrompt}
          />
        ) : null}
        {groupType === "storyboard" ? (
          <StoryboardProductionWorkbench
            units={[openUnit]}
            loading={loading}
            projectNames={projectNames}
            onUploadCandidates={onUploadCandidates}
            onSubmitTask={onSubmitTask}
            onDeleteDraftSubmission={onDeleteDraftSubmission}
            onReviewTask={onReviewTask}
            onArchiveSubmission={onArchiveSubmission}
            onSetPrimarySubmission={onSetPrimarySubmission}
            onSavePrompt={onSavePrompt}
            onGeneratePrompt={onGeneratePrompt}
          />
        ) : null}
        {groupType === "task" ? openUnit.tasks.map((task) => (
          <TaskWorkItem
            key={task.id}
            task={task}
            canReview={canReview}
            loading={loading}
            projectName={projectNames.get(task.project_id) || "未命名项目"}
            onArchiveSubmission={onArchiveSubmission}
            onGeneratePrompt={onGeneratePrompt}
            onDeleteDraftSubmission={onDeleteDraftSubmission}
            onReviewTask={onReviewTask}
            onSavePrompt={onSavePrompt}
            onSetPrimarySubmission={onSetPrimarySubmission}
            onSubmitTask={onSubmitTask}
            onUploadCandidates={onUploadCandidates}
          />
        )) : null}
      </div>

      {/* 候选图位选择菜单（设计 6.1：用户显式指定图位） */}
      {pendingRequest && groupType !== "task" ? (
        <div
          style={{ position: "fixed", inset: 0, zIndex: 60, display: "flex", alignItems: "center", justifyContent: "center", background: "rgba(15,23,42,0.35)" }}
          onClick={(event) => { if (event.target === event.currentTarget && !importing) cancelRequest(); }}
        >
          <div className="panel" style={{ width: 360, padding: 18, background: "#fff" }}>
            <h3 style={{ margin: "0 0 6px", fontSize: 15 }}>选择候选图位</h3>
            <p className="muted" style={{ margin: "0 0 12px", fontSize: 12 }}>
              节点成果将导入所选图位任务（{groupTypeLabel(groupType)}组）。
            </p>
            <div style={{ display: "grid", gap: 8 }}>
              {targetOptions.map((option) => (
                <button
                  key={option.taskId}
                  type="button"
                  className="btn"
                  disabled={option.disabled || importing}
                  title={option.disabledReason ?? undefined}
                  style={{
                    display: "flex", alignItems: "center", justifyContent: "space-between",
                    textAlign: "left", opacity: option.disabled ? 0.5 : 1,
                    cursor: option.disabled ? "not-allowed" : "pointer",
                  }}
                  onClick={() => {
                    if (importing) return;
                    void runImport(openUnit, pendingRequest, option.taskId, option.step);
                  }}
                >
                  <span><strong>{option.badge}</strong> · {option.title}</span>
                  <span className="muted" style={{ fontSize: 11 }}>
                    {option.disabledReason || (option.step ? `step: ${option.step}` : "可导入")}
                  </span>
                </button>
              ))}
              {targetOptions.length === 0 ? (
                <div className="empty-hint">该分组当前没有可导入的图位/步骤。</div>
              ) : null}
            </div>
            {importError ? <p style={{ margin: "10px 0 0", fontSize: 12, color: "#dc2626" }}>{importError}</p> : null}
            <div style={{ display: "flex", justifyContent: "flex-end", gap: 8, marginTop: 14 }}>
              <button type="button" className="btn" disabled={importing} onClick={cancelRequest}>取消</button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  );
}

export default GroupCanvasPage;

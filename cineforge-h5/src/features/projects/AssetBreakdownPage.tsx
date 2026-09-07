import React from "react";
import { AlertTriangle, CheckCircle2, LoaderCircle, X } from "lucide-react";
import { isOwnerAssignableProp } from "../../assetOwner";
import { Button, Panel, Stepper } from "../../shared/components";
import type { StepperStep } from "../../shared/components";
import type { AgentRun, BreakdownDraftAsset, BreakdownDraftData, BreakdownManualReviewItem, Project, ScriptSegment, Storyboard, Task, TemporaryAssetProductionPayload } from "../../types";
import { assetParticipatesInLifecycleGate } from "../../assetLifecycle";
import { withFreshAssetConfirmation } from "../../assetNormalization";
import { isOptionalAssetCompletionField } from "../../assetCompletion";
import { createClientRequestId } from "../../clientId";
import { assetCodeDisplay, assetsMissingPromptDesign, assetsNeedingCompletion, breakdownDraft, canonicalSceneCode, hasPendingAgentRun, isRequiredPendingReview, mergeBreakdownDraft, sceneAssetDisplayLabel, sceneDisplayLabel, textValue } from "../../utils";
import { EditableBreakdownDraft } from "./EditableBreakdownDraft";
import { ReadingReportView } from "./ReadingReportView";
import { WorkflowProgressPanel } from "./WorkflowProgressPanel";

type ResultStage = "reading" | "assets" | "prompts" | "segmentation" | "storyboard";

type AssetBreakdownPageProps = {
  project?: Project;
  episodeId?: string;
  episodeCode?: string;
  scriptVersionId?: string;
  onOpenTaskAssignment?: () => void;
  runs: AgentRun[];
  embedded?: boolean;
  scriptSegments: ScriptSegment[];
  storyboards: Storyboard[];
  tasks: Task[];
  draftOverride?: BreakdownDraftData;
  draftSyncing?: boolean;
  loading: boolean;
  readOnly?: boolean;
  canCreateTemporaryAsset?: boolean;
  onSaveDraft: (draft: BreakdownDraftData) => Promise<boolean | void>;
  onConfirmScriptSegments: (draft: BreakdownDraftData) => Promise<void>;
  onRunStep?: (step: string) => Promise<boolean | void> | boolean | void;
  onRetryPromptDistribution?: (runId: string) => Promise<void>;
  onCreateTemporaryAssetProduction?: (payload: TemporaryAssetProductionPayload) => Promise<boolean>;
  onLockEpisode: (episodeCode: string, draft: BreakdownDraftData) => Promise<void>;
};

export function AssetBreakdownPage({
  project,
  episodeId,
  episodeCode,
  scriptVersionId,
  onOpenTaskAssignment,
  runs,
  embedded,
  scriptSegments,
  storyboards,
  tasks,
  draftOverride,
  draftSyncing = false,
  loading,
  readOnly = false,
  canCreateTemporaryAsset = false,
  onSaveDraft,
  onConfirmScriptSegments,
  onRunStep,
  onRetryPromptDistribution,
  onCreateTemporaryAssetProduction,
  onLockEpisode,
}: AssetBreakdownPageProps) {
  const orderedBreakdownRuns = React.useMemo(
    () => runs.filter((run) => run.agent_type === "script_breakdown").slice().sort((left, right) => runTimestamp(right) - runTimestamp(left)),
    [runs],
  );
  const latestBreakdownRun = orderedBreakdownRuns[0];
  const latestPromptRun = orderedBreakdownRuns.find((run) => String(run.input?.single_step || "") === "asset_prompt_generation");
  const latestReadingRun = runs.find((run) => ["script_reading", "script_breakdown"].includes(run.agent_type));
  const latestBreakdownStatus = latestBreakdownRun ? effectiveAgentRunStatus(latestBreakdownRun) : null;
  const workflowRunning = hasPendingAgentRun(runs, "script_breakdown") || hasPendingAgentRun(runs, "script_segmentation");
  const scriptSegmentsConfirmed = Boolean(scriptSegments.length) && scriptSegments.every((segment) => (
    String((segment.metadata_json || {}).status || "") === "script_confirmed"
    && Array.isArray((segment.metadata_json || {}).dialogue_lines)
  ));
  const draft = React.useMemo(
    () => mergeBreakdownDraft(project, draftOverride || breakdownDraft(null), scriptSegments, storyboards),
    [project?.id, draftOverride, scriptSegments, storyboards],
  );
  const promptFinalization = objectValue(draft.assetPromptFinalization);
  const promptTaskDistribution = objectValue(promptFinalization.task_distribution);
  const authoritativeReadingDraft = draftOverride || draft;
  const readingConfirmed = authoritativeReadingDraft.readingReviewState.status === "confirmed";
  const readingReport = authoritativeReadingDraft.readingReport;
  const hasReadingReport = Boolean(readingReport && Object.keys(readingReport).length);
  const roleNames = React.useMemo(() => readingRoleNameMap(draft), [draft]);
  const hasAssetDraft = Boolean(draft.assets.length);
  const hasPromptDraft = hasAssetDraft && assetsMissingPromptDesign(draft).length === 0;
  const hasStoryboardDraft = Boolean(draft.storyboards.length || storyboards.length);
  const promptsConfirmed = String(draft.confirmedPromptStatus || "") === "confirmed"
    || scriptSegmentsConfirmed
    || Boolean(draft.storyboards.length || storyboards.length);
  const assetInventoryConfirmed = draft.assetReviewState.status === "confirmed";
  const assetReviewBlockers = assetConfirmationBlockers(draft);
  const storyboardTasksDistributed = tasks.some((task) => (
    task.task_type === "storyboard_shot"
    || Boolean(task.storyboard_id && ["text_to_image", "image_to_video"].includes(task.task_type))
  ));
  const pendingStep = runs.find((run) => isPendingRun(run) && ["script_breakdown", "script_segmentation"].includes(run.agent_type));
  const pendingStepKey = breakdownStepKey(pendingStep);
  // 优先选择"待确认"状态的步骤作为默认展示，确保用户能直接看到需要确认的内容
  const getInitialResultStage = (): ResultStage => {
    // 1. 优先：处于 needs_review 状态的步骤
    if (!readingConfirmed && hasReadingReport) return "reading";
    if (!assetInventoryConfirmed && hasAssetDraft) return "assets";
    if (!scriptSegmentsConfirmed && (scriptSegments.length > 0 || draft.scriptSegments.length > 0)) return "segmentation";

    // 2. 其次：已完成但尚未确认的最近步骤
    if (hasStoryboardDraft) return "storyboard";
    if (scriptSegments.length > 0 || draft.scriptSegments.length > 0) return "segmentation";
    if (hasAssetDraft && hasPromptDraft) return "prompts";
    if (hasAssetDraft) return "assets";
    if (readingConfirmed || hasReadingReport) return "reading";

    // 3. 默认
    return "reading";
  };
  const initialResultStage: ResultStage = getInitialResultStage();

  const [runningStep, setRunningStep] = React.useState<string | null>(null);
  const [confirmingStep, setConfirmingStep] = React.useState<string | null>(null);
  const [stepError, setStepError] = React.useState<string | null>(null);
  const [confirmToast, setConfirmToast] = React.useState<string | null>(null);
  const [resultStage, setResultStage] = React.useState<ResultStage>(initialResultStage);
  const [temporaryProductionOpen, setTemporaryProductionOpen] = React.useState(false);
  const [temporaryAssetType, setTemporaryAssetType] = React.useState<TemporaryProductionAssetType>("character");
  React.useEffect(() => {
    setResultStage(initialResultStage);
  }, [project?.id, initialResultStage]);
  React.useEffect(() => {
    if (!workflowRunning) setRunningStep(null);
  }, [workflowRunning]);

  function runStep(key: string) {
    if (!onRunStep) return;
    if (key === "prompts" && (hasPromptDraft || latestPromptRun?.status === "succeeded")) {
      const confirmed = window.confirm("提示词已经生成过。再次执行会创建新的 Prompt 版本，并在完成后重新核对统一任务分发。确认继续吗？");
      if (!confirmed) return;
    }
    setResultStage(key as ResultStage);
    setRunningStep(key);
    void onRunStep(key);
  }

  function requestTemporaryAsset(assetType: TemporaryProductionAssetType) {
    setTemporaryAssetType(assetType);
    setTemporaryProductionOpen(true);
  }

  function scopedDraft(nextDraft: BreakdownDraftData): BreakdownDraftData {
    return {
      ...nextDraft,
      readingRevisionContext: {
        ...(nextDraft.readingRevisionContext || {}),
        ...(episodeId ? { episode_id: episodeId } : {}),
        ...(episodeCode ? { episode_code: episodeCode } : {}),
        ...(scriptVersionId ? { script_version_id: scriptVersionId } : {}),
      },
    };
  }

  async function confirmReading() {
    if (!readingReport || readingConfirmed || confirmingStep) return;
    setConfirmingStep("reading");
    setStepError(null);
    try {
      const saved = await onSaveDraft(scopedDraft(withConfirmedReadingReport(authoritativeReadingDraft, readingReport)));
      if (saved === false) return;
      setResultStage("assets");
      setConfirmToast("围读结果已确认，可开始资产拆解。");
      window.setTimeout(() => setConfirmToast(null), 3000);
    } finally {
      setConfirmingStep(null);
    }
  }

  async function confirmAssets() {
    if (!hasAssetDraft || assetInventoryConfirmed || confirmingStep) return;
    const incomplete = assetsNeedingCompletion(draft);
    if (incomplete.length || assetReviewBlockers.length) {
      setStepError(`还有 ${Math.max(incomplete.length, assetReviewBlockers.length)} 项资产信息需要完善，暂不能确认资产拆解。`);
      setResultStage("assets");
      return;
    }
    setConfirmingStep("assets");
    setStepError(null);
    try {
      const confirmedDraft = withFreshAssetConfirmation(draft);
      const saved = await onSaveDraft(scopedDraft(confirmedDraft));
      if (saved === false) return;
      setConfirmToast("资产拆解已确认，可进入提示词生成阶段。");
      window.setTimeout(() => setConfirmToast(null), 3000);
    } finally {
      setConfirmingStep(null);
    }
  }

  async function confirmSegmentation() {
    if (!draft.scriptSegments.length || scriptSegmentsConfirmed || confirmingStep) return;
    setConfirmingStep("segmentation");
    try {
      await onConfirmScriptSegments(scopedDraft(draft));
    } finally {
      setConfirmingStep(null);
    }
  }

  function stepStatus(key: string): StepperStep["status"] {
    if ((runningStep === key && workflowRunning) || pendingStepKey === key) return "running";
    switch (key) {
      case "reading":
        return readingConfirmed ? "confirmed" : hasReadingReport ? "needs_review" : "ready";
      case "segmentation":
        if (!hasPromptDraft) return "locked";
        return scriptSegmentsConfirmed ? "confirmed" : scriptSegments.length || draft.scriptSegments.length ? "needs_review" : "ready";
      case "assets":
        if (!readingConfirmed) return "locked";
        return assetInventoryConfirmed ? "confirmed" : hasAssetDraft ? "needs_review" : "ready";
      case "prompts":
        if (!assetInventoryConfirmed) return "locked";
        return hasPromptDraft ? "succeeded" : "ready";
      case "storyboard":
        if (!scriptSegmentsConfirmed) return "locked";
        return hasStoryboardDraft ? "succeeded" : "ready";
      default:
        return "locked";
    }
  }

  function stepStatusLabel(key: string, status: StepperStep["status"]): string {
    const labels: Record<string, Partial<Record<string, string>>> = {
      reading: { confirmed: "已完成", needs_review: "结果待确认", ready: "可以开始", locked: "等待上一步", running: "执行中", failed: "执行失败" },
      assets: { confirmed: "已完成", needs_review: "资产待确认", ready: "可以开始", locked: "等待上一步", running: "执行中", failed: "执行失败" },
      prompts: { succeeded: "已完成", ready: "可以开始", locked: "等待上一步", running: "执行中", failed: "执行失败" },
      segmentation: { confirmed: "已完成", needs_review: "结果待确认", ready: "可以开始", locked: "等待上一步", running: "执行中", failed: "执行失败" },
      storyboard: { succeeded: "已完成", ready: "可以开始", locked: "等待上一步", running: "执行中", failed: "执行失败" },
    };
    return labels[key]?.[status || "pending"] ?? String(status || "pending");
  }

  const pipelineSteps: StepperStep[] = [
    { key: "reading", label: "剧本围读" },
    { key: "assets", label: "资产拆解" },
    { key: "prompts", label: "提示词生成" },
    { key: "segmentation", label: "脚本段拆分" },
    { key: "storyboard", label: "分镜拆解" },
  ].map((step) => {
    const status = stepStatus(step.key);
    const isConfirmAction = status === "needs_review";
    const waitingForReadingConfirmation = step.key === "reading" && status === "needs_review";
    const canViewResult = ["succeeded", "done", "confirmed"].includes(String(status));
    const isAssetsStepNeedsReview = step.key === "assets" && status === "needs_review";
    const isSegmentationStepNeedsReview = step.key === "segmentation" && status === "needs_review";
    return {
      ...step,
      status,
      statusLabel: stepStatusLabel(step.key, status),
      selected: resultStage === step.key,
      onSelect: canViewResult ? () => setResultStage(step.key as ResultStage) : undefined,
      onRun: waitingForReadingConfirmation
        ? () => void confirmReading()
        : onRunStep && ["ready", "failed"].includes(String(status)) ? () => runStep(step.key) : undefined,
      onSecondaryRun: onRunStep && step.key === "prompts" && ["succeeded", "done"].includes(String(status)) ? () => runStep(step.key) : undefined,
      secondaryRunLabel: step.key === "prompts" ? "重新生成" : undefined,
      runLabel: waitingForReadingConfirmation
        ? confirmingStep === "reading" ? "确认中..." : "确认围读结果"
        : isConfirmAction && !isAssetsStepNeedsReview && !isSegmentationStepNeedsReview ? `确认${step.label}结果` : status === "failed" ? "重新运行" : `开始${step.label}`,
      runVariant: isConfirmAction && !isAssetsStepNeedsReview && !isSegmentationStepNeedsReview ? "confirm" : "start",
      disabled: loading || readOnly || workflowRunning || Boolean(confirmingStep) || (step.key === "reading" && readingConfirmed) || (step.key === "assets" && (!readingConfirmed || assetInventoryConfirmed)) || (step.key === "prompts" && !assetInventoryConfirmed) || (step.key === "segmentation" && (!hasPromptDraft || scriptSegmentsConfirmed)) || (step.key === "storyboard" && (!scriptSegmentsConfirmed || storyboardTasksDistributed)),
      customActions: isAssetsStepNeedsReview ? (
        <button
          className="pipeline-run is-confirm"
          type="button"
          disabled={loading || readOnly || Boolean(confirmingStep)}
          onClick={() => confirmAssets()}
        >
          {confirmingStep === "assets" ? "确认中..." : "批量确认"}
        </button>
      ) : isSegmentationStepNeedsReview ? (
        <button
          className="pipeline-run is-confirm"
          type="button"
          disabled={loading || readOnly || Boolean(confirmingStep)}
          onClick={() => confirmSegmentation()}
        >
          {confirmingStep === "segmentation" ? "确认中..." : "确认脚本段"}
        </button>
      ) : undefined,
  }});

  return (
    <>
      {confirmToast ? (
        <div className="pipeline-confirm-toast" role="status" aria-live="polite">
          <i className="pipeline-confirm-toast__dot" aria-hidden="true" />
          <span>{confirmToast}</span>
        </div>
      ) : null}
      {latestBreakdownRun && latestBreakdownStatus === "failed" ? (
        <div className="notice error agent-flow-notice">
          <strong>⚠️ 剧本拆解流程失败</strong>
          <span>{agentRunFailureText(latestBreakdownRun)}</span>
          <span>请检查剧本内容或联系技术支持。</span>
        </div>
      ) : null}
      {scriptSegmentsConfirmed && !hasStoryboardDraft ? (
        <div className="notice agent-flow-notice">
          <strong>✅ 脚本段已确认入库（{scriptSegments.length} 条）</strong>
          <span>下一步：在流水线中运行「分镜拆解」，生成分镜脚本和资产详情。</span>
        </div>
      ) : null}
      {scriptSegmentsConfirmed && draft.storyboards.length > 0 && storyboards.length === 0 ? (
        <div className="notice agent-flow-notice">
          <strong>分镜预定稿已生成（{draft.storyboards.length} 条）</strong>
          <span>请在分镜预定稿中核对 Agent 复核项和镜头内容，确认后再锁定本集。</span>
        </div>
      ) : null}
      {scriptSegmentsConfirmed && storyboards.length > 0 ? (
        <div className="notice success agent-flow-notice">
          <strong>✅ 拆解流程已完成</strong>
          <span>脚本段 {scriptSegments.length} 条、分镜 {storyboards.length} 条已入库。可人工修改后保存，或前往任务分发。</span>
        </div>
      ) : null}

      {onRunStep ? (() => {
        const pipelineRunningStep = pipelineSteps.find((step) => step.status === "running");
        const pipelineFailedStep = pipelineSteps.find((step) => step.status === "failed");
        const pipelineReadyStep = pipelineSteps.find((step) => step.status === "ready" || step.status === "current");
        const pipelineComplete = pipelineSteps.every((step) => ["succeeded", "done", "confirmed"].includes(String(step.status)));
        return (
          <>
            <div className={`pipeline-status-banner ${pipelineRunningStep ? "running" : pipelineFailedStep ? "failed" : pipelineComplete ? "complete" : "ready"}`}>
              <span className="pipeline-status-banner__icon">
                {pipelineRunningStep ? <LoaderCircle className="pipeline-node-spinner" size={17} /> : null}
                {pipelineFailedStep ? <AlertTriangle size={17} /> : null}
                {pipelineComplete ? <CheckCircle2 size={17} /> : null}
                {!pipelineRunningStep && !pipelineFailedStep && !pipelineComplete ? <span className="pipeline-ready-dot" /> : null}
              </span>
              <div>
                <strong>
                  {pipelineRunningStep
                    ? pipelineRunningStep.statusLabel
                    : pipelineFailedStep
                      ? `${pipelineFailedStep.label}执行失败`
                      : pipelineComplete
                        ? "本集制作流程已完成"
                        : pipelineReadyStep?.statusLabel === "结果待确认"
                          ? `${pipelineReadyStep.label}结果待确认`
                          : `下一步：${pipelineReadyStep?.label || "等待处理"}`}
                </strong>
                <span>
                  {pipelineRunningStep
                    ? "Agent 正在后台处理，完成后会自动刷新并解锁下一步。"
                    : pipelineFailedStep
                      ? "请查看错误信息并重新运行，后续步骤仍保持锁定。"
                      : pipelineComplete
                        ? "所有步骤均已完成，可以进入任务分发或新增下一集。"
                        : pipelineReadyStep?.statusLabel === "结果待确认"
                          ? `确认${pipelineReadyStep.label}结果后，系统将解锁下一步。`
                          : "完成当前步骤后，系统会自动解锁下一步。"}
                </span>
              </div>
            </div>
            <Stepper steps={pipelineSteps} readOnly={readOnly} />
          </>
        );
      })() : null}
      {workflowRunning ? <WorkflowProgressPanel run={pendingStep} /> : null}
      {!workflowRunning && draftSyncing ? (
        <div className="notice agent-flow-notice" role="status" aria-live="polite">
          <strong>正在同步最新结果…</strong>
          <span>节点已完成，正在拉取最新拆解结果，无需手动刷新。</span>
        </div>
      ) : null}
      {stepError ? <div className="notice error" role="alert">{stepError}</div> : null}
      {resultStage === "reading" ? (
        <ReadingOutcomePanel report={readingReport} latestRun={latestReadingRun} confirmed={readingConfirmed} roleNames={roleNames} />
      ) : null}
      {resultStage === "assets" ? (
        <>
        <EditableBreakdownDraft
          projectId={project?.id}
          stage="assets"
          draft={draft}
          loading={loading}
          readOnly={readOnly}
          scriptSegmentsConfirmed={scriptSegmentsConfirmed}
          promptsConfirmed={promptsConfirmed}
          onSaveDraft={(nextDraft) => onSaveDraft(scopedDraft(nextDraft))}
          assetInventoryConfirmed={assetInventoryConfirmed}
          onRequestTemporaryAsset={canCreateTemporaryAsset ? requestTemporaryAsset : undefined}
          onConfirmScriptSegments={(nextDraft) => onConfirmScriptSegments(scopedDraft(nextDraft))}
          onLockEpisode={(code, nextDraft) => onLockEpisode(code, scopedDraft(nextDraft))}
          onBatchConfirmAssets={() => confirmAssets()}
          batchConfirming={confirmingStep === "assets"}
        />
        </>
      ) : null}
      {resultStage === "prompts" ? (
        <>
        {promptFinalization.status === "succeeded" && promptTaskDistribution.created_tasks !== undefined ? <div className="notice success agent-flow-notice">
          <strong>资产任务已统一分发</strong>
          <span>人物 {numberValue(promptTaskDistribution.character_tasks)} / 场景 {numberValue(promptTaskDistribution.scene_tasks)} / 道具 {numberValue(promptTaskDistribution.prop_tasks)}</span>
          <span>主题音乐 {numberValue(promptTaskDistribution.theme_music_tasks)} / 背景音乐 {numberValue(promptTaskDistribution.background_music_tasks)} / 角色音色 {numberValue(promptTaskDistribution.character_voice_tasks)} / 复用 {numberValue(promptTaskDistribution.reused_tasks)}</span>
          {onOpenTaskAssignment ? <Button disabled={loading || readOnly} onClick={onOpenTaskAssignment}>进入任务分发 →</Button> : null}
        </div> : null}
        {promptFinalization.status === "distribution_failed" ? <div className="notice error agent-flow-notice">
          <strong>资产提示词已完成，任务分发失败</strong>
          <span>{textValue(promptFinalization.error) || "可直接重试任务分发，不会重新运行提示词 Agent。"}</span>
          {latestPromptRun && onRetryPromptDistribution ? (
            <Button disabled={loading || workflowRunning} onClick={() => void onRetryPromptDistribution(latestPromptRun.id)}>重试任务分发</Button>
          ) : null}
        </div> : null}
        <EditableBreakdownDraft
          projectId={project?.id}
          stage="prompts"
          draft={draft}
          loading={loading}
          readOnly={readOnly}
          scriptSegmentsConfirmed={scriptSegmentsConfirmed}
          promptsConfirmed={promptsConfirmed}
          onSaveDraft={(nextDraft) => onSaveDraft(scopedDraft(nextDraft))}
          assetInventoryConfirmed={assetInventoryConfirmed}
          onRequestTemporaryAsset={canCreateTemporaryAsset ? requestTemporaryAsset : undefined}
          onConfirmScriptSegments={(nextDraft) => onConfirmScriptSegments(scopedDraft(nextDraft))}
          onLockEpisode={(code, nextDraft) => onLockEpisode(code, scopedDraft(nextDraft))}
        />
        </>
      ) : null}
      {resultStage === "segmentation" ? (
        <EditableBreakdownDraft
          projectId={project?.id}
          stage="segmentation"
          draft={draft}
          loading={loading}
          readOnly={readOnly}
          scriptSegmentsConfirmed={scriptSegmentsConfirmed}
          promptsConfirmed={promptsConfirmed}
          onSaveDraft={(nextDraft) => onSaveDraft(scopedDraft(nextDraft))}
          assetInventoryConfirmed={assetInventoryConfirmed}
          onRequestTemporaryAsset={canCreateTemporaryAsset ? requestTemporaryAsset : undefined}
          onConfirmScriptSegments={(nextDraft) => onConfirmScriptSegments(scopedDraft(nextDraft))}
          onLockEpisode={(code, nextDraft) => onLockEpisode(code, scopedDraft(nextDraft))}
        />
      ) : null}
      {resultStage === "storyboard" ? (
        <EditableBreakdownDraft
          projectId={project?.id}
          stage="breakdown"
          draft={draft}
          loading={loading}
          readOnly={readOnly}
          scriptSegmentsConfirmed={scriptSegmentsConfirmed}
          promptsConfirmed={promptsConfirmed}
          episodeLocked={storyboardTasksDistributed}
          onSaveDraft={(nextDraft) => onSaveDraft(scopedDraft(nextDraft))}
          onConfirmScriptSegments={(nextDraft) => onConfirmScriptSegments(scopedDraft(nextDraft))}
          onLockEpisode={(code, nextDraft) => onLockEpisode(code, scopedDraft(nextDraft))}
          onOpenTaskAssignment={onOpenTaskAssignment}
        />
      ) : null}
      {resultStage !== "assets" && temporaryProductionOpen && project && episodeId && scriptVersionId && onCreateTemporaryAssetProduction ? (
        <TemporaryProductionAssetDialog
          episodeCode={episodeCode}
          loading={loading}
          project={project}
          episodeId={episodeId}
          scriptVersionId={scriptVersionId}
          initialAssetType={temporaryAssetType}
          onClose={() => setTemporaryProductionOpen(false)}
          onCreate={async (payload) => {
            const created = await onCreateTemporaryAssetProduction(payload);
            if (created) {
              setTemporaryProductionOpen(false);
              onOpenTaskAssignment?.();
            }
            return created;
          }}
        />
      ) : null}
    </>
  );
}

type TemporaryProductionAssetType = TemporaryAssetProductionPayload["asset_type"];

const TEMPORARY_ASSET_TYPE_OPTIONS: Array<{ value: TemporaryProductionAssetType; label: string }> = [
  { value: "character", label: "人物" },
  { value: "scene", label: "场景" },
  { value: "prop", label: "道具" },
  { value: "music", label: "配乐" },
  { value: "voice_profile", label: "角色音色" },
];

function TemporaryProductionAssetDialog({
  project,
  episodeId,
  episodeCode,
  scriptVersionId,
  initialAssetType,
  loading,
  onClose,
  onCreate,
}: {
  project: Project;
  episodeId: string;
  episodeCode?: string;
  scriptVersionId: string;
  initialAssetType?: TemporaryProductionAssetType;
  loading: boolean;
  onClose: () => void;
  onCreate: (payload: TemporaryAssetProductionPayload) => Promise<boolean>;
}) {
  const [assetType, setAssetType] = React.useState<TemporaryProductionAssetType>(initialAssetType || "character");
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [reason, setReason] = React.useState("");
  const [productionRequirement, setProductionRequirement] = React.useState("");
  const [taskVariant, setTaskVariant] = React.useState("A");
  const [error, setError] = React.useState("");
  const [saving, setSaving] = React.useState(false);
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const requestId = React.useRef(createClientRequestId("temporary-production"));

  function selectAssetType(next: TemporaryProductionAssetType) {
    setAssetType(next);
    setTaskVariant(next === "character" ? "A" : "MASTER");
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!name.trim() || !reason.trim() || !productionRequirement.trim() || !taskVariant.trim()) {
      setError("请完整填写资产名称、增加原因、生产要求和生产规格。");
      return;
    }
    setError("");
    setConfirmOpen(true);
  }

  async function confirmCreate() {
    setSaving(true);
    setConfirmOpen(false);
    try {
      const created = await onCreate({
        request_id: requestId.current,
        project_id: project.id,
        episode_id: episodeId,
        script_version_id: scriptVersionId,
        asset_type: assetType,
        name: name.trim(),
        description: description.trim() || null,
        reason: reason.trim(),
        production_requirement: productionRequirement.trim(),
        task_variant: taskVariant.trim().toUpperCase(),
      });
      if (created) onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "人工临时生产资产创建失败");
    } finally {
      setSaving(false);
    }
  }

  return <div className="modal-backdrop temporary-asset-modal" role="presentation" onClick={onClose}>
    <form className="temporary-asset-dialog" onSubmit={submit} onClick={(event) => event.stopPropagation()}>
      <header><div><strong>新增临时生产资产</strong><span>跳过提示词，直接创建待分配生产任务；成果仍须经过导演审核定版。</span></div><button aria-label="关闭" title="关闭" type="button" onClick={onClose}><X size={18} /></button></header>
      <div className="temporary-asset-fields">
        <label><span>所属项目</span><input disabled value={`${project.project_prefix || project.project_no} · ${project.title}`} /></label>
        <label><span>所属分集</span><input disabled value={episodeCode || "当前分集"} /></label>
        <label><span>资产类型</span><select value={assetType} onChange={(event) => selectAssetType(event.target.value as TemporaryProductionAssetType)}>{TEMPORARY_ASSET_TYPE_OPTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
        <label><span>生产规格</span><input required maxLength={20} placeholder={assetType === "character" ? "A" : "MASTER"} value={taskVariant} onChange={(event) => setTaskVariant(event.target.value)} /></label>
        <label className="wide"><span>资产名称</span><input required maxLength={160} value={name} onChange={(event) => setName(event.target.value)} /></label>
        <label className="wide"><span>资产说明（可选）</span><textarea rows={2} value={description} onChange={(event) => setDescription(event.target.value)} /></label>
        <label className="wide"><span>临时增加原因</span><textarea required maxLength={500} rows={2} value={reason} onChange={(event) => setReason(event.target.value)} /></label>
        <label className="wide"><span>给制作员的生产要求</span><textarea required maxLength={2000} rows={4} placeholder="说明画面、角度、风格、交付格式或音频要求；该内容将直接显示在生产任务中。" value={productionRequirement} onChange={(event) => setProductionRequirement(event.target.value)} /></label>
        <div className="temporary-production-flow wide"><strong>流转说明</strong><span>创建待分配任务 → 制作员上传多个候选 → 导演审核并选定主母版 → 进入资产库</span><span>不会运行提示词 Agent，也不会修改资产拆解确认状态。</span></div>
        {error ? <p className="temporary-asset-error wide">{error}</p> : null}
      </div>
      <footer><button type="button" onClick={onClose}>取消</button><button className="primary" disabled={loading || saving} type="submit">{saving ? "正在创建…" : "下一步确认"}</button></footer>
      {confirmOpen ? (
        <div className="temporary-asset-confirm" role="alertdialog" aria-modal="true">
          <strong>确认创建人工临时生产资产？</strong>
          <p>本次只创建生产任务，不进入资产提示词 Agent；提示词、画面或音频制作要求由人工在任务中自行处理。该资产不会加入已确认的正式资产拆解清单。</p>
          <div><button type="button" onClick={() => setConfirmOpen(false)}>返回修改</button><button className="primary" type="button" disabled={saving} onClick={() => void confirmCreate()}>确认创建生产任务</button></div>
        </div>
      ) : null}
    </form>
  </div>;
}

function ReadingOutcomePanel({
  report,
  latestRun,
  confirmed,
  roleNames,
}: {
  report?: Record<string, unknown>;
  latestRun?: AgentRun;
  confirmed: boolean;
  roleNames: Record<string, string>;
}) {
  return (
    <Panel title="剧本围读结果">
      <ReadingReportView report={report} latestRun={latestRun} confirmed={confirmed} roleNames={roleNames} />
    </Panel>
  );
}

function AssetDraftOverview({ draft }: { draft: BreakdownDraftData }) {
  const groups = assetGroups(draft);
  const total = groups.reduce((sum, group) => sum + group.items.length, 0);
  const [characters, scenes, props] = groups;
  return (
    <Panel title="资产草案总览" subtitle="按生产核对口径展示资产；前端展示名称，ID 保留在后端用于关联追溯。">
      <div className="asset-overview-summary">
        {groups.map((group) => (
          <div className={`asset-overview-metric ${group.tone}`} key={group.key}>
            <span>{group.label}</span>
            <strong>{group.items.length}</strong>
          </div>
        ))}
        <div className="asset-overview-metric">
          <span>总资产</span>
          <strong>{total}</strong>
        </div>
      </div>
      {!total ? (
        <div className="empty-hint">暂无资产草案。请先运行完整分镜/资产拆分 Agent，或在下方人工新增资产。</div>
      ) : (
        <div className="asset-overview-groups">
          <section className="asset-overview-group">
            <div className="draft-detail-title">
              <strong>人物资产</strong>
              <span className="badge mint">{characters.items.length} 项</span>
            </div>
            <CharacterAssetTable items={characters.items} draft={draft} />
          </section>
          <section className="asset-overview-group">
            <div className="draft-detail-title">
              <strong>场景资产</strong>
              <span className="badge sky">{scenes.items.length} 项</span>
            </div>
            <SceneAssetTable items={scenes.items} />
          </section>
          <section className="asset-overview-group">
            <div className="draft-detail-title">
              <strong>道具资产</strong>
              <span className="badge sun">{props.items.length} 项</span>
            </div>
            <PropAssetTable items={props.items} draft={draft} />
          </section>
        </div>
      )}
    </Panel>
  );
}

function statusLabel(status?: string) {
  const labels: Record<string, string> = {
    running: "执行中",
    queued: "等待中",
    succeeded: "已完成",
    needs_review: "需复核",
    failed: "失败",
    failed_non_blocking: "失败但继续",
    skipped: "已跳过",
    pending: "待执行",
  };
  return labels[String(status || "").toLowerCase()] || status || "未开始";
}

function formatTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function liveDurationSeconds(node: { status?: string; started_at?: string | null; finished_at?: string | null; duration_seconds?: number }) {
  if (!node.started_at) return Number(node.duration_seconds || 0);
  const start = new Date(node.started_at).getTime();
  if (Number.isNaN(start)) return Number(node.duration_seconds || 0);
  const finish = node.finished_at ? new Date(node.finished_at).getTime() : Date.now();
  if (Number.isNaN(finish)) return Number(node.duration_seconds || 0);
  return Math.max(0, Math.floor((finish - start) / 1000));
}

function CharacterAssetTable({ items, draft }: { items: BreakdownDraftAsset[]; draft: BreakdownDraftData }) {
  if (!items.length) return <div className="empty-hint">暂无人物资产。</div>;
  return (
    <div className="asset-table-wrap">
      <table className="asset-review-table character-table">
        <thead>
          <tr>
            <th>ID</th>
            <th>角色名</th>
            <th>优先级</th>
            <th>类型</th>
            <th>人物关系</th>
            <th>定位</th>
            <th>外貌&核心制作要求</th>
            <th>对应场景名</th>
          </tr>
        </thead>
        <tbody>
          {items.map((asset) => (
            <tr key={asset.client_asset_key}>
              <td>{asset.display_code}</td>
              <td>{asset.display_label}</td>
              <td>{assetPriority(asset)}</td>
              <td>{characterType(asset)}</td>
              <td>{characterRelation(asset)}</td>
              <td>{assetRole(asset)}</td>
              <td>{characterAppearance(asset)}</td>
              <td>{sceneNames(asset, draft).join(" / ") || "待确认"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SceneAssetTable({ items }: { items: BreakdownDraftAsset[] }) {
  if (!items.length) return <div className="empty-hint">暂无场景资产。</div>;
  return (
    <div className="asset-table-wrap">
      <table className="asset-review-table scene-table">
        <thead>
          <tr>
            <th>ID</th>
            <th>场景名</th>
            <th>优先级</th>
            <th>时间</th>
            <th>描述</th>
            <th>核心功能</th>
            <th>空间分布</th>
            <th>视觉目标</th>
            <th>关键道具</th>
            <th>连续性风险</th>
            <th>对应分集</th>
          </tr>
        </thead>
        <tbody>
          {items.map((asset) => (
            <tr key={asset.client_asset_key}>
              <td>{asset.display_code}</td>
              <td>{asset.display_label}</td>
              <td>{assetPriority(asset)}</td>
              <td>{asset.asset_type === "scene" ? asset.attributes.time || "" : ""}</td>
              <td>{asset.description || ""}</td>
              <td>{asset.asset_type === "scene" ? asset.attributes.narrative_function || "" : ""}</td>
              <td>{asset.asset_type === "scene" ? asset.attributes.spatial_zones.join(" / ") : ""}</td>
              <td>{asset.asset_type === "scene" ? asset.attributes.visual_goal || "" : ""}</td>
              <td>{asset.asset_type === "scene" ? asset.attributes.key_props.join(" / ") : ""}</td>
              <td>{asset.asset_type === "scene" ? asset.attributes.continuity_risks.join(" / ") : ""}</td>
              <td>{asset.asset_type === "scene" ? asset.attributes.episode_code || "" : ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PropAssetTable({ items, draft }: { items: BreakdownDraftAsset[]; draft: BreakdownDraftData }) {
  if (!items.length) return <div className="empty-hint">暂无道具资产。</div>;
  const showOwner = items.some(isOwnerAssignableProp);
  return (
    <div className="asset-table-wrap">
      <table className="asset-review-table prop-table">
        <thead>
          <tr>
            <th>ID</th>
            <th>道具名</th>
            <th>优先级</th>
            <th>类型</th>
            <th>外观</th>
            <th>出处</th>
            <th>备注</th>
            {showOwner ? <th>所属角色</th> : null}
            <th>对应场景</th>
          </tr>
        </thead>
        <tbody>
          {items.map((asset) => (
            <tr key={asset.client_asset_key}>
              <td>{asset.display_code}</td>
              <td>{asset.display_label}</td>
              <td>{assetPriority(asset)}</td>
              <td>{asset.asset_type === "prop" ? asset.attributes.prop_type || "" : ""}</td>
              <td>{asset.asset_type === "prop" ? asset.attributes.appearance || asset.description || "" : ""}</td>
              <td>{asset.asset_type === "prop" ? asset.attributes.source || "" : ""}</td>
              <td>{asset.asset_type === "prop" ? asset.attributes.status_change || "" : ""}</td>
              {showOwner ? <td>{isOwnerAssignableProp(asset) ? ownerNames(asset, draft) : "不适用"}</td> : null}
              <td>{propSceneNames(asset, draft)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function SimpleAssetTable({ title, tone, items, draft }: { title: string; tone: string; items: BreakdownDraftAsset[]; draft: BreakdownDraftData }) {
  if (!items.length) return null;
  return (
    <section className="asset-overview-group">
      <div className="draft-detail-title">
        <strong>{title}</strong>
        <span className={`badge ${tone}`}>{items.length} 项</span>
      </div>
      <div className="asset-table-wrap">
        <table className="asset-review-table">
          <thead>
            <tr>
              <th>ID</th>
              <th>名称</th>
              <th>优先级</th>
              <th>制作要求</th>
              <th>备注</th>
              <th>对应场景名</th>
            </tr>
          </thead>
          <tbody>
            {items.map((asset) => (
              <tr key={asset.client_asset_key}>
                <td>{asset.display_code}</td>
                <td>{asset.display_label}</td>
                <td>{assetPriority(asset)}</td>
                <td>{asset.description || "待补充"}</td>
                <td>{assetNotes(asset)}</td>
                <td>{sceneNames(asset, draft).join(" / ") || "待确认"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function runTimestamp(run: Pick<AgentRun, "created_at" | "updated_at">) {
  const timestamp = new Date(run.updated_at || run.created_at).getTime();
  return Number.isFinite(timestamp) ? timestamp : 0;
}

function objectValue(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function numberValue(value: unknown): number {
  const parsed = Number(value || 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function prioritySummary(items: Record<string, unknown>[]) {
  const counts = items.reduce<Record<string, number>>((acc, item) => {
    const priority = String(item.priority || "C").toUpperCase();
    acc[priority] = (acc[priority] || 0) + 1;
    return acc;
  }, {});
  return ["S", "A", "B", "C"].map((key) => counts[key] ? `${key}${counts[key]}` : "").filter(Boolean).join(" / ");
}

function readingRoleNameMap(draft: BreakdownDraftData) {
  const names: Record<string, string> = {};
  draft.assets.filter((asset) => asset.asset_type === "character").forEach((asset) => {
    const code = asset.asset_code;
    const name = asset.name;
    const roleCode = roleReferenceCode(code);
    if (!name || !roleCode) return;
    names[roleCode] = name;
    names[code.toUpperCase()] = name;
  });
  return names;
}

function roleReferenceCode(value: string) {
  return value.toUpperCase().match(/(?:^|-)(R\d{3,})(?:-|$)/)?.[1] || "";
}

function assetGroups(draft: BreakdownDraftData) {
  return [
    { key: "characters", label: "人物资产", tone: "mint", items: draft.assets.filter((item) => item.asset_type === "character") },
    { key: "scenes", label: "场景资产", tone: "sky", items: draft.assets.filter((item) => item.asset_type === "scene") },
    { key: "props", label: "道具资产", tone: "sun", items: draft.assets.filter((item) => item.asset_type === "prop") },
  ] satisfies Array<{ key: string; label: string; tone: string; items: BreakdownDraftAsset[] }>;
}

function uniqueText(values: string[]) {
  return Array.from(new Set(values.map((value) => String(value || "").trim()).filter(Boolean)));
}

function characterType(asset: BreakdownDraftAsset) {
  return asset.asset_type === "character" ? asset.attributes.character_type || "待确认" : "待确认";
}

function assetPriority(asset: BreakdownDraftAsset) {
  return asset.priority;
}

function normalizePriority(value: string) {
  const raw = value.trim().toUpperCase();
  if (["S", "A", "B", "C"].includes(raw)) return raw;
  const map: Record<string, string> = {
    "0": "S",
    "1": "S",
    "2": "A",
    "3": "B",
    "4": "C",
    "最高": "S",
    "核心": "S",
    "主资产": "S",
    "高": "A",
    "重要": "A",
    "中": "B",
    "常规": "B",
    "低": "C",
    "记录": "C",
    "可选": "C",
  };
  return map[raw] || "";
}

function characterRelation(asset: BreakdownDraftAsset) {
  return asset.relations.relationship_text || "";
}

function assetRole(asset: BreakdownDraftAsset) {
  return asset.asset_type === "character" ? asset.attributes.role || asset.description || "待补充" : "待补充";
}

function characterAppearance(asset: BreakdownDraftAsset) {
  return asset.asset_type === "character" ? asset.attributes.appearance || asset.attributes.core_requirement || asset.description || "待补充" : "待补充";
}

function ownerNames(asset: BreakdownDraftAsset, draft: BreakdownDraftData) {
  void draft;
  return asset.relations.owner_character?.target_display_label || "";
}

function propSceneNames(asset: BreakdownDraftAsset, draft: BreakdownDraftData) {
  const linkedScenes = sceneNames(asset, draft);
  if (linkedScenes.length) return linkedScenes.join(" / ");
  return asset.asset_type === "prop" ? asset.attributes.usage_scene || "" : "";
}

function assetNotes(asset: BreakdownDraftAsset) {
  return asset.repairs.map((repair) => repair.reason).filter(Boolean).join("；");
}

function sceneNames(asset: BreakdownDraftAsset, draft: BreakdownDraftData) {
  void draft;
  return asset.relations.scene_links.map((scene) => scene.target_display_label);
}

function sceneTextList(value: unknown): string[] {
  return displayParts(value, { stripReferenceIds: false });
}

function displayList(value: unknown, options: { stripReferenceIds?: boolean } = {}) {
  return displayParts(value, options).join(" / ");
}

function displayParts(value: unknown, options: { stripReferenceIds?: boolean } = {}): string[] {
  if (value === null || value === undefined) return [];
  if (Array.isArray(value)) {
    return uniqueText(value.flatMap((item) => displayParts(item, options)));
  }
  if (typeof value === "object") {
    const item = value as Record<string, unknown>;
    const primary = textValue(item.name || item.scene_name || item.character_name || item.prop_name || item.title || item.label || item.value || item.zone);
    const secondary = textValue(item.description || item.detail || item.note);
    const combined = [primary, secondary].filter(Boolean).join(" ");
    return combined ? displayParts(combined, options) : [];
  }
  return splitDisplayText(String(value), options);
}

function splitDisplayText(value: string, options: { stripReferenceIds?: boolean }) {
  const normalized = value.replace(/([）)])\s*[-–—]\s+(?=(?:[A-Z]{0,3}[-_ ]?\d|场景\d))/gi, "$1/");
  return uniqueText(
    normalized
      .split(/\n+|[、，,；;]+|\s[-–—]\s|\/+/)
      .map((item) => cleanDisplayPart(item, options))
      .filter(Boolean),
  );
}

function cleanDisplayPart(value: string, options: { stripReferenceIds?: boolean }) {
  let text = value.trim();
  if (!text) return "";
  if (options.stripReferenceIds) {
    const bracketName = text.match(/^(?:[A-Z]{0,3}[-_ ]?\d+[A-Z0-9-]*|场景\d+)\s*[（(](.+)[）)]$/i);
    if (bracketName?.[1]) text = bracketName[1].trim();
    text = text.replace(/^(?:S|SC|R|CH|P|PO|J|F|SEG|SB)?[-_ ]?\d+[A-Z0-9-]*[：:\s-]+/i, "").trim();
    if (/^(?:S|SC|R|CH|P|PO|J|F|SEG|SB)?[-_ ]?\d+[A-Z0-9-]*$/i.test(text)) return "";
  }
  return text;
}

function valueText(value: unknown) {
  return textValue(value);
}

function effectiveAgentRunStatus(run: AgentRun) {
  const workflow = run.output?.workflow;
  const workflowStatus = workflow && typeof workflow === "object" ? String((workflow as Record<string, unknown>).status || "") : "";
  const nodeStatus = run.output?.node_status;
  const hasFailedNode = nodeStatus && typeof nodeStatus === "object"
    ? Object.values(nodeStatus as Record<string, unknown>).some((status) => String(status) === "failed")
    : false;
  const hasErrors = Array.isArray(run.output?.errors) && run.output.errors.length > 0;
  if (run.status === "failed" || workflowStatus === "failed" || hasFailedNode || hasErrors) return "failed";
  return run.status;
}

function agentRunFailureText(run: AgentRun) {
  const errors = Array.isArray(run.output?.errors) ? run.output.errors : [];
  const firstError = errors.find((item): item is Record<string, unknown> => Boolean(item) && typeof item === "object");
  const node = firstError?.node ? `节点 ${String(firstError.node)}` : "拆解节点";
  const message = firstError?.message ? String(firstError.message) : run.error_message || "后端未返回具体错误信息";
  const warnings = Array.isArray(run.output?.warnings) ? run.output.warnings.map(String).filter(Boolean).join("；") : "";
  return warnings ? `${node}：${message}。${warnings}` : `${node}：${message}`;
}

function isPendingRun(run?: AgentRun) {
  return Boolean(run && ["pending", "queued", "running"].includes(String(run.status || "")));
}

function breakdownStepKey(run?: AgentRun) {
  const singleStep = String(run?.input?.single_step || "");
  return ({
    script_reading: "reading",
    asset_extract: "assets",
    asset_prompt_generation: "prompts",
    script_segmentation: "segmentation",
    storyboard_breakdown: "storyboard",
    script_breakdown: "storyboard",
  } as Record<string, ResultStage>)[singleStep];
}

function withConfirmedReadingReport(draft: BreakdownDraftData, report: Record<string, unknown>): BreakdownDraftData {
  return {
    ...draft,
    readingReport: report,
    readingReviewState: {
      ...draft.readingReviewState,
      status: "confirmed",
      confirmed_revision_id: null,
      confirmation_mode: "human_confirmed",
    },
  };
}

function assetConfirmationBlockers(draft: BreakdownDraftData) {
  return draft.assets.flatMap((asset) => asset.review_items).filter(isRequiredPendingReview);
}

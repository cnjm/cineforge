import React from "react";
import { AlertTriangle, AudioLines, CheckCircle2, ChevronLeft, ChevronRight, Download, Images, LockKeyhole, RefreshCw, RotateCw, Trash2, Upload, Video, X } from "lucide-react";
import { api, AuthExpiredError, setAuthSession } from "../../api";
import { characterReviewScopeReady, characterReviewSelectionReady, characterViewRequiresIdentityMaster } from "../../characterTaskFlow";
import { activeSubmissions, currentBatchSubmissions, hasInvalidatedSubmissions, isActiveSubmission, isMakerRemovableSubmission, rejectedSubmissionHistory } from "../../submissionLifecycle";
import { dependencyAssetLabel, dependencyAssetTypeLabel, dependencyReasonLabel, displayDependencyAssetCodes } from "../../taskAccess";
import { dependencyDownloadName } from "../../taskDownloads";
import type { Asset, FileUploadResponse, Project, Submission, SubmissionBatch, Task, TaskCandidateUploadResult, TaskCandidateUploadUpdate, WorkspaceResponse } from "../../types";
import { assetCodeDisplay, canonicalSceneCode, platformPromptVersions, taskMediaKind, type TaskMediaKind } from "../../utils";

type Step = "keyframe" | "video";
type QueueKey = "waiting" | "working" | "reviewing" | "rejected" | "completed";

export type TaskUnit = {
  key: string;
  tasks: Task[];
  asset?: Asset;
  character: boolean;
  queue: QueueKey;
};

export type UploadCandidatesHandler = (
  task: Task,
  finalPrompt: string,
  modelName: string,
  toolNames: string[],
  files: File[],
  step?: Step,
  onProgress?: (update: TaskCandidateUploadUpdate) => void,
  resumeUpload?: FileUploadResponse,
) => Promise<TaskCandidateUploadResult[]>;

export type MyTasksPageProps = {
  mode?: "tasks" | "reviews";
  assets: Asset[];
  projects: Project[];
  workspace: WorkspaceResponse | null;
  tasks: Task[];
  loading: boolean;
  onUploadCandidates: UploadCandidatesHandler;
  onSubmitTask: (task: Task, step?: Step) => Promise<void>;
  onSubmitCharacterUnit: (tasks: Task[]) => Promise<void>;
  onDeleteDraftSubmission: (task: Task, submissionId: string) => Promise<void>;
  onReviewTask: (
    task: Task,
    batchId: string,
    decision: "approve" | "rework",
    primarySubmissionId: string | null,
    selectedSubmissionIds: string[],
    comment?: string,
  ) => Promise<void>;
  onReviewCharacterUnit: (
    entries: Array<{
      task: Task;
      batchId: string;
      primarySubmissionId: string | null;
      selectedSubmissionIds: string[];
    }>,
    decision: "approve" | "rework",
    comment?: string,
  ) => Promise<void>;
  onArchiveSubmission: (task: Task, submissionId: string, archived: boolean) => Promise<void>;
  onSetPrimarySubmission: (task: Task, submissionId: string) => Promise<void>;
  onSavePrompt: (task: Task, promptText: string, copied?: boolean) => Promise<void>;
  onGeneratePrompt: (task: Task, step?: Step) => Promise<boolean>;
};

const QUEUES: Array<{ key: QueueKey; label: string }> = [
  { key: "waiting", label: "待前置" },
  { key: "working", label: "制作中" },
  { key: "reviewing", label: "待审核" },
  { key: "rejected", label: "需返工" },
  { key: "completed", label: "已完成" },
];

export function MyTasksPage({
  mode = "tasks",
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
}: MyTasksPageProps) {
  const canReview = mode === "reviews";
  const visibleTasks = React.useMemo(() => {
    if (mode === "reviews") {
      const reviewRows = workspace ? [...(workspace.groups.todo || []), ...(workspace.groups.completed || [])] : [];
      return Array.from(new Map([...tasks, ...reviewRows].map((task) => [task.id, task])).values());
    }
    if (!workspace) return tasks;
    const rows = [...(workspace.groups.todo || []), ...(workspace.groups.completed || [])];
    return Array.from(new Map(rows.map((task) => [task.id, task])).values());
  }, [workspace, tasks, mode]);
  const availableProjects = React.useMemo(() => {
    const ids = new Set(visibleTasks.map((task) => task.project_id));
    return projects.filter((project) => ids.has(project.id));
  }, [projects, visibleTasks]);
  const [selectedProjectId, setSelectedProjectId] = React.useState<string>("all");
  const availableEpisodes = React.useMemo(() => {
    const source = selectedProjectId === "all" ? visibleTasks : visibleTasks.filter((task) => task.project_id === selectedProjectId);
    return Array.from(new Set(source.map((task) => task.episode_code).filter(Boolean) as string[])).sort((left, right) => left.localeCompare(right, "zh-CN", { numeric: true }));
  }, [selectedProjectId, visibleTasks]);
  const [selectedEpisodeCode, setSelectedEpisodeCode] = React.useState<string>("all");
  React.useEffect(() => {
    if (selectedProjectId !== "all" && !availableProjects.some((project) => project.id === selectedProjectId)) setSelectedProjectId("all");
  }, [availableProjects, selectedProjectId]);
  React.useEffect(() => {
    if (selectedEpisodeCode !== "all" && !availableEpisodes.includes(selectedEpisodeCode)) setSelectedEpisodeCode("all");
  }, [availableEpisodes, selectedEpisodeCode]);
  const scopedTasks = React.useMemo(() => visibleTasks.filter((task) => (
    (selectedProjectId === "all" || task.project_id === selectedProjectId)
    && (selectedEpisodeCode === "all" || task.episode_code === selectedEpisodeCode)
  )), [selectedEpisodeCode, selectedProjectId, visibleTasks]);
  const scopedUnits = React.useMemo(() => buildTaskUnits(scopedTasks, assets), [scopedTasks, assets]);
  const [productionCategory, setProductionCategory] = React.useState<"asset" | "storyboard">("asset");
  React.useEffect(() => {
    if (canReview || !scopedUnits.length || scopedUnits.some((unit) => productionCategory === "storyboard" ? Boolean(unit.tasks[0]?.storyboard_id) : !unit.tasks[0]?.storyboard_id)) return;
    setProductionCategory(productionCategory === "asset" ? "storyboard" : "asset");
  }, [canReview, productionCategory, scopedUnits]);
  const units = React.useMemo(() => canReview ? scopedUnits : scopedUnits.filter((unit) => (
    productionCategory === "storyboard" ? Boolean(unit.tasks[0]?.storyboard_id) : !unit.tasks[0]?.storyboard_id
  )), [canReview, productionCategory, scopedUnits]);
  const projectNames = React.useMemo(() => new Map(projects.map((project) => [project.id, project.title || project.name || "未命名项目"])), [projects]);
  const grouped = React.useMemo(() => {
    const output: Record<QueueKey, TaskUnit[]> = {
      waiting: [], working: [], reviewing: [], rejected: [], completed: [],
    };
    units.forEach((unit) => output[unit.queue].push(unit));
    return output;
  }, [units]);
  const preferredQueue: QueueKey = (
    canReview && grouped.reviewing.length ? "reviewing"
      : grouped.working.length ? "working"
          : grouped.reviewing.length ? "reviewing"
            : grouped.rejected.length ? "rejected"
              : grouped.waiting.length ? "waiting"
                : "completed"
);

  const [queue, setQueue] = React.useState<QueueKey>(preferredQueue);

  React.useEffect(() => {
    if (grouped[queue].length) return;
    setQueue(preferredQueue);
  }, [grouped, queue, preferredQueue]);

  const completedCount = grouped.completed.length;
  const activeCount = units.length - completedCount - grouped.waiting.length;
  const outstandingCount = units.length - completedCount;
  const completionRate = units.length ? Math.round((completedCount / units.length) * 100) : 0;
  const currentCharacterUnits = grouped[queue].filter((unit) => unit.character);
  const currentAssetUnits = grouped[queue].filter((unit) => (
    !unit.character
    && unit.tasks[0]?.task_type === "asset"
    && ["scene", "prop"].includes(unit.asset?.asset_type || "")
  ));
  const currentSingleUnits = grouped[queue].filter((unit) => !unit.character && !currentAssetUnits.includes(unit));
  const currentStoryboardUnits = currentSingleUnits.filter((unit) => Boolean(unit.tasks[0]?.storyboard_id));
  const currentLooseUnits = currentSingleUnits.filter((unit) => !unit.tasks[0]?.storyboard_id);
  const queues = mode === "reviews"
    ? QUEUES.filter((item) => ["reviewing", "completed"].includes(item.key))
    : QUEUES.filter((item) => item.key !== "rejected");

  return <>
    <header className="task-workspace-head">
      <div><h3>{canReview ? "审批中心" : "我的任务"}</h3><p>{canReview ? "审核负责项目的候选成果并指定主母版。" : "上传多个候选成果，确认后统一提交导演审核。"}</p></div>
      <div className="profile-stats">
        <div className="profile-stat"><strong>{canReview ? outstandingCount : activeCount}</strong><span>{canReview ? "未完成" : "处理中"}</span></div>
        <div className="profile-stat"><strong>{grouped.reviewing.length}</strong><span>{canReview ? "待我审核" : "待审核"}</span></div>
        <div className="profile-stat"><strong>{completionRate}%</strong><span>完成率</span></div>
      </div>
    </header>

    <nav className="task-context-tabs" aria-label="项目与分集">
      <div className="task-project-tabs">
        <button className={selectedProjectId === "all" ? "active" : ""} type="button" onClick={() => setSelectedProjectId("all")}>
          <span>全部项目</span><strong>{availableProjects.length} 个项目</strong>
        </button>
        {availableProjects.map((project) => <button className={selectedProjectId === project.id ? "active" : ""} key={project.id} type="button" onClick={() => { setSelectedProjectId(project.id); setSelectedEpisodeCode("all"); }}>
          <span>{project.title}</span><strong>{buildTaskUnits(visibleTasks.filter((task) => task.project_id === project.id), assets).filter((unit) => unit.queue !== "completed").length} 未完成</strong>
        </button>)}
      </div>
      <div className="task-episode-tabs">
        <button className={selectedEpisodeCode === "all" ? "active" : ""} type="button" onClick={() => setSelectedEpisodeCode("all")}>全部分集</button>
        {availableEpisodes.map((episode) => <button className={selectedEpisodeCode === episode ? "active" : ""} key={episode} type="button" onClick={() => setSelectedEpisodeCode(episode)}><span>{episode}</span><strong>{buildTaskUnits(visibleTasks.filter((task) => task.episode_code === episode && (selectedProjectId === "all" || task.project_id === selectedProjectId)), assets).filter((unit) => unit.queue !== "completed").length} 未完成</strong></button>)}
      </div>
    </nav>

    {!canReview ? <nav className="task-business-tabs" aria-label="生产类别">
      <button className={productionCategory === "asset" ? "active" : ""} type="button" onClick={() => setProductionCategory("asset")}><span>资产生产</span><strong>{scopedUnits.filter((unit) => !unit.tasks[0]?.storyboard_id).length}</strong></button>
      <button className={productionCategory === "storyboard" ? "active" : ""} type="button" onClick={() => setProductionCategory("storyboard")}><span>分镜生产</span><strong>{scopedUnits.filter((unit) => Boolean(unit.tasks[0]?.storyboard_id)).length}</strong></button>
    </nav> : null}

    {!canReview ? <nav className="task-queue-tabs" aria-label="任务状态">
      {queues.map((item) => <button className={queue === item.key ? "active" : ""} key={item.key} onClick={() => setQueue(item.key)}>
        <span>{item.label}</span><strong>{grouped[item.key].length}</strong>
      </button>)}
    </nav> : null}

    {canReview ? <ReviewWorkbench
      loading={loading}
      projectNames={projectNames}
      units={units}
      onArchiveSubmission={onArchiveSubmission}
      onReviewCharacterUnit={onReviewCharacterUnit}
      onReviewTask={onReviewTask}
      onSetPrimarySubmission={onSetPrimarySubmission}
    /> : <section className="task-workspace-list">
      {currentCharacterUnits.length ? <CharacterTaskTable
        canReview={canReview}
        loading={loading}
        projectNames={projectNames}
        units={currentCharacterUnits}
        onArchiveSubmission={onArchiveSubmission}
        onDeleteDraftSubmission={onDeleteDraftSubmission}
        onGeneratePrompt={onGeneratePrompt}
        onReviewCharacterUnit={onReviewCharacterUnit}
        onSavePrompt={onSavePrompt}
        onSetPrimarySubmission={onSetPrimarySubmission}
        onSubmitCharacterUnit={onSubmitCharacterUnit}
        onUploadCandidates={onUploadCandidates}
      /> : null}
      {["scene", "prop"].map((assetType) => {
        const assetUnits = currentAssetUnits.filter((unit) => unit.asset?.asset_type === assetType);
        return assetUnits.length ? <AssetTaskTable
          assetType={assetType}
          canReview={canReview}
          key={assetType}
          loading={loading}
          projectNames={projectNames}
          units={assetUnits}
          onArchiveSubmission={onArchiveSubmission}
          onDeleteDraftSubmission={onDeleteDraftSubmission}
          onGeneratePrompt={onGeneratePrompt}
          onReviewTask={onReviewTask}
          onSavePrompt={onSavePrompt}
          onSetPrimarySubmission={onSetPrimarySubmission}
          onSubmitTask={onSubmitTask}
          onUploadCandidates={onUploadCandidates}
        /> : null;
      })}
      {currentStoryboardUnits.length ? <StoryboardProductionWorkbench
        loading={loading}
        projectNames={projectNames}
        units={currentStoryboardUnits}
        onArchiveSubmission={onArchiveSubmission}
        onDeleteDraftSubmission={onDeleteDraftSubmission}
        onGeneratePrompt={onGeneratePrompt}
        onReviewTask={onReviewTask}
        onSavePrompt={onSavePrompt}
        onSetPrimarySubmission={onSetPrimarySubmission}
        onSubmitTask={onSubmitTask}
        onUploadCandidates={onUploadCandidates}
      /> : null}
      {currentLooseUnits.map((unit) => <section className="single-production-unit" key={unit.key}>
        {unit.tasks.map((task) => <TaskWorkItem
          canReview={canReview}
          key={task.id}
          loading={loading}
          projectName={projectNames.get(task.project_id) || "未命名项目"}
          task={task}
          onArchiveSubmission={onArchiveSubmission}
          onGeneratePrompt={onGeneratePrompt}
          onDeleteDraftSubmission={onDeleteDraftSubmission}
          onReviewTask={onReviewTask}
          onSavePrompt={onSavePrompt}
          onSetPrimarySubmission={onSetPrimarySubmission}
          onSubmitTask={onSubmitTask}
          onUploadCandidates={onUploadCandidates}
        />)}
      </section>)}
      {!grouped[queue].length ? <div className="empty-hint">当前队列没有任务。</div> : null}
    </section>}
  </>;
}

export function buildTaskUnits(tasks: Task[], assets: Asset[]): TaskUnit[] {
  const assetsById = new Map(assets.map((asset) => [asset.id, asset]));
  const characterGroups = new Map<string, Task[]>();
  const storyboardGroups = new Map<string, Task[]>();
  const units: TaskUnit[] = [];
  tasks.forEach((task) => {
    const asset = task.asset_id ? assetsById.get(task.asset_id) : undefined;
    if (task.storyboard_id) {
      storyboardGroups.set(task.storyboard_id, [...(storyboardGroups.get(task.storyboard_id) || []), task]);
      return;
    }
    if (!task.asset_id) return;
    if (task.task_type === "asset" && asset?.asset_type === "character") {
      const key = [task.asset_id, task.age_stage_code || "BASE", task.costume_variant_code || "BASE"].join("|");
      characterGroups.set(key, [...(characterGroups.get(key) || []), task]);
      return;
    }
    units.push({ key: task.id, tasks: [task], asset, character: false, queue: taskQueue(task) });
  });
  characterGroups.forEach((groupTasks, key) => {
    const sorted = groupTasks.slice().sort((left, right) => String(left.task_variant || "").localeCompare(String(right.task_variant || "")));
    units.push({
      key,
      tasks: sorted,
      asset: sorted[0].asset_id ? assetsById.get(sorted[0].asset_id) : undefined,
      character: true,
      queue: characterUnitQueue(sorted),
    });
  });
  storyboardGroups.forEach((groupTasks, storyboardId) => {
    const sorted = groupTasks.slice().sort((left, right) => storyboardExecutionOrder(left) - storyboardExecutionOrder(right));
    units.push({ key: "storyboard:" + storyboardId, tasks: sorted, character: false, queue: characterUnitQueue(sorted) });
  });
  return units.sort(compareTaskUnits);
}

function storyboardExecutionOrder(task: Task) {
  if (task.task_type === "storyboard_shot") return 0;
  if (task.task_type === "text_to_image") return 1;
  if (["image_to_video", "video_generation"].includes(task.task_type)) return 2;
  return 3;
}

function storyboardExecutionLabel(task: Task) {
  if (task.task_type === "text_to_image") return "关键帧";
  if (["image_to_video", "video_generation"].includes(task.task_type)) return "视频";
  return "分镜";
}

function compareTaskUnits(left: TaskUnit, right: TaskUnit) {
  const codeOrder = String(left.asset?.asset_code || left.tasks[0]?.title || "").localeCompare(
    String(right.asset?.asset_code || right.tasks[0]?.title || ""),
    "zh-CN",
    { numeric: true, sensitivity: "base" },
  );
  if (codeOrder) return codeOrder;
  const leftTask = left.tasks[0];
  const rightTask = right.tasks[0];
  return [
    String(leftTask?.age_stage_code || "").localeCompare(String(rightTask?.age_stage_code || ""), "zh-CN", { numeric: true }),
    String(leftTask?.costume_variant_code || "").localeCompare(String(rightTask?.costume_variant_code || ""), "zh-CN", { numeric: true }),
    String(leftTask?.task_variant || "").localeCompare(String(rightTask?.task_variant || ""), "zh-CN", { numeric: true }),
  ].find((value) => value !== 0) || 0;
}

function currentStoryboardTask(unit: TaskUnit) {
  return unit.tasks.find((task) => task.status !== "completed" && !task.locked)
    || unit.tasks.find((task) => task.status !== "completed")
    || unit.tasks[unit.tasks.length - 1];
}

function storyboardStepState(task: Task) {
  if (task.status === "completed") return "done";
  if (task.locked) return "locked";
  if (["submitted", "reviewing"].includes(task.status)) return "reviewing";
  if (task.status === "rejected") return "rework";
  return "active";
}

export function StoryboardProductionWorkbench({
  units,
  loading,
  projectNames,
  onUploadCandidates,
  onSubmitTask,
  onDeleteDraftSubmission,
  onReviewTask,
  onArchiveSubmission,
  onSetPrimarySubmission,
  onSavePrompt,
  onGeneratePrompt,
}: {
  units: TaskUnit[];
  loading: boolean;
  projectNames: Map<string, string>;
  onUploadCandidates: UploadCandidatesHandler;
  onSubmitTask: MyTasksPageProps["onSubmitTask"];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onReviewTask: MyTasksPageProps["onReviewTask"];
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
  onSavePrompt: MyTasksPageProps["onSavePrompt"];
  onGeneratePrompt: MyTasksPageProps["onGeneratePrompt"];
}) {
  const [activeUnitKey, setActiveUnitKey] = React.useState(units[0]?.key || "");
  const activeUnit = units.find((unit) => unit.key === activeUnitKey) || units[0];
  const suggestedTask = activeUnit ? currentStoryboardTask(activeUnit) : undefined;
  const [activeTaskId, setActiveTaskId] = React.useState(suggestedTask?.id || "");
  const activeTask = activeUnit?.tasks.find((task) => task.id === activeTaskId) || suggestedTask;

  React.useEffect(() => {
    if (!activeUnit) {
      setActiveUnitKey("");
      return;
    }
    if (activeUnit.key !== activeUnitKey) setActiveUnitKey(activeUnit.key);
  }, [activeUnit, activeUnitKey]);

  React.useEffect(() => {
    if (suggestedTask && !activeUnit?.tasks.some((task) => task.id === activeTaskId)) {
      setActiveTaskId(suggestedTask.id);
    }
  }, [activeTaskId, activeUnit, suggestedTask]);

  function openUnit(unit: TaskUnit) {
    setActiveUnitKey(unit.key);
    setActiveTaskId(currentStoryboardTask(unit)?.id || "");
  }

  return <section className="storyboard-production-workbench" aria-label="分镜制作工作台">
    <aside className="storyboard-task-queue">
      <header><strong>父分镜队列</strong><span>{units.length} 项</span></header>
      <div>
        {units.map((unit) => {
          const task = currentStoryboardTask(unit);
          const summary = task?.dependency_summary;
          const pendingAssets = (task?.dependency_assets || []).filter((asset) => !asset.ready);
          const duration = task?.storyboard_duration_seconds;
          return <button className={`storyboard-task-row ${activeUnit?.key === unit.key ? "active" : ""}`} key={unit.key} type="button" onClick={() => openUnit(unit)}>
            <span className="storyboard-task-row-head"><strong>{task?.storyboard_code || "父分镜"}</strong><em>{duration ? `${duration}秒` : "时长未记录"}</em><i className={`status-${unit.queue}`}>{queueLabel(unit.queue)}</i></span>
            <span className="storyboard-task-scene">{canonicalSceneCode(task?.scene_code) || "未绑定场景"} · {task?.scene_name || task?.title}</span>
            <span className="storyboard-task-row-foot">
              <span>{unit.tasks.map((item) => <i className={storyboardStepState(item)} key={item.id}>{storyboardExecutionLabel(item)}</i>)}</span>
              <b className={pendingAssets.length ? "pending" : "ready"}>前置资产 {summary ? `${summary.ready}/${summary.required}` : task?.locked ? "待确认" : "已就绪"}</b>
            </span>
            {pendingAssets.length ? <small>缺 {pendingAssets.slice(0, 2).map((asset) => dependencyAssetLabel(asset)).join("、")}{pendingAssets.length > 2 ? ` 等${pendingAssets.length}项` : ""}</small> : null}
          </button>;
        })}
      </div>
    </aside>
    {activeUnit && activeTask ? <main className="storyboard-task-workspace">
      <header className="storyboard-task-workspace-head">
        <div><span>{projectNames.get(activeTask.project_id) || "未命名项目"} · {activeTask.episode_code || "未关联分集"}</span><strong>{activeTask.storyboard_code || "父分镜"} · {canonicalSceneCode(activeTask.scene_code) || "未绑定场景"} {activeTask.scene_name || ""}</strong></div>
        <nav aria-label="分镜执行步骤">
          {activeUnit.tasks.map((task) => <button className={`${task.id === activeTask.id ? "active" : ""} ${storyboardStepState(task)}`} key={task.id} type="button" onClick={() => setActiveTaskId(task.id)}>
            <span>{storyboardExecutionLabel(task)}</span><small>{taskStatusLabel(task)}</small>
          </button>)}
        </nav>
      </header>
      <TaskWorkItem
        canReview={false}
        loading={loading}
        projectName={projectNames.get(activeTask.project_id) || "未命名项目"}
        task={activeTask}
        onArchiveSubmission={onArchiveSubmission}
        onGeneratePrompt={onGeneratePrompt}
        onDeleteDraftSubmission={onDeleteDraftSubmission}
        onReviewTask={onReviewTask}
        onSavePrompt={onSavePrompt}
        onSetPrimarySubmission={onSetPrimarySubmission}
        onSubmitTask={onSubmitTask}
        onUploadCandidates={onUploadCandidates}
      />
    </main> : <div className="storyboard-workbench-empty">选择左侧父分镜开始制作</div>}
  </section>;
}

type ReviewCategory = "all" | "character" | "scene" | "prop" | "audio" | "storyboard";

function ReviewWorkbench({
  units,
  loading,
  projectNames,
  onReviewTask,
  onReviewCharacterUnit,
  onArchiveSubmission,
  onSetPrimarySubmission,
}: {
  units: TaskUnit[];
  loading: boolean;
  projectNames: Map<string, string>;
  onReviewTask: MyTasksPageProps["onReviewTask"];
  onReviewCharacterUnit: MyTasksPageProps["onReviewCharacterUnit"];
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
}) {
  const [category, setCategory] = React.useState<ReviewCategory>("all");
  const [view, setView] = React.useState<"flow" | "pending">("pending");
  const [query, setQuery] = React.useState("");
  const filteredUnits = React.useMemo(() => units.filter((unit) => {
    const projectName = projectNames.get(unit.tasks[0]?.project_id) || "未命名项目";
    const categoryMatches = category === "all" || reviewUnitCategory(unit) === category;
    const text = [
      projectName,
      unit.asset?.asset_code,
      unit.asset?.name,
      unit.tasks[0]?.title,
      unit.tasks[0]?.scene_code,
      unit.tasks[0]?.scene_name,
      unit.tasks[0]?.assignee_name,
    ].filter(Boolean).join(" ").toLocaleLowerCase();
    const reviewable = unit.tasks.some((task) => ["submitted", "reviewing"].includes(task.status));
    return categoryMatches && (view === "flow" || reviewable) && (!query.trim() || text.includes(query.trim().toLocaleLowerCase()));
  }).sort(compareReviewUnits), [category, projectNames, query, units, view]);
  const [activeKey, setActiveKey] = React.useState(filteredUnits[0]?.key || "");
  const activeUnit = filteredUnits.find((unit) => unit.key === activeKey) || filteredUnits[0];

  React.useEffect(() => {
    if (activeUnit) {
      if (activeKey !== activeUnit.key) setActiveKey(activeUnit.key);
      return;
    }
    setActiveKey("");
  }, [activeKey, activeUnit]);

  return <section className="review-workbench" aria-label="成果审核工作台">
    <aside className="review-queue-panel">
      <div className="review-queue-filter">
        <div className="review-flow-tabs" aria-label="审批视图">
          <button className={view === "pending" ? "active" : ""} type="button" onClick={() => setView("pending")}>待我审核</button>
          <button className={view === "flow" ? "active" : ""} type="button" onClick={() => setView("flow")}>交接流</button>
        </div>
        <input aria-label="搜索待审任务" placeholder="搜索编号、名称或提交人" value={query} onChange={(event) => setQuery(event.target.value)} />
        <div className="review-category-tabs">
          {([
            ["all", "全部"], ["character", "人物"], ["scene", "场景"], ["prop", "道具"], ["audio", "音频"], ["storyboard", "分镜"],
          ] as Array<[ReviewCategory, string]>).map(([key, label]) => <button className={category === key ? "active" : ""} key={key} type="button" onClick={() => setCategory(key)}>{label}</button>)}
        </div>
      </div>
      <div className="review-queue-list">
        {filteredUnits.map((unit) => {
          const task = unit.tasks[0];
          const projectName = projectNames.get(task.project_id) || "未命名项目";
          const batches = unit.tasks.map(reviewDisplayBatch).filter(Boolean) as SubmissionBatch[];
          const candidateCount = unit.tasks.reduce((count, item) => (
            count + activeSubmissions(item.submissions || []).length
          ), 0);
          const flow = reviewFlowSummary(unit);
          return <button className={`review-queue-item ${activeUnit?.key === unit.key ? "active" : ""}`} key={unit.key} type="button" onClick={() => setActiveKey(unit.key)}>
            <span className="review-queue-item-top"><strong>{unit.asset ? assetCodeDisplay(unit.asset.asset_code, "编码异常") : canonicalSceneCode(task.scene_code) || task.task_variant || "任务"}</strong><em>{reviewCategoryLabel(unit)}</em></span>
            <b>{unit.asset?.name || task.scene_name || task.title}</b>
            <span className="review-queue-project">{projectName}</span>
            <span className="review-flow-line"><i className={flow.producer ? "active" : ""}>制作员{flow.producer ? " " + flow.producer : ""}</i><b>→</b><i className={flow.director ? "active" : ""}>导演待审{flow.director ? ` · ${flow.director}${reviewStepUnitLabel(unit)}` : ""}</i><b>→</b><i className={flow.done ? "done" : ""}>结果</i></span>
            <small>{flow.detail}</small>
            <span>{unit.character ? `${unit.tasks.length} 个图位 · ${candidateCount} 张候选` : `${candidateCount} 个候选`} · {task.assignee_name || "未记录提交人"}</span>
            <small>{formatDateTime(batches[0]?.submitted_at || batches[0]?.reviewed_at || task.updated_at)}</small>
          </button>;
        })}
        {!filteredUnits.length ? <div className="review-queue-empty">没有符合条件的审核任务</div> : null}
      </div>
    </aside>
    {activeUnit ? <ReviewUnitWorkspace
      key={activeUnit.key}
      loading={loading}
      projectName={projectNames.get(activeUnit.tasks[0].project_id) || "未命名项目"}
      unit={activeUnit}
      onArchiveSubmission={onArchiveSubmission}
      onReviewCharacterUnit={onReviewCharacterUnit}
      onReviewTask={onReviewTask}
      onSetPrimarySubmission={onSetPrimarySubmission}
    /> : <div className="review-workbench-empty">选择左侧任务开始审核</div>}
  </section>;
}

function ReviewUnitWorkspace({
  unit,
  loading,
  projectName,
  onReviewTask,
  onReviewCharacterUnit,
  onArchiveSubmission,
  onSetPrimarySubmission,
}: {
  unit: TaskUnit;
  loading: boolean;
  projectName: string;
  onReviewTask: MyTasksPageProps["onReviewTask"];
  onReviewCharacterUnit: MyTasksPageProps["onReviewCharacterUnit"];
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
}) {
  const tasksWithBatches = React.useMemo(() => unit.tasks.map((task) => ({ task, batch: reviewDisplayBatch(task) })).filter((entry) => entry.batch), [unit]);
  const initialTask = tasksWithBatches[0]?.task || unit.tasks[0];
  const [activeTaskId, setActiveTaskId] = React.useState(initialTask.id);
  const activeTask = unit.tasks.find((task) => task.id === activeTaskId) || initialTask;
  const activeTaskBatches = (activeTask.submission_batches || [])
    .filter((batch) => (activeTask.submissions || []).some((submission) => (
      submission.batch_id === batch.id && !submission.is_invalidated
    )))
    .slice()
    .sort((left, right) => right.version_no - left.version_no);
  const [activeBatchId, setActiveBatchId] = React.useState(reviewDisplayBatch(initialTask)?.id || "");
  const activeBatch = activeTaskBatches.find((batch) => batch.id === activeBatchId) || reviewDisplayBatch(activeTask);
  // The batch selector is authoritative for the review canvas: selecting
  // batch 2 must show every candidate in batch 2, not the first candidate of
  // the default/latest batch.
  const allCandidates = activeTask.submissions || [];
  const batchCandidates = activeBatch
    ? allCandidates.filter((submission) => submission.batch_id === activeBatch.id)
    : allCandidates;
  const visibleBatchCandidates = batchCandidates.filter((submission) => !submission.is_invalidated);
  const activeCandidates = activeBatch?.status === "submitted"
    ? activeSubmissions(visibleBatchCandidates)
    : visibleBatchCandidates;
  const activeBatchCandidates = activeCandidates;
  const [activeSubmissionId, setActiveSubmissionId] = React.useState(activeCandidates[0]?.id || "");
  const activeSubmission = activeCandidates.find((submission) => submission.id === activeSubmissionId) || activeCandidates[0];
  const [selectedByTask, setSelectedByTask] = React.useState<Record<string, string[]>>(() => reviewInitialSelections(unit.tasks));
  const [primaryByTask, setPrimaryByTask] = React.useState<Record<string, string | null>>(() => reviewInitialPrimaries(unit.tasks));
  const submittedEntries = tasksWithBatches.filter((entry) => entry.batch?.status === "submitted") as Array<{ task: Task; batch: SubmissionBatch }>;
  const batchSignature = tasksWithBatches.map((entry) => `${entry.batch?.id}:${entry.batch?.status}`).join("|");
  const draftKey = `cineforge-review-comment:${batchSignature}`;
  const [reviewComment, setReviewComment] = React.useState(() => localStorage.getItem(draftKey) || "");
  const [reworkTaskIds, setReworkTaskIds] = React.useState(() => submittedEntries.map((entry) => entry.task.id));
  const [approvedReworkTarget, setApprovedReworkTarget] = React.useState<{ task: Task; batch: SubmissionBatch } | null>(null);
  const [approvedReworkReason, setApprovedReworkReason] = React.useState("");
  const pendingReview = submittedEntries.length > 0;
  const allBatchesSubmitted = unit.character
    ? characterReviewScopeReady(unit.tasks, submittedEntries.map(({ task }) => task.id))
    : submittedEntries.length > 0;
  const activeCandidateReviewable = activeBatch?.status === "submitted"
    && Boolean(activeSubmission);
  const allReady = pendingReview && allBatchesSubmitted && submittedEntries.every(({ task }) => {
    const primaryId = primaryByTask[task.id];
    const selectedIds = selectedByTask[task.id] || [];
    return unit.character || unit.asset?.asset_type === "character"
      ? characterReviewSelectionReady(task.task_variant, selectedIds, primaryId)
      : Boolean(selectedIds.length);
  });

  React.useEffect(() => {
    setActiveBatchId(reviewDisplayBatch(activeTask)?.id || "");
  }, [activeTask.id]);

  React.useEffect(() => {
    setSelectedByTask(reviewInitialSelections(unit.tasks));
    setPrimaryByTask(reviewInitialPrimaries(unit.tasks));
    setReviewComment(localStorage.getItem(draftKey) || "");
    setReworkTaskIds(submittedEntries.map((entry) => entry.task.id));
  }, [batchSignature, draftKey]);

  React.useEffect(() => {
    if (!activeCandidates.some((submission) => submission.id === activeSubmissionId)) {
      setActiveSubmissionId(activeCandidates[0]?.id || "");
    }
  }, [activeCandidates, activeSubmissionId]);

  const toggleSelected = React.useCallback((taskId: string, submissionId: string) => {
    setSelectedByTask((current) => {
      const selected = current[taskId] || [];
      const removing = selected.includes(submissionId);
      if (removing && primaryByTask[taskId] === submissionId) {
        setPrimaryByTask((primaries) => ({ ...primaries, [taskId]: null }));
      }
      return { ...current, [taskId]: removing ? selected.filter((id) => id !== submissionId) : [...selected, submissionId] };
    });
  }, [primaryByTask]);

  const togglePrimary = React.useCallback((taskId: string, submissionId: string) => {
    setPrimaryByTask((current) => ({ ...current, [taskId]: current[taskId] === submissionId ? null : submissionId }));
    setSelectedByTask((current) => ({
      ...current,
      [taskId]: Array.from(new Set([...(current[taskId] || []), submissionId])),
    }));
  }, []);

  const moveCandidate = React.useCallback((direction: number) => {
    if (!activeCandidates.length) return;
    const currentIndex = Math.max(0, activeCandidates.findIndex((submission) => submission.id === activeSubmission?.id));
    const nextIndex = (currentIndex + direction + activeCandidates.length) % activeCandidates.length;
    setActiveSubmissionId(activeCandidates[nextIndex].id);
  }, [activeCandidates, activeSubmission?.id]);

  React.useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, button, [contenteditable='true']")) return;
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        moveCandidate(event.key === "ArrowLeft" ? -1 : 1);
        return;
      }
      const poseTask = unit.tasks.find((task) => String(task.task_variant || "").toUpperCase() === event.key.toUpperCase());
      if (poseTask && /^[a-e]$/i.test(event.key)) {
        event.preventDefault();
        setActiveTaskId(poseTask.id);
        return;
      }
      if (!activeCandidateReviewable || !activeSubmission) return;
      if (event.key === "Enter") {
        event.preventDefault();
        toggleSelected(activeTask.id, activeSubmission.id);
      } else if (event.key.toLowerCase() === "m") {
        event.preventDefault();
        togglePrimary(activeTask.id, activeSubmission.id);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [activeCandidateReviewable, activeSubmission, activeTask.id, moveCandidate, togglePrimary, toggleSelected, unit.tasks]);

  function updateComment(value: string) {
    setReviewComment(value);
    if (value.trim()) localStorage.setItem(draftKey, value);
    else localStorage.removeItem(draftKey);
  }

  async function approve() {
    if (!allReady) return;
    if (unit.tasks.length > 1) {
      await onReviewCharacterUnit(submittedEntries.map(({ task, batch }) => ({
        task,
        batchId: batch.id,
        primarySubmissionId: primaryByTask[task.id] || null,
        selectedSubmissionIds: selectedByTask[task.id] || [],
      })), "approve", reviewComment);
    } else {
      const entry = submittedEntries[0];
      if (!entry) return;
      await onReviewTask(entry.task, entry.batch.id, "approve", primaryByTask[entry.task.id] || null, selectedByTask[entry.task.id] || [], reviewComment);
    }
    localStorage.removeItem(draftKey);
  }

  async function rework() {
    const entries = submittedEntries.filter(({ task }) => reworkTaskIds.includes(task.id));
    if (!entries.length || !reviewComment.trim()) return;
    if (unit.tasks.length > 1) {
      await onReviewCharacterUnit(entries.map(({ task, batch }) => ({ task, batchId: batch.id, primarySubmissionId: null, selectedSubmissionIds: [] })), "rework", reviewComment);
    } else {
      const entry = entries[0];
      await onReviewTask(entry.task, entry.batch.id, "rework", null, [], reviewComment);
    }
    localStorage.removeItem(draftKey);
  }

  async function confirmApprovedRework() {
    if (!approvedReworkTarget || !approvedReworkReason.trim()) return;
    await onReviewTask(
      approvedReworkTarget.task,
      approvedReworkTarget.batch.id,
      "rework",
      null,
      [],
      approvedReworkReason.trim(),
    );
    setApprovedReworkTarget(null);
    setApprovedReworkReason("");
  }

  return <>
    <main className={[
      "review-canvas-panel",
      unit.tasks.length > 1 ? "has-pose-tabs" : "",
      activeTaskBatches.length > 1 ? "has-batch-tabs" : "",
    ].filter(Boolean).join(" ")}>
      <header className="review-canvas-heading">
        <div><span>{projectName} · {reviewCategoryLabel(unit)}</span><strong>{unit.asset ? assetCodeDisplay(unit.asset.asset_code, "编码异常") : canonicalSceneCode(activeTask.scene_code) || "任务"} {unit.asset?.name || activeTask.scene_name || activeTask.title}</strong></div>
        <span className={`badge ${unit.queue === "completed" ? "mint" : unit.queue === "reviewing" ? "sky" : "sun"}`}>{queueLabel(unit.queue)}</span>
      </header>
      {unit.tasks.length > 1 ? <nav className="review-pose-tabs" aria-label={unit.character ? "人物定装图位" : "分镜执行步骤"}>
        {unit.tasks.map((task) => {
          const batch = reviewDisplayBatch(task);
          const candidates = (task.submissions || []).filter((submission) => (
            submission.batch_id === batch?.id
            && !submission.is_invalidated
            && (batch?.status !== "submitted" || isActiveSubmission(submission))
          ));
          const ready = unit.character
            ? characterReviewSelectionReady(
              task.task_variant,
              selectedByTask[task.id] || [],
              primaryByTask[task.id],
            )
            : Boolean(selectedByTask[task.id]?.length);
          return <button className={`${task.id === activeTask.id ? "active" : ""} ${ready ? "ready" : ""}`} key={task.id} type="button" onClick={() => setActiveTaskId(task.id)}>
            <strong>{unit.character ? task.task_variant || "图位" : storyboardExecutionLabel(task)}</strong><span>{unit.character ? poseTitle(task) : task.title}</span><em>{candidates.length} 项</em>
          </button>;
        })}
      </nav> : null}
      {activeTaskBatches.length > 1 ? <div className="review-batch-buttons" aria-label="查看成果版本">
        <span className="review-batch-buttons-label">成果版本</span>
        <div className="review-batch-buttons-list">
          {activeTaskBatches.map((batch) => <button
            className={`review-batch-button ${activeBatch?.id === batch.id ? "active" : ""} status-${batch.status}`}
            key={batch.id}
            type="button"
            onClick={() => setActiveBatchId(batch.id)}
          >
            <strong>V{String(batch.version_no).padStart(3, "0")}</strong>
            <span>{reviewBatchStatusLabel(batch.status)}</span>
          </button>)}
        </div>
      </div> : null}
      <ReviewMediaCanvas submission={activeSubmission} />
      <div className="review-filmstrip" aria-label="候选成果">
        <button aria-label="上一个候选" className="review-filmstrip-nav" disabled={activeCandidates.length < 2} title="上一个候选" type="button" onClick={() => moveCandidate(-1)}>‹</button>
        <div className="review-filmstrip-list">
          {activeCandidates.map((submission, index) => <button className={`review-filmstrip-item ${submission.id === activeSubmission?.id ? "active" : ""} ${selectedByTask[activeTask.id]?.includes(submission.id) ? "selected" : ""} ${primaryByTask[activeTask.id] === submission.id ? "primary" : ""} ${!isActiveSubmission(submission) ? "archived" : ""}`} key={submission.id} type="button" onClick={() => setActiveSubmissionId(submission.id)}>
            <ReviewCandidateThumb submission={submission} />
            <span><strong>版本 {index + 1}</strong><small>{formatDateTime(submission.created_at)}</small></span>
            {submission.is_invalidated ? <em>已失效</em> : submission.is_archived ? <em>已移除</em> : primaryByTask[activeTask.id] === submission.id ? <em>主母版</em> : selectedByTask[activeTask.id]?.includes(submission.id) ? <em>已定版</em> : null}
          </button>)}
          {!activeCandidates.length ? <div className="review-filmstrip-empty">当前图位没有可审核的候选成果</div> : null}
        </div>
        <button aria-label="下一个候选" className="review-filmstrip-nav" disabled={activeCandidates.length < 2} title="下一个候选" type="button" onClick={() => moveCandidate(1)}>›</button>
      </div>
    </main>
    <aside className="review-inspector">
      <div className="review-inspector-head"><strong>审核信息</strong><span>{activeBatch ? `第 ${activeBatch.version_no} 批` : "无批次"}</span></div>
      <dl className="review-metadata">
        <div><dt>当前图位</dt><dd>{activeTask.task_variant ? `${activeTask.task_variant} · ${poseTitle(activeTask)}` : reviewCategoryLabel(unit)}</dd></div>
        <div><dt>提交人员</dt><dd>{activeTask.assignee_name || "未记录"}</dd></div>
        <div><dt>提交时间</dt><dd>{formatDateTime(activeBatch?.submitted_at || activeSubmission?.created_at)}</dd></div>
        <div><dt>生成模型</dt><dd>{activeSubmission?.model_name || "未记录"}</dd></div>
        <div><dt>文件类型</dt><dd>{submissionFileTypeLabel(activeSubmission)}</dd></div>
        <div><dt>成果状态</dt><dd>{activeSubmission ? submissionStatusLabel(activeSubmission) : "无"}</dd></div>
        <div><dt>项目名称</dt><dd>{projectName}</dd></div>
      </dl>
      <StoryboardDialogueReference compact task={activeTask} />
      <details className="review-prompt-detail"><summary>{activeTask.variant_kind === "human_temporary" ? "查看人工生产要求" : "查看本次生产提示词"}</summary><p>{activeTask.variant_kind === "human_temporary" ? activeTask.variant_description_zh || "未填写人工生产要求" : activeSubmission?.revised_prompt_text || activeSubmission?.prompt_text || activeTask.latest_prompt_text || activeTask.prompt_text || "暂无提示词记录"}</p></details>
      {activeCandidateReviewable && activeSubmission ? <div className="review-selection-panel">
        <span>当前候选</span>
        <div className="review-selection-actions">
          <button className={`review-select-button ${selectedByTask[activeTask.id]?.includes(activeSubmission.id) ? "selected" : ""}`} aria-pressed={selectedByTask[activeTask.id]?.includes(activeSubmission.id)} type="button" onClick={() => toggleSelected(activeTask.id, activeSubmission.id)}><strong>{selectedByTask[activeTask.id]?.includes(activeSubmission.id) ? "✓" : "+"}</strong><span>定版</span></button>
          {characterTaskRequiresPrimary(activeTask, unit) ? <button className={`review-select-button primary ${primaryByTask[activeTask.id] === activeSubmission.id ? "selected" : ""}`} aria-pressed={primaryByTask[activeTask.id] === activeSubmission.id} type="button" onClick={() => togglePrimary(activeTask.id, activeSubmission.id)}><strong>{primaryByTask[activeTask.id] === activeSubmission.id ? "◆" : "◇"}</strong><span>身份主母版</span></button> : null}
        </div>
      </div> : null}
      {activeBatch ? <p className="review-validation">当前查看第 {activeBatch.version_no} 批，共 {activeCandidates.length} 张候选。切换“查看批次”可查看其他批次；只有待审核批次可以定版。</p> : null}
      {pendingReview && unit.character ? <fieldset className="review-rework-scope">
        <legend>退回图位</legend>
        <div>{submittedEntries.map(({ task }) => <button className={reworkTaskIds.includes(task.id) ? "active" : ""} key={task.id} type="button" onClick={() => setReworkTaskIds((current) => current.includes(task.id) ? current.filter((id) => id !== task.id) : [...current, task.id])}>{task.task_variant}</button>)}</div>
      </fieldset> : null}
      {pendingReview ? <label className="review-comment-field"><span>审核意见</span><textarea placeholder="退回修改时必须填写具体原因" value={reviewComment} onChange={(event) => updateComment(event.target.value)} /></label> : activeBatch?.review_comment ? <div className="review-comment-readonly"><span>审核意见</span><p>{activeBatch.review_comment}</p></div> : null}
      {pendingReview ? <div className="review-final-actions">
        <button className="btn" disabled={loading || !reworkTaskIds.length || !reviewComment.trim()} type="button" onClick={rework}>退回修改</button>
        <button className="btn primary" disabled={loading || !allReady} type="button" onClick={approve}>{unit.character ? characterReviewActionLabel(submittedEntries.map(({ task }) => task), "通过并定版") : "审核通过并定版"}</button>
      </div> : activeSubmission && ["primary_master", "alternate_master", "not_selected"].includes(activeSubmission.status) ? <div className="review-final-actions completed">
        {!activeSubmission.is_primary && activeSubmission.is_selected && isActiveSubmission(activeSubmission) ? <button className="btn primary" disabled={loading} type="button" onClick={() => onSetPrimarySubmission(activeTask, activeSubmission.id)}>设为主母版</button> : null}
        {!activeSubmission.is_primary && !activeSubmission.is_invalidated ? <button className="btn" disabled={loading} type="button" onClick={() => onArchiveSubmission(activeTask, activeSubmission.id, !activeSubmission.is_archived)}>{activeSubmission.is_archived ? "恢复素材" : "归档素材"}</button> : null}
        {activeBatch?.status === "approved" && reviewDisplayBatch(activeTask)?.id === activeBatch.id && activeCandidates.some((submission) => submissionMediaKind(submission) === "image") ? <button className="btn danger-outline" disabled={loading} type="button" onClick={() => {
          setApprovedReworkReason("");
          setApprovedReworkTarget({ task: activeTask, batch: activeBatch });
        }}>撤销通过并退回修改</button> : null}
      </div> : null}
      {pendingReview && !allReady ? <p className="review-validation">{allBatchesSubmitted ? (unit.character || unit.asset?.asset_type === "character" ? "每个待审图位至少选择一张定版；A 图还必须指定身份主母版。" : "请至少选择一张或多张角度成果后再通过。") : "本次处于待审状态的图位尚未全部提交。"}</p> : null}
    </aside>
    {approvedReworkTarget ? <div className="modal-backdrop" role="presentation" onClick={() => { if (!loading) setApprovedReworkTarget(null); }}>
      <section className="approved-rework-confirm" role="alertdialog" aria-modal="true" aria-label="确认撤销审批通过" onClick={(event) => event.stopPropagation()}>
        <header><div><strong>确认撤销审批通过</strong><span>该批图片将退出已通过状态，并重新进入制作员返工队列。</span></div><button className="icon-btn" disabled={loading} title="关闭" type="button" onClick={() => setApprovedReworkTarget(null)}><X aria-hidden="true" size={17} /></button></header>
        <dl><div><dt>任务</dt><dd>{approvedReworkTarget.task.title}</dd></div><div><dt>成果版本</dt><dd>V{String(approvedReworkTarget.batch.version_no).padStart(3, "0")}</dd></div></dl>
        <label className="field"><span>再次退回原因</span><textarea autoFocus maxLength={500} placeholder="请明确填写需要修订的画面、构图或一致性问题" value={approvedReworkReason} onChange={(event) => setApprovedReworkReason(event.target.value)} /></label>
        <p><AlertTriangle aria-hidden="true" size={16} />此操作会通知制作员返工，并保留原审批记录。新成果必须重新提交和审核。</p>
        <footer><button className="btn" disabled={loading} type="button" onClick={() => setApprovedReworkTarget(null)}>取消</button><button className="btn danger" disabled={loading || !approvedReworkReason.trim()} type="button" onClick={() => void confirmApprovedRework()}>{loading ? "处理中" : "确认撤销并退回"}</button></footer>
      </section>
    </div> : null}
  </>;
}

function ReviewMediaCanvas({
  submission,
  emptyContent,
  onDelete,
}: {
  submission?: Submission;
  emptyContent?: React.ReactNode;
  onDelete?: () => Promise<void>;
}) {
  const [url, renewMediaUrl] = useSubmissionMediaUrl(submission, true);
  const [fit, setFit] = React.useState(true);
  const [scale, setScale] = React.useState(1);
  const [offset, setOffset] = React.useState({ x: 0, y: 0 });
  const [dragging, setDragging] = React.useState(false);
  const [expanded, setExpanded] = React.useState(false);
  const [confirmingDelete, setConfirmingDelete] = React.useState(false);
  const [deleting, setDeleting] = React.useState(false);
  const stageRef = React.useRef<HTMLDivElement | null>(null);
  const imageRef = React.useRef<HTMLImageElement | null>(null);
  const dragOrigin = React.useRef<{ x: number; y: number; offsetX: number; offsetY: number } | null>(null);
  const mediaKind = submissionMediaKind(submission);
  const isVideo = mediaKind === "video";
  const isAudio = mediaKind === "audio";
  const canDelete = Boolean(
    submission
    && onDelete
    && mediaKind === "image"
    && isMakerRemovableSubmission(submission),
  );

  React.useEffect(() => {
    setFit(true);
    setScale(1);
    setOffset({ x: 0, y: 0 });
  }, [submission?.id]);

  React.useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable='true']")) return;
      if (event.key.toLowerCase() === "f") {
        setFit(true);
        setOffset({ x: 0, y: 0 });
      } else if (event.key === "1") {
        setFit(false);
        setScale(1);
        setOffset({ x: 0, y: 0 });
      } else if (event.key === "Escape" && expanded) {
        setExpanded(false);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [expanded]);

  function actualSize() {
    setFit(false);
    setScale(1);
    setOffset({ x: 0, y: 0 });
  }

  function fittedImageScale() {
    const stage = stageRef.current;
    const image = imageRef.current;
    if (!stage || !image?.naturalWidth || !image.naturalHeight) return 1;
    return Math.min(1, stage.clientWidth / image.naturalWidth, stage.clientHeight / image.naturalHeight);
  }

  function zoom(delta: number, focus?: { clientX: number; clientY: number }) {
    const stage = stageRef.current;
    const currentScale = fit ? fittedImageScale() : scale;
    const currentOffset = fit ? { x: 0, y: 0 } : offset;
    const nextScale = Math.min(4, Math.max(0.25, Math.round((currentScale + delta) * 100) / 100));
    let nextOffset = currentOffset;
    if (focus && stage && currentScale > 0) {
      const bounds = stage.getBoundingClientRect();
      const focusX = focus.clientX - bounds.left - bounds.width / 2;
      const focusY = focus.clientY - bounds.top - bounds.height / 2;
      const ratio = nextScale / currentScale;
      nextOffset = {
        x: focusX - (focusX - currentOffset.x) * ratio,
        y: focusY - (focusY - currentOffset.y) * ratio,
      };
    }
    setFit(false);
    setScale(nextScale);
    setOffset(nextOffset);
  }

  function onPointerDown(event: React.PointerEvent<HTMLDivElement>) {
    if (fit || mediaKind !== "image" || event.button !== 0) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    dragOrigin.current = { x: event.clientX, y: event.clientY, offsetX: offset.x, offsetY: offset.y };
    setDragging(true);
  }

  function onPointerMove(event: React.PointerEvent<HTMLDivElement>) {
    if (!dragOrigin.current) return;
    setOffset({
      x: dragOrigin.current.offsetX + event.clientX - dragOrigin.current.x,
      y: dragOrigin.current.offsetY + event.clientY - dragOrigin.current.y,
    });
  }

  function endDrag(event: React.PointerEvent<HTMLDivElement>) {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
    dragOrigin.current = null;
    setDragging(false);
  }

  async function confirmDelete() {
    if (!onDelete || !canDelete) return;
    setDeleting(true);
    try {
      await onDelete();
      setConfirmingDelete(false);
    } finally {
      setDeleting(false);
    }
  }

  return <div className={`review-media-viewer ${expanded ? "expanded" : ""}`}>
    <div className="review-viewer-toolbar">
      <span>{submission ? `${submissionFileTypeLabel(submission)}原文件` : "暂无候选"}</span>
      {mediaKind === "image" && submission ? <div>
        <button className={fit ? "active" : ""} title="适应窗口" type="button" onClick={() => { setFit(true); setOffset({ x: 0, y: 0 }); }}>适应</button>
        <button className={!fit && scale === 1 ? "active" : ""} title="按原始像素显示" type="button" onClick={actualSize}>1:1</button>
        <button aria-label="缩小" title="缩小" type="button" onClick={() => zoom(-0.25)}>-</button>
        <output>{fit ? "适应" : `${Math.round(scale * 100)}%`}</output>
        <button aria-label="放大" title="放大" type="button" onClick={() => zoom(0.25)}>+</button>
        <button title={expanded ? "退出全屏查看" : "全屏查看"} type="button" onClick={() => setExpanded((value) => !value)}>{expanded ? "退出全屏" : "全屏"}</button>
        {canDelete && submission ? <SubmissionRemoveButton disabled={deleting} submission={submission} onRequest={() => setConfirmingDelete(true)} /> : null}
      </div> : null}
    </div>
    <div
      ref={stageRef}
      className={`review-media-stage ${fit ? "fit" : "actual"} ${dragging ? "dragging" : ""}`}
      onDoubleClick={() => { if (mediaKind === "image") fit ? actualSize() : setFit(true); }}
      onPointerCancel={endDrag}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onWheel={(event) => { if (mediaKind === "image" && submission) { event.preventDefault(); zoom(event.deltaY < 0 ? 0.25 : -0.25, { clientX: event.clientX, clientY: event.clientY }); } }}
    >
      {!submission ? emptyContent || <div className="review-media-message">当前图位没有候选成果</div>
        : !url ? <div className="review-media-message">正在读取原始文件</div>
          : isVideo ? <RenewableVideo controls preload="metadata" src={url} onPlaybackError={() => renewMediaUrl(true)} />
            : isAudio ? <div className="review-audio-player"><AudioLines aria-hidden="true" size={42} /><audio controls preload="metadata" src={url} onError={() => renewMediaUrl(true)} /></div>
            : <div className="review-image-pan-layer" style={fit ? undefined : { transform: `translate(${offset.x}px, ${offset.y}px)` }}><img ref={imageRef} draggable={false} src={url} alt="待审核候选原图" style={fit ? undefined : { transform: `scale(${scale})` }} /></div>}
      {confirmingDelete ? <div className="review-viewer-delete-confirm" role="alertdialog" aria-modal="true" aria-label="确认移除当前图片">
        <Trash2 aria-hidden="true" size={24} />
        <strong>{submission?.status === "rejected" ? "移除已驳回图片？" : "移除当前草稿？"}</strong>
        <p>图片将退出制作和资产消费列表，导演审核历史、原始文件和操作记录仍会保留。</p>
        <div><button className="btn" disabled={deleting} type="button" onClick={() => setConfirmingDelete(false)}>取消</button><button className="btn danger" disabled={deleting} type="button" onClick={() => void confirmDelete()}>{deleting ? "移除中" : "确认移除"}</button></div>
      </div> : null}
    </div>
  </div>;
}

function ReviewCandidateThumb({ submission }: { submission: Submission }) {
  const [url, renewMediaUrl] = useSubmissionMediaUrl(submission, true);
  if (!url) return <span className="review-thumb-placeholder">加载中</span>;
  if (submissionMediaKind(submission) === "audio") return <span className="review-thumb-audio"><AudioLines aria-hidden="true" size={22} /></span>;
  return submission.file_type === "video"
    ? <RenewableVideo muted preload="metadata" src={url} onPlaybackError={() => renewMediaUrl(true)} />
    : <img loading="lazy" src={url} alt="候选缩略图" />;
}


function submissionMediaKind(submission?: Submission): TaskMediaKind {
  const descriptor = String(submission?.file_type || "").toLowerCase();
  if (["audio", "voice", "voice_profile", "music"].some((token) => descriptor.includes(token))) return "audio";
  return descriptor.includes("video") ? "video" : "image";
}


function mediaKindLabel(kind: TaskMediaKind) {
  return kind === "audio" ? "音频" : kind === "video" ? "视频" : "图片";
}


function submissionFileTypeLabel(submission?: Submission) {
  return submission ? mediaKindLabel(submissionMediaKind(submission)) : "未记录";
}

function RenewableVideo({
  onPlaybackError,
  ...props
}: Omit<React.VideoHTMLAttributes<HTMLVideoElement>, "onError"> & { onPlaybackError: () => void }) {
  const videoRef = React.useRef<HTMLVideoElement | null>(null);
  const playback = React.useRef({ currentTime: 0, playing: false });
  const restoring = React.useRef(false);

  function snapshot() {
    const video = videoRef.current;
    if (!video) return;
    playback.current = { currentTime: Number.isFinite(video.currentTime) ? video.currentTime : playback.current.currentTime, playing: !video.paused };
  }

  function restore() {
    const video = videoRef.current;
    if (!video || !restoring.current) return;
    if (playback.current.currentTime > 0) video.currentTime = playback.current.currentTime;
    if (playback.current.playing) void video.play().catch(() => undefined);
    restoring.current = false;
  }

  return <video
    {...props}
    ref={videoRef}
    onError={() => {
      snapshot();
      restoring.current = true;
      onPlaybackError();
    }}
    onLoadedMetadata={restore}
    onPause={() => { if (!restoring.current) snapshot(); }}
    onPlay={snapshot}
    onTimeUpdate={snapshot}
  />;
}

function useSubmissionMediaUrl(submission: Submission | undefined, requested: boolean) {
  const [url, setUrl] = React.useState("");
  const [renewalKey, setRenewalKey] = React.useState(0);
  const errorRenewedUrl = React.useRef("");
  const lastErrorRenewedAt = React.useRef(0);
  const standbyPlaybackUrl = React.useRef("");
  const renew = React.useCallback((fromPlaybackError = false) => {
    if (fromPlaybackError) {
      const now = Date.now();
      if (!url || errorRenewedUrl.current === url || now - lastErrorRenewedAt.current < 10000) return;
      errorRenewedUrl.current = url;
      lastErrorRenewedAt.current = now;
      if (standbyPlaybackUrl.current) {
        setUrl(standbyPlaybackUrl.current);
        standbyPlaybackUrl.current = "";
        return;
      }
    }
    setRenewalKey((current) => current + 1);
  }, [url]);

  React.useEffect(() => {
    errorRenewedUrl.current = "";
    lastErrorRenewedAt.current = 0;
    standbyPlaybackUrl.current = "";
    setRenewalKey(0);
  }, [submission?.file_id]);

  React.useEffect(() => {
    if (!submission?.file_id || !requested) {
      setUrl("");
      return;
    }
    const controller = new AbortController();
    let renewalTimer = 0;
    async function signPlaybackUrl(activate: boolean) {
      try {
        const playback = await api.getFilePlaybackUrl(submission.file_id!, controller.signal);
        if (activate) setUrl(playback.url);
        else standbyPlaybackUrl.current = playback.url;
        const renewAfterSeconds = Math.max(5, playback.expires_in_seconds - 45);
        renewalTimer = window.setTimeout(() => void signPlaybackUrl(false), renewAfterSeconds * 1000);
      } catch {
        if (controller.signal.aborted) return;
        if (activate) setUrl("");
        renewalTimer = window.setTimeout(() => void signPlaybackUrl(activate), 15000);
      }
    }
    void signPlaybackUrl(true);
    return () => {
      controller.abort();
      if (renewalTimer) window.clearTimeout(renewalTimer);
    };
  }, [renewalKey, requested, submission?.file_id, submission?.file_type]);
  return [url, renew] as const;
}

function reviewInitialSelections(tasks: Task[]) {
  return Object.fromEntries(tasks.map((task) => [task.id, (task.submissions || []).filter((submission) => submission.is_selected && isActiveSubmission(submission)).map((submission) => submission.id)]));
}

function reviewInitialPrimaries(tasks: Task[]) {
  return Object.fromEntries(tasks.map((task) => [task.id, (task.submissions || []).find((submission) => submission.is_primary && isActiveSubmission(submission))?.id || null]));
}

function reviewDisplayBatch(task: Task) {
  const batches = (task.submission_batches || []).slice().sort((left, right) => right.version_no - left.version_no);
  const hasActiveCandidate = (batch: SubmissionBatch) => (task.submissions || []).some((submission) => (
    submission.batch_id === batch.id && isActiveSubmission(submission)
  ));
  return batches.find((batch) => batch.status === "submitted" && hasActiveCandidate(batch))
    || batches.find((batch) => batch.status === "approved" && hasActiveCandidate(batch))
    || batches.find((batch) => batch.status === "rework" && hasActiveCandidate(batch));
}

function characterReviewActionLabel(tasks: Task[], action: string) {
  const variants = tasks.map((task) => String(task.task_variant || "图位").toUpperCase());
  if (variants.length === 1) return `${variants[0]} 图${action}`;
  return `${variants.join("、")} ${action}`;
}

function characterTaskRequiresPrimary(task: Task, unit?: TaskUnit) {
  if (!(unit?.character || unit?.asset?.asset_type === "character")) return false;
  return characterViewRequiresIdentityMaster(task.task_variant);
}

function reviewFlowSummary(unit: TaskUnit) {
  const tasks = unit.tasks;
  const producerTasks = tasks.filter((task) => ["todo", "in_progress", "rejected", "overdue"].includes(task.status));
  const directorTasks = tasks.filter((task) => ["submitted", "reviewing"].includes(task.status));
  const done = tasks.length > 0 && tasks.every((task) => task.status === "completed");
  const producer = producerTasks.length ? (producerTasks[0].assignee_name || "待分配") : "";
  const detail = producerTasks.length
    ? (producerTasks.some((task) => task.status === "rejected") ? "存在退回内容，制作员需返工" : "制作员仍有生产步骤")
    : directorTasks.length ? "已有成果提交，等待导演处理" : done ? "全部必需步骤已通过" : "尚未提交成果";
  return { producer, director: directorTasks.length, done, detail };
}

function compareReviewUnits(left: TaskUnit, right: TaskUnit) {
  const priority = (unit: TaskUnit) => {
    if (unit.tasks.some((task) => ["submitted", "reviewing"].includes(task.status))) return 0;
    if (!unit.tasks.every((task) => task.status === "completed")) return 1;
    return 2;
  };
  return priority(left) - priority(right) || compareTaskUnits(left, right);
}

function reviewStepUnitLabel(unit: TaskUnit) {
  if (unit.character) return "个图位";
  if (unit.tasks[0]?.storyboard_id) return "个步骤";
  return "项成果";
}

function reviewUnitCategory(unit: TaskUnit): ReviewCategory {
  if (unit.character) return "character";
  if (unit.asset?.asset_type === "scene") return "scene";
  if (unit.asset?.asset_type === "prop") return "prop";
  if (taskMediaKind(unit.tasks[0]) === "audio" || ["music", "voice_profile"].includes(unit.asset?.asset_type || "")) return "audio";
  return "storyboard";
}

function reviewCategoryLabel(unit: TaskUnit) {
  return ({ character: "人物定装", scene: "场景资产", prop: "道具资产", audio: "音频成果", storyboard: "分镜成果" } as Record<ReviewCategory, string>)[reviewUnitCategory(unit)] || "生产成果";
}

function reviewBatchStatusLabel(status: string) {
  return ({ submitted: "待审核", approved: "已通过", rework: "已退回", pending: "尚未提交" } as Record<string, string>)[status] || status;
}

function formatDateTime(value?: string | null) {
  if (!value) return "未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(date);
}

function characterUnitQueue(tasks: Task[]): QueueKey {
  if (tasks.every((task) => task.status === "completed")) return "completed";
  if (tasks.some((task) => ["reviewing", "submitted"].includes(task.status))) return "reviewing";
  if (tasks.some((task) => task.status === "rejected")) return "working";
  if (tasks.filter((task) => task.status !== "completed").every((task) => task.locked)) return "waiting";
  return "working";
}

export function CharacterTaskTable({
  units,
  canReview,
  loading,
  projectNames,
  onUploadCandidates,
  onSubmitCharacterUnit,
  onReviewCharacterUnit,
  onDeleteDraftSubmission,
  onArchiveSubmission,
  onSetPrimarySubmission,
  onSavePrompt,
  onGeneratePrompt,
}: {
  units: TaskUnit[];
  canReview: boolean;
  loading: boolean;
  projectNames: Map<string, string>;
  onUploadCandidates: UploadCandidatesHandler;
  onSubmitCharacterUnit: MyTasksPageProps["onSubmitCharacterUnit"];
  onReviewCharacterUnit: MyTasksPageProps["onReviewCharacterUnit"];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
  onSavePrompt: MyTasksPageProps["onSavePrompt"];
  onGeneratePrompt: MyTasksPageProps["onGeneratePrompt"];
}) {
  const [openKey, setOpenKey] = React.useState("");
  const openUnit = units.find((unit) => unit.key === openKey);
  return <>
    <div className="character-task-table-wrap">
      <table className="character-task-table">
        <thead><tr><th>项目</th><th>人物</th><th>年龄 / 装扮</th><th>定装图位</th><th>成果进度</th><th>状态</th><th>操作</th></tr></thead>
        <tbody>{units.map((unit) => {
          const completed = unit.tasks.filter((task) => task.status === "completed").length;
          const uploaded = unit.tasks.filter((task) => (task.submissions || []).some(isActiveSubmission)).length;
          const hasRework = unit.tasks.some((task) => taskHasRework(task));
          return <tr key={unit.key}>
            <td><strong>{projectNames.get(unit.tasks[0].project_id) || "未命名项目"}</strong></td>
            <td><div className="character-task-name"><strong>{assetCodeDisplay(unit.asset?.asset_code, "编码异常")}</strong><span>{unit.asset?.name || unit.tasks[0].title}</span></div></td>
            <td>{characterContextLabel(unit.tasks[0])}</td>
            <td>{unit.tasks.map((task) => task.task_variant).filter(Boolean).join(" / ")}</td>
            <td>{unit.tasks.filter((task) => !task.locked && !["completed", "reviewing", "submitted"].includes(task.status)).length}/{unit.tasks.length} 可独立生成 · {uploaded}/{unit.tasks.length} 已上传 · {completed}/{unit.tasks.length} 已定版</td>
            <td><span className={`badge ${unit.queue === "completed" ? "mint" : unit.queue === "reviewing" ? "sky" : "sun"}`}>{hasRework ? "制作中 · 已退回" : queueLabel(unit.queue)}</span></td>
            <td><button className="text-link-button" type="button" onClick={() => setOpenKey(unit.key)}>{canReview ? "查看并审批" : "定装提示词与上传"}</button></td>
          </tr>;
        })}</tbody>
      </table>
    </div>
    {openUnit ? <CharacterPoseModal
      canReview={canReview}
      loading={loading}
      projectName={projectNames.get(openUnit.tasks[0].project_id) || "未命名项目"}
      unit={openUnit}
      onArchiveSubmission={onArchiveSubmission}
      onClose={() => setOpenKey("")}
      onDeleteDraftSubmission={onDeleteDraftSubmission}
      onGeneratePrompt={onGeneratePrompt}
      onReviewCharacterUnit={onReviewCharacterUnit}
      onSavePrompt={onSavePrompt}
      onSetPrimarySubmission={onSetPrimarySubmission}
      onSubmitCharacterUnit={onSubmitCharacterUnit}
      onUploadCandidates={onUploadCandidates}
    /> : null}
  </>;
}

function AssetTaskTable({
  assetType,
  units,
  canReview,
  loading,
  projectNames,
  onUploadCandidates,
  onSubmitTask,
  onDeleteDraftSubmission,
  onReviewTask,
  onArchiveSubmission,
  onSetPrimarySubmission,
  onSavePrompt,
  onGeneratePrompt,
}: {
  assetType: string;
  units: TaskUnit[];
  canReview: boolean;
  loading: boolean;
  projectNames: Map<string, string>;
  onUploadCandidates: UploadCandidatesHandler;
  onSubmitTask: MyTasksPageProps["onSubmitTask"];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onReviewTask: MyTasksPageProps["onReviewTask"];
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
  onSavePrompt: MyTasksPageProps["onSavePrompt"];
  onGeneratePrompt: MyTasksPageProps["onGeneratePrompt"];
}) {
  const [openKey, setOpenKey] = React.useState("");
  const openUnit = units.find((unit) => unit.key === openKey);
  const label = assetType === "scene" ? "场景资产" : "道具资产";
  return <section className="production-asset-task-section">
    <header className="production-asset-task-title"><strong>{label}</strong><span>{units.length} 项</span></header>
    <div className="character-task-table-wrap">
      <table className="character-task-table production-asset-task-table">
        <thead><tr><th>项目</th><th>编号</th><th>名称</th><th>制作摘要</th><th>候选成果</th><th>状态</th><th>操作</th></tr></thead>
        <tbody>{units.map((unit) => {
          const task = unit.tasks[0];
          const candidates = activeSubmissions(task.submissions || []);
          const approved = candidates.filter((item) => item.is_selected && ["primary_master", "alternate_master"].includes(item.status));
          return <tr key={unit.key}>
            <td><strong>{projectNames.get(task.project_id) || "未命名项目"}</strong></td>
            <td><strong>{assetCodeDisplay(unit.asset?.asset_code, "编码异常")}</strong></td>
            <td><strong>{unit.asset?.name || task.title}</strong></td>
            <td className="production-asset-summary"><span>{unit.asset?.description || task.latest_prompt_text || task.prompt_text || "待补充"}</span></td>
            <td>{approved.length ? `${approved.length} 个已定版` : candidates.length ? `${candidates.length} 个候选` : "尚未上传"}</td>
            <td><span className={`badge ${unit.queue === "completed" ? "mint" : unit.queue === "reviewing" ? "sky" : "sun"}`}>{queueLabel(unit.queue)}</span></td>
            <td><button className="text-link-button" type="button" onClick={() => setOpenKey(unit.key)}>{canReview ? "查看并审批" : "提示词与上传"}</button></td>
          </tr>;
        })}</tbody>
      </table>
    </div>
    {openUnit ? <AssetProductionModal
      asset={openUnit.asset}
      assetType={assetType}
      loading={loading}
      projectName={projectNames.get(openUnit.tasks[0].project_id) || "未命名项目"}
      task={openUnit.tasks[0]}
      onClose={() => setOpenKey("")}
      onDeleteDraftSubmission={onDeleteDraftSubmission}
      onGeneratePrompt={onGeneratePrompt}
      onSavePrompt={onSavePrompt}
      onSubmitTask={onSubmitTask}
      onUploadCandidates={onUploadCandidates}
    /> : null}
  </section>;
}

function AssetProductionModal({
  asset,
  assetType,
  task,
  projectName,
  loading,
  onClose,
  onUploadCandidates,
  onSubmitTask,
  onDeleteDraftSubmission,
  onSavePrompt,
  onGeneratePrompt,
}: {
  asset?: Asset;
  assetType: string;
  task: Task;
  projectName: string;
  loading: boolean;
  onClose: () => void;
  onUploadCandidates: UploadCandidatesHandler;
  onSubmitTask: MyTasksPageProps["onSubmitTask"];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onSavePrompt: MyTasksPageProps["onSavePrompt"];
  onGeneratePrompt: MyTasksPageProps["onGeneratePrompt"];
}) {
  const label = assetType === "scene" ? "场景资产" : "道具资产";
  const humanTemporary = task.variant_kind === "human_temporary";
  const [draftPrompt, setDraftPrompt] = React.useState(task.latest_prompt_text || task.prompt_text || "");
  const [modelName, setModelName] = React.useState("Seedream");
  const [candidateMetadata, setCandidateMetadata] = React.useState<Record<string, { view_label: string; state_label: string; description: string }>>({});
  const [savingCandidateId, setSavingCandidateId] = React.useState<string | null>(null);
  const [previewSubmissionId, setPreviewSubmissionId] = React.useState<string | null>(null);
  const allSubmissions = task.submissions || [];
  const submissions = currentBatchSubmissions(allSubmissions, task.submission_batches || [], "result");
  const rejectedHistory = rejectedSubmissionHistory(allSubmissions, "result");
  const draftSubmissions = submissions.filter((submission) => submission.status === "draft");
  const locked = Boolean(task.locked);
  const waitingReview = ["reviewing", "submitted"].includes(task.status);
  const canUpload = !locked && !waitingReview && task.status !== "completed";

  React.useEffect(() => {
    setDraftPrompt(task.latest_prompt_text || task.prompt_text || "");
  }, [task.latest_prompt_text, task.prompt_text]);
  React.useEffect(() => {
    setCandidateMetadata(Object.fromEntries((task.submissions || []).map((submission) => [submission.id, {
      view_label: submission.view_label || "",
      state_label: submission.state_label || "",
      description: submission.description || "",
    }])));
  }, [task.id, task.submissions]);

  async function saveCandidateMetadata(submission: Submission) {
    const metadata = candidateMetadata[submission.id] || { view_label: "", state_label: "", description: "" };
    setSavingCandidateId(submission.id);
    try {
      await api.patch(`/tasks/${task.id}/submissions/${submission.id}`, metadata);
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        setAuthSession(null);
        window.location.reload();
        return;
      }
    } finally {
      setSavingCandidateId(null);
    }
  }

  return <div className="modal-backdrop" role="presentation" onClick={onClose}>
    <section className="character-pose-modal asset-production-modal" role="dialog" aria-modal="true" aria-label={`${label}生产任务`} onClick={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><h3>{assetCodeDisplay(asset?.asset_code, "资产")} {asset?.name || task.title}</h3><p>{projectName} · {label} · {humanTemporary ? "人工要求、候选成果与统一提交" : "提示词、候选成果与统一提交"}</p></div><button className="btn" type="button" onClick={onClose}>关闭</button></div>
      <TaskWorkflowAlerts task={task} onGeneratePrompt={onGeneratePrompt} />
      <div className="character-pose-workspace asset-production-workspace">
        <section className={`character-prompt-editor ${humanTemporary ? "human-production-requirement" : ""}`}>
          <div className="character-work-title"><strong>{humanTemporary ? "人工生产要求" : "生产提示词"}</strong><span>{taskStatusLabel(task)}</span></div>
          {humanTemporary ? <p>{task.variant_description_zh || "未填写人工生产要求。"}</p> : <textarea value={draftPrompt} onChange={(event) => setDraftPrompt(event.target.value)} />}
          {!humanTemporary ? <>
          <div className="asset-production-model-row"><select disabled={!canUpload} value={modelName} onChange={(event) => setModelName(event.target.value)}><option>Seedream</option><option>libimage</option></select></div>
          <div className="character-prompt-actions">
            <button className="btn" type="button" onClick={() => void copyText(draftPrompt)}>仅复制</button>
            <button className="btn" disabled={!canUpload} type="button" onClick={async () => { await copyText(draftPrompt); await onSavePrompt(task, draftPrompt, true); }}>复制并保存版本</button>
            <button className="btn" disabled={!canUpload} type="button" onClick={() => onSavePrompt(task, draftPrompt, false)}>保存提示词</button>
            <button className="btn" disabled={!canUpload || loading} type="button" onClick={() => onGeneratePrompt(task)}>Agent生成</button>
          </div>
          </> : <span className="human-production-note">本任务跳过提示词 Agent，由制作员按人工要求直接生产。</span>}
        </section>
        <section className="asset-production-candidates">
          <div className="character-work-title"><strong>候选成果</strong><span>{submissions.length} 个可用候选{allSubmissions.length > submissions.length ? ` · ${allSubmissions.length - submissions.length} 个历史留档` : ""}</span></div>
          <div className="asset-production-candidate-grid">
            {submissions.map((submission, index) => <article className={`character-pose-slot asset-candidate-card ${submission.is_primary ? "completed" : ""}`} key={submission.id}>
              <header><strong>候选 {index + 1}</strong><span>{submissionStatusLabel(submission)}</span></header>
              <button className="asset-candidate-preview" type="button" onClick={() => setPreviewSubmissionId(submission.id)}>
                <SubmissionStackCover submission={submission} />
                <span><Images aria-hidden="true" size={14} />单图查看</span>
              </button>
              <div className="character-pose-slot-meta"><span>{submission.model_name || "未记录模型"}</span><span>{formatDateTime(submission.created_at)}</span></div>
              <div className="asset-candidate-metadata">
                <input aria-label="视频编码或角度名称" disabled={!canUpload} placeholder="视频编码/角度名称，可使用中文或自定义名称" value={candidateMetadata[submission.id]?.view_label || ""} onChange={(event) => setCandidateMetadata((current) => ({ ...current, [submission.id]: { ...(current[submission.id] || { view_label: "", state_label: "", description: "" }), view_label: event.target.value } }))} />
                <input aria-label="图片状态" disabled={!canUpload} placeholder="状态，如日景、损坏、使用中" value={candidateMetadata[submission.id]?.state_label || ""} onChange={(event) => setCandidateMetadata((current) => ({ ...current, [submission.id]: { ...(current[submission.id] || { view_label: "", state_label: "", description: "" }), state_label: event.target.value } }))} />
                <textarea aria-label="人工说明" disabled={!canUpload} placeholder="补充人工说明" value={candidateMetadata[submission.id]?.description || ""} onChange={(event) => setCandidateMetadata((current) => ({ ...current, [submission.id]: { ...(current[submission.id] || { view_label: "", state_label: "", description: "" }), description: event.target.value } }))} />
                <div><button className="btn" disabled={!canUpload || savingCandidateId === submission.id} type="button" onClick={() => void saveCandidateMetadata(submission)}>保存说明</button><button className="btn" disabled={!submission.file_id} type="button" onClick={() => void downloadSubmissionFile(submission)}>下载</button></div>
              </div>
            </article>)}
            {canUpload ? <CandidateUploader
              accept=".jpg,.jpeg,.png"
              label="上传候选图片"
              modelName={modelName}
              onUploadCandidates={onUploadCandidates}
              prompt={humanTemporary ? "" : draftPrompt}
              task={task}
              variant="asset"
            /> : null}
            {!submissions.length && !canUpload ? <div className="asset-candidate-empty">暂无可查看的候选图片</div> : null}
          </div>
        </section>
      </div>
      {rejectedHistory.length ? <RejectedSubmissionHistory onDeleteDraftSubmission={onDeleteDraftSubmission} submissions={rejectedHistory} task={task} /> : null}
      {previewSubmissionId ? <SubmissionCarousel
        canReview={false}
        initialSubmissionId={previewSubmissionId}
        onClose={() => setPreviewSubmissionId(null)}
        onDeleteDraftSubmission={onDeleteDraftSubmission}
        submissions={submissions}
        task={task}
      /> : null}
      {locked ? <div className="notice asset-production-notice">等待前置资产：{displayDependencyAssetCodes(task.dependency_asset_codes) || "前置任务"}</div> : null}
      <footer className="character-unit-submit"><span>{draftSubmissions.length ? `已准备 ${draftSubmissions.length} 个候选成果` : waitingReview ? "候选成果正在导演审核" : task.status === "completed" ? "任务已完成，候选成果长期保留" : "至少上传一个候选后才能提交"}</span><button className="btn primary" disabled={!canUpload || loading || !draftSubmissions.length} type="button" onClick={() => onSubmitTask(task)}>提交任务（{draftSubmissions.length}）</button></footer>
    </section>
  </div>;
}

async function downloadSubmissionFile(submission: Submission) {
  if (!submission.file_id) return;
  await triggerBrowserDownload(submission.file_id);
}

async function triggerBrowserDownload(fileId: string, downloadName?: string) {
  const download = await api.getFileDownloadUrl(fileId, downloadName);
  const link = document.createElement("a");
  link.href = download.url;
  if (downloadName) link.download = downloadName;
  link.setAttribute("aria-hidden", "true");
  document.body.appendChild(link);
  link.click();
  link.remove();
}

function CharacterPoseModal({
  unit,
  canReview,
  loading,
  projectName,
  onClose,
  onUploadCandidates,
  onSubmitCharacterUnit,
  onReviewCharacterUnit,
  onDeleteDraftSubmission,
  onArchiveSubmission,
  onSetPrimarySubmission,
  onSavePrompt,
  onGeneratePrompt,
}: {
  unit: TaskUnit;
  canReview: boolean;
  loading: boolean;
  projectName: string;
  onClose: () => void;
  onUploadCandidates: UploadCandidatesHandler;
  onSubmitCharacterUnit: MyTasksPageProps["onSubmitCharacterUnit"];
  onReviewCharacterUnit: MyTasksPageProps["onReviewCharacterUnit"];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
  onSavePrompt: MyTasksPageProps["onSavePrompt"];
  onGeneratePrompt: MyTasksPageProps["onGeneratePrompt"];
}) {
  const tasks = unit.tasks;
  const [activeTaskId, setActiveTaskId] = React.useState(tasks[0].id);
  const [promptDrafts, setPromptDrafts] = React.useState<Record<string, string>>(() => Object.fromEntries(
    tasks.map((task) => [task.id, task.latest_prompt_text || task.prompt_text || ""]),
  ));
  const [selectedByTask, setSelectedByTask] = React.useState<Record<string, string[]>>({});
  const [primaryByTask, setPrimaryByTask] = React.useState<Record<string, string | null>>({});
  const [reviewComment, setReviewComment] = React.useState("");
  const activeTask = tasks.find((task) => task.id === activeTaskId) || tasks[0];
  const humanTemporary = activeTask.variant_kind === "human_temporary";
  const unitLocked = tasks.some((task) => task.locked);
  const canEditActivePrompt = !canReview && !activeTask.locked && !["completed", "reviewing", "submitted"].includes(activeTask.status);
  const reviewEntries = tasks.map((task) => {
    const batch = latestBatch(task.submission_batches || [], "submitted");
    return batch ? {
      task,
      batchId: batch.id,
      primarySubmissionId: primaryByTask[task.id] || null,
      selectedSubmissionIds: selectedByTask[task.id] || [],
    } : null;
  }).filter(Boolean) as Array<{ task: Task; batchId: string; primarySubmissionId: string | null; selectedSubmissionIds: string[] }>;
  const pendingTasks = tasks.filter((task) => !task.locked && !["completed", "reviewing", "submitted"].includes(task.status));
  const reviewableTasks = tasks.filter((task) => ["reviewing", "submitted"].includes(task.status) && !task.locked);
  const allReviewBatchesReady = reviewEntries.length > 0 && reviewEntries.length === reviewableTasks.length;
  const allPendingHaveDrafts = pendingTasks.length > 0 && pendingTasks.every((task) => (
    task.submissions || []
  ).some((item) => item.status === "draft" && isActiveSubmission(item)));
  const canApprove = allReviewBatchesReady && reviewEntries.every((entry) => {
    return characterReviewSelectionReady(
      entry.task.task_variant,
      entry.selectedSubmissionIds,
      entry.primarySubmissionId,
    );
  });
  const promptVersionSignature = tasks.map((task) => `${task.id}:${task.latest_prompt_text || task.prompt_text || ""}`).join("|");
  const completedSlotCount = tasks.filter((task) => task.status === "completed").length;
  const availableSlotCount = tasks.filter((task) => !task.locked && !["completed", "reviewing", "submitted"].includes(task.status)).length;

  React.useEffect(() => {
    setPromptDrafts((current) => Object.fromEntries(tasks.map((task) => [
      task.id,
      task.latest_prompt_text || task.prompt_text || current[task.id] || "",
    ])));
  }, [promptVersionSignature]);

  function chooseReviewSubmission(taskId: string, submissionId: string, selected: boolean) {
    setSelectedByTask((current) => ({
      ...current,
      [taskId]: selected
        ? Array.from(new Set([...(current[taskId] || []), submissionId]))
        : (current[taskId] || []).filter((item) => item !== submissionId),
    }));
    if (!selected && primaryByTask[taskId] === submissionId) {
      setPrimaryByTask((current) => ({ ...current, [taskId]: null }));
    }
  }

  function chooseReviewPrimary(taskId: string, submissionId: string) {
    if (primaryByTask[taskId] === submissionId) {
      setPrimaryByTask((current) => ({ ...current, [taskId]: null }));
      return;
    }
    setPrimaryByTask((current) => ({ ...current, [taskId]: submissionId }));
    setSelectedByTask((current) => ({
      ...current,
      [taskId]: Array.from(new Set([...(current[taskId] || []), submissionId])),
    }));
  }

  return <div className="modal-backdrop" role="presentation" onClick={onClose}>
    <section className="character-pose-modal" role="dialog" aria-modal="true" aria-label="人物定装生产" onClick={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><h3>{assetCodeDisplay(unit.asset?.asset_code, "人物")} {unit.asset?.name || "人物定装"}</h3><p>{projectName} · {characterContextLabel(tasks[0])} · A-E 图位独立生成与提交</p></div><div className="character-unit-head-meta"><span className="badge mint">已定版 {completedSlotCount}/{tasks.length}</span><span className="badge sky">可生成 {availableSlotCount}</span><button className="btn" type="button" onClick={onClose}>关闭</button></div></div>
      <div className="character-pose-tabs">
        {tasks.map((task) => <button className={`${task.id === activeTask.id ? "active" : ""} ${characterTaskSlotStatusClass(task)}`} key={task.id} type="button" onClick={() => setActiveTaskId(task.id)}>{task.locked ? <LockKeyhole aria-label="等待前置任务" size={14} /> : null}<strong>{task.task_variant}</strong><span>{poseTitle(task)}</span><em>{characterTaskSlotStatus(task)}</em></button>)}
      </div>
      <TaskWorkflowAlerts task={activeTask} onGeneratePrompt={onGeneratePrompt} />
      <div className="character-pose-workspace">
        <section className={`character-prompt-editor ${humanTemporary ? "human-production-requirement" : ""}`}>
          <div className="character-work-title"><strong>{humanTemporary ? "人工生产要求" : `${activeTask.task_variant} 图位提示词`}</strong><span className={`character-slot-status ${characterTaskSlotStatusClass(activeTask)}`}>{characterTaskSlotStatus(activeTask)}</span></div>
          {activeTask.locked ? <div className="character-slot-lock-notice"><LockKeyhole aria-hidden="true" size={16} /><span>{characterLockedMessage(activeTask)}{activeTask.dependency_asset_codes?.length ? ` 前置资产：${displayDependencyAssetCodes(activeTask.dependency_asset_codes)}` : ""}</span></div> : null}
          {humanTemporary ? <><p>{activeTask.variant_description_zh || "未填写人工生产要求。"}</p><span className="human-production-note">本任务跳过提示词 Agent，由制作员按人工要求直接生产。</span></> : <><textarea disabled={!canEditActivePrompt} value={promptDrafts[activeTask.id] || ""} onChange={(event) => setPromptDrafts((current) => ({ ...current, [activeTask.id]: event.target.value }))} />
          <div className="character-prompt-actions">
            <button className="btn" type="button" onClick={() => void copyText(promptDrafts[activeTask.id] || "")}>仅复制</button>
            <button className="btn" disabled={!canEditActivePrompt} type="button" onClick={async () => {
              const prompt = promptDrafts[activeTask.id] || "";
              await copyText(prompt);
              await onSavePrompt(activeTask, prompt, true);
            }}>复制并保存版本</button>
            <button className="btn" disabled={!canEditActivePrompt} type="button" onClick={() => onSavePrompt(activeTask, promptDrafts[activeTask.id] || "", false)}>保存提示词</button>
            <button className="btn" disabled={!canEditActivePrompt || loading} type="button" onClick={() => onGeneratePrompt(activeTask)}>Agent生成</button>
          </div>
          </>}
        </section>
        <section className="character-pose-upload-area">
          {tasks.map((task) => {
            const allSubmissions = task.submissions || [];
            const submissions = currentBatchSubmissions(allSubmissions, task.submission_batches || [], "result");
            const rejectedHistory = rejectedSubmissionHistory(allSubmissions, "result");
            const batch = latestBatch(task.submission_batches || [], "submitted");
            const reviewCandidates = batch ? submissions.filter((item) => item.batch_id === batch.id) : [];
             const canUploadPose = !canReview && !task.locked && !["completed", "reviewing", "submitted"].includes(task.status);
            const stackSubmissions = submissions;
            return <article className={`character-pose-slot ${task.status === "completed" ? "completed" : ""} ${task.locked ? "locked" : ""}`} key={task.id}>
              <header>{task.locked ? <LockKeyhole aria-label="等待前置任务" size={14} /> : null}<strong>{task.task_variant}</strong><span>{poseTitle(task)}</span><em className={characterTaskSlotStatusClass(task)}>{characterTaskSlotStatus(task)}</em></header>
              {canReview && reviewCandidates.length ? <div className="pose-review-candidates">{reviewCandidates.map((submission) => <div className="pose-review-candidate" key={submission.id}>
                <MediaPreview submission={submission} />
                <div className="review-choice-row"><button className={`review-choice ${selectedByTask[task.id]?.includes(submission.id) ? "selected" : ""}`} aria-pressed={selectedByTask[task.id]?.includes(submission.id)} type="button" onClick={() => chooseReviewSubmission(task.id, submission.id, !selectedByTask[task.id]?.includes(submission.id))}>定版</button>{characterTaskRequiresPrimary(task, unit) ? <button className={`review-choice primary ${primaryByTask[task.id] === submission.id ? "selected" : ""}`} aria-pressed={primaryByTask[task.id] === submission.id} type="button" onClick={() => chooseReviewPrimary(task.id, submission.id)}>身份主母版</button> : null}</div>
              </div>)}</div> : task.locked ? <div className="pose-slot-locked"><LockKeyhole aria-hidden="true" size={24} /><strong>{hasInvalidatedSubmissions(task.submissions || []) ? "待重新生产" : "等待 A 图定版"}</strong><span>{hasInvalidatedSubmissions(task.submissions || []) ? "旧成果基于上一版 A，已失效；新 A 通过后重新解锁" : "B-E 图位将在 A 完成后独立解锁"}</span></div> : stackSubmissions.length ? <SubmissionStack
                canReview={canReview}
                compact
                onArchiveSubmission={onArchiveSubmission}
                onDeleteDraftSubmission={onDeleteDraftSubmission}
                onSetPrimarySubmission={onSetPrimarySubmission}
                submissions={stackSubmissions}
                task={task}
              /> : canUploadPose ? <CandidateUploader
                accept=".jpg,.jpeg,.png"
                label={`上传 ${task.task_variant || "图位"} 候选`}
                modelName="Seedream"
                onUploadCandidates={onUploadCandidates}
                prompt={task.variant_kind === "human_temporary" ? "" : promptDrafts[task.id] || ""}
                task={task}
                variant="pose"
              /> : <div className="pose-upload-empty"><Images aria-hidden="true" size={24} /><span>暂无候选</span></div>}
              {canUploadPose && stackSubmissions.length ? <CandidateUploader
                accept=".jpg,.jpeg,.png"
                compact
                label="继续上传"
                modelName="Seedream"
                onUploadCandidates={onUploadCandidates}
                prompt={task.variant_kind === "human_temporary" ? "" : promptDrafts[task.id] || ""}
                task={task}
                variant="pose"
              /> : null}
              <div className="character-pose-slot-meta"><span>{submissions.length} 个可用候选{allSubmissions.length > submissions.length ? ` · ${allSubmissions.length - submissions.length} 个历史留档` : ""}</span><span>{characterTaskSlotStatus(task)}</span></div>
              {!canReview && rejectedHistory.length ? <RejectedSubmissionHistory onDeleteDraftSubmission={onDeleteDraftSubmission} submissions={rejectedHistory} task={task} /> : null}
            </article>;
          })}
        </section>
      </div>
      {unitLocked ? <div className="notice">{tasks.some((task) => task.locked && hasInvalidatedSubmissions(task.submissions || [])) ? "B-E 旧成果已失效，等待新 A 通过后重新生产" : `等待前置资产：${displayDependencyAssetCodes(Array.from(new Set(tasks.flatMap((task) => task.dependency_asset_codes || [])))) || "A 图或服饰道具"}`}</div> : null}
      {canReview && reviewEntries.length ? <div className="character-unit-review-footer"><label className="field"><span>本次审核意见</span><textarea placeholder="退回时必须填写具体修订原因" value={reviewComment} onChange={(event) => setReviewComment(event.target.value)} /></label><div><button className="btn" disabled={loading || !allReviewBatchesReady || !reviewComment.trim()} onClick={() => onReviewCharacterUnit(reviewEntries, "rework", reviewComment)}>{characterReviewActionLabel(reviewEntries.map((entry) => entry.task), "退回返工")}</button><button className="btn primary" disabled={loading || !canApprove} onClick={() => onReviewCharacterUnit(reviewEntries, "approve", reviewComment)}>{characterReviewActionLabel(reviewEntries.map((entry) => entry.task), "通过并定版")}</button></div></div> : null}
      {!canReview ? <footer className="character-unit-submit"><span>{allPendingHaveDrafts ? `已准备 ${pendingTasks.length} 个可提交图位` : pendingTasks.length ? "当前可用图位至少上传一个候选后才能提交" : tasks.some((task) => task.locked && hasInvalidatedSubmissions(task.submissions || [])) ? "等待新 A 通过后重新生产 B-E" : "等待 A 图定版后解锁下一批图位"}</span><button className="btn primary" disabled={loading || !pendingTasks.length || !allPendingHaveDrafts} onClick={() => onSubmitCharacterUnit(pendingTasks)}>提交当前图位</button></footer> : null}
    </section>
  </div>;
}

function characterContextLabel(task: Task) {
  const age = task.age_stage_code || "基础身份";
  const costume = task.costume_variant_code || "基础定装";
  return `${age} / ${costume}`;
}

type CandidateUploaderProps = {
  accept: string;
  compact?: boolean;
  emptyPreview?: boolean;
  label: string;
  mediaHint?: string;
  modelName: string;
  onUploadCandidates: UploadCandidatesHandler;
  prompt: string;
  step?: Step;
  task: Task;
  variant: "large" | "asset" | "pose";
};

type CandidateUploadItem = TaskCandidateUploadUpdate & { id: string };

function SubmissionRemoveButton({
  submission,
  onRequest,
  disabled = false,
}: {
  submission: Submission;
  onRequest: () => void;
  disabled?: boolean;
}) {
  if (!isMakerRemovableSubmission(submission)) return null;
  return <button className="review-viewer-delete" aria-label="移除当前图片" disabled={disabled} title={submission.status === "rejected" ? "移除已驳回图片" : "移除当前草稿"} type="button" onClick={onRequest}><Trash2 aria-hidden="true" size={15} /></button>;
}

function CandidateUploader({
  accept,
  compact = false,
  emptyPreview = false,
  label,
  mediaHint,
  modelName,
  onUploadCandidates,
  prompt,
  step,
  task,
  variant,
}: CandidateUploaderProps) {
  const [items, setItems] = React.useState<CandidateUploadItem[]>([]);
  const sequence = React.useRef(0);
  const busy = items.some((item) => ["queued", "uploading", "registering"].includes(item.status));

  function updateItem(update: TaskCandidateUploadUpdate) {
    setItems((current) => current.map((item) => item.file === update.file ? { ...item, ...update } : item));
  }

  async function upload(files: File[], resumeUpload?: FileUploadResponse) {
    await onUploadCandidates(task, prompt, modelName, [modelName], files, step, updateItem, resumeUpload);
  }

  function selectFiles(files: File[]) {
    if (!files.length) return;
    const queued = files.map((file) => ({
      file,
      id: `${task.id}-${Date.now()}-${sequence.current++}`,
      progress: 0,
      status: "queued" as const,
    }));
    setItems((current) => [...current.filter((item) => item.status !== "success"), ...queued]);
    void upload(files);
  }

  function retry(item: CandidateUploadItem) {
    setItems((current) => current.map((candidate) => candidate.id === item.id
      ? { ...candidate, error: undefined, progress: 0, status: "queued" }
      : candidate));
    void upload([item.file], item.uploaded);
  }

  const input = <input
    accept={accept}
    disabled={busy}
    multiple
    type="file"
    onChange={(event) => {
      const files = Array.from(event.currentTarget.files || []) as File[];
      event.currentTarget.value = "";
      selectFiles(files);
    }}
  />;
  const trigger = variant === "large"
    ? <label className={`task-large-upload-zone ${emptyPreview ? "empty-preview" : ""} ${busy ? "busy" : ""}`}>
        <Upload aria-hidden="true" size={26} />
        <strong>{busy ? "候选文件上传中" : label}</strong>
        <span>{mediaHint || "支持一次选择多个文件"}</span>
        {input}
      </label>
    : variant === "asset"
      ? <label className={`character-pose-slot asset-candidate-upload ${busy ? "busy" : ""}`}>
          <header><strong>添加</strong><span>{busy ? "上传中" : "支持多选"}</span></header>
          <div className="asset-candidate-upload-empty"><Upload aria-hidden="true" size={22} /><strong>{label}</strong><span>选择后自动上传</span></div>
          {input}
        </label>
      : <label className={`${compact ? "pose-upload-button" : "pose-upload-empty pose-upload-trigger"} ${busy ? "busy" : ""}`}>
          <Upload aria-hidden="true" size={compact ? 15 : 24} />
          <span>{busy ? "上传中" : label}</span>
          {input}
        </label>;

  return <div className={`candidate-uploader ${variant} ${compact ? "compact" : ""}`}>
    {trigger}
    {items.length ? <div className="candidate-upload-queue" aria-label="候选文件上传队列">
      {items.map((item) => <div className={`candidate-upload-item ${item.status}`} key={item.id}>
        <span className="candidate-upload-status" aria-hidden="true">
          {item.status === "success" ? <CheckCircle2 size={15} />
            : item.status === "failed" ? <AlertTriangle size={15} />
              : <Upload size={15} />}
        </span>
        <span className="candidate-upload-file"><strong title={item.file.name}>{item.file.name}</strong><small>{uploadStatusLabel(item)} · {formatFileSize(item.file.size)}</small></span>
        <span className="candidate-upload-progress" aria-label={`上传进度 ${item.progress}%`}>
          <i style={{ width: `${item.progress}%` }} />
        </span>
        {item.status === "failed" ? <button aria-label={`重试 ${item.file.name}`} disabled={busy} title="重试" type="button" onClick={() => retry(item)}><RotateCw size={14} /></button>
          : item.status === "success" ? <button aria-label={`收起 ${item.file.name} 上传记录`} title="收起上传记录" type="button" onClick={() => setItems((current) => current.filter((candidate) => candidate.id !== item.id))}><X size={14} /></button>
          : !["queued", "uploading", "registering"].includes(item.status) ? <button aria-label={`移除 ${item.file.name}`} title="移除记录" type="button" onClick={() => setItems((current) => current.filter((candidate) => candidate.id !== item.id))}><X size={14} /></button>
            : <span className="candidate-upload-percent">{item.progress}%</span>}
        {item.error ? <small className="candidate-upload-error" title={item.error}>{item.error}</small> : null}
      </div>)}
    </div> : null}
  </div>;
}

function uploadStatusLabel(item: CandidateUploadItem) {
  return ({
    queued: "等待上传",
    uploading: `上传 ${item.progress}%`,
    registering: "正在登记候选",
    success: "上传成功",
    failed: "上传失败",
  } as const)[item.status];
}

function formatFileSize(size: number) {
  if (size < 1024 * 1024) return `${Math.max(1, Math.round(size / 1024))} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

function TaskWorkflowAlerts({
  task,
  onGeneratePrompt,
  step,
}: {
  task: Task;
  onGeneratePrompt: MyTasksPageProps["onGeneratePrompt"];
  step?: Step;
}) {
  const [contextDismissed, setContextDismissed] = React.useState(false);
  const [regenerating, setRegenerating] = React.useState(false);
  const reworkBatch = (task.submission_batches || [])
    .filter((batch) => batch.status === "rework")
    .sort((left, right) => right.version_no - left.version_no)[0];

  React.useEffect(() => {
    setContextDismissed(false);
  }, [task.asset_context_outdated, task.id]);

  async function regenerate() {
    setRegenerating(true);
    try {
      const applied = await onGeneratePrompt(task, step);
      if (applied) setContextDismissed(true);
    } finally {
      setRegenerating(false);
    }
  }

  const showRework = reworkBatch && !["completed", "reviewing", "submitted"].includes(task.status);
  const showContext = task.asset_context_outdated && !contextDismissed && task.status !== "completed";
  const canRegenerate = !task.locked && !["completed", "reviewing", "submitted"].includes(task.status);
  if (!showRework && !showContext) return <section className="task-workflow-alerts empty" />;
  return <section className="task-workflow-alerts" aria-label="任务处理提醒">
    {showRework ? <div className="task-rework-alert">
      <AlertTriangle aria-hidden="true" size={19} />
      <div><strong>第 {reworkBatch.version_no} 批成果已退回</strong><p>{reworkBatch.review_comment || "请根据导演反馈修改候选成果后重新提交。"}</p><span>{reworkBatch.reviewed_at ? `退回时间 ${formatDateTime(reworkBatch.reviewed_at)}` : "等待返工处理"}</span></div>
    </div> : null}
    {showContext ? <div className="task-context-alert">
      <RefreshCw aria-hidden="true" size={19} />
      <div><strong>参考主母版已更新</strong><p>当前提示词基于旧版资产上下文，请确认是否按最新主母版重新生成。</p></div>
      <div className="task-context-actions"><button className="btn" disabled={regenerating || !canRegenerate} title={canRegenerate ? "按最新资产上下文重新生成" : "任务当前状态不允许修改提示词"} type="button" onClick={() => void regenerate()}>{regenerating ? "生成中" : "重新生成提示词"}</button><button className="btn" disabled={regenerating} title="仅关闭本次提醒，不修改任务或主母版" type="button" onClick={() => setContextDismissed(true)}>继续当前版本</button></div>
    </div> : null}
  </section>;
}

function poseTitle(task: Task) {
  const title = task.title || "";
  const match = title.match(/\[[A-E]\]\s*(.+)$/);
  return match?.[1] || ({ A: "正面全身", B: "半侧全身", C: "背面全身", D: "面部表情", E: "服装细节" } as Record<string, string>)[task.task_variant || ""] || "定装图位";
}

function characterTaskSlotStatus(task: Task) {
  if (task.locked && hasInvalidatedSubmissions(task.submissions || [])) return "待重新生产";
  if (task.status === "completed") return "已定版";
  if (task.locked) return "等待 A 定版";
  if (["reviewing", "submitted"].includes(task.status)) return "待审核";
  if (taskHasRework(task)) return taskHasActiveDraft(task) ? "返工中" : "需返工";
  return "可独立生成";
}

function characterTaskSlotStatusClass(task: Task) {
  if (task.locked && hasInvalidatedSubmissions(task.submissions || [])) return "locked";
  if (task.status === "completed") return "completed";
  if (task.locked) return "locked";
  if (["reviewing", "submitted"].includes(task.status)) return "reviewing";
  if (taskHasRework(task)) return "rejected";
  return "available";
}

function taskHasRework(task: Task) {
  return task.status === "rejected" || (task.submission_batches || []).some((batch) => batch.status === "rework");
}

function taskHasActiveDraft(task: Task) {
  return (task.submissions || []).some((submission) => submission.status === "draft" && isActiveSubmission(submission));
}

function characterLockedMessage(task: Task) {
  return hasInvalidatedSubmissions(task.submissions || [])
    ? "旧成果基于上一版 A，现已失效；等待新 A 通过后重新生产。"
    : "等待 A 图定版后解锁。";
}

export function queueLabel(queue: QueueKey) {
  return QUEUES.find((item) => item.key === queue)?.label || queue;
}

function taskQueue(task: Task): QueueKey {
  if (task.locked && task.status !== "completed") return "waiting";
  if (task.status === "completed") return "completed";
  if (["reviewing", "submitted"].includes(task.status)) return "reviewing";
  if (task.status === "rejected") return "working";
  return "working";
}

function DependencyAssetsPanel({ task }: { task: Task }) {
  const assets = task.dependency_assets || [];
  const summary = task.dependency_summary;
  const [downloadError, setDownloadError] = React.useState("");
  if (!assets.length && !task.locked) return null;
  return <details className={`task-dependency-panel ${task.locked ? "blocked" : "ready"}`} open={Boolean(task.locked)}>
    <summary>
      <span><LockKeyhole aria-hidden="true" size={16} /><strong>前置资产</strong></span>
      <em>{summary?.required ? `${summary.ready}/${summary.required} 已就绪` : task.locked ? "等待前置资产" : "已就绪"}</em>
    </summary>
    {assets.length ? <div className="task-dependency-list">
      {assets.map((asset) => <article className={asset.ready ? "ready" : "pending"} key={`${asset.asset_id || asset.asset_code}:${asset.asset_code}`}>
        <span className="task-dependency-state">{asset.ready ? <CheckCircle2 aria-hidden="true" size={17} /> : <AlertTriangle aria-hidden="true" size={17} />}</span>
        <div><strong>{dependencyAssetLabel(asset)}</strong><span>{dependencyAssetTypeLabel(asset.asset_type)} · {dependencyReasonLabel(asset)}</span></div>
        {asset.files.length ? <div className="task-dependency-downloads">
          {asset.files.map((file, index) => {
            const downloadName = dependencyDownloadName(asset, file);
            return <button className="task-dependency-download" key={file.file_id} title={`浏览器下载 ${downloadName}`} type="button" onClick={async (event) => {
            event.preventDefault();
            setDownloadError("");
            try {
              await triggerBrowserDownload(file.file_id, downloadName);
            } catch (error) {
              setDownloadError(error instanceof Error ? error.message : "前置资产下载失败");
            }
          }}><Download aria-hidden="true" size={14} /><span>{file.view_label || `文件 ${index + 1}`}</span></button>;
          })}
        </div> : <span className="task-dependency-no-file">无可下载文件</span>}
      </article>)}
    </div> : <p className="task-dependency-fallback">{displayDependencyAssetCodes(task.dependency_asset_codes) || "前置任务尚未完成"}</p>}
    {downloadError ? <p className="task-dependency-error">{downloadError}</p> : null}
  </details>;
}

function StoryboardDialogueReference({ task, compact = false }: { task: Task; compact?: boolean }) {
  if (!task.storyboard_id) return null;
  const mirrorRows = (task.storyboard_mirror_shots || []).filter((shot) => (
    String(shot.dialogue || "").trim() || String(shot.dialogue_back_translation || "").trim()
  ));
  const rows = mirrorRows.length ? mirrorRows.map((shot, index) => ({
    code: shot.id || shot.label || String.fromCharCode(65 + index),
    thai: shot.dialogue,
    chinese: shot.dialogue_back_translation,
  })) : task.storyboard_dialogue || task.storyboard_dialogue_back_translation ? [{
    code: "父分镜",
    thai: task.storyboard_dialogue,
    chinese: task.storyboard_dialogue_back_translation,
  }] : [];
  if (!rows.length) return null;
  return <section className={`task-dialogue-reference ${compact ? "compact" : ""}`} aria-label="中泰台词参考">
    <header><strong>中泰台词</strong><span>{task.storyboard_code || "当前分镜"}</span></header>
    <div>{rows.map((row, index) => <article key={`${row.code}-${index}`}>
      <strong>{row.code}</strong>
      <div><span>泰语</span><p lang="th">{row.thai || "无"}</p></div>
      <div><span>中文回译</span><p>{row.chinese || "暂无回译"}</p></div>
    </article>)}</div>
  </section>;
}

export function TaskWorkItem({
  task,
  canReview,
  loading,
  projectName,
  onUploadCandidates,
  onSubmitTask,
  onDeleteDraftSubmission,
  onReviewTask,
  onArchiveSubmission,
  onSetPrimarySubmission,
  onSavePrompt,
  onGeneratePrompt,
}: {
  task: Task;
  canReview: boolean;
  loading: boolean;
  projectName?: string;
  onUploadCandidates: UploadCandidatesHandler;
  onSubmitTask: (task: Task, step?: Step) => Promise<void>;
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onReviewTask: MyTasksPageProps["onReviewTask"];
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
  onSavePrompt: MyTasksPageProps["onSavePrompt"];
  onGeneratePrompt: MyTasksPageProps["onGeneratePrompt"];
}) {
  const isStoryboard = task.task_type === "storyboard_shot";
  const humanTemporary = task.variant_kind === "human_temporary";
  const submittedBatch = latestBatch(task.submission_batches || [], "submitted");
  const initialStep: Step = submittedBatch?.step === "video" || (!submittedBatch && isStoryboard && task.keyframe_done) ? "video" : "keyframe";
  const initialMediaKind = taskMediaKind(task, initialStep);
  const initialBasePrompt = task.latest_prompt_text || task.prompt_text || "";
  const initialModelName = task.production_model || (initialMediaKind === "audio" ? "音频制作" : initialStep === "video" ? "Seedance" : "Seedream");
  const initialPromptOptions = initialMediaKind === "audio" ? [] : platformPromptVersions(initialBasePrompt, initialStep === "video" ? "image_to_video" : "text_to_image");
  const initialPlatformPrompt = initialPromptOptions.find((item) => item.platform === initialModelName) || initialPromptOptions[0];
  const [step, setStep] = React.useState<Step>(initialStep);
  const [draftPrompt, setDraftPrompt] = React.useState(initialPlatformPrompt?.prompt || initialBasePrompt);
  const [modelName, setModelName] = React.useState(initialPlatformPrompt?.platform || initialModelName);
  const [selectedIds, setSelectedIds] = React.useState<string[]>([]);
  const [primaryId, setPrimaryId] = React.useState<string | null>(null);
  const [reviewComment, setReviewComment] = React.useState("");
  const currentStep: Step | undefined = isStoryboard ? step : undefined;
  const normalizedStep = isStoryboard ? step : submissionStep(task);
  const relevantSubmissions = (task.submissions || []).filter((item) => submissionStepValue(item) === normalizedStep);
  const currentSubmissions = currentBatchSubmissions(relevantSubmissions, task.submission_batches || [], normalizedStep);
  const rejectedHistory = rejectedSubmissionHistory(relevantSubmissions, normalizedStep);
  const reviewBatch = submittedBatch && submittedBatch.step === normalizedStep ? submittedBatch : undefined;
  const reviewSubmissions = reviewBatch ? currentSubmissions.filter((item) => item.batch_id === reviewBatch.id) : [];
  const draftSubmissions = currentSubmissions.filter((item) => item.status === "draft");
  const locked = Boolean(task.locked);
  const waitingReview = ["reviewing", "submitted"].includes(task.status);
  const mediaKind = taskMediaKind(task, normalizedStep);
  const isVideo = mediaKind === "video";
  const isAudio = mediaKind === "audio";
  const canUpload = !canReview && !locked && !waitingReview && task.status !== "completed";
  const platformOptions = React.useMemo(() => (
    isAudio ? [] : platformPromptVersions(task.latest_prompt_text || task.prompt_text || "", isVideo ? "image_to_video" : "text_to_image")
  ), [isAudio, isVideo, task.latest_prompt_text, task.prompt_text]);

  React.useEffect(() => {
    const basePrompt = task.latest_prompt_text || task.prompt_text || "";
    if (isAudio) {
      setModelName(task.production_model || "音频制作");
      setDraftPrompt(basePrompt);
      return;
    }
    const preferredModel = task.production_model && platformOptions.some((item) => item.platform === task.production_model)
      ? task.production_model
      : platformOptions[0]?.platform || (isVideo ? "Seedance" : "Seedream");
    setModelName(preferredModel);
    setDraftPrompt(platformOptions.find((item) => item.platform === preferredModel)?.prompt || basePrompt);
  }, [isAudio, isVideo, platformOptions, task.latest_prompt_text, task.production_model, task.prompt_text]);

  React.useEffect(() => {
    if (!reviewBatch) {
      setSelectedIds([]);
      setPrimaryId(null);
      return;
    }
    setSelectedIds([]);
    setPrimaryId(null);
  }, [reviewBatch?.id]);

  function toggleSelected(id: string, selected: boolean) {
    setSelectedIds((current) => selected ? Array.from(new Set([...current, id])) : current.filter((item) => item !== id));
    if (!selected && primaryId === id) setPrimaryId(null);
  }

  function choosePrimary(id: string) {
    if (primaryId === id) {
      setPrimaryId(null);
      return;
    }
    setPrimaryId(id);
    setSelectedIds((current) => Array.from(new Set([...current, id])));
  }

  return <article className="task-work-item">
    <header className="task-work-item-head">
      <div><strong>{task.title}</strong><span>{projectName ? `${projectName} / ` : ""}{task.assignee_name || "未分配"}{task.scene_name ? ` / ${task.scene_name}` : ""}</span></div>
      <span className={`badge ${task.status === "completed" ? "mint" : task.status === "reviewing" ? "sky" : "sun"}`}>{taskStatusLabel(task)}</span>
    </header>

    <TaskWorkflowAlerts task={task} step={currentStep} onGeneratePrompt={onGeneratePrompt} />

    <DependencyAssetsPanel task={task} />

    <StoryboardDialogueReference task={task} />

    {canReview || humanTemporary ? <details className="task-requirement" open={humanTemporary || undefined}><summary>查看生产要求</summary><p>{humanTemporary ? task.variant_description_zh || "未填写人工生产要求。" : task.latest_prompt_text || task.prompt_text || "暂无生产提示词。"}</p></details> : null}

    {isStoryboard ? <div className="step-tabs">
      <button className={`step-tab ${step === "keyframe" ? "active" : ""} ${task.keyframe_done ? "done" : ""}`} onClick={() => setStep("keyframe")}>关键帧{task.keyframe_done ? " / 已定版" : ""}</button>
      <button className={`step-tab ${step === "video" ? "active" : ""} ${task.video_done ? "done" : ""}`} disabled={!task.keyframe_done && step !== "video"} onClick={() => setStep("video")}>视频{task.video_done ? " / 已定版" : ""}</button>
    </div> : null}

    {canReview && reviewBatch ? null : currentSubmissions.length || !canUpload ? <MediaGallery
      canReview={canReview}
      submissions={currentSubmissions}
      task={task}
      onArchiveSubmission={onArchiveSubmission}
      onDeleteDraftSubmission={onDeleteDraftSubmission}
      onSetPrimarySubmission={onSetPrimarySubmission}
    /> : null}

    {!canReview && canUpload ? currentSubmissions.length ? <CandidateUploader
      accept={mediaKind === "video" ? ".mp4,.mov" : mediaKind === "audio" ? ".wav,.mp3" : ".jpg,.jpeg,.png"}
      label={`继续上传${mediaKindLabel(mediaKind)}候选`}
      mediaHint={mediaKind === "video" ? "支持 MP4、MOV" : mediaKind === "audio" ? "支持 WAV、MP3" : "支持 JPG、JPEG、PNG"}
      modelName={modelName}
      onUploadCandidates={onUploadCandidates}
      prompt={draftPrompt}
      step={currentStep}
      task={task}
      variant="large"
    /> : <ReviewMediaCanvas emptyContent={<CandidateUploader
      accept={mediaKind === "video" ? ".mp4,.mov" : mediaKind === "audio" ? ".wav,.mp3" : ".jpg,.jpeg,.png"}
      emptyPreview
      label={`上传${mediaKindLabel(mediaKind)}候选`}
      mediaHint={mediaKind === "video" ? "支持 MP4、MOV" : mediaKind === "audio" ? "支持 WAV、MP3" : "支持 JPG、JPEG、PNG"}
      modelName={modelName}
      onUploadCandidates={onUploadCandidates}
      prompt={draftPrompt}
      step={currentStep}
      task={task}
      variant="large"
    />} /> : null}

    {!canReview && rejectedHistory.length ? <RejectedSubmissionHistory
      onDeleteDraftSubmission={onDeleteDraftSubmission}
      submissions={rejectedHistory}
      task={task}
    /> : null}

    {canReview && reviewBatch ? <section className="task-review-panel">
      <div className="task-review-title"><strong>审核第 {reviewBatch.version_no} 批成果</strong><span>可选择多个定版，但必须指定一个主母版</span></div>
      <div className="task-review-options">
        {reviewSubmissions.map((submission, index) => <article className="task-review-option" key={submission.id}>
          <MediaPreview submission={submission} />
          <div className="review-choice-row"><button className={`review-choice ${selectedIds.includes(submission.id) ? "selected" : ""}`} aria-pressed={selectedIds.includes(submission.id)} type="button" onClick={() => toggleSelected(submission.id, !selectedIds.includes(submission.id))}>定版</button><button className={`review-choice primary ${primaryId === submission.id ? "selected" : ""}`} aria-pressed={primaryId === submission.id} type="button" onClick={() => choosePrimary(submission.id)}>主母版</button></div>
          <small>候选 {index + 1}</small>
        </article>)}
      </div>
      <label className="field"><span>审核意见</span><textarea value={reviewComment} onChange={(event) => setReviewComment(event.target.value)} /></label>
      <div className="task-review-actions">
        <button className="btn" disabled={loading || !reviewComment.trim()} onClick={() => onReviewTask(task, reviewBatch.id, "rework", null, [], reviewComment)}>退回返工</button>
        <button className="btn primary" disabled={loading || !primaryId || !selectedIds.length} onClick={() => onReviewTask(task, reviewBatch.id, "approve", primaryId, selectedIds, reviewComment)}>审核通过并定版</button>
      </div>
    </section> : null}

    {!canReview ? <>
      {!humanTemporary ? <section className="platform-production-prompt">
        <header><span>{isAudio ? "生产提示词" : "生产平台"}</span>{!isAudio ? <div>{platformOptions.map((item) => <button className={modelName === item.platform ? "active" : ""} key={item.platform} type="button" onClick={() => { setModelName(item.platform); setDraftPrompt(item.prompt); }}>{item.platform}</button>)}</div> : null}</header>
        <textarea aria-label="生产提示词" disabled={!canUpload} value={draftPrompt} onChange={(event) => setDraftPrompt(event.target.value)} />
      </section> : <div className="human-production-note">本任务跳过提示词 Agent，直接按人工要求上传生产候选。</div>}
      <div className="task-upload-row">
        <span>{isAudio ? modelName : `当前平台：${modelName}`}</span>
        {!isAudio && !humanTemporary ? <button className="btn" disabled={!canUpload || loading} onClick={() => onGeneratePrompt(task, currentStep)}>重新生成提示词</button> : null}
        <button className="btn primary" disabled={!canUpload || loading || !draftSubmissions.length} onClick={() => onSubmitTask(task, currentStep)}>提交当前批次（{draftSubmissions.length}）</button>
      </div>
      {!humanTemporary ? <div className="task-prompt-actions"><button className="btn" onClick={() => void copyText(draftPrompt)}>复制提示词</button><button className="btn" disabled={!canUpload} onClick={async () => { await copyText(draftPrompt); await onSavePrompt(task, draftPrompt, true); }}>复制并保存版本</button><button className="btn" disabled={!canUpload} onClick={() => onSavePrompt(task, draftPrompt, false)}>仅保存版本</button></div> : null}
    </> : null}
  </article>;
}

function MediaGallery({
  task,
  submissions,
  canReview,
  onArchiveSubmission,
  onDeleteDraftSubmission,
  onSetPrimarySubmission,
}: {
  task: Task;
  submissions: Submission[];
  canReview: boolean;
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
}) {
  if (!submissions.length) return <div className="task-media-empty">尚未上传候选成果。</div>;
  return <section className="task-media-section">
    <div className="task-media-toolbar"><strong>候选成果（{submissions.length}）</strong></div>
    <SubmissionStack
      canReview={canReview}
      onArchiveSubmission={onArchiveSubmission}
      onDeleteDraftSubmission={onDeleteDraftSubmission}
      onSetPrimarySubmission={onSetPrimarySubmission}
      submissions={submissions}
      task={task}
    />
  </section>;
}

function RejectedSubmissionHistory({
  task,
  submissions,
  onDeleteDraftSubmission,
}: {
  task: Task;
  submissions: Submission[];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
}) {
  const [open, setOpen] = React.useState(false);
  if (!submissions.length) return null;
  return <section className="rejected-submission-history">
    <button type="button" onClick={() => setOpen(true)}><RotateCw aria-hidden="true" size={15} /><span>历史驳回参考</span><strong>{submissions.length}</strong></button>
    <p>仅用于返工对照，不计入当前可用候选。</p>
    {open ? <SubmissionCarousel
      canReview={false}
      initialSubmissionId={submissions[submissions.length - 1].id}
      onClose={() => setOpen(false)}
      onDeleteDraftSubmission={onDeleteDraftSubmission}
      submissions={submissions}
      task={task}
    /> : null}
  </section>;
}

function SubmissionStack({
  task,
  submissions,
  canReview,
  compact = false,
  onArchiveSubmission,
  onDeleteDraftSubmission,
  onSetPrimarySubmission,
}: {
  task: Task;
  submissions: Submission[];
  canReview: boolean;
  compact?: boolean;
  onArchiveSubmission: MyTasksPageProps["onArchiveSubmission"];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onSetPrimarySubmission: MyTasksPageProps["onSetPrimarySubmission"];
}) {
  const [open, setOpen] = React.useState(false);
  const draftSubmissions = submissions.filter((submission) => submission.status === "draft" && isActiveSubmission(submission));
  const featured = (!canReview ? draftSubmissions[draftSubmissions.length - 1] : undefined)
    || submissions.find((submission) => submission.is_primary)
    || submissions[submissions.length - 1];
  if (!featured) return null;

  return <>
    <article className={`submission-stack ${compact ? "compact" : ""}`}>
      <button
        aria-label={`查看 ${submissions.length} 个候选素材`}
        className={`submission-stack-card ${submissions.length > 1 ? "has-multiple" : ""}`}
        type="button"
        onClick={() => setOpen(true)}
      >
        <SubmissionStackCover submission={featured} />
        {featured.is_primary ? <span className="submission-primary-badge">主母版</span> : null}
        {featured.status === "draft" ? <span className="submission-draft-badge">可删除草稿</span> : null}
        <span className="submission-stack-count" title={`${submissions.length} 个候选素材`}><Images aria-hidden="true" size={14} />{submissions.length}</span>
      </button>
      <div className="submission-stack-meta"><strong>{submissionStatusLabel(featured)}</strong><span>{featured.model_name || "未记录模型"}</span></div>
    </article>
    {open ? <SubmissionCarousel
      canReview={canReview}
      initialSubmissionId={featured.id}
      onArchiveSubmission={onArchiveSubmission}
      onClose={() => setOpen(false)}
      onDeleteDraftSubmission={onDeleteDraftSubmission}
      onSetPrimarySubmission={onSetPrimarySubmission}
      submissions={submissions}
      task={task}
    /> : null}
  </>;
}

function SubmissionStackCover({ submission }: { submission: Submission }) {
  if (submissionMediaKind(submission) === "audio") {
    return <div className="submission-stack-audio"><AudioLines aria-hidden="true" size={30} /><span>音频候选</span></div>;
  }
  if (submission.file_type === "video") {
    return <div className="submission-stack-video"><Video aria-hidden="true" size={30} /><span>视频候选</span></div>;
  }
  return <MediaPreview interactive={false} submission={submission} />;
}

function SubmissionCarousel({
  task,
  submissions,
  initialSubmissionId,
  canReview,
  onClose,
  onArchiveSubmission,
  onDeleteDraftSubmission,
  onSetPrimarySubmission,
}: {
  task: Task;
  submissions: Submission[];
  initialSubmissionId: string;
  canReview: boolean;
  onClose: () => void;
  onArchiveSubmission?: MyTasksPageProps["onArchiveSubmission"];
  onDeleteDraftSubmission: MyTasksPageProps["onDeleteDraftSubmission"];
  onSetPrimarySubmission?: MyTasksPageProps["onSetPrimarySubmission"];
}) {
  const [activeId, setActiveId] = React.useState(initialSubmissionId);
  const [actionLoading, setActionLoading] = React.useState(false);
  const activeIndex = Math.max(0, submissions.findIndex((submission) => submission.id === activeId));
  const activeSubmission = submissions[activeIndex];

  React.useEffect(() => {
    if (!submissions.length) {
      onClose();
      return;
    }
    if (!submissions.some((submission) => submission.id === activeId)) {
      setActiveId(submissions[Math.min(activeIndex, submissions.length - 1)].id);
    }
  }, [activeId, activeIndex, onClose, submissions]);

  const move = React.useCallback((direction: -1 | 1) => {
    if (submissions.length < 2) return;
    const nextIndex = (activeIndex + direction + submissions.length) % submissions.length;
    setActiveId(submissions[nextIndex].id);
  }, [activeIndex, submissions]);

  React.useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable='true']")) return;
      if (event.key === "ArrowLeft") move(-1);
      if (event.key === "ArrowRight") move(1);
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [move, onClose]);

  if (!activeSubmission) return null;
  const canManage = canReview && isActiveSubmission(activeSubmission) && Boolean(onArchiveSubmission && onSetPrimarySubmission) && !["draft", "submitted", "rejected"].includes(activeSubmission.status);

  async function deleteActive() {
    setActionLoading(true);
    try {
      await onDeleteDraftSubmission(task, activeSubmission.id);
      const remaining = submissions.filter((submission) => submission.id !== activeSubmission.id);
      if (remaining.length) {
        setActiveId(remaining[Math.min(activeIndex, remaining.length - 1)].id);
      } else {
        onClose();
      }
    } finally {
      setActionLoading(false);
    }
  }

  async function manageActive(action: "primary" | "archive") {
    setActionLoading(true);
    try {
      if (action === "primary") {
        await onSetPrimarySubmission?.(task, activeSubmission.id);
      } else {
        await onArchiveSubmission?.(task, activeSubmission.id, !activeSubmission.is_archived);
      }
    } finally {
      setActionLoading(false);
    }
  }

  return <div className="modal-backdrop submission-carousel" role="presentation" onClick={onClose}>
    <section className="submission-carousel-dialog" role="dialog" aria-modal="true" aria-label="候选素材预览" onClick={(event) => event.stopPropagation()}>
      <header>
        <div><strong>候选成果</strong><span>{activeIndex + 1} / {submissions.length}</span>{activeSubmission.is_primary ? <em>主母版</em> : null}</div>
        <button className="submission-icon-button" aria-label="关闭预览" title="关闭" type="button" onClick={onClose}><X aria-hidden="true" size={20} /></button>
      </header>
      <div className="submission-carousel-viewport">
        <button className="submission-carousel-nav previous" aria-label="上一项" disabled={submissions.length < 2} title="上一项" type="button" onClick={() => move(-1)}><ChevronLeft aria-hidden="true" size={26} /></button>
        <div className="submission-carousel-media"><ReviewMediaCanvas submission={activeSubmission} onDelete={!canReview ? deleteActive : undefined} /></div>
        <button className="submission-carousel-nav next" aria-label="下一项" disabled={submissions.length < 2} title="下一项" type="button" onClick={() => move(1)}><ChevronRight aria-hidden="true" size={26} /></button>
      </div>
      <footer>
        <div className="submission-carousel-meta"><strong>{submissionStatusLabel(activeSubmission)}</strong><span>{submissionFileTypeLabel(activeSubmission)} · {activeSubmission.model_name || "未记录模型"} · {formatDateTime(activeSubmission.created_at)}</span></div>
        <div className="submission-carousel-actions">
          <button className="btn" disabled={actionLoading || !activeSubmission.file_id} type="button" onClick={() => void downloadSubmissionFile(activeSubmission)}><Download aria-hidden="true" size={16} />下载当前文件</button>
          {canManage && !activeSubmission.is_primary ? <button className="btn" disabled={actionLoading} type="button" onClick={() => void manageActive("primary")}>设为主母版</button> : null}
          {canManage && !activeSubmission.is_primary ? <button className="btn" disabled={actionLoading} type="button" onClick={() => void manageActive("archive")}>归档</button> : null}
        </div>
      </footer>
    </section>
  </div>;
}

function MediaPreview({ submission, interactive = true }: { submission: Submission; interactive?: boolean }) {
  const mediaKind = submissionMediaKind(submission);
  const isVideo = mediaKind === "video";
  const isAudio = mediaKind === "audio";
  const [requested, setRequested] = React.useState(!isVideo);
  const [lightboxOpen, setLightboxOpen] = React.useState(false);
  const [url, renewMediaUrl] = useSubmissionMediaUrl(submission, requested);
  if (!submission.file_id) return <div className="task-media-placeholder">历史文件</div>;
  if (isVideo && !requested) return <button className="task-media-placeholder task-media-load" type="button" onClick={() => setRequested(true)}>加载视频预览</button>;
  if (!url) return <div className="task-media-placeholder">加载中</div>;
  if (isVideo) return <RenewableVideo className="task-media-preview" controls preload="metadata" src={url} onPlaybackError={() => renewMediaUrl(true)} />;
  if (isAudio) return <div className="task-media-audio"><AudioLines aria-hidden="true" size={26} /><audio controls preload="metadata" src={url} onError={() => renewMediaUrl(true)} /></div>;
  const image = <img className="task-media-preview" loading="lazy" src={url} alt="候选成果" />;
  if (!interactive) return image;
  return <>
    <button className="task-media-preview-button" title="查看原图" type="button" onClick={(event) => { event.stopPropagation(); setLightboxOpen(true); }}>{image}<span>查看原图</span></button>
    {lightboxOpen ? <div className="modal-backdrop media-lightbox" role="presentation" onClick={(event) => { event.stopPropagation(); setLightboxOpen(false); }}>
      <section className="media-lightbox-dialog" role="dialog" aria-modal="true" aria-label="候选成果原图" onClick={(event) => event.stopPropagation()}>
        <header><strong>候选成果原图</strong><button className="media-lightbox-close" aria-label="关闭原图" title="关闭" type="button" onClick={() => setLightboxOpen(false)}>×</button></header>
        <ReviewMediaCanvas submission={submission} />
      </section>
    </div> : null}
  </>;
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch {
      // Intranet HTTP deployments can block the Clipboard API.
    }
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand("copy");
  textarea.remove();
}

function latestBatch(batches: SubmissionBatch[], status: string) {
  return batches.filter((item) => item.status === status).sort((left, right) => right.version_no - left.version_no)[0];
}

function submissionStep(task: Task) {
  if (taskMediaKind(task) === "audio") return "audio";
  if (task.task_type === "text_to_image" && task.storyboard_id) return "keyframe";
  if (["image_to_video", "video_generation"].includes(task.task_type) && task.storyboard_id) return "video";
  return "result";
}

function submissionStepValue(submission: Submission) {
  return submission.step || "result";
}

function taskStatusLabel(task: Task) {
  if (task.locked && task.status !== "completed") return "待前置";
  if (!["completed", "reviewing", "submitted"].includes(task.status) && taskHasRework(task)) {
    return taskHasActiveDraft(task) ? "返工中" : "需返工";
  }
  return ({
    todo: "制作中", in_progress: "制作中", submitted: "待审核", reviewing: "待审核",
    completed: "已完成", rejected: "需返工", overdue: "已逾期",
  } as Record<string, string>)[task.status] || task.status;
}

function submissionStatusLabel(submission: Submission) {
  if (submission.is_invalidated) return "已失效 · 历史留档";
  if (submission.is_archived) return submission.status === "rejected" ? "已驳回 · 制作员已移除" : "已移除留档";
  return ({
    draft: "上传草稿", submitted: "待审核", primary_master: "主母版",
    alternate_master: "备选母版", not_selected: "未选中", rejected: "已驳回",
  } as Record<string, string>)[submission.status] || submission.status;
}

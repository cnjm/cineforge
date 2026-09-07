import React from "react";
import { Panel } from "../../shared/components";
import { OutlineSelect } from "../../shared/OutlineSelect";
import { memberAvatarUrl } from "../../shared/memberAvatars";
import type { Asset, AssetVariantPlan, PageKey, SceneGating, Task, User } from "../../types";
import { assetCodeDisplay, userDisplayName, userInitial } from "../../utils";

type TaskAssignPageProps = {
  users: User[];
  tasks: Task[];
  assets: Asset[];
  assetVariantPlans: AssetVariantPlan[];
  projectId: string;
  episodeId?: string;
  sceneGating: SceneGating[];
  projectStatus?: string;
  onAssignTask: (task: Task, assigneeId: string, reassignmentReason?: string) => Promise<void>;
  onBulkAssign: (scope: string, key: string, assigneeId: string, reassignmentReason?: string) => Promise<void>;
  onCreateAssetVariantPlan: (projectId: string, payload: { asset_id: string; episode_id?: string | null; variant_kind: string; title_zh: string; description_zh: string; storyboard_ids: string[]; source?: string; confidence?: number | null }) => Promise<void>;
  onUpdateAssetVariantPlan: (planId: string, payload: Partial<Pick<AssetVariantPlan, "title_zh" | "description_zh" | "status" | "storyboard_ids">>) => Promise<void>;
  jump: (page: PageKey) => void;
  embedded?: boolean;
  readOnly?: boolean;
};

const ASSET_TYPE_LABELS: Record<string, string> = {
  character: "人物",
  scene: "场景",
  prop: "道具",
  music: "音乐",
  voice_profile: "角色音色",
};

const ASSET_TYPES = ["character", "scene", "prop", "music"] as const;
type AssetTypeKey = typeof ASSET_TYPES[number];

const ASSET_PRIORITIES = ["S", "A", "B", "C"] as const;
type AssetPriority = typeof ASSET_PRIORITIES[number];

type AssetTaskGroup = {
  key: string;
  asset: Asset;
  priority: AssetPriority;
  ageStageCode: string;
  costumeVariantCode: string;
  contextLabel: string;
  tasks: Task[];
  viewCodes: string[];
  dependencyCodes: string[];
  assigneeId: string;
  locked: boolean;
};

type PendingReassignment = {
  title: string;
  previousNames: string;
  nextName: string;
  taskCount: number;
  execute: (reason: string) => Promise<void>;
};

function AssigneeSelect({
  value,
  users,
  disabled,
  onChange,
}: {
  value: string;
  users: User[];
  disabled?: boolean;
  onChange: (id: string) => void;
}) {
  return (
    <OutlineSelect
      value={value}
      disabled={disabled}
      ariaLabel="选择负责人"
      className="outline-select--assignee"
      options={[
        { value: "", label: "未分配" },
        ...users.map((user) => ({
          value: user.id,
          label: userDisplayName(user),
          avatarText: userInitial(user),
          avatarUrl: memberAvatarUrl(user, users),
        })),
      ]}
      onChange={onChange}
    />
  );
}

export function TaskAssignPage({
  users,
  tasks,
  assets,
  assetVariantPlans,
  projectId,
  episodeId,
  sceneGating,
  projectStatus,
  onAssignTask,
  onBulkAssign,
  onCreateAssetVariantPlan,
  onUpdateAssetVariantPlan,
  jump,
  embedded,
  readOnly = false,
}: TaskAssignPageProps) {
  const [view, setView] = React.useState<"asset" | "scene">("asset");
  const [assetType, setAssetType] = React.useState<AssetTypeKey>("character");
  const [draftAssignees, setDraftAssignees] = React.useState<Record<string, string>>({});
  const [bulkAssignee, setBulkAssignee] = React.useState<Record<string, string>>({});
  const [pendingReassignment, setPendingReassignment] = React.useState<PendingReassignment | null>(null);
  const [reassignmentReason, setReassignmentReason] = React.useState("");
  const [reassignmentSaving, setReassignmentSaving] = React.useState(false);

  React.useEffect(() => {
    setDraftAssignees({});
  }, [tasks]);

  const assignable = users.filter((user) => user.role !== "director" && user.role !== "admin");
  const assigneeOptions = assignable.length ? assignable : users;

  const assetById = React.useMemo(() => {
    const map: Record<string, Asset> = {};
    assets.forEach((asset) => {
      map[asset.id] = asset;
    });
    return map;
  }, [assets]);

  const assetTasks = tasks.filter((task) => Boolean(task.asset_id) && !task.storyboard_id && [
    "asset", "text_to_image", "audio",
  ].includes(task.task_type) && !task.variant_plan_id);
  const storyboardTasks = tasks.filter((task) => Boolean(task.storyboard_id) && [
    "storyboard_shot", "text_to_image", "image_to_video",
  ].includes(task.task_type));

  const assetTaskGroups = React.useMemo(() => groupAssetTasks(assetTasks, assetById), [assetTasks, assetById]);
  React.useEffect(() => {
    if (assetTaskGroups.some((group) => assignmentAssetType(group.asset) === assetType)) return;
    setAssetType(ASSET_TYPES.find((type) => assetTaskGroups.some((group) => assignmentAssetType(group.asset) === type)) || "character");
  }, [assetTaskGroups, assetType]);

  function selectedAssignee(task: Task) {
    return draftAssignees[task.id] ?? task.assignee_id ?? "";
  }

  const lockByScene = React.useMemo(() => {
    const map: Record<string, boolean> = {};
    sceneGating.forEach((entry) => {
      if (entry.scene_name) map[entry.scene_name] = entry.locked;
      if (entry.scene_code) map[entry.scene_code] = entry.locked;
    });
    return map;
  }, [sceneGating]);

  function assigneeName(userId: string | null | undefined) {
    if (!userId) return "未分配";
    return assigneeOptions.find((user) => user.id === userId)?.display_name || "未知执行人";
  }

  function mutableAssignmentTasks(targetTasks: Task[]) {
    return targetTasks.filter((task) => !["submitted", "reviewing", "completed"].includes(task.status));
  }

  function tasksBeingReassigned(targetTasks: Task[], nextAssigneeId: string) {
    return mutableAssignmentTasks(targetTasks).filter((task) => Boolean(task.assignee_id && task.assignee_id !== nextAssigneeId));
  }

  function linkedAssignmentTasks(task: Task) {
    if (task.storyboard_id && ["text_to_image", "image_to_video"].includes(task.task_type)) {
      return storyboardTasks.filter((item) => item.storyboard_id === task.storyboard_id && ["text_to_image", "image_to_video"].includes(item.task_type));
    }
    return [task];
  }

  function bulkScopeTasks(scope: string, key: string) {
    if (scope === "asset_category") {
      return assetTaskGroups.filter((group) => assignmentAssetType(group.asset) === key).flatMap((group) => group.tasks);
    }
    if (scope === "scene") {
      return shotFlows
        .filter((flow) => flow.scene === key || flow.sceneCode === key)
        .filter((flow) => flow.promptReady && !flow.videoDone && (flow.activePhase === "keyframe" || flow.videoUnlocked))
        .map((flow) => flow.assignTask);
    }
    if (scope === "all_assets") return assetTasks;
    if (scope === "all_storyboards") {
      return shotFlows
        .filter((flow) => flow.promptReady && !flow.videoDone && (flow.activePhase === "keyframe" || flow.videoUnlocked))
        .map((flow) => flow.assignTask);
    }
    return [];
  }

  async function requestAssignment(
    title: string,
    targetTasks: Task[],
    nextAssigneeId: string,
    execute: (reason?: string) => Promise<void>,
  ) {
    const reassignments = tasksBeingReassigned(targetTasks, nextAssigneeId);
    if (!reassignments.length) {
      await execute();
      return;
    }
    const previousNames = Array.from(new Set(reassignments.map((task) => assigneeName(task.assignee_id)))).join("、");
    setReassignmentReason("");
    setPendingReassignment({
      title,
      previousNames,
      nextName: assigneeName(nextAssigneeId),
      taskCount: reassignments.length,
      execute: async (reason) => execute(reason),
    });
  }

  async function confirmReassignment() {
    if (!pendingReassignment || !reassignmentReason.trim()) return;
    setReassignmentSaving(true);
    try {
      await pendingReassignment.execute(reassignmentReason.trim());
      setPendingReassignment(null);
      setReassignmentReason("");
    } finally {
      setReassignmentSaving(false);
    }
  }

  function statusBadge(task: Task) {
    if (task.status === "completed") return <span className="badge mint">已完成</span>;
    if (task.status === "in_progress") return <span className="badge sky">进行中</span>;
    if (task.assignee_id) return <span className="badge sky">已分配</span>;
    return <span className="badge sun">待分配</span>;
  }

  // ===== ShotFlow: 分镜任务流抽象（对齐 legacy-ui） =====
  type ShotFlow = {
    id: string;
    title: string;
    scene: string;
    sceneCode: string;
    promptText: string;
    promptReady: boolean;
    keyframeDone: boolean;
    videoDone: boolean;
    videoUnlocked: boolean;
    activePhase: "keyframe" | "video";
    assignTask: Task;
  };

  const shotFlows = React.useMemo(() => buildShotFlows(storyboardTasks), [storyboardTasks]);

  function shotFlowStatusBadge(flow: ShotFlow, task: Task) {
    if (["rejected", "rework", "needs_rework"].includes(String(task.status))) {
      return <span className="badge sun">需返工</span>;
    }
    if (flow.videoDone || task.status === "completed") return <span className="badge mint">已完成</span>;
    if (!flow.promptReady) return <span className="badge muted">待提示词</span>;
    if (!flow.keyframeDone) {
      if (task.assignee_id || ["in_progress", "submitted", "reviewing"].includes(String(task.status))) {
        return <span className="badge sky">已分配</span>;
      }
      return <span className="badge sun">待分配</span>;
    }
    if (!flow.videoDone) {
      if (flow.activePhase === "video" && (task.assignee_id || ["in_progress", "submitted", "reviewing"].includes(String(task.status)))) {
        return <span className="badge sky">已分配</span>;
      }
      return <span className="badge sun">待分配</span>;
    }
    return <span className="badge sun">待分配</span>;
  }

  function ShotFlowSteps({ flow }: { flow: ShotFlow }) {
    const steps = [
      { key: "prompt", label: "提示词", state: "done" as const },
      { key: "keyframe", label: "关键帧", state: flow.keyframeDone ? ("done" as const) : ("pending" as const) },
      { key: "video", label: "视频", state: flow.videoDone ? ("done" as const) : ("pending" as const) },
    ];
    return (
      <ol className="shot-flow-steps" aria-label="镜头任务流">
        {steps.map((step, index) => (
          <li key={step.key} className={`shot-flow-steps__item is-${step.state}`}>
            {index > 0 ? <i className="shot-flow-steps__rail" aria-hidden="true" /> : null}
            <span>{step.label}{step.state === "done" ? "✓" : ""}</span>
          </li>
        ))}
      </ol>
    );
  }

  function ShotFlowRow({ flow }: { flow: ShotFlow }) {
    const task = flow.assignTask;
    const assigneeId = selectedAssignee(task);
    const completed = flow.videoDone;
    const assignLocked = !flow.promptReady || (flow.activePhase === "video" && !flow.videoUnlocked) || flow.videoDone;
    const isReassignment = tasksBeingReassigned([task], assigneeId).length > 0;
    const phaseLabel = flow.activePhase === "video" ? "视频" : "关键帧";
    const sameAssignee = Boolean(assigneeId) && assigneeId === (task.assignee_id ?? "");
    const canSubmitAssign = Boolean(assigneeId) && !sameAssignee;
    const phaseText = flow.videoDone ? "已完成" : flow.activePhase === "video" ? "视频" : "关键帧";
    const titleBase = flow.title.replace(/\s*(关键帧|视频|图生视频)$/u, "").trim();
    const titleWithPhase = `${titleBase} ${phaseText === "已完成" ? "视频" : phaseText}`;
    const actionLabel = flow.videoDone
      ? "已完成"
      : isReassignment
        ? `改派${phaseLabel}`
        : `分配${phaseLabel}`;
    return (
      <tr className={assignLocked && !flow.videoDone ? "is-phase-locked" : undefined}>
        <td>
          <div className="assignment-asset-name">
            <strong>{titleWithPhase}</strong>
            <span>{flow.promptText || "等待 Agent 生成提示词后可分发关键帧"}</span>
          </div>
        </td>
        <td>{phaseText}</td>
        <td><ShotFlowSteps flow={flow} /></td>
        <td>
          <AssigneeSelect
            value={assigneeId}
            users={assigneeOptions}
            disabled={readOnly || assignLocked || completed}
            onChange={(id) => setDraftAssignees((current) => ({ ...current, [task.id]: id }))}
          />
        </td>
        <td>{shotFlowStatusBadge(flow, task)}</td>
        <td className="asset-assignment-action-cell">
          <button
            className="btn assignment-row-action"
            disabled={readOnly || assignLocked || completed || !canSubmitAssign}
            title={!flow.promptReady
              ? "请先由 Agent 生成提示词"
              : flow.activePhase === "video" && !flow.videoUnlocked
                ? "关键帧审批通过后才能分发视频"
                : !assigneeId
                  ? "请先选择执行人"
                  : sameAssignee
                    ? "当前执行人未变更"
                    : undefined}
            onClick={() => void requestAssignment(
              `${flow.title} · ${phaseLabel}`,
              [task],
              assigneeId,
              (reason) => onAssignTask(task, assigneeId, reason),
            )}
          >
            {actionLabel}
          </button>
        </td>
      </tr>
    );
  }

  function VariantPlanPanel() {
    const availableShots = Array.from(new Map(
      storyboardTasks.filter((task) => task.storyboard_id).map((task) => [task.storyboard_id as string, task]),
    ).values());
    const variantAssets = assets.filter((asset) => asset.asset_type === "scene" || asset.asset_type === "prop");
    async function addVariant() {
      if (readOnly || !variantAssets.length) return;
      const assetCode = window.prompt("输入场景/道具资产编号，例如 SC001 或 P001");
      const asset = variantAssets.find((item) => item.asset_code === assetCode?.trim() || item.name === assetCode?.trim());
      if (!asset) return;
      const kind = asset.asset_type === "scene" ? "scene_view" : "prop_state";
      const title = window.prompt("中文变体名称", asset.asset_type === "scene" ? "入口向内视角" : "使用中状态");
      const description = window.prompt("中文视角/状态描述");
      if (!title?.trim() || !description?.trim()) return;
      await onCreateAssetVariantPlan(projectId, {
        asset_id: asset.id,
        episode_id: episodeId || null,
        variant_kind: kind,
        title_zh: title.trim(),
        description_zh: description.trim(),
        storyboard_ids: availableShots.slice(0, 1).map((task) => task.storyboard_id as string),
        source: "director",
      });
    }
    return (
      <Panel
        title="场景 / 道具变体计划"
        subtitle="分镜生成后自动建议；同一变体可复用到多个分镜，保存关联后任务依赖会自动刷新。"
        action={<button className="btn primary" disabled={readOnly || !variantAssets.length} type="button" onClick={() => void addVariant()}>新增变体</button>}
      >
        {!assetVariantPlans.length ? <div className="empty-hint">当前分集还没有场景视角或道具状态变体。</div> : (
          <div className="variant-plan-list">
            {assetVariantPlans.map((plan) => (
              <VariantPlanRow
                key={plan.id}
                plan={plan}
                availableShots={availableShots}
                readOnly={readOnly}
                onSave={onUpdateAssetVariantPlan}
              />
            ))}
          </div>
        )}
      </Panel>
    );
  }

  function TaskRow({ task }: { task: Task }) {
    const assigneeId = selectedAssignee(task);
    const completed = task.status === "completed";
    const assignmentTasks = linkedAssignmentTasks(task);
    const isReassignment = tasksBeingReassigned(assignmentTasks, assigneeId).length > 0;
    return (
      <div className="assign-row" key={task.id}>
        <div className="assign-main">
          <strong>{task.title}</strong>
          <span>{task.variant_kind === "human_temporary" ? task.variant_description_zh || "待补充人工生产要求" : task.prompt_text || task.latest_prompt_text || "暂无提示词"}</span>
        </div>
        {task.storyboard_id ? (
          <span className="step-chips">
            {task.task_type === "storyboard_shot" ? <>
              <span className={task.keyframe_done ? "chip done" : "chip"}>关键帧{task.keyframe_done ? "✓" : ""}</span>
              <span className={task.video_done ? "chip done" : "chip"}>视频{task.video_done ? "✓" : ""}</span>
            </> : <span className="chip">{task.task_type === "image_to_video" ? "视频" : "关键帧"}</span>}
          </span>
        ) : null}
        <AssigneeSelect
          value={assigneeId}
          users={assigneeOptions}
          disabled={readOnly || completed}
          onChange={(id) => setDraftAssignees((current) => ({ ...current, [task.id]: id }))}
        />
        {statusBadge(task)}
        <button
          className="btn"
          disabled={readOnly || completed || assigneeId === (task.assignee_id ?? "")}
          onClick={() => void requestAssignment(
            task.title,
            assignmentTasks,
            assigneeId,
            (reason) => onAssignTask(task, assigneeId, reason),
          )}
        >
          {isReassignment ? "改派" : "分配"}
        </button>
      </div>
    );
  }

  function AssetGroupRow({ group }: { group: AssetTaskGroup }) {
    const draftKey = `asset:${group.key}`;
    const assigneeId = bulkAssignee[draftKey] ?? group.assigneeId;
    const completed = group.tasks.filter((task) => task.status === "completed").length;
    const hasReviewing = group.tasks.some((task) => ["reviewing", "submitted"].includes(task.status));
    const hasInProgress = group.tasks.some((task) => task.status === "in_progress");
    const hasRework = group.tasks.some((task) => task.status === "rejected");
    const fullyCompleted = completed === group.tasks.length;
    const isHumanTemporary = group.tasks.every((task) => task.variant_kind === "human_temporary");
    const assignmentReadOnly = readOnly && !isHumanTemporary;
    const isAudio = assignmentAssetType(group.asset) === "music";
    const outputLabel = isAudio ? "音频母版" : assetOutputLabel(group.viewCodes);
    const isReassignment = tasksBeingReassigned(group.tasks, assigneeId).length > 0;
    const status = fullyCompleted
      ? <span className="badge mint">已完成</span>
      : hasReviewing
        ? <span className="badge sky">待审核</span>
        : hasRework
          ? <span className="badge sun">需返工</span>
          : hasInProgress
            ? <span className="badge sky">进行中 {completed}/{group.tasks.length}</span>
      : group.locked
        ? <span className="badge sun">等待道具</span>
        : assigneeId
          ? <span className="badge sky">已分配</span>
          : <span className="badge sun">待分配</span>;
    return <tr>
      <td><span className={`asset-priority-badge priority-${group.priority.toLowerCase()}`}>{group.priority}</span></td>
      <td>{ASSET_TYPE_LABELS[group.asset.asset_type] || "资产"}</td>
      <td><div className="assignment-asset-name"><strong>{assetCodeDisplay(group.asset.asset_code, "编码异常")}</strong><span>{group.asset.name}</span></div></td>
      <td>{group.contextLabel}</td>
      <td><strong>{outputLabel}</strong><span className="assignment-output-count">{group.tasks.length}{isAudio ? "个任务" : "张"}</span></td>
      <td>{group.dependencyCodes.length
        ? <span className={group.locked ? "dependency-waiting" : "dependency-ready"}>{group.locked ? "等待" : "已完成"}：{group.dependencyCodes.join("、")}</span>
        : <span className="muted">无前置依赖</span>}</td>
      <td><AssigneeSelect
        value={assigneeId}
        users={assigneeOptions}
        disabled={assignmentReadOnly || fullyCompleted}
        onChange={(id) => setBulkAssignee((current) => ({ ...current, [draftKey]: id }))}
      /></td>
      <td>{status}</td>
      <td><button
        className="btn"
        disabled={assignmentReadOnly || fullyCompleted || !assigneeId || assigneeId === group.assigneeId}
        onClick={() => void requestAssignment(
          `${assetCodeDisplay(group.asset.asset_code, "编码异常")} ${group.asset.name}`,
          group.tasks,
          assigneeId,
          (reason) => onBulkAssign("asset_context", group.key, assigneeId, reason),
        )}
      >{isReassignment ? "改派" : "分配"}</button></td>
    </tr>;
  }

  function BulkAssignBar({ scope, groupKey, label = "整批分配给" }: { scope: string; groupKey: string; label?: string }) {
    const value = bulkAssignee[groupKey] ?? "";
    const targetTasks = bulkScopeTasks(scope, groupKey);
    return (
      <div className="bulk-assign-bar">
        <span className="bulk-assign-bar__label">{label}</span>
        <AssigneeSelect
          value={value}
          users={assigneeOptions}
          disabled={readOnly}
          onChange={(id) => setBulkAssignee((current) => ({ ...current, [groupKey]: id }))}
        />
        <button
          className="btn assignment-outline-btn"
          disabled={readOnly || !value}
          onClick={() => void requestAssignment(
            label.replace(/整批分配给$/, "") || `${groupKey} 批量任务`,
            targetTasks,
            value,
            (reason) => onBulkAssign(scope, groupKey, value, reason),
          )}
        >
          整批分配
        </button>
      </div>
    );
  }

  const stageReady = projectStatus === "task_assignment" || projectStatus === "production";

  return (
    <>
      <div className="task-assignment-workbench">
        <div className="assign-view-switch" role="tablist" aria-label="任务分区">
          <button
            className={`assign-view-switch__card ${view === "asset" ? "is-active" : ""}`}
            role="tab"
            aria-selected={view === "asset"}
            type="button"
            onClick={() => setView("asset")}
          >
            <div className="assign-view-switch__head">
              <strong>基础资产任务</strong>
              <em>{assetTasks.length}</em>
            </div>
            <span>人物、场景、道具定装与母版生产分配</span>
          </button>
          <button
            className={`assign-view-switch__card ${view === "scene" ? "is-active" : ""}`}
            role="tab"
            aria-selected={view === "scene"}
            type="button"
            onClick={() => setView("scene")}
          >
            <div className="assign-view-switch__head">
              <strong>场景分镜分配</strong>
              <em>{storyboardTasks.length}</em>
            </div>
            <span>提示词就绪后分发关键帧，审批通过后再开视频</span>
          </button>
        </div>

        {!stageReady && !assetTasks.length && !storyboardTasks.length ? (
          <div className="empty-hint">尚未生成任务。资产任务会在提示词生成完成后统一分发，分镜任务会在锁定分镜后生成。</div>
        ) : null}

        {view === "asset" ? (
          <section className="asset-assignment-page">
            <div className="assignment-asset-type-chips" role="tablist" aria-label="资产类型">
              {ASSET_TYPES.map((type) => {
                const count = assetTaskGroups.filter((group) => assignmentAssetType(group.asset) === type).length;
                return (
                  <button
                    className={assetType === type ? "is-active" : ""}
                    key={type}
                    type="button"
                    role="tab"
                    aria-selected={assetType === type}
                    onClick={() => setAssetType(type)}
                  >
                    {ASSET_TYPE_LABELS[type]}
                    <span>{count}</span>
                  </button>
                );
              })}
            </div>
            <BulkAssignBar
              scope="asset_category"
              groupKey={assetType}
              label={`当前${ASSET_TYPE_LABELS[assetType]}资产整批分配给`}
            />
            {(() => {
              const sortedTypedGroups = [...assetTaskGroups]
                .filter((group) => assignmentAssetType(group.asset) === assetType)
                .sort((a, b) => ASSET_PRIORITIES.indexOf(a.priority) - ASSET_PRIORITIES.indexOf(b.priority));
              return sortedTypedGroups.length ? (
                <div className="asset-assignment-table-wrap">
                  <table className="asset-assignment-table">
                    <thead>
                      <tr>
                        <th>等级</th>
                        <th>类型</th>
                        <th>资产</th>
                        <th>生产内容</th>
                        <th>图位</th>
                        <th>前置依赖</th>
                        <th>执行人</th>
                        <th>状态</th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {sortedTypedGroups.map((group) => <AssetGroupRow group={group} key={group.key} />)}
                    </tbody>
                  </table>
                </div>
              ) : null;
            })()}
            {assetTasks.length && !assetTaskGroups.some((group) => assignmentAssetType(group.asset) === assetType)
              ? <div className="empty-hint">暂无{ASSET_TYPE_LABELS[assetType]}资产任务。</div>
              : null}
            {!assetTasks.length ? <div className="empty-hint">暂无资产任务。</div> : null}
          </section>
        ) : (
          <section className="assign-groups shot-assignment-page">
            {(() => {
              const sceneFlows = shotFlows.reduce<Record<string, typeof shotFlows>>((groups, flow) => {
                const scene = flow.scene;
                (groups[scene] = groups[scene] || []).push(flow);
                return groups;
              }, {});
              return Object.entries(sceneFlows).map(([scene, flows]) => {
                const locked = lockByScene[scene] ?? false;
                return (
                  <Panel
                    key={scene}
                    title={scene}
                    subtitle={`${flows.length} 个分镜任务`}
                    action={locked ? <span className="admin-tag admin-tag--warn">资产未完成，未解锁</span> : <span className="badge mint shot-unlock-badge">✓ 已解锁</span>}
                  >
                    <BulkAssignBar scope="scene" groupKey={scene} label="整批分配给" />
                    <div className="asset-assignment-table-wrap">
                      <table className="asset-assignment-table shot-assignment-table">
                        <thead>
                          <tr>
                            <th>镜头</th>
                            <th>当前阶段</th>
                            <th>任务进度</th>
                            <th>执行人</th>
                            <th>状态</th>
                            <th>操作</th>
                          </tr>
                        </thead>
                        <tbody>
                          {flows.map((flow) => <ShotFlowRow flow={flow} key={flow.id} />)}
                        </tbody>
                      </table>
                    </div>
                  </Panel>
                );
              });
            })()}
            {!storyboardTasks.length ? <div className="empty-hint">暂无分镜任务。锁定当前分镜终版后会自动生成。</div> : null}
          </section>
        )}
      </div>
      {pendingReassignment ? <div className="modal-backdrop" role="presentation" onClick={() => { if (!reassignmentSaving) setPendingReassignment(null); }}>
        <section className="reassignment-confirm-modal" role="alertdialog" aria-modal="true" aria-label="确认改派任务" onClick={(event) => event.stopPropagation()}>
          <div className="modal-head"><div><h3>确认改派任务</h3><p>改派后系统将通知原执行人和新执行人。</p></div><button className="btn" disabled={reassignmentSaving} type="button" onClick={() => setPendingReassignment(null)}>关闭</button></div>
          <div className="reassignment-confirm-body">
            <strong>{pendingReassignment.title}</strong>
            <dl><div><dt>影响任务</dt><dd>{pendingReassignment.taskCount} 项</dd></div><div><dt>原执行人</dt><dd>{pendingReassignment.previousNames}</dd></div><div><dt>新执行人</dt><dd>{pendingReassignment.nextName}</dd></div></dl>
            <label className="field"><span>改派原因</span><textarea autoFocus maxLength={500} placeholder="请填写人员调整、排期或质量要求等具体原因" value={reassignmentReason} onChange={(event) => setReassignmentReason(event.target.value)} /></label>
          </div>
          <footer><button className="btn" disabled={reassignmentSaving} type="button" onClick={() => setPendingReassignment(null)}>取消</button><button className="btn primary" disabled={reassignmentSaving || !reassignmentReason.trim()} type="button" onClick={() => void confirmReassignment()}>{reassignmentSaving ? "改派中" : "确认改派并通知"}</button></footer>
        </section>
      </div> : null}
    </>
  );
}

function VariantPlanRow({
  plan,
  availableShots,
  readOnly,
  onSave,
}: {
  plan: AssetVariantPlan;
  availableShots: Task[];
  readOnly: boolean;
  onSave: (planId: string, payload: Partial<Pick<AssetVariantPlan, "title_zh" | "description_zh" | "status" | "storyboard_ids">>) => Promise<void>;
}) {
  const [title, setTitle] = React.useState(plan.title_zh);
  const [description, setDescription] = React.useState(plan.description_zh);
  const [storyboardIds, setStoryboardIds] = React.useState(plan.storyboard_ids);
  const [saving, setSaving] = React.useState(false);
  React.useEffect(() => {
    setTitle(plan.title_zh);
    setDescription(plan.description_zh);
    setStoryboardIds(plan.storyboard_ids);
  }, [plan.id, plan.title_zh, plan.description_zh, plan.storyboard_ids]);
  const dirty = title !== plan.title_zh || description !== plan.description_zh
    || storyboardIds.join(",") !== plan.storyboard_ids.join(",");
  async function save(payload: Partial<Pick<AssetVariantPlan, "title_zh" | "description_zh" | "status" | "storyboard_ids">> = {}) {
    setSaving(true);
    try {
      await onSave(plan.id, { title_zh: title.trim(), description_zh: description.trim(), storyboard_ids: storyboardIds, ...payload });
    } finally {
      setSaving(false);
    }
  }
  return (
    <div className={`variant-plan-row ${plan.status === "retired" ? "retired" : ""}`}>
      <div className="variant-plan-code"><strong>{plan.task_code || `${assetCodeDisplay(plan.asset_code, "编码异常")}-${plan.variant_code}`}</strong><span>{plan.variant_kind === "scene_view" ? "场景视角" : "道具状态"}</span></div>
      <div className="variant-plan-fields">
        <input disabled={readOnly || plan.status === "retired"} value={title} onChange={(event) => setTitle(event.target.value)} aria-label="变体名称" />
        <textarea disabled={readOnly || plan.status === "retired"} value={description} onChange={(event) => setDescription(event.target.value)} aria-label="变体描述" />
      </div>
      <div className="variant-plan-links">
        <span className="muted">关联分镜</span>
        <select
          disabled={readOnly || plan.status === "retired"}
          multiple
          value={storyboardIds}
          onChange={(event) => setStoryboardIds(Array.from(event.target.selectedOptions).map((option) => option.value))}
          aria-label="选择关联分镜"
        >
          {availableShots.map((task) => <option key={task.storyboard_id} value={task.storyboard_id as string}>{task.title}</option>)}
        </select>
        <span className="variant-plan-shot-codes">{plan.storyboard_codes.length ? plan.storyboard_codes.join("、") : "未关联分镜"}</span>
      </div>
      <div className="variant-plan-actions">
        <span className={`badge ${plan.status === "confirmed" ? "mint" : plan.status === "retired" ? "muted" : "sun"}`}>{plan.status === "confirmed" ? "已确认" : plan.status === "retired" ? "已停用" : "待确认"}</span>
        <button className="btn" disabled={readOnly || !dirty || saving || plan.status === "retired"} type="button" onClick={() => void save()}>{saving ? "保存中" : "保存"}</button>
        <button className="btn danger" disabled={readOnly || saving || plan.status === "retired"} type="button" onClick={() => void save({ status: "retired" })}>停用</button>
      </div>
    </div>
  );
}

function groupAssetTasks(tasks: Task[], assetById: Record<string, Asset>): AssetTaskGroup[] {
  const groups = new Map<string, AssetTaskGroup>();
  for (const task of tasks) {
    if (!task.asset_id || !assetById[task.asset_id]) continue;
    const asset = assetById[task.asset_id];
    const ageStageCode = task.age_stage_code || "";
    const costumeVariantCode = task.costume_variant_code || "";
    const key = `${asset.id}|${ageStageCode}|${costumeVariantCode}`;
    const current = groups.get(key) || {
      key,
      asset,
      priority: assetPriorityValue(asset),
      ageStageCode,
      costumeVariantCode,
      contextLabel: assetContextLabel(asset, ageStageCode, costumeVariantCode),
      tasks: [],
      viewCodes: [],
      dependencyCodes: [],
      assigneeId: "",
      locked: false,
    };
    current.tasks.push(task);
    if (task.task_variant && !current.viewCodes.includes(task.task_variant)) current.viewCodes.push(task.task_variant);
    const externalDependencyCodes = (task.dependency_asset_codes || []).filter((code) => code !== asset.asset_code);
    for (const code of externalDependencyCodes) {
      if (!current.dependencyCodes.includes(code)) current.dependencyCodes.push(code);
    }
    current.assigneeId = current.assigneeId || task.assignee_id || "";
    // A-E views may depend on view A within the same asset. Only surface an
    // asset group as waiting when the unresolved dependency is external.
    current.locked = current.locked || (externalDependencyCodes.length > 0 && Boolean(task.locked));
    groups.set(key, current);
  }
  const categoryOrder: Record<string, number> = { character: 0, scene: 1, prop: 2, music: 3 };
  return Array.from(groups.values()).map((group) => ({
    ...group,
    tasks: group.tasks.slice().sort((left, right) => String(left.task_variant || "").localeCompare(String(right.task_variant || ""))),
    viewCodes: group.viewCodes.slice().sort(),
  })).sort((left, right) => (
    (categoryOrder[assignmentAssetType(left.asset)] ?? 9) - (categoryOrder[assignmentAssetType(right.asset)] ?? 9)
    || String(left.asset.asset_code || "").localeCompare(String(right.asset.asset_code || ""))
    || left.contextLabel.localeCompare(right.contextLabel)
  ));
}

function assetPriorityValue(asset: Asset): AssetPriority {
  const metadata = asset.metadata || {};
  const source = recordValue(metadata.breakdown_asset);
  const value = String(source.priority || metadata.priority || "C").toUpperCase();
  return ASSET_PRIORITIES.includes(value as AssetPriority) ? value as AssetPriority : "C";
}

function assetContextLabel(asset: Asset, ageStageCode: string, costumeVariantCode: string) {
  if (assignmentAssetType(asset) === "music") return asset.asset_type === "voice_profile" ? "角色音色母版" : "分集音乐母版";
  if (asset.asset_type === "scene") return "场景母版";
  if (asset.asset_type === "prop") return "道具母版";
  if (!ageStageCode && !costumeVariantCode) return "角色身份基础定装";
  const metadata = asset.metadata || {};
  const source = recordValue(metadata.breakdown_asset);
  const stagesSource = Array.isArray(source.age_stages) ? source.age_stages : metadata.age_stages;
  const stages = Array.isArray(stagesSource) ? stagesSource.map(recordValue) : [];
  const stage = stages.find((item) => String(item.stage_code || "") === ageStageCode);
  const variants = stage && Array.isArray(stage.costume_variants) ? stage.costume_variants.map(recordValue) : [];
  const costume = variants.find((item) => String(item.variant_code || "") === costumeVariantCode);
  const ageLabel = [ageStageCode, String(stage?.name || "")].filter(Boolean).join(" ");
  const costumeLabel = [costumeVariantCode, String(costume?.name || "")].filter(Boolean).join(" ");
  return [ageLabel, costumeLabel].filter(Boolean).join(" / ") || "人物定装";
}

function assignmentAssetType(asset: Asset): AssetTypeKey {
  return ["music", "voice_profile", "audio"].includes(String(asset.asset_type || "")) ? "music" : asset.asset_type as AssetTypeKey;
}

function assetOutputLabel(viewCodes: string[]) {
  if (viewCodes.join(",") === "A,B,C,D,E") return "A-E 定装";
  if (viewCodes.join(",") === "MASTER") return "母版图";
  return `${viewCodes.join("、") || "未设置"} 定装`;
}

function recordValue(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function taskText(task: Task | undefined, key: string) {
  if (!task) return "";
  const value = task[key as keyof Task];
  return typeof value === "string" ? value : "";
}

function buildShotFlows(tasks: Task[]) {
  const groups = new Map<string, Task[]>();
  for (const task of tasks) {
    const key = String(task.storyboard_id || task.id);
    const list = groups.get(key) || [];
    list.push(task);
    groups.set(key, list);
  }
  return Array.from(groups.entries()).map(([id, group]) => {
    const unified = group.find((task) => task.task_type === "storyboard_shot");
    const keyframeTask = group.find((task) => task.task_type === "text_to_image") || unified;
    const videoTask = group.find((task) => task.task_type === "image_to_video") || unified;
    const display = unified || keyframeTask || videoTask || group[0];
    const promptText = (
      taskText(display, "prompt_text")
      || taskText(display, "latest_prompt_text")
      || taskText(keyframeTask, "prompt_text")
      || taskText(keyframeTask, "latest_prompt_text")
    );
    const promptReady = Boolean(promptText.trim());
    const keyframeDone = Boolean(
      display?.keyframe_done
      || keyframeTask?.keyframe_done
      || (keyframeTask && keyframeTask !== unified && ["completed"].includes(String(keyframeTask.status))),
    );
    const videoDone = Boolean(
      display?.video_done
      || videoTask?.video_done
      || (videoTask && videoTask !== unified && videoTask.status === "completed"),
    );
    const videoUnlocked = keyframeDone && !videoDone;
    const activePhase: "keyframe" | "video" = keyframeDone ? "video" : "keyframe";
    const assignTask = activePhase === "video"
      ? (videoTask || display)
      : (keyframeTask || display);
    const title = taskText(unified, "title")
      || taskText(display, "title").replace(/\s*(关键帧|视频|图生视频)$/u, "")
      || id;
    return {
      id,
      title,
      scene: taskText(display, "scene_name") || taskText(display, "scene_code") || "未归类场景",
      sceneCode: taskText(display, "scene_code"),
      promptText,
      promptReady,
      keyframeDone,
      videoDone,
      videoUnlocked,
      activePhase,
      assignTask: assignTask!,
    };
  }).sort((left, right) => left.title.localeCompare(right.title, "zh"));
}

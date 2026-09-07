import { assetParticipatesInLifecycleGate, characterAssetIsNonVisual } from "./assetLifecycle";
import { assetDisplayLabel, assetNamePart, assetReferenceDisplayLabel, hydrateStoryboardSceneNames, shortAssetCode } from "./assetCode";
import { productionProgressByEntity } from "./projectProgress";
import { normalizeManualReviewItems } from "./reviewNormalization";
import { parseAssetNormalizations, parseBreakdownRead, parseGlobalAssetReviewItems } from "./assetNormalization";
import { promptRecordMatchesAsset } from "./promptRecordMatch";
import type {
  AgentRun,
  AssetLifecycleStatus,
  AssetNormalizationReviewItem,
  BreakdownDraftAsset,
  BreakdownDraftData,
  BreakdownDraftScriptSegment,
  BreakdownDraftStoryboard,
  BreakdownManualReviewItem,
  BreakdownRead,
  Project,
  ProjectStage,
  ScriptSegment,
  Storyboard,
  Task,
  TaskPhaseKey,
  User,
} from "./types";

export { characterAssetIsNonVisual } from "./assetLifecycle";

export function scriptSegmentEpisode(segment: ScriptSegment) {
  return segment.episode_code || `EP${String(segment.order_no || 1).padStart(2, "0")}`;
}

export function segmentDisplayTitle(segment: ScriptSegment) {
  return segment.title || segment.script_segment_code || "未命名脚本原文段";
}

/**
 * Normalize all scene references to the user-facing SC### convention.
 * Storage may contain S###, SC###, or a project-prefixed code such as HJBG-S###.
 */
export function canonicalSceneCode(value?: unknown) {
  const raw = String(value ?? "").trim().toUpperCase();
  if (!raw) return "";
  const match = raw.match(/(?:^|[-\s])S(?:C)?0*(\d+)(?=$|[-\s:：·])/i)
    || raw.match(/^(?:SC|S)0*(\d+)$/i);
  return match ? `SC${String(Number(match[1])).padStart(3, "0")}` : raw;
}

export { shortAssetCode };
export { assetDisplayLabel, assetNamePart };
export { assetReferenceDisplayLabel };

export function hasFormalAssetCode(value?: unknown, source?: unknown) {
  return Boolean(shortAssetCode(value, source));
}

export function assetCodeDisplay(value?: unknown, emptyLabel = "保存后分配", source?: unknown) {
  return shortAssetCode(value, source) || emptyLabel;
}

export function breakdownDraftProjectPrefix(projectId: string) {
  return `breakdown:${projectId}:`;
}

export function breakdownDraftScopeKey(projectId: string, episodeId: string, scriptVersionId: string) {
  return `${breakdownDraftProjectPrefix(projectId)}${episodeId}:${scriptVersionId}`;
}

// Status values that mean a review item no longer blocks confirmation.
export const RESOLVED_REVIEW_STATUSES = ["confirmed", "resolved", "ignored", "applied"] as const;

// Single source of truth for "this review item is required and still blocks confirmation".
// Used by asset lifecycle status, the workbench, and the breakdown-page confirmation gate.
export function isRequiredPendingReview(item: AssetNormalizationReviewItem): boolean {
  return (
    item.requirement_level === "required"
    && !item.human_input
    && !RESOLVED_REVIEW_STATUSES.includes(item.status.toLowerCase() as (typeof RESOLVED_REVIEW_STATUSES)[number])
  );
}

export function assetLifecycleStatus(asset: BreakdownDraftAsset): AssetLifecycleStatus {
  const blockingReview = asset.review_items.some(isRequiredPendingReview);
  return blockingReview ? "needs_completion" : "pending_confirmation";
}

export function normalizeAssetLifecycleStatus(value: unknown, fallback: AssetLifecycleStatus = "pending_confirmation"): AssetLifecycleStatus {
  const normalized = String(value || "").trim().toLowerCase();
  if (normalized === "needs_completion" || normalized === "prompt_pending" || normalized === "pending_confirmation" || normalized === "confirmed") return normalized;
  return fallback;
}

export function assetLifecycleLabel(status: AssetLifecycleStatus) {
  return {
    needs_completion: "待完善",
    prompt_pending: "待补 Prompt",
    pending_confirmation: "待确认",
    confirmed: "已确认",
  }[status];
}

export function assetsNeedingCompletion(draft: BreakdownDraftData) {
  return draft.assets.filter((asset) => assetParticipatesInLifecycleGate(asset) && assetLifecycleStatus(asset) === "needs_completion");
}

export function assetsAwaitingConfirmation(draft: BreakdownDraftData) {
  return draft.assets.filter((asset) => assetParticipatesInLifecycleGate(asset) && assetLifecycleStatus(asset) === "pending_confirmation");
}

function sceneNamePart(value?: unknown) {
  const raw = String(value ?? "").trim();
  if (!raw) return "";
  if (/^(?:[A-Z0-9]+-)?S(?:C)?0*\d+$/i.test(raw)) return "";
  const match = raw.match(/^(?:[A-Z0-9]+-)?S(?:C)?0*\d+\s*[-:：·]?\s*(.+)$/i);
  return match?.[1]?.trim() || raw;
}

/** Return a stable display label without changing the stored asset code. */
export function sceneDisplayLabel(code?: unknown, name?: unknown) {
  const rawCode = String(code ?? "").trim();
  const rawName = String(name ?? "").trim();
  const canonical = canonicalSceneCode(rawCode) || canonicalSceneCode(rawName);
  const cleanName = sceneNamePart(rawName) || sceneNamePart(rawCode);
  if (canonical && cleanName) return `${canonical}-${cleanName}`;
  return cleanName || canonical || "未命名场景";
}

/** Display a scene asset code only when the backend has issued it formally. */
export function sceneAssetDisplayLabel(code?: unknown, name?: unknown, source?: unknown) {
  const shortCode = shortAssetCode(code, source);
  const cleanName = sceneNamePart(name) || sceneNamePart(code);
  if (shortCode && cleanName) return `${shortCode}-${cleanName}`;
  return cleanName || shortCode || "未匹配场景";
}

export function textFromMetadata(segment: ScriptSegment, key: string) {
  const value = segment.metadata_json?.[key];
  return typeof value === "string" && value.trim() ? value : "";
}

export function sourceContext(segment: ScriptSegment) {
  const raw = segment.metadata_json?.source_context;
  if (raw && typeof raw === "object") {
    const context = raw as Record<string, unknown>;
    return {
      before: typeof context.before === "string" ? context.before : "",
      current: typeof context.current === "string" ? context.current : segment.source_text,
      after: typeof context.after === "string" ? context.after : "",
    };
  }
  return { before: "", current: segment.source_text, after: "" };
}

export function visibleSegments(segments: ScriptSegment[]) {
  return segments;
}

export function joinScriptOriginalText<T extends { source_text?: string | null }>(segments: T[]) {
  return segments
    .map((segment) => (segment.source_text || "").trim())
    .filter(Boolean)
    .join("\n\n");
}

export function isAgentPending(run?: Pick<AgentRun, "status"> | null) {
  return ["queued", "running", "working", "in_progress", "processing"].includes(String(run?.status || "").toLowerCase());
}

export function hasPendingAgentRun(runs: AgentRun[], agentType: string) {
  return runs.some((run) => run.agent_type === agentType && isAgentPending(run));
}

export function breakdownDraft(output: unknown) {
  const empty: BreakdownDraftData = {
    scriptSegments: [],
    storyboards: [],
    assets: [],
    globalAssetReviewItems: [],
    costumeDesigns: [],
    costumeProcessingSummary: [],
    assetPromptDesigns: [],
    assetPromptProcessingSummary: [],
    manualReviewItems: [],
    notes: [],
    readingReviewState: { status: "needs_review" },
    assetReviewState: { status: "needs_review" },
  };
  if (!output || typeof output !== "object") return empty;
  const data = output as Record<string, unknown>;
  const costumeDesigns = Array.isArray(data.costume_designs) ? data.costume_designs as Record<string, unknown>[] : [];
  const explicitCostumeSummary = Array.isArray(data.costume_processing_summary)
    ? data.costume_processing_summary as Record<string, unknown>[]
    : [];
  return {
    ...empty,
    scriptSegments: Array.isArray(data.script_segments)
      ? (data.script_segments as BreakdownDraftScriptSegment[])
      : Array.isArray(data.scriptSegments)
        ? (data.scriptSegments as BreakdownDraftScriptSegment[])
        : [],
    storyboards: Array.isArray(data.storyboards) ? (data.storyboards as BreakdownDraftStoryboard[]) : [],
    assets: Array.isArray(data.assets) ? (data.assets as BreakdownDraftAsset[]) : [],
    globalAssetReviewItems: Array.isArray(data.global_review_items)
      ? parseGlobalAssetReviewItems(data.global_review_items)
      : [],
    readingReport: data.reading_report && typeof data.reading_report === "object" ? data.reading_report as Record<string, unknown> : undefined,
    readingReviewState: reviewState(data.reading_review_state),
    assetReviewState: reviewState(data.asset_review_state),
    costumeDesigns,
    costumeProcessingSummary: explicitCostumeSummary,
    assetPromptDesigns: Array.isArray(data.asset_prompt_designs)
      ? data.asset_prompt_designs as Record<string, unknown>[]
      : [],
    assetPromptProcessingSummary: Array.isArray(data.asset_prompt_processing_summary)
      ? data.asset_prompt_processing_summary as Record<string, unknown>[]
      : explicitCostumeSummary,
    manualReviewItems: normalizeManualReviewItems(data.manual_review_items),
    notes: Array.isArray(data.notes) ? data.notes.map(String) : [],
    readingRevisionContext: data.reading_revision_context && typeof data.reading_revision_context === "object"
      ? data.reading_revision_context as Record<string, unknown>
      : undefined,
    assetPromptFinalization: data.asset_prompt_finalization && typeof data.asset_prompt_finalization === "object"
      ? data.asset_prompt_finalization as Record<string, unknown>
      : undefined,
  };
}

export function draftFromBreakdown(value: BreakdownRead | unknown): BreakdownDraftData {
  const breakdown = parseBreakdownRead(value);
  const base = breakdownDraft(breakdown.view);
  const assets = parseAssetNormalizations(breakdown.view.assets);
  return {
    ...base,
    assets,
    version: breakdown.version,
  };
}

export function mergeBreakdownDraft(
  project: Project | undefined,
  draft: BreakdownDraftData,
  scriptSegments: ScriptSegment[],
  storyboards: Storyboard[],
): BreakdownDraftData {
  const persistedScriptSegments = scriptSegments.map((segment) => {
    const metadata = segment.metadata_json || {};
    return {
      id: segment.id,
      script_segment_code: segment.script_segment_code,
      episode_code: segment.episode_code,
      order_no: segment.order_no,
      title: segment.title,
      source_text: segment.source_text,
      summary: segment.summary,
      story_function: segment.story_function,
      dominant_emotion: segment.dominant_emotion,
      rhythm: segment.rhythm,
      viewpoint: segment.viewpoint,
      estimated_duration_seconds: numberFromMetadata(metadata, "estimated_duration_seconds", "duration_seconds"),
      role_refs: stringListFromMetadata(metadata, "role_refs", "characters"),
      scene_refs: stringListFromMetadata(metadata, "scene_refs", "locations"),
      prop_refs: stringListFromMetadata(metadata, "prop_refs"),
      production_notes: stringListFromMetadata(metadata, "production_notes"),
      dialogue_lines: stringListFromMetadata(metadata, "dialogue_lines"),
      metadata,
    };
  });
  const persistedCodes = new Set(persistedScriptSegments.map((item) => item.script_segment_code).filter(Boolean));
  const persistedByCode = new Map(persistedScriptSegments.map((item) => [item.script_segment_code, item]));
  const draftMatchesPersisted = Boolean(
    draft.scriptSegments?.length === persistedScriptSegments.length
    && draft.scriptSegments.every((item) => item.script_segment_code && persistedCodes.has(item.script_segment_code))
  );
  const merged: BreakdownDraftData = {
    ...breakdownDraft(null),
    ...draft,
    scriptSegments: persistedScriptSegments.length && !draftMatchesPersisted
      ? persistedScriptSegments
      : draft.scriptSegments?.length
        ? draft.scriptSegments.map((item) => mergeScriptSegmentProductionFields(item, persistedByCode.get(item.script_segment_code)))
        : persistedScriptSegments,
    storyboards: draft.storyboards?.length ? draft.storyboards.map((item, index) => {
      const persisted = storyboards.find((storyboard) => (
        (item.storyboard_code && storyboard.storyboard_code === item.storyboard_code)
        || storyboard.order_num === (item.order_num || item.order_no || index + 1)
      ));
      return persisted ? {
        ...item,
        mirror_shots: persisted.mirror_shots?.length ? persisted.mirror_shots : item.mirror_shots,
      } : item;
    }) : storyboards.map((storyboard) => ({
      storyboard_code: storyboard.storyboard_code || undefined,
      title: storyboard.title || `分镜 ${storyboard.order_num}`,
      description: storyboard.description,
      camera: storyboard.camera || "",
      dialogue: storyboard.dialogue || "",
      characters: storyboard.characters || [],
      duration_seconds: storyboard.duration_seconds || undefined,
      shot_size: storyboard.shot_type || undefined,
      scene_code: storyboard.scene_code || undefined,
      scene_name: storyboard.scene_name || undefined,
      mirror_shots: storyboard.mirror_shots || [],
      order_num: storyboard.order_num,
      shot_no: String(storyboard.order_num),
      keyframe_code: "",
      video_code: "",
    })),
  };
  if (!merged.scriptSegments.length && project?.script_text) {
    merged.scriptSegments = [{
      episode_code: "EP01",
      order_no: 1,
      title: project.title,
      source_text: project.script_text,
      summary: project.script_text.slice(0, 240),
    }];
  }
  merged.storyboards = hydrateStoryboardSceneNames(merged.storyboards, merged.assets);
  return merged;
}

function mergeScriptSegmentProductionFields(
  draft: BreakdownDraftScriptSegment,
  persisted?: BreakdownDraftScriptSegment,
): BreakdownDraftScriptSegment {
  if (!persisted) return draft;
  return {
    ...persisted,
    ...draft,
    estimated_duration_seconds: draft.estimated_duration_seconds ?? persisted.estimated_duration_seconds ?? draft.duration_seconds,
    role_refs: draft.role_refs?.length ? draft.role_refs : draft.characters?.length ? draft.characters : persisted.role_refs,
    scene_refs: draft.scene_refs?.length ? draft.scene_refs : draft.locations?.length ? draft.locations : persisted.scene_refs,
    prop_refs: draft.prop_refs?.length ? draft.prop_refs : persisted.prop_refs,
    production_notes: draft.production_notes?.length ? draft.production_notes : persisted.production_notes,
    metadata: { ...(persisted.metadata || {}), ...(draft.metadata || {}) },
  };
}

function stringListFromMetadata(metadata: Record<string, unknown>, ...keys: string[]): string[] {
  for (const key of keys) {
    const value = metadata[key];
    if (Array.isArray(value)) return value.map(String).map((item) => item.trim()).filter(Boolean);
  }
  return [];
}

function numberFromMetadata(metadata: Record<string, unknown>, ...keys: string[]): number | undefined {
  for (const key of keys) {
    const raw = metadata[key];
    if (raw == null || raw === "") continue;
    const value = Number(raw);
    if (Number.isFinite(value) && value >= 0) return value;
  }
  return undefined;
}

export function textValue(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (Array.isArray(value)) return value.map(textValue).filter(Boolean).join(" / ");
  if (typeof value === "object") return JSON.stringify(value);
  return String(value).trim();
}

function reviewState(value: unknown): BreakdownDraftData["readingReviewState"] {
  if (!value || typeof value !== "object" || Array.isArray(value)) return { status: "needs_review" };
  const state = value as Record<string, unknown>;
  const status = ["needs_review", "pending_confirmation", "confirmed"].includes(String(state.status))
    ? state.status as BreakdownDraftData["readingReviewState"]["status"]
    : "needs_review";
  return {
    status,
    confirmed_revision_id: typeof state.confirmed_revision_id === "string" ? state.confirmed_revision_id : null,
    confirmation_mode: typeof state.confirmation_mode === "string" ? state.confirmation_mode : null,
  };
}

export function assetsMissingPromptDesign(draft: BreakdownDraftData) {
  const designs = draft.assetPromptDesigns.length ? draft.assetPromptDesigns : draft.costumeDesigns;
  const summaries = draft.assetPromptProcessingSummary.length ? draft.assetPromptProcessingSummary : draft.costumeProcessingSummary;
  return draft.assets.filter((asset) => {
    if (!assetParticipatesInLifecycleGate(asset)) return false;
    const status = assetLifecycleStatus(asset);
    if (status === "needs_completion" || status === "prompt_pending") return true;
    const design = designs.find((item) => promptRecordMatchesAsset(item, asset));
    if (design && !design.error && (design.prompt || design.input_hash || design.skill)) return false;
    const summary = summaries.find((item) => promptRecordMatchesAsset(item, asset));
    return String(summary?.processing_status || "").toLowerCase() !== "reused";
  });
}

export { promptRecordMatchesAsset };

export function taskTypeLabel(type: string) {
  const labels: Record<string, string> = {
    script_breakdown: "剧本拆解",
    asset_confirm: "资产确认",
    asset: "资产",
    audio: "音频",
    text_to_image: "文生图",
    image_to_video: "图生视频",
    video_generation: "视频",
    assembly: "剪辑",
    editing: "剪辑",
    final_output: "成片",
  };
  return labels[type] || type || "未分类";
}

export function labelAgentType(type: string) {
  const labels: Record<string, string> = {
    script_reading: "剧本围读",
    script_segmentation: "脚本段拆分",
    script_breakdown: "剧本围读与拆解",
    asset_extract: "资产清单生成",
    relation_check: "关系校验",
    content_compliance_review: "内容合理性审查",
    text_to_image_prompt: "文生图提示词",
    image_to_video_prompt: "图生视频提示词",
  };
  return labels[type] ?? type;
}

export function taskProgressByType(tasks: Task[]) {
  return productionProgressByEntity(tasks).filter((row) => row.total > 0);
}

export const taskPhaseMeta: Record<TaskPhaseKey, { label: string; desc: string; types: string[]; tone: string }> = {
  asset_production: {
    label: "资产生产",
    desc: "人物、场景、道具和音频资产任务",
    types: ["asset", "asset_confirm", "text_to_image", "audio"],
    tone: "mint",
  },
  storyboard_production: {
    label: "分镜生产",
    desc: "按分镜和场景聚合的图生视频任务",
    types: ["image_to_video", "video_generation"],
    tone: "sky",
  },
  editing: {
    label: "剪辑",
    desc: "按本集分镜顺序串联成片",
    types: ["assembly", "editing", "final_output"],
    tone: "sun",
  },
};

export function phaseForTask(task: Task): TaskPhaseKey {
  const type = task.task_type || "";
  if (taskPhaseMeta.storyboard_production.types.includes(type)) return "storyboard_production";
  if (taskPhaseMeta.editing.types.includes(type)) return "editing";
  return "asset_production";
}


export type TaskMediaKind = "image" | "video" | "audio";

export function taskMediaKind(task: Task, step?: string | null): TaskMediaKind {
  const normalizedStep = String(step || "").toLowerCase();
  const descriptor = `${task.task_type || ""} ${task.media_type || ""}`.toLowerCase();
  if (normalizedStep === "audio" || ["audio", "voice", "voice_profile", "music", "theme_music", "background_music"].some((token) => descriptor.includes(token))) return "audio";
  if (normalizedStep === "video" || ["image_to_video", "video_generation", "assembly", "final_output"].includes(task.task_type)) return "video";
  return "image";
}

export function groupTasksByPhase(tasks: Task[]) {
  return (Object.keys(taskPhaseMeta) as TaskPhaseKey[]).map((phase) => {
    const phaseTasks = tasks.filter((task) => phaseForTask(task) === phase);
    const statusGroups = phaseTasks.reduce<Record<string, Task[]>>((acc, task) => {
      const status = task.status || "todo";
      if (!acc[status]) acc[status] = [];
      acc[status].push(task);
      return acc;
    }, {});
    const typeGroups = phaseTasks.reduce<Record<string, Task[]>>((acc, task) => {
      const type = task.task_type || "unknown";
      if (!acc[type]) acc[type] = [];
      acc[type].push(task);
      return acc;
    }, {});
    return { phase, tasks: phaseTasks, statusGroups, typeGroups };
  });
}

export function statusTone(status?: string) {
  const normalized = (status || "").toLowerCase();
  if (["succeeded", "completed", "done", "success", "idle", "空闲"].includes(normalized)) return "mint";
  if (["failed", "error", "offline", "故障", "异常", "离线"].includes(normalized)) return "red";
  if (["running", "working", "in_progress", "processing", "工作中", "进行中"].includes(normalized)) return "sky";
  if (["queued", "pending", "waiting", "todo", "等待输入"].includes(normalized)) return "sun";
  return "sky";
}

export function statusLabel(status?: string) {
  const labels: Record<string, string> = {
    running: "工作中",
    working: "工作中",
    in_progress: "工作中",
    queued: "等待中",
    pending: "等待中",
    waiting: "等待输入",
    todo: "待处理",
    completed: "已完成",
    succeeded: "已成功",
    failed: "失败",
  };
  return labels[(status || "").toLowerCase()] || status || "未开始";
}

export function isOverdueTask(task: Task) {
  return task.status !== "completed" && Boolean(task.due_at) && new Date(task.due_at as string).getTime() < Date.now();
}

export function platformPromptVersions(prompt: string, taskType: string) {
  const isVideo = taskType === "image_to_video" || taskType === "video_generation";
  if (isVideo) {
    return [
      { platform: "Seedance", prompt: `${prompt}，镜头运动自然，主体一致，时长 3-5 秒，cinematic motion, smooth camera, consistent subject.` },
      { platform: "可灵", prompt: `${prompt}，保持首帧主体一致，镜头运动自然，时长 3-5 秒，smooth motion, stable composition, cinematic pacing.` },
    ];
  }
  return [
    { platform: "Seedream", prompt: `${prompt}，高清细节，角色一致性，电影感构图，cinematic composition, high detail, consistent character design.` },
    { platform: "libimage", prompt: `${prompt}，画面干净，主体明确，适合后续图生视频，clean composition, clear subject, high detail, no text watermark.` },
  ];
}

export function userDisplayName(user?: User | null) {
  return user?.display_name || user?.username || user?.phone || "用户";
}

export function userInitial(user?: User | null) {
  return userDisplayName(user).slice(0, 1);
}

export function formatFileSize(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

export function canManage(user?: User | null) {
  return user?.role === "director" || user?.role === "admin";
}

const backendStageMap: Record<string, ProjectStage> = {
  draft: "draft",
  reading: "draft",
  breakdown_review: "breakdown_review",
  asset_locking: "asset_locking",
  locked: "asset_locking",
  task_assignment: "task_assignment",
  production: "production",
  assembly: "production",
  completed: "completed",
  archived: "completed",
};

export function stageForProject(project?: Pick<Project, "status" | "current_stage" | "storyboard_count" | "task_count">, index = 0): ProjectStage {
  if (!project) return "draft";
  const explicitStage = String(project.current_stage || "").toLowerCase();
  if (backendStageMap[explicitStage]) return backendStageMap[explicitStage];
  const normalized = String(project.status || "").toLowerCase();
  if (backendStageMap[normalized]) return backendStageMap[normalized];
  if (normalized.includes("draft") || normalized.includes("草稿") || normalized.includes("new")) return "draft";
  if (normalized.includes("breakdown") || normalized.includes("拆解")) return "breakdown_review";
  if (normalized.includes("lock") || normalized.includes("锁定")) return "asset_locking";
  if (normalized.includes("assign") || normalized.includes("分配")) return "task_assignment";
  if (normalized.includes("complete") || normalized.includes("done") || normalized.includes("完成")) return "completed";
  if (normalized.includes("production") || normalized.includes("生产")) return "production";
  const stageOrder: ProjectStage[] = ["draft", "breakdown_review", "asset_locking", "task_assignment", "production", "completed"];
  return stageOrder[Math.min(index, stageOrder.length - 1)];
}

import React from "react";
import { ChevronRight, X } from "lucide-react";
import { createClientRequestId } from "../../clientId";
import { withEditedAssetInventory } from "../../assetNormalization";
import { Field, Panel } from "../../shared/components";
import { LongTextButton, LongTextTextarea } from "../../shared/LongText";
import type {
  BreakdownDraftAsset,
  BreakdownDraftData,
  BreakdownMirrorShot,
  BreakdownManualReviewItem,
  BreakdownDraftScriptSegment,
  BreakdownDraftStoryboard,
  TemporaryAssetProductionPayload,
} from "../../types";
import { assetDisplayLabel, assetReferenceDisplayLabel, assetsMissingPromptDesign, canonicalSceneCode, joinScriptOriginalText, mergeBreakdownDraft, sceneAssetDisplayLabel, shortAssetCode } from "../../utils";
import { ownerCharacterRelation, type OwnerCharacterOption } from "../../assetOwner";
import { AssetWorkbench, type AssetReviewCompletion } from "./AssetWorkbench";
import { AssetGlobalSuggestions } from "./AssetGlobalSuggestions";

type EditableBreakdownDraftProps = {
  draft: BreakdownDraftData;
  projectId?: string;
  stage: "assets" | "prompts" | "segmentation" | "breakdown";
  loading: boolean;
  readOnly?: boolean;
  scriptSegmentsConfirmed?: boolean;
  assetInventoryConfirmed?: boolean;
  promptsConfirmed?: boolean;
  episodeLocked?: boolean;
  onRequestTemporaryAsset?: (assetType: TemporaryAssetProductionPayload["asset_type"]) => void;
  onOpenTaskAssignment?: () => void;
  onSaveDraft: (draft: BreakdownDraftData) => Promise<boolean | void>;
  onConfirmScriptSegments: (draft: BreakdownDraftData) => Promise<void>;
  onLockEpisode: (episodeCode: string, draft: BreakdownDraftData) => Promise<void>;
  onBatchConfirmAssets?: () => void;
  batchConfirming?: boolean;
};

export function EditableBreakdownDraft({
  draft,
  projectId,
  stage,
  loading,
  readOnly = false,
  scriptSegmentsConfirmed = false,
  assetInventoryConfirmed = false,
  promptsConfirmed = true,
  episodeLocked = false,
  onRequestTemporaryAsset,
  onOpenTaskAssignment,
  onSaveDraft,
  onConfirmScriptSegments,
  onLockEpisode,
  onBatchConfirmAssets,
  batchConfirming = false,
}: EditableBreakdownDraftProps) {
  const [workingDraft, setWorkingDraft] = React.useState<BreakdownDraftData>(() => prepareDraft(draft, stage));
  const [episodeCode, setEpisodeCode] = React.useState("EP01");
  const [persistedSceneOptions, setPersistedSceneOptions] = React.useState<BreakdownDraftAsset[]>([]);
  const [editingScriptSegments, setEditingScriptSegments] = React.useState(false);
  const [editingScriptSegmentIndex, setEditingScriptSegmentIndex] = React.useState<number | null>(null);
  const [editingStoryboards, setEditingStoryboards] = React.useState(false);
  const [editingStoryboardIndex, setEditingStoryboardIndex] = React.useState<number | null>(null);
  const [addStoryboardOpen, setAddStoryboardOpen] = React.useState(false);
  const [addStoryboardPrefillSegment, setAddStoryboardPrefillSegment] = React.useState("");
  const [addStoryboardAfterIndex, setAddStoryboardAfterIndex] = React.useState<number | null>(null);
  const [pendingStoryboardScrollIndex, setPendingStoryboardScrollIndex] = React.useState<number | null>(null);
  const [promptSummaryStatus, setPromptSummaryStatus] = React.useState<"new" | "reused" | "error" | null>(null);
  const [addScriptSegmentOpen, setAddScriptSegmentOpen] = React.useState(false);
  const [lockingEpisode, setLockingEpisode] = React.useState(false);

  React.useEffect(() => {
    if (editingScriptSegments || editingStoryboards) return;
    setWorkingDraft(prepareDraft(draft, stage));
  }, [draft, stage, editingScriptSegments, editingStoryboards]);
  React.useEffect(() => {
    setPersistedSceneOptions(sceneOptionsFromDraft(draft));
  }, [stage, draft.assets]);

  React.useEffect(() => {
    if (pendingStoryboardScrollIndex == null) return undefined;
    const timer = window.setTimeout(() => {
      const node = document.querySelector(`[data-storyboard-index="${pendingStoryboardScrollIndex}"]`);
      if (node instanceof HTMLElement) {
        node.scrollIntoView({ behavior: "smooth", block: "center" });
        node.classList.add("is-just-added");
        window.setTimeout(() => node.classList.remove("is-just-added"), 1600);
      }
      setPendingStoryboardScrollIndex(null);
    }, 60);
    return () => window.clearTimeout(timer);
  }, [pendingStoryboardScrollIndex, workingDraft.storyboards.length]);

  function updateStoryboard(index: number, patch: Partial<BreakdownDraftStoryboard>) {
    setWorkingDraft((current) => ({
      ...current,
      storyboards: current.storyboards.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item),
    }));
  }

  function updateScriptSegment(index: number, patch: Partial<BreakdownDraftScriptSegment>) {
    setWorkingDraft((current) => ({
      ...current,
      scriptSegments: current.scriptSegments.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item),
    }));
  }

  function moveItem<T>(items: T[], index: number, direction: -1 | 1) {
    const target = index + direction;
    if (target < 0 || target >= items.length) return items;
    const next = [...items];
    [next[index], next[target]] = [next[target], next[index]];
    return next;
  }

  function moveStoryboardWithinSegment(items: BreakdownDraftStoryboard[], index: number, direction: -1 | 1) {
    const target = index + direction;
    if (target < 0 || target >= items.length) return items;

    // Prevent moving across segment boundaries
    const fromSeg = String(items[index].script_segment_code || "").trim();
    const toSeg = String(items[target].script_segment_code || "").trim();
    if (fromSeg !== toSeg) return items;

    const next = [...items];

    // Swap the two adjacent items in the flat array
    [next[index], next[target]] = [next[target], next[index]];

    // Renumber global order_num
    const renumbered = next.map((item, itemIndex) => ({ ...item, order_num: itemIndex + 1 }));

    // Also renumber segment_order_num for all segments
    const segmentGroups: Record<string, number> = {};
    const result = renumbered.map((item) => {
      const segCode = String(item.script_segment_code || "").trim();
      segmentGroups[segCode] = (segmentGroups[segCode] || 0) + 1;
      return { ...item, segment_order_num: segmentGroups[segCode] };
    });

    return result;
  }

  function openAddStoryboardModal(segmentCode = "", afterIndex: number | null = null) {
    setAddStoryboardPrefillSegment(segmentCode);
    setAddStoryboardAfterIndex(afterIndex);
    setAddStoryboardOpen(true);
  }

  function insertStoryboard(payload: {
    segmentCode: string;
    segmentTitle: string;
    title: string;
    insertMode: "segment_start" | "after_shot" | "segment_end";
    afterGlobalIndex: number | null;
  }) {
    const { draft, insertIndex } = insertStoryboardIntoDraft(workingDraft, payload);
    setWorkingDraft(draft);
    setEditingStoryboards(true);
    setPendingStoryboardScrollIndex(insertIndex);
    setAddStoryboardOpen(false);
    setAddStoryboardPrefillSegment("");
    setAddStoryboardAfterIndex(null);
    setEditingStoryboardIndex(insertIndex);
  }

  async function insertScriptSegment(payload: {
    insertMode: "list_start" | "after_segment" | "list_end";
    afterIndex: number | null;
    segment: BreakdownDraftScriptSegment;
  }): Promise<boolean> {
    let insertAt = workingDraft.scriptSegments.length;
    if (payload.insertMode === "list_start") insertAt = 0;
    else if (payload.insertMode === "after_segment" && payload.afterIndex != null) {
      insertAt = Math.min(workingDraft.scriptSegments.length, Math.max(0, payload.afterIndex + 1));
    }
    const list = [...workingDraft.scriptSegments];
    const safeAt = Math.min(list.length, Math.max(0, insertAt));
    list.splice(safeAt, 0, {
      ...payload.segment,
      episode_code: episodeCode,
      order_no: safeAt + 1,
    });
    const renumbered = list.map((item, index) => ({ ...item, order_no: index + 1 }));
    const persisted = {
      ...normalizedDraft,
      scriptSegments: renumbered,
    };
    const saved = await onSaveDraft(persisted);
    if (saved === false) return false;
    setWorkingDraft(prepareDraft(persisted, stage));
    setAddScriptSegmentOpen(false);
    setEditingScriptSegments(false);
    return true;
  }

  function addAsset(assetType: "character" | "scene" | "prop" = "character") {
    const clientAssetKey = createClientAssetKey();
    const draftCode = `DRAFT-${assetType === "character" ? "R" : assetType === "scene" ? "SC" : "P"}${clientAssetKey.replace(/\W/g, "").slice(-6).toUpperCase()}`;
    const attributes = assetType === "character" ? {
      character_type: null, visual_presence: "on_screen" as const, role: null, gender: null, age: null,
      appearance: null, core_requirement: null, personality: null, background: null,
      has_dialogue: false, dialogue_evidence: [], context_codes: [],
    } : assetType === "scene" ? {
      context_code: null, render_mode: null, scene_no: null, interior_exterior: null, time: null,
      location: null, atmosphere: null, narrative_function: null, spatial_zones: [], visual_goal: null,
      key_props: [], key_prop_codes: [], continuity_risks: [], episode_code: episodeCode,
    } : {
      level: null, prop_type: null, appearance: null, source: null, status_change: null,
      variant_states: [], usage_scene: null, scene_description: null, context_codes: [],
    };
    setWorkingDraft((current) => syncAssetGroups(
      current,
      [
        ...current.assets,
        {
          normalization_version: "AssetNormalization.v1",
          asset_type: assetType,
          client_asset_key: clientAssetKey,
          asset_code: draftCode,
          display_code: "待分配",
          display_label: "待命名",
          code_state: "candidate",
          name: "待命名",
          description: null,
          priority: "C",
          attributes,
          relations: { relationship_text: null, scene_links: [], owner_character: null, storyboard_codes: [], script_segment_codes: [], evidence: [], confidence: "unknown" },
          review_items: [],
          source: { source_kind: "human", agent_run_id: null, skill_name: null, raw_group: "assets", raw_index: current.assets.length },
          repairs: [],
          status: "needs_completion",
          metadata: { confirmation_status: "needs_completion" },
        },
      ],
    ));
  }

  const temporaryAssetMode = assetInventoryConfirmed && Boolean(onRequestTemporaryAsset) && ["segmentation", "breakdown"].includes(stage);
  function addAssetAction(assetType: "character" | "scene" | "prop") {
    if (temporaryAssetMode && onRequestTemporaryAsset) {
      onRequestTemporaryAsset(assetType);
      return;
    }
    addAsset(assetType);
  }

  async function completeManualReviews(values: AssetReviewCompletion[]) {
    const previousDraft = workingDraft;
    const completedDraft = values.reduce(applyAssetReviewCompletion, workingDraft);
    const persistedDraft = syncAssetGroups(completedDraft, completedDraft.assets);
    // Fix stale relations before saving to ensure AssetNormalization.v1 contract compliance
    const fixedDraft = fixStaleRelations(persistedDraft);
    // Preserve readingRevisionContext from original draft to ensure episode_id and script_version_id are included
    const draftWithContext = {
      ...fixedDraft,
      readingRevisionContext: draft.readingRevisionContext || fixedDraft.readingRevisionContext,
    };

    setWorkingDraft(draftWithContext);
    try {
      const saved = await onSaveDraft(draftWithContext);
      if (saved === false) setWorkingDraft((current) => current === draftWithContext ? previousDraft : current);
      return saved !== false;
    } catch (error) {
      setWorkingDraft((current) => current === draftWithContext ? previousDraft : current);
      throw error;
    }
  }

  async function updateAndSaveAsset(clientAssetKey: string, asset: BreakdownDraftAsset) {
    const previousDraft = workingDraft;
    const updatedDraft = syncAssetGroups(
      workingDraft,
      workingDraft.assets.map((item) => item.client_asset_key === clientAssetKey ? asset : item),
    );
    const persistedDraft = mergeBreakdownDraft(undefined, updatedDraft, [], []);
    // Fix stale relations before saving
    const fixedDraft = fixStaleRelations(persistedDraft);
    // Preserve readingRevisionContext to ensure episode_id and script_version_id are included
    const draftWithContext = {
      ...fixedDraft,
      readingRevisionContext: draft.readingRevisionContext || fixedDraft.readingRevisionContext,
    };

    setWorkingDraft(draftWithContext);
    const saved = await onSaveDraft(draftWithContext);
    if (saved === false) setWorkingDraft((current) => current === draftWithContext ? previousDraft : current);
    return saved !== false;
  }

  async function deleteAndSaveAsset(clientAssetKey: string) {
    const previousDraft = workingDraft;
    const updatedDraft = removeAssetFromDraft(workingDraft, clientAssetKey);
    const persistedDraft = mergeBreakdownDraft(undefined, updatedDraft, [], []);
    // Fix stale relations before saving
    const fixedDraft = fixStaleRelations(persistedDraft);
    // Preserve readingRevisionContext to ensure episode_id and script_version_id are included
    const draftWithContext = {
      ...fixedDraft,
      readingRevisionContext: draft.readingRevisionContext || fixedDraft.readingRevisionContext,
    };

    setWorkingDraft(draftWithContext);
    const saved = await onSaveDraft(draftWithContext);
    if (saved === false) setWorkingDraft((current) => current === draftWithContext ? previousDraft : current);
  }

  const mergedDraft = mergeBreakdownDraft(undefined, workingDraft, [], []);
  const assetStage = stage === "assets" || stage === "prompts";
  const normalizedDraft = mergedDraft;
  const storyboardGroups = groupStoryboardsBySegment(workingDraft.storyboards, workingDraft.scriptSegments);
  const storyboardValidationErrors = validateStoryboardDrafts(normalizedDraft.storyboards);
  const canLockEpisode = normalizedDraft.storyboards.length > 0 && storyboardValidationErrors.length === 0;
  const canConfirmScriptSegments = normalizedDraft.scriptSegments.length > 0;
  const segmentationReviewItems = workingDraft.manualReviewItems.filter((item) => String(item.source || "").includes("segmentation"));
  const storyboardReviewItems = workingDraft.manualReviewItems.filter((item) => !String(item.source || "").includes("segmentation"));
  const storyboardReminderScopes = splitStoryboardReminders(storyboardReviewItems, storyboardGroups);
  const extractedCharacters = workingDraft.assets.filter((asset) => asset.asset_type === "character").length;
  const extractedScenes = workingDraft.assets.filter((asset) => asset.asset_type === "scene").length;
  const extractedProps = workingDraft.assets.filter((asset) => asset.asset_type === "prop").length;
  const costumeProcessingRows = (
    workingDraft.assetPromptProcessingSummary.length
      ? workingDraft.assetPromptProcessingSummary
      : workingDraft.assetPromptDesigns.length
        ? workingDraft.assetPromptDesigns
        : workingDraft.costumeProcessingSummary.length
          ? workingDraft.costumeProcessingSummary
          : workingDraft.costumeDesigns
  ).map(costumeProcessingRow);
  const newCostumeContexts = costumeProcessingRows.filter((row) => row.status === "new").length;
  const reusedCostumeCharacters = costumeProcessingRows.filter((row) => row.status === "reused").length;
  const failedCostumeContexts = costumeProcessingRows.filter((row) => row.status === "error").length;
  const missingPromptAssets = stage === "prompts" ? assetsMissingPromptDesign(normalizedDraft) : [];
  const panelCopy = {
    assets: ["资产拆解与确认", ""],
    prompts: ["资产提示词与任务分发", ""],
    segmentation: ["脚本段调整", "核对脚本原文段的顺序、原文和摘要，确认后作为分镜预定稿的稳定输入。"],
    breakdown: ["分镜调整与定稿", "核对分镜顺序、画面、运镜和台词，确认后锁定本集并分发任务；补充镜头后可再次生成任务。"],
  }[stage];
  const taskAssignmentHint = promptsConfirmed && onOpenTaskAssignment ? (
    <button className="asset-distribution-hint" type="button" onClick={onOpenTaskAssignment}>
      <i aria-hidden="true" />
      资产已可并行制作，可前往任务分发
      <span aria-hidden="true">→</span>
    </button>
  ) : null;
  const panelAction = stage === "breakdown" ? taskAssignmentHint : null;


  async function saveScriptSegmentChanges() {
    const saved = await onSaveDraft(normalizedDraft);
    if (saved !== false) {
      setEditingScriptSegments(false);
      setEditingScriptSegmentIndex(null);
    }
  }

  function cancelScriptSegmentChanges() {
    setWorkingDraft(prepareDraft(draft, stage));
    setEditingScriptSegments(false);
    setEditingScriptSegmentIndex(null);
  }

  async function saveStoryboardChanges() {
    const saved = await onSaveDraft(normalizedDraft);
    if (saved !== false) {
      setEditingStoryboards(false);
      setEditingStoryboardIndex(null);
    }
  }

  function cancelStoryboardChanges() {
    setWorkingDraft(prepareDraft(draft, stage));
    setEditingStoryboards(false);
    setEditingStoryboardIndex(null);
  }

  async function lockOrRegenerateEpisode() {
    if (lockingEpisode) return;
    setLockingEpisode(true);
    try {
      await onLockEpisode(episodeCode || "EP01", normalizedDraft);
    } finally {
      setLockingEpisode(false);
    }
  }

  return (
    <Panel
      title={panelCopy[0]}
      subtitle={panelCopy[1]}
      action={stage === "assets" ? <AssetGlobalSuggestions items={workingDraft.globalAssetReviewItems} /> : panelAction}
    >
      {stage === "breakdown" ? <div className="edit-toolbar">
        <Field label="分集编号">
          <input disabled={readOnly} value={episodeCode} onChange={(event) => setEpisodeCode(event.target.value.toUpperCase())} />
        </Field>
        <div className="edit-toolbar-actions">
          <button
            className="btn primary"
            disabled={loading || readOnly || editingStoryboards || !canLockEpisode || lockingEpisode}
            type="button"
            onClick={() => void lockOrRegenerateEpisode()}
          >
            {lockingEpisode ? "正在生成任务..." : episodeLocked ? "重新生成分镜任务" : "锁定本集并分发任务"}
          </button>
          {!canLockEpisode ? <span className="muted">需先生成或补充分镜后才能分发任务。</span> : null}
          {editingStoryboards ? <span className="muted">请先保存分镜修改，再锁定本集。</span> : null}
        </div>
      </div> : null}

      {stage === "segmentation" ? <section className="editable-section">
        <div className="draft-detail-title">
          <strong>脚本原文段（合并显示）</strong>
          <span className="muted">按拆分顺序合并为连续文本，便于导演整体阅读；下方明细用于维护分镜关联。</span>
        </div>
        <LongTextTextarea
          className="script-original-preview"
          disabled
          title="脚本原文段合并全文"
          value={joinScriptOriginalText(workingDraft.scriptSegments)}
          placeholder="暂无脚本原文段。请先运行 Agent 拆解或手动新增。"
          onChange={() => undefined}
        />
      </section> : null}

      {stage === "segmentation" && (workingDraft.notes.length > 0 || segmentationReviewItems.length > 0) ? <section className="segment-agent-summary" aria-label="Agent 拆分说明">
        {workingDraft.notes.length > 0 ? <div><strong>拆分说明</strong><ul>{workingDraft.notes.map((note, index) => <li key={`${note}-${index}`}>{note}</li>)}</ul></div> : null}
        {segmentationReviewItems.length > 0 ? <div><strong>待确认事项</strong><ul>{segmentationReviewItems.map((item, index) => <li key={`${item.item}-${index}`}>{item.item}{item.detail ? `：${item.detail}` : ""}</li>)}</ul></div> : null}
      </section> : null}

      {stage === "segmentation" ? <section className="editable-section">
        <div className="draft-detail-title">
          <strong>脚本段明细</strong>
          <div className="inline-actions script-segment-actions">
            <button className="btn script-segment-action-button" disabled={loading || readOnly} type="button" onClick={() => setAddScriptSegmentOpen(true)}>+ 新增脚本原文段</button>
            {editingScriptSegments ? <button className="btn script-segment-action-button" disabled={loading || readOnly} type="button" onClick={cancelScriptSegmentChanges}>取消编辑</button> : null}
            <button className={`btn script-segment-action-button${editingScriptSegments ? " primary" : ""}`} disabled={loading || readOnly} type="button" onClick={() => editingScriptSegments ? void saveScriptSegmentChanges() : setEditingScriptSegments(true)}>{editingScriptSegments ? "保存修改" : "编辑"}</button>
          </div>
        </div>
        {workingDraft.scriptSegments.length ? <div className="script-segment-table-wrap">
          <table className={`script-segment-table${editingScriptSegments ? " is-editing" : ""}`}>
            <thead><tr><th>顺序</th><th>编号</th><th>标题</th><th>原文</th><th>摘要</th><th>剧情功能</th><th>情绪 / 节奏</th><th>视角</th><th>生产信息</th><th>操作</th></tr></thead>
            <tbody>{workingDraft.scriptSegments.map((segment, index) => (
              <tr key={`${segment.script_segment_code || "segment"}-${index}`}>
                <td>{segment.order_no || index + 1}</td>
                <td><strong>{segment.script_segment_code || `J${String(index + 1).padStart(3, "0")}`}</strong></td>
                <td><SegmentText value={segment.title} /></td>
                <td><SegmentClampedText title={`${segment.script_segment_code || `脚本段 ${index + 1}`} 原文全文`} value={segment.source_text} /></td>
                <td><SegmentClampedText title={`${segment.script_segment_code || `脚本段 ${index + 1}`} 摘要全文`} value={segment.summary} /></td>
                <td><SegmentText value={normalizeSegmentValue(segment.story_function, STORY_FUNCTION_LABELS)} /></td>
                <td>
                  <div className="segment-emotion-rhythm">
                    <SegmentText value={normalizeSegmentText(segment.dominant_emotion, EMOTION_LABELS)} />
                    <hr />
                    <SegmentText value={normalizeSegmentValue(segment.rhythm, RHYTHM_LABELS)} />
                  </div>
                </td>
                <td><SegmentText value={normalizeSegmentText(segment.viewpoint, VIEWPOINT_LABELS)} /></td>
                <td><SegmentProductionInfo assets={scriptSegmentReferenceAssets(workingDraft)} editing={false} segment={segment} onChange={(patch) => updateScriptSegment(index, patch)} /></td>
                <td>
                  {editingScriptSegments ? (
                    <div className="storyboard-row-actions">
                      <button className="btn storyboard-row-action-btn" disabled={loading || readOnly} type="button" onClick={() => setEditingScriptSegmentIndex(index)}>编辑</button>
                      <button className="btn storyboard-row-action-btn icon-btn" disabled={index === 0} title="上移" type="button" onClick={() => setWorkingDraft((current) => ({ ...current, scriptSegments: moveItem<BreakdownDraftScriptSegment>(current.scriptSegments, index, -1).map((item, itemIndex) => ({ ...item, order_no: itemIndex + 1 })) }))}>↑</button>
                      <button className="btn storyboard-row-action-btn icon-btn" disabled={index === workingDraft.scriptSegments.length - 1} title="下移" type="button" onClick={() => setWorkingDraft((current) => ({ ...current, scriptSegments: moveItem<BreakdownDraftScriptSegment>(current.scriptSegments, index, 1).map((item, itemIndex) => ({ ...item, order_no: itemIndex + 1 })) }))}>↓</button>
                      <button className="btn storyboard-row-action-btn danger" type="button" onClick={() => setWorkingDraft((current) => ({ ...current, scriptSegments: current.scriptSegments.filter((_, itemIndex) => itemIndex !== index).map((item, itemIndex) => ({ ...item, order_no: itemIndex + 1 })) }))}>删除</button>
                    </div>
                  ) : (
                    <span className="storyboard-ops-placeholder">-</span>
                  )}
                </td>
              </tr>
            ))}</tbody>
          </table>
        </div> : <div className="empty-hint">暂无脚本原文段，可手动新增或先运行 Agent 拆解。</div>}
        {addScriptSegmentOpen ? (
          <AddScriptSegmentModal
            episodeCode={episodeCode}
            segments={workingDraft.scriptSegments}
            onClose={() => setAddScriptSegmentOpen(false)}
            onConfirm={insertScriptSegment}
          />
        ) : null}
        {editingScriptSegmentIndex !== null ? (
          <EditScriptSegmentModal
            segment={workingDraft.scriptSegments[editingScriptSegmentIndex]!}
            index={editingScriptSegmentIndex}
            assets={scriptSegmentReferenceAssets(workingDraft)}
            onClose={() => setEditingScriptSegmentIndex(null)}
            onSave={(patch) => updateScriptSegment(editingScriptSegmentIndex, patch)}
          />
        ) : null}
      </section> : null}

      {stage === "breakdown" ? <section className="editable-section">
        <div className="draft-detail-title">
          <strong>分镜表</strong>
          <div className="inline-actions storyboard-actions">
            {storyboardReminderScopes.global.length ? <StoryboardReminderButton items={storyboardReminderScopes.global} label={`全局 Agent提醒 ${storyboardReminderScopes.global.length}`} title="分镜表全局 Agent 提醒" /> : null}
            <button className="btn storyboard-action-button" disabled={loading || readOnly} type="button" onClick={() => openAddStoryboardModal()}>新增分镜</button>
            {editingStoryboards ? <button className="btn storyboard-action-button" disabled={loading || readOnly} type="button" onClick={cancelStoryboardChanges}>取消编辑</button> : null}
            <button className={`btn storyboard-action-button${editingStoryboards ? " primary" : ""}`} disabled={loading || readOnly || (editingStoryboards && storyboardValidationErrors.length > 0)} type="button" onClick={() => editingStoryboards ? void saveStoryboardChanges() : setEditingStoryboards(true)}>{editingStoryboards ? "保存修改" : "编辑"}</button>
          </div>
        </div>
        {editingStoryboards && storyboardValidationErrors.length ? <div className="notice error" role="alert">{storyboardValidationErrors.slice(0, 5).map((message) => <span key={message}>{message}</span>)}</div> : null}
        {workingDraft.storyboards.length ? <div className="storyboard-groups">
          {storyboardGroups.map((group) => (
            <section className="storyboard-segment-block" key={group.code}>
              <div className="storyboard-segment-summary">
                <div><strong>{group.code}</strong><span>{group.title}</span></div>
                <div className="storyboard-segment-metrics">
                  <span>计划 {group.plannedDuration ? `${group.plannedDuration} 秒` : "未设置"}</span>
                  <span>{group.items.length} 个分镜</span>
                  <span>实际 <em className={group.plannedDuration && group.actualDuration !== group.plannedDuration ? "duration-mismatch-num" : "duration-match-num"}>{group.actualDuration}</em> 秒</span>
                  {(storyboardReminderScopes.bySegment.get(group.code) || []).length ? <StoryboardReminderButton items={storyboardReminderScopes.bySegment.get(group.code) || []} label={`Agent提醒 ${(storyboardReminderScopes.bySegment.get(group.code) || []).length}`} title={`${group.code} Agent 提醒`} /> : null}
                  {group.sourceText ? <LongTextButton label="查看脚本段" title={`${group.code} 脚本原文`} text={group.sourceText} /> : null}
                </div>
              </div>
              <div className="storyboard-segment-scroll">
                <table className={`storyboard-table${editingStoryboards ? " is-editing" : ""}`}>
                  <thead><tr><th>段内序</th><th>分镜编号</th><th>场景</th><th>镜头参数</th><th>角色</th><th>画面内容</th><th>镜头执行</th><th>台词</th><th>操作</th></tr></thead>
                  <tbody>
                    {group.items.map(({ storyboard, index }, localIndex) => (
                      <tr
                        key={`${storyboard.storyboard_code || storyboard.title || "storyboard"}-${index}`}
                        data-storyboard-index={index}
                      >
                        <td>{storyboard.segment_order_num || localIndex + 1}</td>
                        <td><div className="storyboard-identity"><strong>{storyboard.storyboard_code || `F${String(index + 1).padStart(3, "0")}`}</strong><span>{storyboard.title || `分镜 ${index + 1}`}</span></div></td>
                        <td><SegmentText value={storyboardSceneLabel(storyboard, scriptSegmentReferenceAssets(workingDraft))} /></td>
                        <td><StoryboardParameterLayers storyboard={storyboard} /></td>
                        <td><StoryboardCharacterLayers assets={scriptSegmentReferenceAssets(workingDraft)} storyboard={storyboard} /></td>
                        <td><StoryboardTextLayers field="description" label="画面内容" storyboard={storyboard} /></td>
                        <td><StoryboardTextLayers field="camera" label="镜头执行" storyboard={storyboard} /></td>
                        <td><StoryboardDialogueLayers storyboard={storyboard} /></td>
                        <td>
                          {editingStoryboards ? (
                            <div className="storyboard-row-actions">
                              <button className="btn storyboard-row-action-btn" disabled={loading || readOnly} type="button" onClick={() => setEditingStoryboardIndex(index)}>编辑</button>
                              <button className="btn storyboard-row-action-btn icon-btn" disabled={index === 0} title="上移" type="button" onClick={() => setWorkingDraft((current) => ({ ...current, storyboards: moveStoryboardWithinSegment(current.storyboards, index, -1) }))}>↑</button>
                              <button className="btn storyboard-row-action-btn icon-btn" disabled={index === workingDraft.storyboards.length - 1} title="下移" type="button" onClick={() => setWorkingDraft((current) => ({ ...current, storyboards: moveStoryboardWithinSegment(current.storyboards, index, 1) }))}>↓</button>
                              <button className="btn storyboard-row-action-btn danger" type="button" onClick={() => setWorkingDraft((current) => {
                                const filtered = current.storyboards.filter((_, itemIndex) => itemIndex !== index);
                                // Renumber both order_num and segment_order_num after deletion
                                const segmentGroups: Record<string, number> = {};
                                const result = filtered.map((item, itemIndex) => {
                                  const segCode = String(item.script_segment_code || "").trim();
                                  segmentGroups[segCode] = (segmentGroups[segCode] || 0) + 1;
                                  return { ...item, order_num: itemIndex + 1, segment_order_num: segmentGroups[segCode] };
                                });
                                return { ...current, storyboards: result };
                              })}>删除</button>
                              {(storyboardReminderScopes.byStoryboard.get(storyboard.storyboard_code || "") || []).length ? <StoryboardReminderButton items={storyboardReminderScopes.byStoryboard.get(storyboard.storyboard_code || "") || []} label={`提醒 ${(storyboardReminderScopes.byStoryboard.get(storyboard.storyboard_code || "") || []).length}`} title={`${storyboard.storyboard_code || "当前分镜"} Agent 提醒`} /> : null}
                            </div>
                          ) : (
                            <span className="storyboard-ops-placeholder">-</span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          ))}
        </div> : <p className="draft-empty-note">分镜拆解尚未进行，完成脚本段确认后即可开始。也可先点击「新增分镜」手动创建。</p>}
        {addStoryboardOpen ? (
          <AddStoryboardModal
            groups={storyboardGroups}
            prefillAfterIndex={addStoryboardAfterIndex}
            prefillSegmentCode={addStoryboardPrefillSegment}
            scriptSegments={workingDraft.scriptSegments}
            storyboards={workingDraft.storyboards}
            onClose={() => {
              setAddStoryboardOpen(false);
              setAddStoryboardPrefillSegment("");
              setAddStoryboardAfterIndex(null);
            }}
            onConfirm={insertStoryboard}
          />
        ) : null}
        {editingStoryboardIndex !== null && workingDraft.storyboards[editingStoryboardIndex] ? (
          <EditStoryboardModal
            index={editingStoryboardIndex}
            scriptSegments={workingDraft.scriptSegments}
            storyboard={workingDraft.storyboards[editingStoryboardIndex]}
            onClose={() => setEditingStoryboardIndex(null)}
            onSave={(patch) => {
              updateStoryboard(editingStoryboardIndex, patch);
              setEditingStoryboards(true);
              setEditingStoryboardIndex(null);
            }}
          />
        ) : null}
      </section> : null}

      {assetStage ? <section className="editable-section">
        {stage === "prompts" ? <div className="asset-prompt-flow" aria-label="资产提示词执行流程">
          <div className="asset-prompt-flow__head">
            <strong>处理流程</strong>
            <span>资产拆分完成后，为不同资产生成专用 Prompt</span>
          </div>
          <div className="asset-prompt-flow__steps">
            <article className="asset-prompt-flow__step complete">
              <span className="asset-prompt-flow__index">1</span>
              <div className="asset-prompt-flow__content">
                <div className="asset-prompt-flow__title"><strong>资产拆分</strong><em className="asset-prompt-flow__status">已完成</em></div>
                <p>从剧本中识别各种资产</p>
                <small className="asset-prompt-flow__skill">asset-extract</small>
              </div>
              <b className="asset-prompt-flow__metric">共 {extractedCharacters + extractedScenes + extractedProps} 项资产</b>
            </article>
            <div className="asset-prompt-flow__connector" aria-hidden="true" />
            <article className={`asset-prompt-flow__step ${costumeProcessingRows.length ? missingPromptAssets.length ? "running" : "complete" : "waiting"}`}>
              <span className="asset-prompt-flow__index">2</span>
              <div className="asset-prompt-flow__content">
                <div className="asset-prompt-flow__title"><strong>生成专用资产 Prompt</strong><em className="asset-prompt-flow__status">{costumeProcessingRows.length ? missingPromptAssets.length ? "处理中" : "已完成" : "等待生成"}</em></div>
                <p>按视觉资产生成提示词</p>
                <small className="asset-prompt-flow__skill">character / scene / prop design skills</small>
              </div>
              <b className="asset-prompt-flow__metric">新增 {newCostumeContexts} · 复用 {reusedCostumeCharacters} · 错误 {failedCostumeContexts}</b>
            </article>
          </div>
          <div className="asset-prompt-results">
            <div className="asset-prompt-results__head">
              <strong>处理结果</strong>
              <span>共处理 {costumeProcessingRows.length} 项</span>
            </div>
            <div className="asset-prompt-results__grid">
              <button className="asset-prompt-result-card new" disabled={!newCostumeContexts} type="button" onClick={() => setPromptSummaryStatus("new")}>
                <strong>{newCostumeContexts}</strong>
                <span>新生成 Prompt</span>
                <em>{newCostumeContexts ? "查看明细 →" : "暂无新增"}</em>
              </button>
              <button className="asset-prompt-result-card reused" disabled={!reusedCostumeCharacters} type="button" onClick={() => setPromptSummaryStatus("reused")}>
                <strong>{reusedCostumeCharacters}</strong>
                <span>复用已有</span>
                <em>{reusedCostumeCharacters ? "查看明细 →" : "暂无复用"}</em>
              </button>
              <button className="asset-prompt-result-card error" disabled={!failedCostumeContexts} type="button" onClick={() => setPromptSummaryStatus("error")}>
                <strong>{failedCostumeContexts}</strong>
                <span>生成失败</span>
                <em>{failedCostumeContexts ? "查看明细 →" : "无错误"}</em>
              </button>
            </div>
          </div>
        </div> : null}
        <div className="draft-detail-title">
          <strong>专用资产拆分与定装结果</strong>
        </div>
        <AssetWorkbench
          assets={workingDraft.assets}
          inventoryConfirmed={workingDraft.assetReviewState.status === "confirmed"}
          projectId={projectId}
          episodeCode={String((draft.readingRevisionContext || {}).episode_code || "")}
          costumeDesigns={workingDraft.assetPromptDesigns.length ? workingDraft.assetPromptDesigns : workingDraft.costumeDesigns}
          sceneOptions={persistedSceneOptions}
          mode={stage === "assets" ? "edit" : "output"}
          readOnly={readOnly || stage === "prompts"}
          onUpdateAsset={updateAndSaveAsset}
          onDeleteAsset={(clientAssetKey) => deleteAndSaveAsset(clientAssetKey)}
          onAddAsset={(type) => { if (type !== "music") addAssetAction(type); }}
          onCompleteReviews={completeManualReviews}
        />
        {stage === "prompts" && promptSummaryStatus ? <PromptProcessingSummaryModal rows={costumeProcessingRows.filter((row) => row.status === promptSummaryStatus)} status={promptSummaryStatus} onClose={() => setPromptSummaryStatus(null)} /> : null}
      </section> : null}
    </Panel>
  );
}

const STORY_FUNCTION_LABELS: Record<string, string> = {
  hook: "开场钩子",
  setup: "铺垫设定",
  inciting_incident: "诱发事件",
  rising_action: "冲突升级",
  transition: "情节过渡",
  turning_point: "剧情转折",
  climax: "高潮",
  resolution: "收束解决",
  payoff: "情节回收",
  exposition: "信息交代",
};

const RHYTHM_LABELS: Record<string, string> = {
  fast: "快",
  medium: "中等",
  moderate: "中等",
  slow: "慢",
  accelerating: "加速",
  decelerating: "放缓",
};

const EMOTION_LABELS: Record<string, string> = {
  neutral: "平静",
  tension: "紧张",
  tense: "紧张",
  suspense: "悬疑",
  fear: "恐惧",
  anger: "愤怒",
  sadness: "悲伤",
  joy: "喜悦",
  hope: "希望",
  despair: "绝望",
};

const VIEWPOINT_LABELS: Record<string, string> = {
  objective: "客观视角",
  omniscient: "全知视角",
  first_person: "第一人称视角",
  third_person: "第三人称视角",
  protagonist: "主角视角",
};

const SHOT_SIZE_LABELS: Record<string, string> = {
  ECU: "极近特写",
  CU: "特写",
  MCU: "中近景",
  MS: "中景",
  MFS: "中全景",
  MLS: "中全景",
  FS: "全景",
  LS: "全景",
  WS: "远景",
  EWS: "大远景",
  ELS: "大远景",
};

function normalizeStoryboardShotSize(value?: string | null) {
  const text = String(value || "").trim().replace(/->|-/g, "→");
  if (!text) return "未明确";
  return text.split("→").map((part) => {
    const normalized = part.trim();
    return SHOT_SIZE_LABELS[normalized.toUpperCase()] || normalized;
  }).filter(Boolean).join(" → ");
}

function normalizeStoryboardCamera(value?: string | null) {
  let text = String(value || "").trim();
  for (const [code, label] of Object.entries(SHOT_SIZE_LABELS)) {
    text = text.replace(new RegExp(`\\b${code}\\b`, "gi"), label);
  }
  return text;
}

function SegmentText({ value }: { value?: string | null }) {
  return <span className="segment-readonly-text">{String(value || "-")}</span>;
}

function SegmentClampedText({ title, value }: { title: string; value?: string | null }) {
  const text = String(value || "").trim();
  const contentRef = React.useRef<HTMLSpanElement>(null);
  const [truncated, setTruncated] = React.useState(false);
  React.useLayoutEffect(() => {
    const content = contentRef.current;
    if (!content) return undefined;
    const measure = () => setTruncated(content.scrollHeight > content.clientHeight + 1);
    measure();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    observer?.observe(content);
    return () => observer?.disconnect();
  }, [text]);
  if (!text) return <SegmentText />;
  return <div className="segment-clamped-text">
    <span ref={contentRef}>{text}</span>
    {truncated ? <LongTextButton title={title} text={text} /> : null}
  </div>;
}

function StoryFunctionSelect({ value, onChange }: { value?: string | null; onChange: (value: string) => void }) {
  return <NormalizedSelect labels={STORY_FUNCTION_LABELS} placeholder="请选择剧情功能" value={value} onChange={onChange} />;
}

function RhythmSelect({ value, onChange }: { value?: string | null; onChange: (value: string) => void }) {
  return <NormalizedSelect labels={RHYTHM_LABELS} placeholder="请选择节奏" value={value} onChange={onChange} />;
}

function NormalizedSelect({ labels, placeholder, value, onChange }: { labels: Record<string, string>; placeholder: string; value?: string | null; onChange: (value: string) => void }) {
  const current = String(value || "").trim();
  const normalizedKey = current.toLowerCase().replace(/[\s-]+/g, "_");
  const isKnown = Boolean(labels[normalizedKey]);
  return <select value={isKnown ? normalizedKey : current} onChange={(event) => onChange(event.target.value)}>
    <option value="">{placeholder}</option>
    {!isKnown && current ? <option value={current}>{current}</option> : null}
    {Object.entries(labels).map(([key, label]) => <option key={key} value={key}>{label}</option>)}
  </select>;
}

function SegmentProductionInfo({
  assets,
  editing,
  segment,
  onChange,
}: {
  assets: BreakdownDraftAsset[];
  editing: boolean;
  segment: BreakdownDraftScriptSegment;
  onChange: (patch: Partial<BreakdownDraftScriptSegment>) => void;
}) {
  const [open, setOpen] = React.useState(false);
  const duration = segment.estimated_duration_seconds ?? segment.duration_seconds;
  const roleRefs = segment.role_refs?.length ? segment.role_refs : segment.characters || [];
  const sceneRefs = segment.scene_refs?.length ? segment.scene_refs : segment.locations || [];
  const propRefs = segment.prop_refs || [];
  const notes = segment.production_notes || [];
  const hasDetails = duration != null || roleRefs.length > 0 || sceneRefs.length > 0 || propRefs.length > 0 || notes.length > 0;
  React.useEffect(() => {
    if (!open) return undefined;
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);
  return <>
    <button className="text-link-btn segment-production-link" type="button" onClick={() => setOpen(true)}>{hasDetails ? "查看详情" : "暂无信息"}</button>
    {open ? <div className="modal-backdrop episode-script-modal-backdrop" role="presentation" onClick={() => setOpen(false)}>
      <section className="long-text-modal segment-production-modal episode-script-modal" role="dialog" aria-modal="true" aria-label="脚本段生产信息" onClick={(event) => event.stopPropagation()}>
        <div className="modal-head episode-script-modal__head"><div><h3>脚本段生产信息</h3><p>{segment.script_segment_code || segment.title || "未编号脚本段"}</p></div><button className="episode-script-modal__close" type="button" aria-label="关闭" title="关闭" onClick={() => setOpen(false)}><X size={18} strokeWidth={1.8} /></button></div>
        <div className="segment-production-modal-body">
          {editing ? <div className="segment-production-edit-grid">
            <label><span>预计时长（秒）</span><input min={0} type="number" value={duration ?? ""} onChange={(event) => onChange({ estimated_duration_seconds: event.target.value === "" ? null : Number(event.target.value) })} /></label>
            <label><span>关联角色</span><input placeholder="R001, R002" value={roleRefs.join(", ")} onChange={(event) => onChange({ role_refs: parseReferenceList(event.target.value) })} /><SegmentReferenceList assets={assets} codes={roleRefs} /></label>
            <label><span>关联场景</span><input placeholder="SC001, SC002" value={sceneRefs.join(", ")} onChange={(event) => onChange({ scene_refs: parseReferenceList(event.target.value) })} /><SegmentReferenceList assets={assets} codes={sceneRefs} kind="scene" /></label>
            <label><span>关联道具</span><input placeholder="P001, P002" value={propRefs.join(", ")} onChange={(event) => onChange({ prop_refs: parseReferenceList(event.target.value) })} /><SegmentReferenceList assets={assets} codes={propRefs} /></label>
            <label className="production-notes-field"><span>生产备注</span><textarea value={notes.join("\n")} onChange={(event) => onChange({ production_notes: event.target.value.split(/\r?\n/).map((item) => item.trim()).filter(Boolean) })} /></label>
          </div> : hasDetails ? <div className="segment-production-values">
            <div><strong>预计时长</strong><span>{duration != null ? `${duration} 秒` : "-"}</span></div>
            <div><strong>关联角色</strong><SegmentReferenceList assets={assets} codes={roleRefs} /></div>
            <div><strong>关联场景</strong><SegmentReferenceList assets={assets} codes={sceneRefs} kind="scene" /></div>
            <div><strong>关联道具</strong><SegmentReferenceList assets={assets} codes={propRefs} /></div>
            <div className="production-notes-value"><strong>生产备注</strong><span>{notes.join("；") || "-"}</span></div>
          </div> : <div className="empty-hint">暂无生产信息。</div>}
        </div>
      </section>
    </div> : null}
  </>;
}

function SegmentReferenceList({ assets, codes, kind }: { assets: BreakdownDraftAsset[]; codes: string[]; kind?: "scene" }) {
  if (!codes.length) return <span className="segment-reference-empty">-</span>;
  const names = new Map(assets.map((asset) => [String(asset.asset_code || "").trim().toUpperCase(), asset]));
  return <div className="segment-reference-list">{codes.map((code, index) => {
    const normalizedCode = String(code).trim().toUpperCase();
    const asset = names.get(normalizedCode) || (kind === "scene"
      ? assets.find((candidate) => canonicalSceneCode(candidate.asset_code) === canonicalSceneCode(code))
      : undefined);
    const assetType = kind === "scene" ? "scene" : asset?.asset_type === "character" ? "character" : "prop";
    const label = kind === "scene"
      ? asset?.display_label || sceneAssetDisplayLabel(undefined, String(code))
      : assetReferenceDisplayLabel(asset?.asset_code || code, assets, assetType);
    return <span className="segment-reference-item" key={`${normalizedCode}-${index}`}><b>{label}</b></span>;
  })}</div>;
}

function scriptSegmentReferenceAssets(draft: BreakdownDraftData) {
  const byCode = new Map<string, BreakdownDraftAsset>();
  for (const asset of draft.assets) {
    const code = String(asset.asset_code || "").trim().toUpperCase();
    if (code && !byCode.has(code)) byCode.set(code, asset);
  }
  return Array.from(byCode.values());
}

function storyFunctionOptions(currentValue: string) {
  const entries = Object.entries(STORY_FUNCTION_LABELS);
  const options = entries.map(([value, label]) => ({ value, label }));
  if (currentValue && !STORY_FUNCTION_LABELS[currentValue]) {
    options.unshift({ value: currentValue, label: currentValue });
  }
  return options;
}

function rhythmOptions(currentValue: string) {
  const entries = Object.entries(RHYTHM_LABELS);
  const options = entries.map(([value, label]) => ({ value, label }));
  if (currentValue && !RHYTHM_LABELS[currentValue]) {
    options.unshift({ value: currentValue, label: currentValue });
  }
  return options;
}

function ModalListSelect({
  options,
  placeholder,
  value,
  onChange,
}: {
  options: Array<{ value: string; label: string }>;
  placeholder: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const [open, setOpen] = React.useState(false);
  const [openUp, setOpenUp] = React.useState(false);
  const rootRef = React.useRef<HTMLDivElement>(null);
  const selected = options.find((item) => item.value === value);

  React.useEffect(() => {
    function onPointerDown(event: MouseEvent) {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    }
    window.addEventListener("mousedown", onPointerDown);
    return () => window.removeEventListener("mousedown", onPointerDown);
  }, []);

  function toggleOpen() {
    if (open) {
      setOpen(false);
      return;
    }
    const root = rootRef.current;
    if (root) {
      const triggerRect = root.getBoundingClientRect();
      setOpenUp(window.innerHeight - triggerRect.bottom < 260);
    }
    setOpen(true);
  }

  return (
    <div className={`project-settings__multi modal-list-select ${open ? "is-open" : ""} ${openUp ? "is-up" : ""}`} ref={rootRef}>
      <button type="button" className="project-settings__multi-trigger" onClick={toggleOpen}>
        <span className={selected ? "" : "is-placeholder"}>{selected?.label || placeholder}</span>
        <ChevronRight size={14} strokeWidth={2} className="project-settings__multi-caret" />
      </button>
      {open ? (
        <div className="project-settings__multi-menu" role="listbox" aria-label={placeholder}>
          {options.map((option) => {
            const checked = option.value === value;
            return (
              <button
                key={option.value}
                type="button"
                role="option"
                aria-selected={checked}
                className={`project-settings__multi-option ${checked ? "is-checked" : ""}`}
                onClick={() => {
                  onChange(option.value);
                  setOpen(false);
                }}
              >
                <span>{option.label}</span>
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function AddScriptSegmentModal({
  episodeCode,
  segments,
  onClose,
  onConfirm,
}: {
  episodeCode: string;
  segments: BreakdownDraftScriptSegment[];
  onClose: () => void;
  onConfirm: (payload: {
    insertMode: "list_start" | "after_segment" | "list_end";
    afterIndex: number | null;
    segment: BreakdownDraftScriptSegment;
  }) => Promise<boolean>;
}) {
  const [title, setTitle] = React.useState(`脚本原文段 ${segments.length + 1}`);
  const [sourceText, setSourceText] = React.useState("");
  const [summary, setSummary] = React.useState("");
  const [storyFunction, setStoryFunction] = React.useState("");
  const [dominantEmotion, setDominantEmotion] = React.useState("");
  const [rhythm, setRhythm] = React.useState("");
  const [viewpoint, setViewpoint] = React.useState("");
  const [insertMode, setInsertMode] = React.useState<"list_start" | "after_segment" | "list_end">("list_end");
  const [afterIndex, setAfterIndex] = React.useState<number | null>(segments.length ? segments.length - 1 : null);
  const [submitting, setSubmitting] = React.useState(false);

  React.useEffect(() => {
    if (insertMode !== "after_segment") return;
    if (!segments.length) {
      setAfterIndex(null);
      setInsertMode("list_end");
      return;
    }
    if (afterIndex == null || afterIndex < 0 || afterIndex >= segments.length) {
      setAfterIndex(segments.length - 1);
    }
  }, [afterIndex, insertMode, segments.length]);

  React.useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !submitting) onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose, submitting]);

  const positionOptions: Array<{
    mode: "list_start" | "after_segment" | "list_end";
    label: string;
    disabled?: boolean;
  }> = [
    { mode: "list_end", label: "列表末尾" },
    { mode: "after_segment", label: "指定脚本段之后", disabled: !segments.length },
    { mode: "list_start", label: "列表最前" },
  ];

  const afterLabel = afterIndex == null
    ? ""
    : (() => {
      const segment = segments[afterIndex];
      if (!segment) return "";
      return segment.script_segment_code || segment.title || `脚本段 ${afterIndex + 1}`;
    })();

  const hint = insertMode === "list_start"
    ? "将插入到列表最前，顺序自动重排。"
    : insertMode === "list_end"
      ? "将追加到列表末尾。"
      : `将插入到 ${afterLabel || "指定脚本段"} 之后，后续顺序自动后移。`;

  return <div className="modal-backdrop add-storyboard-backdrop" role="presentation" onClick={() => { if (!submitting) onClose(); }}>
    <section className="add-storyboard-modal edit-storyboard-modal add-script-segment-modal" role="dialog" aria-modal="true" aria-label="新增脚本原文段" onClick={(event) => event.stopPropagation()}>
      <header className="add-storyboard-modal__head">
        <div>
          <h3>新增脚本原文段</h3>
          <p>填写脚本段标题与内容信息，确认新增后返回表格查看状态；需要时可再进入编辑。</p>
        </div>
        <button className="add-storyboard-modal__close" type="button" onClick={onClose} aria-label="关闭">
          <X size={18} strokeWidth={1.8} />
        </button>
      </header>

      <div className="add-storyboard-modal__body edit-storyboard-modal__body">
        <div className="edit-storyboard-grid">
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>标题</span>
            <div className="add-storyboard-field__control">
              <input value={title} onChange={(event) => setTitle(event.target.value)} placeholder={`脚本原文段 ${segments.length + 1}`} />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>原文</span>
            <div className="add-storyboard-field__control">
              <textarea value={sourceText} onChange={(event) => setSourceText(event.target.value)} placeholder="粘贴或输入该脚本段原文" />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>摘要</span>
            <div className="add-storyboard-field__control">
              <textarea value={summary} onChange={(event) => setSummary(event.target.value)} placeholder="用一两句话概括该段剧情" />
            </div>
          </label>
          <div className="add-storyboard-field">
            <span>剧情功能</span>
            <ModalListSelect
              options={storyFunctionOptions(storyFunction)}
              placeholder="请选择剧情功能"
              value={storyFunction}
              onChange={setStoryFunction}
            />
          </div>
          <label className="add-storyboard-field">
            <span>情绪</span>
            <div className="add-storyboard-field__control">
              <input value={dominantEmotion} onChange={(event) => setDominantEmotion(event.target.value)} placeholder="例如：愤怒、绝望" />
            </div>
          </label>
          <div className="add-storyboard-field">
            <span>节奏</span>
            <ModalListSelect
              options={rhythmOptions(rhythm)}
              placeholder="请选择节奏"
              value={rhythm}
              onChange={setRhythm}
            />
          </div>
          <label className="add-storyboard-field">
            <span>视角</span>
            <div className="add-storyboard-field__control">
              <input value={viewpoint} onChange={(event) => setViewpoint(event.target.value)} placeholder="例如：客观，聚焦郑夏允视角" />
            </div>
          </label>
          <div className="add-storyboard-field edit-storyboard-grid__full">
            <span>插入位置</span>
            <div className="add-storyboard-position-rail" role="radiogroup" aria-label="插入位置">
              {positionOptions.map((option) => {
                const active = insertMode === option.mode;
                return (
                  <label
                    key={option.mode}
                    className={[
                      "add-storyboard-position-rail__item",
                      active ? "is-active" : "",
                      option.disabled ? "is-disabled" : "",
                    ].filter(Boolean).join(" ")}
                  >
                    <input
                      checked={active}
                      disabled={option.disabled}
                      name="script-segment-insert-mode"
                      type="radio"
                      onChange={() => setInsertMode(option.mode)}
                    />
                    <i aria-hidden="true" />
                    <em>{option.label}</em>
                  </label>
                );
              })}
            </div>
            {insertMode === "after_segment" ? (
              <div className="add-storyboard-after-select">
                <ModalListSelect
                  options={segments.map((segment, index) => ({
                    value: String(index),
                    label: `顺序 ${segment.order_no || index + 1} · ${segment.script_segment_code || segment.title || `脚本段 ${index + 1}`}`,
                  }))}
                  placeholder="请选择脚本段"
                  value={afterIndex == null ? "" : String(afterIndex)}
                  onChange={(value) => setAfterIndex(value === "" ? null : Number(value))}
                />
              </div>
            ) : null}
            <p className="add-storyboard-hint">{hint}</p>
          </div>
        </div>
      </div>

      <footer className="add-storyboard-modal__actions">
        <button className="add-storyboard-modal__cancel" disabled={submitting} type="button" onClick={onClose}>取消</button>
        <button
          className="add-storyboard-modal__confirm modal-primary-action"
          disabled={submitting}
          type="button"
          onClick={async () => {
            if (submitting) return;
            setSubmitting(true);
            try {
              await onConfirm({
                insertMode,
                afterIndex: insertMode === "after_segment" ? afterIndex : null,
                segment: {
                  episode_code: episodeCode,
                  title: title.trim() || `脚本原文段 ${segments.length + 1}`,
                  source_text: sourceText,
                  summary,
                  story_function: storyFunction || null,
                  dominant_emotion: dominantEmotion || null,
                  rhythm: rhythm || null,
                  viewpoint: viewpoint || null,
                },
              });
            } finally {
              setSubmitting(false);
            }
          }}
        >
          {submitting ? "新增中..." : "确认新增"}
        </button>
      </footer>
    </section>
  </div>;
}

function EditScriptSegmentModal({
  segment,
  index,
  assets,
  onClose,
  onSave,
}: {
  segment: BreakdownDraftScriptSegment;
  index: number;
  assets: BreakdownDraftAsset[];
  onClose: () => void;
  onSave: (patch: Partial<BreakdownDraftScriptSegment>) => void;
}) {
  const [code, setCode] = React.useState(segment.script_segment_code || `J${String(index + 1).padStart(3, "0")}`);
  const [title, setTitle] = React.useState(segment.title || "");
  const [sourceText, setSourceText] = React.useState(segment.source_text || "");
  const [summary, setSummary] = React.useState(segment.summary || "");
  const [storyFunction, setStoryFunction] = React.useState(segment.story_function || "");
  const [dominantEmotion, setDominantEmotion] = React.useState(segment.dominant_emotion || "");
  const [rhythm, setRhythm] = React.useState(segment.rhythm || "");
  const [viewpoint, setViewpoint] = React.useState(segment.viewpoint || "");
  const [duration, setDuration] = React.useState(segment.estimated_duration_seconds != null ? String(segment.estimated_duration_seconds) : "");
  const [roleRefs, setRoleRefs] = React.useState((segment.role_refs?.length ? segment.role_refs : segment.characters || []).join(", "));
  const [sceneRefs, setSceneRefs] = React.useState((segment.scene_refs?.length ? segment.scene_refs : segment.locations || []).join(", "));
  const [propRefs, setPropRefs] = React.useState((segment.prop_refs || []).join(", "));
  const [productionNotes, setProductionNotes] = React.useState((segment.production_notes || []).join("\n"));
  const [submitting, setSubmitting] = React.useState(false);

  React.useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !submitting) onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose, submitting]);

  return <div className="modal-backdrop add-storyboard-backdrop" role="presentation" onClick={() => { if (!submitting) onClose(); }}>
    <section className="add-storyboard-modal edit-storyboard-modal edit-script-segment-modal" role="dialog" aria-modal="true" aria-label="编辑脚本原文段" onClick={(event) => event.stopPropagation()}>
      <header className="add-storyboard-modal__head">
        <div>
          <h3>编辑脚本原文段</h3>
          <p>修改脚本段的标题、内容和生产信息。</p>
        </div>
        <button className="add-storyboard-modal__close" type="button" onClick={onClose} aria-label="关闭">
          <X size={18} strokeWidth={1.8} />
        </button>
      </header>

      <div className="add-storyboard-modal__body edit-storyboard-modal__body">
        <div className="edit-storyboard-grid">
          <label className="add-storyboard-field">
            <span>编号</span>
            <div className="add-storyboard-field__control">
              <input value={code} onChange={(event) => setCode(event.target.value)} placeholder={`J${String(index + 1).padStart(3, "0")}`} />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>标题</span>
            <div className="add-storyboard-field__control">
              <input value={title} onChange={(event) => setTitle(event.target.value)} placeholder={`脚本原文段 ${index + 1}`} />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>原文</span>
            <div className="add-storyboard-field__control">
              <textarea value={sourceText} onChange={(event) => setSourceText(event.target.value)} placeholder="粘贴或输入该脚本段原文" />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>摘要</span>
            <div className="add-storyboard-field__control">
              <textarea value={summary} onChange={(event) => setSummary(event.target.value)} placeholder="用一两句话概括该段剧情" />
            </div>
          </label>
          <div className="add-storyboard-field">
            <span>剧情功能</span>
            <ModalListSelect
              options={storyFunctionOptions(storyFunction)}
              placeholder="请选择剧情功能"
              value={storyFunction}
              onChange={setStoryFunction}
            />
          </div>
          <label className="add-storyboard-field">
            <span>情绪</span>
            <div className="add-storyboard-field__control">
              <input value={dominantEmotion} onChange={(event) => setDominantEmotion(event.target.value)} placeholder="例如：愤怒、绝望" />
            </div>
          </label>
          <div className="add-storyboard-field">
            <span>节奏</span>
            <ModalListSelect
              options={rhythmOptions(rhythm)}
              placeholder="请选择节奏"
              value={rhythm}
              onChange={setRhythm}
            />
          </div>
          <label className="add-storyboard-field">
            <span>视角</span>
            <div className="add-storyboard-field__control">
              <input value={viewpoint} onChange={(event) => setViewpoint(event.target.value)} placeholder="例如：客观，聚焦郑夏允视角" />
            </div>
          </label>
          <label className="add-storyboard-field">
            <span>预计时长（秒）</span>
            <div className="add-storyboard-field__control">
              <input min={0} type="number" value={duration} onChange={(event) => setDuration(event.target.value)} placeholder="0" />
            </div>
          </label>
          <label className="add-storyboard-field">
            <span>关联角色</span>
            <div className="add-storyboard-field__control">
              <input placeholder="R001, R002" value={roleRefs} onChange={(event) => setRoleRefs(event.target.value)} />
            </div>
          </label>
          <label className="add-storyboard-field">
            <span>关联场景</span>
            <div className="add-storyboard-field__control">
              <input placeholder="SC001, SC002" value={sceneRefs} onChange={(event) => setSceneRefs(event.target.value)} />
            </div>
          </label>
          <label className="add-storyboard-field">
            <span>关联道具</span>
            <div className="add-storyboard-field__control">
              <input placeholder="P001, P002" value={propRefs} onChange={(event) => setPropRefs(event.target.value)} />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>生产备注</span>
            <div className="add-storyboard-field__control">
              <textarea value={productionNotes} onChange={(event) => setProductionNotes(event.target.value)} placeholder="每行一条备注" />
            </div>
          </label>
        </div>
      </div>

      <footer className="add-storyboard-modal__actions">
        <button className="add-storyboard-modal__cancel" disabled={submitting} type="button" onClick={onClose}>取消</button>
        <button
          className="add-storyboard-modal__confirm modal-primary-action"
          disabled={submitting}
          type="button"
          onClick={() => {
            if (submitting) return;
            setSubmitting(true);
            onSave({
              script_segment_code: code.trim() || null,
              title: title.trim(),
              source_text: sourceText,
              summary,
              story_function: storyFunction || null,
              dominant_emotion: dominantEmotion || null,
              rhythm: rhythm || null,
              viewpoint: viewpoint || null,
              estimated_duration_seconds: duration === "" ? null : Number(duration),
              role_refs: parseReferenceList(roleRefs),
              scene_refs: parseReferenceList(sceneRefs),
              prop_refs: parseReferenceList(propRefs),
              production_notes: productionNotes.split(/\r?\n/).map((item) => item.trim()).filter(Boolean),
            });
            setSubmitting(false);
            onClose();
          }}
        >
          {submitting ? "保存中..." : "保存修改"}
        </button>
      </footer>
    </section>
  </div>;
}

function normalizeSegmentValue(value: string | null | undefined, labels: Record<string, string>) {
  const original = String(value || "").trim();
  if (!original) return "-";
  const key = original.toLowerCase().replace(/[\s-]+/g, "_");
  return labels[key] || original;
}

function normalizeSegmentText(value: string | null | undefined, labels: Record<string, string>) {
  return normalizeSegmentValue(value, labels).replace(/\s*(?:->|=>|→|＞)\s*/g, " → ");
}

function parseReferenceList(value: string) {
  return value.split(/[,，、/\s]+/).map((item) => item.trim()).filter(Boolean);
}

function normalizeSelectKey(value: string | null | undefined, labels: Record<string, string>) {
  const current = String(value || "").trim();
  if (!current) return "";
  const key = current.toLowerCase().replace(/[\s-]+/g, "_");
  return labels[key] ? key : current;
}

function groupStoryboardsBySegment(
  storyboards: BreakdownDraftStoryboard[],
  scriptSegments: BreakdownDraftScriptSegment[],
) {
  const segments = new Map(scriptSegments.map((segment, index) => [String(segment.script_segment_code || "").trim(), {
    order: segment.order_no || index + 1,
    title: segment.title || `脚本段 ${index + 1}`,
    sourceText: segment.source_text || "",
    plannedDuration: segment.estimated_duration_seconds ?? segment.duration_seconds ?? 0,
  }]));
  const grouped = new Map<string, Array<{ storyboard: BreakdownDraftStoryboard; index: number }>>();
  storyboards.forEach((storyboard, index) => {
    const code = String(storyboard.script_segment_code || "未关联脚本段").trim() || "未关联脚本段";
    const items = grouped.get(code) || [];
    items.push({ storyboard, index });
    grouped.set(code, items);
  });
  return Array.from(grouped.entries()).map(([code, items], groupIndex) => {
    const segment = segments.get(code);
    const first = items[0]?.storyboard;
    items.sort((left, right) => (
      (left.storyboard.segment_order_num || left.storyboard.order_num || left.index + 1)
      - (right.storyboard.segment_order_num || right.storyboard.order_num || right.index + 1)
    ));
    return {
      code,
      order: segment?.order ?? groupIndex + scriptSegments.length + 1,
      title: segment?.title || first?.script_segment_title || "未匹配脚本段标题",
      sourceText: segment?.sourceText || first?.script_segment_source_text || "",
      plannedDuration: segment?.plannedDuration || first?.script_segment_estimated_duration_seconds || 0,
      actualDuration: items.reduce((sum, item) => sum + (item.storyboard.duration_seconds || 0), 0),
      items,
    };
  }).sort((left, right) => left.order - right.order);
}

function StoryboardReminderButton({
  items,
  label,
  title,
}: {
  items: BreakdownManualReviewItem[];
  label: string;
  title: string;
}) {
  return <LongTextButton
    className="agent-reminder-button"
    modalClassName="agent-reminder-modal"
    label={label}
    title={title}
    text={formatStoryboardReminders(items)}
  />;
}

function storyboardLayers(storyboard: BreakdownDraftStoryboard): BreakdownMirrorShot[] {
  return storyboard.mirror_shots || [];
}

function dialogueLines(value: unknown): string[] {
  if (Array.isArray(value)) return value.flatMap(dialogueLines);
  return String(value || "").split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

function storyboardSceneLabel(storyboard: BreakdownDraftStoryboard, assets: BreakdownDraftAsset[]) {
  const named = String(storyboard.scene_name || "").trim();
  if (named) return assetDisplayLabel(storyboard.scene_code, named);
  return assetReferenceDisplayLabel(storyboard.scene_code || storyboard.scene_name, assets, "scene");
}

function insertStoryboardIntoDraft(
  draft: BreakdownDraftData,
  payload: {
    segmentCode: string;
    segmentTitle: string;
    title: string;
    insertMode: "segment_start" | "after_shot" | "segment_end";
    afterGlobalIndex: number | null;
  },
) {
  const boards = [...draft.storyboards];
  const segmentCode = String(payload.segmentCode || "").trim() || "未关联脚本段";
  const segmentIndices = boards
    .map((board, index) => ({ board, index }))
    .filter(({ board }) => (String(board.script_segment_code || "").trim() || "未关联脚本段") === segmentCode);

  let insertIndex = boards.length;
  if (payload.insertMode === "segment_start") {
    insertIndex = segmentIndices[0]?.index ?? boards.length;
  } else if (payload.insertMode === "after_shot" && payload.afterGlobalIndex != null && payload.afterGlobalIndex >= 0) {
    insertIndex = Math.min(payload.afterGlobalIndex + 1, boards.length);
  } else if (segmentIndices.length) {
    insertIndex = segmentIndices[segmentIndices.length - 1].index + 1;
  }

  const nextTitle = String(payload.title || "").trim() || `分镜 ${boards.length + 1}`;
  const created: BreakdownDraftStoryboard = {
    title: nextTitle,
    script_segment_code: segmentCode,
    script_segment_title: payload.segmentTitle || "",
    description: "",
    camera: "",
    dialogue: "",
    characters: [],
    duration_seconds: 10,
    shot_size: "中景 → 近景",
    mirror_shots: [],
    order_num: 0,
    segment_order_num: 0,
  };

  boards.splice(insertIndex, 0, created);
  const renumbered = boards.map((item, index) => ({ ...item, order_num: index + 1 }));

  let segmentOrderCounter: Record<string, number> = {};
  const finalStoryboards = renumbered.map((item) => {
    const segCode = String(item.script_segment_code || "").trim() || "未关联脚本段";
    segmentOrderCounter[segCode] = (segmentOrderCounter[segCode] || 0) + 1;
    return { ...item, segment_order_num: segmentOrderCounter[segCode] };
  });

  return {
    draft: { ...draft, storyboards: finalStoryboards },
    insertIndex,
  };
}

function validateStoryboardDrafts(storyboards: BreakdownDraftStoryboard[]): string[] {
  const errors: string[] = [];
  storyboards.forEach((storyboard, index) => {
    const code = storyboard.storyboard_code || `分镜 ${index + 1}`;
    const duration = Number(storyboard.duration_seconds || 0);
    if (duration < 10 || duration > 15) errors.push(`${code} 父分镜时长必须为 10-15 秒。`);
    const shots = storyboard.mirror_shots || [];
    if (!shots.length) return;
    if (shots.length < 2) errors.push(`${code} 使用镜中分镜时至少需要 A/B 两个子镜头。`);
    if (shots.length > 6) errors.push(`${code} 镜中分镜最多允许 A-F 六个子镜头。`);
    if (shots.some((shot) => Number(shot.duration_seconds || 0) <= 0)) errors.push(`${code} 子镜头时长必须大于 0 秒。`);
    shots.forEach((shot, shotIndex) => {
      const label = shot.id || String.fromCharCode(65 + shotIndex);
      if (!shot.shot_function) errors.push(`${code}-${label} 缺少镜头职能。`);
      if (!shot.movement_reason) errors.push(`${code}-${label} 缺少运镜动机。`);
      if (!shot.environmental_pressure) errors.push(`${code}-${label} 缺少环境压力细节。`);
      if (!shot.micro_action) errors.push(`${code}-${label} 缺少身体微动作。`);
      if (!shot.sound_or_motif) errors.push(`${code}-${label} 缺少声音或视觉母题。`);
      if (shotIndex > 0) {
        const previous = shots[shotIndex - 1];
        if (shot.shot_size === previous.shot_size && shot.camera === previous.camera) {
          errors.push(`${code}-${label} 与前一子镜头需要不同景别或角度。`);
        }
      }
    });
    const total = shots.reduce((sum, shot) => sum + Number(shot.duration_seconds || 0), 0);
    if (Math.abs(total - duration) > 0.01) errors.push(`${code} 子镜头总时长必须等于父分镜时长。`);
    const parentDialogue = dialogueLines(storyboard.dialogue).join("\n");
    const childDialogue = shots.flatMap((shot) => dialogueLines(shot.dialogue)).join("\n");
    if (parentDialogue && childDialogue !== parentDialogue) errors.push(`${code} 子镜头台词必须逐字保留父分镜原文行、说话人前缀、标点和顺序。`);
  });
  return errors;
}

function StoryboardParameterLayers({ storyboard }: { storyboard: BreakdownDraftStoryboard }) {
  const shots = storyboardLayers(storyboard);
  if (!shots.length) return <span className="storyboard-parameter-value">{normalizeStoryboardShotSize(storyboard.shot_size)}/{storyboard.duration_seconds ? `${storyboard.duration_seconds}秒` : "未设置"}</span>;
  return <div className="storyboard-layer-list">{shots.map((shot, index) => <div className="storyboard-layer-line" key={`${shot.id || index}-parameter`}>
    <strong>{shot.id || shot.label || String.fromCharCode(65 + index)}</strong>
    <span className="storyboard-parameter-value">{normalizeStoryboardShotSize(shot.shot_size)}/{shot.duration_seconds != null ? `${shot.duration_seconds}秒` : "未单独输出"}</span>
  </div>)}</div>;
}

function StoryboardCharacterLayers({ storyboard, assets }: { storyboard: BreakdownDraftStoryboard; assets: BreakdownDraftAsset[] }) {
  const shots = storyboardLayers(storyboard);
  if (!shots.length) return <div className="storyboard-character-list">{(storyboard.characters || []).map((character) => <span key={character}>{assetReferenceDisplayLabel(character, assets, "character")}</span>)}{!storyboard.characters?.length ? <span>-</span> : null}</div>;
  return <div className="storyboard-layer-list">{shots.map((shot, index) => {
    const code = shot.id || shot.label || String.fromCharCode(65 + index);
    const characters = shot.characters?.length ? shot.characters : storyboard.characters || [];
    return <div className="storyboard-layer-line" key={`${code}-characters`}><strong>{code}</strong><span>{characters.length ? characters.map((character) => assetReferenceDisplayLabel(character, assets, "character")).join("、") : "未单独输出"}</span></div>;
  })}</div>;
}

function StoryboardTextLayers({
  storyboard,
  field,
  label,
}: {
  storyboard: BreakdownDraftStoryboard;
  field: "description" | "camera";
  label: string;
}) {
  const shots = storyboardLayers(storyboard);
  const parentCode = storyboard.storyboard_code || `分镜 ${storyboard.order_num || storyboard.order_no || ""}`.trim();
  if (!shots.length) {
    const value = field === "camera" ? normalizeStoryboardCamera(storyboard.camera) : storyboard[field];
    return <SegmentClampedText title={`${parentCode} ${label}`} value={value} />;
  }
  return <div className="storyboard-layer-list">{shots.map((shot, index) => {
    const code = shot.id || shot.label || String.fromCharCode(65 + index);
    const value = field === "description"
      ? [
          shot.shot_function ? `职能：${shot.shot_function}` : "",
          shot.description || "",
          shot.environmental_pressure ? `环境：${shot.environmental_pressure}` : "",
          shot.micro_action ? `动作：${shot.micro_action}` : "",
          shot.sound_or_motif ? `声音/母题：${shot.sound_or_motif}` : "",
        ].filter(Boolean).join("\n")
      : field === "camera"
        ? [normalizeStoryboardCamera(shot.camera), shot.movement_reason ? `动机：${shot.movement_reason}` : ""].filter(Boolean).join("\n")
        : shot[field];
    const fallback = "未单独输出";
    return <div className="storyboard-layer-line storyboard-layer-text" key={`${code}-${field}`}><strong>{code}</strong><SegmentClampedText title={`${parentCode}-${code} ${label}`} value={value || fallback} /></div>;
  })}</div>;
}

function StoryboardDialogueLayers({ storyboard }: { storyboard: BreakdownDraftStoryboard }) {
  const shots = storyboardLayers(storyboard);
  const parentCode = storyboard.storyboard_code || `分镜 ${storyboard.order_num || storyboard.order_no || ""}`.trim();
  if (!shots.length) {
    return <StoryboardDialogueValue
      backTranslation={storyboard.dialogue_back_translation}
      dialogue={storyboard.dialogue}
      title={`${parentCode} 台词`}
    />;
  }
  return <div className="storyboard-layer-list">{shots.map((shot, index) => {
    const code = shot.id || shot.label || String.fromCharCode(65 + index);
    return <div className="storyboard-layer-line storyboard-layer-text" key={`${code}-dialogue`}>
      <strong>{code}</strong>
      <StoryboardDialogueValue
        backTranslation={shot.dialogue_back_translation}
        dialogue={shot.dialogue}
        title={`${parentCode}-${code} 台词`}
      />
    </div>;
  })}</div>;
}

function StoryboardDialogueValue({
  title,
  dialogue,
  backTranslation,
}: {
  title: string;
  dialogue?: string | null;
  backTranslation?: string | null;
}) {
  return <div className="storyboard-dialogue-value">
    <SegmentClampedText title={title} value={dialogue || "无台词"} />
    <BackTranslationReference value={backTranslation} />
  </div>;
}

function BackTranslationReference({ value }: { value?: string | null }) {
  if (!String(value || "").trim()) return null;
  return <div className="storyboard-back-translation">
    <span>中文回译</span>
    <SegmentClampedText title="中文回译" value={value} />
  </div>;
}

function MirrorShotEditor({
  shots,
  parentDuration,
  parentDialogue,
  onChange,
}: {
  shots: BreakdownMirrorShot[];
  parentDuration: number;
  parentDialogue: string;
  onChange: (shots: BreakdownMirrorShot[]) => void;
}) {
  function update(index: number, patch: Partial<BreakdownMirrorShot>) {
    onChange(shots.map((shot, shotIndex) => shotIndex === index ? { ...shot, ...patch } : shot));
  }
  const totalDuration = shots.reduce((sum, shot) => sum + Number(shot.duration_seconds || 0), 0);
  const childDialogue = shots.flatMap((shot) => dialogueLines(shot.dialogue)).join("\n");
  const normalizedParentDialogue = dialogueLines(parentDialogue).join("\n");
  return <div className="mirror-shot-editor">
    <div className="mirror-shot-editor-head">
      <strong>镜中分镜</strong>
      <button className="btn" type="button" onClick={() => onChange([...shots, {
        id: String.fromCharCode(65 + shots.length),
        description: "",
        camera: "",
        shot_size: "",
        duration_seconds: null,
        dialogue: "",
        characters: [],
        shot_function: "",
        movement_reason: "",
        environmental_pressure: "",
        micro_action: "",
        sound_or_motif: "",
      }])}>新增子镜头</button>
    </div>
    {shots.length && Math.abs(totalDuration - parentDuration) > 0.01 ? <span className="field-hint error">子镜头合计 {totalDuration.toFixed(1)} 秒，必须等于父分镜 {parentDuration.toFixed(1)} 秒。</span> : null}
    {shots.length && normalizedParentDialogue && childDialogue !== normalizedParentDialogue ? <span className="field-hint error">子镜头台词必须按原行顺序逐字覆盖父分镜台词，说话人前缀和标点也不能省略、改写或重复。</span> : null}
    {shots.map((shot, index) => <div className="mirror-shot-edit-row" key={`${shot.id || shot.label || "mirror"}-${index}`}>
      <div className="mirror-shot-edit-meta">
        <input aria-label="镜中分镜编号" value={shot.id || shot.label || ""} onChange={(event) => update(index, { id: event.target.value.toUpperCase(), label: undefined })} />
        <select aria-label="镜中分镜职能" value={shot.shot_function || ""} onChange={(event) => update(index, { shot_function: event.target.value })}>
          <option value="">镜头职能</option>
          {["Establish", "Reveal", "Power", "Pressure", "Detail", "Reaction", "Shift", "Impact", "Aftermath", "Exit"].map((value) => <option key={value} value={value}>{value}</option>)}
        </select>
        <input aria-label="镜中分镜景别" placeholder="景别" value={shot.shot_size || ""} onChange={(event) => update(index, { shot_size: event.target.value })} />
        <input aria-label="镜中分镜时长" min={0.1} step={0.1} placeholder="秒" type="number" value={shot.duration_seconds ?? ""} onChange={(event) => update(index, { duration_seconds: event.target.value ? Number(event.target.value) : null })} />
        <button className="btn danger" type="button" onClick={() => onChange(shots.filter((_, shotIndex) => shotIndex !== index))}>删除</button>
      </div>
      <input placeholder="子镜头出场角色，用顿号或逗号分隔" value={(shot.characters || []).join("、")} onChange={(event) => update(index, { characters: event.target.value.split(/[、,，]/).map((item) => item.trim()).filter(Boolean) })} />
      <textarea placeholder="子镜头画面" value={shot.description || ""} onChange={(event) => update(index, { description: event.target.value })} />
      <textarea placeholder="子镜头机位、角度和运镜" value={shot.camera || ""} onChange={(event) => update(index, { camera: event.target.value })} />
      <textarea placeholder="运镜动机" value={shot.movement_reason || ""} onChange={(event) => update(index, { movement_reason: event.target.value })} />
      <textarea placeholder="环境压力" value={shot.environmental_pressure || ""} onChange={(event) => update(index, { environmental_pressure: event.target.value })} />
      <textarea placeholder="身体微动作" value={shot.micro_action || ""} onChange={(event) => update(index, { micro_action: event.target.value })} />
      <textarea placeholder="声音或视觉母题" value={shot.sound_or_motif || ""} onChange={(event) => update(index, { sound_or_motif: event.target.value })} />
      <textarea placeholder="子镜头台词（无则留空）" value={shot.dialogue || ""} onChange={(event) => update(index, { dialogue: event.target.value })} />
      <BackTranslationReference value={shot.dialogue_back_translation} />
    </div>)}
  </div>;
}

function formatStoryboardReminders(items: BreakdownManualReviewItem[]) {
  return items.map((item, index) => {
    const details = [
      `${index + 1}. ${item.item}`,
      item.detail || "",
      item.suggestion ? `Agent建议：${item.suggestion}` : "",
      item.source ? `来源：${item.source}` : "",
    ].filter(Boolean);
    return details.join("\n");
  }).join("\n\n");
}

function splitStoryboardReminders(
  reminders: BreakdownManualReviewItem[],
  groups: ReturnType<typeof groupStoryboardsBySegment>,
) {
  const global: BreakdownManualReviewItem[] = [];
  const bySegment = new Map<string, BreakdownManualReviewItem[]>();
  const byStoryboard = new Map<string, BreakdownManualReviewItem[]>();
  const segmentAliases = new Map<string, string>();
  const storyboardAliases = new Map<string, { code: string; segmentCode: string; localOrder: number }>();
  for (const group of groups) {
    segmentAliases.set(group.code.toUpperCase(), group.code);
    const shortSegment = group.code.match(/J\d{3}$/i)?.[0].toUpperCase();
    if (shortSegment) segmentAliases.set(shortSegment, group.code);
    group.items.forEach(({ storyboard }, index) => {
      const code = String(storyboard.storyboard_code || "").trim();
      if (!code) return;
      const entry = { code, segmentCode: group.code, localOrder: storyboard.segment_order_num || index + 1 };
      storyboardAliases.set(code.toUpperCase(), entry);
      const shortCode = code.match(/F\d{3}$/i)?.[0].toUpperCase();
      if (shortCode) storyboardAliases.set(shortCode, entry);
      storyboardAliases.set(`SH${String(storyboard.order_num || index + 1).padStart(3, "0")}`, entry);
    });
  }
  const add = (map: Map<string, BreakdownManualReviewItem[]>, key: string, item: BreakdownManualReviewItem) => {
    map.set(key, [...(map.get(key) || []), item]);
  };
  for (const reminder of reminders) {
    const text = [reminder.scope_code, reminder.code, reminder.path, reminder.item, reminder.detail, reminder.suggestion].filter(Boolean).join(" ");
    const explicitScope = String(reminder.scope_type || "").toLowerCase();
    const explicitCode = String(reminder.scope_code || "").toUpperCase();
    if (explicitScope === "storyboard" && storyboardAliases.has(explicitCode)) {
      add(byStoryboard, storyboardAliases.get(explicitCode)!.code, reminder);
      continue;
    }
    if (explicitScope === "script_segment" && segmentAliases.has(explicitCode)) {
      add(bySegment, segmentAliases.get(explicitCode)!, reminder);
      continue;
    }
    if (explicitScope === "episode") {
      global.push(reminder);
      continue;
    }
    const storyboardMatches = Array.from(new Set([
      ...(text.match(/[A-Z0-9]{2,8}-EP\d{2}-F\d{3}/gi) || []),
      ...(text.match(/\bF\d{3}\b/gi) || []),
      ...(text.match(/\bSH\d{3}\b/gi) || []),
    ].map((code) => storyboardAliases.get(code.toUpperCase())).filter(Boolean))) as Array<{ code: string; segmentCode: string; localOrder: number }>;
    if (storyboardMatches.length === 1) {
      add(byStoryboard, storyboardMatches[0].code, reminder);
      continue;
    }
    if (storyboardMatches.length > 1) {
      const segmentCodes = new Set(storyboardMatches.map((item) => item.segmentCode));
      if (segmentCodes.size === 1) add(bySegment, storyboardMatches[0].segmentCode, reminder);
      else global.push(reminder);
      continue;
    }
    const segmentMatches = Array.from(new Set([
      ...(text.match(/[A-Z0-9]{2,8}-EP\d{2}-J\d{3}/gi) || []),
      ...(text.match(/\bJ\d{3}\b/gi) || []),
    ].map((code) => segmentAliases.get(code.toUpperCase())).filter(Boolean))) as string[];
    if (segmentMatches.length === 1) {
      const multipleLocalShots = /(?:shot|分镜)\s*\d+\s*(?:→|->|\/|、|和|及|-)\s*\d+/i.test(text);
      const localShot = text.match(/(?:shot|分镜)\s*[-_]?\s*(\d+)/i);
      if (localShot && !multipleLocalShots) {
        const localOrder = Number(localShot[1]);
        const group = groups.find((item) => item.code === segmentMatches[0]);
        const storyboard = group?.items.find((item, index) => (item.storyboard.segment_order_num || index + 1) === localOrder)?.storyboard;
        if (storyboard?.storyboard_code) {
          add(byStoryboard, storyboard.storyboard_code, reminder);
          continue;
        }
      }
      add(bySegment, segmentMatches[0], reminder);
      continue;
    }
    global.push(reminder);
  }
  return { global, bySegment, byStoryboard };
}

function sceneOptionsFromDraft(draft: BreakdownDraftData) {
  return draft.assets.filter((asset) => asset.asset_type === "scene");
}

function prepareDraft(draft: BreakdownDraftData, stage: EditableBreakdownDraftProps["stage"]) {
  void stage;
  return mergeBreakdownDraft(undefined, draft, [], []);
}

function removeAssetFromDraft(draft: BreakdownDraftData, clientAssetKey: string) {
  return syncAssetGroups(draft, draft.assets.filter((asset) => asset.client_asset_key !== clientAssetKey));
}

function applyAssetReviewCompletion(draft: BreakdownDraftData, value: AssetReviewCompletion): BreakdownDraftData {
  const asset = draft.assets.find((item) => item.client_asset_key === value.clientAssetKey);
  if (!asset) return draft;
  const review = asset.review_items.find((item) => item.review_id === value.reviewId);
  if (!review || review.asset_key !== asset.client_asset_key) return draft;
  const deferred = value.mode === "defer";
  const updatedReview = {
    ...review,
    human_input: value.humanInput.trim() || null,
    status: deferred ? "deferred" : value.mode === "ignore" ? "ignored" : "confirmed",
  };
  let updatedAsset: BreakdownDraftAsset = {
    ...asset,
    review_items: asset.review_items.map((item) => item.review_id === value.reviewId ? updatedReview : item),
  };
  if (!deferred && value.mode !== "ignore") updatedAsset = applyReviewTarget(updatedAsset, review.target_field, value);
  return {
    ...draft,
    assets: draft.assets.map((item) => item.client_asset_key === asset.client_asset_key ? updatedAsset : item),
  };
}

function applyReviewTarget(asset: BreakdownDraftAsset, targetField: string, value: AssetReviewCompletion): BreakdownDraftAsset {
  if (targetField === "relations.scene_links") {
    const sceneLinks = (value.selectedScenes || []).map((scene) => ({
      target_asset_key: scene.client_asset_key,
      target_asset_code: scene.asset_code,
      target_display_code: scene.display_code,
      target_display_label: scene.display_label,
    }));
    return { ...asset, relations: { ...asset.relations, scene_links: sceneLinks } };
  }
  if (targetField === "relations.owner_character") {
    return { ...asset, relations: { ...asset.relations, owner_character: ownerCharacterRelation(value.selectedCharacter) } };
  }
  if (targetField === "description") return { ...asset, description: value.humanInput.trim() || null };
  if (targetField === "name") {
    const name = value.humanInput.trim();
    return name ? { ...asset, name, display_label: `${asset.display_code}-${name}` } : asset;
  }
  if (!targetField.startsWith("attributes.")) return asset;
  const field = targetField.slice("attributes.".length);
  const attributes = asset.attributes as unknown as Record<string, unknown>;
  if (!Object.prototype.hasOwnProperty.call(attributes, field)) return asset;
  const current = attributes[field];
  const input = value.humanInput.trim();
  const nextValue = Array.isArray(current)
    ? input.split(/[、，,；;\n]+/).map((item) => item.trim()).filter(Boolean)
    : typeof current === "boolean"
      ? ["true", "1", "是", "有"].includes(input.toLowerCase())
      : input || null;
  return { ...asset, attributes: { ...asset.attributes, [field]: nextValue } } as BreakdownDraftAsset;
}

const STORYBOARD_SHOT_SIZE_OPTIONS = [
  "极近特写", "特写", "近景", "中近景", "中景", "中全景", "全景", "远景", "大远景",
  "中景 → 近景", "近景 → 特写", "全景 → 中景", "远景 → 中全景",
];

function EditStoryboardModal({
  index,
  scriptSegments,
  storyboard,
  onClose,
  onSave,
}: {
  index: number;
  scriptSegments: BreakdownDraftScriptSegment[];
  storyboard: BreakdownDraftStoryboard;
  onClose: () => void;
  onSave: (patch: Partial<BreakdownDraftStoryboard>) => void;
}) {
  const segmentOptions = React.useMemo(() => {
    const map = new Map<string, string>();
    for (const segment of scriptSegments) {
      const code = String(segment.script_segment_code || "").trim();
      if (!code) continue;
      map.set(code, `${code} ${segment.title || ""}`.trim());
    }
    const current = String(storyboard.script_segment_code || "").trim();
    if (current && !map.has(current)) map.set(current, current);
    if (!map.size) map.set("未关联脚本段", "未关联脚本段");
    return Array.from(map.entries()).map(([value, label]) => ({ value, label }));
  }, [scriptSegments, storyboard.script_segment_code]);

  const [draft, setDraft] = React.useState<BreakdownDraftStoryboard>(() => ({ ...storyboard }));
  React.useEffect(() => { setDraft({ ...storyboard }); }, [storyboard]);

  const shotSizeOptions = React.useMemo(() => {
    const current = String(draft.shot_size || "").trim();
    const values = new Set<string>(STORYBOARD_SHOT_SIZE_OPTIONS);
    if (current) values.add(current);
    return Array.from(values).map((value) => ({ value, label: value }));
  }, [draft.shot_size]);

  React.useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  function patchDraft(next: Partial<BreakdownDraftStoryboard>) {
    setDraft((current) => ({ ...current, ...next }));
  }

  return <div className="modal-backdrop add-storyboard-backdrop" role="presentation" onClick={onClose}>
    <section className="add-storyboard-modal edit-storyboard-modal" role="dialog" aria-modal="true" aria-label="编辑分镜" onClick={(event) => event.stopPropagation()}>
      <header className="add-storyboard-modal__head">
        <div>
          <h3>编辑分镜</h3>
          <p>完善编号、景别、画面与台词后保存；可继续调整顺序再统一保存入库。</p>
        </div>
        <button className="add-storyboard-modal__close" type="button" onClick={onClose} aria-label="关闭">
          <X size={18} strokeWidth={1.8} />
        </button>
      </header>

      <div className="add-storyboard-modal__body edit-storyboard-modal__body">
        <div className="edit-storyboard-grid">
          <label className="add-storyboard-field">
            <span>分镜编号</span>
            <div className="add-storyboard-field__control">
              <input value={draft.storyboard_code || ""} onChange={(event) => patchDraft({ storyboard_code: event.target.value })} placeholder={`F${String(index + 1).padStart(3, "0")}`} />
            </div>
          </label>
          <div className="add-storyboard-field">
            <span>关联脚本段</span>
            <ModalListSelect
              options={segmentOptions}
              placeholder="请选择脚本段"
              value={String(draft.script_segment_code || "").trim() || segmentOptions[0]?.value || ""}
              onChange={(value) => patchDraft({
                script_segment_code: value,
                script_segment_title: scriptSegments.find((item) => item.script_segment_code === value)?.title || draft.script_segment_title,
              })}
            />
          </div>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>分镜标题</span>
            <div className="add-storyboard-field__control">
              <input value={draft.title || ""} onChange={(event) => patchDraft({ title: event.target.value })} placeholder={`分镜 ${index + 1}`} />
            </div>
          </label>
          <label className="add-storyboard-field">
            <span>场景</span>
            <div className="add-storyboard-field__control">
              <input value={draft.scene_name || ""} onChange={(event) => patchDraft({ scene_name: event.target.value })} placeholder="例如：俱乐部-走廊" />
            </div>
          </label>
          <div className="add-storyboard-field">
            <span>景别</span>
            <ModalListSelect
              options={shotSizeOptions}
              placeholder="请选择景别"
              value={String(draft.shot_size || "").trim()}
              onChange={(value) => patchDraft({ shot_size: value })}
            />
          </div>
          <label className="add-storyboard-field">
            <span>时长（秒）</span>
            <div className="add-storyboard-field__control">
              <input min={10} max={15} type="number" value={draft.duration_seconds ?? 10} onChange={(event) => patchDraft({ duration_seconds: Number(event.target.value) || 10 })} />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>角色（每行一个或顿号分隔）</span>
            <div className="add-storyboard-field__control">
              <textarea
                value={(draft.characters || []).join("、")}
                onChange={(event) => patchDraft({ characters: event.target.value.split(/[、,，/\n]+/).map((item) => item.trim()).filter(Boolean) })}
                placeholder="R001-郑夏允、R003-李俊赫"
              />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>画面内容</span>
            <div className="add-storyboard-field__control">
              <textarea value={draft.description || ""} onChange={(event) => patchDraft({ description: event.target.value })} placeholder="描述画面主体与动作" />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>镜头执行</span>
            <div className="add-storyboard-field__control">
              <textarea value={draft.camera || ""} onChange={(event) => patchDraft({ camera: event.target.value })} placeholder="机位、运镜与动机" />
            </div>
          </label>
          <label className="add-storyboard-field edit-storyboard-grid__full">
            <span>台词</span>
            <div className="add-storyboard-field__control">
              <textarea value={draft.dialogue || ""} onChange={(event) => patchDraft({ dialogue: event.target.value })} placeholder="无台词可留空" />
            </div>
          </label>
        </div>
        <div className="edit-storyboard-mirror">
          <strong>镜中分镜</strong>
          <MirrorShotEditor
            parentDialogue={draft.dialogue || ""}
            parentDuration={Number(draft.duration_seconds || 0)}
            shots={storyboardLayers(draft)}
            onChange={(mirrorShots) => patchDraft({ mirror_shots: mirrorShots })}
          />
        </div>
      </div>

      <footer className="add-storyboard-modal__actions">
        <button className="add-storyboard-modal__cancel" type="button" onClick={onClose}>取消</button>
        <button className="add-storyboard-modal__confirm modal-primary-action" type="button" onClick={() => onSave(draft)}>完成编辑</button>
      </footer>
    </section>
  </div>;
}

function AddStoryboardModal({
  groups,
  prefillAfterIndex = null,
  prefillSegmentCode,
  scriptSegments,
  storyboards,
  onClose,
  onConfirm,
}: {
  groups: ReturnType<typeof groupStoryboardsBySegment>;
  prefillAfterIndex?: number | null;
  prefillSegmentCode?: string;
  scriptSegments: BreakdownDraftScriptSegment[];
  storyboards: BreakdownDraftStoryboard[];
  onClose: () => void;
  onConfirm: (payload: {
    segmentCode: string;
    segmentTitle: string;
    title: string;
    insertMode: "segment_start" | "after_shot" | "segment_end";
    afterGlobalIndex: number | null;
  }) => void;
}) {
  const segmentOptions = React.useMemo(() => {
    const map = new Map<string, string>();
    for (const segment of scriptSegments) {
      const code = String(segment.script_segment_code || "").trim();
      if (!code) continue;
      map.set(code, segment.title || code);
    }
    for (const group of groups) {
      if (!map.has(group.code)) map.set(group.code, group.title || group.code);
    }
    if (!map.size) map.set("未关联脚本段", "未关联脚本段");
    return Array.from(map.entries()).map(([code, title]) => ({ code, title }));
  }, [groups, scriptSegments]);

  const defaultSegment = prefillSegmentCode && segmentOptions.some((item) => item.code === prefillSegmentCode)
    ? prefillSegmentCode
    : segmentOptions[0]?.code || "未关联脚本段";
  const [segmentCode, setSegmentCode] = React.useState(defaultSegment);
  const [insertMode, setInsertMode] = React.useState<"segment_start" | "after_shot" | "segment_end">(
    prefillAfterIndex != null ? "after_shot" : "segment_end",
  );
  const [afterGlobalIndex, setAfterGlobalIndex] = React.useState<number | null>(prefillAfterIndex);
  const [title, setTitle] = React.useState(`分镜 ${storyboards.length + 1}`);

  const segmentShots = React.useMemo(
    () => storyboards
      .map((board, index) => ({ board, index }))
      .filter(({ board }) => (String(board.script_segment_code || "").trim() || "未关联脚本段") === segmentCode),
    [segmentCode, storyboards],
  );

  React.useEffect(() => {
    if (insertMode !== "after_shot") return;
    if (!segmentShots.length) {
      setAfterGlobalIndex(null);
      setInsertMode("segment_end");
      return;
    }
    if (afterGlobalIndex == null || !segmentShots.some((item) => item.index === afterGlobalIndex)) {
      setAfterGlobalIndex(segmentShots[segmentShots.length - 1].index);
    }
  }, [afterGlobalIndex, insertMode, segmentShots]);

  React.useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  const selectedSegment = segmentOptions.find((item) => item.code === segmentCode);

  const positionOptions: Array<{
    mode: "segment_start" | "after_shot" | "segment_end";
    label: string;
    disabled?: boolean;
  }> = [
    { mode: "segment_end", label: "该脚本段末尾" },
    { mode: "after_shot", label: "指定分镜之后（补镜头）", disabled: !segmentShots.length },
    { mode: "segment_start", label: "该脚本段最前" },
  ];

  const afterShotLabel = afterGlobalIndex == null
    ? ""
    : (() => {
      const board = storyboards[afterGlobalIndex];
      if (!board) return "";
      return board.storyboard_code || board.title || `分镜 ${afterGlobalIndex + 1}`;
    })();

  const hint = insertMode === "segment_start"
    ? `将插入到 ${selectedSegment?.code || segmentCode} 最前，段内序自动重排；创建后打开编辑。`
    : insertMode === "segment_end"
      ? `将追加到 ${selectedSegment?.code || segmentCode} 末尾；创建后打开编辑。`
      : `将插入到 ${afterShotLabel || "指定分镜"} 之后，后续分镜段内序自动后移；创建后打开编辑。`;

  return <div className="modal-backdrop add-storyboard-backdrop" role="presentation" onClick={onClose}>
    <section className="add-storyboard-modal" role="dialog" aria-modal="true" aria-label="新增分镜" onClick={(event) => event.stopPropagation()}>
      <header className="add-storyboard-modal__head">
        <div>
          <h3>新增分镜</h3>
          <p>默认加在该脚本段末尾；若中段缺镜头，可选「指定分镜之后」插入后再分发任务。</p>
        </div>
        <button className="add-storyboard-modal__close" type="button" onClick={onClose} aria-label="关闭">
          <X size={18} strokeWidth={1.8} />
        </button>
      </header>

      <div className="add-storyboard-modal__body">
        <div className="add-storyboard-field">
          <span>所属脚本段</span>
          <ModalListSelect
            options={segmentOptions.map((item) => ({ value: item.code, label: `${item.code} ${item.title}` }))}
            placeholder="请选择脚本段"
            value={segmentCode}
            onChange={setSegmentCode}
          />
        </div>

        <label className="add-storyboard-field">
          <span>分镜标题</span>
          <div className="add-storyboard-field__control">
            <input value={title} onChange={(event) => setTitle(event.target.value)} placeholder={`分镜 ${storyboards.length + 1}`} />
          </div>
        </label>

        <div className="add-storyboard-field">
          <span>插入位置</span>
          <div className="add-storyboard-position-rail" role="radiogroup" aria-label="插入位置">
            {positionOptions.map((option) => {
              const active = insertMode === option.mode;
              return (
                <label
                  key={option.mode}
                  className={[
                    "add-storyboard-position-rail__item",
                    active ? "is-active" : "",
                    option.disabled ? "is-disabled" : "",
                  ].filter(Boolean).join(" ")}
                >
                  <input
                    checked={active}
                    disabled={option.disabled}
                    name="storyboard-insert-mode"
                    type="radio"
                    onChange={() => setInsertMode(option.mode)}
                  />
                  <i aria-hidden="true" />
                  <em>{option.label}</em>
                </label>
              );
            })}
          </div>
          {insertMode === "after_shot" ? (
            <div className="add-storyboard-after-select">
              <ModalListSelect
                options={segmentShots.map(({ board, index }, localIndex) => ({
                  value: String(index),
                  label: `段内序 ${board.segment_order_num || localIndex + 1} · ${board.storyboard_code || board.title || `分镜 ${index + 1}`}`,
                }))}
                placeholder="请选择分镜"
                value={afterGlobalIndex == null ? "" : String(afterGlobalIndex)}
                onChange={(value) => setAfterGlobalIndex(value === "" ? null : Number(value))}
              />
            </div>
          ) : null}
          <p className="add-storyboard-hint">{hint}</p>
        </div>
      </div>

      <footer className="add-storyboard-modal__actions">
        <button className="add-storyboard-modal__cancel" type="button" onClick={onClose}>取消</button>
        <button
          className="add-storyboard-modal__confirm modal-primary-action"
          type="button"
          onClick={() => onConfirm({
            segmentCode,
            segmentTitle: selectedSegment?.title || "",
            title: title.trim() || `分镜 ${storyboards.length + 1}`,
            insertMode,
            afterGlobalIndex: insertMode === "after_shot" ? afterGlobalIndex : null,
          })}
        >
          确认新增
        </button>
      </footer>
    </section>
  </div>;
}

function PromptProcessingSummaryModal({ rows, status, onClose }: { rows: Array<ReturnType<typeof costumeProcessingRow>>; status: "new" | "reused" | "error"; onClose: () => void }) {
  const title = status === "new" ? "新增资产 Prompt" : status === "reused" ? "复用资产 Prompt" : "资产 Prompt 错误";
  return <div className="modal-backdrop prompt-summary-backdrop" role="presentation" onClick={onClose}>
    <section className="confirmation-modal costume-summary-modal" role="dialog" aria-modal="true" aria-label={title} onClick={(event) => event.stopPropagation()}>
      <div className="modal-head"><div><h3>{title}</h3><p>共 {rows.length} 项</p></div><button className="asset-scheme-close" type="button" aria-label="关闭" title="关闭" onClick={onClose}><X aria-hidden="true" size={18} strokeWidth={1.8} /></button></div>
      <div className="costume-context-table-wrap">
        <table className="costume-context-table">
          <thead><tr><th>处理结果</th><th>资产</th><th>资产编号</th><th>年龄阶段</th><th>装扮版本</th><th>生成内容</th><th>说明</th></tr></thead>
          <tbody>{rows.map((row) => <tr key={row.key}>
            <td><span className={`badge ${row.status === "error" ? "red" : row.status === "reused" ? "mint" : "sky"}`}>{row.status === "error" ? "错误" : row.status === "reused" ? "复用" : "新增"}</span></td>
            <td><strong>{row.character || "未命名"}</strong></td>
            <td><strong>{row.assetCode || "编码异常"}</strong></td>
            <td>{row.ageStage || "默认"}</td>
            <td>{row.costumeVariant || "默认"}</td>
            <td>{row.generationLabel}{row.structuredMode ? <small>{row.structuredMode}{row.cacheHit ? " / 命中缓存" : ""}</small> : null}</td>
            <td className={row.status === "error" ? "costume-context-error" : ""}>{row.detail}</td>
          </tr>)}</tbody>
        </table>
      </div>
    </section>
  </div>;
}

function costumeProcessingRow(item: Record<string, unknown>, index: number) {
  const viewPrompts = Array.isArray(item.view_prompts) ? item.view_prompts : [];
  const runtime = item.runtime_policy_execution && typeof item.runtime_policy_execution === "object" && !Array.isArray(item.runtime_policy_execution)
    ? item.runtime_policy_execution as Record<string, unknown>
    : {};
  const rawResponse = item.raw_model_response && typeof item.raw_model_response === "object" && !Array.isArray(item.raw_model_response)
    ? item.raw_model_response as Record<string, unknown>
    : {};
  const error = String(item.error || "").trim();
  const promptCount = Number(item.prompt_count) || viewPrompts.length || (item.prompt ? 1 : 0);
  const ageStage = String(item.age_stage_code || "").trim();
  const costumeVariant = String(item.costume_variant_code || "").trim();
  const rawStatus = String(item.processing_status || "").trim().toLowerCase();
  const status = error || rawStatus === "error" ? "error" : rawStatus === "reused" ? "reused" : "new";
  const assetTaskCount = Number(item.asset_task_count) || 0;
  const outputSpec = String(item.output_spec || (promptCount > 1 ? "A-E" : "A"));
  const assetType = String(item.asset_type || "character").trim().toLowerCase();
  const assetTypeLabel = assetType === "scene" ? "场景" : assetType === "prop" ? "道具" : "人物";
  const generationLabel = status === "reused"
    ? `已有资产任务${assetTaskCount ? ` ${assetTaskCount} 项` : ""}`
    : status === "error"
      ? "未生成有效提示词"
      : `${assetTypeLabel} ${outputSpec} 提示词${promptCount ? ` ${promptCount} 项` : ""}`;
  const assetCode = shortAssetCode(item.asset_code, item.asset_code_source);
  return {
    key: String(item.context_key || [status, assetCode, ageStage, costumeVariant].filter(Boolean).join("/") || `context-${index + 1}`),
    character: String(item.asset_name || item.character_name || item.matched_name || item.candidate_name || item.name || "").trim(),
    assetCode,
    ageStage,
    costumeVariant,
    generationLabel,
    structuredMode: String(item.structured_output_mode || rawResponse.structured_output_mode || ""),
    cacheHit: item.cache_hit === true || runtime.cache_hit === true,
    detail: String(item.detail || (status === "reused" ? "复用已有资产任务，本次不再生成重复提示词。" : error || `生成新的${assetTypeLabel}资产提示词。`)),
    status,
  };
}

function createClientAssetKey() {
  return createClientRequestId("manual-asset");
}

function syncAssetGroups(draft: BreakdownDraftData, assets: BreakdownDraftAsset[]): BreakdownDraftData {
  return withEditedAssetInventory(draft, assets);
}

/**
 * Fix stale relations: when a character asset's asset_code, display_code, or display_label changes,
 * all owner_character and scene_links relations pointing to it must be updated to match the new values.
 * This ensures AssetNormalization.v1 contract compliance.
 */
function fixStaleRelations(draft: BreakdownDraftData): BreakdownDraftData {
  // Build a lookup map: target_asset_key -> current asset identity
  const assetIdentityMap = new Map<string, { asset_code: string; display_code: string; display_label: string }>();
  for (const asset of draft.assets) {
    assetIdentityMap.set(asset.client_asset_key, {
      asset_code: asset.asset_code,
      display_code: asset.display_code,
      display_label: asset.display_label,
    });
  }

  // Fix each asset's relations
  const fixedAssets = draft.assets.map((asset) => {
    let needsFix = false;
    let fixedRelations = { ...asset.relations };

    // Fix owner_character relation
    if (asset.relations.owner_character) {
      const targetKey = asset.relations.owner_character.target_asset_key;
      const currentIdentity = assetIdentityMap.get(targetKey);
      if (currentIdentity) {
        const relation = asset.relations.owner_character;
        if (
          relation.target_asset_code !== currentIdentity.asset_code
          || relation.target_display_code !== currentIdentity.display_code
          || relation.target_display_label !== currentIdentity.display_label
        ) {
          needsFix = true;
          fixedRelations.owner_character = {
            target_asset_key: targetKey,
            target_asset_code: currentIdentity.asset_code,
            target_display_code: currentIdentity.display_code,
            target_display_label: currentIdentity.display_label,
          };
        }
      }
    }

    // Fix scene_links relations
    if (asset.relations.scene_links && asset.relations.scene_links.length > 0) {
      const fixedSceneLinks = asset.relations.scene_links.map((link) => {
        const targetKey = link.target_asset_key;
        const currentIdentity = assetIdentityMap.get(targetKey);
        if (currentIdentity) {
          if (
            link.target_asset_code !== currentIdentity.asset_code
            || link.target_display_code !== currentIdentity.display_code
            || link.target_display_label !== currentIdentity.display_label
          ) {
            needsFix = true;
            return {
              target_asset_key: targetKey,
              target_asset_code: currentIdentity.asset_code,
              target_display_code: currentIdentity.display_code,
              target_display_label: currentIdentity.display_label,
            };
          }
        }
        return link;
      });
      if (needsFix) {
        fixedRelations.scene_links = fixedSceneLinks;
      }
    }

    return needsFix ? { ...asset, relations: fixedRelations } : asset;
  });

  return { ...draft, assets: fixedAssets };
}

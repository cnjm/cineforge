import React from "react";
import { Plus, X } from "lucide-react";
import { currentDraftCharacterOptions, isOwnerAssignableProp, ownerCharacterRelation, selectedOwnerCharacterKey, type OwnerCharacterOption } from "../../assetOwner";
import { assetReviewFieldLabel, assetTypeDisplayLabel, assetWorkflowStatus } from "../../assetReviewDisplay";
import { Field } from "../../shared/components";
import { LongTextTextarea } from "../../shared/LongText";
import { isRequiredPendingReview, promptRecordMatchesAsset } from "../../utils";
import type {
  AssetNormalizationReviewItem,
  BreakdownDraftAsset,
} from "../../types";

type AssetMode = "output" | "edit";
type AssetTypeKey = "character" | "scene" | "prop" | "music";

type AssetWorkbenchProps = {
  assets: BreakdownDraftAsset[];
  costumeDesigns?: Record<string, unknown>[];
  sceneOptions?: BreakdownDraftAsset[];
  mode: AssetMode;
  inventoryConfirmed?: boolean;
  readOnly?: boolean;
  onUpdateAsset?: (clientAssetKey: string, asset: BreakdownDraftAsset) => Promise<boolean | void> | boolean | void;
  onDeleteAsset?: (clientAssetKey: string) => void;
  onAddAsset?: (type: AssetTypeKey) => void;
  onCompleteReviews?: (values: AssetReviewCompletion[]) => Promise<boolean | void>;
  projectId?: string;
  episodeCode?: string;
};

export type AssetReviewCompletion = {
  clientAssetKey: string;
  reviewId: string;
  humanInput: string;
  selectedScenes?: BreakdownDraftAsset[];
  selectedCharacter?: OwnerCharacterOption;
  mode: "adopt" | "modify" | "defer" | "ignore";
};

const TYPE_TABS: Array<{ key: AssetTypeKey; label: string }> = [
  { key: "character", label: "人物" },
  { key: "scene", label: "场景" },
  { key: "prop", label: "道具" },
  { key: "music", label: "音乐" },
];

const PRIORITIES = ["S", "A", "B", "C"] as const;

export function AssetWorkbench({
  assets,
  costumeDesigns = [],
  sceneOptions = [],
  mode,
  inventoryConfirmed = false,
  readOnly = false,
  onUpdateAsset,
  onDeleteAsset,
  onAddAsset,
  onCompleteReviews,
  episodeCode,
}: AssetWorkbenchProps) {
  const musicRows = plannedMusicRows(assets, episodeCode);
  const availableTypes = TYPE_TABS.filter((tab) => tab.key === "music" ? musicRows.length : assets.some((asset) => asset.asset_type === tab.key));
  const [activeType, setActiveType] = React.useState<AssetTypeKey>(availableTypes[0]?.key || "character");
  const [activeKey, setActiveKey] = React.useState<string | null>(null);
  const [editing, setEditing] = React.useState(false);
  const [formAsset, setFormAsset] = React.useState<BreakdownDraftAsset | null>(null);
  const [reviewAssetKey, setReviewAssetKey] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (!availableTypes.some((tab) => tab.key === activeType)) setActiveType(availableTypes[0]?.key || "character");
  }, [activeType, availableTypes]);

  const rows = activeType === "music" ? [] : assets.filter((asset) => asset.asset_type === activeType);
  const characterOptions = currentDraftCharacterOptions(assets);
  const scenes = uniqueAssetsByKey([...sceneOptions.filter((asset) => asset.asset_type === "scene"), ...assets.filter((asset) => asset.asset_type === "scene")]);
  const activeAsset = activeKey ? assets.find((asset) => asset.client_asset_key === activeKey) : undefined;
  const reviewAsset = reviewAssetKey ? assets.find((asset) => asset.client_asset_key === reviewAssetKey) : undefined;

  function openAsset(asset: BreakdownDraftAsset, edit = false) {
    setActiveKey(asset.client_asset_key);
    setFormAsset(structuredClone(asset));
    setEditing(edit);
  }

  async function saveAsset() {
    if (!formAsset || !onUpdateAsset) return;
    const saved = await onUpdateAsset(formAsset.client_asset_key, formAsset);
    if (saved !== false) setEditing(false);
  }

  if (!assets.length && !musicRows.length) return <div className="empty-hint">暂无资产。</div>;

  return <>
    <div className="asset-type-tabs" role="tablist" aria-label="资产类型">
      {TYPE_TABS.map((tab) => {
        const count = tab.key === "music" ? musicRows.length : assets.filter((asset) => asset.asset_type === tab.key).length;
        return <button className={`asset-type-tab ${activeType === tab.key ? "active" : ""}`} disabled={!count} key={tab.key} onClick={() => setActiveType(tab.key)} role="tab" type="button">{tab.label}<span>{count}</span></button>;
      })}
    </div>

    {activeType === "music" ? <MusicRows rows={musicRows} /> : <div className="asset-priority-groups">
      {PRIORITIES.map((priority) => {
        const priorityRows = rows.filter((asset) => asset.priority === priority);
        if (!priorityRows.length) return null;
        const fields = tableFields(activeType, mode);
        const showReviewCols = activeType === "scene" || mode === "output";
        const showAppearanceCol = (activeType === "character" || activeType === "prop") && mode === "output";
        return <section className="asset-priority-group" key={priority}>
          <div className="asset-priority-head"><strong>{priority} 级</strong><span>{priorityRows.length} 项</span></div>
          <div className="asset-compact-table-wrap">
            <table className={`asset-compact-table ${activeType} ${mode}`}>
              <colgroup>
                <col className="asset-code-col" />
                <col className="asset-name-col" />
                {activeType === "scene" ? <><col className="asset-core-field-col" /><col className="asset-core-field-col" /></> : <><col className="asset-core-field-col" />{showAppearanceCol ? <col className="asset-appearance-col" /> : null}</>}
                <col className="asset-summary-col" />
                {showReviewCols ? <><col className="asset-uncertainty-col" /><col className="asset-suggestion-col" /><col className="asset-human-col" /></> : null}
                <col className="asset-status-col" />
                <col className="asset-actions-col" />
              </colgroup>
              <thead><tr>
                <th className="asset-code-col">编号</th>
                <th className="asset-name-col">名称</th>
                {activeType === "scene" ? <><th className="asset-core-field-col">地点 / 时间</th><th className="asset-core-field-col">叙事功能</th></> : <><th className="asset-core-field-col">{activeType === "character" ? "角色定位" : activeType === "prop" ? "道具类型" : "核心定位"}</th>{showAppearanceCol ? <th className="asset-appearance-col">{activeType === "prop" ? "外观 / 状态" : "外貌 / 年龄"}</th> : null}</>}
                <th className="asset-summary-col">方案摘要</th>
                {showReviewCols ? <><th className="asset-uncertainty-col">不确定项</th><th className="asset-suggestion-col">Agent 建议</th><th className="asset-human-col">人工完善</th></> : null}
                <th className="asset-status-col">状态</th>
                <th className="asset-actions-col">操作</th>
              </tr></thead>
              <tbody>{priorityRows.map((asset) => {
                const pending = requiredPendingReviews(asset);
                const assetConfirmed = asset.code_state === "formal";
                const assetFields = tableFields(activeType, mode);
                return <tr key={asset.client_asset_key}>
                  <td className="asset-code-col">{asset.display_code}</td>
                  <td className="asset-name-col">{asset.display_label}</td>
                  {assetFields.map((field) => <td className={field.key === (activeType === "character" ? "appearance" : "appearance") ? "asset-appearance-col" : "asset-core-field-col"} key={field.key}>{field.value(asset) || "待补充"}</td>)}
                  <td className="asset-summary-col">{asset.description || assetAttributeSummary(asset) || "待补充"}</td>
                  {showReviewCols ? <><td className="asset-uncertainty-col"><ReviewSummary items={asset.review_items} kind="issue" /></td><td className="asset-suggestion-col"><ReviewSummary items={asset.review_items} kind="suggestion" /></td><td className="asset-human-col"><ReviewSummary items={asset.review_items} kind="human" /></td></> : null}
                  <td className="asset-status-col"><span className={`badge ${pending.length || !assetConfirmed ? "sun" : "mint"}`}>{assetWorkflowStatus(pending.length, assetConfirmed)}</span></td>
                  <td className="asset-actions-col"><div className="table-actions">
                    {pending.length && mode === "edit" ? <button className="btn primary" disabled={readOnly} type="button" onClick={() => setReviewAssetKey(asset.client_asset_key)}>完善</button> : null}
                    <button className="btn btn-view-plan" type="button" onClick={() => openAsset(asset)}>查看方案</button>
                    {/* TODO: 后续实现单个资产确认功能 */}
                    {!assetConfirmed ? <button className="btn" type="button" disabled>确认</button> : null}
                    {mode === "edit" ? <button className="btn danger" disabled={readOnly} type="button" onClick={() => onDeleteAsset?.(asset.client_asset_key)}>删除</button> : null}
                  </div></td>
                </tr>;
              })}</tbody>
            </table>
          </div>
        </section>;
      })}
    </div>}

    {mode === "edit" && onAddAsset && activeType !== "music" ? (
      <div className="asset-tab-add">
        <button className="btn asset-add-button" disabled={readOnly} type="button" onClick={() => onAddAsset(activeType)}>
          <Plus size={14} strokeWidth={2.2} aria-hidden="true" />
          {`新增${TYPE_TABS.find((tab) => tab.key === activeType)?.label || "资产"}`}
        </button>
      </div>
    ) : null}

    {activeAsset && formAsset ? <AssetModal
      asset={formAsset}
      characterOptions={characterOptions}
      design={promptRecordForAsset(activeAsset, costumeDesigns)}
      editing={editing}
      readOnly={readOnly}
      scenes={scenes}
      onChange={setFormAsset}
      onClose={() => { setActiveKey(null); setFormAsset(null); setEditing(false); }}
      onEdit={() => setEditing(true)}
      onCancel={() => { setFormAsset(structuredClone(activeAsset)); setEditing(false); }}
      onSave={() => void saveAsset()}
    /> : null}

    {reviewAsset ? <ReviewModal asset={reviewAsset} characterOptions={characterOptions} scenes={scenes} onClose={() => setReviewAssetKey(null)} onSave={async (values) => {
      const saved = await onCompleteReviews?.(values);
      if (saved !== false) setReviewAssetKey(null);
      return saved;
    }} /> : null}
  </>;
}

type FieldWidth = "compact" | "half" | "full";

function AssetFieldCard({ label, value, width, showSource, onViewSource }: {
  label: string;
  value?: React.ReactNode;
  width?: FieldWidth;
  showSource?: boolean;
  onViewSource?: () => void;
}) {
  if (value === undefined || value === null || value === "") value = "—";
  const className = `asset-scheme-field${width ? ` asset-scheme-field--${width}` : ""}${showSource ? " asset-scheme-field--has-source" : ""}`;
  return (
    <div className={className}>
      <b>{label}</b>
      <p>{value}</p>
      {showSource && onViewSource ? <button className="asset-field-source-button" type="button" onClick={onViewSource}>查看原文</button> : null}
    </div>
  );
}

function AssetReadView({ asset, onViewSource }: {
  asset: BreakdownDraftAsset;
  onViewSource?: () => void;
}) {
  const t = asset.asset_type;
  const a = asset.attributes;
  const sourceFields = t === "character"
    ? ["制作要求", "外貌", "核心要求", "关系说明"]
    : t === "scene"
      ? ["制作要求", "叙事功能", "视觉目标", "关系说明"]
      : t === "prop"
        ? ["制作要求", "外观", "状态变化", "关系说明"]
        : [];
  const hasSource = Boolean(onViewSource);
  const show = (label: string) => hasSource && sourceFields.includes(label);
  return (
    <div className="asset-scheme-fields asset-prototype-fields">
      <AssetFieldCard label="编号" value={asset.display_code || "—"} width="compact" />
      <AssetFieldCard label="名称" value={asset.name || "—"} width="compact" />
      <AssetFieldCard label="优先级" value={asset.priority || "—"} width="compact" />
      <AssetFieldCard label="制作要求" value={asset.description || "—"} width="full" showSource={show("制作要求")} onViewSource={onViewSource} />
      {t === "character" ? (
        <>
          <AssetFieldCard label="角色定位" value={a.role || "—"} width="half" />
          <AssetFieldCard label="角色类型" value={a.character_type || "—"} width="half" />
          <AssetFieldCard label="外貌" value={a.appearance || "—"} width="full" showSource={show("外貌")} onViewSource={onViewSource} />
          <AssetFieldCard label="核心要求" value={a.core_requirement || "—"} width="full" showSource={show("核心要求")} onViewSource={onViewSource} />
        </>
      ) : null}
      {t === "scene" ? (
        <>
          <AssetFieldCard label="地点" value={a.location || "—"} width="half" />
          <AssetFieldCard label="时间" value={a.time || "—"} width="half" />
          <AssetFieldCard label="叙事功能" value={a.narrative_function || "—"} width="full" showSource={show("叙事功能")} onViewSource={onViewSource} />
          <AssetFieldCard label="视觉目标" value={a.visual_goal || "—"} width="full" showSource={show("视觉目标")} onViewSource={onViewSource} />
        </>
      ) : null}
      {t === "prop" ? (
        <>
          <AssetFieldCard label="道具类型" value={a.prop_type || "—"} width="compact" />
          <AssetFieldCard label="出处" value={a.source || "—"} width="compact" />
          <AssetFieldCard label="外观" value={a.appearance || "—"} width="full" showSource={show("外观")} onViewSource={onViewSource} />
          <AssetFieldCard label="状态变化" value={a.status_change || "—"} width="full" showSource={show("状态变化")} onViewSource={onViewSource} />
        </>
      ) : null}
      <AssetFieldCard label="关系说明" value={asset.relations.relationship_text || "—"} width="full" showSource={show("关系说明")} onViewSource={onViewSource} />
    </div>
  );
}

function extractSourceText(asset: BreakdownDraftAsset): string {
  const evidence = asset.relations?.evidence;
  if (Array.isArray(evidence) && evidence.length > 0) {
    const texts = evidence.map((item) => {
      if (typeof item === "string") return item;
      if (item && typeof item === "object") {
        const obj = item as Record<string, unknown>;
        const text = obj.text ?? obj.source_text ?? obj.content ?? obj.evidence;
        if (typeof text === "string" && text.trim()) return text.trim();
      }
      return "";
    }).filter(Boolean);
    if (texts.length > 0) return texts.join("\n\n");
  }
  return "暂无关联的剧本原文。";
}

function AssetModal({ asset, design, scenes, characterOptions, editing, readOnly, onChange, onClose, onEdit, onCancel, onSave }: {
  asset: BreakdownDraftAsset;
  design?: Record<string, unknown>;
  scenes: BreakdownDraftAsset[];
  characterOptions: OwnerCharacterOption[];
  editing: boolean;
  readOnly: boolean;
  onChange: (asset: BreakdownDraftAsset) => void;
  onClose: () => void;
  onEdit: () => void;
  onCancel: () => void;
  onSave: () => void;
}) {
  const [section, setSection] = React.useState<"basic" | "relations" | "production">("basic");
  const [sourceOpen, setSourceOpen] = React.useState(false);
  const typeKey = asset.asset_type;
  function patch(patchValue: Partial<BreakdownDraftAsset>) { onChange({ ...asset, ...patchValue } as BreakdownDraftAsset); }
  function patchAttributes(patchValue: Record<string, unknown>) { onChange({ ...asset, attributes: { ...asset.attributes, ...patchValue } } as BreakdownDraftAsset); }
  function toggleScene(scene: BreakdownDraftAsset) {
    const selected = asset.relations.scene_links.some((link) => link.target_asset_key === scene.client_asset_key);
    const link = { target_asset_key: scene.client_asset_key, target_asset_code: scene.asset_code, target_display_code: scene.display_code, target_display_label: scene.display_label };
    patch({ relations: { ...asset.relations, scene_links: selected ? asset.relations.scene_links.filter((item) => item.target_asset_key !== scene.client_asset_key) : [...asset.relations.scene_links, link] } });
  }
  function updateOwner(key: string) {
    const option = characterOptions.find((item) => item.key === key);
    patch({ relations: { ...asset.relations, owner_character: ownerCharacterRelation(option) } });
  }
  return <div className="modal-backdrop" role="presentation" onClick={onClose}><section className="asset-scheme-modal" role="dialog" aria-modal="true" aria-label={`${asset.display_label}方案`} onClick={(event) => event.stopPropagation()}>
    <div className="modal-head"><div><h3>{asset.display_code} {asset.name}</h3><p>{assetTypeDisplayLabel(asset.asset_type)} · {asset.priority} 级 · {asset.code_state === "formal" ? "正式编号" : "编号待锁定"}</p></div><button className="asset-scheme-close" type="button" aria-label="关闭" title="关闭" onClick={onClose}><X aria-hidden="true" size={18} strokeWidth={1.8} /></button></div>
    <div className="asset-scheme-body">
      <div className="asset-scheme-tabs" role="tablist">
        <button className={section === "basic" ? "active" : ""} onClick={() => setSection("basic")} role="tab" type="button">{asset.asset_type === "character" ? "角色身份" : "基础方案"}</button>
        {asset.asset_type !== "scene" ? <button className={section === "relations" ? "active" : ""} onClick={() => setSection("relations")} role="tab" type="button">关联场景</button> : null}
        <button className={section === "production" ? "active" : ""} onClick={() => setSection("production")} role="tab" type="button">生产提示</button>
      </div>
      {section === "basic" ? (editing ? <div className="editable-grid asset-modal-edit-grid">
        <Field label="编号"><input disabled value={asset.display_code} /></Field>
        <Field label="名称"><input disabled={readOnly || !editing} value={asset.name} onChange={(event) => patch({ name: event.target.value, display_label: `${asset.display_code}-${event.target.value}` })} /></Field>
        <Field label="优先级"><select disabled={readOnly || !editing} value={asset.priority} onChange={(event) => patch({ priority: event.target.value as BreakdownDraftAsset["priority"] })}>{["S", "A", "B", "C"].map((value) => <option key={value}>{value}</option>)}</select></Field>
        <Field label="制作要求" full><LongTextTextarea title="制作要求" disabled={readOnly || !editing} value={asset.description || ""} onChange={(value) => patch({ description: value || null })} /></Field>
        <TypedAttributeFields asset={asset} disabled={readOnly || !editing} onChange={patchAttributes} />
        <Field label="关系说明" full><LongTextTextarea title="关系说明" disabled={readOnly || !editing} value={asset.relations.relationship_text || ""} onChange={(value) => patch({ relations: { ...asset.relations, relationship_text: value || null } })} /></Field>
        {asset.asset_type === "prop" && isOwnerAssignableProp(asset) ? <Field label="所属角色"><select disabled={readOnly || !editing} value={selectedOwnerCharacterKey(asset, characterOptions)} onChange={(event) => updateOwner(event.target.value)}><option value="">未指定</option>{characterOptions.map((option) => <option key={option.key} value={option.key}>{option.asset.display_label}</option>)}</select></Field> : null}
      </div> : <AssetReadView asset={asset} onViewSource={typeKey !== "music" ? () => setSourceOpen(true) : undefined} />) : null}
      {section === "relations" && asset.asset_type !== "scene" ? (() => {
        const selected = asset.relations.scene_links;
        const selectedKeys = new Set(selected.map((link) => link.target_asset_key));
        const sceneNames = selected.map((link) => scenes.find((s) => s.client_asset_key === link.target_asset_key)?.display_label || link.target_display_label || link.target_asset_code).filter(Boolean) as string[];
        const sourceIsManual = selected.length > 0;
        if (!editing) {
          return <section className="asset-relation-section"><header><strong>使用场景</strong></header><div className="asset-relation-read-list">{sceneNames.length ? sceneNames.map((name) => <div className="asset-relation-read-item" key={name}>{name}</div>) : <p>暂未关联场景</p>}</div></section>;
        }
        const selectedCount = selected.length;
        return <section className="asset-relation-section"><header><strong>使用场景</strong><span className={`badge ${sourceIsManual ? "mint" : "sky"}`}>{sourceIsManual ? "人工选择" : "Agent 匹配"}</span></header><div className="scene-multi-select"><div className="scene-multi-select-toolbar"><span>已选择 {selectedCount}/{scenes.length}</span><div><button className="btn" disabled={readOnly || !scenes.length || selectedCount === scenes.length} type="button" onClick={() => { const allKeys = new Set(scenes.map((s) => s.client_asset_key)); const allLinks = scenes.map((scene) => ({ target_asset_key: scene.client_asset_key, target_asset_code: scene.asset_code, target_display_code: scene.display_code, target_display_label: scene.display_label })); patch({ relations: { ...asset.relations, scene_links: allLinks.filter((link) => allKeys.has(link.target_asset_key)) } }); }}>全选</button><button className="btn" disabled={readOnly || !selectedCount} type="button" onClick={() => patch({ relations: { ...asset.relations, scene_links: [] } })}>清空</button></div></div><div className="scene-multi-select-options">{scenes.map((scene) => { const checked = selectedKeys.has(scene.client_asset_key); return <label key={scene.client_asset_key}><input checked={checked} disabled={readOnly} type="checkbox" onChange={() => toggleScene(scene)} /><span>{scene.display_label}</span></label>; })}{!scenes.length ? <span className="asset-review-empty">暂无已落库场景</span> : null}</div></div></section>;
      })() : null}
      {section === "production" ? <PromptRecordView design={design} /> : null}
    </div>
    <div className="asset-scheme-footer">{editing ? <><button className="btn" type="button" onClick={onCancel}>取消修改</button><button className="btn primary" disabled={readOnly} type="button" onClick={onSave}>保存修改</button></> : <button className="btn asset-scheme-edit" disabled={readOnly} type="button" onClick={onEdit}>编辑方案</button>}</div>
  </section>
  {sourceOpen ? <div className="modal-backdrop" role="presentation" onClick={(event) => { event.stopPropagation(); setSourceOpen(false); }}>
    <section className="long-text-modal asset-source-modal" role="dialog" aria-modal="true" aria-label={`${asset.name || "资产"}关联剧本原文`} onClick={(event) => event.stopPropagation()}>
      <div className="modal-head">
        <div><h3>{asset.display_code} {asset.name || "未命名"} · 剧本原文</h3><p>资产拆解引用文本</p></div>
        <button className="asset-scheme-close" type="button" aria-label="关闭" title="关闭" onClick={() => setSourceOpen(false)}><X aria-hidden="true" size={18} strokeWidth={1.8} /></button>
      </div>
      <pre className="long-text-modal-body asset-source-modal__body">{extractSourceText(asset)}</pre>
    </section>
  </div> : null}
  </div>;
}

function TypedAttributeFields({ asset, disabled, onChange }: { asset: BreakdownDraftAsset; disabled: boolean; onChange: (patch: Record<string, unknown>) => void }) {
  if (asset.asset_type === "character") return <>
    <Field label="角色定位"><input disabled={disabled} value={asset.attributes.role || ""} onChange={(event) => onChange({ role: event.target.value || null })} /></Field>
    <Field label="角色类型"><input disabled={disabled} value={asset.attributes.character_type || ""} onChange={(event) => onChange({ character_type: event.target.value || null })} /></Field>
    <Field label="外貌" full><LongTextTextarea title="外貌" disabled={disabled} value={asset.attributes.appearance || ""} onChange={(value) => onChange({ appearance: value || null })} /></Field>
    <Field label="核心要求" full><LongTextTextarea title="核心要求" disabled={disabled} value={asset.attributes.core_requirement || ""} onChange={(value) => onChange({ core_requirement: value || null })} /></Field>
  </>;
  if (asset.asset_type === "scene") return <>
    <Field label="地点"><input disabled={disabled} value={asset.attributes.location || ""} onChange={(event) => onChange({ location: event.target.value || null })} /></Field>
    <Field label="时间"><input disabled={disabled} value={asset.attributes.time || ""} onChange={(event) => onChange({ time: event.target.value || null })} /></Field>
    <Field label="叙事功能" full><LongTextTextarea title="叙事功能" disabled={disabled} value={asset.attributes.narrative_function || ""} onChange={(value) => onChange({ narrative_function: value || null })} /></Field>
    <Field label="视觉目标" full><LongTextTextarea title="视觉目标" disabled={disabled} value={asset.attributes.visual_goal || ""} onChange={(value) => onChange({ visual_goal: value || null })} /></Field>
  </>;
  return <>
    <Field label="道具类型"><input disabled={disabled} value={asset.attributes.prop_type || ""} onChange={(event) => onChange({ prop_type: event.target.value || null })} /></Field>
    <Field label="出处"><input disabled={disabled} value={asset.attributes.source || ""} onChange={(event) => onChange({ source: event.target.value || null })} /></Field>
    <Field label="外观" full><LongTextTextarea title="外观" disabled={disabled} value={asset.attributes.appearance || ""} onChange={(value) => onChange({ appearance: value || null })} /></Field>
    <Field label="状态变化" full><LongTextTextarea title="状态变化" disabled={disabled} value={asset.attributes.status_change || ""} onChange={(value) => onChange({ status_change: value || null })} /></Field>
  </>;
}

function PromptRecordView({ design }: { design?: Record<string, unknown> }) {
  if (!design) return <section className="asset-production-empty">尚未生成独立提示词记录。</section>;
  const ageStages = Array.isArray(design.age_stages) ? design.age_stages as Record<string, unknown>[] : [];
  const prompts = Array.isArray(design.view_prompts) ? design.view_prompts as Record<string, unknown>[] : [];
  // 单条 prompt 展示：取第一条或主 prompt
  const mainPrompt = prompts[0];
  const title = mainPrompt
    ? String(mainPrompt.title || mainPrompt.code || `-`)
    : String(design.prompt_title || design.title || "-");
  const promptText = mainPrompt
    ? String(mainPrompt.prompt || "")
    : String(design.prompt || "");
  if (!promptText) return <section className="asset-production-empty">尚未生成独立提示词记录。</section>;
  return <section className="asset-production-simple"><div><span>生产提示</span><strong>{title}</strong></div><p>{promptText}</p></section>;
}

function ReviewModal({ asset, scenes, characterOptions, onClose, onSave }: { asset: BreakdownDraftAsset; scenes: BreakdownDraftAsset[]; characterOptions: OwnerCharacterOption[]; onClose: () => void; onSave: (values: AssetReviewCompletion[]) => Promise<boolean | void> }) {
  const items = requiredPendingReviews(asset);
  const [values, setValues] = React.useState<Record<string, AssetReviewCompletion>>(() => Object.fromEntries(items.map((item) => [item.review_id, {
    clientAssetKey: asset.client_asset_key,
    reviewId: item.review_id,
    humanInput: "",
    mode: "modify",
  }])));
  const [saving, setSaving] = React.useState(false);
  const [feedback, setFeedback] = React.useState("");
  function update(item: AssetNormalizationReviewItem, patch: Partial<AssetReviewCompletion>) { setValues((current) => ({ ...current, [item.review_id]: { ...current[item.review_id], ...patch } })); }
  async function submit() {
    setSaving(true);
    setFeedback("");
    try {
      const saved = await onSave(Object.values(values));
      if (saved === false) setFeedback("保存未成功，请检查填写内容后重试。");
    } catch (error) {
      setFeedback(error instanceof Error ? error.message : "保存未成功，请稍后重试。");
    } finally {
      setSaving(false);
    }
  }
  const incompleteCount = items.filter((item) => !reviewCompletionReady(item, values[item.review_id])).length;
  return <div className="modal-backdrop" role="presentation" onClick={onClose}><section className="asset-scheme-modal asset-completion-modal" role="dialog" aria-modal="true" aria-label={`${asset.display_label}完善`} onClick={(event) => event.stopPropagation()}>
    <div className="modal-head"><div><h3>{asset.display_label} · 完善信息</h3><p>先填写人工确认内容；Agent 建议仅供参考，不会自动采用。</p></div><button className="btn" type="button" onClick={onClose}><X aria-hidden="true" size={16} />关闭</button></div>
    <div className="asset-completion-body"><div className="completion-item-list">{items.map((item, index) => <ReviewCompletionItem
      characterOptions={characterOptions}
      index={index}
      item={item}
      key={item.review_id}
      scenes={scenes}
      value={values[item.review_id]}
      onUpdate={(patch) => update(item, patch)}
    />)}</div></div>
    <div className="asset-scheme-footer"><span className={`completion-save-status ${feedback ? "error" : ""}`}>{feedback || (incompleteCount ? `还有 ${incompleteCount} 项未填写` : "已填写完整，可以保存")}</span><button className="btn" disabled={saving} type="button" onClick={onClose}>取消</button><button className="btn primary" disabled={saving || incompleteCount > 0} type="button" onClick={() => void submit()}>{saving ? "保存中" : "保存完善内容"}</button></div>
  </section></div>;
}

function ReviewCompletionItem({ item, index, value, scenes, characterOptions, onUpdate }: {
  item: AssetNormalizationReviewItem;
  index: number;
  value?: AssetReviewCompletion;
  scenes: BreakdownDraftAsset[];
  characterOptions: OwnerCharacterOption[];
  onUpdate: (patch: Partial<AssetReviewCompletion>) => void;
}) {
  const ownerField = item.target_field === "relations.owner_character";
  const sceneField = item.target_field === "relations.scene_links";
  const fieldLabel = assetReviewFieldLabel(item.target_field);
  const suggestion = item.suggestion?.trim() || "";
  const suggestedCharacter = ownerField ? suggestedOwnerCharacter(suggestion, characterOptions) : undefined;
  const suggestedSceneList = sceneField ? suggestedScenes(suggestion, scenes) : [];
  const adopt = !suggestion ? undefined
    : ownerField ? suggestedCharacter ? () => onUpdate({ selectedCharacter: suggestedCharacter, humanInput: suggestedCharacter.name, mode: "adopt" }) : undefined
      : sceneField ? suggestedSceneList.length ? () => onUpdate({ selectedScenes: suggestedSceneList, humanInput: suggestedSceneList.map((scene) => scene.display_label).join("、"), mode: "adopt" }) : undefined
        : () => onUpdate({ humanInput: suggestion, mode: "adopt" });

  return <article className="completion-item">
    <header className="completion-item-head"><strong>{index + 1}. {fieldLabel}</strong><span>必须填写</span></header>
    <div className="completion-context"><b>需要确认</b><p>{item.issue}</p></div>
    <ReviewSuggestionPanel suggestion={suggestion} onAdopt={adopt} />
    {ownerField ? <Field label="人工确认内容" required><select value={value?.selectedCharacter?.key || ""} onChange={(event) => { const selectedCharacter = characterOptions.find((option) => option.key === event.target.value); onUpdate({ selectedCharacter, humanInput: selectedCharacter?.name || "", mode: "modify" }); }}><option value="">请选择所属角色</option>{characterOptions.map((option) => <option key={option.key} value={option.key}>{option.asset.display_label}</option>)}</select></Field>
      : sceneField ? <Field label="人工确认内容" required><div className="scene-multi-select">{scenes.map((scene) => <label key={scene.client_asset_key}><input checked={Boolean(value?.selectedScenes?.some((selected) => selected.client_asset_key === scene.client_asset_key))} type="checkbox" onChange={() => { const selected = value?.selectedScenes || []; const next = selected.some((entry) => entry.client_asset_key === scene.client_asset_key) ? selected.filter((entry) => entry.client_asset_key !== scene.client_asset_key) : [...selected, scene]; onUpdate({ selectedScenes: next, humanInput: next.map((entry) => entry.display_label).join("、"), mode: "modify" }); }} /><span>{scene.display_label}</span></label>)}</div></Field>
        : <Field label="人工确认内容" required><LongTextTextarea title={`${fieldLabel}人工确认内容`} placeholder="请填写最终采用的明确内容，或点击上方“采用建议”" value={value?.humanInput || ""} onChange={(humanInput) => onUpdate({ humanInput, mode: "modify" })} /></Field>}
  </article>;
}

function ReviewSuggestionPanel({ suggestion, onAdopt }: { suggestion: string; onAdopt?: () => void }) {
  if (!suggestion) return <div className="completion-suggestion empty"><b>Agent 建议</b><p>未提供建议，请人工填写。</p></div>;
  return <div className="completion-suggestion"><div><b>Agent 建议</b><button className="btn" disabled={!onAdopt} title={onAdopt ? "采用 Agent 建议" : "建议未对应现有选项，请在下方人工选择"} type="button" onClick={onAdopt}>采用建议</button></div><p>{suggestion}</p></div>;
}

function suggestedOwnerCharacter(suggestion: string, options: OwnerCharacterOption[]) {
  const text = normalizedSuggestionText(suggestion);
  return options.find((option) => [option.name, option.shortCode, option.asset.display_label].some((value) => value && text.includes(normalizedSuggestionText(value))));
}

function suggestedScenes(suggestion: string, scenes: BreakdownDraftAsset[]) {
  const text = normalizedSuggestionText(suggestion);
  return scenes.filter((scene) => [scene.name, scene.display_code, scene.display_label].some((value) => value && text.includes(normalizedSuggestionText(value))));
}

function normalizedSuggestionText(value: string) {
  return value.toLowerCase().replace(/\s+/g, "");
}

function reviewCompletionReady(item: AssetNormalizationReviewItem, value?: AssetReviewCompletion) {
  if (!value) return false;
  if (item.target_field === "relations.owner_character") return Boolean(value.selectedCharacter);
  if (item.target_field === "relations.scene_links") return Boolean(value.selectedScenes?.length);
  return Boolean(value.humanInput.trim());
}

function MusicRows({ rows }: { rows: Record<string, unknown>[] }) {
  return <div className="asset-compact-table-wrap"><table className="asset-compact-table music"><thead><tr><th>类型</th><th>名称</th><th>关联</th><th>说明</th></tr></thead><tbody>{rows.map((row, index) => <tr key={String(row.client_asset_key || row.context_key || index)}><td>{musicTypeLabel(String(row.asset_type || row.music_type || "music"))}</td><td>{String(row.display_label || row.name || "未命名")}</td><td>{musicRelationLabel(row)}</td><td>{String(row.description || row.prompt || "待生成")}</td></tr>)}</tbody></table></div>;
}

function musicRelationLabel(row: Record<string, unknown>) {
  const character = String(row.character_code || "").trim();
  if (character) return character;
  const episode = String(row.episode_code || "").trim();
  if (episode) return episode;
  return "项目";
}

function ReviewSummary({ items, kind }: { items: AssetNormalizationReviewItem[]; kind: "issue" | "suggestion" | "human" }) {
  const values = items
    .map((item) => kind === "issue" ? item.issue : kind === "suggestion" ? item.suggestion : item.human_input)
    .filter((value): value is string => Boolean(value?.trim()));
  if (!values.length) return <span className="asset-review-empty">{kind === "human" && items.length ? "待人工完善" : "无"}</span>;
  return <div className={`asset-review-summary ${kind === "suggestion" ? "suggestion" : kind}`} title={values.join("\n")}>
    {values.map((value, index) => <span key={`${value}-${index}`}>{value}</span>)}
  </div>;
}

function tableFields(type: Exclude<AssetTypeKey, "music">, mode: AssetMode = "output"): Array<{ key: string; label: string; value: (asset: BreakdownDraftAsset) => string }> {
  if (type === "character") {
    const fields: Array<{ key: string; label: string; value: (asset: BreakdownDraftAsset) => string }> = [
      { key: "role", label: "角色定位", value: (asset) => asset.asset_type === "character" ? asset.attributes.role || asset.attributes.character_type || "" : "" },
    ];
    if (mode === "output") {
      fields.push({ key: "appearance", label: "外貌/年龄", value: (asset) => asset.asset_type === "character" ? [asset.attributes.age, asset.attributes.appearance].filter(Boolean).join("；") : "" });
    }
    return fields;
  }
  if (type === "scene") return [
    { key: "location", label: "地点/时间", value: (asset) => asset.asset_type === "scene" ? [asset.attributes.location, asset.attributes.time].filter(Boolean).join("；") : "" },
    { key: "function", label: "叙事功能", value: (asset) => asset.asset_type === "scene" ? asset.attributes.narrative_function || asset.attributes.visual_goal || "" : "" },
  ];
  const propFields: Array<{ key: string; label: string; value: (asset: BreakdownDraftAsset) => string }> = [
    { key: "type", label: "道具类型", value: (asset) => asset.asset_type === "prop" ? asset.attributes.prop_type || "" : "" },
  ];
  if (mode === "output") {
    propFields.push({ key: "appearance", label: "外观/状态", value: (asset) => asset.asset_type === "prop" ? [asset.attributes.appearance, asset.attributes.status_change].filter(Boolean).join("；") : "" });
  }
  return propFields;
}

function plannedMusicRows(assets: BreakdownDraftAsset[], episodeCode?: string): Record<string, unknown>[] {
  const episode = String(episodeCode || "").trim();
  const fixed: Record<string, unknown>[] = [
    // Theme music is project-level: one master shared across the whole project.
    { client_asset_key: "audio-plan-theme", asset_type: "theme_music", display_label: "主题音乐", description: "项目固定主题音乐生产计划（全项目共用一首）" },
    // Background music is episode-level: one master per episode.
    { client_asset_key: "audio-plan-background", asset_type: "background_music", display_label: "背景音乐", description: "分集固定背景音乐生产计划", episode_code: episode || undefined },
  ];
  const voices = assets.filter((asset) => (
    asset.asset_type === "character"
    && ["S", "A"].includes(asset.priority)
    && characterHasDialogue(asset)
  )).map((asset) => ({
    client_asset_key: `audio-plan-voice:${asset.client_asset_key}`,
    asset_type: "voice_profile",
    display_label: `${asset.display_label} 角色音色`,
    character_code: asset.display_code,
    description: "有台词的 S/A 级角色固定创建角色音色任务",
  }));
  return [...fixed, ...voices];
}

export function characterHasDialogue(asset: BreakdownDraftAsset) {
  return asset.asset_type === "character" && asset.attributes.has_dialogue;
}

function musicTypeLabel(value: string) {
  const normalized = value.toLowerCase();
  if (normalized.includes("voice")) return "角色音色";
  if (normalized.includes("theme")) return "主题音乐";
  return "背景音乐";
}

function requiredPendingReviews(asset: BreakdownDraftAsset) {
  return asset.review_items.filter(isRequiredPendingReview);
}

function promptRecordForAsset(asset: BreakdownDraftAsset, records: Record<string, unknown>[]) {
  return records.find((record) => promptRecordMatchesAsset(record, asset));
}

function uniqueAssetsByKey(assets: BreakdownDraftAsset[]) {
  return Array.from(new Map(assets.map((asset) => [asset.client_asset_key, asset])).values());
}

function assetAttributeSummary(asset: BreakdownDraftAsset) {
  if (asset.asset_type === "character") return [asset.attributes.role, asset.attributes.character_type, asset.attributes.appearance].filter(Boolean).join("；");
  if (asset.asset_type === "scene") return [asset.attributes.location, asset.attributes.time, asset.attributes.narrative_function].filter(Boolean).join("；");
  return [asset.attributes.prop_type, asset.attributes.appearance, asset.attributes.status_change].filter(Boolean).join("；");
}

function relationSummary(asset: BreakdownDraftAsset) {
  const scenes = asset.relations.scene_links.map((item) => item.target_display_label);
  const owner = asset.relations.owner_character?.target_display_label;
  return [owner, ...scenes].filter(Boolean).join("；");
}

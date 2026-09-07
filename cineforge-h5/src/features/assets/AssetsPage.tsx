import React from "react";
import { AudioLines, ChevronLeft, ChevronRight, Film, Image as ImageIcon, Maximize2, Search, X } from "lucide-react";
import { api, API_BASE, getAuthToken } from "../../api";
import { PageHeader } from "../../shared/components";
import { isActiveSubmission } from "../../submissionLifecycle";
import type { Asset, AssetLibraryItemResponse, Project, Storyboard, Submission, Task } from "../../types";
import { assetCodeDisplay } from "../../utils";

type AssetsPageProps = {
  apiAssets: Asset[];
  projects: Project[];
  storyboards: Storyboard[];
  tasks: Task[];
};

type LibraryCategory = "character" | "scene" | "prop" | "audio" | "keyframe" | "video" | "final" | "other";
type CompanyCategory = "character" | "scene" | "prop" | "audio" | "video";

type LibraryVersion = {
  id: string;
  versionNo: number;
  batchVersionNo?: number | null;
  fileId?: string | null;
  filePath?: string | null;
  fileType: "image" | "video" | "audio";
  fileName: string;
  searchNames: string[];
  isPrimary: boolean;
  status: string;
  modelName?: string | null;
  createdAt?: string | null;
  contextLabel?: string;
  mediaRole?: "image" | "keyframe" | "video" | "audio";
  viewLabel?: string | null;
  stateLabel?: string | null;
  description?: string | null;
};

type LibraryItem = {
  id: string;
  kind: "asset" | "production";
  category: LibraryCategory;
  categoryLabel: string;
  title: string;
  code: string;
  description: string;
  projectId?: string | null;
  projectCode: string;
  projectName: string;
  episodeCodes: string[];
  sourceLabel: string;
  episodeCode: string;
  sceneCode: string;
  sceneName: string;
  storyboardCode: string;
  versions: LibraryVersion[];
  current: LibraryVersion | null;
  finalizedAt: string;
  mergeKey?: string;
  storyboardMergeKey?: string;
};

const categoryLabels: Record<LibraryCategory, string> = {
  character: "人物母版",
  scene: "场景母版",
  prop: "道具母版",
  audio: "音频",
  keyframe: "关键帧",
  video: "分镜视频",
  final: "成片",
  other: "其他资产",
};

const companyTabs: Array<{ value: "" | CompanyCategory; label: string }> = [
  { value: "", label: "全部" },
  { value: "character", label: "人物" },
  { value: "scene", label: "场景" },
  { value: "prop", label: "道具" },
  { value: "audio", label: "音频" },
  { value: "video", label: "视频" },
];

export function AssetsPage({ apiAssets, projects, storyboards, tasks }: AssetsPageProps) {
  const [query, setQuery] = React.useState("");
  const [category, setCategory] = React.useState<CompanyCategory | "">("");
  const [viewerSelection, setViewerSelection] = React.useState<{ itemId: string; versionId?: string } | null>(null);
  const [libraryResponse, setLibraryResponse] = React.useState<AssetLibraryItemResponse[] | null>(null);
  const [libraryApiAvailable, setLibraryApiAvailable] = React.useState<boolean | null>(null);
  React.useEffect(() => {
    const controller = new AbortController();
    setLibraryApiAvailable(null);
    api.get<AssetLibraryItemResponse[]>("/assets/library?include_versions=true").then((response) => {
      if (controller.signal.aborted) return;
      setLibraryResponse(response);
      setLibraryApiAvailable(true);
    }).catch(() => { if (!controller.signal.aborted) setLibraryApiAvailable(false); });
    return () => controller.abort();
  }, []);
  const items = React.useMemo(() => {
    if (libraryApiAvailable === null) return [];
    const source = libraryApiAvailable
      ? normalizeLibraryResponse(libraryResponse, projects, apiAssets)
      : buildLibraryItems(apiAssets, tasks, projects, storyboards);
    return mergeStoryboardCards(source.filter((item) => (
      Boolean(item.current?.isPrimary)
      || (["scene", "prop"].includes(item.category) && item.versions.length > 0)
    )))
      .filter((item): item is LibraryItem & { category: CompanyCategory } => ["character", "scene", "prop", "audio", "video"].includes(item.category))
      .sort((left, right) => sortableTime(right.finalizedAt) - sortableTime(left.finalizedAt) || left.title.localeCompare(right.title, "zh-CN"));
  }, [apiAssets, libraryApiAvailable, libraryResponse, projects, storyboards, tasks]);
  const filtered = React.useMemo(() => items.filter((item) => {
    if (category && item.category !== category) return false;
    const queryTokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
    if (!queryTokens.length) return true;
    const haystack = [
      item.title,
      item.categoryLabel,
      item.code,
      item.description,
      item.projectCode,
      item.projectName,
      item.sourceLabel,
      ...item.episodeCodes,
      item.episodeCode,
      item.sceneCode,
      item.sceneName,
      item.storyboardCode,
      item.current?.fileName,
      ...item.versions.flatMap((version) => [version.fileName, ...version.searchNames]),
    ].join(" ").toLowerCase();
    return queryTokens.every((token) => haystack.includes(token));
  }), [category, items, query]);
  const viewerItem = items.find((item) => item.id === viewerSelection?.itemId);

  return <>
    <PageHeader title="公司资产库" desc="统一查看已审核定版的图片、音频和视频资产。" />
    <section className="asset-library-toolbar" aria-label="资产筛选">
      <label className="asset-library-search"><Search aria-hidden="true" size={17} /><input placeholder="搜索业务编码、标准文件名、项目或场景" value={query} onChange={(event) => setQuery(event.target.value)} /></label>
      <span>{filtered.length} 项</span>
    </section>
    <nav className="asset-company-tabs" aria-label="资产类型">{companyTabs.map((tab) => <button className={`${category === tab.value ? "active" : ""} ${tab.value || "all"}`} key={tab.label} type="button" onClick={() => setCategory(tab.value)}><span>{tab.label}</span><strong>{tab.value ? items.filter((item) => item.category === tab.value).length : items.length}</strong></button>)}</nav>
    {libraryApiAvailable === null ? <div className="asset-library-empty">正在加载公司资产库…</div> : <section className="asset-company-grid" aria-label="已定版资产">{filtered.map((item) => <LibraryCard item={item} key={item.id} onOpenVersions={(itemId, versionId) => setViewerSelection({ itemId, versionId })} />)}</section>}
    {libraryApiAvailable !== null && !filtered.length ? <div className="empty-hint">当前筛选条件下没有已审核定版资产。</div> : null}
    {viewerItem ? <VersionViewer initialVersionId={viewerSelection?.versionId} item={viewerItem} onClose={() => setViewerSelection(null)} /> : null}
  </>;
}

function LibraryCard({ item, onOpenVersions }: { item: LibraryItem; onOpenVersions: (id: string, versionId?: string) => void }) {
  const previewVersions = React.useMemo(() => {
    const versions = Array.from(new Map(
      [item.current, ...item.versions].filter((version): version is LibraryVersion => Boolean(version)).map((version) => [version.id, version]),
    ).values());
    if (item.category !== "character") return versions;
    const viewOrder = new Map(["A", "B", "C", "D", "E"].map((view, index) => [view, index]));
    return versions.sort((left, right) => (
      (viewOrder.get(characterViewCode(left.contextLabel)) ?? 99) - (viewOrder.get(characterViewCode(right.contextLabel)) ?? 99)
      || right.versionNo - left.versionNo
    ));
  }, [item.category, item.current, item.versions]);
  const [previewIndex, setPreviewIndex] = React.useState(0);
  React.useEffect(() => setPreviewIndex(0), [item.id]);
  const preview = previewVersions[previewIndex] || item.current;
  const changePreview = (offset: number) => setPreviewIndex((current) => (
    previewVersions.length ? (current + offset + previewVersions.length) % previewVersions.length : 0
  ));
  return <article className={`asset-library-card ${item.category}`}>
    <div className="asset-library-preview">
      <LibraryMedia version={preview} />
      <button aria-label={`打开${item.title}当前缩略图`} className="asset-preview-open-surface" title="打开当前缩略图" type="button" onClick={() => onOpenVersions(item.id, preview?.id)} />
      {item.category === "character"
        ? <span className="asset-current-badge">{characterViewLabel(preview?.contextLabel)}</span>
        : preview?.isPrimary
          ? <span className="asset-current-badge">主母版</span>
          : <span className="asset-current-badge fallback">母图</span>}
      <span className="asset-version-count">{Math.max(1, previewVersions.length)} 版</span>
      <span className="asset-preview-open"><Maximize2 aria-hidden="true" size={16} /></span>
      {previewVersions.length > 1 ? <div className="asset-preview-pagination" aria-label="缩略图翻页">
        <button aria-label="上一张缩略图" title="上一张" type="button" onClick={() => changePreview(-1)}><ChevronLeft size={17} /></button>
        <span>{previewIndex + 1}/{previewVersions.length}</span>
        <button aria-label="下一张缩略图" title="下一张" type="button" onClick={() => changePreview(1)}><ChevronRight size={17} /></button>
      </div> : null}
    </div>
    <div className="asset-library-card-body">
      <div className="asset-library-card-heading"><div><span>{item.category === "character" ? "角色名" : item.categoryLabel}</span><strong>{item.code}</strong></div><h3>{item.title}</h3></div>
      <dl className="asset-library-card-facts">
        <div><dt>资产来源</dt><dd title={item.projectName}>{item.projectName}</dd></div>
        <div><dt>所属剧集</dt><dd>{episodeDisplay(item)}</dd></div>
        <div><dt>{item.kind === "production" ? "场景 / 分镜" : "定版层级"}</dt><dd>{item.kind === "production" ? `${item.sceneCode} / ${item.storyboardCode}` : item.sourceLabel}</dd></div>
        <div><dt>最近定版</dt><dd>{formatShortDate(item.finalizedAt)}</dd></div>
      </dl>
      <p className="asset-library-description" title={item.description}>{item.description || (item.sceneName !== "项目资产" ? item.sceneName : "暂无描述")}</p>
      <div className="asset-library-card-footer"><span title={preview?.fileName}>{preview?.fileName || standardPlaceholderName(item)}</span><button className="asset-version-button" type="button" onClick={() => onOpenVersions(item.id, preview?.id)}>{item.category === "character" ? "图位与版本" : "全部版本"}</button></div>
    </div>
  </article>;
}

function VersionViewer({ item, initialVersionId, onClose }: { item: LibraryItem; initialVersionId?: string; onClose: () => void }) {
  const initialId = item.versions.some((version) => version.id === initialVersionId) ? initialVersionId! : item.current?.id || item.versions[0]?.id || "";
  const [selectedId, setSelectedId] = React.useState(initialId);
  const selectedIndex = Math.max(0, item.versions.findIndex((version) => version.id === selectedId));
  const selected = item.versions[selectedIndex] || item.current;

  React.useEffect(() => setSelectedId(initialId), [initialId, item.id]);
  React.useEffect(() => {
    function keydown(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
      if (event.key === "ArrowLeft" && item.versions.length) setSelectedId(item.versions[(selectedIndex - 1 + item.versions.length) % item.versions.length].id);
      if (event.key === "ArrowRight" && item.versions.length) setSelectedId(item.versions[(selectedIndex + 1) % item.versions.length].id);
    }
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  }, [item.versions, onClose, selectedIndex]);

  return <div className="modal-backdrop asset-version-modal" role="presentation" onClick={onClose}>
    <section className={`asset-version-dialog ${item.category}`} role="dialog" aria-modal="true" aria-label={`${item.title} 多版本`} onClick={(event) => event.stopPropagation()}>
      <header><div><strong>{item.code} · {item.title}</strong><span>{item.projectCode} / {item.episodeCode} / {item.sceneCode} / {item.storyboardCode}</span></div><button aria-label="关闭" title="关闭" type="button" onClick={onClose}><X size={19} /></button></header>
      <div className="asset-version-stage">
        <button aria-label="上一版本" disabled={item.versions.length < 2} title="上一版本" type="button" onClick={() => setSelectedId(item.versions[(selectedIndex - 1 + item.versions.length) % item.versions.length].id)}><ChevronLeft size={24} /></button>
        <LibraryMedia detailed version={selected || null} />
        <button aria-label="下一版本" disabled={item.versions.length < 2} title="下一版本" type="button" onClick={() => setSelectedId(item.versions[(selectedIndex + 1) % item.versions.length].id)}><ChevronRight size={24} /></button>
      </div>
      <div className="asset-version-info"><div><strong>{selected?.fileName || standardPlaceholderName(item)}</strong>{selected?.isPrimary ? <span>当前展示母版</span> : <span>{selected?.status === "primary_master" ? "图位主版本" : versionStatusLabel(selected?.status)}</span>}</div><small>{selected ? `${mediaRoleLabel(selected.mediaRole)} · 定版 V${pad(selected.versionNo, 3)}${selected.batchVersionNo ? ` · 审核批次 B${pad(selected.batchVersionNo, 3)}` : ""}${selected.contextLabel ? ` · ${selected.contextLabel}` : ""} · ${selected.modelName || "未记录模型"} · ${formatDate(selected.createdAt)}` : "暂无媒体文件"}</small>{selected?.viewLabel || selected?.stateLabel || selected?.description ? <div className="asset-version-labels">{selected.viewLabel ? <span>角度：{selected.viewLabel}</span> : null}{selected.stateLabel ? <span>状态：{selected.stateLabel}</span> : null}{selected.description ? <p>说明：{selected.description}</p> : null}</div> : null}</div>
      <div className="asset-version-filmstrip">
        {item.versions.map((version) => <button className={`${version.id === selected?.id ? "active" : ""} ${version.isPrimary ? "primary" : ""}`} key={version.id} type="button" onClick={() => setSelectedId(version.id)}>
          <LibraryMedia thumbnail version={version} />
          <span><strong>V{pad(version.versionNo, 3)}</strong><small>{version.viewLabel ? "角度：" + version.viewLabel + " · " : ""}{version.stateLabel ? "状态：" + version.stateLabel + " · " : ""}{mediaRoleLabel(version.mediaRole)} · {version.isPrimary ? "展示母版" : version.status === "primary_master" ? "图位主版本" : versionStatusLabel(version.status)}</small></span>
        </button>)}
        {!item.versions.length ? <div className="asset-library-empty">该资产只有业务记录，尚无可读取的媒体版本。</div> : null}
      </div>
    </section>
  </div>;
}

function LibraryMedia({ version, detailed = false, thumbnail = false }: { version: LibraryVersion | null; detailed?: boolean; thumbnail?: boolean }) {
  const [url, renew] = useLibraryMediaUrl(version);
  if (version?.fileType === "audio") return <div className="asset-media-placeholder audio"> <AudioLines size={thumbnail ? 18 : 28} />{detailed && url ? <audio controls preload="metadata" src={url} /> : <span>{url ? "音频母版" : "音频读取中"}</span>}</div>;
  if (!version || !url) return <div className="asset-media-placeholder">{version?.fileType === "video" ? <Film size={thumbnail ? 18 : 28} /> : <ImageIcon size={thumbnail ? 18 : 28} />}<span>{version ? "媒体读取中" : "暂无母图"}</span></div>;
  if (version.fileType === "video") return <video controls={detailed} muted={!detailed} preload="metadata" src={url} onError={renew} />;
  return <img loading="lazy" src={url} alt={version.fileName} />;
}

function useLibraryMediaUrl(version: LibraryVersion | null) {
  const [url, setUrl] = React.useState("");
  const [renewalKey, setRenewalKey] = React.useState(0);
  const standby = React.useRef("");
  React.useEffect(() => {
    setUrl("");
    standby.current = "";
    setRenewalKey(0);
  }, [version?.id]);
  React.useEffect(() => {
    if (!version) return;
    if (!version.fileId) {
      setUrl(version.filePath || "");
      return;
    }
    const controller = new AbortController();
    let objectUrl = "";
    let renewalTimer = 0;
    if (version.fileType === "video") {
      async function sign(activate: boolean) {
        try {
          const result = await api.getFilePlaybackUrl(version.fileId!, controller.signal);
          if (activate) setUrl(result.url);
          else standby.current = result.url;
          renewalTimer = window.setTimeout(() => void sign(false), Math.max(5, result.expires_in_seconds - 45) * 1000);
        } catch {
          if (!controller.signal.aborted && activate) setUrl("");
        }
      }
      void sign(true);
      return () => { controller.abort(); if (renewalTimer) window.clearTimeout(renewalTimer); };
    }
    fetch(`${API_BASE}/api/files/${version.fileId}/content`, { headers: { Authorization: `Bearer ${getAuthToken()}` }, signal: controller.signal })
      .then((response) => response.ok ? response.blob() : Promise.reject(new Error("媒体读取失败")))
      .then((blob) => { objectUrl = URL.createObjectURL(blob); setUrl(objectUrl); })
      .catch(() => { if (!controller.signal.aborted) setUrl(""); });
    return () => { controller.abort(); if (objectUrl) URL.revokeObjectURL(objectUrl); };
  }, [renewalKey, version?.fileId, version?.filePath, version?.fileType, version?.id]);
  return [url, () => {
    if (standby.current) {
      setUrl(standby.current);
      standby.current = "";
    } else {
      setRenewalKey((current) => current + 1);
    }
  }] as const;
}

function normalizeLibraryResponse(response: unknown, projects: Project[], assets: Asset[]): LibraryItem[] {
  const root = asRecord(response);
  const rawItems = Array.isArray(response)
    ? response
    : Array.isArray(root.items) ? root.items
      : Array.isArray(root.assets) ? root.assets
        : [];
  const projectMap = new Map(projects.map((project) => [project.id, project]));
  const assetMap = new Map(assets.map((asset) => [asset.id, asset]));
  return rawItems.flatMap((rawItem, index) => {
    const item = asRecord(rawItem);
    if (!Object.keys(item).length) return [];
    const currentRaw = asRecord(item.current_master);
    if (!Object.keys(currentRaw).length) return [];
    const rawVersions = Array.isArray(item.versions) ? item.versions : [];
    const hierarchy = Object.keys(asRecord(item.hierarchy)).length
      ? asRecord(item.hierarchy)
      : Object.keys(asRecord(currentRaw.hierarchy)).length
        ? asRecord(currentRaw.hierarchy)
        : asRecord(asRecord(rawVersions[0]).hierarchy);
    const projectNode = asRecord(hierarchy.project);
    const episodeNode = asRecord(hierarchy.episode);
    const sceneNode = asRecord(hierarchy.scene);
    const storyboardNode = asRecord(hierarchy.storyboard);
    const projectId = firstString(item.project_id, hierarchy.project_id, projectNode.id) || null;
    const project = projectId ? projectMap.get(projectId) : undefined;
    const projectCode = businessToken(firstString(
      item.project_code,
      hierarchy.project_code,
      hierarchy.project_prefix,
      projectNode.code,
      project?.project_prefix,
      project?.project_no,
      project?.title,
      "LIB",
    ));
    const outputType = firstString(item.output_type, item.media_type, hierarchy.output_type, hierarchy.media_type, item.asset_type);
    const category = outputType === "asset_master" ? assetCategory(firstString(item.asset_type, hierarchy.asset_type)) : structuredCategory(outputType);
    const kind = ["keyframe", "video", "final"].includes(category) ? "production" : "asset";
    const currentKey = versionIdentity(currentRaw);
    const combinedVersions = currentKey && !rawVersions.some((version) => versionIdentity(asRecord(version)) === currentKey)
      ? [currentRaw, ...rawVersions]
      : rawVersions;
    const rawCode = firstString(
      item.business_code,
      kind === "production" ? hierarchy.artifact_code : hierarchy.asset_code,
      item.asset_code,
      item.code,
      hierarchy.business_code,
    );
    const code = rawCode
      ? kind === "asset" && ["character", "scene", "prop"].includes(category)
        ? assetCodeDisplay(rawCode, "编码异常")
        : rawCode
      : kind === "production"
        ? `SH${pad(index + 1, 3)}-${category === "keyframe" ? "KEYFRAME" : "VIDEO"}`
        : "编码异常";
    const versions = combinedVersions.map((rawVersion, versionIndex) => normalizeStructuredVersion(
      asRecord(rawVersion),
      `${projectCode}-${businessToken(code)}`,
      versionIndex,
      currentKey,
    )).sort((left, right) => Number(right.isPrimary) - Number(left.isPrimary) || right.versionNo - left.versionNo);
    const current = versions.find((version) => version.isPrimary) || versions[0] || null;
    const sourceAsset = assetMap.get(firstString(item.source_id, hierarchy.asset_id));
    const episodeCode = businessToken(firstString(item.episode_code, hierarchy.episode_code, episodeNode.code, "EP01"));
    const episodeCodes = Array.from(new Set([
      ...(Array.isArray(item.episode_codes) ? item.episode_codes : []),
      ...(kind === "production" ? [episodeCode] : []),
    ].map((value) => businessToken(String(value))).filter((value) => value && value !== "NA")));
    const sceneCode = businessToken(firstString(item.scene_code, hierarchy.scene_code, sceneNode.code, kind === "asset" ? "MASTER" : "SC001"));
    const storyboardCode = businessToken(firstString(item.storyboard_code, hierarchy.storyboard_code, storyboardNode.code, kind === "asset" ? "MASTER" : "SH001"));
    const mergeTaskId = outputType === "video"
      ? firstString(hierarchy.depends_on_task_id)
      : firstString(hierarchy.task_id);
    const storyboardMergeKey = kind === "production"
      ? `storyboard:${projectId || projectCode}:${firstString(hierarchy.storyboard_id, hierarchy.storyboard_code, storyboardCode)}`
      : undefined;
    return [{
      id: `library:${firstString(item.library_key, item.id, item.library_item_id, code) || index}`,
      kind,
      category,
      categoryLabel: categoryLabels[category],
      title: firstString(item.display_name, item.name, item.title, hierarchy.title, code),
      code: businessToken(code),
      description: firstString(item.description, item.summary, sourceAsset?.description),
      projectId,
      projectCode,
      projectName: firstString(item.project_name, hierarchy.project_name, projectNode.name, project?.title, project?.name, projectId ? "未命名项目" : "公共资产"),
      episodeCodes,
      sourceLabel: firstString(item.source_label, kind === "production" ? category === "keyframe" ? "关键帧定版" : "视频定版" : "项目资产定版"),
      episodeCode: kind === "asset" && !firstString(item.episode_code, hierarchy.episode_code, episodeNode.code) ? "项目资产" : episodeCode,
      sceneCode,
      sceneName: firstString(item.scene_name, hierarchy.scene_name, sceneNode.name, kind === "asset" ? "项目资产" : "未命名场景"),
      storyboardCode,
      versions,
      current,
      finalizedAt: firstString(item.latest_version_at, currentRaw.created_at) || maxVersionDate(versions),
      mergeKey: kind === "production" && mergeTaskId ? `task:${mergeTaskId}` : undefined,
      storyboardMergeKey,
    } satisfies LibraryItem];
  });
}

function normalizeStructuredVersion(raw: Record<string, unknown>, baseName: string, index: number, currentKey: string): LibraryVersion {
  const versionNo = firstNumber(raw.context_version_no, raw.version_no, raw.version, raw.final_version_no) || index + 1;
  const mediaDescriptor = [
    raw.file_type,
    raw.media_type,
    raw.mime_type,
    raw.standard_filename,
    raw.display_name,
    raw.canonical_display_name,
    raw.original_file_name,
    raw.file_name,
    raw.file_path,
  ].filter((value): value is string => typeof value === "string" && Boolean(value.trim())).join(" ").toLowerCase();
  const fileType = inferMediaFileType(mediaDescriptor);
  const fallbackName = `${baseName}-V${pad(versionNo, 3)}.${mediaExtension(fileType)}`;
  const displayName = resolveVersionDisplayName(raw, fallbackName, fileType);
  const isPrimary = Boolean(raw.is_card_master || (currentKey && versionIdentity(raw) === currentKey));
  const hierarchy = asRecord(raw.hierarchy);
  const contextLabel = [
    firstString(hierarchy.task_variant),
    firstString(hierarchy.age_stage_code),
    firstString(hierarchy.costume_variant_code),
  ].filter(Boolean).join(" / ");
  const outputType = firstString(hierarchy.output_type, raw.media_type, raw.mime_type, fileType);
  const viewLabel = firstString(raw.view_label, raw.viewLabel) || null;
  const stateLabel = firstString(raw.state_label, raw.stateLabel) || null;
  const description = firstString(raw.description, raw.submission_description) || null;
  return {
    id: firstString(raw.id, raw.version_id, raw.submission_id, raw.file_id) || `${baseName}:version:${index}`,
    versionNo,
    batchVersionNo: firstNumber(raw.batch_version_no, raw.review_batch_version_no) || null,
    fileId: firstString(raw.file_id) || null,
    filePath: firstString(raw.file_path, raw.url) || null,
    fileType,
    fileName: displayName,
    searchNames: Array.from(new Set([
      raw.standard_filename,
      raw.display_name,
      raw.canonical_display_name,
      raw.original_file_name,
      raw.file_name,
    ].filter((value): value is string => typeof value === "string" && Boolean(value.trim())).map((value) => value.trim()))),
    isPrimary,
    status: firstString(raw.status, isPrimary ? "primary_master" : "alternate_master"),
    modelName: firstString(raw.model_name, raw.base_model) || null,
    createdAt: firstString(raw.created_at, raw.reviewed_at, raw.updated_at) || null,
    contextLabel,
    mediaRole: audioCategory(outputType) ? "audio" : outputType.includes("keyframe") ? "keyframe" : fileType === "video" ? "video" : "image",
    viewLabel,
    stateLabel,
    description,
  };
}

function structuredCategory(value: string): LibraryCategory {
  const normalized = value.toLowerCase();
  if (audioCategory(normalized)) return "audio";
  if (normalized.includes("keyframe") || normalized.includes("storyboard_image") || normalized === "image") return "keyframe";
  if (normalized.includes("final")) return "final";
  if (normalized.includes("video")) return "video";
  return assetCategory(normalized);
}

function versionIdentity(version: Record<string, unknown>) {
  return firstString(
    version.id,
    version.version_id,
    version.submission_id,
    version.file_id,
    version.standard_filename,
    version.display_name,
    version.canonical_display_name,
  );
}

function asRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function firstString(...values: unknown[]) {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function firstNumber(...values: unknown[]) {
  for (const value of values) {
    const parsed = typeof value === "number" ? value : typeof value === "string" && value.trim() ? Number(value) : Number.NaN;
    if (Number.isFinite(parsed) && parsed > 0) return Math.trunc(parsed);
  }
  return 0;
}

function resolveVersionDisplayName(raw: Record<string, unknown>, fallbackName: string, fileType: LibraryVersion["fileType"]) {
  const preferred = firstString(
    raw.standard_filename,
    raw.display_name,
    raw.canonical_display_name,
    raw.file_name,
  );
  if (!preferred) return fallbackName;
  if (filenameExtension(preferred)) return preferred;
  const extension = [
    raw.standard_filename,
    raw.canonical_display_name,
    raw.original_file_name,
    raw.file_name,
    raw.file_path,
  ].map((value) => filenameExtension(typeof value === "string" ? value : "")).find(Boolean)
    || mimeExtension(firstString(raw.mime_type))
    || mediaExtension(fileType);
  return `${preferred}.${extension}`;
}

function inferMediaFileType(descriptor: string): LibraryVersion["fileType"] {
  const extension = filenameExtension(descriptor);
  if (audioCategory(descriptor) || /\.(mp3|wav|flac|aac|m4a|ogg|opus)(?:\s|$)/i.test(descriptor) || ["mp3", "wav", "flac", "aac", "m4a", "ogg", "opus"].includes(extension)) return "audio";
  if (descriptor.includes("video") || /\.(mp4|mov|m4v|avi|mkv|webm)(?:\s|$)/i.test(descriptor) || ["mp4", "mov", "m4v", "avi", "mkv", "webm"].includes(extension)) return "video";
  return "image";
}

function filenameExtension(value: string) {
  const leaf = value.split(/[?#]/, 1)[0].replace(/\\/g, "/").split("/").pop() || "";
  const match = leaf.match(/\.([a-z0-9]{1,10})$/i);
  return match?.[1]?.toLowerCase() || "";
}

function replaceFilenameExtension(value: string, extension: string) {
  const normalizedExtension = extension.toLowerCase().replace(/[^a-z0-9]+/g, "");
  if (!normalizedExtension) return value;
  return filenameExtension(value)
    ? value.replace(/\.[a-z0-9]{1,10}$/i, `.${normalizedExtension}`)
    : `${value}.${normalizedExtension}`;
}

function mimeExtension(value: string) {
  const normalized = value.toLowerCase();
  if (normalized.includes("jpeg")) return "jpg";
  if (normalized.includes("png")) return "png";
  if (normalized.includes("webp")) return "webp";
  if (normalized.includes("quicktime")) return "mov";
  if (normalized.includes("webm")) return "webm";
  if (normalized.includes("wav")) return "wav";
  if (normalized.includes("mpeg") && normalized.includes("audio")) return "mp3";
  if (normalized.includes("mp4")) return "mp4";
  return "";
}

function audioCategory(value: string) {
  const normalized = value.toLowerCase();
  return ["audio", "voice", "voice_profile", "music", "music_theme", "music_cue"].some((token) => normalized.includes(token));
}

function maxVersionDate(versions: LibraryVersion[]) {
  return versions.reduce((latest, version) => {
    if (!version.createdAt) return latest;
    return !latest || Date.parse(version.createdAt) > Date.parse(latest) ? version.createdAt : latest;
  }, "");
}

function sortableTime(value?: string | null) {
  const parsed = value ? Date.parse(value) : Number.NaN;
  return Number.isFinite(parsed) ? parsed : 0;
}

function mergeStoryboardCards(items: LibraryItem[]) {
  const primaryGroups = new Map<string, LibraryItem[]>();
  const fallbackGroups = new Map<string, LibraryItem[]>();
  const standalone: LibraryItem[] = [];
  items.forEach((item) => {
    if (!["keyframe", "video", "final"].includes(item.category)) {
      standalone.push(item);
      return;
    }
    const key = item.mergeKey || `single:${item.id}`;
    primaryGroups.set(key, [...(primaryGroups.get(key) || []), item]);
  });

  const readyGroups: Array<[string, LibraryItem[]]> = [];
  primaryGroups.forEach((group, primaryKey) => {
    const mediaRoles = new Set(group.map((item) => item.category));
    if (group.length > 1 && mediaRoles.has("keyframe") && (mediaRoles.has("video") || mediaRoles.has("final"))) {
      readyGroups.push([primaryKey, group]);
      return;
    }
    const fallbackKey = group[0].storyboardMergeKey;
    if (!fallbackKey) {
      readyGroups.push([primaryKey, group]);
      return;
    }
    fallbackGroups.set(fallbackKey, [...(fallbackGroups.get(fallbackKey) || []), ...group]);
  });
  fallbackGroups.forEach((group, key) => readyGroups.push([key, group]));

  readyGroups.forEach(([mergeKey, group]) => {
    const versions = Array.from(new Map(group.flatMap((item) => item.versions).map((version) => [version.id, version])).values())
      .sort((left, right) => sortableTime(right.createdAt) - sortableTime(left.createdAt) || right.versionNo - left.versionNo);
    const keyframeCover = group.map((item) => item.current).find((version) => version?.isPrimary && version.mediaRole === "keyframe");
    const current = keyframeCover || group.map((item) => item.current).find((version) => version?.isPrimary) || null;
    if (!current) return;
    const representative = group.find((item) => item.current?.mediaRole === "keyframe") || group[0];
    standalone.push({
      ...representative,
      id: `merged:${mergeKey}`,
      category: "video",
      categoryLabel: "视频",
      code: representative.code.replace(/-(KEYFRAME|VIDEO|FINAL)$/i, ""),
      versions,
      current,
      episodeCodes: Array.from(new Set(group.flatMap((item) => item.episodeCodes))),
      sourceLabel: group.some((item) => item.current?.mediaRole === "video") ? "关键帧 / 视频定版" : representative.sourceLabel,
      finalizedAt: group.reduce((latest, item) => sortableTime(item.finalizedAt) > sortableTime(latest) ? item.finalizedAt : latest, ""),
    });
  });
  return standalone;
}

function buildLibraryItems(assets: Asset[], tasks: Task[], projects: Project[], storyboards: Storyboard[]) {
  const projectMap = new Map(projects.map((project) => [project.id, project]));
  const assetMap = new Map(assets.map((asset) => [asset.id, asset]));
  const storyboardMap = new Map(storyboards.map((storyboard) => [storyboard.id, storyboard]));
  const output: LibraryItem[] = [];

  tasks.filter((task) => task.task_type === "asset").forEach((task, taskIndex) => {
    const asset = task.asset_id ? assetMap.get(task.asset_id) : undefined;
    const category = assetCategory(asset?.asset_type);
    const project = projectMap.get(task.project_id);
    const projectCode = projectBusinessCode(project, asset);
    const assetCode = assetBusinessCode(asset, category, taskIndex + 1);
    const variant = businessToken(task.task_variant || "MASTER");
    const versions = submissionVersions(task, "result", (version, alternate) => `${projectCode}-${assetTypeToken(category)}-${assetCode}-${variant}-V${pad(version, 3)}${alternate}.${mediaExtension(category === "audio" ? "audio" : "image")}`);
    if (!versions.length) return;
    output.push({
      id: `asset-task:${task.id}`,
      kind: "asset",
      category,
      categoryLabel: categoryLabels[category],
      title: asset?.name || task.title,
      code: `${assetCode}-${variant}`,
      description: asset?.description || "",
      projectId: task.project_id,
      projectCode,
      projectName: project?.title || project?.name || "未命名项目",
      episodeCodes: task.episode_code ? [businessToken(task.episode_code)] : [],
      sourceLabel: "项目资产定版",
      episodeCode: "项目资产",
      sceneCode: task.scene_code || structuredString(asset, "scene_code") || "MASTER",
      sceneName: task.scene_name || structuredString(asset, "scene_name") || "项目资产",
      storyboardCode: variant,
      versions,
      current: currentVersion(versions),
      finalizedAt: maxVersionDate(versions),
    });
  });

  tasks.filter((task) => task.task_type !== "asset").forEach((task) => {
    const storyboard = task.storyboard_id ? storyboardMap.get(task.storyboard_id) : undefined;
    const steps = productionSteps(task);
    steps.forEach((step) => {
      const category: LibraryCategory = step === "audio" ? "audio" : step === "video" ? (task.task_type === "final_video" ? "final" : "video") : "keyframe";
      const project = projectMap.get(task.project_id);
      const projectCode = projectBusinessCode(project);
      const episodeCode = task.episode_code || (storyboard ? `EP${pad(storyboard.episode_num, 2)}` : "EP01");
      const sceneCode = task.scene_code || storyboard?.scene_code || "SC001";
      const sceneName = task.scene_name || storyboard?.scene_name || "未命名场景";
      const storyboardCode = task.storyboard_code || storyboard?.storyboard_code || (storyboard ? `SH${pad(storyboard.order_num, 3)}` : "SH001");
      const mediaToken = category === "keyframe" ? "KEYFRAME" : category === "final" ? "FINAL" : category === "audio" ? "AUDIO" : "VIDEO";
      const versions = submissionVersions(task, step, (version, alternate) => `${projectCode}-${businessToken(episodeCode)}-${businessToken(sceneCode)}-${businessToken(storyboardCode)}-${mediaToken}-V${pad(version, 3)}${alternate}.${mediaExtension(category === "keyframe" ? "image" : category === "audio" ? "audio" : "video")}`);
      if (!versions.length) return;
      output.push({
        id: `production:${task.id}:${step}`,
        kind: "production",
        category,
        categoryLabel: categoryLabels[category],
        title: storyboard?.title || task.title,
        code: `${storyboardCode}-${mediaToken}`,
        description: storyboard?.description || task.title,
        projectId: task.project_id,
        projectCode,
        projectName: project?.title || project?.name || "未命名项目",
        episodeCodes: [businessToken(episodeCode)],
        sourceLabel: category === "keyframe" ? "关键帧定版" : category === "audio" ? "音频定版" : "视频定版",
        episodeCode: businessToken(episodeCode),
        sceneCode: businessToken(sceneCode),
        sceneName,
        storyboardCode: businessToken(storyboardCode),
        versions,
        current: currentVersion(versions),
        finalizedAt: maxVersionDate(versions),
        mergeKey: category === "keyframe"
          ? `task:${task.id}`
          : category === "video" && task.depends_on_task_id
            ? `task:${task.depends_on_task_id}`
            : undefined,
        storyboardMergeKey: `storyboard:${task.project_id}:${task.storyboard_id || storyboardCode}`,
      });
    });
  });
  return output.sort((left, right) => [left.projectCode, left.episodeCode, left.sceneCode, left.storyboardCode, left.code].join("|").localeCompare([right.projectCode, right.episodeCode, right.sceneCode, right.storyboardCode, right.code].join("|"), "zh-CN", { numeric: true }));
}

function submissionVersions(task: Task, step: string, fileName: (version: number, alternate: string) => string): LibraryVersion[] {
  const batchVersions = new Map((task.submission_batches || []).map((batch) => [batch.id, batch.version_no]));
  const selected = (task.submissions || []).filter((submission) => (
    isActiveSubmission(submission)
    && submission.is_selected
    && ["primary_master", "alternate_master"].includes(submission.status)
    && submissionStep(task, submission) === step
  ));
  const batchCounts = new Map<number, number>();
  return selected.map((submission, index) => {
    const versionNo = submission.version_no || (submission.batch_id ? batchVersions.get(submission.batch_id) : undefined) || index + 1;
    const count = (batchCounts.get(versionNo) || 0) + 1;
    batchCounts.set(versionNo, count);
    const alternate = count > 1 && !submission.is_primary ? `-ALT${pad(count, 2)}` : "";
    const generatedName = fileName(versionNo, alternate);
    const actualExtension = filenameExtension(submission.file_name || submission.file_path || "")
      || mimeExtension(submission.mime_type || "");
    const resolvedName = actualExtension ? replaceFilenameExtension(generatedName, actualExtension) : generatedName;
    const fileType = inferMediaFileType([
      submission.file_type,
      submission.media_type,
      submission.mime_type,
      submission.file_name,
      submission.file_path,
    ].filter(Boolean).join(" ").toLowerCase());
    return {
      id: submission.id,
      versionNo,
      batchVersionNo: submission.batch_id ? batchVersions.get(submission.batch_id) || null : null,
      fileId: submission.file_id,
      filePath: submission.file_path,
      fileType,
      fileName: resolvedName,
      searchNames: [resolvedName, submission.file_name || ""].filter(Boolean),
      isPrimary: submission.is_primary,
      status: submission.status,
      modelName: submission.model_name,
      createdAt: submission.created_at,
      contextLabel: [task.task_variant, task.age_stage_code, task.costume_variant_code].filter(Boolean).join(" / "),
      viewLabel: submission.view_label || null,
      stateLabel: submission.state_label || null,
      description: submission.description || null,
      mediaRole: fileType === "audio" ? "audio" : step === "keyframe" ? "keyframe" : step === "video" ? "video" : fileType === "video" ? "video" : "image",
    };
  }).sort((left, right) => Number(right.isPrimary) - Number(left.isPrimary) || right.versionNo - left.versionNo || String(right.createdAt || "").localeCompare(String(left.createdAt || "")));
}

function currentVersion(versions: LibraryVersion[]) {
  return versions.find((version) => version.isPrimary) || versions[0] || null;
}

function productionSteps(task: Task) {
  const selectedSteps = new Set((task.submissions || []).filter((submission) => (
    isActiveSubmission(submission)
    && (submission.is_selected || submission.is_primary || ["primary_master", "alternate_master"].includes(submission.status))
  )).map((submission) => submissionStep(task, submission)));
  if (selectedSteps.size) return Array.from(selectedSteps).filter((step) => ["keyframe", "video", "audio"].includes(step));
  if (audioCategory(`${task.task_type} ${task.media_type || ""}`)) return ["audio"];
  if (["image_to_video", "video_generation", "final_video"].includes(task.task_type) || task.media_type?.includes("video")) return ["video"];
  if (["text_to_image", "storyboard_shot"].includes(task.task_type)) return ["keyframe"];
  return [];
}

function submissionStep(task: Task, submission: Submission) {
  if (submission.step) return submission.step;
  if (audioCategory(`${task.task_type} ${task.media_type || ""} ${submission.file_type}`)) return "audio";
  if (["image_to_video", "video_generation", "final_video"].includes(task.task_type)) return "video";
  if (["text_to_image", "storyboard_shot"].includes(task.task_type)) return "keyframe";
  return "result";
}

function projectBusinessCode(project?: Project, asset?: Asset) {
  return businessToken(asset?.project_code || structuredString(asset, "project_code") || project?.project_prefix || project?.project_no || project?.title || "LIB").slice(0, 12) || "LIB";
}

function assetBusinessCode(asset: Asset | undefined, category: LibraryCategory, index: number) {
  const raw = asset?.asset_code || structuredString(asset, "asset_code");
  if (raw) {
    const segments = businessToken(raw).split("-").filter(Boolean);
    return segments[segments.length - 1] || businessToken(raw);
  }
  const prefix = category === "character" ? "C" : category === "scene" ? "S" : category === "prop" ? "P" : "A";
  return `${prefix}${pad(index, 3)}`;
}

function assetCategory(value?: string | null): LibraryCategory {
  if (audioCategory(value || "")) return "audio";
  if (value === "character") return "character";
  if (value === "scene") return "scene";
  if (value === "prop") return "prop";
  return "other";
}

function assetTypeToken(category: LibraryCategory) {
  return ({ character: "CHAR", scene: "SCENE", prop: "PROP", audio: "AUDIO", other: "ASSET" } as Partial<Record<LibraryCategory, string>>)[category] || "ASSET";
}

function structuredString(asset: Asset | undefined, key: string) {
  if (!asset) return "";
  const direct = (asset as unknown as Record<string, unknown>)[key];
  if (typeof direct === "string" && direct.trim()) return direct.trim();
  const metadata = asset.metadata?.[key];
  return typeof metadata === "string" ? metadata.trim() : "";
}

function businessToken(value: string) {
  return value.trim().toUpperCase().replace(/[^A-Z0-9\u4e00-\u9fff]+/g, "-").replace(/^-+|-+$/g, "") || "NA";
}

function standardPlaceholderName(item: LibraryItem) {
  const type = item.kind === "asset" ? assetTypeToken(item.category) : item.category === "keyframe" ? "KEYFRAME" : item.category === "final" ? "FINAL" : item.category === "audio" ? "AUDIO" : "VIDEO";
  return item.kind === "asset"
    ? `${item.projectCode}-${type}-${leafBusinessCode(item.code, "ASSET")}-MASTER-V001.${item.category === "audio" ? "mp3" : "png"}`
    : [
      item.projectCode,
      leafBusinessCode(item.episodeCode, "EP00"),
      leafBusinessCode(item.sceneCode, ""),
      leafBusinessCode(item.storyboardCode, "F000"),
      type,
      "V001",
    ].filter(Boolean).join("-") + `.${item.category === "keyframe" ? "png" : item.category === "audio" ? "mp3" : "mp4"}`;
}

function leafBusinessCode(value: string, fallback: string) {
  const normalized = businessToken(value);
  if (normalized === "NA") return fallback;
  const parts = normalized.split("-").filter(Boolean);
  return parts[parts.length - 1] || fallback;
}

function mediaExtension(type: string) {
  return type === "video" ? "mp4" : type === "audio" ? "mp3" : "png";
}

function mediaRoleLabel(role?: LibraryVersion["mediaRole"]) {
  return ({ keyframe: "关键帧", video: "视频", audio: "音频", image: "图片" } as Record<string, string>)[role || ""] || "媒体";
}

function versionStatusLabel(status?: string) {
  return ({ primary_master: "主母版", alternate_master: "备选母版", master: "母图" } as Record<string, string>)[status || ""] || "历史定版";
}

function characterViewLabel(contextLabel?: string) {
  const view = characterViewCode(contextLabel);
  return ({ A: "A-Pose", MASTER: "A-Pose", B: "B · 半侧", C: "C · 背面", D: "D · 面部", E: "E · 细节" } as Record<string, string>)[view] || `图位 ${view}`;
}

function characterViewCode(contextLabel?: string) {
  const view = String(contextLabel || "A").split("/")[0].trim().toUpperCase();
  return view === "MASTER" ? "A" : view;
}

function formatDate(value?: string | null) {
  if (!value) return "时间未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "时间未记录";
  return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(date);
}

function formatShortDate(value?: string | null) {
  if (!value) return "时间未记录";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "时间未记录";
  return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "2-digit", day: "2-digit" }).format(date);
}

function episodeDisplay(item: LibraryItem) {
  if (item.episodeCodes.length) return item.episodeCodes.join(" / ");
  return item.kind === "asset" ? "项目通用" : item.episodeCode || "未关联剧集";
}

function pad(value: number, length: number) {
  return String(Math.max(0, Math.trunc(value))).padStart(length, "0");
}

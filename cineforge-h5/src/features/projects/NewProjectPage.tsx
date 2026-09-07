import React from "react";
import { api } from "../../api";
import { Field, PageHeader, Panel } from "../../shared/components";
import { FileText, X } from "lucide-react";
import type { CulturalContext, EpisodeProductionBrief, ProductionBriefCatalog, ProductionContentType, ProductionStyleCatalog, Project, ProjectCreatePayload, ProjectEntryMode, ProjectEpisode, ProjectImportPayload, ProjectProductionBrief, ProjectUpdatePayload, ScriptSegment } from "../../types";
import { formatFileSize } from "../../utils";

type NewProjectPageProps = {
  currentProject?: Project;
  mode: ProjectEntryMode["type"];
  targetEpisodeId?: string;
  loading: boolean;
  readOnly?: boolean;
  breakdownRunning?: boolean;
  onRunReading: (projectId?: string, episodeId?: string, scriptVersionId?: string) => Promise<boolean>;
  onCreateProject: (payload: ProjectCreatePayload) => Promise<Project | null>;
  onUpdateProject: (projectId: string, payload: ProjectUpdatePayload) => Promise<Project | null>;
  onImportProject: (payload: ProjectImportPayload, file: File) => Promise<Project | null>;
  embedded?: boolean;
  asModal?: boolean;
  onClose?: () => void;
  seedEpisodes?: ProjectEpisode[];
  scriptSegments: ScriptSegment[];
};

const genreOptions = ["都市情感", "都市悬疑", "甜宠爱情", "家庭伦理", "职场成长", "校园青春", "古装爱情", "古装权谋", "历史正剧", "动作冒险", "奇幻穿越", "科幻", "喜剧"];

const culturalContextOptions: CulturalContext[] = [
  { context_code: "CN_CONTEMPORARY", name: "中国现代", region: "CN-mainland", era: "contemporary", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "CN_REPUBLIC", name: "中国民国", region: "CN-mainland", era: "republican", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "CN_ANCIENT", name: "中国古代", region: "CN", era: "ancient", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "JP_CONTEMPORARY", name: "日本现代", region: "JP", era: "contemporary", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "JP_ANCIENT", name: "日本古代", region: "JP", era: "ancient", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "KR_CONTEMPORARY", name: "韩国现代", region: "KR", era: "contemporary", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "TH_CONTEMPORARY", name: "泰国现代", region: "TH", era: "contemporary", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "US_CONTEMPORARY", name: "美国现代", region: "US", era: "contemporary", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "UK_CONTEMPORARY", name: "英国现代", region: "UK", era: "contemporary", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "WESTERN_HISTORICAL", name: "西方历史时期", region: "western", era: "historical", world_type: "real_world", timeline: "待围读识别" },
  { context_code: "EASTERN_FICTIONAL", name: "架空东方", region: "eastern", era: "fictional", world_type: "fictional_history", timeline: "待围读识别" },
  { context_code: "WESTERN_FICTIONAL", name: "架空西方", region: "western", era: "fictional", world_type: "fictional_history", timeline: "待围读识别" },
];

function emptyProductionBrief(): ProjectProductionBrief {
  return {
    schema_version: "ProjectProductionBrief.v1",
    content_type: "animated_drama",
    delivery_aspect_ratio: "9:16",
    primary_style_id: "",
    style_catalog_version: "",
    cultural_contexts: [],
    primary_cultural_context_code: "",
    field_sources: {
      content_type: "human_confirmed",
      delivery_aspect_ratio: "human_confirmed",
      primary_style_id: "human_confirmed",
      cultural_contexts: "human_confirmed",
    },
  };
}

function productionBriefFromProject(project: Project): ProjectProductionBrief {
  return project.production_brief || emptyProductionBrief();
}

function projectFormFromProject(project: Project): ProjectCreatePayload {
  return {
    title: project.title,
    project_prefix: project.project_prefix || "",
    genre: project.genre || "",
    script_text: project.script_text || "",
    production_brief: productionBriefFromProject(project),
  };
}

export function NewProjectPage({
  currentProject,
  mode,
  targetEpisodeId,
  loading,
  readOnly = false,
  breakdownRunning = false,
  onRunReading,
  onCreateProject,
  onUpdateProject,
  onImportProject,
  embedded,
  asModal,
  onClose,
  seedEpisodes,
}: NewProjectPageProps) {
  const fileInputRef = React.useRef<HTMLInputElement | null>(null);
  const [form, setForm] = React.useState<ProjectCreatePayload>({
    title: "",
    project_prefix: "",
    genre: "",
    script_text: "",
    production_brief: emptyProductionBrief(),
  });
  const [selectedFile, setSelectedFile] = React.useState<File | null>(null);
  const [dragging, setDragging] = React.useState(false);
  const [importing, setImporting] = React.useState(false);
  const [fileMessage, setFileMessage] = React.useState<string | null>(null);
  const [styleCatalog, setStyleCatalog] = React.useState<ProductionStyleCatalog | null>(null);
  const [briefCatalog, setBriefCatalog] = React.useState<ProductionBriefCatalog | null>(null);
  const [styleCatalogError, setStyleCatalogError] = React.useState(false);
  const [metadataEditing, setMetadataEditing] = React.useState(!currentProject);
  const [editConfirmOpen, setEditConfirmOpen] = React.useState(false);
  const [rerunNotice, setRerunNotice] = React.useState(false);
  const [episodes, setEpisodes] = React.useState<ProjectEpisode[]>([]);
  const [episodeNo, setEpisodeNo] = React.useState(1);
  const [readingEpisodeId, setReadingEpisodeId] = React.useState("");
  const [episodeVersionConfirmOpen, setEpisodeVersionConfirmOpen] = React.useState(false);
  const [pendingRunAfterImport, setPendingRunAfterImport] = React.useState(false);
  const [targetDurationSeconds, setTargetDurationSeconds] = React.useState(120);

  const acceptedScriptExtensions = ".txt,.md,.docx";
  const maxScriptFileSize = 20 * 1024 * 1024;

  React.useEffect(() => {
    if (!currentProject) return;
    setForm(projectFormFromProject(currentProject));
    setMetadataEditing(false);
  }, [currentProject?.id, currentProject?.updated_at, currentProject?.script_text]);

  React.useEffect(() => {
    let active = true;
    if (!currentProject?.id) {
      setEpisodes(seedEpisodes?.length ? seedEpisodes : []);
      setEpisodeNo(1);
      setReadingEpisodeId("");
      return () => { active = false; };
    }
    if (seedEpisodes?.length) {
      const items = seedEpisodes;
      setEpisodes(items);
      const targetEpisode = targetEpisodeId ? items.find((item) => item.id === targetEpisodeId) : undefined;
      setEpisodeNo(targetEpisode?.episode_no || Math.max(0, ...items.map((item) => item.episode_no)) + 1);
      const inheritedDuration = targetEpisode?.production_brief?.target_duration_seconds;
      if (inheritedDuration) setTargetDurationSeconds(inheritedDuration);
      setReadingEpisodeId((current) => items.some((item) => item.id === current)
        ? current
        : items[items.length - 1]?.id || "");
      return () => { active = false; };
    }
    void api.get<ProjectEpisode[]>(`/projects/${currentProject.id}/episodes`).then((items) => {
      if (!active) return;
      setEpisodes(items);
      const targetEpisode = targetEpisodeId ? items.find((item) => item.id === targetEpisodeId) : undefined;
      setEpisodeNo(targetEpisode?.episode_no || Math.max(0, ...items.map((item) => item.episode_no)) + 1);
      const inheritedDuration = targetEpisode?.production_brief?.target_duration_seconds;
      if (inheritedDuration) setTargetDurationSeconds(inheritedDuration);
      setReadingEpisodeId((current) => items.some((item) => item.id === current)
        ? current
        : items[items.length - 1]?.id || "");
    }).catch(() => {
      if (active) setEpisodes([]);
    });
    return () => { active = false; };
  }, [currentProject?.id, currentProject?.updated_at, targetEpisodeId, seedEpisodes]);

  React.useEffect(() => {
    let active = true;
    void api.get<ProductionBriefCatalog>("/production-brief/catalog").then((catalog) => {
      if (!active) return;
      setBriefCatalog(catalog);
      setStyleCatalog({
        schema_version: catalog.schema_version,
        catalog_version: catalog.style_catalog_version,
        max_modifiers: 0,
        primary_styles: catalog.styles,
        modifiers: [],
      });
      setStyleCatalogError(false);
    }).catch(() => {
      if (active) setStyleCatalogError(true);
    });
    return () => { active = false; };
  }, []);

  function fileBaseName(filename: string) {
    return filename.replace(/\.[^.]+$/, "").trim();
  }

  function autoProjectPrefix(seed: string) {
    const ascii = seed.replace(/\.[^.]+$/, "").replace(/[^a-zA-Z0-9]/g, "").toUpperCase();
    if (ascii) return ascii.slice(0, 5);
    return `P${Date.now().toString(36).slice(-4).toUpperCase()}`.slice(0, 5);
  }

  function normalizedProjectPayload() {
    return {
      ...form,
      title: form.title.trim(),
      project_prefix: form.project_prefix.trim().toUpperCase().slice(0, 5),
      genre: form.genre?.trim(),
      script_text: form.script_text?.trim(),
      production_brief: normalizedProductionBrief(),
    };
  }

  async function saveProjectDraft() {
    const payload = normalizedProjectPayload();
    return currentProject?.id
      ? onUpdateProject(currentProject.id, payload)
      : onCreateProject(payload);
  }

  function normalizedProductionBrief(): ProjectProductionBrief {
    const brief = form.production_brief || emptyProductionBrief();
    const contexts = Array.isArray(brief.cultural_contexts) ? brief.cultural_contexts : [];
    const contentType = brief.content_type;
    return {
      ...brief,
      schema_version: "ProjectProductionBrief.v1",
      primary_style_id: contentType === "live_action_drama" ? "live_action_cinematic" : brief.primary_style_id,
      style_catalog_version: styleCatalog?.catalog_version || brief.style_catalog_version,
      cultural_contexts: contexts,
      primary_cultural_context_code: contexts.some((item) => item.context_code === brief.primary_cultural_context_code)
        ? brief.primary_cultural_context_code
        : contexts[0]?.context_code || "",
    };
  }

  function updateProductionBrief(updates: Partial<ProjectProductionBrief>) {
    setForm((current) => ({
      ...current,
      production_brief: {
        ...(current.production_brief || emptyProductionBrief()),
        ...updates,
      },
    }));
  }

  function selectContentType(contentType: ProductionContentType) {
    updateProductionBrief({
      content_type: contentType,
      primary_style_id: contentType === "live_action_drama" ? "live_action_cinematic" : "",
    });
  }

  function selectDeliveryAspectRatio(aspectRatio: "9:16" | "16:9") {
    const option = briefCatalog?.delivery_aspect_ratios.find((item) => item.value === aspectRatio);
    updateProductionBrief({ delivery_aspect_ratio: aspectRatio });
    setTargetDurationSeconds(option?.suggested_duration_seconds || (aspectRatio === "9:16" ? 120 : 180));
  }

  function setPrimaryCulture(contextCode: string) {
    const option = briefCatalog?.cultural_contexts.find((item) => item.context_code === contextCode);
    if (!option) return;
    const current = normalizedProductionBrief().cultural_contexts.filter((item) => item.context_code !== contextCode);
    updateProductionBrief({ cultural_contexts: [option, ...current], primary_cultural_context_code: contextCode });
  }

  function updateAdditionalCulture(index: number, contextCode: string) {
    const option = briefCatalog?.cultural_contexts.find((item) => item.context_code === contextCode);
    if (!option) return;
    const brief = normalizedProductionBrief();
    const contexts = [...brief.cultural_contexts];
    contexts[index] = option;
    updateProductionBrief({ cultural_contexts: contexts.filter((item, itemIndex, all) => all.findIndex((other) => other.context_code === item.context_code) === itemIndex) });
  }

  function addCulturalContext() {
    const brief = normalizedProductionBrief();
    const option = briefCatalog?.cultural_contexts.find((item) => !brief.cultural_contexts.some((current) => current.context_code === item.context_code));
    if (option) updateProductionBrief({ cultural_contexts: [...brief.cultural_contexts, option] });
  }

  function removeCulturalContext(index: number) {
    const brief = normalizedProductionBrief();
    updateProductionBrief({ cultural_contexts: brief.cultural_contexts.filter((_, itemIndex) => itemIndex !== index) });
  }

  function setDurationMinutes(minutes: number) {
    setTargetDurationSeconds(Math.max(0, minutes) * 60 + (targetDurationSeconds % 60));
  }

  function setDurationRemainder(seconds: number) {
    setTargetDurationSeconds(Math.floor(targetDurationSeconds / 60) * 60 + Math.max(0, Math.min(59, seconds)));
  }

  function normalizedImportPayload(confirmExistingEpisode = false): ProjectImportPayload {
    const fallbackTitle = selectedFile ? fileBaseName(selectedFile.name) : "未命名项目";
    const payload = {
      ...normalizedProjectPayload(),
      title: form.title.trim() || fallbackTitle,
      project_prefix: (form.project_prefix.trim() || autoProjectPrefix(selectedFile?.name || fallbackTitle)).toUpperCase().slice(0, 5),
      project_id: mode === "create_project" ? undefined : currentProject?.id,
    };
    return {
      title: payload.title,
      project_prefix: payload.project_prefix,
      genre: payload.genre,
      project_id: payload.project_id,
      production_brief: mode === "create_project" ? payload.production_brief : undefined,
      episode_brief: mode === "add_script_version" ? undefined : {
        schema_version: "EpisodeProductionBrief.v1",
        target_duration_seconds: Math.max(1, Number(targetDurationSeconds) || 1),
        field_sources: { target_duration_seconds: "human_confirmed" },
      } satisfies EpisodeProductionBrief,
      episode_no: episodeNo,
      confirm_existing_episode_version: confirmExistingEpisode,
    };
  }

  function selectScriptFile(file?: File | null) {
    if (!file) return;
    const suffix = file.name.split(".").pop()?.toLowerCase();
    if (!suffix || !["txt", "md", "docx"].includes(suffix)) {
      setSelectedFile(null);
      setFileMessage("当前仅支持 txt、md、docx 剧本文件。");
      return;
    }
    if (file.size > maxScriptFileSize) {
      setSelectedFile(null);
      setFileMessage("剧本文件不能超过 20MB。");
      return;
    }
    setSelectedFile(file);
    setForm((current) => ({
      ...current,
      title: current.title.trim() ? current.title : fileBaseName(file.name),
      project_prefix: current.project_prefix.trim() ? current.project_prefix : autoProjectPrefix(file.name),
    }));
    setFileMessage(null);
  }

  function handleFileChange(event: React.ChangeEvent<HTMLInputElement>) {
    selectScriptFile(event.target.files?.[0] ?? null);
    event.target.value = "";
  }

  function handleDrop(event: React.DragEvent<HTMLButtonElement>) {
    event.preventDefault();
    setDragging(false);
    selectScriptFile(event.dataTransfer.files?.[0] ?? null);
  }

  function selectPrimaryStyle(styleId: string) {
    updateProductionBrief({
      primary_style_id: styleId,
      style_catalog_version: styleCatalog?.catalog_version || "",
    });
  }

  async function saveMetadataChanges() {
    const savedProject = await saveProjectDraft();
    if (!savedProject) return;
    setForm(projectFormFromProject(savedProject));
    setMetadataEditing(false);
    setRerunNotice(true);
  }

  async function importScriptFile(runAfterImport = false, confirmExistingEpisode = false) {
    if (!selectedFile) {
      setFileMessage("请先选择剧本文件。");
      fileInputRef.current?.click();
      return;
    }
    const existingEpisode = episodes.find((item) => item.episode_no === episodeNo);
    if (existingEpisode && !confirmExistingEpisode) {
      setPendingRunAfterImport(runAfterImport);
      setEpisodeVersionConfirmOpen(true);
      return;
    }
    setImporting(true);
    try {
      if (currentProject && metadataEditing) {
        const savedProject = await saveProjectDraft();
        if (!savedProject) return;
      }
      const createdProject = await onImportProject(normalizedImportPayload(confirmExistingEpisode), selectedFile);
      if (createdProject) {
        setForm(projectFormFromProject(createdProject));
        setMetadataEditing(false);
        setSelectedFile(null);
        setFileMessage(`剧本文件已导入，已解析 ${createdProject.script_text?.trim().length || 0} 字。`);
        if (runAfterImport) {
          await onRunReading(
            createdProject.id,
            createdProject.latest_import?.episode_id,
            createdProject.latest_import?.script_version_id,
          );
        }
        if (asModal && onClose) {
          // 弹框模式下关闭窗口，让父组件页面刷新
          window.setTimeout(() => onClose(), 0);
        }
      }
    } finally {
      setImporting(false);
    }
  }

  const actionBusy = loading || importing || breakdownRunning;
  const normalizedBrief = normalizedProductionBrief();
  const availableStyles = (styleCatalog?.primary_styles || []).filter((item) => (
    normalizedBrief.content_type === "live_action_drama" ? item.medium === "live_action" : item.medium !== "live_action"
  ));
  const isCreateProject = mode === "create_project";
  const isProjectOverview = mode === "project_overview";
  const isEpisodeImport = mode === "add_episode" || mode === "add_script_version";
  const formLocked = readOnly || Boolean(currentProject && !metadataEditing);
  const missingMetadata = [
    !form.title.trim() ? "项目名称" : "",
    !form.project_prefix.trim() ? "项目缩写" : "",
    !normalizedBrief.content_type ? "成片类型" : "",
    !normalizedBrief.delivery_aspect_ratio ? "成片画幅" : "",
    !normalizedBrief.primary_style_id ? "具体视觉风格" : "",
    !normalizedBrief.cultural_contexts.length ? "文化与时代背景" : "",
    !normalizedBrief.primary_cultural_context_code ? "主要文化背景" : "",
  ].filter(Boolean);
  const metadataComplete = missingMetadata.length === 0;
  const uploadDisabled = readOnly || actionBusy;
  const targetEpisode = targetEpisodeId ? episodes.find((item) => item.id === targetEpisodeId) : undefined;
  const missingImportRequired = [
    ...(isCreateProject ? missingMetadata : []),
    mode === "add_script_version" && !targetEpisode ? "目标分集" : "",
    mode !== "add_script_version" && (!Number.isFinite(targetDurationSeconds) || targetDurationSeconds < 1) ? "目标时长" : "",
    !selectedFile ? "剧本文件" : "",
  ].filter(Boolean);
  const canImport = missingImportRequired.length === 0 && !uploadDisabled;
  const canSaveMetadata = metadataComplete && metadataEditing && !actionBusy && !readOnly;
  const selectedEpisode = episodes.find((item) => item.episode_no === episodeNo);
  const selectedReadingEpisode = episodes.find((item) => item.id === readingEpisodeId);
  const importDisabledReason = missingImportRequired.length
    ? `请先补齐：${missingImportRequired.join("、")}`
    : breakdownRunning ? "拆解运行中，请等待完成" : readOnly ? "当前为只读状态" : "";

  if (isEpisodeImport) {
    const episodeLabel = mode === "add_script_version"
      ? targetEpisode?.episode_code || "加载中"
      : `EP${String(episodeNo).padStart(2, "0")}`;
    const formBody = (
      <>
        <form className="import-project-form episode-import-form" onSubmit={(event) => event.preventDefault()}>
          <div className="form-grid episode-import-identity">
            <Field label="所属项目">
              <input disabled value={currentProject?.title || ""} />
            </Field>
            <Field label="项目缩写">
              <input disabled value={currentProject?.project_prefix || ""} />
            </Field>
            <Field label={mode === "add_script_version" ? "目标分集" : "新分集"}>
              <input disabled value={episodeLabel} />
            </Field>
            <Field label="项目制作设置" full>
              <div className="production-brief-summary">
                <span>{normalizedBrief.content_type === "animated_drama" ? "动漫短剧" : normalizedBrief.content_type === "live_action_drama" ? "真人短剧" : "动真结合"}</span>
                <span>{normalizedBrief.delivery_aspect_ratio}</span>
                <span>{styleCatalog?.primary_styles.find((item) => item.style_id === normalizedBrief.primary_style_id)?.label || normalizedBrief.primary_style_id}</span>
                <span>{normalizedBrief.cultural_contexts.find((item) => item.context_code === normalizedBrief.primary_cultural_context_code)?.name || "未设置文化背景"}</span>
              </div>
            </Field>
            <Field label="本集目标时长" full required>
              <div className="duration-control">
                <div className="duration-control__unit">
                  <input disabled={mode === "add_script_version"} min={0} type="number" value={Math.floor(targetDurationSeconds / 60)} onChange={(event) => setDurationMinutes(Number(event.target.value))} />
                  <span>分</span>
                </div>
                <div className="duration-control__unit">
                  <input disabled={mode === "add_script_version"} max={59} min={0} type="number" value={targetDurationSeconds % 60} onChange={(event) => setDurationRemainder(Number(event.target.value))} />
                  <span>秒</span>
                </div>
              </div>
            </Field>
          </div>
          <input ref={fileInputRef} hidden aria-hidden="true" disabled={uploadDisabled} tabIndex={-1} type="file" accept={acceptedScriptExtensions} onChange={handleFileChange} />
          <Field label="剧本文件" full required>
            {asModal ? (
              <button
                className={`project-settings__script ${selectedFile ? "has-file" : ""} ${dragging ? "is-dragging" : ""}`}
                disabled={uploadDisabled}
                type="button"
                onClick={() => fileInputRef.current?.click()}
                onDragEnter={(event) => { event.preventDefault(); setDragging(true); }}
                onDragOver={(event) => event.preventDefault()}
                onDragLeave={() => setDragging(false)}
                onDrop={handleDrop}
              >
                {selectedFile ? (
                  <div className="project-settings__script-file">
                    <FileText size={22} strokeWidth={1.7} />
                    <div>
                      <strong>{selectedFile.name}</strong>
                      <p>{formatFileSize(selectedFile.size)} · 点击可重新上传</p>
                    </div>
                  </div>
                ) : (
                  <>
                    <FileText size={26} strokeWidth={1.6} />
                    <strong>点击/拖拽上传剧本</strong>
                    <p>支持 TXT / DOC / DOCX / PDF</p>
                  </>
                )}
              </button>
            ) : (
              <button
                className={`upload-zone upload-zone-button import-upload-zone ${dragging ? "dragging" : ""}`}
                disabled={uploadDisabled}
                type="button"
                onClick={() => fileInputRef.current?.click()}
                onDragEnter={(event) => { event.preventDefault(); setDragging(true); }}
                onDragOver={(event) => event.preventDefault()}
                onDragLeave={() => setDragging(false)}
                onDrop={handleDrop}
              >
                <div>
                  <div className="upload-icon">UP</div>
                  <strong>{selectedFile ? selectedFile.name : "点击/拖拽上传剧本"}</strong>
                  {selectedFile ? <span>{formatFileSize(selectedFile.size)}</span> : <span>支持 TXT / DOC / DOCX / PDF</span>}
                </div>
              </button>
            )}
          </Field>
          {fileMessage ? <div className="empty-hint full">{fileMessage}</div> : null}
          {!asModal ? (
            <div className="form-actions import-actions">
              <button className="btn" disabled={!canImport} title={importDisabledReason} type="button" onClick={() => void importScriptFile(false)}>
                {mode === "add_script_version" ? "保存新版本" : "仅导入并保存"}
              </button>
              <button className="btn primary" disabled={!canImport} title={importDisabledReason} type="button" onClick={() => void importScriptFile(true)}>
                {breakdownRunning ? "剧本阅读运行中..." : "导入后进入剧本阅读"}
              </button>
            </div>
          ) : null}
        </form>
        {episodeVersionConfirmOpen ? (
          <div className="modal-backdrop" role="presentation" onClick={() => setEpisodeVersionConfirmOpen(false)}>
            <section className="confirmation-modal" role="dialog" aria-modal="true" aria-label="确认创建剧本新版本" onClick={(event) => event.stopPropagation()}>
              <div className="modal-head"><div><h3>确认创建剧本新版本</h3><p>{selectedEpisode?.episode_code} 将保留历史版本。</p></div></div>
              <div className="confirmation-modal-body">
                <strong>版本变化</strong>
                <span>当前版本：v{selectedEpisode?.current_version_no || 1}</span>
                <span>导入后：v{(selectedEpisode?.current_version_no || 0) + 1}</span>
                <span>历史版本保留，不覆盖原始记录</span>
              </div>
              <div className="confirmation-modal-actions">
                <button className="btn" type="button" onClick={() => setEpisodeVersionConfirmOpen(false)}>取消</button>
                <button className="btn primary" type="button" onClick={() => { setEpisodeVersionConfirmOpen(false); void importScriptFile(pendingRunAfterImport, true); }}>确认并创建新版本</button>
              </div>
            </section>
          </div>
        ) : null}
      </>
    );

    if (!asModal) {
      return (
        <>
          <div className="steps script-import-steps">
            {["确认项目", "选择剧本", "保存导入", "进入剧本阅读"].map((item, index) => (
              <div className={`step ${index === 1 ? "active" : index === 0 ? "done" : ""}`} key={item}>
                <div className="step-no">{index + 1}</div>
                <strong>{item}</strong>
              </div>
            ))}
          </div>
          <Panel title={mode === "add_script_version" ? "上传剧本新版本" : "导入新分集剧本"}>{formBody}</Panel>
        </>
      );
    }

    return (
      <>
        <div className="project-settings" role="dialog" aria-modal="true" aria-label={mode === "add_script_version" ? "上传剧本新版本" : "导入新分集剧本"}>
          <button type="button" className="project-settings__backdrop" aria-label="关闭" onClick={onClose} />
          <section className="project-settings__panel project-settings__panel--script-upload" onClick={(event) => event.stopPropagation()}>
            <header className="project-settings__head">
              <h3>{mode === "add_script_version" ? "上传剧本新版本" : "导入新分集剧本"}</h3>
              {onClose ? (
                <button type="button" className="project-settings__close" onClick={onClose} aria-label="关闭">
                  <X size={18} strokeWidth={1.8} />
                </button>
              ) : null}
            </header>
            <div className="project-settings__body project-settings__body--script-upload">
              {formBody}
            </div>
            <footer className="project-settings__foot project-settings__foot--script-upload">
              <button className="project-settings__cancel" disabled={!canImport} title={importDisabledReason} type="button" onClick={() => void importScriptFile(false)}>
                {mode === "add_script_version" ? "保存新版本" : "仅导入并保存"}
              </button>
              <button className="project-settings__submit" disabled={!canImport} title={importDisabledReason} type="button" onClick={() => void importScriptFile(true)}>
                {breakdownRunning ? "剧本阅读运行中..." : "导入后进入剧本阅读"}
              </button>
            </footer>
          </section>
        </div>
      </>
    );
  }

  return (
    <>
      {embedded ? null : <PageHeader title="项目创建阶段" desc="填写项目约束并导入剧本。" />}
      {isCreateProject ? (
        <div className="steps script-import-steps">
          {["填写项目约束", "选择剧本", "保存导入", "进入剧本阅读"].map((item, index) => (
            <div className={`step ${index === 0 && !metadataComplete ? "active" : index === 1 && metadataComplete ? "active" : metadataComplete && index === 0 ? "done" : ""}`} key={item}>
              <div className="step-no">{index + 1}</div>
              <strong>{item}</strong>
            </div>
          ))}
        </div>
      ) : null}
      <Panel title={isProjectOverview ? "项目设置" : "创建项目并导入首集"}>
        <div className="import-project-toolbar">
          <div>
            <strong>{currentProject ? currentProject.title : "新建项目"}</strong>
          </div>
          {currentProject ? (
            <div className="form-actions compact-actions">
              {!metadataEditing ? <button className="btn" disabled={readOnly || actionBusy} type="button" onClick={() => setEditConfirmOpen(true)}>修改项目信息</button> : null}
              <select
                aria-label="围读目标分集"
                disabled={readOnly || actionBusy || !episodes.length}
                value={readingEpisodeId}
                onChange={(event) => setReadingEpisodeId(event.target.value)}
              >
                {episodes.map((item) => <option key={item.id} value={item.id}>{item.episode_code}</option>)}
              </select>
              <button className="btn primary" disabled={readOnly || actionBusy || !selectedReadingEpisode?.current_version_id} type="button" onClick={() => void onRunReading(currentProject.id, selectedReadingEpisode?.id, selectedReadingEpisode?.current_version_id || undefined)}>
                {breakdownRunning ? "剧本阅读运行中..." : "进入剧本阅读"}
              </button>
            </div>
          ) : null}
        </div>
        {rerunNotice ? (
          <div className="notice import-rerun-notice">
            <strong>项目信息已更新</strong>
            <span>系统已按修改字段标记受影响的分集阶段，未受影响结果继续保留。</span>
          </div>
        ) : null}
        <form className="import-project-form" onSubmit={(event) => event.preventDefault()}>
          <div className="script-import-columns">
            <section className="script-import-column script-import-source-column">
              <h3>{isCreateProject ? "项目与剧本" : "项目资料"}</h3>
              <div className="form-grid import-column-grid">
                <Field label="项目名称" required>
                  <input disabled={formLocked} value={form.title} onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))} />
                </Field>
                <Field label="项目缩写" required>
                  <input disabled={!isCreateProject || formLocked} maxLength={5} value={form.project_prefix} onChange={(event) => setForm((current) => ({ ...current, project_prefix: event.target.value.toUpperCase() }))} />
                </Field>
                {isCreateProject ? (
                  <>
                    <Field label="首集" required>
                      <input disabled value="EP01" />
                    </Field>
                    <Field label="目标时长" required>
                      <div className="duration-control">
                        <input min={0} type="number" value={Math.floor(targetDurationSeconds / 60)} onChange={(event) => setDurationMinutes(Number(event.target.value))} />
                        <span>分</span>
                        <input max={59} min={0} type="number" value={targetDurationSeconds % 60} onChange={(event) => setDurationRemainder(Number(event.target.value))} />
                        <span>秒</span>
                      </div>
                    </Field>
                    <input ref={fileInputRef} hidden aria-hidden="true" disabled={uploadDisabled} tabIndex={-1} type="file" accept={acceptedScriptExtensions} onChange={handleFileChange} />
                    <Field label="剧本文件" full required>
                      <button
                        className={`upload-zone upload-zone-button import-upload-zone ${dragging ? "dragging" : ""}`}
                        disabled={uploadDisabled}
                        type="button"
                        onClick={() => fileInputRef.current?.click()}
                        onDragEnter={(event) => { event.preventDefault(); setDragging(true); }}
                        onDragOver={(event) => event.preventDefault()}
                        onDragLeave={() => setDragging(false)}
                        onDrop={handleDrop}
                      >
                        <div>
                          <div className="upload-icon">UP</div>
                          <strong>{selectedFile ? selectedFile.name : "上传或拖拽剧本文件"}</strong>
                          {selectedFile ? <span>{formatFileSize(selectedFile.size)}</span> : <span>TXT、Markdown、DOCX，最大 20MB</span>}
                        </div>
                      </button>
                    </Field>
                    {fileMessage ? <div className="empty-hint full">{fileMessage}</div> : null}
                  </>
                ) : null}
              </div>
            </section>
            <section className="script-import-column script-import-style-column">
              <h3>制作设置</h3>
              <div className="form-grid import-column-grid">
                <Field label="成片类型" full required>
                  <div className="production-segmented-control">
                    {(briefCatalog?.content_types || [
                      { value: "animated_drama" as const, label: "动漫短剧" },
                      { value: "live_action_drama" as const, label: "真人短剧" },
                      { value: "hybrid_drama" as const, label: "动真结合" },
                    ]).map((item) => (
                      <button className={normalizedBrief.content_type === item.value ? "selected" : ""} disabled={formLocked} key={item.value} type="button" onClick={() => selectContentType(item.value)}>{item.label}</button>
                    ))}
                  </div>
                </Field>
                <Field label="成片画幅" full required>
                  <div className="production-segmented-control aspect-ratio-control">
                    {(["9:16", "16:9"] as const).map((item) => (
                      <button className={normalizedBrief.delivery_aspect_ratio === item ? "selected" : ""} disabled={formLocked} key={item} type="button" onClick={() => selectDeliveryAspectRatio(item)}>
                        <span className={`aspect-ratio-icon ${item === "9:16" ? "portrait" : "landscape"}`} aria-hidden="true" />
                        {item === "9:16" ? "竖屏 9:16" : "横屏 16:9"}
                      </button>
                    ))}
                  </div>
                </Field>
                <Field label="具体视觉风格" full required>
                  <select disabled={formLocked || !styleCatalog || normalizedBrief.content_type === "live_action_drama"} value={normalizedBrief.primary_style_id || ""} onChange={(event) => selectPrimaryStyle(event.target.value)}>
                    <option value="">请选择具体视觉风格</option>
                    {availableStyles.map((item) => <option key={item.style_id} value={item.style_id}>{item.label} · {item.description}</option>)}
                  </select>
                  {styleCatalogError ? <span className="field-hint error">风格目录加载失败</span> : null}
                </Field>
                <Field label="主要文化与时代背景" full required>
                  <select disabled={formLocked || !briefCatalog} value={normalizedBrief.primary_cultural_context_code} onChange={(event) => setPrimaryCulture(event.target.value)}>
                    <option value="">请选择主要文化与时代背景</option>
                    {(briefCatalog?.cultural_contexts || []).map((item) => <option key={item.context_code} value={item.context_code}>{item.name}</option>)}
                  </select>
                </Field>
                {normalizedBrief.cultural_contexts.slice(1).map((context, offset) => (
                  <Field label={`其他文化背景 ${offset + 1}`} full key={`${context.context_code}-${offset}`}>
                    <div className="additional-culture-row">
                      <select disabled={formLocked} value={context.context_code} onChange={(event) => updateAdditionalCulture(offset + 1, event.target.value)}>
                        {(briefCatalog?.cultural_contexts || []).map((item) => <option key={item.context_code} value={item.context_code}>{item.name}</option>)}
                      </select>
                      <button className="btn" disabled={formLocked} type="button" onClick={() => removeCulturalContext(offset + 1)}>移除</button>
                    </div>
                  </Field>
                ))}
                <div className="production-settings-actions full">
                  <button className="btn" disabled={formLocked || normalizedBrief.cultural_contexts.length >= (briefCatalog?.cultural_contexts.length || 0)} type="button" onClick={addCulturalContext}>增加背景</button>
                </div>
                <div className="production-brief-summary full">
                  <span>{normalizedBrief.delivery_aspect_ratio === "9:16" ? "前 3 秒钩子" : "前 5 秒钩子"}</span>
                  <span>资产母版 16:9</span>
                  <span>分镜与视频 {normalizedBrief.delivery_aspect_ratio}</span>
                  {isCreateProject ? <span>目标 {Math.floor(targetDurationSeconds / 60)}:{String(targetDurationSeconds % 60).padStart(2, "0")}</span> : null}
                </div>
              </div>
            </section>
          </div>
          {metadataEditing && currentProject ? (
            <div className="form-actions metadata-edit-actions">
              <button className="btn" disabled={actionBusy} type="button" onClick={() => { setForm(projectFormFromProject(currentProject)); setMetadataEditing(false); }}>取消修改</button>
              <button className="btn primary" disabled={!canSaveMetadata} type="button" onClick={() => void saveMetadataChanges()}>保存项目信息</button>
            </div>
          ) : null}
          {isCreateProject ? (
            <div className="form-actions import-actions">
              <button className="btn" disabled={!canImport} title={importDisabledReason} type="button" onClick={() => void importScriptFile(false)}>
                仅导入并保存
              </button>
              <button className="btn primary" disabled={!canImport} title={importDisabledReason} type="button" onClick={() => void importScriptFile(true)}>
                {breakdownRunning ? "剧本阅读运行中..." : "导入后进入剧本阅读"}
              </button>
            </div>
          ) : null}
        </form>
      </Panel>
      {editConfirmOpen ? (
        <div className="modal-backdrop" role="presentation" onClick={() => setEditConfirmOpen(false)}>
          <section className="confirmation-modal" role="dialog" aria-modal="true" aria-label="确认修改项目信息" onClick={(event) => event.stopPropagation()}>
            <div className="modal-head">
              <div>
                <h3>确认修改项目信息</h3>
                <p>制作设置修改后，已有生成结果可能需要重新运行。</p>
              </div>
            </div>
            <div className="confirmation-modal-actions">
              <button className="btn" type="button" onClick={() => setEditConfirmOpen(false)}>取消</button>
              <button className="btn primary" type="button" onClick={() => { setEditConfirmOpen(false); setRerunNotice(false); setMetadataEditing(true); }}>确认修改</button>
            </div>
          </section>
        </div>
      ) : null}
    </>
  );
}

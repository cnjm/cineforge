import React from "react";
import { Check, ChevronRight, CircleAlert, FileText, Image as ImageIcon, Trash2, Upload, X } from "lucide-react";
import { api } from "../../api";
import { ConfirmModal } from "../../shared/ConfirmModal";
import { MemberPicker, usersToMemberOptions } from "../../shared/MemberPicker";
import type {
  EpisodeProductionBrief,
  ProductionBriefCatalog,
  ProductionContentType,
  Project,
  ProjectImportPayload,
  ProjectProductionBrief,
  User,
} from "../../types";

/**
 * 新建项目弹框（UI 移植自 legacy-ui/src/features/projects/ProjectOverviewPage.tsx 的 ProjectSettingsModal）。
 * 与 legacy-ui 的差异：
 * 1. 数据源改为真实接口 GET /production-brief/catalog（而非本地中文常量），draft 直接存 code，提交时无需中文↔code 映射；
 * 2. 剧本上传保留真实 File 对象，提交时调 importProjectFromFile 走后端文件上传（legacy-ui 仅存文件名 demo）；
 * 3. 成片类型 / 画幅 / 视觉风格 / 文化背景 选项均来自 catalog；
 * 4. 封面 / 成员 / 权限 / 分集数 依赖后端未实现字段，UI 渲染但提交时不发送（标 TODO）。
 */

// 成片类型 label → 本地图（catalog content_types.label 为中文，与 legacy-ui 一致）
const PRODUCTION_TYPE_IMAGES: Record<string, string> = {
  动漫短剧: "/styles/prod-anime.png",
  真人短剧: "/styles/prod-live.png",
  动真结合: "/styles/prod-hybrid.png",
};

// 视觉风格 style_id → 封面图（与 legacy-ui 封面一一对应）
const STYLE_IMAGE_MAP: Record<string, string> = {
  live_action_cinematic: "/styles/prod-live.png",
  anime_2d_cel: "/styles/style-2d-ri-man.png",
  comic_2d_american: "/styles/style-2d-mei-man.png",
  anime_2_5d: "/styles/style-25d-ri-xi.png",
  rounded_3d_cinematic_cartoon: "/styles/style-3d-cartoon.png",
  animation_3d_standard: "/styles/style-3d-anim.png",
  cgi_3d_photoreal: "/styles/style-3d-cgi.png",
  guofeng_3d_cinematic: "/styles/style-3d-guofeng.png",
  ink_2d_chinese: "/styles/style-2d-shuimo.png",
  // TODO(创建项目弹框): 以下样式在 legacy-ui 有封面但 cine-forge 后端 catalog 暂未提供，预留映射便于后续对齐
  webtoon: "/styles/style-webtoon.png",
  thick_paint: "/styles/style-thick-paint.png",
  retro_cel: "/styles/style-retro-cel.png",
  mono_manga: "/styles/style-mono-manga.png",
};

// 类型（题材）下拉常量。catalog 不提供 genre，属自由分类。
const PROJECT_GENRE_OPTIONS = ["武侠", "仙侠", "都市", "悬疑", "科幻", "历史", "言情", "玄幻", "末世", "搞笑", "其他"];

// 画幅可视化比例框尺寸（与 legacy-ui ratioIconSize 一致）
function ratioIconSize(ratio: readonly [number, number], max = 22) {
  const [rw, rh] = ratio;
  if (rw >= rh) {
    return { width: max, height: Math.max(5, Math.round((max * rh) / rw)) };
  }
  return { width: Math.max(5, Math.round((max * rw) / rh)), height: max };
}

type SettingsDraft = {
  title: string;
  abbreviation: string;
  permission: "all" | "specific";
  directorIds: string[];
  creatorIds: string[];
  contentType: ProductionContentType | "";
  aspectRatio: "9:16" | "16:9" | "";
  styleId: string;
  cultureCodes: string[]; // context_code 数组
  genre: string;
  targetMinutes: number;
  targetSeconds: number;
  coverPreview?: string; // 本地预览 URL，TODO 后端未支持 cover_image，暂不上传
  scriptFileName?: string; // 本地显示用，随 selectedFile 同步
};

const INITIAL_DRAFT: SettingsDraft = {
  title: "",
  abbreviation: "",
  permission: "all",
  directorIds: [],
  creatorIds: [],
  contentType: "",
  aspectRatio: "",
  styleId: "",
  cultureCodes: [],
  genre: "",
  targetMinutes: 0,
  targetSeconds: 0,
  coverPreview: undefined,
  scriptFileName: undefined,
};

export type EditInitialDraft = {
  projectId: string;
  project: Project;
  title: string;
  abbreviation: string;
  contentType: ProductionContentType | "";
  aspectRatio: "9:16" | "16:9" | "";
  styleId: string;
  cultureCodes: string[];
  genre: string;
  targetMinutes: number;
  targetSeconds: number;
  coverPreview?: string;
  scriptFileName?: string;
};

export function ProjectSettingsModal({
  users,
  onClose,
  onSubmit,
  onSubmitEdit,
  initial,
  mode = "create",
}: {
  users: User[];
  onClose: () => void;
  /** 创建模式：提交。组装好 payload + 真实剧本文件 + 是否导入后进入剧本阅读。返回创建的项目（失败返回 null）。 */
  onSubmit: (payload: ProjectImportPayload, file: File, runAfterImport: boolean) => Promise<Project | null>;
  /** 编辑模式：保存。组装好 payload（可含 File 重新上传剧本），返回更新后的项目。 */
  onSubmitEdit?: (payload: EditInitialDraft & {
    newScriptFile: File | null;
    newCoverFile: File | null;
    production_brief: ProjectProductionBrief | null;
  }) => Promise<Project | null>;
  /** 编辑模式初始值（create 模式不传） */
  initial?: EditInitialDraft;
  mode?: "create" | "edit";
}) {
  const isEdit = mode === "edit";
  const [draft, setDraft] = React.useState<SettingsDraft>(
    isEdit && initial
      ? {
          title: initial.title,
          abbreviation: initial.abbreviation,
          permission: "all",
          directorIds: [],
          creatorIds: [],
          contentType: initial.contentType,
          aspectRatio: initial.aspectRatio,
          styleId: initial.styleId,
          cultureCodes: initial.cultureCodes ?? [],
          genre: initial.genre,
          targetMinutes: initial.targetMinutes,
          targetSeconds: initial.targetSeconds,
          coverPreview: initial.coverPreview,
          scriptFileName: initial.scriptFileName,
        }
      : INITIAL_DRAFT,
  );
  const [catalog, setCatalog] = React.useState<ProductionBriefCatalog | null>(null);
  const [catalogError, setCatalogError] = React.useState<string | null>(null);
  const [selectedFile, setSelectedFile] = React.useState<File | null>(null);
  const [coverFile, setCoverFile] = React.useState<File | null>(null);
  const [submitting, setSubmitting] = React.useState(false);
  const [submitError, setSubmitError] = React.useState<string | null>(null);
  const [confirmEditOpen, setConfirmEditOpen] = React.useState(false);
  const [cultureOpen, setCultureOpen] = React.useState(false);
  const [genreOpen, setGenreOpen] = React.useState(false);
  const cultureRef = React.useRef<HTMLDivElement>(null);
  const genreRef = React.useRef<HTMLDivElement>(null);
  const scriptRef = React.useRef<HTMLInputElement>(null);
  const fileRef = React.useRef<HTMLInputElement>(null);
  const composingRef = React.useRef(false);

  // 拉取真实 catalog
  React.useEffect(() => {
    let alive = true;
    api
      .get<ProductionBriefCatalog>("/production-brief/catalog")
      .then((data) => {
        if (!alive) return;
        setCatalog(data);
        if (isEdit && initial) {
          // 编辑模式：不再用 catalog 默认项覆盖回显的选择
          return;
        }
        // 默认选中第一项，降低用户操作成本
        setDraft((cur) => ({
          ...cur,
          contentType: data.content_types[0]?.value ?? cur.contentType,
          aspectRatio: data.delivery_aspect_ratios[0]?.value ?? cur.aspectRatio,
          styleId:
            data.content_types[0]?.default_style_id ??
            data.styles.find((s) => s.medium !== "live_action")?.style_id ??
            data.styles[0]?.style_id ??
            cur.styleId,
        }));
      })
      .catch((err) => {
        if (!alive) return;
        setCatalogError(err instanceof Error ? err.message : "加载制作设置目录失败");
      });
    return () => {
      alive = false;
    };
  }, []);

  // ESC 关闭
  React.useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && !submitting) onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, submitting]);

  // 点击外部关闭下拉
  React.useEffect(() => {
    function onPointerDown(e: MouseEvent) {
      if (cultureRef.current && !cultureRef.current.contains(e.target as Node)) setCultureOpen(false);
      if (genreRef.current && !genreRef.current.contains(e.target as Node)) setGenreOpen(false);
    }
    window.addEventListener("mousedown", onPointerDown);
    return () => window.removeEventListener("mousedown", onPointerDown);
  }, []);

  function update<K extends keyof SettingsDraft>(key: K, value: SettingsDraft[K]) {
    setDraft((cur) => ({ ...cur, [key]: value }));
  }

  function handleScriptFile(file: File | null) {
    if (!file) return;
    // 以 cine-forge 后端实际支持的格式为准（txt/md/docx，20MB）
    const allowed = /\.(txt|md|docx)$/i.test(file.name);
    if (!allowed) {
      window.alert("仅支持 TXT / MD / DOCX 格式剧本文件");
      return;
    }
    if (file.size > 20 * 1024 * 1024) {
      window.alert("剧本文件不能超过 20MB");
      return;
    }
    setSelectedFile(file);
    update("scriptFileName", file.name);
    // 自动回填：仅当字段为空时才从文件名提取，已有值不覆盖
    setDraft((cur) => {
      const fileBaseName = file.name.replace(/\.[^.]+$/, "").trim();
      const ascii = fileBaseName.replace(/[^a-zA-Z0-9]/g, "").toUpperCase();
      const autoAbbr = ascii
        ? ascii.slice(0, 5)
        : `P${Date.now().toString(36).slice(-4).toUpperCase()}`.slice(0, 5);
      return {
        ...cur,
        title: cur.title.trim() ? cur.title : fileBaseName,
        abbreviation: cur.abbreviation.trim() ? cur.abbreviation : autoAbbr,
      };
    });
  }

  function handleCoverFile(file: File | null) {
    if (!file) return;
    if (!/^image\/(png|jpeg|jpg)$/i.test(file.type)) {
      window.alert("仅支持 JPG / PNG 格式");
      return;
    }
    // TODO(创建项目弹框-后端需求2): 后端 Project 暂无 cover_image 字段，封面当前仅本地预览，提交时不上传。
    setCoverFile(file);
    update("coverPreview", URL.createObjectURL(file));
  }

  const contentTypes = catalog?.content_types ?? [];
  const aspectRatios = catalog?.delivery_aspect_ratios ?? [];
  const styles = catalog?.styles ?? [];
  const contexts = catalog?.cultural_contexts ?? [];

  // 编辑模式：剧本文件不必重传（已有 scriptFileName 代表项目存在剧本），
  // 允许用户只改基础设置而不重新上传剧本；若用户重新选了文件则按新文件走。
  const canSubmitEdit =
    Boolean(draft.title.trim()) &&
    Boolean(draft.abbreviation.trim()) &&
    Boolean(draft.contentType) &&
    Boolean(draft.aspectRatio) &&
    Boolean(draft.styleId) &&
    draft.cultureCodes.length > 0 &&
    Boolean(draft.genre.trim()) &&
    (draft.targetMinutes > 0 || draft.targetSeconds > 0) &&
    !submitting;

  const canSubmitCreate =
    Boolean(draft.title.trim()) &&
    Boolean(draft.abbreviation.trim()) &&
    Boolean(draft.contentType) &&
    Boolean(draft.aspectRatio) &&
    Boolean(draft.styleId) &&
    draft.cultureCodes.length > 0 &&
    Boolean(draft.genre.trim()) &&
    (draft.targetMinutes > 0 || draft.targetSeconds > 0) &&
    selectedFile !== null &&
    !submitting;

  const canSubmit = isEdit ? canSubmitEdit : canSubmitCreate;

  async function handleSubmit(runAfterImport: boolean) {
    if (!canSubmit || !catalog) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const contextsSelected = draft.cultureCodes
        .map((code) => contexts.find((c) => c.context_code === code))
        .filter(Boolean) as typeof contexts;

      const production_brief: ProjectProductionBrief = {
        schema_version: "ProjectProductionBrief.v1",
        content_type: draft.contentType as ProductionContentType,
        delivery_aspect_ratio: draft.aspectRatio as "9:16" | "16:9",
        // 真人短剧走 live_action_cinematic，与 NewProjectPage 现有口径一致
        primary_style_id:
          draft.contentType === "live_action_drama" ? "live_action_cinematic" : draft.styleId,
        style_catalog_version: catalog.style_catalog_version,
        cultural_contexts: contextsSelected,
        primary_cultural_context_code: contextsSelected[0]?.context_code ?? "",
        field_sources: {
          content_type: "human_confirmed",
          delivery_aspect_ratio: "human_confirmed",
          primary_style_id: "human_confirmed",
          cultural_contexts: "human_confirmed",
        },
      };

      const episode_brief: EpisodeProductionBrief = {
        schema_version: "EpisodeProductionBrief.v1",
        target_duration_seconds: draft.targetMinutes * 60 + draft.targetSeconds,
        field_sources: { target_duration_seconds: "human_confirmed" },
      };

      const payload: ProjectImportPayload = {
        title: draft.title.trim(),
        project_prefix: draft.abbreviation.trim().toUpperCase().slice(0, 5),
        genre: draft.genre.trim(),
        production_brief,
        episode_brief,
        episode_no: 1,
      };

      // TODO(创建项目弹框-后端需求5): 项目成员（导演/创作者）后端未提供写入接口，暂不提交。
      // TODO(创建项目弹框-后端需求2): 封面 cover_image 后端未实现，coverFile 暂不上传。
      const created = await onSubmit(payload, selectedFile, runAfterImport);
      if (created) {
        onClose();
      } else {
        setSubmitError("创建项目失败，请重试");
      }
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "创建项目失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleEditSubmit() {
    if (!canSubmit || !catalog || !initial || !onSubmitEdit) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const contextsSelected = draft.cultureCodes
        .map((code) => contexts.find((c) => c.context_code === code))
        .filter(Boolean) as typeof contexts;

      const production_brief: ProjectProductionBrief = {
        schema_version: "ProjectProductionBrief.v1",
        content_type: draft.contentType as ProductionContentType,
        delivery_aspect_ratio: draft.aspectRatio as "9:16" | "16:9",
        // 真人短剧走 live_action_cinematic，与 NewProjectPage 现有口径一致
        primary_style_id:
          draft.contentType === "live_action_drama" ? "live_action_cinematic" : draft.styleId,
        style_catalog_version: catalog.style_catalog_version,
        cultural_contexts: contextsSelected,
        primary_cultural_context_code: contextsSelected[0]?.context_code ?? "",
        field_sources: {
          content_type: "human_confirmed",
          delivery_aspect_ratio: "human_confirmed",
          primary_style_id: "human_confirmed",
          cultural_contexts: "human_confirmed",
        },
      };

      // TODO(创建项目弹框-后端需求5): 项目成员（导演/创作者）后端未提供写入接口，暂不提交。
      // TODO(创建项目弹框-后端需求2): 封面 cover_image 后端未实现，coverFile 暂不上传。
      const updated = await onSubmitEdit({
        ...initial,
        title: draft.title.trim(),
        abbreviation: draft.abbreviation.trim().toUpperCase().slice(0, 5),
        contentType: draft.contentType as ProductionContentType,
        aspectRatio: draft.aspectRatio as "9:16" | "16:9",
        styleId: production_brief.primary_style_id,
        cultureCodes: draft.cultureCodes,
        genre: draft.genre.trim(),
        targetMinutes: draft.targetMinutes,
        targetSeconds: draft.targetSeconds,
        coverPreview: draft.coverPreview,
        scriptFileName: draft.scriptFileName,
        newScriptFile: selectedFile,
        newCoverFile: coverFile,
        production_brief,
      });
      if (updated) {
        onClose();
      } else {
        setSubmitError("保存失败，请重试");
      }
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      <div className="project-settings" role="dialog" aria-modal="true" aria-label={isEdit ? "编辑项目" : "创建项目"}>
      <button type="button" className="project-settings__backdrop" aria-label="关闭" onClick={onClose} />
      <section className="project-settings__panel" onClick={(e) => e.stopPropagation()}>
        <header className="project-settings__head">
          <h3>{isEdit ? "编辑项目" : "创建项目"}</h3>
          <button type="button" className="project-settings__close" onClick={onClose} aria-label="关闭" disabled={submitting}>
            <X size={18} strokeWidth={1.8} />
          </button>
        </header>

        <div className="project-settings__body">
          <div className="project-settings__col">
            <section className="project-settings__block">
              <h4>基本信息</h4>

              <div className="project-settings__row project-settings__row--identity">
                <label className="project-settings__field">
                  <span className="project-settings__label">
                    <em>*</em>项目名称
                  </span>
                  <input
                    value={draft.title}
                    placeholder="请输入项目名称"
                    onChange={(e) => update("title", e.target.value)}
                  />
                </label>

                <label className="project-settings__field">
                  <span className="project-settings__label">
                    <em>*</em>项目缩写
                  </span>
                  <input
                    value={draft.abbreviation}
                    placeholder="如：雪中"
                    maxLength={5}
                    onCompositionStart={() => { composingRef.current = true; }}
                    onCompositionEnd={(e) => {
                      composingRef.current = false;
                      const v = (e.target as HTMLInputElement).value;
                      update("abbreviation", v.toUpperCase());
                    }}
                    onChange={(e) => {
                      const v = e.target.value;
                      if (composingRef.current) {
                        // 组合阶段：始终把原始值同步到 state，避免 React 受控 value 覆盖 IME 中间态；
                        // .toUpperCase() 延后到 compositionEnd 再做，防止中文拼音被强制大写打断。
                        update("abbreviation", v);
                      } else {
                        update("abbreviation", v.toUpperCase());
                      }
                    }}
                  />
                </label>
              </div>

              <div className="project-settings__field">
                <span className="project-settings__label">
                  <em>*</em>项目权限
                </span>
                <div className="project-settings__radios">
                  <label className={draft.permission === "all" ? "is-active" : ""}>
                    <input
                      type="radio"
                      name="permission"
                      checked={draft.permission === "all"}
                      onChange={() => update("permission", "all")}
                    />
                    全部用户
                  </label>
                  <label className={draft.permission === "specific" ? "is-active" : ""}>
                    <input
                      type="radio"
                      name="permission"
                      checked={draft.permission === "specific"}
                      onChange={() => update("permission", "specific")}
                    />
                    指定用户
                  </label>
                </div>

                {draft.permission === "specific" ? (
                  <div className="project-settings__members">
                    {/* TODO(创建项目弹框-后端需求5): 项目成员写入接口未实现，UI 占位、提交时不发送。 */}
                    <MemberPicker
                      className="project-settings__member-group"
                      itemClassName="project-settings__member"
                      title="导演"
                      members={usersToMemberOptions(users.filter((u) => u.role === "director"))}
                      selectedIds={draft.directorIds}
                      onChange={(ids) => update("directorIds", ids)}
                    />
                    <MemberPicker
                      className="project-settings__member-group"
                      itemClassName="project-settings__member"
                      title="创作者"
                      members={usersToMemberOptions(users.filter((u) => u.role === "artist"))}
                      selectedIds={draft.creatorIds}
                      onChange={(ids) => update("creatorIds", ids)}
                    />
                  </div>
                ) : null}
              </div>
            </section>

            <section className="project-settings__block">
              <h4>创作设置</h4>

              {catalogError ? (
                <p className="project-settings__error">制作设置目录加载失败：{catalogError}</p>
              ) : null}

              <div className="project-settings__field">
                <span className="project-settings__label">
                  <em>*</em>成片类型
                </span>
                <div className="project-settings__style-grid project-settings__style-grid--prod" role="listbox" aria-label="成片类型">
                  {contentTypes.map((item) => {
                    const active = draft.contentType === item.value;
                    const img = PRODUCTION_TYPE_IMAGES[item.label];
                    return (
                      <button
                        key={item.value}
                        type="button"
                        role="option"
                        aria-selected={active}
                        className={`project-settings__style-tile ${active ? "is-active" : ""}`}
                        onClick={() => {
                          update("contentType", item.value);
                          // 切换类型时同步默认风格
                          const fallbackStyleId =
                            item.default_style_id ??
                            styles.find((s) =>
                              item.value === "live_action_drama" ? s.medium === "live_action" : s.medium !== "live_action",
                            )?.style_id ??
                            "";
                          if (fallbackStyleId) update("styleId", fallbackStyleId);
                        }}
                      >
                        {img ? <img src={img} alt="" draggable={false} /> : null}
                        <span>{item.label}</span>
                      </button>
                    );
                  })}
                </div>
              </div>

              <div className="project-settings__row project-settings__row--episode-duration">
                <div className="project-settings__field project-settings__field--episodes">
                  <span className="project-settings__label">
                    <em>*</em>集数
                    <span
                      className="project-settings__hint"
                      tabIndex={0}
                      aria-label="为了防止 agent 拆分不准确，集数只能一集一集增加"
                    >
                      <CircleAlert size={13} strokeWidth={2} />
                      <i className="project-settings__hint-tip">
                        为了防止 agent 拆分不准确，集数只能一集一集增加
                      </i>
                    </span>
                  </span>
                  {/* TODO(创建项目弹框-后端需求2): episode_count 后端未实现，创建时固定首集 EP01。 */}
                  <input type="text" value="第 1 集" readOnly disabled />
                </div>

                <div className="project-settings__field project-settings__field--duration">
                  <span className="project-settings__label">
                    <em>*</em>目标时长
                  </span>
                  <div className="project-settings__duration">
                    <label>
                      <input
                        type="number"
                        min={0}
                        max={999}
                        value={draft.targetMinutes}
                        onChange={(e) =>
                          update("targetMinutes", Math.max(0, Math.min(999, Number(e.target.value) || 0)))
                        }
                      />
                      <span>分</span>
                    </label>
                    <label>
                      <input
                        type="number"
                        min={0}
                        max={59}
                        value={draft.targetSeconds}
                        onChange={(e) =>
                          update("targetSeconds", Math.max(0, Math.min(59, Number(e.target.value) || 0)))
                        }
                      />
                      <span>秒</span>
                    </label>
                  </div>
                </div>
              </div>

              <div className="project-settings__field project-settings__field--aspect">
                <span className="project-settings__label">
                  <em>*</em>画面比例
                </span>
                <div className="project-settings__aspect-presets" role="listbox" aria-label="画面比例">
                  {aspectRatios.map((preset) => {
                    const [rw, rh] = preset.value.split(":").map(Number) as [number, number];
                    const icon = ratioIconSize([rw, rh]);
                    const active = draft.aspectRatio === preset.value;
                    return (
                      <button
                        key={preset.value}
                        type="button"
                        role="option"
                        aria-selected={active}
                        className={`project-settings__aspect-preset ${active ? "is-active" : ""}`}
                        onClick={() => update("aspectRatio", preset.value)}
                      >
                        <span className="project-settings__aspect-slot" aria-hidden="true">
                          <i
                            className="project-settings__aspect-icon"
                            style={{ width: icon.width, height: icon.height }}
                          />
                        </span>
                        <span>{preset.value}</span>
                      </button>
                    );
                  })}
                </div>
              </div>

              <div className="project-settings__field">
                <span className="project-settings__label">
                  <em>*</em>具体视觉风格
                </span>
                <div className="project-settings__style-grid" role="listbox" aria-label="具体视觉风格">
                  {styles.map((item) => {
                    const active = draft.styleId === item.style_id;
                    const img = STYLE_IMAGE_MAP[item.style_id];
                    return (
                      <button
                        key={item.style_id}
                        type="button"
                        role="option"
                        aria-selected={active}
                        title={`${item.label} · ${item.description ?? ""}`}
                        className={`project-settings__style-tile ${active ? "is-active" : ""}`}
                        onClick={() => {
                          const style = styles.find((s) => s.style_id === item.style_id);
                          update("styleId", item.style_id);
                          // 自动同步 contentType 与 style 保持兼容，避免后端校验拒绝
                          if (style?.medium === "live_action" && draft.contentType !== "live_action_drama") {
                            update("contentType", "live_action_drama");
                          } else if (style?.medium !== "live_action" && draft.contentType === "live_action_drama") {
                            // 选了非真人风格时，若当前是真人短剧，切换为动漫短剧（首项兼容）
                            const fallbackContentType =
                              contentTypes.find((c) => c.value !== "live_action_drama")?.value ??
                              draft.contentType;
                            if (fallbackContentType) update("contentType", fallbackContentType);
                          }
                        }}
                      >
                        {img ? <img src={img} alt="" draggable={false} /> : null}
                        <span className="project-settings__style-label">{item.label}</span>
                      </button>
                    );
                  })}
                </div>
              </div>

              <div className="project-settings__field">
                <span className="project-settings__label">
                  <em>*</em>主要文化与时代背景
                </span>
                <div
                  className={`project-settings__multi ${cultureOpen ? "is-open" : ""}`}
                  ref={cultureRef}
                >
                  <button
                    type="button"
                    className="project-settings__multi-trigger"
                    onClick={() => setCultureOpen((v) => !v)}
                  >
                    <span className={draft.cultureCodes.length ? "" : "is-placeholder"}>
                      {draft.cultureCodes.length
                        ? draft.cultureCodes
                            .map((code) => contexts.find((c) => c.context_code === code)?.name ?? code)
                            .join("、")
                        : "请选择主要文化与时代背景"}
                    </span>
                    <ChevronRight size={14} strokeWidth={2} className="project-settings__multi-caret" />
                  </button>
                  {cultureOpen ? (
                    <div className="project-settings__multi-menu" role="listbox" aria-multiselectable="true">
                      {contexts.map((option) => {
                        const checked = draft.cultureCodes.includes(option.context_code);
                        return (
                          <button
                            key={option.context_code}
                            type="button"
                            role="option"
                            aria-selected={checked}
                            className={`project-settings__multi-option ${checked ? "is-checked" : ""}`}
                            onClick={() =>
                              update(
                                "cultureCodes",
                                checked
                                  ? draft.cultureCodes.filter((c) => c !== option.context_code)
                                  : [...draft.cultureCodes, option.context_code],
                              )
                            }
                          >
                            <span>{option.name}</span>
                            {checked ? <Check size={14} strokeWidth={2.4} /> : null}
                          </button>
                        );
                      })}
                    </div>
                  ) : null}
                </div>
              </div>

              <div className="project-settings__field">
                <span className="project-settings__label">
                  <em>*</em>类型
                </span>
                <div className={`project-settings__multi is-up ${genreOpen ? "is-open" : ""}`} ref={genreRef}>
                  <button
                    type="button"
                    className="project-settings__multi-trigger"
                    onClick={() => setGenreOpen((v) => !v)}
                  >
                    <span className={draft.genre ? "" : "is-placeholder"}>
                      {draft.genre || "请选择类型"}
                    </span>
                    <ChevronRight size={14} strokeWidth={2} className="project-settings__multi-caret" />
                  </button>
                  {genreOpen ? (
                    <div className="project-settings__multi-menu" role="listbox" aria-label="类型">
                      {PROJECT_GENRE_OPTIONS.map((option) => {
                        const checked = draft.genre === option;
                        return (
                          <button
                            key={option}
                            type="button"
                            role="option"
                            aria-selected={checked}
                            className={`project-settings__multi-option ${checked ? "is-checked" : ""}`}
                            onClick={() => {
                              update("genre", option);
                              setGenreOpen(false);
                            }}
                          >
                            <span>{option}</span>
                            {checked ? <Check size={14} strokeWidth={2.4} /> : null}
                          </button>
                        );
                      })}
                    </div>
                  ) : null}
                </div>
              </div>
            </section>
          </div>

          <div className="project-settings__col project-settings__col--cover">
            <section className="project-settings__block">
              <h4>剧本文件</h4>
              <button
                type="button"
                className={`project-settings__script ${draft.scriptFileName ? "has-file" : ""}`}
                onClick={() => scriptRef.current?.click()}
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => {
                  event.preventDefault();
                  handleScriptFile(event.dataTransfer.files?.[0] ?? null);
                }}
              >
                {draft.scriptFileName ? (
                  <div className="project-settings__script-file">
                    <FileText size={22} strokeWidth={1.7} />
                    <div>
                      <strong>{draft.scriptFileName}</strong>
                      <p>点击可重新上传</p>
                    </div>
                    <span
                      className="project-settings__script-clear"
                      role="button"
                      tabIndex={0}
                      onClick={(event) => {
                        event.stopPropagation();
                        setSelectedFile(null);
                        update("scriptFileName", undefined);
                        if (scriptRef.current) scriptRef.current.value = "";
                      }}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.stopPropagation();
                          setSelectedFile(null);
                          update("scriptFileName", undefined);
                          if (scriptRef.current) scriptRef.current.value = "";
                        }
                      }}
                    >
                      移除
                    </span>
                  </div>
                ) : (
                  <>
                    <FileText size={26} strokeWidth={1.6} />
                    <strong>点击/拖拽上传剧本</strong>
                    <p>支持 TXT / MD / DOCX（≤20MB）</p>
                  </>
                )}
              </button>
              <input
                ref={scriptRef}
                type="file"
                accept=".txt,.md,.docx"
                hidden
                onChange={(event) => handleScriptFile(event.target.files?.[0] ?? null)}
              />
            </section>

            <section className="project-settings__block">
              <h4>项目封面</h4>
              {/* TODO(创建项目弹框-后端需求2): cover_image 后端未实现，当前仅本地预览、不上传。 */}
              <button
                type="button"
                className={`project-settings__cover ${draft.coverPreview ? "has-image" : ""}`}
                onClick={() => fileRef.current?.click()}
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => {
                  event.preventDefault();
                  handleCoverFile(event.dataTransfer.files?.[0] ?? null);
                }}
              >
                {draft.coverPreview ? (
                  <img src={draft.coverPreview} alt="项目封面预览" />
                ) : (
                  <>
                    <div className="project-settings__cover-hint">
                      <Upload size={28} strokeWidth={1.6} />
                      <strong>点击/拖拽</strong>
                      <p>仅支持 JPG / PNG 格式</p>
                      <p>建议比例 9:16</p>
                    </div>
                    <div className="project-settings__ai-slot">
                      {/* TODO(创建项目弹框-后端需求2): AI 智能封面生成依赖后端，暂为占位。 */}
                      <span
                        className="project-settings__ai"
                        role="button"
                        tabIndex={0}
                        onClick={(event) => {
                          event.stopPropagation();
                        }}
                        onKeyDown={(event) => {
                          if (event.key === "Enter" || event.key === " ") event.stopPropagation();
                        }}
                      >
                        <ImageIcon size={14} strokeWidth={2} />
                        智能封面生成
                        <ChevronRight size={14} strokeWidth={2.2} />
                      </span>
                    </div>
                  </>
                )}
              </button>
              <input
                ref={fileRef}
                type="file"
                accept="image/png,image/jpeg"
                hidden
                onChange={(event) => handleCoverFile(event.target.files?.[0] ?? null)}
              />
            </section>
          </div>
        </div>

        {submitError ? <p className="project-settings__error">{submitError}</p> : null}

        <footer className="project-settings__foot">
          {isEdit ? (
            <>
              <button type="button" className="project-settings__cancel" onClick={onClose} disabled={submitting}>
                取消
              </button>
              <button
                type="button"
                className="project-settings__submit primary"
                onClick={() => setConfirmEditOpen(true)}
                disabled={!canSubmit || submitting}
              >
                {submitting ? "保存中…" : "保存"}
              </button>
            </>
          ) : (
            <>
              <button type="button" className="project-settings__cancel" onClick={onClose} disabled={submitting}>
                取消
              </button>
              <button
                type="button"
                className="project-settings__submit"
                onClick={() => handleSubmit(false)}
                disabled={!canSubmit}
              >
                {submitting ? "创建中…" : "创建并保存"}
              </button>
              <button
                type="button"
                className="project-settings__submit primary"
                onClick={() => handleSubmit(true)}
                disabled={!canSubmit}
              >
                {submitting ? "创建中…" : "创建并进入剧本阅读"}
              </button>
            </>
          )}
        </footer>
      </section>
    </div>

    {confirmEditOpen ? (
      <ConfirmModal
        title="确认修改项目信息"
        description="制作设置修改后，已有生成结果可能需要重新运行。"
        confirmText="确认修改"
        cancelText="取消"
        confirming={submitting}
        confirmDisabled={submitting}
        onClose={() => setConfirmEditOpen(false)}
        onConfirm={async () => {
          setConfirmEditOpen(false);
          // 等一个 tick 让确认弹框先退出，避免视觉上两个弹框同时消失时的轻微错位
          await new Promise((r) => setTimeout(r, 0));
          await handleEditSubmit();
        }}
      />
    ) : null}
    </>
  );
}

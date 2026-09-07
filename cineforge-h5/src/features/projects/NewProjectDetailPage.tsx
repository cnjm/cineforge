import React from "react";
import { ArrowLeft, Pencil, Upload } from "lucide-react";
import { api, AuthExpiredError, setAuthSession } from "../../api";
import { PageHeader } from "../../shared/components";
import type {
  AgentRun,
  AgentStageMapItem,
  Asset,
  AssetVariantPlan,
  BreakdownDraftData,
  DashboardSummary,
  PageKey,
  ProductionContentType,
  Project,
  ProjectCreatePayload,
  ProjectDetailModule,
  ProjectEpisode,
  ProjectEntryMode,
  ProjectImportPayload,
  ProjectProductionBrief,
  ProjectUpdatePayload,
  ScriptSegment,
  SceneGating,
  Storyboard,
  Task,
  User,
} from "../../types";
import { breakdownDraftScopeKey, hasPendingAgentRun, isAgentPending, stageForProject } from "../../utils";
import { matchesLineagePayload } from "../../runData";
import { coverForProjectId } from "./sharedProjectCatalog";
import { EpisodeWorkbench, episodeStatusLabel, episodeStatusTone, episodeStatusTagClass } from "./EpisodeWorkbench";
import { NewProjectPage } from "./NewProjectPage";
import { ArchiveReviewPanel, ProjectProductionStage } from "./ProjectProductionStage";
import { stageMeta } from "./projectMeta";
import { TaskAssignPage } from "./TaskAssignPage";
import { ProjectSettingsModal, type EditInitialDraft } from "./ProjectSettingsModal";

type ProjectDetailPageProps = {
  project?: Project;
  entryMode: ProjectEntryMode;
  dashboard: DashboardSummary | null;
  jump: (page: PageKey) => void;
  users: User[];
  runs: AgentRun[];
  agentStageMap: AgentStageMapItem[];
  scriptSegments: ScriptSegment[];
  storyboards: Storyboard[];
  tasks: Task[];
  assets: Asset[];
  assetVariantPlans: AssetVariantPlan[];
  draftOverrides: Record<string, BreakdownDraftData>;
  breakdownDraftSyncing: Record<string, boolean>;
  loading: boolean;
  currentUser: User;
  onRunBreakdownStep: (step: string, projectId?: string, episodeId?: string, scriptVersionId?: string) => Promise<boolean>;
  onRetryPromptDistribution: (projectId: string, runId: string) => Promise<void>;
  onCreateTemporaryAssetProduction: (payload: TemporaryAssetProductionPayload) => Promise<boolean>;
  onCreateProject: (payload: ProjectCreatePayload) => Promise<Project | null>;
  onUpdateProject: (projectId: string, payload: ProjectUpdatePayload) => Promise<Project | null>;
  onImportProject: (payload: ProjectImportPayload, file: File) => Promise<Project | null>;
  onRunProjectAgent: (agentType: string) => Promise<void>;
  onMaterializeAgentRun: (run: AgentRun) => Promise<void>;
  onAssignTask: (task: Task, assigneeId: string, reassignmentReason?: string) => Promise<void>;
  onBulkAssign: (projectId: string, scope: string, key: string, assigneeId: string, episodeId?: string, reassignmentReason?: string) => Promise<void>;
  onGenerateStoryboardTasks: (projectId: string, episodeId: string, scriptVersionId: string) => Promise<void>;
  onCreateAssetVariantPlan: (projectId: string, payload: { asset_id: string; episode_id?: string | null; script_version_id?: string | null; variant_kind: string; title_zh: string; description_zh: string; storyboard_ids: string[]; source?: string; confidence?: number | null }) => Promise<void>;
  onUpdateAssetVariantPlan: (planId: string, payload: Partial<Pick<AssetVariantPlan, "title_zh" | "description_zh" | "status" | "storyboard_ids">>) => Promise<void>;
  onFetchSceneGating: (projectId: string) => Promise<SceneGating[]>;
  onLoadProjectData: (projectId: string, options?: { includePayload?: boolean; includeCollections?: boolean; includeUsers?: boolean; episodeId?: string; episodeCode?: string; scriptVersionId?: string }) => Promise<unknown>;
  onLoadBreakdownDraft: (projectId: string, episodeId: string, scriptVersionId: string) => Promise<void>;
  onSaveBreakdownDraft: (draft: BreakdownDraftData) => Promise<boolean>;
  onConfirmScriptSegments: (draft: BreakdownDraftData) => Promise<void>;
  onLockEpisodeBreakdown: (episodeCode: string, draft: BreakdownDraftData) => Promise<boolean>;
  onDeleteProject: (project: Project, adminPassword: string) => Promise<void>;
  onOpenProject: (projectId: string) => void;
  onAddEpisode: (projectId: string) => void;
  onAddScriptVersion: (projectId: string, episodeId: string) => void;
  /** 编辑项目提交：复用项目管理页同一弹框，走 updateProjectSettings */
  onSubmitEdit?: (payload: EditInitialDraft & {
    newScriptFile: File | null;
    newCoverFile: File | null;
    production_brief: ProjectProductionBrief | null;
  }) => Promise<Project | null>;
};

type ProjectIdentity = {
  title: string;
  abbreviation: string;
  genre: string;
  chips: Array<{ label: string; value: string }>;
};

export function NewProjectDetailPage({
  project,
  entryMode,
  dashboard,
  jump,
  users,
  runs,
  agentStageMap,
  scriptSegments,
  storyboards,
  tasks,
  assets,
  assetVariantPlans,
  draftOverrides,
  breakdownDraftSyncing,
  loading,
  currentUser,
  onRunBreakdownStep,
  onRetryPromptDistribution,
  onCreateTemporaryAssetProduction,
  onCreateProject,
  onUpdateProject,
  onImportProject,
  onRunProjectAgent,
  onMaterializeAgentRun,
  onAssignTask,
  onBulkAssign,
  onGenerateStoryboardTasks,
  onCreateAssetVariantPlan,
  onUpdateAssetVariantPlan,
  onFetchSceneGating,
  onLoadProjectData,
  onLoadBreakdownDraft,
  onSaveBreakdownDraft,
  onConfirmScriptSegments,
  onLockEpisodeBreakdown,
  onDeleteProject,
  onOpenProject,
  onAddEpisode,
  onAddScriptVersion,
  onSubmitEdit,
}: ProjectDetailPageProps) {
  const initialStage = stageForProject(project, 0);
  const meta = stageMeta[initialStage];
  const hasProject = Boolean(project);
  const projectTasks = project ? tasks.filter((task) => task.project_id === project.id) : [];
  const projectAssets = React.useMemo(() => {
    if (!project) return [];
    const referencedAssetIds = new Set(projectTasks.map((task) => task.asset_id).filter(Boolean));
    return assets.filter((asset) => asset.project_id === project.id
      || (!asset.project_id && referencedAssetIds.has(asset.id)));
  }, [assets, project?.id, projectTasks]);

  const [activeModule, setActiveModule] = React.useState<ProjectDetailModule>(
    entryMode.type === "project_overview" && entryMode.module ? entryMode.module : "import"
  );
  // 当 entryMode 变化时（如从其他页面跳转携带 module 参数），同步更新选中 tab
  React.useEffect(() => {
    if (entryMode.type === "project_overview" && entryMode.module) {
      setActiveModule(entryMode.module);
    }
  }, [entryMode]);
  const [sceneGating, setSceneGating] = React.useState<SceneGating[]>([]);
  const [unlocked, setUnlocked] = React.useState(false);
  const [unlocking, setUnlocking] = React.useState(false);
  const [episodes, setEpisodes] = React.useState<ProjectEpisode[]>([]);
  const [selectedEpisodeId, setSelectedEpisodeId] = React.useState("");
  const [episodeDetails, setEpisodeDetails] = React.useState<Record<string, ProjectEpisode>>({});
  const [episodesLoading, setEpisodesLoading] = React.useState(false);
  const [editingSettings, setEditingSettings] = React.useState(false);
  const [coverPreviewUrl, setCoverPreviewUrl] = React.useState<string | null>(null);
  const coverInputRef = React.useRef<HTMLInputElement>(null);
  const [scriptUploadEpisodeId, setScriptUploadEpisodeId] = React.useState<string | null>(null);
  const [addEpisodeOpen, setAddEpisodeOpen] = React.useState(false);

  const isProjectOverview = entryMode.type === "project_overview";
  const isCreateProject = entryMode.type === "create_project";
  const fromProjectManagement = isProjectOverview
    && (entryMode as { from?: string }).from === "projectManagement";

  const entryEpisodeId = entryMode.type === "add_script_version"
    ? entryMode.episodeId
    : entryMode.type === "project_overview" ? entryMode.episodeId || "" : "";
  const needsEpisodeData = activeModule === "revision" || activeModule === "assignment";

  React.useEffect(() => {
    if (entryEpisodeId) setSelectedEpisodeId(entryEpisodeId);
  }, [project?.id, entryEpisodeId]);

  React.useEffect(() => {
    setUnlocked(false);
  }, [project?.id, project?.status]);

  React.useEffect(() => {
    setActiveModule(entryMode.type === "project_overview" ? entryMode.module || "import" : "import");
  }, [project?.id, entryMode.type, entryMode.type === "project_overview" ? entryMode.module : undefined]);

  const taskDistributed = ["production", "assembly", "completed"].includes(String(project?.status || ""));
  const readonly = taskDistributed && !unlocked;

  const modules = ([
    { key: "import", label: "总览", desc: "阶段与设置", available: true, lockedView: readonly },
    { key: "revision", label: "分集工作台", desc: "围读与拆解", available: hasProject, lockedView: readonly },
    { key: "assignment", label: "任务分发", desc: "人员与任务", available: hasProject, lockedView: readonly },
    { key: "progress", label: "进度跟踪", desc: "生产进度", available: hasProject && taskDistributed, lockedView: false },
    { key: "archive", label: "终审归档", desc: "成片审核", available: hasProject && taskDistributed, lockedView: false },
  ] satisfies Array<{ key: ProjectDetailModule; label: string; desc: string; available: boolean; lockedView: boolean }>).filter((item) => item.available);
  const selectedModule: ProjectDetailModule = modules.some((item) => item.key === activeModule) ? activeModule : modules[0]?.key || "import";
  const selectedReadonly = isProjectOverview && readonly && ["import", "revision", "assignment"].includes(selectedModule);

  const detailTitle = isCreateProject
    ? "新增项目"
    : entryMode.type === "add_episode"
      ? `${project?.title || "项目"} / 新增分集`
      : entryMode.type === "add_script_version"
        ? `${project?.title || "项目"} / 上传剧本新版本`
        : `${project?.title || "项目"} / 项目详情`;

  const projectIdentity = React.useMemo(() => {
    if (!project) return null;
    return buildProjectIdentity(project);
  }, [project]);

  const projectCoverUrl = React.useMemo(() => {
    if (coverPreviewUrl) return coverPreviewUrl;
    if (!project) return "";
    const backendCover = String(
      (project as unknown as { cover_image?: string; coverImage?: string }).cover_image
      || (project as unknown as { coverImage?: string }).coverImage
      || "",
    ).trim();
    return backendCover || coverForProjectId(project.id);
  }, [project, coverPreviewUrl]);

  const projectDetailFields = React.useMemo(() => {
    if (!projectIdentity) return [];
    return buildProjectDetailFields(projectIdentity, meta.label);
  }, [projectIdentity, meta.label]);

  const loadEpisodes = React.useCallback(async () => {
    if (!project?.id || !needsEpisodeData) {
      setEpisodes([]);
      setSelectedEpisodeId("");
      return;
    }
    setEpisodesLoading(true);
    try {
      const items = await api.get<ProjectEpisode[]>(`/projects/${project.id}/episodes`);
      setEpisodes(items);
      setSelectedEpisodeId((current) => items.some((item) => item.id === current)
        ? current
        : items.some((item) => item.id === entryEpisodeId) ? entryEpisodeId : items[0]?.id || "");
    } catch {
      setEpisodes([]);
    } finally {
      setEpisodesLoading(false);
    }
  }, [project?.id, entryEpisodeId, needsEpisodeData]);

  React.useEffect(() => {
    void loadEpisodes();
  }, [loadEpisodes, project?.updated_at]);

  React.useEffect(() => {
    if (!project?.id || !selectedEpisodeId || activeModule !== "revision") return;
    let active = true;
    void api.get<ProjectEpisode>(`/projects/${project.id}/episodes/${selectedEpisodeId}`).then((detail) => {
      if (active) setEpisodeDetails((current) => ({ ...current, [selectedEpisodeId]: detail }));
    }).catch(() => undefined);
    return () => { active = false; };
  }, [project?.id, selectedEpisodeId, activeModule]);

  React.useEffect(() => {
    let active = true;
    if (project?.id && activeModule === "assignment") {
      onFetchSceneGating(project.id).then((rows) => {
        if (active) setSceneGating(rows);
      });
    } else {
      setSceneGating([]);
    }
    return () => {
      active = false;
    };
  }, [project?.id, activeModule, onFetchSceneGating]);

  async function unlockReadonlyModules() {
    const password = window.prompt("请输入当前登录账号密码解锁修改");
    if (!password) return;
    setUnlocking(true);
    try {
      await api.post("/auth/verify-password", { password });
      setUnlocked(true);
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        setAuthSession(null);
        window.location.reload();
        return;
      }
      window.alert(err instanceof Error ? err.message : "密码校验失败");
    } finally {
      setUnlocking(false);
    }
  }

  const selectedEpisodeBase = episodes.find((item) => item.id === selectedEpisodeId);
  const selectedEpisode = selectedEpisodeId ? episodeDetails[selectedEpisodeId] || selectedEpisodeBase : undefined;

  const projectDataModules: ProjectDetailModule[] = ["revision", "assignment", "progress", "archive"];
  React.useEffect(() => {
    if (!project?.id || !projectDataModules.includes(activeModule) || (needsEpisodeData && !selectedEpisode)) return;
    void onLoadProjectData(project.id, {
      includePayload: activeModule === "revision",
      includeCollections: true,
      includeUsers: activeModule === "assignment",
      episodeId: selectedEpisode?.id,
      episodeCode: selectedEpisode?.episode_code,
      scriptVersionId: selectedEpisode?.current_version_id || undefined,
    });
  }, [project?.id, activeModule, selectedEpisode?.id, selectedEpisode?.current_version_id, onLoadProjectData]);

  const selectedDraftKey = project?.id && selectedEpisode?.current_version_id
    ? breakdownDraftScopeKey(project.id, selectedEpisode.id, selectedEpisode.current_version_id)
    : "";
  const selectedDraft = selectedDraftKey ? draftOverrides[selectedDraftKey] : undefined;
  const selectedDraftSyncing = selectedDraftKey ? Boolean(breakdownDraftSyncing[selectedDraftKey]) : false;

  React.useEffect(() => {
    if (activeModule !== "revision" || !project?.id || !selectedEpisode?.id || !selectedEpisode.current_version_id) return;
    void onLoadBreakdownDraft(project.id, selectedEpisode.id, selectedEpisode.current_version_id);
  }, [project?.id, activeModule, selectedEpisode?.id, selectedEpisode?.current_version_id, onLoadBreakdownDraft]);

  const selectedScriptSegments = selectedEpisode
    ? scriptSegments.filter((item) => item.episode_id === selectedEpisode.id || item.episode_code === selectedEpisode.episode_code)
    : [];
  const selectedStoryboards = selectedEpisode
    ? storyboards.filter((item) => item.episode_num === selectedEpisode.episode_no)
    : [];
  const selectedSegmentIds = new Set(selectedScriptSegments.map((item) => item.id));
  const selectedStoryboardIds = new Set(selectedStoryboards.map((item) => item.id));
  const selectedTasks = projectTasks.filter((item) => {
    if (selectedEpisode?.current_version_id && item.script_version_id) {
      return item.script_version_id === selectedEpisode.current_version_id;
    }
    return item.episode_id === selectedEpisode?.id
      || (item.script_segment_id && selectedSegmentIds.has(item.script_segment_id))
      || (item.storyboard_id && selectedStoryboardIds.has(item.storyboard_id));
  });
  const selectedRuns = selectedEpisode ? runs.filter((run) => {
    const input = runLineagePayload(run);
    return episodeLineageMatches(input, selectedEpisode);
  }) : [];

  const runCompletionSignal = React.useMemo(
    () => selectedRuns
      .filter((run) => !isAgentPending(run))
      .map((run) => `${run.id}:${run.status}:${run.updated_at || ""}`)
      .sort()
      .join("|"),
    [selectedRuns],
  );
  const lastCompletionRef = React.useRef<{ scope: string; signal: string } | null>(null);
  React.useEffect(() => {
    if (activeModule !== "revision" || !project?.id || !selectedEpisode?.id || !selectedEpisode.current_version_id) {
      lastCompletionRef.current = null;
      return;
    }
    const scope = selectedDraftKey;
    const baseline = lastCompletionRef.current;
    if (!baseline || baseline.scope !== scope) {
      lastCompletionRef.current = { scope, signal: runCompletionSignal };
      return;
    }
    if (baseline.signal === runCompletionSignal) return;
    lastCompletionRef.current = { scope, signal: runCompletionSignal };
    void onLoadBreakdownDraft(project.id, selectedEpisode.id, selectedEpisode.current_version_id);
  }, [runCompletionSignal, selectedDraftKey, activeModule, project?.id, selectedEpisode?.id, selectedEpisode?.current_version_id, onLoadBreakdownDraft]);

  // 构建编辑项目弹框的初始值（复用 ProjectManagementPage 同一逻辑）
  function buildEditInitial(): EditInitialDraft | null {
    if (!project) return null;
    const brief = project.production_brief as unknown as ProjectProductionBrief | null;
    if (!brief) {
      window.alert("该项目创建于旧版本，缺少创作设置，暂不支持通过此弹框编辑。");
      return null;
    }
    const contentType = (brief.content_type as ProductionContentType) || "";
    const aspectRatio = (brief.delivery_aspect_ratio as "9:16" | "16:9") || "";
    const cultureCodes = Array.isArray(brief.cultural_contexts)
      ? brief.cultural_contexts.map((c) => (c as { context_code?: string })?.context_code || "").filter(Boolean)
      : [];
    const DEFAULT_TOTAL_SECONDS = 120;
    const scriptLabel = project.latest_import
      ? `剧本（v${project.latest_import.script_version_no} 已导入）`
      : "剧本（已导入）";
    const coverPreview = (project as unknown as { cover_image?: string }).cover_image;
    return {
      projectId: project.id,
      project,
      title: project.title || "",
      abbreviation: project.project_prefix || "",
      contentType,
      aspectRatio,
      styleId: brief.primary_style_id || "",
      cultureCodes,
      genre: project.genre || "",
      targetMinutes: Math.floor(DEFAULT_TOTAL_SECONDS / 60),
      targetSeconds: DEFAULT_TOTAL_SECONDS % 60,
      coverPreview,
      scriptFileName: scriptLabel,
    };
  }

  async function handleEditSubmit(payload: EditInitialDraft & {
    newScriptFile: File | null;
    newCoverFile: File | null;
    production_brief: ProjectProductionBrief | null;
  }): Promise<Project | null> {
    if (!onSubmitEdit) return null;
    const updated = await onSubmitEdit(payload);
    setEditingSettings(false);
    return updated;
  }

  async function handleDetailCoverFile(file: File | null) {
    if (!file || !project) return;
    const reader = new FileReader();
    reader.onload = () => {
      setCoverPreviewUrl(reader.result as string);
    };
    reader.readAsDataURL(file);
    // TODO(项目详情-后端需求1): 后端需支持项目封面图上传接口，当前仅前端预览
    // await api.upload(`/projects/${project.id}/cover`, file);
  }

  async function deleteSelectedEpisode() {
    if (!project?.id || !selectedEpisode) return;
    const code = window.prompt(`输入分集编号确认删除（${selectedEpisode.episode_code}）`);
    if (!code || code !== selectedEpisode.episode_code) return;
    try {
      await api.delete(`/projects/${project.id}/episodes/${selectedEpisode.id}`, {
        confirm_episode_code: selectedEpisode.episode_code,
        delete_files: true,
      });
      setEpisodeDetails((current) => {
        const next = { ...current };
        delete next[selectedEpisode.id];
        return next;
      });
      await loadEpisodes();
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        setAuthSession(null);
        window.location.reload();
        return;
      }
      window.alert(err instanceof Error ? err.message : "删除分集失败");
    }
  }

  function handleAddNextEpisode() {
    if (!project) return;
    setAddEpisodeOpen(true);
  }

  function handleCloseAddEpisode() {
    setAddEpisodeOpen(false);
  }

  async function handleImportNewEpisode(payload: ProjectImportPayload, file: File) {
    if (!project) return null;
    try {
      const created = await onImportProject?.(payload, file) ?? null;
      setAddEpisodeOpen(false);
      await loadEpisodes();
      if (created?.id) {
        setSelectedEpisodeId(created.id);
      }
      return created;
    } catch (err) {
      window.alert(err instanceof Error ? err.message : "导入分集失败");
      return null;
    }
  }

  return (
    <div className={`admin-page ${isProjectOverview && project ? "project-detail-page" : ""}`}>
      {isProjectOverview && project && projectIdentity ? (
        <>
          {(readonly || isCreateProject || !fromProjectManagement) ? (
            <div className="project-detail-toolbar">
              <div className="project-detail-toolbar__actions">
                {readonly ? (
                  <button className="admin-btn-ghost" disabled={unlocking} type="button" onClick={unlockReadonlyModules}>
                    解锁修改
                  </button>
                ) : null}
                {isCreateProject ? (
                  <button className="admin-btn-ghost" type="button" onClick={() => jump("projectOverview")}>
                    <ArrowLeft size={16} />
                    返回项目列表
                  </button>
                ) : null}
                {!fromProjectManagement ? (
                  <button
                    className="admin-btn-danger"
                    disabled={loading}
                    type="button"
                    onClick={() => {
                      const password = window.prompt("请输入管理员密码确认软删除项目");
                      if (password !== null) void onDeleteProject(project, password);
                    }}
                  >
                    删除项目
                  </button>
                ) : null}
              </div>
            </div>
          ) : null}

          {readonly ? (
            <div className="admin-notice">
              项目已进入任务分发后的生产阶段，设置与拆解默认只读。
            </div>
          ) : null}

          <header className="project-detail-summary">
            <div className="project-detail-summary__main">
              <div className="project-detail-summary__settings" aria-label="项目详情">
                <div className="project-detail-summary__settings-head">
                  <h3 className="project-detail-summary__settings-title">项目详情</h3>
                  <button
                    type="button"
                    className="project-detail-summary__edit"
                    disabled={readonly}
                    onClick={() => setEditingSettings(true)}
                  >
                    <Pencil size={12} strokeWidth={2} />
                    编辑设置
                  </button>
                </div>
                <div className="project-detail-summary__body">
                  <div className="project-detail-summary__cover">
                    {projectCoverUrl ? (
                      <img src={projectCoverUrl} alt="" draggable={false} />
                    ) : (
                      <button
                        type="button"
                        className="project-detail-summary__cover-upload"
                        disabled={readonly}
                        onClick={() => {
                          if (readonly) return;
                          coverInputRef.current?.click();
                        }}
                      >
                        <Upload size={18} strokeWidth={1.8} />
                        上传封面
                      </button>
                    )}
                    <input
                      ref={coverInputRef}
                      type="file"
                      accept="image/png,image/jpeg,image/jpg,image/webp"
                      hidden
                      onChange={(event) => {
                        handleDetailCoverFile(event.target.files?.[0] ?? null);
                        event.target.value = "";
                      }}
                    />
                  </div>
                  <div className="project-detail-summary__settings-grid">
                    {projectDetailFields.map((field) => (
                      <div key={field.label}>
                        <span>{field.label}</span>
                        <strong>{field.value}</strong>
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            </div>
          </header>

          <nav className="project-detail-tabs" aria-label="项目详情切换">
            {modules.map((item) => (
              <button
                key={item.key}
                type="button"
                className={`project-detail-tabs__item ${selectedModule === item.key ? "is-active" : ""} ${item.lockedView ? "is-readonly" : ""}`}
                onClick={() => {
                  setActiveModule(item.key);
                  if (item.key !== "import") setEditingSettings(false);
                }}
              >
                {item.label}
                {item.lockedView ? <i>只读</i> : null}
              </button>
            ))}
          </nav>
        </>
      ) : (
        <div className="admin-section-head admin-section-head--detail">
          <div>
            <h2>{detailTitle}</h2>
            <p className="admin-page__desc">
              {isCreateProject
                ? "创建全新项目并导入首集剧本。"
                : entryMode.type === "add_episode"
                  ? "为当前项目导入下一集剧本。"
                  : entryMode.type === "add_script_version"
                    ? "为当前分集保留历史并创建剧本新版本。"
                    : "项目设置统一维护；分集内容按故事梗概和拆解流程独立推进。"}
            </p>
          </div>
          <div className="admin-page__actions">
            {isCreateProject ? (
              <button className="admin-btn-ghost" type="button" onClick={() => jump("projectOverview")}>
                <ArrowLeft size={16} />
                返回项目列表
              </button>
            ) : null}
            {!isCreateProject && !isProjectOverview && project ? (
              <button className="admin-btn-ghost" type="button" onClick={() => onOpenProject(project.id)}>
                <ArrowLeft size={16} />
                返回项目总览
              </button>
            ) : null}
          </div>
        </div>
      )}

      {isProjectOverview && project && editingSettings ? (
        (() => {
          const editInitial = buildEditInitial();
          if (!editInitial) {
            return (
              <ProjectSettingsModal
                users={users}
                mode="edit"
                onClose={() => setEditingSettings(false)}
                onSubmit={async () => null}
                onSubmitEdit={handleEditSubmit}
              />
            );
          }
          return (
            <ProjectSettingsModal
              users={users}
              mode="edit"
              initial={editInitial}
              onClose={() => setEditingSettings(false)}
              onSubmit={async () => null}
              onSubmitEdit={handleEditSubmit}
            />
          );
        })()
      ) : null}

      <div className={`project-detail-stack admin-detail-stack ${readonly ? "readonly-stack" : ""}`}>
        {!isProjectOverview || selectedModule === "import" ? (
          <section className={`project-module admin-panel ${selectedReadonly ? "module-readonly" : ""} ${isProjectOverview ? "project-module--flush" : ""}`}>
            {isProjectOverview && project ? (
              <ProjectOverviewDashboard
                project={project}
                stageLabel={meta.label}
                episodeCount={episodes.length}
                taskCount={Math.max(projectTasks.length, episodes.length ? 36 : 0)}
                episodes={episodes}
              />
            ) : entryMode.type === "add_script_version" ? (
              <NewProjectPage
                key={`${entryMode.type}-${project?.id || "new"}-${entryEpisodeId}`}
                embedded
                currentProject={project}
                mode={entryMode.type}
                targetEpisodeId={String((entryMode as { episodeId?: string }).episodeId || "") || undefined}
                seedEpisodes={episodes}
                loading={loading}
                readOnly={readonly}
                breakdownRunning={hasPendingAgentRun(runs, "script_breakdown")}
                scriptSegments={scriptSegments}
                onRunReading={(pid, eid, svid) => onRunBreakdownStep("reading", pid, eid, svid)}
                onCreateProject={onCreateProject}
                onUpdateProject={onUpdateProject}
                onImportProject={onImportProject}
              />
            ) : null}
          </section>
        ) : null}

        {hasProject && selectedModule === "revision" && project ? (
          <EpisodeWorkbench
            project={project}
            episodes={episodes}
            selectedEpisodeId={selectedEpisodeId}
            onSelectEpisode={setSelectedEpisodeId}
            episodesLoading={episodesLoading}
            selectedEpisode={selectedEpisode}
            selectedDraft={selectedDraft}
            selectedDraftSyncing={selectedDraftSyncing}
            selectedRuns={selectedRuns}
            selectedScriptSegments={selectedScriptSegments}
            selectedStoryboards={selectedStoryboards}
            selectedTasks={selectedTasks}
            loading={loading}
            readonly={selectedReadonly}
            currentUser={currentUser}
            onAddEpisode={handleAddNextEpisode}
            onCloseAddEpisode={handleCloseAddEpisode}
            addEpisodeOpen={addEpisodeOpen}
            onCreateProject={onCreateProject}
            onUpdateProject={onUpdateProject}
            onImportProject={handleImportNewEpisode}
            onOpenScriptUpload={setScriptUploadEpisodeId}
            onDeleteEpisode={() => { void deleteSelectedEpisode(); }}
            onSaveDraft={onSaveBreakdownDraft}
            onConfirmScriptSegments={onConfirmScriptSegments}
            onRunBreakdownStep={onRunBreakdownStep}
            onRetryPromptDistribution={onRetryPromptDistribution}
            onCreateTemporaryAssetProduction={onCreateTemporaryAssetProduction}
            onLockEpisodeBreakdown={onLockEpisodeBreakdown}
            onGenerateStoryboardTasks={onGenerateStoryboardTasks}
            onJumpToTasks={() => jump("myTasks")}
            onOpenTaskAssignment={() => setActiveModule("assignment")}
          />
        ) : null}

        {hasProject && selectedModule === "assignment" ? (
          <section className={`project-module admin-panel ${selectedReadonly ? "module-readonly" : ""}`}>
            <PageHeader
              title="任务分发"
              desc="资产生产、分镜生产和剪辑任务按分集分配；生产阶段后默认只读。"
              action={episodes.length ? (
                <label className="episode-task-selector">
                  <span>当前分集</span>
                  <select value={selectedEpisodeId} onChange={(event) => setSelectedEpisodeId(event.target.value)}>
                    {episodes.map((item) => <option key={item.id} value={item.id}>{item.episode_code}</option>)}
                  </select>
                </label>
              ) : undefined}
            />
            <TaskAssignPage
              embedded
              readOnly={readonly}
              users={users}
              tasks={selectedTasks}
              assets={projectAssets}
              assetVariantPlans={assetVariantPlans.filter((item) => !selectedEpisode || item.episode_id === selectedEpisode.id)}
              sceneGating={sceneGating}
              projectStatus={project?.status}
              onAssignTask={onAssignTask}
              onBulkAssign={(scope, key, assigneeId, reassignmentReason) => onBulkAssign(
                project!.id,
                scope,
                key,
                assigneeId,
                scope === "scene" || scope === "all_storyboards" ? selectedEpisode?.id : undefined,
                reassignmentReason,
              )}
              projectId={project!.id}
              episodeId={selectedEpisode?.id}
              onCreateAssetVariantPlan={onCreateAssetVariantPlan}
              onUpdateAssetVariantPlan={onUpdateAssetVariantPlan}
              jump={jump}
            />
          </section>
        ) : null}

        {hasProject && taskDistributed && selectedModule === "progress" ? (
          <section className="project-module admin-panel">
            <PageHeader title="进度跟踪" desc="任务分发完成后展示脚本、分镜、图片、视频和成片进度。" />
            <ProjectProductionStage dashboard={dashboard} scriptSegments={scriptSegments} storyboards={storyboards} tasks={projectTasks} assets={projectAssets} jump={jump} />
          </section>
        ) : null}

        {hasProject && taskDistributed && selectedModule === "archive" ? (
          <section className="project-module admin-panel">
            <PageHeader title="终审归档" desc="剪辑成片上传后，导演审核并归档到资产库。" />
            <ArchiveReviewPanel tasks={projectTasks} assets={projectAssets} />
          </section>
        ) : null}
      </div>
      {hasProject && scriptUploadEpisodeId ? (
        <NewProjectPage
          key={`script-upload-${project.id}-${scriptUploadEpisodeId}`}
          asModal
          embedded
          currentProject={project}
          mode="add_script_version"
          targetEpisodeId={scriptUploadEpisodeId}
          seedEpisodes={episodes}
          loading={loading}
          readOnly={false}
          breakdownRunning={hasPendingAgentRun(runs, "script_breakdown")}
          scriptSegments={scriptSegments}
          onClose={() => setScriptUploadEpisodeId(null)}
          onRunReading={(pid, eid, svid) => onRunBreakdownStep("reading", pid, eid, svid)}
          onCreateProject={onCreateProject}
          onUpdateProject={onUpdateProject}
          onImportProject={onImportProject}
        />
      ) : null}
    </div>
  );
}

function ProjectOverviewDashboard({
  project,
  stageLabel,
  episodeCount,
  taskCount,
  episodes,
}: {
  project: Project;
  stageLabel: string;
  episodeCount: number;
  taskCount: number;
  episodes: ProjectEpisode[];
}) {
  return (
    <div className="project-overview-dash">
      <section className="project-overview-dash__kpis" aria-label="项目状态">
        <article>
          <span>当前阶段</span>
          <strong className="project-overview-dash__stage">{stageLabel}</strong>
        </article>
        <article>
          <span>分集数量</span>
          <strong>{episodeCount}</strong>
        </article>
        <article>
          <span>任务数量</span>
          <strong>{taskCount}</strong>
        </article>
      </section>

      <section className={`project-overview-dash__episodes${episodes.length ? "" : " is-empty"}`}>
        <div className="project-overview-dash__settings-head">
          <div>
            <h3>分集进度</h3>
            <p>{episodes.length ? `共 ${episodes.length} 集 · 展示最近推进中的分集` : "暂无分集"}</p>
          </div>
        </div>
        {episodes.length ? (
          <ul className="project-overview-dash__episode-list">
            {episodes.slice(0, 8).map((episode) => (
              <li key={episode.id}>
                <div className="project-overview-dash__episode-main">
                  <strong>{episode.episode_code}</strong>
                  <em>{episode.summary?.trim() || `第 ${episode.episode_no} 集`}</em>
                </div>
                <span className={episodeStatusTagClass(episodeStatusTone(episode.breakdown_status))}>
                  {episodeStatusLabel(episode.breakdown_status)}
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <div className="empty-hint project-overview-dash__empty">还没有分集。可先新增分集并上传剧本，系统会在围读环节启动 Agent。</div>
        )}
      </section>
      <p className="project-overview-dash__foot"><span>项目 ID</span> · <strong>{project.id}</strong></p>
    </div>
  );
}

function recordValue(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function buildProjectIdentity(project: Project): ProjectIdentity {
  const brief = recordValue(project.production_brief);
  const contentTypeMap: Record<string, string> = {
    animated_drama: "动漫短剧",
    live_action_drama: "真人短剧",
    hybrid_drama: "动真结合",
  };
  const contentType = String(brief.content_type || "");
  const cultures = Array.isArray(brief.cultural_contexts)
    ? brief.cultural_contexts
      .map((item) => {
        if (!item || typeof item !== "object") return "";
        const row = item as Record<string, unknown>;
        return String(row.name || row.context_code || "");
      })
      .filter(Boolean)
    : [];
  const durationObj = recordValue(brief.episode_brief);
  const durationSeconds = Number(durationObj.target_duration_seconds || 0);
  const duration = durationSeconds > 0
    ? `${Math.floor(durationSeconds / 60)} 分 ${durationSeconds % 60} 秒`
    : "—";
  const style = String(brief.primary_style_label || brief.primary_style_id || "").trim() || "未设置";
  const aspect = String(brief.delivery_aspect_ratio || "9:16");
  const abbreviation = String(project.project_prefix || "").trim() || "—";
  const genre = String(project.genre || "").trim() || "未设置";

  return {
    title: project.title || "未命名项目",
    abbreviation,
    genre,
    chips: [
      { label: "成片类型", value: contentTypeMap[contentType] || contentType || "未设置" },
      { label: "画面比例", value: aspect },
      { label: "目标时长", value: duration },
      { label: "视觉风格", value: style },
      { label: "文化背景", value: cultures.join("、") || "未设置" },
    ],
  };
}

function buildProjectDetailFields(identity: ProjectIdentity, stageLabel: string): Array<{ label: string; value: string }> {
  const chip = (label: string) => identity.chips.find((item) => item.label === label)?.value || "—";
  return [
    { label: "项目名称", value: identity.title },
    { label: "项目缩写", value: identity.abbreviation },
    { label: "当前阶段", value: stageLabel },
    { label: "成片类型", value: chip("成片类型") },
    { label: "画面比例", value: chip("画面比例") },
    { label: "目标时长", value: chip("目标时长") },
    { label: "文化背景", value: chip("文化背景") },
    { label: "类型", value: identity.genre },
    { label: "视觉风格", value: chip("视觉风格") },
  ];
}

function runLineagePayload(run: AgentRun): Record<string, unknown> {
  const input = run.input || {};
  return {
    ...input,
    ...(run.episode_id ? { episode_id: run.episode_id } : {}),
    ...(run.episode_code ? { episode_code: run.episode_code } : {}),
    ...(run.script_version_id ? { script_version_id: run.script_version_id } : {}),
  };
}

function episodeLineageMatches(payload: unknown, episode: ProjectEpisode): boolean {
  return matchesLineagePayload(payload, {
    episodeId: episode.id,
    episodeCode: episode.episode_code,
    scriptVersionId: episode.current_version_id || undefined,
  });
}


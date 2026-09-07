import { Pencil, Search, Trash2, Users } from "lucide-react";
import React from "react";
import { ProjectMembersModal } from "./ProjectMembersModal";
import { EditInitialDraft, ProjectSettingsModal } from "./ProjectSettingsModal";
import { buildSharedProjectCards, type SharedProjectCard } from "./sharedProjectCatalog";
import type {
  ProductionContentType,
  Project,
  ProjectImportPayload,
  ProjectProductionBrief,
  Task,
  User,
} from "../../types";

/**
 * 项目管理页面（UI 移植自 legacy-ui/src/features/projects/ProjectOverviewPage.tsx 的主列表区）。
 *
 * 与 legacy-ui 的差异（基于 cine-forge 现有接口）：
 * 1. 列表数据来源：真实接口 GET /projects（经 buildSharedProjectCards 转卡片视图），不再混入 FOUNDATION_PROJECTS 演示项目；
 * 2. 删除：真实接口 POST /projects/:id/soft-delete（softDeleteProject）；
 * 3. 新建项目：复用已迁移的 ProjectSettingsModal，提交走 importProjectFromFile 真实接口；
 * 4. 成员管理：后端无接口，弹框用 localStorage 临时持久化（ProjectMembersModal 内 TODO 标注）；
 * 5. 编辑项目：复用 ProjectSettingsModal（mode="edit"），走 PATCH /projects/:id（仅改字段）
 *    或 POST /projects/import（重新上传剧本时用，复用导入接口 project_id 场景更新）；
 *    updateProjectSettings 封装了两条链路。
 *
 * 对应后端需求文档（legacy 需求文档仓库 docs/需求文档/项目管理-后端接口需求文档.md）
 */

type ProjectManagementPageProps = {
  projects: Project[];
  tasks: Task[];
  users: User[];
  /** 选中项目 → 跳转项目详情 */
  onSelectProject: (projectId: string, from?: string) => void;
  /** 软删除项目（真实接口） */
  onDeleteProject: (project: Project, adminPassword: string) => Promise<void>;
  /** 创建项目提交：组装 payload + 真实剧本文件 + 是否导入后进入剧本阅读 */
  onSubmitCreate: (payload: ProjectImportPayload, file: File, runAfterImport: boolean) => Promise<Project | null>;
  /** 编辑项目提交：组装 payload + 可选新剧本/封面文件，返回更新后的项目 */
  onSubmitEdit: (payload: EditInitialDraft & {
    newScriptFile: File | null;
    newCoverFile: File | null;
    production_brief: ProjectProductionBrief | null;
  }) => Promise<Project | null>;
};

export function ProjectManagementPage({
  projects,
  tasks,
  users,
  onSelectProject,
  onDeleteProject,
  onSubmitCreate,
  onSubmitEdit,
}: ProjectManagementPageProps) {
  const [query, setQuery] = React.useState("");
  const [createOpen, setCreateOpen] = React.useState(false);
  const [membersFor, setMembersFor] = React.useState<SharedProjectCard | null>(null);
  const [editingFor, setEditingFor] = React.useState<{
    card: SharedProjectCard;
    initial: EditInitialDraft;
  } | null>(null);

  // 真实项目 → 卡片视图（含封面兜底 / 进度推导）
  const projectCards = React.useMemo(
    () => buildSharedProjectCards(projects, tasks),
    [projects, tasks],
  );

  const visibleCards = React.useMemo(() => {
    const keyword = query.trim().toLowerCase();
    if (!keyword) return projectCards;
    return projectCards.filter((item) => item.title.toLowerCase().includes(keyword));
  }, [projectCards, query]);

  function openCard(card: SharedProjectCard) {
    onSelectProject(card.id, "projectManagement");
  }

  function buildEditInitial(card: SharedProjectCard): EditInitialDraft | null {
    const liveProject = projects.find((item) => item.id === card.id);
    if (!liveProject) return null;
    const brief = liveProject.production_brief as unknown as ProjectProductionBrief | null;
    if (!brief) {
      // 极少数旧项目没有 production_brief，这些项目无法走新版弹框编辑。
      window.alert("该项目创建于旧版本，缺少创作设置，暂不支持通过此弹框编辑。");
      return null;
    }
    const contentType = (brief.content_type as ProductionContentType) || "";
    const aspectRatio = (brief.delivery_aspect_ratio as "9:16" | "16:9") || "";
    const cultureCodes = Array.isArray(brief.cultural_contexts)
      ? brief.cultural_contexts.map((c) => (c as { context_code?: string })?.context_code || "").filter(Boolean)
      : [];
    // TODO(项目管理-编辑): 目标时长应从首集 EpisodeProductionBrief 读取，GET /projects 未内嵌分集信息。
    //  当前兜底 2 分钟，用户可根据实际需要在弹框中重新调整；保存后若重新上传剧本会写入该值，否则只改基础字段不碰时长。
    const DEFAULT_TOTAL_SECONDS = 120;
    const scriptLabel = liveProject.latest_import
      ? `剧本（v${liveProject.latest_import.script_version_no} 已导入）`
      : "剧本（已导入）";
    // coverPreview: 后端暂未返回 cover_image，编辑时留空让用户重新上传即可（本地预览用，不提交）。
    const coverPreview = liveProject
      ? (liveProject as unknown as { cover_image?: string }).cover_image
      : undefined;
    return {
      projectId: liveProject.id,
      project: liveProject,
      title: liveProject.title || "",
      abbreviation: liveProject.project_prefix || "",
      contentType,
      aspectRatio,
      styleId: brief.primary_style_id || "",
      cultureCodes,
      genre: liveProject.genre || card.genre || "",
      targetMinutes: Math.floor(DEFAULT_TOTAL_SECONDS / 60),
      targetSeconds: DEFAULT_TOTAL_SECONDS % 60,
      coverPreview,
      scriptFileName: scriptLabel,
    };
  }

  function openEditSettings(card: SharedProjectCard) {
    const initial = buildEditInitial(card);
    if (!initial) return;
    setEditingFor({ card, initial });
  }

  function handleDelete(card: SharedProjectCard) {
    const liveProject = projects.find((item) => item.id === card.id);
    if (!liveProject) {
      window.alert("该项目为演示数据，无法在当前接口下删除。");
      return;
    }
    const password = window.prompt("请输入管理员密码确认软删除项目");
    if (password !== null) void onDeleteProject(liveProject, password);
  }

  return (
    <div className="admin-projects-page">
      <div className="admin-projects-head">
        <h2 className="admin-projects-title">所有项目</h2>
        <div className="admin-projects-tools">
          <label className="admin-projects-search">
            <Search size={15} strokeWidth={1.8} aria-hidden="true" />
            <input
              type="search"
              value={query}
              placeholder="搜索项目"
              onChange={(event) => setQuery(event.target.value)}
            />
          </label>
        </div>
      </div>

      <section className="admin-project-grid" aria-label="项目列表">
        <button type="button" className="admin-project-tile admin-project-tile--new" onClick={() => setCreateOpen(true)}>
          <img className="admin-project-tile__new-art" src="/icons/new-project-card.png" alt="" draggable={false} />
          <span className="admin-project-tile__create-label">新建项目</span>
        </button>

        {visibleCards.map((card) => {
          const hasCover = Boolean(card.coverImage) && card.coverImage !== "/cineforge-mark.png";
          return (
            <article
              key={card.id}
              className={`admin-project-tile ${hasCover ? "has-cover" : "is-plain"}`}
              style={hasCover ? { backgroundImage: `url(${card.coverImage})` } : undefined}
              onClick={() => openCard(card)}
            >
              {hasCover ? <div className="admin-project-tile__shade" /> : null}
              {!hasCover ? (
                <img className="admin-project-tile__mark" src="/cineforge-mark.png" alt="" draggable={false} />
              ) : null}

              <div className="admin-project-tile__meta">
                <h3>{card.title}</h3>
                <p>剧集数：{card.episodes}</p>
              </div>

              <div className="admin-project-tile__actions" onClick={(event) => event.stopPropagation()}>
                <button type="button" onClick={() => setMembersFor(card)}>
                  <Users size={13} strokeWidth={1.8} />
                  成员管理
                </button>
                <i />
                <button type="button" onClick={() => openEditSettings(card)}>
                  <Pencil size={13} strokeWidth={1.8} />
                  编辑
                </button>
                <i />
                <button type="button" onClick={() => handleDelete(card)}>
                  <Trash2 size={13} strokeWidth={1.8} />
                  删除
                </button>
              </div>
            </article>
          );
        })}
      </section>

      {!visibleCards.length ? <div className="admin-empty">没有匹配的项目</div> : null}

      {membersFor ? (
        <ProjectMembersModal
          projectId={membersFor.id}
          projectTitle={membersFor.title}
          users={users}
          onClose={() => setMembersFor(null)}
        />
      ) : null}

      {createOpen ? (
        <ProjectSettingsModal
          users={users}
          onClose={() => setCreateOpen(false)}
          onSubmit={onSubmitCreate}
        />
      ) : null}

      {editingFor ? (
        <ProjectSettingsModal
          users={users}
          mode="edit"
          initial={editingFor.initial}
          onClose={() => setEditingFor(null)}
          onSubmit={onSubmitCreate}
          onSubmitEdit={onSubmitEdit}
        />
      ) : null}
    </div>
  );
}

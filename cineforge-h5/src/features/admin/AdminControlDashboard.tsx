import { Plus } from "lucide-react";
import React, { useMemo, useState } from "react";
import type { AdminOverview, AdminRecentReading, Project, ProjectImportPayload, Task, User } from "../../types";
import { userDisplayName, userInitial } from "../../utils";
import { relativeTimeValue, reviewAssetKind, type ReviewAssetKind } from "../review/shared";
import { buildSharedProjectCards, type SharedProjectCard } from "../projects/sharedProjectCatalog";
import { listDemoTeamUsers } from "./demoTeamMembers";
import { ProjectSettingsModal } from "../projects/ProjectSettingsModal";

type AdminControlDashboardProps = {
  projects: Project[];
  tasks: Task[];
  users?: User[];
  admin?: AdminOverview | null;
  onSelectProject: (projectId: string) => void;
  onOpenAssets: (projectId: string) => void;
  onOpenTasks: () => void;
  onOpenReviewCenter: () => void;
  onCreateProject?: () => void;
  /** 新版弹框创建项目：组装 payload + 真实剧本文件，调 importProjectFromFile。 */
  onSubmitCreateFromModal?: (
    payload: ProjectImportPayload,
    file: File,
    runAfterImport: boolean,
  ) => Promise<Project | null>;
};

type TeamMember = {
  name: string;
  role: string;
  avatar: string;
  initial: string;
};

type DemoProjectCard = {
  id: string;
  title: string;
  genre: string;
  status: string;
  statusTone: "blue" | "muted" | "done";
  progress: number;
  episodes: string;
  coverImage: string;
  team: TeamMember[];
  isDemo?: boolean;
};

function teamMembersFromUsers(users: User[]): TeamMember[] {
  // TODO(总控看板-后端接口需求文档-1 需求5): 当前只能拿到全局 users 前 4 个，
  // 导致「所有项目卡片显示同一批团队成员头像」，语义错误。
  // 后端需新增 GET /projects/:id/members（或在 /projects 返回中嵌入 members 字段，最多 4 个），
  // 前端改为按项目维度拉取成员后再渲染卡片团队区。
  const source = users.slice(0, 4);
  if (source.length === 0) {
    // 没有真实用户时，用 demo 数据兜底；avatar 为空时回退姓名首字。
    return listDemoTeamUsers().slice(0, 4).map((user) => ({
      name: userDisplayName(user),
      role: String(user.role || ""),
      avatar: String(user.avatar_url || ""),
      initial: userInitial(user),
    }));
  }
  return source.map((user) => ({
    name: userDisplayName(user),
    role: String(user.role || ""),
    avatar: String(user.avatar_url || ""),
    initial: userInitial(user),
  }));
}

function sharedToDashboardCard(project: SharedProjectCard, team: TeamMember[]): DemoProjectCard {
  return {
    id: project.id,
    title: project.title,
    genre: project.genre,
    status: project.status,
    statusTone: project.statusTone,
    progress: project.progress,
    episodes: project.episodeLabel,
    coverImage: project.coverImage,
    team,
    isDemo: project.isDemo,
  };
}

/**
 * 剧本围读记录：使用后端 /admin/overview 返回的 recent_readings。
 * 数据源：agent_runs 表中 agent_type='script_reading' 的最近 3 条记录。
 * 无数据时展示「暂无围读记录」空状态。
 */
function getRecentReadings(admin: AdminOverview | null | undefined): AdminRecentReading[] {
  return admin?.recent_readings ?? [];
}

/**
 * 构建待处理事项列表。
 * 仅使用前端 tasks/projects 真实数据聚合，不使用任何静态/兜底数据。
 * 当无数据时返回空数组，由渲染层展示空状态。
 *
 * 分镜审批：reviewAssetKind 为 storyboard/keyframe + status 为 submitting/reviewing/in_review
 * 资产确认：reviewAssetKind 为 character/scene/prop + status 为非终态
 */
export type TodoItem = {
  title: string;
  meta: string;
  tone: "blue" | "muted";
  taskId?: string;
};

// 终态：已完成/已通过/已拒绝/已返工
const TERMINAL_STATUSES = new Set(["completed", "done", "approved", "rejected", "rework"]);
// 待审核状态
const PENDING_REVIEW_STATUSES = new Set(["submitted", "reviewing", "in_review"]);

function isStoryboardKind(kind: ReviewAssetKind): boolean {
  return kind === "storyboard" || kind === "keyframe";
}

function isAssetKind(kind: ReviewAssetKind): boolean {
  return kind === "character" || kind === "scene" || kind === "prop";
}

function normalizeStatusKey(raw: unknown): string {
  return String(raw || "").toLowerCase().trim();
}

export function buildTodoItems(tasks: Task[], projects: Project[]): TodoItem[] {
  const projectMap = new Map<string, string>();
  projects.forEach((p) => {
    projectMap.set(String(p.id), String(p.title || p.name || "未命名项目"));
  });

  // 分镜审批：待审核的分镜/关键帧任务
  const storyboardReviewTasks = tasks.filter((t) => {
    const kind = reviewAssetKind(t);
    if (!isStoryboardKind(kind)) return false;
    const status = normalizeStatusKey(t.status);
    return PENDING_REVIEW_STATUSES.has(status);
  });

  // 资产确认：待处理的资产类任务（人物/场景/道具），非终态
  const assetConfirmTasks = tasks.filter((t) => {
    const kind = reviewAssetKind(t);
    if (!isAssetKind(kind)) return false;
    const status = normalizeStatusKey(t.status);
    return !TERMINAL_STATUSES.has(status);
  });

  // 按更新时间降序排列
  const storyboardSorted = [...storyboardReviewTasks].sort((a, b) => {
    const ta = a.updated_at || a.created_at || "";
    const tb = b.updated_at || b.created_at || "";
    return new Date(tb).getTime() - new Date(ta).getTime();
  });

  const assetSorted = [...assetConfirmTasks].sort((a, b) => {
    const ta = a.updated_at || a.created_at || "";
    const tb = b.updated_at || b.created_at || "";
    return new Date(tb).getTime() - new Date(ta).getTime();
  });

  // 合并后按时间降序，取最新 4 条
  const merged = [...storyboardSorted.slice(0, 4), ...assetSorted.slice(0, 4)];
  merged.sort((a, b) => {
    const ta = a.updated_at || a.created_at || "";
    const tb = b.updated_at || b.created_at || "";
    return new Date(tb).getTime() - new Date(ta).getTime();
  });
  return merged.slice(0, 4).map((task) => {
    const kind = reviewAssetKind(task);
    const isSb = isStoryboardKind(kind);
    const projectTitle = projectMap.get(String(task.project_id)) || task.project_title || "未知项目";
    const time = relativeTimeValue(task.updated_at || task.created_at);
    const title = isSb
      ? (task.title || task.storyboard_code || "分镜任务")
      : (task.title || task.scene_name || "资产任务");
    return {
      title,
      meta: `${projectTitle} · ${time}`,
      tone: isSb ? "blue" : "muted",
      taskId: task.id,
    };
  });
}

export function countTodoItems(tasks: Task[]): number {
  let count = 0;
  for (const t of tasks) {
    const kind = reviewAssetKind(t);
    const status = normalizeStatusKey(t.status);
    if (isStoryboardKind(kind) && PENDING_REVIEW_STATUSES.has(status)) {
      count += 1;
    }
    if (isAssetKind(kind) && !TERMINAL_STATUSES.has(status)) {
      count += 1;
    }
  }
  return count;
}

export function AdminControlDashboard({
  projects,
  tasks,
  users,
  admin,
  onSelectProject,
  onOpenAssets,
  onOpenTasks,
  onOpenReviewCenter,
  onCreateProject: _onCreateProject,
  onSubmitCreateFromModal,
}: AdminControlDashboardProps) {
  // _onCreateProject：父组件 main.tsx 仍下传，但总控看板页面已移除"使用旧版"入口，保留以避免类型不一致。
  const [createModalOpen, setCreateModalOpen] = useState(false);
  const teamUsers = React.useMemo(
    () => (users && users.length ? users : listDemoTeamUsers()),
    [users],
  );
  const projectTeam = React.useMemo(() => teamMembersFromUsers(teamUsers), [teamUsers]);
  const cards = React.useMemo(
    () => buildSharedProjectCards(projects, tasks).map((project) => sharedToDashboardCard(project, projectTeam)),
    [projects, tasks, projectTeam],
  );

  // KPI 计算：优先用接口数据，缺失字段用死数据
  // TODO(总控看板-后端接口需求文档-1 需求1): 以下 4 个 KPI 当前全部走兜底逻辑，展示的不是真实数据。
  // 后端需在 /admin/overview 返回 active_project_count / monthly_completed_episodes /
  // pending_items_count / team_creator_count 四个字段，前端去掉兜底后直接读取。
  //   - active_project_count: 当前用 projects.length 兜底（含已归档/已删除，口径不一致）
  //   - monthly_completed_episodes: 当前硬编码为 0（后端完全未提供）
  //   - pending_items_count: 当前用 overdue_task_count 兜底，再缺失时硬编码 8
  //   - team_creator_count: 当前用 user_count 兜底，再缺失时用 teamUsers.length（含管理员，口径不一致）
  const activeProjectCount = admin?.active_project_count ?? projects.length;

  // 本月完成集数：Phase 1 后端暂未提供，先用 0
  const monthlyEpisodeCount = 0;

  // 待处理事项：仅聚合真实的分镜审批 + 资产确认待办
  const todoItems = useMemo(() => buildTodoItems(tasks, projects), [tasks, projects]);
  const pendingTodoCount = useMemo(() => countTodoItems(tasks), [tasks]);

  // 团队创作者
  const creatorCount = admin?.user_count ?? teamUsers.length;

  const kpiItems: Array<{ key: string; value: string; label: string; alert?: boolean }> = [
    { key: "active", value: String(activeProjectCount), label: "进行中项目" },
    { key: "episodes", value: String(monthlyEpisodeCount), label: "本月完成集数" },
    { key: "todos", value: String(pendingTodoCount), label: "待处理事项", alert: pendingTodoCount > 0 },
    { key: "creators", value: String(creatorCount), label: "团队创作者" },
  ];

  function openProject(project: DemoProjectCard) {
    onSelectProject(project.id);
  }

  function openAssets(project: DemoProjectCard, event: React.MouseEvent) {
    event.stopPropagation();
    onOpenAssets(project.id);
  }

  function handleCreateProjectClick() {
    // 打开创建项目弹框（UI 移植自 legacy-ui，对接 cine-forge 真实 catalog + importProjectFromFile）
    setCreateModalOpen(true);
  }

  return (
    <div className="admin-dash">
      <section className="admin-kpi-row" aria-label="关键指标">
        {kpiItems.map((item) => {
          const isPendingCard = item.key === "todos";
          return (
            <div
              key={item.key}
              className={`admin-kpi${isPendingCard ? " is-clickable" : ""}`}
              role={isPendingCard ? "button" : undefined}
              tabIndex={isPendingCard ? 0 : undefined}
              onClick={isPendingCard ? onOpenReviewCenter : undefined}
              onKeyDown={(event) => {
                if (isPendingCard && (event.key === "Enter" || event.key === " ")) {
                  event.preventDefault();
                  onOpenReviewCenter();
                }
              }}
            >
              <div className="admin-kpi__top">
                <span>{item.label}</span>
                {item.alert ? <i className="admin-kpi__dot" /> : null}
              </div>
              <strong>{item.value}</strong>
            </div>
          );
        })}
      </section>

      <div className="admin-dash-grid">
        <section className="admin-projects">
          <div className="admin-section-head">
            <h2>全部项目</h2>
            <div className="admin-section-actions">
              <button type="button" className="admin-btn-solid" onClick={handleCreateProjectClick}>
                <Plus size={14} strokeWidth={2.5} />
                新建项目
              </button>
            </div>
          </div>

          <div className="admin-project-list">
            {cards.length === 0 ? (
              <div className="admin-panel" style={{ textAlign: "center", color: "var(--admin-muted)" }}>
                暂无项目，点击右上角「新建项目」开始创建。
              </div>
            ) : null}
            {cards.map((project) => (
              <article
                className="admin-project-card is-clickable"
                key={project.id}
                role="link"
                tabIndex={0}
                onClick={() => openProject(project)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    openProject(project);
                  }
                }}
              >
                <img
                  className="admin-project-card__cover"
                  src={project.coverImage}
                  alt={project.title}
                  width={96}
                  height={128}
                  onError={(e) => {
                    const target = e.currentTarget;
                    if (target.src !== "/cineforge-mark.png") target.src = "/cineforge-mark.png";
                  }}
                />
                <div className="admin-project-card__body">
                  <div className="admin-project-card__main">
                    <div className="admin-project-card__title-row">
                      <h3>{project.title}</h3>
                      <span className="admin-genre">{project.genre}</span>
                      <span className={`admin-tag admin-tag--${project.statusTone}`}>{project.status}</span>
                    </div>
                    <div className="admin-progress-row">
                      <div className={`admin-progress ${project.statusTone === "done" ? "is-done" : ""}`}>
                        <span style={{ width: `${project.progress}%` }} />
                      </div>
                      <em className={project.statusTone === "done" ? "is-done" : ""}>{project.progress}%</em>
                    </div>
                    <div className="admin-project-card__meta">
                      <strong className="admin-project-card__episodes">{project.episodes}</strong>
                      <div className="admin-role-avatars" aria-label="项目团队成员">
                        {project.team.map((member) => (
                          <span
                            key={`${project.id}-${member.name}`}
                            className="admin-role-avatars__item"
                            title={member.name}
                          >
                            {member.avatar ? (
                              <img
                                className="admin-role-avatars__img"
                                src={member.avatar}
                                alt={member.name}
                                onError={(e) => {
                                  e.currentTarget.style.display = "none";
                                }}
                              />
                            ) : (
                              <span className="admin-role-avatars__fallback">{member.initial}</span>
                            )}
                          </span>
                        ))}
                      </div>
                    </div>
                    <div className="admin-project-card__actions">
                      <button type="button" className="admin-text-link" onClick={(event) => openAssets(project, event)}>
                        资产管理
                      </button>
                    </div>
                  </div>
                </div>
                <span className="admin-project-card__detail-icon" aria-hidden="true">
                  <span className="admin-project-card__detail-icon-glyph" />
                </span>
              </article>
            ))}
          </div>

        </section>

        <aside className="admin-side-stack">
          <section className="admin-panel admin-panel--split">
            <h2>剧本围读记录</h2>
            <div className="admin-recent-block">
              <div className="admin-recent-label">最近围读</div>
              <ul className="admin-record-list">
                {(() => {
                  const readings = getRecentReadings(admin);
                  if (!readings.length) {
                    return <li className="admin-record-list__empty">暂无围读记录</li>;
                  }
                  return readings.map((item) => (
                    <li key={item.id}>
                      <div className="admin-record-list__main">
                        <strong>{item.name}</strong>
                        <span>{item.meta}</span>
                      </div>
                      <button
                        type="button"
                        className="admin-link"
                        onClick={() => {
                          if (item.projectId) onSelectProject(item.projectId);
                        }}
                      >
                        查看
                      </button>
                    </li>
                  ));
                })()}
              </ul>
            </div>
          </section>

          <section className="admin-panel admin-panel--todo">
            <div className="admin-section-head admin-section-head--compact">
              <h2>待处理</h2>
              {todoItems.length > 0 ? <span className="admin-badge">{todoItems.length}</span> : null}
            </div>
            {todoItems.length > 0 ? (
              <ul className="admin-todo-list">
                {todoItems.map((item) => (
                  <li key={`${item.taskId || item.title}`}>
                    <i className={`admin-todo-dot admin-todo-dot--${item.tone}`} />
                    <button type="button" onClick={onOpenReviewCenter}>
                      <strong>{item.title}</strong>
                      <span>{item.meta}</span>
                    </button>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="admin-todo-list-empty">
                <span>暂无待处理任务</span>
              </div>
            )}
          </section>
        </aside>
      </div>

      {createModalOpen && onSubmitCreateFromModal ? (
        <ProjectSettingsModal
          users={teamUsers}
          onClose={() => setCreateModalOpen(false)}
          onSubmit={onSubmitCreateFromModal}
        />
      ) : null}
    </div>
  );
}

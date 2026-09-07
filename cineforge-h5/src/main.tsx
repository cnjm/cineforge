import React from "react";
import { createRoot } from "react-dom/client";
import { LoaderCircle } from "lucide-react";
import { AdminControlDashboard } from "./features/admin/AdminControlDashboard";
import { ProjectManagementPage } from "./features/projects/ProjectManagementPage";
import { useCineForgeApp } from "./hooks/useCineForgeApp";
import { AdminRail } from "./layout/AdminRail";
import { TopLineSwitcher } from "./layout/TopLineSwitcher";
import { homePageForLine, lineForPage, type ProductLine } from "./layout/appLayout";
import { LoginPage } from "./layout/LoginPage";
import { NotificationModal, getNotificationBadgeCount } from "./shared/NotificationModal";
import { isOverdueTask } from "./utils";
import { canAccessReviewCenter as userCanAccessReviewCenter } from "./taskAccess";
import { CanvasPage } from "./pages/CanvasPage";
import "./design-tokens.css";
import "typeface-d-din";
import "./styles.css";

/** 画布项目嵌入地址（由 vite.config.ts 中 define.__CANVAS_URL__ 注入） */
declare const __CANVAS_URL__: string;

const AgentAdminPage = React.lazy(() => import("./features/admin/AgentAdminPage").then((module) => ({ default: module.AgentAdminPage })));
const TeamMembersPage = React.lazy(() => import("./features/teamMembers/TeamMembersPage").then((module) => ({ default: module.TeamMembersPage })));
const SystemLogsPage = React.lazy(() => import("./features/admin/SystemLogsPage").then((module) => ({ default: module.SystemLogsPage })));
const AssetsPage = React.lazy(() => import("./features/assets/AssetsPage").then((module) => ({ default: module.AssetsPage })));
const NewProjectDetailPage = React.lazy(() => import("./features/projects/NewProjectDetailPage").then((module) => ({ default: module.NewProjectDetailPage })));
const QueryAgentPage = React.lazy(() => import("./features/query/QueryAgentPage").then((module) => ({ default: module.QueryAgentPage })));
const MyTasksPage = React.lazy(() => import("./features/tasks/MyTasksPage").then((module) => ({ default: module.MyTasksPage })));
const ProjectOverviewPage = React.lazy(() => import("./features/projects/ProjectOverviewPage").then((module) => ({ default: module.ProjectOverviewPage })));
const CreationCenterPage = React.lazy(() => import("./features/create/CreationCenterPage").then((module) => ({ default: module.CreationCenterPage })));
const ReviewCenterDashboard = React.lazy(() => import("./features/review/ReviewCenterDashboard").then((module) => ({ default: module.ReviewCenterDashboard })));
const ReviewWorkbenchPage = React.lazy(() => import("./features/review/ReviewWorkbenchPage").then((module) => ({ default: module.ReviewWorkbenchPage })));
const AssetLibraryPage = React.lazy(() => import("./features/assetLibrary/AssetLibraryPage").then((module) => ({ default: module.AssetsPage })));
const GroupCanvasPage = React.lazy(() => import("./pages/GroupCanvasPage").then((module) => ({ default: module.GroupCanvasPage })));
import "./styles/creation-center.css";
import "./features/review/reviewCenter.css";
import "./styles/asset-library-v2.css";
import "./styles/team-members.css";

function ToastNotice({ message, onDone }: { message: string; onDone: () => void }) {
  const [leaving, setLeaving] = React.useState(false);
  React.useEffect(() => {
    setLeaving(false);
    const fadeTimer = window.setTimeout(() => setLeaving(true), 2200);
    const doneTimer = window.setTimeout(onDone, 2700);
    return () => {
      window.clearTimeout(fadeTimer);
      window.clearTimeout(doneTimer);
    };
  }, [message, onDone]);
  return (
    <div className={`toast-notice${leaving ? " leaving" : ""}`} role="status" aria-live="polite">
      <div className="toast-notice-card">{message}</div>
    </div>
  );
}

class AppErrorBoundary extends React.Component<{ children: React.ReactNode }, { error: string | null }> {
  constructor(props: { children: React.ReactNode }) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error: unknown) {
    return { error: error instanceof Error ? error.message : String(error) };
  }

  render() {
    if (this.state.error) {
      return (
        <main className="login-shell">
          <div className="login-backgrounds" aria-hidden="true">
            <img className="login-background is-active" src="/login-backgrounds/login-bg-01.jpg" alt="" />
          </div>
          <div className="login-page-brand" aria-label="CineForge">
            <img src="/cineforge-logo.png" alt="CineForge" />
          </div>
          <section className="login-card login-card--focused" role="dialog" aria-modal="true">
            <form className="login-form" onSubmit={(e) => { e.preventDefault(); window.location.reload(); }}>
              <div className="login-form__heading">
                <h1>前端运行异常</h1>
                <p>{this.state.error}</p>
              </div>
              <button className="login-submit login-submit--white" type="submit">
                重新加载
              </button>
            </form>
          </section>
        </main>
      );
    }
    return this.props.children;
  }
}

function App() {
  const app = useCineForgeApp();
  const [productLine, setProductLine] = React.useState<ProductLine>("drama");
  const [sidebarCollapsed, setSidebarCollapsed] = React.useState(() => {
    try {
      return localStorage.getItem("cineforge.sidebarCollapsed") === "1";
    } catch {
      return false;
    }
  });
  const [assetLibraryProjectId, setAssetLibraryProjectId] = React.useState<string | null>(null);
  // 主题模式：浅色 / 深色 / 跟随系统（对齐 legacy-ui AdminShell）
  const [themeMode, setThemeMode] = React.useState<"dark" | "light" | "system">(() => {
    try {
      const saved = localStorage.getItem("cineforge-admin-theme");
      if (saved === "light" || saved === "dark" || saved === "system") return saved;
    } catch {
      /* ignore */
    }
    return "dark";
  });
  const [systemTheme, setSystemTheme] = React.useState<"dark" | "light">(() =>
    typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light",
  );
  // 实际生效主题：system 模式下跟随系统偏好
  const theme = themeMode === "system" ? systemTheme : themeMode;

  React.useEffect(() => {
    try {
      localStorage.setItem("cineforge-admin-theme", themeMode);
    } catch {
      /* ignore */
    }
    // 将主题属性同步到 html 和 body，使全局滚动条等元素可以响应主题切换
    document.documentElement.setAttribute("data-theme", theme);
    document.body.setAttribute("data-theme", theme);
  }, [theme, themeMode]);

  // 监听系统主题变化，system 模式下实时同步
  React.useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    function syncSystemTheme() {
      setSystemTheme(media.matches ? "dark" : "light");
    }
    syncSystemTheme();
    media.addEventListener("change", syncSystemTheme);
    return () => media.removeEventListener("change", syncSystemTheme);
  }, []);

  function toggleSidebarCollapsed() {
    setSidebarCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem("cineforge.sidebarCollapsed", next ? "1" : "0");
      } catch {
        /* ignore */
      }
      return next;
    });
  }

  function go(target: PageKey) {
    // makerTrack/taskCanvas → groupCanvas 归一（画布分组设计 3.1/3.2）
    const page = target === "makerTrack" || target === "taskCanvas" ? "groupCanvas" : target;
    setProductLine(lineForPage(page));
    // 切换页面时重置资产库已选项目，避免上一次的项目详情残留
    if (target !== "assetLibrary") setAssetLibraryProjectId(null);
    app.setPage(page);
  }

  function changeLine(next: ProductLine) {
    if (next === productLine) return;
    setProductLine(next);
    app.setPage(homePageForLine(next, app.session?.user.role));
  }

  if (!app.session) {
    return <LoginPage loading={app.loading} error={app.error} onLogin={app.login} onClearError={app.clearLoginError} />;
  }

  const canAccessReviewCenter = userCanAccessReviewCenter(app.session.user);
  const reviewWorkspaceTasks = [
    ...(app.reviewWorkspace?.groups.todo || []),
    ...(app.reviewWorkspace?.groups.completed || []),
  ];
  const reviewCount = reviewWorkspaceTasks.length > 0
    ? reviewWorkspaceTasks.filter((task) => {
        const status = String(task.status || "");
        return ["submitted", "reviewing", "rejected"].includes(status);
      }).length
    : app.tasks.filter((task) => {
        const status = String(task.status || "");
        return ["submitted", "reviewing", "rejected"].includes(status);
      }).length;

  // 所有项目详情页统一使用新版
  const isNewProjectDetailPage = app.page === "projectDetail";
  const detailProject = app.projectEntryMode.type === "create_project"
    ? undefined
    : app.projects.find((project) => project.id === app.projectEntryMode.projectId);

  // 项目详情页的来源页（用于返回按钮和侧栏高亮）
  const detailFrom = (app.projectEntryMode as { from?: string } | undefined)?.from;
  const sidebarActivePage = app.page === "projectDetail" && detailFrom
    ? detailFrom
    : app.page;

  // 判断是否在资产库项目详情页（从资产库首页点进某个项目的资产列表）
  const isAssetLibraryDetail = app.page === "assetLibrary" && assetLibraryProjectId !== null;
  const assetLibraryProject = isAssetLibraryDetail
    ? app.projects.find((project) => project.id === assetLibraryProjectId)
    : undefined;

  // 画布工作台（canvas）保持 cine-forge 原逻辑：保留侧栏+顶栏，在 content 区域内嵌入 iframe
  // shellFullscreen 预留给 legacy-ui 体系的制作画布等全屏工作台（阶段三再启用）
  const shellFullscreen = false;

  return (
    <div
      className={[
        "admin-app",
        sidebarCollapsed ? "admin-app--collapsed" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      data-theme={theme}
    >
      <AdminRail
        page={app.page}
        setPage={go}
        collapsed={sidebarCollapsed}
        onToggleCollapsed={toggleSidebarCollapsed}
        sidebarActivePage={sidebarActivePage}
        onGhostNav={(item) => {
          console.info("[AdminRail] ghost nav clicked (not wired yet):", item.label);
        }}
      />
      <main className="admin-main">
        <TopLineSwitcher
          page={app.page}
          notificationCount={getNotificationBadgeCount(app.notifications, app.tasks.filter(isOverdueTask).length)}
          currentUser={app.session.user}
          onOpenNotifications={() => app.setNotificationsOpen(true)}
          onLogout={app.logout}
          onUpdateAvatar={app.updateAvatar}
          onChangePassword={app.changePassword}
          theme={theme}
          themeMode={themeMode}
          systemTheme={systemTheme}
          onSelectThemeMode={setThemeMode}
          customTitle={
            isNewProjectDetailPage ? (detailProject?.name ?? "项目详情")
            : isAssetLibraryDetail ? ((assetLibraryProject as { title?: string })?.title ?? "项目资产")
            : undefined
          }
          showBackButton={isNewProjectDetailPage || isAssetLibraryDetail}
          onBack={
            isNewProjectDetailPage ? () => app.setPage(detailFrom === "adminDashboard" ? "adminDashboard" : "projectManagement")
            : isAssetLibraryDetail ? () => setAssetLibraryProjectId(null)
            : undefined
          }
        />
        <section className={`admin-content${app.page === "canvas" ? " content--canvas" : ""}`}>
          {app.error ? <div className="notice error">{app.error}</div> : null}
          {app.loading ? (
            <div className="app-loading-overlay">
              <LoaderCircle className="app-loading-spinner" size={36} strokeWidth={2} />
              <span className="app-loading-text">数据加载中...</span>
            </div>
          ) : null}
          {app.notice ? <ToastNotice message={app.notice} onDone={() => app.setNotice(null)} /> : null}
          <React.Suspense fallback={
            <div className="app-loading-overlay">
              <LoaderCircle className="app-loading-spinner" size={36} strokeWidth={2} />
              <span className="app-loading-text">模块加载中...</span>
            </div>
          }>
          {app.page === "projectManagement" && (
            <ProjectManagementPage
              projects={app.projects}
              tasks={app.tasks}
              users={app.users}
              onSelectProject={app.selectProject}
              onDeleteProject={app.softDeleteProject}
              onSubmitCreate={async (payload, file, runAfterImport) => {
                const created = await app.importProjectFromFile(payload, file);
                if (created && runAfterImport) {
                  const imp = created.latest_import;
                  await app.runBreakdownStep(
                    "reading",
                    created.id,
                    imp?.episode_id,
                    imp?.script_version_id,
                  );
                }
                if (created) {
                  app.selectProject(created.id, "projectManagement");
                }
                return created;
              }}
              onSubmitEdit={async (payload) => {
                if (!payload.production_brief) {
                  throw new Error("缺少创作设置，无法保存。");
                }
                const targetSeconds = payload.targetMinutes * 60 + payload.targetSeconds;
                return app.updateProjectSettings({
                  projectId: payload.projectId,
                  title: payload.title,
                  abbreviation: payload.abbreviation,
                  genre: payload.genre,
                  production_brief: payload.production_brief,
                  targetSeconds,
                  newScriptFile: payload.newScriptFile,
                });
              }}
            />
          )}
          {app.page === "projectOverview" && (
            <ProjectOverviewPage
              projects={app.projects}
              tasks={app.tasks}
              onDeleteProject={app.softDeleteProject}
              onCreateProject={app.startNewProject}
              onSelectProject={app.selectProject}
            />
          )}
          {app.page === "adminDashboard" && (
            <AdminControlDashboard
              projects={app.projects}
              tasks={app.tasks}
              users={app.users}
              admin={app.admin}
              onSelectProject={(projectId) => {
                if (projectId) {
                  app.selectProject(projectId, "adminDashboard", "revision");
                } else {
                  app.startNewProject();
                }
              }}
              onOpenAssets={(projectId) => {
                if (projectId) app.selectProject(projectId);
                app.setPage("assets");
              }}
              onOpenTasks={() => app.setPage("myTasks")}
              onOpenReviewCenter={() => app.setPage("reviewCenter")}
              onCreateProject={() => app.startNewProject()}
              onSubmitCreateFromModal={async (payload, file, runAfterImport) => {
                const created = await app.importProjectFromFile(payload, file);
                if (created && runAfterImport) {
                  const imp = created.latest_import;
                  await app.runBreakdownStep(
                    "reading",
                    created.id,
                    imp?.episode_id,
                    imp?.script_version_id,
                  );
                }
                if (created) {
                  app.selectProject(created.id, "adminDashboard");
                }
                return created;
              }}
            />
          )}
          {app.page === "projectDetail" ? (
            <NewProjectDetailPage
              project={detailProject}
              entryMode={app.projectEntryMode}
              dashboard={app.dashboard}
              jump={app.setPage}
              users={app.users}
              runs={app.runs}
              agentStageMap={app.agentStageMap}
              scriptSegments={app.scriptSegments}
              storyboards={app.storyboards}
              tasks={app.tasks}
              assets={app.assets}
              assetVariantPlans={app.assetVariantPlans}
              draftOverrides={app.draftOverrides}
              breakdownDraftSyncing={app.breakdownDraftSyncing}
              loading={app.loading}
              currentUser={app.session.user}
              onRunBreakdownStep={app.runBreakdownStep}
              onRetryPromptDistribution={app.retryPromptTaskDistribution}
              onCreateTemporaryAssetProduction={app.createTemporaryAssetProduction}
              onCreateProject={app.createProject}
              onUpdateProject={app.updateProject}
              onImportProject={app.importProjectFromFile}
              onRunProjectAgent={app.runProjectAgent}
              onMaterializeAgentRun={app.materializeAgentRun}
              onAssignTask={app.assignTask}
              onBulkAssign={app.bulkAssignTasks}
              onGenerateStoryboardTasks={app.generateStoryboardTasks}
              onCreateAssetVariantPlan={app.createAssetVariantPlan}
              onUpdateAssetVariantPlan={app.updateAssetVariantPlan}
              onFetchSceneGating={app.fetchSceneGating}
              onLoadProjectData={app.refreshProjectData}
              onLoadBreakdownDraft={app.loadBreakdownDraft}
              onSaveBreakdownDraft={app.saveBreakdownDraft}
              onConfirmScriptSegments={app.confirmScriptSegments}
              onLockEpisodeBreakdown={app.lockEpisodeBreakdown}
              onDeleteProject={app.softDeleteProject}
              onOpenProject={app.selectProject}
              onAddEpisode={app.startAddEpisode}
              onAddScriptVersion={app.startAddScriptVersion}
              onSubmitEdit={async (payload) => {
                if (!payload.production_brief) {
                  throw new Error("缺少创作设置，无法保存。");
                }
                const targetSeconds = payload.targetMinutes * 60 + payload.targetSeconds;
                return app.updateProjectSettings({
                  projectId: payload.projectId,
                  title: payload.title,
                  abbreviation: payload.abbreviation,
                  genre: payload.genre,
                  production_brief: payload.production_brief,
                  targetSeconds,
                  newScriptFile: payload.newScriptFile,
                });
              }}
            />
          ) : null}
          {app.page === "myTasks" && (
            <MyTasksPage
              assets={app.assets}
              projects={app.projects}
              workspace={app.workspace}
              tasks={app.tasks}
              loading={app.loading}
              onUploadCandidates={app.uploadTaskCandidates}
              onSubmitTask={app.submitTaskBatch}
              onSubmitCharacterUnit={app.submitCharacterTaskUnit}
              onDeleteDraftSubmission={app.deleteTaskDraftSubmission}
              onReviewTask={app.reviewTaskBatch}
              onReviewCharacterUnit={app.reviewCharacterTaskUnit}
              onArchiveSubmission={app.archiveTaskSubmission}
              onSetPrimarySubmission={app.setPrimaryTaskSubmission}
              onSavePrompt={app.saveTaskPrompt}
              onGeneratePrompt={app.generateTaskPrompt}
            />
          )}
          {app.page === "reviewCenter" && (
            <ReviewCenterDashboard
              workspace={app.reviewWorkspace}
              projects={app.projects}
              tasks={app.tasks}
              loading={app.loading}
              canReview={canAccessReviewCenter}
              onReviewTask={app.reviewTaskBatch}
              onSetPrimarySubmission={app.setPrimaryTaskSubmission}
            />
          )}
          {app.page === "reviewWorkbench" && canAccessReviewCenter && (
            <ReviewWorkbenchPage
              workspace={app.reviewWorkspace}
              projects={app.projects}
              tasks={app.tasks}
              currentUserName={app.session?.user?.display_name}
              loading={app.loading}
              onReviewTask={app.reviewTaskBatch}
              onOpenLegacyQueue={() => app.setPage("reviewCenter")}
              onExit={() => app.setPage("adminDashboard")}
            />
          )}
          {app.page === "assets" && <AssetsPage
            apiAssets={app.assets}
            projects={app.projects}
            storyboards={app.storyboards}
            tasks={app.tasks}
          />}
          {app.page === "assetLibrary" && (
            <AssetLibraryPage
              apiAssets={app.assets}
              projects={app.projects}
              storyboards={app.storyboards}
              tasks={app.tasks}
              activeProjectId={assetLibraryProjectId}
              onOpenProject={(projectId) => setAssetLibraryProjectId(projectId)}
              onBackToProjects={() => setAssetLibraryProjectId(null)}
            />
          )}
          {app.page === "agentAdmin" && (
            <AgentAdminPage
              runs={app.runs}
              hermesConfig={app.hermesConfig}
              hermesHealth={app.hermesHealth}
              loading={app.loading}
              onRefreshHermes={app.refreshHermesStatus}
            />
          )}
          {app.page === "teamMembers" && (
            <TeamMembersPage
              users={app.users}
              admin={app.admin}
              loading={app.loading}
              onCreateUser={app.createUser}
              onResetPassword={app.resetUserPassword}
              onToggleActive={app.setUserActive}
            />
          )}
          {app.page === "systemLogs" && <SystemLogsPage runs={app.runs} admin={app.admin} />}
          {app.page === "queryAgent" && <QueryAgentPage project={app.project} tasks={app.tasks} assets={app.assets} />}
          {app.page === "canvas" && <CanvasPage />}
          {app.page === "workbench" && <CanvasPage src={`${__CANVAS_URL__}/workbench`} />}
          {app.page === "creationCenter" && (
            <CreationCenterPage
              tasks={app.tasks}
              projects={app.projects}
              loading={app.loading}
              currentUserRole={app.session?.user?.role ?? null}
            />
          )}
          {app.page === "groupCanvas" && (
            <GroupCanvasPage
              canReview={canAccessReviewCenter}
              assets={app.assets}
              projects={app.projects}
              workspace={app.workspace}
              tasks={app.tasks}
              loading={app.loading}
              onUploadCandidates={app.uploadTaskCandidates}
              onSubmitTask={app.submitTaskBatch}
              onSubmitCharacterUnit={app.submitCharacterTaskUnit}
              onDeleteDraftSubmission={app.deleteTaskDraftSubmission}
              onReviewTask={app.reviewTaskBatch}
              onReviewCharacterUnit={app.reviewCharacterTaskUnit}
              onArchiveSubmission={app.archiveTaskSubmission}
              onSetPrimarySubmission={app.setPrimaryTaskSubmission}
              onSavePrompt={app.saveTaskPrompt}
              onGeneratePrompt={app.generateTaskPrompt}
            />
          )}
          </React.Suspense>
        </section>
      </main>
      {app.notificationsOpen ? <NotificationModal notifications={app.notifications} tasks={app.tasks} dashboard={app.dashboard} onClose={() => app.setNotificationsOpen(false)} /> : null}
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <AppErrorBoundary>
      <App />
    </AppErrorBoundary>
  </React.StrictMode>,
);

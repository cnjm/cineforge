import React from "react";
import { api, API_BASE, AuthExpiredError, getAuthToken, getPlatformToken, getSessionExpiresAt, getTapToken, isSessionExpired, normalizeUser, readStoredUser, setAuthSession } from "../api";
import { createClientUuid } from "../clientId";
import { breakdownPayload } from "../assetNormalization";
import type {
  AdminOverview,
  AgentMaterializeResponse,
  AgentRun,
  AgentStageMapItem,
  Asset,
  AssetVariantPlan,
  AuthSession,
  BreakdownDraftData,
  BreakdownRead,
  DashboardSummary,
  FileUploadResponse,
  HermesConfig,
  HermesHealth,
  LoginResponse,
  NewUserForm,
  PageKey,
  Project,
  ProjectCreatePayload,
  ProjectDetailModule,
  ProjectEntryMode,
  ProjectImportPayload,
  ProjectUpdatePayload,
  ScriptSegment,
  ScriptSegmentsConfirmResponse,
  SceneGating,
  Submission,
  SubmissionBatch,
  Storyboard,
  Task,
  TemporaryAssetProductionPayload,
  TemporaryAssetProductionResponse,
  User,
  UserNotification,
  WorkspaceResponse,
} from "../types";
import {
  addQuery,
  normalizeRunList,
  parseRunEventBlock,
  type RunLineageScope,
} from "../runData";
import {
  breakdownDraftProjectPrefix,
  breakdownDraftScopeKey,
  canManage,
  draftFromBreakdown,
  hasPendingAgentRun,
  isAgentPending,
  labelAgentType,
  taskMediaKind,
} from "../utils";

const RUN_LIST_PAGE_SIZE = 100;
const FORMAL_BREAKDOWN_AGENT_TYPES = new Set([
  "script_reading",
  "asset_extract",
  "asset_prompt_generation",
  "script_segmentation",
  "storyboard_breakdown",
  "script_breakdown",
]);

async function fetchAllRunPages<T>(path: string): Promise<T[]> {
  const items: T[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < 20; page += 1) {
    const response = await api.get<unknown>(addQuery(path, {
      limit: String(RUN_LIST_PAGE_SIZE),
      cursor,
    }));
    const normalized = normalizeRunList<T>(response);
    items.push(...normalized.items);
    if (!normalized.hasMore || !normalized.nextCursor) break;
    cursor = normalized.nextCursor;
  }
  return items;
}

type ProjectDataLoadOptions = RunLineageScope & {
  includePayload?: boolean;
  includeCollections?: boolean;
  includeUsers?: boolean;
};

type RefreshOptions = {
  silent?: boolean;
  includeProjectData?: boolean;
  includeWorkspace?: boolean;
  includePayload?: boolean;
  includeTasks?: boolean;
  includeAssets?: boolean;
  includeAdmin?: boolean;
  includeUsers?: boolean;
  /** Load task/asset collections only for the active project. */
  projectScopeId?: string | null;
};

function mergeAgentRunSummaries(current: AgentRun[], summaries: AgentRun[]) {
  const currentById = new Map(current.map((run) => [run.id, run]));
  return summaries.map((summary) => {
    const previous = currentById.get(summary.id);
    if (!previous) return summary;
    return {
      ...previous,
      ...summary,
      input: previous.input,
      output: previous.output,
      token_usage: previous.token_usage,
      feedback_json: previous.feedback_json,
    };
  });
}

function mergeAgentRunDetails(summaries: AgentRun[], details: AgentRun[]) {
  const detailsById = new Map(details.map((run) => [run.id, run]));
  return summaries.map((summary) => ({ ...summary, ...(detailsById.get(summary.id) || {}) }));
}

export function useCineForgeApp() {
  const [session, setSession] = React.useState<AuthSession | null>(() => {
    const user = readStoredUser();
    const token = getAuthToken();
    const tapcanvasToken = getTapToken();
    const platformToken = getPlatformToken();
    const sessionExpiresAt = getSessionExpiresAt();
    // 关键修复：初始化时必须验证 session 过期时间，隔一天直接访问时过期则不建立会话
    if (!token || !user || !tapcanvasToken || !platformToken || !sessionExpiresAt) return null;
    if (Date.now() >= sessionExpiresAt * 1000) {
      // 过期了，清理存储避免再次读取
      setAuthSession(null);
      return null;
    }
    return { token, user, tapcanvas_token: tapcanvasToken, platform_token: platformToken, session_expires_at: sessionExpiresAt };
  });

  // 全局安全网：捕获任何未处理的 AuthExpiredError，确保跳转到登录页
  React.useEffect(() => {
    function handleUnhandledRejection(event: PromiseRejectionEvent) {
      const err = event.reason;
      if (err instanceof AuthExpiredError) {
        event.preventDefault();
        setAuthSession(null);
        setSession(null);
      }
    }
    window.addEventListener("unhandledrejection", handleUnhandledRejection);
    return () => window.removeEventListener("unhandledrejection", handleUnhandledRejection);
  }, []);

  // 运行时会话过期巡检：浏览器长时间打开时，session_expires_at 会到达
  React.useEffect(() => {
    if (!session) return;
    function checkExpired() {
      if (isSessionExpired()) {
        setAuthSession(null);
        setSession(null);
      }
    }
    checkExpired();
    // 每 30s 巡检一次，足够及时发现隔一天/隔夜过期，又不消耗性能
    const timer = window.setInterval(checkExpired, 30_000);
    return () => window.clearInterval(timer);
  }, [session?.session_expires_at]);
  const [page, setPageState] = React.useState<PageKey>("adminDashboard");
  const [projects, setProjects] = React.useState<Project[]>([]);
  const [tasks, setTasks] = React.useState<Task[]>([]);
  const [assets, setAssets] = React.useState<Asset[]>([]);
  const [assetVariantPlans, setAssetVariantPlans] = React.useState<AssetVariantPlan[]>([]);
  const [scriptSegments, setScriptSegments] = React.useState<ScriptSegment[]>([]);
  const [storyboards, setStoryboards] = React.useState<Storyboard[]>([]);
  const [users, setUsers] = React.useState<User[]>([]);
  const [runs, setRuns] = React.useState<AgentRun[]>([]);
  const [draftOverrides, setDraftOverrides] = React.useState<Record<string, BreakdownDraftData>>({});
  // Scope keys whose breakdown draft is currently being (re)fetched, so the
  // workbench can show a subtle "syncing latest result" hint instead of silently
  // swapping stale content when a node auto-reloads after completion.
  const [breakdownDraftSyncing, setBreakdownDraftSyncing] = React.useState<Record<string, boolean>>({});
  const [selectedProjectId, setSelectedProjectId] = React.useState<string | null>(null);
  const [projectEntryMode, setProjectEntryMode] = React.useState<ProjectEntryMode>({ type: "create_project" });
  const [agentStageMap, setAgentStageMap] = React.useState<AgentStageMapItem[]>([]);
  const [dashboard, setDashboard] = React.useState<DashboardSummary | null>(null);
  const [workspace, setWorkspace] = React.useState<WorkspaceResponse | null>(null);
  const [reviewWorkspace, setReviewWorkspace] = React.useState<WorkspaceResponse | null>(null);
  const [admin, setAdmin] = React.useState<AdminOverview | null>(null);
  const [hermesConfig, setHermesConfig] = React.useState<HermesConfig | null>(null);
  const [hermesHealth, setHermesHealth] = React.useState<HermesHealth | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);
  const [notificationsOpen, setNotificationsOpen] = React.useState(false);
  const [notifications, setNotifications] = React.useState<UserNotification[]>([]);
  const [loading, setLoading] = React.useState(false);

  const project = projects.find((item) => item.id === selectedProjectId) ?? projects[0];

  function setPage(nextPage: PageKey) {
    if (nextPage === "projectDetail") {
      const targetProject = projects.find((item) => item.id === selectedProjectId) ?? projects[0];
      setProjectEntryMode(targetProject
        ? { type: "project_overview", projectId: targetProject.id }
        : { type: "create_project" });
    }
    setPageState(nextPage);
    if (nextPage === "projectOverview") {
      void refresh(undefined, {
        includeProjectData: false,
        includeTasks: true,
        includeAssets: false,
        projectScopeId: null,
      });
    }
    if (nextPage === "adminDashboard") {
      // 总控看板需要用户列表、管理统计（KPI / 待处理事项）和任务数据（进度计算）
      // TODO(总控看板-后端接口需求文档-1 需求5): 当前只加载全局 /users，导致所有项目卡片显示同一批团队成员。
      // 后端新增 GET /projects/:id/members（或在 /projects 返回中嵌入 members 字段，最多 4 个）后，
      // 需在此处按项目维度拉取成员并下发给 AdminControlDashboard，替换 teamMembersFromUsers 的全局兜底。
      void refresh(undefined, {
        includeProjectData: false,
        includeTasks: true,
        includeAssets: false,
        includeAdmin: true,
        includeUsers: true,
        projectScopeId: null,
      });
    }
    if (nextPage === "myTasks" || nextPage === "reviewCenter" || nextPage === "reviewWorkbench") {
      void refresh(undefined, {
        includeProjectData: false,
        includeWorkspace: true,
        includeTasks: true,
        includeAssets: true,
        projectScopeId: null,
      });
    }
    if (nextPage === "assets" || nextPage === "queryAgent" || nextPage === "assetLibrary") {
      const activeProject = projects.find((item) => item.id === selectedProjectId) ?? projects[0];
      void refresh(activeProject?.id, {
        includeProjectData: Boolean(activeProject),
        includeTasks: true,
        includeAssets: true,
        projectScopeId: null,
      });
    }
    if (nextPage === "agentAdmin" || nextPage === "systemLogs" || nextPage === "teamMembers") {
      const activeProject = projects.find((item) => item.id === selectedProjectId) ?? projects[0];
      const teamLikePage = nextPage === "teamMembers";
      void refresh(activeProject?.id, {
        includeProjectData: Boolean(activeProject) && !teamLikePage,
        includeTasks: false,
        includeAssets: false,
        includeAdmin: true,
        // 显式传 includeUsers: true，避免依赖 refresh 内部默认值读取旧 page 导致成员列表不加载
        includeUsers: true,
      });
    }
    if (nextPage === "creationCenter") {
      void refresh(undefined, {
        includeProjectData: true,
        includeTasks: true,
        includeAssets: false,
        includeUsers: true,
        projectScopeId: null,
      });
    }
    if (nextPage === "projectManagement") {
      void refresh(undefined, {
        includeProjectData: false,
        includeTasks: true,
        includeAssets: false,
        includeUsers: true,
        projectScopeId: null,
      });
    }
  }

  function startNewProject() {
    setProjectEntryMode({ type: "create_project" });
    setPageState("projectDetail");
  }

  function startAddEpisode(projectId: string) {
    setSelectedProjectId(projectId);
    setProjectEntryMode({ type: "add_episode", projectId });
    setPageState("projectDetail");
    void refresh(projectId, {
      includeProjectData: false,
      includeTasks: false,
      includeAssets: false,
      // 显式传 includeUsers: true，避免依赖 refresh 内部默认值读取旧 page 导致 NewProjectDetailPage 的成员列表为空
      includeUsers: true,
      projectScopeId: projectId,
    });
  }

  function startAddScriptVersion(projectId: string, episodeId: string) {
    setSelectedProjectId(projectId);
    setProjectEntryMode({ type: "add_script_version", projectId, episodeId });
    setPageState("projectDetail");
    void refresh(projectId, {
      includeProjectData: false,
      includeTasks: false,
      includeAssets: false,
      // 显式传 includeUsers: true，避免依赖 refresh 内部默认值读取旧 page 导致 NewProjectDetailPage 的成员列表为空
      includeUsers: true,
      projectScopeId: projectId,
    });
  }

  React.useEffect(() => {
    if (session) refresh();
  }, [session?.token]);

  React.useEffect(() => {
    if (!session || (page !== "reviewCenter" && page !== "reviewWorkbench") || !["admin", "director"].includes(session.user.role)) return;
    let cancelled = false;
    async function syncReviewWorkspace() {
      try {
        const result = await api.get<WorkspaceResponse>("/workspace/reviews");
        if (!cancelled) setReviewWorkspace(result);
      } catch (err) {
        if (err instanceof AuthExpiredError) {
          logout();
          return;
        }
        // 兜底：当本地会话已过期时也直接登出（避免 api 预检查之外的分支漏判）
        if (isSessionExpired()) {
          logout();
          return;
        }
        if (!cancelled) setReviewWorkspace(null);
      }
    }
    void syncReviewWorkspace();
    const timer = window.setInterval(() => void syncReviewWorkspace(), 10_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [session?.token, session?.user.role, page]);

  React.useEffect(() => {
    if (!session) return;
    let cancelled = false;
    async function syncNotifications() {
      try {
        const result = await api.get<UserNotification[]>("/workspace/notifications");
        if (!cancelled) setNotifications(result);
      } catch (err) {
        if (err instanceof AuthExpiredError) {
          logout();
          return;
        }
        // 兜底：当本地会话已过期时也直接登出（避免 api 预检查之外的分支漏判）
        if (isSessionExpired()) {
          logout();
          return;
        }
        if (!cancelled) setNotifications([]);
      }
    }
    void syncNotifications();
    const timer = window.setInterval(() => void syncNotifications(), 10_000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [session?.token]);

  React.useEffect(() => {
    if (!notificationsOpen || !notifications.some((item) => !item.is_read)) return;
    void api.post<{ updated: number }>("/workspace/notifications/read-all").then(() => {
      setNotifications((current) => current.map((item) => ({ ...item, is_read: true })));
    }).catch(() => undefined);
  }, [notificationsOpen, notifications]);

  const hasPendingProjectRun = runs.some(isAgentPending);
  React.useEffect(() => {
    if (!session || page !== "projectDetail" || !project?.id || !hasPendingProjectRun) return;
    let cancelled = false;
    let polling = false;
    let fallbackTimer: number | null = null;
    const controller = new AbortController();
    const reconcileCompletedRun = async () => {
      // Light path: a completed node only needs project-scoped data (dashboard,
      // runs, script segments, storyboards) refreshed — NOT the global
      // /projects + /tasks + /assets collections a full refresh() re-fetches.
      // The breakdown draft view is reloaded separately by ProjectDetailPage's
      // completion-signal effect, so the workbench reflects results without a
      // manual page refresh.
      if (!cancelled) await refreshProjectData(project.id, { includePayload: false });
    };
    const hydrateLiveDetails = async (nextRuns: AgentRun[]) => {
      const previousRuns = new Map(runs.map((run) => [run.id, run]));
      const detailTargets = nextRuns.filter((run) => (
        isAgentPending(run)
        || (
        !isAgentPending(run) && isAgentPending(previousRuns.get(run.id))
        )
      ));
      const runResults = await Promise.allSettled(
        detailTargets.map((run) => api.get<AgentRun>(`/agent-runs/${run.id}`)),
      );
      const runDetails = runResults.filter((result): result is PromiseFulfilledResult<AgentRun> => result.status === "fulfilled").map((result) => result.value);
      setRuns((current) => mergeAgentRunDetails(mergeAgentRunSummaries(current, nextRuns), runDetails));
    };
    const poll = async () => {
      if (cancelled || polling) return;
      polling = true;
      try {
        const result = await refreshProjectData(project.id, { includePayload: false });
        await hydrateLiveDetails(result.runs);
        if (!cancelled && !result.hasPendingRun) {
          await reconcileCompletedRun();
        }
      } catch {
        // Keep the polling loop alive across transient API/network failures.
      } finally {
        polling = false;
      }
    };
    const startFallbackPolling = () => {
      if (cancelled || fallbackTimer !== null) return;
      void poll();
      fallbackTimer = window.setInterval(() => void poll(), 5000);
    };
    const consumeRunEvents = async () => {
      try {
        const response = await fetch(`${API_BASE}/api/projects/${project.id}/run-events`, {
          headers: {
            Accept: "text/event-stream",
            ...(getAuthToken() ? { Authorization: `Bearer ${getAuthToken()}` } : {}),
          },
          signal: controller.signal,
        });
        if (!response.ok || !response.body) throw new Error("run event stream unavailable");
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        while (!cancelled) {
          const chunk = await reader.read();
          buffer += decoder.decode(chunk.value || new Uint8Array(), { stream: !chunk.done });
          const blocks = buffer.split(/\r?\n\r?\n/);
          buffer = blocks.pop() || "";
          for (const block of blocks) {
            const parsed = parseRunEventBlock(block);
            if (!parsed || parsed.event !== "snapshot" || !parsed.data || typeof parsed.data !== "object") continue;
            const snapshot = parsed.data as Record<string, unknown>;
            const nextRuns = normalizeRunList<AgentRun>(snapshot.agent_runs).items;
            if (snapshot.agent_runs === undefined) continue;
            await hydrateLiveDetails(nextRuns);
            const pending = nextRuns.some(isAgentPending);
            if (!pending) await reconcileCompletedRun();
          }
          if (chunk.done) break;
        }
        if (!cancelled) startFallbackPolling();
      } catch {
        if (!cancelled) startFallbackPolling();
      }
    };
    void consumeRunEvents();
    return () => {
      cancelled = true;
      controller.abort();
      if (fallbackTimer !== null) window.clearInterval(fallbackTimer);
    };
  }, [session?.token, page, project?.id, hasPendingProjectRun]);

  async function login(phone: string, password: string) {
    setLoading(true);
    setError(null);
    try {
      const result = await api.post<LoginResponse>("/auth/login", { phone, password });
      const token = result.access_token;
      if (!token || !result.tapcanvas_token || !result.platform_token || !result.session_expires_at) {
        throw new Error("登录接口未返回完整会话");
      }
      const user = normalizeUser(result.user);
      const nextSession: AuthSession = {
        token,
        user,
        tapcanvas_token: result.tapcanvas_token,
        platform_token: result.platform_token,
        session_expires_at: result.session_expires_at,
      };
      setAuthSession(nextSession);
      setSession(nextSession);
      setPage("adminDashboard");
      setNotice(`欢迎回来，${user.display_name}`);
      window.setTimeout(() => void refresh(), 0);
    } catch (err) {
      setError(err instanceof Error ? err.message : "登录失败");
    } finally {
      setLoading(false);
    }
  }

  function clearLoginError() {
    setError(null);
  }

  function logout() {
    setAuthSession(null);
    setSession(null);
    setProjects([]);
    setTasks([]);
    setAssets([]);
    setAssetVariantPlans([]);
    setScriptSegments([]);
    setStoryboards([]);
    setUsers([]);
    setRuns([]);
    setDraftOverrides({});
    setAgentStageMap([]);
    setDashboard(null);
    setNotifications([]);
    setWorkspace(null);
    setReviewWorkspace(null);
    setAdmin(null);
    setNotice(null);
    setError(null);
  }

  // TODO(全局框架与主题-任务B): 后端 /auth/avatar 与 /auth/password 接口就绪后，
  // 移除下方 try/catch 兜底，直接对接真实持久化。当前先调用后端接口，
  // 成功则更新本地会话；失败返回 false 由 AccountMenu 展示错误，不阻塞 UI 迁移。
  async function updateAvatar(avatarUrl: string) {
    if (!session) return false;
    try {
      await api.post("/auth/avatar", { avatar_url: avatarUrl });
      const nextSession: AuthSession = { ...session, user: { ...session.user, avatar_url: avatarUrl } };
      setAuthSession(nextSession);
      setSession(nextSession);
      setUsers((current) => current.map((item) => (item.id === nextSession.user.id ? nextSession.user : item)));
      setNotice("头像已更新");
      return true;
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        logout();
        setNotice("登录已过期，请重新登录。");
        return false;
      }
      console.error("updateAvatar error", err);
      return false;
    }
  }

  async function changePassword(currentPassword: string, newPassword: string): Promise<{ ok: boolean; error?: string }> {
    if (!session) return { ok: false, error: "未登录" };
    try {
      await api.post("/auth/password", { current_password: currentPassword, new_password: newPassword });
      setNotice("密码已修改，请使用新密码登录");
      return { ok: true };
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        logout();
        setNotice("登录已过期，请重新登录。");
        return { ok: false, error: "登录已过期，请重新登录" };
      }
      console.error("changePassword error", err);
      const message = err instanceof Error ? err.message : "密码修改失败";
      return { ok: false, error: message };
    }
  }

  const refreshProjectData = React.useCallback(async (projectId: string, options: ProjectDataLoadOptions = {}) => {
    const includeCollections = options.includeCollections ?? false;
    const includeUsers = options.includeUsers ?? false;
    const [dashboardData, runData, segmentResult, storyboardResult, variantPlanResult, taskResult, assetResult, userResult] = await Promise.all([
      api.get<DashboardSummary>(`/projects/${projectId}/dashboard`),
      fetchAllRunPages<AgentRun>(`/agent-runs?project_id=${encodeURIComponent(projectId)}`),
      api.get<ScriptSegment[]>(`/projects/${projectId}/script-segments`).catch((err) => { if (err instanceof AuthExpiredError) throw err; return []; }),
      api.get<Storyboard[]>(`/projects/${projectId}/storyboards`).catch((err) => { if (err instanceof AuthExpiredError) throw err; return []; }),
      Promise.resolve([] as AssetVariantPlan[]),
      includeCollections ? api.get<Task[]>(`/tasks?project_id=${encodeURIComponent(projectId)}`).catch((err) => { if (err instanceof AuthExpiredError) throw err; return []; }) : Promise.resolve(null),
      includeCollections ? api.get<Asset[]>(`/assets?project_id=${encodeURIComponent(projectId)}&include_metadata=false`).catch((err) => { if (err instanceof AuthExpiredError) throw err; return []; }) : Promise.resolve(null),
      includeUsers && canManage(session?.user) ? api.get<User[]>("/users").catch((err) => { if (err instanceof AuthExpiredError) throw err; return []; }) : Promise.resolve(null),
    ]);
    setDashboard(dashboardData);
    setRuns((current) => mergeAgentRunSummaries(current, runData));
    setScriptSegments(segmentResult);
    setStoryboards(storyboardResult);
    setAssetVariantPlans(variantPlanResult);
    if (taskResult) setTasks(taskResult);
    if (assetResult) setAssets(assetResult);
    if (userResult) setUsers(userResult);
    return {
      hasPendingRun: runData.some(isAgentPending),
      runs: runData,
    };
  }, [session?.user.id, session?.user.role]);

  async function refresh(preferredProjectId?: string, options: RefreshOptions = {}) {
    if (!options.silent) {
      setLoading(true);
      setError(null);
    }
    try {
      const canLoadReviews = session?.user.role === "admin" || session?.user.role === "director";
      const includeWorkspace = options.includeWorkspace ?? (page === "myTasks" || page === "reviewCenter");
      const includeProjectData = options.includeProjectData ?? ["projectDetail", "assets", "agentAdmin", "systemLogs", "queryAgent", "assetLibrary"].includes(page);
      const includeTasks = options.includeTasks ?? ["projectOverview", "projectDetail", "myTasks", "reviewCenter", "assets", "queryAgent", "assetLibrary"].includes(page);
      const includeAssets = options.includeAssets ?? ["projectDetail", "myTasks", "reviewCenter", "assets", "queryAgent", "assetLibrary"].includes(page);
      const includeAdmin = options.includeAdmin ?? ["agentAdmin", "teamMembers", "systemLogs"].includes(page);
      const includeUsers = options.includeUsers ?? ["agentAdmin", "teamMembers", "systemLogs", "projectManagement", "creationCenter", "projectDetail"].includes(page);
      const projectScopeId = options.projectScopeId !== undefined
        ? options.projectScopeId
        : page === "projectDetail" && !includeWorkspace
          ? preferredProjectId ?? selectedProjectId
          : null;
      const taskPath = projectScopeId ? `/tasks?project_id=${encodeURIComponent(projectScopeId)}` : "/tasks";
      const assetPath = projectScopeId
        ? `/assets?project_id=${encodeURIComponent(projectScopeId)}&include_metadata=false`
        : "/assets?include_metadata=false";
      const [projectData, taskData, assetData, workspaceData, reviewWorkspaceData] = await Promise.all([
        api.get<Project[]>("/projects"),
        includeTasks ? api.get<Task[]>(taskPath) : Promise.resolve(null),
        includeAssets ? api.get<Asset[]>(assetPath) : Promise.resolve(null),
        includeWorkspace ? api.get<WorkspaceResponse>("/workspace/tasks") : Promise.resolve(null),
        includeWorkspace && canLoadReviews ? api.get<WorkspaceResponse>("/workspace/reviews").catch((err) => { if (err instanceof AuthExpiredError) throw err; console.warn("[reviewWorkspace] /workspace/reviews failed:", err); return null; }) : Promise.resolve(null),
      ]);
      setProjects(projectData || []);
      const activeProject = projectData.find((item) => item.id === (preferredProjectId ?? selectedProjectId)) ?? projectData[0];
      if (activeProject && activeProject.id !== selectedProjectId) {
        setSelectedProjectId(activeProject.id);
      }
      if (taskData) setTasks(taskData);
      if (assetData) setAssets(assetData);
      if (includeWorkspace) {
        console.log("[useCineForgeApp] workspace (from /workspace/tasks):", workspaceData ? `todo=${workspaceData.groups?.todo?.length || 0}, completed=${workspaceData.groups?.completed?.length || 0}` : "null");
        console.log("[useCineForgeApp] reviewWorkspace (from /workspace/reviews):", reviewWorkspaceData ? `todo=${reviewWorkspaceData.groups?.todo?.length || 0}, completed=${reviewWorkspaceData.groups?.completed?.length || 0}` : "null");
        console.log("[useCineForgeApp] tasks count:", taskData?.length || 0);
        setWorkspace(workspaceData);
        setReviewWorkspace(reviewWorkspaceData);
      }
      if (canManage(session?.user) && (includeAdmin || includeUsers)) {
        if (includeUsers) {
          const userResult = await api.get<User[]>("/users").catch((err) => { if (err instanceof AuthExpiredError) throw err; console.warn("[useCineForgeApp] /users failed:", err); return null; });
          if (userResult) setUsers(userResult);
        }
        if (includeAdmin) {
          // TODO(总控看板-后端接口需求文档-1 需求1/需求3): /admin/overview 当前仅返回 { stats: Record<string, number> }，
          // 缺少 active_project_count / monthly_completed_episodes / pending_items_count / team_creator_count /
          // pending_items / recent_readings 等结构化字段。后端扩展返回后，AdminOverview 类型与 AdminControlDashboard
          // 的 KPI / 待办列表 / 围读记录渲染需同步对接（见 types.ts 与 AdminControlDashboard.tsx 中的 TODO）。
          const adminResult = await api.get<AdminOverview>("/admin/overview").catch((err) => { if (err instanceof AuthExpiredError) throw err; console.warn("[useCineForgeApp] /admin/overview failed:", err); return null; });
          if (adminResult) setAdmin(adminResult);
          await refreshHermesStatus(false);
        }
      } else if (!canManage(session?.user)) {
        setUsers([]);
        setAdmin(null);
        setHermesConfig(null);
        setHermesHealth(null);
      }
      if (activeProject && includeProjectData) {
        await refreshProjectData(activeProject.id, { includePayload: options.includePayload ?? false });
      } else if (!includeProjectData) {
        setDashboard(null);
        setRuns([]);
        setScriptSegments([]);
        setStoryboards([]);
        setAssetVariantPlans([]);
      }
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        logout();
        setNotice("登录已过期，请重新登录。");
        return;
      }
      // 兜底：本地会话已过期也判定为登出，避免 api 预检查之外的 500 走了普通错误分支
      if (isSessionExpired()) {
        logout();
        setNotice("登录已过期，请重新登录。");
        return;
      }
      setError(err instanceof Error ? err.message : "接口加载失败");
    } finally {
      if (!options.silent) setLoading(false);
    }
  }

  async function refreshHermesStatus(showNotice = true) {
    if (!canManage(session?.user)) return;
    try {
      const [config, health] = await Promise.all([
        api.get<HermesConfig>("/agents/hermes/config"),
        api.get<HermesHealth>("/agents/hermes/health"),
      ]);
      setHermesConfig(config);
      setHermesHealth(health);
      if (showNotice) {
        setNotice(health.status === "ok" ? "Hermes 授权和连接已通过检查。" : `Hermes 状态：${health.status}`);
      }
    } catch (err) {
      setHermesHealth(null);
      setError(err instanceof Error ? err.message : "Hermes 状态检查失败");
    }
  }

  async function runBreakdownStep(step: string, projectId?: string, episodeId?: string, scriptVersionId?: string) {
    const targetProjectId = projectId ?? project?.id;
    if (!targetProjectId) return false;
    if (hasPendingAgentRun(runs, "script_breakdown")) {
      setNotice("已有拆解步骤正在运行，请等待完成后再运行下一步。");
      return false;
    }
    const stepLabels: Record<string, string> = {
      reading: "剧本围读",
      assets: "资产拆解",
      prompts: "提示词生成",
      segmentation: "脚本段拆分",
      storyboard: "分镜拆解",
    };
    const label = stepLabels[step] || step;
    setLoading(true);
    setError(null);
    try {
      const params = new URLSearchParams();
      if (episodeId) params.set("episode_id", episodeId);
      if (scriptVersionId) params.set("script_version_id", scriptVersionId);
      const query = params.toString();
      const run = await api.post<AgentRun>(`/projects/${targetProjectId}/breakdown-steps/${step}${query ? `?${query}` : ""}`);
      setSelectedProjectId(targetProjectId);
      setRuns((current) => [run, ...current]);
      // 不再弹乐观提示：是否真正起跑以进度面板/按键禁用为准（避免误导）。
      await refresh(targetProjectId);
      setProjectEntryMode({ type: "project_overview", projectId: targetProjectId, module: "revision", episodeId });
      setPageState("projectDetail");
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : `${label}运行失败`);
      return false;
    } finally {
      setLoading(false);
    }
  }

  async function createProject(payload: ProjectCreatePayload) {
    setLoading(true);
    setError(null);
    try {
      if (!payload.title?.trim()) throw new Error("请填写项目名称");
      if (!payload.project_prefix?.trim()) throw new Error("请填写项目缩写");
      if (!payload.script_text?.trim()) throw new Error("请填写剧本文本");
      const createdProject = await api.post<Project>("/projects", payload);
      setSelectedProjectId(createdProject.id);
      setProjectEntryMode({ type: "project_overview", projectId: createdProject.id });
      setNotice(`项目「${payload.title}」已创建，可继续运行剧本阅读和拆解。`);
      await refresh(createdProject.id);
      setPageState("projectDetail");
      return createdProject;
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建项目失败");
      return null;
    } finally {
      setLoading(false);
    }
  }

  async function retryPromptTaskDistribution(projectId: string, runId: string) {
    setLoading(true);
    setError(null);
    try {
      const run = await api.post<AgentRun>(`/projects/${projectId}/breakdown-steps/prompts/${runId}/retry-distribution`);
      setRuns((current) => [run, ...current.filter((item) => item.id !== run.id)]);
      setNotice("资产提示词任务已重新分发，提示词 Agent 未重复执行。");
      await refresh(projectId);
    } catch (err) {
      setError(err instanceof Error ? err.message : "资产任务分发重试失败");
    } finally {
      setLoading(false);
    }
  }

  async function createTemporaryAssetProduction(payload: TemporaryAssetProductionPayload) {
    setLoading(true);
    setError(null);
    try {
      const result = await api.post<TemporaryAssetProductionResponse>("/assets/temporary-production", payload);
      setNotice(`已创建 ${result.asset.asset_code}，人工生产任务已进入待分配。`);
      await refresh(payload.project_id, {
        includeProjectData: true,
        includeTasks: true,
        includeAssets: true,
        projectScopeId: payload.project_id,
      });
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : "人工临时生产资产创建失败");
      return false;
    } finally {
      setLoading(false);
    }
  }

  const loadBreakdownDraft = React.useCallback(async (projectId: string, episodeId: string, scriptVersionId: string) => {
    const scopeKey = breakdownDraftScopeKey(projectId, episodeId, scriptVersionId);
    const params = new URLSearchParams({ episode_id: episodeId, script_version_id: scriptVersionId });
    setBreakdownDraftSyncing((current) => ({ ...current, [scopeKey]: true }));
    try {
      const currentBreakdown = await api.get<unknown>(`/projects/${projectId}/breakdowns/current?${params.toString()}`);
      setDraftOverrides((current) => {
        const next = { ...current };
        if (currentBreakdown) {
          const draft = draftFromBreakdown(currentBreakdown);
          next[scopeKey] = {
            ...draft,
            readingRevisionContext: {
              ...(draft.readingRevisionContext || {}),
              episode_id: episodeId,
              script_version_id: scriptVersionId,
            },
          };
        } else {
          delete next[scopeKey];
        }
        return next;
      });
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        logout();
        setNotice("登录已过期，请重新登录。");
        return;
      }
      setError(err instanceof Error ? err.message : "拆解草稿加载失败");
    } finally {
      setBreakdownDraftSyncing((current) => {
        if (!current[scopeKey]) return current;
        const next = { ...current };
        delete next[scopeKey];
        return next;
      });
    }
  }, []);

  async function updateProject(projectId: string, payload: ProjectUpdatePayload) {
    setLoading(true);
    setError(null);
    try {
      if (!payload.title?.trim()) throw new Error("请填写项目名称");
      if (!payload.project_prefix?.trim()) throw new Error("请填写项目缩写");
      if (!payload.script_text?.trim()) throw new Error("请填写剧本文本");
      const updatedProject = await api.patch<Project>(`/projects/${projectId}`, payload);
      setSelectedProjectId(updatedProject.id);
      setProjectEntryMode({ type: "project_overview", projectId: updatedProject.id });
      setNotice(`项目「${updatedProject.title}」草稿已保存，可继续运行剧本阅读和拆解。`);
      await refresh(updatedProject.id);
      setPageState("projectDetail");
      return updatedProject;
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存项目草稿失败");
      return null;
    } finally {
      setLoading(false);
    }
  }

  async function updateProjectSettings(input: {
    projectId: string;
    title: string;
    abbreviation: string;
    genre: string;
    production_brief: ProjectProductionBrief;
    targetSeconds: number;
    newScriptFile: File | null;
  }): Promise<Project | null> {
    setLoading(true);
    setError(null);
    try {
      let result: Project;
      const normalizedPrefix = input.abbreviation.trim().toUpperCase().slice(0, 5);
      if (input.newScriptFile) {
        // 用户重新上传剧本：走 /projects/import，带 project_id 更新现有项目并创建新剧本版本。
        const importPayload: ProjectImportPayload = {
          title: input.title.trim(),
          project_prefix: normalizedPrefix,
          genre: input.genre.trim(),
          project_id: input.projectId,
          production_brief: input.production_brief,
          episode_brief: {
            schema_version: "EpisodeProductionBrief.v1",
            target_duration_seconds: input.targetSeconds,
            field_sources: { target_duration_seconds: "human_confirmed" },
          },
          // 编辑模式下若剧本集已存在，默认允许创建新版本而非因重复阻断。
          confirm_existing_episode_version: true,
        };
        result = await api.importProject(importPayload, input.newScriptFile);
        setNotice(`项目「${result.title}」剧本已更新，分集草稿已同步刷新。`);
      } else {
        // 仅更新元信息 / production_brief，不换剧本。
        const updatePayload: Partial<ProjectUpdatePayload> = {
          title: input.title.trim(),
          project_prefix: normalizedPrefix,
          genre: input.genre.trim(),
          production_brief: input.production_brief,
        };
        result = await api.patch<Project>(`/projects/${input.projectId}`, updatePayload);
        setNotice(`项目「${result.title}」已保存。`);
      }
      // 保持在项目管理页：只刷新项目 / 任务（卡片进度依赖 tasks），不跳转详情。
      await refresh(undefined, {
        includeProjectData: false,
        includeWorkspace: false,
        includeTasks: true,
        includeAssets: false,
        includeAdmin: false,
        includeUsers: false,
      });
      return result;
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存项目失败");
      return null;
    } finally {
      setLoading(false);
    }
  }

  async function importProjectFromFile(payload: ProjectImportPayload, file: File) {
    setLoading(true);
    setError(null);
    try {
      if (!payload.title?.trim()) throw new Error("请填写项目名称");
      if (!payload.project_prefix?.trim()) throw new Error("请填写项目缩写");
      if (!file) throw new Error("请先选择剧本文件");
      const normalized = {
        title: payload.title.trim(),
        project_prefix: payload.project_prefix.trim().toUpperCase().slice(0, 5),
        genre: payload.genre?.trim(),
        project_id: payload.project_id,
        production_brief: payload.production_brief,
        episode_brief: payload.episode_brief,
        episode_no: payload.episode_no,
        confirm_existing_episode_version: payload.confirm_existing_episode_version,
      };
      const createdProject = await api.importProject(normalized, file);
      setSelectedProjectId(createdProject.id);
      setProjectEntryMode({
        type: "project_overview",
        projectId: createdProject.id,
        module: "revision",
        episodeId: createdProject.latest_import?.episode_id,
      });
      const importContext = createdProject.latest_import;
      setNotice(payload.project_id
        ? importContext?.replaced_existing_episode
          ? `${importContext.episode_code} 已创建剧本新版本 v${importContext.script_version_no}。`
          : `${importContext?.episode_code || "新分集"} 已导入项目「${createdProject.title}」。`
        : `项目「${createdProject.title}」及首集 ${importContext?.episode_code || "EP01"} 已创建。`);
      await refresh(createdProject.id);
      setPageState("projectDetail");
      return createdProject;
    } catch (err) {
      setError(err instanceof Error ? err.message : "导入剧本失败");
      return null;
    } finally {
      setLoading(false);
    }
  }

  async function runProjectAgent(agentType: string) {
    if (!project) return;
    if (FORMAL_BREAKDOWN_AGENT_TYPES.has(agentType)) {
      setError("正式拆解必须从分集工作台按五步流程执行。");
      return;
    }
    if (hasPendingAgentRun(runs, agentType)) {
      setNotice(`${labelAgentType(agentType)} 正在运行，请等待 Agent 返回结果后再提交。`);
      return;
    }
    setLoading(true);
    try {
      const run = await api.post<AgentRun>(`/projects/${project.id}/agents/${agentType}/jobs`);
      setRuns((current) => [run, ...current]);
      if (run.status === "running") {
        setNotice(`${labelAgentType(agentType)} 已进入后台运行，页面会自动刷新；完成后可查看输出并按需写入项目数据。`);
        await refresh(project.id);
      } else {
        setNotice(`${labelAgentType(agentType)} 已运行完成，可查看输出并按需写入项目数据。`);
        await refresh(project.id);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Agent 运行失败");
    } finally {
      setLoading(false);
    }
  }

  async function materializeAgentRun(run: AgentRun) {
    if (!project) return;
    if (FORMAL_BREAKDOWN_AGENT_TYPES.has(run.agent_type)) {
      setError("正式拆解结果由五步流程自动归一化，禁止从 Agent 运行记录重复写入。");
      return;
    }
    setLoading(true);
    try {
      const result = await api.post<AgentMaterializeResponse>(`/projects/${project.id}/agents/runs/${run.id}/materialize`);
      setNotice(`${result.message} 脚本原文段 ${result.created_script_segments}，版本 ${result.created_entity_versions}，样本 ${result.created_training_samples}。`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "写入项目数据失败");
    } finally {
      setLoading(false);
    }
  }

  async function saveBreakdownDraft(draft: BreakdownDraftData) {
    if (!project) return false;
    const revisionContext = draft.readingRevisionContext || {};
    const episodeId = String(revisionContext.episode_id || "").trim();
    const scriptVersionId = String(revisionContext.script_version_id || "").trim();
    if (!episodeId || !scriptVersionId) {
      setError("当前拆解草稿缺少分集或剧本版本范围，无法保存。");
      return false;
    }
    const payload = breakdownPayload(draft);
    setLoading(true);
    setError(null);
    try {
      const saved = await api.post<BreakdownRead>(`/projects/${project.id}/breakdowns/current`, payload);
      const scopeKey = breakdownDraftScopeKey(project.id, episodeId, scriptVersionId);
      const savedDraft = draftFromBreakdown(saved);
      setDraftOverrides((current) => ({
        ...current,
        [scopeKey]: {
          ...savedDraft,
          readingRevisionContext: {
            ...(savedDraft.readingRevisionContext || {}),
            episode_id: episodeId,
            script_version_id: scriptVersionId,
          },
        },
      }));
      const needsPromptRegeneration = ["production", "assembly", "completed"].includes(String(project.status || ""));
      setNotice(needsPromptRegeneration ? "拆解草稿已保存到后端；内容已变更，需要重新触发提示词生成流程。" : "拆解草稿已保存到后端。");
      void refresh(project.id, {
        silent: true,
        includeProjectData: false,
        includeTasks: false,
        includeAssets: true,
        projectScopeId: project.id,
      });
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存拆解草稿失败");
      return false;
    } finally {
      setLoading(false);
    }
  }

  async function confirmScriptSegments(draft: BreakdownDraftData) {
    if (!project) return;
    setLoading(true);
    setError(null);
    try {
      const result = await api.post<ScriptSegmentsConfirmResponse>(
        `/projects/${project.id}/script-segments/confirm`,
        breakdownPayload(draft),
      );
      setNotice(`脚本段已确认入库：${result.script_segment_count} 条。已进入分镜和资产拆分阶段。`);
      await refresh(project.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "确认脚本段失败");
    } finally {
      setLoading(false);
    }
  }

  async function lockEpisodeBreakdown(episodeCode: string, draft: BreakdownDraftData) {
    if (!project) return false;
    setLoading(true);
    setError(null);
    try {
      await api.post(`/projects/${project.id}/breakdowns/lock`, {
        ...breakdownPayload(draft, episodeCode || "EP01"),
        create_asset_tasks: false,
      });
      setNotice(`已锁定 ${episodeCode || "EP01"}，正在生成分镜任务；资产任务已在提示词步骤统一创建。`);
      await refresh(project.id);
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : "锁定本集失败");
      return false;
    } finally {
      setLoading(false);
    }
  }

  async function softDeleteProject(targetProject: Project, adminPassword: string) {
    const password = adminPassword.trim();
    if (!password) return;
    setLoading(true);
    setError(null);
    try {
      await api.post(`/projects/${targetProject.id}/soft-delete`, { admin_password: password });
      setProjects((current) => current.filter((item) => item.id !== targetProject.id));
      setDraftOverrides((current) => {
        const next = { ...current };
        const projectPrefix = breakdownDraftProjectPrefix(targetProject.id);
        Object.keys(next).forEach((key) => {
          if (key.startsWith(projectPrefix)) delete next[key];
        });
        return next;
      });
      setNotice(`项目「${targetProject.title}」已提交软删除。`);
      await refresh(selectedProjectId === targetProject.id ? undefined : selectedProjectId ?? undefined);
      if (selectedProjectId === targetProject.id) setPage("projectOverview");
    } catch (err) {
      setError(err instanceof Error ? err.message : "项目删除失败");
    } finally {
      setLoading(false);
    }
  }

  async function saveTaskPrompt(task: Task, promptText: string, copied = false) {
    await api.post(`/tasks/${task.id}/prompts`, {
      prompt_type: copied ? "human_edit_copied" : "human_edit",
      prompt_text: promptText,
      source: "human",
      copied,
    });
    setNotice(copied ? `任务「${task.title}」的提示词已复制并保存版本。` : `任务「${task.title}」已保存改版提示词。`);
    await refresh();
  }

  async function generateTaskPrompt(task: Task, step?: "keyframe" | "video") {
    setLoading(true);
    try {
      const query = step ? `?step=${step}` : "";
      let run = await api.post<AgentRun>(`/tasks/${task.id}/prompt-jobs${query}`);
      setRuns((current) => [run, ...current.filter((item) => item.id !== run.id)]);
      setNotice(`任务「${task.title}」正在生成最新提示词。`);
      const deadline = Date.now() + 8 * 60 * 1000;
      let transientFailures = 0;
      const hasMaterialization = () => Boolean(
        run.output?.task_prompt_materialization
        && typeof run.output.task_prompt_materialization === "object",
      );
      while (["queued", "running"].includes(run.status) || (run.status === "succeeded" && !hasMaterialization())) {
        if (Date.now() >= deadline) throw new Error("Agent 生成提示词超时，请稍后重试");
        await new Promise((resolve) => window.setTimeout(resolve, 1500));
        try {
          run = await api.get<AgentRun>(`/agent-runs/${run.id}`);
          transientFailures = 0;
          setRuns((current) => [run, ...current.filter((item) => item.id !== run.id)]);
        } catch (err) {
          transientFailures += 1;
          if (transientFailures >= 3) throw err;
        }
      }
      if (run.status !== "succeeded") throw new Error(run.error_message || "Agent 生成提示词失败");
      const materialization = run.output?.task_prompt_materialization as Record<string, unknown> | undefined;
      if (materialization?.applied !== true) throw new Error("Agent 已完成，但提示词未能写入当前任务");
      await refresh(task.project_id);
      if (materialization.context_current !== true) {
        setNotice(`任务「${task.title}」生成期间参考主母版再次更新，请重新生成。`);
        return false;
      }
      setNotice(`任务「${task.title}」已应用最新 Agent 提示词。`);
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : "生成提示词失败");
      return false;
    } finally {
      setLoading(false);
    }
  }

  async function uploadTaskCandidates(
    task: Task,
    finalPrompt?: string,
    modelName?: string,
    toolNames?: string[],
    files: File[] = [],
    step?: "keyframe" | "video",
    onProgress?: (update: import("../types").TaskCandidateUploadUpdate) => void,
    resumeUpload?: FileUploadResponse,
  ): Promise<import("../types").TaskCandidateUploadResult[]> {
    setLoading(true);
    setError(null);
    try {
      if (!files.length) throw new Error("请先选择要上传的候选文件");
      const mediaKind = taskMediaKind(task, step);
      const uploadedFiles = await Promise.all(files.map(async (file) => {
        let uploaded = files.length === 1 ? resumeUpload : undefined;
        try {
          if (!uploaded) {
            onProgress?.({ file, progress: 0, status: "uploading" });
            uploaded = await api.uploadTaskFile(task.id, file, (progress) => {
              onProgress?.({ file, progress, status: "uploading" });
            });
          }
          onProgress?.({ file, progress: 100, status: "registering", uploaded });
          return { file, uploaded };
        } catch (err) {
          const error = err instanceof Error ? err.message : "上传失败";
          const result = { file, progress: uploaded ? 100 : 0, status: "failed" as const, error, uploaded };
          onProgress?.(result);
          return result;
        }
      }));
      // Register files serially so every candidate joins the same draft batch.
      // The API also locks the task row, but sequencing here avoids avoidable
      // unique-key retries when several large uploads finish at the same time.
      const results: import("../types").TaskCandidateUploadResult[] = [];
      for (const item of uploadedFiles) {
        if ("status" in item && item.status === "failed") {
          results.push(item);
          continue;
        }
        const { file, uploaded } = item;
        try {
          const submission = await api.post<Submission>(`/tasks/${task.id}/submissions`, {
            file_id: uploaded.id,
            file_path: uploaded.url,
            file_type: mediaKind,
            step: step ?? null,
            prompt_text: finalPrompt || task.latest_prompt_text || task.prompt_text,
            original_prompt_text: task.prompt_text,
            revised_prompt_text: finalPrompt,
            model_name: modelName,
            tool_names: toolNames?.length ? toolNames : [modelName || (mediaKind === "video" ? "Seedance" : mediaKind === "audio" ? "音频制作" : "Seedream")],
          });
          const result = { file, progress: 100, status: "success" as const, uploaded, submission };
          onProgress?.(result);
          results.push(result);
        } catch (err) {
          const result = {
            file,
            progress: 100,
            status: "failed" as const,
            error: err instanceof Error ? err.message : "候选登记失败",
            uploaded,
          };
          onProgress?.(result);
          results.push(result);
        }
      }
      const successCount = results.filter((item) => item.status === "success").length;
      const failedCount = results.length - successCount;
      if (successCount) await refresh();
      if (successCount && failedCount) {
        setNotice(`任务「${task.title}」已上传 ${successCount} 个候选，${failedCount} 个失败，可单独重试。`);
      } else if (successCount) {
        setNotice(`任务「${task.title}」已上传 ${successCount} 个候选成果，请确认后提交审核。`);
      } else {
        setError(`任务「${task.title}」的 ${failedCount} 个候选均上传失败，请在文件队列中重试。`);
      }
      return results;
    } catch (err) {
      setError(err instanceof Error ? err.message : "上传失败");
      return files.map((file) => ({ file, progress: 0, status: "failed" as const, error: err instanceof Error ? err.message : "上传失败" }));
    } finally {
      setLoading(false);
    }
  }

  async function submitTaskBatch(task: Task, step?: "keyframe" | "video") {
    setLoading(true);
    try {
      const batch = await api.post<SubmissionBatch>(`/tasks/${task.id}/submit`, {
        step: step ?? null,
        request_id: createClientUuid(),
      });
      setNotice(`任务「${task.title}」第 ${batch.version_no} 批成果已提交导演审核。`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "提交审核失败");
    } finally {
      setLoading(false);
    }
  }

  async function submitCharacterTaskUnit(tasks: Task[]) {
    setLoading(true);
    try {
      await api.post<SubmissionBatch[]>("/tasks/submission-batches/bulk-submit", {
        request_id: createClientUuid(),
        entries: tasks.map((task) => ({ task_id: task.id, step: null })),
      });
      setNotice(`人物定装 ${tasks.map((task) => task.task_variant).join("、")} 图位已提交导演审核。`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "人物定装统一提交失败");
    } finally {
      setLoading(false);
    }
  }

  async function deleteTaskDraftSubmission(task: Task, submissionId: string) {
    setLoading(true);
    try {
      await api.delete(`/tasks/${task.id}/submissions/${submissionId}`);
      setNotice("候选图片已移除，导演审核历史、原始文件和操作记录已保留。");
      await refresh();
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        logout();
        setNotice("登录已过期，请重新登录。");
        return;
      }
      setError(err instanceof Error ? err.message : "移除候选图片失败");
      throw err;
    } finally {
      setLoading(false);
    }
  }

  async function reviewTaskBatch(
    task: Task,
    batchId: string,
    decision: "approve" | "rework",
    primarySubmissionId: string | null,
    selectedSubmissionIds: string[],
    comment?: string,
  ) {
    setLoading(true);
    try {
      await api.post<Task>(`/tasks/${task.id}/submission-batches/${batchId}/review`, {
        decision,
        primary_submission_id: primarySubmissionId,
        selected_submission_ids: selectedSubmissionIds,
        comment: comment || null,
        request_id: createClientUuid(),
      });
      setNotice(decision === "approve" ? `任务「${task.title}」已审核定版。` : `任务「${task.title}」已退回返工。`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "审核失败");
    } finally {
      setLoading(false);
    }
  }

  async function reviewCharacterTaskUnit(
    entries: Array<{
      task: Task;
      batchId: string;
      primarySubmissionId: string | null;
      selectedSubmissionIds: string[];
    }>,
    decision: "approve" | "rework",
    comment?: string,
  ) {
    setLoading(true);
    try {
      await api.post<Task[]>("/tasks/submission-batches/bulk-review", {
        request_id: createClientUuid(),
        decision,
        comment: comment || null,
        entries: entries.map((entry) => ({
          task_id: entry.task.id,
          batch_id: entry.batchId,
          primary_submission_id: decision === "approve" ? entry.primarySubmissionId : null,
          selected_submission_ids: decision === "approve" ? entry.selectedSubmissionIds : [],
        })),
      });
      const variants = entries.map((entry) => entry.task.task_variant || "图位").join("、");
      setNotice(decision === "approve" ? `人物 ${variants} 定装已审核定版。` : `人物 ${variants} 定装已退回返工。`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "人物定装统一审核失败");
    } finally {
      setLoading(false);
    }
  }

  async function archiveTaskSubmission(task: Task, submissionId: string, archived: boolean) {
    setLoading(true);
    try {
      await api.patch(`/tasks/${task.id}/submissions/${submissionId}/archive`, { archived });
      setNotice(archived ? "候选成果已归档。" : "候选成果已恢复。");
      await refresh();
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        logout();
        setNotice("登录已过期，请重新登录。");
        return;
      }
      setError(err instanceof Error ? err.message : "归档操作失败");
    } finally {
      setLoading(false);
    }
  }

  async function setPrimaryTaskSubmission(task: Task, submissionId: string) {
    setLoading(true);
    try {
      await api.patch(`/tasks/${task.id}/primary-submission`, { submission_id: submissionId });
      setNotice(`任务「${task.title}」的主母版已更新。`);
      await refresh();
    } catch (err) {
      if (err instanceof AuthExpiredError) {
        logout();
        setNotice("登录已过期，请重新登录。");
        return;
      }
      setError(err instanceof Error ? err.message : "主母版更新失败");
    } finally {
      setLoading(false);
    }
  }

  async function assignTask(task: Task, assigneeId: string, reassignmentReason?: string) {
    setLoading(true);
    setError(null);
    try {
      const reassigned = Boolean(task.assignee_id && task.assignee_id !== assigneeId);
      await api.patch<Task>(`/tasks/${task.id}`, {
        assignee_id: assigneeId || null,
        reassignment_reason: reassignmentReason?.trim() || null,
      });
      setNotice(reassigned ? `任务「${task.title}」已改派并通知相关执行人。` : `任务「${task.title}」已更新执行人。`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "任务分配失败");
    } finally {
      setLoading(false);
    }
  }

  async function generateAssetTasks(projectId: string, episodeId: string, scriptVersionId: string) {
    setLoading(true);
    setError(null);
    try {
      const query = new URLSearchParams({ episode_id: episodeId, script_version_id: scriptVersionId });
      const result = await api.post<{ created_tasks: number; assets: number; audio?: { created_tasks?: number; voice_tasks?: number } }>(`/projects/${projectId}/asset-tasks?${query}`);
      const audioSummary = result.audio ? `，其中音频 ${result.audio.created_tasks || 0} 个、角色音色 ${result.audio.voice_tasks || 0} 个` : "";
      setNotice(`已生成资产任务：${result.assets} 个视觉资产、${result.created_tasks} 个新任务${audioSummary}。可继续完成脚本段拆分与确认。`);
      await refresh();
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : "生成资产任务失败");
      return false;
    } finally {
      setLoading(false);
    }
  }

  async function generateStoryboardTasks(projectId: string, episodeId: string, scriptVersionId: string) {
    setLoading(true);
    setError(null);
    try {
      const query = new URLSearchParams({ episode_id: episodeId, script_version_id: scriptVersionId });
      const result = await api.post<{ created_tasks: number; storyboards: number; auto_variant_plans?: number }>(`/projects/${projectId}/storyboard-tasks?${query}`);
      setNotice(`已生成分镜任务：${result.storyboards} 个分镜、${result.created_tasks} 个新任务${result.auto_variant_plans ? `，新增 ${result.auto_variant_plans} 个场景/道具变体建议` : ""}。`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "生成分镜任务失败");
    } finally {
      setLoading(false);
    }
  }

  async function bulkAssignTasks(projectId: string, scope: string, key: string, assigneeId: string, episodeId?: string, reassignmentReason?: string) {
    setLoading(true);
    setError(null);
    try {
      const result = await api.post<{ assigned: number; skipped?: number; skipped_by_status?: Record<string, number> }>(`/projects/${projectId}/task-assignments`, {
        scope,
        key,
        assignee_id: assigneeId || null,
        episode_id: episodeId || null,
        reassignment_reason: reassignmentReason?.trim() || null,
      });
      const skipped = result.skipped ? `，跳过 ${result.skipped} 个已开始、待审核或已完成任务` : "";
      const reassigned = Number((result as { reassigned?: number }).reassigned || 0);
      setNotice(`${reassigned ? `已改派 ${reassigned} 个任务并发送通知；` : ""}已批量分配 ${result.assigned} 个任务${skipped}。`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "批量分配失败");
    } finally {
      setLoading(false);
    }
  }

  const fetchSceneGating = React.useCallback(async (projectId: string): Promise<SceneGating[]> => {
    try {
      return await api.get<SceneGating[]>(`/projects/${projectId}/scene-gating`);
    } catch {
      return [];
    }
  }, []);

  async function createUser(form: NewUserForm) {
    setLoading(true);
    setError(null);
    try {
      await api.post<User>("/users", {
        display_name: form.display_name,
        username: form.phone,
        phone: form.phone,
        role: form.role,
        password: form.password,
      });
      setNotice(`账号「${form.display_name}」已创建。`);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建账号失败");
    } finally {
      setLoading(false);
    }
  }

  // 重置成员密码：后端 POST /users/{id}/reset-password 返回 { user, temporary_password }。
  // 不传 password 时后端按手机号后 6 位生成；返回临时密码供页面弹窗展示给管理员转交。
  async function resetUserPassword(userId: string, displayName: string, password?: string): Promise<string | null> {
    setLoading(true);
    setError(null);
    try {
      const result = await api.post<{ user: User; temporary_password: string }>(
        `/users/${userId}/reset-password`,
        password ? { password } : {},
      );
      setNotice(`账号「${displayName}」的密码已重置。`);
      await refresh();
      return result.temporary_password;
    } catch (err) {
      setError(err instanceof Error ? err.message : "重置密码失败");
      return null;
    } finally {
      setLoading(false);
    }
  }

  // 启用/停用账号：后端 PATCH /users/{id}/status，body { is_active: boolean }，返回更新后的 User。
  // 不新增字段，仅切换现有 User.is_active。
  async function setUserActive(userId: string, displayName: string, isActive: boolean): Promise<boolean> {
    setLoading(true);
    setError(null);
    try {
      await api.patch<User>(`/users/${userId}/status`, { is_active: isActive });
      setNotice(`账号「${displayName}」已${isActive ? "启用" : "停用"}。`);
      await refresh();
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : isActive ? "启用账号失败" : "停用账号失败");
      return false;
    } finally {
      setLoading(false);
    }
  }

  function selectProject(projectId: string, from?: string, module?: ProjectDetailModule) {
    setSelectedProjectId(projectId);
    setProjectEntryMode({ type: "project_overview", projectId, from, module });
    setPageState("projectDetail");
    void refresh(projectId, {
      includeProjectData: false,
      includeTasks: false,
      includeAssets: false,
      // 显式传 includeUsers: true，避免依赖 refresh 内部默认值读取旧 page 导致 NewProjectDetailPage 的成员列表为空
      includeUsers: true,
      projectScopeId: projectId,
    });
  }

  async function createAssetVariantPlan(projectId: string, payload: { asset_id: string; episode_id?: string | null; script_version_id?: string | null; variant_kind: string; title_zh: string; description_zh: string; storyboard_ids: string[]; source?: string; confidence?: number | null }) {
    setLoading(true);
    setError(null);
    try {
      await api.post<AssetVariantPlan>(`/assets/variant-plans?project_id=${projectId}`, payload);
      setNotice("资产变体已创建，关联分镜任务依赖已刷新。");
      await refresh(projectId);
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建资产变体失败");
    } finally {
      setLoading(false);
    }
  }

  async function updateAssetVariantPlan(planId: string, payload: Partial<Pick<AssetVariantPlan, "title_zh" | "description_zh" | "status" | "storyboard_ids">>) {
    setLoading(true);
    setError(null);
    try {
      await api.patch<AssetVariantPlan>(`/assets/variant-plans/${planId}`, payload);
      setNotice("资产变体已更新，任务依赖已同步。");
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : "更新资产变体失败");
    } finally {
      setLoading(false);
    }
  }

  return {
    session,
    page,
    setPage,
    projectEntryMode,
    projects,
    tasks,
    assets,
    scriptSegments,
    storyboards,
    users,
    runs,
    draftOverrides,
    breakdownDraftSyncing,
    loadBreakdownDraft,
    project,
    agentStageMap,
    dashboard,
    workspace,
    reviewWorkspace,
    admin,
    hermesConfig,
    hermesHealth,
    error,
    notice,
    setNotice,
    notificationsOpen,
    notifications,
    setNotificationsOpen,
    loading,
    login,
    clearLoginError,
    logout,
    updateAvatar,
    changePassword,
    refreshHermesStatus,
    refreshProjectData,
    runBreakdownStep,
    retryPromptTaskDistribution,
    createTemporaryAssetProduction,
    createProject,
    updateProject,
    updateProjectSettings,
    importProjectFromFile,
    runProjectAgent,
    materializeAgentRun,
    assignTask,
    generateAssetTasks,
    generateStoryboardTasks,
    createAssetVariantPlan,
    updateAssetVariantPlan,
    assetVariantPlans,
    bulkAssignTasks,
    fetchSceneGating,
    saveBreakdownDraft,
    confirmScriptSegments,
    lockEpisodeBreakdown,
    softDeleteProject,
    uploadTaskCandidates,
    submitTaskBatch,
    submitCharacterTaskUnit,
    deleteTaskDraftSubmission,
    reviewTaskBatch,
    reviewCharacterTaskUnit,
    archiveTaskSubmission,
    setPrimaryTaskSubmission,
    saveTaskPrompt,
    generateTaskPrompt,
    createUser,
    resetUserPassword,
    setUserActive,
    selectProject,
    startNewProject,
    startAddEpisode,
    startAddScriptVersion,
  };
}

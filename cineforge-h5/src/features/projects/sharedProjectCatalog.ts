import type { Project } from "../../types";

/** 总控看板 / 项目卡片共用的展示类型。 */
export type SharedProjectCard = {
  id: string;
  title: string;
  genre: string;
  episodes: number;
  episodeLabel: string;
  coverImage: string;
  status: string;
  statusTone: "blue" | "muted" | "done";
  progress: number;
  isDemo: boolean;
};

const COVER_POOL = [
  "/cineforge-mark.png",
];
// TODO(总控看板-后端接口需求文档-1 需求2): 当前 COVER_POOL 仅 1 张图，所有项目卡片显示同一封面，辨识度差。
// 后端在 Project 上补 cover_image 字段后，coverForProjectId 仅作空值兜底，可保留默认图或扩充封面池。

const HIDDEN_PROJECT_TITLE_PATTERNS = [
  /^ui\s*交互测试$/i,
  /^ui\s*测试项目$/i,
  /^测试\s*\d+$/i,
  /漫剧工具/,
  /^测试项目$/,
];

export function isHiddenDemoProjectTitle(title?: string) {
  const normalized = String(title || "").trim();
  if (!normalized) return false;
  return HIDDEN_PROJECT_TITLE_PATTERNS.some((pattern) => pattern.test(normalized));
}

/**
 * 根据项目 ID hash 从封面池里选一个默认封面。
 * Phase 1 临时方案：后端补上 cover_image 后优先用真实字段。
 */
export function coverForProjectId(projectId: string): string {
  let hash = 0;
  for (let index = 0; index < projectId.length; index += 1) {
    hash = (hash * 31 + projectId.charCodeAt(index)) >>> 0;
  }
  return COVER_POOL[hash % COVER_POOL.length];
}

/**
 * 根据 Project.status + storyboard_count/task_count 推导出展示用的状态和进度。
 * 进度口径：以任务完成率为主，没有任务时用分镜数估算。
 */
// TODO(总控看板-后端接口需求文档-1 需求2): 进度口径前端多口径估算（tasks 完成率 / storyboard_count 估算），
// 与后端实际进度不一致。后端在 Project 上补 progress_percent（0-100，统一口径计算）后，
// 此函数应优先读取 project.progress_percent，仅在缺失时回退到当前推导逻辑。
function liveStatusMeta(
  project: Project,
  tasksByProject: Map<string, { total: number; done: number }>,
): Pick<SharedProjectCard, "status" | "statusTone" | "progress"> {
  const raw = String(project.status || "").toLowerCase();
  const done = raw.includes("complete") || raw.includes("完成") || raw.includes("完结") || raw.includes("archived");
  const inScript = raw.includes("script") || raw.includes("draft") || raw.includes("剧本") || raw.includes("reading");
  const sb = Number(project.storyboard_count || 0);
  const tc = Number(project.task_count || 0);

  if (done) return { status: "已完结", statusTone: "done", progress: 100 };

  // 用 tasks 计算更准确的完成率
  const taskStat = tasksByProject.get(project.id);
  if (taskStat && taskStat.total > 0) {
    const rate = Math.round((taskStat.done / taskStat.total) * 100);
    return {
      status: inScript ? "剧本阶段" : "制作中",
      statusTone: inScript ? "muted" : "blue",
      progress: rate === 100 ? 98 : rate,
    };
  }

  if (inScript) return { status: "剧本阶段", statusTone: "muted", progress: sb <= 0 ? 5 : 15 };
  const progress = sb <= 0 && tc <= 0 ? 8 : Math.min(92, 18 + sb + tc * 2);
  return { status: "制作中", statusTone: "blue", progress };
}

function liveToSharedCard(
  project: Project,
  tasksByProject: Map<string, { total: number; done: number }>,
): SharedProjectCard {
  const meta = liveStatusMeta(project, tasksByProject);
  // TODO(总控看板-后端接口需求文档-1 需求2): 当前用 storyboard_count（分镜数）当集数展示，
  // 可能出现 1024集 这类异常值；缺少「已完成 / 总集数」格式。
  // 后端补 episode_count + completed_episode_count 后，应改为：
  //   episodes = project.episode_count
  //   episodeLabel = `${project.completed_episode_count ?? 0}/${project.episode_count}集`
  const episodes = Number(project.storyboard_count || 0);
  const title = project.title || "未命名项目";
  const rawGenre = String(project.genre || "").trim();
  // TODO(总控看板-后端接口需求文档-1 需求2): cover_image 当前以 `as unknown as` 强转兜底读取，
  // 后端在 Project 类型上补 cover_image 字段后可移除强转，直接用 project.cover_image。
  const coverCandidate = String(
    (project as unknown as { cover_image?: string; coverImage?: string }).cover_image
    || (project as unknown as { coverImage?: string }).coverImage
    || "",
  ).trim();
  return {
    id: project.id,
    title,
    genre: rawGenre && rawGenre !== "未设置" ? rawGenre : "未设置",
    episodes,
    episodeLabel: `${episodes}集`,
    coverImage: coverCandidate || coverForProjectId(project.id),
    status: meta.status,
    statusTone: meta.statusTone,
    progress: meta.progress,
    isDemo: false,
  };
}

/**
 * 构建总控看板展示项目卡片：
 * - 仅使用接口真实项目（移除 FOUNDATION_PROJECTS 硬编码演示项目，避免与真实数据混杂）
 * - 过滤测试项目等噪音
 * - 按创建时间倒序
 */
export function buildSharedProjectCards(
  liveProjects: Project[],
  allTasks: { project_id?: string | null; status?: string }[] = [],
): SharedProjectCard[] {
  // 按 project_id 聚合任务：总数 + 完成数
  const tasksByProject = new Map<string, { total: number; done: number }>();
  for (const task of allTasks) {
    const pid = String(task.project_id || "");
    if (!pid) continue;
    const stat = tasksByProject.get(pid) || { total: 0, done: 0 };
    stat.total += 1;
    const st = String(task.status || "").toLowerCase();
    if (st === "completed" || st === "done" || st.includes("完成")) stat.done += 1;
    tasksByProject.set(pid, stat);
  }

  return [...liveProjects]
    .sort((a, b) => new Date(String(b.created_at || 0)).getTime() - new Date(String(a.created_at || 0)).getTime())
    .map((p) => liveToSharedCard(p, tasksByProject))
    .filter((item) => !isHiddenDemoProjectTitle(item.title));
}

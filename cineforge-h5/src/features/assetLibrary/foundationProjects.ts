import type { Project } from "../../types";

/**
 * 资产库模块项目卡片构建器。
 *
 * 仅基于真实接口数据（`Project` → `SharedProjectCard`）构建，
 * 不再注入演示项目与演示封面兜底。
 */

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

/** 真实环境噪音项目过滤（ui 交互测试、漫剧工具等）。 */
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

/** 真实项目封面：仅读 `Project.cover_image`，无演示兜底。 */
export function resolveProjectCover(project: Project): string | undefined {
  const raw = String(
    (project as unknown as { cover_image?: string; coverImage?: string }).cover_image
      || (project as unknown as { coverImage?: string }).coverImage
      || "",
  ).trim();
  return raw || undefined;
}

function liveStatusMeta(project: Project): Pick<SharedProjectCard, "status" | "statusTone" | "progress"> {
  const raw = String(project.status || "").toLowerCase();
  if (raw.includes("complete") || raw.includes("完成") || raw.includes("完结")) {
    return { status: "已完结", statusTone: "done", progress: 100 };
  }
  if (raw.includes("script") || raw.includes("draft") || raw.includes("剧本")) {
    return { status: "剧本阶段", statusTone: "muted", progress: 20 };
  }
  const episodes = Number(project.storyboard_count || 0);
  const progress = episodes <= 0 ? 8 : Math.min(92, 18 + episodes * 3);
  return { status: "制作中", statusTone: "blue", progress };
}

function liveToSharedCard(project: Project): SharedProjectCard {
  const meta = liveStatusMeta(project);
  const episodes = Number(project.storyboard_count || 0);
  const title = project.title || "未命名项目";
  const rawGenre = String(project.genre || "").trim();
  return {
    id: project.id,
    title,
    genre: rawGenre && rawGenre !== "未设置" ? rawGenre : "未设置",
    episodes,
    episodeLabel: `${episodes}集`,
    coverImage: resolveProjectCover(project) ?? "",
    status: meta.status,
    statusTone: meta.statusTone,
    progress: meta.progress,
    isDemo: false,
  };
}

/**
 * 资产库项目卡片列表：仅基于接口真实项目构建。
 * 1) 按创建时间倒序
 * 2) 过滤 ui 交互测试 / 漫剧工具等噪音项目
 */
export function buildSharedProjectCards(liveProjects: Project[]): SharedProjectCard[] {
  return [...liveProjects]
    .sort((a, b) => new Date(String(b.created_at || 0)).getTime() - new Date(String(a.created_at || 0)).getTime())
    .map(liveToSharedCard)
    .filter((item) => !isHiddenDemoProjectTitle(item.title));
}

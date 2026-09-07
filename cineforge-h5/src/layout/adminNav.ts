import type { PageKey } from "../types";

/**
 * AdminRail 导航项（legacy-ui AdminShell 风格）
 * - icon：lucide-react 图标 key
 * - key: 对应 PageKey；"_ghost" 为占位入口，点击不切换页面
 */
export type AdminRailItem = {
  key: PageKey | "_ghost" | "settings";
  label: string;
  icon:
    | "dashboard"
    | "projects"
    | "create"
    | "review"
    | "assets"
    | "team"
    | "ai"
    | "settings"
    | "file"
    | "list"
    | "clapperboard";
  /** 是否为占位入口（点击时触发 onGhostNav 回调，不切换页面） */
  ghost?: boolean;
};

/**
 * 截图1 新版 AdminRail 主菜单（按用户要求放在最前）
 * - 总控看板 / 项目管理 / 创作中心 / 审批中心：已接入真实页面；
 * - 其余：暂时以 ghost 形式处理，后续阶段再对接真实页面。
 */
export const adminPrimaryNav: AdminRailItem[] = [
  { key: "adminDashboard", label: "总控看板", icon: "dashboard", ghost: false },
  { key: "projectManagement", label: "项目管理", icon: "projects", ghost: false },
  { key: "creationCenter", label: "创作中心", icon: "create", ghost: false },
  { key: "reviewCenter", label: "审批中心", icon: "review", ghost: false },
  { key: "assetLibrary", label: "资产库", icon: "assets", ghost: false },
  { key: "teamMembers", label: "团队成员", icon: "team", ghost: false },
  // { key: "agentAdmin", label: "智能体", icon: "ai", ghost: false },
];

/**
 * 原有菜单保留（扁平展开），图标使用 lucide-react 作为占位展示
 */
export const adminLegacyNav: AdminRailItem[] = [
  // { key: "projectOverview", label: "项目总览", icon: "projects" },
  // { key: "projectDetail", label: "项目详情", icon: "file" },
  // { key: "myTasks", label: "我的任务", icon: "list" },
  // { key: "queryAgent", label: "查询助手", icon: "ai" },
  { key: "canvas", label: "画布工作台", icon: "create" },
  { key: "groupCanvas", label: "任务分组画布", icon: "list" },
];

/** 底部固定菜单：系统设置 */
export const adminBottomNav: AdminRailItem[] = [
  // { key: "systemLogs", label: "系统设置", icon: "settings" },
];

/**
 * 合并后的 AdminRail 菜单：截图1 主菜单在前 + 原有菜单在后 + 底部菜单独立
 */
export function buildAdminRailNav(
  _role?: "admin" | "editor",
): { top: AdminRailItem[]; bottom: AdminRailItem[] } {
  return {
    top: [...adminPrimaryNav, ...adminLegacyNav],
    bottom: adminBottomNav,
  };
}

/**
 * 顶栏页面标题（AdminShell 风格）
 */
export function adminPageTitle(page: PageKey): string {
  switch (page) {
    case "adminDashboard":
      return "总控看板";
    case "projectManagement":
      return "项目管理";
    case "projectOverview":
      return "项目总览";
    case "projectDetail":
      return "项目详情";
    case "myTasks":
      return "待办队列";
    case "reviewCenter":
      return "资产审批";
    case "assets":
      return "资产库";
    case "assetLibrary":
      return "资产库";
    case "queryAgent":
      return "查询助手";
    case "agentAdmin":
      return "智能体";
    case "teamMembers":
      return "团队成员";
    case "canvas":
      return "画布工作台";
    case "workbench":
      return "工作台画布";
    case "groupCanvas":
      return "任务分组画布";
    case "systemLogs":
      return "系统设置";
    default:
      return "项目管理";
  }
}

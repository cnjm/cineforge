import type { NavGroup, PageKey, UserRole } from "../types";
import { dramaPagesForRole } from "../taskAccess";

/** 开发期产线：页眉切换；客户交付时可隐藏电商 */
export type ProductLine = "drama" | "ecommerce";

export const productLineMeta: Record<ProductLine, { label: string; workbench: string; hint: string }> = {
  drama: {
    label: "漫剧",
    workbench: "漫剧工作台",
    hint: "围读 → 成片全控制",
  },
  ecommerce: {
    label: "电商 CUT",
    workbench: "电商 CUT 工作台",
    hint: "场次挖矿 → 待审",
  },
};

/** 电商页面已下线，保留空集合避免 lineForPage/navGroupsForLine 抛错 */
const ecommercePageKeys = new Set<PageKey>([]);

export function lineForPage(page: PageKey): ProductLine {
  return ecommercePageKeys.has(page) ? "ecommerce" : "drama";
}

export function homePageForLine(line: ProductLine, role?: UserRole): PageKey {
  // 电商 CUT 入口已下线，切换到电商线时也回退到漫剧的项目列表
  return "projectOverview";
}

/**
 * IA v2：按「项目对象 → 今日工作 → 审片成片 → 素材 → 设置」组织。
 * 阶段二兼容：保留 projectDetail / queryAgent / canvas 三个老入口，阶段三再决定去留。
 */
export const navGroups: NavGroup[] = [
  {
    title: "项目管理",
    items: [
      { key: "projectOverview", label: "项目总览" },
      { key: "projectDetail", label: "项目详情" },
    ],
  },
  {
    title: "任务中心",
    items: [
      { key: "myTasks", label: "我的任务" },
      { key: "reviewCenter", label: "资产审批", reviewOnly: true },
      { key: "reviewWorkbench", label: "审片台", reviewOnly: true },
    ],
  },
  { title: "内容资产", items: [{ key: "assets", label: "资产库" }] },
  {
    title: "管理中心",
    variant: "submenu",
    items: [{ key: "systemLogs", label: "系统设置" }],
  },
  { title: "Agent", items: [
    { key: "agentAdmin", label: "智能体" },
    { key: "queryAgent", label: "查询助手" }
  ] },
  {
    title: "章节画布",
    items: [{ key: "canvas", label: "画布工作台" }],
  },
];

export function navGroupsForLine(line: ProductLine, role?: UserRole): NavGroup[] {
  const lineAllowed = line === "ecommerce" ? ecommercePageKeys : null;
  const roleAllowed = role && line === "drama" ? dramaPagesForRole(role) : null;
  return navGroups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => {
        if (lineAllowed && !lineAllowed.has(item.key)) return false;
        if (line === "drama" && ecommercePageKeys.has(item.key)) return false;
        if (roleAllowed && !roleAllowed.has(item.key)) return false;
        return true;
      }),
    }))
    .filter((group) => group.items.length > 0);
}

export const pageTitles: Record<PageKey, string> = {
  adminDashboard: "总控看板",
  projectOverview: "项目总览",
  projectDetail: "项目详情",
  myTasks: "我的任务",
  makerTrack: "制作画布",
  taskCanvas: "制作画布",
  groupCanvas: "任务分组画布",
  creationCenter: "创作中心",
  reviewCenter: "分镜审核",
  reviewWorkbench: "审片台",
  assets: "资产库",
  assetLibrary: "资产库",
  agentAdmin: "智能体",
  teamMembers: "团队成员",
  systemLogs: "系统设置",
  queryAgent: "查询助手",
  canvas: "画布工作台",
  workbench: "工作台画布",
};

/**
 * 顶栏仅展示当前页面标题（legacy-ui AdminShell 风格）。
 * 当页面在 pageTitles 中有精确映射时直接使用，否则按 legacy-ui 的分组规则 fallback。
 */
export function adminPageTitle(page: PageKey): string {
  const direct = pageTitles[page];
  if (direct) return direct;
  switch (page) {
    case "reviewCenter":
      return "分镜审核";
    case "taskCanvas":
    case "makerTrack":
      return "制作画布";
    case "groupCanvas":
      return "任务分组画布";
    case "systemLogs":
      return "系统设置";
    default:
      return "项目管理";
  }
}

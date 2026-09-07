import { shortAssetCode } from "./assetCode.ts";
import type { DependencyAsset, PageKey, User, UserRole } from "./types.ts";

export function canAccessReviewCenter(user?: User | null) {
  return user?.role === "director" || user?.role === "admin";
}

export function displayDependencyAssetCodes(codes?: string[] | null) {
  return (codes || []).map((code) => shortAssetCode(code) || "编码异常").join("、");
}

export function dependencyAssetLabel(asset: DependencyAsset) {
  return [asset.asset_code, asset.asset_name].filter(Boolean).join("-") || "未命名资产";
}

export function dependencyAssetTypeLabel(type?: string | null) {
  return ({ character: "人物", scene: "场景", prop: "道具", music: "音乐", voice_profile: "角色音色" } as Record<string, string>)[String(type || "")] || "资产";
}

export function dependencyReasonLabel(asset: DependencyAsset) {
  if (asset.ready) return asset.files.length ? `${asset.files.length} 个正式文件` : "已就绪";
  return ({
    asset_not_found: "资产记录缺失",
    unassigned: "尚未分配",
    production_incomplete: asset.production_status === "rework" ? "已退回，正在返工" : "制作中",
    rejected: "已退回，正在返工",
    rework: "已退回，正在返工",
    rework_required: "已退回，正在返工",
    pending_review: "待导演审核",
    approved_file_missing: "已通过但无有效正式文件",
    not_ready: "尚未就绪",
  } as Record<string, string>)[String(asset.reason || "")] || "尚未就绪";
}

/** 超管 / 项目负责人：总控看板壳层（阶段二仅用于条件判断，AdminShell 组件阶段三再启用） */
export function usesAdminShell(role: UserRole) {
  return role === "admin" || role === "director";
}

/** 登录 / 产线切换后的默认主战场（阶段二暂不启用，保留旧 projectOverview 默认页） */
export function homePageForRole(role: UserRole): PageKey {
  switch (role) {
    case "artist":
      return "groupCanvas";
    case "editor":
      // 默认回到待办队列
      return "myTasks";
    case "director":
    case "admin":
      return "adminDashboard";
    case "script_editor":
      return "projectOverview";
    default:
      return "projectOverview";
  }
}

/** 退出全屏主战场后回到的系统壳页面 */
export function shellPageForRole(role: UserRole): PageKey {
  switch (role) {
    case "artist":
    case "editor":
      return "myTasks";
    case "director":
    case "admin":
      return "adminDashboard";
    case "script_editor":
      return "projectOverview";
    default:
      return "projectOverview";
  }
}

/** 各角色在漫剧线可见的页面白名单（电商线仍走 line 过滤） */
export function dramaPagesForRole(role: UserRole): Set<PageKey> {
  const shared: PageKey[] = ["assets", "queryAgent", "myTasks", "canvas"];
  switch (role) {
    case "artist":
      return new Set<PageKey>([...shared, "groupCanvas"]);
    case "editor":
      // 仅保留与其他非制作角色一致的白名单
      return new Set<PageKey>([...shared]);
    case "director":
    case "admin":
      return new Set<PageKey>([
        "adminDashboard",
        "projectOverview",
        "projectDetail",
        "creationCenter",
        "myTasks",
        "reviewCenter",
        "assets",
        "agentAdmin",
        "systemLogs",
        "queryAgent",
        "canvas",
      ]);
    case "script_editor":
      return new Set<PageKey>(["projectOverview", "projectDetail", "assets", "queryAgent", "myTasks", "canvas"]);
    default:
      return new Set<PageKey>(["projectOverview", "myTasks", "assets", "queryAgent", "canvas"]);
  }
}

/** 角色中文标签 */
export function roleLabel(role: UserRole): string {
  return (
    {
      director: "导演",
      artist: "制作师",
      editor: "剪辑",
      admin: "管理员",
      script_editor: "编剧",
    } as Record<UserRole, string>
  )[role] || role;
}

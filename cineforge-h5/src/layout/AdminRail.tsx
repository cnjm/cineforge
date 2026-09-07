import {
  Bot,
  Box,
  ClipboardCheck,
  Clapperboard,
  FileText,
  Film,
  FolderKanban,
  LayoutDashboard,
  List,
  PanelLeftClose,
  PanelLeftOpen,
  PenTool,
  Settings,
  ShoppingCart,
  Sparkles,
  Users,
  Wrench,
} from "lucide-react";
import type { PageKey } from "../types";
import {
  buildAdminRailNav,
  type AdminRailItem,
} from "./adminNav";

const iconMap = {
  dashboard: LayoutDashboard,
  projects: FolderKanban,
  create: PenTool,
  review: ClipboardCheck,
  assets: Box,
  team: Users,
  ai: Bot,
  settings: Settings,
  file: FileText,
  list: List,
  clapperboard: Clapperboard,
  film: Film,
  tool: Wrench,
  shopping: ShoppingCart,
  sparkles: Sparkles,
} as const;

function NavButton({
  item,
  active,
  collapsed,
  onClick,
}: {
  item: AdminRailItem;
  active: boolean;
  collapsed: boolean;
  onClick: () => void;
}) {
  const Icon = iconMap[item.icon];
  return (
    <button
      type="button"
      className={`admin-nav-item ${active ? "is-active" : ""}`}
      title={collapsed ? item.label : undefined}
      aria-label={item.label}
      aria-current={active ? "page" : undefined}
      onClick={onClick}
    >
      <Icon size={20} strokeWidth={1.8} />
      {!collapsed ? <span>{item.label}</span> : null}
    </button>
  );
}

export type AdminRailProps = {
  page: PageKey;
  setPage: (page: PageKey) => void;
  collapsed: boolean;
  onToggleCollapsed: () => void;
  /** 侧边栏高亮的菜单项（用于控制侧边栏高亮与实际页面解耦） */
  sidebarActivePage?: PageKey;
  /** 点击占位（截图1 新增的）菜单时的回调 */
  onGhostNav?: (item: AdminRailItem) => void;
};

export function AdminRail({
  page,
  setPage,
  collapsed,
  onToggleCollapsed,
  sidebarActivePage,
  onGhostNav,
}: AdminRailProps) {
  const { top, bottom } = buildAdminRailNav();
  const activePage = sidebarActivePage ?? page;

  function handleNav(item: AdminRailItem) {
    if (item.ghost) {
      onGhostNav?.(item);
      return;
    }
    if (item.key === "settings") {
      setPage("systemLogs");
      return;
    }
    setPage(item.key);
  }

  function isActive(item: AdminRailItem) {
    if (item.ghost) return false;
    const k = item.key;
    return k === activePage;
  }

  return (
    <aside className={`admin-rail${collapsed ? " admin-rail--collapsed" : ""}`} aria-label="导航">
      <div className="admin-rail__top">
        <div className="admin-rail__brand">
          <img
            className="admin-rail__logo"
            src={collapsed ? "/cineforge-mark.png" : "/cineforge-logo.png"}
            alt="CineForge"
            draggable={false}
          />
        </div>
        <nav className="admin-rail__nav">
          {top.map((item, idx) => (
            <NavButton
              key={`${item.key}-${idx}`}
              item={item}
              active={isActive(item)}
              collapsed={collapsed}
              onClick={() => handleNav(item)}
            />
          ))}
        </nav>
      </div>
      <div className="admin-rail__bottom">
        {bottom.map((item, idx) => (
          <NavButton
            key={`${item.key}-${idx}`}
            item={item}
            active={isActive(item)}
            collapsed={collapsed}
            onClick={() => handleNav(item)}
          />
        ))}
        <button
          type="button"
          className="admin-rail__toggle"
          onClick={onToggleCollapsed}
          title={collapsed ? "展开导航" : "收起导航"}
          aria-label={collapsed ? "展开导航" : "收起导航"}
        >
          {collapsed ? <PanelLeftOpen size={20} strokeWidth={1.8} /> : <PanelLeftClose size={20} strokeWidth={1.8} />}
          {!collapsed ? <span>收起</span> : null}
        </button>
      </div>
    </aside>
  );
}

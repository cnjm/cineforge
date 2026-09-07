import React from "react";
import type { PageKey, User } from "../types";
import { userDisplayName, userInitial } from "../utils";
import { roleLabel } from "../taskAccess";
import { navGroupsForLine, type ProductLine } from "./appLayout";

type SidebarProps = {
  line: ProductLine;
  page: PageKey;
  setPage: (page: PageKey) => void;
  currentUser: User;
  notificationCount: number;
  canAccessReviewCenter: boolean;
  reviewCount: number;
  collapsed: boolean;
  onToggleCollapsed: () => void;
  onOpenNotifications: () => void;
  onLogout: () => void;
};

function BrandLogo({ className, collapsed }: { className?: string; collapsed?: boolean }) {
  const [broken, setBroken] = React.useState(false);
  if (broken) {
    return (
      <span className={`rf-brand-text ${collapsed ? "rf-brand-text--rail" : ""}`}>
        CineForge<span className="logo-dot" />
      </span>
    );
  }
  return (
    <img
      className={className}
      src="/cineforge-logo.png"
      alt="CineForge"
      onError={() => setBroken(true)}
    />
  );
}

export function Sidebar({
  line,
  page,
  setPage,
  currentUser,
  notificationCount,
  canAccessReviewCenter,
  reviewCount,
  collapsed,
  onToggleCollapsed,
  onOpenNotifications,
  onLogout,
}: SidebarProps) {
  const groups = navGroupsForLine(line, currentUser.role);

  return (
    <aside className={`sidebar ${collapsed ? "is-collapsed" : ""}`} data-line={line}>
      <div className="logo">
        {collapsed ? (
          <div className="logo__rail">
            <BrandLogo className="rf-logo-mark rf-logo-mark--rail" collapsed />
            <button
              className="sidebar-collapse-btn sidebar-collapse-btn--expand"
              type="button"
              title="展开侧栏"
              aria-label="展开侧栏"
              onClick={onToggleCollapsed}
            >
              ›
            </button>
          </div>
        ) : (
          <div className="logo__row">
            <h1 className="rf-brand">
              <BrandLogo className="rf-logo-mark" />
            </h1>
            <button
              className="sidebar-collapse-btn"
              type="button"
              title="收起侧栏"
              aria-label="收起侧栏"
              onClick={onToggleCollapsed}
            >
              «
            </button>
          </div>
        )}
      </div>
      {groups.map((group) => {
        const visibleItems = group.items.filter((item) => !item.reviewOnly || canAccessReviewCenter);
        if (!visibleItems.length) return null;
        const showGroupBadge = Boolean(group.showReviewBadge && reviewCount && canAccessReviewCenter);
        return (
          <div className={`nav-group ${group.variant === "submenu" ? "submenu" : ""}`} key={group.title}>
            {!collapsed ? (
              <div className="nav-label">
                <span>{group.title}</span>
                {showGroupBadge ? <span className="nav-badge nav-badge--group">{reviewCount}</span> : null}
              </div>
            ) : null}
            {visibleItems.map((item) => (
              <button
                className={`nav-item ${page === item.key ? "active" : ""}`}
                key={item.key}
                onClick={() => setPage(item.key)}
                type="button"
                title={item.label}
              >
                <span className="nav-item__label">{collapsed ? item.label.slice(0, 1) : item.label}</span>
                {/* 阶段二：组级角标替代单项角标，避免重复 */}
                {!collapsed && item.badge ? <span className="nav-badge">{item.badge}</span> : null}
              </button>
            ))}
          </div>
        );
      })}
      {!collapsed ? (
        <>
          <button className="notification-entry" onClick={onOpenNotifications} type="button">
            <span>通知中心</span>
            <strong>{notificationCount}</strong>
          </button>
          <div className="user-box">
            <div className="avatar">{userInitial(currentUser)}</div>
            <div>
              <strong>{userDisplayName(currentUser)}</strong>
              <span>{roleLabel(currentUser.role)}</span>
            </div>
            <button className="logout-btn" onClick={onLogout} type="button">
              退出
            </button>
          </div>
        </>
      ) : (
        <button className="sidebar-rail-user" type="button" title={userDisplayName(currentUser)} onClick={onOpenNotifications}>
          {userInitial(currentUser)}
        </button>
      )}
    </aside>
  );
}

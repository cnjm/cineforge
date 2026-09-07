import React from "react";
import { Bell, Check, ChevronLeft, Monitor, Moon, Sun } from "lucide-react";
import type { PageKey, User } from "../types";
import { adminPageTitle } from "./appLayout";
import { AccountMenu } from "./AccountMenu";

type ThemeMode = "dark" | "light" | "system";

type TopLineSwitcherProps = {
  page: PageKey;
  notificationCount: number;
  currentUser: User;
  onOpenNotifications: () => void;
  onLogout: () => void;
  onUpdateAvatar: (avatarUrl: string) => Promise<boolean>;
  onChangePassword: (currentPassword: string, newPassword: string) => Promise<boolean>;
  /** 实际生效主题（system 模式下已解析为 dark/light） */
  theme: "dark" | "light";
  /** 用户选择的主题模式 */
  themeMode: ThemeMode;
  /** 系统当前主题偏好 */
  systemTheme: "dark" | "light";
  /** 切换主题模式 */
  onSelectThemeMode: (mode: ThemeMode) => void;
  /** 自定义标题（如项目名称），替换默认页面标题 */
  customTitle?: string;
  /** 是否显示返回按钮 */
  showBackButton?: boolean;
  /** 返回按钮的点击回调 */
  onBack?: () => void;
};

export function TopLineSwitcher({
  page,
  notificationCount,
  currentUser,
  onOpenNotifications,
  onLogout,
  onUpdateAvatar,
  onChangePassword,
  theme,
  themeMode,
  systemTheme,
  onSelectThemeMode,
  customTitle,
  showBackButton,
  onBack,
}: TopLineSwitcherProps) {
  const pageTitle = customTitle ?? adminPageTitle(page);
  const [themeMenuOpen, setThemeMenuOpen] = React.useState(false);
  const themeMenuRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (themeMenuRef.current && !themeMenuRef.current.contains(e.target as Node)) {
        setThemeMenuOpen(false);
      }
    }
    function handleEscape(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setThemeMenuOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("keydown", handleEscape);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleEscape);
    };
  }, []);

  function selectMode(mode: ThemeMode) {
    onSelectThemeMode(mode);
    setThemeMenuOpen(false);
  }

  return (
    <header className={`admin-topbar admin-topbar--theme-${theme}`} aria-label="顶栏">
      <div className="admin-topbar__left">
        {showBackButton ? (
          <button
            type="button"
            className="admin-topbar__back"
            onClick={onBack}
            aria-label="返回项目列表"
            title="返回项目列表"
          >
            <ChevronLeft size={14} strokeWidth={2.4} />
          </button>
        ) : null}
        <h1 className={`admin-page-title${page === "assetLibrary" ? " is-asset-library" : ""}`}>{pageTitle}</h1>
        <div id="admin-title-extra" className="admin-topbar__extra" />
      </div>
      <div className="admin-topbar__right">
        <button
          type="button"
          className="admin-icon-btn"
          onClick={onOpenNotifications}
          aria-label="通知"
          title="通知"
        >
          <Bell size={18} />
          {notificationCount > 0 ? <i className="admin-icon-btn__dot" aria-hidden /> : null}
        </button>
        <div className={`admin-theme-picker${themeMenuOpen ? " is-open" : ""}`} ref={themeMenuRef}>
          <button
            type="button"
            className="admin-icon-btn admin-theme-toggle"
            onClick={() => setThemeMenuOpen((v) => !v)}
            aria-label="外观模式"
            aria-haspopup="menu"
            aria-expanded={themeMenuOpen}
            title="外观模式"
          >
            {themeMode === "system" ? (
              <Monitor size={18} strokeWidth={1.8} />
            ) : theme === "dark" ? (
              <Moon size={18} strokeWidth={1.8} />
            ) : (
              <Sun size={18} strokeWidth={1.8} />
            )}
          </button>
          {themeMenuOpen ? (
            <div className="admin-theme-menu" role="menu" aria-label="选择外观模式">
              <button
                type="button"
                role="menuitemradio"
                aria-checked={themeMode === "light"}
                className={themeMode === "light" ? "is-active" : ""}
                onClick={() => selectMode("light")}
              >
                <Sun size={18} strokeWidth={1.8} aria-hidden />
                <span>浅色模式</span>
                {themeMode === "light" ? <Check size={17} strokeWidth={2} aria-hidden /> : null}
              </button>
              <button
                type="button"
                role="menuitemradio"
                aria-checked={themeMode === "dark"}
                className={themeMode === "dark" ? "is-active" : ""}
                onClick={() => selectMode("dark")}
              >
                <Moon size={18} strokeWidth={1.8} aria-hidden />
                <span>深色模式</span>
                {themeMode === "dark" ? <Check size={17} strokeWidth={2} aria-hidden /> : null}
              </button>
              <button
                type="button"
                role="menuitemradio"
                aria-checked={themeMode === "system"}
                className={themeMode === "system" ? "is-active" : ""}
                onClick={() => selectMode("system")}
              >
                <Monitor size={18} strokeWidth={1.8} aria-hidden />
                <span className="admin-theme-menu__system">
                  跟随系统 <em>· {systemTheme === "light" ? "浅色" : "深色"}</em>
                </span>
                {themeMode === "system" ? <Check size={17} strokeWidth={2} aria-hidden /> : null}
              </button>
            </div>
          ) : null}
        </div>
        <AccountMenu
          currentUser={currentUser}
          onLogout={onLogout}
          onUpdateAvatar={onUpdateAvatar}
          onChangePassword={onChangePassword}
        />
      </div>
    </header>
  );
}

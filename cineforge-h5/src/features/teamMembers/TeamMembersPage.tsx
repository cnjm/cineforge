import React from "react";
import { ChevronLeft, ChevronRight, MoreHorizontal, Plus, Search, X } from "lucide-react";
import { OutlineSelect } from "../../shared/OutlineSelect";
import { roleLabel } from "../../taskAccess";
import type { AdminOverview, NewUserForm, User, UserRole } from "../../types";
import { userDisplayName, userInitial } from "../../utils";

type TeamMembersPageProps = {
  users: User[];
  admin: AdminOverview | null;
  loading: boolean;
  onCreateUser: (form: NewUserForm) => Promise<void>;
  // 重置密码：返回临时密码（成功）或 null（失败/取消），由页面弹窗展示给管理员转交。
  onResetPassword: (userId: string, displayName: string, password?: string) => Promise<string | null>;
  // 启用/停用账号：返回是否切换成功，成功后由上层 refresh 刷新列表。
  onToggleActive: (userId: string, displayName: string, isActive: boolean) => Promise<boolean>;
};

type KpiKey = "all" | "director" | "artist" | "editor";
type RoleFilter = "all" | UserRole;

const PAGE_SIZE = 10;

const roleOptions: Array<{ value: UserRole; label: string }> = [
  { value: "director", label: "导演" },
  { value: "admin", label: "管理员" },
  { value: "script_editor", label: "编剧 / 剧本编辑" },
  { value: "artist", label: "制作师" },
  { value: "editor", label: "剪辑师" },
];

// TODO(团队成员-后端接口需求文档-需求6): 后端无权限管理接口,实际权限由后端 RBAC 控制。
// 以下为各角色权限范围说明(静态展示,非可配置开关)。后端补 /api/permissions/roles 后改为可配置。
const PERMISSION_ITEMS = [
  { title: "导演全量可见", desc: "项目、审片与成员管理" },
  { title: "制作师只看自己负责的任务", desc: "资产 / 分镜制作范围" },
  { title: "剪辑师只看剪辑任务", desc: "成片过料与下载" },
  { title: "甲方可看项目进度", desc: "只读进度，不可改配置" },
];

function roleTone(role: UserRole): "blue" | "violet" | "warn" | "done" | "cyan" {
  if (role === "director") return "blue";
  if (role === "admin") return "violet";
  if (role === "editor") return "done";
  if (role === "script_editor") return "cyan";
  return "warn";
}

// TODO(团队成员-后端接口需求文档-需求1): 后端 User.role 为单值字段,不支持一人多角色。
// 后端补 User.roles: UserRole[] 后,此处改为 `user.roles || [user.role]` 以支持多角色展示与筛选。
function effectiveRoles(user: User): UserRole[] {
  return [user.role];
}

function matchesKpi(user: User, kpi: KpiKey) {
  const roles = effectiveRoles(user);
  if (kpi === "all") return true;
  if (kpi === "director") return roles.includes("director") || roles.includes("admin");
  return roles.includes(kpi);
}

function buildPageItems(current: number, total: number) {
  if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1);
  const pages = new Set<number>([1, total, current, current - 1, current + 1]);
  if (current <= 3) [2, 3, 4].forEach((n) => pages.add(n));
  if (current >= total - 2) [total - 3, total - 2, total - 1].forEach((n) => pages.add(n));
  const sorted = [...pages].filter((n) => n >= 1 && n <= total).sort((a, b) => a - b);
  const items: Array<number | "ellipsis"> = [];
  for (let i = 0; i < sorted.length; i += 1) {
    if (i > 0 && sorted[i] - sorted[i - 1] > 1) items.push("ellipsis");
    items.push(sorted[i]);
  }
  return items;
}

export function TeamMembersPage({ users, admin, loading, onCreateUser, onResetPassword, onToggleActive }: TeamMembersPageProps) {
  const emptyForm: NewUserForm = {
    display_name: "",
    phone: "",
    role: "artist",
    password: "",
  };
  const [newUser, setNewUser] = React.useState<NewUserForm>(emptyForm);
  const [activeKpi, setActiveKpi] = React.useState<KpiKey>("all");
  const [roleFilter, setRoleFilter] = React.useState<RoleFilter>("all");
  const [search, setSearch] = React.useState("");
  const [showCreate, setShowCreate] = React.useState(false);
  const [page, setPage] = React.useState(1);
  const [openMenuId, setOpenMenuId] = React.useState<string | null>(null);
  // 重置密码成功后展示的临时密码（弹窗），null 表示不展示。
  const [resetResult, setResetResult] = React.useState<{ displayName: string; temporaryPassword: string } | null>(null);
  const [copied, setCopied] = React.useState(false);
  const resolvedPassword = newUser.password || newUser.phone.slice(-6);

  function openCreateModal() {
    setNewUser(emptyForm);
    setShowCreate(true);
  }

  function closeCreateModal() {
    setShowCreate(false);
    setNewUser(emptyForm);
  }

  const directorCount = users.filter((user) => effectiveRoles(user).some((role) => role === "director" || role === "admin")).length;
  const artistCount = users.filter((user) => effectiveRoles(user).includes("artist")).length;
  const editorCount = users.filter((user) => effectiveRoles(user).includes("editor")).length;
  const totalCount = users.length || admin?.stats?.users || 0;

  const kpiItems: Array<{ key: KpiKey; value: string; label: string }> = [
    { key: "all", value: String(totalCount), label: "全部成员" },
    { key: "director", value: String(directorCount), label: "导演 / 管理" },
    { key: "artist", value: String(artistCount), label: "制作师" },
    { key: "editor", value: String(editorCount), label: "剪辑" },
  ];

  const filteredUsers = React.useMemo(() => {
    const query = search.trim().toLowerCase();
    return users.filter((user) => {
      if (!matchesKpi(user, activeKpi)) return false;
      if (roleFilter !== "all" && !effectiveRoles(user).includes(roleFilter)) return false;
      if (!query) return true;
      const haystack = `${userDisplayName(user)} ${user.phone || ""}`.toLowerCase();
      return haystack.includes(query);
    });
  }, [users, activeKpi, roleFilter, search]);

  const totalPages = Math.max(1, Math.ceil(filteredUsers.length / PAGE_SIZE) || 1);
  const currentPage = Math.min(page, totalPages);
  const pageUsers = filteredUsers.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);
  const pageItems = buildPageItems(currentPage, totalPages);

  React.useEffect(() => {
    setPage(1);
  }, [activeKpi, roleFilter, search]);

  React.useEffect(() => {
    function onDocClick() {
      setOpenMenuId(null);
    }
    document.addEventListener("click", onDocClick);
    return () => document.removeEventListener("click", onDocClick);
  }, []);

  React.useEffect(() => {
    if (!showCreate) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") closeCreateModal();
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [showCreate]);

  async function submitNewUser(event: React.FormEvent) {
    event.preventDefault();
    if (!newUser.display_name.trim() || !newUser.phone.trim()) return;
    await onCreateUser({
      ...newUser,
      display_name: newUser.display_name.trim(),
      phone: newUser.phone.trim(),
      password: resolvedPassword,
    });
    closeCreateModal();
  }

  // 重置密码：不可逆操作，先二次确认；成功后弹临时密码弹窗供管理员转交。
  async function handleResetPassword(user: User) {
    setOpenMenuId(null);
    const confirmed = window.confirm(`确定重置「${userDisplayName(user)}」的密码吗？旧密码将立即失效。`);
    if (!confirmed) return;
    const temporaryPassword = await onResetPassword(user.id, userDisplayName(user));
    if (temporaryPassword) {
      setResetResult({ displayName: userDisplayName(user), temporaryPassword });
      setCopied(false);
    }
  }

  // 启用/停用切换：根据当前 is_active 决定下一个状态（is_active === false 视为已停用）。
  async function handleToggleActive(user: User) {
    setOpenMenuId(null);
    const nextActive = user.is_active === false;
    const action = nextActive ? "启用" : "停用";
    const confirmed = window.confirm(`确定${action}「${userDisplayName(user)}」账号吗？`);
    if (!confirmed) return;
    await onToggleActive(user.id, userDisplayName(user), nextActive);
  }

  async function copyTempPassword() {
    if (!resetResult) return;
    try {
      await navigator.clipboard.writeText(resetResult.temporaryPassword);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      // 剪贴板不可用时静默失败，用户可手动选中复制
    }
  }

  React.useEffect(() => {
    if (!resetResult) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") setResetResult(null);
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [resetResult]);

  return (
    <div className="admin-dash admin-team-page">
      <section className="admin-kpi-row" aria-label="成员统计">
        {kpiItems.map((item) => (
          <button
            key={item.key}
            type="button"
            className={`admin-kpi ${activeKpi === item.key ? "is-selected" : ""}`}
            onClick={() => setActiveKpi(item.key)}
          >
            <div className="admin-kpi__top">
              <span>{item.label}</span>
            </div>
            <strong>{item.value}</strong>
          </button>
        ))}
      </section>

      <div className="admin-dash-grid admin-team-grid">
        <section className="admin-team-main">
          <div className="admin-section-head admin-team-head">
            <h2>成员管理</h2>
            <button type="button" className="admin-btn-solid" onClick={openCreateModal}>
              <Plus size={14} strokeWidth={2.5} />
              添加成员
            </button>
          </div>

          <section className="admin-panel admin-team-table-panel">
            <div className="admin-team-toolbar">
              <span className="admin-team-toolbar__count">共{filteredUsers.length}个成员</span>
              <div className="admin-team-toolbar__right">
                <OutlineSelect
                  value={roleFilter}
                  onChange={(value) => setRoleFilter(value as RoleFilter)}
                  ariaLabel="按角色筛选"
                  className="outline-select--team-role"
                  options={[
                    { value: "all", label: "全部" },
                    ...roleOptions,
                  ]}
                />
                <label className="admin-projects-search admin-team-project-search">
                  <Search size={14} strokeWidth={1.8} aria-hidden />
                  <input
                    type="search"
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                    placeholder="搜索姓名或手机号"
                  />
                </label>
              </div>
            </div>

            <div className="admin-team-card-list" role="list">
              {pageUsers.length ? (
                pageUsers.map((user) => (
                  <article
                    className={`admin-team-member-card${user.is_active === false ? " is-inactive" : ""}`}
                    key={user.id}
                    role="listitem"
                  >
                    <div className="admin-team-member-card__avatar">
                      {/* 右上角风格：无头像数据时，以姓名首字替代默认头像。 */}
                      {user.avatar_url ? (
                        <img
                          src={user.avatar_url}
                          alt={`${userDisplayName(user)}的头像`}
                          loading="lazy"
                          onError={(event) => {
                            event.currentTarget.style.display = "none";
                          }}
                        />
                      ) : null}
                      <span aria-hidden>{userInitial(user)}</span>
                    </div>
                    <div className="admin-team-member-card__body">
                      <div className="admin-team-member-card__name-row">
                        <strong>{userDisplayName(user)}</strong>
                        <div className="admin-team-member-card__roles">
                          {user.is_active === false ? (
                            <span className="admin-team-member-card__identity-tag is-inactive-tag">已停用</span>
                          ) : null}
                          {effectiveRoles(user).map((role) => (
                            <span className={`admin-team-member-card__identity-tag is-${role}`} key={role}>
                              {roleLabel(role)}
                            </span>
                          ))}
                        </div>
                      </div>
                      <span>{user.phone || "未绑定手机号"}</span>
                    </div>
                    <div className="admin-team-row-menu">
                      <button
                        type="button"
                        className="admin-team-row-menu__btn"
                        aria-label="更多操作"
                        onClick={(event) => {
                          event.stopPropagation();
                          setOpenMenuId((current) => (current === user.id ? null : user.id));
                        }}
                      >
                        <MoreHorizontal size={16} strokeWidth={1.8} />
                      </button>
                      {openMenuId === user.id ? (
                        <div className="admin-team-row-menu__pop" role="menu">
                          {/* 后端已实现 POST /users/{id}/reset-password（admin.py），无需新增字段。 */}
                          <button type="button" role="menuitem" onClick={() => handleResetPassword(user)}>
                            重置密码
                          </button>
                          {/* TODO(团队成员-后端接口需求文档-需求3): 后端无 PATCH /users/:id 接口,待补齐后移除 disabled 并实现「调整权限」弹窗。 */}
                          <button type="button" role="menuitem" disabled>
                            调整权限
                          </button>
                          {/* 后端已实现 PATCH /users/{id}/status（admin.py），无需新增字段；按钮文案随 is_active 切换。 */}
                          <button type="button" role="menuitem" onClick={() => handleToggleActive(user)}>
                            {user.is_active === false ? "启用账号" : "停用账号"}
                          </button>
                        </div>
                      ) : null}
                    </div>
                  </article>
                ))
              ) : (
                <div className="admin-team-empty">
                  <strong>暂无匹配成员</strong>
                  <p className="admin-panel__desc">
                    {users.length ? "调整筛选或搜索条件后再试。" : "点击右上角「添加成员」新增账号。"}
                  </p>
                </div>
              )}
            </div>

            <div className="admin-team-pager">
              <span>共 {filteredUsers.length} 条数据 · 每页 {PAGE_SIZE} 人</span>
              <div className="admin-team-pager__right">
                <div className="admin-team-pager__nav" aria-label="分页">
                  <button
                    type="button"
                    className="admin-team-pager__btn"
                    disabled={currentPage <= 1}
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                    aria-label="上一页"
                  >
                    <ChevronLeft size={14} strokeWidth={2} />
                  </button>
                  {pageItems.map((item, index) =>
                    item === "ellipsis" ? (
                      <span key={`e-${index}`} className="admin-team-pager__ellipsis">
                        …
                      </span>
                    ) : (
                      <button
                        key={item}
                        type="button"
                        className={`admin-team-pager__page ${currentPage === item ? "is-active" : ""}`}
                        onClick={() => setPage(item)}
                        aria-current={currentPage === item ? "page" : undefined}
                      >
                        {item}
                      </button>
                    ),
                  )}
                  <button
                    type="button"
                    className="admin-team-pager__btn"
                    disabled={currentPage >= totalPages}
                    onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                    aria-label="下一页"
                  >
                    <ChevronRight size={14} strokeWidth={2} />
                  </button>
                </div>
              </div>
            </div>
          </section>
        </section>

        <aside className="admin-side-stack">
          <section className="admin-panel admin-panel--todo">
            <div className="admin-section-head admin-section-head--compact">
              <h2>分组与权限</h2>
            </div>
            <p className="admin-panel__desc">个人权限、分组权限、项目阶段权限</p>
            <ul className="admin-team-perm-list">
              {PERMISSION_ITEMS.map((item) => (
                <li key={item.title}>
                  <div className="admin-team-perm-list__main">
                    <strong>{item.title}</strong>
                    <span>{item.desc}</span>
                  </div>
                  <label className="admin-team-switch" aria-disabled="true">
                    <input type="checkbox" checked readOnly disabled />
                    <i />
                  </label>
                </li>
              ))}
            </ul>
          </section>

          <section className="admin-panel admin-panel--split">
            <h2>角色说明</h2>
            <ul className="admin-record-list admin-team-role-list">
              {roleOptions.map((option) => (
                <li key={option.value}>
                  <div className="admin-record-list__main">
                    <strong>{option.label}</strong>
                    <span>{option.value}</span>
                  </div>
                  <span className={`admin-tag admin-tag--${roleTone(option.value)}`}>
                    {roleLabel(option.value)}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        </aside>
      </div>

      {showCreate ? (
        <div className="admin-team-modal" role="presentation">
          <button type="button" className="admin-team-modal__backdrop" aria-label="关闭弹窗" onClick={closeCreateModal} />
          <section
            className="admin-team-modal__panel"
            role="dialog"
            aria-modal="true"
            aria-labelledby="admin-team-add-title"
            onClick={(event) => event.stopPropagation()}
          >
            <header className="admin-team-modal__head">
              <h3 id="admin-team-add-title">添加成员</h3>
              <button type="button" className="admin-team-modal__close" onClick={closeCreateModal} aria-label="关闭">
                <X size={16} strokeWidth={2} />
              </button>
            </header>
            <form className="admin-team-modal__form" onSubmit={submitNewUser}>
              <label className="admin-team-modal__row">
                <span className="admin-team-modal__label">
                  <i aria-hidden>*</i>姓名
                </span>
                <input
                  placeholder="请输入姓名"
                  required
                  autoFocus
                  value={newUser.display_name}
                  onChange={(event) => setNewUser((current) => ({ ...current, display_name: event.target.value }))}
                />
              </label>
              <label className="admin-team-modal__row">
                <span className="admin-team-modal__label">
                  <i aria-hidden>*</i>手机号
                </span>
                <input
                  inputMode="tel"
                  placeholder="请输入手机号"
                  required
                  value={newUser.phone}
                  onChange={(event) => setNewUser((current) => ({ ...current, phone: event.target.value }))}
                />
              </label>
              <label className="admin-team-modal__row">
                <span className="admin-team-modal__label">
                  <i aria-hidden>*</i>成员权限
                </span>
                <select
                  value={newUser.role}
                  onChange={(event) => setNewUser((current) => ({ ...current, role: event.target.value as UserRole }))}
                  required
                >
                  {roleOptions.map((option) => (
                    <option key={option.value} value={option.value}>
                      {option.label}
                    </option>
                  ))}
                </select>
              </label>
              <label className="admin-team-modal__row">
                <span className="admin-team-modal__label">
                  <i aria-hidden>*</i>初始密码
                </span>
                <input
                  placeholder={newUser.phone ? `默认 ${newUser.phone.slice(-6)}` : "默认手机号后6位"}
                  type="text"
                  value={newUser.password}
                  onChange={(event) => setNewUser((current) => ({ ...current, password: event.target.value }))}
                />
              </label>
              <div className="admin-team-modal__actions">
                <button type="button" className="admin-team-modal__btn" onClick={closeCreateModal}>
                  取消
                </button>
                <button type="submit" className="admin-team-modal__btn admin-team-modal__btn--primary" disabled={loading}>
                  确认
                </button>
              </div>
            </form>
          </section>
        </div>
      ) : null}

      {resetResult ? (
        <div className="admin-team-modal" role="presentation">
          <button
            type="button"
            className="admin-team-modal__backdrop"
            aria-label="关闭弹窗"
            onClick={() => setResetResult(null)}
          />
          <section
            className="admin-team-modal__panel"
            role="dialog"
            aria-modal="true"
            aria-labelledby="admin-team-reset-title"
            onClick={(event) => event.stopPropagation()}
          >
            <header className="admin-team-modal__head">
              <h3 id="admin-team-reset-title">重置密码成功</h3>
              <button
                type="button"
                className="admin-team-modal__close"
                onClick={() => setResetResult(null)}
                aria-label="关闭"
              >
                <X size={16} strokeWidth={2} />
              </button>
            </header>
            <div className="admin-team-modal__form">
              <p className="admin-team-reset-desc">
                账号「<strong>{resetResult.displayName}</strong>」的临时密码已生成，请安全转交给成员并提醒首次登录后修改。
              </p>
              <div className="admin-team-reset-pwd">
                <span className="admin-team-reset-pwd__label">临时密码</span>
                <code className="admin-team-reset-pwd__value">{resetResult.temporaryPassword}</code>
                <button type="button" className="admin-team-modal__btn" onClick={copyTempPassword}>
                  {copied ? "已复制" : "复制"}
                </button>
              </div>
              <div className="admin-team-modal__actions">
                <button
                  type="button"
                  className="admin-team-modal__btn admin-team-modal__btn--primary"
                  onClick={() => setResetResult(null)}
                >
                  我已知晓
                </button>
              </div>
            </div>
          </section>
        </div>
      ) : null}

    </div>
  );
}

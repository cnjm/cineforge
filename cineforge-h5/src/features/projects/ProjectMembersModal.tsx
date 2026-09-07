import { Check, Search, UserPlus, X } from "lucide-react";
import React from "react";
import { memberAvatarUrl } from "../../shared/memberAvatars";
import { OutlineSelect } from "../../shared/OutlineSelect";
import { roleLabel } from "../../taskAccess";
import type { User, UserRole } from "../../types";
import { userDisplayName } from "../../utils";

/**
 * 项目成员管理弹框（UI 移植自 legacy-ui/src/features/projects/ProjectMembersModal.tsx）。
 *
 * 差异（基于 cine-forge 现状）：
 * 1. 后端暂无项目成员读写接口（POST/GET /projects/:id/members），当前用 localStorage 临时持久化，
 *    刷新后仍可见，但仅限本机。后端就绪后切换为接口读写。
 * 2. DEMO_POOL 头像资源沿用 cine-forge 现有 memberAvatarUrl 兜底逻辑。
 */

export type ProjectDuty = "owner" | "director" | "script_editor" | "artist" | "editor";

export type ProjectMemberRecord = {
  userId: string;
  name: string;
  avatar?: string;
  systemRole: UserRole;
  duty: ProjectDuty;
};

type Props = {
  projectId: string;
  projectTitle: string;
  users: User[];
  onClose: () => void;
};

// TODO(项目管理-后端需求1): 后端暂无项目成员接口，当前用 localStorage 持久化。后端就绪后改为接口读写。
const STORAGE_KEY = "cineforge.projectMembers.v1";

const DUTY_OPTIONS: Array<{ value: ProjectDuty; label: string }> = [
  { value: "owner", label: "项目负责人" },
  { value: "director", label: "导演" },
  { value: "script_editor", label: "编剧" },
  { value: "artist", label: "制作师" },
  { value: "editor", label: "剪辑师" },
];

function dutyFromSystemRole(role: UserRole): ProjectDuty {
  if (role === "admin") return "owner";
  if (role === "director") return "director";
  if (role === "script_editor") return "script_editor";
  if (role === "editor") return "editor";
  return "artist";
}

function dutyLabel(duty: ProjectDuty) {
  return DUTY_OPTIONS.find((item) => item.value === duty)?.label || duty;
}

function readStore(): Record<string, ProjectMemberRecord[]> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Record<string, ProjectMemberRecord[]>;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function writeStore(next: Record<string, ProjectMemberRecord[]>) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
  } catch {
    /* ignore */
  }
}

function buildPool(users: User[]) {
  return users.map((user) => ({
    id: user.id,
    name: userDisplayName(user),
    systemRole: user.role,
    avatar: memberAvatarUrl(user, users),
  }));
}

function defaultMembers(
  projectId: string,
  pool: Array<{ id: string; name: string; systemRole: UserRole; avatar?: string }>,
): ProjectMemberRecord[] {
  // 默认放入负责人（管理员/导演）作为初始成员，避免空列表
  const owners = pool.filter((item) => item.systemRole === "admin" || item.systemRole === "director");
  const seed = owners.length ? owners.slice(0, 2) : pool.slice(0, 1);
  return seed.map((item) => ({
    userId: item.id,
    name: item.name,
    avatar: item.avatar,
    systemRole: item.systemRole,
    duty: dutyFromSystemRole(item.systemRole),
  }));
}

export function ProjectMembersModal({ projectId, projectTitle, users, onClose }: Props) {
  const pool = React.useMemo(() => buildPool(users), [users]);
  const [members, setMembers] = React.useState<ProjectMemberRecord[]>(() => {
    const stored = readStore()[projectId];
    if (stored?.length) return stored;
    return defaultMembers(projectId, pool);
  });
  const [query, setQuery] = React.useState("");
  const [notice, setNotice] = React.useState<string | null>(null);

  // TODO(项目管理-后端需求1): 当前变更自动写入 localStorage；后端接口就绪后改为 POST /projects/:id/members。
  React.useEffect(() => {
    const store = readStore();
    writeStore({ ...store, [projectId]: members });
  }, [members, projectId]);

  const memberIds = React.useMemo(() => new Set(members.map((item) => item.userId)), [members]);

  const inviteCandidates = React.useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return pool.filter((item) => {
      if (memberIds.has(item.id)) return false;
      if (!keyword) return true;
      return item.name.toLowerCase().includes(keyword) || roleLabel(item.systemRole).includes(keyword);
    });
  }, [pool, memberIds, query]);

  function invite(candidate: { id: string; name: string; systemRole: UserRole; avatar?: string }) {
    setMembers((current) => [
      ...current,
      {
        userId: candidate.id,
        name: candidate.name,
        avatar: candidate.avatar,
        systemRole: candidate.systemRole,
        duty: dutyFromSystemRole(candidate.systemRole),
      },
    ]);
    setNotice(`已将 ${candidate.name} 加入本项目`);
    window.setTimeout(() => setNotice(null), 1800);
  }

  function removeMember(userId: string) {
    const target = members.find((item) => item.userId === userId);
    if (target?.duty === "owner" && members.filter((item) => item.duty === "owner").length <= 1) {
      setNotice("至少保留一名项目负责人");
      window.setTimeout(() => setNotice(null), 1800);
      return;
    }
    setMembers((current) => current.filter((item) => item.userId !== userId));
  }

  function updateDuty(userId: string, duty: ProjectDuty) {
    setMembers((current) => current.map((item) => (
      item.userId === userId ? { ...item, duty } : item
    )));
  }

  return (
    <div className="project-members" role="presentation">
      <button type="button" className="project-members__backdrop" aria-label="关闭" onClick={onClose} />
      <section className="project-members__panel" role="dialog" aria-modal="true" aria-label={`${projectTitle} 成员管理`} onClick={(event) => event.stopPropagation()}>
        <header className="project-members__head">
          <div>
            <h3>成员管理</h3>
          </div>
          <button type="button" className="project-members__close" onClick={onClose} aria-label="关闭">
            <X size={16} strokeWidth={1.8} />
          </button>
        </header>

        {notice ? <div className="project-members__notice" role="status">{notice}</div> : null}

        <div className="project-members__body">
          <section className="project-members__section">
            <div className="project-members__section-head">
              <strong>{projectTitle}制作成员</strong>
              <span>{members.length} 人</span>
            </div>
            <div className="project-members__list">
              {members.map((member) => (
                <article key={member.userId} className="project-members__row">
                  <div className="project-members__identity">
                    {member.avatar ? <img src={member.avatar} alt="" /> : <span>{member.name.slice(0, 1)}</span>}
                    <div>
                      <strong>{member.name}</strong>
                      <em>团队身份：{roleLabel(member.systemRole)}</em>
                    </div>
                  </div>
                  <OutlineSelect
                    value={member.duty}
                    ariaLabel={`${member.name} 项目职责`}
                    className="outline-select--project-duty"
                    options={DUTY_OPTIONS.map((item) => ({ value: item.value, label: item.label }))}
                    onChange={(value) => updateDuty(member.userId, value as ProjectDuty)}
                  />
                  <button type="button" className="project-members__remove" onClick={() => removeMember(member.userId)}>
                    移除
                  </button>
                </article>
              ))}
              {!members.length ? <div className="project-members__empty">暂无项目成员，请从右侧邀请。</div> : null}
            </div>
          </section>

          <section className="project-members__section">
            <div className="project-members__section-head">
              <strong>从团队邀请</strong>
              <span>可选 {inviteCandidates.length}</span>
            </div>
            <label className="project-members__search">
              <Search size={14} strokeWidth={1.8} aria-hidden="true" />
              <input
                type="search"
                value={query}
                placeholder="搜索姓名或岗位"
                onChange={(event) => setQuery(event.target.value)}
              />
            </label>
            <div className="project-members__invite-list">
              {inviteCandidates.map((candidate) => (
                <button
                  key={candidate.id}
                  type="button"
                  className="project-members__invite-row"
                  onClick={() => invite(candidate)}
                >
                  <div className="project-members__invite-person">
                    {candidate.avatar ? <img src={candidate.avatar} alt="" /> : <span>{candidate.name.slice(0, 1)}</span>}
                    <div>
                      <strong>{candidate.name}</strong>
                      <em>{roleLabel(candidate.systemRole)} · 加入后默认职责：{dutyLabel(dutyFromSystemRole(candidate.systemRole))}</em>
                    </div>
                  </div>
                  <i><UserPlus size={14} strokeWidth={1.8} /></i>
                </button>
              ))}
              {!inviteCandidates.length ? (
                <div className="project-members__empty">
                  {query.trim() ? "没有匹配的可邀请成员" : "团队成员已全部加入本项目"}
                </div>
              ) : null}
            </div>
          </section>
        </div>

        <footer className="project-members__foot">
          <span className="project-members__foot-note">
            <Check size={13} strokeWidth={2.4} aria-hidden="true" />
            变更已自动保存到本项目
          </span>
          <button type="button" className="btn assignment-outline-btn" onClick={onClose}>完成</button>
        </footer>
      </section>
    </div>
  );
}

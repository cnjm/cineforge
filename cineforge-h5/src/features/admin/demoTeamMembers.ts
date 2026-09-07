import type { User, UserRole } from "../../types";

const STORAGE_KEY = "cineforge.demoTeamMembers.v1";

/** 演示团队成员数据（后端不可用或用户列表为空时使用）。 */
export const FOUNDATION_TEAM_USERS: User[] = [
  { id: "demo-team-xie-yang", role: "director", display_name: "谢阳", username: "xieyang", phone: "18152010001" },
  { id: "demo-team-wang-mujun", role: "artist", display_name: "王穆君", username: "wangmujun", phone: "18152010005" },
  { id: "demo-team-hong-fanru", role: "artist", display_name: "洪凡入", username: "hongfanru", phone: "18152010006" },
  { id: "demo-team-xu-chengzhi", role: "artist", display_name: "徐成智", username: "xuchengzhi", phone: "18152010007" },
];

function readCustomTeamUsers(): User[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw) as User[];
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item) => item && typeof item.id === "string" && typeof item.display_name === "string");
  } catch {
    return [];
  }
}

function writeCustomTeamUsers(users: User[]) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(users));
  } catch {
    /* ignore */
  }
}

export function listDemoTeamUsers(): User[] {
  const custom = readCustomTeamUsers();
  const customIds = new Set(custom.map((item) => item.id));
  const customPhones = new Set(custom.map((item) => String(item.phone || "").trim()).filter(Boolean));
  const foundation = FOUNDATION_TEAM_USERS.filter(
    (item) => !customIds.has(item.id) && !customPhones.has(String(item.phone || "").trim()),
  );
  return [...custom, ...foundation];
}

export function addDemoTeamUser(input: {
  display_name: string;
  phone: string;
  role: UserRole;
}): User {
  const user: User = {
    id: `demo-team-${Date.now()}`,
    display_name: input.display_name.trim() || "未命名成员",
    username: input.display_name.trim() || "user",
    phone: input.phone.trim(),
    role: input.role,
  };
  writeCustomTeamUsers([user, ...readCustomTeamUsers()]);
  return user;
}

export function isPreviewOrOfflineUsers(users: User[] | null | undefined) {
  return !users || users.length === 0;
}

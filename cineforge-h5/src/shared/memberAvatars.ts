import type { User } from "../types";
import { userDisplayName } from "../utils";

const DEFAULT_MALE_AVATARS = [
  "/admin-avatar.png",
];
// TODO(总控看板-后端接口需求文档-1 需求4): 当前默认头像池仅 1 张 /admin-avatar.png，
// 所有用户头像回退到同一张图，无法区分用户。
// 后端在 User 上补 avatar_url 字段后，fromUser 分支会优先命中，此默认池仅作空值兜底。

const MEMBER_AVATARS: Record<string, string> = {};

/** 成员头像 URL 解析：优先用用户配置的 avatar_url，再用姓名映射，最后用默认池。 */
export function memberAvatarUrl(user: User, allUsers: User[] = [], options?: { fallback?: boolean }) {
  const name = userDisplayName(user).trim();
  if (MEMBER_AVATARS[name]) return MEMBER_AVATARS[name];
  const fromUser = String(user.avatar_url || "").trim();
  if (fromUser) return fromUser;
  if (options?.fallback === false) return "";
  const index = Math.max(0, allUsers.findIndex((item) => item.id === user.id));
  return DEFAULT_MALE_AVATARS[index % DEFAULT_MALE_AVATARS.length] || "/admin-avatar.png";
}

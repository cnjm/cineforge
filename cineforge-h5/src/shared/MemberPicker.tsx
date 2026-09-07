import React, { useState } from "react";
import { Check } from "lucide-react";
import type { User } from "../types";
import { userDisplayName } from "../utils";
import { memberAvatarUrl } from "./memberAvatars";

export type MemberOption = {
  id: string;
  name: string;
  avatar?: string;
  role?: string;
};

/**
 * 将全局 User 列表转为 MemberPicker 选项。
 * 使用 fallback:false 让无 avatar_url 的用户返回空字符串，
 * 从而触发 MemberPicker 的渐变背景 + 首字 fallback 展示效果。
 */
export function usersToMemberOptions(users: User[]): MemberOption[] {
  return users.map((user) => ({
    id: user.id,
    name: userDisplayName(user),
    avatar: memberAvatarUrl(user, users, { fallback: false }) || undefined,
    role: String(user.role || ""),
  }));
}

function MemberAvatar({ member }: { member: MemberOption }) {
  const [imgFailed, setImgFailed] = useState(false);
  if (member.avatar && !imgFailed) {
    return (
      <img
        src={member.avatar}
        alt=""
        onError={() => setImgFailed(true)}
        style={{ display: "block" }}
      />
    );
  }
  return <span className="member-picker__avatar-fallback">{member.name.slice(0, 1)}</span>;
}

export function MemberPicker({
  title,
  members,
  selectedIds,
  onChange,
  selectionMode = "multiple",
  emptyHint = "暂无成员",
  className = "member-picker",
  itemClassName = "member-picker__item",
}: {
  title: string;
  members: MemberOption[];
  selectedIds: string[];
  onChange: (ids: string[]) => void;
  selectionMode?: "single" | "multiple";
  emptyHint?: string;
  className?: string;
  itemClassName?: string;
}) {
  function toggle(id: string) {
    if (selectionMode === "single") {
      onChange(selectedIds.includes(id) ? [] : [id]);
      return;
    }
    if (selectedIds.includes(id)) {
      onChange(selectedIds.filter((item) => item !== id));
      return;
    }
    onChange([...selectedIds, id]);
  }

  return (
    <div className={className}>
      <div className="member-picker__head project-settings__member-head">
        <strong>{title}</strong>
      </div>
      {members.length ? (
        <div className="member-picker__list project-settings__member-list">
          {members.map((member) => {
            const checked = selectedIds.includes(member.id);
            return (
              <button
                key={member.id}
                type="button"
                className={`${itemClassName} ${checked ? "is-checked" : ""}`}
                onClick={() => toggle(member.id)}
                aria-pressed={checked}
              >
                <MemberAvatar member={member} />
                <span className="project-settings__member-name member-picker__name">{member.name}</span>
                <i className="project-settings__member-check member-picker__check" aria-hidden="true">
                  {checked ? <Check size={11} strokeWidth={2.8} /> : null}
                </i>
              </button>
            );
          })}
        </div>
      ) : (
        <p className="member-picker__empty">{emptyHint}</p>
      )}
    </div>
  );
}

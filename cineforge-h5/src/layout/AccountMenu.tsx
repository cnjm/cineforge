import React from "react";
import { createPortal } from "react-dom";
import {
  Check,
  ChevronDown,
  Eye,
  EyeOff,
  ImageUp,
  KeyRound,
  LogOut,
  Upload,
  X,
} from "lucide-react";
import { memberAvatarUrl } from "../shared/memberAvatars";
import type { User } from "../types";
import { userDisplayName, userInitial } from "../utils";

type AccountMenuProps = {
  currentUser: User;
  onLogout: () => void;
  onUpdateAvatar: (avatarUrl: string) => Promise<boolean>;
  onChangePassword: (currentPassword: string, newPassword: string) => Promise<{ ok: boolean; error?: string }>;
};

type AccountDialog = "avatar" | "password" | null;

async function avatarDataUrl(file: File) {
  if (!file.type.startsWith("image/")) throw new Error("请选择图片文件");
  if (file.size > 8 * 1024 * 1024) throw new Error("图片不能超过 8MB");

  const bitmap = await createImageBitmap(file);
  const size = 320;
  const scale = Math.max(size / bitmap.width, size / bitmap.height);
  const width = bitmap.width * scale;
  const height = bitmap.height * scale;
  const canvas = document.createElement("canvas");
  canvas.width = size;
  canvas.height = size;
  const context = canvas.getContext("2d");
  if (!context) throw new Error("图片处理失败，请重新选择");
  context.fillStyle = "#1A1A1A";
  context.fillRect(0, 0, size, size);
  context.drawImage(bitmap, (size - width) / 2, (size - height) / 2, width, height);
  bitmap.close();
  return canvas.toDataURL("image/jpeg", 0.88);
}

export function AccountMenu({ currentUser, onLogout, onUpdateAvatar, onChangePassword }: AccountMenuProps) {
  const [open, setOpen] = React.useState(false);
  const [dialog, setDialog] = React.useState<AccountDialog>(null);
  const [avatarDraft, setAvatarDraft] = React.useState("");
  const [avatarError, setAvatarError] = React.useState("");
  const [currentPassword, setCurrentPassword] = React.useState("");
  const [newPassword, setNewPassword] = React.useState("");
  const [confirmPassword, setConfirmPassword] = React.useState("");
  const [passwordError, setPasswordError] = React.useState("");
  const [showCurrentPassword, setShowCurrentPassword] = React.useState(false);
  const [showNewPassword, setShowNewPassword] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const menuRef = React.useRef<HTMLDivElement>(null);
  const fileRef = React.useRef<HTMLInputElement>(null);
  const currentAvatar = memberAvatarUrl(currentUser, [currentUser], { fallback: false });
  const visibleAvatar = avatarDraft || currentAvatar;
  const name = userDisplayName(currentUser);

  function closeDialog() {
    setDialog(null);
    setAvatarDraft("");
    setAvatarError("");
    setCurrentPassword("");
    setNewPassword("");
    setConfirmPassword("");
    setPasswordError("");
    setShowCurrentPassword(false);
    setShowNewPassword(false);
  }

  React.useEffect(() => {
    if (!open) return;
    function onPointerDown(event: MouseEvent) {
      if (!menuRef.current?.contains(event.target as Node)) setOpen(false);
    }
    window.addEventListener("mousedown", onPointerDown);
    return () => window.removeEventListener("mousedown", onPointerDown);
  }, [open]);

  React.useEffect(() => {
    if (!dialog) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape" && !saving) closeDialog();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [dialog, saving]);

  function openDialog(next: Exclude<AccountDialog, null>) {
    setOpen(false);
    setDialog(next);
  }

  async function selectAvatar(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    setAvatarError("");
    try {
      setAvatarDraft(await avatarDataUrl(file));
    } catch (error) {
      setAvatarError(error instanceof Error ? error.message : "图片读取失败");
    }
  }

  async function saveAvatar() {
    if (!avatarDraft || saving) return;
    setSaving(true);
    setAvatarError("");
    const saved = await onUpdateAvatar(avatarDraft);
    setSaving(false);
    if (saved) closeDialog();
    else setAvatarError("头像保存失败，请重试");
  }

  async function savePassword(event: React.FormEvent) {
    event.preventDefault();
    if (saving) return;
    if (newPassword.length < 6) {
      setPasswordError("新密码至少需要 6 位");
      return;
    }
    if (newPassword !== confirmPassword) {
      setPasswordError("两次输入的新密码不一致");
      return;
    }
    if (currentPassword === newPassword) {
      setPasswordError("新密码不能与当前密码相同");
      return;
    }
    setSaving(true);
    setPasswordError("");
    const result = await onChangePassword(currentPassword, newPassword);
    setSaving(false);
    if (result.ok) closeDialog();
    else setPasswordError(result.error || "当前密码不正确，或密码修改失败");
  }

  return (
    <>
      <div className="admin-account" ref={menuRef}>
        <button
          type="button"
          className={`admin-user-chip ${open ? "is-open" : ""}`}
          aria-haspopup="menu"
          aria-expanded={open}
          onClick={() => setOpen((value) => !value)}
        >
          <span className="admin-user-chip__avatar-wrap">
            {currentAvatar ? (
              <img className="admin-user-chip__avatar" src={currentAvatar} alt="" />
            ) : (
              <span className="admin-user-chip__fallback" aria-hidden>{userInitial(currentUser)}</span>
            )}
          </span>
          <span className="admin-user-chip__name">{name}</span>
          <ChevronDown className="admin-user-chip__chevron" size={14} strokeWidth={1.8} aria-hidden />
        </button>

        {open ? (
          <div className="admin-account-menu" role="menu">
            <div className="admin-account-menu__identity">
              <strong>{name}</strong>
              <span>{currentUser.phone || "未绑定手机号"}</span>
            </div>
            <button type="button" role="menuitem" onClick={() => openDialog("avatar")}>
              <ImageUp size={16} strokeWidth={1.8} aria-hidden />
              修改头像
            </button>
            <button type="button" role="menuitem" onClick={() => openDialog("password")}>
              <KeyRound size={16} strokeWidth={1.8} aria-hidden />
              修改密码
            </button>
            <div className="admin-account-menu__divider" />
            <button type="button" className="is-danger" role="menuitem" onClick={onLogout}>
              <LogOut size={16} strokeWidth={1.8} aria-hidden />
              退出登录
            </button>
          </div>
        ) : null}
      </div>

      {dialog === "avatar" ? createPortal(
        <div className="admin-account-modal" role="presentation" onMouseDown={(event) => {
          if (event.target === event.currentTarget && !saving) closeDialog();
        }}>
          <section className="admin-account-dialog" role="dialog" aria-modal="true" aria-labelledby="account-avatar-title">
            <header>
              <div>
                <h2 id="account-avatar-title">修改头像</h2>
                <p>支持 JPG、PNG，文件不超过 8MB</p>
              </div>
              <button type="button" className="admin-account-dialog__close" aria-label="关闭" onClick={closeDialog} disabled={saving}>
                <X size={18} strokeWidth={1.8} />
              </button>
            </header>
            <div className="admin-avatar-editor">
              <div className="admin-avatar-editor__preview">
                {visibleAvatar ? <img src={visibleAvatar} alt="头像预览" /> : <span>{userInitial(currentUser)}</span>}
              </div>
              <button type="button" className="admin-account-secondary" onClick={() => fileRef.current?.click()} disabled={saving}>
                <Upload size={16} strokeWidth={1.8} />
                选择图片
              </button>
              <input ref={fileRef} className="admin-account-file" type="file" accept="image/jpeg,image/png,image/webp" onChange={selectAvatar} />
              {avatarError ? <p className="admin-account-error">{avatarError}</p> : null}
            </div>
            <footer>
              <button type="button" className="admin-account-secondary" onClick={closeDialog} disabled={saving}>取消</button>
              <button type="button" className="admin-account-primary" onClick={saveAvatar} disabled={!avatarDraft || saving}>
                {saving ? "保存中" : "保存"}
              </button>
            </footer>
          </section>
        </div>,
        document.body,
      ) : null}

      {dialog === "password" ? createPortal(
        <div className="admin-account-modal" role="presentation" onMouseDown={(event) => {
          if (event.target === event.currentTarget && !saving) closeDialog();
        }}>
          <section className="admin-account-dialog" role="dialog" aria-modal="true" aria-labelledby="account-password-title">
            <header>
              <div>
                <h2 id="account-password-title">修改密码</h2>
                <p>新密码至少 6 位，修改后请使用新密码登录</p>
              </div>
              <button type="button" className="admin-account-dialog__close" aria-label="关闭" onClick={closeDialog} disabled={saving}>
                <X size={18} strokeWidth={1.8} />
              </button>
            </header>
            <form className="admin-password-form" onSubmit={savePassword}>
              <label>
                <span>当前密码</span>
                <div className="admin-password-input">
                  <input autoComplete="current-password" type={showCurrentPassword ? "text" : "password"} value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} placeholder="请输入当前密码" />
                  <button type="button" aria-label={showCurrentPassword ? "隐藏密码" : "显示密码"} onClick={() => setShowCurrentPassword((value) => !value)}>
                    {showCurrentPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                  </button>
                </div>
              </label>
              <label>
                <span>新密码</span>
                <div className="admin-password-input">
                  <input autoComplete="new-password" minLength={6} type={showNewPassword ? "text" : "password"} value={newPassword} onChange={(event) => setNewPassword(event.target.value)} placeholder="请输入至少 6 位密码" />
                  <button type="button" aria-label={showNewPassword ? "隐藏密码" : "显示密码"} onClick={() => setShowNewPassword((value) => !value)}>
                    {showNewPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                  </button>
                </div>
              </label>
              <label>
                <span>确认新密码</span>
                <div className="admin-password-input">
                  <input autoComplete="new-password" type={showNewPassword ? "text" : "password"} value={confirmPassword} onChange={(event) => setConfirmPassword(event.target.value)} placeholder="再次输入新密码" />
                  {confirmPassword && confirmPassword === newPassword ? <Check className="admin-password-input__valid" size={16} aria-hidden /> : null}
                </div>
              </label>
              {passwordError ? <p className="admin-account-error">{passwordError}</p> : null}
              <footer>
                <button type="button" className="admin-account-secondary" onClick={closeDialog} disabled={saving}>取消</button>
                <button type="submit" className="admin-account-primary" disabled={!currentPassword || newPassword.length < 6 || !confirmPassword || saving}>
                  {saving ? "修改中" : "确认修改"}
                </button>
              </footer>
            </form>
          </section>
        </div>,
        document.body,
      ) : null}
    </>
  );
}

import React from "react";

/**
 * 通用确认弹框（视觉与 ProjectSettingsModal 面板保持一致：同背景/圆角/边框/按钮，深浅主题自适应）。
 * 用于保存前二次确认、删除前确认等场景。
 *
 * 放置于 .admin-app 作用域内，通过 .confirm-modal 专用类名控样式。
 */
export function ConfirmModal({
  title,
  description,
  confirmText = "确认修改",
  cancelText = "取消",
  onConfirm,
  onClose,
  confirmDisabled = false,
  confirming = false,
  tone = "info",
}: {
  title: string;
  description: React.ReactNode;
  confirmText?: string;
  cancelText?: string;
  onConfirm: () => void | Promise<void>;
  onClose: () => void;
  confirmDisabled?: boolean;
  confirming?: boolean;
  /** 强调色，预留：目前只有 info（中性），将来可加 danger。 */
  tone?: "info" | "danger";
}) {
  const [working, setWorking] = React.useState(false);
  const disabled = confirmDisabled || confirming || working;
  React.useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && !disabled) onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, disabled]);

  async function handleConfirm() {
    if (disabled) return;
    setWorking(true);
    try {
      await onConfirm();
    } finally {
      setWorking(false);
    }
  }

  const role = tone === "danger" ? "alertdialog" : "dialog";
  return (
    <div
      className="modal-backdrop confirm-modal-backdrop"
      role="presentation"
      onClick={onClose}
    >
      <section
        className={`confirm-modal confirm-modal--${tone}`}
        role={role}
        aria-modal="true"
        aria-label={title}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="confirm-modal__body">
          <h4 className="confirm-modal__title">{title}</h4>
          <p className="confirm-modal__desc">{description}</p>
        </div>
        <footer className="confirm-modal__foot">
          <button
            type="button"
            className="project-settings__cancel"
            onClick={onClose}
            disabled={disabled}
          >
            {cancelText}
          </button>
          <button
            type="button"
            className="project-settings__submit primary"
            onClick={handleConfirm}
            disabled={disabled}
          >
            {confirming || working ? `${confirmText}中…` : confirmText}
          </button>
        </footer>
      </section>
    </div>
  );
}

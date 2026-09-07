import React from "react";
import { X } from "lucide-react";

type LongTextPreviewProps = {
  title: string;
  text?: string | null;
  emptyText?: string;
  maxChars?: number;
};

type LongTextTextareaProps = {
  title: string;
  value: string;
  disabled?: boolean;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
};

export function LongTextPreview({ title, text, emptyText = "暂无内容", maxChars = 120 }: LongTextPreviewProps) {
  const value = String(text || "").trim();
  if (!value) return <p className="long-text-empty">{emptyText}</p>;
  const truncated = value.length > maxChars;
  return (
    <div className="long-text-preview">
      <p>{truncated ? `${value.slice(0, maxChars).trim()}...` : value}</p>
      {truncated ? <LongTextButton title={title} text={value} /> : null}
    </div>
  );
}

export function LongTextTextarea({ title, value, disabled, onChange, placeholder, className }: LongTextTextareaProps) {
  const text = String(value || "");
  return (
    <div className="long-text-editor">
      <textarea className={className} disabled={disabled} value={text} placeholder={placeholder} onChange={(event) => onChange(event.target.value)} />
      {text.trim() ? (
        <div className="long-text-editor-actions">
          <LongTextButton title={title} text={text} />
        </div>
      ) : null}
    </div>
  );
}

export function LongTextButton({
  title,
  text,
  label = "查看全文",
  className = "",
  modalClassName = "",
}: {
  title: string;
  text: string;
  label?: string;
  className?: string;
  modalClassName?: string;
}) {
  const [open, setOpen] = React.useState(false);
  React.useEffect(() => {
    if (!open) return undefined;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);
  return (
    <>
      <button className={`text-link-btn ${className}`.trim()} type="button" onClick={() => setOpen(true)}>{label}</button>
      {open ? (
        <div className="modal-backdrop episode-script-modal-backdrop" role="presentation" onClick={() => setOpen(false)}>
          <section
            className={`long-text-modal episode-script-modal ${modalClassName}`.trim()}
            role="dialog"
            aria-modal="true"
            aria-label={title}
            onClick={(event) => event.stopPropagation()}
          >
            <div className="modal-head episode-script-modal__head">
              <div>
                <h3>{title}</h3>
                <p>{text.length} 字符</p>
              </div>
              <button className="episode-script-modal__close" type="button" aria-label="关闭" title="关闭" onClick={() => setOpen(false)}>
                <X size={18} strokeWidth={1.8} />
              </button>
            </div>
            <pre className="long-text-modal-body episode-script-modal__body">{text}</pre>
          </section>
        </div>
      ) : null}
    </>
  );
}

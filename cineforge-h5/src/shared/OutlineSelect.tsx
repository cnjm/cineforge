import React from "react";

/**
 * 描边下拉选择器（UI 移植自 legacy-ui/src/shared/OutlineSelect.tsx）。
 * 用于 ProjectMembersModal 的成员职责选择等场景。
 */

export type OutlineSelectOption = {
  value: string;
  label: string;
  /** 名字头像文案（通常取姓名首字）；无图片时回退 */
  avatarText?: string;
  /** 团队成员头像图片 */
  avatarUrl?: string;
};

type OutlineSelectProps = {
  value: string;
  options: OutlineSelectOption[];
  disabled?: boolean;
  onChange: (value: string) => void;
  ariaLabel?: string;
  className?: string;
  placeholder?: string;
};

function OptionAvatar({ text, url }: { text?: string; url?: string }) {
  if (!text && !url) return null;
  return (
    <span className={`outline-select__avatar ${url ? "has-image" : ""}`} aria-hidden="true">
      {text ? <em>{text}</em> : null}
      {url ? (
        <img
          src={url}
          alt=""
          loading="lazy"
          onError={(event) => {
            event.currentTarget.style.display = "none";
          }}
        />
      ) : null}
    </span>
  );
}

export function OutlineSelect({
  value,
  options,
  disabled = false,
  onChange,
  ariaLabel,
  className,
  placeholder = "未分配",
}: OutlineSelectProps) {
  const [open, setOpen] = React.useState(false);
  const rootRef = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    if (!open) return;
    function onPointerDown(event: MouseEvent) {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    }
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") setOpen(false);
    }
    window.addEventListener("mousedown", onPointerDown);
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("mousedown", onPointerDown);
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  const selected = options.find((option) => option.value === value);
  const label = selected?.label || placeholder;
  const hasAvatarOptions = options.some((option) => option.avatarText || option.avatarUrl);

  return (
    <div
      className={`outline-select ${hasAvatarOptions ? "outline-select--with-avatar" : ""} ${open ? "is-open" : ""} ${disabled ? "is-disabled" : ""} ${className || ""}`}
      ref={rootRef}
    >
      <button
        type="button"
        className="outline-select__trigger"
        disabled={disabled}
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={ariaLabel}
        title={label}
        onClick={() => {
          if (!disabled) setOpen((current) => !current);
        }}
      >
        <span className={`outline-select__value ${selected ? "" : "is-placeholder"}`}>
          <OptionAvatar text={selected?.avatarText} url={selected?.avatarUrl} />
          <em>{label}</em>
        </span>
        <svg className="outline-select__caret" width="12" height="12" viewBox="0 0 12 12" fill="none" aria-hidden>
          <path d="M3 4.5L6 7.5L9 4.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      {open ? (
        <div className="outline-select__menu" role="listbox" aria-label={ariaLabel}>
          {options.map((option) => {
            const active = option.value === value;
            return (
              <button
                key={option.value || "__empty"}
                type="button"
                role="option"
                aria-selected={active}
                className={`outline-select__option ${active ? "is-active" : ""}`}
                title={option.label}
                onClick={() => {
                  onChange(option.value);
                  setOpen(false);
                }}
              >
                <OptionAvatar text={option.avatarText} url={option.avatarUrl} />
                <span>{option.label}</span>
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

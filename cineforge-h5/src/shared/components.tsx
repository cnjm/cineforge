import React from "react";
import { AlertTriangle, Check, Circle, LoaderCircle, LockKeyhole } from "lucide-react";
import { isActiveSubmission } from "../submissionLifecycle";
import type { Asset, Task } from "../types";

export function PageHeader({ title, desc, action }: { title: string; desc: string; action?: React.ReactNode }) {
  return (
    <div className="page-header">
      <div>
        <h2>{title}</h2>
        <p>{desc}</p>
      </div>
      {action ? <div className="top-actions">{action}</div> : null}
    </div>
  );
}

type ButtonVariant = "default" | "primary" | "danger" | "mint";

export function Button({
  variant = "default",
  children,
  className = "",
  ...rest
}: { variant?: ButtonVariant } & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const variantClass = variant === "default" ? "" : variant;
  return (
    <button className={`btn ${variantClass} ${className}`.trim()} type="button" {...rest}>
      {children}
    </button>
  );
}

export function StageActionBar({ children }: { children: React.ReactNode }) {
  return <div className="stage-actions">{children}</div>;
}

export type StepStatus = "pending" | "locked" | "ready" | "running" | "needs_review" | "confirmed" | "succeeded" | "failed" | "done" | "current";

export type StepperStep = {
  key: string;
  label: string;
  status?: StepStatus;
  onRun?: () => void;
  onConfirm?: () => void;
  onSelect?: () => void;
  onSecondaryRun?: () => void;
  runLabel?: string;
  secondaryRunLabel?: string;
  runVariant?: "start" | "confirm";
  statusLabel?: string;
  description?: string;
  disabled?: boolean;
  confirmDisabled?: boolean;
  selected?: boolean;
  customActions?: React.ReactNode;
};

function StepIcon({ status, index }: { status: StepStatus; index: number }) {
  if (status === "locked") return <LockKeyhole size={14} strokeWidth={2} />;
  if (status === "running") return <LoaderCircle className="pipeline-node-spinner" size={16} strokeWidth={2.2} />;
  if (status === "succeeded" || status === "done" || status === "confirmed") return <Check size={17} strokeWidth={2.6} />;
  if (status === "failed") return <AlertTriangle size={16} strokeWidth={2.2} />;
  if (status === "ready" || status === "current" || status === "needs_review") return <span>{index + 1}</span>;
  return <Circle size={13} strokeWidth={1.8} />;
}

export function Stepper({ steps, readOnly = false }: { steps: StepperStep[]; readOnly?: boolean }) {
  return (
    <div className="pipeline-stepper">
      {steps.map((step, index) => {
        const status = step.status ?? "pending";
        const previousStatus = index > 0 ? steps[index - 1]?.status : undefined;
        const linkFilled = previousStatus === "succeeded" || previousStatus === "done" || previousStatus === "confirmed";
        const canView = Boolean(step.onSelect && ["succeeded", "done", "confirmed"].includes(status));
        const canRun = Boolean(step.onRun && ["ready", "current", "failed", "needs_review"].includes(status));
        const isConfirmAction = step.runVariant === "confirm" || (step.runLabel && step.runLabel.includes("确认"));
        return (
          <React.Fragment key={step.key}>
            {index > 0 ? <div className={`pipeline-link ${linkFilled ? "filled" : ""}`} /> : null}
            <div className={`pipeline-step pipeline-step--${step.key} ${status} ${step.selected ? "selected" : ""}`} aria-current={status === "running" || status === "ready" || status === "current" ? "step" : undefined}>
              <div className="pipeline-node"><StepIcon status={status} index={index} /></div>
              <div className="pipeline-step-body">
                <strong>{step.label}</strong>
                <span className="pipeline-status-label">{step.statusLabel || status}</span>
                {step.description ? <span className="pipeline-step-description">{step.description}</span> : null}
                <div className="pipeline-step-actions">
                  {step.customActions ? step.customActions : (
                    <>
                      {canView ? (
                        <button className="pipeline-view" type="button" disabled={step.selected} onClick={step.onSelect}>
                          {step.selected ? "查看中" : "查看结果"}
                        </button>
                      ) : null}
                      {canRun ? (
                        <button
                          className={`pipeline-run ${isConfirmAction ? "is-confirm" : "is-start"}`}
                          type="button"
                          disabled={readOnly || step.disabled}
                          onClick={step.onRun}
                        >
                          {status === "failed" ? "重新运行" : step.runLabel ?? "开始"}
                        </button>
                      ) : null}
                      {step.onSecondaryRun && ["succeeded", "done"].includes(status) ? (
                        <button
                          className="pipeline-run is-regen"
                          type="button"
                          disabled={readOnly || step.disabled}
                          onClick={step.onSecondaryRun}
                        >
                          {step.secondaryRunLabel || "重新生成"}
                        </button>
                      ) : null}
                    </>
                  )}
                </div>
              </div>
            </div>
          </React.Fragment>
        );
      })}
    </div>
  );
}

export function StatsGrid({ items }: { items: Array<[string, string | number, string, string]> }) {
  return (
    <div className="stats-grid">
      {items.map(([label, value, change, tone]) => (
        <div className="stat-card" key={label}>
          <div className="stat-icon">{iconFor(label)}</div>
          <div className="stat-value">{value}</div>
          <div className="stat-label">{label}</div>
          <div className={`stat-change ${tone}`}>{change}</div>
        </div>
      ))}
    </div>
  );
}

export function Panel({ title, subtitle, action, children }: { title: string; subtitle?: string; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <div className="panel-title">{title}</div>
          {subtitle ? <div className="panel-subtitle">{subtitle}</div> : null}
        </div>
        {action ? <div className="panel-action">{action}</div> : null}
      </div>
      <div className="panel-body">{children}</div>
    </section>
  );
}

export function Field({ label, full, required, children }: { label: React.ReactNode; full?: boolean; required?: boolean; children: React.ReactNode }) {
  return (
    <label className={`field ${full ? "full" : ""}`}>
      <span className="field-label">{required ? <b className="required-mark" aria-hidden="true">*</b> : null}{label}</span>
      {children}
    </label>
  );
}

export function FlowLine({ steps }: { steps: Array<[string, string]> }) {
  return (
    <div className="flow-line">
      {steps.map(([title, desc], index) => (
        <div className="flow-step" key={title}>
          <div className="flow-index">{index + 1}</div>
          <strong>{title}</strong>
          <span>{desc}</span>
        </div>
      ))}
    </div>
  );
}

export function MediaStrip({ assets, tasks, onClick }: { assets: Asset[]; tasks: Task[]; onClick?: () => void }) {
  const submittedTasks = tasks.filter((task) => Array.isArray(task.submissions) && task.submissions.some(isActiveSubmission));
  const items = [
    ...assets.slice(0, 6).map((asset) => ({
      id: asset.id,
      title: asset.name,
      subtitle: `${asset.asset_type} / v${asset.version}`,
      mediaType: asset.asset_type || "",
    })),
    ...submittedTasks.slice(0, Math.max(0, 6 - assets.length)).map((task) => ({
      id: task.id,
      title: task.title,
      subtitle: `${task.task_type} / ${task.status}`,
      mediaType: task.task_type || "",
    })),
  ];
  return (
    <div className="media-strip">
      {items.map((item) => (
        <div className="media-card" key={item.id} onClick={onClick}>
          <div className={`thumb ${item.mediaType.includes("video") || item.mediaType === "final_video" ? "video" : item.mediaType === "scene" ? "scene" : item.mediaType === "prop" ? "prop" : ""}`}>
            {item.mediaType.includes("video") || item.mediaType === "final_video" ? <span className="play">PLAY</span> : null}
          </div>
          <div className="media-card-body">
            <strong>{item.title}</strong>
            <span className="muted">{item.subtitle}</span>
          </div>
        </div>
      ))}
      {!items.length ? <div className="empty-hint">暂无完工媒体或资产。</div> : null}
    </div>
  );
}

export function Chat({ who, user, children }: { who: string; user?: boolean; children: React.ReactNode }) {
  return (
    <div className={`msg ${user ? "user" : ""}`}>
      <div className="msg-avatar">{who}</div>
      <div className="bubble">{children}</div>
    </div>
  );
}

function iconFor(label: string) {
  const map: Record<string, string> = {
    项目: "PRJ",
    总完成率: "%",
    分镜: "CUT",
    任务: "TSK",
    风险: "!",
    分集: "EP",
    图片: "IMG",
    视频: "VID",
    评分: "SC",
  };
  return map[label] ?? "RF";
}

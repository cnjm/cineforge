import React from "react";
import { Panel } from "../../shared/components";
import type { AgentRun, WorkflowProgressNode } from "../../types";

type WorkflowProgressPanelProps = {
  run?: AgentRun;
};

function formatDuration(seconds?: number | null): string {
  const total = Math.max(0, Math.round(Number(seconds) || 0));
  if (total <= 0) return "0秒";
  if (total < 60) return `${total}秒`;
  const minutes = Math.floor(total / 60);
  const rest = total % 60;
  return rest ? `${minutes}分${rest}秒` : `${minutes}分`;
}

// 运行中的节点用 started_at 客户端实时计算耗时（后端仅在节点切换时写进度，
// 长步骤运行期间不会更新，因此这里前端每秒 tick 补足实时跳秒）。
function nodeLiveDuration(node: WorkflowProgressNode, now: number): number {
  if (node.status === "running" && node.started_at) {
    const start = new Date(node.started_at).getTime();
    if (!Number.isNaN(start)) return Math.max(0, (now - start) / 1000);
  }
  return node.duration_seconds ?? 0;
}

export function WorkflowProgressPanel({ run }: WorkflowProgressPanelProps) {
  const isRunning = ["queued", "running"].includes(String(run?.status || ""));
  const [now, setNow] = React.useState(() => Date.now());

  React.useEffect(() => {
    if (!isRunning) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [isRunning]);

  if (!run) return null;

  const progress = run.output?.progress;
  if (!progress) return null;

  const {
    percent = 0,
    current_label = "",
    message = "",
    nodes = [],
    phase = "",
  } = progress;
  const workflowStatus = String((run.output?.workflow as Record<string, unknown> | undefined)?.status || "");
  const isFailed = run.status === "failed" || workflowStatus === "failed";
  const isSucceeded = run.status === "succeeded" && workflowStatus !== "needs_review";
  const needsReview = workflowStatus === "needs_review";

  const statusTone = isFailed ? "error" : needsReview ? "warning" : isSucceeded ? "success" : "info";
  const statusText = isFailed ? "失败" : needsReview ? "需人工复核" : isSucceeded ? "完成" : "运行中";

  // 实时总耗时 = 各已开始节点的实时耗时之和（运行中节点用 started_at 计算）
  const liveElapsed = nodes.reduce((sum, node) => sum + (node.started_at ? nodeLiveDuration(node, now) : 0), 0);
  const runningNode = nodes.find((node) => node.status === "running");
  const currentLive = runningNode ? nodeLiveDuration(runningNode, now) : 0;
  const completedCount = nodes.filter((n) => ["succeeded", "failed", "failed_non_blocking", "skipped", "needs_review"].includes(n.status)).length;

  return (
    <Panel
      title="Agent 工作流进度"
      subtitle={`${percent}% · ${current_label || statusText} · 总耗时 ${formatDuration(liveElapsed)}`}
      action={
        <span className={`badge ${statusTone}`}>
          {statusText}
        </span>
      }
    >
      <div className="workflow-progress-container">
        {message ? (
          <div className="workflow-message">
            <span className="workflow-message-icon">{isRunning ? "⏳" : isFailed ? "❌" : needsReview ? "⚠️" : "✅"}</span>
            <span>{message}</span>
            {isRunning && currentLive > 0 ? (
              <span className="workflow-message-timer">当前步骤已运行 {formatDuration(currentLive)}</span>
            ) : null}
          </div>
        ) : null}

        {phase ? (
          <div className="workflow-phase">
            <strong>执行阶段：</strong>
            {phase === "script_segmentation" ? "脚本段拆分" : phase.startsWith("single:") ? "正式分步执行" : phase}
          </div>
        ) : null}

        <div className="workflow-progress-bar">
          <div className="progress-track">
            <div
              className={`progress-fill ${statusTone}`}
              style={{ width: `${Math.min(percent, 100)}%` }}
            />
          </div>
          <div className="progress-label">{percent}%</div>
        </div>

        <div className="workflow-timing-summary">
          <span>总耗时：<strong>{formatDuration(liveElapsed)}</strong></span>
          <span>已完成：<strong>{completedCount}/{nodes.length}</strong> 步</span>
        </div>

        <div className="workflow-nodes-grid">
          {nodes.map((node) => (
            <NodeStatusCard key={node.key} node={node} now={now} />
          ))}
        </div>
      </div>
    </Panel>
  );
}

function NodeStatusCard({ node, now }: { node: WorkflowProgressNode; now: number }) {
  const { status } = node;
  const label = workflowNodeDisplayLabel(node.key, node.label);
  const isPending = status === "pending";
  const isRunning = status === "running";
  const isSucceeded = status === "succeeded";
  const isFailed = status === "failed";
  const isSkipped = status === "skipped";
  const isNonBlockingFail = status === "failed_non_blocking";

  const statusIcon = isPending
    ? "⏸️"
    : isRunning
    ? "⏳"
    : isSucceeded
    ? "✅"
    : isFailed
    ? "❌"
    : isNonBlockingFail
    ? "⚠️"
    : isSkipped
    ? "⏭️"
    : "•";

  const statusClass = isPending
    ? "pending"
    : isRunning
    ? "running"
    : isSucceeded
    ? "succeeded"
    : isFailed
    ? "failed"
    : isNonBlockingFail
    ? "warning"
    : isSkipped
    ? "skipped"
    : "unknown";

  const statusText = isPending
    ? "待执行"
    : isRunning
    ? "执行中"
    : isSucceeded
    ? "已完成"
    : isFailed
    ? "失败"
    : isNonBlockingFail
    ? "部分失败"
    : isSkipped
    ? "已跳过"
    : status;

  const duration = nodeLiveDuration(node, now);
  const showDuration = !isPending && (isRunning || isSucceeded || isFailed || isNonBlockingFail);

  return (
    <div className={`workflow-node-card ${statusClass}`}>
      <div className="node-icon">{statusIcon}</div>
      <div className="node-content">
        <div className="node-label">{label}</div>
        <div className="node-status-text">{statusText}</div>
        {showDuration ? (
          <div className={`node-duration ${isRunning ? "running" : ""}`}>
            {isRunning ? "已运行 " : "耗时 "}
            {formatDuration(duration)}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function workflowNodeDisplayLabel(key: string, fallback: string) {
  if (key === "run_asset_extract") return "资产拆分（asset-extract）";
  if (key === "run_asset_costume_design") return "角色定装提示词（text-to-image-prompt）";
  return fallback;
}

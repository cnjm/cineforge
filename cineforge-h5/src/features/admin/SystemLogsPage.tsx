import { PageHeader, Panel } from "../../shared/components";
import type { AdminOverview, AgentRun } from "../../types";
import { labelAgentType } from "../../utils";

type SystemLogsPageProps = {
  runs: AgentRun[];
  admin: AdminOverview | null;
};

export function SystemLogsPage({ runs, admin }: SystemLogsPageProps) {
  const rows = runs.slice(0, 10).map((run) => ["后端记录", labelAgentType(run.agent_type), run.status]);
  return (
    <div className="system-settings-page">
      <PageHeader title="系统操作日志" desc="记录剧本拆分、资产锁定、Agent 写入、成品上传、下载和资产更新等可追溯动作。" />
      <Panel title="操作日志" subtitle="当前展示近期业务动作，后续接完整审计表">
        <div className="log-list">
          {rows.map(([time, user, action], index) => (
            <div className="log-row" key={`${time}-${action}-${index}`}>
              <span>{time}</span>
              <strong>{user}</strong>
              <p>{action}</p>
            </div>
          ))}
          {!rows.length ? <div className="empty-hint">暂无真实操作日志或 Agent 运行记录。</div> : null}
        </div>
      </Panel>
      <Panel title="系统统计" subtitle="来自后端管理概览和 Agent 运行记录">
        <div className="metric-list">
          {Object.entries(admin?.stats ?? {}).map(([key, value]) => (
            <div key={key}>
              <span>{key}</span>
              <strong>{value}</strong>
            </div>
          ))}
          {!admin?.stats ? <div className="empty-hint">暂无后端统计数据。</div> : null}
        </div>
      </Panel>
    </div>
  );
}

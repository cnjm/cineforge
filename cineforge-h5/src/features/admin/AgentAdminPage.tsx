import type { AgentRun, HermesAuthPrompt, HermesConfig, HermesHealth } from "../../types";
import { labelAgentType } from "../../utils";

type AgentAdminPageProps = {
  runs: AgentRun[];
  hermesConfig: HermesConfig | null;
  hermesHealth: HermesHealth | null;
  loading: boolean;
  onRefreshHermes: (showNotice?: boolean) => Promise<void>;
};

type StatusTone = "ok" | "bad" | "warn" | "blue" | "muted";

export function AgentAdminPage({ runs, hermesConfig, hermesHealth, loading, onRefreshHermes }: AgentAdminPageProps) {
  const host = window.location.hostname || "localhost";
  const dashboardUrl = hermesConfig?.dashboard_url || `http://${host}:9119`;
  const webuiUrl = hermesConfig?.webui_url || `http://${host}:3000`;
  const hermesStatus = hermesHealth?.status || "未检查";
  const hermesTone: StatusTone = hermesStatus === "ok"
    ? "ok"
    : hermesStatus === "unreachable" || hermesStatus === "auth_failed"
      ? "bad"
      : "warn";
  const authPrompt = hermesAuthPrompt(hermesConfig, hermesHealth);
  const recentRuns = runs.length
    ? runs.slice(0, 6).map((run) => ({
        name: labelAgentType(run.agent_type),
        status: run.status,
        desc: run.output
          ? "已有输出，可进入运行记录查看结果"
          : run.status === "failed"
            ? "执行失败，等待管理员排查"
            : "任务已提交到 Agent 编排层",
        lastRun: run.duration_ms ? `${run.duration_ms}ms` : "刚刚",
        tone: (run.status === "succeeded" ? "ok" : run.status === "failed" ? "bad" : "warn") as StatusTone,
      }))
    : [];

  const connections: Array<{ name: string; status: string; desc: string; tone: StatusTone }> = [
    {
      name: "Hermes",
      status: hermesStatus,
      desc: `${authPrompt.title} / 模型：${hermesConfig?.model || "hermes-agent"}`,
      tone: hermesTone,
    },
    {
      name: "Open WebUI",
      status: hermesConfig?.webui_url ? "已配置" : "待配置",
      desc: "提示词调试 / 对话测试 / 管理员入口",
      tone: hermesConfig?.webui_url ? "blue" : "warn",
    },
    {
      name: "CineForge API",
      status: "已联通",
      desc: "项目、任务、资产和反馈数据",
      tone: "ok",
    },
    {
      name: "训练样本库",
      status: "未接入",
      desc: "等待训练样本接口返回真实数据",
      tone: "warn",
    },
  ];

  const pipeline = [
    ["剧本阅读", "script-reading"],
    ["剧本拆解", "script-breakdown"],
    ["合理性审查", "content-compliance-review"],
    ["图文视频提示词", "text-to-image / image-to-video"],
    ["查询助手", "项目上下文检索"],
  ] as const;

  return (
    <div className="agent-admin-page">
      <section className="agent-admin-hero-card">
        <div className="agent-admin-hero-card__copy">
          <span className="agent-admin-chip is-violet">业务侧 Agent 管理</span>
          <h2>Agent 运行、训练样本、反馈闭环和测试工作台</h2>
          <p>
            管理中心只暴露业务生产相关能力。Hermes 与 Open WebUI 作为外部工具入口展示，容器、镜像、日志卷等运维能力不嵌入 CineForge 前端。
          </p>
        </div>
        <div className="agent-admin-actions">
          <a className="admin-btn-solid" href={dashboardUrl} target="_blank" rel="noreferrer">
            打开 Hermes Dashboard
          </a>
          <a className="admin-btn-ghost" href={webuiUrl} target="_blank" rel="noreferrer">
            打开 Open WebUI
          </a>
          <button className="admin-btn-ghost" type="button" disabled={loading} onClick={() => void onRefreshHermes()}>
            重新检查授权
          </button>
        </div>
      </section>

      <section className={`agent-admin-auth-card is-${authPrompt.tone}`}>
        <div className="agent-admin-auth-card__copy">
          <span className={`agent-admin-chip is-${authPrompt.tone}`}>Hermes 授权</span>
          <h3>{authPrompt.title}</h3>
          <p>{authPrompt.desc}</p>
          {hermesHealth?.models_error || hermesHealth?.error ? (
            <code>{hermesHealth.models_error || hermesHealth.error}</code>
          ) : null}
        </div>
        <div className="agent-admin-actions">
          <a className="admin-btn-solid" href={dashboardUrl} target="_blank" rel="noreferrer">
            {authPrompt.action}
          </a>
          <a className="admin-btn-ghost" href={webuiUrl} target="_blank" rel="noreferrer">
            打开 Open WebUI
          </a>
          <button className="admin-btn-ghost" type="button" disabled={loading} onClick={() => void onRefreshHermes()}>
            已授权，刷新状态
          </button>
        </div>
      </section>

      <section className="agent-admin-grid">
        <section className="agent-admin-panel">
          <header>
            <div>
              <h3>连接状态</h3>
              <p>展示业务依赖可用性，不暴露 Docker / Portainer 运维入口</p>
            </div>
          </header>
          <div className="agent-admin-connection-list">
            {connections.map((item) => (
              <article className="agent-admin-connection" key={item.name}>
                <div>
                  <strong>{item.name}</strong>
                  <span>{item.desc}</span>
                </div>
                <em className={`agent-admin-status is-${item.tone}`}>{item.status}</em>
              </article>
            ))}
          </div>
        </section>

        <section className="agent-admin-panel">
          <header>
            <div>
              <h3>Agent 运行</h3>
              <p>按业务链路查看当前 Agent 状态和最近运行</p>
            </div>
          </header>
          <div className="agent-admin-run-list">
            {recentRuns.map((agent) => (
              <article className="agent-admin-run" key={`${agent.name}-${agent.lastRun}`}>
                <i className={`is-${agent.tone}`} aria-hidden="true">{agent.name.slice(0, 1)}</i>
                <div>
                  <strong>{agent.name}</strong>
                  <span>{agent.desc}</span>
                </div>
                <em className={`agent-admin-status is-${agent.tone}`}>{agent.status}</em>
                <small>{agent.lastRun}</small>
              </article>
            ))}
            {!recentRuns.length ? <div className="agent-admin-empty">暂无 Agent 运行记录</div> : null}
          </div>
        </section>
      </section>

      <section className="agent-admin-grid is-compact">
        <section className="agent-admin-panel">
          <header>
            <div>
              <h3>训练样本</h3>
              <p>从真实生产动作沉淀，不直接改写已锁定项目数据</p>
            </div>
          </header>
          <div className="agent-admin-empty">训练样本列表暂未接入前端数据源</div>
        </section>
        <section className="agent-admin-panel">
          <header>
            <div>
              <h3>反馈闭环</h3>
              <p>从人工修正到 Agent 复训的业务闭环</p>
            </div>
          </header>
          <div className="agent-admin-empty">暂无真实反馈记录</div>
        </section>
      </section>

      <section className="agent-admin-panel">
        <header>
          <div>
            <h3>Agent 边界</h3>
            <p>业务 Agent 管理中心只处理生产链路能力</p>
          </div>
        </header>
        <ol className="agent-admin-pipeline">
          {pipeline.map(([title, desc], index) => (
            <li key={title}>
              <em>{index + 1}</em>
              <div>
                <strong>{title}</strong>
                <span>{desc}</span>
              </div>
            </li>
          ))}
        </ol>
      </section>
    </div>
  );
}

function hermesAuthPrompt(config: HermesConfig | null, health: HermesHealth | null): HermesAuthPrompt & { tone: StatusTone } {
  if (health?.status === "ok") {
    return {
      tone: "ok",
      title: "授权正常",
      desc: "CineForge 已能通过当前 API Key 访问 Hermes 模型列表，Agent 链路可以继续调用。",
      action: "打开 Hermes Dashboard",
    };
  }

  if (health?.status === "auth_failed") {
    return {
      tone: "bad",
      title: "需要完成 Hermes 授权",
      desc: "Hermes 已响应健康检查，但模型接口返回 401。请在 Hermes Dashboard 或 Open WebUI 中登录/确认授权，并检查服务器 HERMES_API_KEY 与 Hermes API_SERVER_KEY 是否一致。",
      action: "去 Hermes 授权",
    };
  }

  if (config && !config.api_key_configured) {
    return {
      tone: "warn",
      title: "仍在使用默认 API Key",
      desc: "当前配置看起来还是默认占位 Key。生产或共享环境需要在 .env 中设置 HERMES_API_KEY，并与 Hermes 的 API_SERVER_KEY 保持一致。",
      action: "打开 Hermes Dashboard",
    };
  }

  if (health?.status === "unreachable") {
    return {
      tone: "bad",
      title: "无法连接 Hermes",
      desc: "CineForge 后端暂时连不上 Hermes 健康地址。请先确认 Hermes 服务已启动，再回到这里重新检查授权状态。",
      action: "打开 Hermes Dashboard",
    };
  }

  return {
    tone: "warn",
    title: "等待授权检查",
    desc: "尚未拿到完整的 Hermes 授权状态。管理员可以打开 Hermes 或 Open WebUI 完成登录授权，再刷新检查。",
    action: "打开 Hermes Dashboard",
  };
}

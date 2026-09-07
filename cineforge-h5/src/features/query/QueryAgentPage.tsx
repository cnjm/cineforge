import React from "react";
import { api } from "../../api";
import { Chat, PageHeader, Panel } from "../../shared/components";
import type { Asset, Project, QueryAgentResponse, Task } from "../../types";

type QueryAgentPageProps = {
  project?: Project;
  tasks: Task[];
  assets: Asset[];
};

export function QueryAgentPage({ project, tasks, assets }: QueryAgentPageProps) {
  const [question, setQuestion] = React.useState("查一下当前项目现在最影响进度的内容。");
  const [answer, setAnswer] = React.useState<QueryAgentResponse | null>(null);
  const [querying, setQuerying] = React.useState(false);
  const [error, setError] = React.useState("");

  async function ask(nextQuestion = question) {
    const text = nextQuestion.trim();
    if (!project || !text) return;
    setQuestion(text);
    setQuerying(true);
    setError("");
    try {
      const result = await api.post<QueryAgentResponse>(`/projects/${project.id}/project-query`, {
        question: text,
        limit: 6,
      });
      setAnswer(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : "查询失败");
    } finally {
      setQuerying(false);
    }
  }

  const quickQuestions = [
    "当前项目有哪些逾期或阻塞任务？",
    "列出已经上传结果的任务和使用模型。",
    "当前项目的分镜和脚本原文段有哪些关键内容？",
    "查询资产、角色或道具相关信息。",
    "哪些提示词或提交物可以作为训练样本？",
  ];

  return (
    <>
      <PageHeader title="查询助手" desc="项目上下文检索已接入，按个人权限查询项目、脚本原文段、分镜、任务、提交物和提示词。" />
      <section className="chat-layout">
        <div className="chat-box">
          <Chat who="AI">
            当前项目：{project?.title ?? "未选择项目"}。我会检索你有权限访问的项目、脚本原文段、分镜、任务、提交物和提示词上下文。
          </Chat>
          <div className="query-compose">
            <textarea value={question} onChange={(event) => setQuestion(event.target.value)} placeholder="输入你想查询的项目问题" />
            <button className="btn primary" disabled={!project || querying} onClick={() => ask()}>
              {querying ? "查询中" : "查询"}
            </button>
          </div>
          {error ? <div className="notice error">{error}</div> : null}
          {answer ? (
            <>
              <Chat who="导" user>{answer.question}</Chat>
              <Chat who="AI">
                <pre className="query-answer">{answer.answer}</pre>
              </Chat>
              <Panel title="引用来源" subtitle={`检索文档 ${answer.document_count} 条 / 模式 ${String(answer.retrieval.mode || "local_vector")}`}>
                <div className="query-reference-list">
                  {answer.references.map((reference) => (
                    <article className="query-reference" key={`${reference.source_type}-${reference.source_id}`}>
                      <div>
                        <strong>{reference.title}</strong>
                        <span>{reference.source_type} / 相关度 {reference.score}</span>
                      </div>
                      <p>{reference.excerpt}</p>
                    </article>
                  ))}
                </div>
              </Panel>
            </>
          ) : (
            <Chat who="AI">当前有 {tasks.length || 0} 个任务、{assets.length || 0} 个资产。点击右侧快捷问题或直接输入问题开始查询。</Chat>
          )}
        </div>
        <aside className="quick-panel">
          {quickQuestions.map((item) => (
            <button className="btn" disabled={!project || querying} key={item} onClick={() => ask(item)}>{item}</button>
          ))}
        </aside>
      </section>
    </>
  );
}

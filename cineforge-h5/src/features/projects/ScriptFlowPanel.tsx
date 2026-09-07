import { Panel } from "../../shared/components";
import { activeSubmissions } from "../../submissionLifecycle";
import type { ScriptSegment, Storyboard, Task } from "../../types";
import {
  joinScriptOriginalText,
  scriptSegmentEpisode,
  segmentDisplayTitle,
  sourceContext,
  visibleSegments,
} from "../../utils";

export function ScriptFlowPanel({
  scriptSegments,
  storyboards,
  tasks,
}: {
  scriptSegments: ScriptSegment[];
  storyboards: Storyboard[];
  tasks: Task[];
}) {
  const segments = visibleSegments(scriptSegments);
  const hasApiData = scriptSegments.length > 0;

  function storyboardsForSegment(segment: ScriptSegment) {
    const matched = storyboards.filter((storyboard) => storyboard.script_segment_id === segment.id);
    if (matched.length || hasApiData) return matched;
    return [];
  }

  function tasksForSegment(segment: ScriptSegment, matchedStoryboards: Storyboard[]) {
    const storyboardIds = new Set(matchedStoryboards.map((storyboard) => storyboard.id));
    return tasks.filter((task) => task.script_segment_id === segment.id || (task.storyboard_id && storyboardIds.has(task.storyboard_id)));
  }

  return (
    <Panel
      title="项目数据流转"
      subtitle="按脚本原文段串起原文上下文、分镜、任务和提交物；Agent 输出后可在剧本拆解流程中确认定稿。"
    >
      {!hasApiData ? <div className="empty-hint">当前项目暂无脚本原文段数据。请先运行剧本拆解 Agent，并在运行记录中点击“写入项目数据”。</div> : null}
      {hasApiData ? (
        <div className="script-original-panel">
          <div className="draft-detail-title">
            <strong>脚本原文段（合并显示）</strong>
            <span className="muted">{segments.length} 个原文段，已按顺序合并。</span>
          </div>
          <pre>{joinScriptOriginalText(segments) || "暂无脚本原文。"}</pre>
        </div>
      ) : null}
      <div className="script-flow-list">
        {segments.map((segment) => {
          const context = sourceContext(segment);
          const matchedStoryboards = storyboardsForSegment(segment);
          const matchedTasks = tasksForSegment(segment, matchedStoryboards);
          const submissions = matchedTasks.reduce((sum, task) => sum + activeSubmissions(task.submissions || []).length, 0);
          return (
            <article className="script-flow-card" key={segment.id}>
              <div className="script-flow-head">
                <div>
                  <span className="flow-code">{scriptSegmentEpisode(segment)} / {segment.script_segment_code}</span>
                  <h3>{segmentDisplayTitle(segment)}</h3>
                  <p>{segment.summary || segment.source_text}</p>
                </div>
                <div className="flow-counts">
                  <span><strong>{matchedStoryboards.length}</strong> 分镜</span>
                  <span><strong>{matchedTasks.length}</strong> 任务</span>
                  <span><strong>{submissions}</strong> 提交物</span>
                </div>
              </div>
              <div className="flow-tags">
                <span>功能：{segment.story_function || "未明确"}</span>
                <span>情绪：{segment.dominant_emotion || "未明确"}</span>
                <span>节奏：{segment.rhythm || "未明确"}</span>
                <span>视角：{segment.viewpoint || "未明确"}</span>
              </div>
              <div className="source-context-grid">
                <div>
                  <strong>上文</strong>
                  <p>{context.before || "未提供"}</p>
                </div>
                <div className="current">
                  <strong>原文依据</strong>
                  <p>{context.current || segment.source_text || "未提供"}</p>
                </div>
                <div>
                  <strong>下文</strong>
                  <p>{context.after || "未提供"}</p>
                </div>
              </div>
              <div className="linked-board">
                {!matchedStoryboards.length ? <div className="empty-hint">该脚本原文段暂无分镜，请先完成拆解写入或人工补充分镜。</div> : null}
                {matchedStoryboards.slice(0, 4).map((storyboard) => (
                  <div className="linked-shot" key={storyboard.id}>
                    <strong>{storyboard.title || `分镜 ${storyboard.order_num}`}</strong>
                    <span>{storyboard.description}</span>
                    <em>{storyboard.camera || storyboard.status || "待处理"}</em>
                  </div>
                ))}
              </div>
            </article>
          );
        })}
      </div>
    </Panel>
  );
}

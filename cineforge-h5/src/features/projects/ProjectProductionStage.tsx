import { MediaStrip, Panel, StatsGrid } from "../../shared/components";
import type { Asset, DashboardSummary, PageKey, ScriptSegment, Storyboard, Task } from "../../types";
import { assetReferenceDisplayLabel, scriptSegmentEpisode, segmentDisplayTitle, visibleSegments } from "../../utils";
import { productionProgressSummary } from "../../projectProgress";

type ProjectProductionStageProps = {
  dashboard: DashboardSummary | null;
  scriptSegments: ScriptSegment[];
  storyboards: Storyboard[];
  tasks: Task[];
  assets: Asset[];
  jump: (page: PageKey) => void;
};

export function ProjectProductionStage({
  dashboard,
  scriptSegments,
  storyboards,
  tasks,
  assets,
  jump,
}: ProjectProductionStageProps) {
  const segments = visibleSegments(scriptSegments);
  const production = productionProgressSummary(tasks);
  const assetProgress = production.rows.find((row) => row.key === "asset")!;
  const storyboardProgress = production.rows.find((row) => row.key === "storyboard")!;
  const rows = storyboards.length
    ? storyboards.slice(0, 6).map((storyboard) => {
        const relatedTasks = tasks.filter((task) => task.storyboard_id === storyboard.id);
        const status = relatedTasks.length
          ? relatedTasks.every((task) => task.status === "completed") ? "已完成" : "生产中"
          : "待分发";
        return [
          `${String(storyboard.episode_num).padStart(2, "0")}-${String(storyboard.order_num).padStart(3, "0")}`,
          storyboard.title || storyboard.description,
          status,
          Array.isArray(storyboard.characters) ? storyboard.characters.map((character) => assetReferenceDisplayLabel(character, assets, "character")).join("/") || "未绑定" : "未绑定",
          storyboard.duration_seconds ? `${storyboard.duration_seconds}s` : "待定",
          storyboard.camera || "无",
        ];
      })
    : [];

  return (
    <>
      <div className="tabs">
        {segments.map((segment, index) => (
          <button className={`tab ${index === 0 ? "active" : ""}`} key={segment.id}>
            {scriptSegmentEpisode(segment)} {segmentDisplayTitle(segment)}
          </button>
        ))}
      </div>
      <StatsGrid
        items={[
          ["脚本原文段", segments.length, "项目脚本原文段", "pink"],
          ["资产进度", assetProgress.done + "/" + assetProgress.total, "按正式资产聚合", "mint"],
          ["分镜进度", storyboardProgress.done + "/" + storyboardProgress.total, "按父分镜聚合", "sky"],
          ["父分镜", dashboard?.storyboard_count ?? storyboards.length, "镜中分镜不独立计数", "sun"],
          ["资产记录", assets.length, "项目资产库记录", "red"],
        ]}
      />
      <section className="detail-layout">
        <Panel title="分镜进度详情" subtitle="分镜、脚本、评分、关联资产、风险闭环展示">
          <div className="shot-table">
            {rows.map((row) => (
              <div className={`shot-row ${row[2] === "逾期" ? "overdue" : ""}`} key={row[0]}>
                {row.map((cell, index) => (
                  <span key={`${row[0]}-${index}`}>{cell}</span>
                ))}
              </div>
            ))}
            {!rows.length ? <div className="empty-hint">暂无分镜进度数据。</div> : null}
          </div>
        </Panel>
        <Panel title="完工内容" subtitle="图片、视频、成品聚合预览">
          <MediaStrip assets={assets} tasks={tasks} onClick={() => jump("assets")} />
        </Panel>
      </section>
    </>
  );
}

export function ArchiveReviewPanel({ tasks, assets }: { tasks: Task[]; assets: Asset[] }) {
  const completedTasks = tasks.filter((task) => task.status === "completed");
  const finalTasks = tasks.filter((task) => ["assembly", "final_output"].includes(task.task_type));
  return (
    <Panel title="终审归档清单" subtitle="成片上传、导演审核和资产归档将在此处汇总。">
      <div className="review-grid">
        {[
          ["已完成任务", `${completedTasks.length}`, "可进入导演终审的任务数量。"],
          ["剪辑/成品任务", `${finalTasks.length}`, "剪辑阶段和成品输出任务。"],
          ["资产库记录", `${assets.length}`, "已写入资产库的图片、视频和成品资产。"],
          ["归档状态", finalTasks.some((task) => task.status === "completed") ? "待审核" : "待成片", "导演确认后进入最终归档。"],
        ].map(([name, status, desc]) => (
          <div className="review-item" key={name}>
            <strong>{name}</strong>
            <span className="badge sky">{status}</span>
            <p>{desc}</p>
          </div>
        ))}
      </div>
    </Panel>
  );
}

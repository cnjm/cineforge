import React from "react";
import { PageHeader, StatsGrid } from "../../shared/components";
import type { Project, Task } from "../../types";
import { isOverdueTask, stageForProject, taskProgressByType } from "../../utils";
import { productionProgressSummary } from "../../projectProgress";
import { stageMeta, stageOrder } from "./projectMeta";

type ProjectOverviewPageProps = {
  projects: Project[];
  tasks: Task[];
  onDeleteProject: (project: Project, adminPassword: string) => Promise<void>;
  onCreateProject: () => void;
  onSelectProject: (projectId: string) => void;
};

export function ProjectOverviewPage({
  projects,
  tasks,
  onDeleteProject,
  onCreateProject,
  onSelectProject,
}: ProjectOverviewPageProps) {
  const productionSummary = React.useMemo(() => productionProgressSummary(tasks), [tasks]);
  const overviewProjects = React.useMemo(
    () => [...projects].sort((a, b) => new Date(b.created_at || 0).getTime() - new Date(a.created_at || 0).getTime()),
    [projects],
  );
  const tasksByProject = React.useMemo(() => {
    const grouped = new Map<string, Task[]>();
    tasks.forEach((task) => {
      const projectTasks = grouped.get(task.project_id);
      if (projectTasks) projectTasks.push(task);
      else grouped.set(task.project_id, [task]);
    });
    return grouped;
  }, [tasks]);
  const totalStoryboards = projects.reduce((sum, project) => sum + (project.storyboard_count || 0), 0);
  const completion = productionSummary.total ? Math.round((productionSummary.done / productionSummary.total) * 100) : 0;
  const overdueTasks = tasks.filter(isOverdueTask).length;

  return (
    <>
      <PageHeader
        title="项目总览"
        desc="以剧本为项目维度，按资产与父分镜汇总生产进度和风险。"
        action={
          <>
            <button className="btn" onClick={onCreateProject}>
              新增项目
            </button>
          </>
        }
      />
      <StatsGrid
        items={[
          ["项目", projects.length, "剧本维度管理", "pink"],
          ["总完成率", completion + "%", productionSummary.total ? productionSummary.done + "/" + productionSummary.total + " 生产单元" : "等待任务生成", "mint"],
          ["父分镜", totalStoryboards, "镜中分镜不独立计数", "sky"],
          ["生产单元", productionSummary.total, productionSummary.done + " 个已完成", "sun"],
          ["风险", overdueTasks, "全部项目逾期任务", "red"],
        ]}
      />
      <section className="overview-layout overview-layout-simple">
        <div>
          <section className="projects-grid">
            {!overviewProjects.length ? (
              <div className="empty-hint wide">暂无项目。请点击“新增项目”创建剧本项目，或进入项目详情导入剧本文本。</div>
            ) : null}
            {overviewProjects.map((project, index) => {
              const projectTasks = tasksByProject.get(project.id) || [];
              const progressRows = taskProgressByType(projectTasks);
              const projectProduction = productionProgressSummary(projectTasks);
              const stage = stageForProject(project, index);
              const meta = stageMeta[stage];
              const activeStep = stageOrder.indexOf(stage);
              return (
                <article className="project-card project-feature" key={project.id} onClick={() => onSelectProject(project.id)}>
                  <div className="project-top">
                    <div>
                      <div className="project-name">{project.title}</div>
                      <div className="project-meta">{project.genre ?? "漫剧项目"} / 当前阶段：{meta.label}</div>
                    </div>
                    <span className={`badge ${meta.tone}`}>{meta.short}</span>
                  </div>
                  <div className="stage-strip">
                    {stageOrder.map((step, stepIndex) => (
                      <span className={`stage-dot ${stepIndex <= activeStep ? "done" : ""} ${step === stage ? "active" : ""}`} key={step}>
                        {stageMeta[step].short}
                      </span>
                    ))}
                  </div>
                  <p className="stage-desc">{meta.desc}</p>
                  <div className="production-list">
                    {progressRows.map((item) => (
                      <div className="production-row" key={item.label}>
                        <span>{item.label}</span>
                        <div className="progress">
                          <span style={{ width: `${item.total ? Math.round((item.done / item.total) * 100) : 0}%` }} />
                        </div>
                        <strong>{item.done}/{item.total}</strong>
                      </div>
                    ))}
                    {!progressRows.length ? <div className="empty-hint">暂无生产任务数据。</div> : null}
                  </div>
                  <div className="project-foot">
                    <span>{project.storyboard_count} 父分镜</span>
                    <span>{projectProduction.total} 生产单元</span>
                    <div className="project-card-actions">
                      <details className="project-more-menu" onClick={(event) => event.stopPropagation()}>
                        <summary className="btn">更多</summary>
                        <div className="project-more-popover">
                          <button
                            className="btn danger"
                            onClick={(event) => {
                              event.stopPropagation();
                              const password = window.prompt("请输入管理员密码确认软删除项目");
                              if (password !== null) void onDeleteProject(project, password);
                            }}
                          >
                            删除项目
                          </button>
                        </div>
                      </details>
                      <button className="btn primary" onClick={(event) => { event.stopPropagation(); onSelectProject(project.id); }}>{meta.action}</button>
                    </div>
                  </div>
                </article>
              );
            })}
            <article className="project-card new-card" onClick={onCreateProject}>
              <div className="big-plus">+</div>
              <div className="project-name">新项目入口</div>
              <p className="muted">创建全新项目并导入 EP01 首集剧本。</p>
            </article>
          </section>
        </div>
      </section>
    </>
  );
}

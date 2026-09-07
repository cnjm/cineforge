import type { ProjectStage } from "../../types";

export const stageMeta: Record<ProjectStage, { label: string; short: string; action: string; desc: string; tone: string }> = {
  draft: {
    label: "项目创建阶段",
    short: "剧本导入",
    action: "继续导入",
    desc: "导入剧本、拆出项目信息、保存草稿或定稿。",
    tone: "pink",
  },
  breakdown_review: {
    label: "拆解与定稿阶段",
    short: "剧本拆解",
    action: "查看拆解",
    desc: "审阅围读确认、资产预定稿、脚本段与分镜预定稿。",
    tone: "sky",
  },
  asset_locking: {
    label: "分镜/资产拆分阶段",
    short: "分镜资产",
    action: "去拆分",
    desc: "基于已确认脚本段继续拆分分镜、人物、场景和道具。",
    tone: "sun",
  },
  task_assignment: {
    label: "任务分配阶段",
    short: "人员派工",
    action: "去分配",
    desc: "按文生图、图生视频、剪辑任务派发给项目组。",
    tone: "mint",
  },
  production: {
    label: "生产中",
    short: "进度跟踪",
    action: "查看进度",
    desc: "查看分集、分镜、图片、视频、成品、评分和风险项。",
    tone: "pink",
  },
  completed: {
    label: "成片完成",
    short: "终审归档",
    action: "查看成片",
    desc: "确认最终成片、沉淀资产版本和操作日志。",
    tone: "mint",
  },
};

export const stageOrder: ProjectStage[] = ["draft", "breakdown_review", "asset_locking", "task_assignment", "production", "completed"];

import React from "react";
import { createPortal } from "react-dom";
import {
  Ban,
  Bot,
  Camera,
  ChevronLeft,
  ChevronRight,
  Copy,
  CopyPlus,
  Image,
  Images,
  Info,
  Lock,
  PersonStanding,
  RefreshCw,
  Save,
  Search,
  UserRound,
  X,
} from "lucide-react";
import { OutlineSelect } from "../../shared/OutlineSelect";
import type { Project, Task, UserRole } from "../../types";

const PAGE_SIZE_OPTIONS = [10, 20, 50] as const;

const CHARACTER_SLOTS = [
  { key: "A", label: "正面全身 A-Pose", short: "正" },
  { key: "B", label: "侧面或 3/4 全身", short: "侧" },
  { key: "C", label: "背面全身", short: "背" },
  { key: "D", label: "面部与表情参考", short: "面" },
  { key: "E", label: "服装与材质细节", short: "服" },
] as const;

type CharacterSlotKey = (typeof CHARACTER_SLOTS)[number]["key"];

type CreationCenterPageProps = {
  tasks: Task[];
  projects: Project[];
  loading?: boolean;
  currentUserRole?: UserRole | null;
  onOpenCanvas?: (task: Task) => void;
};

type StatusKey = "waiting" | "working" | "reviewing" | "rejected" | "completed" | "other";
type TaskKind = "character" | "scene" | "prop" | "music" | "storyboard" | "keyframe";
type BucketFilter = "all" | "asset" | "storyboard";
type StatusFilter = "all" | StatusKey;

const STATUS_META: Record<StatusKey, { label: string; tone: string }> = {
  waiting: { label: "待制作", tone: "waiting" },
  working: { label: "制作中", tone: "working" },
  reviewing: { label: "待审核", tone: "reviewing" },
  rejected: { label: "需返工", tone: "rejected" },
  completed: { label: "已完成", tone: "completed" },
  other: { label: "已逾期", tone: "other" },
};

const STATUS_FILTER_OPTIONS: { value: StatusFilter; label: string }[] = [
  { value: "all", label: "状态" },
  { value: "waiting", label: "待制作" },
  { value: "working", label: "制作中" },
  { value: "reviewing", label: "待审核" },
  { value: "rejected", label: "需返工" },
  { value: "completed", label: "已完成" },
  { value: "other", label: "已逾期" },
];

function field(task: Task, key: string) {
  return String(task[key] || "").trim();
}

function normalizeStatus(raw: unknown): StatusKey {
  const status = String(raw || "").toLowerCase();
  if (["todo", "waiting", "pending", "locked"].includes(status)) return "waiting";
  if (["in_progress", "working", "doing", "active"].includes(status)) return "working";
  if (["submitted", "reviewing", "in_review"].includes(status)) return "reviewing";
  if (["rejected", "rework"].includes(status)) return "rejected";
  if (["completed", "done", "approved"].includes(status)) return "completed";
  if (["overdue", "expired"].includes(status)) return "other";
  return "other";
}

function isVideoTask(task: Task) {
  const media = field(task, "media_type").toLowerCase();
  const type = field(task, "task_type").toLowerCase();
  const title = field(task, "title");
  return media === "video" || type === "image_to_video" || title.includes("视频");
}

function taskKind(task: Task): TaskKind {
  const code = (field(task, "storyboard_code") || field(task, "scene_code") || "").toUpperCase();
  const sceneName = field(task, "scene_name");
  const title = field(task, "title");
  const type = field(task, "task_type");

  if (/^M\d/.test(code) || sceneName.includes("音乐") || title.includes("音乐")) return "music";
  if (/^P\d/.test(code) || sceneName.includes("道具") || type === "asset" && sceneName.includes("道具")) return "prop";
  if (/^SC\d/.test(code) || sceneName.includes("场景")) return "scene";
  if (/^BG-/.test(code) || sceneName.includes("分镜") || type === "storyboard_shot") {
    return isVideoTask(task) ? "storyboard" : "keyframe";
  }
  if (/^R\d/.test(code) || sceneName.includes("人物") || sceneName.includes("定装") || type === "asset") {
    return "character";
  }
  return isVideoTask(task) ? "storyboard" : "keyframe";
}

function isAssetTask(task: Task) {
  const kind = taskKind(task);
  return kind === "character" || kind === "scene" || kind === "prop";
}

function isStoryboardBucketTask(task: Task) {
  const kind = taskKind(task);
  return kind === "keyframe" || kind === "storyboard";
}

const ASSET_TYPE_OPTIONS = [
  { value: "character", label: "人物" },
  { value: "scene", label: "场景" },
  { value: "prop", label: "道具" },
] as const;

const STORYBOARD_TYPE_OPTIONS = [
  { value: "keyframe", label: "关键帧" },
  { value: "storyboard", label: "分镜" },
] as const;

function canFilterAssignee(role?: UserRole | null) {
  return role === "director" || role === "admin";
}

function kindBadge(task: Task) {
  const kind = taskKind(task);
  if (kind === "character") return { label: "人物", tone: "character" };
  if (kind === "scene") return { label: "场景", tone: "scene" };
  if (kind === "prop") return { label: "道具", tone: "prop" };
  if (kind === "music") return { label: "音乐", tone: "music" };
  if (kind === "keyframe") return { label: "关键帧", tone: "keyframe" };
  if (kind === "storyboard") return { label: "分镜", tone: "storyboard" };
  return { label: "任务", tone: "other" };
}

function characterAgeLabel(task: Task) {
  return field(task, "age_stage_label")
    || field(task, "age_stage_code")
    || "基础身份";
}

function characterCostumeLabel(task: Task) {
  const explicit = field(task, "costume_label") || field(task, "outfit_label");
  if (explicit) return explicit;
  const variant = field(task, "task_variant");
  const code = (field(task, "storyboard_code") || "").toUpperCase();
  if (variant.includes("表情") || code.includes("EXPR")) return "表情板";
  if (variant.includes("定妆") || variant.includes("定装")) return variant;
  if (/^[A-E]$/i.test(variant)) return `${variant.toUpperCase()} 定装`;
  if (/-A\b/.test(code) || code.endsWith("-A")) return "基础定妆";
  if (/-B\b/.test(code) || code.endsWith("-B")) return "B 定装";
  if (variant === "基础身份照" || !variant) return "基础定妆";
  return variant;
}

function assetInfoLine(task: Task) {
  const kind = taskKind(task);
  if (kind === "character") return `${characterAgeLabel(task)} · ${characterCostumeLabel(task)}`;
  if (kind === "scene") return field(task, "task_variant") || "场景母版";
  if (kind === "prop") return field(task, "task_variant") || "道具母版";
  if (kind === "music") return field(task, "task_variant") || "音乐母版";
  return "";
}

function taskTitle(task: Task) {
  return String(task.title || task.scene_name || task.storyboard_code || task.id || "未命名任务");
}

function taskSubtitle(task: Task) {
  if (isAssetTask(task)) {
    return assetInfoLine(task) || "资产制作";
  }
  const variant = field(task, "task_variant") || field(task, "age_stage_code");
  if (variant) return variant;
  return field(task, "scene_name") || field(task, "prompt_text") || "制作任务";
}

function projectLine(task: Task, projectName: string) {
  const short = projectName.replace(/《|》/g, "").slice(0, 8) || "项目";
  const episode = field(task, "episode_code") || "EP01";
  return `${short} · ${episode}`;
}

function contentTitle(task: Task) {
  if (isAssetTask(task)) {
    const kind = taskKind(task);
    if (kind === "character") {
      const costume = characterCostumeLabel(task);
      if (costume.includes("表情")) return "表情板";
      if (costume.includes("定妆") || costume.includes("定装")) return "定装照";
      return "基础身份照";
    }
    if (kind === "scene") return "场景母版";
    if (kind === "prop") return "道具母版";
    if (kind === "music") return "音乐母版";
  }
  const kind = taskKind(task);
  if (kind === "keyframe") return "关键帧";
  if (field(task, "shot_size")) return field(task, "shot_size");
  return "分镜镜头";
}

function contentDesc(task: Task) {
  const prompt =
    field(task, "latest_prompt_text")
    || field(task, "prompt_text")
    || field(task, "description");

  const twoLineSamples: Record<string, string> = {
    demo_task_maker_kbg_v014: "关键帧已定版，推进图生视频。\n注意角色连贯与镜头运动节奏。",
    demo_task_f014: "泰男冷脸特写，傍晚窗光，浅景深。\n保持前后镜头光影与景别衔接。",
    demo_task_maker_kbg_r001a: "郑夏允俱乐部女招待定装，清秀带疲惫。\n正侧背面服按图位分别交付。",
  };
  if (twoLineSamples[task.id]) return twoLineSamples[task.id];

  return prompt || "暂无制作要求";
}

function taskThumb(task: Task) {
  const submissions = Array.isArray(task.submissions) ? task.submissions : [];
  for (const submission of submissions) {
    const raw = String(
      (submission as { thumbnail_url?: string; preview_url?: string; file_path?: string }).thumbnail_url
      || (submission as { preview_url?: string }).preview_url
      || (submission as { file_path?: string }).file_path
      || "",
    );
    if (raw && !raw.startsWith("demo://")) return raw;
  }
  return String((task as { thumbnail_url?: string; cover_url?: string; preview_url?: string }).thumbnail_url
    || (task as { cover_url?: string }).cover_url
    || (task as { preview_url?: string }).preview_url
    || "");
}

function parseRequiredViews(task: Task): CharacterSlotKey[] | null {
  // TODO: 后端需在 Task 上返回 required_views 字段（string[] | string），当前仅通过类型强转读取
  const raw = (task as { required_views?: unknown }).required_views
    ?? (task as { required_slots?: unknown }).required_slots;
  if (Array.isArray(raw) && raw.length) {
    const keys = raw
      .map((item) => String(item || "").trim().toUpperCase())
      .filter((item): item is CharacterSlotKey => CHARACTER_SLOTS.some((slot) => slot.key === item));
    return keys.length ? keys : null;
  }
  const text = field(task, "required_views");
  if (text) {
    const keys = text
      .split(/[,，\s]+/)
      .map((item) => item.trim().toUpperCase())
      .filter((item): item is CharacterSlotKey => CHARACTER_SLOTS.some((slot) => slot.key === item));
    return keys.length ? keys : null;
  }
  return null;
}

function characterRequiredSlots(task: Task) {
  const explicit = parseRequiredViews(task);
  if (explicit) return CHARACTER_SLOTS.filter((slot) => explicit.includes(slot.key));

  const code = (field(task, "storyboard_code") || "").toUpperCase();
  const costume = characterCostumeLabel(task);
  const priority = (field(task, "asset_priority") || field(task, "priority") || "").toUpperCase();

  if (priority === "B" || priority === "C") {
    return CHARACTER_SLOTS.filter((slot) => slot.key === "A");
  }

  if (code.includes("EXPR") || costume.includes("表情")) {
    return CHARACTER_SLOTS.filter((slot) => slot.key === "D");
  }
  if (code.includes("CU")) {
    return CHARACTER_SLOTS.filter((slot) => slot.key === "A");
  }
  return CHARACTER_SLOTS.filter((slot) => slot.key === "A" || slot.key === "B" || slot.key === "D");
}

type SlotProgressStatus = StatusKey;

type ProgressSlot = {
  key: string;
  label: string;
  short: string;
  status: SlotProgressStatus;
  required: boolean;
};

type ProgressModel = {
  mode: "character" | "master" | "storyboard" | "keyframe";
  slots: ProgressSlot[];
  summary: string;
};

function slotStatusLabel(status: SlotProgressStatus) {
  return STATUS_META[status]?.label || "进行中";
}

function parseSlotStatusMap(task: Task): Partial<Record<string, SlotProgressStatus>> {
  // TODO: 后端需在 Task 上返回 slot_progress 字段（Record<string, StatusKey>），当前仅通过类型强转读取
  const raw = (task as { slot_progress?: unknown }).slot_progress;
  if (!raw || typeof raw !== "object") return {};
  const map: Partial<Record<string, SlotProgressStatus>> = {};
  Object.entries(raw as Record<string, unknown>).forEach(([key, value]) => {
    map[String(key).toUpperCase()] = normalizeStatus(value);
  });
  return map;
}

function demoSlotStatuses(total: number, taskStatus: StatusKey): SlotProgressStatus[] {
  if (total <= 0) return [];
  if (taskStatus === "waiting") return Array.from({ length: total }, () => "waiting" as const);
  if (taskStatus === "completed") return Array.from({ length: total }, () => "completed" as const);
  if (taskStatus === "rejected") {
    return Array.from({ length: total }, (_, index) => (index === 0 ? "rejected" : "waiting") as SlotProgressStatus);
  }
  if (taskStatus === "reviewing") {
    return Array.from({ length: total }, (_, index) => (
      index === 0 ? "completed" : index === 1 ? "reviewing" : "waiting"
    ) as SlotProgressStatus);
  }
  const afterFront: SlotProgressStatus[] = ["working", "reviewing", "rejected", "waiting"];
  return Array.from({ length: total }, (_, index) => (
    index === 0 ? "completed" : afterFront[(index - 1) % afterFront.length]
  ));
}

function applyFrontGate(slots: ProgressSlot[]): ProgressSlot[] {
  if (slots.length <= 1) return slots;
  const frontDone = slots[0]?.status === "completed";
  if (frontDone) return slots;
  return slots.map((slot, index) => (
    index === 0 ? slot : { ...slot, status: "waiting" as const }
  ));
}

function summarizeSlots(slots: ProgressSlot[]) {
  const done = slots.filter((slot) => slot.status === "completed").length;
  const total = slots.length;
  return {
    summary: `${done}/${total}`,
    done,
    total,
  };
}

function progressModel(task: Task): ProgressModel {
  const status = normalizeStatus(task.status);
  const kind = taskKind(task);
  const explicitMap = parseSlotStatusMap(task);

  if (kind === "character") {
    const slotsMeta = characterRequiredSlots(task);
    const fallback = demoSlotStatuses(slotsMeta.length, status);
    const rawSlots: ProgressSlot[] = slotsMeta.map((slot, index) => ({
      key: slot.key,
      label: slot.label,
      short: slot.short,
      status: explicitMap[slot.key] || fallback[index] || "waiting",
      required: true,
    }));
    const slots = applyFrontGate(rawSlots);
    const { summary } = summarizeSlots(slots);
    return { mode: "character", slots, summary };
  }

  if (kind === "scene" || kind === "prop" || kind === "music") {
    const label = kind === "scene" ? "场景母版" : kind === "prop" ? "道具母版" : "音乐母版";
    const short = kind === "scene" ? "景" : kind === "prop" ? "道" : "乐";
    const slotStatus = explicitMap.M || explicitMap.MASTER || status;
    return {
      mode: "master",
      slots: [{
        key: "M",
        label,
        short,
        status: slotStatus === "other" ? "working" : slotStatus,
        required: true,
      }],
      summary: "单图",
    };
  }

  if (kind === "keyframe") {
    const stepStatus = explicitMap.KEYFRAME || status;
    return {
      mode: "keyframe",
      slots: [{
        key: "KF",
        label: "关键帧",
        short: "帧",
        status: stepStatus === "other" ? "working" : stepStatus,
        required: true,
      }],
      summary: "单图",
    };
  }

  const keyframeStatus = explicitMap.KEYFRAME
    || (status === "waiting" ? "waiting"
      : status === "working" ? "working"
        : status === "rejected" ? "rejected"
          : "completed");
  const videoStatus = explicitMap.VIDEO
    || (status === "completed" ? "completed"
      : status === "reviewing" ? "reviewing"
        : status === "rejected" ? "waiting"
          : status === "working" ? (keyframeStatus === "completed" ? "working" : "waiting")
            : "waiting");

  const slots: ProgressSlot[] = [
    {
      key: "KF",
      label: "关键帧",
      short: "帧",
      status: keyframeStatus === "other" ? "working" : keyframeStatus,
      required: true,
    },
    {
      key: "VD",
      label: "视频",
      short: "视",
      status: videoStatus === "other" ? "working" : videoStatus,
      required: true,
    },
  ];
  const { summary } = summarizeSlots(slots);
  return {
    mode: "storyboard",
    slots,
    summary,
  };
}

function canvasActionMeta(status: StatusKey) {
  if (status === "rejected") return { label: "继续返工", tone: "danger" };
  if (status === "completed") return { label: "查看画布", tone: "ghost" };
  return { label: "去制作", tone: "primary" };
}

function promptTextOf(task: Task) {
  return (
    field(task, "latest_prompt_text")
    || field(task, "prompt_text")
    || field(task, "description")
    || "项目管理尚未下发提示词。请联系项目负责人在资产拆解/顺序机中确认后同步。"
  );
}

function slotPromptText(task: Task, slot: ProgressSlot | null) {
  const base = promptTextOf(task);

  if (!slot || slot.key === "A") {
    if (base.includes("角色身份锚点") || base.includes("图位 A")) return base;
  }

  const name = taskTitle(task).replace(/^R\d+[A-Z]?-?[A-Z]?\s*·\s*/i, "").split("·")[0]?.trim() || "角色";
  const identityLine = base.includes("角色身份锚点")
    ? base.split("\n").find((line) => line.startsWith("角色身份锚点")) || base.split("\n")[0]
    : `角色身份锚点：${name}角色定装。${base}`;

  if (!slot || slot.key === "A") {
    return [
      identityLine,
      base.includes("角色身份锚点") ? "" : base,
      "",
      "图位 A：正面平视，全身从头到脚完整呈现，人物居中，作为后续图位的主参考图。",
      "姿态要求：标准 A-Pose 站立，双臂从身体两侧自然展开约 30 度，手指放松，双脚与肩同宽，身体重心居中，神态自然。",
      "",
      "负向要求：避免改变角色身份、年龄、骨相、五官、体型、发型、服装结构和材质；避免错误肢体、手指异常、重复身体、裁切、遮挡、透视畸变、模糊、低清晰度、文字、水印和标识。",
      "",
      "图位说明：正面平视，全身从头到脚完整呈现，人物居中，作为后续图位的主参考图。",
      "",
      "参考图依赖：无前置参考图；输出将作为 B-E 的主参考图。",
      "",
      "生产要求：中性纯色摄影棚背景，柔和均匀布光，角色与服装色彩准确，轮廓和材质边缘清晰，无场景叙事元素干扰。",
    ].filter((line, index, arr) => !(line === "" && arr[index - 1] === "")).join("\n").trim();
  }

  const slotBriefs: Record<string, string[]> = {
    B: [
      `图位 B：${slot.label}`,
      "构图：侧面或 3/4 全身，展示头侧轮廓、肩背线条与服装侧面结构。",
      "姿态要求：与正面定版同一站姿逻辑，身体可自然转体，四肢完整不遮挡。",
      "参考图依赖：必须锁定正面 A 定版作为主参考，禁止身份漂移。",
      "生产要求：中性纯色摄影棚背景，柔和均匀布光，轮廓与材质边缘清晰。",
    ],
    C: [
      `图位 C：${slot.label}`,
      "构图：背面全身，展示后脑发型、后领、背缝、腰带与下装后侧。",
      "姿态要求：背面站姿，重心稳定，左右结构可读。",
      "参考图依赖：必须锁定正面 A 定版作为主参考，禁止新增未确认元素。",
      "生产要求：中性纯色摄影棚背景，柔和均匀布光，背面轮廓不过曝不过黑。",
    ],
    D: [
      `图位 D：${slot.label}`,
      "构图：面部近景或胸上近景，五官为主体，正脸或轻微 3/4 脸。",
      "表演要求：展现从卑微恳求到孤注一掷自荐的转变，突出坚韧与绝境中抓住希望的眼神；避免夸张卡通化。",
      "参考图依赖：必须锁定正面 A 定版五官与妆造。",
      "生产要求：柔光均匀照亮五官，眼神高光清晰，无文字水印。",
    ],
    E: [
      `图位 E：${slot.label}`,
      "构图：半身或局部特写，聚焦领口、袖口、面料纹理、缝线与关键配饰。",
      "材质要求：准确呈现俱乐部制服材质厚度、光泽与磨损感，膝盖擦伤细节可读。",
      "参考图依赖：必须锁定正面 A 定版服装版型与配色。",
      "生产要求：侧向柔光强调材质起伏，避免过曝丢失纹理。",
    ],
  };

  const brief = slotBriefs[slot.key] || [
    `图位：${slot.label}`,
    "参考图依赖：必须锁定正面 A 定版作为主参考。",
    "生产要求：中性纯色摄影棚背景，柔和均匀布光，轮廓与材质边缘清晰。",
  ];

  return [
    identityLine,
    "",
    ...brief,
    "",
    "负向要求：避免改变角色身份、年龄、骨相、五官、体型、发型、服装结构和材质；避免错误肢体、手指异常、重复身体、裁切、遮挡、透视畸变、模糊、低清晰度、文字、水印和标识。",
  ].join("\n");
}

function slotUnlocked(slots: ProgressSlot[], index: number) {
  if (index <= 0) return true;
  return slots[0]?.status === "completed";
}

function promptHeaderMeta(task: Task) {
  const kind = taskKind(task);
  const project = field(task, "project_title") || "项目";
  if (kind === "character") {
    return `${project} · ${characterAgeLabel(task)} / ${characterCostumeLabel(task)}`;
  }
  return `${project} · ${contentTitle(task)}`;
}

type PromptSection = {
  key: string;
  title: string;
  body: string;
};

function parsePromptSections(text: string): PromptSection[] {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const sections: PromptSection[] = [];
  let current: { key: string; title: string; lines: string[] } | null = null;

  const flush = () => {
    if (!current) return;
    sections.push({
      key: current.key,
      title: current.title,
      body: current.lines.join("\n").replace(/^\n+|\n+$/g, ""),
    });
    current = null;
  };

  const classify = (raw: string) => {
    const line = raw.trim();
    if (!line) return null;
    if (/^(角色身份锚点|角色身份|【角色身份】)/.test(line)) return { key: "identity", title: line };
    if (/^(图位\s*[A-E]|【图位】)/.test(line)) return { key: "slot", title: line };
    if (/^图位说明/.test(line)) return { key: "slotInfo", title: line };
    if (/^姿态要求/.test(line)) return { key: "pose", title: line };
    if (/^(负向要求|Negative\s*prompt)/i.test(line)) return { key: "negative", title: line };
    if (/^参考图依赖/.test(line)) return { key: "ref", title: line };
    if (/^生产要求/.test(line)) return { key: "prod", title: line };
    return null;
  };

  const splitTitleBody = (titleLine: string) => {
    const idx = titleLine.search(/[：:]/);
    if (idx > 0 && idx < titleLine.length - 1) {
      return {
        title: titleLine.slice(0, idx).trim(),
        body: titleLine.slice(idx + 1).trim(),
      };
    }
    return { title: titleLine.replace(/^【|】$/g, "").trim(), body: "" };
  };

  lines.forEach((line) => {
    const hit = classify(line);
    if (hit) {
      flush();
      const parts = splitTitleBody(hit.title);
      current = {
        key: hit.key,
        title: parts.title.replace(/^【|】$/g, ""),
        lines: parts.body ? [parts.body] : [],
      };
      return;
    }
    if (!current) {
      if (!line.trim()) return;
      current = { key: "body", title: "", lines: [line] };
      return;
    }
    current.lines.push(line);
  });
  flush();
  return sections.filter((section) => section.title || section.body);
}

function PromptSectionIcon({ sectionKey }: { sectionKey: string }) {
  const props = { size: 15, strokeWidth: 1.8 } as const;
  if (sectionKey === "identity") return <UserRound {...props} />;
  if (sectionKey === "slot" || sectionKey === "slotInfo") return <Image {...props} />;
  if (sectionKey === "pose") return <PersonStanding {...props} />;
  if (sectionKey === "negative") return <Ban {...props} />;
  if (sectionKey === "ref") return <Images {...props} />;
  if (sectionKey === "prod") return <Camera {...props} />;
  return <Info {...props} />;
}

function parseDeadline(task: Task) {
  const raw =
    field(task, "due_at")
    || field(task, "deadline")
    || field(task, "due_date")
    || field(task, "updated_at")
    || field(task, "created_at");
  const date = raw ? new Date(raw) : null;
  if (!date || Number.isNaN(date.getTime())) {
    return { primary: "未设置", remain: "", stamp: 0 };
  }

  const now = new Date();
  const sameDay =
    date.getFullYear() === now.getFullYear()
    && date.getMonth() === now.getMonth()
    && date.getDate() === now.getDate();
  const hh = String(date.getHours()).padStart(2, "0");
  const mm = String(date.getMinutes()).padStart(2, "0");
  const primary = sameDay
    ? `今天 ${hh}:${mm}`
    : `${date.getMonth() + 1}月${date.getDate()}日 ${hh}:${mm}`;

  const diffMs = date.getTime() - now.getTime();
  if (diffMs <= 0) return { primary, remain: "已逾期", stamp: date.getTime() };
  const hours = Math.floor(diffMs / 3_600_000);
  if (hours < 24) return { primary, remain: `剩余 ${Math.max(1, hours)} 小时`, stamp: date.getTime() };
  const days = Math.floor(hours / 24);
  return { primary, remain: `剩余 ${days} 天`, stamp: date.getTime() };
}

export function CreationCenterPage({
  tasks,
  projects,
  loading = false,
  currentUserRole = null,
  onOpenCanvas,
}: CreationCenterPageProps) {
  const [titleExtraHost, setTitleExtraHost] = React.useState<HTMLElement | null>(null);
  const [projectFilter, setProjectFilter] = React.useState("all");
  const [queryInput, setQueryInput] = React.useState("");
  const [query, setQuery] = React.useState("");
  const [bucket, setBucket] = React.useState<BucketFilter>("all");
  const [typeFilter, setTypeFilter] = React.useState("all");
  const [assigneeFilter, setAssigneeFilter] = React.useState("all");
  const [statusFilter, setStatusFilter] = React.useState<StatusFilter>("all");
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState<(typeof PAGE_SIZE_OPTIONS)[number]>(10);
  const [promptTask, setPromptTask] = React.useState<Task | null>(null);
  const [promptSlotKey, setPromptSlotKey] = React.useState<string>("");
  const [promptDraft, setPromptDraft] = React.useState("");
  const [toast, setToast] = React.useState<string | null>(null);
  const showAssigneeFilter = canFilterAssignee(currentUserRole);

  const promptProgress = React.useMemo(
    () => (promptTask ? progressModel(promptTask) : null),
    [promptTask],
  );

  const promptSlots = promptProgress?.slots || [];
  const activePromptSlot = promptSlots.find((slot) => slot.key === promptSlotKey) || promptSlots[0] || null;
  const finalizedCount = promptSlots.filter((slot) => slot.status === "completed").length;
  const generatableCount = promptSlots.filter((slot, index) => (
    slotUnlocked(promptSlots, index) && slot.status !== "completed"
  )).length;

  React.useEffect(() => {
    if (!promptTask) {
      setPromptSlotKey("");
      setPromptDraft("");
      return;
    }
    const progress = progressModel(promptTask);
    const firstOpen = progress.slots.find((_, index) => slotUnlocked(progress.slots, index) && progress.slots[index].status !== "completed")
      || progress.slots[0]
      || null;
    setPromptSlotKey(firstOpen?.key || "");
    setPromptDraft(slotPromptText(promptTask, firstOpen));
  }, [promptTask]);

  React.useEffect(() => {
    if (!toast) return undefined;
    const timer = window.setTimeout(() => setToast(null), 2400);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const showToast = React.useCallback((message: string) => {
    setToast(message);
  }, []);

  const handleOpenCanvas = React.useCallback((task: Task) => {
    if (onOpenCanvas) {
      onOpenCanvas(task);
    } else {
      showToast("创作画布暂未开放");
    }
  }, [onOpenCanvas, showToast]);

  const copyPrompt = React.useCallback(async (message: string) => {
    try {
      await navigator.clipboard.writeText(promptDraft);
      showToast(message);
    } catch {
      showToast("复制失败，请手动选择文本");
    }
  }, [promptDraft, showToast]);

  const portalHost = typeof document !== "undefined"
    ? (document.querySelector(".admin-app") || document.body)
    : null;

  React.useEffect(() => {
    setTitleExtraHost(document.getElementById("admin-title-extra"));
  }, []);

  const projectNames = React.useMemo(
    () => new Map(projects.map((project) => [String(project.id), String(project.title || project.name || "未命名项目")])),
    [projects],
  );

  const uniqueTasks = React.useMemo(
    () => Array.from(new Map(tasks.map((task) => [task.id, task])).values()),
    [tasks],
  );

  const projectOptions = React.useMemo(() => {
    const ids = new Set(uniqueTasks.map((task) => task.project_id).filter(Boolean));
    const options = Array.from(ids).map((id) => ({
      value: String(id),
      label: projectNames.get(String(id)) || "未命名项目",
    }));
    options.sort((a, b) => a.label.localeCompare(b.label, "zh"));
    return [{ value: "all", label: "全部项目" }, ...options];
  }, [projectNames, uniqueTasks]);

  const typeOptions = React.useMemo(() => {
    if (bucket === "asset") {
      return [
        { value: "all", label: "任务类型" },
        ...ASSET_TYPE_OPTIONS.map((item) => ({ value: item.value, label: item.label })),
      ];
    }
    if (bucket === "storyboard") {
      return [
        { value: "all", label: "任务类型" },
        ...STORYBOARD_TYPE_OPTIONS.map((item) => ({ value: item.value, label: item.label })),
      ];
    }
    return [
      { value: "all", label: "全部" },
      ...ASSET_TYPE_OPTIONS.map((item) => ({ value: item.value, label: item.label })),
      ...STORYBOARD_TYPE_OPTIONS.map((item) => ({ value: item.value, label: item.label })),
    ];
  }, [bucket]);

  const assigneeOptions = React.useMemo(() => {
    const names = Array.from(
      new Set(
        uniqueTasks
          .map((task) => String(task.assignee_name || "").trim())
          .filter((name): name is string => Boolean(name)),
      ),
    ).sort((a: string, b: string) => a.localeCompare(b, "zh"));
    return [
      { value: "all", label: "执行人" },
      ...names.map((name) => ({ value: name, label: name })),
    ];
  }, [uniqueTasks]);

  React.useEffect(() => {
    if (projectFilter === "all") return;
    if (!projectOptions.some((option) => option.value === projectFilter)) setProjectFilter("all");
  }, [projectFilter, projectOptions]);

  React.useEffect(() => {
    setTypeFilter("all");
    setPage(1);
  }, [bucket]);

  React.useEffect(() => {
    if (typeFilter === "all") return;
    if (!typeOptions.some((option) => option.value === typeFilter)) setTypeFilter("all");
  }, [typeFilter, typeOptions]);

  React.useEffect(() => {
    if (!showAssigneeFilter && assigneeFilter !== "all") setAssigneeFilter("all");
  }, [assigneeFilter, showAssigneeFilter]);

  // Debounce search input to avoid excessive re-renders
  React.useEffect(() => {
    const timer = window.setTimeout(() => setQuery(queryInput), 200);
    return () => window.clearTimeout(timer);
  }, [queryInput]);

  React.useEffect(() => {
    setPage(1);
  }, [projectFilter, query, typeFilter, assigneeFilter, statusFilter, pageSize]);

  const resetFilters = React.useCallback(() => {
    setProjectFilter("all");
    setQueryInput("");
    setQuery("");
    setBucket("all");
    setTypeFilter("all");
    setAssigneeFilter("all");
    setStatusFilter("all");
    setPage(1);
  }, []);

  const hasActiveFilters = projectFilter !== "all"
    || queryInput.length > 0
    || bucket !== "all"
    || typeFilter !== "all"
    || assigneeFilter !== "all"
    || statusFilter !== "all";

  const scopedTasks = React.useMemo(() => {
    return uniqueTasks.filter((task) => {
      if (projectFilter !== "all" && String(task.project_id) !== projectFilter) return false;
      return true;
    });
  }, [projectFilter, uniqueTasks]);

  const bucketCounts = React.useMemo(() => {
    let asset = 0;
    let storyboard = 0;
    scopedTasks.forEach((task) => {
      if (isAssetTask(task)) asset += 1;
      else if (isStoryboardBucketTask(task)) storyboard += 1;
    });
    return { all: scopedTasks.length, asset, storyboard };
  }, [scopedTasks]);

  const rows = React.useMemo(() => {
    const needle = query.trim().toLowerCase();
    const filtered = scopedTasks.filter((task) => {
      if (bucket === "asset" && !isAssetTask(task)) return false;
      if (bucket === "storyboard" && !isStoryboardBucketTask(task)) return false;
      const badge = kindBadge(task);
      if (typeFilter !== "all" && badge.tone !== typeFilter) return false;
      if (statusFilter !== "all" && normalizeStatus(task.status) !== statusFilter) return false;
      if (showAssigneeFilter && assigneeFilter !== "all" && String(task.assignee_name || "") !== assigneeFilter) {
        return false;
      }
      if (!needle) return true;
      const hay = [
        taskTitle(task),
        taskSubtitle(task),
        contentTitle(task),
        contentDesc(task),
        projectNames.get(String(task.project_id)) || task.project_title,
        task.episode_code,
        task.assignee_name,
      ]
        .map((value) => String(value || "").toLowerCase())
        .join(" ");
      return hay.includes(needle);
    });

    const statusOrder: Record<StatusKey, number> = {
      working: 0,
      completed: 1,
      waiting: 2,
      reviewing: 3,
      rejected: 4,
      other: 5,
    };

    const kindOrder = (task: Task) => {
      const kind = taskKind(task);
      if (kind === "character") return 0;
      if (kind === "prop") return 1;
      if (kind === "scene") return 2;
      if (kind === "music") return 3;
      if (kind === "keyframe") return 4;
      return 5;
    };

    return filtered.slice().sort((left, right) => {
      if (bucket === "all") {
        const byKind = kindOrder(left) - kindOrder(right);
        if (byKind !== 0) return byKind;
      }
      const byStatus = statusOrder[normalizeStatus(left.status)] - statusOrder[normalizeStatus(right.status)];
      if (byStatus !== 0) return byStatus;
      return taskTitle(left).localeCompare(taskTitle(right), "zh");
    });
  }, [assigneeFilter, bucket, projectNames, query, scopedTasks, showAssigneeFilter, statusFilter, typeFilter]);

  const pageCount = Math.max(1, Math.ceil(rows.length / pageSize));
  const safePage = Math.min(page, pageCount);
  const pagedRows = rows.slice((safePage - 1) * pageSize, safePage * pageSize);

  React.useEffect(() => {
    if (page > pageCount) setPage(pageCount);
  }, [page, pageCount]);

  return (
    <div className="creation-center-page creation-center-page--list">
      {titleExtraHost
        ? createPortal(
          <div className="review-hub__topbar-tools">
            <OutlineSelect
              value={projectFilter}
              ariaLabel="全部项目"
              className="outline-select--episode"
              options={projectOptions}
              onChange={setProjectFilter}
            />
            <label className="review-hub__search">
              <Search size={15} strokeWidth={2} aria-hidden="true" />
              <input
                type="text"
                value={queryInput}
                onChange={(event) => setQueryInput(event.target.value)}
                placeholder="搜索任务 / 项目 / 执行人"
                aria-label="搜索任务"
              />
              {queryInput ? (
                <button
                  type="button"
                  className="review-hub__search-clear"
                  onClick={() => setQueryInput("")}
                  aria-label="清除搜索"
                >
                  <X size={14} strokeWidth={2} />
                </button>
              ) : null}
            </label>
          </div>,
          titleExtraHost,
        )
        : null}

      <div className="creation-center-page__toolbar">
        <div className="creation-center-page__bucket-tabs" role="tablist" aria-label="任务分组">
          {([
            { key: "all", label: "全部任务", count: bucketCounts.all },
            { key: "asset", label: "资产制作", count: bucketCounts.asset },
            { key: "storyboard", label: "分镜制作", count: bucketCounts.storyboard },
          ] as const).map((item) => (
            <button
              key={item.key}
              type="button"
              role="tab"
              aria-selected={bucket === item.key}
              className={bucket === item.key ? "is-active" : undefined}
              onClick={() => setBucket(item.key)}
            >
              {item.label}
              <em>{item.count}</em>
            </button>
          ))}
        </div>
        <div className="creation-center-page__filters">
          <OutlineSelect
            value={typeFilter}
            ariaLabel="任务类型"
            className="outline-select--episode"
            options={typeOptions}
            onChange={setTypeFilter}
          />
          {showAssigneeFilter ? (
            <OutlineSelect
              value={assigneeFilter}
              ariaLabel="执行人"
              className="outline-select--episode"
              options={assigneeOptions}
              onChange={setAssigneeFilter}
            />
          ) : null}
          <OutlineSelect
            value={statusFilter}
            ariaLabel="状态"
            className="outline-select--episode"
            options={STATUS_FILTER_OPTIONS}
            onChange={(value) => setStatusFilter(value as StatusFilter)}
          />
          {hasActiveFilters ? (
            <button
              type="button"
              className="creation-center-page__reset-filters"
              onClick={resetFilters}
              title="清除所有筛选条件"
            >
              重置筛选
            </button>
          ) : null}
        </div>
      </div>

      <div className="creation-center-page__panel">
        <div className="creation-center-page__table-wrap">
          <table className="creation-center-page__table creation-center-page__table--rich">
            <thead>
              <tr>
                <th>任务信息</th>
                <th>制作内容</th>
                <th>任务进度</th>
                <th>状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {loading && !rows.length ? (
                <tr>
                  <td colSpan={5} className="creation-center-page__empty">正在加载任务…</td>
                </tr>
              ) : null}
              {!loading && !rows.length ? (
                <tr>
                  <td colSpan={5} className="creation-center-page__empty">暂无任务。可从项目管理分发任务后刷新。</td>
                </tr>
              ) : null}
              {pagedRows.map((task) => {
                const statusKey = normalizeStatus(task.status);
                const status = STATUS_META[statusKey];
                const badge = kindBadge(task);
                const projectName =
                  projectNames.get(task.project_id) || String(task.project_title || "未命名项目");
                const thumb = taskThumb(task);
                const progress = progressModel(task);
                const action = canvasActionMeta(statusKey);
                return (
                  <tr key={task.id}>
                    <td>
                      <div className="creation-center-page__task-info">
                        <span className="creation-center-page__thumb" aria-hidden="true">
                          {thumb ? <img src={thumb} alt="" /> : <i />}
                          <em className={`creation-center-page__kind is-${badge.tone}`}>{badge.label}</em>
                        </span>
                        <span className="creation-center-page__text-stack">
                          <span className="creation-center-page__text-head">
                            <button
                              type="button"
                              className="creation-center-page__text-title is-link"
                              onClick={() => handleOpenCanvas(task)}
                            >
                              {taskTitle(task)}
                            </button>
                            <span className="creation-center-page__text-sub">{taskSubtitle(task)}</span>
                          </span>
                          <span className="creation-center-page__text-foot">{projectLine(task, projectName)}</span>
                        </span>
                      </div>
                    </td>
                    <td className="creation-center-page__cell-content">
                      <div className="creation-center-page__content-stack">
                        <strong className="creation-center-page__text-title">{contentTitle(task)}</strong>
                        <span className="creation-center-page__content-desc">{contentDesc(task)}</span>
                      </div>
                    </td>
                    <td className="creation-center-page__cell-progress">
                      <div className={`creation-center-page__progress is-${progress.mode}`}>
                        <div className="creation-center-page__slot-pills" aria-label="任务进度">
                          {progress.slots.map((slot) => (
                            <span
                              key={slot.key}
                              className={`creation-center-page__slot-chip is-${slot.status}`}
                              title={`${slot.label} · ${slotStatusLabel(slot.status)}`}
                            >
                              <span className="creation-center-page__slot-chip-top">
                                <i aria-hidden="true" />
                                <b>{slot.short}</b>
                              </span>
                              <em>{slotStatusLabel(slot.status)}</em>
                            </span>
                          ))}
                        </div>
                      </div>
                    </td>
                    <td>
                      <div className="creation-center-page__status-block">
                        <span className={`creation-center-page__status-pill is-${status.tone}`}>
                          <i aria-hidden="true" />
                          {status.label}
                        </span>
                      </div>
                    </td>
                    <td>
                      <div className="creation-center-page__actions">
                        <button
                          type="button"
                          className="creation-center-page__action-btn"
                          onClick={() => setPromptTask(task)}
                          title="查看项目管理下发的提示词"
                        >
                          提示词
                        </button>
                        <button
                          type="button"
                          className={`creation-center-page__enter is-${action.tone}`}
                          onClick={() => handleOpenCanvas(task)}
                          title="跳转创作画布"
                        >
                          {action.label}
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>

        <div className="creation-center-page__footer">
          <span>共 {rows.length} 条</span>
          <div className="creation-center-page__pager">
            <button
              type="button"
              aria-label="上一页"
              disabled={safePage <= 1}
              onClick={() => setPage((value) => Math.max(1, value - 1))}
            >
              <ChevronLeft size={14} strokeWidth={1.8} />
            </button>
            {Array.from({ length: pageCount }, (_, index) => index + 1)
              .filter((item) => item === 1 || item === pageCount || Math.abs(item - safePage) <= 1)
              .reduce<number[]>((list, item, index, source) => {
                if (index > 0 && item - source[index - 1] > 1) list.push(-1);
                list.push(item);
                return list;
              }, [])
              .map((item, index) => (
                item < 0 ? (
                  <span key={`gap-${index}`} className="creation-center-page__pager-gap">…</span>
                ) : (
                  <button
                    key={item}
                    type="button"
                    className={item === safePage ? "is-active" : undefined}
                    onClick={() => setPage(item)}
                  >
                    {item}
                  </button>
                )
              ))}
            <button
              type="button"
              aria-label="下一页"
              disabled={safePage >= pageCount}
              onClick={() => setPage((value) => Math.min(pageCount, value + 1))}
            >
              <ChevronRight size={14} strokeWidth={1.8} />
            </button>
            <label className="creation-center-page__page-size">
              <select
                value={pageSize}
                onChange={(event) => setPageSize(Number(event.target.value) as (typeof PAGE_SIZE_OPTIONS)[number])}
              >
                {PAGE_SIZE_OPTIONS.map((size) => (
                  <option key={size} value={size}>{size} 条/页</option>
                ))}
              </select>
            </label>
          </div>
        </div>
      </div>

      {toast && portalHost
        ? createPortal(
          <div className="creation-center-page__toast" role="status">
            <span>{toast}</span>
            <button type="button" aria-label="关闭提示" onClick={() => setToast(null)}>
              <X size={14} strokeWidth={1.8} />
            </button>
          </div>,
          portalHost,
        )
        : null}

      {promptTask && portalHost
        ? createPortal(
          <div className="creation-center-page__prompt-layer" role="dialog" aria-modal="true" aria-label="制作提示词">
            <button
              type="button"
              className="creation-center-page__prompt-backdrop"
              aria-label="关闭"
              onClick={() => setPromptTask(null)}
            />
            <div className="creation-center-page__prompt-panel">
              <header className="creation-center-page__prompt-header">
                <div className="creation-center-page__prompt-heading">
                  <strong>{taskTitle(promptTask)}</strong>
                  <span>{promptHeaderMeta(promptTask)}</span>
                </div>
                <div className="creation-center-page__prompt-header-aside">
                  <em className="creation-center-page__prompt-badge is-done">已定版 {finalizedCount}/{Math.max(1, promptSlots.length)}</em>
                  <em className="creation-center-page__prompt-badge is-ready">可生成 {generatableCount}</em>
                  <button type="button" className="creation-center-page__prompt-close" aria-label="关闭提示词" onClick={() => setPromptTask(null)}>
                    <X size={16} strokeWidth={1.8} />
                  </button>
                </div>
              </header>

              <div className="creation-center-page__prompt-tabs" role="tablist" aria-label="任务进度图位">
                {promptSlots.map((slot, index) => {
                  const unlocked = slotUnlocked(promptSlots, index);
                  const active = slot.key === (activePromptSlot?.key || "");
                  return (
                    <button
                      key={slot.key}
                      type="button"
                      role="tab"
                      aria-selected={active}
                      disabled={!unlocked}
                      className={[
                        "creation-center-page__prompt-tab",
                        active ? "is-active" : "",
                        unlocked ? "" : "is-locked",
                        `is-${slot.status}`,
                      ].filter(Boolean).join(" ")}
                      onClick={() => {
                        if (!unlocked) return;
                        setPromptSlotKey(slot.key);
                        setPromptDraft(slotPromptText(promptTask, slot));
                      }}
                    >
                      <span className="creation-center-page__prompt-tab-top">
                        {!unlocked ? <Lock size={12} strokeWidth={1.8} /> : null}
                        <b>{slot.short}</b>
                        <span>{slot.label}</span>
                      </span>
                      <em>{slotStatusLabel(slot.status)}</em>
                    </button>
                  );
                })}
              </div>

              <section className="creation-center-page__prompt-main">
                <div className="creation-center-page__prompt-main-head">
                  <strong>
                    {activePromptSlot ? `${activePromptSlot.short} ${activePromptSlot.label}提示词` : "任务提示词"}
                  </strong>
                  {activePromptSlot ? (
                    <em className={`creation-center-page__prompt-chip is-${activePromptSlot.status}`}>
                      {slotStatusLabel(activePromptSlot.status)}
                    </em>
                  ) : null}
                </div>

                <div className="creation-center-page__prompt-body">
                  {parsePromptSections(promptDraft).map((section, index) => (
                    <article key={`${section.key}-${index}`} className={`creation-center-page__prompt-section is-${section.key}`}>
                      {section.title ? (
                        <header>
                          <i aria-hidden="true"><PromptSectionIcon sectionKey={section.key} /></i>
                          <strong>{section.title}</strong>
                        </header>
                      ) : null}
                      {section.body ? <p>{section.body}</p> : null}
                    </article>
                  ))}
                </div>
              </section>

              <footer className="creation-center-page__prompt-footer">
                <div className="creation-center-page__prompt-tools">
                  <button
                    type="button"
                    data-tip="复制提示词"
                    aria-label="复制提示词"
                    onClick={() => void copyPrompt("已复制提示词")}
                  >
                    <Copy size={15} strokeWidth={1.8} />
                  </button>
                  <button
                    type="button"
                    data-tip="复制并保存版本"
                    aria-label="复制并保存版本"
                    onClick={() => void copyPrompt("已复制并保存版本（演示）")}
                  >
                    <CopyPlus size={15} strokeWidth={1.8} />
                  </button>
                  <button
                    type="button"
                    data-tip="保存提示词"
                    aria-label="保存提示词"
                    onClick={() => showToast("提示词已保存（演示）")}
                  >
                    <Save size={15} strokeWidth={1.8} />
                  </button>
                  <button
                    type="button"
                    data-tip="Agent 生成提示词"
                    aria-label="Agent 生成提示词"
                    onClick={() => showToast("Agent 生成中（演示）")}
                  >
                    <Bot size={15} strokeWidth={1.8} />
                  </button>
                  <button
                    type="button"
                    data-tip="重新生成提示词"
                    aria-label="重新生成提示词"
                    onClick={() => showToast("Agent 重新生成中（演示）")}
                  >
                    <RefreshCw size={15} strokeWidth={1.8} />
                  </button>
                </div>
                <div className="creation-center-page__prompt-footer-actions">
                  <button
                    type="button"
                    className="creation-center-page__prompt-cancel"
                    onClick={() => setPromptTask(null)}
                  >
                    取消
                  </button>
                  <button
                    type="button"
                    className="creation-center-page__prompt-submit"
                    onClick={() => {
                      handleOpenCanvas(promptTask);
                      setPromptTask(null);
                    }}
                  >
                    去画布制作
                  </button>
                </div>
              </footer>
            </div>
          </div>,
          portalHost,
        )
        : null}
    </div>
  );
}
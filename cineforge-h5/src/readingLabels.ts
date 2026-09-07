export type ReadingLabelKind = "shot_density" | "culture" | "relationship" | "emotion" | "visual_style" | "risk";

const READING_LABELS: Record<ReadingLabelKind, Record<string, string>> = {
  shot_density: {
    high: "高密度",
    medium: "中等密度",
    moderate: "中等密度",
    low: "低密度",
  },
  culture: {
    contemporary: "当代",
    modern: "近现代",
    ancient: "古代",
    historical: "历史时期",
    mythological: "神话时代",
    mythology: "神话体系",
    chinese: "中华文化",
    china: "中国",
    east_asian: "东亚文化",
    western: "西方文化",
    fictional: "架空文化",
    mixed: "混合文化",
  },
  relationship: {
    ally: "盟友",
    allies: "盟友",
    enemy: "敌对",
    enemies: "敌对",
    rival: "对手",
    rivals: "对手",
    mentor: "师徒",
    mentor_student: "师徒",
    parent_child: "亲子",
    siblings: "手足",
    sibling: "手足",
    friends: "朋友",
    friend: "朋友",
    romantic: "情感关系",
    lovers: "恋人",
    family: "家族",
    colleague: "同僚",
    master_servant: "主从",
  },
  emotion: {
    neutral: "平静",
    calm: "平静",
    tension: "紧张",
    tense: "紧张",
    suspense: "悬疑",
    fear: "恐惧",
    anger: "愤怒",
    sadness: "悲伤",
    joy: "喜悦",
    hope: "希望",
    despair: "绝望",
    surprise: "惊讶",
    excitement: "兴奋",
    high: "高",
    medium: "中",
    moderate: "中",
    low: "低",
  },
  visual_style: {
    cinematic: "电影感",
    realistic: "写实",
    realism: "写实主义",
    stylized: "风格化",
    anime: "动画风格",
    manga: "漫画风格",
    ink_wash: "水墨风格",
    chinese_ink: "中国水墨",
    cyberpunk: "赛博朋克",
    retro: "复古",
    minimal: "极简",
    high_contrast: "高对比度",
    warm: "暖色调",
    cool: "冷色调",
  },
  risk: {
    production_risk: "生产风险",
    cultural_misread: "文化误读风险",
    cultural_accuracy: "文化准确性",
    continuity: "连续性风险",
    character_continuity: "人物连续性",
    visual_continuity: "视觉连续性",
    visual_complexity: "视觉实现难度",
    technical_complexity: "技术实现难度",
    schedule: "排期风险",
    budget: "成本风险",
    content_safety: "内容安全",
    copyright: "版权风险",
    manual_review: "人工复核",
  },
};

export function readingLabel(kind: ReadingLabelKind, value: unknown): string {
  const source = value === null || value === undefined ? "" : String(value).trim();
  if (!source) return "";
  return READING_LABELS[kind][normalizeReadingKey(source)] || source;
}

export function readingLabels(kind: ReadingLabelKind, values: string[]): string[] {
  return values.map((value) => readingLabel(kind, value));
}

function normalizeReadingKey(value: string): string {
  return value.toLowerCase().replace(/[\s-]+/g, "_");
}

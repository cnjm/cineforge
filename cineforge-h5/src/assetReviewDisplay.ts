import type { AssetNormalizationGlobalReviewItem } from "./types.ts";

const FIELD_LABELS: Record<string, string> = {
  inventory: "资产清单",
  name: "名称",
  description: "制作要求",
  priority: "优先级",
  accessories: "配饰",
  age: "年龄",
  appearance: "外观",
  architecture: "建筑结构",
  architecture_design: "建筑设计",
  asset_code: "资产编号",
  body: "体态",
  buttons_and_accessories: "纽扣与配饰",
  character_design: "角色设计",
  color: "颜色",
  color_aging: "色彩老化",
  colors: "颜色",
  "colors, textures": "颜色与纹理",
  "colors, textures, materials": "颜色、纹理与材质",
  content_compliance: "内容合规",
  content_review: "内容审核",
  continuity_rules: "连续性规则",
  "costume.description": "服装说明",
  costume_variant: "服装变体",
  cultural_origin: "文化背景",
  curtain_detail: "窗帘细节",
  display_code: "展示编号",
  display_label: "展示名称",
  dressing_level: "着装程度",
  exterior_view: "外景视图",
  hair: "发型",
  "hair, eyes, skin": "发型、眼睛与肤色",
  hair_style: "发型",
  identity_anchors: "身份锚点",
  lighting: "光线",
  lighting_baseline: "光照基准",
  lining: "内衬",
  "logo/标识": "标识",
  matching_uniform: "配套校服",
  material: "材质",
  materials: "材质",
  mirror_spec: "镜面规格",
  pants_color: "裤装颜色",
  pattern_detail: "版型细节",
  physique: "体型",
  prompt: "生产提示",
  "prompt / material_bible.colors": "生产提示 / 材质色彩",
  relationship_map: "人物关系",
  scale: "尺寸比例",
  scene_context: "场景上下文",
  school_badge: "校徽",
  shape: "形状",
  "shape, prompt": "形状与生产提示",
  skin_tone: "肤色",
  "spatial_zones/circulation": "空间分区与动线",
  state_prompts: "状态提示词",
  "state_prompts, material_bible": "状态提示词与材质规范",
  style_detail: "款式细节",
  "suit_color, tie_color": "西装与领带颜色",
  suit_details: "西装细节",
  sunglasses_style: "墨镜款式",
  "textures, colors": "纹理与颜色",
  tie_state: "领带状态",
  visual_elements: "视觉元素",
  visual_style: "视觉风格",
  "cultural_origin.language": "文化背景 / 语言",
  "relations.relationship_text": "关系说明",
  "relations.scene_links": "关联场景",
  "relations.owner_character": "所属角色",
  "relations.storyboard_codes": "关联分镜",
  "relations.script_segment_codes": "关联脚本段",
  "relations.confidence": "关联置信度",
  "attributes.character_type": "角色类型",
  "attributes.visual_presence": "出场方式",
  "attributes.role": "角色定位",
  "attributes.role_type": "角色定位",
  "attributes.gender": "性别",
  "attributes.age": "年龄",
  "attributes.appearance": "外观",
  "attributes.core_requirement": "核心制作要求",
  "attributes.personality": "性格",
  "attributes.background": "角色背景",
  "attributes.has_dialogue": "是否有台词",
  "attributes.dialogue_evidence": "台词依据",
  "attributes.context_codes": "上下文编号",
  "attributes.context_code": "上下文编号",
  "attributes.render_mode": "呈现方式",
  "attributes.scene_no": "场景编号",
  "attributes.interior_exterior": "内外景",
  "attributes.time": "时间",
  "attributes.location": "地点",
  "attributes.atmosphere": "氛围",
  "attributes.narrative_function": "叙事功能",
  "attributes.spatial_zones": "空间分区",
  "attributes.visual_goal": "视觉目标",
  "attributes.key_props": "关键道具",
  "attributes.key_prop_codes": "关键道具编号",
  "attributes.continuity_risks": "连续性风险",
  "attributes.episode_code": "关联分集",
  "attributes.level": "资产级别",
  "attributes.prop_type": "道具类型",
  "attributes.source": "道具出处",
  "attributes.status_change": "状态变化",
  "attributes.variant_states": "变体状态",
  "attributes.usage_scene": "使用场景",
  "attributes.scene_description": "场景说明",
};

const FIELD_TOKEN_LABELS: Record<string, string> = {
  asset: "资产",
  attributes: "属性",
  character: "角色",
  confidence: "置信度",
  context: "上下文",
  cultural: "文化",
  description: "说明",
  episode: "分集",
  field: "字段",
  inventory: "资产清单",
  language: "语言",
  music: "音乐",
  name: "名称",
  origin: "背景",
  owner: "所属",
  prop: "道具",
  relations: "关系",
  scene: "场景",
  source: "来源",
  status: "状态",
  storyboard: "分镜",
  type: "类型",
};

export function assetReviewFieldLabel(value: unknown): string {
  const field = String(value || "").trim();
  if (!field) return "其他信息";
  const normalized = field.toLowerCase();
  if (FIELD_LABELS[normalized]) return FIELD_LABELS[normalized];
  if (/^[\u3400-\u9fff\s/、，（）()]+$/.test(field)) return field;
  const tokens = normalized.split(/[._/\[\]-]+/).filter(Boolean);
  const translated = tokens.map((token) => FIELD_TOKEN_LABELS[token]).filter(Boolean);
  return translated.length ? Array.from(new Set(translated)).join(" / ") : "其他信息";
}

export function aggregateGlobalReviewSuggestions(items: AssetNormalizationGlobalReviewItem[]): string[] {
  const seen = new Set<string>();
  const suggestions: string[] = [];
  items.forEach((item) => {
    const suggestion = String(item.suggestion || "").trim();
    const normalized = suggestion.replace(/\s+/g, " ");
    if (!normalized || seen.has(normalized)) return;
    seen.add(normalized);
    suggestions.push(suggestion);
  });
  return suggestions;
}

export function assetTypeDisplayLabel(value: string): string {
  if (value === "character") return "人物";
  if (value === "scene") return "场景";
  if (value === "prop") return "道具";
  return "资产";
}

export function assetWorkflowStatus(requiredReviewCount: number, inventoryConfirmed: boolean): string {
  if (requiredReviewCount > 0) return `待完善 ${requiredReviewCount}`;
  return inventoryConfirmed ? "已确认" : "待确认";
}

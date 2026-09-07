from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from app.agents.skill_contracts import get_skill_contract


@dataclass(frozen=True)
class DisplayField:
    key: str
    label: str
    source_path: str
    component: str = "text"
    editable: bool = False
    feedback_skill: str | None = None
    required_for_display: bool = False

    def to_schema(self) -> dict[str, Any]:
        return {
            "key": self.key,
            "label": self.label,
            "source_path": self.source_path,
            "component": self.component,
            "editable": self.editable,
            "feedback_skill": self.feedback_skill,
            "required_for_display": self.required_for_display,
        }


@dataclass(frozen=True)
class DisplaySection:
    key: str
    title: str
    source_path: str
    component: str
    fields: tuple[DisplayField, ...]

    def to_schema(self) -> dict[str, Any]:
        return {
            "key": self.key,
            "title": self.title,
            "source_path": self.source_path,
            "component": self.component,
            "fields": [field.to_schema() for field in self.fields],
        }


@dataclass(frozen=True)
class SkillDisplayContract:
    skill: str
    primary_frontend_model: str
    sections: tuple[DisplaySection, ...]

    def to_schema(self) -> dict[str, Any]:
        contract = get_skill_contract(self.skill)
        return {
            "skill": self.skill,
            "contract_version": contract.version,
            "output_schema": contract.json_schema,
            "primary_frontend_model": self.primary_frontend_model,
            "sections": [section.to_schema() for section in self.sections],
        }


SCRIPT_READING_DISPLAY = SkillDisplayContract(
    skill="script-reading",
    primary_frontend_model="reading_output",
    sections=(
        DisplaySection(
            key="story_overview",
            title="故事概览",
            source_path="story_overview",
            component="key_value",
            fields=(
                DisplayField("logline", "一句话故事", "story_overview.logline", editable=True, feedback_skill="script-reading"),
                DisplayField("synopsis", "剧情概要", "story_overview.synopsis", "textarea", True, "script-reading", True),
                DisplayField("core_conflict", "核心冲突", "story_overview.core_conflict", "textarea", True, "script-reading"),
                DisplayField("main_hook", "开场钩子", "story_overview.main_hook", "textarea", True, "script-reading"),
                DisplayField("audience_promise", "观众期待", "story_overview.audience_promise", "textarea", True, "script-reading"),
                DisplayField("genre_tags", "类型标签", "story_overview.genre_tags", "tag_list", True, "script-reading"),
            ),
        ),
        DisplaySection(
            key="platform_strategy",
            title="平台策略",
            source_path="platform_strategy",
            component="key_value",
            fields=(
                DisplayField("primary_platform", "主平台", "platform_strategy.primary_platform", editable=True, feedback_skill="script-reading"),
                DisplayField("aspect_ratio", "画幅", "platform_strategy.aspect_ratio", editable=True, feedback_skill="script-reading"),
                DisplayField("target_duration_seconds", "目标时长", "platform_strategy.target_duration_seconds", "number", True, "script-reading"),
                DisplayField("rhythm_strategy", "节奏策略", "platform_strategy.rhythm_strategy", "textarea", True, "script-reading"),
                DisplayField("composition_strategy", "构图策略", "platform_strategy.composition_strategy", "textarea", True, "script-reading"),
                DisplayField("subtitle_strategy", "字幕策略", "platform_strategy.subtitle_strategy", "textarea", True, "script-reading"),
                DisplayField("shot_density", "镜头密度", "platform_strategy.shot_density", editable=True, feedback_skill="script-reading"),
            ),
        ),
        DisplaySection(
            key="story_relationships",
            title="人物关系",
            source_path="relationship_map",
            component="table",
            fields=(
                DisplayField("relationship_map", "人物关系", "relationship_map[]", "table", True, "script-reading"),
            ),
        ),
        DisplaySection(
            key="reading_guidance",
            title="围读结论",
            source_path="worldview/emotion_curve/visual_style/cultural_origin/recommended_visual_style/segmentation_guidance/production_risks/manual_review_items",
            component="tables",
            fields=(
                DisplayField("worldview", "世界观", "worldview", "json", True, "script-reading"),
                DisplayField("emotion_curve", "情感曲线", "emotion_curve[]", "table", True, "script-reading"),
                DisplayField("visual_style", "视觉风格", "visual_style", "json", True, "script-reading"),
                DisplayField("cultural_origin", "文化与时代判断", "cultural_origin", "json", True, "script-reading"),
                DisplayField("recommended_visual_style", "推荐视觉风格", "recommended_visual_style", "json", True, "script-reading"),
                DisplayField("segmentation_guidance", "拆段指导", "segmentation_guidance", "json", True, "script-reading"),
                DisplayField("production_risks", "生产风险", "production_risks[]", "table", True, "script-reading"),
                DisplayField("manual_review_items", "人工复核项", "manual_review_items[]", "table", True, "script-reading"),
            ),
        ),
    ),
)


SCRIPT_BREAKDOWN_DISPLAY = SkillDisplayContract(
    skill="script-breakdown",
    primary_frontend_model="breakdown_view",
    sections=(
        DisplaySection(
            key="script_segments",
            title="脚本原文段",
            source_path="breakdown_view.script_segments",
            component="editable_table",
            fields=(
                DisplayField("script_segment_code", "原文段编码", "script_segments[].script_segment_code", feedback_skill="script-segmentation", required_for_display=True),
                DisplayField("episode_code", "分集", "script_segments[].episode_code", editable=True, feedback_skill="script-segmentation"),
                DisplayField("order_no", "顺序", "script_segments[].order_no", editable=True, feedback_skill="script-segmentation", required_for_display=True),
                DisplayField("title", "标题", "script_segments[].title", editable=True, feedback_skill="script-segmentation"),
                DisplayField("source_text", "原文", "script_segments[].source_text", editable=True, feedback_skill="script-segmentation", required_for_display=True),
                DisplayField("summary", "摘要", "script_segments[].summary", editable=True, feedback_skill="script-segmentation"),
                DisplayField("story_function", "剧情功能", "script_segments[].story_function", editable=True, feedback_skill="script-segmentation"),
                DisplayField("dominant_emotion", "主导情绪", "script_segments[].dominant_emotion", editable=True, feedback_skill="script-segmentation"),
                DisplayField("rhythm", "节奏", "script_segments[].rhythm", editable=True, feedback_skill="script-segmentation"),
                DisplayField("viewpoint", "视角", "script_segments[].viewpoint", editable=True, feedback_skill="script-segmentation"),
                DisplayField("estimated_duration_seconds", "预计时长", "script_segments[].estimated_duration_seconds", "number", True, "script-segmentation"),
                DisplayField("role_refs", "关联角色", "script_segments[].role_refs", "tag_list", True, "script-segmentation"),
                DisplayField("scene_refs", "关联场景", "script_segments[].scene_refs", "tag_list", True, "script-segmentation"),
                DisplayField("prop_refs", "关联道具", "script_segments[].prop_refs", "tag_list", True, "script-segmentation"),
                DisplayField("production_notes", "生产备注", "script_segments[].production_notes", "tag_list", True, "script-segmentation"),
            ),
        ),
        DisplaySection(
            key="storyboards",
            title="分镜表",
            source_path="breakdown_view.storyboards",
            component="editable_cards",
            fields=(
                DisplayField("storyboard_code", "分镜编码", "storyboards[].storyboard_code", feedback_skill="script-breakdown", required_for_display=True),
                DisplayField("script_segment_code", "关联原文段", "storyboards[].script_segment_code", editable=True, feedback_skill="script-breakdown", required_for_display=True),
                DisplayField("order_num", "顺序", "storyboards[].order_num", editable=True, feedback_skill="script-breakdown", required_for_display=True),
                DisplayField("shot_size", "景别", "storyboards[].shot_size", editable=True, feedback_skill="script-breakdown"),
                DisplayField("duration_seconds", "时长", "storyboards[].duration_seconds", "number", True, "script-breakdown", True),
                DisplayField("characters", "角色", "storyboards[].characters", "tag_list", True, "script-breakdown"),
                DisplayField("camera", "运镜", "storyboards[].camera", "textarea", True, "script-breakdown"),
                DisplayField("description", "画面描述", "storyboards[].description", "textarea", True, "script-breakdown", True),
                DisplayField("dialogue", "台词", "storyboards[].dialogue", "textarea", True, "script-breakdown"),
                DisplayField("mirror_shots", "镜中分镜", "storyboards[].mirror_shots", "nested_table", True, "script-breakdown"),
                DisplayField("keyframe_code", "出图编号", "storyboards[].keyframe_code", editable=True, feedback_skill="script-breakdown"),
                DisplayField("video_code", "视频编号", "storyboards[].video_code", editable=True, feedback_skill="script-breakdown"),
            ),
        ),
        DisplaySection(
            key="assets",
            title="正式资产引用",
            source_path="breakdown_view.assets",
            component="cards",
            fields=(
                DisplayField("asset_type", "类型", "assets[].asset_type", "text", feedback_skill="asset-extract", required_for_display=True),
                DisplayField("asset_code", "编码", "assets[].asset_code", feedback_skill="asset-extract", required_for_display=True),
                DisplayField("name", "名称", "assets[].name", feedback_skill="asset-extract", required_for_display=True),
                DisplayField("description", "制作描述", "assets[].description", "text", feedback_skill="asset-extract"),
                DisplayField("related_storyboard_codes", "关联分镜", "assets[].related_storyboard_codes", "tag_list", True, "asset-extract"),
                DisplayField("metadata", "扩展信息", "assets[].metadata", "metadata", feedback_skill="asset-extract"),
            ),
        ),
        DisplaySection(
            key="production_plan",
            title="出图与视频清单",
            source_path="breakdown_view.keyframe_plan/breakdown_view.video_plan",
            component="tables",
            fields=(
                DisplayField("keyframe_code", "关键帧编号", "keyframe_plan[].keyframe_code", feedback_skill="script-breakdown"),
                DisplayField("purpose", "出图用途", "keyframe_plan[].purpose", editable=True, feedback_skill="script-breakdown"),
                DisplayField("video_code", "视频编号", "video_plan[].video_code", feedback_skill="script-breakdown"),
                DisplayField("seed_keyframes", "种子帧", "video_plan[].seed_keyframes", "tag_list", True, "script-breakdown"),
            ),
        ),
        DisplaySection(
            key="manual_review",
            title="复核与检查",
            source_path="breakdown_view.manual_review_items",
            component="checklist",
            fields=(
                DisplayField("missing_asset_suggestions", "缺失资产建议", "missing_asset_suggestions[]", "table", feedback_skill="script-breakdown"),
                DisplayField("manual_review_items", "人工复核项", "manual_review_items[]", "table", True, "script-breakdown"),
                DisplayField("delivery_checklist", "交付检查", "delivery_checklist[]", "table", True, "script-breakdown"),
                DisplayField("notes", "拆解说明", "notes[]", "tag_list", True, "script-breakdown"),
            ),
        ),
    ),
)


SCRIPT_SEGMENTATION_DISPLAY = SkillDisplayContract(
    skill="script-segmentation",
    primary_frontend_model="script_segments",
    sections=(
        DisplaySection(
            key="script_segments",
            title="脚本原文段",
            source_path="script_segments",
            component="editable_table",
            fields=(
                DisplayField("script_segment_code", "原文段编码", "script_segments[].script_segment_code", feedback_skill="script-segmentation"),
                DisplayField("episode_code", "分集", "script_segments[].episode_code", editable=True, feedback_skill="script-segmentation"),
                DisplayField("order_no", "顺序", "script_segments[].order_no", "number", True, "script-segmentation", True),
                DisplayField("title", "标题", "script_segments[].title", editable=True, feedback_skill="script-segmentation"),
                DisplayField("source_text", "原文", "script_segments[].source_text", "textarea", True, "script-segmentation", True),
                DisplayField("summary", "摘要", "script_segments[].summary", "textarea", True, "script-segmentation"),
                DisplayField("story_function", "剧情功能", "script_segments[].story_function", editable=True, feedback_skill="script-segmentation"),
                DisplayField("dominant_emotion", "主导情绪", "script_segments[].dominant_emotion", editable=True, feedback_skill="script-segmentation"),
                DisplayField("rhythm", "节奏", "script_segments[].rhythm", editable=True, feedback_skill="script-segmentation"),
                DisplayField("viewpoint", "视角", "script_segments[].viewpoint", editable=True, feedback_skill="script-segmentation"),
                DisplayField("estimated_duration_seconds", "预计时长", "script_segments[].estimated_duration_seconds", "number", True, "script-segmentation"),
                DisplayField("role_refs", "关联角色", "script_segments[].role_refs", "tag_list", True, "script-segmentation"),
                DisplayField("scene_refs", "关联场景", "script_segments[].scene_refs", "tag_list", True, "script-segmentation"),
                DisplayField("prop_refs", "关联道具", "script_segments[].prop_refs", "tag_list", True, "script-segmentation"),
                DisplayField("production_notes", "生产备注", "script_segments[].production_notes", "tag_list", True, "script-segmentation"),
            ),
        ),
        DisplaySection(
            key="manual_review",
            title="拆段复核",
            source_path="segmentation_notes/manual_review_items",
            component="checklist",
            fields=(
                DisplayField("segmentation_notes", "拆段说明", "segmentation_notes[]", "tag_list", True, "script-segmentation"),
                DisplayField("manual_review_items", "人工复核项", "manual_review_items[]", "table", True, "script-segmentation"),
            ),
        ),
    ),
)


ASSET_EXTRACT_DISPLAY = SkillDisplayContract(
    skill="asset-extract",
    primary_frontend_model="asset_master_draft",
    sections=(
        DisplaySection(
            key="characters",
            title="人物资产",
            source_path="characters",
            component="editable_cards",
            fields=(
                DisplayField("asset_code", "资产编码", "characters[].asset_code", feedback_skill="asset-extract", required_for_display=True),
                DisplayField("name", "名称", "characters[].name", editable=True, feedback_skill="asset-extract", required_for_display=True),
                DisplayField("priority", "优先级", "characters[].priority", "select", True, "asset-extract", True),
                DisplayField("appearance", "外观", "characters[].appearance", "textarea", True, "asset-extract"),
                DisplayField("related_scene_codes", "关联场景", "characters[].related_scene_codes", "tag_list", True, "asset-extract"),
            ),
        ),
        DisplaySection(
            key="scenes",
            title="场景资产",
            source_path="scenes",
            component="editable_cards",
            fields=(
                DisplayField("asset_code", "资产编码", "scenes[].asset_code", feedback_skill="asset-extract", required_for_display=True),
                DisplayField("name", "名称", "scenes[].name", editable=True, feedback_skill="asset-extract", required_for_display=True),
                DisplayField("priority", "优先级", "scenes[].priority", "select", True, "asset-extract", True),
                DisplayField("description", "场景描述", "scenes[].description", "textarea", True, "asset-extract"),
                DisplayField("spatial_zones", "空间分区", "scenes[].spatial_zones", "tag_list", True, "asset-extract"),
            ),
        ),
        DisplaySection(
            key="props",
            title="道具资产",
            source_path="props",
            component="editable_cards",
            fields=(
                DisplayField("asset_code", "资产编码", "props[].asset_code", feedback_skill="asset-extract", required_for_display=True),
                DisplayField("name", "名称", "props[].name", editable=True, feedback_skill="asset-extract", required_for_display=True),
                DisplayField("priority", "优先级", "props[].priority", "select", True, "asset-extract", True),
                DisplayField("prop_type", "道具类型", "props[].prop_type", "select", True, "asset-extract"),
                DisplayField("appearance", "外观", "props[].appearance", "textarea", True, "asset-extract"),
                DisplayField("related_scene_codes", "关联场景", "props[].related_scene_codes", "tag_list", True, "asset-extract"),
            ),
        ),
        DisplaySection(
            key="manual_review",
            title="资产复核",
            source_path="manual_review_items/notes",
            component="checklist",
            fields=(
                DisplayField("manual_review_items", "人工复核项", "manual_review_items[]", "table", True, "asset-extract"),
                DisplayField("notes", "抽取说明", "notes[]", "tag_list", True, "asset-extract"),
            ),
        ),
    ),
)


RELATION_CHECK_DISPLAY = SkillDisplayContract(
    skill="relation-check",
    primary_frontend_model="relation_check",
    sections=(
        DisplaySection(
            key="relation_report",
            title="关系校验摘要",
            source_path="relation_report",
            component="summary",
            fields=(
                DisplayField("ok", "校验结果", "relation_report.ok", "status", feedback_skill="relation-check", required_for_display=True),
                DisplayField("checked_counts", "检查数量", "relation_report.checked_counts", "metadata", feedback_skill="relation-check"),
                DisplayField("missing_links", "缺失关联", "relation_report.missing_links", "table", feedback_skill="relation-check"),
                DisplayField("weak_links", "弱关联", "relation_report.weak_links", "table", feedback_skill="relation-check"),
                DisplayField("suggested_repairs", "修复建议", "relation_report.suggested_repairs", "table", feedback_skill="relation-check"),
            ),
        ),
        DisplaySection(
            key="link_details",
            title="关联明细",
            source_path="storyboard_links/asset_links/plan_links",
            component="tables",
            fields=(
                DisplayField("storyboard_links", "分镜关联", "storyboard_links[]", "table", feedback_skill="relation-check"),
                DisplayField("asset_links", "资产关联", "asset_links[]", "table", feedback_skill="relation-check"),
                DisplayField("plan_links", "计划关联", "plan_links[]", "table", feedback_skill="relation-check"),
            ),
        ),
        DisplaySection(
            key="manual_review",
            title="关系复核",
            source_path="manual_review_items/delivery_checklist/notes",
            component="checklist",
            fields=(
                DisplayField("manual_review_items", "人工复核项", "manual_review_items[]", "table", True, "relation-check"),
                DisplayField("delivery_checklist", "交付检查", "delivery_checklist[]", "table", True, "relation-check"),
                DisplayField("notes", "校验说明", "notes[]", "tag_list", True, "relation-check"),
            ),
        ),
    ),
)


COMPLIANCE_REVIEW_DISPLAY = SkillDisplayContract(
    skill="content-compliance-review",
    primary_frontend_model="content_review",
    sections=(
        DisplaySection(
            key="risk_summary",
            title="风险摘要",
            source_path="risk_level/manual_review_items",
            component="summary",
            fields=(
                DisplayField("risk_level", "风险等级", "risk_level", feedback_skill="content-compliance-review", required_for_display=True),
                DisplayField("score", "评分", "score", "number", feedback_skill="content-compliance-review"),
                DisplayField("manual_review_items", "人工复核项", "manual_review_items[]", "table", True, "content-compliance-review"),
            ),
        ),
        DisplaySection(
            key="review_tables",
            title="审查明细",
            source_path="asset_review/storyboard_review/dialogue_review/period_consistency_review/prompt_safety_review",
            component="tables",
            fields=(
                DisplayField("asset_review", "资产审查", "asset_review[]", "table", True, "content-compliance-review"),
                DisplayField("storyboard_review", "分镜审查", "storyboard_review[]", "table", True, "content-compliance-review"),
                DisplayField("dialogue_review", "对白审查", "dialogue_review[]", "table", True, "content-compliance-review"),
                DisplayField("period_consistency_review", "年代一致性", "period_consistency_review[]", "table", True, "content-compliance-review"),
                DisplayField("prompt_safety_review", "提示词安全", "prompt_safety_review[]", "table", True, "content-compliance-review"),
            ),
        ),
        DisplaySection(
            key="mitigation",
            title="替换与降级策略",
            source_path="sensitive_replacements/safety_downgrade_chain",
            component="tables",
            fields=(
                DisplayField("sensitive_replacements", "敏感替换", "sensitive_replacements[]", "table", True, "content-compliance-review"),
                DisplayField("safety_downgrade_chain", "安全降级链", "safety_downgrade_chain[]", "table", True, "content-compliance-review", True),
            ),
        ),
    ),
)


TEXT_TO_IMAGE_DISPLAY = SkillDisplayContract(
    skill="text-to-image-prompt",
    primary_frontend_model="prompt_output",
    sections=(
        DisplaySection(
            key="prompt_editor",
            title="文生图 Prompt",
            source_path="prompt",
            component="prompt_editor",
            fields=(
                DisplayField("prompt", "正向提示词", "prompt", "textarea", True, "text-to-image-prompt", True),
                DisplayField("negative_prompt", "负向提示词", "negative_prompt", "textarea", True, "text-to-image-prompt"),
                DisplayField("aspect_ratio", "画幅", "aspect_ratio", editable=True, feedback_skill="text-to-image-prompt"),
                DisplayField("quality_tags", "画质标签", "quality_tags[]", "tag_list", True, "text-to-image-prompt"),
                DisplayField("style_keywords", "风格词", "style_keywords[]", "tag_list", True, "text-to-image-prompt"),
            ),
        ),
        DisplaySection(
            key="style_guides",
            title="材质与定装指南",
            source_path="material_layers/character_apose/view_prompts/fusion_rules/reference_images/manual_review_items",
            component="tables",
            fields=(
                DisplayField("material_layers", "材质分层", "material_layers", "json", True, "text-to-image-prompt"),
                DisplayField("character_apose", "A-Pose 定装", "character_apose", "json", True, "text-to-image-prompt"),
                DisplayField("view_prompts", "定装视图", "view_prompts[]", "table", True, "text-to-image-prompt"),
                DisplayField("fusion_rules", "多图融合规则", "fusion_rules", "json", True, "text-to-image-prompt"),
                DisplayField("reference_images", "参考图", "reference_images[]", "tag_list", True, "text-to-image-prompt"),
                DisplayField("manual_review_items", "人工复核项", "manual_review_items[]", "table", True, "text-to-image-prompt"),
            ),
        ),
    ),
)


def _asset_prompt_display(
    skill: str,
    title: str,
    primary_model: str,
    bible_key: str,
    bible_title: str,
    variant_key: str,
    variant_title: str,
) -> SkillDisplayContract:
    return SkillDisplayContract(
        skill=skill,
        primary_frontend_model=primary_model,
        sections=(
            DisplaySection(
                key="prompt",
                title=title,
                source_path="prompt/negative_prompt/aspect_ratio",
                component="prompt_editor",
                fields=(
                    DisplayField("prompt", "正向提示词", "prompt", "textarea", True, skill, True),
                    DisplayField("negative_prompt", "负向提示词", "negative_prompt", "textarea", True, skill),
                    DisplayField("aspect_ratio", "资产母版画幅", "aspect_ratio", editable=True, feedback_skill=skill),
                ),
            ),
            DisplaySection(
                key="production_structure",
                title=bible_title,
                source_path=bible_key,
                component="json",
                fields=(DisplayField(bible_key, bible_title, bible_key, "json", feedback_skill=skill),),
            ),
            DisplaySection(
                key="variants",
                title=variant_title,
                source_path=variant_key,
                component="nested_table",
                fields=(DisplayField(variant_key, variant_title, f"{variant_key}[]", "table", feedback_skill=skill),),
            ),
            DisplaySection(
                key="manual_review",
                title="人工复核项",
                source_path="manual_review_items",
                component="checklist",
                fields=(DisplayField("manual_review_items", "人工复核项", "manual_review_items[]", "table", feedback_skill=skill),),
            ),
        ),
    )


CHARACTER_DESIGN_PROMPT_DISPLAY = _asset_prompt_display(
    "character-design-prompt", "人物资产 Prompt", "asset_prompt_design", "view_prompts", "图位 Prompt", "view_prompts", "人物图位",
)
SCENE_DESIGN_PROMPT_DISPLAY = _asset_prompt_display(
    "scene-design-prompt", "场景资产 Prompt", "asset_prompt_design", "spatial_bible", "空间圣经", "view_prompts", "场景视角",
)
PROP_DESIGN_PROMPT_DISPLAY = _asset_prompt_display(
    "prop-design-prompt", "道具资产 Prompt", "asset_prompt_design", "material_bible", "材质圣经", "state_prompts", "道具状态",
)


IMAGE_TO_VIDEO_DISPLAY = SkillDisplayContract(
    skill="image-to-video-prompt",
    primary_frontend_model="prompt_output",
    sections=(
        DisplaySection(
            key="prompt_editor",
            title="图生视频 Prompt",
            source_path="motion_description/camera_movement/duration",
            component="prompt_editor",
            fields=(
                DisplayField("motion_description", "动作描述", "motion_description", "textarea", True, "image-to-video-prompt", True),
                DisplayField("camera_movement", "运镜", "camera_movement", "textarea", True, "image-to-video-prompt", True),
                DisplayField("duration", "时长", "duration", "number", True, "image-to-video-prompt", True),
                DisplayField("quality_preset", "画质预设", "quality_preset", editable=True, feedback_skill="image-to-video-prompt"),
                DisplayField("style_tags", "风格标签", "style_tags[]", "tag_list", True, "image-to-video-prompt"),
                DisplayField("constraint_words", "约束词", "constraint_words[]", "tag_list", True, "image-to-video-prompt"),
            ),
        ),
        DisplaySection(
            key="seedance_guides",
            title="空间、时间与编辑策略",
            source_path="jump_cut_points/spatial_layer/temporal_layer/extension_strategy/segment_plan/manual_review_items",
            component="tables",
            fields=(
                DisplayField("jump_cut_points", "跳切点", "jump_cut_points[]", "table", True, "image-to-video-prompt"),
                DisplayField("spatial_layer", "空间分层", "spatial_layer", "json", True, "image-to-video-prompt"),
                DisplayField("temporal_layer", "时间分层", "temporal_layer", "json", True, "image-to-video-prompt"),
                DisplayField("extension_strategy", "延长与拼接策略", "extension_strategy", "json", True, "image-to-video-prompt"),
                DisplayField("segment_plan", "分段计划", "segment_plan[]", "table", True, "image-to-video-prompt"),
                DisplayField("manual_review_items", "人工复核项", "manual_review_items[]", "table", True, "image-to-video-prompt"),
            ),
        ),
    ),
)


DISPLAY_CONTRACTS: dict[str, SkillDisplayContract] = {
    contract.skill: contract
    for contract in (
        SCRIPT_READING_DISPLAY,
        SCRIPT_SEGMENTATION_DISPLAY,
        SCRIPT_BREAKDOWN_DISPLAY,
        ASSET_EXTRACT_DISPLAY,
        RELATION_CHECK_DISPLAY,
        COMPLIANCE_REVIEW_DISPLAY,
        TEXT_TO_IMAGE_DISPLAY,
        CHARACTER_DESIGN_PROMPT_DISPLAY,
        SCENE_DESIGN_PROMPT_DISPLAY,
        PROP_DESIGN_PROMPT_DISPLAY,
        IMAGE_TO_VIDEO_DISPLAY,
    )
}


def validate_display_contract(contract: SkillDisplayContract) -> None:
    for section in contract.sections:
        for field in section.fields:
            feedback_skill = field.feedback_skill or contract.skill
            output_schema = get_skill_contract(feedback_skill).output_schema
            source_root = field.source_path.split(".", 1)[0].split("[]", 1)[0]
            if not source_root or source_root not in output_schema:
                raise ValueError(
                    f"{contract.skill} display field {field.source_path!r} "
                    f"is not declared by {feedback_skill} output contract"
                )


for _display_contract in DISPLAY_CONTRACTS.values():
    validate_display_contract(_display_contract)


def get_display_contract(skill: str) -> SkillDisplayContract:
    return DISPLAY_CONTRACTS[skill]

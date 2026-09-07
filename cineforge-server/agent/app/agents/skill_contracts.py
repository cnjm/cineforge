from __future__ import annotations

import hashlib
import json
import re
from dataclasses import dataclass, field
from typing import Any

from jsonschema import Draft202012Validator


@dataclass(frozen=True)
class SkillContract:
    skill: str
    version: str
    task: str
    requirements: tuple[str, ...]
    required_fields: tuple[str, ...]
    output_schema: dict[str, Any]
    nested_required: dict[str, tuple[str, ...]] = field(default_factory=dict)
    consumed_brief_fields: tuple[str, ...] = ()
    prompt_sections: dict[str, tuple[str, ...]] = field(default_factory=dict)
    affected_output_paths: dict[str, tuple[str, ...]] = field(default_factory=dict)

    @property
    def json_schema(self) -> dict[str, Any]:
        schema = {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "$id": f"urn:cineforge:skill-contract:{self.skill}:{self.version}",
            "title": f"{self.skill}@{self.version}",
            "type": "object",
            "properties": {
                field: _json_schema_for_descriptor(descriptor)
                for field, descriptor in self.output_schema.items()
            },
            "required": list(self.required_fields),
            "additionalProperties": True,
        }
        _apply_nested_required(schema, self.nested_required)
        return schema


@dataclass(frozen=True)
class SkillRuntimePolicy:
    skill: str
    node_weight: str
    run_scope: str
    rerun_scopes: tuple[str, ...]
    workflow_lane: str
    cache_policy: str
    retry_policy: str
    batch_size: int | None = None


SCRIPT_READING_CONTRACT = SkillContract(
    skill="script-reading",
    version="2.0.0",
    task="剧本阅读",
    requirements=(
        "阅读剧本原文，产出 ReadingReport v1 围读报告；不要生成分镜、出图 prompt 或视频 prompt。",
        "围读报告是后续人工确认、资产抽取和脚本段拆分的输入，只输出故事理解、平台策略、文化与风格、节奏、风险和人工复核项。",
        "relationship_map 中每条关系必须同时输出 from_role_name、from_role_code、to_role_name、to_role_code；R 编号只是关系引用码，不代表已创建生产资产。",
        "必须输出 cultural_origin：按角色名称、地名、时代用词、文化元素、称谓、建筑、道具和食物信号判断 classification、confidence、signals；冲突或不足时写入 manual_review_items。",
        "resolved_production_brief.cultural_contexts 是人工确认的文化与时代硬约束，优先级高于 Agent 推断；不得改写或合并掉已确认上下文。穿越、多时空项目必须保留全部上下文，供 asset-extract 为场景资产引用对应 context_code。",
        "必须使用 production-style-catalog reference 中的稳定 style_id。resolved_production_brief.primary_style_id 是人工确认的硬约束，不得覆盖；recommended_visual_style 只能复述该选择并解释执行方向。",
        "视觉优先级固定为：人工确认 primary_style_id > 人工确认文化与时代背景 > Agent 推荐 > 系统默认；style_modifiers 只能补充主风格，不能改变媒介、写实等级或覆盖主风格。",
        "不要输出 role_candidates、scene_candidates、prop_candidates、asset_change_proposals 或任何定装提示词；这些内容由 asset-extract 和 text-to-image-prompt 专用 Skill 负责。",
        "project、source、schema_version 由后端包装，不要编造 project_id、workflow_run_id 或数据库 ID。",
        "只返回 JSON 对象，不要 Markdown，不要解释文字。",
    ),
    required_fields=(
        "story_overview",
        "platform_strategy",
        "cultural_origin",
        "recommended_visual_style",
        "segmentation_guidance",
        "production_risks",
        "manual_review_items",
    ),
    output_schema={
        "story_overview": "object: logline, synopsis, core_conflict, main_hook, audience_promise, genre_tags",
        "platform_strategy": "object: primary_platform, aspect_ratio, target_duration_seconds, rhythm_strategy, composition_strategy, subtitle_strategy, shot_density",
        "relationship_map": "array<object: from_role_name, from_role_code, to_role_name, to_role_code, relationship, conflict, evidence>",
        "worldview": "object: time_period, power_system, social_order, rules, forbidden_misreads",
        "emotion_curve": "array<object: beat, story_position, emotion, intensity, visual_strategy>",
        "visual_style": "object: style_keywords, color_palette, lighting_rules, camera_language, forbidden_styles",
        "cultural_origin": "object: classification, confidence, region, era, signals=array<object:dimension,value,evidence,weight>, status",
        "recommended_visual_style": "object: primary_style_id, alternative_style_ids=array<string>, modifier_ids=array<string>, reason, confidence, status",
        "segmentation_guidance": "object: recommended_episode_count, target_duration_seconds, segment_strategy, opening_hook_requirement, segment_density, must_keep_together, must_split_before",
        "production_risks": "array<object: risk_type, severity, description, affected_assets, suggestion>",
        "manual_review_items": "array<object: field, severity, question, suggestion>",
    },
    consumed_brief_fields=(
        "content_type", "delivery_aspect_ratio", "target_duration_seconds", "opening_hook_seconds",
        "primary_style_id", "resolved_style", "cultural_contexts", "primary_cultural_context_code",
        "narrative_grammar_by_context", "document_type", "fidelity_mode",
    ),
    prompt_sections={
        "项目硬约束": ("content_type", "document_type", "fidelity_mode"),
        "平台制作策略": ("delivery_aspect_ratio", "target_duration_seconds", "opening_hook_seconds"),
        "文化时代与视觉风格": ("primary_style_id", "resolved_style", "cultural_contexts", "primary_cultural_context_code", "narrative_grammar_by_context"),
    },
    affected_output_paths={
        "content_type": ("story_overview", "platform_strategy"),
        "delivery_aspect_ratio": ("platform_strategy.aspect_ratio", "platform_strategy.composition_strategy"),
        "target_duration_seconds": ("platform_strategy.target_duration_seconds", "segmentation_guidance"),
        "opening_hook_seconds": ("segmentation_guidance.opening_hook_requirement",),
        "resolved_style": ("recommended_visual_style", "visual_style"),
        "cultural_contexts": ("cultural_origin", "worldview", "production_risks"),
        "narrative_grammar_by_context": ("emotion_curve", "segmentation_guidance"),
        "fidelity_mode": ("story_overview", "production_risks"),
    },
)


SCRIPT_BREAKDOWN_CONTRACT = SkillContract(
    skill="script-breakdown",
    version="2.1.2",
    task="分镜拆解",
    requirements=(
        "只基于 confirmed_script_segments 和 normalized_asset_inventory 生成分镜；不得重新拆分脚本段，不得创建、改写或重新编号正式资产。",
        "每条分镜必须原样引用 confirmed_script_segments[].script_segment_code，并且角色、场景、道具只能引用 normalized_asset_inventory.assets 中存在的正式 asset_code。",
        "每条分镜必须同时输出 scene_code 和 scene_name；scene_code 必须引用 normalized_asset_inventory.assets 中的正式场景 asset_code，scene_name 必须逐字使用该资产对应的正式名称，禁止只输出编号或自造名称。",
        "发现正式资产缺失时只输出 missing_asset_suggestions，包含证据、优先级和建议，不得把建议混入 characters、scenes、props 或 assets。",
        "每条父分镜时长必须为 10-15 秒，缺失或越界视为结构错误，不交给内容审查 Skill 兜底。镜中分镜不受 10 秒下限约束，但必须大于 0 且总和等于父分镜时长。",
        "父分镜是一次完整的 10-15 秒视频生成单元；同一图片来源、同一空间和连续对白优先合并为一个父分镜，在 mirror_shots / 镜中分镜中记录内部硬切，不得把镜中分镜提升成 3-9 秒父分镜。",
        "只要使用 mirror_shots，就必须输出 2-6 个完整内部镜头且至少包含 A/B；相邻子镜头必须采用不同景别或角度形成明确切换，禁止单一连续长镜头冒充镜中分镜。",
        "每个镜中分镜必须输出 id、shot_function、description、camera、movement_reason、shot_size、duration_seconds、dialogue、characters、environmental_pressure、micro_action、sound_or_motif。子镜头时长允许小数且总和必须等于父分镜 duration_seconds。没有内部镜头时输出 []，禁止只输出 A。",
        "父分镜存在 dialogue 且使用 mirror_shots 时，必须按发生顺序将完整原文台词行分配到 A/B/C 的 dialogue；说话人前缀（如 Win：）、字词和全半角标点都属于原文，承载该行的子镜头必须逐字复制整行，禁止只保留冒号后的对白正文。禁止截断一行台词或把一行拆给多个子镜头，无台词的子镜头写空字符串。子镜头 dialogue 依次合并后必须与父分镜 dialogue 完全一致，禁止漏句、重复、换序、摘要或改写。",
        "每个镜中分镜必须承担至少一个镜头职能：Establish、Reveal、Power、Pressure、Detail、Reaction、Shift、Impact、Aftermath、Exit；画面必须同时包含环境压力、人物身体微动作、声音或视觉母题，camera 的运动必须由信息、动作或情绪变化触发。",
        "每个镜中分镜只允许一个主要运镜；对白镜头必须先写可见动作再承接完整台词，确保人物、空间、道具状态和视线方向在 A/B/C 之间连续。",
        "所有 array 字段无内容时必须输出 []，禁止输出 null。",
        "不要一行台词拆一个分镜；连续 2-5 句对白可保留在同一分镜的 dialogue 字段中，按换行保留原文台词。",
        "台词不做删减；仅当画面、空间、人物调度或情绪目标明显变化时才新建分镜，优先输出核心可生产分镜。",
        "分镜描述要具体到画面、动作、情绪、环境，不要只写泛泛的剧情摘要。",
        "所有分镜、图片和视频计划必须带可追溯关联 ID。",
        "正式分镜编号只使用 project-episode-Fxxx；禁止输出 shot_no、V001 或 J001-V001。段内顺序只写 order_num，由后台生成正式分镜编号。",
        "关键帧编号使用 storyboard_code-KF01，视频编号使用 storyboard_code-VID01；VID 不得缩写为 V，避免与人物装扮变体 V01 冲突。",
        "shot_size 必须输出中文景别，camera 必须输出中文机位、角度、运镜和构图；禁止 ECU、CU、MCU、MS、LS 等英文缩写进入标准化字段。",
        "manual_review_items 必须结构化标注 scope_type(storyboard|script_segment|episode) 和 scope_code；不要只在自由文本中写 F/J/EP 编号。",
        "storyboard_shots、coverage_checks、scene_packages 由后端根据分镜和已确认资产归一化；Agent 可以输出但不能改写资产主数据。",
    ),
    required_fields=(
        "storyboards",
        "manual_review_items",
    ),
    output_schema={
        "storyboards": "array<object: storyboard_code, script_segment_code, order_num, shot_size(必填中文景别), duration_seconds(10-15), characters=array, camera(中文镜头执行), description, dialogue, task_type, mirror_shots=array<object: id(A|B|C|D|E|F), shot_function(Establish|Reveal|Power|Pressure|Detail|Reaction|Shift|Impact|Aftermath|Exit), description, camera(中文), movement_reason, shot_size(必填中文), duration_seconds(number), dialogue(完整原文台词行), characters=array, environmental_pressure, micro_action, sound_or_motif>, episode_code, scene_code, scene_name, keyframe_code(storyboard_code-KF01), video_code(storyboard_code-VID01)>",
        "storyboard_shots": "array<object: shot_code(SH001), storyboard_code, script_segment_code, scene_code, scene_name, shot_function(Establish|Reveal|Power|Pressure|Detail|Reaction|Shift|Impact|Aftermath|Exit), shot_size, camera_direction, required_zones=array<string>, visible_props=array<string>, role_codes=array<string>, prop_codes=array<string>, mood, rhythm, action, dialogue, duration_seconds, environmental_pressure, micro_action, sound_or_motif, storyboard_prompt>",
        "coverage_checks": "array<object: shot_code, storyboard_code, scene_code, scene_name, coverage_status(covered|needs_view|needs_variant|needs_new_scene), missing_requirements=array<string>, suggested_asset=object>",
        "scene_packages": "array<object: scene_code, scene_name, priority(S|A|B|C), storyboard_shots=array<string>, storyboard_codes=array<string>, required_roles=array<string>, required_props=array<string>, required_scene_views=array<string>, coverage_status(ready_for_assignment|needs_assets), blocking_items=array<string>, tasks=array<object: task_type, title>>",
        "missing_asset_suggestions": "array<object: asset_type, name, priority(S|A|B|C), evidence, blocking, suggestion>",
        "keyframe_plan": "array<object: round, keyframe_code, storyboard_code, title, purpose, priority, seed_for_video>",
        "video_plan": "array<object: video_code, shot_range, seed_keyframes, title, mode, duration_seconds>",
        "delivery_checklist": "array<object: category, item, status, note>",
        "manual_review_items": "array<object: item, detail, source, scope_type(storyboard|script_segment|episode), scope_code>",
        "notes": "array",
        "source": "object",
        "workflow": "object",
    },
    nested_required={
        "storyboards[]": (
            "script_segment_code",
            "order_num",
            "shot_size",
            "duration_seconds",
            "characters",
            "camera",
            "description",
            "dialogue",
            "mirror_shots",
            "scene_code",
            "scene_name",
        ),
        "storyboards[].mirror_shots[]": (
            "id",
            "description",
            "camera",
            "shot_size",
            "duration_seconds",
            "dialogue",
            "characters",
        ),
    },
    consumed_brief_fields=(
        "content_type", "delivery_aspect_ratio", "target_duration_seconds", "opening_hook_seconds",
        "resolved_style", "cultural_contexts", "narrative_grammar_by_context", "fidelity_mode",
    ),
    prompt_sections={
        "分镜节奏预算": ("delivery_aspect_ratio", "target_duration_seconds", "opening_hook_seconds", "narrative_grammar_by_context"),
        "画面生产边界": ("content_type", "resolved_style", "cultural_contexts", "fidelity_mode"),
    },
    affected_output_paths={
        "delivery_aspect_ratio": ("storyboards.camera", "storyboards.shot_size"),
        "target_duration_seconds": ("storyboards.duration_seconds",),
        "narrative_grammar_by_context": ("storyboards", "storyboard_shots"),
        "resolved_style": ("storyboards.description", "storyboard_shots.storyboard_prompt"),
    },
)


SCRIPT_SEGMENTATION_CONTRACT = SkillContract(
    skill="script-segmentation",
    version="1.3.1",
    task="脚本段拆分",
    requirements=(
        "只负责把剧本原文拆成可追溯脚本段，不生成分镜、资产、关键帧或视频计划。",
        "按场次、空间变化、剧情动作、人物目标或情绪节拍切分，不要按每一句台词机械切分。",
        "每个脚本段必须保留 source_text 原文依据，summary 只做概括，不能改写 source_text。",
        "每个脚本段必须输出 dialogue_lines，按原文顺序保存本段全部台词块；每个台词块必须从说话人行开始，并逐字包含说话人前缀（如 Win：）、紧随的表演括注和完整对白正文，保留原始换行与全半角标点。一整个台词块是不可拆分语义单元，禁止截断、摘要、改写、重复、换序或只保留对白正文，无台词时输出 []。",
        "每段必须输出 estimated_duration_seconds、role_refs、scene_refs、prop_refs、production_notes；所有资产引用只能来自 normalized_asset_inventory.assets 中的正式 asset_code。",
        "输出段数要适合后续逐段分镜；长动作段可以拆开，连续对白同场景可合并。",
        "每段必须输出 context_code；动真结合项目还必须输出 render_mode=animated|live_action，单段不得混合两种表现域。",
        "所有 array 字段无内容时必须输出 []，禁止输出 null。",
        "只返回 JSON 对象，不要 Markdown，不要解释文字。",
    ),
    required_fields=("script_segments",),
    output_schema={
        "script_segments": "array<object: episode_code, order_no, title, source_text, dialogue_lines=array<string>, summary, story_function, dominant_emotion, rhythm, viewpoint, context_code, render_mode(animated|live_action), estimated_duration_seconds, role_refs=array<string>, scene_refs=array<string>, prop_refs=array<string>, production_notes=array<string>>",
        "segmentation_notes": "array",
        "manual_review_items": "array",
        "source": "object",
    },
    nested_required={
        "script_segments[]": (
            "source_text",
            "dialogue_lines",
            "estimated_duration_seconds",
            "role_refs",
            "scene_refs",
            "prop_refs",
            "production_notes",
        ),
    },
    consumed_brief_fields=(
        "content_type", "target_duration_seconds", "opening_hook_seconds", "primary_cultural_context_code",
        "narrative_grammar_by_context", "fidelity_mode",
    ),
    prompt_sections={
        "脚本段预算": ("target_duration_seconds", "opening_hook_seconds", "narrative_grammar_by_context"),
        "内容边界": ("content_type", "primary_cultural_context_code", "fidelity_mode"),
    },
    affected_output_paths={
        "target_duration_seconds": ("script_segments.estimated_duration_seconds",),
        "opening_hook_seconds": ("script_segments.story_function",),
        "narrative_grammar_by_context": ("script_segments.rhythm", "script_segments.story_function"),
        "fidelity_mode": ("script_segments.source_text",),
    },
)


CONTENT_COMPLIANCE_CONTRACT = SkillContract(
    skill="content-compliance-review",
    version="1.0.0",
    task="内容合规审查",
    requirements=(
        "进行敏感词合规检查与替换。",
        "校验年代一致性，识别古今元素冲突或设定不一致。",
        "对暴力、政治、色情内容给出安全降级策略。",
        "必须返回 5 级降级链：带动作细节、画面静态定帧、只拍反应、画外音、硬切。",
        "manual_review_items 必须标注 scope_type(storyboard|script_segment|episode) 和 scope_code，确保提醒能归属具体 F 分镜、J 脚本段或整集。",
        "只返回 JSON 对象，不要 Markdown，不要解释文字。",
    ),
    required_fields=(
        "asset_review",
        "storyboard_review",
        "dialogue_review",
        "period_consistency_review",
        "prompt_safety_review",
        "sensitive_replacements",
        "safety_downgrade_chain",
        "manual_review_items",
        "risk_level",
    ),
    output_schema={
        "asset_review": "array",
        "storyboard_review": "array",
        "dialogue_review": "array",
        "period_consistency_review": "array",
        "prompt_safety_review": "array",
        "sensitive_replacements": "array<object: keyword, replacement, strategy>",
        "safety_downgrade_chain": "array<object: level, strategy, instruction> levels: 动作细节/静态定帧/只拍反应/画外音/硬切",
        "missing_items": "array",
        "manual_review_items": "array<object|string: item, detail, source, scope_type(storyboard|script_segment|episode), scope_code>",
        "risk_level": "string",
        "score": "integer",
    },
)


ASSET_EXTRACT_CONTRACT = SkillContract(
    skill="asset-extract",
    version="2.1.0",
    task="资产抽取",
    requirements=(
        "从剧本原文和已确认围读报告中抽取人物、场景、道具资产，不依赖脚本段，不虚构没有原文依据的资产。",
        "资产编码由后台统一生成；人物使用 R001/R002，场景使用 SC001/SC002，道具使用 P001/P002；人物类型必须单独输出 character_type。",
        "服装不允许使用独立编码体系；服装类资产必须作为 props 输出，asset_type=prop，prop_type=服饰，asset_code 使用 P001/P002。",
        "场景只允许输出可独立建立空间母版的环境；茶几、沙发、靠垫、门、灯具等室内物件必须输出到 props，禁止作为 scenes。",
        "同一人物身份只能输出一个人物母版；不同分集重复出现的人物保留同一候选身份，由后台 existing_asset_master_snapshot 匹配正式母版。",
        "人物必须输出 visual_presence：on_screen 表示本集实际出现在画面中，voice_only 表示只有画外音/声音没有画面，mentioned_only 表示只在对白、旁白或背景设定中被提及但本集未出场；非 on_screen 只登记资产和正式编号，不生成角色定装 Prompt 或任务，后续首次 on_screen 时再补生成。无法判断时使用 on_screen。",
        "同一道具的不同状态只能输出一个道具母版；例如‘威士忌’与‘威士忌（含冰块）’统一命名为‘威士忌’，将‘含冰块’写入 status_change。",
        "所有资产必须输出 priority，取值只能是 S、A、B、C；无法判断时输出 C。",
        "每个人物必须输出 has_dialogue；本集有明确对白或画外音台词时为 true，否则为 false。有台词时同时输出 dialogue_evidence，逐项保留说话人、场景或原文短证据，不得仅按出场推断。角色音色只会为 has_dialogue=true 的 S/A 级人物创建。",
        "当前阶段可能还没有分镜；如果没有 storyboards，related_storyboard_codes 必须输出 []，不要解释缺少分镜。",
        "初次抽取时 related_script_segment_codes 输出 []；脚本段确认后只能提出关联增量，不得重建或覆盖正式资产。",
        "每个人物必须优先输出 related_scene_codes 和 scene_names，场景编号引用本次 scenes 中的稳定 asset_code；只有 Agent 无法确定时才留空交由人工补充。",
        "每个场景必须输出 context_code；动真结合项目的场景必须输出 render_mode=animated|live_action，同一场景不得混合两种表现域。",
        "每个道具必须优先输出 related_scene_codes 和 scene_names，场景编号引用本次 scenes 中的稳定 asset_code；只有 Agent 无法确定时才留空交由人工补充。",
        "owner_character 和 owner_character_id 始终可空；仅服饰、配饰在原文有明确穿戴或佩戴关系时可输出，普通道具不得因缺少归属人物生成 manual_review_items，也不得阻塞资产确认或后续流程。",
        "输出 characters、scenes、props 和统一 assets 数组；不确定的信息写入 manual_review_items。",
        "资产字段必须按前端资产表归一化输出：展示字段只放名称/描述，ID 字段单独输出；多值字段必须输出数组，不要用分隔符拼成字符串。",
        "即使没有识别到某类资产，也必须输出空数组，不要输出文字说明。",
        "只返回 JSON 对象，不要 Markdown，不要解释文字。",
    ),
    required_fields=(
        "assets",
        "characters",
        "scenes",
        "props",
    ),
    output_schema={
        "assets": "array<object: asset_type(character|scene|prop), asset_code, name, description, related_storyboard_codes, related_script_segment_codes, source_text, metadata>",
        "characters": "array<object: asset_code, asset_type=character, name, character_type, visual_presence(on_screen|voice_only|mentioned_only), priority(S|A|B|C), has_dialogue=boolean, dialogue_evidence=array<object|string>, context_codes=array<string>, relationship, role, appearance, core_requirement, related_scene_codes=array<string>, scene_names=array<string>, scene_ids=array<string>, related_storyboard_codes, related_script_segment_codes>",
        "scenes": "array<object: asset_code, asset_type=scene, name, priority(S|A|B|C), context_code, render_mode(animated|live_action), time, description, narrative_function, spatial_zones=array<string>, visual_goal, key_props=array<string>, key_prop_ids=array<string>, continuity_risks=array<string>, episode_code, related_storyboard_codes, related_script_segment_codes>",
        "props": "array<object: asset_code, asset_type=prop, name, prop_type(服饰|配饰|武器|车辆|电子设备|生活用品|家具陈设|文件信物|食物饮品|其他), context_codes=array<string>, appearance, source, priority(S|A|B|C), status_change, owner_character, owner_character_id, related_scene_codes=array<string>, scene_names=array<string>, scene_ids=array<string>, related_storyboard_codes, related_script_segment_codes>",
        "manual_review_items": "array",
        "notes": "array",
        "source": "object",
        "workflow": "object",
    },
    nested_required={
        "characters[]": ("name", "character_type", "priority", "has_dialogue"),
        "scenes[]": ("name", "priority"),
        "props[]": ("name", "prop_type", "priority"),
    },
    consumed_brief_fields=(
        "content_type", "primary_style_id", "resolved_style", "cultural_contexts",
        "primary_cultural_context_code", "narrative_grammar_by_context", "fidelity_mode",
    ),
    prompt_sections={
        "资产抽取边界": ("content_type", "fidelity_mode"),
        "文化时代归属": ("cultural_contexts", "primary_cultural_context_code", "narrative_grammar_by_context"),
        "视觉生产分类": ("primary_style_id", "resolved_style"),
    },
    affected_output_paths={
        "content_type": ("characters", "scenes", "props"),
        "cultural_contexts": ("characters.context_codes", "scenes.context_code", "props.context_codes"),
        "resolved_style": ("scenes.render_mode", "assets.metadata"),
        "fidelity_mode": ("assets.source_text",),
    },
)


RELATION_CHECK_CONTRACT = SkillContract(
    skill="relation-check",
    version="1.0.0",
    task="关系校验",
    requirements=(
        "校验脚本原文段、分镜、人物、场景、道具、出图计划和视频计划之间的关联关系。",
        "检查每个分镜是否有有效 script_segment_code，每个资产是否有关联分镜和脚本原文段。",
        "检查 keyframe_plan、video_plan 是否能追溯到 storyboard_code、keyframe_code、video_code。",
        "不要改写主内容，只输出关系报告、缺失项、修复建议和人工复核项。",
        "只返回 JSON 对象，不要 Markdown，不要解释文字。",
    ),
    required_fields=(
        "relation_report",
        "manual_review_items",
    ),
    output_schema={
        "relation_report": "object: ok, checked_counts, missing_links, weak_links, suggested_repairs",
        "storyboard_links": "array<object: storyboard_code, script_segment_code, asset_codes, status, issues>",
        "asset_links": "array<object: asset_code, related_storyboard_codes, related_script_segment_codes, status, issues>",
        "plan_links": "array<object: target_code, target_type, storyboard_code, status, issues>",
        "manual_review_items": "array",
        "delivery_checklist": "array<object: category, item, status, note>",
        "notes": "array",
        "source": "object",
        "workflow": "object",
    },
)


TEXT_TO_IMAGE_PROMPT_CONTRACT = SkillContract(
    skill="text-to-image-prompt",
    version="1.0.0",
    task="生成 Seedream / libimage 文生图 Prompt",
    requirements=(
        "当前前端只支持 Seedream 和 libimage 两个文生图模型；不要输出即梦、Nano banana、GPT、Gemini 或其他模型建议。",
        "提示词正文 prompt 必须使用中文，描述主体、场景、动作、情绪、构图和镜头需求。",
        "负面词 negative_prompt 必须使用中文，描述不要出现的内容、错误肢体、水印、文字、低质画面等。",
        "画质标签 quality_tags 和风格词 style_keywords 必须使用英文短语，例如 high detail、cinematic composition、consistent character design、clean background。",
        "如需在 prompt 末尾补充英文，只能补充画质、风格、光线、构图类短标签；不要把主体叙事改成英文。",
        "材质分层写法：角色材质、环境材质、光照材质分开描述。",
        "角色 A-Pose 定装模板：正面站立、双臂展开、全身可见。",
        "当输入 task_type=costume 时必须读取 output_spec：A-E 输出全部五个图位；A、B、C、D、E 单个代码只输出对应图位。每个实际图位都必须包含可独立执行的完整 prompt 和 negative_prompt，禁止只输出公共提示词再让前端拼接。非角色定装任务可以返回空数组。",
        "当输入包含 CharacterProductionContext.v1 时，它是当前唯一生产上下文：只能使用 character_identity、age_stage、costume_variant、resolved_style、view_requirements 和 studio_requirement；禁止补入未在当前上下文出现的其他年龄、装扮、剧情动作或场景叙事。",
        "production_context.manual_review_items 仅用于返回人工复核信息，禁止把其中的问题、推测、问号、备选项或 AI 建议写入正向提示词。",
        "costume_variant 只描述当前装扮。若结构、颜色或材质字段缺失，应生成 manual_review_items，不得从其他装扮或剧情动作推断并混入当前提示词。",
        "必须逐项读取 view_requirements；每个 code 的 prompt、pose、description 和 reference_requirement 只能对应当前 code，禁止合并其他图位。",
        "所有已识别人物都必须有有效定装提示词：S/A 通常使用 A-E，B/C 至少使用 A；不得因群演、背影或本集戏份少而返回空 prompt。",
        "view_prompts 中 A 为正面全身 A-Pose，B 为侧面或 3/4 全身，C 为背面全身，D 为面部与表情参考，E 为服装与材质细节；B-E 必须明确引用已确认的图 A 保持角色一致性。",
        "每个实际 view_prompts.prompt 都必须独立包含：角色身份、当前年龄阶段、脸型五官、体型、发型肤质、当前装扮的内外层结构/颜色/材质/配饰、本图位构图与机位、本图位姿态与表情、中性摄影棚背景与布光、跨图连续性约束；不得依赖公共 prompt 才能执行。",
        "pose 只描述当前图位的人体姿态、动作和表情，不得把 A-E 的方位或构图汇总在一个字段；description 只描述当前图位的角度、景别、构图和制作目的。A 的 pose 只能是正面全身 A-Pose，不得包含侧面、背面、面部近景或材质细节要求。",
        "每个实际图位都必须给出可直接执行的中文 negative_prompt，至少约束身份漂移、年龄漂移、骨相/五官/体型/发型变化、装扮结构或材质变化、错误肢体、裁切遮挡、透视畸变、模糊、文字和水印。",
        "JSON 字段名使用约定的英文 key，但 prompt、negative_prompt、pose、description、reference_requirement 的字段值必须使用中文。",
        "组图叙事能力：多图融合规则、输出尺寸策略。",
        "提示词必须方便执行人员复制到 Seedream 或 libimage 工具。",
        "不确定信息写入 manual_review_items，单项必须包含 asset_code、asset_name、issue_type、uncertainty、suggestion、target、target_field、status；target_field 必须指向可人工回填的规范字段，uncertainty 只描述缺失或冲突事实，suggestion 只写可执行的生产建议。",
        "所有 array 字段无内容时必须输出 []，禁止输出 null。",
    ),
    required_fields=(
        "prompt",
        "negative_prompt",
        "aspect_ratio",
    ),
    output_schema={
        "prompt": "string",
        "negative_prompt": "string",
        "style_keywords": "array",
        "material_layers": "object?",
        "aspect_ratio": "string",
        "quality_tags": "array",
        "reference_images": "array?",
        "character_apose": "object?",
        "view_prompts": "array<{code:string,title:string,prompt:string,negative_prompt:string,pose:string,description:string,reference_requirement:string}>",
        "fusion_rules": "object?",
        "manual_review_items": "array",
        "skill": "text-to-image-prompt",
        "source_label": "agent",
    },
    nested_required={
        "view_prompts[]": (
            "code",
            "prompt",
            "negative_prompt",
            "pose",
            "description",
            "reference_requirement",
        ),
    },
    consumed_brief_fields=(
        "content_type", "delivery_aspect_ratio", "primary_style_id", "resolved_style",
        "cultural_contexts", "primary_cultural_context_code",
    ),
    prompt_sections={
        "交付构图": ("delivery_aspect_ratio",),
        "媒介与风格": ("content_type", "primary_style_id", "resolved_style"),
        "文化时代": ("cultural_contexts", "primary_cultural_context_code"),
    },
    affected_output_paths={
        "delivery_aspect_ratio": ("aspect_ratio", "prompt"),
        "resolved_style": ("prompt", "negative_prompt", "style_keywords"),
        "cultural_contexts": ("prompt", "manual_review_items"),
    },
)


CHARACTER_DESIGN_PROMPT_CONTRACT = SkillContract(
    skill="character-design-prompt",
    version="1.2.0",
    task="生成人物资产母版提示词",
    requirements=(
        "输入 production_context 必须是 CharacterProductionContext.v1，并且是当前人物唯一允许使用的生产上下文。",
        "只生成当前人物、年龄阶段和装扮版本的 A 图母版；不得混入其他年龄、装扮、场景动作或未确认推测。",
        "prompt、negative_prompt、pose、description、reference_requirement 必须使用中文。",
        "A 图必须为正面全身 A-Pose、中性摄影棚背景，完整呈现身份、骨相、体型、发型、肤质、服装层次、颜色和材质。",
        "不确定内容只能写入 manual_review_items，不能写入正向提示词。",
        "只返回符合 JSON Schema 的 JSON 对象，不要 Markdown。",
    ),
    required_fields=("asset_code", "context_key", "prompt", "negative_prompt", "aspect_ratio", "view_prompts", "manual_review_items"),
    output_schema={
        "asset_code": "string",
        "asset_type": "character",
        "context_key": "string",
        "prompt": "string",
        "negative_prompt": "string",
        "aspect_ratio": "string",
        "view_prompts": "array<{code:string,title:string,prompt:string,negative_prompt:string,pose:string,description:string,reference_requirement:string}>",
        "material_layers": "object?",
        "identity_anchors": "array<string>",
        "manual_review_items": "array<object: asset_code, asset_name, issue_type, uncertainty, suggestion, target, target_field, status>",
    },
    nested_required={
        "view_prompts[]": ("code", "prompt", "negative_prompt", "pose", "description", "reference_requirement"),
    },
    consumed_brief_fields=(
        "content_type", "asset_master_aspect_ratio", "primary_style_id", "resolved_style",
        "cultural_contexts", "primary_cultural_context_code",
    ),
    prompt_sections={
        "媒介与风格": ("content_type", "primary_style_id", "resolved_style"),
        "文化时代约束": ("cultural_contexts", "primary_cultural_context_code"),
        "资产母版规格": ("asset_master_aspect_ratio",),
    },
    affected_output_paths={
        "content_type": ("prompt", "negative_prompt"),
        "asset_master_aspect_ratio": ("aspect_ratio",),
        "primary_style_id": ("prompt", "view_prompts"),
        "resolved_style": ("prompt", "negative_prompt", "material_layers", "view_prompts"),
        "cultural_contexts": ("prompt", "material_layers", "manual_review_items"),
        "primary_cultural_context_code": ("prompt", "manual_review_items"),
    },
)


SCENE_DESIGN_PROMPT_CONTRACT = SkillContract(
    skill="scene-design-prompt",
    version="1.2.0",
    task="生成场景资产母版提示词",
    requirements=(
        "输入 production_context 必须是 SceneProductionContext.v1；只描述当前场景，不得创建人物或独立道具资产。",
        "生成可复用的场景空间母版，明确空间分区、动线、主机位、建筑与陈设边界、光照基准和连续性锚点。",
        "MASTER 为场景标准母版；view_prompts 中的视角名称和角度描述必须使用中文。",
        "如果 production_context.variant_requirement 存在，只生成该 variant_code 对应的一条视角 Prompt；title、description 和角度说明沿用其中的中文要求。",
        "不得把茶几、沙发、靠垫、灯具等物件误写成独立场景，它们只能作为空间内陈设约束。",
        "不确定内容只能写入 manual_review_items；只返回符合 JSON Schema 的 JSON 对象。",
    ),
    required_fields=("asset_code", "context_key", "prompt", "negative_prompt", "aspect_ratio", "view_prompts", "manual_review_items"),
    output_schema={
        "asset_code": "string",
        "asset_type": "scene",
        "context_key": "string",
        "prompt": "string",
        "negative_prompt": "string",
        "aspect_ratio": "string",
        "spatial_bible": "object: zones=array<string>, circulation, fixed_anchors=array<string>, lighting_baseline, continuity_rules=array<string>",
        "view_prompts": "array<{code:string,title:string,prompt:string,negative_prompt:string,camera_angle:string,framing:string,description:string,reference_requirement:string}>",
        "manual_review_items": "array<object: asset_code, asset_name, issue_type, uncertainty, suggestion, target, target_field, status>",
    },
    nested_required={
        "view_prompts[]": ("code", "title", "prompt", "negative_prompt", "camera_angle", "framing", "description", "reference_requirement"),
    },
    consumed_brief_fields=(
        "content_type", "asset_master_aspect_ratio", "primary_style_id", "resolved_style",
        "cultural_contexts", "primary_cultural_context_code", "narrative_grammar_by_context",
    ),
    prompt_sections={
        "媒介与渲染": ("content_type", "primary_style_id", "resolved_style"),
        "文化时代与空间语法": ("cultural_contexts", "primary_cultural_context_code", "narrative_grammar_by_context"),
        "场景母版规格": ("asset_master_aspect_ratio",),
    },
    affected_output_paths={
        "content_type": ("prompt", "negative_prompt"),
        "asset_master_aspect_ratio": ("aspect_ratio",),
        "primary_style_id": ("prompt", "view_prompts"),
        "resolved_style": ("prompt", "spatial_bible.lighting_baseline", "view_prompts"),
        "cultural_contexts": ("prompt", "spatial_bible.fixed_anchors"),
        "primary_cultural_context_code": ("prompt", "spatial_bible.fixed_anchors"),
        "narrative_grammar_by_context": ("view_prompts",),
    },
)


PROP_DESIGN_PROMPT_CONTRACT = SkillContract(
    skill="prop-design-prompt",
    version="1.2.0",
    task="生成道具资产母版提示词",
    requirements=(
        "输入 production_context 必须是 PropProductionContext.v1；只描述当前道具，不得创建场景或人物。",
        "生成可复用道具母版，明确外形、比例、材质、颜色、纹理、年代与文化特征和默认状态。",
        "MASTER 为标准状态；不同状态写入 state_prompts，不能拆成重复道具资产。",
        "state_prompts 的状态名称和描述必须使用中文，状态代码使用 Pxxx-Vxxx 约定中的 Vxxx 部分。",
        "如果 production_context.variant_requirement 存在，只生成该 variant_code 对应的一条状态 Prompt；title、description 和状态说明沿用其中的中文要求。",
        "asset_identity.owner_character 和 asset_identity.owner_role_code 始终可空；仅服饰、配饰可将已确认归属作为提示上下文。owner_character 传中文人物名，owner_role_code 传 Rxxx 短码；缺失时不得生成补全项或阻塞结果。",
        "不确定内容只能写入 manual_review_items；只返回符合 JSON Schema 的 JSON 对象。",
    ),
    required_fields=("asset_code", "context_key", "prompt", "negative_prompt", "aspect_ratio", "state_prompts", "manual_review_items"),
    output_schema={
        "asset_code": "string",
        "asset_type": "prop",
        "context_key": "string",
        "prompt": "string",
        "negative_prompt": "string",
        "aspect_ratio": "string",
        "material_bible": "object: shape, scale, materials=array<string>, colors=array<string>, textures=array<string>, continuity_rules=array<string>",
        "state_prompts": "array<{code:string,title:string,prompt:string,negative_prompt:string,description:string,reference_requirement:string}>",
        "manual_review_items": "array<object: asset_code, asset_name, issue_type, uncertainty, suggestion, target, target_field, status>",
    },
    nested_required={
        "state_prompts[]": ("code", "title", "prompt", "negative_prompt", "description", "reference_requirement"),
    },
    consumed_brief_fields=(
        "content_type", "asset_master_aspect_ratio", "primary_style_id", "resolved_style",
        "cultural_contexts", "primary_cultural_context_code",
    ),
    prompt_sections={
        "媒介与材质风格": ("content_type", "primary_style_id", "resolved_style"),
        "文化时代约束": ("cultural_contexts", "primary_cultural_context_code"),
        "道具母版规格": ("asset_master_aspect_ratio",),
    },
    affected_output_paths={
        "content_type": ("prompt", "negative_prompt"),
        "asset_master_aspect_ratio": ("aspect_ratio",),
        "primary_style_id": ("prompt", "state_prompts"),
        "resolved_style": ("prompt", "material_bible", "state_prompts"),
        "cultural_contexts": ("prompt", "material_bible"),
        "primary_cultural_context_code": ("prompt", "material_bible"),
    },
)


IMAGE_TO_VIDEO_PROMPT_CONTRACT = SkillContract(
    skill="image-to-video-prompt",
    version="1.0.0",
    task="生成 Seedance / 可灵 图生视频 Prompt",
    requirements=(
        "当前前端只支持 Seedance 和可灵两个图生视频模型；不要输出 Runway、Veo、Pika、Sora 或其他模型建议。",
        "视频提示词主体必须使用中文，描述动作、镜头运动、节奏、情绪变化、主体一致性和时间顺序。",
        "约束词 constraint_words 必须使用中文，描述不要出现的抖动、变形、穿帮、文字、水印、主体漂移等。",
        "画质预设 quality_preset、风格词 style_tags、画质风格三件套必须使用英文短语，例如 cinematic motion、smooth camera、stable composition、high detail。",
        "如需在 prompt 末尾补充英文，只能补充画质、风格、镜头运动类短标签；不要把动作叙事改成英文。",
        "多镜头跳切：标记跳切点。",
        "空间层+时间层双维度理解：空间分层（前中后景）+ 时间分层（开始-过程-结束）。",
        "画质→风格→约束词三件套：画质预设 + 风格标签 + 约束词。",
        "视频延长与分段拼接策略：支持延长和分段。",
        "官方推荐句式：按 Seedance / 可灵可执行的方式组织动作、镜头和编辑任务句式。",
        "提示词必须方便执行人员复制到 Seedance 或可灵工具。",
        "不确定信息可以写入 manual_review_items；这些内容只作为后续视频生成阶段人工控制提示，不阻塞当前流程。",
        "所有 array 字段无内容时必须输出 []，禁止输出 null。",
    ),
    required_fields=(
        "motion_description",
        "camera_movement",
        "duration",
        "quality_preset",
    ),
    output_schema={
        "motion_description": "string",
        "camera_movement": "string",
        "duration": "number",
        "quality_preset": "string",
        "style_tags": "array",
        "constraint_words": "array",
        "jump_cut_points": "array?",
        "spatial_layer": "object",
        "temporal_layer": "object",
        "extension_strategy": "object?",
        "segment_plan": "array?",
        "manual_review_items": "array",
        "skill": "image-to-video-prompt",
        "source_label": "agent",
    },
    consumed_brief_fields=(
        "content_type", "delivery_aspect_ratio", "target_duration_seconds", "primary_style_id",
        "resolved_style", "cultural_contexts", "primary_cultural_context_code", "narrative_grammar_by_context",
    ),
    prompt_sections={
        "视频交付": ("delivery_aspect_ratio", "target_duration_seconds"),
        "动作媒介与风格": ("content_type", "primary_style_id", "resolved_style"),
        "文化叙事语法": ("cultural_contexts", "primary_cultural_context_code", "narrative_grammar_by_context"),
    },
    affected_output_paths={
        "delivery_aspect_ratio": ("camera_movement", "spatial_layer"),
        "resolved_style": ("motion_description", "quality_preset", "style_tags"),
        "narrative_grammar_by_context": ("temporal_layer", "segment_plan"),
    },
)


SKILL_CONTRACTS: dict[str, SkillContract] = {
    contract.skill: contract
    for contract in (
        SCRIPT_READING_CONTRACT,
        SCRIPT_SEGMENTATION_CONTRACT,
        SCRIPT_BREAKDOWN_CONTRACT,
        CONTENT_COMPLIANCE_CONTRACT,
        ASSET_EXTRACT_CONTRACT,
        RELATION_CHECK_CONTRACT,
        CHARACTER_DESIGN_PROMPT_CONTRACT,
        SCENE_DESIGN_PROMPT_CONTRACT,
        PROP_DESIGN_PROMPT_CONTRACT,
        TEXT_TO_IMAGE_PROMPT_CONTRACT,
        IMAGE_TO_VIDEO_PROMPT_CONTRACT,
    )
}

SKILL_RUNTIME_POLICIES: dict[str, SkillRuntimePolicy] = {
    "script-reading": SkillRuntimePolicy(
        skill="script-reading",
        node_weight="medium_heavy",
        run_scope="project_or_episode",
        rerun_scopes=("project", "episode"),
        workflow_lane="script_breakdown_main",
        cache_policy="reuse_when_script_text_and_skill_version_unchanged",
        retry_policy="manual_or_backend_retry",
    ),
    "script-breakdown": SkillRuntimePolicy(
        skill="script-breakdown",
        node_weight="heavy",
        run_scope="episode",
        rerun_scopes=("episode", "scene", "script_segment"),
        workflow_lane="script_breakdown_main",
        cache_policy="invalidate_when_script_reading_or_script_text_changes",
        retry_policy="manual_or_backend_retry",
    ),
    "script-segmentation": SkillRuntimePolicy(
        skill="script-segmentation",
        node_weight="medium",
        run_scope="episode",
        rerun_scopes=("episode", "script_segment"),
        workflow_lane="script_breakdown_main",
        cache_policy="invalidate_when_script_text_reading_or_asset_master_changes",
        retry_policy="manual_or_backend_retry",
    ),
    "content-compliance-review": SkillRuntimePolicy(
        skill="content-compliance-review",
        node_weight="medium_light",
        run_scope="episode_or_storyboard_batch",
        rerun_scopes=("episode", "storyboard_batch", "storyboard"),
        workflow_lane="gate",
        cache_policy="invalidate_when_reviewed_artifact_changes",
        retry_policy="backend_retry_allowed",
    ),
    "asset-extract": SkillRuntimePolicy(
        skill="asset-extract",
        node_weight="medium_heavy",
        run_scope="episode",
        rerun_scopes=("episode", "scene", "script_segment"),
        workflow_lane="script_breakdown_main",
        cache_policy="invalidate_when_script_text_reading_or_asset_master_changes",
        retry_policy="manual_or_backend_retry",
    ),
    "relation-check": SkillRuntimePolicy(
        skill="relation-check",
        node_weight="medium_light",
        run_scope="episode_or_storyboard_batch",
        rerun_scopes=("episode", "storyboard_batch", "asset_batch"),
        workflow_lane="script_breakdown_main",
        cache_policy="invalidate_when_breakdown_or_assets_change",
        retry_policy="backend_retry_allowed",
    ),
    "character-design-prompt": SkillRuntimePolicy(
        skill="character-design-prompt",
        node_weight="medium_frequent",
        run_scope="asset_context",
        rerun_scopes=("asset", "asset_context"),
        workflow_lane="asset_prompt_production",
        cache_policy="invalidate_when_production_context_or_resolved_brief_changes",
        retry_policy="single_item_or_batch_retry",
        batch_size=5,
    ),
    "scene-design-prompt": SkillRuntimePolicy(
        skill="scene-design-prompt",
        node_weight="medium_frequent",
        run_scope="asset_context",
        rerun_scopes=("asset", "asset_context", "scene_view"),
        workflow_lane="asset_prompt_production",
        cache_policy="invalidate_when_production_context_or_resolved_brief_changes",
        retry_policy="single_item_or_batch_retry",
        batch_size=5,
    ),
    "prop-design-prompt": SkillRuntimePolicy(
        skill="prop-design-prompt",
        node_weight="medium_frequent",
        run_scope="asset_context",
        rerun_scopes=("asset", "asset_context", "prop_state"),
        workflow_lane="asset_prompt_production",
        cache_policy="invalidate_when_production_context_or_resolved_brief_changes",
        retry_policy="single_item_or_batch_retry",
        batch_size=5,
    ),
    "text-to-image-prompt": SkillRuntimePolicy(
        skill="text-to-image-prompt",
        node_weight="medium_frequent",
        run_scope="asset_keyframe_or_storyboard",
        rerun_scopes=("asset", "keyframe", "storyboard", "batch"),
        workflow_lane="prompt_production",
        cache_policy="invalidate_when_locked_breakdown_or_style_guide_changes",
        retry_policy="single_item_or_batch_retry",
        batch_size=10,
    ),
    "image-to-video-prompt": SkillRuntimePolicy(
        skill="image-to-video-prompt",
        node_weight="medium_heavy_frequent",
        run_scope="video_clip_or_storyboard",
        rerun_scopes=("video_clip", "storyboard", "batch"),
        workflow_lane="prompt_production",
        cache_policy="invalidate_when_image_reference_or_storyboard_motion_changes",
        retry_policy="single_item_or_batch_retry",
        batch_size=8,
    ),
}

PROMPT_MODE_SKILLS = {
    "t2i": "text-to-image-prompt",
    "i2v": "image-to-video-prompt",
}


def get_skill_contract(skill: str) -> SkillContract:
    return SKILL_CONTRACTS[skill]


def get_skill_runtime_policy(skill: str) -> SkillRuntimePolicy:
    return SKILL_RUNTIME_POLICIES[skill]


def get_prompt_skill_contract(mode: str) -> SkillContract:
    return get_skill_contract(PROMPT_MODE_SKILLS[mode])


def build_skill_runtime_metadata(skill: str, payload: dict[str, Any]) -> dict[str, Any]:
    contract = get_skill_contract(skill)
    policy = get_skill_runtime_policy(skill)
    contract_hash = stable_payload_hash(
        {
            "skill": contract.skill,
            "version": contract.version,
            "task": contract.task,
            "requirements": contract.requirements,
            "required_fields": contract.required_fields,
            "nested_required": contract.nested_required,
            "output_schema": contract.output_schema,
            "json_schema": contract.json_schema,
            "consumed_brief_fields": contract.consumed_brief_fields,
            "prompt_sections": contract.prompt_sections,
            "affected_output_paths": contract.affected_output_paths,
        }
    )
    return {
        "skill": skill,
        "task": contract.task,
        "contract_version": contract.version,
        "skill_version": contract.version,
        "contract_hash": contract_hash,
        "schema_format": "json-schema-2020-12",
        "input_hash": stable_payload_hash(semantic_skill_payload(payload)),
        "node_weight": policy.node_weight,
        "run_scope": policy.run_scope,
        "can_rerun_scope": list(policy.rerun_scopes),
        "workflow_lane": policy.workflow_lane,
        "cache_policy": policy.cache_policy,
        "retry_policy": policy.retry_policy,
        "batch_size": policy.batch_size,
        "brief_trace": build_brief_trace(contract, payload),
    }


def build_brief_trace(contract: SkillContract, payload: dict[str, Any]) -> dict[str, Any]:
    resolved = payload.get("resolved_production_brief")
    brief = resolved if isinstance(resolved, dict) else {}
    field_sources = brief.get("field_sources") if isinstance(brief.get("field_sources"), dict) else {}
    projection = {
        field_name: brief.get(field_name)
        for field_name in contract.consumed_brief_fields
        if field_name in brief
    }
    input_fields: list[dict[str, Any]] = []
    for field_name in contract.consumed_brief_fields:
        if field_name not in brief:
            continue
        prompt_sections = [
            section_name
            for section_name, section_fields in contract.prompt_sections.items()
            if field_name in section_fields
        ]
        for relative_path, value in _trace_leaf_values(field_name, brief.get(field_name)):
            input_fields.append({
                "path": f"resolved_production_brief.{relative_path}",
                "label": _trace_field_label(contract.skill, relative_path),
                "source": str(
                    field_sources.get(relative_path)
                    or field_sources.get(field_name)
                    or "resolved_production_brief"
                ),
                "value": _trace_value(value),
                "prompt_sections": prompt_sections,
                "affected_output_paths": list(contract.affected_output_paths.get(field_name, ())),
            })
    context = payload.get("production_context")
    if isinstance(context, dict):
        trace_fields = {
            field_name: (prompt_sections, output_paths)
            for field_name, prompt_sections, output_paths in _context_trace_fields(contract.skill)
        }
        for field_name, value in context.items():
            if field_name in {"schema_version", "lineage"} or value in (None, "", [], {}):
                continue
            default_sections, default_output_paths = trace_fields.get(
                field_name,
                (("生产上下文",), ("prompt",)),
            )
            for relative_path, leaf_value in _trace_leaf_values(field_name, value):
                prompt_sections, output_paths = _context_trace_leaf_impact(
                    contract.skill,
                    relative_path,
                    default_sections,
                    default_output_paths,
                )
                input_fields.append({
                    "path": f"production_context.{relative_path}",
                    "label": _trace_field_label(contract.skill, relative_path),
                    "source": _production_context_source(context, relative_path),
                    "value": _trace_value(leaf_value),
                    "prompt_sections": list(prompt_sections),
                    "affected_output_paths": list(output_paths),
                })
    return {
        "schema_version": "SkillBriefTrace.v3",
        "skill": contract.skill,
        "consumed_fields": list(contract.consumed_brief_fields),
        "input_projection": projection,
        "input_fields": input_fields,
        "prompt_sections": {key: list(value) for key, value in contract.prompt_sections.items()},
        "affected_output_paths": {key: list(value) for key, value in contract.affected_output_paths.items()},
    }


def _context_trace_fields(skill: str) -> tuple[tuple[str, tuple[str, ...], tuple[str, ...]], ...]:
    return {
        "character-design-prompt": (
            ("context_key", ("资产范围",), ("context_key",)),
            ("character_identity", ("角色身份",), ("prompt", "view_prompts")),
            ("age_stage", ("年龄阶段",), ("prompt", "view_prompts")),
            ("costume_variant", ("服装与配饰",), ("prompt", "material_layers", "view_prompts")),
            ("resolved_style", ("媒介与风格",), ("prompt", "negative_prompt", "view_prompts")),
            ("cultural_origin", ("文化时代约束",), ("prompt", "manual_review_items")),
            ("global_visual_constraints", ("全局视觉约束",), ("prompt", "negative_prompt")),
            ("output_spec", ("图位要求",), ("view_prompts",)),
            ("view_requirements", ("图位要求",), ("view_prompts",)),
            ("related_scene_codes", ("资产范围",), ("prompt",)),
            ("studio_requirement", ("摄影棚约束",), ("prompt", "negative_prompt")),
            ("content_boundaries", ("内容边界",), ("prompt", "negative_prompt")),
            ("generation_scope", ("图位要求",), ("view_prompts",)),
            ("manual_review_items", ("人工复核",), ("manual_review_items",)),
        ),
        "scene-design-prompt": (
            ("context_key", ("资产范围",), ("context_key",)),
            ("asset_identity", ("场景身份",), ("prompt", "view_prompts")),
            ("spatial_identity", ("场景空间",), ("prompt", "spatial_bible", "view_prompts")),
            ("cultural_context", ("文化时代与空间语法",), ("prompt", "spatial_bible.fixed_anchors")),
            ("narrative_grammar", ("文化时代与空间语法",), ("view_prompts",)),
            ("resolved_style", ("媒介与渲染",), ("prompt", "spatial_bible.lighting_baseline", "view_prompts")),
            ("global_visual_constraints", ("全局视觉约束",), ("prompt", "negative_prompt")),
            ("master_requirement", ("场景母版规格",), ("prompt", "view_prompts")),
            ("content_boundaries", ("内容边界",), ("prompt", "negative_prompt")),
            ("variant_requirement", ("视角要求",), ("view_prompts",)),
            ("manual_review_items", ("人工复核",), ("manual_review_items",)),
        ),
        "prop-design-prompt": (
            ("context_key", ("资产范围",), ("context_key",)),
            ("asset_identity", ("道具身份",), ("prompt", "state_prompts")),
            ("material_identity", ("材质结构",), ("prompt", "material_bible")),
            ("default_state", ("状态变化",), ("prompt", "material_bible", "state_prompts")),
            ("known_state_changes", ("状态变化",), ("state_prompts",)),
            ("cultural_context", ("文化时代约束",), ("prompt", "material_bible")),
            ("resolved_style", ("媒介与材质风格",), ("prompt", "material_bible", "state_prompts")),
            ("global_visual_constraints", ("全局视觉约束",), ("prompt", "negative_prompt")),
            ("master_requirement", ("道具母版规格",), ("prompt", "state_prompts")),
            ("content_boundaries", ("内容边界",), ("prompt", "negative_prompt")),
            ("variant_requirement", ("状态变化",), ("state_prompts",)),
            ("manual_review_items", ("人工复核",), ("manual_review_items",)),
        ),
    }.get(skill, ())


def _context_trace_leaf_impact(
    skill: str,
    field_path: str,
    default_sections: tuple[str, ...],
    default_output_paths: tuple[str, ...],
) -> tuple[tuple[str, ...], tuple[str, ...]]:
    output_paths = {
        "character-design-prompt": {
            "character_identity.asset_id": ("asset_code", "context_key"),
            "character_identity.name": ("prompt", "view_prompts[].prompt"),
            "character_identity.priority": ("prompt", "view_prompts"),
            "character_identity.narrative_identity": ("prompt", "identity_anchors"),
            "character_identity.appearance": ("prompt", "view_prompts[].prompt"),
            "character_identity.face": ("prompt", "view_prompts[].prompt", "identity_anchors"),
            "character_identity.body": ("prompt", "view_prompts[].prompt", "identity_anchors"),
            "character_identity.hair": ("prompt", "view_prompts[].prompt", "identity_anchors"),
            "character_identity.skin": ("prompt", "view_prompts[].prompt", "identity_anchors"),
            "character_identity.identity_anchors": ("prompt", "identity_anchors", "view_prompts[].prompt"),
            "character_identity.forbidden_changes": ("negative_prompt", "manual_review_items"),
            "age_stage.stage_code": ("context_key", "view_prompts"),
            "age_stage.name": ("prompt", "view_prompts[].prompt"),
            "age_stage.age_range": ("prompt", "view_prompts[].prompt"),
            "age_stage.timeline": ("prompt", "view_prompts[].prompt"),
            "age_stage.face_changes": ("prompt", "view_prompts[].prompt"),
            "age_stage.body_changes": ("prompt", "view_prompts[].prompt"),
            "age_stage.hair_skin_changes": ("prompt", "view_prompts[].prompt"),
            "age_stage.identity_anchors": ("prompt", "identity_anchors"),
            "age_stage.forbidden_changes": ("negative_prompt", "manual_review_items"),
            "costume_variant.variant_code": ("context_key", "view_prompts"),
            "costume_variant.name": ("prompt", "view_prompts[].prompt"),
            "costume_variant.appearance": ("prompt", "material_layers", "view_prompts[].prompt"),
            "costume_variant.layers": ("prompt", "material_layers", "view_prompts[].prompt"),
            "costume_variant.colors": ("prompt", "material_layers", "view_prompts[].prompt"),
            "costume_variant.materials": ("prompt", "material_layers", "view_prompts[].prompt"),
            "costume_variant.accessories": ("prompt", "material_layers", "view_prompts[].prompt"),
            "costume_variant.wear_state": ("prompt", "material_layers", "view_prompts[].prompt"),
        },
        "scene-design-prompt": {
            "asset_identity.asset_code": ("asset_code", "context_key"),
            "asset_identity.name": ("prompt", "view_prompts[].prompt"),
            "asset_identity.priority": ("prompt", "view_prompts"),
            "asset_identity.description": ("prompt", "view_prompts[].description"),
            "asset_identity.narrative_function": ("prompt", "view_prompts[].prompt"),
            "asset_identity.time": ("prompt", "spatial_bible.lighting_baseline", "view_prompts[].prompt"),
            "asset_identity.context_code": ("prompt", "spatial_bible.fixed_anchors"),
            "asset_identity.render_mode": ("prompt", "negative_prompt", "view_prompts[].prompt"),
            "spatial_identity.zones": ("prompt", "spatial_bible.zones", "view_prompts[].prompt"),
            "spatial_identity.key_props": ("prompt", "spatial_bible.fixed_anchors", "view_prompts[].prompt"),
            "spatial_identity.continuity_risks": ("negative_prompt", "spatial_bible.continuity_rules"),
            "spatial_identity.visual_goal": ("prompt", "spatial_bible.lighting_baseline", "view_prompts[].prompt"),
            "cultural_context.context_code": ("prompt", "spatial_bible.fixed_anchors"),
            "cultural_context.region": ("prompt", "spatial_bible.fixed_anchors"),
            "cultural_context.era": ("prompt", "spatial_bible.fixed_anchors"),
            "cultural_context.timeline": ("prompt", "spatial_bible.fixed_anchors"),
        },
        "prop-design-prompt": {
            "asset_identity.asset_code": ("asset_code", "context_key"),
            "asset_identity.name": ("prompt", "state_prompts[].prompt"),
            "asset_identity.priority": ("prompt", "state_prompts"),
            "asset_identity.prop_type": ("prompt", "material_bible.shape"),
            "asset_identity.description": ("prompt", "state_prompts[].description"),
            "asset_identity.owner_character": ("prompt", "manual_review_items"),
            "asset_identity.owner_role_code": ("prompt", "manual_review_items"),
            "material_identity.shape": ("prompt", "material_bible.shape"),
            "material_identity.scale": ("prompt", "material_bible.scale"),
            "material_identity.materials": ("prompt", "material_bible.materials"),
            "material_identity.colors": ("prompt", "material_bible.colors"),
            "material_identity.textures": ("prompt", "material_bible.textures"),
            "default_state": ("prompt", "material_bible.continuity_rules", "state_prompts"),
            "known_state_changes": ("state_prompts",),
            "cultural_context.context_code": ("prompt", "material_bible.continuity_rules"),
            "cultural_context.region": ("prompt", "material_bible.continuity_rules"),
            "cultural_context.era": ("prompt", "material_bible.continuity_rules"),
            "cultural_context.timeline": ("prompt", "material_bible.continuity_rules"),
        },
    }.get(skill, {}).get(field_path)
    return default_sections, output_paths or default_output_paths


def _trace_leaf_values(path: str, value: Any) -> list[tuple[str, Any]]:
    if value in (None, "", [], {}):
        return []
    if isinstance(value, dict):
        leaves: list[tuple[str, Any]] = []
        for key, child in value.items():
            leaves.extend(_trace_leaf_values(f"{path}.{key}", child))
        return leaves
    if isinstance(value, list) and value and all(isinstance(item, dict) for item in value):
        aggregated: dict[str, list[Any]] = {}
        for item in value:
            for child_path, child in _trace_leaf_values(f"{path}[]", item):
                aggregated.setdefault(child_path, []).append(child)
        return list(aggregated.items())
    return [(path, value)]


def _trace_field_label(skill: str, field_path: str) -> str:
    skill_labels = {
        "character-design-prompt": {
            "character_identity.asset_id": "人物资产编码",
            "character_identity.name": "人物名称",
            "character_identity.priority": "人物优先级",
            "character_identity.narrative_identity": "人物叙事身份",
            "age_stage.name": "年龄阶段名称",
            "age_stage.timeline": "年龄阶段时间线",
            "costume_variant.name": "装扮名称",
            "costume_variant.materials": "服装材质",
        },
        "scene-design-prompt": {
            "asset_identity.asset_code": "场景资产编码",
            "asset_identity.name": "场景名称",
            "asset_identity.priority": "场景优先级",
            "asset_identity.time": "场景时间",
            "spatial_identity.zones": "空间分区",
            "spatial_identity.key_props": "场景关键陈设",
        },
        "prop-design-prompt": {
            "asset_identity.asset_code": "道具资产编码",
            "asset_identity.name": "道具名称",
            "asset_identity.priority": "道具优先级",
            "material_identity.shape": "道具外形",
            "material_identity.scale": "道具比例",
            "material_identity.materials": "道具材质",
            "material_identity.colors": "道具颜色",
            "material_identity.textures": "道具纹理",
        },
    }.get(skill, {})
    if field_path in skill_labels:
        return skill_labels[field_path]
    field_name = field_path.rsplit(".", 1)[-1].removesuffix("[]")
    return {
        "context_key": "资产上下文",
        "content_type": "成片类型",
        "asset_master_aspect_ratio": "资产母版画幅",
        "primary_style_id": "主视觉风格",
        "style_id": "视觉风格编码",
        "name": "名称",
        "priority": "优先级",
        "description": "描述",
        "appearance": "外观",
        "face": "面部特征",
        "body": "体型",
        "hair": "发型",
        "skin": "肤质",
        "identity_anchors": "身份锚点",
        "forbidden_changes": "禁止变化",
        "stage_code": "年龄阶段编码",
        "age_range": "年龄范围",
        "timeline": "时间线",
        "face_changes": "面部变化",
        "body_changes": "体型变化",
        "hair_skin_changes": "发型与肤质变化",
        "variant_code": "变体编码",
        "layers": "层次",
        "colors": "颜色",
        "materials": "材质",
        "accessories": "配饰",
        "wear_state": "穿着状态",
        "time": "时间",
        "context_code": "文化时代编码",
        "region": "地区",
        "era": "时代",
        "render_mode": "呈现方式",
        "zones": "空间分区",
        "key_props": "关键陈设",
        "continuity_risks": "连续性风险",
        "visual_goal": "视觉目标",
        "prop_type": "道具类型",
        "owner_character": "归属人物",
        "owner_role_code": "归属人物短码",
        "shape": "外形",
        "scale": "比例",
        "textures": "纹理",
        "default_state": "默认状态",
        "known_state_changes": "状态变化",
        "code": "编码",
        "title": "名称",
        "prompt": "正向提示词",
        "negative_prompt": "负向提示词",
        "camera_angle": "机位角度",
        "framing": "构图要求",
        "pose": "姿势要求",
        "reference_requirement": "参考图要求",
        "include": "必须包含",
        "exclude": "必须排除",
    }.get(field_name, "其他字段")


def _production_context_source(context: dict[str, Any], field_name: str) -> str:
    lineage = context.get("lineage") if isinstance(context.get("lineage"), dict) else {}
    field_sources = lineage.get("field_sources") if isinstance(lineage.get("field_sources"), dict) else {}
    if field_sources.get(field_name):
        return str(field_sources[field_name])
    root_field = field_name.split(".", 1)[0].removesuffix("[]")
    if field_sources.get(root_field):
        return str(field_sources[root_field])
    if root_field in {
        "view_requirements", "studio_requirement", "master_requirement", "content_boundaries",
        "default_state", "generation_scope", "variant_requirement",
    }:
        return "system_production_rule"
    if root_field in {"cultural_context", "narrative_grammar", "resolved_style"}:
        return "resolved_production_brief"
    return str(lineage.get("asset_source") or lineage.get("reading_source") or "normalized_asset_inventory")


def _trace_value(value: Any, *, max_chars: int = 1200) -> Any:
    """Keep trace values inspectable without duplicating full scripts in Agent output."""
    serialized = json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)
    if len(serialized) <= max_chars:
        return value
    if isinstance(value, str):
        return value[:max_chars] + "..."
    return {
        "summary": serialized[:max_chars] + "...",
        "truncated": True,
        "original_length": len(serialized),
    }


def production_brief_impact_manifest() -> dict[str, Any]:
    return {
        "schema_version": "ProductionBriefImpactManifest.v1",
        "skills": [
            {
                "skill": contract.skill,
                "skill_version": contract.version,
                "task": contract.task,
                "consumed_brief_fields": list(contract.consumed_brief_fields),
                "prompt_sections": {key: list(value) for key, value in contract.prompt_sections.items()},
                "affected_output_paths": {key: list(value) for key, value in contract.affected_output_paths.items()},
                "output_json_schema": contract.json_schema,
            }
            for contract in SKILL_CONTRACTS.values()
            if contract.consumed_brief_fields
        ],
    }


def stable_payload_hash(payload: dict[str, Any]) -> str:
    normalized = json.dumps(payload, ensure_ascii=False, sort_keys=True, separators=(",", ":"), default=str)
    return hashlib.sha256(normalized.encode("utf-8")).hexdigest()


_VOLATILE_SKILL_INPUT_FIELDS = {
    "agent_run_ids",
    "force_rerun",
    "node_key",
    "parent_run_id",
    "persist_progress",
    "progress",
    "requested_at",
    "_runtime_retry_count",
    "rerun_of",
    "retry_of",
    "trigger",
    "workflow_run_id",
}


def semantic_skill_payload(value: Any) -> Any:
    if isinstance(value, dict):
        return {
            key: semantic_skill_payload(item)
            for key, item in value.items()
            if key not in _VOLATILE_SKILL_INPUT_FIELDS
        }
    if isinstance(value, list):
        return [semantic_skill_payload(item) for item in value]
    if isinstance(value, tuple):
        return [semantic_skill_payload(item) for item in value]
    return value


def _json_schema_for_descriptor(descriptor: Any) -> dict[str, Any]:
    if isinstance(descriptor, dict):
        return descriptor
    raw = str(descriptor or "").strip()
    text = raw.lower()
    if text.startswith("array"):
        inner = _angle_payload(raw)
        return {
            "type": "array",
            "items": _json_schema_for_array_item(inner) if inner is not None else {},
        }
    if text.startswith("object"):
        _, separator, fields = raw.partition(":")
        schema = _object_schema(fields if separator else "")
        if raw.endswith("?"):
            return {"oneOf": [schema, {"type": "null"}]}
        return schema
    if text.rstrip("?") == "integer":
        return {"type": "integer"}
    if text.rstrip("?") == "number":
        return {"type": "number"}
    if text.rstrip("?") == "boolean":
        return {"type": "boolean"}
    if text.rstrip("?") == "string":
        return {"type": "string"}
    return {"type": "string", "const": raw} if text else {}


def _apply_nested_required(
    schema: dict[str, Any],
    nested_required: dict[str, tuple[str, ...]],
) -> None:
    for path, required_fields in nested_required.items():
        target = _nested_schema_at_path(schema, path)
        properties = target.get("properties")
        if target.get("type") != "object" or not isinstance(properties, dict):
            raise ValueError(f"嵌套必填路径不是对象 Schema：{path}")
        unknown_fields = [field_name for field_name in required_fields if field_name not in properties]
        if unknown_fields:
            raise ValueError(f"嵌套必填路径 {path} 包含未声明字段：{', '.join(unknown_fields)}")
        target["required"] = list(required_fields)


def _nested_schema_at_path(schema: dict[str, Any], path: str) -> dict[str, Any]:
    current = schema
    for segment in path.split("."):
        is_array_item = segment.endswith("[]")
        field_name = segment[:-2] if is_array_item else segment
        properties = current.get("properties")
        if not field_name or not isinstance(properties, dict) or field_name not in properties:
            raise ValueError(f"无法解析嵌套必填路径：{path}")
        current = properties[field_name]
        if is_array_item:
            if current.get("type") != "array" or not isinstance(current.get("items"), dict):
                raise ValueError(f"嵌套必填路径不是数组 Schema：{path}")
            current = current["items"]
    return current


def _json_schema_for_array_item(inner: str) -> dict[str, Any]:
    value = inner.strip()
    if value.lower().startswith("object|string"):
        # 支持 "object|string"（无字段）与 "object|string: a, b"（带字段）两种写法，
        # 元素允许是对象或字符串。
        _, separator, fields = value.partition(":")
        return {"oneOf": [_object_schema(fields if separator else ""), {"type": "string"}]}
    if value.startswith("{") and value.endswith("}"):
        return _object_schema(value[1:-1])
    if value.lower().startswith("object"):
        _, separator, fields = value.partition(":")
        return _object_schema(fields if separator else "")
    if value.lower().rstrip("?") in {"string", "number", "integer", "boolean"}:
        return _json_schema_for_descriptor(value)
    # Example identifiers such as SC001 describe string references, not constants.
    return {"type": "string"}


def _object_schema(fields: str) -> dict[str, Any]:
    properties: dict[str, Any] = {}
    for token in _split_top_level(fields):
        name, schema = _property_schema(token)
        if name:
            properties[name] = schema
    schema: dict[str, Any] = {"type": "object"}
    if properties:
        schema["properties"] = properties
        schema["additionalProperties"] = True
    return schema


def _property_schema(token: str) -> tuple[str, dict[str, Any]]:
    value = token.strip()
    if not value:
        return "", {}
    suffix_index = _top_level_separator(value, ";")
    if suffix_index >= 0:
        value = value[:suffix_index].strip()

    separator_index = _top_level_separator(value, "=:")
    if separator_index >= 0:
        name = value[:separator_index].strip()
        descriptor = value[separator_index + 1:].strip()
        if value[separator_index] == "=" and not _looks_like_descriptor(descriptor):
            return name, {"type": "string", "const": descriptor}
        return name, _json_schema_for_descriptor(descriptor)

    opening = value.find("(")
    if opening > 0 and value.endswith(")"):
        name = value[:opening].strip()
        constraint = value[opening + 1:-1].strip()
        range_match = re.fullmatch(r"(-?\d+(?:\.\d+)?)\s*-\s*(-?\d+(?:\.\d+)?)", constraint)
        if range_match:
            minimum = _json_number(range_match.group(1))
            maximum = _json_number(range_match.group(2))
            return name, {"type": "number", "minimum": minimum, "maximum": maximum}
        if "|" in constraint:
            choices = [choice.strip() for choice in constraint.split("|") if choice.strip()]
            return name, {"type": "string", "enum": choices}
        if constraint.lower() in {"string", "number", "integer", "boolean", "object", "array"}:
            return name, _json_schema_for_descriptor(constraint)
        return name, {}

    return value.rstrip("?"), {}


def _looks_like_descriptor(value: str) -> bool:
    lowered = value.lower().rstrip("?")
    return lowered in {"array", "object", "string", "number", "integer", "boolean"} or any(
        lowered.startswith(prefix) for prefix in ("array<", "object:")
    )


def _angle_payload(value: str) -> str | None:
    start = value.find("<")
    if start < 0:
        return None
    depth = 0
    for index in range(start, len(value)):
        if value[index] == "<":
            depth += 1
        elif value[index] == ">":
            depth -= 1
            if depth == 0:
                return value[start + 1:index]
    return None


def _split_top_level(value: str) -> list[str]:
    parts: list[str] = []
    start = 0
    depths = {"<": 0, "(": 0, "{": 0, "[": 0}
    pairs = {">": "<", ")": "(", "}": "{", "]": "["}
    for index, character in enumerate(value):
        if character in depths:
            depths[character] += 1
        elif character in pairs:
            opener = pairs[character]
            depths[opener] = max(0, depths[opener] - 1)
        elif character == "," and not any(depths.values()):
            parts.append(value[start:index].strip())
            start = index + 1
    tail = value[start:].strip()
    if tail:
        parts.append(tail)
    return parts


def _top_level_separator(value: str, separators: str) -> int:
    depths = {"<": 0, "(": 0, "{": 0, "[": 0}
    pairs = {">": "<", ")": "(", "}": "{", "]": "["}
    for index, character in enumerate(value):
        if character in separators and not any(depths.values()):
            return index
        if character in depths:
            depths[character] += 1
        elif character in pairs:
            opener = pairs[character]
            depths[opener] = max(0, depths[opener] - 1)
    return -1


def _json_number(value: str) -> int | float:
    return float(value) if "." in value else int(value)


def _validation_path(error: Any) -> str:
    parts = [str(part) for part in error.absolute_path]
    return ".".join(parts) if parts else "output"


def validate_required_fields(output: dict[str, Any], contract: SkillContract | str) -> None:
    resolved = get_skill_contract(contract) if isinstance(contract, str) else contract
    missing = [
        field
        for field in resolved.required_fields
        if field not in output or output.get(field) is None or output.get(field) == ""
    ]
    if missing:
        raise ValueError(f"{resolved.skill} Agent 输出缺少必填字段：{', '.join(missing)}")
    errors = sorted(
        Draft202012Validator(resolved.json_schema).iter_errors(output),
        key=lambda error: tuple(str(part) for part in error.absolute_path),
    )
    if errors:
        invalid = list(dict.fromkeys(_validation_path(error) for error in errors))
        raise ValueError(f"{resolved.skill} Agent 输出字段类型错误：{', '.join(invalid)}")

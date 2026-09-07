from dataclasses import dataclass
from typing import Any

from app.agents.display_contracts import get_display_contract
from app.agents.skill_contracts import get_skill_contract, get_skill_runtime_policy
from app.models.enums import AgentKind, PRODUCTION_AGENT_KINDS, ProductionAgentKind
from app.schemas.agents import AgentDefinitionRead


@dataclass(frozen=True)
class AgentDefinition:
    key: AgentKind
    name: str
    skill: str
    description: str
    stage: str
    capabilities: tuple[str, ...]
    input_schema: dict[str, Any]
    output_schema: dict[str, Any]
    runtime_policy: dict[str, Any] | None = None
    display_schema: dict[str, Any] | None = None
    version: str = "0.1.0"
    enabled: bool = True

    def to_read_model(self) -> AgentDefinitionRead:
        runtime_policy = self.runtime_policy
        if runtime_policy is None:
            try:
                policy = get_skill_runtime_policy(self.skill)
                runtime_policy = {
                    "node_weight": policy.node_weight,
                    "run_scope": policy.run_scope,
                    "can_rerun_scope": list(policy.rerun_scopes),
                    "workflow_lane": policy.workflow_lane,
                    "cache_policy": policy.cache_policy,
                    "retry_policy": policy.retry_policy,
                    "batch_size": policy.batch_size,
                }
            except KeyError:
                runtime_policy = {}
        display_schema = self.display_schema
        if display_schema is None:
            try:
                display_schema = get_display_contract(self.skill).to_schema()
            except KeyError:
                display_schema = {}
        return AgentDefinitionRead(
            key=ProductionAgentKind(self.key.value),
            name=self.name,
            skill=self.skill,
            description=self.description,
            stage=self.stage,
            capabilities=list(self.capabilities),
            input_schema=self.input_schema,
            output_schema=self.output_schema,
            runtime_policy=runtime_policy,
            display_schema=display_schema,
            version=self.version,
            enabled=self.enabled,
        )


class AgentRegistry:
    def __init__(self) -> None:
        self._definitions: dict[AgentKind, AgentDefinition] = {}

    def register(self, definition: AgentDefinition) -> None:
        if definition.key not in PRODUCTION_AGENT_KINDS:
            raise ValueError(f"Legacy AgentKind cannot be registered: {definition.key.value}")
        self._definitions[definition.key] = definition

    def list(self) -> list[AgentDefinition]:
        return list(self._definitions.values())

    def get(self, key: AgentKind) -> AgentDefinition:
        return self._definitions[key]


AGENT_STAGE_MAP: dict[str, list[AgentKind]] = {
    "draft": [AgentKind.script_reading],
    "reading": [AgentKind.script_reading],
    "breakdown_review": [
        AgentKind.script_segmentation,
        AgentKind.script_breakdown,
        AgentKind.content_compliance_review,
        AgentKind.relation_check,
    ],
    "asset_locking": [
        AgentKind.asset_extract,
        AgentKind.character_design_prompt,
        AgentKind.scene_design_prompt,
        AgentKind.prop_design_prompt,
        AgentKind.script_breakdown,
        AgentKind.relation_check,
        AgentKind.text_to_image_prompt,
        AgentKind.content_compliance_review,
    ],
    "task_assignment": [
        AgentKind.character_design_prompt,
        AgentKind.scene_design_prompt,
        AgentKind.prop_design_prompt,
        AgentKind.text_to_image_prompt,
        AgentKind.image_to_video_prompt,
    ],
    "locked": [
        AgentKind.character_design_prompt,
        AgentKind.scene_design_prompt,
        AgentKind.prop_design_prompt,
        AgentKind.text_to_image_prompt,
        AgentKind.image_to_video_prompt,
    ],
    "production": [
        AgentKind.character_design_prompt,
        AgentKind.scene_design_prompt,
        AgentKind.prop_design_prompt,
        AgentKind.text_to_image_prompt,
        AgentKind.image_to_video_prompt,
        AgentKind.content_compliance_review,
    ],
    "assembly": [AgentKind.image_to_video_prompt],
    "completed": [AgentKind.content_compliance_review],
    "archived": [],
}


registry = AgentRegistry()

registry.register(
    AgentDefinition(
        key=AgentKind.script_reading,
        name="剧本阅读 Agent",
        skill="script-reading",
        description="阅读剧本原文，产出故事理解、平台策略、文化风格判断、拆段指导、生产风险和人工复核项。",
        stage="reading",
        capabilities=("script.reading", "reading.report", "platform.strategy", "culture.analyze", "segmentation.guide", "production.risk"),
        input_schema={"script_text": "string", "reading_notes": "array?", "episode_num": "integer?"},
        output_schema=get_skill_contract("script-reading").json_schema,
        version=get_skill_contract("script-reading").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.script_segmentation,
        name="脚本段拆分 Agent",
        skill="script-segmentation",
        description="基于已确认围读和正式资产，把剧本原文拆成可追溯脚本段，不生成分镜或重建资产。",
        stage="breakdown_review",
        capabilities=("script.segment", "script.traceability", "asset.reference", "segment.review_items"),
        input_schema={
            "script_text": "string",
            "reading_report": "object",
            "reading_review_state": "object",
            "normalized_asset_inventory": "AssetNormalization.v1",
            "asset_review_state": "object",
            "episode_num": "integer?",
        },
        output_schema=get_skill_contract("script-segmentation").json_schema,
        version=get_skill_contract("script-segmentation").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.script_breakdown,
        name="剧本拆解 Agent",
        skill="script-breakdown",
        description="基于已确认脚本段和正式资产生成分镜与生产计划；只能引用资产 ID，缺失资产仅提出建议。",
        stage="breakdown_review",
        capabilities=("storyboard.extract", "asset.reference", "missing_asset.suggest", "image_task.list", "video_task.list", "same_source_shot.merge"),
        input_schema={
            "script_text": "string",
            "script_segments": "array",
            "normalized_asset_inventory": "AssetNormalization.v1",
            "asset_review_state": "object",
            "episode_num": "integer?",
        },
        output_schema=get_skill_contract("script-breakdown").json_schema,
        version=get_skill_contract("script-breakdown").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.content_compliance_review,
        name="内容合理性审查 Agent",
        skill="content-compliance-review",
        description="敏感词合规检查与替换、年代一致性校验，以及暴力/政治/色情内容的安全降级策略。",
        stage="breakdown_review",
        capabilities=(
            "review.sensitive_words",
            "review.sensitive_replacement",
            "review.period_consistency",
            "review.safety_downgrade_chain",
            "review.asset.complete",
            "review.storyboard.reasonableness",
            "review.dialogue.split",
            "review.prompt.safety",
        ),
        input_schema={"artifact": "object", "artifact_type": "string?", "project_context": "object?"},
        output_schema=get_skill_contract("content-compliance-review").json_schema,
        version=get_skill_contract("content-compliance-review").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.asset_extract,
        name="资产清单生成 Agent",
        skill="asset-extract",
        description="基于剧本原文和已确认围读生成人物、场景、道具正式资产候选；后续只提出关联增量。",
        stage="asset_locking",
        capabilities=("asset.extract", "asset.character_scene_prop", "asset.traceability", "asset.review_items"),
        input_schema={
            "script_text": "string",
            "reading_report": "object",
            "reading_review_state": "object",
            "existing_asset_master_snapshot": "object?",
            "project_context": "object?",
        },
        output_schema=get_skill_contract("asset-extract").json_schema,
        version=get_skill_contract("asset-extract").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.relation_check,
        name="关系校验 Agent",
        skill="relation-check",
        description="校验脚本段、分镜、正式资产、出图计划和视频计划之间的追溯关系，不改写业务内容。",
        stage="breakdown_review",
        capabilities=("relation.check", "traceability.validate", "asset.link.validate", "plan.link.validate"),
        input_schema={"breakdown_view": "object", "project_context": "object?"},
        output_schema=get_skill_contract("relation-check").json_schema,
        version=get_skill_contract("relation-check").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.character_design_prompt,
        name="人物资产 Prompt Agent",
        skill="character-design-prompt",
        description="基于统一 ResolvedProductionBrief 和人物生产上下文生成角色母版与 A 图提示词。",
        stage="asset_locking",
        capabilities=("prompt.asset.character", "prompt.character.master", "prompt.traceability"),
        input_schema={
            "asset": "object",
            "production_context": "CharacterProductionContext.v1",
            "resolved_production_brief": "ResolvedProductionBrief.v1",
        },
        output_schema=get_skill_contract("character-design-prompt").json_schema,
        version=get_skill_contract("character-design-prompt").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.scene_design_prompt,
        name="场景资产 Prompt Agent",
        skill="scene-design-prompt",
        description="基于统一 ResolvedProductionBrief 和场景生产上下文生成场景空间母版与视角提示词。",
        stage="asset_locking",
        capabilities=("prompt.asset.scene", "prompt.scene.master", "prompt.traceability"),
        input_schema={
            "asset": "object",
            "production_context": "SceneProductionContext.v1",
            "resolved_production_brief": "ResolvedProductionBrief.v1",
        },
        output_schema=get_skill_contract("scene-design-prompt").json_schema,
        version=get_skill_contract("scene-design-prompt").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.prop_design_prompt,
        name="道具资产 Prompt Agent",
        skill="prop-design-prompt",
        description="基于统一 ResolvedProductionBrief 和道具生产上下文生成道具母版与状态提示词。",
        stage="asset_locking",
        capabilities=("prompt.asset.prop", "prompt.prop.master", "prompt.traceability"),
        input_schema={
            "asset": "object",
            "production_context": "PropProductionContext.v1",
            "resolved_production_brief": "ResolvedProductionBrief.v1",
        },
        output_schema=get_skill_contract("prop-design-prompt").json_schema,
        version=get_skill_contract("prop-design-prompt").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.text_to_image_prompt,
        name="文生图提示词 Agent",
        skill="text-to-image-prompt",
        description="生成 Seedream 5.0 lite 的 T2I Prompt，支持中英混写、材质分层、角色 A-Pose 定装模板、组图叙事和多图融合。",
        stage="production",
        capabilities=(
            "prompt.t2i",
            "prompt.seedream_5_lite",
            "prompt.bilingual",
            "prompt.material_layers",
            "prompt.character_apose",
            "prompt.multi_image_fusion",
            "prompt.composition_narrative",
        ),
        input_schema={
            "task_type": "string?",        # keyframe/costume/composition
            "storyboard": "object?",
            "asset": "object?",
            "assets": "array?",
            "style_guide": "object?",
            "task_id": "uuid?",
        },
        output_schema=get_skill_contract("text-to-image-prompt").json_schema,
        version=get_skill_contract("text-to-image-prompt").version,
    )
)
registry.register(
    AgentDefinition(
        key=AgentKind.image_to_video_prompt,
        name="图生视频提示词 Agent",
        skill="image-to-video-prompt",
        description="生成 Seedance 2.0 的 I2V Prompt，支持多镜头跳切、空间层+时间层双维度理解、画质风格约束词三件套、视频延长和分段拼接。",
        stage="production",
        capabilities=(
            "prompt.i2v",
            "prompt.seedance_2",
            "motion.describe",
            "multi_shot.jump_cut",
            "space_time.layering",
            "video.extension",
            "segment.stitching",
        ),
        input_schema={
            "image_ref": "string",         # 参考图URL（必填）
            "storyboard": "object",        # 分镜信息（必填）
            "style_guide": "object?",
            "duration": "number?",         # 期望时长（默认10秒）
            "task_id": "uuid?",
        },
        output_schema=get_skill_contract("image-to-video-prompt").json_schema,
        version=get_skill_contract("image-to-video-prompt").version,
    )
)

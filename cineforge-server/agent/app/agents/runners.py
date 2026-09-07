from __future__ import annotations

import asyncio
from copy import deepcopy
import json
import re
from typing import Any, Protocol

from json_repair import repair_json

from app.agents.breakdown_contract import attach_breakdown_contract, normalize_manual_review_items
from app.agents.hermes import hermes_client
from app.agents.runtime_policy import runtime_policy_executor
from app.agents.skill_contracts import (
    SkillContract,
    build_skill_runtime_metadata,
    get_skill_contract,
    get_skill_runtime_policy,
    validate_required_fields,
)
from app.agents.style_catalog import get_primary_style, get_style_modifier
from app.agents.production_brief import aspect_profile, rhythm_profile
from app.config import settings
from app.schemas.asset_normalization import AssetNormalizationResultV1
from app.services.asset_matching import match_existing_assets
from app.services.relation_validation import validate_breakdown_relations


class AgentRunner(Protocol):
    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        ...


class AgentExecutionError(Exception):
    def __init__(self, message: str, *, output: dict[str, Any] | None = None) -> None:
        super().__init__(message)
        self.output = output or {}


_ASSET_DESIGN_PROMPT_SKILLS = {
    "character-design-prompt",
    "scene-design-prompt",
    "prop-design-prompt",
}

_PROMPT_SKILLS_WITH_SHAPE_REPAIR = {
    *_ASSET_DESIGN_PROMPT_SKILLS,
    "text-to-image-prompt",
    "image-to-video-prompt",
}

_PARENT_STORYBOARD_MIN_SECONDS = 10
_PARENT_STORYBOARD_MAX_SECONDS = 15
# 镜中分镜 shot_function 受控词汇表。json_schema 已带此 enum，但 Hermes 对嵌套 enum
# 不做硬强制，模型偶尔输出超纲词（如 Action）。校验门、批次要求和纠正轮统一引用此常量，
# 确保"允许值"始终随错误一并告知模型，使其能自我修正而非瞎猜。
_MIRROR_SHOT_FUNCTIONS: tuple[str, ...] = (
    "Establish", "Reveal", "Power", "Pressure", "Detail", "Reaction",
    "Shift", "Impact", "Aftermath", "Exit",
)
# 单集剧本喂给 LLM 的字符安全上限。前端强制单集导入，正常单集远小于此值
# （线上实测最大 ~3.3k），此上限仅为防超长异常输入撑爆 context 的护栏，
# 而非日常截断点。reading 与 segmentation 统一引用，避免全文双塞。
_MAX_SCRIPT_CHARS = 60000


class ScriptReadingRunner:
    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        script_text = str(payload.get("script_text") or "").strip()
        if not script_text:
            raise ValueError("剧本文本为空，不能运行剧本阅读 Agent")
        return await runtime_policy_executor.execute(
            "script-reading", payload, lambda: _build_hermes_script_reading(payload)
        )


class ComplianceReviewRunner:
    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        return await runtime_policy_executor.execute(
            "content-compliance-review",
            payload,
            lambda: _build_hermes_skill_json(get_skill_contract("content-compliance-review"), payload),
        )


class AssetExtractRunner:
    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        async def operation() -> dict[str, Any]:
            output = await _build_hermes_skill_json(get_skill_contract("asset-extract"), payload)
            agent_raw_output = deepcopy(output)
            required_assets = _dedupe_assets(
                [
                    *_as_dict_list(output.get("assets")),
                    *_as_dict_list(output.get("characters")),
                    *_as_dict_list(output.get("scenes")),
                    *_as_dict_list(output.get("props")),
                ]
            )
            existing_assets = payload.get("existing_asset_master_snapshot")
            if isinstance(existing_assets, list):
                output["asset_matching"] = match_existing_assets(required_assets, existing_assets).to_dict()
            output["asset_gate"] = {
                "status": "needs_review",
                "requires_human_confirmation": True,
                "candidate_count": len(required_assets),
            }
            output["agent_raw_output"] = agent_raw_output
            return output

        return await runtime_policy_executor.execute("asset-extract", payload, operation)


class AssetDesignPromptRunner:
    skill: str
    context_schema: str

    def __init__(self, skill: str, context_schema: str) -> None:
        self.skill = skill
        self.context_schema = context_schema

    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        context = payload.get("production_context")
        if not isinstance(context, dict) or context.get("schema_version") != self.context_schema:
            raise ValueError(f"{self.skill} requires {self.context_schema}")

        async def operation() -> dict[str, Any]:
            output = await _build_hermes_skill_json(get_skill_contract(self.skill), payload)
            output["asset_code"] = str(output.get("asset_code") or payload.get("asset", {}).get("asset_code") or "")
            output["asset_type"] = self.skill.split("-", 1)[0]
            output["context_key"] = str(output.get("context_key") or context.get("context_key") or output["asset_code"])
            resolved = payload.get("resolved_production_brief") if isinstance(payload.get("resolved_production_brief"), dict) else {}
            output["aspect_ratio"] = str(output.get("aspect_ratio") or resolved.get("asset_master_aspect_ratio") or "16:9")
            variant = context.get("variant_requirement") if isinstance(context.get("variant_requirement"), dict) else {}
            variant_code = str(variant.get("variant_code") or "").strip().upper()
            if variant_code and self.skill in {"scene-design-prompt", "prop-design-prompt"}:
                field = "view_prompts" if self.skill == "scene-design-prompt" else "state_prompts"
                items = output.get(field) if isinstance(output.get(field), list) else []
                selected = next((dict(item) for item in items if isinstance(item, dict) and str(item.get("code") or "").upper() == variant_code), None)
                if selected is None:
                    selected = dict(items[0]) if items and isinstance(items[0], dict) else {}
                selected.update({
                    "code": variant_code,
                    "title": str(variant.get("title_zh") or selected.get("title") or variant_code),
                    "prompt": str(selected.get("prompt") or output.get("prompt") or ""),
                    "negative_prompt": str(selected.get("negative_prompt") or output.get("negative_prompt") or ""),
                    "description": str(variant.get("description_zh") or selected.get("description") or ""),
                    "reference_requirement": str(selected.get("reference_requirement") or ""),
                })
                if self.skill == "scene-design-prompt":
                    selected.setdefault("camera_angle", "")
                    selected.setdefault("framing", "")
                output[field] = [selected]
            return output

        return await runtime_policy_executor.execute(self.skill, payload, operation)


class CharacterDesignPromptRunner(AssetDesignPromptRunner):
    def __init__(self) -> None:
        super().__init__("character-design-prompt", "CharacterProductionContext.v1")


class SceneDesignPromptRunner(AssetDesignPromptRunner):
    def __init__(self) -> None:
        super().__init__("scene-design-prompt", "SceneProductionContext.v1")


class PropDesignPromptRunner(AssetDesignPromptRunner):
    def __init__(self) -> None:
        super().__init__("prop-design-prompt", "PropProductionContext.v1")


class RelationCheckRunner:
    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        async def operation() -> dict[str, Any]:
            content = dict(payload.get("breakdown_view")) if isinstance(payload.get("breakdown_view"), dict) else dict(payload)
            deterministic = validate_breakdown_relations(content)
            if not deterministic["ok"]:
                output = {
                    "relation_report": {
                        "ok": False,
                        "checked_counts": deterministic["checked_counts"],
                        "missing_links": deterministic["blocking_errors"],
                        "weak_links": deterministic["warnings"],
                        "suggested_repairs": [],
                    },
                    "storyboard_links": [],
                    "asset_links": [],
                    "plan_links": [],
                    "manual_review_items": deterministic["blocking_errors"],
                    "delivery_checklist": [],
                    "notes": ["后台确定性关系校验未通过，已阻止语义校验继续执行。"],
                    "deterministic_check": deterministic,
                    "gate_status": "blocked",
                    "llm_provider": "backend-deterministic",
                    "llm_model": None,
                    "source": {"provider": "backend-deterministic"},
                }
                return _attach_skill_execution_metadata(output, "relation-check", payload)
            output = await _build_hermes_skill_json(get_skill_contract("relation-check"), payload)
            relation_report = dict(output.get("relation_report") or {})
            relation_report["ok"] = bool(relation_report.get("ok", True))
            relation_report["deterministic_ok"] = True
            relation_report["checked_counts"] = deterministic["checked_counts"]
            output["relation_report"] = relation_report
            output["deterministic_check"] = deterministic
            output["gate_status"] = "passed"
            return output

        return await runtime_policy_executor.execute("relation-check", payload, operation)


class ScriptSegmentationRunner:
    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        return await runtime_policy_executor.execute(
            "script-segmentation", payload, lambda: _build_hermes_script_segmentation(payload)
        )


class PromptRunner:
    def __init__(self, mode: str) -> None:
        self.mode = mode

    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        from app.agents.workflows import create_prompt_workflow_runner

        return await create_prompt_workflow_runner(self.mode).run(payload)  # type: ignore[arg-type]


class TextToImagePromptRunner:
    """文生图 Prompt Runner - 专用于 Seedream 5.0 lite"""

    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        """
        生成 Seedream 5.0 lite 的 T2I Prompt

        输入:
        - task_type: keyframe/costume/composition
        - storyboard: 分镜信息
        - asset: 单个资产（如果是资产定装）
        - assets: 多个资产（如果是组图）
        - style_guide: 风格指南
        """
        context = payload.get("production_context") if isinstance(payload.get("production_context"), dict) else {}
        specialized = {
            "CharacterProductionContext.v1": CharacterDesignPromptRunner,
            "SceneProductionContext.v1": SceneDesignPromptRunner,
            "PropProductionContext.v1": PropDesignPromptRunner,
        }.get(str(context.get("schema_version") or ""))
        if specialized is not None:
            return await specialized().run(payload)

        # 构建分镜关键帧专用输入
        input_data = {
            "task_type": payload.get("task_type", "keyframe"),
            "output_spec": payload.get("output_spec"),
            "description": payload.get("description"),
            "storyboard": payload.get("storyboard"),
            "asset": payload.get("asset"),
            "assets": payload.get("assets"),
            "reading_to_asset_context": payload.get("reading_to_asset_context"),
            "production_context": payload.get("production_context"),
            "production_references": payload.get("production_references"),
            "agent_reminders": payload.get("agent_reminders"),
            "style_guide": payload.get("style_guide"),
            "resolved_production_brief": payload.get("resolved_production_brief"),
            "platform": "seedream",  # 固定为 Seedream
            "task_id": payload.get("task_id"),
            "storyboard_id": payload.get("storyboard_id"),
        }

        return await PromptRunner("t2i").run(input_data)


class ImageToVideoPromptRunner:
    """图生视频 Prompt Runner - 专用于 Seedance 2.0"""

    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        """
        生成 Seedance 2.0 的 I2V Prompt

        输入:
        - image_ref: 参考图 URL
        - storyboard: 分镜信息
        - style_guide: 风格指南
        - duration: 期望时长（秒）
        """
        # 构建 Seedance 专用输入
        keyframe_reference = payload.get("keyframe_reference")
        image_ref = payload.get("image_ref")
        if not image_ref and isinstance(keyframe_reference, dict):
            image_ref = keyframe_reference.get("file_path") or keyframe_reference.get("file_id")
        input_data = {
            "description": payload.get("description"),
            "image_ref": image_ref,
            "keyframe_reference": keyframe_reference,
            "storyboard": payload.get("storyboard"),
            "asset": payload.get("asset"),
            "assets": payload.get("assets"),
            "production_context": payload.get("production_context"),
            "production_references": payload.get("production_references"),
            "agent_reminders": payload.get("agent_reminders"),
            "style_guide": payload.get("style_guide"),
            "duration": payload.get("duration", 10),  # 默认 10 秒
            "resolved_production_brief": payload.get("resolved_production_brief"),
            "platform": "seedance",  # 固定为 Seedance
            "task_id": payload.get("task_id"),
            "storyboard_id": payload.get("storyboard_id"),
        }

        return await PromptRunner("i2v").run(input_data)


async def _build_hermes_skill_json(
    contract: SkillContract,
    payload: dict[str, Any],
) -> dict[str, Any]:
    system = (
        f"你是 CineForge AI 漫剧生产平台的 {contract.skill} Agent。"
        "你必须完全依据当前 skill 能力生成结果。"
        "生成提示词时必须遵守语言标准：prompt/negative_prompt/动作叙事/约束说明使用中文；quality_tags、style_keywords、style_tags、quality_preset 等画质标签和风格词使用英文短语。"
        "当前前端只支持图片模型 Seedream/libimage，视频模型 Seedance/可灵；不要输出其他模型名称或平台建议。"
        "后端只做 JSON 解析和 schema 校验，不会用本地规则补内容。"
        "必须只返回一个 JSON 对象，不要使用 Markdown，不要输出解释文字。"
        "所有数组字段无内容时必须返回 []，禁止返回 null；不要调用代码执行工具生成 JSON。"
    )
    user = json.dumps(
        {
            "任务": contract.task,
            "skill": contract.skill,
            "要求": list(contract.requirements),
            "必须返回字段": contract.output_schema,
            "必填字段": list(contract.required_fields),
            "输入": payload,
        },
        ensure_ascii=False,
    )
    parsed, response = await _chat_json_with_retry(
        system=system,
        user=user,
        attempts=_invalid_json_attempts(contract.skill),
        json_schema=contract.json_schema,
        schema_name=contract.skill,
    )
    if not parsed:
        raise AgentExecutionError(
            f"{contract.skill} Agent 未返回可解析 JSON",
            output=_unparseable_json_output(
                skill=contract.skill,
                content=response.get("content"),
                response=response,
                message=f"{contract.skill} Agent 未返回可解析 JSON",
            ),
        )
    if contract.skill in _ASSET_DESIGN_PROMPT_SKILLS:
        asset = payload.get("asset") if isinstance(payload.get("asset"), dict) else {}
        context = payload.get("production_context") if isinstance(payload.get("production_context"), dict) else {}
        resolved = payload.get("resolved_production_brief") if isinstance(payload.get("resolved_production_brief"), dict) else {}
        parsed.setdefault("asset_code", asset.get("asset_code"))
        parsed.setdefault("context_key", context.get("context_key") or asset.get("asset_code"))
        parsed.setdefault("aspect_ratio", resolved.get("asset_master_aspect_ratio") or "16:9")
        _coerce_asset_design_output_types(contract.skill, parsed)
    try:
        validate_required_fields(parsed, contract)
    except ValueError as exc:
        if contract.skill not in _PROMPT_SKILLS_WITH_SHAPE_REPAIR:
            raise AgentExecutionError(
                str(exc),
                output=_contract_validation_error_output(contract, parsed, response, str(exc)),
            ) from exc
        parsed, response = await _repair_skill_contract_output(
            contract,
            parsed,
            validation_error=str(exc),
        )
        if contract.skill in _ASSET_DESIGN_PROMPT_SKILLS:
            asset = payload.get("asset") if isinstance(payload.get("asset"), dict) else {}
            context = payload.get("production_context") if isinstance(payload.get("production_context"), dict) else {}
            resolved = payload.get("resolved_production_brief") if isinstance(payload.get("resolved_production_brief"), dict) else {}
            parsed.setdefault("asset_code", asset.get("asset_code"))
            parsed.setdefault("context_key", context.get("context_key") or asset.get("asset_code"))
            parsed.setdefault("aspect_ratio", resolved.get("asset_master_aspect_ratio") or "16:9")
            _coerce_asset_design_output_types(contract.skill, parsed)
        try:
            validate_required_fields(parsed, contract)
        except ValueError as repair_exc:
            raise AgentExecutionError(
                str(repair_exc),
                output=_contract_validation_error_output(contract, parsed, response, str(repair_exc)),
            ) from repair_exc
    if "manual_review_items" in parsed:
        parsed["manual_review_items"] = normalize_manual_review_items(parsed.get("manual_review_items"), source=contract.skill)
    parsed["skill"] = contract.skill
    parsed["llm_provider"] = "hermes-agent"
    parsed["llm_model"] = hermes_client.model
    parsed["source"] = {**dict(parsed.get("source") or {}), "provider": "hermes-agent", "model": hermes_client.model}
    parsed["raw_model_response"] = {
        "content": response.get("content"),
        "structured_output_mode": response.get("structured_output_mode"),
        "finish_reason": response.get("finish_reason"),
        "content_length": response.get("content_length"),
        "elapsed_ms": response.get("elapsed_ms"),
        "hermes_session_id": response.get("hermes_session_id"),
        "content_source": response.get("content_source"),
        "gateway_final_content": response.get("gateway_final_content"),
        "session_recovery_error": response.get("session_recovery_error"),
        "usage": dict(response.get("usage") or {}),
    }
    return _attach_skill_execution_metadata(parsed, contract.skill, payload)


def _coerce_asset_design_output_types(skill: str, output: dict[str, Any]) -> None:
    """Normalize optional descriptive metadata without changing generated prompt semantics."""
    object_fields = {
        "character-design-prompt": ("material_layers", "character_apose"),
        "scene-design-prompt": ("spatial_bible",),
        "prop-design-prompt": ("material_bible",),
    }.get(skill, ())
    for field in object_fields:
        value = output.get(field)
        if value is None or isinstance(value, dict):
            continue
        output[field] = {"items": value} if isinstance(value, list) else {"description": value}


async def _repair_skill_contract_output(
    contract: SkillContract,
    output: dict[str, Any],
    *,
    validation_error: str,
) -> tuple[dict[str, Any], dict[str, Any]]:
    """Repair prompt output shape once without regenerating from the full production input."""
    response = await hermes_client.chat_json(
        system=(
            "你是 JSON 结构纠错器。只修复字段缺失、字段类型和 JSON 结构，"
            "保留已有提示词语义，不扩写、不重新创作。只返回修复后的完整 JSON 对象。"
        ),
        user=json.dumps(
            {
                "校验错误": validation_error,
                "必须返回字段": contract.output_schema,
                "必填字段": list(contract.required_fields),
                "待修复输出": output,
            },
            ensure_ascii=False,
        ),
        json_schema=contract.json_schema,
        schema_name=f"{contract.skill}-repair",
    )
    repaired = _parse_json_object(response.get("content") or "")
    if not repaired and isinstance(response.get("artifact"), dict):
        repaired = _parse_json_object(str(response["artifact"].get("content") or ""))
    if not repaired:
        raise AgentExecutionError(
            f"{contract.skill} Agent JSON 结构纠错失败",
            output=_unparseable_json_output(
                skill=contract.skill,
                content=response.get("content"),
                response=response,
                message=f"{contract.skill} Agent JSON 结构纠错失败",
            ),
        )
    return repaired, response


async def _build_hermes_script_reading(payload: dict[str, Any]) -> dict[str, Any]:
    contract = get_skill_contract("script-reading")
    system = (
        "你是 CineForge AI 漫剧生产平台的 script-reading Agent。"
        "你只负责故事理解、平台策略、文化与风格判断、拆段指导、生产风险和人工复核，不识别资产候选，不生成分镜、出图 prompt 或视频 prompt。"
        "必须只返回一个 JSON 对象，不要使用 Markdown，不要输出解释文字。"
        "所有数组字段无内容时必须返回 []，禁止返回 null。"
        "输入可能包含多个分集，但只分析 episode_num 指定的当前分集；遇到下一集标记后立即停止。"
        "输出必须紧凑，不得复述剧本，不得为同一证据写多段近义描述；字符串内容中的双引号必须正确转义。"
        "禁止调用终端、文件系统、write_file、read_file 或任何其他工具，禁止写入文件或只返回文件路径。"
        "完整 JSON 必须直接放在本次 assistant message.content 中。"
    )
    user = json.dumps(
        {
            "任务": contract.task,
            "skill": contract.skill,
            "要求": list(contract.requirements),
            "必须返回字段": contract.output_schema,
            "必填字段": list(contract.required_fields),
            "输入": {
                "project_title": payload.get("project_title"),
                "project_prefix": payload.get("project_prefix"),
                "genre": payload.get("genre"),
                "episode_num": payload.get("episode_num"),
                "resolved_production_brief": payload.get("resolved_production_brief"),
                "episode_id": payload.get("episode_id"),
                "episode_code": payload.get("episode_code"),
                "script_id": payload.get("script_id"),
                "script_version_id": payload.get("script_version_id"),
                "script_version_no": payload.get("script_version_no"),
                # P1-8b: 不再传 script_source。它是 script_context 的整份拷贝，内含
                # 与上面重复的 id 字段、一份全文 script_text（与下面的 script_text 重复
                # 双塞）、以及 episode_production_brief。reading builder 代码不读它；
                # episode_production_brief 唯一实义字段 target_duration_seconds 已并入
                # resolved_production_brief（reading 从那里消费）。故删除不丢任何输入，
                # 仅消除全文双塞。script_text 单份传入，上限提至 _MAX_SCRIPT_CHARS 护栏。
                "existing_asset_master_snapshot": payload.get("existing_asset_master_snapshot") or [],
                "script_text": str(payload.get("script_text") or "")[:_MAX_SCRIPT_CHARS],
            },
        },
        ensure_ascii=False,
    )
    parsed, response = await _chat_json_with_retry(
        system=system,
        user=user,
        attempts=_invalid_json_attempts(contract.skill),
        timeout_seconds=settings.script_reading_timeout_seconds,
        json_schema=contract.json_schema,
        schema_name=contract.skill,
    )
    if not parsed:
        raise AgentExecutionError(
            "script-reading Agent 未返回可解析 JSON",
            output=_unparseable_json_output(
                skill="script-reading",
                content=response.get("content"),
                response=response,
                message="script-reading Agent 未返回可解析 JSON",
            ),
        )
    try:
        validate_required_fields(parsed, contract)
    except ValueError as exc:
        raise AgentExecutionError(
            str(exc),
            output=_contract_validation_error_output(contract, parsed, response, str(exc)),
        ) from exc
    output = _normalize_script_reading_output(parsed, payload)
    output["skill"] = "script-reading"
    output["llm_provider"] = "hermes-agent"
    output["llm_model"] = hermes_client.model
    output["source"] = {**dict(parsed.get("source") or {}), "provider": "hermes-agent", "model": hermes_client.model}
    output["raw_model_response"] = {
        "content": response.get("content"),
        "structured_output_mode": response.get("structured_output_mode"),
        "finish_reason": response.get("finish_reason"),
        "content_length": response.get("content_length"),
        "elapsed_ms": response.get("elapsed_ms"),
        "hermes_session_id": response.get("hermes_session_id"),
        "content_source": response.get("content_source"),
        "gateway_final_content": response.get("gateway_final_content"),
        "session_recovery_error": response.get("session_recovery_error"),
        "usage": dict(response.get("usage") or {}),
    }
    if isinstance(response.get("artifact"), dict):
        output["source"]["hermes_artifact"] = {
            "path": response["artifact"].get("path"),
            "content_length": response["artifact"].get("content_length"),
        }
    return _attach_skill_execution_metadata(output, "script-reading", payload)


_JSON_ONLY_REINFORCE = (
    "上一次调用你没有返回 JSON 本体。现在必须立即在响应正文中直接输出【完整的 JSON 对象】。"
    "严禁声称已把结果写入任何文件、路径或磁盘；严禁只给“交付物概览/说明/统计”等文字；严禁使用 Markdown 或代码块。"
    "响应的第一个字符必须是 {，最后一个字符必须是 }，中间是完整可解析的 JSON。"
    "字符串内容中的双引号必须使用反斜杠正确转义。"
)

_JSON_SYNTAX_REPAIR = (
    "你是 JSON 语法纠错器。只修复下面输出中的括号、逗号、引号和转义错误，"
    "不得补写、删减或重新生成业务内容。只返回修复后的完整 JSON 对象。"
)


async def _chat_json_with_retry(
    *,
    system: str,
    user: str,
    attempts: int = 3,
    timeout_seconds: float | None = None,
    json_schema: dict[str, Any] | None = None,
    schema_name: str | None = None,
) -> tuple[dict[str, Any], dict[str, Any]]:
    """调用 Hermes 并解析 JSON；若模型返回概览/写文件等非 JSON 文本，用更强硬指令重试。

    DeepSeek 在 temperature>0 时偶发“偷懒”返回概览而非 JSON 本体，属非确定性行为，
    因此这里最多尝试 attempts 次，首次失败后追加强制指令。
    """
    response = await hermes_client.chat_json(
        system=system,
        user=user,
        timeout_seconds=timeout_seconds,
        json_schema=json_schema,
        schema_name=schema_name,
    )
    _raise_if_truncated_response(response, schema_name=schema_name)
    content = str(response.get("content") or "")
    artifact_content = str(response["artifact"].get("content") or "") if isinstance(response.get("artifact"), dict) else ""
    parsed = _parse_json_object(content)
    if not parsed and artifact_content:
        parsed = _parse_json_object(artifact_content)
    # json_repair only fixes transport-level JSON syntax. Business fields are
    # still validated against the skill contract by the caller.
    if parsed:
        return parsed, response
    failed_content = str(response.get("content") or "")
    if not failed_content and isinstance(response.get("artifact"), dict):
        failed_content = str(response["artifact"].get("content") or "")
    for retry_index in range(max(0, attempts - 1)):
        repair_syntax = retry_index == 0 and "{" in failed_content
        response = await hermes_client.chat_json(
            system=_JSON_SYNTAX_REPAIR if repair_syntax else f"{system}\n{_JSON_ONLY_REINFORCE}",
            user=f"待修复输出：\n{failed_content}" if repair_syntax else f"{user}\n\n{_JSON_ONLY_REINFORCE}",
            timeout_seconds=timeout_seconds,
            json_schema=json_schema,
            schema_name=schema_name,
        )
        _raise_if_truncated_response(response, schema_name=schema_name)
        content = str(response.get("content") or "")
        artifact_content = str(response["artifact"].get("content") or "") if isinstance(response.get("artifact"), dict) else ""
        parsed = _parse_json_object(content)
        if not parsed and artifact_content:
            parsed = _parse_json_object(artifact_content)
        if parsed:
            return parsed, response
        failed_content = content or artifact_content or failed_content
    return parsed, response


def _invalid_json_attempts(skill: str) -> int:
    if skill == "script-reading":
        return 1
    retry_policy = get_skill_runtime_policy(skill).retry_policy
    if retry_policy in {"backend_retry_allowed", "single_item_or_batch_retry"}:
        return 2
    if retry_policy == "manual_or_backend_retry":
        # Episode-scale skills already have RuntimePolicy transport retries.
        # Avoid nesting full model retries inside the same absolute deadline.
        return 1
    return 1


def _raise_if_truncated_response(response: dict[str, Any], *, schema_name: str | None) -> None:
    if str(response.get("finish_reason") or "").lower() != "length":
        return
    skill = str(schema_name or "skill")
    raise AgentExecutionError(
        f"{skill} Agent 输出因 token 上限被截断",
        output=_unparseable_json_output(
            skill=skill,
            content=response.get("content"),
            response=response,
            extra={
                "finish_reason": "length",
                "structured_output_mode": response.get("structured_output_mode"),
                "elapsed_ms": response.get("elapsed_ms"),
                "usage": dict(response.get("usage") or {}),
            },
            message=f"{skill} Agent 输出因 token 上限被截断",
        ),
    )


def _attach_skill_execution_metadata(
    output: dict[str, Any],
    skill: str,
    payload: dict[str, Any],
) -> dict[str, Any]:
    metadata = build_skill_runtime_metadata(skill, payload)
    output.update(
        {
            "skill": skill,
            "skill_version": metadata["skill_version"],
            "contract_version": metadata["contract_version"],
            "contract_hash": metadata["contract_hash"],
            "input_hash": metadata["input_hash"],
            "brief_trace": metadata["brief_trace"],
        }
    )
    output["source"] = {
        **dict(output.get("source") or {}),
        "skill": skill,
        "skill_version": metadata["skill_version"],
        "contract_version": metadata["contract_version"],
        "contract_hash": metadata["contract_hash"],
        "input_hash": metadata["input_hash"],
        "brief_trace": metadata["brief_trace"],
    }
    return output


async def _build_hermes_script_breakdown(
    payload: dict[str, Any],
) -> dict[str, Any]:
    episode_num = _coerce_int(payload.get("episode_num"), 1)
    project_prefix = str(payload.get("project_prefix") or "RF").strip().upper()[:5] or "RF"
    resolved_brief = _resolved_production_brief(payload)
    input_script_segments = _normalize_input_script_segments(payload.get("script_segments"), episode_num)
    if not input_script_segments:
        raise ValueError("分镜拆解必须使用已人工确认的 confirmed_script_segments")
    return await _build_hermes_storyboard_breakdown(
        payload,
        input_script_segments=input_script_segments,
        episode_num=episode_num,
        project_prefix=project_prefix,
        resolved_brief=resolved_brief,
    )


async def _build_hermes_storyboard_breakdown(
    payload: dict[str, Any],
    *,
    input_script_segments: list[dict[str, Any]],
    episode_num: int,
    project_prefix: str,
    resolved_brief: dict[str, Any],
) -> dict[str, Any]:
    batch_size = max(1, settings.storyboard_batch_size)
    storyboards: list[dict[str, Any]] = []
    manual_review_items: list[Any] = []
    missing_asset_suggestions: list[dict[str, Any]] = []
    notes: list[Any] = []
    raw_batches: list[dict[str, Any]] = []
    inventory = _strict_reviewed_asset_inventory(payload)
    asset_reference_index = _compact_asset_reference_index(inventory.get("assets"))
    if not asset_reference_index:
        raise ValueError("分镜拆解必须使用已人工确认的 normalized_asset_inventory")
    system = (
        "你是 CineForge 的分镜导演与剪辑 Agent。输入脚本段已经人工确认，你只负责为这些脚本段生成可直接生产的分镜。"
        "不得重拆脚本段，不得重新生成角色、场景、道具、定装提示词、关键帧提示词或视频提示词。"
        "父分镜是一次完整的 10-15 秒视频生成单元；镜中分镜是父分镜内部 2-6 个明确硬切，不受 10 秒下限约束。"
        "任何完整台词行都是不可拆分的语义原子，不得截断、摘要、改写、重复或跨子镜头拆句。"
        "说话人前缀（如 Win：）、字词和全半角标点都属于台词原文；子镜头必须逐字复制整行，禁止只复制冒号后的对白正文。"
        "每个内部镜头必须有镜头职能、环境压力、身体微动作、声音或视觉母题以及明确的运镜动机。"
        "必须只返回 JSON 对象，不要 Markdown，不要解释文字；数组无内容时返回 []，禁止返回 null。"
    )
    storyboard_contract = get_skill_contract("script-breakdown")
    semaphore = asyncio.Semaphore(max(1, settings.storyboard_batch_concurrency))

    async def generate_batch(batch_start: int, batch: list[dict[str, Any]]) -> dict[str, Any]:
        segment_plans = _storyboard_segment_inputs(batch, payload.get("resolved_production_brief"))
        user = json.dumps(
            {
                "任务": "分镜预定稿",
                "项目缩写": project_prefix,
                "集数": episode_num,
                "统一制作约束": resolved_brief,
                "资产编号表": asset_reference_index,
                "confirmed_script_segments": segment_plans,
                "必须返回字段": _storyboard_only_schema(),
                "分镜要求": [
                    "每个脚本段必须严格生成 planned_shot_count 条分镜，禁止遗漏、增加或跨段合并。",
                    "storyboards[].script_segment_code 必须原样引用 confirmed_script_segments[].script_segment_code。",
                    "每条分镜的 context_code 和 render_mode 必须与所属脚本段一致；render_mode 只能是 animated 或 live_action。",
                    "order_num 是脚本段内顺序，每个脚本段都从 1 开始连续编号。",
                    "同一脚本段内所有分镜 duration_seconds 合计必须等于 duration_budget_seconds。",
                    "禁止输出 shot_no、V001、J001-V001 等冗余镜头编号；正式分镜编号和视频编号均由后台生成。",
                    "角色、场景、道具只能引用脚本段已有引用或资产编号表，不得新增资产。",
                    "每条分镜必须包含画面描述、运镜、景别、时长、角色、场景编号、场景正式名称和台词；scene_code 与 scene_name 必须逐项对应资产编号表中的同一正式场景。",
                    "shot_size 必须使用中文景别：极近特写、特写、近景、中近景、中景、中全景、全景、远景、大远景；禁止 ECU、CU、MCU、MS、LS 等英文缩写。",
                    "camera 必须使用中文描述机位、角度、运镜和构图；禁止用英文缩写代替中文镜头语言。",
                    "description 只写镜头内可见人物、动作、环境和叙事结果；camera 只写景别、机位、角度、运动和构图，不得重复 description 的剧情动作。",
                    "若前一脚本段是从后一脚本段提取的开场钩子，钩子动作只在前段生成一次；后一主体段必须从钩子结束后的下一动作接续，禁止完整回放同一动作。",
                    "不同分镜不得重复同一人物动作或同一信息揭示；只有剧本明确要求回放或多视角重复时才允许，并在 source.repeated_action_reason 说明原因。",
                    "manual_review_items 必须输出对象，并填写 scope_type(storyboard|script_segment|episode) 与 scope_code；分镜级使用正式 F 编号，脚本段级使用 J 编号，全局使用 episode_code。",
                    "父分镜必须为 10-15 秒；同一脚本段内按实际动作、对白可读时长和情绪节拍拆分，不按每句台词机械拆分。镜中子镜头不受 10 秒下限约束。",
                    "同一空间、同一图片来源和连续对白优先放进一个父分镜；需要切换景别或反应镜头时，输出 2-6 个 mirror_shots，禁止只输出 A。",
                    "mirror_shots[].duration_seconds 必须大于 0 且合计严格等于父分镜；相邻子镜头必须改变景别或角度，形成可识别的硬切。",
                    "mirror_shots 每项必须填写 shot_function、environmental_pressure、micro_action、sound_or_motif、movement_reason；每项只写一个主要运镜。",
                    f"shot_function 只能取以下之一：{'、'.join(_MIRROR_SHOT_FUNCTIONS)}；禁止使用 Action、Transition 等表外词，若为动作镜头请用 Impact 或 Detail，衔接镜头请用 Shift。",
                    "父分镜台词按原文整行保留；说话人前缀（如 Win：）、字词和全半角标点都属于原文，承载该行的子镜头 dialogue 必须逐字复制整行，禁止只保留冒号后的对白正文。每一整行只能分配给一个子镜头，按发生顺序合并后必须与父分镜 dialogue 完全一致。对白前先描述说话人的可见动作。",
                    "只输出 storyboards、missing_asset_suggestions、manual_review_items、notes、source 五个顶层字段。",
                ],
                "禁止事项": [
                    "禁止输出 script_segments、assets、characters、scenes、props。",
                    "禁止输出定装 A-E 提示词、材质提示词、负面提示词、关键帧计划或视频计划。",
                    "禁止改写脚本原文，禁止虚构脚本中不存在的角色、场景、道具或情节。",
                ],
            },
            ensure_ascii=False,
        )
        async with semaphore:
            parsed, response = await _chat_json_with_retry(
                system=system,
                user=user,
                json_schema=storyboard_contract.json_schema,
                schema_name="script-breakdown-storyboards",
            )
        parsed = _normalize_storyboard_batch(parsed, segment_plans)
        plan_errors = _storyboard_batch_plan_errors(parsed, segment_plans)
        if parsed and plan_errors:
            correction_user = json.dumps(
                {
                    "任务": "修正分镜预定稿结构",
                    "必须修正的问题": plan_errors,
                    "shot_function允许值": list(_MIRROR_SHOT_FUNCTIONS),
                    "confirmed_script_segments": segment_plans,
                    "原始输出": parsed,
                    "父子台词逐字复制目标": [
                        {
                            "script_segment_code": storyboard.get("script_segment_code"),
                            "order_num": storyboard.get("order_num"),
                            "parent_dialogue_lines": _storyboard_dialogue_lines(storyboard.get("dialogue")),
                        }
                        for storyboard in _as_dict_list(parsed.get("storyboards"))
                    ],
                    "修正规则": [
                        "严格按每段 planned_shot_count 输出，不得遗漏或新增分镜。",
                        "script_segment_code 必须原样引用，order_num 必须按段从 1 连续编号。",
                        "每段分镜时长合计必须等于 duration_budget_seconds。",
                        "每条父分镜必须为 10-15 秒，shot_size 和 camera 必须使用中文，不得输出 shot_no 或 V001。镜中子镜头时长必须大于 0 且总和等于父分镜。",
                        "镜中分镜必须完整输出 2-6 项，并补齐镜头职能、环境压力、身体微动作、声音或视觉母题、运镜动机；相邻项使用不同景别或角度。",
                        f"每个镜中分镜的 shot_function 只能取 shot_function允许值 之一（{'、'.join(_MIRROR_SHOT_FUNCTIONS)}）；把 Action 等表外词改成语义最接近的允许值：动作/冲击镜头用 Impact，细节/递物镜头用 Detail，衔接/转场镜头用 Shift。",
                        "父分镜和镜中分镜必须逐行完整保留原文台词；一整行台词不能被拆到多个子镜头。父子台词逐字复制目标中的每一行都是不可修改字符串，说话人前缀、冒号、顿号和其他全半角标点必须一并复制，禁止只保留冒号后的正文。",
                        "删除重复分镜；description 与 camera 不得重复叙述同一段剧情动作。",
                        "只返回修正后的完整 JSON 对象。",
                    ],
                },
                ensure_ascii=False,
            )
            async with semaphore:
                parsed, response = await _chat_json_with_retry(
                    system=system,
                    user=correction_user,
                    json_schema=storyboard_contract.json_schema,
                    schema_name="script-breakdown-storyboards-correction",
                )
            parsed = _normalize_storyboard_batch(parsed, segment_plans)
            plan_errors = _storyboard_batch_plan_errors(parsed, segment_plans)
        batch_storyboards = _as_dict_list(parsed.get("storyboards")) if parsed else []
        if not batch_storyboards or plan_errors:
            raise AgentExecutionError(
                f"分镜预定稿第 {batch_start // batch_size + 1} 批未满足脚本段分镜规划",
                output=_unparseable_json_output(
                    skill="script-breakdown",
                    content=response.get("content"),
                    response=response,
                    extra={
                        "output_target": "storyboards",
                        "batch_start": batch_start,
                        "plan_errors": plan_errors,
                    },
                ),
            )
        return {
            "batch_start": batch_start,
            "batch": batch,
            "storyboards": batch_storyboards,
            "manual_review_items": list(parsed.get("manual_review_items") or []),
            "missing_asset_suggestions": _as_dict_list(parsed.get("missing_asset_suggestions")),
            "notes": list(parsed.get("notes") or []),
            "content": response.get("content"),
            "structured_output_mode": response.get("structured_output_mode"),
        }

    batches = [
        (batch_start, input_script_segments[batch_start: batch_start + batch_size])
        for batch_start in range(0, len(input_script_segments), batch_size)
    ]
    batch_results = await asyncio.gather(*(
        generate_batch(batch_start, batch)
        for batch_start, batch in batches
    ))
    for batch_result in batch_results:
        batch_start = int(batch_result["batch_start"])
        batch = list(batch_result["batch"])
        batch_storyboards = list(batch_result["storyboards"])
        for storyboard in batch_storyboards:
            normalized_storyboard = dict(storyboard)
            normalized_storyboard.pop("storyboard_code", None)
            normalized_storyboard["segment_order_num"] = _coerce_int(
                normalized_storyboard.get("order_num"),
                1,
            )
            storyboards.append(normalized_storyboard)
        manual_review_items.extend(list(batch_result["manual_review_items"]))
        missing_asset_suggestions.extend(list(batch_result["missing_asset_suggestions"]))
        notes.extend(list(batch_result["notes"]))
        raw_batches.append({
            "batch_no": batch_start // batch_size + 1,
            "script_segment_codes": [item.get("script_segment_code") for item in batch],
            "content": batch_result["content"],
            "structured_output_mode": batch_result["structured_output_mode"],
        })

    parsed_output = {
        "storyboards": storyboards,
        "characters": [],
        "scenes": [],
        "props": [],
        "assets": [],
        "manual_review_items": manual_review_items,
        "notes": notes,
        "source": {"mode": "confirmed_script_segment_batches", "batch_size": batch_size},
    }
    output = _normalize_breakdown_output(
        parsed_output,
        episode_num,
        input_script_segments=input_script_segments,
        asset_reference_index=asset_reference_index,
    )
    output.update({
        "llm_provider": "hermes-agent",
        "llm_model": hermes_client.model,
        "source_label": "agent",
        "storyboard_only": True,
        "workflow_phase": "storyboard_draft",
        "source": {**dict(output.get("source") or {}), "provider": "hermes-agent", "model": hermes_client.model},
        "workflow": {
            "name": "storyboard_breakdown_hermes",
            "backend": "hermes-agent",
            "nodes": ["load_confirmed_segments", "batch_storyboards", "normalize_json"],
            "batch_size": batch_size,
            "batch_count": (len(input_script_segments) + batch_size - 1) // batch_size,
            "concurrency": max(1, settings.storyboard_batch_concurrency),
        },
        "raw_model_response": {"batches": raw_batches},
        "missing_asset_suggestions": missing_asset_suggestions,
    })
    contracted = attach_breakdown_contract(output, project_prefix=project_prefix, episode_num=episode_num)
    return _attach_skill_execution_metadata(contracted, "script-breakdown", payload)


async def _build_hermes_script_segmentation(
    payload: dict[str, Any],
) -> dict[str, Any]:
    script_text = str(payload.get("script_text") or "").strip()
    if not script_text:
        raise ValueError("剧本文本为空，不能运行脚本段拆分 Agent")
    episode_num = _coerce_int(payload.get("episode_num"), 1)
    project_prefix = str(payload.get("project_prefix") or "RF").strip().upper()[:5] or "RF"
    project_title = str(payload.get("project_title") or "").strip()
    resolved_brief = _resolved_production_brief(payload)
    reading_review_state = payload.get("reading_review_state")
    reading_report = payload.get("reading_report")
    if (
        not isinstance(reading_review_state, dict)
        or str(reading_review_state.get("status") or "") != "confirmed"
        or not isinstance(reading_report, dict)
    ):
        raise ValueError("脚本段拆分必须使用已人工确认的 reading_report")
    reading_report = dict(reading_report)
    inventory = _strict_reviewed_asset_inventory(payload)
    asset_reference_index = _compact_asset_reference_index(inventory.get("assets"))
    if not asset_reference_index:
        raise ValueError("脚本段拆分必须使用已人工确认的 normalized_asset_inventory")
    contract = get_skill_contract("script-segmentation")
    reading_context = _compact_reading_for_segmentation(reading_report)
    # Meta-filtered estimate. The regex extractor cannot cover every screenplay
    # format (colon vs. colon-less speaker lines, etc.), so its blocks are used
    # only as a best-effort hint — never as the pass/fail gate. When the estimate
    # is unreliable (no real speaker block survives the filter, e.g. a script whose
    # only colon line is a top meta note), we do not feed it to the LLM at all.
    reliable_dialogue_blocks = _reliable_dialogue_blocks(script_text)
    system = (
        "你是 CineForge AI 漫剧生产平台的 script-segmentation Agent。"
        "你只在剧本原文中划分可追溯脚本段，用于后续逐段分镜。"
        "不要生成分镜、资产、关键帧、视频计划或提示词。"
        "dialogue_lines 的每一项都是不可拆分的原文台词块，必须从说话人行开始，逐字保留说话人前缀、表演括注、正文、换行与标点。"
        "必须只返回一个 JSON 对象，不要使用 Markdown，不要输出解释文字。"
        "所有数组字段无内容时必须返回 []，禁止返回 null。"
        "严禁声称已把结果写入任何文件或路径，严禁只返回交付物概览/统计说明；响应正文本身必须就是完整 JSON。"
    )
    user = json.dumps(
        {
            "任务": contract.task,
            "skill": "script-segmentation",
            "output_target": "script_segments",
            "项目标题": project_title,
            "项目缩写": project_prefix,
            "集数": episode_num,
            "统一制作约束": resolved_brief,
            "人工确认围读摘要": reading_context,
            "人工确认正式资产编号表": asset_reference_index,
            "剧本文本": script_text[:_MAX_SCRIPT_CHARS],
            **({"必须逐字覆盖的原文台词块": reliable_dialogue_blocks} if reliable_dialogue_blocks else {}),
            "拆分要求": list(contract.requirements),
            "必须返回字段": contract.output_schema,
            "必填字段": list(contract.required_fields),
            "禁止事项": [
                "禁止输出 storyboards、assets、keyframe_plan、video_plan",
                "禁止改写 source_text；source_text 必须来自原文",
                "禁止把每一句台词单独拆成脚本段",
                "禁止忽略人工确认围读报告。",
                "禁止引用围读候选临时编号；role_refs、scene_refs、prop_refs 只能使用人工确认正式资产编号表中的 asset_code。",
                "脚本段颗粒度要匹配生产平台画像中的单集时长、节奏和镜头密度",
            ],
        },
        ensure_ascii=False,
    )
    parsed, response = await _chat_json_with_retry(
        system=system,
        user=user,
        json_schema=contract.json_schema,
        schema_name=contract.skill,
    )
    if not parsed:
        raise AgentExecutionError(
            "script-segmentation Agent 未返回可解析 JSON",
            output=_unparseable_json_output(
                skill="script-segmentation",
                content=response.get("content"),
                response=response,
                extra={"output_target": "script_segments"},
                message="script-segmentation Agent 未返回可解析 JSON",
            ),
        )
    try:
        validate_required_fields(parsed, contract)
    except ValueError as exc:
        raise AgentExecutionError(
            str(exc),
            output=_contract_validation_error_output(
                contract,
                parsed,
                response,
                str(exc),
                extra={"output_target": "script_segments"},
            ),
        ) from exc
    # ① Hard, format-agnostic rewrite gate: every dialogue_line must be a verbatim,
    # in-order substring of the source script (whitespace-insensitive). This catches
    # rewrites/fabrications/reordering without assuming any speaker-line format.
    rewrite_errors = _dialogue_rewrite_errors(parsed, script_text)
    if rewrite_errors:
        correction_user = json.dumps(
            {
                "任务": "修正脚本段台词原文块",
                "必须修正的问题": rewrite_errors,
                "原始输出": parsed,
                "修正规则": [
                    "保持原有脚本段数量、顺序、source_text 和其他字段不变，只修正各段 dialogue_lines。",
                    "每条 dialogue_lines 必须是剧本原文中的一段连续文字，逐字复制，禁止改写、摘要、臆造或调换顺序。",
                    "说话人前缀、表演括注、正文、原始换行和全半角标点必须逐字复制，禁止只保留冒号后的对白正文。",
                    "无台词脚本段输出 dialogue_lines=[]；只返回修正后的完整 JSON 对象。",
                ],
            },
            ensure_ascii=False,
        )
        parsed, response = await _chat_json_with_retry(
            system=system,
            user=correction_user,
            json_schema=contract.json_schema,
            schema_name="script-segmentation-dialogue-correction",
        )
        if parsed:
            try:
                validate_required_fields(parsed, contract)
            except ValueError as exc:
                raise AgentExecutionError(
                    str(exc),
                    output=_contract_validation_error_output(
                        contract,
                        parsed,
                        response,
                        str(exc),
                        extra={"output_target": "script_segments"},
                    ),
                ) from exc
        rewrite_errors = _dialogue_rewrite_errors(parsed, script_text)
    if not parsed or rewrite_errors:
        raise AgentExecutionError(
            "script-segmentation Agent 改写或臆造了台词原文，未逐字取自剧本",
            output=_contract_validation_error_output(
                contract,
                parsed or {},
                response,
                "script-segmentation Agent 改写或臆造了台词原文，未逐字取自剧本",
                extra={
                    "output_target": "script_segments",
                    "plan_errors": rewrite_errors,
                },
            ),
        )
    script_segments = _normalize_script_segments(parsed, episode_num, payload.get("resolved_production_brief"))
    if not script_segments:
        raise ValueError("script-segmentation Agent 未返回 script_segments")
    # ② Best-effort omission check → soft manual-review item (never fails). Skipped
    # automatically when the extractor estimate is unreliable (deadlock-proof).
    manual_review_items = _as_text_list(parsed.get("manual_review_items"))
    manual_review_items.extend(_dialogue_coverage_warnings(parsed, script_text))
    output = {
        "script_segments": script_segments,
        "storyboards": [],
        "assets": [],
        "characters": [],
        "scenes": [],
        "props": [],
        "manual_review_items": list(dict.fromkeys(manual_review_items)),
        "notes": _as_text_list(parsed.get("segmentation_notes") or parsed.get("notes")),
        "skill": "script-segmentation",
        "output_target": "script_segments",
        "llm_provider": "hermes-agent",
        "llm_model": hermes_client.model,
        "source_label": "agent",
        "source": {**dict(parsed.get("source") or {}), "provider": "hermes-agent", "model": hermes_client.model},
        "raw_model_response": {
            "content": response.get("content"),
            "structured_output_mode": response.get("structured_output_mode"),
        },
        "workflow_phase": "script_segmentation",
        "segmentation_only": True,
    }
    contracted = attach_breakdown_contract(output, project_prefix=project_prefix, episode_num=episode_num)
    return _attach_skill_execution_metadata(contracted, "script-segmentation", payload)


def _compact_reading_for_segmentation(report: dict[str, Any]) -> dict[str, Any]:
    return {
        key: report.get(key)
        for key in (
            "story_overview",
            "platform_strategy",
            "segmentation_guidance",
            "cultural_origin",
            "worldview",
            "emotion_curve",
        )
        if report.get(key) not in (None, {}, [])
    }


_SCRIPT_SPEAKER_LINE_RE = re.compile(
    r"^(?P<speaker>[^：:\n]{1,30}(?:（[^）\n]{1,20}）|\([^\)\n]{1,20}\))?)[：:](?P<inline>.*)$"
)
_SCRIPT_DIRECTION_ONLY_RE = re.compile(r"^[（(][^）)]*[）)]$")


def _extract_script_dialogue_blocks(script_text: str) -> list[str]:
    lines = [line.strip() for line in str(script_text or "").splitlines()]
    blocks: list[str] = []
    index = 0
    while index < len(lines):
        line = lines[index]
        match = _SCRIPT_SPEAKER_LINE_RE.fullmatch(line) if line and not line.startswith(("（", "(", "[", "【")) else None
        if not match:
            index += 1
            continue
        block = [line]
        index += 1
        if not match.group("inline").strip():
            while index < len(lines) and not lines[index]:
                index += 1
            if index < len(lines):
                first_content = lines[index]
                if not _SCRIPT_SPEAKER_LINE_RE.fullmatch(first_content):
                    block.append(first_content)
                    index += 1
                    if _SCRIPT_DIRECTION_ONLY_RE.fullmatch(first_content):
                        while index < len(lines) and not lines[index]:
                            index += 1
                        if index < len(lines):
                            block.append(lines[index])
                            index += 1
        while index < len(lines):
            candidate = lines[index]
            if not _SCRIPT_DIRECTION_ONLY_RE.fullmatch(candidate):
                break
            block.append(candidate)
            index += 1
            while index < len(lines) and not lines[index]:
                index += 1
            if index < len(lines):
                block.append(lines[index])
                index += 1
        blocks.append("\n".join(block))
    return blocks


_DIALOGUE_COVERAGE_MIN_RATIO = 0.6


def _strip_ws(text: str) -> str:
    """Whitespace-insensitive form: drop all Unicode whitespace incl. full-width space."""
    return re.sub(r"\s+", "", str(text or ""))


def _dialogue_actual_lines(parsed: dict[str, Any]) -> list[str]:
    return [
        line
        for segment in _as_dict_list(parsed.get("script_segments"))
        for line in _as_text_list(segment.get("dialogue_lines"))
    ] if parsed else []


def _dialogue_rewrite_errors(parsed: dict[str, Any], script_text: str) -> list[str]:
    """① Hard, format-agnostic gate.

    Each dialogue_line must be a verbatim, in-order substring of the source script
    (whitespace-insensitive). Detects rewrites/fabrications (not found at all) and
    reordering (found only before the running cursor). Format of speaker lines is
    irrelevant, so this works for both colon and colon-less screenplays.
    """
    source = _strip_ws(script_text)
    if not source:
        return []
    errors: list[str] = []
    cursor = 0
    for line in _dialogue_actual_lines(parsed):
        needle = _strip_ws(line)
        if not needle:
            continue
        at = source.find(needle, cursor)
        if at >= 0:
            cursor = at + len(needle)
            continue
        # Not found from cursor onward — either rewritten/fabricated or out of order.
        if needle in source:
            errors.append(
                f"dialogue_lines 顺序与原剧本不一致（应按原文先后排列）：{json.dumps(line, ensure_ascii=False)}"
            )
        else:
            errors.append(
                f"dialogue_lines 不是剧本原文的逐字片段（疑似改写/臆造）：{json.dumps(line, ensure_ascii=False)}"
            )
    return errors


def _reliable_dialogue_blocks(script_text: str) -> list[str]:
    """Meta-filtered estimate of source dialogue blocks.

    Drops pseudo-blocks whose speaker portion contains whitespace or is overlong —
    e.g. a top meta note like ``测试需求：竖屏短剧 …`` that happens to contain a colon.
    Returns [] when nothing plausible survives (estimate is then treated as unreliable).
    """
    plausible: list[str] = []
    for block in _extract_script_dialogue_blocks(script_text):
        first_line = block.splitlines()[0] if block else ""
        match = _SCRIPT_SPEAKER_LINE_RE.fullmatch(first_line)
        if not match:
            continue
        speaker = match.group("speaker") or ""
        # Strip a trailing performance parenthetical before judging the bare name.
        bare = re.sub(r"[（(][^）)]*[）)]$", "", speaker).strip()
        if not bare or " " in bare or "　" in bare or len(bare) > 12:
            continue
        # Space-separated inline content after the colon signals a config/meta note
        # (e.g. ``测试需求：竖屏短剧 韩剧 …``), not Chinese screenplay dialogue.
        inline = (match.group("inline") or "").strip()
        if " " in inline or "　" in inline:
            continue
        plausible.append(block)
    return plausible


def _dialogue_coverage_warnings(parsed: dict[str, Any], script_text: str) -> list[str]:
    """② Best-effort omission check → soft warning list (never raises/fails).

    Uses the meta-filtered estimate only. When the estimate is unreliable (no
    plausible block), returns [] so the step never deadlocks on an unparseable format.
    """
    estimated = _reliable_dialogue_blocks(script_text)
    if not estimated:
        return []
    actual_count = len([line for line in _dialogue_actual_lines(parsed) if _strip_ws(line)])
    if actual_count >= len(estimated) * _DIALOGUE_COVERAGE_MIN_RATIO:
        return []
    return [
        f"疑似漏台词：原文估算约 {len(estimated)} 个台词块，脚本段仅覆盖 {actual_count} 条，请人工核对是否有台词遗漏。"
    ]


def _normalize_breakdown_output(
    parsed: dict[str, Any],
    episode_num: int,
    *,
    input_script_segments: list[dict[str, Any]] | None = None,
    asset_reference_index: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    script_segments = _normalize_script_segments(parsed, episode_num) or list(input_script_segments or [])
    storyboards = _normalize_storyboards(
        parsed,
        script_segments,
        episode_num,
        asset_reference_index=asset_reference_index,
    )
    if not storyboards:
        raise ValueError("模型未返回 storyboards 分镜表，已取消本地兜底，请重新运行或调整模型配置")

    characters = _normalize_character_assets(_extract_asset_group(parsed, "characters"))
    scenes = _normalize_scene_assets(_extract_asset_group(parsed, "scenes"))
    props = _normalize_prop_assets(_extract_asset_group(parsed, "props"))
    generic_assets = _normalize_generic_assets(parsed.get("assets"))
    assets = _dedupe_assets([*generic_assets, *characters, *scenes, *props])

    manual_review_items = _as_text_list(parsed.get("manual_review_items") or parsed.get("warnings"))
    for segment in script_segments:
        manual_review_items.extend(_as_text_list(segment.get("manual_review_items")))

    return {
        "script_segments": script_segments,
        "storyboards": storyboards,
        "assets": assets,
        "characters": characters,
        "scenes": scenes,
        "props": props,
        "reading_report": _normalize_reading_report(parsed),
        "readthrough": _normalize_readthrough(parsed.get("readthrough")),
        "dialogue_script": _normalize_dialogue_script(parsed.get("dialogue_script")),
        "keyframe_plan": _normalize_keyframe_plan(parsed.get("keyframe_plan")),
        "video_plan": _normalize_video_plan(parsed.get("video_plan")),
        "cross_references": _normalize_cross_references(parsed.get("cross_references")),
        "delivery_checklist": _normalize_delivery_checklist(parsed.get("delivery_checklist")),
        "manual_review_items": list(dict.fromkeys(manual_review_items)),
        "notes": _as_text_list(parsed.get("notes")),
        "skill": "script-breakdown",
    }


def _unparseable_json_output(
    *,
    skill: str,
    content: Any,
    response: dict[str, Any] | None = None,
    extra: dict[str, Any] | None = None,
    message: str = "模型未返回可解析 JSON",
) -> dict[str, Any]:
    response_data = response if isinstance(response, dict) else {}
    raw_content = "" if content is None else str(content)
    preview_chars = 8000
    raw_model_response = {
        "content": raw_content,
        "content_length": response_data.get("content_length") or len(raw_content),
        "content_preview": raw_content[:preview_chars],
        "content_tail": raw_content[-preview_chars:] if len(raw_content) > preview_chars else raw_content,
        "structured_output_mode": response_data.get("structured_output_mode"),
        "finish_reason": response_data.get("finish_reason"),
        "elapsed_ms": response_data.get("elapsed_ms"),
        "hermes_session_id": response_data.get("hermes_session_id"),
        "content_source": response_data.get("content_source"),
        "gateway_final_content": response_data.get("gateway_final_content"),
        "session_recovery_error": response_data.get("session_recovery_error"),
        "usage": dict(response_data.get("usage") or {}),
    }
    artifact = response_data.get("artifact")
    if isinstance(artifact, dict):
        raw_model_response["hermes_artifact"] = {
            "path": artifact.get("path"),
            "content_length": artifact.get("content_length"),
        }
    return {
        "skill": skill,
        **dict(extra or {}),
        "llm_provider": "hermes-agent",
        "llm_model": hermes_client.model,
        "raw_model_response": raw_model_response,
        "errors": [
            {
                "code": "unparseable_json",
                "message": message,
                "raw_content_preview": raw_content[:preview_chars],
                "raw_content_tail": raw_content[-preview_chars:] if len(raw_content) > preview_chars else raw_content,
                "raw_content_length": len(raw_content),
            }
        ],
    }


def _contract_validation_error_output(
    contract: SkillContract,
    parsed: dict[str, Any],
    response: dict[str, Any],
    message: str,
    *,
    extra: dict[str, Any] | None = None,
) -> dict[str, Any]:
    output = _unparseable_json_output(
        skill=contract.skill,
        content=response.get("content"),
        response=response,
        extra=extra,
        message=message,
    )
    output["parsed_output"] = parsed
    output["errors"][0]["code"] = "contract_validation_failed"
    return output


def _normalize_script_segments(
    parsed: dict[str, Any],
    episode_num: int,
    resolved_brief: Any = None,
) -> list[dict[str, Any]]:
    brief = resolved_brief if isinstance(resolved_brief, dict) else {}
    default_context = _clean_text(brief.get("primary_cultural_context_code"))
    content_type = _clean_text(brief.get("content_type"))
    default_render_mode = "live_action" if content_type == "live_action_drama" else "animated"
    raw_segments = parsed.get("script_segments")
    if not isinstance(raw_segments, list):
        raw_segments = []
        for episode in _as_dict_list(parsed.get("episodes")):
            raw_segments.extend(_as_dict_list(episode.get("script_segments")))

    segments: list[dict[str, Any]] = []
    for index, item in enumerate(_as_dict_list(raw_segments), start=1):
        order_no = _coerce_int(_pick(item, "order_no", "order_num", "index", "序号"), index)
        episode_code = str(_pick(item, "episode_code", "episode") or f"EP{episode_num:02d}").strip() or f"EP{episode_num:02d}"
        if episode_code.isdigit():
            episode_code = f"EP{int(episode_code):02d}"
        elif not episode_code.upper().startswith("EP"):
            episode_code = f"EP{episode_num:02d}"
        source_text = _clean_text(_pick(item, "source_text", "original_text", "原文", "text"))
        summary = _clean_text(_pick(item, "summary", "摘要", "description")) or source_text[:240]
        description = _clean_text(_pick(item, "description", "shot_description", "画面描述")) or summary or source_text
        role_refs = _as_text_list(_pick(item, "role_refs", "characters", "character_refs", "character_names", "角色"))
        scene_refs = _as_text_list(_pick(item, "scene_refs", "locations", "location_refs", "scenes", "场景"))
        prop_refs = _as_text_list(_pick(item, "prop_refs", "props", "prop_codes", "道具"))
        production_notes = _as_text_list(_pick(item, "production_notes", "notes", "制作备注", "生产备注"))
        estimated_duration_seconds = _coerce_int(
            _pick(item, "estimated_duration_seconds", "duration_seconds", "duration", "预计时长", "时长"),
            6,
        )
        segment = {
            "episode_code": episode_code,
            "order_no": order_no,
            "title": _clean_text(_pick(item, "title", "name", "标题")) or f"脚本段 {order_no:03d}",
            "source_text": source_text or description or "模型未返回原文依据，需要人工补充。",
            "summary": summary or description[:240],
            "story_function": _clean_text(_pick(item, "story_function", "function", "剧情功能")) or "未明确",
            "dominant_emotion": _clean_text(_pick(item, "dominant_emotion", "emotion", "情绪")) or "未明确",
            "rhythm": _clean_text(_pick(item, "rhythm", "pace", "节奏")) or "未明确",
            "viewpoint": _clean_text(_pick(item, "viewpoint", "pov", "视角")) or "未明确",
            "context_code": _clean_text(_pick(item, "context_code", "文化背景编号")) or default_context,
            "render_mode": _clean_text(_pick(item, "render_mode", "表现类型")) or default_render_mode,
            "storyboard_order_num": order_no,
            "description": description,
            "dialogue": _normalize_dialogue_text(_pick(item, "dialogue", "line", "台词")),
            "dialogue_lines": _as_text_list(_pick(item, "dialogue_lines", "台词原文行")),
            "camera": _clean_text(_pick(item, "camera", "camera_motion", "shot", "运镜"))
            or "中景，稳定镜头，保留角色动作和场景关系",
            "estimated_duration_seconds": estimated_duration_seconds,
            "duration_seconds": estimated_duration_seconds,
            "role_refs": role_refs,
            "scene_refs": scene_refs,
            "prop_refs": prop_refs,
            "production_notes": production_notes,
            "characters": role_refs,
            "locations": scene_refs,
            "keyframes": _normalize_keyframes(_pick(item, "keyframes", "关键帧"), description),
            "related_asset_names": _as_text_list(_pick(item, "related_asset_names", "assets", "关联资产")),
            "shot_no": _clean_text(_pick(item, "shot_no", "镜号")),
            "shot_size": _clean_text(_pick(item, "shot_size", "景别")),
            "space": _clean_text(_pick(item, "space", "空间")),
            "keyframe_code": _clean_text(_pick(item, "keyframe_code", "出图编号")),
            "video_code": _clean_text(_pick(item, "video_code", "视频编号")),
            "task_type": _clean_text(_pick(item, "task_type", "任务类型")),
            "mirror_shots": _normalize_mirror_shots(_pick(item, "mirror_shots", "镜中分镜")),
            "episode_name": _clean_text(_pick(item, "episode_name", "分集名称", "分集名称/ID")),
            "scene_name": _clean_text(_pick(item, "scene_name", "场景名称", "场景名称/ID")) or (scene_refs[0] if scene_refs else ""),
            "manual_review_items": _as_text_list(_pick(item, "manual_review_items", "warnings", "待确认")),
        }
        source_context = item.get("source_context")
        if isinstance(source_context, dict):
            segment["source_context"] = source_context
        else:
            segment["source_context"] = {"before": "", "current": segment["source_text"], "after": ""}
        segments.append(segment)
    return segments


def _normalize_input_script_segments(raw: Any, episode_num: int) -> list[dict[str, Any]]:
    segments: list[dict[str, Any]] = []
    for index, item in enumerate(_as_dict_list(raw), start=1):
        source_text = _clean_text(_pick(item, "source_text", "content", "text", "原文"))
        if not source_text:
            continue
        order_no = _coerce_int(_pick(item, "order_no", "order_num", "index", "序号"), index)
        episode_code = _clean_text(_pick(item, "episode_code", "episode")) or f"EP{episode_num:02d}"
        metadata = item.get("metadata_json") if isinstance(item.get("metadata_json"), dict) else item.get("metadata")
        segment = {
            "script_segment_code": _clean_text(_pick(item, "script_segment_code", "segment_code", "code", "id")) or f"EP{episode_num:02d}-J{order_no:03d}",
            "episode_code": episode_code,
            "order_no": order_no,
            "title": _clean_text(_pick(item, "title", "name", "标题")),
            "source_text": source_text,
            "summary": _clean_text(_pick(item, "summary", "摘要")),
            "story_function": _clean_text(_pick(item, "story_function", "叙事功能")),
            "dominant_emotion": _clean_text(_pick(item, "dominant_emotion", "情绪")),
            "rhythm": _clean_text(_pick(item, "rhythm", "节奏")),
            "viewpoint": _clean_text(_pick(item, "viewpoint", "视角")),
            "dialogue_lines": _as_text_list(
                _pick(item, "dialogue_lines", "台词原文行")
                or (metadata.get("dialogue_lines") if isinstance(metadata, dict) else None)
            ),
        }
        if isinstance(metadata, dict):
            segment["metadata"] = metadata
            segment["estimated_duration_seconds"] = _coerce_int(
                _pick(item, "estimated_duration_seconds", "duration_seconds")
                or metadata.get("estimated_duration_seconds")
                or metadata.get("duration_seconds"),
                0,
            ) or None
            segment["role_refs"] = _as_text_list(
                _pick(item, "role_refs", "characters") or metadata.get("role_refs") or metadata.get("characters")
            )
            segment["scene_refs"] = _as_text_list(
                _pick(item, "scene_refs", "locations") or metadata.get("scene_refs") or metadata.get("locations")
            )
            segment["prop_refs"] = _as_text_list(
                _pick(item, "prop_refs", "props") or metadata.get("prop_refs") or metadata.get("props")
            )
            segment["production_notes"] = _as_text_list(
                _pick(item, "production_notes", "notes") or metadata.get("production_notes") or metadata.get("notes")
            )
            segment["context_code"] = _clean_text(item.get("context_code") or metadata.get("context_code"))
            segment["render_mode"] = _clean_text(item.get("render_mode") or metadata.get("render_mode"))
        else:
            segment["context_code"] = _clean_text(item.get("context_code"))
            segment["render_mode"] = _clean_text(item.get("render_mode"))
        segments.append(segment)
    return segments


def _normalize_storyboards(
    parsed: dict[str, Any],
    script_segments: list[dict[str, Any]],
    episode_num: int,
    *,
    asset_reference_index: list[dict[str, Any]] | None = None,
) -> list[dict[str, Any]]:
    raw_storyboards = parsed.get("storyboards")
    if not isinstance(raw_storyboards, list):
        raw_storyboards = []
        for episode in _as_dict_list(parsed.get("episodes")):
            raw_storyboards.extend(_as_dict_list(episode.get("storyboards")))

    source_items = _as_dict_list(raw_storyboards) or script_segments
    segment_index = {
        _clean_text(segment.get("script_segment_code")): (
            _coerce_int(segment.get("order_no"), index),
            segment,
        )
        for index, segment in enumerate(script_segments, start=1)
        if _clean_text(segment.get("script_segment_code"))
    }
    scene_names_by_code = {
        _clean_text(item.get("asset_code")).upper(): _clean_text(item.get("name"))
        for item in _as_dict_list(asset_reference_index)
        if _clean_text(item.get("asset_type")).lower() == "scene"
        and _clean_text(item.get("asset_code"))
        and _clean_text(item.get("name"))
    }
    indexed_items = list(enumerate(source_items))
    indexed_items.sort(
        key=lambda pair: (
            segment_index.get(
                _clean_text(_pick(pair[1], "script_segment_code", "segment_code")),
                (len(script_segments) + 1, {}),
            )[0],
            _coerce_int(
                _pick(pair[1], "segment_order_num", "order_num", "order_no", "storyboard_order_num", "index", "序号"),
                pair[0] + 1,
            ),
            pair[0],
        )
    )
    storyboards: list[dict[str, Any]] = []
    seen: set[tuple[str, str, str]] = set()
    for source_index, (_, item) in enumerate(indexed_items, start=1):
        segment_code = _clean_text(_pick(item, "script_segment_code", "segment_code"))
        segment_order_num = _coerce_int(
            _pick(item, "segment_order_num", "order_num", "order_no", "storyboard_order_num", "index", "序号"),
            source_index,
        )
        description = _clean_text(_pick(item, "description", "summary", "source_text", "画面描述")) or "模型未返回画面描述，需要人工补充。"
        dialogue = _normalize_dialogue_text(_pick(item, "dialogue", "line", "台词"))
        signature = (segment_code, _dedupe_text(description), _dedupe_text(dialogue))
        if signature in seen:
            continue
        seen.add(signature)
        order_num = len(storyboards) + 1
        current_episode_num = _episode_num_from_item(item, episode_num)
        segment = segment_index.get(segment_code, (0, {}))[1]
        scene_code = _clean_text(_pick(item, "scene_code", "场景编号"))
        scene_name = _clean_text(_pick(item, "scene_name", "场景名称", "场景名称/ID"))
        if not scene_name and scene_code:
            scene_name = scene_names_by_code.get(scene_code.upper(), "")
        storyboards.append(
            {
                "episode_num": current_episode_num,
                "order_num": order_num,
                "segment_order_num": segment_order_num,
                "script_segment_code": segment_code,
                "script_segment_title": _clean_text(segment.get("title")),
                "script_segment_source_text": _clean_text(segment.get("source_text")),
                "title": _clean_text(_pick(item, "title", "name", "标题")) or f"分镜 {order_num:02d}",
                "description": description,
                "dialogue": dialogue,
                "camera": _clean_text(_pick(item, "camera", "camera_motion", "shot", "运镜"))
                or "中景，稳定镜头，保留角色动作和场景关系",
                "duration_seconds": _coerce_int(_pick(item, "duration_seconds", "duration", "时长"), 10),
                "characters": _as_text_list(_pick(item, "characters", "character_names", "角色")),
                "role_refs": _as_text_list(_pick(item, "role_refs", "character_refs", "角色编号")),
                "prop_refs": _as_text_list(_pick(item, "prop_refs", "道具编号")),
                "keyframes": _normalize_keyframes(_pick(item, "keyframes", "关键帧"), description),
                "agent_shot_no": _clean_text(_pick(item, "shot_no", "镜号")),
                "agent_shot_size": _clean_text(_pick(item, "shot_size", "景别")),
                "shot_size": _normalize_shot_size_cn(_pick(item, "shot_size", "景别")),
                "space": _clean_text(_pick(item, "space", "空间")),
                "scene_code": scene_code,
                "keyframe_code": _clean_text(_pick(item, "keyframe_code", "出图编号")),
                "video_code": _clean_text(_pick(item, "video_code", "视频编号")),
                "task_type": _clean_text(_pick(item, "task_type", "任务类型")),
                "mirror_shots": _normalize_mirror_shots(_pick(item, "mirror_shots", "镜中分镜")),
                "episode_name": _clean_text(_pick(item, "episode_name", "分集名称", "分集名称/ID")),
                "scene_name": scene_name,
            }
        )
    return storyboards


def _extract_asset_group(parsed: dict[str, Any], key: str) -> Any:
    direct = parsed.get(key)
    if direct:
        return direct
    assets = parsed.get("assets")
    if isinstance(assets, dict):
        return assets.get(key)
    return None


def _normalize_character_assets(raw: Any) -> list[dict[str, Any]]:
    assets = []
    for item in _as_dict_list(raw):
        name = _clean_text(_pick(item, "name", "character_name", "角色名"))
        if not name:
            continue
        character_type = _clean_text(_pick(item, "character_type", "type_label", "角色类型", "类型"))
        metadata = {
            "asset_code": _clean_text(_pick(item, "asset_code", "id", "ID")),
            "character_type": character_type,
            "visual_presence": _normalize_visual_presence(
                _pick(item, "visual_presence", "presence_mode", "appearance_scope", "出场方式", "是否出场"),
                character_type=character_type,
            ),
            "relationship": _clean_text(_pick(item, "relationship", "relationships", "faction", "camp", "人物关系", "阵营关系")),
            "role": _clean_text(_pick(item, "role", "定位", "角色定位")),
            "gender": _clean_text(_pick(item, "gender", "性别")) or "未明确",
            "age": _clean_text(_pick(item, "age", "年龄")) or "未明确",
            "appearance": _clean_text(_pick(item, "appearance", "外貌特质", "外貌")),
            "core_requirement": _clean_text(_pick(item, "core_requirement", "production_requirement", "visual_key", "stable_prompt", "核心制作要求", "制作要求")),
            "personality": _clean_text(_pick(item, "personality", "性格特点", "性格")),
            "background": _clean_text(_pick(item, "background", "身份背景", "背景")),
            "priority": _asset_priority(_pick(item, "priority", "visual_priority", "level", "优先级", "视觉优先级")),
            "visual_priority": _asset_priority(_pick(item, "visual_priority", "priority", "level", "视觉优先级", "优先级")),
            "stable_prompt": _clean_text(_pick(item, "stable_prompt", "reference_prompt", "稳定参考提示词")),
            "notes": _clean_text(_pick(item, "notes", "remark", "备注")),
        }
        description = "；".join(value for value in metadata.values() if value)
        assets.append(
            {
                "type": "character",
                "asset_type": "character",
                "asset_code": metadata["asset_code"],
                "name": name,
                "description": description,
                "metadata": metadata,
                "source_label": "agent",
                "character_type": metadata["character_type"],
                "priority": metadata["priority"],
                "relationship": metadata["relationship"],
                "role": metadata["role"],
                "appearance": metadata["appearance"],
                "core_requirement": metadata["core_requirement"],
                "visual_presence": metadata["visual_presence"],
                **_asset_relation_fields(item),
            }
        )
    return assets


def _normalize_scene_assets(raw: Any) -> list[dict[str, Any]]:
    assets = []
    for item in _as_dict_list(raw):
        name = _clean_text(_pick(item, "name", "scene_name", "场景名称"))
        if not name:
            continue
        spatial_zones = _as_text_list(_pick(item, "spatial_zones", "space_zones", "spatial_distribution", "space_distribution", "空间分布", "空间分区"))
        key_props = _as_text_list(_pick(item, "key_props", "props", "important_props", "关键道具"))
        key_prop_ids = _as_text_list(_pick(item, "key_prop_ids", "prop_ids", "关键道具ID"))
        continuity_risks = _as_text_list(_pick(item, "continuity_risks", "continuity_risk", "continuity_notes", "连续性风险"))
        metadata = {
            "asset_code": _clean_text(_pick(item, "asset_code", "id", "ID")),
            "scene_no": _clean_text(_pick(item, "scene_no", "scene_code", "场景编号")),
            "interior_exterior": _clean_text(_pick(item, "interior_exterior", "内外景")) or "未明确",
            "time": _clean_text(_pick(item, "time", "scene_time", "时间")),
            "location": _clean_text(_pick(item, "location", "地点")),
            "description": _clean_text(_pick(item, "description", "scene_description", "描述")),
            "atmosphere": _clean_text(_pick(item, "atmosphere", "氛围/备注", "备注")),
            "narrative_function": _clean_text(_pick(item, "narrative_function", "core_function", "story_function", "叙事功能", "核心功能")),
            "spatial_zones": spatial_zones,
            "visual_goal": _clean_text(_pick(item, "visual_goal", "lighting_rhythm", "lighting", "visual_target", "视觉目标", "光线节奏")),
            "priority": _asset_priority(_pick(item, "priority", "visual_priority", "level", "优先级", "视觉优先级")),
            "key_props": key_props,
            "key_prop_ids": key_prop_ids,
            "continuity_risks": continuity_risks,
            "episode_code": _clean_text(_pick(item, "episode_code", "episode", "对应分集", "分集ID")),
        }
        description = "；".join(_clean_text(value) for value in metadata.values() if _clean_text(value))
        assets.append(
            {
                "type": "scene",
                "asset_type": "scene",
                "asset_code": metadata["asset_code"],
                "name": name,
                "description": description,
                "metadata": metadata,
                "source_label": "agent",
                "priority": metadata["priority"],
                "time": metadata["time"],
                "narrative_function": metadata["narrative_function"],
                "spatial_zones": metadata["spatial_zones"],
                "visual_goal": metadata["visual_goal"],
                "key_props": metadata["key_props"],
                "key_prop_ids": metadata["key_prop_ids"],
                "continuity_risks": metadata["continuity_risks"],
                "episode_code": metadata["episode_code"],
                **_asset_relation_fields(item),
            }
        )
    return assets


def _normalize_prop_assets(raw: Any) -> list[dict[str, Any]]:
    assets = []
    for item in _as_dict_list(raw):
        name = _clean_text(_pick(item, "name", "prop_name", "道具名称"))
        if not name:
            continue
        scene_names = _normalize_name_list(_pick(item, "scene_names", "scene", "usage_scene", "scene_name", "对应场景", "使用场景"))
        scene_ids = _normalize_id_list(_pick(item, "scene_ids", "scene_id", "scene_code", "对应场景ID"))
        if not scene_ids:
            scene_ids = _extract_reference_ids(_pick(item, "scene_names", "scene", "usage_scene", "scene_name", "对应场景", "使用场景"), prefixes=("S", "SC"))
        metadata = {
            "asset_code": _clean_text(_pick(item, "asset_code", "id", "ID")),
            "level": _clean_text(_pick(item, "level", "级别")),
            "prop_type": _prop_type_value(_pick(item, "prop_type", "type", "类型"), name=name, context=_clean_text(_pick(item, "appearance", "visual", "look", "外观"))),
            "appearance": _clean_text(_pick(item, "appearance", "visual", "look", "外观")),
            "source": _clean_text(_pick(item, "source", "origin", "出处")),
            "priority": _asset_priority(_pick(item, "priority", "level", "visual_priority", "优先级", "视觉优先级")),
            "status_change": _clean_text(_pick(item, "status_change", "state_change", "change_note", "notes", "remark", "备注", "状态变化说明")),
            "owner_character": _clean_text(_pick(item, "owner_character", "owner", "character", "所属角色")),
            "owner_character_id": _clean_text(_pick(item, "owner_character_id", "owner_id", "character_id", "所属角色ID")),
            "scene_names": scene_names,
            "scene_ids": scene_ids,
            "usage_scene": _clean_text(_pick(item, "usage_scene", "使用场景")),
            "scene_description": _clean_text(_pick(item, "scene_description", "description", "场景说明")),
        }
        description = "；".join(_clean_text(value) for value in metadata.values() if _clean_text(value))
        assets.append(
            {
                "type": "prop",
                "asset_type": "prop",
                "asset_code": metadata["asset_code"],
                "name": name,
                "description": description,
                "metadata": metadata,
                "source_label": "agent",
                "priority": metadata["priority"],
                "prop_type": metadata["prop_type"],
                "appearance": metadata["appearance"],
                "source": metadata["source"],
                "status_change": metadata["status_change"],
                "owner_character": metadata["owner_character"],
                "owner_character_id": metadata["owner_character_id"],
                "scene_names": metadata["scene_names"],
                "scene_ids": metadata["scene_ids"],
                **_asset_relation_fields(item),
            }
        )
    return assets


def _normalize_generic_assets(raw: Any) -> list[dict[str, Any]]:
    if isinstance(raw, dict):
        return []
    assets = []
    for item in _as_dict_list(raw):
        name = _clean_text(_pick(item, "name", "asset_name", "名称"))
        if not name:
            continue
        asset_type = _clean_text(_pick(item, "type", "asset_type", "类型")) or "prop"
        asset_type = _normalize_asset_type(asset_type)
        metadata = item.get("metadata") if isinstance(item.get("metadata"), dict) else {}
        assets.append(
            {
                "type": asset_type,
                "asset_type": asset_type,
                "asset_code": _clean_text(_pick(item, "asset_code", "id", "ID")),
                "name": name,
                "description": _clean_text(_pick(item, "description", "summary", "说明")),
                "metadata": metadata,
                "source_label": "agent",
                **_asset_relation_fields(item),
            }
        )
    return assets


def _asset_relation_fields(item: dict[str, Any]) -> dict[str, Any]:
    fields: dict[str, Any] = {
        "related_storyboard_codes": _as_text_list(_pick(item, "related_storyboard_codes", "storyboard_codes", "关联分镜")),
        "related_script_segment_codes": _as_text_list(_pick(item, "related_script_segment_codes", "script_segment_codes", "关联脚本段")),
    }
    relation_evidence = item.get("relation_evidence") or item.get("关联证据")
    if isinstance(relation_evidence, list):
        fields["relation_evidence"] = _as_dict_list(relation_evidence)
    relation_confidence = _clean_text(_pick(item, "relation_confidence", "confidence", "关联置信度"))
    if relation_confidence:
        fields["relation_confidence"] = relation_confidence
    return fields


def _asset_priority(value: Any) -> str:
    raw = _clean_text(value).upper()
    if raw in {"S", "A", "B", "C"}:
        return raw
    mapping = {
        "0": "S",
        "1": "S",
        "2": "A",
        "3": "B",
        "4": "C",
        "最高": "S",
        "核心": "S",
        "主资产": "S",
        "高": "A",
        "重要": "A",
        "中": "B",
        "常规": "B",
        "低": "C",
        "记录": "C",
        "可选": "C",
    }
    return mapping.get(raw, "C")


def _normalize_script_reading_output(parsed: dict[str, Any], payload: dict[str, Any]) -> dict[str, Any]:
    report = _normalize_script_reading_report(parsed, payload)
    forbidden_asset_fields = {
        "role_candidates", "scene_candidates", "prop_candidates", "asset_change_proposals",
        "asset_inventory_summary", "characters", "scenes", "props", "assets", "asset_draft",
    }
    return {
        **{key: value for key, value in parsed.items() if key not in forbidden_asset_fields},
        "schema_version": "reading_report_v1",
        "reading_report": report,
        "story_overview": report["story_overview"],
        "platform_strategy": report["platform_strategy"],
        "relationship_map": list(report.get("relationship_map") or []),
        "worldview": dict(report.get("worldview") or {}),
        "emotion_curve": list(report.get("emotion_curve") or []),
        "visual_style": dict(report.get("visual_style") or {}),
        "cultural_origin": dict(report.get("cultural_origin") or {}),
        "recommended_visual_style": dict(report.get("recommended_visual_style") or {}),
        "segmentation_guidance": dict(report.get("segmentation_guidance") or {}),
        "production_risks": list(report.get("production_risks") or []),
        "manual_review_items": list(report.get("manual_review_items") or []),
    }


def _storyboard_segment_inputs(
    script_segments: list[dict[str, Any]],
    resolved_brief: Any = None,
) -> list[dict[str, Any]]:
    brief = resolved_brief if isinstance(resolved_brief, dict) else {}
    grammar_map = brief.get("narrative_grammar_by_context") if isinstance(brief.get("narrative_grammar_by_context"), dict) else {}
    primary_context = _clean_text(brief.get("primary_cultural_context_code"))
    aspect = aspect_profile(brief.get("delivery_aspect_ratio")) or {"rhythm_multiplier": 1.0}
    aspect_multiplier = float(aspect.get("rhythm_multiplier") or 1.0)
    inputs: list[dict[str, Any]] = []
    for segment in script_segments:
        source_duration = _coerce_int(segment.get("estimated_duration_seconds"), _PARENT_STORYBOARD_MIN_SECONDS)
        metadata = segment.get("metadata") if isinstance(segment.get("metadata"), dict) else {}
        context_code = _clean_text(
            segment.get("context_code") or metadata.get("context_code") or primary_context
        )
        grammar_id = _clean_text(grammar_map.get(context_code) or "cn_drama")
        grammar = rhythm_profile(grammar_id) or {"base_shot_seconds": _PARENT_STORYBOARD_MIN_SECONDS, "camera_rules": []}
        target_shot_seconds = max(
            float(_PARENT_STORYBOARD_MIN_SECONDS),
            float(grammar.get("base_shot_seconds") or _PARENT_STORYBOARD_MIN_SECONDS) * aspect_multiplier,
        )
        semantic_multiplier = _segment_rhythm_multiplier(segment)
        target_shot_seconds = max(
            float(_PARENT_STORYBOARD_MIN_SECONDS),
            min(float(_PARENT_STORYBOARD_MAX_SECONDS), target_shot_seconds * semantic_multiplier),
        )
        planned_count = _planned_storyboard_count(source_duration, target_shot_seconds)
        duration_budget = max(
            _PARENT_STORYBOARD_MIN_SECONDS * planned_count,
            min(_PARENT_STORYBOARD_MAX_SECONDS * planned_count, source_duration),
        )
        inputs.append({
            "script_segment_code": segment.get("script_segment_code"),
            "episode_code": segment.get("episode_code"),
            "order_no": segment.get("order_no"),
            "title": segment.get("title"),
            "source_text": _clean_text(segment.get("source_text"))[:1800],
            "dialogue_lines": _as_text_list(segment.get("dialogue_lines") or metadata.get("dialogue_lines")),
            "summary": segment.get("summary"),
            "story_function": segment.get("story_function"),
            "dominant_emotion": segment.get("dominant_emotion"),
            "rhythm": segment.get("rhythm"),
            "viewpoint": segment.get("viewpoint"),
            "estimated_duration_seconds": source_duration,
            "duration_budget_seconds": duration_budget,
            "planned_shot_count": planned_count,
            "target_shot_seconds": round(target_shot_seconds, 2),
            "visual_beat_count": max(planned_count, _coerce_int(metadata.get("visual_beat_count"), planned_count)),
            "context_code": context_code,
            "render_mode": _clean_text(segment.get("render_mode") or metadata.get("render_mode")) or (
                "live_action" if brief.get("content_type") == "live_action_drama" else "animated"
            ),
            "narrative_grammar": grammar_id,
            "camera_rules": list(grammar.get("camera_rules") or []),
            "role_refs": list(segment.get("role_refs") or []),
            "scene_refs": list(segment.get("scene_refs") or []),
            "prop_refs": list(segment.get("prop_refs") or []),
            "production_notes": list(segment.get("production_notes") or []),
        })
    return inputs


def _segment_rhythm_multiplier(segment: dict[str, Any]) -> float:
    text = " ".join(_clean_text(segment.get(key)) for key in ("story_function", "dominant_emotion", "rhythm", "title"))
    if any(token in text for token in ("动作", "追逐", "冲突", "揭示", "反转", "爆发", "快")):
        return 0.8
    if any(token in text for token in ("情绪", "悲伤", "告别", "沉默", "留白", "回忆", "慢")):
        return 1.2
    return 1.0


def _planned_storyboard_count(duration_seconds: int, target_shot_seconds: float = 10.0) -> int:
    duration = max(_PARENT_STORYBOARD_MIN_SECONDS, _coerce_int(duration_seconds, _PARENT_STORYBOARD_MIN_SECONDS))
    minimum_count = max(1, (duration + _PARENT_STORYBOARD_MAX_SECONDS - 1) // _PARENT_STORYBOARD_MAX_SECONDS)
    maximum_count = duration // _PARENT_STORYBOARD_MIN_SECONDS
    if maximum_count < minimum_count:
        return minimum_count
    target_count = max(1, round(duration / max(float(_PARENT_STORYBOARD_MIN_SECONDS), target_shot_seconds)))
    return max(minimum_count, min(maximum_count, target_count))


def _normalize_storyboard_batch(parsed: dict[str, Any], segment_plans: list[dict[str, Any]]) -> dict[str, Any]:
    if not isinstance(parsed, dict):
        return {}
    normalized = dict(parsed)
    storyboards = _as_dict_list(parsed.get("storyboards"))
    if storyboards:
        plans = {_clean_text(item.get("script_segment_code")): item for item in segment_plans}
        for storyboard in storyboards:
            plan = plans.get(_clean_text(storyboard.get("script_segment_code"))) or {}
            storyboard["context_code"] = _clean_text(storyboard.get("context_code") or plan.get("context_code"))
            storyboard["render_mode"] = _clean_text(storyboard.get("render_mode") or plan.get("render_mode"))
        normalized["storyboards"] = storyboards
    return normalized


def _storyboard_batch_plan_errors(parsed: dict[str, Any], segment_plans: list[dict[str, Any]]) -> list[str]:
    storyboards = _as_dict_list(parsed.get("storyboards")) if parsed else []
    expected = {
        _clean_text(plan.get("script_segment_code")): _coerce_int(plan.get("planned_shot_count"), 1)
        for plan in segment_plans
    }
    counts = {code: 0 for code in expected}
    orders: dict[str, list[int]] = {code: [] for code in expected}
    durations: dict[str, list[int]] = {code: [] for code in expected}
    dialogues: dict[str, list[str]] = {code: [] for code in expected}
    seen: set[tuple[str, str, str]] = set()
    errors: list[str] = []
    for index, storyboard in enumerate(storyboards, start=1):
        code = _clean_text(storyboard.get("script_segment_code"))
        if code not in expected:
            errors.append(f"storyboards[{index - 1}] 引用了未知脚本段 {code or '空值'}")
            continue
        counts[code] += 1
        orders[code].append(_coerce_int(storyboard.get("order_num"), counts[code]))
        duration = _coerce_int(storyboard.get("duration_seconds"), 0)
        durations[code].append(duration)
        dialogues[code].extend(_storyboard_dialogue_lines(storyboard.get("dialogue")))
        if duration < _PARENT_STORYBOARD_MIN_SECONDS or duration > _PARENT_STORYBOARD_MAX_SECONDS:
            errors.append(f"{code} 第 {counts[code]} 条父分镜时长必须为 10-15 秒，实际 {duration} 秒")
        if _clean_text(storyboard.get("shot_no")):
            errors.append(f"{code} 第 {counts[code]} 条分镜禁止输出 shot_no，由后台生成正式编号")
        shot_size = _clean_text(storyboard.get("shot_size"))
        if not shot_size or re.search(r"\b(?:ECU|CU|MCU|MS|MFS|MLS|LS|WS|EWS|FS)\b", shot_size, flags=re.I):
            errors.append(f"{code} 第 {counts[code]} 条分镜 shot_size 必须使用中文景别")
        description = _clean_text(storyboard.get("description"))
        if not description:
            errors.append(f"{code} 第 {counts[code]} 条分镜缺少画面描述")
        mirror_shots = _as_dict_list(storyboard.get("mirror_shots"))
        if len(mirror_shots) == 1 or len(mirror_shots) > 6:
            errors.append(f"{code} 第 {counts[code]} 条分镜的镜中分镜必须为 0 项或 2-6 项")
        mirror_total = 0.0
        child_dialogue_lines: list[str] = []
        previous_signature: tuple[str, str] | None = None
        for mirror_index, mirror in enumerate(mirror_shots, start=1):
            mirror_duration = _coerce_float(mirror.get("duration_seconds"))
            if mirror_duration is None or mirror_duration <= 0:
                errors.append(f"{code} 第 {counts[code]} 条分镜的子镜头 {mirror_index} 时长必须大于 0 秒")
            else:
                mirror_total += mirror_duration
            for field in (
                "shot_function", "description", "camera", "movement_reason", "shot_size",
                "environmental_pressure", "micro_action", "sound_or_motif",
            ):
                if not _clean_text(mirror.get(field)):
                    errors.append(f"{code} 第 {counts[code]} 条分镜的子镜头 {mirror_index} 缺少 {field}")
            shot_function = _clean_text(mirror.get("shot_function"))
            if shot_function and shot_function not in _MIRROR_SHOT_FUNCTIONS:
                errors.append(
                    f"{code} 第 {counts[code]} 条分镜的子镜头 {mirror_index} shot_function "
                    f"“{shot_function}” 无效，只能取以下之一："
                    f"{'、'.join(_MIRROR_SHOT_FUNCTIONS)}"
                )
            signature = (_clean_text(mirror.get("shot_size")), _clean_text(mirror.get("camera")))
            if previous_signature == signature:
                errors.append(f"{code} 第 {counts[code]} 条分镜的相邻子镜头 {mirror_index - 1}/{mirror_index} 缺少明确景别或角度切换")
            previous_signature = signature
            child_dialogue_lines.extend(_storyboard_dialogue_lines(mirror.get("dialogue")))
        if mirror_shots and abs(mirror_total - duration) > 0.1:
            errors.append(f"{code} 第 {counts[code]} 条分镜的子镜头合计 {mirror_total:g} 秒，必须等于父分镜 {duration} 秒")
        parent_dialogue_lines = _storyboard_dialogue_lines(storyboard.get("dialogue"))
        if mirror_shots and parent_dialogue_lines != child_dialogue_lines:
            expected_lines = json.dumps(parent_dialogue_lines, ensure_ascii=False)
            actual_lines = json.dumps(child_dialogue_lines, ensure_ascii=False)
            errors.append(
                f"{code} 第 {counts[code]} 条分镜的子镜头台词必须逐行完整覆盖父分镜；"
                f"期望 {expected_lines}，实际 {actual_lines}。说话人前缀、字词和全半角标点必须逐字复制，"
                "禁止拆句、漏句、重复、换序或改写"
            )
        signature = (code, _dedupe_text(description), _dedupe_text(storyboard.get("dialogue")))
        if signature in seen:
            errors.append(f"{code} 存在重复分镜内容")
        seen.add(signature)
    for code, expected_count in expected.items():
        if counts[code] != expected_count:
            errors.append(f"{code} 必须输出 {expected_count} 条分镜，实际 {counts[code]} 条")
        if sorted(orders[code]) != list(range(1, counts[code] + 1)):
            errors.append(f"{code} 的 order_num 必须从 1 连续编号")
        expected_budget = next(
            _coerce_int(plan.get("duration_budget_seconds"), _PARENT_STORYBOARD_MIN_SECONDS)
            for plan in segment_plans
            if _clean_text(plan.get("script_segment_code")) == code
        )
        if sum(durations[code]) != expected_budget:
            errors.append(f"{code} 分镜时长合计必须为 {expected_budget} 秒，实际 {sum(durations[code])} 秒")
        plan = next(
            item for item in segment_plans
            if _clean_text(item.get("script_segment_code")) == code
        )
        expected_dialogue = _storyboard_dialogue_lines(plan.get("dialogue_lines"))
        if expected_dialogue and dialogues[code] != expected_dialogue:
            expected_lines = json.dumps(expected_dialogue, ensure_ascii=False)
            actual_lines = json.dumps(dialogues[code], ensure_ascii=False)
            errors.append(
                f"{code} 的父分镜台词必须逐行完整覆盖 confirmed_script_segments[].dialogue_lines；"
                f"期望 {expected_lines}，实际 {actual_lines}。说话人前缀、字词和全半角标点必须逐字复制，"
                "禁止拆句、漏句、重复、换序或改写"
            )
    return errors


def _dedupe_text(value: Any) -> str:
    return "".join(_clean_text(value).lower().split()).strip("，。；：,.!?！？")


def _storyboard_dialogue_lines(value: Any) -> list[str]:
    if isinstance(value, list):
        return [line for item in value for line in _storyboard_dialogue_lines(item)]
    return [line.strip() for line in str(value or "").splitlines() if line.strip()]


def _normalize_dialogue_text(value: Any) -> str:
    return "\n".join(_storyboard_dialogue_lines(value))


def _normalize_shot_size_cn(value: Any) -> str:
    text = _clean_text(value).replace("->", "→").replace("-", "→")
    if not text:
        return "未明确"
    labels = {
        "ECU": "极近特写",
        "CU": "特写",
        "MCU": "中近景",
        "MS": "中景",
        "MFS": "中全景",
        "MLS": "中全景",
        "FS": "全景",
        "LS": "全景",
        "WS": "远景",
        "EWS": "大远景",
        "ELS": "大远景",
    }
    parts = [part.strip() for part in text.split("→") if part.strip()]
    return " → ".join(labels.get(part.upper(), part) for part in parts)


def _storyboard_only_schema() -> dict[str, Any]:
    contract_schema = get_skill_contract("script-breakdown").output_schema
    fields = ("storyboards", "missing_asset_suggestions", "manual_review_items", "notes", "source")
    return {field: contract_schema[field] for field in fields}


def _strict_reviewed_asset_inventory(payload: dict[str, Any]) -> dict[str, Any]:
    review_state = payload.get("asset_review_state")
    inventory = payload.get("normalized_asset_inventory")
    if not isinstance(review_state, dict) or str(review_state.get("status") or "") != "confirmed":
        raise ValueError("资产清单尚未人工确认")
    if not isinstance(inventory, dict):
        raise ValueError("normalized_asset_inventory 必须是 AssetNormalization.v1 对象")
    return AssetNormalizationResultV1.model_validate(inventory).model_dump(mode="python")


def _compact_asset_reference_index(raw: Any) -> list[dict[str, str]]:
    assets: list[dict[str, str]] = []
    seen: set[str] = set()
    for item in _as_dict_list(raw):
        code = _clean_text(_pick(item, "asset_code", "code", "id")).upper()
        if not code or code in seen:
            continue
        seen.add(code)
        assets.append({
            "asset_code": code,
            "asset_type": _clean_text(_pick(item, "asset_type", "type")),
            "name": _clean_text(_pick(item, "name", "asset_name")),
        })
    return assets


def _normalize_visual_presence(value: Any, *, character_type: Any = None) -> str:
    normalized = _clean_text(value).lower().replace("-", "_").replace(" ", "_")
    if normalized in {"mentioned_only", "mentioned", "背景提及"} or any(
        marker in normalized for marker in ("仅提及", "未出场", "不出镜")
    ):
        return "mentioned_only"
    if normalized in {"voice_only", "voice", "off_screen"} or any(
        marker in normalized for marker in ("画外音", "仅声音")
    ):
        return "voice_only"
    if normalized:
        return "on_screen"
    fallback = _clean_text(character_type).lower().replace("-", "_").replace(" ", "_")
    if any(marker in fallback for marker in ("mentioned_only", "背景提及", "仅提及", "未出场", "不出镜")):
        return "mentioned_only"
    if any(marker in fallback for marker in ("voice_only", "off_screen", "画外音", "仅声音")):
        return "voice_only"
    return "on_screen"


def _normalize_script_reading_report(parsed: dict[str, Any], payload: dict[str, Any]) -> dict[str, Any]:
    existing = parsed.get("reading_report") if isinstance(parsed.get("reading_report"), dict) else {}
    key_table = _key_element_table_map(parsed.get("key_element_table"))
    resolved_brief = _resolved_production_brief(payload)
    legacy_key_elements = _dict_or_empty(parsed.get("keyElements"))
    legacy_visual_style = _dict_or_empty(legacy_key_elements.get("视觉风格"))
    story_overview = _dict_or_empty(parsed.get("story_overview")) or _dict_or_empty(existing.get("story_overview"))
    project_overview = _dict_or_empty(parsed.get("project_overview"))
    platform_strategy = _dict_or_empty(parsed.get("platform_strategy")) or _dict_or_empty(existing.get("platform_strategy"))
    role_candidates = _normalize_role_candidates(
        parsed.get("role_candidates")
        or existing.get("role_candidates")
        or parsed.get("characters")
        or _dict_or_empty(parsed.get("asset_draft")).get("characters")
    )
    scene_candidates = _normalize_scene_candidates(
        parsed.get("scene_candidates")
        or existing.get("scene_candidates")
        or parsed.get("scenes")
        or _dict_or_empty(parsed.get("asset_draft")).get("scenes")
    )
    prop_candidates = _normalize_prop_candidates(
        parsed.get("prop_candidates")
        or existing.get("prop_candidates")
        or parsed.get("props")
        or _dict_or_empty(parsed.get("asset_draft")).get("props")
    )
    report = {
        "schema_version": "reading_report_v1",
        "project": {
            "project_id": _clean_text(payload.get("project_id")),
            "project_title": _clean_text(payload.get("project_title") or project_overview.get("title")),
            "project_prefix": _clean_text(payload.get("project_prefix")),
            "genre": _clean_text(payload.get("genre") or project_overview.get("project_type") or key_table.get("project_type")),
            "script_text_chars": len(str(payload.get("script_text") or "")),
            "production_brief": resolved_brief,
            "episode_id": _clean_text(payload.get("episode_id")),
            "episode_code": _clean_text(payload.get("episode_code")),
            "script_id": _clean_text(payload.get("script_id")),
            "script_version_id": _clean_text(payload.get("script_version_id")),
            "script_version_no": _coerce_int(payload.get("script_version_no"), 0),
            "script_source": _dict_or_empty(payload.get("script_source")),
        },
        "source": {
            "source_label": "agent",
            "skill": "script-reading",
        },
        "story_overview": {
            "logline": _clean_text(_pick(story_overview, "logline", "一句话故事")),
            "synopsis": _clean_text(_pick(story_overview, "synopsis", "summary", "剧情概要")) or _clean_text(project_overview.get("summary")),
            "core_conflict": _clean_text(_pick(story_overview, "core_conflict", "核心冲突")) or key_table.get("core_conflict", ""),
            "main_hook": _clean_text(_pick(story_overview, "main_hook", "opening_hook", "开场钩子")),
            "audience_promise": _clean_text(_pick(story_overview, "audience_promise", "爽点", "观众期待")),
            "genre_tags": _as_text_list(_pick(story_overview, "genre_tags", "tags")) or _as_text_list(project_overview.get("genre_tags")),
        },
        "platform_strategy": {
            "primary_platform": "竖屏短剧" if resolved_brief.get("delivery_aspect_ratio") == "9:16" else "横屏短片",
            "aspect_ratio": _clean_text(resolved_brief.get("delivery_aspect_ratio")),
            "target_duration_seconds": _coerce_int(resolved_brief.get("target_duration_seconds"), 0),
            "opening_hook_target_seconds": _coerce_int(resolved_brief.get("opening_hook_seconds"), 0),
            "rhythm_strategy": _clean_text(_pick(platform_strategy, "rhythm_strategy", "节奏策略")) or _resolved_rhythm_label(resolved_brief),
            "composition_strategy": _clean_text(_pick(platform_strategy, "composition_strategy", "构图策略")) or "依据平台安全区控制主体、字幕和关键道具位置。",
            "subtitle_strategy": _clean_text(_pick(platform_strategy, "subtitle_strategy", "字幕策略")) or "由分镜对白密度决定",
            "shot_density": _clean_text(_pick(platform_strategy, "shot_density", "镜头密度")) or _resolved_rhythm_label(resolved_brief),
        },
        "relationship_map": _normalize_relationship_map(
            parsed.get("relationship_map") or existing.get("relationship_map"),
            role_candidates,
        ),
        "worldview": _dict_or_empty(parsed.get("worldview")) or {
            "time_period": key_table.get("time_span", ""),
            "power_system": "",
            "social_order": "",
            "rules": _as_text_list(key_table.get("worldview")),
            "forbidden_misreads": [],
        },
        "emotion_curve": _normalize_named_rows(parsed.get("emotion_curve") or existing.get("emotion_curve"), ("beat", "story_position", "emotion", "intensity", "visual_strategy")),
        "visual_style": _normalize_visual_style(parsed, key_table),
        "cultural_origin": _normalize_cultural_origin(parsed, existing, legacy_visual_style),
        "recommended_visual_style": _normalize_recommended_visual_style(parsed, existing, resolved_brief),
        "segmentation_guidance": _normalize_segmentation_guidance(parsed.get("segmentation_guidance") or existing.get("segmentation_guidance"), resolved_brief),
        "production_risks": _normalize_named_rows(parsed.get("production_risks") or key_table.get("production_risks"), ("risk_type", "severity", "description", "affected_assets", "suggestion")),
        "manual_review_items": normalize_manual_review_items(
            parsed.get("manual_review_items") or parsed.get("unclear_items") or key_table.get("manual_review_items"),
            source="script-reading",
        ),
    }
    confirmed_contexts = _as_dict_list(resolved_brief.get("cultural_contexts"))
    if confirmed_contexts:
        default_code = _clean_text(resolved_brief.get("primary_cultural_context_code"))
        default_context = next(
            (item for item in confirmed_contexts if _clean_text(item.get("context_code")) == default_code),
            confirmed_contexts[0],
        )
        report["cultural_origin"] = {
            "classification": _clean_text(default_context.get("name")),
            "confidence": 1,
            "region": _clean_text(default_context.get("region")),
            "era": _clean_text(default_context.get("era")),
            "signals": [
                {
                    "dimension": "human_confirmed_context",
                    "value": _clean_text(item.get("name")),
                    "evidence": _clean_text(item.get("timeline")),
                    "weight": 1,
                }
                for item in confirmed_contexts
            ],
            "status": "human_confirmed",
            "default_cultural_context_code": _clean_text(default_context.get("context_code")),
            "cultural_contexts": confirmed_contexts,
        }
    if not report["story_overview"]["genre_tags"] and key_table.get("project_type"):
        report["story_overview"]["genre_tags"] = [key_table["project_type"]]
    return report


def _normalize_role_candidates(raw: Any) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for index, item in enumerate(_as_dict_list(raw), start=1):
        name = _clean_text(_pick(item, "name", "character_name", "role_name", "角色名"))
        if not name:
            continue
        code = _normalize_candidate_code(_pick(item, "role_code", "asset_code", "id", "角色编号"), "R", index)
        priority = _asset_priority(_pick(item, "priority", "visual_priority", "level", "优先级"))
        dramatic_function = _clean_text(_pick(item, "dramatic_function", "role", "function", "戏剧功能", "定位"))
        role_type = _clean_text(_pick(item, "role_type", "character_type", "type_label", "角色类型"))
        related_scene_codes, related_scene_names = _normalize_related_scene_references(item)
        rows.append(
            {
                "role_code": code,
                "asset_code": code,
                "name": name,
                "priority": priority,
                "role_type": role_type,
                "dramatic_function": dramatic_function,
                "first_appearance": _clean_text(_pick(item, "first_appearance", "first_seen", "首次出现")),
                "appearance_clues": _as_text_list(_pick(item, "appearance_clues", "appearance", "外貌线索", "外貌")),
                "costume_direction": _clean_text(_pick(item, "costume_direction", "costume", "core_requirement", "服装方向")),
                "continuity_anchors": _as_text_list(_pick(item, "continuity_anchors", "visual_anchors", "stable_anchors", "连续性锚点")),
                "emotional_arc": _clean_text(_pick(item, "emotional_arc", "emotion_curve", "emotion", "情绪曲线", "情绪")) or _role_emotional_arc_fallback(dramatic_function, role_type),
                "prompt_usage": _clean_text(_pick(item, "prompt_usage", "usage", "purpose", "用途")) or _role_prompt_usage_fallback(priority),
                "forbidden_variations": _as_text_list(_pick(item, "forbidden_variations", "forbidden_styles", "禁止变化")),
                "related_scene_codes": related_scene_codes,
                "related_scene_names": related_scene_names,
                "evidence": _clean_text(_pick(item, "evidence", "source_text", "原文依据")),
                "manual_review_items": normalize_manual_review_items(_pick(item, "manual_review_items", "review_items", "待确认"), source="script-reading"),
            }
        )
    return _sort_and_renumber_candidates(rows, "role_code", "R")


def _normalize_scene_candidates(raw: Any) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for index, item in enumerate(_as_dict_list(raw), start=1):
        name = _clean_text(_pick(item, "name", "scene_name", "场景名称"))
        if not name:
            continue
        code = _normalize_candidate_code(_pick(item, "scene_code", "asset_code", "id", "场景编号"), "SC", index)
        priority = _asset_priority(_pick(item, "priority", "visual_priority", "level", "优先级"))
        spatial_zones = _as_text_list(_pick(item, "spatial_zones", "space_zones", "空间分区"))
        mood = _clean_text(_pick(item, "mood", "emotion", "atmosphere", "情绪氛围", "氛围"))
        lighting = _clean_text(_pick(item, "lighting", "lighting_rhythm", "光线"))
        rows.append(
            {
                "scene_code": code,
                "asset_code": code,
                "name": name,
                "priority": priority,
                "cultural_context_code": _clean_text(_pick(item, "cultural_context_code", "context_code", "文化背景编号")),
                "context_code": _clean_text(_pick(item, "context_code", "cultural_context_code", "文化背景编号")),
                "render_mode": _clean_text(_pick(item, "render_mode", "表现类型")),
                "dramatic_function": _clean_text(_pick(item, "dramatic_function", "narrative_function", "story_function", "戏剧功能")),
                "visual_direction": _clean_text(_pick(item, "visual_direction", "visual_goal", "description", "视觉方向")),
                "shot_size": _clean_text(_pick(item, "shot_size", "shot_type", "camera_framing", "camera_direction", "view_angle", "景别", "镜头方位")) or _scene_shot_fallback(spatial_zones),
                "camera_direction": _clean_text(_pick(item, "camera_direction", "camera_angle", "view_angle", "镜头方位")),
                "mood": mood or lighting or "按剧情情绪和光线状态细化。",
                "lighting": lighting,
                "spatial_zones": spatial_zones,
                "scene_view_plan": _clean_text(_pick(item, "scene_view_plan", "view_plan", "multi_view_plan", "多视角计划")) or _scene_view_plan_fallback(priority, mood or lighting),
                "reuse_potential": _clean_text(_pick(item, "reuse_potential", "reuse", "复用潜力")) or "medium",
                "prompt_usage": _clean_text(_pick(item, "prompt_usage", "usage", "purpose", "用途")) or _scene_prompt_usage_fallback(priority),
                "evidence": _clean_text(_pick(item, "evidence", "source_text", "原文依据")),
            }
        )
    return _sort_and_renumber_candidates(rows, "scene_code", "SC")


def _normalize_prop_candidates(raw: Any) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for index, item in enumerate(_as_dict_list(raw), start=1):
        name = _clean_text(_pick(item, "name", "prop_name", "道具名称"))
        if not name:
            continue
        code = _normalize_candidate_code(_pick(item, "prop_code", "asset_code", "id", "道具编号"), "P", index)
        prop_type = _prop_type_value(_pick(item, "prop_type", "subtype", "type", "子类型", "类型"), name=name, context=_clean_text(_pick(item, "story_function", "function", "故事功能")))
        related_scene_codes, related_scene_names = _normalize_related_scene_references(item)
        rows.append(
            {
                "prop_code": code,
                "asset_code": code,
                "name": name,
                "priority": _asset_priority(_pick(item, "priority", "visual_priority", "level", "优先级")),
                "prop_type": prop_type,
                "story_function": _clean_text(_pick(item, "story_function", "function", "故事功能")),
                "visual_anchor": _clean_text(_pick(item, "visual_anchor", "appearance", "visual", "视觉锚点")),
                "status_changes": _as_text_list(_pick(item, "status_changes", "status_change", "状态变化")),
                "owner_role_code": _clean_text(_pick(item, "owner_role_code", "owner_character_id", "owner_id", "所属角色编号")),
                "related_scene_codes": related_scene_codes,
                "related_scene_names": related_scene_names,
                "prompt_usage": _clean_text(_pick(item, "prompt_usage", "usage", "purpose", "用途")) or _prop_prompt_usage_fallback(prop_type),
                "evidence": _clean_text(_pick(item, "evidence", "source_text", "原文依据")),
            }
        )
    return _sort_and_renumber_candidates(rows, "prop_code", "P")


def _normalize_related_scene_references(item: dict[str, Any]) -> tuple[list[str], list[str]]:
    codes = _as_text_list(_pick(item, "related_scene_codes", "scene_codes"))
    names = _as_text_list(_pick(item, "related_scene_names", "scene_names"))
    related = _pick(item, "related_scenes", "scenes")
    if isinstance(related, list):
        for scene in related:
            if isinstance(scene, dict):
                code = _clean_text(_pick(scene, "scene_code", "asset_code", "code", "场景编号"))
                name = _clean_text(_pick(scene, "scene_name", "name", "场景名称"))
                if code:
                    codes.append(code)
                if name:
                    names.append(name)
            elif isinstance(scene, str):
                if re.fullmatch(r"SC\d+", scene.strip(), flags=re.I):
                    codes.append(scene.strip().upper())
                elif scene.strip():
                    names.append(scene.strip())
    return list(dict.fromkeys(codes)), list(dict.fromkeys(names))


def _role_emotional_arc_fallback(dramatic_function: str, role_type: str) -> str:
    basis = dramatic_function or role_type
    return f"根据{basis}在剧情推进中变化，分镜阶段按脚本段情绪细化。" if basis else "随剧情关键节点变化，分镜阶段细化。"


def _role_prompt_usage_fallback(priority: str) -> str:
    return "用于角色定稿、关键帧、表情近景和图生视频一致性。" if priority in {"S", "A"} else "用于分镜匹配、群像/辅助镜头和连续性记录。"


def _scene_shot_fallback(spatial_zones: list[str]) -> str:
    if spatial_zones:
        return f"先建立全景/中景母版，按空间分区补关键视角：{'、'.join(spatial_zones)}"
    return "先建立全景/中景母版，故事板阶段按镜头需求补视角。"


def _scene_view_plan_fallback(priority: str, mood: str = "") -> str:
    if priority == "S":
        views = ["全景母版", "入口/门口向内", "核心动作区中景", "关键道具/情绪特写"]
    elif priority == "A":
        views = ["全景母版", "核心动作区中景", "必要补充视角"]
    else:
        views = ["全景母版", "按故事板覆盖检查补视角"]
    suffix = f"；光线/情绪：{mood}" if mood else ""
    return "、".join(views) + suffix


def _scene_prompt_usage_fallback(priority: str) -> str:
    return "用于场景母版、多视角图、故事板关键帧和图生视频背景一致性。" if priority in {"S", "A"} else "用于分镜空间定位和必要时补充场景视角。"


def _prop_prompt_usage_fallback(prop_type: str) -> str:
    if prop_type in {"服饰", "配饰"}:
        return "用于角色定稿、穿戴连续性和近景细节。"
    if prop_type == "车辆":
        return "用于场景调度、移动镜头和图生视频连续性。"
    if prop_type == "武器":
        return "用于动作分镜、关键帧和安全降级检查。"
    return "用于场景布置、角色动作和关键帧细节。"


def _prop_type_value(value: Any, *, name: str = "", context: str = "") -> str:
    text = _clean_text(value).lower()
    direct = _clean_text(value)
    allowed = {"服饰", "配饰", "武器", "车辆", "电子设备", "生活用品", "家具陈设", "文件信物", "食物饮品", "其他"}
    if direct in allowed:
        return direct
    source = f"{text} {name} {context}".lower()
    if any(token in source for token in ("服", "衣", "裙", "袍", "鞋", "帽", "costume", "clothes", "cloth", "wear")):
        return "服饰"
    if any(token in source for token in ("饰", "戒", "链", "簪", "耳环", "手镯", "accessory", "jewelry")):
        return "配饰"
    if any(token in source for token in ("刀", "剑", "枪", "弓", "箭", "武器", "weapon")):
        return "武器"
    if any(token in source for token in ("车", "马车", "汽车", "船", "飞行器", "vehicle", "car")):
        return "车辆"
    if any(token in source for token in ("手机", "电脑", "屏", "耳机", "相机", "电子", "device", "phone", "computer")):
        return "电子设备"
    if any(token in source for token in ("桌", "椅", "床", "柜", "灯", "窗帘", "家具", "陈设", "furniture")):
        return "家具陈设"
    if any(token in source for token in ("信", "文件", "合同", "照片", "玉佩", "信物", "token", "document")):
        return "文件信物"
    if any(token in source for token in ("饭", "菜", "酒", "茶", "水", "杯", "食物", "饮品", "food", "drink")):
        return "食物饮品"
    if any(token in source for token in ("碗", "伞", "包", "钥匙", "生活", "日用品", "daily")):
        return "生活用品"
    return "其他"


def _sort_and_renumber_candidates(rows: list[dict[str, Any]], code_key: str, prefix: str) -> list[dict[str, Any]]:
    priority_rank = {"S": 0, "A": 1, "B": 2, "C": 3}
    sorted_rows = sorted(
        rows,
        key=lambda item: (
            priority_rank.get(_asset_priority(item.get("priority")), 3),
            _candidate_code_number(item.get(code_key) or item.get("asset_code")),
            _clean_text(item.get("name")),
        ),
    )
    for index, item in enumerate(sorted_rows, start=1):
        code = f"{prefix}{index:03d}"
        item[code_key] = code
        item["asset_code"] = code
    return sorted_rows


def _candidate_code_number(value: Any) -> int:
    match = re.search(r"(\d+)", _clean_text(value))
    return int(match.group(1)) if match else 10**9


def _normalize_candidate_code(value: Any, prefix: str, index: int) -> str:
    raw = _clean_text(value).upper().replace("-", "")
    if prefix == "SC" and raw.startswith("S") and raw[1:].isdigit():
        raw = f"SC{int(raw[1:]):03d}"
    if raw.startswith(prefix) and raw[len(prefix):].isdigit():
        return f"{prefix}{int(raw[len(prefix):]):03d}"
    return f"{prefix}{index:03d}"


def _reading_candidates_to_assets(candidates: list[dict[str, Any]], asset_type: str) -> list[dict[str, Any]]:
    code_key = {"character": "role_code", "scene": "scene_code", "prop": "prop_code"}[asset_type]
    return [
        {
            "type": asset_type,
            "asset_type": asset_type,
            "asset_code": item.get(code_key) or item.get("asset_code"),
            "name": item.get("name"),
            "description": _clean_text(item.get("dramatic_function") or item.get("visual_direction") or item.get("story_function")),
            "priority": item.get("priority") or "C",
            "metadata": item,
            "source_label": "script-reading",
        }
        for item in candidates
    ]


def _normalize_asset_inventory_summary(raw: Any, roles: list[dict[str, Any]], scenes: list[dict[str, Any]], props: list[dict[str, Any]]) -> dict[str, Any]:
    summary = _dict_or_empty(raw)
    if summary:
        return summary
    grouped_roles = _group_codes_by_priority(roles, "role_code")
    grouped_scenes = _group_codes_by_priority(scenes, "scene_code")
    grouped_props = _group_codes_by_priority(props, "prop_code")
    must_lock = [
        *grouped_roles["S"],
        *grouped_roles["A"],
        *grouped_scenes["S"],
        *grouped_scenes["A"],
        *grouped_props["S"],
        *grouped_props["A"],
    ]
    return {"roles": grouped_roles, "scenes": grouped_scenes, "props": grouped_props, "must_lock_before_storyboard": must_lock}


def _group_codes_by_priority(items: list[dict[str, Any]], code_key: str) -> dict[str, list[str]]:
    grouped = {"S": [], "A": [], "B": [], "C": []}
    for item in items:
        priority = _asset_priority(item.get("priority"))
        code = _clean_text(item.get(code_key) or item.get("asset_code"))
        if code:
            grouped[priority].append(code)
    return grouped


def _normalize_segmentation_guidance(raw: Any, resolved_brief: dict[str, Any]) -> dict[str, Any]:
    data = _dict_or_empty(raw)
    return {
        "recommended_episode_count": _coerce_int(_pick(data, "recommended_episode_count", "episode_count"), 1),
        "target_duration_seconds": _coerce_int(_pick(data, "target_duration_seconds", "duration"), _coerce_int(resolved_brief.get("target_duration_seconds"), 0)),
        "segment_strategy": _clean_text(_pick(data, "segment_strategy", "strategy")) or "按冲突转折、场景变化和人物目标变化拆段。",
        "opening_hook_requirement": _clean_text(_pick(data, "opening_hook_requirement", "opening_hook")),
        "segment_density": _clean_text(_pick(data, "segment_density", "density")) or _resolved_rhythm_label(resolved_brief),
        "must_keep_together": _as_text_list(_pick(data, "must_keep_together", "keep_together")),
        "must_split_before": _as_text_list(_pick(data, "must_split_before", "split_before")),
    }


def _normalize_asset_change_proposals(
    raw: Any,
    roles: list[dict[str, Any]],
    scenes: list[dict[str, Any]],
    props: list[dict[str, Any]],
    existing_assets: Any,
) -> list[dict[str, Any]]:
    candidates = [
        *[("character", item) for item in roles],
        *[("scene", item) for item in scenes],
        *[("prop", item) for item in props],
    ]
    baseline = _as_dict_list(existing_assets)
    baseline_by_code = {
        _clean_text(item.get("asset_code")).upper(): item
        for item in baseline
        if _clean_text(item.get("asset_code"))
    }
    baseline_by_name = {
        (_clean_text(item.get("asset_type")).lower(), _clean_text(item.get("name")).casefold()): item
        for item in baseline
        if _clean_text(item.get("name"))
    }
    candidate_map: dict[str, tuple[str, dict[str, Any]]] = {}
    for asset_type, item in candidates:
        code = _clean_text(item.get("asset_code") or item.get(f"{asset_type}_code") or item.get("role_code") or item.get("scene_code") or item.get("prop_code")).upper()
        if code:
            candidate_map[code] = (asset_type, item)

    normalized: list[dict[str, Any]] = []
    allowed_actions = {"new", "supplement", "conflict", "reuse"}
    for proposal in _as_dict_list(raw):
        code = _clean_text(_pick(proposal, "candidate_code", "asset_code", "code")).upper()
        asset_type, candidate = candidate_map.get(code, (_clean_text(proposal.get("asset_type")).lower(), {}))
        name = _clean_text(_pick(proposal, "candidate_name", "name")) or _clean_text(candidate.get("name"))
        matched = baseline_by_code.get(code) or baseline_by_name.get((asset_type, name.casefold()))
        action = _clean_text(proposal.get("action")).lower()
        if action not in allowed_actions:
            action = "reuse" if matched else "new"
        normalized.append({
            "asset_type": asset_type,
            "candidate_code": code,
            "candidate_name": name,
            "matched_asset_id": _clean_text(proposal.get("matched_asset_id")) or _clean_text((matched or {}).get("asset_id")),
            "matched_asset_code": _clean_text(proposal.get("matched_asset_code")) or _clean_text((matched or {}).get("asset_code")),
            "action": action,
            "changed_fields": _as_text_list(proposal.get("changed_fields")),
            "reason": _clean_text(proposal.get("reason")),
            "evidence": _clean_text(proposal.get("evidence")),
            "confidence": proposal.get("confidence"),
        })
    if normalized:
        return normalized

    for asset_type, candidate in candidates:
        code = _clean_text(candidate.get("asset_code") or candidate.get("role_code") or candidate.get("scene_code") or candidate.get("prop_code")).upper()
        name = _clean_text(candidate.get("name"))
        matched = baseline_by_code.get(code) or baseline_by_name.get((asset_type, name.casefold()))
        normalized.append({
            "asset_type": asset_type,
            "candidate_code": code,
            "candidate_name": name,
            "matched_asset_id": _clean_text((matched or {}).get("asset_id")),
            "matched_asset_code": _clean_text((matched or {}).get("asset_code")),
            "action": "reuse" if matched else "new",
            "changed_fields": [],
            "reason": "后台按资产编号或同类型名称匹配" if matched else "当前资产母版未找到匹配项",
            "evidence": _clean_text(candidate.get("evidence")),
            "confidence": 1 if matched and code else 0.7,
        })
    return normalized


def _normalize_visual_style(parsed: dict[str, Any], key_table: dict[str, str]) -> dict[str, Any]:
    data = _dict_or_empty(parsed.get("visual_style"))
    return {
        "style_keywords": _as_text_list(_pick(data, "style_keywords", "keywords")) or _as_text_list(key_table.get("visual_style")),
        "color_palette": _as_text_list(_pick(data, "color_palette", "palette")),
        "lighting_rules": _as_text_list(_pick(data, "lighting_rules", "lighting")),
        "camera_language": _as_text_list(_pick(data, "camera_language", "camera")),
        "forbidden_styles": _as_text_list(_pick(data, "forbidden_styles", "forbidden_misreads")),
    }


def _normalize_cultural_origin(
    parsed: dict[str, Any],
    existing: dict[str, Any],
    legacy_visual_style: dict[str, Any],
) -> dict[str, Any]:
    data = _dict_or_empty(parsed.get("cultural_origin")) or _dict_or_empty(existing.get("cultural_origin"))
    classification = _clean_text(
        _pick(data, "classification", "cultural_origin", "文化背景")
        or _pick(_dict_or_empty(parsed.get("visual_style")), "cultural_origin", "文化背景")
        or legacy_visual_style.get("文化背景")
    )
    signal_text = _clean_text(
        _pick(data, "signal_source", "文化背景信号来源")
        or legacy_visual_style.get("文化背景信号来源")
    )
    signals = _as_dict_list(data.get("signals"))
    if signal_text and not signals:
        signals = [{"dimension": "summary", "value": classification, "evidence": signal_text, "weight": None}]
    return {
        "classification": classification,
        "confidence": _confidence_number(data.get("confidence")),
        "region": _clean_text(_pick(data, "region", "地域")),
        "era": _clean_text(_pick(data, "era", "time_period", "时代")),
        "signals": signals,
        "status": _clean_text(data.get("status")) or ("agent_inferred" if classification and classification != "[待确认]" else "needs_confirmation"),
    }


def _normalize_recommended_visual_style(
    parsed: dict[str, Any],
    existing: dict[str, Any],
    resolved_brief: dict[str, Any],
) -> dict[str, Any]:
    primary = _dict_or_empty(resolved_brief.get("resolved_style"))
    if primary:
        return {
            "catalog_version": _clean_text(resolved_brief.get("style_catalog_version")),
            "primary_style_id": primary["style_id"],
            "primary_style_label": primary["label"],
            "alternative_style_ids": [],
            "modifier_ids": [],
            "modifier_labels": [],
            "reason": "项目导入阶段已由人工选择，作为下游生产硬约束。",
            "confidence": 1.0,
            "status": "human_confirmed",
        }
    data = _dict_or_empty(parsed.get("recommended_visual_style")) or _dict_or_empty(existing.get("recommended_visual_style"))
    style_id = _clean_text(_pick(data, "primary_style_id", "style_id", "推荐画面风格"))
    style = get_primary_style(style_id)
    alternatives = [item for item in _as_text_list(_pick(data, "alternative_style_ids", "alternatives")) if get_primary_style(item)]
    modifier_ids = [item for item in _as_text_list(_pick(data, "modifier_ids", "style_modifiers")) if get_style_modifier(item)]
    return {
        "catalog_version": _clean_text(resolved_brief.get("style_catalog_version")),
        "primary_style_id": style_id if style else "",
        "primary_style_label": style.get("label", "") if style else "",
        "alternative_style_ids": alternatives[:2],
        "modifier_ids": modifier_ids[:2],
        "reason": _clean_text(_pick(data, "reason", "recommendation_reason", "推荐理由")),
        "confidence": _confidence_number(data.get("confidence")),
        "status": "agent_recommended" if style else "needs_confirmation",
    }


def _confidence_number(value: Any) -> float | None:
    try:
        number = float(value)
    except (TypeError, ValueError):
        return None
    return max(0.0, min(1.0, number))


def _key_element_table_map(raw: Any) -> dict[str, str]:
    output: dict[str, str] = {}
    for item in _as_dict_list(raw):
        field = _clean_text(_pick(item, "field", "key", "字段"))
        value = _clean_text(_pick(item, "value", "内容", "description"))
        if field and value:
            output[field] = value
    return output


def _dict_or_empty(value: Any) -> dict[str, Any]:
    return dict(value) if isinstance(value, dict) else {}


def _normalize_reading_report(parsed: dict[str, Any]) -> dict[str, Any]:
    raw = parsed.get("reading_report")
    if not isinstance(raw, dict):
        return {}
    basic_info = raw.get("basic_info") if isinstance(raw.get("basic_info"), dict) else {}
    return {
        "basic_info": {
            "scene_count": _clean_text(_pick(basic_info, "scene_count", "总场景数")),
            "character_count": _clean_text(_pick(basic_info, "character_count", "总角色数")),
            "time_span": _clean_text(_pick(basic_info, "time_span", "时间跨度")),
            "story_type": _clean_text(_pick(basic_info, "story_type", "核心叙事类型", "叙事类型")),
            "visual_style": _clean_text(_pick(basic_info, "visual_style", "画风偏好")),
            "target_platform": _clean_text(_pick(basic_info, "target_platform", "目标平台")),
        },
        "scene_flow": _normalize_named_rows(raw.get("scene_flow"), ("scene_code", "scene_name", "key_emotion", "key_visual_moment")),
        "character_overview": _normalize_named_rows(raw.get("character_overview"), ("name", "role", "first_seen", "visual_priority")),
        "key_visual_moments": _as_text_list(raw.get("key_visual_moments")),
        "dependencies": _as_text_list(raw.get("dependencies")),
    }


def _normalize_readthrough(raw: Any) -> list[dict[str, Any]]:
    items = []
    for item in _as_dict_list(raw):
        items.append(
            {
                "scene_code": _clean_text(_pick(item, "scene_code", "场景编号")),
                "scene_name": _clean_text(_pick(item, "scene_name", "name", "场景名称")),
                "spatial_zones": _normalize_named_rows(item.get("spatial_zones"), ("zone", "description")),
                "narrative_function": _clean_text(_pick(item, "narrative_function", "叙事功能")),
                "lighting_rhythm": _clean_text(_pick(item, "lighting_rhythm", "光线节奏")),
                "callbacks": _clean_text(_pick(item, "callbacks", "前后呼应")),
            }
        )
    return items


def _normalize_dialogue_script(raw: Any) -> list[dict[str, Any]]:
    items = []
    for item in _as_dict_list(raw):
        beats = []
        for beat in _as_dict_list(item.get("beats")):
            beats.append(
                {
                    "speaker": _clean_text(_pick(beat, "speaker", "角色", "说话人")),
                    "text": _clean_text(_pick(beat, "text", "dialogue", "台词")),
                    "note": _clean_text(_pick(beat, "note", "action", "动作", "备注")),
                }
            )
        items.append(
            {
                "scene_code": _clean_text(_pick(item, "scene_code", "场景编号")),
                "scene_name": _clean_text(_pick(item, "scene_name", "name", "场景名称")),
                "mood": _clean_text(_pick(item, "mood", "氛围")),
                "time": _clean_text(_pick(item, "time", "时间")),
                "beats": beats,
            }
        )
    return items


def _normalize_keyframe_plan(raw: Any) -> list[dict[str, Any]]:
    rows = []
    for item in _as_dict_list(raw):
        rows.append(
            {
                "round": _clean_text(_pick(item, "round", "轮次")),
                "keyframe_code": _clean_text(_pick(item, "keyframe_code", "编号")),
                "shot_no": _clean_text(_pick(item, "shot_no", "镜号")),
                "title": _clean_text(_pick(item, "title", "内容")),
                "purpose": _clean_text(_pick(item, "purpose", "用途")),
                "priority": _clean_text(_pick(item, "priority", "优先级")),
                "seed_for_video": _clean_text(_pick(item, "seed_for_video", "种子帧用途")),
            }
        )
    return rows


def _normalize_video_plan(raw: Any) -> list[dict[str, Any]]:
    rows = []
    for item in _as_dict_list(raw):
        rows.append(
            {
                "video_code": _clean_text(_pick(item, "video_code", "编号")),
                "shot_range": _clean_text(_pick(item, "shot_range", "镜号")),
                "seed_keyframes": _as_text_list(_pick(item, "seed_keyframes", "基于关键帧")),
                "title": _clean_text(_pick(item, "title", "内容")),
                "mode": _clean_text(_pick(item, "mode", "模式")),
                "duration_seconds": _coerce_int(_pick(item, "duration_seconds", "duration", "时长"), 5),
            }
        )
    return rows


def _normalize_cross_references(raw: Any) -> dict[str, list[dict[str, Any]]]:
    if not isinstance(raw, dict):
        return {"characters": [], "scenes": [], "props": []}
    return {
        "characters": _normalize_ref_rows(raw.get("characters")),
        "scenes": _normalize_ref_rows(raw.get("scenes")),
        "props": _normalize_ref_rows(raw.get("props")),
    }


def _normalize_ref_rows(raw: Any) -> list[dict[str, Any]]:
    rows = []
    for item in _as_dict_list(raw):
        rows.append(
            {
                "asset_code": _clean_text(_pick(item, "asset_code", "id", "ID")),
                "name": _clean_text(_pick(item, "name", "名称")),
                "keyframes": _as_text_list(_pick(item, "keyframes", "关键帧")),
                "videos": _as_text_list(_pick(item, "videos", "视频片段")),
            }
        )
    return rows


def _normalize_delivery_checklist(raw: Any) -> list[dict[str, Any]]:
    rows = []
    for item in _as_dict_list(raw):
        rows.append(
            {
                "category": _clean_text(_pick(item, "category", "分类")),
                "item": _clean_text(_pick(item, "item", "检查项")),
                "status": _clean_text(_pick(item, "status", "状态")) or "pending",
                "note": _clean_text(_pick(item, "note", "说明")),
            }
        )
    return rows


def _normalize_named_rows(raw: Any, keys: tuple[str, ...]) -> list[dict[str, Any]]:
    rows = []
    for item in _as_dict_list(raw):
        rows.append({key: _clean_text(item.get(key)) for key in keys})
    return rows


def _normalize_relationship_map(raw: Any, role_candidates: list[dict[str, Any]]) -> list[dict[str, Any]]:
    names_by_code = {
        _clean_text(item.get("role_code")).upper(): _clean_text(item.get("name"))
        for item in role_candidates
        if _clean_text(item.get("role_code")) and _clean_text(item.get("name"))
    }
    rows: list[dict[str, Any]] = []
    for item in _as_dict_list(raw):
        from_code = _clean_text(_pick(item, "from_role_code", "from_code", "source_role_code", "人物A编号")).upper()
        to_code = _clean_text(_pick(item, "to_role_code", "to_code", "target_role_code", "人物B编号")).upper()
        rows.append({
            "from_role_name": _clean_text(_pick(item, "from_role_name", "from_name", "source_role_name", "人物A")) or names_by_code.get(from_code, ""),
            "from_role_code": from_code,
            "to_role_name": _clean_text(_pick(item, "to_role_name", "to_name", "target_role_name", "人物B")) or names_by_code.get(to_code, ""),
            "to_role_code": to_code,
            "relationship": _clean_text(_pick(item, "relationship", "relation", "关系")),
            "conflict": _clean_text(_pick(item, "conflict", "冲突")),
            "evidence": _clean_text(_pick(item, "evidence", "source_text", "依据")),
        })
    return rows


def _normalize_asset_type(value: str) -> str:
    normalized = value.strip().lower()
    mapping = {
        "人物": "character",
        "角色": "character",
        "character": "character",
        "scene": "scene",
        "场景": "scene",
        "prop": "prop",
        "道具": "prop",
    }
    return mapping.get(normalized, normalized if normalized in {"character", "scene", "prop"} else "prop")


_ASSET_CODE_PATTERN = re.compile(r"(?:^|-)(SC|R|P)(\d+)$", re.I)
_PROP_OBJECT_TERMS = {
    "茶几", "沙发", "靠垫", "沙发靠垫", "酒杯", "酒瓶", "威士忌", "手机", "房门",
    "领带", "西装外套", "桌", "椅", "床", "柜", "灯", "门", "窗帘",
}
_SPATIAL_NAME_SUFFIXES = ("房间", "客厅", "卧室", "餐厅", "走廊", "大厅", "酒店", "公寓", "办公室", "街道", "区域", "区")


def _normalize_asset_extract_output(output: dict[str, Any]) -> dict[str, Any]:
    """Canonicalize dedicated asset-extract output before matching or workflow use."""
    grouped_sources = (
        ("characters", "character"),
        ("scenes", "scene"),
        ("props", "prop"),
        ("assets", ""),
    )
    merged: dict[tuple[str, str], dict[str, Any]] = {}
    order: list[tuple[str, str]] = []
    for source_key, source_type in grouped_sources:
        for raw in _as_dict_list(output.get(source_key)):
            item = dict(raw)
            asset_type = _asset_extract_item_type(item, source_type)
            item["asset_type"] = asset_type
            item["type"] = asset_type
            code = _asset_candidate_code(item)
            if code:
                item["asset_code"] = code
            if asset_type == "prop":
                item = _normalize_prop_master_state(item)
            name_key = _canonical_asset_name(item.get("name"))
            code_key = _canonical_asset_code(code, asset_type)
            identity = name_key or code_key
            if not identity:
                continue
            key = (asset_type, identity)
            if key not in merged:
                merged[key] = item
                order.append(key)
            else:
                merged[key] = _merge_asset_extract_candidates(merged[key], item)

    assets = [merged[key] for key in order]
    _repair_asset_candidate_codes(assets)
    normalized = dict(output)
    normalized["characters"] = [item for item in assets if item.get("asset_type") == "character"]
    normalized["scenes"] = [item for item in assets if item.get("asset_type") == "scene"]
    normalized["props"] = [item for item in assets if item.get("asset_type") == "prop"]
    normalized["assets"] = assets
    return normalized


def _asset_extract_item_type(item: dict[str, Any], source_type: str) -> str:
    code = _asset_candidate_code(item)
    code_match = _ASSET_CODE_PATTERN.search(code)
    code_type = {"R": "character", "SC": "scene", "P": "prop"}.get(
        code_match.group(1).upper() if code_match else ""
    )
    name = _clean_text(_pick(item, "name", "role_name", "scene_name", "prop_name"))
    if code_type == "character" or item.get("role_code") or item.get("character_name"):
        return "character"
    if _looks_like_prop_item(item, name):
        return "prop"
    if code_type:
        return code_type
    raw_explicit = _clean_text(item.get("asset_type") or item.get("type"))
    explicit = _normalize_asset_type(raw_explicit) if raw_explicit else ""
    return explicit or source_type or "prop"


def _looks_like_prop_item(item: dict[str, Any], name: str) -> bool:
    if item.get("prop_code") or item.get("prop_type"):
        return True
    compact = re.sub(r"[\s\-_/]+", "", name)
    if not compact or compact.endswith(_SPATIAL_NAME_SUFFIXES):
        return False
    return compact in _PROP_OBJECT_TERMS or any(
        compact.endswith(term) for term in _PROP_OBJECT_TERMS if len(term) > 1
    )


def _asset_candidate_code(item: dict[str, Any]) -> str:
    return _clean_text(_pick(item, "asset_code", "role_code", "scene_code", "prop_code", "id", "ID")).upper()


def _canonical_asset_code(value: str, asset_type: str) -> str:
    match = _ASSET_CODE_PATTERN.search(value or "")
    if not match:
        return ""
    prefix = match.group(1).upper()
    if {"character": "R", "scene": "SC", "prop": "P"}.get(asset_type) != prefix:
        return ""
    return f"{prefix}{int(match.group(2)):03d}"


def _canonical_asset_name(value: Any) -> str:
    return re.sub(r"[\s\-_/（）()【】\[\]]+", "", _clean_text(value)).casefold()


def _normalize_prop_master_state(item: dict[str, Any]) -> dict[str, Any]:
    name = _clean_text(_pick(item, "name", "prop_name"))
    state = ""
    if re.search(r"威士忌", name) and re.search(r"含冰块|带冰块|加冰|冰块", name):
        state = "含冰块"
        name = "威士忌"
    if not state:
        return item
    metadata = dict(item.get("metadata") or {})
    states = list(dict.fromkeys([*_as_text_list(metadata.get("variant_states")), state]))
    status_change = "；".join(dict.fromkeys([
        *_as_text_list(item.get("status_change")),
        *_as_text_list(metadata.get("status_change")),
        state,
    ]))
    metadata.update({"variant_states": states, "status_change": status_change})
    return {**item, "name": name, "status_change": status_change, "metadata": metadata}


def _merge_asset_extract_candidates(left: dict[str, Any], right: dict[str, Any]) -> dict[str, Any]:
    merged = {**right, **left}
    left_meta = dict(left.get("metadata") or {})
    right_meta = dict(right.get("metadata") or {})
    merged["metadata"] = {**right_meta, **left_meta}
    for key in (
        "related_scene_codes", "scene_names", "scene_ids", "related_storyboard_codes",
        "related_script_segment_codes", "key_props", "key_prop_ids", "variant_states",
    ):
        values = list(dict.fromkeys([*_as_text_list(left.get(key)), *_as_text_list(right.get(key))]))
        if values:
            merged[key] = values
    states = list(dict.fromkeys([
        *_as_text_list(left.get("status_change")),
        *_as_text_list(right.get("status_change")),
        *_as_text_list(left_meta.get("variant_states")),
        *_as_text_list(right_meta.get("variant_states")),
    ]))
    if states:
        merged["status_change"] = "；".join(states)
        merged["metadata"] = {**merged["metadata"], "variant_states": states, "status_change": "；".join(states)}
    return merged


def _repair_asset_candidate_codes(assets: list[dict[str, Any]]) -> None:
    prefixes = {"character": "R", "scene": "SC", "prop": "P"}
    used: dict[str, set[str]] = {key: set() for key in prefixes}
    counters: dict[str, int] = {key: 0 for key in prefixes}
    for item in assets:
        asset_type = str(item.get("asset_type") or "prop")
        canonical = _canonical_asset_code(_asset_candidate_code(item), asset_type)
        if canonical and canonical not in used[asset_type]:
            item["asset_code"] = canonical
            used[asset_type].add(canonical)
            counters[asset_type] = max(counters[asset_type], int(re.search(r"\d+$", canonical).group()))
        else:
            item["asset_code"] = ""
    for item in assets:
        if item.get("asset_code"):
            continue
        asset_type = str(item.get("asset_type") or "prop")
        while True:
            counters[asset_type] += 1
            code = f"{prefixes[asset_type]}{counters[asset_type]:03d}"
            if code not in used[asset_type]:
                item["asset_code"] = code
                used[asset_type].add(code)
                break


def _dedupe_assets(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
    seen: set[tuple[str, str]] = set()
    assets = []
    for item in items:
        key = (str(item.get("type") or item.get("asset_type") or ""), str(item.get("name") or ""))
        if not key[1] or key in seen:
            continue
        seen.add(key)
        assets.append(item)
    return assets


def _normalize_keyframes(raw: Any, fallback_description: str) -> list[dict[str, Any]]:
    frames = []
    for index, item in enumerate(_as_dict_list(raw), start=1):
        description = _clean_text(_pick(item, "description", "text", "画面"))
        if description:
            frames.append({"order": _coerce_int(_pick(item, "order", "index", "序号"), index), "description": description})
    if not frames:
        frames.append({"order": 1, "description": fallback_description[:160]})
    return frames


def _normalize_mirror_shots(raw: Any) -> list[dict[str, Any]]:
    shots: list[dict[str, Any]] = []
    for index, item in enumerate(_as_dict_list(raw), start=1):
        description = _clean_text(_pick(item, "description", "text", "画面描述"))
        if description:
            duration = _pick(item, "duration_seconds", "duration", "时长")
            shots.append({
                "id": _clean_text(_pick(item, "id", "label", "code", "镜中分镜")) or chr(64 + index),
                "description": description,
                "camera": _clean_text(_pick(item, "camera", "camera_direction", "运镜", "机位")),
                "shot_size": _clean_text(_pick(item, "shot_size", "景别")),
                "duration_seconds": _coerce_float(duration) if duration not in (None, "") else None,
                "dialogue": _normalize_dialogue_text(_pick(item, "dialogue", "line", "台词")),
                "characters": _as_text_list(_pick(item, "characters", "role_codes", "角色")),
                "shot_function": _clean_text(_pick(item, "shot_function", "function", "镜头职能")),
                "movement_reason": _clean_text(_pick(item, "movement_reason", "camera_reason", "运镜动机")),
                "environmental_pressure": _clean_text(_pick(item, "environmental_pressure", "环境压力")),
                "micro_action": _clean_text(_pick(item, "micro_action", "身体微动作", "微动作")),
                "sound_or_motif": _clean_text(_pick(item, "sound_or_motif", "sound_anchor", "声音或视觉母题")),
            })
    if isinstance(raw, list):
        for index, item in enumerate(raw, start=1):
            if isinstance(item, str) and item.strip():
                shots.append({
                    "id": chr(64 + index),
                    "description": item.strip(),
                    "camera": "",
                    "shot_size": "",
                    "duration_seconds": None,
                    "dialogue": "",
                    "characters": [],
                    "shot_function": "",
                    "movement_reason": "",
                    "environmental_pressure": "",
                    "micro_action": "",
                    "sound_or_motif": "",
                })
    return shots


def _parse_json_object(content: str) -> dict[str, Any]:
    text = content.strip()
    if not text:
        return {}
    fence = re.search(r"```(?:json)?\s*(.*?)\s*```", text, flags=re.S)
    if fence:
        text = fence.group(1)
    start = text.find("{")
    end = text.rfind("}")
    if start >= 0 and end > start:
        text = text[start : end + 1]
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        try:
            parsed = repair_json(text, return_objects=True)
        except (TypeError, ValueError, json.JSONDecodeError):
            return {}
    return parsed if isinstance(parsed, dict) else {}


def _episode_num_from_item(item: dict[str, Any], fallback: int) -> int:
    if item.get("episode_num") is not None:
        return _coerce_int(item.get("episode_num"), fallback)
    episode_code = str(item.get("episode_code") or "").upper().replace("EP", "")
    return _coerce_int(episode_code, fallback)


def _pick(item: dict[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in item and item[key] not in (None, "", []):
            return item[key]
    return None


def _as_dict_list(raw: Any) -> list[dict[str, Any]]:
    if isinstance(raw, list):
        return [item for item in raw if isinstance(item, dict)]
    return []


def _resolved_production_brief(payload: dict[str, Any]) -> dict[str, Any]:
    brief = payload.get("resolved_production_brief")
    if not isinstance(brief, dict) or brief.get("schema_version") != "ResolvedProductionBrief.v1":
        raise ValueError("Agent payload requires ResolvedProductionBrief.v1")
    return dict(brief)


def _resolved_rhythm_label(resolved_brief: dict[str, Any]) -> str:
    code = _clean_text(resolved_brief.get("primary_cultural_context_code"))
    grammar = _clean_text(_dict_or_empty(resolved_brief.get("narrative_grammar_by_context")).get(code))
    return grammar or "按当前文化叙事语法控制节拍"


def _as_text_list(raw: Any) -> list[str]:
    if isinstance(raw, list):
        return [_clean_text(item) for item in raw if _clean_text(item)]
    if isinstance(raw, str) and raw.strip():
        return [raw.strip()]
    return []


def _normalize_name_list(raw: Any) -> list[str]:
    return [
        item
        for item in _normalize_display_parts(raw)
        if item and not re.fullmatch(r"(?:S|SC|R|CH|P|PO|J|F|SEG|SB)?[-_ ]?\d+[A-Z0-9-]*", item, flags=re.I)
    ]


def _normalize_id_list(raw: Any) -> list[str]:
    ids = _extract_reference_ids(raw, prefixes=("S", "SC", "R", "CH", "P", "PO", "J", "F", "SEG", "SB"))
    explicit = [
        item
        for item in _normalize_display_parts(raw, strip_ids=False)
        if re.fullmatch(r"(?:S|SC|R|CH|P|PO|J|F|SEG|SB)?[-_ ]?\d+[A-Z0-9-]*", item, flags=re.I)
    ]
    return list(dict.fromkeys([*ids, *explicit]))


def _extract_reference_ids(raw: Any, *, prefixes: tuple[str, ...]) -> list[str]:
    text = _clean_text(raw)
    if not text:
        return []
    pattern = r"\b(?:" + "|".join(re.escape(prefix) for prefix in prefixes) + r")[-_ ]?\d+[A-Z0-9-]*\b"
    return list(dict.fromkeys(match.group(0).strip() for match in re.finditer(pattern, text, flags=re.I)))


def _normalize_display_parts(raw: Any, *, strip_ids: bool = True) -> list[str]:
    if raw is None:
        return []
    if isinstance(raw, list):
        return list(dict.fromkeys(part for item in raw for part in _normalize_display_parts(item, strip_ids=strip_ids) if part))
    if isinstance(raw, dict):
        value = _pick(raw, "name", "scene_name", "character_name", "prop_name", "title", "label", "value", "zone")
        return _normalize_display_parts(value, strip_ids=strip_ids)
    text = str(raw).strip()
    if not text:
        return []
    normalized = re.sub(r"([）)])\s*[-–—]\s*(?=(?:[A-Z]{0,3}[-_ ]?\d|场景\d))", r"\1/", text)
    parts = re.split(r"\n+|[、，,；;]+|\s[-–—]\s|/+", normalized)
    cleaned = []
    for part in parts:
        value = part.strip()
        if strip_ids:
            bracket_name = re.match(r"^(?:[A-Z]{0,3}[-_ ]?\d+[A-Z0-9-]*|场景\d+)\s*[（(](.+)[）)]$", value, flags=re.I)
            if bracket_name:
                value = bracket_name.group(1).strip()
            value = re.sub(r"^(?:S|SC|R|CH|P|PO|J|F|SEG|SB)?[-_ ]?\d+[A-Z0-9-]*[：:\s-]+", "", value, flags=re.I).strip()
        if value:
            cleaned.append(value)
    return list(dict.fromkeys(cleaned))


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, (dict, list)):
        return json.dumps(value, ensure_ascii=False)
    return str(value).strip()


def _coerce_int(value: Any, fallback: int) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return fallback


def _coerce_float(value: Any) -> float | None:
    try:
        return float(value)
    except (TypeError, ValueError):
        return None

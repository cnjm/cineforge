from __future__ import annotations

import asyncio
import json
import re
from typing import Any, Protocol, TypedDict

from json_repair import repair_json

from app.agents.hermes import hermes_client
from app.agents.prompting import PromptMode
from app.agents.runtime_policy import runtime_policy_executor
from app.agents.production_context import semantic_character_prompt_context
from app.agents.skill_contracts import build_skill_runtime_metadata, get_prompt_skill_contract, get_skill_runtime_policy, validate_required_fields
from app.config import settings


class WorkflowRunner(Protocol):
    name: str
    backend: str

    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        ...


class PromptWorkflowState(TypedDict, total=False):
    payload: dict[str, Any]
    targets: list[dict[str, Any]]
    output: dict[str, Any]
    diagnostics: list[str]


class LangGraphPromptWorkflowRunner:
    def __init__(self, mode: PromptMode) -> None:
        self.mode = mode
        self.name = f"prompt_{mode}_langgraph_workflow"
        self.backend = "langgraph"
        self._graph = self._compile_graph()

    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        state = await self._graph.ainvoke({"payload": payload, "diagnostics": []})
        output = dict(state["output"])
        output["workflow"] = {
            "name": self.name,
            "backend": self.backend,
            "nodes": state.get("diagnostics", []),
            "lane": "prompt_production",
        }
        return output

    def _compile_graph(self) -> Any:
        from langgraph.graph import END, START, StateGraph

        graph = StateGraph(PromptWorkflowState)
        graph.add_node("select_prompt_targets", self._select_prompt_targets)
        graph.add_node("generate_prompt", self._generate_prompt)
        graph.add_edge(START, "select_prompt_targets")
        graph.add_edge("select_prompt_targets", "generate_prompt")
        graph.add_edge("generate_prompt", END)
        return graph.compile()

    async def _select_prompt_targets(self, state: PromptWorkflowState) -> PromptWorkflowState:
        payload = state.get("payload") or {}
        diagnostics = [*(state.get("diagnostics") or []), "select_prompt_targets"]
        return {**state, "targets": select_prompt_targets(self.mode, payload), "diagnostics": diagnostics}

    async def _generate_prompt(self, state: PromptWorkflowState) -> PromptWorkflowState:
        payload = state.get("payload") or {}
        targets = list(state.get("targets") or [])
        diagnostics = [*(state.get("diagnostics") or []), "generate_prompt"]
        skill = get_prompt_skill_contract(self.mode).skill
        if "prompt_targets" not in payload:
            output = await runtime_policy_executor.execute(
                skill,
                payload,
                lambda: _build_hermes_prompt(self.mode, payload),
                cache_payload=_prompt_cache_payload(skill, payload),
            )
            output["node_runtime"] = {
                "generate_prompt": build_skill_runtime_metadata(skill, payload)
            }
            diagnostics.append("agent_skill")
            return {"output": output, "diagnostics": diagnostics, "targets": targets}

        policy = get_skill_runtime_policy(get_prompt_skill_contract(self.mode).skill)
        batch_size = policy.batch_size or len(targets) or 1
        items: list[dict[str, Any]] = []
        base_payload = {key: value for key, value in payload.items() if key != "prompt_targets"}
        semaphore = asyncio.Semaphore(max(1, settings.agent_prompt_concurrency))

        async def generate_target(index: int, target: dict[str, Any]) -> dict[str, Any]:
            target_payload = {**base_payload, **target, "target_index": index, "target_count": len(targets)}
            async with semaphore:
                item = await runtime_policy_executor.execute(
                    skill,
                    target_payload,
                    lambda target_payload=target_payload: _build_hermes_prompt(self.mode, target_payload),
                    cache_payload=_prompt_cache_payload(skill, target_payload),
                )
            item["target"] = {
                "target_id": target.get("target_id"),
                "target_type": target.get("target_type"),
                "storyboard_code": target.get("storyboard_code"),
                "asset_code": target.get("asset_code"),
                "keyframe_code": target.get("keyframe_code"),
                "video_code": target.get("video_code"),
            }
            item["node_runtime"] = {
                "generate_prompt": build_skill_runtime_metadata(skill, target_payload)
            }
            return item

        indexed_targets = list(enumerate(targets, start=1))
        for batch in chunk_prompt_targets(indexed_targets, batch_size):
            items.extend(await asyncio.gather(*(
                generate_target(index, target)
                for index, target in batch
            )))
        output = {
            "skill": skill,
            "items": items,
            "batch": {
                "target_count": len(targets),
                "batch_size": batch_size,
                "batch_count": len(chunk_prompt_targets(targets, batch_size)),
                "concurrency": max(1, settings.agent_prompt_concurrency),
                "can_rerun_scope": list(policy.rerun_scopes),
            },
            "node_runtime": {
                "select_prompt_targets": {
                    "target_count": len(targets),
                    "input_hash": build_skill_runtime_metadata(skill, payload)["input_hash"],
                    "workflow_lane": "prompt_production",
                    "run_scope": policy.run_scope,
                    "can_rerun_scope": list(policy.rerun_scopes),
                    "batch_size": batch_size,
                }
            },
        }
        diagnostics.append("agent_skill_batch")
        return {"output": output, "diagnostics": diagnostics}


def create_prompt_workflow_runner(mode: PromptMode) -> WorkflowRunner:
    return LangGraphPromptWorkflowRunner(mode)


def _prompt_cache_payload(skill: str, payload: dict[str, Any]) -> dict[str, Any]:
    context = payload.get("production_context")
    if skill != "text-to-image-prompt" or not isinstance(context, dict) or context.get("schema_version") != "CharacterProductionContext.v1":
        return payload
    return {
        "task_type": payload.get("task_type"),
        "output_spec": payload.get("output_spec"),
        "production_context": semantic_character_prompt_context(context),
        "resolved_production_brief": payload.get("resolved_production_brief") or {},
        "production_references": payload.get("production_references") or [],
        "platform": payload.get("platform"),
    }


def select_prompt_targets(mode: PromptMode, payload: dict[str, Any]) -> list[dict[str, Any]]:
    explicit = payload.get("prompt_targets")
    if isinstance(explicit, list):
        return [dict(item) for item in explicit if isinstance(item, dict)]
    breakdown = payload.get("breakdown_view") if isinstance(payload.get("breakdown_view"), dict) else {}
    if not breakdown:
        return [dict(payload)]
    if mode == "t2i":
        return _select_t2i_targets(breakdown)
    return _select_i2v_targets(breakdown)


def chunk_prompt_targets(targets: list[Any], batch_size: int) -> list[list[Any]]:
    size = max(1, batch_size)
    return [targets[index : index + size] for index in range(0, len(targets), size)]


def _select_t2i_targets(breakdown: dict[str, Any]) -> list[dict[str, Any]]:
    targets: list[dict[str, Any]] = []
    agent_reminders = list(breakdown.get("manual_review_items") or [])
    # Keyframes are embedded in the storyboard plan and auto-derived from it — there is
    # no separate keyframe_plan produced or persisted, so targets come from storyboards.
    for item in breakdown.get("storyboards") or []:
        if not isinstance(item, dict):
            continue
        targets.append(
            {
                "target_id": item.get("keyframe_code") or item.get("storyboard_code"),
                "target_type": "storyboard_keyframe",
                "keyframe_code": item.get("keyframe_code"),
                "storyboard_code": item.get("storyboard_code"),
                "storyboard": item,
                "agent_reminders": agent_reminders,
            }
        )
    return targets


def _select_i2v_targets(breakdown: dict[str, Any]) -> list[dict[str, Any]]:
    targets: list[dict[str, Any]] = []
    agent_reminders = list(breakdown.get("manual_review_items") or [])
    # Video clips are derived from the storyboard plan — no separate video_plan is
    # produced or persisted, so targets come directly from storyboards.
    for item in breakdown.get("storyboards") or []:
        if not isinstance(item, dict):
            continue
        targets.append(
            {
                "target_id": item.get("video_code") or item.get("storyboard_code"),
                "target_type": "storyboard_video",
                "video_code": item.get("video_code"),
                "storyboard_code": item.get("storyboard_code"),
                "storyboard": item,
                "agent_reminders": agent_reminders,
            }
        )
    return targets


async def _build_hermes_prompt(
    mode: PromptMode,
    payload: dict[str, Any],
) -> dict[str, Any]:
    contract = get_prompt_skill_contract(mode)
    system = (
        f"你是 CineForge AI 漫剧生产平台的 {contract.skill} Agent。"
        "请根据剧本、分镜、资产约束和人工备注生成可执行提示词。"
        "提示词语言标准：prompt、negative_prompt、动作叙事和约束说明使用中文；quality_tags、style_keywords、style_tags、quality_preset 等画质标签和风格词使用英文短语。"
        "当前前端只支持图片模型 Seedream/libimage，视频模型 Seedance/可灵；不要输出其他模型名称或平台建议。"
        "后端只做 JSON 解析和 schema 校验，不会用本地规则补 prompt。"
        "只返回 JSON，不要使用 Markdown。"
    )
    user = json.dumps(
        {
            "任务": contract.task,
            "skill": contract.skill,
            "要求": list(contract.requirements),
            "输入": _compact_prompt_payload(payload),
            "必须返回字段": contract.output_schema,
            "必填字段": list(contract.required_fields),
        },
        ensure_ascii=False,
    )
    response = await hermes_client.chat_json(
        system=system,
        user=user,
        json_schema=contract.json_schema,
        schema_name=contract.skill,
    )
    parsed = _parse_json_object(response.get("content") or "")
    if not parsed:
        raise ValueError(f"{contract.skill} Agent 未返回可解析 JSON")
    _apply_prompt_defaults(mode, parsed, payload)
    if contract.skill in {"text-to-image-prompt", "image-to-video-prompt"}:
        _coerce_prompt_output_types(parsed)
    try:
        validate_required_fields(parsed, contract)
    except ValueError as validation_error:
        if contract.skill not in {"text-to-image-prompt", "image-to-video-prompt"}:
            raise
        repaired_response = await _repair_prompt_output(contract, parsed, str(validation_error))
        repaired = _parse_json_object(repaired_response.get("content") or "")
        if not repaired and isinstance(repaired_response.get("artifact"), dict):
            repaired = _parse_json_object(str(repaired_response["artifact"].get("content") or ""))
        if not repaired:
            raise ValueError(f"{contract.skill} Agent JSON 结构纠错失败") from validation_error
        _coerce_prompt_output_types(repaired)
        _apply_prompt_defaults(mode, repaired, payload)
        validate_required_fields(repaired, contract)
        parsed = repaired
        response = repaired_response
    output = dict(parsed)
    output["skill"] = contract.skill
    output["llm_provider"] = "hermes-agent"
    output["llm_model"] = hermes_client.model
    output["source"] = {**dict(output.get("source") or {}), "provider": "hermes-agent"}
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
    runtime = build_skill_runtime_metadata(contract.skill, payload)
    output.update({
        "skill_version": runtime["skill_version"],
        "contract_version": runtime["contract_version"],
        "contract_hash": runtime["contract_hash"],
        "input_hash": runtime["input_hash"],
    })
    output["source"].update({
        "skill": contract.skill,
        "skill_version": runtime["skill_version"],
        "contract_version": runtime["contract_version"],
        "contract_hash": runtime["contract_hash"],
        "input_hash": runtime["input_hash"],
    })
    return output


def _apply_prompt_defaults(mode: PromptMode, parsed: dict[str, Any], payload: dict[str, Any]) -> None:
    if mode == "t2i":
        resolved = payload.get("resolved_production_brief") if isinstance(payload.get("resolved_production_brief"), dict) else {}
        if payload.get("task_type") == "costume":
            parsed["aspect_ratio"] = str(resolved.get("asset_master_aspect_ratio") or "16:9")
        else:
            parsed["aspect_ratio"] = str(resolved.get("delivery_aspect_ratio") or "9:16")


def _coerce_prompt_output_types(parsed: dict[str, Any]) -> None:
    """Keep optional prompt metadata usable when a model emits a scalar instead of an object."""
    for field in ("material_layers", "character_apose", "fusion_rules"):
        value = parsed.get(field)
        if value is None:
            continue
        if isinstance(value, dict):
            continue
        parsed[field] = {"description": value} if not isinstance(value, list) else {"items": value}
    for field in ("style_keywords", "quality_tags", "reference_images", "manual_review_items"):
        value = parsed.get(field)
        if value is None:
            parsed[field] = []
        elif not isinstance(value, list):
            parsed[field] = [value]
    view_prompts = parsed.get("view_prompts")
    if isinstance(view_prompts, dict):
        parsed["view_prompts"] = [
            {"code": str(code), **item}
            for code, item in view_prompts.items()
            if isinstance(item, dict)
        ]
    elif view_prompts is not None and not isinstance(view_prompts, list):
        parsed["view_prompts"] = []


async def _repair_prompt_output(
    contract: Any,
    output: dict[str, Any],
    validation_error: str,
) -> dict[str, Any]:
    response = await hermes_client.chat_json(
        system="你是 JSON 结构纠错器。只修复字段类型、字段缺失和 JSON 结构，保留已有提示词语义；只返回完整 JSON 对象。",
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
    return response


def _parse_json_object(content: str) -> dict[str, Any]:
    text = content.strip()
    if not text:
        return {}
    fence = re.search(r"```(?:json)?\s*(\{.*?\})\s*```", text, flags=re.S)
    if fence:
        text = fence.group(1)
    else:
        start = text.find("{")
        end = text.rfind("}")
        if start >= 0 and end > start:
            text = text[start : end + 1]
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError:
        try:
            parsed = repair_json(text, return_objects=True)
        except Exception:
            return {}
    return parsed if isinstance(parsed, dict) else {}


def _compact_prompt_payload(payload: dict[str, Any]) -> dict[str, Any]:
    storyboard = payload.get("storyboard") if isinstance(payload.get("storyboard"), dict) else {}
    assets = payload.get("assets") if isinstance(payload.get("assets"), list) else []
    reminders = payload.get("agent_reminders") if isinstance(payload.get("agent_reminders"), list) else []
    compact_assets = []
    for asset in assets[:4]:
        if not isinstance(asset, dict):
            continue
        compact_assets.append(
            {
                "name": asset.get("name"),
                "type": asset.get("asset_type") or asset.get("type"),
                "description": asset.get("description"),
                "tags": asset.get("tags"),
            }
        )
    return {
        "task_id": payload.get("task_id"),
        "storyboard_id": payload.get("storyboard_id"),
        "task_type": payload.get("task_type"),
        "output_spec": payload.get("output_spec"),
        "description": payload.get("description") or storyboard.get("description"),
        "storyboard": {
            "title": storyboard.get("title"),
            "description": storyboard.get("description"),
            "characters": storyboard.get("characters"),
            "camera": storyboard.get("camera"),
            "duration_seconds": storyboard.get("duration_seconds"),
        },
        "assets": compact_assets,
        "asset": payload.get("asset") if isinstance(payload.get("asset"), dict) else {},
        "reading_to_asset_context": payload.get("reading_to_asset_context") if isinstance(payload.get("reading_to_asset_context"), dict) else {},
        "production_context": payload.get("production_context") if isinstance(payload.get("production_context"), dict) else {},
        "production_references": [
            dict(item) for item in payload.get("production_references", [])[:30]
            if isinstance(item, dict)
        ] if isinstance(payload.get("production_references"), list) else [],
        "resolved_production_brief": payload.get("resolved_production_brief") if isinstance(payload.get("resolved_production_brief"), dict) else {},
        "image_ref": payload.get("image_ref"),
        "duration": payload.get("duration"),
        "agent_reminders": [
            {
                "item": item.get("item"),
                "detail": item.get("detail"),
                "source": item.get("source"),
                "code": item.get("code"),
            }
            if isinstance(item, dict) else {"item": str(item)}
            for item in reminders[:30]
        ],
        "style_guide": payload.get("style_guide") if isinstance(payload.get("style_guide"), dict) else {},
    }

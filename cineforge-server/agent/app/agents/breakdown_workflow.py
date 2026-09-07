from __future__ import annotations

import asyncio
import uuid
import re
from difflib import SequenceMatcher
from datetime import UTC, datetime
from typing import Any, TypedDict

from app.agents.breakdown_contract import (
    attach_breakdown_contract,
    blocking_manual_review_items,
    normalize_manual_review_items,
)
from app.agents.checkpointing import open_workflow_checkpointer
from app.agents.production_context import (
    build_character_production_contexts,
    build_prop_production_context,
    build_reading_to_asset_context,
    build_scene_production_context,
    prompt_asset_from_context,
    scope_character_production_context,
)
from app.agents.runners import (
    AgentExecutionError,
    AssetExtractRunner,
    CharacterDesignPromptRunner,
    ComplianceReviewRunner,
    PropDesignPromptRunner,
    RelationCheckRunner,
    SceneDesignPromptRunner,
    ScriptReadingRunner,
    _build_hermes_script_breakdown,
    _build_hermes_script_segmentation,
)
from app.agents.runtime_policy import runtime_policy_executor
from app.agents.skill_contracts import build_skill_runtime_metadata, get_skill_runtime_policy, stable_payload_hash
from app.config import settings
from app.schemas.asset_normalization import AssetNormalizationResultV1
from app.services.asset_normalization import normalize_asset_inventory_v1


class ScriptBreakdownWorkflowState(TypedDict, total=False):
    workflow_run_id: str
    project_id: str | None
    project_prefix: str
    project_title: str | None
    genre: str | None
    production_brief: dict[str, Any]
    episode_production_brief: dict[str, Any]
    resolved_production_brief: dict[str, Any]
    episode_num: int
    script_text: str
    payload: dict[str, Any]

    reading_output: dict[str, Any]
    costume_designs: list[dict[str, Any]]  # 新增：定装方案列表
    costume_processing_summary: list[dict[str, Any]]
    asset_prompt_designs: list[dict[str, Any]]
    asset_prompt_processing_summary: list[dict[str, Any]]
    breakdown_output: dict[str, Any]
    agent_raw_asset_output: dict[str, Any]
    normalized_asset_inventory: dict[str, Any]
    relation_report: dict[str, Any]
    review_output: dict[str, Any]
    normalized_view: dict[str, Any]
    validation_report: dict[str, Any]
    agent_run_ids: dict[str, str]
    node_status: dict[str, str]
    node_runtime: dict[str, dict[str, Any]]
    errors: list[dict[str, Any]]
    warnings: list[str]
    final_status: str
    output: dict[str, Any]
    feedback_candidate: dict[str, Any]


async def run_script_breakdown_workflow(payload: dict[str, Any]) -> dict[str, Any]:
    workflow_run_id = str(payload.get("workflow_run_id") or uuid.uuid4())
    initial_state: ScriptBreakdownWorkflowState = {
        "workflow_run_id": workflow_run_id,
        "project_id": str(payload.get("project_id")) if payload.get("project_id") else None,
        "project_prefix": str(payload.get("project_prefix") or "RF").strip().upper()[:5] or "RF",
        "project_title": str(payload.get("project_title") or "") or None,
        "genre": str(payload.get("genre") or "") or None,
        "production_brief": dict(payload.get("production_brief") or {}),
        "episode_production_brief": dict(payload.get("episode_production_brief") or {}),
        "resolved_production_brief": dict(payload.get("resolved_production_brief") or {}),
        "episode_num": _safe_int(payload.get("episode_num"), 1),
        "script_text": str(payload.get("script_text") or ""),
        "payload": dict(payload),
        "agent_run_ids": {},
        "node_status": {},
        "node_runtime": {},
        "errors": [],
        "warnings": [],
    }
    config = {
        "configurable": {
            "thread_id": workflow_run_id,
        }
    }
    async with open_workflow_checkpointer() as checkpointer:
        graph = build_script_breakdown_workflow_graph(checkpointer=checkpointer)
        if checkpointer is None:
            state = await graph.ainvoke(initial_state)
        else:
            snapshot = await graph.aget_state(config)
            checkpoint_state = dict(snapshot.values or {})
            if checkpoint_state and snapshot.next:
                state = await graph.ainvoke(None, config=config)
            elif checkpoint_state.get("output"):
                state = checkpoint_state
            else:
                state = await graph.ainvoke(initial_state, config=config)
    return dict(state["output"])


def build_script_breakdown_workflow_graph(*, checkpointer: Any | None = None) -> Any:
    from langgraph.graph import END, START, StateGraph

    graph = StateGraph(ScriptBreakdownWorkflowState)
    graph.add_node("load_project_context", load_project_context)
    graph.add_node("bootstrap_single_step", bootstrap_single_step)
    graph.add_node("run_script_reading", run_script_reading)
    graph.add_node("run_asset_extract", run_asset_extract)
    graph.add_node("run_asset_costume_design", run_asset_costume_design)  # 资产预定稿节点
    graph.add_node("run_script_segmentation", run_script_segmentation)
    graph.add_node("run_script_breakdown", run_script_breakdown)
    graph.add_node("validate_breakdown_contract", validate_breakdown_contract)
    graph.add_node("run_relation_check", run_relation_check)
    graph.add_node("run_content_review", run_content_review)
    graph.add_node("normalize_breakdown_view", normalize_breakdown_view)
    graph.add_node("route_workflow_status", route_workflow_status)
    graph.add_node("save_failed_result", save_failed_result)
    graph.add_node("save_needs_review_result", save_needs_review_result)
    graph.add_node("save_succeeded_result", save_succeeded_result)

    graph.add_edge(START, "load_project_context")
    graph.add_conditional_edges(
        "load_project_context",
        route_after_context_entry,
        {
            "continue": "run_script_reading",
            "single_step": "bootstrap_single_step",
            "storyboard_asset_breakdown": "run_script_breakdown",
            "asset_confirmation_required": "save_needs_review_result",
            "failed": "save_failed_result",
        },
    )
    graph.add_conditional_edges(
        "bootstrap_single_step",
        route_single_step_target,
        {
            "run_script_reading": "run_script_reading",
            "run_asset_extract": "run_asset_extract",
            "run_asset_costume_design": "run_asset_costume_design",
            "run_script_segmentation": "run_script_segmentation",
            "run_script_breakdown": "run_script_breakdown",
            "run_relation_check": "run_relation_check",
            "run_content_review": "run_content_review",
            "failed": "save_failed_result",
        },
    )
    graph.add_conditional_edges(
        "run_script_reading",
        route_after_script_reading_entry,
        {
            "continue": "run_asset_extract",
            "single_step_complete": "route_workflow_status",
            "needs_review": "save_needs_review_result",
            "failed": "save_failed_result",
        },
    )
    graph.add_conditional_edges(
        "run_asset_extract",
        route_after_asset_extract_entry,
        {
            "continue": "run_asset_costume_design",
            "needs_review": "save_needs_review_result",
            "failed": "save_failed_result",
        },
    )
    graph.add_conditional_edges(
        "run_asset_costume_design",
        route_after_costume_design_entry,
        {
            "continue": "run_script_segmentation",
            "single_step_complete": "route_workflow_status",
            "failed": "save_failed_result",
        },
    )
    graph.add_conditional_edges(
        "run_script_segmentation",
        route_after_segmentation_entry,
        {
            "segmentation_only": "save_succeeded_result",
            "single_step_complete": "route_workflow_status",
            "continue": "run_script_breakdown",
            "failed": "save_failed_result",
        },
    )
    graph.add_conditional_edges(
        "run_script_breakdown",
        route_after_breakdown,
        {
            "continue": "validate_breakdown_contract",
            "failed": "save_failed_result",
        },
    )
    graph.add_conditional_edges(
        "validate_breakdown_contract",
        route_after_contract_validation_entry,
        {
            "continue": "run_relation_check",
            "single_step_complete": "normalize_breakdown_view",
            "needs_review": "save_needs_review_result",
            "failed": "save_failed_result",
        },
    )
    graph.add_conditional_edges(
        "run_relation_check",
        route_after_relation_check_entry,
        {
            "continue": "run_content_review",
            "single_step_complete": "normalize_breakdown_view",
            "needs_review": "save_needs_review_result",
            "failed": "save_failed_result",
        },
    )
    graph.add_conditional_edges(
        "run_content_review",
        route_after_content_review,
        {"continue": "normalize_breakdown_view", "failed": "save_failed_result"},
    )
    graph.add_edge("normalize_breakdown_view", "route_workflow_status")
    graph.add_conditional_edges(
        "route_workflow_status",
        route_to_save_node,
        {
            "failed": "save_failed_result",
            "needs_review": "save_needs_review_result",
            "succeeded": "save_succeeded_result",
        },
    )
    graph.add_edge("save_failed_result", END)
    graph.add_edge("save_needs_review_result", END)
    graph.add_edge("save_succeeded_result", END)
    return graph.compile(checkpointer=checkpointer) if checkpointer is not None else graph.compile()


# 正式分步执行与完整流程共用同一张生产图；旧编码仅保留 API 兼容。
SINGLE_STEP_NODES = {
    "script_reading": "run_script_reading",
    "asset_extract": "run_asset_extract",
    "asset_prompt_generation": "run_asset_costume_design",
    "storyboard_breakdown": "run_script_breakdown",
    "asset_costume_design": "run_asset_costume_design",
    "script_segmentation": "run_script_segmentation",
    "script_breakdown": "run_script_breakdown",
    "relation_check": "run_relation_check",
    "content_review": "run_content_review",
}


def build_single_step_workflow_graph(single_step: str) -> Any:
    """兼容旧调用方；单步与完整执行现在共用同一张生产图。"""
    del single_step
    return build_script_breakdown_workflow_graph()


async def bootstrap_single_step(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    """为单步执行注入前置数据（由 API 层预加载到 payload）。"""
    if _has_blocking_errors(state):
        return _skipped(state, "bootstrap_single_step")
    await _persist_node_running(state, "bootstrap_single_step", "正在准备单步执行所需数据")
    payload = dict(state.get("payload") or {})
    single_step = str(payload.get("single_step") or "")
    updates: dict[str, Any] = {}
    warnings = list(state.get("warnings") or [])

    if single_step == "asset_extract":
        reading_report = _reviewed_reading_report(payload)
        reading_output = {
            "reading_report": reading_report,
            "reading_review_state": dict(payload.get("reading_review_state") or {}),
        } if reading_report else {}
        updates["reading_output"] = reading_output
    elif single_step in {"asset_prompt_generation", "asset_costume_design"}:
        reading_report = _reviewed_reading_report(payload)
        reading_output = {
            "reading_report": reading_report,
            "reading_review_state": dict(payload.get("reading_review_state") or {}),
        } if reading_report else {}
        if not reading_output.get("reading_report"):
            warnings.append("未找到人工确认的围读结果，请先完成围读确认。")
        confirmed_assets = _reviewed_normalized_asset_inventory(payload)
        if not confirmed_assets:
            warnings.append("未找到人工确认的资产清单，请先完成资产拆解确认。")
        updates["reading_output"] = reading_output
        updates["normalized_asset_inventory"] = confirmed_assets
    elif single_step == "script_segmentation":
        reading_report = _reviewed_reading_report(payload)
        if not reading_report:
            warnings.append("未找到人工确认后的围读报告，脚本段拆分已被阻止。")
            next_state = {
                **state,
                "warnings": warnings,
                "node_status": _node_status(state, "bootstrap_single_step", "failed"),
                "errors": [
                    *(state.get("errors") or []),
                    {"node": "bootstrap_single_step", "message": "请先确认 reading_report，再运行脚本段拆分。"},
                ],
            }
            await _persist_node_state(next_state, "bootstrap_single_step", "缺少人工确认围读报告")
            return next_state
        reading_output = {
            "reading_report": reading_report,
            "reading_review_state": dict(payload.get("reading_review_state") or {}),
        }
        updates["reading_output"] = reading_output
        updates["payload"] = {
            **payload,
            "reading_output": reading_output,
            "reading_report": reading_report,
        }
    elif single_step in {"content_review", "relation_check"}:
        normalized_view = dict(payload.get("normalized_view") or {})
        if not normalized_view:
            target = "关系校验" if single_step == "relation_check" else "内容审查"
            warnings.append(f"未找到已有的分镜拆解结果，请先运行分镜拆解再运行{target}。")
        updates["normalized_view"] = normalized_view
        updates["normalized_asset_inventory"] = _normalized_asset_inventory(payload)

    next_state = {
        **state,
        **updates,
        "warnings": warnings,
        "node_status": _node_status(state, "bootstrap_single_step", "succeeded"),
    }
    await _persist_node_state(next_state, "bootstrap_single_step", "单步执行数据准备完成")
    return next_state


async def load_project_context(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    await _persist_node_running(state, "load_project_context", "正在读取项目剧本文本")
    script_text = str(state.get("script_text") or "").strip()
    node_status = _node_status(state, "load_project_context", "succeeded" if script_text else "failed")
    if not script_text:
        next_state = {
            **state,
            "node_status": node_status,
            "errors": [
                *(state.get("errors") or []),
                {"node": "load_project_context", "message": "剧本文本为空，不能运行拆解工作流。"},
            ],
        }
        await _persist_node_state(next_state, "load_project_context", "剧本文本为空")
        return next_state
    payload = {
        **dict(state.get("payload") or {}),
        "script_text": script_text,
        "episode_num": state.get("episode_num") or 1,
        "project_prefix": state.get("project_prefix") or "RF",
        "project_title": state.get("project_title"),
        "genre": state.get("genre"),
        "production_brief": dict(state.get("production_brief") or {}),
        "episode_production_brief": dict(state.get("episode_production_brief") or {}),
        "resolved_production_brief": dict(state.get("resolved_production_brief") or {}),
        "workflow": "script_breakdown_workflow",
    }
    warnings = list(state.get("warnings") or [])
    confirmed_assets = _reviewed_normalized_asset_inventory(payload)
    if payload.get("workflow_phase") == "storyboard_asset_breakdown" and not confirmed_assets:
        warnings.append("缺少已确认的 normalized_asset_inventory，分镜拆解已在资产人工确认检查点停止。")
    next_state = {
        **state,
        "script_text": script_text,
        "payload": payload,
        "normalized_asset_inventory": confirmed_assets,
        "warnings": warnings,
        "node_status": node_status,
        "node_runtime": _node_runtime_metadata(state, "load_project_context", payload=payload),
    }
    await _persist_node_state(next_state, "load_project_context", "项目上下文读取完成")
    return next_state


async def run_script_reading(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    if _has_blocking_errors(state):
        return _skipped(state, "run_script_reading")
    try:
        payload = dict(state.get("payload") or {})
        confirmed_report = _reviewed_reading_report(payload)
        if confirmed_report:
            output = {
                "reading_report": confirmed_report,
                "reading_review_state": dict(payload.get("reading_review_state") or {}),
                "source": "human_confirmed_reading",
            }
            status = "reused_confirmed"
            message = "已读取人工确认围读报告"
        else:
            await _persist_node_running(state, "run_script_reading", "正在围读剧本并抽取基础信息")
            output = await ScriptReadingRunner().run(payload)
            status = "succeeded"
            message = "围读报告已生成，等待人工确认"
        # reading_revision_context 不在此写入：_save_workflow_result 会用
        # _reading_revision_context(payload) 重算并写入最终 output 与 reading_output
        # (1249/1253)，中间无下游节点读取该子键，此处写入纯属被覆盖的冗余。
        next_state = {
            **state,
            "reading_output": output,
            "node_status": _node_status(state, "run_script_reading", status),
            "node_runtime": _node_runtime_metadata(state, "run_script_reading", skill="script-reading", payload=payload),
        }
        await _persist_node_state(next_state, "run_script_reading", message)
        return next_state
    except Exception as exc:
        message = _exception_message(exc)
        debug_output = _exception_output(exc)
        next_state = {
            **state,
            "reading_output": debug_output,
            "node_status": _node_status(state, "run_script_reading", "failed_non_blocking"),
            "warnings": [*(state.get("warnings") or []), f"剧本阅读节点失败：{message}"],
        }
        await _persist_node_state(next_state, "run_script_reading", f"剧本围读失败，已继续后续流程：{message}")
        return next_state


async def run_asset_extract(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    if _has_blocking_errors(state):
        return _skipped(state, "run_asset_extract")
    payload = dict(state.get("payload") or {})
    confirmed_assets = _reviewed_normalized_asset_inventory(payload)
    draft_assets = _normalized_asset_inventory(payload)
    force_asset_extract = bool(payload.get("force_asset_extract"))
    reading_output = dict(state.get("reading_output") or {})
    reading_report = dict(reading_output.get("reading_report") or {})
    try:
        if confirmed_assets and not force_asset_extract:
            output = confirmed_assets
            status = "reused_confirmed"
            message = "已读取人工确认的正式资产清单"
            execution_source = "confirmed_input"
        elif (
            payload.get("single_step") == "asset_costume_design"
            and any(_dict_list(draft_assets.get(key)) for key in ("assets", "characters", "scenes", "props"))
            and not force_asset_extract
        ):
            output = draft_assets
            status = "reused_draft"
            message = "已读取当前人工资产草稿并补齐专用 Prompt"
            execution_source = "human_draft_input"
        else:
            await _persist_node_running(state, "run_asset_extract", "正在生成正式资产清单")
            runner_payload = {
                "script_text": payload.get("script_text"),
                "episode_num": payload.get("episode_num"),
                "project_prefix": payload.get("project_prefix"),
                "project_title": payload.get("project_title"),
                "genre": payload.get("genre"),
                "production_brief": payload.get("production_brief") or {},
                "resolved_production_brief": payload.get("resolved_production_brief") or {},
                "episode_id": payload.get("episode_id"),
                "episode_code": payload.get("episode_code"),
                "script_id": payload.get("script_id"),
                "script_version_id": payload.get("script_version_id"),
                "existing_asset_master_snapshot": payload.get("existing_asset_master_snapshot") or [],
                "force_rerun": force_asset_extract,
                "reading_report": _compact_reading_for_asset_extract(reading_report),
                "project_context": _project_context(state),
            }
            output = await AssetExtractRunner().run(runner_payload)
            status = "succeeded"
            message = "资产清单候选已生成，等待人工确认"
            execution_source = "agent"

        agent_raw_output = dict(output.get("agent_raw_output") or output)
        if execution_source == "agent":
            agent_raw_output = _inherit_reading_review_suggestions(agent_raw_output, reading_report)
        source_kind = {
            "agent": "agent",
            "confirmed_input": "confirmed_inventory",
            "human_draft_input": "human",
        }[execution_source]
        normalized_inventory = normalize_asset_inventory_v1(
            agent_raw_output,
            {
                "project_prefix": state.get("project_prefix") or "RF",
                "source_kind": source_kind,
                "agent_run_id": output.get("agent_run_id") or (state.get("agent_run_ids") or {}).get("run_asset_extract"),
                "skill_name": "asset-extract",
            },
        )
        normalized_payload = normalized_inventory.model_dump(mode="python")
        asset_view = {
            "normalization_version": normalized_payload["normalization_version"],
            "assets": list(normalized_payload["assets"]),
        }
        extracted_reading_output = {
            **dict(state.get("reading_output") or {}),
            "asset_matching": dict(output.get("asset_matching") or {}),
            "asset_extract_source": execution_source,
        }
        # NOTE: agent_raw_asset_output / normalized_asset_inventory / reading_output
        # are written to top-level state below (the keys every downstream node and
        # _save_workflow_result actually read). They are intentionally NOT copied back
        # into state.payload — no reader consumes payload.<those keys> (routers gate on
        # payload.asset_review_state, which is never "confirmed" right after extraction,
        # so the inventory copy was pure write-only redundancy). payload is carried
        # forward unchanged via {**state}.
        runtime_payload = runner_payload if execution_source == "agent" else payload
        runtime = _node_runtime_metadata(
            state,
            "run_asset_extract",
            skill="asset-extract",
            payload=runtime_payload,
        )
        runtime["run_asset_extract"]["execution_source"] = execution_source
        next_state = {
            **state,
            "reading_output": extracted_reading_output,
            "agent_raw_asset_output": agent_raw_output,
            "normalized_asset_inventory": normalized_payload,
            "breakdown_output": {
                "normalization_version": normalized_inventory.normalization_version,
                "assets": normalized_payload["assets"],
                "agent_raw_asset_output": agent_raw_output,
                "workflow_phase": "asset_extract",
                "asset_confirmation_required": not bool(confirmed_assets),
            },
            "normalized_view": asset_view,
            "node_status": _node_status(state, "run_asset_extract", status),
            "node_runtime": runtime,
        }
        await _persist_node_state(next_state, "run_asset_extract", message)
        return next_state
    except Exception as exc:
        message = _exception_message(exc)
        error = {"node": "run_asset_extract", "message": message}
        debug_output = _exception_output(exc)
        if debug_output:
            error["debug"] = debug_output
        next_state = {
            **state,
            "agent_raw_asset_output": debug_output,
            "node_status": _node_status(state, "run_asset_extract", "failed"),
            "errors": [*(state.get("errors") or []), error],
        }
        await _persist_node_state(next_state, "run_asset_extract", f"资产清单生成失败：{message}", error=error)
        return next_state


async def run_asset_costume_design(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    """Generate schema-validated character, scene, and prop asset prompt drafts."""
    if _has_blocking_errors(state):
        return _skipped(state, "run_asset_costume_design")

    await _persist_node_running(state, "run_asset_costume_design", "正在生成资产预定稿")

    try:
        reading_output = dict(state.get("reading_output") or {})
        resolved_brief = dict(state.get("resolved_production_brief") or {})
        if resolved_brief.get("schema_version") != "ResolvedProductionBrief.v1":
            raise ValueError("资产预定稿缺少 ResolvedProductionBrief.v1")
        grouped = _categorized_normalized_inventory(
            _normalized_asset_inventory(dict(state.get("normalized_asset_inventory") or {}), direct=True)
        )
        all_characters = [item for item in grouped["characters"] if str(item.get("name") or "").strip()]
        deferred_characters = [item for item in all_characters if not _character_requires_visual_master(item)]
        production_characters = [item for item in all_characters if _character_requires_visual_master(item)]
        production_scenes = [item for item in grouped["scenes"] if str(item.get("name") or "").strip()]
        production_props = [item for item in grouped["props"] if str(item.get("name") or "").strip()]
        reading_context = build_reading_to_asset_context(reading_output, resolved_brief)
        warnings = list(state.get("warnings") or [])
        if not any((production_characters, production_scenes, production_props)):
            warnings.append("资产清单为空，本次没有可生成的资产预定稿。")

        existing_assets = [
            *_dict_list(dict(state.get("payload") or {}).get("existing_asset_master_snapshot")),
            *_dict_list(reading_output.get("existing_characters")),
            *_dict_list(reading_output.get("existing_props")),
            *_dict_list(reading_output.get("existing_scenes")),
        ]
        existing_by_code = {
            alias: item
            for item in existing_assets
            for alias in _asset_code_aliases(item.get("asset_code"))
        }
        existing_by_name = {
            (_asset_type_key(item), _asset_name_key(item.get("name"))): item
            for item in existing_assets
            if str(item.get("name") or "").strip()
        }

        designs: list[dict[str, Any]] = []
        context_jobs: list[tuple[dict[str, Any], dict[str, Any], dict[str, Any] | None, str]] = []
        reused_assets: list[dict[str, Any]] = []
        existing_props = [*production_props, *_dict_list(reading_output.get("existing_props"))]

        def matched_asset(asset: dict[str, Any], asset_type: str) -> dict[str, Any] | None:
            asset_code = str(asset.get("asset_code") or "").strip()
            return next(
                (existing_by_code[alias] for alias in _asset_code_aliases(asset_code) if alias in existing_by_code),
                None,
            ) or existing_by_name.get((asset_type, _asset_name_key(asset.get("name"))))

        def append_reused(asset: dict[str, Any], existing: dict[str, Any], asset_type: str) -> None:
            task_contexts = _dict_list(existing.get("asset_task_contexts"))
            reused_assets.append({
                "processing_status": "reused",
                "asset_type": asset_type,
                "candidate_asset_code": asset.get("asset_code"),
                "candidate_name": asset.get("name"),
                "matched_asset_id": existing.get("asset_id") or existing.get("id"),
                "matched_asset_code": existing.get("asset_code"),
                "matched_name": existing.get("name"),
                "reuse_status": "reused_existing",
                "asset_task_count": len(task_contexts),
                "detail": f"复用 {existing.get('asset_code') or asset.get('asset_code')} 已有资产任务（{len(task_contexts)} 项），当前上下文没有新增变体。",
            })

        for char in production_characters:
            existing = matched_asset(char, "character")
            contexts = _character_prompt_contexts(char, existing, reading_context, existing_props)
            if existing and not contexts and _asset_prompt_can_reuse(existing):
                append_reused(char, existing, "character")
            for context in contexts:
                context_jobs.append((char, context, existing, "character-design-prompt"))

        for scene in production_scenes:
            existing = matched_asset(scene, "scene")
            if (
                existing
                and _asset_prompt_can_reuse(existing)
            ):
                append_reused(scene, existing, "scene")
                continue
            context_jobs.append((scene, build_scene_production_context(scene, reading_context, resolved_brief), existing, "scene-design-prompt"))

        for prop in production_props:
            existing = matched_asset(prop, "prop")
            if (
                existing
                and _asset_prompt_can_reuse(existing)
            ):
                append_reused(prop, existing, "prop")
                continue
            context_jobs.append((prop, build_prop_production_context(prop, reading_context, resolved_brief), existing, "prop-design-prompt"))

        runners = {
            "character-design-prompt": CharacterDesignPromptRunner(),
            "scene-design-prompt": SceneDesignPromptRunner(),
            "prop-design-prompt": PropDesignPromptRunner(),
        }
        batch_size = 5
        semaphore = asyncio.Semaphore(max(1, settings.asset_prompt_concurrency))

        async def generate_context(
            asset: dict[str, Any],
            context: dict[str, Any],
            existing: dict[str, Any] | None,
            skill: str,
        ) -> dict[str, Any]:
            asset_type = skill.split("-", 1)[0]
            asset_code = str(asset.get("asset_code") or asset.get("role_code") or "").strip()
            context_key = str(context.get("context_key") or asset_code)
            is_character = asset_type == "character"
            target_output_spec = str(context.get("output_spec") or _character_output_spec(asset)).upper() if is_character else "MASTER"
            generation_output_spec = "A" if is_character else "MASTER"
            generation_context = scope_character_production_context(context, generation_output_spec) if is_character else context
            prompt_asset = prompt_asset_from_context(context) if is_character else asset
            try:
                async with semaphore:
                    prompt_output = await runners[skill].run({
                        "task_type": asset_type,
                        "output_spec": generation_output_spec,
                        "asset": prompt_asset,
                        "production_context": generation_context,
                        "resolved_production_brief": resolved_brief,
                        "platform": "seedream",
                        "context_key": context_key,
                        "force_rerun": _costume_design_needs_retry(existing),
                    })
                view_prompts = (
                    _normalize_costume_view_prompts(prompt_output, output_spec=generation_output_spec, asset=prompt_asset)
                    if is_character else _dict_list(prompt_output.get("view_prompts") or prompt_output.get("state_prompts"))
                )
                scoped_reviews = [
                    {
                        **item,
                        "asset_code": asset_code,
                        "asset_name": asset.get("name"),
                        "age_stage_code": dict(context.get("age_stage") or {}).get("stage_code"),
                        "costume_variant_code": dict(context.get("costume_variant") or {}).get("variant_code"),
                    }
                    for item in _dict_list(prompt_output.get("manual_review_items"))
                ]
                raw_model_response = _compact_prompt_response_diagnostics(
                    prompt_output.get("raw_model_response")
                )
                return {
                    "context_key": context_key,
                    "context_schema_version": context.get("schema_version"),
                    "asset_type": asset_type,
                    "asset_code": asset_code,
                    "asset_code_source": asset.get("asset_code_source"),
                    "asset_name": asset.get("name"),
                    "skill": skill,
                    "character_id": asset_code,
                    "character_name": asset.get("name") if is_character else None,
                    "matched_asset_id": existing.get("asset_id") if existing else None,
                    "matched_asset_code": existing.get("asset_code") if existing else None,
                    "priority": asset.get("priority"),
                    "age_stage_code": dict(context.get("age_stage") or {}).get("stage_code"),
                    "costume_variant_code": dict(context.get("costume_variant") or {}).get("variant_code"),
                    "prompt": prompt_output.get("prompt"),
                    "negative_prompt": prompt_output.get("negative_prompt"),
                    "aspect_ratio": prompt_output.get("aspect_ratio", "1:1"),
                    "material_layers": prompt_output.get("material_layers"),
                    "character_apose": prompt_output.get("character_apose"),
                    "spatial_bible": prompt_output.get("spatial_bible"),
                    "material_bible": prompt_output.get("material_bible"),
                    "state_prompts": prompt_output.get("state_prompts") or [],
                    "output_spec": target_output_spec,
                    "generated_output_spec": generation_output_spec,
                    "deferred_view_codes": ["B", "C", "D", "E"] if target_output_spec != "A" else [],
                    "view_prompts": view_prompts,
                    "manual_review_items": scoped_reviews,
                    "input_snapshot": context,
                    # P2: resolved_production_brief 不再逐 design 内嵌（存储膨胀主因）。
                    # 它是 runner 输入(682行 LLM 消费)与 run_input 顶层单份(materialize
                    # 12520/12538 从那里读)；finalize/前端不读 design 内嵌，签名也已同步移除。
                    "source_type": "agent",
                    "brief_trace": prompt_output.get("brief_trace") or {},
                    "skill_version": prompt_output.get("skill_version"),
                    "contract_version": prompt_output.get("contract_version"),
                    "input_hash": prompt_output.get("input_hash"),
                    "runtime_policy_execution": prompt_output.get("runtime_policy_execution"),
                    "structured_output_mode": dict(prompt_output.get("raw_model_response") or {}).get("structured_output_mode"),
                    "raw_model_response": raw_model_response,
                }
            except Exception as context_exc:
                error_output = _exception_output(context_exc)
                return {
                    "context_key": context_key,
                    "context_schema_version": context.get("schema_version"),
                    "asset_type": asset_type,
                    "asset_code": asset_code,
                    "asset_code_source": asset.get("asset_code_source"),
                    "asset_name": asset.get("name"),
                    "skill": skill,
                    "character_id": asset_code,
                    "character_name": asset.get("name") if is_character else None,
                    "matched_asset_id": existing.get("asset_id") if existing else None,
                    "matched_asset_code": existing.get("asset_code") if existing else None,
                    "priority": asset.get("priority"),
                    "age_stage_code": dict(context.get("age_stage") or {}).get("stage_code"),
                    "costume_variant_code": dict(context.get("costume_variant") or {}).get("variant_code"),
                    "output_spec": target_output_spec,
                    "generated_output_spec": generation_output_spec,
                    "deferred_view_codes": ["B", "C", "D", "E"] if target_output_spec != "A" else [],
                    "input_snapshot": context,
                    # P2: 见成功分支说明——resolved_production_brief 不再逐 design 内嵌。
                    "error": str(context_exc),
                    "raw_model_response": _compact_prompt_response_diagnostics(
                        error_output.get("raw_model_response")
                    ),
                }

        for batch_start in range(0, len(context_jobs), batch_size):
            batch = context_jobs[batch_start:batch_start + batch_size]
            designs.extend(await asyncio.gather(*(
                generate_context(asset, context, existing, skill)
                for asset, context, existing, skill in batch
            )))
            if _should_persist_progress(state):
                partial_summary = _costume_processing_summary(designs, reused_assets, deferred_characters)
                partial_runtime = _node_runtime_metadata(
                    state,
                    "run_asset_costume_design",
                    payload={
                        "resolved_production_brief": resolved_brief,
                        "context_count": len(context_jobs),
                    },
                )
                partial_runtime["run_asset_costume_design"].update({
                    "context_count": len(context_jobs),
                    "completed_context_count": len(designs),
                    "created_context_count": sum(item.get("processing_status") == "new" for item in partial_summary),
                    "error_context_count": sum(item.get("processing_status") == "error" for item in partial_summary),
                    "reused_asset_count": len(reused_assets),
                    "batch_size": batch_size,
                    "batch_count": (len(context_jobs) + batch_size - 1) // batch_size,
                    "completed_batch_count": (batch_start // batch_size) + 1,
                    "concurrency": max(1, settings.asset_prompt_concurrency),
                    "skills": ["character-design-prompt", "scene-design-prompt", "prop-design-prompt"],
                })
                partial_state: ScriptBreakdownWorkflowState = {
                    **state,
                    "costume_designs": [item for item in designs if item.get("asset_type") == "character"],
                    "asset_prompt_designs": list(designs),
                    "costume_processing_summary": partial_summary,
                    "asset_prompt_processing_summary": partial_summary,
                    "node_runtime": partial_runtime,
                }
                await _persist_workflow_progress(
                    partial_state,
                    node_status=_node_status(state, "run_asset_costume_design", "running"),
                    current_node="run_asset_costume_design",
                    message=f"资产提示上下文已完成 {len(designs)}/{len(context_jobs)}",
                    workflow_status="running",
                )

        processing_summary = _costume_processing_summary(designs, reused_assets, deferred_characters)
        created_count = sum(item.get("processing_status") == "new" for item in processing_summary)
        error_count = sum(item.get("processing_status") == "error" for item in processing_summary)
        formal_prompt_stage = _single_step(state) == "asset_prompt_generation"
        normalized_inventory = dict(state.get("normalized_asset_inventory") or {})
        asset_predraft_view = {
            **dict(state.get("normalized_view") or {}),
            "normalization_version": normalized_inventory.get("normalization_version"),
            "assets": list(normalized_inventory.get("assets") or []),
            "asset_prompt_designs": designs,
            "asset_prompt_processing_summary": processing_summary,
            "prompt_confirmation_required": not formal_prompt_stage,
        }
        for duplicate_group in ("characters", "scenes", "props"):
            asset_predraft_view.pop(duplicate_group, None)
        breakdown_output = {
            "breakdown_view": asset_predraft_view,
            "workflow_phase": "asset_prompt_generation" if formal_prompt_stage else "asset_costume_design",
            "prompt_confirmation_required": not formal_prompt_stage,
            "normalization_version": normalized_inventory.get("normalization_version"),
            "assets": list(normalized_inventory.get("assets") or []),
            "asset_prompt_designs": designs,
            "asset_prompt_processing_summary": processing_summary,
            "costume_designs": [item for item in designs if item.get("asset_type") == "character"],
            "costume_processing_summary": processing_summary,
        }
        runtime = _node_runtime_metadata(
            state,
            "run_asset_costume_design",
            payload={
                "resolved_production_brief": resolved_brief,
                "character_count": len(production_characters),
                "scene_count": len(production_scenes),
                "prop_count": len(production_props),
                "asset_count": len(asset_predraft_view.get("assets") or []),
                "asset_master_count": len(normalized_inventory.get("assets") or []),
                "context_count": len(context_jobs),
            },
        )
        runtime["run_asset_costume_design"].update({
            "context_count": len(context_jobs),
            "reused_asset_count": len(reused_assets),
            "created_context_count": created_count,
            "error_context_count": error_count,
            "reused_assets": reused_assets,
            "skills": ["character-design-prompt", "scene-design-prompt", "prop-design-prompt"],
            "skill_contracts": {
                skill: build_skill_runtime_metadata(skill, {
                    "resolved_production_brief": resolved_brief,
                    "production_context_count": sum(job[3] == skill for job in context_jobs),
                })
                for skill in runners
            },
            "batch_size": batch_size,
            "batch_count": (len(context_jobs) + batch_size - 1) // batch_size,
            "concurrency": max(1, settings.asset_prompt_concurrency),
            "generation_strategy": "a_first_deferred_views",
        })

        prompt_errors = [item for item in processing_summary if item.get("processing_status") == "error"] if formal_prompt_stage else []
        next_state = {
            **state,
            "costume_designs": [item for item in designs if item.get("asset_type") == "character"],
            "asset_prompt_designs": designs,
            "costume_processing_summary": processing_summary,
            "asset_prompt_processing_summary": processing_summary,
            "breakdown_output": breakdown_output,
            "normalized_view": asset_predraft_view,
            "warnings": warnings,
            "node_status": _node_status(state, "run_asset_costume_design", "failed" if prompt_errors else "succeeded"),
            "node_runtime": runtime,
            "errors": [
                *(state.get("errors") or []),
                *([{
                    "node": "run_asset_costume_design",
                    "message": f"{len(prompt_errors)} 个资产提示词生成失败，未分发资产任务。",
                }] if prompt_errors else []),
            ],
        }

        await _persist_node_state(
            next_state,
            "run_asset_costume_design",
            f"新增 {created_count} 个资产提示上下文，复用 {len(reused_assets)} 个已有资产任务，错误 {error_count} 个"
        )

        return next_state

    except Exception as exc:
        message = _exception_message(exc)
        next_state = {
            **state,
            "costume_designs": [],
            "asset_prompt_designs": [],
            "node_status": _node_status(state, "run_asset_costume_design", "failed_non_blocking"),
            "warnings": [
                *(state.get("warnings") or []),
                f"资产预定稿节点失败：{message}，已继续后续流程"
            ],
        }
        await _persist_node_state(
            next_state,
            "run_asset_costume_design",
            f"资产预定稿失败，已继续：{message}"
        )
        return next_state


async def run_script_breakdown(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    if _has_blocking_errors(state):
        return _skipped(state, "run_script_breakdown")
    await _persist_node_running(state, "run_script_breakdown", "正在生成分镜、资产和生产清单")
    try:
        payload = dict(state.get("payload") or {})
        output = await runtime_policy_executor.execute(
            "script-breakdown", payload, lambda: _build_hermes_script_breakdown(payload)
        )
        next_state = {
            **state,
            "breakdown_output": output,
            "validation_report": dict(output.get("validation_report") or {}),
            "node_status": _node_status(state, "run_script_breakdown", "succeeded"),
            "node_runtime": _node_runtime_metadata(state, "run_script_breakdown", skill="script-breakdown", payload=payload),
        }
        await _persist_node_state(next_state, "run_script_breakdown", "分镜和资产拆解完成")
        return next_state
    except Exception as exc:
        message = _exception_message(exc)
        error = {"node": "run_script_breakdown", "message": message}
        debug_output = _exception_output(exc)
        if debug_output:
            error["debug"] = debug_output
        next_state = {
            **state,
            "breakdown_output": debug_output,
            "node_status": _node_status(state, "run_script_breakdown", "failed"),
            "errors": [*(state.get("errors") or []), error],
        }
        await _persist_node_state(next_state, "run_script_breakdown", f"分镜和资产拆解失败：{message}", error=error)
        return next_state


async def run_script_segmentation(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    if _has_blocking_errors(state):
        return _skipped(state, "run_script_segmentation")
    await _persist_node_running(state, "run_script_segmentation", "正在拆分脚本段")
    try:
        payload = dict(state.get("payload") or {})
        reading_output = dict(state.get("reading_output") or {})
        reading_report = dict(payload.get("reading_report") or reading_output.get("reading_report") or {})
        # reading_output/reading_report are merged only to feed THIS node's segmentation
        # runner (runners.py:912). Keep segmentation_payload identical to the old merged
        # payload so the LLM input — and thus its output — is unchanged.
        segmentation_payload = payload
        if reading_output or reading_report:
            segmentation_payload = {
                **payload,
                "reading_output": reading_output,
                "reading_report": reading_report,
            }
        output = await runtime_policy_executor.execute(
            "script-segmentation", segmentation_payload, lambda: _build_hermes_script_segmentation(segmentation_payload)
        )
        view = dict(output.get("breakdown_view") or {})
        script_segments = _dict_list(view.get("script_segments") or output.get("script_segments"))
        # Only script_segments must reach downstream (run_script_breakdown reads
        # payload.script_segments at runners.py:678). The reading merge is not re-forwarded
        # — downstream reads reading_output from top-level state, not payload. And
        # confirmed_script_segments has no production reader (storyboard derives its own
        # segment_plans), so it is dropped rather than written write-only.
        forwarded_payload = {**payload, "script_segments": script_segments}
        next_state = {
            **state,
            "payload": forwarded_payload,
            "breakdown_output": output,
            "normalized_view": view,
            "validation_report": dict(view.get("validation_report") or output.get("validation_report") or {}),
            "node_status": _node_status(state, "run_script_segmentation", "succeeded"),
            "node_runtime": _node_runtime_metadata(state, "run_script_segmentation", skill="script-segmentation", payload={**segmentation_payload, "script_segments": script_segments, "output_target": "script_segments"}),
        }
        await _persist_node_state(next_state, "run_script_segmentation", "脚本段拆分完成")
        return next_state
    except Exception as exc:
        message = _exception_message(exc)
        error = {"node": "run_script_segmentation", "message": message}
        next_state = {
            **state,
            "breakdown_output": {},
            "node_status": _node_status(state, "run_script_segmentation", "failed"),
            "errors": [*(state.get("errors") or []), error],
        }
        await _persist_node_state(next_state, "run_script_segmentation", f"脚本段拆分失败：{message}", error=error)
        return next_state


async def validate_breakdown_contract(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    if _has_blocking_errors(state):
        return _skipped(state, "validate_breakdown_contract")
    await _persist_node_running(state, "validate_breakdown_contract", "正在校验拆解 JSON 字段")
    breakdown = dict(state.get("breakdown_output") or {})
    output = attach_breakdown_contract(
        breakdown,
        project_prefix=state.get("project_prefix") or "RF",
        episode_num=_safe_int(state.get("episode_num"), 1),
    )
    report = dict(output.get("validation_report") or {})
    status = "succeeded" if report.get("ok", False) else "failed"
    errors = list(state.get("errors") or [])
    if not report.get("ok", False):
        errors.append({"node": "validate_breakdown_contract", "message": "拆解结果未通过结构化契约校验。", "report": report})
    next_state = {
        **state,
        "breakdown_output": output,
        "normalized_view": dict(output.get("breakdown_view") or {}),
        "validation_report": report,
        "node_status": _node_status(state, "validate_breakdown_contract", status),
        "node_runtime": _node_runtime_metadata(
            state,
            "validate_breakdown_contract",
            payload={"breakdown_output": breakdown, "project_prefix": state.get("project_prefix"), "episode_num": state.get("episode_num")},
        ),
        "errors": errors,
    }
    await _persist_node_state(next_state, "validate_breakdown_contract", "拆解 JSON 字段校验完成")
    return next_state


async def run_content_review(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    if not state.get("normalized_view"):
        return _skipped(state, "run_content_review")
    await _persist_node_running(state, "run_content_review", "正在执行内容合规审查")
    review_payload = {
        "artifact_type": "breakdown_view",
        "artifact": state.get("normalized_view") or {},
        "skill": "content-compliance-review",
    }
    try:
        output = await ComplianceReviewRunner().run(review_payload)
        next_state = {
            **state,
            "review_output": output,
            "node_status": _node_status(state, "run_content_review", "succeeded"),
            "node_runtime": _node_runtime_metadata(
                state,
                "run_content_review",
                skill="content-compliance-review",
                payload=review_payload,
            ),
        }
        await _persist_node_state(next_state, "run_content_review", "内容合规审查完成")
        return next_state
    except Exception as exc:
        message = _exception_message(exc)
        error = {"node": "run_content_review", "message": message}
        next_state = {
            **state,
            "review_output": {},
            "node_status": _node_status(state, "run_content_review", "failed"),
            "errors": [*(state.get("errors") or []), error],
        }
        await _persist_node_state(next_state, "run_content_review", f"内容审查失败：{message}", error=error)
        return next_state


async def run_relation_check(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    if not state.get("normalized_view"):
        return _skipped(state, "run_relation_check")
    await _persist_node_running(state, "run_relation_check", "正在校验分镜与资产关系")
    relation_payload = {
        "breakdown_view": dict(state.get("normalized_view") or {}),
        "normalized_asset_inventory": _normalized_asset_inventory(
            dict(state.get("normalized_asset_inventory") or {}), direct=True,
        ),
        "project_context": _project_context(state),
        "skill": "relation-check",
    }
    try:
        output = await RelationCheckRunner().run(relation_payload)
        relation_status = "needs_review" if _relation_requires_review(output) else "succeeded"
        next_state = {
            **state,
            "relation_report": output,
            "node_status": _node_status(state, "run_relation_check", relation_status),
            "node_runtime": _node_runtime_metadata(
                state,
                "run_relation_check",
                skill="relation-check",
                payload=relation_payload,
            ),
        }
        message = "确定性关系校验未通过，等待人工修复" if relation_status == "needs_review" else "分镜与资产关系校验完成"
        await _persist_node_state(next_state, "run_relation_check", message)
        return next_state
    except Exception as exc:
        message = _exception_message(exc)
        next_state = {
            **state,
            "relation_report": {},
            "node_status": _node_status(state, "run_relation_check", "failed_non_blocking"),
            "warnings": [*(state.get("warnings") or []), f"关系校验节点失败：{message}"],
        }
        await _persist_node_state(next_state, "run_relation_check", f"关系校验失败，已转人工复核：{message}")
        return next_state


async def normalize_breakdown_view(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    if not state.get("normalized_view"):
        return _skipped(state, "normalize_breakdown_view")
    await _persist_node_running(state, "normalize_breakdown_view", "正在归一化前端展示字段")
    source_view = dict(state.get("normalized_view") or {})
    inventory = _normalized_asset_inventory(
        dict(state.get("normalized_asset_inventory") or {}), direct=True,
    )
    normalized = attach_breakdown_contract(
        source_view,
        project_prefix=state.get("project_prefix") or "RF",
        episode_num=_safe_int(state.get("episode_num"), 1),
        source=dict(source_view.get("source") or {}),
    )
    view = dict(normalized.get("breakdown_view") or source_view)
    if inventory:
        view["normalization_version"] = inventory["normalization_version"]
        view["assets"] = list(inventory["assets"])
    for duplicate_group in ("characters", "scenes", "props"):
        view.pop(duplicate_group, None)
    review = dict(state.get("review_output") or {})
    relation = dict(state.get("relation_report") or {})
    asset = dict(state.get("normalized_asset_inventory") or {})
    asset_review_items = [
        review
        for item in _dict_list(asset.get("assets"))
        for review in _dict_list(item.get("review_items"))
    ]
    relation_review_items = normalize_manual_review_items(relation.get("manual_review_items"), source="relation_check")
    review_items = normalize_manual_review_items(review.get("manual_review_items"), source="content_review")
    all_review_items = [
        *normalize_manual_review_items(view.get("manual_review_items"), source="contract"),
        *asset_review_items,
        *relation_review_items,
        *review_items,
    ]
    if all_review_items:
        view["manual_review_items"] = normalize_manual_review_items(all_review_items, source="workflow")
    relation_checklist = _dict_list(relation.get("delivery_checklist"))
    if relation_checklist:
        view["delivery_checklist"] = [*list(view.get("delivery_checklist") or []), *relation_checklist]
    if relation:
        view["relation_report"] = relation.get("relation_report") if isinstance(relation.get("relation_report"), dict) else relation
        view["relation_check"] = relation
    if asset:
        view["asset_extract"] = asset
    view["content_review"] = review
    output = dict(state.get("breakdown_output") or {})
    output["breakdown_view"] = view
    output["validation_report"] = view.get("validation_report") or state.get("validation_report") or {}
    next_state = {
        **state,
        "normalized_view": view,
        "breakdown_output": output,
        "node_status": _node_status(state, "normalize_breakdown_view", "succeeded"),
    }
    await _persist_node_state(next_state, "normalize_breakdown_view", "前端展示字段归一化完成")
    return next_state


async def route_workflow_status(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    await _persist_node_running(state, "route_workflow_status", "正在汇总工作流状态")
    node_status = dict(state.get("node_status") or {})
    errors = list(state.get("errors") or [])
    status = "failed" if any(value == "failed" for value in node_status.values()) or errors else "succeeded"
    if status == "succeeded" and (
        any(value == "failed_non_blocking" for value in node_status.values())
        or blocking_manual_review_items(
            (state.get("normalized_view") or {}).get("manual_review_items"),
            source="workflow",
        )
    ):
        status = "needs_review"
    if status == "succeeded" and _single_step(state) in {
        "script_reading",
        "asset_extract",
        "script_segmentation",
        "storyboard_breakdown",
        "script_breakdown",
    }:
        status = "needs_review"
    next_state = {
        **state,
        "final_status": status,
        "node_status": _node_status(state, "route_workflow_status", status),
    }
    await _persist_node_state(next_state, "route_workflow_status", "工作流状态汇总完成")
    return next_state


async def save_failed_result(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    next_state = _save_workflow_result(state, "failed", "save_failed_result")
    await _persist_node_state(next_state, "save_failed_result", "拆解流程失败", workflow_status="failed")
    return next_state


async def save_needs_review_result(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    next_state = _save_workflow_result(state, "needs_review", "save_needs_review_result")
    await _persist_node_state(next_state, "save_needs_review_result", "拆解流程完成，需人工复核", workflow_status="needs_review")
    return next_state


async def save_succeeded_result(state: ScriptBreakdownWorkflowState) -> ScriptBreakdownWorkflowState:
    next_state = _save_workflow_result(state, "succeeded", "save_succeeded_result")
    await _persist_node_state(next_state, "save_succeeded_result", "拆解流程完成", workflow_status="succeeded")
    return next_state


def _save_workflow_result(
    state: ScriptBreakdownWorkflowState,
    status: str,
    save_node: str,
) -> ScriptBreakdownWorkflowState:
    # NOTE ON APPARENT DUPLICATION: reading_report / assets / review_state are written
    # to several keys below (top-level, reading_output, breakdown_view). This is NOT
    # dead redundancy — each copy has a distinct consumer:
    #   - top-level reading_report/assets/normalization_version → _append_normalized_workflow_breakdown
    #     (repositories.py) uses these as the normalization persistence source.
    #   - reading_output → repositories.py _reading_output_from_run + production_context.py.
    #   - breakdown_view.* → the frontend BreakdownRead.view rendering path.
    # Do not "deduplicate" these into one key without redirecting every consumer first.
    node_status = _node_status(state, save_node, "succeeded")
    errors = list(state.get("errors") or [])
    output = dict(state.get("breakdown_output") or {})
    output["workflow"] = {
        "name": "script_breakdown_workflow",
        "backend": "langgraph",
        "status": status,
        "workflow_run_id": state.get("workflow_run_id"),
        "nodes": list(node_status.keys()),
    }
    output["node_status"] = node_status
    output["node_runtime"] = dict(state.get("node_runtime") or {})
    output["errors"] = errors
    output["warnings"] = list(state.get("warnings") or [])
    output["project_context"] = _project_context(state)
    revision_context = _reading_revision_context(dict(state.get("payload") or {}))
    output.update({key: value for key, value in revision_context.items() if value})
    output["reading_revision_context"] = revision_context
    reading_output = dict(state.get("reading_output") or {})
    reading_output["reading_revision_context"] = revision_context
    reading_report = dict(reading_output.get("reading_report") or {})
    payload = dict(state.get("payload") or {})
    reading_review_state = dict(
        reading_output.get("reading_review_state") or payload.get("reading_review_state") or {}
    )
    if reading_report and not output.get("reading_report"):
        output["reading_report"] = reading_report
    if reading_review_state:
        output["reading_review_state"] = reading_review_state
    if reading_report:
        view = dict(output.get("breakdown_view") or {})
        if not view.get("reading_report"):
            view["reading_report"] = reading_report
        if reading_review_state:
            view["reading_review_state"] = reading_review_state
        view["manual_review_items"] = normalize_manual_review_items(
            [
                *normalize_manual_review_items(view.get("manual_review_items"), source="workflow"),
                *normalize_manual_review_items(reading_report.get("manual_review_items"), source="script-reading"),
            ],
            source="workflow",
        )
        output["breakdown_view"] = view
    output["reading_output"] = reading_output
    output["costume_designs"] = list(state.get("costume_designs") or [])
    output["costume_processing_summary"] = list(state.get("costume_processing_summary") or [])
    output["asset_prompt_designs"] = list(state.get("asset_prompt_designs") or [])
    output["asset_prompt_processing_summary"] = list(state.get("asset_prompt_processing_summary") or [])
    normalized_inventory = dict(state.get("normalized_asset_inventory") or {})
    if normalized_inventory:
        output["normalized_asset_inventory"] = normalized_inventory
        output["normalization_version"] = normalized_inventory.get("normalization_version")
        output["assets"] = list(normalized_inventory.get("assets") or [])
    asset_review_state = dict(payload.get("asset_review_state") or {})
    if asset_review_state:
        output["asset_review_state"] = asset_review_state
        if isinstance(output.get("breakdown_view"), dict):
            output["breakdown_view"]["asset_review_state"] = asset_review_state
    output["agent_raw_asset_output"] = dict(state.get("agent_raw_asset_output") or {})
    output.pop("asset_output", None)
    for duplicate_group in ("characters", "scenes", "props"):
        output.pop(duplicate_group, None)
    output["relation_report"] = dict(state.get("relation_report") or {})
    output["review_output"] = dict(state.get("review_output") or {})
    output["agent_run_ids"] = dict(state.get("agent_run_ids") or {})
    if isinstance(state.get("breakdown_output"), dict) and (state.get("breakdown_output") or {}).get("raw_model_response"):
        output["debug_output"] = {"run_script_breakdown": dict(state.get("breakdown_output") or {})}
    output["feedback_candidate"] = _build_feedback_candidate(state, status, node_status, errors)
    return {
        **state,
        "output": output,
        "feedback_candidate": output["feedback_candidate"],
        "final_status": status,
        "node_status": node_status,
    }


def route_after_context(state: ScriptBreakdownWorkflowState) -> str:
    if _has_blocking_errors(state):
        return "failed"
    phase = str((state.get("payload") or {}).get("workflow_phase") or "")
    if phase == "storyboard_asset_breakdown":
        return "storyboard_asset_breakdown" if _reviewed_normalized_asset_inventory(state.get("payload") or {}) else "asset_confirmation_required"
    return "continue"


def route_after_context_entry(state: ScriptBreakdownWorkflowState) -> str:
    if _has_blocking_errors(state):
        return "failed"
    if _single_step(state):
        return "single_step"
    return route_after_context(state)


def route_single_step_target(state: ScriptBreakdownWorkflowState) -> str:
    if _has_blocking_errors(state):
        return "failed"
    single_step = _single_step(state)
    if single_step == "asset_costume_design":
        return "run_asset_extract"
    return SINGLE_STEP_NODES.get(single_step, "failed")


def route_after_asset_extract(state: ScriptBreakdownWorkflowState) -> str:
    if _has_blocking_errors(state):
        return "failed"
    if _reviewed_normalized_asset_inventory(state.get("payload") or {}):
        return "continue"
    return "needs_review"


def route_after_asset_extract_entry(state: ScriptBreakdownWorkflowState) -> str:
    if _single_step(state) == "asset_extract":
        return "failed" if _has_blocking_errors(state) else "needs_review"
    if _single_step(state) == "asset_costume_design":
        return "failed" if _has_blocking_errors(state) else "continue"
    return route_after_asset_extract(state)


def route_after_script_reading(state: ScriptBreakdownWorkflowState) -> str:
    if _has_blocking_errors(state):
        return "failed"
    if _reviewed_reading_report(state.get("payload") or {}):
        return "continue"
    return "needs_review"


def route_after_script_reading_entry(state: ScriptBreakdownWorkflowState) -> str:
    if _single_step(state) == "script_reading":
        return "failed" if _has_blocking_errors(state) else "needs_review"
    return route_after_script_reading(state)


def route_after_costume_design_entry(state: ScriptBreakdownWorkflowState) -> str:
    if _has_blocking_errors(state):
        return "failed"
    if _single_step(state) in {"asset_prompt_generation", "asset_costume_design"}:
        return "single_step_complete"
    return "continue"


def route_after_breakdown(state: ScriptBreakdownWorkflowState) -> str:
    return "failed" if _has_blocking_errors(state) else "continue"


def route_after_segmentation(state: ScriptBreakdownWorkflowState) -> str:
    if _has_blocking_errors(state):
        return "failed"
    phase = str((state.get("payload") or {}).get("workflow_phase") or "")
    if phase == "script_segmentation":
        return "segmentation_only"
    return "continue"


def route_after_segmentation_entry(state: ScriptBreakdownWorkflowState) -> str:
    if _single_step(state) == "script_segmentation":
        return "failed" if _has_blocking_errors(state) else "single_step_complete"
    return route_after_segmentation(state)


def route_after_contract_validation(state: ScriptBreakdownWorkflowState) -> str:
    report = dict(state.get("validation_report") or {})
    if _has_blocking_errors(state) or not report.get("ok", False):
        return "failed"
    if _blocking_missing_asset_suggestions(state.get("breakdown_output") or {}):
        return "needs_review"
    return "continue"


def route_after_contract_validation_entry(state: ScriptBreakdownWorkflowState) -> str:
    return route_after_contract_validation(state)


def route_after_relation_check(state: ScriptBreakdownWorkflowState) -> str:
    if _has_blocking_errors(state):
        return "failed"
    if _relation_requires_review(state.get("relation_report") or {}):
        return "needs_review"
    return "continue"


def _relation_requires_review(report: dict[str, Any]) -> bool:
    if report.get("gate_status") != "blocked":
        return False
    review_items = normalize_manual_review_items(report.get("manual_review_items"), source="relation_check")
    return not review_items or bool(blocking_manual_review_items(review_items, source="relation_check"))


def route_after_relation_check_entry(state: ScriptBreakdownWorkflowState) -> str:
    if _single_step(state) == "relation_check":
        return "failed" if _has_blocking_errors(state) else "single_step_complete"
    return route_after_relation_check(state)


def route_after_content_review(state: ScriptBreakdownWorkflowState) -> str:
    return "failed" if _has_blocking_errors(state) else "continue"


def route_to_save_node(state: ScriptBreakdownWorkflowState) -> str:
    status = str(state.get("final_status") or "failed")
    return status if status in {"failed", "needs_review", "succeeded"} else "failed"


def _single_step(state: ScriptBreakdownWorkflowState) -> str:
    return str((state.get("payload") or {}).get("single_step") or "").strip()


def _node_status(state: ScriptBreakdownWorkflowState, node: str, status: str) -> dict[str, str]:
    return {**dict(state.get("node_status") or {}), node: status}


async def _persist_node_running(state: ScriptBreakdownWorkflowState, node: str, message: str) -> None:
    if not _should_persist_progress(state):
        return
    node_status = _node_status(state, node, "running")
    await _persist_workflow_progress(state, node_status=node_status, current_node=node, message=message, workflow_status="running")


async def _persist_node_state(
    state: ScriptBreakdownWorkflowState,
    node: str,
    message: str,
    *,
    workflow_status: str | None = None,
    error: dict[str, Any] | None = None,
) -> None:
    if not _should_persist_progress(state):
        return
    await _persist_workflow_progress(
        state,
        node_status=dict(state.get("node_status") or {}),
        current_node=node,
        message=message,
        workflow_status=workflow_status,
        error=error,
    )


def _should_persist_progress(state: ScriptBreakdownWorkflowState) -> bool:
    return bool((state.get("payload") or {}).get("persist_progress"))


async def _persist_workflow_progress(
    state: ScriptBreakdownWorkflowState,
    *,
    node_status: dict[str, str],
    current_node: str,
    message: str,
    workflow_status: str | None,
    error: dict[str, Any] | None = None,
) -> None:
    workflow_run_id = state.get("workflow_run_id")
    if not workflow_run_id:
        return
    try:
        workflow_uuid = uuid.UUID(str(workflow_run_id))
    except ValueError:
        return
    progress = _workflow_progress_payload(state, node_status, current_node, message)
    try:
        from app.db.base import async_session
        from app.services import repositories as repo

        async with async_session() as session:
            await repo.update_workflow_run_progress(
                session,
                workflow_uuid,
                status=workflow_status or _workflow_status_from_nodes(node_status),
                node_status=node_status,
                progress=progress,
                error=error,
                node_runtime=dict(state.get("node_runtime") or {}),
                workflow_output=dict(state),
            )
    except Exception:
        return


def _workflow_progress_payload(
    state: ScriptBreakdownWorkflowState,
    node_status: dict[str, str],
    current_node: str,
    message: str,
) -> dict[str, Any]:
    expected_nodes = _expected_progress_nodes(state, node_status)
    completed_statuses = {"succeeded", "failed", "failed_non_blocking", "skipped", "needs_review"}
    completed = sum(1 for node in expected_nodes if node_status.get(node) in completed_statuses)
    total = max(len(expected_nodes), 1)
    return {
        "current_node": current_node,
        "current_label": _workflow_node_label(current_node),
        "message": message,
        "completed": min(completed, total),
        "total": total,
        "percent": int(min(completed, total) / total * 100),
        "phase": str((state.get("payload") or {}).get("workflow_phase") or "full_breakdown"),
        "nodes": [
            {
                "key": node,
                "label": _workflow_node_label(node),
                "status": node_status.get(node, "pending"),
            }
            for node in expected_nodes
        ],
        "updated_at": datetime.now(UTC).isoformat(),
    }


def _expected_progress_nodes(state: ScriptBreakdownWorkflowState, node_status: dict[str, str]) -> list[str]:
    failed = any(status == "failed" for status in node_status.values()) or bool(state.get("errors"))
    payload = state.get("payload") or {}
    single_step = str(payload.get("single_step") or "")
    final_status = str(state.get("final_status") or "")
    save_node_single = (
        "save_failed_result"
        if failed
        else "save_needs_review_result"
        if single_step == "asset_extract" or final_status == "needs_review"
        else "save_succeeded_result"
    )
    if single_step:
        target = SINGLE_STEP_NODES.get(single_step)
        base = ["load_project_context", "bootstrap_single_step"]
        if single_step in {"storyboard_breakdown", "script_breakdown"}:
            steps = [
                "run_script_breakdown",
                "validate_breakdown_contract",
                "run_relation_check",
                "run_content_review",
                "normalize_breakdown_view",
            ]
        elif single_step == "asset_costume_design":
            steps = ["run_asset_extract", "run_asset_costume_design"]
        elif single_step in {"content_review", "relation_check"}:
            steps = ["run_content_review", "normalize_breakdown_view"]
            if single_step == "relation_check":
                steps[0] = "run_relation_check"
        else:
            steps = [target] if target else []
        route_nodes = [] if failed or single_step in {"script_reading", "asset_extract"} else ["route_workflow_status"]
        return [*base, *steps, *route_nodes, save_node_single]

    if (
        node_status.get("save_needs_review_result") == "succeeded"
        and not _reviewed_normalized_asset_inventory(payload)
        and "run_script_breakdown" not in node_status
    ):
        completed_nodes = ["load_project_context"]
        for node in ("run_script_reading", "run_asset_extract"):
            if node in node_status:
                completed_nodes.append(node)
        return [*completed_nodes, "save_needs_review_result"]

    phase = str(payload.get("workflow_phase") or "")
    if phase == "script_segmentation":
        save_node = "save_failed_result" if failed else "save_succeeded_result"
        return [
            "load_project_context",
            "run_script_reading",
            "run_asset_extract",
            "run_asset_costume_design",
            "run_script_segmentation",
            save_node,
        ]

    final_status = str(state.get("final_status") or "")
    if failed:
        save_node = "save_failed_result"
    elif final_status == "needs_review":
        save_node = "save_needs_review_result"
    else:
        save_node = "save_succeeded_result"
    if phase == "storyboard_asset_breakdown":
        return [
            "load_project_context",
            "run_script_breakdown",
            "validate_breakdown_contract",
            "run_relation_check",
            "run_content_review",
            "normalize_breakdown_view",
            "route_workflow_status",
            save_node,
        ]
    return [
        "load_project_context",
        "run_script_reading",
        "run_asset_extract",
        "run_asset_costume_design",
        "run_script_segmentation",
        "run_script_breakdown",
        "validate_breakdown_contract",
        "run_relation_check",
        "run_content_review",
        "normalize_breakdown_view",
        "route_workflow_status",
        save_node,
    ]


def _workflow_status_from_nodes(node_status: dict[str, str]) -> str:
    if any(status == "failed" for status in node_status.values()):
        return "failed"
    if any(status == "running" for status in node_status.values()):
        return "running"
    return "running"


def _workflow_node_label(node: str) -> str:
    labels = {
        "load_project_context": "读取项目剧本",
        "bootstrap_single_step": "准备步骤数据",
        "run_script_reading": "剧本围读",
        "run_asset_extract": "正式资产清单",
        "run_asset_costume_design": "资产预定稿",
        "run_script_segmentation": "脚本段拆分",
        "run_script_breakdown": "分镜预定稿",
        "validate_breakdown_contract": "字段校验",
        "run_relation_check": "关系校验",
        "run_content_review": "后台内容审查",
        "normalize_breakdown_view": "前端字段归一化",
        "route_workflow_status": "状态汇总",
        "save_failed_result": "保存失败结果",
        "save_needs_review_result": "保存复核结果",
        "save_succeeded_result": "保存成功结果",
    }
    return labels.get(node, node)


def _has_blocking_errors(state: ScriptBreakdownWorkflowState) -> bool:
    return bool(state.get("errors"))


def _skipped(state: ScriptBreakdownWorkflowState, node: str) -> ScriptBreakdownWorkflowState:
    return {**state, "node_status": _node_status(state, node, "skipped")}


def _node_runtime_metadata(
    state: ScriptBreakdownWorkflowState,
    node: str,
    *,
    payload: dict[str, Any],
    skill: str | None = None,
) -> dict[str, dict[str, Any]]:
    if skill:
        metadata = build_skill_runtime_metadata(skill, payload)
    else:
        metadata = {
            "input_hash": stable_payload_hash(payload),
            "contract_version": None,
            "skill_version": None,
            "node_weight": "light",
            "run_scope": "workflow",
            "can_rerun_scope": ["workflow"],
            "workflow_lane": "script_breakdown_main",
            "cache_policy": "always_recompute_when_workflow_runs",
            "retry_policy": "inherit_workflow_retry",
        }
    metadata["node"] = node
    return {**dict(state.get("node_runtime") or {}), node: metadata}


def _project_context(state: ScriptBreakdownWorkflowState) -> dict[str, Any]:
    return {
        "project_id": state.get("project_id"),
        "project_prefix": state.get("project_prefix"),
        "project_title": state.get("project_title"),
        "genre": state.get("genre"),
        "resolved_production_brief": dict(state.get("resolved_production_brief") or {}),
        "episode_num": state.get("episode_num"),
        "workflow_run_id": state.get("workflow_run_id"),
    }


def _compact_reading_for_asset_extract(report: dict[str, Any]) -> dict[str, Any]:
    return {
        key: report.get(key)
        for key in (
            "story_overview",
            "relationship_map",
            "worldview",
            "cultural_origin",
            "recommended_visual_style",
            "visual_style",
            "manual_review_items",
            "report_status",
            "confirmed_at",
        )
        if report.get(key) not in (None, {}, [])
    }


def _inherit_reading_review_suggestions(
    asset_output: dict[str, Any],
    reading_report: dict[str, Any],
) -> dict[str, Any]:
    """Restore a structured reading suggestion when asset extraction repeats the same field without it."""
    suggestions_by_field = {
        str(item.get("field") or item.get("target_field") or "").strip().lower(): str(item.get("suggestion") or "").strip()
        for item in _dict_list(reading_report.get("manual_review_items"))
        if str(item.get("field") or item.get("target_field") or "").strip()
        and str(item.get("suggestion") or "").strip()
    }
    if not suggestions_by_field:
        return asset_output
    reviews = _dict_list(asset_output.get("manual_review_items"))
    if not reviews:
        return asset_output
    enriched = []
    for item in reviews:
        field = str(item.get("field") or item.get("target_field") or "").strip().lower()
        suggestion = str(item.get("suggestion") or "").strip()
        inherited = suggestions_by_field.get(field, "") if not suggestion else ""
        enriched.append({**item, **({"suggestion": inherited} if inherited else {})})
    return {**asset_output, "manual_review_items": enriched}


def _build_asset_predraft_view(
    reading_output: dict[str, Any],
    costume_designs: list[dict[str, Any]],
    *,
    costume_processing_summary: list[dict[str, Any]] | None = None,
    project_prefix: str,
    episode_num: int,
    prompt_confirmation_required: bool = True,
) -> dict[str, Any]:
    reading_report = dict(reading_output.get("reading_report") or {})
    role_candidates = _candidate_source(reading_report, reading_output, "role_candidates", "characters")
    scene_candidates = _candidate_source(reading_report, reading_output, "scene_candidates", "scenes")
    prop_candidates = _candidate_source(reading_report, reading_output, "prop_candidates", "props")

    role_name_by_code = {
        str(candidate.get("role_code") or candidate.get("asset_code") or "").strip(): str(candidate.get("name") or "").strip()
        for candidate in role_candidates
        if str(candidate.get("role_code") or candidate.get("asset_code") or "").strip()
    }
    characters = [
        _candidate_to_asset(candidate, "character", index, role_name_by_code=role_name_by_code)
        for index, candidate in enumerate(_sort_candidates(role_candidates), start=1)
    ]
    existing_characters = _dict_list(reading_output.get("existing_characters"))
    existing_by_code = {
        str(item.get("asset_code") or "").strip(): item
        for item in existing_characters
        if str(item.get("asset_code") or "").strip()
    }
    existing_by_name = {
        str(item.get("name") or "").strip(): item
        for item in existing_characters
        if str(item.get("name") or "").strip()
    }
    characters = [
        _merge_existing_character_asset(
            asset,
            existing_by_code.get(str(asset.get("asset_code") or "").strip())
            or existing_by_name.get(str(asset.get("name") or "").strip()),
        )
        for asset in characters
    ]
    scenes = [
        _candidate_to_asset(candidate, "scene", index, role_name_by_code=role_name_by_code)
        for index, candidate in enumerate(_sort_candidates(scene_candidates), start=1)
    ]
    props = [
        _candidate_to_asset(candidate, "prop", index, role_name_by_code=role_name_by_code)
        for index, candidate in enumerate(_sort_candidates(prop_candidates), start=1)
    ]
    manual_review_items = normalize_manual_review_items(
        [
            *reading_report.get("manual_review_items", []),
            *[
                {
                    **item,
                    "code": str(design.get("character_id") or ""),
                    "detail": _review_detail_with_asset(design, item),
                    "source": "asset_costume_design",
                }
                for design in costume_designs
                for item in _dict_list(design.get("manual_review_items"))
            ],
        ],
        source="asset_predraft",
    )
    if not prompt_confirmation_required:
        manual_review_items = []
    costume_by_code: dict[str, list[dict[str, Any]]] = {}
    costume_by_name: dict[str, list[dict[str, Any]]] = {}
    for item in costume_designs:
        code = str(item.get("character_id") or "").strip()
        name = str(item.get("character_name") or "").strip()
        if code:
            costume_by_code.setdefault(code, []).append(item)
        if name:
            costume_by_name.setdefault(name, []).append(item)
    characters = [
        _merge_character_costume_designs(
            asset,
            costume_by_code.get(str(asset.get("asset_code") or "").strip())
            or costume_by_name.get(str(asset.get("name") or "").strip())
            or [],
        )
        for asset in characters
    ]
    design_by_key = {
        key: item
        for item in costume_designs
        for key in (
            f"{item.get('asset_type') or 'character'}:{str(item.get('asset_code') or item.get('character_id') or '').strip()}",
            f"{item.get('asset_type') or 'character'}:{str(item.get('asset_name') or item.get('character_name') or '').strip()}",
        )
        if key.split(":", 1)[1]
    }
    scenes = [_merge_asset_prompt_design(asset, design_by_key.get(f"scene:{asset.get('asset_code')}") or design_by_key.get(f"scene:{asset.get('name')}"), confirmation_required=prompt_confirmation_required) for asset in scenes]
    props = [_merge_asset_prompt_design(asset, design_by_key.get(f"prop:{asset.get('asset_code')}") or design_by_key.get(f"prop:{asset.get('name')}"), confirmation_required=prompt_confirmation_required) for asset in props]
    characters = [_merge_asset_prompt_design(asset, design_by_key.get(f"character:{asset.get('asset_code')}") or design_by_key.get(f"character:{asset.get('name')}"), confirmation_required=prompt_confirmation_required) for asset in characters]
    assets = [*characters, *scenes, *props]
    assets, manual_review_items = _match_costume_asset_suggestions(assets, manual_review_items)
    if prompt_confirmation_required:
        manual_review_items = normalize_manual_review_items(
            [*manual_review_items, *_missing_asset_review_items(assets, manual_review_items)],
            source="asset_predraft",
        )
        manual_review_items = _normalize_prop_owner_review_items(manual_review_items, assets)
        manual_review_items = [_standardize_production_hint(item, assets) for item in manual_review_items]
    else:
        manual_review_items = []
    assets = [_attach_asset_production_hints(asset, manual_review_items) for asset in assets]
    characters = [asset for asset in assets if asset.get("asset_type") == "character"]
    scenes = [asset for asset in assets if asset.get("asset_type") == "scene"]
    props = [asset for asset in assets if asset.get("asset_type") == "prop"]
    asset_masters = [_asset_to_master(asset) for asset in assets]
    return {
        "schema_version": "asset_predraft_v1",
        "workflow_phase": "asset_prompt_generation" if not prompt_confirmation_required else "asset_costume_design",
        "prompt_confirmation_required": prompt_confirmation_required,
        "project": {
            "project_prefix": project_prefix,
            "episode_num": episode_num,
        },
        "source": {
            "source_label": "agent",
            "skills": ["asset-extract", "character-design-prompt", "scene-design-prompt", "prop-design-prompt"],
            "asset_skill": "asset-extract",
            "prompt_skills": ["character-design-prompt", "scene-design-prompt", "prop-design-prompt"],
            "reading_source": reading_output.get("source") or "reading_output",
        },
        "asset_extract": dict(reading_output.get("asset_extract") or {}),
        "reading_report": reading_report,
        "reading_review_state": dict(reading_output.get("reading_review_state") or {}),
        "characters": characters,
        "scenes": scenes,
        "props": props,
        "assets": assets,
        "asset_masters": asset_masters,
        "asset_prompt_designs": costume_designs,
        "asset_prompt_processing_summary": list(costume_processing_summary or []),
        "costume_designs": costume_designs,
        "costume_processing_summary": list(costume_processing_summary or []),
        "manual_review_items": manual_review_items,
        "notes": [
            "资产预定稿先由 asset-extract 从原文和确认围读稿生成统一资产清单，再由人物、场景、道具专用 Prompt Skill 生成各自母版提示词。",
            "人物按角色/年龄阶段/装扮上下文首轮生成 A 图提示词；S/A 的 B-E 延迟到 A 定版后按图位独立生成。场景与道具先生成 MASTER，后续视角或状态通过 P/SC 变体任务追加。",
            "待确认问题与生产提示词隔离，人工补充后只需重新运行受影响的资产上下文。",
        ],
    }


def _candidate_source(
    reading_report: dict[str, Any],
    reading_output: dict[str, Any],
    report_key: str,
    output_key: str,
) -> list[dict[str, Any]]:
    # Once asset-extract has run, its canonical groups are authoritative over
    # older reading candidates that may still be embedded in the confirmed report.
    return _dict_list(reading_output.get(report_key)) or _dict_list(reading_output.get(output_key)) or _dict_list(reading_report.get(report_key))


def _sort_candidates(candidates: list[dict[str, Any]]) -> list[dict[str, Any]]:
    priority_order = {"S": 0, "A": 1, "B": 2, "C": 3}
    return sorted(
        candidates,
        key=lambda item: (
            priority_order.get(str(item.get("priority") or "C").upper(), 4),
            str(item.get("asset_code") or item.get("role_code") or item.get("scene_code") or item.get("prop_code") or item.get("name") or ""),
        ),
    )


def _candidate_to_asset(
    candidate: dict[str, Any],
    asset_type: str,
    index: int,
    *,
    role_name_by_code: dict[str, str],
) -> dict[str, Any]:
    code_key = {"character": "role_code", "scene": "scene_code", "prop": "prop_code"}[asset_type]
    prefix = {"character": "R", "scene": "SC", "prop": "P"}[asset_type]
    code = str(candidate.get("asset_code") or candidate.get(code_key) or f"{prefix}{index:03d}").strip()
    if asset_type == "scene" and re.fullmatch(r"S\d+", code.upper()):
        code = f"SC{code[1:]}"
    metadata = {**dict(candidate), **dict(candidate.get("metadata") or {})}
    if asset_type == "prop":
        owner_code = str(candidate.get("owner_role_code") or candidate.get("owner_character_id") or "").strip()
        if owner_code and not metadata.get("owner_character"):
            metadata["owner_character"] = role_name_by_code.get(owner_code, owner_code)
    return {
        **candidate,
        "asset_type": asset_type,
        "type": asset_type,
        "asset_code": code,
        "name": str(candidate.get("name") or "").strip(),
        "description": _asset_description(candidate, asset_type),
        "priority": str(candidate.get("priority") or "C").upper(),
        "status": str(candidate.get("status") or "candidate"),
        "metadata": metadata,
    }


def _asset_description(candidate: dict[str, Any], asset_type: str) -> str:
    if asset_type == "character":
        parts = [
            str(candidate.get("dramatic_function") or "").strip(),
            _display_text(candidate.get("appearance_clues")),
            str(candidate.get("costume_direction") or "").strip(),
            str(candidate.get("prompt_usage") or "").strip(),
        ]
    elif asset_type == "scene":
        parts = [
            str(candidate.get("dramatic_function") or "").strip(),
            str(candidate.get("visual_direction") or "").strip(),
            str(candidate.get("shot_size") or "").strip(),
            str(candidate.get("prompt_usage") or "").strip(),
        ]
    else:
        parts = [
            str(candidate.get("story_function") or "").strip(),
            str(candidate.get("visual_anchor") or "").strip(),
            str(candidate.get("prompt_usage") or "").strip(),
        ]
    return "；".join(part for part in parts if part)


def _asset_to_master(asset: dict[str, Any]) -> dict[str, Any]:
    metadata = dict(asset.get("metadata") or {})
    asset_type = str(asset.get("asset_type") or "")
    asset_role = {
        "character": "character_master",
        "scene": "scene_master",
        "prop": "prop_master",
    }.get(asset_type, "asset_master")
    return {
        "asset_code": asset.get("asset_code"),
        "asset_type": asset_type,
        "asset_role": asset_role,
        "name": asset.get("name"),
        "priority": asset.get("priority") or metadata.get("priority") or "C",
        "visual_goal": metadata.get("visual_direction") or metadata.get("prompt_usage") or asset.get("description") or "",
        "covered_zones": _text_list(metadata.get("spatial_zones")),
        "camera_coverage": _text_list(metadata.get("scene_view_plan")) or _text_list(metadata.get("shot_size")),
        "key_props": _text_list(metadata.get("key_props")),
        "lighting_states": _text_list(metadata.get("lighting")),
        "mood_tags": _text_list(metadata.get("mood") or metadata.get("emotional_arc")),
        "style_rules": metadata.get("continuity_anchors") or metadata.get("forbidden_variations") or "",
        "usage": metadata.get("prompt_usage") or metadata.get("story_function") or metadata.get("dramatic_function") or "",
        "evidence": metadata.get("evidence") or "",
        "metadata": metadata,
    }


def _merge_existing_character_asset(
    asset: dict[str, Any],
    existing: dict[str, Any] | None,
) -> dict[str, Any]:
    if not existing:
        return asset
    metadata = {**dict(asset.get("metadata") or {}), **dict(existing.get("metadata") or {})}
    age_stages = existing.get("age_stages") or metadata.get("age_stages") or asset.get("age_stages") or []
    return {
        **asset,
        **existing,
        "type": "character",
        "asset_type": "character",
        "asset_code": existing.get("asset_code") or asset.get("asset_code"),
        "name": existing.get("name") or asset.get("name"),
        "agent_asset_code": asset.get("asset_code"),
        "matched_asset_id": existing.get("asset_id") or existing.get("id"),
        "matched_asset_code": existing.get("asset_code"),
        "reuse_status": "reused_existing",
        "age_stages": age_stages,
        "metadata": {**metadata, "age_stages": age_stages} if age_stages else metadata,
    }


def _costume_design_needs_retry(asset: dict[str, Any] | None) -> bool:
    """Bypass prompt cache when the previous result failed or is no longer current."""
    if not isinstance(asset, dict):
        return False
    if _asset_prompt_requires_regeneration(asset):
        return True
    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}
    candidates = [asset.get("costume_design"), metadata.get("costume_design")]
    for value in candidates:
        if not isinstance(value, dict):
            continue
        if str(value.get("error") or "").strip():
            return True
        if str(value.get("status") or "").strip().lower() in {"failed", "error"}:
            return True
    return False


def _character_prompt_contexts(
    character: dict[str, Any],
    existing: dict[str, Any] | None,
    reading_context: dict[str, Any],
    existing_props: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    if (
        not existing
        or not _asset_prompt_can_reuse(existing)
    ):
        return build_character_production_contexts(character, existing, reading_context, existing_props)
    requested_stages = _character_age_stages(character)
    if not requested_stages:
        return []
    existing_stages = _character_age_stages(existing)
    pending_stages = _new_character_age_stage_contexts(requested_stages, existing_stages)
    if not pending_stages:
        return []
    scoped_metadata = {**dict(existing.get("metadata") or {}), "age_stages": pending_stages}
    scoped_existing = {**existing, "age_stages": pending_stages, "metadata": scoped_metadata}
    contexts = build_character_production_contexts(character, scoped_existing, reading_context, existing_props)
    return [context for context in contexts if not _asset_task_exists_for_context(existing, context)]


def _character_requires_visual_master(character: dict[str, Any] | None) -> bool:
    """Only characters visible in the current episode need a visual master now."""
    if not isinstance(character, dict):
        return True
    metadata = {
        **(character.get("attributes") if isinstance(character.get("attributes"), dict) else {}),
        **(character.get("metadata") if isinstance(character.get("metadata"), dict) else {}),
    }
    raw = character.get("visual_presence") or character.get("presence_mode") or character.get("appearance_scope") or metadata.get("visual_presence")
    normalized = str(raw or "").strip().lower().replace("-", "_").replace(" ", "_")
    if normalized in {"mentioned_only", "mentioned", "背景提及", "voice_only", "voice", "off_screen"} or any(
        marker in normalized for marker in ("仅提及", "未出场", "不出镜", "画外音", "仅声音")
    ):
        return False
    if normalized:
        return True
    character_type = character.get("character_type") or metadata.get("character_type")
    type_text = str(character_type or "").strip().lower().replace("-", "_").replace(" ", "_")
    return not any(
        marker in type_text
        for marker in ("mentioned_only", "背景提及", "仅提及", "未出场", "不出镜", "voice_only", "off_screen", "画外音", "仅声音")
    )


def _character_has_asset_tasks(asset: dict[str, Any] | None) -> bool:
    if not isinstance(asset, dict):
        return False
    return asset.get("has_asset_tasks") is True or bool(_dict_list(asset.get("asset_task_contexts")))


def _asset_prompt_requires_regeneration(asset: dict[str, Any] | None) -> bool:
    if not isinstance(asset, dict):
        return False
    raw_metadata = asset.get("metadata") or asset.get("metadata_json")
    metadata = raw_metadata if isinstance(raw_metadata, dict) else {}
    lifecycle = str(
        metadata.get("confirmation_status")
        or asset.get("confirmation_status")
        or asset.get("status")
        or ""
    ).strip().lower()
    validity = str(metadata.get("prompt_validity") or asset.get("prompt_validity") or "").strip().lower()
    return lifecycle == "prompt_pending" or validity in {"stale", "missing"}


def _asset_prompt_can_reuse(asset: dict[str, Any] | None) -> bool:
    if not isinstance(asset, dict) or _asset_prompt_requires_regeneration(asset):
        return False
    if _character_has_asset_tasks(asset):
        return True
    metadata = asset.get("metadata") or asset.get("metadata_json")
    metadata = metadata if isinstance(metadata, dict) else {}
    lifecycle = str(
        metadata.get("confirmation_status")
        or asset.get("confirmation_status")
        or asset.get("status")
        or ""
    ).strip().lower()
    validity = str(metadata.get("prompt_validity") or asset.get("prompt_validity") or "").strip().lower()
    has_prompt = bool(
        asset.get("prompt")
        or asset.get("prompt_text")
        or asset.get("input_hash")
        or asset.get("skill")
        or metadata.get("prompt_design")
        or metadata.get("costume_design")
        or metadata.get("prompt_lineage")
    )
    return has_prompt and (validity == "current" or lifecycle in {"confirmed", "pending_confirmation"})


def _asset_task_exists_for_context(asset: dict[str, Any], context: dict[str, Any]) -> bool:
    age_stage_code = _asset_name_key(dict(context.get("age_stage") or {}).get("stage_code"))
    costume_variant_code = _asset_name_key(dict(context.get("costume_variant") or {}).get("variant_code"))
    for task in _dict_list(asset.get("asset_task_contexts")):
        task_age = _asset_name_key(task.get("age_stage_code"))
        task_costume = _asset_name_key(task.get("costume_variant_code"))
        if task_age == age_stage_code and task_costume == costume_variant_code:
            return True
    return False


def _costume_processing_summary(
    costume_designs: list[dict[str, Any]],
    reused_characters: list[dict[str, Any]],
    deferred_characters: list[dict[str, Any]] | None = None,
) -> list[dict[str, Any]]:
    summary: list[dict[str, Any]] = []
    for index, design in enumerate(costume_designs, start=1):
        error = str(design.get("error") or "").strip()
        asset_type = str(design.get("asset_type") or "character").strip().lower()
        type_label = {"character": "人物", "scene": "场景", "prop": "道具"}.get(asset_type, "资产")
        age_stage_code = str(design.get("age_stage_code") or "").strip()
        costume_variant_code = str(design.get("costume_variant_code") or "").strip()
        runtime = dict(design.get("runtime_policy_execution") or {})
        view_prompts = _dict_list(design.get("view_prompts"))
        context_label = " / ".join(item for item in (age_stage_code, costume_variant_code) if item)
        detail = (
            f"提示词生成失败：{error}"
            if error
            else f"生成新增{context_label}{type_label}资产母版提示词。"
            if context_label
            else f"生成新的{type_label}资产母版提示词。"
        )
        summary.append({
            "processing_status": "error" if error else "new",
            "context_key": design.get("context_key") or f"costume-context-{index}",
            "candidate_asset_code": design.get("character_id"),
            "asset_code": design.get("matched_asset_code") or design.get("asset_code") or design.get("character_id"),
            "asset_code_source": design.get("asset_code_source"),
            "asset_type": asset_type,
            "character_name": design.get("character_name") or design.get("asset_name"),
            "age_stage_code": age_stage_code or None,
            "costume_variant_code": costume_variant_code or None,
            "output_spec": design.get("generated_output_spec") or design.get("output_spec") or "A",
            "prompt_count": len(view_prompts) or (1 if design.get("prompt") else 0),
            "structured_output_mode": design.get("structured_output_mode"),
            "cache_hit": runtime.get("cache_hit") is True,
            "detail": detail,
            "error": error or None,
        })
    summary.extend({
        "processing_status": "reused",
        "context_key": f"reuse:{item.get('matched_asset_code') or item.get('candidate_asset_code') or index}",
        "candidate_asset_code": item.get("candidate_asset_code"),
        "asset_code": item.get("matched_asset_code") or item.get("candidate_asset_code"),
        "asset_type": item.get("asset_type") or "character",
        "character_name": item.get("matched_name") or item.get("candidate_name"),
        "age_stage_code": None,
        "costume_variant_code": None,
        "output_spec": None,
        "prompt_count": 0,
        "asset_task_count": item.get("asset_task_count") or 0,
        "detail": item.get("detail") or "复用已有角色资产任务。",
        "error": None,
    } for index, item in enumerate(reused_characters, start=1))
    summary.extend({
        "processing_status": "deferred",
        "context_key": f"deferred:{item.get('asset_code') or item.get('role_code') or item.get('name') or index}",
        "candidate_asset_code": item.get("asset_code") or item.get("role_code"),
        "asset_code": item.get("asset_code") or item.get("role_code"),
        "asset_code_source": item.get("asset_code_source"),
        "asset_type": "character",
        "character_name": item.get("name"),
        "age_stage_code": None,
        "costume_variant_code": None,
        "output_spec": None,
        "prompt_count": 0,
        "asset_task_count": 0,
        "detail": "本集仅提及或只有画外音，已登记正式资产，待首次出场后生成角色定装 Prompt。",
        "error": None,
    } for index, item in enumerate(deferred_characters or [], start=1))
    return summary


def _character_age_stages(asset: dict[str, Any]) -> list[dict[str, Any]]:
    metadata = dict(asset.get("metadata") or {})
    return _dict_list(asset.get("age_stages") or metadata.get("age_stages"))


def _new_character_age_stage_contexts(
    requested: list[dict[str, Any]],
    existing: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    existing_by_key = {
        key: stage
        for stage in existing
        for key in _stage_keys(stage)
    }
    pending: list[dict[str, Any]] = []
    for stage in requested:
        baseline = next((existing_by_key[key] for key in _stage_keys(stage) if key in existing_by_key), None)
        if baseline is None:
            pending.append(dict(stage))
            continue
        requested_variants = _dict_list(stage.get("costume_variants"))
        if not requested_variants:
            continue
        existing_variant_keys = {
            key
            for variant in _dict_list(baseline.get("costume_variants"))
            for key in _variant_keys(variant)
        }
        new_variants = [
            dict(variant)
            for variant in requested_variants
            if not any(key in existing_variant_keys for key in _variant_keys(variant))
        ]
        if new_variants:
            pending.append({**stage, "costume_variants": new_variants})
    return pending


def _stage_keys(stage: dict[str, Any]) -> set[str]:
    return {
        value for value in (
            f"code:{_asset_name_key(stage.get('stage_code'))}",
            f"name:{_asset_name_key(stage.get('name'))}",
        ) if value not in {"code:", "name:"}
    }


def _variant_keys(variant: dict[str, Any]) -> set[str]:
    return {
        value for value in (
            f"code:{_asset_name_key(variant.get('variant_code'))}",
            f"name:{_asset_name_key(variant.get('name'))}",
        ) if value not in {"code:", "name:"}
    }


def _asset_code_aliases(value: Any) -> set[str]:
    code = str(value or "").strip().upper()
    if not code:
        return set()
    aliases = {code}
    match = re.search(r"(?:^|-)(SC|R|P)\d+$", code)
    if match:
        aliases.add(code[match.start(1):])
    return aliases


def _asset_name_key(value: Any) -> str:
    return re.sub(r"[\s\-_/]+", "", str(value or "").strip()).casefold()


def _asset_type_key(asset: dict[str, Any]) -> str:
    return str(asset.get("asset_type") or asset.get("type") or "asset").strip().lower()


def _merge_character_costume_designs(
    asset: dict[str, Any],
    designs: list[dict[str, Any]],
) -> dict[str, Any]:
    if not designs:
        return asset
    merged = asset
    for design in designs:
        if not design.get("age_stage_code"):
            merged = _merge_character_costume_design(merged, design)
    stages = [dict(item) for item in merged.get("age_stages") or dict(merged.get("metadata") or {}).get("age_stages") or [] if isinstance(item, dict)]
    if not stages:
        return merged
    for design in designs:
        stage_code = str(design.get("age_stage_code") or "").strip()
        variant_code = str(design.get("costume_variant_code") or "").strip()
        if not stage_code:
            continue
        for stage_index, stage in enumerate(stages):
            if str(stage.get("stage_code") or "").strip() != stage_code:
                continue
            if not variant_code:
                stages[stage_index] = {**stage, **_scoped_costume_design_fields(design)}
                break
            variants = [dict(item) for item in stage.get("costume_variants") or [] if isinstance(item, dict)]
            for variant_index, variant in enumerate(variants):
                if str(variant.get("variant_code") or "").strip() == variant_code:
                    variants[variant_index] = {**variant, **_scoped_costume_design_fields(design)}
                    break
            stages[stage_index] = {**stage, "costume_variants": variants}
            break
    metadata = dict(merged.get("metadata") or {})
    metadata["age_stages"] = stages
    metadata["production_contexts"] = {
        str(item.get("context_key") or ""): {
            "context_schema_version": item.get("context_schema_version"),
            "source_type": item.get("source_type") or ("error" if item.get("error") else "agent"),
            "input_snapshot": item.get("input_snapshot") or {},
            "error": item.get("error"),
        }
        for item in designs
        if item.get("context_key")
    }
    return {**merged, "age_stages": stages, "metadata": metadata}


def _merge_asset_prompt_design(
    asset: dict[str, Any],
    design: dict[str, Any] | None,
    *,
    confirmation_required: bool = True,
) -> dict[str, Any]:
    if not isinstance(design, dict):
        return asset
    merged = dict(asset)
    metadata = dict(merged.get("metadata") or {})
    prompt_fields = (
        "prompt", "negative_prompt", "aspect_ratio", "view_prompts", "state_prompts",
        "spatial_bible", "material_bible", "brief_trace", "skill", "skill_version",
        "contract_version", "input_hash", "manual_review_items",
    )
    for key in prompt_fields:
        value = design.get(key)
        if value not in (None, "", [], {}):
            merged[key] = value
    metadata["prompt_design"] = {key: design.get(key) for key in prompt_fields if design.get(key) not in (None, "", [], {})}
    metadata["production_context"] = design.get("input_snapshot") or metadata.get("production_context") or {}
    metadata["prompt_lineage"] = {
        "skill": design.get("skill"),
        "skill_version": design.get("skill_version"),
        "contract_version": design.get("contract_version"),
        "input_hash": design.get("input_hash"),
        "brief_trace": design.get("brief_trace") or {},
    }
    if not str(design.get("error") or "").strip() and any(
        design.get(key) not in (None, "", [], {})
        for key in ("prompt", "view_prompts", "state_prompts", "spatial_bible", "material_bible")
    ):
        if confirmation_required:
            merged["status"] = "pending_confirmation"
            metadata["confirmation_status"] = "pending_confirmation"
        else:
            merged["status"] = "confirmed"
            metadata["confirmation_status"] = "confirmed"
        metadata["prompt_validity"] = "current"
        if design.get("input_hash"):
            metadata["prompt_generated_for_input_hash"] = design["input_hash"]
    merged["metadata"] = metadata
    return merged


def _scoped_costume_design_fields(design: dict[str, Any]) -> dict[str, Any]:
    return {
        key: value
        for key, value in {
            "prompt": design.get("prompt"),
            "negative_prompt": design.get("negative_prompt"),
            "aspect_ratio": design.get("aspect_ratio"),
            "material_layers": design.get("material_layers"),
            "character_apose": design.get("character_apose"),
            "output_spec": design.get("output_spec"),
            "generated_output_spec": design.get("generated_output_spec"),
            "generated_view_codes": design.get("generated_view_codes") or _generated_view_codes(design),
            "deferred_view_codes": design.get("deferred_view_codes"),
            "generation_strategy": design.get("generation_strategy"),
            "view_prompts": design.get("view_prompts"),
            "prompt_context_key": design.get("context_key"),
            "prompt_context_schema_version": design.get("context_schema_version"),
            "prompt_source_type": design.get("source_type"),
            "prompt_input_snapshot": design.get("input_snapshot"),
            "prompt_error": design.get("error"),
        }.items()
        if value not in (None, "", [], {})
    }


def _merge_character_costume_design(
    asset: dict[str, Any],
    design: dict[str, Any] | None,
) -> dict[str, Any]:
    if not design:
        return asset
    metadata = dict(asset.get("metadata") or {})
    costume_design = {
        key: design.get(key)
        for key in (
            "prompt",
            "negative_prompt",
            "aspect_ratio",
            "material_layers",
            "character_apose",
            "output_spec",
            "generated_output_spec",
            "generated_view_codes",
            "deferred_view_codes",
            "generation_strategy",
            "view_prompts",
            "error",
        )
        if design.get(key) not in (None, "", [], {})
    }
    metadata["costume_design"] = costume_design
    return {
        **asset,
        "prompt": design.get("prompt") or asset.get("prompt") or "",
        "negative_prompt": design.get("negative_prompt") or asset.get("negative_prompt") or "",
        "material_layers": design.get("material_layers") or asset.get("material_layers") or {},
        "character_apose": design.get("character_apose") or asset.get("character_apose") or {},
        "view_prompts": design.get("view_prompts") or asset.get("view_prompts") or [],
        "aspect_ratio": design.get("aspect_ratio") or asset.get("aspect_ratio") or "1:1",
        "generated_output_spec": design.get("generated_output_spec"),
        "generated_view_codes": design.get("generated_view_codes") or _generated_view_codes(design),
        "deferred_view_codes": design.get("deferred_view_codes") or [],
        "generation_strategy": design.get("generation_strategy"),
        "metadata": metadata,
    }


def _generated_view_codes(design: dict[str, Any]) -> list[str]:
    return list(dict.fromkeys(
        str(item.get("code") or item.get("view_code") or "").strip().upper()
        for item in _dict_list(design.get("view_prompts"))
        if str(item.get("code") or item.get("view_code") or "").strip()
    ))


_COSTUME_VIEW_DEFINITIONS: dict[str, tuple[str, str, str]] = {
    "A": ("正面全身 A-Pose", "正面平视，全身从头到脚完整呈现，人物居中，作为后续图位的主参考图。", "标准 A-Pose 站立，双臂从身体两侧自然展开约 30 度，手指放松，双脚与肩同宽，身体重心居中，神态自然。"),
    "B": ("侧面或 3/4 全身", "侧面或 45 度半侧全身构图，从头到脚完整呈现，保持图 A 的脸型、发型、体型、服装和比例。", "自然直立，四肢放松且不遮挡服装轮廓，头部与身体朝向一致，神态保持与图 A 连续。"),
    "C": ("背面全身", "背面平视全身构图，完整展示发型、服装和配饰背面，保持图 A 设计。", "背向镜头自然直立，双臂稍离躯干，手部放松，不遮挡服装背部结构和配饰。"),
    "D": ("面部与表情参考", "面部近景构图，清晰展示五官、发型、肤色和核心表情，保持图 A 一致。", "头部自然稳定，双唇自然闭合，目光与角色设定一致，避免夸张表情和遮挡五官。"),
    "E": ("服装与材质细节", "服装与配饰细节构图，清晰展示层次、纹理、接缝和关键材质。", "保持静止且服装自然垂落，手臂和配饰不遮挡需要展示的材质、纹理与结构细节。"),
}


def _normalize_costume_view_prompts(output: dict[str, Any], *, output_spec: str, asset: dict[str, Any] | None = None) -> list[dict[str, str]]:
    allowed_codes = ["A"] if output_spec == "A" else list(_COSTUME_VIEW_DEFINITIONS)
    raw = output.get("view_prompts")
    if isinstance(raw, dict):
        raw_items = [{"code": key, **value} for key, value in raw.items() if isinstance(value, dict)]
    else:
        raw_items = _dict_list(raw)
    by_code = {str(item.get("code") or item.get("view_code") or "").strip().upper(): item for item in raw_items}
    base_prompt = str(output.get("prompt") or "").strip()
    base_negative = str(output.get("negative_prompt") or "").strip()
    identity_prompt = _costume_identity_prompt(asset or {}, base_prompt)
    prompt_counts: dict[str, int] = {}
    for item in raw_items:
        item_prompt = str(item.get("prompt") or "").strip()
        if item_prompt:
            prompt_counts[item_prompt] = prompt_counts.get(item_prompt, 0) + 1
    normalized: list[dict[str, str]] = []
    for code in allowed_codes:
        title, default_description, default_pose = _COSTUME_VIEW_DEFINITIONS[code]
        source = by_code.get(code) or {}
        description = str(source.get("description") or default_description).strip()
        prompt = str(source.get("prompt") or "").strip()
        pose = str(source.get("pose") or source.get("angle") or "").strip()
        prompt_was_normalized = not _is_production_ready_costume_prompt(prompt, asset or {}) or prompt_counts.get(prompt, 0) > 1 or _is_fused_costume_view_prompt(prompt)
        pose_was_normalized = not pose or _is_fused_costume_view_prompt(pose)
        if pose_was_normalized:
            pose = default_pose
        if prompt_was_normalized:
            dependency = "必须引用已审核通过的图 A 作为角色一致性参考。" if code != "A" else "本图作为后续 B-E 图位的角色一致性主参考图。"
            prompt = "\n".join(part for part in (
                f"主体与身份：{identity_prompt}",
                f"图位 {code}：{description}",
                f"姿态要求：{pose}",
                "制作环境：中性纯色摄影棚背景，柔和均匀布光，人物和服装色彩准确，全身或细节边缘清晰，无环境叙事干扰。",
                dependency,
            ) if part)
        negative_prompt = str(source.get("negative_prompt") or base_negative).strip() or (
            "避免改变角色身份、年龄、骨相、五官、体型、发型、服装结构和材质；避免错误肢体、手指异常、重复身体、裁切、遮挡、透视畸变、模糊、低清晰度、文字、水印和标识。"
        )
        source_type = str(source.get("source_type") or "").strip()
        if prompt_was_normalized or pose_was_normalized or not source.get("description") or not source.get("reference_requirement") or not source.get("negative_prompt"):
            source_type = "backend_normalized"
        elif source_type not in {"agent", "backend_normalized", "frontend_normalized"}:
            source_type = "agent"
        normalized.append({
            "code": code,
            "title": str(source.get("title") or title).strip(),
            "prompt": prompt,
            "negative_prompt": negative_prompt,
            "pose": pose,
            "description": description,
            "reference_requirement": str(
                source.get("reference_requirement")
                or ("使用图 A 保持角色身份、骨相、发型、体型和服装一致。" if code != "A" else "无前置参考图；输出将作为 B-E 的主参考图。")
            ).strip(),
            "source_type": source_type,
        })
    return normalized


def _character_output_spec(character: dict[str, Any]) -> str:
    priority = str(character.get("priority") or "C").upper()
    return str(character.get("output_spec") or ("A-E" if priority in {"S", "A"} else "A")).upper()


def _costume_identity_prompt(asset: dict[str, Any], fallback: str) -> str:
    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else asset
    parts = [
        f"{asset.get('name')}角色定装" if asset.get("name") else "",
        _display_text(metadata.get("identity_setting") or metadata.get("dramatic_function")),
        _display_text(metadata.get("visual_features") or metadata.get("appearance_clues") or metadata.get("appearance")),
        _display_text(asset.get("description") or metadata.get("description")),
        _display_text(metadata.get("age_stage")),
        _display_text(metadata.get("current_costume")),
        _display_text(metadata.get("costume_plan") or metadata.get("costume_direction") or metadata.get("core_requirement")),
        _display_text(asset.get("material_layers") or metadata.get("material_layers")),
        _display_text(metadata.get("style") or metadata.get("visual_style") or metadata.get("quality_tags")),
        _display_text(metadata.get("continuity_constraints") or metadata.get("continuity_anchors") or metadata.get("forbidden_variations")),
        _display_text(metadata.get("visual_effects")),
        fallback if fallback and not _is_fused_costume_view_prompt(fallback) else "",
    ]
    identity = "，".join(dict.fromkeys(part for part in parts if part))
    return identity or (fallback if not _is_fused_costume_view_prompt(fallback) else "严格依据输入资产保持角色身份、年龄、体型、发型和当前装扮一致")


def _is_fused_costume_view_prompt(value: str) -> bool:
    markers = ("正面", "侧面", "45", "背面", "面部近景", "服装细节", "材质细节")
    codes = ("图位 A", "图位 B", "图位 C", "图位 D", "图位 E")
    return sum(marker in value for marker in markers) >= 3 or sum(code in value for code in codes) >= 2


def _is_production_ready_costume_prompt(value: str, asset: dict[str, Any]) -> bool:
    compact = "".join(value.split())
    if len(compact) < 80:
        return False
    name = str(asset.get("name") or "").strip()
    return not name or name in value


def _attach_asset_production_hints(
    asset: dict[str, Any],
    review_items: list[dict[str, Any]],
) -> dict[str, Any]:
    asset_code = str(asset.get("asset_code") or "").strip()
    hints = [item for item in review_items if asset_code and str(item.get("code") or "").strip() == asset_code]
    if not hints:
        return asset
    metadata = dict(asset.get("metadata") or {})
    metadata["production_hints"] = hints
    return {**asset, "production_hints": hints, "metadata": metadata}


def _match_costume_asset_suggestions(
    assets: list[dict[str, Any]],
    review_items: list[dict[str, Any]],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    props = [asset for asset in assets if asset.get("asset_type") == "prop"]
    character_codes = {
        _bare_asset_code(asset.get("asset_code"))
        for asset in assets
        if asset.get("asset_type") == "character"
    }
    updated_items: list[dict[str, Any]] = []
    links_by_character: dict[str, list[dict[str, Any]]] = {}

    for item in review_items:
        text = " ".join(str(item.get(key) or "") for key in ("item", "uncertainty", "detail", "suggestion"))
        character_code = _bare_asset_code(item.get("asset_code") or item.get("code"))
        if character_code not in character_codes or not _is_costume_production_suggestion(text):
            updated_items.append(item)
            continue
        match = _match_existing_costume_prop(text, props, character_code)
        if not match:
            updated_items.append({
                **item,
                "match_status": "not_found",
                "match_action": "create_candidate",
                "match_confidence": "0",
            })
            continue
        prop, status, confidence = match
        prop_code = str(prop.get("asset_code") or "").strip()
        prop_name = str(prop.get("name") or "").strip()
        action = "link_existing" if status == "exact" else "manual_confirm"
        suggestion = (
            f"已匹配现有服饰道具 {prop_code} {prop_name}；请将其关联到角色的具体年龄阶段和装扮变体，无需重复创建道具。"
            if status == "exact"
            else f"找到可能匹配的服饰道具 {prop_code} {prop_name}，需人工确认是否为同一资产。"
        )
        updated_items.append({
            **item,
            "suggestion": suggestion,
            "match_status": status,
            "matched_prop_code": prop_code,
            "matched_prop_name": prop_name,
            "match_action": action,
            "match_confidence": f"{confidence:.2f}",
        })
        if status == "exact":
            links_by_character.setdefault(character_code, []).append({
                "prop_code": prop_code,
                "prop_name": prop_name,
                "match_status": status,
                "match_action": action,
                "assignment_status": "needs_age_stage_assignment",
                "source_review_item": str(item.get("item") or "").strip(),
            })

    updated_assets: list[dict[str, Any]] = []
    for asset in assets:
        links = links_by_character.get(_bare_asset_code(asset.get("asset_code")), [])
        if asset.get("asset_type") != "character" or not links:
            updated_assets.append(asset)
            continue
        metadata = dict(asset.get("metadata") or {})
        existing = [link for link in metadata.get("costume_prop_links") or [] if isinstance(link, dict)]
        by_code = {str(link.get("prop_code") or ""): link for link in [*existing, *links]}
        metadata["costume_prop_links"] = list(by_code.values())
        updated_assets.append({**asset, "metadata": metadata})
    return updated_assets, updated_items


def _match_existing_costume_prop(
    text: str,
    props: list[dict[str, Any]],
    character_code: str,
) -> tuple[dict[str, Any], str, float] | None:
    referenced_codes = {_bare_asset_code(code) for code in re.findall(r"(?:[A-Z0-9]{1,8}-)?P\d{2,}", text, flags=re.I)}
    for prop in props:
        if _bare_asset_code(prop.get("asset_code")) in referenced_codes:
            return prop, "conflict" if _prop_owner_conflicts(prop, character_code) else "exact", 1.0
    normalized_text = _normalize_asset_match_text(text)
    for prop in props:
        name = _normalize_asset_match_text(prop.get("name"))
        if name and name in normalized_text:
            return prop, "conflict" if _prop_owner_conflicts(prop, character_code) else "exact", 0.98
    suggested_name = _normalize_asset_match_text(re.split(r"需|建议|应", text, maxsplit=1)[0])
    candidates = [
        (SequenceMatcher(None, suggested_name, _normalize_asset_match_text(prop.get("name"))).ratio(), prop)
        for prop in props
        if _normalize_asset_match_text(prop.get("name"))
    ]
    if candidates:
        confidence, prop = max(candidates, key=lambda pair: pair[0])
        if confidence >= 0.72:
            return prop, "conflict" if _prop_owner_conflicts(prop, character_code) else "probable", confidence
    return None


def _is_costume_production_suggestion(text: str) -> bool:
    return bool(re.search(r"定装|服装|服饰|婚纱|睡袍|长袍|外套|裙|鞋|帽|披风", text, flags=re.I))


def _prop_owner_conflicts(prop: dict[str, Any], character_code: str) -> bool:
    metadata = prop.get("metadata") if isinstance(prop.get("metadata"), dict) else prop
    owner = _bare_asset_code(metadata.get("owner_role_code") or metadata.get("owner_character_id"))
    return bool(owner and owner.startswith("R") and owner != character_code)


def _bare_asset_code(value: Any) -> str:
    return str(value or "").strip().upper().split("-")[-1]


def _normalize_asset_match_text(value: Any) -> str:
    return re.sub(r"[^0-9A-Za-z\u4e00-\u9fff]", "", str(value or "").lower())


def _standardize_production_hint(
    item: dict[str, Any],
    assets: list[dict[str, Any]],
) -> dict[str, Any]:
    code = str(item.get("asset_code") or item.get("code") or "").strip()
    asset = next((candidate for candidate in assets if code and str(candidate.get("asset_code") or "").strip() == code), None)
    asset_type = str((asset or {}).get("asset_type") or "project").strip()
    target = {
        "character": "角色定装与一致性",
        "prop": "道具母版与状态图",
        "scene": "场景母版与多视角",
    }.get(asset_type, "项目级生产约束")
    uncertainty = str(item.get("uncertainty") or item.get("item") or "").strip()
    suggestion = str(item.get("suggestion") or item.get("detail") or "").strip()
    issue_type = str(item.get("issue_type") or "production_uncertainty").strip()
    target_field = str(item.get("target_field") or _production_hint_target_field(item, asset_type)).strip()
    missing_reason = str(item.get("missing_reason") or "").strip()
    if not missing_reason:
        missing_reason = "relation_not_matched" if issue_type == "relation_unmatched" else "agent_not_output"
    return {
        **item,
        "code": code,
        "asset_code": code,
        "asset_name": str(item.get("asset_name") or (asset or {}).get("name") or "项目级").strip(),
        "issue_type": issue_type,
        "uncertainty": uncertainty,
        "suggestion": suggestion or uncertainty,
        "target": str(item.get("target") or target).strip(),
        "target_field": target_field,
        "missing_reason": missing_reason,
        "human_input": str(item.get("human_input") or "").strip(),
        "status": str(item.get("status") or "pending").strip(),
    }


def _missing_asset_review_items(
    assets: list[dict[str, Any]],
    existing_items: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    requirements = {
        "character": [
            ("role_type", ("role_type", "character_type", "role_type_label", "role"), "角色定位", "agent_missing_field", "agent_not_output", "结合剧情功能补充角色定位。"),
            ("scene_names", ("scene_names", "scene_ids", "related_scene_names", "related_scene_codes"), "使用场景", "relation_unmatched", "relation_not_matched", "根据剧本出场关系补充人物使用场景。"),
        ],
        "prop": [
            ("prop_type", ("prop_type",), "道具类型", "agent_missing_field", "agent_not_output", "补充服饰、配饰、武器、车辆等明确类型。"),
            ("appearance_material", ("appearance_material", "appearance", "material", "visual_anchor"), "外观与材质", "agent_missing_field", "agent_not_output", "补充外形、颜色、材质、纹理和磨损特征。"),
            ("state_plan", ("state_plan", "status_change"), "状态方案", "agent_missing_field", "agent_not_output", "补充道具在剧情前后或不同镜头中的状态变化。"),
            ("scene_names", ("scene_names", "scene_ids", "related_scene_names", "related_scene_codes"), "使用场景", "relation_unmatched", "relation_not_matched", "根据剧本出场场景补充使用场景；服饰需与角色所在场景匹配。"),
        ],
    }
    output: list[dict[str, Any]] = []
    for asset in assets:
        asset_type = str(asset.get("asset_type") or "").strip()
        code = str(asset.get("asset_code") or "").strip()
        name = str(asset.get("name") or code or "未命名资产").strip()
        for target_field, aliases, label, issue_type, missing_reason, suggestion in requirements.get(asset_type, []):
            if _asset_has_value(asset, aliases) or _has_review_for_field(existing_items, code, target_field, asset_type):
                continue
            reason_text = "Agent 未输出" if missing_reason == "agent_not_output" else "未从围读关系中匹配到"
            output.append({
                "item": f"{label}待补充",
                "detail": suggestion,
                "code": code,
                "asset_code": code,
                "asset_name": name,
                "issue_type": issue_type,
                "uncertainty": f"{reason_text}{label}",
                "suggestion": suggestion,
                "target": "角色定装与一致性" if asset_type == "character" else "道具母版与状态图",
                "target_field": target_field,
                "missing_reason": missing_reason,
                "status": "pending",
                "source": "asset_field_audit",
            })
        if (
            asset_type == "prop"
            and _prop_allows_optional_owner_hint(asset)
            and not _asset_has_value(asset, ("owner_character", "owner_character_id", "owner_role_code"))
            and not _has_review_for_field(existing_items, code, "owner_character", asset_type)
        ):
            output.append({
                "item": "所属角色可选补充",
                "detail": "如该服饰或配饰属于固定角色，可补充归属关系；未补充不影响资产确认和后续生产。",
                "code": code,
                "asset_code": code,
                "asset_name": name,
                "issue_type": "optional_relation_hint",
                "uncertainty": "未记录可选所属角色",
                "suggestion": "仅在存在明确穿戴或佩戴关系时补充所属角色。",
                "target": "道具母版与状态图",
                "target_field": "owner_character",
                "missing_reason": "optional_relation_not_matched",
                "requirement_level": "optional",
                "severity": "info",
                "status": "optional",
                "source": "asset_field_audit",
            })
    return output


def _normalize_prop_owner_review_items(
    review_items: list[dict[str, Any]],
    assets: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    assets_by_code = {
        alias: asset
        for asset in assets
        for alias in _asset_code_aliases(asset.get("asset_code"))
    }
    output: list[dict[str, Any]] = []
    for item in review_items:
        code = str(item.get("asset_code") or item.get("code") or "").strip()
        asset = next(
            (assets_by_code[alias] for alias in _asset_code_aliases(code) if alias in assets_by_code),
            None,
        )
        asset_type = str((asset or {}).get("asset_type") or "").strip()
        target_field = str(item.get("target_field") or _production_hint_target_field(item, asset_type)).strip()
        if target_field != "owner_character":
            output.append(item)
            continue
        if not asset or asset_type != "prop" or not _prop_allows_optional_owner_hint(asset):
            continue
        output.append({
            **item,
            "target_field": "owner_character",
            "issue_type": "optional_relation_hint",
            "missing_reason": "optional_relation_not_matched",
            "requirement_level": "optional",
            "severity": "info",
            "status": "optional",
        })
    return output


def _prop_allows_optional_owner_hint(asset: dict[str, Any]) -> bool:
    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}
    prop_type = str(asset.get("prop_type") or metadata.get("prop_type") or "").strip().lower()
    return prop_type in {
        "服饰", "配饰", "服装", "衣物", "首饰", "饰品",
        "costume", "clothing", "apparel", "garment", "wearable",
        "accessory", "accessories", "jewelry", "jewellery",
    }


def _asset_has_value(asset: dict[str, Any], aliases: tuple[str, ...]) -> bool:
    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}
    return any((asset.get(key) if key in asset else metadata.get(key)) not in (None, "", [], {}) for key in aliases)


def _has_review_for_field(
    items: list[dict[str, Any]],
    asset_code: str,
    target_field: str,
    asset_type: str,
) -> bool:
    for item in items:
        code = str(item.get("asset_code") or item.get("code") or "").strip()
        if code != asset_code:
            continue
        if str(item.get("target_field") or _production_hint_target_field(item, asset_type)).strip() == target_field:
            return True
    return False


def _production_hint_target_field(item: dict[str, Any], asset_type: str) -> str:
    text = " ".join(str(item.get(key) or "") for key in ("item", "uncertainty", "detail", "suggestion")).lower()
    field_terms = {
        "prop_type": ("道具类型", "子类型", "prop_type"),
        "owner_character": ("所属角色", "持有", "佩戴", "owner"),
        "appearance_material": ("外观", "材质", "纹理", "appearance", "material"),
        "state_plan": ("状态方案", "状态变化", "status_change"),
        "scene_names": ("使用场景", "所在场景", "出场场景", "scene_names"),
        "role_type": ("角色定位", "角色类型", "role_type"),
        "identity_setting": ("关系", "前妻", "妻子", "未婚妻", "家族", "identity"),
        "visual_effects": ("透明", "鬼魂", "幽灵", "灵体", "visual_effect"),
        "visual_features": ("外貌", "视觉特征", "发型", "体型", "appearance"),
        "costume_plan": ("服装方案", "服装", "服饰", "costume"),
    }
    allowed = {
        "character": {"role_type", "identity_setting", "visual_effects", "visual_features", "costume_plan", "scene_names"},
        "prop": {"prop_type", "owner_character", "appearance_material", "state_plan", "scene_names"},
    }.get(asset_type, set())
    for field, terms in field_terms.items():
        if field in allowed and any(term in text for term in terms):
            return field
    return ""


def _review_detail_with_asset(design: dict[str, Any], item: dict[str, Any]) -> str:
    character = str(design.get("character_name") or design.get("character_id") or "").strip()
    detail = str(item.get("detail") or item.get("message") or item.get("suggestion") or "").strip()
    return f"{character}：{detail}" if character and detail else detail or character


def _display_text(value: Any) -> str:
    if isinstance(value, list):
        return "、".join(str(item).strip() for item in value if str(item).strip())
    return str(value or "").strip()


def _categorized_normalized_inventory(inventory: dict[str, Any]) -> dict[str, list[dict[str, Any]]]:
    assets = _dict_list(inventory.get("assets"))
    return {
        "assets": assets,
        "characters": [item for item in assets if item.get("asset_type") == "character"],
        "scenes": [item for item in assets if item.get("asset_type") == "scene"],
        "props": [item for item in assets if item.get("asset_type") == "prop"],
    }


def _reading_revision_context(payload: dict[str, Any]) -> dict[str, Any]:
    script_source = dict(payload.get("script_source") or {})
    return {
        "episode_id": payload.get("episode_id") or script_source.get("episode_id"),
        "episode_code": payload.get("episode_code") or script_source.get("episode_code"),
        "script_id": payload.get("script_id") or script_source.get("script_id"),
        "script_version_id": payload.get("script_version_id") or script_source.get("script_version_id"),
    }


def _safe_int(value: Any, fallback: int) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return fallback


def _normalized_asset_inventory(payload: dict[str, Any], *, direct: bool = False) -> dict[str, Any]:
    value = payload if direct else payload.get("normalized_asset_inventory")
    if value in (None, {}):
        return {}
    if not isinstance(value, dict):
        raise ValueError("normalized_asset_inventory 必须是 AssetNormalization.v1 对象")
    return AssetNormalizationResultV1.model_validate(value).model_dump(mode="python")


def _reviewed_normalized_asset_inventory(payload: dict[str, Any]) -> dict[str, Any]:
    review_state = payload.get("asset_review_state")
    if not isinstance(review_state, dict) or str(review_state.get("status") or "") != "confirmed":
        return {}
    return _normalized_asset_inventory(payload)


def _reviewed_reading_report(payload: dict[str, Any]) -> dict[str, Any]:
    review_state = payload.get("reading_review_state")
    report = payload.get("reading_report")
    if (
        not isinstance(review_state, dict)
        or str(review_state.get("status") or "") != "confirmed"
        or not isinstance(report, dict)
    ):
        return {}
    return dict(report)


def _blocking_missing_asset_suggestions(output: dict[str, Any]) -> list[dict[str, Any]]:
    suggestions = output.get("missing_asset_suggestions")
    if not isinstance(suggestions, list):
        view = output.get("breakdown_view") if isinstance(output.get("breakdown_view"), dict) else {}
        suggestions = view.get("missing_asset_suggestions")
    return [
        dict(item)
        for item in suggestions or []
        if isinstance(item, dict)
        and (item.get("blocking") is True or str(item.get("priority") or "").upper() in {"S", "A"})
    ]


def _text_list(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, list):
        return [str(item).strip() for item in value if str(item).strip()]
    text = str(value).strip()
    return [text] if text else []


def _dict_list(value: Any) -> list[dict[str, Any]]:
    return [item for item in value if isinstance(item, dict)] if isinstance(value, list) else []


def _exception_message(exc: Exception) -> str:
    return str(exc) or type(exc).__name__


def _exception_output(exc: Exception) -> dict[str, Any]:
    if isinstance(exc, AgentExecutionError) and isinstance(exc.output, dict):
        return dict(exc.output)
    return {}


def _compact_prompt_response_diagnostics(value: Any) -> dict[str, Any]:
    raw = value if isinstance(value, dict) else {}
    keys = (
        "structured_output_mode",
        "finish_reason",
        "content_length",
        "elapsed_ms",
        "hermes_session_id",
        "content_source",
        "gateway_final_content",
        "session_recovery_error",
        "usage",
    )
    return {key: raw.get(key) for key in keys}


def _build_feedback_candidate(
    state: ScriptBreakdownWorkflowState,
    status: str,
    node_status: dict[str, str],
    errors: list[dict[str, Any]],
) -> dict[str, Any]:
    payload = dict(state.get("payload") or {})
    skills = _feedback_skills(node_status)
    return {
        "sample_type": "workflow_feedback_candidate",
        "workflow_type": "script_breakdown_workflow",
        "workflow_run_id": state.get("workflow_run_id"),
        "project_id": state.get("project_id"),
        "status": status,
        "trigger": payload.get("trigger"),
        "skills": skills,
        "primary_skill": "script-breakdown" if "script-breakdown" in skills else (skills[-1] if skills else "script-breakdown"),
        "agent_input": {
            "project_id": state.get("project_id"),
            "project_prefix": state.get("project_prefix"),
            "project_title": state.get("project_title"),
            "genre": state.get("genre"),
            "resolved_production_brief": dict(state.get("resolved_production_brief") or {}),
            "episode_num": state.get("episode_num"),
            # P3 采集层完备：显式记录 episode/script_version 血缘（配对键的作用域标识）。
            "episode_id": payload.get("episode_id"),
            "episode_code": payload.get("episode_code"),
            "script_version_id": payload.get("script_version_id"),
            "script_version_no": payload.get("script_version_no"),
            "single_step": payload.get("single_step"),
            "script_text_chars": len(str(state.get("script_text") or "")),
            "workflow": payload.get("workflow"),
            "workflow_phase": payload.get("workflow_phase"),
            "output_target": payload.get("output_target"),
            "script_segment_count": len(_dict_list(payload.get("script_segments"))),
        },
        "agent_outputs": {
            "reading_output": dict(state.get("reading_output") or {}),
            "breakdown_output": dict(state.get("breakdown_output") or {}),
            "normalized_asset_inventory": dict(state.get("normalized_asset_inventory") or {}),
            "relation_report": dict(state.get("relation_report") or {}),
            "review_output": dict(state.get("review_output") or {}),
        },
        "normalized_output": dict(state.get("normalized_view") or {}),
        "validation_report": dict(state.get("validation_report") or {}),
        "node_status": dict(node_status),
        "errors": list(errors),
        "warnings": list(state.get("warnings") or []),
        "training_ready": False,
        "next_action": "await_human_review_or_confirmation",
    }


def _feedback_skills(node_status: dict[str, str]) -> list[str]:
    node_skills = {
        "run_script_reading": "script-reading",
        "run_asset_extract": "asset-extract",
        "run_asset_costume_design": "text-to-image-prompt",
        "run_script_segmentation": "script-segmentation",
        "run_script_breakdown": "script-breakdown",
        "run_relation_check": "relation-check",
        "run_content_review": "content-compliance-review",
    }
    return list(
        dict.fromkeys(
            skill
            for node, status in node_status.items()
            if status in {"succeeded", "failed_non_blocking"}
            for skill in [node_skills.get(node)]
            if skill
        )
    )


graph = build_script_breakdown_workflow_graph()

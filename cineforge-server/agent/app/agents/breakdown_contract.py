from __future__ import annotations

from copy import deepcopy
import json
import re
from typing import Any, Literal

from pydantic import BaseModel, Field


IssueSeverity = Literal["error", "warning", "repair"]
ContractStatus = Literal["valid", "needs_review", "invalid", "repair_attempted"]


class ValidationIssue(BaseModel):
    path: str
    code: str
    message: str
    severity: IssueSeverity = "warning"


class BreakdownValidationReport(BaseModel):
    ok: bool
    status: ContractStatus
    blocking_errors: list[ValidationIssue] = Field(default_factory=list)
    warnings: list[ValidationIssue] = Field(default_factory=list)
    repairs: list[ValidationIssue] = Field(default_factory=list)
    manual_review_items: list[dict[str, Any]] = Field(default_factory=list)
    counts: dict[str, int] = Field(default_factory=dict)


DEFAULT_CAMERA = "中景，稳定镜头，保留角色动作和场景关系"


def attach_breakdown_contract(
    output: dict[str, Any],
    *,
    project_prefix: str | None = None,
    episode_num: int = 1,
    source: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Return a backwards-compatible agent output with a validated breakdown_view."""
    normalized = deepcopy(output)
    prefix = _project_prefix(project_prefix)
    normalized["script_segments"] = _normalize_segments_for_view(
        normalized.get("script_segments"),
        project_prefix=prefix,
        episode_num=episode_num,
    )
    normalized["storyboards"] = _normalize_storyboards_for_view(
        normalized.get("storyboards"),
        script_segments=normalized["script_segments"],
        project_prefix=prefix,
        episode_num=episode_num,
    )
    asset_values = list(normalized.get("assets") or [])
    uses_asset_normalization_v1 = (
        normalized.get("normalization_version") == "AssetNormalization.v1"
        and all(isinstance(item, dict) and item.get("normalization_version") == "AssetNormalization.v1" for item in asset_values)
    )
    assets = _normalize_assets_for_view(
        asset_values if uses_asset_normalization_v1 else [
            *asset_values,
            *list(normalized.get("characters") or []),
            *list(normalized.get("scenes") or []),
            *list(normalized.get("props") or []),
        ],
        storyboards=normalized["storyboards"],
        project_prefix=prefix,
    )
    normalized["assets"] = assets
    normalized["characters"] = [item for item in assets if item.get("asset_type") == "character"]
    normalized["scenes"] = [item for item in assets if item.get("asset_type") == "scene"]
    normalized["props"] = [item for item in assets if item.get("asset_type") == "prop"]
    normalized["asset_masters"] = _normalize_asset_masters(normalized.get("asset_masters"), assets=assets)
    normalized["storyboard_shots"] = _normalize_storyboard_shots(
        normalized.get("storyboard_shots"),
        storyboards=normalized["storyboards"],
        assets=assets,
    )
    normalized["coverage_checks"] = _normalize_coverage_checks(
        normalized.get("coverage_checks"),
        asset_masters=normalized["asset_masters"],
        storyboard_shots=normalized["storyboard_shots"],
    )
    normalized["scene_packages"] = _normalize_scene_packages(
        normalized.get("scene_packages"),
        asset_masters=normalized["asset_masters"],
        storyboard_shots=normalized["storyboard_shots"],
        coverage_checks=normalized["coverage_checks"],
    )

    report = validate_breakdown_view(normalized)
    if normalized.get("segmentation_only") is True or normalized.get("workflow_phase") == "script_segmentation":
        report.blocking_errors = [
            item for item in report.blocking_errors if item.code != "missing_storyboards"
        ]
        report.ok = not report.blocking_errors
        report.status = "needs_review" if report.warnings or blocking_manual_review_items(report.manual_review_items) else "valid"
    existing_manual_review = normalize_manual_review_items(normalized.get("manual_review_items"), source="agent")
    normalized["manual_review_items"] = _dedupe_manual_review_items([*existing_manual_review, *report.manual_review_items])
    report.manual_review_items = normalized["manual_review_items"]

    source_payload = {
        "source_label": normalized.get("source_label") or "agent",
        "llm_provider": normalized.get("llm_provider"),
        "llm_model": normalized.get("llm_model"),
        **(source or {}),
    }
    view = {
        "schema_version": "1.0.0",
        "normalization_version": normalized.get("normalization_version"),
        "project": {
            "project_prefix": prefix,
            "episode_num": episode_num,
        },
        "source": source_payload,
        "reading_report": normalized.get("reading_report") or {},
        "readthrough": list(normalized.get("readthrough") or []),
        "script_segments": normalized["script_segments"],
        "storyboards": normalized["storyboards"],
        "assets": normalized["assets"],
        "characters": normalized["characters"],
        "scenes": normalized["scenes"],
        "props": normalized["props"],
        "asset_masters": normalized["asset_masters"],
        "asset_prompt_designs": list(normalized.get("asset_prompt_designs") or []),
        "asset_prompt_processing_summary": list(normalized.get("asset_prompt_processing_summary") or []),
        "costume_designs": list(normalized.get("costume_designs") or []),
        "costume_processing_summary": list(normalized.get("costume_processing_summary") or []),
        "storyboard_shots": normalized["storyboard_shots"],
        "coverage_checks": normalized["coverage_checks"],
        "scene_packages": normalized["scene_packages"],
        "delivery_checklist": list(normalized.get("delivery_checklist") or []),
        "manual_review_items": normalized["manual_review_items"],
        "notes": _as_text_list(normalized.get("notes")),
        "workflow_phase": normalized.get("workflow_phase"),
        "segmentation_only": normalized.get("segmentation_only") is True,
        "validation_report": report.model_dump(mode="json"),
    }
    normalized["breakdown_view"] = view
    normalized["validation_report"] = view["validation_report"]
    return normalized


def validate_breakdown_view(output: dict[str, Any]) -> BreakdownValidationReport:
    errors: list[ValidationIssue] = []
    warnings: list[ValidationIssue] = []
    repairs: list[ValidationIssue] = []
    manual_review_items: list[dict[str, Any]] = []

    script_segments = _dict_list(output.get("script_segments"))
    storyboards = _dict_list(output.get("storyboards"))
    assets = _dict_list(output.get("assets"))

    if not script_segments:
        errors.append(_issue("script_segments", "missing_script_segments", "缺少脚本段，不能生成可追溯分镜。", "error"))
    if not storyboards:
        errors.append(_issue("storyboards", "missing_storyboards", "缺少分镜，不能进入业务展示。", "error"))

    segment_codes = {str(item.get("script_segment_code")) for item in script_segments if item.get("script_segment_code")}
    for index, segment in enumerate(script_segments, start=1):
        path = f"script_segments[{index - 1}]"
        if _is_repaired(segment, "script_segment_code"):
            repairs.append(_issue(f"{path}.script_segment_code", "generated_segment_code", "后端已生成脚本段编号。", "repair"))
        if not str(segment.get("source_text") or segment.get("summary") or "").strip():
            errors.append(_issue(path, "missing_segment_text", "脚本段缺少原文或摘要。", "error"))

    for index, storyboard in enumerate(storyboards, start=1):
        path = f"storyboards[{index - 1}]"
        if _is_repaired(storyboard, "storyboard_code"):
            repairs.append(_issue(f"{path}.storyboard_code", "generated_storyboard_code", "后端已生成分镜编号。", "repair"))
        if _is_repaired(storyboard, "script_segment_code"):
            repairs.append(_issue(f"{path}.script_segment_code", "auto_linked_segment", "后端已根据顺序自动关联脚本段。", "repair"))
        description = str(storyboard.get("description") or "").strip()
        if not description or "模型未返回画面描述" in description:
            errors.append(_issue(f"{path}.description", "missing_description", "分镜缺少有效画面描述。", "error"))
        segment_code = str(storyboard.get("script_segment_code") or "")
        if not segment_code or segment_code not in segment_codes:
            errors.append(_issue(f"{path}.script_segment_code", "invalid_segment_link", "分镜没有关联到有效脚本段。", "error"))
        camera = str(storyboard.get("camera") or "").strip()
        if not camera:
            warnings.append(_issue(f"{path}.camera", "missing_camera", "分镜缺少运镜描述。"))
            manual_review_items.append(_review_item(
                f"{storyboard.get('storyboard_code') or path} 缺少运镜描述。",
                source="contract",
                path=f"{path}.camera",
                code="missing_camera",
            ))
        elif camera == DEFAULT_CAMERA:
            warnings.append(_issue(f"{path}.camera", "default_camera", "分镜使用了默认运镜描述，需要人工确认。"))
        duration = _int_or_none(storyboard.get("duration_seconds"))
        if duration is None:
            errors.append(_issue(f"{path}.duration_seconds", "missing_duration", "分镜缺少时长。", "error"))
        elif duration < 10 or duration > 15:
            errors.append(_issue(f"{path}.duration_seconds", "duration_out_of_range", f"父分镜时长 {duration}s 超出 10-15 秒硬性范围。", "error"))
        if str(storyboard.get("shot_size") or "").strip() in {"", "未明确"}:
            errors.append(_issue(f"{path}.shot_size", "missing_shot_size", "分镜缺少明确的中文景别。", "error"))
        mirror_shots = _dict_list(storyboard.get("mirror_shots"))
        if mirror_shots and len(mirror_shots) < 2:
            errors.append(_issue(f"{path}.mirror_shots", "incomplete_mirror_sequence", "镜中分镜必须至少包含 A/B 两个完整内部镜头。", "error"))
        if len(mirror_shots) > 6:
            errors.append(_issue(f"{path}.mirror_shots", "mirror_sequence_too_dense", "镜中分镜最多包含 A-F 六个内部镜头。", "error"))
        mirror_duration_total = 0.0
        mirror_durations_complete = bool(mirror_shots)
        for mirror_index, mirror in enumerate(mirror_shots, start=1):
            mirror_path = f"{path}.mirror_shots[{mirror_index - 1}]"
            if _is_repaired(mirror, "id"):
                repairs.append(_issue(f"{mirror_path}.id", "generated_mirror_shot_id", "后端已生成镜中分镜 ID。", "repair"))
            if not str(mirror.get("description") or "").strip():
                errors.append(_issue(f"{mirror_path}.description", "missing_mirror_description", "镜中分镜缺少画面描述。", "error"))
            if not str(mirror.get("camera") or "").strip():
                errors.append(_issue(f"{mirror_path}.camera", "missing_mirror_camera", "镜中分镜缺少镜头执行描述。", "error"))
            if str(mirror.get("shot_size") or "").strip() in {"", "未明确"}:
                errors.append(_issue(f"{mirror_path}.shot_size", "missing_mirror_shot_size", "镜中分镜缺少明确的中文景别。", "error"))
            for field, code, message in (
                ("shot_function", "missing_mirror_shot_function", "镜中分镜缺少镜头职能。"),
                ("movement_reason", "missing_mirror_movement_reason", "镜中分镜缺少运镜动机。"),
                ("environmental_pressure", "missing_mirror_environment", "镜中分镜缺少环境压力细节。"),
                ("micro_action", "missing_mirror_micro_action", "镜中分镜缺少人物身体微动作。"),
                ("sound_or_motif", "missing_mirror_motif", "镜中分镜缺少声音或视觉母题。"),
            ):
                if not str(mirror.get(field) or "").strip():
                    errors.append(_issue(f"{mirror_path}.{field}", code, message, "error"))
            mirror_duration = _float_or_none(mirror.get("duration_seconds"))
            if mirror_duration is None or mirror_duration <= 0:
                mirror_durations_complete = False
                errors.append(_issue(f"{mirror_path}.duration_seconds", "missing_mirror_duration", "镜中分镜缺少有效时长。", "error"))
            else:
                mirror_duration_total += mirror_duration
        if mirror_durations_complete and duration is not None and abs(mirror_duration_total - duration) > 0.1:
            errors.append(_issue(
                f"{path}.mirror_shots",
                "mirror_duration_mismatch",
                f"镜中分镜总时长 {mirror_duration_total:g}s 与父分镜 {duration}s 不一致。",
                "error",
            ))
        parent_dialogue_lines = _dialogue_lines(storyboard.get("dialogue"))
        if mirror_shots and parent_dialogue_lines:
            child_dialogue_lines = [
                line
                for mirror in mirror_shots
                for line in _dialogue_lines(mirror.get("dialogue"))
            ]
            if not child_dialogue_lines:
                errors.append(_issue(
                    f"{path}.mirror_shots",
                    "mirror_dialogue_not_split",
                    "父分镜包含台词，但镜中分镜 A/B 未按完整原文台词行分配台词。",
                    "error",
                ))
            elif child_dialogue_lines != parent_dialogue_lines:
                errors.append(_issue(
                    f"{path}.mirror_shots",
                    "mirror_dialogue_coverage_mismatch",
                    "镜中分镜台词合并后与父分镜台词不一致，存在漏句、重复、乱序或改写。",
                    "error",
                ))

    seen_asset_codes: set[str] = set()
    for index, asset in enumerate(assets, start=1):
        path = f"assets[{index - 1}]"
        if _is_repaired(asset, "asset_code"):
            repairs.append(_issue(f"{path}.asset_code", "generated_asset_code", "后端已生成资产编号。", "repair"))
        if asset.get("asset_code") in seen_asset_codes:
            warnings.append(_issue(f"{path}.asset_code", "duplicate_asset_code", "资产编号重复，后续落库会去重。"))
        seen_asset_codes.add(str(asset.get("asset_code")))
        if asset.get("asset_type") not in {"character", "scene", "prop"}:
            warnings.append(_issue(f"{path}.asset_type", "unknown_asset_type", "资产类型无法识别，已按道具处理。"))
        if not str(asset.get("name") or "").strip():
            errors.append(_issue(f"{path}.name", "missing_asset_name", "资产缺少名称。", "error"))
        if not asset.get("related_storyboard_codes"):
            warnings.append(_issue(f"{path}.related_storyboard_codes", "missing_asset_storyboard_links", "资产没有关联分镜，需要人工确认。"))
            manual_review_items.append(_review_item(
                f"{asset.get('asset_code') or path} 没有关联分镜。",
                source="contract",
                path=f"{path}.related_storyboard_codes",
                code="missing_asset_storyboard_links",
            ))

    counts = {
        "script_segments": len(script_segments),
        "storyboards": len(storyboards),
        "assets": len(assets),
        "characters": len([item for item in assets if item.get("asset_type") == "character"]),
        "scenes": len([item for item in assets if item.get("asset_type") == "scene"]),
        "props": len([item for item in assets if item.get("asset_type") == "prop"]),
    }
    status: ContractStatus
    if errors:
        status = "invalid"
    elif warnings or blocking_manual_review_items(manual_review_items):
        status = "needs_review"
    elif repairs:
        status = "repair_attempted"
    else:
        status = "valid"
    return BreakdownValidationReport(
        ok=not errors,
        status=status,
        blocking_errors=errors,
        warnings=warnings,
        repairs=repairs,
        manual_review_items=_dedupe_manual_review_items(manual_review_items),
        counts=counts,
    )


def _normalize_segments_for_view(
    raw: Any,
    *,
    project_prefix: str,
    episode_num: int,
) -> list[dict[str, Any]]:
    segments: list[dict[str, Any]] = []
    for index, item in enumerate(_dict_list(raw), start=1):
        segment = dict(item)
        episode_code = _episode_code(segment.get("episode_code") or segment.get("episode_num"), episode_num)
        order_no = _safe_int(segment.get("order_no") or segment.get("order_num"), index)
        if not segment.get("script_segment_code"):
            segment["script_segment_code"] = f"{project_prefix}-{episode_code}-J{order_no:03d}"
            _mark_repaired(segment, "script_segment_code")
        segment["episode_code"] = episode_code
        segment["order_no"] = order_no
        segment.setdefault("title", f"脚本段 {order_no:03d}")
        segment.setdefault("source_text", segment.get("summary") or segment.get("description") or "")
        segment.setdefault("summary", str(segment.get("source_text") or "")[:240])
        segment.setdefault("story_function", "未明确")
        segment.setdefault("dominant_emotion", "未明确")
        segment.setdefault("rhythm", "未明确")
        segment.setdefault("viewpoint", "未明确")
        segments.append(segment)
    return segments


def _normalize_storyboards_for_view(
    raw: Any,
    *,
    script_segments: list[dict[str, Any]],
    project_prefix: str,
    episode_num: int,
) -> list[dict[str, Any]]:
    storyboards: list[dict[str, Any]] = []
    segments_by_order = {
        (_episode_code(item.get("episode_code"), episode_num), _safe_int(item.get("order_no"), index)): item
        for index, item in enumerate(script_segments, start=1)
    }
    segments_by_code = {
        str(item.get("script_segment_code") or "").strip(): item
        for item in script_segments
        if str(item.get("script_segment_code") or "").strip()
    }
    segment_order_by_code = {
        code: _safe_int(segment.get("order_no"), index)
        for index, (code, segment) in enumerate(segments_by_code.items(), start=1)
    }
    first_segment = script_segments[0] if script_segments else {}
    raw_storyboards = list(enumerate(_dict_list(raw)))
    raw_storyboards.sort(key=lambda pair: (
        segment_order_by_code.get(str(pair[1].get("script_segment_code") or "").strip(), len(script_segments) + 1),
        _safe_int(pair[1].get("segment_order_num") or pair[1].get("order_num") or pair[1].get("order_no"), pair[0] + 1),
        pair[0],
    ))
    for index, (_, item) in enumerate(raw_storyboards, start=1):
        storyboard = dict(item)
        episode_code = _episode_code(storyboard.get("episode_code") or storyboard.get("episode_num"), episode_num)
        order_no = index
        storyboard["episode_code"] = episode_code
        storyboard["episode_num"] = _episode_num(episode_code)
        storyboard["order_no"] = order_no
        storyboard["order_num"] = order_no
        if not storyboard.get("storyboard_code"):
            storyboard["storyboard_code"] = f"{project_prefix}-{episode_code}-F{order_no:03d}"
            _mark_repaired(storyboard, "storyboard_code")
        storyboard_code = str(storyboard.get("storyboard_code") or "").strip()
        agent_shot_no = str(storyboard.get("agent_shot_no") or storyboard.get("shot_no") or "").strip()
        if agent_shot_no:
            storyboard["agent_shot_no"] = agent_shot_no
        storyboard.pop("shot_no", None)
        raw_shot_size = str(storyboard.get("agent_shot_size") or storyboard.get("shot_size") or "").strip()
        if raw_shot_size:
            storyboard["agent_shot_size"] = raw_shot_size
        storyboard["shot_size"] = _normalize_shot_size_cn(storyboard.get("shot_size"))
        if storyboard.get("keyframe_code"):
            storyboard["agent_keyframe_code"] = storyboard.get("keyframe_code")
        if storyboard.get("video_code"):
            storyboard["agent_video_code"] = storyboard.get("video_code")
        storyboard["keyframe_code"] = f"{storyboard_code}-KF01"
        storyboard["video_code"] = f"{storyboard_code}-VID01"
        if not storyboard.get("script_segment_code"):
            segment = segments_by_order.get((episode_code, order_no)) or first_segment
            if segment.get("script_segment_code"):
                storyboard["script_segment_code"] = segment["script_segment_code"]
                _mark_repaired(storyboard, "script_segment_code")
        linked_segment = segments_by_code.get(str(storyboard.get("script_segment_code") or "").strip())
        if linked_segment:
            storyboard["script_segment_title"] = linked_segment.get("title")
            storyboard["script_segment_source_text"] = linked_segment.get("source_text")
            storyboard["script_segment_estimated_duration_seconds"] = linked_segment.get("estimated_duration_seconds") or linked_segment.get("duration_seconds")
        storyboard.setdefault("description", "")
        storyboard.setdefault("camera", "")
        storyboard["characters"] = _as_text_list(storyboard.get("characters"))
        storyboard["keyframes"] = _dict_list(storyboard.get("keyframes"))
        storyboard["mirror_shots"] = _normalize_mirror_shots(storyboard.get("mirror_shots"))
        storyboards.append(storyboard)
    return storyboards


def _normalize_assets_for_view(raw: list[Any], *, storyboards: list[dict[str, Any]], project_prefix: str | None = None) -> list[dict[str, Any]]:
    if raw and all(
        isinstance(item, dict) and item.get("normalization_version") == "AssetNormalization.v1"
        for item in raw
    ):
        return [deepcopy(item) for item in raw]
    assets: list[dict[str, Any]] = []
    seen: dict[tuple[str, str, str], int] = {}
    counters = {"character": 0, "scene": 0, "prop": 0}
    occupied: set[str] = set()
    for item in raw:
        if not isinstance(item, dict):
            continue
        asset_type = _asset_type(item.get("asset_type") or item.get("type"))
        formal_code = _canonical_contract_asset_code(
            item.get("asset_code"),
            project_prefix=project_prefix,
            asset_type=asset_type,
        )
        if not formal_code:
            continue
        occupied.add(formal_code)
        sequence = int(re.search(r"(\d+)$", formal_code).group(1))
        counters[asset_type] = max(counters[asset_type], sequence)
    for item in raw:
        if not isinstance(item, dict):
            continue
        asset = dict(item)
        asset_type = _asset_type(asset.get("asset_type") or asset.get("type"))
        name = str(asset.get("name") or asset.get("role_name") or asset.get("scene_name") or asset.get("prop_name") or "").strip()
        if not name:
            continue
        explicit_code = str(asset.get("asset_code") or "").strip()
        key = (asset_type, "code", explicit_code.upper()) if explicit_code else (asset_type, "name", _asset_name_key(name))
        asset["asset_type"] = asset_type
        asset["type"] = asset_type
        asset["name"] = name
        if explicit_code:
            asset["agent_asset_code"] = explicit_code
        asset.setdefault("status", "draft")
        asset.setdefault("metadata", {})
        relation_evidence = _relation_evidence(asset, storyboards)
        related_storyboard_codes = _as_text_list(asset.get("related_storyboard_codes") or asset.get("storyboard_codes"))
        if not related_storyboard_codes:
            related_storyboard_codes = list(dict.fromkeys(item["storyboard_code"] for item in relation_evidence if item.get("storyboard_code")))
        asset["related_storyboard_codes"] = related_storyboard_codes
        related_script_segment_codes = _as_text_list(asset.get("related_script_segment_codes") or asset.get("script_segment_codes"))
        if not related_script_segment_codes:
            related_script_segment_codes = _related_script_segments(asset, storyboards)
        asset["related_script_segment_codes"] = related_script_segment_codes
        if relation_evidence and not asset.get("relation_evidence"):
            asset["relation_evidence"] = relation_evidence
        if relation_evidence and not asset.get("relation_confidence"):
            asset["relation_confidence"] = _relation_confidence(relation_evidence)
        if key in seen:
            assets[seen[key]] = _merge_asset(assets[seen[key]], asset)
            continue
        formal_code = _canonical_contract_asset_code(
            explicit_code,
            project_prefix=project_prefix,
            asset_type=asset_type,
        )
        if formal_code and formal_code not in {item.get("asset_code") for item in assets}:
            generated_code = formal_code
        else:
            while True:
                counters[asset_type] += 1
                generated_code = _asset_code(asset_type, counters[asset_type], project_prefix=project_prefix)
                if generated_code not in occupied:
                    break
            occupied.add(generated_code)
        if explicit_code and explicit_code != generated_code:
            asset["agent_asset_code"] = explicit_code
        asset["asset_code"] = generated_code
        if asset.get("asset_code_source") != "backend":
            asset["asset_code_source"] = "agent_normalized"
        if explicit_code != generated_code:
            _mark_repaired(asset, "asset_code")
        seen[key] = len(assets)
        assets.append(asset)
    return assets


def _normalize_asset_masters(raw: Any, *, assets: list[dict[str, Any]]) -> list[dict[str, Any]]:
    raw_masters = _dict_list(raw)
    if raw_masters:
        return [_normalize_asset_master(item, fallback_asset=None) for item in raw_masters]
    return [_normalize_asset_master(asset, fallback_asset=asset) for asset in assets]


def _normalize_asset_master(item: dict[str, Any], *, fallback_asset: dict[str, Any] | None) -> dict[str, Any]:
    asset = fallback_asset or item
    metadata = asset.get("metadata") if isinstance(asset.get("metadata"), dict) else {}
    asset_type = _asset_type(_first_text(item, asset, metadata, keys=("asset_type", "type")))
    asset_code = _first_text(item, asset, metadata, keys=("asset_code", "scene_code", "role_code", "prop_code"))
    name = _first_text(item, asset, metadata, keys=("name", "scene_name", "role_name", "prop_name"))
    priority = _priority_value(_first_text(item, asset, metadata, keys=("priority", "visual_priority")))
    visual_goal = _first_text(
        item,
        asset,
        metadata,
        keys=("visual_goal", "visual_target", "visual_direction", "prompt_usage", "core_requirement", "description"),
    )
    if asset_type == "scene":
        asset_role = _first_text(item, asset, metadata, keys=("asset_role",)) or "scene_master"
    elif asset_type == "character":
        asset_role = _first_text(item, asset, metadata, keys=("asset_role",)) or "character_master"
    else:
        asset_role = _first_text(item, asset, metadata, keys=("asset_role",)) or "prop_master"
    return {
        **item,
        "asset_code": asset_code,
        "asset_type": asset_type,
        "asset_role": asset_role,
        "name": name,
        "priority": priority,
        "visual_goal": visual_goal,
        "covered_zones": _first_text_list(item, asset, metadata, keys=("covered_zones", "spatial_zones", "zones")),
        "camera_coverage": _first_text_list(item, asset, metadata, keys=("camera_coverage", "camera_direction", "shot_size")),
        "key_props": _first_text_list(item, asset, metadata, keys=("key_props", "props", "prop_names")),
        "lighting_states": _first_text_list(item, asset, metadata, keys=("lighting_states", "lighting", "light")),
        "mood_tags": _first_text_list(item, asset, metadata, keys=("mood_tags", "mood", "atmosphere", "emotional_arc")),
        "style_rules": _first_text(item, asset, metadata, keys=("style_rules", "style", "forbidden_variations", "continuity_anchors")),
        "usage": _first_text(item, asset, metadata, keys=("usage", "prompt_usage", "function", "story_function")),
        "evidence": _first_text(item, asset, metadata, keys=("evidence", "source_text", "relation_evidence")),
    }


def _normalize_storyboard_shots(
    raw: Any,
    *,
    storyboards: list[dict[str, Any]],
    assets: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    raw_shots = _dict_list(raw)
    if raw_shots:
        return [_normalize_storyboard_shot(item, index=index, storyboards=storyboards, assets=assets) for index, item in enumerate(raw_shots, start=1)]
    return [_normalize_storyboard_shot(storyboard, index=index, storyboards=storyboards, assets=assets) for index, storyboard in enumerate(storyboards, start=1)]


def _normalize_storyboard_shot(
    item: dict[str, Any],
    *,
    index: int,
    storyboards: list[dict[str, Any]],
    assets: list[dict[str, Any]],
) -> dict[str, Any]:
    storyboard_code = str(item.get("storyboard_code") or "").strip()
    storyboard = _find_by_code(storyboards, "storyboard_code", storyboard_code) or item
    scene_asset = _match_scene_asset(item, storyboard, assets)
    scene_code = _first_text(item, storyboard, scene_asset or {}, keys=("scene_code", "asset_code"))
    scene_name = _first_text(item, storyboard, scene_asset or {}, keys=("scene_name", "space", "name"))
    character_refs = _as_text_list(item.get("role_codes") or item.get("character_codes") or item.get("characters") or storyboard.get("characters"))
    role_codes = _match_asset_refs(character_refs, assets, "character")
    prop_refs = _as_text_list(item.get("prop_codes") or item.get("visible_props") or item.get("props") or item.get("key_props"))
    prop_codes = _match_asset_refs(prop_refs, assets, "prop")
    shot_code = str(item.get("shot_code") or item.get("shot_id") or "").strip() or f"SH{index:03d}"
    required_zones = _first_text_list(item, storyboard, keys=("required_zones", "spatial_zones", "zones"))
    if not required_zones:
        required_zones = _as_text_list(storyboard.get("space"))
    camera_direction = _first_text(item, storyboard, keys=("camera_direction", "camera"))
    shot_size = _first_text(item, storyboard, keys=("shot_size", "frame", "framing"))
    action = _first_text(item, storyboard, keys=("action", "description", "summary"))
    mood = _first_text(item, storyboard, keys=("mood", "emotion", "dominant_emotion"))
    prompt = _first_text(item, storyboard, keys=("storyboard_prompt", "prompt", "keyframe_prompt"))
    if not prompt:
        prompt_parts = [shot_size, camera_direction, action, mood]
        prompt = "；".join(part for part in prompt_parts if part)
    return {
        **item,
        "shot_code": shot_code,
        "storyboard_code": storyboard_code or str(storyboard.get("storyboard_code") or "").strip(),
        "script_segment_code": _first_text(item, storyboard, keys=("script_segment_code",)),
        "scene_code": scene_code,
        "scene_name": scene_name,
        "shot_function": _first_text(item, storyboard, keys=("shot_function", "function", "task_type")) or "未明确",
        "shot_size": shot_size,
        "camera_direction": camera_direction,
        "required_zones": required_zones,
        "visible_props": prop_refs,
        "role_codes": role_codes,
        "prop_codes": prop_codes,
        "mood": mood,
        "rhythm": _first_text(item, storyboard, keys=("rhythm", "editing_rhythm")),
        "action": action,
        "dialogue": _first_text(item, storyboard, keys=("dialogue",)),
        "duration_seconds": _safe_int(item.get("duration_seconds") or storyboard.get("duration_seconds"), 0),
        "environmental_pressure": _first_text(item, storyboard, keys=("environmental_pressure", "lighting", "atmosphere")),
        "micro_action": _first_text(item, storyboard, keys=("micro_action", "body_action")),
        "sound_or_motif": _first_text(item, storyboard, keys=("sound_or_motif", "sound", "visual_motif")),
        "storyboard_prompt": prompt,
    }


def _normalize_coverage_checks(
    raw: Any,
    *,
    asset_masters: list[dict[str, Any]],
    storyboard_shots: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    raw_checks = _dict_list(raw)
    if raw_checks:
        return [_normalize_coverage_check(item, index=index, asset_masters=asset_masters, storyboard_shots=storyboard_shots) for index, item in enumerate(raw_checks, start=1)]
    return [_build_coverage_check(shot, index=index, asset_masters=asset_masters) for index, shot in enumerate(storyboard_shots, start=1)]


def _normalize_coverage_check(
    item: dict[str, Any],
    *,
    index: int,
    asset_masters: list[dict[str, Any]],
    storyboard_shots: list[dict[str, Any]],
) -> dict[str, Any]:
    shot_code = str(item.get("shot_code") or "").strip()
    shot = _find_by_code(storyboard_shots, "shot_code", shot_code) or item
    built = _build_coverage_check(shot, index=index, asset_masters=asset_masters)
    return {**built, **item, "missing_requirements": _as_text_list(item.get("missing_requirements")) or built["missing_requirements"]}


def _build_coverage_check(
    shot: dict[str, Any],
    *,
    index: int,
    asset_masters: list[dict[str, Any]],
) -> dict[str, Any]:
    scene = _find_scene_master(shot, asset_masters)
    scene_code = str(shot.get("scene_code") or (scene or {}).get("asset_code") or "").strip()
    scene_name = str(shot.get("scene_name") or (scene or {}).get("name") or "").strip()
    missing: list[str] = []
    status = "covered"
    if not scene:
        status = "needs_new_scene"
        missing.append("缺少可绑定的场景母版")
    else:
        covered_zones = _as_text_list(scene.get("covered_zones"))
        camera_coverage = _as_text_list(scene.get("camera_coverage"))
        mood_tags = _as_text_list(scene.get("mood_tags")) + _as_text_list(scene.get("lighting_states"))
        for zone in _as_text_list(shot.get("required_zones")):
            if covered_zones and not _any_text_match(zone, covered_zones):
                missing.append(f"缺少空间区域：{zone}")
        camera_direction = str(shot.get("camera_direction") or "").strip()
        if camera_direction and camera_coverage and not _any_text_match(camera_direction, camera_coverage):
            missing.append(f"缺少镜头方位：{camera_direction}")
        mood = str(shot.get("mood") or "").strip()
        needs_variant = bool(mood and mood_tags and not _any_text_match(mood, mood_tags))
        if missing:
            status = "needs_view"
        elif needs_variant:
            status = "needs_variant"
            missing.append(f"缺少情绪/光线变体：{mood}")
    suggested_asset: dict[str, Any] = {}
    if status != "covered":
        view_code = f"{scene_code or 'SCENE'}-V{index:02d}"
        suggested_asset = {
            "asset_type": "scene_view" if status in {"needs_view", "needs_variant"} else "scene_master",
            "parent_scene_code": scene_code,
            "view_code": view_code,
            "view_name": f"{scene_name or '新场景'}补充视角",
            "purpose": f"支持 {shot.get('storyboard_code') or shot.get('shot_code') or '分镜'} 的故事板和图生视频",
        }
    return {
        "shot_code": str(shot.get("shot_code") or "").strip(),
        "storyboard_code": str(shot.get("storyboard_code") or "").strip(),
        "scene_code": scene_code,
        "scene_name": scene_name,
        "coverage_status": status,
        "missing_requirements": missing,
        "suggested_asset": suggested_asset,
    }


def _normalize_scene_packages(
    raw: Any,
    *,
    asset_masters: list[dict[str, Any]],
    storyboard_shots: list[dict[str, Any]],
    coverage_checks: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    raw_packages = _dict_list(raw)
    if raw_packages:
        return raw_packages
    packages: list[dict[str, Any]] = []
    grouped: dict[str, list[dict[str, Any]]] = {}
    for shot in storyboard_shots:
        key = str(shot.get("scene_code") or shot.get("scene_name") or "未绑定场景").strip()
        grouped.setdefault(key, []).append(shot)
    checks_by_shot = {str(item.get("shot_code") or ""): item for item in coverage_checks}
    for key, shots in grouped.items():
        first = shots[0]
        scene = _find_scene_master(first, asset_masters)
        scene_code = str(first.get("scene_code") or (scene or {}).get("asset_code") or key).strip()
        scene_name = str(first.get("scene_name") or (scene or {}).get("name") or key).strip()
        checks = [checks_by_shot.get(str(shot.get("shot_code") or "")) for shot in shots]
        blocking_items = [
            missing
            for check in checks
            if isinstance(check, dict) and check.get("coverage_status") != "covered"
            for missing in _as_text_list(check.get("missing_requirements"))
        ]
        required_scene_views = [scene_code] if scene_code else []
        required_scene_views.extend(
            str((check.get("suggested_asset") or {}).get("view_code"))
            for check in checks
            if isinstance(check, dict) and isinstance(check.get("suggested_asset"), dict) and (check.get("suggested_asset") or {}).get("view_code")
        )
        tasks = []
        for view_code in required_scene_views[1:]:
            tasks.append({"task_type": "scene_view", "title": f"{view_code} 补充场景视角"})
        for shot in shots:
            label = str(shot.get("shot_code") or shot.get("storyboard_code") or "分镜").strip()
            tasks.append({"task_type": "keyframe", "title": f"{label} 关键帧"})
            tasks.append({"task_type": "image_to_video", "title": f"{label} 图生视频"})
        packages.append(
            {
                "scene_code": scene_code,
                "scene_name": scene_name,
                "priority": _priority_value((scene or {}).get("priority")),
                "storyboard_shots": [str(shot.get("shot_code") or shot.get("storyboard_code") or "").strip() for shot in shots if shot.get("shot_code") or shot.get("storyboard_code")],
                "storyboard_codes": [str(shot.get("storyboard_code") or "").strip() for shot in shots if shot.get("storyboard_code")],
                "required_roles": list(dict.fromkeys(code for shot in shots for code in _as_text_list(shot.get("role_codes")))),
                "required_props": list(dict.fromkeys(code for shot in shots for code in _as_text_list(shot.get("prop_codes")))),
                "required_scene_views": list(dict.fromkeys(item for item in required_scene_views if item)),
                "coverage_status": "needs_assets" if blocking_items else "ready_for_assignment",
                "blocking_items": list(dict.fromkeys(blocking_items)),
                "tasks": tasks,
            }
        )
    return packages


def _merge_asset(existing: dict[str, Any], incoming: dict[str, Any]) -> dict[str, Any]:
    merged = dict(existing)
    for field in ("name", "description", "status", "source_label"):
        merged[field] = _prefer_text(merged.get(field), incoming.get(field))
    for field in ("related_storyboard_codes", "related_script_segment_codes"):
        merged[field] = list(dict.fromkeys([*_as_text_list(merged.get(field)), *_as_text_list(incoming.get(field))]))
    if isinstance(merged.get("metadata"), dict) or isinstance(incoming.get("metadata"), dict):
        merged["metadata"] = _merge_metadata(
            merged.get("metadata") if isinstance(merged.get("metadata"), dict) else {},
            incoming.get("metadata") if isinstance(incoming.get("metadata"), dict) else {},
        )
    if isinstance(incoming.get("relation_evidence"), list):
        existing_evidence = _dict_list(merged.get("relation_evidence"))
        incoming_evidence = _dict_list(incoming.get("relation_evidence"))
        merged["relation_evidence"] = _dedupe_relation_evidence([*existing_evidence, *incoming_evidence])
    if incoming.get("agent_asset_code") and not merged.get("agent_asset_code"):
        merged["agent_asset_code"] = incoming.get("agent_asset_code")
    merged["relation_confidence"] = _stronger_confidence(merged.get("relation_confidence"), incoming.get("relation_confidence"))
    return merged


def _merge_metadata(existing: dict[str, Any], incoming: dict[str, Any]) -> dict[str, Any]:
    merged = dict(existing)
    for key, value in incoming.items():
        if isinstance(value, list):
            merged[key] = list(dict.fromkeys([*_as_text_list(merged.get(key)), *_as_text_list(value)]))
        elif _clean_metadata_value(value) and len(_clean_metadata_value(value)) > len(_clean_metadata_value(merged.get(key))):
            merged[key] = value
        elif key not in merged:
            merged[key] = value
    return merged


def _dedupe_relation_evidence(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
    deduped: list[dict[str, Any]] = []
    seen: set[tuple[str, str, str]] = set()
    for item in items:
        key = (
            str(item.get("storyboard_code") or ""),
            str(item.get("script_segment_code") or ""),
            str(item.get("source_field") or ""),
        )
        if key in seen:
            continue
        seen.add(key)
        deduped.append(item)
    return deduped


def _prefer_text(current: Any, candidate: Any) -> Any:
    current_text = str(current or "").strip()
    candidate_text = str(candidate or "").strip()
    if not current_text:
        return candidate
    if not candidate_text:
        return current
    return candidate if len(candidate_text) > len(current_text) else current


def _stronger_confidence(current: Any, candidate: Any) -> str:
    rank = {"": 0, "low": 1, "medium": 2, "high": 3}
    current_text = str(current or "").strip()
    candidate_text = str(candidate or "").strip()
    return candidate_text if rank.get(candidate_text, 0) > rank.get(current_text, 0) else current_text


def _clean_metadata_value(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, (dict, list)):
        return json.dumps(value, ensure_ascii=False)
    return str(value).strip()


def _asset_name_key(name: str) -> str:
    return str(name or "").strip().replace("（", "(").replace("）", ")").upper()


def _normalize_mirror_shots(raw: Any) -> list[dict[str, Any]]:
    shots: list[dict[str, Any]] = []
    for index, item in enumerate(_dict_list(raw), start=1):
        shot = dict(item)
        if not shot.get("id") and shot.get("label"):
            shot["id"] = shot.get("label")
        if not shot.get("id"):
            shot["id"] = chr(ord("A") + index - 1)
            _mark_repaired(shot, "id")
        shot.pop("label", None)
        shot.setdefault("description", "")
        shot.setdefault("dialogue", "")
        shot.setdefault("camera", "")
        shot["characters"] = _as_text_list(shot.get("characters") or shot.get("role_codes"))
        shot["shot_size"] = _normalize_shot_size_cn(shot.get("shot_size")) if shot.get("shot_size") else ""
        duration = shot.get("duration_seconds")
        if duration not in (None, ""):
            try:
                shot["duration_seconds"] = float(duration)
            except (TypeError, ValueError):
                shot["duration_seconds"] = None
        else:
            shot["duration_seconds"] = None
        shots.append(shot)
    return shots


def _related_storyboards(asset: dict[str, Any], storyboards: list[dict[str, Any]]) -> list[str]:
    explicit = _as_text_list(asset.get("related_storyboard_codes") or asset.get("storyboard_codes"))
    if explicit:
        return explicit
    return list(dict.fromkeys(item["storyboard_code"] for item in _relation_evidence(asset, storyboards) if item.get("storyboard_code")))


def _related_script_segments(asset: dict[str, Any], storyboards: list[dict[str, Any]]) -> list[str]:
    explicit = _as_text_list(asset.get("related_script_segment_codes") or asset.get("script_segment_codes"))
    if explicit:
        return explicit
    related_storyboards = set(_as_text_list(asset.get("related_storyboard_codes")))
    return list(
        dict.fromkeys(
            str(storyboard.get("script_segment_code"))
            for storyboard in storyboards
            if storyboard.get("script_segment_code") and storyboard.get("storyboard_code") in related_storyboards
        )
    )


def _relation_evidence(asset: dict[str, Any], storyboards: list[dict[str, Any]]) -> list[dict[str, Any]]:
    asset_type = _asset_type(asset.get("asset_type") or asset.get("type"))
    aliases = _asset_aliases(asset)
    if not aliases:
        return []
    evidence: list[dict[str, Any]] = []
    seen_codes: set[str] = set()
    for storyboard in storyboards:
        storyboard_code = str(storyboard.get("storyboard_code") or "").strip()
        if not storyboard_code:
            continue
        match = _storyboard_asset_match(asset_type, aliases, storyboard)
        if not match:
            continue
        if storyboard_code in seen_codes:
            continue
        seen_codes.add(storyboard_code)
        evidence.append(
            {
                "storyboard_code": storyboard_code,
                "script_segment_code": str(storyboard.get("script_segment_code") or "").strip(),
                "match_type": match["match_type"],
                "matched_text": match["matched_text"],
                "source_field": match["source_field"],
                "confidence": match["confidence"],
            }
        )
    return evidence


def _storyboard_asset_match(asset_type: str, aliases: list[str], storyboard: dict[str, Any]) -> dict[str, Any] | None:
    if asset_type == "character":
        for character in _as_text_list(storyboard.get("characters")):
            matched = _matching_alias(aliases, character)
            if matched:
                return {"match_type": "character_name", "matched_text": matched, "source_field": "characters", "confidence": "high"}
        for field in ("description", "dialogue", "title"):
            matched = _matching_alias(aliases, str(storyboard.get(field) or ""))
            if matched:
                return {"match_type": "character_text", "matched_text": matched, "source_field": field, "confidence": "medium"}
    elif asset_type == "scene":
        for field in ("scene_name", "space", "title", "description"):
            matched = _matching_alias(aliases, str(storyboard.get(field) or ""))
            if matched:
                confidence = "high" if field in {"scene_name", "space"} else "medium"
                return {"match_type": "scene_text", "matched_text": matched, "source_field": field, "confidence": confidence}
    else:
        for field in ("description", "dialogue", "title", "scene_name", "space"):
            matched = _matching_alias(aliases, str(storyboard.get(field) or ""))
            if matched:
                return {"match_type": "prop_text", "matched_text": matched, "source_field": field, "confidence": "medium"}
        for index, mirror in enumerate(_dict_list(storyboard.get("mirror_shots")), start=1):
            matched = _matching_alias(aliases, str(mirror.get("description") or ""))
            if matched:
                return {"match_type": "prop_mirror_shot", "matched_text": matched, "source_field": f"mirror_shots[{index - 1}].description", "confidence": "medium"}
    return None


def _first_text(*sources: dict[str, Any], keys: tuple[str, ...]) -> str:
    for source in sources:
        if not isinstance(source, dict):
            continue
        for key in keys:
            value = source.get(key)
            if value is None:
                continue
            if isinstance(value, list):
                text = " / ".join(_as_text_list(value))
            elif isinstance(value, dict):
                text = json.dumps(value, ensure_ascii=False)
            else:
                text = str(value).strip()
            if text:
                return text
    return ""


def _first_text_list(*sources: dict[str, Any], keys: tuple[str, ...]) -> list[str]:
    for source in sources:
        if not isinstance(source, dict):
            continue
        for key in keys:
            value = source.get(key)
            if isinstance(value, list):
                texts = []
                for item in value:
                    if isinstance(item, dict):
                        text = _first_text(item, keys=("zone", "name", "description", "value"))
                    else:
                        text = str(item or "").strip()
                    if text:
                        texts.append(text)
                if texts:
                    return list(dict.fromkeys(texts))
            text = str(value or "").strip()
            if text:
                return [text]
    return []


def _priority_value(value: Any) -> str:
    raw = str(value or "").strip().upper()
    mapping = {"1": "S", "2": "A", "3": "B", "4": "C", "HIGH": "A", "MEDIUM": "B", "LOW": "C", "高": "A", "中": "B", "低": "C"}
    return raw if raw in {"S", "A", "B", "C"} else mapping.get(raw, "C")


def _find_by_code(items: list[dict[str, Any]], key: str, code: str) -> dict[str, Any] | None:
    if not code:
        return None
    for item in items:
        if str(item.get(key) or "").strip() == code:
            return item
    return None


def _match_scene_asset(item: dict[str, Any], storyboard: dict[str, Any], assets: list[dict[str, Any]]) -> dict[str, Any] | None:
    scene_code = _first_text(item, storyboard, keys=("scene_code", "asset_code"))
    scene_name = _first_text(item, storyboard, keys=("scene_name", "space", "name"))
    scenes = [asset for asset in assets if asset.get("asset_type") == "scene"]
    for scene in scenes:
        if scene_code and scene_code in {str(scene.get("asset_code") or ""), str(scene.get("agent_asset_code") or ""), str(scene.get("scene_code") or "")}:
            return scene
    for scene in scenes:
        aliases = _asset_aliases(scene)
        if scene_name and _matching_alias(aliases, scene_name):
            return scene
    text = " ".join(str(storyboard.get(field) or "") for field in ("scene_name", "space", "title", "description"))
    for scene in scenes:
        matched = _matching_alias(_asset_aliases(scene), text)
        if matched:
            return scene
    return None


def _match_asset_refs(refs: list[str], assets: list[dict[str, Any]], asset_type: str) -> list[str]:
    matched: list[str] = []
    typed_assets = [asset for asset in assets if asset.get("asset_type") == asset_type]
    for ref in refs:
        code = ""
        for asset in typed_assets:
            aliases = _asset_aliases(asset)
            if ref in {str(asset.get("asset_code") or ""), str(asset.get("agent_asset_code") or "")} or _matching_alias(aliases, ref):
                code = str(asset.get("asset_code") or ref).strip()
                break
        matched.append(code or ref)
    return list(dict.fromkeys(item for item in matched if item))


def _find_scene_master(shot: dict[str, Any], asset_masters: list[dict[str, Any]]) -> dict[str, Any] | None:
    scene_code = str(shot.get("scene_code") or "").strip()
    scene_name = str(shot.get("scene_name") or "").strip()
    scenes = [item for item in asset_masters if item.get("asset_type") == "scene"]
    for scene in scenes:
        if scene_code and scene_code in {str(scene.get("asset_code") or ""), str(scene.get("parent_scene_code") or "")}:
            return scene
    for scene in scenes:
        aliases = _asset_aliases(scene)
        if scene_name and _matching_alias(aliases, scene_name):
            return scene
    return None


def _any_text_match(needle: str, haystacks: list[str]) -> bool:
    if not needle:
        return True
    for haystack in haystacks:
        if _matching_alias([needle], haystack):
            return True
    return False


def _asset_aliases(asset: dict[str, Any]) -> list[str]:
    raw_values = [
        asset.get("name"),
        asset.get("asset_code"),
        asset.get("description"),
    ]
    metadata = asset.get("metadata")
    if isinstance(metadata, dict):
        raw_values.extend(
            metadata.get(key)
            for key in (
                "role",
                "scene_no",
                "location",
                "prop_type",
                "usage_scene",
                "scene_description",
                "stable_prompt",
            )
        )
    aliases: list[str] = []
    for value in raw_values:
        text = str(value or "").strip()
        if not text:
            continue
        aliases.append(text)
        aliases.extend(_split_alias_text(text))
    filtered: list[str] = []
    for alias in aliases:
        cleaned = alias.strip(" 　，,。；;：:（）()【】[]《》<>\"'")
        if not cleaned:
            continue
        if len(cleaned) > 24:
            continue
        if len(cleaned) < 2 and cleaned not in {"弓", "杖"}:
            continue
        if cleaned not in filtered:
            filtered.append(cleaned)
        for suffix in ("飞行器", "玉璧", "碎片", "光球", "骨弓", "骨杖", "弓", "杖"):
            if cleaned.endswith(suffix) and suffix not in filtered:
                filtered.append(suffix)
    return filtered[:32]


def _split_alias_text(text: str) -> list[str]:
    pieces: list[str] = []
    current = ""
    bracket_stack = 0
    for char in text:
        if char in "（([【《":
            if current.strip():
                pieces.append(current.strip())
            current = ""
            bracket_stack += 1
            continue
        if char in "）)]】》":
            if current.strip():
                pieces.append(current.strip())
            current = ""
            bracket_stack = max(0, bracket_stack - 1)
            continue
        if bracket_stack == 0 and char in "，,。；;：:/、| ":
            if current.strip():
                pieces.append(current.strip())
            current = ""
            continue
        current += char
    if current.strip():
        pieces.append(current.strip())
    expanded: list[str] = []
    for piece in pieces:
        expanded.append(piece)
        if "的" in piece:
            expanded.extend(part for part in piece.split("的") if part)
    return expanded


def _matching_alias(aliases: list[str], text: str) -> str:
    haystack = str(text or "").strip()
    if not haystack:
        return ""
    for alias in aliases:
        if alias and alias == haystack:
            return alias
    for alias in aliases:
        if alias and (alias in haystack or haystack in alias):
            return alias
    return ""


def _relation_confidence(evidence: list[dict[str, Any]]) -> str:
    confidences = {str(item.get("confidence") or "") for item in evidence}
    if "high" in confidences:
        return "high"
    if "medium" in confidences:
        return "medium"
    return "low"


def _issue(path: str, code: str, message: str, severity: IssueSeverity = "warning") -> ValidationIssue:
    return ValidationIssue(path=path, code=code, message=message, severity=severity)


def _dict_list(value: Any) -> list[dict[str, Any]]:
    return [item for item in value if isinstance(item, dict)] if isinstance(value, list) else []


def _as_text_list(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, list):
        return [str(item).strip() for item in value if str(item).strip()]
    text = str(value).strip()
    return [text] if text else []


def normalize_manual_review_items(value: Any, *, source: str = "agent") -> list[dict[str, Any]]:
    if value is None:
        return []
    if isinstance(value, list):
        return _dedupe_manual_review_items([item for raw in value for item in normalize_manual_review_items(raw, source=source)])
    if isinstance(value, dict):
        item = dict(value)
        text = str(
            item.get("item")
            or item.get("title")
            or item.get("question")
            or item.get("message")
            or item.get("detail")
            or item.get("uncertainty")
            or item.get("issue_type")
            or item.get("field")
            or item.get("suggestion")
            or ""
        ).strip()
        if not text:
            return []
        detail = str(item.get("detail") or item.get("message") or "").strip()
        normalized = {
            "item": text,
            "detail": detail,
            "status": str(item.get("status") or "待确认").strip(),
            "severity": str(item.get("severity") or "warning").strip(),
            "source": str(item.get("source") or source).strip(),
        }
        for key in (
            "field", "question", "path", "code", "asset_code", "asset_name", "issue_type", "uncertainty", "suggestion", "target",
            "target_field", "missing_reason", "human_input", "applied_at", "match_status", "matched_prop_code",
            "matched_prop_name", "match_action", "match_confidence",
            "scope_type", "scope_code", "requirement_level",
        ):
            if item.get(key):
                normalized[key] = str(item[key]).strip()
        return [normalized]

    text = str(value).strip()
    if not text:
        return []
    if text.startswith("{") and text.endswith("}"):
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError:
            parsed = None
        if isinstance(parsed, dict):
            return normalize_manual_review_items(parsed, source=source)
    return [_review_item(text, source=source)]


def blocking_manual_review_items(value: Any, *, source: str = "agent") -> list[dict[str, Any]]:
    return [
        item
        for item in normalize_manual_review_items(value, source=source)
        if str(item.get("requirement_level") or "").strip().lower() != "optional"
        and str(item.get("status") or "").strip().lower() != "optional"
    ]


def _review_item(
    item: str,
    *,
    detail: str = "",
    status: str = "待确认",
    severity: str = "warning",
    source: str = "agent",
    path: str | None = None,
    code: str | None = None,
) -> dict[str, Any]:
    normalized: dict[str, Any] = {
        "item": str(item).strip(),
        "detail": str(detail).strip(),
        "status": str(status).strip() or "待确认",
        "severity": str(severity).strip() or "warning",
        "source": str(source).strip() or "agent",
    }
    if path:
        normalized["path"] = str(path).strip()
    if code:
        normalized["code"] = str(code).strip()
    return normalized


def _dedupe_manual_review_items(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
    deduped: list[dict[str, Any]] = []
    seen: set[tuple[str, str, str, str]] = set()
    for item in items:
        if not isinstance(item, dict):
            continue
        title = str(item.get("item") or "").strip()
        if not title:
            continue
        normalized = {
            "item": title,
            "detail": str(item.get("detail") or "").strip(),
            "status": str(item.get("status") or "待确认").strip(),
            "severity": str(item.get("severity") or "warning").strip(),
            "source": str(item.get("source") or "agent").strip(),
        }
        for key in (
            "field", "question", "path", "code", "asset_code", "asset_name", "issue_type", "uncertainty", "suggestion", "target",
            "target_field", "missing_reason", "human_input", "applied_at", "match_status", "matched_prop_code",
            "matched_prop_name", "match_action", "match_confidence",
            "scope_type", "scope_code", "requirement_level",
        ):
            if item.get(key):
                normalized[key] = str(item[key]).strip()
        key = (
            normalized["item"],
            normalized["detail"],
            normalized.get("source", ""),
            normalized.get("code", ""),
        )
        if key in seen:
            continue
        seen.add(key)
        deduped.append(normalized)
    return deduped


def _project_prefix(value: str | None) -> str:
    prefix = str(value or "RF").strip().upper()[:5]
    return prefix or "RF"


def _episode_code(value: Any, fallback_num: int) -> str:
    if value is None or str(value).strip() == "":
        return f"EP{fallback_num:02d}"
    raw = str(value).strip().upper()
    if raw.startswith("EP"):
        number = raw.replace("EP", "", 1)
        return f"EP{_safe_int(number, fallback_num):02d}" if number.isdigit() else raw
    if raw.isdigit():
        return f"EP{int(raw):02d}"
    return f"EP{fallback_num:02d}"


def _episode_num(episode_code: str) -> int:
    raw = episode_code.upper().replace("EP", "", 1)
    return _safe_int(raw, 1)


def _safe_int(value: Any, fallback: int) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return fallback


def _int_or_none(value: Any) -> int | None:
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def _float_or_none(value: Any) -> float | None:
    try:
        return float(value)
    except (TypeError, ValueError):
        return None


def _dialogue_lines(value: Any) -> list[str]:
    if isinstance(value, list):
        return [line for item in value for line in _dialogue_lines(item)]
    return [line.strip() for line in str(value or "").splitlines() if line.strip()]


def _normalize_shot_size_cn(value: Any) -> str:
    text = str(value or "").strip().replace("->", "→").replace("-", "→")
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


def _asset_type(value: Any) -> str:
    raw = str(value or "prop").strip().lower()
    mapping = {
        "character": "character",
        "人物": "character",
        "角色": "character",
        "scene": "scene",
        "场景": "scene",
        "prop": "prop",
        "道具": "prop",
    }
    return mapping.get(raw, "prop")


def _asset_code(asset_type: str, index: int, *, project_prefix: str | None = None) -> str:
    prefix = {"character": "R", "scene": "SC", "prop": "P"}.get(asset_type, "P")
    if project_prefix:
        return f"{_project_prefix(project_prefix)}-{prefix}{index:03d}"
    return f"{prefix}{index:03d}"


def _canonical_contract_asset_code(
    value: Any,
    *,
    project_prefix: str | None,
    asset_type: str,
) -> str | None:
    if not project_prefix:
        return None
    match = re.fullmatch(
        rf"{re.escape(_project_prefix(project_prefix))}-(R|SC|S|P)(\d+)",
        str(value or "").strip().upper(),
    )
    if not match:
        return None
    code_prefix, number = match.groups()
    matched_type = {"R": "character", "SC": "scene", "S": "scene", "P": "prop"}[code_prefix]
    if matched_type != asset_type:
        return None
    canonical_prefix = {"character": "R", "scene": "SC", "prop": "P"}[asset_type]
    return f"{_project_prefix(project_prefix)}-{canonical_prefix}{int(number):03d}"


def _mark_repaired(item: dict[str, Any], field: str) -> None:
    repairs = item.setdefault("_contract_repairs", [])
    if field not in repairs:
        repairs.append(field)


def _is_repaired(item: dict[str, Any], field: str) -> bool:
    repairs = item.get("_contract_repairs")
    return isinstance(repairs, list) and field in repairs

from __future__ import annotations

import hashlib
import json
import re
from typing import Any

CHARACTER_VIEW_REQUIREMENTS: dict[str, dict[str, str]] = {
    "A": {
        "title": "正面全身 A-Pose",
        "framing": "正面平视，全身从头到脚完整呈现，人物居中，作为后续图位的主参考图。",
        "pose": "标准 A-Pose 站立，双臂从身体两侧自然展开约 30 度，手指放松，双脚与肩同宽，身体重心居中，神态自然。",
        "reference_requirement": "无前置参考图；本图作为 B-E 的角色一致性主参考图。",
    },
    "B": {
        "title": "侧面或 3/4 全身",
        "framing": "侧面或 45 度半侧全身构图，从头到脚完整呈现。",
        "pose": "自然直立，四肢放松且不遮挡服装轮廓，头部与身体朝向一致。",
        "reference_requirement": "必须使用已审核通过的图 A，保持身份、骨相、发型、体型和服装一致。",
    },
    "C": {
        "title": "背面全身",
        "framing": "背面平视全身构图，完整展示发型、服装和配饰背面。",
        "pose": "背向镜头自然直立，双臂稍离躯干，不遮挡服装背部结构和配饰。",
        "reference_requirement": "必须使用已审核通过的图 A，保持身份、骨相、发型、体型和服装一致。",
    },
    "D": {
        "title": "面部与表情参考",
        "framing": "面部近景构图，清晰展示五官、发型、肤色和核心表情。",
        "pose": "头部自然稳定，双唇自然闭合，目光与角色设定一致，不遮挡五官。",
        "reference_requirement": "必须使用已审核通过的图 A，保持身份、骨相、发型和肤色一致。",
    },
    "E": {
        "title": "服装与材质细节",
        "framing": "服装与配饰细节构图，清晰展示层次、纹理、接缝和关键材质。",
        "pose": "保持静止且服装自然垂落，不遮挡需要展示的材质、纹理与结构细节。",
        "reference_requirement": "必须使用已审核通过的图 A，保持当前装扮设计和材质一致。",
    },
}


def build_reading_to_asset_context(
    reading_output: dict[str, Any],
    resolved_production_brief: dict[str, Any] | None,
) -> dict[str, Any]:
    report = _dict(reading_output.get("reading_report"))
    resolved = _dict(resolved_production_brief)
    resolved_field_sources = _dict(resolved.get("field_sources"))
    reading_source = (
        _text(reading_output.get("source"))
        or _text(_dict(report.get("source")).get("source_label"))
        or "unknown"
    )
    asset_source = _normalized_asset_source(reading_output.get("asset_extract_source")) or reading_source
    serialized = json.dumps(report, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return {
        "schema_version": "ReadingToAssetContext.v1",
        "lineage": {
            "reading_source": reading_source,
            "asset_source": asset_source,
            "confirmed_at": _text(report.get("confirmed_at")),
            "confirmed_reading_hash": hashlib.sha256(serialized.encode("utf-8")).hexdigest(),
            "style_catalog_version": _text(resolved.get("style_catalog_version")),
            "field_sources": {
                "cultural_origin": reading_source,
                "resolved_style": _text(resolved_field_sources.get("resolved_style"))
                or "resolved_production_brief",
                "resolved_brief_projection": "resolved_production_brief",
                "global_visual_constraints": reading_source,
                "manual_review_items": reading_source,
            },
        },
        "cultural_origin": _dict(report.get("cultural_origin")),
        "resolved_style": _dict(resolved.get("resolved_style")),
        "resolved_brief_projection": {
            key: resolved.get(key)
            for key in (
                "content_type", "asset_master_aspect_ratio", "primary_style_id", "cultural_contexts",
                "primary_cultural_context_code", "narrative_grammar_by_context",
            )
            if resolved.get(key) not in (None, "", [], {})
        },
        "global_visual_constraints": _visual_constraints(report),
        "manual_review_items": _list_of_dicts(report.get("manual_review_items")),
    }


def build_character_production_contexts(
    reading_character: dict[str, Any],
    existing_asset: dict[str, Any] | None,
    reading_context: dict[str, Any],
    prop_candidates: list[dict[str, Any]] | None = None,
) -> list[dict[str, Any]]:
    existing = existing_asset or {}
    metadata = {
        **_dict(reading_character.get("attributes")),
        **_dict(reading_character.get("metadata")),
        **_dict(existing.get("metadata")),
    }
    asset_code = _text(existing.get("asset_code") or reading_character.get("asset_code") or reading_character.get("role_code"))
    name = _text(existing.get("name") or reading_character.get("name"))
    priority = _text(existing.get("priority") or reading_character.get("priority") or metadata.get("priority") or "C").upper()
    identity = _character_identity(reading_character, existing, metadata, asset_code, name, priority)
    asset_source = (
        _asset_item_source(existing)
        or ("confirmed_asset_snapshot" if existing else _reading_context_asset_source(reading_context))
    )
    stages = _list_of_dicts(existing.get("age_stages") or metadata.get("age_stages"))
    props = _prop_map(prop_candidates or [])
    review_items = _scoped_review_items(reading_context.get("manual_review_items"), asset_code, name)
    if not stages:
        return [_production_context(identity, None, None, reading_context, review_items, props, asset_source)]
    contexts: list[dict[str, Any]] = []
    for stage in stages:
        variants = _list_of_dicts(stage.get("costume_variants"))
        if variants:
            contexts.extend(
                _production_context(identity, stage, variant, reading_context, review_items, props, asset_source)
                for variant in variants
            )
        else:
            contexts.append(_production_context(identity, stage, None, reading_context, review_items, props, asset_source))
    return contexts


def build_scene_production_context(
    scene: dict[str, Any],
    reading_context: dict[str, Any],
    resolved_production_brief: dict[str, Any],
) -> dict[str, Any]:
    metadata = {**_dict(scene.get("attributes")), **_dict(scene.get("metadata"))}
    asset_code = _text(scene.get("asset_code"))
    name = _text(scene.get("name") or scene.get("scene_name"))
    context_code = _text(scene.get("context_code") or metadata.get("context_code"))
    resolved = _dict(resolved_production_brief)
    asset_source = _asset_context_source(scene, reading_context)
    return {
        "schema_version": "SceneProductionContext.v1",
        "context_key": asset_code,
        "asset_identity": {
            "asset_code": asset_code,
            "name": name,
            "priority": _text(scene.get("priority") or metadata.get("priority") or "C").upper(),
            "description": _first(scene, metadata, keys=("description", "scene_description", "visual_goal")),
            "narrative_function": _first(scene, metadata, keys=("narrative_function", "story_function")),
            "time": _first(scene, metadata, keys=("time", "time_period")),
            "context_code": context_code,
            "render_mode": _first(scene, metadata, keys=("render_mode",)) or _render_mode(resolved),
        },
        "spatial_identity": {
            "zones": _first_list(scene, metadata, keys=("spatial_zones", "spatial_layout", "zones")),
            "key_props": _first_list(scene, metadata, keys=("key_props",)),
            "continuity_risks": _first_list(scene, metadata, keys=("continuity_risks", "continuity_constraints")),
            "visual_goal": _first(scene, metadata, keys=("visual_goal", "visual_lighting", "atmosphere")),
        },
        "cultural_context": _brief_cultural_context(resolved, context_code),
        "narrative_grammar": _text(_dict(resolved.get("narrative_grammar_by_context")).get(context_code)),
        "resolved_style": _dict(resolved.get("resolved_style")),
        "global_visual_constraints": _dict(reading_context.get("global_visual_constraints")),
        "master_requirement": {
            "code": "MASTER",
            "title": "场景标准母版",
            "camera_angle": "平视广角主机位",
            "framing": "完整展示空间布局、主要区域、出入口、人物动线和固定陈设",
            "reference_requirement": "无前置参考图；本图作为后续 V001、V002 等场景视角的空间连续性主参考。",
        },
        "content_boundaries": {
            "include": ["空间结构", "固定陈设", "主机位", "光照基准", "文化时代锚点"],
            "exclude": ["剧情瞬间", "临时人物动作", "独立道具设计", "未确认空间"],
        },
        "manual_review_items": _scoped_review_items(reading_context.get("manual_review_items"), asset_code, name),
        "lineage": _lineage_with_field_sources(
            reading_context,
            {
                "context_key": "system_derived",
                "asset_identity": asset_source,
                "spatial_identity": asset_source,
                "cultural_context": _resolved_field_source(resolved, "cultural_contexts"),
                "narrative_grammar": _resolved_field_source(resolved, "narrative_grammar_by_context"),
                "resolved_style": _resolved_field_source(resolved, "resolved_style"),
                "global_visual_constraints": _reading_context_field_source(
                    reading_context, "global_visual_constraints"
                ),
                "master_requirement": "system_production_rule",
                "content_boundaries": "system_production_rule",
                "manual_review_items": _reading_context_field_source(reading_context, "manual_review_items"),
            },
        ),
    }


def build_prop_production_context(
    prop: dict[str, Any],
    reading_context: dict[str, Any],
    resolved_production_brief: dict[str, Any],
) -> dict[str, Any]:
    metadata = {**_dict(prop.get("attributes")), **_dict(prop.get("metadata"))}
    asset_code = _text(prop.get("asset_code"))
    name = _text(prop.get("name") or prop.get("prop_name"))
    resolved = _dict(resolved_production_brief)
    asset_source = _asset_context_source(prop, reading_context)
    context_codes = _first_list(prop, metadata, keys=("context_codes", "related_context_codes"))
    primary_context = context_codes[0] if context_codes else _text(resolved.get("primary_cultural_context_code"))
    status_values = _first_list(prop, metadata, keys=("status_changes", "state_plan"))
    scalar_status = _first(prop, metadata, keys=("status_change",))
    if scalar_status and scalar_status not in status_values:
        status_values.append(scalar_status)
    relations = _dict(prop.get("relations"))
    owner_link = _dict(relations.get("owner_character"))
    owner_character = _first(prop, metadata, keys=("owner_character",)) or _text(owner_link.get("target_display_label")).split("-", 1)[-1]
    owner_role_code = _short_role_code(
        _first(prop, metadata, keys=("owner_role_code", "owner_character_id"))
        or owner_link.get("target_display_code")
    )
    if _short_role_code(owner_character):
        owner_character = ""
    return {
        "schema_version": "PropProductionContext.v1",
        "context_key": asset_code,
        "asset_identity": {
            "asset_code": asset_code,
            "name": name,
            "priority": _text(prop.get("priority") or metadata.get("priority") or "C").upper(),
            "prop_type": _first(prop, metadata, keys=("prop_type", "type")),
            "description": _first(prop, metadata, keys=("description", "appearance", "appearance_material")),
            "owner_character": owner_character,
            "owner_role_code": owner_role_code,
        },
        "material_identity": {
            "shape": _first(prop, metadata, keys=("shape", "appearance", "visual_anchor")),
            "scale": _first(prop, metadata, keys=("scale", "proportion")),
            "materials": _first_list(prop, metadata, keys=("materials", "material")),
            "colors": _first_list(prop, metadata, keys=("colors", "color_palette")),
            "textures": _first_list(prop, metadata, keys=("textures", "texture")),
        },
        "default_state": "标准完好状态",
        "known_state_changes": status_values,
        "cultural_context": _brief_cultural_context(resolved, primary_context),
        "resolved_style": _dict(resolved.get("resolved_style")),
        "global_visual_constraints": _dict(reading_context.get("global_visual_constraints")),
        "master_requirement": {
            "code": "MASTER",
            "title": "道具标准母版",
            "framing": "单一道具完整入画，清晰呈现比例、结构、材质、颜色与纹理",
            "reference_requirement": "无前置参考图；本图作为后续 Pxxx-V001 等状态变体的一致性主参考。",
        },
        "content_boundaries": {
            "include": ["道具结构", "比例", "材质", "默认状态", "文化时代锚点"],
            "exclude": ["剧情动作", "人物手部", "完整场景", "未确认状态"],
        },
        "manual_review_items": _scoped_review_items(reading_context.get("manual_review_items"), asset_code, name),
        "lineage": _lineage_with_field_sources(
            reading_context,
            {
                "context_key": "system_derived",
                "asset_identity": asset_source,
                "material_identity": asset_source,
                "default_state": "system_production_rule",
                "known_state_changes": asset_source,
                "cultural_context": _resolved_field_source(resolved, "cultural_contexts"),
                "resolved_style": _resolved_field_source(resolved, "resolved_style"),
                "global_visual_constraints": _reading_context_field_source(
                    reading_context, "global_visual_constraints"
                ),
                "master_requirement": "system_production_rule",
                "content_boundaries": "system_production_rule",
                "manual_review_items": _reading_context_field_source(reading_context, "manual_review_items"),
            },
        ),
    }


def semantic_asset_prompt_context(context: dict[str, Any]) -> dict[str, Any]:
    return {
        key: value
        for key, value in context.items()
        if key not in {"lineage", "manual_review_items"} and value not in (None, "", [], {})
    }


def _brief_cultural_context(resolved: dict[str, Any], context_code: str) -> dict[str, Any]:
    contexts = _list_of_dicts(resolved.get("cultural_contexts"))
    target = context_code or _text(resolved.get("primary_cultural_context_code"))
    return next((dict(item) for item in contexts if _text(item.get("context_code")) == target), {})


def _render_mode(resolved: dict[str, Any]) -> str:
    return "live_action" if resolved.get("content_type") == "live_action_drama" else "animated"


def scope_character_production_context(
    context: dict[str, Any],
    view_codes: str | list[str] | tuple[str, ...],
) -> dict[str, Any]:
    requested = [view_codes] if isinstance(view_codes, str) else list(view_codes)
    allowed = [str(code).strip().upper() for code in requested if str(code).strip().upper() in CHARACTER_VIEW_REQUIREMENTS]
    if not allowed:
        return dict(context)
    requirements = [
        dict(item)
        for item in _list_of_dicts(context.get("view_requirements"))
        if _text(item.get("code")).upper() in allowed
    ]
    if not requirements:
        requirements = [{"code": code, **CHARACTER_VIEW_REQUIREMENTS[code]} for code in allowed]
    scoped = {
        **context,
        "output_spec": allowed[0] if len(allowed) == 1 else "-".join(allowed),
        "view_requirements": requirements,
        "generation_scope": {"view_codes": allowed, "mode": "single_view" if len(allowed) == 1 else "selected_views"},
    }
    lineage = _dict(scoped.get("lineage"))
    lineage["field_sources"] = {
        **_dict(lineage.get("field_sources")),
        "output_spec": "system_runtime_scope",
        "view_requirements": "system_runtime_scope",
        "generation_scope": "system_runtime_scope",
    }
    scoped["lineage"] = lineage
    return scoped


def semantic_character_prompt_context(context: dict[str, Any]) -> dict[str, Any]:
    """Only fields that can change the produced pixels belong in the prompt cache key."""
    return {
        key: context.get(key)
        for key in (
            "schema_version",
            "context_key",
            "character_identity",
            "age_stage",
            "costume_variant",
            "resolved_style",
            "cultural_origin",
            "global_visual_constraints",
            "output_spec",
            "view_requirements",
            "studio_requirement",
            "content_boundaries",
            "generation_scope",
        )
        if context.get(key) not in (None, "", [], {})
    }


def prompt_asset_from_context(context: dict[str, Any]) -> dict[str, Any]:
    identity = _dict(context.get("character_identity"))
    stage = _dict(context.get("age_stage"))
    costume = _dict(context.get("costume_variant"))
    return {
        "asset_code": identity.get("asset_id"),
        "name": identity.get("name"),
        "priority": identity.get("priority"),
        "description": identity.get("narrative_identity"),
        "metadata": {
            "identity_setting": identity.get("narrative_identity"),
            "visual_features": identity.get("appearance"),
            "continuity_anchors": identity.get("identity_anchors"),
            "forbidden_variations": identity.get("forbidden_changes"),
            "age_stage": stage,
            "current_costume": costume,
        },
    }


def _production_context(
    identity: dict[str, Any],
    stage: dict[str, Any] | None,
    variant: dict[str, Any] | None,
    reading_context: dict[str, Any],
    review_items: list[dict[str, Any]],
    props: dict[str, dict[str, Any]],
    asset_source: str,
) -> dict[str, Any]:
    age = _age_stage(stage)
    costume = _costume_variant(variant, props)
    output_spec = _text((stage or {}).get("output_spec")) or ("A-E" if identity.get("priority") in {"S", "A"} else "A")
    codes = ["A"] if output_spec.upper() == "A" else list(CHARACTER_VIEW_REQUIREMENTS)
    stage_code = _text(age.get("stage_code"))
    variant_code = _text(costume.get("variant_code"))
    key = "/".join(item for item in (identity.get("asset_id"), stage_code, variant_code) if item)
    scene_codes = _text_list(
        (variant or {}).get("related_scene_codes")
        or (variant or {}).get("scene_codes")
        or (stage or {}).get("related_scene_codes")
        or (stage or {}).get("scene_codes")
        or []
    )
    return {
        "schema_version": "CharacterProductionContext.v1",
        "context_key": key,
        "character_identity": identity,
        "age_stage": age,
        "costume_variant": costume,
        "resolved_style": _dict(reading_context.get("resolved_style")),
        "cultural_origin": _dict(reading_context.get("cultural_origin")),
        "global_visual_constraints": _dict(reading_context.get("global_visual_constraints")),
        "output_spec": output_spec.upper(),
        "view_requirements": [
            {"code": code, **CHARACTER_VIEW_REQUIREMENTS[code]}
            for code in codes
        ],
        "related_scene_codes": scene_codes,
        "studio_requirement": {
            "background": "中性纯色摄影棚背景，无场景叙事元素、文字、Logo和杂物。",
            "lighting": "柔和均匀布光，人物和服装色彩准确，轮廓与材质边缘清晰。",
            "composition": "严格按当前图位构图，不融合其他图位。",
        },
        "content_boundaries": {
            "include": ["角色稳定身份", "当前年龄阶段", "当前装扮", "当前图位", "已确认生产风格"],
            "exclude": ["其他装扮", "剧情动作", "场景叙事", "未确认推测", "人工复核问题", "其他图位要求"],
        },
        "manual_review_items": _context_review_items(review_items, stage_code, variant_code),
        "lineage": _lineage_with_field_sources(
            reading_context,
            {
                "context_key": "system_derived",
                "character_identity": asset_source,
                "age_stage": asset_source,
                "costume_variant": asset_source,
                "resolved_style": _reading_context_field_source(reading_context, "resolved_style"),
                "cultural_origin": _reading_context_field_source(reading_context, "cultural_origin"),
                "global_visual_constraints": _reading_context_field_source(
                    reading_context, "global_visual_constraints"
                ),
                "output_spec": asset_source,
                "view_requirements": "system_production_rule",
                "related_scene_codes": asset_source,
                "studio_requirement": "system_production_rule",
                "content_boundaries": "system_production_rule",
                "manual_review_items": _reading_context_field_source(reading_context, "manual_review_items"),
            },
        ),
    }


def _character_identity(
    reading: dict[str, Any],
    existing: dict[str, Any],
    metadata: dict[str, Any],
    asset_code: str,
    name: str,
    priority: str,
) -> dict[str, Any]:
    return {
        "asset_id": asset_code,
        "name": name,
        "priority": priority,
        "narrative_identity": _first(existing, metadata, reading, keys=("identity_setting", "dramatic_function", "role", "description")),
        "appearance": _first(existing, metadata, reading, keys=("visual_features", "appearance", "appearance_clues")),
        "face": _first(existing, metadata, reading, keys=("face", "face_features", "facial_features")),
        "body": _first(existing, metadata, reading, keys=("body", "body_type", "physique")),
        "hair": _first(existing, metadata, reading, keys=("hair", "hair_style", "hair_features")),
        "skin": _first(existing, metadata, reading, keys=("skin", "skin_tone", "skin_texture")),
        "identity_anchors": _first_list(existing, metadata, reading, keys=("identity_anchors", "continuity_anchors")),
        "forbidden_changes": _first_list(existing, metadata, reading, keys=("forbidden_changes", "forbidden_variations")),
    }


def _age_stage(stage: dict[str, Any] | None) -> dict[str, Any]:
    if not stage:
        return {}
    return {
        "stage_code": _text(stage.get("stage_code")),
        "name": _text(stage.get("name")),
        "age_range": _text(stage.get("age_range")),
        "timeline": _text(stage.get("timeline")),
        "face_changes": _text(stage.get("face_changes")),
        "body_changes": _text(stage.get("body_changes")),
        "hair_skin_changes": _text(stage.get("hair_skin_changes")),
        "identity_anchors": _text_list(stage.get("identity_anchors")),
        "forbidden_changes": _text_list(stage.get("forbidden_changes")),
    }


def _costume_variant(variant: dict[str, Any] | None, props: dict[str, dict[str, Any]]) -> dict[str, Any]:
    if not variant:
        return {}
    linked_props = [props[code] for code in _text_list(variant.get("costume_prop_codes")) if code in props]
    visual_sources = [_costume_visual_fields(item) for item in linked_props]
    own_visual = _costume_visual_fields(variant)
    return {
        "variant_code": _text(variant.get("variant_code")),
        "name": _text(variant.get("name")),
        "appearance": own_visual.get("appearance") or _joined(visual_sources, "appearance"),
        "layers": own_visual.get("layers") or _joined_list(visual_sources, "layers"),
        "colors": own_visual.get("colors") or _joined_list(visual_sources, "colors"),
        "materials": own_visual.get("materials") or _joined_list(visual_sources, "materials"),
        "accessories": own_visual.get("accessories") or _joined_list(visual_sources, "accessories"),
        "wear_state": own_visual.get("wear_state") or _joined(visual_sources, "wear_state"),
        "linked_prop_codes": _text_list(variant.get("costume_prop_codes")),
    }


def _costume_visual_fields(source: dict[str, Any]) -> dict[str, Any]:
    metadata = _dict(source.get("metadata"))
    return {
        "appearance": _first(source, metadata, keys=("visual_description", "appearance", "visual_anchor")),
        "layers": _first_list(source, metadata, keys=("costume_layers", "layers", "layering")),
        "colors": _first_list(source, metadata, keys=("color_palette", "colors")),
        "materials": _first_list(source, metadata, keys=("materials", "material")),
        "accessories": _first_list(source, metadata, keys=("accessories",)),
        "wear_state": _first(source, metadata, keys=("wear_state", "condition")),
    }


def _visual_constraints(report: dict[str, Any]) -> dict[str, Any]:
    visual = _dict(report.get("visual_style"))
    return {
        "color_palette": _text_list(visual.get("color_palette")),
        "lighting_rules": _text_list(visual.get("lighting_rules")),
        "camera_language": _text_list(visual.get("camera_language")),
        "forbidden_styles": _text_list(visual.get("forbidden_styles")),
    }


def _prop_map(items: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    output: dict[str, dict[str, Any]] = {}
    for item in items:
        code = _text(item.get("prop_code") or item.get("asset_code"))
        if code:
            output[code] = item
    return output


def _scoped_review_items(value: Any, asset_code: str, name: str) -> list[dict[str, Any]]:
    return [
        item for item in _list_of_dicts(value)
        if _text(item.get("asset_code") or item.get("code")) in {"", asset_code}
        and _text(item.get("asset_name") or item.get("name")) in {"", name}
    ]


def _context_review_items(items: list[dict[str, Any]], stage_code: str, variant_code: str) -> list[dict[str, Any]]:
    return [
        item for item in items
        if _text(item.get("age_stage_code")) in {"", stage_code}
        and _text(item.get("costume_variant_code")) in {"", variant_code}
    ]


def _first(*sources: dict[str, Any], keys: tuple[str, ...]) -> str:
    for source in sources:
        for key in keys:
            value = source.get(key)
            if value not in (None, "", [], {}):
                return _display(value)
    return ""


def _first_list(*sources: dict[str, Any], keys: tuple[str, ...]) -> list[str]:
    for source in sources:
        for key in keys:
            values = _text_list(source.get(key))
            if values:
                return values
    return []


def _short_role_code(value: Any) -> str:
    match = re.search(r"(?:^|-)(R\d+)$", _text(value).upper())
    return match.group(1) if match else ""


def _joined(items: list[dict[str, Any]], key: str) -> str:
    return "；".join(dict.fromkeys(_text(item.get(key)) for item in items if _text(item.get(key))))


def _joined_list(items: list[dict[str, Any]], key: str) -> list[str]:
    return list(dict.fromkeys(value for item in items for value in _text_list(item.get(key))))


def _display(value: Any) -> str:
    if isinstance(value, list):
        return "；".join(_display(item) for item in value if _display(item))
    if isinstance(value, dict):
        return "；".join(f"{key}：{_display(item)}" for key, item in value.items() if _display(item))
    return _text(value)


def _text(value: Any) -> str:
    return str(value or "").strip()


def _text_list(value: Any) -> list[str]:
    if isinstance(value, list):
        return list(dict.fromkeys(_text(item) for item in value if _text(item)))
    if value in (None, "", {}):
        return []
    return [_text(value)]


def _dict(value: Any) -> dict[str, Any]:
    return dict(value) if isinstance(value, dict) else {}


def _list_of_dicts(value: Any) -> list[dict[str, Any]]:
    return [dict(item) for item in value or [] if isinstance(item, dict)] if isinstance(value, list) else []


def _lineage_with_field_sources(
    reading_context: dict[str, Any],
    field_sources: dict[str, str],
) -> dict[str, Any]:
    lineage = _dict(reading_context.get("lineage"))
    return {
        **lineage,
        "field_sources": {
            **_dict(lineage.get("field_sources")),
            **{key: value for key, value in field_sources.items() if value},
        },
    }


def _reading_context_field_source(reading_context: dict[str, Any], field_name: str) -> str:
    lineage = _dict(reading_context.get("lineage"))
    field_sources = _dict(lineage.get("field_sources"))
    return _text(field_sources.get(field_name)) or _text(lineage.get("reading_source")) or "unknown"


def _reading_context_asset_source(reading_context: dict[str, Any]) -> str:
    lineage = _dict(reading_context.get("lineage"))
    return _text(lineage.get("asset_source")) or _text(lineage.get("reading_source")) or "unknown"


def _asset_item_source(value: Any) -> str:
    asset = _dict(value)
    metadata = _dict(asset.get("metadata"))
    field_sources = _dict(asset.get("field_sources"))
    return (
        _text(field_sources.get("asset"))
        or _text(asset.get("confirmation_source"))
        or _text(asset.get("source_label"))
        or _text(asset.get("source_type"))
        or _text(metadata.get("confirmation_source"))
        or _text(metadata.get("source_label"))
    )


def _asset_context_source(asset: dict[str, Any], reading_context: dict[str, Any]) -> str:
    workflow_source = _reading_context_asset_source(reading_context)
    if workflow_source in {"human_draft_input", "normalized_asset_inventory"}:
        return workflow_source
    return _asset_item_source(asset) or workflow_source


def _normalized_asset_source(value: Any) -> str:
    source = _text(value)
    return {
        "confirmed_input": "normalized_asset_inventory",
        "human_draft_input": "human_draft_input",
        "agent": "asset_extract_agent",
    }.get(source, source)


def _resolved_field_source(resolved: dict[str, Any], field_name: str) -> str:
    return _text(_dict(resolved.get("field_sources")).get(field_name)) or "resolved_production_brief"

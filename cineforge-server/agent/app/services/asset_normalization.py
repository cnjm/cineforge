from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Mapping
from typing import Any

from app.schemas.asset_normalization import (
    AssetNormalizationContextV1,
    AssetNormalizationResultV1,
    AssetNormalizationV1,
    AssetLinkV1,
    AssetRelationsV1,
    AssetReviewItemV1,
    AssetSourceV1,
    CharacterAttributesV1,
    GlobalAssetReviewItemV1,
    NormalizationRepairV1,
    PropAttributesV1,
    RelationEvidenceV1,
    SceneAttributesV1,
)


class AssetNormalizationError(ValueError):
    pass


_GROUP_TYPES = {
    "characters": "character",
    "scenes": "scene",
    "props": "prop",
    "assets": None,
}
_TYPE_ALIASES = {
    "character": "character",
    "人物": "character",
    "角色": "character",
    "scene": "scene",
    "场景": "scene",
    "prop": "prop",
    "道具": "prop",
}
_TYPE_PREFIX = {"character": "R", "scene": "SC", "prop": "P"}
_PRIORITY_ALIASES = {
    "S": "S",
    "A": "A",
    "B": "B",
    "C": "C",
    "高": "A",
    "中": "B",
    "低": "C",
    "1": "S",
    "2": "A",
    "3": "B",
    "4": "C",
}
_VISUAL_PRESENCE_ALIASES = {
    "on_screen": "on_screen",
    "onscreen": "on_screen",
    "出镜": "on_screen",
    "voice_only": "voice_only",
    "off_screen": "voice_only",
    "画外音": "voice_only",
    "mentioned_only": "mentioned_only",
    "mentioned": "mentioned_only",
    "仅提及": "mentioned_only",
    "未出场": "mentioned_only",
}
_CONFIDENCE_VALUES = {"high", "medium", "low", "unknown"}


def normalize_asset_inventory_v1(
    raw: Mapping[str, Any],
    context: AssetNormalizationContextV1 | Mapping[str, Any],
) -> AssetNormalizationResultV1:
    """Normalize visual extraction assets once, without lifecycle or persistence data."""
    ctx = context if isinstance(context, AssetNormalizationContextV1) else AssetNormalizationContextV1.model_validate(context)
    if raw.get("normalization_version") == "AssetNormalization.v1":
        return AssetNormalizationResultV1.model_validate(raw)

    prefix = _project_prefix(ctx.project_prefix)
    candidates = _collect_candidates(raw)
    merged = _dedupe_candidates(candidates)
    _assign_candidate_codes(merged, prefix)
    assets = [_build_asset(item, ctx, prefix) for item in merged]
    _resolve_relations(assets, merged, prefix)
    global_review_items = _bind_global_review_items(
        assets,
        raw.get("manual_review_items") or raw.get("review_items"),
    )
    return AssetNormalizationResultV1(assets=assets, global_review_items=global_review_items)


def to_formal_asset_identity(
    asset: AssetNormalizationV1,
    *,
    formal_asset_code: str | None = None,
) -> AssetNormalizationV1:
    """Promote a candidate identity without repeating semantic normalization."""
    code = str(formal_asset_code or asset.asset_code).strip().upper()
    display_code = _display_code(code)
    expected_prefix = _TYPE_PREFIX[asset.asset_type]
    if not re.fullmatch(rf"[A-Z0-9]+-{expected_prefix}\d{{3,}}", code):
        raise AssetNormalizationError(f"invalid formal {asset.asset_type} code: {code}")
    return asset.model_copy(
        update={
            "asset_code": code,
            "display_code": display_code,
            "display_label": f"{display_code}-{asset.name}",
            "code_state": "formal",
        }
    )


def formalize_asset_inventory_v1(
    inventory: AssetNormalizationResultV1,
    formal_codes_by_asset_key: Mapping[str, str],
) -> AssetNormalizationResultV1:
    """Atomically promote every identity and rewrite all asset relation targets."""
    expected_keys = {asset.client_asset_key for asset in inventory.assets}
    supplied_keys = {str(key) for key in formal_codes_by_asset_key}
    if supplied_keys != expected_keys:
        missing = sorted(expected_keys - supplied_keys)
        unexpected = sorted(supplied_keys - expected_keys)
        raise AssetNormalizationError(
            f"formal identity mapping must cover the inventory exactly; missing={missing}, unexpected={unexpected}"
        )
    normalized_codes = [str(formal_codes_by_asset_key[key]).strip().upper() for key in expected_keys]
    if len(normalized_codes) != len(set(normalized_codes)):
        raise AssetNormalizationError("formal asset codes must be unique")

    formal_assets = [
        to_formal_asset_identity(
            asset,
            formal_asset_code=formal_codes_by_asset_key[asset.client_asset_key],
        )
        for asset in inventory.assets
    ]
    formal_by_key = {asset.client_asset_key: asset for asset in formal_assets}
    rewritten: list[AssetNormalizationV1] = []
    for asset in formal_assets:
        scene_links = [_formal_link(link.target_asset_key, formal_by_key) for link in asset.relations.scene_links]
        owner = (
            _formal_link(asset.relations.owner_character.target_asset_key, formal_by_key)
            if asset.relations.owner_character is not None
            else None
        )
        relations = asset.relations.model_copy(update={"scene_links": scene_links, "owner_character": owner})
        rewritten.append(asset.model_copy(update={"relations": relations}))
    return AssetNormalizationResultV1(
        assets=rewritten,
        global_review_items=inventory.global_review_items,
    )


def _formal_link(asset_key: str, formal_by_key: Mapping[str, AssetNormalizationV1]) -> AssetLinkV1:
    target = formal_by_key.get(asset_key)
    if target is None:
        raise AssetNormalizationError(f"relation target is outside the formal inventory: {asset_key}")
    return AssetLinkV1(
        target_asset_key=target.client_asset_key,
        target_asset_code=target.asset_code,
        target_display_code=target.display_code,
        target_display_label=target.display_label,
    )


def _collect_candidates(raw: Mapping[str, Any]) -> list[dict[str, Any]]:
    candidates: list[dict[str, Any]] = []
    for group, fallback_type in _GROUP_TYPES.items():
        values = raw.get(group)
        if values is None:
            continue
        if not isinstance(values, list):
            raise AssetNormalizationError(f"{group} must be a list")
        for index, value in enumerate(values):
            if not isinstance(value, Mapping):
                raise AssetNormalizationError(f"{group}[{index}] must be an object")
            item = dict(value)
            asset_type = _asset_type(item.get("asset_type") or item.get("type"), fallback_type)
            name = _text(_pick(item, "name", "role_name", "scene_name", "prop_name"))
            if not name:
                raise AssetNormalizationError(f"{group}[{index}] is missing asset name")
            item["_asset_type"] = asset_type
            item["_name"] = name
            item["_source_records"] = [{"raw_group": group, "raw_index": index}]
            candidates.append(item)
    return candidates


def _asset_type(value: Any, fallback: str | None) -> str:
    raw = _text(value).lower()
    if raw:
        normalized = _TYPE_ALIASES.get(raw)
        if not normalized:
            raise AssetNormalizationError(f"unknown asset type: {value}")
        if fallback and normalized != fallback:
            raise AssetNormalizationError(f"asset type {value} conflicts with source group {fallback}")
        return normalized
    if fallback:
        return fallback
    raise AssetNormalizationError("asset_type is required for assets[] items")


def _dedupe_candidates(candidates: list[dict[str, Any]]) -> list[dict[str, Any]]:
    output: list[dict[str, Any]] = []
    aliases: dict[tuple[str, str, str], int] = {}
    for item in candidates:
        asset_type = item["_asset_type"]
        name_key = _name_key(item["_name"])
        client_key = _text(item.get("client_asset_key"))
        raw_code = _candidate_code(item, asset_type)
        identities = [
            (asset_type, "client", client_key),
            (asset_type, "code", raw_code),
            (asset_type, "name", name_key),
        ]
        identities = [identity for identity in identities if identity[2]]
        matched = {aliases[identity] for identity in identities if identity in aliases}
        if len(matched) > 1:
            raise AssetNormalizationError(f"conflicting identities for {item['_name']}")
        if matched:
            output_index = matched.pop()
            output[output_index] = _merge_candidate(output[output_index], item)
        else:
            output_index = len(output)
            output.append(item)
        for identity in identities:
            aliases[identity] = output_index
    return output


def _merge_candidate(left: dict[str, Any], right: dict[str, Any]) -> dict[str, Any]:
    merged = dict(left)
    merged["_source_records"] = [*left["_source_records"], *right["_source_records"]]
    for key, value in right.items():
        if key.startswith("_") or value in (None, "", [], {}):
            continue
        if key not in merged or merged[key] in (None, "", [], {}):
            merged[key] = value
        elif isinstance(merged[key], list) and isinstance(value, list):
            merged[key] = _unique([*merged[key], *value])
        elif key == "metadata" and isinstance(merged[key], Mapping) and isinstance(value, Mapping):
            merged[key] = _merge_mapping(merged[key], value)
    return merged


def _merge_mapping(left: Mapping[str, Any], right: Mapping[str, Any]) -> dict[str, Any]:
    merged = dict(left)
    for key, value in right.items():
        if key not in merged or merged[key] in (None, "", [], {}):
            merged[key] = value
        elif isinstance(merged[key], list) and isinstance(value, list):
            merged[key] = _unique([*merged[key], *value])
    return merged


def _assign_candidate_codes(items: list[dict[str, Any]], project_prefix: str) -> None:
    occupied: dict[str, set[int]] = {asset_type: set() for asset_type in _TYPE_PREFIX}
    for item in items:
        asset_type = item["_asset_type"]
        number = _candidate_number(item, asset_type)
        if number is not None and number not in occupied[asset_type]:
            item["_candidate_number"] = number
            occupied[asset_type].add(number)
        else:
            item["_candidate_number"] = None
    for item in items:
        asset_type = item["_asset_type"]
        number = item["_candidate_number"]
        if number is None:
            number = 1
            while number in occupied[asset_type]:
                number += 1
            occupied[asset_type].add(number)
            item["_candidate_number"] = number
        display_code = f"{_TYPE_PREFIX[asset_type]}{number:03d}"
        item["_display_code"] = display_code
        item["_asset_code"] = f"{project_prefix}-{display_code}"


def _build_asset(item: dict[str, Any], ctx: AssetNormalizationContextV1, project_prefix: str) -> AssetNormalizationV1:
    asset_type = item["_asset_type"]
    name = item["_name"]
    client_key = _text(item.get("client_asset_key")) or _stable_client_key(asset_type, name)
    repairs: list[NormalizationRepairV1] = []
    raw_code = _text(_pick(item, "asset_code", "role_code", "scene_code", "prop_code"))
    if raw_code.upper() != item["_asset_code"]:
        repairs.append(NormalizationRepairV1(
            field="asset_code",
            action="canonicalize",
            before=raw_code or None,
            after=item["_asset_code"],
            reason="asset codes use the project-prefixed candidate format",
        ))
    metadata = item.get("metadata") if isinstance(item.get("metadata"), Mapping) else {}
    source_record = item["_source_records"][0]
    source = AssetSourceV1(
        source_kind=ctx.source_kind,
        agent_run_id=ctx.agent_run_id,
        skill_name=ctx.skill_name,
        raw_group=source_record["raw_group"],
        raw_index=source_record["raw_index"],
    )
    display_code = item["_display_code"]
    asset = AssetNormalizationV1(
        client_asset_key=client_key,
        asset_code=item["_asset_code"],
        display_code=display_code,
        display_label=f"{display_code}-{name}",
        asset_type=asset_type,
        name=name,
        description=_optional_text(_field(item, metadata, "description", "scene_description")),
        priority=_priority(_field(item, metadata, "priority", "visual_priority")),
        attributes=_attributes(item, metadata, asset_type),
        source=source,
        repairs=repairs,
    )
    asset.review_items = _review_items(
        item.get("manual_review_items") or item.get("review_items"),
        asset,
        default_source=ctx.skill_name or "agent",
    )
    return asset


def _attributes(item: Mapping[str, Any], metadata: Mapping[str, Any], asset_type: str) -> Any:
    nested = item.get("attributes") if isinstance(item.get("attributes"), Mapping) else {}
    sources = (item, nested, metadata)
    if asset_type == "character":
        raw_presence = _first(sources, "visual_presence", "presence_mode", "appearance_scope")
        return CharacterAttributesV1(
            character_type=_optional_text(_first(sources, "character_type")),
            visual_presence=_visual_presence(raw_presence),
            role=_optional_text(_first(sources, "role", "dramatic_function")),
            gender=_optional_text(_first(sources, "gender")),
            age=_optional_text(_first(sources, "age")),
            appearance=_optional_text(_first(sources, "appearance", "appearance_clues")),
            core_requirement=_optional_text(_first(sources, "core_requirement")),
            personality=_optional_text(_first(sources, "personality")),
            background=_optional_text(_first(sources, "background")),
            has_dialogue=bool(_first(sources, "has_dialogue")),
            dialogue_evidence=_text_list(_first(sources, "dialogue_evidence")),
            context_codes=_text_list(_first(sources, "context_codes")),
        )
    if asset_type == "scene":
        render_mode = _optional_text(_first(sources, "render_mode"))
        if render_mode not in {None, "animated", "live_action"}:
            render_mode = None
        return SceneAttributesV1(
            context_code=_optional_text(_first(sources, "context_code")),
            render_mode=render_mode,
            scene_no=_optional_text(_first(sources, "scene_no")),
            interior_exterior=_optional_text(_first(sources, "interior_exterior")),
            time=_optional_text(_first(sources, "time")),
            location=_optional_text(_first(sources, "location")),
            atmosphere=_optional_text(_first(sources, "atmosphere")),
            narrative_function=_optional_text(_first(sources, "narrative_function", "dramatic_function")),
            spatial_zones=_text_list(_first(sources, "spatial_zones", "zones")),
            visual_goal=_optional_text(_first(sources, "visual_goal", "visual_direction")),
            key_props=_text_list(_first(sources, "key_props")),
            key_prop_codes=_text_list(_first(sources, "key_prop_codes", "key_prop_ids")),
            continuity_risks=_text_list(_first(sources, "continuity_risks")),
            episode_code=_optional_text(_first(sources, "episode_code")),
        )
    return PropAttributesV1(
        level=_optional_text(_first(sources, "level")),
        prop_type=_optional_text(_first(sources, "prop_type")),
        appearance=_optional_text(_first(sources, "appearance", "visual_anchor")),
        source=_optional_text(_first(sources, "source")),
        status_change=_optional_text(_first(sources, "status_change")),
        variant_states=_text_list(_first(sources, "variant_states")),
        usage_scene=_optional_text(_first(sources, "usage_scene")),
        scene_description=_optional_text(_first(sources, "scene_description")),
        context_codes=_text_list(_first(sources, "context_codes")),
    )


def _resolve_relations(assets: list[AssetNormalizationV1], raw_items: list[dict[str, Any]], project_prefix: str) -> None:
    by_type_and_code: dict[tuple[str, str], AssetNormalizationV1] = {}
    by_type_and_name: dict[tuple[str, str], AssetNormalizationV1] = {}
    for asset in assets:
        by_type_and_code[(asset.asset_type, asset.asset_code.upper())] = asset
        by_type_and_code[(asset.asset_type, asset.display_code.upper())] = asset
        by_type_and_name[(asset.asset_type, _name_key(asset.name))] = asset

    for asset, item in zip(assets, raw_items, strict=True):
        metadata = item.get("metadata") if isinstance(item.get("metadata"), Mapping) else {}
        nested = item.get("relations") if isinstance(item.get("relations"), Mapping) else {}
        sources = (item, nested, metadata)
        scene_refs = _relation_values(_first(sources, "scenes", "related_scenes", "related_scene_codes", "scene_codes", "scene_ids"))
        scene_names = _text_list(_first(sources, "scene_names", "related_scene_names"))
        scenes = _asset_targets(scene_refs, scene_names, "scene", by_type_and_code, by_type_and_name, project_prefix)
        storyboard_codes = _relation_codes(_first(sources, "storyboards", "related_storyboard_codes", "storyboard_codes"))
        script_codes = _relation_codes(_first(sources, "script_segments", "related_script_segment_codes", "script_segment_codes"))
        owner_code = _optional_text(_first(sources, "owner_character_code", "owner_character_id", "owner_role_code"))
        owner_name = _optional_text(_first(sources, "owner_character_name", "owner_character"))
        owner = _single_asset_target(owner_code, owner_name, "character", by_type_and_code, by_type_and_name, project_prefix)
        confidence = _text(_first(sources, "confidence", "relation_confidence")).lower()
        evidence = _evidence(_first(sources, "evidence", "relation_evidence"))
        asset.relations = AssetRelationsV1(
            relationship_text=_optional_text(_first(sources, "relationship_text", "relationship")),
            scene_links=scenes,
            owner_character=owner,
            storyboard_codes=[_external_code(code, project_prefix) for code in storyboard_codes],
            script_segment_codes=[_external_code(code, project_prefix) for code in script_codes],
            evidence=evidence,
            confidence=confidence if confidence in _CONFIDENCE_VALUES else "unknown",
        )


def _asset_targets(
    codes: list[str],
    names: list[str],
    asset_type: str,
    by_code: Mapping[tuple[str, str], AssetNormalizationV1],
    by_name: Mapping[tuple[str, str], AssetNormalizationV1],
    project_prefix: str,
) -> list[AssetLinkV1]:
    targets: list[AssetLinkV1] = []
    seen: set[str] = set()
    for index in range(max(len(codes), len(names))):
        code = codes[index] if index < len(codes) else None
        name = names[index] if index < len(names) else None
        target = _single_asset_target(code, name, asset_type, by_code, by_name, project_prefix)
        if target and target.target_asset_key not in seen:
            targets.append(target)
            seen.add(target.target_asset_key)
    return targets


def _single_asset_target(
    code: str | None,
    name: str | None,
    asset_type: str,
    by_code: Mapping[tuple[str, str], AssetNormalizationV1],
    by_name: Mapping[tuple[str, str], AssetNormalizationV1],
    project_prefix: str,
) -> AssetLinkV1 | None:
    candidate = None
    if code:
        normalized_code = _full_reference_code(code, asset_type, project_prefix)
        candidate = by_code.get((asset_type, normalized_code)) or by_code.get((asset_type, _display_code(normalized_code)))
    if candidate is None and name:
        candidate = by_name.get((asset_type, _name_key(name)))
    if candidate is None:
        return None
    return AssetLinkV1(
        target_asset_key=candidate.client_asset_key,
        target_asset_code=candidate.asset_code,
        target_display_code=candidate.display_code,
        target_display_label=candidate.display_label,
    )


def _external_code(code: str, project_prefix: str) -> str:
    normalized = _text(code).upper()
    if normalized and "-" not in normalized:
        normalized = f"{project_prefix}-{normalized}"
    return normalized


def _bind_global_review_items(
    assets: list[AssetNormalizationV1],
    raw_reviews: Any,
) -> list[GlobalAssetReviewItemV1]:
    reviews = _mapping_list(raw_reviews)
    if not reviews:
        return []
    by_code = {value.upper(): asset for asset in assets for value in (asset.asset_code, asset.display_code)}
    by_key = {asset.client_asset_key: asset for asset in assets}
    by_name = {_name_key(asset.name): asset for asset in assets}
    by_type_and_name = {(asset.asset_type, _name_key(asset.name)): asset for asset in assets}
    global_items: list[GlobalAssetReviewItemV1] = []
    for raw in reviews:
        explicit_key = _text(raw.get("client_asset_key"))
        explicit_code = _text(_pick(raw, "asset_code", "code", "target_code"))
        explicit_name = _text(_pick(raw, "asset_name", "name"))
        field = _text(raw.get("field"))
        field_scope, field_name = _split_review_field(field)
        legacy_type, legacy_name, legacy_field = _legacy_review_field_target(field)
        asset = by_key.get(explicit_key)
        asset = asset or by_code.get(explicit_code.upper())
        asset = asset or by_code.get(field_scope.upper())
        asset = asset or by_name.get(_name_key(explicit_name))
        asset = asset or by_type_and_name.get((legacy_type, _name_key(legacy_name)))
        if asset is None:
            global_items.append(_global_review_item(raw, field))
            continue
        normalized = _review_items(
            [raw],
            asset,
            default_source="agent",
            field_override=legacy_field if legacy_type else field_name,
        )
        asset.review_items = _dedupe_reviews([*asset.review_items, *normalized])
    return _dedupe_global_reviews(global_items)


def _legacy_review_field_target(field: str) -> tuple[str, str, str | None]:
    parts = [part.strip() for part in field.split(".") if part.strip()]
    if len(parts) < 2:
        return "", "", None
    asset_type = _TYPE_ALIASES.get(parts[0].lower(), "")
    return asset_type, parts[1], ".".join(parts[2:]) or "asset"


def _global_review_item(raw: Mapping[str, Any], field: str) -> GlobalAssetReviewItemV1:
    target_field = field or _text(raw.get("target_field")) or "inventory"
    item = _text(_pick(raw, "item", "question", "uncertainty", "message")) or f"{target_field}待确认"
    detail = _optional_text(_pick(raw, "detail", "uncertainty", "question", "message"))
    severity = _text(raw.get("severity")).lower()
    severity = severity if severity in {"warning", "error"} else "warning"
    requirement_level = _text(raw.get("requirement_level")).lower()
    if requirement_level not in {"required", "optional"}:
        requirement_level = "required" if bool(raw.get("requires_review", True)) else "optional"
    digest = _hash_text("inventory", target_field, item, detail or "")[:16]
    return GlobalAssetReviewItemV1(
        review_id=f"global-review:{digest}",
        target_field=target_field,
        issue=detail or item,
        suggestion=_optional_text(raw.get("suggestion")),
        status=_text(raw.get("status")) or "pending",
        severity=severity,
        requirement_level=requirement_level,
        source=_text(raw.get("source")) or "agent",
        source_path=_optional_text(raw.get("source_path")) or (field or None),
        human_input=_optional_text(raw.get("human_input")),
    )


def _dedupe_global_reviews(values: list[GlobalAssetReviewItemV1]) -> list[GlobalAssetReviewItemV1]:
    output: list[GlobalAssetReviewItemV1] = []
    seen: set[str] = set()
    for item in values:
        if item.review_id in seen:
            continue
        output.append(item)
        seen.add(item.review_id)
    return output


def _review_items(
    values: Any,
    asset: AssetNormalizationV1,
    *,
    default_source: str,
    field_override: str | None = None,
) -> list[AssetReviewItemV1]:
    output: list[AssetReviewItemV1] = []
    for raw in _mapping_list(values):
        field = field_override or _text(_pick(raw, "target_field", "field")) or "asset"
        item = _text(_pick(raw, "item", "question", "uncertainty", "message")) or f"{field}待确认"
        detail = _optional_text(_pick(raw, "detail", "uncertainty", "question", "message"))
        issue = detail or item
        suggestion = _optional_text(raw.get("suggestion"))
        severity = _text(raw.get("severity")).lower()
        severity = severity if severity in {"warning", "error"} else "warning"
        digest = _hash_text(asset.client_asset_key, field, item, detail or "")[:16]
        requirement_level = _text(raw.get("requirement_level")).lower()
        if requirement_level not in {"required", "optional"}:
            requirement_level = "required" if bool(raw.get("requires_review", True)) else "optional"
        output.append(AssetReviewItemV1(
            review_id=f"review:{digest}",
            asset_key=asset.client_asset_key,
            target_field=field,
            issue=issue,
            suggestion=suggestion,
            status=_text(raw.get("status")) or "pending",
            severity=severity,
            requirement_level=requirement_level,
            source=_text(raw.get("source")) or default_source,
            source_path=_optional_text(raw.get("source_path")),
            human_input=_optional_text(raw.get("human_input")),
        ))
    return _dedupe_reviews(output)


def _dedupe_reviews(values: list[AssetReviewItemV1]) -> list[AssetReviewItemV1]:
    return list({item.review_id: item for item in values}.values())


def _evidence(value: Any) -> list[RelationEvidenceV1]:
    output: list[RelationEvidenceV1] = []
    for item in _mapping_list(value):
        output.append(RelationEvidenceV1(
            target_code=_optional_text(_pick(item, "target_code", "scene_code")),
            storyboard_code=_optional_text(item.get("storyboard_code")),
            script_segment_code=_optional_text(item.get("script_segment_code")),
            source_field=_optional_text(item.get("source_field")),
            excerpt=_optional_text(_pick(item, "excerpt", "evidence", "source_text")),
        ))
    return output


def _relation_values(value: Any) -> list[str]:
    values: list[str] = []
    for item in value if isinstance(value, list) else ([value] if value not in (None, "") else []):
        if isinstance(item, Mapping):
            code = _text(_pick(item, "code", "asset_code", "scene_code"))
            if code:
                values.append(code)
        elif _text(item):
            values.append(_text(item))
    return _unique(values)


def _relation_codes(value: Any) -> list[str]:
    values: list[str] = []
    for item in value if isinstance(value, list) else ([value] if value not in (None, "") else []):
        if isinstance(item, Mapping):
            code = _text(_pick(item, "code", "storyboard_code", "script_segment_code"))
        else:
            code = _text(item)
        if code:
            values.append(code)
    return _unique(values)


def _candidate_code(item: Mapping[str, Any], asset_type: str) -> str:
    raw = _text(_pick(item, "asset_code", "role_code", "scene_code", "prop_code")).upper()
    number = _code_number(raw, asset_type)
    return f"{_TYPE_PREFIX[asset_type]}{number:03d}" if number is not None else ""


def _candidate_number(item: Mapping[str, Any], asset_type: str) -> int | None:
    return _code_number(_text(_pick(item, "asset_code", "role_code", "scene_code", "prop_code")).upper(), asset_type)


def _code_number(code: str, asset_type: str) -> int | None:
    if not code:
        return None
    match = re.search(r"(?:^|-)(SC|S|R|P)(\d+)$", code.upper())
    if not match:
        return None
    code_type = {"SC": "scene", "S": "scene", "R": "character", "P": "prop"}[match.group(1)]
    return int(match.group(2)) if code_type == asset_type else None


def _full_reference_code(code: str, asset_type: str, project_prefix: str) -> str:
    number = _code_number(_text(code).upper(), asset_type)
    if number is None:
        return _text(code).upper()
    return f"{project_prefix}-{_TYPE_PREFIX[asset_type]}{number:03d}"


def _display_code(code: str) -> str:
    return code.rsplit("-", 1)[-1]


def _project_prefix(value: str) -> str:
    prefix = re.sub(r"[^A-Z0-9]", "", _text(value).upper())
    if not prefix:
        raise AssetNormalizationError("project_prefix must contain letters or digits")
    return prefix


def _stable_client_key(asset_type: str, name: str) -> str:
    return f"asset:{asset_type}:{_hash_text(asset_type, _name_key(name))[:20]}"


def _name_key(value: Any) -> str:
    return re.sub(r"[\s\-_/（）()【】\[\]]+", "", _text(value)).casefold()


def _hash_text(*values: str) -> str:
    return hashlib.sha256("\x1f".join(values).encode("utf-8")).hexdigest()


def _priority(value: Any) -> str:
    return _PRIORITY_ALIASES.get(_text(value).upper(), "C")


def _visual_presence(value: Any) -> str:
    raw = _text(value).lower()
    return _VISUAL_PRESENCE_ALIASES.get(raw, "on_screen")


def _split_review_field(value: str) -> tuple[str, str | None]:
    if not value:
        return "", None
    parts = re.split(r"[.:/]", value, maxsplit=1)
    if len(parts) == 2 and re.search(r"(?:SC|R|P)\d+$", parts[0], re.IGNORECASE):
        return parts[0], parts[1]
    return "", value


def _field(item: Mapping[str, Any], metadata: Mapping[str, Any], *keys: str) -> Any:
    return _first((item, metadata), *keys)


def _first(sources: tuple[Mapping[str, Any], ...], *keys: str) -> Any:
    for source in sources:
        for key in keys:
            if key in source and source[key] not in (None, "", [], {}):
                return source[key]
    return None


def _pick(source: Mapping[str, Any], *keys: str) -> Any:
    return _first((source,), *keys)


def _text(value: Any) -> str:
    return str(value or "").strip()


def _optional_text(value: Any) -> str | None:
    text = _text(value)
    return text or None


def _text_list(value: Any) -> list[str]:
    if value in (None, ""):
        return []
    values = value if isinstance(value, list) else [value]
    return _unique([_text(item) for item in values if _text(item)])


def _mapping_list(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    return [dict(item) for item in value if isinstance(item, Mapping)]


def _unique(values: list[Any]) -> list[Any]:
    output: list[Any] = []
    seen: set[str] = set()
    for value in values:
        marker = json.dumps(value, ensure_ascii=False, sort_keys=True) if isinstance(value, (dict, list)) else str(value)
        if marker not in seen:
            seen.add(marker)
            output.append(value)
    return output

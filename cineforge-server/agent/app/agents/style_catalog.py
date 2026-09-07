from __future__ import annotations

import copy
import json
from functools import lru_cache
from pathlib import Path
from typing import Any


CATALOG_PATH = Path(__file__).with_name("references") / "production_style_catalog.v1.json"


@lru_cache(maxsize=1)
def _cached_catalog() -> dict[str, Any]:
    catalog = json.loads(CATALOG_PATH.read_text(encoding="utf-8"))
    _validate_catalog(catalog)
    return catalog


def get_production_style_catalog() -> dict[str, Any]:
    return copy.deepcopy(_cached_catalog())


def get_primary_style(style_id: str | None) -> dict[str, Any] | None:
    target = str(style_id or "").strip()
    return next((copy.deepcopy(item) for item in _cached_catalog()["primary_styles"] if item["style_id"] == target), None)


def get_style_modifier(modifier_id: str | None) -> dict[str, Any] | None:
    target = str(modifier_id or "").strip()
    return next((copy.deepcopy(item) for item in _cached_catalog()["modifiers"] if item["modifier_id"] == target), None)


def normalize_style_selection(profile: dict[str, Any] | None) -> dict[str, Any] | None:
    if profile is None:
        return None
    output = dict(profile)
    style_id = str(output.get("primary_style_id") or "").strip()
    modifiers = list(dict.fromkeys(str(item).strip() for item in output.get("style_modifiers") or [] if str(item).strip()))
    max_modifiers = int(_cached_catalog().get("max_modifiers") or 2)
    if style_id and get_primary_style(style_id) is None:
        raise ValueError(f"Unknown production style: {style_id}")
    unknown_modifiers = [item for item in modifiers if get_style_modifier(item) is None]
    if unknown_modifiers:
        raise ValueError(f"Unknown production style modifiers: {', '.join(unknown_modifiers)}")
    if len(modifiers) > max_modifiers:
        raise ValueError(f"At most {max_modifiers} production style modifiers are allowed")
    output["style_catalog_version"] = _cached_catalog()["catalog_version"]
    output["primary_style_id"] = style_id
    output["style_modifiers"] = modifiers
    output["style_selection_source"] = "human_confirmed" if style_id else "agent_recommendation_requested"
    return output


def resolved_style_selection(profile: dict[str, Any] | None) -> dict[str, Any]:
    normalized = normalize_style_selection(profile) or {}
    primary = get_primary_style(normalized.get("primary_style_id"))
    modifiers = [get_style_modifier(item) for item in normalized.get("style_modifiers") or []]
    return {
        "catalog_version": _cached_catalog()["catalog_version"],
        "primary_style": primary or {},
        "modifiers": [item for item in modifiers if item],
        "selection_source": normalized.get("style_selection_source") or "agent_recommendation_requested",
    }


def _validate_catalog(catalog: dict[str, Any]) -> None:
    if catalog.get("schema_version") != "production_style_catalog.v1":
        raise ValueError("Unsupported production style catalog schema")
    primary_ids = [str(item.get("style_id") or "") for item in catalog.get("primary_styles") or []]
    modifier_ids = [str(item.get("modifier_id") or "") for item in catalog.get("modifiers") or []]
    if not primary_ids or any(not item for item in primary_ids) or len(primary_ids) != len(set(primary_ids)):
        raise ValueError("Production style IDs must be non-empty and unique")
    if any(not item for item in modifier_ids) or len(modifier_ids) != len(set(modifier_ids)):
        raise ValueError("Production style modifier IDs must be non-empty and unique")

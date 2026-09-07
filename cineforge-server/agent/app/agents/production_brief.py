from __future__ import annotations

import copy
import json
from functools import lru_cache
from pathlib import Path
from typing import Any

from app.agents.style_catalog import get_production_style_catalog


CATALOG_PATH = Path(__file__).with_name("references") / "production_brief_catalog.v1.json"
LIVE_ACTION_STYLE_ID = "live_action_cinematic"
ANIMATED_CONTENT_TYPES = {"animated_drama", "hybrid_drama"}


@lru_cache(maxsize=1)
def _catalog() -> dict[str, Any]:
    with CATALOG_PATH.open("r", encoding="utf-8") as handle:
        catalog = json.load(handle)
    if catalog.get("schema_version") != "production_brief_catalog.v1":
        raise ValueError("Unsupported production brief catalog")
    return catalog


def get_production_brief_catalog() -> dict[str, Any]:
    catalog = copy.deepcopy(_catalog())
    styles = get_production_style_catalog()
    catalog["styles"] = styles.get("primary_styles") or []
    catalog["style_catalog_version"] = styles.get("catalog_version")
    return catalog


def culture_context_by_code(code: str | None) -> dict[str, Any] | None:
    target = str(code or "").strip().upper()
    return next(
        (copy.deepcopy(item) for item in _catalog().get("cultural_contexts") or [] if item.get("context_code") == target),
        None,
    )


def rhythm_profile(profile_id: str | None) -> dict[str, Any] | None:
    target = str(profile_id or "").strip()
    return next(
        (copy.deepcopy(item) for item in _catalog().get("rhythm_profiles") or [] if item.get("profile_id") == target),
        None,
    )


def aspect_profile(aspect_ratio: str | None) -> dict[str, Any] | None:
    target = str(aspect_ratio or "").strip()
    return next(
        (copy.deepcopy(item) for item in _catalog().get("delivery_aspect_ratios") or [] if item.get("value") == target),
        None,
    )

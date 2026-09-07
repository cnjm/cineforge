from __future__ import annotations

import re
from dataclasses import dataclass
from collections.abc import Mapping
from typing import Any

from app.models.enums import AssetType
from app.schemas.domain import AssetRead


@dataclass(frozen=True)
class AssetMatchResult:
    matches: list[dict[str, Any]]
    missing_assets: list[dict[str, Any]]

    def to_dict(self) -> dict[str, Any]:
        return {
            "matches": self.matches,
            "missing_assets": self.missing_assets,
        }


def match_existing_assets(
    required_assets: list[dict[str, Any]],
    existing_assets: list[AssetRead | Mapping[str, Any]],
    *,
    min_score: float = 0.62,
) -> AssetMatchResult:
    matches: list[dict[str, Any]] = []
    missing: list[dict[str, Any]] = []

    for required in required_assets:
        required_type = _asset_type_value(required.get("asset_type") or required.get("type"))
        required_name = str(required.get("name") or "").strip()
        if not required_type or not required_name:
            missing.append(_missing(required, "缺少资产类型或名称，无法匹配本地资产库"))
            continue

        candidates = [asset for asset in existing_assets if _asset_type_value(_asset_value(asset, "asset_type")) == required_type]
        scored = [
            (_score_required_asset(required, asset), asset)
            for asset in candidates
        ]
        scored.sort(key=lambda item: item[0], reverse=True)
        best_score, best_asset = scored[0] if scored else (0.0, None)

        if best_asset is not None and best_score >= min_score:
            matches.append(_match(required, best_asset, best_score))
        else:
            missing.append(_missing(required, "本地资产库没有达到匹配阈值的同类型资产"))

    return AssetMatchResult(matches=matches, missing_assets=missing)


def _score_required_asset(required: dict[str, Any], asset: AssetRead | Mapping[str, Any]) -> float:
    required_name = str(required.get("name") or "")
    required_description = str(required.get("description") or required.get("stable_prompt") or "")
    asset_description = " ".join(
        str(value or "")
        for value in (
            _asset_value(asset, "name"),
            _asset_value(asset, "description"),
            _asset_value(asset, "prompt_text"),
            " ".join(_asset_value(asset, "tags", []) or []),
        )
    )
    asset_name = str(_asset_value(asset, "name") or "")
    metadata = _asset_metadata(asset)
    name_score = _token_similarity(required_name, asset_name)
    description_score = _token_similarity(required_description, asset_description)
    exact_bonus = 0.2 if required_name and required_name == asset_name else 0.0
    code_bonus = 0.1 if required.get("asset_code") and required.get("asset_code") == metadata.get("source_asset_code") else 0.0
    return min(1.0, name_score * 0.65 + description_score * 0.25 + exact_bonus + code_bonus)


def _match(required: dict[str, Any], asset: AssetRead | Mapping[str, Any], score: float) -> dict[str, Any]:
    metadata = _asset_metadata(asset)
    return {
        "required_asset_code": required.get("asset_code"),
        "required_name": required.get("name"),
        "required_asset_type": _asset_type_value(required.get("asset_type") or required.get("type")),
        "matched_asset_id": str(_asset_value(asset, "id") or _asset_value(asset, "asset_id") or ""),
        "matched_asset_code": _asset_value(asset, "asset_code") or metadata.get("asset_code"),
        "matched_name": str(_asset_value(asset, "name") or ""),
        "matched_asset_type": _asset_type_value(_asset_value(asset, "asset_type")),
        "match_score": round(score, 4),
        "match_reason": _match_reason(required, asset, score),
    }


def _missing(required: dict[str, Any], reason: str) -> dict[str, Any]:
    return {
        "required_asset_code": required.get("asset_code"),
        "name": required.get("name"),
        "asset_type": _asset_type_value(required.get("asset_type") or required.get("type")),
        "description": required.get("description"),
        "reason": reason,
    }


def _match_reason(required: dict[str, Any], asset: AssetRead | Mapping[str, Any], score: float) -> str:
    required_name = str(required.get("name") or "")
    if required_name and required_name == str(_asset_value(asset, "name") or ""):
        return "名称完全一致，资产类型一致"
    if score >= 0.78:
        return "名称或描述高度相似，资产类型一致"
    return "名称或描述部分相似，资产类型一致，建议人工确认"


def _token_similarity(left: str, right: str) -> float:
    left_tokens = _tokens(left)
    right_tokens = _tokens(right)
    if not left_tokens or not right_tokens:
        return 0.0
    overlap = len(left_tokens & right_tokens)
    union = len(left_tokens | right_tokens)
    return overlap / union if union else 0.0


def _tokens(value: str) -> set[str]:
    text = value.lower().strip()
    if not text:
        return set()
    words = {item for item in re.split(r"[\s,，。；;:/\\|、（）()《》【】\[\]{}<>\"']+", text) if item}
    cjk_chars = {char for char in text if "\u4e00" <= char <= "\u9fff"}
    return words | cjk_chars


def _asset_type_value(value: Any) -> str:
    if isinstance(value, AssetType):
        return value.value
    normalized = str(value or "").strip().lower()
    aliases = {
        "人物": "character",
        "角色": "character",
        "character": "character",
        "scene": "scene",
        "场景": "scene",
        "prop": "prop",
        "道具": "prop",
    }
    return aliases.get(normalized, normalized)


def _asset_value(asset: AssetRead | Mapping[str, Any], key: str, default: Any = None) -> Any:
    if isinstance(asset, Mapping):
        return asset.get(key, default)
    return getattr(asset, key, default)


def _asset_metadata(asset: AssetRead | Mapping[str, Any]) -> dict[str, Any]:
    value = _asset_value(asset, "metadata")
    if not isinstance(value, dict):
        value = _asset_value(asset, "metadata_json")
    return dict(value) if isinstance(value, dict) else {}

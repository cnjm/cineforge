from __future__ import annotations

from typing import Any


def validate_breakdown_relations(content: dict[str, Any]) -> dict[str, Any]:
    segments = _dict_list(content.get("script_segments"))
    storyboards = _dict_list(content.get("storyboards"))
    assets = _dedupe_assets(
        [
            *_dict_list(content.get("assets")),
            *_dict_list(content.get("characters")),
            *_dict_list(content.get("scenes")),
            *_dict_list(content.get("props")),
        ]
    )
    segment_codes = _codes(segments, "script_segment_code")
    storyboard_codes = _codes(storyboards, "storyboard_code")
    asset_codes = _codes(assets, "asset_code")
    errors: list[dict[str, Any]] = []
    warnings: list[dict[str, Any]] = []

    for storyboard in storyboards:
        storyboard_code = str(storyboard.get("storyboard_code") or "").strip()
        segment_code = str(storyboard.get("script_segment_code") or "").strip()
        if not segment_code or segment_code not in segment_codes:
            errors.append(_issue("invalid_storyboard_segment", storyboard_code, "分镜引用了不存在的脚本段", segment_code))
        for asset_code in _storyboard_asset_refs(storyboard):
            if asset_code not in asset_codes:
                errors.append(_issue("invalid_storyboard_asset", storyboard_code, "分镜引用了不存在的正式资产", asset_code))

    for asset in assets:
        asset_code = str(asset.get("asset_code") or "").strip()
        for segment_code in _text_list(asset.get("related_script_segment_codes")):
            if segment_code not in segment_codes:
                errors.append(_issue("invalid_asset_segment", asset_code, "资产引用了不存在的脚本段", segment_code))
        for storyboard_code in _text_list(asset.get("related_storyboard_codes")):
            if storyboard_code not in storyboard_codes:
                errors.append(_issue("invalid_asset_storyboard", asset_code, "资产引用了不存在的分镜", storyboard_code))
        if storyboards and not _text_list(asset.get("related_storyboard_codes")):
            warnings.append(_issue("asset_without_storyboard", asset_code, "资产尚未关联分镜", ""))

    return {
        "ok": not errors,
        "status": "passed" if not errors else "blocked",
        "checked_counts": {
            "script_segments": len(segments),
            "storyboards": len(storyboards),
            "assets": len(assets),
        },
        "blocking_errors": errors,
        "warnings": warnings,
    }


def _storyboard_asset_refs(storyboard: dict[str, Any]) -> list[str]:
    refs = [
        *_text_list(storyboard.get("asset_codes")),
        *_text_list(storyboard.get("role_refs")),
        *_text_list(storyboard.get("prop_refs")),
    ]
    scene_code = str(storyboard.get("scene_code") or "").strip()
    if scene_code:
        refs.append(scene_code)
    return list(dict.fromkeys(ref for ref in refs if _looks_like_asset_code(ref)))


def _looks_like_asset_code(value: str) -> bool:
    bare = value.rsplit("-", 1)[-1]
    return bare.startswith(("R", "SC", "P")) and any(char.isdigit() for char in bare)


def _dedupe_assets(items: list[dict[str, Any]]) -> list[dict[str, Any]]:
    by_code: dict[str, dict[str, Any]] = {}
    without_code: list[dict[str, Any]] = []
    for item in items:
        code = str(item.get("asset_code") or "").strip()
        if code:
            by_code.setdefault(code, item)
        else:
            without_code.append(item)
    return [*by_code.values(), *without_code]


def _codes(items: list[dict[str, Any]], key: str) -> set[str]:
    return {str(item.get(key) or "").strip() for item in items if str(item.get(key) or "").strip()}


def _dict_list(value: Any) -> list[dict[str, Any]]:
    return [dict(item) for item in value or [] if isinstance(item, dict)] if isinstance(value, list) else []


def _text_list(value: Any) -> list[str]:
    if not isinstance(value, list):
        return []
    return [str(item).strip() for item in value if str(item).strip()]


def _issue(code: str, scope_code: str, message: str, reference: str) -> dict[str, Any]:
    return {"code": code, "scope_code": scope_code, "message": message, "reference": reference}

from __future__ import annotations

from typing import Any

from app.agents.production_brief import (
    aspect_profile,
    culture_context_by_code,
)
from app.agents.style_catalog import get_primary_style
from app.schemas.production_brief import (
    EpisodeProductionBrief,
    ProjectProductionBrief,
    ResolvedProductionBrief,
)


def build_resolved_production_brief(
    *,
    project_title: str,
    project_prefix: str,
    production_brief: dict[str, Any],
    script_context: dict[str, Any],
) -> dict[str, Any]:
    project = ProjectProductionBrief.model_validate(production_brief)
    episode = EpisodeProductionBrief.model_validate(script_context.get("episode_production_brief"))
    aspect = aspect_profile(project.delivery_aspect_ratio)
    if aspect is None:
        raise ValueError(f"Unsupported delivery aspect ratio: {project.delivery_aspect_ratio}")
    style = get_primary_style(project.primary_style_id)
    if style is None:
        raise ValueError(f"Unknown production style: {project.primary_style_id}")

    contexts = []
    grammars: dict[str, str] = {}
    for item in project.cultural_contexts:
        catalog_item = culture_context_by_code(item.context_code) or {}
        grammar = item.narrative_grammar or catalog_item.get("narrative_grammar") or "cn_drama"
        normalized = item.model_copy(update={"narrative_grammar": grammar})
        contexts.append(normalized)
        grammars[item.context_code] = grammar

    resolved = ResolvedProductionBrief(
        project_title=project_title,
        project_prefix=project_prefix,
        episode_code=str(script_context.get("episode_code") or "EP01"),
        script_language=str(script_context.get("language") or "unknown"),
        content_type=project.content_type,
        delivery_aspect_ratio=project.delivery_aspect_ratio,
        target_duration_seconds=episode.target_duration_seconds,
        opening_hook_seconds=int(aspect.get("opening_hook_seconds") or 3),
        primary_style_id=project.primary_style_id,
        style_catalog_version=project.style_catalog_version,
        resolved_style=style,
        cultural_contexts=contexts,
        primary_cultural_context_code=project.primary_cultural_context_code,
        narrative_grammar_by_context=grammars,
        field_sources={
            "project_title": "project_record",
            "project_prefix": "project_record",
            "episode_code": "script_version" if script_context.get("episode_code") else "system_default",
            "content_type": "project_production_brief",
            "delivery_aspect_ratio": "project_production_brief",
            "target_duration_seconds": "episode_production_brief",
            "primary_style_id": "project_production_brief",
            "style_catalog_version": "project_production_brief",
            "cultural_contexts": "project_production_brief",
            "primary_cultural_context_code": "project_production_brief",
            **project.field_sources,
            **episode.field_sources,
            "document_type": "system_default",
            "fidelity_mode": "system_default",
            "asset_master_aspect_ratio": "system_default",
            "opening_hook_seconds": "system_derived",
            "script_language": "script_version" if script_context.get("language") else "system_default",
            "resolved_style": "production_style_catalog",
            "narrative_grammar_by_context": "system_derived",
        },
    )
    return resolved.model_dump(mode="json")


def build_agent_execution_context(
    *,
    project_id: str,
    project_title: str,
    project_prefix: str,
    production_brief: dict[str, Any],
    script_context: dict[str, Any],
) -> dict[str, Any]:
    resolved = build_resolved_production_brief(
        project_title=project_title,
        project_prefix=project_prefix,
        production_brief=production_brief,
        script_context=script_context,
    )
    return {
        "project_id": project_id,
        "project_title": project_title,
        "project_prefix": project_prefix,
        "episode_num": int(script_context.get("episode_no") or 1),
        "episode_id": script_context.get("episode_id"),
        "episode_code": script_context.get("episode_code") or "EP01",
        "script_id": script_context.get("script_id"),
        "script_version_id": script_context.get("script_version_id"),
        "script_version_no": script_context.get("script_version_no"),
        "script_source": dict(script_context),
        "script_text": str(script_context.get("script_text") or ""),
        "production_brief": dict(production_brief),
        "episode_production_brief": dict(script_context.get("episode_production_brief") or {}),
        "resolved_production_brief": resolved,
    }

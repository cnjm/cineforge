from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, Field, model_validator

from app.agents.production_brief import LIVE_ACTION_STYLE_ID
from app.agents.style_catalog import get_primary_style, get_production_style_catalog


ContentType = Literal["animated_drama", "live_action_drama", "hybrid_drama"]
DeliveryAspectRatio = Literal["9:16", "16:9"]
WorldType = Literal["real_world", "fictional_history", "fictional_world"]


class CulturalContextBrief(BaseModel):
    context_code: str = Field(min_length=1, max_length=48)
    name: str = Field(min_length=1, max_length=120)
    region: str = Field(min_length=1, max_length=80)
    era: str = Field(min_length=1, max_length=80)
    world_type: WorldType
    timeline: str = Field(default="待围读识别", max_length=160)
    narrative_grammar: str | None = Field(default=None, max_length=48)


class ProjectProductionBrief(BaseModel):
    schema_version: Literal["ProjectProductionBrief.v1"] = "ProjectProductionBrief.v1"
    content_type: ContentType
    delivery_aspect_ratio: DeliveryAspectRatio
    primary_style_id: str = Field(min_length=1, max_length=80)
    style_catalog_version: str = Field(default_factory=lambda: str(get_production_style_catalog().get("catalog_version") or ""))
    cultural_contexts: list[CulturalContextBrief] = Field(min_length=1)
    primary_cultural_context_code: str = Field(min_length=1, max_length=48)
    field_sources: dict[str, str] = Field(default_factory=dict)

    @model_validator(mode="after")
    def validate_contract(self) -> "ProjectProductionBrief":
        codes = [item.context_code for item in self.cultural_contexts]
        if len(codes) != len(set(codes)):
            raise ValueError("cultural_contexts must use unique context_code values")
        if self.primary_cultural_context_code not in codes:
            raise ValueError("primary_cultural_context_code must reference cultural_contexts")
        style = get_primary_style(self.primary_style_id)
        if style is None:
            raise ValueError(f"Unknown production style: {self.primary_style_id}")
        is_live = style.get("medium") == "live_action"
        if self.content_type == "live_action_drama" and self.primary_style_id != LIVE_ACTION_STYLE_ID:
            raise ValueError("live_action_drama must use live_action_cinematic")
        if self.content_type in {"animated_drama", "hybrid_drama"} and is_live:
            raise ValueError("animated and hybrid projects must select a non-live-action style")
        return self


class EpisodeProductionBrief(BaseModel):
    schema_version: Literal["EpisodeProductionBrief.v1"] = "EpisodeProductionBrief.v1"
    target_duration_seconds: int = Field(ge=1, le=7200)
    field_sources: dict[str, str] = Field(default_factory=dict)


class ResolvedProductionBrief(BaseModel):
    schema_version: Literal["ResolvedProductionBrief.v1"] = "ResolvedProductionBrief.v1"
    project_title: str
    project_prefix: str
    episode_code: str
    script_language: str
    content_type: ContentType
    delivery_aspect_ratio: DeliveryAspectRatio
    target_duration_seconds: int
    asset_master_aspect_ratio: Literal["16:9"] = "16:9"
    opening_hook_seconds: int
    primary_style_id: str
    style_catalog_version: str
    resolved_style: dict = Field(default_factory=dict)
    cultural_contexts: list[CulturalContextBrief]
    primary_cultural_context_code: str
    narrative_grammar_by_context: dict[str, str] = Field(default_factory=dict)
    document_type: Literal["non_standard_script"] = "non_standard_script"
    fidelity_mode: Literal["strict_source"] = "strict_source"
    field_sources: dict[str, str] = Field(default_factory=dict)

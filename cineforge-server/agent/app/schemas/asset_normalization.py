from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator


NormalizationVersion = Literal["AssetNormalization.v1"]
AssetTypeV1 = Literal["character", "scene", "prop"]
AssetPriorityV1 = Literal["S", "A", "B", "C"]
AssetCodeStateV1 = Literal["candidate", "formal"]


class CharacterAttributesV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    character_type: str | None = None
    visual_presence: Literal["on_screen", "voice_only", "mentioned_only"] = "on_screen"
    role: str | None = None
    gender: str | None = None
    age: str | None = None
    appearance: str | None = None
    core_requirement: str | None = None
    personality: str | None = None
    background: str | None = None
    has_dialogue: bool = False
    dialogue_evidence: list[str] = Field(default_factory=list)
    context_codes: list[str] = Field(default_factory=list)


class SceneAttributesV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    context_code: str | None = None
    render_mode: Literal["animated", "live_action"] | None = None
    scene_no: str | None = None
    interior_exterior: str | None = None
    time: str | None = None
    location: str | None = None
    atmosphere: str | None = None
    narrative_function: str | None = None
    spatial_zones: list[str] = Field(default_factory=list)
    visual_goal: str | None = None
    key_props: list[str] = Field(default_factory=list)
    key_prop_codes: list[str] = Field(default_factory=list)
    continuity_risks: list[str] = Field(default_factory=list)
    episode_code: str | None = None


class PropAttributesV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    level: str | None = None
    prop_type: str | None = None
    appearance: str | None = None
    source: str | None = None
    status_change: str | None = None
    variant_states: list[str] = Field(default_factory=list)
    usage_scene: str | None = None
    scene_description: str | None = None
    context_codes: list[str] = Field(default_factory=list)


class AssetLinkV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    target_asset_key: str = Field(min_length=1)
    target_asset_code: str = Field(min_length=1)
    target_display_code: str = Field(min_length=1)
    target_display_label: str = Field(min_length=1)


class RelationEvidenceV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    target_code: str | None = None
    storyboard_code: str | None = None
    script_segment_code: str | None = None
    source_field: str | None = None
    excerpt: str | None = None


class AssetRelationsV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    relationship_text: str | None = None
    scene_links: list[AssetLinkV1] = Field(default_factory=list)
    owner_character: AssetLinkV1 | None = None
    storyboard_codes: list[str] = Field(default_factory=list)
    script_segment_codes: list[str] = Field(default_factory=list)
    evidence: list[RelationEvidenceV1] = Field(default_factory=list)
    confidence: Literal["high", "medium", "low", "unknown"] = "unknown"


class AssetReviewItemV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    review_id: str = Field(min_length=1)
    asset_key: str = Field(min_length=1)
    target_field: str = Field(min_length=1)
    issue: str = Field(min_length=1)
    suggestion: str | None = None
    status: str = "pending"
    severity: Literal["warning", "error"] = "warning"
    requirement_level: Literal["required", "optional"] = "required"
    source: str = "agent"
    source_path: str | None = None
    human_input: str | None = None


class GlobalAssetReviewItemV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    review_id: str = Field(min_length=1)
    target_scope: Literal["inventory"] = "inventory"
    target_field: str = Field(min_length=1)
    issue: str = Field(min_length=1)
    suggestion: str | None = None
    status: str = "pending"
    severity: Literal["warning", "error"] = "warning"
    requirement_level: Literal["required", "optional"] = "required"
    source: str = "agent"
    source_path: str | None = None
    human_input: str | None = None


class AssetSourceV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    source_kind: Literal["agent", "human", "confirmed_inventory"] = "agent"
    agent_run_id: str | None = None
    skill_name: str | None = None
    raw_group: Literal["assets", "characters", "scenes", "props"]
    raw_index: int = Field(ge=0)


class NormalizationRepairV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    field: str
    action: str
    before: Any = None
    after: Any = None
    reason: str


AssetAttributesV1 = CharacterAttributesV1 | SceneAttributesV1 | PropAttributesV1


class AssetNormalizationV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    normalization_version: NormalizationVersion = "AssetNormalization.v1"
    client_asset_key: str = Field(min_length=1)
    asset_code: str = Field(min_length=1)
    display_code: str = Field(min_length=1)
    display_label: str = Field(min_length=1)
    code_state: AssetCodeStateV1 = "candidate"
    asset_type: AssetTypeV1
    name: str = Field(min_length=1)
    description: str | None = None
    priority: AssetPriorityV1
    attributes: AssetAttributesV1
    relations: AssetRelationsV1 = Field(default_factory=AssetRelationsV1)
    review_items: list[AssetReviewItemV1] = Field(default_factory=list)
    source: AssetSourceV1
    repairs: list[NormalizationRepairV1] = Field(default_factory=list)

    @model_validator(mode="after")
    def attributes_match_asset_type(self) -> "AssetNormalizationV1":
        expected = {
            "character": CharacterAttributesV1,
            "scene": SceneAttributesV1,
            "prop": PropAttributesV1,
        }[self.asset_type]
        if not isinstance(self.attributes, expected):
            raise ValueError(f"{self.asset_type} requires {expected.__name__}")
        expected_display_prefix = {"character": "R", "scene": "SC", "prop": "P"}[self.asset_type]
        if not self.display_code.startswith(expected_display_prefix):
            raise ValueError(f"{self.asset_type} has an invalid display_code")
        if not self.asset_code.endswith(f"-{self.display_code}"):
            raise ValueError("asset_code and display_code must identify the same asset")
        if self.display_label != f"{self.display_code}-{self.name}":
            raise ValueError("display_label must be derived from display_code and name")
        if any(item.asset_key != self.client_asset_key for item in self.review_items):
            raise ValueError("review_items must belong to their containing asset")
        return self


class AssetNormalizationContextV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    project_prefix: str
    source_kind: Literal["agent", "human", "confirmed_inventory"] = "agent"
    agent_run_id: str | None = None
    skill_name: str | None = "asset-extract"


class AssetNormalizationResultV1(BaseModel):
    model_config = ConfigDict(extra="forbid")

    normalization_version: NormalizationVersion = "AssetNormalization.v1"
    assets: list[AssetNormalizationV1] = Field(default_factory=list)
    global_review_items: list[GlobalAssetReviewItemV1] = Field(default_factory=list)

    @model_validator(mode="after")
    def inventory_is_unique_and_closed(self) -> "AssetNormalizationResultV1":
        for field in ("client_asset_key", "asset_code", "display_code"):
            values = [getattr(asset, field) for asset in self.assets]
            if len(values) != len(set(values)):
                raise ValueError(f"assets must have unique {field}")
        by_key = {asset.client_asset_key: asset for asset in self.assets}
        review_keys: set[tuple[str, str]] = set()
        for asset in self.assets:
            for item in asset.review_items:
                review_key = (item.asset_key, item.review_id)
                if review_key in review_keys:
                    raise ValueError("review_id must be unique within an asset")
                review_keys.add(review_key)
            for link in asset.relations.scene_links:
                self._validate_link(link, by_key, expected_type="scene")
            if asset.relations.owner_character is not None:
                self._validate_link(asset.relations.owner_character, by_key, expected_type="character")
        global_review_ids = [item.review_id for item in self.global_review_items]
        if len(global_review_ids) != len(set(global_review_ids)):
            raise ValueError("global review_id must be unique")
        return self

    @staticmethod
    def _validate_link(
        link: AssetLinkV1,
        by_key: dict[str, AssetNormalizationV1],
        *,
        expected_type: AssetTypeV1,
    ) -> None:
        target = by_key.get(link.target_asset_key)
        if target is None:
            raise ValueError("asset relation target must exist in the same inventory")
        if target.asset_type != expected_type:
            raise ValueError(f"asset relation target must be a {expected_type}")
        if (
            link.target_asset_code != target.asset_code
            or link.target_display_code != target.display_code
            or link.target_display_label != target.display_label
        ):
            raise ValueError("asset relation target identity does not match its inventory asset")

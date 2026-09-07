"""
服装资产相关的 Schema 定义
"""
from __future__ import annotations

from datetime import datetime
from typing import Literal
from uuid import UUID

from pydantic import BaseModel, Field


class CostumeAssetCreate(BaseModel):
    """创建服装资产"""

    # 基础字段
    name: str = Field(..., max_length=160, description="服装名称")
    prop_type: Literal["costume"] = Field(default="costume", description="道具类型-服装")

    # 视觉描述
    appearance: str = Field(..., description="外观描述（详细的视觉描述）")
    material: str = Field(..., description="材质（皮革/布料/金属/混合）")
    color_palette: list[str] = Field(default_factory=list, description="色彩列表")
    style: str = Field(..., description="风格（古风/现代/科幻/奇幻）")

    # 关联关系
    owner_character: str | None = Field(None, description="所属角色名称")
    owner_character_id: str | None = Field(None, description="所属角色ID")
    scene_names: list[str] = Field(default_factory=list, description="对应场景名称列表")
    scene_ids: list[str] = Field(default_factory=list, description="对应场景ID列表")

    # 社会文化背景
    era: str = Field(..., description="年代背景（远古/春秋战国/唐朝/民国/现代/未来）")
    social_class: str = Field(..., description="社会阶层（贵族/平民/奴隶/商人/官员/军人）")
    family_background: str = Field(..., description="家庭出身（富裕世家/普通家庭/贫寒/孤儿）")
    occupation_context: str = Field(..., description="职业背景（猎人/农夫/士兵/祭司/工匠）")

    # 文化符号
    cultural_symbols: list[str] = Field(default_factory=list, description="文化符号列表")
    status_indicators: list[str] = Field(default_factory=list, description="地位标识列表")
    regional_style: str = Field(..., description="地域风格（中原/边疆/海外/异域）")

    # 制作工艺
    craftsmanship_level: str = Field(..., description="工艺水平（粗糙/普通/精致/奢华）")
    maker_origin: str | None = Field(None, description="制作来源（自制/家传/购买/赏赐/战利品）")
    wear_history: str | None = Field(None, description="穿戴历史（新制/传承/修补/改造）")

    # 功能与状态
    function: str | None = Field(None, description="功能性说明")
    condition: str = Field(default="崭新", description="状态（崭新/破损/血迹/污渍）")
    status_change: str | None = Field(None, description="状态变化记录")

    # 情感与故事
    emotional_value: str | None = Field(None, description="情感价值（普通/纪念/珍藏/诅咒）")
    backstory: str | None = Field(None, description="服装背景故事")
    symbolic_meaning: str | None = Field(None, description="象征意义（权力/自由/复仇/希望）")

    # 生产相关
    priority: Literal["S", "A", "B", "C"] = Field(..., description="优先级")
    reference_images: list[str] = Field(default_factory=list, description="参考图URL列表")
    first_appearance_segment: str | None = Field(None, description="首次出现的脚本段")
    related_storyboard_codes: list[str] = Field(default_factory=list, description="关联的分镜编码")

    # 元数据
    source: str | None = Field(None, description="来源说明")
    manual_review_items: list[dict] = Field(default_factory=list, description="需人工复核的项")


class CostumeAssetRead(BaseModel):
    """读取服装资产"""

    id: UUID
    asset_code: str
    project_id: UUID

    # 基础字段
    name: str
    asset_type: Literal["prop"] = "prop"
    prop_type: Literal["costume"] = "costume"

    # 视觉描述
    appearance: str
    material: str
    color_palette: list[str]
    style: str

    # 关联关系
    owner_character: str | None = None
    owner_character_id: str | None = None
    scene_names: list[str]
    scene_ids: list[str]

    # 社会文化背景
    era: str
    social_class: str
    family_background: str
    occupation_context: str

    # 文化符号
    cultural_symbols: list[str]
    status_indicators: list[str]
    regional_style: str

    # 制作工艺
    craftsmanship_level: str
    maker_origin: str | None = None
    wear_history: str | None = None

    # 功能与状态
    function: str | None = None
    condition: str
    status_change: str | None = None

    # 情感与故事
    emotional_value: str | None = None
    backstory: str | None = None
    symbolic_meaning: str | None = None

    # 生产相关
    priority: Literal["S", "A", "B", "C"]
    reference_images: list[str]
    first_appearance_segment: str | None = None
    related_storyboard_codes: list[str]

    # 元数据
    source: str | None = None
    manual_review_items: list[dict]
    metadata_json: dict = Field(default_factory=dict)

    # 时间戳
    created_at: datetime
    updated_at: datetime

    class Config:
        from_attributes = True


class CostumeAssetUpdate(BaseModel):
    """更新服装资产"""

    name: str | None = None
    appearance: str | None = None
    material: str | None = None
    color_palette: list[str] | None = None
    style: str | None = None

    owner_character: str | None = None
    owner_character_id: str | None = None
    scene_names: list[str] | None = None
    scene_ids: list[str] | None = None

    era: str | None = None
    social_class: str | None = None
    family_background: str | None = None
    occupation_context: str | None = None

    cultural_symbols: list[str] | None = None
    status_indicators: list[str] | None = None
    regional_style: str | None = None

    craftsmanship_level: str | None = None
    maker_origin: str | None = None
    wear_history: str | None = None

    function: str | None = None
    condition: str | None = None
    status_change: str | None = None

    emotional_value: str | None = None
    backstory: str | None = None
    symbolic_meaning: str | None = None

    priority: Literal["S", "A", "B", "C"] | None = None
    reference_images: list[str] | None = None
    first_appearance_segment: str | None = None
    related_storyboard_codes: list[str] | None = None

    manual_review_items: list[dict] | None = None

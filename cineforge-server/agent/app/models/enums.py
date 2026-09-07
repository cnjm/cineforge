from enum import StrEnum


class UserRole(StrEnum):
    director = "director"
    script_editor = "script_editor"
    artist = "artist"
    editor = "editor"
    admin = "admin"


class ProjectStatus(StrEnum):
    draft = "draft"
    reading = "reading"
    breakdown_review = "breakdown_review"
    asset_locking = "asset_locking"
    task_assignment = "task_assignment"
    locked = "locked"
    production = "production"
    assembly = "assembly"
    completed = "completed"
    archived = "archived"


class AssetType(StrEnum):
    character = "character"
    scene = "scene"
    prop = "prop"
    music = "music"
    voice_profile = "voice_profile"
    storyboard = "storyboard"
    storyboard_image = "storyboard_image"
    storyboard_video = "storyboard_video"
    effect = "effect"
    final_video = "final_video"


class TaskStatus(StrEnum):
    todo = "todo"
    in_progress = "in_progress"
    submitted = "submitted"
    reviewing = "reviewing"
    completed = "completed"
    rejected = "rejected"
    overdue = "overdue"


class TaskType(StrEnum):
    script_breakdown = "script_breakdown"
    asset_confirm = "asset_confirm"
    asset = "asset"
    audio = "audio"
    text_to_image = "text_to_image"
    image_to_video = "image_to_video"
    storyboard_shot = "storyboard_shot"
    video_generation = "video_generation"
    assembly = "assembly"
    final_output = "final_output"


class ProductionAgentKind(StrEnum):
    """Agent types accepted by current production execution APIs."""

    script_reading = "script_reading"
    script_segmentation = "script_segmentation"
    script_breakdown = "script_breakdown"
    content_compliance_review = "content_compliance_review"
    asset_extract = "asset_extract"
    relation_check = "relation_check"
    character_design_prompt = "character_design_prompt"
    scene_design_prompt = "scene_design_prompt"
    prop_design_prompt = "prop_design_prompt"
    text_to_image_prompt = "text_to_image_prompt"
    image_to_video_prompt = "image_to_video_prompt"


class AgentKind(StrEnum):
    """Persisted Agent type, including values required to read historical rows.

    New execution surfaces must use ``ProductionAgentKind``. PostgreSQL enum
    values cannot be removed safely while historical AgentRun and training
    sample rows may still contain them.
    """

    script_reading = "script_reading"
    script_segmentation = "script_segmentation"
    script_breakdown = "script_breakdown"
    content_compliance_review = "content_compliance_review"
    asset_extract = "asset_extract"
    relation_check = "relation_check"
    character_design_prompt = "character_design_prompt"
    scene_design_prompt = "scene_design_prompt"
    prop_design_prompt = "prop_design_prompt"
    text_to_image_prompt = "text_to_image_prompt"
    image_to_video_prompt = "image_to_video_prompt"

    # Database compatibility only. These values are not registered or runnable.
    asset_match = "asset_match"
    asset_confirm = "asset_confirm"
    video_prompt = "video_prompt"
    review = "review"
    query = "query"


PRODUCTION_AGENT_KINDS: tuple[AgentKind, ...] = tuple(
    AgentKind(kind.value) for kind in ProductionAgentKind
)
LEGACY_AGENT_KINDS: frozenset[AgentKind] = frozenset(set(AgentKind) - set(PRODUCTION_AGENT_KINDS))


class AgentRunStatus(StrEnum):
    queued = "queued"
    running = "running"
    succeeded = "succeeded"
    failed = "failed"

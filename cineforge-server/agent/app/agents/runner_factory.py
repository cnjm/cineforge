from __future__ import annotations

from app.agents.runners import (
    AgentRunner,
    AssetExtractRunner,
    CharacterDesignPromptRunner,
    ComplianceReviewRunner,
    ImageToVideoPromptRunner,
    RelationCheckRunner,
    PropDesignPromptRunner,
    SceneDesignPromptRunner,
    ScriptReadingRunner,
    TextToImagePromptRunner,
)
from app.agents.workflow_runner import ScriptBreakdownWorkflowRunner, ScriptSegmentationWorkflowRunner
from app.models.enums import AgentKind


def get_runner(agent_type: AgentKind) -> AgentRunner:
    runners: dict[AgentKind, AgentRunner] = {
        AgentKind.script_reading: ScriptReadingRunner(),
        AgentKind.script_segmentation: ScriptSegmentationWorkflowRunner(),
        AgentKind.script_breakdown: ScriptBreakdownWorkflowRunner(),
        AgentKind.content_compliance_review: ComplianceReviewRunner(),
        AgentKind.asset_extract: AssetExtractRunner(),
        AgentKind.relation_check: RelationCheckRunner(),
        AgentKind.character_design_prompt: CharacterDesignPromptRunner(),
        AgentKind.scene_design_prompt: SceneDesignPromptRunner(),
        AgentKind.prop_design_prompt: PropDesignPromptRunner(),
        AgentKind.text_to_image_prompt: TextToImagePromptRunner(),
        AgentKind.image_to_video_prompt: ImageToVideoPromptRunner(),
    }
    return runners[agent_type]

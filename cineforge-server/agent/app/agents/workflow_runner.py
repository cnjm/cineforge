from __future__ import annotations

from typing import Any

from app.agents.breakdown_workflow import run_script_breakdown_workflow


class ScriptBreakdownWorkflowRunner:
    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        script_text = str(payload.get("script_text") or "").strip()
        if not script_text:
            raise ValueError("剧本文本为空，不能运行剧本拆解工作流")
        return await run_script_breakdown_workflow(payload)


class ScriptSegmentationWorkflowRunner:
    async def run(self, payload: dict[str, Any]) -> dict[str, Any]:
        script_text = str(payload.get("script_text") or "").strip()
        if not script_text:
            raise ValueError("剧本文本为空，不能运行脚本段拆分工作流")
        return await run_script_breakdown_workflow(
            {
                **payload,
                "single_step": "script_segmentation",
                "workflow_phase": str(payload.get("workflow_phase") or "single:script_segmentation"),
            }
        )

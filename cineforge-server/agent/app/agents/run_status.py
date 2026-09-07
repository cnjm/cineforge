from __future__ import annotations

from typing import Any


def agent_output_failed(output: dict[str, Any]) -> bool:
    workflow = output.get("workflow") if isinstance(output.get("workflow"), dict) else {}
    node_status = output.get("node_status") if isinstance(output.get("node_status"), dict) else {}
    return (
        workflow.get("status") == "failed"
        or any(status == "failed" for status in node_status.values())
        or bool(output.get("errors"))
    )


def agent_output_error_message(output: dict[str, Any]) -> str | None:
    if not agent_output_failed(output):
        return None
    errors = output.get("errors") if isinstance(output.get("errors"), list) else []
    first_error = errors[0].get("message") if errors and isinstance(errors[0], dict) else None
    return first_error or "Agent workflow failed"

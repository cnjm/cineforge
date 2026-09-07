from __future__ import annotations

import hashlib
from datetime import UTC, datetime
from typing import Any, Literal


PromptMode = Literal["t2i", "i2v"]

def build_feedback_training_sample(
    *,
    run_id: str,
    agent_type: str,
    run_input: dict[str, Any],
    run_output: dict[str, Any] | None,
    feedback: dict[str, Any],
) -> dict[str, Any]:
    original_output = _as_dict(feedback.get("original_output")) or _as_dict(run_output)
    edited_output = _as_dict(feedback.get("edited_output"))
    sample = {
        "sample_type": "human_prompt_revision",
        "schema_version": "agent-feedback-sample-v0.1",
        "run_id": run_id,
        "agent_type": agent_type,
        "input": run_input,
        "original_output": original_output,
        "edited_output": edited_output,
        "rating": feedback.get("rating"),
        "note": feedback.get("note"),
        "created_at": datetime.now(UTC).isoformat(),
    }
    sample["sample_id"] = _stable_id(sample)
    return sample


def _as_dict(value: Any) -> dict[str, Any]:
    return value if isinstance(value, dict) else {}


def _stable_id(sample: dict[str, Any]) -> str:
    raw = repr(
        (
            sample.get("run_id"),
            sample.get("agent_type"),
            sample.get("input"),
            sample.get("original_output"),
            sample.get("edited_output"),
            sample.get("rating"),
            sample.get("note"),
        )
    ).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()[:24]

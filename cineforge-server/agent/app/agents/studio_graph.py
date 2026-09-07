"""LangGraph Studio entry point for the production breakdown workflow.

Keep this module as a stable import target for local tooling. The graph itself
must live in ``breakdown_workflow`` so Studio and production cannot drift.
"""

from app.agents.breakdown_workflow import graph

__all__ = ["graph"]

from datetime import datetime
from typing import Any
from uuid import UUID

from pydantic import BaseModel, Field

from app.models.enums import AgentKind, AgentRunStatus, ProductionAgentKind


class AgentDefinitionRead(BaseModel):
    key: ProductionAgentKind
    name: str
    skill: str
    description: str
    stage: str
    capabilities: list[str]
    input_schema: dict[str, Any]
    output_schema: dict[str, Any]
    runtime_policy: dict[str, Any] = Field(default_factory=dict)
    display_schema: dict[str, Any] = Field(default_factory=dict)
    version: str
    enabled: bool = True


class AgentStageMapItem(BaseModel):
    stage: str
    agents: list[AgentDefinitionRead]


class AgentRunMaterializeResponse(BaseModel):
    run_id: UUID
    project_id: UUID
    agent_type: AgentKind
    created_script_segments: int = 0
    created_entity_versions: int = 0
    created_artifact_revisions: int = 0
    created_training_samples: int = 0
    message: str


class AgentJobCreate(BaseModel):
    agent_type: ProductionAgentKind
    project_id: UUID | None = None
    storyboard_id: UUID | None = None
    task_id: UUID | None = None
    input: dict[str, Any] = Field(default_factory=dict)
    idempotency_key: str | None = None


class AgentRunRead(BaseModel):
    id: UUID
    project_id: UUID | None
    parent_run_id: UUID | None = None
    storyboard_id: UUID | None = None
    task_id: UUID | None = None
    agent_type: AgentKind
    status: AgentRunStatus
    input: dict[str, Any]
    output: dict[str, Any] | None = None
    error_message: str | None = None
    duration_ms: int | None = None
    token_usage: dict[str, Any] | None = None
    feedback_json: dict[str, Any] | None = None
    model: str | None = None
    skill_name: str | None = None
    skill_version: str | None = None
    contract_version: str | None = None
    contract_hash: str | None = None
    prompt_version: str | None = None
    input_hash: str | None = None
    node_key: str | None = None
    workflow_run_id: UUID | None = None
    summary: dict[str, Any] = Field(default_factory=dict)
    payload_ref: str | None = None
    payload_size_bytes: int | None = None
    payload_sha256: str | None = None
    data_state: str = "agent_raw"
    created_at: datetime
    updated_at: datetime


class WorkflowRunRead(BaseModel):
    id: UUID
    project_id: UUID
    agent_run_id: UUID | None = None
    workflow_type: str
    status: str
    node_status: dict[str, Any] = Field(default_factory=dict)
    result: dict[str, Any] = Field(default_factory=dict)
    errors: dict[str, Any] = Field(default_factory=dict)
    input: dict[str, Any] = Field(default_factory=dict)
    can_retry: bool = False
    can_rerun: bool = False
    can_materialize: bool = False
    materialized: bool = False
    summary: dict[str, Any] = Field(default_factory=dict)
    payload_ref: str | None = None
    payload_size_bytes: int | None = None
    payload_sha256: str | None = None
    created_by: UUID | None = None
    created_at: datetime
    updated_at: datetime


class AgentRunSummaryRead(BaseModel):
    id: UUID
    project_id: UUID | None
    parent_run_id: UUID | None = None
    storyboard_id: UUID | None = None
    task_id: UUID | None = None
    agent_type: AgentKind
    status: AgentRunStatus
    error_message: str | None = None
    duration_ms: int | None = None
    model: str | None = None
    skill_name: str | None = None
    skill_version: str | None = None
    contract_version: str | None = None
    contract_hash: str | None = None
    prompt_version: str | None = None
    input_hash: str | None = None
    node_key: str | None = None
    workflow_run_id: UUID | None = None
    episode_id: str | None = None
    episode_code: str | None = None
    script_version_id: str | None = None
    summary: dict[str, Any] = Field(default_factory=dict)
    payload_ref: str | None = None
    payload_size_bytes: int | None = None
    payload_sha256: str | None = None
    created_at: datetime
    updated_at: datetime


class WorkflowRunSummaryRead(BaseModel):
    id: UUID
    project_id: UUID
    agent_run_id: UUID | None = None
    workflow_type: str
    status: str
    can_retry: bool = False
    can_rerun: bool = False
    can_materialize: bool = False
    materialized: bool = False
    created_by: UUID | None = None
    episode_id: str | None = None
    episode_code: str | None = None
    script_version_id: str | None = None
    summary: dict[str, Any] = Field(default_factory=dict)
    payload_ref: str | None = None
    payload_size_bytes: int | None = None
    payload_sha256: str | None = None
    created_at: datetime
    updated_at: datetime


class AgentRunPage(BaseModel):
    items: list[AgentRunSummaryRead]
    next_cursor: str | None = None
    has_more: bool = False
    limit: int


class WorkflowRunPage(BaseModel):
    items: list[WorkflowRunSummaryRead]
    next_cursor: str | None = None
    has_more: bool = False
    limit: int


class AgentFeedbackCreate(BaseModel):
    rating: int | None = Field(default=None, ge=1, le=10)
    original_output: dict[str, Any] | None = None
    edited_output: dict[str, Any] | None = None
    note: str | None = None


class AgentFeedbackRead(AgentFeedbackCreate):
    run_id: UUID
    sample_id: str | None = None
    created_at: datetime


class AgentTrainingSampleRead(BaseModel):
    sample_id: str
    sample_type: str
    schema_version: str
    run_id: UUID | None = None
    agent_type: AgentKind | None = None
    input: dict[str, Any]
    original_output: dict[str, Any]
    edited_output: dict[str, Any]
    confirmed_output: dict[str, Any] = Field(default_factory=dict)
    rating: int | None = None
    note: str | None = None
    entity_type: str | None = None
    entity_code: str | None = None
    change_summary: list[str] = Field(default_factory=list)
    training_tags: list[str] = Field(default_factory=list)
    training_ready: bool = False
    payload_ref: str | None = None
    payload_size_bytes: int | None = None
    payload_sha256: str | None = None
    data_state: str = "final"
    created_at: datetime


class QueryAgentRequest(BaseModel):
    question: str = Field(min_length=1, max_length=1000)
    limit: int = Field(default=5, ge=1, le=10)


class QueryAgentReference(BaseModel):
    source_type: str
    source_id: str
    title: str
    score: float
    excerpt: str
    metadata: dict[str, Any] = Field(default_factory=dict)


class QueryAgentResponse(BaseModel):
    question: str
    answer: str
    references: list[QueryAgentReference]
    document_count: int
    retrieval: dict[str, Any] = Field(default_factory=dict)

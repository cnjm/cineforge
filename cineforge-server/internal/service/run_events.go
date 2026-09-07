package service

// run-events 快照（P4d）：对齐 legacy app/api/routes/streaming.py 的 _project_run_snapshot。
//
// 形状逐字段对齐：
//   - agent_runs  → AgentRunSummaryRead（agent_run_to_summary；episode/version 来自 summary_json）
//   - workflows   → WorkflowRunSummaryRead（workflow_run_to_summary）
//
// 前端只消费 snapshot.agent_runs（fetch + ReadableStream），字段缺失会让 normalizeRunList 缺列，
// 因此本文件按 legacy 字段清单逐一映射。agent_runs 项复用 projects_episodes 的
// AgentRunSummaryItem（同一 AgentRunSummaryRead 形状）。

import (
	"context"
	"encoding/json"
	"time"

	"cineforge/server/internal/model"
)

// WorkflowRunSummaryItem 对齐 legacy WorkflowRunSummaryRead。
type WorkflowRunSummaryItem struct {
	ID               string         `json:"id"`
	ProjectID        string         `json:"project_id"`
	AgentRunID       *string        `json:"agent_run_id"`
	WorkflowType     string         `json:"workflow_type"`
	Status           string         `json:"status"`
	CanRetry         bool           `json:"can_retry"`
	CanRerun         bool           `json:"can_rerun"`
	CanMaterialize   bool           `json:"can_materialize"`
	Materialized     bool           `json:"materialized"`
	CreatedBy        *string        `json:"created_by"`
	EpisodeID        *string        `json:"episode_id"`
	EpisodeCode      *string        `json:"episode_code"`
	ScriptVersionID  *string        `json:"script_version_id"`
	Summary          map[string]any `json:"summary"`
	PayloadRef       *string        `json:"payload_ref"`
	PayloadSizeBytes *int32         `json:"payload_size_bytes"`
	PayloadSha256    *string        `json:"payload_sha256"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// RunEventSnapshot 对齐 legacy _project_run_snapshot 返回体。
type RunEventSnapshot struct {
	ProjectID string                   `json:"project_id"`
	Workflows []WorkflowRunSummaryItem `json:"workflows"`
	AgentRuns []AgentRunSummaryItem    `json:"agent_runs"`
	EmittedAt string                   `json:"emitted_at"`
}

// jsonMap 解析 nullable jsonb 列；null/非法 → 空 map。
func jsonMap(raw *json.RawMessage) map[string]any {
	if raw == nil || len(*raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(*raw, &m); err != nil {
		return map[string]any{}
	}
	if m == nil {
		return map[string]any{}
	}
	return m
}

// anyString 把 summary 值转 *string（对齐 legacy summary.get(key)，值存空串也保真输出）。
func anyString(v any) *string {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		return &t
	default:
		raw, err := json.Marshal(t)
		if err != nil || string(raw) == "null" {
			return nil
		}
		s := string(raw)
		return &s
	}
}

// toWorkflowRunSummaryItem 对齐 legacy workflow_run_to_summary。
func toWorkflowRunSummaryItem(w *model.WorkflowRuns) WorkflowRunSummaryItem {
	summary := jsonMap(w.SummaryJson)
	materialized := w.Status == "materialized"
	terminalForRetry := w.Status == "failed" || w.Status == "needs_review"
	return WorkflowRunSummaryItem{
		ID:              w.ID,
		ProjectID:       w.ProjectId,
		AgentRunID:      w.AgentRunId,
		WorkflowType:    w.WorkflowType,
		Status:          w.Status,
		CanRetry:        terminalForRetry && !materialized,
		CanMaterialize:  (w.Status == "succeeded" || w.Status == "needs_review") && !materialized,
		Materialized:    materialized,
		CreatedBy:       w.CreatedBy,
		EpisodeID:       anyString(summary["episode_id"]),
		EpisodeCode:     anyString(summary["episode_code"]),
		ScriptVersionID: anyString(summary["script_version_id"]),
		Summary:         summary,
		PayloadRef:      w.PayloadRef,
		PayloadSizeBytes: w.PayloadSizeBytes,
		PayloadSha256:   w.PayloadSha256,
		CreatedAt:       w.CreatedAt,
		UpdatedAt:       w.UpdatedAt,
	}
}

// RunEventSnapshot 生成 SSE 首帧/重载快照（对齐 streaming._project_run_snapshot）。
// 存在性先于权限（404/403 逐字）。agent_runs/workflows 各取最近 100 条。
func (s *ProjectService) RunEventSnapshot(ctx context.Context, userID, role, projectID string) (*RunEventSnapshot, error) {
	p, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	ok, err := s.canRead(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	runs, err := s.projects.ListAgentRunSnapshots(ctx, projectID, 100)
	if err != nil {
		return nil, err
	}
	workflows, err := s.projects.ListWorkflowRuns(ctx, projectID, 100)
	if err != nil {
		return nil, err
	}
	snapshot := &RunEventSnapshot{
		ProjectID: projectID,
		Workflows: make([]WorkflowRunSummaryItem, 0, len(workflows)),
		AgentRuns: make([]AgentRunSummaryItem, 0, len(runs)),
		EmittedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for i := range runs {
		snapshot.AgentRuns = append(snapshot.AgentRuns, toAgentRunSummary(&runs[i]))
	}
	for i := range workflows {
		snapshot.Workflows = append(snapshot.Workflows, toWorkflowRunSummaryItem(&workflows[i]))
	}
	return snapshot, nil
}
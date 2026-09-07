package service

// P4e feedback / training-samples（对齐 legacy agents.py runs_router + training_router 契约）：
//
//	POST /agent-runs/{run_id}/feedback          → 201 AgentFeedbackRead（404 "Agent run not found"）
//	GET  /agent-runs/{run_id}/feedback          → list[AgentFeedbackRead]（original/edited 包装为 {"text":...}）
//	GET  /agent-training-samples                → list[AgentTrainingSampleRead]（project_id/agent_type 过滤）
//	POST /agent-training-samples                → 201 AgentTrainingSampleRead（director/admin，403 "Training sample permission denied"）

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
)

// AgentFeedbackInput 对齐 AgentFeedbackCreate。
type AgentFeedbackInput struct {
	Rating         *int32         `json:"rating"`
	OriginalOutput map[string]any `json:"original_output"`
	EditedOutput   map[string]any `json:"edited_output"`
	Note           *string        `json:"note"`
}

// AgentFeedbackItem 对齐 AgentFeedbackRead。
type AgentFeedbackItem struct {
	RunID          string          `json:"run_id"`
	SampleID       *string         `json:"sample_id"`
	Rating         *int32          `json:"rating"`
	OriginalOutput *map[string]any `json:"original_output"`
	EditedOutput   *map[string]any `json:"edited_output"`
	Note           *string         `json:"note"`
	CreatedAt      time.Time       `json:"created_at"`
}

// TrainingSampleItem 对齐 AgentTrainingSampleRead。
type TrainingSampleItem struct {
	SampleID         string         `json:"sample_id"`
	SampleType       string         `json:"sample_type"`
	SchemaVersion    string         `json:"schema_version"`
	RunID            *string        `json:"run_id"`
	AgentType        *string        `json:"agent_type"`
	Input            map[string]any `json:"input"`
	OriginalOutput   map[string]any `json:"original_output"`
	EditedOutput     map[string]any `json:"edited_output"`
	ConfirmedOutput  map[string]any `json:"confirmed_output"`
	Rating           *int32         `json:"rating"`
	Note             *string        `json:"note"`
	EntityType       *string        `json:"entity_type"`
	EntityCode       *string        `json:"entity_code"`
	ChangeSummary    []string       `json:"change_summary"`
	TrainingTags     []string       `json:"training_tags"`
	TrainingReady    bool           `json:"training_ready"`
	PayloadRef       *string        `json:"payload_ref"`
	PayloadSizeBytes *int32         `json:"payload_size_bytes"`
	PayloadSha256    *string        `json:"payload_sha256"`
	DataState        string         `json:"data_state"`
	CreatedAt        time.Time      `json:"created_at"`
}

// TrainingSampleCreateInput 对齐 AgentTrainingSampleCreate。
type TrainingSampleCreateInput struct {
	ProjectID       *string        `json:"project_id"`
	AgentType       *string        `json:"agent_type"`
	SampleType      string         `json:"sample_type"`
	EntityType      *string        `json:"entity_type"`
	EntityCode      *string        `json:"entity_code"`
	InputJSON       map[string]any `json:"input_json"`
	AgentOutputJSON map[string]any `json:"agent_output_json"`
	HumanModified   map[string]any `json:"human_modified_output_json"`
	Confirmed       map[string]any `json:"confirmed_output_json"`
	ChangeSummary   []string       `json:"change_summary"`
	QualityScore    *int32         `json:"quality_score"`
	TrainingTags    []string       `json:"training_tags"`
	TrainingReady   bool           `json:"training_ready"`
	CreatedBy       *string        `json:"created_by"`
}

// AddAgentFeedback 对齐 legacy add_agent_feedback。
func (s *ProjectService) AddAgentFeedback(ctx context.Context, userID, runID string, fb AgentFeedbackInput) (*AgentFeedbackItem, error) {
	run, err := s.projects.GetAgentRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, notFound404("Agent run not found")
	}
	row, sampleID, err := s.projects.AddAgentFeedback(ctx, run, repository.AddAgentFeedbackInput{
		RunID:          runID,
		UserID:         userID,
		Rating:         fb.Rating,
		OriginalOutput: fb.OriginalOutput,
		EditedOutput:   fb.EditedOutput,
		Note:           fb.Note,
	})
	if err != nil {
		return nil, err
	}
	return &AgentFeedbackItem{
		RunID:          runID,
		SampleID:       strPtr(sampleID),
		Rating:         fb.Rating,
		OriginalOutput: mapPtr(fb.OriginalOutput),
		EditedOutput:   mapPtr(fb.EditedOutput),
		Note:           fb.Note,
		CreatedAt:      row.CreatedAt,
	}, nil
}

// ListAgentFeedback 对齐 legacy list_agent_feedback（original/edited 包装为 {"text": ...}）。
func (s *ProjectService) ListAgentFeedback(ctx context.Context, runID string) ([]AgentFeedbackItem, error) {
	run, err := s.projects.GetAgentRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, notFound404("Agent run not found")
	}
	rows, err := s.projects.ListAgentFeedback(ctx, runID)
	if err != nil {
		return nil, err
	}
	items := make([]AgentFeedbackItem, 0, len(rows))
	for _, row := range rows {
		item := AgentFeedbackItem{RunID: runID, CreatedAt: row.CreatedAt}
		if row.BeforeText != nil {
			item.OriginalOutput = mapPtr(map[string]any{"text": *row.BeforeText})
		}
		if row.AfterText != nil {
			item.EditedOutput = mapPtr(map[string]any{"text": *row.AfterText})
		}
		items = append(items, item)
	}
	return items, nil
}

// ListTrainingSamples 对齐 legacy list_training_samples。
func (s *ProjectService) ListTrainingSamples(ctx context.Context, projectID, agentType *string) ([]TrainingSampleItem, error) {
	rows, err := s.projects.ListTrainingSamples(ctx, projectID, agentType)
	if err != nil {
		return nil, err
	}
	items := make([]TrainingSampleItem, 0, len(rows))
	for i := range rows {
		items = append(items, *trainingSampleToItem(&rows[i]))
	}
	return items, nil
}

// CreateTrainingSample 对齐 legacy create_training_sample（403 "Training sample permission denied"）。
func (s *ProjectService) CreateTrainingSample(ctx context.Context, userID, role string, in TrainingSampleCreateInput) (*TrainingSampleItem, error) {
	if role != "director" && role != "admin" {
		return nil, forbidden403("Training sample permission denied")
	}
	createdBy := in.CreatedBy
	if createdBy == nil || *createdBy == "" {
		createdBy = strPtr(userID)
	}
	inserted, err := s.projects.CreateTrainingSample(ctx, repository.CreateTrainingSampleInput{
		ProjectID:       in.ProjectID,
		AgentType:       in.AgentType,
		SampleType:      in.SampleType,
		EntityType:      in.EntityType,
		EntityCode:      in.EntityCode,
		InputJSON:       in.InputJSON,
		AgentOutputJSON: in.AgentOutputJSON,
		HumanModified:   in.HumanModified,
		Confirmed:       in.Confirmed,
		ChangeSummary:   in.ChangeSummary,
		QualityScore:    in.QualityScore,
		TrainingTags:    in.TrainingTags,
		TrainingReady:   in.TrainingReady,
		CreatedBy:       createdBy,
	})
	if err != nil {
		return nil, err
	}
	item := trainingSampleToItem(inserted)
	item.RunID = nil // create_training_sample 响应恒 run_id=None（legacy 显式 None）
	return item, nil
}

// trainingSampleToItem 对齐 training_sample_to_read（list 形态：entity_code 可解析 UUID 时派生 run_id）。
func trainingSampleToItem(s *model.AgentTrainingSamples) *TrainingSampleItem {
	return &TrainingSampleItem{
		SampleID:         s.ID,
		SampleType:       s.SampleType,
		SchemaVersion:    "1.0",
		RunID:            runIDFromEntityCode(s),
		AgentType:        s.AgentType,
		Input:            rawMap(s.InputJson),
		OriginalOutput:   rawMap(s.AgentOutputJson),
		EditedOutput:     rawMap(s.HumanModifiedOutputJson),
		ConfirmedOutput:  rawMap(s.ConfirmedOutputJson),
		Rating:           s.QualityScore,
		Note:             nil,
		EntityType:       s.EntityType,
		EntityCode:       s.EntityCode,
		ChangeSummary:    rawStrings(s.ChangeSummary),
		TrainingTags:     rawStrings(s.TrainingTags),
		TrainingReady:    s.TrainingReady,
		PayloadRef:       s.PayloadRef,
		PayloadSizeBytes: s.PayloadSizeBytes,
		PayloadSha256:    s.PayloadSha256,
		DataState:        s.DataState,
		CreatedAt:        s.CreatedAt,
	}
}

// runIDFromEntityCode entity_code 为可解析 UUID 时返回（对齐 UUID(entity_code) try/except）。
func runIDFromEntityCode(s *model.AgentTrainingSamples) *string {
	if s.EntityType == nil || *s.EntityType != "agent_run" || s.EntityCode == nil {
		return nil
	}
	if _, err := uuid.Parse(*s.EntityCode); err != nil {
		return nil
	}
	return s.EntityCode
}

func mapPtr(m map[string]any) *map[string]any {
	if m == nil {
		return nil
	}
	return &m
}

// rawMap jsonb → map（nil-safe）。
func rawMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// rawStrings jsonb 数组 → []string（nil-safe）。
func rawStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}

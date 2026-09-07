package repository

// P4e feedback / training-samples 持久化（对齐 legacy add_agent_feedback /
// list_agent_feedback / list_training_samples / create_training_sample）：
//
//   - AddAgentFeedback  → 单事务写 agent_feedback + agent_feedback training sample
//     + 回写 agent_runs.feedback_json（feedback_count/latest_feedback_at/latest_sample_id）
//   - ListAgentFeedback → 按 created_at DESC 列出某 run 的反馈（before_text/after_text 原样）
//   - ListTrainingSamples / CreateTrainingSample → 训练样本的查/建
//

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cineforge/server/internal/model"
)

const agentFeedbackColumns = `id, run_id, task_id, asset_id, before_text, after_text,
	feedback_type, created_by, created_at, updated_at`

const agentTrainingSampleColumns = `id, project_id, agent_type, sample_type, entity_type, entity_code,
	input_json, agent_output_json, human_modified_output_json, confirmed_output_json, change_summary,
	quality_score, training_tags, training_ready, created_by, source_run_id, payload_ref,
	payload_size_bytes, payload_sha256, data_state, lineage_key, created_at, updated_at`

// AddAgentFeedbackInput add_agent_feedback 入参（map 形态由事务内部 marshal）。
type AddAgentFeedbackInput struct {
	RunID          string
	UserID         string
	Rating         *int32
	OriginalOutput map[string]any
	EditedOutput   map[string]any
	Note           *string
}

// AddAgentFeedback 对齐 legacy add_agent_feedback（一次 commit 完成三处写入）。
func (r *Projects) AddAgentFeedback(ctx context.Context, run *model.AgentRuns, in AddAgentFeedbackInput) (*model.AgentFeedback, string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("feedback begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// before_text / after_text = str(original|edited_output or {})，nil 时保持 NULL。
	var beforeText, afterText *string
	if in.OriginalOutput != nil {
		s := stringifyMap(in.OriginalOutput)
		beforeText = &s
	}
	if in.EditedOutput != nil {
		s := stringifyMap(in.EditedOutput)
		afterText = &s
	}
	feedbackID := newUUIDString()
	row := &model.AgentFeedback{
		ID:           feedbackID,
		RunId:        &in.RunID,
		BeforeText:   beforeText,
		AfterText:    afterText,
		FeedbackType: "agent_feedback",
		CreatedBy:    &in.UserID,
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO agent_feedback (id, run_id, before_text, after_text, feedback_type, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at`,
		row.ID, row.RunId, row.BeforeText, row.AfterText, row.FeedbackType, row.CreatedBy).
		Scan(&row.CreatedAt); err != nil {
		return nil, "", fmt.Errorf("insert agent feedback: %w", err)
	}

	// training sample（agent_output_json = original or run.output_json or {}）。
	original := in.OriginalOutput
	if len(original) == 0 {
		original = artifactJSON(runJSONP(run.OutputJson))
	}
	edited := in.EditedOutput
	if edited == nil {
		edited = map[string]any{}
	}
	changeSummary := []string{"人工反馈已记录"}
	if in.Note != nil && *in.Note != "" {
		changeSummary = []string{*in.Note}
	}
	sampleID := newUUIDString()
	if _, err := tx.Exec(ctx,
		`INSERT INTO agent_training_samples (
			id, project_id, agent_type, sample_type, entity_type, entity_code,
			input_json, agent_output_json, human_modified_output_json, confirmed_output_json,
			change_summary, quality_score, training_tags, training_ready, created_by,
			data_state, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'final',$16,$16)`,
		sampleID, run.ProjectId, run.AgentType, "agent_feedback", "agent_run", in.RunID,
		jsonB(artifactJSON(runJSON(run.InputJson))),
		jsonB(original), jsonB(edited), jsonB(edited),
		jsonB(changeSummary), in.Rating, jsonB([]string{"human_modified", "feedback"}),
		in.EditedOutput != nil, in.UserID, timeNow()); err != nil {
		return nil, "", fmt.Errorf("insert feedback training sample: %w", err)
	}

	// feedback_json = {feedback_count, latest_feedback_at, latest_sample_id}。
	var count int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM agent_feedback WHERE run_id = $1`, in.RunID).Scan(&count); err != nil {
		return nil, "", fmt.Errorf("count agent feedback: %w", err)
	}
	_, err = tx.Exec(ctx,
		`UPDATE agent_runs SET feedback_json = $2::jsonb, updated_at = now() WHERE id = $1`, in.RunID,
		jsonB(map[string]any{
			"feedback_count":     count,
			"latest_feedback_at": row.CreatedAt.UTC().Format(time.RFC3339),
			"latest_sample_id":   sampleID,
		}))
	if err != nil {
		return nil, "", fmt.Errorf("update agent run feedback: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("feedback commit: %w", err)
	}
	return row, sampleID, nil
}

// ListAgentFeedback 列出某 run 的反馈（不存在用空切片，run 存在性由 service 校验）。
func (r *Projects) ListAgentFeedback(ctx context.Context, runID string) ([]model.AgentFeedback, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+agentFeedbackColumns+` FROM agent_feedback WHERE run_id = $1 ORDER BY created_at DESC`, runID)
	if err != nil {
		return nil, fmt.Errorf("list agent feedback: %w", err)
	}
	defer rows.Close()
	items := []model.AgentFeedback{}
	for rows.Next() {
		var item model.AgentFeedback
		if err := rows.Scan(&item.ID, &item.RunId, &item.TaskId, &item.AssetId, &item.BeforeText,
			&item.AfterText, &item.FeedbackType, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan agent feedback: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent feedback: %w", err)
	}
	return items, nil
}

// ListTrainingSamples 列训练样本（project_id / agent_type 可选过滤，created_at DESC）。
func (r *Projects) ListTrainingSamples(ctx context.Context, projectID, agentType *string) ([]model.AgentTrainingSamples, error) {
	query := `SELECT ` + agentTrainingSampleColumns + ` FROM agent_training_samples`
	args := []any{}
	var where []string
	if projectID != nil {
		where = append(where, "project_id = $1")
		args = append(args, *projectID)
	}
	if agentType != nil {
		where = append(where, fmt.Sprintf("agent_type = $%d", len(args)+1))
		args = append(args, *agentType)
	}
	if len(where) > 0 {
		query += " WHERE " + joinAnd(where)
	}
	query += " ORDER BY created_at DESC"
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list training samples: %w", err)
	}
	defer rows.Close()
	items := []model.AgentTrainingSamples{}
	for rows.Next() {
		var item model.AgentTrainingSamples
		if err := rows.Scan(&item.ID, &item.ProjectId, &item.AgentType, &item.SampleType,
			&item.EntityType, &item.EntityCode, &item.InputJson, &item.AgentOutputJson,
			&item.HumanModifiedOutputJson, &item.ConfirmedOutputJson, &item.ChangeSummary,
			&item.QualityScore, &item.TrainingTags, &item.TrainingReady, &item.CreatedBy,
			&item.SourceRunId, &item.PayloadRef, &item.PayloadSizeBytes, &item.PayloadSha256,
			&item.DataState, &item.LineageKey, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan training sample: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate training samples: %w", err)
	}
	return items, nil
}

// CreateTrainingSampleInput 对齐 AgentTrainingSampleCreate（map 形态由落库时 marshal）。
type CreateTrainingSampleInput struct {
	ProjectID       *string
	AgentType       *string
	SampleType      string
	EntityType      *string
	EntityCode      *string
	InputJSON       map[string]any
	AgentOutputJSON map[string]any
	HumanModified   map[string]any
	Confirmed       map[string]any
	ChangeSummary   []string
	QualityScore    *int32
	TrainingTags    []string
	TrainingReady   bool
	CreatedBy       *string
}

// CreateTrainingSample 对齐 legacy create_training_sample（data_state 默认 'final'）。
func (r *Projects) CreateTrainingSample(ctx context.Context, in CreateTrainingSampleInput) (*model.AgentTrainingSamples, error) {
	item := &model.AgentTrainingSamples{
		ID:                      newUUIDString(),
		ProjectId:               in.ProjectID,
		AgentType:               in.AgentType,
		SampleType:              in.SampleType,
		EntityType:              in.EntityType,
		EntityCode:              in.EntityCode,
		InputJson:               jsonB(orMap(in.InputJSON)),
		AgentOutputJson:         jsonB(orMap(in.AgentOutputJSON)),
		HumanModifiedOutputJson: jsonB(orMap(in.HumanModified)),
		ConfirmedOutputJson:     jsonB(orMap(in.Confirmed)),
		ChangeSummary:           jsonB(orStrings(in.ChangeSummary)),
		QualityScore:            in.QualityScore,
		TrainingTags:            jsonB(orStrings(in.TrainingTags)),
		TrainingReady:           in.TrainingReady,
		CreatedBy:               in.CreatedBy,
		DataState:               "final",
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO agent_training_samples (id, project_id, agent_type, sample_type, entity_type, entity_code,
			input_json, agent_output_json, human_modified_output_json, confirmed_output_json, change_summary,
			quality_score, training_tags, training_ready, created_by, data_state, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'final',$16,$16)
		 RETURNING `+agentTrainingSampleColumns,
		item.ID, item.ProjectId, item.AgentType, item.SampleType, item.EntityType, item.EntityCode,
		item.InputJson, item.AgentOutputJson, item.HumanModifiedOutputJson, item.ConfirmedOutputJson,
		item.ChangeSummary, item.QualityScore, item.TrainingTags, item.TrainingReady, item.CreatedBy, timeNow()).
		Scan(&item.ID, &item.ProjectId, &item.AgentType, &item.SampleType, &item.EntityType,
			&item.EntityCode, &item.InputJson, &item.AgentOutputJson, &item.HumanModifiedOutputJson,
			&item.ConfirmedOutputJson, &item.ChangeSummary, &item.QualityScore, &item.TrainingTags,
			&item.TrainingReady, &item.CreatedBy, &item.SourceRunId, &item.PayloadRef,
			&item.PayloadSizeBytes, &item.PayloadSha256, &item.DataState, &item.LineageKey,
			&item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create training sample: %w", err)
	}
	return item, nil
}

// ---- 纯函数 ----

// stringifyMap 把 map 文本化（对齐 legacy str(dict)；此处用紧凑 JSON 便于展示）。
func stringifyMap(m map[string]any) string {
	data, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// orMap 空/nil map → 空 map 的 jsonb 表示。
func orMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// joinAnd 用 " AND " 连接条件（值与符号间不含用户输入，仅列名占位）。
func joinAnd(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " AND "
		}
		out += p
	}
	return out
}

package repository

// P4e materialize 确定性分支（对齐 legacy repositories.materialize_agent_run）：
//
//   - relation_check            → 追加 relation_report 归一化 artifact revision（agent_raw→normalized 幂等链）
//   - content_compliance_review → 生成 agent_review entity version（REVIEW-{project}-{run[:8]}）
//   - 两者恒定幂等写 {agent_type}_materialized training sample
//
// 前置校验（项目/run/FORMAL gate/type 支持）在 service 层完成；本文件只做确定写入，
// 全部在单事务内（与 legacy session.commit() 一次落库语义一致）。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/model"
)

// MaterializeAgentRunInput MaterializeAgentRun 入参。
type MaterializeAgentRunInput struct {
	Run       *model.AgentRuns
	ProjectID string
	UserID    string
}

// MaterializeAgentRunResult 对齐 AgentRunMaterializeResponse 的计数部分。
type MaterializeAgentRunResult struct {
	EntityVersionsCreated    int
	ArtifactRevisionsCreated int
	TrainingSamplesCreated   int
}

// MaterializeAgentRun 在单事务内执行 materialize 确定性写入。
func (r *Projects) MaterializeAgentRun(ctx context.Context, in MaterializeAgentRunInput) (*MaterializeAgentRunResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("materialize begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	out := &MaterializeAgentRunResult{}

	runInput := artifactJSON(runJSON(in.Run.InputJson))
	runOutput := artifactJSON(runJSONP(in.Run.OutputJson))

	switch in.Run.AgentType {
	case "relation_check":
		source := relationCheckMaterializeSource(runOutput)
		scope := firstNonEmpty(
			strOf(runInput, "script_version_id"),
			strOf(runInput, "episode_id"),
			strOf(runInput, "storyboard_id"),
			in.ProjectID,
		)
		// 对齐 _agent_artifact_id：uuid5(NAMESPACE_URL, "cineforge:relation_report:{project}:{scope}")。
		artifactID := uuid5Value("cineforge:relation_report:" + in.ProjectID + ":" + scope)
		created, err := r.appendAgentOutputRevisionTx(ctx, tx, in.Run, "relation_report", source, "needs_review", artifactID, in.ProjectID)
		if err != nil {
			return nil, err
		}
		out.ArtifactRevisionsCreated = created
	case "content_compliance_review":
		// 对齐 create_entity_version：entity_code = REVIEW-{project_id}-{run.id.hex[:8]}。
		entityCode := fmt.Sprintf("REVIEW-%s-%s", in.ProjectID, runIDPrefix(in.Run.ID))
		entityID := in.Run.ID
		ev := model.EntityVersions{
			ProjectId:     &in.ProjectID,
			EntityType:    "agent_review",
			EntityId:      &entityID,
			EntityCode:    entityCode,
			Source:        "agent",
			Status:        "needs_review",
			ContentJson:   asRaw(materializeRaw(in.Run.OutputJson)),
			ChangeSummary: strPtr("内容合理性审查结果"),
			ChangedFields: json.RawMessage(`["asset_review","storyboard_review","dialogue_review","prompt_safety_review"]`),
			AgentRunId:    &in.Run.ID,
			CreatedBy:     &in.UserID,
		}
		if _, err := r.insertEntityVersionTx(ctx, tx, ev); err != nil {
			return nil, err
		}
		out.EntityVersionsCreated = 1
	}

	// 支持的类型恒定幂等写 training sample（relation_check / content_compliance_review 都走这里）。
	// sample_type = "{agent_type}_materialized"。
	created, err := r.insertMaterializedSampleTx(ctx, tx, in.Run, in.UserID)
	if err != nil {
		return nil, err
	}
	out.TrainingSamplesCreated = created

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("materialize commit: %w", err)
	}
	return out, nil
}

// appendAgentOutputRevisionTx 对齐 _append_agent_output_revision：
// 幂等追加 agent_raw → normalized 两级 ArtifactRevision，返回实际新建数量（0 或 1）。
func (r *Projects) appendAgentOutputRevisionTx(ctx context.Context, tx pgx.Tx,
	run *model.AgentRuns, artifactType string, normalized map[string]any, status, artifactID, projectID string) (int, error) {
	cur, err := r.latestArtifactRevisionTx(ctx, tx, artifactType, artifactID)
	if err != nil {
		return 0, err
	}
	curVersionNo := int32(0)
	var curHash *string
	if cur != nil {
		curVersionNo = cur.VersionNo
		curHash = &cur.ContentHash
	}

	normalizedDigest := ContentHashString(normalized)
	// 同 run/同内容的 normalized revision 已存在 → 幂等返回（created=false）。
	existing, err := r.findArtifactRevisionTx(ctx, tx,
		`artifact_type=$1 AND artifact_id=$2 AND source_run_id=$3 AND source_type='system' AND data_state='normalized' AND content_hash=$4
		 ORDER BY version_no DESC LIMIT 1`,
		artifactType, artifactID, run.ID, normalizedDigest)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		return 0, nil
	}

	lineage := agentRunLineage(run)

	// 1) agent_raw 层（原始输出）。
	rawPayload := artifactJSON(runJSONP(run.OutputJson))
	rawDigest := ContentHashString(rawPayload)
	raw, err := r.findArtifactRevisionTx(ctx, tx,
		`artifact_type=$1 AND artifact_id=$2 AND source_run_id=$3 AND source_type='agent' AND data_state='agent_raw' AND content_hash=$4
		 ORDER BY version_no DESC LIMIT 1`,
		artifactType, artifactID, run.ID, rawDigest)
	if err != nil {
		return 0, err
	}
	if raw == nil {
		rawOutput := rawPayload
		if run.PayloadRef != nil && *run.PayloadRef != "" {
			rawOutput = map[string]any{
				"summary":              runJSONP(run.SummaryJson),
				"payload_externalized": true,
			}
		}
		raw = &model.ArtifactRevisions{
			ID:                newUUIDString(),
			ProjectId:         projectID,
			ArtifactType:      artifactType,
			ArtifactId:        artifactID,
			VersionNo:         curVersionNo + 1,
			ParentRevisionId:  revID(cur),
			SourceType:        "agent",
			SourceRunId:       &run.ID,
			SkillName:         lineage["skill_name"],
			SkillVersion:      lineage["skill_version"],
			ContractVersion:   lineage["contract_version"],
			ContractHash:      lineage["contract_hash"],
			ModelName:         lineage["model"],
			PromptVersion:     lineage["prompt_version"],
			InputHash:         lineage["input_hash"],
			InputSnapshot:     json.RawMessage(`{}`),
			RawOutput:         jsonB(rawOutput),
			NormalizedContent: json.RawMessage(`{}`),
			ChangeDiff: jsonB(map[string]any{
				"previous_content_hash": curHash,
			}),
			ContentHash:      rawDigest,
			Status:           "agent_raw",
			DataState:        "agent_raw",
			PayloadRef:       run.PayloadRef,
			PayloadSizeBytes: run.PayloadSizeBytes,
			PayloadSha256:    run.PayloadSha256,
			IDempotencyKey: strPtr(artifactRevisionIdempotencyKey(
				artifactType, artifactID, run.ID, "agent", "agent_raw", rawDigest)),
		}
		var created bool
		raw, created, err = r.insertArtifactRevisionIdempotentTx(ctx, tx, raw)
		if err != nil {
			return 0, err
		}
		_ = created
	}

	// 2) normalized 层（schema 校验后展示字段）。
	inputSnapshot := map[string]any{}
	for key, value := range runInputSnapshot(run) {
		inputSnapshot[key] = value
	}
	inputSnapshot["agent_raw_revision_id"] = raw.ID
	normalizedRev := &model.ArtifactRevisions{
		ID:                newUUIDString(),
		ProjectId:         projectID,
		ArtifactType:      artifactType,
		ArtifactId:        artifactID,
		VersionNo:         maxInt32(curVersionNo, raw.VersionNo) + 1,
		ParentRevisionId:  &raw.ID,
		SourceType:        "system",
		SourceRunId:       &run.ID,
		SkillName:         lineage["skill_name"],
		SkillVersion:      lineage["skill_version"],
		ContractVersion:   lineage["contract_version"],
		ContractHash:      lineage["contract_hash"],
		ModelName:         lineage["model"],
		PromptVersion:     lineage["prompt_version"],
		InputHash:         lineage["input_hash"],
		InputSnapshot:     jsonB(inputSnapshot),
		RawOutput:         json.RawMessage(`{}`),
		NormalizedContent: jsonB(normalized),
		ChangeDiff: jsonB(map[string]any{
			"previous_content_hash": raw.ContentHash,
			"source_transition":     "agent_raw->normalized",
		}),
		ContentHash: normalizedDigest,
		Status:      status,
		DataState:   "normalized",
		IDempotencyKey: strPtr(artifactRevisionIdempotencyKey(
			artifactType, artifactID, run.ID, "system", "normalized", normalizedDigest)),
	}
	createdRev, created, err := r.insertArtifactRevisionIdempotentTx(ctx, tx, normalizedRev)
	if err != nil {
		return 0, err
	}
	_ = createdRev
	if created {
		return 1, nil
	}
	return 0, nil
}

// insertArtifactRevisionIdempotentTx 幂等插入（ON CONFLICT idempotency_key DO NOTHING）；
// 冲突时按 idempotency_key 重查已有行，返回 (existing, false)。
func (r *Projects) insertArtifactRevisionIdempotentTx(ctx context.Context, tx pgx.Tx, rev *model.ArtifactRevisions) (*model.ArtifactRevisions, bool, error) {
	err := tx.QueryRow(ctx,
		`INSERT INTO artifact_revisions (
			id, project_id, artifact_type, artifact_id, version_no, parent_revision_id, source_type,
			source_run_id, skill_name, skill_version, contract_version, contract_hash, model_name,
			prompt_version, input_hash, input_snapshot, raw_output, normalized_content, change_diff,
			content_hash, status, created_by, data_state, payload_ref, payload_size_bytes, payload_sha256,
			idempotency_key)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
		 ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`,
		rev.ID, rev.ProjectId, rev.ArtifactType, rev.ArtifactId, rev.VersionNo, rev.ParentRevisionId,
		rev.SourceType, rev.SourceRunId, rev.SkillName, rev.SkillVersion, rev.ContractVersion,
		rev.ContractHash, rev.ModelName, rev.PromptVersion, rev.InputHash, rev.InputSnapshot,
		rev.RawOutput, rev.NormalizedContent, rev.ChangeDiff, rev.ContentHash, rev.Status,
		rev.CreatedBy, rev.DataState, rev.PayloadRef, rev.PayloadSizeBytes, rev.PayloadSha256,
		rev.IDempotencyKey).Scan(&rev.ID)
	if err == nil {
		return rev, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("insert artifact revision: %w", err)
	}
	if rev.IDempotencyKey == nil {
		return nil, false, fmt.Errorf("insert artifact revision: no row and no idempotency key")
	}
	existing, err := r.findArtifactRevisionTx(ctx, tx,
		`idempotency_key=$1 LIMIT 1`, *rev.IDempotencyKey)
	if err != nil {
		return nil, false, err
	}
	return existing, false, nil
}

// latestArtifactRevisionTx 按 version_no 取某 artifact 最新 revision（不存在返回 (nil,nil)）。
func (r *Projects) latestArtifactRevisionTx(ctx context.Context, tx pgx.Tx, artifactType, artifactID string) (*model.ArtifactRevisions, error) {
	return scanArtifactRevision(tx.QueryRow(ctx,
		`SELECT `+artifactRevisionColumns+` FROM artifact_revisions
		 WHERE artifact_type=$1 AND artifact_id=$2 ORDER BY version_no DESC LIMIT 1`, artifactType, artifactID))
}

// findArtifactRevisionTx 按附加条件取最新 revision（不存在返回 (nil,nil)）。
func (r *Projects) findArtifactRevisionTx(ctx context.Context, tx pgx.Tx, where string, args ...any) (*model.ArtifactRevisions, error) {
	return scanArtifactRevision(tx.QueryRow(ctx,
		`SELECT `+artifactRevisionColumns+` FROM artifact_revisions WHERE `+where, args...))
}

// UpdateAgentRunResult 更新 agent run 的终态（status/output/error_message），返回更新后的行。
// 对齐 retry-distribution 的 save_agent_run(model_copy(update=...))。
func (r *Projects) UpdateAgentRunResult(ctx context.Context, runID, status string, output map[string]any, errorMessage *string) (*model.AgentRuns, error) {
	now := time.Now().UTC()
	if _, err := r.pool.Exec(ctx,
		`UPDATE agent_runs SET status=$1, output_json=$2, error_message=$3, updated_at=$4 WHERE id=$5`,
		status, jsonB(output), errorMessage, now, runID); err != nil {
		return nil, fmt.Errorf("update agent run result: %w", err)
	}
	return r.GetAgentRun(ctx, runID)
}

// insertMaterializedSampleTx 幂等写 {agent_type}_materialized training sample（对齐 materialize 尾部）。
// 已存在（project_id, sample_type, entity_type=agent_run, entity_code=run.id）时不重复插入。
func (r *Projects) insertMaterializedSampleTx(ctx context.Context, tx pgx.Tx, run *model.AgentRuns, userID string) (int, error) {
	sampleType := run.AgentType + "_materialized"
	var existsID string
	err := tx.QueryRow(ctx,
		`SELECT id FROM agent_training_samples
		 WHERE project_id=$1 AND sample_type=$2 AND entity_type='agent_run' AND entity_code=$3
		 LIMIT 1`,
		run.ProjectId, sampleType, run.ID).Scan(&existsID)
	if err == nil {
		return 0, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("check materialized sample: %w", err)
	}
	now := timeNow()
	_, err = tx.Exec(ctx,
		`INSERT INTO agent_training_samples (id, project_id, agent_type, sample_type, entity_type, entity_code,
		   input_json, agent_output_json, human_modified_output_json, confirmed_output_json, change_summary,
		   training_tags, training_ready, created_by, data_state, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,'agent_run',$5,$6,$7,$8,$9,$10,$11,FALSE,$12,'final',$13,$13)`,
		newUUIDString(), run.ProjectId, run.AgentType, sampleType, run.ID,
		jsonB(artifactJSON(runJSON(run.InputJson))),
		jsonB(artifactJSON(runJSONP(run.OutputJson))),
		json.RawMessage(`{}`),
		json.RawMessage(`{}`),
		jsonB([]any{"Agent 输出已写入业务草案，等待人工修正或导演锁定"}),
		jsonB([]any{"agent_output", "materialized", "needs_human_review"}),
		userID, now)
	if err != nil {
		return 0, fmt.Errorf("insert materialized sample: %w", err)
	}
	return 1, nil
}

// ---- materialize 纯函数 ----

// relationCheckMaterializeSource 对齐 _relation_check_materialize_source。
func relationCheckMaterializeSource(output map[string]any) map[string]any {
	breakdownView := mapStringAny(output["breakdown_view"])
	candidates := []map[string]any{mapStringAny(output["relation_check"]), mapStringAny(breakdownView["relation_check"])}
	for _, candidate := range candidates {
		if candidate != nil && len(candidate) > 0 {
			return candidate
		}
	}
	if mapStringAny(output["relation_report"]) != nil {
		return output
	}
	if mapStringAny(breakdownView["relation_report"]) != nil {
		return breakdownView
	}
	return output
}

// artifactRevisionIdempotencyKey 对齐 _artifact_revision_idempotency_key。
func artifactRevisionIdempotencyKey(artifactType, artifactID, sourceRunID, sourceType, dataState, contentHash string) string {
	return ContentHashString(map[string]any{
		"artifact_type": artifactType,
		"artifact_id":   artifactID,
		"source_run_id": sourceRunID,
		"source_type":   sourceType,
		"data_state":    dataState,
		"content_hash":  contentHash,
	})
}

// agentRunLineage 返回 run 血缘字段（对齐 _agent_run_lineage 的 DB 直读形态）。
func agentRunLineage(run *model.AgentRuns) map[string]*string {
	return map[string]*string{
		"skill_name":       run.SkillName,
		"skill_version":    run.SkillVersion,
		"contract_version": run.ContractVersion,
		"contract_hash":    run.ContractHash,
		"model":            run.Model,
		"prompt_version":   run.PromptVersion,
		"input_hash":       run.InputHash,
	}
}

// runInputSnapshot run.input 的 map 视图（raw_output 段不展开）。
func runInputSnapshot(run *model.AgentRuns) map[string]any {
	return artifactJSON(runJSON(run.InputJson))
}

func runJSONP(p *json.RawMessage) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return runJSON(*p)
}

// materializeRaw 返回 *json.RawMessage 的值形态；空/nil → {}。
func materializeRaw(p *json.RawMessage) json.RawMessage {
	if p == nil || len(*p) == 0 {
		return json.RawMessage(`{}`)
	}
	return *p
}

// ArtifactOutput 返回 agent run output 的 map 视图（nil-safe，供 service 校验/解析）。
func ArtifactOutput(p *json.RawMessage) map[string]any {
	return artifactJSON(runJSONP(p))
}

func runJSON(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func artifactJSON(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func mapStringAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func revID(rev *model.ArtifactRevisions) *string {
	if rev == nil {
		return nil
	}
	return &rev.ID
}

// runIDPrefix 取 run id 的前 8 位（对齐 Python UUID.hex[:8]）。
func runIDPrefix(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

func timeNow() time.Time { return time.Now().UTC() }

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

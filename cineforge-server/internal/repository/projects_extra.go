package repository

// projects 域补充仓储：import 事务、分集状态判定、agent/workflow run、
// dashboard 聚合、操作日志与文件登记（P3d）。

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/model"
)

// DuplicateVersionError 对齐 legacy "该分集已存在相同内容的 v{n}，未创建重复版本。"。
type DuplicateVersionError struct {
	VersionNo int32
}

func (e *DuplicateVersionError) Error() string {
	return fmt.Sprintf("该分集已存在相同内容的 v%d，未创建重复版本。", e.VersionNo)
}

// ImportVersionInput import_project_script_version 的入参。
type ImportVersionInput struct {
	ProjectID       string
	EpisodeNo       int32
	ScriptText      string
	Filename        string
	ParserName      string
	Language        string
	SourceFileID    *string
	UserID          string
	ProductionBrief json.RawMessage // 可空（nil 表示不更新）
	EpisodeBrief    json.RawMessage // 可空（nil 表示不更新）
}

// ImportVersionResult 对齐 ProjectImportContext。
type ImportVersionResult struct {
	EpisodeID       string
	EpisodeNo       int32
	EpisodeCode     string
	ScriptID        string
	ScriptVersionID string
	VersionNo       int32
	CreatedEpisode  bool
	CreatedScript   bool
	ContentHash     string
}

// ImportScriptVersion 在单事务内完成分集/剧本/版本的落库（对齐 legacy 3498-3623）。
func (r *Projects) ImportScriptVersion(ctx context.Context, in ImportVersionInput) (*ImportVersionResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin import tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var project model.Projects
	if err := tx.QueryRow(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = $1 FOR UPDATE`, in.ProjectID).
		Scan(&project.ID, &project.ProjectNo, &project.ProjectPrefix, &project.Name, &project.Title,
			&project.Genre, &project.Style, &project.Status, &project.CurrentStage, &project.ManagerId,
			&project.CreatedById, &project.ScriptText, &project.ProductionBrief, &project.LockedAt,
			&project.ArchivedAt, &project.DeletedAt, &project.DeletedById, &project.DeleteReason,
			&project.CreatedAt, &project.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lock project: %w", err)
	}

	var episode model.ProjectEpisodes
	err = tx.QueryRow(ctx,
		`SELECT `+episodeColumns+` FROM project_episodes WHERE project_id = $1 AND episode_no = $2`,
		in.ProjectID, in.EpisodeNo).Scan(&episode.ID, &episode.ProjectId, &episode.EpisodeNo,
		&episode.EpisodeCode, &episode.Title, &episode.Summary, &episode.ProductionBrief,
		&episode.CreatedAt, &episode.UpdatedAt)
	createdEpisode := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !createdEpisode {
		return nil, fmt.Errorf("find episode: %w", err)
	}
	if createdEpisode {
		episode.ID = newUUIDString()
		episode.ProjectId = in.ProjectID
		episode.EpisodeNo = in.EpisodeNo
		episode.EpisodeCode = fmt.Sprintf("EP%02d", in.EpisodeNo)
		episodeBriefRaw := orEmptyJSON(in.EpisodeBrief)
		episode.ProductionBrief = &episodeBriefRaw
		if _, err := tx.Exec(ctx,
			`INSERT INTO project_episodes (id, project_id, episode_no, episode_code, title, summary, production_brief)
			 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			episode.ID, episode.ProjectId, episode.EpisodeNo, episode.EpisodeCode,
			episode.Title, episode.Summary, episode.ProductionBrief); err != nil {
			return nil, fmt.Errorf("create episode: %w", err)
		}
	}

	var script model.Scripts
	err = tx.QueryRow(ctx,
		`SELECT `+scriptColumns+` FROM scripts WHERE project_id = $1 AND episode_id = $2 ORDER BY created_at LIMIT 1`,
		in.ProjectID, episode.ID).Scan(&script.ID, &script.ProjectId, &script.EpisodeId,
		&script.ScriptCode, &script.Title, &script.Content, &script.Status, &script.CurrentVersionId,
		&script.CreatedBy, &script.CreatedAt, &script.UpdatedAt)
	createdScript := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !createdScript {
		return nil, fmt.Errorf("find script: %w", err)
	}
	if createdScript {
		script.ID = newUUIDString()
		script.ProjectId = in.ProjectID
		script.EpisodeId = &episode.ID
		prefix := ptrOr(project.ProjectPrefix, ptrOr(project.ProjectNo, "RF"))
		script.ScriptCode = fmt.Sprintf("%s-EP%02d-SCRIPT", prefix, in.EpisodeNo)
		script.Title = ptr(fmt.Sprintf("第%d集", in.EpisodeNo))
		script.Content = strings.TrimSpace(in.ScriptText)
		script.Status = "draft"
		script.CreatedBy = &in.UserID
		if _, err := tx.Exec(ctx,
			`INSERT INTO scripts (id, project_id, episode_id, script_code, title, content, status, current_version_id, created_by)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			script.ID, script.ProjectId, script.EpisodeId, script.ScriptCode, script.Title,
			script.Content, script.Status, script.CurrentVersionId, script.CreatedBy); err != nil {
			return nil, fmt.Errorf("create script: %w", err)
		}
	}

	versionNo, err := r.maxScriptVersionNoTx(ctx, tx, script.ID)
	if err != nil {
		return nil, err
	}
	content := strings.TrimSpace(in.ScriptText)
	sum := sha256.Sum256([]byte(content))
	contentHash := hex.EncodeToString(sum[:])
	var dupNo int32
	err = tx.QueryRow(ctx,
		`SELECT version_no FROM script_versions WHERE script_id = $1 AND content_hash = $2 LIMIT 1`,
		script.ID, contentHash).Scan(&dupNo)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check duplicate version: %w", err)
	}
	if err == nil {
		return nil, &DuplicateVersionError{VersionNo: dupNo}
	}

	versionID := newUUIDString()
	filename := cut(in.Filename, 255)
	parserName := cut(in.ParserName, 80)
	language := cut(in.Language, 20)
	if _, err := tx.Exec(ctx,
		`INSERT INTO script_versions (
			id, script_id, version_no, source, content, content_hash, source_file_id,
			original_filename, parser_name, language, source_agent_run_id, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		versionID, script.ID, versionNo, "import", content, contentHash, in.SourceFileID,
		filename, parserName, language, nil, &in.UserID); err != nil {
		return nil, fmt.Errorf("create script version: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE scripts SET content = $2, current_version_id = $3, updated_at = now() WHERE id = $1`,
		script.ID, content, versionID); err != nil {
		return nil, fmt.Errorf("update script current version: %w", err)
	}
	if in.EpisodeBrief != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE project_episodes SET production_brief = $2, updated_at = now() WHERE id = $1`,
			episode.ID, in.EpisodeBrief); err != nil {
			return nil, fmt.Errorf("update episode brief: %w", err)
		}
	}
	if in.ProductionBrief != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE projects SET production_brief = $2 WHERE id = $1`,
			in.ProjectID, in.ProductionBrief); err != nil {
			return nil, fmt.Errorf("update project brief: %w", err)
		}
	}

	// 重建 project.script_text：全项目按 episode_no 排序的脚本拼接。
	rows, err := tx.Query(ctx,
		`SELECT s.script_code, s.title, s.content FROM scripts s
		 JOIN project_episodes e ON e.id = s.episode_id
		 WHERE s.project_id = $1 ORDER BY e.episode_no`, in.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("collect scripts: %w", err)
	}
	var parts []string
	for rows.Next() {
		var code, title, scriptContent string
		if err := rows.Scan(&code, &title, &scriptContent); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan script: %w", err)
		}
		trimmed := strings.TrimSpace(scriptContent)
		if trimmed == "" {
			continue
		}
		parts = append(parts, `--- `+code+` / `+title+` ---`+"\n"+trimmed)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scripts: %w", err)
	}
	joined := strings.TrimSpace(strings.Join(parts, "\n\n"))
	newStatus := project.Status
	if newStatus == "archived" {
		newStatus = "draft"
	}
	if _, err := tx.Exec(ctx,
		`UPDATE projects SET script_text = $2, status = $3, current_stage = $4, updated_at = now() WHERE id = $1`,
		in.ProjectID, strings.TrimSpace(joined), newStatus, newStatus); err != nil {
		return nil, fmt.Errorf("update project: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit import tx: %w", err)
	}
	return &ImportVersionResult{
		EpisodeID:       episode.ID,
		EpisodeNo:       episode.EpisodeNo,
		EpisodeCode:     episode.EpisodeCode,
		ScriptID:        script.ID,
		ScriptVersionID: versionID,
		VersionNo:       versionNo,
		CreatedEpisode:  createdEpisode,
		CreatedScript:   createdScript,
		ContentHash:     contentHash,
	}, nil
}

func (r *Projects) maxScriptVersionNoTx(ctx context.Context, q pgx.Tx, scriptID string) (int32, error) {
	var n int32
	if err := q.QueryRow(ctx,
		`SELECT coalesce(max(version_no), 0) FROM script_versions WHERE script_id = $1`,
		scriptID).Scan(&n); err != nil {
		return 0, fmt.Errorf("max script version no: %w", err)
	}
	return n + 1, nil
}

// ---- 分集状态判定 ----

// ReadingConfirmedRevision 对齐 legacy _latest_episode_reading_revision；
// 返回最新 human/confirmed/final 的 reading_report revision（作用域精确匹配）。
func (r *Projects) ReadingConfirmedRevision(ctx context.Context, projectID, episodeID string, scriptVersionID *string) (*model.ArtifactRevisions, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+artifactRevisionColumns+` FROM artifact_revisions
		 WHERE project_id = $1 AND artifact_type = 'reading_report'
		   AND source_type = 'human' AND status = 'confirmed' AND data_state = 'final'
		 ORDER BY version_no DESC, created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list reading revisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		rev, err := scanArtifactRevision(rows)
		if err != nil {
			return nil, fmt.Errorf("scan reading revision: %w", err)
		}
		snapshot := map[string]any{}
		_ = json.Unmarshal(rev.InputSnapshot, &snapshot)
		if fmt.Sprintf("%v", snapshot["episode_id"]) != episodeID {
			continue
		}
		if scriptVersionID != nil && fmt.Sprintf("%v", snapshot["script_version_id"]) != *scriptVersionID {
			continue
		}
		return rev, nil
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reading revisions: %w", err)
	}
	return nil, nil
}

// HasScriptSegments 是否存在该分集的脚本段行。
func (r *Projects) HasScriptSegments(ctx context.Context, episodeID string) (bool, error) {
	return r.exists(ctx,
		`SELECT 1 FROM script_segments WHERE episode_id = $1 LIMIT 1`, episodeID)
}

// HasStoryboards 是否存在该项目的分镜（按 episode_num 匹配）。
func (r *Projects) HasStoryboards(ctx context.Context, projectID string, episodeNo int32) (bool, error) {
	return r.exists(ctx,
		`SELECT 1 FROM storyboards WHERE project_id = $1 AND episode_num = $2 LIMIT 1`,
		projectID, episodeNo)
}

// HasActiveTasks 是否存在未退役的任务。
func (r *Projects) HasActiveTasks(ctx context.Context, episodeID string) (bool, error) {
	return r.exists(ctx,
		`SELECT 1 FROM tasks WHERE episode_id = $1 AND is_retired = false LIMIT 1`, episodeID)
}

// HasAssetBindings 是否存在当前版本的 active 资产绑定（对齐 legacy 2543-2550）。
func (r *Projects) HasAssetBindings(ctx context.Context, projectID, episodeID, scriptVersionID string) (bool, error) {
	return r.exists(ctx,
		`SELECT 1 FROM episode_asset_bindings
		 WHERE project_id = $1 AND episode_id = $2 AND script_version_id = $3 AND status = 'active'
		 LIMIT 1`, projectID, episodeID, scriptVersionID)
}

func (r *Projects) exists(ctx context.Context, q string, args ...any) (bool, error) {
	var one int
	err := r.pool.QueryRow(ctx, q, args...).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("exists check: %w", err)
	}
	return true, nil
}

// ---- 操作日志 / 文件登记 ----

// RegisterOperationLog 追加 audit 行（对齐 legacy repo.add_operation_log 的最小字段）。
func (r *Projects) RegisterOperationLog(ctx context.Context, projectID string, operatorID string, targetType string, targetID string, action string, detail any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO operation_logs (id, operator_id, project_id, target_type, target_id, action, detail)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		newUUIDString(), operatorID, projectID, targetType, targetID, action, raw)
	if err != nil {
		return fmt.Errorf("register operation log: %w", err)
	}
	return nil
}

// AddFile 登记 files 行（导入归档、多候选上传共用）。
func (r *Projects) AddFile(ctx context.Context, f *model.Files) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO files (id, file_code, bucket, object_key, file_name, mime_type, file_size,
			checksum, uploaded_by, project_id, episode_id, task_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		f.ID, f.FileCode, f.Bucket, f.ObjectKey, f.FileName, f.MimeType, f.FileSize,
		f.Checksum, f.UploadedBy, f.ProjectId, f.EpisodeId, f.TaskId)
	if err != nil {
		return fmt.Errorf("add file: %w", err)
	}
	return nil
}

// ---- agent / workflow runs ----

// AgentRunCreate agent_runs 新建入参。
type AgentRunCreate struct {
	ID              string
	ProjectID       *string
	EpisodeID       *string
	ScriptID        *string
	StoryboardID    *string
	TaskID          *string
	WorkflowRunID   *string
	AgentType       string
	Status          string
	InputJSON       json.RawMessage
	VersionNo       int32
	DataState       string
	Model           *string
	SkillName       *string
	SkillVersion    *string
	ContractVersion *string
	PromptVersion   *string
	InputHash       *string
	NodeKey         *string
}

// CreateAgentRun 插入 agent_runs（P4c 用 status=queued；非正式 jobs 用 running）。
func (r *Projects) CreateAgentRun(ctx context.Context, in AgentRunCreate) (*model.AgentRuns, error) {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO agent_runs (
			id, project_id, workflow_run_id, node_key, episode_id, script_id, task_id,
			storyboard_id, agent_type, status, input_json, version_no, data_state, model, skill_name,
			skill_version, contract_version, prompt_version, input_hash)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		in.ID, in.ProjectID, in.WorkflowRunID, in.NodeKey, in.EpisodeID, in.ScriptID, in.TaskID,
		in.StoryboardID, in.AgentType, in.Status, in.InputJSON, in.VersionNo, in.DataState, in.Model,
		in.SkillName, in.SkillVersion, in.ContractVersion, in.PromptVersion, in.InputHash)
	if err != nil {
		return nil, fmt.Errorf("create agent run: %w", err)
	}
	return r.GetAgentRun(ctx, in.ID)
}

// WorkflowRunCreate workflow_runs 新建入参。
type WorkflowRunCreate struct {
	ID             string
	ProjectID      string
	AgentRunID     *string
	WorkflowType   string
	Status         string
	NodeStatus     json.RawMessage
	Result         json.RawMessage
	Error          json.RawMessage
	Input          json.RawMessage
	CreatedBy      *string
	IDempotencyKey *string
}

// CreateWorkflowRun 插入 workflow_runs。
func (r *Projects) CreateWorkflowRun(ctx context.Context, in WorkflowRunCreate) (*model.WorkflowRuns, error) {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO workflow_runs (
			id, project_id, agent_run_id, workflow_type, status, node_status_json, result_json,
			error_json, input_json, created_by, idempotency_key)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		in.ID, in.ProjectID, in.AgentRunID, in.WorkflowType, in.Status, in.NodeStatus,
		in.Result, in.Error, in.Input, in.CreatedBy, in.IDempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("create workflow run: %w", err)
	}
	return r.FindWorkflowByID(ctx, in.ID)
}

// FindWorkflowByID 按 id 查询 workflow run。
func (r *Projects) FindWorkflowByID(ctx context.Context, id string) (*model.WorkflowRuns, error) {
	return scanWorkflowRun(r.pool.QueryRow(ctx,
		workflowRunColumnsSQL()+` FROM workflow_runs WHERE id = $1`, id))
}

// FindWorkflowByIdempotencyKey 幂等键查重；不存在返回 (nil, nil)。
func (r *Projects) FindWorkflowByIdempotencyKey(ctx context.Context, key string) (*model.WorkflowRuns, error) {
	return scanWorkflowRun(r.pool.QueryRow(ctx,
		workflowRunColumnsSQL()+` FROM workflow_runs WHERE idempotency_key = $1`, key))
}

func (r *Projects) GetAgentRun(ctx context.Context, id string) (*model.AgentRuns, error) {
	return scanAgentRun(r.pool.QueryRow(ctx,
		agentRunColumnsSQL()+` FROM agent_runs WHERE id = $1`, id))
}

// FailAgentRunEnqueue 对齐 legacy orchestrator.submit_background 入队失败路径：
// agent_runs → failed（error_message + token_usage.enqueue_failed），
// workflow_runs → failed（error_json.error_message + result_json.agent_run_ids 合并）。
func (r *Projects) FailAgentRunEnqueue(ctx context.Context, agentRunID, workflowID, errorMessage string) error {
	tokenUsage := `{"worker": "celery", "enqueue_failed": true}`
	if _, err := r.pool.Exec(ctx,
		`UPDATE agent_runs SET status = 'failed', error_message = $2, token_usage = $3::jsonb, updated_at = now()
		  WHERE id = $1`, agentRunID, errorMessage, tokenUsage); err != nil {
		return fmt.Errorf("fail agent run enqueue: %w", err)
	}
	var resultJSON json.RawMessage
	var errorJSON json.RawMessage
	err := r.pool.QueryRow(ctx,
		`SELECT result_json, error_json FROM workflow_runs WHERE id = $1`, workflowID).
		Scan(&resultJSON, &errorJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // workflow 行不存在则只标记 agent_run
		}
		return fmt.Errorf("load workflow for enqueue failure: %w", err)
	}
	result := map[string]any{}
	_ = json.Unmarshal(resultJSON, &result)
	if result == nil {
		result = map[string]any{}
	}
	result["agent_run_ids"] = []string{agentRunID}
	errorShape := map[string]any{
		"error_message": errorMessage,
		"errors":        []any{},
		"warnings":      []any{},
		"node_runtime":  map[string]any{},
		"agent_run_ids": []string{agentRunID},
	}
	mergedResult, _ := json.Marshal(result)
	mergedError, _ := json.Marshal(errorShape)
	if _, err := r.pool.Exec(ctx,
		`UPDATE workflow_runs SET status = 'failed', result_json = $2::jsonb, error_json = $3::jsonb, updated_at = now()
		  WHERE id = $1`, workflowID, mergedResult, mergedError); err != nil {
		return fmt.Errorf("fail workflow run enqueue: %w", err)
	}
	return nil
}

// PendingAgentRun 对齐 legacy get_pending_agent_run 的简化匹配：
// 同项目同 agent_type、parent_run_id IS NULL、status queued/running，且 input 顶层命中作用域。
func (r *Projects) PendingAgentRun(ctx context.Context, projectID, agentType string, episodeID, scriptVersionID *string) (*model.AgentRuns, error) {
	rows, err := r.pool.Query(ctx,
		agentRunColumnsSQL()+` FROM agent_runs
		 WHERE project_id = $1 AND agent_type = $2 AND parent_run_id IS NULL
		   AND status IN ('queued','running')
		 ORDER BY created_at DESC`, projectID, agentType)
	if err != nil {
		return nil, fmt.Errorf("pending agent run: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		run, err := scanAgentRun(rows)
		if err != nil {
			return nil, err
		}
		if payloadMatchesLineage(run.InputJson, episodeID, scriptVersionID) {
			return run, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

// payloadMatchesLineage 顶层作用域匹配（P3d 创建的 run 将 episode_id/script_version_id 置于顶层）。
func payloadMatchesLineage(raw json.RawMessage, episodeID, scriptVersionID *string) bool {
	var m struct {
		EpisodeID       *string `json:"episode_id"`
		ScriptVersionID *string `json:"script_version_id"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	if episodeID != nil && (m.EpisodeID == nil || *m.EpisodeID != *episodeID) {
		return false
	}
	if scriptVersionID != nil && (m.ScriptVersionID == nil || *m.ScriptVersionID != *scriptVersionID) {
		return false
	}
	return true
}

// AgentRunColumns / scans
const agentRunColumns = `id, project_id, parent_run_id, workflow_run_id, node_key, episode_id, script_id,
	storyboard_id, task_id, agent_type, status, input_json, output_json, summary_json, payload_ref,
	payload_size_bytes, payload_sha256, error_message, token_usage, duration_ms, feedback_json,
	version_no, model, skill_name, skill_version, contract_version, contract_hash, prompt_version,
	input_hash, data_state, created_at, updated_at`

func agentRunColumnsSQL() string { return "SELECT " + agentRunColumns }

func scanAgentRun(row pgx.Row) (*model.AgentRuns, error) {
	var r model.AgentRuns
	err := row.Scan(&r.ID, &r.ProjectId, &r.ParentRunId, &r.WorkflowRunId, &r.NodeKey,
		&r.EpisodeId, &r.ScriptId, &r.StoryboardId, &r.TaskId, &r.AgentType, &r.Status,
		&r.InputJson, &r.OutputJson, &r.SummaryJson, &r.PayloadRef, &r.PayloadSizeBytes,
		&r.PayloadSha256, &r.ErrorMessage, &r.TokenUsage, &r.DurationMs, &r.FeedbackJson,
		&r.VersionNo, &r.Model, &r.SkillName, &r.SkillVersion, &r.ContractVersion,
		&r.ContractHash, &r.PromptVersion, &r.InputHash, &r.DataState, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan agent run: %w", err)
	}
	return &r, nil
}

const workflowRunColumns = `id, project_id, agent_run_id, workflow_type, status, node_status_json,
	result_json, summary_json, payload_ref, payload_size_bytes, payload_sha256, error_json,
	input_json, created_by, lock_key, lock_token, idempotency_key, created_at, updated_at`

func workflowRunColumnsSQL() string { return "SELECT " + workflowRunColumns }

func scanWorkflowRun(row pgx.Row) (*model.WorkflowRuns, error) {
	var r model.WorkflowRuns
	err := row.Scan(&r.ID, &r.ProjectId, &r.AgentRunId, &r.WorkflowType, &r.Status,
		&r.NodeStatusJson, &r.ResultJson, &r.SummaryJson, &r.PayloadRef, &r.PayloadSizeBytes,
		&r.PayloadSha256, &r.ErrorJson, &r.InputJson, &r.CreatedBy, &r.LockKey, &r.LockToken,
		&r.IDempotencyKey, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan workflow run: %w", err)
	}
	return &r, nil
}

// ---- artifact revisions ----

const artifactRevisionColumns = `id, project_id, artifact_type, artifact_id, version_no, parent_revision_id,
	source_type, source_run_id, skill_name, skill_version, contract_version, contract_hash, model_name,
	prompt_version, input_hash, input_snapshot, raw_output, normalized_content, change_diff, content_hash,
	status, created_by, data_state, payload_ref, payload_size_bytes, payload_sha256, idempotency_key, created_at, updated_at`

func scanArtifactRevision(row pgx.Row) (*model.ArtifactRevisions, error) {
	var r model.ArtifactRevisions
	err := row.Scan(&r.ID, &r.ProjectId, &r.ArtifactType, &r.ArtifactId, &r.VersionNo,
		&r.ParentRevisionId, &r.SourceType, &r.SourceRunId, &r.SkillName, &r.SkillVersion,
		&r.ContractVersion, &r.ContractHash, &r.ModelName, &r.PromptVersion, &r.InputHash,
		&r.InputSnapshot, &r.RawOutput, &r.NormalizedContent, &r.ChangeDiff, &r.ContentHash,
		&r.Status, &r.CreatedBy, &r.DataState, &r.PayloadRef, &r.PayloadSizeBytes,
		&r.PayloadSha256, &r.IDempotencyKey, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan artifact revision: %w", err)
	}
	return &r, nil
}

// AppendArtifactRevision 追加一条 artifact revision。
func (r *Projects) AppendArtifactRevision(ctx context.Context, rev *model.ArtifactRevisions) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO artifact_revisions (
			id, project_id, artifact_type, artifact_id, version_no, parent_revision_id, source_type,
			source_run_id, skill_name, skill_version, contract_version, contract_hash, model_name,
			prompt_version, input_hash, input_snapshot, raw_output, normalized_content, change_diff,
			content_hash, status, created_by, data_state, payload_ref, payload_size_bytes, payload_sha256,
			idempotency_key)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)`,
		rev.ID, rev.ProjectId, rev.ArtifactType, rev.ArtifactId, rev.VersionNo, rev.ParentRevisionId,
		rev.SourceType, rev.SourceRunId, rev.SkillName, rev.SkillVersion, rev.ContractVersion,
		rev.ContractHash, rev.ModelName, rev.PromptVersion, rev.InputHash, rev.InputSnapshot,
		rev.RawOutput, rev.NormalizedContent, rev.ChangeDiff, rev.ContentHash, rev.Status,
		rev.CreatedBy, rev.DataState, rev.PayloadRef, rev.PayloadSizeBytes, rev.PayloadSha256,
		rev.IDempotencyKey)
	if err != nil {
		return fmt.Errorf("append artifact revision: %w", err)
	}
	return nil
}

// GetArtifactRevisionByID 按主键读取 artifact revision；不存在返回 (nil, nil)。
func (r *Projects) GetArtifactRevisionByID(ctx context.Context, id string) (*model.ArtifactRevisions, error) {
	return scanArtifactRevision(r.pool.QueryRow(ctx,
		`SELECT `+artifactRevisionColumns+` FROM artifact_revisions WHERE id = $1`, id))
}

// ListArtifactRevisionsForArtifact 列出某 artifact 的全部 revision（version 升序，对齐 legacy 客观顺序）。
func (r *Projects) ListArtifactRevisionsForArtifact(ctx context.Context, artifactType, artifactID string) ([]model.ArtifactRevisions, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+artifactRevisionColumns+` FROM artifact_revisions
		 WHERE artifact_type = $1 AND artifact_id = $2 ORDER BY version_no`, artifactType, artifactID)
	if err != nil {
		return nil, fmt.Errorf("list artifact revisions: %w", err)
	}
	defer rows.Close()
	var out []model.ArtifactRevisions
	for rows.Next() {
		rev, err := scanArtifactRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MaxArtifactVersionNo 返回给定 artifact 的最大版本号。
func (r *Projects) MaxArtifactVersionNo(ctx context.Context, artifactType, artifactID string) (int32, error) {
	var n int32
	if err := r.pool.QueryRow(ctx,
		`SELECT coalesce(max(version_no), 0) FROM artifact_revisions WHERE artifact_type = $1 AND artifact_id = $2`,
		artifactType, artifactID).Scan(&n); err != nil {
		return 0, fmt.Errorf("max artifact version no: %w", err)
	}
	return n, nil
}

// ---- breakdown 视图中转 ----

// LatestBreakdown 取项目最新 ScriptBreakdown（version 倒序）。
func (r *Projects) LatestBreakdown(ctx context.Context, projectID string) (*model.ScriptBreakdowns, error) {
	var b model.ScriptBreakdowns
	err := r.pool.QueryRow(ctx,
		`SELECT id, project_id, version, content_json, agent_run_id, edited_by_id, data_state, created_at, updated_at
		 FROM script_breakdowns WHERE project_id = $1 ORDER BY version DESC, created_at DESC LIMIT 1`, projectID).
		Scan(&b.ID, &b.ProjectId, &b.Version, &b.ContentJson, &b.AgentRunId,
			&b.EditedById, &b.DataState, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan breakdown: %w", err)
	}
	return &b, nil
}

// createBreakdownScan 扫描 script_breakdowns 行（LatestBreakdown/LatestScopedBreakdown 共用）。
func createBreakdownScan(row pgx.Row, b *model.ScriptBreakdowns) error {
	return row.Scan(&b.ID, &b.ProjectId, &b.Version, &b.ContentJson, &b.AgentRunId,
		&b.EditedById, &b.DataState, &b.CreatedAt, &b.UpdatedAt)
}

// LatestScopedBreakdown 取项目最新且匹配作用域（episode_id/script_version_id 命中的 content_json）的
// ScriptBreakdown（version 倒序；对齐 legacy get_current_breakdown 的 lineage 过滤）。
func (r *Projects) LatestScopedBreakdown(ctx context.Context, projectID string, episodeID, scriptVersionID *string) (*model.ScriptBreakdowns, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, project_id, version, content_json, agent_run_id, edited_by_id, data_state, created_at, updated_at
		 FROM script_breakdowns WHERE project_id = $1
		 ORDER BY version DESC, created_at DESC
		 LIMIT 100`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list breakdowns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b model.ScriptBreakdowns
		if err := createBreakdownScan(rows, &b); err != nil {
			return nil, err
		}
		if breakdownContentMatchesLineage(b.ContentJson, episodeID, scriptVersionID) {
			return &b, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate breakdowns: %w", err)
	}
	return nil, nil
}

// breakdownContentMatchesLineage 对齐 legacy _payload_matches_lineage_values 的顶层命中分支：
// content_json 顶层出现 episode_id/script_version_id 时按值精确匹配；两者都缺省视为全量匹配。
func breakdownContentMatchesLineage(raw json.RawMessage, episodeID, scriptVersionID *string) bool {
	if episodeID == nil && scriptVersionID == nil {
		return true
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	directEpisode, hasEpisode := m["episode_id"]
	directVersion, hasVersion := m["script_version_id"]
	if !hasEpisode && !hasVersion {
		return false
	}
	if episodeID != nil && hasEpisode && fmt.Sprintf("%v", directEpisode) != *episodeID {
		return false
	}
	if scriptVersionID != nil && hasVersion && fmt.Sprintf("%v", directVersion) != *scriptVersionID {
		return false
	}
	return true
}

// UpdateWorkflowRunAgentRun 回填 workflow_runs.agent_run_id（对齐 legacy update_workflow_run_from_agent_run）。
func (r *Projects) UpdateWorkflowRunAgentRun(ctx context.Context, workflowID, agentRunID string) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE workflow_runs SET agent_run_id = $2, updated_at = now() WHERE id = $1`,
		workflowID, agentRunID); err != nil {
		return fmt.Errorf("update workflow agent run: %w", err)
	}
	return nil
}

// UpdateProjectStage 更新项目 status/current_stage（拆解落库后进入 breakdown_review）。
func (r *Projects) UpdateProjectStage(ctx context.Context, projectID, status string) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE projects SET status = $2, current_stage = $3, updated_at = now() WHERE id = $1`,
		projectID, status, status); err != nil {
		return fmt.Errorf("update project stage: %w", err)
	}
	return nil
}

// UpdateNextBreakdownVersion 计算并插入新的 script_breakdowns 行：
// version = 项目全量 max(version)+1（对齐 save_breakdown_revision，非作用域内计数）。
func (r *Projects) CreateBreakdownVersion(ctx context.Context, content json.RawMessage, projectID string, editedByID *string, dataState string) (*model.ScriptBreakdowns, error) {
	var version int32
	if err := r.pool.QueryRow(ctx,
		`SELECT coalesce(max(version), 0) FROM script_breakdowns WHERE project_id = $1`, projectID).Scan(&version); err != nil {
		return nil, fmt.Errorf("max breakdown version: %w", err)
	}
	b := &model.ScriptBreakdowns{
		ID:          newUUIDString(),
		ProjectId:   projectID,
		Version:     version + 1,
		ContentJson: content,
		EditedById:  editedByID,
		DataState:   dataState,
	}
	if err := r.CreateBreakdown(ctx, b); err != nil {
		return nil, err
	}
	created, err := r.FindBreakdownByID(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	if created == nil {
		created = b
	}
	return created, nil
}

// FindBreakdownByID 按 id 查询 script_breakdowns。
func (r *Projects) FindBreakdownByID(ctx context.Context, id string) (*model.ScriptBreakdowns, error) {
	var b model.ScriptBreakdowns
	err := r.pool.QueryRow(ctx,
		`SELECT id, project_id, version, content_json, agent_run_id, edited_by_id, data_state, created_at, updated_at
		 FROM script_breakdowns WHERE id = $1`, id).
		Scan(&b.ID, &b.ProjectId, &b.Version, &b.ContentJson, &b.AgentRunId,
			&b.EditedById, &b.DataState, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan breakdown: %w", err)
	}
	return &b, nil
}

// CreateBreakdown 插入 script_breakdowns 行（version 由调用方计算）。
func (r *Projects) CreateBreakdown(ctx context.Context, b *model.ScriptBreakdowns) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO script_breakdowns (id, project_id, version, content_json, agent_run_id, edited_by_id, data_state)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		b.ID, b.ProjectId, b.Version, b.ContentJson, b.AgentRunId, b.EditedById, b.DataState)
	if err != nil {
		return fmt.Errorf("create breakdown: %w", err)
	}
	return nil
}

// ---- 工具 ----

func newUUIDString() string {
	id, err := auth.NewUUID()
	if err != nil {
		panic(fmt.Sprintf("generate uuid: %v", err))
	}
	return id
}

func ptr[T any](v T) *T { return &v }

func ptrOr[T any](v *T, fallback T) T {
	if v == nil {
		return fallback
	}
	return *v
}

func cut(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

func orEmptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

// ContentHashString 计算 JSON 稳定 hash（legacy _content_hash 的 Go 等价：
// encoding/json 对 map 按键排序 → 确定性输出；跨系统 hex 不同属预期，仅内部一致）。
func ContentHashString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		raw = []byte(`null`)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// AgentRunPageSpec 对齐 legacy list_agent_run_page；游标 base64("created_at_rfc3339|id")。
type AgentRunPageSpec struct {
	ProjectID *string
	Limit     int
	Cursor    string
}

// AgentRunPageResult 游标下一页结果。
type AgentRunPageResult struct {
	Rows       []model.AgentRuns
	HasMore    bool
	NextCursor *string
}

// DecodeAgentRunCursor 解析游标；非法返回 (nil, nil)，调用方按无游标处理。
func DecodeAgentRunCursor(cursor string) (*time.Time, *string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return nil, nil, nil
	}
	text := string(raw)
	idx := strings.LastIndexByte(text, '|')
	if idx <= 0 || idx == len(text)-1 {
		return nil, nil, nil
	}
	created, err := time.Parse(time.RFC3339Nano, text[:idx])
	if err != nil {
		return nil, nil, nil
	}
	id := text[idx+1:]
	return &created, &id, nil
}

// EncodeAgentRunCursor 编码游标（对齐 legacy _encode_page_cursor）。
func EncodeAgentRunCursor(createdAt time.Time, id string) string {
	payload := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.StdEncoding.EncodeToString([]byte(payload))
}

// ListAgentRunPage 分页列出 agent runs（created_at/id 倒序；对齐 legacy 游标语义）。
func (r *Projects) ListAgentRunPage(ctx context.Context, spec AgentRunPageSpec) (*AgentRunPageResult, error) {
	q := `SELECT ` + agentRunColumns + ` FROM agent_runs WHERE 1=1`
	args := []any{}
	if spec.ProjectID != nil {
		args = append(args, *spec.ProjectID)
		q += fmt.Sprintf(" AND project_id = $%d", len(args))
	}
	if spec.Cursor != "" {
		created, id, err := DecodeAgentRunCursor(spec.Cursor)
		if err != nil || (created == nil || id == nil) {
			created, id = nil, nil
		}
		if created != nil && id != nil {
			args = append(args, *created, *id)
			q += fmt.Sprintf(` AND (created_at < $%d OR (created_at = $%[1]d AND id < $%d))`, len(args)-1, len(args))
		}
	}
	args = append(args, spec.Limit+1)
	q += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(args))
	if spec.Limit <= 0 || spec.Limit > 200 {
		spec.Limit = 50
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent run page: %w", err)
	}
	defer rows.Close()
	var out []model.AgentRuns
	for rows.Next() {
		run, err := scanAgentRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := &AgentRunPageResult{}
	if len(out) > spec.Limit {
		result.HasMore = true
		out = out[:spec.Limit]
	}
	if result.HasMore && len(out) > 0 {
		last := out[len(out)-1]
		cursor := EncodeAgentRunCursor(last.CreatedAt, last.ID)
		result.NextCursor = &cursor
	}
	result.Rows = out
	return result, nil
}

// ---- dashboard 聚合（对齐 legacy get_dashboard，缓存由 P3g 接入）----

// DashboardCounts 聚合项目 dashboard 指标。
type DashboardCounts struct {
	StoryboardCount int
	AssetCount      int
	TaskCount       int
	CompletedCount  int
	OverdueCount    int
	CompletionRate  float64
}

// DashboardCounts 计算 dashboard 聚合（isDirector=false 时任务/分镜走可见过滤）。
func (r *Projects) DashboardCounts(ctx context.Context, projectID, userID string, isDirector bool) (*DashboardCounts, error) {
	var out DashboardCounts

	var total, completed int
	if isDirector {
		if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM tasks
			WHERE is_retired = false AND variant_plan_id IS NULL AND project_id = $1`, projectID).Scan(&total); err != nil {
			return nil, fmt.Errorf("dashboard task count: %w", err)
		}
	} else {
		if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE is_retired = false
			AND (asset_id IS NULL OR asset_id NOT IN (SELECT id FROM assets WHERE status IN ('deleted','excluded')))
			AND variant_plan_id IS NULL AND project_id = $1
			AND (project_id IN (SELECT project_id FROM permissions WHERE user_id = $2 AND project_id IS NOT NULL
			   AND permission_type LIKE '`+ProjectRolePrefix+`%')
			 OR ((assignee_id = $2) AND (status <> 'completed' OR visible_until IS NULL OR visible_until >= now())))`,
			projectID, userID).Scan(&total); err != nil {
			return nil, fmt.Errorf("dashboard task count (scoped): %w", err)
		}
	}
	out.TaskCount = total

	completedStmt := `SELECT count(*) FROM tasks WHERE status = 'completed' AND is_retired = false AND variant_plan_id IS NULL AND project_id = $1`
	overdueStmt := `SELECT count(*) FROM tasks WHERE status <> 'completed' AND is_retired = false AND variant_plan_id IS NULL
		AND project_id = $1 AND due_at IS NOT NULL AND due_at < now()`
	if isDirector {
		if err := r.pool.QueryRow(ctx, completedStmt, projectID).Scan(&completed); err != nil {
			return nil, fmt.Errorf("dashboard completed count: %w", err)
		}
		if err := r.pool.QueryRow(ctx, overdueStmt, projectID).Scan(&out.OverdueCount); err != nil {
			return nil, fmt.Errorf("dashboard overdue count: %w", err)
		}
	} else {
		scoped := ` AND (project_id IN (SELECT project_id FROM permissions WHERE user_id = $2 AND project_id IS NOT NULL
			AND permission_type LIKE '` + ProjectRolePrefix + `%')
			OR ((assignee_id = $2) AND (status <> 'completed' OR visible_until IS NULL OR visible_until >= now())))`
		if err := r.pool.QueryRow(ctx, completedStmt+scoped, projectID, userID).Scan(&completed); err != nil {
			return nil, fmt.Errorf("dashboard completed count (scoped): %w", err)
		}
		if err := r.pool.QueryRow(ctx, overdueStmt+scoped, projectID, userID).Scan(&out.OverdueCount); err != nil {
			return nil, fmt.Errorf("dashboard overdue count (scoped): %w", err)
		}
	}
	out.CompletedCount = completed
	if total > 0 {
		out.CompletionRate = math.Round(float64(completed)/float64(total)*100) / 100
	}

	if isDirector {
		if err := r.pool.QueryRow(ctx,
			`SELECT count(*) FROM storyboards WHERE project_id = $1`, projectID).Scan(&out.StoryboardCount); err != nil {
			return nil, fmt.Errorf("dashboard storyboard count: %w", err)
		}
	} else {
		if err := r.pool.QueryRow(ctx,
			`SELECT count(DISTINCT storyboard_id) FROM tasks WHERE is_retired = false AND variant_plan_id IS NULL
			 AND project_id = $1 AND storyboard_id IS NOT NULL
			 AND (project_id IN (SELECT project_id FROM permissions WHERE user_id = $2 AND project_id IS NOT NULL
				AND permission_type LIKE '`+ProjectRolePrefix+`%')
			  OR ((assignee_id = $2) AND (status <> 'completed' OR visible_until IS NULL OR visible_until >= now())))`,
			projectID, userID).Scan(&out.StoryboardCount); err != nil {
			return nil, fmt.Errorf("dashboard storyboard count (scoped): %w", err)
		}
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM assets WHERE project_id = $1 OR project_id IS NULL`, projectID).Scan(&out.AssetCount); err != nil {
		return nil, fmt.Errorf("dashboard asset count: %w", err)
	}
	return &out, nil
}

// ---- 脚本段 / 分镜任务（拆解门禁读取）----

// ListScriptSegments 列出门禁用脚本段；status 非空时仅返回 metadata 中 status 匹配的行
// （对齐 legacy list_script_segments(..., status="script_confirmed")）。
func (r *Projects) ListScriptSegments(ctx context.Context, projectID string, episodeID *string, status string) ([]model.ScriptSegments, error) {
	q := `SELECT id, project_id, episode_id, script_segment_code, episode_code, order_no, title,
		source_text, summary, story_function, dominant_emotion, rhythm, viewpoint, context_code,
		render_mode, metadata_json, current_version_id, created_by, created_at, updated_at
		FROM script_segments WHERE project_id = $1`
	args := []any{projectID}
	if episodeID != nil {
		args = append(args, *episodeID)
		q += ` AND episode_id = $2`
	}
	q += ` ORDER BY episode_code, order_no`
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list script segments: %w", err)
	}
	defer rows.Close()
	var out []model.ScriptSegments
	for rows.Next() {
		var s model.ScriptSegments
		if err := rows.Scan(&s.ID, &s.ProjectId, &s.EpisodeId, &s.ScriptSegmentCode, &s.EpisodeCode,
			&s.OrderNo, &s.Title, &s.SourceText, &s.Summary, &s.StoryFunction, &s.DominantEmotion,
			&s.Rhythm, &s.Viewpoint, &s.ContextCode, &s.RenderMode, &s.MetadataJson,
			&s.CurrentVersionId, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan script segment: %w", err)
		}
		if status != "" && segmentMetadataStatus(s.MetadataJson) != status {
			continue
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate script segments: %w", err)
	}
	return out, nil
}

// segmentMetadataStatus 读取 segment.metadata_json.status（legacy metadata 取 status）。
func segmentMetadataStatus(raw json.RawMessage) string {
	var m struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	return m.Status
}

// StoryboardTasksDistributed 该分集是否存在已分发的分镜任务（对齐 legacy storyboard_tasks_distributed）。
func (r *Projects) StoryboardTasksDistributed(ctx context.Context, projectID, episodeID string) (bool, error) {
	return r.exists(ctx,
		`SELECT 1 FROM tasks t JOIN storyboards s ON s.id = t.storyboard_id
		 WHERE t.is_retired = false AND s.project_id = $1
		   AND s.episode_num = (SELECT episode_no FROM project_episodes WHERE id = $2) LIMIT 1`,
		projectID, episodeID)
}

// HasActiveProjectTasks 项目是否存在非退役且处于活动状态的任务（PATCH 制作设置保护）。
func (r *Projects) HasActiveProjectTasks(ctx context.Context, projectID string) (bool, error) {
	return r.exists(ctx,
		`SELECT 1 FROM tasks WHERE project_id = $1 AND is_retired = false
		 AND status IN ('todo','in_progress','submitted','reviewing','overdue') LIMIT 1`, projectID)
}

// HasScripts 项目是否已导入剧本（前缀锁判断）。
func (r *Projects) HasScripts(ctx context.Context, projectID string) (bool, error) {
	return r.exists(ctx, `SELECT 1 FROM scripts WHERE project_id = $1 LIMIT 1`, projectID)
}

// UpdateProjectFields 更新项目可编辑列（对齐 legacy update_project）。
func (r *Projects) UpdateProjectFields(ctx context.Context, p *model.Projects) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE projects SET title = $2, name = $3, genre = $4, script_text = $5, project_prefix = $6,
		 manager_id = $7, production_brief = $8, updated_at = now() WHERE id = $1`,
		p.ID, p.Title, p.Name, p.Genre, p.ScriptText, p.ProjectPrefix, p.ManagerId, p.ProductionBrief)
	if err != nil {
		return fmt.Errorf("update project fields: %w", err)
	}
	return nil
}

// UpdateEpisodeProductionBrief 更新分集制作简报。
func (r *Projects) UpdateEpisodeProductionBrief(ctx context.Context, episodeID string, brief json.RawMessage) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE project_episodes SET production_brief = $2, updated_at = now() WHERE id = $1`,
		episodeID, brief)
	if err != nil {
		return fmt.Errorf("update episode production brief: %w", err)
	}
	return nil
}

// UpdateEpisodeSummary 回填分集故事梗概（围读确认时 _reading_summary(confirmed_report)）。
func (r *Projects) UpdateEpisodeSummary(ctx context.Context, episodeID, summary string) error {
	if _, err := r.pool.Exec(ctx,
		`UPDATE project_episodes SET summary = $2, updated_at = now() WHERE id = $1`,
		episodeID, summary); err != nil {
		return fmt.Errorf("update episode summary: %w", err)
	}
	return nil
}

// ---- run-events 快照（P4d，对齐 legacy streaming._project_run_snapshot 的形状）----

// ListWorkflowRuns 列出项目 workflow runs（created_at/id 倒序 LIMIT），供 run-events 快照。
func (r *Projects) ListWorkflowRuns(ctx context.Context, projectID string, limit int) ([]model.WorkflowRuns, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx,
		workflowRunColumnsSQL()+` FROM workflow_runs
		 WHERE project_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("list workflow runs: %w", err)
	}
	defer rows.Close()
	var out []model.WorkflowRuns
	for rows.Next() {
		w, err := scanWorkflowRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// agentRunSummaryColumns 快照专用轻量列集：排除 input/output/token_usage/feedback 大列
// （对齐 legacy list_agent_run_page 的 defer 语义，SSE 心跳期间不反复读 blob）。
const agentRunSummaryColumns = `id, project_id, parent_run_id, storyboard_id, task_id, agent_type, status,
	error_message, duration_ms, model, skill_name, skill_version, contract_version, contract_hash,
	prompt_version, input_hash, node_key, workflow_run_id, summary_json, payload_ref,
	payload_size_bytes, payload_sha256, created_at, updated_at`

// scanAgentRunSummary 扫描轻量列集（用于快照；剩余列零值）。
func scanAgentRunSummary(row pgx.Row) (*model.AgentRuns, error) {
	var r model.AgentRuns
	err := row.Scan(&r.ID, &r.ProjectId, &r.ParentRunId, &r.StoryboardId, &r.TaskId, &r.AgentType,
		&r.Status, &r.ErrorMessage, &r.DurationMs, &r.Model, &r.SkillName, &r.SkillVersion,
		&r.ContractVersion, &r.ContractHash, &r.PromptVersion, &r.InputHash, &r.NodeKey,
		&r.WorkflowRunId, &r.SummaryJson, &r.PayloadRef, &r.PayloadSizeBytes, &r.PayloadSha256,
		&r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan agent run summary: %w", err)
	}
	return &r, nil
}

// ListAgentRunSnapshots 轻量列出 agent runs（created_at/id 倒序 LIMIT 100；对齐 legacy snapshot 用法）。
func (r *Projects) ListAgentRunSnapshots(ctx context.Context, projectID string, limit int) ([]model.AgentRuns, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+agentRunSummaryColumns+` FROM agent_runs
		 WHERE project_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent run snapshots: %w", err)
	}
	defer rows.Close()
	var out []model.AgentRuns
	for rows.Next() {
		run, err := scanAgentRunSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

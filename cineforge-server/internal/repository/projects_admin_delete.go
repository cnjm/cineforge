package repository

// P3d 回补 + P3f-e：分集级联删除（对齐 legacy repositories.py hard_delete_project_episode）。
// 文件清理链路：级联删除事务内收集候选（delete_files 门控）→ 提交后由 service 层删除 MinIO 对象
// → FinalizeEpisodeDeleteFileCleanupTx 删除成功清理的文件行并回填操作日志（对齐 cleanup_deleted_episode_files）。

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// EpisodeSnapshot 供删除前确认的一集最小信息。
type EpisodeSnapshot struct {
	EpisodeID   string
	EpisodeCode string
	EpisodeNo   int32
	ProjectID   string
}

// FindEpisodeForDelete 返回一集的最小信息：不存在或不属于项目 → ok=false，不做 404/403 映射。
func (r *Projects) FindEpisodeForDelete(ctx context.Context, projectID, episodeID string) (*EpisodeSnapshot, bool, error) {
	var snap EpisodeSnapshot
	err := r.pool.QueryRow(ctx,
		`SELECT id, episode_code, episode_no, project_id FROM project_episodes WHERE id=$1`,
		episodeID).Scan(&snap.EpisodeID, &snap.EpisodeCode, &snap.EpisodeNo, &snap.ProjectID)
	if err == pgx.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if snap.ProjectID != projectID {
		return nil, false, nil
	}
	return &snap, true, nil
}

// deleteCounts 记录各表删除行数（对齐 ProjectEpisodeDeleteResponse.deleted_counts）。
type deleteCounts struct {
	tallies map[string]int
}

func newDeleteCounts() *deleteCounts {
	return &deleteCounts{tallies: map[string]int{}}
}

func (c *deleteCounts) set(label string, n int) { c.tallies[label] = n }
func (c *deleteCounts) list() map[string]int {
	out := make(map[string]int, len(c.tallies))
	for k, v := range c.tallies {
		out[k] = v
	}
	return out
}

// HardDeleteProjectEpisodeTx 对齐 legacy hard_delete_project_episode。
// 调用前保证：project、episode 存在且 episode 属于 project、确认码已校验。
func (r *Projects) HardDeleteProjectEpisodeTx(ctx context.Context, projectID, episodeID, episodeCode string, deleteFiles bool, userID string) (*EpisodeDeleteResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin episode delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var episodeNo int32
	if err := tx.QueryRow(ctx,
		`SELECT episode_no FROM project_episodes WHERE id=$1 FOR UPDATE`, episodeID).Scan(&episodeNo); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("episode gone: %w", pgx.ErrNoRows)
		}
		return nil, err
	}
	project, err := lockProjectTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, fmt.Errorf("project gone: %w", pgx.ErrNoRows)
	}

	scriptIDs := queryIDs(ctx, tx,
		`SELECT id FROM scripts WHERE project_id=$1 AND episode_id=$2`, projectID, episodeID)
	scriptIDSet := toSet(scriptIDs)
	scriptVersionIDs := queryIDs(ctx, tx,
		`SELECT id FROM script_versions WHERE script_id = ANY($1)`, toArray(scriptIDs))
	scriptVersionIDSet := toSet(scriptVersionIDs)
	// source_file_ids 只保留“该分集自己的”源文件（跨项目/跨分集共享的不标记）。
	sourceFileIDs := []string{}
	eachRow(ctx, tx,
		`SELECT source_file_id FROM script_versions WHERE script_id = ANY($1) AND source_file_id IS NOT NULL`, []any{toArray(scriptIDs)},
		func(rows pgx.Rows) error {
			var id *string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			if id != nil {
				sourceFileIDs = append(sourceFileIDs, *id)
			}
			return nil
		})

	segmentIDs := queryIDs(ctx, tx,
		`SELECT id FROM script_segments WHERE project_id=$1 AND (episode_id=$2 OR episode_code=$3)`,
		projectID, episodeID, episodeCode)

	storyboardIDs := []string{}
	rows, err := tx.Query(ctx,
		`SELECT id FROM storyboards WHERE project_id=$1 AND episode_num=$2`,
		projectID, episodeNo)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		storyboardIDs = append(storyboardIDs, id)
	}
	rows.Close()
	if len(scriptIDs) > 0 {
		storyboardIDs = append(storyboardIDs, queryIDs(ctx, tx,
			`SELECT id FROM storyboards WHERE project_id=$1 AND script_id = ANY($2)`, projectID, toArray(scriptIDs))...)
	}
	if len(segmentIDs) > 0 {
		storyboardIDs = append(storyboardIDs, queryIDs(ctx, tx,
			`SELECT id FROM storyboards WHERE project_id=$1 AND script_segment_id = ANY($2)`, projectID, toArray(segmentIDs))...)
	}
	storyboardSet := toSet(storyboardIDs)

	taskIDs := queryIDs(ctx, tx,
		`SELECT id FROM tasks WHERE project_id=$1 AND episode_id=$2`, projectID, episodeID)
	taskIDs = appendTaskMembership(ctx, tx, taskIDs, taskFilenames{
		scriptIDs:      scriptIDs,
		segmentIDs:     segmentIDs,
		storyboardIDs:  storyboardIDs,
		projectID:      projectID,
	})

	imageIDs := outputIDs(ctx, tx, "image_outputs", projectID, episodeNo, scriptIDs, storyboardIDs, taskIDs, episodeID, nil)
	videoIDs := outputIDs(ctx, tx, "video_outputs", projectID, episodeNo, scriptIDs, storyboardIDs, taskIDs, episodeID, nil)
	finalIDs := outputIDs(ctx, tx, "final_outputs", projectID, episodeNo, scriptIDs, storyboardIDs, taskIDs, episodeID, episodeID)

	candidateFileIDSet := map[string]bool{}
	for _, id := range sourceFileIDs {
		candidateFileIDSet[id] = true
	}
	for _, t := range []struct {
		table string
		col   string
	}{{"image_outputs", "file_id"}, {"video_outputs", "file_id"}, {"final_outputs", "file_id"}} {
		for _, f := range outputByte(ContextReuse(ctx), tx, t.table, projectID, episodeNo, scriptIDs, storyboardIDs, taskIDs, episodeID, t.col) {
			candidateFileIDSet[f] = true
		}
	}
	if len(episodeID) > 0 {
		for _, id := range queryIDs(ctx, tx, `SELECT id FROM files WHERE episode_id=$1`, episodeID) {
			candidateFileIDSet[id] = true
		}
	}
	if len(taskIDs) > 0 {
		for _, id := range queryIDs(ctx, tx, `SELECT id FROM files WHERE task_id = ANY($1)`, toArray(taskIDs)) {
			candidateFileIDSet[id] = true
		}
	}

	submissionLocations := map[string]bool{}
	if len(taskIDs) > 0 || len(storyboardIDs) > 0 {
		eachRow(ctx, tx,
			`SELECT file_path FROM submissions WHERE task_id = ANY($1) OR storyboard_id = ANY($2)`,
			[]any{toArray(taskIDs), toArray(storyboardIDs)},
			func(rows pgx.Rows) error {
				var path *string
				if err := rows.Scan(&path); err != nil {
					return err
				}
				if path != nil && *path != "" {
					submissionLocations[*path] = true
				}
				return nil
			})
	}
	if len(submissionLocations) > 0 {
		eachRow(ctx, tx, `SELECT id, bucket, object_key FROM files`, nil,
			func(rows pgx.Rows) error {
				var id, bucket, objectKey string
				if err := rows.Scan(&id, &bucket, &objectKey); err != nil {
					return err
				}
				uri := "minio://" + bucket + "/" + objectKey
				if submissionLocations[uri] || submissionLocations[objectKey] {
					candidateFileIDSet[id] = true
				}
				return nil
			})
	}

	// Agent runs：直接归属 + lineage 匹配。
	agentRunIDs := queryIDs(ctx, tx,
		`SELECT id FROM agent_runs WHERE project_id=$1 AND (episode_id=$2 OR script_id = ANY($3) OR storyboard_id = ANY($4) OR task_id = ANY($5))`,
		projectID, episodeID, toArraySafe(scriptIDs), toArraySafe(storyboardIDs), toArraySafe(taskIDs))
	agentRunSet := toSet(agentRunIDs)
	eachRow(ctx, tx, `SELECT id, input_json FROM agent_runs WHERE project_id=$1`, []any{projectID},
		func(rows pgx.Rows) error {
			var id string
			var input []byte
			if err := rows.Scan(&id, &input); err != nil {
				return err
			}
			if lineageMatchesEpisodeJSON(input, episodeID, scriptIDSet, scriptVersionIDSet, episodeCode) {
				agentRunSet[id] = true
			}
			return nil
		})

	// reading_report / reading_asset_application lineage 保留（资产审计），只计数不删除。
	lineageArtifactIDs := map[string]bool{}
	eachRow(ctx, tx,
		`SELECT id, input_snapshot FROM artifact_revisions WHERE project_id=$1 AND artifact_type='reading_report'`, []any{projectID},
		func(rows pgx.Rows) error {
			var id string
			var input []byte
			if err := rows.Scan(&id, &input); err != nil {
				return err
			}
			if lineageMatchesEpisodeJSON(input, episodeID, scriptIDSet, scriptVersionIDSet, "") {
				lineageArtifactIDs[id] = true
			}
			return nil
		})
	applicationIDs := []string{}
	if len(lineageArtifactIDs) > 0 {
		applicationIDs = queryIDs(ctx, tx,
			`SELECT id FROM artifact_revisions WHERE project_id=$1 AND artifact_type='reading_asset_application' AND artifact_id = ANY($2)`,
			projectID, toMapArray(lineageArtifactIDs))
	}
	for _, id := range applicationIDs {
		lineageArtifactIDs[id] = true
	}

	breakdownIDs := []string{}
	{
		lookup := map[string]bool{}
		rows, err := tx.Query(ctx, `SELECT id, agent_run_id, content_json FROM script_breakdowns WHERE project_id=$1`, projectID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var agentRunID *string
			var content []byte
			if err := rows.Scan(&id, &agentRunID, &content); err != nil {
				rows.Close()
				return nil, err
			}
			if agentRunID != nil && agentRunSet[*agentRunID] {
				lookup[id] = true
			}
			if len(content) > 0 && lineageMatchesEpisodeJSON(content, episodeID, scriptIDSet, scriptVersionIDSet, episodeCode) {
				lookup[id] = true
			}
		}
		rows.Close()
		for id := range lookup {
			breakdownIDs = append(breakdownIDs, id)
		}
	}

	entityVersionIDs := []string{}
	{
		lookup := map[string]bool{}
		rows, err := tx.Query(ctx, `SELECT id, entity_id, content_json FROM entity_versions WHERE project_id=$1 AND (entity_id = ANY($2) OR entity_id = ANY($3) OR entity_id = ANY($4))`,
			projectID, toArraySafe(segmentIDs), toArraySafe(storyboardIDs), toArraySafe([]string{}))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var entityID *string
			var content []byte
			if err := rows.Scan(&id, &entityID, &content); err != nil {
				rows.Close()
				return nil, err
			}
			if entityID != nil && (toSet(segmentIDs)[*entityID] || toSet(storyboardIDs)[*entityID]) {
				lookup[id] = true
			}
			if len(content) > 0 && lineageMatchesEpisodeJSON(content, episodeID, scriptIDSet, scriptVersionIDSet, episodeCode) {
				lookup[id] = true
			}
		}
		rows.Close()
		for id := range lookup {
			entityVersionIDs = append(entityVersionIDs, id)
		}
	}

	trainingSampleIDs := []string{}
	{
		rows, err := tx.Query(ctx, `SELECT id, input_json, confirmed_output_json FROM agent_training_samples WHERE project_id=$1 AND sample_type <> 'reading_report_confirmed'`, projectID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var input, confirmed []byte
			if err := rows.Scan(&id, &input, &confirmed); err != nil {
				rows.Close()
				return nil, err
			}
			if lineageMatchesEpisodeJSON(input, episodeID, scriptIDSet, scriptVersionIDSet, episodeCode) ||
				lineageMatchesEpisodeJSON(confirmed, episodeID, scriptIDSet, scriptVersionIDSet, episodeCode) {
				trainingSampleIDs = append(trainingSampleIDs, id)
			}
		}
		rows.Close()
	}

	workflowIDs := []string{}
	{
		rows, err := tx.Query(ctx, `SELECT id, agent_run_id, input_json FROM workflow_runs WHERE project_id=$1`, projectID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var agentRunID *string
			var input []byte
			if err := rows.Scan(&id, &agentRunID, &input); err != nil {
				rows.Close()
				return nil, err
			}
			if agentRunID != nil && agentRunSet[*agentRunID] {
				workflowIDs = append(workflowIDs, id)
			} else if lineageMatchesEpisodeJSON(input, episodeID, scriptIDSet, scriptVersionIDSet, "") {
				workflowIDs = append(workflowIDs, id)
			}
		}
		rows.Close()
	}

	counts := newDeleteCounts()

	// 资产绑定/关系/清单。
	if len(episodeID) > 0 {
		bindings := queryIDs(ctx, tx, `SELECT id FROM episode_asset_bindings WHERE episode_id=$1`, episodeID)
		deleteMany(ctx, tx, "episode_asset_bindings", bindings, counts, "asset_bindings")
	}
	{
		filters := []string{`episode_id=$1`}
		args := []any{episodeID}
		if len(scriptIDSet) > 0 {
			args = append(args, toArray(scriptIDs))
			filters = append(filters, fmt.Sprintf(`script_id = ANY($%d)`, len(args)))
		}
		if len(storyboardSet) > 0 {
			args = append(args, toArray(storyboardIDs))
			filters = append(filters, fmt.Sprintf(`storyboard_id = ANY($%d)`, len(args)))
		}
		if len(imageIDs) > 0 {
			args = append(args, toArray(imageIDs))
			filters = append(filters, fmt.Sprintf(`image_output_id = ANY($%d)`, len(args)))
		}
		if len(videoIDs) > 0 {
			args = append(args, toArray(videoIDs))
			filters = append(filters, fmt.Sprintf(`video_output_id = ANY($%d)`, len(args)))
		}
		if len(finalIDs) > 0 {
			args = append(args, toArray(finalIDs))
			filters = append(filters, fmt.Sprintf(`final_output_id = ANY($%d)`, len(args)))
		}
		relationCount := deleteWhere(ctx, tx,
			`DELETE FROM asset_relations WHERE project_id=$1 AND (`+strings.Join(filters, " OR ")+`)`,
			append([]any{projectID}, args...))
		counts.set("asset_relations", relationCount)
	}
	if len(storyboardIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE project_assets SET storyboard_id=NULL WHERE storyboard_id = ANY($1)`, toArray(storyboardIDs)); err != nil {
			return nil, err
		}
	}
	{
		filters := []string{`episode_id=$1`}
		args := []any{episodeID}
		if len(scriptIDs) > 0 {
			args = append(args, toArray(scriptIDs))
			filters = append(filters, fmt.Sprintf(`script_id = ANY($%d)`, len(args)))
		}
		if len(segmentIDs) > 0 {
			args = append(args, toArray(segmentIDs))
			filters = append(filters, fmt.Sprintf(`script_segment_id = ANY($%d)`, len(args)))
		}
		if len(storyboardIDs) > 0 {
			args = append(args, toArray(storyboardIDs))
			filters = append(filters, fmt.Sprintf(`storyboard_id = ANY($%d)`, len(args)))
		}
		deleteWhere(ctx, tx,
			`DELETE FROM project_asset_list_items WHERE (`+strings.Join(filters, " OR ")+`)`, args)
	}
	{
		var listIDs []string
		if len(scriptIDs) > 0 {
			listIDs = append(listIDs, queryIDs(ctx, tx,
				`SELECT id FROM project_asset_lists WHERE source_script_id = ANY($1)`, toArray(scriptIDs))...)
		}
		if len(agentRunIDs) > 0 {
			listIDs = append(listIDs, queryIDs(ctx, tx,
				`SELECT id FROM project_asset_lists WHERE source_agent_run_id = ANY($1)`, toArray(agentRunIDs))...)
		}
		listSet := toSet(listIDs)
		if len(listIDs) > 0 {
			if _, err := tx.Exec(ctx,
				`UPDATE project_asset_lists SET source_script_id=NULL, source_agent_run_id=NULL WHERE id = ANY($1)`, toArray(listIDs)); err != nil {
				return nil, err
			}
			empty := queryIDs(ctx, tx,
				`SELECT pl.id FROM project_asset_lists pl LEFT JOIN project_asset_list_items pi ON pi.asset_list_id=pl.id WHERE pl.id = ANY($1) AND pi.id IS NULL`,
				toArray(listIDs))
			deleteMany(ctx, tx, "project_asset_lists", empty, counts, "empty_asset_lists")
			_ = listSet
		}
	}

	// 任务及其附属。
	if len(taskIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE tasks SET depends_on_task_id=NULL WHERE depends_on_task_id = ANY($1)`, toArray(taskIDs)); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM task_dependencies WHERE task_id = ANY($1) OR depends_on_task_id = ANY($1)`, toArray(taskIDs)); err != nil {
			return nil, err
		}
		taskPrompts := queryIDs(ctx, tx, `SELECT id FROM task_prompts WHERE task_id = ANY($1)`, toArray(taskIDs))
		deleteMany(ctx, tx, "task_prompts", taskPrompts, counts, "task_prompts")
		subIDs := queryIDs(ctx, tx, `SELECT id FROM submissions WHERE task_id = ANY($1)`, toArray(taskIDs))
		deleteMany(ctx, tx, "submissions", subIDs, counts, "submissions")
		candidateImports := queryIDs(ctx, tx, `SELECT id FROM task_candidate_imports WHERE task_id = ANY($1)`, toArray(taskIDs))
		deleteMany(ctx, tx, "task_candidate_imports", candidateImports, counts, "task_candidate_imports")
	}
	if len(storyboardIDs) > 0 {
		subIDs := queryIDs(ctx, tx,
			`SELECT id FROM submissions WHERE storyboard_id = ANY($1)`, toArray(storyboardIDs))
		deleteMany(ctx, tx, "submissions", subIDs, counts, "submissions")
		// batch 清理：不再被任何 submission 引用的批次行。
		batchIDs := queryIDs(ctx, tx,
			`SELECT DISTINCT sb.id FROM task_submission_batches sb LEFT JOIN submissions s ON s.batch_id=sb.id
			 WHERE sb.id IN (SELECT DISTINCT batch_id FROM submissions WHERE storyboard_id = ANY($1)) AND s.id IS NULL`,
			toArray(storyboardIDs))
		deleteMany(ctx, tx, "task_submission_batches", batchIDs, counts, "task_submission_batches")
	}
	deleteMany(ctx, tx, "image_outputs", imageIDs, counts, "image_outputs")
	deleteMany(ctx, tx, "video_outputs", videoIDs, counts, "video_outputs")
	deleteMany(ctx, tx, "final_outputs", finalIDs, counts, "final_outputs")
	if len(taskIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE asset_versions SET source_task_id=NULL WHERE source_task_id = ANY($1)`, toArray(taskIDs)); err != nil {
			return nil, err
		}
	}
	if len(agentRunIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE asset_versions SET source_agent_run_id=NULL WHERE source_agent_run_id = ANY($1)`, toArray(agentRunIDs)); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE storyboard_versions SET source_agent_run_id=NULL WHERE source_agent_run_id = ANY($1) AND NOT (storyboard_id = ANY($2))`,
			toArray(agentRunIDs), toArraySafe(storyboardIDs)); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE script_versions SET source_agent_run_id=NULL WHERE source_agent_run_id = ANY($1) AND NOT (id = ANY($2))`,
			toArray(agentRunIDs), toArraySafe(scriptVersionIDs)); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE entity_versions SET agent_run_id=NULL WHERE agent_run_id = ANY($1) AND NOT (id = ANY($2))`,
			toArray(agentRunIDs), toArraySafe(entityVersionIDs)); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE artifact_revisions SET source_run_id=NULL WHERE source_run_id = ANY($1)`, toArray(agentRunIDs)); err != nil {
			return nil, err
		}
	}
	{
		feedbackIDs := []string{}
		if len(agentRunIDs) > 0 {
			feedbackIDs = append(feedbackIDs, queryIDs(ctx, tx,
				`SELECT id FROM agent_feedback WHERE run_id = ANY($1)`, toArray(agentRunIDs))...)
		}
		if len(taskIDs) > 0 {
			feedbackIDs = append(feedbackIDs, queryIDs(ctx, tx,
				`SELECT id FROM agent_feedback WHERE task_id = ANY($1)`, toArray(taskIDs))...)
		}
		deleteMany(ctx, tx, "agent_feedback", feedbackIDs, counts, "agent_feedback")
	}
	if len(agentRunIDs) > 0 {
		issues := queryIDs(ctx, tx, `SELECT id FROM agent_issues WHERE run_id = ANY($1)`, toArray(agentRunIDs))
		deleteMany(ctx, tx, "agent_issues", issues, counts, "agent_issues")
	}
	if len(storyboardIDs) > 0 {
		versions := queryIDs(ctx, tx,
			`SELECT id FROM storyboard_versions WHERE storyboard_id = ANY($1)`, toArray(storyboardIDs))
		deleteMany(ctx, tx, "storyboard_versions", versions, counts, "storyboard_versions")
	}
	if len(agentRunIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE agent_runs SET parent_run_id=NULL WHERE parent_run_id = ANY($1)`, toArray(agentRunIDs)); err != nil {
			return nil, err
		}
	}
	deleteMany(ctx, tx, "workflow_runs", workflowIDs, counts, "workflow_runs")
	deleteMany(ctx, tx, "script_breakdowns", breakdownIDs, counts, "breakdown_revisions")
	if len(entityVersionIDs) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE entity_versions SET base_version_id=NULL WHERE base_version_id = ANY($1)`, toArray(entityVersionIDs)); err != nil {
			return nil, err
		}
	}
	deleteMany(ctx, tx, "entity_versions", entityVersionIDs, counts, "entity_versions")
	deleteMany(ctx, tx, "agent_training_samples", trainingSampleIDs, counts, "training_samples")
	counts.set("reading_revisions_retained_for_asset_audit", len(lineageArtifactIDs))
	if len(candidateFileIDSet) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE files SET task_id=NULL, episode_id=NULL WHERE id = ANY($1)`, stringSetArray(candidateFileIDSet)); err != nil {
			return nil, err
		}
	}
	deleteMany(ctx, tx, "script_versions", scriptVersionIDs, counts, "script_versions")
	deleteMany(ctx, tx, "agent_runs", agentRunIDs, counts, "agent_runs")
	deleteMany(ctx, tx, "tasks", taskIDs, counts, "tasks")
	deleteMany(ctx, tx, "storyboards", storyboardIDs, counts, "storyboards")
	deleteMany(ctx, tx, "script_segments", segmentIDs, counts, "script_segments")
	deleteMany(ctx, tx, "scripts", scriptIDs, counts, "scripts")
	if len(episodeID) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM project_episodes WHERE id=$1`, episodeID); err != nil {
			return nil, err
		}
	}
	counts.set("episodes", 1)

	// 重建 project.script_text（对齐 legacy：按 episode_no 排序拼接剩余剧本）。
	if err := rebuildProjectScriptText(ctx, tx, projectID); err != nil {
		return nil, err
	}

	// file_cleanup：计算仍被引用（共享保留）与可清理候选。
	// P3f：delete_files=true 时只收集候选，MinIO 删除与本步骤之后单独执行
	// （对齐 legacy cleanup_deleted_episode_files：先提交级联删除，再删除对象/文件行）。
	var cleanupObjects, sharedObjects = []map[string]any{}, []map[string]any{}
	cleanupStatus := "not_requested"
	if deleteFiles && len(candidateFileIDSet) > 0 {
		var err error
		cleanupObjects, sharedObjects, err = r.cleanupCandidateFiles(ctx, tx, candidateFileIDSet)
		if err != nil {
			return nil, err
		}
		cleanupStatus = "pending"
	}

	detail, _ := json.Marshal(map[string]any{
		"episode_code":   episodeCode,
		"deleted_counts": counts.list(),
		"delete_files":   deleteFiles,
		"file_cleanup": map[string]any{
			"status":  cleanupStatus,
			"objects": cleanupObjects,
		},
	})
	opLogID, err := r.insertOperationLogTx(ctx, tx, userID, projectID, "project_episode", episodeID, "project_episode_hard_deleted", detail)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit episode delete: %w", err)
	}
	result := &EpisodeDeleteResult{
		ProjectID:      projectID,
		EpisodeID:      episodeID,
		EpisodeCode:    episodeCode,
		DeletedCounts:  counts.list(),
		CleanupObjects: cleanupObjects,
		SharedObjects:  sharedObjects,
		DeleteFiles:    deleteFiles,
		OpLogID:        opLogID,
	}
	return result, nil
}

// ContextReuse 是为兼容 outputByte 签名保留的哨兵 helper。
type contextKey int

const contextReuseKey contextKey = 1

// ContextReuse 返回 ctx 存根：调用方用 r.pool.Query（见 outputByte）时不需要携带自定义值。
func ContextReuse(ctx context.Context) context.Context { return ctx }

// rebuildProjectScriptText 对齐 legacy 删除后 project.script_text 重建。
func rebuildProjectScriptText(ctx context.Context, tx pgx.Tx, projectID string) error {
	type piece struct {
		no      int32
		code    string
		title   *string
		content string
	}
	var pieces []piece
	rows, err := tx.Query(ctx,
		`SELECT pe.episode_no, s.script_code, s.title, s.content
		 FROM scripts s JOIN project_episodes pe ON pe.id = s.episode_id
		 WHERE s.project_id=$1 ORDER BY pe.episode_no`, projectID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p piece
		if err := rows.Scan(&p.no, &p.code, &p.title, &p.content); err != nil {
			rows.Close()
			return err
		}
		pieces = append(pieces, p)
	}
	rows.Close()
	var blocks []string
	for _, p := range pieces {
		snippet := strings.TrimSpace(p.content)
		if snippet == "" {
			continue
		}
		title := ""
		if p.title != nil {
			title = *p.title
		}
		blocks = append(blocks, strings.TrimSpace(fmt.Sprintf("--- %s / %s ---\n%s", p.code, title, snippet)))
	}
	scriptText := strings.Join(blocks, "\n\n")
	var titleVal *string
	if scriptText != "" {
		titleVal = &scriptText
	}
	_, err = tx.Exec(ctx, `UPDATE projects SET script_text=$2, updated_at=now() WHERE id=$1`, projectID, titleVal)
	return err
}

// cleanupCandidateFiles 对齐 legacy cleanup_deleted_episode_files 的引用计数部分。
// 返回可直接清理的对象（clean）与仍被项目级或其他分集数据引用的共享对象（shared，带 reason）。
func (r *Projects) cleanupCandidateFiles(ctx context.Context, tx pgx.Tx, candidateIDs map[string]bool) ([]map[string]any, []map[string]any, error) {
	var clean, shared []map[string]any
	if len(candidateIDs) == 0 {
		return clean, shared, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT id, bucket, object_key, file_name FROM files WHERE id = ANY($1)`, stringSetArray(candidateIDs))
	if err != nil {
		return nil, nil, err
	}
	type fileRow struct {
		id, bucket, objectKey, fileName string
	}
	var fileRows []fileRow
	for rows.Next() {
		var f fileRow
		if err := rows.Scan(&f.id, &f.bucket, &f.objectKey, &f.fileName); err != nil {
			rows.Close()
			return nil, nil, err
		}
		fileRows = append(fileRows, f)
	}
	rows.Close()
	for _, f := range fileRows {
		var refs int32
		for _, probe := range []string{
			`SELECT COUNT(*) FROM script_versions WHERE source_file_id=$1`,
			`SELECT COUNT(*) FROM asset_versions WHERE file_id=$1 OR preview_file_id=$1`,
			`SELECT COUNT(*) FROM image_outputs WHERE file_id=$1`,
			`SELECT COUNT(*) FROM video_outputs WHERE file_id=$1`,
			`SELECT COUNT(*) FROM final_outputs WHERE file_id=$1`,
		} {
			if err := tx.QueryRow(ctx, probe, f.id).Scan(&refs); err != nil {
				continue
			}
			if refs > 0 {
				break
			}
		}
		item := map[string]any{
			"file_id":    f.id,
			"bucket":     f.bucket,
			"object_key": f.objectKey,
			"file_name":  f.fileName,
		}
		if refs > 0 {
			item["reason"] = "仍被项目级或其他分集数据引用"
			shared = append(shared, item)
			continue
		}
		clean = append(clean, item)
	}
	return clean, shared, nil
}

// FinalizeEpisodeDeleteFileCleanupTx 分集删除的文件清理终局（对齐 legacy cleanup_deleted_episode_files
// 的对象删除之后半段）：删除成功清理的文件行，并把操作日志 detail.file_cleanup 回填最终结果。
// 必须在级联删除事务提交后调用（此时文件引用计数已反映删除后的库态）。
func (r *Projects) FinalizeEpisodeDeleteFileCleanupTx(ctx context.Context, opLogID string, succeeded, failed, shared []map[string]any) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin episode file cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	succeededIDs := make([]string, 0, len(succeeded))
	if len(succeeded) > 0 {
		for _, item := range succeeded {
			if id := fmt.Sprintf("%v", item["file_id"]); id != "" && id != "<nil>" {
				succeededIDs = append(succeededIDs, id)
			}
		}
	}
	if len(succeededIDs) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM files WHERE id = ANY($1)`, toArray(succeededIDs)); err != nil {
			return fmt.Errorf("delete cleaned files: %w", err)
		}
	}
	result, _ := json.Marshal(map[string]any{
		"succeeded":       len(succeeded),
		"failed":          len(failed),
		"shared_retained": len(shared),
		"pending_retry":   len(failed),
		"failed_objects":  failed,
		"shared_objects":  shared,
	})
	if _, err := tx.Exec(ctx,
		`UPDATE operation_logs SET detail = jsonb_set(detail, '{file_cleanup}', $1::jsonb, true), updated_at = now() WHERE id = $2`,
		string(result), opLogID); err != nil {
		return fmt.Errorf("update op log file_cleanup: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit episode file cleanup: %w", err)
	}
	return nil
}

// ---- 通用集合/删除辅助 ----

// queryIDs 返回查询结果的第一列（uuid）。
func queryIDs(ctx context.Context, tx pgx.Tx, query string, args ...any) []string {
	var ids []string
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// eachRow 对查询结果的每一行执行 fn（fn 内 Scan）。
func eachRow(ctx context.Context, tx pgx.Tx, query string, args []any, fn func(rows pgx.Rows) error) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func toSet(ids []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			out[id] = true
		}
	}
	return out
}

// toArray 转 pgx 数组参数（空数组时返回 nil，避免 ANY() 空 pgtype 报错）。
func toArray(ids []string) any {
	if len(ids) == 0 {
		return nil
	}
	vals := make([]string, 0, len(ids))
	for _, id := range ids {
		vals = append(vals, id)
	}
	return vals
}

// toArraySafe 同 toArray，但空时返回空串切片（用于无需过滤的分支）。
func toArraySafe(ids []string) []string {
	if len(ids) == 0 {
		return []string{}
	}
	return ids
}

func stringSetArray(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	return out
}

// toMapArray 返回 map 键切片。
func toMapArray(m map[string]bool) []string {
	return stringSetArray(m)
}

// deleteMany 按 label 记录删除行数。
func deleteMany(ctx context.Context, tx pgx.Tx, table string, ids []string, counts *deleteCounts, label string) {
	if len(ids) == 0 {
		counts.set(label, 0)
		return
	}
	quoted := table
	if !isSafeTableName(table) {
		counts.set(label, 0)
		return
	}
	tag, err := tx.Exec(ctx, `DELETE FROM `+quoted+` WHERE id = ANY($1)`, toArray(ids))
	if err != nil {
		counts.set(label, 0)
		return
	}
	counts.set(label, int(tag.RowsAffected()))
}

func isSafeTableName(name string) bool {
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return name != ""
}

func deleteWhere(ctx context.Context, tx pgx.Tx, query string, args ...any) int {
	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0
	}
	return int(tag.RowsAffected())
}

// appendTaskMembership 合并任务过滤条件（episode_id / script_id / segment_id / storyboard_id）。
func appendTaskMembership(ctx context.Context, tx pgx.Tx, taskIDs []string, filters taskFilenames) []string {
	out := toSet(taskIDs)
	if len(filters.scriptIDs) > 0 {
		for _, id := range queryIDs(ctx, tx,
			`SELECT id FROM tasks WHERE project_id=$1 AND script_id = ANY($2)`, filters.projectID, toArray(filters.scriptIDs)) {
			out[id] = true
		}
	}
	if len(filters.segmentIDs) > 0 {
		for _, id := range queryIDs(ctx, tx,
			`SELECT id FROM tasks WHERE project_id=$1 AND script_segment_id = ANY($2)`, filters.projectID, toArray(filters.segmentIDs)) {
			out[id] = true
		}
	}
	if len(filters.storyboardIDs) > 0 {
		for _, id := range queryIDs(ctx, tx,
			`SELECT id FROM tasks WHERE project_id=$1 AND storyboard_id = ANY($2)`, filters.projectID, toArray(filters.storyboardIDs)) {
			out[id] = true
		}
	}
	result := make([]string, 0, len(out))
	for id := range out {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

type taskFilenames struct {
	projectID     string
	scriptIDs     []string
	segmentIDs    []string
	storyboardIDs []string
}

// outputIDs 返回某 output 表命中行的 id（按各归属列 OR 过滤）。
func outputIDs(ctx context.Context, tx pgx.Tx, table, projectID string, episodeNo int32, scriptIDs, storyboardIDs, taskIDs []string, episodeID string, _ any) []string {
	var conditions []string
	var args []any
	if episodeID != "" {
		args = append(args, episodeID)
		conditions = append(conditions, fmt.Sprintf("episode_id = $%d", len(args)))
	}
	// final_outputs 无 script_id/storyboard_id 列（对齐 legacy：仅 episode_id/task_id 过滤）。
	if table != "final_outputs" {
		if len(scriptIDs) > 0 {
			args = append(args, toArray(scriptIDs))
			conditions = append(conditions, fmt.Sprintf("script_id = ANY($%d)", len(args)))
		}
		if len(storyboardIDs) > 0 {
			args = append(args, toArray(storyboardIDs))
			conditions = append(conditions, fmt.Sprintf("storyboard_id = ANY($%d)", len(args)))
		}
	}
	if len(taskIDs) > 0 {
		args = append(args, toArray(taskIDs))
		conditions = append(conditions, fmt.Sprintf("task_id = ANY($%d)", len(args)))
	}
	if len(conditions) == 0 {
		return nil
	}
	return queryIDs(ctx, tx,
		fmt.Sprintf(`SELECT id FROM %s WHERE project_id=$%d AND (%s)`, table, len(args)+1, strings.Join(conditions, " OR ")),
		append([]any{projectID}, args...)...)
}

// outputByte 返回某 output 表某文件列命中的 file_id（对齐 candidate_file_ids 收集）。
func outputByte(ctx context.Context, tx pgx.Tx, table, projectID string, episodeNo int32, scriptIDs, storyboardIDs, taskIDs []string, episodeID string, fileCol string) []string {
	var conditions []string
	var args []any
	if episodeID != "" {
		args = append(args, episodeID)
		conditions = append(conditions, fmt.Sprintf("episode_id = $%d", len(args)))
	}
	if table != "final_outputs" {
		if len(scriptIDs) > 0 {
			args = append(args, toArray(scriptIDs))
			conditions = append(conditions, fmt.Sprintf("script_id = ANY($%d)", len(args)))
		}
		if len(storyboardIDs) > 0 {
			args = append(args, toArray(storyboardIDs))
			conditions = append(conditions, fmt.Sprintf("storyboard_id = ANY($%d)", len(args)))
		}
	}
	if len(taskIDs) > 0 {
		args = append(args, toArray(taskIDs))
		conditions = append(conditions, fmt.Sprintf("task_id = ANY($%d)", len(args)))
	}
	var ids []string
	if len(conditions) == 0 {
		return ids
	}
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE project_id=$%d AND (%s)`, fileCol, table, len(args)+1, strings.Join(conditions, " OR "))
	rows, err := tx.Query(ctx, query, append([]any{projectID}, args...)...)
	if err != nil {
		return ids
	}
	defer rows.Close()
	for rows.Next() {
		var id *string
		if err := rows.Scan(&id); err == nil && id != nil {
			ids = append(ids, *id)
		}
	}
	return ids
}

// lineageMatchesEpisodeJSON 对齐 legacy _lineage_matches_episode。
func lineageMatchesEpisodeJSON(payload []byte, episodeID string, scriptIDs, scriptVersionIDs map[string]bool, episodeCode string) bool {
	if len(payload) == 0 || string(payload) == "null" || string(payload) == "{}" {
		return false
	}
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return false
	}
	return lineageMatchesEpisode(v, episodeID, scriptIDs, scriptVersionIDs, episodeCode)
}

func lineageMatchesEpisode(v any, episodeID string, scriptIDs, scriptVersionIDs map[string]bool, episodeCode string) bool {
	switch typed := v.(type) {
	case []any:
		for _, item := range typed {
			if lineageMatchesEpisode(item, episodeID, scriptIDs, scriptVersionIDs, episodeCode) {
				return true
			}
		}
		return false
	case []map[string]any:
		for _, item := range typed {
			if lineageMatchesEpisode(item, episodeID, scriptIDs, scriptVersionIDs, episodeCode) {
				return true
			}
		}
		return false
	case map[string]any:
		for key, value := range typed {
			switch key {
			case "episode_id":
				if fmt.Sprintf("%v", value) == episodeID {
					return true
				}
			case "script_id":
				if scriptIDs[fmt.Sprintf("%v", value)] {
					return true
				}
			case "script_version_id":
				if scriptVersionIDs[fmt.Sprintf("%v", value)] {
					return true
				}
			case "episode_code":
				if episodeCode != "" && strings.EqualFold(fmt.Sprintf("%v", value), episodeCode) {
					return true
				}
			}
			if lineageMatchesEpisode(value, episodeID, scriptIDs, scriptVersionIDs, episodeCode) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
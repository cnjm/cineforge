package service

// P4d run-events 快照形状契约测试（service 层直查，不需要 Redis）：
// 验证 agent_runs/workflows 输出对齐 legacy AgentRunSummaryRead / WorkflowRunSummaryRead，
// 尤其是 lineage（episode/episode_code/script_version_id）与 can_retry/can_materialize/materialized 推导。

import (
	"context"
	"strings"
	"testing"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/repository"
	"cineforge/server/internal/testutil"
)

// seedSnapshotData 建项目 + 多样 agent_run/workflow_run 行。
func seedSnapshotData(t *testing.T) (projectID string, cleanup func()) {
	t.Helper()
	projectID, err := auth.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool := testutil.Pool(t)
	unique := strings.ToUpper(projectID[:4])
	if _, err := pool.Exec(ctx,
		`INSERT INTO projects (id, project_no, project_prefix, name, title, genre, status, current_stage, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,'测试','draft','draft',now(),now())`,
		projectID, "RF-SNAP-"+unique, "S"+unique, "快照契约", "快照契约"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	insertRun := func(agentType, status, summary string) string {
		id, err := auth.NewUUID()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO agent_runs (id, project_id, agent_type, status, input_json, summary_json, version_no, data_state, created_at, updated_at)
			 VALUES ($1,$2,$3,$4,'{}'::jsonb,$5::jsonb,1,'agent_raw',now(),now())`,
			id, projectID, agentType, status, nullableJSON(summary)); err != nil {
			t.Fatalf("seed agent run: %v", err)
		}
		return id
	}
	insertWorkflow := func(workflowType, status string, agentRunID *string) {
		id, err := auth.NewUUID()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO workflow_runs (id, project_id, agent_run_id, workflow_type, status, node_status_json, result_json, error_json, input_json, created_at, updated_at)
			 VALUES ($1,$2,$3,$4,$5,'{}'::jsonb,'{}'::jsonb,'{}'::jsonb,'{}'::jsonb,now(),now())`,
			id, projectID, agentRunID, workflowType, status); err != nil {
			t.Fatalf("seed workflow run: %v", err)
		}
	}

	// agent run A：summary 带 lineage（模拟 agent worker 写入）
	agentID := insertRun("script_reading", "succeeded",
		`{"skill":"script-reading","status":"ok","episode_id":"`+projectID[:6]+`-ep1","episode_code":"EP01","script_version_id":"`+projectID[:6]+`-v1","script_version_no":1}`)
	// agent run B：summary 无 lineage、workflow_run_id 在 summary（回退路径）
	insertRun("script_segmentation", "succeeded",
		`{"workflow_run_id":"`+projectID[:6]+`-wf1","script_segments_count":3}`)
	// workflow run：materialized（materialized 置位 + can_retry false）
	insertWorkflow("script_reading", "materialized", &agentID)

	cleanup = func() {
		pool := testutil.Pool(t)
		_, _ = pool.Exec(context.Background(), `DELETE FROM agent_runs WHERE project_id = $1`, projectID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM workflow_runs WHERE project_id = $1`, projectID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM projects WHERE id = $1`, projectID)
	}
	return projectID, cleanup
}

func nullableJSON(raw string) *string {
	// 空 summary → NULL；否则原文（测试里直接给合法 JSON 字符串）
	if raw == "" {
		return nil
	}
	s := raw
	return &s
}

func TestRunEventSnapshotShape(t *testing.T) {
	projectID, cleanup := seedSnapshotData(t)
	defer cleanup()

	pool := testutil.Pool(t)
	projects := repository.NewProjects(pool)
	s := NewProjectService(pool, projects, repository.NewUsers(pool), nil, nil, nil)

	snapshot, err := s.RunEventSnapshot(context.Background(), "unit-director", "director", projectID)
	if err != nil {
		t.Fatalf("RunEventSnapshot: %v", err)
	}
	if snapshot.ProjectID != projectID {
		t.Fatalf("project_id=%v", snapshot.ProjectID)
	}
	if snapshot.EmittedAt == "" {
		t.Fatal("emitted_at 为空")
	}
	if len(snapshot.AgentRuns) < 2 || len(snapshot.Workflows) < 1 {
		t.Fatalf("agent_runs=%d workflows=%d", len(snapshot.AgentRuns), len(snapshot.Workflows))
	}

	// agent run A：lineage 映射到顶层
	// 找标记 episode_code 的 run
	var lineageRun *AgentRunSummaryItem
	for i := range snapshot.AgentRuns {
		if v := snapshot.AgentRuns[i].EpisodeCode; v != nil && *v == "EP01" {
			lineageRun = &snapshot.AgentRuns[i]
			break
		}
	}
	if lineageRun == nil {
		t.Fatalf("未找到 lineage run（EpisodeCode=EP01）: %+v", snapshot.AgentRuns)
	}
	if lineageRun.EpisodeID == nil || lineageRun.ScriptVersionID == nil {
		t.Fatalf("lineage 字段缺失: %+v", lineageRun)
	}
	if lineageRun.AgentType != "script_reading" || lineageRun.Status != "succeeded" {
		t.Fatalf("agent_type/status=%s/%s", lineageRun.AgentType, lineageRun.Status)
	}
	if len(lineageRun.Summary) == 0 {
		t.Fatal("summary 缺失")
	}

	// agent run B：workflow_run_id 从 summary 回退
	foundFallback := false
	for i := range snapshot.AgentRuns {
		if v := snapshot.AgentRuns[i].WorkflowRunID; v != nil && *v == projectID[:6]+"-wf1" {
			foundFallback = true
		}
	}
	if !foundFallback {
		t.Fatalf("workflow_run_id summary 回退缺失: %+v", snapshot.AgentRuns)
	}

	// workflow：materialized 推导
	if len(snapshot.Workflows) == 0 || !snapshot.Workflows[0].Materialized {
		t.Fatalf("workflows[0] 应为 materialized: %+v", snapshot.Workflows)
	}
	wf := snapshot.Workflows[0]
	if wf.WorkflowType != "script_reading" || wf.CanRetry || wf.CanMaterialize || wf.CanRerun {
		t.Fatalf("workflow flags 错误: %+v", wf)
	}

	// 幽灵项目 → 404（service 层 StatusError）
	_, err = s.RunEventSnapshot(context.Background(), "unit-director", "director", "00000000-0000-0000-0000-000000000000")
	se, ok := err.(*StatusError)
	if !ok || se.Status != 404 || se.Detail != "Project not found" {
		t.Fatalf("ghost project err=%v", err)
	}
}
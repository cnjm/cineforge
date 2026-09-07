package service

// P3e task 域业务服务（对齐 legacy app/api/routes/tasks.py + projects.py 的分组路由 +
// workbench_rpc 的提交/审核路径）。职责边界：
//   - 事务所有权（pgx.Tx begin/commit/rollback；仓储变更函数不 commit）
//   - repo 类型化错误 → StatusError 逐字映射（KeyError→404 / PermissionError→403 / ValueError→400）
//   - 确定性任务创建/审核状态/版本号仍属后端（不交给模型）
//
// 注：generate_asset_tasks / generate_storyboard_tasks（流程1/流程2）体量超出本文件范围，
// 含 _create_audio_asset_tasks / _asset_task_context_specs / refresh_pending_asset_task_prompts
// 等整段确定性落库链路，单独成文件 tasks_grouping svc 层处理。

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/agentclient"
	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
)

// 任务状态明文（repository 常量未导出；保持单字面量对齐）。
const (
	taskStatusSubmitted = "submitted"
	taskStatusReviewing = "reviewing"
	taskStatusCompleted = "completed"
)

// operation_logs.action（repository opLogTask* 常量镜像）。
const (
	opLogActionTaskUpdated     = "task_updated"
	opLogActionTaskPromptSaved = "task_prompt_saved"
)

// TaskService 任务域服务。
type TaskService struct {
	pool     *pgxpool.Pool
	tasks    *repository.Tasks
	projects *repository.Projects
	agents   *agentclient.Client
}

// NewTaskService 创建任务域服务。agents 可空：agent 子模块未接入时 prompt-jobs
// 照常落库但跳过 enqueue（P4c 后始终注入）。
func NewTaskService(pool *pgxpool.Pool, tasks *repository.Tasks, projects *repository.Projects, agents *agentclient.Client) *TaskService {
	return &TaskService{pool: pool, tasks: tasks, projects: projects, agents: agents}
}

// taskActor 组装仓储调用方身份。
func taskActor(userID, role string) repository.TaskActor {
	return repository.TaskActor{ID: userID, Role: role}
}

// ---- 事务 / 错误映射 ----

// withTx 拥有事务生命周期：fn 内任何错误都 rollback，成功提交。
func (s *TaskService) withTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// mapTaskError 把仓库类型化错误映射为 StatusError（detail 逐字对齐 legacy 路由）。
func mapTaskError(err error, notFoundDetail, forbiddenDetail string) error {
	if err == nil {
		return nil
	}
	if repository.IsNotFoundError(err) {
		return notFound404(notFoundDetail)
	}
	if repository.IsForbiddenError(err) {
		return forbidden403(forbiddenDetail)
	}
	if detail, ok := repository.BadRequestDetail(err); ok {
		return badRequest400(detail)
	}
	return err
}

// mapProjectTaskError project 域路由的 404/403 文案（"Project not found / denied"）。
func mapProjectTaskError(err error) error {
	return mapTaskError(err, "Project not found", "Project permission denied")
}

// ---- 任务读 ----

// ListTasks 对齐 list_tasks（任务工作台列表，无项目过滤时的全量可见任务）。
func (s *TaskService) ListTasks(ctx context.Context, userID, role string,
	projectID *string, status *string, assigneeID *string) ([]repository.TaskView, error) {
	return s.tasks.ListTasks(ctx, s.pool, taskActor(userID, role), projectID, status, assigneeID, time.Now().UTC())
}

// GetTask 对齐 get_task。
func (s *TaskService) GetTask(ctx context.Context, userID, role, taskID string) (repository.TaskView, error) {
	view, err := s.tasks.GetTask(ctx, s.pool, taskID, taskActor(userID, role))
	if err != nil {
		return repository.TaskView{}, mapTaskError(err, "Task not found", "Task permission denied")
	}
	return view, nil
}

// ---- 任务写 ----

// UpdateTask 对齐 update_task：事务内更新 + task_updated 审计 + 提交后重读 TaskRead。
func (s *TaskService) UpdateTask(ctx context.Context, userID, role, taskID string,
	in repository.TaskUpdateInput) (repository.TaskView, error) {
	actor := taskActor(userID, role)
	now := time.Now().UTC()
	var updated *model.Tasks
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		updated, err = s.tasks.UpdateTask(ctx, tx, taskID, in, actor, now)
		if err != nil {
			return err
		}
		return s.tasks.InsertOperationLog(ctx, tx, userID, &updated.ProjectId, &updated.ID,
			"task", opLogActionTaskUpdated, map[string]any{
				"status":              updated.Status,
				"assignee_id":         updated.AssigneeId,
				"reassignment_reason": in.ReassignmentReason,
			})
	})
	if err != nil {
		return repository.TaskView{}, mapTaskError(err, "Task not found", "Task permission denied")
	}
	return s.GetTask(ctx, userID, role, taskID)
}

// ---- 提示词 ----

// ListTaskPrompts 对齐 list_task_prompts（task 存在性 404）。
func (s *TaskService) ListTaskPrompts(ctx context.Context, userID, role, taskID string) ([]repository.PromptView, error) {
	if _, err := s.GetTask(ctx, userID, role, taskID); err != nil {
		return nil, err
	}
	return s.tasks.ListTaskPrompts(ctx, s.pool, taskID)
}

// assertTaskPromptEditable 对齐 _assert_task_prompt_editable。
func assertTaskPromptEditable(task repository.TaskView) error {
	if task.Locked {
		labels := strings.Join(task.DependencyAssetCodes, "、")
		if labels == "" {
			labels = "前置任务"
		}
		return badRequest400(fmt.Sprintf("%s 尚未完成，当前任务暂未解锁。", labels))
	}
	if task.Status == taskStatusReviewing || task.Status == taskStatusSubmitted {
		return badRequest400("当前任务正在审核，不能保存或重新生成提示词。")
	}
	if task.Status == taskStatusCompleted {
		return badRequest400("已完成任务不能保存或重新生成提示词；如需换版，请先由导演发起返工。")
	}
	return nil
}

// AddTaskPrompt 对齐 create_task_prompt（路由层前置校验 + 201 返回 prompt 视图）。
func (s *TaskService) AddTaskPrompt(ctx context.Context, userID, role, taskID string,
	in repository.TaskPromptCreate) (repository.PromptView, error) {
	actor := taskActor(userID, role)
	taskView, err := s.tasks.EnsureTaskUpdateAccess(ctx, s.pool, taskID, actor)
	if err != nil {
		return repository.PromptView{}, mapTaskError(err, "Task not found", "Task permission denied")
	}
	if err := assertTaskPromptEditable(taskView); err != nil {
		return repository.PromptView{}, err
	}
	if taskView.VariantKind != nil && *taskView.VariantKind == "human_temporary" {
		return repository.PromptView{}, badRequest400("人工临时生产任务已跳过提示词流程，不能保存任务提示词。")
	}
	if in.CreatedBy == nil {
		in.CreatedBy = &userID
	}
	var item *model.TaskPrompts
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		item, err = s.tasks.AddTaskPrompt(ctx, tx, taskID, in)
		if err != nil {
			return err
		}
		return s.tasks.InsertOperationLog(ctx, tx, userID, &taskView.ProjectId, &item.ID,
			"task_prompt", opLogActionTaskPromptSaved, map[string]any{
				"task_id": taskID, "prompt_type": item.PromptType, "source": item.Source,
			})
	})
	if err != nil {
		return repository.PromptView{}, mapTaskError(err, "Task not found", "Task permission denied")
	}
	return repository.PromptToView(item), nil
}

// ---- 候选登记 / 提交 / 审核 ----

// ListTaskSubmissions 对齐 list_submissions（复用 get_task 的 submissions + 404/403）。
func (s *TaskService) ListTaskSubmissions(ctx context.Context, userID, role, taskID string) ([]repository.SubmissionView, error) {
	task, err := s.GetTask(ctx, userID, role, taskID)
	if err != nil {
		return nil, err
	}
	if task.Submissions == nil {
		return []repository.SubmissionView{}, nil
	}
	return task.Submissions, nil
}

// AddSubmission 对齐 create_submission。
func (s *TaskService) AddSubmission(ctx context.Context, userID, role, taskID string,
	in repository.SubmissionCreate) (repository.SubmissionView, error) {
	actor := taskActor(userID, role)
	if in.SubmittedById == nil {
		in.SubmittedById = &userID
	}
	var view *repository.SubmissionView
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		view, err = s.tasks.AddSubmission(ctx, tx, taskID, in, actor.ID, actor.Role)
		return err
	})
	if err != nil {
		return repository.SubmissionView{}, mapTaskError(err, "Task not found", "Task permission denied")
	}
	return *view, nil
}

// UpdateSubmissionMetadata 对齐 update_submission_metadata。
func (s *TaskService) UpdateSubmissionMetadata(ctx context.Context, userID, role, taskID, submissionID string,
	in repository.SubmissionMetadataUpdate) (repository.SubmissionView, error) {
	var view *repository.SubmissionView
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		view, err = s.tasks.UpdateSubmissionMetadata(ctx, tx, taskID, submissionID, in, userID, role)
		return err
	})
	if err != nil {
		return repository.SubmissionView{}, mapTaskError(err, "Task or submission not found", "Task permission denied")
	}
	return *view, nil
}

// DeleteDraftSubmission 对齐 delete_draft_submission。
func (s *TaskService) DeleteDraftSubmission(ctx context.Context, userID, role, taskID, submissionID string) (map[string]string, error) {
	var out map[string]string
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.tasks.DeleteDraftSubmission(ctx, tx, taskID, submissionID, userID, role)
		return err
	})
	if err != nil {
		return nil, mapTaskError(err, "Task or submission not found", "Task permission denied")
	}
	return out, nil
}

// SubmitTaskBatch 对齐 submit_task_batch（单任务提交 → SubmissionBatchRead）。
func (s *TaskService) SubmitTaskBatch(ctx context.Context, userID, role, taskID string,
	in repository.TaskSubmitInput) (repository.BatchView, error) {
	var batch *model.TaskSubmissionBatches
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		batch, err = s.tasks.SubmitTaskBatch(ctx, tx, taskID, in, userID, role)
		return err
	})
	if err != nil {
		return repository.BatchView{}, mapTaskError(err, "Task not found", "Task permission denied")
	}
	return repository.BatchToView(batch), nil
}

// ReviewTaskBatch 对齐 review_task_batch（审核后重读 TaskRead）。
func (s *TaskService) ReviewTaskBatch(ctx context.Context, userID, role, taskID, batchID string,
	in repository.TaskReviewInput) (repository.TaskView, error) {
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		_, err := s.tasks.ReviewTaskBatch(ctx, tx, taskID, batchID, in, userID, role)
		return err
	})
	if err != nil {
		return repository.TaskView{}, mapTaskError(err, "Task or submission batch not found", "Director review permission required")
	}
	return s.GetTask(ctx, userID, role, taskID)
}

// BulkSubmitTaskBatches 对齐 bulk_submit_task_batches。
func (s *TaskService) BulkSubmitTaskBatches(ctx context.Context, userID, role, requestID string,
	entries []repository.TaskBulkSubmitEntry) ([]repository.BatchView, error) {
	var batches []*model.TaskSubmissionBatches
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		batches, err = s.tasks.BulkSubmitTaskBatches(ctx, tx, requestID, entries, userID, role)
		return err
	})
	if err != nil {
		return nil, mapTaskError(err, "Task not found", "Task permission denied")
	}
	out := make([]repository.BatchView, 0, len(batches))
	for _, b := range batches {
		out = append(out, repository.BatchToView(b))
	}
	return out, nil
}

// BulkReviewTaskBatches 对齐 bulk_review_task_batches（审核后逐任务重读 TaskRead）。
func (s *TaskService) BulkReviewTaskBatches(ctx context.Context, userID, role, requestID, decision string,
	comment *string, entries []repository.TaskBulkReviewEntry) ([]repository.TaskView, error) {
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		return s.tasks.BulkReviewTaskBatches(ctx, tx, requestID, decision, comment, entries, userID, role)
	})
	if err != nil {
		return nil, mapTaskError(err, "Task or submission batch not found", "Director review permission required")
	}
	out := make([]repository.TaskView, 0, len(entries))
	for _, entry := range entries {
		view, err := s.GetTask(ctx, userID, role, entry.TaskID)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

// SetSubmissionArchived 对齐 archive_submission。
func (s *TaskService) SetSubmissionArchived(ctx context.Context, userID, role, taskID, submissionID string,
	archived bool) (repository.SubmissionView, error) {
	var view *repository.SubmissionView
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		view, err = s.tasks.SetSubmissionArchived(ctx, tx, taskID, submissionID, userID, role, archived)
		return err
	})
	if err != nil {
		return repository.SubmissionView{}, mapTaskError(err, "Task or submission not found", "Director archive permission required")
	}
	return *view, nil
}

// SetPrimarySubmission 对齐 change_primary_submission。
func (s *TaskService) SetPrimarySubmission(ctx context.Context, userID, role, taskID, submissionID string) (repository.TaskView, error) {
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		return s.tasks.SetPrimarySubmission(ctx, tx, taskID, submissionID, userID, role)
	})
	if err != nil {
		return repository.TaskView{}, mapTaskError(err, "Task or submission not found", "Director review permission required")
	}
	return s.GetTask(ctx, userID, role, taskID)
}

// ---- 工作台聚合 ----

// WorkspaceStats 对齐 _workspace_payload.stats。
type WorkspaceStats struct {
	Todo           int     `json:"todo"`
	Completed      int     `json:"completed"`
	Overdue        int     `json:"overdue"`
	CompletionRate float64 `json:"completion_rate"`
}

// WorkspaceGroups 对齐 _workspace_payload.groups。
type WorkspaceGroups struct {
	Todo      []repository.TaskView `json:"todo"`
	Completed []repository.TaskView `json:"completed"`
}

// WorkspacePayload 对齐 GET /workspace/tasks 与 /workspace/reviews 响应。
type WorkspacePayload struct {
	Stats  WorkspaceStats  `json:"stats"`
	Groups WorkspaceGroups `json:"groups"`
}

// buildWorkspacePayload 对齐 _workspace_payload。
func buildWorkspacePayload(tasks []repository.TaskView, now time.Time) WorkspacePayload {
	completed := make([]repository.TaskView, 0, len(tasks))
	todo := make([]repository.TaskView, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == taskStatusCompleted {
			completed = append(completed, task)
		} else {
			todo = append(todo, task)
		}
	}
	overdue := 0
	for _, task := range todo {
		if task.DueAt != nil && task.DueAt.Before(now) {
			overdue++
		}
	}
	rate := 0.0
	if len(tasks) > 0 {
		rate = math.Round(float64(len(completed))/float64(len(tasks))*100) / 100
	}
	if todo == nil {
		todo = []repository.TaskView{}
	}
	if completed == nil {
		completed = []repository.TaskView{}
	}
	return WorkspacePayload{
		Stats: WorkspaceStats{
			Todo: len(todo), Completed: len(completed), Overdue: overdue, CompletionRate: rate,
		},
		Groups: WorkspaceGroups{Todo: todo, Completed: completed},
	}
}

// Workspace 对齐 get_workspace_tasks：非 director/admin 强制以自身为 assignee。
func (s *TaskService) Workspace(ctx context.Context, userID, role string, assigneeID *string) (WorkspacePayload, error) {
	effective := assigneeID
	if !isDirectorOrAdminRole(role) || assigneeID == nil {
		effective = &userID
	}
	tasks, err := s.ListTasks(ctx, userID, role, nil, nil, effective)
	if err != nil {
		return WorkspacePayload{}, err
	}
	return buildWorkspacePayload(tasks, time.Now().UTC()), nil
}

// ReviewWorkspace 对齐 get_review_workspace（非 director/admin → 403）。
func (s *TaskService) ReviewWorkspace(ctx context.Context, userID, role string) (WorkspacePayload, error) {
	if !isDirectorOrAdminRole(role) {
		return WorkspacePayload{}, forbidden403("Project review permission required")
	}
	tasks, err := s.tasks.ListReviewTasks(ctx, s.pool, taskActor(userID, role))
	if err != nil {
		return WorkspacePayload{}, forbidden403("Project review permission required")
	}
	return buildWorkspacePayload(tasks, time.Now().UTC()), nil
}

// ---- 分组 / 看板 ----

// ListProjectStoryboards 对齐 list_project_storyboards。
func (s *TaskService) ListProjectStoryboards(ctx context.Context, userID, role, projectID string,
	episodeID *string) ([]repository.StoryboardView, error) {
	if _, err := s.GetProjectForTask(ctx, userID, role, projectID); err != nil {
		return nil, err
	}
	return s.tasks.ListStoryboards(ctx, s.pool, projectID, episodeID, taskActor(userID, role), time.Now().UTC())
}

// GetProductionBoard 对齐 get_project_board。
func (s *TaskService) GetProductionBoard(ctx context.Context, userID, role, projectID string) ([]repository.ProductionBoardRow, error) {
	if _, err := s.GetProjectForTask(ctx, userID, role, projectID); err != nil {
		return nil, err
	}
	return s.tasks.GetProductionBoard(ctx, s.pool, projectID, taskActor(userID, role), time.Now().UTC())
}

// SceneGating 对齐 get_scene_gating。
func (s *TaskService) SceneGating(ctx context.Context, userID, role, projectID string) ([]repository.SceneGatingRow, error) {
	if _, err := s.GetProjectForTask(ctx, userID, role, projectID); err != nil {
		return nil, err
	}
	return s.tasks.SceneGating(ctx, s.pool, projectID, taskActor(userID, role))
}

// BulkAssignTasks 对齐 bulk_assign_tasks（director/admin 路由前置校验）。
func (s *TaskService) BulkAssignTasks(ctx context.Context, userID, role, projectID string,
	in repository.BulkAssignType) (*repository.BulkAssignResult, error) {
	var result *repository.BulkAssignResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = s.tasks.BulkAssignTasks(ctx, tx, projectID, in, taskActor(userID, role), time.Now().UTC())
		return err
	})
	if err != nil {
		return nil, mapProjectTaskError(err)
	}
	return result, nil
}

// CreateStoryboardVideoTasks 对齐 create_storyboard_video_tasks。
func (s *TaskService) CreateStoryboardVideoTasks(ctx context.Context, userID, role, projectID string,
	in repository.StoryboardVideoTaskCreate) (*repository.StoryboardVideoTaskResult, error) {
	var result *repository.StoryboardVideoTaskResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = s.tasks.CreateStoryboardVideoTasks(ctx, tx, projectID, in, taskActor(userID, role), time.Now().UTC())
		return err
	})
	if err != nil {
		return nil, mapProjectTaskError(err)
	}
	return result, nil
}

// GenerateAssetTasks 对齐 generate_asset_tasks（P3f-d；行锁幂等 + 守卫逐字）。
func (s *TaskService) GenerateAssetTasks(ctx context.Context, userID, role, projectID,
	episodeID, scriptVersionID string) (map[string]any, error) {
	var result map[string]any
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = s.tasks.GenerateAssetTasks(ctx, tx, projectID, episodeID, scriptVersionID,
			taskActor(userID, role))
		return err
	})
	if err != nil {
		return nil, mapProjectTaskError(err)
	}
	return result, nil
}

// GenerateStoryboardTasks 对齐 generate_storyboard_tasks（P3f-d）。
func (s *TaskService) GenerateStoryboardTasks(ctx context.Context, userID, role, projectID,
	episodeID, scriptVersionID string) (map[string]any, error) {
	var result map[string]any
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = s.tasks.GenerateStoryboardTasks(ctx, tx, projectID, episodeID, scriptVersionID,
			taskActor(userID, role))
		return err
	})
	if err != nil {
		return nil, mapProjectTaskError(err)
	}
	return result, nil
}

// GetProjectForTask 对齐 legacy _can_access_project：project 存在性（404 优先），
// director/admin 放行，项目角色放行，否则项目内有「可见任务」也放行（否则 403）。
func (s *TaskService) GetProjectForTask(ctx context.Context, userID, role, projectID string) (*model.Projects, error) {
	p, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	if isDirectorOrAdminRole(role) {
		return p, nil
	}
	roleName, err := s.projects.ProjectMemberRole(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	if roleName == "" {
		hasVisible, err := s.projects.HasVisibleTaskInProject(ctx, userID, projectID)
		if err != nil {
			return nil, err
		}
		if !hasVisible {
			return nil, forbidden403("Project permission denied")
		}
	}
	return p, nil
}

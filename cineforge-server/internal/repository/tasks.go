package repository

// P3e task 域仓储（对齐 legacy app/services/repositories.py 的 task/submission/batch 读写 + 依赖契约）。
// 不做任何模型/历史决策：所有 schema 变更走 goose migration；这里只读取/写入现有表。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/model"
)

// queryer 由 *pgxpool.Pool 和 pgx.Tx 共同实现：读路径传 pool，写路径在事务内传 tx。
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Tasks 基于共享连接池的 task 域仓储。
type Tasks struct {
	pool *pgxpool.Pool
}

// NewTasks 创建 Tasks 仓储。
func NewTasks(pool *pgxpool.Pool) *Tasks { return &Tasks{pool: pool} }

// ---- 常量（对齐 legacy repositories.py） ----

const (
	projectRolePrefix   = "project_role:"
	approvedPrimaryMaster = "primary_master"
	approvedAlternateMaster = "alternate_master"
)

// inactiveTaskAssetStatuses 对齐 _INACTIVE_TASK_ASSET_STATUSES。
var inactiveTaskAssetStatuses = map[string]bool{"deleted": true, "excluded": true}

// approvedSubmissionStatuses 对齐 APPROVED_SUBMISSION_STATUSES。
var approvedSubmissionStatuses = map[string]bool{
	approvedPrimaryMaster: true, approvedAlternateMaster: true,
}

// ---- 视图模型（对齐 TaskRead / SubmissionRead / SubmissionBatchRead / ...） ----

// SubmissionView 对齐 SubmissionRead。
type SubmissionView struct {
	ID                       string          `json:"id"`
	TaskId                   string          `json:"task_id"`
	BatchId                  *string         `json:"batch_id"`
	FileId                   *string         `json:"file_id"`
	StoryboardId             *string         `json:"storyboard_id"`
	FilePath                 string          `json:"file_path"`
	FileType                 string          `json:"file_type"`
	Step                     *string         `json:"step"`
	PromptText               *string         `json:"prompt_text"`
	OriginalPromptText       *string         `json:"original_prompt_text"`
	RevisedPromptText        *string         `json:"revised_prompt_text"`
	ViewLabel                *string         `json:"view_label"`
	StateLabel               *string         `json:"state_label"`
	Description              *string         `json:"description"`
	ModelName                *string         `json:"model_name"`
	ToolNames                []string         `json:"tool_names"`
	SubmittedById            *string         `json:"submitted_by_id"`
	Status                   string          `json:"status"`
	IsSelected               bool            `json:"is_selected"`
	IsPrimary                bool            `json:"is_primary"`
	IsArchived               bool            `json:"is_archived"`
	ArchivedById             *string         `json:"archived_by_id"`
	ArchivedAt               *time.Time      `json:"archived_at"`
	SourceMasterSubmissionId *string         `json:"source_master_submission_id"`
	IsInvalidated            bool            `json:"is_invalidated"`
	InvalidatedAt            *time.Time      `json:"invalidated_at"`
	InvalidatedById          *string         `json:"invalidated_by_id"`
	InvalidatedReason        *string         `json:"invalidated_reason"`
	InvalidatedSourceId      *string         `json:"invalidated_source_id"`
	PurgedAt                 *time.Time      `json:"purged_at"`
	CreatedAt                time.Time       `json:"created_at"`
}

// BatchView 对齐 SubmissionBatchRead。
type BatchView struct {
	ID              string     `json:"id"`
	TaskId          string     `json:"task_id"`
	Step            string     `json:"step"`
	VersionNo       int32      `json:"version_no"`
	Status          string     `json:"status"`
	SubmittedById   *string    `json:"submitted_by_id"`
	SubmittedAt     *time.Time `json:"submitted_at"`
	ReviewedById    *string    `json:"reviewed_by_id"`
	ReviewedAt      *time.Time `json:"reviewed_at"`
	ReviewComment   *string    `json:"review_comment"`
	SubmitRequestId *string    `json:"submit_request_id"`
	ReviewRequestId *string    `json:"review_request_id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// DependencySummaryView 对齐 DependencySummaryRead。
type DependencySummaryView struct {
	Required int `json:"required"`
	Ready    int `json:"ready"`
	Pending  int `json:"pending"`
}

// DependencyFileView 对齐 DependencyFileRead。
type DependencyFileView struct {
	FileId          string  `json:"file_id"`
	FileName        *string `json:"file_name"`
	MimeType        *string `json:"mime_type"`
	FileSize        *int32  `json:"file_size"`
	AssetVersionId  *string `json:"asset_version_id"`
	DownloadName    *string `json:"download_name"`
	ViewLabel       *string `json:"view_label"`
	SubmissionId    *string `json:"submission_id"`
	Step            *string `json:"step"`
}

// DependencyAssetView 对齐 DependencyAssetRead。
type DependencyAssetView struct {
	AssetId          *string               `json:"asset_id"`
	AssetCode        string                `json:"asset_code"`
	AssetName        *string               `json:"asset_name"`
	AssetType        *string               `json:"asset_type"`
	RequiredFor      *string               `json:"required_for"`
	ProductionStatus *string               `json:"production_status"`
	ApprovalStatus   *string               `json:"approval_status"`
	Ready            bool                  `json:"ready"`
	Reason           *string               `json:"reason"`
	Files            []DependencyFileView  `json:"files"`
}

// TaskView 对齐 TaskRead（56 字段）。
type TaskView struct {
	ID                          string                `json:"id"`
	ProjectId                   string                `json:"project_id"`
	EpisodeId                   *string               `json:"episode_id"`
	EpisodeCode                 *string               `json:"episode_code"`
	ScriptId                    *string               `json:"script_id"`
	ScriptVersionId             *string               `json:"script_version_id"`
	StoryboardId                *string               `json:"storyboard_id"`
	AssetId                     *string               `json:"asset_id"`
	ScriptSegmentId             *string               `json:"script_segment_id"`
	SceneCode                   *string               `json:"scene_code"`
	SceneName                   *string               `json:"scene_name"`
	StoryboardCode              *string               `json:"storyboard_code"`
	StoryboardDurationSeconds   *int32                `json:"storyboard_duration_seconds"`
	StoryboardDialogue          *string               `json:"storyboard_dialogue"`
	StoryboardDialogueBackTranslation *string         `json:"storyboard_dialogue_back_translation"`
	StoryboardMirrorShots       []map[string]any      `json:"storyboard_mirror_shots"`
	TaskType                    string                `json:"task_type"`
	Title                       string                `json:"title"`
	AssigneeId                  *string               `json:"assignee_id"`
	AssigneeName                *string               `json:"assignee_name"`
	Status                      string                `json:"status"`
	PromptText                  *string               `json:"prompt_text"`
	LatestPromptText            *string               `json:"latest_prompt_text"`
	DueAt                       *time.Time            `json:"due_at"`
	CompletedAt                 *time.Time            `json:"completed_at"`
	ProductionModel             *string               `json:"production_model"`
	MediaType                   *string               `json:"media_type"`
	TaskVariant                 *string               `json:"task_variant"`
	AgeStageCode                *string               `json:"age_stage_code"`
	CostumeVariantCode          *string               `json:"costume_variant_code"`
	VariantPlanId               *string               `json:"variant_plan_id"`
	VariantKind                 *string               `json:"variant_kind"`
	VariantTitleZh              *string               `json:"variant_title_zh"`
	VariantDescriptionZh        *string               `json:"variant_description_zh"`
	LinkedStoryboardIds         []string              `json:"linked_storyboard_ids"`
	PromptRevisionId            *string               `json:"prompt_revision_id"`
	AssetContextOutdated        bool                  `json:"asset_context_outdated"`
	DependsOnTaskId             *string               `json:"depends_on_task_id"`
	IsRetired                   bool                  `json:"is_retired"`
	RetiredAt                   *time.Time            `json:"retired_at"`
	RetiredBy                   *string               `json:"retired_by"`
	RetiredReason               *string               `json:"retired_reason"`
	DependencyTaskIds           []string              `json:"dependency_task_ids"`
	DependencyAssetCodes        []string              `json:"dependency_asset_codes"`
	DependencySummary           DependencySummaryView `json:"dependency_summary"`
	DependencyAssets            []DependencyAssetView `json:"dependency_assets"`
	KeyframeDone                bool                  `json:"keyframe_done"`
	VideoDone                   bool                  `json:"video_done"`
	Locked                      bool                  `json:"locked"`
	Submissions                 []SubmissionView      `json:"submissions"`
	SubmissionBatches           []BatchView           `json:"submission_batches"`
	CreatedAt                   time.Time             `json:"created_at"`
	UpdatedAt                   time.Time             `json:"updated_at"`
}

// PromptView 对齐 TaskPromptRead。
type PromptView struct {
	ID         string     `json:"id"`
	TaskId     string     `json:"task_id"`
	PromptType string     `json:"prompt_type"`
	PromptText string     `json:"prompt_text"`
	Source     string     `json:"source"`
	Copied     bool       `json:"copied"`
	CreatedBy  *string    `json:"created_by"`
	VersionNo  int32      `json:"version_no"`
	CreatedAt  time.Time  `json:"created_at"`
}

// StoryboardView 对齐 StoryboardRead。
type StoryboardView struct {
	ID                           string           `json:"id"`
	ProjectId                    string           `json:"project_id"`
	ScriptSegmentId              *string          `json:"script_segment_id"`
	EpisodeNum                   int32            `json:"episode_num"`
	OrderNum                     int32            `json:"order_num"`
	StoryboardCode               *string          `json:"storyboard_code"`
	Title                        *string          `json:"title"`
	Description                  string           `json:"description"`
	Dialogue                     *string          `json:"dialogue"`
	DialogueBackTranslation      *string          `json:"dialogue_back_translation"`
	Camera                       *string          `json:"camera"`
	ShotType                     *string          `json:"shot_type"`
	DurationSeconds              *int32           `json:"duration_seconds"`
	Characters                   []string         `json:"characters"`
	Keyframes                    []map[string]any `json:"keyframes"`
	MirrorShots                  []map[string]any `json:"mirror_shots"`
	SceneCode                    *string          `json:"scene_code"`
	SceneName                    *string          `json:"scene_name"`
	ContextCode                  *string          `json:"context_code"`
	RenderMode                   *string          `json:"render_mode"`
	Status                       string           `json:"status"`
}

// ProductionBoardRow 对齐 ProductionBoardRow。
type ProductionBoardRow struct {
	StoryboardId string            `json:"storyboard_id"`
	EpisodeNum   int32             `json:"episode_num"`
	OrderNum     int32             `json:"order_num"`
	Title        *string           `json:"title"`
	Description  string            `json:"description"`
	Characters   []string          `json:"characters"`
	Status       string            `json:"status"`
	Tasks        []TaskView        `json:"tasks"`
	Submissions  []SubmissionView  `json:"submissions"`
}

// SceneGatingRow 对齐 _scene_gating_core 单场景输出。
type SceneGatingRow struct {
	SceneCode           string   `json:"scene_code"`
	SceneName           *string  `json:"scene_name"`
	RequiredAssetCodes  []string `json:"required_asset_codes"`
	PendingAssetCodes   []string `json:"pending_asset_codes"`
	Locked              bool     `json:"locked"`
}

// ---- task row IO ----

const taskColumns = `id, project_id, episode_id, script_id, script_version_id, idempotency_key,
	script_segment_id, storyboard_id, asset_id, scene_code, scene_name, task_type, title, assignee_id,
	assigned_by, assigned_at, status, prompt_text, latest_prompt_text, due_at, completed_at, visible_until,
	production_model, media_type, task_variant, age_stage_code, costume_variant_code, variant_plan_id,
	variant_kind, variant_title_zh, variant_description_zh, prompt_revision_id, asset_context_outdated,
	depends_on_task_id, is_retired, retired_at, retired_by, retired_reason, created_at, updated_at`

func scanTask(row pgx.Row) (*model.Tasks, error) {
	var t model.Tasks
	err := row.Scan(
		&t.ID, &t.ProjectId, &t.EpisodeId, &t.ScriptId, &t.ScriptVersionId, &t.IDempotencyKey,
		&t.ScriptSegmentId, &t.StoryboardId, &t.AssetId, &t.SceneCode, &t.SceneName, &t.TaskType,
		&t.Title, &t.AssigneeId, &t.AssignedBy, &t.AssignedAt, &t.Status, &t.PromptText,
		&t.LatestPromptText, &t.DueAt, &t.CompletedAt, &t.VisibleUntil, &t.ProductionModel,
		&t.MediaType, &t.TaskVariant, &t.AgeStageCode, &t.CostumeVariantCode, &t.VariantPlanId,
		&t.VariantKind, &t.VariantTitleZh, &t.VariantDescriptionZh, &t.PromptRevisionId,
		&t.AssetContextOutdated, &t.DependsOnTaskId, &t.IsRetired, &t.RetiredAt, &t.RetiredBy,
		&t.RetiredReason, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func scanTaskRows(rows pgx.Rows) ([]*model.Tasks, error) {
	defer rows.Close()
	var out []*model.Tasks
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// activeAssetTaskCondition 对齐 _active_asset_task_condition。
func activeAssetTaskCondition() string {
	return `tasks.is_retired = false AND (tasks.asset_id IS NULL OR tasks.asset_id IN (
		SELECT id FROM assets WHERE status NOT IN ('deleted','excluded')))`
}

// GetActiveAssetTask 对齐 _get_active_asset_task（无行锁版本）。
func (r *Tasks) GetActiveAssetTask(ctx context.Context, q queryer, taskID string) (*model.Tasks, error) {
	t, err := scanTask(q.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = $1 AND `+activeAssetTaskCondition(), taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active asset task: %w", err)
	}
	return t, nil
}

// GetActiveAssetTaskForUpdate 对齐 _get_active_asset_task(with_for_update=True)。
func (r *Tasks) GetActiveAssetTaskForUpdate(ctx context.Context, tx pgx.Tx, taskID string) (*model.Tasks, error) {
	t, err := scanTask(tx.QueryRow(ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE id = $1 AND `+activeAssetTaskCondition()+` FOR UPDATE`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active asset task for update: %w", err)
	}
	return t, nil
}

// ---- project permission helpers（对齐 _has_project_permission / _can_write_project / _can_access_project）----

// HasProjectPermission 是否存在 project_role 权限行。
func (r *Tasks) HasProjectPermission(ctx context.Context, q queryer, userID, projectID string) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM permissions WHERE user_id = $1 AND project_id = $2 AND permission_type LIKE $3)`,
		userID, projectID, projectRolePrefix+"%").Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has project permission: %w", err)
	}
	return exists, nil
}

// ProjectMemberRole 对齐 _project_member_role（permission_type 首个 project_role:* 取角色，缺省/非法 → ""）。
func (r *Tasks) ProjectMemberRole(ctx context.Context, q queryer, userID, projectID string) (string, error) {
	var ptype string
	err := q.QueryRow(ctx,
		`SELECT permission_type FROM permissions
		 WHERE user_id = $1 AND project_id = $2 AND permission_type LIKE $3
		 ORDER BY created_at LIMIT 1`,
		userID, projectID, projectRolePrefix+"%").Scan(&ptype)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("project member role: %w", err)
	}
	parts := strings.SplitN(ptype, ":", 2)
	if len(parts) != 2 {
		return "", nil
	}
	return parts[1], nil
}

// CanWriteProject 对齐 _can_write_project（director/admin 恒 true；否则 owner/manager/lead）。
func (r *Tasks) CanWriteProject(ctx context.Context, q queryer, projectID, userID, role string) (bool, error) {
	if isDirectorOrAdminRole(role) {
		return true, nil
	}
	roleName, err := r.ProjectMemberRole(ctx, q, userID, projectID)
	if err != nil {
		return false, err
	}
	return projectWriteRoles[roleName], nil
}

// CanAccessProject 对齐 legacy _can_access_project：director/admin、任意 project_role 权限，
// 或项目内有「可见任务」（assignee 可见任务回退）。
func (r *Tasks) CanAccessProject(ctx context.Context, q queryer, projectID, userID, role string) (bool, error) {
	if isDirectorOrAdminRole(role) {
		return true, nil
	}
	has, err := r.HasProjectPermission(ctx, q, userID, projectID)
	if err != nil {
		return false, err
	}
	if has {
		return true, nil
	}
	return hasVisibleTaskInProject(ctx, q, userID, projectID)
}

// ---- submission / batch row IO ----

const submissionColumns = `id, task_id, batch_id, file_id, storyboard_id, file_path, file_type, step,
	prompt_text, original_prompt_text, revised_prompt_text, view_label, state_label, description,
	model_name, tool_names, submitted_by_id, status, is_selected, is_primary, is_archived,
	archived_by_id, archived_at, source_master_submission_id, is_invalidated, invalidated_at,
	invalidated_by_id, invalidated_reason, invalidated_source_id, purged_at, created_at, updated_at`

func scanSubmission(row pgx.Row) (*model.Submissions, error) {
	var s model.Submissions
	err := row.Scan(
		&s.ID, &s.TaskId, &s.BatchId, &s.FileId, &s.StoryboardId, &s.FilePath, &s.FileType,
		&s.Step, &s.PromptText, &s.OriginalPromptText, &s.RevisedPromptText, &s.ViewLabel,
		&s.StateLabel, &s.Description, &s.ModelName, &s.ToolNames, &s.SubmittedById, &s.Status,
		&s.IsSelected, &s.IsPrimary, &s.IsArchived, &s.ArchivedById, &s.ArchivedAt,
		&s.SourceMasterSubmissionId, &s.IsInvalidated, &s.InvalidatedAt, &s.InvalidatedById,
		&s.InvalidatedReason, &s.InvalidatedSourceId, &s.PurgedAt, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func scanSubmissionRows(rows pgx.Rows) ([]*model.Submissions, error) {
	defer rows.Close()
	var out []*model.Submissions
	for rows.Next() {
		s, err := scanSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const batchColumns = `id, task_id, step, version_no, status, submitted_by_id, submitted_at,
	reviewed_by_id, reviewed_at, review_comment, submit_request_id, review_request_id, created_at, updated_at`

func scanBatch(row pgx.Row) (*model.TaskSubmissionBatches, error) {
	var b model.TaskSubmissionBatches
	err := row.Scan(
		&b.ID, &b.TaskId, &b.Step, &b.VersionNo, &b.Status, &b.SubmittedById, &b.SubmittedAt,
		&b.ReviewedById, &b.ReviewedAt, &b.ReviewComment, &b.SubmitRequestId, &b.ReviewRequestId,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func scanBatchRows(rows pgx.Rows) ([]*model.TaskSubmissionBatches, error) {
	defer rows.Close()
	var out []*model.TaskSubmissionBatches
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// submissionToView 对齐 submission_to_read（tool_names 归一化 + is_invalidated 转 bool）。
func submissionToView(s *model.Submissions) SubmissionView {
	var toolNames []string
	_ = json.Unmarshal(s.ToolNames, &toolNames)
	if toolNames == nil {
		toolNames = []string{}
	}
	return SubmissionView{
		ID: s.ID, TaskId: s.TaskId, BatchId: s.BatchId, FileId: s.FileId,
		StoryboardId: s.StoryboardId, FilePath: s.FilePath, FileType: s.FileType, Step: s.Step,
		PromptText: s.PromptText, OriginalPromptText: s.OriginalPromptText,
		RevisedPromptText: s.RevisedPromptText, ViewLabel: s.ViewLabel, StateLabel: s.StateLabel,
		Description: s.Description, ModelName: s.ModelName, ToolNames: toolNames,
		SubmittedById: s.SubmittedById, Status: s.Status, IsSelected: s.IsSelected,
		IsPrimary: s.IsPrimary, IsArchived: s.IsArchived, ArchivedById: s.ArchivedById,
		ArchivedAt: s.ArchivedAt, SourceMasterSubmissionId: s.SourceMasterSubmissionId,
		IsInvalidated: s.IsInvalidated, InvalidatedAt: s.InvalidatedAt,
		InvalidatedById: s.InvalidatedById, InvalidatedReason: s.InvalidatedReason,
		InvalidatedSourceId: s.InvalidatedSourceId, PurgedAt: s.PurgedAt, CreatedAt: s.CreatedAt,
	}
}

// batchToView 对齐 submission_batch_to_read（纯直传）。
func batchToView(b *model.TaskSubmissionBatches) BatchView {
	return BatchView{
		ID: b.ID, TaskId: b.TaskId, Step: b.Step, VersionNo: b.VersionNo, Status: b.Status,
		SubmittedById: b.SubmittedById, SubmittedAt: b.SubmittedAt, ReviewedById: b.ReviewedById,
		ReviewedAt: b.ReviewedAt, ReviewComment: b.ReviewComment,
		SubmitRequestId: b.SubmitRequestId, ReviewRequestId: b.ReviewRequestId,
		CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
	}
}

// BatchToView 导出单批视图（服务层提交/审核写路径用）。
func BatchToView(b *model.TaskSubmissionBatches) BatchView { return batchToView(b) }

// ---- task_to_read 辅助（对齐 repositories.py task_to_read） ----

// storyboardBackTranslation 对齐 _storyboard_back_translation。
func storyboardBackTranslation(s *model.Storyboards) *string {
	if s == nil {
		return nil
	}
	var shots []map[string]any
	_ = json.Unmarshal(s.MirrorShots, &shots)
	parts := []string{}
	for _, item := range shots {
		if v, ok := item["dialogue_back_translation"]; ok {
			text := strings.TrimSpace(fmt.Sprintf("%v", v))
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	joined := strings.Join(parts, "\n")
	return &joined
}

func storyboardMirrorShots(s *model.Storyboards) []map[string]any {
	if s == nil {
		return []map[string]any{}
	}
	var shots []map[string]any
	_ = json.Unmarshal(s.MirrorShots, &shots)
	out := make([]map[string]any, 0, len(shots))
	for _, item := range shots {
		if item != nil {
			out = append(out, item)
		}
	}
	return out
}

// taskToRead 对齐 task_to_read；依赖上下文由调用方批量解析后传入。
func taskToRead(t *model.Tasks, s *model.Storyboards, episodeCode *string, assigneeName *string,
	locked bool, depIDs, depCodes []string, summary DependencySummaryView, deps []DependencyAssetView,
	submissions []*model.Submissions, batches []*model.TaskSubmissionBatches) TaskView {
	steps := map[string]bool{}
	for _, item := range submissions {
		if item == nil {
			continue
		}
		if approvedSubmissionStatuses[item.Status] && item.IsSelected && !item.IsArchived && !item.IsInvalidated {
			steps[strings.ToLower(nonNil(item.Step))] = true
		}
	}
	episodeCode2 := episodeCode
	if episodeCode2 == nil && s != nil {
		c := fmt.Sprintf("EP%02d", s.EpisodeNum)
		episodeCode2 = &c
	}
	depIDsOut := depIDs
	if depIDsOut == nil {
		depIDsOut = []string{}
	}
	depCodesOut := depCodes
	if depCodesOut == nil {
		depCodesOut = []string{}
	}
	depsOut := deps
	if depsOut == nil {
		depsOut = []DependencyAssetView{}
	}
	subViews := make([]SubmissionView, 0, len(submissions))
	for _, item := range submissions {
		if item != nil {
			subViews = append(subViews, submissionToView(item))
		}
	}
	batchViews := make([]BatchView, 0, len(batches))
	for _, item := range batches {
		if item != nil {
			batchViews = append(batchViews, batchToView(item))
		}
	}
	return TaskView{
		ID: t.ID, ProjectId: t.ProjectId, EpisodeId: t.EpisodeId, EpisodeCode: episodeCode2,
		ScriptId: t.ScriptId, ScriptVersionId: t.ScriptVersionId, StoryboardId: t.StoryboardId,
		AssetId: t.AssetId, ScriptSegmentId: t.ScriptSegmentId, SceneCode: t.SceneCode,
		SceneName: t.SceneName,
		StoryboardCode:                   storyboardStr(s, storyboardCodeField),
		StoryboardDurationSeconds:        storyboardDuration(s),
		StoryboardDialogue:               storyboardDialogue(s),
		StoryboardDialogueBackTranslation: storyboardBackTranslation(s),
		StoryboardMirrorShots:            storyboardMirrorShots(s),
		TaskType: t.TaskType, Title: t.Title, AssigneeId: t.AssigneeId, AssigneeName: assigneeName,
		Status: t.Status, PromptText: t.PromptText, LatestPromptText: t.LatestPromptText,
		DueAt: t.DueAt, CompletedAt: t.CompletedAt, ProductionModel: t.ProductionModel,
		MediaType: t.MediaType, TaskVariant: t.TaskVariant, AgeStageCode: t.AgeStageCode,
		CostumeVariantCode: t.CostumeVariantCode, VariantPlanId: t.VariantPlanId,
		VariantKind: t.VariantKind, VariantTitleZh: t.VariantTitleZh,
		VariantDescriptionZh: t.VariantDescriptionZh, LinkedStoryboardIds: []string{},
		PromptRevisionId: t.PromptRevisionId, AssetContextOutdated: t.AssetContextOutdated,
		DependsOnTaskId: t.DependsOnTaskId, IsRetired: t.IsRetired, RetiredAt: t.RetiredAt,
		RetiredBy: t.RetiredBy, RetiredReason: t.RetiredReason,
		DependencyTaskIds: depIDsOut, DependencyAssetCodes: depCodesOut,
		DependencySummary: summary, DependencyAssets: depsOut,
		KeyframeDone: steps["keyframe"], VideoDone: steps["video"], Locked: locked,
		Submissions: subViews, SubmissionBatches: batchViews,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

type storyboardField int

const (
	storyboardCodeField storyboardField = iota
	storyboardDialogueField
)

func storyboardStr(s *model.Storyboards, f storyboardField) *string {
	if s == nil {
		return nil
	}
	var v *string
	switch f {
	case storyboardCodeField:
		v = s.StoryboardCode
	case storyboardDialogueField:
		v = s.Dialogue
	}
	return v
}

func storyboardDuration(s *model.Storyboards) *int32 {
	if s == nil {
		return nil
	}
	if s.DurationSeconds != nil {
		v := *s.DurationSeconds
		return &v
	}
	return nil
}

func storyboardDialogue(s *model.Storyboards) *string {
	if s == nil {
		return nil
	}
	return s.Dialogue
}

func nonNil(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// ---- 共享辅助 ----

// isDirectorOrAdminRole 对齐 Service.isDirectorOrAdminRole（repository 独立实现避免循环依赖）。
func isDirectorOrAdminRole(role string) bool {
	return role == "director" || role == "admin"
}

// projectWriteRoles 对齐 Service.projectWriteRoles（owner/manager/lead）。
var projectWriteRoles = map[string]bool{"owner": true, "manager": true, "lead": true}
package service

// projects 域业务服务（对齐 legacy app/api/routes/projects.py + repositories.py）：
// 项目 CRUD 与可见性、剧本导入事务、分集列表/详情、dashboard 聚合。
// 契约约定：KeyError→404、PermissionError→403、ValueError→400、HTTPException 逐字 detail。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/agentclient"
	"cineforge/server/internal/model"
	"cineforge/server/internal/productionbrief"
	"cineforge/server/internal/repository"
)

// StatusError 携带 HTTP 状态码 + 逐字 detail（映射 legacy HTTPException）。
type StatusError struct {
	Status int
	Detail string
	Err    error
}

func (e *StatusError) Error() string { return e.Detail }
func (e *StatusError) Unwrap() error { return e.Err }

func badRequest400(detail string) error { return &StatusError{Status: 400, Detail: detail} }
func conflict409(detail string) error   { return &StatusError{Status: 409, Detail: detail} }
func notFound404(detail string) error   { return &StatusError{Status: 404, Detail: detail} }
func forbidden403(detail string) error  { return &StatusError{Status: 403, Detail: detail} }
func status503(detail string) error     { return &StatusError{Status: 503, Detail: detail} }
func status413(detail string) error     { return &StatusError{Status: 413, Detail: detail} }

// StatusOf 提取 StatusError.code；非 StatusError 视为 500。
func StatusOf(err error) (int, string) {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status, se.Detail
	}
	return 500, ""
}

// projectWriteRoles 对齐 legacy PROJECT_WRITE_ROLES（owner/manager/lead）。
var projectWriteRoles = map[string]bool{"owner": true, "manager": true, "lead": true}

func isDirectorOrAdminRole(role string) bool {
	return role == "director" || role == "admin"
}

// ProjectItem 对齐 ProjectRead。
type ProjectItem struct {
	ID              string          `json:"id"`
	ProjectNo       *string         `json:"project_no"`
	ProjectPrefix   *string         `json:"project_prefix"`
	Name            *string         `json:"name"`
	Title           string          `json:"title"`
	Genre           *string         `json:"genre"`
	Status          string          `json:"status"`
	CurrentStage    *string         `json:"current_stage"`
	ManagerID       *string         `json:"manager_id"`
	ScriptText      *string         `json:"script_text"`
	ProductionBrief json.RawMessage `json:"production_brief"`
	ArchivedAt      *time.Time      `json:"archived_at"`
	DeletedAt       *time.Time      `json:"deleted_at"`
	StoryboardCount int             `json:"storyboard_count"`
	TaskCount       int             `json:"task_count"`
	LatestImport    *ImportItem     `json:"latest_import"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// ImportItem 对齐 ProjectImportContext。
type ImportItem struct {
	EpisodeID               string `json:"episode_id"`
	EpisodeNo               int32  `json:"episode_no"`
	EpisodeCode             string `json:"episode_code"`
	ScriptID                string `json:"script_id"`
	ScriptVersionID         string `json:"script_version_id"`
	ScriptVersionNo         int32  `json:"script_version_no"`
	CreatedEpisode          bool   `json:"created_episode"`
	CreatedScript           bool   `json:"created_script"`
	ReplacedExistingEpisode bool   `json:"replaced_existing_episode"`
	ContentHash             string `json:"content_hash"`
}

func toProjectItem(p *model.Projects, sb, tc int) ProjectItem {
	item := ProjectItem{
		ID:              p.ID,
		ProjectNo:       p.ProjectNo,
		ProjectPrefix:   p.ProjectPrefix,
		Name:            p.Name,
		Title:           p.Title,
		Genre:           p.Genre,
		Status:          p.Status,
		ManagerID:       p.ManagerId,
		ScriptText:      p.ScriptText,
		ProductionBrief: briefOrEmpty(p.ProductionBrief),
		ArchivedAt:      p.ArchivedAt,
		DeletedAt:       p.DeletedAt,
		StoryboardCount: sb,
		TaskCount:       tc,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
	if p.CurrentStage != "" {
		stage := p.CurrentStage
		item.CurrentStage = &stage
	}
	return item
}

// briefOrEmpty production_brief 列 NULL 时输出 null（legacy dict|None）。
func briefOrEmpty(raw *json.RawMessage) json.RawMessage {
	if raw == nil || len(*raw) == 0 {
		return json.RawMessage(`null`)
	}
	return *raw
}

// CreateProjectInput 对齐 ProjectCreate。
type CreateProjectInput struct {
	Title           string
	Genre           *string
	ScriptText      *string
	ProjectPrefix   *string
	ManagerID       *string
	ProductionBrief json.RawMessage
}

// UpdateProjectInput 对齐 ProjectUpdate（production_brief 指针语义：nil=不改）。
type UpdateProjectInput struct {
	Title           *string
	Genre           *string
	ScriptText      *string
	ProjectPrefix   *string
	ManagerID       *string
	ProductionBrief *json.RawMessage
}

// ImportRequest /api/projects/import 查询参数 + 文件体。
type ImportRequest struct {
	Title                         *string
	ProjectPrefix                 *string
	Genre                         *string
	ProjectID                     *string
	ProductionBrief               string
	EpisodeBrief                  string
	EpisodeNo                     *int32
	ConfirmExistingEpisodeVersion bool
	Filename                      string
	Raw                           []byte
}

// ImportArchiver P3f 之前生产默认不可用：import 时归档失败 → 契约 503。
type ImportArchiver interface {
	ArchiveImportFile(ctx context.Context, projectID string, raw []byte, filename, uploadedBy string) (*model.Files, error)
}

type archiveUnavailableArchiver struct{}

// ArchiveImportFile 未接线（P3f MinIO 实现），恒返回错误 → handler 层 503。
func (archiveUnavailableArchiver) ArchiveImportFile(context.Context, string, []byte, string, string) (*model.Files, error) {
	return nil, errors.New("import archiver not yet wired (expected in P3f)")
}

// DefaultArchiver 返回当前生产默认归档器（恒 503）。
func DefaultArchiver() ImportArchiver { return archiveUnavailableArchiver{} }

// ObjectRemover 删除对象存储对象（分集级联删除时释放 MinIO 文件；对齐 legacy delete_stored_object）。
type ObjectRemover interface {
	Remove(ctx context.Context, bucket, objectKey string) error
}

// ProjectService 承载项目/分集/导入/dashboard 业务。
type ProjectService struct {
	pool     *pgxpool.Pool
	projects *repository.Projects
	users    *repository.Users
	archiver ImportArchiver
	objects  ObjectRemover
	agents   *agentclient.Client
}

// NewProjectService 创建 ProjectService。objects 可空：MinIO 未配置时文件清理按失败计数。
// agents 可空：agent 子模块未接入时五步提交跳过 enqueue（P4c 后始终注入）。
func NewProjectService(pool *pgxpool.Pool, projects *repository.Projects, users *repository.Users, archiver ImportArchiver, objects ObjectRemover, agents *agentclient.Client) *ProjectService {
	if archiver == nil {
		archiver = DefaultArchiver()
	}
	return &ProjectService{pool: pool, projects: projects, users: users, archiver: archiver, objects: objects, agents: agents}
}

// ---- 可见性辅助（对齐 legacy _can_access_project / _can_write_project）----

// canRead project 读取权限：director/admin、任意 project_role，或项目内有「可见任务」
// （对齐 legacy _can_access_project，含可见任务回退）。
func (s *ProjectService) canRead(ctx context.Context, userID, role, projectID string) (bool, error) {
	if isDirectorOrAdminRole(role) {
		return true, nil
	}
	has, err := s.projects.HasProjectPermission(ctx, userID, projectID)
	if err != nil {
		return false, err
	}
	if has {
		return true, nil
	}
	return s.projects.HasVisibleTaskInProject(ctx, userID, projectID)
}

// canWrite project 写入权限：director/admin 或 owner/manager/lead。
func (s *ProjectService) canWrite(ctx context.Context, userID, role, projectID string) (bool, error) {
	if isDirectorOrAdminRole(role) {
		return true, nil
	}
	roleName, err := s.projects.ProjectMemberRole(ctx, userID, projectID)
	if err != nil {
		return false, err
	}
	return projectWriteRoles[roleName], nil
}

// ---- ListProjects ----

// ListProjects 可见项目列表（director 全量；其余合并项目角色 ∪ 可见任务所在项目）。
func (s *ProjectService) ListProjects(ctx context.Context, userID, role string) ([]ProjectItem, error) {
	var cards []repository.ProjectCard
	if isDirectorOrAdminRole(role) {
		list, err := s.projects.ListCards(ctx, false)
		if err != nil {
			return nil, err
		}
		cards = list
	} else {
		ids, err := s.projects.VisibleProjectIDs(ctx, userID)
		if err != nil {
			return nil, err
		}
		list, err := s.projects.ListCardsByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		cards = list
	}
	out := make([]ProjectItem, 0, len(cards))
	for _, c := range cards {
		counts, err := s.projects.CountsDirector(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, toProjectItem(&c.Projects, counts.StoryboardCount, counts.TaskCount))
	}
	return out, nil
}

// GetProject 读取项目（404/403 逐字；存在性先于权限，对齐 legacy get_project）+ 计数。
func (s *ProjectService) GetProject(ctx context.Context, userID, role, projectID string) (*ProjectItem, error) {
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
	counts, err := s.projects.CountsDirector(ctx, projectID)
	if err != nil {
		return nil, err
	}
	item := toProjectItem(p, counts.StoryboardCount, counts.TaskCount)
	return &item, nil
}

// CreateProject 创建草稿项目（director/admin；production_brief 校验收 400）。
func (s *ProjectService) CreateProject(ctx context.Context, userID string, in CreateProjectInput) (*ProjectItem, error) {
	parsed, err := productionbrief.ParseProjectBrief(briefOrEmpty(&in.ProductionBrief))
	if err != nil {
		return nil, badRequest400("production_brief: " + err.Error())
	}
	parsedJSON, err := json.Marshal(parsed)
	if err != nil {
		return nil, err
	}
	parsedBriefRaw := json.RawMessage(parsedJSON)
	briefNo, err := projectNo()
	if err != nil {
		return nil, err
	}
	managerID := in.ManagerID
	if managerID == nil {
		managerID = &userID
	}
	p := &model.Projects{
		ProjectNo:       &briefNo,
		ProjectPrefix:   prefixOrNil(in.ProjectPrefix),
		Title:           in.Title,
		Name:            &in.Title,
		Genre:           in.Genre,
		ScriptText:      in.ScriptText,
		ProductionBrief: &parsedBriefRaw,
		Status:          "draft",
		CurrentStage:    "draft",
		ManagerId:       managerID,
		CreatedById:     &userID,
	}
	id, err := newUUID()
	if err != nil {
		return nil, err
	}
	p.ID = id
	if err := s.projects.Create(ctx, p); err != nil {
		return nil, err
	}
	item := toProjectItem(p, 0, 0)
	if err := s.projects.RegisterOperationLog(ctx, p.ID, userID, "project", p.ID, "project_created", map[string]any{
		"title": p.Title, "project_prefix": p.ProjectPrefix, "production_brief": p.ProductionBrief, "source": "manual",
	}); err != nil {
		return nil, err
	}
	return &item, nil
}

// UpdateProject 更新项目（director/admin；生产任务/缩写锁校验对齐 legacy）。
func (s *ProjectService) UpdateProject(ctx context.Context, userID, projectID string, in UpdateProjectInput) (*ProjectItem, error) {
	p, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	if in.ProductionBrief != nil {
		hasActive, err := s.projects.HasActiveProjectTasks(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if hasActive {
			return nil, badRequest400("项目存在已分发或执行中的任务，请先撤回或关闭受影响任务后再修改制作设置。")
		}
	}
	if in.ProjectPrefix != nil {
		requested := prefixOrNil(in.ProjectPrefix)
		hasScripts, err := s.projects.HasScripts(ctx, projectID)
		if err != nil {
			return nil, err
		}
		if hasScripts && !equalStringPtr(requested, p.ProjectPrefix) {
			return nil, badRequest400("项目已导入剧本，项目缩写已锁定；修改编号体系需使用专用迁移流程。")
		}
	}
	profileBefore := projectProfileContent(p)
	if in.Title != nil {
		p.Title = *in.Title
		p.Name = in.Title
	}
	if in.Genre != nil {
		p.Genre = in.Genre
	}
	if in.ScriptText != nil {
		p.ScriptText = in.ScriptText
	}
	if in.ProjectPrefix != nil {
		p.ProjectPrefix = prefixOrNil(in.ProjectPrefix)
	}
	if in.ManagerID != nil {
		p.ManagerId = in.ManagerID
	}
	if in.ProductionBrief != nil {
		p.ProductionBrief = in.ProductionBrief
	}
	if err := s.projects.UpdateProjectFields(ctx, p); err != nil {
		return nil, err
	}
	// P3d 不落 project_profile revision（P3g 通知/审计补齐）；draft 更新即返回。
	_ = profileBefore
	counts, err := s.projects.CountsDirector(ctx, projectID)
	if err != nil {
		return nil, err
	}
	item := toProjectItem(p, counts.StoryboardCount, counts.TaskCount)
	if err := s.projects.RegisterOperationLog(ctx, projectID, userID, "project", projectID, "project_draft_updated", map[string]any{
		"title": p.Title, "project_prefix": p.ProjectPrefix, "script_text_chars": len(ptrOr(p.ScriptText, "")),
		"production_brief": p.ProductionBrief,
	}); err != nil {
		return nil, err
	}
	return &item, nil
}

// ---- 剧本导入（对齐 legacy import_project 全流程）----

// Import 执行剧本导入：校验 → 解析 → 建档/合并 → 归档 → 落库。
// 返回 ProjectRead + latest_import。
func (s *ProjectService) Import(ctx context.Context, userID string, req ImportRequest) (*ProjectItem, error) {
	normalizedTitle, normalizedPrefix := normalizeImportFields(req.Title, req.ProjectPrefix, req.Filename)
	if normalizedTitle == "" {
		return nil, badRequest400("请填写项目名称或选择带文件名的剧本文件。")
	}
	if normalizedPrefix == "" {
		return nil, badRequest400("请填写项目缩写。")
	}
	if err := ValidateScriptUpload(req.Filename, req.Raw); err != nil {
		return nil, mapScriptUploadError(err)
	}
	scriptText := ""
	parseStatus, parseError := "succeeded", ""
	if len(req.Raw) > 0 {
		text, err := ExtractScriptText(req.Filename, req.Raw)
		if err != nil {
			parseStatus, parseError = "failed", err.Error()
		} else {
			scriptText = text
		}
	}
	if parseError != "" {
		return nil, badRequest400(parseError)
	}
	if len(req.Raw) > 0 && strings.TrimSpace(scriptText) == "" {
		return nil, badRequest400("剧本文件未解析出有效文本，请检查文件内容或格式。")
	}
	if err := ValidateScriptText(scriptText); err != nil {
		return nil, badRequest400(err.Error())
	}
	if len(req.Raw) == 0 {
		return nil, badRequest400("请上传剧本文件后再导入。")
	}
	parsedProjectBrief, err := parseModelQueryBrief(req.ProductionBrief, "production_brief")
	if err != nil {
		return nil, err
	}
	parsedEpisodeBrief, err := parseModelQueryBrief(req.EpisodeBrief, "episode_brief")
	if err != nil {
		return nil, err
	}
	if req.ProjectID == nil && parsedProjectBrief == nil {
		return nil, badRequest400("production_brief is required")
	}

	// 既有项目：合并新分集/版本。
	if req.ProjectID != nil {
		project, err := s.projects.FindByID(ctx, *req.ProjectID)
		if err != nil {
			return nil, err
		}
		if project == nil || project.DeletedAt != nil {
			return nil, notFound404("Project not found")
		}
		if req.ProjectPrefix != nil && project.ProjectPrefix != nil && normalizedPrefix != *project.ProjectPrefix {
			return nil, badRequest400("项目已导入剧本，项目缩写已锁定；新增分集必须沿用现有项目缩写。")
		}
		resolvedEpisodeNo := req.EpisodeNo
		if resolvedEpisodeNo == nil {
			max, err := s.projects.MaxEpisodeNo(ctx, *req.ProjectID)
			if err != nil {
				return nil, err
			}
			n := max + 1
			resolvedEpisodeNo = &n
		}
		if err := ValidateSingleEpisodeScript(scriptText, int(*resolvedEpisodeNo)); err != nil {
			return nil, badRequest400(err.Error())
		}
		existing, err := s.projects.FindEpisodeByNo(ctx, *req.ProjectID, *resolvedEpisodeNo)
		if err != nil {
			return nil, err
		}
		if existing != nil && !req.ConfirmExistingEpisodeVersion {
			return nil, conflict409(fmt.Sprintf("%d 集已存在，请确认后创建新的剧本版本。", *resolvedEpisodeNo))
		}
		importCtx, err := s.runImportTx(ctx, *req.ProjectID, resolvedEpisodeNo, scriptText, req.Filename, parsedProjectBrief, parsedEpisodeBrief, userID, req)
		if err != nil {
			return nil, err
		}
		counts, err := s.projects.CountsDirector(ctx, *req.ProjectID)
		if err != nil {
			return nil, err
		}
		refreshed, err := s.projects.FindByID(ctx, *req.ProjectID)
		if err != nil {
			return nil, err
		}
		item := toProjectItem(refreshed, counts.StoryboardCount, counts.TaskCount)
		item.LatestImport = importCtx
		if err := s.projects.RegisterOperationLog(ctx, *req.ProjectID, userID, "project", *req.ProjectID, "project_script_merged", map[string]any{
			"filename": req.Filename, "script_chars": len(scriptText),
			"parse_version": map[string]any{"parser": ParserName(req.Filename), "status": parseStatus, "error": parseError, "text_chars": len(scriptText)},
			"production_brief": project.ProductionBrief, "episode_brief": parsedEpisodeBrief,
			"episode": importCtx,
		}); err != nil {
			return nil, err
		}
		return &item, nil
	}

	// 新项目：先建档再导入 EP1。
	createdPrefix := normalizedPrefix
	created, err := s.CreateProject(ctx, userID, CreateProjectInput{
		Title:           normalizedTitle,
		Genre:           req.Genre,
		ProjectPrefix:   &createdPrefix,
		ScriptText:      strPtr(""),
		ProductionBrief: toJSONBytes(parsedProjectBrief),
	})
	if err != nil {
		return nil, err
	}
	episodeNo := int32(1)
	if req.EpisodeNo != nil {
		episodeNo = *req.EpisodeNo
	}
	if err := ValidateSingleEpisodeScript(scriptText, int(episodeNo)); err != nil {
		return nil, badRequest400(err.Error())
	}
	effectiveEpisodeBrief := parsedEpisodeBrief
	if effectiveEpisodeBrief == nil {
		effectiveEpisodeBrief = map[string]any{"schema_version": "EpisodeProductionBrief.v1", "target_duration_seconds": 120, "field_sources": map[string]string{}}
	}
	importCtx, err := s.runImportTx(ctx, created.ID, &episodeNo, scriptText, req.Filename, parsedProjectBrief, effectiveEpisodeBrief, userID, req)
	if err != nil {
		return nil, err
	}
	counts, err := s.projects.CountsDirector(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	refreshed, err := s.projects.FindByID(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	item := toProjectItem(refreshed, counts.StoryboardCount, counts.TaskCount)
	item.LatestImport = importCtx
	if err := s.projects.RegisterOperationLog(ctx, created.ID, userID, "project", created.ID, "project_imported", map[string]any{
		"title": item.Title, "project_prefix": item.ProjectPrefix, "production_brief": item.ProductionBrief,
		"episode_brief": effectiveEpisodeBrief,
		"episode":       importCtx,
		"filename":      req.Filename, "script_chars": len(scriptText),
		"parse_version": map[string]any{"parser": ParserName(req.Filename), "status": parseStatus, "error": parseError, "text_chars": len(scriptText)},
	}); err != nil {
		return nil, err
	}
	return &item, nil
}

// runImportTx 归档（503）+ ImportScriptVersion 落库 + latest_import 组装。
// 作用在 s.projects.ImportScriptVersion 的事务语义上；归档失败不落库。
// 归档错误映射对齐 legacy _try_archive_import_file：ValueError→400，其余→503。
func (s *ProjectService) runImportTx(ctx context.Context, projectID string, episodeNo *int32, scriptText, filename string, projectBrief, episodeBrief map[string]any, userID string, req ImportRequest) (*ImportItem, error) {
	var sourceFileID *string
	archived, err := s.archiver.ArchiveImportFile(ctx, projectID, req.Raw, req.Filename, userID)
	if err != nil {
		if st, detail := StatusOf(err); st != 500 {
			return nil, &StatusError{Status: st, Detail: detail}
		}
		return nil, status503("剧本原文件归档失败，未创建正式剧本版本，请稍后重试。")
	}
	if archived != nil {
		sourceFileID = &archived.ID
	}
	var prodBriefRaw, epiBriefRaw json.RawMessage
	if projectBrief != nil {
		b, _ := json.Marshal(projectBrief)
		prodBriefRaw = b
	}
	if episodeBrief != nil {
		b, _ := json.Marshal(episodeBrief)
		epiBriefRaw = b
	}
	result, err := s.projects.ImportScriptVersion(ctx, repository.ImportVersionInput{
		ProjectID:       projectID,
		EpisodeNo:       *episodeNo,
		ScriptText:      scriptText,
		Filename:        filename,
		ParserName:      ParserName(filename),
		Language:        DetectScriptLanguage(scriptText),
		SourceFileID:    sourceFileID,
		UserID:          userID,
		ProductionBrief: prodBriefRaw,
		EpisodeBrief:    epiBriefRaw,
	})
	if err != nil {
		var dup *repository.DuplicateVersionError
		if errors.As(err, &dup) {
			return nil, conflict409(fmt.Sprintf("该分集已存在相同内容的 v%d，未创建重复版本。", dup.VersionNo))
		}
		return nil, err
	}
	if result == nil {
		return nil, notFound404("Project not found")
	}
	return &ImportItem{
		EpisodeID:       result.EpisodeID,
		EpisodeNo:       result.EpisodeNo,
		EpisodeCode:     result.EpisodeCode,
		ScriptID:        result.ScriptID,
		ScriptVersionID: result.ScriptVersionID,
		ScriptVersionNo: result.VersionNo,
		CreatedEpisode:  result.CreatedEpisode,
		CreatedScript:   result.CreatedScript,
		ContentHash:     result.ContentHash,
	}, nil
}

// ---- 工具 ----

func newUUID() (string, error) { return auth.NewUUID() }

// projectNo 对齐 legacy _project_no：RF-YYYYMMDD-HEX6。
func projectNo() (string, error) {
	id, err := auth.NewUUID()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("RF-%s-%s", time.Now().UTC().Format("20060102"), strings.ToUpper(id[:6])), nil
}

func prefixOrNil(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(strings.ToUpper(*v))
	if trimmed == "" {
		return nil
	}
	runes := []rune(trimmed)
	if len(runes) > 5 {
		trimmed = string(runes[:5])
	}
	return &trimmed
}

func rawPtr(raw json.RawMessage) *json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return &raw
}

func strPtr(s string) *string { return &s }

func ptrOr[T any](v *T, fallback T) T {
	if v == nil {
		return fallback
	}
	return *v
}

func equalStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// toJSONBytes 将值转 bytes（nil→"null"）。
func toJSONBytes(v any) []byte {
	if v == nil {
		return []byte(`null`)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte(`null`)
	}
	return raw
}

// parseModelQueryBrief 对齐 legacy _parse_model_query：raw 空→(nil,nil)，
// 非 JSON object→400，随后按契约校验。
func parseModelQueryBrief(raw, field string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, badRequest400(field + " must be valid JSON")
	}
	if parsed == nil {
		return nil, badRequest400(field + " must be a JSON object")
	}
	if field == "production_brief" {
		if err := productionbrief.ValidateProjectBriefMap(parsed); err != nil {
			return nil, badRequest400(field + ": " + err.Error())
		}
	}
	if field == "episode_brief" {
		if err := productionbrief.ValidateEpisodeBriefMap(parsed); err != nil {
			return nil, badRequest400(field + ": " + err.Error())
		}
	}
	return parsed, nil
}

// mapScriptUploadError 400/413 区分（legacy _validate_script_upload）。
func mapScriptUploadError(err error) error {
	switch {
	case errors.Is(err, ErrFileTooLarge):
		return status413(err.Error())
	default:
		return badRequest400(err.Error())
	}
}

// normalizeImportFields 对齐 legacy _normalize_import_project_fields。
func normalizeImportFields(title, projectPrefix *string, filename string) (string, string) {
	normalizedTitle := strings.TrimSpace(ptrOr(title, ""))
	if normalizedTitle == "" {
		normalizedTitle = fileNameStem(filename)
	}
	normalizedPrefix := strings.TrimSpace(strings.ToUpper(ptrOr(projectPrefix, "")))
	if len(normalizedPrefix) > 5 {
		normalizedPrefix = normalizedPrefix[:5]
	}
	if normalizedPrefix == "" {
		normalizedPrefix = autoImportPrefix(normalizedTitle, filename)
	}
	return normalizedTitle, normalizedPrefix
}

// fileNameStem 取文件名去扩展名（fcitx legacy _filename_title）。
func fileNameStem(filename string) string {
	name := strings.TrimSpace(filename)
	if idx := strings.LastIndexAny(name, "/\\"); idx >= 0 {
		name = name[idx+1:]
	}
	if idx := strings.LastIndexByte(name, '.'); idx > 0 {
		name = name[:idx]
	}
	return name
}

// autoImportPrefix 对齐 legacy _auto_import_prefix。
func autoImportPrefix(seed, filename string) string {
	src := seed
	if src == "" {
		src = filename
	}
	var ascii strings.Builder
	for _, ch := range strings.ToUpper(src) {
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			ascii.WriteRune(ch)
		}
	}
	part := ascii.String()
	if part != "" {
		if len(part) > 5 {
			part = part[:5]
		}
		return part
	}
	hash := 0
	for _, ch := range seed {
		hash = (hash*31 + int(ch)) % 10000
	}
	if seed == "" {
		hash = 0
	}
	p := fmt.Sprintf("P%04d", hash)
	return p[:5]
}

// projectProfileContent 对齐 legacy _project_profile_content。
func projectProfileContent(p *model.Projects) map[string]any {
	brief := map[string]any{}
	if p.ProductionBrief != nil {
		_ = json.Unmarshal(*p.ProductionBrief, &brief)
	}
	return map[string]any{"genre": p.Genre, "production_brief": brief}
}
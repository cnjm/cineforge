package repository

// projects 域仓储（对齐 legacy app/services/repositories.py 的单库数据访问）。
// 表：projects / project_episodes / scripts / script_versions / storyboards / tasks / permissions。

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/model"
)

// Projects 基于共享连接池的项目仓储。
type Projects struct {
	pool *pgxpool.Pool
}

// NewProjects 创建 Projects 仓储。
func NewProjects(pool *pgxpool.Pool) *Projects { return &Projects{pool: pool} }

const projectColumns = `id, project_no, project_prefix, name, title, genre, style, status, current_stage,
	manager_id, created_by_id, script_text, production_brief, locked_at, archived_at, deleted_at,
	deleted_by_id, delete_reason, created_at, updated_at`

func scanProject(row pgx.Row) (*model.Projects, error) {
	var p model.Projects
	err := row.Scan(
		&p.ID, &p.ProjectNo, &p.ProjectPrefix, &p.Name, &p.Title, &p.Genre, &p.Style,
		&p.Status, &p.CurrentStage, &p.ManagerId, &p.CreatedById, &p.ScriptText,
		&p.ProductionBrief, &p.LockedAt, &p.ArchivedAt, &p.DeletedAt, &p.DeletedById,
		&p.DeleteReason, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// CardColumns 与 scanProjectCard 用于列表/详情卡片的轻量行（投影必需列）。
const projectCardColumns = `id, project_no, project_prefix, name, title, genre, status, current_stage,
	manager_id, script_text, production_brief, archived_at, deleted_at, created_at, updated_at`

// ProjectCard 项目列表/详情卡片的投影行（嵌入 model.Projects）。
type ProjectCard struct {
	model.Projects
}

func scanProjectCard(row pgx.Row, c *ProjectCard) error {
	return row.Scan(
		&c.ID, &c.ProjectNo, &c.ProjectPrefix, &c.Name, &c.Title, &c.Genre,
		&c.Status, &c.CurrentStage, &c.ManagerId, &c.ScriptText, &c.ProductionBrief,
		&c.ArchivedAt, &c.DeletedAt, &c.CreatedAt, &c.UpdatedAt,
	)
}

// FindByID 按主键查询未删除项目；不存在返回 (nil, nil)。
func (r *Projects) FindByID(ctx context.Context, id string) (*model.Projects, error) {
	p, err := scanProject(r.pool.QueryRow(ctx,
		`SELECT `+projectColumns+` FROM projects WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find project: %w", err)
	}
	return p, nil
}

// ListCards 返回未被软删的项目（可选包含 archived），按 created_at 倒序（对齐 legacy）。
func (r *Projects) ListCards(ctx context.Context, includeArchived bool) ([]ProjectCard, error) {
	q := `SELECT ` + projectCardColumns + ` FROM projects WHERE deleted_at IS NULL`
	if !includeArchived {
		q += ` AND archived_at IS NULL`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list project cards: %w", err)
	}
	defer rows.Close()
	var out []ProjectCard
	for rows.Next() {
		var c ProjectCard
		if err := scanProjectCard(rows, &c); err != nil {
			return nil, fmt.Errorf("scan project card: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project cards: %w", err)
	}
	return out, nil
}

// ListCardsByIDs 按 id 集合取卡片（保持入参顺序），用于非 director 的可见集合。
func (r *Projects) ListCardsByIDs(ctx context.Context, ids []string) ([]ProjectCard, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+projectCardColumns+` FROM projects WHERE id = ANY($1) AND deleted_at IS NULL`, ids)
	if err != nil {
		return nil, fmt.Errorf("list project cards by ids: %w", err)
	}
	defer rows.Close()
	byID := map[string]ProjectCard{}
	for rows.Next() {
		var c ProjectCard
		if err := scanProjectCard(rows, &c); err != nil {
			return nil, fmt.Errorf("scan project card: %w", err)
		}
		byID[c.ID] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate project cards: %w", err)
	}
	out := make([]ProjectCard, 0, len(ids))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// ProjectCounts 聚合项目的 storyboard 数量（全量，未过滤权限）与未退役任务数量。
type ProjectCounts struct {
	StoryboardCount int
	TaskCount       int
}

// CountsDirector 按 director/admin 口径统计：全量 storyboards + 未退役 tasks。
func (r *Projects) CountsDirector(ctx context.Context, projectID string) (*ProjectCounts, error) {
	var out ProjectCounts
	err := r.pool.QueryRow(ctx,
		`SELECT
			(SELECT count(*) FROM storyboards WHERE project_id = $1),
			(SELECT count(*) FROM tasks WHERE project_id = $1 AND is_retired = false)`,
		projectID).Scan(&out.StoryboardCount, &out.TaskCount)
	if err != nil {
		return nil, fmt.Errorf("counts director: %w", err)
	}
	return &out, nil
}

// Create 插入项目。字段按 legacy create_project 落库；id 由调用方生成。
func (r *Projects) Create(ctx context.Context, p *model.Projects) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO projects (
			id, project_no, project_prefix, name, title, genre, status, current_stage,
			manager_id, created_by_id, script_text, production_brief)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		p.ID, p.ProjectNo, p.ProjectPrefix, p.Name, p.Title, p.Genre, p.Status, p.CurrentStage,
		p.ManagerId, p.CreatedById, p.ScriptText, p.ProductionBrief)
	if err != nil {
		return fmt.Errorf("create project: %w", err)
	}
	return nil
}

// ---- 项目角色 / 可见性 ----

// HasProjectPermission 返回 user 是否有该项目角色（project_role:%）。
func (r *Projects) HasProjectPermission(ctx context.Context, userID, projectID string) (bool, error) {
	var one int
	err := r.pool.QueryRow(ctx,
		`SELECT 1 FROM permissions WHERE user_id = $1 AND project_id = $2
		 AND permission_type LIKE '`+ProjectRolePrefix+`%' LIMIT 1`,
		userID, projectID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("has project permission: %w", err)
	}
	return true, nil
}

// ProjectMemberRole 返回 user 在该项目的项目角色（owner/manager/lead/member/viewer）；
// 无角色返回 ""。
func (r *Projects) ProjectMemberRole(ctx context.Context, userID, projectID string) (string, error) {
	var role string
	err := r.pool.QueryRow(ctx,
		`SELECT substr(permission_type, length($1::text) + 1)
		 FROM permissions WHERE user_id = $2 AND project_id = $3
		   AND permission_type LIKE '`+ProjectRolePrefix+`%' LIMIT 1`,
		ProjectRolePrefix, userID, projectID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("project member role: %w", err)
	}
	return role, nil
}

// ProjectRoleIDs 返回 user 有项目角色的项目 id 集合。
func (r *Projects) ProjectRoleIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT project_id FROM permissions WHERE user_id = $1 AND project_id IS NOT NULL
		 AND permission_type LIKE '`+ProjectRolePrefix+`%'`, userID)
	if err != nil {
		return nil, fmt.Errorf("project role ids: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// hasVisibleTaskInProject 返回 user 在项目内是否有「可见任务」（legacy _can_access_project 的可见任务回退分支）。
// SQL 对齐 _visible_task_stmt（非 director）的投影 + project_id 过滤；queryer 让 pool 与 pgx.Tx 都可用。
func hasVisibleTaskInProject(ctx context.Context, q queryer, userID, projectID string) (bool, error) {
	var one int
	err := q.QueryRow(ctx,
		`SELECT 1 FROM tasks WHERE project_id = $2
		 AND is_retired = false
		 AND (asset_id IS NULL OR asset_id NOT IN (SELECT id FROM assets WHERE status IN ('deleted','excluded')))
		 AND variant_plan_id IS NULL
		 AND (
			project_id IN (
				SELECT project_id FROM permissions WHERE user_id = $1 AND project_id IS NOT NULL
				 AND permission_type LIKE '`+ProjectRolePrefix+`%')
			OR (
				(assignee_id = $1)
				AND (status <> 'completed' OR visible_until IS NULL OR visible_until >= now())
			)
		 ) LIMIT 1`,
		userID, projectID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("has visible task in project: %w", err)
	}
	return true, nil
}

// HasVisibleTaskInProject 返回 user 在项目内是否有「可见任务」。
func (r *Projects) HasVisibleTaskInProject(ctx context.Context, userID, projectID string) (bool, error) {
	return hasVisibleTaskInProject(ctx, r.pool, userID, projectID)
}

// VisibleTaskProjectIDs 返回 user 可见任务所在的项目 id 集合。
// SQL 对齐 legacy _visible_task_stmt（非 director）的投影。
func (r *Projects) VisibleTaskProjectIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT project_id FROM tasks
		 WHERE is_retired = false
		   AND (asset_id IS NULL OR asset_id NOT IN (SELECT id FROM assets WHERE status IN ('deleted','excluded')))
		   AND variant_plan_id IS NULL
		 AND (
			project_id IN (
				SELECT project_id FROM permissions WHERE user_id = $1 AND project_id IS NOT NULL
				 AND permission_type LIKE '`+ProjectRolePrefix+`%')
			OR (
				(assignee_id = $1)
				AND (status <> 'completed' OR visible_until IS NULL OR visible_until >= now())
			)
		 )`, userID)
	if err != nil {
		return nil, fmt.Errorf("visible task projects: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// VisibleProjectIDs 合并 项目角色 ∪ 可见任务所在项目（legacy _visible_project_ids）。
func (r *Projects) VisibleProjectIDs(ctx context.Context, userID string) ([]string, error) {
	roleIDs, err := r.ProjectRoleIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	taskIDs, err := r.VisibleTaskProjectIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(roleIDs)+len(taskIDs))
	for _, ids := range [][]string{roleIDs, taskIDs} {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// ---- 分集 / 剧本 ----

const episodeColumns = `id, project_id, episode_no, episode_code, title, summary, production_brief, created_at, updated_at`

func scanEpisode(row pgx.Row) (*model.ProjectEpisodes, error) {
	var e model.ProjectEpisodes
	err := row.Scan(&e.ID, &e.ProjectId, &e.EpisodeNo, &e.EpisodeCode, &e.Title, &e.Summary,
		&e.ProductionBrief, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// FindEpisodeByID 按主键查询分集；不存在返回 (nil, nil)。
func (r *Projects) FindEpisodeByID(ctx context.Context, id string) (*model.ProjectEpisodes, error) {
	e, err := scanEpisode(r.pool.QueryRow(ctx,
		`SELECT `+episodeColumns+` FROM project_episodes WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find episode: %w", err)
	}
	return e, nil
}

// EpisodeCount 返回项目分集总数。
func (r *Projects) EpisodeCount(ctx context.Context, projectID string) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM project_episodes WHERE project_id = $1`, projectID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count episodes: %w", err)
	}
	return n, nil
}

// FindEpisodeByNo 按项目内分集号查询；不存在返回 (nil, nil)。
func (r *Projects) FindEpisodeByNo(ctx context.Context, projectID string, episodeNo int32) (*model.ProjectEpisodes, error) {
	e, err := scanEpisode(r.pool.QueryRow(ctx,
		`SELECT `+episodeColumns+` FROM project_episodes WHERE project_id = $1 AND episode_no = $2`,
		projectID, episodeNo))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find episode by no: %w", err)
	}
	return e, nil
}

// MaxEpisodeNo 返回项目内最大 episode_no（无分集时 0）。
func (r *Projects) MaxEpisodeNo(ctx context.Context, projectID string) (int32, error) {
	var n int32
	if err := r.pool.QueryRow(ctx,
		`SELECT coalesce(max(episode_no), 0) FROM project_episodes WHERE project_id = $1`,
		projectID).Scan(&n); err != nil {
		return 0, fmt.Errorf("max episode no: %w", err)
	}
	return n, nil
}

// ListEpisodes 按 episode_no 升序列出分集。
func (r *Projects) ListEpisodes(ctx context.Context, projectID string) ([]model.ProjectEpisodes, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+episodeColumns+` FROM project_episodes WHERE project_id = $1 ORDER BY episode_no`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list episodes: %w", err)
	}
	defer rows.Close()
	var out []model.ProjectEpisodes
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, fmt.Errorf("scan episode: %w", err)
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateEpisode 插入分集。
func (r *Projects) CreateEpisode(ctx context.Context, e *model.ProjectEpisodes) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO project_episodes (id, project_id, episode_no, episode_code, title, summary, production_brief)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		e.ID, e.ProjectId, e.EpisodeNo, e.EpisodeCode, e.Title, e.Summary, e.ProductionBrief)
	if err != nil {
		return fmt.Errorf("create episode: %w", err)
	}
	return nil
}

// ---- Scripts / ScriptVersions ----

const scriptColumns = `id, project_id, episode_id, script_code, title, content, status, current_version_id, created_by, created_at, updated_at`

func scanScript(row pgx.Row) (*model.Scripts, error) {
	var s model.Scripts
	err := row.Scan(&s.ID, &s.ProjectId, &s.EpisodeId, &s.ScriptCode, &s.Title, &s.Content,
		&s.Status, &s.CurrentVersionId, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// FindScriptForEpisode 取分集关联的第一个 Script（按 created_at 升序，对齐 legacy）。
func (r *Projects) FindScriptForEpisode(ctx context.Context, projectID, episodeID string) (*model.Scripts, error) {
	s, err := scanScript(r.pool.QueryRow(ctx,
		`SELECT `+scriptColumns+` FROM scripts
		 WHERE project_id = $1 AND episode_id = $2 ORDER BY created_at LIMIT 1`,
		projectID, episodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find script for episode: %w", err)
	}
	return s, nil
}

// ScriptCount 返回项目剧本数。
func (r *Projects) ScriptCount(ctx context.Context, projectID string) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM scripts WHERE project_id = $1`, projectID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count scripts: %w", err)
	}
	return n, nil
}

// CreateScript 插入剧本；id 由调用方生成。
func (r *Projects) CreateScript(ctx context.Context, s *model.Scripts) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO scripts (id, project_id, episode_id, script_code, title, content, status, current_version_id, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		s.ID, s.ProjectId, s.EpisodeId, s.ScriptCode, s.Title, s.Content, s.Status,
		s.CurrentVersionId, s.CreatedBy)
	if err != nil {
		return fmt.Errorf("create script: %w", err)
	}
	return nil
}

// UpdateScriptCurrentVersion 更新剧本 current_version_id + content + updated_at。
func (r *Projects) UpdateScriptCurrentVersion(ctx context.Context, scriptID, versionID, content string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE scripts SET current_version_id = $2, content = $3, updated_at = now() WHERE id = $1`,
		scriptID, versionID, content)
	if err != nil {
		return fmt.Errorf("update script current version: %w", err)
	}
	return nil
}

const scriptVersionColumns = `id, script_id, version_no, source, content, content_hash, source_file_id,
	original_filename, parser_name, language, source_agent_run_id, created_by, created_at, updated_at`

func scanScriptVersion(row pgx.Row) (*model.ScriptVersions, error) {
	var v model.ScriptVersions
	err := row.Scan(&v.ID, &v.ScriptId, &v.VersionNo, &v.Source, &v.Content, &v.ContentHash,
		&v.SourceFileId, &v.OriginalFilename, &v.ParserName, &v.Language, &v.SourceAgentRunId,
		&v.CreatedBy, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// FindScriptVersionByID 按主键查询剧本版本；不存在返回 (nil, nil)。
func (r *Projects) FindScriptVersionByID(ctx context.Context, id string) (*model.ScriptVersions, error) {
	v, err := scanScriptVersion(r.pool.QueryRow(ctx,
		`SELECT `+scriptVersionColumns+` FROM script_versions WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find script version: %w", err)
	}
	return v, nil
}

// FindScriptVersionByHash 按脚本+内容 hash 查版本（幂等去重）；不存在返回 (nil, nil)。
func (r *Projects) FindScriptVersionByHash(ctx context.Context, scriptID, contentHash string) (*model.ScriptVersions, error) {
	v, err := scanScriptVersion(r.pool.QueryRow(ctx,
		`SELECT `+scriptVersionColumns+` FROM script_versions
		 WHERE script_id = $1 AND content_hash = $2 ORDER BY version_no DESC LIMIT 1`,
		scriptID, contentHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find script version by hash: %w", err)
	}
	return v, nil
}

// MaxScriptVersionNo 返回剧本当前最大版本号（无版本时 0）。
func (r *Projects) MaxScriptVersionNo(ctx context.Context, scriptID string) (int32, error) {
	var n int32
	if err := r.pool.QueryRow(ctx,
		`SELECT coalesce(max(version_no), 0) FROM script_versions WHERE script_id = $1`,
		scriptID).Scan(&n); err != nil {
		return 0, fmt.Errorf("max script version no: %w", err)
	}
	return n, nil
}

// CountScriptVersions 返回剧本版本数。
func (r *Projects) CountScriptVersions(ctx context.Context, scriptID string) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM script_versions WHERE script_id = $1`, scriptID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count script versions: %w", err)
	}
	return n, nil
}

// ListScriptVersions 按 version_no 倒序列出剧本版本。
func (r *Projects) ListScriptVersions(ctx context.Context, scriptID string) ([]model.ScriptVersions, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+scriptVersionColumns+` FROM script_versions WHERE script_id = $1 ORDER BY version_no DESC`,
		scriptID)
	if err != nil {
		return nil, fmt.Errorf("list script versions: %w", err)
	}
	defer rows.Close()
	var out []model.ScriptVersions
	for rows.Next() {
		v, err := scanScriptVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan script version: %w", err)
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateScriptVersion 插入剧本版本。
func (r *Projects) CreateScriptVersion(ctx context.Context, v *model.ScriptVersions) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO script_versions (
			id, script_id, version_no, source, content, content_hash, source_file_id,
			original_filename, parser_name, language, source_agent_run_id, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		v.ID, v.ScriptId, v.VersionNo, v.Source, v.Content, v.ContentHash, v.SourceFileId,
		v.OriginalFilename, v.ParserName, v.Language, v.SourceAgentRunId, v.CreatedBy)
	if err != nil {
		return fmt.Errorf("create script version: %w", err)
	}
	return nil
}
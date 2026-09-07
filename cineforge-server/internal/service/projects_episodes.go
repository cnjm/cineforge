package service

// 分集列表/详情、8 层 breakdown_status、dashboard 聚合（对齐 legacy）。
// 分集状态的 stale_stages 判定将 reading revision 的 input_snapshot 哈希与当前
// project/episode 简报做比对；脚本段/分镜/任务仅做存在性检查（P3d 读侧）。

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"cineforge/server/internal/model"
	"cineforge/server/internal/productionbrief"
	"cineforge/server/internal/repository"
)

// EpisodeItem 对齐 ProjectEpisodeRead。
type EpisodeItem struct {
	ID                       string          `json:"id"`
	ProjectID                string          `json:"project_id"`
	EpisodeNo                int32           `json:"episode_no"`
	EpisodeCode              string          `json:"episode_code"`
	Title                    *string         `json:"title"`
	Summary                  *string         `json:"summary"`
	BreakdownStatus          string          `json:"breakdown_status"`
	StaleStages              []string        `json:"stale_stages"`
	ScriptID                 *string         `json:"script_id"`
	CurrentVersionID         *string         `json:"current_version_id"`
	CurrentVersionNo         *int32          `json:"current_version_no"`
	VersionCount             int             `json:"version_count"`
	CurrentContentHash       *string         `json:"current_content_hash"`
	CurrentOriginalFilename  *string         `json:"current_original_filename"`
	ProductionBrief          json.RawMessage `json:"production_brief"`
	UpdatedAt                time.Time       `json:"updated_at"`
}

// ScriptVersionSummaryItem 对齐 ScriptVersionSummary。
type ScriptVersionSummaryItem struct {
	ID               string    `json:"id"`
	VersionNo        int32     `json:"version_no"`
	ContentHash      *string   `json:"content_hash"`
	OriginalFilename *string   `json:"original_filename"`
	ParserName       *string   `json:"parser_name"`
	Language         *string   `json:"language"`
	CreatedAt        time.Time `json:"created_at"`
	IsCurrent        bool      `json:"is_current"`
}

// ScriptVersionDetailItem 对齐 ScriptVersionDetail。
type ScriptVersionDetailItem struct {
	ScriptVersionSummaryItem
	ScriptID   string `json:"script_id"`
	ScriptText string `json:"script_text"`
}

// EpisodeDetailItem 对齐 ProjectEpisodeDetail。
type EpisodeDetailItem struct {
	EpisodeItem
	Versions []ScriptVersionSummaryItem `json:"versions"`
	Assets   []any                      `json:"assets"`
}

// DashboardSummaryItem 对齐 DashboardSummary。
type DashboardSummaryItem struct {
	ProjectID          string   `json:"project_id"`
	StoryboardCount    int      `json:"storyboard_count"`
	AssetCount         int      `json:"asset_count"`
	TaskCount          int      `json:"task_count"`
	CompletedTaskCount int      `json:"completed_task_count"`
	OverdueTaskCount   int      `json:"overdue_task_count"`
	CompletionRate     float64  `json:"completion_rate"`
	RecentActivity     []string `json:"recent_activity"`
}

// ListEpisodes 列出项目分集（含 breakdown_status / stale_stages）。
func (s *ProjectService) ListEpisodes(ctx context.Context, userID, role, projectID string) ([]EpisodeItem, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	ok, err := s.canRead(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	episodes, err := s.projects.ListEpisodes(ctx, projectID)
	if err != nil {
		return nil, err
	}
	view := s.latestBreakdownView(ctx, projectID)
	out := make([]EpisodeItem, 0, len(episodes))
	for _, ep := range episodes {
		item, err := s.buildEpisodeItem(ctx, project, &ep, view)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, nil
}

// GetEpisodeDetail 分集详情（版本列表 + 空资产绑定）。
func (s *ProjectService) GetEpisodeDetail(ctx context.Context, userID, role, projectID, episodeID string) (*EpisodeDetailItem, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	ok, err := s.canRead(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	ep, err := s.projects.FindEpisodeByID(ctx, episodeID)
	if err != nil {
		return nil, err
	}
	// 即便项目存在，分集页面对 404 文案为 "Episode not found"。
	if ep == nil || ep.ProjectId != projectID {
		return nil, notFound404("Episode not found")
	}
	view := s.latestBreakdownView(ctx, projectID)
	item, err := s.buildEpisodeItem(ctx, project, ep, view)
	if err != nil {
		return nil, err
	}
	detail := &EpisodeDetailItem{EpisodeItem: *item, Assets: []any{}}
	script, err := s.projects.FindScriptForEpisode(ctx, projectID, ep.ID)
	if err != nil {
		return nil, err
	}
	if script == nil {
		return detail, nil
	}
	versions, err := s.projects.ListScriptVersions(ctx, script.ID)
	if err != nil {
		return nil, err
	}
	detail.Versions = make([]ScriptVersionSummaryItem, 0, len(versions))
	seenCurrent := false
	for _, v := range versions {
		sv := ScriptVersionSummaryItem{
			ID: v.ID, VersionNo: v.VersionNo, ContentHash: v.ContentHash,
			OriginalFilename: v.OriginalFilename, ParserName: v.ParserName, Language: v.Language,
			CreatedAt: v.CreatedAt,
		}
		if script.CurrentVersionId != nil && v.ID == *script.CurrentVersionId {
			sv.IsCurrent = true
			seenCurrent = true
		}
		detail.Versions = append(detail.Versions, sv)
	}
	// 缺省第一个版本标记为当前（对齐 legacy 兜底）。
	if !seenCurrent && len(detail.Versions) > 0 {
		detail.Versions[0].IsCurrent = true
	}
	return detail, nil
}

// GetScriptVersionDetail 成品版本查看（分集详情内 versions/{id}）。
func (s *ProjectService) GetScriptVersionDetail(ctx context.Context, userID, role, projectID, episodeID, versionID string) (*ScriptVersionDetailItem, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	ok, err := s.canRead(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	ep, err := s.projects.FindEpisodeByID(ctx, episodeID)
	if err != nil {
		return nil, err
	}
	if ep == nil || ep.ProjectId != projectID {
		return nil, notFound404("Episode not found")
	}
	script, err := s.projects.FindScriptForEpisode(ctx, projectID, ep.ID)
	if err != nil {
		return nil, err
	}
	if script == nil {
		return nil, notFound404("Script version not found")
	}
	v, err := s.projects.FindScriptVersionByID(ctx, versionID)
	if err != nil {
		return nil, err
	}
	if v == nil || v.ScriptId != script.ID {
		return nil, notFound404("Script version not found")
	}
	isCurrent := script.CurrentVersionId != nil && *script.CurrentVersionId == v.ID
	return &ScriptVersionDetailItem{
		ScriptVersionSummaryItem: ScriptVersionSummaryItem{
			ID: v.ID, VersionNo: v.VersionNo, ContentHash: v.ContentHash,
			OriginalFilename: v.OriginalFilename, ParserName: v.ParserName, Language: v.Language,
			CreatedAt: v.CreatedAt, IsCurrent: isCurrent,
		},
		ScriptID:   script.ID,
		ScriptText: v.Content,
	}, nil
}

// UpdateEpisodeBrief 更新分集制作简报（director/admin；返回校验后的简报 JSON）。
func (s *ProjectService) UpdateEpisodeBrief(ctx context.Context, userID, role, projectID, episodeID string, raw json.RawMessage) (json.RawMessage, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	if !isDirectorOrAdminRole(role) {
		return nil, forbidden403("Project permission denied")
	}
	ep, err := s.projects.FindEpisodeByID(ctx, episodeID)
	if err != nil {
		return nil, err
	}
	if ep == nil || ep.ProjectId != projectID {
		return nil, notFound404("Episode not found")
	}
	parsed, err := parseEpisodeBriefJSON(raw)
	if err != nil {
		return nil, badRequest400(err.Error())
	}
	if err := s.projects.UpdateEpisodeProductionBrief(ctx, episodeID, parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

// Dashboard 项目健康度聚合（对齐 legacy get_dashboard；redis 缓存 P3g 接入）。
func (s *ProjectService) Dashboard(ctx context.Context, userID, role, projectID string) (*DashboardSummaryItem, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	ok, err := s.canRead(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	counts, err := s.projects.DashboardCounts(ctx, projectID, userID, isDirectorOrAdminRole(role))
	if err != nil {
		return nil, err
	}
	return &DashboardSummaryItem{
		ProjectID:          projectID,
		StoryboardCount:    counts.StoryboardCount,
		AssetCount:         counts.AssetCount,
		TaskCount:          counts.TaskCount,
		CompletedTaskCount: counts.CompletedCount,
		OverdueTaskCount:   counts.OverdueCount,
		CompletionRate:     counts.CompletionRate,
		RecentActivity:     []string{"PostgreSQL 项目数据已启用", "DeepSeek Agent 提示词链路已接入"},
	}, nil
}

// ---- 分集状态判定 ----

// buildEpisodeItem 组装单个分集读模型（含状态/版本/过期判定）。
func (s *ProjectService) buildEpisodeItem(ctx context.Context, project *model.Projects, ep *model.ProjectEpisodes, view map[string]any) (*EpisodeItem, error) {
	var versionCount int
	var scriptID *string
	var currentVersionID *string
	var currentVersionNo *int32
	var currentHash, currentFilename *string
	script, err := s.projects.FindScriptForEpisode(ctx, project.ID, ep.ID)
	if err != nil {
		return nil, err
	}
	if script != nil {
		scriptID = &script.ID
		currentVersionID = script.CurrentVersionId
		count, err := s.projects.CountScriptVersions(ctx, script.ID)
		if err != nil {
			return nil, err
		}
		versionCount = count
		if script.CurrentVersionId != nil {
			if v, err := s.projects.FindScriptVersionByID(ctx, *script.CurrentVersionId); err != nil {
				return nil, err
			} else if v != nil {
				currentVersionNo = &v.VersionNo
				currentHash = v.ContentHash
				currentFilename = v.OriginalFilename
			}
		}
	}
	var readingRev *model.ArtifactRevisions
	if currentVersionID != nil {
		rev, err := s.projects.ReadingConfirmedRevision(ctx, project.ID, ep.ID, currentVersionID)
		if err != nil {
			return nil, err
		}
		readingRev = rev
	}
	hasSegments, err := s.projects.HasScriptSegments(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	hasStoryboards, err := s.projects.HasStoryboards(ctx, project.ID, ep.EpisodeNo)
	if err != nil {
		return nil, err
	}
	hasTasks, err := s.projects.HasActiveTasks(ctx, ep.ID)
	if err != nil {
		return nil, err
	}
	staleStages := episodeStaleStages(project, readingRev, ep)

	return &EpisodeItem{
		ID:                      ep.ID,
		ProjectID:               ep.ProjectId,
		EpisodeNo:               ep.EpisodeNo,
		EpisodeCode:             ep.EpisodeCode,
		Title:                   ep.Title,
		Summary:                 readingSummary(readingRev, ep.Summary),
		BreakdownStatus:         episodeBreakdownStatus(readingRev, view, hasSegments, hasStoryboards, hasTasks, staleStages),
		StaleStages:             staleStages,
		ScriptID:                scriptID,
		CurrentVersionID:        currentVersionID,
		CurrentVersionNo:        currentVersionNo,
		VersionCount:            versionCount,
		CurrentContentHash:      currentHash,
		CurrentOriginalFilename: currentFilename,
		ProductionBrief:         briefOrEmpty(ep.ProductionBrief),
		UpdatedAt:               ep.UpdatedAt,
	}, nil
}

// latestBreakdownView 取项目最新拆解视图（content_json 失败→零值 map）。
func (s *ProjectService) latestBreakdownView(ctx context.Context, projectID string) map[string]any {
	b, err := s.projects.LatestBreakdown(ctx, projectID)
	if err != nil || b == nil {
		return map[string]any{}
	}
	var view map[string]any
	if err := json.Unmarshal(b.ContentJson, &view); err != nil {
		return map[string]any{}
	}
	return view
}

// episodeBreakdownStatus 8 层状态判定（对齐 legacy 2899-2927）。
func episodeBreakdownStatus(readingRev *model.ArtifactRevisions, view map[string]any, hasSegments, hasStoryboards, hasTasks bool, staleStages []string) string {
	status := "script_imported"
	readingState, _ := view["reading_review_state"].(map[string]any)
	_, hasReport := view["reading_report"].(map[string]any)
	if readingRev != nil || (stringOf(readingState, "status") == "confirmed" && hasReport) {
		status = "reading_confirmed"
	}
	for _, key := range []string{"asset_prompt_designs", "costume_designs", "asset_masters", "assets"} {
		if anyOf(view, key) {
			if stringOf(view, "source_label") == "human_modified" || stringOf(view, "source_label") == "human_confirmed" {
				status = "asset_saved"
			} else {
				status = "asset_draft"
			}
			break
		}
	}
	if hasSegments {
		status = "breakdown_confirmed"
	}
	if hasStoryboards {
		status = "storyboard_ready"
	}
	if hasTasks {
		status = "production"
	}
	if len(staleStages) > 0 {
		status = "outdated"
	}
	return status
}

// episodeStaleStages 对齐 legacy 2930-2949（profile 哈希比对；episode brief 变→storyboards/tasks）。
func episodeStaleStages(project *model.Projects, revision *model.ArtifactRevisions, ep *model.ProjectEpisodes) []string {
	if revision == nil {
		return nil
	}
	snapshot := map[string]any{}
	_ = json.Unmarshal(revision.InputSnapshot, &snapshot)
	if stringOf(snapshot, "project_profile_hash") == "" {
		return nil
	}
	current := projectProfileHashes(project)
	var stages []string
	if h := stringOf(snapshot, "content_type_hash"); h != "" && h != current["content_type_hash"] {
		stages = append(stages, "reading", "breakdown", "assets", "storyboards", "tasks")
	}
	if h := stringOf(snapshot, "cultural_contexts_hash"); h != "" && h != current["cultural_contexts_hash"] {
		stages = append(stages, "reading", "breakdown", "assets", "storyboards", "tasks")
	}
	if h := stringOf(snapshot, "delivery_aspect_ratio_hash"); h != "" && h != current["delivery_aspect_ratio_hash"] {
		stages = append(stages, "storyboards", "tasks")
	}
	if h := stringOf(snapshot, "primary_style_hash"); h != "" && h != current["primary_style_hash"] {
		stages = append(stages, "assets", "storyboards", "tasks")
	}
	if h := stringOf(snapshot, "production_brief_hash"); h == "" {
		if stringOf(snapshot, "genre_hash") != current["genre_hash"] {
			stages = append(stages, "assets", "storyboards", "tasks")
		}
	}
	if h := stringOf(snapshot, "episode_production_brief_hash"); h != "" {
		currentEpisodeHash := repository.ContentHashString(briefOrEmpty(ep.ProductionBrief))
		if currentEpisodeHash != h {
			stages = append(stages, "storyboards", "tasks")
		}
	}
	return dedupe(stages)
}

// projectProfileHashes 对齐 legacy _project_profile_hashes。
func projectProfileHashes(project *model.Projects) map[string]string {
	brief := map[string]any{}
	if project.ProductionBrief != nil {
		_ = json.Unmarshal(*project.ProductionBrief, &brief)
	}
	content := map[string]any{"genre": project.Genre, "production_brief": brief}
	return map[string]string{
		"project_profile_hash":    repository.ContentHashString(content),
		"genre_hash":              repository.ContentHashString(map[string]any{"genre": project.Genre}),
		"production_brief_hash":   repository.ContentHashString(brief),
		"content_type_hash":       repository.ContentHashString(brief["content_type"]),
		"delivery_aspect_ratio_hash": repository.ContentHashString(brief["delivery_aspect_ratio"]),
		"primary_style_hash": repository.ContentHashString(map[string]any{
			"primary_style_id": brief["primary_style_id"], "style_catalog_version": brief["style_catalog_version"],
		}),
		"cultural_contexts_hash": repository.ContentHashString(map[string]any{
			"cultural_contexts": brief["cultural_contexts"], "primary_cultural_context_code": brief["primary_cultural_context_code"],
		}),
	}
}

// readingSummary 对齐 legacy _reading_summary（先记忆化 summary，再 synopsis→logline；+conflict）。
func readingSummary(readingRev *model.ArtifactRevisions, stored *string) *string {
	if stored != nil {
		trimmed := trimmed(stored)
		if trimmed != "" && trimmed != "由 projects.script_text 回填" {
			return &trimmed
		}
	}
	if readingRev == nil {
		return nil
	}
	report := map[string]any{}
	_ = json.Unmarshal(readingRev.NormalizedContent, &report)
	return readingSummaryFromMap(report)
}

// readingSummaryFromMap 对齐 legacy _reading_summary（围读确认落库时复用）。
func readingSummaryFromMap(report map[string]any) *string {
	overview, _ := report["story_overview"].(map[string]any)
	if overview == nil {
		return nil
	}
	if synopsis := trimmedString(overview, "synopsis"); synopsis != "" {
		out := runeCut(synopsis, 2000)
		return &out
	}
	logline := trimmedString(overview, "logline")
	conflict := trimmedString(overview, "core_conflict")
	if logline != "" && conflict != "" && !strings.Contains(logline, conflict) {
		out := runeCut(logline+"；"+conflict, 500)
		return &out
	}
	combined := logline
	if combined == "" {
		combined = conflict
	}
	if combined != "" {
		out := runeCut(combined, 500)
		return &out
	}
	return nil
}

// parseEpisodeBriefJSON 校验 + 序列化 EpisodeProductionBrief。
func parseEpisodeBriefJSON(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, badRequest400("production_brief must be a JSON object")
	}
	if err := productionbriefValidateEpisodeMap(m); err != nil {
		return nil, err
	}
	out, _ := json.Marshal(m)
	return out, nil
}

// ---- 内部工具 ----

func stringOf(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

func anyOf(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case []any:
		return len(t) > 0
	case map[string]any:
		return true
	case string:
		return t != ""
	default:
		return v != nil
	}
}

func trimmedString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	name, _ := m[key].(string)
	return strings.TrimSpace(name)
}

func trimmed(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// runeCut 按 rune 截断（对齐 Python 子串语义）。
func runeCut(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func productionbriefValidateEpisodeMap(m map[string]any) error {
	raw, _ := json.Marshal(m)
	if _, err := productionbrief.ParseEpisodeBrief(raw); err != nil {
		return badRequest400(err.Error())
	}
	return nil
}

// ---- Agent runs（P3d 读侧：提交后轮询 + 列表）----

// AgentRunSummaryItem 对齐 AgentRunSummaryRead（列表与 run-events 快照共用；P4d 补齐 lineage 字段）。
type AgentRunSummaryItem struct {
	ID               string         `json:"id"`
	ProjectID        *string        `json:"project_id"`
	ParentRunID      *string        `json:"parent_run_id"`
	StoryboardID     *string        `json:"storyboard_id"`
	TaskID           *string        `json:"task_id"`
	AgentType        string         `json:"agent_type"`
	Status           string         `json:"status"`
	ErrorMessage     *string        `json:"error_message"`
	DurationMs       *int32         `json:"duration_ms"`
	Model            *string        `json:"model"`
	SkillName        *string        `json:"skill_name"`
	SkillVersion     *string        `json:"skill_version"`
	ContractVersion  *string        `json:"contract_version"`
	ContractHash     *string        `json:"contract_hash"`
	PromptVersion    *string        `json:"prompt_version"`
	InputHash        *string        `json:"input_hash"`
	NodeKey          *string        `json:"node_key"`
	WorkflowRunID    *string        `json:"workflow_run_id"`
	EpisodeID        *string        `json:"episode_id"`
	EpisodeCode      *string        `json:"episode_code"`
	ScriptVersionID  *string        `json:"script_version_id"`
	Summary          map[string]any `json:"summary"`
	PayloadRef       *string        `json:"payload_ref"`
	PayloadSizeBytes *int32         `json:"payload_size_bytes"`
	PayloadSha256    *string        `json:"payload_sha256"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// AgentRunPageItem 对齐 AgentRunPage。
type AgentRunPageItem struct {
	Items      []AgentRunSummaryItem `json:"items"`
	NextCursor *string               `json:"next_cursor"`
	HasMore    bool                  `json:"has_more"`
	Limit      int                   `json:"limit"`
}

func toAgentRunSummary(run *model.AgentRuns) AgentRunSummaryItem {
	summary := jsonMap(run.SummaryJson)
	item := AgentRunSummaryItem{
		ID: run.ID, ProjectID: run.ProjectId, ParentRunID: run.ParentRunId,
		StoryboardID: run.StoryboardId, TaskID: run.TaskId, AgentType: run.AgentType,
		Status: run.Status, ErrorMessage: run.ErrorMessage, DurationMs: run.DurationMs,
		Model: run.Model, SkillName: run.SkillName, SkillVersion: run.SkillVersion,
		ContractVersion: run.ContractVersion, ContractHash: run.ContractHash,
		PromptVersion: run.PromptVersion, InputHash: run.InputHash, NodeKey: run.NodeKey,
		WorkflowRunID:    run.WorkflowRunId,
		EpisodeID:        anyString(summary["episode_id"]),
		EpisodeCode:      anyString(summary["episode_code"]),
		ScriptVersionID:  anyString(summary["script_version_id"]),
		Summary:          summary,
		PayloadRef:       run.PayloadRef,
		PayloadSizeBytes: run.PayloadSizeBytes,
		PayloadSha256:    run.PayloadSha256,
		CreatedAt:        run.CreatedAt,
		UpdatedAt:        run.UpdatedAt,
	}
	if item.WorkflowRunID == nil {
		item.WorkflowRunID = anyString(summary["workflow_run_id"])
	}
	return item
}

// ListAgentRuns 对齐 legacy list_agent_run_page（任意登录用户可读，不做项目级过滤）。
func (s *ProjectService) ListAgentRuns(ctx context.Context, projectID *string, limit int, cursor string) (*AgentRunPageItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	result, err := s.projects.ListAgentRunPage(ctx, repository.AgentRunPageSpec{
		ProjectID: projectID, Limit: limit, Cursor: cursor,
	})
	if err != nil {
		return nil, err
	}
	out := &AgentRunPageItem{Items: []AgentRunSummaryItem{}, HasMore: result.HasMore, NextCursor: result.NextCursor, Limit: limit}
	for i := range result.Rows {
		out.Items = append(out.Items, toAgentRunSummary(&result.Rows[i]))
	}
	return out, nil
}

// GetAgentRunForUser 对齐 legacy get_agent_run_for_user（project 级读权限；task 级 P3e 补）。
func (s *ProjectService) GetAgentRunForUser(ctx context.Context, userID, role, runID string) (*AgentRunItem, error) {
	run, err := s.projects.GetAgentRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, notFound404("Agent run not found")
	}
	if run.ProjectId != nil {
		ok, err := s.canRead(ctx, userID, role, *run.ProjectId)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, forbidden403("Agent run permission denied")
		}
	} else if !isDirectorOrAdminRole(role) {
		return nil, forbidden403("Agent run permission denied")
	}
	return toAgentRunItem(run), nil
}

// ListProjectScriptSegments 对齐 legacy GET /projects/{project_id}/script-segments
// （optional episode_id 过滤；任意登录用户可读，需项目读权限）。
func (s *ProjectService) ListProjectScriptSegments(ctx context.Context, userID, role, projectID string, episodeID *string) ([]map[string]any, error) {
	project, err := s.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project == nil || project.DeletedAt != nil {
		return nil, notFound404("Project not found")
	}
	ok, err := s.canRead(ctx, userID, role, projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, forbidden403("Project permission denied")
	}
	rows, err := s.projects.ListScriptSegments(ctx, projectID, episodeID, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for i := range rows {
		out = append(out, scriptSegmentPublic(&rows[i]))
	}
	return out, nil
}

// ProductionBriefCatalog 组装 GET /api/production-brief/catalog（brief catalog + styles 透传）。
func ProductionBriefCatalog() (json.RawMessage, error) {
	brief := productionbrief.BriefCatalogJSON()
	styles := productionbrief.StyleCatalogJSON()
	var catalog map[string]any
	if err := json.Unmarshal(brief, &catalog); err != nil {
		return nil, err
	}
	var styleCatalog productionbrief.StyleCatalog
	if err := json.Unmarshal(styles, &styleCatalog); err != nil {
		return nil, err
	}
	catalog["styles"] = styleCatalog.PrimaryStyles
	catalog["style_catalog_version"] = styleCatalog.CatalogVersion
	raw, err := json.Marshal(catalog)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
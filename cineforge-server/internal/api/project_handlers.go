package api

// P3d 项目域 handlers（对齐 legacy app/api/routes/{projects, production_brief}.py +
// agents.py 的 agent-runs 子路由）。
// 契约约定：KeyError→404、PermissionError→403、ValueError→400、HTTPException 逐字 detail。

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"cineforge/server/internal/service"
)

func (s *Server) registerProjects(g *gin.RouterGroup) {
	projects := g.Group("/projects", s.requireAuth())
	projects.GET("", s.handleListProjects)
	projects.POST("", s.requireDirectorOrAdmin(), s.handleCreateProject)
	projects.POST("/import", s.requireDirectorOrAdmin(), s.handleImportProject)
	projects.GET("/:project_id", s.handleGetProject)
	projects.PATCH("/:project_id", s.requireDirectorOrAdmin(), s.handleUpdateProject)
	projects.POST("/:project_id/soft-delete", s.requireDirectorOrAdmin(), s.handleSoftDeleteProject)
	projects.DELETE("/:project_id/episodes/:episode_id", s.requireDirectorOrAdmin(), s.handleDeleteProjectEpisode)
	projects.POST("/:project_id/script-segments/confirm", s.requireDirectorOrAdmin(), s.handleConfirmScriptSegments)
	projects.POST("/:project_id/breakdowns/lock", s.requireDirectorOrAdmin(), s.handleLockBreakdown)
	projects.GET("/:project_id/episodes", s.handleListEpisodes)
	projects.GET("/:project_id/episodes/:episode_id", s.handleGetEpisodeDetail)
	projects.GET("/:project_id/episodes/:episode_id/versions/:version_id", s.handleGetScriptVersionDetail)
	projects.PATCH("/:project_id/episodes/:episode_id/production-brief", s.requireDirectorOrAdmin(), s.handleUpdateEpisodeBrief)
	projects.GET("/:project_id/dashboard", s.handleDashboard)
	projects.GET("/:project_id/run-events", s.handleRunEvents)
	projects.POST("/:project_id/query-agent", s.handleProjectQuery)
	projects.GET("/:project_id/breakdowns/current", s.handleCurrentBreakdown)
	projects.POST("/:project_id/breakdowns/current", s.handleSaveBreakdown)
	projects.POST("/:project_id/breakdown-steps/:step", s.handleCreateBreakdownStep)
	projects.POST("/:project_id/agents/runs/:run_id/materialize", s.handleMaterializeAgentRun)
	projects.POST("/:project_id/agents/:agent_type/jobs", s.handleCreateProjectAgentJob)
	projects.POST("/:project_id/breakdown-steps/prompts/:run_id/retry-distribution", s.handleRetryAssetPromptDistribution)
	projects.GET("/:project_id/script-segments", s.handleListScriptSegments)
	projects.GET("/:project_id/asset-revisions/current/scene-options", s.handleSceneAssetOptions)
}

func (s *Server) registerProductionBrief(g *gin.RouterGroup) {
	brief := g.Group("/production-brief", s.requireAuth())
	brief.GET("/catalog", s.handleProductionBriefCatalog)
}

func (s *Server) registerAgentRuns(g *gin.RouterGroup) {
	runs := g.Group("/agent-runs", s.requireAuth())
	runs.GET("", s.handleListAgentRuns)
	runs.GET("/:run_id", s.handleGetAgentRun)
}

// ---- /projects ----

func (s *Server) handleListProjects(c *gin.Context) {
	user := userFromContext(c)
	items, err := s.projects.ListProjects(c.Request.Context(), user.ID, user.Role)
	if err != nil {
		writeServiceError(c, "项目中", err)
		return
	}
	if items == nil {
		items = []service.ProjectItem{}
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) handleCreateProject(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		Title           *string         `json:"title"`
		Genre           *string         `json:"genre"`
		ScriptText      *string         `json:"script_text"`
		ProjectPrefix   *string         `json:"project_prefix"`
		ManagerID       *string         `json:"manager_id"`
		ProductionBrief json.RawMessage `json:"production_brief"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	title := ""
	if payload.Title != nil {
		title = *payload.Title
	}
	item, err := s.projects.CreateProject(c.Request.Context(), user.ID, service.CreateProjectInput{
		Title:           title,
		Genre:           payload.Genre,
		ScriptText:      payload.ScriptText,
		ProjectPrefix:   payload.ProjectPrefix,
		ManagerID:       payload.ManagerID,
		ProductionBrief: payload.ProductionBrief,
	})
	if err != nil {
		writeServiceError(c, "创建项目", err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (s *Server) handleImportProject(c *gin.Context) {
	user := userFromContext(c)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		unprocessableEntity(c, "请求体读取失败")
		return
	}
	filename := c.Query("filename")
	if filename == "" {
		filename = "script.txt"
	}
	var episodeNo *int32
	if value := c.Query("episode_no"); value != "" {
		parsedI, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			unprocessableEntity(c, "episode_no 必须是整数")
			return
		}
		n := int32(parsedI)
		episodeNo = &n
	}
	confirmExisting := c.Query("confirm_existing_episode_version") == "true"
	req := service.ImportRequest{
		Title:                         strPtrFromQuery(c.Query("title")),
		ProjectPrefix:                 strPtrFromQuery(c.Query("project_prefix")),
		Genre:                         strPtrFromQuery(c.Query("genre")),
		ProjectID:                     strPtrFromQuery(c.Query("project_id")),
		ProductionBrief:               c.Query("production_brief"),
		EpisodeBrief:                  c.Query("episode_brief"),
		EpisodeNo:                     episodeNo,
		ConfirmExistingEpisodeVersion: confirmExisting,
		Filename:                      filename,
		Raw:                           raw,
	}
	item, err := s.projects.Import(c.Request.Context(), user.ID, req)
	if err != nil {
		writeServiceError(c, "导入剧本", err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (s *Server) handleGetProject(c *gin.Context) {
	user := userFromContext(c)
	item, err := s.projects.GetProject(c.Request.Context(), user.ID, user.Role, c.Param("project_id"))
	if err != nil {
		writeServiceError(c, "获取项目", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) handleUpdateProject(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		Title           *string          `json:"title"`
		Genre           *string          `json:"genre"`
		ScriptText      *string          `json:"script_text"`
		ProjectPrefix   *string          `json:"project_prefix"`
		ManagerID       *string          `json:"manager_id"`
		ProductionBrief *json.RawMessage `json:"production_brief"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	item, err := s.projects.UpdateProject(c.Request.Context(), user.ID, c.Param("project_id"), service.UpdateProjectInput{
		Title: payload.Title, Genre: payload.Genre, ScriptText: payload.ScriptText,
		ProjectPrefix: payload.ProjectPrefix, ManagerID: payload.ManagerID,
		ProductionBrief: payload.ProductionBrief,
	})
	if err != nil {
		writeServiceError(c, "更新项目", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// ---- /projects/{id}/episodes ----

func (s *Server) handleListEpisodes(c *gin.Context) {
	user := userFromContext(c)
	items, err := s.projects.ListEpisodes(c.Request.Context(), user.ID, user.Role, c.Param("project_id"))
	if err != nil {
		writeServiceError(c, "分集列表", err)
		return
	}
	if items == nil {
		items = []service.EpisodeItem{}
	}
	c.JSON(http.StatusOK, items)
}

func (s *Server) handleGetEpisodeDetail(c *gin.Context) {
	user := userFromContext(c)
	item, err := s.projects.GetEpisodeDetail(c.Request.Context(), user.ID, user.Role, c.Param("project_id"), c.Param("episode_id"))
	if err != nil {
		writeServiceError(c, "分集详情", err)
		return
	}
	// 资产绑定（当前剧本版本的 active 绑定，对齐 EpisodeAssetBindingRead 列表）。
	if len(item.Versions) > 0 {
		currentVersionID := ""
		for _, v := range item.Versions {
			if v.IsCurrent {
				currentVersionID = v.ID
				break
			}
		}
		if currentVersionID != "" {
			bindings, ok := s.assets.EpisodeAssetBindings(c.Request.Context(),
				c.Param("project_id"), c.Param("episode_id"), currentVersionID)
			if ok == nil {
				item.Assets = bindings
			}
		}
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) handleGetScriptVersionDetail(c *gin.Context) {
	user := userFromContext(c)
	item, err := s.projects.GetScriptVersionDetail(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), c.Param("episode_id"), c.Param("version_id"))
	if err != nil {
		writeServiceError(c, "剧本版本", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) handleUpdateEpisodeBrief(c *gin.Context) {
	user := userFromContext(c)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		unprocessableEntity(c, "请求体读取失败")
		return
	}
	brief, err := s.projects.UpdateEpisodeBrief(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), c.Param("episode_id"), raw)
	if err != nil {
		writeServiceError(c, "更新分集制作简报", err)
		return
	}
	c.JSON(http.StatusOK, brief)
}

// ---- /projects/{id}/dashboard ----

func (s *Server) handleDashboard(c *gin.Context) {
	user := userFromContext(c)
	item, err := s.projects.Dashboard(c.Request.Context(), user.ID, user.Role, c.Param("project_id"))
	if err != nil {
		writeServiceError(c, "项目健康度", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// ---- /projects/{id}/breakdowns/current ----

func (s *Server) handleCurrentBreakdown(c *gin.Context) {
	user := userFromContext(c)
	episodeID := strPtrFromQuery(c.Query("episode_id"))
	scriptVersionID := strPtrFromQuery(c.Query("script_version_id"))
	item, err := s.projects.CurrentBreakdown(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), episodeID, scriptVersionID)
	if err != nil {
		writeServiceError(c, "拆解当前稿", err)
		return
	}
	if item == nil {
		c.Data(http.StatusOK, "application/json; charset=utf-8", []byte(`null`))
		return
	}
	c.JSON(http.StatusOK, item)
}

func (s *Server) handleSaveBreakdown(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		View            map[string]any `json:"view"`
		EpisodeCode     *string        `json:"episode_code"`
		EpisodeID       *string        `json:"episode_id"`
		ScriptVersionID *string        `json:"script_version_id"`
		ChangeSummary   []string       `json:"change_summary"`
		SourceLabel     *string        `json:"source_label"`
		DataState       *string        `json:"data_state"`
		ExpectedVersion *int32         `json:"expected_version"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	req := service.BreakdownSaveRequest{
		View: payload.View, EpisodeCode: payload.EpisodeCode, EpisodeID: payload.EpisodeID,
		ScriptVersionID: payload.ScriptVersionID, ChangeSummary: payload.ChangeSummary,
		ExpectedVersion: payload.ExpectedVersion,
	}
	if payload.SourceLabel != nil {
		req.SourceLabel = *payload.SourceLabel
	}
	if payload.DataState != nil {
		req.DataState = *payload.DataState
	}
	item, err := s.projects.SaveBreakdown(c.Request.Context(), user.ID, user.Role, c.Param("project_id"), req)
	if err != nil {
		writeServiceError(c, "保存拆解", err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

// ---- /projects/{id}/breakdown-steps/{step} ----

func (s *Server) handleCreateBreakdownStep(c *gin.Context) {
	user := userFromContext(c)
	run, err := s.projects.SubmitBreakdownStep(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), c.Param("step"),
		strPtrFromQuery(c.Query("episode_id")), strPtrFromQuery(c.Query("script_version_id")))
	if err != nil {
		writeServiceError(c, "拆解步骤", err)
		return
	}
	c.JSON(http.StatusAccepted, run)
}

// handleMaterializeAgentRun 对齐 legacy POST /{project_id}/agents/runs/{run_id}/materialize。
func (s *Server) handleMaterializeAgentRun(c *gin.Context) {
	user := userFromContext(c)
	result, err := s.projects.MaterializeAgentRun(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), c.Param("run_id"))
	if err != nil {
		writeServiceError(c, "Agent 物化", err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// handleCreateProjectAgentJob 对齐 legacy POST /{project_id}/agents/{agent_type}/jobs。
// 409 FORMAL gate 在项目校验前（legacy 顺序）；未知类型对齐 FastAPI 枚举 → 422。
func (s *Server) handleCreateProjectAgentJob(c *gin.Context) {
	user := userFromContext(c)
	agentType := c.Param("agent_type")
	if !agentDefinitionExists(agentType) {
		unprocessableEntity(c, "Unknown agent type: "+agentType)
		return
	}
	if service.IsFormalBreakdownAgentType(agentType) {
		conflict409(c, "拆解类 Agent 只能通过正式分步流程执行。")
		return
	}
	run, err := s.projects.SubmitProjectAgentJob(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), agentType, agentStageOK(agentType))
	if err != nil {
		writeServiceError(c, "Agent 项目任务", err)
		return
	}
	c.JSON(http.StatusAccepted, run)
}

// handleRetryAssetPromptDistribution 对齐 legacy retry_asset_prompt_distribution
// （404/403/409 阶梯逐字保留，200 返回 AgentRunRead）。
func (s *Server) handleRetryAssetPromptDistribution(c *gin.Context) {
	user := userFromContext(c)
	run, err := s.tasks.RetryAssetPromptTaskDistribution(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), c.Param("run_id"))
	if err != nil {
		writeServiceError(c, "资产提示词任务重试分发", err)
		return
	}
	c.JSON(http.StatusOK, run)
}

// ---- /projects/{id}/script-segments ----

func (s *Server) handleListScriptSegments(c *gin.Context) {
	user := userFromContext(c)
	items, err := s.projects.ListProjectScriptSegments(c.Request.Context(), user.ID, user.Role, c.Param("project_id"),
		strPtrFromQuery(c.Query("episode_id")))
	if err != nil {
		writeServiceError(c, "脚本段列表", err)
		return
	}
	c.JSON(http.StatusOK, items)
}

// ---- /production-brief/catalog ----

func (s *Server) handleProductionBriefCatalog(c *gin.Context) {
	catalog, err := service.ProductionBriefCatalog()
	if err != nil {
		internalError(c, "production brief catalog 加载失败")
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", catalog)
}

// ---- /agent-runs ----

func (s *Server) handleListAgentRuns(c *gin.Context) {
	limitRaw := c.Query("limit")
	limit := 50
	if limitRaw != "" {
		if parsedI, err := strconv.Atoi(limitRaw); err == nil {
			limit = parsedI
		}
	}
	page, err := s.projects.ListAgentRuns(c.Request.Context(),
		strPtrFromQuery(c.Query("project_id")), limit, c.Query("cursor"))
	if err != nil {
		writeServiceError(c, "Agent 运行列表", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (s *Server) handleGetAgentRun(c *gin.Context) {
	user := userFromContext(c)
	item, err := s.projects.GetAgentRunForUser(c.Request.Context(), user.ID, user.Role, c.Param("run_id"))
	if err != nil {
		writeServiceError(c, "Agent 运行详情", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// handleProjectQuery 对齐 legacy POST /projects/{project_id}/query-agent（本地向量检索）。
func (s *Server) handleProjectQuery(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		Question string `json:"question"`
		Limit    int    `json:"limit"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if payload.Limit == 0 {
		payload.Limit = 5
	}
	item, err := s.projects.ProjectQuery(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), payload.Question, payload.Limit)
	if err != nil {
		writeServiceError(c, "项目检索", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// ---- 公共 ----

// writeServiceError 将 service.StatusError 转 HTTP 响应；非 StatusError 视为 500。
func writeServiceError(c *gin.Context, label string, err error) {
	status, detail := service.StatusOf(err)
	if status == 500 {
		internalError(c, "服务内部错误："+label)
		return
	}
	errorJSON(c, status, detail)
}

func strPtrFromQuery(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

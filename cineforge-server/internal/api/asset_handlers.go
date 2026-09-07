package api

// P3f-c 资产域 handlers（对齐 legacy app/api/routes/assets.py）：
//   - GET  /assets/library             资产库（project_id + include_versions）
//   - GET  /assets                     资产列表（条件过滤）
//   - POST /assets                     正式码分配创建（director/admin）
//   - POST /assets/temporary-production 人工临时生产（director/admin，幂等）
//   - GET/POST /assets/variant-plans   变体计划列表/创建（创建恒 400）
//   - PATCH/DELETE /assets/variant-plans/{id}  更新/退役
//   - GET  /assets/{id}                资产详情
// scene-options 挂在 /projects/{pid}/asset-revisions/current/scene-options → registerProjects。
//
// 错误文案逐字对齐 legacy routes，30000 字符上限外不再另有校验。

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"cineforge/server/internal/repository"
	"cineforge/server/internal/service"
)

// registerAssets 注册 /assets 路由组。
func (s *Server) registerAssets(g *gin.RouterGroup) {
	assets := g.Group("/assets", s.requireAuth())
	assets.GET("/library", s.handleAssetLibrary)
	assets.GET("", s.handleListAssets)
	assets.POST("", s.requireDirectorOrAdmin(), s.handleCreateAsset)
	assets.POST("/temporary-production", s.requireDirectorOrAdmin(), s.handleTemporaryProduction)
	assets.GET("/variant-plans", s.handleListVariantPlans)
	assets.POST("/variant-plans", s.requireDirectorOrAdmin(), s.handleCreateVariantPlan)
	assets.PATCH("/variant-plans/:plan_id", s.requireDirectorOrAdmin(), s.handleUpdateVariantPlan)
	assets.DELETE("/variant-plans/:plan_id", s.requireDirectorOrAdmin(), s.handleRetireVariantPlan)
	assets.GET("/:asset_id", s.handleGetAsset)
}

func assetActor(c *gin.Context) repository.TaskActor {
	user := userFromContext(c)
	return repository.TaskActor{ID: user.ID, Role: user.Role}
}

// parseFlexBool 对齐 FastAPI bool 解析（true/false/t/yes/y/on/1 等大小写不敏感）。
func parseFlexBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "t", "yes", "y", "on", "1":
		return true, true
	case "false", "f", "no", "n", "off", "0":
		return false, true
	}
	return false, false
}

// ---- GET /assets/library ----

func (s *Server) handleAssetLibrary(c *gin.Context) {
	projectID := strPtrFromQuery(c.Query("project_id"))
	includeVersions := false
	if raw, ok := parseFlexBool(c.Query("include_versions")); ok {
		includeVersions = raw
	}
	items, err := s.assets.ListAssetLibrary(c.Request.Context(), assetActor(c), projectID, includeVersions)
	if err != nil {
		writeServiceError(c, "资产库加载", err)
		return
	}
	c.JSON(http.StatusOK, items)
}

// ---- GET /assets ----

func (s *Server) handleListAssets(c *gin.Context) {
	includeMetadata := true
	if raw, ok := parseFlexBool(c.Query("include_metadata")); ok {
		includeMetadata = raw
	}
	items, err := s.assets.ListAssets(c.Request.Context(), assetActor(c), repository.ListAssetsParams{
		AssetType:       c.Query("asset_type"),
		Search:          c.Query("search"),
		ProjectID:       strPtrFromQuery(c.Query("project_id")),
		IncludeMetadata: includeMetadata,
	})
	if err != nil {
		writeServiceError(c, "资产列表加载", err)
		return
	}
	c.JSON(http.StatusOK, items)
}

// ---- POST /assets ----

type createAssetRequest struct {
	AssetType   string         `json:"asset_type"`
	Name        string         `json:"name"`
	Description *string        `json:"description"`
	Tags        []string       `json:"tags"`
	PreviewPath *string        `json:"preview_path"`
	FilePath    *string        `json:"file_path"`
	PromptText  *string        `json:"prompt_text"`
	BaseModel   *string        `json:"base_model"`
	Metadata    map[string]any `json:"metadata"`
}

func (s *Server) handleCreateAsset(c *gin.Context) {
	var payload createAssetRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		unprocessableEntity(c, "name 不能为空")
		return
	}
	view, err := s.assets.CreateAsset(c.Request.Context(), assetActor(c), repository.CreateAssetInput{
		AssetType:   payload.AssetType,
		Name:        payload.Name,
		Description: payload.Description,
		Tags:        payload.Tags,
		PreviewPath: payload.PreviewPath,
		FilePath:    payload.FilePath,
		PromptText:  payload.PromptText,
		BaseModel:   payload.BaseModel,
		Metadata:    payload.Metadata,
	})
	if err != nil {
		writeServiceError(c, "创建资产", err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

// ---- POST /assets/temporary-production ----

type temporaryProductionRequest struct {
	RequestID             string  `json:"request_id"`
	ProjectID             string  `json:"project_id"`
	EpisodeID             string  `json:"episode_id"`
	ScriptVersionID       string  `json:"script_version_id"`
	AssetType             string  `json:"asset_type"`
	Name                  string  `json:"name"`
	Description           *string `json:"description"`
	Reason                string  `json:"reason"`
	ProductionRequirement string  `json:"production_requirement"`
	TaskVariant           string  `json:"task_variant"`
}

func (s *Server) handleTemporaryProduction(c *gin.Context) {
	var payload temporaryProductionRequest
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if payload.TaskVariant == "" {
		payload.TaskVariant = "MASTER"
	}
	result, err := s.assets.CreateTemporaryAssetProduction(c.Request.Context(), assetActor(c), repository.TemporaryProductionInput{
		RequestID:             payload.RequestID,
		ProjectID:             payload.ProjectID,
		EpisodeID:             payload.EpisodeID,
		ScriptVersionID:       payload.ScriptVersionID,
		AssetType:             payload.AssetType,
		Name:                  payload.Name,
		Description:           payload.Description,
		Reason:                payload.Reason,
		ProductionRequirement: payload.ProductionRequirement,
		TaskVariant:           payload.TaskVariant,
	})
	if err != nil {
		writeServiceError(c, "人工临时生产", err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// ---- GET /assets/variant-plans ----

func (s *Server) handleListVariantPlans(c *gin.Context) {
	projectID := c.Query("project_id")
	if projectID == "" {
		unprocessableEntity(c, "project_id 不能为空")
		return
	}
	items, err := s.assets.ListAssetVariantPlans(c.Request.Context(), assetActor(c), projectID,
		strPtrFromQuery(c.Query("episode_id")))
	if err != nil {
		writeServiceError(c, "变体计划加载", err)
		return
	}
	c.JSON(http.StatusOK, items)
}

// ---- POST /assets/variant-plans ----

func (s *Server) handleCreateVariantPlan(c *gin.Context) {
	view, err := s.assets.CreateAssetVariantPlan(c.Request.Context())
	if err != nil {
		writeServiceError(c, "创建变体计划", err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

// ---- PATCH/DELETE /assets/variant-plans/{id} ----

func (s *Server) handleUpdateVariantPlan(c *gin.Context) {
	var payload struct {
		TitleZh       *string  `json:"title_zh"`
		DescriptionZh *string  `json:"description_zh"`
		Status        *string  `json:"status"`
		StoryboardIDs []string `json:"storyboard_ids"`
	}
	hasStoryboardIDs := false
	if err := c.ShouldBindJSON(&payload); err == nil {
		hasStoryboardIDs = payload.StoryboardIDs != nil
	}
	view, err := s.assets.UpdateAssetVariantPlan(c.Request.Context(), assetActor(c), c.Param("plan_id"), repository.UpdateAssetVariantPlanInput{
		TitleZh:          payload.TitleZh,
		DescriptionZh:    payload.DescriptionZh,
		Status:           payload.Status,
		StoryboardIDs:    payload.StoryboardIDs,
		HasStoryboardIDs: hasStoryboardIDs,
	})
	if err != nil {
		writeServiceError(c, "更新变体计划", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

func (s *Server) handleRetireVariantPlan(c *gin.Context) {
	view, err := s.assets.UpdateAssetVariantPlan(c.Request.Context(), assetActor(c), c.Param("plan_id"), repository.UpdateAssetVariantPlanInput{
		Status: strPtrFromQuery("retired"),
	})
	if err != nil {
		writeServiceError(c, "退役变体计划", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ---- GET /assets/{id} ----

func (s *Server) handleGetAsset(c *gin.Context) {
	view, err := s.assets.GetAsset(c.Request.Context(), assetActor(c), c.Param("asset_id"))
	if err != nil {
		writeServiceError(c, "资产详情", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ---- GET /projects/{pid}/asset-revisions/current/scene-options ----

func (s *Server) handleSceneAssetOptions(c *gin.Context) {
	items, err := s.assets.ListSceneAssetOptions(c.Request.Context(), assetActor(c),
		c.Param("project_id"), c.Query("search"))
	if err != nil {
		writeServiceError(c, "场景选项加载", err)
		return
	}
	c.JSON(http.StatusOK, items)
}

// 保持 service 包引用（listAssets 等返回类型使用）。
var _ = service.StatusOf

package api

// /api/agents/* 端点（对齐 legacy app/api/routes/agents.py）：
//
//   - GET /agents                    → list[AgentDefinitionRead]（见 agents_registry.go）
//   - GET /agents/stage-map          → list[{stage, agents[]}]
//   - GET /agents/{agent_type}       → AgentDefinitionRead
//   - GET /agents/hermes/config      → Hermes 配置
//   - GET /agents/hermes/health      → Hermes 状态（P4e 真实探活：health/models 探测链）
//   - POST /agent-jobs               → 非正式 Agent 任务（director/admin，202）
//   - GET /agent-runs、/agent-runs/{run_id} → 运行列表/详情（已有）
//   - POST/GET /agent-runs/{run_id}/feedback、GET/POST /agent-training-samples → feedback/training
//
// legacy 静态端点无鉴权依赖（无 Depends），此处保持同样契约。

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"cineforge/server/internal/config"
	"cineforge/server/internal/service"
)

const hermesPlaceholderAPIKey = "change-me-hermes-local"

// hermesProbeTimeout 对齐 legacy health() 的 timeout=min(hermes_timeout_seconds, 8.0)。
const hermesProbeTimeout = 8 * time.Second

func (s *Server) registerAgents(g *gin.RouterGroup) {
	a := g.Group("/agents")
	a.GET("", s.handleListAgents)
	a.GET("/hermes/config", s.handleHermesConfig)
	a.GET("/hermes/health", s.handleHermesHealth)
	a.GET("/stage-map", s.handleAgentStageMap)
	a.GET("/:agent_type", s.handleGetAgent)

	// 非正式 Agent 任务（对齐 legacy app/api/routes/agents.py jobs_router）。
	jobs := g.Group("/agent-jobs", s.requireAuth(), s.requireDirectorOrAdmin())
	jobs.POST("", s.handleCreateAgentJob)

	// feedback / training-samples（对齐 legacy runs_router + training_router）。
	runs := g.Group("/agent-runs", s.requireAuth())
	runs.POST("/:run_id/feedback", s.handleAddAgentFeedback)
	runs.GET("/:run_id/feedback", s.handleListAgentFeedback)
	training := g.Group("/agent-training-samples", s.requireAuth())
	training.GET("", s.handleListAgentTrainingSamples)
	training.POST("", s.requireDirectorOrAdmin(), s.handleCreateAgentTrainingSample)
}

// handleCreateAgentJob 对齐 legacy POST /agent-jobs → orchestrator.submit_background。
// 未注册类型 → 404 "Agent not found"（legacy registry.get 抛 KeyError）；
// status=running 落库后立即入队，返回 202。
func (s *Server) handleCreateAgentJob(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		AgentType      string         `json:"agent_type"`
		ProjectID      *string        `json:"project_id"`
		StoryboardID   *string        `json:"storyboard_id"`
		TaskID         *string        `json:"task_id"`
		Input          map[string]any `json:"input"`
		IdempotencyKey *string        `json:"idempotency_key"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if !agentDefinitionExists(payload.AgentType) {
		notFound404(c, "Agent not found")
		return
	}
	run, err := s.projects.SubmitAgentJob(c.Request.Context(), user.ID, service.AgentJobInput{
		AgentType:      payload.AgentType,
		ProjectID:      payload.ProjectID,
		StoryboardID:   payload.StoryboardID,
		TaskID:         payload.TaskID,
		Input:          payload.Input,
		IdempotencyKey: payload.IdempotencyKey,
	})
	if err != nil {
		writeServiceError(c, "Agent 任务提交", err)
		return
	}
	c.JSON(http.StatusAccepted, run)
}

// handleAddAgentFeedback 对齐 legacy POST /agent-runs/{run_id}/feedback（201）。
// rating 范围校验对齐 Pydantic ge=1, le=10 → 422。
func (s *Server) handleAddAgentFeedback(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		Rating         *int32         `json:"rating"`
		OriginalOutput map[string]any `json:"original_output"`
		EditedOutput   map[string]any `json:"edited_output"`
		Note           *string        `json:"note"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if payload.Rating != nil && (*payload.Rating < 1 || *payload.Rating > 10) {
		unprocessableEntity(c, "rating 必须是 1-10 的整数")
		return
	}
	item, err := s.projects.AddAgentFeedback(c.Request.Context(), user.ID, c.Param("run_id"),
		service.AgentFeedbackInput{
			Rating:         payload.Rating,
			OriginalOutput: payload.OriginalOutput,
			EditedOutput:   payload.EditedOutput,
			Note:           payload.Note,
		})
	if err != nil {
		writeServiceError(c, "Agent 反馈", err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

// handleListAgentFeedback 对齐 legacy GET /agent-runs/{run_id}/feedback。
func (s *Server) handleListAgentFeedback(c *gin.Context) {
	items, err := s.projects.ListAgentFeedback(c.Request.Context(), c.Param("run_id"))
	if err != nil {
		writeServiceError(c, "Agent 反馈列表", err)
		return
	}
	c.JSON(http.StatusOK, items)
}

// handleListAgentTrainingSamples 对齐 legacy GET /agent-training-samples。
func (s *Server) handleListAgentTrainingSamples(c *gin.Context) {
	agentType := strPtrFromQuery(c.Query("agent_type"))
	if agentType != nil && !validAgentKind(*agentType) {
		unprocessableEntity(c, "Unknown agent type: "+*agentType)
		return
	}
	items, err := s.projects.ListTrainingSamples(c.Request.Context(),
		strPtrFromQuery(c.Query("project_id")), agentType)
	if err != nil {
		writeServiceError(c, "训练样本列表", err)
		return
	}
	if items == nil {
		items = []service.TrainingSampleItem{}
	}
	c.JSON(http.StatusOK, items)
}

// handleCreateAgentTrainingSample 对齐 legacy POST /agent-training-samples（director/admin，201）。
func (s *Server) handleCreateAgentTrainingSample(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		ProjectID       *string        `json:"project_id"`
		AgentType       *string        `json:"agent_type"`
		SampleType      string         `json:"sample_type"`
		EntityType      *string        `json:"entity_type"`
		EntityCode      *string        `json:"entity_code"`
		InputJSON       map[string]any `json:"input_json"`
		AgentOutputJSON map[string]any `json:"agent_output_json"`
		HumanModified   map[string]any `json:"human_modified_output_json"`
		Confirmed       map[string]any `json:"confirmed_output_json"`
		ChangeSummary   []string       `json:"change_summary"`
		QualityScore    *int32         `json:"quality_score"`
		TrainingTags    []string       `json:"training_tags"`
		TrainingReady   bool           `json:"training_ready"`
		CreatedBy       *string        `json:"created_by"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if payload.AgentType != nil && !validProductionAgentKind(*payload.AgentType) {
		unprocessableEntity(c, "Unknown agent type: "+*payload.AgentType)
		return
	}
	if payload.QualityScore != nil && (*payload.QualityScore < 1 || *payload.QualityScore > 10) {
		unprocessableEntity(c, "quality_score 必须是 1-10 的整数")
		return
	}
	item, err := s.projects.CreateTrainingSample(c.Request.Context(), user.ID, user.Role,
		service.TrainingSampleCreateInput{
			ProjectID:       payload.ProjectID,
			AgentType:       payload.AgentType,
			SampleType:      payload.SampleType,
			EntityType:      payload.EntityType,
			EntityCode:      payload.EntityCode,
			InputJSON:       payload.InputJSON,
			AgentOutputJSON: payload.AgentOutputJSON,
			HumanModified:   payload.HumanModified,
			Confirmed:       payload.Confirmed,
			ChangeSummary:   payload.ChangeSummary,
			QualityScore:    payload.QualityScore,
			TrainingTags:    payload.TrainingTags,
			TrainingReady:   payload.TrainingReady,
			CreatedBy:       payload.CreatedBy,
		})
	if err != nil {
		writeServiceError(c, "训练样本创建", err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

// fillHermesURL 空值按域名填充 dashboard（:9119）/ webui（:3000），对齐 legacy _fill_hermes_urls。
func fillHermesURL(cfg *config.Config, url, port string) string {
	if url != "" {
		return url
	}
	if cfg.Domains.Server == "" {
		return ""
	}
	return "http://" + cfg.Domains.Server + port
}

func (s *Server) handleHermesConfig(c *gin.Context) {
	h := s.cfg.Hermes
	c.JSON(http.StatusOK, gin.H{
		"enabled":            h.Enabled,
		"base_url":           h.Addr,
		"health_url":         h.HealthURL,
		"model":              h.Model,
		"dashboard_url":      fillHermesURL(s.cfg, h.DashboardURL, ":9119"),
		"webui_url":          fillHermesURL(s.cfg, h.WebUIURL, ":3000"),
		"api_key_configured": h.APIKey != "" && h.APIKey != hermesPlaceholderAPIKey,
	})
}

func (s *Server) handleHermesHealth(c *gin.Context) {
	h := s.cfg.Hermes
	body := gin.H{
		"enabled":       h.Enabled,
		"base_url":      h.Addr,
		"health_url":    h.HealthURL,
		"model":         h.Model,
		"dashboard_url": fillHermesURL(s.cfg, h.DashboardURL, ":9119"),
		"webui_url":     fillHermesURL(s.cfg, h.WebUIURL, ":3000"),
		"status":        "unknown",
	}
	if !h.Enabled {
		body["status"] = "disabled"
		c.JSON(http.StatusOK, body)
		return
	}

	// 探测链对齐 legacy HermesClient.health()：先 health_url，后 {base_url}/models。
	// status 优先级：unreachable(health 网络失败) > ok/unhealthy(health 状态码) > auth_failed/unhealthy(models 状态码)。
	client := &http.Client{Timeout: hermesProbeTimeout}

	healthResp, err := client.Get(h.HealthURL)
	if err != nil {
		body["status"] = "unreachable"
		body["error"] = err.Error()
		c.JSON(http.StatusOK, body)
		return
	}
	body["health_status_code"] = healthResp.StatusCode
	body["health"] = hermesSafeJSON(healthResp)
	healthResp.Body.Close()
	if healthResp.StatusCode >= 200 && healthResp.StatusCode < 300 {
		body["status"] = "ok"
	} else {
		body["status"] = "unhealthy"
	}

	modelsURL := strings.TrimSuffix(h.Addr, "/") + "/models"
	req, reqErr := http.NewRequest(http.MethodGet, modelsURL, nil)
	if reqErr == nil {
		req.Header.Set("Authorization", "Bearer "+h.APIKey)
		req.Header.Set("Content-Type", "application/json")
		modelsResp, merr := client.Do(req)
		if merr != nil {
			body["models_error"] = merr.Error()
		} else {
			body["models_status_code"] = modelsResp.StatusCode
			body["models"] = hermesSafeJSON(modelsResp)
			modelsResp.Body.Close()
			if !(modelsResp.StatusCode >= 200 && modelsResp.StatusCode < 300) {
				if modelsResp.StatusCode == http.StatusUnauthorized {
					body["status"] = "auth_failed"
				} else {
					body["status"] = "unhealthy"
				}
			}
		}
	}
	c.JSON(http.StatusOK, body)
}

// hermesSafeJSON 对齐 legacy _safe_json：可解析 JSON 返回原值，否则截断文本。
func hermesSafeJSON(resp *http.Response) any {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out any
	if err := json.Unmarshal(data, &out); err == nil {
		return out
	}
	text := string(data)
	if len(text) > 500 {
		text = text[:500]
	}
	return text
}

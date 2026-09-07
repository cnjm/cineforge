package api

// P3e task 域 handlers（对齐 legacy app/api/routes/tasks.py + projects.py 的分组路由）。
// 错误映射与 service 层一致：KeyError→404 / PermissionError→403 / ValueError→400，
// HTTPException detail 逐字对齐（"Task not found"、"Project permission denied" 等）。
//
// 说明：
//   - POST /tasks/{id}/prompt-jobs（异步 Agent 运行）属 P4 agent 执行，本阶段不注册。
//   - POST /projects/{id}/asset-tasks 与 /storyboard-tasks（流程1/流程2 生成器）
//     体量单独成文件，等范围确认后接入，本文件不注册。

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"cineforge/server/internal/repository"
)

// taskStatusSet 对齐 TaskStatus 枚举（用于 list/update 的参数枚举校验 → 422）。
var taskStatusSet = map[string]bool{
	"todo": true, "in_progress": true, "submitted": true, "reviewing": true,
	"completed": true, "rejected": true, "overdue": true,
}

// ---- /tasks 注册 ----

func (s *Server) registerTasks(g *gin.RouterGroup) {
	tasks := g.Group("/tasks", s.requireAuth())
	tasks.GET("", s.handleListTasks)
	tasks.GET("/:task_id", s.handleGetTask)
	tasks.PATCH("/:task_id", s.handleUpdateTask)
	tasks.GET("/:task_id/prompts", s.handleListTaskPrompts)
	tasks.POST("/:task_id/prompts", s.handleCreateTaskPrompt)
	tasks.POST("/:task_id/prompt-jobs", s.handleCreateTaskPromptJob)
	tasks.GET("/:task_id/submissions", s.handleListSubmissions)
	tasks.POST("/:task_id/submissions", s.handleCreateSubmission)
	tasks.POST("/:task_id/submit", s.handleSubmitTaskBatch)
	tasks.POST("/submission-batches/bulk-submit", s.handleBulkSubmitTaskBatches)
	tasks.POST("/submission-batches/bulk-review", s.handleBulkReviewTaskBatches)
	tasks.PATCH("/:task_id/submissions/:submission_id", s.handleUpdateSubmissionMetadata)
	tasks.DELETE("/:task_id/submissions/:submission_id", s.handleDeleteDraftSubmission)
	tasks.POST("/:task_id/submission-batches/:batch_id/review", s.handleReviewTaskBatch)
	tasks.PATCH("/:task_id/submissions/:submission_id/archive", s.handleArchiveSubmission)
	tasks.PATCH("/:task_id/primary-submission", s.handleChangePrimarySubmission)
}

// ---- /workspace 注册 ----

func (s *Server) registerWorkspaceTasks(g *gin.RouterGroup) {
	ws := g.Group("/workspace", s.requireAuth())
	ws.GET("/tasks", s.handleGetWorkspaceTasks)
	ws.GET("/reviews", s.handleGetReviewWorkspace)
	ws.GET("/notifications", s.handleListWorkspaceNotifications)
	ws.POST("/notifications/read-all", s.handleMarkAllNotificationsRead)
}

// ---- GET /workspace/notifications ----

func (s *Server) handleListWorkspaceNotifications(c *gin.Context) {
	user := userFromContext(c)
	items, err := s.notify.List(c.Request.Context(), user.ID)
	if err != nil {
		internalError(c, "加载通知失败")
		return
	}
	if items == nil {
		items = []repository.NotifyView{}
	}
	c.JSON(http.StatusOK, items)
}

// ---- POST /workspace/notifications/read-all ----

func (s *Server) handleMarkAllNotificationsRead(c *gin.Context) {
	user := userFromContext(c)
	updated, err := s.notify.MarkAllRead(c.Request.Context(), user.ID)
	if err != nil {
		internalError(c, "通知已读标记失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": updated})
}

// ---- /projects/{id} 分组注册 ----

func (s *Server) registerProjectGroupingRoutes(g *gin.RouterGroup) {
	projects := g.Group("/projects", s.requireAuth())
	projects.GET("/:project_id/storyboards", s.handleListProjectStoryboards)
	projects.GET("/:project_id/board", s.handleGetProductionBoard)
	projects.GET("/:project_id/scene-gating", s.handleGetSceneGating)
	projects.POST("/:project_id/storyboard-video-tasks", s.handleCreateStoryboardVideoTasks)
	projects.POST("/:project_id/asset-tasks", s.requireDirectorOrAdmin(), s.handleGenerateAssetTasks)
	projects.POST("/:project_id/storyboard-tasks", s.requireDirectorOrAdmin(), s.handleGenerateStoryboardTasks)
	projects.POST("/:project_id/task-assignments", s.requireDirectorOrAdmin(), s.handleBulkAssignTasks)
}

// ---- GET /tasks ----

func (s *Server) handleListTasks(c *gin.Context) {
	user := userFromContext(c)
	status := strPtrFromQuery(c.Query("status"))
	if status != nil && !taskStatusSet[*status] {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	items, err := s.tasks.ListTasks(c.Request.Context(), user.ID, user.Role,
		strPtrFromQuery(c.Query("project_id")), status, strPtrFromQuery(c.Query("assignee_id")))
	if err != nil {
		writeServiceError(c, "任务列表", err)
		return
	}
	if items == nil {
		items = []repository.TaskView{}
	}
	c.JSON(http.StatusOK, items)
}

// ---- GET /tasks/{task_id} ----

func (s *Server) handleGetTask(c *gin.Context) {
	user := userFromContext(c)
	view, err := s.tasks.GetTask(c.Request.Context(), user.ID, user.Role, c.Param("task_id"))
	if err != nil {
		writeServiceError(c, "任务详情", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ---- PATCH /tasks/{task_id} ----

func (s *Server) handleUpdateTask(c *gin.Context) {
	user := userFromContext(c)
	raw, err := readJSONObject(c)
	if err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	in := repository.TaskUpdateInput{}
	if value, present, ok := bindNullableField(raw, "status"); ok {
		if present {
			if value != nil && !taskStatusSet[*value] {
				unprocessableEntity(c, "请求体格式错误")
				return
			}
			in.Status = value
			in.StatusSet = true
		}
	}
	if value, present, ok := bindNullableField(raw, "assignee_id"); ok {
		in.AssigneeID = value
		in.AssigneeIDSet = present
	}
	if value, present, ok := bindNullableField(raw, "reassignment_reason"); ok {
		in.ReassignmentReason = value
		_ = present // 提供与否均消费；空字符串语义由 repo 处理
	}
	if value, present, ok := bindNullableField(raw, "latest_prompt_text"); ok {
		in.LatestPromptText = value
		in.LatestPromptSet = present
	}
	if value, present, ok := bindNullableField(raw, "production_model"); ok {
		in.ProductionModel = value
		in.ProductionModelSet = present
	}
	view, err := s.tasks.UpdateTask(c.Request.Context(), user.ID, user.Role, c.Param("task_id"), in)
	if err != nil {
		writeServiceError(c, "更新任务", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ---- GET /tasks/{task_id}/prompts ----

func (s *Server) handleListTaskPrompts(c *gin.Context) {
	user := userFromContext(c)
	items, err := s.tasks.ListTaskPrompts(c.Request.Context(), user.ID, user.Role, c.Param("task_id"))
	if err != nil {
		writeServiceError(c, "任务提示词", err)
		return
	}
	if items == nil {
		items = []repository.PromptView{}
	}
	c.JSON(http.StatusOK, items)
}

// ---- POST /tasks/{task_id}/prompts（201） ----

func (s *Server) handleCreateTaskPrompt(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		PromptType *string `json:"prompt_type"`
		PromptText string  `json:"prompt_text"`
		Source     *string `json:"source"`
		Copied     *bool   `json:"copied"`
		CreatedBy  *string `json:"created_by"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	in := repository.TaskPromptCreate{
		PromptType: "human_edit",
		PromptText: payload.PromptText,
		Source:     "human",
		Copied:     false,
		CreatedBy:  payload.CreatedBy,
	}
	if payload.PromptType != nil {
		in.PromptType = *payload.PromptType
	}
	if payload.Source != nil {
		in.Source = *payload.Source
	}
	if payload.Copied != nil {
		in.Copied = *payload.Copied
	}
	view, err := s.tasks.AddTaskPrompt(c.Request.Context(), user.ID, user.Role, c.Param("task_id"), in)
	if err != nil {
		writeServiceError(c, "保存任务提示词", err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

// ---- GET /tasks/{task_id}/submissions ----

// handleCreateTaskPromptJob 对齐 legacy POST /tasks/{task_id}/prompt-jobs（202）。
// step 为 storyboard_shot 的可选 Query 参数（keyframe/video）。
func (s *Server) handleCreateTaskPromptJob(c *gin.Context) {
	user := userFromContext(c)
	var step *string
	if raw := c.Query("step"); raw != "" {
		step = &raw
	}
	run, err := s.tasks.CreateTaskPromptJob(c.Request.Context(), user.ID, user.Role, c.Param("task_id"), step)
	if err != nil {
		writeServiceError(c, "任务 Prompt 提交", err)
		return
	}
	c.JSON(http.StatusAccepted, run)
}

func (s *Server) handleListSubmissions(c *gin.Context) {
	user := userFromContext(c)
	items, err := s.tasks.ListTaskSubmissions(c.Request.Context(), user.ID, user.Role, c.Param("task_id"))
	if err != nil {
		writeServiceError(c, "任务候选", err)
		return
	}
	if items == nil {
		items = []repository.SubmissionView{}
	}
	c.JSON(http.StatusOK, items)
}

// ---- POST /tasks/{task_id}/submissions（201） ----

func (s *Server) handleCreateSubmission(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		FileId             *string  `json:"file_id"`
		FilePath           string   `json:"file_path"`
		FileType           string   `json:"file_type"`
		Step               *string  `json:"step"`
		PromptText         *string  `json:"prompt_text"`
		OriginalPromptText *string  `json:"original_prompt_text"`
		RevisedPromptText  *string  `json:"revised_prompt_text"`
		ViewLabel          *string  `json:"view_label"`
		StateLabel         *string  `json:"state_label"`
		Description        *string  `json:"description"`
		ModelName          *string  `json:"model_name"`
		ToolNames          []string `json:"tool_names"`
		SubmittedById      *string  `json:"submitted_by_id"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if payload.FilePath == "" || payload.FileType == "" {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	view, err := s.tasks.AddSubmission(c.Request.Context(), user.ID, user.Role, c.Param("task_id"),
		repository.SubmissionCreate{
			FileId:             payload.FileId,
			FilePath:           payload.FilePath,
			FileType:           payload.FileType,
			Step:               payload.Step,
			PromptText:         payload.PromptText,
			OriginalPromptText: payload.OriginalPromptText,
			RevisedPromptText:  payload.RevisedPromptText,
			ViewLabel:          payload.ViewLabel,
			StateLabel:         payload.StateLabel,
			Description:        payload.Description,
			ModelName:          payload.ModelName,
			ToolNames:          payload.ToolNames,
			SubmittedById:      payload.SubmittedById,
		})
	if err != nil {
		writeServiceError(c, "登记候选", err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

// normalizeUUIDString 校验并规范化请求幂等键为连字符小写 UUID（对齐 FastAPI UUID 字段）。
// 接受连字符与无连字符十六进制两种形式；非 UUID 返回 (零值, false) → handler 422。
func normalizeUUIDString(s string) (string, bool) {
	s = strings.TrimSpace(s)
	switch len(s) {
	case 32:
		if _, err := hex.DecodeString(s); err != nil {
			return "", false
		}
		return fmt.Sprintf("%s-%s-%s-%s-%s", s[0:8], s[8:12], s[12:16], s[16:20], s[20:32]), true
	case 36:
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return "", false
		}
		if _, err := hex.DecodeString(strings.ReplaceAll(s, "-", "")); err != nil {
			return "", false
		}
		return strings.ToLower(s), true
	default:
		return "", false
	}
}

// ---- POST /tasks/{task_id}/submit ----

func (s *Server) handleSubmitTaskBatch(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		Step      *string `json:"step"`
		RequestID *string `json:"request_id"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	var requestID *string
	if payload.RequestID != nil && *payload.RequestID != "" {
		normalized, ok := normalizeUUIDString(*payload.RequestID)
		if !ok {
			unprocessableEntity(c, "请求体格式错误")
			return
		}
		requestID = &normalized
	}
	view, err := s.tasks.SubmitTaskBatch(c.Request.Context(), user.ID, user.Role, c.Param("task_id"),
		repository.TaskSubmitInput{Step: payload.Step, RequestID: requestID})
	if err != nil {
		writeServiceError(c, "提交审核", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ---- POST /tasks/submission-batches/bulk-submit ----

func (s *Server) handleBulkSubmitTaskBatches(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		RequestID string `json:"request_id"`
		Entries   []struct {
			TaskID string  `json:"task_id"`
			Step   *string `json:"step"`
		} `json:"entries"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	requestID, ok := normalizeUUIDString(payload.RequestID)
	if payload.RequestID == "" || !ok {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if len(payload.Entries) == 0 {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	entries := make([]repository.TaskBulkSubmitEntry, 0, len(payload.Entries))
	for _, item := range payload.Entries {
		entries = append(entries, repository.TaskBulkSubmitEntry{TaskID: item.TaskID, Step: item.Step})
	}
	views, err := s.tasks.BulkSubmitTaskBatches(c.Request.Context(), user.ID, user.Role, requestID, entries)
	if err != nil {
		writeServiceError(c, "批量提交审核", err)
		return
	}
	if views == nil {
		views = []repository.BatchView{}
	}
	c.JSON(http.StatusOK, views)
}

// ---- POST /tasks/submission-batches/bulk-review ----

func (s *Server) handleBulkReviewTaskBatches(c *gin.Context) {
	user := userFromContext(c)
	raw, err := readJSONObject(c)
	if err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	requestID, err := requiredStringField(raw, "request_id")
	if err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	normalizedID, ok := normalizeUUIDString(requestID)
	if !ok {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	requestID = normalizedID
	decision, present, ok := bindNullableField(raw, "decision")
	if !ok || !present || decision == nil || (*decision != "approve" && *decision != "rework") {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	comment, _, _ := bindNullableField(raw, "comment")
	entriesRaw, ok := raw["entries"]
	if !ok {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	var entriesPayload []struct {
		TaskID                string   `json:"task_id"`
		BatchID               string   `json:"batch_id"`
		PrimarySubmissionID   *string  `json:"primary_submission_id"`
		SelectedSubmissionIDs []string `json:"selected_submission_ids"`
	}
	if err := json.Unmarshal(entriesRaw, &entriesPayload); err != nil || len(entriesPayload) == 0 {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	entries := make([]repository.TaskBulkReviewEntry, 0, len(entriesPayload))
	for _, item := range entriesPayload {
		if item.TaskID == "" || item.BatchID == "" {
			unprocessableEntity(c, "请求体格式错误")
			return
		}
		entries = append(entries, repository.TaskBulkReviewEntry{
			TaskID: item.TaskID, BatchID: item.BatchID,
			PrimarySubmissionID:   item.PrimarySubmissionID,
			SelectedSubmissionIDs: item.SelectedSubmissionIDs,
		})
	}
	views, err := s.tasks.BulkReviewTaskBatches(c.Request.Context(), user.ID, user.Role, requestID, *decision, comment, entries)
	if err != nil {
		writeServiceError(c, "批量审核", err)
		return
	}
	if views == nil {
		views = []repository.TaskView{}
	}
	c.JSON(http.StatusOK, views)
}

// ---- PATCH /tasks/{task_id}/submissions/{submission_id} ----

func (s *Server) handleUpdateSubmissionMetadata(c *gin.Context) {
	user := userFromContext(c)
	raw, err := readJSONObject(c)
	if err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	var in repository.SubmissionMetadataUpdate
	if value, present, ok := bindNullableField(raw, "view_label"); ok {
		in.ViewLabel = value
		in.ViewLabelSet = present
	}
	if value, present, ok := bindNullableField(raw, "state_label"); ok {
		in.StateLabel = value
		in.StateLabelSet = present
	}
	if value, present, ok := bindNullableField(raw, "description"); ok {
		in.Description = value
		in.DescriptionSet = present
	}
	view, err := s.tasks.UpdateSubmissionMetadata(c.Request.Context(), user.ID, user.Role,
		c.Param("task_id"), c.Param("submission_id"), in)
	if err != nil {
		writeServiceError(c, "更新候选", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ---- DELETE /tasks/{task_id}/submissions/{submission_id} ----

func (s *Server) handleDeleteDraftSubmission(c *gin.Context) {
	user := userFromContext(c)
	out, err := s.tasks.DeleteDraftSubmission(c.Request.Context(), user.ID, user.Role,
		c.Param("task_id"), c.Param("submission_id"))
	if err != nil {
		writeServiceError(c, "删除草稿候选", err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// ---- POST /tasks/{task_id}/submission-batches/{batch_id}/review ----

func (s *Server) handleReviewTaskBatch(c *gin.Context) {
	user := userFromContext(c)
	raw, err := readJSONObject(c)
	if err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	decision, present, ok := bindNullableField(raw, "decision")
	if !ok || !present || decision == nil || (*decision != "approve" && *decision != "rework") {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	primaryID, _, _ := bindNullableField(raw, "primary_submission_id")
	comment, _, _ := bindNullableField(raw, "comment")
	requestIDRaw, _, _ := bindNullableField(raw, "request_id")
	var requestID *string
	if requestIDRaw != nil && *requestIDRaw != "" {
		normalized, ok := normalizeUUIDString(*requestIDRaw)
		if !ok {
			unprocessableEntity(c, "请求体格式错误")
			return
		}
		requestID = &normalized
	}
	selectedRaw, ok := raw["selected_submission_ids"]
	if !ok {
		selectedRaw = nil
	}
	var selected []string
	if selectedRaw != nil {
		if err := json.Unmarshal(selectedRaw, &selected); err != nil {
			unprocessableEntity(c, "请求体格式错误")
			return
		}
	}
	view, err := s.tasks.ReviewTaskBatch(c.Request.Context(), user.ID, user.Role,
		c.Param("task_id"), c.Param("batch_id"),
		repository.TaskReviewInput{
			Decision: *decision, PrimarySubmissionID: primaryID,
			SelectedSubmissionIDs: selected, Comment: comment, RequestID: requestID,
		})
	if err != nil {
		writeServiceError(c, "审核任务", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ---- PATCH /tasks/{task_id}/submissions/{submission_id}/archive ----

func (s *Server) handleArchiveSubmission(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		Archived *bool `json:"archived"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	archived := true
	if payload.Archived != nil {
		archived = *payload.Archived
	}
	view, err := s.tasks.SetSubmissionArchived(c.Request.Context(), user.ID, user.Role,
		c.Param("task_id"), c.Param("submission_id"), archived)
	if err != nil {
		writeServiceError(c, "归档候选", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ---- PATCH /tasks/{task_id}/primary-submission ----

func (s *Server) handleChangePrimarySubmission(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		SubmissionID string `json:"submission_id"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if payload.SubmissionID == "" {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	view, err := s.tasks.SetPrimarySubmission(c.Request.Context(), user.ID, user.Role,
		c.Param("task_id"), payload.SubmissionID)
	if err != nil {
		writeServiceError(c, "指定主母版", err)
		return
	}
	c.JSON(http.StatusOK, view)
}

// ---- GET /workspace/tasks ----

func (s *Server) handleGetWorkspaceTasks(c *gin.Context) {
	user := userFromContext(c)
	payload, err := s.tasks.Workspace(c.Request.Context(), user.ID, user.Role,
		strPtrFromQuery(c.Query("assignee_id")))
	if err != nil {
		writeServiceError(c, "任务工作台", err)
		return
	}
	c.JSON(http.StatusOK, payload)
}

// ---- GET /workspace/reviews ----

func (s *Server) handleGetReviewWorkspace(c *gin.Context) {
	user := userFromContext(c)
	payload, err := s.tasks.ReviewWorkspace(c.Request.Context(), user.ID, user.Role)
	if err != nil {
		writeServiceError(c, "导演审核工作台", err)
		return
	}
	c.JSON(http.StatusOK, payload)
}

// ---- GET /projects/{project_id}/storyboards ----

func (s *Server) handleListProjectStoryboards(c *gin.Context) {
	user := userFromContext(c)
	items, err := s.tasks.ListProjectStoryboards(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), strPtrFromQuery(c.Query("episode_id")))
	if err != nil {
		writeServiceError(c, "项目分镜", err)
		return
	}
	if items == nil {
		items = []repository.StoryboardView{}
	}
	c.JSON(http.StatusOK, items)
}

// ---- GET /projects/{project_id}/board ----

func (s *Server) handleGetProductionBoard(c *gin.Context) {
	user := userFromContext(c)
	rows, err := s.tasks.GetProductionBoard(c.Request.Context(), user.ID, user.Role, c.Param("project_id"))
	if err != nil {
		writeServiceError(c, "生产看板", err)
		return
	}
	if rows == nil {
		rows = []repository.ProductionBoardRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// ---- GET /projects/{project_id}/scene-gating ----

func (s *Server) handleGetSceneGating(c *gin.Context) {
	user := userFromContext(c)
	rows, err := s.tasks.SceneGating(c.Request.Context(), user.ID, user.Role, c.Param("project_id"))
	if err != nil {
		writeServiceError(c, "场景解锁", err)
		return
	}
	if rows == nil {
		rows = []repository.SceneGatingRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// ---- POST /projects/{project_id}/storyboard-video-tasks（201） ----

func (s *Server) handleCreateStoryboardVideoTasks(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		EpisodeCode *string `json:"episode_code"`
		SceneCode   *string `json:"scene_code"`
		SceneName   *string `json:"scene_name"`
		AssigneeID  *string `json:"assignee_id"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	result, err := s.tasks.CreateStoryboardVideoTasks(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"),
		repository.StoryboardVideoTaskCreate{
			EpisodeCode: payload.EpisodeCode, SceneCode: payload.SceneCode,
			SceneName: payload.SceneName, AssigneeID: payload.AssigneeID,
		})
	if err != nil {
		writeServiceError(c, "创建分镜视频任务", err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// ---- 生成器：POST /projects/{project_id}/asset-tasks（201） ----

func (s *Server) handleGenerateAssetTasks(c *gin.Context) {
	user := userFromContext(c)
	episodeID := c.Query("episode_id")
	scriptVersionID := c.Query("script_version_id")
	if episodeID == "" || scriptVersionID == "" {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	result, err := s.tasks.GenerateAssetTasks(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), episodeID, scriptVersionID)
	if err != nil {
		writeServiceError(c, "生成资产任务", err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// ---- 生成器：POST /projects/{project_id}/storyboard-tasks（201） ----

func (s *Server) handleGenerateStoryboardTasks(c *gin.Context) {
	user := userFromContext(c)
	episodeID := c.Query("episode_id")
	scriptVersionID := c.Query("script_version_id")
	if episodeID == "" || scriptVersionID == "" {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	result, err := s.tasks.GenerateStoryboardTasks(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), episodeID, scriptVersionID)
	if err != nil {
		writeServiceError(c, "生成分镜任务", err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// ---- POST /projects/{project_id}/task-assignments ----

func (s *Server) handleBulkAssignTasks(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		Scope              string  `json:"scope"`
		Key                string  `json:"key"`
		AssigneeID         *string `json:"assignee_id"`
		EpisodeID          *string `json:"episode_id"`
		ReassignmentReason *string `json:"reassignment_reason"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	result, err := s.tasks.BulkAssignTasks(c.Request.Context(), user.ID, user.Role, c.Param("project_id"),
		repository.BulkAssignType{
			Scope: payload.Scope, Key: payload.Key, AssigneeID: payload.AssigneeID,
			EpisodeID: payload.EpisodeID, ReassignmentReason: payload.ReassignmentReason,
		})
	if err != nil {
		writeServiceError(c, "批量改派", err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// ---- body 解析助手（对齐 Pydantic exclude_unset / 必填字段语义） ----

// readJSONObject 读取请求体并顶层展开为 map（保留字段缺省区分）。
func readJSONObject(c *gin.Context) (map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// bindNullableField 解析字段：返回 (值, 是否显式提供, 是否解析成功)。
// 缺省 → (nil, false, true)；显式 null → (nil, true, true)；类型不匹配 → (nil, false, false)。
func bindNullableField(raw map[string]json.RawMessage, key string) (*string, bool, bool) {
	value, ok := raw[key]
	if !ok {
		return nil, false, true
	}
	if string(value) == "null" {
		return nil, true, true
	}
	var s string
	if err := json.Unmarshal(value, &s); err != nil {
		return nil, false, false
	}
	return &s, true, true
}

// requiredStringField 要求字段存在、非 null 且非空字符串。
func requiredStringField(raw map[string]json.RawMessage, key string) (string, error) {
	value, present, ok := bindNullableField(raw, key)
	if !ok || !present || value == nil || *value == "" {
		return "", errFieldMissing
	}
	return *value, nil
}

var errFieldMissing = &struct{ error }{}

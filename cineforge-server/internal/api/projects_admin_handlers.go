package api

// P3d 回补 handlers：soft-delete / script-segments confirm / breakdowns lock /
// episode hard delete（对齐 legacy app/api/routes/projects.py 对应路径）。

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"cineforge/server/internal/service"
)

// ---- /projects/{id}/soft-delete ----

// ProjectAdminActionRequest 对齐 legacy ProjectAdminActionRequest（admin_password + reason）。
type ProjectAdminActionRequest struct {
	AdminPassword string  `json:"admin_password"`
	Reason        *string `json:"reason"`
}

func (s *Server) handleSoftDeleteProject(c *gin.Context) {
	user := userFromContext(c)
	var payload ProjectAdminActionRequest
	if err := c.ShouldBindJSON(&payload); err != nil || payload.AdminPassword == "" {
		unprocessableEntity(c, "请提供管理员密码")
		return
	}
	if payload.AdminPassword != "" && len(payload.AdminPassword) > 64 {
		badRequest400(c, "admin_password 长度需在 1-64 之间")
		return
	}
	item, err := s.projects.SoftDeleteProject(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), payload.AdminPassword, payload.Reason)
	if err != nil {
		writeServiceError(c, "软删项目", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// ---- /projects/{id}/script-segments/confirm ----

func (s *Server) handleConfirmScriptSegments(c *gin.Context) {
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
	item, err := s.projects.ConfirmScriptSegments(c.Request.Context(), user.ID, user.Role, c.Param("project_id"), req)
	if err != nil {
		writeServiceError(c, "确认脚本段", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// ---- /projects/{id}/breakdowns/lock ----

func (s *Server) handleLockBreakdown(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		View             map[string]any `json:"view"`
		EpisodeCode      *string        `json:"episode_code"`
		EpisodeID        *string        `json:"episode_id"`
		ScriptVersionID  *string        `json:"script_version_id"`
		ChangeSummary    []string       `json:"change_summary"`
		SourceLabel      *string        `json:"source_label"`
		DataState        *string        `json:"data_state"`
		ExpectedVersion  *int32         `json:"expected_version"`
		CreateAssetTasks bool           `json:"create_asset_tasks"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	req := service.BreakdownLockRequest{
		BreakdownSaveRequest: service.BreakdownSaveRequest{
			View: payload.View, EpisodeCode: payload.EpisodeCode, EpisodeID: payload.EpisodeID,
			ScriptVersionID: payload.ScriptVersionID, ChangeSummary: payload.ChangeSummary,
			ExpectedVersion: payload.ExpectedVersion,
		},
		CreateAssetTasks: payload.CreateAssetTasks,
	}
	if payload.SourceLabel != nil {
		req.SourceLabel = *payload.SourceLabel
	}
	if payload.DataState != nil {
		req.DataState = *payload.DataState
	}
	item, err := s.projects.LockBreakdownEpisode(c.Request.Context(), user.ID, user.Role, c.Param("project_id"), req)
	if err != nil {
		writeServiceError(c, "锁定分镜拆解", err)
		return
	}
	c.JSON(http.StatusOK, item)
}

// ---- DELETE /projects/{id}/episodes/{episode_id} ----

func (s *Server) handleDeleteProjectEpisode(c *gin.Context) {
	user := userFromContext(c)
	var payload struct {
		ConfirmEpisodeCode string `json:"confirm_episode_code"`
		DeleteFiles        *bool  `json:"delete_files"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	deleteFiles := true
	if payload.DeleteFiles != nil {
		deleteFiles = *payload.DeleteFiles
	}
	item, err := s.projects.HardDeleteProjectEpisode(c.Request.Context(), user.ID, user.Role,
		c.Param("project_id"), c.Param("episode_id"), payload.ConfirmEpisodeCode, deleteFiles)
	if err != nil {
		writeServiceError(c, "删除分集", err)
		return
	}
	c.JSON(http.StatusOK, item)
}
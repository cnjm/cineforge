package api

// /api/users/* 端点（对齐 legacy app/api/routes/admin.py，全部要求 director/admin）：
//
//   - GET  /users                     → UserRead[]
//   - POST /users                     → 201 UserRead（409 手机号已存在）
//   - PATCH /users/{id}/status        → UserRead（404 User not found）
//   - POST /users/{id}/reset-password → {user, temporary_password}（404）

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"cineforge/server/internal/service"
)

// validRoles 全局角色枚举（对齐 users.role enum）。
var validRoles = map[string]bool{
	"director": true, "script_editor": true, "artist": true, "editor": true, "admin": true,
}

type createUserRequest struct {
	Phone       string  `json:"phone"`
	Name        *string `json:"name"`
	DisplayName *string `json:"display_name"`
	Username    *string `json:"username"`
	Role        string  `json:"role"`
	Password    *string `json:"password"`
	IsActive    *bool   `json:"is_active"`
}

type setUserStatusRequest struct {
	IsActive bool `json:"is_active"`
}

type resetPasswordRequest struct {
	Password *string `json:"password"`
}

func (s *Server) registerUsers(g *gin.RouterGroup) {
	u := g.Group("/users", s.requireAuth(), s.requireDirectorOrAdmin())
	u.GET("", s.handleListUsers)
	u.POST("", s.handleCreateUser)
	u.PATCH("/:id/status", s.handleSetUserStatus)
	u.POST("/:id/reset-password", s.handleResetPassword)
}

func (s *Server) handleListUsers(c *gin.Context) {
	users, err := s.userSvc.List(c.Request.Context())
	if err != nil {
		internalError(c, "加载成员列表失败")
		return
	}
	c.JSON(http.StatusOK, users)
}

func (s *Server) handleCreateUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	phone := strings.TrimSpace(req.Phone)
	if len(phone) < 6 || len(phone) > 20 {
		unprocessableEntity(c, "phone 长度需在 6-20 之间")
		return
	}
	if !validRoles[req.Role] {
		unprocessableEntity(c, "role 必须是 director/script_editor/artist/editor/admin")
		return
	}
	if req.Password != nil && (len(*req.Password) < 6 || len(*req.Password) > 64) {
		unprocessableEntity(c, "password 长度需在 6-64 之间")
		return
	}
	if req.Name != nil && (len(*req.Name) > 80) {
		unprocessableEntity(c, "name 长度需在 1-80 之间")
		return
	}
	if req.DisplayName != nil && (len(*req.DisplayName) > 80) {
		unprocessableEntity(c, "display_name 长度需在 1-80 之间")
		return
	}

	in := service.UserCreateInput{
		Phone:    phone,
		Role:     req.Role,
		IsActive: true,
	}
	if req.Name != nil {
		in.Name = *req.Name
	}
	if req.DisplayName != nil {
		in.DisplayName = *req.DisplayName
	}
	if req.Username != nil {
		in.Username = *req.Username
	}
	if req.Password != nil {
		in.Password = *req.Password
	}
	if req.IsActive != nil {
		in.IsActive = *req.IsActive
	}

	view, err := s.userSvc.Create(c.Request.Context(), in)
	switch {
	case err == nil:
		c.JSON(http.StatusCreated, view)
	case errors.Is(err, service.ErrPhoneExists):
		errorJSON(c, http.StatusConflict, "手机号已存在")
	default:
		internalError(c, "创建成员失败")
	}
}

func (s *Server) handleSetUserStatus(c *gin.Context) {
	id := c.Param("id")
	var req setUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	view, err := s.userSvc.SetActive(c.Request.Context(), id, req.IsActive)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, view)
	case errors.Is(err, service.ErrUserNotFound):
		notFound404(c, service.ErrUserNotFound.Error())
	default:
		internalError(c, "账号状态更新失败")
	}
}

func (s *Server) handleResetPassword(c *gin.Context) {
	id := c.Param("id")
	req := resetPasswordRequest{}
	if err := c.ShouldBindJSON(&req); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if req.Password != nil && (len(*req.Password) < 6 || len(*req.Password) > 64) {
		unprocessableEntity(c, "password 长度需在 6-64 之间")
		return
	}

	password := ""
	if req.Password != nil {
		password = *req.Password
	}
	result, err := s.userSvc.ResetPassword(c.Request.Context(), id, password)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, result)
	case errors.Is(err, service.ErrUserNotFound):
		notFound404(c, service.ErrUserNotFound.Error())
	default:
		internalError(c, "重置密码失败")
	}
}

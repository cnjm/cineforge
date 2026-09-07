package api

// /api/auth/* 端点（对齐 legacy app/api/routes/auth.py）：
//
//   - POST /auth/login             → 201 语义伪 200：AuthResponse
//   - GET  /auth/me                → UserRead
//   - POST /auth/verify-password   → {"ok": true} / 401 当前账号密码错误
//   - POST /auth/password          → UserRead / 401 当前账号密码错误

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"cineforge/server/internal/service"
)

type loginRequest struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

type verifyPasswordRequest struct {
	Password string `json:"password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) registerAuth(g *gin.RouterGroup) {
	a := g.Group("/auth")
	a.POST("/login", s.handleLogin)
	authed := a.Group("", s.requireAuth())
	authed.GET("/me", s.handleMe)
	authed.POST("/verify-password", s.handleVerifyPassword)
	authed.POST("/password", s.handleChangePassword)
}

func (s *Server) handleLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	phone := strings.TrimSpace(req.Phone)
	if len(phone) < 6 || len(phone) > 20 {
		unprocessableEntity(c, "phone 长度需在 6-20 之间")
		return
	}
	if len(req.Password) < 1 || len(req.Password) > 64 {
		unprocessableEntity(c, "password 长度需在 1-64 之间")
		return
	}

	ctx := c.Request.Context()
	result, err := s.authSvc.Login(ctx, phone, req.Password)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, result)
	case errors.Is(err, service.ErrInvalidCredentials):
		unauthorized401(c, service.ErrInvalidCredentials.Error())
	case errors.Is(err, service.ErrLoginUnavailable):
		internalError(c, service.ErrLoginUnavailable.Error())
	case errors.Is(err, service.ErrSyncUnavailable):
		internalError(c, service.ErrSyncUnavailable.Error())
	case errors.Is(err, service.ErrTokenIssue):
		internalError(c, service.ErrTokenIssue.Error())
	default:
		internalError(c, "登录失败")
	}
}

func (s *Server) handleMe(c *gin.Context) {
	user := userFromContext(c)
	view := service.ToUserView(user)
	c.JSON(http.StatusOK, view)
}

func (s *Server) handleVerifyPassword(c *gin.Context) {
	var req verifyPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if len(req.Password) < 1 || len(req.Password) > 64 {
		unprocessableEntity(c, "password 长度需在 1-64 之间")
		return
	}
	user := userFromContext(c)
	err := s.authSvc.VerifyPassword(c.Request.Context(), user.ID, req.Password)
	if errors.Is(err, service.ErrPasswordWrong) {
		unauthorized401(c, service.ErrPasswordWrong.Error())
		return
	}
	if err != nil {
		internalError(c, "密码校验失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handleChangePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if len(req.CurrentPassword) < 1 || len(req.CurrentPassword) > 64 {
		unprocessableEntity(c, "current_password 长度需在 1-64 之间")
		return
	}
	if len(req.NewPassword) < 6 || len(req.NewPassword) > 64 {
		unprocessableEntity(c, "new_password 长度需在 6-64 之间")
		return
	}
	user := userFromContext(c)
	err := s.authSvc.ChangePassword(c.Request.Context(), user.ID, req.CurrentPassword, req.NewPassword)
	if errors.Is(err, service.ErrPasswordWrong) {
		unauthorized401(c, service.ErrPasswordWrong.Error())
		return
	}
	if err != nil {
		internalError(c, "密码修改失败")
		return
	}
	c.JSON(http.StatusOK, service.ToUserView(user))
}

package api

// auth 中间件与用户身份注入（对齐 legacy FastAPI HTTPBearer + get_current_user）：
//
//   - 解析 Authorization: Bearer <access_token>（2-part 自定义格式）
//   - 无效 → 401 "Invalid token"，过期 → 401 "Token expired"
//   - 用户不存在/停用 → 401 "User disabled or not found"
//   - requireDirectorOrAdmin → 403 "Director or admin permission required"

import (
	"strings"

	"github.com/gin-gonic/gin"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/model"
)

const (
	contextUserKey = "current_user"
)

// userFromContext 从 gin context 取当前用户（必须经过 requireAuth）。
func userFromContext(c *gin.Context) *model.Users {
	if v, ok := c.Get(contextUserKey); ok {
		if u, ok2 := v.(*model.Users); ok2 {
			return u
		}
	}
	return nil
}

// requireAuth 验证 Bearer access_token 并注入用户。
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			unauthorized401(c, "Not authenticated")
			return
		}
		token := strings.TrimPrefix(header, "Bearer ")
		claims, err := auth.VerifyAccessToken(s.cfg.Auth.Secret, token)
		if err != nil {
			unauthorized401(c, err.Error()) // "Invalid token" / "Token expired"
			return
		}
		user, err := s.users.FindByID(c.Request.Context(), claims.Sub)
		if err != nil {
			internalError(c, "无法加载用户信息")
			return
		}
		if user == nil || !user.IsActive {
			unauthorized401(c, "User disabled or not found")
			return
		}
		c.Set(contextUserKey, user)
		c.Next()
	}
}

// requireDirectorOrAdmin 仅 director / admin 可访问。
func (s *Server) requireDirectorOrAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := userFromContext(c)
		if user == nil || (user.Role != "director" && user.Role != "admin") {
			forbidden403(c, "Director or admin permission required")
			return
		}
		c.Next()
	}
}

package service

// auth 域业务服务：登录、令牌签发、me、密码校验与修改。
// 单库 monolith：登录时不向 system 投影同步（users 即 master），
// legacy 的 "平台账号同步暂不可用" 因此不再出现，但 detail 文案保留在
// ErrSyncUnavailable 中以维持契约引用完整性。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/config"
	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
)

// sentinel 错误 → handler 映射 HTTP 状态（detail 逐字对齐 legacy）。
var (
	ErrLoginUnavailable                = errors.New("登录服务暂不可用，请确认后端数据库已启动并且 DATABASE_URL 可连接")
	ErrInvalidCredentials              = errors.New("手机号或密码错误")
	ErrSyncUnavailable                 = errors.New("平台账号同步暂不可用")
	ErrTokenIssue                      = errors.New("登录令牌签发暂不可用")
	ErrUserNotFound                    = errors.New("User not found")
	ErrPasswordWrong                   = errors.New("当前账号密码错误")
	ErrDirectorOrAdminRequired         = errors.New("Director or admin permission required")
	ErrProjectPermissionDenied         = errors.New("Project permission denied")
	ErrPhoneExists                     = repository.ErrPhoneExists
	ErrAdminPasswordVerificationFailed = errors.New("Admin password verification failed")
)

// UserView 是对外 user 表示（不泄漏 password_hash / sync_version）。
type UserView struct {
	ID          string  `json:"id"`
	Username    string  `json:"username"`
	Phone       *string `json:"phone"`
	Name        *string `json:"name"`
	DisplayName string  `json:"display_name"`
	Role        string  `json:"role"`
	IsActive    bool    `json:"is_active"`
}

// ToUserView 转换 model.Users → UserView。
func ToUserView(u *model.Users) UserView {
	return UserView{
		ID:          u.ID,
		Username:    u.Username,
		Phone:       u.Phone,
		Name:        u.Name,
		DisplayName: u.DisplayName,
		Role:        u.Role,
		IsActive:    u.IsActive,
	}
}

// LoginResult 登录成功响应（对齐 AuthResponse）。
type LoginResult struct {
	AccessToken      string   `json:"access_token"`
	TokenType        string   `json:"token_type"`
	User             UserView `json:"user"`
	TapcanvasToken   string   `json:"tapcanvas_token"`
	PlatformToken    string   `json:"platform_token"`
	SessionExpiresAt int64    `json:"session_expires_at"`
}

// AuthService 承载登录、会话签发与自身密码管理。
type AuthService struct {
	users *repository.Users
	cfg   *config.Config
}

// NewAuthService 创建 AuthService。
func NewAuthService(users *repository.Users, cfg *config.Config) *AuthService {
	return &AuthService{users: users, cfg: cfg}
}

// Me 按 ID 取当前用户；不存在/停用返回 ErrUserNotFound（映射 401 用户停用/不存在）。
func (s *AuthService) Me(ctx context.Context, userID string) (*UserView, error) {
	u, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("me: %w", err)
	}
	if u == nil {
		return nil, ErrUserNotFound
	}
	view := ToUserView(u)
	return &view, nil
}

// Login 主登录：查用户 → 密码校验 → 签发三 token 会话。
func (s *AuthService) Login(ctx context.Context, phone, password string) (*LoginResult, error) {
	user, err := s.users.FindByPhone(ctx, strings.TrimSpace(phone))
	if err != nil {
		return nil, ErrLoginUnavailable
	}
	// （单库）登录前系统投影 sync → no-op
	if user == nil || !user.IsActive {
		return nil, ErrInvalidCredentials
	}
	hash := ""
	if user.PasswordHash != nil {
		hash = *user.PasswordHash
	}
	if !auth.VerifyPassword(hash, password) {
		return nil, ErrInvalidCredentials
	}
	result, err := s.issueTokens(user)
	if err != nil {
		return nil, ErrTokenIssue
	}
	return result, nil
}

func (s *AuthService) issueTokens(user *model.Users) (*LoginResult, error) {
	ttl := s.cfg.Auth.SessionTTLSeconds
	if ttl <= 0 || ttl > auth.MaxSessionSeconds {
		return nil, ErrTokenIssue
	}
	if s.cfg.Auth.Secret == "" || s.cfg.Auth.TapcanvasJWTSecret == "" || s.cfg.Auth.PlatformJWTPrivateKey == "" {
		return nil, ErrTokenIssue
	}
	now := time.Now().Unix()
	exp := now + int64(ttl)

	access := auth.SignAccessToken(s.cfg.Auth.Secret, user.ID, user.Role, exp)
	tap, err := auth.SignTapcanvasToken(s.cfg.Auth.TapcanvasJWTSecret, user.ID,
		user.Username, user.DisplayName, user.Role, now, exp)
	if err != nil {
		return nil, fmt.Errorf("sign tapcanvas token: %w", err)
	}
	platform, err := auth.SignPlatformToken(&s.cfg.Auth, user.ID, now, exp)
	if err != nil {
		return nil, fmt.Errorf("sign platform token: %w", err)
	}
	view := ToUserView(user)
	return &LoginResult{
		AccessToken:      access,
		TokenType:        "bearer",
		User:             view,
		TapcanvasToken:   tap,
		PlatformToken:    platform,
		SessionExpiresAt: exp,
	}, nil
}

// VerifyPassword 校验当前账号密码（错误 → ErrPasswordWrong）。
func (s *AuthService) VerifyPassword(ctx context.Context, userID, password string) error {
	u, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("verify password: %w", err)
	}
	if u == nil {
		return ErrUserNotFound
	}
	hash := ""
	if u.PasswordHash != nil {
		hash = *u.PasswordHash
	}
	if !auth.VerifyPassword(hash, password) {
		return ErrPasswordWrong
	}
	return nil
}

// ChangePassword 修改自身密码：current 校验失败 → ErrPasswordWrong。
func (s *AuthService) ChangePassword(ctx context.Context, userID, current, newPassword string) error {
	if err := s.VerifyPassword(ctx, userID, current); err != nil {
		return err
	}
	newHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}
	if _, err := s.users.SetPasswordHash(ctx, userID, newHash); err != nil {
		return fmt.Errorf("save new password: %w", err)
	}
	return nil
}

package service

// user 管理业务服务（仅 director/admin）：列表、创建、启停、重置密码。
// 对齐 legacy app/api/routes/admin.py + app/services/repositories.py 的创建规则。

import (
	"context"
	"fmt"
	"strings"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
)

// UserCreateInput 已通过校验的创建 input。
type UserCreateInput struct {
	Phone       string
	Name        string
	DisplayName string
	Username    string
	Role        string
	Password    string
	IsActive    bool
}

// ResetPasswordResult 重置密码响应（对齐 {user, temporary_password}）。
type ResetPasswordResult struct {
	User              UserView `json:"user"`
	TemporaryPassword string   `json:"temporary_password"`
}

// UserService 承载成员管理。
type UserService struct {
	users *repository.Users
}

// NewUserService 创建 UserService。
func NewUserService(users *repository.Users) *UserService {
	return &UserService{users: users}
}

// List 返回全部成员（前端按 created_at 展示）。
func (s *UserService) List(ctx context.Context) ([]UserView, error) {
	all, err := s.users.List(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]UserView, 0, len(all))
	for i := range all {
		views = append(views, ToUserView(&all[i]))
	}
	return views, nil
}

// Create 创建成员。create 规则（legacy）：
//   - username = phone（未提供时）
//   - name = (name or display_name or phone).strip()；display_name = name
//   - password 未提供时默认 phone 后 6 位
func (s *UserService) Create(ctx context.Context, in UserCreateInput) (*UserView, error) {
	phone := strings.TrimSpace(in.Phone)

	id, err := auth.NewUUID()
	if err != nil {
		return nil, fmt.Errorf("create user id: %w", err)
	}

	username := strings.TrimSpace(in.Username)
	if username == "" {
		username = phone
	}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = strings.TrimSpace(in.DisplayName)
	}
	if name == "" {
		name = phone
	}
	displayName := name

	// 唯一约束冲突在 repository 层转 ErrPhoneExists
	phonePtr := phone
	namePtr := name
	pw := in.Password
	if pw == "" {
		pw = auth.DefaultPasswordForPhone(phone)
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	u := &model.Users{
		ID:           id,
		Username:     username,
		Phone:        &phonePtr,
		Name:         &namePtr,
		DisplayName:  displayName,
		Role:         in.Role,
		PasswordHash: &hash,
		IsActive:     in.IsActive,
		SyncVersion:  0,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	view := ToUserView(u)
	return &view, nil
}

// SetActive 启停账号（停用后其会话仍有效 hasta exp，启动时 get_current_user 拦截）。
func (s *UserService) SetActive(ctx context.Context, id string, active bool) (*UserView, error) {
	updated, err := s.users.SetActive(ctx, id, active)
	if err != nil {
		return nil, err
	}
	if !updated {
		return nil, ErrUserNotFound
	}
	u, err := s.users.FindByID(ctx, id)
	if err != nil || u == nil {
		return nil, fmt.Errorf("reload user after status change: %w", err)
	}
	view := ToUserView(u)
	return &view, nil
}

// ResetPassword 重置密码；password 为空时用"手机号（或 username）后 6 位"。
func (s *UserService) ResetPassword(ctx context.Context, id, password string) (*ResetPasswordResult, error) {
	u, err := s.users.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrUserNotFound
	}

	effective := password
	if effective == "" {
		if u.Phone != nil && *u.Phone != "" {
			effective = auth.DefaultPasswordForPhone(*u.Phone)
		} else {
			effective = auth.DefaultPasswordForPhone(u.Username)
		}
	}
	hash, err := auth.HashPassword(effective)
	if err != nil {
		return nil, fmt.Errorf("hash reset password: %w", err)
	}
	updated, err := s.users.SetPasswordHash(ctx, id, hash)
	if err != nil {
		return nil, fmt.Errorf("save reset password: %w", err)
	}
	if !updated {
		return nil, ErrUserNotFound
	}
	view := ToUserView(u)
	return &ResetPasswordResult{User: view, TemporaryPassword: effective}, nil
}

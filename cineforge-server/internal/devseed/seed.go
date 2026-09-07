package devseed

// 开发账号自动种子（dev-only，配置门控 + 幂等）。
//
// 目的：新拉代码、启动本地后端后无需手工造号即可登录联调。
// 行为：CINEFORGE_DEVSEED_ENABLED=true 且 phone/password 非空时，按 phone
// 查重，不存在则用与成员管理一致的规则（PBKDF2 + uuid）创建；已存在则跳过。
// 生产环境保持 Enabled=false，本包不执行任何操作。

import (
	"context"
	"fmt"
	"log/slog"

	"cineforge/server/internal/config"
	"cineforge/server/internal/repository"
	"cineforge/server/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureDevUser 保证开发账号存在；返回错误时调用方记日志但不阻断启动。
func EnsureDevUser(ctx context.Context, pool *pgxpool.Pool, cfg config.DevSeedConfig, logger *slog.Logger) error {
	if !cfg.Enabled || cfg.Phone == "" || cfg.Password == "" {
		return nil
	}

	users := repository.NewUsers(pool)
	svc := service.NewUserService(users)

	existing, err := users.FindByPhone(ctx, cfg.Phone)
	if err != nil {
		return fmt.Errorf("devseed check existing: %w", err)
	}
	if existing != nil {
		if !existing.IsActive {
			logger.Warn("devseed: dev account exists but is disabled", "phone", cfg.Phone)
			return nil
		}
		logger.Info("devseed: dev account already exists, skip",
			"phone", cfg.Phone, "username", existing.Username, "role", existing.Role)
		return nil
	}

	username := cfg.Username
	if username == "" {
		username = cfg.Phone
	}
	display := cfg.DisplayName
	if display == "" {
		display = "开发账号"
	}
	_, err = svc.Create(ctx, service.UserCreateInput{
		Phone:       cfg.Phone,
		Username:    username,
		DisplayName: display,
		Role:        cfg.Role,
		Password:    cfg.Password,
		IsActive:    true,
	})
	if err != nil {
		return fmt.Errorf("devseed create: %w", err)
	}
	logger.Info("devseed: dev account created",
		"phone", cfg.Phone, "username", username, "role", cfg.Role)
	return nil
}